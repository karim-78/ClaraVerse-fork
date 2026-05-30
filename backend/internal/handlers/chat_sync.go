package handlers

import (
	"claraverse/internal/models"
	"claraverse/internal/services"
	"context"
	"log"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/gofiber/fiber/v2"
)

// killSandboxForConversation tells the e2b-service to immediately tear down
// the persistent sandbox associated with this conversation. Called when the
// user deletes a chat — avoids letting the sandbox sit around burning E2B
// sandbox-hours until the 15-min idle eviction fires. Fire-and-forget;
// failures are logged but never block the chat delete response.
func killSandboxForConversation(conversationID string) {
	if conversationID == "" {
		return
	}
	base := os.Getenv("E2B_SERVICE_URL")
	if base == "" {
		base = "http://e2b-service:8001"
	}
	endpoint := strings.TrimRight(base, "/") + "/sandboxes/" + url.PathEscape(conversationID)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, "DELETE", endpoint, nil)
	if err != nil {
		log.Printf("⚠️ [SANDBOX-CLEANUP] build request failed: %v", err)
		return
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		log.Printf("⚠️ [SANDBOX-CLEANUP] DELETE %s failed: %v", endpoint, err)
		return
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusOK {
		log.Printf("🧹 [SANDBOX-CLEANUP] killed sandbox for conv %s", conversationID)
	}
}

// ChatSyncHandler handles HTTP requests for chat sync operations
type ChatSyncHandler struct {
	service *services.ChatSyncService
}

// NewChatSyncHandler creates a new chat sync handler
func NewChatSyncHandler(service *services.ChatSyncService) *ChatSyncHandler {
	return &ChatSyncHandler{
		service: service,
	}
}

// CreateOrUpdate creates a new chat or updates an existing one
// POST /api/chats
func (h *ChatSyncHandler) CreateOrUpdate(c *fiber.Ctx) error {
	userID, ok := c.Locals("user_id").(string)
	if !ok || userID == "" {
		return c.Status(fiber.StatusUnauthorized).JSON(fiber.Map{
			"error": "Authentication required",
		})
	}

	var req models.CreateChatRequest
	if err := c.BodyParser(&req); err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{
			"error": "Invalid request body",
		})
	}

	if req.ID == "" {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{
			"error": "Chat ID is required",
		})
	}

	chat, err := h.service.CreateOrUpdateChat(c.Context(), userID, &req)
	if err != nil {
		log.Printf("❌ Failed to create/update chat: %v", err)

		// Check for version conflict (use strings.Contains to avoid panic on short errors)
		errMsg := err.Error()
		if strings.Contains(errMsg, "version conflict") {
			return c.Status(fiber.StatusConflict).JSON(fiber.Map{
				"error": "Version conflict - chat was modified by another device",
			})
		}

		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{
			"error": "Failed to save chat",
		})
	}

	log.Printf("✅ Chat %s saved for user %s (version: %d)", req.ID, userID, chat.Version)
	return c.Status(fiber.StatusOK).JSON(chat)
}

// Get retrieves a single chat with decrypted messages
// GET /api/chats/:id
func (h *ChatSyncHandler) Get(c *fiber.Ctx) error {
	userID, ok := c.Locals("user_id").(string)
	if !ok || userID == "" {
		return c.Status(fiber.StatusUnauthorized).JSON(fiber.Map{
			"error": "Authentication required",
		})
	}

	chatID := c.Params("id")
	if chatID == "" {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{
			"error": "Chat ID is required",
		})
	}

	chat, err := h.service.GetChat(c.Context(), userID, chatID)
	if err != nil {
		if err.Error() == "chat not found" {
			return c.Status(fiber.StatusNotFound).JSON(fiber.Map{
				"error": "Chat not found",
			})
		}
		log.Printf("❌ Failed to get chat: %v", err)
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{
			"error": "Failed to get chat",
		})
	}

	return c.JSON(chat)
}

// List returns a paginated list of chats
// GET /api/chats?page=1&page_size=20&starred=true
func (h *ChatSyncHandler) List(c *fiber.Ctx) error {
	userID, ok := c.Locals("user_id").(string)
	if !ok || userID == "" {
		return c.Status(fiber.StatusUnauthorized).JSON(fiber.Map{
			"error": "Authentication required",
		})
	}

	page, _ := strconv.Atoi(c.Query("page", "1"))
	pageSize, _ := strconv.Atoi(c.Query("page_size", "20"))
	starred := c.Query("starred") == "true"

	chats, err := h.service.ListChats(c.Context(), userID, page, pageSize, starred)
	if err != nil {
		log.Printf("❌ Failed to list chats: %v", err)
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{
			"error": "Failed to list chats",
		})
	}

	return c.JSON(chats)
}

// Search performs a server-side decrypt-and-scan full-text search over the
// user's chats. Encryption rules out an index — we trade O(n) decrypt-time
// CPU for keeping the privacy guarantee.
// GET /api/chats/search?q=foo&limit=25
func (h *ChatSyncHandler) Search(c *fiber.Ctx) error {
	userID, ok := c.Locals("user_id").(string)
	if !ok || userID == "" {
		return c.Status(fiber.StatusUnauthorized).JSON(fiber.Map{
			"error": "Authentication required",
		})
	}

	query := c.Query("q", "")
	limit, _ := strconv.Atoi(c.Query("limit", "25"))

	result, err := h.service.SearchChats(c.Context(), userID, query, limit)
	if err != nil {
		log.Printf("❌ Chat search failed for user %s: %v", userID, err)
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{
			"error": "Search failed",
		})
	}
	return c.JSON(result)
}

// Update performs a partial update on a chat
// PUT /api/chats/:id
func (h *ChatSyncHandler) Update(c *fiber.Ctx) error {
	userID, ok := c.Locals("user_id").(string)
	if !ok || userID == "" {
		return c.Status(fiber.StatusUnauthorized).JSON(fiber.Map{
			"error": "Authentication required",
		})
	}

	chatID := c.Params("id")
	if chatID == "" {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{
			"error": "Chat ID is required",
		})
	}

	var req models.UpdateChatRequest
	if err := c.BodyParser(&req); err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{
			"error": "Invalid request body",
		})
	}

	chat, err := h.service.UpdateChat(c.Context(), userID, chatID, &req)
	if err != nil {
		if err.Error() == "chat not found or version conflict" {
			return c.Status(fiber.StatusConflict).JSON(fiber.Map{
				"error": "Chat not found or version conflict",
			})
		}
		log.Printf("❌ Failed to update chat: %v", err)
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{
			"error": "Failed to update chat",
		})
	}

	log.Printf("✅ Chat %s updated for user %s", chatID, userID)
	return c.JSON(chat)
}

// Delete removes a chat
// DELETE /api/chats/:id
func (h *ChatSyncHandler) Delete(c *fiber.Ctx) error {
	userID, ok := c.Locals("user_id").(string)
	if !ok || userID == "" {
		return c.Status(fiber.StatusUnauthorized).JSON(fiber.Map{
			"error": "Authentication required",
		})
	}

	chatID := c.Params("id")
	if chatID == "" {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{
			"error": "Chat ID is required",
		})
	}

	err := h.service.DeleteChat(c.Context(), userID, chatID)
	if err == nil {
		// Best-effort: kill the conversation's persistent E2B sandbox
		// immediately rather than waiting for the 15-min idle eviction.
		// Direct cost saving — an abandoned sandbox burns E2B sandbox-hours
		// until the eviction ticker runs.
		go killSandboxForConversation(chatID)
	}
	if err != nil {
		if err.Error() == "chat not found" {
			return c.Status(fiber.StatusNotFound).JSON(fiber.Map{
				"error": "Chat not found",
			})
		}
		log.Printf("❌ Failed to delete chat: %v", err)
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{
			"error": "Failed to delete chat",
		})
	}

	log.Printf("✅ Chat %s deleted for user %s", chatID, userID)
	return c.JSON(fiber.Map{
		"success": true,
		"message": "Chat deleted",
	})
}

// BulkSync uploads multiple chats at once
// POST /api/chats/sync
func (h *ChatSyncHandler) BulkSync(c *fiber.Ctx) error {
	userID, ok := c.Locals("user_id").(string)
	if !ok || userID == "" {
		return c.Status(fiber.StatusUnauthorized).JSON(fiber.Map{
			"error": "Authentication required",
		})
	}

	var req models.BulkSyncRequest
	if err := c.BodyParser(&req); err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{
			"error": "Invalid request body",
		})
	}

	if len(req.Chats) == 0 {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{
			"error": "No chats provided",
		})
	}

	// Limit bulk sync to prevent abuse
	if len(req.Chats) > 100 {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{
			"error": "Maximum 100 chats per bulk sync",
		})
	}

	result, err := h.service.BulkSync(c.Context(), userID, &req)
	if err != nil {
		log.Printf("❌ Failed to bulk sync: %v", err)
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{
			"error": "Failed to bulk sync chats",
		})
	}

	log.Printf("✅ Bulk sync for user %s: %d synced, %d failed", userID, result.Synced, result.Failed)
	return c.JSON(result)
}

// SyncAll returns all chats for initial sync
// GET /api/chats/sync
func (h *ChatSyncHandler) SyncAll(c *fiber.Ctx) error {
	userID, ok := c.Locals("user_id").(string)
	if !ok || userID == "" {
		return c.Status(fiber.StatusUnauthorized).JSON(fiber.Map{
			"error": "Authentication required",
		})
	}

	result, err := h.service.GetAllChats(c.Context(), userID)
	if err != nil {
		log.Printf("❌ Failed to get all chats: %v", err)
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{
			"error": "Failed to get chats",
		})
	}

	log.Printf("✅ Sync all for user %s: %d chats", userID, result.TotalCount)
	return c.JSON(result)
}

// AddMessage adds a single message to a chat
// POST /api/chats/:id/messages
func (h *ChatSyncHandler) AddMessage(c *fiber.Ctx) error {
	userID, ok := c.Locals("user_id").(string)
	if !ok || userID == "" {
		return c.Status(fiber.StatusUnauthorized).JSON(fiber.Map{
			"error": "Authentication required",
		})
	}

	chatID := c.Params("id")
	if chatID == "" {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{
			"error": "Chat ID is required",
		})
	}

	var req models.ChatAddMessageRequest
	if err := c.BodyParser(&req); err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{
			"error": "Invalid request body",
		})
	}

	chat, err := h.service.AddMessage(c.Context(), userID, chatID, &req)
	if err != nil {
		if err.Error() == "chat not found or version conflict" || err.Error() == "version conflict during update" {
			return c.Status(fiber.StatusConflict).JSON(fiber.Map{
				"error": "Version conflict - please refresh and try again",
			})
		}
		log.Printf("❌ Failed to add message: %v", err)
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{
			"error": "Failed to add message",
		})
	}

	log.Printf("✅ Message added to chat %s for user %s", chatID, userID)
	return c.JSON(chat)
}

// DeleteAll deletes all chats for a user (GDPR compliance)
// DELETE /api/chats
func (h *ChatSyncHandler) DeleteAll(c *fiber.Ctx) error {
	userID, ok := c.Locals("user_id").(string)
	if !ok || userID == "" {
		return c.Status(fiber.StatusUnauthorized).JSON(fiber.Map{
			"error": "Authentication required",
		})
	}

	count, err := h.service.DeleteAllUserChats(c.Context(), userID)
	if err != nil {
		log.Printf("❌ Failed to delete all chats: %v", err)
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{
			"error": "Failed to delete chats",
		})
	}

	log.Printf("✅ Deleted %d chats for user %s", count, userID)
	return c.JSON(fiber.Map{
		"success": true,
		"deleted": count,
	})
}
