package models

import (
	"time"

	"go.mongodb.org/mongo-driver/bson/primitive"
)

// KnowledgeFileStatus is the lifecycle state of an uploaded knowledge file.
type KnowledgeFileStatus string

const (
	KnowledgeFileStatusQueued    KnowledgeFileStatus = "queued"
	KnowledgeFileStatusIngesting KnowledgeFileStatus = "ingesting"
	KnowledgeFileStatusReady     KnowledgeFileStatus = "ready"
	KnowledgeFileStatusFailed    KnowledgeFileStatus = "failed"
)

// KnowledgeFile is one uploaded file in a project's knowledge base.
//
// We persist the raw bytes to disk (uploads/kb/<project>/<sha256>.<ext>)
// and keep only metadata here. The chunk text + vectors live in Qdrant
// — Mongo is the catalog and lifecycle tracker, never the search store.
type KnowledgeFile struct {
	ID          primitive.ObjectID  `bson:"_id,omitempty" json:"id"`
	ProjectID   primitive.ObjectID  `bson:"projectId" json:"project_id"`
	UserID      string              `bson:"userId" json:"user_id"`
	Filename    string              `bson:"filename" json:"filename"`
	ContentType string              `bson:"contentType" json:"content_type"`
	SizeBytes   int64               `bson:"sizeBytes" json:"size_bytes"`
	SHA256      string              `bson:"sha256" json:"sha256"`
	StoragePath string              `bson:"storagePath" json:"-"` // disk path, not exposed
	SourceURL   string              `bson:"sourceUrl,omitempty" json:"source_url,omitempty"`
	Status      KnowledgeFileStatus `bson:"status" json:"status"`
	Error       string              `bson:"error,omitempty" json:"error,omitempty"`
	// IngestProgress: 0.0–1.0 during ingestion so the UI can render a
	// progress bar. Updated after each batch upsert to Qdrant.
	IngestProgress float64 `bson:"ingestProgress,omitempty" json:"ingest_progress,omitempty"`
	ChunkCount     int     `bson:"chunkCount,omitempty" json:"chunk_count,omitempty"`
	CreatedAt      time.Time  `bson:"createdAt" json:"created_at"`
	IngestedAt     *time.Time `bson:"ingestedAt,omitempty" json:"ingested_at,omitempty"`
}

// KnowledgeCollection is the per-project Qdrant collection record.
//
// One per project. Tracks which embedder produced its vectors so we
// don't accidentally mix dimensions, and lets the admin "Reingest with
// different embedder" flow know what it's changing from.
type KnowledgeCollection struct {
	ID               primitive.ObjectID `bson:"_id,omitempty" json:"id"`
	ProjectID        primitive.ObjectID `bson:"projectId" json:"project_id"`
	UserID           string             `bson:"userId" json:"user_id"`
	QdrantCollection string             `bson:"qdrantCollection" json:"qdrant_collection"` // "kb_<project_hex>"

	// Embedder fingerprint. Dimensions matter — if it changes, all
	// existing points are incompatible and the collection must be
	// rebuilt. We refuse to ingest a file when the embedder's reported
	// dim differs from this record's stored dim.
	EmbedderID     string `bson:"embedderId" json:"embedder_id"`
	EmbedderDims   int    `bson:"embedderDims" json:"embedder_dims"`
	SparseEnabled  bool   `bson:"sparseEnabled" json:"sparse_enabled"`

	// Contextual chunk prefixing (Anthropic-style) is opt-in per project.
	// Big quality win, costs ~1 cheap LLM call per chunk during ingest.
	// Off by default in v1 so deployments without a configured cheap
	// model don't surprise-bill users.
	ContextualEnabled bool `bson:"contextualEnabled" json:"contextual_enabled"`

	FileCount     int       `bson:"fileCount" json:"file_count"`
	ChunkCount    int       `bson:"chunkCount" json:"chunk_count"`
	CreatedAt     time.Time `bson:"createdAt" json:"created_at"`
	LastIndexedAt *time.Time `bson:"lastIndexedAt,omitempty" json:"last_indexed_at,omitempty"`
}

// KnowledgeSearchHit is one returned chunk from a search_knowledge call.
// Mirrors the Qdrant payload schema we upsert during ingest.
type KnowledgeSearchHit struct {
	Score    float32 `json:"score"`
	Text     string  `json:"text"`
	FileID   string  `json:"file_id"`
	FileName string  `json:"file_name"`
	ChunkIdx int     `json:"chunk_idx"`
	// Page and Section are best-effort — present for PDFs / structured
	// markdown, absent for plain text.
	Page    int    `json:"page,omitempty"`
	Section string `json:"section,omitempty"`
	// ProjectID is included so a multi-project search response is
	// self-describing — the UI can group chunks by project.
	ProjectID string `json:"project_id"`
}
