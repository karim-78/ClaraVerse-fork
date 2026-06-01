// Integration settings resolver for the Composio + E2B tools.
//
// Tools previously read their credentials directly from env vars
// (e.g. `os.Getenv("COMPOSIO_API_KEY")`). That worked for users who
// set env vars at compose-up time but locked admins out of changing
// keys without a container restart.
//
// This package-level resolver checks the DB-backed settings table
// first (admin-editable via /api/admin/integration-settings) and
// falls back to the env var for backwards compatibility. So:
//
//   - Existing deployments with env vars keep working unchanged.
//   - New deployments can wire keys via the admin UI; no restart.
//   - DB values take precedence when both are present.
//
// The settings service is wired in once at startup (`SetSettingsService`)
// rather than passed through every tool factory. Tool factories
// register N tools each and threading the service through every
// `New*Tool()` would be ~50 signature changes for no real benefit
// — a package-level singleton is the simplest fit for "global env
// replacement."
package tools

import (
	"context"
	"os"
	"sync"
	"time"
)

// settingsReader is the minimum surface we need from
// services.SettingsService. Defined locally (and matched at wiring
// time via interface assignment in main.go) to avoid an import
// cycle with services/.
type settingsReader interface {
	Get(ctx context.Context, key string) (string, error)
}

var (
	settingsMu       sync.RWMutex
	settingsBackend  settingsReader
	settingsCache    = map[string]cachedSetting{}
	settingsCacheTTL = 30 * time.Second // cache 30s to avoid hammering the DB on every tool call
)

type cachedSetting struct {
	value   string
	expires time.Time
}

// SetSettingsBackend wires the DB-backed settings reader. Called
// once from main.go after the SettingsService is initialized.
// Calling with nil is the same as "no DB backend" — env-only mode.
func SetSettingsBackend(r settingsReader) {
	settingsMu.Lock()
	defer settingsMu.Unlock()
	settingsBackend = r
	// Drop the cache when the backend changes so a fresh wiring
	// doesn't serve stale "empty" results from before init.
	settingsCache = map[string]cachedSetting{}
}

// resolveSetting reads a value with the precedence:
//   1. DB-backed setting (admin-editable, 30s cached)
//   2. Env var fallback
//
// Returns empty string when neither source has a value. Callers
// should treat empty as "not configured" and surface a user-facing
// "configure this integration in the admin panel" error.
func resolveSetting(settingKey, envKey string) string {
	// Cache check first (no locking on the read path — the cache
	// map is read-mostly; a stale value during write contention is
	// fine, the next call gets the fresh value).
	settingsMu.RLock()
	cached, ok := settingsCache[settingKey]
	backend := settingsBackend
	settingsMu.RUnlock()

	if ok && time.Now().Before(cached.expires) {
		if cached.value != "" {
			return cached.value
		}
		// Cached empty — fall through to env, no DB call.
		return os.Getenv(envKey)
	}

	// Cache miss or expired. Pull from DB if backend wired.
	dbValue := ""
	if backend != nil {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		if v, err := backend.Get(ctx, settingKey); err == nil {
			dbValue = v
		}
	}

	// Update cache (empty or not — caching empty is fine, next 30s
	// of "not configured" lookups skip the DB entirely).
	settingsMu.Lock()
	settingsCache[settingKey] = cachedSetting{
		value:   dbValue,
		expires: time.Now().Add(settingsCacheTTL),
	}
	settingsMu.Unlock()

	if dbValue != "" {
		return dbValue
	}
	return os.Getenv(envKey)
}

// InvalidateSettingsCache clears all cached values. Called by the
// admin handler after a successful PUT so the next tool call sees
// the new key immediately instead of waiting for the TTL.
func InvalidateSettingsCache() {
	settingsMu.Lock()
	defer settingsMu.Unlock()
	settingsCache = map[string]cachedSetting{}
}

// ─── Composio ────────────────────────────────────────────────────

// GetComposioAPIKey returns the Composio master API key — required
// for every Composio integration. Empty when not configured.
func GetComposioAPIKey() string {
	return resolveSetting("composio.api_key", "COMPOSIO_API_KEY")
}

// GetComposioAuthConfigID returns the per-integration OAuth config
// ID created in the Composio dashboard. slug is the lowercase
// integration name (e.g. "gmail", "googlesheets"). Returns empty
// when the integration isn't configured.
func GetComposioAuthConfigID(slug string) string {
	settingKey := "composio." + slug + "_auth_config_id"
	envKey := "COMPOSIO_" + composioEnvSlug(slug) + "_AUTH_CONFIG_ID"
	return resolveSetting(settingKey, envKey)
}

// composioEnvSlug uppercases an integration slug for the env-var
// fallback name. E.g. "googlesheets" → "GOOGLESHEETS".
func composioEnvSlug(slug string) string {
	out := make([]byte, len(slug))
	for i := 0; i < len(slug); i++ {
		c := slug[i]
		if c >= 'a' && c <= 'z' {
			c -= 32
		}
		out[i] = c
	}
	return string(out)
}

// ─── E2B ──────────────────────────────────────────────────────────

// GetE2BAPIKey returns the E2B code-interpreter API key. Empty
// when not configured. The existing E2B settings key is
// "e2b.api_key" (matches what admin_e2b.go writes); we honor that
// and don't introduce a parallel key.
func GetE2BAPIKey() string {
	return resolveSetting("e2b.api_key", "E2B_API_KEY")
}
