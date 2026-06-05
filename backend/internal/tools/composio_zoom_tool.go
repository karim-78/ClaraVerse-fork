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

// composioZoomRateLimiter implements per-user rate limiting for Composio Zoom API calls
type composioZoomRateLimiter struct {
	requests map[string][]time.Time
	mutex    sync.RWMutex
	maxCalls int
	window   time.Duration
}

var globalZoomRateLimiter = &composioZoomRateLimiter{
	requests: make(map[string][]time.Time),
	maxCalls: 30,
	window:   1 * time.Minute,
}

func checkZoomRateLimit(args map[string]interface{}) error {
	userID, ok := args["__user_id__"].(string)
	if !ok || userID == "" {
		log.Printf("⚠️ [ZOOM] No user ID for rate limiting")
		return nil
	}

	globalZoomRateLimiter.mutex.Lock()
	defer globalZoomRateLimiter.mutex.Unlock()

	now := time.Now()
	windowStart := now.Add(-globalZoomRateLimiter.window)

	timestamps := globalZoomRateLimiter.requests[userID]
	validTimestamps := []time.Time{}
	for _, ts := range timestamps {
		if ts.After(windowStart) {
			validTimestamps = append(validTimestamps, ts)
		}
	}

	if len(validTimestamps) >= globalZoomRateLimiter.maxCalls {
		return fmt.Errorf("rate limit exceeded: max %d requests per minute", globalZoomRateLimiter.maxCalls)
	}

	validTimestamps = append(validTimestamps, now)
	globalZoomRateLimiter.requests[userID] = validTimestamps
	return nil
}

// NewZoomCreateMeetingTool creates a tool for creating Zoom meetings
func NewZoomCreateMeetingTool() *Tool {
	return &Tool{
		Name:        "zoom_create_meeting",
		DisplayName: "Zoom - Create Meeting",
		Description: `Create a new Zoom meeting. Can create either an instant meeting (starts now) or a scheduled meeting (starts at a future time). Returns the meeting join URL, meeting ID, and password.

WHEN TO USE THIS TOOL:
- The user asks "schedule a Zoom meeting" or "create a meeting"
- The user wants to set up a video conference
- The user says "set up a call for tomorrow at 3pm"

PARAMETERS:
- topic (REQUIRED): The meeting title, e.g., "Weekly Team Standup"
- start_time (optional): When the meeting starts in ISO 8601 format, e.g., "2024-06-15T14:00:00Z". If omitted, creates an instant meeting.
- duration (optional): Meeting length in minutes (default: 60)
- agenda (optional): Meeting description/agenda text
- timezone (optional): Timezone string, e.g., "America/New_York" or "UTC"
- password (optional): Meeting password. Auto-generated if not set.
- type (optional): 1=instant, 2=scheduled (default: 2)

RETURNS: Meeting join URL, meeting ID, password, and host URL.`,
		Icon:     "Video",
		Source:   ToolSourceComposio,
		Category: "integration",
		Keywords: []string{"zoom", "meeting", "create", "schedule", "video", "conference", "composio"},
		Parameters: map[string]interface{}{
			"type": "object",
			"properties": map[string]interface{}{
				"credential_id": map[string]interface{}{
					"type":        "string",
					"description": "INTERNAL: Auto-injected by system. Do not set manually.",
				},
				"topic": map[string]interface{}{
					"type":        "string",
					"description": "Meeting topic/title",
				},
				"agenda": map[string]interface{}{
					"type":        "string",
					"description": "Meeting agenda/description",
				},
				"start_time": map[string]interface{}{
					"type":        "string",
					"description": "Start time in ISO 8601 format (e.g., '2024-01-15T10:00:00Z'). If not set, creates instant meeting.",
				},
				"duration": map[string]interface{}{
					"type":        "integer",
					"description": "Meeting duration in minutes (default: 60)",
				},
				"timezone": map[string]interface{}{
					"type":        "string",
					"description": "Timezone (e.g., 'America/New_York', 'UTC')",
				},
				"password": map[string]interface{}{
					"type":        "string",
					"description": "Meeting password (optional, auto-generated if not set)",
				},
				"type": map[string]interface{}{
					"type":        "integer",
					"description": "Meeting type: 1=instant, 2=scheduled, 3=recurring no fixed time, 8=recurring fixed time (default: 2)",
				},
			},
			"required": []string{"topic"},
		},
		Execute: executeZoomCreateMeeting,
	}
}

func executeZoomCreateMeeting(args map[string]interface{}) (string, error) {
	if err := checkZoomRateLimit(args); err != nil {
		return "", err
	}

	credData, err := GetCredentialData(args, "composio_zoom")
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

	if topic, ok := args["topic"].(string); ok {
		input["topic"] = topic
	}
	if agenda, ok := args["agenda"].(string); ok {
		input["agenda"] = agenda
	}
	if startTime, ok := args["start_time"].(string); ok {
		input["start_time"] = startTime
	}
	if duration, ok := args["duration"].(float64); ok {
		input["duration"] = int(duration)
	}
	if timezone, ok := args["timezone"].(string); ok {
		input["timezone"] = timezone
	}
	if password, ok := args["password"].(string); ok {
		input["password"] = password
	}
	if meetingType, ok := args["type"].(float64); ok {
		input["type"] = int(meetingType)
	}

	return callComposioZoomAPI(composioAPIKey, entityID, "ZOOM_CREATE_A_MEETING", input)
}

// NewZoomListMeetingsTool creates a tool for listing meetings
func NewZoomListMeetingsTool() *Tool {
	return &Tool{
		Name:        "zoom_list_meetings",
		DisplayName: "Zoom - List Meetings",
		Description: `List the authenticated user's Zoom meetings. Returns meeting topics, IDs, start times, durations, and join URLs. Can filter by meeting type (upcoming, live, past).

WHEN TO USE THIS TOOL:
- The user asks "show me my Zoom meetings" or "what meetings do I have?"
- The user wants to see upcoming or past meetings
- You need to find a meeting ID for other operations (get, update, delete)

PARAMETERS:
- type (optional): Filter meetings by type: "scheduled", "live", "upcoming", "upcoming_meetings", or "previous_meetings"
- page_size (optional): How many meetings to return (default: 30, max: 300)

RETURNS: List of meetings with their IDs, topics, start times, durations, and join URLs.`,
		Icon:     "List",
		Source:   ToolSourceComposio,
		Category: "integration",
		Keywords: []string{"zoom", "meeting", "list", "schedule", "composio"},
		Parameters: map[string]interface{}{
			"type": "object",
			"properties": map[string]interface{}{
				"credential_id": map[string]interface{}{
					"type":        "string",
					"description": "INTERNAL: Auto-injected by system. Do not set manually.",
				},
				"type": map[string]interface{}{
					"type":        "string",
					"description": "Type of meetings: 'scheduled', 'live', 'upcoming', 'upcoming_meetings', 'previous_meetings'",
				},
				"page_size": map[string]interface{}{
					"type":        "integer",
					"description": "Number of meetings per page (default: 30, max: 300)",
				},
			},
			"required": []string{},
		},
		Execute: executeZoomListMeetings,
	}
}

func executeZoomListMeetings(args map[string]interface{}) (string, error) {
	if err := checkZoomRateLimit(args); err != nil {
		return "", err
	}

	credData, err := GetCredentialData(args, "composio_zoom")
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

	if meetingType, ok := args["type"].(string); ok {
		input["type"] = meetingType
	}
	if pageSize, ok := args["page_size"].(float64); ok {
		input["page_size"] = int(pageSize)
	}

	return callComposioZoomAPI(composioAPIKey, entityID, "ZOOM_LIST_MEETINGS", input)
}

// NewZoomGetMeetingTool creates a tool for getting meeting details
func NewZoomGetMeetingTool() *Tool {
	return &Tool{
		Name:        "zoom_get_meeting",
		DisplayName: "Zoom - Get Meeting",
		Description: `Get full details of a specific Zoom meeting by its meeting ID. Returns the meeting topic, start time, duration, join URL, password, host info, and settings.

WHEN TO USE THIS TOOL:
- The user asks "show me details of meeting 123456789"
- The user wants the join URL or password for a specific meeting
- You need meeting details before updating or sharing a meeting

PARAMETERS:
- meeting_id (REQUIRED): The Zoom meeting ID or UUID (e.g., "123456789")

RETURNS: Meeting topic, join URL, password, start time, duration, host email, and meeting settings.`,
		Icon:     "Video",
		Source:   ToolSourceComposio,
		Category: "integration",
		Keywords: []string{"zoom", "meeting", "get", "details", "composio"},
		Parameters: map[string]interface{}{
			"type": "object",
			"properties": map[string]interface{}{
				"credential_id": map[string]interface{}{
					"type":        "string",
					"description": "INTERNAL: Auto-injected by system. Do not set manually.",
				},
				"meeting_id": map[string]interface{}{
					"type":        "string",
					"description": "The meeting ID or UUID",
				},
			},
			"required": []string{"meeting_id"},
		},
		Execute: executeZoomGetMeeting,
	}
}

func executeZoomGetMeeting(args map[string]interface{}) (string, error) {
	if err := checkZoomRateLimit(args); err != nil {
		return "", err
	}

	credData, err := GetCredentialData(args, "composio_zoom")
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

	if meetingID, ok := args["meeting_id"].(string); ok {
		input["meeting_id"] = meetingID
	}

	return callComposioZoomAPI(composioAPIKey, entityID, "ZOOM_GET_A_MEETING", input)
}

// NewZoomUpdateMeetingTool creates a tool for updating meetings
func NewZoomUpdateMeetingTool() *Tool {
	return &Tool{
		Name:        "zoom_update_meeting",
		DisplayName: "Zoom - Update Meeting",
		Description: `Update an existing Zoom meeting's details. Can change the topic, agenda, start time, or duration. Only the fields you provide will be updated; other fields remain unchanged.

WHEN TO USE THIS TOOL:
- The user asks "change my meeting time" or "update the meeting topic"
- The user wants to reschedule a meeting
- The user needs to modify meeting details

PARAMETERS:
- meeting_id (REQUIRED): The Zoom meeting ID to update
- topic (optional): New meeting title
- agenda (optional): New meeting description
- start_time (optional): New start time in ISO 8601 format (e.g., "2024-06-15T14:00:00Z")
- duration (optional): New duration in minutes

RETURNS: Confirmation that the meeting was updated successfully.`,
		Icon:     "Edit",
		Source:   ToolSourceComposio,
		Category: "integration",
		Keywords: []string{"zoom", "meeting", "update", "edit", "composio"},
		Parameters: map[string]interface{}{
			"type": "object",
			"properties": map[string]interface{}{
				"credential_id": map[string]interface{}{
					"type":        "string",
					"description": "INTERNAL: Auto-injected by system. Do not set manually.",
				},
				"meeting_id": map[string]interface{}{
					"type":        "string",
					"description": "The meeting ID to update",
				},
				"topic": map[string]interface{}{
					"type":        "string",
					"description": "New meeting topic",
				},
				"agenda": map[string]interface{}{
					"type":        "string",
					"description": "New meeting agenda",
				},
				"start_time": map[string]interface{}{
					"type":        "string",
					"description": "New start time (ISO 8601 format)",
				},
				"duration": map[string]interface{}{
					"type":        "integer",
					"description": "New duration in minutes",
				},
			},
			"required": []string{"meeting_id"},
		},
		Execute: executeZoomUpdateMeeting,
	}
}

func executeZoomUpdateMeeting(args map[string]interface{}) (string, error) {
	if err := checkZoomRateLimit(args); err != nil {
		return "", err
	}

	credData, err := GetCredentialData(args, "composio_zoom")
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

	if meetingID, ok := args["meeting_id"].(string); ok {
		input["meeting_id"] = meetingID
	}
	if topic, ok := args["topic"].(string); ok {
		input["topic"] = topic
	}
	if agenda, ok := args["agenda"].(string); ok {
		input["agenda"] = agenda
	}
	if startTime, ok := args["start_time"].(string); ok {
		input["start_time"] = startTime
	}
	if duration, ok := args["duration"].(float64); ok {
		input["duration"] = int(duration)
	}

	return callComposioZoomAPI(composioAPIKey, entityID, "ZOOM_UPDATE_A_MEETING", input)
}

// NewZoomDeleteMeetingTool creates a tool for deleting meetings
func NewZoomDeleteMeetingTool() *Tool {
	return &Tool{
		Name:        "zoom_delete_meeting",
		DisplayName: "Zoom - Delete Meeting",
		Description: `Delete (cancel) a Zoom meeting permanently. This removes the meeting and invalidates its join URL. This action cannot be undone.

WHEN TO USE THIS TOOL:
- The user asks "cancel my meeting" or "delete this Zoom meeting"
- The user wants to permanently remove a scheduled meeting

PARAMETERS:
- meeting_id (REQUIRED): The Zoom meeting ID to delete

RETURNS: Confirmation that the meeting was deleted. WARNING: This is permanent and cannot be undone.`,
		Icon:     "Trash",
		Source:   ToolSourceComposio,
		Category: "integration",
		Keywords: []string{"zoom", "meeting", "delete", "cancel", "composio"},
		Parameters: map[string]interface{}{
			"type": "object",
			"properties": map[string]interface{}{
				"credential_id": map[string]interface{}{
					"type":        "string",
					"description": "INTERNAL: Auto-injected by system. Do not set manually.",
				},
				"meeting_id": map[string]interface{}{
					"type":        "string",
					"description": "The meeting ID to delete",
				},
			},
			"required": []string{"meeting_id"},
		},
		Execute: executeZoomDeleteMeeting,
	}
}

func executeZoomDeleteMeeting(args map[string]interface{}) (string, error) {
	if err := checkZoomRateLimit(args); err != nil {
		return "", err
	}

	credData, err := GetCredentialData(args, "composio_zoom")
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

	if meetingID, ok := args["meeting_id"].(string); ok {
		input["meeting_id"] = meetingID
	}

	return callComposioZoomAPI(composioAPIKey, entityID, "ZOOM_DELETE_A_MEETING", input)
}

// NewZoomGetUserTool creates a tool for getting user info
func NewZoomGetUserTool() *Tool {
	return &Tool{
		Name:        "zoom_get_user",
		DisplayName: "Zoom - Get My Profile",
		Description: `Get the authenticated Zoom user's profile information. Returns the user's name, email, account type, timezone, and other profile details. No parameters needed - automatically uses the connected Zoom account.

WHEN TO USE THIS TOOL:
- The user asks "what's my Zoom account info?" or "show my Zoom profile"
- You need the user's Zoom email or account details
- The user wants to verify their Zoom connection is working

PARAMETERS: None required. Automatically uses the authenticated user's account.

RETURNS: User name, email address, account type, timezone, and profile details.`,
		Icon:     "User",
		Source:   ToolSourceComposio,
		Category: "integration",
		Keywords: []string{"zoom", "user", "profile", "account", "composio"},
		Parameters: map[string]interface{}{
			"type": "object",
			"properties": map[string]interface{}{
				"credential_id": map[string]interface{}{
					"type":        "string",
					"description": "INTERNAL: Auto-injected by system. Do not set manually.",
				},
			},
			"required": []string{},
		},
		Execute: executeZoomGetUser,
	}
}

func executeZoomGetUser(args map[string]interface{}) (string, error) {
	if err := checkZoomRateLimit(args); err != nil {
		return "", err
	}

	credData, err := GetCredentialData(args, "composio_zoom")
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

	return callComposioZoomAPI(composioAPIKey, entityID, "ZOOM_GET_A_USER", input)
}

// NewZoomAddMeetingRegistrantTool creates a tool for adding registrants
func NewZoomAddMeetingRegistrantTool() *Tool {
	return &Tool{
		Name:        "zoom_add_registrant",
		DisplayName: "Zoom - Add Meeting Registrant",
		Description: `Add a person as a registrant to a Zoom meeting. This pre-registers them so they receive a unique join link. Useful for meetings that require registration.

WHEN TO USE THIS TOOL:
- The user asks "register John for the meeting" or "add attendees to my Zoom meeting"
- The user wants to pre-register someone for a scheduled meeting

PARAMETERS:
- meeting_id (REQUIRED): The Zoom meeting ID to register for
- email (REQUIRED): The registrant's email address
- first_name (REQUIRED): The registrant's first name
- last_name (optional): The registrant's last name

RETURNS: Registration confirmation with a unique join URL for the registrant.`,
		Icon:     "UserPlus",
		Source:   ToolSourceComposio,
		Category: "integration",
		Keywords: []string{"zoom", "meeting", "registrant", "add", "composio"},
		Parameters: map[string]interface{}{
			"type": "object",
			"properties": map[string]interface{}{
				"credential_id": map[string]interface{}{
					"type":        "string",
					"description": "INTERNAL: Auto-injected by system. Do not set manually.",
				},
				"meeting_id": map[string]interface{}{
					"type":        "string",
					"description": "The meeting ID",
				},
				"email": map[string]interface{}{
					"type":        "string",
					"description": "Registrant's email address",
				},
				"first_name": map[string]interface{}{
					"type":        "string",
					"description": "Registrant's first name",
				},
				"last_name": map[string]interface{}{
					"type":        "string",
					"description": "Registrant's last name",
				},
			},
			"required": []string{"meeting_id", "email", "first_name"},
		},
		Execute: executeZoomAddRegistrant,
	}
}

func executeZoomAddRegistrant(args map[string]interface{}) (string, error) {
	if err := checkZoomRateLimit(args); err != nil {
		return "", err
	}

	credData, err := GetCredentialData(args, "composio_zoom")
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

	if meetingID, ok := args["meeting_id"].(string); ok {
		input["meeting_id"] = meetingID
	}
	if email, ok := args["email"].(string); ok {
		input["email"] = email
	}
	if firstName, ok := args["first_name"].(string); ok {
		input["first_name"] = firstName
	}
	if lastName, ok := args["last_name"].(string); ok {
		input["last_name"] = lastName
	}

	return callComposioZoomAPI(composioAPIKey, entityID, "ZOOM_ADD_A_MEETING_REGISTRANT", input)
}

// callComposioZoomAPI makes a v2 API call to Composio for Zoom actions
func callComposioZoomAPI(apiKey string, entityID string, action string, input map[string]interface{}) (string, error) {
	connectedAccountID, err := getZoomConnectedAccountID(apiKey, entityID, "zoom")
	if err != nil {
		return "", fmt.Errorf("failed to get connected account: %w", err)
	}

	apiURL := "https://backend.composio.dev/api/v3/tools/execute/" + action

	v2Payload := map[string]interface{}{
		"connected_account_id": connectedAccountID,
		"user_id":              entityID,
		"arguments":            input,
	}

	jsonData, err := json.Marshal(v2Payload)
	if err != nil {
		return "", fmt.Errorf("failed to marshal request: %w", err)
	}

	log.Printf("📹 [ZOOM] Action: %s, ConnectedAccount: %s", action, maskSensitiveID(connectedAccountID))

	req, err := http.NewRequest("POST", apiURL, bytes.NewBuffer(jsonData))
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

	respBody, _ := io.ReadAll(resp.Body)

	if resp.StatusCode >= 400 {
		log.Printf("❌ [ZOOM] API error (status %d) for action %s: %s", resp.StatusCode, action, string(respBody))
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

// getZoomConnectedAccountID retrieves the connected account ID from Composio v3 API
func getZoomConnectedAccountID(apiKey string, userID string, appName string) (string, error) {
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
				return account.ID, nil
			}
			return account.ID, nil
		}
	}

	return "", fmt.Errorf("no %s connection found for user. Please connect your Zoom account first", appName)
}
