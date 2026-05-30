package handlers

import (
	"strconv"

	"claraverse/internal/models"
	"claraverse/internal/services"

	"github.com/gofiber/fiber/v2"
	"go.mongodb.org/mongo-driver/bson/primitive"
)

// RoutineHandler handles Dobby's Claw routine endpoints
type RoutineHandler struct {
	routineService *services.RoutineService
	channelService *services.ChannelService
	mcpService     *services.MCPBridgeService
	taskStore      *services.NexusTaskStore
}

// NewRoutineHandler creates a new routine handler
func NewRoutineHandler(routineService *services.RoutineService) *RoutineHandler {
	return &RoutineHandler{
		routineService: routineService,
	}
}

// SetChannelService sets the channel service for status endpoint
func (h *RoutineHandler) SetChannelService(svc *services.ChannelService) {
	h.channelService = svc
}

// SetMCPBridgeService sets the MCP bridge service for status endpoint
func (h *RoutineHandler) SetMCPBridgeService(svc *services.MCPBridgeService) {
	h.mcpService = svc
}

// SetTaskStore sets the task store for routine run history
func (h *RoutineHandler) SetTaskStore(store *services.NexusTaskStore) {
	h.taskStore = store
}

// GetRoutineRuns returns the execution history for a specific routine
func (h *RoutineHandler) GetRoutineRuns(c *fiber.Ctx) error {
	userID := c.Locals("user_id").(string)
	routineIDStr := c.Params("id")

	routineOID, err := primitive.ObjectIDFromHex(routineIDStr)
	if err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "invalid routine ID"})
	}

	if h.taskStore == nil {
		return c.JSON([]interface{}{})
	}

	limit := int64(20)
	if l, err := strconv.ParseInt(c.Query("limit", "20"), 10, 64); err == nil && l > 0 && l <= 100 {
		limit = l
	}

	tasks, err := h.taskStore.GetByRoutineID(c.Context(), userID, routineOID, limit)
	if err != nil {
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": err.Error()})
	}

	// Map to a lean response format
	type RoutineRun struct {
		ID          string  `json:"id"`
		Status      string  `json:"status"`
		Mode        string  `json:"mode"`
		Goal        string  `json:"goal"`
		Summary     string  `json:"summary,omitempty"`
		Error       string  `json:"error,omitempty"`
		CreatedAt   string  `json:"created_at"`
		CompletedAt *string `json:"completed_at,omitempty"`
	}

	runs := make([]RoutineRun, 0, len(tasks))
	for _, t := range tasks {
		run := RoutineRun{
			ID:        t.ID.Hex(),
			Status:    string(t.Status),
			Mode:      t.Mode,
			Goal:      t.Goal,
			Error:     t.Error,
			CreatedAt: t.CreatedAt.Format("2006-01-02T15:04:05Z"),
		}
		if t.Result != nil {
			run.Summary = t.Result.Summary
		}
		if t.CompletedAt != nil {
			s := t.CompletedAt.Format("2006-01-02T15:04:05Z")
			run.CompletedAt = &s
		}
		runs = append(runs, run)
	}

	return c.JSON(runs)
}

// ListRoutines returns all routines for the authenticated user
func (h *RoutineHandler) ListRoutines(c *fiber.Ctx) error {
	userID := c.Locals("user_id").(string)

	routines, err := h.routineService.List(c.Context(), userID)
	if err != nil {
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{
			"error": err.Error(),
		})
	}

	if routines == nil {
		routines = []*models.Routine{}
	}

	return c.JSON(fiber.Map{
		"routines": routines,
	})
}

// CreateRoutine creates a new routine
func (h *RoutineHandler) CreateRoutine(c *fiber.Ctx) error {
	userID := c.Locals("user_id").(string)

	var req models.CreateRoutineRequest
	if err := c.BodyParser(&req); err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{
			"error": "Invalid request body",
		})
	}

	if req.Name == "" || req.Prompt == "" || req.CronExpression == "" || req.Timezone == "" {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{
			"error": "name, prompt, cronExpression, and timezone are required",
		})
	}

	if req.DeliveryMethod == "" {
		req.DeliveryMethod = "store"
	}

	routine, err := h.routineService.Create(c.Context(), userID, &req)
	if err != nil {
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{
			"error": err.Error(),
		})
	}

	return c.Status(fiber.StatusCreated).JSON(routine)
}

// GetRoutine returns a single routine
func (h *RoutineHandler) GetRoutine(c *fiber.Ctx) error {
	userID := c.Locals("user_id").(string)
	routineID := c.Params("id")

	routine, err := h.routineService.GetByID(c.Context(), userID, routineID)
	if err != nil {
		return c.Status(fiber.StatusNotFound).JSON(fiber.Map{
			"error": err.Error(),
		})
	}

	return c.JSON(routine)
}

// UpdateRoutine updates a routine
func (h *RoutineHandler) UpdateRoutine(c *fiber.Ctx) error {
	userID := c.Locals("user_id").(string)
	routineID := c.Params("id")

	var req models.UpdateRoutineRequest
	if err := c.BodyParser(&req); err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{
			"error": "Invalid request body",
		})
	}

	routine, err := h.routineService.Update(c.Context(), userID, routineID, &req)
	if err != nil {
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{
			"error": err.Error(),
		})
	}

	return c.JSON(routine)
}

// DeleteRoutine removes a routine
func (h *RoutineHandler) DeleteRoutine(c *fiber.Ctx) error {
	userID := c.Locals("user_id").(string)
	routineID := c.Params("id")

	if err := h.routineService.Delete(c.Context(), userID, routineID); err != nil {
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{
			"error": err.Error(),
		})
	}

	return c.Status(fiber.StatusNoContent).Send(nil)
}

// TestRoutine runs a routine prompt and returns the result without saving
func (h *RoutineHandler) TestRoutine(c *fiber.Ctx) error {
	userID := c.Locals("user_id").(string)

	var req models.TestRoutineRequest
	if err := c.BodyParser(&req); err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{
			"error": "Invalid request body",
		})
	}

	if req.Prompt == "" {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{
			"error": "prompt is required",
		})
	}

	result, err := h.routineService.TestRoutine(c.Context(), userID, &req)
	if err != nil {
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{
			"error": err.Error(),
		})
	}

	return c.JSON(fiber.Map{
		"result": result,
	})
}

// TriggerRoutine executes a routine immediately
func (h *RoutineHandler) TriggerRoutine(c *fiber.Ctx) error {
	userID := c.Locals("user_id").(string)
	routineID := c.Params("id")

	if err := h.routineService.Trigger(c.Context(), userID, routineID); err != nil {
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{
			"error": err.Error(),
		})
	}

	return c.JSON(fiber.Map{
		"message": "Routine triggered successfully",
	})
}

// GetClawStatus returns the combined Dobby's Claw status
func (h *RoutineHandler) GetClawStatus(c *fiber.Ctx) error {
	userID := c.Locals("user_id").(string)

	status := h.routineService.GetUserStatus(c.Context(), userID, h.channelService, h.mcpService)

	return c.JSON(status)
}

// ListMCPServers returns MCP server info from the user's connected bridge
func (h *RoutineHandler) ListMCPServers(c *fiber.Ctx) error {
	userID := c.Locals("user_id").(string)

	if h.mcpService == nil {
		return c.JSON(fiber.Map{"servers": []interface{}{}})
	}

	conn, exists := h.mcpService.GetUserConnection(userID)
	if !exists || conn == nil || !conn.IsActive {
		return c.JSON(fiber.Map{"servers": []interface{}{}})
	}

	// Return tools grouped for the frontend
	return c.JSON(fiber.Map{
		"connected":  true,
		"platform":   conn.Platform,
		"tool_count": len(conn.Tools),
		"tools":      conn.Tools,
	})
}

// AddMCPServer sends an add_server command to the user's MCP bridge client
func (h *RoutineHandler) AddMCPServer(c *fiber.Ctx) error {
	userID := c.Locals("user_id").(string)

	if h.mcpService == nil {
		return c.Status(fiber.StatusServiceUnavailable).JSON(fiber.Map{
			"error": "MCP service not available",
		})
	}

	var req models.MCPServerConfig
	if err := c.BodyParser(&req); err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{
			"error": "Invalid request body",
		})
	}

	if req.Name == "" || req.Command == "" {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{
			"error": "name and command are required",
		})
	}

	payload := map[string]interface{}{
		"name":        req.Name,
		"description": req.Description,
		"command":     req.Command,
		"args":        req.Args,
	}

	if err := h.mcpService.SendServerCommand(userID, "add_server", payload); err != nil {
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{
			"error": err.Error(),
		})
	}

	return c.Status(fiber.StatusCreated).JSON(fiber.Map{
		"message": "Server added successfully",
	})
}

// UpdateMCPServer toggles an MCP server on the user's bridge client
func (h *RoutineHandler) UpdateMCPServer(c *fiber.Ctx) error {
	userID := c.Locals("user_id").(string)
	serverName := c.Params("name")

	if h.mcpService == nil {
		return c.Status(fiber.StatusServiceUnavailable).JSON(fiber.Map{
			"error": "MCP service not available",
		})
	}

	var req struct {
		Enabled *bool `json:"enabled"`
	}
	if err := c.BodyParser(&req); err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{
			"error": "Invalid request body",
		})
	}

	if req.Enabled == nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{
			"error": "enabled field is required",
		})
	}

	payload := map[string]interface{}{
		"name":    serverName,
		"enabled": *req.Enabled,
	}

	if err := h.mcpService.SendServerCommand(userID, "toggle_server", payload); err != nil {
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{
			"error": err.Error(),
		})
	}

	return c.JSON(fiber.Map{
		"message": "Server updated successfully",
	})
}

// RemoveMCPServer removes an MCP server from the user's bridge client
func (h *RoutineHandler) RemoveMCPServer(c *fiber.Ctx) error {
	userID := c.Locals("user_id").(string)
	serverName := c.Params("name")

	if h.mcpService == nil {
		return c.Status(fiber.StatusServiceUnavailable).JSON(fiber.Map{
			"error": "MCP service not available",
		})
	}

	payload := map[string]interface{}{
		"name": serverName,
	}

	if err := h.mcpService.SendServerCommand(userID, "remove_server", payload); err != nil {
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{
			"error": err.Error(),
		})
	}

	return c.JSON(fiber.Map{
		"message": "Server removed successfully",
	})
}
