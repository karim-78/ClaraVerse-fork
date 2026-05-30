package handlers

import (
	"claraverse/internal/execution"
	"claraverse/internal/models"
	"claraverse/internal/services"
	"context"
	"log"

	"github.com/gofiber/fiber/v2"
	"go.mongodb.org/mongo-driver/bson/primitive"
)

// TriggerHandler handles agent trigger endpoints (API key authenticated)
type TriggerHandler struct {
	agentService     *services.AgentService
	executionService *services.ExecutionService
	workflowEngine   *execution.WorkflowEngine
}

// NewTriggerHandler creates a new trigger handler
func NewTriggerHandler(
	agentService *services.AgentService,
	executionService *services.ExecutionService,
	workflowEngine *execution.WorkflowEngine,
) *TriggerHandler {
	return &TriggerHandler{
		agentService:     agentService,
		executionService: executionService,
		workflowEngine:   workflowEngine,
	}
}

// TriggerAgent executes an agent via API key
// POST /api/trigger/:agentId
func (h *TriggerHandler) TriggerAgent(c *fiber.Ctx) error {
	agentID := c.Params("agentId")
	userID := c.Locals("user_id").(string)

	// Parse request body
	var req models.TriggerAgentRequest
	if err := c.BodyParser(&req); err != nil && err.Error() != "Unprocessable Entity" {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{
			"error": "Invalid request body",
		})
	}

	// Get the agent
	agent, err := h.agentService.GetAgentByID(agentID)
	if err != nil {
		log.Printf("❌ [TRIGGER] Agent not found: %s", agentID)
		return c.Status(fiber.StatusNotFound).JSON(fiber.Map{
			"error": "Agent not found",
		})
	}

	// Verify the agent belongs to the user (API key owner)
	if agent.UserID != userID {
		log.Printf("🚫 [TRIGGER] User %s attempted to trigger agent %s (owned by %s)", userID, agentID, agent.UserID)
		return c.Status(fiber.StatusForbidden).JSON(fiber.Map{
			"error": "You do not have permission to trigger this agent",
		})
	}

	// Check if agent has a workflow
	if agent.Workflow == nil || len(agent.Workflow.Blocks) == 0 {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{
			"error": "Agent has no workflow configured",
		})
	}

	// Get API key ID from context (for tracking)
	var apiKeyID primitive.ObjectID
	if apiKey, ok := c.Locals("api_key").(*models.APIKey); ok {
		apiKeyID = apiKey.ID
	}

	// Create execution record
	execReq := &services.CreateExecutionRequest{
		AgentID:         agentID,
		UserID:          userID,
		WorkflowVersion: agent.Workflow.Version,
		TriggerType:     "api",
		APIKeyID:        apiKeyID,
		Input:           req.Input,
	}

	execRecord, err := h.executionService.Create(c.Context(), execReq)
	if err != nil {
		log.Printf("❌ [TRIGGER] Failed to create execution record: %v", err)
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{
			"error": "Failed to create execution",
		})
	}

	// Update execution status to running
	if err := h.executionService.UpdateStatus(c.Context(), execRecord.ID, "running"); err != nil {
		log.Printf("⚠️ [TRIGGER] Failed to update status: %v", err)
	}

	// Build execution options - block checker is DISABLED for API triggers
	// Block checker should only run during platform testing (WebSocket), not production API calls
	execOpts := &ExecuteWorkflowOptions{
		AgentDescription:   agent.Description,
		EnableBlockChecker: false, // Disabled for API triggers
		CheckerModelID:     req.CheckerModelID,
	}

	// Execute workflow asynchronously (pass userID for credential resolution)
	go h.executeWorkflow(execRecord.ID, agent.Workflow, req.Input, userID, execOpts)

	log.Printf("🚀 [TRIGGER] Triggered agent %s via API (execution: %s)", agentID, execRecord.ID.Hex())

	return c.Status(fiber.StatusAccepted).JSON(models.TriggerAgentResponse{
		ExecutionID: execRecord.ID.Hex(),
		Status:      "running",
		Message:     "Agent execution started",
	})
}

// ExecuteWorkflowOptions contains options for executing a workflow
type ExecuteWorkflowOptions struct {
	AgentDescription   string
	EnableBlockChecker bool
	CheckerModelID     string
}

// executeWorkflow runs the workflow and updates the execution record
func (h *TriggerHandler) executeWorkflow(executionID primitive.ObjectID, workflow *models.Workflow, input map[string]interface{}, userID string, opts *ExecuteWorkflowOptions) {
	ctx := context.Background()

	// Create a channel for status updates (we'll drain it since API triggers don't need real-time)
	statusChan := make(chan models.ExecutionUpdate, 100)
	go func() {
		for range statusChan {
			// Drain channel - future: could publish to Redis for status polling
		}
	}()

	// Transform input to properly wrap file references for Start blocks
	transformedInput := h.transformInputForWorkflow(workflow, input)

	// Inject user context for credential resolution and tool execution
	if transformedInput == nil {
		transformedInput = make(map[string]interface{})
	}
	transformedInput["__user_id__"] = userID

	// Build execution options - block checker DISABLED for API triggers.
	// Forward executionID + userID so durable state checkpoints and orphan
	// recovery work for API-triggered runs too.
	execOptions := &execution.ExecutionOptions{
		EnableBlockChecker: false, // Disabled for API triggers
		ExecutionID:        executionID.Hex(),
		UserID:             userID,
	}
	if opts != nil {
		execOptions.WorkflowGoal = opts.AgentDescription
		execOptions.CheckerModelID = opts.CheckerModelID
	}
	log.Printf("🔍 [TRIGGER] Block checker disabled (API trigger - validation only runs during platform testing)")

	// Execute the workflow
	result, err := h.workflowEngine.ExecuteWithOptions(ctx, workflow, transformedInput, statusChan, execOptions)
	close(statusChan)

	// Update execution record
	completeReq := &services.ExecutionCompleteRequest{
		Status: "completed",
	}

	if err != nil {
		completeReq.Status = "failed"
		completeReq.Error = err.Error()
		log.Printf("❌ [TRIGGER] Execution %s failed: %v", executionID.Hex(), err)
	} else {
		completeReq.Status = result.Status
		completeReq.Output = result.Output
		completeReq.BlockStates = result.BlockStates
		if result.Error != "" {
			completeReq.Error = result.Error
		}
		log.Printf("✅ [TRIGGER] Execution %s completed with status: %s", executionID.Hex(), result.Status)
	}

	if err := h.executionService.Complete(ctx, executionID, completeReq); err != nil {
		log.Printf("⚠️ [TRIGGER] Failed to complete execution record: %v", err)
	}
}

// GetExecutionStatus gets the status of an execution
// GET /api/trigger/status/:executionId
func (h *TriggerHandler) GetExecutionStatus(c *fiber.Ctx) error {
	executionIDStr := c.Params("executionId")
	userID := c.Locals("user_id").(string)

	executionID, err := primitive.ObjectIDFromHex(executionIDStr)
	if err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{
			"error": "Invalid execution ID",
		})
	}

	execution, err := h.executionService.GetByIDAndUser(c.Context(), executionID, userID)
	if err != nil {
		if err.Error() == "execution not found" {
			return c.Status(fiber.StatusNotFound).JSON(fiber.Map{
				"error": "Execution not found",
			})
		}
		log.Printf("❌ [TRIGGER] Failed to get execution: %v", err)
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{
			"error": "Failed to get execution status",
		})
	}

	return c.JSON(execution)
}

// transformInputForWorkflow transforms API input to match workflow expectations
// This handles the case where file reference fields (file_id, filename, mime_type)
// are passed directly in input but the Start block expects them nested under a variable name
func (h *TriggerHandler) transformInputForWorkflow(workflow *models.Workflow, input map[string]interface{}) map[string]interface{} {
	if workflow == nil || input == nil {
		return input
	}

	// Find the Start block (variable block with operation "read")
	var startBlockVariableName string
	var startBlockInputType string

	for _, block := range workflow.Blocks {
		if block.Type == "variable" {
			config := block.Config

			operation, _ := config["operation"].(string)
			if operation != "read" {
				continue
			}

			// Found a Start block - get its variable name and input type
			if varName, ok := config["variableName"].(string); ok {
				startBlockVariableName = varName
			}
			if inputType, ok := config["inputType"].(string); ok {
				startBlockInputType = inputType
			}
			break
		}
	}

	// If no Start block found, return input as-is
	if startBlockVariableName == "" {
		return input
	}

	log.Printf("🔧 [TRIGGER] Found Start block: variableName=%s, inputType=%s", startBlockVariableName, startBlockInputType)

	// Check if input contains file reference fields at top level
	_, hasFileID := input["file_id"]
	_, hasFilename := input["filename"]
	_, hasMimeType := input["mime_type"]

	isFileReferenceInput := hasFileID && (hasFilename || hasMimeType)

	// If this is a file input type and we have file reference fields at top level,
	// wrap them under the Start block's variable name
	if startBlockInputType == "file" && isFileReferenceInput {
		log.Printf("🔧 [TRIGGER] Wrapping file reference fields under variable '%s'", startBlockVariableName)

		fileRef := map[string]interface{}{
			"file_id": input["file_id"],
		}
		if hasFilename {
			fileRef["filename"] = input["filename"]
		}
		if hasMimeType {
			fileRef["mime_type"] = input["mime_type"]
		}

		// Create new input with file reference wrapped
		newInput := make(map[string]interface{})

		// Copy non-file-reference fields
		for k, v := range input {
			if k != "file_id" && k != "filename" && k != "mime_type" {
				newInput[k] = v
			}
		}

		// Add wrapped file reference
		newInput[startBlockVariableName] = fileRef

		log.Printf("✅ [TRIGGER] Transformed input: %+v", newInput)
		return newInput
	}

	// For text inputs, check if the variable name doesn't exist but we have a single text value
	if startBlockInputType == "text" || startBlockInputType == "" {
		// If the variable already exists in input, no transformation needed
		if _, exists := input[startBlockVariableName]; exists {
			return input
		}

		// If input has a single "text", "value", or "message" field, map it to the variable name
		if text, ok := input["text"].(string); ok {
			newInput := make(map[string]interface{})
			for k, v := range input {
				newInput[k] = v
			}
			newInput[startBlockVariableName] = text
			log.Printf("🔧 [TRIGGER] Mapped 'text' field to variable '%s'", startBlockVariableName)
			return newInput
		}
		if value, ok := input["value"].(string); ok {
			newInput := make(map[string]interface{})
			for k, v := range input {
				newInput[k] = v
			}
			newInput[startBlockVariableName] = value
			log.Printf("🔧 [TRIGGER] Mapped 'value' field to variable '%s'", startBlockVariableName)
			return newInput
		}
		if message, ok := input["message"].(string); ok {
			newInput := make(map[string]interface{})
			for k, v := range input {
				newInput[k] = v
			}
			newInput[startBlockVariableName] = message
			log.Printf("🔧 [TRIGGER] Mapped 'message' field to variable '%s'", startBlockVariableName)
			return newInput
		}
	}

	return input
}
