package handlers

import (
	"bytes"
	"claraverse/internal/models"
	"claraverse/internal/services"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"
)

// CredentialTester handles testing credentials for different integrations
type CredentialTester struct {
	credentialService *services.CredentialService
	httpClient        *http.Client
}

// NewCredentialTester creates a new credential tester
func NewCredentialTester(credentialService *services.CredentialService) *CredentialTester {
	return &CredentialTester{
		credentialService: credentialService,
		httpClient: &http.Client{
			Timeout: 10 * time.Second,
		},
	}
}

// Test tests a credential by making a real API call based on integration type
func (t *CredentialTester) Test(ctx context.Context, cred *models.DecryptedCredential) *models.TestCredentialResponse {
	switch cred.IntegrationType {
	case "discord":
		return t.testDiscord(ctx, cred.Data)
	case "slack":
		return t.testSlack(ctx, cred.Data)
	case "telegram":
		return t.testTelegram(ctx, cred.Data)
	case "telegram_bot":
		return t.testTelegramBot(ctx, cred.Data)
	case "teams":
		return t.testTeams(ctx, cred.Data)
	case "notion":
		return t.testNotion(ctx, cred.Data)
	case "github":
		return t.testGitHub(ctx, cred.Data)
	case "gitlab":
		return t.testGitLab(ctx, cred.Data)
	case "linear":
		return t.testLinear(ctx, cred.Data)
	case "jira":
		return t.testJira(ctx, cred.Data)
	case "airtable":
		return t.testAirtable(ctx, cred.Data)
	case "trello":
		return t.testTrello(ctx, cred.Data)
	case "hubspot":
		return t.testHubSpot(ctx, cred.Data)
	case "sendgrid":
		return t.testSendGrid(ctx, cred.Data)
	case "brevo":
		return t.testBrevo(ctx, cred.Data)
	case "mailchimp":
		return t.testMailchimp(ctx, cred.Data)
	case "openai":
		return t.testOpenAI(ctx, cred.Data)
	case "anthropic":
		return t.testAnthropic(ctx, cred.Data)
	case "google_ai":
		return t.testGoogleAI(ctx, cred.Data)
	case "google_chat":
		return t.testGoogleChat(ctx, cred.Data)
	case "zoom":
		return t.testZoom(ctx, cred.Data)
	case "referralmonk":
		return t.testReferralMonk(ctx, cred.Data)
	case "composio_googlesheets":
		return t.testComposioGoogleSheets(ctx, cred.Data)
	case "composio_gmail":
		return t.testComposioGmail(ctx, cred.Data)
	case "composio_linkedin":
		return t.testComposioLinkedIn(ctx, cred.Data)
	case "composio_googlecalendar":
		return t.testComposioGoogleCalendar(ctx, cred.Data)
	case "composio_googledrive":
		return t.testComposioGoogleDrive(ctx, cred.Data)
	case "composio_canva":
		return t.testComposioCanva(ctx, cred.Data)
	case "composio_twitter":
		return t.testComposioTwitter(ctx, cred.Data)
	case "composio_youtube":
		return t.testComposioYouTube(ctx, cred.Data)
	case "composio_zoom":
		return t.testComposioZoom(ctx, cred.Data)
	case "unipile":
		return t.testUnipile(ctx, cred.Data)
	case "custom_webhook":
		return t.testCustomWebhook(ctx, cred.Data)
	case "rest_api":
		return t.testRestAPI(ctx, cred.Data)
	case "mongodb":
		return t.testMongoDB(ctx, cred.Data)
	case "redis":
		return t.testRedis(ctx, cred.Data)
	default:
		return &models.TestCredentialResponse{
			Success: false,
			Message: "Testing not implemented for this integration type",
		}
	}
}

// testDiscord tests a Discord webhook by sending a test message
func (t *CredentialTester) testDiscord(ctx context.Context, data map[string]interface{}) *models.TestCredentialResponse {
	webhookURL, ok := data["webhook_url"].(string)
	if !ok || webhookURL == "" {
		return &models.TestCredentialResponse{
			Success: false,
			Message: "Webhook URL is required",
		}
	}

	payload := map[string]string{
		"content": "🔗 **ClaraVerse Test** - Webhook connection verified!",
	}
	body, _ := json.Marshal(payload)

	req, _ := http.NewRequestWithContext(ctx, "POST", webhookURL, bytes.NewBuffer(body))
	req.Header.Set("Content-Type", "application/json")

	resp, err := t.httpClient.Do(req)
	if err != nil {
		return &models.TestCredentialResponse{
			Success: false,
			Message: "Failed to connect to Discord",
			Details: err.Error(),
		}
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 200 && resp.StatusCode < 300 {
		return &models.TestCredentialResponse{
			Success: true,
			Message: "Discord webhook is working! A test message was sent.",
		}
	}

	return &models.TestCredentialResponse{
		Success: false,
		Message: fmt.Sprintf("Discord returned status %d", resp.StatusCode),
	}
}

// testSlack tests a Slack webhook
func (t *CredentialTester) testSlack(ctx context.Context, data map[string]interface{}) *models.TestCredentialResponse {
	webhookURL, ok := data["webhook_url"].(string)
	if !ok || webhookURL == "" {
		return &models.TestCredentialResponse{
			Success: false,
			Message: "Webhook URL is required",
		}
	}

	payload := map[string]string{
		"text": "🔗 *ClaraVerse Test* - Webhook connection verified!",
	}
	body, _ := json.Marshal(payload)

	req, _ := http.NewRequestWithContext(ctx, "POST", webhookURL, bytes.NewBuffer(body))
	req.Header.Set("Content-Type", "application/json")

	resp, err := t.httpClient.Do(req)
	if err != nil {
		return &models.TestCredentialResponse{
			Success: false,
			Message: "Failed to connect to Slack",
			Details: err.Error(),
		}
	}
	defer resp.Body.Close()

	if resp.StatusCode == 200 {
		return &models.TestCredentialResponse{
			Success: true,
			Message: "Slack webhook is working! A test message was sent.",
		}
	}

	return &models.TestCredentialResponse{
		Success: false,
		Message: fmt.Sprintf("Slack returned status %d", resp.StatusCode),
	}
}

// testTelegram tests a Telegram bot token
func (t *CredentialTester) testTelegram(ctx context.Context, data map[string]interface{}) *models.TestCredentialResponse {
	botToken, ok := data["bot_token"].(string)
	if !ok || botToken == "" {
		return &models.TestCredentialResponse{
			Success: false,
			Message: "Bot token is required",
		}
	}

	chatID, ok := data["chat_id"].(string)
	if !ok || chatID == "" {
		return &models.TestCredentialResponse{
			Success: false,
			Message: "Chat ID is required",
		}
	}

	url := fmt.Sprintf("https://api.telegram.org/bot%s/sendMessage", botToken)
	payload := map[string]string{
		"chat_id": chatID,
		"text":    "🔗 ClaraVerse Test - Bot connection verified!",
	}
	body, _ := json.Marshal(payload)

	req, _ := http.NewRequestWithContext(ctx, "POST", url, bytes.NewBuffer(body))
	req.Header.Set("Content-Type", "application/json")

	resp, err := t.httpClient.Do(req)
	if err != nil {
		return &models.TestCredentialResponse{
			Success: false,
			Message: "Failed to connect to Telegram",
			Details: err.Error(),
		}
	}
	defer resp.Body.Close()

	var result map[string]interface{}
	json.NewDecoder(resp.Body).Decode(&result)

	if ok, _ := result["ok"].(bool); ok {
		return &models.TestCredentialResponse{
			Success: true,
			Message: "Telegram bot is working! A test message was sent.",
		}
	}

	description, _ := result["description"].(string)
	return &models.TestCredentialResponse{
		Success: false,
		Message: "Telegram API error",
		Details: description,
	}
}

// testTelegramBot tests a Telegram bot token using getMe API (for webhook/channel integrations)
func (t *CredentialTester) testTelegramBot(ctx context.Context, data map[string]interface{}) *models.TestCredentialResponse {
	botToken, ok := data["bot_token"].(string)
	if !ok || botToken == "" {
		return &models.TestCredentialResponse{
			Success: false,
			Message: "Bot token is required",
		}
	}

	// Use getMe API to verify the bot token is valid
	url := fmt.Sprintf("https://api.telegram.org/bot%s/getMe", botToken)

	req, _ := http.NewRequestWithContext(ctx, "GET", url, nil)

	resp, err := t.httpClient.Do(req)
	if err != nil {
		return &models.TestCredentialResponse{
			Success: false,
			Message: "Failed to connect to Telegram",
			Details: err.Error(),
		}
	}
	defer resp.Body.Close()

	var result map[string]interface{}
	json.NewDecoder(resp.Body).Decode(&result)

	if ok, _ := result["ok"].(bool); ok {
		// Extract bot info
		if botResult, ok := result["result"].(map[string]interface{}); ok {
			botUsername, _ := botResult["username"].(string)
			botFirstName, _ := botResult["first_name"].(string)
			return &models.TestCredentialResponse{
				Success: true,
				Message: fmt.Sprintf("Bot verified: @%s (%s)", botUsername, botFirstName),
			}
		}
		return &models.TestCredentialResponse{
			Success: true,
			Message: "Telegram bot token is valid!",
		}
	}

	description, _ := result["description"].(string)
	return &models.TestCredentialResponse{
		Success: false,
		Message: "Invalid bot token",
		Details: description,
	}
}

// testTeams tests a Microsoft Teams webhook
func (t *CredentialTester) testTeams(ctx context.Context, data map[string]interface{}) *models.TestCredentialResponse {
	webhookURL, ok := data["webhook_url"].(string)
	if !ok || webhookURL == "" {
		return &models.TestCredentialResponse{
			Success: false,
			Message: "Webhook URL is required",
		}
	}

	payload := map[string]string{
		"text": "🔗 **ClaraVerse Test** - Webhook connection verified!",
	}
	body, _ := json.Marshal(payload)

	req, _ := http.NewRequestWithContext(ctx, "POST", webhookURL, bytes.NewBuffer(body))
	req.Header.Set("Content-Type", "application/json")

	resp, err := t.httpClient.Do(req)
	if err != nil {
		return &models.TestCredentialResponse{
			Success: false,
			Message: "Failed to connect to Teams",
			Details: err.Error(),
		}
	}
	defer resp.Body.Close()

	if resp.StatusCode == 200 {
		return &models.TestCredentialResponse{
			Success: true,
			Message: "Teams webhook is working! A test message was sent.",
		}
	}

	return &models.TestCredentialResponse{
		Success: false,
		Message: fmt.Sprintf("Teams returned status %d", resp.StatusCode),
	}
}

// testNotion tests a Notion API key
func (t *CredentialTester) testNotion(ctx context.Context, data map[string]interface{}) *models.TestCredentialResponse {
	apiKey, ok := data["api_key"].(string)
	if !ok || apiKey == "" {
		return &models.TestCredentialResponse{
			Success: false,
			Message: "API key is required",
		}
	}

	req, _ := http.NewRequestWithContext(ctx, "GET", "https://api.notion.com/v1/users/me", nil)
	req.Header.Set("Authorization", "Bearer "+apiKey)
	req.Header.Set("Notion-Version", "2022-06-28")

	resp, err := t.httpClient.Do(req)
	if err != nil {
		return &models.TestCredentialResponse{
			Success: false,
			Message: "Failed to connect to Notion",
			Details: err.Error(),
		}
	}
	defer resp.Body.Close()

	if resp.StatusCode == 200 {
		var result map[string]interface{}
		json.NewDecoder(resp.Body).Decode(&result)
		name, _ := result["name"].(string)
		return &models.TestCredentialResponse{
			Success: true,
			Message: "Notion API key is valid!",
			Details: fmt.Sprintf("Connected as: %s", name),
		}
	}

	return &models.TestCredentialResponse{
		Success: false,
		Message: fmt.Sprintf("Notion returned status %d", resp.StatusCode),
	}
}

// testGitHub tests a GitHub personal access token
func (t *CredentialTester) testGitHub(ctx context.Context, data map[string]interface{}) *models.TestCredentialResponse {
	token, ok := data["personal_access_token"].(string)
	if !ok || token == "" {
		return &models.TestCredentialResponse{
			Success: false,
			Message: "Personal access token is required",
		}
	}

	req, _ := http.NewRequestWithContext(ctx, "GET", "https://api.github.com/user", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Accept", "application/vnd.github.v3+json")

	resp, err := t.httpClient.Do(req)
	if err != nil {
		return &models.TestCredentialResponse{
			Success: false,
			Message: "Failed to connect to GitHub",
			Details: err.Error(),
		}
	}
	defer resp.Body.Close()

	if resp.StatusCode == 200 {
		var result map[string]interface{}
		json.NewDecoder(resp.Body).Decode(&result)
		login, _ := result["login"].(string)
		return &models.TestCredentialResponse{
			Success: true,
			Message: "GitHub token is valid!",
			Details: fmt.Sprintf("Authenticated as: %s", login),
		}
	}

	return &models.TestCredentialResponse{
		Success: false,
		Message: fmt.Sprintf("GitHub returned status %d", resp.StatusCode),
	}
}

// testGitLab tests a GitLab personal access token
func (t *CredentialTester) testGitLab(ctx context.Context, data map[string]interface{}) *models.TestCredentialResponse {
	token, ok := data["personal_access_token"].(string)
	if !ok || token == "" {
		return &models.TestCredentialResponse{
			Success: false,
			Message: "Personal access token is required",
		}
	}

	baseURL := "https://gitlab.com"
	if url, ok := data["base_url"].(string); ok && url != "" {
		baseURL = strings.TrimSuffix(url, "/")
	}

	req, _ := http.NewRequestWithContext(ctx, "GET", baseURL+"/api/v4/user", nil)
	req.Header.Set("PRIVATE-TOKEN", token)

	resp, err := t.httpClient.Do(req)
	if err != nil {
		return &models.TestCredentialResponse{
			Success: false,
			Message: "Failed to connect to GitLab",
			Details: err.Error(),
		}
	}
	defer resp.Body.Close()

	if resp.StatusCode == 200 {
		var result map[string]interface{}
		json.NewDecoder(resp.Body).Decode(&result)
		username, _ := result["username"].(string)
		return &models.TestCredentialResponse{
			Success: true,
			Message: "GitLab token is valid!",
			Details: fmt.Sprintf("Authenticated as: %s", username),
		}
	}

	return &models.TestCredentialResponse{
		Success: false,
		Message: fmt.Sprintf("GitLab returned status %d", resp.StatusCode),
	}
}

// testLinear tests a Linear API key
func (t *CredentialTester) testLinear(ctx context.Context, data map[string]interface{}) *models.TestCredentialResponse {
	apiKey, ok := data["api_key"].(string)
	if !ok || apiKey == "" {
		return &models.TestCredentialResponse{
			Success: false,
			Message: "API key is required",
		}
	}

	query := `{"query": "{ viewer { id name email } }"}`
	req, _ := http.NewRequestWithContext(ctx, "POST", "https://api.linear.app/graphql", strings.NewReader(query))
	req.Header.Set("Authorization", apiKey)
	req.Header.Set("Content-Type", "application/json")

	resp, err := t.httpClient.Do(req)
	if err != nil {
		return &models.TestCredentialResponse{
			Success: false,
			Message: "Failed to connect to Linear",
			Details: err.Error(),
		}
	}
	defer resp.Body.Close()

	if resp.StatusCode == 200 {
		var result map[string]interface{}
		json.NewDecoder(resp.Body).Decode(&result)
		if data, ok := result["data"].(map[string]interface{}); ok {
			if viewer, ok := data["viewer"].(map[string]interface{}); ok {
				name, _ := viewer["name"].(string)
				return &models.TestCredentialResponse{
					Success: true,
					Message: "Linear API key is valid!",
					Details: fmt.Sprintf("Authenticated as: %s", name),
				}
			}
		}
	}

	return &models.TestCredentialResponse{
		Success: false,
		Message: fmt.Sprintf("Linear returned status %d", resp.StatusCode),
	}
}

// testJira tests Jira credentials
func (t *CredentialTester) testJira(ctx context.Context, data map[string]interface{}) *models.TestCredentialResponse {
	email, _ := data["email"].(string)
	apiToken, _ := data["api_token"].(string)
	domain, _ := data["domain"].(string)

	if email == "" || apiToken == "" || domain == "" {
		return &models.TestCredentialResponse{
			Success: false,
			Message: "Email, API token, and domain are required",
		}
	}

	url := fmt.Sprintf("https://%s/rest/api/3/myself", domain)
	req, _ := http.NewRequestWithContext(ctx, "GET", url, nil)
	req.SetBasicAuth(email, apiToken)
	req.Header.Set("Accept", "application/json")

	resp, err := t.httpClient.Do(req)
	if err != nil {
		return &models.TestCredentialResponse{
			Success: false,
			Message: "Failed to connect to Jira",
			Details: err.Error(),
		}
	}
	defer resp.Body.Close()

	if resp.StatusCode == 200 {
		var result map[string]interface{}
		json.NewDecoder(resp.Body).Decode(&result)
		displayName, _ := result["displayName"].(string)
		return &models.TestCredentialResponse{
			Success: true,
			Message: "Jira credentials are valid!",
			Details: fmt.Sprintf("Authenticated as: %s", displayName),
		}
	}

	return &models.TestCredentialResponse{
		Success: false,
		Message: fmt.Sprintf("Jira returned status %d", resp.StatusCode),
	}
}

// testAirtable tests an Airtable API key
func (t *CredentialTester) testAirtable(ctx context.Context, data map[string]interface{}) *models.TestCredentialResponse {
	apiKey, ok := data["api_key"].(string)
	if !ok || apiKey == "" {
		return &models.TestCredentialResponse{
			Success: false,
			Message: "API key is required",
		}
	}

	req, _ := http.NewRequestWithContext(ctx, "GET", "https://api.airtable.com/v0/meta/whoami", nil)
	req.Header.Set("Authorization", "Bearer "+apiKey)

	resp, err := t.httpClient.Do(req)
	if err != nil {
		return &models.TestCredentialResponse{
			Success: false,
			Message: "Failed to connect to Airtable",
			Details: err.Error(),
		}
	}
	defer resp.Body.Close()

	if resp.StatusCode == 200 {
		return &models.TestCredentialResponse{
			Success: true,
			Message: "Airtable API key is valid!",
		}
	}

	return &models.TestCredentialResponse{
		Success: false,
		Message: fmt.Sprintf("Airtable returned status %d", resp.StatusCode),
	}
}

// testTrello tests Trello credentials
func (t *CredentialTester) testTrello(ctx context.Context, data map[string]interface{}) *models.TestCredentialResponse {
	apiKey, _ := data["api_key"].(string)
	token, _ := data["token"].(string)

	if apiKey == "" || token == "" {
		return &models.TestCredentialResponse{
			Success: false,
			Message: "API key and token are required",
		}
	}

	url := fmt.Sprintf("https://api.trello.com/1/members/me?key=%s&token=%s", apiKey, token)
	req, _ := http.NewRequestWithContext(ctx, "GET", url, nil)

	resp, err := t.httpClient.Do(req)
	if err != nil {
		return &models.TestCredentialResponse{
			Success: false,
			Message: "Failed to connect to Trello",
			Details: err.Error(),
		}
	}
	defer resp.Body.Close()

	if resp.StatusCode == 200 {
		var result map[string]interface{}
		json.NewDecoder(resp.Body).Decode(&result)
		username, _ := result["username"].(string)
		return &models.TestCredentialResponse{
			Success: true,
			Message: "Trello credentials are valid!",
			Details: fmt.Sprintf("Authenticated as: %s", username),
		}
	}

	return &models.TestCredentialResponse{
		Success: false,
		Message: fmt.Sprintf("Trello returned status %d", resp.StatusCode),
	}
}

// testHubSpot tests a HubSpot access token
func (t *CredentialTester) testHubSpot(ctx context.Context, data map[string]interface{}) *models.TestCredentialResponse {
	accessToken, ok := data["access_token"].(string)
	if !ok || accessToken == "" {
		return &models.TestCredentialResponse{
			Success: false,
			Message: "Access token is required",
		}
	}

	req, _ := http.NewRequestWithContext(ctx, "GET", "https://api.hubapi.com/crm/v3/objects/contacts?limit=1", nil)
	req.Header.Set("Authorization", "Bearer "+accessToken)

	resp, err := t.httpClient.Do(req)
	if err != nil {
		return &models.TestCredentialResponse{
			Success: false,
			Message: "Failed to connect to HubSpot",
			Details: err.Error(),
		}
	}
	defer resp.Body.Close()

	if resp.StatusCode == 200 {
		return &models.TestCredentialResponse{
			Success: true,
			Message: "HubSpot access token is valid!",
		}
	}

	return &models.TestCredentialResponse{
		Success: false,
		Message: fmt.Sprintf("HubSpot returned status %d", resp.StatusCode),
	}
}

// testSendGrid tests a SendGrid API key and optionally verifies sender identity
func (t *CredentialTester) testSendGrid(ctx context.Context, data map[string]interface{}) *models.TestCredentialResponse {
	apiKey, ok := data["api_key"].(string)
	if !ok || apiKey == "" {
		return &models.TestCredentialResponse{
			Success: false,
			Message: "API key is required",
		}
	}

	// First, test the API key validity
	req, _ := http.NewRequestWithContext(ctx, "GET", "https://api.sendgrid.com/v3/user/profile", nil)
	req.Header.Set("Authorization", "Bearer "+apiKey)

	resp, err := t.httpClient.Do(req)
	if err != nil {
		return &models.TestCredentialResponse{
			Success: false,
			Message: "Failed to connect to SendGrid",
			Details: err.Error(),
		}
	}
	defer resp.Body.Close()

	if resp.StatusCode != 200 {
		return &models.TestCredentialResponse{
			Success: false,
			Message: fmt.Sprintf("SendGrid returned status %d - invalid API key", resp.StatusCode),
		}
	}

	// API key is valid - now check verified senders if from_email is provided
	fromEmail, hasFromEmail := data["from_email"].(string)
	if !hasFromEmail || fromEmail == "" {
		return &models.TestCredentialResponse{
			Success: true,
			Message: "SendGrid API key is valid!",
			Details: "Note: Add a 'Default From Email' to verify sender identity.",
		}
	}

	// Check verified senders
	sendersReq, _ := http.NewRequestWithContext(ctx, "GET", "https://api.sendgrid.com/v3/verified_senders", nil)
	sendersReq.Header.Set("Authorization", "Bearer "+apiKey)

	sendersResp, err := t.httpClient.Do(sendersReq)
	if err != nil {
		return &models.TestCredentialResponse{
			Success: true,
			Message: "SendGrid API key is valid!",
			Details: fmt.Sprintf("Could not verify sender '%s' - check SendGrid Sender Identity settings.", fromEmail),
		}
	}
	defer sendersResp.Body.Close()

	if sendersResp.StatusCode == 200 {
		var result map[string]interface{}
		json.NewDecoder(sendersResp.Body).Decode(&result)

		// Check if the from_email is in the verified senders list
		senderVerified := false
		if results, ok := result["results"].([]interface{}); ok {
			for _, sender := range results {
				if s, ok := sender.(map[string]interface{}); ok {
					if email, ok := s["from_email"].(string); ok {
						if strings.EqualFold(email, fromEmail) {
							if verified, ok := s["verified"].(bool); ok && verified {
								senderVerified = true
								break
							}
						}
					}
				}
			}
		}

		if senderVerified {
			return &models.TestCredentialResponse{
				Success: true,
				Message: "SendGrid API key and sender identity verified!",
				Details: fmt.Sprintf("Verified sender: %s", fromEmail),
			}
		}

		return &models.TestCredentialResponse{
			Success: true,
			Message: "SendGrid API key is valid, but sender not verified!",
			Details: fmt.Sprintf("'%s' is not a verified sender. Visit https://app.sendgrid.com/settings/sender_auth to verify it.", fromEmail),
		}
	}

	return &models.TestCredentialResponse{
		Success: true,
		Message: "SendGrid API key is valid!",
		Details: fmt.Sprintf("Could not check sender verification for '%s'.", fromEmail),
	}
}

// testBrevo tests a Brevo (SendInBlue) API key
func (t *CredentialTester) testBrevo(ctx context.Context, data map[string]interface{}) *models.TestCredentialResponse {
	apiKey, ok := data["api_key"].(string)
	if !ok || apiKey == "" {
		return &models.TestCredentialResponse{
			Success: false,
			Message: "API key is required",
		}
	}

	// Validate API key format
	if !strings.HasPrefix(apiKey, "xkeysib-") {
		return &models.TestCredentialResponse{
			Success: false,
			Message: "Invalid API key format - Brevo API keys start with 'xkeysib-'",
		}
	}

	// Test the API key by getting account info
	req, _ := http.NewRequestWithContext(ctx, "GET", "https://api.brevo.com/v3/account", nil)
	req.Header.Set("api-key", apiKey)
	req.Header.Set("Accept", "application/json")

	resp, err := t.httpClient.Do(req)
	if err != nil {
		return &models.TestCredentialResponse{
			Success: false,
			Message: "Failed to connect to Brevo",
			Details: err.Error(),
		}
	}
	defer resp.Body.Close()

	if resp.StatusCode != 200 {
		bodyBytes, _ := io.ReadAll(resp.Body)
		return &models.TestCredentialResponse{
			Success: false,
			Message: fmt.Sprintf("Brevo returned status %d - invalid API key", resp.StatusCode),
			Details: string(bodyBytes),
		}
	}

	var result map[string]interface{}
	json.NewDecoder(resp.Body).Decode(&result)

	// Extract account info
	email, _ := result["email"].(string)
	companyName := ""
	if plan, ok := result["plan"].([]interface{}); ok && len(plan) > 0 {
		if planInfo, ok := plan[0].(map[string]interface{}); ok {
			if name, ok := planInfo["type"].(string); ok {
				companyName = name
			}
		}
	}

	details := fmt.Sprintf("Account: %s", email)
	if companyName != "" {
		details += fmt.Sprintf(" (Plan: %s)", companyName)
	}

	// Check if from_email is provided and verify sender
	fromEmail, hasFromEmail := data["from_email"].(string)
	if hasFromEmail && fromEmail != "" {
		// Get senders list
		sendersReq, _ := http.NewRequestWithContext(ctx, "GET", "https://api.brevo.com/v3/senders", nil)
		sendersReq.Header.Set("api-key", apiKey)
		sendersReq.Header.Set("Accept", "application/json")

		sendersResp, err := t.httpClient.Do(sendersReq)
		if err == nil {
			defer sendersResp.Body.Close()
			if sendersResp.StatusCode == 200 {
				var sendersResult map[string]interface{}
				json.NewDecoder(sendersResp.Body).Decode(&sendersResult)

				senderFound := false
				if senders, ok := sendersResult["senders"].([]interface{}); ok {
					for _, sender := range senders {
						if s, ok := sender.(map[string]interface{}); ok {
							if senderEmail, ok := s["email"].(string); ok {
								if strings.EqualFold(senderEmail, fromEmail) {
									senderFound = true
									if active, ok := s["active"].(bool); ok && active {
										details += fmt.Sprintf("\nVerified sender: %s ✓", fromEmail)
									} else {
										details += fmt.Sprintf("\nSender '%s' found but not active", fromEmail)
									}
									break
								}
							}
						}
					}
				}
				if !senderFound {
					details += fmt.Sprintf("\nWarning: '%s' not found in verified senders", fromEmail)
				}
			}
		}
	}

	return &models.TestCredentialResponse{
		Success: true,
		Message: "Brevo API key is valid!",
		Details: details,
	}
}

// testMailchimp tests a Mailchimp API key
func (t *CredentialTester) testMailchimp(ctx context.Context, data map[string]interface{}) *models.TestCredentialResponse {
	apiKey, ok := data["api_key"].(string)
	if !ok || apiKey == "" {
		return &models.TestCredentialResponse{
			Success: false,
			Message: "API key is required",
		}
	}

	// Extract datacenter from API key (format: xxx-usX)
	parts := strings.Split(apiKey, "-")
	if len(parts) < 2 {
		return &models.TestCredentialResponse{
			Success: false,
			Message: "Invalid API key format",
		}
	}
	dc := parts[len(parts)-1]

	url := fmt.Sprintf("https://%s.api.mailchimp.com/3.0/", dc)
	req, _ := http.NewRequestWithContext(ctx, "GET", url, nil)
	req.SetBasicAuth("anystring", apiKey)

	resp, err := t.httpClient.Do(req)
	if err != nil {
		return &models.TestCredentialResponse{
			Success: false,
			Message: "Failed to connect to Mailchimp",
			Details: err.Error(),
		}
	}
	defer resp.Body.Close()

	if resp.StatusCode == 200 {
		var result map[string]interface{}
		json.NewDecoder(resp.Body).Decode(&result)
		accountName, _ := result["account_name"].(string)
		return &models.TestCredentialResponse{
			Success: true,
			Message: "Mailchimp API key is valid!",
			Details: fmt.Sprintf("Account: %s", accountName),
		}
	}

	return &models.TestCredentialResponse{
		Success: false,
		Message: fmt.Sprintf("Mailchimp returned status %d", resp.StatusCode),
	}
}

// testOpenAI tests an OpenAI API key
func (t *CredentialTester) testOpenAI(ctx context.Context, data map[string]interface{}) *models.TestCredentialResponse {
	apiKey, ok := data["api_key"].(string)
	if !ok || apiKey == "" {
		return &models.TestCredentialResponse{
			Success: false,
			Message: "API key is required",
		}
	}

	req, _ := http.NewRequestWithContext(ctx, "GET", "https://api.openai.com/v1/models", nil)
	req.Header.Set("Authorization", "Bearer "+apiKey)

	resp, err := t.httpClient.Do(req)
	if err != nil {
		return &models.TestCredentialResponse{
			Success: false,
			Message: "Failed to connect to OpenAI",
			Details: err.Error(),
		}
	}
	defer resp.Body.Close()

	if resp.StatusCode == 200 {
		return &models.TestCredentialResponse{
			Success: true,
			Message: "OpenAI API key is valid!",
		}
	}

	return &models.TestCredentialResponse{
		Success: false,
		Message: fmt.Sprintf("OpenAI returned status %d", resp.StatusCode),
	}
}

// testAnthropic tests an Anthropic API key
func (t *CredentialTester) testAnthropic(ctx context.Context, data map[string]interface{}) *models.TestCredentialResponse {
	apiKey, ok := data["api_key"].(string)
	if !ok || apiKey == "" {
		return &models.TestCredentialResponse{
			Success: false,
			Message: "API key is required",
		}
	}

	// Send a minimal message to test the API key
	payload := map[string]interface{}{
		"model":      "claude-3-haiku-20240307",
		"max_tokens": 10,
		"messages": []map[string]string{
			{"role": "user", "content": "Hi"},
		},
	}
	body, _ := json.Marshal(payload)

	req, _ := http.NewRequestWithContext(ctx, "POST", "https://api.anthropic.com/v1/messages", bytes.NewBuffer(body))
	req.Header.Set("x-api-key", apiKey)
	req.Header.Set("anthropic-version", "2023-06-01")
	req.Header.Set("Content-Type", "application/json")

	resp, err := t.httpClient.Do(req)
	if err != nil {
		return &models.TestCredentialResponse{
			Success: false,
			Message: "Failed to connect to Anthropic",
			Details: err.Error(),
		}
	}
	defer resp.Body.Close()

	if resp.StatusCode == 200 {
		return &models.TestCredentialResponse{
			Success: true,
			Message: "Anthropic API key is valid!",
		}
	}

	// Read error response
	bodyBytes, _ := io.ReadAll(resp.Body)
	return &models.TestCredentialResponse{
		Success: false,
		Message: fmt.Sprintf("Anthropic returned status %d", resp.StatusCode),
		Details: string(bodyBytes),
	}
}

// testGoogleAI tests a Google AI API key
func (t *CredentialTester) testGoogleAI(ctx context.Context, data map[string]interface{}) *models.TestCredentialResponse {
	apiKey, ok := data["api_key"].(string)
	if !ok || apiKey == "" {
		return &models.TestCredentialResponse{
			Success: false,
			Message: "API key is required",
		}
	}

	url := fmt.Sprintf("https://generativelanguage.googleapis.com/v1/models?key=%s", apiKey)
	req, _ := http.NewRequestWithContext(ctx, "GET", url, nil)

	resp, err := t.httpClient.Do(req)
	if err != nil {
		return &models.TestCredentialResponse{
			Success: false,
			Message: "Failed to connect to Google AI",
			Details: err.Error(),
		}
	}
	defer resp.Body.Close()

	if resp.StatusCode == 200 {
		return &models.TestCredentialResponse{
			Success: true,
			Message: "Google AI API key is valid!",
		}
	}

	return &models.TestCredentialResponse{
		Success: false,
		Message: fmt.Sprintf("Google AI returned status %d", resp.StatusCode),
	}
}

// testGoogleChat tests a Google Chat webhook
func (t *CredentialTester) testGoogleChat(ctx context.Context, data map[string]interface{}) *models.TestCredentialResponse {
	webhookURL, ok := data["webhook_url"].(string)
	if !ok || webhookURL == "" {
		return &models.TestCredentialResponse{
			Success: false,
			Message: "Webhook URL is required",
		}
	}

	// Validate it's a Google Chat webhook URL
	if !strings.Contains(webhookURL, "chat.googleapis.com") {
		return &models.TestCredentialResponse{
			Success: false,
			Message: "Invalid Google Chat webhook URL - must contain chat.googleapis.com",
		}
	}

	// Google Chat webhook payload format
	payload := map[string]string{
		"text": "🔗 *ClaraVerse Test* - Webhook connection verified!",
	}
	body, _ := json.Marshal(payload)

	req, _ := http.NewRequestWithContext(ctx, "POST", webhookURL, bytes.NewBuffer(body))
	req.Header.Set("Content-Type", "application/json; charset=UTF-8")

	resp, err := t.httpClient.Do(req)
	if err != nil {
		return &models.TestCredentialResponse{
			Success: false,
			Message: "Failed to connect to Google Chat",
			Details: err.Error(),
		}
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 200 && resp.StatusCode < 300 {
		return &models.TestCredentialResponse{
			Success: true,
			Message: "Google Chat webhook is working! A test message was sent.",
		}
	}

	// Read error response for details
	bodyBytes, _ := io.ReadAll(resp.Body)
	return &models.TestCredentialResponse{
		Success: false,
		Message: fmt.Sprintf("Google Chat returned status %d", resp.StatusCode),
		Details: string(bodyBytes),
	}
}

// testZoom tests Zoom Server-to-Server OAuth credentials
func (t *CredentialTester) testZoom(ctx context.Context, data map[string]interface{}) *models.TestCredentialResponse {
	accountID, _ := data["account_id"].(string)
	clientID, _ := data["client_id"].(string)
	clientSecret, _ := data["client_secret"].(string)

	if accountID == "" || clientID == "" || clientSecret == "" {
		return &models.TestCredentialResponse{
			Success: false,
			Message: "Account ID, Client ID, and Client Secret are required",
		}
	}

	// Try to get an OAuth access token
	tokenURL := "https://zoom.us/oauth/token"
	tokenData := fmt.Sprintf("grant_type=account_credentials&account_id=%s", accountID)

	req, _ := http.NewRequestWithContext(ctx, "POST", tokenURL, strings.NewReader(tokenData))

	// Basic auth with client_id:client_secret
	auth := base64.StdEncoding.EncodeToString([]byte(clientID + ":" + clientSecret))
	req.Header.Set("Authorization", "Basic "+auth)
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")

	resp, err := t.httpClient.Do(req)
	if err != nil {
		return &models.TestCredentialResponse{
			Success: false,
			Message: "Failed to connect to Zoom OAuth",
			Details: err.Error(),
		}
	}
	defer resp.Body.Close()

	bodyBytes, _ := io.ReadAll(resp.Body)

	if resp.StatusCode != 200 {
		var errorResp map[string]interface{}
		json.Unmarshal(bodyBytes, &errorResp)
		errorMsg := "Unknown error"
		if reason, ok := errorResp["reason"].(string); ok {
			errorMsg = reason
		} else if errStr, ok := errorResp["error"].(string); ok {
			errorMsg = errStr
		}
		return &models.TestCredentialResponse{
			Success: false,
			Message: fmt.Sprintf("Zoom OAuth failed: %s", errorMsg),
			Details: string(bodyBytes),
		}
	}

	var tokenResp map[string]interface{}
	json.Unmarshal(bodyBytes, &tokenResp)

	if _, ok := tokenResp["access_token"].(string); !ok {
		return &models.TestCredentialResponse{
			Success: false,
			Message: "Zoom OAuth returned invalid response",
			Details: string(bodyBytes),
		}
	}

	// Token obtained successfully - now verify we can list users (basic API test)
	accessToken := tokenResp["access_token"].(string)

	userReq, _ := http.NewRequestWithContext(ctx, "GET", "https://api.zoom.us/v2/users/me", nil)
	userReq.Header.Set("Authorization", "Bearer "+accessToken)

	userResp, err := t.httpClient.Do(userReq)
	if err != nil {
		return &models.TestCredentialResponse{
			Success: true,
			Message: "Zoom OAuth credentials are valid!",
			Details: "Token generated successfully, but could not verify API access.",
		}
	}
	defer userResp.Body.Close()

	if userResp.StatusCode == 200 {
		var userInfo map[string]interface{}
		json.NewDecoder(userResp.Body).Decode(&userInfo)
		email, _ := userInfo["email"].(string)
		firstName, _ := userInfo["first_name"].(string)
		lastName, _ := userInfo["last_name"].(string)

		name := strings.TrimSpace(firstName + " " + lastName)
		if name == "" {
			name = email
		}

		return &models.TestCredentialResponse{
			Success: true,
			Message: "Zoom credentials verified!",
			Details: fmt.Sprintf("Connected as: %s (%s)", name, email),
		}
	}

	return &models.TestCredentialResponse{
		Success: true,
		Message: "Zoom OAuth credentials are valid!",
		Details: "Token generated successfully.",
	}
}

// testCustomWebhook tests a custom webhook
func (t *CredentialTester) testCustomWebhook(ctx context.Context, data map[string]interface{}) *models.TestCredentialResponse {
	url, ok := data["url"].(string)
	if !ok || url == "" {
		return &models.TestCredentialResponse{
			Success: false,
			Message: "URL is required",
		}
	}

	method := "POST"
	if m, ok := data["method"].(string); ok && m != "" {
		method = m
	}

	payload := map[string]string{
		"test":    "true",
		"message": "ClaraVerse webhook test",
	}
	body, _ := json.Marshal(payload)

	req, _ := http.NewRequestWithContext(ctx, method, url, bytes.NewBuffer(body))
	req.Header.Set("Content-Type", "application/json")

	// Add authentication if configured
	if authType, ok := data["auth_type"].(string); ok && authType != "none" {
		authValue, _ := data["auth_value"].(string)
		switch authType {
		case "bearer":
			req.Header.Set("Authorization", "Bearer "+authValue)
		case "basic":
			// authValue should be "user:pass"
			parts := strings.SplitN(authValue, ":", 2)
			if len(parts) == 2 {
				req.SetBasicAuth(parts[0], parts[1])
			}
		case "api_key":
			req.Header.Set("X-API-Key", authValue)
		}
	}

	// Add custom headers
	if headers, ok := data["headers"].(string); ok && headers != "" {
		var headerMap map[string]string
		if err := json.Unmarshal([]byte(headers), &headerMap); err == nil {
			for k, v := range headerMap {
				req.Header.Set(k, v)
			}
		}
	}

	resp, err := t.httpClient.Do(req)
	if err != nil {
		return &models.TestCredentialResponse{
			Success: false,
			Message: "Failed to connect to webhook",
			Details: err.Error(),
		}
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 200 && resp.StatusCode < 300 {
		return &models.TestCredentialResponse{
			Success: true,
			Message: fmt.Sprintf("Webhook responded with status %d", resp.StatusCode),
		}
	}

	return &models.TestCredentialResponse{
		Success: false,
		Message: fmt.Sprintf("Webhook returned status %d", resp.StatusCode),
	}
}

// testRestAPI tests a REST API endpoint
func (t *CredentialTester) testRestAPI(ctx context.Context, data map[string]interface{}) *models.TestCredentialResponse {
	baseURL, ok := data["base_url"].(string)
	if !ok || baseURL == "" {
		return &models.TestCredentialResponse{
			Success: false,
			Message: "Base URL is required",
		}
	}

	req, _ := http.NewRequestWithContext(ctx, "GET", baseURL, nil)

	// Add authentication if configured
	if authType, ok := data["auth_type"].(string); ok && authType != "none" {
		authValue, _ := data["auth_value"].(string)
		switch authType {
		case "bearer":
			req.Header.Set("Authorization", "Bearer "+authValue)
		case "basic":
			parts := strings.SplitN(authValue, ":", 2)
			if len(parts) == 2 {
				req.SetBasicAuth(parts[0], parts[1])
			}
		case "api_key_header":
			headerName := "X-API-Key"
			if name, ok := data["auth_header_name"].(string); ok && name != "" {
				headerName = name
			}
			req.Header.Set(headerName, authValue)
		case "api_key_query":
			q := req.URL.Query()
			q.Add("api_key", authValue)
			req.URL.RawQuery = q.Encode()
		}
	}

	// Add default headers
	if headers, ok := data["headers"].(string); ok && headers != "" {
		var headerMap map[string]string
		if err := json.Unmarshal([]byte(headers), &headerMap); err == nil {
			for k, v := range headerMap {
				req.Header.Set(k, v)
			}
		}
	}

	resp, err := t.httpClient.Do(req)
	if err != nil {
		return &models.TestCredentialResponse{
			Success: false,
			Message: "Failed to connect to API",
			Details: err.Error(),
		}
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 200 && resp.StatusCode < 300 {
		return &models.TestCredentialResponse{
			Success: true,
			Message: fmt.Sprintf("API responded with status %d", resp.StatusCode),
		}
	}

	return &models.TestCredentialResponse{
		Success: false,
		Message: fmt.Sprintf("API returned status %d", resp.StatusCode),
	}
}

// testMongoDB tests MongoDB connection credentials
func (t *CredentialTester) testMongoDB(ctx context.Context, data map[string]interface{}) *models.TestCredentialResponse {
	connectionString, ok := data["connection_string"].(string)
	if !ok || connectionString == "" {
		return &models.TestCredentialResponse{
			Success: false,
			Message: "Connection string is required",
		}
	}

	database, _ := data["database"].(string)
	if database == "" {
		return &models.TestCredentialResponse{
			Success: false,
			Message: "Database name is required",
		}
	}

	// Import MongoDB driver dynamically to test connection
	// We'll use a simple HTTP-based approach to avoid adding heavy dependencies
	// For production, we use the actual MongoDB driver in the tools

	// Validate connection string format
	if !strings.HasPrefix(connectionString, "mongodb://") && !strings.HasPrefix(connectionString, "mongodb+srv://") {
		return &models.TestCredentialResponse{
			Success: false,
			Message: "Invalid connection string format",
			Details: "Connection string must start with 'mongodb://' or 'mongodb+srv://'",
		}
	}

	// Use MongoDB Go driver for actual connection test
	// Import is done at runtime via the tool execution
	return t.testMongoDBConnection(ctx, connectionString, database)
}

// testMongoDBConnection performs the actual MongoDB connection test
func (t *CredentialTester) testMongoDBConnection(ctx context.Context, connectionString, database string) *models.TestCredentialResponse {
	// We need to use the MongoDB driver here
	// Import: go.mongodb.org/mongo-driver/mongo
	// Since this is a handler and we want to keep it lightweight,
	// we'll call a helper that uses the actual driver

	// For now, return a placeholder that will be replaced with actual implementation
	// Using the MongoDB driver directly here

	// Create a context with timeout for the connection test
	testCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()

	// Attempt to connect using the MongoDB driver
	// Note: The actual implementation requires importing the MongoDB driver
	// which is already a dependency in this project (used in mongodb_tool.go)

	// We'll use a goroutine-based approach to test the connection
	// without blocking the handler for too long

	resultChan := make(chan *models.TestCredentialResponse, 1)

	go func() {
		result := testMongoDBWithDriver(testCtx, connectionString, database)
		resultChan <- result
	}()

	select {
	case result := <-resultChan:
		return result
	case <-testCtx.Done():
		return &models.TestCredentialResponse{
			Success: false,
			Message: "Connection test timed out",
			Details: "MongoDB server did not respond within 10 seconds",
		}
	}
}

// testRedis tests Redis connection credentials
func (t *CredentialTester) testRedis(ctx context.Context, data map[string]interface{}) *models.TestCredentialResponse {
	host, _ := data["host"].(string)
	if host == "" {
		host = "localhost"
	}

	port, _ := data["port"].(string)
	if port == "" {
		port = "6379"
	}

	password, _ := data["password"].(string)
	dbNum, _ := data["database"].(string)
	if dbNum == "" {
		dbNum = "0"
	}

	// Create a context with timeout
	testCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()

	resultChan := make(chan *models.TestCredentialResponse, 1)

	go func() {
		result := testRedisWithDriver(testCtx, host, port, password, dbNum)
		resultChan <- result
	}()

	select {
	case result := <-resultChan:
		return result
	case <-testCtx.Done():
		return &models.TestCredentialResponse{
			Success: false,
			Message: "Connection test timed out",
			Details: "Redis server did not respond within 10 seconds",
		}
	}
}

// testReferralMonk tests ReferralMonk API credentials by calling their API
func (t *CredentialTester) testReferralMonk(ctx context.Context, data map[string]interface{}) *models.TestCredentialResponse {
	apiToken, ok := data["api_token"].(string)
	if !ok || apiToken == "" {
		return &models.TestCredentialResponse{
			Success: false,
			Message: "API Token is required",
		}
	}

	apiSecret, ok := data["api_secret"].(string)
	if !ok || apiSecret == "" {
		return &models.TestCredentialResponse{
			Success: false,
			Message: "API Secret is required",
		}
	}

	// Make a simple API call to verify credentials - using a test endpoint if available
	// For now, we'll verify the credentials are properly formatted
	url := "https://ahaguru.referralmonk.com/api/campaign"

	// Create a minimal test payload that won't actually send a message
	// Note: This is a validation check - we're verifying the API responds to our credentials
	testPayload := map[string]interface{}{
		"template_name": "test_validation",
		"channel":       "whatsapp",
		"recipients":    []map[string]interface{}{},
	}

	body, _ := json.Marshal(testPayload)
	req, err := http.NewRequestWithContext(ctx, "POST", url, bytes.NewBuffer(body))
	if err != nil {
		return &models.TestCredentialResponse{
			Success: false,
			Message: "Failed to create request",
			Details: err.Error(),
		}
	}

	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Api-Token", apiToken)
	req.Header.Set("Api-Secret", apiSecret)

	resp, err := t.httpClient.Do(req)
	if err != nil {
		return &models.TestCredentialResponse{
			Success: false,
			Message: "Failed to connect to ReferralMonk API",
			Details: err.Error(),
		}
	}
	defer resp.Body.Close()

	bodyBytes, _ := io.ReadAll(resp.Body)

	// Check for authentication success (even if template doesn't exist, auth should work)
	// 401/403 = bad credentials
	// 400/404 = credentials work but request issue (which is expected for test)
	// 200/201 = success
	if resp.StatusCode == 401 || resp.StatusCode == 403 {
		return &models.TestCredentialResponse{
			Success: false,
			Message: "Invalid API credentials",
			Details: fmt.Sprintf("Authentication failed: %s", string(bodyBytes)),
		}
	}

	// If we get 200, 201, 400, or 404, credentials are valid (just the request format might be off)
	if resp.StatusCode < 500 {
		return &models.TestCredentialResponse{
			Success: true,
			Message: "ReferralMonk API credentials verified successfully",
			Details: "API token and secret are valid",
		}
	}

	// 500+ errors indicate server issues
	return &models.TestCredentialResponse{
		Success: false,
		Message: "ReferralMonk API server error",
		Details: string(bodyBytes),
	}
}

// testUnipile tests Unipile credentials by listing connected accounts
func (t *CredentialTester) testUnipile(ctx context.Context, data map[string]interface{}) *models.TestCredentialResponse {
	dsn, _ := data["dsn"].(string)
	accessToken, _ := data["access_token"].(string)

	if dsn == "" {
		return &models.TestCredentialResponse{
			Success: false,
			Message: "DSN (Data Source Name) is required",
		}
	}
	if accessToken == "" {
		return &models.TestCredentialResponse{
			Success: false,
			Message: "Access Token is required",
		}
	}

	// Build base URL from DSN
	baseURL := dsn
	if !strings.HasPrefix(baseURL, "http://") && !strings.HasPrefix(baseURL, "https://") {
		baseURL = "https://" + baseURL
	}
	baseURL = strings.TrimRight(baseURL, "/")

	// Test by listing accounts
	req, err := http.NewRequestWithContext(ctx, "GET", baseURL+"/api/v1/accounts", nil)
	if err != nil {
		return &models.TestCredentialResponse{
			Success: false,
			Message: "Failed to create request",
			Details: err.Error(),
		}
	}

	req.Header.Set("X-API-KEY", accessToken)
	req.Header.Set("Accept", "application/json")

	resp, err := t.httpClient.Do(req)
	if err != nil {
		return &models.TestCredentialResponse{
			Success: false,
			Message: "Failed to connect to Unipile",
			Details: err.Error(),
		}
	}
	defer resp.Body.Close()

	if resp.StatusCode == 401 || resp.StatusCode == 403 {
		return &models.TestCredentialResponse{
			Success: false,
			Message: "Invalid Unipile credentials",
			Details: "Check your DSN and Access Token",
		}
	}

	if resp.StatusCode >= 200 && resp.StatusCode < 300 {
		var result map[string]interface{}
		json.NewDecoder(resp.Body).Decode(&result)

		// Count connected accounts
		accountCount := 0
		if items, ok := result["items"].([]interface{}); ok {
			accountCount = len(items)
		}

		return &models.TestCredentialResponse{
			Success: true,
			Message: "Unipile credentials verified!",
			Details: fmt.Sprintf("Connected accounts: %d", accountCount),
		}
	}

	bodyBytes, _ := io.ReadAll(resp.Body)
	return &models.TestCredentialResponse{
		Success: false,
		Message: fmt.Sprintf("Unipile returned status %d", resp.StatusCode),
		Details: string(bodyBytes),
	}
}

// testComposioGoogleSheets tests Composio Google Sheets connection
func (t *CredentialTester) testComposioGoogleSheets(ctx context.Context, data map[string]interface{}) *models.TestCredentialResponse {
	return t.testComposioIntegration(ctx, data, "Google Sheets", "googlesheets", "")
}

// testComposioGmail tests Composio Gmail connection
func (t *CredentialTester) testComposioGmail(ctx context.Context, data map[string]interface{}) *models.TestCredentialResponse {
	return t.testComposioIntegration(ctx, data, "Gmail", "gmail", "GMAIL_LIST_LABELS")
}

// testComposioLinkedIn tests a Composio LinkedIn connection
func (t *CredentialTester) testComposioLinkedIn(ctx context.Context, data map[string]interface{}) *models.TestCredentialResponse {
	return t.testComposioIntegration(ctx, data, "LinkedIn", "linkedin", "")
}

// testComposioIntegration is a generic helper that tests any Composio integration.
// It verifies the connected account exists and optionally executes a lightweight read-only action.
func (t *CredentialTester) testComposioIntegration(
	ctx context.Context,
	data map[string]interface{},
	serviceName string,
	serviceSlug string,
	testAction string,
) *models.TestCredentialResponse {
	entityID, ok := data["composio_entity_id"].(string)
	if !ok || entityID == "" {
		return &models.TestCredentialResponse{
			Success: false,
			Message: "Entity ID is required",
		}
	}

	composioAPIKey := os.Getenv("COMPOSIO_API_KEY")
	if composioAPIKey == "" {
		return &models.TestCredentialResponse{
			Success: false,
			Message: "Composio integration not configured",
			Details: "COMPOSIO_API_KEY environment variable not set",
		}
	}

	log.Printf("🔍 [CREDENTIAL_TEST] Testing %s (slug: %s) for entity: %s", serviceName, serviceSlug, entityID)

	// Step 1: Verify connected account exists via v3 API
	connURL := fmt.Sprintf("https://backend.composio.dev/api/v3/connected_accounts?user_ids=%s", url.QueryEscape(entityID))
	req, err := http.NewRequestWithContext(ctx, "GET", connURL, nil)
	if err != nil {
		return &models.TestCredentialResponse{
			Success: false,
			Message: "Failed to create test request",
		}
	}
	req.Header.Set("x-api-key", composioAPIKey)

	resp, err := t.httpClient.Do(req)
	if err != nil {
		log.Printf("❌ [CREDENTIAL_TEST] Failed to connect to Composio API: %v", err)
		return &models.TestCredentialResponse{
			Success: false,
			Message: "Failed to connect to Composio API",
			Details: err.Error(),
		}
	}
	defer resp.Body.Close()

	bodyBytes, _ := io.ReadAll(resp.Body)

	if resp.StatusCode >= 400 {
		log.Printf("❌ [CREDENTIAL_TEST] Composio v3 API returned status %d: %s", resp.StatusCode, string(bodyBytes))
		return &models.TestCredentialResponse{
			Success: false,
			Message: fmt.Sprintf("Failed to verify %s connection", serviceName),
			Details: string(bodyBytes),
		}
	}

	var response struct {
		Items []map[string]interface{} `json:"items"`
	}
	if err := json.Unmarshal(bodyBytes, &response); err != nil {
		log.Printf("❌ [CREDENTIAL_TEST] Failed to parse Composio response: %v", err)
		return &models.TestCredentialResponse{
			Success: false,
			Message: "Failed to parse Composio response",
		}
	}

	log.Printf("🔍 [CREDENTIAL_TEST] Found %d connected accounts for entity %s", len(response.Items), entityID)

	// Log all toolkit slugs found for debugging
	for i, account := range response.Items {
		if toolkit, ok := account["toolkit"].(map[string]interface{}); ok {
			slug, _ := toolkit["slug"].(string)
			log.Printf("🔍 [CREDENTIAL_TEST]   Account %d: toolkit.slug=%s", i, slug)
		}
	}

	// Find the matching connected account
	var connectedAccountID string
	for _, account := range response.Items {
		if toolkit, ok := account["toolkit"].(map[string]interface{}); ok {
			if slug, ok := toolkit["slug"].(string); ok && slug == serviceSlug {
				// Extract connected account ID for action execution
				if deprecated, ok := account["deprecated"].(map[string]interface{}); ok {
					if uuid, ok := deprecated["uuid"].(string); ok {
						connectedAccountID = uuid
					}
				}
				if connectedAccountID == "" {
					if id, ok := account["id"].(string); ok {
						connectedAccountID = id
					}
				}
				break
			}
		}
	}

	if connectedAccountID == "" {
		log.Printf("❌ [CREDENTIAL_TEST] No %s connection found (looking for slug: %s)", serviceName, serviceSlug)
		return &models.TestCredentialResponse{
			Success: false,
			Message: fmt.Sprintf("No %s connection found", serviceName),
			Details: fmt.Sprintf("Please reconnect your %s account", serviceName),
		}
	}

	log.Printf("✅ [CREDENTIAL_TEST] Found %s connected account: %s", serviceName, connectedAccountID)

	// Step 2: If a test action is provided, execute it to verify the integration actually works
	if testAction != "" {
		actionURL := "https://backend.composio.dev/api/v2/actions/" + testAction + "/execute"
		payload := map[string]interface{}{
			"connectedAccountId": connectedAccountID,
			"input":              map[string]interface{}{},
		}
		jsonData, err := json.Marshal(payload)
		if err != nil {
			return &models.TestCredentialResponse{
				Success: true,
				Message: fmt.Sprintf("%s account connected (action test skipped)", serviceName),
				Details: fmt.Sprintf("Entity ID: %s", entityID),
			}
		}

		log.Printf("🔍 [CREDENTIAL_TEST] Executing test action: %s", testAction)

		actionReq, err := http.NewRequestWithContext(ctx, "POST", actionURL, bytes.NewBuffer(jsonData))
		if err != nil {
			return &models.TestCredentialResponse{
				Success: true,
				Message: fmt.Sprintf("%s account connected (action test skipped)", serviceName),
				Details: fmt.Sprintf("Entity ID: %s", entityID),
			}
		}
		actionReq.Header.Set("Content-Type", "application/json")
		actionReq.Header.Set("x-api-key", composioAPIKey)

		actionResp, err := t.httpClient.Do(actionReq)
		if err != nil {
			log.Printf("❌ [CREDENTIAL_TEST] Action request failed: %v", err)
			return &models.TestCredentialResponse{
				Success: true,
				Message: fmt.Sprintf("%s connected but action test failed: %v", serviceName, err),
				Details: fmt.Sprintf("Entity ID: %s", entityID),
			}
		}
		defer actionResp.Body.Close()

		actionBody, _ := io.ReadAll(actionResp.Body)
		log.Printf("🔍 [CREDENTIAL_TEST] Action %s returned status %d: %s", testAction, actionResp.StatusCode, string(actionBody))

		if actionResp.StatusCode >= 400 {
			return &models.TestCredentialResponse{
				Success: false,
				Message: fmt.Sprintf("%s connected but API call failed (status %d)", serviceName, actionResp.StatusCode),
				Details: string(actionBody),
			}
		}

		return &models.TestCredentialResponse{
			Success: true,
			Message: fmt.Sprintf("%s connected and verified successfully", serviceName),
			Details: fmt.Sprintf("Entity ID: %s, Action: %s", entityID, testAction),
		}
	}

	return &models.TestCredentialResponse{
		Success: true,
		Message: fmt.Sprintf("%s connected successfully via Composio", serviceName),
		Details: fmt.Sprintf("Entity ID: %s", entityID),
	}
}

// testComposioGoogleCalendar tests a Composio Google Calendar connection
func (t *CredentialTester) testComposioGoogleCalendar(ctx context.Context, data map[string]interface{}) *models.TestCredentialResponse {
	return t.testComposioIntegration(ctx, data, "Google Calendar", "googlecalendar", "GOOGLECALENDAR_LIST_CALENDARS")
}

// testComposioGoogleDrive tests a Composio Google Drive connection
func (t *CredentialTester) testComposioGoogleDrive(ctx context.Context, data map[string]interface{}) *models.TestCredentialResponse {
	return t.testComposioIntegration(ctx, data, "Google Drive", "googledrive", "GOOGLEDRIVE_LIST_FILES")
}

// testComposioCanva tests a Composio Canva connection
func (t *CredentialTester) testComposioCanva(ctx context.Context, data map[string]interface{}) *models.TestCredentialResponse {
	return t.testComposioIntegration(ctx, data, "Canva", "canva", "CANVA_LIST_USER_DESIGNS")
}

// testComposioTwitter tests a Composio Twitter/X connection
func (t *CredentialTester) testComposioTwitter(ctx context.Context, data map[string]interface{}) *models.TestCredentialResponse {
	return t.testComposioIntegration(ctx, data, "Twitter/X", "twitter", "")
}

// testComposioYouTube tests a Composio YouTube connection
func (t *CredentialTester) testComposioYouTube(ctx context.Context, data map[string]interface{}) *models.TestCredentialResponse {
	return t.testComposioIntegration(ctx, data, "YouTube", "youtube", "YOUTUBE_LIST_USER_SUBSCRIPTIONS")
}

// testComposioZoom tests a Composio Zoom connection
func (t *CredentialTester) testComposioZoom(ctx context.Context, data map[string]interface{}) *models.TestCredentialResponse {
	return t.testComposioIntegration(ctx, data, "Zoom", "zoom", "ZOOM_LIST_MEETINGS")
}
