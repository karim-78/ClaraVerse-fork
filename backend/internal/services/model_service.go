package services

import (
	"claraverse/internal/database"
	"claraverse/internal/models"
	"database/sql"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"strings"
	"time"
)

// ModelService handles model operations
type ModelService struct {
	db *database.DB
}

// NewModelService creates a new model service
func NewModelService(db *database.DB) *ModelService {
	return &ModelService{db: db}
}

// GetDB returns the underlying database connection
func (s *ModelService) GetDB() *database.DB {
	return s.db
}

// GetAll returns all models, optionally filtered by visibility
// Excludes models from audio-only providers (those are for transcription only)
func (s *ModelService) GetAll(visibleOnly bool) ([]models.Model, error) {
	query := `
		SELECT m.id, m.provider_id, p.name as provider_name, p.favicon as provider_favicon,
		       m.name, m.display_name, m.description, m.context_length, m.supports_tools,
		       m.supports_streaming, m.supports_vision, m.smart_tool_router, m.is_visible, m.system_prompt, m.fetched_at
		FROM models m
		JOIN providers p ON m.provider_id = p.id
		WHERE (p.audio_only = 0 OR p.audio_only IS NULL)
	`
	if visibleOnly {
		query += " AND m.is_visible = 1"
	}
	query += " ORDER BY p.name, m.name"

	rows, err := s.db.Query(query)
	if err != nil {
		return nil, fmt.Errorf("failed to query models: %w", err)
	}
	defer rows.Close()

	var modelsList []models.Model
	for rows.Next() {
		var m models.Model
		var displayName, description, systemPrompt, providerFavicon sql.NullString
		var contextLength sql.NullInt64
		var fetchedAt sql.NullTime

		err := rows.Scan(&m.ID, &m.ProviderID, &m.ProviderName, &providerFavicon,
			&m.Name, &displayName, &description, &contextLength, &m.SupportsTools,
			&m.SupportsStreaming, &m.SupportsVision, &m.SmartToolRouter, &m.IsVisible, &systemPrompt, &fetchedAt)
		if err != nil {
			return nil, fmt.Errorf("failed to scan model: %w", err)
		}

		// Handle nullable fields
		if displayName.Valid {
			m.DisplayName = displayName.String
		}
		if description.Valid {
			m.Description = description.String
		}
		if contextLength.Valid {
			m.ContextLength = int(contextLength.Int64)
		}
		if systemPrompt.Valid {
			m.SystemPrompt = systemPrompt.String
		}
		if providerFavicon.Valid {
			m.ProviderFavicon = providerFavicon.String
		}
		if fetchedAt.Valid {
			m.FetchedAt = fetchedAt.Time
		}

		modelsList = append(modelsList, m)
	}

	return modelsList, nil
}

// GetByProvider returns models for a specific provider
func (s *ModelService) GetByProvider(providerID int, visibleOnly bool) ([]models.Model, error) {
	query := `
		SELECT m.id, m.provider_id, p.name as provider_name, p.favicon as provider_favicon,
		       m.name, m.display_name, m.description, m.context_length, m.supports_tools,
		       m.supports_streaming, m.supports_vision, m.smart_tool_router, m.is_visible, m.system_prompt, m.fetched_at
		FROM models m
		JOIN providers p ON m.provider_id = p.id
		WHERE m.provider_id = ?
	`
	if visibleOnly {
		query += " AND m.is_visible = 1"
	}
	query += " ORDER BY m.name"

	rows, err := s.db.Query(query, providerID)
	if err != nil {
		return nil, fmt.Errorf("failed to query models: %w", err)
	}
	defer rows.Close()

	var modelsList []models.Model
	for rows.Next() {
		var m models.Model
		var displayName, description, systemPrompt, providerFavicon sql.NullString
		var contextLength sql.NullInt64
		var fetchedAt sql.NullTime

		err := rows.Scan(&m.ID, &m.ProviderID, &m.ProviderName, &providerFavicon,
			&m.Name, &displayName, &description, &contextLength, &m.SupportsTools,
			&m.SupportsStreaming, &m.SupportsVision, &m.SmartToolRouter, &m.IsVisible, &systemPrompt, &fetchedAt)
		if err != nil {
			return nil, fmt.Errorf("failed to scan model: %w", err)
		}

		// Handle nullable fields
		if displayName.Valid {
			m.DisplayName = displayName.String
		}
		if description.Valid {
			m.Description = description.String
		}
		if contextLength.Valid {
			m.ContextLength = int(contextLength.Int64)
		}
		if systemPrompt.Valid {
			m.SystemPrompt = systemPrompt.String
		}
		if providerFavicon.Valid {
			m.ProviderFavicon = providerFavicon.String
		}
		if fetchedAt.Valid {
			m.FetchedAt = fetchedAt.Time
		}

		modelsList = append(modelsList, m)
	}

	return modelsList, nil
}

// GetToolPredictorModels returns only models that can be used as tool predictors
// These are models with smart_tool_router = true and is_visible = true
func (s *ModelService) GetToolPredictorModels() ([]models.Model, error) {
	query := `
		SELECT m.id, m.provider_id, p.name as provider_name, p.favicon as provider_favicon,
		       m.name, m.display_name, m.description, m.context_length, m.supports_tools,
		       m.supports_streaming, m.supports_vision, m.smart_tool_router, m.is_visible, m.system_prompt, m.fetched_at
		FROM models m
		JOIN providers p ON m.provider_id = p.id
		WHERE m.smart_tool_router = 1
		  AND m.is_visible = 1
		  AND (p.audio_only = 0 OR p.audio_only IS NULL)
		ORDER BY p.name, m.name
	`

	rows, err := s.db.Query(query)
	if err != nil {
		return nil, fmt.Errorf("failed to query tool predictor models: %w", err)
	}
	defer rows.Close()

	var modelsList []models.Model
	for rows.Next() {
		var m models.Model
		var displayName, description, systemPrompt, providerFavicon sql.NullString
		var contextLength sql.NullInt64
		var fetchedAt sql.NullTime

		err := rows.Scan(&m.ID, &m.ProviderID, &m.ProviderName, &providerFavicon,
			&m.Name, &displayName, &description, &contextLength, &m.SupportsTools,
			&m.SupportsStreaming, &m.SupportsVision, &m.SmartToolRouter, &m.IsVisible, &systemPrompt, &fetchedAt)
		if err != nil {
			return nil, fmt.Errorf("failed to scan tool predictor model: %w", err)
		}

		// Handle nullable fields
		if displayName.Valid {
			m.DisplayName = displayName.String
		}
		if description.Valid {
			m.Description = description.String
		}
		if providerFavicon.Valid {
			m.ProviderFavicon = providerFavicon.String
		}
		if contextLength.Valid {
			m.ContextLength = int(contextLength.Int64)
		}
		if systemPrompt.Valid {
			m.SystemPrompt = systemPrompt.String
		}
		if fetchedAt.Valid {
			m.FetchedAt = fetchedAt.Time
		}

		modelsList = append(modelsList, m)
	}

	return modelsList, nil
}

// FetchFromProvider fetches models from a provider's API
func (s *ModelService) FetchFromProvider(provider *models.Provider) error {
	log.Printf("🔄 Fetching models from provider: %s", provider.Name)

	// Bedrock's OpenAI-compatible shim does NOT expose /v1/models — it
	// returns UnknownOperationException. We have to probe a curated catalog
	// of model IDs and keep only the ones our key actually has access to.
	if isBedrockProvider(provider.BaseURL) {
		discovered, err := probeBedrockOpenAIShim(provider.APIKey, provider.BaseURL)
		if err != nil {
			return fmt.Errorf("bedrock probe failed: %w", err)
		}
		log.Printf("✅ Bedrock probe: %d of %d candidate models accessible", len(discovered), len(bedrockOpenAIShimCatalog))
		return s.upsertModels(provider, discovered)
	}

	// OpenAI-compatible providers: GET /v1/models works as standard.
	req, err := http.NewRequest("GET", provider.BaseURL+"/models", nil)
	if err != nil {
		return fmt.Errorf("failed to create request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+provider.APIKey)
	req.Header.Set("Content-Type", "application/json")

	client := &http.Client{Timeout: 60 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("failed to fetch models: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("API error (status %d): %s", resp.StatusCode, string(body))
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return fmt.Errorf("failed to read response: %w", err)
	}

	var modelsResp models.OpenAIModelsResponse
	if err := json.Unmarshal(body, &modelsResp); err != nil {
		return fmt.Errorf("failed to parse models response: %w", err)
	}

	ids := make([]string, 0, len(modelsResp.Data))
	for _, m := range modelsResp.Data {
		ids = append(ids, m.ID)
	}
	log.Printf("✅ Fetched %d models from %s", len(ids), provider.Name)
	return s.upsertModels(provider, ids)
}

// upsertModels writes a discovered model list to the database. Shared by
// the OpenAI-compat path (GET /v1/models) and the Bedrock probe path.
func (s *ModelService) upsertModels(provider *models.Provider, modelIDs []string) error {
	for _, id := range modelIDs {
		_, err := s.db.Exec(`
			INSERT INTO models (id, provider_id, name, display_name, fetched_at)
			VALUES (?, ?, ?, ?, ?)
			ON DUPLICATE KEY UPDATE
				name = VALUES(name),
				display_name = VALUES(display_name),
				fetched_at = VALUES(fetched_at)
		`, fmt.Sprintf("%d:%s", provider.ID, id), provider.ID, id, id, time.Now())
		if err != nil {
			log.Printf("⚠️  Failed to store model %s: %v", id, err)
		}
	}

	_, err := s.db.Exec(`
		INSERT INTO model_refresh_log (provider_id, models_fetched, refreshed_at)
		VALUES (?, ?, ?)
	`, provider.ID, len(modelIDs), time.Now())
	if err != nil {
		log.Printf("⚠️  Failed to log refresh: %v", err)
	}

	log.Printf("✅ Refreshed %d models for provider %s", len(modelIDs), provider.Name)
	return nil
}

// isBedrockProvider sniffs the base_url to decide whether to use the Bedrock
// probe path (no /v1/models endpoint) vs the standard OpenAI-compatible
// path. Matches both the runtime and mantle hosts.
func isBedrockProvider(baseURL string) bool {
	u := strings.ToLower(baseURL)
	return strings.Contains(u, "bedrock-runtime") || strings.Contains(u, "bedrock-mantle")
}

// bedrockOpenAIShimCatalog is the curated list of model IDs that Bedrock's
// OpenAI-compatible inference engine ("Mantle") currently serves. AWS rolls
// new ones into this surface quietly — add candidates here when AWS
// announces, the probe will skip anything not accessible to the given key.
//
// Pulled from live testing on ap-south-1 / us-east-1 in May 2026. Update
// over time; keep IDs alphabetical to make diffs reviewable.
var bedrockOpenAIShimCatalog = []string{
	// Moonshot
	"moonshotai.kimi-k2.5",
	// OpenAI gpt-oss (Harmony reasoning format — model emits <reasoning> tags)
	"openai.gpt-oss-120b-1:0",
	"openai.gpt-oss-20b-1:0",
	// Qwen
	"qwen.qwen3-32b-v1:0",
	"qwen.qwen3-coder-480b-a35b-v1:0",
	// Z.AI (formerly Zhipu)
	"zai.glm-5",
}

// probeBedrockOpenAIShim sends a minimal chat-completions request to each
// candidate model and keeps the ones the key has access to. We deliberately
// use chat-completions (not /v1/models which the shim doesn't expose) — a
// 200 = the key can invoke it, 400/404 = it's not on the catalog for this
// account/region.
//
// Runs concurrently with a bounded worker pool to keep total probe time
// under ~5s even if a few endpoints are slow. Each probe asks for 1 token
// so cost is negligible — call this once on provider add + maybe once a
// week on a refresh schedule, not on every chat turn.
func probeBedrockOpenAIShim(apiKey, baseURL string) ([]string, error) {
	const concurrency = 4
	type result struct {
		id string
		ok bool
	}
	jobs := make(chan string, len(bedrockOpenAIShimCatalog))
	results := make(chan result, len(bedrockOpenAIShimCatalog))

	probe := func(id string) bool {
		body := fmt.Sprintf(`{"model":%q,"messages":[{"role":"user","content":"ok"}],"max_tokens":1}`, id)
		req, err := http.NewRequest("POST",
			strings.TrimRight(baseURL, "/")+"/chat/completions",
			strings.NewReader(body))
		if err != nil {
			return false
		}
		req.Header.Set("Authorization", "Bearer "+apiKey)
		req.Header.Set("Content-Type", "application/json")
		client := &http.Client{Timeout: 15 * time.Second}
		resp, err := client.Do(req)
		if err != nil {
			return false
		}
		defer resp.Body.Close()
		// 200 = invokable. 400/404 = unknown id for this key. 401/403 =
		// auth — treat as failure so we don't surface a model the user
		// can't actually use.
		return resp.StatusCode == http.StatusOK
	}

	for i := 0; i < concurrency; i++ {
		go func() {
			for id := range jobs {
				results <- result{id: id, ok: probe(id)}
			}
		}()
	}
	for _, id := range bedrockOpenAIShimCatalog {
		jobs <- id
	}
	close(jobs)

	var accessible []string
	for i := 0; i < len(bedrockOpenAIShimCatalog); i++ {
		r := <-results
		if r.ok {
			accessible = append(accessible, r.id)
		}
	}
	// Stable order matches the catalog so re-runs don't shuffle the DB.
	out := make([]string, 0, len(accessible))
	for _, id := range bedrockOpenAIShimCatalog {
		for _, a := range accessible {
			if a == id {
				out = append(out, id)
				break
			}
		}
	}
	return out, nil
}

// SyncModelAliasMetadata syncs metadata from model aliases to the database
// This updates existing model records with flags like smart_tool_router, agents, supports_vision
func (s *ModelService) SyncModelAliasMetadata(providerID int, aliases map[string]models.ModelAlias) error {
	if len(aliases) == 0 {
		return nil
	}

	log.Printf("🔄 [MODEL-SYNC] Syncing metadata for %d model aliases (provider %d)", len(aliases), providerID)

	for modelID, alias := range aliases {
		// Build update statement for fields that are set in the alias
		updateParts := []string{}
		args := []interface{}{}

		// Smart tool router flag
		if alias.SmartToolRouter != nil {
			updateParts = append(updateParts, "smart_tool_router = ?")
			if *alias.SmartToolRouter {
				args = append(args, 1)
			} else {
				args = append(args, 0)
			}
		}

		// Free tier flag
		if alias.FreeTier != nil {
			updateParts = append(updateParts, "free_tier = ?")
			if *alias.FreeTier {
				args = append(args, 1)
			} else {
				args = append(args, 0)
			}
		}

		// Supports vision flag
		if alias.SupportsVision != nil {
			updateParts = append(updateParts, "supports_vision = ?")
			if *alias.SupportsVision {
				args = append(args, 1)
			} else {
				args = append(args, 0)
			}
		}

		// Display name
		if alias.DisplayName != "" {
			updateParts = append(updateParts, "display_name = ?")
			args = append(args, alias.DisplayName)
		}

		// Description
		if alias.Description != "" {
			updateParts = append(updateParts, "description = ?")
			args = append(args, alias.Description)
		}

		if len(updateParts) == 0 {
			continue // No metadata to sync for this alias
		}

		// Add WHERE clause arguments
		args = append(args, modelID, providerID)

		query := fmt.Sprintf(`
			UPDATE models
			SET %s
			WHERE id = ? AND provider_id = ?
		`, strings.Join(updateParts, ", "))

		result, err := s.db.Exec(query, args...)
		if err != nil {
			log.Printf("⚠️  [MODEL-SYNC] Failed to update model %s: %v", modelID, err)
			continue
		}

		rowsAffected, _ := result.RowsAffected()
		if rowsAffected > 0 {
			log.Printf("   ✅ Updated model %s: %s", modelID, strings.Join(updateParts, ", "))
		}
	}

	log.Printf("✅ [MODEL-SYNC] Model alias metadata sync completed for provider %d", providerID)
	return nil
}

// LoadAllAliasesFromDB loads all model aliases from the database
// Returns map[providerID]map[aliasName]ModelAlias
func (s *ModelService) LoadAllAliasesFromDB() (map[int]map[string]models.ModelAlias, error) {
	query := `
		SELECT provider_id, alias_name, model_id, display_name, description,
		       supports_vision, agents_enabled, smart_tool_router, free_tier,
		       structured_output_support, structured_output_compliance,
		       structured_output_warning, structured_output_speed_ms,
		       structured_output_badge, memory_extractor, memory_selector
		FROM model_aliases
		ORDER BY provider_id, alias_name
	`

	rows, err := s.db.Query(query)
	if err != nil {
		return nil, fmt.Errorf("failed to query model aliases: %w", err)
	}
	defer rows.Close()

	result := make(map[int]map[string]models.ModelAlias)

	for rows.Next() {
		var providerID int
		var aliasName, modelID, displayName string
		var description, structuredOutputSupport, structuredOutputWarning, structuredOutputBadge sql.NullString
		var supportsVision, agentsEnabled, smartToolRouter, freeTier, memoryExtractor, memorySelector sql.NullBool
		var structuredOutputCompliance, structuredOutputSpeedMs sql.NullInt64

		err := rows.Scan(&providerID, &aliasName, &modelID, &displayName, &description,
			&supportsVision, &agentsEnabled, &smartToolRouter, &freeTier,
			&structuredOutputSupport, &structuredOutputCompliance,
			&structuredOutputWarning, &structuredOutputSpeedMs,
			&structuredOutputBadge, &memoryExtractor, &memorySelector)
		if err != nil {
			log.Printf("⚠️  Failed to scan alias: %v", err)
			continue
		}

		// Initialize provider map if not exists
		if result[providerID] == nil {
			result[providerID] = make(map[string]models.ModelAlias)
		}

		// Build ModelAlias struct
		alias := models.ModelAlias{
			ActualModel: modelID,
			DisplayName: displayName,
		}

		if description.Valid {
			alias.Description = description.String
		}
		if supportsVision.Valid {
			val := supportsVision.Bool
			alias.SupportsVision = &val
		}
		if agentsEnabled.Valid {
			val := agentsEnabled.Bool
			alias.Agents = &val
		}
		if smartToolRouter.Valid {
			val := smartToolRouter.Bool
			alias.SmartToolRouter = &val
		}
		if freeTier.Valid {
			val := freeTier.Bool
			alias.FreeTier = &val
		}
		if structuredOutputSupport.Valid {
			alias.StructuredOutputSupport = structuredOutputSupport.String
		}
		if structuredOutputCompliance.Valid {
			val := int(structuredOutputCompliance.Int64)
			alias.StructuredOutputCompliance = &val
		}
		if structuredOutputWarning.Valid {
			alias.StructuredOutputWarning = structuredOutputWarning.String
		}
		if structuredOutputSpeedMs.Valid {
			val := int(structuredOutputSpeedMs.Int64)
			alias.StructuredOutputSpeedMs = &val
		}
		if structuredOutputBadge.Valid {
			alias.StructuredOutputBadge = structuredOutputBadge.String
		}
		if memoryExtractor.Valid {
			val := memoryExtractor.Bool
			alias.MemoryExtractor = &val
		}
		if memorySelector.Valid {
			val := memorySelector.Bool
			alias.MemorySelector = &val
		}

		result[providerID][aliasName] = alias
	}

	log.Printf("✅ Loaded %d provider alias sets from database", len(result))
	return result, nil
}

// LoadAllRecommendedModelsFromDB loads all recommended models from the database
// Returns map[providerID]*RecommendedModels
func (s *ModelService) LoadAllRecommendedModelsFromDB() (map[int]*models.RecommendedModels, error) {
	query := `
		SELECT provider_id, tier, model_alias
		FROM recommended_models
		ORDER BY provider_id, tier
	`

	rows, err := s.db.Query(query)
	if err != nil {
		return nil, fmt.Errorf("failed to query recommended models: %w", err)
	}
	defer rows.Close()

	result := make(map[int]*models.RecommendedModels)

	for rows.Next() {
		var providerID int
		var tier, modelAlias string

		err := rows.Scan(&providerID, &tier, &modelAlias)
		if err != nil {
			log.Printf("⚠️  Failed to scan recommended model: %v", err)
			continue
		}

		// Initialize provider recommendations if not exists
		if result[providerID] == nil {
			result[providerID] = &models.RecommendedModels{}
		}

		// Set the appropriate tier
		switch tier {
		case "top":
			result[providerID].Top = modelAlias
		case "medium":
			result[providerID].Medium = modelAlias
		case "fastest":
			result[providerID].Fastest = modelAlias
		case "new":
			result[providerID].New = modelAlias
		}
	}

	log.Printf("✅ Loaded recommended models for %d providers from database", len(result))
	return result, nil
}

// SaveAliasesToDB saves model aliases to the database
func (s *ModelService) SaveAliasesToDB(providerID int, aliases map[string]models.ModelAlias) error {
	if len(aliases) == 0 {
		return nil
	}

	log.Printf("💾 [MODEL-ALIAS] Saving %d aliases to database for provider %d", len(aliases), providerID)

	for aliasName, alias := range aliases {
		_, err := s.db.Exec(`
			INSERT INTO model_aliases (
				alias_name, model_id, provider_id, display_name, description,
				supports_vision, agents_enabled, smart_tool_router, free_tier,
				structured_output_support, structured_output_compliance,
				structured_output_warning, structured_output_speed_ms,
				structured_output_badge, memory_extractor, memory_selector
			) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
			ON DUPLICATE KEY UPDATE
				model_id = VALUES(model_id),
				display_name = VALUES(display_name),
				description = VALUES(description),
				supports_vision = VALUES(supports_vision),
				agents_enabled = VALUES(agents_enabled),
				smart_tool_router = VALUES(smart_tool_router),
				free_tier = VALUES(free_tier),
				structured_output_support = VALUES(structured_output_support),
				structured_output_compliance = VALUES(structured_output_compliance),
				structured_output_warning = VALUES(structured_output_warning),
				structured_output_speed_ms = VALUES(structured_output_speed_ms),
				structured_output_badge = VALUES(structured_output_badge),
				memory_extractor = VALUES(memory_extractor),
				memory_selector = VALUES(memory_selector)
		`,
			aliasName,
			alias.ActualModel,
			providerID,
			alias.DisplayName,
			nullString(alias.Description),
			nullBool(alias.SupportsVision),
			nullBool(alias.Agents),
			nullBool(alias.SmartToolRouter),
			nullBool(alias.FreeTier),
			nullString(alias.StructuredOutputSupport),
			nullInt(alias.StructuredOutputCompliance),
			nullString(alias.StructuredOutputWarning),
			nullInt(alias.StructuredOutputSpeedMs),
			nullString(alias.StructuredOutputBadge),
			nullBool(alias.MemoryExtractor),
			nullBool(alias.MemorySelector),
		)

		if err != nil {
			log.Printf("⚠️  [MODEL-ALIAS] Failed to save alias %s: %v", aliasName, err)
			continue
		}
	}

	log.Printf("✅ [MODEL-ALIAS] Saved %d aliases to database for provider %d", len(aliases), providerID)
	return nil
}

// SaveRecommendedModelsToDB saves recommended models to the database
func (s *ModelService) SaveRecommendedModelsToDB(providerID int, recommended *models.RecommendedModels) error {
	if recommended == nil {
		return nil
	}

	log.Printf("💾 [RECOMMENDED] Saving recommended models to database for provider %d", providerID)

	// Delete existing recommendations for this provider
	_, err := s.db.Exec("DELETE FROM recommended_models WHERE provider_id = ?", providerID)
	if err != nil {
		return fmt.Errorf("failed to delete old recommendations: %w", err)
	}

	// Insert new recommendations
	tiers := map[string]string{
		"top":     recommended.Top,
		"medium":  recommended.Medium,
		"fastest": recommended.Fastest,
		"new":     recommended.New,
	}

	for tier, modelAlias := range tiers {
		if modelAlias == "" {
			continue
		}

		_, err := s.db.Exec(`
			INSERT INTO recommended_models (provider_id, tier, model_alias)
			VALUES (?, ?, ?)
		`, providerID, tier, modelAlias)

		if err != nil {
			log.Printf("⚠️  [RECOMMENDED] Failed to save %s tier: %v", tier, err)
		}
	}

	log.Printf("✅ [RECOMMENDED] Saved recommended models for provider %d", providerID)
	return nil
}

// Helper functions for nullable values
func nullString(s string) interface{} {
	if s == "" {
		return nil
	}
	return s
}

func nullInt(i *int) interface{} {
	if i == nil {
		return nil
	}
	return *i
}

func nullBool(b *bool) interface{} {
	if b == nil {
		return nil
	}
	return *b
}

// IsFreeTier checks if a model is marked as free tier.
// Checks both the models table (for non-aliased models) and the model_aliases
// table (for aliased models where the ID is the alias_name).
func (s *ModelService) IsFreeTier(modelID string) bool {
	// First check model_aliases table (aliased models use alias_name as ID)
	var aliasFreeTier int
	err := s.db.QueryRow(`
		SELECT COALESCE(free_tier, 0)
		FROM model_aliases
		WHERE alias_name = ?
	`, modelID).Scan(&aliasFreeTier)
	if err == nil && aliasFreeTier == 1 {
		return true
	}

	// Fall back to models table (non-aliased models)
	var modelFreeTier int
	err = s.db.QueryRow(`
		SELECT COALESCE(free_tier, 0)
		FROM models
		WHERE id = ?
	`, modelID).Scan(&modelFreeTier)

	return err == nil && modelFreeTier == 1
}
