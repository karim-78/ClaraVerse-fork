package services

import (
	"context"
	"log"
	"sync"

	"claraverse/internal/tools"

	"github.com/google/uuid"
	"go.mongodb.org/mongo-driver/bson/primitive"
)

// CortexService is the central orchestrator for the Nexus multi-agent system.
// It classifies user requests, dispatches daemons, and aggregates results.
// Daemon execution is decoupled from WebSocket connections via the EventBus —
// daemons survive client disconnection and results persist in MongoDB/Engram.
type CortexService struct {
	// Reused services (not modified)
	chatService      *ChatService
	providerService  *ProviderService
	toolRegistry     *tools.Registry
	toolService      *ToolService
	toolPredictorSvc *ToolPredictorService

	// Nexus services
	personaService *PersonaService
	taskStore      *NexusTaskStore
	sessionStore   *NexusSessionStore
	engramService  *EngramService
	daemonPool     *DaemonPool
	contextBuilder *CortexContextBuilder
	toolSelector   *CortexToolSelector

	// MCP bridge for forwarding tool calls to user's desktop client
	mcpBridge *MCPBridgeService

	// Daemon templates for template-aware classification
	templateStore *DaemonTemplateStore

	// Project store for resolving project system instructions
	projectStore *NexusProjectStore

	// Save store for injecting saved items as reference context
	saveStore *NexusSaveStore

	// Skill service for resolving skills attached to daemons
	skillService *SkillService

	// Artifact store powers structured daemon-to-daemon handoff. Optional —
	// when nil the produce/list/read_artifact tools surface "unavailable"
	// errors but Nexus otherwise continues to work via text dep results.
	artifactStore *NexusArtifactStore

	// Orchestration durability store — per multi-daemon-task checkpoint +
	// heartbeat. When nil, orchestrations run as before but do not survive
	// a backend crash. When wired, main.go runs an orphan scan at boot to
	// resume interrupted runs.
	nexusOrchStore *NexusOrchestrationStore

	// RAG searcher — when set, daemons running on project-scoped tasks
	// automatically get search_knowledge injected when the project has
	// indexed files. Nil disables the feature gracefully.
	ragSearcher RAGSearcher

	// EventBus — decouples execution from WS lifecycle
	eventBus *NexusEventBus

	// Per-user daemon semaphores (max 5 concurrent daemons)
	userSemaphores sync.Map // userID → chan struct{} (buffered, cap 5)
}

// maxDaemonsPerUser is the maximum concurrent daemons a single user can have.
// Bumped to 10 (was 5) so parallel multi-daemon orchestrations don't queue
// up Writer/Synthesizer steps from sibling tasks. The orchestrator's
// no-drop fix means cap-exhausted daemons wait instead of disappearing,
// but waiting still feels slow — 10 gives ~2 simultaneous research+write
// workflows of breathing room before anything queues.
const maxDaemonsPerUser = 10

// NewCortexService creates a new Cortex orchestrator
func NewCortexService(
	chatService *ChatService,
	providerService *ProviderService,
	toolRegistry *tools.Registry,
	toolService *ToolService,
	toolPredictorSvc *ToolPredictorService,
	personaService *PersonaService,
	taskStore *NexusTaskStore,
	sessionStore *NexusSessionStore,
	engramService *EngramService,
	daemonPool *DaemonPool,
	eventBus *NexusEventBus,
) *CortexService {
	contextBuilder := NewCortexContextBuilder(
		personaService,
		engramService,
		sessionStore,
		nil, // MemorySelectionService — set via setter
	)

	toolSelector := NewCortexToolSelector(
		toolRegistry,
		toolPredictorSvc,
	)

	return &CortexService{
		chatService:      chatService,
		providerService:  providerService,
		toolRegistry:     toolRegistry,
		toolService:      toolService,
		toolPredictorSvc: toolPredictorSvc,
		personaService:   personaService,
		taskStore:        taskStore,
		sessionStore:     sessionStore,
		engramService:    engramService,
		daemonPool:       daemonPool,
		contextBuilder:   contextBuilder,
		toolSelector:     toolSelector,
		eventBus:         eventBus,
	}
}

// SetMemorySelectionService sets the memory selection service (late dependency injection)
func (s *CortexService) SetMemorySelectionService(svc *MemorySelectionService) {
	s.contextBuilder.memorySelectionSvc = svc
}

// SetRAGSearcher wires the RAG layer so daemons can get search_knowledge
// auto-injected on project-scoped tasks. Optional — when nil, daemons
// run as before with no project-knowledge tool. main.go calls this once.
func (s *CortexService) SetRAGSearcher(r RAGSearcher) {
	s.ragSearcher = r
}

// SetArtifactStore wires the Nexus artifact store. Called by main.go after
// both services exist. When set, daemons get the produce/list/read_artifact
// tools and the system-prompt-time artifact listing.
func (s *CortexService) SetArtifactStore(store *NexusArtifactStore) {
	s.artifactStore = store
}

// SetNexusOrchStore wires the orchestration durability store. Called by
// main.go after construction. With it set, multi-daemon runs checkpoint
// every completed daemon, heartbeat every 10s, and survive crashes
// (orphan scan resumes them at boot).
func (s *CortexService) SetNexusOrchStore(store *NexusOrchestrationStore) {
	s.nexusOrchStore = store
}

// NexusOrchStore returns the orchestration durability store (may be nil).
// Used by main.go's startup orphan scan.
func (s *CortexService) NexusOrchStore() *NexusOrchestrationStore {
	return s.nexusOrchStore
}

// SetToolService sets the tool service on the tool selector (late dependency injection)
func (s *CortexService) SetToolService(svc *ToolService) {
	s.toolSelector.SetToolService(svc)
}

// SetMCPBridge sets the MCP bridge service for routing tool calls to user's desktop client
func (s *CortexService) SetMCPBridge(svc *MCPBridgeService) {
	s.mcpBridge = svc
}

// SetDaemonTemplateStore sets the daemon template store for template-aware classification
func (s *CortexService) SetDaemonTemplateStore(store *DaemonTemplateStore) {
	s.templateStore = store
	s.contextBuilder.templateStore = store
}

// SetProjectStore sets the project store for resolving project system instructions
func (s *CortexService) SetProjectStore(store *NexusProjectStore) {
	s.projectStore = store
}

// SetSaveStore sets the save store for injecting saved items as reference context
func (s *CortexService) SetSaveStore(store *NexusSaveStore) {
	s.saveStore = store
}

// SetSkillService sets the skill service for resolving daemon skills
func (s *CortexService) SetSkillService(svc *SkillService) {
	s.skillService = svc
	s.contextBuilder.skillService = svc
}

// EventBus returns the event bus for external subscribers (WS handler)
func (s *CortexService) EventBus() *NexusEventBus {
	return s.eventBus
}

// publish sends an event to all subscribers for a user via the EventBus
func (s *CortexService) publish(userID, eventType string, data interface{}) {
	s.eventBus.Publish(userID, NexusEvent{Type: eventType, Data: data})
}

// publishError emits a structured user-facing error.
//
// Why structured: the bare `publish("error", {"message": "..."})` pattern
// shipped a single string the frontend had nowhere to disambiguate. Code
// lets the UI choose how to render (toast vs panel vs inline). Hint
// guides the user on what they can actually do about it. TraceID points
// support at the OTel waterfall in the admin trace viewer for the same
// session.
//
// The internalErr is logged but never reaches the user — it can contain
// stack traces, internal IDs, or sensitive details. The user-facing
// message must be self-contained.
//
// Callers should pick a small, stable set of codes (e.g.
// "session_unavailable", "daemon_capacity", "classification_failed") so
// the frontend can localize / route them consistently.
func (s *CortexService) publishError(userID, code, userMessage, hint string, internalErr error) {
	data := map[string]interface{}{
		"code":     code,
		"message":  userMessage,
		"hint":     hint,
		"trace_id": "", // populated when ctx-bound trace is available
	}
	if internalErr != nil {
		log.Printf("[USER-ERR] user=%s code=%s msg=%q internal=%v", userID, code, userMessage, internalErr)
	} else {
		log.Printf("[USER-ERR] user=%s code=%s msg=%q", userID, code, userMessage)
	}
	s.publish(userID, "error", data)
}

// acquireDaemonSlot acquires a daemon slot for the user, returns false if at capacity
func (s *CortexService) acquireDaemonSlot(userID string) bool {
	sem := s.getOrCreateSemaphore(userID)
	select {
	case sem <- struct{}{}:
		return true
	default:
		return false
	}
}

// releaseDaemonSlot releases a daemon slot for the user
func (s *CortexService) releaseDaemonSlot(userID string) {
	sem := s.getOrCreateSemaphore(userID)
	select {
	case <-sem:
	default:
	}
}

// getOrCreateSemaphore returns the per-user daemon semaphore
func (s *CortexService) getOrCreateSemaphore(userID string) chan struct{} {
	if val, ok := s.userSemaphores.Load(userID); ok {
		return val.(chan struct{})
	}
	sem := make(chan struct{}, maxDaemonsPerUser)
	actual, _ := s.userSemaphores.LoadOrStore(userID, sem)
	return actual.(chan struct{})
}

// HandleUserMessageSync is a synchronous wrapper around HandleUserMessage.
// It subscribes to the EventBus, runs HandleUserMessage, and collects the final response.
// Used by RoutineService and ChannelHandler for non-WebSocket integrations.
func (s *CortexService) HandleUserMessageSync(
	ctx context.Context,
	userID string,
	message string,
	modelID string,
) (string, error) {
	subID := "sync-" + uuid.New().String()
	eventCh := s.eventBus.Subscribe(userID, subID, 64)
	defer s.eventBus.Unsubscribe(userID, subID)

	// HandleUserMessage blocks until the task completes
	doneCh := make(chan struct{})
	go func() {
		defer close(doneCh)
		s.HandleUserMessage(ctx, userID, primitive.NilObjectID, message, modelID, "", "", "", "", primitive.NilObjectID, nil, nil)
	}()

	// Collect events, extract final response.
	// Drain eventCh after doneCh fires to avoid missing the final event
	// when both channels are ready simultaneously (Go select is random).
	var response string
	done := false
	for !done {
		select {
		case event, ok := <-eventCh:
			if !ok {
				done = true
				break
			}
			switch event.Type {
			case "cortex_response":
				if data, ok := event.Data.(map[string]interface{}); ok {
					if content, ok := data["content"].(string); ok {
						response = content
					}
				}
			case "task_completed":
				if data, ok := event.Data.(map[string]interface{}); ok {
					if result, ok := data["result"].(*NexusEventTaskResult); ok && result != nil {
						if response == "" {
							response = result.Summary
						}
					}
				}
				done = true
			case "task_failed":
				if response == "" {
					response = "I encountered an error processing your request."
				}
				done = true
			case "error":
				if data, ok := event.Data.(map[string]string); ok {
					if msg, ok := data["message"]; ok {
						log.Printf("[CORTEX-SYNC] Error for user %s: %s", userID, msg)
					}
				}
				if response == "" {
					response = "I encountered an error processing your request."
				}
			}
		case <-doneCh:
			// HandleUserMessage returned — drain remaining events before returning.
			for {
				select {
				case event, ok := <-eventCh:
					if !ok {
						break
					}
					if event.Type == "cortex_response" {
						if data, ok := event.Data.(map[string]interface{}); ok {
							if content, ok := data["content"].(string); ok {
								response = content
							}
						}
					}
					if event.Type == "task_completed" {
						if data, ok := event.Data.(map[string]interface{}); ok {
							if result, ok := data["result"].(*NexusEventTaskResult); ok && result != nil {
								if response == "" {
									response = result.Summary
								}
							}
						}
					}
				default:
					// No more buffered events
					goto drained
				}
			}
		drained:
			done = true
		case <-ctx.Done():
			if response == "" {
				response = "Request timed out."
			}
			return response, ctx.Err()
		}
	}
	if response == "" {
		response = "No response generated."
	}
	return response, nil
}

// HandleRoutineSync is a synchronous wrapper for routine executions.
// Tags the created task with source="routine" and the routine's ObjectID,
// and skips session/project tracking so routine tasks don't appear on kanban boards.
func (s *CortexService) HandleRoutineSync(
	ctx context.Context,
	userID string,
	message string,
	modelID string,
	routineID primitive.ObjectID,
) (string, error) {
	subID := "routine-sync-" + uuid.New().String()
	eventCh := s.eventBus.Subscribe(userID, subID, 64)
	defer s.eventBus.Unsubscribe(userID, subID)

	doneCh := make(chan struct{})
	go func() {
		defer close(doneCh)
		s.HandleUserMessage(ctx, userID, primitive.NilObjectID, message, modelID, "", "", "", "", routineID, nil, nil)
	}()

	var response string
	done := false
	for !done {
		select {
		case event, ok := <-eventCh:
			if !ok {
				done = true
				break
			}
			switch event.Type {
			case "cortex_response":
				if data, ok := event.Data.(map[string]interface{}); ok {
					if content, ok := data["content"].(string); ok {
						response = content
					}
				}
			case "task_completed":
				if data, ok := event.Data.(map[string]interface{}); ok {
					if result, ok := data["result"].(*NexusEventTaskResult); ok && result != nil {
						if response == "" {
							response = result.Summary
						}
					}
				}
				done = true
			case "task_failed":
				if response == "" {
					response = "I encountered an error processing your request."
				}
				done = true
			case "error":
				if data, ok := event.Data.(map[string]string); ok {
					if msg, ok := data["message"]; ok {
						log.Printf("[CORTEX-ROUTINE] Error for user %s: %s", userID, msg)
					}
				}
				if response == "" {
					response = "I encountered an error processing your request."
				}
			}
		case <-doneCh:
			for {
				select {
				case event, ok := <-eventCh:
					if !ok {
						break
					}
					if event.Type == "cortex_response" {
						if data, ok := event.Data.(map[string]interface{}); ok {
							if content, ok := data["content"].(string); ok {
								response = content
							}
						}
					}
					if event.Type == "task_completed" {
						if data, ok := event.Data.(map[string]interface{}); ok {
							if result, ok := data["result"].(*NexusEventTaskResult); ok && result != nil {
								if response == "" {
									response = result.Summary
								}
							}
						}
					}
				default:
					goto routineDrained
				}
			}
		routineDrained:
			done = true
		case <-ctx.Done():
			if response == "" {
				response = "Request timed out."
			}
			return response, ctx.Err()
		}
	}
	if response == "" {
		response = "No response generated."
	}
	return response, nil
}

// NexusEventTaskResult is used to extract task result from event data
type NexusEventTaskResult struct {
	Summary string `json:"summary"`
}
