package execution

// ============================================================================
// Per-workflow concurrency limiter
//
// Problem: a misconfigured webhook can spawn many simultaneous instances of
// the same workflow (e.g. a stuck upstream that retries every second).
// Without a cap, they all run concurrently — each holding open sandboxes,
// LLM calls, MCP connections — and the backend self-DoSes.
//
// Solution: a tiny in-memory limiter keyed by workflow_id. Tries to acquire
// a slot; if the workflow is at-capacity, returns immediately with a typed
// error so the caller can 429 the request (with a Retry-After hint) rather
// than queueing forever and starving everything else.
//
// Defaults: 5 concurrent executions per workflow_id. Adjustable per-process
// via the WORKFLOW_PER_ID_LIMIT env var. Single-node only — multi-node
// deployments need a distributed counter (Redis INCR with TTL is the usual
// pattern).
//
// This is NOT a queue. We do not buffer rejected runs. The reasoning is
// that webhook triggers are typically retryable upstream; rather than
// silently piling up work, we tell the caller to back off — which is the
// signal they need to fix the underlying cause.

import (
	"fmt"
	"os"
	"strconv"
	"sync"
	"time"
)

// LimiterError is returned by Acquire when the workflow is at capacity.
// Callers can check via errors.As to convert it to a 429 + Retry-After.
type LimiterError struct {
	WorkflowID  string
	Active      int
	Limit       int
	RetryAfter  time.Duration
}

func (e *LimiterError) Error() string {
	return fmt.Sprintf("workflow %s at capacity (%d/%d concurrent runs) — retry in %v",
		e.WorkflowID, e.Active, e.Limit, e.RetryAfter)
}

// WorkflowLimiter caps concurrent executions per workflow_id.
type WorkflowLimiter struct {
	mu       sync.Mutex
	limit    int
	active   map[string]int
}

var (
	defaultLimiterOnce sync.Once
	defaultLimiter     *WorkflowLimiter
)

// DefaultLimiter returns the process-global limiter, lazily constructed
// from WORKFLOW_PER_ID_LIMIT (default 5). Thread-safe.
func DefaultLimiter() *WorkflowLimiter {
	defaultLimiterOnce.Do(func() {
		limit := 5
		if v := os.Getenv("WORKFLOW_PER_ID_LIMIT"); v != "" {
			if n, err := strconv.Atoi(v); err == nil && n > 0 {
				limit = n
			}
		}
		defaultLimiter = NewWorkflowLimiter(limit)
	})
	return defaultLimiter
}

// NewWorkflowLimiter constructs a fresh limiter with the given cap.
func NewWorkflowLimiter(limit int) *WorkflowLimiter {
	if limit <= 0 {
		limit = 5
	}
	return &WorkflowLimiter{
		limit:  limit,
		active: make(map[string]int),
	}
}

// Acquire tries to reserve a slot for workflowID. Returns nil on success,
// *LimiterError when at cap. Caller MUST call Release after the execution
// finishes (defer is fine — see ExecuteWithOptions). Empty workflowID is
// treated as opt-out (no limiting) — useful for ephemeral test runs.
func (w *WorkflowLimiter) Acquire(workflowID string) error {
	if workflowID == "" {
		return nil
	}
	w.mu.Lock()
	defer w.mu.Unlock()
	cur := w.active[workflowID]
	if cur >= w.limit {
		return &LimiterError{
			WorkflowID: workflowID,
			Active:     cur,
			Limit:      w.limit,
			// 10s is a reasonable nudge: long enough to let active runs
			// finish, short enough that polling clients don't give up.
			RetryAfter: 10 * time.Second,
		}
	}
	w.active[workflowID] = cur + 1
	return nil
}

// Release returns a slot to the pool. Idempotent for unknown ids — safer
// than panicking on a programming error in a long-running daemon.
func (w *WorkflowLimiter) Release(workflowID string) {
	if workflowID == "" {
		return
	}
	w.mu.Lock()
	defer w.mu.Unlock()
	cur := w.active[workflowID]
	if cur <= 1 {
		delete(w.active, workflowID)
	} else {
		w.active[workflowID] = cur - 1
	}
}

// Stats returns a snapshot of currently-busy workflow ids and their slot
// counts. Used by the admin /api/admin/workflow-concurrency endpoint (not
// yet wired) and by tests.
func (w *WorkflowLimiter) Stats() map[string]int {
	w.mu.Lock()
	defer w.mu.Unlock()
	out := make(map[string]int, len(w.active))
	for k, v := range w.active {
		out[k] = v
	}
	return out
}

// Limit returns the configured cap.
func (w *WorkflowLimiter) Limit() int {
	return w.limit
}
