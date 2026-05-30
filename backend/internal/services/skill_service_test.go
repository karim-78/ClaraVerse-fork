// Tests for SkillService — covers the auto-routing scoring logic
// (skillTokenize + RouteMessage) which is one of the most behaviour-
// sensitive pieces of the system: changing a few weights here changes
// which skill (and therefore which tools + system prompt) a user gets
// for a given message.
//
// skillTokenize is a pure function — runs in the default unit test
// suite. RouteMessage hits Mongo + does an aggregation pipeline so its
// tests use the integration build tag and the live-Mongo helper.

package services

import (
	"strings"
	"testing"
)

// ----------------------------------------------------------------------
// skillTokenize — pure function, unit test (no Mongo, no build tag)
// ----------------------------------------------------------------------

func TestSkillTokenize_BasicSplitting(t *testing.T) {
	got := skillTokenize("Hello world")
	if len(got) != 2 || got[0] != "hello" || got[1] != "world" {
		t.Errorf("expected [hello world], got %v", got)
	}
}

func TestSkillTokenize_LowercasesEverything(t *testing.T) {
	got := skillTokenize("FOO Bar BAZ")
	for _, tok := range got {
		if tok != strings.ToLower(tok) {
			t.Errorf("token %q was not lowercased", tok)
		}
	}
}

func TestSkillTokenize_SplitsOnDashesUnderscoresSlashes(t *testing.T) {
	// `-`, `_`, `/` should split into separate tokens. This is why a skill
	// keyword "code review" matches a message "code-review please".
	got := skillTokenize("code-review_now/please")
	want := map[string]bool{"code": true, "review": true, "now": true, "please": true}
	if len(got) != len(want) {
		t.Fatalf("expected %d tokens, got %d (%v)", len(want), len(got), got)
	}
	for _, tok := range got {
		if !want[tok] {
			t.Errorf("unexpected token %q", tok)
		}
	}
}

func TestSkillTokenize_Deduplicates(t *testing.T) {
	got := skillTokenize("foo foo bar foo bar")
	if len(got) != 2 {
		t.Errorf("expected 2 unique tokens, got %d (%v)", len(got), got)
	}
}

func TestSkillTokenize_EmptyInput(t *testing.T) {
	got := skillTokenize("")
	if len(got) != 0 {
		t.Errorf("expected empty result, got %v", got)
	}
}

func TestSkillTokenize_DropsEmptyAfterSplit(t *testing.T) {
	// Adjacent separators (e.g. "foo--bar") would otherwise produce a
	// zero-length token in the slice.
	got := skillTokenize("foo--bar")
	for _, tok := range got {
		if tok == "" {
			t.Error("got empty token in result")
		}
	}
}
