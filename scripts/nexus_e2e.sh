#!/usr/bin/env bash
#
# Nexus end-to-end tester.
#
# Fires a task through the headless REST API and reports what actually
# happened. Independent of the web UI — if this passes, the system works;
# if this fails, the bug is in the Nexus pipeline (not the WebSocket or
# the React state layer).
#
# Usage:
#   AUTH_TOKEN=<your jwt> ./scripts/nexus_e2e.sh \
#     [PROMPT="research X and create a PDF"] \
#     [PROJECT_ID=<optional>] \
#     [TIMEOUT=300]
#
# What it tests (with /run/sync — blocks until completion):
#   - Task is created
#   - Cortex picks the right mode (logs the classification)
#   - Daemons execute and finish (logs each one)
#   - If multi_daemon: artifacts produced + downstream reads them
#   - Final synthesis lands in task.result.summary
#   - Reports duration, daemons used, tool calls, artifact count
#
# Exit codes:
#   0  task completed successfully (status=completed)
#   1  task failed (status=failed)
#   2  timeout
#   3  bad input / auth missing
#   4  HTTP error from backend

set -euo pipefail

BASE_URL="${BASE_URL:-http://localhost:3001}"
PROMPT="${PROMPT:-research what claraverse is and create a PDF report summarising it}"
PROJECT_ID="${PROJECT_ID:-}"
TIMEOUT="${TIMEOUT:-300}"
MODE="${MODE:-}"

if [[ -z "${AUTH_TOKEN:-}" ]]; then
  echo "❌ AUTH_TOKEN env var required. Get one from your browser devtools:" >&2
  echo "   localStorage.getItem('auth_token') in the console while logged into ClaraVerse" >&2
  exit 3
fi

echo "🚀 Firing Nexus task (sync mode, timeout ${TIMEOUT}s)"
echo "   Backend: ${BASE_URL}"
echo "   Prompt:  ${PROMPT}"
echo "   Project: ${PROJECT_ID:-<inbox>}"
echo "   Mode:    ${MODE:-<auto>}"
echo

START_MS=$(python3 -c 'import time; print(int(time.time()*1000))' 2>/dev/null || date +%s)

# Build JSON body. jq if available for safety; else printf-escape.
if command -v jq >/dev/null 2>&1; then
  BODY=$(jq -nc \
    --arg prompt "$PROMPT" \
    --arg project_id "$PROJECT_ID" \
    --arg mode "$MODE" \
    --argjson timeout "$TIMEOUT" \
    '{prompt: $prompt, project_id: $project_id, mode: $mode, timeout_seconds: $timeout}')
else
  BODY=$(printf '{"prompt":"%s","project_id":"%s","mode":"%s","timeout_seconds":%s}' \
    "${PROMPT//\"/\\\"}" "$PROJECT_ID" "$MODE" "$TIMEOUT")
fi

RESPONSE=$(mktemp)
HTTP_CODE=$(curl -sS -o "$RESPONSE" -w "%{http_code}" \
  -X POST "${BASE_URL}/api/nexus/run/sync" \
  -H "Authorization: Bearer ${AUTH_TOKEN}" \
  -H "Content-Type: application/json" \
  --max-time "$((TIMEOUT + 10))" \
  -d "$BODY")

END_MS=$(python3 -c 'import time; print(int(time.time()*1000))' 2>/dev/null || date +%s)
ELAPSED=$((END_MS - START_MS))

echo "📨 HTTP ${HTTP_CODE}  (${ELAPSED}ms)"
echo

if [[ "$HTTP_CODE" == "504" ]]; then
  echo "⏰ TIMEOUT — task did not complete in time"
  jq -r '.task | {id, status, mode, error}' "$RESPONSE" 2>/dev/null || cat "$RESPONSE"
  rm -f "$RESPONSE"
  exit 2
fi

if [[ "$HTTP_CODE" != "200" ]]; then
  echo "❌ HTTP error"
  cat "$RESPONSE"
  rm -f "$RESPONSE"
  exit 4
fi

# Pretty-print the result with what actually happened.
if command -v jq >/dev/null 2>&1; then
  echo "═══ Task ═══"
  jq -r '.task | {id, status, mode, error, prompt, started_at, completed_at}' "$RESPONSE"
  echo
  echo "═══ Result summary (first 800 chars) ═══"
  jq -r '.task.result.summary // "(no summary)"' "$RESPONSE" | head -c 800
  echo
  echo
  echo "═══ Stats ═══"
  jq -r '{duration_ms, fired_at, finished_at}' "$RESPONSE"

  STATUS=$(jq -r '.task.status' "$RESPONSE")
else
  cat "$RESPONSE"
  STATUS=$(grep -o '"status":"[^"]*"' "$RESPONSE" | head -1 | cut -d'"' -f4)
fi

rm -f "$RESPONSE"

case "$STATUS" in
  completed)
    echo
    echo "✅ Task completed"
    exit 0
    ;;
  failed)
    echo
    echo "❌ Task failed"
    exit 1
    ;;
  *)
    echo
    echo "⚠️  Unexpected status: $STATUS"
    exit 4
    ;;
esac
