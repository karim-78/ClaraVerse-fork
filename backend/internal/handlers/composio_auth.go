package handlers

import (
	"bytes"
	"claraverse/internal/models"
	"claraverse/internal/security"
	"claraverse/internal/services"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"

	"github.com/gofiber/fiber/v2"
)

// OAuth scopes required for each service
var requiredScopes = map[string][]string{
	"gmail": {
		"https://www.googleapis.com/auth/gmail.send",
		"https://www.googleapis.com/auth/gmail.readonly",
		"https://www.googleapis.com/auth/gmail.modify",
	},
	"googlesheets": {
		"https://www.googleapis.com/auth/spreadsheets",
	},
}

// ComposioAuthHandler handles Composio OAuth flow
type ComposioAuthHandler struct {
	credentialService *services.CredentialService
	httpClient        *http.Client
	stateStore        *security.OAuthStateStore
}

// NewComposioAuthHandler creates a new Composio auth handler
func NewComposioAuthHandler(credentialService *services.CredentialService) *ComposioAuthHandler {
	return &ComposioAuthHandler{
		credentialService: credentialService,
		httpClient:        &http.Client{Timeout: 30 * time.Second},
		stateStore:        security.NewOAuthStateStore(),
	}
}

// InitiateGoogleSheetsAuth initiates OAuth flow for Google Sheets via Composio
// GET /api/integrations/composio/googlesheets/authorize
func (h *ComposioAuthHandler) InitiateGoogleSheetsAuth(c *fiber.Ctx) error {
	userID := c.Locals("user_id").(string)

	composioAPIKey := os.Getenv("COMPOSIO_API_KEY")
	if composioAPIKey == "" {
		log.Printf("❌ [COMPOSIO] COMPOSIO_API_KEY not set")
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{
			"error": "Composio integration not configured",
		})
	}

	// Use ClaraVerse user ID as Composio entity ID for simplicity
	entityID := userID

	// Validate and sanitize redirect URL
	redirectURL := c.Query("redirect_url")
	if redirectURL == "" {
		// Default to frontend settings page
		frontendURL := os.Getenv("FRONTEND_URL")
		if frontendURL == "" {
			// Only allow localhost fallback in non-production environments
			if os.Getenv("ENVIRONMENT") == "production" {
				log.Printf("❌ [COMPOSIO] FRONTEND_URL not set in production")
				return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{
					"error": "Configuration error",
				})
			}
			frontendURL = "http://localhost:5173"
		}
		redirectURL = fmt.Sprintf("%s/settings?tab=credentials", frontendURL)
	} else {
		// Validate redirect URL against allowed origins
		if err := validateRedirectURL(redirectURL); err != nil {
			log.Printf("⚠️ [COMPOSIO] Invalid redirect URL: %s", redirectURL)
			return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{
				"error": "Invalid redirect URL",
			})
		}
	}

	// Get auth config ID from environment
	// This must be created in Composio dashboard first
	authConfigID := os.Getenv("COMPOSIO_GOOGLESHEETS_AUTH_CONFIG_ID")
	if authConfigID == "" {
		log.Printf("❌ [COMPOSIO] COMPOSIO_GOOGLESHEETS_AUTH_CONFIG_ID not set")
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{
			"error": "Google Sheets auth config not configured. Please set COMPOSIO_GOOGLESHEETS_AUTH_CONFIG_ID in environment.",
		})
	}

	// ✅ SECURITY FIX: Generate CSRF state token
	stateToken, err := h.stateStore.GenerateState(userID, "googlesheets")
	if err != nil {
		log.Printf("❌ [COMPOSIO] Failed to generate state token: %v", err)
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{
			"error": "Failed to initiate OAuth",
		})
	}

	// Call Composio API v3 to create a link for OAuth
	// v3 uses /link endpoint which returns redirect_url
	payload := map[string]interface{}{
		"auth_config_id": authConfigID,
		"user_id":        entityID,
	}

	jsonData, err := json.Marshal(payload)
	if err != nil {
		log.Printf("❌ [COMPOSIO] Failed to marshal request: %v", err)
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{
			"error": "Failed to initiate OAuth",
		})
	}

	// Create connection link using v3 endpoint
	composioURL := "https://backend.composio.dev/api/v3/connected_accounts/link"
	req, err := http.NewRequest("POST", composioURL, bytes.NewBuffer(jsonData))
	if err != nil {
		log.Printf("❌ [COMPOSIO] Failed to create request: %v", err)
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{
			"error": "Failed to initiate OAuth",
		})
	}

	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("x-api-key", composioAPIKey)

	resp, err := h.httpClient.Do(req)
	if err != nil {
		log.Printf("❌ [COMPOSIO] Failed to call Composio API: %v", err)
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{
			"error": "Failed to initiate OAuth",
		})
	}
	defer resp.Body.Close()

	respBody, _ := io.ReadAll(resp.Body)

	if resp.StatusCode >= 400 {
		log.Printf("❌ [COMPOSIO] API error (status %d): %s", resp.StatusCode, string(respBody))
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{
			"error": fmt.Sprintf("Composio API error: %s", string(respBody)),
		})
	}

	// Parse response to get redirectUrl
	var composioResp map[string]interface{}
	if err := json.Unmarshal(respBody, &composioResp); err != nil {
		log.Printf("❌ [COMPOSIO] Failed to parse response: %v", err)
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{
			"error": "Failed to parse OAuth response",
		})
	}

	// v3 API returns redirect_url (snake_case)
	redirectURLFromComposio, ok := composioResp["redirect_url"].(string)
	if !ok {
		log.Printf("❌ [COMPOSIO] No redirect_url in response: %v", composioResp)
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{
			"error": "Invalid OAuth response from Composio",
		})
	}

	// ✅ SECURITY FIX: Append state token to OAuth URL for CSRF protection
	parsedURL, err := url.Parse(redirectURLFromComposio)
	if err != nil {
		log.Printf("❌ [COMPOSIO] Failed to parse OAuth URL: %v", err)
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{
			"error": "Invalid OAuth URL",
		})
	}
	query := parsedURL.Query()
	query.Set("state", stateToken)
	parsedURL.RawQuery = query.Encode()
	authURLWithState := parsedURL.String()

	log.Printf("✅ [COMPOSIO] Initiated Google Sheets OAuth for user %s", userID)

	// Return the OAuth URL to frontend
	return c.JSON(fiber.Map{
		"authUrl":     authURLWithState,
		"entityId":    entityID,
		"redirectUrl": redirectURL,
	})
}

// HandleComposioCallback handles OAuth callback from Composio
// GET /api/integrations/composio/callback
func (h *ComposioAuthHandler) HandleComposioCallback(c *fiber.Ctx) error {
	// Get query parameters
	code := c.Query("code")
	state := c.Query("state")
	errorParam := c.Query("error")

	// Get frontend URL
	frontendURL := os.Getenv("FRONTEND_URL")
	if frontendURL == "" {
		if os.Getenv("ENVIRONMENT") == "production" {
			log.Printf("❌ [COMPOSIO] FRONTEND_URL not set in production")
			return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{
				"error": "Configuration error",
			})
		}
		frontendURL = "http://localhost:5173"
	}

	if errorParam != "" {
		log.Printf("❌ [COMPOSIO] OAuth error: %s", errorParam)
		return c.Redirect(fmt.Sprintf("%s/settings?tab=credentials&error=%s",
			frontendURL, url.QueryEscape(errorParam)))
	}

	if code == "" {
		log.Printf("❌ [COMPOSIO] No code in callback")
		return c.Redirect(fmt.Sprintf("%s/settings?tab=credentials&error=no_code", frontendURL))
	}

	// ✅ SECURITY FIX: Validate CSRF state token
	if state == "" {
		log.Printf("❌ [COMPOSIO] Missing state token in callback")
		return c.Redirect(fmt.Sprintf("%s/settings?tab=credentials&error=invalid_state", frontendURL))
	}

	userID, service, err := h.stateStore.ValidateState(state)
	if err != nil {
		log.Printf("❌ [COMPOSIO] Invalid state token: %v", err)
		return c.Redirect(fmt.Sprintf("%s/settings?tab=credentials&error=invalid_state", frontendURL))
	}

	log.Printf("✅ [COMPOSIO] Valid OAuth callback for user %s, service: %s", userID, service)

	// ✅ SECURITY FIX: Store code server-side instead of passing in URL
	// Generate a temporary session token to pass to frontend
	sessionToken, err := h.stateStore.GenerateState(userID, service+"_callback")
	if err != nil {
		log.Printf("❌ [COMPOSIO] Failed to generate session token: %v", err)
		return c.Redirect(fmt.Sprintf("%s/settings?tab=credentials&error=session_error", frontendURL))
	}

	// Store the authorization code temporarily (reusing state store for simplicity)
	// In production, you might want a separate session store
	codeStoreKey := "oauth_code:" + sessionToken
	_, err = h.stateStore.GenerateState(codeStoreKey, code) // Store code using state as key
	if err != nil {
		log.Printf("❌ [COMPOSIO] Failed to store authorization code: %v", err)
		return c.Redirect(fmt.Sprintf("%s/settings?tab=credentials&error=session_error", frontendURL))
	}

	// ✅ SECURITY FIX: Redirect without exposing authorization code in URL
	redirectURL := fmt.Sprintf("%s/settings?tab=credentials&composio_success=true&service=%s&session=%s",
		frontendURL, url.QueryEscape(service), url.QueryEscape(sessionToken))

	log.Printf("✅ [COMPOSIO] OAuth callback successful, redirecting user %s", userID)
	return c.Redirect(redirectURL)
}

// GetConnectedAccount retrieves Composio connected account for entity
// GET /api/integrations/composio/connected-account
func (h *ComposioAuthHandler) GetConnectedAccount(c *fiber.Ctx) error {
	userID := c.Locals("user_id").(string)
	entityID := userID // We use user ID as entity ID

	composioAPIKey := os.Getenv("COMPOSIO_API_KEY")
	if composioAPIKey == "" {
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{
			"error": "Composio integration not configured",
		})
	}

	// Get connected accounts for entity using v3 API
	baseURL := "https://backend.composio.dev/api/v3/connected_accounts"
	params := url.Values{}
	params.Add("user_ids", entityID)
	fullURL := baseURL + "?" + params.Encode()
	req, err := http.NewRequest("GET", fullURL, nil)
	if err != nil {
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{
			"error": "Failed to fetch connected account",
		})
	}

	req.Header.Set("x-api-key", composioAPIKey)

	resp, err := h.httpClient.Do(req)
	if err != nil {
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{
			"error": "Failed to fetch connected account",
		})
	}
	defer resp.Body.Close()

	respBody, _ := io.ReadAll(resp.Body)

	if resp.StatusCode >= 400 {
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{
			"error": string(respBody),
		})
	}

	// v3 API returns {items: [...], total_pages, current_page, ...}
	var response struct {
		Items []map[string]interface{} `json:"items"`
	}
	if err := json.Unmarshal(respBody, &response); err != nil {
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{
			"error": "Failed to parse response",
		})
	}

	// Find Google Sheets connected account
	// v3 API uses toolkit.slug instead of integrationId
	for _, account := range response.Items {
		if toolkit, ok := account["toolkit"].(map[string]interface{}); ok {
			if slug, ok := toolkit["slug"].(string); ok && slug == "googlesheets" {
				return c.JSON(account)
			}
		}
	}

	return c.Status(fiber.StatusNotFound).JSON(fiber.Map{
		"error": "No Google Sheets connection found",
	})
}

// CompleteComposioSetup creates credential after OAuth success
// POST /api/integrations/composio/complete-setup
func (h *ComposioAuthHandler) CompleteComposioSetup(c *fiber.Ctx) error {
	userID := c.Locals("user_id").(string)

	var req struct {
		Name            string `json:"name"`
		IntegrationType string `json:"integrationType"`
	}

	if err := c.BodyParser(&req); err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{
			"error": "Invalid request body",
		})
	}

	if req.Name == "" {
		req.Name = "Google Sheets"
	}

	if req.IntegrationType == "" {
		req.IntegrationType = "composio_googlesheets"
	}

	// Extract service name from integration type (e.g., "composio_gmail" -> "gmail")
	serviceName := req.IntegrationType
	if len(serviceName) > 9 && serviceName[:9] == "composio_" {
		serviceName = serviceName[9:] // Remove "composio_" prefix
	}

	// Entity ID is the same as user ID
	entityID := userID

	// Verify the connection exists in Composio
	composioAPIKey := os.Getenv("COMPOSIO_API_KEY")
	if composioAPIKey == "" {
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{
			"error": "Composio integration not configured",
		})
	}

	// Get connected accounts to verify using v3 API
	baseURL := "https://backend.composio.dev/api/v3/connected_accounts"
	params := url.Values{}
	params.Add("user_ids", entityID)
	fullURL := baseURL + "?" + params.Encode()
	httpReq, _ := http.NewRequest("GET", fullURL, nil)
	httpReq.Header.Set("x-api-key", composioAPIKey)

	resp, err := h.httpClient.Do(httpReq)
	if err != nil {
		log.Printf("❌ [COMPOSIO] Failed to verify connection: %v", err)
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{
			"error": "Failed to verify Composio connection",
		})
	}
	defer resp.Body.Close()

	respBody, _ := io.ReadAll(resp.Body)
	var response struct {
		Items []map[string]interface{} `json:"items"`
	}
	if err := json.Unmarshal(respBody, &response); err != nil {
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{
			"error": "Failed to verify connection",
		})
	}

	// Check if the service is connected (v3 uses toolkit.slug)
	found := false
	for _, account := range response.Items {
		if toolkit, ok := account["toolkit"].(map[string]interface{}); ok {
			if slug, ok := toolkit["slug"].(string); ok && slug == serviceName {
				found = true
				break
			}
		}
	}

	if !found {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{
			"error": fmt.Sprintf("%s not connected in Composio. Please complete OAuth first.", strings.Title(serviceName)),
		})
	}

	// Create credential
	credential, err := h.credentialService.Create(c.Context(), userID, &models.CreateCredentialRequest{
		Name:            req.Name,
		IntegrationType: req.IntegrationType,
		Data: map[string]interface{}{
			"composio_entity_id": entityID,
		},
	})

	if err != nil {
		log.Printf("❌ [COMPOSIO] Failed to create credential: %v", err)
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{
			"error": "Failed to save credential",
		})
	}

	log.Printf("✅ [COMPOSIO] Created credential for user %s with entity_id %s", userID, entityID)

	return c.Status(fiber.StatusCreated).JSON(credential)
}

// InitiateGmailAuth initiates OAuth flow for Gmail via Composio
// GET /api/integrations/composio/gmail/authorize
func (h *ComposioAuthHandler) InitiateGmailAuth(c *fiber.Ctx) error {
	userID := c.Locals("user_id").(string)

	composioAPIKey := os.Getenv("COMPOSIO_API_KEY")
	if composioAPIKey == "" {
		log.Printf("❌ [COMPOSIO] COMPOSIO_API_KEY not set")
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{
			"error": "Composio integration not configured",
		})
	}

	// Use ClaraVerse user ID as Composio entity ID
	entityID := userID

	// Validate and sanitize redirect URL
	redirectURL := c.Query("redirect_url")
	if redirectURL == "" {
		// Default to frontend settings page
		frontendURL := os.Getenv("FRONTEND_URL")
		if frontendURL == "" {
			// Only allow localhost fallback in non-production environments
			if os.Getenv("ENVIRONMENT") == "production" {
				log.Printf("❌ [COMPOSIO] FRONTEND_URL not set in production")
				return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{
					"error": "Configuration error",
				})
			}
			frontendURL = "http://localhost:5173"
		}
		redirectURL = fmt.Sprintf("%s/settings?tab=credentials", frontendURL)
	} else {
		// Validate redirect URL against allowed origins
		if err := validateRedirectURL(redirectURL); err != nil {
			log.Printf("⚠️ [COMPOSIO] Invalid redirect URL: %s", redirectURL)
			return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{
				"error": "Invalid redirect URL",
			})
		}
	}

	// Get auth config ID from environment
	authConfigID := os.Getenv("COMPOSIO_GMAIL_AUTH_CONFIG_ID")
	if authConfigID == "" {
		log.Printf("❌ [COMPOSIO] COMPOSIO_GMAIL_AUTH_CONFIG_ID not set")
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{
			"error": "Gmail auth config not configured. Please set COMPOSIO_GMAIL_AUTH_CONFIG_ID in environment.",
		})
	}

	// ✅ SECURITY FIX: Generate CSRF state token
	stateToken, err := h.stateStore.GenerateState(userID, "gmail")
	if err != nil {
		log.Printf("❌ [COMPOSIO] Failed to generate state token: %v", err)
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{
			"error": "Failed to initiate OAuth",
		})
	}

	// Call Composio API v3 to create a link for OAuth
	payload := map[string]interface{}{
		"auth_config_id": authConfigID,
		"user_id":        entityID,
	}

	jsonData, err := json.Marshal(payload)
	if err != nil {
		log.Printf("❌ [COMPOSIO] Failed to marshal request: %v", err)
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{
			"error": "Failed to initiate OAuth",
		})
	}

	// Create connection link using v3 endpoint
	composioURL := "https://backend.composio.dev/api/v3/connected_accounts/link"
	req, err := http.NewRequest("POST", composioURL, bytes.NewBuffer(jsonData))
	if err != nil {
		log.Printf("❌ [COMPOSIO] Failed to create request: %v", err)
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{
			"error": "Failed to initiate OAuth",
		})
	}

	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("x-api-key", composioAPIKey)

	resp, err := h.httpClient.Do(req)
	if err != nil {
		log.Printf("❌ [COMPOSIO] Failed to call Composio API: %v", err)
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{
			"error": "Failed to initiate OAuth",
		})
	}
	defer resp.Body.Close()

	respBody, _ := io.ReadAll(resp.Body)

	if resp.StatusCode >= 400 {
		log.Printf("❌ [COMPOSIO] API error (status %d): %s", resp.StatusCode, string(respBody))
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{
			"error": fmt.Sprintf("Composio API error: %s", string(respBody)),
		})
	}

	// Parse response to get redirectUrl
	var composioResp map[string]interface{}
	if err := json.Unmarshal(respBody, &composioResp); err != nil {
		log.Printf("❌ [COMPOSIO] Failed to parse response: %v", err)
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{
			"error": "Failed to parse OAuth response",
		})
	}

	// v3 API returns redirect_url
	redirectURLFromComposio, ok := composioResp["redirect_url"].(string)
	if !ok {
		log.Printf("❌ [COMPOSIO] No redirect_url in response: %v", composioResp)
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{
			"error": "Invalid OAuth response from Composio",
		})
	}

	// ✅ SECURITY FIX: Append state token to OAuth URL for CSRF protection
	gmailOauthURL, err := url.Parse(redirectURLFromComposio)
	if err != nil {
		log.Printf("❌ [COMPOSIO] Failed to parse OAuth URL: %v", err)
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{
			"error": "Invalid OAuth URL",
		})
	}
	gmailQueryParams := gmailOauthURL.Query()
	gmailQueryParams.Set("state", stateToken)
	gmailOauthURL.RawQuery = gmailQueryParams.Encode()
	authURLWithState := gmailOauthURL.String()

	log.Printf("✅ [COMPOSIO] Initiated Gmail OAuth for user %s", userID)

	// Return the OAuth URL to frontend
	return c.JSON(fiber.Map{
		"authUrl":     authURLWithState,
		"entityId":    entityID,
		"redirectUrl": redirectURL,
	})
}

// InitiateLinkedInAuth initiates OAuth flow for LinkedIn via Composio
// GET /api/integrations/composio/linkedin/authorize
func (h *ComposioAuthHandler) InitiateLinkedInAuth(c *fiber.Ctx) error {
	userID := c.Locals("user_id").(string)

	composioAPIKey := os.Getenv("COMPOSIO_API_KEY")
	if composioAPIKey == "" {
		log.Printf("❌ [COMPOSIO] COMPOSIO_API_KEY not set")
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{
			"error": "Composio integration not configured",
		})
	}

	// Use ClaraVerse user ID as Composio entity ID
	entityID := userID

	// Validate and sanitize redirect URL
	redirectURL := c.Query("redirect_url")
	if redirectURL == "" {
		// Default to frontend settings page
		frontendURL := os.Getenv("FRONTEND_URL")
		if frontendURL == "" {
			// Only allow localhost fallback in non-production environments
			if os.Getenv("ENVIRONMENT") == "production" {
				log.Printf("❌ [COMPOSIO] FRONTEND_URL not set in production")
				return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{
					"error": "Configuration error",
				})
			}
			frontendURL = "http://localhost:5173"
		}
		redirectURL = fmt.Sprintf("%s/settings?tab=credentials", frontendURL)
	} else {
		// Validate redirect URL against allowed origins
		if err := validateRedirectURL(redirectURL); err != nil {
			log.Printf("⚠️ [COMPOSIO] Invalid redirect URL: %s", redirectURL)
			return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{
				"error": "Invalid redirect URL",
			})
		}
	}

	// Get auth config ID from environment
	authConfigID := os.Getenv("COMPOSIO_LINKEDIN_AUTH_CONFIG_ID")
	if authConfigID == "" {
		log.Printf("❌ [COMPOSIO] COMPOSIO_LINKEDIN_AUTH_CONFIG_ID not set")
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{
			"error": "LinkedIn auth config not configured. Please set COMPOSIO_LINKEDIN_AUTH_CONFIG_ID in environment.",
		})
	}

	// ✅ SECURITY FIX: Generate CSRF state token
	stateToken, err := h.stateStore.GenerateState(userID, "linkedin")
	if err != nil {
		log.Printf("❌ [COMPOSIO] Failed to generate state token: %v", err)
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{
			"error": "Failed to initiate OAuth",
		})
	}

	// Call Composio API v3 to create a link for OAuth
	payload := map[string]interface{}{
		"auth_config_id": authConfigID,
		"user_id":        entityID,
	}

	jsonData, err := json.Marshal(payload)
	if err != nil {
		log.Printf("❌ [COMPOSIO] Failed to marshal request: %v", err)
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{
			"error": "Failed to initiate OAuth",
		})
	}

	// Create connection link using v3 endpoint
	composioURL := "https://backend.composio.dev/api/v3/connected_accounts/link"
	req, err := http.NewRequest("POST", composioURL, bytes.NewBuffer(jsonData))
	if err != nil {
		log.Printf("❌ [COMPOSIO] Failed to create request: %v", err)
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{
			"error": "Failed to initiate OAuth",
		})
	}

	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("x-api-key", composioAPIKey)

	resp, err := h.httpClient.Do(req)
	if err != nil {
		log.Printf("❌ [COMPOSIO] Failed to call Composio API: %v", err)
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{
			"error": "Failed to initiate OAuth",
		})
	}
	defer resp.Body.Close()

	respBody, _ := io.ReadAll(resp.Body)

	if resp.StatusCode >= 400 {
		log.Printf("❌ [COMPOSIO] API error (status %d): %s", resp.StatusCode, string(respBody))
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{
			"error": fmt.Sprintf("Composio API error: %s", string(respBody)),
		})
	}

	// Parse response to get redirectUrl
	var composioResp map[string]interface{}
	if err := json.Unmarshal(respBody, &composioResp); err != nil {
		log.Printf("❌ [COMPOSIO] Failed to parse response: %v", err)
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{
			"error": "Failed to parse OAuth response",
		})
	}

	// v3 API returns redirect_url
	redirectURLFromComposio, ok := composioResp["redirect_url"].(string)
	if !ok {
		log.Printf("❌ [COMPOSIO] No redirect_url in response: %v", composioResp)
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{
			"error": "Invalid OAuth response from Composio",
		})
	}

	// ✅ SECURITY FIX: Append state token to OAuth URL for CSRF protection
	linkedinOauthURL, err := url.Parse(redirectURLFromComposio)
	if err != nil {
		log.Printf("❌ [COMPOSIO] Failed to parse OAuth URL: %v", err)
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{
			"error": "Invalid OAuth URL",
		})
	}
	linkedinQueryParams := linkedinOauthURL.Query()
	linkedinQueryParams.Set("state", stateToken)
	linkedinOauthURL.RawQuery = linkedinQueryParams.Encode()
	authURLWithState := linkedinOauthURL.String()

	log.Printf("✅ [COMPOSIO] Initiated LinkedIn OAuth for user %s", userID)

	// Return the OAuth URL to frontend
	return c.JSON(fiber.Map{
		"authUrl":     authURLWithState,
		"entityId":    entityID,
		"redirectUrl": redirectURL,
	})
}

// initiateComposioOAuth is a generic helper for Composio OAuth flows
func (h *ComposioAuthHandler) initiateComposioOAuth(c *fiber.Ctx, serviceName, envVarName, serviceSlug string) error {
	userID := c.Locals("user_id").(string)

	composioAPIKey := os.Getenv("COMPOSIO_API_KEY")
	if composioAPIKey == "" {
		log.Printf("❌ [COMPOSIO] COMPOSIO_API_KEY not set")
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{
			"error": "Composio integration not configured",
		})
	}

	entityID := userID

	// Validate and sanitize redirect URL
	redirectURL := c.Query("redirect_url")
	if redirectURL == "" {
		frontendURL := os.Getenv("FRONTEND_URL")
		if frontendURL == "" {
			if os.Getenv("ENVIRONMENT") == "production" {
				log.Printf("❌ [COMPOSIO] FRONTEND_URL not set in production")
				return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{
					"error": "Configuration error",
				})
			}
			frontendURL = "http://localhost:5173"
		}
		redirectURL = fmt.Sprintf("%s/settings?tab=credentials", frontendURL)
	} else {
		if err := validateRedirectURL(redirectURL); err != nil {
			log.Printf("⚠️ [COMPOSIO] Invalid redirect URL: %s", redirectURL)
			return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{
				"error": "Invalid redirect URL",
			})
		}
	}

	authConfigID := os.Getenv(envVarName)
	if authConfigID == "" {
		log.Printf("❌ [COMPOSIO] %s not set", envVarName)
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{
			"error": fmt.Sprintf("%s auth config not configured. Please set %s in environment.", serviceName, envVarName),
		})
	}

	stateToken, err := h.stateStore.GenerateState(userID, serviceSlug)
	if err != nil {
		log.Printf("❌ [COMPOSIO] Failed to generate state token: %v", err)
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{
			"error": "Failed to initiate OAuth",
		})
	}

	payload := map[string]interface{}{
		"auth_config_id": authConfigID,
		"user_id":        entityID,
	}

	jsonData, err := json.Marshal(payload)
	if err != nil {
		log.Printf("❌ [COMPOSIO] Failed to marshal request: %v", err)
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{
			"error": "Failed to initiate OAuth",
		})
	}

	composioURL := "https://backend.composio.dev/api/v3/connected_accounts/link"
	req, err := http.NewRequest("POST", composioURL, bytes.NewBuffer(jsonData))
	if err != nil {
		log.Printf("❌ [COMPOSIO] Failed to create request: %v", err)
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{
			"error": "Failed to initiate OAuth",
		})
	}

	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("x-api-key", composioAPIKey)

	resp, err := h.httpClient.Do(req)
	if err != nil {
		log.Printf("❌ [COMPOSIO] Failed to call Composio API: %v", err)
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{
			"error": "Failed to initiate OAuth",
		})
	}
	defer resp.Body.Close()

	respBody, _ := io.ReadAll(resp.Body)

	if resp.StatusCode >= 400 {
		log.Printf("❌ [COMPOSIO] API error (status %d): %s", resp.StatusCode, string(respBody))
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{
			"error": fmt.Sprintf("Composio API error: %s", string(respBody)),
		})
	}

	var composioResp map[string]interface{}
	if err := json.Unmarshal(respBody, &composioResp); err != nil {
		log.Printf("❌ [COMPOSIO] Failed to parse response: %v", err)
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{
			"error": "Failed to parse OAuth response",
		})
	}

	redirectURLFromComposio, ok := composioResp["redirect_url"].(string)
	if !ok {
		log.Printf("❌ [COMPOSIO] No redirect_url in response: %v", composioResp)
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{
			"error": "Invalid OAuth response from Composio",
		})
	}

	oauthURL, err := url.Parse(redirectURLFromComposio)
	if err != nil {
		log.Printf("❌ [COMPOSIO] Failed to parse OAuth URL: %v", err)
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{
			"error": "Invalid OAuth URL",
		})
	}
	queryParams := oauthURL.Query()
	queryParams.Set("state", stateToken)
	oauthURL.RawQuery = queryParams.Encode()
	authURLWithState := oauthURL.String()

	log.Printf("✅ [COMPOSIO] Initiated %s OAuth for user %s", serviceName, userID)

	return c.JSON(fiber.Map{
		"authUrl":     authURLWithState,
		"entityId":    entityID,
		"redirectUrl": redirectURL,
	})
}

// InitiateGoogleCalendarAuth initiates OAuth flow for Google Calendar via Composio
// GET /api/integrations/composio/googlecalendar/authorize
func (h *ComposioAuthHandler) InitiateGoogleCalendarAuth(c *fiber.Ctx) error {
	return h.initiateComposioOAuth(c, "Google Calendar", "COMPOSIO_GOOGLECALENDAR_AUTH_CONFIG_ID", "googlecalendar")
}

// InitiateGoogleDriveAuth initiates OAuth flow for Google Drive via Composio
// GET /api/integrations/composio/googledrive/authorize
func (h *ComposioAuthHandler) InitiateGoogleDriveAuth(c *fiber.Ctx) error {
	return h.initiateComposioOAuth(c, "Google Drive", "COMPOSIO_GOOGLEDRIVE_AUTH_CONFIG_ID", "googledrive")
}

// InitiateCanvaAuth initiates OAuth flow for Canva via Composio
// GET /api/integrations/composio/canva/authorize
func (h *ComposioAuthHandler) InitiateCanvaAuth(c *fiber.Ctx) error {
	return h.initiateComposioOAuth(c, "Canva", "COMPOSIO_CANVA_AUTH_CONFIG_ID", "canva")
}

// InitiateTwitterAuth initiates OAuth flow for Twitter/X via Composio
// GET /api/integrations/composio/twitter/authorize
func (h *ComposioAuthHandler) InitiateTwitterAuth(c *fiber.Ctx) error {
	return h.initiateComposioOAuth(c, "Twitter/X", "COMPOSIO_TWITTER_AUTH_CONFIG_ID", "twitter")
}

// InitiateYouTubeAuth initiates OAuth flow for YouTube via Composio
// GET /api/integrations/composio/youtube/authorize
func (h *ComposioAuthHandler) InitiateYouTubeAuth(c *fiber.Ctx) error {
	return h.initiateComposioOAuth(c, "YouTube", "COMPOSIO_YOUTUBE_AUTH_CONFIG_ID", "youtube")
}

// InitiateZoomAuth initiates OAuth flow for Zoom via Composio
// GET /api/integrations/composio/zoom/authorize
func (h *ComposioAuthHandler) InitiateZoomAuth(c *fiber.Ctx) error {
	return h.initiateComposioOAuth(c, "Zoom", "COMPOSIO_ZOOM_AUTH_CONFIG_ID", "zoom")
}

// validateRedirectURL validates that a redirect URL is safe
func validateRedirectURL(redirectURL string) error {
	parsedURL, err := url.Parse(redirectURL)
	if err != nil {
		return fmt.Errorf("invalid URL format")
	}

	// Must use HTTPS in production
	if os.Getenv("ENVIRONMENT") == "production" && parsedURL.Scheme != "https" {
		return fmt.Errorf("redirect URL must use HTTPS in production")
	}

	// Allow localhost for development
	if parsedURL.Scheme == "http" && (parsedURL.Hostname() == "localhost" || parsedURL.Hostname() == "127.0.0.1") {
		return nil
	}

	// Validate against FRONTEND_URL or ALLOWED_ORIGINS
	frontendURL := os.Getenv("FRONTEND_URL")
	allowedOrigins := os.Getenv("ALLOWED_ORIGINS")

	if frontendURL != "" {
		parsedFrontend, err := url.Parse(frontendURL)
		if err == nil && parsedURL.Host == parsedFrontend.Host {
			return nil
		}
	}

	if allowedOrigins != "" {
		origins := strings.Split(allowedOrigins, ",")
		for _, origin := range origins {
			origin = strings.TrimSpace(origin)
			parsedOrigin, err := url.Parse(origin)
			if err == nil && parsedURL.Host == parsedOrigin.Host {
				return nil
			}
		}
	}

	return fmt.Errorf("redirect URL not in allowed origins")
}
