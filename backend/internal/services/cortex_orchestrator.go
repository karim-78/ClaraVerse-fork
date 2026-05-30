package services

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"strings"
	"sync"
	"time"
	"unicode"
	"unicode/utf8"

	"claraverse/internal/models"

	"go.mongodb.org/mongo-driver/bson/primitive"
)

// NexusUpdateChan is the type for sending updates to the WebSocket handler
type NexusUpdateChan chan<- NexusEvent

// NexusEvent represents an event sent from Cortex to subscribers via the EventBus
type NexusEvent struct {
	Type string      `json:"type"`
	Data interface{} `json:"data"`
}

// ClassificationResult is the structured output from Cortex's classification LLM call
type ClassificationResult struct {
	Mode    string       `json:"mode"` // "quick", "daemon", "multi_daemon"
	Reply   string       `json:"reply,omitempty"`
	Daemons []DaemonPlan `json:"daemons,omitempty"`
}

// DaemonPlan describes a single daemon to deploy
type DaemonPlan struct {
	Index        int      `json:"index"`
	Role         string   `json:"role"`
	RoleLabel    string   `json:"role_label"`
	Persona      string   `json:"persona"`
	TaskSummary  string   `json:"task_summary"`
	ToolsNeeded  []string `json:"tools_needed"`
	DependsOn    []int    `json:"depends_on"`
	TemplateSlug string   `json:"template_slug,omitempty"`
}

// HandleUserMessage is the main entry point for Nexus — classifies and dispatches.
// This method BLOCKS until the task completes (daemons finish + synthesis done).
// All events are published to the EventBus — callers don't need a direct channel.
// The ctx controls the execution lifetime (use context.Background() for WS calls
// so daemons survive disconnection).
// followUpTaskID: when non-empty, reuses the existing task instead of creating a new one
// (used for follow-up messages on a completed/failed task).
// routineID: when non-NilObjectID, tags the task as a routine execution and skips session/project tracking.
func (s *CortexService) HandleUserMessage(
	ctx context.Context,
	userID string,
	sessionID primitive.ObjectID,
	message string,
	modelID string,
	modeOverride string, // "daemon", "multi_daemon", or "" for auto-classify
	templateID string,   // direct template selection — skips classification entirely
	projectID string,    // assign task to a project (empty = inbox)
	followUpTaskID string, // reuse existing task for follow-ups (empty = new task)
	routineID primitive.ObjectID, // non-NilObjectID = routine execution (skips kanban tracking)
	skillIDs []string,   // explicit skill IDs from frontend (nil = auto-resolve from session/routing)
	saveIDs []string,    // saved items to attach as reference context
) {
	isRoutine := routineID != primitive.NilObjectID
	// Add a ceiling timeout so we never run forever
	execCtx, cancel := context.WithTimeout(ctx, 10*time.Minute)
	defer cancel()

	// 1. Ensure session exists
	session, err := s.sessionStore.GetOrCreate(execCtx, userID)
	if err != nil {
		s.publish(userID, "error", map[string]string{"message": "failed to get session"})
		return
	}

	// Update activity
	_ = s.sessionStore.UpdateActivity(execCtx, userID)

	// 2. Create or reuse a task
	var pendingTask *models.NexusTask
	if followUpTaskID != "" {
		// Follow-up: reuse the existing task instead of creating a new kanban card
		if taskOID, err := primitive.ObjectIDFromHex(followUpTaskID); err == nil {
			if existing, err := s.taskStore.GetByID(execCtx, userID, taskOID); err == nil && existing != nil {
				pendingTask = existing
				pendingTask.Status = models.NexusTaskStatusExecuting
				pendingTask.Mode = "pending_classification"
				_ = s.taskStore.UpdateModeAndStatus(execCtx, userID, pendingTask.ID, "pending_classification", models.NexusTaskStatusExecuting, "")
				// Inherit model from original task if follow-up didn't specify one
				if modelID == "" && pendingTask.ModelID != "" {
					modelID = pendingTask.ModelID
				}
				// Inherit project from original task if follow-up didn't specify one
				if projectID == "" && pendingTask.ProjectID != nil {
					projectID = pendingTask.ProjectID.Hex()
				}
				// Move back to active tasks
				_ = s.sessionStore.RemoveRecentTask(execCtx, userID, pendingTask.ID)
				_ = s.sessionStore.AddActiveTask(execCtx, userID, pendingTask.ID)
				s.publish(userID, "task_updated", pendingTask)
			}
		}
	}
	if pendingTask == nil {
		// New task — create a pending card immediately so it appears on the kanban
		pendingTask = &models.NexusTask{
			SessionID: session.ID,
			UserID:    userID,
			Prompt:    message,
			Goal:      message,
			Mode:      "pending_classification",
			Status:    models.NexusTaskStatusPending,
			ModelID:   modelID,
		}
		if isRoutine {
			pendingTask.Source = "routine"
			pendingTask.RoutineID = &routineID
		}
		if projectID != "" {
			oid, err := primitive.ObjectIDFromHex(projectID)
			if err == nil {
				pendingTask.ProjectID = &oid
			}
		}
		// Fallback: assign to user's first project so tasks are never orphaned
		// (skip for routine tasks — they don't belong to any project)
		if !isRoutine && pendingTask.ProjectID == nil && s.projectStore != nil {
			if projects, err := s.projectStore.List(execCtx, userID); err == nil && len(projects) > 0 {
				pendingTask.ProjectID = &projects[0].ID
				projectID = projects[0].ID.Hex()
			}
		}
		_ = s.taskStore.Create(execCtx, pendingTask)
		if !isRoutine {
			_ = s.sessionStore.AddActiveTask(execCtx, userID, pendingTask.ID)
		}
		s.publish(userID, "task_created", pendingTask)
	}

	// 3. Resolve project system instruction
	var projectInstruction string
	if projectID != "" && s.projectStore != nil {
		if oid, err := primitive.ObjectIDFromHex(projectID); err == nil {
			if proj, err := s.projectStore.GetByID(execCtx, userID, oid); err == nil && proj != nil {
				projectInstruction = proj.SystemInstruction
			}
		}
	}

	// 3a. Inject attached saves as reference context
	if len(saveIDs) > 0 && s.saveStore != nil {
		if len(saveIDs) > 10 {
			saveIDs = saveIDs[:10]
		}
		var saveOIDs []primitive.ObjectID
		for _, sid := range saveIDs {
			if oid, err := primitive.ObjectIDFromHex(sid); err == nil {
				saveOIDs = append(saveOIDs, oid)
			}
		}
		saves, err := s.saveStore.GetByIDs(execCtx, userID, saveOIDs)
		if err == nil && len(saves) > 0 {
			var refDocs string
			for _, save := range saves {
				content := save.Content
				if len(content) > 2000 {
					content = content[:1997] + "..."
				}
				refDocs += fmt.Sprintf("### %s\n%s\n\n", save.Title, content)
			}
			message = fmt.Sprintf("## Reference Documents\n\n%s---\n\n%s", refDocs, message)
		}
	}

	// 3b. Resolve skill IDs: explicit from message > pinned from session > auto-routed
	// Cap incoming skill IDs to prevent unbounded MongoDB lookups
	if len(skillIDs) > 10 {
		skillIDs = skillIDs[:10]
	}
	var resolvedSkillIDs []primitive.ObjectID
	// Explicit skill IDs from the frontend
	for _, sid := range skillIDs {
		if oid, err := primitive.ObjectIDFromHex(sid); err == nil {
			resolvedSkillIDs = append(resolvedSkillIDs, oid)
		}
	}
	// Pinned session skills (if no explicit skills provided)
	if len(resolvedSkillIDs) == 0 && session != nil && len(session.PinnedSkillIDs) > 0 {
		resolvedSkillIDs = append(resolvedSkillIDs, session.PinnedSkillIDs...)
	}
	// Auto-route: if still empty, try keyword matching
	if len(resolvedSkillIDs) == 0 && s.skillService != nil {
		if matched, err := s.skillService.RouteMessage(execCtx, userID, message); err == nil && matched != nil {
			resolvedSkillIDs = append(resolvedSkillIDs, matched.ID)
		}
	}
	// Deduplicate
	if len(resolvedSkillIDs) > 1 {
		seen := make(map[primitive.ObjectID]bool)
		deduped := resolvedSkillIDs[:0]
		for _, id := range resolvedSkillIDs {
			if !seen[id] {
				seen[id] = true
				deduped = append(deduped, id)
			}
		}
		resolvedSkillIDs = deduped
	}

	// 4. Build Cortex context
	activeDaemons, _ := s.daemonPool.GetActiveDaemons(execCtx, userID)
	recentMessages := s.buildConversationHistory(execCtx, userID, session.ID, message, pendingTask.ProjectID)

	systemPrompt, err := s.contextBuilder.BuildCortexSystemPrompt(execCtx, userID, recentMessages, activeDaemons, projectInstruction)
	if err != nil {
		log.Printf("[CORTEX] Failed to build system prompt for user %s: %v", userID, err)
		systemPrompt = "You are Cortex, Dobby's AI orchestrator."
	}

	// 4. Classify the request
	// Priority: templateID (direct template) > modeOverride (force mode) > auto-classify
	var classification *ClassificationResult
	if templateID != "" && s.templateStore != nil {
		// Direct template selection — skip classification entirely
		tmplOID, _ := primitive.ObjectIDFromHex(templateID)
		tmpl, err := s.templateStore.GetByID(execCtx, userID, tmplOID)
		if err != nil || tmpl == nil {
			log.Printf("[CORTEX] Template %s not found for user %s, falling back to auto-classify", templateID, userID)
		} else {
			log.Printf("[CORTEX] Direct template dispatch: %s (%s) — skipping classification", tmpl.Name, tmpl.Slug)
			classification = &ClassificationResult{
				Mode: "daemon",
				Daemons: []DaemonPlan{{
					Index:        0,
					Role:         tmpl.Role,
					RoleLabel:    tmpl.RoleLabel,
					TemplateSlug: tmpl.Slug,
					TaskSummary:  message,
					ToolsNeeded:  tmpl.DefaultTools,
				}},
			}
			s.publish(userID, "cortex_thinking", map[string]string{"content": "Deploying " + tmpl.Name + "..."})
		}
	}
	if classification == nil && (modeOverride == "daemon" || modeOverride == "multi_daemon") {
		log.Printf("[CORTEX] Using user-selected mode override: %s", modeOverride)
		classification = &ClassificationResult{Mode: modeOverride}
		s.publish(userID, "cortex_thinking", map[string]string{"content": "Dispatching " + modeOverride + "..."})
	} else if classification == nil {
		s.publish(userID, "cortex_thinking", map[string]string{"content": "Analyzing your request..."})

		var err error
		classification, err = s.classifyRequest(execCtx, userID, modelID, message, systemPrompt, recentMessages, activeDaemons)
		if err != nil {
			log.Printf("[CORTEX] Classification failed for user %s: %v", userID, err)
			classification = &ClassificationResult{Mode: "quick"}
		}
	}

	s.publish(userID, "cortex_classified", map[string]interface{}{
		"mode":            classification.Mode,
		"daemons_planned": classification.Daemons,
	})

	// 5. Dispatch based on mode — pass the pre-created task so handlers reuse it
	switch classification.Mode {
	case "quick":
		s.handleQuickMode(execCtx, userID, session.ID, modelID, message, systemPrompt, recentMessages, pendingTask)
	case "status":
		s.handleStatusMode(execCtx, userID, session.ID, modelID, message, systemPrompt, activeDaemons, pendingTask)
	case "daemon":
		s.handleDaemonMode(execCtx, userID, session.ID, modelID, message, classification, pendingTask, projectInstruction, resolvedSkillIDs)
	case "multi_daemon":
		s.handleMultiDaemonMode(execCtx, userID, session.ID, modelID, message, classification, pendingTask, projectInstruction, resolvedSkillIDs)
	default:
		s.handleQuickMode(execCtx, userID, session.ID, modelID, message, systemPrompt, recentMessages, pendingTask)
	}
}

// classifyRequest uses an LLM call to classify the user's request.
// Uses a STANDALONE classification prompt — not combined with the full Cortex context
// to avoid confusing the LLM into answering the question instead of classifying it.
func (s *CortexService) classifyRequest(
	ctx context.Context,
	userID string,
	modelID string,
	message string,
	systemPrompt string,
	recentMessages []map[string]interface{},
	activeDaemons []models.Daemon,
) (*ClassificationResult, error) {
	classificationPrompt := s.contextBuilder.BuildClassificationPrompt(ctx, userID, activeDaemons)

	messages := []map[string]interface{}{
		{"role": "system", "content": classificationPrompt},
	}
	messages = append(messages, recentMessages...)

	response, err := s.chatService.ChatCompletionSync(ctx, userID, modelID, messages, nil)
	if err != nil {
		return nil, fmt.Errorf("classification LLM call failed: %w", err)
	}

	log.Printf("[CORTEX] Classification raw response: %s", truncateString(response, 500))

	result := &ClassificationResult{}

	jsonStr := extractJSONFromLLM(response)
	if jsonStr == "" {
		log.Printf("[CORTEX] Classification: no JSON found, falling back to quick mode")
		return &ClassificationResult{Mode: "quick", Reply: response}, nil
	}

	if err := json.Unmarshal([]byte(jsonStr), result); err != nil {
		log.Printf("[CORTEX] Classification: JSON parse error: %v, falling back to quick", err)
		return &ClassificationResult{Mode: "quick", Reply: response}, nil
	}

	result.Mode = strings.ToLower(strings.TrimSpace(result.Mode))

	for i := range result.Daemons {
		if result.Daemons[i].Index == 0 && i > 0 {
			result.Daemons[i].Index = i
		}
		if result.Daemons[i].RoleLabel == "" {
			result.Daemons[i].RoleLabel = titleCase(result.Daemons[i].Role) + " Daemon"
		}
	}

	if len(result.Daemons) == 1 {
		result.Mode = "daemon"
	}

	log.Printf("[CORTEX] Classification result: mode=%s, daemons=%d", result.Mode, len(result.Daemons))
	for i, d := range result.Daemons {
		log.Printf("[CORTEX]   Daemon %d: %s (%s) — %s, depends_on=%v", i, d.RoleLabel, d.Role, d.TaskSummary, d.DependsOn)
	}

	return result, nil
}

// handleQuickMode handles simple queries with a direct LLM response
func (s *CortexService) handleQuickMode(
	ctx context.Context,
	userID string,
	sessionID primitive.ObjectID,
	modelID string,
	message string,
	systemPrompt string,
	recentMessages []map[string]interface{},
	task *models.NexusTask,
) {
	// Update the pre-created pending task to quick/executing
	task.Mode = "quick"
	task.Status = models.NexusTaskStatusExecuting
	_ = s.taskStore.UpdateModeAndStatus(ctx, userID, task.ID, "quick", models.NexusTaskStatusExecuting, "")
	s.publish(userID, "task_updated", task)

	allTools, _ := s.toolSelector.SelectToolsForDaemon(ctx, userID, "", nil, message)

	messages := []map[string]interface{}{
		{"role": "system", "content": systemPrompt},
	}
	messages = append(messages, recentMessages...)

	response, err := s.chatService.ChatCompletionWithTools(
		ctx, userID, "", modelID, messages, allTools, 10,
	)
	if err != nil {
		_ = s.taskStore.SetError(ctx, userID, task.ID, err.Error())
		if task.Source != "routine" {
			_ = s.sessionStore.RemoveActiveTask(ctx, userID, task.ID)
		}
		_ = s.sessionStore.IncrementStats(ctx, userID, false)
		s.publish(userID, "task_failed", map[string]interface{}{
			"task_id": task.ID,
			"error":   err.Error(),
		})
		return
	}

	result := &models.NexusTaskResult{Summary: response}
	_ = s.taskStore.SetResult(ctx, userID, task.ID, result)
	if task.Source != "routine" {
		_ = s.sessionStore.RemoveActiveTask(ctx, userID, task.ID)
		_ = s.sessionStore.AddRecentTask(ctx, userID, task.ID)
	}
	_ = s.sessionStore.IncrementStats(ctx, userID, true)

	_ = s.engramService.Write(ctx, &models.EngramEntry{
		SessionID: sessionID,
		UserID:    userID,
		Type:      "task_result",
		Key:       fmt.Sprintf("quick_%s", task.ID.Hex()),
		Value:     response,
		Summary:   truncateString(response, 200),
		Source:    "cortex",
	})

	s.publish(userID, "cortex_response", map[string]interface{}{
		"content": response,
		"task_id": task.ID,
	})
	s.publish(userID, "task_completed", map[string]interface{}{
		"task_id": task.ID,
		"result":  &NexusEventTaskResult{Summary: result.Summary},
	})
}

// handleDaemonMode deploys a single daemon for a focused task
func (s *CortexService) handleDaemonMode(
	ctx context.Context,
	userID string,
	sessionID primitive.ObjectID,
	modelID string,
	message string,
	classification *ClassificationResult,
	task *models.NexusTask,
	projectInstruction string,
	skillIDs []primitive.ObjectID,
) {
	if len(classification.Daemons) == 0 {
		s.publish(userID, "error", map[string]string{"message": "no daemon plan in classification"})
		return
	}

	plan := classification.Daemons[0]

	// Update the pre-created pending task to daemon/executing
	task.Mode = "daemon"
	task.Status = models.NexusTaskStatusExecuting
	task.Goal = plan.TaskSummary
	_ = s.taskStore.UpdateModeAndStatus(ctx, userID, task.ID, "daemon", models.NexusTaskStatusExecuting, plan.TaskSummary)
	s.publish(userID, "task_updated", task)

	if !s.acquireDaemonSlot(userID) {
		_ = s.taskStore.SetError(ctx, userID, task.ID, "maximum concurrent daemons reached")
		s.publish(userID, "error", map[string]string{"message": "maximum concurrent daemons reached (5)"})
		return
	}

	// Resolve daemon template if the LLM selected one
	var resolvedTemplate *models.DaemonTemplate
	if plan.TemplateSlug != "" && s.templateStore != nil {
		if tmpl, err := s.templateStore.GetBySlug(ctx, userID, plan.TemplateSlug); err == nil {
			resolvedTemplate = tmpl
			log.Printf("[CORTEX] Using daemon template: %s (%s)", tmpl.Name, tmpl.Slug)
		}
	}

	daemon := &models.Daemon{
		ID:            primitive.NewObjectID(),
		SessionID:     sessionID,
		UserID:        userID,
		TaskID:        task.ID,
		Role:          plan.Role,
		RoleLabel:     plan.RoleLabel,
		TemplateSlug:  plan.TemplateSlug,
		Persona:       plan.Persona,
		AssignedTools: plan.ToolsNeeded,
		PlanIndex:     0,
		Status:        models.DaemonStatusIdle,
		CurrentAction: plan.TaskSummary,
		MaxIterations: 25,
		MaxRetries:    3,
		ModelID:       modelID,
		CreatedAt:     time.Now(),
	}

	// Apply template overrides — template provides defaults, LLM plan keeps task_summary
	if resolvedTemplate != nil {
		daemon.Role = resolvedTemplate.Role
		daemon.RoleLabel = resolvedTemplate.RoleLabel
		daemon.Persona = resolvedTemplate.BuildSystemPromptSection()
		if len(resolvedTemplate.DefaultTools) > 0 {
			daemon.AssignedTools = resolvedTemplate.DefaultTools
		}
		if resolvedTemplate.MaxIterations > 0 {
			daemon.MaxIterations = resolvedTemplate.MaxIterations
		}
		if resolvedTemplate.MaxRetries > 0 {
			daemon.MaxRetries = resolvedTemplate.MaxRetries
		}
	}

	// Merge skill-required tools into daemon's assigned tools
	if len(skillIDs) > 0 {
		_, skillTools := s.contextBuilder.BuildSkillsSection(ctx, skillIDs)
		if len(skillTools) > 0 {
			toolSet := make(map[string]bool)
			for _, t := range daemon.AssignedTools {
				toolSet[t] = true
			}
			for _, t := range skillTools {
				if !toolSet[t] {
					daemon.AssignedTools = append(daemon.AssignedTools, t)
				}
			}
		}
		daemon.AssignedSkillIDs = skillIDs
	}

	_ = s.daemonPool.Create(ctx, daemon)
	_ = s.taskStore.SetDaemonID(ctx, userID, task.ID, daemon.ID)
	_ = s.sessionStore.AddActiveDaemon(ctx, userID, daemon.ID)

	s.publish(userID, "daemon_deployed", map[string]interface{}{
		"daemon_id":    daemon.ID,
		"task_id":      task.ID,
		"role":         daemon.Role,
		"role_label":   daemon.RoleLabel,
		"task_summary": plan.TaskSummary,
	})

	updateChan := make(chan DaemonUpdate, 50)

	runner := NewDaemonRunner(DaemonRunnerConfig{
		Daemon:          daemon,
		UserID:          userID,
		PlanIndex:       0,
		UpdateChan:      updateChan,
		ChatService:     s.chatService,
		ProviderService: s.providerService,
		ToolRegistry:    s.toolRegistry,
		ToolService:     s.toolService,
		MCPBridge:       s.mcpBridge,
		EngramService:   s.engramService,
		DaemonStore:     s.daemonPool,
		ContextBuilder:  s.contextBuilder,
		ToolSelector:    s.toolSelector,
		OriginalMessage:    message,
		ProjectInstruction: projectInstruction,
		SkillIDs:           skillIDs,
	})

	s.daemonPool.RegisterRunner(daemon.ID.Hex(), runner.Cancel)

	go func() {
		defer s.releaseDaemonSlot(userID)
		defer s.daemonPool.UnregisterRunner(daemon.ID.Hex())
		defer close(updateChan)
		runner.Execute(ctx)
	}()

	// Forward daemon updates to EventBus — blocks until daemon finishes
	s.forwardDaemonUpdates(ctx, userID, sessionID, modelID, task.ID, updateChan, message, 0, task.ProjectID, task.Source == "routine")
}

// handleMultiDaemonMode deploys multiple daemons with dependency coordination
func (s *CortexService) handleMultiDaemonMode(
	ctx context.Context,
	userID string,
	sessionID primitive.ObjectID,
	modelID string,
	message string,
	classification *ClassificationResult,
	parentTask *models.NexusTask,
	projectInstruction string,
	skillIDs []primitive.ObjectID,
) {
	if len(classification.Daemons) == 0 {
		s.publish(userID, "error", map[string]string{"message": "no daemon plans"})
		return
	}

	// Update the pre-created pending task to multi_daemon/executing
	parentTask.Mode = "multi_daemon"
	parentTask.Status = models.NexusTaskStatusExecuting
	_ = s.taskStore.UpdateModeAndStatus(ctx, userID, parentTask.ID, "multi_daemon", models.NexusTaskStatusExecuting, "")
	s.publish(userID, "task_updated", parentTask)

	daemons := make(map[int]*models.Daemon)
	for _, plan := range classification.Daemons {
		subTask := &models.NexusTask{
			SessionID:    sessionID,
			UserID:       userID,
			ParentTaskID: &parentTask.ID,
			ProjectID:    parentTask.ProjectID,
			Prompt:       plan.TaskSummary,
			Goal:         plan.TaskSummary,
			Mode:         "daemon",
			Status:       models.NexusTaskStatusPending,
			Source:       "decomposition",
			ModelID:      modelID,
		}
		_ = s.taskStore.Create(ctx, subTask)
		_ = s.taskStore.AddSubTaskID(ctx, userID, parentTask.ID, subTask.ID)

		daemon := &models.Daemon{
			ID:            primitive.NewObjectID(),
			SessionID:     sessionID,
			UserID:        userID,
			TaskID:        subTask.ID,
			Role:          plan.Role,
			RoleLabel:     plan.RoleLabel,
			TemplateSlug:  plan.TemplateSlug,
			Persona:       plan.Persona,
			AssignedTools: plan.ToolsNeeded,
			PlanIndex:     plan.Index,
			DependsOn:     plan.DependsOn,
			Status:        models.DaemonStatusIdle,
			CurrentAction: plan.TaskSummary,
			MaxIterations: 25,
			MaxRetries:    3,
			ModelID:       modelID,
			CreatedAt:     time.Now(),
		}

		// Apply template overrides if LLM selected a template
		if plan.TemplateSlug != "" && s.templateStore != nil {
			if tmpl, err := s.templateStore.GetBySlug(ctx, userID, plan.TemplateSlug); err == nil {
				daemon.Role = tmpl.Role
				daemon.RoleLabel = tmpl.RoleLabel
				daemon.Persona = tmpl.BuildSystemPromptSection()
				if len(tmpl.DefaultTools) > 0 {
					daemon.AssignedTools = tmpl.DefaultTools
				}
				if tmpl.MaxIterations > 0 {
					daemon.MaxIterations = tmpl.MaxIterations
				}
				if tmpl.MaxRetries > 0 {
					daemon.MaxRetries = tmpl.MaxRetries
				}
			}
		}

		// Merge skill-required tools into daemon's assigned tools (same as single-daemon path)
		if len(skillIDs) > 0 {
			_, skillTools := s.contextBuilder.BuildSkillsSection(ctx, skillIDs)
			if len(skillTools) > 0 {
				toolSet := make(map[string]bool)
				for _, t := range daemon.AssignedTools {
					toolSet[t] = true
				}
				for _, t := range skillTools {
					if !toolSet[t] {
						daemon.AssignedTools = append(daemon.AssignedTools, t)
					}
				}
			}
			daemon.AssignedSkillIDs = skillIDs
		}

		_ = s.daemonPool.Create(ctx, daemon)
		_ = s.taskStore.SetDaemonID(ctx, userID, subTask.ID, daemon.ID)
		daemons[plan.Index] = daemon
	}

	s.orchestrateMultiDaemon(ctx, userID, sessionID, parentTask.ID, modelID, message, classification.Daemons, daemons, projectInstruction, parentTask.Source == "routine", skillIDs, nil)
}

// orchestrateMultiDaemon manages the dependency-coordinated execution of multiple daemons.
//
// Durability: when nexusOrchStore is set this:
//   - Initializes a per-orchestration document at start (upsert).
//   - Runs a heartbeat goroutine that pings the store every 10s.
//   - Checkpoints each completed daemon's summary so a crashed backend
//     can resume without re-running finished work.
//   - Marks the orchestration completed (or failed/interrupted) at the end.
//
// resumeCompleted is non-nil on resume paths — pre-populates the completed
// map and causes the launch loop to skip daemons whose work is already
// recorded. Side-effects (engram writes, template stats, learnings) only
// fire for daemons we actually re-launch, so resume is idempotent.
func (s *CortexService) orchestrateMultiDaemon(
	ctx context.Context,
	userID string,
	sessionID primitive.ObjectID,
	parentTaskID primitive.ObjectID,
	modelID string,
	originalMessage string,
	plans []DaemonPlan,
	daemons map[int]*models.Daemon,
	projectInstruction string,
	isRoutine bool,
	skillIDs []primitive.ObjectID,
	resumeCompleted map[int]*DaemonResult,
) {
	// === Durability: init state + heartbeat ====================================
	if s.nexusOrchStore != nil {
		daemonIDs := make(map[string]primitive.ObjectID, len(daemons))
		for idx, d := range daemons {
			daemonIDs[fmt.Sprintf("%d", idx)] = d.ID
		}
		state := &NexusOrchestrationState{
			SessionID:          sessionID,
			UserID:             userID,
			ParentTaskID:       parentTaskID,
			ModelID:            modelID,
			OriginalMessage:    originalMessage,
			ProjectInstruction: projectInstruction,
			IsRoutine:          isRoutine,
			Plans:              plans,
			DaemonIDs:          daemonIDs,
			SkillIDs:           skillIDs,
		}
		if err := s.nexusOrchStore.Init(ctx, state); err != nil {
			// Non-fatal — we lose crash-resume for this run but proceed normally.
			log.Printf("[NEXUS-DURABILITY] state Init failed for task %s (non-fatal, run continues): %v",
				parentTaskID.Hex(), err)
		}

		hbCtx, hbCancel := context.WithCancel(ctx)
		defer hbCancel()
		go s.runOrchestrationHeartbeat(hbCtx, sessionID, parentTaskID)
	}

	completed := make(map[int]*DaemonResult)
	if resumeCompleted != nil {
		for k, v := range resumeCompleted {
			completed[k] = v
		}
		log.Printf("[NEXUS-DURABILITY] Resuming task %s with %d daemon(s) already done",
			parentTaskID.Hex(), len(completed))
	}
	pending := make(map[int]DaemonPlan)
	var running int
	var mu sync.Mutex

	updateChan := make(chan DaemonUpdate, 100)
	// WaitGroup tracks all launched daemon goroutines so we can close updateChan
	// when the last one finishes (preventing the range loop from hanging forever).
	var wg sync.WaitGroup

	for _, plan := range plans {
		// Skip daemons already completed in a previous (interrupted) run.
		if _, done := completed[plan.Index]; done {
			continue
		}
		// Deps may already be met on resume — promote into the "ready" tier.
		depsMet := len(plan.DependsOn) == 0 || allDepsMet(plan.DependsOn, completed)
		if depsMet {
			if !s.acquireDaemonSlot(userID) {
				s.publish(userID, "error", map[string]string{
					"message": fmt.Sprintf("cannot deploy daemon %s: at capacity", plan.RoleLabel),
				})
				continue
			}

			daemon := daemons[plan.Index]
			if daemon == nil {
				log.Printf("[NEXUS-DURABILITY] daemon for plan index %d missing — skipping", plan.Index)
				s.releaseDaemonSlot(userID)
				continue
			}
			_ = s.sessionStore.AddActiveDaemon(ctx, userID, daemon.ID)

			var depResults map[string]string
			if len(plan.DependsOn) > 0 {
				depResults = collectResults(plan.DependsOn, completed, plans)
			}

			s.publish(userID, "daemon_deployed", map[string]interface{}{
				"daemon_id":    daemon.ID,
				"task_id":      parentTaskID,
				"role":         daemon.Role,
				"role_label":   daemon.RoleLabel,
				"task_summary": plan.TaskSummary,
			})

			wg.Add(1)
			s.launchDaemon(ctx, userID, daemon, plan.Index, depResults, originalMessage, updateChan, projectInstruction, skillIDs, &wg)
			mu.Lock()
			running++
			mu.Unlock()
		} else {
			pending[plan.Index] = plan
		}
	}

	// Edge case: a resume where every remaining daemon was already done →
	// nothing launched, so go straight to synthesis.
	if running == 0 && len(pending) == 0 {
		close(updateChan)
		s.finishOrchestration(ctx, userID, parentTaskID, modelID, completed, plans, isRoutine, NexusOrchStatusCompleted)
		return
	}

	// Close updateChan when all daemon goroutines finish so the range loop terminates.
	go func() {
		wg.Wait()
		close(updateChan)
	}()

	for update := range updateChan {
		s.forwardSingleUpdate(userID, update)

		if update.Type == "completed" {
			mu.Lock()
			completed[update.Index] = update.Result
			running--

			_ = s.engramService.Write(ctx, &models.EngramEntry{
				SessionID: sessionID,
				UserID:    userID,
				Type:      "daemon_output",
				Key:       fmt.Sprintf("daemon_%d_%s", update.Index, update.Role),
				Value:     update.Result.Summary,
				Summary:   truncateString(update.Result.Summary, 200),
				Source:    fmt.Sprintf("daemon_%d", update.Index),
			})

			daemon := daemons[update.Index]
			_ = s.sessionStore.RemoveActiveDaemon(ctx, userID, daemon.ID)
			_ = s.taskStore.SetResult(ctx, userID, daemon.TaskID, &models.NexusTaskResult{
				Summary: update.Result.Summary,
			})

			// Durability checkpoint — survives crash so resume skips this daemon.
			if s.nexusOrchStore != nil {
				rec := CompletedDaemonRecord{
					Index:     update.Index,
					DaemonID:  daemon.ID.Hex(),
					Role:      daemon.Role,
					RoleLabel: daemon.RoleLabel,
					Summary:   update.Result.Summary,
				}
				if err := s.nexusOrchStore.CheckpointDaemon(ctx, sessionID, parentTaskID, rec); err != nil {
					log.Printf("[NEXUS-DURABILITY] checkpoint failed for daemon %d (non-fatal): %v",
						update.Index, err)
				}
			}

			// Track template stats + extract learnings
			s.recordTemplateStats(ctx, userID, daemon.ID, true)
			go s.extractTemplateLearnings(context.Background(), userID, daemon.ModelID, daemon.ID)

			for idx, plan := range pending {
				if allDepsMet(plan.DependsOn, completed) {
					depResults := collectResults(plan.DependsOn, completed, plans)
					delete(pending, idx)

					if s.acquireDaemonSlot(userID) {
						pendingDaemon := daemons[idx]
						_ = s.sessionStore.AddActiveDaemon(ctx, userID, pendingDaemon.ID)

						s.publish(userID, "daemon_deployed", map[string]interface{}{
							"daemon_id":    pendingDaemon.ID,
							"task_id":      parentTaskID,
							"role":         pendingDaemon.Role,
							"role_label":   pendingDaemon.RoleLabel,
							"task_summary": plan.TaskSummary,
						})

						wg.Add(1)
						s.launchDaemon(ctx, userID, pendingDaemon, idx, depResults, originalMessage, updateChan, projectInstruction, skillIDs, &wg)
						running++
					}
				}
			}
			mu.Unlock()
		}

		if update.Type == "failed" {
			mu.Lock()
			running--
			daemon := daemons[update.Index]
			_ = s.sessionStore.RemoveActiveDaemon(ctx, userID, daemon.ID)
			_ = s.taskStore.SetError(ctx, userID, daemon.TaskID, update.Error)

			// Track template stats + extract learnings from failure
			s.recordTemplateStats(ctx, userID, daemon.ID, false)
			go s.extractTemplateLearnings(context.Background(), userID, daemon.ModelID, daemon.ID)

			s.failDependentChain(ctx, userID, update.Index, pending, daemons)
			mu.Unlock()
		}

		mu.Lock()
		allDone := running == 0 && len(pending) == 0
		mu.Unlock()

		if allDone {
			break
		}
	}

	// Decide terminal status: anything completed → completed; otherwise failed.
	status := NexusOrchStatusCompleted
	if len(completed) == 0 {
		status = NexusOrchStatusFailed
	}
	s.finishOrchestration(ctx, userID, parentTaskID, modelID, completed, plans, isRoutine, status)
}

// runOrchestrationHeartbeat pings the state store every NexusOrchHeartbeatInterval
// so the orphan scanner doesn't mistake a long-running orchestration for a crashed one.
func (s *CortexService) runOrchestrationHeartbeat(
	ctx context.Context,
	sessionID, parentTaskID primitive.ObjectID,
) {
	if s.nexusOrchStore == nil {
		return
	}
	tick := time.NewTicker(NexusOrchHeartbeatInterval)
	defer tick.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-tick.C:
			// Use background context so a finishing parent ctx doesn't
			// race the final heartbeat. The store write is cheap.
			_ = s.nexusOrchStore.Heartbeat(context.Background(), sessionID, parentTaskID)
		}
	}
}

// finishOrchestration runs synthesis and then writes the terminal status to
// the durability store, triggering the 14-day TTL prune.
func (s *CortexService) finishOrchestration(
	ctx context.Context,
	userID string,
	parentTaskID primitive.ObjectID,
	modelID string,
	completed map[int]*DaemonResult,
	plans []DaemonPlan,
	isRoutine bool,
	status NexusOrchestrationStatus,
) {
	s.synthesizeResults(ctx, userID, parentTaskID, modelID, completed, plans, isRoutine)
	if s.nexusOrchStore != nil {
		// Find the session id for the mark-completed write. Cheapest way is to
		// pull it from any daemon doc, but synthesizeResults already wrote the
		// terminal task state, so we look it up by parentTaskID.
		if task, err := s.taskStore.GetByID(context.Background(), userID, parentTaskID); err == nil && task != nil {
			if err := s.nexusOrchStore.MarkCompleted(context.Background(), task.SessionID, parentTaskID, status); err != nil {
				log.Printf("[NEXUS-DURABILITY] MarkCompleted failed for task %s (non-fatal): %v",
					parentTaskID.Hex(), err)
			}
		}
	}
}

// ResumeOrchestration re-enters orchestrateMultiDaemon for an interrupted
// multi-daemon run. Called from main.go's startup orphan scan.
//
// Resume semantics:
//   - DaemonIDs map identifies the daemon docs from the original run.
//   - CompletedDaemons map skips re-running anything that finished before
//     the crash. We rely on per-daemon recordTemplateStats + engram writes
//     having fired at completion time, so resume does not re-fire them.
//   - In-flight daemons (started but not checkpointed) get re-launched.
//     This is safe for our typical daemons (LLM + read-mostly tools) and
//     matches the workflow engine's idempotency posture.
func (s *CortexService) ResumeOrchestration(ctx context.Context, state NexusOrchestrationState) {
	defer func() {
		if r := recover(); r != nil {
			log.Printf("⚠️ [NEXUS-RESUME] resume panicked for task %s: %v", state.ParentTaskID.Hex(), r)
		}
	}()
	if s.nexusOrchStore == nil {
		log.Printf("[NEXUS-RESUME] cannot resume task %s — orch store unavailable", state.ParentTaskID.Hex())
		return
	}

	// Reload daemon docs by ID.
	daemons := make(map[int]*models.Daemon, len(state.DaemonIDs))
	for idxStr, did := range state.DaemonIDs {
		var idx int
		if _, err := fmt.Sscanf(idxStr, "%d", &idx); err != nil {
			log.Printf("[NEXUS-RESUME] bad daemon index key %q: %v", idxStr, err)
			continue
		}
		d, err := s.daemonPool.GetByID(ctx, state.UserID, did)
		if err != nil || d == nil {
			log.Printf("[NEXUS-RESUME] daemon %s (idx=%d) missing from pool, skipping", did.Hex(), idx)
			continue
		}
		daemons[idx] = d
	}

	// Build pre-completed map from state checkpoints.
	resumeCompleted := make(map[int]*DaemonResult, len(state.CompletedDaemons))
	for _, rec := range state.CompletedDaemons {
		r := rec // capture
		resumeCompleted[r.Index] = &DaemonResult{Summary: r.Summary}
	}

	log.Printf("[NEXUS-RESUME] Resuming session=%s task=%s — %d plan(s), %d daemon(s) loaded, %d already completed",
		state.SessionID.Hex(), state.ParentTaskID.Hex(),
		len(state.Plans), len(daemons), len(resumeCompleted))

	s.orchestrateMultiDaemon(
		ctx,
		state.UserID,
		state.SessionID,
		state.ParentTaskID,
		state.ModelID,
		state.OriginalMessage,
		state.Plans,
		daemons,
		state.ProjectInstruction,
		state.IsRoutine,
		state.SkillIDs,
		resumeCompleted,
	)
}

// launchDaemon starts a daemon runner in a goroutine.
// wg is optional — when non-nil, wg.Done() is called when the goroutine exits.
func (s *CortexService) launchDaemon(
	ctx context.Context,
	userID string,
	daemon *models.Daemon,
	planIndex int,
	depResults map[string]string,
	originalMessage string,
	updateChan chan DaemonUpdate,
	projectInstruction string,
	skillIDs []primitive.ObjectID,
	wg *sync.WaitGroup,
) {
	runner := NewDaemonRunner(DaemonRunnerConfig{
		Daemon:          daemon,
		UserID:          userID,
		PlanIndex:       planIndex,
		UpdateChan:      updateChan,
		ChatService:     s.chatService,
		ProviderService: s.providerService,
		ToolRegistry:    s.toolRegistry,
		ToolService:     s.toolService,
		MCPBridge:       s.mcpBridge,
		EngramService:   s.engramService,
		DaemonStore:     s.daemonPool,
		ContextBuilder:  s.contextBuilder,
		ToolSelector:    s.toolSelector,
		ArtifactStore:   s.artifactStore,
		DepResults:         depResults,
		OriginalMessage:    originalMessage,
		ProjectInstruction: projectInstruction,
		SkillIDs:           skillIDs,
	})

	s.daemonPool.RegisterRunner(daemon.ID.Hex(), runner.Cancel)

	go func() {
		if wg != nil {
			defer wg.Done()
		}
		defer s.releaseDaemonSlot(userID)
		defer s.daemonPool.UnregisterRunner(daemon.ID.Hex())
		runner.Execute(ctx)
	}()
}

// forwardDaemonUpdates reads from daemon updateChan and publishes to EventBus (single daemon mode).
// retryCount tracks how many auto-retries have been done for this task (max 2).
func (s *CortexService) forwardDaemonUpdates(
	ctx context.Context,
	userID string,
	sessionID primitive.ObjectID,
	modelID string,
	taskID primitive.ObjectID,
	updateChan <-chan DaemonUpdate,
	originalMessage string,
	retryCount int,
	projectID *primitive.ObjectID,
	isRoutine bool,
) {
	for update := range updateChan {
		s.forwardSingleUpdate(userID, update)

		if update.Type == "completed" {
			_ = s.sessionStore.RemoveActiveDaemon(ctx, userID, update.DaemonID)
			if !isRoutine {
				_ = s.sessionStore.RemoveActiveTask(ctx, userID, taskID)
				_ = s.sessionStore.AddRecentTask(ctx, userID, taskID)
			}
			_ = s.sessionStore.IncrementStats(ctx, userID, true)

			if update.Result != nil {
				_ = s.taskStore.SetResult(ctx, userID, taskID, &models.NexusTaskResult{
					Summary: update.Result.Summary,
				})
			}

			// Track template stats (success) + extract learnings
			s.recordTemplateStats(ctx, userID, update.DaemonID, true)
			go s.extractTemplateLearnings(context.Background(), userID, modelID, update.DaemonID)

			var eventResult *NexusEventTaskResult
			if update.Result != nil {
				eventResult = &NexusEventTaskResult{Summary: update.Result.Summary}
			}
			s.publish(userID, "task_completed", map[string]interface{}{
				"task_id": taskID,
				"result":  eventResult,
			})

			// Verify quality + publish proactive response (or auto-retry)
			if update.Result != nil {
				go s.verifyAndRespond(context.Background(), userID, sessionID, modelID, taskID, originalMessage, update.Result.Summary, retryCount, projectID)
			}
		}

		if update.Type == "failed" {
			_ = s.sessionStore.RemoveActiveDaemon(ctx, userID, update.DaemonID)
			if !isRoutine {
				_ = s.sessionStore.RemoveActiveTask(ctx, userID, taskID)
			}
			_ = s.sessionStore.IncrementStats(ctx, userID, false)
			_ = s.taskStore.SetError(ctx, userID, taskID, update.Error)

			// Track template stats (failure) + extract learnings
			s.recordTemplateStats(ctx, userID, update.DaemonID, false)
			go s.extractTemplateLearnings(context.Background(), userID, modelID, update.DaemonID)

			s.publish(userID, "task_failed", map[string]interface{}{
				"task_id": taskID,
				"error":   update.Error,
			})
		}
	}
}

// forwardSingleUpdate converts a DaemonUpdate to a NexusEvent and publishes it
func (s *CortexService) forwardSingleUpdate(userID string, update DaemonUpdate) {
	var eventType string
	switch update.Type {
	case "status":
		eventType = "daemon_status"
	case "tool_call":
		eventType = "daemon_tool_call"
	case "tool_result":
		eventType = "daemon_tool_result"
	case "thinking":
		eventType = "daemon_thinking"
	case "completed":
		eventType = "daemon_completed"
	case "failed":
		eventType = "daemon_failed"
	case "question":
		eventType = "daemon_question"
	default:
		eventType = "daemon_status"
	}

	s.publish(userID, eventType, update)
}

// synthesizeResults aggregates completed daemon results into a final response
func (s *CortexService) synthesizeResults(
	ctx context.Context,
	userID string,
	parentTaskID primitive.ObjectID,
	modelID string,
	completed map[int]*DaemonResult,
	plans []DaemonPlan,
	isRoutine bool,
) {
	if len(completed) == 0 {
		_ = s.taskStore.SetError(ctx, userID, parentTaskID, "no daemons completed")
		if !isRoutine {
			_ = s.sessionStore.RemoveActiveTask(ctx, userID, parentTaskID)
		}
		_ = s.sessionStore.IncrementStats(ctx, userID, false)
		s.publish(userID, "task_failed", map[string]interface{}{
			"task_id": parentTaskID,
			"error":   "no daemons completed",
		})
		return
	}

	var sb strings.Builder
	sb.WriteString("The following daemons have completed their work. Synthesize a cohesive final response:\n\n")
	for _, plan := range plans {
		if result, ok := completed[plan.Index]; ok {
			sb.WriteString(fmt.Sprintf("### %s (%s)\n%s\n\n", plan.RoleLabel, plan.Role, result.Summary))
		}
	}
	sb.WriteString("Provide a unified, well-structured summary combining all daemon results.")

	messages := []map[string]interface{}{
		{"role": "system", "content": "You are Cortex, an AI orchestrator. Synthesize the results from multiple specialized daemons into a cohesive response."},
		{"role": "user", "content": sb.String()},
	}

	synthesis, err := s.chatService.ChatCompletionSync(ctx, userID, modelID, messages, nil)
	if err != nil {
		var fallback strings.Builder
		for _, plan := range plans {
			if result, ok := completed[plan.Index]; ok {
				fallback.WriteString(fmt.Sprintf("**%s:** %s\n\n", plan.RoleLabel, result.Summary))
			}
		}
		synthesis = fallback.String()
	}

	taskResult := &models.NexusTaskResult{Summary: synthesis}
	_ = s.taskStore.SetResult(ctx, userID, parentTaskID, taskResult)
	if !isRoutine {
		_ = s.sessionStore.RemoveActiveTask(ctx, userID, parentTaskID)
		_ = s.sessionStore.AddRecentTask(ctx, userID, parentTaskID)
	}
	_ = s.sessionStore.IncrementStats(ctx, userID, true)

	s.publish(userID, "cortex_response", map[string]interface{}{
		"content": synthesis,
		"task_id": parentTaskID,
	})
	s.publish(userID, "task_completed", map[string]interface{}{
		"task_id": parentTaskID,
		"result":  &NexusEventTaskResult{Summary: taskResult.Summary},
	})
}

// failDependentChain cascade-fails all daemons that depend on a failed daemon
func (s *CortexService) failDependentChain(
	ctx context.Context,
	userID string,
	failedIndex int,
	pending map[int]DaemonPlan,
	daemons map[int]*models.Daemon,
) {
	failedSet := map[int]bool{failedIndex: true}
	changed := true

	for changed {
		changed = false
		for idx, plan := range pending {
			if failedSet[idx] {
				continue
			}
			for _, dep := range plan.DependsOn {
				if failedSet[dep] {
					failedSet[idx] = true
					changed = true
					break
				}
			}
		}
	}

	for idx := range failedSet {
		if idx == failedIndex {
			continue
		}
		if daemon, ok := daemons[idx]; ok {
			_ = s.taskStore.SetError(ctx, userID, daemon.TaskID, "dependency failed")
			delete(pending, idx)
			s.publish(userID, "daemon_failed", map[string]interface{}{
				"daemon_id": daemon.ID,
				"error":     "dependency failed",
				"can_retry": false,
			})
		}
	}
}

// allDepsMet checks if all dependency indices have completed results
func allDepsMet(deps []int, completed map[int]*DaemonResult) bool {
	for _, dep := range deps {
		if _, ok := completed[dep]; !ok {
			return false
		}
	}
	return true
}

// collectResults gathers predecessor results for injection into a dependent daemon's context
func collectResults(deps []int, completed map[int]*DaemonResult, plans []DaemonPlan) map[string]string {
	results := make(map[string]string)
	for _, dep := range deps {
		if result, ok := completed[dep]; ok {
			label := fmt.Sprintf("Daemon %d", dep)
			for _, p := range plans {
				if p.Index == dep {
					label = p.RoleLabel
					break
				}
			}
			results[label] = result.Summary
		}
	}
	return results
}

// buildConversationHistory loads recent task turns from the session and appends the current message.
// When projectID is non-nil, only tasks from that project are included (strict isolation).
// Falls back to a single user message on error.
func (s *CortexService) buildConversationHistory(
	ctx context.Context,
	userID string,
	sessionID primitive.ObjectID,
	currentMessage string,
	projectID *primitive.ObjectID,
) []map[string]interface{} {
	recentTasks, err := s.taskStore.GetRecentBySessionAndProject(ctx, userID, sessionID, projectID, 5)
	if err != nil || len(recentTasks) == 0 {
		return []map[string]interface{}{
			{"role": "user", "content": currentMessage},
		}
	}

	var messages []map[string]interface{}

	// Tasks are returned newest-first; reverse to chronological order
	for i := len(recentTasks) - 1; i >= 0; i-- {
		task := recentTasks[i]
		messages = append(messages, map[string]interface{}{
			"role":    "user",
			"content": task.Prompt,
		})
		if task.Result != nil && task.Result.Summary != "" {
			messages = append(messages, map[string]interface{}{
				"role":    "assistant",
				"content": task.Result.Summary,
			})
		} else if task.Error != "" {
			messages = append(messages, map[string]interface{}{
				"role":    "assistant",
				"content": fmt.Sprintf("[Task failed: %s]", task.Error),
			})
		}
	}

	// Append current message at the end
	messages = append(messages, map[string]interface{}{
		"role":    "user",
		"content": currentMessage,
	})

	return messages
}

// handleStatusMode responds to status/continuation questions.
// When daemons are active: reports their live progress (no LLM needed).
// When no daemons are active: uses LLM with conversation history to give a contextual answer
// about previous results (e.g. "what did you find?", "continue", etc.)
func (s *CortexService) handleStatusMode(
	ctx context.Context,
	userID string,
	sessionID primitive.ObjectID,
	modelID string,
	message string,
	systemPrompt string,
	activeDaemons []models.Daemon,
	task *models.NexusTask,
) {
	// Finalize the pre-created pending task as a quick status check
	task.Mode = "quick"
	task.Status = models.NexusTaskStatusExecuting
	_ = s.taskStore.UpdateModeAndStatus(ctx, userID, task.ID, "quick", models.NexusTaskStatusExecuting, "")
	s.publish(userID, "task_updated", task)

	defer func() {
		// Mark completed when status response is done
		_ = s.sessionStore.RemoveActiveTask(ctx, userID, task.ID)
		_ = s.sessionStore.AddRecentTask(ctx, userID, task.ID)
	}()
	// If daemons are actively running, use LLM to give a smart, contextual status update
	if len(activeDaemons) > 0 {
		var daemonCtx strings.Builder
		daemonCtx.WriteString("Currently active tasks:\n")
		for i, d := range activeDaemons {
			task, _ := s.taskStore.GetByID(ctx, userID, d.TaskID)
			goal := d.RoleLabel
			if task != nil && task.Goal != "" {
				goal = task.Goal
			}
			daemonCtx.WriteString(fmt.Sprintf(
				"\n[Task %d] %s (%s)\n  Goal: %s\n  Currently: %s\n  Progress: %.0f%%\n",
				i+1, d.RoleLabel, d.Role, goal, d.CurrentAction, d.Progress*100,
			))
		}

		messages := []map[string]interface{}{
			{"role": "system", "content": systemPrompt + "\n\n" + daemonCtx.String() +
				"\n\nThe user is asking about their running tasks. " +
				"If they ask about a specific task, identify which one and give a focused update. " +
				"If they ask generally, give a brief overview of all. " +
				"Be conversational — respond like a real person giving a status update."},
			{"role": "user", "content": message},
		}

		response, err := s.chatService.ChatCompletionSync(ctx, userID, modelID, messages, nil)
		if err != nil {
			// Fallback to static dump
			var sb strings.Builder
			sb.WriteString("Here's the current status:\n\n")
			for _, d := range activeDaemons {
				sb.WriteString(fmt.Sprintf("- **%s**: %s — %.0f%%\n", d.RoleLabel, d.CurrentAction, d.Progress*100))
			}
			response = sb.String()
		}

		s.publish(userID, "cortex_response", map[string]interface{}{
			"content": response,
			"task_id": task.ID,
		})
		result := &models.NexusTaskResult{Summary: response}
		_ = s.taskStore.SetResult(ctx, userID, task.ID, result)
		s.publish(userID, "task_completed", map[string]interface{}{"task_id": task.ID, "result": &NexusEventTaskResult{Summary: response}})
		return
	}

	// No active daemons — user is asking about previous results ("what did you find?", "continue", etc.)
	// Use LLM with full conversation history so it can reference past task outputs
	recentMessages := s.buildConversationHistory(ctx, userID, sessionID, message, task.ProjectID)

	messages := []map[string]interface{}{
		{"role": "system", "content": systemPrompt + "\n\nThe user is asking about the status or results of previous work. Use the conversation history above to give a complete, contextual answer. If a previous task produced results, share them in full — do not truncate or summarize unless the user explicitly asks for a summary."},
	}
	messages = append(messages, recentMessages...)

	response, err := s.chatService.ChatCompletionSync(ctx, userID, modelID, messages, nil)
	if err != nil {
		// Fallback: show raw last task result
		recentTasks, taskErr := s.taskStore.GetRecentBySession(ctx, userID, sessionID, 1)
		if taskErr == nil && len(recentTasks) > 0 && recentTasks[0].Result != nil {
			response = recentTasks[0].Result.Summary
		} else {
			response = "I couldn't retrieve the previous results. Please try rephrasing your question."
		}
	}

	s.publish(userID, "cortex_response", map[string]interface{}{
		"content": response,
		"task_id": task.ID,
	})
	result := &models.NexusTaskResult{Summary: response}
	_ = s.taskStore.SetResult(ctx, userID, task.ID, result)
	s.publish(userID, "task_completed", map[string]interface{}{"task_id": task.ID, "result": &NexusEventTaskResult{Summary: response}})
}

// maxAutoRetries is the maximum number of times Cortex will auto-retry a daemon
// when verification finds the output inadequate.
const maxAutoRetries = 2

// verifyAndRespond evaluates daemon output quality and publishes a proactive response.
// This is the single LLM call that both verifies AND generates the user-facing message.
// If adequate: publishes a natural proactive cortex_response (like a real person reporting back).
// If inadequate + retries left: notifies user and auto-retries with a new daemon.
// If inadequate + no retries: publishes what we have with a note about gaps.
// On any error: falls back to publishing the daemon output directly.
func (s *CortexService) verifyAndRespond(
	ctx context.Context,
	userID string,
	sessionID primitive.ObjectID,
	modelID string,
	taskID primitive.ObjectID,
	originalMessage string,
	daemonSummary string,
	retryCount int,
	projectID *primitive.ObjectID,
) {
	defer func() {
		if r := recover(); r != nil {
			log.Printf("[CORTEX] verifyAndRespond panicked: %v", r)
			// Fallback: at least publish something
			s.publish(userID, "cortex_response", map[string]interface{}{
				"content": daemonSummary,
				"task_id": taskID,
			})
		}
	}()

	prompt := fmt.Sprintf(`You are Cortex, an AI assistant reporting back to the user after a daemon completed a task.

User's original request: %s

Daemon output: %s

Do TWO things:
1. EVALUATE: Does this daemon output adequately address what the user asked for? Be strict — partial or vague results count as inadequate.
2. RESPOND: Write a brief, natural proactive message (2-4 sentences) presenting the results to the user. Sound like a helpful colleague, not a robot. Highlight key findings. End with an offer to help further.

Respond with ONLY a JSON object:
{"adequate": true/false, "note": "what's missing if inadequate", "proactive_message": "your natural response to the user"}`,
		originalMessage, truncateString(daemonSummary, 4000))

	messages := []map[string]interface{}{
		{"role": "system", "content": "You are Cortex. Respond with ONLY valid JSON. No markdown code blocks."},
		{"role": "user", "content": prompt},
	}

	response, err := s.chatService.ChatCompletionSync(ctx, userID, modelID, messages, nil)
	if err != nil {
		log.Printf("[CORTEX] verifyAndRespond LLM failed, publishing raw output: %v", err)
		s.publish(userID, "cortex_response", map[string]interface{}{
			"content": daemonSummary,
			"task_id": taskID,
		})
		return
	}

	jsonStr := extractJSONFromLLM(response)
	if jsonStr == "" {
		// LLM didn't return JSON — publish daemon output directly
		s.publish(userID, "cortex_response", map[string]interface{}{
			"content": daemonSummary,
			"task_id": taskID,
		})
		return
	}

	var result struct {
		Adequate        bool   `json:"adequate"`
		Note            string `json:"note"`
		ProactiveMessage string `json:"proactive_message"`
	}
	if err := json.Unmarshal([]byte(jsonStr), &result); err != nil {
		s.publish(userID, "cortex_response", map[string]interface{}{
			"content": daemonSummary,
			"task_id": taskID,
		})
		return
	}

	log.Printf("[CORTEX] Verification for task %s (attempt %d): adequate=%v, note=%s",
		taskID.Hex(), retryCount+1, result.Adequate, result.Note)

	if result.Adequate {
		// Good output — publish the natural proactive message
		msg := result.ProactiveMessage
		if msg == "" {
			msg = daemonSummary
		}
		s.publish(userID, "cortex_response", map[string]interface{}{
			"content": msg,
			"task_id": taskID,
		})
		return
	}

	// Output is inadequate — auto-retry if we have retries left
	if retryCount < maxAutoRetries {
		log.Printf("[CORTEX] Auto-retrying task %s (attempt %d/%d): %s",
			taskID.Hex(), retryCount+1, maxAutoRetries, result.Note)
		s.publish(userID, "cortex_thinking", map[string]string{
			"content": fmt.Sprintf("That didn't fully cover what you asked for — %s. Let me try again with a different approach.", result.Note),
		})
		s.retryDaemonTask(ctx, userID, sessionID, modelID, originalMessage, daemonSummary, result.Note, retryCount+1, projectID)
	} else {
		// Max retries exhausted — publish best effort with a note
		msg := result.ProactiveMessage
		if msg == "" {
			msg = daemonSummary
		}
		s.publish(userID, "cortex_response", map[string]interface{}{
			"content": fmt.Sprintf("%s\n\n---\n*I made %d attempts at this task but couldn't fully satisfy your request. %s Feel free to rephrase or break it into smaller tasks.*",
				msg, maxAutoRetries+1, result.Note),
			"task_id": taskID,
		})
	}
}

// retryDaemonTask deploys a new daemon with enhanced context from the previous attempt's output and feedback.
func (s *CortexService) retryDaemonTask(
	ctx context.Context,
	userID string,
	sessionID primitive.ObjectID,
	modelID string,
	originalMessage string,
	previousOutput string,
	feedback string,
	retryCount int,
	projectID *primitive.ObjectID,
) {
	// Build an enhanced message that includes what went wrong
	enhancedMessage := fmt.Sprintf(`RETRY — Previous attempt was inadequate. Try harder and be more thorough.

Original request: %s

Previous daemon output (DO NOT just repeat this — improve on it):
%s

What was wrong with the previous attempt: %s

Instructions: Address the original request completely. Use different tools or approaches if the previous attempt fell short. Be comprehensive.`,
		originalMessage, truncateString(previousOutput, 3000), feedback)

	// Create a task for the retry
	task := &models.NexusTask{
		SessionID: sessionID,
		UserID:    userID,
		ProjectID: projectID,
		Prompt:    originalMessage,
		Goal:      fmt.Sprintf("[Retry %d] %s", retryCount, originalMessage),
		Mode:      "daemon",
		Status:    models.NexusTaskStatusExecuting,
		ModelID:   modelID,
		Source:    "auto_retry",
	}
	_ = s.taskStore.Create(ctx, task)
	_ = s.sessionStore.AddActiveTask(ctx, userID, task.ID)
	s.publish(userID, "task_created", task)

	if !s.acquireDaemonSlot(userID) {
		_ = s.taskStore.SetError(ctx, userID, task.ID, "maximum concurrent daemons reached for retry")
		return
	}

	daemon := &models.Daemon{
		ID:            primitive.NewObjectID(),
		SessionID:     sessionID,
		UserID:        userID,
		TaskID:        task.ID,
		Role:          "researcher",
		RoleLabel:     "Retry Daemon",
		Persona:       "Thorough investigator who addresses gaps from previous attempts. Uses multiple tools and cross-references findings.",
		AssignedTools: []string{"search"},
		PlanIndex:     0,
		Status:        models.DaemonStatusIdle,
		CurrentAction: fmt.Sprintf("[Retry %d] %s", retryCount, truncateString(originalMessage, 100)),
		MaxIterations: 25,
		MaxRetries:    3,
		ModelID:       modelID,
		CreatedAt:     time.Now(),
	}
	_ = s.daemonPool.Create(ctx, daemon)
	_ = s.taskStore.SetDaemonID(ctx, userID, task.ID, daemon.ID)
	_ = s.sessionStore.AddActiveDaemon(ctx, userID, daemon.ID)

	s.publish(userID, "daemon_deployed", map[string]interface{}{
		"daemon_id":    daemon.ID,
		"task_id":      task.ID,
		"role":         daemon.Role,
		"role_label":   daemon.RoleLabel,
		"task_summary": fmt.Sprintf("Retry: %s", truncateString(originalMessage, 100)),
	})

	updateChan := make(chan DaemonUpdate, 50)

	// Resolve project instruction for the retry daemon
	var retryProjectInstruction string
	if projectID != nil && s.projectStore != nil {
		if proj, err := s.projectStore.GetByID(ctx, userID, *projectID); err == nil {
			retryProjectInstruction = proj.SystemInstruction
		}
	}

	runner := NewDaemonRunner(DaemonRunnerConfig{
		Daemon:             daemon,
		UserID:             userID,
		PlanIndex:          0,
		UpdateChan:         updateChan,
		ChatService:        s.chatService,
		ProviderService:    s.providerService,
		ToolRegistry:       s.toolRegistry,
		ToolService:        s.toolService,
		MCPBridge:          s.mcpBridge,
		EngramService:      s.engramService,
		DaemonStore:        s.daemonPool,
		ContextBuilder:     s.contextBuilder,
		ToolSelector:       s.toolSelector,
		OriginalMessage:    enhancedMessage,
		ProjectInstruction: retryProjectInstruction,
	})

	s.daemonPool.RegisterRunner(daemon.ID.Hex(), runner.Cancel)

	go func() {
		defer s.releaseDaemonSlot(userID)
		defer s.daemonPool.UnregisterRunner(daemon.ID.Hex())
		defer close(updateChan)
		runner.Execute(ctx)
	}()

	// Forward and verify again (with incremented retryCount)
	s.forwardDaemonUpdates(ctx, userID, sessionID, modelID, task.ID, updateChan, originalMessage, retryCount, projectID, task.Source == "routine")
}

const maxManualRetries = 3

// RetryTask handles a user-initiated manual retry of a failed/completed task.
// It creates a NEW task linked to the original, injects failure context, and
// dispatches through the normal classification + daemon flow.
func (s *CortexService) RetryTask(
	ctx context.Context,
	userID string,
	sessionID primitive.ObjectID,
	taskID primitive.ObjectID,
) error {
	// 1. Load the original task
	originalTask, err := s.taskStore.GetByID(ctx, userID, taskID)
	if err != nil {
		return fmt.Errorf("task not found: %w", err)
	}

	// 2. If the task is still executing, cancel it first so the retry can proceed
	if originalTask.Status == models.NexusTaskStatusExecuting || originalTask.Status == models.NexusTaskStatusWaitingInput {
		if originalTask.DaemonID != nil {
			_ = s.daemonPool.Cancel(originalTask.DaemonID.Hex())
			_ = s.daemonPool.UpdateStatus(ctx, userID, *originalTask.DaemonID, "failed", "cancelled for retry", 0)
			_ = s.sessionStore.RemoveActiveDaemon(ctx, userID, *originalTask.DaemonID)
		}
		_ = s.taskStore.UpdateStatus(ctx, userID, taskID, models.NexusTaskStatusCancelled)
		_ = s.sessionStore.RemoveActiveTask(ctx, userID, taskID)
		_ = s.sessionStore.AddRecentTask(ctx, userID, taskID)
		originalTask.Status = models.NexusTaskStatusCancelled

		s.publish(userID, "task_status_changed", map[string]interface{}{
			"task_id": taskID.Hex(),
			"status":  "cancelled",
		})

		// Brief pause for goroutine to exit and release semaphore slot
		time.Sleep(200 * time.Millisecond)
	}

	// 3. Validate: only terminal tasks can be retried
	if originalTask.Status != models.NexusTaskStatusFailed &&
		originalTask.Status != models.NexusTaskStatusCompleted &&
		originalTask.Status != models.NexusTaskStatusCancelled {
		return fmt.Errorf("task is not in a terminal state (status: %s)", originalTask.Status)
	}

	// 4. Find root task ID and count existing retries
	rootTaskID, _ := s.taskStore.GetRootTaskID(ctx, userID, taskID)
	retryCount, _ := s.taskStore.CountManualRetries(ctx, userID, rootTaskID)
	if retryCount >= maxManualRetries {
		return fmt.Errorf("maximum retry limit (%d) reached for this task", maxManualRetries)
	}

	// 5. Build enhanced message with error context
	enhancedMessage := s.buildRetryMessage(originalTask)

	// 6. Publish retry started event
	s.publish(userID, "cortex_thinking", map[string]string{
		"content": fmt.Sprintf("Retrying task (attempt %d/%d)...", retryCount+1, maxManualRetries),
	})

	// 7. Dispatch on background context so it survives WS disconnect
	go s.handleRetryDispatch(
		context.Background(),
		userID,
		sessionID,
		originalTask,
		rootTaskID,
		retryCount+1,
		enhancedMessage,
	)

	return nil
}

// handleRetryDispatch creates a retry task and dispatches it through the normal classification flow.
func (s *CortexService) handleRetryDispatch(
	ctx context.Context,
	userID string,
	sessionID primitive.ObjectID,
	originalTask *models.NexusTask,
	rootTaskID primitive.ObjectID,
	retryNumber int,
	enhancedMessage string,
) {
	execCtx, cancel := context.WithTimeout(ctx, 10*time.Minute)
	defer cancel()

	// Use the original task's session if sessionID is nil
	if sessionID == primitive.NilObjectID {
		sessionID = originalTask.SessionID
	}

	// Ensure session exists
	session, err := s.sessionStore.GetOrCreate(execCtx, userID)
	if err != nil {
		s.publish(userID, "error", map[string]string{"message": "failed to get session for retry"})
		return
	}
	if sessionID == primitive.NilObjectID {
		sessionID = session.ID
	}

	// Create retry task with linkage to root
	retryTask := &models.NexusTask{
		SessionID:        sessionID,
		UserID:           userID,
		ProjectID:        originalTask.ProjectID,
		Prompt:           originalTask.Prompt,
		Goal:             fmt.Sprintf("[Retry %d] %s", retryNumber, originalTask.Goal),
		Mode:             "pending_classification",
		Status:           models.NexusTaskStatusPending,
		ModelID:          originalTask.ModelID,
		Source:           "manual_retry",
		RetryOfTaskID:    &rootTaskID,
		ManualRetryCount: retryNumber,
	}
	_ = s.taskStore.Create(execCtx, retryTask)
	_ = s.sessionStore.AddActiveTask(execCtx, userID, retryTask.ID)
	s.publish(userID, "task_created", retryTask)

	// Resolve project instruction from the original task's project
	var projectInstruction string
	if originalTask.ProjectID != nil && s.projectStore != nil {
		if proj, err := s.projectStore.GetByID(execCtx, userID, *originalTask.ProjectID); err == nil {
			projectInstruction = proj.SystemInstruction
		}
	}

	// Build context for classification
	activeDaemons, _ := s.daemonPool.GetActiveDaemons(execCtx, userID)
	recentMessages := s.buildConversationHistory(execCtx, userID, sessionID, enhancedMessage, originalTask.ProjectID)

	systemPrompt, err := s.contextBuilder.BuildCortexSystemPrompt(execCtx, userID, recentMessages, activeDaemons, projectInstruction)
	if err != nil {
		systemPrompt = "You are Cortex, Dobby's AI orchestrator."
	}

	// Classify (may choose a different mode with error context)
	classification, err := s.classifyRequest(execCtx, userID, originalTask.ModelID, enhancedMessage, systemPrompt, recentMessages, activeDaemons)
	if err != nil {
		log.Printf("[CORTEX] Retry classification failed, defaulting to daemon: %v", err)
		classification = &ClassificationResult{Mode: "daemon"}
	}

	s.publish(userID, "cortex_classified", map[string]interface{}{
		"mode":            classification.Mode,
		"daemons_planned": classification.Daemons,
	})

	// Dispatch to the appropriate handler (retries don't carry skill IDs — nil)
	switch classification.Mode {
	case "quick":
		s.handleQuickMode(execCtx, userID, sessionID, originalTask.ModelID, enhancedMessage, systemPrompt, recentMessages, retryTask)
	case "daemon":
		s.handleDaemonMode(execCtx, userID, sessionID, originalTask.ModelID, enhancedMessage, classification, retryTask, projectInstruction, nil)
	case "multi_daemon":
		s.handleMultiDaemonMode(execCtx, userID, sessionID, originalTask.ModelID, enhancedMessage, classification, retryTask, projectInstruction, nil)
	default:
		s.handleDaemonMode(execCtx, userID, sessionID, originalTask.ModelID, enhancedMessage, classification, retryTask, projectInstruction, nil)
	}
}

// buildRetryMessage constructs an enhanced prompt that includes the previous attempt's error context.
func (s *CortexService) buildRetryMessage(task *models.NexusTask) string {
	var sb strings.Builder
	sb.WriteString("RETRY — Previous attempt failed. Try a different approach.\n\n")
	sb.WriteString(fmt.Sprintf("Original request: %s\n\n", task.Prompt))

	if task.Error != "" {
		sb.WriteString(fmt.Sprintf("Previous error: %s\n\n", task.Error))
	}
	if task.Result != nil && task.Result.Summary != "" {
		sb.WriteString(fmt.Sprintf("Previous attempt output (incomplete/inadequate):\n%s\n\n",
			truncateString(task.Result.Summary, 3000)))
	}

	sb.WriteString("Instructions: Address the original request completely. If the previous attempt failed due to a tool error, try different tools or approaches. Be thorough and comprehensive.")
	return sb.String()
}

// extractJSONFromLLM extracts a JSON object from a string that may contain markdown code blocks
func extractJSONFromLLM(s string) string {
	if idx := strings.Index(s, "```json"); idx >= 0 {
		start := idx + 7
		end := strings.Index(s[start:], "```")
		if end >= 0 {
			return strings.TrimSpace(s[start : start+end])
		}
	}
	if idx := strings.Index(s, "```"); idx >= 0 {
		start := idx + 3
		if nlIdx := strings.Index(s[start:], "\n"); nlIdx >= 0 {
			start += nlIdx + 1
		}
		end := strings.Index(s[start:], "```")
		if end >= 0 {
			candidate := strings.TrimSpace(s[start : start+end])
			if strings.HasPrefix(candidate, "{") {
				return candidate
			}
		}
	}

	if idx := strings.Index(s, "{"); idx >= 0 {
		depth := 0
		for i := idx; i < len(s); i++ {
			switch s[i] {
			case '{':
				depth++
			case '}':
				depth--
				if depth == 0 {
					return s[idx : i+1]
				}
			}
		}
	}

	return ""
}

// recordTemplateStats looks up the daemon's role against templates and increments stats.
// This runs inline (fast DB lookup + update), no LLM calls.
func (s *CortexService) recordTemplateStats(ctx context.Context, userID string, daemonID primitive.ObjectID, success bool) {
	if s.templateStore == nil {
		return
	}

	daemon, err := s.daemonPool.GetByID(ctx, userID, daemonID)
	if err != nil || daemon == nil {
		return
	}

	// Try to find the template: prefer explicit slug, fallback to role
	slug := daemon.TemplateSlug
	if slug == "" {
		slug = daemon.Role
	}
	tmpl, err := s.templateStore.GetBySlug(ctx, userID, slug)
	if err != nil || tmpl == nil {
		return
	}

	log.Printf("[CORTEX] Recording template stats: %s (slug=%s) success=%v iterations=%d", tmpl.Name, tmpl.Slug, success, daemon.Iterations)
	_ = s.templateStore.IncrementStats(ctx, tmpl.ID, success, daemon.Iterations)
}

// extractTemplateLearnings runs async after daemon completion.
// It summarizes the daemon's tool usage and asks the LLM to extract reusable learnings
// that get stored on the template for future daemon runs.
func (s *CortexService) extractTemplateLearnings(
	ctx context.Context,
	userID string,
	modelID string,
	daemonID primitive.ObjectID,
) {
	defer func() {
		if r := recover(); r != nil {
			log.Printf("[CORTEX] extractTemplateLearnings panicked: %v", r)
		}
	}()

	if s.templateStore == nil {
		log.Printf("[CORTEX] Learning extraction skipped: no template store")
		return
	}

	daemon, err := s.daemonPool.GetByID(ctx, userID, daemonID)
	if err != nil || daemon == nil {
		log.Printf("[CORTEX] Learning extraction skipped: daemon not found (id=%s, err=%v)", daemonID.Hex(), err)
		return
	}

	// Prefer explicit slug, fallback to role
	slug := daemon.TemplateSlug
	if slug == "" {
		slug = daemon.Role
	}
	tmpl, err := s.templateStore.GetBySlug(ctx, userID, slug)
	if err != nil || tmpl == nil {
		log.Printf("[CORTEX] Learning extraction skipped: template not found for slug=%s (err=%v)", slug, err)
		return
	}

	// Build a concise tool usage summary from daemon messages
	var toolSummary strings.Builder
	toolCount := 0
	for _, msg := range daemon.Messages {
		if msg.ToolCall != nil {
			toolCount++
			truncArgs := msg.ToolCall.Arguments
			if len(truncArgs) > 200 {
				truncArgs = truncArgs[:200] + "..."
			}
			toolSummary.WriteString(fmt.Sprintf("CALL %d: %s(%s)\n", toolCount, msg.ToolCall.Name, truncArgs))
		}
		if msg.ToolResult != nil {
			status := "OK"
			if msg.ToolResult.IsError {
				status = "ERROR"
			}
			truncContent := msg.ToolResult.Content
			if len(truncContent) > 300 {
				truncContent = truncContent[:300] + "..."
			}
			toolSummary.WriteString(fmt.Sprintf("  -> %s: %s\n", status, truncContent))
		}
	}

	// Skip if very few tool calls (not much to learn from)
	if toolCount < 3 {
		log.Printf("[CORTEX] Learning extraction skipped for %s: only %d tool calls (need >= 3)", tmpl.Slug, toolCount)
		return
	}

	log.Printf("[CORTEX] Extracting learnings for template %s from %d tool calls", tmpl.Slug, toolCount)

	// Get existing learnings to avoid duplicates
	existingLearnings := ""
	if len(tmpl.Learnings) > 0 {
		var existing []string
		for _, l := range tmpl.Learnings {
			existing = append(existing, fmt.Sprintf("- [%s] %s (confidence: %.1f)", l.Category, l.Content, l.Confidence))
		}
		existingLearnings = "\n\nEXISTING LEARNINGS (do not repeat these):\n" + strings.Join(existing, "\n")
	}

	// Extract task description from first user message
	taskDesc := ""
	for _, msg := range daemon.Messages {
		if msg.Role == "user" && msg.Content != "" {
			taskDesc = msg.Content
			if len(taskDesc) > 500 {
				taskDesc = taskDesc[:500] + "..."
			}
			break
		}
	}

	prompt := fmt.Sprintf(`Analyze this daemon execution and extract reusable learnings.

DAEMON ROLE: %s (%s)
TASK: %s
ITERATIONS: %d
TOOL CALLS: %d
%s
TOOL EXECUTION LOG:
%s

Extract 1-5 concise, actionable learnings from this execution. Focus on:
- Tool usage patterns that worked well or failed
- Workflow strategies that were effective
- Constraints or gotchas discovered
- Output patterns that should be repeated

Respond with ONLY a JSON array:
[{"key": "short_snake_case_key", "content": "The learning in 1-2 sentences", "category": "tool_usage|workflow|output|constraint", "confidence": 0.5-1.0}]

If nothing useful to learn, respond with: []`,
		daemon.Role, daemon.RoleLabel,
		taskDesc,
		daemon.Iterations,
		toolCount,
		existingLearnings,
		truncateString(toolSummary.String(), 6000))

	messages := []map[string]interface{}{
		{"role": "system", "content": "You are an AI learning extractor. Respond with ONLY valid JSON. No markdown code blocks."},
		{"role": "user", "content": prompt},
	}

	response, err := s.chatService.ChatCompletionSync(ctx, userID, modelID, messages, nil)
	if err != nil {
		log.Printf("[CORTEX] Template learning extraction LLM failed: %v", err)
		return
	}

	jsonStr := extractJSONFromLLM(response)
	if jsonStr == "" || jsonStr == "[]" {
		return
	}

	type learningEntry struct {
		Key        string  `json:"key"`
		Content    string  `json:"content"`
		Category   string  `json:"category"`
		Confidence float64 `json:"confidence"`
	}

	var learnings []learningEntry
	if err := json.Unmarshal([]byte(jsonStr), &learnings); err != nil {
		// LLM sometimes wraps in an object like {"learnings": [...]}
		var wrapper map[string]json.RawMessage
		if wErr := json.Unmarshal([]byte(jsonStr), &wrapper); wErr == nil {
			for _, v := range wrapper {
				if jErr := json.Unmarshal(v, &learnings); jErr == nil && len(learnings) > 0 {
					break
				}
			}
		}
		// LLM sometimes returns a single object instead of an array
		if len(learnings) == 0 {
			var single learningEntry
			if sErr := json.Unmarshal([]byte(jsonStr), &single); sErr == nil && single.Key != "" {
				learnings = []learningEntry{single}
			}
		}
		if len(learnings) == 0 {
			return
		}
	}

	added := 0
	for _, l := range learnings {
		if l.Key == "" || l.Content == "" {
			continue
		}
		if l.Confidence < 0.3 || l.Confidence > 1.0 {
			l.Confidence = 0.5
		}
		validCategories := map[string]bool{"tool_usage": true, "workflow": true, "output": true, "constraint": true}
		if !validCategories[l.Category] {
			l.Category = "workflow"
		}
		err := s.templateStore.AddLearning(ctx, tmpl.ID, models.TemplateLearning{
			Key:        l.Key,
			Content:    l.Content,
			Category:   l.Category,
			Confidence: l.Confidence,
		})
		if err != nil {
			log.Printf("[CORTEX] Failed to add learning '%s': %v", l.Key, err)
		} else {
			added++
		}
	}

	if added > 0 {
		log.Printf("[CORTEX] Extracted %d learnings for template %s (%s)", added, tmpl.Name, tmpl.Slug)
	}
}

// titleCase uppercases the first letter of a string (replaces deprecated strings.Title).
func titleCase(s string) string {
	if s == "" {
		return s
	}
	r, size := utf8.DecodeRuneInString(s)
	return string(unicode.ToUpper(r)) + s[size:]
}
