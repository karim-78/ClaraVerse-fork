package services

import (
	"bytes"
	"claraverse/internal/database"
	"claraverse/internal/models"
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"strings"
	"sync"
	"time"
)

// ModelManagementService handles model CRUD operations with dual-write to SQLite and providers.json
type ModelManagementService struct {
	db            *database.DB
	providersFile string
	fileMutex     sync.Mutex // Protects providers.json file operations
}

// NewModelManagementService creates a new model management service
// providersFile is optional - if empty, only database operations are performed
func NewModelManagementService(db *database.DB) *ModelManagementService {
	return &ModelManagementService{
		db:            db,
		providersFile: "", // No longer using providers file
	}
}

// ================== DUAL-WRITE COORDINATOR ==================

// CreateModel creates a new model in both database and providers.json
func (s *ModelManagementService) CreateModel(ctx context.Context, req *CreateModelRequest) (*models.Model, error) {
	log.Printf("📝 [MODEL-MGMT] Creating model: %s (provider %d)", req.ModelID, req.ProviderID)

	// Step 1: Begin SQLite transaction
	tx, err := s.db.Begin()
	if err != nil {
		return nil, fmt.Errorf("failed to begin transaction: %w", err)
	}
	defer tx.Rollback()

	// Step 2: Insert into database
	_, err = tx.Exec(`
		INSERT INTO models (id, provider_id, name, display_name, description, context_length,
			supports_tools, supports_streaming, supports_vision, is_visible, system_prompt, fetched_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
	`, req.ModelID, req.ProviderID, req.Name, req.DisplayName, req.Description, req.ContextLength,
		req.SupportsTools, req.SupportsStreaming, req.SupportsVision, req.IsVisible, req.SystemPrompt, time.Now())

	if err != nil {
		return nil, fmt.Errorf("failed to insert model: %w", err)
	}

	// Step 3: Commit transaction
	if err := tx.Commit(); err != nil {
		return nil, fmt.Errorf("failed to commit transaction: %w", err)
	}

	log.Printf("✅ [MODEL-MGMT] Created model: %s", req.ModelID)

	// Fetch and return the created model
	return s.GetModelByID(req.ModelID)
}

// UpdateModel updates an existing model in both database and providers.json
func (s *ModelManagementService) UpdateModel(ctx context.Context, modelID string, req *UpdateModelRequest) (*models.Model, error) {
	log.Printf("📝 [MODEL-MGMT] Updating model: %s", modelID)

	// Build dynamic update query
	updateParts := []string{}
	args := []interface{}{}

	if req.DisplayName != nil {
		updateParts = append(updateParts, "display_name = ?")
		args = append(args, *req.DisplayName)
	}
	if req.Description != nil {
		updateParts = append(updateParts, "description = ?")
		args = append(args, *req.Description)
	}
	if req.ContextLength != nil {
		updateParts = append(updateParts, "context_length = ?")
		args = append(args, *req.ContextLength)
	}
	if req.SupportsTools != nil {
		updateParts = append(updateParts, "supports_tools = ?")
		args = append(args, *req.SupportsTools)
	}
	if req.SupportsStreaming != nil {
		updateParts = append(updateParts, "supports_streaming = ?")
		args = append(args, *req.SupportsStreaming)
	}
	if req.SupportsVision != nil {
		updateParts = append(updateParts, "supports_vision = ?")
		args = append(args, *req.SupportsVision)
	}
	if req.IsVisible != nil {
		updateParts = append(updateParts, "is_visible = ?")
		args = append(args, *req.IsVisible)
		log.Printf("[DEBUG] Adding is_visible to update: value=%v type=%T", *req.IsVisible, *req.IsVisible)
	} else {
		log.Printf("[DEBUG] is_visible field is nil, not updating")
	}
	if req.SystemPrompt != nil {
		updateParts = append(updateParts, "system_prompt = ?")
		args = append(args, *req.SystemPrompt)
	}
	if req.SmartToolRouter != nil {
		updateParts = append(updateParts, "smart_tool_router = ?")
		args = append(args, *req.SmartToolRouter)
	}
	if req.FreeTier != nil {
		updateParts = append(updateParts, "free_tier = ?")
		args = append(args, *req.FreeTier)
	}

	if len(updateParts) == 0 {
		return s.GetModelByID(modelID)
	}

	// Add WHERE clause
	args = append(args, modelID)
	query := fmt.Sprintf("UPDATE models SET %s WHERE id = ?", joinStrings(updateParts, ", "))

	log.Printf("[DEBUG] SQL Query: %s", query)
	log.Printf("[DEBUG] SQL Args: %v", args)

	// Step 1: Begin transaction
	tx, err := s.db.Begin()
	if err != nil {
		return nil, fmt.Errorf("failed to begin transaction: %w", err)
	}
	defer tx.Rollback()

	// Step 2: Execute update
	result, err := tx.Exec(query, args...)
	if err != nil {
		return nil, fmt.Errorf("failed to update model: %w", err)
	}

	rowsAffected, _ := result.RowsAffected()
	log.Printf("[DEBUG] SQL execution successful, rows affected: %d", rowsAffected)

	// Step 3: Commit transaction
	if err := tx.Commit(); err != nil {
		return nil, fmt.Errorf("failed to commit transaction: %w", err)
	}

	log.Printf("✅ [MODEL-MGMT] Updated model: %s", modelID)

	// Get fresh model state from database
	updatedModel, err := s.GetModelByID(modelID)
	if err != nil {
		return nil, err
	}
	log.Printf("[DEBUG] Retrieved is_visible after update: %v", updatedModel.IsVisible)
	return updatedModel, nil
}

// DeleteModel deletes a model from both database and providers.json
func (s *ModelManagementService) DeleteModel(ctx context.Context, modelID string) error {
	log.Printf("🗑️  [MODEL-MGMT] Deleting model: %s", modelID)

	// Step 1: Begin transaction
	tx, err := s.db.Begin()
	if err != nil {
		return fmt.Errorf("failed to begin transaction: %w", err)
	}
	defer tx.Rollback()

	// Step 2: Delete from database (cascades to model_capabilities and model_aliases)
	result, err := tx.Exec("DELETE FROM models WHERE id = ?", modelID)
	if err != nil {
		return fmt.Errorf("failed to delete model: %w", err)
	}

	rowsAffected, _ := result.RowsAffected()
	if rowsAffected == 0 {
		return fmt.Errorf("model not found: %s", modelID)
	}

	// Step 3: Commit transaction
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("failed to commit transaction: %w", err)
	}

	log.Printf("✅ [MODEL-MGMT] Deleted model: %s", modelID)
	return nil
}

// reloadConfigServiceCache reloads the config service cache from database
func (s *ModelManagementService) reloadConfigServiceCache() error {
	log.Printf("🔄 [MODEL-MGMT] Reloading config service cache from database...")

	configService := GetConfigService()

	// Get all providers and their aliases from database
	rows, err := s.db.Query(`
		SELECT DISTINCT provider_id FROM model_aliases
	`)
	if err != nil {
		return fmt.Errorf("failed to query provider IDs: %w", err)
	}
	defer rows.Close()

	var providerIDs []int
	for rows.Next() {
		var providerID int
		if err := rows.Scan(&providerID); err != nil {
			return fmt.Errorf("failed to scan provider ID: %w", err)
		}
		providerIDs = append(providerIDs, providerID)
	}

	// Reload aliases for each provider
	for _, providerID := range providerIDs {
		aliases, err := s.getModelAliasesForProvider(providerID)
		if err != nil {
			log.Printf("⚠️  [MODEL-MGMT] Failed to load aliases for provider %d: %v", providerID, err)
			continue
		}

		// Update config service cache
		configService.SetModelAliases(providerID, aliases)
		log.Printf("✅ [MODEL-MGMT] Reloaded %d aliases for provider %d", len(aliases), providerID)
	}

	log.Printf("✅ [MODEL-MGMT] Config service cache reloaded successfully")
	return nil
}

// getModelAliasesForProvider retrieves all model aliases for a provider from model_aliases table
func (s *ModelManagementService) getModelAliasesForProvider(providerID int) (map[string]models.ModelAlias, error) {
	rows, err := s.db.Query(`
		SELECT alias_name, model_id, display_name, description, supports_vision,
		       agents_enabled, smart_tool_router, free_tier, structured_output_support,
		       structured_output_compliance, structured_output_warning, structured_output_speed_ms,
		       structured_output_badge, memory_extractor, memory_selector
		FROM model_aliases
		WHERE provider_id = ?
	`, providerID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	aliases := make(map[string]models.ModelAlias)

	for rows.Next() {
		var aliasName, modelID, displayName string
		var description, structuredOutputSupport, structuredOutputWarning, structuredOutputBadge sql.NullString
		var supportsVision, agentsEnabled, smartToolRouter, freeTier, memoryExtractor, memorySelector sql.NullBool
		var structuredOutputCompliance, structuredOutputSpeedMs sql.NullInt64

		err := rows.Scan(&aliasName, &modelID, &displayName, &description, &supportsVision,
			&agentsEnabled, &smartToolRouter, &freeTier, &structuredOutputSupport,
			&structuredOutputCompliance, &structuredOutputWarning, &structuredOutputSpeedMs,
			&structuredOutputBadge, &memoryExtractor, &memorySelector)
		if err != nil {
			return nil, err
		}

		alias := models.ModelAlias{
			ActualModel: modelID,
			DisplayName: displayName,
		}

		if description.Valid {
			alias.Description = description.String
		}
		if supportsVision.Valid {
			vision := supportsVision.Bool
			alias.SupportsVision = &vision
		}
		if agentsEnabled.Valid {
			agents := agentsEnabled.Bool
			alias.Agents = &agents
		}
		if smartToolRouter.Valid {
			router := smartToolRouter.Bool
			alias.SmartToolRouter = &router
		}
		if freeTier.Valid {
			free := freeTier.Bool
			alias.FreeTier = &free
		}
		if structuredOutputSupport.Valid {
			alias.StructuredOutputSupport = structuredOutputSupport.String
		}
		if structuredOutputCompliance.Valid {
			compliance := int(structuredOutputCompliance.Int64)
			alias.StructuredOutputCompliance = &compliance
		}
		if structuredOutputWarning.Valid {
			alias.StructuredOutputWarning = structuredOutputWarning.String
		}
		if structuredOutputSpeedMs.Valid {
			speed := int(structuredOutputSpeedMs.Int64)
			alias.StructuredOutputSpeedMs = &speed
		}
		if structuredOutputBadge.Valid {
			alias.StructuredOutputBadge = structuredOutputBadge.String
		}
		if memoryExtractor.Valid {
			extractor := memoryExtractor.Bool
			alias.MemoryExtractor = &extractor
		}
		if memorySelector.Valid {
			selector := memorySelector.Bool
			alias.MemorySelector = &selector
		}

		aliases[aliasName] = alias
	}

	return aliases, nil
}

// ================== MODEL FETCHING ==================

// FetchModelsFromProvider fetches models from a provider's API and stores them
func (s *ModelManagementService) FetchModelsFromProvider(ctx context.Context, providerID int) (int, error) {
	log.Printf("🔄 [MODEL-MGMT] Fetching models from provider %d", providerID)

	// Get provider details
	provider, err := s.getProviderByID(providerID)
	if err != nil {
		return 0, fmt.Errorf("failed to get provider: %w", err)
	}

	// Bedrock's OpenAI-compatible shim ("Mantle") does NOT expose a
	// /v1/models listing — a GET there 404s with <UnknownOperationException/>.
	// Probe the curated catalog instead (POST /chat/completions per candidate)
	// and store whatever this key/region can actually invoke. Same logic the
	// legacy ModelService.FetchFromProvider path uses; both live in this pkg.
	if isBedrockProvider(provider.BaseURL) {
		discovered, err := probeBedrockOpenAIShim(provider.APIKey, provider.BaseURL)
		if err != nil {
			return 0, fmt.Errorf("bedrock probe failed: %w", err)
		}
		count := 0
		for _, id := range discovered {
			if _, err := s.db.Exec(`
				INSERT INTO models (id, provider_id, name, display_name, is_visible, fetched_at)
				VALUES (?, ?, ?, ?, 0, ?)
				ON DUPLICATE KEY UPDATE
					name = VALUES(name),
					display_name = VALUES(display_name),
					fetched_at = VALUES(fetched_at)
			`, id, providerID, id, id, time.Now()); err != nil {
				log.Printf("⚠️  [MODEL-MGMT] Failed to store bedrock model %s: %v", id, err)
			} else {
				count++
			}
		}
		log.Printf("✅ [MODEL-MGMT] Bedrock probe stored %d of %d catalog models for provider %d", count, len(discovered), providerID)
		return count, nil
	}

	// Create HTTP request to provider's /v1/models endpoint
	req, err := http.NewRequest("GET", provider.BaseURL+"/models", nil)
	if err != nil {
		return 0, fmt.Errorf("failed to create request: %w", err)
	}

	req.Header.Set("Authorization", "Bearer "+provider.APIKey)
	req.Header.Set("Content-Type", "application/json")

	client := &http.Client{Timeout: 30 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return 0, fmt.Errorf("failed to fetch models: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return 0, fmt.Errorf("API error (status %d): %s", resp.StatusCode, string(body))
	}

	// Parse response
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return 0, fmt.Errorf("failed to read response: %w", err)
	}

	var modelsResp models.OpenAIModelsResponse
	if err := json.Unmarshal(body, &modelsResp); err != nil {
		return 0, fmt.Errorf("failed to parse models response: %w", err)
	}

	log.Printf("✅ [MODEL-MGMT] Fetched %d models from provider %d", len(modelsResp.Data), providerID)

	// Store models in database (all hidden by default - admin must manually toggle visibility)
	count := 0
	for _, modelData := range modelsResp.Data {
		_, err := s.db.Exec(`
			INSERT INTO models (id, provider_id, name, display_name, is_visible, fetched_at)
			VALUES (?, ?, ?, ?, 0, ?)
			ON DUPLICATE KEY UPDATE
				name = VALUES(name),
				display_name = VALUES(display_name),
				fetched_at = VALUES(fetched_at)
		`, modelData.ID, providerID, modelData.ID, modelData.ID, time.Now())

		if err != nil {
			log.Printf("⚠️  [MODEL-MGMT] Failed to store model %s: %v", modelData.ID, err)
		} else {
			count++
		}
	}

	log.Printf("✅ [MODEL-MGMT] Stored %d models for provider %d", count, providerID)
	return count, nil
}

// ================== MODEL TESTING ==================

// TestModelConnection performs a basic connection test
func (s *ModelManagementService) TestModelConnection(ctx context.Context, modelID string) (*ConnectionTestResult, error) {
	log.Printf("🔌 [MODEL-MGMT] Testing connection for model: %s", modelID)

	model, err := s.GetModelByID(modelID)
	if err != nil {
		return nil, err
	}

	provider, err := s.getProviderByID(model.ProviderID)
	if err != nil {
		return nil, err
	}

	start := time.Now()

	// Send test prompt
	reqBody := map[string]interface{}{
		"model": modelID,
		"messages": []map[string]string{
			{"role": "user", "content": "Hello! Respond with OK"},
		},
		"max_tokens": 10,
	}

	jsonData, _ := json.Marshal(reqBody)
	req, err := http.NewRequest("POST", provider.BaseURL+"/chat/completions", bytes.NewBuffer(jsonData))
	if err != nil {
		return nil, err
	}

	req.Header.Set("Authorization", "Bearer "+provider.APIKey)
	req.Header.Set("Content-Type", "application/json")

	client := &http.Client{Timeout: 30 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return &ConnectionTestResult{
			ModelID:   modelID,
			Passed:    false,
			LatencyMs: int(time.Since(start).Milliseconds()),
			Error:     err.Error(),
		}, nil
	}
	defer resp.Body.Close()

	latency := int(time.Since(start).Milliseconds())

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return &ConnectionTestResult{
			ModelID:   modelID,
			Passed:    false,
			LatencyMs: latency,
			Error:     fmt.Sprintf("API error (status %d): %s", resp.StatusCode, string(body)),
		}, nil
	}

	// Update database
	_, err = s.db.Exec(`
		REPLACE INTO model_capabilities (model_id, provider_id, connection_test_passed, tested_at)
		VALUES (?, ?, 1, ?)
	`, modelID, model.ProviderID, time.Now())

	if err != nil {
		log.Printf("⚠️  [MODEL-MGMT] Failed to update test result: %v", err)
	}

	log.Printf("✅ [MODEL-MGMT] Connection test passed for %s (latency: %dms)", modelID, latency)
	return &ConnectionTestResult{
		ModelID:   modelID,
		Passed:    true,
		LatencyMs: latency,
	}, nil
}

// RunBenchmark runs a comprehensive benchmark suite on a model
func (s *ModelManagementService) RunBenchmark(ctx context.Context, modelID string) (*BenchmarkResults, error) {
	log.Printf("📊 [MODEL-MGMT] Starting benchmark suite for model: %s", modelID)

	model, err := s.GetModelByID(modelID)
	if err != nil {
		log.Printf("❌ [MODEL-MGMT] Failed to get model %s: %v", modelID, err)
		return nil, fmt.Errorf("model not found: %w", err)
	}

	provider, err := s.getProviderByID(model.ProviderID)
	if err != nil {
		log.Printf("❌ [MODEL-MGMT] Failed to get provider %d: %v", model.ProviderID, err)
		return nil, fmt.Errorf("provider not found: %w", err)
	}

	log.Printf("   Provider: %s (%s)", provider.Name, provider.BaseURL)

	results := &BenchmarkResults{
		LastTested: time.Now().Format(time.RFC3339),
	}

	// 1. Run connection test
	log.Printf("   [1/3] Running connection test...")
	connResult, err := s.TestModelConnection(ctx, modelID)
	if err == nil {
		results.ConnectionTest = connResult
		log.Printf("   ✓ Connection test complete")
	} else {
		log.Printf("   ✗ Connection test failed: %v", err)
	}

	// 2. Run structured output test
	log.Printf("   [2/3] Running structured output test (5 prompts)...")
	structuredResult, err := s.testStructuredOutput(ctx, modelID, provider)
	if err == nil {
		results.StructuredOutput = structuredResult
		log.Printf("   ✓ Structured output test complete")
	} else {
		log.Printf("   ✗ Structured output test failed: %v", err)
	}

	// 3. Run performance test
	log.Printf("   [3/3] Running performance test (3 prompts)...")
	perfResult, err := s.testPerformance(ctx, modelID, provider)
	if err == nil {
		results.Performance = perfResult
		log.Printf("   ✓ Performance test complete")
	} else {
		log.Printf("   ✗ Performance test failed: %v", err)
	}

	// 4. Update database with benchmark results
	if results.StructuredOutput != nil {
		_, err = s.db.Exec(`
			UPDATE model_capabilities
			SET structured_output_compliance = ?,
				structured_output_speed_ms = ?,
				benchmark_date = ?
			WHERE model_id = ? AND provider_id = ?
		`, results.StructuredOutput.CompliancePercentage,
			results.StructuredOutput.AverageSpeedMs,
			time.Now(),
			modelID,
			model.ProviderID)

		if err != nil {
			log.Printf("⚠️  [MODEL-MGMT] Failed to update benchmark results in DB: %v", err)
		}
	}

	log.Printf("✅ [MODEL-MGMT] Benchmark suite completed for %s", modelID)
	return results, nil
}

// testStructuredOutput tests JSON schema compliance
func (s *ModelManagementService) testStructuredOutput(ctx context.Context, modelID string, provider *models.Provider) (*StructuredOutputBenchmark, error) {
	log.Printf("🧪 [MODEL-MGMT] Testing structured output for model: %s at %s", modelID, provider.BaseURL)

	testPrompts := []string{
		`Generate a JSON object with fields: name (string), age (number), active (boolean)`,
		`Create a JSON array with 3 objects, each with id and title fields`,
		`Output JSON with nested structure: user { profile { name, email } }`,
		`Return JSON with array field "tags" containing 5 strings`,
		`Generate JSON matching: { count: number, items: string[] }`,
	}

	passedTests := 0
	totalLatency := 0
	totalTests := len(testPrompts)
	failureReasons := []string{}

	client := &http.Client{Timeout: 60 * time.Second}

	for i, prompt := range testPrompts {
		start := time.Now()

		reqBody := map[string]interface{}{
			"model": modelID,
			"messages": []map[string]string{
				{"role": "user", "content": prompt},
			},
			"max_tokens": 200,
		}

		jsonData, _ := json.Marshal(reqBody)
		req, err := http.NewRequest("POST", provider.BaseURL+"/chat/completions", bytes.NewBuffer(jsonData))
		if err != nil {
			failureReasons = append(failureReasons, fmt.Sprintf("Test %d: request creation failed - %v", i+1, err))
			continue
		}

		req.Header.Set("Authorization", "Bearer "+provider.APIKey)
		req.Header.Set("Content-Type", "application/json")

		resp, err := client.Do(req)
		if err != nil {
			failureReasons = append(failureReasons, fmt.Sprintf("Test %d: HTTP request failed - %v", i+1, err))
			continue
		}

		latency := int(time.Since(start).Milliseconds())
		totalLatency += latency

		if resp.StatusCode == http.StatusOK {
			// Check if response is valid JSON
			body, _ := io.ReadAll(resp.Body)
			var result map[string]interface{}
			if json.Unmarshal(body, &result) == nil {
				passedTests++
				log.Printf("   ✓ Test %d passed (latency: %dms)", i+1, latency)
			} else {
				failureReasons = append(failureReasons, fmt.Sprintf("Test %d: invalid JSON response", i+1))
			}
		} else {
			body, _ := io.ReadAll(resp.Body)
			failureReasons = append(failureReasons, fmt.Sprintf("Test %d: HTTP %d - %s", i+1, resp.StatusCode, string(body)))
		}
		resp.Body.Close()
	}

	if len(failureReasons) > 0 {
		log.Printf("⚠️  [MODEL-MGMT] Structured output test failures: %v", failureReasons)
	}

	compliancePercentage := (passedTests * 100) / totalTests
	avgSpeedMs := 0
	if totalLatency > 0 && passedTests > 0 {
		avgSpeedMs = totalLatency / passedTests
	}

	qualityLevel := "poor"
	if compliancePercentage >= 90 {
		qualityLevel = "excellent"
	} else if compliancePercentage >= 75 {
		qualityLevel = "good"
	} else if compliancePercentage >= 50 {
		qualityLevel = "fair"
	}

	log.Printf("   📊 Results: %d/%d passed (%d%%), avg speed: %dms, quality: %s",
		passedTests, totalTests, compliancePercentage, avgSpeedMs, qualityLevel)

	return &StructuredOutputBenchmark{
		CompliancePercentage: compliancePercentage,
		AverageSpeedMs:       avgSpeedMs,
		QualityLevel:         qualityLevel,
		TestsPassed:          passedTests,
		TestsFailed:          totalTests - passedTests,
	}, nil
}

// testPerformance tests model performance metrics
func (s *ModelManagementService) testPerformance(ctx context.Context, modelID string, provider *models.Provider) (*PerformanceBenchmark, error) {
	log.Printf("⚡ [MODEL-MGMT] Testing performance for model: %s", modelID)

	testPrompt := "Write a detailed explanation of machine learning in 200 words."
	numTests := 3
	totalLatency := 0
	totalTokens := 0

	client := &http.Client{Timeout: 60 * time.Second}

	for i := 0; i < numTests; i++ {
		start := time.Now()

		reqBody := map[string]interface{}{
			"model": modelID,
			"messages": []map[string]string{
				{"role": "user", "content": testPrompt},
			},
			"max_tokens": 300,
		}

		jsonData, _ := json.Marshal(reqBody)
		req, err := http.NewRequest("POST", provider.BaseURL+"/chat/completions", bytes.NewBuffer(jsonData))
		if err != nil {
			continue
		}

		req.Header.Set("Authorization", "Bearer "+provider.APIKey)
		req.Header.Set("Content-Type", "application/json")

		resp, err := client.Do(req)
		if err != nil {
			continue
		}

		latency := int(time.Since(start).Milliseconds())
		totalLatency += latency

		if resp.StatusCode == http.StatusOK {
			body, _ := io.ReadAll(resp.Body)
			var result map[string]interface{}
			if json.Unmarshal(body, &result) == nil {
				if usage, ok := result["usage"].(map[string]interface{}); ok {
					if completionTokens, ok := usage["completion_tokens"].(float64); ok {
						totalTokens += int(completionTokens)
					}
				}
			}
		}
		resp.Body.Close()
	}

	avgLatencyMs := totalLatency / numTests
	avgTokens := float64(totalTokens) / float64(numTests)
	tokensPerSecond := (avgTokens / float64(avgLatencyMs)) * 1000

	return &PerformanceBenchmark{
		TokensPerSecond: tokensPerSecond,
		AvgLatencyMs:    avgLatencyMs,
		TestedAt:        time.Now().Format(time.RFC3339),
	}, nil
}

// ================== ALIAS MANAGEMENT ==================

// CreateAlias creates a new model alias
func (s *ModelManagementService) CreateAlias(ctx context.Context, req *CreateAliasRequest) error {
	log.Printf("📝 [MODEL-MGMT] Creating alias: %s -> %s (provider %d)", req.AliasName, req.ModelID, req.ProviderID)

	// Begin transaction
	tx, err := s.db.Begin()
	if err != nil {
		return fmt.Errorf("failed to begin transaction: %w", err)
	}
	defer tx.Rollback()

	// Convert empty string to NULL for ENUM fields
	var structuredOutputSupport interface{}
	if req.StructuredOutputSupport == "" {
		structuredOutputSupport = nil
	} else {
		structuredOutputSupport = req.StructuredOutputSupport
	}

	// Convert empty strings to NULL for optional text fields
	var structuredOutputWarning interface{}
	if req.StructuredOutputWarning == "" {
		structuredOutputWarning = nil
	} else {
		structuredOutputWarning = req.StructuredOutputWarning
	}

	var structuredOutputBadge interface{}
	if req.StructuredOutputBadge == "" {
		structuredOutputBadge = nil
	} else {
		structuredOutputBadge = req.StructuredOutputBadge
	}

	var description interface{}
	if req.Description == "" {
		description = nil
	} else {
		description = req.Description
	}

	// Insert alias
	_, err = tx.Exec(`
		INSERT INTO model_aliases (alias_name, model_id, provider_id, display_name, description,
			supports_vision, agents_enabled, smart_tool_router, free_tier,
			structured_output_support, structured_output_compliance, structured_output_warning,
			structured_output_speed_ms, structured_output_badge, memory_extractor, memory_selector)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
	`, req.AliasName, req.ModelID, req.ProviderID, req.DisplayName, description,
		req.SupportsVision, req.AgentsEnabled, req.SmartToolRouter, req.FreeTier,
		structuredOutputSupport, req.StructuredOutputCompliance, structuredOutputWarning,
		req.StructuredOutputSpeedMs, structuredOutputBadge, req.MemoryExtractor, req.MemorySelector)

	if err != nil {
		return fmt.Errorf("failed to insert alias: %w", err)
	}

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("failed to commit transaction: %w", err)
	}

	// Reload config service cache with updated aliases from database
	if err := s.reloadConfigServiceCache(); err != nil {
		log.Printf("⚠️  [MODEL-MGMT] Failed to reload config cache: %v", err)
	}

	log.Printf("✅ [MODEL-MGMT] Created alias: %s", req.AliasName)
	return nil
}

// DeleteAlias deletes a model alias
func (s *ModelManagementService) DeleteAlias(ctx context.Context, aliasName string, providerID int) error {
	log.Printf("🗑️  [MODEL-MGMT] Deleting alias: %s (provider %d)", aliasName, providerID)

	tx, err := s.db.Begin()
	if err != nil {
		return fmt.Errorf("failed to begin transaction: %w", err)
	}
	defer tx.Rollback()

	result, err := tx.Exec("DELETE FROM model_aliases WHERE alias_name = ? AND provider_id = ?", aliasName, providerID)
	if err != nil {
		return fmt.Errorf("failed to delete alias: %w", err)
	}

	rowsAffected, _ := result.RowsAffected()
	if rowsAffected == 0 {
		return fmt.Errorf("alias not found: %s", aliasName)
	}

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("failed to commit transaction: %w", err)
	}

	// Reload config service cache with updated aliases from database
	if err := s.reloadConfigServiceCache(); err != nil {
		log.Printf("⚠️  [MODEL-MGMT] Failed to reload config cache: %v", err)
	}

	log.Printf("✅ [MODEL-MGMT] Deleted alias: %s", aliasName)
	return nil
}

// GetAliases retrieves all aliases for a model
func (s *ModelManagementService) GetAliases(ctx context.Context, modelID string) ([]models.ModelAliasView, error) {
	log.Printf("🔍 [MODEL-MGMT] Fetching aliases for model: %s", modelID)

	rows, err := s.db.Query(`
		SELECT id, alias_name, model_id, provider_id, display_name, description,
		       supports_vision, agents_enabled, smart_tool_router, free_tier,
		       structured_output_support, structured_output_compliance, structured_output_warning,
		       structured_output_speed_ms, structured_output_badge, memory_extractor, memory_selector,
		       created_at, updated_at
		FROM model_aliases
		WHERE model_id = ?
		ORDER BY created_at DESC
	`, modelID)

	if err != nil {
		return nil, fmt.Errorf("failed to query aliases: %w", err)
	}
	defer rows.Close()

	var aliases []models.ModelAliasView
	for rows.Next() {
		var alias models.ModelAliasView
		var description, structuredOutputSupport, structuredOutputWarning, structuredOutputBadge sql.NullString
		var structuredOutputCompliance, structuredOutputSpeedMs sql.NullInt64
		var supportsVision, agentsEnabled, smartToolRouter, freeTier, memoryExtractor, memorySelector sql.NullBool

		err := rows.Scan(
			&alias.ID, &alias.AliasName, &alias.ModelID, &alias.ProviderID, &alias.DisplayName, &description,
			&supportsVision, &agentsEnabled, &smartToolRouter, &freeTier,
			&structuredOutputSupport, &structuredOutputCompliance, &structuredOutputWarning,
			&structuredOutputSpeedMs, &structuredOutputBadge, &memoryExtractor, &memorySelector,
			&alias.CreatedAt, &alias.UpdatedAt,
		)

		if err != nil {
			return nil, fmt.Errorf("failed to scan alias: %w", err)
		}

		// Handle nullable fields
		if description.Valid {
			alias.Description = &description.String
		}
		if supportsVision.Valid {
			alias.SupportsVision = &supportsVision.Bool
		}
		if agentsEnabled.Valid {
			alias.AgentsEnabled = &agentsEnabled.Bool
		}
		if smartToolRouter.Valid {
			alias.SmartToolRouter = &smartToolRouter.Bool
		}
		if freeTier.Valid {
			alias.FreeTier = &freeTier.Bool
		}
		if structuredOutputSupport.Valid {
			alias.StructuredOutputSupport = &structuredOutputSupport.String
		}
		if structuredOutputCompliance.Valid {
			compliance := int(structuredOutputCompliance.Int64)
			alias.StructuredOutputCompliance = &compliance
		}
		if structuredOutputWarning.Valid {
			alias.StructuredOutputWarning = &structuredOutputWarning.String
		}
		if structuredOutputSpeedMs.Valid {
			speed := int(structuredOutputSpeedMs.Int64)
			alias.StructuredOutputSpeedMs = &speed
		}
		if structuredOutputBadge.Valid {
			alias.StructuredOutputBadge = &structuredOutputBadge.String
		}
		if memoryExtractor.Valid {
			alias.MemoryExtractor = &memoryExtractor.Bool
		}
		if memorySelector.Valid {
			alias.MemorySelector = &memorySelector.Bool
		}

		aliases = append(aliases, alias)
	}

	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("error iterating aliases: %w", err)
	}

	log.Printf("✅ [MODEL-MGMT] Found %d aliases for model %s", len(aliases), modelID)
	return aliases, nil
}

// ImportAliasesFromJSON imports all aliases from providers.json into the database
func (s *ModelManagementService) ImportAliasesFromJSON(ctx context.Context) error {
	log.Printf("📥 [MODEL-MGMT] Starting import of aliases from providers.json to database...")

	// Read providers.json
	data, err := os.ReadFile(s.providersFile)
	if err != nil {
		return fmt.Errorf("failed to read providers.json: %w", err)
	}

	var cfg models.ProvidersConfig
	if err := json.Unmarshal(data, &cfg); err != nil {
		return fmt.Errorf("failed to parse providers.json: %w", err)
	}

	totalImported := 0
	totalSkipped := 0

	// Iterate through all providers
	for _, provider := range cfg.Providers {
		// Get provider ID from database
		var providerID int
		err := s.db.QueryRow(`SELECT id FROM providers WHERE name = ?`, provider.Name).Scan(&providerID)
		if err != nil {
			log.Printf("⚠️  [MODEL-MGMT] Provider %s not found in database, skipping aliases", provider.Name)
			continue
		}

		// Iterate through all model_aliases for this provider
		for aliasName, aliasConfig := range provider.ModelAliases {
			// Check if alias already exists
			var existingID int
			err := s.db.QueryRow(`SELECT id FROM model_aliases WHERE alias_name = ? AND provider_id = ?`,
				aliasName, providerID).Scan(&existingID)

			if err == nil {
				// Alias already exists, skip
				totalSkipped++
				continue
			}

			// Extract values from aliasConfig
			modelID := aliasConfig.ActualModel
			displayName := aliasConfig.DisplayName
			description := aliasConfig.Description
			supportsVision := aliasConfig.SupportsVision
			agentsEnabled := aliasConfig.Agents
			smartToolRouter := aliasConfig.SmartToolRouter
			freeTier := aliasConfig.FreeTier
			structuredOutputSupport := aliasConfig.StructuredOutputSupport
			structuredOutputCompliance := aliasConfig.StructuredOutputCompliance
			structuredOutputWarning := aliasConfig.StructuredOutputWarning
			structuredOutputSpeedMs := aliasConfig.StructuredOutputSpeedMs
			structuredOutputBadge := aliasConfig.StructuredOutputBadge
			memoryExtractor := aliasConfig.MemoryExtractor
			memorySelector := aliasConfig.MemorySelector

			// Insert alias into database
			_, err = s.db.Exec(`
				INSERT INTO model_aliases (alias_name, model_id, provider_id, display_name, description,
					supports_vision, agents_enabled, smart_tool_router, free_tier,
					structured_output_support, structured_output_compliance, structured_output_warning,
					structured_output_speed_ms, structured_output_badge, memory_extractor, memory_selector)
				VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
			`, aliasName, modelID, providerID, displayName, description,
				supportsVision, agentsEnabled, smartToolRouter, freeTier,
				structuredOutputSupport, structuredOutputCompliance, structuredOutputWarning,
				structuredOutputSpeedMs, structuredOutputBadge, memoryExtractor, memorySelector)

			if err != nil {
				log.Printf("⚠️  [MODEL-MGMT] Failed to import alias %s: %v", aliasName, err)
				continue
			}

			totalImported++
			log.Printf("   ✓ Imported alias: %s -> %s (provider: %s)", aliasName, modelID, provider.Name)
		}
	}

	log.Printf("✅ [MODEL-MGMT] Import complete: %d aliases imported, %d skipped (already exist)", totalImported, totalSkipped)
	return nil
}

// ================== HELPER METHODS ==================

// GetModelByID retrieves a model by ID
func (s *ModelManagementService) GetModelByID(modelID string) (*models.Model, error) {
	var m models.Model
	var displayName, description, systemPrompt, providerFavicon sql.NullString
	var contextLength sql.NullInt64
	var fetchedAt sql.NullTime

	err := s.db.QueryRow(`
		SELECT m.id, m.provider_id, p.name as provider_name, p.favicon as provider_favicon,
		       m.name, m.display_name, m.description, m.context_length, m.supports_tools,
		       m.supports_streaming, m.supports_vision, m.smart_tool_router, m.is_visible, m.system_prompt, m.fetched_at
		FROM models m
		JOIN providers p ON m.provider_id = p.id
		WHERE m.id = ?
	`, modelID).Scan(&m.ID, &m.ProviderID, &m.ProviderName, &providerFavicon,
		&m.Name, &displayName, &description, &contextLength, &m.SupportsTools,
		&m.SupportsStreaming, &m.SupportsVision, &m.SmartToolRouter, &m.IsVisible, &systemPrompt, &fetchedAt)

	if err == sql.ErrNoRows {
		return nil, fmt.Errorf("model not found: %s", modelID)
	}
	if err != nil {
		return nil, fmt.Errorf("failed to query model: %w", err)
	}

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

	return &m, nil
}

// getProviderByID retrieves a provider by ID
func (s *ModelManagementService) getProviderByID(id int) (*models.Provider, error) {
	var p models.Provider
	var systemPrompt, favicon sql.NullString
	err := s.db.QueryRow(`
		SELECT id, name, base_url, api_key, enabled, audio_only, system_prompt, favicon, created_at, updated_at
		FROM providers
		WHERE id = ?
	`, id).Scan(&p.ID, &p.Name, &p.BaseURL, &p.APIKey, &p.Enabled, &p.AudioOnly, &systemPrompt, &favicon, &p.CreatedAt, &p.UpdatedAt)

	if err == sql.ErrNoRows {
		return nil, fmt.Errorf("provider not found")
	}
	if err != nil {
		return nil, fmt.Errorf("failed to query provider: %w", err)
	}

	if systemPrompt.Valid {
		p.SystemPrompt = systemPrompt.String
	}
	if favicon.Valid {
		p.Favicon = favicon.String
	}

	return &p, nil
}

// ================== REQUEST/RESPONSE TYPES ==================

// CreateModelRequest represents a request to create a new model
type CreateModelRequest struct {
	ModelID           string
	ProviderID        int
	Name              string
	DisplayName       string
	Description       string
	ContextLength     int
	SupportsTools     bool
	SupportsStreaming bool
	SupportsVision    bool
	IsVisible         bool
	SystemPrompt      string
}

// UpdateModelRequest represents a request to update a model
type UpdateModelRequest struct {
	DisplayName       *string
	Description       *string
	ContextLength     *int
	SupportsTools     *bool
	SupportsStreaming *bool
	SupportsVision    *bool
	IsVisible         *bool
	SystemPrompt      *string
	SmartToolRouter   *bool
	FreeTier          *bool
}

// CreateAliasRequest represents a request to create a model alias
type CreateAliasRequest struct {
	AliasName                  string `json:"alias_name"`
	ModelID                    string `json:"model_id"`
	ProviderID                 int    `json:"provider_id"`
	DisplayName                string `json:"display_name"`
	Description                string `json:"description"`
	SupportsVision             *bool  `json:"supports_vision"`
	AgentsEnabled              *bool  `json:"agents_enabled"`
	SmartToolRouter            *bool  `json:"smart_tool_router"`
	FreeTier                   *bool  `json:"free_tier"`
	StructuredOutputSupport    string `json:"structured_output_support"`
	StructuredOutputCompliance *int   `json:"structured_output_compliance"`
	StructuredOutputWarning    string `json:"structured_output_warning"`
	StructuredOutputSpeedMs    *int   `json:"structured_output_speed_ms"`
	StructuredOutputBadge      string `json:"structured_output_badge"`
	MemoryExtractor            *bool  `json:"memory_extractor"`
	MemorySelector             *bool  `json:"memory_selector"`
}

// ConnectionTestResult represents the result of a connection test
type ConnectionTestResult struct {
	ModelID   string
	Passed    bool
	LatencyMs int
	Error     string
}

// StructuredOutputBenchmark represents structured output test results
type StructuredOutputBenchmark struct {
	CompliancePercentage int    `json:"compliance_percentage"`
	AverageSpeedMs       int    `json:"average_speed_ms"`
	QualityLevel         string `json:"quality_level"`
	TestsPassed          int    `json:"tests_passed"`
	TestsFailed          int    `json:"tests_failed"`
}

// PerformanceBenchmark represents performance test results
type PerformanceBenchmark struct {
	TokensPerSecond float64 `json:"tokens_per_second"`
	AvgLatencyMs    int     `json:"avg_latency_ms"`
	TestedAt        string  `json:"tested_at"`
}

// BenchmarkResults represents comprehensive benchmark test results
type BenchmarkResults struct {
	ConnectionTest   *ConnectionTestResult      `json:"connection_test,omitempty"`
	StructuredOutput *StructuredOutputBenchmark `json:"structured_output,omitempty"`
	Performance      *PerformanceBenchmark      `json:"performance,omitempty"`
	LastTested       string                     `json:"last_tested,omitempty"`
}

// ================== GLOBAL TIER MANAGEMENT ==================

// TierAssignment represents a model assigned to a global tier
type TierAssignment struct {
	ModelID     string `json:"model_id"`
	ProviderID  int    `json:"provider_id"`
	DisplayName string `json:"display_name"`
	Tier        string `json:"tier"`
}

// SetGlobalTier assigns a model to a global tier (tier1-tier5)
// Only one model can occupy each tier slot
func (s *ModelManagementService) SetGlobalTier(modelID string, providerID int, tier string) error {
	// Validate tier value
	validTiers := map[string]bool{
		"tier1": true,
		"tier2": true,
		"tier3": true,
		"tier4": true,
		"tier5": true,
	}

	if !validTiers[tier] {
		return fmt.Errorf("invalid tier: %s (must be tier1, tier2, tier3, tier4, or tier5)", tier)
	}

	// Check if model exists
	var displayName string
	err := s.db.QueryRow("SELECT display_name FROM models WHERE id = ? AND provider_id = ?", modelID, providerID).Scan(&displayName)
	if err == sql.ErrNoRows {
		return fmt.Errorf("model not found: %s", modelID)
	}
	if err != nil {
		return fmt.Errorf("failed to verify model: %w", err)
	}

	// Use model ID as alias if display name is empty
	alias := modelID
	if displayName != "" {
		alias = displayName
	}

	// Try to insert (will fail if tier already occupied due to unique constraint)
	_, err = s.db.Exec(`
		INSERT INTO recommended_models (provider_id, tier, model_alias)
		VALUES (?, ?, ?)
		ON DUPLICATE KEY UPDATE
			provider_id = VALUES(provider_id),
			model_alias = VALUES(model_alias),
			updated_at = CURRENT_TIMESTAMP
	`, providerID, tier, alias)

	if err != nil {
		return fmt.Errorf("failed to set tier: %w", err)
	}

	log.Printf("✅ [TIER] Assigned %s to %s", alias, tier)
	return nil
}

// GetGlobalTiers retrieves all 5 tier assignments
func (s *ModelManagementService) GetGlobalTiers() (map[string]*TierAssignment, error) {
	rows, err := s.db.Query(`
		SELECT r.tier, r.provider_id, r.model_alias, m.id as model_id, m.display_name
		FROM recommended_models r
		LEFT JOIN models m ON r.provider_id = m.provider_id AND (m.display_name = r.model_alias OR m.id = r.model_alias)
		ORDER BY r.tier
	`)
	if err != nil {
		return nil, fmt.Errorf("failed to query tiers: %w", err)
	}
	defer rows.Close()

	tiers := make(map[string]*TierAssignment)

	for rows.Next() {
		var tier, modelAlias, modelID string
		var providerID int
		var displayName sql.NullString

		err := rows.Scan(&tier, &providerID, &modelAlias, &modelID, &displayName)
		if err != nil {
			log.Printf("⚠️  Failed to scan tier: %v", err)
			continue
		}

		assignment := &TierAssignment{
			ModelID:     modelID,
			ProviderID:  providerID,
			DisplayName: modelAlias,
			Tier:        tier,
		}

		if displayName.Valid && displayName.String != "" {
			assignment.DisplayName = displayName.String
		}

		tiers[tier] = assignment
	}

	return tiers, nil
}

// ClearTier removes a model from a tier
func (s *ModelManagementService) ClearTier(tier string) error {
	// Validate tier
	validTiers := map[string]bool{
		"tier1": true,
		"tier2": true,
		"tier3": true,
		"tier4": true,
		"tier5": true,
	}

	if !validTiers[tier] {
		return fmt.Errorf("invalid tier: %s", tier)
	}

	result, err := s.db.Exec("DELETE FROM recommended_models WHERE tier = ?", tier)
	if err != nil {
		return fmt.Errorf("failed to clear tier: %w", err)
	}

	rowsAffected, _ := result.RowsAffected()
	if rowsAffected == 0 {
		return fmt.Errorf("tier %s is already empty", tier)
	}

	log.Printf("✅ [TIER] Cleared %s", tier)
	return nil
}

// BulkUpdateAgentsEnabled updates agents_enabled for multiple models
func (s *ModelManagementService) BulkUpdateAgentsEnabled(modelIDs []string, enabled bool) error {
	if len(modelIDs) == 0 {
		return fmt.Errorf("no model IDs provided")
	}

	// Build placeholders for IN clause
	placeholders := make([]string, len(modelIDs))
	args := make([]interface{}, len(modelIDs)+1)
	args[0] = enabled

	for i, modelID := range modelIDs {
		placeholders[i] = "?"
		args[i+1] = modelID
	}

	query := fmt.Sprintf(`
		UPDATE models
		SET agents_enabled = ?
		WHERE id IN (%s)
	`, strings.Join(placeholders, ","))

	result, err := s.db.Exec(query, args...)
	if err != nil {
		return fmt.Errorf("failed to bulk update agents_enabled: %w", err)
	}

	rowsAffected, _ := result.RowsAffected()
	log.Printf("✅ [BULK] Updated agents_enabled=%v for %d models", enabled, rowsAffected)
	return nil
}

// BulkUpdateVisibility bulk shows/hides models from users
func (s *ModelManagementService) BulkUpdateVisibility(modelIDs []string, visible bool) error {
	if len(modelIDs) == 0 {
		return fmt.Errorf("no model IDs provided")
	}

	// Build placeholders for IN clause
	placeholders := make([]string, len(modelIDs))
	args := make([]interface{}, len(modelIDs)+1)
	args[0] = visible

	for i, modelID := range modelIDs {
		placeholders[i] = "?"
		args[i+1] = modelID
	}

	query := fmt.Sprintf(`
		UPDATE models
		SET is_visible = ?
		WHERE id IN (%s)
	`, strings.Join(placeholders, ","))

	result, err := s.db.Exec(query, args...)
	if err != nil {
		return fmt.Errorf("failed to bulk update visibility: %w", err)
	}

	rowsAffected, _ := result.RowsAffected()
	log.Printf("✅ [BULK] Updated is_visible=%v for %d models", visible, rowsAffected)
	return nil
}

// ================== UTILITY FUNCTIONS ==================

// joinStrings joins a slice of strings with a separator
func joinStrings(parts []string, sep string) string {
	if len(parts) == 0 {
		return ""
	}
	result := parts[0]
	for i := 1; i < len(parts); i++ {
		result += sep + parts[i]
	}
	return result
}
