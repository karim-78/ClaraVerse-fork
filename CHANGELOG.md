# Changelog

All notable changes to ClaraVerse.

This project uses semantic versioning. Tags are cut as `vMAJOR.MINOR.PATCH`.

## [0.3.0] — 2026-06-01

Project-scoped knowledge bases (RAG) wired through every surface
that touches an LLM. Same retrieval pipeline across Chat, Nexus
daemons, and Workflows — the differentiator that closes the biggest
remaining gap vs. OpenWebUI per the v0.2.0 retrospective.

### RAG stack

- **Qdrant + FastEmbed sidecars** added to `docker-compose.yml` and
  `docker-compose.production.yml`. Fresh installs now boot the
  Knowledge feature end-to-end with no extra setup. Embedding models
  cached in a docker volume so restarts are fast after the first
  ~140 MB cold download.
- **Default stack**: bge-small-en-v1.5 (384-dim cosine, dense) +
  Qdrant/bm25 (sparse) + bge-reranker-base (cross-encoder). All
  three swappable per-deployment via `EMBEDDINGS_*` env vars.
- **One Qdrant collection per project** (`kb_<project_oid>`) so
  per-project snapshots, deletes, and embedder choice are trivial.
- **Hybrid retrieval (dense + sparse with RRF fusion)** runs by
  default for fresh collections. Phase A's pure-dense fallback is
  retained for pre-existing collections — Qdrant doesn't support
  adding a vector kind retroactively, so older collections stay
  dense-only until reingested.
- **Reranker on by default**: cross-encoder rerank on top-50 → top-K
  measurably better than vector order alone (verified live: same
  query went 5.24 → 6.77 → 8.45 score progression through dense →
  hybrid → hybrid+rerank).
- **Background warmup**: embeddings sidecar pre-loads dense + sparse
  models on boot in daemon threads so the FastAPI port binds
  immediately and the first ingest doesn't pay the cold-start cost.

### Three surfaces, one model

- **Chat**: multi-project chip picker above the input. Click `+
  Knowledge`, check projects, chips appear. Per-chat selection
  persists to localStorage (survives reloads). When the user sends,
  `search_knowledge` is injected as a per-turn tool scoped to the
  selected projects. Picker selection is the source of truth — drop
  the chips, drop the tool.
- **Nexus**: daemons spawned on a project task automatically get
  `search_knowledge` when the project has indexed knowledge. The
  Cortex classifier reads the project's knowledge state and biases
  the daemon's task summary toward "Search the project knowledge
  base for X" phrasing. The researcher quality gate accepts
  `search_knowledge` calls as a satisfier (alongside `search_web`
  / `fetch_url` / `read_artifact`).
- **Workflows**: new `knowledge_search` block in the agent builder.
  Full settings panel: project multi-select pulled from REST,
  templated query field, `top_k` (1-30), rerank toggle. Outputs
  `{chunks, count, elapsed_ms}` for downstream LLM blocks.

### Frontend additions

- New per-project **Knowledge tab** under each project in Nexus.
  Drag-and-drop upload (PDF / MD / TXT / HTML, up to 50 MB),
  file list with live ingest progress (polled every 3s while
  anything is non-terminal), embeddings sidecar warmup banner that
  shows only when files are actually queued/ingesting.
- Chat sidebar's previously "Coming Soon" **Projects** entry now
  active. Click navigates to Nexus for project management; tooltip
  surfaces how many projects are currently attached to the chat.
- New stores: `useChatKnowledgeStore` (per-chat persisted picker
  selection), `useNexusStore` partial persist for live activity
  panel survival across navigation.

### Data model

- New Mongo collections: `nexus_knowledge_files`,
  `nexus_knowledge_collections`. Files are catalog only — actual
  chunk text + vectors live in Qdrant. Per-collection record
  tracks embedder fingerprint so we refuse to ingest a file when
  the embedder's dim drifts from what the collection was built with.
- New backend models: `KnowledgeFile`, `KnowledgeCollection`,
  `KnowledgeSearchHit`.
- New `KnowledgeProjectIDs` + `InjectedTools` fields on
  `UserConnection` for per-turn chat tool wiring.
- `NexusOrchestrationState` carries `ProjectID` so a resumed
  multi-daemon run re-attaches `search_knowledge` correctly.

### Backend services

- New `backend/internal/services/rag/` package: parser
  (PDF/MD/TXT/HTML with page + section preservation),
  markdown- and code-fence-aware recursive chunker (1000/200),
  Qdrant + FastEmbed HTTP clients, ingest worker (drains a Mongo
  queue, batches of 64), search orchestrator (multi-project,
  hybrid, RRF, dedupe, rerank).
- New `RAGSearcher` interface in `services/` (avoids import cycle
  with `services/rag`). `NewRAGSearcher` wires it into the
  concrete service. Both `CortexService` and `ChatService` accept
  it via setters.
- `daemon_runner.go`: per-runner `injectedTools` map for
  context-bound tools that shouldn't go through the global
  registry. `executeTool` dispatches injected tools first.
- `tools/registry.go`: `GetMCPTools` now filters by
  `Source == ToolSourceMCPLocal` instead of returning anything in
  the userTools bucket — fixes a misroute where injected built-in
  tools were getting routed through the MCP bridge.

### Test harnesses

- `scripts/rag_e2e.sh`: full Nexus path. Provisions a user,
  creates a project, uploads a synthetic doc with a distinctive
  marker, waits for ingest, runs REST search, fires a Nexus task
  that should auto-use `search_knowledge`, asserts the marker
  appears in the daemon's reply.
- `scripts/rag_chat_e2e.py`: same shape for the chat WebSocket
  path. (Currently blocked by a fiber upgrade quirk in the
  websockets python client; left in for follow-up debug. Chat
  wiring is verified by build + boot log + the shared
  `RAGSearcher` interface backing both paths.)

### Documentation

- `docs/RAG.md`: full design doc, including the quality-lever
  ranking (hybrid → rerank → contextual chunking → smart
  chunking → citations → per-project embedder).
- `.env.example`: documents all `QDRANT_URL`, `EMBEDDINGS_SERVICE_URL`,
  `EMBEDDINGS_*_MODEL`, and `EMBEDDINGS_PRELOAD_RERANKER` knobs.
- `README.md`: Knowledge bases row added to the feature matrix;
  the single-container `docker run` install now flags that RAG
  needs the Compose setup (no qdrant/embeddings sidecars in the
  one-container image).

### Deferred to next release

- **Contextual chunk prefixing** (Anthropic-style, ~35% retrieval
  improvement). Toggleable flag preserved on
  `KnowledgeCollection.ContextualEnabled` so this lands without a
  schema migration.
- **Per-project embedder override** in admin UI (backend already
  stores `EmbedderID` per collection; UI not yet built).
- **Reingest button** on the file list for migrating pre-Phase-C
  collections to hybrid.

### Fixes for new surfaces (caught during dogfooding)

- Frontend `nexusService` + `knowledgeService` list endpoints
  coerce JSON `null` → `[]` (Go's nil-slice serialization). Saved
  every consumer from a defensive `?? []` guard.
- Embeddings sidecar pre-warms models in a daemon thread at boot,
  so the "model warming up" banner doesn't get stuck on indefinitely.
- Knowledge tab upload was reading `auth_token` from localStorage,
  but the app stores it as `access_token`. Switched to
  `authClient.getAccessToken()` — single source of truth.
- Chat picker checkboxes were unresponsive because the Zustand
  selector subscribed to `s.get` (stable function ref). Switched
  to subscribing to `s.selections[chatKey] ?? KNOWLEDGE_EMPTY` —
  toggling now updates the chip row instantly.

## [0.2.0] — 2026-05-31

A hardening release focused on making Nexus — the multi-agent orchestration
layer — production-grade. Sixteen fixes across the daemon pipeline, the
LLM transport layer, the Kanban UI lifecycle, and the multi-user safety
boundary. Plus a rebrand revert to the original ClaraVerse name and
rose-pink palette.

### Nexus — orchestration durability

- **Daemon quality gate.** Before a daemon can claim "done," we verify it
  actually did its job. Multi-daemon workers with downstream consumers
  must have called `produce_artifact` at least once (without it,
  downstream daemons re-derive everything from a thin summary).
  Researcher-role daemons must have called at least one
  information-gathering tool (`search_web`, `fetch_url`, `read_artifact`,
  etc.) — finishing without searching means hallucinated output ~95% of
  the time. Violations inject a corrective reprompt and continue the
  loop, capped at 2 enforcement cycles so a stubborn model can't be
  pinned forever.
- **Five knobs for long-running tasks.** Bumped the orchestrator execution
  timeout from 10 min → 30 min, daemon max iterations from 40 → 100, and
  per-user daemon concurrency from 5 → 10. Added exponential backoff
  (capped at 30 s) for transient LLM errors — 429s, 5xx, network blips
  no longer kill the run. Added phase-summary recycling: every 25
  iterations the daemon compacts its conversation to
  `[system, original task, phase summary]` so 100-iteration runs don't
  blow the context window even after aggressive tool-result trimming.
- **Fixed the "queued tasks never start" bug.** Three independent root
  causes were silently swallowing tasks: (1) pending daemons were being
  dropped from the queue before slot acquisition succeeded; (2) a slot
  exhaustion deadlock had no break-out — fixed with a stuck-pending
  detector that marks the daemon failed and exits the loop; (3) zombie
  daemons surviving a backend restart were never reaped, so they
  occupied phantom slots forever — fixed with boot-time
  `CleanupStaleDaemons` and `CleanupStaleTasks`.
- **Artifact isolation per orchestration.** The synthesis step used to
  pull every artifact the user had ever produced into the final prompt,
  causing stale Python code to leak into edge-computing answers. Now
  uses `ListSince(parent.CreatedAt)` to scope to artifacts produced
  within this orchestration's lifetime.
- **Synthesis resilience.** Added structured error publishing
  (`error_code`, `user_message`, `hint`, internal `err`) so the UI can
  show actionable messages instead of raw stack traces. Added 30-min
  ceilings on resume-from-mongo contexts.

### Nexus — LLM transport (Bedrock OpenAI shim)

The Bedrock OpenAI-compatible endpoint is strict about request body
shape; four cooperating fixes closed a long-running 400 loop:

- **`cache_control` removal.** Bedrock rejects Anthropic-style
  `cache_control` blocks on the OpenAI endpoint.
- **HTML-escape disabled** on JSON encode (the default Go behavior
  rewrites `<>&` to `<>&`, which broke the parser on
  certain inputs).
- **20 KB request-body guard** with aggressive tool-result shrinking when
  a single message body exceeds the threshold.
- **Tool call ID sanitization.** Bedrock rejects the
  `functions.X:N`-style IDs some providers emit; rewrites them to
  `call_<n>`.
- **`parallel_tool_calls=false`** forced — Bedrock 400s on assistant
  messages containing multiple `tool_calls` in a single turn.
- **`filterValidToolCalls`** drops tool calls whose `arguments` JSON was
  truncated mid-string by the model — echoing those back triggers
  "Unterminated string at column N" 400s.
- **Anti-loop nudge** appended 5 iterations before the cap, telling the
  model to write its final answer instead of burning the budget on more
  tool calls.

### Multi-agent — what actually makes the multi-daemon path work

- **Artifact handoff between daemons.** Wired the artifact store + a
  `SubagentRunner` adapter onto every daemon path, taught the system
  prompt to use `produce_artifact` / `list_artifacts` / `read_artifact`
  for cross-daemon handoff (without this, downstream daemons only got a
  thin summary of upstream output).
- **PDF auto-nudge.** The context builder detects tasks containing
  pdf/report/document and biases the prompt toward an artifact-producing
  flow.
- **Classifier bias.** `BuildClassificationPrompt` now strongly favors
  `MULTI_DAEMON` for "X and Y"-shaped requests (research X and write Y
  was previously misclassified as single-daemon).
- **Multi-daemon context.** Each daemon now gets a terse `Position:
  daemon N of M / Handoff:` block in its system prompt — the model
  understands it has downstream consumers and uses the artifact tools.

### Multi-user safety

- **Audited isolation across the shared services.** EventBus, task
  store, daemon pool queries, artifact store, orchestration state,
  WebSocket handler — all correctly scope by `userId`/`sessionId`.
- **Closed the one gap that existed.** `DaemonPool.Cancel(daemonID)` had
  no ownership check; any authenticated user with a daemon ObjectID
  could cancel another user's daemon. Added `CancelForUser(ctx, userID,
  daemonID)` that loads the daemon scoped to the user before
  cancelling; routed the WebSocket `cancel_daemon` handler through it.
- **Concurrent stress test** (`scripts/nexus_multiuser_stress.sh`):
  provisions N test users, fires N tasks in parallel with unique marker
  strings, asserts each user's result contains its OWN marker and none
  of the others'. Validates the entire shared-service isolation under
  load. **5/5 clean, ~36 s end-to-end concurrent run, zero cross-user
  contamination.**

### Frontend — Nexus UI lifecycle

- **Live activity panel survives navigation.** `useNexusStore` now wraps
  `persist` with `sessionStorage`, partialized to `conversation` (last
  200), `daemons`, `classification`, `missedUpdates`. Switching from
  Chat → Nexus → Workflows → Nexus keeps the panel populated; hard
  reload within the tab session also restores it.
- **REST rehydration on mount.** `Nexus.tsx` fetches active daemons from
  `GET /api/nexus/daemons` on mount so even a cold reload (where
  sessionStorage was empty) repopulates the panel headers instantly,
  while the WebSocket streams live updates on top.
- **Task vanishing fix.** Tasks now rehydrate from REST on connect (not
  just from the WS `session_state` message, which can lag if events
  arrived while the WS was disconnected). Three trigger paths:
  connect/projectId change, document `visibilitychange`, and mount.
- **Daemon panel.** `expandedColumns` includes 'done' by default;
  `extractFileLinks` regex matches `(api/)?files/<id>` and renders green
  download buttons in `TaskDetailPanel`.
- **Toast on completion.** `task_completed` events now surface a success
  toast with a `truncateForToast` helper.

### Backend — execution + headless API

- **`/api/nexus/run` and `/api/nexus/run/sync` endpoints.** Headless REST
  for firing a Nexus task: fire-and-poll vs. block-until-completion.
  Both honor a 30-min ceiling.
- **`/api/workflows/templates` endpoint.** Workflow template gallery
  backend with 5 built-in templates.
- **Backup/restore admin endpoints.**
- **Integration test suite** for `nexus_orchestration_state` and
  `nexus_artifact_store`. Caught a `bson` tag bug on
  `NexusArtifactSummary` (`size_bytes` / `created_at` were returning 0).
- **`CosineSimilarity` precision fix.** The homemade sqrt with 4 Newton
  iterations was imprecise; replaced with `math.Sqrt` — caught by unit
  test.
- **`sendUpdate` panic guard.** Was crashing the whole backend on a
  `send on closed channel`; now uses `defer recover()`.

### Branding

- **Rose pink restored.** Design tokens reverted to the original
  ClaraVerse palette: `#e91e63` accent with hover `#f06292` / active
  `#c2185b`, HSL `340 82% 52%` for shadcn `primary`/`ring`, matching
  glow shadows and gradients. The "Emerald Edition" was an experimental
  detour.
- **Name restored.** 70 files reverted from `DobbyAI` → `ClaraVerse`
  across user-facing copy, meta tags, comments, log lines, and config
  defaults. URLs `dobbyai.app → claraverse.app`. The `DOBBY_DF` /
  `<</DOBBY_DF>>` protocol tokens between the Go runtime and the Python
  runner were **deliberately preserved** — those are a wire-format
  sentinel, not a brand string, and renaming them would silently break
  DataFrame extraction.

### Scripts

- `scripts/nexus_e2e.sh` — single-task end-to-end tester against
  `/api/nexus/run/sync`.
- `scripts/nexus_multiuser_stress.sh` — concurrent multi-user
  isolation/contamination test.
- `backend/cmd/mint_token` — mints a JWT for the first mongo user
  (used by both scripts).

### Documentation

- `docs/SYSTEMS.md` — ~600-line walkthrough of the Nexus pipeline:
  Cortex classifier → DaemonPlan DAG → DaemonRunner goroutines →
  synthesis. Reference for anyone debugging the multi-agent path.

---

For prior history (v0.1.x and earlier), see
[GitHub Releases](https://github.com/claraverse-space/ClaraVerse/releases).
