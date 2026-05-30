package models

import (
	"time"

	"go.mongodb.org/mongo-driver/bson/primitive"
)

// EncryptedChat represents a chat stored in MongoDB with encrypted messages
type EncryptedChat struct {
	ID                primitive.ObjectID `bson:"_id,omitempty" json:"id"`
	UserID            string             `bson:"userId" json:"user_id"`
	ChatID            string             `bson:"chatId" json:"chat_id"`             // Frontend-generated UUID
	Title             string             `bson:"title" json:"title"`                // Plaintext title for listing
	EncryptedMessages string             `bson:"encryptedMessages" json:"-"`        // AES-256-GCM encrypted JSON array of messages
	IsStarred         bool               `bson:"isStarred" json:"is_starred"`
	Model             string             `bson:"model,omitempty" json:"model,omitempty"` // Selected model for this chat
	Version           int64              `bson:"version" json:"version"`            // Optimistic locking
	CreatedAt         time.Time          `bson:"createdAt" json:"created_at"`
	UpdatedAt         time.Time          `bson:"updatedAt" json:"updated_at"`
}

// ChatMessage represents a single message in a chat (unencrypted form)
type ChatMessage struct {
	ID            string           `json:"id"`
	Role          string           `json:"role"`      // "user", "assistant", "system"
	Content       string           `json:"content"`
	Timestamp     int64            `json:"timestamp"` // Unix milliseconds
	IsStreaming   bool             `json:"isStreaming,omitempty"`
	Status        string           `json:"status,omitempty"`    // "sending", "sent", "error"
	Error         string           `json:"error,omitempty"`
	Attachments   []ChatAttachment `json:"attachments,omitempty"`
	ToolCalls     []ToolCall       `json:"toolCalls,omitempty"`
	Reasoning     string           `json:"reasoning,omitempty"` // Thinking/reasoning process
	Artifacts     []Artifact       `json:"artifacts,omitempty"`
	AgentId       string           `json:"agentId,omitempty"`
	AgentName     string           `json:"agentName,omitempty"`
	AgentAvatar   string           `json:"agentAvatar,omitempty"`

	// Response versioning fields
	VersionGroupId string `json:"versionGroupId,omitempty"` // Groups all versions of same response
	VersionNumber  int    `json:"versionNumber,omitempty"`  // 1, 2, 3... within the group
	IsHidden       bool   `json:"isHidden,omitempty"`       // Hidden versions (not current)
	RetryType      string `json:"retryType,omitempty"`      // Type of retry: regenerate, add_details, etc.
}

// ToolCall represents a tool invocation in a message
type ToolCall struct {
	ID          string     `json:"id"`
	Name        string     `json:"name"`
	DisplayName string     `json:"displayName,omitempty"`
	Icon        string     `json:"icon,omitempty"`
	Status      string     `json:"status"` // "executing", "completed"
	Query       string     `json:"query,omitempty"`
	Result      string     `json:"result,omitempty"`
	Plots       []PlotData `json:"plots,omitempty"`
	Timestamp   int64      `json:"timestamp"`
	IsExpanded  bool       `json:"isExpanded,omitempty"`
}

// Artifact represents renderable content (HTML, SVG, Mermaid)
type Artifact struct {
	ID       string            `json:"id"`
	Type     string            `json:"type"` // "html", "svg", "mermaid", "image"
	Title    string            `json:"title"`
	Content  string            `json:"content"`
	Images   []ArtifactImage   `json:"images,omitempty"`
	Metadata map[string]interface{} `json:"metadata,omitempty"`
}

// ArtifactImage represents an image in an artifact
type ArtifactImage struct {
	Data    string `json:"data"`    // Base64-encoded
	Format  string `json:"format"`  // png, jpg, svg
	Caption string `json:"caption,omitempty"`
}

// ChatAttachment represents a file attached to a message
type ChatAttachment struct {
	FileID      string       `json:"file_id"`
	Type        string       `json:"type"`      // Attachment type: "image", "document", "data"
	URL         string       `json:"url"`
	MimeType    string       `json:"mime_type"`
	Size        int64        `json:"size"`
	Filename    string       `json:"filename,omitempty"`
	Expired     bool         `json:"expired,omitempty"`
	// Document-specific fields
	PageCount   int          `json:"page_count,omitempty"`
	WordCount   int          `json:"word_count,omitempty"`
	Preview     string       `json:"preview,omitempty"`     // Text preview or thumbnail
	// Data file-specific fields
	DataPreview *DataPreview `json:"data_preview,omitempty"`
}

// DataPreview represents a preview of CSV/tabular data
type DataPreview struct {
	Headers  []string   `json:"headers"`
	Rows     [][]string `json:"rows"`
	RowCount int        `json:"row_count"` // Total rows in file
	ColCount int        `json:"col_count"` // Total columns
}

// ChatResponse is the decrypted chat returned to the frontend
type ChatResponse struct {
	ID        string        `json:"id"`
	Title     string        `json:"title"`
	Messages  []ChatMessage `json:"messages"`
	IsStarred bool          `json:"is_starred"`
	Model     string        `json:"model,omitempty"`
	Version   int64         `json:"version"`
	CreatedAt time.Time     `json:"created_at"`
	UpdatedAt time.Time     `json:"updated_at"`
}

// ChatListItem is a summary of a chat for listing (no messages)
type ChatListItem struct {
	ID           string    `json:"id"`
	Title        string    `json:"title"`
	IsStarred    bool      `json:"is_starred"`
	Model        string    `json:"model,omitempty"`
	MessageCount int       `json:"message_count"`
	Version      int64     `json:"version"`
	CreatedAt    time.Time `json:"created_at"`
	UpdatedAt    time.Time `json:"updated_at"`
}

// CreateChatRequest is the request body for creating/updating a chat
type CreateChatRequest struct {
	ID        string        `json:"id"`        // Frontend-generated UUID
	Title     string        `json:"title"`
	Messages  []ChatMessage `json:"messages"`
	IsStarred bool          `json:"is_starred"`
	Model     string        `json:"model,omitempty"`
	Version   int64         `json:"version,omitempty"` // For optimistic locking on updates
}

// UpdateChatRequest is the request body for partial chat updates
type UpdateChatRequest struct {
	Title     *string `json:"title,omitempty"`
	IsStarred *bool   `json:"is_starred,omitempty"`
	Model     *string `json:"model,omitempty"`
	Version   int64   `json:"version"` // Required for optimistic locking
}

// ChatAddMessageRequest is the request body for adding a single message to a synced chat
type ChatAddMessageRequest struct {
	Message ChatMessage `json:"message"`
	Version int64       `json:"version"` // For optimistic locking
}

// BulkSyncRequest is the request body for uploading multiple chats
type BulkSyncRequest struct {
	Chats []CreateChatRequest `json:"chats"`
}

// BulkSyncResponse is the response for bulk sync operation
type BulkSyncResponse struct {
	Synced  int      `json:"synced"`
	Failed  int      `json:"failed"`
	Errors  []string `json:"errors,omitempty"`
	ChatIDs []string `json:"chat_ids"` // IDs of successfully synced chats
}

// SyncAllResponse is the response for fetching all chats for initial sync
type SyncAllResponse struct {
	Chats      []ChatResponse `json:"chats"`
	TotalCount int            `json:"total_count"`
	SyncedAt   time.Time      `json:"synced_at"`
}

// ChatListResponse is the paginated response for listing chats
type ChatListResponse struct {
	Chats      []ChatListItem `json:"chats"`
	TotalCount int64          `json:"total_count"`
	Page       int            `json:"page"`
	PageSize   int            `json:"page_size"`
	HasMore    bool           `json:"has_more"`
}

// ChatSearchHit is a single full-text search result with enough context for
// the UI to render a preview chip and (eventually) jump to the exact message.
type ChatSearchHit struct {
	ChatID       string    `json:"chat_id"`
	Title        string    `json:"title"`
	IsStarred    bool      `json:"is_starred"`
	UpdatedAt    time.Time `json:"updated_at"`
	Snippet      string    `json:"snippet"`
	MatchField   string    `json:"match_field"`              // "title" | "message"
	MessageID    string    `json:"message_id,omitempty"`
	MessageRole  string    `json:"message_role,omitempty"`
	MessageIndex int       `json:"message_index,omitempty"`
}

// ChatSearchResponse is what /api/chats/search returns.
type ChatSearchResponse struct {
	Query string          `json:"query"`
	Hits  []ChatSearchHit `json:"hits"`
	Total int             `json:"total"`
}
