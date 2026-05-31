package handlers

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"sync"
	"time"

	"claraverse/internal/models"
	"claraverse/internal/services"

	"github.com/gofiber/contrib/websocket"
	"github.com/google/uuid"
	"go.mongodb.org/mongo-driver/bson/primitive"
)

// NexusWebSocketHandler handles WebSocket connections for the Nexus multi-agent system
type NexusWebSocketHandler struct {
	cortexService *services.CortexService
	sessionStore  *services.NexusSessionStore
	taskStore     *services.NexusTaskStore
	daemonPool    *services.DaemonPool
	personaStore  *services.PersonaService
	engramService *services.EngramService
	eventBus      *services.NexusEventBus
	mcpBridge     *services.MCPBridgeService
}

// NewNexusWebSocketHandler creates a new Nexus WebSocket handler
func NewNexusWebSocketHandler(
	cortexService *services.CortexService,
	sessionStore *services.NexusSessionStore,
	taskStore *services.NexusTaskStore,
	daemonPool *services.DaemonPool,
	personaStore *services.PersonaService,
	engramService *services.EngramService,
	eventBus *services.NexusEventBus,
	mcpBridge *services.MCPBridgeService,
) *NexusWebSocketHandler {
	return &NexusWebSocketHandler{
		cortexService: cortexService,
		sessionStore:  sessionStore,
		taskStore:     taskStore,
		daemonPool:    daemonPool,
		personaStore:  personaStore,
		engramService: engramService,
		eventBus:      eventBus,
		mcpBridge:     mcpBridge,
	}
}

// NexusClientMessage represents a message from the client
type NexusClientMessage struct {
	Type     string `json:"type"`
	Content  string `json:"content,omitempty"`
	DaemonID string `json:"daemon_id,omitempty"`
	TaskID   string `json:"task_id,omitempty"`
	SkillID  string `json:"skill_id,omitempty"`
	Answer   string `json:"answer,omitempty"`
	ModelID  string `json:"model_id,omitempty"`
	Status   string `json:"status,omitempty"`

	// Task creation hints from AddCardPanel
	DaemonMode string   `json:"daemon_mode,omitempty"` // "daemon", "multi_daemon" — overrides LLM classification
	TemplateID string   `json:"template_id,omitempty"` // Direct daemon template — skips classification entirely
	Tools      []string `json:"tools,omitempty"`
	SkillIDs   []string `json:"skill_ids,omitempty"`   // Explicit skill IDs to attach to daemon
	SaveIDs    []string `json:"save_ids,omitempty"`   // Saved items to attach as context
	Priority   int      `json:"priority,omitempty"`
	ProjectID  string   `json:"project_id,omitempty"` // Assign task to a project

	// Persona updates
	Facts []NexusPersonaFactUpdate `json:"facts,omitempty"`
}

// NexusPersonaFactUpdate represents a persona fact update from the client
type NexusPersonaFactUpdate struct {
	ID         string  `json:"id,omitempty"`
	Category   string  `json:"category"`
	Content    string  `json:"content"`
	Action     string  `json:"action"` // "create", "update", "delete"
	Confidence float64 `json:"confidence,omitempty"`
}

// NexusServerMessage represents a message sent to the client
type NexusServerMessage struct {
	Type string      `json:"type"`
	Data interface{} `json:"data,omitempty"`
}

// Handle is the WebSocket handler for /ws/nexus
func (h *NexusWebSocketHandler) Handle(c *websocket.Conn) {
	rawUserID, ok := c.Locals("user_id").(string)
	if !ok || rawUserID == "" {
		log.Printf("[NEXUS-WS] Connection rejected: missing or invalid user_id")
		c.WriteJSON(NexusServerMessage{Type: "error", Data: map[string]string{"message": "unauthorized"}})
		return
	}
	userID := rawUserID
	connID := uuid.New().String()

	log.Printf("[NEXUS-WS] Connection opened: %s (user: %s)", connID, userID)

	ctx := context.Background()

	// Create write channel and done signal
	writeChan := make(chan NexusServerMessage, 100)
	done := make(chan struct{})
	var closeOnce sync.Once
	closeDone := func() { closeOnce.Do(func() { close(done) }) }

	// Write mutex — serializes WebSocket writes (JSON messages + protocol pings)
	var writeMu sync.Mutex

	// Write loop — sole consumer of writeChan, exits on done signal
	go func() {
		defer func() {
			if r := recover(); r != nil {
				log.Printf("[NEXUS-WS] Write loop recovered for %s: %v", connID, r)
			}
		}()
		for {
			select {
			case <-done:
				return
			case msg := <-writeChan:
				writeMu.Lock()
				err := c.WriteJSON(msg)
				writeMu.Unlock()
				if err != nil {
					log.Printf("[NEXUS-WS] Write error for %s: %v", connID, err)
					return
				}
			}
		}
	}()

	// Ping loop — uses write mutex to avoid concurrent writes
	go func() {
		defer func() {
			if r := recover(); r != nil {
				log.Printf("[NEXUS-WS] Ping loop recovered for %s: %v", connID, r)
			}
		}()
		ticker := time.NewTicker(30 * time.Second)
		defer ticker.Stop()
		for {
			select {
			case <-ticker.C:
				writeMu.Lock()
				err := c.WriteMessage(websocket.PingMessage, nil)
				writeMu.Unlock()
				if err != nil {
					return
				}
			case <-done:
				return
			}
		}
	}()

	// Subscribe to EventBus — receive all Nexus events for this user
	eventCh := h.eventBus.Subscribe(userID, connID, 200)

	// EventBus → WebSocket forwarder
	go func() {
		defer func() {
			if r := recover(); r != nil {
				log.Printf("[NEXUS-WS] Event forwarder recovered for %s: %v", connID, r)
			}
		}()
		for {
			select {
			case <-done:
				return
			case event := <-eventCh:
				select {
				case <-done:
					return
				case writeChan <- NexusServerMessage{Type: event.Type, Data: event.Data}:
				}
			}
		}
	}()

	defer func() {
		closeDone()
		h.eventBus.Unsubscribe(userID, connID)
		log.Printf("[NEXUS-WS] Connection closed: %s", connID)
	}()

	// Send connected message + initial session state
	session, _ := h.sessionStore.GetOrCreate(ctx, userID)
	writeChan <- NexusServerMessage{
		Type: "connected",
		Data: map[string]interface{}{
			"session_id": session.ID,
		},
	}

	h.sendSessionState(ctx, userID, session, writeChan)

	// Drain any events that arrived while user was disconnected
	h.sendMissedUpdates(ctx, userID, writeChan)

	// Read loop
	for {
		_, msg, err := c.ReadMessage()
		if err != nil {
			if websocket.IsCloseError(err, websocket.CloseNormalClosure, websocket.CloseGoingAway) {
				break
			}
			log.Printf("[NEXUS-WS] Read error for %s: %v", connID, err)
			break
		}

		var clientMsg NexusClientMessage
		if err := json.Unmarshal(msg, &clientMsg); err != nil {
			writeChan <- NexusServerMessage{
				Type: "error",
				Data: map[string]string{"message": "invalid message format"},
			}
			continue
		}

		switch clientMsg.Type {
		case "ping":
			writeChan <- NexusServerMessage{Type: "pong"}

		case "send_message":
			h.handleSendMessage(ctx, userID, session.ID, clientMsg)

		case "cancel_daemon":
			h.handleCancelDaemon(ctx, userID, clientMsg, writeChan)

		case "cancel_all":
			h.handleCancelAll(ctx, userID, writeChan)

		case "cancel_task":
			h.handleCancelTask(ctx, userID, clientMsg, writeChan)

		case "retry_task":
			h.handleRetryTask(ctx, userID, session.ID, clientMsg, writeChan)

		case "get_session":
			h.sendSessionState(ctx, userID, session, writeChan)

		case "explain_task":
			h.handleExplainTask(ctx, userID, clientMsg, writeChan)

		case "get_daemon_detail":
			h.handleGetDaemonDetail(ctx, userID, clientMsg, writeChan)

		case "pin_skill":
			h.handlePinSkill(ctx, userID, clientMsg, writeChan)

		case "unpin_skill":
			h.handleUnpinSkill(ctx, userID, clientMsg, writeChan)

		case "update_task_status":
			h.handleUpdateTaskStatus(ctx, userID, clientMsg, writeChan)

		case "update_persona":
			h.handleUpdatePersona(ctx, userID, clientMsg, writeChan)

		default:
			writeChan <- NexusServerMessage{
				Type: "error",
				Data: map[string]string{"message": "unknown message type: " + clientMsg.Type},
			}
		}
	}
}

// handleSendMessage processes a user message through Cortex.
// Cortex runs on a BACKGROUND context — daemons survive WebSocket disconnection.
// Events flow through the EventBus, not through a direct channel.
func (h *NexusWebSocketHandler) handleSendMessage(
	ctx context.Context,
	userID string,
	sessionID primitive.ObjectID,
	msg NexusClientMessage,
) {
	if msg.Content == "" {
		h.eventBus.Publish(userID, services.NexusEvent{
			Type: "error",
			Data: map[string]string{"message": "empty message"},
		})
		return
	}

	content := msg.Content

	// If a task_id is provided, inject the original task's context as a preamble
	// so Cortex can treat this as a follow-up rather than a brand-new request.
	if msg.TaskID != "" {
		taskOID, err := primitive.ObjectIDFromHex(msg.TaskID)
		if err == nil {
			task, err := h.taskStore.GetByID(ctx, userID, taskOID)
			if err == nil && task != nil {
				var taskContext string
				if task.Result != nil && task.Result.Summary != "" {
					summary := task.Result.Summary
					if len(summary) > 500 {
						summary = summary[:497] + "..."
					}
					taskContext = fmt.Sprintf(
						"[Follow-up on completed task: %q — Result: %s]\n\n%s",
						task.Goal, summary, content,
					)
				} else if task.Error != "" {
					taskContext = fmt.Sprintf(
						"[Follow-up on failed task: %q — Error: %s]\n\n%s",
						task.Goal, task.Error, content,
					)
				} else {
					taskContext = fmt.Sprintf(
						"[Follow-up on task: %q (status: %s)]\n\n%s",
						task.Goal, string(task.Status), content,
					)
				}
				content = taskContext
			}
		}
	}

	// Fire and forget — Cortex runs on a background context with its own 10min timeout.
	// Daemons will complete even if this WebSocket connection closes.
	go h.cortexService.HandleUserMessage(
		context.Background(), // NOT the WS context — daemon execution survives disconnection
		userID,
		sessionID,
		content,
		msg.ModelID,
		msg.DaemonMode,       // User's explicit mode override from AddCardPanel (empty = auto-classify)
		msg.TemplateID,       // Direct template selection — skips classification entirely
		msg.ProjectID,        // Assign task to a project (empty = inbox)
		msg.TaskID,           // Follow-up: reuse existing task (empty = new task)
		primitive.NilObjectID, // Not a routine execution
		msg.SkillIDs,         // Explicit skill IDs from AddCardPanel (nil = auto-resolve)
		msg.SaveIDs,          // Saved items to attach as reference context
	)
}

// handleCancelDaemon cancels a specific daemon
func (h *NexusWebSocketHandler) handleCancelDaemon(
	ctx context.Context,
	userID string,
	msg NexusClientMessage,
	writeChan chan NexusServerMessage,
) {
	if msg.DaemonID == "" {
		writeChan <- NexusServerMessage{
			Type: "error",
			Data: map[string]string{"message": "missing daemon_id"},
		}
		return
	}

	if err := h.daemonPool.CancelForUser(ctx, userID, msg.DaemonID); err != nil {
		writeChan <- NexusServerMessage{
			Type: "error",
			Data: map[string]string{"message": "daemon not found"},
		}
		return
	}

	daemonOID, err := primitive.ObjectIDFromHex(msg.DaemonID)
	if err == nil {
		_ = h.daemonPool.UpdateStatus(ctx, userID, daemonOID, "failed", "cancelled", 0)
		_ = h.sessionStore.RemoveActiveDaemon(ctx, userID, daemonOID)
	}

	writeChan <- NexusServerMessage{
		Type: "daemon_cancelled",
		Data: map[string]string{"daemon_id": msg.DaemonID},
	}
}

// handleCancelAll cancels all active daemons for the user
func (h *NexusWebSocketHandler) handleCancelAll(
	ctx context.Context,
	userID string,
	writeChan chan NexusServerMessage,
) {
	h.daemonPool.CancelAllForUser(ctx, userID)
	writeChan <- NexusServerMessage{
		Type: "all_cancelled",
	}
}

// handleCancelTask cancels an executing task and its daemon directly by task ID.
// Unlike update_task_status (which requires knowing the target column), this is a
// simple "stop this task" action.
func (h *NexusWebSocketHandler) handleCancelTask(
	ctx context.Context,
	userID string,
	msg NexusClientMessage,
	writeChan chan NexusServerMessage,
) {
	if msg.TaskID == "" {
		writeChan <- NexusServerMessage{Type: "error", Data: map[string]string{"message": "missing task_id"}}
		return
	}

	taskOID, err := primitive.ObjectIDFromHex(msg.TaskID)
	if err != nil {
		writeChan <- NexusServerMessage{Type: "error", Data: map[string]string{"message": "invalid task_id"}}
		return
	}

	task, err := h.taskStore.GetByID(ctx, userID, taskOID)
	if err != nil {
		writeChan <- NexusServerMessage{Type: "error", Data: map[string]string{"message": "task not found"}}
		return
	}

	if task.Status != models.NexusTaskStatusExecuting && task.Status != models.NexusTaskStatusWaitingInput && task.Status != models.NexusTaskStatusPending {
		writeChan <- NexusServerMessage{Type: "error", Data: map[string]string{"message": "task is not active"}}
		return
	}

	// Cancel daemon if one is running
	if task.DaemonID != nil {
		_ = h.daemonPool.Cancel(task.DaemonID.Hex())
		_ = h.daemonPool.UpdateStatus(ctx, userID, *task.DaemonID, "failed", "cancelled by user", 0)
		_ = h.sessionStore.RemoveActiveDaemon(ctx, userID, *task.DaemonID)
	}

	_ = h.taskStore.UpdateStatus(ctx, userID, taskOID, models.NexusTaskStatusCancelled)
	_ = h.sessionStore.RemoveActiveTask(ctx, userID, taskOID)
	_ = h.sessionStore.AddRecentTask(ctx, userID, taskOID)

	h.eventBus.Publish(userID, services.NexusEvent{
		Type: "task_status_changed",
		Data: map[string]interface{}{
			"task_id": msg.TaskID,
			"status":  "cancelled",
		},
	})

	writeChan <- NexusServerMessage{
		Type: "task_cancelled",
		Data: map[string]string{"task_id": msg.TaskID},
	}
}

// handleRetryTask retries a failed/completed task with error context injection
func (h *NexusWebSocketHandler) handleRetryTask(
	ctx context.Context,
	userID string,
	sessionID primitive.ObjectID,
	msg NexusClientMessage,
	writeChan chan NexusServerMessage,
) {
	if msg.TaskID == "" {
		writeChan <- NexusServerMessage{
			Type: "error",
			Data: map[string]string{"message": "missing task_id for retry"},
		}
		return
	}

	taskOID, err := primitive.ObjectIDFromHex(msg.TaskID)
	if err != nil {
		writeChan <- NexusServerMessage{
			Type: "error",
			Data: map[string]string{"message": "invalid task_id"},
		}
		return
	}

	// RetryTask runs dispatch on background context so it survives WS disconnect
	err = h.cortexService.RetryTask(ctx, userID, sessionID, taskOID)
	if err != nil {
		writeChan <- NexusServerMessage{
			Type: "error",
			Data: map[string]string{"message": err.Error()},
		}
		return
	}

	writeChan <- NexusServerMessage{
		Type: "retry_started",
		Data: map[string]string{"original_task_id": msg.TaskID},
	}
}

// handleGetDaemonDetail returns a daemon's full message history
func (h *NexusWebSocketHandler) handleGetDaemonDetail(
	ctx context.Context,
	userID string,
	msg NexusClientMessage,
	writeChan chan NexusServerMessage,
) {
	if msg.DaemonID == "" {
		return
	}

	daemonOID, err := primitive.ObjectIDFromHex(msg.DaemonID)
	if err != nil {
		return
	}

	daemon, err := h.daemonPool.GetByID(ctx, userID, daemonOID)
	if err != nil {
		return
	}

	writeChan <- NexusServerMessage{
		Type: "daemon_detail",
		Data: daemon,
	}
}

// handlePinSkill pins a skill to the session
func (h *NexusWebSocketHandler) handlePinSkill(
	ctx context.Context,
	userID string,
	msg NexusClientMessage,
	writeChan chan NexusServerMessage,
) {
	skillOID, err := primitive.ObjectIDFromHex(msg.SkillID)
	if err != nil {
		return
	}
	_ = h.sessionStore.PinSkill(ctx, userID, skillOID)
	writeChan <- NexusServerMessage{Type: "skill_pinned", Data: map[string]string{"skill_id": msg.SkillID}}
}

// handleUnpinSkill unpins a skill from the session
func (h *NexusWebSocketHandler) handleUnpinSkill(
	ctx context.Context,
	userID string,
	msg NexusClientMessage,
	writeChan chan NexusServerMessage,
) {
	skillOID, err := primitive.ObjectIDFromHex(msg.SkillID)
	if err != nil {
		return
	}
	_ = h.sessionStore.UnpinSkill(ctx, userID, skillOID)
	writeChan <- NexusServerMessage{Type: "skill_unpinned", Data: map[string]string{"skill_id": msg.SkillID}}
}

// handleUpdateTaskStatus processes manual task status changes (from kanban drag-and-drop).
// When moving an executing task away from "executing", the associated daemon is cancelled.
func (h *NexusWebSocketHandler) handleUpdateTaskStatus(
	ctx context.Context,
	userID string,
	msg NexusClientMessage,
	writeChan chan NexusServerMessage,
) {
	if msg.TaskID == "" || msg.Status == "" {
		writeChan <- NexusServerMessage{Type: "error", Data: map[string]string{"message": "task_id and status required"}}
		return
	}

	validStatuses := map[string]bool{
		"draft": true, "pending": true, "executing": true, "completed": true, "failed": true, "cancelled": true,
	}
	if !validStatuses[msg.Status] {
		writeChan <- NexusServerMessage{Type: "error", Data: map[string]string{"message": "invalid status: " + msg.Status}}
		return
	}

	taskID, err := primitive.ObjectIDFromHex(msg.TaskID)
	if err != nil {
		writeChan <- NexusServerMessage{Type: "error", Data: map[string]string{"message": "invalid task_id"}}
		return
	}

	newStatus := models.NexusTaskStatus(msg.Status)

	// If the task was executing and is being moved away, cancel its daemon
	task, err := h.taskStore.GetByID(ctx, userID, taskID)
	if err != nil {
		writeChan <- NexusServerMessage{Type: "error", Data: map[string]string{"message": "task not found"}}
		return
	}

	// Draft → Pending: submit to Cortex for classification + execution
	if task.Status == models.NexusTaskStatusDraft && newStatus == models.NexusTaskStatusPending {
		// Update status to pending first
		err = h.taskStore.UpdateStatus(ctx, userID, taskID, models.NexusTaskStatusPending)
		if err != nil {
			writeChan <- NexusServerMessage{Type: "error", Data: map[string]string{"message": fmt.Sprintf("failed to update status: %v", err)}}
			return
		}

		// Broadcast status change
		h.eventBus.Publish(userID, services.NexusEvent{
			Type: "task_status_changed",
			Data: map[string]interface{}{
				"task_id": msg.TaskID,
				"status":  "pending",
			},
		})

		// Fire Cortex to classify and execute the task
		go h.cortexService.HandleUserMessage(
			context.Background(),
			userID,
			task.SessionID,
			task.Prompt,
			task.ModelID,
			"",                    // auto-classify mode
			"",                    // no template
			"",                    // project already set on task
			task.ID.Hex(),         // reuse existing task
			primitive.NilObjectID, // not a routine
			nil,                   // auto-resolve skills
			nil,                   // no saves
		)
		return
	}

	wasExecuting := task.Status == models.NexusTaskStatusExecuting || task.Status == models.NexusTaskStatusWaitingInput
	movingAway := newStatus != models.NexusTaskStatusExecuting && newStatus != models.NexusTaskStatusWaitingInput

	if wasExecuting && movingAway && task.DaemonID != nil {
		daemonIDStr := task.DaemonID.Hex()
		_ = h.daemonPool.Cancel(daemonIDStr)
		_ = h.daemonPool.UpdateStatus(ctx, userID, *task.DaemonID, "failed", "cancelled by user", 0)
		_ = h.sessionStore.RemoveActiveDaemon(ctx, userID, *task.DaemonID)
		_ = h.sessionStore.RemoveActiveTask(ctx, userID, taskID)

		// If moved to a terminal state, add to recent list
		if newStatus == models.NexusTaskStatusFailed || newStatus == models.NexusTaskStatusCancelled || newStatus == models.NexusTaskStatusCompleted {
			_ = h.sessionStore.AddRecentTask(ctx, userID, taskID)
		}

		h.eventBus.Publish(userID, services.NexusEvent{
			Type: "daemon_cancelled",
			Data: map[string]string{"daemon_id": daemonIDStr},
		})
	}

	err = h.taskStore.UpdateStatus(ctx, userID, taskID, newStatus)
	if err != nil {
		writeChan <- NexusServerMessage{Type: "error", Data: map[string]string{"message": fmt.Sprintf("failed to update status: %v", err)}}
		return
	}

	// Broadcast the update to all connected clients for this user
	h.eventBus.Publish(userID, services.NexusEvent{
		Type: "task_status_changed",
		Data: map[string]interface{}{
			"task_id": msg.TaskID,
			"status":  msg.Status,
		},
	})
}

// handleUpdatePersona processes persona fact updates
func (h *NexusWebSocketHandler) handleUpdatePersona(
	ctx context.Context,
	userID string,
	msg NexusClientMessage,
	writeChan chan NexusServerMessage,
) {
	for _, factUpdate := range msg.Facts {
		switch factUpdate.Action {
		case "create":
			conf := factUpdate.Confidence
			if conf == 0 {
				conf = 1.0
			}
			_ = h.personaStore.Create(ctx, &models.PersonaFact{
				UserID:     userID,
				Category:   factUpdate.Category,
				Content:    factUpdate.Content,
				Confidence: conf,
				Source:     "user_explicit",
			})
		case "update":
			if factUpdate.ID != "" {
				factOID, err := primitive.ObjectIDFromHex(factUpdate.ID)
				if err == nil {
					_ = h.personaStore.Update(ctx, userID, factOID, factUpdate.Content, factUpdate.Category)
				}
			}
		case "delete":
			if factUpdate.ID != "" {
				factOID, err := primitive.ObjectIDFromHex(factUpdate.ID)
				if err == nil {
					_ = h.personaStore.Delete(ctx, userID, factOID)
				}
			}
		}
	}

	facts, err := h.personaStore.GetAll(ctx, userID)
	if err == nil {
		writeChan <- NexusServerMessage{
			Type: "persona_updated",
			Data: map[string]interface{}{"facts": facts},
		}

		// Push updated persona to TUI daemon so local persona.json stays in sync
		if h.mcpBridge != nil {
			h.mcpBridge.PushPersonaSync(userID, facts)
		}
	}
}

// sendSessionState sends the full session state to the client
func (h *NexusWebSocketHandler) sendSessionState(
	ctx context.Context,
	userID string,
	session *models.NexusSession,
	writeChan chan NexusServerMessage,
) {
	activeDaemons, _ := h.daemonPool.GetActiveDaemons(ctx, userID)

	// Fetch tasks from the session's curated lists (active + recent) instead of
	// List() which would surface old tasks when recent ones are deleted
	var taskIDs []primitive.ObjectID
	taskIDs = append(taskIDs, session.ActiveTaskIDs...)
	taskIDs = append(taskIDs, session.RecentTaskIDs...)
	allTasks, _ := h.taskStore.GetByIDs(ctx, userID, taskIDs)

	// Filter out routine-created tasks — they have their own history view
	var recentTasks []models.NexusTask
	for _, t := range allTasks {
		if t.Source != "routine" {
			recentTasks = append(recentTasks, t)
		}
	}

	persona, _ := h.personaStore.GetAll(ctx, userID)
	engrams, _ := h.engramService.GetRecent(ctx, userID, 10)

	// Also fetch TUI-synced brain data (memories + skills) separately
	// so they aren't pushed out of the recent_engrams top-10 by daemon outputs
	brainEngrams, _ := h.engramService.GetBySources(ctx, userID, []string{"tui_sync", "tui_skill"}, 30)

	bridgeConnected := false
	if h.mcpBridge != nil {
		bridgeConnected = h.mcpBridge.IsUserConnected(userID)
	}

	writeChan <- NexusServerMessage{
		Type: "session_state",
		Data: map[string]interface{}{
			"session":          session,
			"active_daemons":   activeDaemons,
			"recent_tasks":     recentTasks,
			"persona":          persona,
			"recent_engrams":   engrams,
			"brain_memories":   brainEngrams,
			"bridge_connected": bridgeConnected,
		},
	}

	// If the TUI bridge is connected but we have no brain memories in MongoDB,
	// request the MCP client to re-sync its local state so data appears on next refresh.
	if bridgeConnected && len(brainEngrams) == 0 && h.mcpBridge != nil {
		h.mcpBridge.RequestSync(userID)
	}
}

// MissedUpdateItem is a structured summary of an event the user missed while disconnected.
type MissedUpdateItem struct {
	ID        string    `json:"id"`
	EventType string    `json:"event_type"`
	TaskID    string    `json:"task_id,omitempty"`
	Goal      string    `json:"goal,omitempty"`
	Summary   string    `json:"summary"`
	Content   string    `json:"content"`
	Timestamp time.Time `json:"timestamp"`
}

// sendMissedUpdates drains any pending events for the user and sends them
// as a structured "missed_updates" message with card-friendly data.
func (h *NexusWebSocketHandler) sendMissedUpdates(
	ctx context.Context,
	userID string,
	writeChan chan NexusServerMessage,
) {
	pending := h.eventBus.DrainPending(userID)
	if len(pending) == 0 {
		return
	}

	var updates []MissedUpdateItem
	seen := make(map[string]bool) // deduplicate by task_id + event_type

	for _, event := range pending {
		item := MissedUpdateItem{
			ID:        uuid.New().String(),
			EventType: event.Type,
			Timestamp: time.Now(),
		}

		// Extract fields from event data
		dataMap, ok := event.Data.(map[string]interface{})
		if !ok {
			continue
		}

		if taskID, ok := dataMap["task_id"]; ok {
			item.TaskID = fmt.Sprintf("%v", taskID)
		}

		switch event.Type {
		case "cortex_response":
			content, _ := dataMap["content"].(string)
			item.Content = content
			if len(content) > 150 {
				item.Summary = content[:147] + "..."
			} else {
				item.Summary = content
			}
			item.Goal = "Cortex Response"

		case "task_completed":
			result, _ := dataMap["result"].(map[string]interface{})
			if result == nil {
				// Result might be a struct that was serialized
				if resultObj, ok := dataMap["result"].(*models.NexusTaskResult); ok && resultObj != nil {
					item.Content = resultObj.Summary
				}
			} else {
				item.Content, _ = result["summary"].(string)
			}
			// Look up the task for the goal
			if item.TaskID != "" {
				if taskOID, err := primitive.ObjectIDFromHex(item.TaskID); err == nil {
					if task, err := h.taskStore.GetByID(ctx, userID, taskOID); err == nil {
						item.Goal = task.Goal
					}
				}
			}
			if item.Goal == "" {
				item.Goal = "Task Completed"
			}
			if len(item.Content) > 150 {
				item.Summary = item.Content[:147] + "..."
			} else {
				item.Summary = item.Content
			}

		case "task_failed":
			errMsg, _ := dataMap["error"].(string)
			item.Content = errMsg
			item.Summary = "Task failed: " + errMsg
			item.Goal = "Task Failed"

		case "daemon_completed":
			result, _ := dataMap["result"].(map[string]interface{})
			if result != nil {
				item.Content, _ = result["summary"].(string)
			}
			role, _ := dataMap["role"].(string)
			item.Goal = role + " completed"
			if len(item.Content) > 150 {
				item.Summary = item.Content[:147] + "..."
			} else {
				item.Summary = item.Content
			}

		default:
			// For other important types, use a generic approach
			if content, ok := dataMap["content"].(string); ok {
				item.Content = content
				if len(content) > 150 {
					item.Summary = content[:147] + "..."
				} else {
					item.Summary = content
				}
			}
			item.Goal = event.Type
		}

		// Deduplicate: prefer cortex_response over task_completed for same task
		dedupeKey := item.TaskID + ":" + item.EventType
		if item.TaskID == "" {
			dedupeKey = item.ID // unique if no task
		}
		if seen[dedupeKey] {
			continue
		}
		seen[dedupeKey] = true

		if item.Summary != "" {
			updates = append(updates, item)
		}
	}

	if len(updates) == 0 {
		return
	}

	log.Printf("[NEXUS-WS] Sending %d missed updates to user %s", len(updates), userID)

	writeChan <- NexusServerMessage{
		Type: "missed_updates",
		Data: map[string]interface{}{
			"updates": updates,
			"count":   len(updates),
		},
	}
}

// handleExplainTask loads a task's full result and publishes it as a cortex_response.
// Triggered when the user clicks a "missed update" card to get the full details.
func (h *NexusWebSocketHandler) handleExplainTask(
	ctx context.Context,
	userID string,
	msg NexusClientMessage,
	writeChan chan NexusServerMessage,
) {
	if msg.TaskID == "" {
		writeChan <- NexusServerMessage{
			Type: "error",
			Data: map[string]string{"message": "missing task_id"},
		}
		return
	}

	taskOID, err := primitive.ObjectIDFromHex(msg.TaskID)
	if err != nil {
		writeChan <- NexusServerMessage{
			Type: "error",
			Data: map[string]string{"message": "invalid task_id"},
		}
		return
	}

	task, err := h.taskStore.GetByID(ctx, userID, taskOID)
	if err != nil {
		writeChan <- NexusServerMessage{
			Type: "error",
			Data: map[string]string{"message": "task not found"},
		}
		return
	}

	var content string
	if task.Result != nil {
		content = task.Result.Summary
	} else if task.Error != "" {
		content = fmt.Sprintf("This task failed: %s", task.Error)
	} else {
		content = fmt.Sprintf("This task (%s) is still in progress: %s", task.Goal, string(task.Status))
	}

	// Publish as a cortex_response so it appears in conversation
	h.eventBus.Publish(userID, services.NexusEvent{
		Type: "cortex_response",
		Data: map[string]interface{}{
			"content": content,
			"task_id": task.ID,
		},
	})
}
