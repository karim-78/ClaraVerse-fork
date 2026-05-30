//go:build integration

// Integration tests for the workflow MongoStateStore + concurrency limiter.
//
// What the contract guarantees and what these tests cover:
//
//   - Init upserts: re-calling Init for the same execution_id preserves
//     block_outputs (resume idempotency).
//   - CheckpointBlock writes per-block records keyed by block_id with `.`
//     sanitized (mongo dot-notation safety); refreshes heartbeat.
//   - GetBlockOutput is the idempotency cache — already-completed blocks
//     return their cached output on retry instead of re-firing.
//   - Heartbeat is cheap + isolates the orphan scanner from false positives.
//   - FindOrphaned returns running execs with stale heartbeat, ignores
//     fresh + completed ones.
//   - MarkCompleted flips status + stamps completed_at (which the TTL
//     index uses to prune in 30 days).
//   - sanitizeBSONKey protects against block IDs containing `.` or `$`.
//
// What's at stake: regression here either re-fires side-effecting blocks
// (a duplicate Stripe charge, a re-sent email) or fails to resume a
// crashed workflow.
package execution

import (
	"context"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"

	"claraverse/internal/database"
	"claraverse/internal/models"
)

func mongoBaseURI() string {
	if v := os.Getenv("MONGODB_URI"); v != "" {
		if i := strings.LastIndex(v, "/"); i > strings.Index(v, "://")+2 {
			v = v[:i]
		}
		return v
	}
	return "mongodb://localhost:27017"
}

func newTestMongo(t *testing.T) (*database.MongoDB, func()) {
	t.Helper()
	dbName := fmt.Sprintf("claraverse_itest_exec_%d_%d", time.Now().UnixNano(), os.Getpid())
	uri := mongoBaseURI() + "/" + dbName
	md, err := database.NewMongoDB(uri)
	if err != nil {
		t.Skipf("mongo unreachable: %v", err)
	}
	return md, func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = md.Database().Drop(ctx)
		_ = md.Close(context.Background())
	}
}

func mustOK(t *testing.T, err error, msg string) {
	t.Helper()
	if err != nil {
		t.Fatalf("%s: %v", msg, err)
	}
}

func TestMongoStateStore_FullLifecycle(t *testing.T) {
	md, cleanup := newTestMongo(t)
	defer cleanup()

	store, err := NewMongoStateStore(md)
	mustOK(t, err, "construct store")

	ctx := context.Background()
	execID := "exec-lifecycle-1"
	wfID := "wf-1"
	userID := "u-1"
	snapshot := &models.Workflow{ID: "wf-1", AgentID: "agent-1"}
	input := map[string]any{"k": "v"}

	// --- Init ---------------------------------------------------------
	mustOK(t, store.Init(ctx, execID, wfID, userID, snapshot, input), "init")

	st, err := store.Load(ctx, execID)
	mustOK(t, err, "load after init")
	if st == nil {
		t.Fatal("load returned nil after init")
	}
	if st.Status != StateStatusRunning {
		t.Errorf("expected status=running, got %s", st.Status)
	}

	// --- CheckpointBlock ---------------------------------------------
	ckpt := BlockCheckpoint{
		Output:     map[string]any{"result": "ok", "count": 42},
		InputHash:  "hash-1",
		DurationMs: 123,
	}
	mustOK(t, store.CheckpointBlock(ctx, execID, "block-A", ckpt), "checkpoint A")

	// --- GetBlockOutput hit -----------------------------------------
	got, hit, err := store.GetBlockOutput(ctx, execID, "block-A")
	mustOK(t, err, "GetBlockOutput")
	if !hit {
		t.Fatal("expected idempotency cache hit")
	}
	if got.Output["result"] != "ok" {
		t.Errorf("cached output mismatch: %v", got.Output)
	}
	if got.CompletedAt.IsZero() {
		t.Error("CompletedAt should be populated on checkpoint")
	}

	// --- GetBlockOutput miss ----------------------------------------
	_, hit, _ = store.GetBlockOutput(ctx, execID, "block-B")
	if hit {
		t.Error("unexpected hit for unwritten block")
	}

	// --- Idempotent re-Init preserves checkpoints --------------------
	// Resume path: backend restarts, calls Init again for the same
	// execution_id. The block we already finished must NOT be re-fired.
	mustOK(t, store.Init(ctx, execID, wfID, userID, snapshot, input), "re-init")
	got, hit, _ = store.GetBlockOutput(ctx, execID, "block-A")
	if !hit {
		t.Fatal("re-init wiped block_outputs — would cause duplicate side effects on resume")
	}

	// --- Heartbeat ---------------------------------------------------
	stBefore, _ := store.Load(ctx, execID)
	time.Sleep(15 * time.Millisecond)
	mustOK(t, store.Heartbeat(ctx, execID), "heartbeat")
	stAfter, _ := store.Load(ctx, execID)
	if !stAfter.LastHeartbeatAt.After(stBefore.LastHeartbeatAt) {
		t.Error("heartbeat did not advance last_heartbeat_at")
	}

	// --- MarkCompleted ------------------------------------------------
	mustOK(t, store.MarkCompleted(ctx, execID, StateStatusCompleted), "mark completed")
	st, _ = store.Load(ctx, execID)
	if st.Status != StateStatusCompleted {
		t.Errorf("expected status=completed, got %s", st.Status)
	}
	if st.CompletedAt == nil || st.CompletedAt.IsZero() {
		t.Error("CompletedAt should be set after MarkCompleted")
	}
}

func TestMongoStateStore_OrphanDetection(t *testing.T) {
	md, cleanup := newTestMongo(t)
	defer cleanup()

	store, err := NewMongoStateStore(md)
	mustOK(t, err, "construct store")

	ctx := context.Background()

	// Two executions: one fresh, one stale.
	mustOK(t, store.Init(ctx, "fresh", "wf", "u", &models.Workflow{}, nil), "init fresh")
	mustOK(t, store.Init(ctx, "stale", "wf", "u", &models.Workflow{}, nil), "init stale")

	// Backdate the stale one's heartbeat past the threshold.
	coll := md.Collection("workflow_execution_state")
	_, err = coll.UpdateOne(ctx,
		map[string]any{"execution_id": "stale"},
		map[string]any{"$set": map[string]any{"last_heartbeat_at": time.Now().UTC().Add(-5 * time.Minute)}},
	)
	mustOK(t, err, "backdate")

	orphans, err := store.FindOrphaned(ctx, OrphanThreshold)
	mustOK(t, err, "FindOrphaned")
	if len(orphans) != 1 {
		t.Fatalf("expected 1 orphan, got %d", len(orphans))
	}
	if orphans[0].ExecutionID != "stale" {
		t.Fatalf("wrong orphan returned: %s", orphans[0].ExecutionID)
	}

	// MarkCompleted should remove from orphan scan even with stale heartbeat.
	mustOK(t, store.MarkCompleted(ctx, "stale", StateStatusFailed), "mark stale failed")
	orphans, _ = store.FindOrphaned(ctx, OrphanThreshold)
	if len(orphans) != 0 {
		t.Errorf("completed orphan still in scan: %d", len(orphans))
	}
}

func TestMongoStateStore_BlockIDDotSanitization(t *testing.T) {
	// Block IDs containing `.` or `$` would silently corrupt the nested
	// document (mongo treats them as path separators / operators). The
	// sanitizer must replace them BEFORE the write, and reads must
	// route through the same sanitizer.
	md, cleanup := newTestMongo(t)
	defer cleanup()

	store, err := NewMongoStateStore(md)
	mustOK(t, err, "construct store")

	ctx := context.Background()
	mustOK(t, store.Init(ctx, "exec-dots", "wf", "u", &models.Workflow{}, nil), "init")

	weirdID := "block.with.dots$and$dollars"
	mustOK(t, store.CheckpointBlock(ctx, "exec-dots", weirdID, BlockCheckpoint{
		Output:    map[string]any{"x": 1},
		InputHash: "h",
	}), "checkpoint weird id")

	_, hit, err := store.GetBlockOutput(ctx, "exec-dots", weirdID)
	mustOK(t, err, "Get with weird id")
	if !hit {
		t.Fatal("dot-containing block ID not retrievable — sanitizer broken")
	}
}

func TestWorkflowLimiter_PerWorkflowCap(t *testing.T) {
	// The limiter caps concurrent executions per workflow_id. Acquiring
	// past the limit returns a LimiterError (which the HTTP handler maps
	// to 429 + Retry-After). Release lets the next caller in.
	limiter := NewWorkflowLimiter(2)

	mustOK(t, limiter.Acquire("wf-A"), "first acquire")
	mustOK(t, limiter.Acquire("wf-A"), "second acquire")

	err := limiter.Acquire("wf-A")
	if err == nil {
		t.Fatal("third acquire should have been rejected")
	}
	if _, ok := err.(*LimiterError); !ok {
		t.Errorf("expected *LimiterError, got %T", err)
	}

	// A different workflow shares no slots.
	mustOK(t, limiter.Acquire("wf-B"), "different workflow not blocked")

	// Release frees a slot.
	limiter.Release("wf-A")
	mustOK(t, limiter.Acquire("wf-A"), "after release")

	// Stats sanity.
	stats := limiter.Stats()
	if stats["wf-A"] != 2 {
		t.Errorf("expected 2 in flight on wf-A, got %d", stats["wf-A"])
	}
	if limiter.Limit() != 2 {
		t.Errorf("expected limit=2, got %d", limiter.Limit())
	}
}

func TestWorkflowLimiter_DefaultLimit(t *testing.T) {
	limiter := DefaultLimiter()
	if limiter.Limit() <= 0 {
		t.Errorf("default limit should be > 0, got %d", limiter.Limit())
	}
}
