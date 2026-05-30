package execution

import (
	"bytes"
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
)

// BlockCheckResult represents the structured output from the block completion checker
type BlockCheckResult struct {
	Passed       bool   `json:"passed"`        // true if block accomplished its job
	Reason       string `json:"reason"`        // explanation of why it passed or failed
	ActualOutput string `json:"actual_output"` // truncated actual output for debugging (populated by checker)
}

// BlockChecker validates whether a block actually accomplished its intended job
// This prevents workflows from continuing when a block technically "completed"
// but didn't actually do what it was supposed to do (e.g., tool errors, timeouts)
type BlockChecker struct {
	providerService *services.ProviderService
	httpClient      *http.Client
}

// NewBlockChecker creates a new block checker
func NewBlockChecker(providerService *services.ProviderService) *BlockChecker {
	return &BlockChecker{
		providerService: providerService,
		httpClient: &http.Client{
			Timeout: 30 * time.Second,
		},
	}
}

// CheckBlockCompletion validates if a block actually accomplished its intended task
// Parameters:
// - ctx: context for cancellation
// - workflowGoal: the overall workflow objective (from user's request)
// - block: the block that just executed
// - blockInput: what was passed to the block
// - blockOutput: what the block produced
// - modelID: the model to use for checking (should be a fast, cheap model)
//
// Returns:
// - BlockCheckResult with passed/failed status and reason
// - error if the check itself failed
func (c *BlockChecker) CheckBlockCompletion(
	ctx context.Context,
	workflowGoal string,
	block models.Block,
	blockInput map[string]any,
	blockOutput map[string]any,
	modelID string,
) (*BlockCheckResult, error) {
	log.Printf("🔍 [BLOCK-CHECKER] Checking completion for block '%s' (type: %s)", block.Name, block.Type)

	// Skip checking for Start blocks (variable type with read operation)
	if block.Type == "variable" {
		log.Printf("⏭️ [BLOCK-CHECKER] Skipping Start block '%s'", block.Name)
		return &BlockCheckResult{Passed: true, Reason: "Start block - no validation needed"}, nil
	}

	// Build the validation prompt
	prompt := c.buildValidationPrompt(workflowGoal, block, blockInput, blockOutput)

	// Get provider for the model. Use *WithName so the request carries
	// the upstream API name — strict providers (Bedrock) 400 on the row id.
	provider, apiName, err := c.providerService.GetByModelIDWithName(modelID)
	if err != nil {
		log.Printf("⚠️ [BLOCK-CHECKER] Provider error, defaulting to passed: %v", err)
		return &BlockCheckResult{Passed: true, Reason: "Provider error - defaulting to passed"}, nil
	}
	modelID = apiName

	// Build the request with structured output
	requestBody := map[string]interface{}{
		"model":       modelID,
		"max_tokens":  200,
		"temperature": 0.1, // Low temperature for consistent validation
		"messages": []map[string]string{
			{
				"role":    "system",
				"content": "You are a workflow block validator. Analyze if the block accomplished its intended job based on its purpose, input, and output. Be strict but fair - if there are clear errors, tool failures, or the output doesn't match the intent, it should fail.",
			},
			{
				"role":    "user",
				"content": prompt,
			},
		},
		"response_format": map[string]interface{}{
			"type": "json_schema",
			"json_schema": map[string]interface{}{
				"name":   "block_check_result",
				"strict": true,
				"schema": map[string]interface{}{
					"type": "object",
					"properties": map[string]interface{}{
						"passed": map[string]interface{}{
							"type":        "boolean",
							"description": "true if the block accomplished its intended job, false otherwise",
						},
						"reason": map[string]interface{}{
							"type":        "string",
							"description": "Brief explanation (1-2 sentences) of why the block passed or failed",
						},
					},
					"required":             []string{"passed", "reason"},
					"additionalProperties": false,
				},
			},
		},
	}

	bodyBytes, err := json.Marshal(requestBody)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal request: %w", err)
	}

	// Make the API call
	apiURL := fmt.Sprintf("%s/chat/completions", provider.BaseURL)
	req, err := http.NewRequestWithContext(ctx, "POST", apiURL, bytes.NewReader(bodyBytes))
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}

	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", fmt.Sprintf("Bearer %s", provider.APIKey))

	resp, err := c.httpClient.Do(req)
	if err != nil {
		log.Printf("⚠️ [BLOCK-CHECKER] HTTP error, defaulting to passed: %v", err)
		return &BlockCheckResult{Passed: true, Reason: "HTTP error during check - defaulting to passed"}, nil
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		log.Printf("⚠️ [BLOCK-CHECKER] API error (status %d): %s, defaulting to passed", resp.StatusCode, string(body))
		return &BlockCheckResult{Passed: true, Reason: "API error during check - defaulting to passed"}, nil
	}

	// Parse response
	var apiResp struct {
		Choices []struct {
			Message struct {
				Content string `json:"content"`
			} `json:"message"`
		} `json:"choices"`
	}

	if err := json.NewDecoder(resp.Body).Decode(&apiResp); err != nil {
		log.Printf("⚠️ [BLOCK-CHECKER] Decode error, defaulting to passed: %v", err)
		return &BlockCheckResult{Passed: true, Reason: "Response decode error - defaulting to passed"}, nil
	}

	if len(apiResp.Choices) == 0 || apiResp.Choices[0].Message.Content == "" {
		log.Printf("⚠️ [BLOCK-CHECKER] Empty response, defaulting to passed")
		return &BlockCheckResult{Passed: true, Reason: "Empty response from checker - defaulting to passed"}, nil
	}

	// Parse the structured output
	var result BlockCheckResult
	if err := json.Unmarshal([]byte(apiResp.Choices[0].Message.Content), &result); err != nil {
		log.Printf("⚠️ [BLOCK-CHECKER] JSON parse error, defaulting to passed: %v", err)
		return &BlockCheckResult{Passed: true, Reason: "JSON parse error - defaulting to passed"}, nil
	}

	// Always populate ActualOutput with a summary of what the block produced
	// This helps with debugging when the block fails
	result.ActualOutput = c.summarizeOutputForError(blockOutput)

	if result.Passed {
		log.Printf("✅ [BLOCK-CHECKER] Block '%s' PASSED: %s", block.Name, result.Reason)
	} else {
		log.Printf("❌ [BLOCK-CHECKER] Block '%s' FAILED: %s\n   Actual Output: %s", block.Name, result.Reason, result.ActualOutput)
	}

	return &result, nil
}

// buildValidationPrompt creates the prompt for block validation
func (c *BlockChecker) buildValidationPrompt(
	workflowGoal string,
	block models.Block,
	blockInput map[string]any,
	blockOutput map[string]any,
) string {
	// Extract key information from output
	outputSummary := c.summarizeOutput(blockOutput)

	// Check for obvious failures
	hasError := false
	errorMessages := []string{}

	// Check for tool errors
	if toolCalls, ok := blockOutput["toolCalls"].([]interface{}); ok {
		for _, tc := range toolCalls {
			if tcMap, ok := tc.(map[string]interface{}); ok {
				if errMsg, ok := tcMap["error"].(string); ok && errMsg != "" {
					hasError = true
					errorMessages = append(errorMessages, fmt.Sprintf("Tool '%s' error: %s", tcMap["name"], errMsg))
				}
			}
		}
	}

	// Check for timeout
	if timedOut, ok := blockOutput["timedOut"].(bool); ok && timedOut {
		hasError = true
		errorMessages = append(errorMessages, "Block timed out before completing")
	}

	// Check for parse errors
	if parseErr, ok := blockOutput["_parseError"].(string); ok && parseErr != "" {
		hasError = true
		errorMessages = append(errorMessages, fmt.Sprintf("Parse error: %s", parseErr))
	}

	// Check for aggregated tool errors
	if toolErr, ok := blockOutput["_toolError"].(string); ok && toolErr != "" {
		hasError = true
		errorMessages = append(errorMessages, fmt.Sprintf("Tool error: %s", toolErr))
	}

	// Check for empty response - handle both string and object types
	hasResponse := false
	if respStr, ok := blockOutput["response"].(string); ok && respStr != "" {
		hasResponse = true
	} else if respObj, ok := blockOutput["response"].(map[string]any); ok && len(respObj) > 0 {
		hasResponse = true
	}
	if !hasResponse && block.Type == "llm_inference" {
		hasError = true
		errorMessages = append(errorMessages, "Block produced no response")
	}

	// Build the prompt - include current date so the model knows what year it is
	currentDate := time.Now().Format("January 2, 2006")
	prompt := fmt.Sprintf(`## IMPORTANT: CURRENT DATE
**Today's Date:** %s
(This is the actual current date. Do NOT assume dates in the output are in the future.)

## WORKFLOW CONTEXT
**Overall Goal:** %s

## BLOCK BEING VALIDATED
**Block Name:** %s
**Block Type:** %s
**Block Description:** %s

## BLOCK INPUT (what it received)
%s

## BLOCK OUTPUT (what it produced)
%s
`,
		currentDate,
		workflowGoal,
		block.Name,
		block.Type,
		block.Description,
		c.formatForPrompt(blockInput),
		outputSummary,
	)

	// Add error context if any
	if hasError {
		prompt += fmt.Sprintf(`
## DETECTED ISSUES
The following issues were detected in the block output:
%s

`, c.formatErrors(errorMessages))
	}

	prompt += `
## YOUR TASK
Analyze if this block accomplished its intended job within the workflow.

CRITICAL DISTINCTION - External Failures vs Block Failures:
- **External failures** (API rate limits, service unavailable, network errors): The block DID ITS JOB correctly by calling the right tool with correct parameters. The failure is EXTERNAL. Mark as PASSED if the block handled it gracefully (explained the error, provided fallback info).
- **Block failures** (wrong tool called, missing required data, timeout, empty response): The block FAILED to do its job.

Consider:
1. Did the block call the correct tool(s) with appropriate parameters?
2. If a tool returned an external error (rate limit, auth error, service down), did the block handle it gracefully?
3. Does the response acknowledge and explain what happened?
4. Is there meaningful information that downstream blocks can use (even if just error context)?

IMPORTANT:
- External API errors (429 rate limit, 503 service unavailable, etc.) are NOT the block's fault - PASS if handled gracefully
- Parse errors are formatting issues, not functional failures - PASS if the response content is meaningful
- If the block called the right tool and got an external error, it PASSED (the tool worked, the API didn't)
- Only FAIL if: block timed out, produced no response, called wrong tools, or completely failed to attempt its task

Return your judgment as JSON with "passed" (boolean) and "reason" (brief explanation).`

	return prompt
}

// summarizeOutput creates a readable summary of block output for the prompt
func (c *BlockChecker) summarizeOutput(output map[string]any) string {
	summary := ""

	// Response text - handle both string and object types
	if resp, ok := output["response"].(string); ok && resp != "" {
		// Truncate long responses
		if len(resp) > 500 {
			resp = resp[:500] + "... [truncated]"
		}
		summary += fmt.Sprintf("**Response:** %s\n\n", resp)
	} else if respObj, ok := output["response"].(map[string]any); ok && len(respObj) > 0 {
		// Response is a structured object (from schema validation)
		respJSON, err := json.Marshal(respObj)
		if err == nil {
			respStr := string(respJSON)
			if len(respStr) > 500 {
				respStr = respStr[:500] + "... [truncated]"
			}
			summary += fmt.Sprintf("**Response (structured):** %s\n\n", respStr)
		}
	}

	// Timeout status
	if timedOut, ok := output["timedOut"].(bool); ok && timedOut {
		summary += "**Status:** TIMED OUT\n\n"
	}

	// Iterations
	if iterations, ok := output["iterations"].(int); ok {
		summary += fmt.Sprintf("**Iterations:** %d\n\n", iterations)
	} else if iterations, ok := output["iterations"].(float64); ok {
		summary += fmt.Sprintf("**Iterations:** %.0f\n\n", iterations)
	}

	// Tool calls summary
	if toolCalls, ok := output["toolCalls"].([]interface{}); ok && len(toolCalls) > 0 {
		summary += "**Tool Calls:**\n"
		errorCount := 0
		successCount := 0
		for i, tc := range toolCalls {
			if i >= 5 {
				summary += fmt.Sprintf("  ... and %d more tool calls\n", len(toolCalls)-5)
				break
			}
			if tcMap, ok := tc.(map[string]interface{}); ok {
				name, _ := tcMap["name"].(string)
				errMsg, hasErr := tcMap["error"].(string)
				if hasErr && errMsg != "" {
					errorCount++
					summary += fmt.Sprintf("  - %s: ❌ ERROR: %s\n", name, errMsg)
				} else {
					successCount++
					summary += fmt.Sprintf("  - %s: ✓ Success\n", name)
				}
			}
		}
		summary += fmt.Sprintf("\nTotal: %d successful, %d failed\n\n", successCount, errorCount)
	}

	// Parse errors
	if parseErr, ok := output["_parseError"].(string); ok && parseErr != "" {
		summary += fmt.Sprintf("**Parse Error:** %s\n\n", parseErr)
	}

	// Tool validation warning
	if warning, ok := output["_toolValidationWarning"].(string); ok && warning != "" {
		summary += fmt.Sprintf("**Warning:** %s\n\n", warning)
	}

	// Artifacts (images, charts, etc.)
	if artifacts, ok := output["artifacts"].([]interface{}); ok && len(artifacts) > 0 {
		summary += fmt.Sprintf("**Artifacts Generated:** %d artifact(s) created\n", len(artifacts))
		for i, art := range artifacts {
			if i >= 3 {
				summary += fmt.Sprintf("  ... and %d more artifacts\n", len(artifacts)-3)
				break
			}
			if artMap, ok := art.(map[string]interface{}); ok {
				artType, _ := artMap["type"].(string)
				artFormat, _ := artMap["format"].(string)
				if artType != "" {
					summary += fmt.Sprintf("  - Type: %s, Format: %s\n", artType, artFormat)
				} else {
					summary += fmt.Sprintf("  - Format: %s\n", artFormat)
				}
			}
		}
		summary += "\n"
	}

	// Generated files
	if files, ok := output["generatedFiles"].([]interface{}); ok && len(files) > 0 {
		summary += fmt.Sprintf("**Generated Files:** %d file(s) created\n", len(files))
		for i, file := range files {
			if i >= 3 {
				summary += fmt.Sprintf("  ... and %d more files\n", len(files)-3)
				break
			}
			if fileMap, ok := file.(map[string]interface{}); ok {
				fileName, _ := fileMap["name"].(string)
				fileType, _ := fileMap["type"].(string)
				summary += fmt.Sprintf("  - %s (type: %s)\n", fileName, fileType)
			}
		}
		summary += "\n"
	}

	if summary == "" {
		// Fallback: dump some of the output
		outputBytes, _ := json.MarshalIndent(output, "", "  ")
		if len(outputBytes) > 1000 {
			summary = string(outputBytes[:1000]) + "... [truncated]"
		} else {
			summary = string(outputBytes)
		}
	}

	return summary
}

// formatForPrompt formats input data for the prompt
func (c *BlockChecker) formatForPrompt(data map[string]any) string {
	// For input, just show key names and brief values
	if len(data) == 0 {
		return "(empty)"
	}

	result := ""
	for k, v := range data {
		// Skip internal fields
		if k[0] == '_' {
			continue
		}
		valStr := fmt.Sprintf("%v", v)
		if len(valStr) > 200 {
			valStr = valStr[:200] + "..."
		}
		result += fmt.Sprintf("- **%s:** %s\n", k, valStr)
	}

	if result == "" {
		return "(internal data only)"
	}
	return result
}

// formatErrors formats error messages for the prompt
func (c *BlockChecker) formatErrors(errors []string) string {
	result := ""
	for _, err := range errors {
		result += fmt.Sprintf("- %s\n", err)
	}
	return result
}

// ShouldCheckBlock determines if a block should be validated
// Some blocks (like Start/variable blocks) don't need validation
func ShouldCheckBlock(block models.Block) bool {
	// Skip Start blocks (variable type with read operation)
	if block.Type == "variable" {
		if op, ok := block.Config["operation"].(string); ok && op == "read" {
			return false
		}
	}

	// Only check LLM blocks (they're the ones that can fail in complex ways)
	return block.Type == "llm_inference"
}

// summarizeOutputForError creates a concise summary of block output for error messages
// This helps developers understand what went wrong when a block fails validation
func (c *BlockChecker) summarizeOutputForError(output map[string]any) string {
	var parts []string

	// Include the response (truncated) - handle both string and object types
	if resp, ok := output["response"].(string); ok && resp != "" {
		truncated := resp
		if len(truncated) > 300 {
			truncated = truncated[:300] + "..."
		}
		parts = append(parts, fmt.Sprintf("Response: %q", truncated))
	} else if respObj, ok := output["response"].(map[string]any); ok && len(respObj) > 0 {
		// Response is a structured object (from schema validation)
		respJSON, err := json.Marshal(respObj)
		if err == nil {
			truncated := string(respJSON)
			if len(truncated) > 300 {
				truncated = truncated[:300] + "..."
			}
			parts = append(parts, fmt.Sprintf("Response: %s", truncated))
		} else {
			parts = append(parts, fmt.Sprintf("Response: (object with %d keys)", len(respObj)))
		}
	} else {
		parts = append(parts, "Response: (empty)")
	}

	// Include parse error if present
	if parseErr, ok := output["_parseError"].(string); ok && parseErr != "" {
		parts = append(parts, fmt.Sprintf("Parse Error: %s", parseErr))
	}

	// Include tool validation warning if present
	if warning, ok := output["_toolValidationWarning"].(string); ok && warning != "" {
		parts = append(parts, fmt.Sprintf("Tool Warning: %s", warning))
	}

	// Summarize tool calls - handle both []models.ToolCallRecord and []interface{}
	toolCallsSummarized := false
	if toolCalls, ok := output["toolCalls"].([]models.ToolCallRecord); ok && len(toolCalls) > 0 {
		successCount := 0
		failCount := 0
		var failedTools []string
		for _, tc := range toolCalls {
			if tc.Error != "" {
				failCount++
				failedTools = append(failedTools, fmt.Sprintf("%s: %s", tc.Name, tc.Error))
			} else {
				successCount++
			}
		}
		parts = append(parts, fmt.Sprintf("Tools: %d called (%d success, %d failed)", len(toolCalls), successCount, failCount))
		if len(failedTools) > 0 && len(failedTools) <= 3 {
			for _, ft := range failedTools {
				parts = append(parts, fmt.Sprintf("  - %s", ft))
			}
		}
		toolCallsSummarized = true
	} else if toolCalls, ok := output["toolCalls"].([]interface{}); ok && len(toolCalls) > 0 {
		// Fallback for []interface{} type (e.g., from JSON unmarshaling)
		successCount := 0
		failCount := 0
		var failedTools []string
		for _, tc := range toolCalls {
			if tcMap, ok := tc.(map[string]interface{}); ok {
				name, _ := tcMap["name"].(string)
				if errMsg, hasErr := tcMap["error"].(string); hasErr && errMsg != "" {
					failCount++
					failedTools = append(failedTools, fmt.Sprintf("%s: %s", name, errMsg))
				} else {
					successCount++
				}
			}
		}
		parts = append(parts, fmt.Sprintf("Tools: %d called (%d success, %d failed)", len(toolCalls), successCount, failCount))
		if len(failedTools) > 0 && len(failedTools) <= 3 {
			for _, ft := range failedTools {
				parts = append(parts, fmt.Sprintf("  - %s", ft))
			}
		}
		toolCallsSummarized = true
	}
	if !toolCallsSummarized {
		parts = append(parts, "Tools: none called")
	}

	// Include structured data summary if present
	if data, ok := output["data"]; ok && data != nil {
		dataBytes, _ := json.Marshal(data)
		if len(dataBytes) > 200 {
			parts = append(parts, fmt.Sprintf("Data: %s...", string(dataBytes[:200])))
		} else if len(dataBytes) > 0 {
			parts = append(parts, fmt.Sprintf("Data: %s", string(dataBytes)))
		}
	}

	return strings.Join(parts, " | ")
}
