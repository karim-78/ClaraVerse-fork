package execution

import (
	"claraverse/internal/models"
	"claraverse/internal/services"
	"claraverse/internal/services/rag"
	"claraverse/internal/tools"
	"context"
	"fmt"
	"os"
)

// BlockExecutor interface for all block types
type BlockExecutor interface {
	Execute(ctx context.Context, block models.Block, inputs map[string]any) (map[string]any, error)
}

// ExecutorRegistry maps block types to executors
type ExecutorRegistry struct {
	executors map[string]BlockExecutor
}

// NewExecutorRegistry creates a new executor registry with all block type executors
// Hybrid Architecture: Supports variable, llm_inference, and code_block types.
// - variable: Input/output data handling
// - llm_inference: AI reasoning with tool access
// - code_block: Direct tool execution (no LLM, faster & deterministic)
func NewExecutorRegistry(
	chatService *services.ChatService,
	providerService *services.ProviderService,
	toolRegistry *tools.Registry,
	credentialService *services.CredentialService,
	ragService *rag.Service,
) *ExecutorRegistry {
	return &ExecutorRegistry{
		executors: map[string]BlockExecutor{
			// Variable blocks handle input/output data
			"variable": NewVariableExecutor(),
			// LLM blocks handle all intelligent actions via tools
			"llm_inference": NewAgentBlockExecutor(chatService, providerService, toolRegistry, credentialService),
			// Code blocks execute tools directly without LLM (faster, deterministic)
			"code_block": NewToolExecutor(toolRegistry, credentialService),

			// === n8n-style deterministic blocks (no LLM at runtime) ===
			// HTTP Request: universal REST API calls
			"http_request": NewHTTPRequestExecutor(),
			// If Condition: routes data to true/false branches
			"if_condition": NewIfConditionExecutor(),
			// Transform: set/delete/rename/extract fields
			"transform": NewTransformExecutor(),
			// Triggers (MVP passthrough — real registration comes later)
			"webhook_trigger":  NewWebhookTriggerExecutor(),
			"schedule_trigger": NewScheduleTriggerExecutor(),

			// === New block types ===
			// For Each: iterate over arrays
			"for_each": NewForEachExecutor(),
			// Inline Code: run Python or JavaScript
			"inline_code": NewInlineCodeExecutor(),
			// Sub-Agent: trigger another agent
			"sub_agent": NewSubAgentExecutor(getInternalBaseURL(), os.Getenv("INTERNAL_SERVICE_KEY")),

			// === Data manipulation blocks (n8n parity) ===
			"filter":      NewFilterExecutor(),
			"switch":      NewSwitchExecutor(),
			"merge":       NewMergeExecutor(),
			"aggregate":   NewAggregateExecutor(),
			"sort":        NewSortExecutor(),
			"limit":       NewLimitExecutor(),
			"deduplicate": NewDeduplicateExecutor(),
			"wait":        NewWaitExecutor(),

			// === Knowledge / RAG ==========================================
			// Project-scoped retrieval block. Returns chunks for downstream
			// LLM consumption. Same backend Service as chat + Nexus tools —
			// just exposed as an explicit node in the visual workflow.
			"knowledge_search": NewKnowledgeSearchExecutor(ragService),
		},
	}
}

// getInternalBaseURL returns the base URL for internal API calls (self-referencing)
func getInternalBaseURL() string {
	port := os.Getenv("PORT")
	if port == "" {
		port = "3001"
	}
	return "http://localhost:" + port
}

// Get retrieves an executor for a block type
func (r *ExecutorRegistry) Get(blockType string) (BlockExecutor, error) {
	exec, ok := r.executors[blockType]
	if !ok {
		return nil, fmt.Errorf("no executor registered for block type: %s", blockType)
	}
	return exec, nil
}

// Register adds a new executor for a block type
func (r *ExecutorRegistry) Register(blockType string, executor BlockExecutor) {
	r.executors[blockType] = executor
}
