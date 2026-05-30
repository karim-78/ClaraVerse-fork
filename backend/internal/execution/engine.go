package execution

import (
	"claraverse/internal/models"
	"claraverse/internal/services"
	"context"
	"fmt"
	"log"
	"runtime/debug"
	"sync"
	"time"
)

// trySend attempts a non-blocking send on statusChan. If the buffer is full,
// it drops the update and logs a warning instead of blocking the execution goroutine.
func trySend(statusChan chan<- models.ExecutionUpdate, update models.ExecutionUpdate) {
	select {
	case statusChan <- update:
	default:
		log.Printf("⚠️ [ENGINE] Status channel full, dropping update for block '%s' (status: %s)", update.BlockID, update.Status)
	}
}

// recoverBlock is a deferred function that catches panics in block goroutines,
// preventing a single block crash from taking down the entire server process.
// It marks the block as failed and broadcasts completion so dependent blocks can proceed.
func recoverBlock(
	blockID, blockName string,
	blockStates map[string]*models.BlockState,
	statesMu *sync.RWMutex,
	failedBlocks map[string]bool,
	completedCond *sync.Cond,
	statusChan chan<- models.ExecutionUpdate,
	executionErrors *[]string,
	errorsMu *sync.Mutex,
) {
	r := recover()
	if r == nil {
		return
	}
	stack := string(debug.Stack())
	log.Printf("🔥 [ENGINE] PANIC in block '%s' (%s): %v\n%s", blockName, blockID, r, stack)

	statesMu.Lock()
	blockStates[blockID].Status = string(TransitionBlockStatus(
		BlockStatus(blockStates[blockID].Status), BlockStatusFailed))
	blockStates[blockID].CompletedAt = timePtr(time.Now())
	blockStates[blockID].Error = fmt.Sprintf("internal panic: %v", r)
	statesMu.Unlock()

	trySend(statusChan, models.ExecutionUpdate{
		Type:    "execution_update",
		BlockID: blockID,
		Status:  "failed",
		Error:   fmt.Sprintf("internal panic: %v", r),
	})

	errorsMu.Lock()
	*executionErrors = append(*executionErrors, fmt.Sprintf("%s: panic: %v", blockName, r))
	errorsMu.Unlock()

	completedCond.L.Lock()
	failedBlocks[blockID] = true
	completedCond.Broadcast()
	completedCond.L.Unlock()
}

// WorkflowEngine executes workflows as DAGs with parallel execution.
// Optionally durable: when stateStore is set, every block's output is
// checkpointed to storage after completion, retries return cached results,
// and crashes can be resumed from the last checkpoint.
type WorkflowEngine struct {
	registry     *ExecutorRegistry
	blockChecker *BlockChecker
	stateStore   StateStore // nil = ephemeral (legacy behaviour)
}

// NewWorkflowEngine creates a new workflow engine
func NewWorkflowEngine(registry *ExecutorRegistry) *WorkflowEngine {
	return &WorkflowEngine{registry: registry}
}

// NewWorkflowEngineWithChecker creates a workflow engine with block completion checking
func NewWorkflowEngineWithChecker(registry *ExecutorRegistry, providerService *services.ProviderService) *WorkflowEngine {
	return &WorkflowEngine{
		registry:     registry,
		blockChecker: NewBlockChecker(providerService),
	}
}

// SetBlockChecker allows setting the block checker after creation
func (e *WorkflowEngine) SetBlockChecker(checker *BlockChecker) {
	e.blockChecker = checker
}

// SetStateStore enables durable execution. When set, every block's output
// is checkpointed to storage as it completes, retries within the same
// execution return cached output (idempotency), and crashed executions can
// be resumed via ResumeExecution. Pass nil to opt out (legacy ephemeral mode).
func (e *WorkflowEngine) SetStateStore(s StateStore) {
	e.stateStore = s
}

// ExecutionResult contains the final result of a workflow execution
type ExecutionResult struct {
	Status      string                        `json:"status"` // completed, failed, partial
	Output      map[string]any                `json:"output"`
	BlockStates map[string]*models.BlockState `json:"block_states"`
	Error       string                        `json:"error,omitempty"`
}

// CheckpointFunc is called after each block completes to persist state for crash recovery.
type CheckpointFunc func(blockID, status string, output map[string]any)

// ExecutionOptions contains optional settings for workflow execution
type ExecutionOptions struct {
	// WorkflowGoal is the high-level objective of the workflow (used for block checking)
	WorkflowGoal string
	// CheckerModelID is the model to use for block completion checking
	// If empty, block checking is disabled
	CheckerModelID string
	// EnableBlockChecker enables/disables block completion validation
	EnableBlockChecker bool
	// Checkpoint is called after each block completes to persist state.
	// If nil, no checkpointing is performed.
	Checkpoint CheckpointFunc
	// ExecutionID identifies this run for the durable state store. When set
	// + the engine has a state store wired (SetStateStore), each block's
	// output is checkpointed, retries return cached output, and a crash
	// can be resumed via ResumeExecution. Empty = ephemeral (legacy).
	ExecutionID string
	// UserID is recorded on the durable execution state so orphan detection
	// at startup can report ownership. Optional; safe to leave empty.
	UserID string
	// Resuming hints that this execution was started before and is now
	// being continued after a crash. Engine will skip blocks whose outputs
	// it finds in the state store instead of re-running them.
	Resuming bool
}

// Execute runs a workflow and streams updates via the statusChan
// This is the backwards-compatible version without block checking
func (e *WorkflowEngine) Execute(
	ctx context.Context,
	workflow *models.Workflow,
	input map[string]any,
	statusChan chan<- models.ExecutionUpdate,
) (*ExecutionResult, error) {
	return e.ExecuteWithOptions(ctx, workflow, input, statusChan, nil)
}

// ExecuteWithOptions runs a workflow with optional block completion checking
func (e *WorkflowEngine) ExecuteWithOptions(
	ctx context.Context,
	workflow *models.Workflow,
	input map[string]any,
	statusChan chan<- models.ExecutionUpdate,
	options *ExecutionOptions,
) (*ExecutionResult, error) {
	log.Printf("🚀 [ENGINE] Starting workflow execution with %d blocks", len(workflow.Blocks))

	// Per-workflow concurrency cap. Prevents a misconfigured webhook from
	// spawning unbounded simultaneous runs of the same workflow_id and
	// self-DoS-ing the backend (sandboxes, LLM keys, MCP connections all
	// get exhausted). Acquire returns immediately if at cap — caller's
	// 429 handling converts this to an HTTP response with Retry-After.
	limiter := DefaultLimiter()
	if err := limiter.Acquire(workflow.ID); err != nil {
		log.Printf("🚦 [ENGINE] Concurrency cap hit for workflow %s — rejecting", workflow.ID)
		return nil, err
	}
	defer limiter.Release(workflow.ID)

	// Pull options into locals so we can reference them with nil-safe
	// defaults below.
	executionID := ""
	userID := ""
	resuming := false
	if options != nil {
		executionID = options.ExecutionID
		userID = options.UserID
		resuming = options.Resuming
	}

	// Open the root OpenTelemetry span for this execution. Every block
	// becomes a child span via ExecuteBlockWithTracing. If no tracer is
	// configured (TracingEnabled=false), this is a no-op stub.
	ctx, rootSpan := StartExecutionSpan(ctx, executionID, workflow.ID, userID, len(workflow.Blocks))
	defer rootSpan.End()

	// Durable execution bootstrap. When stateStore is wired AND we have an
	// executionID, write the initial state (idempotent — Resuming will see
	// the existing doc and keep its block_outputs) and start a heartbeat
	// goroutine that pings every 10s so orphan detection on restart can
	// distinguish "still running" from "crashed mid-flight".
	if e.stateStore != nil && executionID != "" {
		if err := e.stateStore.Init(ctx, executionID, workflow.ID, userID, workflow, input); err != nil {
			log.Printf("⚠️ [ENGINE] state store init failed (continuing ephemeral): %v", err)
		} else {
			stopHeartbeat := make(chan struct{})
			defer close(stopHeartbeat)
			go func() {
				ticker := time.NewTicker(HeartbeatInterval)
				defer ticker.Stop()
				for {
					select {
					case <-stopHeartbeat:
						return
					case <-ctx.Done():
						return
					case <-ticker.C:
						_ = e.stateStore.Heartbeat(context.Background(), executionID)
					}
				}
			}()
			if resuming {
				log.Printf("🔄 [ENGINE] Resuming execution %s — checkpointed blocks will be skipped", executionID)
			}
		}
	}

	// Workflow-level timeout: prevents runaway workflows from running forever.
	// Default 10 minutes, configurable per-workflow via WorkflowTimeout field.
	workflowTimeout := 10 * time.Minute
	if workflow.WorkflowTimeout > 0 {
		workflowTimeout = time.Duration(workflow.WorkflowTimeout) * time.Second
	}
	ctx, workflowCancel := context.WithTimeout(ctx, workflowTimeout)
	defer workflowCancel()

	// Goroutine semaphore: limits the number of blocks executing concurrently.
	// Prevents resource exhaustion when workflows have many parallel branches.
	maxParallel := 20
	if workflow.MaxParallelBlocks > 0 {
		maxParallel = workflow.MaxParallelBlocks
	}
	blockSemaphore := make(chan struct{}, maxParallel)

	// Build block index
	blockIndex := make(map[string]models.Block)
	for _, block := range workflow.Blocks {
		blockIndex[block.ID] = block
	}

	// Pre-flight: validate template references to catch typos early
	LogTemplateWarnings(workflow)

	// Build dependency graph
	// dependencies[blockID] = list of block IDs that must complete before this block
	dependencies := make(map[string][]string)
	// dependents[blockID] = list of block IDs that depend on this block
	dependents := make(map[string][]string)

	for _, block := range workflow.Blocks {
		dependencies[block.ID] = []string{}
		dependents[block.ID] = []string{}
	}

	// connectionsBySource maps sourceBlockID -> all connections from that block
	// Used for conditional routing (If/Switch blocks with named output ports)
	connectionsBySource := make(map[string][]models.Connection)

	for _, conn := range workflow.Connections {
		// conn.SourceBlockID -> conn.TargetBlockID
		dependencies[conn.TargetBlockID] = append(dependencies[conn.TargetBlockID], conn.SourceBlockID)
		dependents[conn.SourceBlockID] = append(dependents[conn.SourceBlockID], conn.TargetBlockID)
		connectionsBySource[conn.SourceBlockID] = append(connectionsBySource[conn.SourceBlockID], conn)
	}

	// Find start blocks (no dependencies)
	var startBlocks []string
	for blockID, deps := range dependencies {
		if len(deps) == 0 {
			startBlocks = append(startBlocks, blockID)
		}
	}

	if len(startBlocks) == 0 && len(workflow.Blocks) > 0 {
		return nil, fmt.Errorf("workflow has no start blocks (circular dependency?)")
	}

	log.Printf("📊 [ENGINE] Found %d start blocks: %v", len(startBlocks), startBlocks)

	// Initialize block states and outputs
	blockStates := make(map[string]*models.BlockState)
	blockOutputs := make(map[string]map[string]any)
	var statesMu sync.RWMutex

	for _, block := range workflow.Blocks {
		blockStates[block.ID] = &models.BlockState{
			Status: "pending",
		}
	}

	// Initialize with workflow variables and input
	globalInputs := make(map[string]any)
	log.Printf("🔍 [ENGINE] Workflow input received: %+v", input)

	// First, set workflow variable defaults
	for _, variable := range workflow.Variables {
		if variable.DefaultValue != nil {
			globalInputs[variable.Name] = variable.DefaultValue
			log.Printf("🔍 [ENGINE] Added workflow variable default: %s = %v", variable.Name, variable.DefaultValue)
		}
	}

	// Then, override with execution input (takes precedence over defaults)
	for k, v := range input {
		globalInputs[k] = v
		log.Printf("🔍 [ENGINE] Added/overrode from execution input: %s = %v", k, v)
	}

	// Extract workflow-level model override (workflow-level field takes priority, Start block as fallback)
	if workflow.WorkflowModelID != "" {
		globalInputs["_workflowModelId"] = workflow.WorkflowModelID
		log.Printf("🎯 [ENGINE] Using workflow-level model: %s", workflow.WorkflowModelID)
	} else {
		// Fallback: check Start block config for backward compatibility
		for _, block := range workflow.Blocks {
			if block.Type == "variable" {
				if op, ok := block.Config["operation"].(string); ok && op == "read" {
					if varName, ok := block.Config["variableName"].(string); ok && varName == "input" {
						if modelID, ok := block.Config["workflowModelId"].(string); ok && modelID != "" {
							globalInputs["_workflowModelId"] = modelID
							log.Printf("🎯 [ENGINE] Using Start block model (fallback): %s", modelID)
						}
					}
				}
			}
		}
	}

	// Track completed blocks for dependency resolution
	completedBlocks := make(map[string]bool)
	failedBlocks := make(map[string]bool)
	var completedMu sync.Mutex
	// completedCond is signaled whenever a block completes, replacing polling loops
	completedCond := sync.NewCond(&completedMu)

	// Error tracking
	var executionErrors []string
	var errorsMu sync.Mutex

	// Circuit breaker: after 5 consecutive failures from the same error source
	// (e.g., server_5xx, rate_limit), skip retries for other blocks hitting that source.
	circuitBreaker := NewCircuitBreaker(5)

	// WaitGroup for tracking all goroutines
	var wg sync.WaitGroup

	// Recursive function to execute a block and schedule dependents
	var executeBlock func(blockID string)
	executeBlock = func(blockID string) {
		block := blockIndex[blockID]

		// Update status to running
		statesMu.Lock()
		blockStates[blockID].Status = string(TransitionBlockStatus(
			BlockStatus(blockStates[blockID].Status), BlockStatusRunning))
		blockStates[blockID].StartedAt = timePtr(time.Now())
		statesMu.Unlock()

		// Send status update (without inputs yet - will send after building them)
		trySend(statusChan, models.ExecutionUpdate{
			Type:    "execution_update",
			BlockID: blockID,
			Status:  "running",
		})

		log.Printf("▶️ [ENGINE] Executing block '%s' (type: %s)", block.Name, block.Type)

		// Build inputs for this block from:
		// 1. Global inputs (workflow input + variables)
		// 2. Outputs from upstream blocks
		blockInputs := make(map[string]any)
		log.Printf("🔍 [ENGINE] Block '%s': globalInputs keys: %v", block.Name, getMapKeys(globalInputs))
		for k, v := range globalInputs {
			blockInputs[k] = v
		}
		log.Printf("🔍 [ENGINE] Block '%s': blockInputs after globalInputs: %v", block.Name, getMapKeys(blockInputs))

		// Make ALL completed block outputs available for template resolution
		// This allows blocks to reference any upstream block, not just directly connected ones
		// Example: Final block can use {{start.response}}, {{research-overview.response}}, etc.
		statesMu.RLock()

		essentialKeys := []string{
			"response", "data", "output", "value", "result",
			"artifacts", "toolResults", "tokens", "model",
			"iterations", "_parseError", "rawResponse",
			"generatedFiles", "toolCalls", "timedOut",
		}

		// Track which block is directly connected (for flattening priority)
		directlyConnectedBlockID := ""
		for _, conn := range workflow.Connections {
			if conn.TargetBlockID == blockID {
				directlyConnectedBlockID = conn.SourceBlockID
				break
			}
		}

		// Add ALL completed block outputs (for template access like {{block-name.response}})
		for completedBlockID, output := range blockOutputs {
			sourceBlock, exists := blockIndex[completedBlockID]
			if !exists {
				continue
			}

			// Create clean output (only essential keys)
			cleanOutput := make(map[string]any)
			for _, key := range essentialKeys {
				if val, exists := output[key]; exists {
					cleanOutput[key] = val
				}
			}

			// Store under normalizedId (e.g., "research-overview")
			if sourceBlock.NormalizedID != "" {
				blockInputs[sourceBlock.NormalizedID] = cleanOutput
			}

			// Also store under block ID if different
			if sourceBlock.ID != "" && sourceBlock.ID != sourceBlock.NormalizedID {
				blockInputs[sourceBlock.ID] = cleanOutput
			}
		}

		// Log available block references
		log.Printf("🔗 [ENGINE] Block '%s' can access %d upstream blocks", block.Name, len(blockOutputs))

		// Flatten essential keys from DIRECTLY CONNECTED block only (for {{response}} shorthand)
		if directlyConnectedBlockID != "" {
			if output, ok := blockOutputs[directlyConnectedBlockID]; ok {
				for _, key := range essentialKeys {
					if val, exists := output[key]; exists {
						blockInputs[key] = val
					}
				}
				log.Printf("🔗 [ENGINE] Flattened keys from directly connected block '%s'", blockIndex[directlyConnectedBlockID].Name)
			}
		}

		statesMu.RUnlock()

		// Store the available inputs in BlockState for debugging
		statesMu.Lock()
		blockStates[blockID].Inputs = blockInputs
		statesMu.Unlock()

		log.Printf("🔍 [ENGINE] Block '%s': stored %d input keys for debugging: %v", block.Name, len(blockInputs), getMapKeys(blockInputs))

		// Send updated status with inputs for debugging
		trySend(statusChan, models.ExecutionUpdate{
			Type:    "execution_update",
			BlockID: blockID,
			Status:  "running",
			Inputs:  blockInputs,
		})

		// Get executor for this block type
		executor, lookupErr := e.registry.Get(block.Type)
		if lookupErr != nil {
			handleBlockError(blockID, block.Name, lookupErr, blockStates, &statesMu, statusChan, &executionErrors, &errorsMu)
			completedMu.Lock()
			failedBlocks[blockID] = true
			completedCond.Broadcast()
			completedMu.Unlock()
			return
		}

		// Create timeout context
		// Default: 30s for most blocks, 120s for LLM blocks (they need more time for API calls)
		timeout := 30 * time.Second
		if block.Type == "llm_inference" {
			timeout = 120 * time.Second // LLM blocks get 2 minutes by default
		}
		// User-specified timeout can override, but LLM blocks get at least 120s
		if block.Timeout > 0 {
			userTimeout := time.Duration(block.Timeout) * time.Second
			if block.Type == "llm_inference" && userTimeout < 120*time.Second {
				// LLM blocks need at least 120s for reasoning/streaming
				timeout = 120 * time.Second
			} else {
				timeout = userTimeout
			}
		}
		blockCtx, cancel := context.WithTimeout(ctx, timeout)
		defer cancel()

		// Idempotency cache (#2): if this block already completed under
		// this execution (e.g. we crashed after CheckpointBlock but before
		// notifying downstream blocks; or this is a Resuming run), return
		// the prior output instead of re-executing. Critical for
		// side-effecting blocks (HTTP POST, email, payment) — they must
		// fire exactly once per execution.
		var (
			output        map[string]any
			execErr       error
			servedFromCkpt bool
		)
		if e.stateStore != nil && executionID != "" {
			cached, hit, err := e.stateStore.GetBlockOutput(blockCtx, executionID, blockID)
			if err != nil {
				log.Printf("⚠️ [ENGINE] cache lookup failed for block %s (proceeding): %v", blockID, err)
			} else if hit && cached.Error == "" {
				inputHash := HashBlockInput(blockInputs)
				if cached.InputHash == "" || cached.InputHash == inputHash {
					output = cached.Output
					servedFromCkpt = true
					log.Printf("⏩ [ENGINE] Block '%s' served from checkpoint (skipping re-execution)", block.Name)
				} else {
					log.Printf("⚠️ [ENGINE] Block '%s' has cached output but input hash differs — re-executing", block.Name)
				}
			}
		}

		// Open a child span for the block. Includes block_id, type, name,
		// and whether the work was actually performed or cache-served. Span
		// ends after retries + checkpoint so the duration reflects user-
		// observed latency.
		blockCtx, blockSpan := StartBlockSpan(blockCtx, block.ID, block.Type, block.Name, servedFromCkpt)
		defer blockSpan.End()

		// Execute the block (with optional retries) — only if we didn't
		// already have a cached output.
		blockStart := time.Now()
		if !servedFromCkpt {
			output, execErr = executor.Execute(blockCtx, block, blockInputs)
		}

		// Retry logic: if block has RetryConfig and error is retryable, retry with backoff
		if execErr != nil && block.RetryConfig != nil && block.RetryConfig.MaxRetries > 0 {
			classifiedErr := ClassifyError(execErr)
			errSource := ErrorSource(classifiedErr)
			circuitBreaker.RecordFailure(errSource)

			if circuitBreaker.IsTripped(errSource) {
				log.Printf("🔌 [ENGINE] Block '%s': circuit breaker open for '%s' — skipping retries",
					block.Name, errSource)
				trySend(statusChan, models.ExecutionUpdate{
					Type:    "execution_update",
					BlockID: blockID,
					Status:  "circuit_breaker_tripped",
					Output: map[string]any{
						"source":     errSource,
						"error":      execErr.Error(),
						"userAction": UserFriendlyError(classifiedErr),
					},
				})
			} else if ShouldRetry(classifiedErr, block.RetryConfig.RetryOn) {
				backoff := NewBackoffCalculator(
					block.RetryConfig.BackoffMs,
					block.RetryConfig.MaxBackoffMs,
					2.0, 20,
				)

				var retryHistory []models.RetryAttempt
				retryHistory = append(retryHistory, models.RetryAttempt{
					Attempt:   0,
					Error:     execErr.Error(),
					ErrorType: classifiedErr.Category.String(),
					Timestamp: time.Now(),
				})

				for attempt := 1; attempt <= block.RetryConfig.MaxRetries; attempt++ {
					// Calculate delay (respect RetryAfter from 429 responses)
					delay := backoff.NextDelay(attempt - 1)
					if classifiedErr.RetryAfter > 0 {
						retryAfterDelay := time.Duration(classifiedErr.RetryAfter) * time.Second
						if retryAfterDelay > delay {
							delay = retryAfterDelay
						}
					}

					log.Printf("🔄 [ENGINE] Block '%s': retrying (%d/%d) after %v — %s",
						block.Name, attempt, block.RetryConfig.MaxRetries, delay, classifiedErr.Message)

					// Send retry status update
					trySend(statusChan, models.ExecutionUpdate{
						Type:    "execution_update",
						BlockID: blockID,
						Status:  "retrying",
						Output: map[string]any{
							"attempt":    attempt,
							"maxRetries": block.RetryConfig.MaxRetries,
							"error":      execErr.Error(),
							"errorType":  classifiedErr.Category.String(),
						},
					})

					// Wait before retrying
					select {
					case <-ctx.Done():
						execErr = fmt.Errorf("context cancelled during retry wait")
						break
					case <-time.After(delay):
					}

					if ctx.Err() != nil {
						break
					}

					// Re-execute with fresh timeout
					retryCtx, retryCancel := context.WithTimeout(ctx, timeout)
					attemptStart := time.Now()
					output, execErr = executor.Execute(retryCtx, block, blockInputs)
					retryCancel()

					if execErr == nil {
						// Success on retry — reset circuit breaker for this source
						circuitBreaker.RecordSuccess(errSource)
						log.Printf("✅ [ENGINE] Block '%s': succeeded on retry %d/%d", block.Name, attempt, block.RetryConfig.MaxRetries)
						statesMu.Lock()
						blockStates[blockID].RetryCount = attempt
						blockStates[blockID].RetryHistory = retryHistory
						statesMu.Unlock()
						break
					}

					// Record failed attempt
					classifiedErr = ClassifyError(execErr)
					errSource = ErrorSource(classifiedErr)
					if circuitBreaker.RecordFailure(errSource) {
						log.Printf("🔌 [ENGINE] Circuit breaker tripped for '%s' — aborting retries for block '%s'",
							errSource, block.Name)
						break
					}
					retryHistory = append(retryHistory, models.RetryAttempt{
						Attempt:   attempt,
						Error:     execErr.Error(),
						ErrorType: classifiedErr.Category.String(),
						Timestamp: time.Now(),
						Duration:  time.Since(attemptStart).Milliseconds(),
					})

					// Stop retrying if error is not retryable (e.g., became permanent)
					if !ShouldRetry(classifiedErr, block.RetryConfig.RetryOn) {
						log.Printf("⛔ [ENGINE] Block '%s': error no longer retryable after attempt %d — %s",
							block.Name, attempt, classifiedErr.Category.String())
						break
					}
				}

				// Store retry history even if all retries failed
				if execErr != nil {
					statesMu.Lock()
					blockStates[blockID].RetryCount = len(retryHistory) - 1
					blockStates[blockID].RetryHistory = retryHistory
					statesMu.Unlock()
				}
			}
		}

		if execErr != nil {
			handleBlockError(blockID, block.Name, execErr, blockStates, &statesMu, statusChan, &executionErrors, &errorsMu)
			completedMu.Lock()
			failedBlocks[blockID] = true
			completedCond.Broadcast()
			completedMu.Unlock()
			return
		}

		// Block Completion Check: Validate if block actually accomplished its job
		// This catches cases where a block "completed" but didn't actually succeed
		// (e.g., repeated tool errors, timeouts, empty responses)
		if options != nil && options.EnableBlockChecker && e.blockChecker != nil && ShouldCheckBlock(block) {
			log.Printf("🔍 [ENGINE] Running block completion check for '%s'", block.Name)

			checkerModelID := options.CheckerModelID
			if checkerModelID == "" {
				// Default to a fast model for checking
				checkerModelID = "gpt-4.1"
			}

			checkResult, checkErr := e.blockChecker.CheckBlockCompletion(
				ctx,
				options.WorkflowGoal,
				block,
				blockInputs,
				output,
				checkerModelID,
			)

			if checkErr != nil {
				log.Printf("⚠️ [ENGINE] Block checker error (continuing): %v", checkErr)
			} else if !checkResult.Passed {
				// Block failed the completion check - treat as failure
				log.Printf("❌ [ENGINE] Block '%s' failed completion check: %s\n   Actual Output: %s", block.Name, checkResult.Reason, checkResult.ActualOutput)

				// Add check failure info to output for visibility
				output["_blockCheckFailed"] = true
				output["_blockCheckReason"] = checkResult.Reason
				output["_blockActualOutput"] = checkResult.ActualOutput

				checkError := fmt.Errorf("block did not accomplish its job: %s\n\nActual Output: %s", checkResult.Reason, checkResult.ActualOutput)
				handleBlockError(blockID, block.Name, checkError, blockStates, &statesMu, statusChan, &executionErrors, &errorsMu)
				completedMu.Lock()
				failedBlocks[blockID] = true
				completedCond.Broadcast()
				completedMu.Unlock()
				return
			} else {
				log.Printf("✓ [ENGINE] Block '%s' passed completion check: %s", block.Name, checkResult.Reason)
			}
		}

		// Store output and mark completed
		statesMu.Lock()
		blockOutputs[blockID] = output
		blockStates[blockID].Status = string(TransitionBlockStatus(
			BlockStatus(blockStates[blockID].Status), BlockStatusCompleted))
		blockStates[blockID].CompletedAt = timePtr(time.Now())
		blockStates[blockID].Outputs = output
		statesMu.Unlock()

		// Durable checkpoint (#1 + #2). Skip when we served from cache —
		// the data is already there. This is the single most important
		// line in the durability layer: after this write succeeds, a
		// crash before the next block notifies will resume from here
		// instead of re-running this block. Failure to checkpoint is
		// logged but NOT fatal — we'd rather complete the workflow than
		// abort on a transient mongo blip.
		if e.stateStore != nil && executionID != "" && !servedFromCkpt {
			ckpt := BlockCheckpoint{
				Output:      output,
				InputHash:   HashBlockInput(blockInputs),
				CompletedAt: time.Now().UTC(),
				DurationMs:  time.Since(blockStart).Milliseconds(),
			}
			if cerr := e.stateStore.CheckpointBlock(context.Background(), executionID, blockID, ckpt); cerr != nil {
				log.Printf("⚠️ [ENGINE] checkpoint write failed for block %s (continuing): %v", blockID, cerr)
			}
		}

		// Legacy ad-hoc checkpoint hook (kept for backwards-compat with
		// existing callers; will be removed when they migrate to the
		// state store).
		if options != nil && options.Checkpoint != nil {
			options.Checkpoint(blockID, "completed", output)
		}

		// Send completion update with inputs for debugging
		trySend(statusChan, models.ExecutionUpdate{
			Type:    "execution_update",
			BlockID: blockID,
			Status:  "completed",
			Inputs:  blockInputs,
			Output:  output,
		})

		log.Printf("✅ [ENGINE] Block '%s' completed", block.Name)

		// Mark as completed and check dependents
		completedMu.Lock()
		completedBlocks[blockID] = true
		completedCond.Broadcast()

		// === FOR_EACH ITERATION ===
		// If this block is a for_each, handle iteration: run "loop_body" downstream
		// blocks once per item, then fire "done" downstream blocks with aggregated results.
		if block.Type == "for_each" {
			items, _ := output["response"].([]any)
			itemVariable := "item"
			if iv, ok := block.Config["itemVariable"].(string); ok && iv != "" {
				itemVariable = iv
			}

			// Separate loop_body vs done dependents (direct deps of for_each)
			var loopBodyDeps []string
			var doneDeps []string
			doneDepSet := make(map[string]bool)
			for _, depBlockID := range dependents[blockID] {
				isLoopBody := false
				isDone := false
				for _, conn := range connectionsBySource[blockID] {
					if conn.TargetBlockID != depBlockID {
						continue
					}
					if conn.SourceOutput == "loop_body" {
						isLoopBody = true
					} else if conn.SourceOutput == "done" {
						isDone = true
					} else {
						// No specific output = default, treat as loop_body
						isLoopBody = true
					}
				}
				if isLoopBody {
					loopBodyDeps = append(loopBodyDeps, depBlockID)
				}
				if isDone {
					doneDeps = append(doneDeps, depBlockID)
					doneDepSet[depBlockID] = true
				}
			}

			// Walk the dependency graph to find ALL blocks in the loop body subgraph.
			// This includes not just direct loop_body deps but their entire downstream chain.
			// Without this, indirect blocks (e.g., for_each → AI Agent → Run Tool) would
			// race with the next iteration and read stale/deleted data.
			loopBodySubgraph := make(map[string]bool)
			var walkLoopDeps func(string)
			walkLoopDeps = func(bid string) {
				if loopBodySubgraph[bid] || doneDepSet[bid] || bid == blockID {
					return
				}
				loopBodySubgraph[bid] = true
				for _, downstream := range dependents[bid] {
					walkLoopDeps(downstream)
				}
			}
			for _, dep := range loopBodyDeps {
				walkLoopDeps(dep)
			}

			log.Printf("🔄 [ENGINE] For-each '%s': iterating %d items over %d loop_body blocks (%d in subgraph), %d done blocks",
				block.Name, len(items), len(loopBodyDeps), len(loopBodySubgraph), len(doneDeps))

			// Run loop_body subgraph once per item, waiting for the full chain per iteration
			var iterationResults []map[string]any
			for i, item := range items {
				log.Printf("🔄 [ENGINE] For-each '%s': iteration %d/%d", block.Name, i+1, len(items))

				// Send iteration progress update
				trySend(statusChan, models.ExecutionUpdate{
					Type:    "execution_update",
					BlockID: blockID,
					Status:  "running",
					Output: map[string]any{
						"_iteration":   i + 1,
						"_totalItems":  len(items),
						"_currentItem": item,
					},
				})

				// Create per-iteration output for this for_each block
				perItemOutput := map[string]any{
					"response":   item,
					"data":       item,
					itemVariable: item,
					"index":      i,
					"totalItems": len(items),
					"branch":     "loop_body",
				}

				// Override for_each output with per-item data
				statesMu.Lock()
				blockOutputs[blockID] = perItemOutput
				statesMu.Unlock()

				// Reset ALL blocks in the loop body subgraph for this iteration
				for bid := range loopBodySubgraph {
					statesMu.Lock()
					blockStates[bid].Status = string(TransitionBlockStatus(
						BlockStatus(blockStates[bid].Status), BlockStatusPending))
					blockStates[bid].StartedAt = nil
					blockStates[bid].CompletedAt = nil
					blockStates[bid].Error = ""
					delete(blockOutputs, bid)
					statesMu.Unlock()
					delete(completedBlocks, bid)
				}

				// Execute direct loop_body dependents to kick off the chain
				for _, depBlockID := range loopBodyDeps {
					// Check non-loop dependencies are met
					allDepsReady := true
					for _, reqBlockID := range dependencies[depBlockID] {
						if reqBlockID == blockID || loopBodySubgraph[reqBlockID] {
							continue // for_each is done; subgraph blocks will re-run
						}
						if !completedBlocks[reqBlockID] {
							allDepsReady = false
							break
						}
					}
					if !allDepsReady {
						log.Printf("⚠️ [ENGINE] Skipping loop body block '%s' — other deps not ready", blockIndex[depBlockID].Name)
						continue
					}

					completedMu.Unlock()

					var iterWg sync.WaitGroup
					iterWg.Add(1)
					go func(bid string) {
						defer iterWg.Done()
						defer recoverBlock(bid, blockIndex[bid].Name, blockStates, &statesMu, failedBlocks, completedCond, statusChan, &executionErrors, &errorsMu)
						blockSemaphore <- struct{}{}
						defer func() { <-blockSemaphore }()
						executeBlock(bid)
					}(depBlockID)
					iterWg.Wait()

					completedMu.Lock()
				}

				// Wait for ALL blocks in the loop subgraph to complete.
				// Direct deps dispatched above will asynchronously trigger downstream blocks
				// in the chain (via normal dependent dispatch). We must wait for the entire
				// chain to finish before starting the next iteration.
				// Uses completedCond instead of polling to avoid CPU waste and latency.
				for {
					allDone := true
					for bid := range loopBodySubgraph {
						if !completedBlocks[bid] {
							allDone = false
							break
						}
					}
					if allDone {
						break
					}
					completedCond.Wait()
				}

				// Collect iteration results from the LAST block(s) in the chain
				// (blocks with no downstream blocks in the subgraph)
				for bid := range loopBodySubgraph {
					hasDownstream := false
					for _, downstream := range dependents[bid] {
						if loopBodySubgraph[downstream] {
							hasDownstream = true
							break
						}
					}
					if !hasDownstream {
						statesMu.RLock()
						if iterOutput, ok := blockOutputs[bid]; ok {
							resultCopy := make(map[string]any)
							for k, v := range iterOutput {
								resultCopy[k] = v
							}
							resultCopy["_iterationIndex"] = i
							iterationResults = append(iterationResults, resultCopy)
						}
						statesMu.RUnlock()
					}
				}
			}

			// Restore the for_each block's output to the full aggregated version
			output["iterationResults"] = iterationResults
			statesMu.Lock()
			blockOutputs[blockID] = output
			statesMu.Unlock()

			log.Printf("✅ [ENGINE] For-each '%s': all %d iterations complete, %d results collected",
				block.Name, len(items), len(iterationResults))

			// Re-send "completed" status for the for_each block.
			// During iteration we sent "running" updates which override the initial "completed",
			// so the frontend needs a final "completed" to update the block state.
			trySend(statusChan, models.ExecutionUpdate{
				Type:    "execution_update",
				BlockID: blockID,
				Status:  "completed",
				Output:  output,
			})

			// Now fire "done" dependents
			for _, depBlockID := range doneDeps {
				allDepsReady := true
				for _, reqBlockID := range dependencies[depBlockID] {
					if reqBlockID == blockID {
						continue
					}
					if !completedBlocks[reqBlockID] {
						allDepsReady = false
						break
					}
				}
				if allDepsReady {
					wg.Add(1)
					go func(bid string) {
						defer wg.Done()
						defer recoverBlock(bid, blockIndex[bid].Name, blockStates, &statesMu, failedBlocks, completedCond, statusChan, &executionErrors, &errorsMu)
						blockSemaphore <- struct{}{}
						defer func() { <-blockSemaphore }()
						executeBlock(bid)
					}(depBlockID)
				}
			}

			completedMu.Unlock()
			return // Skip normal dependent dispatch below
		}

		// === NORMAL DEPENDENT DISPATCH (non-for_each blocks) ===
		// Check if any dependent blocks can now run
		for _, depBlockID := range dependents[blockID] {
			canRun := true

			// Branch-aware routing: check if the connection from this block
			// to the dependent has a SourceOutput that matches the block's branch.
			// Empty or "output" SourceOutput always fires (backward compatible).
			branchBlocked := false
			for _, conn := range connectionsBySource[blockID] {
				if conn.TargetBlockID != depBlockID {
					continue
				}
				if conn.SourceOutput != "" && conn.SourceOutput != "output" {
					// This connection requires a specific branch (e.g., "true" or "false")
					branch, _ := output["branch"].(string)
					// Wildcard branch "*" means all branches fire (used by for_each)
					if branch != "*" && branch != conn.SourceOutput {
						branchBlocked = true
						break
					}
				}
			}
			if branchBlocked {
				log.Printf("🔀 [ENGINE] Skipping block '%s' — branch not taken", blockIndex[depBlockID].Name)
				continue
			}

			for _, reqBlockID := range dependencies[depBlockID] {
				if !completedBlocks[reqBlockID] {
					// Check if the required block failed - if so, we can't run
					if failedBlocks[reqBlockID] {
						canRun = false
						break
					}
					// Required block hasn't completed yet
					canRun = false
					break
				}
			}
			if canRun {
				// Queue this block for execution
				wg.Add(1)
				go func(bid string) {
					defer wg.Done()
					defer recoverBlock(bid, blockIndex[bid].Name, blockStates, &statesMu, failedBlocks, completedCond, statusChan, &executionErrors, &errorsMu)
					blockSemaphore <- struct{}{}
					defer func() { <-blockSemaphore }()
					executeBlock(bid)
				}(depBlockID)
			}
		}

		// Memory cleanup: free upstream block outputs once all their consumers have run.
		// This prevents O(n^2) memory growth in large workflows.
		// Skip terminal blocks (no dependents) since their outputs are needed for finalOutput.
		for _, upstreamID := range dependencies[blockID] {
			if len(dependents[upstreamID]) == 0 {
				continue // terminal block, keep output
			}
			allConsumersDone := true
			for _, consumerID := range dependents[upstreamID] {
				if !completedBlocks[consumerID] && !failedBlocks[consumerID] {
					allConsumersDone = false
					break
				}
			}
			if allConsumersDone {
				statesMu.Lock()
				delete(blockOutputs, upstreamID)
				statesMu.Unlock()
			}
		}

		completedMu.Unlock()
	}

	// Start execution with start blocks
	for _, blockID := range startBlocks {
		wg.Add(1)
		go func(bid string) {
			defer wg.Done()
			defer recoverBlock(bid, blockIndex[bid].Name, blockStates, &statesMu, failedBlocks, completedCond, statusChan, &executionErrors, &errorsMu)
			blockSemaphore <- struct{}{} // acquire
			defer func() { <-blockSemaphore }() // release
			executeBlock(bid)
		}(blockID)
	}

	// Wait for all blocks to complete
	wg.Wait()

	// Determine final status
	finalStatus := "completed"
	var failedBlockIDs []string
	var completedCount, failedCount int

	statesMu.RLock()
	for blockID, state := range blockStates {
		if state.Status == "completed" {
			completedCount++
		} else if state.Status == "failed" {
			failedCount++
			failedBlockIDs = append(failedBlockIDs, blockID)
		}
	}
	statesMu.RUnlock()

	if failedCount > 0 {
		if completedCount > 0 {
			finalStatus = "partial"
		} else {
			finalStatus = "failed"
		}
	}

	// Collect final output from terminal blocks (blocks with no dependents)
	finalOutput := make(map[string]any)
	statesMu.RLock()
	for blockID, deps := range dependents {
		if len(deps) == 0 {
			if output, ok := blockOutputs[blockID]; ok {
				block := blockIndex[blockID]
				finalOutput[block.Name] = output
			}
		}
	}
	statesMu.RUnlock()

	// Build error message if any
	var errorMsg string
	errorsMu.Lock()
	if len(executionErrors) > 0 {
		errorMsg = fmt.Sprintf("%d block(s) failed: %v", len(executionErrors), executionErrors)
	}
	errorsMu.Unlock()

	log.Printf("🏁 [ENGINE] Workflow execution %s: %d completed, %d failed",
		finalStatus, completedCount, failedCount)

	// Finalise the durable state. We attach the final status so the orphan
	// scanner stops considering this execution. completed_at also drives
	// the 30-day TTL prune on the state collection.
	if e.stateStore != nil && executionID != "" {
		status := StateStatusCompleted
		if finalStatus != "completed" {
			status = StateStatusFailed
		}
		if cerr := e.stateStore.MarkCompleted(context.Background(), executionID, status); cerr != nil {
			log.Printf("⚠️ [ENGINE] mark completed failed for %s: %v", executionID, cerr)
		}
	}
	if errorMsg != "" {
		AnnotateSpanError(rootSpan, errorMsg)
	}

	return &ExecutionResult{
		Status:      finalStatus,
		Output:      finalOutput,
		BlockStates: blockStates,
		Error:       errorMsg,
	}, nil
}

// ResumeExecution restarts an interrupted execution from its checkpoints.
// Called on startup for each orphan reported by stateStore.FindOrphaned —
// loads the workflow snapshot from the state doc and re-runs the engine
// with Resuming=true. Already-completed blocks come straight from the
// idempotency cache so we don't redo work or re-fire side effects.
//
// Operates on a fresh background context: the original caller is long
// gone. statusChan is buffered + drained-and-discarded — we don't have a
// WebSocket to stream to during recovery.
func (e *WorkflowEngine) ResumeExecution(ctx context.Context, state ExecutionState) (*ExecutionResult, error) {
	if e.stateStore == nil {
		return nil, fmt.Errorf("state store not configured — cannot resume")
	}
	if state.WorkflowSnapshot == nil {
		return nil, fmt.Errorf("execution %s has no workflow snapshot — unrecoverable", state.ExecutionID)
	}
	log.Printf("🔁 [ENGINE] Resuming %s (%d blocks checkpointed)", state.ExecutionID, len(state.BlockOutputs))

	// Drain channel: nobody's listening on the other side; we still need
	// trySend to be non-blocking.
	statusChan := make(chan models.ExecutionUpdate, 256)
	go func() {
		for range statusChan {
		}
	}()

	return e.ExecuteWithOptions(ctx, state.WorkflowSnapshot, state.InitialInput, statusChan, &ExecutionOptions{
		ExecutionID: state.ExecutionID,
		UserID:      state.UserID,
		Resuming:    true,
	})
}

// handleBlockError handles block execution errors with classification for debugging
func handleBlockError(
	blockID, blockName string,
	err error,
	blockStates map[string]*models.BlockState,
	statesMu *sync.RWMutex,
	statusChan chan<- models.ExecutionUpdate,
	executionErrors *[]string,
	errorsMu *sync.Mutex,
) {
	// Try to extract error classification for better debugging
	var errorType string
	var retryable bool

	if execErr, ok := err.(*ExecutionError); ok {
		errorType = execErr.Category.String()
		retryable = execErr.Retryable
		log.Printf("❌ [ENGINE] Block '%s' failed: %v [type=%s, retryable=%v]", blockName, err, errorType, retryable)
	} else {
		errorType = "unknown"
		retryable = false
		log.Printf("❌ [ENGINE] Block '%s' failed: %v", blockName, err)
	}

	statesMu.Lock()
	blockStates[blockID].Status = string(TransitionBlockStatus(
		BlockStatus(blockStates[blockID].Status), BlockStatusFailed))
	blockStates[blockID].CompletedAt = timePtr(time.Now())
	blockStates[blockID].Error = err.Error()
	statesMu.Unlock()

	// Build user-friendly error message if this is a classified error
	userMessage := err.Error()
	if execErr, ok := err.(*ExecutionError); ok {
		if friendly := UserFriendlyError(execErr); friendly != "" {
			userMessage = friendly
		}
	}

	// Include error classification in status update for frontend visibility
	trySend(statusChan, models.ExecutionUpdate{
		Type:    "execution_update",
		BlockID: blockID,
		Status:  "failed",
		Error:   userMessage,
		Output: map[string]any{
			"errorType":    errorType,
			"retryable":    retryable,
			"rawError":     err.Error(),
		},
	})

	errorsMu.Lock()
	*executionErrors = append(*executionErrors, fmt.Sprintf("%s: %s", blockName, err.Error()))
	errorsMu.Unlock()
}

// timePtr returns a pointer to a time.Time
func timePtr(t time.Time) *time.Time {
	return &t
}

// getMapKeys returns the keys of a map as a slice
func getMapKeys(m map[string]any) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	return keys
}

// BuildAPIResponse converts an ExecutionResult into a clean, structured API response
// This provides a standardized output format for API consumers
func (e *WorkflowEngine) BuildAPIResponse(
	result *ExecutionResult,
	workflow *models.Workflow,
	executionID string,
	durationMs int64,
) *models.ExecutionAPIResponse {
	response := &models.ExecutionAPIResponse{
		Status:    result.Status,
		Artifacts: []models.APIArtifact{},
		Files:     []models.APIFile{},
		Blocks:    make(map[string]models.APIBlockOutput),
		Metadata: models.ExecutionMetadata{
			ExecutionID: executionID,
			DurationMs:  durationMs,
		},
		Error: result.Error,
	}

	// Build block index for lookups
	blockIndex := make(map[string]models.Block)
	for _, block := range workflow.Blocks {
		blockIndex[block.ID] = block
	}

	// Track totals
	var totalTokens int
	var blocksExecuted, blocksFailed int

	// Process block states
	for blockID, state := range result.BlockStates {
		block, exists := blockIndex[blockID]
		if !exists {
			continue
		}

		// Create clean block output
		blockOutput := models.APIBlockOutput{
			Name:   block.Name,
			Type:   block.Type,
			Status: state.Status,
		}

		if state.Status == "completed" {
			blocksExecuted++
		} else if state.Status == "failed" {
			blocksFailed++
			blockOutput.Error = state.Error
		}

		// Extract response text from outputs
		if state.Outputs != nil {
			// Primary response
			if resp, ok := state.Outputs["response"].(string); ok {
				blockOutput.Response = resp
			}

			// Extract tokens (for metadata, but don't expose)
			if tokens, ok := state.Outputs["tokens"].(map[string]any); ok {
				if total, ok := tokens["total"].(int); ok {
					totalTokens += total
				} else if total, ok := tokens["total"].(float64); ok {
					totalTokens += int(total)
				}
			}

			// Calculate duration from timestamps
			if state.StartedAt != nil && state.CompletedAt != nil {
				blockOutput.DurationMs = state.CompletedAt.Sub(*state.StartedAt).Milliseconds()
			}

			// Extract structured data - filter all outputs except response
			cleanData := make(map[string]any)
			for k, v := range state.Outputs {
				// Skip internal fields and the response (already extracted)
				if !isInternalField(k) && k != "response" {
					// Also check nested output object
					if k == "output" {
						if outputMap, ok := v.(map[string]any); ok {
							for ok, ov := range outputMap {
								if !isInternalField(ok) && ok != "response" {
									cleanData[ok] = ov
								}
							}
						}
					} else {
						cleanData[k] = v
					}
				}
			}
			if len(cleanData) > 0 {
				blockOutput.Data = cleanData
			}

			// Extract artifacts from this block
			artifacts := extractArtifactsFromBlockOutput(state.Outputs, block.Name)
			response.Artifacts = append(response.Artifacts, artifacts...)

			// Extract files from this block
			files := extractFilesFromBlockOutput(state.Outputs, block.Name)
			response.Files = append(response.Files, files...)
		}

		response.Blocks[block.ID] = blockOutput
	}

	// Set metadata
	response.Metadata.TotalTokens = totalTokens
	response.Metadata.BlocksExecuted = blocksExecuted
	response.Metadata.BlocksFailed = blocksFailed
	if workflow != nil {
		response.Metadata.WorkflowVersion = workflow.Version
	}

	// Extract the primary result and structured data from terminal blocks
	response.Result, response.Data = extractPrimaryResultAndData(result.Output, result.BlockStates)

	log.Printf("📦 [ENGINE] Built API response: status=%s, result_length=%d, has_data=%v, artifacts=%d, files=%d",
		response.Status, len(response.Result), response.Data != nil, len(response.Artifacts), len(response.Files))

	return response
}

// extractPrimaryResultAndData gets the main text result AND structured data from the workflow output
// For structured output blocks, the "data" field contains the parsed JSON which we return separately
func extractPrimaryResultAndData(output map[string]any, blockStates map[string]*models.BlockState) (string, any) {
	// First, try to get from the final output (terminal blocks)
	for blockName, blockOutput := range output {
		if blockData, ok := blockOutput.(map[string]any); ok {
			var resultStr string
			var structuredData any

			// Look for response field (the text/JSON string)
			if resp, ok := blockData["response"].(string); ok && resp != "" {
				resultStr = resp
				log.Printf("📝 [ENGINE] Extracted primary result from block '%s' (%d chars)", blockName, len(resp))
			} else if resp, ok := blockData["rawResponse"].(string); ok && resp != "" {
				// Fallback to rawResponse
				resultStr = resp
			}

			// Look for structured data field (parsed JSON from structured output blocks)
			// This is populated when outputFormat="json" and the response was successfully parsed
			if data, ok := blockData["data"]; ok && data != nil {
				structuredData = data
				log.Printf("📊 [ENGINE] Extracted structured data from block '%s'", blockName)
			}

			if resultStr != "" {
				return resultStr, structuredData
			}
		}
	}

	// Fallback: find the last completed block with a response
	var lastResponse string
	var lastData any
	for _, state := range blockStates {
		if state.Status == "completed" && state.Outputs != nil {
			if resp, ok := state.Outputs["response"].(string); ok && resp != "" {
				lastResponse = resp
			}
			if data, ok := state.Outputs["data"]; ok && data != nil {
				lastData = data
			}
		}
	}

	return lastResponse, lastData
}

// extractArtifactsFromBlockOutput extracts artifacts from a block's output
func extractArtifactsFromBlockOutput(outputs map[string]any, blockName string) []models.APIArtifact {
	var artifacts []models.APIArtifact

	// Check for artifacts array
	if rawArtifacts, ok := outputs["artifacts"]; ok {
		switch arts := rawArtifacts.(type) {
		case []any:
			for _, a := range arts {
				if artMap, ok := a.(map[string]any); ok {
					artifact := models.APIArtifact{
						SourceBlock: blockName,
					}
					if t, ok := artMap["type"].(string); ok {
						artifact.Type = t
					}
					if f, ok := artMap["format"].(string); ok {
						artifact.Format = f
					}
					if d, ok := artMap["data"].(string); ok {
						artifact.Data = d
					}
					if t, ok := artMap["title"].(string); ok {
						artifact.Title = t
					}
					if artifact.Data != "" && len(artifact.Data) > 100 {
						artifacts = append(artifacts, artifact)
					}
				}
			}
		}
	}

	return artifacts
}

// extractFilesFromBlockOutput extracts generated files from a block's output
func extractFilesFromBlockOutput(outputs map[string]any, blockName string) []models.APIFile {
	var files []models.APIFile

	// Check for generatedFiles array
	if rawFiles, ok := outputs["generatedFiles"]; ok {
		switch fs := rawFiles.(type) {
		case []any:
			for _, f := range fs {
				if fileMap, ok := f.(map[string]any); ok {
					file := models.APIFile{
						SourceBlock: blockName,
					}
					if id, ok := fileMap["file_id"].(string); ok {
						file.FileID = id
					}
					if fn, ok := fileMap["filename"].(string); ok {
						file.Filename = fn
					}
					if url, ok := fileMap["download_url"].(string); ok {
						file.DownloadURL = url
					}
					if mt, ok := fileMap["mime_type"].(string); ok {
						file.MimeType = mt
					}
					if sz, ok := fileMap["size"].(float64); ok {
						file.Size = int64(sz)
					}
					if file.FileID != "" || file.DownloadURL != "" {
						files = append(files, file)
					}
				}
			}
		}
	}

	// Also check for single file reference
	if fileURL, ok := outputs["file_url"].(string); ok && fileURL != "" {
		file := models.APIFile{
			DownloadURL: fileURL,
			SourceBlock: blockName,
		}
		if fn, ok := outputs["file_name"].(string); ok {
			file.Filename = fn
		}
		files = append(files, file)
	}

	return files
}

// isInternalField checks if a field name is internal and should be hidden from API response
func isInternalField(key string) bool {
	// Any field starting with _ or __ is internal
	if len(key) > 0 && key[0] == '_' {
		return true
	}
	
	internalFields := map[string]bool{
		// Response duplicates
		"rawResponse":  true,
		"output":       true, // Duplicate of response
		
		// Execution internals
		"tokens":       true,
		"toolCalls":    true,
		"iterations":   true,
		"model":        true, // Internal model ID - never expose
		
		// Already extracted separately
		"artifacts":      true,
		"generatedFiles": true,
		"file_url":       true,
		"file_name":      true,
		
		// Passthrough noise
		"start":        true,
		"input":        true, // Passthrough from workflow input
		"value":        true, // Duplicate of input
		"timedOut":     true,
	}
	return internalFields[key]
}