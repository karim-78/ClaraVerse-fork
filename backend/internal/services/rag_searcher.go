package services

import (
	"context"

	"claraverse/internal/models"
	"claraverse/internal/services/rag"
	"claraverse/internal/tools"

	"go.mongodb.org/mongo-driver/bson/primitive"
)

// RAGSearcher is the narrow interface the orchestrator + daemon
// runner consume from the RAG layer. It deliberately avoids
// importing the full rag.Service into anything that doesn't
// actually need it — keeps the dependency graph one-way (services
// depend on rag, never the other way) and makes test fakes trivial.
//
// The two methods cover the only two things the orchestrator cares
// about: "does this project have any indexed knowledge?" (gates
// whether to surface the tool at all) and "build me a tool instance
// bound to this user + these projects" (the actual invocation
// adapter, since the search_knowledge tool is per-user-per-context).
type RAGSearcher interface {
	HasKnowledge(ctx context.Context, userID string, projectID primitive.ObjectID) bool
	BuildSearchTool(userID string, defaultProjectIDs []string) *tools.Tool
}

// ragSearcherAdapter wraps rag.Service to satisfy RAGSearcher.
// Lives in services/ rather than rag/ so the rag package doesn't
// have to know about the services/tools imports. Adapter pattern
// keeps the rag package import-clean.
type ragSearcherAdapter struct {
	svc *rag.Service
}

// NewRAGSearcher returns a RAGSearcher backed by the live rag.Service.
// main.go calls this once and threads the result into Cortex + daemon
// configs.
func NewRAGSearcher(svc *rag.Service) RAGSearcher {
	if svc == nil {
		return nil // nil is a valid "RAG not wired" signal everywhere
	}
	return &ragSearcherAdapter{svc: svc}
}

func (a *ragSearcherAdapter) HasKnowledge(ctx context.Context, userID string, projectID primitive.ObjectID) bool {
	return a.svc.HasKnowledge(ctx, userID, projectID)
}

func (a *ragSearcherAdapter) BuildSearchTool(userID string, defaultProjectIDs []string) *tools.Tool {
	return tools.NewSearchKnowledgeTool(a.svc, userID, defaultProjectIDs)
}

// Ensure the model type stays linked so the import isn't pruned —
// the package gets used indirectly via cortex_orchestrator's calls,
// but Go won't keep this import without a reference.
var _ models.NexusTask
