package handlers

import (
	"claraverse/internal/filecache"
	"claraverse/internal/models"
	"claraverse/internal/services"
	"context"
	"log"
	"strings"
	"time"

	"github.com/gofiber/fiber/v2"
)

// UserHandler handles user data and GDPR compliance endpoints
type UserHandler struct {
	chatService            *services.ChatService
	userService            *services.UserService
	agentService           *services.AgentService
	executionService       *services.ExecutionService
	apiKeyService          *services.APIKeyService
	credentialService      *services.CredentialService
	chatSyncService        *services.ChatSyncService
	schedulerService       *services.SchedulerService
	builderConvService     *services.BuilderConversationService
}

// NewUserHandler creates a new user handler
func NewUserHandler(chatService *services.ChatService, userService *services.UserService) *UserHandler {
	return &UserHandler{
		chatService: chatService,
		userService: userService,
	}
}

// SetGDPRServices sets optional services needed for complete GDPR deletion
func (h *UserHandler) SetGDPRServices(
	agentService *services.AgentService,
	executionService *services.ExecutionService,
	apiKeyService *services.APIKeyService,
	credentialService *services.CredentialService,
	chatSyncService *services.ChatSyncService,
	schedulerService *services.SchedulerService,
	builderConvService *services.BuilderConversationService,
) {
	h.agentService = agentService
	h.executionService = executionService
	h.apiKeyService = apiKeyService
	h.credentialService = credentialService
	h.chatSyncService = chatSyncService
	h.schedulerService = schedulerService
	h.builderConvService = builderConvService
}

// GetPreferences returns user preferences
// GET /api/user/preferences
func (h *UserHandler) GetPreferences(c *fiber.Ctx) error {
	userID, ok := c.Locals("user_id").(string)
	if !ok || userID == "" {
		return c.Status(fiber.StatusUnauthorized).JSON(fiber.Map{
			"error": "Authentication required",
		})
	}

	if h.userService == nil {
		// Fallback if MongoDB not configured
		return c.JSON(models.UserPreferences{
			StoreBuilderChatHistory: true,
			DefaultModelID:          "",
		})
	}

	prefs, err := h.userService.GetPreferences(c.Context(), userID)
	if err != nil {
		log.Printf("⚠️ Failed to get preferences for user %s: %v", userID, err)
		// Return defaults on error
		return c.JSON(models.UserPreferences{
			StoreBuilderChatHistory: true,
			DefaultModelID:          "",
		})
	}

	return c.JSON(prefs)
}

// UpdatePreferences updates user preferences
// PUT /api/user/preferences
func (h *UserHandler) UpdatePreferences(c *fiber.Ctx) error {
	userID, ok := c.Locals("user_id").(string)
	if !ok || userID == "" {
		return c.Status(fiber.StatusUnauthorized).JSON(fiber.Map{
			"error": "Authentication required",
		})
	}

	if h.userService == nil {
		return c.Status(fiber.StatusServiceUnavailable).JSON(fiber.Map{
			"error": "User service not available",
		})
	}

	var req models.UpdateUserPreferencesRequest
	if err := c.BodyParser(&req); err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{
			"error": "Invalid request body",
		})
	}

	prefs, err := h.userService.UpdatePreferences(c.Context(), userID, &req)
	if err != nil {
		log.Printf("❌ Failed to update preferences for user %s: %v", userID, err)
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{
			"error": "Failed to update preferences",
		})
	}

	log.Printf("✅ Updated preferences for user %s", userID)
	return c.JSON(prefs)
}

// MarkWelcomePopupSeen marks the welcome popup as seen
// POST /api/user/welcome-popup-seen
func (h *UserHandler) MarkWelcomePopupSeen(c *fiber.Ctx) error {
	userID, ok := c.Locals("user_id").(string)
	if !ok || userID == "" {
		return c.Status(fiber.StatusUnauthorized).JSON(fiber.Map{
			"error": "Authentication required",
		})
	}

	if h.userService == nil {
		return c.Status(fiber.StatusServiceUnavailable).JSON(fiber.Map{
			"error": "User service not available",
		})
	}

	err := h.userService.MarkWelcomePopupSeen(c.Context(), userID)
	if err != nil {
		log.Printf("❌ Failed to mark welcome popup seen for user %s: %v", userID, err)
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{
			"error": "Failed to update popup status",
		})
	}

	log.Printf("✅ Welcome popup marked as seen for user %s", userID)
	return c.JSON(fiber.Map{
		"success": true,
		"message": "Welcome popup marked as seen",
	})
}

// ExportData exports all user data (GDPR Article 15 & 20 - Right to Access and Portability)
// GET /api/user/data
func (h *UserHandler) ExportData(c *fiber.Ctx) error {
	userID, ok := c.Locals("user_id").(string)
	if !ok || userID == "" {
		return c.Status(fiber.StatusUnauthorized).JSON(fiber.Map{
			"error": "Authentication required",
		})
	}

	userEmail, _ := c.Locals("user_email").(string)

	log.Printf("📦 [GDPR] Data export requested by user: %s", userID)

	// Get all conversations for this user
	conversations, err := h.chatService.GetAllConversationsByUser(userID)
	if err != nil {
		log.Printf("❌ Failed to export conversations: %v", err)
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{
			"error": "Failed to export data",
		})
	}

	// Get file metadata (uploaded files)
	fileCache := filecache.GetService()
	fileMetadata := fileCache.GetAllFilesByUser(userID)

	// Compile all user data
	exportData := fiber.Map{
		"user_id":         userID,
		"user_email":      userEmail,
		"export_date":     time.Now().Format(time.RFC3339),
		"conversations":   conversations,
		"uploaded_files":  fileMetadata,
		"data_categories": []string{
			"conversations",
			"messages",
			"uploaded_files",
			"user_profile",
		},
		"privacy_notice": "This export contains all personal data we have stored for your account.",
	}

	log.Printf("✅ [GDPR] Data exported for user %s: %d conversations, %d files",
		userID, len(conversations), len(fileMetadata))

	return c.JSON(exportData)
}

// DeleteAccountRequest is the request body for account deletion
type DeleteAccountRequest struct {
	Confirmation string `json:"confirmation"`
}

// DeleteAccount deletes all user data (GDPR Article 17 - Right to Erasure)
// DELETE /api/user/account
// Requires confirmation phrase: "delete my account"
func (h *UserHandler) DeleteAccount(c *fiber.Ctx) error {
	userID, ok := c.Locals("user_id").(string)
	if !ok || userID == "" {
		return c.Status(fiber.StatusUnauthorized).JSON(fiber.Map{
			"error": "Authentication required",
		})
	}

	// Parse and validate confirmation phrase
	var req DeleteAccountRequest
	if err := c.BodyParser(&req); err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{
			"error": "Invalid request body",
		})
	}

	// Validate confirmation phrase (case-insensitive)
	if strings.TrimSpace(strings.ToLower(req.Confirmation)) != "delete my account" {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{
			"error":    "Invalid confirmation phrase",
			"required": "delete my account",
		})
	}

	userEmail, _ := c.Locals("user_email").(string)
	ctx := context.Background()

	log.Printf("🗑️  [GDPR] Account deletion CONFIRMED by user: %s (%s)", userID, userEmail)

	// Track deletion results
	deletionResults := fiber.Map{}

	// 1. Delete schedules first (they reference agents)
	if h.schedulerService != nil {
		count, err := h.schedulerService.DeleteAllByUser(ctx, userID)
		if err != nil {
			log.Printf("⚠️  [GDPR] Failed to delete schedules: %v", err)
		} else {
			deletionResults["schedules"] = count
		}
	}

	// 2. Delete executions (they reference agents)
	if h.executionService != nil {
		count, err := h.executionService.DeleteAllByUser(ctx, userID)
		if err != nil {
			log.Printf("⚠️  [GDPR] Failed to delete executions: %v", err)
		} else {
			deletionResults["executions"] = count
		}
	}

	// 3. Delete agents, workflows, and workflow versions
	if h.agentService != nil {
		count, err := h.agentService.DeleteAllByUser(ctx, userID)
		if err != nil {
			log.Printf("⚠️  [GDPR] Failed to delete agents: %v", err)
		} else {
			deletionResults["agents"] = count
		}
	}

	// 4. Delete API keys
	if h.apiKeyService != nil {
		count, err := h.apiKeyService.DeleteAllByUser(ctx, userID)
		if err != nil {
			log.Printf("⚠️  [GDPR] Failed to delete API keys: %v", err)
		} else {
			deletionResults["api_keys"] = count
		}
	}

	// 5. Delete credentials
	if h.credentialService != nil {
		count, err := h.credentialService.DeleteAllByUser(ctx, userID)
		if err != nil {
			log.Printf("⚠️  [GDPR] Failed to delete credentials: %v", err)
		} else {
			deletionResults["credentials"] = count
		}
	}

	// 6. Delete cloud-synced chats
	if h.chatSyncService != nil {
		count, err := h.chatSyncService.DeleteAllUserChats(ctx, userID)
		if err != nil {
			log.Printf("⚠️  [GDPR] Failed to delete synced chats: %v", err)
		} else {
			deletionResults["synced_chats"] = count
		}
	}

	// 7. Delete builder conversations
	if h.builderConvService != nil {
		err := h.builderConvService.DeleteConversationsByUser(ctx, userID)
		if err != nil {
			log.Printf("⚠️  [GDPR] Failed to delete builder conversations: %v", err)
		} else {
			deletionResults["builder_conversations"] = "deleted"
		}
	}

	// 8. Delete SQL conversations
	if err := h.chatService.DeleteAllConversationsByUser(userID); err != nil {
		log.Printf("⚠️  [GDPR] Failed to delete conversations: %v", err)
	} else {
		deletionResults["conversations"] = "deleted"
	}

	// 9. Delete all uploaded files
	fileCache := filecache.GetService()
	deletedFiles, err := fileCache.DeleteAllFilesByUser(userID)
	if err != nil {
		log.Printf("⚠️  [GDPR] Failed to delete some files: %v", err)
	}
	deletionResults["files"] = deletedFiles

	// 10. Delete user record (last)
	if h.userService != nil {
		err := h.userService.DeleteUser(ctx, userID)
		if err != nil {
			log.Printf("⚠️  [GDPR] Failed to delete user record: %v", err)
		} else {
			deletionResults["user_record"] = "deleted"
		}
	}

	log.Printf("✅ [GDPR] Account deletion completed for user %s", userID)

	return c.JSON(fiber.Map{
		"message":            "Account and all associated data deleted successfully",
		"user_id":            userID,
		"deletion_timestamp": time.Now().Format(time.RFC3339),
		"deleted":            deletionResults,
		"retention_note":     "Audit logs may be retained for up to 90 days for security purposes.",
	})
}

// GetPrivacyPolicy returns privacy policy information (GDPR Article 13 - Transparency)
// GET /api/privacy-policy
func (h *UserHandler) GetPrivacyPolicy(c *fiber.Ctx) error {
	policy := fiber.Map{
		"service_name": "ClaraVerse",
		"last_updated": "2025-11-18",

		"data_collected": []string{
			"User ID (from authentication provider)",
			"Email address (from authentication provider)",
			"Chat messages and conversation history",
			"Uploaded files (images, PDFs, CSV, Excel, JSON, text files)",
			"Usage metadata (timestamps, model selections)",
		},

		"legal_basis": "Legitimate interest in providing the service (GDPR Article 6(1)(f))",

		"data_retention": fiber.Map{
			"conversations":  "30 minutes (automatic deletion)",
			"uploaded_files": "Linked to conversation lifetime (30 minutes) - includes images, PDFs, CSV, Excel, JSON, and all data files",
			"audit_logs":     "90 days (security and compliance purposes)",
		},

		"third_parties": []fiber.Map{
			{
				"name":    "Supabase",
				"purpose": "Authentication and user management",
				"data":    []string{"user_id", "email", "authentication tokens"},
			},
			{
				"name":    "AI Model Providers",
				"purpose": "Processing chat messages and generating responses",
				"data":    []string{"chat messages", "uploaded file content"},
				"note":    "Varies based on selected model provider (OpenAI, Anthropic, etc.)",
			},
		},

		"user_rights": []fiber.Map{
			{
				"right":       "Right to Access (Art. 15)",
				"description": "Download all your personal data",
				"endpoint":    "GET /api/user/data",
			},
			{
				"right":       "Right to Erasure (Art. 17)",
				"description": "Delete all your personal data",
				"endpoint":    "DELETE /api/user/account",
			},
			{
				"right":       "Right to Data Portability (Art. 20)",
				"description": "Export your data in machine-readable format (JSON)",
				"endpoint":    "GET /api/user/data",
			},
		},

		"security_measures": []string{
			"AES-256-GCM encryption for sensitive file content",
			"JWT-based authentication",
			"HTTPS encryption in transit (production)",
			"Automatic data expiration (30 minutes)",
			"Rate limiting and DDoS protection",
		},

		"contact": fiber.Map{
			"data_controller": "ClaraVerse Team",
			"email":           "privacy@claraverse.com",
			"note":            "For privacy inquiries, data requests, or to exercise your rights",
		},

		"cookie_policy": "This service uses minimal cookies for authentication purposes only.",

		"changes_to_policy": "We will notify users of significant changes via email or in-app notification.",
	}

	return c.JSON(policy)
}
