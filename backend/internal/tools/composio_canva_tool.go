package tools

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
	"sync"
	"time"
)

// composioCanvaRateLimiter implements per-user rate limiting for Composio Canva API calls
type composioCanvaRateLimiter struct {
	requests map[string][]time.Time
	mutex    sync.RWMutex
	maxCalls int
	window   time.Duration
}

var globalCanvaRateLimiter = &composioCanvaRateLimiter{
	requests: make(map[string][]time.Time),
	maxCalls: 30,
	window:   1 * time.Minute,
}

func checkCanvaRateLimit(args map[string]interface{}) error {
	userID, ok := args["__user_id__"].(string)
	if !ok || userID == "" {
		log.Printf("⚠️ [CANVA] No user ID for rate limiting")
		return nil
	}

	globalCanvaRateLimiter.mutex.Lock()
	defer globalCanvaRateLimiter.mutex.Unlock()

	now := time.Now()
	windowStart := now.Add(-globalCanvaRateLimiter.window)

	timestamps := globalCanvaRateLimiter.requests[userID]
	validTimestamps := []time.Time{}
	for _, ts := range timestamps {
		if ts.After(windowStart) {
			validTimestamps = append(validTimestamps, ts)
		}
	}

	if len(validTimestamps) >= globalCanvaRateLimiter.maxCalls {
		return fmt.Errorf("rate limit exceeded: max %d requests per minute", globalCanvaRateLimiter.maxCalls)
	}

	validTimestamps = append(validTimestamps, now)
	globalCanvaRateLimiter.requests[userID] = validTimestamps
	return nil
}

// NewCanvaListDesignsTool creates a tool for listing Canva designs
func NewCanvaListDesignsTool() *Tool {
	return &Tool{
		Name:        "canva_list_designs",
		DisplayName: "Canva - List Designs",
		Description: `List all designs in the user's Canva account with pagination support.

WHEN TO USE THIS TOOL:
- User wants to see their Canva designs
- User asks "show my designs" or "list my Canva projects"
- User wants to browse their design library

PARAMETERS:
- continuation (optional): Pagination token from a previous response to get the next page of results.

RETURNS: List of designs with design IDs, titles, thumbnails, and creation dates. Includes a continuation token if more results are available.`,
		Icon:     "Palette",
		Source:   ToolSourceComposio,
		Category: "integration",
		Keywords: []string{"canva", "design", "list", "graphics", "composio"},
		Parameters: map[string]interface{}{
			"type": "object",
			"properties": map[string]interface{}{
				"credential_id": map[string]interface{}{
					"type":        "string",
					"description": "INTERNAL: Auto-injected by system. Do not set manually.",
				},
				"continuation": map[string]interface{}{
					"type":        "string",
					"description": "Pagination token for next page of results",
				},
			},
			"required": []string{},
		},
		Execute: executeCanvaListDesigns,
	}
}

func executeCanvaListDesigns(args map[string]interface{}) (string, error) {
	if err := checkCanvaRateLimit(args); err != nil {
		return "", err
	}

	credData, err := GetCredentialData(args, "composio_canva")
	if err != nil {
		return "", fmt.Errorf("failed to get Composio credentials: %w", err)
	}

	entityID, ok := credData["composio_entity_id"].(string)
	if !ok || entityID == "" {
		return "", fmt.Errorf("composio_entity_id not found in credentials")
	}

	composioAPIKey := GetComposioAPIKey()
	if composioAPIKey == "" {
		return "", fmt.Errorf("COMPOSIO_API_KEY environment variable not set")
	}

	input := map[string]interface{}{}

	if continuation, ok := args["continuation"].(string); ok {
		input["continuation"] = continuation
	}

	return callComposioCanvaAPI(composioAPIKey, entityID, "CANVA_LIST_USER_DESIGNS", input)
}

// NewCanvaGetDesignTool creates a tool for getting design details
func NewCanvaGetDesignTool() *Tool {
	return &Tool{
		Name:        "canva_get_design",
		DisplayName: "Canva - Get Design",
		Description: `Get detailed information about a specific Canva design including metadata, dimensions, and access info.

WHEN TO USE THIS TOOL:
- User wants details about a specific design
- User asks "show me info about this design" or needs design dimensions
- User needs the design ID or metadata for further operations

PARAMETERS:
- design_id (REQUIRED): The Canva design ID. Example: "DAFxyz123abc"

RETURNS: Design metadata including title, dimensions, page count, creation date, and access permissions.`,
		Icon:     "Image",
		Source:   ToolSourceComposio,
		Category: "integration",
		Keywords: []string{"canva", "design", "get", "details", "composio"},
		Parameters: map[string]interface{}{
			"type": "object",
			"properties": map[string]interface{}{
				"credential_id": map[string]interface{}{
					"type":        "string",
					"description": "INTERNAL: Auto-injected by system. Do not set manually.",
				},
				"design_id": map[string]interface{}{
					"type":        "string",
					"description": "ID of the design to retrieve",
				},
			},
			"required": []string{"design_id"},
		},
		Execute: executeCanvaGetDesign,
	}
}

func executeCanvaGetDesign(args map[string]interface{}) (string, error) {
	if err := checkCanvaRateLimit(args); err != nil {
		return "", err
	}

	credData, err := GetCredentialData(args, "composio_canva")
	if err != nil {
		return "", fmt.Errorf("failed to get Composio credentials: %w", err)
	}

	entityID, ok := credData["composio_entity_id"].(string)
	if !ok || entityID == "" {
		return "", fmt.Errorf("composio_entity_id not found in credentials")
	}

	composioAPIKey := GetComposioAPIKey()
	if composioAPIKey == "" {
		return "", fmt.Errorf("COMPOSIO_API_KEY environment variable not set")
	}

	input := map[string]interface{}{}

	if designID, ok := args["design_id"].(string); ok {
		input["design_id"] = designID
	}

	return callComposioCanvaAPI(composioAPIKey, entityID, "CANVA_FETCH_DESIGN_METADATA_AND_ACCESS_INFORMATION", input)
}

// NewCanvaCreateDesignTool creates a tool for creating new designs
func NewCanvaCreateDesignTool() *Tool {
	return &Tool{
		Name:        "canva_create_design",
		DisplayName: "Canva - Create Design",
		Description: `Create a new blank design in Canva with optional custom dimensions.

WHEN TO USE THIS TOOL:
- User wants to create a new Canva design
- User says "make a new poster" or "create an Instagram post design"
- User wants to start a new design project

PARAMETERS:
- design_type (optional): Preset design type. Example: "Poster", "Instagram Post", "Presentation", "Logo"
- title (optional): Name for the design. Example: "Summer Sale Banner"
- width (optional): Custom width in pixels. Example: 1080
- height (optional): Custom height in pixels. Example: 1920

RETURNS: The created design data including design ID, edit URL, and thumbnail.`,
		Icon:     "PlusSquare",
		Source:   ToolSourceComposio,
		Category: "integration",
		Keywords: []string{"canva", "design", "create", "new", "composio"},
		Parameters: map[string]interface{}{
			"type": "object",
			"properties": map[string]interface{}{
				"credential_id": map[string]interface{}{
					"type":        "string",
					"description": "INTERNAL: Auto-injected by system. Do not set manually.",
				},
				"design_type": map[string]interface{}{
					"type":        "string",
					"description": "Type of design to create (e.g., 'Poster', 'Instagram Post', 'Presentation')",
				},
				"title": map[string]interface{}{
					"type":        "string",
					"description": "Title for the new design",
				},
				"width": map[string]interface{}{
					"type":        "integer",
					"description": "Width in pixels (for custom size)",
				},
				"height": map[string]interface{}{
					"type":        "integer",
					"description": "Height in pixels (for custom size)",
				},
			},
			"required": []string{},
		},
		Execute: executeCanvaCreateDesign,
	}
}

func executeCanvaCreateDesign(args map[string]interface{}) (string, error) {
	if err := checkCanvaRateLimit(args); err != nil {
		return "", err
	}

	credData, err := GetCredentialData(args, "composio_canva")
	if err != nil {
		return "", fmt.Errorf("failed to get Composio credentials: %w", err)
	}

	entityID, ok := credData["composio_entity_id"].(string)
	if !ok || entityID == "" {
		return "", fmt.Errorf("composio_entity_id not found in credentials")
	}

	composioAPIKey := GetComposioAPIKey()
	if composioAPIKey == "" {
		return "", fmt.Errorf("COMPOSIO_API_KEY environment variable not set")
	}

	input := map[string]interface{}{}

	if designType, ok := args["design_type"].(string); ok {
		input["design_type"] = designType
	}
	if title, ok := args["title"].(string); ok {
		input["title"] = title
	}
	if width, ok := args["width"].(float64); ok {
		input["width"] = int(width)
	}
	if height, ok := args["height"].(float64); ok {
		input["height"] = int(height)
	}

	return callComposioCanvaAPI(composioAPIKey, entityID, "CANVA_CREATE_CANVA_DESIGN_WITH_OPTIONAL_ASSET", input)
}

// NewCanvaExportDesignTool creates a tool for exporting designs
func NewCanvaExportDesignTool() *Tool {
	return &Tool{
		Name:        "canva_export_design",
		DisplayName: "Canva - Export Design",
		Description: `Export a Canva design to a downloadable file format (PNG, JPG, PDF, or SVG).

WHEN TO USE THIS TOOL:
- User wants to download a Canva design
- User says "export my design as PDF" or "download this as PNG"
- User needs a shareable file from a Canva design

PARAMETERS:
- design_id (REQUIRED): The Canva design ID to export. Example: "DAFxyz123abc"
- format (optional): Output format - "png", "jpg", "pdf", or "svg". Default: "png"

RETURNS: Export job status with download URL when ready.`,
		Icon:     "Download",
		Source:   ToolSourceComposio,
		Category: "integration",
		Keywords: []string{"canva", "design", "export", "download", "composio"},
		Parameters: map[string]interface{}{
			"type": "object",
			"properties": map[string]interface{}{
				"credential_id": map[string]interface{}{
					"type":        "string",
					"description": "INTERNAL: Auto-injected by system. Do not set manually.",
				},
				"design_id": map[string]interface{}{
					"type":        "string",
					"description": "ID of the design to export",
				},
				"format": map[string]interface{}{
					"type":        "string",
					"description": "Export format: 'png', 'jpg', 'pdf', 'svg'",
					"enum":        []string{"png", "jpg", "pdf", "svg"},
				},
			},
			"required": []string{"design_id"},
		},
		Execute: executeCanvaExportDesign,
	}
}

func executeCanvaExportDesign(args map[string]interface{}) (string, error) {
	if err := checkCanvaRateLimit(args); err != nil {
		return "", err
	}

	credData, err := GetCredentialData(args, "composio_canva")
	if err != nil {
		return "", fmt.Errorf("failed to get Composio credentials: %w", err)
	}

	entityID, ok := credData["composio_entity_id"].(string)
	if !ok || entityID == "" {
		return "", fmt.Errorf("composio_entity_id not found in credentials")
	}

	composioAPIKey := GetComposioAPIKey()
	if composioAPIKey == "" {
		return "", fmt.Errorf("COMPOSIO_API_KEY environment variable not set")
	}

	input := map[string]interface{}{}

	if designID, ok := args["design_id"].(string); ok {
		input["design_id"] = designID
	}
	if format, ok := args["format"].(string); ok {
		input["format"] = format
	} else {
		input["format"] = "png"
	}

	return callComposioCanvaAPI(composioAPIKey, entityID, "CANVA_INITIATES_CANVA_DESIGN_EXPORT_JOB", input)
}

// NewCanvaListBrandTemplatesTool creates a tool for listing brand templates
func NewCanvaListBrandTemplatesTool() *Tool {
	return &Tool{
		Name:        "canva_list_brand_templates",
		DisplayName: "Canva - List Brand Templates",
		Description: `List brand templates from the user's Canva Brand Kit with pagination support.

WHEN TO USE THIS TOOL:
- User wants to see their brand templates
- User asks "show my brand templates" or "list brand kit templates"
- User needs to find a branded template to use

PARAMETERS:
- continuation (optional): Pagination token from a previous response to get the next page.

RETURNS: List of brand templates with template IDs, titles, and thumbnails.`,
		Icon:     "Layout",
		Source:   ToolSourceComposio,
		Category: "integration",
		Keywords: []string{"canva", "brand", "templates", "list", "composio"},
		Parameters: map[string]interface{}{
			"type": "object",
			"properties": map[string]interface{}{
				"credential_id": map[string]interface{}{
					"type":        "string",
					"description": "INTERNAL: Auto-injected by system. Do not set manually.",
				},
				"continuation": map[string]interface{}{
					"type":        "string",
					"description": "Pagination token for next page of results",
				},
			},
			"required": []string{},
		},
		Execute: executeCanvaListBrandTemplates,
	}
}

func executeCanvaListBrandTemplates(args map[string]interface{}) (string, error) {
	if err := checkCanvaRateLimit(args); err != nil {
		return "", err
	}

	credData, err := GetCredentialData(args, "composio_canva")
	if err != nil {
		return "", fmt.Errorf("failed to get Composio credentials: %w", err)
	}

	entityID, ok := credData["composio_entity_id"].(string)
	if !ok || entityID == "" {
		return "", fmt.Errorf("composio_entity_id not found in credentials")
	}

	composioAPIKey := GetComposioAPIKey()
	if composioAPIKey == "" {
		return "", fmt.Errorf("COMPOSIO_API_KEY environment variable not set")
	}

	input := map[string]interface{}{}

	if continuation, ok := args["continuation"].(string); ok {
		input["continuation"] = continuation
	}

	return callComposioCanvaAPI(composioAPIKey, entityID, "CANVA_ACCESS_USER_SPECIFIC_BRAND_TEMPLATES_LIST", input)
}

// NewCanvaUploadAssetTool creates a tool for uploading assets
func NewCanvaUploadAssetTool() *Tool {
	return &Tool{
		Name:        "canva_upload_asset",
		DisplayName: "Canva - Upload Asset",
		Description: `Upload an image or video asset to Canva's media library from a URL.

NOTE: This action is currently unavailable via Composio.

WHEN TO USE THIS TOOL:
- User wants to upload media to Canva
- User says "add this image to my Canva library"

RETURNS: Error - this action is currently unavailable.`,
		Icon:     "Upload",
		Source:   ToolSourceComposio,
		Category: "integration",
		Keywords: []string{"canva", "upload", "asset", "image", "composio"},
		Parameters: map[string]interface{}{
			"type": "object",
			"properties": map[string]interface{}{
				"credential_id": map[string]interface{}{
					"type":        "string",
					"description": "INTERNAL: Auto-injected by system. Do not set manually.",
				},
				"url": map[string]interface{}{
					"type":        "string",
					"description": "URL of the asset to upload",
				},
				"name": map[string]interface{}{
					"type":        "string",
					"description": "Name for the uploaded asset",
				},
			},
			"required": []string{"url"},
		},
		Execute: executeCanvaUploadAsset,
	}
}

func executeCanvaUploadAsset(args map[string]interface{}) (string, error) {
	// CANVA_UPLOAD_ASSET has been removed from Composio.
	return "", fmt.Errorf("the 'Upload Asset' action is currently unavailable via Composio")
}

// callComposioCanvaAPI makes a v2 API call to Composio for Canva actions
func callComposioCanvaAPI(apiKey string, entityID string, action string, input map[string]interface{}) (string, error) {
	connectedAccountID, err := getCanvaConnectedAccountID(apiKey, entityID, "canva")
	if err != nil {
		return "", fmt.Errorf("failed to get connected account: %w", err)
	}

	apiURL := "https://backend.composio.dev/api/v2/actions/" + action + "/execute"

	v2Payload := map[string]interface{}{
		"connectedAccountId": connectedAccountID,
		"input":              input,
	}

	jsonData, err := json.Marshal(v2Payload)
	if err != nil {
		return "", fmt.Errorf("failed to marshal request: %w", err)
	}

	log.Printf("🎨 [CANVA] Action: %s, ConnectedAccount: %s", action, maskSensitiveID(connectedAccountID))

	req, err := http.NewRequest("POST", apiURL, bytes.NewBuffer(jsonData))
	if err != nil {
		return "", fmt.Errorf("failed to create request: %w", err)
	}

	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("x-api-key", apiKey)

	client := &http.Client{Timeout: 60 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return "", fmt.Errorf("failed to send request: %w", err)
	}
	defer resp.Body.Close()

	respBody, _ := io.ReadAll(resp.Body)

	if resp.StatusCode >= 400 {
		log.Printf("❌ [CANVA] API error (status %d) for action %s: %s", resp.StatusCode, action, string(respBody))
		if resp.StatusCode == 429 {
			return "", fmt.Errorf("rate limit exceeded, please try again later")
		}
		return "", fmt.Errorf("API error (status %d): %s", resp.StatusCode, string(respBody))
	}

	var apiResponse map[string]interface{}
	if err := json.Unmarshal(respBody, &apiResponse); err != nil {
		return string(respBody), nil
	}

	result, _ := json.MarshalIndent(apiResponse, "", "  ")
	return string(result), nil
}

// getCanvaConnectedAccountID retrieves the connected account ID from Composio v3 API
func getCanvaConnectedAccountID(apiKey string, userID string, appName string) (string, error) {
	baseURL := "https://backend.composio.dev/api/v3/connected_accounts"
	params := url.Values{}
	params.Add("user_ids", userID)
	fullURL := baseURL + "?" + params.Encode()

	req, err := http.NewRequest("GET", fullURL, nil)
	if err != nil {
		return "", fmt.Errorf("failed to create request: %w", err)
	}

	req.Header.Set("x-api-key", apiKey)

	client := &http.Client{Timeout: 30 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return "", fmt.Errorf("failed to fetch connected accounts: %w", err)
	}
	defer resp.Body.Close()

	respBody, _ := io.ReadAll(resp.Body)

	if resp.StatusCode >= 400 {
		return "", fmt.Errorf("Composio API error (status %d): %s", resp.StatusCode, string(respBody))
	}

	var response struct {
		Items []struct {
			ID      string `json:"id"`
			Toolkit struct {
				Slug string `json:"slug"`
			} `json:"toolkit"`
			Deprecated struct {
				UUID string `json:"uuid"`
			} `json:"deprecated"`
		} `json:"items"`
	}
	if err := json.Unmarshal(respBody, &response); err != nil {
		return "", fmt.Errorf("failed to parse response: %w", err)
	}

	for _, account := range response.Items {
		if account.Toolkit.Slug == appName {
			if account.Deprecated.UUID != "" {
				return account.Deprecated.UUID, nil
			}
			return account.ID, nil
		}
	}

	return "", fmt.Errorf("no %s connection found for user. Please connect your Canva account first", appName)
}
