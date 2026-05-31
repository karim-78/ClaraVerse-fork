package rag

import (
	"context"
	"fmt"
	"time"

	"claraverse/internal/database"
	"claraverse/internal/models"

	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/bson/primitive"
	"go.mongodb.org/mongo-driver/mongo"
	"go.mongodb.org/mongo-driver/mongo/options"
)

// FileStore is the Mongo-backed catalog of uploaded knowledge files.
// All queries are scoped by userId — every public method requires it.
// A missing userId scope on any query is a multi-tenant data leak, so
// the signatures make it impossible to omit.
type FileStore struct {
	col *mongo.Collection
}

// NewFileStore wires a FileStore against the standard collection.
func NewFileStore(db *database.MongoDB) *FileStore {
	return &FileStore{col: db.Collection(database.CollectionNexusKnowledgeFiles)}
}

// Create inserts a new file in "queued" state. Returns the new ID.
func (s *FileStore) Create(ctx context.Context, file *models.KnowledgeFile) error {
	if file.CreatedAt.IsZero() {
		file.CreatedAt = time.Now()
	}
	if file.Status == "" {
		file.Status = models.KnowledgeFileStatusQueued
	}
	res, err := s.col.InsertOne(ctx, file)
	if err != nil {
		return fmt.Errorf("knowledge file insert: %w", err)
	}
	file.ID = res.InsertedID.(primitive.ObjectID)
	return nil
}

// ListByProject returns all files in a project, newest first.
// Scoped by userId — never returns another user's files even if the
// projectId is correct.
func (s *FileStore) ListByProject(ctx context.Context, userID string, projectID primitive.ObjectID) ([]models.KnowledgeFile, error) {
	cursor, err := s.col.Find(ctx,
		bson.M{"userId": userID, "projectId": projectID},
		options.Find().SetSort(bson.D{{Key: "createdAt", Value: -1}}),
	)
	if err != nil {
		return nil, fmt.Errorf("knowledge files list: %w", err)
	}
	defer cursor.Close(ctx)
	var files []models.KnowledgeFile
	if err := cursor.All(ctx, &files); err != nil {
		return nil, err
	}
	return files, nil
}

// GetByID returns one file scoped by userId. Returns nil, nil when not
// found — callers should treat that as a 404.
func (s *FileStore) GetByID(ctx context.Context, userID string, id primitive.ObjectID) (*models.KnowledgeFile, error) {
	var f models.KnowledgeFile
	err := s.col.FindOne(ctx, bson.M{"_id": id, "userId": userID}).Decode(&f)
	if err == mongo.ErrNoDocuments {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("knowledge file get: %w", err)
	}
	return &f, nil
}

// NextQueued atomically claims the next queued file for ingestion.
// Used by the background worker — find-and-modify so two workers
// can't pick the same file. Returns nil, nil when the queue is empty.
func (s *FileStore) NextQueued(ctx context.Context) (*models.KnowledgeFile, error) {
	var f models.KnowledgeFile
	err := s.col.FindOneAndUpdate(ctx,
		bson.M{"status": models.KnowledgeFileStatusQueued},
		bson.M{"$set": bson.M{"status": models.KnowledgeFileStatusIngesting}},
		options.FindOneAndUpdate().
			SetSort(bson.D{{Key: "createdAt", Value: 1}}). // oldest first
			SetReturnDocument(options.After),
	).Decode(&f)
	if err == mongo.ErrNoDocuments {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("knowledge file claim: %w", err)
	}
	return &f, nil
}

// UpdateStatus sets the lifecycle state of a file. Used by the ingest
// worker to publish progress to the UI (status + ingestProgress +
// chunkCount + ingestedAt are the four fields that flip during a run).
func (s *FileStore) UpdateStatus(ctx context.Context, id primitive.ObjectID, patch bson.M) error {
	_, err := s.col.UpdateByID(ctx, id, bson.M{"$set": patch})
	if err != nil {
		return fmt.Errorf("knowledge file update: %w", err)
	}
	return nil
}

// Delete removes a file record. The caller is responsible for also
// deleting the on-disk bytes and the Qdrant points — Service.DeleteFile
// orchestrates both, this is the bare DB op.
func (s *FileStore) Delete(ctx context.Context, userID string, id primitive.ObjectID) error {
	res, err := s.col.DeleteOne(ctx, bson.M{"_id": id, "userId": userID})
	if err != nil {
		return fmt.Errorf("knowledge file delete: %w", err)
	}
	if res.DeletedCount == 0 {
		return fmt.Errorf("not found")
	}
	return nil
}

// ─── CollectionStore ────────────────────────────────────────────────

// CollectionStore is the per-project Qdrant-collection metadata
// record. Tracks what embedder was used so we don't accidentally mix
// dimensions when the admin swaps the model.
type CollectionStore struct {
	col *mongo.Collection
}

func NewCollectionStore(db *database.MongoDB) *CollectionStore {
	return &CollectionStore{col: db.Collection(database.CollectionNexusKnowledgeCollections)}
}

// GetOrCreate returns the per-project collection record, creating it
// if missing. Default embedder fingerprint comes from the embeddings
// sidecar's health probe — keeps us in sync with whatever model the
// admin has loaded right now.
func (s *CollectionStore) GetOrCreate(ctx context.Context, userID string, projectID primitive.ObjectID, embedderID string, embedderDims int) (*models.KnowledgeCollection, error) {
	var c models.KnowledgeCollection
	err := s.col.FindOne(ctx, bson.M{"projectId": projectID, "userId": userID}).Decode(&c)
	if err == nil {
		return &c, nil
	}
	if err != mongo.ErrNoDocuments {
		return nil, fmt.Errorf("knowledge collection get: %w", err)
	}
	c = models.KnowledgeCollection{
		ProjectID:        projectID,
		UserID:           userID,
		QdrantCollection: fmt.Sprintf("kb_%s", projectID.Hex()),
		EmbedderID:       embedderID,
		EmbedderDims:     embedderDims,
		SparseEnabled:    false, // flipped to true in Phase C
		CreatedAt:        time.Now(),
	}
	res, err := s.col.InsertOne(ctx, &c)
	if err != nil {
		return nil, fmt.Errorf("knowledge collection insert: %w", err)
	}
	c.ID = res.InsertedID.(primitive.ObjectID)
	return &c, nil
}

// Get returns a project's collection record or nil if not provisioned
// yet. Used by search to short-circuit when no knowledge exists for a
// project — saves a useless Qdrant round trip.
func (s *CollectionStore) Get(ctx context.Context, userID string, projectID primitive.ObjectID) (*models.KnowledgeCollection, error) {
	var c models.KnowledgeCollection
	err := s.col.FindOne(ctx, bson.M{"projectId": projectID, "userId": userID}).Decode(&c)
	if err == mongo.ErrNoDocuments {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("knowledge collection get: %w", err)
	}
	return &c, nil
}

// MarkSparseEnabled flips the per-project hybrid-search flag. Called
// at first ingest of a freshly-provisioned collection so the Qdrant
// collection is created with sparse vectors configured. Pre-existing
// collections from Phase A keep the flag false until they're
// reingested — Qdrant collections can't be retrofitted with a new
// vector kind, only recreated.
func (s *CollectionStore) MarkSparseEnabled(ctx context.Context, id primitive.ObjectID) error {
	_, err := s.col.UpdateByID(ctx, id, bson.M{"$set": bson.M{"sparseEnabled": true}})
	return err
}

// IncrementChunks adjusts the per-project chunk + file counters.
// Called from the ingest worker on file completion and from delete.
func (s *CollectionStore) IncrementChunks(ctx context.Context, projectID primitive.ObjectID, deltaFiles, deltaChunks int) error {
	now := time.Now()
	_, err := s.col.UpdateOne(ctx,
		bson.M{"projectId": projectID},
		bson.M{
			"$inc": bson.M{"fileCount": deltaFiles, "chunkCount": deltaChunks},
			"$set": bson.M{"lastIndexedAt": now},
		},
	)
	return err
}
