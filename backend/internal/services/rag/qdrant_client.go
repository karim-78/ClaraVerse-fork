package rag

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"
)

// QdrantClient is a minimal HTTP client for Qdrant's REST API. We use
// HTTP rather than gRPC because (a) the official Go client adds heavy
// dependencies for features we don't use, (b) every operation we need
// has a single-RPC HTTP endpoint, and (c) being able to curl against
// the same URL the backend uses is invaluable when debugging.
//
// Scope is deliberately small: create collection, upsert points, delete
// by filter, hybrid search, and snapshot. Everything else (aliases,
// quantization, shard config, distributed scaling) is out of scope and
// can be configured directly via the Qdrant dashboard.
type QdrantClient struct {
	baseURL string
	http    *http.Client
}

// NewQdrantClient builds a client pointed at a Qdrant HTTP endpoint.
// baseURL is typically `http://qdrant:6333` in docker.
func NewQdrantClient(baseURL string) *QdrantClient {
	return &QdrantClient{
		baseURL: baseURL,
		http:    &http.Client{Timeout: 60 * time.Second},
	}
}

// ─── Collection management ────────────────────────────────────────────

type vectorParams struct {
	Size     int    `json:"size"`
	Distance string `json:"distance"` // "Cosine"
}

type sparseVectorParams struct {
	// Empty struct triggers Qdrant default sparse config. We may add
	// `modifier: "idf"` later when we run the BM25 path in Phase C.
}

type createCollectionReq struct {
	// Named vectors: "dense" for the bge embedding, "sparse" for BM25
	// (added in Phase C). Named vectors let us add sparse later without
	// migrating existing collections — they ignore vector names they
	// don't have.
	Vectors        map[string]vectorParams       `json:"vectors"`
	SparseVectors  map[string]sparseVectorParams `json:"sparse_vectors,omitempty"`
	HnswConfig     map[string]any                `json:"hnsw_config,omitempty"`
	OptimizersConfig map[string]any              `json:"optimizers_config,omitempty"`
}

// CreateCollection provisions a per-project Qdrant collection.
//
// Distance is hardcoded Cosine — bge models are trained with cosine
// similarity and other distances perform measurably worse. If we ever
// support embedders trained on dot-product, we'll add a parameter.
//
// Idempotent: if the collection already exists, returns nil. We don't
// want startup or first-ingest to fail just because the collection is
// already there.
func (c *QdrantClient) CreateCollection(ctx context.Context, name string, denseDim int, withSparse bool) error {
	body := createCollectionReq{
		Vectors: map[string]vectorParams{
			"dense": {Size: denseDim, Distance: "Cosine"},
		},
		// Tuned for "many small collections, fast warm reads": smaller
		// m (graph fan-out) to keep memory in check across N projects;
		// payload_m=16 makes filter-then-search fast for our
		// always-filtered queries.
		HnswConfig: map[string]any{
			"m":              16,
			"ef_construct":   128,
			"full_scan_threshold": 10_000,
			"payload_m":      16,
		},
		// Index immediately rather than waiting for the threshold —
		// projects with <10k chunks (most) wouldn't otherwise get
		// indexed at all and would fall back to brute force.
		OptimizersConfig: map[string]any{
			"indexing_threshold": 1000,
		},
	}
	if withSparse {
		body.SparseVectors = map[string]sparseVectorParams{
			"sparse": {},
		}
	}
	raw, _ := json.Marshal(body)

	req, err := http.NewRequestWithContext(ctx, http.MethodPut, fmt.Sprintf("%s/collections/%s", c.baseURL, name), bytes.NewReader(raw))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := c.http.Do(req)
	if err != nil {
		return fmt.Errorf("qdrant create %s: %w", name, err)
	}
	defer resp.Body.Close()
	// Qdrant returns 200 on create AND on already-exists (with a
	// different status string). Anything else is a real error.
	if resp.StatusCode == http.StatusOK {
		return nil
	}
	b, _ := io.ReadAll(resp.Body)
	// 409-ish "already exists" comes back as 400 with a message we can
	// recognize. Treat as success.
	bs := string(b)
	if bytes.Contains(b, []byte("already exists")) {
		return nil
	}
	return fmt.Errorf("qdrant create %s: status %d: %s", name, resp.StatusCode, bs)
}

// DeleteCollection drops an entire collection (used when a project is
// deleted). Returns nil if the collection doesn't exist.
func (c *QdrantClient) DeleteCollection(ctx context.Context, name string) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodDelete, fmt.Sprintf("%s/collections/%s", c.baseURL, name), nil)
	if err != nil {
		return err
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return fmt.Errorf("qdrant delete %s: %w", name, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusOK || resp.StatusCode == http.StatusNotFound {
		return nil
	}
	b, _ := io.ReadAll(resp.Body)
	return fmt.Errorf("qdrant delete %s: status %d: %s", name, resp.StatusCode, string(b))
}

// CollectionExists checks whether a collection is present. Cheap —
// used by ingest as a guard before upsert.
func (c *QdrantClient) CollectionExists(ctx context.Context, name string) (bool, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, fmt.Sprintf("%s/collections/%s/exists", c.baseURL, name), nil)
	if err != nil {
		return false, err
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return false, fmt.Errorf("qdrant exists %s: %w", name, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return false, fmt.Errorf("qdrant exists %s: status %d", name, resp.StatusCode)
	}
	var body struct {
		Result struct {
			Exists bool `json:"exists"`
		} `json:"result"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		return false, err
	}
	return body.Result.Exists, nil
}

// ─── Points (upsert / delete / search) ───────────────────────────────

// Point is one Qdrant point — a unique ID, named vectors, and payload.
//
// We always use string IDs (UUIDs) so the caller can deterministically
// derive them from (file_id, chunk_idx) and re-upserts are idempotent.
type Point struct {
	ID      string                 `json:"id"`
	Vectors map[string]any         `json:"vector"` // {"dense": [...], "sparse": {...}}
	Payload map[string]any         `json:"payload"`
}

type upsertReq struct {
	Points []Point `json:"points"`
}

// Upsert inserts or replaces a batch of points. The `wait=true` query
// param makes Qdrant ack only after the points are visible to search —
// without it, an upsert followed immediately by search can miss the
// new points and confuse users during ingest.
func (c *QdrantClient) Upsert(ctx context.Context, collection string, points []Point) error {
	body, _ := json.Marshal(upsertReq{Points: points})
	url := fmt.Sprintf("%s/collections/%s/points?wait=true", c.baseURL, collection)
	req, err := http.NewRequestWithContext(ctx, http.MethodPut, url, bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := c.http.Do(req)
	if err != nil {
		return fmt.Errorf("qdrant upsert %s: %w", collection, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		b, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("qdrant upsert %s: status %d: %s", collection, resp.StatusCode, string(b))
	}
	return nil
}

// DeleteByFilter removes all points matching the filter. Used when a
// knowledge file is deleted (filter: file_id = X) or when a file is
// re-ingested (delete then upsert).
func (c *QdrantClient) DeleteByFilter(ctx context.Context, collection string, filter map[string]any) error {
	body, _ := json.Marshal(map[string]any{"filter": filter})
	url := fmt.Sprintf("%s/collections/%s/points/delete?wait=true", c.baseURL, collection)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := c.http.Do(req)
	if err != nil {
		return fmt.Errorf("qdrant delete by filter %s: %w", collection, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		b, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("qdrant delete by filter %s: status %d: %s", collection, resp.StatusCode, string(b))
	}
	return nil
}

// ─── Search ───────────────────────────────────────────────────────────

// SearchHit is one result from Qdrant search. Score semantics depend
// on the distance metric (Cosine: higher is better, range [-1, 1]).
type SearchHit struct {
	ID      string         `json:"id"`
	Score   float32        `json:"score"`
	Payload map[string]any `json:"payload"`
}

type searchReq struct {
	Vector       map[string]any `json:"vector"`
	Filter       map[string]any `json:"filter,omitempty"`
	Limit        int            `json:"limit"`
	WithPayload  bool           `json:"with_payload"`
	ScoreThreshold *float32     `json:"score_threshold,omitempty"`
}

// SearchDense runs a dense-vector search against the "dense" named
// vector in a collection. The filter is optional but typically present
// to scope to a file_id or user_id.
//
// In Phase C we'll add SearchHybrid that calls /points/query with
// both dense and sparse using RRF fusion. For Phase A, dense-only is
// good enough and lets us validate the whole pipeline end-to-end.
func (c *QdrantClient) SearchDense(ctx context.Context, collection string, vec DenseVec, filter map[string]any, limit int) ([]SearchHit, error) {
	body, _ := json.Marshal(searchReq{
		Vector:      map[string]any{"name": "dense", "vector": vec},
		Filter:      filter,
		Limit:       limit,
		WithPayload: true,
	})
	url := fmt.Sprintf("%s/collections/%s/points/search", c.baseURL, collection)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := c.http.Do(req)
	if err != nil {
		return nil, fmt.Errorf("qdrant search %s: %w", collection, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		b, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("qdrant search %s: status %d: %s", collection, resp.StatusCode, string(b))
	}
	var body2 struct {
		Result []SearchHit `json:"result"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&body2); err != nil {
		return nil, fmt.Errorf("qdrant search decode: %w", err)
	}
	return body2.Result, nil
}

// SearchHybrid runs dense + sparse retrieval with reciprocal-rank
// fusion via Qdrant's /points/query endpoint. Returns the top `limit`
// hits across both vector kinds.
//
// We use the "prefetch" + RRF pattern that's Qdrant's recommended way
// to combine vector kinds: each prefetch returns its own top-K, then
// the outer query fuses by reciprocal rank. The sparse vector
// catches keyword matches (proper nouns, codenames, exact-match
// queries) that dense embeddings smooth over.
//
// Falls back to dense-only when the caller passes an empty sparse
// vector — handy for queries against pre-Phase-C collections that
// don't have sparse indexed.
func (c *QdrantClient) SearchHybrid(ctx context.Context, collection string, dense DenseVec, sparse SparseVec, filter map[string]any, limit int) ([]SearchHit, error) {
	if len(sparse.Indices) == 0 {
		// Dense-only fallback — sparse missing means either an older
		// collection or the sidecar didn't produce one. Caller doesn't
		// need to switch code paths.
		return c.SearchDense(ctx, collection, dense, filter, limit)
	}
	body, _ := json.Marshal(map[string]any{
		"prefetch": []map[string]any{
			{
				"query": dense,
				"using": "dense",
				"limit": limit * 2,
			},
			{
				"query": map[string]any{
					"indices": sparse.Indices,
					"values":  sparse.Values,
				},
				"using": "sparse",
				"limit": limit * 2,
			},
		},
		"query": map[string]any{
			"fusion": "rrf",
		},
		"filter":       filter,
		"limit":        limit,
		"with_payload": true,
	})
	url := fmt.Sprintf("%s/collections/%s/points/query", c.baseURL, collection)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := c.http.Do(req)
	if err != nil {
		return nil, fmt.Errorf("qdrant hybrid %s: %w", collection, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		b, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("qdrant hybrid %s: status %d: %s", collection, resp.StatusCode, string(b))
	}
	// /points/query wraps results in {result: {points: [...]}}
	var body2 struct {
		Result struct {
			Points []SearchHit `json:"points"`
		} `json:"result"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&body2); err != nil {
		return nil, fmt.Errorf("qdrant hybrid decode: %w", err)
	}
	return body2.Result.Points, nil
}

// Ping checks Qdrant liveness. Returns nil on success.
func (c *QdrantClient) Ping(ctx context.Context) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.baseURL+"/healthz", nil)
	if err != nil {
		return err
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return err
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("qdrant /healthz: status %d", resp.StatusCode)
	}
	return nil
}
