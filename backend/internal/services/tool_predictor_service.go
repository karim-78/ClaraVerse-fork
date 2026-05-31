package services

import (
	"bytes"
	"claraverse/internal/database"
	"claraverse/internal/health"
	"claraverse/internal/models"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"strings"
	"sync"
	"time"
)

// routerModelCandidate represents a smart_tool_router model with speed info
type routerModelCandidate struct {
	ModelID      string
	ModelName    string // actual model name for API call
	ProviderID   int
	ProviderName string
	SpeedMs      int
}

// ToolPredictorService handles dynamic tool selection for chat requests
type ToolPredictorService struct {
	db                    *database.DB
	providerService       *ProviderService
	chatService           *ChatService
	userService           *UserService
	settingsService       *SettingsService
	healthService         *health.Service
	redisService          *RedisService
	routerModels          []routerModelCandidate
	routerIndex           int
	mu                    sync.Mutex
}

const (
	toolCacheKeyPrefix = "chat:tools:"
	toolCacheTTL       = 2 * time.Hour
)

// ToolPredictionResult represents selected tools from predictor
type ToolPredictionResult struct {
	SelectedTools []string `json:"selected_tools"` // Array of tool names
	Reasoning     string   `json:"reasoning"`
}

// Tool prediction system prompt (adapted from WorkflowGeneratorV2)
const ToolPredictionSystemPrompt = `You are a tool selection expert for ClaraVerse chat system. Analyze the user's message and select the MINIMUM set of tools needed to respond effectively.

CRITICAL RULES:
- Select ONLY tools that are DIRECTLY needed for THIS specific request
- Most requests need 0-3 tools. Rarely should you select more than 5 tools
- If no tools are needed (general conversation, advice, explanation), return empty array
- Don't over-select "just in case" - be precise and minimal

WHEN TO SELECT TOOLS:
- Search tools: User asks for current info, news, research, "look up", "search for"
- Time tools: User asks "what time", "current date", mentions time-sensitive info
- File tools: User mentions reading/processing files (CSV, PDF, etc.)
- Communication tools: User wants to send message to specific platform (Discord, Slack, email)
- Calculation tools: Complex math, data analysis
- API tools: Interacting with specific services (GitHub, Jira, etc.)

WHEN NOT TO SELECT TOOLS:
- General questions, explanations, advice, brainstorming
- Coding help (unless explicitly needs to search docs/internet)
- Writing tasks (emails, documents, summaries of provided text)
- Conversation, jokes, casual chat

Return JSON with selected_tools array (just tool names) and reasoning.`

// toolPredictionSchema defines structured output for tool selection
var toolPredictionSchema = map[string]interface{}{
	"type": "object",
	"properties": map[string]interface{}{
		"selected_tools": map[string]interface{}{
			"type": "array",
			"items": map[string]interface{}{
				"type":        "string",
				"description": "Tool name from available tools",
			},
			"description": "Array of tool names needed for this request",
		},
		"reasoning": map[string]interface{}{
			"type":        "string",
			"description": "Brief explanation of tool selection",
		},
	},
	"required":             []string{"selected_tools", "reasoning"},
	"additionalProperties": false,
}

// NewToolPredictorService creates a new tool predictor service
func NewToolPredictorService(
	db *database.DB,
	providerService *ProviderService,
	chatService *ChatService,
) *ToolPredictorService {
	svc := &ToolPredictorService{
		db:              db,
		providerService: providerService,
		chatService:     chatService,
		routerModels:    make([]routerModelCandidate, 0),
	}

	// Discover router models on startup
	svc.DiscoverRouterModels()

	return svc
}

// SetUserService sets the user service for preference lookup
func (s *ToolPredictorService) SetUserService(userService *UserService) {
	s.userService = userService
	log.Printf("✅ [TOOL-PREDICTOR] User service set for preference lookup")
}

// SetHealthService sets the health service for provider health tracking
func (s *ToolPredictorService) SetHealthService(hs *health.Service) {
	s.healthService = hs
	log.Printf("✅ [TOOL-PREDICTOR] Health service set for provider health tracking")
}

// SetSettingsService sets the settings service for system-wide model assignments
func (s *ToolPredictorService) SetSettingsService(settingsService *SettingsService) {
	s.settingsService = settingsService
	log.Printf("✅ [TOOL-PREDICTOR] Settings service set for system model assignment")
}

// SetRedisService sets the Redis service for per-conversation tool caching
func (s *ToolPredictorService) SetRedisService(redisService *RedisService) {
	s.redisService = redisService
	log.Printf("✅ [TOOL-PREDICTOR] Redis service set for tool caching")
}

// DiscoverRouterModels loads all smart_tool_router models from DB, sorted by speed (fastest first)
func (s *ToolPredictorService) DiscoverRouterModels() {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.routerModels = make([]routerModelCandidate, 0)

	// Query models table for smart_tool_router models, join with providers for name
	rows, err := s.db.Query(`
		SELECT m.id, m.name, m.provider_id, COALESCE(pr.name, '') as provider_name
		FROM models m
		JOIN providers pr ON m.provider_id = pr.id
		WHERE m.smart_tool_router = 1 AND m.is_visible = 1 AND pr.enabled = 1
		ORDER BY m.id ASC
	`)
	if err != nil {
		log.Printf("⚠️ [TOOL-PREDICTOR] Failed to query smart_tool_router models: %v", err)
		return
	}
	defer rows.Close()

	for rows.Next() {
		var modelID, modelName, providerName string
		var providerID int

		if err := rows.Scan(&modelID, &modelName, &providerID, &providerName); err != nil {
			log.Printf("⚠️ [TOOL-PREDICTOR] Failed to scan row: %v", err)
			continue
		}

		// Try to get speed from model_aliases table
		speedMs := 999999
		_ = s.db.QueryRow(`
			SELECT COALESCE(structured_output_speed_ms, 999999)
			FROM model_aliases
			WHERE alias_name = ? AND smart_tool_router = 1
		`, modelID).Scan(&speedMs)

		candidate := routerModelCandidate{
			ModelID:      modelID,
			ModelName:    modelName,
			ProviderID:   providerID,
			ProviderName: providerName,
			SpeedMs:      speedMs,
		}
		s.routerModels = append(s.routerModels, candidate)
		log.Printf("✅ [TOOL-PREDICTOR] Found router model: %s (%s) provider=%s speed=%dms",
			modelID, modelName, providerName, speedMs)
	}

	// Sort by speed (fastest first)
	sortRouterModelsBySpeed(s.routerModels)

	s.routerIndex = 0
	log.Printf("🎯 [TOOL-PREDICTOR] Discovered %d smart router models", len(s.routerModels))
}

// sortRouterModelsBySpeed sorts models by speed ascending (fastest first)
func sortRouterModelsBySpeed(models []routerModelCandidate) {
	n := len(models)
	for i := 0; i < n-1; i++ {
		for j := 0; j < n-i-1; j++ {
			if models[j].SpeedMs > models[j+1].SpeedMs {
				models[j], models[j+1] = models[j+1], models[j]
			}
		}
	}
}

// PredictTools predicts which tools are needed for a user message
// Returns selected tool definitions and error (nil on success)
// On failure, returns nil (caller should use all tools as fallback)
// conversationID: Used for Redis-based tool caching per conversation
// conversationHistory: Recent conversation messages for better context-aware tool selection
func (s *ToolPredictorService) PredictTools(
	ctx context.Context,
	userID string,
	conversationID string,
	userMessage string,
	availableTools []map[string]interface{},
	conversationHistory []map[string]interface{},
) ([]map[string]interface{}, error) {

	// Load cached tool names from Redis (with short timeout to avoid blocking)
	cachedNames := s.getCachedToolNames(ctx, conversationID)

	// Get predictor model for user (or use pool)
	predictorModelID, err := s.getPredictorModelForUser(ctx, userID)
	if err != nil {
		log.Printf("⚠️ [TOOL-PREDICTOR] Could not get predictor model: %v, using pool", err)
		predictorModelID = ""
	}

	// Build tool list and user prompt
	toolListPrompt := s.buildToolListPrompt(availableTools)
	userPrompt := fmt.Sprintf(`USER MESSAGE:
%s

AVAILABLE TOOLS:
%s

Select the minimal set of tools needed. Return JSON with selected_tools and reasoning.`,
		userMessage, toolListPrompt)

	// Build messages with conversation history for better context
	messages := []map[string]interface{}{
		{
			"role":    "system",
			"content": ToolPredictionSystemPrompt,
		},
	}

	// Add recent conversation history for multi-turn context (exclude current message)
	// Limit to last 6 messages (3 pairs) to avoid token bloat
	historyLimit := 6
	startIdx := len(conversationHistory) - historyLimit
	if startIdx < 0 {
		startIdx = 0
	}
	for i := startIdx; i < len(conversationHistory); i++ {
		msg := conversationHistory[i]
		messages = append(messages, msg)
	}

	// Add current user message with tool selection prompt
	messages = append(messages, map[string]interface{}{
		"role":    "user",
		"content": userPrompt,
	})

	// Try with user-preferred model first, then failover through the pool
	maxAttempts := 3
	var lastErr error
	var predictedNames []string

	for attempt := 1; attempt <= maxAttempts; attempt++ {
		var provider *models.Provider
		var actualModel string
		var providerID int

		if attempt == 1 && predictorModelID != "" {
			// First attempt: try user's preferred model
			provider, actualModel, providerID, err = s.resolveSpecificModel(predictorModelID)
			if err != nil {
				log.Printf("⚠️ [TOOL-PREDICTOR] User-preferred model %s not found: %v, using pool", predictorModelID, err)
				provider, actualModel, providerID, err = s.getNextHealthyRouter()
			}
		} else {
			// Pool: get next healthy router model
			provider, actualModel, providerID, err = s.getNextHealthyRouter()
		}

		if err != nil {
			lastErr = err
			log.Printf("⚠️ [TOOL-PREDICTOR] Attempt %d: no router model available: %v", attempt, err)
			break // no models at all
		}

		log.Printf("🤖 [TOOL-PREDICTOR] Attempt %d: using model %s (%s) from %s",
			attempt, actualModel, provider.Name, provider.BaseURL)

		result, err := s.callPredictorAPI(ctx, provider, actualModel, messages)
		if err != nil {
			lastErr = err
			log.Printf("⚠️ [TOOL-PREDICTOR] Attempt %d failed: %v", attempt, err)

			// Report health failure
			if s.healthService != nil {
				bodyStr := err.Error()
				if health.IsQuotaError(0, bodyStr) {
					cooldown := health.ParseCooldownDuration(0, bodyStr)
					s.healthService.SetCooldown(health.CapabilityChat, providerID, actualModel, cooldown)
					log.Printf("[HEALTH] Tool predictor: provider %s/%s quota exceeded, cooldown %v", provider.Name, actualModel, cooldown)
				} else {
					s.healthService.MarkUnhealthy(health.CapabilityChat, providerID, actualModel, bodyStr, 0)
				}
			}
			continue // try next model
		}

		// Report health success
		if s.healthService != nil {
			s.healthService.MarkHealthy(health.CapabilityChat, providerID, actualModel)
		}

		log.Printf("✅ [TOOL-PREDICTOR] Selected %d tools: %v", len(result.SelectedTools), result.SelectedTools)
		log.Printf("💭 [TOOL-PREDICTOR] Reasoning: %s", result.Reasoning)

		predictedNames = result.SelectedTools
		break // success
	}

	// Merge predicted tools with cached tools (union)
	mergedNames := s.mergeToolNames(predictedNames, cachedNames)

	// If both predictor and cache failed, return error
	if len(mergedNames) == 0 && lastErr != nil {
		return nil, fmt.Errorf("all predictor models failed after %d attempts and no cache available: %w", maxAttempts, lastErr)
	}

	// Cache the merged tool names in Redis
	s.cacheToolNames(ctx, conversationID, mergedNames)

	// Filter available tools by merged names
	selectedToolDefs := s.filterToolsByNames(availableTools, mergedNames)
	log.Printf("📊 [TOOL-PREDICTOR] Reduced from %d to %d tools (predicted=%d, cached=%d, merged=%d)",
		len(availableTools), len(selectedToolDefs), len(predictedNames), len(cachedNames), len(mergedNames))

	return selectedToolDefs, nil
}

// callPredictorAPI makes the actual HTTP call to the predictor model
func (s *ToolPredictorService) callPredictorAPI(
	ctx context.Context,
	provider *models.Provider,
	actualModel string,
	messages []map[string]interface{},
) (*ToolPredictionResult, error) {
	// Build request with structured output
	requestBody := map[string]interface{}{
		"model":       actualModel,
		"messages":    messages,
		"stream":      false,
		"temperature": 0.2, // Low temp for consistency
		"response_format": map[string]interface{}{
			"type": "json_schema",
			"json_schema": map[string]interface{}{
				"name":   "tool_prediction",
				"strict": true,
				"schema": toolPredictionSchema,
			},
		},
	}

	reqBody, err := json.Marshal(requestBody)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal request: %w", err)
	}

	log.Printf("📤 [TOOL-PREDICTOR] Sending prediction request to %s", provider.BaseURL)

	// Create HTTP request with timeout
	httpReq, err := http.NewRequestWithContext(ctx, "POST", provider.BaseURL+"/chat/completions", bytes.NewBuffer(reqBody))
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}

	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("Authorization", "Bearer "+provider.APIKey)

	// Send request with 30s timeout
	client := &http.Client{Timeout: 30 * time.Second}
	resp, err := client.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("request failed: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("failed to read response: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		log.Printf("⚠️ [TOOL-PREDICTOR] API error: %s", string(body))
		return nil, fmt.Errorf("API error (status %d): %s", resp.StatusCode, string(body))
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
		return nil, fmt.Errorf("failed to parse API response: %w", err)
	}

	if len(apiResponse.Choices) == 0 {
		return nil, fmt.Errorf("no response from predictor model")
	}

	// Parse the prediction result — strip markdown code blocks that some LLMs add
	// even when response_format is set (e.g. ZAI/glm models ignore structured output)
	var result ToolPredictionResult
	content := strings.TrimSpace(apiResponse.Choices[0].Message.Content)
	content = stripMarkdownCodeBlock(content)

	if err := json.Unmarshal([]byte(content), &result); err != nil {
		log.Printf("⚠️ [TOOL-PREDICTOR] Failed to parse prediction: %v, content: %s", err, content)
		return nil, fmt.Errorf("failed to parse prediction: %w", err)
	}

	return &result, nil
}

// getNextHealthyRouter returns the next healthy router model using round-robin, fastest first
func (s *ToolPredictorService) getNextHealthyRouter() (*models.Provider, string, int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if len(s.routerModels) == 0 {
		return nil, "", 0, fmt.Errorf("no smart_tool_router models available")
	}

	// Try all models in round-robin fashion
	for attempts := 0; attempts < len(s.routerModels); attempts++ {
		candidate := s.routerModels[s.routerIndex]
		s.routerIndex = (s.routerIndex + 1) % len(s.routerModels)

		// Check health via system-wide health service
		if s.healthService != nil && !s.healthService.IsProviderHealthy(health.CapabilityChat, candidate.ProviderID, candidate.ModelName) {
			log.Printf("⏭️ [TOOL-PREDICTOR] Skipping unhealthy: %s (%s)", candidate.ModelID, candidate.ProviderName)
			continue
		}

		provider, err := s.providerService.GetByID(candidate.ProviderID)
		if err != nil {
			log.Printf("⚠️ [TOOL-PREDICTOR] Failed to get provider %d: %v", candidate.ProviderID, err)
			continue
		}

		log.Printf("🔄 [TOOL-PREDICTOR] Selected router: %s (%s) speed=%dms", candidate.ModelID, candidate.ProviderName, candidate.SpeedMs)
		return provider, candidate.ModelName, candidate.ProviderID, nil
	}

	// All unhealthy — fall back to fastest (first in sorted list)
	candidate := s.routerModels[0]
	provider, err := s.providerService.GetByID(candidate.ProviderID)
	if err != nil {
		return nil, "", 0, fmt.Errorf("failed to get fallback provider: %w", err)
	}

	log.Printf("⚠️ [TOOL-PREDICTOR] All routers unhealthy, using fastest: %s (%s)", candidate.ModelID, candidate.ProviderName)
	return provider, candidate.ModelName, candidate.ProviderID, nil
}

// resolveSpecificModel resolves a user-preferred model ID to provider and model name
func (s *ToolPredictorService) resolveSpecificModel(modelID string) (*models.Provider, string, int, error) {
	// Try models table first
	var providerID int
	var modelName string
	var smartToolRouter int

	err := s.db.QueryRow(`
		SELECT m.name, m.provider_id, COALESCE(m.smart_tool_router, 0)
		FROM models m
		WHERE m.id = ? AND m.is_visible = 1
	`, modelID).Scan(&modelName, &providerID, &smartToolRouter)

	if err != nil {
		// Try as model alias
		if s.chatService != nil {
			if provider, actualModel, found := s.chatService.ResolveModelAlias(modelID); found {
				return provider, actualModel, provider.ID, nil
			}
		}
		return nil, "", 0, fmt.Errorf("model %s not found", modelID)
	}

	provider, err := s.providerService.GetByID(providerID)
	if err != nil {
		return nil, "", 0, fmt.Errorf("failed to get provider: %w", err)
	}

	return provider, modelName, providerID, nil
}

// buildToolListPrompt creates a concise list of tools for the prompt
func (s *ToolPredictorService) buildToolListPrompt(tools []map[string]interface{}) string {
	var builder strings.Builder

	for i, toolDef := range tools {
		fn, ok := toolDef["function"].(map[string]interface{})
		if !ok {
			continue
		}

		name, _ := fn["name"].(string)
		desc, _ := fn["description"].(string)

		builder.WriteString(fmt.Sprintf("%d. %s: %s\n", i+1, name, desc))
	}

	return builder.String()
}

// filterToolsByNames filters tool definitions by selected names
func (s *ToolPredictorService) filterToolsByNames(
	allTools []map[string]interface{},
	selectedNames []string,
) []map[string]interface{} {

	// Build set for O(1) lookup
	nameSet := make(map[string]bool)
	for _, name := range selectedNames {
		nameSet[name] = true
	}

	filtered := make([]map[string]interface{}, 0, len(selectedNames))

	for _, toolDef := range allTools {
		fn, ok := toolDef["function"].(map[string]interface{})
		if !ok {
			continue
		}

		name, ok := fn["name"].(string)
		if !ok {
			continue
		}

		if nameSet[name] {
			filtered = append(filtered, toolDef)
		}
	}

	return filtered
}

// getPredictorModelForUser gets the user's preferred predictor model from MongoDB
func (s *ToolPredictorService) getPredictorModelForUser(ctx context.Context, userID string) (string, error) {
	// Check system-wide setting first (admin override)
	if s.settingsService != nil {
		assignments, err := s.settingsService.GetSystemModelAssignments(ctx)
		if err == nil && assignments.ToolSelector != "" {
			log.Printf("🎯 [TOOL-PREDICTOR] Using system-assigned model: %s", assignments.ToolSelector)
			return assignments.ToolSelector, nil
		}
	}

	// Fall back to user preference
	if s.userService == nil {
		return "", nil
	}

	// Get user from MongoDB
	user, err := s.userService.GetUserBySupabaseID(ctx, userID)
	if err != nil {
		return "", nil
	}

	// Check if user has a preferred predictor model
	if user.Preferences.ToolPredictorModelID != "" {
		log.Printf("🎯 [TOOL-PREDICTOR] Using user-preferred model: %s", user.Preferences.ToolPredictorModelID)
		return user.Preferences.ToolPredictorModelID, nil
	}

	return "", nil
}

// ═══════════════════════════════════════════════════════════════════════════
// REDIS TOOL CACHE — per-conversation tool persistence
// ═══════════════════════════════════════════════════════════════════════════

func (s *ToolPredictorService) toolCacheKey(conversationID string) string {
	return toolCacheKeyPrefix + conversationID
}

// getCachedToolNames returns cached tool names for a conversation, or nil on miss/error
func (s *ToolPredictorService) getCachedToolNames(ctx context.Context, conversationID string) []string {
	if s.redisService == nil || conversationID == "" {
		return nil
	}

	cacheCtx, cancel := context.WithTimeout(ctx, 500*time.Millisecond)
	defer cancel()

	exists, err := s.redisService.Exists(cacheCtx, s.toolCacheKey(conversationID))
	if err != nil {
		log.Printf("⚠️ [TOOL-CACHE] Redis Exists error (non-fatal): %v", err)
		return nil
	}
	if exists == 0 {
		return nil
	}

	names, err := s.redisService.SMembers(cacheCtx, s.toolCacheKey(conversationID))
	if err != nil {
		log.Printf("⚠️ [TOOL-CACHE] Redis SMembers error (non-fatal): %v", err)
		return nil
	}

	log.Printf("📦 [TOOL-CACHE] Cache HIT for %s: %d tools %v", conversationID, len(names), names)
	return names
}

// cacheToolNames stores tool names in Redis for a conversation
func (s *ToolPredictorService) cacheToolNames(ctx context.Context, conversationID string, toolNames []string) {
	if s.redisService == nil || conversationID == "" || len(toolNames) == 0 {
		return
	}

	cacheCtx, cancel := context.WithTimeout(ctx, 500*time.Millisecond)
	defer cancel()

	key := s.toolCacheKey(conversationID)
	members := make([]interface{}, len(toolNames))
	for i, name := range toolNames {
		members[i] = name
	}

	if _, err := s.redisService.SAdd(cacheCtx, key, members...); err != nil {
		log.Printf("⚠️ [TOOL-CACHE] Failed to cache tools for %s: %v", conversationID, err)
		return
	}
	_ = s.redisService.Expire(cacheCtx, key, toolCacheTTL)
	log.Printf("💾 [TOOL-CACHE] Cached %d tools for conversation %s", len(toolNames), conversationID)
}

// AddToolToCache adds a single tool name to the conversation cache (called on tool execution)
func (s *ToolPredictorService) AddToolToCache(ctx context.Context, conversationID string, toolName string) {
	if s.redisService == nil || conversationID == "" || toolName == "" {
		return
	}

	cacheCtx, cancel := context.WithTimeout(ctx, 500*time.Millisecond)
	defer cancel()

	key := s.toolCacheKey(conversationID)
	if _, err := s.redisService.SAdd(cacheCtx, key, toolName); err != nil {
		log.Printf("⚠️ [TOOL-CACHE] Failed to add tool %s to cache for %s: %v", toolName, conversationID, err)
		return
	}
	_ = s.redisService.Expire(cacheCtx, key, toolCacheTTL)
}

// mergeToolNames returns the union of two tool name slices (deduped)
func (s *ToolPredictorService) mergeToolNames(predicted []string, cached []string) []string {
	nameSet := make(map[string]bool, len(predicted)+len(cached))
	for _, n := range predicted {
		nameSet[n] = true
	}
	for _, n := range cached {
		nameSet[n] = true
	}

	merged := make([]string, 0, len(nameSet))
	for name := range nameSet {
		merged = append(merged, name)
	}
	return merged
}

// stripMarkdownCodeBlock removes ```json ... ``` wrapping that some LLMs add
// even when response_format with json_schema is specified.
func stripMarkdownCodeBlock(s string) string {
	s = strings.TrimSpace(s)
	// Handle ```json ... ``` or ``` ... ```
	if strings.HasPrefix(s, "```") {
		// Remove opening line (```json or ```)
		if idx := strings.Index(s, "\n"); idx != -1 {
			s = s[idx+1:]
		}
		// Remove closing ```
		if idx := strings.LastIndex(s, "```"); idx != -1 {
			s = s[:idx]
		}
		s = strings.TrimSpace(s)
	}
	return s
}
