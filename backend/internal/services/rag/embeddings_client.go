// Package rag implements the project-scoped knowledge-base layer:
// ingestion, vector storage, and retrieval. It speaks HTTP to two
// sidecars — Qdrant (vector DB) and our embeddings service (FastEmbed
// wrapper) — and exposes a Service that the tool layer, HTTP handlers,
// and workflow executor can all call into.
//
// Why a package and not files under services/: the RAG path has its
// own concept surface (chunks, vectors, hybrid search, reranking) that
// doesn't map onto any existing service. Keeping it scoped lets us
// evolve the internals without leaking across the codebase.
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

// EmbeddingsClient is the HTTP wrapper around the embeddings sidecar.
//
// All endpoints are POST and accept JSON. We keep retries minimal here
// — the sidecar is on the same docker network, so a failure is almost
// always either a model loading delay (cold start) or a real bug.
// Callers that need patience should set a long context deadline.
type EmbeddingsClient struct {
	baseURL string
	http    *http.Client
}

// NewEmbeddingsClient builds a client pointed at the sidecar. baseURL
// is typically `http://embeddings:8002` inside docker, or
// `http://localhost:8002` when running the backend natively against a
// `docker compose up embeddings` sidecar.
func NewEmbeddingsClient(baseURL string) *EmbeddingsClient {
	return &EmbeddingsClient{
		baseURL: baseURL,
		// Generous timeout — first request after sidecar cold start
		// downloads model weights (133 MB) and can take 30-60s.
		// Subsequent requests are sub-second.
		http: &http.Client{Timeout: 120 * time.Second},
	}
}

// DenseVec is a 384-dim float32 vector (for bge-small) or 1024-dim
// (for bge-large). The sidecar returns float64 over JSON; we narrow to
// float32 because Qdrant stores float32 internally and there's no
// reason to carry the extra precision.
type DenseVec []float32

// SparseVec is a sparse representation: parallel arrays of indices
// and values. Format matches Qdrant's sparse vector API exactly.
type SparseVec struct {
	Indices []uint32  `json:"indices"`
	Values  []float32 `json:"values"`
}

type embedReq struct {
	Texts []string `json:"texts"`
}

type embedResp struct {
	Dense       []struct{ Values []float32 `json:"values"` } `json:"dense"`
	Sparse      []SparseVec                                  `json:"sparse"`
	Dim         int                                          `json:"dim"`
	ModelDense  string                                       `json:"model_dense"`
	ModelSparse string                                       `json:"model_sparse"`
	TookMs      int                                          `json:"took_ms"`
}

// EmbedBatch produces dense+sparse vectors for a batch of texts.
// Used at ingest time — each call upserts one batch into Qdrant.
//
// The sidecar caps batches at 256. We don't enforce that here; the
// sidecar will reject and we'll surface the error verbatim so callers
// learn the constraint loudly the first time.
func (c *EmbeddingsClient) EmbedBatch(ctx context.Context, texts []string) (dense []DenseVec, sparse []SparseVec, dim int, err error) {
	body, _ := json.Marshal(embedReq{Texts: texts})
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL+"/embed", bytes.NewReader(body))
	if err != nil {
		return nil, nil, 0, err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := c.http.Do(req)
	if err != nil {
		return nil, nil, 0, fmt.Errorf("embeddings /embed: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		b, _ := io.ReadAll(resp.Body)
		return nil, nil, 0, fmt.Errorf("embeddings /embed: status %d: %s", resp.StatusCode, string(b))
	}
	var er embedResp
	if err := json.NewDecoder(resp.Body).Decode(&er); err != nil {
		return nil, nil, 0, fmt.Errorf("embeddings /embed decode: %w", err)
	}
	dense = make([]DenseVec, len(er.Dense))
	for i, d := range er.Dense {
		dense[i] = DenseVec(d.Values)
	}
	return dense, er.Sparse, er.Dim, nil
}

type embedQueryReq struct {
	Query string `json:"query"`
}

type embedQueryResp struct {
	Dense  struct{ Values []float32 `json:"values"` } `json:"dense"`
	Sparse SparseVec                                  `json:"sparse"`
	Dim    int                                        `json:"dim"`
	TookMs int                                        `json:"took_ms"`
}

// EmbedQuery produces dense+sparse vectors for a single search query.
// The sidecar applies a bge-style "query:" prefix so retrieval quality
// matches the asymmetric ingest/query split bge was trained for.
func (c *EmbeddingsClient) EmbedQuery(ctx context.Context, query string) (DenseVec, SparseVec, error) {
	body, _ := json.Marshal(embedQueryReq{Query: query})
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL+"/embed/query", bytes.NewReader(body))
	if err != nil {
		return nil, SparseVec{}, err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := c.http.Do(req)
	if err != nil {
		return nil, SparseVec{}, fmt.Errorf("embeddings /embed/query: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		b, _ := io.ReadAll(resp.Body)
		return nil, SparseVec{}, fmt.Errorf("embeddings /embed/query: status %d: %s", resp.StatusCode, string(b))
	}
	var qr embedQueryResp
	if err := json.NewDecoder(resp.Body).Decode(&qr); err != nil {
		return nil, SparseVec{}, fmt.Errorf("embeddings /embed/query decode: %w", err)
	}
	return DenseVec(qr.Dense.Values), qr.Sparse, nil
}

type rerankReq struct {
	Query     string   `json:"query"`
	Documents []string `json:"documents"`
	TopK      int      `json:"top_k"`
}

// RerankHit is one result from the cross-encoder rerank step. Index
// points back into the input documents array; the caller carries
// payload through without round-tripping it.
type RerankHit struct {
	Index int     `json:"index"`
	Score float32 `json:"score"`
}

type rerankResp struct {
	Hits   []RerankHit `json:"hits"`
	Model  string      `json:"model"`
	TookMs int         `json:"took_ms"`
}

// Rerank scores documents against a query using the cross-encoder
// model loaded in the sidecar. Caps the batch at 200 — beyond that
// the cross-encoder gets prohibitively slow on CPU.
func (c *EmbeddingsClient) Rerank(ctx context.Context, query string, documents []string, topK int) ([]RerankHit, error) {
	if len(documents) > 200 {
		documents = documents[:200]
	}
	body, _ := json.Marshal(rerankReq{Query: query, Documents: documents, TopK: topK})
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL+"/rerank", bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := c.http.Do(req)
	if err != nil {
		return nil, fmt.Errorf("embeddings /rerank: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		b, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("embeddings /rerank: status %d: %s", resp.StatusCode, string(b))
	}
	var rr rerankResp
	if err := json.NewDecoder(resp.Body).Decode(&rr); err != nil {
		return nil, fmt.Errorf("embeddings /rerank decode: %w", err)
	}
	return rr.Hits, nil
}

// HealthInfo describes what the sidecar has loaded right now.
type HealthInfo struct {
	OK           bool              `json:"ok"`
	DenseLoaded  bool              `json:"dense_loaded"`
	SparseLoaded bool              `json:"sparse_loaded"`
	RerankLoaded bool              `json:"rerank_loaded"`
	DenseDim     int               `json:"dense_dim"`
	Models       map[string]string `json:"models"`
}

// Health probes the sidecar and reports what's loaded. Used by the
// admin Knowledge tab to surface "embeddings service is warming up,
// first ingest may take ~60s" without surprising the user.
func (c *EmbeddingsClient) Health(ctx context.Context) (*HealthInfo, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.baseURL+"/health", nil)
	if err != nil {
		return nil, err
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return nil, fmt.Errorf("embeddings /health: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("embeddings /health: status %d", resp.StatusCode)
	}
	var h HealthInfo
	if err := json.NewDecoder(resp.Body).Decode(&h); err != nil {
		return nil, fmt.Errorf("embeddings /health decode: %w", err)
	}
	return &h, nil
}
