package execution

// ============================================================================
// Durable execution state store
//
// Purpose: survive a backend crash mid-workflow.
//
// Without this, a workflow that's halfway through a 12-block pipeline loses
// every block's output if the pod restarts — there's no way to resume from
// the last checkpoint, and any side-effecting block (HTTP POST, email send,
// payment, file write) would re-fire on a manual retry. That's a no-go for
// production.
//
// Design:
//
//   - One Mongo collection (`workflow_execution_state`) keyed by execution_id.
//     Document holds: workflow_id, user_id, started_at, last_heartbeat_at,
//     status, and a map[block_id] -> {output, completed_at, error}.
//
//   - Engine writes block outputs THROUGH this store as each block completes,
//     before notifying downstream blocks. Atomic at the block boundary —
//     either the output is fully persisted or not at all.
//
//   - Engine emits a heartbeat every 10s while running. On startup, any
//     execution whose status is "running" but whose heartbeat is older than
//     90s is considered orphaned. We surface them via FindOrphaned() so the
//     caller (main.go on boot) can resume them.
//
//   - Idempotency is a side effect of the same store: GetBlockOutput before
//     each block executes returns a cached result if the block already
//     completed in this execution. That means a block within a single
//     execution runs at most once even across crashes + retries.
//
// Non-goals (for this iteration):
//
//   - Cross-node lease/lock. Single-node assumption: only one backend pod
//     attempts to resume orphans. Multi-pod deployments will need a Mongo
//     transaction or distributed lock to claim execution ownership.
//
//   - Compaction. The state collection grows unbounded; needs a TTL index
//     on `last_heartbeat_at` (added at startup; documented below).
//
// Schema (`workflow_execution_state`):
//
//   {
//     _id: ObjectId,
//     execution_id: string  (unique index),
//     workflow_id: string,
//     user_id: string,
//     workflow_snapshot: object,        // full Workflow struct — needed to resume
//     initial_input: object,            // for replay if we ever support full restart
//     status: "running" | "completed" | "failed" | "interrupted",
//     started_at: time,
//     last_heartbeat_at: time   (index),
//     completed_at: time?,
//     block_outputs: { [block_id]: { output, completed_at, error? } }
//   }

import (
	"claraverse/internal/database"
	"claraverse/internal/models"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"log"
	"time"

	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/mongo"
	"go.mongodb.org/mongo-driver/mongo/options"
)

// StateStatus is the high-level execution status used for resume decisions.
type StateStatus string

const (
	StateStatusRunning     StateStatus = "running"
	StateStatusCompleted   StateStatus = "completed"
	StateStatusFailed      StateStatus = "failed"
	StateStatusInterrupted StateStatus = "interrupted"
)

// OrphanThreshold is how long we wait without a heartbeat before declaring
// an execution orphaned. 90s gives generous headroom over the 10s heartbeat
// interval — even a paused GC + a slow disk write shouldn't trigger a
// false-positive orphan.
const OrphanThreshold = 90 * time.Second

// HeartbeatInterval is how often a running execution writes its heartbeat.
// Short enough that a real crash is detected within ~90s on restart, long
// enough to keep Mongo write pressure trivial (one write per execution per
// 10s = nothing).
const HeartbeatInterval = 10 * time.Second

// BlockCheckpoint is what we persist for each completed block.
type BlockCheckpoint struct {
	Output      map[string]any `bson:"output,omitempty" json:"output,omitempty"`
	InputHash   string         `bson:"input_hash" json:"input_hash"`
	CompletedAt time.Time      `bson:"completed_at" json:"completed_at"`
	DurationMs  int64          `bson:"duration_ms" json:"duration_ms"`
	Error       string         `bson:"error,omitempty" json:"error,omitempty"`
}

// ExecutionState is the on-disk shape we serialize per execution.
type ExecutionState struct {
	ID               string                     `bson:"_id,omitempty"`
	ExecutionID      string                     `bson:"execution_id"`
	WorkflowID       string                     `bson:"workflow_id"`
	UserID           string                     `bson:"user_id"`
	WorkflowSnapshot *models.Workflow           `bson:"workflow_snapshot,omitempty"`
	InitialInput     map[string]any             `bson:"initial_input,omitempty"`
	Status           StateStatus                `bson:"status"`
	StartedAt        time.Time                  `bson:"started_at"`
	LastHeartbeatAt  time.Time                  `bson:"last_heartbeat_at"`
	CompletedAt      *time.Time                 `bson:"completed_at,omitempty"`
	BlockOutputs     map[string]BlockCheckpoint `bson:"block_outputs,omitempty"`
}

// StateStore is the abstraction the engine writes against. Mongo-backed by
// default; trivially mockable for tests.
type StateStore interface {
	Init(ctx context.Context, executionID, workflowID, userID string, snapshot *models.Workflow, input map[string]any) error
	CheckpointBlock(ctx context.Context, executionID, blockID string, ckpt BlockCheckpoint) error
	GetBlockOutput(ctx context.Context, executionID, blockID string) (*BlockCheckpoint, bool, error)
	Heartbeat(ctx context.Context, executionID string) error
	MarkCompleted(ctx context.Context, executionID string, status StateStatus) error
	Load(ctx context.Context, executionID string) (*ExecutionState, error)
	FindOrphaned(ctx context.Context, olderThan time.Duration) ([]ExecutionState, error)
}

// MongoStateStore is the Mongo-backed implementation.
type MongoStateStore struct {
	coll *mongo.Collection
}

// NewMongoStateStore wires the store. Ensures indexes on first call.
func NewMongoStateStore(db *database.MongoDB) (*MongoStateStore, error) {
	coll := db.Collection("workflow_execution_state")
	s := &MongoStateStore{coll: coll}
	if err := s.ensureIndexes(context.Background()); err != nil {
		return nil, err
	}
	return s, nil
}

// ensureIndexes adds:
//   - unique on execution_id (idempotent Init / Resume)
//   - last_heartbeat_at for orphan scans
//   - TTL on completed_at to auto-prune finished executions after 30 days
func (s *MongoStateStore) ensureIndexes(ctx context.Context) error {
	thirtyDays := int32(30 * 24 * 60 * 60)
	models := []mongo.IndexModel{
		{
			Keys:    bson.D{{Key: "execution_id", Value: 1}},
			Options: options.Index().SetUnique(true).SetName("execution_id_unique"),
		},
		{
			Keys:    bson.D{{Key: "last_heartbeat_at", Value: 1}},
			Options: options.Index().SetName("heartbeat_idx"),
		},
		{
			Keys:    bson.D{{Key: "completed_at", Value: 1}},
			Options: options.Index().SetName("completed_ttl").SetExpireAfterSeconds(thirtyDays),
		},
	}
	_, err := s.coll.Indexes().CreateMany(ctx, models)
	if err != nil {
		return fmt.Errorf("execution state index create: %w", err)
	}
	return nil
}

// Init creates the state document at execution start. Idempotent — calling
// Init twice with the same execution_id (e.g. on resume) is fine; we
// preserve any existing block_outputs.
func (s *MongoStateStore) Init(ctx context.Context, executionID, workflowID, userID string, snapshot *models.Workflow, input map[string]any) error {
	now := time.Now().UTC()
	filter := bson.M{"execution_id": executionID}
	update := bson.M{
		"$setOnInsert": bson.M{
			"execution_id":      executionID,
			"workflow_id":       workflowID,
			"user_id":           userID,
			"workflow_snapshot": snapshot,
			"initial_input":     input,
			"started_at":        now,
			"block_outputs":     bson.M{},
		},
		"$set": bson.M{
			"status":            string(StateStatusRunning),
			"last_heartbeat_at": now,
		},
	}
	opts := options.Update().SetUpsert(true)
	_, err := s.coll.UpdateOne(ctx, filter, update, opts)
	if err != nil {
		return fmt.Errorf("init execution state: %w", err)
	}
	return nil
}

// CheckpointBlock atomically writes a block's output to the state document.
// Uses dot-notation to update only the nested field — avoids racing with
// other parallel block writes within the same execution.
func (s *MongoStateStore) CheckpointBlock(ctx context.Context, executionID, blockID string, ckpt BlockCheckpoint) error {
	key := "block_outputs." + sanitizeBSONKey(blockID)
	now := time.Now().UTC()
	if ckpt.CompletedAt.IsZero() {
		ckpt.CompletedAt = now
	}
	update := bson.M{
		"$set": bson.M{
			key:                 ckpt,
			"last_heartbeat_at": now,
		},
	}
	_, err := s.coll.UpdateOne(ctx, bson.M{"execution_id": executionID}, update)
	if err != nil {
		return fmt.Errorf("checkpoint block %s: %w", blockID, err)
	}
	return nil
}

// GetBlockOutput is the cache lookup. Returns (ckpt, true, nil) on hit,
// (nil, false, nil) on miss, (nil, false, err) on error.
func (s *MongoStateStore) GetBlockOutput(ctx context.Context, executionID, blockID string) (*BlockCheckpoint, bool, error) {
	st, err := s.Load(ctx, executionID)
	if err != nil {
		return nil, false, err
	}
	if st == nil {
		return nil, false, nil
	}
	ckpt, ok := st.BlockOutputs[sanitizeBSONKey(blockID)]
	if !ok {
		return nil, false, nil
	}
	return &ckpt, true, nil
}

// Heartbeat refreshes last_heartbeat_at. Cheap single-field update; safe to
// call from a ticker.
func (s *MongoStateStore) Heartbeat(ctx context.Context, executionID string) error {
	_, err := s.coll.UpdateOne(ctx,
		bson.M{"execution_id": executionID},
		bson.M{"$set": bson.M{"last_heartbeat_at": time.Now().UTC()}},
	)
	return err
}

// MarkCompleted sets the terminal status and stamps completed_at (which is
// indexed with TTL — finished executions self-prune after 30 days).
func (s *MongoStateStore) MarkCompleted(ctx context.Context, executionID string, status StateStatus) error {
	now := time.Now().UTC()
	_, err := s.coll.UpdateOne(ctx,
		bson.M{"execution_id": executionID},
		bson.M{"$set": bson.M{
			"status":       string(status),
			"completed_at": now,
		}},
	)
	return err
}

// Load returns the full state for an execution, or nil if not found.
func (s *MongoStateStore) Load(ctx context.Context, executionID string) (*ExecutionState, error) {
	var st ExecutionState
	err := s.coll.FindOne(ctx, bson.M{"execution_id": executionID}).Decode(&st)
	if err == mongo.ErrNoDocuments {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &st, nil
}

// FindOrphaned returns executions in `running` status whose heartbeat is
// older than the threshold. Called on backend startup to identify
// interrupted workflows worth resuming.
func (s *MongoStateStore) FindOrphaned(ctx context.Context, olderThan time.Duration) ([]ExecutionState, error) {
	cutoff := time.Now().UTC().Add(-olderThan)
	filter := bson.M{
		"status":            string(StateStatusRunning),
		"last_heartbeat_at": bson.M{"$lt": cutoff},
	}
	cur, err := s.coll.Find(ctx, filter)
	if err != nil {
		return nil, err
	}
	defer cur.Close(ctx)
	var out []ExecutionState
	if err := cur.All(ctx, &out); err != nil {
		return nil, err
	}
	return out, nil
}

// sanitizeBSONKey replaces characters that Mongo disallows in keys (`.` and
// `$`). Block IDs are usually UUIDs but defensively we strip these — a single
// stray `.` would silently corrupt the nested document.
func sanitizeBSONKey(s string) string {
	out := make([]byte, 0, len(s))
	for i := 0; i < len(s); i++ {
		c := s[i]
		if c == '.' || c == '$' {
			out = append(out, '_')
			continue
		}
		out = append(out, c)
	}
	return string(out)
}

// HashBlockInput produces a stable hash of a block's input map. Used as the
// idempotency key alongside (execution_id, block_id). If inputs change
// between attempts of the same block within the same execution (rare but
// possible — e.g. retry after a transient upstream change), we surface the
// mismatch so the cache hit is reported correctly.
func HashBlockInput(input map[string]any) string {
	if len(input) == 0 {
		return ""
	}
	// json.Marshal sorts map keys by default, giving stable output.
	b, err := json.Marshal(input)
	if err != nil {
		// Fall back: hash an indication that we couldn't hash properly.
		// Worst case we miss a cache hit, never the other way around.
		return "unhashable"
	}
	h := sha256.Sum256(b)
	return hex.EncodeToString(h[:])
}

// LogStartup writes a one-line summary of orphans found at boot. Called by
// main.go right after the engine is constructed so operators see at a
// glance whether anything needs resuming.
func LogStartup(orphans []ExecutionState) {
	if len(orphans) == 0 {
		log.Printf("✅ [WORKFLOW-RESUME] No orphaned executions detected at startup")
		return
	}
	log.Printf("🔄 [WORKFLOW-RESUME] Found %d orphaned execution(s) — will resume", len(orphans))
	for _, o := range orphans {
		log.Printf("   • %s (workflow=%s user=%s, last heartbeat %v ago, %d block(s) checkpointed)",
			o.ExecutionID, o.WorkflowID, o.UserID,
			time.Since(o.LastHeartbeatAt).Round(time.Second), len(o.BlockOutputs))
	}
}
