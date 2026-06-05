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

// composioCalendarRateLimiter implements per-user rate limiting for Composio Calendar API calls
type composioCalendarRateLimiter struct {
	requests map[string][]time.Time
	mutex    sync.RWMutex
	maxCalls int
	window   time.Duration
}

var globalCalendarRateLimiter = &composioCalendarRateLimiter{
	requests: make(map[string][]time.Time),
	maxCalls: 50,
	window:   1 * time.Minute,
}

func checkCalendarRateLimit(args map[string]interface{}) error {
	userID, ok := args["__user_id__"].(string)
	if !ok || userID == "" {
		log.Printf("⚠️ [GOOGLECALENDAR] No user ID for rate limiting")
		return nil
	}

	globalCalendarRateLimiter.mutex.Lock()
	defer globalCalendarRateLimiter.mutex.Unlock()

	now := time.Now()
	windowStart := now.Add(-globalCalendarRateLimiter.window)

	timestamps := globalCalendarRateLimiter.requests[userID]
	validTimestamps := []time.Time{}
	for _, ts := range timestamps {
		if ts.After(windowStart) {
			validTimestamps = append(validTimestamps, ts)
		}
	}

	if len(validTimestamps) >= globalCalendarRateLimiter.maxCalls {
		return fmt.Errorf("rate limit exceeded: max %d requests per minute", globalCalendarRateLimiter.maxCalls)
	}

	validTimestamps = append(validTimestamps, now)
	globalCalendarRateLimiter.requests[userID] = validTimestamps
	return nil
}

// NewGoogleCalendarCreateEventTool creates a tool for creating calendar events
func NewGoogleCalendarCreateEventTool() *Tool {
	return &Tool{
		Name:        "googlecalendar_create_event",
		DisplayName: "Google Calendar - Create Event",
		Description: `Create a new event on the user's Google Calendar with title, time, location, and attendees.

WHEN TO USE THIS TOOL:
- User wants to schedule a meeting or event
- User says "create a calendar event" or "schedule a meeting"
- User wants to add something to their calendar

PARAMETERS:
- summary (REQUIRED): Event title. Example: "Team standup"
- start_time (REQUIRED): Start in RFC3339 format. Example: "2024-01-15T10:00:00-05:00"
- end_time (REQUIRED): End in RFC3339 format. Example: "2024-01-15T11:00:00-05:00"
- description (optional): Event details. Example: "Weekly sync to discuss project progress"
- location (optional): Where the event takes place. Example: "Conference Room B"
- timezone (optional): Timezone name. Example: "America/New_York". Default: UTC
- attendees (optional): List of email addresses to invite. Example: ["alice@example.com"]
- calendar_id (optional): Which calendar to use. Default: "primary"

RETURNS: The created event data including event ID, HTML link, and confirmed details.`,
		Icon:     "Calendar",
		Source:   ToolSourceComposio,
		Category: "integration",
		Keywords: []string{"google", "calendar", "event", "create", "schedule", "meeting", "composio"},
		Parameters: map[string]interface{}{
			"type": "object",
			"properties": map[string]interface{}{
				"credential_id": map[string]interface{}{
					"type":        "string",
					"description": "INTERNAL: Auto-injected by system. Do not set manually.",
				},
				"summary": map[string]interface{}{
					"type":        "string",
					"description": "Title/summary of the event",
				},
				"description": map[string]interface{}{
					"type":        "string",
					"description": "Description of the event",
				},
				"location": map[string]interface{}{
					"type":        "string",
					"description": "Location of the event",
				},
				"start_time": map[string]interface{}{
					"type":        "string",
					"description": "Start time in RFC3339 format (e.g., '2024-01-15T10:00:00Z' or '2024-01-15T10:00:00-05:00')",
				},
				"end_time": map[string]interface{}{
					"type":        "string",
					"description": "End time in RFC3339 format (e.g., '2024-01-15T11:00:00Z')",
				},
				"timezone": map[string]interface{}{
					"type":        "string",
					"description": "Timezone (e.g., 'America/New_York', 'UTC'). Default: UTC",
				},
				"attendees": map[string]interface{}{
					"type":        "array",
					"description": "List of attendee email addresses",
					"items": map[string]interface{}{
						"type": "string",
					},
				},
				"calendar_id": map[string]interface{}{
					"type":        "string",
					"description": "Calendar ID (default: 'primary')",
				},
			},
			"required": []string{"summary", "start_time", "end_time"},
		},
		Execute: executeGoogleCalendarCreateEvent,
	}
}

func executeGoogleCalendarCreateEvent(args map[string]interface{}) (string, error) {
	if err := checkCalendarRateLimit(args); err != nil {
		return "", err
	}

	credData, err := GetCredentialData(args, "composio_googlecalendar")
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

	if summary, ok := args["summary"].(string); ok {
		input["summary"] = summary
	}
	if description, ok := args["description"].(string); ok {
		input["description"] = description
	}
	if location, ok := args["location"].(string); ok {
		input["location"] = location
	}
	if startTime, ok := args["start_time"].(string); ok {
		input["start_time"] = startTime
	}
	if endTime, ok := args["end_time"].(string); ok {
		input["end_time"] = endTime
	}
	if timezone, ok := args["timezone"].(string); ok {
		input["timezone"] = timezone
	}
	if attendees, ok := args["attendees"].([]interface{}); ok {
		input["attendees"] = attendees
	}
	if calendarID, ok := args["calendar_id"].(string); ok && calendarID != "" {
		input["calendar_id"] = calendarID
	} else {
		input["calendar_id"] = "primary"
	}

	return callComposioCalendarAPI(composioAPIKey, entityID, "GOOGLECALENDAR_CREATE_EVENT", input)
}

// NewGoogleCalendarListEventsTool creates a tool for listing calendar events
func NewGoogleCalendarListEventsTool() *Tool {
	return &Tool{
		Name:        "googlecalendar_list_events",
		DisplayName: "Google Calendar - List Events",
		Description: `List events from the user's Google Calendar, optionally filtered by time range or search query.

WHEN TO USE THIS TOOL:
- User wants to see their upcoming events or schedule
- User asks "what's on my calendar" or "show my meetings today"
- User wants to find a specific event by keyword

PARAMETERS:
- time_min (optional): Only show events after this time (RFC3339). Example: "2024-01-15T00:00:00Z"
- time_max (optional): Only show events before this time (RFC3339). Example: "2024-01-16T00:00:00Z"
- max_results (optional): Maximum events to return. Default: 10. Example: 25
- q (optional): Text search to filter events. Example: "standup"
- calendar_id (optional): Which calendar to query. Default: "primary"

RETURNS: List of events with titles, times, locations, attendees, and event IDs.`,
		Icon:     "Calendar",
		Source:   ToolSourceComposio,
		Category: "integration",
		Keywords: []string{"google", "calendar", "event", "list", "schedule", "composio"},
		Parameters: map[string]interface{}{
			"type": "object",
			"properties": map[string]interface{}{
				"credential_id": map[string]interface{}{
					"type":        "string",
					"description": "INTERNAL: Auto-injected by system. Do not set manually.",
				},
				"time_min": map[string]interface{}{
					"type":        "string",
					"description": "Lower bound for event start time (RFC3339 format)",
				},
				"time_max": map[string]interface{}{
					"type":        "string",
					"description": "Upper bound for event start time (RFC3339 format)",
				},
				"max_results": map[string]interface{}{
					"type":        "integer",
					"description": "Maximum number of events to return (default: 10)",
				},
				"calendar_id": map[string]interface{}{
					"type":        "string",
					"description": "Calendar ID (default: 'primary')",
				},
				"q": map[string]interface{}{
					"type":        "string",
					"description": "Free text search terms to find events",
				},
			},
			"required": []string{},
		},
		Execute: executeGoogleCalendarListEvents,
	}
}

func executeGoogleCalendarListEvents(args map[string]interface{}) (string, error) {
	if err := checkCalendarRateLimit(args); err != nil {
		return "", err
	}

	credData, err := GetCredentialData(args, "composio_googlecalendar")
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

	if timeMin, ok := args["time_min"].(string); ok {
		input["time_min"] = timeMin
	}
	if timeMax, ok := args["time_max"].(string); ok {
		input["time_max"] = timeMax
	}
	if maxResults, ok := args["max_results"].(float64); ok {
		input["max_results"] = int(maxResults)
	}
	if q, ok := args["q"].(string); ok {
		input["q"] = q
	}
	if calendarID, ok := args["calendar_id"].(string); ok && calendarID != "" {
		input["calendar_id"] = calendarID
	} else {
		input["calendar_id"] = "primary"
	}

	return callComposioCalendarAPI(composioAPIKey, entityID, "GOOGLECALENDAR_EVENTS_LIST", input)
}

// NewGoogleCalendarFindFreeSlotsTool creates a tool for finding free time slots
func NewGoogleCalendarFindFreeSlotsTool() *Tool {
	return &Tool{
		Name:        "googlecalendar_find_free_slots",
		DisplayName: "Google Calendar - Find Free Slots",
		Description: `Find free and busy time slots in Google Calendar to check availability for scheduling.

WHEN TO USE THIS TOOL:
- User wants to find available time slots
- User asks "when am I free" or "check my availability"
- User needs to find a good time for a meeting

PARAMETERS:
- time_min (REQUIRED): Start of the time window to check (RFC3339). Example: "2024-01-15T08:00:00Z"
- time_max (REQUIRED): End of the time window to check (RFC3339). Example: "2024-01-15T18:00:00Z"
- calendar_ids (optional): List of calendar IDs to check. Default: primary calendar only.

RETURNS: List of busy time intervals and free slots within the specified time range.`,
		Icon:     "Clock",
		Source:   ToolSourceComposio,
		Category: "integration",
		Keywords: []string{"google", "calendar", "free", "busy", "availability", "composio"},
		Parameters: map[string]interface{}{
			"type": "object",
			"properties": map[string]interface{}{
				"credential_id": map[string]interface{}{
					"type":        "string",
					"description": "INTERNAL: Auto-injected by system. Do not set manually.",
				},
				"time_min": map[string]interface{}{
					"type":        "string",
					"description": "Start of the interval (RFC3339 format)",
				},
				"time_max": map[string]interface{}{
					"type":        "string",
					"description": "End of the interval (RFC3339 format)",
				},
				"calendar_ids": map[string]interface{}{
					"type":        "array",
					"description": "List of calendar IDs to check (default: primary)",
					"items": map[string]interface{}{
						"type": "string",
					},
				},
			},
			"required": []string{"time_min", "time_max"},
		},
		Execute: executeGoogleCalendarFindFreeSlots,
	}
}

func executeGoogleCalendarFindFreeSlots(args map[string]interface{}) (string, error) {
	if err := checkCalendarRateLimit(args); err != nil {
		return "", err
	}

	credData, err := GetCredentialData(args, "composio_googlecalendar")
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

	if timeMin, ok := args["time_min"].(string); ok {
		input["time_min"] = timeMin
	}
	if timeMax, ok := args["time_max"].(string); ok {
		input["time_max"] = timeMax
	}
	if calendarIDs, ok := args["calendar_ids"].([]interface{}); ok {
		input["calendar_ids"] = calendarIDs
	}

	return callComposioCalendarAPI(composioAPIKey, entityID, "GOOGLECALENDAR_FIND_FREE_SLOTS", input)
}

// NewGoogleCalendarQuickAddEventTool creates a tool for quick event creation
func NewGoogleCalendarQuickAddEventTool() *Tool {
	return &Tool{
		Name:        "googlecalendar_quick_add_event",
		DisplayName: "Google Calendar - Quick Add Event",
		Description: `Quickly create a Google Calendar event using a natural language description instead of structured fields.

WHEN TO USE THIS TOOL:
- User gives an informal event description like "Lunch with Sarah Friday at noon"
- User wants to quickly add an event without specifying exact RFC3339 times
- Simpler alternative to the full "Create Event" tool

PARAMETERS:
- text (REQUIRED): Natural language event description. Example: "Meeting with John tomorrow at 3pm" or "Dentist appointment Friday 2-3pm"
- calendar_id (optional): Which calendar to add to. Default: "primary"

RETURNS: The created event data with the parsed title, date, and time.`,
		Icon:     "Zap",
		Source:   ToolSourceComposio,
		Category: "integration",
		Keywords: []string{"google", "calendar", "quick", "add", "natural", "language", "composio"},
		Parameters: map[string]interface{}{
			"type": "object",
			"properties": map[string]interface{}{
				"credential_id": map[string]interface{}{
					"type":        "string",
					"description": "INTERNAL: Auto-injected by system. Do not set manually.",
				},
				"text": map[string]interface{}{
					"type":        "string",
					"description": "Natural language description of the event (e.g., 'Meeting with John tomorrow at 3pm')",
				},
				"calendar_id": map[string]interface{}{
					"type":        "string",
					"description": "Calendar ID (default: 'primary')",
				},
			},
			"required": []string{"text"},
		},
		Execute: executeGoogleCalendarQuickAddEvent,
	}
}

func executeGoogleCalendarQuickAddEvent(args map[string]interface{}) (string, error) {
	if err := checkCalendarRateLimit(args); err != nil {
		return "", err
	}

	credData, err := GetCredentialData(args, "composio_googlecalendar")
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

	if text, ok := args["text"].(string); ok {
		input["text"] = text
	}
	if calendarID, ok := args["calendar_id"].(string); ok && calendarID != "" {
		input["calendar_id"] = calendarID
	} else {
		input["calendar_id"] = "primary"
	}

	return callComposioCalendarAPI(composioAPIKey, entityID, "GOOGLECALENDAR_QUICK_ADD", input)
}

// NewGoogleCalendarDeleteEventTool creates a tool for deleting calendar events
func NewGoogleCalendarDeleteEventTool() *Tool {
	return &Tool{
		Name:        "googlecalendar_delete_event",
		DisplayName: "Google Calendar - Delete Event",
		Description: `Permanently delete an event from Google Calendar. This action cannot be undone.

WHEN TO USE THIS TOOL:
- User wants to cancel or delete a calendar event
- User says "remove that meeting" or "cancel the event"

PARAMETERS:
- event_id (REQUIRED): The Google Calendar event ID. Example: "abc123def456"
- calendar_id (optional): Which calendar the event is on. Default: "primary"

RETURNS: Confirmation that the event was deleted. WARNING: This is permanent.`,
		Icon:     "Trash",
		Source:   ToolSourceComposio,
		Category: "integration",
		Keywords: []string{"google", "calendar", "delete", "remove", "cancel", "composio"},
		Parameters: map[string]interface{}{
			"type": "object",
			"properties": map[string]interface{}{
				"credential_id": map[string]interface{}{
					"type":        "string",
					"description": "INTERNAL: Auto-injected by system. Do not set manually.",
				},
				"event_id": map[string]interface{}{
					"type":        "string",
					"description": "ID of the event to delete",
				},
				"calendar_id": map[string]interface{}{
					"type":        "string",
					"description": "Calendar ID (default: 'primary')",
				},
			},
			"required": []string{"event_id"},
		},
		Execute: executeGoogleCalendarDeleteEvent,
	}
}

func executeGoogleCalendarDeleteEvent(args map[string]interface{}) (string, error) {
	if err := checkCalendarRateLimit(args); err != nil {
		return "", err
	}

	credData, err := GetCredentialData(args, "composio_googlecalendar")
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

	if eventID, ok := args["event_id"].(string); ok {
		input["event_id"] = eventID
	}
	if calendarID, ok := args["calendar_id"].(string); ok && calendarID != "" {
		input["calendar_id"] = calendarID
	} else {
		input["calendar_id"] = "primary"
	}

	return callComposioCalendarAPI(composioAPIKey, entityID, "GOOGLECALENDAR_DELETE_EVENT", input)
}

// NewGoogleCalendarGetEventTool creates a tool for getting event details
func NewGoogleCalendarGetEventTool() *Tool {
	return &Tool{
		Name:        "googlecalendar_get_event",
		DisplayName: "Google Calendar - Get Event",
		Description: `Get full details of a specific Google Calendar event by its event ID.

WHEN TO USE THIS TOOL:
- User wants to see details of a specific event
- User asks "what are the details of that meeting"
- Need to look up attendees, description, or location for an event

PARAMETERS:
- event_id (REQUIRED): The Google Calendar event ID. Example: "abc123def456"
- calendar_id (optional): Which calendar to look in. Default: "primary"

RETURNS: Full event details including title, time, location, description, attendees, and status.`,
		Icon:     "Calendar",
		Source:   ToolSourceComposio,
		Category: "integration",
		Keywords: []string{"google", "calendar", "get", "event", "details", "composio"},
		Parameters: map[string]interface{}{
			"type": "object",
			"properties": map[string]interface{}{
				"credential_id": map[string]interface{}{
					"type":        "string",
					"description": "INTERNAL: Auto-injected by system. Do not set manually.",
				},
				"event_id": map[string]interface{}{
					"type":        "string",
					"description": "ID of the event to retrieve",
				},
				"calendar_id": map[string]interface{}{
					"type":        "string",
					"description": "Calendar ID (default: 'primary')",
				},
			},
			"required": []string{"event_id"},
		},
		Execute: executeGoogleCalendarGetEvent,
	}
}

func executeGoogleCalendarGetEvent(args map[string]interface{}) (string, error) {
	if err := checkCalendarRateLimit(args); err != nil {
		return "", err
	}

	credData, err := GetCredentialData(args, "composio_googlecalendar")
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

	if eventID, ok := args["event_id"].(string); ok {
		input["event_id"] = eventID
	}
	if calendarID, ok := args["calendar_id"].(string); ok && calendarID != "" {
		input["calendar_id"] = calendarID
	} else {
		input["calendar_id"] = "primary"
	}

	return callComposioCalendarAPI(composioAPIKey, entityID, "GOOGLECALENDAR_FIND_EVENT", input)
}

// NewGoogleCalendarUpdateEventTool creates a tool for updating calendar events
func NewGoogleCalendarUpdateEventTool() *Tool {
	return &Tool{
		Name:        "googlecalendar_update_event",
		DisplayName: "Google Calendar - Update Event",
		Description: `Update an existing Google Calendar event to change its time, title, location, or other details.

WHEN TO USE THIS TOOL:
- User wants to reschedule a meeting
- User says "change the meeting time" or "update the event description"
- User wants to modify any detail of an existing event

PARAMETERS:
- event_id (REQUIRED): The event ID to update. Example: "abc123def456"
- summary (optional): New event title. Example: "Updated Team Sync"
- description (optional): New event description.
- location (optional): New location. Example: "Room 301"
- start_time (optional): New start time (RFC3339). Example: "2024-01-15T14:00:00Z"
- end_time (optional): New end time (RFC3339). Example: "2024-01-15T15:00:00Z"
- calendar_id (optional): Which calendar. Default: "primary"

RETURNS: The updated event data with all current field values.`,
		Icon:     "Edit",
		Source:   ToolSourceComposio,
		Category: "integration",
		Keywords: []string{"google", "calendar", "update", "edit", "modify", "composio"},
		Parameters: map[string]interface{}{
			"type": "object",
			"properties": map[string]interface{}{
				"credential_id": map[string]interface{}{
					"type":        "string",
					"description": "INTERNAL: Auto-injected by system. Do not set manually.",
				},
				"event_id": map[string]interface{}{
					"type":        "string",
					"description": "ID of the event to update",
				},
				"summary": map[string]interface{}{
					"type":        "string",
					"description": "New title/summary of the event",
				},
				"description": map[string]interface{}{
					"type":        "string",
					"description": "New description",
				},
				"location": map[string]interface{}{
					"type":        "string",
					"description": "New location",
				},
				"start_time": map[string]interface{}{
					"type":        "string",
					"description": "New start time (RFC3339 format)",
				},
				"end_time": map[string]interface{}{
					"type":        "string",
					"description": "New end time (RFC3339 format)",
				},
				"calendar_id": map[string]interface{}{
					"type":        "string",
					"description": "Calendar ID (default: 'primary')",
				},
			},
			"required": []string{"event_id"},
		},
		Execute: executeGoogleCalendarUpdateEvent,
	}
}

func executeGoogleCalendarUpdateEvent(args map[string]interface{}) (string, error) {
	if err := checkCalendarRateLimit(args); err != nil {
		return "", err
	}

	credData, err := GetCredentialData(args, "composio_googlecalendar")
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

	if eventID, ok := args["event_id"].(string); ok {
		input["event_id"] = eventID
	}
	if summary, ok := args["summary"].(string); ok {
		input["summary"] = summary
	}
	if description, ok := args["description"].(string); ok {
		input["description"] = description
	}
	if location, ok := args["location"].(string); ok {
		input["location"] = location
	}
	if startTime, ok := args["start_time"].(string); ok {
		input["start_time"] = startTime
	}
	if endTime, ok := args["end_time"].(string); ok {
		input["end_time"] = endTime
	}
	if calendarID, ok := args["calendar_id"].(string); ok && calendarID != "" {
		input["calendar_id"] = calendarID
	} else {
		input["calendar_id"] = "primary"
	}

	return callComposioCalendarAPI(composioAPIKey, entityID, "GOOGLECALENDAR_UPDATE_EVENT", input)
}

// NewGoogleCalendarListCalendarsTool creates a tool for listing calendars
func NewGoogleCalendarListCalendarsTool() *Tool {
	return &Tool{
		Name:        "googlecalendar_list_calendars",
		DisplayName: "Google Calendar - List Calendars",
		Description: `List all Google Calendars accessible by the user, including shared and subscribed calendars.

WHEN TO USE THIS TOOL:
- User wants to see all their calendars
- User asks "which calendars do I have" or "show my calendar list"
- Need to find a specific calendar ID before creating/listing events

PARAMETERS: None required.

RETURNS: List of calendars with their IDs, names, colors, and access roles. The primary calendar ID is "primary".`,
		Icon:     "List",
		Source:   ToolSourceComposio,
		Category: "integration",
		Keywords: []string{"google", "calendar", "list", "calendars", "composio"},
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
		Execute: executeGoogleCalendarListCalendars,
	}
}

func executeGoogleCalendarListCalendars(args map[string]interface{}) (string, error) {
	if err := checkCalendarRateLimit(args); err != nil {
		return "", err
	}

	credData, err := GetCredentialData(args, "composio_googlecalendar")
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

	return callComposioCalendarAPI(composioAPIKey, entityID, "GOOGLECALENDAR_LIST_CALENDARS", input)
}

// callComposioCalendarAPI makes a v2 API call to Composio for Google Calendar actions
func callComposioCalendarAPI(apiKey string, entityID string, action string, input map[string]interface{}) (string, error) {
	connectedAccountID, err := getCalendarConnectedAccountID(apiKey, entityID, "googlecalendar")
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

	log.Printf("🔍 [GOOGLECALENDAR] Action: %s, ConnectedAccount: %s", action, maskSensitiveID(connectedAccountID))

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
		log.Printf("❌ [GOOGLECALENDAR] API error (status %d) for action %s: %s", resp.StatusCode, action, string(respBody))
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

// getCalendarConnectedAccountID retrieves the connected account ID from Composio v3 API
func getCalendarConnectedAccountID(apiKey string, userID string, appName string) (string, error) {
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

	return "", fmt.Errorf("no %s connection found for user. Please connect your Google Calendar account first", appName)
}
