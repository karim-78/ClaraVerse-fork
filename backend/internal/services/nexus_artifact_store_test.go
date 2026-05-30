//go:build integration

// Integration tests for the Nexus artifact store — the structured
// daemon-to-daemon handoff layer.
//
// Contract under test:
//
//   - Produce inserts new artifacts or upserts when (session, name)
//     already exists. The upsert is what lets a daemon "refine" its own
//     artifact across iterations without polluting the catalogue.
//   - Read by (session, name) returns the full content; nil/nil when
//     missing so callers can distinguish missing from error.
//   - List returns slim summaries (no content) sorted most-recent first
//     so the catalogue we paste into a daemon's system prompt stays
//     small even when artifacts are big.
//   - DeleteForSession scopes to one session — protects multi-session
//     isolation when the orchestration retry path wipes a prior attempt.
//   - MaxArtifactContentBytes (1 MiB) caps any single artifact so a
//     buggy or hostile daemon can't fill Mongo.
//   - Required-field validation on Produce.
//
// These tests would catch: cross-session bleed, missing content cap,
// broken upsert, list returning content (size regression), or read
// returning a wrong artifact.
package services

import (
	"context"
	"strings"
	"testing"

	"go.mongodb.org/mongo-driver/bson/primitive"
)

func TestNexusArtifactStore_ProduceListReadDelete(t *testing.T) {
	md, cleanup := newTestMongo(t)
	defer cleanup()

	store, err := NewNexusArtifactStore(md)
	mustOK(t, err, "construct store")

	ctx := context.Background()
	userID := "u-artifact-test"
	sessionA := primitive.NewObjectID()
	sessionB := primitive.NewObjectID()
	daemon1 := primitive.NewObjectID()
	daemon2 := primitive.NewObjectID()

	// --- Produce in session A ----------------------------------------
	a1, err := store.Produce(ctx, userID, sessionA, daemon1,
		"research-notes", "text/markdown",
		"# Notes\n- finding 1\n- finding 2",
		"preliminary research")
	mustOK(t, err, "produce a1")
	if a1 == nil || a1.ID.IsZero() {
		t.Fatal("Produce should return doc with ID set")
	}

	_, err = store.Produce(ctx, userID, sessionA, daemon2,
		"draft-report", "text/markdown",
		"# Report\nWIP",
		"early draft")
	mustOK(t, err, "produce a2")

	// --- Cross-session artifact in session B (must NOT leak into A) --
	_, err = store.Produce(ctx, userID, sessionB, daemon1,
		"research-notes", // same name, different session — should not collide
		"text/markdown",
		"DIFFERENT SESSION",
		"should not appear in session A list")
	mustOK(t, err, "produce b (same name, different session)")

	// --- List for session A: exactly 2, no content ------------------
	summaries, err := store.List(ctx, sessionA)
	mustOK(t, err, "list session A")
	if len(summaries) != 2 {
		t.Fatalf("expected 2 artifacts in session A, got %d", len(summaries))
	}
	for _, s := range summaries {
		// NexusArtifactSummary has no Content field — that's the whole point.
		// We sanity-check SizeBytes is set so the picker can show sizes.
		if s.SizeBytes == 0 {
			t.Errorf("artifact %s should have non-zero SizeBytes", s.Name)
		}
	}

	// Sort is most-recent first: draft-report produced after research-notes.
	if summaries[0].Name != "draft-report" {
		t.Errorf("expected newest-first, got %s first", summaries[0].Name)
	}

	// --- Read by name -------------------------------------------------
	got, err := store.Read(ctx, sessionA, "research-notes")
	mustOK(t, err, "read research-notes")
	if got == nil {
		t.Fatal("read returned nil for existing artifact")
	}
	if !strings.Contains(got.Content, "finding 1") {
		t.Fatalf("read returned wrong content: %q", got.Content)
	}

	// --- Upsert: same (session, name) overwrites --------------------
	_, err = store.Produce(ctx, userID, sessionA, daemon2,
		"research-notes", "text/markdown",
		"# Updated notes",
		"refined")
	mustOK(t, err, "upsert research-notes")

	summaries, _ = store.List(ctx, sessionA)
	if len(summaries) != 2 {
		t.Fatalf("upsert should not increase count, got %d", len(summaries))
	}
	got, _ = store.Read(ctx, sessionA, "research-notes")
	if got.Content != "# Updated notes" {
		t.Fatalf("upsert content not applied: %q", got.Content)
	}

	// --- Read missing returns nil/nil -------------------------------
	missing, err := store.Read(ctx, sessionA, "does-not-exist")
	mustOK(t, err, "read missing should not error")
	if missing != nil {
		t.Fatal("read missing should return nil")
	}

	// --- DeleteForSession scopes to one session ---------------------
	count, err := store.DeleteForSession(ctx, sessionA)
	mustOK(t, err, "delete session A")
	if count != 2 {
		t.Fatalf("expected 2 deletions, got %d", count)
	}
	summaries, _ = store.List(ctx, sessionA)
	if len(summaries) != 0 {
		t.Fatalf("session A should be empty after delete, got %d", len(summaries))
	}
	summaries, _ = store.List(ctx, sessionB)
	if len(summaries) != 1 {
		t.Fatalf("session B should be untouched, got %d", len(summaries))
	}
}

func TestNexusArtifactStore_ContentSizeCap(t *testing.T) {
	// A 1 MiB+1 byte artifact must be rejected so a buggy daemon can't
	// fill Mongo with multi-MB blobs.
	md, cleanup := newTestMongo(t)
	defer cleanup()

	store, err := NewNexusArtifactStore(md)
	mustOK(t, err, "construct store")

	ctx := context.Background()
	huge := strings.Repeat("x", MaxArtifactContentBytes+1)

	_, err = store.Produce(ctx, "u", primitive.NewObjectID(), primitive.NewObjectID(),
		"too-big", "text", huge, "")
	if err == nil {
		t.Fatal("expected over-cap content to be rejected")
	}
	if !strings.Contains(err.Error(), "too large") {
		t.Errorf("expected 'too large' in error, got: %v", err)
	}
}

func TestNexusArtifactStore_ProduceValidation(t *testing.T) {
	md, cleanup := newTestMongo(t)
	defer cleanup()

	store, err := NewNexusArtifactStore(md)
	mustOK(t, err, "construct store")

	ctx := context.Background()
	sid := primitive.NewObjectID()
	did := primitive.NewObjectID()

	// Empty user
	if _, err := store.Produce(ctx, "", sid, did, "n", "text", "x", ""); err == nil {
		t.Error("expected error for empty user_id")
	}
	// Zero session
	if _, err := store.Produce(ctx, "u", primitive.NilObjectID, did, "n", "text", "x", ""); err == nil {
		t.Error("expected error for zero session_id")
	}
	// Empty name
	if _, err := store.Produce(ctx, "u", sid, did, "", "text", "x", ""); err == nil {
		t.Error("expected error for empty name")
	}
}

func TestNexusArtifactStore_ContentTypeDefault(t *testing.T) {
	// Empty content_type should default to "text" so the daemon doesn't
	// have to specify it for plain text artifacts.
	md, cleanup := newTestMongo(t)
	defer cleanup()

	store, err := NewNexusArtifactStore(md)
	mustOK(t, err, "construct store")

	ctx := context.Background()
	sid := primitive.NewObjectID()
	doc, err := store.Produce(ctx, "u", sid, primitive.NewObjectID(),
		"plain", "", "just text", "")
	mustOK(t, err, "produce with empty content_type")
	if doc.ContentType != "text" {
		t.Errorf("expected default content_type=text, got %q", doc.ContentType)
	}
}
