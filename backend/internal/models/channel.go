package models

import (
	"time"

	"go.mongodb.org/mongo-driver/bson/primitive"
)

// ChannelPlatform represents supported channel platforms
type ChannelPlatform string

const (
	ChannelPlatformTelegram ChannelPlatform = "telegram"
	// Future: discord, slack, whatsapp, etc.
)

// Channel represents a communication channel configuration for a user
// Channels allow users to chat with DobbyAI from external platforms
// instead of the web UI
type Channel struct {
	ID       primitive.ObjectID `bson:"_id,omitempty" json:"id"`
	UserID   string             `bson:"userId" json:"userId"`
	Platform ChannelPlatform    `bson:"platform" json:"platform"`
	Name     string             `bson:"name" json:"name"` // User-friendly name like "My Telegram Bot"
	Enabled  bool               `bson:"enabled" json:"enabled"`

	// Platform-specific configuration (encrypted)
	EncryptedConfig string `bson:"encryptedConfig" json:"-"` // Never sent to frontend

	// Webhook configuration
	WebhookSecret string `bson:"webhookSecret" json:"-"` // For verifying incoming webhooks
	WebhookURL    string `bson:"-" json:"webhookUrl"`    // Generated, not stored

	// Bot info (populated after verification)
	BotUsername string `bson:"botUsername,omitempty" json:"botUsername,omitempty"`
	BotName     string `bson:"botName,omitempty" json:"botName,omitempty"`

	// Session management
	DefaultModelID      string `bson:"defaultModelId,omitempty" json:"defaultModelId,omitempty"`
	DefaultSystemPrompt string `bson:"defaultSystemPrompt,omitempty" json:"defaultSystemPrompt,omitempty"`
	MaxHistoryMessages  int    `bson:"maxHistoryMessages" json:"maxHistoryMessages"` // How many messages to keep in context

	// Access control - if empty, anyone can use the bot; if set, only these users can use it
	// Supports Telegram usernames (without @) or user IDs
	AllowedUsers []string `bson:"allowedUsers,omitempty" json:"allowedUsers,omitempty"`

	// Stats
	MessageCount int64      `bson:"messageCount" json:"messageCount"`
	LastUsedAt   *time.Time `bson:"lastUsedAt,omitempty" json:"lastUsedAt,omitempty"`

	// Timestamps
	CreatedAt time.Time `bson:"createdAt" json:"createdAt"`
	UpdatedAt time.Time `bson:"updatedAt" json:"updatedAt"`
}

// TelegramConfig holds Telegram-specific channel configuration
type TelegramConfig struct {
	BotToken string `json:"bot_token"`
}

// ChannelSession tracks an active conversation for a channel user
type ChannelSession struct {
	ID             primitive.ObjectID `bson:"_id,omitempty" json:"id"`
	ChannelID      primitive.ObjectID `bson:"channelId" json:"channelId"`
	PlatformUserID string             `bson:"platformUserId" json:"platformUserId"` // e.g., Telegram user ID
	PlatformChatID string             `bson:"platformChatId" json:"platformChatId"` // e.g., Telegram chat ID
	ConversationID string             `bson:"conversationId" json:"conversationId"` // DobbyAI conversation ID
	ModelID        string             `bson:"modelId" json:"modelId"`
	Messages       []ChannelMessage   `bson:"messages" json:"messages"`
	CreatedAt      time.Time          `bson:"createdAt" json:"createdAt"`
	UpdatedAt      time.Time          `bson:"updatedAt" json:"updatedAt"`
}

// ChannelMessage represents a message in a channel session
type ChannelMessage struct {
	Role      string    `bson:"role" json:"role"` // "user" or "assistant"
	Content   string    `bson:"content" json:"content"`
	Timestamp time.Time `bson:"timestamp" json:"timestamp"`
}

// CreateChannelRequest is the request body for creating a channel
type CreateChannelRequest struct {
	Platform ChannelPlatform        `json:"platform"`
	Name     string                 `json:"name"`
	Config   map[string]interface{} `json:"config"` // Platform-specific config
}

// UpdateChannelRequest is the request body for updating a channel
type UpdateChannelRequest struct {
	Name                *string   `json:"name,omitempty"`
	Enabled             *bool     `json:"enabled,omitempty"`
	DefaultModelID      *string   `json:"defaultModelId,omitempty"`
	DefaultSystemPrompt *string   `json:"defaultSystemPrompt,omitempty"`
	MaxHistoryMessages  *int      `json:"maxHistoryMessages,omitempty"`
	AllowedUsers        *[]string `json:"allowedUsers,omitempty"` // Allowlist for access control
}

// ChannelResponse is the response for channel endpoints (safe for frontend)
type ChannelResponse struct {
	ID                  string          `json:"id"`
	Platform            ChannelPlatform `json:"platform"`
	Name                string          `json:"name"`
	Enabled             bool            `json:"enabled"`
	WebhookURL          string          `json:"webhookUrl"`
	BotUsername         string          `json:"botUsername,omitempty"`
	BotName             string          `json:"botName,omitempty"`
	DefaultModelID      string          `json:"defaultModelId,omitempty"`
	DefaultSystemPrompt string          `json:"defaultSystemPrompt,omitempty"`
	MaxHistoryMessages  int             `json:"maxHistoryMessages"`
	AllowedUsers        []string        `json:"allowedUsers,omitempty"` // Allowlist for access control
	MessageCount        int64           `json:"messageCount"`
	LastUsedAt          *time.Time      `json:"lastUsedAt,omitempty"`
	CreatedAt           time.Time       `json:"createdAt"`
}

// TestChannelResponse is the response for testing a channel
type TestChannelResponse struct {
	Success     bool   `json:"success"`
	Message     string `json:"message"`
	BotUsername string `json:"botUsername,omitempty"`
	BotName     string `json:"botName,omitempty"`
}

// ListChannelsResponse is the response for listing channels
type ListChannelsResponse struct {
	Channels []ChannelResponse `json:"channels"`
	Total    int               `json:"total"`
}

// TelegramUpdate represents an incoming Telegram webhook update
type TelegramUpdate struct {
	UpdateID int64            `json:"update_id"`
	Message  *TelegramMessage `json:"message,omitempty"`
}

// TelegramMessage represents a Telegram message
type TelegramMessage struct {
	MessageID int64         `json:"message_id"`
	From      *TelegramUser `json:"from,omitempty"`
	Chat      *TelegramChat `json:"chat"`
	Date      int64         `json:"date"`
	Text      string        `json:"text,omitempty"`

	// Media attachments
	Photo    []TelegramPhotoSize `json:"photo,omitempty"`    // Array of photo sizes
	Voice    *TelegramVoice      `json:"voice,omitempty"`    // Voice message
	Audio    *TelegramAudio      `json:"audio,omitempty"`    // Audio file
	Document *TelegramDocument   `json:"document,omitempty"` // Document/file
	Caption  string              `json:"caption,omitempty"`  // Caption for media
}

// TelegramPhotoSize represents a photo size (Telegram sends multiple sizes)
type TelegramPhotoSize struct {
	FileID       string `json:"file_id"`
	FileUniqueID string `json:"file_unique_id"`
	Width        int    `json:"width"`
	Height       int    `json:"height"`
	FileSize     int    `json:"file_size,omitempty"`
}

// TelegramVoice represents a voice message
type TelegramVoice struct {
	FileID       string `json:"file_id"`
	FileUniqueID string `json:"file_unique_id"`
	Duration     int    `json:"duration"`
	MimeType     string `json:"mime_type,omitempty"`
	FileSize     int    `json:"file_size,omitempty"`
}

// TelegramAudio represents an audio file
type TelegramAudio struct {
	FileID       string `json:"file_id"`
	FileUniqueID string `json:"file_unique_id"`
	Duration     int    `json:"duration"`
	Performer    string `json:"performer,omitempty"`
	Title        string `json:"title,omitempty"`
	MimeType     string `json:"mime_type,omitempty"`
	FileSize     int    `json:"file_size,omitempty"`
}

// TelegramDocument represents a document/file
type TelegramDocument struct {
	FileID       string `json:"file_id"`
	FileUniqueID string `json:"file_unique_id"`
	FileName     string `json:"file_name,omitempty"`
	MimeType     string `json:"mime_type,omitempty"`
	FileSize     int    `json:"file_size,omitempty"`
}

// TelegramUser represents a Telegram user
type TelegramUser struct {
	ID        int64  `json:"id"`
	IsBot     bool   `json:"is_bot"`
	FirstName string `json:"first_name"`
	LastName  string `json:"last_name,omitempty"`
	Username  string `json:"username,omitempty"`
}

// TelegramChat represents a Telegram chat
type TelegramChat struct {
	ID        int64  `json:"id"`
	Type      string `json:"type"` // "private", "group", "supergroup", "channel"
	Title     string `json:"title,omitempty"`
	Username  string `json:"username,omitempty"`
	FirstName string `json:"first_name,omitempty"`
	LastName  string `json:"last_name,omitempty"`
}
