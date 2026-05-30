package services

import (
	"errors"
	"log"
	"strings"
	"sync"
	"time"
)

// Stream buffer constants for production safety
const (
	MaxChunksPerBuffer = 10000        // Prevent memory exhaustion
	MaxBufferSize      = 1 << 20      // 1MB max per buffer
	DefaultBufferTTL   = 2 * time.Minute
	CleanupInterval    = 30 * time.Second
)

// Error types for stream buffer operations
var (
	ErrBufferNotFound    = errors.New("stream buffer not found")
	ErrBufferFull        = errors.New("stream buffer full: max chunks exceeded")
	ErrBufferSizeExceeded = errors.New("stream buffer size exceeded")
	ErrResumeTooFast     = errors.New("resume rate limit exceeded")
)

// BufferedMessage represents a message that needs to be delivered on reconnect
type BufferedMessage struct {
	Type            string                 `json:"type"`
	Content         string                 `json:"content,omitempty"`
	ToolName        string                 `json:"tool_name,omitempty"`
	ToolDisplayName string                 `json:"tool_display_name,omitempty"`
	ToolIcon        string                 `json:"tool_icon,omitempty"`
	ToolDescription string                 `json:"tool_description,omitempty"`
	Status          string                 `json:"status,omitempty"`
	Arguments       map[string]interface{} `json:"arguments,omitempty"`
	Result          string                 `json:"result,omitempty"`
	Plots           interface{}            `json:"plots,omitempty"`      // For image artifacts
	DataFrames      interface{}            `json:"dataframes,omitempty"` // For tabular artifacts
	Delivered       bool                   `json:"-"`                    // Track if already delivered (not serialized)
}

// StreamBuffer holds buffered chunks for a streaming conversation
type StreamBuffer struct {
	ConversationID   string
	UserID           string
	ConnID           string             // Original connection ID
	Chunks           []string           // Buffered text chunks
	PendingMessages  []BufferedMessage  // Important messages (tool_result, etc.) to deliver on reconnect
	TotalSize        int                // Current total size of all chunks
	IsComplete       bool               // Generation finished?
	FullContent      string             // Full content if complete
	CreatedAt        time.Time
	LastChunkAt      time.Time          // Last chunk received time
	ResumeCount      int                // Track resume attempts
	LastResume       time.Time          // Prevent rapid resume spam
	mutex            sync.Mutex
}

// StreamBufferService manages stream buffers for disconnected clients
type StreamBufferService struct {
	buffers     map[string]*StreamBuffer // conversationID -> buffer
	mutex       sync.RWMutex
	ttl         time.Duration
	cleanupTick *time.Ticker
	done        chan struct{}
}

// NewStreamBufferService creates a new stream buffer service
func NewStreamBufferService() *StreamBufferService {
	svc := &StreamBufferService{
		buffers:     make(map[string]*StreamBuffer),
		ttl:         DefaultBufferTTL,
		cleanupTick: time.NewTicker(CleanupInterval),
		done:        make(chan struct{}),
	}
	go svc.cleanupLoop()
	log.Println("📦 StreamBufferService initialized")
	return svc
}

// cleanupLoop periodically removes expired buffers
func (s *StreamBufferService) cleanupLoop() {
	for {
		select {
		case <-s.done:
			return
		case <-s.cleanupTick.C:
			s.cleanup()
		}
	}
}

// cleanup removes expired buffers
func (s *StreamBufferService) cleanup() {
	s.mutex.Lock()
	defer s.mutex.Unlock()

	now := time.Now()
	expired := 0
	for convID, buf := range s.buffers {
		if now.Sub(buf.CreatedAt) > s.ttl {
			delete(s.buffers, convID)
			expired++
			log.Printf("📦 Buffer expired for conversation %s", convID)
		}
	}
	if expired > 0 {
		log.Printf("📦 Cleaned up %d expired buffers, %d active", expired, len(s.buffers))
	}
}

// Shutdown gracefully shuts down the service
func (s *StreamBufferService) Shutdown() {
	close(s.done)
	s.cleanupTick.Stop()
	s.mutex.Lock()
	defer s.mutex.Unlock()
	s.buffers = nil
	log.Println("📦 StreamBufferService shutdown complete")
}

// CreateBuffer creates a new buffer for a conversation
func (s *StreamBufferService) CreateBuffer(conversationID, userID, connID string) {
	s.mutex.Lock()
	defer s.mutex.Unlock()

	// If buffer already exists, don't overwrite (prevents race conditions)
	if _, exists := s.buffers[conversationID]; exists {
		log.Printf("📦 Buffer already exists for conversation %s", conversationID)
		return
	}

	s.buffers[conversationID] = &StreamBuffer{
		ConversationID:  conversationID,
		UserID:          userID,
		ConnID:          connID,
		Chunks:          make([]string, 0, 100), // Pre-allocate for performance
		PendingMessages: make([]BufferedMessage, 0, 10), // For tool results, etc.
		TotalSize:       0,
		CreatedAt:       time.Now(),
		LastChunkAt:     time.Now(),
	}
	log.Printf("📦 Buffer created for conversation %s (user: %s)", conversationID, userID)
}

// AppendChunk adds a chunk to the buffer
func (s *StreamBufferService) AppendChunk(conversationID, chunk string) error {
	s.mutex.RLock()
	buf, exists := s.buffers[conversationID]
	s.mutex.RUnlock()

	if !exists {
		// Buffer doesn't exist - this is normal if streaming started before disconnect
		return nil
	}

	buf.mutex.Lock()
	defer buf.mutex.Unlock()

	// Safety limits
	if len(buf.Chunks) >= MaxChunksPerBuffer {
		log.Printf("⚠️ Buffer full for conversation %s (max chunks: %d)", conversationID, MaxChunksPerBuffer)
		return ErrBufferFull
	}

	if buf.TotalSize+len(chunk) > MaxBufferSize {
		log.Printf("⚠️ Buffer size exceeded for conversation %s (max: %d bytes)", conversationID, MaxBufferSize)
		return ErrBufferSizeExceeded
	}

	buf.Chunks = append(buf.Chunks, chunk)
	buf.TotalSize += len(chunk)
	buf.LastChunkAt = time.Now()

	return nil
}

// AppendMessage adds an important message to the buffer for delivery on reconnect
// This is used for tool_result, artifacts, and other critical messages that shouldn't be lost
func (s *StreamBufferService) AppendMessage(conversationID string, msg BufferedMessage) error {
	s.mutex.RLock()
	buf, exists := s.buffers[conversationID]
	s.mutex.RUnlock()

	if !exists {
		// Buffer doesn't exist - this is normal if streaming started before disconnect
		return nil
	}

	buf.mutex.Lock()
	defer buf.mutex.Unlock()

	// Limit pending messages to prevent memory issues
	if len(buf.PendingMessages) >= 50 {
		log.Printf("⚠️ Too many pending messages for conversation %s", conversationID)
		return ErrBufferFull
	}

	buf.PendingMessages = append(buf.PendingMessages, msg)
	buf.LastChunkAt = time.Now()

	log.Printf("📦 Buffered message type=%s for conversation %s (pending: %d)",
		msg.Type, conversationID, len(buf.PendingMessages))

	return nil
}

// MarkMessagesDelivered marks all pending messages as delivered to prevent duplicate replay
func (s *StreamBufferService) MarkMessagesDelivered(conversationID string) {
	s.mutex.RLock()
	buf, exists := s.buffers[conversationID]
	s.mutex.RUnlock()

	if !exists {
		return
	}

	buf.mutex.Lock()
	defer buf.mutex.Unlock()

	for i := range buf.PendingMessages {
		buf.PendingMessages[i].Delivered = true
	}
	log.Printf("📦 Marked %d pending messages as delivered for conversation %s", len(buf.PendingMessages), conversationID)
}

// MarkComplete marks the buffer as complete with the full content
func (s *StreamBufferService) MarkComplete(conversationID, fullContent string) {
	s.mutex.RLock()
	buf, exists := s.buffers[conversationID]
	s.mutex.RUnlock()

	if !exists {
		return
	}

	buf.mutex.Lock()
	defer buf.mutex.Unlock()

	buf.IsComplete = true
	buf.FullContent = fullContent
	log.Printf("📦 Buffer marked complete for conversation %s (size: %d bytes)", conversationID, len(fullContent))
}

// GetBuffer retrieves a buffer without clearing it (allows multiple resume attempts)
func (s *StreamBufferService) GetBuffer(conversationID string) (*StreamBuffer, error) {
	s.mutex.Lock()
	defer s.mutex.Unlock()

	buf, exists := s.buffers[conversationID]
	if !exists {
		return nil, ErrBufferNotFound
	}

	// Rate limit: 1 resume per second
	if time.Since(buf.LastResume) < time.Second {
		return nil, ErrResumeTooFast
	}

	buf.ResumeCount++
	buf.LastResume = time.Now()

	log.Printf("📦 Buffer retrieved for conversation %s (resume #%d, chunks: %d)",
		conversationID, buf.ResumeCount, len(buf.Chunks))

	return buf, nil
}

// ClearBuffer removes a buffer after successful resume
func (s *StreamBufferService) ClearBuffer(conversationID string) {
	s.mutex.Lock()
	defer s.mutex.Unlock()

	if _, exists := s.buffers[conversationID]; exists {
		delete(s.buffers, conversationID)
		log.Printf("📦 Buffer cleared for conversation %s", conversationID)
	}
}

// HasBuffer checks if a buffer exists for a conversation
func (s *StreamBufferService) HasBuffer(conversationID string) bool {
	s.mutex.RLock()
	defer s.mutex.RUnlock()
	_, exists := s.buffers[conversationID]
	return exists
}

// GetBufferStats returns statistics about the buffer service
func (s *StreamBufferService) GetBufferStats() map[string]interface{} {
	s.mutex.RLock()
	defer s.mutex.RUnlock()

	totalChunks := 0
	totalSize := 0
	for _, buf := range s.buffers {
		buf.mutex.Lock()
		totalChunks += len(buf.Chunks)
		totalSize += buf.TotalSize
		buf.mutex.Unlock()
	}

	return map[string]interface{}{
		"active_buffers": len(s.buffers),
		"total_chunks":   totalChunks,
		"total_size":     totalSize,
	}
}

// GetBufferInfo returns detailed info about a specific buffer (for debugging)
func (s *StreamBufferService) GetBufferInfo(conversationID string) map[string]interface{} {
	s.mutex.RLock()
	buf, exists := s.buffers[conversationID]
	s.mutex.RUnlock()

	if !exists {
		return nil
	}

	buf.mutex.Lock()
	defer buf.mutex.Unlock()

	return map[string]interface{}{
		"conversation_id": buf.ConversationID,
		"user_id":         buf.UserID,
		"conn_id":         buf.ConnID,
		"chunk_count":     len(buf.Chunks),
		"total_size":      buf.TotalSize,
		"is_complete":     buf.IsComplete,
		"created_at":      buf.CreatedAt,
		"last_chunk_at":   buf.LastChunkAt,
		"resume_count":    buf.ResumeCount,
		"age_seconds":     time.Since(buf.CreatedAt).Seconds(),
	}
}

// BufferData represents the data needed for stream resume
type BufferData struct {
	ConversationID  string
	UserID          string
	CombinedChunks  string
	IsComplete      bool
	ChunkCount      int
	PendingMessages []BufferedMessage // Tool results, artifacts, etc. to replay
}

// GetBufferData safely retrieves buffer data for resume operations
func (s *StreamBufferService) GetBufferData(conversationID string) (*BufferData, error) {
	s.mutex.Lock()
	defer s.mutex.Unlock()

	buf, exists := s.buffers[conversationID]
	if !exists {
		return nil, ErrBufferNotFound
	}

	// Rate limit: 1 resume per second
	if time.Since(buf.LastResume) < time.Second {
		return nil, ErrResumeTooFast
	}

	buf.ResumeCount++
	buf.LastResume = time.Now()

	buf.mutex.Lock()
	defer buf.mutex.Unlock()

	// Combine all chunks
	var combined strings.Builder
	for _, chunk := range buf.Chunks {
		combined.WriteString(chunk)
	}

	// Copy only undelivered pending messages
	var pendingMsgs []BufferedMessage
	for _, msg := range buf.PendingMessages {
		if !msg.Delivered {
			pendingMsgs = append(pendingMsgs, msg)
		}
	}

	return &BufferData{
		ConversationID:  buf.ConversationID,
		UserID:          buf.UserID,
		CombinedChunks:  combined.String(),
		IsComplete:      buf.IsComplete,
		ChunkCount:      len(buf.Chunks),
		PendingMessages: pendingMsgs,
	}, nil
}
