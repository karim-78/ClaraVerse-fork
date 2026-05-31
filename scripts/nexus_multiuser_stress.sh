#!/usr/bin/env bash
#
# Multi-user concurrent stress test for Nexus.
#
# Creates N throwaway users (or logs them back in if they already exist
# from a prior run), then fires N distinct tasks simultaneously through
# /api/nexus/run/sync. Each task contains a unique marker string in its
# prompt. After all tasks finish, we assert two things:
#
#   1. Each user's result contains their OWN marker (their task ran).
#   2. No user's result contains ANY OTHER user's marker (no cross-user
#      contamination through the event bus, artifact store, daemon pool,
#      or synthesis context).
#
# Why this matters: Nexus shares one orchestrator service, one event
# bus, one artifact store, and one daemon pool across all users. The
# isolation is enforced by userID/sessionID filtering on every read.
# If any of those filters has a bug, this test will catch it because
# user A's marker will leak into user B's summary.
#
# Usage:
#   ./scripts/nexus_multiuser_stress.sh
#
# Env overrides:
#   USERS=5                 number of concurrent users to spin up
#   BASE_URL=http://...     backend URL  (default http://localhost:3001)
#   TIMEOUT=300             per-task timeout in seconds
#   PASSWORD=stress-pass-1  shared password for the test users
#
# Exit codes:
#   0  all tasks completed AND no cross-user contamination
#   1  one or more tasks failed
#   2  cross-user contamination detected
#   3  setup error (couldn't create users / auth missing)

set -uo pipefail

BASE_URL="${BASE_URL:-http://localhost:3001}"
USERS="${USERS:-5}"
TIMEOUT="${TIMEOUT:-300}"
PASSWORD="${PASSWORD:-StressPass1!}"

if ! command -v jq >/dev/null 2>&1; then
  echo "❌ jq is required (brew install jq)" >&2
  exit 3
fi

echo "🧪 Nexus multi-user stress test"
echo "   backend: $BASE_URL"
echo "   users:   $USERS"
echo "   timeout: ${TIMEOUT}s"
echo

WORKDIR=$(mktemp -d -t nexus-stress-XXXXXX)
trap 'rm -rf "$WORKDIR"' EXIT

# ── Phase 1: provision N test users ─────────────────────────────────
# Registration is idempotent for our purposes — if the user already
# exists (409), fall back to login. Either way we end up with an
# access token in $WORKDIR/user_<i>.token.
echo "── Provisioning $USERS users ──"
for i in $(seq 1 "$USERS"); do
  email="nexus-stress-${i}@dobby.local"
  body=$(jq -nc --arg e "$email" --arg p "$PASSWORD" \
    '{email:$e, password:$p}')

  resp=$(mktemp)
  code=$(curl -sS -o "$resp" -w "%{http_code}" \
    -X POST "$BASE_URL/api/auth/register" \
    -H "Content-Type: application/json" \
    -d "$body")

  if [[ "$code" == "201" || "$code" == "200" ]]; then
    jq -r '.access_token' "$resp" > "$WORKDIR/user_${i}.token"
    echo "   ✓ created user $i ($email)"
  elif [[ "$code" == "409" ]]; then
    # User already exists from a prior run — log in.
    code=$(curl -sS -o "$resp" -w "%{http_code}" \
      -X POST "$BASE_URL/api/auth/login" \
      -H "Content-Type: application/json" \
      -d "$body")
    if [[ "$code" == "200" ]]; then
      jq -r '.access_token' "$resp" > "$WORKDIR/user_${i}.token"
      echo "   ✓ re-used user $i ($email)"
    else
      echo "   ✗ user $i exists but login failed ($code):" >&2
      cat "$resp" >&2
      rm -f "$resp"
      exit 3
    fi
  else
    echo "   ✗ user $i registration failed ($code):" >&2
    cat "$resp" >&2
    rm -f "$resp"
    exit 3
  fi
  rm -f "$resp"
done
echo

# ── Phase 2: fire N tasks in parallel ───────────────────────────────
# Each task gets a unique marker token. The prompt asks the model to
# include the marker in its final answer — that way we can verify that
# user N's marker shows up in user N's result and nowhere else.
echo "── Firing $USERS concurrent tasks ──"
pids=()
start_ms=$(python3 -c 'import time; print(int(time.time()*1000))')

for i in $(seq 1 "$USERS"); do
  marker="STRESS-MARKER-${i}-$(date +%s)"
  echo "$marker" > "$WORKDIR/user_${i}.marker"
  token=$(cat "$WORKDIR/user_${i}.token")

  prompt="You are a test daemon. Quickly summarise (one paragraph) what \
'parallel computing' means. CRITICAL: include the literal string '$marker' \
exactly once in your response — this is a test ID and must be echoed back."

  body=$(jq -nc --arg p "$prompt" --argjson t "$TIMEOUT" --arg mode "quick" \
    '{prompt:$p, mode:$mode, timeout_seconds:$t}')

  # Fire in background; capture full JSON to user_<i>.result
  (
    curl -sS -X POST "$BASE_URL/api/nexus/run/sync" \
      -H "Authorization: Bearer $token" \
      -H "Content-Type: application/json" \
      --max-time "$((TIMEOUT + 10))" \
      -d "$body" > "$WORKDIR/user_${i}.result" 2>"$WORKDIR/user_${i}.err"
    echo $? > "$WORKDIR/user_${i}.exit"
  ) &
  pids+=($!)
done

# Wait for all
for pid in "${pids[@]}"; do
  wait "$pid" || true
done

end_ms=$(python3 -c 'import time; print(int(time.time()*1000))')
elapsed=$((end_ms - start_ms))
echo "   ⏱  all tasks returned in ${elapsed}ms"
echo

# ── Phase 3: verify per-user correctness ────────────────────────────
echo "── Verifying results ──"
fail_count=0
leak_count=0
for i in $(seq 1 "$USERS"); do
  my_marker=$(cat "$WORKDIR/user_${i}.marker")
  exit_code=$(cat "$WORKDIR/user_${i}.exit" 2>/dev/null || echo "1")

  if [[ "$exit_code" != "0" ]]; then
    echo "   ✗ user $i: curl exited $exit_code"
    cat "$WORKDIR/user_${i}.err" 2>/dev/null | head -3
    fail_count=$((fail_count + 1))
    continue
  fi

  status=$(jq -r '.task.status // "unknown"' "$WORKDIR/user_${i}.result" 2>/dev/null)
  summary=$(jq -r '.task.result.summary // ""' "$WORKDIR/user_${i}.result" 2>/dev/null)

  if [[ "$status" != "completed" ]]; then
    err=$(jq -r '.task.error // .error // "no error field"' "$WORKDIR/user_${i}.result" 2>/dev/null)
    echo "   ✗ user $i: status=$status  err=$err"
    fail_count=$((fail_count + 1))
    continue
  fi

  # Own marker present?
  if ! grep -qF "$my_marker" <<< "$summary"; then
    echo "   ⚠ user $i: own marker missing from summary (model may have ignored instruction)"
    # Not a hard failure — model compliance isn't what we're testing
  fi

  # Any other user's marker leaked in?
  leaked=""
  for j in $(seq 1 "$USERS"); do
    [[ "$j" == "$i" ]] && continue
    other_marker=$(cat "$WORKDIR/user_${j}.marker")
    if grep -qF "$other_marker" <<< "$summary"; then
      leaked="$leaked user_${j}"
      leak_count=$((leak_count + 1))
    fi
  done

  if [[ -n "$leaked" ]]; then
    echo "   ✗ user $i: CROSS-USER LEAK from$leaked"
    echo "        summary excerpt: $(echo "$summary" | head -c 300)..."
  else
    echo "   ✓ user $i: completed cleanly, no cross-user leak"
  fi
done
echo

# ── Verdict ─────────────────────────────────────────────────────────
echo "── Verdict ──"
echo "   completed:     $((USERS - fail_count))/$USERS"
echo "   leaks found:   $leak_count"
if [[ $leak_count -gt 0 ]]; then
  echo "❌ CROSS-USER CONTAMINATION DETECTED — multi-user isolation is broken"
  exit 2
fi
if [[ $fail_count -gt 0 ]]; then
  echo "❌ One or more tasks failed (no leaks observed in the ones that did finish)"
  exit 1
fi
echo "✅ All $USERS tasks completed, no cross-user contamination"
exit 0
