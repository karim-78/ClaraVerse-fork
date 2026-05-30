package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"strings"
)

// MemoryAccessKey is the args-map key chat_service uses to hand the model's
// memory tools a concrete adapter. Defined in this package so the tools
// don't import services (which would create a cycle — services already
// imports tools to invoke them).
const MemoryAccessKey = "__memory_access__"

// MemoryAccess is the contract chat_service satisfies so add_memory /
// search_memory can read+write the user's long-term memory. Modeled on the
// 2026 Anthropic memory-tool pattern: explicit, model-driven, with the host
// providing only the minimum surface (add/search) — no list/delete from the
// model. Deletion stays a user-only action via Settings.
type MemoryAccess interface {
	AddMemory(ctx context.Context, userID, content, category, conversationID string, tags []string) (string, error) // returns memory_id
	SearchMemory(ctx context.Context, userID, query string, limit int) ([]MemorySearchHit, error)
}

// MemorySearchHit is the slim record returned to the model. We deliberately
// don't ship score / category / timestamps — the model doesn't need them
// for decision-making and they bloat the context.
type MemorySearchHit struct {
	ID        string  `json:"id"`
	Content   string  `json:"content"`
	Relevance float64 `json:"relevance,omitempty"`
}

// ─── add_memory ────────────────────────────────────────────────────────

func NewAddMemoryTool() *Tool {
	return &Tool{
		Name:        "add_memory",
		DisplayName: "Add Memory",
		Description: `Save a durable, cross-conversation fact about the user that you'll want to remember later. Use this when the user has shared something stable enough to apply in future conversations — preferences, identity, goals, hard constraints.

Use it when:
- The user states a preference ("I prefer Python", "vegan", "respond concisely")
- The user states identity/role ("I'm a backend engineer at X")
- The user gives a recurring instruction ("always cite sources", "use IST timezone")
- The user shares a hard constraint ("I'm allergic to peanuts")

Do NOT use it for:
- Transient context that won't matter next week ("today I'm working on X", "the test passed")
- Information already captured in a prior memory (you'll see existing memories in your system prompt — don't duplicate)
- Anything you inferred but the user didn't actually state

Categories: 'personal_info' (identity/demographics) | 'preferences' (likes/dislikes) | 'context' (ongoing situation) | 'fact' (objective info) | 'instruction' (recurring directive). Default 'preferences' when unsure.

The memory is stored encrypted and visible to the user in Settings → Memory.`,
		Icon:     "Brain",
		Source:   ToolSourceBuiltin,
		Category: "memory",
		Keywords: []string{"memory", "remember", "save", "preference", "fact"},
		Parameters: map[string]interface{}{
			"type":     "object",
			"required": []string{"content"},
			"properties": map[string]interface{}{
				"content": map[string]interface{}{
					"type":        "string",
					"description": "The fact to remember. Phrase as a third-person statement about the user (e.g., 'User prefers Python over JavaScript' not 'I prefer Python'). Keep it under 200 chars.",
				},
				"category": map[string]interface{}{
					"type":        "string",
					"enum":        []string{"personal_info", "preferences", "context", "fact", "instruction"},
					"description": "Categorization. Default 'preferences'.",
				},
				"tags": map[string]interface{}{
					"type":        "array",
					"items":       map[string]interface{}{"type": "string"},
					"description": "Optional searchable tags (e.g., ['coding', 'python']).",
				},
			},
		},
		Execute: executeAddMemory,
	}
}

func executeAddMemory(args map[string]interface{}) (string, error) {
	access, ok := args[MemoryAccessKey].(MemoryAccess)
	if !ok || access == nil {
		return "", fmt.Errorf("memory tools not wired up in this context")
	}
	userID, _ := args["__user_id__"].(string)
	if userID == "" {
		return "", fmt.Errorf("user id missing from tool context")
	}
	convID, _ := args["__conversation_id__"].(string)

	content, _ := args["content"].(string)
	content = strings.TrimSpace(content)
	if content == "" {
		return "", fmt.Errorf("content is required")
	}
	if len(content) > 500 {
		return "", fmt.Errorf("content too long (%d chars, max 500)", len(content))
	}

	category, _ := args["category"].(string)
	if category == "" {
		category = "preferences"
	}

	var tags []string
	if raw, ok := args["tags"].([]interface{}); ok {
		for _, t := range raw {
			if s, ok := t.(string); ok && s != "" {
				tags = append(tags, s)
			}
		}
	}

	log.Printf("🧠 [TOOL add_memory] user=%s category=%s content=%q", userID, category, truncate(content, 80))
	id, err := access.AddMemory(context.Background(), userID, content, category, convID, tags)
	if err != nil {
		return "", fmt.Errorf("add_memory failed: %w", err)
	}

	out, _ := json.Marshal(map[string]interface{}{
		"ok":        true,
		"memory_id": id,
		"message":   "Memory saved. It will appear in future conversations when relevant.",
	})
	return string(out), nil
}

// ─── search_memory ─────────────────────────────────────────────────────

func NewSearchMemoryTool() *Tool {
	return &Tool{
		Name:        "search_memory",
		DisplayName: "Search Memory",
		Description: `Look up specific past facts about the user you might not have in current context. Use this when the user references something they told you before, OR when you want to check whether you know something before assuming.

Examples:
- User says "as I mentioned before, my stack is…" — search to find what they said
- User asks for advice about something personal — search to ground the answer in their actual situation
- You're about to ask a question — search first to see if the user already answered it earlier

Returns up to 'limit' memories with relevance scores. If nothing relevant comes back, treat that as "the user hasn't told me about this" — don't fabricate.`,
		Icon:     "BrainCircuit",
		Source:   ToolSourceBuiltin,
		Category: "memory",
		Keywords: []string{"memory", "search", "recall", "find", "lookup"},
		Parameters: map[string]interface{}{
			"type":     "object",
			"required": []string{"query"},
			"properties": map[string]interface{}{
				"query": map[string]interface{}{
					"type":        "string",
					"description": "What you're trying to find. Phrase as a search query, not a question. E.g., 'user dietary preferences' not 'what does the user eat?'.",
				},
				"limit": map[string]interface{}{
					"type":        "integer",
					"description": "Max memories to return (1–10). Default 5.",
					"minimum":     1,
					"maximum":     10,
				},
			},
		},
		Execute: executeSearchMemory,
	}
}

func executeSearchMemory(args map[string]interface{}) (string, error) {
	access, ok := args[MemoryAccessKey].(MemoryAccess)
	if !ok || access == nil {
		return "", fmt.Errorf("memory tools not wired up in this context")
	}
	userID, _ := args["__user_id__"].(string)
	if userID == "" {
		return "", fmt.Errorf("user id missing from tool context")
	}

	query, _ := args["query"].(string)
	query = strings.TrimSpace(query)
	if query == "" {
		return "", fmt.Errorf("query is required")
	}

	limit := 5
	if v, ok := args["limit"].(float64); ok {
		if iv := int(v); iv > 0 {
			limit = iv
		}
	}
	if limit > 10 {
		limit = 10
	}

	log.Printf("🧠 [TOOL search_memory] user=%s limit=%d query=%q", userID, limit, query)
	hits, err := access.SearchMemory(context.Background(), userID, query, limit)
	if err != nil {
		return "", fmt.Errorf("search_memory failed: %w", err)
	}

	out, _ := json.Marshal(map[string]interface{}{
		"query": query,
		"hits":  hits,
		"count": len(hits),
	})
	return string(out), nil
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "…"
}
