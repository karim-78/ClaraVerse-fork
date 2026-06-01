package tools

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"
)

// maskSensitiveID masks a sensitive ID for safe logging (e.g., "acc_abc123xyz" -> "acc_...xyz")
func maskSensitiveID(id string) string {
	if len(id) <= 8 {
		return "***"
	}
	return id[:4] + "..." + id[len(id)-4:]
}

// composioRateLimiter implements per-user rate limiting for Composio API calls
type composioRateLimiter struct {
	requests map[string][]time.Time // userID -> timestamps
	mutex    sync.RWMutex
	maxCalls int           // max calls per window
	window   time.Duration // time window
}

var globalComposioRateLimiter = &composioRateLimiter{
	requests: make(map[string][]time.Time),
	maxCalls: 50,                 // 50 calls per minute per user
	window:   1 * time.Minute,
}

// checkRateLimit checks if user has exceeded rate limit
func (rl *composioRateLimiter) checkRateLimit(userID string) error {
	rl.mutex.Lock()
	defer rl.mutex.Unlock()

	now := time.Now()
	windowStart := now.Add(-rl.window)

	// Get user's request history
	timestamps := rl.requests[userID]

	// Remove timestamps outside window
	validTimestamps := []time.Time{}
	for _, ts := range timestamps {
		if ts.After(windowStart) {
			validTimestamps = append(validTimestamps, ts)
		}
	}

	// Check if limit exceeded
	if len(validTimestamps) >= rl.maxCalls {
		return fmt.Errorf("rate limit exceeded: max %d requests per minute", rl.maxCalls)
	}

	// Add current timestamp
	validTimestamps = append(validTimestamps, now)
	rl.requests[userID] = validTimestamps

	return nil
}

// checkComposioRateLimit checks rate limit using user ID from args
func checkComposioRateLimit(args map[string]interface{}) error {
	// Extract user ID from args (injected by chat service)
	userID, ok := args["__user_id__"].(string)
	if !ok || userID == "" {
		// If no user ID, allow but log warning
		log.Printf("⚠️ [COMPOSIO] No user ID for rate limiting")
		return nil
	}

	return globalComposioRateLimiter.checkRateLimit(userID)
}

// NewComposioGoogleSheetsReadTool creates a tool for reading Google Sheets via Composio
func NewComposioGoogleSheetsReadTool() *Tool {
	return &Tool{
		Name:        "googlesheets_read",
		DisplayName: "Google Sheets - Read Range",
		Description: `Read data from a specific range in a Google Sheets spreadsheet. Returns cell values as a 2D array.

WHEN TO USE THIS TOOL:
- User wants to read data from a spreadsheet
- User says "get the data from my spreadsheet" or "read cells A1 to D10"
- Need to fetch spreadsheet data for analysis or processing

PARAMETERS:
- spreadsheet_id (REQUIRED): The spreadsheet ID from the Google Sheets URL. Example: "1BxiMVs0XRA5nFMdKvBdBZjgmUUqptlbs74OgVE2upms"
- range (REQUIRED): Cell range to read in A1 notation. Example: "Sheet1!A1:D10" or "Sheet1!A:D"

RETURNS: 2D array of cell values from the specified range.`,
		Icon:     "FileSpreadsheet",
		Source:   ToolSourceComposio,
		Category: "integration",
		Keywords: []string{"google", "sheets", "spreadsheet", "read", "data", "excel", "composio"},
		Parameters: map[string]interface{}{
			"type": "object",
			"properties": map[string]interface{}{
				"credential_id": map[string]interface{}{
					"type":        "string",
					"description": "INTERNAL: Auto-injected by system. Do not set manually.",
				},
				"spreadsheet_id": map[string]interface{}{
					"type":        "string",
					"description": "Google Sheets spreadsheet ID (from the URL)",
				},
				"range": map[string]interface{}{
					"type":        "string",
					"description": "Range to read (e.g., 'Sheet1!A1:D10' or 'Sheet1!A:D')",
				},
			},
			"required": []string{"spreadsheet_id", "range"},
		},
		Execute: executeComposioGoogleSheetsRead,
	}
}

// NewComposioGoogleSheetsWriteTool creates a tool for writing to Google Sheets via Composio
func NewComposioGoogleSheetsWriteTool() *Tool {
	return &Tool{
		Name:        "googlesheets_write",
		DisplayName: "Google Sheets - Write Range",
		Description: `Write data to a specific range in a Google Sheets spreadsheet. Overwrites existing cell values in the range.

WHEN TO USE THIS TOOL:
- User wants to write or update data in a spreadsheet
- User says "put this data in the spreadsheet" or "update cells A1 to D5"
- Need to overwrite existing data with new values

PARAMETERS:
- spreadsheet_id (REQUIRED): The spreadsheet ID from the URL. Example: "1BxiMVs0XRA5nFMdKvBdBZjgmUUqptlbs74OgVE2upms"
- range (REQUIRED): Cell range to write to, must include sheet name. Example: "Sheet1!A1:D10"
- values (REQUIRED): 2D array of values to write. Example: [["Name", "Age"], ["Alice", 30], ["Bob", 25]]

RETURNS: Confirmation with number of cells updated. Note: Formulas are evaluated (USER_ENTERED mode).`,
		Icon:     "FileSpreadsheet",
		Source:   ToolSourceComposio,
		Category: "integration",
		Keywords: []string{"google", "sheets", "spreadsheet", "write", "update", "data", "excel", "composio"},
		Parameters: map[string]interface{}{
			"type": "object",
			"properties": map[string]interface{}{
				"credential_id": map[string]interface{}{
					"type":        "string",
					"description": "INTERNAL: Auto-injected by system. Do not set manually.",
				},
				"spreadsheet_id": map[string]interface{}{
					"type":        "string",
					"description": "Google Sheets spreadsheet ID (from the URL)",
				},
				"range": map[string]interface{}{
					"type":        "string",
					"description": "Sheet name and range to write (e.g., 'Sheet1!A1:D10'). Sheet name is required.",
				},
				"values": map[string]interface{}{
					"type":        "array",
					"description": "2D array of values to write [[row1], [row2], ...] or JSON string",
					"items": map[string]interface{}{
						"type": "array",
						"items": map[string]interface{}{}, // Allow any type (string, number, boolean, etc.)
					},
				},
			},
			"required": []string{"spreadsheet_id", "range", "values"},
		},
		Execute: executeComposioGoogleSheetsWrite,
	}
}

// NewComposioGoogleSheetsAppendTool creates a tool for appending to Google Sheets via Composio
func NewComposioGoogleSheetsAppendTool() *Tool {
	return &Tool{
		Name:        "googlesheets_append",
		DisplayName: "Google Sheets - Append Rows",
		Description: `Append new rows to the end of existing data in a Google Sheets spreadsheet without overwriting anything.

WHEN TO USE THIS TOOL:
- User wants to add new rows to a spreadsheet
- User says "add this data to the spreadsheet" or "append these rows"
- Need to add entries to a log, tracker, or growing dataset

PARAMETERS:
- spreadsheet_id (REQUIRED): The spreadsheet ID from the URL. Example: "1BxiMVs0XRA5nFMdKvBdBZjgmUUqptlbs74OgVE2upms"
- range (REQUIRED): Sheet and column range to append to. Example: "Sheet1!A:D" or "Sheet1"
- values (REQUIRED): 2D array of rows to append. Example: [["Alice", "alice@example.com", "2024-01-15"]]

RETURNS: Confirmation with the range where data was appended. Formulas are evaluated (USER_ENTERED mode).`,
		Icon:     "FileSpreadsheet",
		Source:   ToolSourceComposio,
		Category: "integration",
		Keywords: []string{"google", "sheets", "spreadsheet", "append", "add", "insert", "data", "excel", "composio"},
		Parameters: map[string]interface{}{
			"type": "object",
			"properties": map[string]interface{}{
				"credential_id": map[string]interface{}{
					"type":        "string",
					"description": "INTERNAL: Auto-injected by system. Do not set manually.",
				},
				"spreadsheet_id": map[string]interface{}{
					"type":        "string",
					"description": "Google Sheets spreadsheet ID (from the URL)",
				},
				"range": map[string]interface{}{
					"type":        "string",
					"description": "Sheet name and column range to append to (e.g., 'Sheet1!A:D' or 'Sheet1')",
				},
				"values": map[string]interface{}{
					"type":        "array",
					"description": "2D array of values to append [[row1], [row2], ...] or JSON string",
					"items": map[string]interface{}{
						"type": "array",
						"items": map[string]interface{}{}, // Allow any type (string, number, boolean, etc.)
					},
				},
			},
			"required": []string{"spreadsheet_id", "range", "values"},
		},
		Execute: executeComposioGoogleSheetsAppend,
	}
}

func executeComposioGoogleSheetsRead(args map[string]interface{}) (string, error) {
	// ✅ RATE LIMITING - Check per-user rate limit
	if err := checkComposioRateLimit(args); err != nil {
		return "", err
	}

	// Get Composio credentials
	credData, err := GetCredentialData(args, "composio_googlesheets")
	if err != nil {
		return "", fmt.Errorf("failed to get Composio credentials: %w", err)
	}

	entityID, ok := credData["composio_entity_id"].(string)
	if !ok || entityID == "" {
		return "", fmt.Errorf("composio_entity_id not found in credentials")
	}

	// Extract parameters
	spreadsheetID, _ := args["spreadsheet_id"].(string)
	rangeSpec, _ := args["range"].(string)

	if spreadsheetID == "" {
		return "", fmt.Errorf("'spreadsheet_id' is required")
	}
	if rangeSpec == "" {
		return "", fmt.Errorf("'range' is required")
	}

	// Call Composio API
	composioAPIKey := GetComposioAPIKey()
	if composioAPIKey == "" {
		return "", fmt.Errorf("COMPOSIO_API_KEY environment variable not set")
	}

	// Use exact parameter names from Composio docs
	payload := map[string]interface{}{
		"entityId": entityID,
		"appName":  "googlesheets",
		"input": map[string]interface{}{
			"spreadsheet_id": spreadsheetID,
			"ranges":         []string{rangeSpec},
		},
	}

	return callComposioAPI(composioAPIKey, "GOOGLESHEETS_BATCH_GET", payload)
}

func executeComposioGoogleSheetsWrite(args map[string]interface{}) (string, error) {
	// ✅ RATE LIMITING
	if err := checkComposioRateLimit(args); err != nil {
		return "", err
	}

	// Get Composio credentials
	credData, err := GetCredentialData(args, "composio_googlesheets")
	if err != nil {
		return "", fmt.Errorf("failed to get Composio credentials: %w", err)
	}

	entityID, ok := credData["composio_entity_id"].(string)
	if !ok || entityID == "" {
		return "", fmt.Errorf("composio_entity_id not found in credentials")
	}

	// Extract parameters
	spreadsheetID, _ := args["spreadsheet_id"].(string)
	rangeSpec, _ := args["range"].(string)
	values := args["values"]

	if spreadsheetID == "" {
		return "", fmt.Errorf("'spreadsheet_id' is required")
	}
	if rangeSpec == "" {
		return "", fmt.Errorf("'range' is required")
	}
	if values == nil {
		return "", fmt.Errorf("'values' is required")
	}

	// Parse values into a 2D array (handles JSON strings, plain strings, and arrays)
	valuesArray := parseSheetValues(values)
	if valuesArray == nil {
		return "", fmt.Errorf("values must be array, JSON string, or plain string")
	}

	// Extract sheet name from range (e.g., "Sheet1!A1:D10" -> "Sheet1")
	sheetName := "Sheet1"
	for i := 0; i < len(rangeSpec); i++ {
		if rangeSpec[i] == '!' {
			sheetName = rangeSpec[:i]
			break
		}
	}

	// Call Composio API
	composioAPIKey := GetComposioAPIKey()
	if composioAPIKey == "" {
		return "", fmt.Errorf("COMPOSIO_API_KEY environment variable not set")
	}

	// Use exact parameter names from Composio docs for GOOGLESHEETS_BATCH_UPDATE
	payload := map[string]interface{}{
		"entityId": entityID,
		"appName":  "googlesheets",
		"input": map[string]interface{}{
			"spreadsheet_id":   spreadsheetID,
			"sheet_name":       sheetName,
			"values":           valuesArray,
			"valueInputOption": "USER_ENTERED", // Default value from docs
		},
	}

	return callComposioAPI(composioAPIKey, "GOOGLESHEETS_BATCH_UPDATE", payload)
}

func executeComposioGoogleSheetsAppend(args map[string]interface{}) (string, error) {
	// ✅ RATE LIMITING
	if err := checkComposioRateLimit(args); err != nil {
		return "", err
	}

	// Get Composio credentials
	credData, err := GetCredentialData(args, "composio_googlesheets")
	if err != nil {
		return "", fmt.Errorf("failed to get Composio credentials: %w", err)
	}

	entityID, ok := credData["composio_entity_id"].(string)
	if !ok || entityID == "" {
		return "", fmt.Errorf("composio_entity_id not found in credentials")
	}

	// Extract parameters
	spreadsheetID, _ := args["spreadsheet_id"].(string)
	rangeSpec, _ := args["range"].(string)
	values := args["values"]

	if spreadsheetID == "" {
		return "", fmt.Errorf("'spreadsheet_id' is required")
	}
	if rangeSpec == "" {
		return "", fmt.Errorf("'range' is required")
	}
	if values == nil {
		return "", fmt.Errorf("'values' is required")
	}

	// Parse values into a 2D array (handles JSON strings, plain strings, and arrays)
	valuesArray := parseSheetValues(values)
	if valuesArray == nil {
		return "", fmt.Errorf("values must be array, JSON string, or plain string")
	}

	// Call Composio API
	composioAPIKey := GetComposioAPIKey()
	if composioAPIKey == "" {
		return "", fmt.Errorf("COMPOSIO_API_KEY environment variable not set")
	}

	// Use exact parameter names from Composio docs for GOOGLESHEETS_SPREADSHEETS_VALUES_APPEND
	payload := map[string]interface{}{
		"entityId": entityID,
		"appName":  "googlesheets",
		"input": map[string]interface{}{
			"spreadsheetId":    spreadsheetID,
			"range":            rangeSpec,
			"valueInputOption": "USER_ENTERED", // Required by docs
			"values":           valuesArray,
		},
	}

	return callComposioAPI(composioAPIKey, "GOOGLESHEETS_SPREADSHEETS_VALUES_APPEND", payload)
}

// parseSheetValues converts a values argument into a 2D array for Google Sheets.
// Accepts: [][]interface{} (pass-through), []interface{} (rows), JSON string,
// bracket-delimited string like "[[a,b],[c,d]]" (from template interpolation), or plain string.
func parseSheetValues(values interface{}) [][]interface{} {
	switch v := values.(type) {
	case string:
		// 1. Try valid JSON 2D array
		var arr [][]interface{}
		if err := json.Unmarshal([]byte(v), &arr); err == nil {
			return arr
		}
		// 2. Try valid JSON 1D array
		var flat []interface{}
		if err := json.Unmarshal([]byte(v), &flat); err == nil {
			return [][]interface{}{flat}
		}
		// 3. Heuristic: detect [[val1,val2],[val3,val4]] pattern from template interpolation
		//    (values aren't quoted so JSON parse fails, but the structure is clear)
		if parsed := parseUnquotedSheetArray(v); parsed != nil {
			return parsed
		}
		// 4. Plain string — wrap as single cell
		return [][]interface{}{{v}}
	case [][]interface{}:
		return v
	case []interface{}:
		var result [][]interface{}
		for _, row := range v {
			if rowArr, ok := row.([]interface{}); ok {
				result = append(result, rowArr)
			} else {
				// Single value row
				result = append(result, []interface{}{row})
			}
		}
		return result
	default:
		return nil
	}
}

// parseUnquotedSheetArray handles strings like "[[url1,id1],[url2,id2]]" where values
// aren't JSON-quoted (common after template interpolation resolves {{...}} placeholders).
// Returns nil if the string doesn't match the [[...]] pattern.
func parseUnquotedSheetArray(s string) [][]interface{} {
	s = strings.TrimSpace(s)
	if !strings.HasPrefix(s, "[[") || !strings.HasSuffix(s, "]]") {
		return nil
	}
	// Strip outer [[ and ]]
	inner := s[2 : len(s)-2]
	if inner == "" {
		return nil
	}
	// Split rows by "],[" — this is safe because "],["  is very unlikely in real cell values
	rowStrs := strings.Split(inner, "],[")
	var result [][]interface{}
	for _, rowStr := range rowStrs {
		rowStr = strings.TrimSpace(rowStr)
		if rowStr == "" {
			continue
		}
		// Try to parse as a JSON array by wrapping values in quotes
		// This handles values that contain commas if they're properly quoted
		var jsonRow []interface{}
		if err := json.Unmarshal([]byte("["+rowStr+"]"), &jsonRow); err == nil {
			result = append(result, jsonRow)
			continue
		}
		// Fallback: naive comma split (only safe for values without commas)
		cells := strings.Split(rowStr, ",")
		row := make([]interface{}, len(cells))
		for i, cell := range cells {
			row[i] = strings.TrimSpace(cell)
		}
		result = append(result, row)
	}
	if len(result) == 0 {
		return nil
	}
	return result
}

// NewComposioGoogleSheetsCreateTool creates a tool for creating new Google Sheets via Composio
func NewComposioGoogleSheetsCreateTool() *Tool {
	return &Tool{
		Name:        "googlesheets_create",
		DisplayName: "Google Sheets - Create Spreadsheet",
		Description: `Create a brand new Google Sheets spreadsheet in the user's Google Drive.

WHEN TO USE THIS TOOL:
- User wants to create a new spreadsheet
- User says "make a new spreadsheet" or "create a Google Sheet"

PARAMETERS:
- title (optional): Spreadsheet name. Example: "Q1 Sales Report". Default: "Untitled spreadsheet"

RETURNS: The new spreadsheet's ID, URL, and title.`,
		Icon:     "FileSpreadsheet",
		Source:   ToolSourceComposio,
		Category: "integration",
		Keywords: []string{"google", "sheets", "spreadsheet", "create", "new", "composio"},
		Parameters: map[string]interface{}{
			"type": "object",
			"properties": map[string]interface{}{
				"credential_id": map[string]interface{}{
					"type":        "string",
					"description": "INTERNAL: Auto-injected by system. Do not set manually.",
				},
				"title": map[string]interface{}{
					"type":        "string",
					"description": "Title for the new spreadsheet (optional, defaults to 'Untitled spreadsheet')",
				},
			},
			"required": []string{},
		},
		Execute: executeComposioGoogleSheetsCreate,
	}
}

func executeComposioGoogleSheetsCreate(args map[string]interface{}) (string, error) {
	// ✅ RATE LIMITING
	if err := checkComposioRateLimit(args); err != nil {
		return "", err
	}

	// Get Composio credentials
	credData, err := GetCredentialData(args, "composio_googlesheets")
	if err != nil {
		return "", fmt.Errorf("failed to get Composio credentials: %w", err)
	}

	entityID, ok := credData["composio_entity_id"].(string)
	if !ok || entityID == "" {
		return "", fmt.Errorf("composio_entity_id not found in credentials")
	}

	// Extract optional title parameter
	title, _ := args["title"].(string)

	// Call Composio API
	composioAPIKey := GetComposioAPIKey()
	if composioAPIKey == "" {
		return "", fmt.Errorf("COMPOSIO_API_KEY environment variable not set")
	}

	// Build payload based on whether title is provided
	input := map[string]interface{}{}
	if title != "" {
		input["title"] = title
	}

	payload := map[string]interface{}{
		"entityId": entityID,
		"appName":  "googlesheets",
		"input":    input,
	}

	return callComposioAPI(composioAPIKey, "GOOGLESHEETS_CREATE_GOOGLE_SHEET1", payload)
}

// NewComposioGoogleSheetsInfoTool creates a tool for getting spreadsheet metadata via Composio
func NewComposioGoogleSheetsInfoTool() *Tool {
	return &Tool{
		Name:        "googlesheets_get_info",
		DisplayName: "Google Sheets - Get Spreadsheet Info",
		Description: `Get metadata about a Google Sheets spreadsheet including title, locale, timezone, and all sheet/tab properties.

WHEN TO USE THIS TOOL:
- User wants to know the structure of a spreadsheet
- Need to find sheet IDs, dimensions, or tab colors
- Want to check if a spreadsheet exists and see its properties

PARAMETERS:
- spreadsheet_id (REQUIRED): The spreadsheet ID from the URL. Example: "1BxiMVs0XRA5nFMdKvBdBZjgmUUqptlbs74OgVE2upms"

RETURNS: Spreadsheet title, locale, timezone, and list of all sheets with their IDs, names, row/column counts, and tab colors.`,
		Icon:     "FileSpreadsheet",
		Source:   ToolSourceComposio,
		Category: "integration",
		Keywords: []string{"google", "sheets", "spreadsheet", "info", "metadata", "sheets list", "composio"},
		Parameters: map[string]interface{}{
			"type": "object",
			"properties": map[string]interface{}{
				"credential_id": map[string]interface{}{
					"type":        "string",
					"description": "INTERNAL: Auto-injected by system. Do not set manually.",
				},
				"spreadsheet_id": map[string]interface{}{
					"type":        "string",
					"description": "Google Sheets spreadsheet ID (from the URL)",
				},
			},
			"required": []string{"spreadsheet_id"},
		},
		Execute: executeComposioGoogleSheetsInfo,
	}
}

func executeComposioGoogleSheetsInfo(args map[string]interface{}) (string, error) {
	// ✅ RATE LIMITING
	if err := checkComposioRateLimit(args); err != nil {
		return "", err
	}

	credData, err := GetCredentialData(args, "composio_googlesheets")
	if err != nil {
		return "", fmt.Errorf("failed to get Composio credentials: %w", err)
	}

	entityID, ok := credData["composio_entity_id"].(string)
	if !ok || entityID == "" {
		return "", fmt.Errorf("composio_entity_id not found in credentials")
	}

	spreadsheetID, _ := args["spreadsheet_id"].(string)
	if spreadsheetID == "" {
		return "", fmt.Errorf("'spreadsheet_id' is required")
	}

	composioAPIKey := GetComposioAPIKey()
	if composioAPIKey == "" {
		return "", fmt.Errorf("COMPOSIO_API_KEY environment variable not set")
	}

	payload := map[string]interface{}{
		"entityId": entityID,
		"appName":  "googlesheets",
		"input": map[string]interface{}{
			"spreadsheet_id": spreadsheetID,
		},
	}

	return callComposioAPI(composioAPIKey, "GOOGLESHEETS_GET_SPREADSHEET_INFO", payload)
}

// NewComposioGoogleSheetsListSheetsTool creates a tool for listing sheet names via Composio
func NewComposioGoogleSheetsListSheetsTool() *Tool {
	return &Tool{
		Name:        "googlesheets_list_sheets",
		DisplayName: "Google Sheets - List Sheet Names",
		Description: `Get a simple list of all sheet/tab names in a Google Spreadsheet. Fast and lightweight - no cell data is returned.

WHEN TO USE THIS TOOL:
- User wants to know what sheets/tabs exist in a spreadsheet
- Need to verify a sheet name before reading or writing to it
- User asks "what tabs does this spreadsheet have"

PARAMETERS:
- spreadsheet_id (REQUIRED): The spreadsheet ID from the URL. Example: "1BxiMVs0XRA5nFMdKvBdBZjgmUUqptlbs74OgVE2upms"

RETURNS: Array of sheet/tab names in order. Example: ["Sheet1", "Data", "Summary"]`,
		Icon:     "FileSpreadsheet",
		Source:   ToolSourceComposio,
		Category: "integration",
		Keywords: []string{"google", "sheets", "spreadsheet", "list", "tabs", "worksheets", "composio"},
		Parameters: map[string]interface{}{
			"type": "object",
			"properties": map[string]interface{}{
				"credential_id": map[string]interface{}{
					"type":        "string",
					"description": "INTERNAL: Auto-injected by system. Do not set manually.",
				},
				"spreadsheet_id": map[string]interface{}{
					"type":        "string",
					"description": "Google Sheets spreadsheet ID (from the URL)",
				},
			},
			"required": []string{"spreadsheet_id"},
		},
		Execute: executeComposioGoogleSheetsListSheets,
	}
}

func executeComposioGoogleSheetsListSheets(args map[string]interface{}) (string, error) {
	// ✅ RATE LIMITING
	if err := checkComposioRateLimit(args); err != nil {
		return "", err
	}

	credData, err := GetCredentialData(args, "composio_googlesheets")
	if err != nil {
		return "", fmt.Errorf("failed to get Composio credentials: %w", err)
	}

	entityID, ok := credData["composio_entity_id"].(string)
	if !ok || entityID == "" {
		return "", fmt.Errorf("composio_entity_id not found in credentials")
	}

	spreadsheetID, _ := args["spreadsheet_id"].(string)
	if spreadsheetID == "" {
		return "", fmt.Errorf("'spreadsheet_id' is required")
	}

	composioAPIKey := GetComposioAPIKey()
	if composioAPIKey == "" {
		return "", fmt.Errorf("COMPOSIO_API_KEY environment variable not set")
	}

	payload := map[string]interface{}{
		"entityId": entityID,
		"appName":  "googlesheets",
		"input": map[string]interface{}{
			"spreadsheet_id": spreadsheetID,
		},
	}

	return callComposioAPI(composioAPIKey, "GOOGLESHEETS_GET_SHEET_NAMES", payload)
}

// NewComposioGoogleSheetsSearchTool creates a tool for searching spreadsheets via Composio
func NewComposioGoogleSheetsSearchTool() *Tool {
	return &Tool{
		Name:        "googlesheets_search",
		DisplayName: "Google Sheets - Search Spreadsheets",
		Description: `Search for Google Spreadsheets by name or content when you don't have the spreadsheet ID.

WHEN TO USE THIS TOOL:
- User wants to find a spreadsheet by name
- User says "find my budget spreadsheet" or "search for sales data"
- Need to discover the spreadsheet ID before reading/writing

PARAMETERS:
- query (optional): Search text to match against spreadsheet names and content. Example: "Q1 Budget"
- max_results (optional): Max results to return. Default: 10.

RETURNS: List of matching spreadsheets with their IDs, titles, and metadata.`,
		Icon:     "FileSpreadsheet",
		Source:   ToolSourceComposio,
		Category: "integration",
		Keywords: []string{"google", "sheets", "spreadsheet", "search", "find", "discover", "composio"},
		Parameters: map[string]interface{}{
			"type": "object",
			"properties": map[string]interface{}{
				"credential_id": map[string]interface{}{
					"type":        "string",
					"description": "INTERNAL: Auto-injected by system. Do not set manually.",
				},
				"query": map[string]interface{}{
					"type":        "string",
					"description": "Search query (searches in name and content)",
				},
				"max_results": map[string]interface{}{
					"type":        "integer",
					"description": "Maximum number of results to return (default: 10)",
				},
			},
			"required": []string{},
		},
		Execute: executeComposioGoogleSheetsSearch,
	}
}

func executeComposioGoogleSheetsSearch(args map[string]interface{}) (string, error) {
	// ✅ RATE LIMITING
	if err := checkComposioRateLimit(args); err != nil {
		return "", err
	}

	credData, err := GetCredentialData(args, "composio_googlesheets")
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

	// Build input parameters
	input := map[string]interface{}{}

	if query, ok := args["query"].(string); ok && query != "" {
		input["query"] = query
	}

	if maxResults, ok := args["max_results"].(float64); ok {
		input["max_results"] = int(maxResults)
	}

	payload := map[string]interface{}{
		"entityId": entityID,
		"appName":  "googlesheets",
		"input":    input,
	}

	return callComposioAPI(composioAPIKey, "GOOGLESHEETS_SEARCH_SPREADSHEETS", payload)
}

// NewComposioGoogleSheetsClearTool creates a tool for clearing cell values via Composio
func NewComposioGoogleSheetsClearTool() *Tool {
	return &Tool{
		Name:        "googlesheets_clear",
		DisplayName: "Google Sheets - Clear Values",
		Description: `Clear cell values from a range in Google Sheets. Removes data and formulas but preserves cell formatting and notes.

WHEN TO USE THIS TOOL:
- User wants to clear data from cells without deleting the cells
- User says "clear this range" or "erase the data in column A"
- Need to reset a range while keeping formatting intact

PARAMETERS:
- spreadsheet_id (REQUIRED): The spreadsheet ID. Example: "1BxiMVs0XRA5nFMdKvBdBZjgmUUqptlbs74OgVE2upms"
- range (REQUIRED): Range to clear in A1 notation. Example: "Sheet1!A1:D10" or "Sheet1!A:D"

RETURNS: Confirmation with the range that was cleared.`,
		Icon:     "FileSpreadsheet",
		Source:   ToolSourceComposio,
		Category: "integration",
		Keywords: []string{"google", "sheets", "spreadsheet", "clear", "delete", "erase", "composio"},
		Parameters: map[string]interface{}{
			"type": "object",
			"properties": map[string]interface{}{
				"credential_id": map[string]interface{}{
					"type":        "string",
					"description": "INTERNAL: Auto-injected by system. Do not set manually.",
				},
				"spreadsheet_id": map[string]interface{}{
					"type":        "string",
					"description": "Google Sheets spreadsheet ID (from the URL)",
				},
				"range": map[string]interface{}{
					"type":        "string",
					"description": "Range to clear (e.g., 'Sheet1!A1:D10' or 'Sheet1!A:D')",
				},
			},
			"required": []string{"spreadsheet_id", "range"},
		},
		Execute: executeComposioGoogleSheetsClear,
	}
}

func executeComposioGoogleSheetsClear(args map[string]interface{}) (string, error) {
	// ✅ RATE LIMITING
	if err := checkComposioRateLimit(args); err != nil {
		return "", err
	}

	credData, err := GetCredentialData(args, "composio_googlesheets")
	if err != nil {
		return "", fmt.Errorf("failed to get Composio credentials: %w", err)
	}

	entityID, ok := credData["composio_entity_id"].(string)
	if !ok || entityID == "" {
		return "", fmt.Errorf("composio_entity_id not found in credentials")
	}

	spreadsheetID, _ := args["spreadsheet_id"].(string)
	rangeSpec, _ := args["range"].(string)

	if spreadsheetID == "" {
		return "", fmt.Errorf("'spreadsheet_id' is required")
	}
	if rangeSpec == "" {
		return "", fmt.Errorf("'range' is required")
	}

	composioAPIKey := GetComposioAPIKey()
	if composioAPIKey == "" {
		return "", fmt.Errorf("COMPOSIO_API_KEY environment variable not set")
	}

	payload := map[string]interface{}{
		"entityId": entityID,
		"appName":  "googlesheets",
		"input": map[string]interface{}{
			"spreadsheet_id": spreadsheetID,
			"range":          rangeSpec,
		},
	}

	return callComposioAPI(composioAPIKey, "GOOGLESHEETS_CLEAR_VALUES", payload)
}

// NewComposioGoogleSheetsAddSheetTool creates a tool for adding new sheets via Composio
func NewComposioGoogleSheetsAddSheetTool() *Tool {
	return &Tool{
		Name:        "googlesheets_add_sheet",
		DisplayName: "Google Sheets - Add Sheet",
		Description: `Add a new worksheet/tab to an existing Google Spreadsheet.

WHEN TO USE THIS TOOL:
- User wants to add a new tab to their spreadsheet
- User says "add a sheet called Data" or "create a new tab"

PARAMETERS:
- spreadsheet_id (REQUIRED): The spreadsheet ID. Example: "1BxiMVs0XRA5nFMdKvBdBZjgmUUqptlbs74OgVE2upms"
- title (optional): Name for the new sheet. Default: auto-generated name like "Sheet2"

RETURNS: The new sheet's ID, title, and properties.`,
		Icon:     "FileSpreadsheet",
		Source:   ToolSourceComposio,
		Category: "integration",
		Keywords: []string{"google", "sheets", "spreadsheet", "add", "create", "tab", "worksheet", "composio"},
		Parameters: map[string]interface{}{
			"type": "object",
			"properties": map[string]interface{}{
				"credential_id": map[string]interface{}{
					"type":        "string",
					"description": "INTERNAL: Auto-injected by system. Do not set manually.",
				},
				"spreadsheet_id": map[string]interface{}{
					"type":        "string",
					"description": "Google Sheets spreadsheet ID (from the URL)",
				},
				"title": map[string]interface{}{
					"type":        "string",
					"description": "Title for the new sheet (default: 'Sheet{N}')",
				},
			},
			"required": []string{"spreadsheet_id"},
		},
		Execute: executeComposioGoogleSheetsAddSheet,
	}
}

func executeComposioGoogleSheetsAddSheet(args map[string]interface{}) (string, error) {
	// ✅ RATE LIMITING
	if err := checkComposioRateLimit(args); err != nil {
		return "", err
	}

	credData, err := GetCredentialData(args, "composio_googlesheets")
	if err != nil {
		return "", fmt.Errorf("failed to get Composio credentials: %w", err)
	}

	entityID, ok := credData["composio_entity_id"].(string)
	if !ok || entityID == "" {
		return "", fmt.Errorf("composio_entity_id not found in credentials")
	}

	spreadsheetID, _ := args["spreadsheet_id"].(string)
	if spreadsheetID == "" {
		return "", fmt.Errorf("'spreadsheet_id' is required")
	}

	composioAPIKey := GetComposioAPIKey()
	if composioAPIKey == "" {
		return "", fmt.Errorf("COMPOSIO_API_KEY environment variable not set")
	}

	// Build input with optional title
	input := map[string]interface{}{
		"spreadsheetId": spreadsheetID,
	}

	// Add optional properties
	properties := map[string]interface{}{}
	if title, ok := args["title"].(string); ok && title != "" {
		properties["title"] = title
	}

	if len(properties) > 0 {
		input["properties"] = properties
	}

	payload := map[string]interface{}{
		"entityId": entityID,
		"appName":  "googlesheets",
		"input":    input,
	}

	return callComposioAPI(composioAPIKey, "GOOGLESHEETS_ADD_SHEET", payload)
}

// NewComposioGoogleSheetsDeleteSheetTool creates a tool for deleting sheets via Composio
func NewComposioGoogleSheetsDeleteSheetTool() *Tool {
	return &Tool{
		Name:        "googlesheets_delete_sheet",
		DisplayName: "Google Sheets - Delete Sheet",
		Description: `Permanently delete a worksheet/tab from a Google Spreadsheet. Cannot be undone.

WHEN TO USE THIS TOOL:
- User wants to remove a tab from their spreadsheet
- User says "delete the old data sheet" or "remove that tab"

PARAMETERS:
- spreadsheet_id (REQUIRED): The spreadsheet ID. Example: "1BxiMVs0XRA5nFMdKvBdBZjgmUUqptlbs74OgVE2upms"
- sheet_id (REQUIRED): Numeric sheet ID (NOT the sheet name). Get this from 'googlesheets_get_info'. Example: 123456789

RETURNS: Confirmation of deletion. WARNING: This is permanent and cannot be undone. Cannot delete the last remaining sheet.`,
		Icon:     "FileSpreadsheet",
		Source:   ToolSourceComposio,
		Category: "integration",
		Keywords: []string{"google", "sheets", "spreadsheet", "delete", "remove", "tab", "worksheet", "composio"},
		Parameters: map[string]interface{}{
			"type": "object",
			"properties": map[string]interface{}{
				"credential_id": map[string]interface{}{
					"type":        "string",
					"description": "INTERNAL: Auto-injected by system. Do not set manually.",
				},
				"spreadsheet_id": map[string]interface{}{
					"type":        "string",
					"description": "Google Sheets spreadsheet ID (from the URL)",
				},
				"sheet_id": map[string]interface{}{
					"type":        "integer",
					"description": "Numeric ID of the sheet to delete (get from googlesheets_get_info)",
				},
			},
			"required": []string{"spreadsheet_id", "sheet_id"},
		},
		Execute: executeComposioGoogleSheetsDeleteSheet,
	}
}

func executeComposioGoogleSheetsDeleteSheet(args map[string]interface{}) (string, error) {
	// ✅ RATE LIMITING
	if err := checkComposioRateLimit(args); err != nil {
		return "", err
	}

	credData, err := GetCredentialData(args, "composio_googlesheets")
	if err != nil {
		return "", fmt.Errorf("failed to get Composio credentials: %w", err)
	}

	entityID, ok := credData["composio_entity_id"].(string)
	if !ok || entityID == "" {
		return "", fmt.Errorf("composio_entity_id not found in credentials")
	}

	spreadsheetID, _ := args["spreadsheet_id"].(string)
	if spreadsheetID == "" {
		return "", fmt.Errorf("'spreadsheet_id' is required")
	}

	// Handle both float64 and int types for sheet_id
	var sheetID int
	switch v := args["sheet_id"].(type) {
	case float64:
		sheetID = int(v)
	case int:
		sheetID = v
	default:
		return "", fmt.Errorf("'sheet_id' must be a number")
	}

	composioAPIKey := GetComposioAPIKey()
	if composioAPIKey == "" {
		return "", fmt.Errorf("COMPOSIO_API_KEY environment variable not set")
	}

	payload := map[string]interface{}{
		"entityId": entityID,
		"appName":  "googlesheets",
		"input": map[string]interface{}{
			"spreadsheetId": spreadsheetID,
			"sheet_id":      sheetID,
		},
	}

	return callComposioAPI(composioAPIKey, "GOOGLESHEETS_DELETE_SHEET", payload)
}

// NewComposioGoogleSheetsFindReplaceTool creates a tool for find and replace via Composio
func NewComposioGoogleSheetsFindReplaceTool() *Tool {
	return &Tool{
		Name:        "googlesheets_find_replace",
		DisplayName: "Google Sheets - Find and Replace",
		Description: `Find and replace text across an entire Google Spreadsheet or a specific sheet. Supports case-sensitive and regex matching.

WHEN TO USE THIS TOOL:
- User wants to find and replace values in a spreadsheet
- User says "replace all 2023 with 2024" or "find and fix spelling errors"
- Need to bulk update values across many cells

PARAMETERS:
- spreadsheet_id (REQUIRED): The spreadsheet ID. Example: "1BxiMVs0XRA5nFMdKvBdBZjgmUUqptlbs74OgVE2upms"
- find (REQUIRED): Text or pattern to find. Example: "2023"
- replace (REQUIRED): Replacement text. Example: "2024"
- sheet_id (optional): Numeric sheet ID to limit search to one sheet. Omit for all sheets.
- match_case (optional): Case-sensitive matching. Default: false.

RETURNS: Number of occurrences found and replaced.`,
		Icon:     "FileSpreadsheet",
		Source:   ToolSourceComposio,
		Category: "integration",
		Keywords: []string{"google", "sheets", "spreadsheet", "find", "replace", "search", "update", "composio"},
		Parameters: map[string]interface{}{
			"type": "object",
			"properties": map[string]interface{}{
				"credential_id": map[string]interface{}{
					"type":        "string",
					"description": "INTERNAL: Auto-injected by system. Do not set manually.",
				},
				"spreadsheet_id": map[string]interface{}{
					"type":        "string",
					"description": "Google Sheets spreadsheet ID (from the URL)",
				},
				"find": map[string]interface{}{
					"type":        "string",
					"description": "Text or pattern to find",
				},
				"replace": map[string]interface{}{
					"type":        "string",
					"description": "Text to replace with",
				},
				"sheet_id": map[string]interface{}{
					"type":        "integer",
					"description": "Optional: Numeric sheet ID to limit search (omit for all sheets)",
				},
				"match_case": map[string]interface{}{
					"type":        "boolean",
					"description": "Whether to match case (default: false)",
				},
			},
			"required": []string{"spreadsheet_id", "find", "replace"},
		},
		Execute: executeComposioGoogleSheetsFindReplace,
	}
}

func executeComposioGoogleSheetsFindReplace(args map[string]interface{}) (string, error) {
	// ✅ RATE LIMITING
	if err := checkComposioRateLimit(args); err != nil {
		return "", err
	}

	credData, err := GetCredentialData(args, "composio_googlesheets")
	if err != nil {
		return "", fmt.Errorf("failed to get Composio credentials: %w", err)
	}

	entityID, ok := credData["composio_entity_id"].(string)
	if !ok || entityID == "" {
		return "", fmt.Errorf("composio_entity_id not found in credentials")
	}

	spreadsheetID, _ := args["spreadsheet_id"].(string)
	find, _ := args["find"].(string)
	replace, _ := args["replace"].(string)

	if spreadsheetID == "" {
		return "", fmt.Errorf("'spreadsheet_id' is required")
	}
	if find == "" {
		return "", fmt.Errorf("'find' is required")
	}
	if replace == "" {
		return "", fmt.Errorf("'replace' is required")
	}

	composioAPIKey := GetComposioAPIKey()
	if composioAPIKey == "" {
		return "", fmt.Errorf("COMPOSIO_API_KEY environment variable not set")
	}

	// Build input
	input := map[string]interface{}{
		"spreadsheetId": spreadsheetID,
		"find":          find,
		"replace":       replace,
	}

	// Add optional parameters
	if sheetID, ok := args["sheet_id"].(float64); ok {
		input["sheetId"] = int(sheetID)
	}
	if matchCase, ok := args["match_case"].(bool); ok {
		input["matchCase"] = matchCase
	}

	payload := map[string]interface{}{
		"entityId": entityID,
		"appName":  "googlesheets",
		"input":    input,
	}

	return callComposioAPI(composioAPIKey, "GOOGLESHEETS_FIND_REPLACE", payload)
}

// NewComposioGoogleSheetsUpsertRowsTool creates a tool for upserting rows via Composio
func NewComposioGoogleSheetsUpsertRowsTool() *Tool {
	return &Tool{
		Name:        "googlesheets_upsert_rows",
		DisplayName: "Google Sheets - Upsert Rows",
		Description: `Smart upsert: update existing rows or insert new ones by matching a key column. Prevents duplicates and auto-maps columns by header.

WHEN TO USE THIS TOOL:
- User wants to sync data into a spreadsheet without creating duplicates
- User says "update or add these records" or "sync this data"
- Need to update existing rows by a unique key (email, SKU, ID) and add new rows for unmatched keys

PARAMETERS:
- spreadsheet_id (REQUIRED): The spreadsheet ID. Example: "1BxiMVs0XRA5nFMdKvBdBZjgmUUqptlbs74OgVE2upms"
- sheet_name (REQUIRED): Tab name to upsert into. Example: "Contacts"
- rows (REQUIRED): 2D array of row data. Example: [["alice@example.com", "Alice", "Updated"], ["bob@example.com", "Bob", "New"]]
- key_column (optional): Column header to match on. Example: "Email" or "SKU"
- headers (optional): Column headers if not using the sheet's first row. Example: ["Email", "Name", "Status"]

RETURNS: Summary of rows updated and rows inserted.`,
		Icon:     "FileSpreadsheet",
		Source:   ToolSourceComposio,
		Category: "integration",
		Keywords: []string{"google", "sheets", "spreadsheet", "upsert", "update", "insert", "merge", "sync", "composio"},
		Parameters: map[string]interface{}{
			"type": "object",
			"properties": map[string]interface{}{
				"credential_id": map[string]interface{}{
					"type":        "string",
					"description": "INTERNAL: Auto-injected by system. Do not set manually.",
				},
				"spreadsheet_id": map[string]interface{}{
					"type":        "string",
					"description": "Google Sheets spreadsheet ID (from the URL)",
				},
				"sheet_name": map[string]interface{}{
					"type":        "string",
					"description": "Name of the sheet/tab to upsert into",
				},
				"key_column": map[string]interface{}{
					"type":        "string",
					"description": "Column name to match on (e.g., 'Email', 'SKU', 'Lead ID')",
				},
				"rows": map[string]interface{}{
					"type":        "array",
					"description": "Array of row data arrays [[row1], [row2], ...]",
					"items": map[string]interface{}{
						"type": "array",
						"items": map[string]interface{}{}, // Allow any type
					},
				},
				"headers": map[string]interface{}{
					"type":        "array",
					"description": "Optional: Array of column headers (if not provided, uses first row of sheet)",
					"items": map[string]interface{}{
						"type": "string",
					},
				},
			},
			"required": []string{"spreadsheet_id", "sheet_name", "rows"},
		},
		Execute: executeComposioGoogleSheetsUpsertRows,
	}
}

func executeComposioGoogleSheetsUpsertRows(args map[string]interface{}) (string, error) {
	// ✅ RATE LIMITING
	if err := checkComposioRateLimit(args); err != nil {
		return "", err
	}

	credData, err := GetCredentialData(args, "composio_googlesheets")
	if err != nil {
		return "", fmt.Errorf("failed to get Composio credentials: %w", err)
	}

	entityID, ok := credData["composio_entity_id"].(string)
	if !ok || entityID == "" {
		return "", fmt.Errorf("composio_entity_id not found in credentials")
	}

	spreadsheetID, _ := args["spreadsheet_id"].(string)
	sheetName, _ := args["sheet_name"].(string)
	rows := args["rows"]

	if spreadsheetID == "" {
		return "", fmt.Errorf("'spreadsheet_id' is required")
	}
	if sheetName == "" {
		return "", fmt.Errorf("'sheet_name' is required")
	}
	if rows == nil {
		return "", fmt.Errorf("'rows' is required")
	}

	composioAPIKey := GetComposioAPIKey()
	if composioAPIKey == "" {
		return "", fmt.Errorf("COMPOSIO_API_KEY environment variable not set")
	}

	// Build input
	input := map[string]interface{}{
		"spreadsheetId": spreadsheetID,
		"sheetName":     sheetName,
		"rows":          rows,
	}

	// Add optional parameters
	if keyColumn, ok := args["key_column"].(string); ok && keyColumn != "" {
		input["keyColumn"] = keyColumn
	}
	if headers, ok := args["headers"].([]interface{}); ok && len(headers) > 0 {
		input["headers"] = headers
	}

	payload := map[string]interface{}{
		"entityId": entityID,
		"appName":  "googlesheets",
		"input":    input,
	}

	return callComposioAPI(composioAPIKey, "GOOGLESHEETS_UPSERT_ROWS", payload)
}

// getConnectedAccountID retrieves the connected account ID from Composio v3 API
func getConnectedAccountID(apiKey string, userID string, appName string) (string, error) {
	// Query v3 API to get connected accounts for this user (URL-safe to prevent injection)
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
		return "", fmt.Errorf("failed to send request: %w", err)
	}
	defer resp.Body.Close()

	respBody, _ := io.ReadAll(resp.Body)

	if resp.StatusCode >= 400 {
		return "", fmt.Errorf("Composio API error (status %d): %s", resp.StatusCode, string(respBody))
	}

	// Parse v3 response
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

	// Find the connected account for this app
	for _, account := range response.Items {
		if account.Toolkit.Slug == appName {
			// v2 execution endpoint needs the old UUID, not the new nano ID
			// Check if deprecated.uuid exists (for v2 compatibility)
			if account.Deprecated.UUID != "" {
				return account.Deprecated.UUID, nil
			}
			// Fall back to nano ID if UUID not available
			return account.ID, nil
		}
	}

	return "", fmt.Errorf("no connected account found for app '%s' and user '%s'", appName, userID)
}

// callComposioAPI makes a request to Composio's v3 API
func callComposioAPI(apiKey string, action string, payload map[string]interface{}) (string, error) {
	// v2 execution endpoint still works with v3 connected accounts
	url := "https://backend.composio.dev/api/v2/actions/" + action + "/execute"

	// Get params from payload
	entityID, _ := payload["entityId"].(string)
	appName, _ := payload["appName"].(string)
	input, _ := payload["input"].(map[string]interface{})

	// For v3, we need to find the connected account ID
	connectedAccountID, err := getConnectedAccountID(apiKey, entityID, appName)
	if err != nil {
		return "", fmt.Errorf("failed to get connected account ID: %w", err)
	}

	// Build v2 payload (v2 execution endpoint uses connectedAccountId with camelCase)
	v2Payload := map[string]interface{}{
		"connectedAccountId": connectedAccountID,
		"input":              input,
	}

	jsonData, err := json.Marshal(v2Payload)
	if err != nil {
		return "", fmt.Errorf("failed to marshal request: %w", err)
	}

	// ✅ SECURE LOGGING - Only log non-sensitive metadata
	log.Printf("🔍 [COMPOSIO] Action: %s, ConnectedAccount: %s", action, maskSensitiveID(connectedAccountID))

	req, err := http.NewRequest("POST", url, bytes.NewBuffer(jsonData))
	if err != nil {
		return "", fmt.Errorf("failed to create request: %w", err)
	}

	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("x-api-key", apiKey)

	client := &http.Client{Timeout: 30 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return "", fmt.Errorf("failed to send request: %w", err)
	}
	defer resp.Body.Close()

	// ✅ SECURITY FIX: Parse and log rate limit headers
	parseRateLimitHeaders(resp.Header, action)

	respBody, _ := io.ReadAll(resp.Body)

	if resp.StatusCode >= 400 {
		// ✅ SECURE ERROR HANDLING - Log full details server-side, sanitize for user
		log.Printf("❌ [COMPOSIO] API error (status %d) for action %s", resp.StatusCode, action)

		// Handle rate limiting with specific error
		if resp.StatusCode == 429 {
			retryAfter := resp.Header.Get("Retry-After")
			if retryAfter != "" {
				log.Printf("⚠️ [COMPOSIO] Rate limited, retry after: %s seconds", retryAfter)
				return "", fmt.Errorf("rate limit exceeded, retry after %s seconds", retryAfter)
			}
			return "", fmt.Errorf("rate limit exceeded, please try again later")
		}

		// Don't expose internal Composio error details to users
		if resp.StatusCode >= 500 {
			return "", fmt.Errorf("external service error (status %d)", resp.StatusCode)
		}
		// Client errors (4xx) can be slightly more specific
		return "", fmt.Errorf("invalid request (status %d): check spreadsheet ID and permissions", resp.StatusCode)
	}

	// Parse response
	var apiResponse map[string]interface{}
	if err := json.Unmarshal(respBody, &apiResponse); err != nil {
		return string(respBody), nil
	}

	// Return formatted response
	result, _ := json.MarshalIndent(apiResponse, "", "  ")
	return string(result), nil
}

// parseRateLimitHeaders parses and logs rate limit headers from Composio API responses
func parseRateLimitHeaders(headers http.Header, action string) {
	limit := headers.Get("X-RateLimit-Limit")
	remaining := headers.Get("X-RateLimit-Remaining")
	reset := headers.Get("X-RateLimit-Reset")

	if limit != "" || remaining != "" || reset != "" {
		log.Printf("📊 [COMPOSIO] Rate limits for %s - Limit: %s, Remaining: %s, Reset: %s",
			action, limit, remaining, reset)

		// Warning if approaching rate limit
		if remaining != "" && limit != "" {
		remainingInt := 0
			limitInt := 0
			fmt.Sscanf(remaining, "%d", &remainingInt)
			fmt.Sscanf(limit, "%d", &limitInt)

			if limitInt > 0 {
				percentRemaining := float64(remainingInt) / float64(limitInt) * 100
				if percentRemaining < 20 {
					log.Printf("⚠️ [COMPOSIO] Rate limit warning: only %.1f%% remaining (%d/%d)",
						percentRemaining, remainingInt, limitInt)
				}
			}
		}
	}
}
