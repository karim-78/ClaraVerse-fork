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

// composioDriveRateLimiter implements per-user rate limiting for Composio Drive API calls
type composioDriveRateLimiter struct {
	requests map[string][]time.Time
	mutex    sync.RWMutex
	maxCalls int
	window   time.Duration
}

var globalDriveRateLimiter = &composioDriveRateLimiter{
	requests: make(map[string][]time.Time),
	maxCalls: 50,
	window:   1 * time.Minute,
}

func checkDriveRateLimit(args map[string]interface{}) error {
	userID, ok := args["__user_id__"].(string)
	if !ok || userID == "" {
		log.Printf("⚠️ [GOOGLEDRIVE] No user ID for rate limiting")
		return nil
	}

	globalDriveRateLimiter.mutex.Lock()
	defer globalDriveRateLimiter.mutex.Unlock()

	now := time.Now()
	windowStart := now.Add(-globalDriveRateLimiter.window)

	timestamps := globalDriveRateLimiter.requests[userID]
	validTimestamps := []time.Time{}
	for _, ts := range timestamps {
		if ts.After(windowStart) {
			validTimestamps = append(validTimestamps, ts)
		}
	}

	if len(validTimestamps) >= globalDriveRateLimiter.maxCalls {
		return fmt.Errorf("rate limit exceeded: max %d requests per minute", globalDriveRateLimiter.maxCalls)
	}

	validTimestamps = append(validTimestamps, now)
	globalDriveRateLimiter.requests[userID] = validTimestamps
	return nil
}

// NewGoogleDriveListFilesTool creates a tool for listing files in Drive
func NewGoogleDriveListFilesTool() *Tool {
	return &Tool{
		Name:        "googledrive_list_files",
		DisplayName: "Google Drive - List Files",
		Description: `List files and folders in the user's Google Drive with optional filtering and sorting.

WHEN TO USE THIS TOOL:
- User wants to see their Google Drive files
- User asks "what files do I have" or "list my Drive"
- User wants to browse files in a specific folder

PARAMETERS:
- q (optional): Google Drive search query. Example: "name contains 'report'" or "mimeType='application/vnd.google-apps.folder'"
- folder_id (optional): List only files in this folder. Default: root folder.
- page_size (optional): Max files to return. Default: 10. Example: 25
- order_by (optional): Sort order. Example: "modifiedTime desc" or "name"

RETURNS: List of files with names, IDs, MIME types, sizes, and modification dates.`,
		Icon:     "HardDrive",
		Source:   ToolSourceComposio,
		Category: "integration",
		Keywords: []string{"google", "drive", "files", "list", "search", "composio"},
		Parameters: map[string]interface{}{
			"type": "object",
			"properties": map[string]interface{}{
				"credential_id": map[string]interface{}{
					"type":        "string",
					"description": "INTERNAL: Auto-injected by system. Do not set manually.",
				},
				"q": map[string]interface{}{
					"type":        "string",
					"description": "Search query (e.g., \"name contains 'report'\" or \"mimeType='application/vnd.google-apps.folder'\")",
				},
				"folder_id": map[string]interface{}{
					"type":        "string",
					"description": "ID of folder to list (default: root)",
				},
				"page_size": map[string]interface{}{
					"type":        "integer",
					"description": "Maximum number of files to return (default: 10)",
				},
				"order_by": map[string]interface{}{
					"type":        "string",
					"description": "Sort order (e.g., 'modifiedTime desc', 'name')",
				},
			},
			"required": []string{},
		},
		Execute: executeGoogleDriveListFiles,
	}
}

func executeGoogleDriveListFiles(args map[string]interface{}) (string, error) {
	if err := checkDriveRateLimit(args); err != nil {
		return "", err
	}

	credData, err := GetCredentialData(args, "composio_googledrive")
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

	if q, ok := args["q"].(string); ok {
		input["q"] = q
	}
	if folderID, ok := args["folder_id"].(string); ok {
		input["folder_id"] = folderID
	}
	if pageSize, ok := args["page_size"].(float64); ok {
		input["page_size"] = int(pageSize)
	}
	if orderBy, ok := args["order_by"].(string); ok {
		input["order_by"] = orderBy
	}

	return callComposioDriveAPI(composioAPIKey, entityID, "GOOGLEDRIVE_LIST_FILES", input)
}

// NewGoogleDriveGetFileTool creates a tool for getting file details
func NewGoogleDriveGetFileTool() *Tool {
	return &Tool{
		Name:        "googledrive_get_file",
		DisplayName: "Google Drive - Get File",
		Description: `Get metadata for a specific file or folder in Google Drive by its file ID.

WHEN TO USE THIS TOOL:
- User wants details about a specific file
- User asks "what is this file" or needs file size, owner, or sharing info
- Need to check file properties before downloading or sharing

PARAMETERS:
- file_id (REQUIRED): The Google Drive file ID. Example: "1a2b3c4d5e6f7g8h9i0j"

RETURNS: File metadata including name, MIME type, size, creation/modification dates, owner, and sharing status.`,
		Icon:     "File",
		Source:   ToolSourceComposio,
		Category: "integration",
		Keywords: []string{"google", "drive", "file", "get", "details", "composio"},
		Parameters: map[string]interface{}{
			"type": "object",
			"properties": map[string]interface{}{
				"credential_id": map[string]interface{}{
					"type":        "string",
					"description": "INTERNAL: Auto-injected by system. Do not set manually.",
				},
				"file_id": map[string]interface{}{
					"type":        "string",
					"description": "ID of the file to retrieve",
				},
			},
			"required": []string{"file_id"},
		},
		Execute: executeGoogleDriveGetFile,
	}
}

func executeGoogleDriveGetFile(args map[string]interface{}) (string, error) {
	if err := checkDriveRateLimit(args); err != nil {
		return "", err
	}

	credData, err := GetCredentialData(args, "composio_googledrive")
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

	if fileID, ok := args["file_id"].(string); ok {
		input["file_id"] = fileID
	}

	return callComposioDriveAPI(composioAPIKey, entityID, "GOOGLEDRIVE_GET_FILE_METADATA", input)
}

// NewGoogleDriveCreateFolderTool creates a tool for creating folders
func NewGoogleDriveCreateFolderTool() *Tool {
	return &Tool{
		Name:        "googledrive_create_folder",
		DisplayName: "Google Drive - Create Folder",
		Description: `Create a new folder in Google Drive, optionally inside an existing folder.

WHEN TO USE THIS TOOL:
- User wants to create a folder in Google Drive
- User says "make a new folder" or "create a folder called X"

PARAMETERS:
- name (REQUIRED): Folder name. Example: "Project Documents"
- parent_id (optional): ID of the parent folder. Default: root Drive folder.

RETURNS: The created folder's ID, name, and URL.`,
		Icon:     "FolderPlus",
		Source:   ToolSourceComposio,
		Category: "integration",
		Keywords: []string{"google", "drive", "folder", "create", "composio"},
		Parameters: map[string]interface{}{
			"type": "object",
			"properties": map[string]interface{}{
				"credential_id": map[string]interface{}{
					"type":        "string",
					"description": "INTERNAL: Auto-injected by system. Do not set manually.",
				},
				"name": map[string]interface{}{
					"type":        "string",
					"description": "Name of the folder to create",
				},
				"parent_id": map[string]interface{}{
					"type":        "string",
					"description": "ID of parent folder (optional, default: root)",
				},
			},
			"required": []string{"name"},
		},
		Execute: executeGoogleDriveCreateFolder,
	}
}

func executeGoogleDriveCreateFolder(args map[string]interface{}) (string, error) {
	if err := checkDriveRateLimit(args); err != nil {
		return "", err
	}

	credData, err := GetCredentialData(args, "composio_googledrive")
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

	if name, ok := args["name"].(string); ok {
		input["name"] = name
	}
	if parentID, ok := args["parent_id"].(string); ok {
		input["parent_id"] = parentID
	}

	return callComposioDriveAPI(composioAPIKey, entityID, "GOOGLEDRIVE_CREATE_FOLDER", input)
}

// NewGoogleDriveSearchFilesTool creates a tool for searching files
func NewGoogleDriveSearchFilesTool() *Tool {
	return &Tool{
		Name:        "googledrive_search_files",
		DisplayName: "Google Drive - Search Files",
		Description: `Search for files in Google Drive by name, content, or file type across the entire Drive.

WHEN TO USE THIS TOOL:
- User wants to find a file by name or content
- User says "find my budget spreadsheet" or "search for files about marketing"
- User needs to locate a file when they don't have the file ID

PARAMETERS:
- query (REQUIRED): Search text. Example: "quarterly report" or "budget 2024"
- file_type (optional): Filter by type - "document", "spreadsheet", "presentation", "pdf", "image", "folder"
- max_results (optional): Max results to return. Default: 10.

RETURNS: List of matching files with names, IDs, types, and modification dates.`,
		Icon:     "Search",
		Source:   ToolSourceComposio,
		Category: "integration",
		Keywords: []string{"google", "drive", "search", "find", "files", "composio"},
		Parameters: map[string]interface{}{
			"type": "object",
			"properties": map[string]interface{}{
				"credential_id": map[string]interface{}{
					"type":        "string",
					"description": "INTERNAL: Auto-injected by system. Do not set manually.",
				},
				"query": map[string]interface{}{
					"type":        "string",
					"description": "Search query text",
				},
				"file_type": map[string]interface{}{
					"type":        "string",
					"description": "Filter by file type: 'document', 'spreadsheet', 'presentation', 'pdf', 'image', 'folder'",
				},
				"max_results": map[string]interface{}{
					"type":        "integer",
					"description": "Maximum results to return (default: 10)",
				},
			},
			"required": []string{"query"},
		},
		Execute: executeGoogleDriveSearchFiles,
	}
}

func executeGoogleDriveSearchFiles(args map[string]interface{}) (string, error) {
	if err := checkDriveRateLimit(args); err != nil {
		return "", err
	}

	credData, err := GetCredentialData(args, "composio_googledrive")
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

	if query, ok := args["query"].(string); ok {
		input["query"] = query
	}
	if fileType, ok := args["file_type"].(string); ok {
		input["file_type"] = fileType
	}
	if maxResults, ok := args["max_results"].(float64); ok {
		input["max_results"] = int(maxResults)
	}

	return callComposioDriveAPI(composioAPIKey, entityID, "GOOGLEDRIVE_FIND_FILE", input)
}

// NewGoogleDriveDeleteFileTool creates a tool for deleting files
func NewGoogleDriveDeleteFileTool() *Tool {
	return &Tool{
		Name:        "googledrive_delete_file",
		DisplayName: "Google Drive - Delete File",
		Description: `Delete a file or folder from Google Drive by moving it to the Trash.

WHEN TO USE THIS TOOL:
- User wants to delete a file or folder from Drive
- User says "remove this file" or "delete that folder"

PARAMETERS:
- file_id (REQUIRED): The Google Drive file or folder ID. Example: "1a2b3c4d5e6f7g8h9i0j"

RETURNS: Confirmation that the file was moved to Trash. Files can be recovered from Trash within 30 days.`,
		Icon:     "Trash",
		Source:   ToolSourceComposio,
		Category: "integration",
		Keywords: []string{"google", "drive", "delete", "remove", "composio"},
		Parameters: map[string]interface{}{
			"type": "object",
			"properties": map[string]interface{}{
				"credential_id": map[string]interface{}{
					"type":        "string",
					"description": "INTERNAL: Auto-injected by system. Do not set manually.",
				},
				"file_id": map[string]interface{}{
					"type":        "string",
					"description": "ID of the file or folder to delete",
				},
			},
			"required": []string{"file_id"},
		},
		Execute: executeGoogleDriveDeleteFile,
	}
}

func executeGoogleDriveDeleteFile(args map[string]interface{}) (string, error) {
	if err := checkDriveRateLimit(args); err != nil {
		return "", err
	}

	credData, err := GetCredentialData(args, "composio_googledrive")
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

	if fileID, ok := args["file_id"].(string); ok {
		input["file_id"] = fileID
	}

	return callComposioDriveAPI(composioAPIKey, entityID, "GOOGLEDRIVE_GOOGLE_DRIVE_DELETE_FOLDER_OR_FILE_ACTION", input)
}

// NewGoogleDriveCopyFileTool creates a tool for copying files
func NewGoogleDriveCopyFileTool() *Tool {
	return &Tool{
		Name:        "googledrive_copy_file",
		DisplayName: "Google Drive - Copy File",
		Description: `Create a copy of an existing file in Google Drive, optionally in a different folder with a new name.

WHEN TO USE THIS TOOL:
- User wants to duplicate a file
- User says "copy this file" or "make a copy of that document"

PARAMETERS:
- file_id (REQUIRED): ID of the file to copy. Example: "1a2b3c4d5e6f7g8h9i0j"
- name (optional): Name for the copy. Default: "Copy of [original name]"
- parent_id (optional): Destination folder ID. Default: same folder as original.

RETURNS: The new copy's file ID, name, and URL.`,
		Icon:     "Copy",
		Source:   ToolSourceComposio,
		Category: "integration",
		Keywords: []string{"google", "drive", "copy", "duplicate", "composio"},
		Parameters: map[string]interface{}{
			"type": "object",
			"properties": map[string]interface{}{
				"credential_id": map[string]interface{}{
					"type":        "string",
					"description": "INTERNAL: Auto-injected by system. Do not set manually.",
				},
				"file_id": map[string]interface{}{
					"type":        "string",
					"description": "ID of the file to copy",
				},
				"name": map[string]interface{}{
					"type":        "string",
					"description": "Name for the copy (optional)",
				},
				"parent_id": map[string]interface{}{
					"type":        "string",
					"description": "Destination folder ID (optional)",
				},
			},
			"required": []string{"file_id"},
		},
		Execute: executeGoogleDriveCopyFile,
	}
}

func executeGoogleDriveCopyFile(args map[string]interface{}) (string, error) {
	if err := checkDriveRateLimit(args); err != nil {
		return "", err
	}

	credData, err := GetCredentialData(args, "composio_googledrive")
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

	if fileID, ok := args["file_id"].(string); ok {
		input["file_id"] = fileID
	}
	if name, ok := args["name"].(string); ok {
		input["name"] = name
	}
	if parentID, ok := args["parent_id"].(string); ok {
		input["parent_id"] = parentID
	}

	return callComposioDriveAPI(composioAPIKey, entityID, "GOOGLEDRIVE_COPY_FILE", input)
}

// NewGoogleDriveMoveFileTool creates a tool for moving files
func NewGoogleDriveMoveFileTool() *Tool {
	return &Tool{
		Name:        "googledrive_move_file",
		DisplayName: "Google Drive - Move File",
		Description: `Move a file from its current folder to a different folder in Google Drive.

WHEN TO USE THIS TOOL:
- User wants to reorganize files in Drive
- User says "move this file to folder X" or "put this document in the reports folder"

PARAMETERS:
- file_id (REQUIRED): ID of the file to move. Example: "1a2b3c4d5e6f7g8h9i0j"
- new_parent_id (REQUIRED): ID of the destination folder. Example: "0B1234567890abcdef"

RETURNS: Confirmation that the file was moved to the new location.`,
		Icon:     "Move",
		Source:   ToolSourceComposio,
		Category: "integration",
		Keywords: []string{"google", "drive", "move", "organize", "composio"},
		Parameters: map[string]interface{}{
			"type": "object",
			"properties": map[string]interface{}{
				"credential_id": map[string]interface{}{
					"type":        "string",
					"description": "INTERNAL: Auto-injected by system. Do not set manually.",
				},
				"file_id": map[string]interface{}{
					"type":        "string",
					"description": "ID of the file to move",
				},
				"new_parent_id": map[string]interface{}{
					"type":        "string",
					"description": "ID of the destination folder",
				},
			},
			"required": []string{"file_id", "new_parent_id"},
		},
		Execute: executeGoogleDriveMoveFile,
	}
}

func executeGoogleDriveMoveFile(args map[string]interface{}) (string, error) {
	if err := checkDriveRateLimit(args); err != nil {
		return "", err
	}

	credData, err := GetCredentialData(args, "composio_googledrive")
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

	if fileID, ok := args["file_id"].(string); ok {
		input["file_id"] = fileID
	}
	if newParentID, ok := args["new_parent_id"].(string); ok {
		input["new_parent_id"] = newParentID
	}

	return callComposioDriveAPI(composioAPIKey, entityID, "GOOGLEDRIVE_MOVE_FILE", input)
}

// NewGoogleDriveDownloadFileTool creates a tool for downloading file content
func NewGoogleDriveDownloadFileTool() *Tool {
	return &Tool{
		Name:        "googledrive_download_file",
		DisplayName: "Google Drive - Download File",
		Description: `Download or export a file from Google Drive. Google Docs/Sheets/Slides are exported to the specified format; other files return a download link.

WHEN TO USE THIS TOOL:
- User wants to download a file from Drive
- User says "export this doc as PDF" or "download that spreadsheet"

PARAMETERS:
- file_id (REQUIRED): ID of the file to download. Example: "1a2b3c4d5e6f7g8h9i0j"
- export_format (optional): Export format for Google Docs - "pdf", "docx", "txt", "html". Not needed for non-Google files.

RETURNS: File content or download URL depending on file type.`,
		Icon:     "Download",
		Source:   ToolSourceComposio,
		Category: "integration",
		Keywords: []string{"google", "drive", "download", "export", "composio"},
		Parameters: map[string]interface{}{
			"type": "object",
			"properties": map[string]interface{}{
				"credential_id": map[string]interface{}{
					"type":        "string",
					"description": "INTERNAL: Auto-injected by system. Do not set manually.",
				},
				"file_id": map[string]interface{}{
					"type":        "string",
					"description": "ID of the file to download",
				},
				"export_format": map[string]interface{}{
					"type":        "string",
					"description": "Export format for Google Docs: 'pdf', 'docx', 'txt', 'html'",
				},
			},
			"required": []string{"file_id"},
		},
		Execute: executeGoogleDriveDownloadFile,
	}
}

func executeGoogleDriveDownloadFile(args map[string]interface{}) (string, error) {
	if err := checkDriveRateLimit(args); err != nil {
		return "", err
	}

	credData, err := GetCredentialData(args, "composio_googledrive")
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

	if fileID, ok := args["file_id"].(string); ok {
		input["file_id"] = fileID
	}
	if exportFormat, ok := args["export_format"].(string); ok {
		input["export_format"] = exportFormat
	}

	return callComposioDriveAPI(composioAPIKey, entityID, "GOOGLEDRIVE_DOWNLOAD_FILE", input)
}

// callComposioDriveAPI makes a v2 API call to Composio for Google Drive actions
func callComposioDriveAPI(apiKey string, entityID string, action string, input map[string]interface{}) (string, error) {
	connectedAccountID, err := getDriveConnectedAccountID(apiKey, entityID, "googledrive")
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

	log.Printf("🔍 [GOOGLEDRIVE] Action: %s, ConnectedAccount: %s", action, maskSensitiveID(connectedAccountID))

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
		log.Printf("❌ [GOOGLEDRIVE] API error (status %d) for action %s: %s", resp.StatusCode, action, string(respBody))
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

// getDriveConnectedAccountID retrieves the connected account ID from Composio v3 API
func getDriveConnectedAccountID(apiKey string, userID string, appName string) (string, error) {
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

	return "", fmt.Errorf("no %s connection found for user. Please connect your Google Drive account first", appName)
}
