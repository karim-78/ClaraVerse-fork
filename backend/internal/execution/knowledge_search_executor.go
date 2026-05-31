// Package execution — knowledge_search block.
//
// This block exposes the same project-scoped RAG retrieval that
// chat and Nexus daemons get, but as an explicit workflow node.
// Configured at design time with a set of project IDs + a templated
// query; outputs `chunks: [...]` for downstream blocks (typically an
// llm_inference block that synthesizes them).
//
// Why a dedicated block (vs. wiring search_knowledge as a tool the
// llm_inference block could call): workflows are mostly deterministic
// data pipelines. Putting retrieval as an explicit upstream block:
//   - Lets the user pick projects + query + top_k visually
//   - Surfaces results as block output for branching/transform/etc.
//   - Decouples retrieval cost from LLM cost (only spend on the LLM
//     if retrieval found anything)
//   - Makes the workflow legible at a glance: "this block searches
//     these knowledge bases, the next block writes the answer"
package execution

import (
	"claraverse/internal/models"
	"claraverse/internal/services/rag"
	"context"
	"fmt"
	"log"
	"strings"
	"time"

	"go.mongodb.org/mongo-driver/bson/primitive"
)

// KnowledgeSearchExecutor is the workflow-side adapter to rag.Service.
type KnowledgeSearchExecutor struct {
	rag *rag.Service
}

// NewKnowledgeSearchExecutor wires the executor. When ragService is
// nil (e.g. RAG not configured for this deployment) the block returns
// an empty result with a clear error string so the workflow can still
// continue or branch on the failure.
func NewKnowledgeSearchExecutor(ragService *rag.Service) *KnowledgeSearchExecutor {
	return &KnowledgeSearchExecutor{rag: ragService}
}

// Execute runs one retrieval. Inputs handle template interpolation
// against block config so `{{prior.query}}`-style references work the
// same way as in HTTP request and transform blocks.
//
// Config schema:
//   {
//     "project_ids": ["<hex>", "<hex>"],   // multi-project allowed
//     "query":       "{{prior.question}}", // string template
//     "top_k":       5,                    // optional, default 5
//     "rerank":      true                  // optional, default true
//   }
//
// Output:
//   {
//     "chunks":  [{ text, file_id, file_name, page, section, score, project_id }, ...],
//     "count":   <int>,
//     "elapsed_ms": <int>
//   }
//
// Authorization: the executor takes the workflow run's user_id from
// the inputs map (every execution path threads it as `__user_id__`,
// matching the convention used by other blocks). Without a user_id we
// refuse — search is always user-scoped at the rag.Service layer.
func (e *KnowledgeSearchExecutor) Execute(ctx context.Context, block models.Block, inputs map[string]any) (map[string]any, error) {
	t0 := time.Now()
	if e.rag == nil {
		return nil, fmt.Errorf("RAG service not configured for this deployment")
	}

	userID, _ := inputs["__user_id__"].(string)
	if userID == "" {
		return nil, fmt.Errorf("user_id missing from workflow execution context")
	}

	config := block.Config

	// Resolve project_ids. Allow either a literal array on the config
	// (design-time pick) or a template expression that evaluates to
	// an array at runtime (e.g. "{{prior.projects}}").
	var projectIDStrs []string
	switch v := config["project_ids"].(type) {
	case []any:
		for _, item := range v {
			if s, ok := item.(string); ok {
				s = strings.TrimSpace(InterpolateTemplate(s, inputs))
				if s != "" {
					projectIDStrs = append(projectIDStrs, s)
				}
			}
		}
	case []string:
		for _, s := range v {
			s = strings.TrimSpace(InterpolateTemplate(s, inputs))
			if s != "" {
				projectIDStrs = append(projectIDStrs, s)
			}
		}
	case string:
		// Single project as string template. Comma-separated allowed
		// as a convenience for users authoring without the array UI.
		for _, s := range strings.Split(InterpolateTemplate(v, inputs), ",") {
			s = strings.TrimSpace(s)
			if s != "" {
				projectIDStrs = append(projectIDStrs, s)
			}
		}
	}
	if len(projectIDStrs) == 0 {
		return nil, fmt.Errorf("project_ids is required (at least one)")
	}

	var projectIDs []primitive.ObjectID
	for _, s := range projectIDStrs {
		oid, err := primitive.ObjectIDFromHex(s)
		if err != nil {
			log.Printf("📚 [KNOWLEDGE-SEARCH] Skipping invalid project_id %q: %v", s, err)
			continue
		}
		projectIDs = append(projectIDs, oid)
	}
	if len(projectIDs) == 0 {
		return nil, fmt.Errorf("no valid project_ids supplied")
	}

	queryRaw := getString(config, "query", "")
	if queryRaw == "" {
		return nil, fmt.Errorf("query is required")
	}
	query := strings.TrimSpace(InterpolateTemplate(queryRaw, inputs))
	if query == "" {
		return nil, fmt.Errorf("query interpolated to empty string — check your template binding")
	}

	topK := 5
	if v, ok := config["top_k"]; ok {
		switch n := v.(type) {
		case int:
			topK = n
		case float64:
			topK = int(n)
		}
	}
	rerank := true
	if v, ok := config["rerank"].(bool); ok {
		rerank = v
	}

	log.Printf("📚 [KNOWLEDGE-SEARCH] Block '%s' user=%s projects=%d query=%q top_k=%d rerank=%v",
		block.Name, userID, len(projectIDs), truncForLog(query, 80), topK, rerank)

	hits, err := e.rag.Search(ctx, userID, rag.SearchOptions{
		Query:      query,
		ProjectIDs: projectIDs,
		TopK:       topK,
		Rerank:     rerank,
	})
	if err != nil {
		return nil, fmt.Errorf("search failed: %w", err)
	}

	// Marshal as plain maps so downstream blocks (transform, llm) can
	// reach into fields with template paths like {{this.chunks[0].text}}.
	chunks := make([]map[string]any, 0, len(hits))
	for _, h := range hits {
		chunks = append(chunks, map[string]any{
			"text":       h.Text,
			"score":      h.Score,
			"file_id":    h.FileID,
			"file_name":  h.FileName,
			"chunk_idx":  h.ChunkIdx,
			"page":       h.Page,
			"section":    h.Section,
			"project_id": h.ProjectID,
		})
	}

	return map[string]any{
		"chunks":     chunks,
		"count":      len(chunks),
		"elapsed_ms": time.Since(t0).Milliseconds(),
	}, nil
}

func truncForLog(s string, max int) string {
	if len(s) <= max {
		return s
	}
	return s[:max] + "…"
}
