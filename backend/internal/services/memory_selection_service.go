package services

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"strings"
	"time"

	"claraverse/internal/crypto"
	"claraverse/internal/database"
	"claraverse/internal/models"
	"go.mongodb.org/mongo-driver/bson/primitive"
)

// MemorySelectionService handles selection of relevant memories. As of
// 2026-05 we run vector retrieval first (Bedrock Titan embeddings + cosine
// similarity) and only fall back to LLM-only selection when no embedding
// service is configured or the embed call fails. The vector path is
// cheaper (no token cost per turn) and scales past 50+ memories without
// degrading.
type MemorySelectionService struct {
	mongodb              *database.MongoDB
	encryptionService    *crypto.EncryptionService
	providerService      *ProviderService
	memoryStorageService *MemoryStorageService
	chatService          *ChatService
	settingsService      *SettingsService
	modelPool            *MemoryModelPool // Dynamic model pool with round-robin and failover
	embeddingService     *EmbeddingService
}

// Memory selection system prompt
const MemorySelectionSystemPrompt = `You are a memory selection system for Dobby AI. Given the user's recent conversation and their memory bank, select the MOST RELEVANT memories.

SELECTION CRITERIA:
1. **Direct Relevance**: Memory directly relates to current conversation topic
2. **Contextual Information**: Memory provides important background context
3. **User Preferences**: Memory contains preferences that affect how to respond
4. **Instructions**: Memory contains guidelines the user wants followed

RULES:
- Select up to 5 memories maximum (fewer is better if not all are relevant)
- Prioritize memories that prevent asking redundant questions
- Include memories that personalize the response
- Skip memories that are obvious or unrelated
- If no memories are relevant, return empty array

Return JSON with selected memory IDs and brief reasoning.`

// memorySelectionSchema defines structured output for memory selection
var memorySelectionSchema = map[string]interface{}{
	"type": "object",
	"properties": map[string]interface{}{
		"selected_memory_ids": map[string]interface{}{
			"type": "array",
			"items": map[string]interface{}{
				"type":        "string",
				"description": "Memory ID from the provided list",
			},
			"description": "IDs of memories relevant to current conversation (max 5)",
		},
		"reasoning": map[string]interface{}{
			"type":        "string",
			"description": "Brief explanation of why these memories are relevant",
		},
	},
	"required":             []string{"selected_memory_ids", "reasoning"},
	"additionalProperties": false,
}

// NewMemorySelectionService creates a new memory selection service
func NewMemorySelectionService(
	mongodb *database.MongoDB,
	encryptionService *crypto.EncryptionService,
	providerService *ProviderService,
	memoryStorageService *MemoryStorageService,
	chatService *ChatService,
	modelPool *MemoryModelPool,
) *MemorySelectionService {
	return &MemorySelectionService{
		mongodb:              mongodb,
		encryptionService:    encryptionService,
		providerService:      providerService,
		memoryStorageService: memoryStorageService,
		chatService:          chatService,
		modelPool:            modelPool,
	}
}

// SetSettingsService sets the settings service for system-wide model assignments
// buildQueryTextFromMessages concatenates the content of the most recent
// user-side messages (skipping tool/system noise) into a single string for
// embedding. We weight toward user turns because the model's own replies
// are already shaped by selected memories — embedding them would create a
// drift loop. Cap at ~2000 chars to keep Titan happy.
func buildQueryTextFromMessages(messages []map[string]interface{}) string {
	if len(messages) == 0 {
		return ""
	}
	const maxLen = 2000
	var b strings.Builder
	for i := len(messages) - 1; i >= 0 && b.Len() < maxLen; i-- {
		role, _ := messages[i]["role"].(string)
		if role != "user" && role != "assistant" {
			continue
		}
		content, _ := messages[i]["content"].(string)
		content = strings.TrimSpace(content)
		if content == "" {
			continue
		}
		if b.Len() > 0 {
			b.WriteString("\n")
		}
		if b.Len()+len(content) > maxLen {
			b.WriteString(content[:maxLen-b.Len()])
			break
		}
		b.WriteString(content)
	}
	return strings.TrimSpace(b.String())
}

// SetEmbeddingService wires the embedding service for the vector-first
// selection path. Called from main.go after all services are constructed.
func (s *MemorySelectionService) SetEmbeddingService(emb *EmbeddingService) {
	s.embeddingService = emb
	if emb != nil {
		log.Printf("📐 [MEMORY-SELECTION] Embedding service attached — vector retrieval enabled")
	}
}

// HasEmbedding reports whether vector retrieval is wired and reachable.
// Used by the chat-side memory tools (search_memory) so they can prefer
// the vector path when available and fall back to score-ordering otherwise.
func (s *MemorySelectionService) HasEmbedding(ctx context.Context) bool {
	return s.embeddingService != nil && s.embeddingService.Available(ctx)
}

// EmbedQuery is a small passthrough so callers (currently the chat
// service's SearchMemory) don't need to hold their own EmbeddingService
// reference. Returns the raw embedding vector.
func (s *MemorySelectionService) EmbedQuery(ctx context.Context, query string) ([]float32, error) {
	if s.embeddingService == nil {
		return nil, fmt.Errorf("embedding service not configured")
	}
	return s.embeddingService.Embed(ctx, query)
}

func (s *MemorySelectionService) SetSettingsService(settingsService *SettingsService) {
	s.settingsService = settingsService
	log.Printf("✅ [MEMORY-SELECTION] Settings service set for system model assignment")
}

// SelectRelevantMemories selects memories relevant to the current conversation.
//
// Strategy (priority order):
//  1. **Vector retrieval** (if embedding service available): embed the last
//     ~6 messages joined together, cosine-similarity against all active
//     memories, take top-K. Cheap, fast, scales.
//  2. **LLM selection** (fallback): the original path — ship every active
//     memory to a selector model and ask it to pick. Slower + token-cost,
//     but works without embeddings.
//  3. **Top-N by score** (final fallback): pure score ordering. Lowest
//     quality but always returns something useful.
func (s *MemorySelectionService) SelectRelevantMemories(
	ctx context.Context,
	userID string,
	recentMessages []map[string]interface{},
	maxMemories int,
) ([]models.DecryptedMemory, error) {

	// Get all active memories for user
	activeMemories, err := s.memoryStorageService.GetActiveMemories(ctx, userID)
	if err != nil {
		return nil, fmt.Errorf("failed to get active memories: %w", err)
	}

	// If no memories, return empty
	if len(activeMemories) == 0 {
		log.Printf("📭 [MEMORY-SELECTION] No active memories for user %s", userID)
		return []models.DecryptedMemory{}, nil
	}

	log.Printf("🔍 [MEMORY-SELECTION] Selecting from %d active memories for user %s", len(activeMemories), userID)

	// If we have fewer memories than max, just return all and update access
	if len(activeMemories) <= maxMemories {
		log.Printf("📚 [MEMORY-SELECTION] Returning all %d memories (below max %d)", len(activeMemories), maxMemories)
		memoryIDs := make([]primitive.ObjectID, len(activeMemories))
		for i, mem := range activeMemories {
			memoryIDs[i] = mem.ID
		}
		s.memoryStorageService.UpdateMemoryAccess(ctx, memoryIDs)
		return activeMemories, nil
	}

	// 1. Vector retrieval path — fast, no token cost.
	if s.embeddingService != nil && s.embeddingService.Available(ctx) {
		queryText := buildQueryTextFromMessages(recentMessages)
		if queryText != "" {
			qVec, embErr := s.embeddingService.Embed(ctx, queryText)
			if embErr == nil {
				hits, sErr := s.memoryStorageService.SearchByEmbedding(ctx, userID, qVec, maxMemories)
				if sErr == nil && len(hits) > 0 {
					selected := make([]models.DecryptedMemory, 0, len(hits))
					ids := make([]primitive.ObjectID, 0, len(hits))
					for _, h := range hits {
						// Drop zero-similarity hits unless we have nothing
						// else. A zero-sim hit means the memory has no
						// embedding (legacy) — it shouldn't displace real
						// matches when real ones exist.
						if h.Similarity > 0 || len(selected) == 0 {
							selected = append(selected, h.Memory)
							ids = append(ids, h.Memory.ID)
						}
					}
					s.memoryStorageService.UpdateMemoryAccess(ctx, ids)
					log.Printf("🎯 [MEMORY-SELECTION] Vector-selected %d memories (top sim=%.3f)",
						len(selected), hits[0].Similarity)
					return selected, nil
				}
				log.Printf("⚠️ [MEMORY-SELECTION] Vector search failed (%v) — falling back to LLM selection", sErr)
			} else {
				log.Printf("⚠️ [MEMORY-SELECTION] Embedding failed (%v) — falling back to LLM selection", embErr)
			}
		}
	}

	// 2. LLM selection path (fallback).
	selectedIDs, reasoning, err := s.selectMemoriesWithLLM(ctx, userID, activeMemories, recentMessages, maxMemories)
	if err != nil {
		log.Printf("⚠️ [MEMORY-SELECTION] LLM selection failed: %v, falling back to top %d by score", err, maxMemories)
		// Fallback: return top N by score
		selectedMemories := activeMemories
		if len(selectedMemories) > maxMemories {
			selectedMemories = selectedMemories[:maxMemories]
		}
		memoryIDs := make([]primitive.ObjectID, len(selectedMemories))
		for i, mem := range selectedMemories {
			memoryIDs[i] = mem.ID
		}
		s.memoryStorageService.UpdateMemoryAccess(ctx, memoryIDs)
		return selectedMemories, nil
	}

	log.Printf("🎯 [MEMORY-SELECTION] LLM selected %d memories: %s", len(selectedIDs), reasoning)

	// Filter memories by selected IDs
	selectedMemories := s.filterMemoriesByIDs(activeMemories, selectedIDs)

	// Update access counts and timestamps
	memoryIDs := make([]primitive.ObjectID, len(selectedMemories))
	for i, mem := range selectedMemories {
		memoryIDs[i] = mem.ID
	}
	if len(memoryIDs) > 0 {
		s.memoryStorageService.UpdateMemoryAccess(ctx, memoryIDs)
	}

	return selectedMemories, nil
}

// selectMemoriesWithLLM uses LLM to select relevant memories with automatic failover
func (s *MemorySelectionService) selectMemoriesWithLLM(
	ctx context.Context,
	userID string,
	memories []models.DecryptedMemory,
	recentMessages []map[string]interface{},
	maxMemories int,
) ([]string, string, error) {

	// Check if user has a custom selector model preference
	userPreferredModel, err := s.getSelectorModelForUser(ctx, userID)
	var selectorModelID string

	if err == nil && userPreferredModel != "" {
		// User has a preference, use it
		selectorModelID = userPreferredModel
		log.Printf("👤 [MEMORY-SELECTION] Using user-preferred model: %s", selectorModelID)
	} else {
		// No user preference, get from model pool
		selectorModelID, err = s.modelPool.GetNextSelector()
		if err != nil {
			return nil, "", fmt.Errorf("no selector models available: %w", err)
		}
	}

	// Try selection with automatic failover (max 3 attempts)
	maxAttempts := 3
	var lastError error

	for attempt := 1; attempt <= maxAttempts; attempt++ {
		selectedIDs, reasoning, err := s.trySelection(ctx, selectorModelID, memories, recentMessages, maxMemories)

		if err == nil {
			// Success!
			s.modelPool.MarkSuccess(selectorModelID)
			return selectedIDs, reasoning, nil
		}

		// Selection failed
		lastError = err
		s.modelPool.MarkFailure(selectorModelID)
		log.Printf("⚠️ [MEMORY-SELECTION] Attempt %d/%d failed with model %s: %v",
			attempt, maxAttempts, selectorModelID, err)

		// If not last attempt, get next model from pool
		if attempt < maxAttempts {
			selectorModelID, err = s.modelPool.GetNextSelector()
			if err != nil {
				return nil, "", fmt.Errorf("no more selectors available after %d attempts: %w", attempt, err)
			}
			log.Printf("🔄 [MEMORY-SELECTION] Retrying with next model: %s", selectorModelID)
		}
	}

	return nil, "", fmt.Errorf("selection failed after %d attempts, last error: %w", maxAttempts, lastError)
}

// trySelection attempts selection with a specific model (internal helper)
func (s *MemorySelectionService) trySelection(
	ctx context.Context,
	selectorModelID string,
	memories []models.DecryptedMemory,
	recentMessages []map[string]interface{},
	maxMemories int,
) ([]string, string, error) {

	// Get provider and model
	provider, actualModel, err := s.getProviderAndModel(selectorModelID)
	if err != nil {
		return nil, "", fmt.Errorf("failed to get provider for selector: %w", err)
	}

	log.Printf("🤖 [MEMORY-SELECTION] Using model: %s (%s)", selectorModelID, actualModel)

	// Build conversation context
	conversationContext := s.buildConversationContext(recentMessages)

	// Build memory list for prompt
	memoryList := s.buildMemoryListPrompt(memories)

	// Build user prompt
	userPrompt := fmt.Sprintf(`RECENT CONVERSATION:
%s

MEMORY BANK (%d memories):
%s

Select up to %d memories that are DIRECTLY relevant to the current conversation. Return JSON with selected memory IDs and reasoning.`,
		conversationContext, len(memories), memoryList, maxMemories)

	// Build messages
	llmMessages := []map[string]interface{}{
		{
			"role":    "system",
			"content": MemorySelectionSystemPrompt,
		},
		{
			"role":    "user",
			"content": userPrompt,
		},
	}

	// Build request with structured output
	requestBody := map[string]interface{}{
		"model":       actualModel,
		"messages":    llmMessages,
		"stream":      false,
		"temperature": 0.2, // Low temp for consistency
		"response_format": map[string]interface{}{
			"type": "json_schema",
			"json_schema": map[string]interface{}{
				"name":   "memory_selection",
				"strict": true,
				"schema": memorySelectionSchema,
			},
		},
	}

	reqBody, err := json.Marshal(requestBody)
	if err != nil {
		return nil, "", fmt.Errorf("failed to marshal request: %w", err)
	}

	// Create HTTP request with timeout
	httpReq, err := http.NewRequestWithContext(ctx, "POST", provider.BaseURL+"/chat/completions", bytes.NewBuffer(reqBody))
	if err != nil {
		return nil, "", fmt.Errorf("failed to create request: %w", err)
	}

	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("Authorization", "Bearer "+provider.APIKey)

	// Send request with 30s timeout
	client := &http.Client{Timeout: 30 * time.Second}
	resp, err := client.Do(httpReq)
	if err != nil {
		return nil, "", fmt.Errorf("request failed: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, "", fmt.Errorf("failed to read response: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		log.Printf("⚠️ [MEMORY-SELECTION] API error: %s", string(body))
		return nil, "", fmt.Errorf("API error (status %d): %s", resp.StatusCode, string(body))
	}

	// Parse response
	var apiResponse struct {
		Choices []struct {
			Message struct {
				Content string `json:"content"`
			} `json:"message"`
		} `json:"choices"`
	}

	if err := json.Unmarshal(body, &apiResponse); err != nil {
		return nil, "", fmt.Errorf("failed to parse API response: %w", err)
	}

	if len(apiResponse.Choices) == 0 {
		return nil, "", fmt.Errorf("no response from selector model")
	}

	// Parse the selection result
	var result models.SelectedMemoriesFromLLM
	content := extractJSONFromLLM(apiResponse.Choices[0].Message.Content)

	if err := json.Unmarshal([]byte(content), &result); err != nil {
		// SECURITY: Don't log decrypted memory content - only log length
		log.Printf("⚠️ [MEMORY-SELECTION] Failed to parse selection: %v (response length: %d bytes)", err, len(content))
		return nil, "", fmt.Errorf("failed to parse selection: %w", err)
	}

	return result.SelectedMemoryIDs, result.Reasoning, nil
}

// buildConversationContext builds a concise context from recent messages
func (s *MemorySelectionService) buildConversationContext(messages []map[string]interface{}) string {
	var builder strings.Builder

	// Only include last 10 messages to keep prompt concise
	startIdx := len(messages) - 10
	if startIdx < 0 {
		startIdx = 0
	}

	for _, msg := range messages[startIdx:] {
		role, _ := msg["role"].(string)
		content, _ := msg["content"].(string)

		// Skip system messages
		if role == "system" {
			continue
		}

		// Format message
		if role == "user" {
			builder.WriteString(fmt.Sprintf("USER: %s\n", content))
		} else if role == "assistant" {
			// Truncate long assistant messages
			if len(content) > 200 {
				content = content[:200] + "..."
			}
			builder.WriteString(fmt.Sprintf("ASSISTANT: %s\n", content))
		}
	}

	return builder.String()
}

// buildMemoryListPrompt creates a numbered list of memories for the prompt
func (s *MemorySelectionService) buildMemoryListPrompt(memories []models.DecryptedMemory) string {
	var builder strings.Builder

	for i, mem := range memories {
		builder.WriteString(fmt.Sprintf("%d. [ID: %s] [Category: %s] %s\n",
			i+1,
			mem.ID.Hex(),
			mem.Category,
			mem.DecryptedContent,
		))
	}

	return builder.String()
}

// filterMemoriesByIDs filters memories to only include selected IDs
func (s *MemorySelectionService) filterMemoriesByIDs(
	memories []models.DecryptedMemory,
	selectedIDs []string,
) []models.DecryptedMemory {

	// Build set for O(1) lookup
	idSet := make(map[string]bool)
	for _, id := range selectedIDs {
		idSet[id] = true
	}

	filtered := make([]models.DecryptedMemory, 0, len(selectedIDs))

	for _, mem := range memories {
		if idSet[mem.ID.Hex()] {
			filtered = append(filtered, mem)
		}
	}

	return filtered
}

// getSelectorModelForUser gets user's preferred selector model
func (s *MemorySelectionService) getSelectorModelForUser(ctx context.Context, userID string) (string, error) {
	// Check system-wide setting first (admin override)
	if s.settingsService != nil {
		assignments, err := s.settingsService.GetSystemModelAssignments(ctx)
		if err == nil && assignments.MemoryExtractor != "" {
			log.Printf("🎯 [MEMORY-SELECTION] Using system-assigned model: %s", assignments.MemoryExtractor)
			return assignments.MemoryExtractor, nil
		}
	}

	// Fall back to user preference
	// Query MongoDB for user preferences
	usersCollection := s.mongodb.Collection(database.CollectionUsers)

	var user models.User
	err := usersCollection.FindOne(ctx, map[string]interface{}{"supabaseUserId": userID}).Decode(&user)
	if err != nil {
		return "", nil // No user found, return empty (will use pool)
	}

	// If user has a preference and it's not empty, use it
	if user.Preferences.MemorySelectorModelID != "" {
		log.Printf("🎯 [MEMORY-SELECTION] Using user-preferred model: %s", user.Preferences.MemorySelectorModelID)
		return user.Preferences.MemorySelectorModelID, nil
	}

	// No preference set - return empty (will use pool)
	return "", nil
}

// getProviderAndModel resolves model ID to provider and actual model name
func (s *MemorySelectionService) getProviderAndModel(modelID string) (*models.Provider, string, error) {
	if modelID == "" {
		return nil, "", fmt.Errorf("model ID is required")
	}

	// Try to resolve through ChatService (handles aliases)
	if s.chatService != nil {
		if provider, actualModel, found := s.chatService.ResolveModelAlias(modelID); found {
			return provider, actualModel, nil
		}
	}

	return nil, "", fmt.Errorf("model %s not found in providers", modelID)
}
