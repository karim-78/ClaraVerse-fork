package handlers

import (
	"bytes"
	"claraverse/internal/filecache"
	"claraverse/internal/models"
	"claraverse/internal/services"
	"claraverse/internal/utils"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/gofiber/contrib/websocket"
	"github.com/google/uuid"
)

// pushFileToSandbox uploads a file into the conversation's persistent E2B
// sandbox at /data/<filename>. Best-effort; callers treat false/error as a
// graceful degradation (preview still injected, just no sandbox path).
//
// We keep this lightweight (no dedicated client struct) because the e2b
// service URL never changes at runtime and the call is fire-and-forget per
// upload. The chat path is the only caller.
func pushFileToSandbox(conversationID, filename string, content []byte) (bool, error) {
	if conversationID == "" {
		return false, fmt.Errorf("no conversation id")
	}
	if len(content) == 0 {
		return false, fmt.Errorf("empty content")
	}
	baseURL := os.Getenv("E2B_SERVICE_URL")
	if baseURL == "" {
		baseURL = "http://e2b-service:8001"
	}
	body, err := json.Marshal(map[string]interface{}{
		"conversation_id": conversationID,
		"target_path":     "/data/" + filename,
		"content_b64":     base64.StdEncoding.EncodeToString(content),
	})
	if err != nil {
		return false, err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, "POST", baseURL+"/files/push", bytes.NewReader(body))
	if err != nil {
		return false, err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return false, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return false, fmt.Errorf("e2b push HTTP %d", resp.StatusCode)
	}
	return true, nil
}

// PromptResponse stores a user's response to an interactive prompt
type PromptResponse struct {
	PromptID   string
	UserID     string
	Answers    map[string]models.InteractiveAnswer
	Skipped    bool
	ReceivedAt time.Time
}

// PromptResponseCache stores prompt responses waiting to be processed
type PromptResponseCache struct {
	responses map[string]*PromptResponse // promptID -> response
	mutex     sync.RWMutex
}

// WebSocketHandler handles WebSocket connections
type WebSocketHandler struct {
	connManager      *services.ConnectionManager
	chatService      *services.ChatService
	analyticsService *services.AnalyticsService // Optional: minimal usage tracking
	usageLimiter     *services.UsageLimiterService
	promptCache      *PromptResponseCache // Cache for interactive prompt responses
}

// NewWebSocketHandler creates a new WebSocket handler
func NewWebSocketHandler(connManager *services.ConnectionManager, chatService *services.ChatService, analyticsService *services.AnalyticsService, usageLimiter *services.UsageLimiterService) *WebSocketHandler {
	return &WebSocketHandler{
		connManager:      connManager,
		chatService:      chatService,
		analyticsService: analyticsService,
		usageLimiter:     usageLimiter,
		promptCache: &PromptResponseCache{
			responses: make(map[string]*PromptResponse),
		},
	}
}

// Handle handles a new WebSocket connection
func (h *WebSocketHandler) Handle(c *websocket.Conn) {
	connID := uuid.New().String()
	userID := c.Locals("user_id").(string)
	clientIP, _ := c.Locals("client_ip").(string)

	// Create a done channel to signal goroutines to stop
	done := make(chan struct{})

	userConn := &models.UserConnection{
		ConnID:         connID,
		UserID:         userID,
		ClientIP:       clientIP,
		Conn:           c,
		ConversationID: "",
		Messages:       make([]map[string]interface{}, 0),
		MessageCount:   0,
		CreatedAt:      time.Now(),
		WriteChan:      make(chan models.ServerMessage, 100),
		StopChan:       make(chan bool, 1),
		// Create a waiter function that tools can use to wait for prompt responses
		PromptWaiter: func(promptID string, timeout time.Duration) (map[string]models.InteractiveAnswer, bool, error) {
			response, err := h.WaitForPromptResponse(promptID, timeout)
			if err != nil {
				return nil, false, err
			}
			return response.Answers, response.Skipped, nil
		},
	}

	h.connManager.Add(userConn)
	defer func() {
		close(done) // Signal all goroutines to stop
		h.connManager.Remove(connID)

		// Track session end (minimal analytics)
		if h.analyticsService != nil && userConn.ConversationID != "" {
			ctx := context.Background()
			h.analyticsService.TrackChatSessionEnd(ctx, connID, userConn.MessageCount)
		}
	}()

	// Configure WebSocket timeouts for long-running operations
	// Set read deadline to 6 minutes (allows for 5 min tool execution + buffer)
	c.SetReadDeadline(time.Now().Add(360 * time.Second))

	// Set up ping/pong handlers to keep connection alive during long tool executions
	c.SetPongHandler(func(appData string) error {
		// Reset read deadline on pong received
		c.SetReadDeadline(time.Now().Add(360 * time.Second))
		return nil
	})

	// Start ping goroutine to keep connection alive
	go h.pingLoop(userConn, done)

	// Start write goroutine
	go h.writeLoop(userConn)

	// Send connected message (no conversation_id - that comes from client)
	userConn.WriteChan <- models.ServerMessage{
		Type:    "connected",
		Content: "WebSocket connected. Ready to receive messages.",
	}

	// Read loop
	h.readLoop(userConn)
}

// pingLoop sends periodic pings to keep the WebSocket connection alive
// This is crucial for long-running tool executions (e.g., Python runner up to 5 min)
func (h *WebSocketHandler) pingLoop(userConn *models.UserConnection, done <-chan struct{}) {
	ticker := time.NewTicker(30 * time.Second) // Send ping every 30 seconds
	defer ticker.Stop()

	for {
		select {
		case <-done:
			return
		case <-ticker.C:
			userConn.Mutex.Lock()
			if err := userConn.Conn.WriteControl(websocket.PingMessage, []byte{}, time.Now().Add(10*time.Second)); err != nil {
				log.Printf("⚠️ Ping failed for %s: %v", userConn.ConnID, err)
				userConn.Mutex.Unlock()
				return
			}
			userConn.Mutex.Unlock()
		}
	}
}

// readLoop handles incoming messages from the client
func (h *WebSocketHandler) readLoop(userConn *models.UserConnection) {
	defer func() {
		if r := recover(); r != nil {
			log.Printf("❌ Panic in readLoop: %v", r)
		}
	}()

	for {
		_, msg, err := userConn.Conn.ReadMessage()
		if err != nil {
			log.Printf("❌ WebSocket read error for %s: %v", userConn.ConnID, err)
			break
		}

		// Reset read deadline after successful read
		userConn.Conn.SetReadDeadline(time.Now().Add(360 * time.Second))

		var clientMsg models.ClientMessage
		if err := json.Unmarshal(msg, &clientMsg); err != nil {
			log.Printf("⚠️  Invalid message format from %s: %v", userConn.ConnID, err)
			userConn.WriteChan <- models.ServerMessage{
				Type:         "error",
				ErrorCode:    "invalid_format",
				ErrorMessage: "Invalid message format",
			}
			continue
		}

		switch clientMsg.Type {
		case "ping":
			// Respond to client heartbeat immediately
			userConn.WriteChan <- models.ServerMessage{
				Type: "pong",
			}
		case "chat_message":
			h.handleChatMessage(userConn, clientMsg)
		case "new_conversation":
			h.handleNewConversation(userConn, clientMsg)
		case "stop_generation":
			h.handleStopGeneration(userConn)
		case "resume_stream":
			h.handleResumeStream(userConn, clientMsg)
		case "interactive_prompt_response":
			h.handleInteractivePromptResponse(userConn, clientMsg)
		default:
			log.Printf("⚠️  Unknown message type: %s", clientMsg.Type)
		}
	}
}

// handleChatMessage handles a chat message from the client
func (h *WebSocketHandler) handleChatMessage(userConn *models.UserConnection, clientMsg models.ClientMessage) {
	// Update conversation ID if provided
	if clientMsg.ConversationID != "" {
		userConn.ConversationID = clientMsg.ConversationID
	}

	// Update model ID if provided (platform model selection)
	if clientMsg.ModelID != "" {
		userConn.ModelID = clientMsg.ModelID
		log.Printf("🎯 Model selected for %s: %s", userConn.ConnID, clientMsg.ModelID)
	}

	// Update custom config if provided (BYOK)
	if clientMsg.CustomConfig != nil {
		userConn.CustomConfig = clientMsg.CustomConfig
		log.Printf("🔑 BYOK config updated for %s: Model=%s",
			userConn.ConnID, clientMsg.CustomConfig.Model)
	}

	// Update system instructions if provided (per-request override)
	if clientMsg.SystemInstructions != "" {
		userConn.SystemInstructions = clientMsg.SystemInstructions
		log.Printf("📝 System instructions updated for %s (length: %d chars)",
			userConn.ConnID, len(clientMsg.SystemInstructions))
	}

	// Update disable tools flag (e.g., for agent builder)
	userConn.DisableTools = clientMsg.DisableTools
	if userConn.DisableTools {
		log.Printf("🔒 Tools disabled for %s (agent builder mode)", userConn.ConnID)
	}

	// Update selected tools filter (e.g., from Dobby's Claw tool picker)
	userConn.SelectedTools = clientMsg.SelectedTools
	if len(userConn.SelectedTools) > 0 {
		log.Printf("🎯 Tool filter active for %s: %v", userConn.ConnID, userConn.SelectedTools)
	}

	// Per-turn RAG knowledge attachment. Reassigned EVERY turn (not
	// merged with prior) so a follow-up with no chips selected gets
	// no knowledge tool — matches the picker UX where chips are the
	// source of truth, not a persistent connection mode.
	userConn.KnowledgeProjectIDs = clientMsg.KnowledgeProjectIDs
	if len(userConn.KnowledgeProjectIDs) > 0 {
		log.Printf("📚 Knowledge attached for %s: %d project(s)", userConn.ConnID, len(userConn.KnowledgeProjectIDs))
	}

	// Propagate the per-turn reasoning_effort hint to the connection so
	// chat_service can attach it to the LLM request body.
	userConn.ReasoningEffort = clientMsg.ReasoningEffort
	if userConn.ReasoningEffort != "" {
		log.Printf("🧠 Reasoning effort: %s (user: %s)", userConn.ReasoningEffort, userConn.ConnID)
	}

	// Priority-based history handling: prefer backend cache, fall back to client history
	userConn.Mutex.Lock()

	// Step 1: Try to get messages from backend cache first
	var cachedMessages []map[string]interface{}
	if userConn.ConversationID != "" {
		cachedMessages = h.chatService.GetConversationMessages(userConn.ConversationID)
	}

	if len(cachedMessages) > 0 {
		// ✅ Cache HIT - backend has valid cache, use it (ignore client history)
		userConn.Messages = cachedMessages

		// Count assistant messages from cache
		assistantCount := 0
		for _, msg := range cachedMessages {
			if role, ok := msg["role"].(string); ok && role == "assistant" {
				assistantCount++
			}
		}
		userConn.MessageCount = assistantCount

		log.Printf("✅ [CACHE-HIT] Using backend cache for %s: %d messages (%d assistant)",
			userConn.ConversationID, len(cachedMessages), assistantCount)

	} else if len(clientMsg.History) > 0 {
		// ❌ Cache MISS - no backend cache, use client history and repopulate
		userConn.Messages = clientMsg.History

		// Count assistant messages from client history
		assistantCount := 0
		for _, msg := range clientMsg.History {
			if role, ok := msg["role"].(string); ok && role == "assistant" {
				assistantCount++
			}
		}
		userConn.MessageCount = assistantCount

		log.Printf("♻️  [CACHE-MISS] Recreating from client history for %s: %d messages (%d assistant)",
			userConn.ConversationID, len(clientMsg.History), assistantCount)

		// Repopulate backend cache from client history
		if userConn.ConversationID != "" {
			h.chatService.SetConversationMessages(userConn.ConversationID, clientMsg.History)
		}

	} else {
		// 🆕 New conversation - no cache, no history
		userConn.Messages = make([]map[string]interface{}, 0)
		userConn.MessageCount = 0

		log.Printf("🆕 [NEW-CONVERSATION] Starting fresh for %s", userConn.ConversationID)

		// Create conversation in database with ownership tracking
		if userConn.ConversationID != "" {
			if err := h.chatService.CreateConversation(userConn.ConversationID, userConn.UserID, "New Conversation"); err != nil {
				log.Printf("⚠️  Failed to create conversation in database: %v", err)
				// Continue anyway - conversation will work from cache
			}
		}
	}

	// 🔍 DIAGNOSTIC: Log final state after history handling
	log.Printf("🔍 [DIAGNOSTIC] After history handling - userConn.Messages count: %d, conversationID: %s",
		len(userConn.Messages), userConn.ConversationID)
	if len(userConn.Messages) > 0 {
		firstMsg := userConn.Messages[0]
		lastMsg := userConn.Messages[len(userConn.Messages)-1]
		log.Printf("🔍 [DIAGNOSTIC] First message role: %v, Last message role: %v",
			firstMsg["role"], lastMsg["role"])
	}

	userConn.Mutex.Unlock()

	// Add user message to conversation
	userConn.Mutex.Lock()

	// Build message content based on whether there are attachments
	var messageContent interface{}
	var documentContext strings.Builder
	var dataFileContext strings.Builder
	var expiredFiles []string // Track expired files

	if len(clientMsg.Attachments) > 0 {
		// Get file cache service
		fileCache := filecache.GetService()

		// Process document attachments first (PDF, DOCX, PPTX)
		for _, att := range clientMsg.Attachments {
			// Check for document files (PDF, DOCX, PPTX)
			isDocument := att.MimeType == "application/pdf" ||
				att.MimeType == "application/vnd.openxmlformats-officedocument.wordprocessingml.document" ||
				att.MimeType == "application/vnd.openxmlformats-officedocument.presentationml.presentation"

			if isDocument && att.FileID != "" {
				// Fetch document text from cache
				cachedFile, err := fileCache.GetByUserAndConversation(
					att.FileID,
					userConn.UserID,
					userConn.ConversationID,
				)

				if err != nil {
					log.Printf("⚠️  Failed to fetch document file %s: %v", att.FileID, err)
					// Track expired file instead of returning error
					expiredFiles = append(expiredFiles, att.Filename)
					continue
				}

				// Build document context
				documentContext.WriteString(fmt.Sprintf("\n\n[Document: %s]\n", att.Filename))
				documentContext.WriteString(fmt.Sprintf("Pages: %d | Words: %d\n\n", cachedFile.PageCount, cachedFile.WordCount))
				documentContext.WriteString(cachedFile.ExtractedText.String())
				documentContext.WriteString("\n---\n")

				log.Printf("📄 Injected document context: %s (%d words) for %s", att.Filename, cachedFile.WordCount, userConn.ConnID)
			}
		}

		// Process CSV/Excel/JSON/Text data files (for context)
		for _, att := range clientMsg.Attachments {
			// Check if it's a data file (CSV, Excel, JSON, Text)
			isDataFile := att.MimeType == "text/csv" ||
				att.MimeType == "text/plain" ||
				att.MimeType == "application/json" ||
				att.MimeType == "application/vnd.ms-excel" ||
				att.MimeType == "application/vnd.openxmlformats-officedocument.spreadsheetml.sheet"

			if isDataFile && att.FileID != "" {
				// Fetch data file from cache
				cachedFile, err := fileCache.GetByUserAndConversation(
					att.FileID,
					userConn.UserID,
					userConn.ConversationID,
				)

				if err != nil {
					log.Printf("⚠️  Failed to fetch data file %s: %v", att.FileID, err)
					// Track expired file instead of returning error
					expiredFiles = append(expiredFiles, att.Filename)
					continue
				}

				// Read file content to get preview (first 10 lines for CSV/text)
				uploadDir := os.Getenv("UPLOAD_DIR")
				if uploadDir == "" {
					uploadDir = "./uploads"
				}

				fileContent, err := os.ReadFile(cachedFile.FilePath)
				if err != nil {
					log.Printf("⚠️  Failed to read data file %s: %v", att.FileID, err)
					continue
				}

				// Get first 10 lines as preview
				lines := strings.Split(string(fileContent), "\n")
				previewLines := 10
				if len(lines) < previewLines {
					previewLines = len(lines)
				}
				preview := strings.Join(lines[:previewLines], "\n")

				// Push the file into the conversation's persistent E2B
				// sandbox at /data/<filename>. The sandbox is pooled by
				// conversation_id, so subsequent `run_python` calls can
				// open the file directly without the model first having
				// to reach into the filecache. Best-effort — if the push
				// fails (sandbox down, e2b unavailable) we just don't
				// advertise the sandbox path and the user can still ask
				// the model to read the inline preview.
				sandboxPath := ""
				if pushed, err := pushFileToSandbox(userConn.ConversationID, att.Filename, fileContent); err == nil && pushed {
					sandboxPath = "/data/" + att.Filename
					log.Printf("📤 [DATA] Pushed %s → %s (sandbox conv=%s)", att.Filename, sandboxPath, userConn.ConversationID)
				} else if err != nil {
					log.Printf("⚠️  [DATA] Sandbox push for %s failed (continuing without): %v", att.Filename, err)
				}

				// Build data file context
				dataFileContext.WriteString(fmt.Sprintf("\n\n[Data File: %s]\n", att.Filename))
				dataFileContext.WriteString(fmt.Sprintf("File ID: %s\n", att.FileID))
				dataFileContext.WriteString(fmt.Sprintf("Type: %s | Size: %d bytes\n", att.MimeType, cachedFile.Size))
				if sandboxPath != "" {
					dataFileContext.WriteString(fmt.Sprintf("Sandbox path: %s (available in run_python; `pd.read_csv('%s')` will work)\n", sandboxPath, sandboxPath))
				}
				dataFileContext.WriteString(fmt.Sprintf("\nPreview (first %d lines):\n", previewLines))
				dataFileContext.WriteString("```\n")
				dataFileContext.WriteString(preview)
				dataFileContext.WriteString("\n```\n")
				dataFileContext.WriteString("---\n")

				log.Printf("📊 Injected data file context: %s (file_id: %s) for %s", att.Filename, att.FileID, userConn.ConnID)
			}
		}

		// Check if we have ACTUAL images (for vision models)
		// CSV/Excel/JSON/Text files should NOT be treated as images
		hasImages := false
		imageRegistry := services.GetImageRegistryService()
		for _, att := range clientMsg.Attachments {
			// Only count as image if Type is "image" AND MimeType starts with "image/"
			isActualImage := att.Type == "image" && strings.HasPrefix(att.MimeType, "image/")
			log.Printf("📎 [ATTACHMENT] Type=%s, MimeType=%s, IsActualImage=%v, Filename=%s",
				att.Type, att.MimeType, isActualImage, att.Filename)
			if isActualImage {
				hasImages = true

				// Register image in the image registry for LLM referencing
				if att.FileID != "" && clientMsg.ConversationID != "" {
					handle := imageRegistry.RegisterUploadedImage(
						clientMsg.ConversationID,
						att.FileID,
						att.Filename,
						0, 0, // Width/height not available here, could be extracted from image if needed
					)
					log.Printf("📸 [IMAGE-REGISTRY] Registered uploaded image as %s (file_id: %s)", handle, att.FileID)
				}
			}
		}

		if hasImages {
			// Vision model format - array of content parts
			contentParts := []map[string]interface{}{}

			// Build combined text content with PDF and data file contexts
			textContent := clientMsg.Content
			if documentContext.Len() > 0 {
				textContent = documentContext.String() + "\n" + textContent
			}
			if dataFileContext.Len() > 0 {
				textContent = dataFileContext.String() + "\n" + textContent
			}
			// Add user query label
			if documentContext.Len() > 0 || dataFileContext.Len() > 0 {
				textContent = textContent + "\n\nUser query: " + clientMsg.Content
			}

			contentParts = append(contentParts, map[string]interface{}{
				"type": "text",
				"text": textContent,
			})

			// Add image attachments (only actual images, not CSV/data files)
			imageUtils := utils.NewImageUtils()
			for _, att := range clientMsg.Attachments {
				// Only process actual images (not CSV/Excel/JSON disguised as images)
				isActualImage := att.Type == "image" && strings.HasPrefix(att.MimeType, "image/")
				if isActualImage {
					var imageURL string

					// If URL is relative (starts with /uploads/), convert to base64
					if strings.HasPrefix(att.URL, "/uploads/") {
						// Extract filename and build local path
						filename := filepath.Base(att.URL)
						localPath := filepath.Join("./uploads", filename)

						// Convert to base64 data URL
						base64URL, err := imageUtils.EncodeToBase64(localPath)
						if err != nil {
							log.Printf("⚠️  Failed to encode image to base64: %v", err)
							// Fall back to original URL
							imageURL = att.URL
						} else {
							imageURL = base64URL
							log.Printf("🔄 Converted local image to base64 (size: %d bytes)", att.Size)
						}
					} else {
						// Already a full URL (http:// or https://)
						imageURL = att.URL
					}

					contentParts = append(contentParts, map[string]interface{}{
						"type": "image_url",
						"image_url": map[string]interface{}{
							"url": imageURL,
						},
					})
				}
			}

			messageContent = contentParts
			log.Printf("🖼️  Chat message from %s with %d attachment(s)", userConn.ConnID, len(clientMsg.Attachments))
		} else if documentContext.Len() > 0 || dataFileContext.Len() > 0 {
			// Document/Data file message (no images)
			var combinedContext strings.Builder
			if documentContext.Len() > 0 {
				combinedContext.WriteString(documentContext.String())
			}
			if dataFileContext.Len() > 0 {
				combinedContext.WriteString(dataFileContext.String())
			}
			combinedContext.WriteString("\n\nUser query: ")
			combinedContext.WriteString(clientMsg.Content)
			messageContent = combinedContext.String()
		} else {
			// No usable attachments
			messageContent = clientMsg.Content
		}
	} else {
		// Text-only message
		messageContent = clientMsg.Content
	}

	userConn.Mutex.Unlock()

	// Check message limits before processing
	if h.usageLimiter != nil {
		ctx := context.Background()

		if userConn.UserID == "anonymous" {
			// Anonymous: backend safety net — 50 messages/day per IP (browser-level 5-msg limit is primary)
			if err := h.usageLimiter.CheckAnonymousMessageLimit(ctx, userConn.ClientIP); err != nil {
				if limitErr, ok := err.(*services.LimitExceededError); ok {
					userConn.WriteChan <- models.ServerMessage{
						Type:         "limit_exceeded",
						ErrorCode:    limitErr.ErrorCode,
						ErrorMessage: limitErr.Message,
						Arguments: map[string]interface{}{
							"limit":      limitErr.Limit,
							"used":       limitErr.Used,
							"reset_at":   limitErr.ResetAt,
							"upgrade_to": limitErr.UpgradeTo,
						},
					}
					log.Printf("⚠️  [ANON-LIMIT] Anonymous IP %s exceeded daily safety limit", userConn.ClientIP)
					return
				}
			}
			go func() {
				if err := h.usageLimiter.IncrementAnonymousMessageCount(context.Background(), userConn.ClientIP); err != nil {
					log.Printf("⚠️  [ANON-LIMIT] Failed to increment counter for IP %s: %v", userConn.ClientIP, err)
				}
			}()
		} else {
			// Authenticated: per-user monthly limit
			if err := h.usageLimiter.CheckMessageLimit(ctx, userConn.UserID); err != nil {
				if limitErr, ok := err.(*services.LimitExceededError); ok {
					userConn.WriteChan <- models.ServerMessage{
						Type:         "limit_exceeded",
						ErrorCode:    limitErr.ErrorCode,
						ErrorMessage: limitErr.Message,
						Arguments: map[string]interface{}{
							"limit":      limitErr.Limit,
							"used":       limitErr.Used,
							"reset_at":   limitErr.ResetAt,
							"upgrade_to": limitErr.UpgradeTo,
						},
					}
					log.Printf("⚠️  [LIMIT] Message limit exceeded for user %s: %s", userConn.UserID, limitErr.Message)
					return
				}
			}
			go func() {
				if err := h.usageLimiter.IncrementMessageCount(context.Background(), userConn.UserID); err != nil {
					log.Printf("⚠️  [LIMIT] Failed to increment message count for user %s: %v", userConn.UserID, err)
				}
			}()
		}
	}

	// Add user message to conversation cache via ChatService
	h.chatService.AddUserMessage(userConn.ConversationID, messageContent)

	log.Printf("💬 Chat message from %s (user: %s, length: %d chars)",
		userConn.ConnID, userConn.UserID, len(clientMsg.Content))

	// Send warning if any files have expired
	if len(expiredFiles) > 0 {
		warningMsg := fmt.Sprintf("⚠ Warning: %d file(s) expired and unavailable: %s",
			len(expiredFiles), strings.Join(expiredFiles, ", "))
		log.Printf("⚠️  [FILE-EXPIRED] %s", warningMsg)

		userConn.WriteChan <- models.ServerMessage{
			Type:         "files_expired",
			ErrorCode:    "files_expired",
			ErrorMessage: warningMsg,
			Content:      strings.Join(expiredFiles, ", "), // File names as comma-separated string
		}
	}

	// Stream response
	go func() {
		if err := h.chatService.StreamChatCompletion(userConn); err != nil {
			log.Printf("❌ Chat completion error: %v", err)
			userConn.WriteChan <- models.ServerMessage{
				Type:         "error",
				ErrorCode:    "chat_error",
				ErrorMessage: err.Error(),
			}
		}
	}()
}

// handleNewConversation handles starting a new conversation (clears history)
func (h *WebSocketHandler) handleNewConversation(userConn *models.UserConnection, clientMsg models.ClientMessage) {
	userConn.Mutex.Lock()

	// Clear all conversation history
	userConn.Messages = make([]map[string]interface{}, 0)
	userConn.MessageCount = 0 // Reset message counter for new conversation

	// Update conversation ID
	if clientMsg.ConversationID != "" {
		userConn.ConversationID = clientMsg.ConversationID
	} else {
		userConn.ConversationID = uuid.New().String()
	}

	// Update model if provided
	if clientMsg.ModelID != "" {
		userConn.ModelID = clientMsg.ModelID
	}

	// Update system instructions if provided
	if clientMsg.SystemInstructions != "" {
		userConn.SystemInstructions = clientMsg.SystemInstructions
	}

	// Update custom config if provided
	if clientMsg.CustomConfig != nil {
		userConn.CustomConfig = clientMsg.CustomConfig
	}

	userConn.Mutex.Unlock()

	// Clear conversation cache
	h.chatService.ClearConversation(userConn.ConversationID)

	// Create conversation in database with ownership tracking
	if err := h.chatService.CreateConversation(userConn.ConversationID, userConn.UserID, "New Conversation"); err != nil {
		log.Printf("⚠️  Failed to create conversation in database: %v", err)
		// Continue anyway - conversation will work from cache
	}
	
	// Track chat session start (minimal analytics)
	if h.analyticsService != nil {
		ctx := context.Background()
		h.analyticsService.TrackChatSessionStart(ctx, userConn.ConnID, userConn.UserID, userConn.ConversationID)
		
		// Update model info if available
		if userConn.ModelID != "" {
			h.analyticsService.UpdateChatSessionModel(ctx, userConn.ConnID, userConn.ModelID, userConn.DisableTools)
		}
	}

	log.Printf("🆕 New conversation started for %s: conversation_id=%s, model=%s",
		userConn.ConnID, userConn.ConversationID, userConn.ModelID)

	// Send acknowledgment
	userConn.WriteChan <- models.ServerMessage{
		Type:           "conversation_reset",
		ConversationID: userConn.ConversationID,
		Content:        "New conversation started",
	}
}

// handleStopGeneration handles a stop generation request
func (h *WebSocketHandler) handleStopGeneration(userConn *models.UserConnection) {
	select {
	case userConn.StopChan <- true:
		log.Printf("⏹️  Stop signal sent for %s", userConn.ConnID)
	default:
		log.Printf("⚠️  Stop channel full or closed for %s", userConn.ConnID)
	}
}

// handleResumeStream handles a request to resume a disconnected stream
func (h *WebSocketHandler) handleResumeStream(userConn *models.UserConnection, clientMsg models.ClientMessage) {
	conversationID := clientMsg.ConversationID
	if conversationID == "" {
		log.Printf("⚠️  Resume stream request with empty conversation ID from %s", userConn.ConnID)
		userConn.WriteChan <- models.ServerMessage{
			Type:         "error",
			ErrorCode:    "missing_conversation_id",
			ErrorMessage: "Conversation ID is required for resume",
		}
		return
	}

	log.Printf("🔄 [RESUME] Resume stream request for conversation %s from %s", conversationID, userConn.ConnID)

	// Get the stream buffer
	streamBuffer := h.chatService.GetStreamBuffer()
	bufferData, err := streamBuffer.GetBufferData(conversationID)

	if err != nil {
		// Buffer not found or rate limited
		log.Printf("⚠️  [RESUME] Buffer not available for %s: %v", conversationID, err)
		userConn.WriteChan <- models.ServerMessage{
			Type:           "stream_missed",
			ConversationID: conversationID,
			Reason:         "expired",
		}
		return
	}

	// Validate user owns this buffer
	if bufferData.UserID != userConn.UserID {
		log.Printf("⚠️  [RESUME] User %s attempted to resume buffer owned by %s", userConn.UserID, bufferData.UserID)
		userConn.WriteChan <- models.ServerMessage{
			Type:           "stream_missed",
			ConversationID: conversationID,
			Reason:         "not_found",
		}
		return
	}

	log.Printf("📦 [RESUME] Sending %d buffered chunks (%d bytes), %d pending messages for conversation %s (complete: %v)",
		bufferData.ChunkCount, len(bufferData.CombinedChunks), len(bufferData.PendingMessages), conversationID, bufferData.IsComplete)

	// First, replay any pending messages (tool results with artifacts, etc.)
	// These are critical messages that might have been missed during disconnect
	for _, pendingMsg := range bufferData.PendingMessages {
		// Skip already delivered messages (prevents duplicates on rapid reconnects)
		if pendingMsg.Delivered {
			continue
		}
		
		log.Printf("📦 [RESUME] Replaying pending message type=%s tool=%s for conversation %s", 
			pendingMsg.Type, pendingMsg.ToolName, conversationID)
		
		// Convert BufferedMessage to ServerMessage with all fields
		serverMsg := models.ServerMessage{
			Type:            pendingMsg.Type,
			ToolName:        pendingMsg.ToolName,
			ToolDisplayName: pendingMsg.ToolDisplayName,
			ToolIcon:        pendingMsg.ToolIcon,
			ToolDescription: pendingMsg.ToolDescription,
			Status:          pendingMsg.Status,
			Result:          pendingMsg.Result,
		}
		
		// Handle plots (for image artifacts)
		if pendingMsg.Plots != nil {
			if plots, ok := pendingMsg.Plots.([]models.PlotData); ok {
				serverMsg.Plots = plots
			} else {
				log.Printf("⚠️ [RESUME] Failed to cast plots for %s - type: %T", pendingMsg.ToolName, pendingMsg.Plots)
			}
		}
		
		userConn.WriteChan <- serverMsg
	}
	
	// Mark pending messages as delivered (prevents duplicates on next resume)
	streamBuffer.MarkMessagesDelivered(conversationID)

	// Send the resume message with all buffered text content
	if len(bufferData.CombinedChunks) > 0 {
		userConn.WriteChan <- models.ServerMessage{
			Type:           "stream_resume",
			ConversationID: conversationID,
			Content:        bufferData.CombinedChunks,
			IsComplete:     bufferData.IsComplete,
		}
	}

	// If the stream is complete, also send stream_end
	if bufferData.IsComplete {
		userConn.WriteChan <- models.ServerMessage{
			Type:           "stream_end",
			ConversationID: conversationID,
		}
		// Clear the buffer since it's complete and delivered
		streamBuffer.ClearBuffer(conversationID)
		log.Printf("📦 [RESUME] Stream complete, buffer cleared for conversation %s", conversationID)
	} else {
		// Stream still in progress - update connection ID so new chunks go to this connection
		// Note: The stream buffer continues to collect chunks from the ongoing generation
		log.Printf("📦 [RESUME] Stream still in progress for conversation %s", conversationID)
	}
}

// handleInteractivePromptResponse handles a user's response to an interactive prompt
func (h *WebSocketHandler) handleInteractivePromptResponse(userConn *models.UserConnection, clientMsg models.ClientMessage) {
	promptID := clientMsg.PromptID
	if promptID == "" {
		log.Printf("⚠️  Interactive prompt response with empty prompt ID from %s", userConn.ConnID)
		userConn.WriteChan <- models.ServerMessage{
			Type:         "error",
			ErrorCode:    "missing_prompt_id",
			ErrorMessage: "Prompt ID is required",
		}
		return
	}

	if clientMsg.Skipped {
		log.Printf("📋 [PROMPT] User %s skipped prompt %s", userConn.UserID, promptID)
	} else {
		log.Printf("📋 [PROMPT] User %s answered prompt %s with %d answers", userConn.UserID, promptID, len(clientMsg.Answers))

		// Log each answer for debugging
		for questionID, answer := range clientMsg.Answers {
			log.Printf("   Question %s: %v (is_other: %v)", questionID, answer.Value, answer.IsOther)
		}
	}

	// Store the response in cache for the waiting tool execution
	h.promptCache.mutex.Lock()
	h.promptCache.responses[promptID] = &PromptResponse{
		PromptID:   promptID,
		UserID:     userConn.UserID,
		Answers:    clientMsg.Answers,
		Skipped:    clientMsg.Skipped,
		ReceivedAt: time.Now(),
	}
	h.promptCache.mutex.Unlock()

	log.Printf("✅ [PROMPT] Prompt %s response stored in cache (waiting tool will receive it)", promptID)
}

// SendInteractivePrompt sends an interactive prompt to the client
// This can be called from anywhere (e.g., during tool execution) to ask the user questions
func (h *WebSocketHandler) SendInteractivePrompt(userConn *models.UserConnection, prompt models.ServerMessage) bool {
	if prompt.Type != "interactive_prompt" {
		log.Printf("⚠️  SendInteractivePrompt called with invalid type: %s", prompt.Type)
		return false
	}

	if prompt.PromptID == "" {
		log.Printf("⚠️  SendInteractivePrompt called with empty PromptID")
		return false
	}

	if len(prompt.Questions) == 0 {
		log.Printf("⚠️  SendInteractivePrompt called with no questions")
		return false
	}

	// Set conversation ID
	prompt.ConversationID = userConn.ConversationID

	// Send the prompt
	success := userConn.SafeSend(prompt)
	if success {
		log.Printf("📋 [PROMPT] Sent interactive prompt %s with %d questions to user %s",
			prompt.PromptID, len(prompt.Questions), userConn.UserID)
	} else {
		log.Printf("❌ [PROMPT] Failed to send interactive prompt %s to user %s",
			prompt.PromptID, userConn.UserID)
	}

	return success
}

// WaitForPromptResponse waits for a user to respond to an interactive prompt
// Blocks until response is received or timeout occurs (default 5 minutes)
func (h *WebSocketHandler) WaitForPromptResponse(promptID string, timeout time.Duration) (*PromptResponse, error) {
	deadline := time.Now().Add(timeout)
	pollInterval := 100 * time.Millisecond

	log.Printf("⏳ [PROMPT] Waiting for response to prompt %s (timeout: %v)", promptID, timeout)

	for time.Now().Before(deadline) {
		// Check if response exists
		h.promptCache.mutex.RLock()
		response, exists := h.promptCache.responses[promptID]
		h.promptCache.mutex.RUnlock()

		if exists {
			// Remove from cache
			h.promptCache.mutex.Lock()
			delete(h.promptCache.responses, promptID)
			h.promptCache.mutex.Unlock()

			log.Printf("✅ [PROMPT] Received response for prompt %s after %.2f seconds",
				promptID, time.Since(response.ReceivedAt.Add(-time.Since(response.ReceivedAt))).Seconds())
			return response, nil
		}

		// Sleep before next poll
		time.Sleep(pollInterval)
	}

	// Timeout
	log.Printf("⏱️  [PROMPT] Timeout waiting for response to prompt %s", promptID)
	return nil, fmt.Errorf("timeout waiting for user response")
}

// writeLoop handles outgoing messages to the client
func (h *WebSocketHandler) writeLoop(userConn *models.UserConnection) {
	defer func() {
		if r := recover(); r != nil {
			log.Printf("❌ Panic in writeLoop: %v", r)
		}
	}()

	for msg := range userConn.WriteChan {
		if err := userConn.Conn.WriteJSON(msg); err != nil {
			log.Printf("❌ WebSocket write error for %s: %v", userConn.ConnID, err)
			return
		}
	}
}
