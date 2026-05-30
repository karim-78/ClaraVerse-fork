package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"strings"
)

// SubagentRunnerKey is the args map key used by chat_service to inject the
// concrete subagent runner. The chat side implements SubagentRunner; the tool
// side calls it through this interface — keeping the tools package free of
// any services import that would otherwise be circular.
const SubagentRunnerKey = "__subagent_runner__"

// SubagentRunner is the contract chat_service satisfies so that any tool
// (currently only spawn_subagent) can fan out an isolated agentic loop.
type SubagentRunner interface {
	RunSubagent(ctx context.Context, params SubagentParams) (SubagentResult, error)
}

// SubagentParams captures everything the parent gives the child agent.
type SubagentParams struct {
	UserID         string
	ParentConvID   string
	Task           string
	SystemPrompt   string
	ModelID        string   // empty = inherit parent's model
	AllowedTools   []string // empty = inherit parent's tool set
	MaxIterations  int      // 0 = default
	ReasoningHint  string   // "low" | "medium" | "high"
}

// SubagentResult is what the child returns.
type SubagentResult struct {
	Summary      string
	IterationsUsed int
	ToolsCalled    []string
}

// NewSpawnSubagentTool creates the model-callable tool. The model uses this
// to delegate self-contained sub-tasks to a fresh agentic loop with its own
// tool list and context budget — the 2026 "subagent as a tool" pattern
// popularised by Claude Code's Agent tool. Use cases: parallel research,
// noisy long-running operations whose intermediate steps the parent doesn't
// need to see, focused work that would otherwise blow the parent's context.
func NewSpawnSubagentTool() *Tool {
	return &Tool{
		Name:        "spawn_subagent",
		DisplayName: "Spawn Subagent",
		Description: `Delegate an isolated sub-task to a fresh agent loop. The subagent runs with its own conversation, its own tool budget, and reports back only a final summary — its intermediate tool calls and reasoning never enter your context. Use this when:

- The task is independent and well-scoped (e.g., "research X across 5 sources and summarize", "scan this directory for Y", "draft a section based on these notes").
- You expect the work to consume many tool calls or long tool outputs that you don't need to see in detail.
- You have multiple independent sub-tasks that can run sequentially without blowing context.

Do NOT use this for:
- Trivial single-tool calls (just call the tool yourself).
- Tasks that need iterative collaboration with the user (the subagent can't ask the user questions).
- Work where you need to see the intermediate steps.

Returns: a single text summary from the subagent.`,
		Icon:     "Bot",
		Category: "orchestration",
		Keywords: []string{"subagent", "delegate", "fan-out", "parallel", "orchestration"},
		Parameters: map[string]interface{}{
			"type":     "object",
			"required": []string{"task"},
			"properties": map[string]interface{}{
				"task": map[string]interface{}{
					"type":        "string",
					"description": "Clear, self-contained task description. Include success criteria. Treat this as the only context the subagent gets.",
				},
				"system_prompt": map[string]interface{}{
					"type":        "string",
					"description": "Optional system prompt override that shapes the subagent's persona/style. If omitted, a generic 'focused worker' persona is used.",
				},
				"allowed_tools": map[string]interface{}{
					"type":        "array",
					"items":       map[string]interface{}{"type": "string"},
					"description": "Optional whitelist of tool names the subagent may call. If empty, the subagent inherits the parent's tool set MINUS spawn_subagent itself (no recursion).",
				},
				"max_iterations": map[string]interface{}{
					"type":        "integer",
					"description": "Hard cap on agent loop iterations (1–25). Defaults to 10.",
					"minimum":     1,
					"maximum":     25,
				},
				"reasoning_effort": map[string]interface{}{
					"type":        "string",
					"enum":        []string{"low", "medium", "high"},
					"description": "Hint to the subagent's model for how much reasoning to spend. Defaults to inheriting from parent.",
				},
			},
		},
		Execute: executeSpawnSubagent,
	}
}

func executeSpawnSubagent(args map[string]interface{}) (string, error) {
	runner, ok := args[SubagentRunnerKey].(SubagentRunner)
	if !ok || runner == nil {
		return "", fmt.Errorf("spawn_subagent is not wired up in this context (no SubagentRunner injected)")
	}

	userID, _ := args["__user_id__"].(string)
	parentConvID, _ := args["__conversation_id__"].(string)
	task, _ := args["task"].(string)
	if strings.TrimSpace(task) == "" {
		return "", fmt.Errorf("task is required and must be non-empty")
	}

	params := SubagentParams{
		UserID:        userID,
		ParentConvID:  parentConvID,
		Task:          task,
		MaxIterations: 10,
	}

	if sp, ok := args["system_prompt"].(string); ok {
		params.SystemPrompt = sp
	}
	if at, ok := args["allowed_tools"].([]interface{}); ok {
		for _, t := range at {
			if s, ok := t.(string); ok && s != "" {
				params.AllowedTools = append(params.AllowedTools, s)
			}
		}
	}
	if mi, ok := args["max_iterations"].(float64); ok && mi > 0 {
		params.MaxIterations = int(mi)
	}
	if re, ok := args["reasoning_effort"].(string); ok {
		params.ReasoningHint = re
	}

	log.Printf("🤖 [SUBAGENT] Spawning for user=%s parent_conv=%s task_len=%d max_iter=%d tools=%d",
		userID, parentConvID, len(task), params.MaxIterations, len(params.AllowedTools))

	ctx := context.Background()
	res, err := runner.RunSubagent(ctx, params)
	if err != nil {
		log.Printf("⚠️ [SUBAGENT] Failed: %v", err)
		return "", fmt.Errorf("subagent failed: %w", err)
	}

	log.Printf("✅ [SUBAGENT] Returned summary_len=%d iterations=%d tools_called=%d",
		len(res.Summary), res.IterationsUsed, len(res.ToolsCalled))

	// Pack a small JSON envelope so the parent model gets structured metadata
	// alongside the prose. Helps it decide whether to spawn another subagent.
	envelope := map[string]interface{}{
		"summary":         res.Summary,
		"iterations_used": res.IterationsUsed,
		"tools_called":    res.ToolsCalled,
	}
	out, err := json.Marshal(envelope)
	if err != nil {
		return res.Summary, nil
	}
	return string(out), nil
}
