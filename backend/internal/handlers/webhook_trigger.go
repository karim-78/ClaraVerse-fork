package handlers

import (
	"claraverse/internal/execution"
	"claraverse/internal/models"
	"claraverse/internal/services"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"log"
	"os"
	"strings"

	"github.com/gofiber/fiber/v2"
	"go.mongodb.org/mongo-driver/bson/primitive"
)

// WebhookTriggerHandler handles incoming webhook requests that trigger agent workflows
type WebhookTriggerHandler struct {
	webhookService   *services.WebhookService
	agentService     *services.AgentService
	executionService *services.ExecutionService
	workflowEngine   *execution.WorkflowEngine
}

// NewWebhookTriggerHandler creates a new webhook trigger handler
func NewWebhookTriggerHandler(
	webhookService *services.WebhookService,
	agentService *services.AgentService,
	executionService *services.ExecutionService,
	workflowEngine *execution.WorkflowEngine,
) *WebhookTriggerHandler {
	return &WebhookTriggerHandler{
		webhookService:   webhookService,
		agentService:     agentService,
		executionService: executionService,
		workflowEngine:   workflowEngine,
	}
}

// HandleIncoming processes an incoming webhook request and triggers the associated agent workflow
// ALL /api/wh/:path — public, no auth required
func (h *WebhookTriggerHandler) HandleIncoming(c *fiber.Ctx) error {
	path := c.Params("path")
	if path == "" {
		return c.Status(fiber.StatusNotFound).JSON(fiber.Map{"error": "webhook not found"})
	}

	// Look up webhook by path
	webhook, err := h.webhookService.GetByPath(c.Context(), path)
	if err != nil {
		log.Printf("❌ [WEBHOOK-IN] Error looking up path %s: %v", path, err)
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": "internal error"})
	}
	if webhook == nil {
		return c.Status(fiber.StatusNotFound).JSON(fiber.Map{"error": "webhook not found"})
	}

	if !webhook.Enabled {
		return c.Status(fiber.StatusGone).JSON(fiber.Map{"error": "webhook is disabled"})
	}

	// Verify HMAC signature if secret is set
	if webhook.Secret != "" {
		signature := c.Get("X-Webhook-Signature")
		if signature == "" {
			signature = c.Get("X-Hub-Signature-256") // GitHub-style header
		}
		if !verifyHMACSignature(c.Body(), webhook.Secret, signature) {
			log.Printf("🚫 [WEBHOOK-IN] Invalid signature for webhook %s (agent: %s)", path, webhook.AgentID)
			return c.Status(fiber.StatusUnauthorized).JSON(fiber.Map{"error": "invalid signature"})
		}
	}

	// Get the agent
	agent, err := h.agentService.GetAgentByID(webhook.AgentID)
	if err != nil {
		log.Printf("❌ [WEBHOOK-IN] Agent not found: %s", webhook.AgentID)
		return c.Status(fiber.StatusNotFound).JSON(fiber.Map{"error": "agent not found"})
	}

	if agent.Workflow == nil || len(agent.Workflow.Blocks) == 0 {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "agent has no workflow"})
	}

	// Parse request body
	var parsedBody any
	if len(c.Body()) > 0 {
		if err := json.Unmarshal(c.Body(), &parsedBody); err != nil {
			// Not JSON — treat as raw string
			parsedBody = string(c.Body())
		}
	}

	// Collect request headers
	reqHeaders := make(map[string]string)
	c.Request().Header.VisitAll(func(key, value []byte) {
		reqHeaders[strings.ToLower(string(key))] = string(value)
	})

	// Collect query params
	queryParams := make(map[string]string)
	c.Request().URI().QueryArgs().VisitAll(func(key, value []byte) {
		queryParams[string(key)] = string(value)
	})

	// Build workflow input
	input := map[string]interface{}{
		"body":        parsedBody,
		"headers":     reqHeaders,
		"method":      c.Method(),
		"path":        path,
		"query":       queryParams,
		"triggerType": "webhook",
		"__user_id__": webhook.UserID, // for credential resolution
	}

	// Create execution record
	var execID primitive.ObjectID
	if h.executionService != nil {
		execRecord, err := h.executionService.Create(c.Context(), &services.CreateExecutionRequest{
			AgentID:         webhook.AgentID,
			UserID:          webhook.UserID,
			WorkflowVersion: agent.Workflow.Version,
			TriggerType:     "webhook",
			Input:           input,
		})
		if err != nil {
			log.Printf("⚠️ [WEBHOOK-IN] Failed to create execution record: %v", err)
		} else {
			execID = execRecord.ID
			h.executionService.UpdateStatus(c.Context(), execID, "running")
		}
	}

	// Determine response mode from webhook trigger block config
	responseMode := "trigger_only"
	responseTemplate := ""
	for _, block := range agent.Workflow.Blocks {
		if block.Type == "webhook_trigger" {
			if mode, ok := block.Config["responseMode"].(string); ok && mode != "" {
				responseMode = mode
			}
			if tmpl, ok := block.Config["responseTemplate"].(string); ok {
				responseTemplate = tmpl
			}
			break
		}
	}

	log.Printf("🔗 [WEBHOOK-IN] Triggered agent %s via webhook /%s (execution: %s, mode: %s)", webhook.AgentID, path, execID.Hex(), responseMode)

	if responseMode == "respond_with_result" {
		return h.executeSyncWorkflow(c, execID, agent.Workflow, input, webhook.UserID, responseTemplate)
	}

	// Default: trigger_only — execute asynchronously
	go h.executeWorkflow(execID, agent.Workflow, input, webhook.UserID)

	return c.Status(fiber.StatusAccepted).JSON(fiber.Map{
		"executionId": execID.Hex(),
		"status":      "running",
		"message":     "Webhook received, workflow execution started",
	})
}

// executeWorkflow runs the workflow and updates the execution record
func (h *WebhookTriggerHandler) executeWorkflow(executionID primitive.ObjectID, workflow *models.Workflow, input map[string]interface{}, userID string) {
	ctx := context.Background()

	// Create a drain channel for status updates
	statusChan := make(chan models.ExecutionUpdate, 100)
	go func() {
		for range statusChan {
			// Drain — future: publish to Redis pub/sub for real-time monitoring
		}
	}()

	// Execute. Forward executionID + userID so the durable state store
	// can checkpoint, dedupe retries, and resume after a crash.
	execOptions := &execution.ExecutionOptions{
		EnableBlockChecker: false, // Disabled for webhook triggers
		ExecutionID:        executionID.Hex(),
		UserID:             userID,
	}

	result, err := h.workflowEngine.ExecuteWithOptions(ctx, workflow, input, statusChan, execOptions)
	close(statusChan)

	// Update execution record
	if h.executionService != nil && !executionID.IsZero() {
		completeReq := &services.ExecutionCompleteRequest{
			Status: "completed",
		}
		if err != nil {
			completeReq.Status = "failed"
			completeReq.Error = err.Error()
			log.Printf("❌ [WEBHOOK-IN] Execution %s failed: %v", executionID.Hex(), err)
		} else {
			completeReq.Status = result.Status
			completeReq.Output = result.Output
			completeReq.BlockStates = result.BlockStates
			if result.Error != "" {
				completeReq.Error = result.Error
			}
			log.Printf("✅ [WEBHOOK-IN] Execution %s completed: %s", executionID.Hex(), result.Status)
		}

		if err := h.executionService.Complete(ctx, executionID, completeReq); err != nil {
			log.Printf("⚠️ [WEBHOOK-IN] Failed to complete execution record: %v", err)
		}
	}
}

// executeSyncWorkflow runs the workflow synchronously and returns the result as the HTTP response.
// If a responseTemplate is provided, resolves {{block-name.field}} placeholders against block outputs.
func (h *WebhookTriggerHandler) executeSyncWorkflow(c *fiber.Ctx, executionID primitive.ObjectID, workflow *models.Workflow, input map[string]interface{}, userID string, responseTemplate string) error {
	ctx := context.Background()

	statusChan := make(chan models.ExecutionUpdate, 100)
	go func() {
		for range statusChan {
		}
	}()

	execOptions := &execution.ExecutionOptions{
		EnableBlockChecker: false,
		ExecutionID:        executionID.Hex(),
		UserID:             userID,
	}

	result, err := h.workflowEngine.ExecuteWithOptions(ctx, workflow, input, statusChan, execOptions)
	close(statusChan)

	// Per-workflow concurrency cap: convert limiter rejection into a 429
	// with Retry-After so the upstream client backs off rather than retrying
	// in a tight loop.
	if limErr, ok := err.(*execution.LimiterError); ok {
		c.Set("Retry-After", fmt.Sprintf("%d", int(limErr.RetryAfter.Seconds())))
		return c.Status(fiber.StatusTooManyRequests).JSON(fiber.Map{
			"error":  "workflow at concurrency cap",
			"detail": limErr.Error(),
		})
	}

	// Update execution record
	if h.executionService != nil && !executionID.IsZero() {
		completeReq := &services.ExecutionCompleteRequest{
			Status: "completed",
		}
		if err != nil {
			completeReq.Status = "failed"
			completeReq.Error = err.Error()
		} else {
			completeReq.Status = result.Status
			completeReq.Output = result.Output
			completeReq.BlockStates = result.BlockStates
			if result.Error != "" {
				completeReq.Error = result.Error
			}
		}
		if completeErr := h.executionService.Complete(context.Background(), executionID, completeReq); completeErr != nil {
			log.Printf("⚠️ [WEBHOOK-SYNC] Failed to complete execution record: %v", completeErr)
		}
	}

	// Handle execution error
	if err != nil {
		log.Printf("❌ [WEBHOOK-SYNC] Execution %s failed: %v", executionID.Hex(), err)
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{
			"error":       err.Error(),
			"executionId": executionID.Hex(),
		})
	}

	log.Printf("✅ [WEBHOOK-SYNC] Execution %s completed: %s", executionID.Hex(), result.Status)

	// Build response body
	if responseTemplate != "" {
		// Build template inputs from block states: { "block-name": outputs, ... }
		templateInputs := make(map[string]any)
		blockIndex := make(map[string]models.Block)
		for _, block := range workflow.Blocks {
			blockIndex[block.ID] = block
		}
		for blockID, state := range result.BlockStates {
			block, ok := blockIndex[blockID]
			if !ok || state.Outputs == nil {
				continue
			}
			if block.NormalizedID != "" {
				templateInputs[block.NormalizedID] = state.Outputs
			}
			if block.Name != "" {
				templateInputs[block.Name] = state.Outputs
			}
		}

		resolved := execution.InterpolateTemplate(responseTemplate, templateInputs)

		// Try to parse as JSON so we return structured data
		var jsonBody any
		if err := json.Unmarshal([]byte(resolved), &jsonBody); err == nil {
			return c.Status(fiber.StatusOK).JSON(jsonBody)
		}
		// Not valid JSON — return as plain text
		c.Set("Content-Type", "text/plain")
		return c.Status(fiber.StatusOK).SendString(resolved)
	}

	// No template — return terminal block outputs directly
	return c.Status(fiber.StatusOK).JSON(fiber.Map{
		"executionId": executionID.Hex(),
		"status":      result.Status,
		"output":      result.Output,
	})
}

// verifyHMACSignature verifies an HMAC-SHA256 signature
func verifyHMACSignature(body []byte, secret, signature string) bool {
	if signature == "" {
		return false
	}

	// Strip "sha256=" prefix if present (GitHub-style)
	signature = strings.TrimPrefix(signature, "sha256=")

	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write(body)
	expected := hex.EncodeToString(mac.Sum(nil))

	return hmac.Equal([]byte(expected), []byte(signature))
}

// GetExecutionStatus returns the status of a webhook-triggered execution (public, no auth).
// GET /api/wh/status/:executionId
func (h *WebhookTriggerHandler) GetExecutionStatus(c *fiber.Ctx) error {
	executionIDStr := c.Params("executionId")

	executionID, err := primitive.ObjectIDFromHex(executionIDStr)
	if err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "Invalid execution ID"})
	}

	record, err := h.executionService.GetByID(c.Context(), executionID)
	if err != nil {
		if err.Error() == "execution not found" {
			return c.Status(fiber.StatusNotFound).JSON(fiber.Map{"error": "Execution not found"})
		}
		log.Printf("❌ [WEBHOOK-STATUS] Failed to get execution: %v", err)
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": "Failed to get execution status"})
	}

	// Only allow querying webhook-triggered executions via this public endpoint
	if record.TriggerType != "webhook" {
		return c.Status(fiber.StatusNotFound).JSON(fiber.Map{"error": "Execution not found"})
	}

	// Return limited info (no blockStates or internal details)
	return c.JSON(fiber.Map{
		"executionId": record.ID.Hex(),
		"status":      record.Status,
		"output":      record.Output,
		"error":       record.Error,
		"startedAt":   record.StartedAt,
		"completedAt": record.CompletedAt,
		"durationMs":  record.DurationMs,
	})
}

// GetWebhookBaseURL returns the base URL for webhook endpoints
func GetWebhookBaseURL() string {
	if url := os.Getenv("WEBHOOK_BASE_URL"); url != "" {
		return strings.TrimRight(url, "/")
	}
	if url := os.Getenv("BASE_URL"); url != "" {
		return strings.TrimRight(url, "/")
	}
	return "http://localhost:8080"
}
