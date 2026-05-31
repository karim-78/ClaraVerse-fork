package services

// ============================================================================
// Nexus orchestration state — durability + resume for multi-daemon runs.
//
// Mirrors what execution.MongoStateStore does for workflows, but scoped
// to Nexus (which has its own data shapes: plans, daemons, sessions).
// Same operational properties:
//
//   • Per-orchestration document, keyed by session_id.
//   • Records the full DaemonPlan slice + each completed daemon's summary
//     so a resume rebuilds the DAG and skips already-done work.
//   • Heartbeat every 10s; orphans (no heartbeat in 90s) are picked up on
//     boot and resumed automatically.
//   • Idempotency falls out of the design: a re-run sees completed daemons
//     in the store and reuses their summaries instead of re-launching them.
//
// Limitations (acknowledged):
//   - Single-node only. Multi-node deployments need a Mongo lease to
//     ensure only one pod resumes a given orphan.
//   - The Daemon documents themselves still live in the `daemons`
//     collection; we point at them by ObjectID rather than embedding.
//     Future hardening could embed for full self-containment.

import (
	"claraverse/internal/database"
	"context"
	"fmt"
	"log"
	"time"

	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/bson/primitive"
	"go.mongodb.org/mongo-driver/mongo"
	"go.mongodb.org/mongo-driver/mongo/options"
)

// NexusOrchestrationStatus is the lifecycle stamp.
type NexusOrchestrationStatus string

const (
	NexusOrchStatusRunning     NexusOrchestrationStatus = "running"
	NexusOrchStatusCompleted   NexusOrchestrationStatus = "completed"
	NexusOrchStatusFailed      NexusOrchestrationStatus = "failed"
	NexusOrchStatusInterrupted NexusOrchestrationStatus = "interrupted"
)

// NexusOrchOrphanThreshold mirrors the workflow constant — if no heartbeat
// in 90s, assume the pod crashed and consider this orchestration orphaned.
const NexusOrchOrphanThreshold = 90 * time.Second

// NexusOrchHeartbeatInterval is the cadence the running orchestration
// pings the store at. 10s is a fine balance between recovery latency
// and Mongo write pressure.
const NexusOrchHeartbeatInterval = 10 * time.Second

// CompletedDaemonRecord is what we persist per completed daemon — enough
// to skip re-execution on resume and to feed dependents.
type CompletedDaemonRecord struct {
	Index       int       `bson:"index" json:"index"`
	DaemonID    string    `bson:"daemon_id" json:"daemon_id"`
	Role        string    `bson:"role" json:"role"`
	RoleLabel   string    `bson:"role_label" json:"role_label"`
	Summary     string    `bson:"summary" json:"summary"`
	CompletedAt time.Time `bson:"completed_at" json:"completed_at"`
}

// NexusOrchestrationState is what we store per orchestration.
type NexusOrchestrationState struct {
	ID                  primitive.ObjectID                `bson:"_id,omitempty" json:"id"`
	SessionID           primitive.ObjectID                `bson:"session_id" json:"session_id"`
	UserID              string                            `bson:"user_id" json:"user_id"`
	ParentTaskID        primitive.ObjectID                `bson:"parent_task_id" json:"parent_task_id"`
	// ProjectID lets a resumed orchestration re-attach search_knowledge
	// to each daemon. Optional — older state records (or tasks created
	// without a project) leave this as the zero ObjectID.
	ProjectID           primitive.ObjectID                `bson:"project_id,omitempty" json:"project_id,omitempty"`
	ModelID             string                            `bson:"model_id" json:"model_id"`
	OriginalMessage     string                            `bson:"original_message" json:"original_message"`
	ProjectInstruction  string                            `bson:"project_instruction,omitempty" json:"project_instruction,omitempty"`
	IsRoutine           bool                              `bson:"is_routine" json:"is_routine"`
	Plans               []DaemonPlan                      `bson:"plans" json:"plans"`
	DaemonIDs           map[string]primitive.ObjectID     `bson:"daemon_ids" json:"daemon_ids"` // keyed by plan index (stringified)
	SkillIDs            []primitive.ObjectID              `bson:"skill_ids,omitempty" json:"skill_ids,omitempty"`
	Status              NexusOrchestrationStatus          `bson:"status" json:"status"`
	StartedAt           time.Time                         `bson:"started_at" json:"started_at"`
	LastHeartbeatAt     time.Time                         `bson:"last_heartbeat_at" json:"last_heartbeat_at"`
	CompletedAt         *time.Time                        `bson:"completed_at,omitempty" json:"completed_at,omitempty"`
	CompletedDaemons    map[string]CompletedDaemonRecord  `bson:"completed_daemons,omitempty" json:"completed_daemons,omitempty"`
}

// NexusOrchestrationStore is the mongo-backed implementation.
type NexusOrchestrationStore struct {
	coll *mongo.Collection
}

// NewNexusOrchestrationStore wires the store + ensures indexes.
func NewNexusOrchestrationStore(db *database.MongoDB) (*NexusOrchestrationStore, error) {
	coll := db.Collection("nexus_orchestration_state")
	s := &NexusOrchestrationStore{coll: coll}
	if err := s.ensureIndexes(context.Background()); err != nil {
		return nil, err
	}
	return s, nil
}

func (s *NexusOrchestrationStore) ensureIndexes(ctx context.Context) error {
	fourteenDays := int32(14 * 24 * 60 * 60)
	idx := []mongo.IndexModel{
		{
			Keys:    bson.D{{Key: "session_id", Value: 1}, {Key: "parent_task_id", Value: 1}},
			Options: options.Index().SetUnique(true).SetName("session_task_unique"),
		},
		{
			Keys:    bson.D{{Key: "last_heartbeat_at", Value: 1}},
			Options: options.Index().SetName("heartbeat_idx"),
		},
		{
			Keys:    bson.D{{Key: "completed_at", Value: 1}},
			Options: options.Index().SetExpireAfterSeconds(fourteenDays).SetName("orchestration_ttl"),
		},
	}
	_, err := s.coll.Indexes().CreateMany(ctx, idx)
	return err
}

// Init creates / refreshes the orchestration state document at start time.
// Upsert semantics — calling Init on resume keeps the existing
// CompletedDaemons map intact while bumping the heartbeat back to "now".
func (s *NexusOrchestrationStore) Init(ctx context.Context, st *NexusOrchestrationState) error {
	if st.SessionID.IsZero() {
		return fmt.Errorf("session_id required")
	}
	if st.ParentTaskID.IsZero() {
		return fmt.Errorf("parent_task_id required")
	}
	now := time.Now().UTC()
	filter := bson.M{"session_id": st.SessionID, "parent_task_id": st.ParentTaskID}
	insert := bson.M{
		"session_id":          st.SessionID,
		"user_id":             st.UserID,
		"parent_task_id":      st.ParentTaskID,
		"model_id":            st.ModelID,
		"original_message":    st.OriginalMessage,
		"project_instruction": st.ProjectInstruction,
		"is_routine":          st.IsRoutine,
		"plans":               st.Plans,
		"daemon_ids":          st.DaemonIDs,
		"skill_ids":           st.SkillIDs,
		"started_at":          now,
		"completed_daemons":   map[string]CompletedDaemonRecord{},
	}
	set := bson.M{
		"status":            string(NexusOrchStatusRunning),
		"last_heartbeat_at": now,
	}
	_, err := s.coll.UpdateOne(ctx, filter,
		bson.M{"$setOnInsert": insert, "$set": set},
		options.Update().SetUpsert(true),
	)
	return err
}

// CheckpointDaemon records a single daemon's completion. Used by the
// orchestrator's `update.Type == "completed"` branch.
func (s *NexusOrchestrationStore) CheckpointDaemon(
	ctx context.Context,
	sessionID, parentTaskID primitive.ObjectID,
	rec CompletedDaemonRecord,
) error {
	if rec.CompletedAt.IsZero() {
		rec.CompletedAt = time.Now().UTC()
	}
	key := fmt.Sprintf("completed_daemons.%d", rec.Index)
	update := bson.M{
		"$set": bson.M{
			key:                 rec,
			"last_heartbeat_at": time.Now().UTC(),
		},
	}
	_, err := s.coll.UpdateOne(ctx,
		bson.M{"session_id": sessionID, "parent_task_id": parentTaskID},
		update,
	)
	return err
}

// Heartbeat refreshes last_heartbeat_at. Called by a 10s ticker.
func (s *NexusOrchestrationStore) Heartbeat(ctx context.Context, sessionID, parentTaskID primitive.ObjectID) error {
	_, err := s.coll.UpdateOne(ctx,
		bson.M{"session_id": sessionID, "parent_task_id": parentTaskID},
		bson.M{"$set": bson.M{"last_heartbeat_at": time.Now().UTC()}},
	)
	return err
}

// MarkCompleted sets the terminal status and stamps completed_at, which
// triggers the 14-day TTL prune.
func (s *NexusOrchestrationStore) MarkCompleted(
	ctx context.Context,
	sessionID, parentTaskID primitive.ObjectID,
	status NexusOrchestrationStatus,
) error {
	now := time.Now().UTC()
	_, err := s.coll.UpdateOne(ctx,
		bson.M{"session_id": sessionID, "parent_task_id": parentTaskID},
		bson.M{"$set": bson.M{
			"status":       string(status),
			"completed_at": now,
		}},
	)
	return err
}

// FindOrphaned returns orchestrations in `running` status with stale
// heartbeats — the cortex service resumes each on startup.
func (s *NexusOrchestrationStore) FindOrphaned(ctx context.Context, olderThan time.Duration) ([]NexusOrchestrationState, error) {
	cutoff := time.Now().UTC().Add(-olderThan)
	filter := bson.M{
		"status":            string(NexusOrchStatusRunning),
		"last_heartbeat_at": bson.M{"$lt": cutoff},
	}
	cursor, err := s.coll.Find(ctx, filter)
	if err != nil {
		return nil, err
	}
	defer cursor.Close(ctx)
	var out []NexusOrchestrationState
	if err := cursor.All(ctx, &out); err != nil {
		return nil, err
	}
	return out, nil
}

// LogStartup writes a one-line summary of orphaned Nexus orchestrations
// at boot. Matches the workflow LogStartup signature so operators see a
// consistent format across both systems.
func LogNexusOrchestrationStartup(orphans []NexusOrchestrationState) {
	if len(orphans) == 0 {
		log.Printf("✅ [NEXUS-RESUME] No orphaned multi-daemon orchestrations at startup")
		return
	}
	log.Printf("🔄 [NEXUS-RESUME] Found %d orphaned Nexus orchestration(s) — will resume", len(orphans))
	for _, o := range orphans {
		log.Printf("   • session=%s task=%s plans=%d completed=%d (last heartbeat %v ago)",
			o.SessionID.Hex(), o.ParentTaskID.Hex(), len(o.Plans), len(o.CompletedDaemons),
			time.Since(o.LastHeartbeatAt).Round(time.Second))
	}
}
