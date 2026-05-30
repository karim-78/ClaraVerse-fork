package models

import (
	"time"

	"go.mongodb.org/mongo-driver/bson/primitive"
)

// Memory represents a single memory extracted from conversations
type Memory struct {
	ID               primitive.ObjectID `bson:"_id,omitempty" json:"id"`
	UserID           string             `bson:"userId" json:"user_id"`
	ConversationID   string             `bson:"conversationId,omitempty" json:"conversation_id,omitempty"` // Source conversation (optional)

	// Memory Content (encrypted)
	EncryptedContent string `bson:"encryptedContent" json:"-"`              // AES-256-GCM encrypted memory text
	ContentHash      string `bson:"contentHash" json:"content_hash"`        // SHA-256 hash for deduplication

	// Metadata (plaintext for querying)
	Category string   `bson:"category" json:"category"`         // "personal_info", "preferences", "context", "fact", "instruction"
	Tags     []string `bson:"tags,omitempty" json:"tags,omitempty"` // Searchable tags (e.g., "coding", "music", "work")

	// PageRank-like Scoring
	Score          float64    `bson:"score" json:"score"`                                           // Current relevance score (0.0-1.0)
	AccessCount    int64      `bson:"accessCount" json:"access_count"`                              // How many times memory was selected/used
	LastAccessedAt *time.Time `bson:"lastAccessedAt,omitempty" json:"last_accessed_at,omitempty"`

	// Decay & Archival
	IsArchived bool       `bson:"isArchived" json:"is_archived"`                  // Decayed below threshold
	ArchivedAt *time.Time `bson:"archivedAt,omitempty" json:"archived_at,omitempty"`

	// Engagement Metrics (for PageRank calculation)
	SourceEngagement float64 `bson:"sourceEngagement" json:"source_engagement"` // Engagement score of conversation it came from

	// Embedding for vector retrieval (added 2026-05). Stored alongside the
	// encrypted text — the embedding is a derivative and isn't sensitive
	// once you have the content. Empty for legacy memories created before
	// embedding support; those fall back to LLM-only selection.
	Embedding []float32 `bson:"embedding,omitempty" json:"-"`

	// Timestamps
	CreatedAt time.Time `bson:"createdAt" json:"created_at"`
	UpdatedAt time.Time `bson:"updatedAt" json:"updated_at"`

	// Version (for deduplication/updates)
	Version int64 `bson:"version" json:"version"` // Incremented on updates
}

// DecryptedMemory represents a memory with decrypted content (for internal use only)
type DecryptedMemory struct {
	Memory
	DecryptedContent string `json:"content"` // Decrypted content
}

// MemoryExtractionJob represents a pending extraction job
type MemoryExtractionJob struct {
	ID                primitive.ObjectID `bson:"_id,omitempty" json:"id"`
	UserID            string             `bson:"userId" json:"user_id"`
	ConversationID    string             `bson:"conversationId" json:"conversation_id"`
	MessageCount      int                `bson:"messageCount" json:"message_count"`        // Number of messages to process
	EncryptedMessages string             `bson:"encryptedMessages" json:"-"`               // Encrypted message batch

	Status       string     `bson:"status" json:"status"`                                   // "pending", "processing", "completed", "failed"
	AttemptCount int        `bson:"attemptCount" json:"attempt_count"`                      // For retry logic
	ErrorMessage string     `bson:"errorMessage,omitempty" json:"error_message,omitempty"`

	CreatedAt   time.Time  `bson:"createdAt" json:"created_at"`
	ProcessedAt *time.Time `bson:"processedAt,omitempty" json:"processed_at,omitempty"`
}

// ConversationEngagement tracks engagement for PageRank calculation
type ConversationEngagement struct {
	ID            primitive.ObjectID `bson:"_id,omitempty" json:"id"`
	UserID        string             `bson:"userId" json:"user_id"`
	ConversationID string            `bson:"conversationId" json:"conversation_id"`

	MessageCount      int `bson:"messageCount" json:"message_count"`              // Total messages in conversation
	UserMessageCount  int `bson:"userMessageCount" json:"user_message_count"`     // User's message count
	AvgResponseLength int `bson:"avgResponseLength" json:"avg_response_length"`   // Average user response length

	EngagementScore float64 `bson:"engagementScore" json:"engagement_score"` // Calculated engagement (0.0-1.0)

	CreatedAt time.Time `bson:"createdAt" json:"created_at"`
	UpdatedAt time.Time `bson:"updatedAt" json:"updated_at"`
}

// MemoryCategory constants
const (
	MemoryCategoryPersonalInfo = "personal_info"
	MemoryCategoryPreferences  = "preferences"
	MemoryCategoryContext      = "context"
	MemoryCategoryFact         = "fact"
	MemoryCategoryInstruction  = "instruction"
)

// MemoryExtractionJobStatus constants
const (
	JobStatusPending    = "pending"
	JobStatusProcessing = "processing"
	JobStatusCompleted  = "completed"
	JobStatusFailed     = "failed"
)

// Memory archive threshold (memories with score below this are archived)
const MemoryArchiveThreshold = 0.15

// ExtractedMemoryFromLLM represents the structured output from the extraction LLM
type ExtractedMemoryFromLLM struct {
	Memories []struct {
		Content  string   `json:"content"`
		Category string   `json:"category"`
		Tags     []string `json:"tags"`
	} `json:"memories"`
}

// SelectedMemoriesFromLLM represents the structured output from the selection LLM
type SelectedMemoriesFromLLM struct {
	SelectedMemoryIDs []string `json:"selected_memory_ids"`
	Reasoning         string   `json:"reasoning"`
}
