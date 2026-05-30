package services

// ============================================================================
// Nexus artifact store — structured task-to-task handoff.
//
// Before this, daemon B saw daemon A's work as a single text string in its
// system prompt, truncated to 4000 chars. That's fine for "summarise"
// pipelines but useless when A produces a dataframe, JSON object, file
// reference, or anything structured.
//
// Now daemons produce + read named artifacts via tools (defined in
// internal/tools/nexus_artifact_tools.go):
//
//   produce_artifact(name, content_type, content, summary?)
//   list_artifacts()
//   read_artifact(name)
//
// Each artifact lives in mongo keyed by (user_id, session_id, name) so it
// survives the originating daemon and is visible to every downstream
// daemon in the same session. Successor daemons see a short
// description+name in their system prompt and pull the full content on
// demand — keeps the system prompt lean while making the full data
// accessible.
//
// Storage: collection `nexus_artifacts`, indexed by session for fast
// list, unique on (session, name) for upsert semantics, TTL 30 days on
// created_at so old session artifacts auto-prune.

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

// NexusArtifact is the document we persist. Self-contained: callers don't
// need to read the originating daemon to make use of the content.
type NexusArtifactDoc struct {
	ID          primitive.ObjectID `bson:"_id,omitempty" json:"id"`
	UserID      string             `bson:"user_id" json:"user_id"`
	SessionID   primitive.ObjectID `bson:"session_id" json:"session_id"`
	DaemonID    primitive.ObjectID `bson:"daemon_id,omitempty" json:"daemon_id,omitempty"`
	Name        string             `bson:"name" json:"name"`
	ContentType string             `bson:"content_type" json:"content_type"` // "text"|"json"|"markdown"|"csv"|"url"|...
	Content     string             `bson:"content" json:"content"`
	Summary     string             `bson:"summary,omitempty" json:"summary,omitempty"`
	SizeBytes   int                `bson:"size_bytes" json:"size_bytes"`
	CreatedAt   time.Time          `bson:"created_at" json:"created_at"`
}

// NexusArtifactStore is the mongo-backed implementation.
type NexusArtifactStore struct {
	coll *mongo.Collection
}

// NewNexusArtifactStore wires the store + ensures indexes.
func NewNexusArtifactStore(db *database.MongoDB) (*NexusArtifactStore, error) {
	coll := db.Collection("nexus_artifacts")
	s := &NexusArtifactStore{coll: coll}
	if err := s.ensureIndexes(context.Background()); err != nil {
		return nil, err
	}
	return s, nil
}

func (s *NexusArtifactStore) ensureIndexes(ctx context.Context) error {
	thirtyDays := int32(30 * 24 * 60 * 60)
	idx := []mongo.IndexModel{
		{
			// Upsert key: one artifact per (session, name) — re-producing
			// the same name from a later daemon overwrites the prior value
			// (intended behavior — "refine the brief" pipelines).
			Keys:    bson.D{{Key: "session_id", Value: 1}, {Key: "name", Value: 1}},
			Options: options.Index().SetUnique(true).SetName("session_name_unique"),
		},
		{
			Keys:    bson.D{{Key: "user_id", Value: 1}, {Key: "session_id", Value: 1}},
			Options: options.Index().SetName("by_user_session"),
		},
		{
			Keys:    bson.D{{Key: "created_at", Value: 1}},
			Options: options.Index().SetExpireAfterSeconds(thirtyDays).SetName("artifact_ttl"),
		},
	}
	_, err := s.coll.Indexes().CreateMany(ctx, idx)
	return err
}

// MaxArtifactContentBytes is the hard cap on a single artifact's content.
// 1 MiB is plenty for JSON, CSV summaries, draft text, etc. — large
// binary artifacts should land in file storage and the artifact would
// hold the URL/handle instead.
const MaxArtifactContentBytes = 1 * 1024 * 1024

// Produce upserts an artifact. Returns the stored document (with id +
// timestamp set) so the calling tool can return a stable reference to
// the model.
func (s *NexusArtifactStore) Produce(
	ctx context.Context,
	userID string,
	sessionID primitive.ObjectID,
	daemonID primitive.ObjectID,
	name, contentType, content, summary string,
) (*NexusArtifactDoc, error) {
	if userID == "" {
		return nil, fmt.Errorf("user_id required")
	}
	if sessionID.IsZero() {
		return nil, fmt.Errorf("session_id required")
	}
	if name == "" {
		return nil, fmt.Errorf("name required")
	}
	if len(content) > MaxArtifactContentBytes {
		return nil, fmt.Errorf("artifact content too large (%d bytes, max %d)",
			len(content), MaxArtifactContentBytes)
	}
	if contentType == "" {
		contentType = "text"
	}

	now := time.Now().UTC()
	doc := bson.M{
		"user_id":      userID,
		"session_id":   sessionID,
		"daemon_id":    daemonID,
		"name":         name,
		"content_type": contentType,
		"content":      content,
		"summary":      summary,
		"size_bytes":   len(content),
		"created_at":   now,
	}
	filter := bson.M{"session_id": sessionID, "name": name}
	update := bson.M{"$set": doc}
	opts := options.Update().SetUpsert(true)
	_, err := s.coll.UpdateOne(ctx, filter, update, opts)
	if err != nil {
		return nil, fmt.Errorf("upsert artifact: %w", err)
	}

	// Read back to get the generated _id.
	var saved NexusArtifactDoc
	if err := s.coll.FindOne(ctx, filter).Decode(&saved); err != nil {
		return nil, err
	}
	log.Printf("📦 [NEXUS-ARTIFACT] produced name=%q type=%s size=%d session=%s",
		name, contentType, len(content), sessionID.Hex())
	return &saved, nil
}

// Read fetches a single artifact by (session, name). Returns nil/nil when
// not found so callers can distinguish "missing" from "error".
func (s *NexusArtifactStore) Read(ctx context.Context, sessionID primitive.ObjectID, name string) (*NexusArtifactDoc, error) {
	var out NexusArtifactDoc
	err := s.coll.FindOne(ctx, bson.M{"session_id": sessionID, "name": name}).Decode(&out)
	if err == mongo.ErrNoDocuments {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &out, nil
}

// List returns all artifacts in a session — slim view (no content) so the
// caller can render a picker without paying for the body. Sorted most-
// recent first.
type NexusArtifactSummary struct {
	Name        string    `json:"name"`
	ContentType string    `json:"content_type"`
	Summary     string    `json:"summary,omitempty"`
	SizeBytes   int       `json:"size_bytes"`
	CreatedAt   time.Time `json:"created_at"`
}

func (s *NexusArtifactStore) List(ctx context.Context, sessionID primitive.ObjectID) ([]NexusArtifactSummary, error) {
	opts := options.Find().
		SetSort(bson.D{{Key: "created_at", Value: -1}}).
		SetProjection(bson.M{"name": 1, "content_type": 1, "summary": 1, "size_bytes": 1, "created_at": 1})
	cursor, err := s.coll.Find(ctx, bson.M{"session_id": sessionID}, opts)
	if err != nil {
		return nil, err
	}
	defer cursor.Close(ctx)
	out := make([]NexusArtifactSummary, 0, 16)
	for cursor.Next(ctx) {
		var s NexusArtifactSummary
		if err := cursor.Decode(&s); err != nil {
			continue
		}
		out = append(out, s)
	}
	return out, nil
}

// DeleteForSession removes every artifact in a session — used by session
// cleanup / orchestration retry flows so a re-run doesn't see stale data
// from the prior attempt under the same name.
func (s *NexusArtifactStore) DeleteForSession(ctx context.Context, sessionID primitive.ObjectID) (int64, error) {
	res, err := s.coll.DeleteMany(ctx, bson.M{"session_id": sessionID})
	if err != nil {
		return 0, err
	}
	return res.DeletedCount, nil
}
