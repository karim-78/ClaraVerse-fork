// Admin endpoints for managing third-party integration credentials
// (Composio + E2B) from the UI instead of env vars.
//
// Why this exists: previously, enabling Composio Gmail meant setting
// COMPOSIO_API_KEY + COMPOSIO_GMAIL_AUTH_CONFIG_ID in the deployment
// env and restarting the backend. Self-hosted users running via
// `claraverse init` couldn't change keys without dropping to the
// docker compose file. This handler stores those values in the same
// DB-backed settings table the existing E2B admin uses, with
// `tools.SetSettingsBackend` wiring the runtime resolver so tools
// pick up the new values within the cache TTL (30s) without a
// restart.
//
// Auth: admin-only via the existing AdminMiddleware (gated at route
// registration in main.go). Keys are stored encrypted at rest by the
// SettingsService's own encryption layer.
//
// The endpoint returns MASKED values on GET (first 4 + last 4 chars
// with the middle redacted) so the UI can show "you've set this"
// without exposing the secret to anyone who can screenshot the
// admin page.
package handlers

import (
	"context"
	"log"
	"os"
	"strings"

	"claraverse/internal/services"
	"claraverse/internal/tools"

	"github.com/gofiber/fiber/v2"
)

// IntegrationsAdminHandler exposes the integration-credentials
// management surface. Backed by the same SettingsService that
// powers system-model assignments and E2B settings.
type IntegrationsAdminHandler struct {
	settingsService *services.SettingsService
}

// NewIntegrationsAdminHandler wires the handler.
func NewIntegrationsAdminHandler(s *services.SettingsService) *IntegrationsAdminHandler {
	return &IntegrationsAdminHandler{settingsService: s}
}

// integrationDef catalogs everything we expose. Adding a new
// integration is a one-row append here + a tool that calls
// tools.GetComposioAuthConfigID("new_slug").
//
// The order is the order the UI renders them in — keep Composio
// master first (it gates everything else), then alphabetical
// integrations, then E2B at the end.
type integrationDef struct {
	Key         string // settings table key
	EnvKey      string // env var name (for the help text + fallback)
	Label       string // UI display name
	Description string // 1-2 sentence help text
	Group       string // "composio" | "e2b" — UI groups by this
	IsPrimary   bool   // master key for the group — gates the others
}

var integrationDefs = []integrationDef{
	{Key: "composio.api_key", EnvKey: "COMPOSIO_API_KEY", Label: "Composio API Key", Description: "Master key for all Composio integrations. Get one at app.composio.dev → Settings → API Keys.", Group: "composio", IsPrimary: true},
	{Key: "composio.gmail_auth_config_id", EnvKey: "COMPOSIO_GMAIL_AUTH_CONFIG_ID", Label: "Gmail", Description: "Composio Auth Config ID for Gmail. Create one in Composio dashboard → Integrations → Gmail.", Group: "composio"},
	{Key: "composio.googlesheets_auth_config_id", EnvKey: "COMPOSIO_GOOGLESHEETS_AUTH_CONFIG_ID", Label: "Google Sheets", Description: "Composio Auth Config ID for Google Sheets.", Group: "composio"},
	{Key: "composio.googledrive_auth_config_id", EnvKey: "COMPOSIO_GOOGLEDRIVE_AUTH_CONFIG_ID", Label: "Google Drive", Description: "Composio Auth Config ID for Google Drive.", Group: "composio"},
	{Key: "composio.googlecalendar_auth_config_id", EnvKey: "COMPOSIO_GOOGLECALENDAR_AUTH_CONFIG_ID", Label: "Google Calendar", Description: "Composio Auth Config ID for Google Calendar.", Group: "composio"},
	{Key: "composio.linkedin_auth_config_id", EnvKey: "COMPOSIO_LINKEDIN_AUTH_CONFIG_ID", Label: "LinkedIn", Description: "Composio Auth Config ID for LinkedIn.", Group: "composio"},
	{Key: "composio.twitter_auth_config_id", EnvKey: "COMPOSIO_TWITTER_AUTH_CONFIG_ID", Label: "Twitter / X", Description: "Composio Auth Config ID for Twitter (X).", Group: "composio"},
	{Key: "composio.youtube_auth_config_id", EnvKey: "COMPOSIO_YOUTUBE_AUTH_CONFIG_ID", Label: "YouTube", Description: "Composio Auth Config ID for YouTube.", Group: "composio"},
	{Key: "composio.zoom_auth_config_id", EnvKey: "COMPOSIO_ZOOM_AUTH_CONFIG_ID", Label: "Zoom", Description: "Composio Auth Config ID for Zoom.", Group: "composio"},
	{Key: "composio.canva_auth_config_id", EnvKey: "COMPOSIO_CANVA_AUTH_CONFIG_ID", Label: "Canva", Description: "Composio Auth Config ID for Canva.", Group: "composio"},
	{Key: "e2b.api_key", EnvKey: "E2B_API_KEY", Label: "E2B API Key", Description: "Code Interpreter sandbox. Get one at e2b.dev. Required for the Python runner tool.", Group: "e2b", IsPrimary: true},
}

// integrationSetting is a single row in the admin UI table.
type integrationSetting struct {
	Key         string `json:"key"`
	EnvKey      string `json:"env_key"`
	Label       string `json:"label"`
	Description string `json:"description"`
	Group       string `json:"group"`
	IsPrimary   bool   `json:"is_primary"`
	// IsSet is true when ANY value is configured (DB or env).
	IsSet bool `json:"is_set"`
	// Source: "db" (admin-edited), "env" (env-var fallback), or
	// empty when nothing's set. Useful for the UI to display
	// "set via env var" warnings (can't unset from UI).
	Source string `json:"source"`
	// MaskedValue shows first/last 4 chars; the rest is dots. The
	// raw value never leaves the backend.
	MaskedValue string `json:"masked_value"`
}

// ─── GET /api/admin/integration-settings ────────────────────────

func (h *IntegrationsAdminHandler) List(c *fiber.Ctx) error {
	if h.settingsService == nil {
		return c.Status(fiber.StatusServiceUnavailable).JSON(fiber.Map{
			"error": "settings service not initialized",
		})
	}

	out := make([]integrationSetting, 0, len(integrationDefs))
	ctx := c.Context()
	for _, def := range integrationDefs {
		dbVal, _ := h.settingsService.Get(ctx, def.Key)
		envVal := ""
		if dbVal == "" {
			// Only read env when DB is empty — env is the fallback.
			envVal = readEnv(def.EnvKey)
		}
		effective := dbVal
		source := "db"
		if effective == "" {
			effective = envVal
			source = "env"
		}
		if effective == "" {
			source = ""
		}
		out = append(out, integrationSetting{
			Key:         def.Key,
			EnvKey:      def.EnvKey,
			Label:       def.Label,
			Description: def.Description,
			Group:       def.Group,
			IsPrimary:   def.IsPrimary,
			IsSet:       effective != "",
			Source:      source,
			MaskedValue: maskSecret(effective),
		})
	}
	return c.JSON(fiber.Map{"settings": out})
}

// ─── PUT /api/admin/integration-settings ────────────────────────

type updateRequest struct {
	Updates map[string]string `json:"updates"` // key → new value. Empty string = clear.
}

func (h *IntegrationsAdminHandler) Update(c *fiber.Ctx) error {
	if h.settingsService == nil {
		return c.Status(fiber.StatusServiceUnavailable).JSON(fiber.Map{
			"error": "settings service not initialized",
		})
	}

	var req updateRequest
	if err := c.BodyParser(&req); err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "invalid body"})
	}

	// Validate every requested key is one we manage. Refuse
	// unknown keys defensively — admins shouldn't be able to
	// write arbitrary settings rows via this endpoint.
	known := make(map[string]bool, len(integrationDefs))
	for _, d := range integrationDefs {
		known[d.Key] = true
	}
	for k := range req.Updates {
		if !known[k] {
			return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{
				"error": "unknown setting key: " + k,
			})
		}
	}

	// Apply updates. Empty string clears the value (writes empty
	// to the settings table — the resolver then falls back to env).
	ctx := context.Background()
	applied := 0
	for k, v := range req.Updates {
		if err := h.settingsService.Set(ctx, k, strings.TrimSpace(v)); err != nil {
			log.Printf("❌ [ADMIN-INT] Failed to save %s: %v", k, err)
			return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{
				"error": "save failed: " + err.Error(),
			})
		}
		applied++
	}

	// Drop the in-memory cache so tools see new values on the
	// next call instead of waiting up to 30s for the TTL.
	tools.InvalidateSettingsCache()

	log.Printf("✅ [ADMIN-INT] Updated %d integration setting(s)", applied)
	return c.JSON(fiber.Map{"applied": applied, "ok": true})
}

// ─── helpers ────────────────────────────────────────────────────

// maskSecret returns the first 4 + last 4 chars of a secret with
// the middle blanked. For very short values, returns "****".
// Empty in → empty out (so the UI can show "(not set)").
func maskSecret(s string) string {
	if s == "" {
		return ""
	}
	if len(s) <= 8 {
		return "****"
	}
	return s[:4] + strings.Repeat("•", 8) + s[len(s)-4:]
}

// readEnv is a thin wrapper so a future test fake could override it
// without monkey-patching os.Getenv globally.
func readEnv(key string) string {
	return os.Getenv(key)
}
