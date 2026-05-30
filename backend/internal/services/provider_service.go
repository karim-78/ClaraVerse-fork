package services

import (
	"claraverse/internal/database"
	"claraverse/internal/models"
	"database/sql"
	"fmt"
	"log"
	"path/filepath"
	"strings"
)

// ProviderService handles provider operations
type ProviderService struct {
	db *database.DB
}

// NewProviderService creates a new provider service
func NewProviderService(db *database.DB) *ProviderService {
	return &ProviderService{db: db}
}

// GetAll returns all enabled providers
func (s *ProviderService) GetAll() ([]models.Provider, error) {
	rows, err := s.db.Query(`
		SELECT id, name, base_url, api_key, enabled, audio_only, image_only, image_edit_only, secure, default_model, system_prompt, favicon, created_at, updated_at
		FROM providers
		WHERE enabled = 1
		ORDER BY name
	`)
	if err != nil {
		return nil, fmt.Errorf("failed to query providers: %w", err)
	}
	defer rows.Close()

	var providers []models.Provider
	for rows.Next() {
		var p models.Provider
		var systemPrompt, favicon, defaultModel sql.NullString
		if err := rows.Scan(&p.ID, &p.Name, &p.BaseURL, &p.APIKey, &p.Enabled, &p.AudioOnly, &p.ImageOnly, &p.ImageEditOnly, &p.Secure, &defaultModel, &systemPrompt, &favicon, &p.CreatedAt, &p.UpdatedAt); err != nil {
			return nil, fmt.Errorf("failed to scan provider: %w", err)
		}
		if systemPrompt.Valid {
			p.SystemPrompt = systemPrompt.String
		}
		if favicon.Valid {
			p.Favicon = favicon.String
		}
		if defaultModel.Valid {
			p.DefaultModel = defaultModel.String
		}
		providers = append(providers, p)
	}

	return providers, nil
}

// GetAllForModels returns all enabled providers that are NOT audio-only (for model selection)
func (s *ProviderService) GetAllForModels() ([]models.Provider, error) {
	rows, err := s.db.Query(`
		SELECT id, name, base_url, api_key, enabled, audio_only, image_only, image_edit_only, secure, default_model, system_prompt, favicon, created_at, updated_at
		FROM providers
		WHERE enabled = 1 AND (audio_only = 0 OR audio_only IS NULL) AND (image_only = 0 OR image_only IS NULL) AND (image_edit_only = 0 OR image_edit_only IS NULL)
		ORDER BY name
	`)
	if err != nil {
		return nil, fmt.Errorf("failed to query providers: %w", err)
	}
	defer rows.Close()

	var providers []models.Provider
	for rows.Next() {
		var p models.Provider
		var systemPrompt, favicon, defaultModel sql.NullString
		if err := rows.Scan(&p.ID, &p.Name, &p.BaseURL, &p.APIKey, &p.Enabled, &p.AudioOnly, &p.ImageOnly, &p.ImageEditOnly, &p.Secure, &defaultModel, &systemPrompt, &favicon, &p.CreatedAt, &p.UpdatedAt); err != nil {
			return nil, fmt.Errorf("failed to scan provider: %w", err)
		}
		if systemPrompt.Valid {
			p.SystemPrompt = systemPrompt.String
		}
		if favicon.Valid {
			p.Favicon = favicon.String
		}
		if defaultModel.Valid {
			p.DefaultModel = defaultModel.String
		}
		providers = append(providers, p)
	}

	return providers, nil
}

// GetByID returns a provider by ID
func (s *ProviderService) GetByID(id int) (*models.Provider, error) {
	var p models.Provider
	var systemPrompt, favicon, defaultModel sql.NullString
	err := s.db.QueryRow(`
		SELECT id, name, base_url, api_key, enabled, audio_only, image_only, image_edit_only, secure, default_model, system_prompt, favicon, created_at, updated_at
		FROM providers
		WHERE id = ?
	`, id).Scan(&p.ID, &p.Name, &p.BaseURL, &p.APIKey, &p.Enabled, &p.AudioOnly, &p.ImageOnly, &p.ImageEditOnly, &p.Secure, &defaultModel, &systemPrompt, &favicon, &p.CreatedAt, &p.UpdatedAt)

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
	if defaultModel.Valid {
		p.DefaultModel = defaultModel.String
	}

	return &p, nil
}

// GetByName returns a provider by name
func (s *ProviderService) GetByName(name string) (*models.Provider, error) {
	var p models.Provider
	var systemPrompt, favicon, defaultModel sql.NullString
	err := s.db.QueryRow(`
		SELECT id, name, base_url, api_key, enabled, audio_only, image_only, image_edit_only, secure, default_model, system_prompt, favicon, created_at, updated_at
		FROM providers
		WHERE name = ?
	`, name).Scan(&p.ID, &p.Name, &p.BaseURL, &p.APIKey, &p.Enabled, &p.AudioOnly, &p.ImageOnly, &p.ImageEditOnly, &p.Secure, &defaultModel, &systemPrompt, &favicon, &p.CreatedAt, &p.UpdatedAt)

	if err == sql.ErrNoRows {
		return nil, nil // Not found, not an error
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
	if defaultModel.Valid {
		p.DefaultModel = defaultModel.String
	}

	return &p, nil
}

// Create creates a new provider
func (s *ProviderService) Create(config models.ProviderConfig) (*models.Provider, error) {
	result, err := s.db.Exec(`
		INSERT INTO providers (name, base_url, api_key, enabled, audio_only, image_only, image_edit_only, default_model, system_prompt, favicon)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
	`, config.Name, config.BaseURL, config.APIKey, config.Enabled, config.AudioOnly, config.ImageOnly, config.ImageEditOnly, config.DefaultModel, config.SystemPrompt, config.Favicon)

	if err != nil {
		return nil, fmt.Errorf("failed to create provider: %w", err)
	}

	id, err := result.LastInsertId()
	if err != nil {
		return nil, fmt.Errorf("failed to get inserted ID: %w", err)
	}

	log.Printf("   ✅ Created provider %s with ID %d", config.Name, id)
	return s.GetByID(int(id))
}

// Update updates an existing provider
func (s *ProviderService) Update(id int, config models.ProviderConfig) error {
	_, err := s.db.Exec(`
		UPDATE providers
		SET base_url = ?, api_key = ?, enabled = ?, audio_only = ?, image_only = ?, image_edit_only = ?,
		    default_model = ?, system_prompt = ?, favicon = ?, updated_at = CURRENT_TIMESTAMP
		WHERE id = ?
	`, config.BaseURL, config.APIKey, config.Enabled, config.AudioOnly, config.ImageOnly, config.ImageEditOnly,
		config.DefaultModel, config.SystemPrompt, config.Favicon, id)

	if err != nil {
		return fmt.Errorf("failed to update provider: %w", err)
	}

	log.Printf("   ✅ Updated provider %s (ID %d)", config.Name, id)
	return nil
}

// SyncFilters syncs filter configuration for a provider
func (s *ProviderService) SyncFilters(providerID int, filters []models.FilterConfig) error {
	// Delete old filters
	if _, err := s.db.Exec("DELETE FROM provider_model_filters WHERE provider_id = ?", providerID); err != nil {
		return fmt.Errorf("failed to delete old filters: %w", err)
	}

	// Insert new filters
	for _, filter := range filters {
		_, err := s.db.Exec(`
			INSERT INTO provider_model_filters (provider_id, model_pattern, action, priority)
			VALUES (?, ?, ?, ?)
		`, providerID, filter.Pattern, filter.Action, filter.Priority)

		if err != nil {
			return fmt.Errorf("failed to insert filter: %w", err)
		}

		log.Printf("      ✓ Added filter: %s (%s)", filter.Pattern, filter.Action)
	}

	return nil
}

// ApplyFilters applies filter rules to models for a provider
func (s *ProviderService) ApplyFilters(providerID int) error {
	// Get filters for this provider ordered by priority (higher first)
	rows, err := s.db.Query(`
		SELECT model_pattern, action
		FROM provider_model_filters
		WHERE provider_id = ?
		ORDER BY priority DESC, id ASC
	`, providerID)
	if err != nil {
		return fmt.Errorf("failed to query filters: %w", err)
	}
	defer rows.Close()

	var filters []struct {
		Pattern string
		Action  string
	}

	for rows.Next() {
		var f struct {
			Pattern string
			Action  string
		}
		if err := rows.Scan(&f.Pattern, &f.Action); err != nil {
			return fmt.Errorf("failed to scan filter: %w", err)
		}
		filters = append(filters, f)
	}

	if len(filters) == 0 {
		// No filters, show all models
		_, err := s.db.Exec(`
			UPDATE models
			SET is_visible = 1
			WHERE provider_id = ?
		`, providerID)
		return err
	}

	// Reset visibility
	if _, err := s.db.Exec("UPDATE models SET is_visible = 0 WHERE provider_id = ?", providerID); err != nil {
		return fmt.Errorf("failed to reset visibility: %w", err)
	}

	// Apply filters
	for _, filter := range filters {
		if filter.Action == "include" {
			// Match pattern using SQL LIKE (convert * to %)
			pattern := strings.ReplaceAll(filter.Pattern, "*", "%")
			_, err := s.db.Exec(`
				UPDATE models
				SET is_visible = 1
				WHERE provider_id = ? AND (name LIKE ? OR id LIKE ?)
			`, providerID, pattern, pattern)
			if err != nil {
				return fmt.Errorf("failed to apply include filter: %w", err)
			}
		} else if filter.Action == "exclude" {
			pattern := strings.ReplaceAll(filter.Pattern, "*", "%")
			_, err := s.db.Exec(`
				UPDATE models
				SET is_visible = 0
				WHERE provider_id = ? AND (name LIKE ? OR id LIKE ?)
			`, providerID, pattern, pattern)
			if err != nil {
				return fmt.Errorf("failed to apply exclude filter: %w", err)
			}
		}
	}

	return nil
}

// matchesPattern checks if a model name matches a wildcard pattern
func matchesPattern(name, pattern string) bool {
	matched, _ := filepath.Match(pattern, name)
	return matched
}

// GetByModelIDWithName returns both the provider and the model's `name`
// column (the upstream API model identifier with no row-id prefix). Use this
// in callers that need to forward the model name to a provider API — sending
// the row id will fail at providers like Bedrock with "invalid model identifier".
func (s *ProviderService) GetByModelIDWithName(modelID string) (*models.Provider, string, error) {
	var providerID int
	var modelName string
	err := s.db.QueryRow(`
		SELECT provider_id, name FROM models WHERE id = ?
	`, modelID).Scan(&providerID, &modelName)

	if err == sql.ErrNoRows {
		return nil, "", fmt.Errorf("model not found: %s", modelID)
	}
	if err != nil {
		return nil, "", fmt.Errorf("failed to query model: %w", err)
	}

	provider, err := s.GetByID(providerID)
	if err != nil {
		return nil, "", err
	}
	return provider, modelName, nil
}

// GetAllIncludingDisabled returns all providers including disabled ones
func (s *ProviderService) GetAllIncludingDisabled() ([]models.Provider, error) {
	rows, err := s.db.Query(`
		SELECT id, name, base_url, api_key, enabled, audio_only, image_only, image_edit_only, secure, default_model, system_prompt, favicon, created_at, updated_at
		FROM providers
		ORDER BY name
	`)
	if err != nil {
		return nil, fmt.Errorf("failed to query providers: %w", err)
	}
	defer rows.Close()

	var providers []models.Provider
	for rows.Next() {
		var p models.Provider
		var systemPrompt, favicon, defaultModel sql.NullString
		if err := rows.Scan(&p.ID, &p.Name, &p.BaseURL, &p.APIKey, &p.Enabled, &p.AudioOnly, &p.ImageOnly, &p.ImageEditOnly, &p.Secure, &defaultModel, &systemPrompt, &favicon, &p.CreatedAt, &p.UpdatedAt); err != nil {
			return nil, fmt.Errorf("failed to scan provider: %w", err)
		}
		if systemPrompt.Valid {
			p.SystemPrompt = systemPrompt.String
		}
		if favicon.Valid {
			p.Favicon = favicon.String
		}
		if defaultModel.Valid {
			p.DefaultModel = defaultModel.String
		}
		providers = append(providers, p)
	}

	return providers, nil
}

// Delete removes a provider and all its associated models from the database
func (s *ProviderService) Delete(id int) error {
	// Models are deleted automatically via ON DELETE CASCADE
	_, err := s.db.Exec(`DELETE FROM providers WHERE id = ?`, id)
	if err != nil {
		return fmt.Errorf("failed to delete provider: %w", err)
	}
	return nil
}

// GetFilters retrieves all filters for a provider
func (s *ProviderService) GetFilters(providerID int) ([]models.FilterConfig, error) {
	rows, err := s.db.Query(`
		SELECT model_pattern, action, priority
		FROM provider_model_filters
		WHERE provider_id = ?
		ORDER BY priority DESC, id ASC
	`, providerID)
	if err != nil {
		return nil, fmt.Errorf("failed to query filters: %w", err)
	}
	defer rows.Close()

	var filters []models.FilterConfig
	for rows.Next() {
		var f models.FilterConfig
		if err := rows.Scan(&f.Pattern, &f.Action, &f.Priority); err != nil {
			return nil, fmt.Errorf("failed to scan filter: %w", err)
		}
		filters = append(filters, f)
	}

	return filters, nil
}
