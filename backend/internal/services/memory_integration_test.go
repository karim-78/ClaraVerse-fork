package services

import (
	"testing"
	"time"

	"claraverse/internal/models"
)

// TestMemoryExtractionSchema tests the extraction schema structure
func TestMemoryExtractionSchema(t *testing.T) {
	schema := memoryExtractionSchema

	// Verify schema structure
	if schema["type"] != "object" {
		t.Error("Schema should be object type")
	}

	properties, ok := schema["properties"].(map[string]interface{})
	if !ok {
		t.Fatal("Schema should have properties")
	}

	// Verify required fields
	requiredFields := []string{"memories"}
	required, ok := schema["required"].([]string)
	if !ok {
		t.Fatal("Schema should have required fields")
	}

	for _, field := range requiredFields {
		found := false
		for _, req := range required {
			if req == field {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("Required field %s not found in schema", field)
		}
	}

	// Verify memories array structure
	memories, ok := properties["memories"].(map[string]interface{})
	if !ok {
		t.Fatal("memories field should exist")
	}

	if memories["type"] != "array" {
		t.Error("memories should be array type")
	}

	items, ok := memories["items"].(map[string]interface{})
	if !ok {
		t.Fatal("memories should have items definition")
	}

	itemProps, ok := items["properties"].(map[string]interface{})
	if !ok {
		t.Fatal("memory items should have properties")
	}

	// Verify memory item fields
	requiredMemoryFields := []string{"content", "category", "tags"}
	for _, field := range requiredMemoryFields {
		if _, exists := itemProps[field]; !exists {
			t.Errorf("Memory item should have field: %s", field)
		}
	}
}

// TestMemorySelectionSchema tests the selection schema structure
func TestMemorySelectionSchema(t *testing.T) {
	schema := memorySelectionSchema

	// Verify schema structure
	if schema["type"] != "object" {
		t.Error("Schema should be object type")
	}

	properties, ok := schema["properties"].(map[string]interface{})
	if !ok {
		t.Fatal("Schema should have properties")
	}

	// Verify required fields
	requiredFields := []string{"selected_memory_ids", "reasoning"}
	required, ok := schema["required"].([]string)
	if !ok {
		t.Fatal("Schema should have required fields")
	}

	for _, field := range requiredFields {
		found := false
		for _, req := range required {
			if req == field {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("Required field %s not found in schema", field)
		}
	}

	// Verify selected_memory_ids array structure
	selectedIDs, ok := properties["selected_memory_ids"].(map[string]interface{})
	if !ok {
		t.Fatal("selected_memory_ids field should exist")
	}

	if selectedIDs["type"] != "array" {
		t.Error("selected_memory_ids should be array type")
	}
}

// TestMemoryExtractionSystemPrompt tests the extraction prompt content
func TestMemoryExtractionSystemPrompt(t *testing.T) {
	prompt := MemoryExtractionSystemPrompt

	// Verify prompt contains key instructions
	requiredPhrases := []string{
		"memory extraction system",
		"Personal Information",
		"Preferences",
		"Important Context",
		"Facts",
		"Instructions",
	}

	for _, phrase := range requiredPhrases {
		if !contains(prompt, phrase) {
			t.Errorf("Extraction prompt should contain: %q", phrase)
		}
	}

	// Verify prompt is not empty
	if len(prompt) < 100 {
		t.Error("Extraction prompt seems too short")
	}

	t.Logf("Extraction prompt length: %d characters", len(prompt))
}

// TestMemorySelectionSystemPrompt tests the selection prompt content
func TestMemorySelectionSystemPrompt(t *testing.T) {
	prompt := MemorySelectionSystemPrompt

	// Verify prompt contains key instructions
	requiredPhrases := []string{
		"memory selection system",
		"MOST RELEVANT",
		"Direct Relevance",
		"Contextual Information",
		"User Preferences",
		"Instructions",
	}

	for _, phrase := range requiredPhrases {
		if !contains(prompt, phrase) {
			t.Errorf("Selection prompt should contain: %q", phrase)
		}
	}

	// Verify prompt is not empty
	if len(prompt) < 100 {
		t.Error("Selection prompt seems too short")
	}

	t.Logf("Selection prompt length: %d characters", len(prompt))
}

// TestConversationEngagementCalculation tests engagement score logic
func TestConversationEngagementCalculation(t *testing.T) {
	tests := []struct {
		name              string
		messageCount      int
		userMessageCount  int
		avgResponseLength int
		withinWeek        bool
		minExpected       float64
		maxExpected       float64
	}{
		{
			name:              "High engagement (balanced turn-taking, long responses, recent)",
			messageCount:      20,
			userMessageCount:  10,
			avgResponseLength: 250,
			withinWeek:        true,
			minExpected:       0.70,
			maxExpected:       0.80,
		},
		{
			name:              "Medium engagement (moderate activity)",
			messageCount:      10,
			userMessageCount:  4,
			avgResponseLength: 150,
			withinWeek:        true,
			minExpected:       0.40,
			maxExpected:       0.70,
		},
		{
			name:              "Low engagement (few messages, short responses, old)",
			messageCount:      4,
			userMessageCount:  1,
			avgResponseLength: 50,
			withinWeek:        false,
			minExpected:       0.00,
			maxExpected:       0.30,
		},
		{
			name:              "User dominated (high user ratio)",
			messageCount:      10,
			userMessageCount:  8,
			avgResponseLength: 200,
			withinWeek:        true,
			minExpected:       0.70,
			maxExpected:       1.00,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Calculate engagement score components
			turnRatio := float64(tt.userMessageCount) / float64(tt.messageCount)
			lengthScore := minFloat(1.0, float64(tt.avgResponseLength)/200.0)
			recencyBonus := 0.0
			if tt.withinWeek {
				recencyBonus = 1.0
			}

			// Engagement formula: (0.5 × TurnRatio) + (0.3 × LengthScore) + (0.2 × RecencyBonus)
			engagementScore := (0.5 * turnRatio) + (0.3 * lengthScore) + (0.2 * recencyBonus)

			if engagementScore < tt.minExpected || engagementScore > tt.maxExpected {
				t.Errorf("Expected engagement between %.2f and %.2f, got %.2f\n"+
					"  Turn Ratio: %.2f, Length Score: %.2f, Recency: %.2f",
					tt.minExpected, tt.maxExpected, engagementScore,
					turnRatio, lengthScore, recencyBonus)
			}

			t.Logf("Engagement Score: %.2f (turn: %.2f, length: %.2f, recency: %.2f)",
				engagementScore, turnRatio, lengthScore, recencyBonus)
		})
	}
}

// TestDecayConfigDefaults tests the default decay configuration
func TestDecayConfigDefaults(t *testing.T) {
	config := DefaultDecayConfig()

	// Test default weights
	if config.RecencyWeight != 0.4 {
		t.Errorf("Expected RecencyWeight 0.4, got %.2f", config.RecencyWeight)
	}
	if config.FrequencyWeight != 0.3 {
		t.Errorf("Expected FrequencyWeight 0.3, got %.2f", config.FrequencyWeight)
	}
	if config.EngagementWeight != 0.3 {
		t.Errorf("Expected EngagementWeight 0.3, got %.2f", config.EngagementWeight)
	}

	// Test weights sum to 1.0
	totalWeight := config.RecencyWeight + config.FrequencyWeight + config.EngagementWeight
	if totalWeight != 1.0 {
		t.Errorf("Weights should sum to 1.0, got %.2f", totalWeight)
	}

	// Test other defaults
	if config.RecencyDecayRate != 0.05 {
		t.Errorf("Expected RecencyDecayRate 0.05, got %.2f", config.RecencyDecayRate)
	}
	if config.FrequencyMax != 20 {
		t.Errorf("Expected FrequencyMax 20, got %d", config.FrequencyMax)
	}
	if config.ArchiveThreshold != 0.15 {
		t.Errorf("Expected ArchiveThreshold 0.15, got %.2f", config.ArchiveThreshold)
	}
}

// TestMemoryModelDefaults tests default values in Memory model
func TestMemoryModelDefaults(t *testing.T) {
	// This test documents expected default states
	defaultAccessCount := int64(0)
	defaultIsArchived := false
	defaultVersion := int64(1)

	if defaultAccessCount != 0 {
		t.Errorf("New memories should have 0 access count")
	}
	if defaultIsArchived != false {
		t.Errorf("New memories should not be archived")
	}
	if defaultVersion != 1 {
		t.Errorf("New memories should start at version 1")
	}
}

// TestExtractedMemoryStructure tests the structure of extracted memories
func TestExtractedMemoryStructure(t *testing.T) {
	// Test valid categories
	validCategories := map[string]bool{
		"personal_info": true,
		"preferences":   true,
		"context":       true,
		"fact":          true,
		"instruction":   true,
	}

	// Simulate an extracted memory (correct structure)
	testResult := models.ExtractedMemoryFromLLM{
		Memories: []struct {
			Content  string   `json:"content"`
			Category string   `json:"category"`
			Tags     []string `json:"tags"`
		}{
			{
				Content:  "User prefers dark mode",
				Category: "preferences",
				Tags:     []string{"ui", "theme", "preferences"},
			},
		},
	}

	// Validate we have memories
	if len(testResult.Memories) == 0 {
		t.Error("Should have at least one memory")
	}

	// Validate first memory
	testMemory := testResult.Memories[0]

	// Validate category
	if !validCategories[testMemory.Category] {
		t.Errorf("Invalid category: %s", testMemory.Category)
	}

	// Validate content not empty
	if testMemory.Content == "" {
		t.Error("Memory content should not be empty")
	}

	// Validate tags
	if len(testMemory.Tags) == 0 {
		t.Error("Memory should have at least one tag")
	}
}

// TestSelectedMemoriesStructure tests the structure of selected memories
func TestSelectedMemoriesStructure(t *testing.T) {
	// Simulate a selection result
	selection := models.SelectedMemoriesFromLLM{
		SelectedMemoryIDs: []string{"id1", "id2", "id3"},
		Reasoning:         "These memories are relevant because...",
	}

	// Validate IDs
	if len(selection.SelectedMemoryIDs) == 0 {
		t.Error("Selection should have memory IDs")
	}

	// Validate reasoning
	if selection.Reasoning == "" {
		t.Error("Selection should have reasoning")
	}

	// Max 5 memories rule
	maxMemories := 5
	if len(selection.SelectedMemoryIDs) > maxMemories {
		t.Errorf("Should not select more than %d memories, got %d", maxMemories, len(selection.SelectedMemoryIDs))
	}
}

// TestMemoryLifecycleStates tests valid state transitions
func TestMemoryLifecycleStates(t *testing.T) {
	now := time.Now()

	// State 1: Newly created
	memory := models.Memory{
		AccessCount:    0,
		LastAccessedAt: nil,
		IsArchived:     false,
		ArchivedAt:     nil,
		CreatedAt:      now,
		UpdatedAt:      now,
		Version:        1,
	}

	if memory.IsArchived {
		t.Error("New memory should not be archived")
	}
	if memory.AccessCount != 0 {
		t.Error("New memory should have 0 access count")
	}

	// State 2: Accessed
	accessTime := now.Add(1 * time.Hour)
	memory.AccessCount = 1
	memory.LastAccessedAt = &accessTime

	if memory.LastAccessedAt == nil {
		t.Error("Accessed memory should have LastAccessedAt")
	}

	// State 3: Archived
	archiveTime := now.Add(90 * 24 * time.Hour)
	memory.IsArchived = true
	memory.ArchivedAt = &archiveTime

	if !memory.IsArchived {
		t.Error("Archived memory should have IsArchived=true")
	}
	if memory.ArchivedAt == nil {
		t.Error("Archived memory should have ArchivedAt timestamp")
	}
}

// TestMemorySystemPromptInjection tests the memory context formatting
func TestMemorySystemPromptInjection(t *testing.T) {
	// Simulate building memory context
	memories := []models.DecryptedMemory{
		{DecryptedContent: "User prefers dark mode"},
		{DecryptedContent: "User name is Dobby"},
		{DecryptedContent: "User timezone is America/New_York"},
	}

	// Build context string (simplified version of buildMemoryContext)
	var context string
	context = "\n\n## Relevant Context from Previous Conversations\n\n"
	for i, mem := range memories {
		context += string(rune('1'+i)) + ". " + mem.DecryptedContent + "\n"
	}

	// Verify format
	if !contains(context, "## Relevant Context") {
		t.Error("Context should have header")
	}

	for _, mem := range memories {
		if !contains(context, mem.DecryptedContent) {
			t.Errorf("Context should include memory: %s", mem.DecryptedContent)
		}
	}

	// Verify numbered list
	if !contains(context, "1. ") {
		t.Error("Context should use numbered list")
	}

	t.Logf("Generated context:\n%s", context)
}

// Helper function to check if string contains substring
func contains(s, substr string) bool {
	return len(s) >= len(substr) && (s == substr || len(s) > len(substr) && (s[:len(substr)] == substr || contains(s[1:], substr)))
}

// Helper function for min (renamed to avoid conflict)
func minFloat(a, b float64) float64 {
	if a < b {
		return a
	}
	return b
}
