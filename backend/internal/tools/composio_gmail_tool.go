package tools

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"sync"
	"time"
)

// composioGmailRateLimiter implements per-user rate limiting for Composio Gmail API calls
type composioGmailRateLimiter struct {
	requests map[string][]time.Time
	mutex    sync.RWMutex
	maxCalls int
	window   time.Duration
}

var globalGmailRateLimiter = &composioGmailRateLimiter{
	requests: make(map[string][]time.Time),
	maxCalls: 50,
	window:   1 * time.Minute,
}

// checkGmailRateLimit checks rate limit using user ID from args
func checkGmailRateLimit(args map[string]interface{}) error {
	userID, ok := args["__user_id__"].(string)
	if !ok || userID == "" {
		log.Printf("⚠️ [GMAIL] No user ID for rate limiting")
		return nil
	}

	globalGmailRateLimiter.mutex.Lock()
	defer globalGmailRateLimiter.mutex.Unlock()

	now := time.Now()
	windowStart := now.Add(-globalGmailRateLimiter.window)

	timestamps := globalGmailRateLimiter.requests[userID]
	validTimestamps := []time.Time{}
	for _, ts := range timestamps {
		if ts.After(windowStart) {
			validTimestamps = append(validTimestamps, ts)
		}
	}

	if len(validTimestamps) >= globalGmailRateLimiter.maxCalls {
		return fmt.Errorf("rate limit exceeded: max %d requests per minute", globalGmailRateLimiter.maxCalls)
	}

	validTimestamps = append(validTimestamps, now)
	globalGmailRateLimiter.requests[userID] = validTimestamps
	return nil
}

// NewComposioGmailSendTool creates a tool for sending emails via Composio Gmail
func NewComposioGmailSendTool() *Tool {
	return &Tool{
		Name:        "gmail_send_email",
		DisplayName: "Gmail - Send Email",
		Description: `Send an email from the user's Gmail account. Supports multiple recipients, CC/BCC, and HTML formatting.

WHEN TO USE THIS TOOL:
- User wants to send an email
- User says "email this to..." or "send a message to..."
- User wants to compose and send an email

PARAMETERS:
- recipient_email (optional): Primary recipient. Example: "alice@example.com"
- subject (optional): Email subject line. Example: "Meeting Follow-up"
- body (optional): Email body text. Example: "Hi Alice, thanks for the meeting today."
- is_html (optional): Set true if body contains HTML. Default: false
- cc (optional): List of CC email addresses. Example: ["bob@example.com"]
- bcc (optional): List of BCC email addresses.

RETURNS: Confirmation with the sent message ID and thread ID.`,
		Icon:     "Mail",
		Source:   ToolSourceComposio,
		Category: "integration",
		Keywords: []string{"gmail", "email", "send", "compose", "composio"},
		Parameters: map[string]interface{}{
			"type": "object",
			"properties": map[string]interface{}{
				"credential_id": map[string]interface{}{
					"type":        "string",
					"description": "INTERNAL: Auto-injected by system. Do not set manually.",
				},
				"recipient_email": map[string]interface{}{
					"type":        "string",
					"description": "Primary recipient email address",
				},
				"subject": map[string]interface{}{
					"type":        "string",
					"description": "Email subject",
				},
				"body": map[string]interface{}{
					"type":        "string",
					"description": "Email body (plain text or HTML)",
				},
				"is_html": map[string]interface{}{
					"type":        "boolean",
					"description": "Set to true if body contains HTML (default: false)",
				},
				"cc": map[string]interface{}{
					"type":        "array",
					"description": "Array of CC email addresses",
					"items": map[string]interface{}{
						"type": "string",
					},
				},
				"bcc": map[string]interface{}{
					"type":        "array",
					"description": "Array of BCC email addresses",
					"items": map[string]interface{}{
						"type": "string",
					},
				},
			},
			"required": []string{},
		},
		Execute: executeComposioGmailSend,
	}
}

func executeComposioGmailSend(args map[string]interface{}) (string, error) {
	if err := checkGmailRateLimit(args); err != nil {
		return "", err
	}

	credData, err := GetCredentialData(args, "composio_gmail")
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

	// Build input
	input := map[string]interface{}{
		"user_id": "me",
	}

	if recipientEmail, ok := args["recipient_email"].(string); ok && recipientEmail != "" {
		input["recipient_email"] = recipientEmail
	}
	if subject, ok := args["subject"].(string); ok {
		input["subject"] = subject
	}
	if body, ok := args["body"].(string); ok {
		input["body"] = body
	}
	if isHTML, ok := args["is_html"].(bool); ok {
		input["is_html"] = isHTML
	}
	if cc, ok := args["cc"].([]interface{}); ok && len(cc) > 0 {
		input["cc"] = cc
	}
	if bcc, ok := args["bcc"].([]interface{}); ok && len(bcc) > 0 {
		input["bcc"] = bcc
	}

	payload := map[string]interface{}{
		"entityId": entityID,
		"appName":  "gmail",
		"input":    input,
	}

	return callComposioGmailAPI(composioAPIKey, entityID, "GMAIL_SEND_EMAIL", payload)
}

// NewComposioGmailFetchTool creates a tool for fetching/searching emails
func NewComposioGmailFetchTool() *Tool {
	return &Tool{
		Name:        "gmail_fetch_emails",
		DisplayName: "Gmail - Fetch Emails",
		Description: `Fetch and search emails from Gmail inbox using Gmail search query syntax.

WHEN TO USE THIS TOOL:
- User wants to check their email or inbox
- User says "show my unread emails" or "find emails from Alice"
- User wants to search for specific emails

PARAMETERS:
- query (optional): Gmail search query. Example: "from:alice@example.com is:unread" or "subject:invoice"
- max_results (optional): Max emails to return. Default: 10. Example: 20
- label_ids (optional): Filter by labels. Example: ["INBOX", "UNREAD"]

RETURNS: List of emails with sender, subject, date, message content, and labels.`,
		Icon:     "Mail",
		Source:   ToolSourceComposio,
		Category: "integration",
		Keywords: []string{"gmail", "email", "fetch", "search", "list", "composio"},
		Parameters: map[string]interface{}{
			"type": "object",
			"properties": map[string]interface{}{
				"credential_id": map[string]interface{}{
					"type":        "string",
					"description": "INTERNAL: Auto-injected by system. Do not set manually.",
				},
				"query": map[string]interface{}{
					"type":        "string",
					"description": "Gmail search query (e.g., 'from:user@example.com is:unread')",
				},
				"max_results": map[string]interface{}{
					"type":        "integer",
					"description": "Maximum number of emails to return (default: 10)",
				},
				"label_ids": map[string]interface{}{
					"type":        "array",
					"description": "Filter by label IDs (e.g., ['INBOX', 'UNREAD'])",
					"items": map[string]interface{}{
						"type": "string",
					},
				},
			},
			"required": []string{},
		},
		Execute: executeComposioGmailFetch,
	}
}

func executeComposioGmailFetch(args map[string]interface{}) (string, error) {
	if err := checkGmailRateLimit(args); err != nil {
		return "", err
	}

	credData, err := GetCredentialData(args, "composio_gmail")
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

	// Build input
	input := map[string]interface{}{
		"user_id":         "me",
		"include_payload": true,
		"verbose":         true,
	}

	if query, ok := args["query"].(string); ok && query != "" {
		input["query"] = query
	}
	if maxResults, ok := args["max_results"].(float64); ok {
		input["max_results"] = int(maxResults)
	} else {
		input["max_results"] = 10
	}
	if labelIDs, ok := args["label_ids"].([]interface{}); ok && len(labelIDs) > 0 {
		input["label_ids"] = labelIDs
	}

	payload := map[string]interface{}{
		"entityId": entityID,
		"appName":  "gmail",
		"input":    input,
	}

	result, err := callComposioGmailAPI(composioAPIKey, entityID, "GMAIL_FETCH_EMAILS", payload)
	if err != nil {
		return "", err
	}

	// Parse and simplify the response for LLM consumption
	return simplifyGmailFetchResponse(result)
}

// NewComposioGmailGetMessageTool creates a tool for getting a specific email by ID
func NewComposioGmailGetMessageTool() *Tool {
	return &Tool{
		Name:        "gmail_get_message",
		DisplayName: "Gmail - Get Message",
		Description: `Get the full content and metadata of a specific Gmail message by its message ID.

WHEN TO USE THIS TOOL:
- Need to read the full content of a specific email
- Have a message ID and want to see the complete email

PARAMETERS:
- message_id (REQUIRED): The Gmail message ID. Example: "18a1b2c3d4e5f678"

RETURNS: Full email including headers (from, to, subject, date), body text, and attachment info.`,
		Icon:     "Mail",
		Source:   ToolSourceComposio,
		Category: "integration",
		Keywords: []string{"gmail", "email", "get", "fetch", "message", "composio"},
		Parameters: map[string]interface{}{
			"type": "object",
			"properties": map[string]interface{}{
				"credential_id": map[string]interface{}{
					"type":        "string",
					"description": "INTERNAL: Auto-injected by system. Do not set manually.",
				},
				"message_id": map[string]interface{}{
					"type":        "string",
					"description": "The Gmail message ID",
				},
			},
			"required": []string{"message_id"},
		},
		Execute: executeComposioGmailGetMessage,
	}
}

func executeComposioGmailGetMessage(args map[string]interface{}) (string, error) {
	if err := checkGmailRateLimit(args); err != nil {
		return "", err
	}

	credData, err := GetCredentialData(args, "composio_gmail")
	if err != nil {
		return "", fmt.Errorf("failed to get Composio credentials: %w", err)
	}

	entityID, ok := credData["composio_entity_id"].(string)
	if !ok || entityID == "" {
		return "", fmt.Errorf("composio_entity_id not found in credentials")
	}

	messageID, _ := args["message_id"].(string)
	if messageID == "" {
		return "", fmt.Errorf("'message_id' is required")
	}

	composioAPIKey := GetComposioAPIKey()
	if composioAPIKey == "" {
		return "", fmt.Errorf("COMPOSIO_API_KEY environment variable not set")
	}

	payload := map[string]interface{}{
		"entityId": entityID,
		"appName":  "gmail",
		"input": map[string]interface{}{
			"message_id": messageID,
			"user_id":    "me",
			"format":     "full",
		},
	}

	return callComposioGmailAPI(composioAPIKey, entityID, "GMAIL_FETCH_MESSAGE_BY_MESSAGE_ID", payload)
}

// NewComposioGmailReplyTool creates a tool for replying to email threads
func NewComposioGmailReplyTool() *Tool {
	return &Tool{
		Name:        "gmail_reply_to_thread",
		DisplayName: "Gmail - Reply to Thread",
		Description: `Reply to an existing Gmail email thread, keeping the conversation together.

WHEN TO USE THIS TOOL:
- User wants to reply to an email
- User says "reply to this email" or "respond to that thread"

PARAMETERS:
- thread_id (REQUIRED): The Gmail thread ID to reply to. Example: "18a1b2c3d4e5f678"
- message_body (optional): Reply text. Example: "Thanks for the update, I'll review it."
- recipient_email (optional): Override recipient email if needed.
- is_html (optional): Set true if body contains HTML.

RETURNS: Confirmation with the sent reply's message ID.`,
		Icon:     "Mail",
		Source:   ToolSourceComposio,
		Category: "integration",
		Keywords: []string{"gmail", "email", "reply", "thread", "conversation", "composio"},
		Parameters: map[string]interface{}{
			"type": "object",
			"properties": map[string]interface{}{
				"credential_id": map[string]interface{}{
					"type":        "string",
					"description": "INTERNAL: Auto-injected by system. Do not set manually.",
				},
				"thread_id": map[string]interface{}{
					"type":        "string",
					"description": "The Gmail thread ID to reply to",
				},
				"message_body": map[string]interface{}{
					"type":        "string",
					"description": "Reply message body",
				},
				"recipient_email": map[string]interface{}{
					"type":        "string",
					"description": "Recipient email (optional if replying to thread)",
				},
				"is_html": map[string]interface{}{
					"type":        "boolean",
					"description": "Set to true if body contains HTML",
				},
			},
			"required": []string{"thread_id"},
		},
		Execute: executeComposioGmailReply,
	}
}

func executeComposioGmailReply(args map[string]interface{}) (string, error) {
	if err := checkGmailRateLimit(args); err != nil {
		return "", err
	}

	credData, err := GetCredentialData(args, "composio_gmail")
	if err != nil {
		return "", fmt.Errorf("failed to get Composio credentials: %w", err)
	}

	entityID, ok := credData["composio_entity_id"].(string)
	if !ok || entityID == "" {
		return "", fmt.Errorf("composio_entity_id not found in credentials")
	}

	threadID, _ := args["thread_id"].(string)
	if threadID == "" {
		return "", fmt.Errorf("'thread_id' is required")
	}

	composioAPIKey := GetComposioAPIKey()
	if composioAPIKey == "" {
		return "", fmt.Errorf("COMPOSIO_API_KEY environment variable not set")
	}

	input := map[string]interface{}{
		"thread_id": threadID,
		"user_id":   "me",
	}

	if messageBody, ok := args["message_body"].(string); ok && messageBody != "" {
		input["message_body"] = messageBody
	}
	if recipientEmail, ok := args["recipient_email"].(string); ok && recipientEmail != "" {
		input["recipient_email"] = recipientEmail
	}
	if isHTML, ok := args["is_html"].(bool); ok {
		input["is_html"] = isHTML
	}

	payload := map[string]interface{}{
		"entityId": entityID,
		"appName":  "gmail",
		"input":    input,
	}

	return callComposioGmailAPI(composioAPIKey, entityID, "GMAIL_REPLY_TO_THREAD", payload)
}

// NewComposioGmailCreateDraftTool creates a tool for creating email drafts
func NewComposioGmailCreateDraftTool() *Tool {
	return &Tool{
		Name:        "gmail_create_draft",
		DisplayName: "Gmail - Create Draft",
		Description: `Create an email draft in Gmail that can be reviewed and sent later.

WHEN TO USE THIS TOOL:
- User wants to draft an email without sending it yet
- User says "create a draft" or "save this email as draft"

PARAMETERS:
- recipient_email (optional): Recipient address. Example: "alice@example.com"
- subject (optional): Email subject. Example: "Proposal Draft"
- body (optional): Email body text.
- is_html (optional): Set true if body contains HTML.

RETURNS: The created draft's ID and message details.`,
		Icon:     "Mail",
		Source:   ToolSourceComposio,
		Category: "integration",
		Keywords: []string{"gmail", "email", "draft", "compose", "save", "composio"},
		Parameters: map[string]interface{}{
			"type": "object",
			"properties": map[string]interface{}{
				"credential_id": map[string]interface{}{
					"type":        "string",
					"description": "INTERNAL: Auto-injected by system. Do not set manually.",
				},
				"recipient_email": map[string]interface{}{
					"type":        "string",
					"description": "Primary recipient email address (optional)",
				},
				"subject": map[string]interface{}{
					"type":        "string",
					"description": "Email subject",
				},
				"body": map[string]interface{}{
					"type":        "string",
					"description": "Email body",
				},
				"is_html": map[string]interface{}{
					"type":        "boolean",
					"description": "Set to true if body contains HTML",
				},
			},
			"required": []string{},
		},
		Execute: executeComposioGmailCreateDraft,
	}
}

func executeComposioGmailCreateDraft(args map[string]interface{}) (string, error) {
	if err := checkGmailRateLimit(args); err != nil {
		return "", err
	}

	credData, err := GetCredentialData(args, "composio_gmail")
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

	input := map[string]interface{}{
		"user_id": "me",
	}

	if recipientEmail, ok := args["recipient_email"].(string); ok && recipientEmail != "" {
		input["recipient_email"] = recipientEmail
	}
	if subject, ok := args["subject"].(string); ok {
		input["subject"] = subject
	}
	if body, ok := args["body"].(string); ok {
		input["body"] = body
	}
	if isHTML, ok := args["is_html"].(bool); ok {
		input["is_html"] = isHTML
	}

	payload := map[string]interface{}{
		"entityId": entityID,
		"appName":  "gmail",
		"input":    input,
	}

	return callComposioGmailAPI(composioAPIKey, entityID, "GMAIL_CREATE_EMAIL_DRAFT", payload)
}

// NewComposioGmailSendDraftTool creates a tool for sending existing drafts
func NewComposioGmailSendDraftTool() *Tool {
	return &Tool{
		Name:        "gmail_send_draft",
		DisplayName: "Gmail - Send Draft",
		Description: `Send a previously created Gmail draft. The draft is removed after sending.

WHEN TO USE THIS TOOL:
- User wants to send a draft they created earlier
- User says "send that draft" or "send draft ID xyz"

PARAMETERS:
- draft_id (REQUIRED): The Gmail draft ID to send. Example: "r1234567890"

RETURNS: Confirmation with the sent message ID. The draft is deleted after sending.`,
		Icon:     "Mail",
		Source:   ToolSourceComposio,
		Category: "integration",
		Keywords: []string{"gmail", "email", "draft", "send", "composio"},
		Parameters: map[string]interface{}{
			"type": "object",
			"properties": map[string]interface{}{
				"credential_id": map[string]interface{}{
					"type":        "string",
					"description": "INTERNAL: Auto-injected by system. Do not set manually.",
				},
				"draft_id": map[string]interface{}{
					"type":        "string",
					"description": "The Gmail draft ID to send",
				},
			},
			"required": []string{"draft_id"},
		},
		Execute: executeComposioGmailSendDraft,
	}
}

func executeComposioGmailSendDraft(args map[string]interface{}) (string, error) {
	if err := checkGmailRateLimit(args); err != nil {
		return "", err
	}

	credData, err := GetCredentialData(args, "composio_gmail")
	if err != nil {
		return "", fmt.Errorf("failed to get Composio credentials: %w", err)
	}

	entityID, ok := credData["composio_entity_id"].(string)
	if !ok || entityID == "" {
		return "", fmt.Errorf("composio_entity_id not found in credentials")
	}

	draftID, _ := args["draft_id"].(string)
	if draftID == "" {
		return "", fmt.Errorf("'draft_id' is required")
	}

	composioAPIKey := GetComposioAPIKey()
	if composioAPIKey == "" {
		return "", fmt.Errorf("COMPOSIO_API_KEY environment variable not set")
	}

	payload := map[string]interface{}{
		"entityId": entityID,
		"appName":  "gmail",
		"input": map[string]interface{}{
			"draft_id": draftID,
			"user_id":  "me",
		},
	}

	return callComposioGmailAPI(composioAPIKey, entityID, "GMAIL_SEND_DRAFT", payload)
}

// NewComposioGmailListDraftsTool creates a tool for listing drafts
func NewComposioGmailListDraftsTool() *Tool {
	return &Tool{
		Name:        "gmail_list_drafts",
		DisplayName: "Gmail - List Drafts",
		Description: `List all saved email drafts in the user's Gmail account.

WHEN TO USE THIS TOOL:
- User wants to see their email drafts
- User asks "show my drafts" or "list unsent emails"

PARAMETERS:
- max_results (optional): Max drafts to return. Default: 10.

RETURNS: List of drafts with draft IDs, subjects, and recipients.`,
		Icon:     "Mail",
		Source:   ToolSourceComposio,
		Category: "integration",
		Keywords: []string{"gmail", "email", "draft", "list", "composio"},
		Parameters: map[string]interface{}{
			"type": "object",
			"properties": map[string]interface{}{
				"credential_id": map[string]interface{}{
					"type":        "string",
					"description": "INTERNAL: Auto-injected by system. Do not set manually.",
				},
				"max_results": map[string]interface{}{
					"type":        "integer",
					"description": "Maximum number of drafts to return (default: 10)",
				},
			},
			"required": []string{},
		},
		Execute: executeComposioGmailListDrafts,
	}
}

func executeComposioGmailListDrafts(args map[string]interface{}) (string, error) {
	if err := checkGmailRateLimit(args); err != nil {
		return "", err
	}

	credData, err := GetCredentialData(args, "composio_gmail")
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

	input := map[string]interface{}{
		"user_id": "me",
		"verbose": true,
	}

	if maxResults, ok := args["max_results"].(float64); ok {
		input["max_results"] = int(maxResults)
	} else {
		input["max_results"] = 10
	}

	payload := map[string]interface{}{
		"entityId": entityID,
		"appName":  "gmail",
		"input":    input,
	}

	return callComposioGmailAPI(composioAPIKey, entityID, "GMAIL_LIST_DRAFTS", payload)
}

// NewComposioGmailAddLabelTool creates a tool for managing email labels
func NewComposioGmailAddLabelTool() *Tool {
	return &Tool{
		Name:        "gmail_add_label",
		DisplayName: "Gmail - Add/Remove Labels",
		Description: `Add or remove labels (tags/categories) on a Gmail message to organize it.

WHEN TO USE THIS TOOL:
- User wants to label, star, or mark an email
- User says "mark this as important" or "remove unread label"
- User wants to organize emails with tags

PARAMETERS:
- message_id (REQUIRED): The Gmail message ID. Example: "18a1b2c3d4e5f678"
- add_label_ids (optional): Labels to add. Example: ["STARRED", "IMPORTANT"]
- remove_label_ids (optional): Labels to remove. Example: ["UNREAD"]

RETURNS: Updated message with current label list.`,
		Icon:     "Mail",
		Source:   ToolSourceComposio,
		Category: "integration",
		Keywords: []string{"gmail", "email", "label", "tag", "organize", "composio"},
		Parameters: map[string]interface{}{
			"type": "object",
			"properties": map[string]interface{}{
				"credential_id": map[string]interface{}{
					"type":        "string",
					"description": "INTERNAL: Auto-injected by system. Do not set manually.",
				},
				"message_id": map[string]interface{}{
					"type":        "string",
					"description": "The Gmail message ID",
				},
				"add_label_ids": map[string]interface{}{
					"type":        "array",
					"description": "Array of label IDs to add (e.g., ['INBOX', 'STARRED'])",
					"items": map[string]interface{}{
						"type": "string",
					},
				},
				"remove_label_ids": map[string]interface{}{
					"type":        "array",
					"description": "Array of label IDs to remove (e.g., ['UNREAD'])",
					"items": map[string]interface{}{
						"type": "string",
					},
				},
			},
			"required": []string{"message_id"},
		},
		Execute: executeComposioGmailAddLabel,
	}
}

func executeComposioGmailAddLabel(args map[string]interface{}) (string, error) {
	if err := checkGmailRateLimit(args); err != nil {
		return "", err
	}

	credData, err := GetCredentialData(args, "composio_gmail")
	if err != nil {
		return "", fmt.Errorf("failed to get Composio credentials: %w", err)
	}

	entityID, ok := credData["composio_entity_id"].(string)
	if !ok || entityID == "" {
		return "", fmt.Errorf("composio_entity_id not found in credentials")
	}

	messageID, _ := args["message_id"].(string)
	if messageID == "" {
		return "", fmt.Errorf("'message_id' is required")
	}

	composioAPIKey := GetComposioAPIKey()
	if composioAPIKey == "" {
		return "", fmt.Errorf("COMPOSIO_API_KEY environment variable not set")
	}

	input := map[string]interface{}{
		"message_id": messageID,
		"user_id":    "me",
	}

	if addLabelIDs, ok := args["add_label_ids"].([]interface{}); ok && len(addLabelIDs) > 0 {
		input["add_label_ids"] = addLabelIDs
	}
	if removeLabelIDs, ok := args["remove_label_ids"].([]interface{}); ok && len(removeLabelIDs) > 0 {
		input["remove_label_ids"] = removeLabelIDs
	}

	payload := map[string]interface{}{
		"entityId": entityID,
		"appName":  "gmail",
		"input":    input,
	}

	return callComposioGmailAPI(composioAPIKey, entityID, "GMAIL_ADD_LABEL_TO_EMAIL", payload)
}

// NewComposioGmailListLabelsTool creates a tool for listing all labels
func NewComposioGmailListLabelsTool() *Tool {
	return &Tool{
		Name:        "gmail_list_labels",
		DisplayName: "Gmail - List Labels",
		Description: `List all Gmail labels including system labels (INBOX, SENT, TRASH) and user-created custom labels.

WHEN TO USE THIS TOOL:
- User wants to see what labels/folders they have in Gmail
- Need to look up a label ID before applying it to a message
- User asks "what labels do I have"

PARAMETERS: None required.

RETURNS: List of all labels with their IDs, names, and types (system vs user).`,
		Icon:     "Mail",
		Source:   ToolSourceComposio,
		Category: "integration",
		Keywords: []string{"gmail", "email", "label", "list", "categories", "composio"},
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
		Execute: executeComposioGmailListLabels,
	}
}

func executeComposioGmailListLabels(args map[string]interface{}) (string, error) {
	if err := checkGmailRateLimit(args); err != nil {
		return "", err
	}

	credData, err := GetCredentialData(args, "composio_gmail")
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

	payload := map[string]interface{}{
		"entityId": entityID,
		"appName":  "gmail",
		"input": map[string]interface{}{
			"user_id": "me",
		},
	}

	return callComposioGmailAPI(composioAPIKey, entityID, "GMAIL_LIST_LABELS", payload)
}

// NewComposioGmailTrashTool creates a tool for moving emails to trash
func NewComposioGmailTrashTool() *Tool {
	return &Tool{
		Name:        "gmail_move_to_trash",
		DisplayName: "Gmail - Move to Trash",
		Description: `Move a Gmail message to the Trash folder. Messages in Trash can be recovered within 30 days.

WHEN TO USE THIS TOOL:
- User wants to delete an email
- User says "trash this email" or "delete that message"

PARAMETERS:
- message_id (REQUIRED): The Gmail message ID to trash. Example: "18a1b2c3d4e5f678"

RETURNS: Confirmation that the message was moved to Trash. Messages are auto-deleted after 30 days in Trash.`,
		Icon:     "Mail",
		Source:   ToolSourceComposio,
		Category: "integration",
		Keywords: []string{"gmail", "email", "trash", "delete", "remove", "composio"},
		Parameters: map[string]interface{}{
			"type": "object",
			"properties": map[string]interface{}{
				"credential_id": map[string]interface{}{
					"type":        "string",
					"description": "INTERNAL: Auto-injected by system. Do not set manually.",
				},
				"message_id": map[string]interface{}{
					"type":        "string",
					"description": "The Gmail message ID to trash",
				},
			},
			"required": []string{"message_id"},
		},
		Execute: executeComposioGmailTrash,
	}
}

func executeComposioGmailTrash(args map[string]interface{}) (string, error) {
	if err := checkGmailRateLimit(args); err != nil {
		return "", err
	}

	credData, err := GetCredentialData(args, "composio_gmail")
	if err != nil {
		return "", fmt.Errorf("failed to get Composio credentials: %w", err)
	}

	entityID, ok := credData["composio_entity_id"].(string)
	if !ok || entityID == "" {
		return "", fmt.Errorf("composio_entity_id not found in credentials")
	}

	messageID, _ := args["message_id"].(string)
	if messageID == "" {
		return "", fmt.Errorf("'message_id' is required")
	}

	composioAPIKey := GetComposioAPIKey()
	if composioAPIKey == "" {
		return "", fmt.Errorf("COMPOSIO_API_KEY environment variable not set")
	}

	payload := map[string]interface{}{
		"entityId": entityID,
		"appName":  "gmail",
		"input": map[string]interface{}{
			"message_id": messageID,
			"user_id":    "me",
		},
	}

	return callComposioGmailAPI(composioAPIKey, entityID, "GMAIL_MOVE_TO_TRASH", payload)
}

// callComposioGmailAPI makes a v2 API call to Composio for Gmail actions
func callComposioGmailAPI(apiKey string, entityID string, action string, payload map[string]interface{}) (string, error) {
	// Get connected account ID
	connectedAccountID, err := getGmailConnectedAccountID(apiKey, entityID, "gmail")
	if err != nil {
		return "", fmt.Errorf("failed to get connected account: %w", err)
	}

	url := "https://backend.composio.dev/api/v2/actions/" + action + "/execute"

	v2Payload := map[string]interface{}{
		"connectedAccountId": connectedAccountID,
		"input":              payload["input"],
	}

	jsonData, err := json.Marshal(v2Payload)
	if err != nil {
		return "", fmt.Errorf("failed to marshal request: %w", err)
	}

	log.Printf("🔍 [GMAIL] Action: %s, ConnectedAccount: %s", action, maskSensitiveID(connectedAccountID))

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
	parseGmailRateLimitHeaders(resp.Header, action)

	respBody, _ := io.ReadAll(resp.Body)

	if resp.StatusCode >= 400 {
		log.Printf("❌ [GMAIL] API error (status %d) for action %s", resp.StatusCode, action)
		log.Printf("❌ [GMAIL] Composio error response: %s", string(respBody))
		log.Printf("❌ [GMAIL] Request payload: %s", string(jsonData))

		// Handle rate limiting with specific error
		if resp.StatusCode == 429 {
			retryAfter := resp.Header.Get("Retry-After")
			if retryAfter != "" {
				log.Printf("⚠️ [GMAIL] Rate limited, retry after: %s seconds", retryAfter)
				return "", fmt.Errorf("rate limit exceeded, retry after %s seconds", retryAfter)
			}
			return "", fmt.Errorf("rate limit exceeded, please try again later")
		}

		if resp.StatusCode >= 500 {
			return "", fmt.Errorf("external service error (status %d)", resp.StatusCode)
		}
		return "", fmt.Errorf("invalid request (status %d): check parameters and permissions", resp.StatusCode)
	}

	var apiResponse map[string]interface{}
	if err := json.Unmarshal(respBody, &apiResponse); err != nil {
		return string(respBody), nil
	}

	result, _ := json.MarshalIndent(apiResponse, "", "  ")
	return string(result), nil
}

// getGmailConnectedAccountID retrieves the connected account ID from Composio v3 API
func getGmailConnectedAccountID(apiKey string, userID string, appName string) (string, error) {
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

	// Parse v3 response with proper structure including deprecated.uuid
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

	return "", fmt.Errorf("no %s connection found for user. Please connect your Gmail account first", appName)
}

// stripHTMLAndClean removes HTML tags and cleans up whitespace from text
func stripHTMLAndClean(html string) string {
	// Remove HTML tags using regex
	re := regexp.MustCompile(`<[^>]*>`)
	text := re.ReplaceAllString(html, "")

	// Decode HTML entities like &nbsp;, &amp;, etc.
	text = strings.ReplaceAll(text, "&nbsp;", " ")
	text = strings.ReplaceAll(text, "&amp;", "&")
	text = strings.ReplaceAll(text, "&lt;", "<")
	text = strings.ReplaceAll(text, "&gt;", ">")
	text = strings.ReplaceAll(text, "&quot;", "\"")
	text = strings.ReplaceAll(text, "&#39;", "'")
	text = strings.ReplaceAll(text, "&apos;", "'")
	text = strings.ReplaceAll(text, "\u00a0", " ") // Non-breaking space
	text = strings.ReplaceAll(text, "\u200b", "") // Zero-width space
	text = strings.ReplaceAll(text, "\u200c", "") // Zero-width non-joiner
	text = strings.ReplaceAll(text, "\u200d", "") // Zero-width joiner
	text = strings.ReplaceAll(text, "\ufeff", "") // Zero-width no-break space
	text = strings.ReplaceAll(text, "\r", "")   // Remove carriage returns
	text = strings.ReplaceAll(text, "\u003e", " ")  // Remove greater-than symbol
	text = strings.ReplaceAll(text, "\u003c", " ")  // Remove less-than symbol
	text = strings.ReplaceAll(text, "\u0026", " ")  // Remove ampersand symbol
	text = strings.ReplaceAll(text, "\u00ab", " ")  // Remove left-pointing double angle quotation mark
	text = strings.ReplaceAll(text, "\u00bb", " ")  // Remove right-pointing double angle quotation mark
	text = strings.ReplaceAll(text, "\u0026", "")  // Remove ampersand symbol


	// Remove excessive whitespace
	lines := strings.Split(text, "\n")
	var cleanedLines []string
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line != "" {
			cleanedLines = append(cleanedLines, line)
		}
	}

	text = strings.Join(cleanedLines, "\n")

	// Collapse multiple spaces into one
	re = regexp.MustCompile(`\s+`)
	text = re.ReplaceAllString(text, " ")

	// Final trim
	text = strings.TrimSpace(text)

	return text
}

// simplifyGmailFetchResponse parses the raw Composio Gmail response and returns a simplified, LLM-friendly format
func simplifyGmailFetchResponse(rawResponse string) (string, error) {
	var response map[string]interface{}
	if err := json.Unmarshal([]byte(rawResponse), &response); err != nil {
		// If parsing fails, return raw response
		return rawResponse, nil
	}

	// Extract the data.messages array
	data, ok := response["data"].(map[string]interface{})
	if !ok {
		return rawResponse, nil
	}

	messages, ok := data["messages"].([]interface{})
	if !ok || len(messages) == 0 {
		return "No emails found matching your criteria.", nil
	}

	// Build simplified response
	simplified := make([]map[string]interface{}, 0, len(messages))

	for _, msg := range messages {
		msgMap, ok := msg.(map[string]interface{})
		if !ok {
			continue
		}

		simplifiedMsg := make(map[string]interface{})

		// Extract essential fields
		if messageID, ok := msgMap["messageId"].(string); ok {
			simplifiedMsg["message_id"] = messageID
		}
		if threadID, ok := msgMap["threadId"].(string); ok {
			simplifiedMsg["thread_id"] = threadID
		}
		if subject, ok := msgMap["subject"].(string); ok {
			simplifiedMsg["subject"] = subject
		}
		if from, ok := msgMap["from"].(string); ok {
			simplifiedMsg["from"] = from
		}
		if date, ok := msgMap["date"].(string); ok {
			simplifiedMsg["date"] = date
		}
		if snippet, ok := msgMap["snippet"].(string); ok {
			simplifiedMsg["snippet"] = snippet
		}

		// Extract message text (prefer full text over snippet)
		// Strip HTML tags and clean up whitespace
		if messageText, ok := msgMap["messageText"].(string); ok && messageText != "" {
			simplifiedMsg["message"] = stripHTMLAndClean(messageText)
		} else if snippet, ok := msgMap["snippet"].(string); ok {
			simplifiedMsg["message"] = snippet
		}

		// Include labels only if they contain useful info (skip internal IDs)
		if labels, ok := msgMap["labelIds"].([]interface{}); ok {
			readableLabels := []string{}
			for _, label := range labels {
				if labelStr, ok := label.(string); ok {
					// Only include readable labels (INBOX, UNREAD, IMPORTANT, etc.)
					if labelStr == "INBOX" || labelStr == "UNREAD" || labelStr == "IMPORTANT" ||
					   labelStr == "STARRED" || labelStr == "SENT" || labelStr == "DRAFT" {
						readableLabels = append(readableLabels, labelStr)
					}
				}
			}
			if len(readableLabels) > 0 {
				simplifiedMsg["labels"] = readableLabels
			}
		}

		simplified = append(simplified, simplifiedMsg)
	}

	// Format as JSON for LLM
	result, err := json.MarshalIndent(map[string]interface{}{
		"count":    len(simplified),
		"messages": simplified,
	}, "", "  ")

	if err != nil {
		return rawResponse, nil
	}

	return string(result), nil
}

// parseGmailRateLimitHeaders parses and logs rate limit headers from Gmail API responses
func parseGmailRateLimitHeaders(headers http.Header, action string) {
	limit := headers.Get("X-RateLimit-Limit")
	remaining := headers.Get("X-RateLimit-Remaining")
	reset := headers.Get("X-RateLimit-Reset")

	if limit != "" || remaining != "" || reset != "" {
		log.Printf("📊 [GMAIL] Rate limits for %s - Limit: %s, Remaining: %s, Reset: %s",
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
					log.Printf("⚠️ [GMAIL] Rate limit warning: only %.1f%% remaining (%d/%d)",
						percentRemaining, remainingInt, limitInt)
				}
			}
		}
	}
}
