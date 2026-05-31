# ClaraVerse RAG — Design

**Status:** approved, building Phase A
**Owner:** ClaraLille
**Goal:** project-scoped knowledge that's shared across Chat, Nexus, and Workflows. Quality bar: as good as the engineering allows without paying anyone.

## TL;DR

```
Projects ARE the knowledge container.
  Upload files to a project → its knowledge base is built.
  Chat picks one or more projects → search_knowledge fans across them.
  Nexus tasks live in a project → daemons get search_knowledge automatically.
  Workflows use an explicit KnowledgeSearch block (project_ids + query).
```

No flat global collections. No per-chat manual attachment. One workspace, three surfaces.

## Stack

| Layer | Choice | Why |
|---|---|---|
| Vector DB | **Qdrant** (docker sidecar) | Native hybrid (dense + sparse), payload filters, snapshots, no schema |
| Default embedder | **BAAI/bge-small-en-v1.5** via FastEmbed | Maintained by Qdrant Inc. MIT. 133 MB ONNX. Top of MTEB for its size. CPU-only. No API key. |
| Sparse vec | **BM25** via FastEmbed (`Qdrant/bm25`) | Hybrid retrieval is the #1 quality lever. ~Free. +10-15% recall on keyword-heavy queries. |
| Reranker | **BAAI/bge-reranker-base** via FastEmbed | Cross-encoder rerank on top-50 → top-5. #2 quality lever. |
| Embedder host | **Python FastAPI sidecar** (`embeddings`) wrapping FastEmbed | Go has no first-party FastEmbed binding. A 100-line FastAPI service is cleaner than ONNX-from-Go and lets us hot-swap models in admin without redeploying the backend. |
| Admin overrides | Per-tenant provider config: OpenAI-compatible / Bedrock Titan / Cohere / Voyage | Same pattern as existing provider system |

## Architecture

```
                ┌─────────────────────────────────────────┐
                │  ClaraVerse backend (Go)                │
                │                                         │
   upload ────► │  /api/projects/:id/knowledge/files      │
                │       │                                 │
                │       ▼                                 │
                │  RAGIngestService                       │
                │   parse → chunk → contextual prefix     │
                │      │            (LLM call, optional)  │
                │      ▼                                  │
                │  embeddings sidecar  ───── dense (384d) │──┐
                │  HTTP :8002                ─ sparse (BM25)│  │
                │      ▲                                  │   │
   search_knowledge ───┘                                  │   │
   tool / handler                                         │   │
                │       │                                 │   ▼
                │       ▼                                 │  Qdrant
                │  Qdrant search (hybrid + filter)        │──HTTP :6333
                │   → top-50                              │
                │  rerank sidecar (cross-encoder)         │
                │   → top-5                               │
                │  return chunks[] with citations         │
                └─────────────────────────────────────────┘
```

Two sidecars: **`qdrant`** (binary, official image) and **`embeddings`** (our Python+FastEmbed FastAPI wrapper). Both live in `docker-compose.yml`. Backend talks HTTP to both.

## Data model

### Mongo (metadata)

- **`nexus_knowledge_files`** — uploaded file metadata
  ```
  { _id, project_id, user_id, filename, content_type, size_bytes,
    sha256, status: "queued" | "ingesting" | "ready" | "failed",
    error?, chunk_count, created_at, ingested_at }
  ```

- **`nexus_knowledge_collections`** — per-project collection record
  ```
  { _id, project_id, user_id, qdrant_collection: "kb_<project_id>",
    embedder_id, embedder_dims, sparse_enabled, doc_count, chunk_count,
    created_at, last_indexed_at }
  ```

We do **not** store chunk text in Mongo — Qdrant payload holds it. Mongo is just the file catalog and per-project bookkeeping.

### Qdrant (vectors + payload)

One **collection per project**: `kb_<project_oid_hex>`.

Configured with:
- Dense vector: 384-dim cosine (matches bge-small)
- Sparse vector: BM25
- Payload schema: `{ file_id, file_name, chunk_idx, chunk_text, page?, section?, contextual_prefix, project_id, user_id }`
- Filter index on `file_id`, `project_id`, `user_id` (HNSW + payload filtering)

Why per-project collection (not one mega-collection with payload filter):
- Cleaner per-project snapshot/restore/delete
- Per-project embedder choice (admin can use bge-small for Project A, OpenAI-large for Project B)
- Tighter HNSW graphs → faster recall
- Simpler authorization — collection access = project access

## Ingestion pipeline

```
File arrives
  ↓
1. Persist raw bytes to backend/uploads/kb/<project>/<sha256>.<ext>
   Insert nexus_knowledge_files with status="queued"
  ↓
2. Background worker picks up queued files (one at a time per project,
   parallel across projects). Marks status="ingesting".
  ↓
3. Parse text by content_type:
   - PDF: pdfium-go or `pdftotext` shellout (preserve page numbers)
   - MD/TXT: passthrough
   - HTML/URL: existing scraper_service
   - DOCX (v1.1): `mammoth` Python in embeddings sidecar
  ↓
4. Smart chunk:
   - Recursive char split, target=1000, overlap=200
   - Respect markdown headers (split at # / ## boundaries when possible)
   - Respect code blocks (don't split inside ``` fences)
   - Each chunk carries: page, section, prev/next chunk_id
  ↓
5. Contextual prefix (Anthropic contextual retrieval pattern):
   - For each chunk, do a small LLM call (cheap model — e.g. Haiku-tier):
     "Here is a chunk from <filename>. Section: <section>.
      In 50 words, summarize where in the document this chunk fits."
   - Prepend the summary to the chunk text BEFORE embedding.
   - This is the #3 quality lever. Costs ~1 cheap LLM call per chunk during
     ingest; zero overhead at query time. Anthropic measured ~35% improvement
     in retrieval failure rate.
   - **Toggleable per project** for cost-sensitive deployments.
  ↓
6. Embed (dense + sparse) via embeddings sidecar batch endpoint
  ↓
7. Upsert to Qdrant with full payload
  ↓
8. Update nexus_knowledge_files status="ready", record chunk_count
```

Failures: mark `status="failed"` with `error`, surface in UI with a retry button.

## Search

`search_knowledge(query, project_ids[], top_k=5, rerank=true)` returns:

```json
[
  {
    "text": "<chunk text>",
    "score": 0.87,
    "file_id": "...",
    "file_name": "Q3 strategy.pdf",
    "page": 7,
    "section": "Pricing strategy",
    "chunk_idx": 12
  },
  ...
]
```

Pipeline per query:

1. Embed query (dense + sparse) via sidecar
2. For each project_id, Qdrant hybrid search with RRF fusion → top-50
3. Merge across projects, dedupe by `(file_id, chunk_idx)`, keep top-50 by fused score
4. Rerank with cross-encoder via sidecar → top-K (default 5)
5. Return with citations

**Default for daemons:** `top_k=5, rerank=true`. They can override if a task needs more recall (`top_k=20, rerank=false` for survey-style work).

## Tool surface

Registered in `backend/internal/tools/`:

```go
NewKnowledgeSearchTool(ragService *RAGService)
  - Name: "search_knowledge"
  - Params: { query: string, project_ids?: []string, top_k?: int, rerank?: bool }
  - Category: "knowledge"
  - Default project_ids = [task.project_id] when called from a Nexus daemon
  - Default project_ids = chat.selected_project_ids when called from chat
```

Daemons see this tool when their task's project has a non-empty knowledge base. The **researcher quality gate** treats a `search_knowledge` call as equally satisfying to `search_web` — so a researcher daemon working in a project with knowledge can satisfy its obligation by searching internal docs.

The Cortex classifier also gains a bias: if the task is in a project with knowledge, prefer `daemon` mode with research role over `quick` mode.

## Three surfaces

### Chat (multi-project)

`TaskInputBar` and the chat send component get a chip row above the textarea:

```
[📁 Q3 Marketing  ×] [📁 Pricing Strategy  ×] [+ Add project]
```

Users can attach 0 or more projects per chat. The send request now carries `knowledge_project_ids: string[]`. When non-empty, the LLM gets `search_knowledge` as a tool with those project IDs as defaults.

A chat with no projects attached has no knowledge tool — model talks from its own knowledge only. (This matches what users expect: "open new chat, just talk to the model.")

### Nexus (project-scoped, automatic)

Tasks already live in a project. No new UI. Daemons running on a project task automatically get `search_knowledge` with `project_ids=[task.project_id]` as the default. If the project has 0 files, the tool isn't registered at all.

### Workflows (explicit block)

New block type `knowledge_search` in `backend/internal/execution/`:

```
inputs:
  project_ids: []string  (configured in block; can also accept upstream)
  query: string          (templated, e.g. {{prior.output}})
  top_k: int             (default 5)
  rerank: bool           (default true)
outputs:
  chunks: [{ text, file_id, file_name, page, section, score }, ...]
```

The block UI in `agent-builder` shows a project chip-picker + query textarea + top_k field + rerank toggle. Output feeds the next block (usually an LLM block that consumes the chunks).

## Project page — Knowledge tab

New view alongside Tasks / Routines / Saves / Settings:

```
┌─ Q3 Marketing > Knowledge ──────────────────────────────────────┐
│                                                                 │
│  ┌──────────────────────────────────────────────────────────┐   │
│  │   ⬆️  Drag files here, paste a URL, or click to browse   │   │
│  │      PDF · DOCX · MD · TXT · HTML · URLs                 │   │
│  └──────────────────────────────────────────────────────────┘   │
│                                                                 │
│  Files (15)                                          [Reingest] │
│  ─────────────────────────────────────────────────────────────  │
│  📄 Q3 strategy.pdf       •  ready  •  47 chunks  •  2 days ago │
│  📄 pricing notes.md      •  ready  •   8 chunks  •  2 days ago │
│  📄 board_deck.pdf        •  ingesting (chunk 23/41)            │
│  📄 broken.pdf            •  failed (encrypted)        [Retry]  │
│  ...                                                            │
│                                                                 │
│  Embedder: bge-small-en-v1.5 (default)         [Change ▾]       │
│  Total chunks: 312  •  Last indexed: 2 hours ago                │
└─────────────────────────────────────────────────────────────────┘
```

Admin can per-project change the embedder; switching triggers a reingest of the whole project (with a confirmation modal — it's not free).

## Quality levers (the "as good as possible" list)

In order of impact, with status:

| # | Lever | Phase | Status |
|---|---|---|---|
| 1 | Hybrid retrieval (dense + sparse BM25) | C | planned |
| 2 | Cross-encoder reranking (top-50 → top-5) | C | planned |
| 3 | Contextual chunk prefixing (Anthropic pattern) | C | planned |
| 4 | Smart chunking (respect markdown / code blocks / page boundaries) | A | building |
| 5 | Citation-first responses (file_id + page → UI chips) | A | building |
| 6 | Per-project embedder choice | A | building |
| 7 | Query expansion / HyDE (generate hypothetical answer, embed that) | D (future) | deferred |
| 8 | Document-level summaries for routing (which project is most relevant for this query?) | D (future) | deferred |
| 9 | Eval harness with golden Q&A pairs per project | D (future) | deferred |

## Build phases

- **Phase A (this session-ish):** Infra (Qdrant + embeddings sidecar), backend ingest pipeline, search_knowledge tool, Knowledge tab UI, file upload wiring.
- **Phase B:** Chat multi-project chip picker, Workflows `KnowledgeSearch` block, Cortex classifier bias + researcher gate accepting `search_knowledge`.
- **Phase C:** Quality layer — sparse BM25 vectors, reranker, contextual chunking.
- **Phase D (deferred):** HyDE, project-routing, eval harness.

## Authorization model

Strict: a user can only attach a project to chat / Nexus / workflow if they own it (or have been granted access — future). `search_knowledge` ALWAYS filters Qdrant by `project_id IN <user's accessible projects>`. Cross-tenant leaks would be a critical bug — fail-closed on the server side, never trust the client to filter.

When a user is deleted, their projects' Qdrant collections are deleted (cascade). When a project is deleted, its collection is deleted. Both via background cleanup job; no orphan vectors.

## Open questions (resolved)

- Q: Multiple projects per chat? **A: Yes, multi-select.**
- Q: Cross-project search? **A: Yes when user explicitly attaches multiple. Never automatic.**
- Q: Personal/global knowledge outside projects? **A: Deferred. Use a "General" project for now.**
- Q: Workflows — implicit project scope or explicit block? **A: Explicit block.**
- Q: Optional in Chat? **A: Yes, project attachment is optional in all surfaces.**
