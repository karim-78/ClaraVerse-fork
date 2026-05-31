package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"claraverse/internal/services/rag"

	"go.mongodb.org/mongo-driver/bson/primitive"
)

// NewSearchKnowledgeTool builds a per-user, per-context instance of the
// search_knowledge tool. It's intentionally NOT a global tool —
// because the tool needs (a) a user identity for ownership filtering
// and (b) default project_ids to fan across when the model doesn't
// pass them. Both are bound at construction time via closure.
//
// Lifecycle:
//
//   * Nexus daemon: orchestrator builds one of these per daemon with
//     defaultProjectIDs = [task.project_id], registers it on the
//     per-user tool map, and tears it down when the daemon exits.
//
//   * Chat: when a user sends a message with `knowledge_project_ids`
//     attached, the chat handler builds an instance with those IDs
//     as defaults and surfaces it to the LLM for that turn.
//
//   * Workflow: NOT this tool. Workflows use the explicit
//     KnowledgeSearch block — same backend Service call, different
//     surface, so the block can validate inputs at design time.
//
// The model can override project_ids in the tool call args (string
// array of project_id hex), but most of the time it just leaves them
// out and we use the defaults. Lower cognitive load → fewer mistakes.
func NewSearchKnowledgeTool(ragSvc *rag.Service, userID string, defaultProjectIDs []string) *Tool {
	defaults := make([]string, 0, len(defaultProjectIDs))
	for _, id := range defaultProjectIDs {
		id = strings.TrimSpace(id)
		if id == "" {
			continue
		}
		defaults = append(defaults, id)
	}

	return &Tool{
		Name:        "search_knowledge",
		DisplayName: "Search project knowledge",
		Description: knowledgeToolDescription(defaults),
		// JSON schema: matches what the model needs to fill in. The
		// project_ids field is documented as "leave empty to use the
		// current project's knowledge base" so the model knows it has
		// a sensible default.
		Parameters: map[string]interface{}{
			"type": "object",
			"properties": map[string]interface{}{
				"query": map[string]interface{}{
					"type":        "string",
					"description": "What you're looking for. Phrase as a search query — multi-word, key concepts. Do NOT phrase as a question (\"What is...\"); the retriever ranks by semantic similarity to the query string itself.",
				},
				"project_ids": map[string]interface{}{
					"type":        "array",
					"items":       map[string]interface{}{"type": "string"},
					"description": "Project IDs to search across. Leave empty to use this conversation's default knowledge bases.",
				},
				"top_k": map[string]interface{}{
					"type":        "integer",
					"description": "Number of chunks to return. Default 5. Use 10-20 for survey / literature-review style tasks where breadth matters more than precision.",
					"default":     5,
				},
				"rerank": map[string]interface{}{
					"type":        "boolean",
					"description": "Whether to run cross-encoder reranking on the top results. Default true — turn off only if latency matters more than quality (e.g. an inline lookup during interactive chat).",
					"default":     true,
				},
			},
			"required": []string{"query"},
		},
		Icon:     "BookOpen",
		Source:   ToolSourceBuiltin,
		Category: "knowledge",
		Keywords: []string{"rag", "knowledge", "search", "documents", "project", "files"},
		UserID:   userID,

		Execute: func(args map[string]interface{}) (string, error) {
			query, _ := args["query"].(string)
			query = strings.TrimSpace(query)
			if query == "" {
				return "", fmt.Errorf("query is required")
			}

			// Resolve project_ids: explicit args override defaults.
			var projectIDStrs []string
			if raw, ok := args["project_ids"]; ok {
				switch v := raw.(type) {
				case []interface{}:
					for _, e := range v {
						if s, ok := e.(string); ok {
							projectIDStrs = append(projectIDStrs, s)
						}
					}
				case []string:
					projectIDStrs = v
				}
			}
			if len(projectIDStrs) == 0 {
				projectIDStrs = defaults
			}
			if len(projectIDStrs) == 0 {
				return "", fmt.Errorf("no project_ids supplied and no default attached to this tool — call only works when the conversation has a project attached")
			}

			// Convert + dedupe.
			seen := map[string]bool{}
			var projectIDs []primitive.ObjectID
			for _, s := range projectIDStrs {
				if seen[s] {
					continue
				}
				seen[s] = true
				oid, err := primitive.ObjectIDFromHex(s)
				if err != nil {
					continue // silently skip bad IDs; the rest may still work
				}
				projectIDs = append(projectIDs, oid)
			}
			if len(projectIDs) == 0 {
				return "", fmt.Errorf("project_ids were all invalid")
			}

			topK := 5
			if v, ok := args["top_k"].(float64); ok && v > 0 {
				topK = int(v)
				if topK > 30 {
					topK = 30 // hard cap — beyond this the response is just noise to the LLM
				}
			}
			rerank := true
			if v, ok := args["rerank"].(bool); ok {
				rerank = v
			}

			// 60s deadline — query embed + N parallel searches + rerank.
			// Generous because cold-start sidecar can be slow, and the
			// daemon caller usually has its own outer deadline.
			ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
			defer cancel()

			hits, err := ragSvc.Search(ctx, userID, rag.SearchOptions{
				Query:      query,
				ProjectIDs: projectIDs,
				TopK:       topK,
				Rerank:     rerank,
			})
			if err != nil {
				return "", fmt.Errorf("search_knowledge: %w", err)
			}
			if len(hits) == 0 {
				return "No relevant chunks found in the attached project knowledge. Try a different query phrasing, or consider that the answer may not be in the uploaded documents.", nil
			}

			// Return as JSON — the LLM parses cleanly and we preserve
			// citation metadata for the UI to render as chips.
			payload := map[string]interface{}{
				"hits": hits,
				"hint": "Each hit cites file_name + page/section. Quote the relevant text in your response and reference the file_name + page so the user can verify.",
			}
			b, _ := json.MarshalIndent(payload, "", "  ")
			return string(b), nil
		},
	}
}

// knowledgeToolDescription is rendered into the system prompt and
// matters a LOT — it teaches the model when to reach for this tool
// vs. web search vs. just answering from prior knowledge. Specific
// > generic; we explicitly tell the model what "attached knowledge"
// means in our context.
func knowledgeToolDescription(defaults []string) string {
	if len(defaults) == 0 {
		return "Search across attached project knowledge bases (uploaded PDFs, markdown, text files, URLs). Returns the most semantically relevant chunks with citations. Use this BEFORE search_web when the user's question is likely answered in their attached documents — internal docs are more authoritative than the open web for project-specific questions."
	}
	return fmt.Sprintf(
		"Search across this conversation's attached project knowledge bases (%d project(s) — uploaded PDFs, markdown, text files, URLs). Returns the most semantically relevant chunks with citations. Use this BEFORE search_web when the user's question is likely answered in their attached documents — internal docs are more authoritative than the open web for project-specific questions. The default project_ids are pre-filled; you only need to pass `project_ids` if you want to scope to a subset.",
		len(defaults),
	)
}
