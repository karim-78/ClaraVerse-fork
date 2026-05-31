# Changelog

All notable changes to ClaraVerse.

This project uses semantic versioning. Tags are cut as `vMAJOR.MINOR.PATCH`.

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
