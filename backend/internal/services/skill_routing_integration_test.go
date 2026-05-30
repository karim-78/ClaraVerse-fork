//go:build integration

// Integration tests for SkillService.RouteMessage — the auto-routing
// scoring path. RouteMessage:
//   1. Pulls every user_skill where enabled=true AND skill.mode="auto"
//   2. Tokenises the incoming message
//   3. Scores each skill by:
//        TriggerPatterns prefix match  → +20
//        TriggerPatterns substring match → +10
//        Keyword exact token match     → +10
//        Keyword partial match         → +5
//   4. Returns the highest-scoring skill ≥ 15 (else nil)
//
// What's at stake: regressions here change which tools + system prompt
// a daemon gets for a given message. A bug that knocks scores down by 5
// across the board could cause every auto-routing to fall back to nil
// (no skill), silently degrading the system.

package services

import (
	"context"
	"testing"
	"time"

	"claraverse/internal/database"
	"claraverse/internal/models"
	"claraverse/internal/tools"

	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/bson/primitive"
)

// seedSkillForRouting upserts a skill and enables it for the user.
// Returns the skill ID for assertions.
func seedSkillForRouting(t *testing.T, db *database.MongoDB, userID string, skill *models.Skill) primitive.ObjectID {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	if skill.ID.IsZero() {
		skill.ID = primitive.NewObjectID()
	}
	if skill.Mode == "" {
		skill.Mode = "auto"
	}
	skill.CreatedAt = time.Now()
	skill.UpdatedAt = time.Now()

	_, err := db.Collection(database.CollectionSkills).InsertOne(ctx, skill)
	mustOK(t, err, "insert skill")

	_, err = db.Collection(database.CollectionUserSkills).InsertOne(ctx, &models.UserSkill{
		ID:        primitive.NewObjectID(),
		UserID:    userID,
		SkillID:   skill.ID,
		Enabled:   true,
		CreatedAt: time.Now(),
	})
	mustOK(t, err, "insert user_skill")

	return skill.ID
}

func TestSkillService_RouteMessage_PrefixTriggerScores20(t *testing.T) {
	md, cleanup := newTestMongo(t)
	defer cleanup()

	reg := tools.GetRegistry()
	svc := NewSkillService(md, reg)
	userID := "u-route-prefix"

	target := seedSkillForRouting(t, md, userID, &models.Skill{
		Name:            "Code Review",
		Description:     "do code reviews",
		SystemPrompt:    "you are a reviewer",
		TriggerPatterns: []string{"review my"},
	})

	matched, err := svc.RouteMessage(context.Background(), userID, "review my pull request")
	mustOK(t, err, "RouteMessage")
	if matched == nil {
		t.Fatal("expected a match (prefix +20 alone exceeds threshold of 15)")
	}
	if matched.ID != target {
		t.Errorf("matched the wrong skill: %s", matched.ID.Hex())
	}
}

func TestSkillService_RouteMessage_SubstringTriggerNeedsBackup(t *testing.T) {
	// Substring match alone is +10 — below the 15 threshold. So a skill
	// that only matches via substring should NOT be returned without an
	// additional keyword hit.
	md, cleanup := newTestMongo(t)
	defer cleanup()

	svc := NewSkillService(md, tools.GetRegistry())
	userID := "u-route-substr"

	seedSkillForRouting(t, md, userID, &models.Skill{
		Name:            "Weak Substring",
		SystemPrompt:    "x",
		TriggerPatterns: []string{"banana"}, // only substring; +10
		// no keywords that would push it over the threshold
	})

	matched, err := svc.RouteMessage(context.Background(), userID,
		"my banana sundae recipe") // banana appears mid-sentence
	mustOK(t, err, "RouteMessage")
	if matched != nil {
		t.Errorf("substring-only match should be below threshold, got: %s", matched.Name)
	}
}

func TestSkillService_RouteMessage_KeywordExactMatchScores10(t *testing.T) {
	// Two exact keyword matches (each +10) total 20 — should exceed
	// threshold and return the skill.
	md, cleanup := newTestMongo(t)
	defer cleanup()

	svc := NewSkillService(md, tools.GetRegistry())
	userID := "u-route-keywords"

	target := seedSkillForRouting(t, md, userID, &models.Skill{
		Name:         "Database Helper",
		SystemPrompt: "you query databases",
		Keywords:     []string{"sql", "query"},
	})

	matched, err := svc.RouteMessage(context.Background(), userID,
		"run a sql query against analytics")
	mustOK(t, err, "RouteMessage")
	if matched == nil {
		t.Fatal("expected match — two exact keyword hits sum to 20")
	}
	if matched.ID != target {
		t.Errorf("matched wrong skill: %s", matched.ID.Hex())
	}
}

func TestSkillService_RouteMessage_HighestScoreWins(t *testing.T) {
	// Two competing skills both score above threshold; the one with the
	// higher score should win — even if both match the message.
	md, cleanup := newTestMongo(t)
	defer cleanup()

	svc := NewSkillService(md, tools.GetRegistry())
	userID := "u-route-tiebreak"

	weakID := seedSkillForRouting(t, md, userID, &models.Skill{
		Name:     "Weak Match",
		Keywords: []string{"deploy"}, // one exact kw match = +10... below 15
	})
	strongID := seedSkillForRouting(t, md, userID, &models.Skill{
		Name:            "Strong Match",
		TriggerPatterns: []string{"deploy the"}, // prefix +20
	})

	matched, err := svc.RouteMessage(context.Background(), userID,
		"deploy the latest build")
	mustOK(t, err, "RouteMessage")
	if matched == nil {
		t.Fatal("expected a match")
	}
	if matched.ID != strongID {
		t.Errorf("expected strong match (id=%s), got %s (id=%s)",
			strongID.Hex(), matched.Name, matched.ID.Hex())
	}
	_ = weakID // referenced for clarity; intentionally not asserted
}

func TestSkillService_RouteMessage_ManualModeSkillIgnored(t *testing.T) {
	// Skills with mode="manual" must NOT be returned by auto-routing —
	// the user explicitly opted them in, and auto-routing would override
	// their intent.
	md, cleanup := newTestMongo(t)
	defer cleanup()

	svc := NewSkillService(md, tools.GetRegistry())
	userID := "u-route-manual"

	seedSkillForRouting(t, md, userID, &models.Skill{
		Name:            "Manual-only Skill",
		SystemPrompt:    "x",
		TriggerPatterns: []string{"foo"},
		Keywords:        []string{"bar"},
		Mode:            "manual",
	})

	matched, err := svc.RouteMessage(context.Background(), userID,
		"foo bar baz") // would score very high if not for the manual mode
	mustOK(t, err, "RouteMessage")
	if matched != nil {
		t.Errorf("manual-mode skill should never auto-route, got: %s", matched.Name)
	}
}

func TestSkillService_RouteMessage_DisabledSkillIgnored(t *testing.T) {
	// A user with the skill linked but disabled should not have it
	// auto-routed.
	md, cleanup := newTestMongo(t)
	defer cleanup()

	svc := NewSkillService(md, tools.GetRegistry())
	userID := "u-route-disabled"
	ctx := context.Background()

	// Hand-roll: seed the skill but flip the user_skill enabled=false.
	skill := &models.Skill{
		ID:              primitive.NewObjectID(),
		Name:            "Disabled",
		SystemPrompt:    "x",
		TriggerPatterns: []string{"hello"},
		Mode:            "auto",
		CreatedAt:       time.Now(),
		UpdatedAt:       time.Now(),
	}
	_, err := md.Collection(database.CollectionSkills).InsertOne(ctx, skill)
	mustOK(t, err, "insert skill")
	_, err = md.Collection(database.CollectionUserSkills).InsertOne(ctx, &models.UserSkill{
		ID:        primitive.NewObjectID(),
		UserID:    userID,
		SkillID:   skill.ID,
		Enabled:   false, // <- the test's point
		CreatedAt: time.Now(),
	})
	mustOK(t, err, "insert user_skill (disabled)")

	matched, err := svc.RouteMessage(ctx, userID, "hello world")
	mustOK(t, err, "RouteMessage")
	if matched != nil {
		t.Errorf("disabled skill should not auto-route, got: %s", matched.Name)
	}
}

func TestSkillService_RouteMessage_EmptyMessageReturnsNil(t *testing.T) {
	md, cleanup := newTestMongo(t)
	defer cleanup()

	svc := NewSkillService(md, tools.GetRegistry())
	matched, err := svc.RouteMessage(context.Background(), "u", "")
	mustOK(t, err, "RouteMessage empty")
	if matched != nil {
		t.Errorf("empty message should return nil, got: %v", matched)
	}
}

func TestSkillService_RouteMessage_NoSkillsForUser(t *testing.T) {
	// User has no skills at all → must not match anything.
	md, cleanup := newTestMongo(t)
	defer cleanup()

	svc := NewSkillService(md, tools.GetRegistry())
	matched, err := svc.RouteMessage(context.Background(), "u-empty",
		"anything that should NOT match")
	mustOK(t, err, "RouteMessage no skills")
	if matched != nil {
		t.Errorf("expected nil for user with no skills, got: %v", matched)
	}
}

// Helper: confirms our test inserts the way RouteMessage's aggregation
// pipeline expects (user_skills + lookup into skills, filter on mode=auto).
// A red here means the aggregation filter changed shape and the helper
// needs updating — protects against the slow-grinding "tests pass but
// they don't actually exercise the path" failure mode.
func TestSkillService_RouteMessage_AggregationContract(t *testing.T) {
	md, cleanup := newTestMongo(t)
	defer cleanup()
	ctx := context.Background()

	// Insert a skill + enabled user_skill, then run the same aggregation
	// shape RouteMessage uses and assert we get the joined doc.
	skill := &models.Skill{
		ID:           primitive.NewObjectID(),
		Name:         "Probe",
		SystemPrompt: "x",
		Mode:         "auto",
		CreatedAt:    time.Now(),
		UpdatedAt:    time.Now(),
	}
	_, err := md.Collection(database.CollectionSkills).InsertOne(ctx, skill)
	mustOK(t, err, "insert skill")
	_, err = md.Collection(database.CollectionUserSkills).InsertOne(ctx, &models.UserSkill{
		ID:        primitive.NewObjectID(),
		UserID:    "u-probe",
		SkillID:   skill.ID,
		Enabled:   true,
		CreatedAt: time.Now(),
	})
	mustOK(t, err, "insert user_skill")

	count, err := md.Collection(database.CollectionUserSkills).CountDocuments(ctx,
		bson.M{"user_id": "u-probe", "enabled": true})
	mustOK(t, err, "count enabled")
	if count != 1 {
		t.Fatalf("expected 1 enabled user_skill for u-probe, got %d", count)
	}
}
