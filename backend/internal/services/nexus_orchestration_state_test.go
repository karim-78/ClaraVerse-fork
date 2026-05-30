//go:build integration

// Integration tests for the Nexus durability store.
//
// What the contract guarantees and what these tests cover:
//
//   - Init upserts: re-calling Init for the same (session, parent_task)
//     keeps the existing CompletedDaemons map intact while bumping
//     heartbeat and status — required for resume idempotency.
//   - CheckpointDaemon writes per-index records and refreshes heartbeat
//     so the orphan scanner doesn't trip mid-run.
//   - Heartbeat bumps last_heartbeat_at without touching anything else.
//   - FindOrphaned returns running docs with stale heartbeats, ignores
//     fresh ones, and ignores completed ones.
//   - MarkCompleted sets terminal status + completed_at and removes the
//     doc from future orphan scans.
//   - Required-field validation on Init.
//
// These are the properties the boot-time auto-resume in main.go relies
// on. A regression here either re-runs finished work or fails to resume.
package services

import (
	"context"
	"testing"
	"time"

	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/bson/primitive"
)

func TestNexusOrchStore_LifecycleAndOrphanDetection(t *testing.T) {
	md, cleanup := newTestMongo(t)
	defer cleanup()

	store, err := NewNexusOrchestrationStore(md)
	mustOK(t, err, "construct store")

	ctx := context.Background()
	sessionID := primitive.NewObjectID()
	parentTaskID := primitive.NewObjectID()

	// --- Init ---------------------------------------------------------
	state := &NexusOrchestrationState{
		SessionID:       sessionID,
		UserID:          "u-test",
		ParentTaskID:    parentTaskID,
		ModelID:         "test-model",
		OriginalMessage: "test message",
		Plans: []DaemonPlan{
			{Index: 0, Role: "researcher", RoleLabel: "Researcher Daemon"},
			{Index: 1, Role: "summarizer", RoleLabel: "Summarizer Daemon", DependsOn: []int{0}},
		},
		DaemonIDs: map[string]primitive.ObjectID{
			"0": primitive.NewObjectID(),
			"1": primitive.NewObjectID(),
		},
	}
	mustOK(t, store.Init(ctx, state), "init state")

	// --- Verify Init shape via raw read -------------------------------
	coll := md.Collection("nexus_orchestration_state")
	var doc bson.M
	mustOK(t, coll.FindOne(ctx, bson.M{"parent_task_id": parentTaskID}).Decode(&doc),
		"read back state")
	if doc["status"] != string(NexusOrchStatusRunning) {
		t.Fatalf("expected status=running, got %v", doc["status"])
	}
	if doc["original_message"] != "test message" {
		t.Fatalf("original_message round-trip failed: %v", doc["original_message"])
	}

	// --- Idempotent re-Init ------------------------------------------
	// Calling Init twice (e.g. on resume) must not reset CompletedDaemons.
	// First seed a checkpoint:
	mustOK(t, store.CheckpointDaemon(ctx, sessionID, parentTaskID, CompletedDaemonRecord{
		Index:    0,
		DaemonID: "daemon-0-hex",
		Role:     "researcher",
		Summary:  "found 3 sources",
	}), "checkpoint daemon 0")

	// Re-Init same state; should NOT clobber completed_daemons (uses $setOnInsert).
	mustOK(t, store.Init(ctx, state), "re-init")
	mustOK(t, coll.FindOne(ctx, bson.M{"parent_task_id": parentTaskID}).Decode(&doc),
		"re-read after re-init")
	completed, _ := doc["completed_daemons"].(bson.M)
	if completed == nil || completed["0"] == nil {
		t.Fatalf("re-init clobbered completed_daemons: %v", doc["completed_daemons"])
	}

	// --- FindOrphaned: fresh heartbeat shouldn't be returned ---------
	orphans, err := store.FindOrphaned(ctx, 90*time.Second)
	mustOK(t, err, "FindOrphaned fresh")
	if len(orphans) != 0 {
		t.Fatalf("fresh orchestration appeared as orphan: %d", len(orphans))
	}

	// --- Stale heartbeat -> appears as orphan -----------------------
	_, err = coll.UpdateOne(ctx,
		bson.M{"parent_task_id": parentTaskID},
		bson.M{"$set": bson.M{"last_heartbeat_at": time.Now().UTC().Add(-5 * time.Minute)}},
	)
	mustOK(t, err, "backdate heartbeat")
	orphans, err = store.FindOrphaned(ctx, 90*time.Second)
	mustOK(t, err, "FindOrphaned stale")
	if len(orphans) != 1 {
		t.Fatalf("expected 1 orphan, got %d", len(orphans))
	}
	if orphans[0].ParentTaskID != parentTaskID {
		t.Fatalf("wrong orphan returned: %s", orphans[0].ParentTaskID.Hex())
	}
	if got := len(orphans[0].CompletedDaemons); got != 1 {
		t.Fatalf("orphan should report 1 completed daemon, got %d", got)
	}

	// --- Heartbeat clears orphan status ------------------------------
	mustOK(t, store.Heartbeat(ctx, sessionID, parentTaskID), "heartbeat")
	orphans, err = store.FindOrphaned(ctx, 90*time.Second)
	mustOK(t, err, "FindOrphaned after heartbeat")
	if len(orphans) != 0 {
		t.Fatalf("heartbeat did not clear orphan: %d", len(orphans))
	}

	// --- MarkCompleted -> not orphan-eligible even with stale heartbeat
	_, _ = coll.UpdateOne(ctx,
		bson.M{"parent_task_id": parentTaskID},
		bson.M{"$set": bson.M{"last_heartbeat_at": time.Now().UTC().Add(-5 * time.Minute)}},
	)
	mustOK(t, store.MarkCompleted(ctx, sessionID, parentTaskID, NexusOrchStatusCompleted),
		"mark completed")
	orphans, err = store.FindOrphaned(ctx, 90*time.Second)
	mustOK(t, err, "FindOrphaned post-complete")
	if len(orphans) != 0 {
		t.Fatalf("completed orchestration still treated as orphan: %d", len(orphans))
	}

	// Status should be flipped, completed_at set.
	mustOK(t, coll.FindOne(ctx, bson.M{"parent_task_id": parentTaskID}).Decode(&doc),
		"final read")
	if doc["status"] != string(NexusOrchStatusCompleted) {
		t.Fatalf("expected status=completed, got %v", doc["status"])
	}
	if _, ok := doc["completed_at"]; !ok {
		t.Fatal("completed_at should be set after MarkCompleted")
	}
}

func TestNexusOrchStore_InitValidation(t *testing.T) {
	md, cleanup := newTestMongo(t)
	defer cleanup()

	store, err := NewNexusOrchestrationStore(md)
	mustOK(t, err, "construct store")

	ctx := context.Background()

	// Missing session_id should be rejected.
	err = store.Init(ctx, &NexusOrchestrationState{
		ParentTaskID: primitive.NewObjectID(),
	})
	if err == nil {
		t.Fatal("expected error for missing session_id")
	}

	// Missing parent_task_id should be rejected.
	err = store.Init(ctx, &NexusOrchestrationState{
		SessionID: primitive.NewObjectID(),
	})
	if err == nil {
		t.Fatal("expected error for missing parent_task_id")
	}
}

func TestNexusOrchStore_CheckpointDaemon_OverwriteSemantics(t *testing.T) {
	// A daemon that completes, then somehow re-completes (e.g. a faulty
	// retry path) should produce a single record per index, with the
	// latest summary winning. This protects against duplicate work bloat.
	md, cleanup := newTestMongo(t)
	defer cleanup()

	store, err := NewNexusOrchestrationStore(md)
	mustOK(t, err, "construct store")

	ctx := context.Background()
	sessionID := primitive.NewObjectID()
	parentTaskID := primitive.NewObjectID()

	mustOK(t, store.Init(ctx, &NexusOrchestrationState{
		SessionID:    sessionID,
		UserID:       "u-test",
		ParentTaskID: parentTaskID,
		Plans:        []DaemonPlan{{Index: 0, Role: "x"}},
		DaemonIDs:    map[string]primitive.ObjectID{"0": primitive.NewObjectID()},
	}), "init")

	first := CompletedDaemonRecord{
		Index: 0, DaemonID: "d", Role: "x", Summary: "first result",
	}
	second := CompletedDaemonRecord{
		Index: 0, DaemonID: "d", Role: "x", Summary: "second result",
	}
	mustOK(t, store.CheckpointDaemon(ctx, sessionID, parentTaskID, first), "first ckpt")
	mustOK(t, store.CheckpointDaemon(ctx, sessionID, parentTaskID, second), "second ckpt")

	var doc bson.M
	coll := md.Collection("nexus_orchestration_state")
	mustOK(t, coll.FindOne(ctx, bson.M{"parent_task_id": parentTaskID}).Decode(&doc),
		"read")
	cd, _ := doc["completed_daemons"].(bson.M)
	if len(cd) != 1 {
		t.Fatalf("expected 1 completed daemon entry, got %d", len(cd))
	}
	entry, _ := cd["0"].(bson.M)
	if entry["summary"] != "second result" {
		t.Fatalf("latest summary should win, got %v", entry["summary"])
	}
}
