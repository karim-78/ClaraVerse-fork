package handlers

import (
	"bytes"
	"claraverse/internal/execution"
	"claraverse/internal/models"
	"claraverse/internal/services"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"strings"
	"time"

	"github.com/gofiber/fiber/v2"
)

// isPlaceholderDescription checks if a description is empty or a placeholder
func isPlaceholderDescription(desc string) bool {
	if desc == "" {
		return true
	}
	// Normalize for comparison
	lower := strings.ToLower(strings.TrimSpace(desc))
	// Common placeholder patterns
	placeholders := []string{
		"describe what this agent does",
		"description",
		"add a description",
		"enter description",
		"agent description",
		"no description",
		"...",
		"-",
	}
	for _, p := range placeholders {
		if lower == p || strings.HasPrefix(lower, p) {
			return true
		}
	}
	return false
}

// AgentHandler handles agent-related HTTP requests
type AgentHandler struct {
	agentService               *services.AgentService
	workflowGeneratorService   *services.WorkflowGeneratorService
	workflowGeneratorV2Service *services.WorkflowGeneratorV2Service
	builderConvService         *services.BuilderConversationService
	providerService            *services.ProviderService
	webhookService             *services.WebhookService
	schedulerService           *services.SchedulerService
	executorRegistry           *execution.ExecutorRegistry
}

// NewAgentHandler creates a new agent handler
func NewAgentHandler(agentService *services.AgentService, workflowGenerator *services.WorkflowGeneratorService) *AgentHandler {
	return &AgentHandler{
		agentService:             agentService,
		workflowGeneratorService: workflowGenerator,
	}
}

// SetWorkflowGeneratorV2Service sets the v2 workflow generator service
func (h *AgentHandler) SetWorkflowGeneratorV2Service(svc *services.WorkflowGeneratorV2Service) {
	h.workflowGeneratorV2Service = svc
}

// SetBuilderConversationService sets the builder conversation service (for sync endpoint)
func (h *AgentHandler) SetBuilderConversationService(svc *services.BuilderConversationService) {
	h.builderConvService = svc
}

// SetProviderService sets the provider service (for Ask mode)
func (h *AgentHandler) SetProviderService(svc *services.ProviderService) {
	h.providerService = svc
}

// SetWebhookService sets the webhook service for auto-registration on deploy
func (h *AgentHandler) SetWebhookService(svc *services.WebhookService) {
	h.webhookService = svc
}

// SetSchedulerService sets the scheduler service for auto-registration on deploy
func (h *AgentHandler) SetSchedulerService(svc *services.SchedulerService) {
	h.schedulerService = svc
}

// SetExecutorRegistry sets the executor registry for single-block test execution
func (h *AgentHandler) SetExecutorRegistry(reg *execution.ExecutorRegistry) {
	h.executorRegistry = reg
}

// Create creates a new agent
// POST /api/agents
func (h *AgentHandler) Create(c *fiber.Ctx) error {
	userID, ok := c.Locals("user_id").(string)
	if !ok || userID == "" {
		return c.Status(fiber.StatusUnauthorized).JSON(fiber.Map{
			"error": "Authentication required",
		})
	}

	var req models.CreateAgentRequest
	if err := c.BodyParser(&req); err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{
			"error": "Invalid request body",
		})
	}

	if req.Name == "" {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{
			"error": "Name is required",
		})
	}

	log.Printf("🤖 [AGENT] Creating agent '%s' for user %s", req.Name, userID)

	agent, err := h.agentService.CreateAgent(userID, req.Name, req.Description)
	if err != nil {
		log.Printf("❌ [AGENT] Failed to create agent: %v", err)
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{
			"error": "Failed to create agent",
		})
	}

	log.Printf("✅ [AGENT] Created agent %s", agent.ID)
	return c.Status(fiber.StatusCreated).JSON(agent)
}

// List returns all agents for the authenticated user with pagination
// GET /api/agents?limit=20&offset=0
func (h *AgentHandler) List(c *fiber.Ctx) error {
	userID, ok := c.Locals("user_id").(string)
	if !ok || userID == "" {
		return c.Status(fiber.StatusUnauthorized).JSON(fiber.Map{
			"error": "Authentication required",
		})
	}

	limit := c.QueryInt("limit", 20)
	offset := c.QueryInt("offset", 0)

	log.Printf("📋 [AGENT] Listing agents for user %s (limit: %d, offset: %d)", userID, limit, offset)

	response, err := h.agentService.ListAgentsPaginated(userID, limit, offset)
	if err != nil {
		log.Printf("❌ [AGENT] Failed to list agents: %v", err)
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{
			"error": "Failed to list agents",
		})
	}

	// Ensure agents array is not null
	if response.Agents == nil {
		response.Agents = []models.AgentListItem{}
	}

	return c.JSON(response)
}

// ListRecent returns the 10 most recent agents for the landing page
// GET /api/agents/recent
func (h *AgentHandler) ListRecent(c *fiber.Ctx) error {
	userID, ok := c.Locals("user_id").(string)
	if !ok || userID == "" {
		return c.Status(fiber.StatusUnauthorized).JSON(fiber.Map{
			"error": "Authentication required",
		})
	}

	log.Printf("📋 [AGENT] Getting recent agents for user %s", userID)

	response, err := h.agentService.GetRecentAgents(userID)
	if err != nil {
		log.Printf("❌ [AGENT] Failed to get recent agents: %v", err)
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{
			"error": "Failed to get recent agents",
		})
	}

	// Ensure agents array is not null
	if response.Agents == nil {
		response.Agents = []models.AgentListItem{}
	}

	return c.JSON(response)
}

// Get returns a single agent by ID
// GET /api/agents/:id
func (h *AgentHandler) Get(c *fiber.Ctx) error {
	userID, ok := c.Locals("user_id").(string)
	if !ok || userID == "" {
		return c.Status(fiber.StatusUnauthorized).JSON(fiber.Map{
			"error": "Authentication required",
		})
	}

	agentID := c.Params("id")
	if agentID == "" {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{
			"error": "Agent ID is required",
		})
	}

	log.Printf("🔍 [AGENT] Getting agent %s for user %s", agentID, userID)

	agent, err := h.agentService.GetAgent(agentID, userID)
	if err != nil {
		if err.Error() == "agent not found" {
			return c.Status(fiber.StatusNotFound).JSON(fiber.Map{
				"error": "Agent not found",
			})
		}
		log.Printf("❌ [AGENT] Failed to get agent: %v", err)
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{
			"error": "Failed to get agent",
		})
	}

	return c.JSON(agent)
}

// Update updates an agent's metadata
// PUT /api/agents/:id
func (h *AgentHandler) Update(c *fiber.Ctx) error {
	userID, ok := c.Locals("user_id").(string)
	if !ok || userID == "" {
		return c.Status(fiber.StatusUnauthorized).JSON(fiber.Map{
			"error": "Authentication required",
		})
	}

	agentID := c.Params("id")
	if agentID == "" {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{
			"error": "Agent ID is required",
		})
	}

	var req models.UpdateAgentRequest
	if err := c.BodyParser(&req); err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{
			"error": "Invalid request body",
		})
	}

	log.Printf("✏️ [AGENT] Updating agent %s for user %s", agentID, userID)

	// Check if we're deploying and need to auto-generate a description
	if req.Status == "deployed" {
		// Get current agent to check if description is empty or placeholder
		currentAgent, err := h.agentService.GetAgent(agentID, userID)
		if err == nil && currentAgent != nil {
			// Auto-generate description if empty or a placeholder
			if isPlaceholderDescription(currentAgent.Description) {
				log.Printf("🔍 [AGENT] Agent %s has no/placeholder description, generating one on deploy", agentID)
				workflow, err := h.agentService.GetWorkflow(agentID)
				if err == nil && workflow != nil {
					description, err := h.workflowGeneratorService.GenerateDescriptionFromWorkflow(workflow, currentAgent.Name)
					if err != nil {
						log.Printf("⚠️ [AGENT] Failed to generate description (non-fatal): %v", err)
					} else if description != "" {
						req.Description = description
						log.Printf("📝 [AGENT] Auto-generated description for agent %s: %s", agentID, description)
					}
				}
			}
		}
	}

	agent, err := h.agentService.UpdateAgent(agentID, userID, &req)
	if err != nil {
		if err.Error() == "agent not found" {
			return c.Status(fiber.StatusNotFound).JSON(fiber.Map{
				"error": "Agent not found",
			})
		}
		log.Printf("❌ [AGENT] Failed to update agent: %v", err)
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{
			"error": "Failed to update agent",
		})
	}

	// Auto-register/unregister triggers on deploy/undeploy
	if req.Status == "deployed" {
		h.registerTriggersOnDeploy(c.Context(), agentID, userID, agent.Workflow)
	} else if req.Status == "draft" || req.Status == "active" {
		h.unregisterTriggersOnUndeploy(c.Context(), agentID, userID)
	}

	log.Printf("✅ [AGENT] Updated agent %s", agentID)
	return c.JSON(agent)
}

// Delete deletes an agent
// DELETE /api/agents/:id
func (h *AgentHandler) Delete(c *fiber.Ctx) error {
	userID, ok := c.Locals("user_id").(string)
	if !ok || userID == "" {
		return c.Status(fiber.StatusUnauthorized).JSON(fiber.Map{
			"error": "Authentication required",
		})
	}

	agentID := c.Params("id")
	if agentID == "" {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{
			"error": "Agent ID is required",
		})
	}

	log.Printf("🗑️ [AGENT] Deleting agent %s for user %s", agentID, userID)

	err := h.agentService.DeleteAgent(agentID, userID)
	if err != nil {
		if err.Error() == "agent not found" {
			return c.Status(fiber.StatusNotFound).JSON(fiber.Map{
				"error": "Agent not found",
			})
		}
		log.Printf("❌ [AGENT] Failed to delete agent: %v", err)
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{
			"error": "Failed to delete agent",
		})
	}

	log.Printf("✅ [AGENT] Deleted agent %s", agentID)
	return c.Status(fiber.StatusNoContent).Send(nil)
}

// SaveWorkflow saves or updates the workflow for an agent
// PUT /api/agents/:id/workflow
func (h *AgentHandler) SaveWorkflow(c *fiber.Ctx) error {
	userID, ok := c.Locals("user_id").(string)
	if !ok || userID == "" {
		return c.Status(fiber.StatusUnauthorized).JSON(fiber.Map{
			"error": "Authentication required",
		})
	}

	agentID := c.Params("id")
	if agentID == "" {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{
			"error": "Agent ID is required",
		})
	}

	var req models.SaveWorkflowRequest
	if err := c.BodyParser(&req); err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{
			"error": "Invalid request body",
		})
	}

	log.Printf("💾 [AGENT] Saving workflow for agent %s (user: %s, blocks: %d)", agentID, userID, len(req.Blocks))

	workflow, err := h.agentService.SaveWorkflow(agentID, userID, &req)
	if err != nil {
		if err.Error() == "agent not found" {
			return c.Status(fiber.StatusNotFound).JSON(fiber.Map{
				"error": "Agent not found",
			})
		}
		log.Printf("❌ [AGENT] Failed to save workflow: %v", err)
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{
			"error": "Failed to save workflow",
		})
	}

	log.Printf("✅ [AGENT] Saved workflow for agent %s (version: %d)", agentID, workflow.Version)
	return c.JSON(workflow)
}

// GetWorkflow returns the workflow for an agent
// GET /api/agents/:id/workflow
func (h *AgentHandler) GetWorkflow(c *fiber.Ctx) error {
	userID, ok := c.Locals("user_id").(string)
	if !ok || userID == "" {
		return c.Status(fiber.StatusUnauthorized).JSON(fiber.Map{
			"error": "Authentication required",
		})
	}

	agentID := c.Params("id")
	if agentID == "" {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{
			"error": "Agent ID is required",
		})
	}

	// Verify agent belongs to user
	_, err := h.agentService.GetAgent(agentID, userID)
	if err != nil {
		if err.Error() == "agent not found" {
			return c.Status(fiber.StatusNotFound).JSON(fiber.Map{
				"error": "Agent not found",
			})
		}
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{
			"error": "Failed to verify agent ownership",
		})
	}

	workflow, err := h.agentService.GetWorkflow(agentID)
	if err != nil {
		if err.Error() == "workflow not found" {
			return c.Status(fiber.StatusNotFound).JSON(fiber.Map{
				"error": "Workflow not found",
			})
		}
		log.Printf("❌ [AGENT] Failed to get workflow: %v", err)
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{
			"error": "Failed to get workflow",
		})
	}

	return c.JSON(workflow)
}

// GenerateWorkflow generates or modifies a workflow using AI
// POST /api/agents/:id/generate-workflow
func (h *AgentHandler) GenerateWorkflow(c *fiber.Ctx) error {
	userID, ok := c.Locals("user_id").(string)
	if !ok || userID == "" {
		return c.Status(fiber.StatusUnauthorized).JSON(fiber.Map{
			"error": "Authentication required",
		})
	}

	agentID := c.Params("id")
	if agentID == "" {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{
			"error": "Agent ID is required",
		})
	}

	// Parse request body
	var req models.WorkflowGenerateRequest
	if err := c.BodyParser(&req); err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{
			"error": "Invalid request body",
		})
	}

	if req.UserMessage == "" {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{
			"error": "User message is required",
		})
	}

	req.AgentID = agentID

	// Get or create the agent - auto-create if it doesn't exist yet
	// This supports the frontend workflow where agent IDs are generated client-side
	agent, err := h.agentService.GetAgent(agentID, userID)
	if err != nil {
		if err.Error() == "agent not found" {
			// Auto-create the agent with a default name (user can rename later)
			log.Printf("🆕 [WORKFLOW-GEN] Agent %s doesn't exist, creating it", agentID)
			agent, err = h.agentService.CreateAgentWithID(agentID, userID, "New Agent", "")
			if err != nil {
				log.Printf("❌ [WORKFLOW-GEN] Failed to auto-create agent: %v", err)
				return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{
					"error": "Failed to create agent",
				})
			}
			log.Printf("✅ [WORKFLOW-GEN] Auto-created agent %s", agentID)
		} else {
			return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{
				"error": "Failed to verify agent ownership",
			})
		}
	}
	_ = agent // Agent verified or created

	log.Printf("🔧 [WORKFLOW-GEN] Generating workflow for agent %s (user: %s)", agentID, userID)

	// Generate the workflow
	response, err := h.workflowGeneratorService.GenerateWorkflow(&req, userID)
	if err != nil {
		log.Printf("❌ [WORKFLOW-GEN] Failed to generate workflow: %v", err)
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{
			"error": "Failed to generate workflow",
		})
	}

	if !response.Success {
		log.Printf("⚠️ [WORKFLOW-GEN] Workflow generation failed: %s", response.Error)
		return c.Status(fiber.StatusUnprocessableEntity).JSON(response)
	}

	// Generate suggested name and description for new workflows
	// Check if agent still has default name - if so, generate a better one
	shouldGenerateMetadata := response.Action == "create" || (agent != nil && agent.Name == "New Agent")
	log.Printf("🔍 [WORKFLOW-GEN] Checking metadata generation: action=%s, agentName=%s, shouldGenerate=%v", response.Action, agent.Name, shouldGenerateMetadata)
	if shouldGenerateMetadata {
		metadata, err := h.workflowGeneratorService.GenerateAgentMetadata(req.UserMessage)
		if err != nil {
			log.Printf("⚠️ [WORKFLOW-GEN] Failed to generate agent metadata (non-fatal): %v", err)
		} else {
			response.SuggestedName = metadata.Name
			response.SuggestedDescription = metadata.Description
			log.Printf("📝 [WORKFLOW-GEN] Suggested agent: name=%s, desc=%s", metadata.Name, metadata.Description)

			// Immediately persist the generated name to the database
			// This ensures the name is saved even if frontend fails to update
			if metadata.Name != "" {
				updateReq := &models.UpdateAgentRequest{
					Name:        metadata.Name,
					Description: metadata.Description,
				}
				_, updateErr := h.agentService.UpdateAgent(agentID, userID, updateReq)
				if updateErr != nil {
					log.Printf("⚠️ [WORKFLOW-GEN] Failed to persist agent metadata (non-fatal): %v", updateErr)
				} else {
					log.Printf("💾 [WORKFLOW-GEN] Persisted agent metadata to database: name=%s", metadata.Name)
				}
			}
		}
	}

	log.Printf("✅ [WORKFLOW-GEN] Generated workflow for agent %s: %d blocks", agentID, len(response.Workflow.Blocks))
	return c.JSON(response)
}

// ============================================================================
// Workflow Version Handlers
// ============================================================================

// ListWorkflowVersions returns all versions for an agent's workflow
// GET /api/agents/:id/workflow/versions
func (h *AgentHandler) ListWorkflowVersions(c *fiber.Ctx) error {
	userID, ok := c.Locals("user_id").(string)
	if !ok || userID == "" {
		return c.Status(fiber.StatusUnauthorized).JSON(fiber.Map{
			"error": "Authentication required",
		})
	}

	agentID := c.Params("id")
	if agentID == "" {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{
			"error": "Agent ID is required",
		})
	}

	log.Printf("📜 [WORKFLOW] Listing versions for agent %s (user: %s)", agentID, userID)

	versions, err := h.agentService.ListWorkflowVersions(agentID, userID)
	if err != nil {
		if err.Error() == "agent not found" {
			return c.Status(fiber.StatusNotFound).JSON(fiber.Map{
				"error": "Agent not found",
			})
		}
		log.Printf("❌ [WORKFLOW] Failed to list versions: %v", err)
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{
			"error": "Failed to list workflow versions",
		})
	}

	return c.JSON(fiber.Map{
		"versions": versions,
		"count":    len(versions),
	})
}

// GetWorkflowVersion returns a specific workflow version
// GET /api/agents/:id/workflow/versions/:version
func (h *AgentHandler) GetWorkflowVersion(c *fiber.Ctx) error {
	userID, ok := c.Locals("user_id").(string)
	if !ok || userID == "" {
		return c.Status(fiber.StatusUnauthorized).JSON(fiber.Map{
			"error": "Authentication required",
		})
	}

	agentID := c.Params("id")
	if agentID == "" {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{
			"error": "Agent ID is required",
		})
	}

	version, err := c.ParamsInt("version")
	if err != nil || version <= 0 {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{
			"error": "Valid version number is required",
		})
	}

	log.Printf("🔍 [WORKFLOW] Getting version %d for agent %s (user: %s)", version, agentID, userID)

	workflow, err := h.agentService.GetWorkflowVersion(agentID, userID, version)
	if err != nil {
		if err.Error() == "agent not found" {
			return c.Status(fiber.StatusNotFound).JSON(fiber.Map{
				"error": "Agent not found",
			})
		}
		if err.Error() == "workflow version not found" {
			return c.Status(fiber.StatusNotFound).JSON(fiber.Map{
				"error": "Workflow version not found",
			})
		}
		log.Printf("❌ [WORKFLOW] Failed to get version: %v", err)
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{
			"error": "Failed to get workflow version",
		})
	}

	return c.JSON(workflow)
}

// RestoreWorkflowVersion restores a workflow to a previous version
// POST /api/agents/:id/workflow/restore/:version
func (h *AgentHandler) RestoreWorkflowVersion(c *fiber.Ctx) error {
	userID, ok := c.Locals("user_id").(string)
	if !ok || userID == "" {
		return c.Status(fiber.StatusUnauthorized).JSON(fiber.Map{
			"error": "Authentication required",
		})
	}

	agentID := c.Params("id")
	if agentID == "" {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{
			"error": "Agent ID is required",
		})
	}

	version, err := c.ParamsInt("version")
	if err != nil || version <= 0 {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{
			"error": "Valid version number is required",
		})
	}

	log.Printf("⏪ [WORKFLOW] Restoring version %d for agent %s (user: %s)", version, agentID, userID)

	workflow, err := h.agentService.RestoreWorkflowVersion(agentID, userID, version)
	if err != nil {
		if err.Error() == "agent not found" {
			return c.Status(fiber.StatusNotFound).JSON(fiber.Map{
				"error": "Agent not found",
			})
		}
		if err.Error() == "workflow version not found" {
			return c.Status(fiber.StatusNotFound).JSON(fiber.Map{
				"error": "Workflow version not found",
			})
		}
		log.Printf("❌ [WORKFLOW] Failed to restore version: %v", err)
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{
			"error": "Failed to restore workflow version",
		})
	}

	log.Printf("✅ [WORKFLOW] Restored version %d for agent %s (new version: %d)", version, agentID, workflow.Version)
	return c.JSON(workflow)
}

// SyncAgent syncs a local agent to the backend on first message
// This creates/updates the agent, workflow, and conversation in one call
// POST /api/agents/:id/sync
func (h *AgentHandler) SyncAgent(c *fiber.Ctx) error {
	userID, ok := c.Locals("user_id").(string)
	if !ok || userID == "" {
		return c.Status(fiber.StatusUnauthorized).JSON(fiber.Map{
			"error": "Authentication required",
		})
	}

	agentID := c.Params("id")
	if agentID == "" {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{
			"error": "Agent ID is required",
		})
	}

	var req models.SyncAgentRequest
	if err := c.BodyParser(&req); err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{
			"error": "Invalid request body",
		})
	}

	if req.Name == "" {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{
			"error": "Agent name is required",
		})
	}

	log.Printf("🔄 [AGENT] Syncing agent %s for user %s", agentID, userID)

	// Sync agent and workflow
	agent, workflow, err := h.agentService.SyncAgent(agentID, userID, &req)
	if err != nil {
		log.Printf("❌ [AGENT] Failed to sync agent: %v", err)
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{
			"error": "Failed to sync agent",
		})
	}

	// Create conversation if builder conversation service is available
	var conversationID string
	if h.builderConvService != nil {
		conv, err := h.builderConvService.CreateConversation(c.Context(), agentID, userID, req.ModelID)
		if err != nil {
			log.Printf("⚠️ [AGENT] Failed to create conversation (non-fatal): %v", err)
			// Continue without conversation - not fatal
		} else {
			conversationID = conv.ID
			log.Printf("✅ [AGENT] Created conversation %s for agent %s", conversationID, agentID)
		}
	}

	log.Printf("✅ [AGENT] Synced agent %s (workflow v%d, conv: %s)", agentID, workflow.Version, conversationID)

	return c.JSON(&models.SyncAgentResponse{
		Agent:          agent,
		Workflow:       workflow,
		ConversationID: conversationID,
	})
}

// GenerateWorkflowV2 generates a workflow using multi-step process with tool selection
// POST /api/agents/:id/generate-workflow-v2
func (h *AgentHandler) GenerateWorkflowV2(c *fiber.Ctx) error {
	userID, ok := c.Locals("user_id").(string)
	if !ok || userID == "" {
		return c.Status(fiber.StatusUnauthorized).JSON(fiber.Map{
			"error": "Authentication required",
		})
	}

	if h.workflowGeneratorV2Service == nil {
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{
			"error": "Workflow generator v2 service not available",
		})
	}

	agentID := c.Params("id")
	if agentID == "" {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{
			"error": "Agent ID is required",
		})
	}

	// Parse request body
	var req services.MultiStepGenerateRequest
	if err := c.BodyParser(&req); err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{
			"error": "Invalid request body",
		})
	}

	if req.UserMessage == "" {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{
			"error": "User message is required",
		})
	}

	req.AgentID = agentID

	// Get or create the agent - auto-create if it doesn't exist yet
	agent, err := h.agentService.GetAgent(agentID, userID)
	if err != nil {
		if err.Error() == "agent not found" {
			log.Printf("🆕 [WORKFLOW-GEN-V2] Agent %s doesn't exist, creating it", agentID)
			agent, err = h.agentService.CreateAgentWithID(agentID, userID, "New Agent", "")
			if err != nil {
				log.Printf("❌ [WORKFLOW-GEN-V2] Failed to auto-create agent: %v", err)
				return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{
					"error": "Failed to create agent",
				})
			}
			log.Printf("✅ [WORKFLOW-GEN-V2] Auto-created agent %s", agentID)
		} else {
			return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{
				"error": "Failed to verify agent ownership",
			})
		}
	}

	log.Printf("🔧 [WORKFLOW-GEN-V2] Starting multi-step generation for agent %s (user: %s)", agentID, userID)

	// Generate the workflow using multi-step process
	response, err := h.workflowGeneratorV2Service.GenerateWorkflowMultiStep(&req, userID, nil)
	if err != nil {
		log.Printf("❌ [WORKFLOW-GEN-V2] Failed to generate workflow: %v", err)
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{
			"error": "Failed to generate workflow",
		})
	}

	if !response.Success {
		log.Printf("⚠️ [WORKFLOW-GEN-V2] Workflow generation failed: %s", response.Error)
		return c.Status(fiber.StatusUnprocessableEntity).JSON(response)
	}

	// Generate suggested name and description for new workflows
	shouldGenerateMetadata := agent != nil && agent.Name == "New Agent"
	if shouldGenerateMetadata && h.workflowGeneratorService != nil {
		metadata, err := h.workflowGeneratorService.GenerateAgentMetadata(req.UserMessage)
		if err != nil {
			log.Printf("⚠️ [WORKFLOW-GEN-V2] Failed to generate agent metadata (non-fatal): %v", err)
		} else if metadata.Name != "" {
			// Persist the generated name
			updateReq := &models.UpdateAgentRequest{
				Name:        metadata.Name,
				Description: metadata.Description,
			}
			_, updateErr := h.agentService.UpdateAgent(agentID, userID, updateReq)
			if updateErr != nil {
				log.Printf("⚠️ [WORKFLOW-GEN-V2] Failed to persist agent metadata (non-fatal): %v", updateErr)
			} else {
				log.Printf("💾 [WORKFLOW-GEN-V2] Persisted agent metadata: name=%s", metadata.Name)
			}
		}
	}

	log.Printf("✅ [WORKFLOW-GEN-V2] Generated workflow for agent %s: %d blocks, %d tools selected",
		agentID, len(response.Workflow.Blocks), len(response.SelectedTools))
	return c.JSON(response)
}

// GetToolRegistry returns all available tools and categories for the frontend
// GET /api/tools/registry
func (h *AgentHandler) GetToolRegistry(c *fiber.Ctx) error {
	return c.JSON(fiber.Map{
		"tools":      services.ToolRegistry,
		"categories": services.ToolCategoryRegistry,
	})
}

// SelectTools performs just the tool selection step (Step 1 only)
// POST /api/agents/:id/select-tools
func (h *AgentHandler) SelectTools(c *fiber.Ctx) error {
	userID, ok := c.Locals("user_id").(string)
	if !ok || userID == "" {
		return c.Status(fiber.StatusUnauthorized).JSON(fiber.Map{
			"error": "Authentication required",
		})
	}

	if h.workflowGeneratorV2Service == nil {
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{
			"error": "Workflow generator v2 service not available",
		})
	}

	agentID := c.Params("id")
	if agentID == "" {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{
			"error": "Agent ID is required",
		})
	}

	// Parse request body
	var req services.MultiStepGenerateRequest
	if err := c.BodyParser(&req); err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{
			"error": "Invalid request body",
		})
	}

	if req.UserMessage == "" {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{
			"error": "User message is required",
		})
	}

	req.AgentID = agentID

	log.Printf("🔧 [TOOL-SELECT] Selecting tools for agent %s (user: %s)", agentID, userID)

	// Perform tool selection only
	result, err := h.workflowGeneratorV2Service.Step1SelectTools(&req, userID)
	if err != nil {
		log.Printf("❌ [TOOL-SELECT] Failed to select tools: %v", err)
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{
			"error": "Failed to select tools",
		})
	}

	log.Printf("✅ [TOOL-SELECT] Selected %d tools for agent %s", len(result.SelectedTools), agentID)
	return c.JSON(result)
}

// GenerateWithToolsRequest is the request for generating a workflow with pre-selected tools
type GenerateWithToolsRequest struct {
	UserMessage     string                   `json:"user_message"`
	ModelID         string                   `json:"model_id,omitempty"`
	SelectedTools   []services.SelectedTool  `json:"selected_tools"`
	CurrentWorkflow *models.Workflow         `json:"current_workflow,omitempty"`
}

// GenerateWithTools performs workflow generation with pre-selected tools (Step 2 only)
// POST /api/agents/:id/generate-with-tools
func (h *AgentHandler) GenerateWithTools(c *fiber.Ctx) error {
	userID, ok := c.Locals("user_id").(string)
	if !ok || userID == "" {
		return c.Status(fiber.StatusUnauthorized).JSON(fiber.Map{
			"error": "Authentication required",
		})
	}

	if h.workflowGeneratorV2Service == nil {
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{
			"error": "Workflow generator v2 service not available",
		})
	}

	agentID := c.Params("id")
	if agentID == "" {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{
			"error": "Agent ID is required",
		})
	}

	// Parse request body
	var req GenerateWithToolsRequest
	if err := c.BodyParser(&req); err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{
			"error": "Invalid request body",
		})
	}

	if req.UserMessage == "" {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{
			"error": "User message is required",
		})
	}

	if len(req.SelectedTools) == 0 {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{
			"error": "Selected tools are required",
		})
	}

	log.Printf("🔧 [GENERATE-WITH-TOOLS] Generating workflow for agent %s with %d pre-selected tools (user: %s)",
		agentID, len(req.SelectedTools), userID)

	// Build the multi-step request
	multiStepReq := &services.MultiStepGenerateRequest{
		AgentID:         agentID,
		UserMessage:     req.UserMessage,
		ModelID:         req.ModelID,
		CurrentWorkflow: req.CurrentWorkflow,
	}

	// Perform workflow generation with pre-selected tools
	result, err := h.workflowGeneratorV2Service.Step2GenerateWorkflow(multiStepReq, req.SelectedTools, userID)
	if err != nil {
		log.Printf("❌ [GENERATE-WITH-TOOLS] Failed to generate workflow: %v", err)
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{
			"error":   "Failed to generate workflow",
			"details": err.Error(),
		})
	}

	if !result.Success {
		log.Printf("⚠️ [GENERATE-WITH-TOOLS] Workflow generation failed: %s", result.Error)
		return c.Status(fiber.StatusUnprocessableEntity).JSON(result)
	}

	// Generate suggested name and description for new workflows
	agent, _ := h.agentService.GetAgent(agentID, userID)
	shouldGenerateMetadata := agent != nil && agent.Name == "New Agent"
	if shouldGenerateMetadata && h.workflowGeneratorService != nil {
		metadata, err := h.workflowGeneratorService.GenerateAgentMetadata(req.UserMessage)
		if err != nil {
			log.Printf("⚠️ [GENERATE-WITH-TOOLS] Failed to generate agent metadata (non-fatal): %v", err)
		} else if metadata.Name != "" {
			result.SuggestedName = metadata.Name
			result.SuggestedDescription = metadata.Description

			// Persist the generated name
			updateReq := &models.UpdateAgentRequest{
				Name:        metadata.Name,
				Description: metadata.Description,
			}
			_, updateErr := h.agentService.UpdateAgent(agentID, userID, updateReq)
			if updateErr != nil {
				log.Printf("⚠️ [GENERATE-WITH-TOOLS] Failed to persist agent metadata (non-fatal): %v", updateErr)
			} else {
				log.Printf("💾 [GENERATE-WITH-TOOLS] Persisted agent metadata: name=%s", metadata.Name)
			}
		}
	}

	log.Printf("✅ [GENERATE-WITH-TOOLS] Generated workflow for agent %s: %d blocks",
		agentID, len(result.Workflow.Blocks))
	return c.JSON(result)
}

// GenerateSampleInput uses AI to generate sample JSON input for a workflow
// POST /api/agents/:id/generate-sample-input
func (h *AgentHandler) GenerateSampleInput(c *fiber.Ctx) error {
	userID, ok := c.Locals("user_id").(string)
	if !ok || userID == "" {
		return c.Status(fiber.StatusUnauthorized).JSON(fiber.Map{
			"error": "Authentication required",
		})
	}

	agentID := c.Params("id")
	if agentID == "" {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{
			"error": "Agent ID is required",
		})
	}

	// Parse request body
	var req struct {
		ModelID string `json:"model_id"`
	}
	if err := c.BodyParser(&req); err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{
			"error": "Invalid request body",
		})
	}

	if req.ModelID == "" {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{
			"error": "Model ID is required",
		})
	}

	// Get the agent and workflow
	agent, err := h.agentService.GetAgent(agentID, userID)
	if err != nil {
		log.Printf("❌ [SAMPLE-INPUT] Failed to get agent: %v", err)
		return c.Status(fiber.StatusNotFound).JSON(fiber.Map{
			"error": "Agent not found",
		})
	}

	if agent.Workflow == nil || len(agent.Workflow.Blocks) == 0 {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{
			"error": "Workflow has no blocks",
		})
	}

	// Generate sample input using the workflow generator service
	sampleInput, err := h.workflowGeneratorService.GenerateSampleInput(agent.Workflow, req.ModelID, userID)
	if err != nil {
		log.Printf("❌ [SAMPLE-INPUT] Failed to generate sample input: %v", err)
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{
			"error":   "Failed to generate sample input",
			"details": err.Error(),
		})
	}

	log.Printf("✅ [SAMPLE-INPUT] Generated sample input for agent %s", agentID)
	return c.JSON(fiber.Map{
		"success":      true,
		"sample_input": sampleInput,
	})
}

// Ask handles Ask mode requests - helps users understand their workflow
// POST /api/agents/ask
func (h *AgentHandler) Ask(c *fiber.Ctx) error {
	userID, ok := c.Locals("user_id").(string)
	if !ok || userID == "" {
		return c.Status(fiber.StatusUnauthorized).JSON(fiber.Map{
			"error": "Authentication required",
		})
	}

	var req struct {
		AgentID string `json:"agent_id"`
		Message string `json:"message"`
		ModelID string `json:"model_id"`
		Context struct {
			Workflow          *models.Workflow       `json:"workflow"`
			AvailableTools    []map[string]string    `json:"available_tools"`
			DeploymentExample string                 `json:"deployment_example"`
		} `json:"context"`
	}

	if err := c.BodyParser(&req); err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{
			"error": "Invalid request body",
		})
	}

	if req.AgentID == "" || req.Message == "" {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{
			"error": "agent_id and message are required",
		})
	}

	if h.providerService == nil {
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{
			"error": "Provider service not available",
		})
	}

	// Get the agent to verify ownership
	agent, err := h.agentService.GetAgent(req.AgentID, userID)
	if err != nil {
		return c.Status(fiber.StatusNotFound).JSON(fiber.Map{
			"error": "Agent not found",
		})
	}

	log.Printf("💬 [ASK] User %s asking about agent %s: %s", userID, agent.Name, req.Message)

	// Build context from workflow
	var workflowContext string
	if req.Context.Workflow != nil && len(req.Context.Workflow.Blocks) > 0 {
		workflowContext = "\n\n## Current Workflow Structure\n"
		for i, block := range req.Context.Workflow.Blocks {
			desc := block.Description
			if desc == "" {
				desc = "No description"
			}
			workflowContext += fmt.Sprintf("%d. **%s** (%s): %s\n", i+1, block.Name, block.Type, desc)
		}
	}

	// Build tools context
	var toolsContext string
	if len(req.Context.AvailableTools) > 0 {
		toolsContext = "\n\n## Available Tools\n"
		for _, tool := range req.Context.AvailableTools {
			toolsContext += fmt.Sprintf("- **%s**: %s (Category: %s)\n",
				tool["name"], tool["description"], tool["category"])
		}
	}

	// Build deployment context
	var deploymentContext string
	if req.Context.DeploymentExample != "" {
		deploymentContext = "\n\n## Deployment API Example\n```bash\n" + req.Context.DeploymentExample + "\n```"
	}

	// Build system prompt
	systemPrompt := fmt.Sprintf(`You are an AI assistant helping users understand their workflow agent in DobbyAI.

**Agent Name**: %s
**Agent Description**: %s

Your role is to:
1. Answer questions about the workflow structure and how it works
2. Explain what tools are available and how to use them
3. Help with deployment and API integration questions
4. Provide clear, concise explanations

**IMPORTANT**: You are in "Ask" mode, which is for answering questions only. If the user asks you to modify the workflow (add, change, remove blocks), politely tell them to switch to "Builder" mode.

%s%s%s

Be helpful, clear, and concise. If you don't know something, say so.`,
		agent.Name,
		agent.Description,
		workflowContext,
		toolsContext,
		deploymentContext,
	)

	// Call LLM with simple chat endpoint
	modelID := req.ModelID
	if modelID == "" {
		modelID = "gpt-4.1" // Default model
	}

	// Get provider for model. Use *WithName so the request body carries
	// the upstream API name (e.g. "zai.glm-5") not the row id ("3:zai.glm-5")
	// — Bedrock and other strict providers reject the prefixed form.
	provider, apiName, err := h.providerService.GetByModelIDWithName(modelID)
	if err != nil {
		log.Printf("❌ [ASK] Failed to get provider for model %s: %v", modelID, err)
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{
			"error": fmt.Sprintf("Model '%s' not found", modelID),
		})
	}
	modelID = apiName

	// Build OpenAI-compatible request
	type Message struct {
		Role    string `json:"role"`
		Content string `json:"content"`
	}
	type OpenAIRequest struct {
		Model    string    `json:"model"`
		Messages []Message `json:"messages"`
	}

	reqBody, err := json.Marshal(OpenAIRequest{
		Model: modelID,
		Messages: []Message{
			{Role: "system", Content: systemPrompt},
			{Role: "user", Content: req.Message},
		},
	})
	if err != nil {
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{
			"error": "Failed to prepare request",
		})
	}

	// Make HTTP request
	httpReq, err := http.NewRequest("POST", provider.BaseURL+"/chat/completions", bytes.NewBuffer(reqBody))
	if err != nil {
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{
			"error": "Failed to create HTTP request",
		})
	}

	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("Authorization", "Bearer "+provider.APIKey)

	client := &http.Client{Timeout: 60 * time.Second}
	resp, err := client.Do(httpReq)
	if err != nil {
		log.Printf("❌ [ASK] HTTP request failed: %v", err)
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{
			"error": "Failed to get response from AI",
		})
	}
	defer resp.Body.Close()

	// Read response
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{
			"error": "Failed to read response",
		})
	}

	if resp.StatusCode != http.StatusOK {
		log.Printf("⚠️ [ASK] API error: %s", string(body))
		return c.Status(resp.StatusCode).JSON(fiber.Map{
			"error": fmt.Sprintf("AI service error: %s", string(body)),
		})
	}

	// Parse response
	var apiResponse struct {
		Choices []struct {
			Message struct {
				Content string `json:"content"`
			} `json:"message"`
		} `json:"choices"`
	}

	if err := json.Unmarshal(body, &apiResponse); err != nil {
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{
			"error": "Failed to parse AI response",
		})
	}

	if len(apiResponse.Choices) == 0 {
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{
			"error": "No response from AI",
		})
	}

	responseText := apiResponse.Choices[0].Message.Content

	log.Printf("✅ [ASK] Response generated for agent %s", agent.Name)
	return c.JSON(fiber.Map{
		"response": responseText,
	})
}

// AutoFillBlock uses AI to suggest block configuration based on upstream execution data.
// POST /api/agents/autofill
func (h *AgentHandler) AutoFillBlock(c *fiber.Ctx) error {
	userID, ok := c.Locals("user_id").(string)
	if !ok || userID == "" {
		return c.Status(fiber.StatusUnauthorized).JSON(fiber.Map{
			"error": "Authentication required",
		})
	}

	var req struct {
		ModelID       string                            `json:"model_id"`
		BlockType     string                            `json:"block_type"`
		BlockName     string                            `json:"block_name"`
		ToolName      string                            `json:"tool_name"`
		ToolSchema    map[string]interface{}             `json:"tool_schema"`
		CurrentConfig map[string]interface{}             `json:"current_config"`
		UpstreamData  map[string]map[string]interface{} `json:"upstream_data"`
		UserContext   string                            `json:"user_context"`
	}

	if err := c.BodyParser(&req); err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{
			"error": "Invalid request body",
		})
	}

	if req.BlockType == "" {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{
			"error": "block_type is required",
		})
	}

	if len(req.UpstreamData) == 0 {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{
			"error": "upstream_data is required (run the workflow first)",
		})
	}

	if h.providerService == nil {
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{
			"error": "Provider service not available",
		})
	}

	// Build the upstream data description
	var upstreamDesc strings.Builder
	upstreamDesc.WriteString("UPSTREAM BLOCKS (available data you can reference using {{block-name.field.path}} syntax):\n")
	for blockName, outputs := range req.UpstreamData {
		upstreamDesc.WriteString(fmt.Sprintf("\nBlock \"%s\":\n", blockName))
		describeValue(&upstreamDesc, blockName, outputs, 0, 4)
		// Include raw JSON sample so the LLM sees the actual data shape
		if sampleJSON, err := json.MarshalIndent(outputs, "  ", "  "); err == nil {
			sample := string(sampleJSON)
			if len(sample) > 500 {
				sample = sample[:500] + "..."
			}
			upstreamDesc.WriteString(fmt.Sprintf("  Raw sample:\n  %s\n", sample))
		}
	}

	// Build block-specific instructions
	blockInstructions := buildBlockInstructions(req.BlockType, req.BlockName, req.ToolName, req.ToolSchema)

	// Build current config description (so LLM knows what's already filled)
	var currentConfigSection string
	if len(req.CurrentConfig) > 0 {
		var configDesc strings.Builder
		configDesc.WriteString("\nCURRENT CONFIGURATION (already filled by the user — preserve these values, do NOT replace with placeholders):\n")
		for key, val := range req.CurrentConfig {
			// Skip internal type fields, credentials, and empty values
			if key == "type" || key == "credentials" || key == "credentials_id" {
				continue
			}
			valStr := fmt.Sprintf("%v", val)
			if valStr == "" || valStr == "<nil>" || valStr == "map[]" || valStr == "[]" {
				continue
			}
			configDesc.WriteString(fmt.Sprintf("  %s = %s\n", key, truncateValue(valStr)))
		}
		currentConfigSection = configDesc.String()
	}

	// Build optional user context section
	var userContextSection string
	if req.UserContext != "" {
		userContextSection = fmt.Sprintf("\nUSER CONTEXT (important — use this information for filling in specific values):\n%s\n", req.UserContext)
	}

	// Build the system prompt
	systemPrompt := fmt.Sprintf(`You are a workflow automation configuration assistant. Your job is to fill in the configuration for a workflow block by mapping upstream data to the block's parameters.

TEMPLATE SYNTAX: Use {{block-name.field.path}} to reference dynamic data from upstream blocks. Use literal values for static configuration.

%s
%s
%s
%s
RULES:
1. Return ONLY a valid JSON object — no markdown, no explanations, no code fences.
2. Use {{block-name.field}} template references for dynamic values from upstream blocks.
3. Use literal values for static configuration (like channel names, formats, etc.).
4. For string parameters that need multiple upstream values, compose them naturally (e.g., "Temperature in {{weather.response.city}}: {{weather.response.temp}}°F").
5. Only include fields that you can meaningfully fill — skip fields you're unsure about.
6. Match upstream data types to parameter types where possible.
7. If the user provided context (sheet IDs, column names, channel names, etc.), use those EXACT values.
8. IMPORTANT: If a field already has a value in the CURRENT CONFIGURATION, keep that exact value unless upstream data provides a clearly better dynamic mapping. Never replace real IDs, URLs, or names with placeholders.
9. NEVER include "credentials", "credentials_id", or any authentication-related fields in your response. Those are managed separately by the user.`,
		upstreamDesc.String(),
		blockInstructions,
		currentConfigSection,
		userContextSection,
	)

	log.Printf("🤖 [AUTOFILL] User %s: auto-filling %s block '%s'", userID, req.BlockType, req.BlockName)

	// Resolve model
	modelID := req.ModelID
	if modelID == "" {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{
			"error": "No model specified — please select a model in the workflow toolbar",
		})
	}

	provider, apiName, err := h.providerService.GetByModelIDWithName(modelID)
	if err != nil {
		log.Printf("❌ [AUTOFILL] Failed to get provider for model %s: %v", modelID, err)
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{
			"error": fmt.Sprintf("Model '%s' not found — configure a model in your workflow first", modelID),
		})
	}
	modelID = apiName // use upstream API name, not row id

	// Build OpenAI-compatible request
	type Message struct {
		Role    string `json:"role"`
		Content string `json:"content"`
	}

	reqBody, err := json.Marshal(map[string]interface{}{
		"model": modelID,
		"messages": []Message{
			{Role: "system", Content: systemPrompt},
			{Role: "user", Content: "Fill in the block configuration now. Return ONLY the JSON object."},
		},
		"response_format": map[string]interface{}{
			"type": "json_object",
		},
	})
	if err != nil {
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{
			"error": "Failed to prepare request",
		})
	}

	httpReq, err := http.NewRequest("POST", provider.BaseURL+"/chat/completions", bytes.NewBuffer(reqBody))
	if err != nil {
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{
			"error": "Failed to create HTTP request",
		})
	}
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("Authorization", "Bearer "+provider.APIKey)

	client := &http.Client{Timeout: 30 * time.Second}
	resp, err := client.Do(httpReq)
	if err != nil {
		log.Printf("❌ [AUTOFILL] HTTP request failed: %v", err)
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{
			"error": "Failed to get response from AI",
		})
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{
			"error": "Failed to read response",
		})
	}

	if resp.StatusCode != http.StatusOK {
		log.Printf("⚠️ [AUTOFILL] API error: %s", string(body))
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{
			"error": "AI service returned an error",
		})
	}

	// Parse OpenAI response
	var apiResponse struct {
		Choices []struct {
			Message struct {
				Content string `json:"content"`
			} `json:"message"`
		} `json:"choices"`
	}

	if err := json.Unmarshal(body, &apiResponse); err != nil || len(apiResponse.Choices) == 0 {
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{
			"error": "Failed to parse AI response",
		})
	}

	responseText := strings.TrimSpace(apiResponse.Choices[0].Message.Content)

	// Strip markdown code fences if present
	responseText = stripCodeFences(responseText)

	// Parse the JSON config
	var suggestedConfig map[string]interface{}
	if err := json.Unmarshal([]byte(responseText), &suggestedConfig); err != nil {
		log.Printf("⚠️ [AUTOFILL] Failed to parse LLM JSON response: %v\nRaw: %s", err, responseText)
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{
			"error": "AI returned invalid JSON — try again",
		})
	}

	// Strip credentials the LLM may have hallucinated
	delete(suggestedConfig, "credentials")
	delete(suggestedConfig, "credentials_id")

	log.Printf("✅ [AUTOFILL] Generated config for %s block '%s': %d fields", req.BlockType, req.BlockName, len(suggestedConfig))

	return c.JSON(fiber.Map{
		"config": suggestedConfig,
	})
}

// stripCodeFences removes markdown ```json ... ``` wrappers from a string.
func stripCodeFences(s string) string {
	s = strings.TrimSpace(s)
	if strings.HasPrefix(s, "```") {
		// Remove opening fence (```json or ```)
		if idx := strings.Index(s, "\n"); idx != -1 {
			s = s[idx+1:]
		}
		// Remove closing fence
		if idx := strings.LastIndex(s, "```"); idx != -1 {
			s = s[:idx]
		}
	}
	return strings.TrimSpace(s)
}

// describeValue recursively describes a JSON value for the LLM prompt, showing field paths and sample values.
func describeValue(sb *strings.Builder, prefix string, value interface{}, depth, maxDepth int) {
	if depth >= maxDepth {
		return
	}
	indent := strings.Repeat("  ", depth+1)

	switch v := value.(type) {
	case map[string]interface{}:
		for key, val := range v {
			path := prefix + "." + key
			switch inner := val.(type) {
			case map[string]interface{}:
				sb.WriteString(fmt.Sprintf("%s- %s (object)\n", indent, path))
				describeValue(sb, path, inner, depth+1, maxDepth)
			case []interface{}:
				if len(inner) > 0 {
					sb.WriteString(fmt.Sprintf("%s- %s (array of %d items)\n", indent, path, len(inner)))
					// Recurse into first element to show full structure
					describeValue(sb, path+".0", inner[0], depth+1, maxDepth)
				} else {
					sb.WriteString(fmt.Sprintf("%s- %s (empty array)\n", indent, path))
				}
			default:
				sb.WriteString(fmt.Sprintf("%s- %s = %s (%s)\n", indent, path, truncateValue(val), typeOf(val)))
			}
		}
	case []interface{}:
		if len(v) > 0 {
			sb.WriteString(fmt.Sprintf("%s- %s (array of %d items)\n", indent, prefix, len(v)))
			describeValue(sb, prefix+".0", v[0], depth+1, maxDepth)
		} else {
			sb.WriteString(fmt.Sprintf("%s- %s (empty array)\n", indent, prefix))
		}
	default:
		// Scalar value (string, number, bool, null) — e.g. first element of an array
		sb.WriteString(fmt.Sprintf("%s- %s = %s (%s)\n", indent, prefix, truncateValue(value), typeOf(value)))
	}
}

// truncateValue returns a short string representation of a value for the prompt.
func truncateValue(v interface{}) string {
	switch val := v.(type) {
	case string:
		if len(val) > 120 {
			return fmt.Sprintf("%q...", val[:120])
		}
		return fmt.Sprintf("%q", val)
	case float64:
		if val == float64(int64(val)) {
			return fmt.Sprintf("%d", int64(val))
		}
		return fmt.Sprintf("%.2f", val)
	case bool:
		return fmt.Sprintf("%v", val)
	case nil:
		return "null"
	default:
		b, _ := json.Marshal(val)
		s := string(b)
		if len(s) > 120 {
			return s[:120] + "..."
		}
		return s
	}
}

// typeOf returns a human-readable type name for a JSON value.
func typeOf(v interface{}) string {
	switch v.(type) {
	case string:
		return "string"
	case float64:
		return "number"
	case bool:
		return "boolean"
	case nil:
		return "null"
	case []interface{}:
		return "array"
	case map[string]interface{}:
		return "object"
	default:
		return "unknown"
	}
}

// buildBlockInstructions returns block-type-specific instructions for the LLM.
func buildBlockInstructions(blockType, blockName, toolName string, toolSchema map[string]interface{}) string {
	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("CURRENT BLOCK: \"%s\" (type: %s)\n", blockName, blockType))

	switch blockType {
	case "code_block":
		sb.WriteString(fmt.Sprintf("TOOL: %s\n", toolName))
		sb.WriteString("Return a JSON object with an \"argumentMapping\" key containing parameter-name → value mappings.\n")
		sb.WriteString("Do NOT include \"credentials\" or \"credentials_id\" in the argumentMapping — those are managed separately.\n")
		sb.WriteString("IMPORTANT: For parameters with type \"array\", use actual JSON arrays — NOT string templates. The system resolves {{...}} inside arrays recursively.\n")
		sb.WriteString("Example (string param): {\"argumentMapping\": {\"channel\": \"#general\", \"message\": \"{{upstream.response}}\"}}\n")
		sb.WriteString("Example (array param):  {\"argumentMapping\": {\"values\": [[\"{{upstream.data.col1}}\", \"{{upstream.data.col2}}\"]]}}\n\n")
		if toolSchema != nil {
			sb.WriteString("TOOL PARAMETERS:\n")
			if props, ok := toolSchema["properties"].(map[string]interface{}); ok {
				required := map[string]bool{}
				if reqArr, ok := toolSchema["required"].([]interface{}); ok {
					for _, r := range reqArr {
						if s, ok := r.(string); ok {
							required[s] = true
						}
					}
				}
				for name, propRaw := range props {
					prop, _ := propRaw.(map[string]interface{})
					pType, _ := prop["type"].(string)
					pDesc, _ := prop["description"].(string)
					reqStr := ""
					if required[name] {
						reqStr = ", REQUIRED"
					}
					enumStr := ""
					if enum, ok := prop["enum"].([]interface{}); ok {
						vals := make([]string, len(enum))
						for i, e := range enum {
							vals[i] = fmt.Sprintf("%v", e)
						}
						enumStr = fmt.Sprintf(", enum: [%s]", strings.Join(vals, ", "))
					}
					sb.WriteString(fmt.Sprintf("- %s (%s%s%s): %s\n", name, pType, reqStr, enumStr, pDesc))
				}
			}
		}

	case "http_request":
		sb.WriteString("Return a JSON object with these optional keys: \"url\", \"method\", \"headers\", \"body\", \"queryParams\".\n")
		sb.WriteString("Example: {\"url\": \"https://api.example.com/data/{{upstream.response.id}}\", \"method\": \"POST\", \"body\": \"{\\\"name\\\": \\\"{{upstream.response.name}}\\\"}\", \"headers\": {\"Authorization\": \"Bearer token\"}}\n")

	case "llm_inference":
		sb.WriteString("Return a JSON object with a \"userPromptTemplate\" key — a natural language prompt that references upstream data.\n")
		sb.WriteString("Example: {\"userPromptTemplate\": \"Summarize the following article:\\n\\n{{fetch-article.response}}\"}\n")

	case "if_condition":
		sb.WriteString("Return a JSON object with \"field\" (upstream path without {{ }}), \"operator\" (one of: equals, not_equals, contains, not_contains, greater_than, less_than, is_true, is_false, is_empty, is_not_empty), and \"value\".\n")
		sb.WriteString("Example: {\"field\": \"response.status\", \"operator\": \"equals\", \"value\": \"success\"}\n")

	case "transform":
		sb.WriteString("Return a JSON object with an \"operations\" array. Each operation has \"type\" (set/rename/delete/extract), \"field\", and optionally \"value\" or \"newField\".\n")
		sb.WriteString("Example: {\"operations\": [{\"type\": \"set\", \"field\": \"summary\", \"value\": \"{{upstream.response.text}}\"}]}\n")

	case "for_each":
		sb.WriteString("Return a JSON object with \"arrayField\" — the path to the array in upstream output.\n")
		sb.WriteString("Example: {\"arrayField\": \"response.items\"}\n")

	case "inline_code":
		sb.WriteString("Return a JSON object with \"code\" — Python code that processes `inputs` dict and sets `output` variable.\n")
		sb.WriteString("Example: {\"code\": \"data = inputs.get('response', {})\\noutput = {'count': len(data.get('items', []))}\"}\n")

	case "sub_agent":
		sb.WriteString("Return a JSON object with \"inputMapping\" — a template string for the sub-agent input.\n")
		sb.WriteString("Example: {\"inputMapping\": \"Process this data: {{upstream.response}}\"}\n")

	default:
		sb.WriteString("Return a JSON object with the appropriate configuration fields for this block.\n")
	}

	return sb.String()
}

// TestBlock executes a single block in isolation with provided upstream data.
// POST /api/agents/test-block
func (h *AgentHandler) TestBlock(c *fiber.Ctx) error {
	userID, ok := c.Locals("user_id").(string)
	if !ok || userID == "" {
		return c.Status(fiber.StatusUnauthorized).JSON(fiber.Map{
			"error": "Authentication required",
		})
	}

	var req struct {
		Block           models.Block                      `json:"block"`
		UpstreamOutputs map[string]map[string]interface{} `json:"upstream_outputs"`
		TestPayload     map[string]interface{}             `json:"test_payload"` // For trigger blocks: simulated incoming data
	}

	if err := c.BodyParser(&req); err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{
			"error": "Invalid request body",
		})
	}

	if req.Block.Type == "" {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{
			"error": "block.type is required",
		})
	}

	if h.executorRegistry == nil {
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{
			"error": "Executor registry not available",
		})
	}

	// Get executor for this block type
	executor, err := h.executorRegistry.Get(req.Block.Type)
	if err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{
			"error": fmt.Sprintf("Unsupported block type: %s", req.Block.Type),
		})
	}

	// Build inputs map (same structure as engine.go)
	blockInputs := make(map[string]any)

	essentialKeys := []string{
		"response", "data", "output", "value", "result",
		"artifacts", "toolResults", "tokens", "model",
		"iterations", "_parseError", "rawResponse",
		"generatedFiles", "toolCalls", "timedOut",
	}

	for normalizedID, outputs := range req.UpstreamOutputs {
		// Store under normalizedId for {{block-name.field}} access
		cleanOutput := make(map[string]any)
		for _, key := range essentialKeys {
			if val, exists := outputs[key]; exists {
				cleanOutput[key] = val
			}
		}
		blockInputs[normalizedID] = cleanOutput

		// Flatten essential keys for {{response}} shorthand access
		for _, key := range essentialKeys {
			if val, exists := outputs[key]; exists {
				blockInputs[key] = val
			}
		}
	}

	// Inject user ID for credential resolution (tools read __user_id__ from inputs)
	blockInputs["__user_id__"] = userID

	// For trigger blocks, merge test payload into inputs so the executor sees it as incoming data
	if len(req.TestPayload) > 0 && (req.Block.Type == "webhook_trigger" || req.Block.Type == "schedule_trigger") {
		for k, v := range req.TestPayload {
			blockInputs[k] = v
		}
		// Also set standard webhook input structure if "body" not explicitly provided
		if _, hasBody := req.TestPayload["body"]; !hasBody {
			blockInputs["body"] = req.TestPayload
		}
	}

	log.Printf("🧪 [TEST-BLOCK] User %s: testing %s block '%s' with %d upstream sources",
		userID, req.Block.Type, req.Block.Name, len(req.UpstreamOutputs))

	// Execute with timeout
	timeout := 30 * time.Second
	if req.Block.Type == "llm_inference" {
		timeout = 120 * time.Second
	}
	if req.Block.Timeout > 0 {
		userTimeout := time.Duration(req.Block.Timeout) * time.Second
		if req.Block.Type == "llm_inference" && userTimeout < 120*time.Second {
			timeout = 120 * time.Second
		} else {
			timeout = userTimeout
		}
	}

	ctx, cancel := context.WithTimeout(c.Context(), timeout)
	defer cancel()

	// Add userID to context for executors that need it
	ctx = context.WithValue(ctx, "userID", userID)

	startTime := time.Now()
	output, execErr := executor.Execute(ctx, req.Block, blockInputs)
	duration := time.Since(startTime)

	if execErr != nil {
		log.Printf("❌ [TEST-BLOCK] Block '%s' failed after %dms: %v", req.Block.Name, duration.Milliseconds(), execErr)
		return c.JSON(fiber.Map{
			"status":      "failed",
			"error":       execErr.Error(),
			"duration_ms": duration.Milliseconds(),
		})
	}

	log.Printf("✅ [TEST-BLOCK] Block '%s' completed in %dms", req.Block.Name, duration.Milliseconds())

	// For for_each blocks, show what a single iteration looks like (what downstream blocks receive)
	if req.Block.Type == "for_each" {
		if items, ok := output["response"].([]any); ok && len(items) > 0 {
			itemVariable := "item"
			if iv, ok := req.Block.Config["itemVariable"].(string); ok && iv != "" {
				itemVariable = iv
			}
			firstItem := items[0]
			perItemPreview := map[string]any{
				"response":    firstItem,
				"data":        firstItem,
				itemVariable:  firstItem,
				"index":       0,
				"totalItems":  len(items),
			}
			return c.JSON(fiber.Map{
				"status":      "completed",
				"output":      perItemPreview,
				"duration_ms": duration.Milliseconds(),
				"_note":       fmt.Sprintf("Showing iteration preview (item 1 of %d). During full workflow run, downstream blocks execute once per item.", len(items)),
			})
		}
	}

	return c.JSON(fiber.Map{
		"status":      "completed",
		"output":      output,
		"duration_ms": duration.Milliseconds(),
	})
}

// registerTriggersOnDeploy scans the workflow for trigger blocks and auto-registers them
func (h *AgentHandler) registerTriggersOnDeploy(ctx context.Context, agentID, userID string, workflow *models.Workflow) {
	if workflow == nil {
		return
	}

	for _, block := range workflow.Blocks {
		switch block.Type {
		case "webhook_trigger":
			if h.webhookService == nil {
				log.Printf("⚠️ [DEPLOY] Webhook service not available, skipping webhook registration for agent %s", agentID)
				continue
			}
			method := "POST"
			if m, ok := block.Config["method"].(string); ok && m != "" {
				method = m
			}
			webhook, err := h.webhookService.CreateWebhook(ctx, agentID, userID, method)
			if err != nil {
				log.Printf("⚠️ [DEPLOY] Failed to register webhook for agent %s: %v", agentID, err)
			} else {
				baseURL := GetWebhookBaseURL()
				log.Printf("🔗 [DEPLOY] Webhook registered for agent %s: %s/api/wh/%s", agentID, baseURL, webhook.Path)
			}

		case "schedule_trigger":
			if h.schedulerService == nil {
				log.Printf("⚠️ [DEPLOY] Scheduler service not available, skipping schedule registration for agent %s", agentID)
				continue
			}
			cronExpr, _ := block.Config["cronExpression"].(string)
			if cronExpr == "" {
				log.Printf("⚠️ [DEPLOY] Schedule trigger block has no cron expression for agent %s", agentID)
				continue
			}
			timezone, _ := block.Config["timezone"].(string)
			if timezone == "" {
				timezone = "UTC"
			}
			_, err := h.schedulerService.CreateSchedule(ctx, agentID, userID, &models.CreateScheduleRequest{
				CronExpression: cronExpr,
				Timezone:       timezone,
			})
			if err != nil {
				// Ignore "already has a schedule" — idempotent deploy
				if !strings.Contains(err.Error(), "already has a schedule") {
					log.Printf("⚠️ [DEPLOY] Failed to register schedule for agent %s: %v", agentID, err)
				}
			} else {
				log.Printf("⏰ [DEPLOY] Schedule registered for agent %s: %s (%s)", agentID, cronExpr, timezone)
			}
		}
	}
}

// unregisterTriggersOnUndeploy removes webhook and schedule registrations when agent is undeployed
func (h *AgentHandler) unregisterTriggersOnUndeploy(ctx context.Context, agentID, userID string) {
	if h.webhookService != nil {
		if err := h.webhookService.DeleteByAgentID(ctx, agentID); err != nil {
			log.Printf("⚠️ [UNDEPLOY] Failed to remove webhook for agent %s: %v", agentID, err)
		}
	}

	if h.schedulerService != nil {
		schedule, err := h.schedulerService.GetScheduleByAgentID(ctx, agentID, userID)
		if err == nil && schedule != nil {
			if err := h.schedulerService.DeleteSchedule(ctx, schedule.ID.Hex(), userID); err != nil {
				log.Printf("⚠️ [UNDEPLOY] Failed to remove schedule for agent %s: %v", agentID, err)
			}
		}
	}
}

// GetWebhook returns the webhook configuration for an agent
// GET /api/agents/:id/webhook
func (h *AgentHandler) GetWebhook(c *fiber.Ctx) error {
	userID, ok := c.Locals("user_id").(string)
	if !ok || userID == "" {
		return c.Status(fiber.StatusUnauthorized).JSON(fiber.Map{"error": "Authentication required"})
	}

	agentID := c.Params("id")
	if h.webhookService == nil {
		return c.Status(fiber.StatusServiceUnavailable).JSON(fiber.Map{"error": "Webhook service not available"})
	}

	webhook, err := h.webhookService.GetByAgentID(c.Context(), agentID)
	if err != nil {
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": "Failed to get webhook"})
	}
	if webhook == nil {
		return c.Status(fiber.StatusNotFound).JSON(fiber.Map{"error": "No webhook configured for this agent"})
	}

	// Verify ownership
	if webhook.UserID != userID {
		return c.Status(fiber.StatusForbidden).JSON(fiber.Map{"error": "Access denied"})
	}

	baseURL := GetWebhookBaseURL()
	return c.JSON(webhook.ToResponse(baseURL))
}

// DeleteWebhook removes the webhook for an agent
// DELETE /api/agents/:id/webhook
func (h *AgentHandler) DeleteWebhook(c *fiber.Ctx) error {
	userID, ok := c.Locals("user_id").(string)
	if !ok || userID == "" {
		return c.Status(fiber.StatusUnauthorized).JSON(fiber.Map{"error": "Authentication required"})
	}

	agentID := c.Params("id")
	if h.webhookService == nil {
		return c.Status(fiber.StatusServiceUnavailable).JSON(fiber.Map{"error": "Webhook service not available"})
	}

	// Verify ownership
	webhook, err := h.webhookService.GetByAgentID(c.Context(), agentID)
	if err != nil || webhook == nil {
		return c.Status(fiber.StatusNotFound).JSON(fiber.Map{"error": "No webhook found"})
	}
	if webhook.UserID != userID {
		return c.Status(fiber.StatusForbidden).JSON(fiber.Map{"error": "Access denied"})
	}

	if err := h.webhookService.DeleteByAgentID(c.Context(), agentID); err != nil {
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": "Failed to delete webhook"})
	}

	return c.Status(fiber.StatusNoContent).Send(nil)
}
