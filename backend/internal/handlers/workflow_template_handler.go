package handlers

// REST endpoints for the workflow template gallery:
//
//   GET  /api/workflow-templates              — list all templates (optional ?category=)
//   GET  /api/workflow-templates/:id          — single template detail
//   POST /api/workflow-templates/:id/clone    — clone into a new agent+workflow for the user
//
// Auth model:
//   - List + Get are public to authenticated users (gallery browsing).
//   - Clone requires an authenticated user; the cloned workflow lands under
//     their account.
//   - There are no per-template ACLs — built-in templates are global and
//     intended to be discoverable by everyone.

import (
	"claraverse/internal/services"

	"github.com/gofiber/fiber/v2"
)

type WorkflowTemplateHandler struct {
	store *services.WorkflowTemplateStore
}

func NewWorkflowTemplateHandler(store *services.WorkflowTemplateStore) *WorkflowTemplateHandler {
	return &WorkflowTemplateHandler{store: store}
}

// List returns the gallery. ?category= filters by category for "Reporting"
// / "Communication" / etc. tabs in the UI.
func (h *WorkflowTemplateHandler) List(c *fiber.Ctx) error {
	category := c.Query("category", "")
	tmpls, err := h.store.List(c.Context(), category)
	if err != nil {
		return c.Status(500).JSON(fiber.Map{"error": err.Error()})
	}
	return c.JSON(fiber.Map{"templates": tmpls, "count": len(tmpls)})
}

// Get returns a single template detail — used by the gallery's "Preview"
// modal before the user commits to cloning.
func (h *WorkflowTemplateHandler) Get(c *fiber.Ctx) error {
	id := c.Params("id")
	tmpl, err := h.store.GetByID(c.Context(), id)
	if err != nil {
		return c.Status(400).JSON(fiber.Map{"error": err.Error()})
	}
	if tmpl == nil {
		return c.Status(404).JSON(fiber.Map{"error": "template not found"})
	}
	return c.JSON(tmpl)
}

// Clone materialises the template into a new agent+workflow under the
// authenticated user. Optional JSON body: {"name": "Custom name"}.
//
// Returns the created agent + workflow so the frontend can redirect
// straight into the editor for the new workflow.
func (h *WorkflowTemplateHandler) Clone(c *fiber.Ctx) error {
	id := c.Params("id")
	userID, ok := c.Locals("userID").(string)
	if !ok || userID == "" {
		return c.Status(401).JSON(fiber.Map{"error": "authentication required"})
	}

	var body struct {
		Name string `json:"name"`
	}
	// Body is optional; ignore parse errors so a clone with no body still works.
	_ = c.BodyParser(&body)

	agent, wf, err := h.store.CloneForUser(c.Context(), id, userID, body.Name)
	if err != nil {
		return c.Status(500).JSON(fiber.Map{"error": err.Error()})
	}
	return c.JSON(fiber.Map{
		"agent":    agent,
		"workflow": wf,
	})
}
