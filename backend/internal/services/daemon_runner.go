package services

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"strings"
	"sync"
	"time"

	"claraverse/internal/models"
	"claraverse/internal/tools"

	"go.mongodb.org/mongo-driver/bson/primitive"
)

// DaemonUpdate represents a real-time status update from a daemon
type DaemonUpdate struct {
	DaemonID      primitive.ObjectID `json:"daemon_id"`
	Index         int                `json:"index"`
	Role          string             `json:"role"`
	Type          string             `json:"type"` // "status","tool_call","tool_result","thinking","completed","failed","question"
	Status        string             `json:"status,omitempty"`
	CurrentAction string             `json:"current_action,omitempty"`
	Progress      float64            `json:"progress,omitempty"`
	Content       string             `json:"content,omitempty"`
	Result        *DaemonResult      `json:"result,omitempty"`
	ToolName      string             `json:"tool_name,omitempty"`
	ToolResult    string             `json:"tool_result,omitempty"`
	Error         string             `json:"error,omitempty"`
	CanRetry      bool               `json:"can_retry,omitempty"`
}

// DaemonResult holds the final output of a completed daemon
type DaemonResult struct {
	Summary   string                 `json:"summary"`
	Data      map[string]interface{} `json:"data,omitempty"`
	Artifacts []models.NexusArtifact `json:"artifacts,omitempty"`
}

// daemonHTTPClient is a shared HTTP client for daemon LLM calls
var daemonHTTPClient = &http.Client{Timeout: 300 * time.Second}

// DaemonRunner executes a single daemon's task via LLM + tool loop
type DaemonRunner struct {
	instance           *models.Daemon
	userID             string
	planIndex          int
	originalMessage    string               // The user's original message (not paraphrased by classifier)
	projectInstruction string               // System instruction from the project
	skillIDs           []primitive.ObjectID  // Skills attached to this daemon
	updateChan      chan<- DaemonUpdate
	cancelCtx       context.Context
	cancelFunc      context.CancelFunc
	mu              sync.Mutex

	// Context management
	contextWindow   int // Model context window in tokens (default 128K)
	toolDefTokens   int // Estimated tokens from tool definitions
	lastPromptTokens int // Actual prompt tokens from last API usage response

	// Services (reused from main app)
	chatService     *ChatService
	providerService *ProviderService
	toolRegistry    *tools.Registry
	toolService     *ToolService
	mcpBridge       *MCPBridgeService
	engramService   *EngramService
	daemonStore     *DaemonPool
	contextBuilder  *CortexContextBuilder
	toolSelector    *CortexToolSelector
	artifactStore   *NexusArtifactStore // optional; enables produce/list/read_artifact tools

	// multiDaemonCtx is non-nil iff this daemon is part of a multi-daemon
	// orchestration. When set, the system prompt explicitly teaches the
	// daemon to use produce_artifact / read_artifact for cross-daemon
	// handoff — without it the model defaults to summary-only handoff and
	// the structured store sits unused.
	multiDaemonCtx *MultiDaemonContext
}

// DaemonRunnerConfig holds configuration for creating a DaemonRunner
type DaemonRunnerConfig struct {
	Daemon          *models.Daemon
	UserID          string
	PlanIndex       int
	UpdateChan      chan<- DaemonUpdate
	ChatService     *ChatService
	ProviderService *ProviderService
	ToolRegistry    *tools.Registry
	ToolService     *ToolService
	MCPBridge       *MCPBridgeService
	EngramService   *EngramService
	DaemonStore     *DaemonPool
	ContextBuilder  *CortexContextBuilder
	ToolSelector    *CortexToolSelector
	ArtifactStore   *NexusArtifactStore
	DepResults         map[string]string          // Predecessor daemon results
	OriginalMessage    string                     // User's original message (passed through to daemon)
	ProjectInstruction string                     // System instruction from the containing project
	SkillIDs           []primitive.ObjectID        // Skills attached to this daemon
	ContextWindow      int                        // Model context window in tokens (0 = default 128K)
	MultiDaemonCtx     *MultiDaemonContext        // Non-nil when this is part of a multi-daemon orchestration
}

// NewDaemonRunner creates and configures a new daemon runner
func NewDaemonRunner(cfg DaemonRunnerConfig) *DaemonRunner {
	ctx, cancel := context.WithCancel(context.Background())

	// Inject dependency results into daemon
	if cfg.DepResults != nil {
		cfg.Daemon.DependencyResults = cfg.DepResults
	}

	contextWindow := cfg.ContextWindow
	if contextWindow <= 0 {
		contextWindow = 128000
	}

	return &DaemonRunner{
		instance:           cfg.Daemon,
		userID:             cfg.UserID,
		planIndex:          cfg.PlanIndex,
		originalMessage:    cfg.OriginalMessage,
		projectInstruction: cfg.ProjectInstruction,
		skillIDs:           cfg.SkillIDs,
		multiDaemonCtx:     cfg.MultiDaemonCtx,
		updateChan:         cfg.UpdateChan,
		cancelCtx:          ctx,
		cancelFunc:         cancel,
		contextWindow:      contextWindow,
		chatService:     cfg.ChatService,
		providerService: cfg.ProviderService,
		toolRegistry:    cfg.ToolRegistry,
		toolService:     cfg.ToolService,
		mcpBridge:       cfg.MCPBridge,
		engramService:   cfg.EngramService,
		daemonStore:     cfg.DaemonStore,
		contextBuilder:  cfg.ContextBuilder,
		toolSelector:    cfg.ToolSelector,
		artifactStore:   cfg.ArtifactStore,
	}
}

// buildArtifactCatalogue renders the "Available artifacts" section appended
// to a daemon's system prompt so the model knows what structured data
// predecessors produced. Compact: names + types + one-line summaries only —
// the model fetches full content via read_artifact on demand.
func buildArtifactCatalogue(items []NexusArtifactSummary) string {
	var b strings.Builder
	b.WriteString("\n## Available Artifacts (from prior daemons in this session)\n\n")
	b.WriteString("These are structured outputs produced by earlier daemons in this orchestration. ")
	b.WriteString("Call read_artifact(name) to pull the full content; do NOT re-derive what's already here.\n\n")
	for _, it := range items {
		line := fmt.Sprintf("- `%s` [%s, %d bytes]", it.Name, it.ContentType, it.SizeBytes)
		if it.Summary != "" {
			line += " — " + it.Summary
		}
		b.WriteString(line)
		b.WriteString("\n")
	}
	b.WriteString("\nProduce your own outputs via produce_artifact(name, content_type, content, summary) so downstream daemons can use them.\n")
	return b.String()
}

// nexusArtifactToolAdapter satisfies tools.NexusArtifactAccess by binding
// the store to this daemon's user + session, so the tools (produce/list/
// read_artifact) can never reach into a different orchestration's data.
type nexusArtifactToolAdapter struct {
	store     *NexusArtifactStore
	userID    string
	sessionID primitive.ObjectID
	daemonID  primitive.ObjectID
}

func (a *nexusArtifactToolAdapter) Produce(ctx context.Context, name, contentType, content, summary string) (tools.ProducedArtifact, error) {
	doc, err := a.store.Produce(ctx, a.userID, a.sessionID, a.daemonID, name, contentType, content, summary)
	if err != nil {
		return tools.ProducedArtifact{}, err
	}
	return tools.ProducedArtifact{
		ID:          doc.ID.Hex(),
		Name:        doc.Name,
		ContentType: doc.ContentType,
		SizeBytes:   doc.SizeBytes,
	}, nil
}

func (a *nexusArtifactToolAdapter) List(ctx context.Context) ([]tools.ListedArtifact, error) {
	items, err := a.store.List(ctx, a.sessionID)
	if err != nil {
		return nil, err
	}
	out := make([]tools.ListedArtifact, 0, len(items))
	for _, s := range items {
		out = append(out, tools.ListedArtifact{
			Name:        s.Name,
			ContentType: s.ContentType,
			Summary:     s.Summary,
			SizeBytes:   s.SizeBytes,
		})
	}
	return out, nil
}

func (a *nexusArtifactToolAdapter) Read(ctx context.Context, name string) (*tools.FetchedArtifact, error) {
	doc, err := a.store.Read(ctx, a.sessionID, name)
	if err != nil {
		return nil, err
	}
	if doc == nil {
		return nil, nil
	}
	return &tools.FetchedArtifact{
		Name:        doc.Name,
		ContentType: doc.ContentType,
		Content:     doc.Content,
		Summary:     doc.Summary,
		SizeBytes:   doc.SizeBytes,
	}, nil
}

// Cancel stops the daemon execution
func (r *DaemonRunner) Cancel() {
	r.cancelFunc()
}

// Execute runs the daemon's task loop
func (r *DaemonRunner) Execute(ctx context.Context) {
	defer func() {
		if rec := recover(); rec != nil {
			log.Printf("[DAEMON %s] Panic recovered: %v", r.instance.RoleLabel, rec)
			r.sendUpdate(DaemonUpdate{
				Type:  "failed",
				Error: fmt.Sprintf("daemon panicked: %v", rec),
			})
		}
	}()

	// Merge parent context with cancel context
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	go func() {
		select {
		case <-r.cancelCtx.Done():
			cancel()
		case <-ctx.Done():
		}
	}()

	// Capture the task description BEFORE updateStatus overwrites CurrentAction
	taskDescription := r.instance.CurrentAction

	// Use the original user message if available — preserves intent like "use browser"
	// Falls back to task_summary if no original message (e.g. sub-tasks from decomposition)
	userPrompt := r.originalMessage
	if userPrompt == "" {
		userPrompt = taskDescription
	}

	// Update daemon status to executing
	r.updateStatus(models.DaemonStatusExecuting, "Initializing...", 0.0)

	// Pull predecessor artifacts so the system prompt advertises what
	// structured data the daemon can read instead of relying only on the
	// 4000-char text summaries. The actual content stays in the store —
	// the daemon pulls via read_artifact when it wants the body.
	var artifactsAvailable []NexusArtifactSummary
	if r.artifactStore != nil && !r.instance.SessionID.IsZero() {
		listCtx, listCancel := context.WithTimeout(ctx, 3*time.Second)
		if items, err := r.artifactStore.List(listCtx, r.instance.SessionID); err == nil {
			artifactsAvailable = items
		}
		listCancel()
	}

	// 1. Build system prompt using the task description (summary for context).
	// multiCtx is set when this daemon is part of a multi-daemon orchestration;
	// the orchestrator passes it via the runner config. When set, the prompt
	// teaches the daemon to use produce_artifact for cross-daemon handoff.
	systemPrompt := r.contextBuilder.BuildDaemonSystemPrompt(
		ctx,
		r.instance.Role,
		r.instance.RoleLabel,
		r.instance.Persona,
		taskDescription,
		r.instance.DependencyResults,
		r.projectInstruction,
		r.skillIDs,
		r.multiDaemonCtx,
	)
	if len(artifactsAvailable) > 0 {
		systemPrompt += buildArtifactCatalogue(artifactsAvailable)
	}

	// 2. Select tools for this daemon using the actual task
	selectedTools, toolNames := r.toolSelector.SelectToolsForDaemon(
		ctx,
		r.userID,
		r.instance.Role,
		r.instance.AssignedTools,
		userPrompt,
	)

	// Cache tool definition token estimate for context tracking
	r.toolDefTokens = EstimateToolDefTokens(selectedTools)

	log.Printf("[DAEMON %s] Starting with %d tools (%d tool-def tokens) for task: %s",
		r.instance.RoleLabel, len(toolNames), r.toolDefTokens, taskDescription)

	// 3. Initialize messages with system prompt and original user message
	messages := []map[string]interface{}{
		{"role": "system", "content": systemPrompt},
		{"role": "user", "content": userPrompt},
	}

	// 4. Get model ID from daemon or session default
	modelID := r.instance.ModelID
	if modelID == "" {
		modelID = "default"
	}

	// 5. Resolve provider config for this model
	config, err := r.resolveModelConfig(modelID)
	if err != nil {
		r.sendUpdate(DaemonUpdate{
			Type:  "failed",
			Error: fmt.Sprintf("failed to resolve model: %v", err),
		})
		r.updateStatus(models.DaemonStatusFailed, "No model available", 0.0)
		return
	}

	// 6. Daemon execution loop
	maxIter := r.instance.MaxIterations
	if maxIter <= 0 {
		maxIter = 25
	}

	for i := 0; i < maxIter; i++ {
		select {
		case <-ctx.Done():
			r.updateStatus(models.DaemonStatusFailed, "Cancelled", 0.0)
			r.sendUpdate(DaemonUpdate{
				Type:  "failed",
				Error: "daemon was cancelled",
			})
			return
		default:
		}

		progress := float64(i) / float64(maxIter)
		r.updateStatus(models.DaemonStatusExecuting, fmt.Sprintf("Iteration %d/%d", i+1, maxIter), progress)

		// Proactive context trimming before LLM call
		r.trimContextIfNeeded(messages)

		// Pre-flight size check
		r.preflightSizeCheck(messages)

		// Call LLM with overflow recovery
		response, toolCalls, finishReason, err := r.callLLMWithOverflowRetry(ctx, config, messages, selectedTools)
		if err != nil {
			if r.handleError(ctx, err) {
				continue // Retry
			}
			return // Fatal error
		}

		// If tool calls, execute them
		if len(toolCalls) > 0 {
			// Add assistant message with tool calls to conversation
			assistantMsg := map[string]interface{}{
				"role":       "assistant",
				"tool_calls": toolCalls,
			}
			if response != "" {
				assistantMsg["content"] = response
				r.sendUpdate(DaemonUpdate{
					Type:    "thinking",
					Content: response,
				})
			}
			messages = append(messages, assistantMsg)

			// Execute each tool
			for _, tc := range toolCalls {
				tcMap, ok := tc.(map[string]interface{})
				if !ok {
					continue
				}
				funcMap, _ := tcMap["function"].(map[string]interface{})
				if funcMap == nil {
					continue
				}
				toolName, _ := funcMap["name"].(string)
				toolArgs, _ := funcMap["arguments"].(string)
				toolCallID, _ := tcMap["id"].(string)

				r.sendUpdate(DaemonUpdate{
					Type:          "tool_call",
					ToolName:      toolName,
					CurrentAction: fmt.Sprintf("Using %s...", toolName),
				})

				// Handle meta-tool search_available_tools
				var result string
				if toolName == "search_available_tools" {
					result = r.handleSearchTools(toolArgs)
				} else {
					result = r.executeTool(ctx, toolName, toolArgs)
				}

				// Store in daemon working memory
				r.addWorkingMemory(ctx, toolName, result)

				r.sendUpdate(DaemonUpdate{
					Type:       "tool_result",
					ToolName:   toolName,
					ToolResult: truncateString(result, 500),
				})

				// Add tool result to messages with adaptive truncation
				cappedResult := r.adaptiveResultForLLM(result, toolName)
				messages = append(messages, map[string]interface{}{
					"role":         "tool",
					"tool_call_id": toolCallID,
					"name":         toolName,
					"content":      cappedResult,
				})
			}

			// Save latest messages to daemon store
			r.persistLatestMessages(ctx, messages)
			continue
		}

		// No tool calls — LLM returned text response
		if response != "" {
			r.sendUpdate(DaemonUpdate{
				Type:    "thinking",
				Content: response,
			})

			// Completion: trust finish_reason from the provider over text matching.
			// "stop" with no tool calls is the canonical end-of-turn for OpenAI
			// Chat Completions and Bedrock's OpenAI shim. Fall back to the
			// string heuristic only when finish_reason is missing (some
			// providers omit it on tool-free responses).
			done := finishReason == "stop"
			if finishReason == "" {
				done = r.isComplete(response)
			}
			if done {
				log.Printf("[DAEMON %s] Completing (finish_reason=%q, response_len=%d)",
					r.instance.RoleLabel, finishReason, len(response))
				r.complete(ctx, response)
				return
			}

			// Not done — add response and prompt to continue
			messages = append(messages, map[string]interface{}{
				"role":    "assistant",
				"content": response,
			})
			messages = append(messages, map[string]interface{}{
				"role":    "user",
				"content": "Continue with your task. If you are finished, clearly state your final result and conclusion.",
			})
		}
	}

	// Max iterations reached — synthesize what we have
	lastResponse := extractLastAssistantResponse(messages)
	if lastResponse != "" {
		r.complete(ctx, lastResponse)
	} else {
		r.sendUpdate(DaemonUpdate{
			Type:  "failed",
			Error: fmt.Sprintf("max iterations (%d) reached without result", maxIter),
		})
		r.updateStatus(models.DaemonStatusFailed, "Max iterations reached", 1.0)
	}
}

// ── Context Management ─────────────────────────────────────────────

// adaptiveResultCap returns the max chars for tool results based on context
// fill ratio.
//
// Tightened on 2026-05-31: Bedrock's /openai/v1 shim returned 400
// ("Unterminated string at column 11") on bodies above ~25 KB even though
// the Go json encoder produced valid output. The fix isn't on our parser
// side — it's that the receiving shim has an undocumented body/string-
// length limit. Capping tool results at 6 KB instead of 16 KB keeps the
// total request body well under that ceiling across all fill-ratio buckets
// while still giving the model enough context to keep working.
//
// Trade-off: a single very long tool result (e.g. a 20 KB page scrape)
// loses more detail than before. The model can mitigate by calling the
// tool with narrower selectors or using read_artifact on a stored chunk.
// We log the truncation so it's visible.
func (r *DaemonRunner) adaptiveResultCap() int {
	usedTokens := r.lastPromptTokens
	if usedTokens == 0 {
		usedTokens = r.toolDefTokens + 2000
	}

	fillRatio := float64(usedTokens) / float64(r.contextWindow)

	switch {
	case fillRatio < 0.40:
		return 6000
	case fillRatio < 0.65:
		return 4000
	case fillRatio < 0.80:
		return 2500
	default:
		return 1500
	}
}

// shrinkToolMessagesAggressively trims every "tool" role message in the
// conversation to a short head+tail snippet (~600 chars total each).
// Called as a last resort when the marshalled body exceeds the provider's
// hard size limit — we need to fit the request through somehow, and
// tool-result bodies are by far the largest mutable thing in the message
// history. Preserves the FIRST message (system prompt) and the LAST tool
// message (the model is mid-loop and needs to see the most-recent
// result intact).
func shrinkToolMessagesAggressively(messages []map[string]interface{}) {
	if len(messages) < 2 {
		return
	}
	const keepChars = 600
	const headChars = 350
	const tailChars = 200
	// Find last tool-role index so we can leave it intact.
	lastTool := -1
	for i := len(messages) - 1; i >= 0; i-- {
		if role, _ := messages[i]["role"].(string); role == "tool" {
			lastTool = i
			break
		}
	}
	for i := 1; i < len(messages); i++ {
		if i == lastTool {
			continue
		}
		role, _ := messages[i]["role"].(string)
		if role != "tool" {
			continue
		}
		content, _ := messages[i]["content"].(string)
		if len(content) <= keepChars {
			continue
		}
		messages[i]["content"] = content[:headChars] +
			"\n\n... [shrunk to fit provider body limit] ...\n\n" +
			content[len(content)-tailChars:]
	}
}

// adaptiveResultForLLM caps a tool result based on current context fill ratio
// and adds tool-specific guidance when truncated.
func (r *DaemonRunner) adaptiveResultForLLM(result, toolName string) string {
	cap := r.adaptiveResultCap()

	if len(result) <= cap {
		return result
	}

	// Smart truncation: head 60% + tail 40%
	headSize := cap * 60 / 100
	tailSize := cap - headSize - 100 // room for notice
	if tailSize < 0 {
		tailSize = 0
	}

	truncated := result[:headSize]
	if tailSize > 0 {
		truncated += "\n\n... [" + fmt.Sprintf("%d", len(result)-headSize-tailSize) + " chars omitted] ...\n\n"
		truncated += result[len(result)-tailSize:]
	}

	// Add tool-specific guidance
	switch {
	case strings.Contains(toolName, "browser") || strings.Contains(toolName, "snapshot"):
		truncated += "\n\n[Context note: browser output was truncated. Use targeted CSS selectors or element IDs to narrow results.]"
	case strings.Contains(toolName, "read") || strings.Contains(toolName, "file"):
		truncated += "\n\n[Context note: file content was truncated. Read specific line ranges with start_line/end_line.]"
	case strings.Contains(toolName, "exec") || strings.Contains(toolName, "bash"):
		truncated += "\n\n[Context note: command output was truncated. Pipe through head/tail/grep to get specific sections.]"
	case strings.Contains(toolName, "search") || strings.Contains(toolName, "grep"):
		truncated += "\n\n[Context note: search results were truncated. Narrow with more specific patterns or file filters.]"
	}

	return truncated
}

// trimContextIfNeeded proactively shrinks older tool results when approaching the context limit.
// Preserves: system prompt (index 0), user prompt (index 1), and the last 6 messages.
func (r *DaemonRunner) trimContextIfNeeded(messages []map[string]interface{}) {
	estimatedTokens := r.estimateContextTokens(messages)
	threshold := int(float64(r.contextWindow) * 0.80)

	if estimatedTokens <= threshold {
		return
	}

	log.Printf("[DAEMON %s] Context at %d tokens (%.0f%% of %d), trimming older tool results",
		r.instance.RoleLabel, estimatedTokens, float64(estimatedTokens)/float64(r.contextWindow)*100, r.contextWindow)

	keepTail := 6
	if keepTail > len(messages)-2 {
		keepTail = len(messages) - 2
	}
	trimEnd := len(messages) - keepTail

	for i := 2; i < trimEnd; i++ {
		role, _ := messages[i]["role"].(string)
		content, _ := messages[i]["content"].(string)
		if role == "tool" && len(content) > 500 {
			summary := content[:200] + "\n\n... [trimmed to fit context window] ...\n\n" + content[len(content)-200:]
			messages[i]["content"] = summary
		}
	}

	// Aggressive trim if still over
	newEstimate := r.estimateContextTokens(messages)
	if newEstimate > threshold {
		r.aggressiveTrim(messages)
	}
}

// aggressiveTrim shrinks ALL tool results to 300 chars (emergency).
func (r *DaemonRunner) aggressiveTrim(messages []map[string]interface{}) {
	log.Printf("[DAEMON %s] Aggressive trim: shrinking all tool results to 300 chars", r.instance.RoleLabel)
	for i := 2; i < len(messages); i++ {
		role, _ := messages[i]["role"].(string)
		content, _ := messages[i]["content"].(string)
		if role == "tool" && len(content) > 300 {
			messages[i]["content"] = content[:150] + "\n[aggressively trimmed]\n" + content[len(content)-100:]
		}
	}
}

// estimateContextTokens estimates total tokens for messages + tool definitions.
func (r *DaemonRunner) estimateContextTokens(messages []map[string]interface{}) int {
	// Use real prompt tokens from last API response if available
	if r.lastPromptTokens > 0 {
		// Add estimated tokens for messages added since last API call
		// This is approximate but better than pure heuristic
		return r.lastPromptTokens
	}
	return EstimateMessagesTokens(messages) + r.toolDefTokens
}

// preflightSizeCheck estimates total tokens and forces trim if over context window.
// Uses a conservative 3.2 chars/token ratio (cheaper than a 400 error from the API).
func (r *DaemonRunner) preflightSizeCheck(messages []map[string]interface{}) {
	totalChars := 0
	for _, msg := range messages {
		if c, ok := msg["content"].(string); ok {
			totalChars += len(c)
		}
		if tc, ok := msg["tool_calls"]; ok {
			tcJSON, _ := json.Marshal(tc)
			totalChars += len(tcJSON)
		}
	}

	// Conservative estimate: 3.2 chars/token
	estimatedTokens := int(float64(totalChars) / 3.2)
	estimatedTokens += r.toolDefTokens

	if estimatedTokens > r.contextWindow {
		log.Printf("[DAEMON %s] Pre-flight: estimated %d tokens exceeds %d context window, forcing trim",
			r.instance.RoleLabel, estimatedTokens, r.contextWindow)
		r.trimContextIfNeeded(messages)
	}
}

// ── Overflow Detection & Recovery ──────────────────────────────────

// isContextOverflowError detects context window overflow errors from the API.
func isContextOverflowError(statusCode int, body string) bool {
	if statusCode == 400 || statusCode == 413 || statusCode == 422 {
		lower := strings.ToLower(body)
		return strings.Contains(lower, "input too long") ||
			strings.Contains(lower, "context_length_exceeded") ||
			strings.Contains(lower, "maximum context length") ||
			strings.Contains(lower, "too many tokens") ||
			strings.Contains(lower, "token limit") ||
			strings.Contains(lower, "context length is only") ||
			strings.Contains(lower, "reduce the length of the input") ||
			strings.Contains(lower, "maximum input length") ||
			(strings.Contains(lower, "input_tokens") && strings.Contains(lower, "parameter"))
	}
	return false
}

// callLLMWithOverflowRetry wraps callLLM with 3-tier overflow recovery.
// Returns (content, toolCalls, finishReason, error).
func (r *DaemonRunner) callLLMWithOverflowRetry(ctx context.Context, config *models.Config, messages []map[string]interface{}, tools []map[string]interface{}) (string, []interface{}, string, error) {
	// Tier 0: Normal call
	response, toolCalls, finish, err := r.callLLM(ctx, config, messages, tools)
	if err == nil {
		return response, toolCalls, finish, nil
	}

	if !strings.Contains(err.Error(), "context overflow") {
		return "", nil, "", err
	}

	// Tier 1: Emergency trim (all tool results → 200 chars), retry
	log.Printf("[DAEMON %s] Context overflow — Tier 1: emergency trim", r.instance.RoleLabel)
	for i := 2; i < len(messages); i++ {
		role, _ := messages[i]["role"].(string)
		content, _ := messages[i]["content"].(string)
		if role == "tool" && len(content) > 200 {
			messages[i]["content"] = content[:100] + "\n[emergency trim]\n" + content[len(content)-50:]
		}
	}

	response, toolCalls, finish, err = r.callLLM(ctx, config, messages, tools)
	if err == nil {
		return response, toolCalls, finish, nil
	}

	if !strings.Contains(err.Error(), "context overflow") {
		return "", nil, "", err
	}

	// Tier 2: Nuclear trim (keep system + summary + last 4 messages)
	log.Printf("[DAEMON %s] Context overflow — Tier 2: nuclear trim", r.instance.RoleLabel)
	if len(messages) > 6 {
		// Build a summary of removed messages
		removedCount := len(messages) - 6
		summary := fmt.Sprintf("[%d earlier messages removed to fit context window. "+
			"Continue based on the remaining context.]", removedCount)

		kept := []map[string]interface{}{
			messages[0], // system prompt
			{"role": "user", "content": summary},
		}
		// Keep last 4 messages
		kept = append(kept, messages[len(messages)-4:]...)
		// Replace messages in-place (can't reassign the slice parameter)
		messages = messages[:0]
		messages = append(messages, kept...)
	}

	response, toolCalls, finish, err = r.callLLM(ctx, config, messages, tools)
	if err == nil {
		return response, toolCalls, finish, nil
	}

	// Tier 3: Give up
	return "", nil, "", fmt.Errorf("context overflow persists after all recovery attempts: %w", err)
}

// ── LLM Interaction ────────────────────────────────────────────────

// resolveModelConfig resolves a model ID to provider config using public ChatService APIs
func (r *DaemonRunner) resolveModelConfig(modelID string) (*models.Config, error) {
	// 1. Try model alias resolution
	if provider, actualModel, found := r.chatService.ResolveModelAlias(modelID); found {
		if provider.Enabled {
			return &models.Config{
				BaseURL:    provider.BaseURL,
				APIKey:     provider.APIKey,
				Model:      actualModel,
				ProviderID: provider.ID,
			}, nil
		}
	}

	// 2. Try provider lookup by model ID. Use the *WithName variant so we
	// send the upstream API name to the provider, not the row id — Bedrock
	// (and anything else strict) 400s on the prefixed form.
	if r.providerService != nil {
		provider, apiName, err := r.providerService.GetByModelIDWithName(modelID)
		if err == nil && provider.Enabled {
			return &models.Config{
				BaseURL:    provider.BaseURL,
				APIKey:     provider.APIKey,
				Model:      apiName,
				ProviderID: provider.ID,
			}, nil
		}
	}

	// 3. Fall back to default provider
	provider, modelName, err := r.chatService.GetDefaultProviderWithModel()
	if err != nil {
		return nil, fmt.Errorf("no provider found for model %s and no default available", modelID)
	}

	return &models.Config{
		BaseURL:    provider.BaseURL,
		APIKey:     provider.APIKey,
		Model:      modelName,
		ProviderID: provider.ID,
	}, nil
}

// callLLM makes a single LLM request and returns content + tool calls.
// Detects context overflow errors and wraps them for the retry handler.
// Extracts usage.prompt_tokens from API response for context tracking.
// callLLM returns content, tool calls, finish_reason, and error.
// finish_reason mirrors the OpenAI Chat Completions field ("stop", "tool_calls",
// "length", "content_filter", etc.). Used by the loop to decide completion
// instead of string-matching the response.
func (r *DaemonRunner) callLLM(ctx context.Context, config *models.Config, messages []map[string]interface{}, availableTools []map[string]interface{}) (string, []interface{}, string, error) {
	// Annotate system message with cache_control for cache-capable providers
	// before serialization. Mutates messages in place; safe because the
	// system prompt is the only marker we set and applyPromptCaching is
	// idempotent.
	applyPromptCaching(messages, config.BaseURL)

	reqBody := map[string]interface{}{
		"model":    config.Model,
		"messages": messages,
		"stream":   false,
	}
	if len(availableTools) > 0 {
		reqBody["tools"] = availableTools
	}

	// Disable HTML-escaping for the request body. Go's default json.Marshal
	// converts `<`, `>`, `&` to `<`, `>`, `&` — safe for HTML
	// embedding, but Bedrock's /openai/v1 shim returns
	// "Expecting value: line 1 column N" when those escaped sequences
	// appear in a message. Diagnosed live 2026-05-31 — any system prompt
	// containing `<...>` syntax (template hints, code samples, daemon
	// orchestration instructions) tripped the loop.
	marshalBody := func() ([]byte, error) {
		var buf bytes.Buffer
		e := json.NewEncoder(&buf)
		e.SetEscapeHTML(false)
		if err := e.Encode(reqBody); err != nil {
			return nil, err
		}
		// Encoder appends a trailing newline; trim for cleaner body +
		// accurate Content-Length.
		return bytes.TrimRight(buf.Bytes(), "\n"), nil
	}

	reqJSON, err := marshalBody()
	if err != nil {
		return "", nil, "", fmt.Errorf("failed to marshal request: %w", err)
	}

	// Hard body-size guard. Bedrock's /openai/v1 shim has an undocumented
	// limit somewhere in the 20-25KB range; bodies above it come back as
	// 400 "Unterminated string at column 11" regardless of actual content
	// validity. We detect oversized bodies, aggressively summarize older
	// tool messages, re-marshal, and try again. This trades a small loss
	// of tool history for the orchestration actually completing — much
	// better than the previous failure mode of dying mid-loop.
	const bodyHardLimit = 20000
	if len(reqJSON) > bodyHardLimit {
		log.Printf("[DAEMON %s] body %d bytes > %d limit — shrinking tool history",
			r.instance.RoleLabel, len(reqJSON), bodyHardLimit)
		shrinkToolMessagesAggressively(messages)
		reqJSON, err = marshalBody()
		if err != nil {
			return "", nil, "", fmt.Errorf("failed to marshal after shrink: %w", err)
		}
		log.Printf("[DAEMON %s] after shrink: %d bytes",
			r.instance.RoleLabel, len(reqJSON))
	}

	url := config.BaseURL + "/chat/completions"
	req, err := http.NewRequestWithContext(ctx, "POST", url, bytes.NewReader(reqJSON))
	if err != nil {
		return "", nil, "", fmt.Errorf("failed to create request: %w", err)
	}

	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+config.APIKey)

	resp, err := daemonHTTPClient.Do(req)
	if err != nil {
		return "", nil, "", fmt.Errorf("request failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		bodyStr := string(body)

		// Check for context overflow
		if isContextOverflowError(resp.StatusCode, bodyStr) {
			return "", nil, "", fmt.Errorf("context overflow: API error (status %d): %s", resp.StatusCode, bodyStr)
		}

		// 400-class errors usually mean we sent something the provider can't
		// parse. Log the first 2 KiB of the body we actually sent so we can
		// diagnose without re-running. Sliced so a 100 KB tool-result payload
		// doesn't drown the logs; the head is where parse errors live.
		if resp.StatusCode == http.StatusBadRequest {
			snippet := reqJSON
			if len(snippet) > 2048 {
				snippet = snippet[:2048]
			}
			log.Printf("[DAEMON %s] 400 from %s → request body (first %d/%d bytes): %s",
				r.instance.RoleLabel, url, len(snippet), len(reqJSON), string(snippet))
		}

		return "", nil, "", fmt.Errorf("API error (status %d): %s", resp.StatusCode, bodyStr)
	}

	var apiResult struct {
		Choices []struct {
			Message struct {
				Content   string        `json:"content"`
				ToolCalls []interface{} `json:"tool_calls"`
			} `json:"message"`
			FinishReason string `json:"finish_reason"`
		} `json:"choices"`
		Usage *struct {
			PromptTokens        int `json:"prompt_tokens"`
			CompletionTokens    int `json:"completion_tokens"`
			TotalTokens         int `json:"total_tokens"`
			PromptTokensDetails *struct {
				CachedTokens int `json:"cached_tokens"`
			} `json:"prompt_tokens_details,omitempty"`
		} `json:"usage"`
	}

	if err := json.NewDecoder(resp.Body).Decode(&apiResult); err != nil {
		return "", nil, "", fmt.Errorf("failed to decode response: %w", err)
	}

	// Track actual token usage for adaptive context management
	if apiResult.Usage != nil && apiResult.Usage.PromptTokens > 0 {
		r.lastPromptTokens = apiResult.Usage.PromptTokens
		fillPct := float64(apiResult.Usage.PromptTokens) / float64(r.contextWindow) * 100
		cached := 0
		if apiResult.Usage.PromptTokensDetails != nil {
			cached = apiResult.Usage.PromptTokensDetails.CachedTokens
		}
		if cached > 0 {
			cachePct := float64(cached) / float64(apiResult.Usage.PromptTokens) * 100
			log.Printf("[DAEMON %s] Token usage: %d prompt (💾 %d cached, %.0f%% hit) / %d completion (%.0f%% of %d context)",
				r.instance.RoleLabel, apiResult.Usage.PromptTokens, cached, cachePct,
				apiResult.Usage.CompletionTokens, fillPct, r.contextWindow)
		} else {
			log.Printf("[DAEMON %s] Token usage: %d prompt / %d completion (%.0f%% of %d context)",
				r.instance.RoleLabel, apiResult.Usage.PromptTokens, apiResult.Usage.CompletionTokens,
				fillPct, r.contextWindow)
		}
	}

	if len(apiResult.Choices) == 0 {
		return "", nil, "", fmt.Errorf("no choices in response")
	}

	choice := apiResult.Choices[0]
	content := choice.Message.Content
	toolCalls := choice.Message.ToolCalls
	finishReason := choice.FinishReason

	return content, toolCalls, finishReason, nil
}

// ── Tool Execution ─────────────────────────────────────────────────

// executeTool runs a single tool — routes MCP tools to the bridge, built-in tools to the registry
func (r *DaemonRunner) executeTool(ctx context.Context, toolName string, argsJSON string) string {
	var args map[string]interface{}
	if err := json.Unmarshal([]byte(argsJSON), &args); err != nil {
		return fmt.Sprintf("Error parsing arguments: %v", err)
	}

	// Pre-tool hooks: same dispatcher as chat. Hooks can mutate args (e.g.
	// redact secrets) or deny the call by returning Allow=false.
	if dec := tools.RunPreToolHooks(toolName, args); !dec.Allow {
		reason := dec.Reason
		if reason == "" {
			reason = "blocked by policy"
		}
		return reason
	}
	hookStart := time.Now()
	defer func() {
		// PostToolUse hooks don't get the result here because the function
		// has many return paths; the audit log hook still records duration
		// via this defer wrapper. For result-rewrite hooks the chat path
		// is the source of truth; daemon results are forwarded as-is.
		_ = tools.RunPostToolHooks(toolName, args, "", time.Since(hookStart), nil)
	}()

	// Check if this is an MCP tool (user's local client) or a built-in tool
	tool, exists := r.toolRegistry.GetUserTool(r.userID, toolName)

	if exists && tool.Source == tools.ToolSourceMCPLocal {
		// MCP tool — route to local MCP client
		if r.mcpBridge == nil || !r.mcpBridge.IsUserConnected(r.userID) {
			log.Printf("[DAEMON %s] MCP client not connected for tool %s", r.instance.RoleLabel, toolName)
			return "Error: MCP client not connected. Please start your local MCP client."
		}

		log.Printf("[DAEMON %s] Routing MCP tool %s to local client", r.instance.RoleLabel, toolName)
		result, err := r.mcpBridge.ExecuteToolOnClient(r.userID, toolName, args, 60*time.Second)
		if err != nil {
			log.Printf("[DAEMON %s] MCP tool %s failed: %v", r.instance.RoleLabel, toolName, err)
			return fmt.Sprintf("Tool error: %v", err)
		}
		return result
	}

	// Built-in tool — inject user context and credentials, execute directly
	args["__user_id__"] = r.userID

	if r.toolService != nil {
		resolver := r.toolService.CreateCredentialResolver(r.userID)
		if resolver != nil {
			args[tools.CredentialResolverKey] = resolver
		}
		credentialID := r.toolService.GetCredentialForTool(ctx, r.userID, toolName)
		if credentialID != "" {
			args["credential_id"] = credentialID
		}
	}

	// Nexus artifact-handoff tools: inject a session-scoped adapter so
	// produce/list/read all operate inside this orchestration only. We
	// only wire this when the artifact store is present (Mongo + Nexus
	// enabled); otherwise the tools surface a clear error.
	if (toolName == "produce_artifact" || toolName == "list_artifacts" || toolName == "read_artifact") && r.artifactStore != nil {
		args[tools.NexusArtifactAccessKey] = &nexusArtifactToolAdapter{
			store:     r.artifactStore,
			userID:    r.userID,
			sessionID: r.instance.SessionID,
			daemonID:  r.instance.ID,
		}
	}

	// spawn_subagent: same injection trick chat_service uses. Daemons can
	// now fan out to a focused subagent for noisy work whose intermediate
	// steps the daemon doesn't need to keep in its context. Without this
	// injection the tool surfaces a "not wired up" error to the model.
	if toolName == "spawn_subagent" && r.chatService != nil {
		args[tools.SubagentRunnerKey] = tools.SubagentRunner(r.chatService)
	}

	result, err := r.toolRegistry.Execute(toolName, args)
	if err != nil {
		log.Printf("[DAEMON %s] Tool %s failed: %v", r.instance.RoleLabel, toolName, err)
		return fmt.Sprintf("Tool error: %v", err)
	}

	return result
}

// handleSearchTools handles the search_available_tools meta-tool
func (r *DaemonRunner) handleSearchTools(argsJSON string) string {
	var args map[string]interface{}
	if err := json.Unmarshal([]byte(argsJSON), &args); err != nil {
		return "Error parsing search query"
	}
	query, _ := args["query"].(string)
	if query == "" {
		return "No search query provided"
	}

	_, summary := r.toolSelector.HandleSearchToolsRequest(r.userID, query)
	return summary
}

// ── State Management ───────────────────────────────────────────────

// addWorkingMemory stores a tool result in daemon's working memory
func (r *DaemonRunner) addWorkingMemory(ctx context.Context, key string, value string) {
	entry := models.DaemonMemoryEntry{
		Key:       key,
		Value:     value,
		Summary:   truncateString(value, 200),
		Timestamp: time.Now(),
	}

	r.mu.Lock()
	r.instance.WorkingMemory = append(r.instance.WorkingMemory, entry)
	r.mu.Unlock()

	if r.daemonStore != nil {
		_ = r.daemonStore.AddWorkingMemory(ctx, r.userID, r.instance.ID, entry)
	}
}

// persistLatestMessages saves the most recent messages to the daemon store
func (r *DaemonRunner) persistLatestMessages(ctx context.Context, messages []map[string]interface{}) {
	if r.daemonStore == nil || len(messages) == 0 {
		return
	}

	// Only persist the last few messages (avoid re-persisting everything)
	start := len(messages) - 3
	if start < 0 {
		start = 0
	}

	for _, msg := range messages[start:] {
		role, _ := msg["role"].(string)
		content, _ := msg["content"].(string)
		dm := models.DaemonMessage{
			Role:      role,
			Content:   content,
			Timestamp: time.Now(),
		}

		// Extract tool call info from assistant messages
		if role == "assistant" {
			if toolCalls, ok := msg["tool_calls"].([]interface{}); ok && len(toolCalls) > 0 {
				if tcMap, ok := toolCalls[0].(map[string]interface{}); ok {
					if funcMap, ok := tcMap["function"].(map[string]interface{}); ok {
						name, _ := funcMap["name"].(string)
						args, _ := funcMap["arguments"].(string)
						callID, _ := tcMap["id"].(string)
						dm.ToolCall = &models.DaemonToolCall{
							Name:      name,
							Arguments: args,
							ID:        callID,
						}
					}
				}
			}
		}

		// Extract tool result info from tool messages
		if role == "tool" {
			toolCallID, _ := msg["tool_call_id"].(string)
			toolName, _ := msg["name"].(string)
			isError := strings.Contains(strings.ToLower(content), "error") || strings.Contains(strings.ToLower(content), "failed")
			dm.ToolResult = &models.DaemonToolResult{
				ToolCallID: toolCallID,
				Content:    content,
				IsError:    isError,
			}
			// Store tool name in content prefix for learning extraction
			if toolName != "" && dm.Content == "" {
				dm.Content = content
			}
			_ = toolName // toolName is in the content context
		}

		_ = r.daemonStore.AppendMessage(ctx, r.userID, r.instance.ID, dm)
	}
}

// isComplete checks if the daemon's response indicates task completion
func (r *DaemonRunner) isComplete(response string) bool {
	lower := strings.ToLower(response)
	completionSignals := []string{
		"task complete",
		"task is complete",
		"i have completed",
		"here is the final",
		"here are the results",
		"in summary",
		"to summarize",
		"final result",
		"my findings",
		"i've finished",
		"i have finished",
	}

	for _, signal := range completionSignals {
		if strings.Contains(lower, signal) {
			return true
		}
	}

	// Substantial response without tool calls is likely final
	return len(response) > 500
}

// complete finalizes the daemon with a successful result
func (r *DaemonRunner) complete(ctx context.Context, response string) {
	result := &DaemonResult{
		Summary: response,
	}

	// Write to engram
	if r.engramService != nil {
		_ = r.engramService.Write(ctx, &models.EngramEntry{
			SessionID: r.instance.SessionID,
			UserID:    r.userID,
			Type:      "daemon_output",
			Key:       fmt.Sprintf("daemon_%d_%s", r.planIndex, r.instance.Role),
			Value:     response,
			Summary:   truncateString(response, 200),
			Source:    fmt.Sprintf("daemon_%s", r.instance.RoleLabel),
		})
	}

	r.updateStatus(models.DaemonStatusCompleted, "Completed", 1.0)
	r.sendUpdate(DaemonUpdate{
		Type:   "completed",
		Result: result,
	})

	log.Printf("[DAEMON %s] Completed successfully", r.instance.RoleLabel)
}

// handleError handles LLM errors with retry logic
func (r *DaemonRunner) handleError(ctx context.Context, err error) bool {
	r.mu.Lock()
	r.instance.RetryCount++
	retryCount := r.instance.RetryCount
	maxRetries := r.instance.MaxRetries
	r.mu.Unlock()

	if maxRetries <= 0 {
		maxRetries = 3
	}

	if retryCount <= maxRetries {
		log.Printf("[DAEMON %s] Error (attempt %d/%d): %v", r.instance.RoleLabel, retryCount, maxRetries, err)
		r.sendUpdate(DaemonUpdate{
			Type:          "status",
			Status:        "retrying",
			CurrentAction: fmt.Sprintf("Retrying after error (attempt %d/%d)", retryCount, maxRetries),
			Error:         err.Error(),
		})

		// Exponential backoff
		backoff := time.Duration(retryCount*retryCount) * time.Second
		select {
		case <-time.After(backoff):
			return true
		case <-ctx.Done():
			return false
		}
	}

	log.Printf("[DAEMON %s] Failed after %d retries: %v", r.instance.RoleLabel, maxRetries, err)
	r.updateStatus(models.DaemonStatusFailed, "Failed", 0.0)
	r.sendUpdate(DaemonUpdate{
		Type:     "failed",
		Error:    fmt.Sprintf("failed after %d retries: %v", maxRetries, err),
		CanRetry: true,
	})
	return false
}

// updateStatus updates the daemon status in the store
func (r *DaemonRunner) updateStatus(status models.DaemonStatus, action string, progress float64) {
	r.mu.Lock()
	r.instance.Status = status
	r.instance.CurrentAction = action
	r.instance.Progress = progress
	r.mu.Unlock()

	if r.daemonStore != nil {
		_ = r.daemonStore.UpdateStatus(context.Background(), r.userID, r.instance.ID, status, action, progress)
	}
}

// sendUpdate sends a DaemonUpdate to the update channel (non-blocking,
// panic-safe).
//
// The `select { case ch <- x: default: }` pattern handles a FULL channel,
// but not a CLOSED one — sending to a closed channel always panics. That
// happens here when the orchestrator has already closed updateChan
// (because all its WaitGroup-tracked goroutines exited) but a late tool
// callback or error handler still tries to publish. Without the deferred
// recover the panic crashes the entire backend process — and worse, the
// caller's own deferred panic handler may itself call sendUpdate to
// report "panicked", re-panicking inside the recover and bypassing it.
//
// We swallow the panic silently here because a daemon update arriving
// after the channel has been closed is by definition a lost message that
// nobody can receive anyway. The corresponding task/orchestration is
// already in a terminal state.
func (r *DaemonRunner) sendUpdate(update DaemonUpdate) {
	defer func() {
		if rec := recover(); rec != nil {
			// chan closed; this update will not be delivered. The daemon
			// has already exited; the orchestrator has moved on.
		}
	}()

	update.DaemonID = r.instance.ID
	update.Index = r.planIndex
	update.Role = r.instance.RoleLabel

	select {
	case r.updateChan <- update:
	default:
	}
}

// ── Helpers ────────────────────────────────────────────────────────

// extractLastAssistantResponse finds the last assistant message in the conversation
func extractLastAssistantResponse(messages []map[string]interface{}) string {
	for i := len(messages) - 1; i >= 0; i-- {
		if role, _ := messages[i]["role"].(string); role == "assistant" {
			if content, _ := messages[i]["content"].(string); content != "" {
				return content
			}
		}
	}
	return ""
}

// truncateString truncates a string to max length with ellipsis
func truncateString(s string, maxLen int) string {
	if len(s) <= maxLen {
		return s
	}
	return s[:maxLen-3] + "..."
}
