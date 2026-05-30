package handlers

import (
	"claraverse/internal/audio"
	"claraverse/internal/filecache"
	"claraverse/internal/models"
	"claraverse/internal/services"
	"claraverse/internal/utils"
	"context"
	"encoding/base64"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/gofiber/fiber/v2"
	"github.com/google/uuid"
	"go.mongodb.org/mongo-driver/bson/primitive"
)

// PendingMediaType represents the type of pending media
type PendingMediaType string

const (
	PendingMediaPhoto    PendingMediaType = "photo"
	PendingMediaDocument PendingMediaType = "document"
	PendingMediaTTL                       = 60 * time.Second // How long to wait for follow-up text

	// Telegram rate limiting - 10 messages per minute per user
	telegramRateLimitWindow  = 60 * time.Second
	telegramRateLimitMaxMsgs = 10
)

// PendingMedia stores media waiting for user's follow-up text message
type PendingMedia struct {
	Type      PendingMediaType
	FileID    string
	MimeType  string
	FileName  string
	ExpiresAt time.Time
	ChannelID primitive.ObjectID
	SessionID primitive.ObjectID
	BotToken  string
}

// pendingMediaCache stores media waiting for text per chat
var (
	pendingMediaCache = make(map[int64]*PendingMedia)
	pendingMediaMutex sync.RWMutex
)

// telegramRateLimiter tracks message timestamps per user for rate limiting
var (
	telegramRateLimit      = make(map[string][]time.Time) // key: channelID:userID
	telegramRateLimitMutex sync.Mutex
)

// ChannelHandler handles channel management and webhook endpoints
type ChannelHandler struct {
	channelService          *services.ChannelService
	chatService             *services.ChatService
	toolService             *services.ToolService
	toolPredictorService    *services.ToolPredictorService    // Smart tool routing
	memorySelectionService  *services.MemorySelectionService  // Memory injection
	memoryExtractionService *services.MemoryExtractionService // Memory extraction
	userService             *services.UserService             // User preferences
	cortexService           *services.CortexService           // Nexus orchestrator
}

// NewChannelHandler creates a new channel handler
func NewChannelHandler(channelService *services.ChannelService, chatService *services.ChatService, toolService *services.ToolService) *ChannelHandler {
	h := &ChannelHandler{
		channelService: channelService,
		chatService:    chatService,
		toolService:    toolService,
	}

	// Set up the message handler for long polling mode
	channelService.SetMessageHandler(func(channel *models.Channel, session *models.ChannelSession, message *models.TelegramMessage) {
		// Create an update wrapper to reuse processTelegramMessage
		update := &models.TelegramUpdate{
			Message: message,
		}
		go h.processTelegramMessage(channel, update)
	})

	// Start polling for localhost mode
	go channelService.StartPolling(context.Background())

	return h
}

// SetToolPredictorService sets the tool predictor service for smart tool routing
func (h *ChannelHandler) SetToolPredictorService(svc *services.ToolPredictorService) {
	h.toolPredictorService = svc
	log.Println("✅ [CHANNEL] Tool predictor service set for smart tool routing")
}

// SetMemorySelectionService sets the memory selection service for memory injection
func (h *ChannelHandler) SetMemorySelectionService(svc *services.MemorySelectionService) {
	h.memorySelectionService = svc
	log.Println("✅ [CHANNEL] Memory selection service set for memory injection")
}

// SetMemoryExtractionService sets the memory extraction service for conversation memory extraction
func (h *ChannelHandler) SetMemoryExtractionService(svc *services.MemoryExtractionService) {
	h.memoryExtractionService = svc
	log.Println("✅ [CHANNEL] Memory extraction service set for conversation memory extraction")
}

// SetUserService sets the user service for preference checking
func (h *ChannelHandler) SetUserService(svc *services.UserService) {
	h.userService = svc
	log.Println("✅ [CHANNEL] User service set for preference checking")
}

// SetChatService sets the chat service (for late initialization)
func (h *ChannelHandler) SetChatService(chatService *services.ChatService) {
	h.chatService = chatService
}

// SetCortexService sets the Cortex orchestrator for Nexus-powered Telegram responses
func (h *ChannelHandler) SetCortexService(svc *services.CortexService) {
	h.cortexService = svc
	log.Println("✅ [CHANNEL] Cortex service set for Nexus-powered Telegram responses")
}

// ============================================================================
// Channel Management Endpoints (Authenticated)
// ============================================================================

// CreateChannel creates a new channel
// POST /api/channels
func (h *ChannelHandler) CreateChannel(c *fiber.Ctx) error {
	userID, ok := c.Locals("user_id").(string)
	if !ok || userID == "" {
		return c.Status(401).JSON(fiber.Map{"error": "Authentication required"})
	}

	var req models.CreateChannelRequest
	if err := c.BodyParser(&req); err != nil {
		return c.Status(400).JSON(fiber.Map{"error": "Invalid request body"})
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	channel, err := h.channelService.Create(ctx, userID, &req)
	if err != nil {
		return c.Status(400).JSON(fiber.Map{"error": err.Error()})
	}

	// Start polling if in localhost mode
	if h.channelService.IsLocalhost() && req.Platform == models.ChannelPlatformTelegram {
		if botToken, ok := req.Config["bot_token"].(string); ok {
			channelID, _ := primitive.ObjectIDFromHex(channel.ID)
			h.channelService.StartPollerForChannel(channelID, botToken)
		}
	}

	return c.Status(201).JSON(channel)
}

// ListChannels lists all channels for the user
// GET /api/channels
func (h *ChannelHandler) ListChannels(c *fiber.Ctx) error {
	userID, ok := c.Locals("user_id").(string)
	if !ok || userID == "" {
		return c.Status(401).JSON(fiber.Map{"error": "Authentication required"})
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	channels, err := h.channelService.ListByUser(ctx, userID)
	if err != nil {
		return c.Status(500).JSON(fiber.Map{"error": err.Error()})
	}

	return c.JSON(models.ListChannelsResponse{
		Channels: convertToValues(channels),
		Total:    len(channels),
	})
}

// GetChannel gets a channel by ID
// GET /api/channels/:id
func (h *ChannelHandler) GetChannel(c *fiber.Ctx) error {
	userID, ok := c.Locals("user_id").(string)
	if !ok || userID == "" {
		return c.Status(401).JSON(fiber.Map{"error": "Authentication required"})
	}

	channelID, err := primitive.ObjectIDFromHex(c.Params("id"))
	if err != nil {
		return c.Status(400).JSON(fiber.Map{"error": "Invalid channel ID"})
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	channel, err := h.channelService.GetByIDAndUser(ctx, channelID, userID)
	if err != nil {
		return c.Status(404).JSON(fiber.Map{"error": "Channel not found"})
	}

	return c.JSON(h.toResponse(channel))
}

// UpdateChannel updates a channel
// PUT /api/channels/:id
func (h *ChannelHandler) UpdateChannel(c *fiber.Ctx) error {
	userID, ok := c.Locals("user_id").(string)
	if !ok || userID == "" {
		return c.Status(401).JSON(fiber.Map{"error": "Authentication required"})
	}

	channelID, err := primitive.ObjectIDFromHex(c.Params("id"))
	if err != nil {
		return c.Status(400).JSON(fiber.Map{"error": "Invalid channel ID"})
	}

	var req models.UpdateChannelRequest
	if err := c.BodyParser(&req); err != nil {
		return c.Status(400).JSON(fiber.Map{"error": "Invalid request body"})
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	channel, err := h.channelService.Update(ctx, channelID, userID, &req)
	if err != nil {
		return c.Status(400).JSON(fiber.Map{"error": err.Error()})
	}

	return c.JSON(channel)
}

// DeleteChannel deletes a channel
// DELETE /api/channels/:id
func (h *ChannelHandler) DeleteChannel(c *fiber.Ctx) error {
	userID, ok := c.Locals("user_id").(string)
	if !ok || userID == "" {
		return c.Status(401).JSON(fiber.Map{"error": "Authentication required"})
	}

	channelID, err := primitive.ObjectIDFromHex(c.Params("id"))
	if err != nil {
		return c.Status(400).JSON(fiber.Map{"error": "Invalid channel ID"})
	}

	// Stop poller if running
	h.channelService.StopPollerForChannel(channelID)

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	if err := h.channelService.Delete(ctx, channelID, userID); err != nil {
		return c.Status(400).JSON(fiber.Map{"error": err.Error()})
	}

	return c.JSON(fiber.Map{"success": true})
}

// TestChannel tests a channel connection
// POST /api/channels/:id/test
func (h *ChannelHandler) TestChannel(c *fiber.Ctx) error {
	userID, ok := c.Locals("user_id").(string)
	if !ok || userID == "" {
		return c.Status(401).JSON(fiber.Map{"error": "Authentication required"})
	}

	channelID, err := primitive.ObjectIDFromHex(c.Params("id"))
	if err != nil {
		return c.Status(400).JSON(fiber.Map{"error": "Invalid channel ID"})
	}

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	result, err := h.channelService.TestChannel(ctx, channelID, userID)
	if err != nil {
		return c.Status(400).JSON(fiber.Map{"error": err.Error()})
	}

	return c.JSON(result)
}

// ============================================================================
// Webhook Endpoints (Public - Verified by Secret)
// ============================================================================

// TelegramWebhook handles incoming Telegram webhook requests
// POST /api/channels/telegram/webhook/:secret
func (h *ChannelHandler) TelegramWebhook(c *fiber.Ctx) error {
	secret := c.Params("secret")
	if secret == "" {
		return c.Status(400).JSON(fiber.Map{"error": "Invalid webhook"})
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	// Find channel by webhook secret
	channel, err := h.channelService.GetByWebhookSecret(ctx, secret)
	if err != nil {
		log.Printf("⚠️ [TELEGRAM-WEBHOOK] Invalid webhook secret")
		return c.Status(404).JSON(fiber.Map{"error": "Invalid webhook"})
	}

	if !channel.Enabled {
		log.Printf("⚠️ [TELEGRAM-WEBHOOK] Channel %s is disabled", channel.ID.Hex())
		return c.SendStatus(200) // Return 200 to prevent Telegram from retrying
	}

	// Parse Telegram update
	var update models.TelegramUpdate
	if err := c.BodyParser(&update); err != nil {
		log.Printf("⚠️ [TELEGRAM-WEBHOOK] Failed to parse update: %v", err)
		return c.SendStatus(200)
	}

	// Only handle text messages for now
	if update.Message == nil || update.Message.Text == "" {
		return c.SendStatus(200)
	}

	// Process message asynchronously
	go h.processTelegramMessage(channel, &update)

	// Return 200 immediately to acknowledge receipt
	return c.SendStatus(200)
}

// processTelegramMessage processes an incoming Telegram message
func (h *ChannelHandler) processTelegramMessage(channel *models.Channel, update *models.TelegramUpdate) {
	ctx, cancel := context.WithTimeout(context.Background(), 180*time.Second) // Increased timeout for tool calls
	defer cancel()

	msg := update.Message
	chatID := msg.Chat.ID
	userID := fmt.Sprintf("%d", msg.From.ID)
	username := ""
	if msg.From != nil {
		username = msg.From.Username
	}

	// Get text content - either from text field or caption (for media)
	text := strings.TrimSpace(msg.Text)
	if text == "" && msg.Caption != "" {
		text = strings.TrimSpace(msg.Caption)
	}

	// Determine message type for logging
	msgType := "text"
	if len(msg.Photo) > 0 {
		msgType = "photo"
	} else if msg.Voice != nil {
		msgType = "voice"
	} else if msg.Document != nil {
		msgType = "document"
	}

	log.Printf("📨 [TELEGRAM] Received %s from user %s (@%s) in chat %d: %s",
		msgType, userID, username, chatID, truncateText(text, 50))

	// Get decrypted config for bot token
	config, err := h.channelService.GetDecryptedConfig(ctx, channel)
	if err != nil {
		log.Printf("❌ [TELEGRAM] Failed to get channel config: %v", err)
		return
	}
	botToken, _ := config["bot_token"].(string)

	// Check if user is allowed to use this channel
	if !h.channelService.IsUserAllowed(channel, userID, username) {
		log.Printf("🚫 [TELEGRAM] User %s (@%s) not in allowlist for channel %s", userID, username, channel.ID.Hex())
		h.channelService.SendTelegramMessage(ctx, botToken, chatID,
			"⛔ You are not authorized to use this bot. Please contact the bot owner for access.")
		return
	}

	// Check rate limit (10 messages per minute)
	if h.checkTelegramRateLimit(channel.ID.Hex(), userID) {
		log.Printf("⏳ [TELEGRAM] Rate limit exceeded for user %s in channel %s", userID, channel.ID.Hex())
		h.channelService.SendTelegramMessage(ctx, botToken, chatID,
			"⏳ You're sending messages too quickly. Please wait a moment (max 10 messages per minute).")
		return
	}

	// Get or create session
	session, err := h.channelService.GetOrCreateSession(ctx, channel.ID, userID, fmt.Sprintf("%d", chatID))
	if err != nil {
		log.Printf("❌ [TELEGRAM] Failed to get session: %v", err)
		h.sendTelegramError(ctx, botToken, chatID, "Sorry, there was an error processing your request.")
		return
	}

	// Handle commands (only from text messages)
	if strings.HasPrefix(text, "/") {
		// Clear any pending media on command
		h.clearPendingMedia(chatID)
		h.handleTelegramCommand(ctx, channel, session, botToken, chatID, text)
		return
	}

	// Handle photo messages - store for later if no caption
	if len(msg.Photo) > 0 {
		if text == "" {
			// No caption - store media and wait for follow-up text
			photo := msg.Photo[len(msg.Photo)-1]
			h.storePendingMedia(chatID, &PendingMedia{
				Type:      PendingMediaPhoto,
				FileID:    photo.FileID,
				MimeType:  "image/jpeg",
				ExpiresAt: time.Now().Add(PendingMediaTTL),
				ChannelID: channel.ID,
				SessionID: session.ID,
				BotToken:  botToken,
			})
			h.channelService.SendTelegramMessage(ctx, botToken, chatID,
				"📷 Got it! What would you like me to do with this image? (Type your question)")
			log.Printf("📷 [TELEGRAM] Stored pending photo for chat %d, waiting for text", chatID)
			return
		}
		// Has caption - process immediately
		h.processTelegramPhoto(ctx, channel, session, msg, botToken, chatID, text)
		return
	}

	// Handle voice messages (process immediately - user can't add text after)
	if msg.Voice != nil {
		h.clearPendingMedia(chatID) // Clear any pending media
		h.processTelegramVoice(ctx, channel, session, msg, botToken, chatID)
		return
	}

	// Handle document messages
	if msg.Document != nil {
		mimeType := msg.Document.MimeType
		isImage := strings.HasPrefix(mimeType, "image/")
		isPDF := mimeType == "application/pdf"
		isDOCX := mimeType == "application/vnd.openxmlformats-officedocument.wordprocessingml.document" ||
			strings.HasSuffix(strings.ToLower(msg.Document.FileName), ".docx")

		if (isImage || isPDF || isDOCX) && text == "" {
			// No caption - store document and wait for follow-up text
			h.storePendingMedia(chatID, &PendingMedia{
				Type:      PendingMediaDocument,
				FileID:    msg.Document.FileID,
				MimeType:  mimeType,
				FileName:  msg.Document.FileName,
				ExpiresAt: time.Now().Add(PendingMediaTTL),
				ChannelID: channel.ID,
				SessionID: session.ID,
				BotToken:  botToken,
			})
			fileType := "file"
			if isImage {
				fileType = "image"
			} else if isPDF {
				fileType = "PDF"
			} else if isDOCX {
				fileType = "document"
			}
			h.channelService.SendTelegramMessage(ctx, botToken, chatID,
				fmt.Sprintf("📄 Got your %s! What would you like me to do with it? (Type your question)", fileType))
			log.Printf("📄 [TELEGRAM] Stored pending document for chat %d, waiting for text", chatID)
			return
		}
		// Has caption or unsupported type - process/route normally
		h.processTelegramDocument(ctx, channel, session, msg, botToken, chatID, text)
		return
	}

	// Handle regular text messages - check for pending media first
	if text == "" {
		h.channelService.SendTelegramMessage(ctx, botToken, chatID,
			"I received your message but couldn't find any text. Please send a text message, photo, or voice note.")
		return
	}

	// Check if there's pending media to process with this text
	if pending := h.getPendingMedia(chatID); pending != nil {
		log.Printf("🔗 [TELEGRAM] Processing pending %s with text: %s", pending.Type, truncateText(text, 30))
		h.clearPendingMedia(chatID)
		h.processPendingMediaWithText(ctx, channel, session, pending, msg, botToken, chatID, text)
		return
	}

	// Start continuous typing indicator (runs until we cancel it)
	typingCtx, cancelTyping := context.WithCancel(ctx)
	go h.channelService.SendContinuousTypingAction(typingCtx, botToken, chatID)

	// Add user message to session history
	h.channelService.AddMessageToSession(ctx, session.ID, "user", text, channel.MaxHistoryMessages)

	// Route through Cortex if available (enables multi-agent Nexus execution)
	if h.cortexService != nil {
		cortexResult, cortexErr := h.cortexService.HandleUserMessageSync(ctx, channel.UserID, text, channel.DefaultModelID)
		cancelTyping()

		if cortexErr != nil {
			log.Printf("❌ [TELEGRAM] Cortex error: %v, falling back to direct chat", cortexErr)
			// Fallback: re-start typing for direct chat path
			typingCtx, typingCancel := context.WithCancel(ctx)
			cancelTyping = typingCancel
			go h.channelService.SendContinuousTypingAction(typingCtx, botToken, chatID)
		} else {
			// Add response to session and send
			h.channelService.AddMessageToSession(ctx, session.ID, "assistant", cortexResult, channel.MaxHistoryMessages)
			h.channelService.IncrementMessageCount(ctx, channel.ID)
			if err := h.channelService.SendTelegramMessageChunked(ctx, botToken, chatID, cortexResult); err != nil {
				log.Printf("❌ [TELEGRAM] Failed to send Cortex response: %v", err)
			} else {
				log.Printf("✅ [TELEGRAM/CORTEX] Sent response to chat %d (%d chars)", chatID, len(cortexResult))
			}
			// Extract memories from conversation
			if h.memoryExtractionService != nil {
				go h.queueMemoryExtraction(context.Background(), channel, session,
					[]map[string]interface{}{{"role": "user", "content": text}}, cortexResult)
			}
			return
		}
	}

	// Build conversation history for AI
	history, _ := h.channelService.GetSessionHistory(ctx, session.ID)
	messages := buildChatMessages(history, channel.DefaultSystemPrompt)

	// Get AI response (with tool support)
	result, err := h.getAIResponse(ctx, channel, session, messages)

	// Stop typing indicator before sending response
	cancelTyping()

	if err != nil {
		log.Printf("❌ [TELEGRAM] AI error: %v", err)
		h.sendTelegramError(ctx, botToken, chatID, "Sorry, I encountered an error. Please try again.")
		return
	}

	// Add assistant response to session
	h.channelService.AddMessageToSession(ctx, session.ID, "assistant", result.Response, channel.MaxHistoryMessages)

	// Increment message count
	h.channelService.IncrementMessageCount(ctx, channel.ID)

	// Check if images were generated - send them as photos
	if len(result.GeneratedImages) > 0 {
		for i, imageData := range result.GeneratedImages {
			log.Printf("🖼️ [TELEGRAM] Sending generated image %d/%d (%d bytes)", i+1, len(result.GeneratedImages), len(imageData))
			caption := ""
			if i == 0 {
				// Use text response as caption for first image (truncated to 1024 chars)
				caption = result.Response
				if len(caption) > 1000 {
					caption = caption[:997] + "..."
				}
			}
			if err := h.channelService.SendTelegramPhoto(ctx, botToken, chatID, imageData, caption); err != nil {
				log.Printf("⚠️ [TELEGRAM] Failed to send generated image: %v, falling back to text", err)
				// Fall back to text response
				h.channelService.SendTelegramMessageChunked(ctx, botToken, chatID, result.Response)
				break
			}
		}
		log.Printf("✅ [TELEGRAM] Sent %d generated image(s) to chat %d", len(result.GeneratedImages), chatID)
	} else {
		// No images - send text response (chunked for long messages)
		if err := h.channelService.SendTelegramMessageChunked(ctx, botToken, chatID, result.Response); err != nil {
			log.Printf("❌ [TELEGRAM] Failed to send response: %v", err)
		}
		log.Printf("✅ [TELEGRAM] Sent response to chat %d (%d chars)", chatID, len(result.Response))
	}
}

// processTelegramPhoto handles incoming photos
func (h *ChannelHandler) processTelegramPhoto(ctx context.Context, channel *models.Channel, session *models.ChannelSession, msg *models.TelegramMessage, botToken string, chatID int64, caption string) {
	// Start typing indicator
	typingCtx, cancelTyping := context.WithCancel(ctx)
	go h.channelService.SendContinuousTypingAction(typingCtx, botToken, chatID)
	defer cancelTyping()

	// Get the largest photo (last in array)
	photo := msg.Photo[len(msg.Photo)-1]

	// Download the photo
	imageData, _, err := h.channelService.DownloadTelegramFile(ctx, botToken, photo.FileID)
	if err != nil {
		log.Printf("❌ [TELEGRAM] Failed to download photo: %v", err)
		h.sendTelegramError(ctx, botToken, chatID, "Sorry, I couldn't download the image.")
		return
	}

	log.Printf("📸 [TELEGRAM] Downloaded photo: %d bytes", len(imageData))

	// Register image in filecache and image registry for editing support
	imageHandle := h.registerTelegramImage(ctx, channel.UserID, session.ConversationID, imageData, "telegram_photo.jpg")
	if imageHandle != "" {
		log.Printf("📸 [TELEGRAM] Registered image as %s for editing", imageHandle)
	}

	// Determine the prompt
	prompt := caption
	if prompt == "" {
		prompt = "Please describe this image in detail."
	}

	// Check if user wants to EDIT the image (use tools) vs ANALYZE it (use vision)
	if h.isImageEditRequest(prompt) && imageHandle != "" {
		log.Printf("🎨 [TELEGRAM] Detected image edit request, routing to tool-based flow")
		h.processImageEditRequest(ctx, channel, session, botToken, chatID, imageHandle, prompt, cancelTyping)
		return
	}

	// Default: Use vision model to analyze/describe the image
	imageBase64 := base64.StdEncoding.EncodeToString(imageData)

	// Build message with image for vision model
	userMessage := map[string]interface{}{
		"role": "user",
		"content": []map[string]interface{}{
			{
				"type": "text",
				"text": prompt,
			},
			{
				"type": "image_url",
				"image_url": map[string]string{
					"url": "data:image/jpeg;base64," + imageBase64,
				},
			},
		},
	}

	// Add to session history (just the text part)
	h.channelService.AddMessageToSession(ctx, session.ID, "user", "[Image] "+prompt, channel.MaxHistoryMessages)

	// Build messages with system prompt
	messages := []map[string]interface{}{}
	if channel.DefaultSystemPrompt != "" {
		messages = append(messages, map[string]interface{}{
			"role":    "system",
			"content": channel.DefaultSystemPrompt,
		})
	}
	messages = append(messages, userMessage)

	// Get AI response (using vision-capable model)
	response, err := h.getAIResponseWithVision(ctx, channel, session, messages)

	cancelTyping()

	if err != nil {
		log.Printf("❌ [TELEGRAM] Vision AI error: %v", err)
		h.sendTelegramError(ctx, botToken, chatID, "Sorry, I couldn't analyze the image. Please try again.")
		return
	}

	// Add response to session
	h.channelService.AddMessageToSession(ctx, session.ID, "assistant", response, channel.MaxHistoryMessages)
	h.channelService.IncrementMessageCount(ctx, channel.ID)

	// Send response
	if err := h.channelService.SendTelegramMessageChunked(ctx, botToken, chatID, response); err != nil {
		log.Printf("❌ [TELEGRAM] Failed to send response: %v", err)
	}

	log.Printf("✅ [TELEGRAM] Sent vision response to chat %d (%d chars)", chatID, len(response))
}

// processTelegramVoice handles incoming voice messages with Whisper transcription
func (h *ChannelHandler) processTelegramVoice(ctx context.Context, channel *models.Channel, session *models.ChannelSession, msg *models.TelegramMessage, botToken string, chatID int64) {
	// Start typing indicator
	typingCtx, cancelTyping := context.WithCancel(ctx)
	go h.channelService.SendContinuousTypingAction(typingCtx, botToken, chatID)
	defer cancelTyping()

	log.Printf("🎤 [TELEGRAM] Processing voice message (duration: %ds)", msg.Voice.Duration)

	// Check if audio service is available
	audioService := audio.GetService()
	if audioService == nil {
		log.Printf("⚠️ [TELEGRAM] Audio service not available")
		h.channelService.SendTelegramMessage(ctx, botToken, chatID,
			"🎤 Voice transcription is not available at the moment. Please send a text message.")
		return
	}

	// Download the voice file
	voiceData, filename, err := h.channelService.DownloadTelegramFile(ctx, botToken, msg.Voice.FileID)
	if err != nil {
		log.Printf("❌ [TELEGRAM] Failed to download voice: %v", err)
		h.sendTelegramError(ctx, botToken, chatID, "Sorry, I couldn't download the voice message.")
		return
	}

	log.Printf("🎤 [TELEGRAM] Downloaded voice: %s (%d bytes)", filename, len(voiceData))

	// Save to temp file (Whisper needs a file path)
	tempDir := os.TempDir()
	ext := ".ogg" // Telegram voice messages are OGG/OPUS
	if strings.HasSuffix(filename, ".oga") {
		ext = ".oga"
	}
	tempFile := filepath.Join(tempDir, fmt.Sprintf("telegram_voice_%s%s", uuid.New().String(), ext))
	if err := os.WriteFile(tempFile, voiceData, 0600); err != nil {
		log.Printf("❌ [TELEGRAM] Failed to save temp voice file: %v", err)
		h.sendTelegramError(ctx, botToken, chatID, "Sorry, I couldn't process the voice message.")
		return
	}
	defer os.Remove(tempFile) // Clean up

	// Transcribe the audio
	req := &audio.TranscribeRequest{
		AudioPath: tempFile,
		Language:  "", // Auto-detect
	}

	transcription, err := audioService.Transcribe(req)
	if err != nil {
		log.Printf("❌ [TELEGRAM] Transcription failed: %v", err)
		h.sendTelegramError(ctx, botToken, chatID, "Sorry, I couldn't transcribe the voice message.")
		return
	}

	log.Printf("✅ [TELEGRAM] Transcribed voice: %d chars (language: %s)", len(transcription.Text), transcription.Language)

	// Add transcribed text to session history
	h.channelService.AddMessageToSession(ctx, session.ID, "user", "[Voice] "+transcription.Text, channel.MaxHistoryMessages)

	// Build conversation history for AI (treating transcribed text as user message)
	history, _ := h.channelService.GetSessionHistory(ctx, session.ID)
	messages := buildChatMessages(history, channel.DefaultSystemPrompt)

	// Get AI response (with tool support)
	result, err := h.getAIResponse(ctx, channel, session, messages)

	cancelTyping()

	if err != nil {
		log.Printf("❌ [TELEGRAM] AI error: %v", err)
		h.sendTelegramError(ctx, botToken, chatID, "Sorry, I encountered an error. Please try again.")
		return
	}

	// Add assistant response to session
	h.channelService.AddMessageToSession(ctx, session.ID, "assistant", result.Response, channel.MaxHistoryMessages)
	h.channelService.IncrementMessageCount(ctx, channel.ID)

	// Check if images were generated
	if len(result.GeneratedImages) > 0 {
		for i, imageData := range result.GeneratedImages {
			caption := ""
			if i == 0 {
				caption = result.Response
				if len(caption) > 1000 {
					caption = caption[:997] + "..."
				}
			}
			if err := h.channelService.SendTelegramPhoto(ctx, botToken, chatID, imageData, caption); err != nil {
				log.Printf("⚠️ [TELEGRAM] Failed to send generated image, falling back to text: %v", err)
				h.channelService.SendTelegramMessageChunked(ctx, botToken, chatID, result.Response)
				break
			}
		}
	} else {
		// Send text response
		if err := h.channelService.SendTelegramMessageChunked(ctx, botToken, chatID, result.Response); err != nil {
			log.Printf("❌ [TELEGRAM] Failed to send response: %v", err)
		}
	}

	log.Printf("✅ [TELEGRAM] Sent voice response to chat %d (%d chars)", chatID, len(result.Response))
}

// processTelegramDocument handles incoming documents (images, PDFs, DOCX, etc.)
func (h *ChannelHandler) processTelegramDocument(ctx context.Context, channel *models.Channel, session *models.ChannelSession, msg *models.TelegramMessage, botToken string, chatID int64, caption string) {
	typingCtx, cancelTyping := context.WithCancel(ctx)
	go h.channelService.SendContinuousTypingAction(typingCtx, botToken, chatID)
	defer cancelTyping()

	mimeType := msg.Document.MimeType
	filename := msg.Document.FileName

	// Route to appropriate handler based on MIME type
	switch {
	case strings.HasPrefix(mimeType, "image/"):
		h.processImageDocument(ctx, channel, session, msg, botToken, chatID, caption, cancelTyping)
	case mimeType == "application/pdf":
		h.processPDFDocument(ctx, channel, session, msg, botToken, chatID, caption, cancelTyping)
	case mimeType == "application/vnd.openxmlformats-officedocument.wordprocessingml.document" ||
		strings.HasSuffix(strings.ToLower(filename), ".docx"):
		h.processDOCXDocument(ctx, channel, session, msg, botToken, chatID, caption, cancelTyping)
	default:
		cancelTyping()
		h.channelService.SendTelegramMessage(ctx, botToken, chatID,
			fmt.Sprintf("📄 I received your file (%s) but I can only process:\n"+
				"• Images (JPG, PNG, GIF, WebP)\n"+
				"• PDF documents\n"+
				"• Word documents (DOCX)\n\n"+
				"Please send a supported file type.", mimeType))
		log.Printf("⚠️ [TELEGRAM] Unsupported document type: %s (%s)", filename, mimeType)
	}
}

// processImageDocument handles image files sent as documents
func (h *ChannelHandler) processImageDocument(ctx context.Context, channel *models.Channel, session *models.ChannelSession, msg *models.TelegramMessage, botToken string, chatID int64, caption string, cancelTyping context.CancelFunc) {
	// Download the document
	imageData, _, err := h.channelService.DownloadTelegramFile(ctx, botToken, msg.Document.FileID)
	if err != nil {
		log.Printf("❌ [TELEGRAM] Failed to download document: %v", err)
		h.sendTelegramError(ctx, botToken, chatID, "Sorry, I couldn't download the file.")
		return
	}

	log.Printf("📄 [TELEGRAM] Downloaded image document: %s (%d bytes)", msg.Document.FileName, len(imageData))

	// Register image in filecache and image registry for editing support
	imageHandle := h.registerTelegramImage(ctx, channel.UserID, session.ConversationID, imageData, msg.Document.FileName)
	if imageHandle != "" {
		log.Printf("📄 [TELEGRAM] Registered image document as %s for editing", imageHandle)
	}

	// Convert to base64 for vision API
	imageBase64 := base64.StdEncoding.EncodeToString(imageData)

	// Determine the prompt
	prompt := caption
	if prompt == "" {
		prompt = "Please describe this image in detail."
	}

	// Build message with image for vision model
	userMessage := map[string]interface{}{
		"role": "user",
		"content": []map[string]interface{}{
			{
				"type": "text",
				"text": prompt,
			},
			{
				"type": "image_url",
				"image_url": map[string]string{
					"url": fmt.Sprintf("data:%s;base64,%s", msg.Document.MimeType, imageBase64),
				},
			},
		},
	}

	// Add to session history (just the text part)
	h.channelService.AddMessageToSession(ctx, session.ID, "user", "[Image] "+prompt, channel.MaxHistoryMessages)

	// Build messages with system prompt
	messages := []map[string]interface{}{}
	if channel.DefaultSystemPrompt != "" {
		messages = append(messages, map[string]interface{}{
			"role":    "system",
			"content": channel.DefaultSystemPrompt,
		})
	}
	messages = append(messages, userMessage)

	// Get AI response
	response, err := h.getAIResponseWithVision(ctx, channel, session, messages)
	cancelTyping()

	if err != nil {
		log.Printf("❌ [TELEGRAM] Vision AI error: %v", err)
		h.sendTelegramError(ctx, botToken, chatID, "Sorry, I couldn't analyze the image. Please try again.")
		return
	}

	// Add response to session
	h.channelService.AddMessageToSession(ctx, session.ID, "assistant", response, channel.MaxHistoryMessages)
	h.channelService.IncrementMessageCount(ctx, channel.ID)

	// Send response
	if err := h.channelService.SendTelegramMessageChunked(ctx, botToken, chatID, response); err != nil {
		log.Printf("❌ [TELEGRAM] Failed to send response: %v", err)
	}

	log.Printf("✅ [TELEGRAM] Sent image document response to chat %d (%d chars)", chatID, len(response))
}

// processPDFDocument handles PDF documents with text extraction
func (h *ChannelHandler) processPDFDocument(ctx context.Context, channel *models.Channel, session *models.ChannelSession, msg *models.TelegramMessage, botToken string, chatID int64, caption string, cancelTyping context.CancelFunc) {
	// Download the PDF
	pdfData, _, err := h.channelService.DownloadTelegramFile(ctx, botToken, msg.Document.FileID)
	if err != nil {
		log.Printf("❌ [TELEGRAM] Failed to download PDF: %v", err)
		h.sendTelegramError(ctx, botToken, chatID, "Sorry, I couldn't download the PDF.")
		return
	}

	log.Printf("📄 [TELEGRAM] Downloaded PDF: %s (%d bytes)", msg.Document.FileName, len(pdfData))

	// Extract text from PDF
	metadata, err := utils.ExtractPDFText(pdfData)
	if err != nil {
		log.Printf("❌ [TELEGRAM] Failed to extract PDF text: %v", err)
		h.sendTelegramError(ctx, botToken, chatID, "Sorry, I couldn't read this PDF. It may be scanned or corrupted.")
		return
	}

	log.Printf("📄 [TELEGRAM] Extracted %d pages, %d words from PDF", metadata.PageCount, metadata.WordCount)

	// Build prompt with extracted text
	prompt := caption
	if prompt == "" {
		prompt = "Please summarize this document."
	}

	// Truncate extracted text if too long (keep first ~10k chars)
	extractedText := metadata.Text
	if len(extractedText) > 10000 {
		extractedText = extractedText[:10000] + "\n\n... [Document truncated - showing first 10,000 characters]"
	}

	userContent := fmt.Sprintf("I've uploaded a PDF document: **%s** (%d pages, %d words)\n\n**My request:** %s\n\n**Document content:**\n%s",
		msg.Document.FileName, metadata.PageCount, metadata.WordCount, prompt, extractedText)

	// Add to session history
	h.channelService.AddMessageToSession(ctx, session.ID, "user", fmt.Sprintf("[PDF: %s] %s", msg.Document.FileName, prompt), channel.MaxHistoryMessages)

	// Build messages for AI
	history, _ := h.channelService.GetSessionHistory(ctx, session.ID)
	messages := buildChatMessages(history, channel.DefaultSystemPrompt)

	// Replace last user message with full document content
	if len(messages) > 0 {
		messages[len(messages)-1] = map[string]interface{}{
			"role":    "user",
			"content": userContent,
		}
	}

	// Get AI response
	result, err := h.getAIResponse(ctx, channel, session, messages)
	cancelTyping()

	if err != nil {
		log.Printf("❌ [TELEGRAM] AI error: %v", err)
		h.sendTelegramError(ctx, botToken, chatID, "Sorry, I couldn't process the document. Please try again.")
		return
	}

	// Add response to session
	h.channelService.AddMessageToSession(ctx, session.ID, "assistant", result.Response, channel.MaxHistoryMessages)
	h.channelService.IncrementMessageCount(ctx, channel.ID)

	// Send response
	if err := h.channelService.SendTelegramMessageChunked(ctx, botToken, chatID, result.Response); err != nil {
		log.Printf("❌ [TELEGRAM] Failed to send response: %v", err)
	}

	log.Printf("✅ [TELEGRAM] Sent PDF response to chat %d (%d chars)", chatID, len(result.Response))
}

// processDOCXDocument handles Word documents with text extraction
func (h *ChannelHandler) processDOCXDocument(ctx context.Context, channel *models.Channel, session *models.ChannelSession, msg *models.TelegramMessage, botToken string, chatID int64, caption string, cancelTyping context.CancelFunc) {
	// Download the DOCX
	docxData, _, err := h.channelService.DownloadTelegramFile(ctx, botToken, msg.Document.FileID)
	if err != nil {
		log.Printf("❌ [TELEGRAM] Failed to download DOCX: %v", err)
		h.sendTelegramError(ctx, botToken, chatID, "Sorry, I couldn't download the document.")
		return
	}

	log.Printf("📄 [TELEGRAM] Downloaded DOCX: %s (%d bytes)", msg.Document.FileName, len(docxData))

	// Extract text from DOCX
	metadata, err := utils.ExtractDOCXText(docxData)
	if err != nil {
		log.Printf("❌ [TELEGRAM] Failed to extract DOCX text: %v", err)
		h.sendTelegramError(ctx, botToken, chatID, "Sorry, I couldn't read this document. It may be corrupted.")
		return
	}

	log.Printf("📄 [TELEGRAM] Extracted %d words from DOCX", metadata.WordCount)

	// Build prompt with extracted text
	prompt := caption
	if prompt == "" {
		prompt = "Please summarize this document."
	}

	// Truncate extracted text if too long
	extractedText := metadata.Text
	if len(extractedText) > 10000 {
		extractedText = extractedText[:10000] + "\n\n... [Document truncated - showing first 10,000 characters]"
	}

	userContent := fmt.Sprintf("I've uploaded a Word document: **%s** (%d words)\n\n**My request:** %s\n\n**Document content:**\n%s",
		msg.Document.FileName, metadata.WordCount, prompt, extractedText)

	// Add to session history
	h.channelService.AddMessageToSession(ctx, session.ID, "user", fmt.Sprintf("[DOCX: %s] %s", msg.Document.FileName, prompt), channel.MaxHistoryMessages)

	// Build messages for AI
	history, _ := h.channelService.GetSessionHistory(ctx, session.ID)
	messages := buildChatMessages(history, channel.DefaultSystemPrompt)

	// Replace last user message with full document content
	if len(messages) > 0 {
		messages[len(messages)-1] = map[string]interface{}{
			"role":    "user",
			"content": userContent,
		}
	}

	// Get AI response
	result, err := h.getAIResponse(ctx, channel, session, messages)
	cancelTyping()

	if err != nil {
		log.Printf("❌ [TELEGRAM] AI error: %v", err)
		h.sendTelegramError(ctx, botToken, chatID, "Sorry, I couldn't process the document. Please try again.")
		return
	}

	// Add response to session
	h.channelService.AddMessageToSession(ctx, session.ID, "assistant", result.Response, channel.MaxHistoryMessages)
	h.channelService.IncrementMessageCount(ctx, channel.ID)

	// Send response
	if err := h.channelService.SendTelegramMessageChunked(ctx, botToken, chatID, result.Response); err != nil {
		log.Printf("❌ [TELEGRAM] Failed to send response: %v", err)
	}

	log.Printf("✅ [TELEGRAM] Sent DOCX response to chat %d (%d chars)", chatID, len(result.Response))
}

// getAIResponseWithVision gets a response from vision-capable AI model
func (h *ChannelHandler) getAIResponseWithVision(ctx context.Context, channel *models.Channel, session *models.ChannelSession, messages []map[string]interface{}) (string, error) {
	if h.chatService == nil {
		return "", fmt.Errorf("chat service not available")
	}

	// Determine which model to use (session preference > channel default > fallback)
	modelID := session.ModelID
	if modelID == "" {
		modelID = channel.DefaultModelID
	}
	if modelID == "" {
		modelID = "gpt-4o" // Default fallback
	}

	// Check if the model supports vision
	if !h.chatService.ModelSupportsVision(modelID) {
		log.Printf("🖼️ [TELEGRAM VISION] Model '%s' doesn't support vision - finding fallback", modelID)

		// Find a vision-capable model
		if _, visionModel, found := h.chatService.FindVisionCapableModel(); found {
			log.Printf("🖼️ [TELEGRAM VISION] Using vision-capable model: %s", visionModel)
			modelID = visionModel
		} else {
			// No vision model found, try with gpt-4o as last resort
			log.Printf("⚠️  [TELEGRAM VISION] No vision model found, trying gpt-4o")
			modelID = "gpt-4o"
		}
	}

	log.Printf("🖼️ [TELEGRAM VISION] Processing image with model: %s", modelID)

	// For vision, we don't use tools
	response, err := h.chatService.ChatCompletionWithTools(ctx, channel.UserID, "", modelID, messages, nil, 1)
	if err != nil {
		return "", err
	}

	return response, nil
}

// handleTelegramCommand handles Telegram bot commands
func (h *ChannelHandler) handleTelegramCommand(ctx context.Context, channel *models.Channel, session *models.ChannelSession, botToken string, chatID int64, text string) {
	parts := strings.Fields(text)
	command := strings.ToLower(parts[0])

	switch command {
	case "/start":
		welcome := fmt.Sprintf("👋 Hello! I'm *%s*, your DobbyAI assistant.\n\n"+
			"Just send me a message and I'll respond!\n\n"+
			"*Commands:*\n"+
			"/new - Start a new conversation\n"+
			"/model - Model selection\n"+
			"/help - Show this help message",
			channel.BotName)
		h.channelService.SendTelegramMessage(ctx, botToken, chatID, welcome)

	case "/new":
		h.channelService.ClearSession(ctx, session.ID)
		h.channelService.SendTelegramMessage(ctx, botToken, chatID, "🔄 Started a new conversation! Your previous context has been cleared.")

	case "/model":
		h.handleModelCommand(ctx, channel, session, botToken, chatID, parts[1:])

	case "/help":
		help := "*Available Commands:*\n\n" +
			"/start - Welcome message\n" +
			"/new - Start a new conversation (clears history)\n" +
			"/model - Show current AI model\n" +
			"/model list - List available models\n" +
			"/model set <name> - Switch to a different model\n" +
			"/help - Show this help message\n\n" +
			"Just type any message to chat with me!"
		h.channelService.SendTelegramMessage(ctx, botToken, chatID, help)

	default:
		h.channelService.SendTelegramMessage(ctx, botToken, chatID, "❓ Unknown command. Type /help for available commands.")
	}
}

// handleModelCommand handles the /model command and its subcommands
func (h *ChannelHandler) handleModelCommand(ctx context.Context, channel *models.Channel, session *models.ChannelSession, botToken string, chatID int64, args []string) {
	// No arguments: show current model
	if len(args) == 0 {
		model := session.ModelID
		if model == "" {
			model = channel.DefaultModelID
		}
		if model == "" {
			model = "default"
		}
		h.channelService.SendTelegramMessage(ctx, botToken, chatID,
			fmt.Sprintf("🤖 *Current model:* `%s`\n\nUse `/model list` to see available models\nUse `/model set <name>` to switch models", model))
		return
	}

	subcommand := strings.ToLower(args[0])

	switch subcommand {
	case "list":
		// Get available models
		if h.chatService == nil {
			h.channelService.SendTelegramMessage(ctx, botToken, chatID, "❌ Model service not available")
			return
		}

		modelList, err := h.chatService.GetVisibleModels()
		if err != nil {
			log.Printf("❌ [TELEGRAM] Failed to get models: %v", err)
			h.channelService.SendTelegramMessage(ctx, botToken, chatID, "❌ Failed to retrieve model list")
			return
		}

		if len(modelList) == 0 {
			h.channelService.SendTelegramMessage(ctx, botToken, chatID, "📭 No models available")
			return
		}

		// Build model list message
		var sb strings.Builder
		sb.WriteString("📋 *Available Models:*\n\n")

		currentModel := session.ModelID
		if currentModel == "" {
			currentModel = channel.DefaultModelID
		}

		for _, m := range modelList {
			name := m["name"].(string)
			displayName := m["display_name"].(string)
			supportsVision := m["supports_vision"].(bool)

			// Mark current model
			marker := ""
			if name == currentModel {
				marker = " ✓"
			}

			// Add vision indicator
			visionIcon := ""
			if supportsVision {
				visionIcon = " 👁"
			}

			sb.WriteString(fmt.Sprintf("• `%s`%s%s\n  _%s_\n", name, visionIcon, marker, displayName))
		}

		sb.WriteString("\n_Use `/model set <name>` to switch_")
		h.channelService.SendTelegramMessage(ctx, botToken, chatID, sb.String())

	case "set":
		if len(args) < 2 {
			h.channelService.SendTelegramMessage(ctx, botToken, chatID, "❌ Please specify a model name.\n\nUsage: `/model set <model_name>`\nExample: `/model set gpt-4o`")
			return
		}

		modelName := args[1]

		// Validate model exists (optional - we'll try to use it anyway)
		if h.chatService != nil {
			modelList, err := h.chatService.GetVisibleModels()
			if err == nil {
				found := false
				for _, m := range modelList {
					if m["name"].(string) == modelName || m["id"].(string) == modelName {
						found = true
						break
					}
				}
				if !found {
					h.channelService.SendTelegramMessage(ctx, botToken, chatID,
						fmt.Sprintf("⚠️ Model `%s` not found in available models.\nUse `/model list` to see available options.", modelName))
					return
				}
			}
		}

		// Update session with new model
		if err := h.channelService.UpdateSessionModel(ctx, session.ID, modelName); err != nil {
			log.Printf("❌ [TELEGRAM] Failed to set model: %v", err)
			h.channelService.SendTelegramMessage(ctx, botToken, chatID, "❌ Failed to update model setting")
			return
		}

		h.channelService.SendTelegramMessage(ctx, botToken, chatID,
			fmt.Sprintf("✅ Model switched to `%s`\n\nYour conversations will now use this model.", modelName))
		log.Printf("📱 [TELEGRAM] User in chat %d switched to model: %s", chatID, modelName)

	default:
		h.channelService.SendTelegramMessage(ctx, botToken, chatID,
			"❓ Unknown model command.\n\nAvailable:\n• `/model` - Show current\n• `/model list` - List available\n• `/model set <name>` - Switch model")
	}
}

// getAIResponse gets a response from the AI with tool support
// Returns the result including any generated images
func (h *ChannelHandler) getAIResponse(ctx context.Context, channel *models.Channel, session *models.ChannelSession, messages []map[string]interface{}) (*services.ChannelToolResult, error) {
	if h.chatService == nil {
		return nil, fmt.Errorf("chat service not available")
	}

	// Use session model > channel default > fallback
	modelID := session.ModelID
	if modelID == "" {
		modelID = channel.DefaultModelID
	}
	if modelID == "" {
		modelID = "gpt-4o-mini" // Default model
	}

	// 🧠 Inject memory context into system prompt if memory selection is available
	messages = h.injectMemoryContext(ctx, channel, session, messages)

	// 🖼️ Inject image context for editing tools if images are registered
	messages = h.injectImageContext(session.ConversationID, messages)

	// Get available tools for the user (MCP-inclusive for Claw integration)
	var availableTools []map[string]interface{}
	if h.toolService != nil {
		credentialFilteredTools := h.toolService.GetAvailableToolsWithMCP(ctx, channel.UserID)
		log.Printf("🔧 [TELEGRAM] %d tools (MCP-inclusive) for user %s", len(credentialFilteredTools), channel.UserID)

		// 🤖 Use smart tool routing if tool predictor is available
		if h.toolPredictorService != nil && len(credentialFilteredTools) > 0 {
			userMessage := extractLastUserMessageFromMaps(messages)
			log.Printf("🤖 [TELEGRAM-TOOL-PREDICTOR] Starting tool prediction for: %.50s...", userMessage)

			predictedTools, err := h.toolPredictorService.PredictTools(
				ctx,
				channel.UserID,
				session.ConversationID,
				userMessage,
				credentialFilteredTools,
				messages, // Pass conversation for context
			)

			if err != nil {
				log.Printf("⚠️  [TELEGRAM-TOOL-PREDICTOR] Prediction failed: %v, using all tools", err)
				availableTools = credentialFilteredTools
			} else {
				log.Printf("✅ [TELEGRAM-TOOL-PREDICTOR] Selected %d/%d tools", len(predictedTools), len(credentialFilteredTools))
				availableTools = predictedTools
			}
		} else {
			availableTools = credentialFilteredTools
		}
	}

	// Inject Claw-aware context into system prompt when MCP tools are present
	messages = injectClawContext(messages, availableTools)

	// Call the chat service with tool support (extended version for image capture)
	// Pass conversationID so tools like edit_image can look up registered images
	result, err := h.chatService.ChatCompletionWithToolsEx(ctx, channel.UserID, session.ConversationID, modelID, messages, availableTools, 10)
	if err != nil {
		return nil, err
	}

	// 🧠 Queue for memory extraction after successful response
	h.queueMemoryExtraction(ctx, channel, session, messages, result.Response)

	return result, nil
}

// getAIResponseForImageEdit gets a response specifically for image editing
// Uses only the edit_image tool and limited iterations to avoid unnecessary LLM calls
func (h *ChannelHandler) getAIResponseForImageEdit(ctx context.Context, channel *models.Channel, session *models.ChannelSession, messages []map[string]interface{}) (*services.ChannelToolResult, error) {
	if h.chatService == nil {
		return nil, fmt.Errorf("chat service not available")
	}

	// Use session model > channel default > fallback
	modelID := session.ModelID
	if modelID == "" {
		modelID = channel.DefaultModelID
	}
	if modelID == "" {
		modelID = "gpt-4o-mini"
	}

	// 🖼️ Inject image context for editing tools
	messages = h.injectImageContext(session.ConversationID, messages)

	// Only include the edit_image tool for this request
	var editImageTool []map[string]interface{}
	if h.toolService != nil {
		allTools := h.toolService.GetAvailableTools(ctx, channel.UserID)
		for _, tool := range allTools {
			if name, ok := tool["name"].(string); ok && name == "edit_image" {
				editImageTool = append(editImageTool, tool)
				break
			}
		}
	}

	if len(editImageTool) == 0 {
		return nil, fmt.Errorf("edit_image tool not available")
	}

	log.Printf("🎨 [TELEGRAM-IMAGE-EDIT] Using edit_image tool with extended timeout")

	// Call with only edit_image tool and max 3 iterations (tool call + response)
	result, err := h.chatService.ChatCompletionWithToolsEx(ctx, channel.UserID, session.ConversationID, modelID, messages, editImageTool, 3)
	if err != nil {
		return nil, err
	}

	return result, nil
}

// injectMemoryContext adds relevant memories to the system prompt
func (h *ChannelHandler) injectMemoryContext(ctx context.Context, channel *models.Channel, session *models.ChannelSession, messages []map[string]interface{}) []map[string]interface{} {
	if h.memorySelectionService == nil {
		return messages // No memory service available
	}

	// Check if user has memory enabled
	if h.userService != nil {
		user, err := h.userService.GetUserBySupabaseID(ctx, channel.UserID)
		if err != nil || user == nil || !user.Preferences.MemoryEnabled {
			return messages // Memory disabled or user not found
		}
	}

	// Select relevant memories based on conversation
	timeoutCtx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()

	maxMemories := 5
	selectedMemories, err := h.memorySelectionService.SelectRelevantMemories(
		timeoutCtx,
		channel.UserID,
		messages,
		maxMemories,
	)

	if err != nil {
		log.Printf("⚠️  [TELEGRAM-MEMORY] Failed to select memories: %v", err)
		return messages
	}

	if len(selectedMemories) == 0 {
		return messages // No relevant memories
	}

	// Build memory context string
	var builder strings.Builder
	builder.WriteString("\n\n## Relevant Context from Previous Conversations\n\n")
	builder.WriteString("The following information was extracted from your past interactions with this user:\n\n")

	for i, mem := range selectedMemories {
		builder.WriteString(fmt.Sprintf("%d. %s\n", i+1, mem.DecryptedContent))
	}

	builder.WriteString("\nUse this context to personalize responses and avoid asking for information the user has already provided.\n")

	log.Printf("🧠 [TELEGRAM-MEMORY] Injected %d memories for user %s", len(selectedMemories), channel.UserID)

	// Append memory context to system message
	memoryContext := builder.String()
	resultMessages := make([]map[string]interface{}, len(messages))
	copy(resultMessages, messages)

	// Find and update system message, or prepend one
	for i, msg := range resultMessages {
		if role, ok := msg["role"].(string); ok && role == "system" {
			if content, ok := msg["content"].(string); ok {
				resultMessages[i] = map[string]interface{}{
					"role":    "system",
					"content": content + memoryContext,
				}
				return resultMessages
			}
		}
	}

	// No system message found, prepend memory as system message
	memorySystemMsg := map[string]interface{}{
		"role":    "system",
		"content": "You are a helpful AI assistant." + memoryContext,
	}
	return append([]map[string]interface{}{memorySystemMsg}, resultMessages...)
}

// injectImageContext adds available images context to the system prompt for editing tools
func (h *ChannelHandler) injectImageContext(conversationID string, messages []map[string]interface{}) []map[string]interface{} {
	if conversationID == "" {
		return messages
	}

	// Get image registry context
	imageRegistry := services.GetImageRegistryService()
	if !imageRegistry.HasImages(conversationID) {
		return messages // No images to reference
	}

	imageContext := imageRegistry.BuildSystemContext(conversationID)
	if imageContext == "" {
		return messages
	}

	log.Printf("🖼️ [TELEGRAM-IMAGE] Injecting image context for conversation %s", conversationID)

	// Append image context to system message
	resultMessages := make([]map[string]interface{}, len(messages))
	copy(resultMessages, messages)

	// Find and update system message
	for i, msg := range resultMessages {
		if role, ok := msg["role"].(string); ok && role == "system" {
			if content, ok := msg["content"].(string); ok {
				resultMessages[i] = map[string]interface{}{
					"role":    "system",
					"content": content + "\n\n" + imageContext,
				}
				return resultMessages
			}
		}
	}

	// No system message found, prepend one with image context
	imageSystemMsg := map[string]interface{}{
		"role":    "system",
		"content": "You are a helpful AI assistant.\n\n" + imageContext,
	}
	return append([]map[string]interface{}{imageSystemMsg}, resultMessages...)
}

// queueMemoryExtraction queues the conversation for memory extraction
func (h *ChannelHandler) queueMemoryExtraction(ctx context.Context, channel *models.Channel, session *models.ChannelSession, messages []map[string]interface{}, response string) {
	if h.memoryExtractionService == nil {
		return // Memory extraction not available
	}

	// Check if user has memory enabled
	if h.userService != nil {
		user, err := h.userService.GetUserBySupabaseID(ctx, channel.UserID)
		if err != nil || user == nil || !user.Preferences.MemoryEnabled {
			return // Memory disabled or user not found
		}

		// Get user's extraction threshold (default 20)
		threshold := user.Preferences.MemoryExtractionThreshold
		if threshold <= 0 {
			threshold = 20
		}

		// Check if threshold reached
		messageCount := len(messages) + 1 // +1 for the response we're about to add
		if messageCount%threshold != 0 {
			return // Threshold not reached yet
		}
	}

	// Add the response to messages for extraction
	extractionMessages := make([]map[string]interface{}, len(messages))
	copy(extractionMessages, messages)
	extractionMessages = append(extractionMessages, map[string]interface{}{
		"role":    "assistant",
		"content": response,
	})

	// Use session conversation ID or create one from session ID
	conversationID := session.ConversationID
	if conversationID == "" {
		conversationID = session.ID.Hex()
	}

	// Queue extraction (non-blocking)
	go func() {
		timeoutCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()

		err := h.memoryExtractionService.EnqueueExtraction(
			timeoutCtx,
			channel.UserID,
			conversationID,
			extractionMessages,
		)
		if err != nil {
			log.Printf("⚠️  [TELEGRAM-MEMORY] Failed to queue extraction: %v", err)
		} else {
			log.Printf("🧠 [TELEGRAM-MEMORY] Queued extraction for conversation %s (%d messages)",
				conversationID, len(extractionMessages))
		}
	}()
}

// extractLastUserMessageFromMaps extracts the last user message from messages array
func extractLastUserMessageFromMaps(messages []map[string]interface{}) string {
	for i := len(messages) - 1; i >= 0; i-- {
		if role, ok := messages[i]["role"].(string); ok && role == "user" {
			if content, ok := messages[i]["content"].(string); ok {
				return content
			}
		}
	}
	return ""
}

// sendTelegramError sends an error message to Telegram
func (h *ChannelHandler) sendTelegramError(ctx context.Context, botToken string, chatID int64, message string) {
	h.channelService.SendTelegramMessage(ctx, botToken, chatID, "⚠️ "+message)
}

// ============================================================================
// Helper Functions
// ============================================================================

// buildChatMessages builds the messages array for the AI from session history
func buildChatMessages(history []models.ChannelMessage, systemPrompt string) []map[string]interface{} {
	messages := []map[string]interface{}{}

	// Telegram formatting guidelines (always appended to system prompt)
	telegramFormatting := `

## Telegram Formatting Rules (IMPORTANT)
You are responding via Telegram. Follow these formatting rules strictly:

**DO NOT USE:**
- Tables (no | column | format |) - Telegram cannot render tables
- HTML tags - Use Markdown only
- Nested lists deeper than 2 levels
- Horizontal rules (---)
- Reference-style links

**CODE BLOCKS:**
- Always specify language: ` + "```python" + ` not just ` + "```" + `
- Keep code blocks short (under 30 lines) - split if longer
- For inline code use single backticks: ` + "`code`" + `

**FORMATTING TO USE:**
- *italic* for emphasis
- **bold** for important terms
- ` + "`inline code`" + ` for commands, filenames, variables
- Numbered lists (1. 2. 3.) for sequences
- Bullet points (- or •) for unordered items
- > for quotes (single line only)

**STRUCTURE:**
- Keep paragraphs short (2-4 sentences max)
- Use line breaks between sections
- Lead with the answer, then explain
- Mobile-friendly: assume small screen width
- For data that would be a table, use formatted lists instead:
  • Item: value
  • Item: value`

	// Add system prompt if configured
	basePrompt := systemPrompt
	if basePrompt == "" {
		// Default system prompt for Telegram
		basePrompt = "You are a helpful AI assistant communicating through Telegram. Be friendly, concise, and helpful."
	}

	// Always append Telegram formatting guidelines
	messages = append(messages, map[string]interface{}{
		"role":    "system",
		"content": basePrompt + telegramFormatting,
	})

	// Add conversation history
	for _, msg := range history {
		messages = append(messages, map[string]interface{}{
			"role":    msg.Role,
			"content": msg.Content,
		})
	}

	return messages
}

// injectClawContext appends MCP tool awareness to the system prompt when MCP tools are available.
// This tells the LLM it can use local MCP tools (filesystem, git, etc.) via Dobby's Claw.
func injectClawContext(messages []map[string]interface{}, availableTools []map[string]interface{}) []map[string]interface{} {
	// Collect MCP tool names from available tools (they have metadata.source == "mcp_local")
	var mcpToolNames []string
	for _, toolDef := range availableTools {
		fn, ok := toolDef["function"].(map[string]interface{})
		if !ok {
			continue
		}
		name, _ := fn["name"].(string)
		if name == "" {
			continue
		}
		// Check metadata for MCP source
		if meta, ok := toolDef["metadata"].(map[string]interface{}); ok {
			if source, _ := meta["source"].(string); source == "mcp_local" {
				mcpToolNames = append(mcpToolNames, name)
			}
		}
	}

	if len(mcpToolNames) == 0 {
		return messages // No MCP tools, nothing to inject
	}

	clawContext := fmt.Sprintf(`

## Dobby's Claw Integration
You have access to %d MCP (Model Context Protocol) tools from the user's local environment.
These tools allow you to interact with the user's filesystem, git repos, and other local services.
When a request requires local actions (file operations, code analysis, etc.), use these tools proactively.
Available MCP tools: %s`, len(mcpToolNames), strings.Join(mcpToolNames, ", "))

	// Append to existing system message
	for i, msg := range messages {
		if role, ok := msg["role"].(string); ok && role == "system" {
			if content, ok := msg["content"].(string); ok {
				messages[i]["content"] = content + clawContext
				return messages
			}
		}
	}

	// No system message found — prepend one
	sysMsg := map[string]interface{}{
		"role":    "system",
		"content": "You are a helpful AI assistant." + clawContext,
	}
	return append([]map[string]interface{}{sysMsg}, messages...)
}

// toResponse converts a Channel to a ChannelResponse
func (h *ChannelHandler) toResponse(channel *models.Channel) *models.ChannelResponse {
	baseURL := getBaseURLFromEnv()
	return &models.ChannelResponse{
		ID:                  channel.ID.Hex(),
		Platform:            channel.Platform,
		Name:                channel.Name,
		Enabled:             channel.Enabled,
		WebhookURL:          fmt.Sprintf("%s/api/channels/%s/webhook/%s", baseURL, channel.Platform, channel.WebhookSecret),
		BotUsername:         channel.BotUsername,
		BotName:             channel.BotName,
		DefaultModelID:      channel.DefaultModelID,
		DefaultSystemPrompt: channel.DefaultSystemPrompt,
		MaxHistoryMessages:  channel.MaxHistoryMessages,
		MessageCount:        channel.MessageCount,
		LastUsedAt:          channel.LastUsedAt,
		CreatedAt:           channel.CreatedAt,
	}
}

func getBaseURLFromEnv() string {
	// Try environment variables
	if url := getEnvStr("API_BASE_URL"); url != "" {
		return url
	}
	if url := getEnvStr("WEBHOOK_BASE_URL"); url != "" {
		return url
	}
	return "http://localhost:3001"
}

func getEnvStr(key string) string {
	// This would need os.Getenv in real implementation
	return ""
}

func convertToValues(ptrs []*models.ChannelResponse) []models.ChannelResponse {
	result := make([]models.ChannelResponse, len(ptrs))
	for i, ptr := range ptrs {
		if ptr != nil {
			result[i] = *ptr
		}
	}
	return result
}

func truncateText(text string, maxLen int) string {
	if len(text) <= maxLen {
		return text
	}
	return text[:maxLen] + "..."
}

// Image URL regex patterns for detecting generated images
var (
	// Match common image generation service URLs
	imageURLRegex = regexp.MustCompile(`https?://[^\s<>"\]]+\.(?:png|jpg|jpeg|gif|webp)(?:\?[^\s<>"\]]*)?`)
	// Match secure file download URLs from DobbyAI
	secureFileRegex = regexp.MustCompile(`https?://[^\s<>"\]]+/api/secure-files/download/[a-zA-Z0-9-]+`)
	// Match DALL-E, Flux, and other image service URLs
	imageServiceRegex = regexp.MustCompile(`https?://(?:oaidalleapiprodscus\.blob\.core\.windows\.net|replicate\.delivery|cdn\.openai\.com|[^\s<>"\]]+\.r2\.cloudflarestorage\.com)[^\s<>"\]]*`)
)

// extractGeneratedImageURL finds an image URL in the AI response
// Returns the first image URL found, or empty string if none
func (h *ChannelHandler) extractGeneratedImageURL(response string) string {
	// Check for common image service URLs first (most reliable)
	if match := imageServiceRegex.FindString(response); match != "" {
		log.Printf("🖼️ [TELEGRAM] Found image service URL: %s", truncateText(match, 50))
		return match
	}

	// Check for secure file URLs
	if match := secureFileRegex.FindString(response); match != "" {
		log.Printf("🖼️ [TELEGRAM] Found secure file URL: %s", truncateText(match, 50))
		return match
	}

	// Check for direct image URLs
	if match := imageURLRegex.FindString(response); match != "" {
		// Avoid false positives (e.g., markdown image syntax explanations)
		if !strings.Contains(response, "```") || strings.Index(response, match) < strings.Index(response, "```") {
			log.Printf("🖼️ [TELEGRAM] Found image URL: %s", truncateText(match, 50))
			return match
		}
	}

	return ""
}

// stripImageURLFromResponse removes the image URL from text to use as caption
// Keeps the description but removes the raw URL
func (h *ChannelHandler) stripImageURLFromResponse(response string) string {
	// Remove URLs from the response for cleaner caption
	result := imageServiceRegex.ReplaceAllString(response, "")
	result = secureFileRegex.ReplaceAllString(result, "")
	result = imageURLRegex.ReplaceAllString(result, "")

	// Clean up any leftover markdown image syntax
	result = regexp.MustCompile(`!\[[^\]]*\]\(\s*\)`).ReplaceAllString(result, "")
	result = regexp.MustCompile(`\[\s*\]\(\s*\)`).ReplaceAllString(result, "")

	// Trim whitespace and limit caption length (Telegram limit: 1024 chars)
	result = strings.TrimSpace(result)
	if len(result) > 1000 {
		result = result[:997] + "..."
	}

	return result
}

// ============================================================================
// Rate Limiting - Prevent message spam
// ============================================================================

// checkTelegramRateLimit checks if a user has exceeded the rate limit (10 msgs/min)
// Returns true if rate limit exceeded, false if OK to proceed
func (h *ChannelHandler) checkTelegramRateLimit(channelID, userID string) bool {
	telegramRateLimitMutex.Lock()
	defer telegramRateLimitMutex.Unlock()

	key := channelID + ":" + userID
	now := time.Now()
	windowStart := now.Add(-telegramRateLimitWindow)

	// Get existing timestamps and filter to only recent ones
	timestamps := telegramRateLimit[key]
	var recentTimestamps []time.Time
	for _, ts := range timestamps {
		if ts.After(windowStart) {
			recentTimestamps = append(recentTimestamps, ts)
		}
	}

	// Check if over limit
	if len(recentTimestamps) >= telegramRateLimitMaxMsgs {
		telegramRateLimit[key] = recentTimestamps // Update with cleaned list
		return true                               // Rate limit exceeded
	}

	// Add current timestamp and store
	recentTimestamps = append(recentTimestamps, now)
	telegramRateLimit[key] = recentTimestamps

	return false // OK to proceed
}

// ============================================================================
// Image Registration - Register Telegram images for editing
// ============================================================================

// registerTelegramImage saves an uploaded image to filecache and registers it in the image registry
// Returns the image handle (e.g., "img-1") or empty string on failure
func (h *ChannelHandler) registerTelegramImage(ctx context.Context, userID, conversationID string, imageData []byte, filename string) string {
	if conversationID == "" {
		log.Printf("⚠️ [TELEGRAM] Cannot register image: no conversation ID")
		return ""
	}

	// Generate unique file ID
	fileID := uuid.New().String()

	// Ensure uploads directory exists
	uploadsDir := "./uploads"
	if err := os.MkdirAll(uploadsDir, 0755); err != nil {
		log.Printf("⚠️ [TELEGRAM] Failed to create uploads directory: %v", err)
		return ""
	}

	// Write to disk
	filePath := filepath.Join(uploadsDir, fileID+".jpg")
	if err := os.WriteFile(filePath, imageData, 0644); err != nil {
		log.Printf("⚠️ [TELEGRAM] Failed to save image: %v", err)
		return ""
	}

	// Register in filecache
	fileCacheService := filecache.GetService()
	cachedFile := &filecache.CachedFile{
		FileID:         fileID,
		UserID:         userID,
		ConversationID: conversationID,
		FilePath:       filePath,
		Filename:       filename,
		MimeType:       "image/jpeg",
		Size:           int64(len(imageData)),
		UploadedAt:     time.Now(),
	}
	fileCacheService.Store(cachedFile)

	// Register in image registry
	imageRegistry := services.GetImageRegistryService()
	handle := imageRegistry.RegisterUploadedImage(conversationID, fileID, filename, 0, 0)

	log.Printf("📸 [TELEGRAM] Registered uploaded image: %s -> %s (file: %s)", handle, fileID, filePath)
	return handle
}

// isImageEditRequest checks if the user's caption indicates they want to edit/modify the image
// rather than analyze/describe it
func (h *ChannelHandler) isImageEditRequest(prompt string) bool {
	prompt = strings.ToLower(prompt)

	// Edit keywords - if any of these appear, it's likely an edit request
	editKeywords := []string{
		"edit", "modify", "change", "transform", "convert",
		"make it", "make this", "turn it", "turn this",
		"add", "remove", "delete", "put", "place",
		"replace", "swap", "switch",
		"enhance", "improve", "fix", "correct",
		"colorize", "recolor", "change color",
		"style", "stylize", "artistic",
		"upscale", "resize", "crop",
		"background", "remove background",
		"filter", "effect",
		"cartoon", "anime", "sketch", "paint",
	}

	for _, keyword := range editKeywords {
		if strings.Contains(prompt, keyword) {
			return true
		}
	}

	return false
}

// processImageEditRequest handles image edit requests by routing to the tool-based AI
func (h *ChannelHandler) processImageEditRequest(ctx context.Context, channel *models.Channel, session *models.ChannelSession, botToken string, chatID int64, imageHandle, prompt string, cancelTyping context.CancelFunc) {
	// Create a longer timeout context for image editing (can take 5+ minutes)
	editCtx, editCancel := context.WithTimeout(context.Background(), 10*time.Minute)
	defer editCancel()

	// Build a message that tells the AI to edit the image
	editPrompt := fmt.Sprintf("The user uploaded an image (registered as %s) and wants you to edit it. Their request: %s\n\nUse the edit_image tool to modify the image according to their request.", imageHandle, prompt)

	// Add to session history
	h.channelService.AddMessageToSession(ctx, session.ID, "user", fmt.Sprintf("[Image: %s] %s", imageHandle, prompt), channel.MaxHistoryMessages)

	// Build conversation history for AI with tools
	history, _ := h.channelService.GetSessionHistory(editCtx, session.ID)
	messages := buildChatMessages(history, channel.DefaultSystemPrompt)

	// Replace the last message with our edit prompt
	if len(messages) > 0 {
		messages[len(messages)-1] = map[string]interface{}{
			"role":    "user",
			"content": editPrompt,
		}
	}

	// Get AI response WITH TOOLS (this allows edit_image to be called)
	// Use editCtx which has a longer timeout for image operations
	result, err := h.getAIResponseForImageEdit(editCtx, channel, session, messages)
	cancelTyping()

	if err != nil {
		log.Printf("❌ [TELEGRAM] Image edit AI error: %v", err)
		h.sendTelegramError(ctx, botToken, chatID, "Sorry, I couldn't process the image edit request. Please try again.")
		return
	}

	// Add response to session
	h.channelService.AddMessageToSession(editCtx, session.ID, "assistant", result.Response, channel.MaxHistoryMessages)
	h.channelService.IncrementMessageCount(editCtx, channel.ID)

	// Send any generated images first
	if len(result.GeneratedImages) > 0 {
		for _, imgData := range result.GeneratedImages {
			if err := h.channelService.SendTelegramPhoto(ctx, botToken, chatID, imgData, ""); err != nil {
				log.Printf("⚠️ [TELEGRAM] Failed to send edited image: %v", err)
			} else {
				log.Printf("🖼️ [TELEGRAM] Sent edited image to chat %d (%d bytes)", chatID, len(imgData))
			}
		}
	}

	// Send text response (may be empty if just the image was the response)
	if result.Response != "" {
		// Strip image URLs from response since we're sending the image separately
		cleanResponse := h.stripImageURLFromResponse(result.Response)
		if cleanResponse != "" {
			if err := h.channelService.SendTelegramMessageChunked(ctx, botToken, chatID, cleanResponse); err != nil {
				log.Printf("❌ [TELEGRAM] Failed to send response: %v", err)
			}
		}
	}

	log.Printf("✅ [TELEGRAM] Processed image edit request for chat %d", chatID)
}

// ============================================================================
// Pending Media Management - Wait for text after image/document upload
// ============================================================================

// storePendingMedia stores media waiting for user's text message
func (h *ChannelHandler) storePendingMedia(chatID int64, media *PendingMedia) {
	pendingMediaMutex.Lock()
	defer pendingMediaMutex.Unlock()
	pendingMediaCache[chatID] = media
}

// getPendingMedia retrieves pending media for a chat (returns nil if expired)
func (h *ChannelHandler) getPendingMedia(chatID int64) *PendingMedia {
	pendingMediaMutex.RLock()
	defer pendingMediaMutex.RUnlock()

	media, exists := pendingMediaCache[chatID]
	if !exists {
		return nil
	}

	// Check if expired
	if time.Now().After(media.ExpiresAt) {
		return nil
	}

	return media
}

// clearPendingMedia removes pending media for a chat
func (h *ChannelHandler) clearPendingMedia(chatID int64) {
	pendingMediaMutex.Lock()
	defer pendingMediaMutex.Unlock()
	delete(pendingMediaCache, chatID)
}

// processPendingMediaWithText processes stored media with the user's follow-up text
func (h *ChannelHandler) processPendingMediaWithText(ctx context.Context, channel *models.Channel, session *models.ChannelSession, pending *PendingMedia, msg *models.TelegramMessage, botToken string, chatID int64, text string) {
	// Start typing indicator
	typingCtx, cancelTyping := context.WithCancel(ctx)
	go h.channelService.SendContinuousTypingAction(typingCtx, botToken, chatID)
	defer cancelTyping()

	switch pending.Type {
	case PendingMediaPhoto:
		// Download and process the photo with the text
		imageData, _, err := h.channelService.DownloadTelegramFile(ctx, botToken, pending.FileID)
		if err != nil {
			log.Printf("❌ [TELEGRAM] Failed to download pending photo: %v", err)
			h.sendTelegramError(ctx, botToken, chatID, "Sorry, I couldn't download the image. Please try sending it again.")
			return
		}

		log.Printf("📸 [TELEGRAM] Processing pending photo with text: %s", truncateText(text, 30))

		// Convert to base64
		imageBase64 := base64.StdEncoding.EncodeToString(imageData)

		// Build vision message
		userMessage := map[string]interface{}{
			"role": "user",
			"content": []map[string]interface{}{
				{"type": "text", "text": text},
				{"type": "image_url", "image_url": map[string]string{
					"url": "data:image/jpeg;base64," + imageBase64,
				}},
			},
		}

		// Add to session
		h.channelService.AddMessageToSession(ctx, session.ID, "user", "[Image] "+text, channel.MaxHistoryMessages)

		// Build messages
		messages := []map[string]interface{}{}
		if channel.DefaultSystemPrompt != "" {
			messages = append(messages, map[string]interface{}{"role": "system", "content": channel.DefaultSystemPrompt})
		}
		messages = append(messages, userMessage)

		// Get AI response
		response, err := h.getAIResponseWithVision(ctx, channel, session, messages)
		cancelTyping()

		if err != nil {
			log.Printf("❌ [TELEGRAM] Vision AI error: %v", err)
			h.sendTelegramError(ctx, botToken, chatID, "Sorry, I couldn't analyze the image. Please try again.")
			return
		}

		// Save and send response
		h.channelService.AddMessageToSession(ctx, session.ID, "assistant", response, channel.MaxHistoryMessages)
		h.channelService.IncrementMessageCount(ctx, channel.ID)
		h.channelService.SendTelegramMessageChunked(ctx, botToken, chatID, response)
		log.Printf("✅ [TELEGRAM] Sent pending photo response to chat %d", chatID)

	case PendingMediaDocument:
		// Route based on MIME type
		switch {
		case strings.HasPrefix(pending.MimeType, "image/"):
			// Process as image document
			imageData, _, err := h.channelService.DownloadTelegramFile(ctx, botToken, pending.FileID)
			if err != nil {
				log.Printf("❌ [TELEGRAM] Failed to download pending image doc: %v", err)
				h.sendTelegramError(ctx, botToken, chatID, "Sorry, I couldn't download the image. Please try sending it again.")
				return
			}

			imageBase64 := base64.StdEncoding.EncodeToString(imageData)
			userMessage := map[string]interface{}{
				"role": "user",
				"content": []map[string]interface{}{
					{"type": "text", "text": text},
					{"type": "image_url", "image_url": map[string]string{
						"url": "data:image/jpeg;base64," + imageBase64,
					}},
				},
			}

			h.channelService.AddMessageToSession(ctx, session.ID, "user", "[Image] "+text, channel.MaxHistoryMessages)

			messages := []map[string]interface{}{}
			if channel.DefaultSystemPrompt != "" {
				messages = append(messages, map[string]interface{}{"role": "system", "content": channel.DefaultSystemPrompt})
			}
			messages = append(messages, userMessage)

			response, err := h.getAIResponseWithVision(ctx, channel, session, messages)
			cancelTyping()

			if err != nil {
				log.Printf("❌ [TELEGRAM] Vision AI error: %v", err)
				h.sendTelegramError(ctx, botToken, chatID, "Sorry, I couldn't analyze the image. Please try again.")
				return
			}

			h.channelService.AddMessageToSession(ctx, session.ID, "assistant", response, channel.MaxHistoryMessages)
			h.channelService.IncrementMessageCount(ctx, channel.ID)
			h.channelService.SendTelegramMessageChunked(ctx, botToken, chatID, response)

		case pending.MimeType == "application/pdf":
			// Download and extract PDF text
			pdfData, _, err := h.channelService.DownloadTelegramFile(ctx, botToken, pending.FileID)
			if err != nil {
				log.Printf("❌ [TELEGRAM] Failed to download pending PDF: %v", err)
				h.sendTelegramError(ctx, botToken, chatID, "Sorry, I couldn't download the PDF. Please try again.")
				return
			}

			pdfMeta, err := utils.ExtractPDFText(pdfData)
			if err != nil {
				log.Printf("❌ [TELEGRAM] Failed to extract PDF text: %v", err)
				h.sendTelegramError(ctx, botToken, chatID, "Sorry, I couldn't read the PDF content.")
				return
			}

			docText := pdfMeta.Text
			if len(docText) > 10000 {
				docText = docText[:10000] + "\n\n[Document truncated...]"
			}

			prompt := fmt.Sprintf("The user uploaded a PDF document (%s, %d pages, ~%d words).\n\n"+
				"Document content:\n%s\n\nUser's question: %s",
				pending.FileName, pdfMeta.PageCount, pdfMeta.WordCount, docText, text)

			h.channelService.AddMessageToSession(ctx, session.ID, "user", "[PDF: "+pending.FileName+"] "+text, channel.MaxHistoryMessages)

			history, _ := h.channelService.GetSessionHistory(ctx, session.ID)
			messages := buildChatMessages(history[:len(history)-1], channel.DefaultSystemPrompt)
			messages = append(messages, map[string]interface{}{"role": "user", "content": prompt})

			result, err := h.getAIResponse(ctx, channel, session, messages)
			cancelTyping()

			if err != nil {
				log.Printf("❌ [TELEGRAM] AI error: %v", err)
				h.sendTelegramError(ctx, botToken, chatID, "Sorry, I encountered an error. Please try again.")
				return
			}

			h.channelService.AddMessageToSession(ctx, session.ID, "assistant", result.Response, channel.MaxHistoryMessages)
			h.channelService.IncrementMessageCount(ctx, channel.ID)
			h.channelService.SendTelegramMessageChunked(ctx, botToken, chatID, result.Response)

		default:
			// DOCX or other
			docData, _, err := h.channelService.DownloadTelegramFile(ctx, botToken, pending.FileID)
			if err != nil {
				log.Printf("❌ [TELEGRAM] Failed to download pending doc: %v", err)
				h.sendTelegramError(ctx, botToken, chatID, "Sorry, I couldn't download the document. Please try again.")
				return
			}

			docMeta, err := utils.ExtractDOCXText(docData)
			if err != nil {
				log.Printf("❌ [TELEGRAM] Failed to extract DOCX text: %v", err)
				h.sendTelegramError(ctx, botToken, chatID, "Sorry, I couldn't read the document content.")
				return
			}

			docText := docMeta.Text
			if len(docText) > 10000 {
				docText = docText[:10000] + "\n\n[Document truncated...]"
			}

			prompt := fmt.Sprintf("The user uploaded a Word document (%s, %d pages, ~%d words).\n\n"+
				"Document content:\n%s\n\nUser's question: %s",
				pending.FileName, docMeta.PageCount, docMeta.WordCount, docText, text)

			h.channelService.AddMessageToSession(ctx, session.ID, "user", "[DOCX: "+pending.FileName+"] "+text, channel.MaxHistoryMessages)

			history, _ := h.channelService.GetSessionHistory(ctx, session.ID)
			messages := buildChatMessages(history[:len(history)-1], channel.DefaultSystemPrompt)
			messages = append(messages, map[string]interface{}{"role": "user", "content": prompt})

			result, err := h.getAIResponse(ctx, channel, session, messages)
			cancelTyping()

			if err != nil {
				log.Printf("❌ [TELEGRAM] AI error: %v", err)
				h.sendTelegramError(ctx, botToken, chatID, "Sorry, I encountered an error. Please try again.")
				return
			}

			h.channelService.AddMessageToSession(ctx, session.ID, "assistant", result.Response, channel.MaxHistoryMessages)
			h.channelService.IncrementMessageCount(ctx, channel.ID)
			h.channelService.SendTelegramMessageChunked(ctx, botToken, chatID, result.Response)
		}
	}
}

// cleanupExpiredPendingMedia removes expired entries from the cache (call periodically)
func (h *ChannelHandler) cleanupExpiredPendingMedia() {
	pendingMediaMutex.Lock()
	defer pendingMediaMutex.Unlock()

	now := time.Now()
	for chatID, media := range pendingMediaCache {
		if now.After(media.ExpiresAt) {
			delete(pendingMediaCache, chatID)
			log.Printf("🧹 [TELEGRAM] Cleaned up expired pending media for chat %d", chatID)
		}
	}
}
