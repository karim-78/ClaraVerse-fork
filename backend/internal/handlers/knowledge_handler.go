// Package handlers exposes the RAG knowledge base over HTTP.
//
// The KnowledgeHandler is the only public surface for managing files
// in a project's knowledge base. Tools and workflows go through
// rag.Service directly inside the backend — they don't loop back
// through HTTP. Everything is project-scoped and userId-checked at
// the boundary; rag.Service applies the same checks again as defense
// in depth.
package handlers

import (
	"fmt"
	"io"
	"net/http"

	"claraverse/internal/services/rag"

	"github.com/gofiber/fiber/v2"
	"go.mongodb.org/mongo-driver/bson/primitive"
)

// KnowledgeHandler exposes the per-project knowledge base.
type KnowledgeHandler struct {
	rag *rag.Service
}

// NewKnowledgeHandler wires a handler. Routes are mounted by the
// main router under /api/projects/:project_id/knowledge.
func NewKnowledgeHandler(rag *rag.Service) *KnowledgeHandler {
	return &KnowledgeHandler{rag: rag}
}

// projectFromCtx parses + validates the :project_id path param.
// Returns the parsed ObjectID + user_id from the auth middleware.
// Anything wrong → handler writes the error response and returns ok=false.
func (h *KnowledgeHandler) projectFromCtx(c *fiber.Ctx) (primitive.ObjectID, string, bool) {
	userID, _ := c.Locals("user_id").(string)
	if userID == "" {
		_ = c.Status(http.StatusUnauthorized).JSON(fiber.Map{"error": "unauthorized"})
		return primitive.NilObjectID, "", false
	}
	pidHex := c.Params("project_id")
	pid, err := primitive.ObjectIDFromHex(pidHex)
	if err != nil {
		_ = c.Status(http.StatusBadRequest).JSON(fiber.Map{"error": "invalid project_id"})
		return primitive.NilObjectID, "", false
	}
	return pid, userID, true
}

// ListFiles → GET /api/projects/:project_id/knowledge/files
func (h *KnowledgeHandler) ListFiles(c *fiber.Ctx) error {
	pid, userID, ok := h.projectFromCtx(c)
	if !ok {
		return nil
	}
	files, err := h.rag.ListFiles(c.Context(), userID, pid)
	if err != nil {
		return c.Status(500).JSON(fiber.Map{"error": err.Error()})
	}
	return c.JSON(fiber.Map{"files": files})
}

// UploadFile → POST /api/projects/:project_id/knowledge/files
//
// Multipart form with a single file under "file". Returns the created
// file record immediately (status="queued"); the worker processes
// asynchronously and the UI polls for completion (or uses the WS
// channel — wired in a later commit).
//
// Limits enforced here, not in rag.Service:
//   - 50 MB per file (large enough for real PDFs, small enough that
//     a malicious upload can't OOM the parser)
//   - One file per request (clients can fan out for batch uploads)
func (h *KnowledgeHandler) UploadFile(c *fiber.Ctx) error {
	pid, userID, ok := h.projectFromCtx(c)
	if !ok {
		return nil
	}

	fh, err := c.FormFile("file")
	if err != nil {
		return c.Status(400).JSON(fiber.Map{"error": "missing 'file' form field"})
	}
	const maxBytes = 50 * 1024 * 1024
	if fh.Size > maxBytes {
		return c.Status(413).JSON(fiber.Map{
			"error":    "file too large",
			"max_size": maxBytes,
			"got_size": fh.Size,
		})
	}

	f, err := fh.Open()
	if err != nil {
		return c.Status(500).JSON(fiber.Map{"error": fmt.Sprintf("open: %v", err)})
	}
	defer f.Close()
	data, err := io.ReadAll(f)
	if err != nil {
		return c.Status(500).JSON(fiber.Map{"error": fmt.Sprintf("read: %v", err)})
	}

	contentType := fh.Header.Get("Content-Type")
	file, err := h.rag.IngestUpload(c.Context(), userID, pid, fh.Filename, contentType, data, "")
	if err != nil {
		return c.Status(500).JSON(fiber.Map{"error": err.Error()})
	}
	return c.Status(201).JSON(file)
}

// DeleteFile → DELETE /api/projects/:project_id/knowledge/files/:file_id
func (h *KnowledgeHandler) DeleteFile(c *fiber.Ctx) error {
	pid, userID, ok := h.projectFromCtx(c)
	if !ok {
		return nil
	}
	fileIDHex := c.Params("file_id")
	fileID, err := primitive.ObjectIDFromHex(fileIDHex)
	if err != nil {
		return c.Status(400).JSON(fiber.Map{"error": "invalid file_id"})
	}
	if err := h.rag.DeleteFile(c.Context(), userID, fileID); err != nil {
		return c.Status(500).JSON(fiber.Map{"error": err.Error()})
	}
	_ = pid // referenced for path-param presence; ownership enforced in Service via userID
	return c.JSON(fiber.Map{"deleted": fileIDHex})
}

// Search → POST /api/projects/:project_id/knowledge/search
//
// Body: { query: string, top_k?: int, rerank?: bool, project_ids?: [string] }
// project_ids in the body overrides the path :project_id — useful for
// the chat multi-project search where the caller is asking "search
// these N projects" and the path is just one of them.
func (h *KnowledgeHandler) Search(c *fiber.Ctx) error {
	pid, userID, ok := h.projectFromCtx(c)
	if !ok {
		return nil
	}
	var body struct {
		Query      string   `json:"query"`
		TopK       int      `json:"top_k"`
		Rerank     *bool    `json:"rerank"`
		ProjectIDs []string `json:"project_ids"`
	}
	if err := c.BodyParser(&body); err != nil {
		return c.Status(400).JSON(fiber.Map{"error": "invalid body"})
	}
	projectIDs := []primitive.ObjectID{pid}
	if len(body.ProjectIDs) > 0 {
		projectIDs = projectIDs[:0]
		for _, s := range body.ProjectIDs {
			oid, err := primitive.ObjectIDFromHex(s)
			if err != nil {
				continue
			}
			projectIDs = append(projectIDs, oid)
		}
		if len(projectIDs) == 0 {
			return c.Status(400).JSON(fiber.Map{"error": "no valid project_ids"})
		}
	}
	rerank := true
	if body.Rerank != nil {
		rerank = *body.Rerank
	}
	hits, err := h.rag.Search(c.Context(), userID, rag.SearchOptions{
		Query:      body.Query,
		ProjectIDs: projectIDs,
		TopK:       body.TopK,
		Rerank:     rerank,
	})
	if err != nil {
		return c.Status(500).JSON(fiber.Map{"error": err.Error()})
	}
	return c.JSON(fiber.Map{"hits": hits})
}

// Health → GET /api/knowledge/health
//
// Surfaces the embeddings sidecar status so the admin UI can warn
// "model still loading, first ingest may take 60s". Not project-scoped.
func (h *KnowledgeHandler) Health(c *fiber.Ctx) error {
	info, err := h.rag.EmbeddingsClient().Health(c.Context())
	if err != nil {
		return c.Status(503).JSON(fiber.Map{"error": err.Error(), "available": false})
	}
	return c.JSON(info)
}
