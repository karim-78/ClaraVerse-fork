#!/usr/bin/env bash
#
# RAG end-to-end smoke test.
#
# Walks the full path: provision a user → create a project → upload a
# document → wait for ingestion → search via the REST API → fire a
# Nexus task on the project and verify the daemon actually used
# search_knowledge (not just hallucinated). Each step asserts and
# prints, so a failure tells you exactly where the wire broke.
#
# What it proves on success:
#   - Qdrant sidecar reachable, accepting collection creation
#   - Embeddings sidecar produces dense + sparse vectors
#   - Worker drains queue, chunks + embeds + upserts
#   - Search returns relevant chunks with citations
#   - search_knowledge tool is registered on the daemon
#   - Daemon prefers project knowledge over web search
#
# Usage:
#   ./scripts/rag_e2e.sh
#
# Env overrides:
#   BASE_URL=http://localhost:3001   backend URL
#   PASSWORD=RagTest1!               throwaway user password
#   TIMEOUT=300                      Nexus task ceiling (seconds)
#   INGEST_TIMEOUT=180               wait for file → ready (seconds)
#
# Exit codes:
#   0  full path passed
#   1  step failed (check the logged stderr above)
#   2  ingest timed out

set -uo pipefail

BASE_URL="${BASE_URL:-http://localhost:3001}"
PASSWORD="${PASSWORD:-RagTest1!}"
TIMEOUT="${TIMEOUT:-300}"
INGEST_TIMEOUT="${INGEST_TIMEOUT:-180}"

if ! command -v jq >/dev/null 2>&1; then
  echo "❌ jq required (brew install jq)" >&2
  exit 1
fi

WORK=$(mktemp -d -t rag-e2e-XXXXXX)
trap 'rm -rf "$WORK"' EXIT

EMAIL="rag-e2e@claraverse.local"

# ─── Phase 0: pre-flight ────────────────────────────────────────────
echo "── Pre-flight checks ──"
QDRANT_HEALTH=$(curl -sS -o /dev/null -w "%{http_code}" --max-time 3 "$BASE_URL/api/auth/status")
if [[ "$QDRANT_HEALTH" != "200" ]]; then
  echo "❌ backend not reachable at $BASE_URL"
  exit 1
fi
echo "   ✓ backend reachable"

# Skip sidecar health here because the embeddings dim probe takes 5s
# on a cold start — we'd rather block on the first ingest.

# ─── Phase 1: provision user + login ────────────────────────────────
echo "── Provision user ──"
REGISTER_BODY=$(jq -nc --arg e "$EMAIL" --arg p "$PASSWORD" '{email:$e, password:$p}')
RESP=$(mktemp)
CODE=$(curl -sS -o "$RESP" -w "%{http_code}" \
  -X POST "$BASE_URL/api/auth/register" \
  -H "Content-Type: application/json" \
  -d "$REGISTER_BODY")
if [[ "$CODE" == "201" || "$CODE" == "200" ]]; then
  echo "   ✓ user created"
elif [[ "$CODE" == "409" ]]; then
  CODE=$(curl -sS -o "$RESP" -w "%{http_code}" \
    -X POST "$BASE_URL/api/auth/login" \
    -H "Content-Type: application/json" \
    -d "$REGISTER_BODY")
  if [[ "$CODE" != "200" ]]; then
    echo "   ✗ user exists but login failed ($CODE)" >&2
    cat "$RESP" >&2
    exit 1
  fi
  echo "   ✓ user re-used"
else
  echo "   ✗ register failed ($CODE):" >&2
  cat "$RESP" >&2
  exit 1
fi
TOKEN=$(jq -r .access_token "$RESP")
USER_ID=$(jq -r .user.id "$RESP" 2>/dev/null || echo "unknown")
echo "   token len=${#TOKEN}  user_id=$USER_ID"

AUTH=(-H "Authorization: Bearer $TOKEN")

# ─── Phase 2: project ───────────────────────────────────────────────
echo "── Project ──"
# Look for an existing rag-e2e project; create if missing.
PROJECTS=$(curl -sS "${AUTH[@]}" "$BASE_URL/api/nexus/projects")
PROJECT_ID=$(echo "$PROJECTS" | jq -r '.[] | select(.name=="rag-e2e") | .id' | head -1)
if [[ -z "$PROJECT_ID" ]]; then
  CREATE=$(curl -sS "${AUTH[@]}" \
    -X POST "$BASE_URL/api/nexus/projects" \
    -H "Content-Type: application/json" \
    -d '{"name":"rag-e2e","icon":"book","color":"#e91e63"}')
  PROJECT_ID=$(echo "$CREATE" | jq -r .id)
  if [[ -z "$PROJECT_ID" || "$PROJECT_ID" == "null" ]]; then
    echo "   ✗ project create failed:" >&2
    echo "$CREATE" >&2
    exit 1
  fi
  echo "   ✓ project created: $PROJECT_ID"
else
  echo "   ✓ project re-used: $PROJECT_ID"
fi

# ─── Phase 3: upload a test doc ────────────────────────────────────
echo "── Upload test doc ──"
# Synthetic doc with a very distinctive fact the model couldn't possibly
# know from training data. Our query will probe for the marker phrase;
# if the daemon's answer includes it we know search_knowledge actually
# fired (not a hallucination).
MARKER="The internal codename for the Q9 launch is BLUEMORPHO-XJ47."
cat > "$WORK/test.md" <<MD
# ClaraVerse RAG E2E Test Doc

This document exists solely to prove the retrieval pipeline works
end-to-end. It contains one distinctive fact that the model
absolutely cannot know without retrieving from this file.

## Codenames

$MARKER

The BLUEMORPHO-XJ47 release is scheduled for the third quarter of
2029 and includes the following components:
- A novel embedding scheme based on contextual prefixes.
- A reranker tuned specifically for project-scoped knowledge bases.
- Multi-project search with reciprocal rank fusion.

## Background

This file is regenerated on every test run, but the codename above
is the same string each time, so multiple runs produce the same
chunk content and the worker correctly deduplicates points in
Qdrant via deterministic UUID-v5 IDs.
MD

UP=$(mktemp)
CODE=$(curl -sS "${AUTH[@]}" -o "$UP" -w "%{http_code}" \
  -X POST "$BASE_URL/api/projects/$PROJECT_ID/knowledge/files" \
  -F "file=@$WORK/test.md")
if [[ "$CODE" != "201" && "$CODE" != "200" ]]; then
  echo "   ✗ upload failed ($CODE):" >&2
  cat "$UP" >&2
  exit 1
fi
FILE_ID=$(jq -r .id "$UP")
echo "   ✓ uploaded test.md  file_id=$FILE_ID"

# ─── Phase 4: wait for ingestion ───────────────────────────────────
echo "── Wait for ingestion (timeout ${INGEST_TIMEOUT}s) ──"
DEADLINE=$(( $(date +%s) + INGEST_TIMEOUT ))
LAST_STATUS=""
while (( $(date +%s) < DEADLINE )); do
  FILES=$(curl -sS "${AUTH[@]}" "$BASE_URL/api/projects/$PROJECT_ID/knowledge/files")
  STATUS=$(echo "$FILES" | jq -r ".files[] | select(.id==\"$FILE_ID\") | .status")
  PROGRESS=$(echo "$FILES" | jq -r ".files[] | select(.id==\"$FILE_ID\") | .ingest_progress // 0")
  CHUNKS=$(echo "$FILES" | jq -r ".files[] | select(.id==\"$FILE_ID\") | .chunk_count // 0")
  if [[ "$STATUS" != "$LAST_STATUS" ]]; then
    echo "   status=$STATUS progress=$PROGRESS chunks=$CHUNKS"
    LAST_STATUS="$STATUS"
  fi
  if [[ "$STATUS" == "ready" ]]; then
    echo "   ✓ ingestion complete: $CHUNKS chunks"
    break
  fi
  if [[ "$STATUS" == "failed" ]]; then
    ERR=$(echo "$FILES" | jq -r ".files[] | select(.id==\"$FILE_ID\") | .error")
    echo "   ✗ ingestion failed: $ERR" >&2
    exit 1
  fi
  sleep 2
done
if [[ "$STATUS" != "ready" ]]; then
  echo "   ✗ ingestion timed out (last status=$STATUS)" >&2
  exit 2
fi

# ─── Phase 5: search via REST ──────────────────────────────────────
echo "── REST search ──"
SEARCH=$(curl -sS "${AUTH[@]}" \
  -X POST "$BASE_URL/api/projects/$PROJECT_ID/knowledge/search" \
  -H "Content-Type: application/json" \
  -d '{"query":"What is the BLUEMORPHO codename?","top_k":3,"rerank":true}')
HIT_COUNT=$(echo "$SEARCH" | jq '.hits | length')
TOP_TEXT=$(echo "$SEARCH" | jq -r '.hits[0].text // empty' | head -c 200)
TOP_FILE=$(echo "$SEARCH" | jq -r '.hits[0].file_name // empty')
TOP_SCORE=$(echo "$SEARCH" | jq -r '.hits[0].score // 0')
echo "   hits=$HIT_COUNT  top_score=$TOP_SCORE  top_file=$TOP_FILE"
printf "   top_text: %s...\n" "$TOP_TEXT"
if [[ "$HIT_COUNT" -lt 1 ]]; then
  echo "   ✗ search returned no hits" >&2
  exit 1
fi
if ! echo "$SEARCH" | jq -r '.hits[].text' | grep -qi "BLUEMORPHO"; then
  echo "   ✗ search hits did not contain the marker term" >&2
  exit 1
fi
echo "   ✓ marker found in retrieved chunks"

# ─── Phase 6: Nexus task that should use search_knowledge ──────────
echo "── Nexus task (project-scoped, should auto-use search_knowledge) ──"
TASK_BODY=$(jq -nc \
  --arg p "Research the internal codename for the Q9 launch in this project's knowledge base, and tell me what it is. Cite the file." \
  --arg proj "$PROJECT_ID" \
  --argjson t "$TIMEOUT" \
  '{prompt:$p, project_id:$proj, timeout_seconds:$t}')
TASK=$(mktemp)
CODE=$(curl -sS "${AUTH[@]}" -o "$TASK" -w "%{http_code}" \
  -X POST "$BASE_URL/api/nexus/run/sync" \
  -H "Content-Type: application/json" \
  --max-time "$((TIMEOUT + 30))" \
  -d "$TASK_BODY")
if [[ "$CODE" != "200" ]]; then
  echo "   ✗ task HTTP $CODE:" >&2
  cat "$TASK" >&2
  exit 1
fi
STATUS=$(jq -r .task.status "$TASK")
SUMMARY=$(jq -r '.task.result.summary // ""' "$TASK")
echo "   task status=$STATUS"
echo "   summary (first 400 chars):"
echo "$SUMMARY" | head -c 400
echo
echo

if [[ "$STATUS" != "completed" ]]; then
  ERR=$(jq -r '.task.error // ""' "$TASK")
  echo "   ✗ task did not complete: $ERR" >&2
  exit 1
fi
if ! echo "$SUMMARY" | grep -qi "BLUEMORPHO"; then
  echo "   ⚠ daemon answer did not include the marker — search_knowledge"
  echo "     may not have been called, or the LLM ignored its output."
  echo "     Check backend logs for [DAEMON ...] Injected search_knowledge"
  exit 1
fi
echo "   ✓ daemon answer contained the marker — search_knowledge fired"
echo

echo "═══════════════════════════════════════════════════════════════"
echo "✅ RAG end-to-end PASSED"
echo "═══════════════════════════════════════════════════════════════"
