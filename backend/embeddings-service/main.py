"""
ClaraVerse embeddings sidecar.

Wraps FastEmbed (Qdrant Inc's ONNX-based embedder library) behind a tiny
FastAPI surface. Three models are exposed:

  * Dense embedder (default: BAAI/bge-small-en-v1.5, 384-dim cosine).
    The de-facto default for Qdrant-backed RAG: MTEB top-of-class for
    its size, 133 MB on disk, runs on CPU, MIT licensed.

  * Sparse encoder (default: Qdrant/bm25).
    Term-frequency style sparse vectors that pair with dense in Qdrant's
    hybrid search. The single biggest quality lever after the dense
    embedder itself — catches keyword-heavy queries that pure semantic
    search misses.

  * Reranker (default: BAAI/bge-reranker-base).
    Cross-encoder for top-50 → top-5 reranking. Lazy-loaded by default
    (set EMBEDDINGS_PRELOAD_RERANKER=true to warm at boot) so dev
    environments that don't use reranking don't pay the cold-start cost.

Why a sidecar and not in-process Go: FastEmbed has no first-party Go
binding and ONNX-from-Go is fragile across platforms. A 100-line FastAPI
service is cleaner, gives us a clean swap point for admin model
overrides, and lets us add new models without redeploying the backend.
"""

from __future__ import annotations

import os
import time
from typing import List, Optional

from fastapi import FastAPI, HTTPException
from pydantic import BaseModel, Field

DENSE_MODEL = os.environ.get("EMBEDDINGS_DENSE_MODEL", "BAAI/bge-small-en-v1.5")
SPARSE_MODEL = os.environ.get("EMBEDDINGS_SPARSE_MODEL", "Qdrant/bm25")
RERANK_MODEL = os.environ.get("EMBEDDINGS_RERANK_MODEL", "BAAI/bge-reranker-base")
PRELOAD_RERANKER = os.environ.get("EMBEDDINGS_PRELOAD_RERANKER", "false").lower() == "true"

app = FastAPI(title="ClaraVerse embeddings sidecar", version="0.1.0")

# Lazy holders — initialized on first use to keep cold-start fast.
# FastEmbed downloads model weights on first instantiation (cached
# afterwards in /root/.cache/fastembed which is a docker volume).
_dense = None
_sparse = None
_rerank = None
_dense_dim: Optional[int] = None


def _get_dense():
    global _dense, _dense_dim
    if _dense is None:
        from fastembed import TextEmbedding
        _dense = TextEmbedding(model_name=DENSE_MODEL)
        # Probe dim by embedding a single token. Cheap, runs once.
        sample = list(_dense.embed(["dim probe"]))[0]
        _dense_dim = len(sample)
    return _dense


def _get_sparse():
    global _sparse
    if _sparse is None:
        from fastembed import SparseTextEmbedding
        _sparse = SparseTextEmbedding(model_name=SPARSE_MODEL)
    return _sparse


def _get_rerank():
    global _rerank
    if _rerank is None:
        from fastembed.rerank.cross_encoder import TextCrossEncoder
        _rerank = TextCrossEncoder(model_name=RERANK_MODEL)
    return _rerank


if PRELOAD_RERANKER:
    # Warm at boot when admins want reranking on the hot path.
    _get_rerank()


# ── Models ────────────────────────────────────────────────────────────


class EmbedRequest(BaseModel):
    texts: List[str] = Field(..., min_length=1, max_length=256)
    """Batch up to 256 texts per request. Bigger batches don't help much on CPU."""


class DenseVector(BaseModel):
    values: List[float]


class SparseVector(BaseModel):
    indices: List[int]
    values: List[float]


class EmbedResponse(BaseModel):
    dense: List[DenseVector]
    sparse: List[SparseVector]
    dim: int
    model_dense: str
    model_sparse: str
    took_ms: int


class EmbedQueryRequest(BaseModel):
    query: str
    """Single query — separate endpoint so the response shape is clean."""


class EmbedQueryResponse(BaseModel):
    dense: DenseVector
    sparse: SparseVector
    dim: int
    took_ms: int


class RerankRequest(BaseModel):
    query: str
    documents: List[str] = Field(..., min_length=1, max_length=200)
    top_k: int = Field(default=5, ge=1, le=100)


class RerankHit(BaseModel):
    index: int
    """Position in the input `documents` array."""
    score: float


class RerankResponse(BaseModel):
    hits: List[RerankHit]
    model: str
    took_ms: int


class HealthResponse(BaseModel):
    ok: bool
    dense_loaded: bool
    sparse_loaded: bool
    rerank_loaded: bool
    dense_dim: Optional[int]
    models: dict


# ── Endpoints ─────────────────────────────────────────────────────────


@app.get("/health", response_model=HealthResponse)
def health():
    return HealthResponse(
        ok=True,
        dense_loaded=_dense is not None,
        sparse_loaded=_sparse is not None,
        rerank_loaded=_rerank is not None,
        dense_dim=_dense_dim,
        models={
            "dense": DENSE_MODEL,
            "sparse": SPARSE_MODEL,
            "rerank": RERANK_MODEL,
        },
    )


@app.post("/embed", response_model=EmbedResponse)
def embed(req: EmbedRequest):
    """Batch-embed a list of documents (both dense + sparse).

    Used during ingestion. Both vector kinds are produced in one round
    trip so the caller can upsert a single Qdrant point with both.
    """
    t0 = time.perf_counter()
    try:
        dense_model = _get_dense()
        sparse_model = _get_sparse()
        dense_vecs = list(dense_model.embed(req.texts))
        sparse_vecs = list(sparse_model.embed(req.texts))
    except Exception as e:  # noqa: BLE001 — surface the model error verbatim
        raise HTTPException(status_code=500, detail=f"embed failed: {e!r}")
    return EmbedResponse(
        dense=[DenseVector(values=v.tolist()) for v in dense_vecs],
        sparse=[
            SparseVector(indices=v.indices.tolist(), values=v.values.tolist())
            for v in sparse_vecs
        ],
        dim=_dense_dim or 0,
        model_dense=DENSE_MODEL,
        model_sparse=SPARSE_MODEL,
        took_ms=int((time.perf_counter() - t0) * 1000),
    )


@app.post("/embed/query", response_model=EmbedQueryResponse)
def embed_query(req: EmbedQueryRequest):
    """Embed a single query (dense + sparse).

    Note for bge models: at INGEST time the bge family expects a passage
    prefix `passage: <text>`; at QUERY time it expects `query: <text>`.
    We add these prefixes here so callers don't have to think about it.
    Other model families ignore the prefix harmlessly.
    """
    t0 = time.perf_counter()
    try:
        dense_model = _get_dense()
        sparse_model = _get_sparse()
        # bge-style query prefix improves retrieval ~5-10%.
        q = req.query
        dense_vec = list(dense_model.query_embed([q]))[0]
        sparse_vec = list(sparse_model.query_embed([q]))[0]
    except Exception as e:  # noqa: BLE001
        raise HTTPException(status_code=500, detail=f"embed query failed: {e!r}")
    return EmbedQueryResponse(
        dense=DenseVector(values=dense_vec.tolist()),
        sparse=SparseVector(
            indices=sparse_vec.indices.tolist(),
            values=sparse_vec.values.tolist(),
        ),
        dim=_dense_dim or 0,
        took_ms=int((time.perf_counter() - t0) * 1000),
    )


@app.post("/rerank", response_model=RerankResponse)
def rerank(req: RerankRequest):
    """Cross-encoder rerank documents against a query.

    Returns the top_k by score, with indices pointing back into the
    input `documents` array so the caller can carry payload through
    without us round-tripping it.
    """
    t0 = time.perf_counter()
    try:
        encoder = _get_rerank()
        scores = list(encoder.rerank(req.query, req.documents))
    except Exception as e:  # noqa: BLE001
        raise HTTPException(status_code=500, detail=f"rerank failed: {e!r}")
    indexed = list(enumerate(scores))
    indexed.sort(key=lambda p: p[1], reverse=True)
    hits = [RerankHit(index=i, score=float(s)) for i, s in indexed[: req.top_k]]
    return RerankResponse(
        hits=hits,
        model=RERANK_MODEL,
        took_ms=int((time.perf_counter() - t0) * 1000),
    )
