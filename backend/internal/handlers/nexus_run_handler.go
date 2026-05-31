package handlers

// Headless Nexus task runner.
//
// What this exists for:
//
// The WebSocket-driven flow is great for the live UI but useless for:
//   - scripting / CI tests
//   - third-party automations that just want to fire a task and read the
//     result back
//   - debugging — running the same prompt repeatedly through curl is the
//     fastest way to isolate whether a problem is in the agent loop or
//     the frontend
//
// Two endpoints:
//
//   POST /api/nexus/run
//     Body: { prompt, project_id?, mode?, model_id?, skill_ids? }
//     Returns immediately with { task_id, status_url }.
//     The orchestration runs to completion in the background; poll
//     /api/nexus/tasks/:id to see when it finishes.
//
//   POST /api/nexus/run/sync
//     Same body. Blocks until the task completes OR the timeout fires
//     (default 5 min, max 10). Returns the final task state inline.
//     Use this from scripts; never from a UI thread.
//
// Both endpoints route through CortexService.HandleUserMessage — same
// path the WebSocket uses — so any classification / daemon / synthesis
// behaviour you see in the UI also happens here. That's the whole point:
// the headless tester proves the *system* works, not just the *transport*.

import (
	"context"
	"fmt"
	"time"

	"claraverse/internal/services"

	"github.com/gofiber/fiber/v2"
	"go.mongodb.org/mongo-driver/bson/primitive"
)

// NexusRunHandler is the headless fire-task / poll-status endpoint set.
type NexusRunHandler struct {
	cortex       *services.CortexService
	taskStore    *services.NexusTaskStore
	sessionStore *services.NexusSessionStore
}

// NewNexusRunHandler wires the handler.
func NewNexusRunHandler(
	cortex *services.CortexService,
	taskStore *services.NexusTaskStore,
	sessionStore *services.NexusSessionStore,
) *NexusRunHandler {
	return &NexusRunHandler{
		cortex:       cortex,
		taskStore:    taskStore,
		sessionStore: sessionStore,
	}
}

// runRequest is the shared shape for both /run and /run/sync.
type runRequest struct {
	Prompt    string   `json:"prompt"`     // required — what the user wants done
	ProjectID string   `json:"project_id"` // optional — which kanban project
	Mode      string   `json:"mode"`       // optional — "daemon" / "multi_daemon" to force; empty = auto-classify
	ModelID   string   `json:"model_id"`   // optional — override default model
	SkillIDs  []string `json:"skill_ids"`  // optional — explicit skills to attach
	// Sync-only: how long to wait for completion before giving up.
	TimeoutSeconds int `json:"timeout_seconds"`
}

// Run fires a task and returns its ID immediately. The cortex pipeline
// runs in a background goroutine with its own context, so the request can
// finish in milliseconds while the daemons keep working.
//
// Caller polls GET /api/nexus/tasks/:id (existing endpoint) to see status,
// or subscribes to the WebSocket if they want live events.
func (h *NexusRunHandler) Run(c *fiber.Ctx) error {
	userID, ok := c.Locals("user_id").(string)
	if !ok || userID == "" {
		return c.Status(401).JSON(fiber.Map{"error": "authentication required"})
	}

	var body runRequest
	if err := c.BodyParser(&body); err != nil {
		return c.Status(400).JSON(fiber.Map{"error": "invalid request body"})
	}
	if body.Prompt == "" {
		return c.Status(400).JSON(fiber.Map{"error": "prompt required"})
	}

	// Pre-create the task ID so we can return it before HandleUserMessage
	// has even started. HandleUserMessage will reuse the task this way
	// (or create its own — we use the session-lookup approach below).
	//
	// Actually HandleUserMessage doesn't accept a pre-created task ID, so
	// we let it create its own and return a stub response with a
	// polling URL the client can hit a moment later.
	//
	// To get the task ID synchronously we'd need to refactor
	// HandleUserMessage. Simpler: fire the call, then return immediately;
	// the client polls /api/nexus/tasks to find the newest task created
	// after fire-time. We expose the fire-time as 'fired_at_ms' so the
	// polling filter is precise.

	firedAt := time.Now().UTC()

	// Dispatch on a fresh background context — survives both the HTTP
	// request lifecycle and any client disconnect. 30 min ceiling matches
	// HandleUserMessage's internal cap.
	go func() {
		bgCtx, cancel := context.WithTimeout(context.Background(), 30*time.Minute)
		defer cancel()
		h.cortex.HandleUserMessage(
			bgCtx,
			userID,
			primitive.NilObjectID, // session: HandleUserMessage will pick the user's session
			body.Prompt,
			body.ModelID,
			body.Mode,        // mode override
			"",               // templateID
			body.ProjectID,
			"",               // followUpTaskID
			primitive.NilObjectID, // routineID
			body.SkillIDs,
			nil, // saveIDs
		)
	}()

	return c.JSON(fiber.Map{
		"status":      "fired",
		"fired_at":    firedAt,
		"fired_at_ms": firedAt.UnixMilli(),
		"hint": "Poll GET /api/nexus/tasks?limit=5 to find the newest task " +
			"(filter by created_at >= fired_at), then GET /api/nexus/tasks/:id " +
			"for full state. Or subscribe to /ws/nexus for live events.",
	})
}

// RunSync fires a task and BLOCKS until it reaches a terminal state
// (completed / failed / cancelled) or the timeout fires. Returns the final
// task document inline.
//
// Designed for scripted tests: one curl call, get the result back, assert
// on it. Not for use from a UI thread.
//
// Polling is intentionally inside this handler (vs the orchestrator
// publishing a sync channel) because it keeps the implementation
// independent of the WebSocket event bus — the test path doesn't share
// behaviour with the live UI path and won't mask its bugs.
func (h *NexusRunHandler) RunSync(c *fiber.Ctx) error {
	userID, ok := c.Locals("user_id").(string)
	if !ok || userID == "" {
		return c.Status(401).JSON(fiber.Map{"error": "authentication required"})
	}

	var body runRequest
	if err := c.BodyParser(&body); err != nil {
		return c.Status(400).JSON(fiber.Map{"error": "invalid request body"})
	}
	if body.Prompt == "" {
		return c.Status(400).JSON(fiber.Map{"error": "prompt required"})
	}

	timeout := time.Duration(body.TimeoutSeconds) * time.Second
	if timeout <= 0 {
		timeout = 5 * time.Minute
	}
	// Match the 30-min HandleUserMessage ceiling. Long deep-research
	// tasks need the headroom; the inner exec context still caps them.
	if timeout > 30*time.Minute {
		timeout = 30 * time.Minute
	}

	firedAt := time.Now().UTC()

	// Fire on a background context so an unhappy client disconnect doesn't
	// kill the orchestration mid-flight.
	go func() {
		bgCtx, cancel := context.WithTimeout(context.Background(), timeout)
		defer cancel()
		h.cortex.HandleUserMessage(
			bgCtx,
			userID,
			primitive.NilObjectID,
			body.Prompt,
			body.ModelID,
			body.Mode,
			"",
			body.ProjectID,
			"",
			primitive.NilObjectID,
			body.SkillIDs,
			nil,
		)
	}()

	// Find the freshly-created task by scanning the user's recent tasks
	// for one created at or after firedAt. The orchestrator inserts the
	// task very early in HandleUserMessage so this usually resolves within
	// a few hundred ms.
	session, err := h.sessionStore.GetOrCreate(c.Context(), userID)
	if err != nil {
		return c.Status(500).JSON(fiber.Map{"error": fmt.Sprintf("session: %v", err)})
	}

	deadline := time.Now().Add(timeout)
	var taskID primitive.ObjectID
	for time.Now().Before(deadline) {
		time.Sleep(300 * time.Millisecond)
		tasks, err := h.taskStore.GetRecentBySession(c.Context(), userID, session.ID, 5)
		if err != nil {
			continue
		}
		for _, t := range tasks {
			if t.CreatedAt.After(firedAt.Add(-1*time.Second)) {
				taskID = t.ID
				break
			}
		}
		if !taskID.IsZero() {
			break
		}
	}

	if taskID.IsZero() {
		return c.Status(504).JSON(fiber.Map{
			"error":   "task did not appear within timeout",
			"timeout": timeout.String(),
		})
	}

	// Now poll the task to completion.
	for time.Now().Before(deadline) {
		task, err := h.taskStore.GetByID(c.Context(), userID, taskID)
		if err != nil {
			return c.Status(500).JSON(fiber.Map{"error": err.Error(), "task_id": taskID.Hex()})
		}
		if task == nil {
			return c.Status(404).JSON(fiber.Map{"error": "task vanished", "task_id": taskID.Hex()})
		}
		switch task.Status {
		case "completed", "failed", "cancelled":
			return c.JSON(fiber.Map{
				"task":        task,
				"fired_at":    firedAt,
				"finished_at": time.Now().UTC(),
				"duration_ms": time.Since(firedAt).Milliseconds(),
			})
		}
		time.Sleep(500 * time.Millisecond)
	}

	// Timeout — return what we have so the caller can decide.
	task, _ := h.taskStore.GetByID(c.Context(), userID, taskID)
	return c.Status(504).JSON(fiber.Map{
		"error":      "task did not complete within timeout",
		"timeout":    timeout.String(),
		"task":       task,
		"hint":       "Task may still be running. GET /api/nexus/tasks/:id to keep polling.",
	})
}
