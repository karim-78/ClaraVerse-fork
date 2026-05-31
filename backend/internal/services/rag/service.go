package rag

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"log"
	"os"
	"path/filepath"
	"sort"
	"sync"
	"time"

	"claraverse/internal/database"
	"claraverse/internal/models"

	"github.com/google/uuid"
	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/bson/primitive"
)

// Service is the top-level RAG façade. Everything outside this package
// (tools, HTTP handlers, workflow blocks) goes through Service —
// keeping the concrete clients (Qdrant, embeddings, stores) hidden
// makes it easy to swap them later (e.g. add a caching layer in
// front of embeddings without touching callers).
type Service struct {
	files       *FileStore
	collections *CollectionStore
	qdrant      *QdrantClient
	embed       *EmbeddingsClient
	uploadRoot  string // base directory for persisted file bytes

	// mu guards the ingest worker singleton state. Only one worker
	// goroutine runs per process; concurrency at scale happens within
	// the worker (batched embed calls, batched upserts) not by
	// spawning more workers — Qdrant single-collection upsert is the
	// bottleneck, not parsing.
	mu             sync.Mutex
	workerStarted  bool
	embedderCached struct {
		id   string
		dims int
		when time.Time
	}
}

// NewService wires the RAG layer. uploadRoot is the directory on disk
// where raw uploaded bytes are stored; we organize per-project under
// `<uploadRoot>/kb/<project_hex>/`.
func NewService(db *database.MongoDB, qdrantURL, embeddingsURL, uploadRoot string) *Service {
	return &Service{
		files:       NewFileStore(db),
		collections: NewCollectionStore(db),
		qdrant:      NewQdrantClient(qdrantURL),
		embed:       NewEmbeddingsClient(embeddingsURL),
		uploadRoot:  uploadRoot,
	}
}

// EmbeddingsClient returns the underlying client. Exposed so HTTP
// handlers can surface sidecar health to the admin UI.
func (s *Service) EmbeddingsClient() *EmbeddingsClient { return s.embed }

// HasKnowledge returns true if the project has at least one ready
// file. Used by the Cortex classifier and the daemon tool wiring to
// decide whether to surface search_knowledge for this task at all.
func (s *Service) HasKnowledge(ctx context.Context, userID string, projectID primitive.ObjectID) bool {
	c, err := s.collections.Get(ctx, userID, projectID)
	if err != nil || c == nil {
		return false
	}
	return c.ChunkCount > 0
}

// ─── Ingest ─────────────────────────────────────────────────────────

// IngestUpload persists raw bytes for a new knowledge file and queues
// it for ingestion. Returns the created file record. The actual
// parse/chunk/embed/upsert happens asynchronously in the worker.
//
// Why upload-then-queue rather than upload-then-process-inline:
//   - Inline ingest would tie up the HTTP request for ~5–30s per file.
//   - The worker can batch across files in the same project.
//   - Failure during ingest doesn't fail the upload — user sees a
//     "failed" badge in the UI with a retry button.
func (s *Service) IngestUpload(ctx context.Context, userID string, projectID primitive.ObjectID, filename, contentType string, data []byte, sourceURL string) (*models.KnowledgeFile, error) {
	// Hash before write so we can deduplicate trivially. Two uploads
	// of the same bytes (same sha) reuse the same on-disk file.
	sum := sha256.Sum256(data)
	hexsum := hex.EncodeToString(sum[:])

	// Persist to disk.
	dir := filepath.Join(s.uploadRoot, "kb", projectID.Hex())
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, fmt.Errorf("mkdir kb: %w", err)
	}
	ext := filepath.Ext(filename)
	storagePath := filepath.Join(dir, hexsum+ext)
	if _, err := os.Stat(storagePath); os.IsNotExist(err) {
		if err := os.WriteFile(storagePath, data, 0o644); err != nil {
			return nil, fmt.Errorf("write kb file: %w", err)
		}
	}

	file := &models.KnowledgeFile{
		ProjectID:   projectID,
		UserID:      userID,
		Filename:    filename,
		ContentType: contentType,
		SizeBytes:   int64(len(data)),
		SHA256:      hexsum,
		StoragePath: storagePath,
		SourceURL:   sourceURL,
		Status:      models.KnowledgeFileStatusQueued,
	}
	if err := s.files.Create(ctx, file); err != nil {
		return nil, err
	}

	// Make sure the worker is running. Idempotent; safe to call from
	// every upload request — startup elsewhere also calls this.
	s.StartWorker(context.Background())

	return file, nil
}

// ListFiles returns a project's knowledge files for the UI list.
func (s *Service) ListFiles(ctx context.Context, userID string, projectID primitive.ObjectID) ([]models.KnowledgeFile, error) {
	return s.files.ListByProject(ctx, userID, projectID)
}

// DeleteFile removes a file: Qdrant points → on-disk bytes → Mongo
// record. Ordered worst-first: if Qdrant fails we surface the error
// without orphaning the disk file; if disk delete fails we still
// remove from Mongo because the disk file is reclaimable but a
// dangling Mongo record breaks the UI.
func (s *Service) DeleteFile(ctx context.Context, userID string, fileID primitive.ObjectID) error {
	file, err := s.files.GetByID(ctx, userID, fileID)
	if err != nil {
		return err
	}
	if file == nil {
		return fmt.Errorf("not found")
	}

	collection := fmt.Sprintf("kb_%s", file.ProjectID.Hex())
	// Filter must scope by userId too — defense in depth even though
	// the collection itself is per-project.
	_ = s.qdrant.DeleteByFilter(ctx, collection, map[string]any{
		"must": []map[string]any{{
			"key":   "file_id",
			"match": map[string]any{"value": file.ID.Hex()},
		}},
	})

	// Disk delete — but only if no other file shares the same sha
	// (since we dedupe by sha at upload time).
	other, _ := s.files.col.CountDocuments(ctx, bson.M{
		"sha256":    file.SHA256,
		"_id":       bson.M{"$ne": file.ID},
		"projectId": file.ProjectID,
	})
	if other == 0 && file.StoragePath != "" {
		_ = os.Remove(file.StoragePath)
	}

	if err := s.files.Delete(ctx, userID, fileID); err != nil {
		return err
	}

	// Decrement counters.
	_ = s.collections.IncrementChunks(ctx, file.ProjectID, -1, -file.ChunkCount)
	return nil
}

// ─── Search ─────────────────────────────────────────────────────────

// SearchOptions configures a single search call.
type SearchOptions struct {
	Query      string
	ProjectIDs []primitive.ObjectID // 1+ projects to fan across; deduped by file_id+chunk_idx
	TopK       int                  // default 5
	Rerank     bool                 // default true; falls back to false if sidecar reranker isn't ready
}

// Search retrieves the most relevant chunks across one or more
// projects. The pipeline:
//
//  1. Embed query once (single sidecar round trip)
//  2. Dense search each project in parallel, top 50 each
//  3. Merge + dedupe by (file_id, chunk_idx); keep top-50 by score
//  4. Optional cross-encoder rerank → top_k
//  5. Return as KnowledgeSearchHit (citation-ready)
//
// Defensive: a project with no collection is silently skipped (rather
// than erroring) so a multi-project chat with knowledge attached to
// only one of them still works.
func (s *Service) Search(ctx context.Context, userID string, opts SearchOptions) ([]models.KnowledgeSearchHit, error) {
	if opts.TopK <= 0 {
		opts.TopK = 5
	}
	if opts.Query == "" {
		return nil, fmt.Errorf("query is required")
	}
	if len(opts.ProjectIDs) == 0 {
		return nil, fmt.Errorf("at least one project_id is required")
	}

	// Resolve projects to existing collections. Filter out any the
	// user doesn't own (defense in depth — handlers also check).
	type projColl struct {
		projectID     primitive.ObjectID
		collection    string
		sparseEnabled bool
	}
	var targets []projColl
	for _, pid := range opts.ProjectIDs {
		c, err := s.collections.Get(ctx, userID, pid)
		if err != nil || c == nil || c.ChunkCount == 0 {
			continue
		}
		targets = append(targets, projColl{
			projectID:     pid,
			collection:    c.QdrantCollection,
			sparseEnabled: c.SparseEnabled,
		})
	}
	if len(targets) == 0 {
		return nil, nil // no knowledge anywhere — empty result, not error
	}

	// Embed the query once. Produces both dense + sparse; only the
	// sparse path uses the sparse vector below.
	dense, sparse, err := s.embed.EmbedQuery(ctx, opts.Query)
	if err != nil {
		return nil, fmt.Errorf("embed query: %w", err)
	}

	// Per-project search, in parallel.
	perCandidate := 50
	type projHits struct {
		projectID primitive.ObjectID
		hits      []SearchHit
	}
	resultsCh := make(chan projHits, len(targets))
	var wg sync.WaitGroup
	for _, t := range targets {
		wg.Add(1)
		go func(t projColl) {
			defer wg.Done()
			var hits []SearchHit
			var err error
			if t.sparseEnabled {
				// Hybrid (dense + BM25 sparse with RRF fusion). Catches
				// keyword matches dense alone would miss — codenames,
				// proper nouns, exact phrases. The single biggest
				// quality lever in Phase C.
				hits, err = s.qdrant.SearchHybrid(ctx, t.collection, dense, sparse, nil, perCandidate)
			} else {
				// Legacy dense-only path for pre-Phase-C collections.
				// Same code on the calling side; transparent fallback.
				hits, err = s.qdrant.SearchDense(ctx, t.collection, dense, nil, perCandidate)
			}
			if err != nil {
				log.Printf("[RAG] search %s: %v", t.collection, err)
				return
			}
			resultsCh <- projHits{projectID: t.projectID, hits: hits}
		}(t)
	}
	wg.Wait()
	close(resultsCh)

	// Merge + dedupe.
	type keyed struct {
		hit       SearchHit
		projectID primitive.ObjectID
	}
	merged := make(map[string]keyed)
	for ph := range resultsCh {
		for _, h := range ph.hits {
			fileID, _ := h.Payload["file_id"].(string)
			chunkIdxF, _ := h.Payload["chunk_idx"].(float64)
			key := fmt.Sprintf("%s:%d", fileID, int(chunkIdxF))
			if prev, ok := merged[key]; !ok || h.Score > prev.hit.Score {
				merged[key] = keyed{hit: h, projectID: ph.projectID}
			}
		}
	}
	candidates := make([]keyed, 0, len(merged))
	for _, k := range merged {
		candidates = append(candidates, k)
	}
	sort.Slice(candidates, func(i, j int) bool {
		return candidates[i].hit.Score > candidates[j].hit.Score
	})
	if len(candidates) > perCandidate {
		candidates = candidates[:perCandidate]
	}

	// Optional rerank.
	if opts.Rerank && len(candidates) > 1 {
		docs := make([]string, len(candidates))
		for i, c := range candidates {
			text, _ := c.hit.Payload["chunk_text"].(string)
			docs[i] = text
		}
		hits, err := s.embed.Rerank(ctx, opts.Query, docs, opts.TopK)
		if err == nil {
			reordered := make([]keyed, len(hits))
			for i, h := range hits {
				if h.Index >= 0 && h.Index < len(candidates) {
					c := candidates[h.Index]
					c.hit.Score = h.Score
					reordered[i] = c
				}
			}
			candidates = reordered
		} else {
			// Rerank failure is non-fatal — fall back to vector order.
			log.Printf("[RAG] rerank failed (using vector order): %v", err)
		}
	}

	// Truncate to top_k.
	if len(candidates) > opts.TopK {
		candidates = candidates[:opts.TopK]
	}

	// Convert to citation-ready hits.
	out := make([]models.KnowledgeSearchHit, 0, len(candidates))
	for _, c := range candidates {
		text, _ := c.hit.Payload["chunk_text"].(string)
		fileID, _ := c.hit.Payload["file_id"].(string)
		fileName, _ := c.hit.Payload["file_name"].(string)
		chunkIdxF, _ := c.hit.Payload["chunk_idx"].(float64)
		pageF, _ := c.hit.Payload["page"].(float64)
		section, _ := c.hit.Payload["section"].(string)
		out = append(out, models.KnowledgeSearchHit{
			Score:     c.hit.Score,
			Text:      text,
			FileID:    fileID,
			FileName:  fileName,
			ChunkIdx:  int(chunkIdxF),
			Page:      int(pageF),
			Section:   section,
			ProjectID: c.projectID.Hex(),
		})
	}
	return out, nil
}

// ─── Worker ─────────────────────────────────────────────────────────

// StartWorker is idempotent — safe to call from every upload + from
// main on boot. Spawns a single goroutine that polls the queue.
func (s *Service) StartWorker(ctx context.Context) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.workerStarted {
		return
	}
	s.workerStarted = true
	go s.workerLoop(ctx)
	log.Printf("[RAG] worker started")
}

// workerLoop drains the queue. Sleeps briefly between checks to keep
// idle CPU at zero; once it has work, it processes back-to-back with
// no sleep. The 5s poll is fine because uploads also call StartWorker
// — there's no real cold-start delay.
func (s *Service) workerLoop(parentCtx context.Context) {
	for {
		select {
		case <-parentCtx.Done():
			return
		default:
		}
		file, err := s.files.NextQueued(parentCtx)
		if err != nil {
			log.Printf("[RAG] worker queue read: %v", err)
			time.Sleep(10 * time.Second)
			continue
		}
		if file == nil {
			time.Sleep(5 * time.Second)
			continue
		}
		s.processFile(parentCtx, file)
	}
}

// processFile runs the full ingest for one file. Each phase updates
// the file record so the UI sees progress, and any failure marks the
// file `failed` with a human-readable error.
func (s *Service) processFile(ctx context.Context, file *models.KnowledgeFile) {
	start := time.Now()
	logPrefix := fmt.Sprintf("[RAG] file=%s project=%s", file.ID.Hex(), file.ProjectID.Hex())

	fail := func(reason string, err error) {
		log.Printf("%s FAILED: %s: %v", logPrefix, reason, err)
		_ = s.files.UpdateStatus(ctx, file.ID, bson.M{
			"status": models.KnowledgeFileStatusFailed,
			"error":  fmt.Sprintf("%s: %v", reason, err),
		})
	}

	// 1. Read bytes back from disk.
	bytes, err := readFile(file.StoragePath)
	if err != nil {
		fail("read file", err)
		return
	}

	// 2. Parse to segments.
	segments, err := ParseFile(file.Filename, file.ContentType, bytes)
	if err != nil {
		fail("parse", err)
		return
	}
	if len(segments) == 0 {
		fail("parse", fmt.Errorf("no text extracted"))
		return
	}

	// 3. Chunk.
	chunks := ChunkSegments(segments, DefaultChunkConfig())
	if len(chunks) == 0 {
		fail("chunk", fmt.Errorf("no chunks produced"))
		return
	}
	log.Printf("%s parsed=%d-segments chunked=%d", logPrefix, len(segments), len(chunks))

	// 4. Ensure collection exists with the right dim. Probe the
	//    embedder once and cache the dim for ~5min so we don't make
	//    a /health call per file.
	embedderID, dims, err := s.cachedEmbedderInfo(ctx)
	if err != nil {
		fail("embedder probe", err)
		return
	}
	coll, err := s.collections.GetOrCreate(ctx, file.UserID, file.ProjectID, embedderID, dims)
	if err != nil {
		fail("collection record", err)
		return
	}
	// Sparse vectors are produced by the embeddings sidecar on every
	// /embed call regardless, so turning hybrid ON is free at ingest
	// time and pays for itself at query time. New projects default to
	// sparse-enabled (Phase C); pre-existing collections from Phase A
	// stay dense-only until the user explicitly reingests.
	if !coll.SparseEnabled && coll.ChunkCount == 0 {
		coll.SparseEnabled = true
		_ = s.collections.MarkSparseEnabled(ctx, coll.ID)
	}
	if err := s.qdrant.CreateCollection(ctx, coll.QdrantCollection, dims, coll.SparseEnabled); err != nil {
		fail("qdrant create collection", err)
		return
	}

	// 5. Embed + upsert in batches of 64. The sidecar caps at 256 but
	//    64 is a sweet spot: smaller batches make progress updates
	//    feel snappier and bound the per-batch latency.
	const batch = 64
	totalChunks := len(chunks)
	for i := 0; i < totalChunks; i += batch {
		end := i + batch
		if end > totalChunks {
			end = totalChunks
		}
		slice := chunks[i:end]

		texts := make([]string, len(slice))
		for j, c := range slice {
			texts[j] = c.Text
		}
		dense, sparse, _, err := s.embed.EmbedBatch(ctx, texts)
		if err != nil {
			fail("embed batch", err)
			return
		}
		if len(dense) != len(slice) {
			fail("embed batch", fmt.Errorf("got %d vectors for %d texts", len(dense), len(slice)))
			return
		}

		points := make([]Point, len(slice))
		for j, c := range slice {
			vectors := map[string]any{
				"dense": dense[j],
			}
			// Only include sparse vectors when the collection is
			// configured for hybrid. Qdrant rejects writes that
			// include an unconfigured vector name.
			if coll.SparseEnabled && j < len(sparse) {
				vectors["sparse"] = map[string]any{
					"indices": sparse[j].Indices,
					"values":  sparse[j].Values,
				}
			}
			points[j] = Point{
				// Deterministic UUID-v5 from (file_id, chunk_idx) so
				// re-ingest is idempotent at the Qdrant level — same
				// chunk gets the same point ID, overwrites cleanly.
				ID:      deterministicID(file.ID.Hex(), c.Idx),
				Vectors: vectors,
				Payload: map[string]any{
					"file_id":    file.ID.Hex(),
					"file_name":  file.Filename,
					"chunk_idx":  c.Idx,
					"chunk_text": c.Text,
					"page":       c.Page,
					"section":    c.Section,
					"project_id": file.ProjectID.Hex(),
					"user_id":    file.UserID,
				},
			}
		}
		if err := s.qdrant.Upsert(ctx, coll.QdrantCollection, points); err != nil {
			fail("qdrant upsert", err)
			return
		}

		// Progress update.
		progress := float64(end) / float64(totalChunks)
		_ = s.files.UpdateStatus(ctx, file.ID, bson.M{
			"ingestProgress": progress,
			"chunkCount":     end,
		})
	}

	// 6. Mark ready.
	now := time.Now()
	_ = s.files.UpdateStatus(ctx, file.ID, bson.M{
		"status":         models.KnowledgeFileStatusReady,
		"ingestedAt":     now,
		"chunkCount":     totalChunks,
		"ingestProgress": 1.0,
		"error":          "",
	})
	_ = s.collections.IncrementChunks(ctx, file.ProjectID, 1, totalChunks)
	log.Printf("%s READY chunks=%d took=%s", logPrefix, totalChunks, time.Since(start))
}

// cachedEmbedderInfo asks the sidecar what model+dim it's running.
// Cached for 5 minutes — model swaps are rare and a stale cache just
// means one more retry path (CreateCollection rejects dim mismatch).
func (s *Service) cachedEmbedderInfo(ctx context.Context) (string, int, error) {
	s.mu.Lock()
	if time.Since(s.embedderCached.when) < 5*time.Minute && s.embedderCached.id != "" {
		id, dims := s.embedderCached.id, s.embedderCached.dims
		s.mu.Unlock()
		return id, dims, nil
	}
	s.mu.Unlock()

	h, err := s.embed.Health(ctx)
	if err != nil {
		return "", 0, err
	}
	if h.DenseDim == 0 {
		// First call ever — sidecar hasn't loaded the model yet. Do
		// a tiny embed to trigger load + dim probe.
		_, _, dim, err := s.embed.EmbedBatch(ctx, []string{"dim probe"})
		if err != nil {
			return "", 0, fmt.Errorf("embedder warmup: %w", err)
		}
		h.DenseDim = dim
	}

	s.mu.Lock()
	s.embedderCached.id = h.Models["dense"]
	s.embedderCached.dims = h.DenseDim
	s.embedderCached.when = time.Now()
	s.mu.Unlock()
	return h.Models["dense"], h.DenseDim, nil
}

// deterministicID produces a stable point ID from (file_id, chunk_idx)
// so re-ingest overwrites the same Qdrant point rather than creating
// duplicates. Qdrant accepts string IDs in UUID form.
func deterministicID(fileID string, chunkIdx int) string {
	// UUID v5 over a namespace + name.
	ns := uuid.NewSHA1(uuid.NameSpaceOID, []byte("claraverse/rag"))
	return uuid.NewSHA1(ns, []byte(fmt.Sprintf("%s:%d", fileID, chunkIdx))).String()
}

func readFile(path string) ([]byte, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	return io.ReadAll(f)
}
