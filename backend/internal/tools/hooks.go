package tools

import (
	"log"
	"regexp"
	"sync"
	"time"
)

// ─── Hook types ────────────────────────────────────────────────────────
//
// Hooks are declarative, low-cost middleware that fires around every tool
// invocation. They run in the same goroutine as the tool — no extra context
// budget, no extra LLM round-trips. The Claude Agent SDK's PreToolUse /
// PostToolUse / Stop hook contract; we expose just the first two for now.
//
// A Pre hook can:
//   - mutate args in place (e.g. redact secrets, inject defaults)
//   - block the call by returning Deny=true with a reason
//
// A Post hook can:
//   - log, audit, attach metadata
//   - rewrite the tool's result (e.g. truncate, redact)

type PreToolDecision struct {
	Allow  bool   // true to proceed, false to abort the tool call
	Reason string // shown to the model when blocking, e.g. "denied by policy"
}

// PreToolHook runs before the tool's Execute. It may mutate `args` in place.
// Returning Allow=false aborts the call; the model sees Reason as the tool result.
type PreToolHook interface {
	Name() string
	PreToolUse(toolName string, args map[string]interface{}) PreToolDecision
}

// PostToolHook runs after the tool's Execute. It may return a modified result
// (e.g. truncated or redacted). Returning the input result unchanged is fine.
type PostToolHook interface {
	Name() string
	PostToolUse(toolName string, args map[string]interface{}, result string, took time.Duration, err error) string
}

// ─── Registry ──────────────────────────────────────────────────────────

var (
	hookMu  sync.RWMutex
	preHks  []PreToolHook
	postHks []PostToolHook
)

// RegisterPreToolHook adds a hook that runs before each tool invocation.
// Hooks fire in registration order. Safe to call from init() or at runtime.
func RegisterPreToolHook(h PreToolHook) {
	hookMu.Lock()
	defer hookMu.Unlock()
	preHks = append(preHks, h)
	log.Printf("🪝 [HOOK] Registered pre-tool hook: %s", h.Name())
}

// RegisterPostToolHook adds a hook that runs after each tool invocation.
func RegisterPostToolHook(h PostToolHook) {
	hookMu.Lock()
	defer hookMu.Unlock()
	postHks = append(postHks, h)
	log.Printf("🪝 [HOOK] Registered post-tool hook: %s", h.Name())
}

// RunPreToolHooks fires every registered pre-hook in order. First hook to
// return Allow=false short-circuits the rest; its Reason flows back to the
// caller as the "result" the model sees.
func RunPreToolHooks(toolName string, args map[string]interface{}) PreToolDecision {
	hookMu.RLock()
	hooks := append([]PreToolHook(nil), preHks...)
	hookMu.RUnlock()
	for _, h := range hooks {
		d := h.PreToolUse(toolName, args)
		if !d.Allow {
			log.Printf("🚫 [HOOK] %s blocked tool=%s reason=%q", h.Name(), toolName, d.Reason)
			return d
		}
	}
	return PreToolDecision{Allow: true}
}

// RunPostToolHooks fires every registered post-hook in order. Each hook sees
// the (possibly already-modified) result of the previous and can rewrite it.
func RunPostToolHooks(toolName string, args map[string]interface{}, result string, took time.Duration, err error) string {
	hookMu.RLock()
	hooks := append([]PostToolHook(nil), postHks...)
	hookMu.RUnlock()
	for _, h := range hooks {
		result = h.PostToolUse(toolName, args, result, took, err)
	}
	return result
}

// ─── Default hooks shipped with the build ──────────────────────────────

// redactSecretsPreHook scrubs values that look like secrets out of tool
// arguments before they're sent to a tool implementation. Defence-in-depth:
// even if a model attempts to leak a real key through a tool argument
// (rare but observed), the tool never sees the raw value.
type redactSecretsPreHook struct{}

func (h *redactSecretsPreHook) Name() string { return "redact-secrets" }

// Patterns matched against any string-valued arg. Loose-enough to catch real
// keys, tight-enough to avoid false-positives on normal text. Order matters
// — the first match wins.
var redactionPatterns = []*regexp.Regexp{
	regexp.MustCompile(`\bAKIA[0-9A-Z]{16}\b`),                                  // AWS access key id
	regexp.MustCompile(`\bASIA[0-9A-Z]{16}\b`),                                  // AWS STS temp key id
	regexp.MustCompile(`\bABSK[A-Za-z0-9+/=]{40,}\b`),                           // Bedrock API key (this build's format)
	regexp.MustCompile(`\bsk-[A-Za-z0-9_-]{20,}\b`),                             // OpenAI/Anthropic-style
	regexp.MustCompile(`\bghp_[A-Za-z0-9]{30,}\b`),                              // GitHub PAT
	regexp.MustCompile(`\bey[A-Za-z0-9_-]+\.ey[A-Za-z0-9_-]+\.[A-Za-z0-9_-]+\b`), // JWT
}

func (h *redactSecretsPreHook) PreToolUse(toolName string, args map[string]interface{}) PreToolDecision {
	redactInArgs(args)
	return PreToolDecision{Allow: true}
}

func redactInArgs(args map[string]interface{}) {
	for k, v := range args {
		switch t := v.(type) {
		case string:
			args[k] = redactString(t)
		case map[string]interface{}:
			redactInArgs(t)
		case []interface{}:
			for i, item := range t {
				if s, ok := item.(string); ok {
					t[i] = redactString(s)
				} else if m, ok := item.(map[string]interface{}); ok {
					redactInArgs(m)
				}
			}
		}
	}
}

func redactString(s string) string {
	for _, p := range redactionPatterns {
		s = p.ReplaceAllString(s, "[REDACTED]")
	}
	return s
}

// auditLogPostHook emits one structured log line per tool invocation with
// duration and success/error status. Cheap, off by default could be added
// later if it gets noisy — for now it's just an INFO line.
type auditLogPostHook struct{}

func (h *auditLogPostHook) Name() string { return "audit-log" }

func (h *auditLogPostHook) PostToolUse(toolName string, args map[string]interface{}, result string, took time.Duration, err error) string {
	if err != nil {
		log.Printf("🪝 [HOOK-AUDIT] tool=%s duration=%s status=error err=%v result_len=%d",
			toolName, took, err, len(result))
	} else {
		log.Printf("🪝 [HOOK-AUDIT] tool=%s duration=%s status=ok result_len=%d",
			toolName, took, len(result))
	}
	return result
}

// init wires up the default hooks at package load. Code that wants to add
// custom hooks (e.g. denylist, slack-notify, file-path validator) calls
// RegisterPreToolHook / RegisterPostToolHook from its own init().
func init() {
	RegisterPreToolHook(&redactSecretsPreHook{})
	RegisterPostToolHook(&auditLogPostHook{})
}
