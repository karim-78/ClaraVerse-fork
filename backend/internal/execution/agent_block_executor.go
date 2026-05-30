package execution

import (
	"bufio"
	"bytes"
	"claraverse/internal/filecache"
	"claraverse/internal/models"
	"claraverse/internal/services"
	"claraverse/internal/tools"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"go.mongodb.org/mongo-driver/bson/primitive"
)

// ExecutionModePrefix is injected before all system prompts during workflow execution
// This forces the LLM into "action mode" rather than "conversational mode"
const ExecutionModePrefix = `## WORKFLOW EXECUTION MODE - MANDATORY INSTRUCTIONS

You are operating in WORKFLOW EXECUTION MODE. This is NOT a conversation.

CRITICAL RULES:
1. DO NOT ask questions - all required data is provided below
2. DO NOT explain what you're about to do - JUST DO IT immediately
3. DO NOT hesitate or offer alternatives - execute the primary task NOW
4. MUST use the available tools to complete your task - tool usage is MANDATORY, not optional
5. DO NOT generate placeholder/example data - use the ACTUAL data provided in the input
6. After completing tool calls, provide a brief confirmation and STOP
7. DO NOT ask for webhook URLs, credentials, or configuration - these are auto-injected

EXECUTION PATTERN:
1. Read the input data provided
2. Immediately call the required tool(s) with the actual data
3. Return a brief confirmation of what was done
4. STOP - do not continue iterating or ask follow-up questions

`

// ToolUsageError represents a validation error when required tools were not called
type ToolUsageError struct {
	Type         string   `json:"type"`          // "no_tool_called" or "required_tool_missing"
	Message      string   `json:"message"`
	EnabledTools []string `json:"enabledTools,omitempty"`
	MissingTools []string `json:"missingTools,omitempty"`
}

// ToolUsageValidator validates that required tools were called during block execution
type ToolUsageValidator struct {
	requiredTools  []string
	enabledTools   []string
	requireAnyTool bool
}

// NewToolUsageValidator creates a new validator based on block configuration
func NewToolUsageValidator(config models.AgentBlockConfig) *ToolUsageValidator {
	return &ToolUsageValidator{
		requiredTools:  config.RequiredTools,
		enabledTools:   config.EnabledTools,
		requireAnyTool: len(config.EnabledTools) > 0 && config.RequireToolUsage,
	}
}

// Validate checks if the required tools were called
// Returns nil if validation passes, or a ToolUsageError if it fails
// IMPORTANT: Tools that were called but failed (e.g., API rate limits) still count as "called"
// because the LLM correctly attempted to use the tool - we don't want to retry in that case
func (v *ToolUsageValidator) Validate(toolCalls []models.ToolCallRecord) *ToolUsageError {
	if !v.requireAnyTool && len(v.requiredTools) == 0 {
		return nil // No validation needed
	}

	// Build sets of attempted tools (all calls) and successful tools
	attemptedTools := make(map[string]bool)
	successfulTools := make(map[string]bool)
	var failedToolErrors []string

	for _, tc := range toolCalls {
		attemptedTools[tc.Name] = true
		if tc.Error == "" {
			successfulTools[tc.Name] = true
		} else {
			// Track tool errors for better error messages
			failedToolErrors = append(failedToolErrors, fmt.Sprintf("%s: %s", tc.Name, tc.Error))
		}
	}

	// Check if any tool was ATTEMPTED (when tools are enabled and required)
	// If a tool was called but failed externally (API error), we don't retry - the LLM did its job
	if v.requireAnyTool && len(attemptedTools) == 0 {
		return &ToolUsageError{
			Type:         "no_tool_called",
			Message:      "Block has tools enabled but none were called. The LLM responded with text only instead of using the available tools.",
			EnabledTools: v.enabledTools,
		}
	}

	// If tools were attempted but ALL failed, check if it's a parameter error or external failure
	// Parameter errors (wrong enum, invalid action, etc.) should trigger retry
	// External errors (API down, rate limit, auth failure) should not retry
	if len(attemptedTools) > 0 && len(successfulTools) == 0 && len(failedToolErrors) > 0 {
		// Check if errors are parameter/validation issues that the LLM can fix
		allParameterErrors := true
		for _, errMsg := range failedToolErrors {
			// Parameter errors contain hints like "Did you mean", "is not valid", "unsupported action"
			isParameterError := strings.Contains(errMsg, "Did you mean") ||
				strings.Contains(errMsg, "is not valid") ||
				strings.Contains(errMsg, "unsupported action") ||
				strings.Contains(errMsg, "is required") ||
				strings.Contains(errMsg, "invalid action")

			if !isParameterError {
				allParameterErrors = false
				break
			}
		}

		// If all errors are parameter errors, treat as validation failure (will retry)
		// If any error is external (API, network, etc.), don't retry
		if !allParameterErrors {
			log.Printf("⚠️ [TOOL-VALIDATOR] Tools were called but failed externally: %v", failedToolErrors)
			return nil // Don't retry - external failure
		}
		// If allParameterErrors is true, fall through to validation logic below
		// This will trigger a retry with the error message feedback
	}

	// Check required specific tools - must be ATTEMPTED (not necessarily successful)
	// If a required tool was called but failed, that's an external issue, not a retry case
	var missingTools []string
	for _, required := range v.requiredTools {
		if !attemptedTools[required] {
			missingTools = append(missingTools, required)
		}
	}

	if len(missingTools) > 0 {
		return &ToolUsageError{
			Type:         "required_tool_missing",
			Message:      fmt.Sprintf("Required tools not called: %v. These tools must be used to complete the task.", missingTools),
			MissingTools: missingTools,
		}
	}

	return nil
}

// AgentBlockExecutor executes LLM blocks as mini-agents with tool support
type AgentBlockExecutor struct {
	chatService       *services.ChatService
	providerService   *services.ProviderService
	toolRegistry      *tools.Registry
	credentialService *services.CredentialService
	httpClient        *http.Client
}

// NewAgentBlockExecutor creates a new agent block executor
func NewAgentBlockExecutor(
	chatService *services.ChatService,
	providerService *services.ProviderService,
	toolRegistry *tools.Registry,
	credentialService *services.CredentialService,
) *AgentBlockExecutor {
	return &AgentBlockExecutor{
		chatService:       chatService,
		providerService:   providerService,
		toolRegistry:      toolRegistry,
		credentialService: credentialService,
		httpClient: &http.Client{
			Timeout: 120 * time.Second,
		},
	}
}

// Execute runs an LLM block as a mini-agent with tool support
// Uses two-phase approach:
//   - Phase 1: Task execution with tools (no schema concerns)
//   - Phase 2: Schema formatting (dedicated formatting step)
// Retry logic only handles tool usage validation, not schema errors
func (e *AgentBlockExecutor) Execute(ctx context.Context, block models.Block, inputs map[string]any) (map[string]any, error) {
	// Parse config with defaults
	config := e.parseConfig(block.Config)

	// Create tool usage validator
	validator := NewToolUsageValidator(config)

	// Retry loop for tool usage validation ONLY
	// Schema formatting is now handled as a separate phase in executeOnce
	var lastResult map[string]any
	var lastValidationError *ToolUsageError

	for attempt := 0; attempt <= config.MaxRetries; attempt++ {
		if attempt > 0 {
			retryReason := fmt.Sprintf("Tool validation failed: %s", lastValidationError.Message)
			log.Printf("🔄 [AGENT-BLOCK] Retry attempt %d for block '%s' - %s",
				attempt, block.Name, retryReason)
			// Inject retry context for stronger prompting
			inputs["_retryAttempt"] = attempt
			inputs["_retryReason"] = retryReason
		}

		// Execute the block (includes Phase 1 + Phase 2)
		result, err := e.executeOnce(ctx, block, inputs, config)
		if err != nil {
			return nil, err // Execution error, not validation error - don't retry
		}

		// Validate tool usage
		toolCalls, _ := result["toolCalls"].([]models.ToolCallRecord)
		validationError := validator.Validate(toolCalls)

		if validationError != nil {
			// Tool validation failed
			lastResult = result
			lastValidationError = validationError
			log.Printf("⚠️ [AGENT-BLOCK] Block '%s' tool validation failed (attempt %d/%d): %s",
				block.Name, attempt+1, config.MaxRetries+1, validationError.Message)

			// If this was the last attempt, return with warning
			if attempt == config.MaxRetries {
				log.Printf("⚠️ [AGENT-BLOCK] Block '%s' exhausted all %d retry attempts, returning with tool validation warning",
					block.Name, config.MaxRetries+1)
				result["_toolValidationWarning"] = validationError.Message
				result["_toolValidationType"] = validationError.Type
				delete(result, "_retryAttempt")
				delete(result, "_retryReason")
				return result, nil
			}

			// Clear retry-specific state for next attempt
			delete(inputs, "_retryAttempt")
			delete(inputs, "_retryReason")
			continue
		}

		// Tool validation passed - schema formatting was already handled in executeOnce Phase 2
		// Clean up and return
		delete(result, "_retryAttempt")
		delete(result, "_retryReason")
		if attempt > 0 {
			log.Printf("✅ [AGENT-BLOCK] Block '%s' succeeded on retry attempt %d", block.Name, attempt)
		}
		return result, nil
	}

	// Should never reach here, but return last result as fallback
	return lastResult, nil
}

// executeOnce performs a single execution attempt of the LLM block
func (e *AgentBlockExecutor) executeOnce(ctx context.Context, block models.Block, inputs map[string]any, config models.AgentBlockConfig) (map[string]any, error) {

	// Model priority: block-level model > workflow-level model > default
	// Only fall back to workflow model if the block doesn't have its own model set
	if config.Model == "" {
		if workflowModelID, ok := inputs["_workflowModelId"].(string); ok && workflowModelID != "" {
			log.Printf("🎯 [AGENT-BLOCK] Block '%s': Using workflow model (no block model set): %s", block.Name, workflowModelID)
			config.Model = workflowModelID
		}
	} else {
		log.Printf("🎯 [AGENT-BLOCK] Block '%s': Using block-level model: %s", block.Name, config.Model)
	}

	log.Printf("🤖 [AGENT-BLOCK] Block '%s': model=%s, enabledTools=%v, maxToolCalls=%d",
		block.Name, config.Model, config.EnabledTools, config.MaxToolCalls)

	// Resolve model (alias -> direct -> fallback)
	provider, modelID, err := e.resolveModel(config.Model)
	if err != nil {
		return nil, fmt.Errorf("failed to resolve model: %w", err)
	}

	log.Printf("✅ [AGENT-BLOCK] Resolved model '%s' -> '%s' (provider: %s)",
		config.Model, modelID, provider.Name)

	// Extract data files for context injection (but don't auto-enable tools)
	dataFiles := e.extractDataFileAttachments(inputs)
	if len(dataFiles) > 0 {
		log.Printf("📊 [AGENT-BLOCK] Found %d data file(s) - tools must be explicitly configured", len(dataFiles))
	}

	// NOTE: Auto-detection removed - blocks only use explicitly configured tools
	// Users must configure enabledTools in the block settings

	// Build initial messages with interpolated prompts
	messages := e.buildMessages(config, inputs)

	// Filter tools to only those enabled for this block
	enabledTools := e.filterTools(config.EnabledTools)

	log.Printf("🔧 [AGENT-BLOCK] Enabled %d tools for block '%s'", len(enabledTools), block.Name)

	// Track tool calls and token usage
	var allToolCalls []models.ToolCallRecord
	var totalTokens models.TokenUsage
	iterations := 0
	var lastContent string // Track last content for timeout handling

	// Track generated chart images for auto-injection into Discord/Slack messages
	// The LLM sees sanitized placeholders like [CHART_IMAGE_SAVED] but we need the real base64
	var generatedCharts []string

	// PRE-POPULATE generatedCharts with artifacts from previous blocks
	// This allows Discord/Slack blocks to access charts generated by upstream blocks
	generatedCharts = e.extractChartsFromInputs(inputs)
	if len(generatedCharts) > 0 {
		log.Printf("🖼️ [AGENT-BLOCK] Pre-loaded %d chart(s) from previous block artifacts for auto-injection", len(generatedCharts))
	}

	// Run agent loop - continues until LLM stops calling tools or timeout
	for {
		iterations++
		log.Printf("🔄 [AGENT-BLOCK] Iteration %d for block '%s'", iterations, block.Name)

		// Enforce iteration limit to prevent infinite loops
		maxIterations := 10 // Default safety limit
		if config.MaxToolCalls > 0 {
			maxIterations = config.MaxToolCalls
		}
		if iterations > maxIterations {
			log.Printf("🛑 [AGENT-BLOCK] Block '%s' reached max iterations (%d)", block.Name, maxIterations)
			return e.buildTimeoutResult(inputs, modelID, totalTokens, allToolCalls, lastContent, iterations)
		}

		// Track tool calls to detect repetition within this iteration only
		// Reset per iteration to allow same tool across different iterations (legitimate refinement)
		executedToolCalls := make(map[string]bool)

		// Check if context is cancelled (timeout)
		select {
		case <-ctx.Done():
			log.Printf("⏱️ [AGENT-BLOCK] Block '%s' timed out after %d iterations", block.Name, iterations)
			return e.buildTimeoutResult(inputs, modelID, totalTokens, allToolCalls, lastContent, iterations)
		default:
			// Continue execution
		}

		// Call LLM with retry for transient errors (timeout, rate limit, server errors)
		response, retryAttempts, err := e.callLLMWithRetry(ctx, provider, modelID, messages, enabledTools, config.Temperature, config.RetryPolicy)
		if err != nil {
			// Include retry info in error for debugging
			if len(retryAttempts) > 0 {
				return nil, fmt.Errorf("LLM call failed in iteration %d after %d attempt(s): %w", iterations, len(retryAttempts)+1, err)
			}
			return nil, fmt.Errorf("LLM call failed in iteration %d: %w", iterations, err)
		}

		// Track retry attempts for surfacing in output (first iteration only to avoid duplicates)
		if iterations == 1 && len(retryAttempts) > 0 {
			inputs["_llmRetryAttempts"] = retryAttempts
		}

		// Accumulate tokens
		totalTokens.Input += response.InputTokens
		totalTokens.Output += response.OutputTokens

		// Check finish_reason for explicit stop signal from LLM
		// This is how chat mode knows to stop - agent mode should do the same
		if response.FinishReason == "stop" || response.FinishReason == "end_turn" {
			log.Printf("✅ [AGENT-BLOCK] LLM signaled stop (finish_reason=%s), completing block '%s'",
				response.FinishReason, block.Name)
			// Continue to completion logic below (same as no tool calls)
		}

		// Check if there are tool calls
		if len(response.ToolCalls) == 0 || response.FinishReason == "stop" || response.FinishReason == "end_turn" {
			// No more tools - agent is done (Phase 1 complete)
			log.Printf("✅ [AGENT-BLOCK] Block '%s' Phase 1 (task execution) completed after %d iteration(s)", block.Name, iterations)

			// Build result starting with all inputs (pass through workflow variables)
			result := make(map[string]any)
			for k, v := range inputs {
				result[k] = v
			}

			// Store raw LLM response
			result["rawResponse"] = response.Content
			result["response"] = response.Content // Default, may be overwritten by schema formatting

			// CRITICAL: Extract and surface tool results for downstream blocks
			// This solves the data passing problem where tool output was buried in toolCalls
			toolResults := e.extractToolResultsForDownstream(allToolCalls)
			toolResultsMaps := make([]map[string]any, 0)
			if len(toolResults) > 0 {
				// Store parsed tool results for easy access
				result["toolResults"] = toolResults

				// Surface key data fields at top level for template access
				// e.g., {{block-name.text}} instead of {{block-name.toolResults.transcribe_audio.text}}
				for _, tr := range toolResults {
					if trMap, ok := tr.(map[string]any); ok {
						toolResultsMaps = append(toolResultsMaps, trMap)
						// Surface common data fields
						for _, key := range []string{"text", "data", "content", "result", "output", "transcription"} {
							if val, exists := trMap[key]; exists && val != nil {
								// Only surface if not already set by LLM response
								if _, alreadySet := result[key]; !alreadySet {
									result[key] = val
									log.Printf("📤 [AGENT-BLOCK] Surfaced tool result field '%s' to top level", key)
								}
							}
						}
					}
				}
			}

			// Phase 2: Schema formatting (if schema is defined)
			// This is the key change - we use a dedicated formatting step for structured output
			if config.OutputSchema != nil {
				log.Printf("📐 [AGENT-BLOCK] Block '%s' starting Phase 2 (schema formatting)", block.Name)

				// Prepare input for schema formatter
				formatInput := FormatInput{
					RawData:     response.Content,
					ToolResults: toolResultsMaps,
					LLMResponse: response.Content,
					Context:     config.SystemPrompt,
				}

				// Call the dedicated schema formatter
				formatOutput, err := e.FormatToSchema(ctx, formatInput, config.OutputSchema, modelID)
				if err != nil {
					log.Printf("⚠️ [AGENT-BLOCK] Phase 2 schema formatting error: %v", err)
					// Continue with raw output if formatting fails
					result["_formatError"] = err.Error()
				} else if formatOutput != nil {
					if formatOutput.Success {
						log.Printf("✅ [AGENT-BLOCK] Phase 2 schema formatting succeeded")
						// Use the formatted data as the response
						result["response"] = formatOutput.Data
						result["data"] = formatOutput.Data
						result["output"] = formatOutput.Data
						// Also spread the fields at top level for easy access
						for k, v := range formatOutput.Data {
							result[k] = v
						}
						// Track the formatting tokens
						totalTokens.Input += formatOutput.Tokens.Input
						totalTokens.Output += formatOutput.Tokens.Output
					} else {
						log.Printf("⚠️ [AGENT-BLOCK] Phase 2 schema formatting failed: %s", formatOutput.Error)
						result["_formatError"] = formatOutput.Error
						// Fall back to basic parsing without validation
						if parsedOutput, err := e.parseAndValidateOutput(response.Content, nil, false); err == nil {
							for k, v := range parsedOutput {
								result[k] = v
							}
						}
					}
				}
			} else {
				// No schema defined - just parse the response as-is
				output, err := e.parseAndValidateOutput(response.Content, nil, false)
				if err != nil {
					log.Printf("⚠️ [AGENT-BLOCK] Output parsing error (no schema): %v", err)
				} else {
					for k, v := range output {
						result[k] = v
					}
					result["output"] = output
				}
			}

			// If LLM response is just a summary but we have tool data, use tool data as response
			if len(allToolCalls) > 0 {
				llmResponse, _ := result["response"].(string)
				// Check if LLM response is a short summary (likely not the actual data)
				if len(llmResponse) < 500 && len(toolResults) > 0 {
					// Check if we have a text field from tools that's more substantial
					if textResult, ok := result["text"].(string); ok && len(textResult) > len(llmResponse) {
						// Keep LLM response as summary, but ensure text is available
						result["summary"] = llmResponse
						log.Printf("📤 [AGENT-BLOCK] Tool 'text' field (%d chars) surfaced; LLM response (%d chars) kept as 'summary'",
							len(textResult), len(llmResponse))
					}
				}
			}

			result["model"] = modelID
			result["tokens"] = map[string]int{
				"input":  totalTokens.Input,
				"output": totalTokens.Output,
				"total":  totalTokens.Input + totalTokens.Output,
			}
			result["toolCalls"] = allToolCalls
			result["iterations"] = iterations

			// Check for tool errors and surface them for block checker
			// This helps distinguish between "LLM didn't use tools" vs "LLM used tools but they failed externally"
			var toolErrors []string
			for _, tc := range allToolCalls {
				if tc.Error != "" {
					toolErrors = append(toolErrors, fmt.Sprintf("%s: %s", tc.Name, tc.Error))
				}
			}
			if len(toolErrors) > 0 {
				result["_toolError"] = strings.Join(toolErrors, "; ")
				log.Printf("⚠️ [AGENT-BLOCK] Block has %d tool error(s): %v", len(toolErrors), toolErrors)
			}

			// Extract artifacts (charts, images) from tool calls for consistent API access
			artifacts := e.extractArtifactsFromToolCalls(allToolCalls)
			result["artifacts"] = artifacts

			// Extract generated files (PDFs, documents) from tool calls
			generatedFiles := e.extractGeneratedFilesFromToolCalls(allToolCalls)
			result["generatedFiles"] = generatedFiles
			// Also expose the first file's download URL directly for easy access
			if len(generatedFiles) > 0 {
				result["file_url"] = generatedFiles[0].DownloadURL
				result["file_name"] = generatedFiles[0].Filename
			}

			// Surface retry information for debugging/monitoring
			if retryAttempts, ok := inputs["_llmRetryAttempts"].([]models.RetryAttempt); ok && len(retryAttempts) > 0 {
				result["_retryInfo"] = map[string]any{
					"totalAttempts": len(retryAttempts) + 1,
					"retriedCount":  len(retryAttempts),
					"history":       retryAttempts,
				}
				log.Printf("📊 [AGENT-BLOCK] Block completed with %d retry attempt(s)", len(retryAttempts))
			}

			log.Printf("🔍 [AGENT-BLOCK] Output keys: %v, artifacts: %d, files: %d, toolResults: %d",
				getMapKeys(result), len(artifacts), len(generatedFiles), len(toolResults))
			return result, nil
		}

		// Execute tools and add results to messages
		log.Printf("🔧 [AGENT-BLOCK] Executing %d tool call(s) in iteration %d", len(response.ToolCalls), iterations)

		// Add assistant message with tool calls
		assistantMsg := map[string]any{
			"role":       "assistant",
			"tool_calls": response.ToolCalls,
		}
		if response.Content != "" {
			assistantMsg["content"] = response.Content
		}
		messages = append(messages, assistantMsg)

		// Execute each tool call
		repeatDetected := false
		for _, toolCall := range response.ToolCalls {
			toolName := e.getToolName(toolCall)

			// Check for repetition - if same tool called twice, it's likely looping
			if executedToolCalls[toolName] {
				log.Printf("⚠️ [AGENT-BLOCK] Detected repeated call to '%s', stopping to prevent loop", toolName)
				repeatDetected = true
				break
			}
			executedToolCalls[toolName] = true

			// Extract userID from inputs for credential resolution (uses __user_id__ convention)
			userID, _ := inputs["__user_id__"].(string)
			toolRecord := e.executeToolCall(toolCall, inputs, dataFiles, generatedCharts, userID, config.Credentials)
			allToolCalls = append(allToolCalls, toolRecord)

			// Extract any chart images from successful tool results for later injection
			if toolRecord.Error == "" && toolRecord.Result != "" {
				charts := e.extractChartsFromResult(toolRecord.Result)
				if len(charts) > 0 {
					generatedCharts = append(generatedCharts, charts...)
					log.Printf("📊 [AGENT-BLOCK] Extracted %d chart(s) from tool '%s' (total: %d)",
						len(charts), toolName, len(generatedCharts))
				}
			}

			// Sanitize tool result for LLM - remove base64 images which are useless as text
			sanitizedResult := e.sanitizeToolResultForLLM(toolRecord.Result)

			// Add tool result to messages
			toolResultMsg := map[string]any{
				"role":         "tool",
				"tool_call_id": toolCall["id"],
				"name":         toolName,
				"content":      sanitizedResult,
			}
			if toolRecord.Error != "" {
				toolResultMsg["content"] = fmt.Sprintf("Error: %s", toolRecord.Error)
			}
			messages = append(messages, toolResultMsg)
		}

		// If repetition detected, exit loop and return current results
		if repeatDetected {
			log.Printf("🛑 [AGENT-BLOCK] Exiting loop due to repeated tool call")
			return e.buildTimeoutResult(inputs, modelID, totalTokens, allToolCalls, lastContent, iterations)
		}

		// Track last content for timeout fallback
		if response.Content != "" {
			lastContent = response.Content
		}
	}
	// Note: Loop only exits via return statements (success or timeout)
}

// buildTimeoutResult creates a result when the block times out
// Instead of returning an error, it returns the collected tool call data
// so downstream blocks can still use the information gathered
func (e *AgentBlockExecutor) buildTimeoutResult(
	inputs map[string]any,
	modelID string,
	totalTokens models.TokenUsage,
	allToolCalls []models.ToolCallRecord,
	lastContent string,
	iterations int,
) (map[string]any, error) {
	// Build result starting with all inputs (pass through workflow variables)
	result := make(map[string]any)
	for k, v := range inputs {
		result[k] = v
	}

	// Build a summary from tool call results if no meaningful content was generated
	var outputContent string
	trimmedContent := strings.TrimSpace(lastContent)
	if trimmedContent != "" {
		outputContent = lastContent
	} else if len(allToolCalls) > 0 {
		// Compile tool results as the output
		var summaryParts []string
		for _, tc := range allToolCalls {
			if tc.Result != "" && tc.Error == "" {
				summaryParts = append(summaryParts, tc.Result)
			}
		}
		if len(summaryParts) > 0 {
			outputContent = strings.Join(summaryParts, "\n\n")
		}
	}

	// Flatten output fields directly into result for consistent access
	// This makes {{block-name.response}} work the same as simple LLM executor
	result["response"] = outputContent
	result["timedOut"] = true

	// CRITICAL: Extract and surface tool results for downstream blocks (same as normal completion)
	toolResults := e.extractToolResultsForDownstream(allToolCalls)
	if len(toolResults) > 0 {
		result["toolResults"] = toolResults

		// Surface key data fields at top level for template access
		for _, tr := range toolResults {
			if trMap, ok := tr.(map[string]any); ok {
				for _, key := range []string{"text", "data", "content", "result", "output", "transcription"} {
					if val, exists := trMap[key]; exists && val != nil {
						if _, alreadySet := result[key]; !alreadySet {
							result[key] = val
							log.Printf("📤 [AGENT-BLOCK] Surfaced tool result field '%s' to top level (timeout)", key)
						}
					}
				}
			}
		}
	}

	// Also keep "output" for backward compatibility
	result["output"] = map[string]any{
		"response":    outputContent,
		"timedOut":    true,
		"iterations":  iterations,
		"toolResults": len(allToolCalls),
	}
	result["rawResponse"] = outputContent
	result["model"] = modelID
	result["tokens"] = map[string]int{
		"input":  totalTokens.Input,
		"output": totalTokens.Output,
		"total":  totalTokens.Input + totalTokens.Output,
	}
	result["toolCalls"] = allToolCalls
	result["iterations"] = iterations

	// Extract artifacts (charts, images) from tool calls for consistent API access
	artifacts := e.extractArtifactsFromToolCalls(allToolCalls)
	result["artifacts"] = artifacts

	// Extract generated files (PDFs, documents) from tool calls
	generatedFiles := e.extractGeneratedFilesFromToolCalls(allToolCalls)
	result["generatedFiles"] = generatedFiles
	// Also expose the first file's download URL directly for easy access
	if len(generatedFiles) > 0 {
		result["file_url"] = generatedFiles[0].DownloadURL
		result["file_name"] = generatedFiles[0].Filename
	}

	log.Printf("⏱️ [AGENT-BLOCK] Timeout result built with %d tool calls, content length: %d, artifacts: %d, files: %d, toolResults: %d",
		len(allToolCalls), len(outputContent), len(artifacts), len(generatedFiles), len(toolResults))
	log.Printf("🔍 [AGENT-BLOCK] Output keys: %v", getMapKeys(result))

	return result, nil
}

// parseToolsList converts various array types to []string for tool names
func parseToolsList(raw interface{}) []string {
	var tools []string

	switch v := raw.(type) {
	case []interface{}:
		for _, t := range v {
			if toolName, ok := t.(string); ok {
				tools = append(tools, toolName)
			}
		}
	case []string:
		tools = v
	case primitive.A: // BSON array type
		for _, t := range v {
			if toolName, ok := t.(string); ok {
				tools = append(tools, toolName)
			}
		}
	default:
		log.Printf("⚠️ [CONFIG] Unknown enabledTools type: %T", raw)
	}

	return tools
}

// parseConfig parses block config into AgentBlockConfig with defaults
func (e *AgentBlockExecutor) parseConfig(config map[string]any) models.AgentBlockConfig {
	result := models.AgentBlockConfig{
		Model:        "sonnet-4.5", // Default model alias
		Temperature:  0.7,
		MaxToolCalls: 15, // Increased to allow agents with multiple search iterations
	}

	// Model
	if v, ok := config["model"].(string); ok && v != "" {
		result.Model = v
	}
	if v, ok := config["modelId"].(string); ok && v != "" {
		result.Model = v
	}

	// Temperature
	if v, ok := config["temperature"].(float64); ok {
		result.Temperature = v
	}

	// System prompt
	if v, ok := config["systemPrompt"].(string); ok {
		result.SystemPrompt = v
	}
	if v, ok := config["system_prompt"].(string); ok && result.SystemPrompt == "" {
		result.SystemPrompt = v
	}

	// User prompt
	if v, ok := config["userPrompt"].(string); ok {
		result.UserPrompt = v
	}
	if v, ok := config["userPromptTemplate"].(string); ok && result.UserPrompt == "" {
		result.UserPrompt = v
	}
	if v, ok := config["user_prompt"].(string); ok && result.UserPrompt == "" {
		result.UserPrompt = v
	}

	// Enabled tools - handle multiple possible types from BSON/JSON
	if enabledToolsRaw, exists := config["enabledTools"]; exists && enabledToolsRaw != nil {
		result.EnabledTools = parseToolsList(enabledToolsRaw)
		log.Printf("🔧 [CONFIG] Parsed enabledTools from config: %v (type was %T)", result.EnabledTools, enabledToolsRaw)
	}
	if len(result.EnabledTools) == 0 {
		if enabledToolsRaw, exists := config["enabled_tools"]; exists && enabledToolsRaw != nil {
			result.EnabledTools = parseToolsList(enabledToolsRaw)
			log.Printf("🔧 [CONFIG] Parsed enabled_tools from config: %v (type was %T)", result.EnabledTools, enabledToolsRaw)
		}
	}

	// Max tool calls
	if v, ok := config["maxToolCalls"].(float64); ok {
		result.MaxToolCalls = int(v)
	}
	if v, ok := config["max_tool_calls"].(float64); ok && result.MaxToolCalls == 15 {
		result.MaxToolCalls = int(v)
	}

	// Credentials - array of credential IDs configured by user for tool authentication
	if credentialsRaw, exists := config["credentials"]; exists && credentialsRaw != nil {
		result.Credentials = parseToolsList(credentialsRaw) // Reuse the same parser ([]string)
		log.Printf("🔐 [CONFIG] Parsed credentials from config: %v", result.Credentials)
	}

	// Output schema
	if v, ok := config["outputSchema"].(map[string]any); ok {
		result.OutputSchema = e.parseJSONSchema(v)
		log.Printf("📋 [CONFIG] Parsed outputSchema with %d required fields: %v", len(result.OutputSchema.Required), result.OutputSchema.Required)
	} else {
		log.Printf("📋 [CONFIG] No outputSchema found in config (outputSchema key: %v)", config["outputSchema"] != nil)
	}

	// Strict output
	if v, ok := config["strictOutput"].(bool); ok {
		result.StrictOutput = v
	}

	// Execution Mode Configuration (NEW)
	// Parse requireToolUsage - if explicitly set, use that value
	if v, ok := config["requireToolUsage"].(bool); ok {
		result.RequireToolUsage = v
	} else {
		// Default: Auto-enable when tools are present for deterministic execution
		result.RequireToolUsage = len(result.EnabledTools) > 0
	}

	// Parse maxRetries - default to 2 for resilience
	result.MaxRetries = 2 // Default
	if v, ok := config["maxRetries"].(float64); ok {
		result.MaxRetries = int(v)
	}
	if v, ok := config["max_retries"].(float64); ok {
		result.MaxRetries = int(v)
	}

	// Parse requiredTools - specific tools that MUST be called
	if requiredToolsRaw, exists := config["requiredTools"]; exists && requiredToolsRaw != nil {
		result.RequiredTools = parseToolsList(requiredToolsRaw)
		log.Printf("🔧 [CONFIG] Parsed requiredTools from config: %v", result.RequiredTools)
	}
	if len(result.RequiredTools) == 0 {
		if requiredToolsRaw, exists := config["required_tools"]; exists && requiredToolsRaw != nil {
			result.RequiredTools = parseToolsList(requiredToolsRaw)
		}
	}

	// Auto-lower temperature for execution mode when tools are enabled
	// Lower temp = more deterministic tool calling
	if len(result.EnabledTools) > 0 {
		if _, explicitTemp := config["temperature"]; !explicitTemp {
			result.Temperature = 0.3
			log.Printf("🔧 [CONFIG] Auto-lowered temperature to 0.3 for execution mode")
		}
	}

	// Parse RetryPolicy for LLM API call retries (transient error handling)
	if retryPolicyRaw, exists := config["retryPolicy"]; exists && retryPolicyRaw != nil {
		if retryMap, ok := retryPolicyRaw.(map[string]any); ok {
			result.RetryPolicy = &models.RetryPolicy{}

			if v, ok := retryMap["maxRetries"].(float64); ok {
				result.RetryPolicy.MaxRetries = int(v)
			}
			if v, ok := retryMap["initialDelay"].(float64); ok {
				result.RetryPolicy.InitialDelay = int(v)
			}
			if v, ok := retryMap["maxDelay"].(float64); ok {
				result.RetryPolicy.MaxDelay = int(v)
			}
			if v, ok := retryMap["backoffMultiplier"].(float64); ok {
				result.RetryPolicy.BackoffMultiplier = v
			}
			if v, ok := retryMap["jitterPercent"].(float64); ok {
				result.RetryPolicy.JitterPercent = int(v)
			}
			if retryOn, ok := retryMap["retryOn"].([]interface{}); ok {
				for _, r := range retryOn {
					if s, ok := r.(string); ok {
						result.RetryPolicy.RetryOn = append(result.RetryPolicy.RetryOn, s)
					}
				}
			}
			log.Printf("🔧 [CONFIG] Parsed retryPolicy: maxRetries=%d, initialDelay=%dms",
				result.RetryPolicy.MaxRetries, result.RetryPolicy.InitialDelay)
		}
	}

	// Apply default retry policy if not specified (for production resilience)
	if result.RetryPolicy == nil {
		result.RetryPolicy = models.DefaultRetryPolicy()
	}

	log.Printf("🔧 [CONFIG] Execution mode: requireToolUsage=%v, maxRetries=%d, requiredTools=%v",
		result.RequireToolUsage, result.MaxRetries, result.RequiredTools)

	return result
}

// parseJSONSchema converts a map to JSONSchema
func (e *AgentBlockExecutor) parseJSONSchema(schema map[string]any) *models.JSONSchema {
	result := &models.JSONSchema{}

	if v, ok := schema["type"].(string); ok {
		result.Type = v
	}

	if v, ok := schema["properties"].(map[string]any); ok {
		result.Properties = make(map[string]*models.JSONSchema)
		for key, prop := range v {
			if propMap, ok := prop.(map[string]any); ok {
				result.Properties[key] = e.parseJSONSchema(propMap)
			}
		}
	}

	if v, ok := schema["items"].(map[string]any); ok {
		result.Items = e.parseJSONSchema(v)
	}

	// Handle required field - support multiple Go types including MongoDB's primitive.A
	// Note: []interface{} and []any are the same type in Go, so only use one
	switch v := schema["required"].(type) {
	case []interface{}:
		log.Printf("📋 [SCHEMA] Found required field ([]interface{}): %v", v)
		for _, r := range v {
			if req, ok := r.(string); ok {
				result.Required = append(result.Required, req)
			}
		}
	case []string:
		log.Printf("📋 [SCHEMA] Found required field ([]string): %v", v)
		result.Required = v
	case primitive.A: // MongoDB BSON array type
		log.Printf("📋 [SCHEMA] Found required field (primitive.A): %v", v)
		for _, r := range v {
			if req, ok := r.(string); ok {
				result.Required = append(result.Required, req)
			}
		}
	default:
		if schema["required"] != nil {
			log.Printf("📋 [SCHEMA] Unhandled required field type: %T", schema["required"])
		}
	}

	if v, ok := schema["description"].(string); ok {
		result.Description = v
	}

	return result
}

// jsonSchemaToMap converts a JSONSchema struct to a map for API requests
// This is used for native structured output (response_format with json_schema)
func (e *AgentBlockExecutor) jsonSchemaToMap(schema *models.JSONSchema) map[string]interface{} {
	if schema == nil {
		return nil
	}

	result := map[string]interface{}{
		"type": schema.Type,
	}

	// Convert properties
	if len(schema.Properties) > 0 {
		props := make(map[string]interface{})
		for key, prop := range schema.Properties {
			props[key] = e.jsonSchemaToMap(prop)
		}
		result["properties"] = props
	}

	// Convert items (for arrays)
	if schema.Items != nil {
		result["items"] = e.jsonSchemaToMap(schema.Items)
	}

	// Add required fields
	if len(schema.Required) > 0 {
		result["required"] = schema.Required
	}

	// Add description if present
	if schema.Description != "" {
		result["description"] = schema.Description
	}

	// Add enum if present
	if len(schema.Enum) > 0 {
		result["enum"] = schema.Enum
	}

	// Strict mode requires additionalProperties: false for objects
	if schema.Type == "object" {
		result["additionalProperties"] = false
	}

	return result
}

// resolveModel resolves model alias to actual model ID and provider.
// CRITICAL: returns the model's *bare API name* (the `name` column from the
// models table), never the row-id form like "3:zai.glm-5" that strict
// providers (Bedrock OpenAI shim) reject as "invalid model identifier".
func (e *AgentBlockExecutor) resolveModel(modelID string) (*models.Provider, string, error) {
	// Step 1: Direct lookup. Use the *WithName variant so we get the
	// upstream API id, not the row id. (GetByModelID alone would echo
	// back the prefixed input which Bedrock then 400s on.)
	provider, apiName, err := e.providerService.GetByModelIDWithName(modelID)
	if err == nil {
		log.Printf("✅ [AGENT-BLOCK] Resolved '%s' -> provider=%s, api name=%s", modelID, provider.Name, apiName)
		return provider, apiName, nil
	}

	// Step 2: Try model alias resolution. ResolveModelAlias already
	// normalises row-id-shaped values via the chat_service patch we shipped.
	log.Printf("🔄 [AGENT-BLOCK] Model '%s' not found directly, trying alias resolution...", modelID)
	if aliasProvider, aliasModel, found := e.chatService.ResolveModelAlias(modelID); found {
		return aliasProvider, aliasModel, nil
	}

	// Step 3: Fallback to default provider with model
	log.Printf("⚠️ [AGENT-BLOCK] Model '%s' not found, using default provider", modelID)
	defaultProvider, defaultModel, err := e.chatService.GetDefaultProviderWithModel()
	if err != nil {
		return nil, "", fmt.Errorf("failed to find provider for model %s: %w", modelID, err)
	}

	return defaultProvider, defaultModel, nil
}

// buildMessages creates the initial messages with interpolated prompts
func (e *AgentBlockExecutor) buildMessages(config models.AgentBlockConfig, inputs map[string]any) []map[string]any {
	messages := []map[string]any{}

	log.Printf("🔍 [AGENT-BLOCK] Building messages with inputs: %+v", inputs)

	// Check for data file attachments first (needed for system prompt enhancement)
	dataAttachments := e.extractDataFileAttachments(inputs)

	// Build the enhanced system prompt with execution mode prefix
	var systemPromptBuilder strings.Builder

	// ALWAYS inject execution mode preamble for deterministic behavior
	systemPromptBuilder.WriteString(ExecutionModePrefix)

	// Add tool-specific mandatory instructions when tools are enabled
	if len(config.EnabledTools) > 0 {
		systemPromptBuilder.WriteString("## REQUIRED TOOLS FOR THIS TASK\n")
		systemPromptBuilder.WriteString("You MUST use one or more of these tools to complete your task:\n\n")

		// Get tool descriptions to help the LLM understand how to use them
		toolDescriptions := e.getToolDescriptions(config.EnabledTools)
		for _, toolDesc := range toolDescriptions {
			systemPromptBuilder.WriteString(toolDesc)
			systemPromptBuilder.WriteString("\n")
		}

		systemPromptBuilder.WriteString("\nIMPORTANT: DO NOT respond with text only. You MUST call at least one of the above tools.\n\n")
	}

	// Check for retry context and add stronger instructions
	if retryAttempt, ok := inputs["_retryAttempt"].(int); ok && retryAttempt > 0 {
		retryReason, _ := inputs["_retryReason"].(string)

		// Determine if this is a schema error or tool error
		if strings.Contains(retryReason, "Schema validation failed") || strings.Contains(retryReason, "schema") {
			// Schema validation retry - guide LLM to fix JSON output format
			systemPromptBuilder.WriteString(fmt.Sprintf(`## ⚠️ SCHEMA VALIDATION RETRY (Attempt %d)
Your previous response did NOT match the required JSON schema.
Error: %s

CRITICAL REQUIREMENTS:
1. Your response MUST be valid JSON that matches the exact schema structure
2. Include ALL required fields - missing fields cause validation failure
3. Use the correct data types (strings vs numbers) as defined in the schema
4. If the schema expects an object with an array property, wrap your array in an object
5. Do NOT add extra fields not defined in the schema

Fix your response NOW to match the required schema exactly.

`, retryAttempt+1, retryReason))
		} else {
			// Tool usage retry
			systemPromptBuilder.WriteString(fmt.Sprintf(`## RETRY NOTICE (Attempt %d)
Your previous response did not use the required tools.
Reason: %s

YOU MUST call the appropriate tool(s) NOW. Do not respond with text only.
This is your last chance - call the tool immediately.

`, retryAttempt+1, retryReason))
		}
		log.Printf("🔄 [AGENT-BLOCK] Added retry notice for attempt %d (reason: %s)", retryAttempt+1, retryReason)
	}

	// Add the user's system prompt
	if config.SystemPrompt != "" {
		systemPromptBuilder.WriteString("## YOUR SPECIFIC TASK\n")
		systemPromptBuilder.WriteString(InterpolateTemplate(config.SystemPrompt, inputs))
		systemPromptBuilder.WriteString("\n\n")
	}

	// If data files present, add analysis guidelines to system prompt
	if len(dataAttachments) > 0 {
		systemPromptBuilder.WriteString(`
## Data Analysis Guidelines
- The data file content is provided in the user message - you can see the structure
- Use the 'analyze_data' tool to run Python code - data is pre-loaded as pandas DataFrame 'df'
- Generate all charts/visualizations in ONE comprehensive tool call
- After receiving results with charts, provide your insights and STOP - do not repeat the analysis
- Charts are automatically captured - you will see [CHART_IMAGE_SAVED] in the result
`)
		log.Printf("📊 [AGENT-BLOCK] Added data analysis guidelines to system prompt")
	}

	// Add structured data context so LLM knows exactly what data is available
	dataContextSection := e.buildDataContext(inputs)
	if dataContextSection != "" {
		systemPromptBuilder.WriteString(dataContextSection)
	}

	// Build the final system prompt
	finalSystemPrompt := systemPromptBuilder.String()
	log.Printf("🔍 [AGENT-BLOCK] System prompt built (%d chars, %d tools enabled)", len(finalSystemPrompt), len(config.EnabledTools))

	messages = append(messages, map[string]any{
		"role":    "system",
		"content": finalSystemPrompt,
	})

	// Add user prompt with potential image attachments
	userPrompt := InterpolateTemplate(config.UserPrompt, inputs)
	log.Printf("🔍 [AGENT-BLOCK] User prompt (interpolated): %s", userPrompt)

	// Inject data file content into user prompt (dataAttachments already extracted above)
	if len(dataAttachments) > 0 {
		var dataContext strings.Builder
		dataContext.WriteString("\n\n--- Data Files ---\n")

		for _, att := range dataAttachments {
			dataContext.WriteString(fmt.Sprintf("\nFile: %s\n", att.Filename))
			dataContext.WriteString(fmt.Sprintf("Type: %s\n", att.MimeType))
			dataContext.WriteString("Content preview (first 100 lines):\n```\n")
			dataContext.WriteString(att.Content)
			dataContext.WriteString("\n```\n")
		}

		userPrompt = userPrompt + dataContext.String()
		log.Printf("📊 [AGENT-BLOCK] Injected %d data file(s) into prompt (%d chars added)",
			len(dataAttachments), dataContext.Len())
	}

	// Check for image attachments and vision model support
	log.Printf("🔍 [AGENT-BLOCK] Checking for image attachments in %d inputs...", len(inputs))
	imageAttachments := e.extractImageAttachments(inputs)
	isVisionModel := e.isOpenAIVisionModel(config.Model)
	log.Printf("🔍 [AGENT-BLOCK] Found %d image attachments, isVisionModel=%v (model=%s)", len(imageAttachments), isVisionModel, config.Model)

	if len(imageAttachments) > 0 && isVisionModel {
		// Build multipart content with text and images
		contentParts := []map[string]any{
			{
				"type": "text",
				"text": userPrompt,
			},
		}

		for _, att := range imageAttachments {
			imageURL := e.getImageAsBase64DataURL(att.FileID)
			if imageURL != "" {
				contentParts = append(contentParts, map[string]any{
					"type": "image_url",
					"image_url": map[string]any{
						"url":    imageURL,
						"detail": "auto",
					},
				})
				log.Printf("🖼️ [AGENT-BLOCK] Added image attachment: %s", att.Filename)
			}
		}

		messages = append(messages, map[string]any{
			"role":    "user",
			"content": contentParts,
		})
	} else {
		// Standard text message
		messages = append(messages, map[string]any{
			"role":    "user",
			"content": userPrompt,
		})
	}

	return messages
}

// buildDataContext creates a structured data context section for the system prompt
// This helps the LLM understand exactly what data is available from previous blocks
func (e *AgentBlockExecutor) buildDataContext(inputs map[string]any) string {
	var builder strings.Builder
	var hasContent bool

	// Categorize inputs
	var workflowInputs []string
	blockOutputs := make(map[string]string)

	for key, value := range inputs {
		// Skip internal keys
		if strings.HasPrefix(key, "_") || strings.HasPrefix(key, "__") {
			continue
		}

		// Skip common passthrough keys
		if key == "input" || key == "value" || key == "start" {
			// Format the main input nicely
			if valueStr := formatValueForContext(value); valueStr != "" {
				workflowInputs = append(workflowInputs, fmt.Sprintf("- **%s**: %s", key, valueStr))
				hasContent = true
			}
			continue
		}

		// Check if it's a block output (nested map with response key)
		if m, ok := value.(map[string]any); ok {
			if response, hasResponse := m["response"]; hasResponse {
				var responseStr string
				switch rv := response.(type) {
				case string:
					responseStr = rv
					if len(responseStr) > 1500 {
						responseStr = responseStr[:1500] + "... [TRUNCATED]"
					}
				default:
					// Map/object response (e.g. webhook body) — JSON-encode for context
					if jsonBytes, err := json.Marshal(rv); err == nil {
						responseStr = string(jsonBytes)
						if len(responseStr) > 1500 {
							responseStr = responseStr[:1500] + "... [TRUNCATED]"
						}
					}
				}
				if responseStr != "" {
					blockOutputs[key] = responseStr
					hasContent = true
				}
			}
		}
	}

	// Always include current datetime for time-sensitive queries (safety net)
	builder.WriteString("\n## CURRENT DATE AND TIME\n")
	now := time.Now()
	builder.WriteString(fmt.Sprintf("**Today's Date:** %s\n", now.Format("Monday, January 2, 2006")))
	builder.WriteString(fmt.Sprintf("**Current Time:** %s\n", now.Format("3:04 PM MST")))
	builder.WriteString(fmt.Sprintf("**ISO Format:** %s\n\n", now.Format(time.RFC3339)))
	builder.WriteString("Use this date when searching for 'today', 'recent', 'latest', or 'current' information.\n\n")

	if !hasContent {
		// Still return the datetime even if no other content
		return builder.String()
	}

	builder.WriteString("## AVAILABLE DATA (Already Resolved)\n")
	builder.WriteString("The following data has been collected from previous steps and is ready for use:\n\n")

	// Present workflow inputs
	if len(workflowInputs) > 0 {
		builder.WriteString("### Direct Inputs\n")
		for _, input := range workflowInputs {
			builder.WriteString(input + "\n")
		}
		builder.WriteString("\n")
	}

	// Present block outputs
	if len(blockOutputs) > 0 {
		builder.WriteString("### Data from Previous Blocks\n")
		for blockID, response := range blockOutputs {
			builder.WriteString(fmt.Sprintf("**From `%s`:**\n", blockID))
			builder.WriteString("```\n")
			builder.WriteString(response)
			builder.WriteString("\n```\n\n")
		}
	}

	builder.WriteString("Use this data directly - DO NOT ask for it or claim you don't have it.\n\n")

	return builder.String()
}

// formatValueForContext formats a value for display in the data context
func formatValueForContext(value any) string {
	switch v := value.(type) {
	case string:
		if len(v) > 500 {
			return fmt.Sprintf("%q... [%d chars total]", v[:500], len(v))
		}
		if len(v) > 100 {
			return fmt.Sprintf("%q", v[:100]+"...")
		}
		return fmt.Sprintf("%q", v)
	case float64:
		if v == float64(int(v)) {
			return fmt.Sprintf("%d", int(v))
		}
		return fmt.Sprintf("%g", v)
	case int:
		return fmt.Sprintf("%d", v)
	case bool:
		return fmt.Sprintf("%t", v)
	case map[string]any:
		// Check for file reference
		if fileID, ok := v["file_id"].(string); ok && fileID != "" {
			filename, _ := v["filename"].(string)
			return fmt.Sprintf("[File: %s]", filename)
		}
		// For other maps, JSON encode briefly
		jsonBytes, err := json.Marshal(v)
		if err != nil {
			return "[complex object]"
		}
		if len(jsonBytes) > 200 {
			return string(jsonBytes[:200]) + "..."
		}
		return string(jsonBytes)
	default:
		return ""
	}
}

// FileAttachment represents an image or file attachment
type FileAttachment struct {
	FileID   string `json:"file_id"`
	Filename string `json:"filename"`
	MimeType string `json:"mime_type"`
	Type     string `json:"type"` // "image", "document", "audio", "data"
}

// extractImageAttachments extracts image attachments from inputs
func (e *AgentBlockExecutor) extractImageAttachments(inputs map[string]any) []FileAttachment {
	var attachments []FileAttachment

	// Helper to extract attachment from a map
	extractFromMap := func(attMap map[string]any) *FileAttachment {
		att := FileAttachment{}
		if v, ok := attMap["file_id"].(string); ok {
			att.FileID = v
		} else if v, ok := attMap["fileId"].(string); ok {
			att.FileID = v
		}
		if v, ok := attMap["filename"].(string); ok {
			att.Filename = v
		}
		if v, ok := attMap["mime_type"].(string); ok {
			att.MimeType = v
		} else if v, ok := attMap["mimeType"].(string); ok {
			att.MimeType = v
		}
		if v, ok := attMap["type"].(string); ok {
			att.Type = v
		}

		// Only include images
		if att.FileID != "" && (att.Type == "image" || strings.HasPrefix(att.MimeType, "image/")) {
			return &att
		}
		return nil
	}

	// Check for "_attachments" or "attachments" in inputs
	var rawAttachments []interface{}
	if att, ok := inputs["_attachments"].([]interface{}); ok {
		rawAttachments = att
	} else if att, ok := inputs["attachments"].([]interface{}); ok {
		rawAttachments = att
	} else if att, ok := inputs["images"].([]interface{}); ok {
		rawAttachments = att
	}

	for _, raw := range rawAttachments {
		if attMap, ok := raw.(map[string]interface{}); ok {
			if att := extractFromMap(attMap); att != nil {
				attachments = append(attachments, *att)
			}
		}
	}

	// Also check for single image file_id
	if fileID, ok := inputs["image_file_id"].(string); ok && fileID != "" {
		attachments = append(attachments, FileAttachment{
			FileID: fileID,
			Type:   "image",
		})
	}

	// Check all inputs for file references that are images (e.g., from Start block)
	for key, value := range inputs {
		// Skip internal keys
		if strings.HasPrefix(key, "_") || key == "attachments" || key == "images" {
			continue
		}

		// Try map[string]any first
		if attMap, ok := value.(map[string]any); ok {
			log.Printf("🔍 [AGENT-BLOCK] Input '%s' is map[string]any: %+v", key, attMap)
			if att := extractFromMap(attMap); att != nil {
				log.Printf("🖼️ [AGENT-BLOCK] Found image file reference in input '%s': %s", key, att.Filename)
				attachments = append(attachments, *att)
			}
		} else if attMap, ok := value.(map[string]interface{}); ok {
			// Try map[string]interface{} (JSON unmarshaling often produces this)
			log.Printf("🔍 [AGENT-BLOCK] Input '%s' is map[string]interface{}: %+v", key, attMap)
			// Convert to map[string]any
			converted := make(map[string]any)
			for k, v := range attMap {
				converted[k] = v
			}
			if att := extractFromMap(converted); att != nil {
				log.Printf("🖼️ [AGENT-BLOCK] Found image file reference in input '%s': %s", key, att.Filename)
				attachments = append(attachments, *att)
			}
		} else if value != nil {
			log.Printf("🔍 [AGENT-BLOCK] Input '%s' has type %T (not a map)", key, value)
		}
	}

	return attachments
}

// DataFileAttachment represents a data file attachment (CSV, JSON, Excel, etc.)
type DataFileAttachment struct {
	FileID   string `json:"file_id"`
	Filename string `json:"filename"`
	MimeType string `json:"mime_type"`
	Content  string `json:"content"` // Preview content (first ~100 lines)
}

// extractDataFileAttachments extracts data file attachments from inputs
func (e *AgentBlockExecutor) extractDataFileAttachments(inputs map[string]any) []DataFileAttachment {
	var attachments []DataFileAttachment

	// Data file MIME types
	dataTypes := map[string]bool{
		"text/csv":                         true,
		"application/json":                 true,
		"application/vnd.ms-excel":         true,
		"application/vnd.openxmlformats-officedocument.spreadsheetml.sheet": true,
		"text/plain":                       true,
		"text/tab-separated-values":        true,
	}

	// Helper to check if a file is a data file
	isDataFile := func(mimeType, filename string) bool {
		if dataTypes[mimeType] {
			return true
		}
		// Check by extension
		ext := strings.ToLower(filepath.Ext(filename))
		return ext == ".csv" || ext == ".json" || ext == ".xlsx" ||
			ext == ".xls" || ext == ".tsv" || ext == ".txt"
	}

	// Check all inputs for file references
	for key, value := range inputs {
		if strings.HasPrefix(key, "_") {
			continue
		}

		var attMap map[string]any
		if m, ok := value.(map[string]any); ok {
			attMap = m
		} else if m, ok := value.(map[string]interface{}); ok {
			attMap = make(map[string]any)
			for k, v := range m {
				attMap[k] = v
			}
		}

		if attMap == nil {
			continue
		}

		fileID, _ := attMap["file_id"].(string)
		if fileID == "" {
			fileID, _ = attMap["fileId"].(string)
		}
		filename, _ := attMap["filename"].(string)
		mimeType, _ := attMap["mime_type"].(string)
		if mimeType == "" {
			mimeType, _ = attMap["mimeType"].(string)
		}

		if fileID != "" && isDataFile(mimeType, filename) {
			// Read file content from cache
			content := e.readDataFileContent(fileID, filename)
			if content != "" {
				attachments = append(attachments, DataFileAttachment{
					FileID:   fileID,
					Filename: filename,
					MimeType: mimeType,
					Content:  content,
				})
				log.Printf("📊 [AGENT-BLOCK] Found data file in input '%s': %s (%d chars)", key, filename, len(content))
			}
		}
	}

	return attachments
}

// readDataFileContent reads content from a data file (CSV, JSON, etc.)
// Returns a preview (first ~100 lines) suitable for LLM context
func (e *AgentBlockExecutor) readDataFileContent(fileID, filename string) string {
	fileCacheService := filecache.GetService()
	file, found := fileCacheService.Get(fileID)
	if !found {
		log.Printf("⚠️ [AGENT-BLOCK] Data file not found in cache: %s", fileID)
		return ""
	}

	if file.FilePath == "" {
		log.Printf("⚠️ [AGENT-BLOCK] Data file path not available: %s", fileID)
		return ""
	}

	// Read file content
	content, err := os.ReadFile(file.FilePath)
	if err != nil {
		log.Printf("❌ [AGENT-BLOCK] Failed to read data file: %v", err)
		return ""
	}

	// Convert to string and limit to first 100 lines for context
	lines := strings.Split(string(content), "\n")
	maxLines := 100
	if len(lines) > maxLines {
		lines = lines[:maxLines]
	}

	preview := strings.Join(lines, "\n")
	log.Printf("✅ [AGENT-BLOCK] Read data file %s (%d lines, %d bytes)",
		filename, len(lines), len(preview))

	return preview
}

// Artifact represents a generated artifact (chart, image, etc.) from tool execution
type Artifact struct {
	Type   string `json:"type"`   // "chart", "image", "file"
	Format string `json:"format"` // "png", "jpeg", "svg", etc.
	Data   string `json:"data"`   // Base64 encoded data
	Title  string `json:"title"`  // Optional title/description
}

// extractArtifactsFromToolCalls extracts all artifacts (charts, images) from tool call results
// This provides a consistent format for API consumers to access generated visualizations
func (e *AgentBlockExecutor) extractArtifactsFromToolCalls(toolCalls []models.ToolCallRecord) []Artifact {
	var artifacts []Artifact

	for _, tc := range toolCalls {
		if tc.Error != "" || tc.Result == "" {
			continue
		}

		// Parse tool result as JSON
		var resultData map[string]any
		if err := json.Unmarshal([]byte(tc.Result), &resultData); err != nil {
			continue
		}

		// Look for charts/images in common E2B response formats
		// E2B analyze_data returns: {"plots": [{"data": "base64...", "type": "png"}], ...}
		if plots, ok := resultData["plots"].([]interface{}); ok {
			for i, p := range plots {
				if plot, ok := p.(map[string]interface{}); ok {
					data, _ := plot["data"].(string)
					format, _ := plot["type"].(string)
					if format == "" {
						format = "png"
					}
					if data != "" && len(data) > 100 {
						artifacts = append(artifacts, Artifact{
							Type:   "chart",
							Format: format,
							Data:   data,
							Title:  fmt.Sprintf("Chart %d from %s", i+1, tc.Name),
						})
					}
				}
			}
		}

		// Also check for single image/plot fields
		for _, key := range []string{"image", "plot", "chart", "figure", "png", "jpeg"} {
			if data, ok := resultData[key].(string); ok && len(data) > 100 {
				// Determine format from key or data URI
				format := "png"
				if key == "jpeg" {
					format = "jpeg"
				}
				if strings.HasPrefix(data, "data:image/") {
					// Extract format from data URI
					if strings.Contains(data, "jpeg") || strings.Contains(data, "jpg") {
						format = "jpeg"
					} else if strings.Contains(data, "svg") {
						format = "svg"
					}
				}

				artifacts = append(artifacts, Artifact{
					Type:   "chart",
					Format: format,
					Data:   data,
					Title:  fmt.Sprintf("Generated %s from %s", key, tc.Name),
				})
			}
		}

		// Check for base64_images array (another common E2B format)
		if images, ok := resultData["base64_images"].([]interface{}); ok {
			for i, img := range images {
				if imgData, ok := img.(string); ok && len(imgData) > 100 {
					artifacts = append(artifacts, Artifact{
						Type:   "chart",
						Format: "png",
						Data:   imgData,
						Title:  fmt.Sprintf("Image %d from %s", i+1, tc.Name),
					})
				}
			}
		}
	}

	log.Printf("📊 [AGENT-BLOCK] Extracted %d artifacts from tool calls", len(artifacts))
	return artifacts
}

// GeneratedFile represents a file generated by a tool (PDF, document, etc.)
type GeneratedFile struct {
	FileID      string `json:"file_id"`
	Filename    string `json:"filename"`
	DownloadURL string `json:"download_url"`
	AccessCode  string `json:"access_code,omitempty"`
	Size        int64  `json:"size,omitempty"`
	MimeType    string `json:"mime_type,omitempty"`
}

// extractGeneratedFilesFromToolCalls extracts file references (PDFs, documents) from tool call results
// This makes download URLs available to subsequent blocks
func (e *AgentBlockExecutor) extractGeneratedFilesFromToolCalls(toolCalls []models.ToolCallRecord) []GeneratedFile {
	var files []GeneratedFile

	for _, tc := range toolCalls {
		if tc.Error != "" || tc.Result == "" {
			continue
		}

		// Parse tool result as JSON
		var resultData map[string]any
		if err := json.Unmarshal([]byte(tc.Result), &resultData); err != nil {
			continue
		}

		// Look for file reference fields
		fileRef := GeneratedFile{}

		if v, ok := resultData["file_id"].(string); ok && v != "" {
			fileRef.FileID = v
		}
		if v, ok := resultData["filename"].(string); ok && v != "" {
			fileRef.Filename = v
		}
		if v, ok := resultData["download_url"].(string); ok && v != "" {
			fileRef.DownloadURL = v
		}
		if v, ok := resultData["access_code"].(string); ok && v != "" {
			fileRef.AccessCode = v
		}
		if v, ok := resultData["size"].(float64); ok {
			fileRef.Size = int64(v)
		}
		if v, ok := resultData["mime_type"].(string); ok && v != "" {
			fileRef.MimeType = v
		}

		// Only add if we have meaningful file reference data
		if fileRef.FileID != "" || fileRef.DownloadURL != "" {
			files = append(files, fileRef)
			log.Printf("📄 [AGENT-BLOCK] Extracted file reference: %s (url: %s)", fileRef.Filename, fileRef.DownloadURL)
		}
	}

	return files
}

// sanitizeToolResultForLLM removes base64 image data from tool results
// Base64 images are huge and useless to the LLM as text - it can't "see" them
// Instead, we replace them with a placeholder indicating a chart was generated
func (e *AgentBlockExecutor) sanitizeToolResultForLLM(result string) string {
	if len(result) < 1000 {
		return result // Small results don't need sanitization
	}

	chartsGenerated := false

	// Pattern to match base64 image data (PNG, JPEG, etc.)
	// Matches: "data:image/png;base64,..." or just long base64 strings
	base64Pattern := regexp.MustCompile(`"data:image/[^;]+;base64,[A-Za-z0-9+/=]{100,}"`)
	if base64Pattern.MatchString(result) {
		chartsGenerated = true
	}
	sanitized := base64Pattern.ReplaceAllString(result, `"[CHART_IMAGE_SAVED]"`)

	// Also match standalone base64 blocks that might not have data URI prefix
	// Look for very long strings of base64 characters (>500 chars)
	longBase64Pattern := regexp.MustCompile(`"[A-Za-z0-9+/=]{500,}"`)
	if longBase64Pattern.MatchString(sanitized) {
		chartsGenerated = true
	}
	sanitized = longBase64Pattern.ReplaceAllString(sanitized, `"[CHART_IMAGE_SAVED]"`)

	// Also handle base64 in "image" or "plot" fields common in E2B responses
	imageFieldPattern := regexp.MustCompile(`"(image|plot|chart|png|jpeg|figure)":\s*"[A-Za-z0-9+/=]{100,}"`)
	sanitized = imageFieldPattern.ReplaceAllString(sanitized, `"$1": "[CHART_IMAGE_SAVED]"`)

	// Truncate if still too long (max 20KB for tool results)
	maxLen := 20000
	if len(sanitized) > maxLen {
		sanitized = sanitized[:maxLen] + "\n... [TRUNCATED - Full result too large for LLM context]"
	}

	// Add clear instruction when charts were generated
	if chartsGenerated {
		sanitized = sanitized + "\n\n[CHARTS SUCCESSFULLY GENERATED AND SAVED. Do NOT call analyze_data again. Provide your final summary/insights based on the analysis output above.]"
	}

	originalLen := len(result)
	newLen := len(sanitized)
	if originalLen != newLen {
		log.Printf("🧹 [AGENT-BLOCK] Sanitized tool result: %d -> %d chars (removed base64/large data, charts=%v)",
			originalLen, newLen, chartsGenerated)
	}

	return sanitized
}

// extractChartsFromResult extracts base64 chart images from tool results
// This is used to collect charts for auto-injection into Discord/Slack messages
func (e *AgentBlockExecutor) extractChartsFromResult(result string) []string {
	var charts []string

	// Try to parse as JSON
	var resultData map[string]any
	if err := json.Unmarshal([]byte(result), &resultData); err != nil {
		return charts
	}

	// Look for plots array (E2B analyze_data format)
	if plots, ok := resultData["plots"].([]interface{}); ok {
		for _, p := range plots {
			if plot, ok := p.(map[string]interface{}); ok {
				// Check for "data" field containing base64
				if data, ok := plot["data"].(string); ok && len(data) > 100 {
					charts = append(charts, data)
				}
			}
		}
	}

	// Also check for single "image" or "chart" field
	for _, key := range []string{"image", "chart", "plot", "figure"} {
		if data, ok := resultData[key].(string); ok && len(data) > 100 {
			charts = append(charts, data)
		}
	}

	return charts
}

// extractChartsFromInputs extracts chart images from previous block artifacts
// This allows downstream blocks (like Discord Publisher) to access charts generated upstream
func (e *AgentBlockExecutor) extractChartsFromInputs(inputs map[string]any) []string {
	var charts []string

	// Helper function to extract charts from artifacts slice
	extractFromArtifacts := func(artifacts []Artifact) {
		for _, artifact := range artifacts {
			if artifact.Type == "chart" && artifact.Data != "" && len(artifact.Data) > 100 {
				charts = append(charts, artifact.Data)
				log.Printf("🖼️ [AGENT-BLOCK] Found chart artifact: %s (format: %s, %d bytes)",
					artifact.Title, artifact.Format, len(artifact.Data))
			}
		}
	}

	// Helper to try converting interface to []Artifact
	tryConvertArtifacts := func(v interface{}) bool {
		// Direct type assertion for []Artifact
		if artifacts, ok := v.([]Artifact); ok {
			extractFromArtifacts(artifacts)
			return true
		}
		// Try []execution.Artifact (same type, different reference)
		if artifacts, ok := v.([]interface{}); ok {
			for _, a := range artifacts {
				if artifact, ok := a.(Artifact); ok {
					if artifact.Type == "chart" && artifact.Data != "" && len(artifact.Data) > 100 {
						charts = append(charts, artifact.Data)
					}
				} else if artifactMap, ok := a.(map[string]interface{}); ok {
					// Handle map representation of artifact
					artifactType, _ := artifactMap["type"].(string)
					artifactData, _ := artifactMap["data"].(string)
					if artifactType == "chart" && artifactData != "" && len(artifactData) > 100 {
						charts = append(charts, artifactData)
					}
				}
			}
			return true
		}
		return false
	}

	// 1. Check direct "artifacts" key in inputs
	if artifacts, ok := inputs["artifacts"]; ok {
		tryConvertArtifacts(artifacts)
	}

	// 2. Check previous block outputs (e.g., inputs["data-analyzer"]["artifacts"])
	// These are stored as map[string]any with block IDs as keys
	for key, value := range inputs {
		// Skip non-block keys
		if key == "artifacts" || key == "input" || key == "value" || key == "start" ||
			strings.HasPrefix(key, "_") || strings.HasPrefix(key, "__") {
			continue
		}

		// Check if value is a map (previous block output)
		if blockOutput, ok := value.(map[string]any); ok {
			// Look for artifacts in this block's output
			if artifacts, ok := blockOutput["artifacts"]; ok {
				tryConvertArtifacts(artifacts)
			}

			// Also check for nested output.artifacts
			if output, ok := blockOutput["output"].(map[string]any); ok {
				if artifacts, ok := output["artifacts"]; ok {
					tryConvertArtifacts(artifacts)
				}
			}

			// Check toolCalls for chart results (some tools return charts in result)
			if toolCalls, ok := blockOutput["toolCalls"].([]models.ToolCallRecord); ok {
				for _, tc := range toolCalls {
					if tc.Result != "" {
						extractedCharts := e.extractChartsFromResult(tc.Result)
						charts = append(charts, extractedCharts...)
					}
				}
			}
			// Also try interface{} slice for toolCalls
			if toolCalls, ok := blockOutput["toolCalls"].([]interface{}); ok {
				for _, tc := range toolCalls {
					if tcMap, ok := tc.(map[string]interface{}); ok {
						if result, ok := tcMap["Result"].(string); ok && result != "" {
							extractedCharts := e.extractChartsFromResult(result)
							charts = append(charts, extractedCharts...)
						}
					}
				}
			}
		}
	}

	return charts
}

// NOTE: detectToolsFromContext was removed - blocks now only use explicitly configured tools
// This ensures predictable behavior where users must configure enabledTools in the block settings

// extractToolResultsForDownstream parses tool call results and extracts data for downstream blocks
// This solves the problem where tool output was buried in toolCalls and not accessible to next blocks
func (e *AgentBlockExecutor) extractToolResultsForDownstream(toolCalls []models.ToolCallRecord) map[string]any {
	results := make(map[string]any)

	for _, tc := range toolCalls {
		if tc.Error != "" || tc.Result == "" {
			continue
		}

		// Try to parse result as JSON
		var parsed map[string]any
		if err := json.Unmarshal([]byte(tc.Result), &parsed); err != nil {
			// Not JSON - store as raw string
			results[tc.Name] = tc.Result
			continue
		}

		// Store parsed result under tool name
		results[tc.Name] = parsed

		log.Printf("📦 [AGENT-BLOCK] Extracted tool result for '%s': %d fields", tc.Name, len(parsed))
	}

	return results
}

// isOpenAIVisionModel checks if the model is an OpenAI vision-capable model
func (e *AgentBlockExecutor) isOpenAIVisionModel(modelID string) bool {
	// OpenAI vision-capable models
	visionModels := []string{
		"gpt-4o",
		"gpt-4o-mini",
		"gpt-4-turbo",
		"gpt-4-vision-preview",
		"gpt-4-turbo-preview",
		"gpt-5",      // GPT-5 series (all variants support vision)
		"gpt-5.1",    // GPT-5.1 series
		"o1",         // o1 models
		"o1-preview",
		"o1-mini",
		"o3",         // o3 models
		"o3-mini",
		"o4-mini",    // o4-mini (if released)
	}

	modelLower := strings.ToLower(modelID)
	for _, vm := range visionModels {
		if strings.Contains(modelLower, vm) {
			return true
		}
	}

	// Also check model aliases that might map to vision models
	// Common patterns: any 4o variant or 5.x variant
	if strings.Contains(modelLower, "gpt-4o") || strings.Contains(modelLower, "4o") ||
		strings.Contains(modelLower, "gpt-5") || strings.Contains(modelLower, "5.1") {
		return true
	}

	return false
}

// getImageAsBase64DataURL converts an image file to a base64 data URL
func (e *AgentBlockExecutor) getImageAsBase64DataURL(fileID string) string {
	// Get file from cache
	fileCacheService := filecache.GetService()
	file, found := fileCacheService.Get(fileID)
	if !found {
		log.Printf("⚠️ [AGENT-BLOCK] Image file not found: %s", fileID)
		return ""
	}

	// Verify it's an image
	if !strings.HasPrefix(file.MimeType, "image/") {
		log.Printf("⚠️ [AGENT-BLOCK] File is not an image: %s (%s)", fileID, file.MimeType)
		return ""
	}

	// Read image from disk
	if file.FilePath == "" {
		log.Printf("⚠️ [AGENT-BLOCK] Image file path not available: %s", fileID)
		return ""
	}

	imageData, err := os.ReadFile(file.FilePath)
	if err != nil {
		log.Printf("❌ [AGENT-BLOCK] Failed to read image file: %v", err)
		return ""
	}

	// Convert to base64 data URL
	base64Image := base64.StdEncoding.EncodeToString(imageData)
	dataURL := fmt.Sprintf("data:%s;base64,%s", file.MimeType, base64Image)

	log.Printf("✅ [AGENT-BLOCK] Converted image to base64 (%d bytes)", len(imageData))
	return dataURL
}

// getToolDescriptions returns human-readable descriptions of enabled tools
// This helps the LLM understand how to use the tools, including key parameters
func (e *AgentBlockExecutor) getToolDescriptions(enabledTools []string) []string {
	if len(enabledTools) == 0 {
		return nil
	}

	enabledSet := make(map[string]bool)
	for _, name := range enabledTools {
		enabledSet[name] = true
	}

	var descriptions []string
	allTools := e.toolRegistry.List()

	for _, tool := range allTools {
		if fn, ok := tool["function"].(map[string]interface{}); ok {
			name, _ := fn["name"].(string)
			if !enabledSet[name] {
				continue
			}

			desc, _ := fn["description"].(string)
			// Truncate long descriptions but keep key info
			if len(desc) > 500 {
				desc = desc[:500] + "..."
			}

			// Build a concise tool summary
			var sb strings.Builder
			sb.WriteString(fmt.Sprintf("### %s\n", name))
			if desc != "" {
				sb.WriteString(fmt.Sprintf("%s\n", desc))
			}

			// Extract key parameters to highlight
			if params, ok := fn["parameters"].(map[string]interface{}); ok {
				if props, ok := params["properties"].(map[string]interface{}); ok {
					var keyParams []string
					for paramName, paramDef := range props {
						// Skip internal parameters
						if strings.HasPrefix(paramName, "_") || paramName == "credential_id" || paramName == "api_key" {
							continue
						}
						if paramMap, ok := paramDef.(map[string]interface{}); ok {
							paramDesc, _ := paramMap["description"].(string)
							// Highlight important params like file_url
							if paramName == "file_url" || paramName == "download_url" {
								keyParams = append(keyParams, fmt.Sprintf("  - **%s**: %s", paramName, paramDesc))
							} else if len(keyParams) < 5 { // Limit to 5 params
								shortDesc := paramDesc
								if len(shortDesc) > 100 {
									shortDesc = shortDesc[:100] + "..."
								}
								keyParams = append(keyParams, fmt.Sprintf("  - %s: %s", paramName, shortDesc))
							}
						}
					}
					if len(keyParams) > 0 {
						sb.WriteString("Key parameters:\n")
						for _, p := range keyParams {
							sb.WriteString(p + "\n")
						}
					}
				}
			}

			descriptions = append(descriptions, sb.String())
		}
	}

	return descriptions
}

// filterTools returns only the tools that are enabled for this block
func (e *AgentBlockExecutor) filterTools(enabledTools []string) []map[string]interface{} {
	if len(enabledTools) == 0 {
		// No tools enabled for this block
		return nil
	}

	// Get all available tools
	allTools := e.toolRegistry.List()

	// Filter to only enabled tools
	var filtered []map[string]interface{}
	enabledSet := make(map[string]bool)
	for _, name := range enabledTools {
		enabledSet[name] = true
	}

	for _, tool := range allTools {
		if fn, ok := tool["function"].(map[string]interface{}); ok {
			if name, ok := fn["name"].(string); ok {
				if enabledSet[name] {
					filtered = append(filtered, tool)
				}
			}
		}
	}

	return filtered
}

// LLMResponse represents the response from the LLM
type LLMResponse struct {
	Content      string
	ToolCalls    []map[string]any
	FinishReason string // "stop", "tool_calls", "end_turn", etc.
	InputTokens  int
	OutputTokens int
}

// callLLM makes a streaming call to the LLM (required for DobbyAI API compatibility)
func (e *AgentBlockExecutor) callLLM(
	ctx context.Context,
	provider *models.Provider,
	modelID string,
	messages []map[string]any,
	tools []map[string]interface{},
	temperature float64,
) (*LLMResponse, error) {
	return e.callLLMWithSchema(ctx, provider, modelID, messages, tools, temperature, nil)
}

// callLLMWithSchema calls the LLM with optional native structured output support
func (e *AgentBlockExecutor) callLLMWithSchema(
	ctx context.Context,
	provider *models.Provider,
	modelID string,
	messages []map[string]any,
	tools []map[string]interface{},
	temperature float64,
	outputSchema *models.JSONSchema,
) (*LLMResponse, error) {

	// Detect provider type by base URL to avoid sending incompatible parameters
	// OpenAI's API is strict and rejects unknown parameters with 400 errors
	isOpenAI := strings.Contains(strings.ToLower(provider.BaseURL), "openai.com")
	isOpenRouter := strings.Contains(strings.ToLower(provider.BaseURL), "openrouter.ai")
	isGLM := strings.Contains(strings.ToLower(provider.BaseURL), "bigmodel.cn") ||
		strings.Contains(strings.ToLower(provider.Name), "glm") ||
		strings.Contains(strings.ToLower(modelID), "glm")

	// Check if the model supports native structured output
	// OpenAI GPT-4o models and newer support json_schema response_format
	supportsStructuredOutput := (isOpenAI || isOpenRouter) && outputSchema != nil && len(tools) == 0

	// Build request body - use streaming for better compatibility with DobbyAI API
	requestBody := map[string]interface{}{
		"model":       modelID,
		"messages":    messages,
		"temperature": temperature,
		"stream":      true, // Use streaming - DobbyAI API works better with streaming
	}

	// Use correct token limit parameter based on provider
	// OpenAI newer models (GPT-4o, o1, etc.) require max_completion_tokens instead of max_tokens
	// Most models support 65K+ output tokens, so we use 32768 as a safe high limit
	if isOpenAI {
		requestBody["max_completion_tokens"] = 32768
	} else {
		requestBody["max_tokens"] = 32768
	}

	// Add native structured output if supported and no tools are being used
	// Note: Can't use response_format with tools - they're mutually exclusive
	if supportsStructuredOutput {
		// Convert JSONSchema to OpenAI's response_format structure
		schemaMap := e.jsonSchemaToMap(outputSchema)
		requestBody["response_format"] = map[string]interface{}{
			"type": "json_schema",
			"json_schema": map[string]interface{}{
				"name":   "structured_output",
				"strict": true,
				"schema": schemaMap,
			},
		}
		log.Printf("📋 [AGENT-BLOCK] Using native structured output (json_schema response_format)")
	}

	// Add provider-specific parameters only where supported
	// Note: Do NOT send unknown parameters to strict APIs (Google, OpenAI, etc.) — they return 400
	isGoogle := strings.Contains(strings.ToLower(provider.BaseURL), "googleapis.com") ||
		strings.Contains(strings.ToLower(provider.BaseURL), "generativelanguage") ||
		strings.Contains(strings.ToLower(provider.Name), "google") ||
		strings.Contains(strings.ToLower(provider.Name), "gemini")
	isQwenOrDeepSeek := strings.Contains(strings.ToLower(provider.BaseURL), "dashscope") ||
		strings.Contains(strings.ToLower(provider.BaseURL), "deepseek.com") ||
		strings.Contains(strings.ToLower(modelID), "qwen") ||
		strings.Contains(strings.ToLower(modelID), "deepseek")

	if isGLM {
		// GLM-specific parameters to disable reasoning mode
		requestBody["think"] = false
		requestBody["do_sample"] = true
		requestBody["top_p"] = 0.95
	} else if isQwenOrDeepSeek && !isGoogle && !isOpenAI && !isOpenRouter {
		// Only send enable_thinking to providers that actually support it (Qwen/DeepSeek)
		requestBody["enable_thinking"] = false
	}
	// All other providers (Google, etc.) get no extra parameters — their APIs reject unknown fields

	// Only include tools if non-empty
	if len(tools) > 0 {
		requestBody["tools"] = tools
	}

	bodyBytes, err := json.Marshal(requestBody)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal request: %w", err)
	}

	// Create request
	endpoint := strings.TrimSuffix(provider.BaseURL, "/") + "/chat/completions"
	log.Printf("🌐 [AGENT-BLOCK] Calling LLM: %s (model: %s, streaming: true)", endpoint, modelID)

	req, err := http.NewRequestWithContext(ctx, "POST", endpoint, bytes.NewReader(bodyBytes))
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}

	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+provider.APIKey)

	// Execute request
	resp, err := e.httpClient.Do(req)
	if err != nil {
		// Classify network/connection errors for retry logic
		return nil, ClassifyError(err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		// Classify HTTP errors for retry logic (429, 5xx are retryable)
		return nil, ClassifyHTTPError(resp.StatusCode, string(body))
	}

	// Process SSE stream and accumulate response
	return e.processStreamResponse(resp.Body)
}

// callLLMWithRetry wraps callLLM with retry logic for transient errors
// Returns the response, retry attempts history, and any final error
func (e *AgentBlockExecutor) callLLMWithRetry(
	ctx context.Context,
	provider *models.Provider,
	modelID string,
	messages []map[string]any,
	tools []map[string]interface{},
	temperature float64,
	retryPolicy *models.RetryPolicy,
) (*LLMResponse, []models.RetryAttempt, error) {
	return e.callLLMWithRetryAndSchema(ctx, provider, modelID, messages, tools, temperature, retryPolicy, nil)
}

// callLLMWithRetryAndSchema wraps callLLMWithSchema with retry logic for transient errors
// Returns the response, retry attempts history, and any final error
func (e *AgentBlockExecutor) callLLMWithRetryAndSchema(
	ctx context.Context,
	provider *models.Provider,
	modelID string,
	messages []map[string]any,
	tools []map[string]interface{},
	temperature float64,
	retryPolicy *models.RetryPolicy,
	outputSchema *models.JSONSchema,
) (*LLMResponse, []models.RetryAttempt, error) {

	// Use default policy if not specified
	if retryPolicy == nil {
		retryPolicy = models.DefaultRetryPolicy()
	}

	// Create backoff calculator
	backoff := NewBackoffCalculator(
		retryPolicy.InitialDelay,
		retryPolicy.MaxDelay,
		retryPolicy.BackoffMultiplier,
		retryPolicy.JitterPercent,
	)

	var attempts []models.RetryAttempt

	for attempt := 0; attempt <= retryPolicy.MaxRetries; attempt++ {
		attemptStart := time.Now()

		// Make the LLM call with optional schema
		response, err := e.callLLMWithSchema(ctx, provider, modelID, messages, tools, temperature, outputSchema)
		attemptDuration := time.Since(attemptStart).Milliseconds()

		if err == nil {
			// Success!
			if attempt > 0 {
				log.Printf("✅ [AGENT-BLOCK] LLM call succeeded on retry attempt %d", attempt)
			}
			return response, attempts, nil
		}

		// Classify the error (may already be classified from callLLM)
		var execErr *ExecutionError
		if e, ok := err.(*ExecutionError); ok {
			execErr = e
		} else {
			execErr = ClassifyError(err)
		}

		// Determine error type string for logging and tracking
		errorType := "unknown"
		if execErr.StatusCode == 429 {
			errorType = "rate_limit"
		} else if execErr.StatusCode >= 500 {
			errorType = "server_error"
		} else if strings.Contains(strings.ToLower(execErr.Message), "timeout") ||
			strings.Contains(strings.ToLower(execErr.Message), "deadline") {
			errorType = "timeout"
		} else if strings.Contains(strings.ToLower(execErr.Message), "network") ||
			strings.Contains(strings.ToLower(execErr.Message), "connection") {
			errorType = "network_error"
		}

		// Record this attempt
		attempts = append(attempts, models.RetryAttempt{
			Attempt:   attempt,
			Error:     execErr.Message,
			ErrorType: errorType,
			Timestamp: attemptStart,
			Duration:  attemptDuration,
		})

		// Check if we should retry
		if attempt < retryPolicy.MaxRetries && ShouldRetry(execErr, retryPolicy.RetryOn) {
			delay := backoff.NextDelay(attempt)

			// Use RetryAfter if available and longer (e.g., from 429 response)
			if execErr.RetryAfter > 0 {
				retryAfterDelay := time.Duration(execErr.RetryAfter) * time.Second
				if retryAfterDelay > delay {
					delay = retryAfterDelay
				}
			}

			log.Printf("🔄 [AGENT-BLOCK] LLM call failed (attempt %d/%d): %s [%s]. Retrying in %v",
				attempt+1, retryPolicy.MaxRetries+1, execErr.Message, errorType, delay)

			// Wait before retry (respecting context cancellation)
			select {
			case <-time.After(delay):
				// Continue to next attempt
			case <-ctx.Done():
				return nil, attempts, &ExecutionError{
					Category:  ErrorCategoryTransient,
					Message:   "Context cancelled during retry wait",
					Retryable: false,
					Cause:     ctx.Err(),
				}
			}
		} else {
			// Not retryable or max retries exceeded
			if attempt >= retryPolicy.MaxRetries {
				log.Printf("❌ [AGENT-BLOCK] LLM call failed after %d attempt(s): %s [%s] (max retries exceeded)",
					attempt+1, execErr.Message, errorType)
			} else {
				log.Printf("❌ [AGENT-BLOCK] LLM call failed: %s [%s] (not retryable)",
					execErr.Message, errorType)
			}
			return nil, attempts, execErr
		}
	}

	// Should not reach here, but safety fallback
	return nil, attempts, &ExecutionError{
		Category:  ErrorCategoryUnknown,
		Message:   "Max retries exceeded",
		Retryable: false,
	}
}

// processStreamResponse processes SSE stream and returns accumulated response
func (e *AgentBlockExecutor) processStreamResponse(reader io.Reader) (*LLMResponse, error) {
	response := &LLMResponse{}
	var contentBuilder strings.Builder

	// Track tool calls by index to accumulate streaming arguments
	toolCallsMap := make(map[int]*toolCallAccumulator)

	scanner := bufio.NewScanner(reader)
	for scanner.Scan() {
		line := scanner.Text()
		if !strings.HasPrefix(line, "data: ") {
			continue
		}

		data := strings.TrimPrefix(line, "data: ")
		if data == "[DONE]" {
			break
		}

		var chunk map[string]interface{}
		if err := json.Unmarshal([]byte(data), &chunk); err != nil {
			continue
		}

		choices, ok := chunk["choices"].([]interface{})
		if !ok || len(choices) == 0 {
			continue
		}

		choice := choices[0].(map[string]interface{})

		// Capture finish_reason when available (usually in the final chunk)
		if finishReason, ok := choice["finish_reason"].(string); ok && finishReason != "" {
			response.FinishReason = finishReason
		}

		delta, ok := choice["delta"].(map[string]interface{})
		if !ok {
			continue
		}

		// Accumulate content chunks
		if content, ok := delta["content"].(string); ok {
			contentBuilder.WriteString(content)
		}

		// Accumulate tool calls
		if toolCallsData, ok := delta["tool_calls"].([]interface{}); ok {
			for _, tc := range toolCallsData {
				toolCallChunk := tc.(map[string]interface{})

				// Get tool call index
				var index int
				if idx, ok := toolCallChunk["index"].(float64); ok {
					index = int(idx)
				}

				// Initialize accumulator if needed
				if _, exists := toolCallsMap[index]; !exists {
					toolCallsMap[index] = &toolCallAccumulator{}
				}

				acc := toolCallsMap[index]

				// Accumulate fields
				if id, ok := toolCallChunk["id"].(string); ok {
					acc.ID = id
				}
				if typ, ok := toolCallChunk["type"].(string); ok {
					acc.Type = typ
				}
				if function, ok := toolCallChunk["function"].(map[string]interface{}); ok {
					if name, ok := function["name"].(string); ok {
						acc.Name = name
					}
					if args, ok := function["arguments"].(string); ok {
						acc.Arguments.WriteString(args)
					}
				}
			}
		}

		// Extract token usage from chunk (some APIs include it in each chunk)
		if usage, ok := chunk["usage"].(map[string]interface{}); ok {
			if pt, ok := usage["prompt_tokens"].(float64); ok {
				response.InputTokens = int(pt)
			}
			if ct, ok := usage["completion_tokens"].(float64); ok {
				response.OutputTokens = int(ct)
			}
		}
	}

	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("error reading stream: %w", err)
	}

	// Set accumulated content
	response.Content = contentBuilder.String()

	// Convert accumulated tool calls to response format
	for _, acc := range toolCallsMap {
		if acc.Name != "" {
			toolCall := map[string]any{
				"id":   acc.ID,
				"type": acc.Type,
				"function": map[string]any{
					"name":      acc.Name,
					"arguments": acc.Arguments.String(),
				},
			}
			response.ToolCalls = append(response.ToolCalls, toolCall)
		}
	}

	log.Printf("✅ [AGENT-BLOCK] Stream processed: content=%d chars, toolCalls=%d, finishReason=%s",
		len(response.Content), len(response.ToolCalls), response.FinishReason)

	return response, nil
}

// toolCallAccumulator accumulates streaming tool call data
type toolCallAccumulator struct {
	ID        string
	Type      string
	Name      string
	Arguments strings.Builder
}

// executeToolCall executes a single tool call and returns the record
func (e *AgentBlockExecutor) executeToolCall(toolCall map[string]any, blockInputs map[string]any, dataFiles []DataFileAttachment, generatedCharts []string, userID string, credentials []string) models.ToolCallRecord {
	startTime := time.Now()

	record := models.ToolCallRecord{
		Arguments: make(map[string]any),
	}

	// Extract tool name and arguments
	if fn, ok := toolCall["function"].(map[string]any); ok {
		if name, ok := fn["name"].(string); ok {
			record.Name = name
		}
		if argsStr, ok := fn["arguments"].(string); ok {
			if err := json.Unmarshal([]byte(argsStr), &record.Arguments); err != nil {
				record.Error = fmt.Sprintf("failed to parse arguments: %v", err)
				record.Duration = time.Since(startTime).Milliseconds()
				return record
			}
		}
	}

	if record.Name == "" {
		record.Error = "missing tool name"
		record.Duration = time.Since(startTime).Milliseconds()
		return record
	}

	// Interpolate template variables in tool arguments
	// This allows tool calls to use {{input}} or other block outputs
	record.Arguments = InterpolateMapValues(record.Arguments, blockInputs)

	// AUTO-INJECT CSV DATA for analyze_data tool
	// This fixes the issue where LLM uses filename as file_id (which doesn't exist in cache)
	// Instead of relying on file lookup, we inject the already-extracted data directly
	if record.Name == "analyze_data" && len(dataFiles) > 0 {
		// Check if csv_data is already provided and non-empty
		existingCSV, hasCSV := record.Arguments["csv_data"].(string)
		if !hasCSV || existingCSV == "" {
			// No csv_data provided - inject from our extracted data files
			dataFile := dataFiles[0] // Use first data file
			if dataFile.Content != "" {
				record.Arguments["csv_data"] = dataFile.Content
				// Clear file_id to prevent lookup attempts with invalid IDs
				delete(record.Arguments, "file_id")
				log.Printf("📊 [AGENT-BLOCK] Auto-injected csv_data from '%s' (%d chars) - bypassing file cache lookup",
					dataFile.Filename, len(dataFile.Content))
			}
		}
	}

	// AUTO-INJECT CHART IMAGES for Discord/Slack messages
	// The LLM may not include image_data at all, or use placeholders - we need the real base64
	if (record.Name == "send_discord_message" || record.Name == "send_slack_message") && len(generatedCharts) > 0 {
		chartToInject := generatedCharts[len(generatedCharts)-1] // Use most recent chart

		// Check if image_data exists and is valid
		imageData, hasImageData := record.Arguments["image_data"].(string)

		shouldInject := false
		if !hasImageData || imageData == "" {
			// No image_data provided - inject the chart
			shouldInject = true
			log.Printf("🖼️ [AGENT-BLOCK] No image_data in tool call, will auto-inject chart")
		} else if len(imageData) < 100 {
			// Placeholder or short text - not real base64
			shouldInject = true
			log.Printf("🖼️ [AGENT-BLOCK] image_data is placeholder (%d chars), will auto-inject chart", len(imageData))
		} else if strings.Contains(imageData, "[CHART_IMAGE_SAVED]") || strings.Contains(imageData, "[BASE64") {
			// Explicit placeholder text
			shouldInject = true
			log.Printf("🖼️ [AGENT-BLOCK] image_data contains placeholder text, will auto-inject chart")
		}

		if shouldInject {
			record.Arguments["image_data"] = chartToInject
			// Also set a filename if not already set
			if _, hasFilename := record.Arguments["image_filename"].(string); !hasFilename {
				record.Arguments["image_filename"] = "chart.png"
			}
			log.Printf("📊 [AGENT-BLOCK] Auto-injected chart image into %s (%d bytes)",
				record.Name, len(chartToInject))
		}
	}

	// Inject credential resolver for tools that need authentication
	// The resolver is user-scoped for security - only credentials owned by userID can be accessed
	var resolver tools.CredentialResolver
	if e.credentialService != nil && userID != "" {
		resolver = e.credentialService.CreateCredentialResolver(userID)
		record.Arguments[tools.CredentialResolverKey] = resolver
		record.Arguments[tools.UserIDKey] = userID
	}

	// Auto-inject credential_id for tools that need it
	// This allows LLM to NOT know about credentials - we handle it automatically
	toolIntegrationType := tools.GetIntegrationTypeForTool(record.Name)
	if toolIntegrationType != "" && e.credentialService != nil && userID != "" {
		var credentialID string

		// First, try to find from explicitly configured credentials
		if len(credentials) > 0 && resolver != nil {
			credentialID = findCredentialForIntegrationType(credentials, toolIntegrationType, resolver)
			if credentialID != "" {
				log.Printf("🔐 [AGENT-BLOCK] Found credential_id=%s from block config for tool=%s",
					credentialID, record.Name)
			}
		}

		// If no credential found in block config, try runtime auto-discovery from user's credentials
		if credentialID == "" {
			log.Printf("🔍 [AGENT-BLOCK] No credentials in block config for tool=%s, trying runtime auto-discovery...", record.Name)
			ctx := context.Background()
			userCreds, err := e.credentialService.ListByUserAndType(ctx, userID, toolIntegrationType)
			if err != nil {
				log.Printf("⚠️ [AGENT-BLOCK] Failed to fetch user credentials: %v", err)
			} else if len(userCreds) == 1 {
				// Exactly one credential of this type - auto-use it
				credentialID = userCreds[0].ID
				log.Printf("🔐 [AGENT-BLOCK] Runtime auto-discovered single credential: %s (%s) for tool=%s",
					userCreds[0].Name, credentialID, record.Name)
			} else if len(userCreds) > 1 {
				log.Printf("⚠️ [AGENT-BLOCK] Multiple credentials (%d) found for %s - cannot auto-select. User should configure in Block Settings.",
					len(userCreds), toolIntegrationType)
			} else {
				log.Printf("⚠️ [AGENT-BLOCK] No %s credentials found for user. Please add one in Credentials Manager.",
					toolIntegrationType)
			}
		}

		// Inject the credential_id if we found one
		if credentialID != "" {
			record.Arguments["credential_id"] = credentialID
			log.Printf("🔐 [AGENT-BLOCK] Auto-injected credential_id=%s for tool=%s (type=%s)",
				credentialID, record.Name, toolIntegrationType)
		}
	}

	// Inject image provider config for generate_image tool
	if record.Name == "generate_image" {
		imageProviderService := services.GetImageProviderService()
		provider := imageProviderService.GetProvider()
		if provider != nil {
			record.Arguments[tools.ImageProviderConfigKey] = &tools.ImageProviderConfig{
				Name:         provider.Name,
				BaseURL:      provider.BaseURL,
				APIKey:       provider.APIKey,
				DefaultModel: provider.DefaultModel,
			}
			log.Printf("🎨 [AGENT-BLOCK] Injected image provider: %s (model: %s)", provider.Name, provider.DefaultModel)
		} else {
			log.Printf("⚠️ [AGENT-BLOCK] No image provider configured for generate_image tool")
		}
	}

	log.Printf("🔧 [AGENT-BLOCK] Executing tool: %s with args: %+v", record.Name, record.Arguments)

	// Execute the tool
	result, err := e.toolRegistry.Execute(record.Name, record.Arguments)
	if err != nil {
		record.Error = err.Error()
		log.Printf("❌ [AGENT-BLOCK] Tool %s failed: %v", record.Name, err)
	} else {
		record.Result = result
		log.Printf("✅ [AGENT-BLOCK] Tool %s succeeded (result length: %d)", record.Name, len(result))
	}

	record.Duration = time.Since(startTime).Milliseconds()

	// Clean up internal keys from Arguments before storing
	// These are injected for tool execution but should not be serialized
	delete(record.Arguments, tools.CredentialResolverKey)
	delete(record.Arguments, tools.UserIDKey)
	delete(record.Arguments, tools.ImageProviderConfigKey)

	return record
}

// getToolName extracts the tool name from a tool call
func (e *AgentBlockExecutor) getToolName(toolCall map[string]any) string {
	if fn, ok := toolCall["function"].(map[string]any); ok {
		if name, ok := fn["name"].(string); ok {
			return name
		}
	}
	return ""
}

// parseAndValidateOutput parses the LLM response and validates against schema
func (e *AgentBlockExecutor) parseAndValidateOutput(
	content string,
	schema *models.JSONSchema,
	strict bool,
) (map[string]any, error) {
	log.Printf("📋 [VALIDATE] parseAndValidateOutput called, schema=%v, strict=%v", schema != nil, strict)

	// If no schema provided, try to parse as JSON or return as-is
	if schema == nil {
		log.Printf("📋 [VALIDATE] No schema provided, skipping validation")
		// Try to parse as JSON
		var output map[string]any
		if err := json.Unmarshal([]byte(content), &output); err == nil {
			return output, nil
		}

		// Return content as "response" field
		return map[string]any{
			"response": content,
		}, nil
	}

	// Extract JSON from content (handle markdown code blocks)
	jsonContent := extractJSON(content)

	// Parse JSON - support both objects {} and arrays []
	var output any
	if err := json.Unmarshal([]byte(jsonContent), &output); err != nil {
		if strict {
			return nil, fmt.Errorf("failed to parse output as JSON: %w", err)
		}
		// Non-strict: return content as-is with error
		return map[string]any{
			"response":    content,
			"_parseError": err.Error(),
		}, nil
	}

	// Validate against schema (basic validation)
	if err := e.validateSchema(output, schema); err != nil {
		log.Printf("❌ [VALIDATE] Schema validation FAILED: %v", err)
		if strict {
			return nil, fmt.Errorf("output validation failed: %w", err)
		}
		// Non-strict: return with validation error at TOP LEVEL for retry loop detection
		log.Printf("⚠️  [AGENT-EXEC] Validation warning (non-strict): %v", err)
		return map[string]any{
			"response":         content,
			"data":             output,
			"_validationError": err.Error(), // TOP LEVEL for retry loop
		}, nil
	}

	log.Printf("✅ [VALIDATE] Schema validation PASSED")
	// Return parsed data as response so downstream blocks can access fields via {{block.response.field}}
	// Also include raw JSON string for debugging
	return map[string]any{
		"response":    output,  // Parsed JSON object - allows {{block.response.field}} access
		"data":        output,  // Alias for response
		"rawResponse": content, // Raw JSON string for debugging
	}, nil
}

// extractJSON extracts JSON from content (handles markdown code blocks)
func extractJSON(content string) string {
	content = strings.TrimSpace(content)

	// Check for markdown JSON code block
	jsonBlockRegex := regexp.MustCompile("```(?:json)?\\s*([\\s\\S]*?)```")
	if matches := jsonBlockRegex.FindStringSubmatch(content); len(matches) > 1 {
		return strings.TrimSpace(matches[1])
	}

	// Try to find JSON object or array
	start := strings.IndexAny(content, "{[")
	if start == -1 {
		return content
	}

	// Find matching closing bracket
	openBracket := content[start]
	closeBracket := byte('}')
	if openBracket == '[' {
		closeBracket = ']'
	}

	depth := 0
	for i := start; i < len(content); i++ {
		if content[i] == openBracket {
			depth++
		} else if content[i] == closeBracket {
			depth--
			if depth == 0 {
				return content[start : i+1]
			}
		}
	}

	return content[start:]
}

// validateSchema performs basic JSON schema validation - supports both objects and arrays
func (e *AgentBlockExecutor) validateSchema(data any, schema *models.JSONSchema) error {
	if schema == nil {
		return nil
	}

	// Handle based on schema type
	if schema.Type == "object" {
		return e.validateObjectSchema(data, schema)
	} else if schema.Type == "array" {
		return e.validateArraySchema(data, schema)
	}

	// If no type specified, infer from data
	if schema.Type == "" {
		if _, isMap := data.(map[string]any); isMap {
			return e.validateObjectSchema(data, schema)
		}
		if _, isSlice := data.([]any); isSlice {
			return e.validateArraySchema(data, schema)
		}
	}

	return nil
}

// validateObjectSchema validates object (map) data against schema
func (e *AgentBlockExecutor) validateObjectSchema(data any, schema *models.JSONSchema) error {
	dataMap, ok := data.(map[string]any)
	if !ok {
		return fmt.Errorf("schema expects object but got %T", data)
	}

	// Check required fields
	for _, required := range schema.Required {
		if _, ok := dataMap[required]; !ok {
			return fmt.Errorf("missing required field: %s", required)
		}
	}

	// Validate property types (basic)
	for key, propSchema := range schema.Properties {
		if value, ok := dataMap[key]; ok {
			if err := e.validateValue(value, propSchema); err != nil {
				return fmt.Errorf("field %s: %w", key, err)
			}
		}
	}

	return nil
}

// validateArraySchema validates array data against schema
func (e *AgentBlockExecutor) validateArraySchema(data any, schema *models.JSONSchema) error {
	dataArray, ok := data.([]any)
	if !ok {
		return fmt.Errorf("schema expects array but got %T", data)
	}

	// If no items schema, we can't validate items
	if schema.Items == nil {
		return nil
	}

	// Validate each item against the items schema
	for i, item := range dataArray {
		if err := e.validateValue(item, schema.Items); err != nil {
			return fmt.Errorf("array[%d]: %w", i, err)
		}
	}

	return nil
}

// validateValue validates a single value against a schema
func (e *AgentBlockExecutor) validateValue(value any, schema *models.JSONSchema) error {
	if schema == nil {
		return nil
	}

	switch schema.Type {
	case "string":
		if _, ok := value.(string); !ok {
			return fmt.Errorf("expected string, got %T", value)
		}
	case "number", "integer":
		switch value.(type) {
		case float64, int, int64:
			// OK
		default:
			return fmt.Errorf("expected number, got %T", value)
		}
	case "boolean":
		if _, ok := value.(bool); !ok {
			return fmt.Errorf("expected boolean, got %T", value)
		}
	case "array":
		if _, ok := value.([]interface{}); !ok {
			return fmt.Errorf("expected array, got %T", value)
		}
	case "object":
		if _, ok := value.(map[string]interface{}); !ok {
			return fmt.Errorf("expected object, got %T", value)
		}
	}

	return nil
}

// findCredentialForIntegrationType finds the first credential matching the integration type
// from the list of credential IDs configured for the block.
func findCredentialForIntegrationType(credentialIDs []string, integrationType string, resolver tools.CredentialResolver) string {
	for _, credID := range credentialIDs {
		cred, err := resolver(credID)
		if err != nil {
			// Skip invalid credentials
			continue
		}
		if cred.IntegrationType == integrationType {
			return credID
		}
	}
	return ""
}
