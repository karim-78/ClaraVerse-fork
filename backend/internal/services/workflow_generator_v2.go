package services

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"strings"
	"time"

	"github.com/google/uuid"

	"claraverse/internal/database"
	"claraverse/internal/models"
)

// WorkflowGeneratorV2Service handles multi-step workflow generation
type WorkflowGeneratorV2Service struct {
	db              *database.DB
	providerService *ProviderService
	chatService     *ChatService
}

// NewWorkflowGeneratorV2Service creates a new v2 workflow generator service
func NewWorkflowGeneratorV2Service(
	db *database.DB,
	providerService *ProviderService,
	chatService *ChatService,
) *WorkflowGeneratorV2Service {
	return &WorkflowGeneratorV2Service{
		db:              db,
		providerService: providerService,
		chatService:     chatService,
	}
}

// ToolSelectionResult represents the result of tool selection (Step 1)
type ToolSelectionResult struct {
	SelectedTools []SelectedTool `json:"selected_tools"`
	Reasoning     string         `json:"reasoning"`
}

// SelectedTool represents a selected tool with reasoning
type SelectedTool struct {
	ToolID   string `json:"tool_id"`
	Category string `json:"category"`
	Reason   string `json:"reason"`
}

// GenerationStep represents a step in the generation process
type GenerationStep struct {
	StepNumber  int      `json:"step_number"`
	StepName    string   `json:"step_name"`
	Status      string   `json:"status"` // "pending", "running", "completed", "failed"
	Description string   `json:"description"`
	Tools       []string `json:"tools,omitempty"` // Tool IDs for step 1 result
}

// MultiStepGenerateRequest is the request for multi-step generation
type MultiStepGenerateRequest struct {
	AgentID             string                      `json:"agent_id"`
	UserMessage         string                      `json:"user_message"`
	ModelID             string                      `json:"model_id,omitempty"`
	CurrentWorkflow     *models.Workflow            `json:"current_workflow,omitempty"`
	ConversationHistory []models.ConversationMessage `json:"conversation_history,omitempty"` // Recent conversation context for better tool selection
}

// MultiStepGenerateResponse is the response for multi-step generation
type MultiStepGenerateResponse struct {
	Success        bool              `json:"success"`
	CurrentStep    int               `json:"current_step"`
	TotalSteps     int               `json:"total_steps"`
	Steps          []GenerationStep  `json:"steps"`
	SelectedTools  []SelectedTool    `json:"selected_tools,omitempty"`
	Workflow       *models.Workflow  `json:"workflow,omitempty"`
	Explanation    string            `json:"explanation,omitempty"`
	Error          string            `json:"error,omitempty"`
	StepInProgress *GenerationStep   `json:"step_in_progress,omitempty"`
}

// Tool selection system prompt - asks LLM to select relevant tools
const ToolSelectionSystemPrompt = `You are a tool selection expert for ClaraVerse workflow builder. Your job is to analyze user requests and select the MINIMUM set of tools needed to accomplish the task.

IMPORTANT: Only select tools that are DIRECTLY needed for the workflow. Don't over-select.

You will be given:
1. A user request describing what workflow they want to build
2. A list of all available tools with their descriptions and use cases

Your task: Select the specific tools needed and explain why each is needed.

Rules:
- Select ONLY tools that will be directly used in the workflow
- If the request mentions "news" or time-sensitive info, ALWAYS include "get_current_time"
- If the request mentions sending to a specific platform (Discord, Slack, etc.), select that messaging tool
- Don't select redundant tools - if search_web is enough, don't also select scrape_web unless needed
- For file processing, select the appropriate reader tool based on file type mentioned

Output format: JSON with selected_tools array and reasoning.`

// BuildToolSelectionUserPrompt builds the user prompt for tool selection
func (s *WorkflowGeneratorV2Service) BuildToolSelectionUserPrompt(userMessage string) string {
	var builder strings.Builder

	builder.WriteString("USER REQUEST:\n")
	builder.WriteString(userMessage)
	builder.WriteString("\n\n")
	builder.WriteString("AVAILABLE TOOLS:\n\n")

	// Group tools by category
	for _, category := range ToolCategoryRegistry {
		tools := GetToolsByCategory(category.ID)
		if len(tools) == 0 {
			continue
		}

		builder.WriteString(fmt.Sprintf("## %s\n", category.Name))
		for _, tool := range tools {
			builder.WriteString(fmt.Sprintf("- **%s**: %s\n", tool.ID, tool.Description))
			if len(tool.UseCases) > 0 {
				builder.WriteString(fmt.Sprintf("  Use cases: %s\n", strings.Join(tool.UseCases, ", ")))
			}
		}
		builder.WriteString("\n")
	}

	builder.WriteString("\nSelect the tools needed for this workflow. Return JSON with selected_tools array.")

	return builder.String()
}

// Tool selection JSON schema for structured output
var toolSelectionSchema = map[string]interface{}{
	"type": "object",
	"properties": map[string]interface{}{
		"selected_tools": map[string]interface{}{
			"type": "array",
			"items": map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"tool_id": map[string]interface{}{
						"type":        "string",
						"description": "The tool ID from the available tools list",
					},
					"category": map[string]interface{}{
						"type":        "string",
						"description": "The category of the tool",
					},
					"reason": map[string]interface{}{
						"type":        "string",
						"description": "Brief reason why this tool is needed",
					},
				},
				"required":             []string{"tool_id", "category", "reason"},
				"additionalProperties": false,
			},
		},
		"reasoning": map[string]interface{}{
			"type":        "string",
			"description": "Overall reasoning for the tool selection",
		},
	},
	"required":             []string{"selected_tools", "reasoning"},
	"additionalProperties": false,
}

// Step1SelectTools performs tool selection using structured output
func (s *WorkflowGeneratorV2Service) Step1SelectTools(req *MultiStepGenerateRequest, userID string) (*ToolSelectionResult, error) {
	log.Printf("🔧 [WORKFLOW-GEN-V2] Step 1: Selecting tools for request: %s", req.UserMessage)

	// Get provider and model
	provider, modelID, err := s.getProviderAndModel(req.ModelID)
	if err != nil {
		return nil, fmt.Errorf("failed to get provider: %w", err)
	}

	// Build the user prompt with all available tools
	userPrompt := s.BuildToolSelectionUserPrompt(req.UserMessage)

	// Build messages with conversation history for better context
	messages := []map[string]interface{}{
		{
			"role":    "system",
			"content": ToolSelectionSystemPrompt,
		},
	}

	// Add conversation history if provided (for multi-turn context)
	if len(req.ConversationHistory) > 0 {
		for _, msg := range req.ConversationHistory {
			messages = append(messages, map[string]interface{}{
				"role":    msg.Role,
				"content": msg.Content,
			})
		}
	}

	// Add current user message
	messages = append(messages, map[string]interface{}{
		"role":    "user",
		"content": userPrompt,
	})

	// Build request with structured output
	requestBody := map[string]interface{}{
		"model":       modelID,
		"messages":    messages,
		"stream":      false,
		"temperature": 0.2, // Low temperature for consistent selection
		"response_format": map[string]interface{}{
			"type": "json_schema",
			"json_schema": map[string]interface{}{
				"name":   "tool_selection",
				"strict": true,
				"schema": toolSelectionSchema,
			},
		},
	}

	reqBody, err := json.Marshal(requestBody)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal request: %w", err)
	}

	log.Printf("📤 [WORKFLOW-GEN-V2] Sending tool selection request to %s", provider.BaseURL)

	// Create HTTP request
	httpReq, err := http.NewRequest("POST", provider.BaseURL+"/chat/completions", bytes.NewBuffer(reqBody))
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}

	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("Authorization", "Bearer "+provider.APIKey)

	// Send request
	client := &http.Client{Timeout: 60 * time.Second}
	resp, err := client.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("request failed: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("failed to read response: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		log.Printf("⚠️ [WORKFLOW-GEN-V2] API error: %s", string(body))
		return nil, fmt.Errorf("API error (status %d): %s", resp.StatusCode, string(body))
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
		return nil, fmt.Errorf("failed to parse API response: %w", err)
	}

	if len(apiResponse.Choices) == 0 {
		return nil, fmt.Errorf("no response from model")
	}

	// Parse the tool selection result
	var result ToolSelectionResult
	content := apiResponse.Choices[0].Message.Content

	if err := json.Unmarshal([]byte(content), &result); err != nil {
		log.Printf("⚠️ [WORKFLOW-GEN-V2] Failed to parse tool selection: %v, content: %s", err, content)
		return nil, fmt.Errorf("failed to parse tool selection: %w", err)
	}

	// Validate selected tools exist
	validTools := make([]SelectedTool, 0)
	for _, selected := range result.SelectedTools {
		if tool := GetToolByID(selected.ToolID); tool != nil {
			selected.Category = tool.Category // Ensure category is correct
			validTools = append(validTools, selected)
		} else {
			log.Printf("⚠️ [WORKFLOW-GEN-V2] Unknown tool selected: %s, skipping", selected.ToolID)
		}
	}
	result.SelectedTools = validTools

	log.Printf("✅ [WORKFLOW-GEN-V2] Selected %d tools: %v", len(result.SelectedTools), getToolIDs(result.SelectedTools))

	return &result, nil
}

// Step2GenerateWorkflow generates the workflow using only selected tools
func (s *WorkflowGeneratorV2Service) Step2GenerateWorkflow(
	req *MultiStepGenerateRequest,
	selectedTools []SelectedTool,
	userID string,
) (*models.WorkflowGenerateResponse, error) {
	log.Printf("🔧 [WORKFLOW-GEN-V2] Step 2: Generating workflow with %d tools", len(selectedTools))

	// Get provider and model
	provider, modelID, err := s.getProviderAndModel(req.ModelID)
	if err != nil {
		return &models.WorkflowGenerateResponse{
			Success: false,
			Error:   fmt.Sprintf("Failed to get provider: %v", err),
		}, nil
	}

	// Build tool IDs list
	toolIDs := getToolIDs(selectedTools)

	// Build system prompt with only selected tools
	systemPrompt := s.buildWorkflowSystemPromptWithTools(toolIDs)

	// Build user message
	userMessage := s.buildUserMessage(req)

	// Build messages with conversation history for better context
	messages := []map[string]interface{}{
		{
			"role":    "system",
			"content": systemPrompt,
		},
	}

	// Add conversation history if provided (for multi-turn context)
	if len(req.ConversationHistory) > 0 {
		for _, msg := range req.ConversationHistory {
			messages = append(messages, map[string]interface{}{
				"role":    msg.Role,
				"content": msg.Content,
			})
		}
	}

	// Add current user message
	messages = append(messages, map[string]interface{}{
		"role":    "user",
		"content": userMessage,
	})

	// Build request
	requestBody := map[string]interface{}{
		"model":       modelID,
		"messages":    messages,
		"stream":      false,
		"temperature": 0.3,
		"response_format": map[string]interface{}{
			"type": "json_object",
		},
	}

	reqBody, err := json.Marshal(requestBody)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal request: %w", err)
	}

	log.Printf("📤 [WORKFLOW-GEN-V2] Sending workflow generation request")

	// Create HTTP request
	httpReq, err := http.NewRequest("POST", provider.BaseURL+"/chat/completions", bytes.NewBuffer(reqBody))
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}

	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("Authorization", "Bearer "+provider.APIKey)

	// Send request
	client := &http.Client{Timeout: 120 * time.Second}
	resp, err := client.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("request failed: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("failed to read response: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		log.Printf("⚠️ [WORKFLOW-GEN-V2] API error: %s", string(body))
		return &models.WorkflowGenerateResponse{
			Success: false,
			Error:   fmt.Sprintf("API error (status %d): %s", resp.StatusCode, string(body)),
		}, nil
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
		return nil, fmt.Errorf("failed to parse API response: %w", err)
	}

	if len(apiResponse.Choices) == 0 {
		return &models.WorkflowGenerateResponse{
			Success: false,
			Error:   "No response from model",
		}, nil
	}

	content := apiResponse.Choices[0].Message.Content
	log.Printf("📥 [WORKFLOW-GEN-V2] Received workflow response (%d chars)", len(content))

	// Parse the workflow
	return s.parseWorkflowResponse(content, req.CurrentWorkflow != nil, req.AgentID)
}

// GenerateWorkflowMultiStep performs the full multi-step generation
func (s *WorkflowGeneratorV2Service) GenerateWorkflowMultiStep(
	req *MultiStepGenerateRequest,
	userID string,
	stepCallback func(step GenerationStep),
) (*MultiStepGenerateResponse, error) {
	response := &MultiStepGenerateResponse{
		TotalSteps: 2,
		Steps: []GenerationStep{
			{StepNumber: 1, StepName: "Tool Selection", Status: "pending", Description: "Analyzing request and selecting relevant tools"},
			{StepNumber: 2, StepName: "Workflow Generation", Status: "pending", Description: "Building the workflow with selected tools"},
		},
	}

	// Step 1: Tool Selection
	response.Steps[0].Status = "running"
	response.CurrentStep = 1
	response.StepInProgress = &response.Steps[0]
	if stepCallback != nil {
		stepCallback(response.Steps[0])
	}

	toolResult, err := s.Step1SelectTools(req, userID)
	if err != nil {
		response.Steps[0].Status = "failed"
		response.Success = false
		response.Error = fmt.Sprintf("Tool selection failed: %v", err)
		return response, nil
	}

	response.Steps[0].Status = "completed"
	response.Steps[0].Tools = getToolIDs(toolResult.SelectedTools)
	response.SelectedTools = toolResult.SelectedTools

	if stepCallback != nil {
		stepCallback(response.Steps[0])
	}

	// Step 2: Workflow Generation
	response.Steps[1].Status = "running"
	response.CurrentStep = 2
	response.StepInProgress = &response.Steps[1]
	if stepCallback != nil {
		stepCallback(response.Steps[1])
	}

	workflowResult, err := s.Step2GenerateWorkflow(req, toolResult.SelectedTools, userID)
	if err != nil {
		response.Steps[1].Status = "failed"
		response.Success = false
		response.Error = fmt.Sprintf("Workflow generation failed: %v", err)
		return response, nil
	}

	if !workflowResult.Success {
		response.Steps[1].Status = "failed"
		response.Success = false
		response.Error = workflowResult.Error
		return response, nil
	}

	response.Steps[1].Status = "completed"
	response.Workflow = workflowResult.Workflow
	response.Explanation = workflowResult.Explanation
	response.Success = true
	response.StepInProgress = nil

	if stepCallback != nil {
		stepCallback(response.Steps[1])
	}

	log.Printf("✅ [WORKFLOW-GEN-V2] Multi-step generation completed successfully")

	return response, nil
}

// buildWorkflowSystemPromptWithTools builds the system prompt with specific tools
func (s *WorkflowGeneratorV2Service) buildWorkflowSystemPromptWithTools(toolIDs []string) string {
	toolsSection := BuildToolPromptSection(toolIDs)
	return strings.Replace(WorkflowSystemPromptBase, "{{DYNAMIC_TOOLS_SECTION}}", toolsSection, 1)
}

// buildUserMessage constructs the user message for workflow generation
func (s *WorkflowGeneratorV2Service) buildUserMessage(req *MultiStepGenerateRequest) string {
	if req.CurrentWorkflow != nil && len(req.CurrentWorkflow.Blocks) > 0 {
		workflowJSON, _ := json.MarshalIndent(req.CurrentWorkflow, "", "  ")
		return fmt.Sprintf(`MODIFICATION REQUEST

Current workflow:
%s

User request: %s

Output the complete modified workflow JSON with all blocks (not just changes).`, string(workflowJSON), req.UserMessage)
	}

	return fmt.Sprintf("CREATE NEW WORKFLOW\n\nUser request: %s", req.UserMessage)
}

// parseWorkflowResponse parses the LLM response into a workflow
func (s *WorkflowGeneratorV2Service) parseWorkflowResponse(content string, isModification bool, agentID string) (*models.WorkflowGenerateResponse, error) {
	// Try to extract JSON from the response
	jsonContent := extractJSON(content)

	// Parse the workflow
	var workflowData struct {
		Blocks      []models.Block      `json:"blocks"`
		Connections []models.Connection `json:"connections"`
		Variables   []models.Variable   `json:"variables"`
		Explanation string              `json:"explanation"`
	}

	if err := json.Unmarshal([]byte(jsonContent), &workflowData); err != nil {
		log.Printf("⚠️ [WORKFLOW-GEN-V2] Failed to parse workflow JSON: %v", err)
		return &models.WorkflowGenerateResponse{
			Success:     false,
			Error:       fmt.Sprintf("Failed to parse workflow JSON: %v", err),
			Explanation: content,
		}, nil
	}

	// Log the generated workflow for debugging
	prettyWorkflow, _ := json.MarshalIndent(workflowData, "", "  ")
	log.Printf("📋 [WORKFLOW-GEN-V2] Generated workflow:\n%s", string(prettyWorkflow))

	// Post-process blocks
	for i := range workflowData.Blocks {
		if workflowData.Blocks[i].NormalizedID == "" {
			workflowData.Blocks[i].NormalizedID = workflowData.Blocks[i].ID
		}
	}

	// Determine action
	action := "create"
	if isModification {
		action = "modify"
	}

	// Build the workflow
	workflow := &models.Workflow{
		ID:          uuid.New().String(),
		AgentID:     agentID,
		Blocks:      workflowData.Blocks,
		Connections: workflowData.Connections,
		Variables:   workflowData.Variables,
		Version:     1,
	}

	log.Printf("✅ [WORKFLOW-GEN-V2] Parsed workflow: %d blocks, %d connections",
		len(workflow.Blocks), len(workflow.Connections))

	return &models.WorkflowGenerateResponse{
		Success:     true,
		Workflow:    workflow,
		Explanation: workflowData.Explanation,
		Action:      action,
		Version:     1,
	}, nil
}

// getProviderAndModel gets the provider and model for the request
func (s *WorkflowGeneratorV2Service) getProviderAndModel(modelID string) (*models.Provider, string, error) {
	if modelID == "" {
		return s.chatService.GetDefaultProviderWithModel()
	}

	// Try to find the model in the database
	var providerID int
	var modelName string

	err := s.db.QueryRow(`
		SELECT m.name, m.provider_id
		FROM models m
		WHERE m.id = ? AND m.is_visible = 1
	`, modelID).Scan(&modelName, &providerID)

	if err != nil {
		if provider, actualModel, found := s.chatService.ResolveModelAlias(modelID); found {
			return provider, actualModel, nil
		}
		return s.chatService.GetDefaultProviderWithModel()
	}

	provider, err := s.providerService.GetByID(providerID)
	if err != nil {
		return nil, "", fmt.Errorf("failed to get provider: %w", err)
	}

	return provider, modelName, nil
}

// Helper function to get tool IDs from selected tools
func getToolIDs(tools []SelectedTool) []string {
	ids := make([]string, len(tools))
	for i, t := range tools {
		ids[i] = t.ToolID
	}
	return ids
}

// extractJSON extracts JSON from a response (handles markdown code blocks)
func extractJSON(content string) string {
	content = strings.TrimSpace(content)

	if strings.HasPrefix(content, "{") {
		return content
	}

	// Try to extract from markdown code block
	if idx := strings.Index(content, "```json"); idx != -1 {
		start := idx + 7
		end := strings.Index(content[start:], "```")
		if end != -1 {
			return strings.TrimSpace(content[start : start+end])
		}
	}

	if idx := strings.Index(content, "```"); idx != -1 {
		start := idx + 3
		// Skip language identifier if present
		if newline := strings.Index(content[start:], "\n"); newline != -1 {
			start = start + newline + 1
		}
		end := strings.Index(content[start:], "```")
		if end != -1 {
			return strings.TrimSpace(content[start : start+end])
		}
	}

	// Find JSON object
	if start := strings.Index(content, "{"); start != -1 {
		depth := 0
		for i := start; i < len(content); i++ {
			if content[i] == '{' {
				depth++
			} else if content[i] == '}' {
				depth--
				if depth == 0 {
					return content[start : i+1]
				}
			}
		}
	}

	return content
}
