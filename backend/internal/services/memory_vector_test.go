// Pure-function tests for the memory vector retrieval path.
//
// What's covered:
//   - CosineSimilarity correctness (identical, orthogonal, opposite,
//     dimension mismatch, empty, zero-norm)
//   - buildQueryTextFromMessages — most-recent-user-first ordering,
//     length cap at 2000 chars, system/tool role exclusion, empty input,
//     boundary truncation
//
// These are the fundamental building blocks of memory retrieval — if
// CosineSimilarity is wrong, every "search_memory" call returns garbage
// rankings; if buildQueryTextFromMessages drops the wrong messages, the
// query vector doesn't represent the user's actual intent and we recall
// irrelevant memories.
//
// Unit tests (no Mongo, no build tag) — run with `go test ./internal/services/`.

package services

import (
	"math"
	"strings"
	"testing"
)

// ----------------------------------------------------------------------
// CosineSimilarity
// ----------------------------------------------------------------------

func TestCosineSimilarity_IdenticalVectors(t *testing.T) {
	a := []float32{1, 2, 3, 4}
	got := CosineSimilarity(a, a)
	if math.Abs(got-1.0) > 1e-9 {
		t.Errorf("identical vectors should give 1.0, got %v", got)
	}
}

func TestCosineSimilarity_OrthogonalVectors(t *testing.T) {
	a := []float32{1, 0, 0}
	b := []float32{0, 1, 0}
	got := CosineSimilarity(a, b)
	if math.Abs(got) > 1e-9 {
		t.Errorf("orthogonal vectors should give 0, got %v", got)
	}
}

func TestCosineSimilarity_OppositeVectors(t *testing.T) {
	a := []float32{1, 2, 3}
	b := []float32{-1, -2, -3}
	got := CosineSimilarity(a, b)
	if math.Abs(got-(-1.0)) > 1e-9 {
		t.Errorf("opposite vectors should give -1.0, got %v", got)
	}
}

func TestCosineSimilarity_ParallelButDifferentMagnitude(t *testing.T) {
	// Cosine is magnitude-invariant.
	a := []float32{1, 2, 3}
	b := []float32{2, 4, 6}
	got := CosineSimilarity(a, b)
	if math.Abs(got-1.0) > 1e-9 {
		t.Errorf("parallel vectors should give 1.0 regardless of magnitude, got %v", got)
	}
}

func TestCosineSimilarity_DimensionMismatchReturnsZero(t *testing.T) {
	// We don't want a partial-overlap match silently degrading retrieval —
	// a dimension mismatch is a programming error and should produce a
	// known-bad score (0) so it ranks last and is visible in any audit.
	a := []float32{1, 2, 3}
	b := []float32{1, 2}
	got := CosineSimilarity(a, b)
	if got != 0 {
		t.Errorf("dimension mismatch should give 0, got %v", got)
	}
}

func TestCosineSimilarity_EmptyVectorReturnsZero(t *testing.T) {
	got := CosineSimilarity([]float32{}, []float32{1, 2, 3})
	if got != 0 {
		t.Errorf("empty vector should give 0, got %v", got)
	}
}

func TestCosineSimilarity_ZeroNormReturnsZero(t *testing.T) {
	// All-zero vector has zero norm; dividing by zero would be NaN. We
	// return 0 instead so the ranking is deterministic.
	zero := []float32{0, 0, 0}
	other := []float32{1, 2, 3}
	got := CosineSimilarity(zero, other)
	if got != 0 {
		t.Errorf("zero vector should give 0, got %v", got)
	}
	got = CosineSimilarity(other, zero)
	if got != 0 {
		t.Errorf("zero vector (b side) should give 0, got %v", got)
	}
}

func TestCosineSimilarity_RealisticHighSimilarity(t *testing.T) {
	// Two vectors that differ slightly — expect high but not 1.0
	// similarity. Sanity check against drift.
	a := []float32{0.1, 0.5, 0.4, 0.2, 0.7}
	b := []float32{0.12, 0.48, 0.41, 0.19, 0.71}
	got := CosineSimilarity(a, b)
	if got < 0.99 || got > 1.0 {
		t.Errorf("expected near-identical vectors to score ~0.999, got %v", got)
	}
}

// ----------------------------------------------------------------------
// buildQueryTextFromMessages
// ----------------------------------------------------------------------

func TestBuildQueryTextFromMessages_EmptyInput(t *testing.T) {
	got := buildQueryTextFromMessages([]map[string]interface{}{})
	if got != "" {
		t.Errorf("empty input should give empty string, got %q", got)
	}
}

func TestBuildQueryTextFromMessages_PreservesNewestFirst(t *testing.T) {
	// Iterates from the end of the slice → newest content appears at the
	// START of the output. Embedding gives most weight to the user's most
	// recent intent.
	msgs := []map[string]interface{}{
		{"role": "user", "content": "first message"},
		{"role": "assistant", "content": "first reply"},
		{"role": "user", "content": "latest message"},
	}
	got := buildQueryTextFromMessages(msgs)
	if !strings.HasPrefix(got, "latest message") {
		t.Errorf("expected newest first, got: %q", got)
	}
	if !strings.Contains(got, "first message") {
		t.Errorf("expected all messages included, missing 'first message': %q", got)
	}
}

func TestBuildQueryTextFromMessages_SkipsSystemAndToolRoles(t *testing.T) {
	msgs := []map[string]interface{}{
		{"role": "system", "content": "ignored system prompt"},
		{"role": "tool", "content": "ignored tool result"},
		{"role": "user", "content": "real user message"},
	}
	got := buildQueryTextFromMessages(msgs)
	if strings.Contains(got, "ignored") {
		t.Errorf("system/tool roles should be excluded, got: %q", got)
	}
	if !strings.Contains(got, "real user message") {
		t.Errorf("user message should be included: %q", got)
	}
}

func TestBuildQueryTextFromMessages_SkipsEmptyContent(t *testing.T) {
	msgs := []map[string]interface{}{
		{"role": "user", "content": "   "}, // whitespace-only
		{"role": "user", "content": "real content"},
	}
	got := buildQueryTextFromMessages(msgs)
	if strings.TrimSpace(got) != "real content" {
		t.Errorf("expected only the real content, got: %q", got)
	}
}

func TestBuildQueryTextFromMessages_RespectsLengthCap(t *testing.T) {
	// Embed messages totaling more than the 2000-char cap; output must
	// not exceed the cap.
	big := strings.Repeat("a", 1500)
	msgs := []map[string]interface{}{
		{"role": "user", "content": big},
		{"role": "user", "content": big},
		{"role": "user", "content": big},
	}
	got := buildQueryTextFromMessages(msgs)
	if len(got) > 2000 {
		t.Errorf("output exceeded 2000-char cap: got %d chars", len(got))
	}
	if len(got) < 1500 {
		t.Errorf("output too short — should pack at least one full message, got %d chars", len(got))
	}
}

func TestBuildQueryTextFromMessages_HandlesNonStringContent(t *testing.T) {
	// Defensive: an upstream caller might pass non-string content (e.g.
	// the OpenAI multimodal format). The builder should skip those
	// without panicking.
	msgs := []map[string]interface{}{
		{"role": "user", "content": []interface{}{map[string]string{"type": "image_url"}}},
		{"role": "user", "content": "plain text"},
	}
	got := buildQueryTextFromMessages(msgs)
	if !strings.Contains(got, "plain text") {
		t.Errorf("plain-text message should still be included: %q", got)
	}
}
