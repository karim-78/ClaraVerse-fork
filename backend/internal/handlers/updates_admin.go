// Admin endpoints for in-app self-update.
//
// Two-step flow:
//
//   1. GET  /api/admin/updates/check
//      Polls the GitHub releases API for the latest published
//      version, compares to the version baked into the running
//      binary, and returns whether an update is available + the
//      release notes URL.
//
//   2. POST /api/admin/updates/apply
//      Triggers the Watchtower sidecar's HTTP API to pull the
//      latest images and recreate the containers. The backend
//      container will be torn down + recreated as part of this —
//      so the response is "started, frontend should reconnect in
//      ~30s" rather than "completed". Frontend polls /health
//      until the new backend is up.
//
// Auth: every endpoint here is admin-gated by the existing
// AdminMiddleware (verified at route registration in main.go).
package handlers

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/gofiber/fiber/v2"
)

// UpdatesAdminHandler exposes the self-update surface.
//
// Lifecycle: created once at startup with the build-time version
// embedded into the binary (or read from env at boot). The handler
// keeps zero per-request state — the GitHub poll runs synchronously
// on each check call. Caching is intentional and minimal so admins
// see fresh data when they hit "check now"; the chat surface polls
// less often via the WS event channel.
type UpdatesAdminHandler struct {
	// currentVersion is the version of the running binary. Read in
	// priority order: VERSION env var (set at compose level for
	// dev), then /app/data/.version file (written by build script),
	// then "dev". Set once at New so we don't re-read on every call.
	currentVersion string

	// watchtowerURL is the HTTP endpoint of the Watchtower sidecar's
	// /v1/update API. Empty when Watchtower isn't configured in
	// this deployment — Apply returns a "manual update required"
	// hint instead of erroring.
	watchtowerURL string

	// watchtowerToken protects Watchtower's HTTP API; required in
	// the Authorization header per Watchtower docs.
	watchtowerToken string

	http *http.Client
}

// NewUpdatesAdminHandler wires the handler. Env-driven config so a
// deployment without Watchtower (e.g. a single-container docker run)
// still answers the check endpoint with useful info.
func NewUpdatesAdminHandler() *UpdatesAdminHandler {
	return &UpdatesAdminHandler{
		currentVersion:  resolveCurrentVersion(),
		watchtowerURL:   os.Getenv("WATCHTOWER_URL"),
		watchtowerToken: os.Getenv("WATCHTOWER_HTTP_API_TOKEN"),
		http:            &http.Client{Timeout: 15 * time.Second},
	}
}

func resolveCurrentVersion() string {
	if v := os.Getenv("CLARAVERSE_VERSION"); v != "" {
		return v
	}
	// Optional: read from a baked-in version file. Build pipeline
	// could write to /app/data/.version. Falls back to "dev"
	// silently so local development isn't noisy.
	if data, err := os.ReadFile("/app/data/.version"); err == nil {
		v := strings.TrimSpace(string(data))
		if v != "" {
			return v
		}
	}
	return "dev"
}

// ─── /api/admin/updates/check ───────────────────────────────────

type checkResponse struct {
	CurrentVersion  string `json:"current_version"`
	LatestVersion   string `json:"latest_version"`
	UpdateAvailable bool   `json:"update_available"`
	ReleaseURL      string `json:"release_url,omitempty"`
	ReleaseName     string `json:"release_name,omitempty"`
	PublishedAt     string `json:"published_at,omitempty"`
	WatchtowerReady bool   `json:"watchtower_ready"`
	HintIfManual    string `json:"hint_if_manual,omitempty"`
}

// Check polls GitHub Releases and reports update status. Cheap
// enough to be called freely; GitHub allows 60 unauthenticated
// requests/hour per IP which is plenty for an admin-only banner
// that refreshes on tab focus.
func (h *UpdatesAdminHandler) Check(c *fiber.Ctx) error {
	type ghRelease struct {
		TagName     string `json:"tag_name"`
		Name        string `json:"name"`
		HTMLURL     string `json:"html_url"`
		PublishedAt string `json:"published_at"`
		Draft       bool   `json:"draft"`
		Prerelease  bool   `json:"prerelease"`
	}

	ctx, cancel := context.WithTimeout(c.Context(), 10*time.Second)
	defer cancel()

	req, _ := http.NewRequestWithContext(ctx, http.MethodGet,
		"https://api.github.com/repos/claraverse-space/ClaraVerse/releases/latest", nil)
	req.Header.Set("Accept", "application/vnd.github+json")

	resp, err := h.http.Do(req)
	if err != nil {
		// Network down / GitHub unreachable. Don't fail the
		// dashboard — surface the error so the banner can be
		// "couldn't check, try again" instead of disappearing.
		return c.JSON(checkResponse{
			CurrentVersion:  h.currentVersion,
			LatestVersion:   "unknown",
			UpdateAvailable: false,
			WatchtowerReady: h.watchtowerURL != "",
			HintIfManual:    fmt.Sprintf("Couldn't reach GitHub: %v", err),
		})
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return c.JSON(checkResponse{
			CurrentVersion:  h.currentVersion,
			LatestVersion:   "unknown",
			UpdateAvailable: false,
			WatchtowerReady: h.watchtowerURL != "",
			HintIfManual:    fmt.Sprintf("GitHub returned %d", resp.StatusCode),
		})
	}

	var rel ghRelease
	if err := json.NewDecoder(resp.Body).Decode(&rel); err != nil {
		return c.JSON(checkResponse{
			CurrentVersion:  h.currentVersion,
			LatestVersion:   "unknown",
			UpdateAvailable: false,
			WatchtowerReady: h.watchtowerURL != "",
			HintIfManual:    fmt.Sprintf("GitHub decode: %v", err),
		})
	}

	// Compare versions. Both are like "v0.3.1" — string compare
	// works for our v{MAJOR}.{MINOR}.{PATCH} scheme because we keep
	// the v prefix and only have single digits past v0 right now.
	// If we ever hit v0.10 vs v0.2 this will sort wrong; revisit
	// then with a real semver comparator. For now string compare
	// catches the 99% case (current "v0.3.1" < latest "v0.3.2").
	latestTag := rel.TagName
	updateAvailable := latestTag != "" &&
		latestTag != h.currentVersion &&
		h.currentVersion != "dev" && // dev builds: never claim out-of-date
		!rel.Draft &&
		!rel.Prerelease

	hint := ""
	if updateAvailable && h.watchtowerURL == "" {
		hint = "Watchtower not configured. Run `claraverse update` from your terminal, or `docker compose pull && docker compose up -d` if using compose directly."
	}

	return c.JSON(checkResponse{
		CurrentVersion:  h.currentVersion,
		LatestVersion:   latestTag,
		UpdateAvailable: updateAvailable,
		ReleaseURL:      rel.HTMLURL,
		ReleaseName:     rel.Name,
		PublishedAt:     rel.PublishedAt,
		WatchtowerReady: h.watchtowerURL != "",
		HintIfManual:    hint,
	})
}

// ─── /api/admin/updates/apply ────────────────────────────────────

type applyResponse struct {
	Started     bool   `json:"started"`
	Message     string `json:"message"`
	ReconnectIn string `json:"reconnect_in,omitempty"`
}

// Apply triggers Watchtower's update endpoint. Returns immediately;
// the backend container will be recreated as part of the update so
// the response may not reach the client (frontend handles this by
// polling /health after seeing a successful POST OR a connection
// reset — either is an "update in progress" signal).
//
// When Watchtower isn't configured we return 503 with a clear
// message so the frontend can fall back to "run `claraverse
// update`" instructions.
func (h *UpdatesAdminHandler) Apply(c *fiber.Ctx) error {
	if h.watchtowerURL == "" {
		return c.Status(http.StatusServiceUnavailable).JSON(applyResponse{
			Started: false,
			Message: "Watchtower sidecar not configured — run `claraverse update` from your terminal instead.",
		})
	}

	// POST to Watchtower's /v1/update. The response is just an
	// ACK — actual container recreation runs async on Watchtower's
	// side. Auth via Bearer token (Watchtower convention).
	ctx, cancel := context.WithTimeout(c.Context(), 5*time.Second)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, http.MethodPost,
		strings.TrimRight(h.watchtowerURL, "/")+"/v1/update", nil)
	if err != nil {
		return c.Status(500).JSON(applyResponse{Started: false, Message: err.Error()})
	}
	if h.watchtowerToken != "" {
		req.Header.Set("Authorization", "Bearer "+h.watchtowerToken)
	}

	resp, err := h.http.Do(req)
	if err != nil {
		return c.Status(502).JSON(applyResponse{
			Started: false,
			Message: fmt.Sprintf("Couldn't reach Watchtower: %v", err),
		})
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusAccepted {
		return c.Status(502).JSON(applyResponse{
			Started: false,
			Message: fmt.Sprintf("Watchtower returned %d", resp.StatusCode),
		})
	}

	return c.JSON(applyResponse{
		Started:     true,
		Message:     "Update started. The backend will restart in 5-30 seconds.",
		ReconnectIn: "30s",
	})
}
