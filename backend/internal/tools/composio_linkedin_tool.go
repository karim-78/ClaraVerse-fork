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

// composioLinkedInRateLimiter implements dual per-user rate limiting (minute + daily)
// to prevent hitting LinkedIn's strict API limits
type composioLinkedInRateLimiter struct {
	minuteRequests map[string][]time.Time
	dailyRequests  map[string][]time.Time
	mutex          sync.RWMutex
	maxPerMinute   int
	maxPerDay      int
	minuteWindow   time.Duration
	dayWindow      time.Duration
}

var globalLinkedInRateLimiter = &composioLinkedInRateLimiter{
	minuteRequests: make(map[string][]time.Time),
	dailyRequests:  make(map[string][]time.Time),
	maxPerMinute:   20,  // Conservative: 20 calls per minute (down from 50)
	maxPerDay:      100, // Daily limit: 100 calls per user per day
	minuteWindow:   1 * time.Minute,
	dayWindow:      24 * time.Hour,
}

// init starts a cleanup goroutine to prevent memory buildup
func init() {
	go cleanupLinkedInRateLimiter()
}

// cleanupLinkedInRateLimiter periodically removes old rate limit data
func cleanupLinkedInRateLimiter() {
	ticker := time.NewTicker(1 * time.Hour)
	defer ticker.Stop()

	for range ticker.C {
		globalLinkedInRateLimiter.mutex.Lock()

		now := time.Now()
		cutoff := now.Add(-25 * time.Hour) // Keep 25 hours of data (1 hour buffer)

		// Clean up old daily requests
		for userID, timestamps := range globalLinkedInRateLimiter.dailyRequests {
			validTimestamps := []time.Time{}
			for _, ts := range timestamps {
				if ts.After(cutoff) {
					validTimestamps = append(validTimestamps, ts)
				}
			}
			if len(validTimestamps) > 0 {
				globalLinkedInRateLimiter.dailyRequests[userID] = validTimestamps
			} else {
				delete(globalLinkedInRateLimiter.dailyRequests, userID)
			}
		}

		// Clean up old minute requests
		minuteCutoff := now.Add(-2 * time.Minute)
		for userID, timestamps := range globalLinkedInRateLimiter.minuteRequests {
			validTimestamps := []time.Time{}
			for _, ts := range timestamps {
				if ts.After(minuteCutoff) {
					validTimestamps = append(validTimestamps, ts)
				}
			}
			if len(validTimestamps) > 0 {
				globalLinkedInRateLimiter.minuteRequests[userID] = validTimestamps
			} else {
				delete(globalLinkedInRateLimiter.minuteRequests, userID)
			}
		}

		globalLinkedInRateLimiter.mutex.Unlock()
		log.Printf("🧹 [LINKEDIN] Rate limiter cleanup completed")
	}
}

// checkLinkedInRateLimit checks both minute and daily rate limits
func checkLinkedInRateLimit(args map[string]interface{}) error {
	userID, ok := args["__user_id__"].(string)
	if !ok || userID == "" {
		log.Printf("⚠️ [LINKEDIN] No user ID for rate limiting")
		return nil
	}

	globalLinkedInRateLimiter.mutex.Lock()
	defer globalLinkedInRateLimiter.mutex.Unlock()

	now := time.Now()

	// Check minute limit
	minuteWindowStart := now.Add(-globalLinkedInRateLimiter.minuteWindow)
	minuteTimestamps := globalLinkedInRateLimiter.minuteRequests[userID]
	validMinuteTimestamps := []time.Time{}
	for _, ts := range minuteTimestamps {
		if ts.After(minuteWindowStart) {
			validMinuteTimestamps = append(validMinuteTimestamps, ts)
		}
	}

	if len(validMinuteTimestamps) >= globalLinkedInRateLimiter.maxPerMinute {
		return fmt.Errorf("LinkedIn rate limit: max %d requests per minute. Please wait before retrying", globalLinkedInRateLimiter.maxPerMinute)
	}

	// Check daily limit
	dayWindowStart := now.Add(-globalLinkedInRateLimiter.dayWindow)
	dailyTimestamps := globalLinkedInRateLimiter.dailyRequests[userID]
	validDailyTimestamps := []time.Time{}
	for _, ts := range dailyTimestamps {
		if ts.After(dayWindowStart) {
			validDailyTimestamps = append(validDailyTimestamps, ts)
		}
	}

	if len(validDailyTimestamps) >= globalLinkedInRateLimiter.maxPerDay {
		remaining := globalLinkedInRateLimiter.dayWindow - now.Sub(validDailyTimestamps[0])
		hoursRemaining := int(remaining.Hours())
		return fmt.Errorf("LinkedIn daily limit reached: max %d requests per day. Limit resets in %d hours", globalLinkedInRateLimiter.maxPerDay, hoursRemaining)
	}

	// Record the request
	validMinuteTimestamps = append(validMinuteTimestamps, now)
	validDailyTimestamps = append(validDailyTimestamps, now)
	globalLinkedInRateLimiter.minuteRequests[userID] = validMinuteTimestamps
	globalLinkedInRateLimiter.dailyRequests[userID] = validDailyTimestamps

	// Log usage for monitoring
	dailyUsage := len(validDailyTimestamps)
	if dailyUsage%10 == 0 { // Log every 10 calls
		log.Printf("📊 [LINKEDIN] User %s has made %d/%d daily LinkedIn calls", maskSensitiveLinkedInID(userID), dailyUsage, globalLinkedInRateLimiter.maxPerDay)
	}

	return nil
}

// NewComposioLinkedInCreateCommentTool creates a tool for creating comments on LinkedIn posts
func NewComposioLinkedInCreateCommentTool() *Tool {
	return &Tool{
		Name:        "linkedin_create_comment",
		DisplayName: "LinkedIn - Create Comment",
		Description: `Add a comment on a LinkedIn post, share, or reply to an existing comment.

WHEN TO USE THIS TOOL:
- User wants to comment on a LinkedIn post
- User says "reply to this post" or "add a comment"

PARAMETERS:
- target_urn (REQUIRED): URN of the post or comment to reply to. Example: "urn:li:share:123456789"
- actor (REQUIRED): URN of the person or organization commenting. Example: "urn:li:person:abc123"
- message (REQUIRED): Comment message object with text content. Example: {"text": "Great insights!"}

RETURNS: The created comment data including comment URN.`,
		Icon:     "linkedin",
		Source:   ToolSourceComposio,
		Category: "integration",
		Keywords: []string{"linkedin", "comment", "reply", "engage", "social", "composio"},
		Parameters: map[string]interface{}{
			"type": "object",
			"properties": map[string]interface{}{
				"credential_id": map[string]interface{}{
					"type":        "string",
					"description": "INTERNAL: Auto-injected by system. Do not set manually.",
				},
				"target_urn": map[string]interface{}{
					"type":        "string",
					"description": "URN of the target entity (share, UGC post, or parent comment)",
				},
				"actor": map[string]interface{}{
					"type":        "string",
					"description": "URN of the actor (person or organization) creating the comment",
				},
				"message": map[string]interface{}{
					"type":        "object",
					"description": "Comment message object with text",
				},
			},
			"required": []string{"target_urn", "actor", "message"},
		},
		Execute: executeComposioLinkedInCreateComment,
	}
}

func executeComposioLinkedInCreateComment(args map[string]interface{}) (string, error) {
	if err := checkLinkedInRateLimit(args); err != nil {
		return "", err
	}

	credData, err := GetCredentialData(args, "composio_linkedin")
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
	input := map[string]interface{}{}

	if targetURN, ok := args["target_urn"].(string); ok && targetURN != "" {
		input["target_urn"] = targetURN
	} else {
		return "", fmt.Errorf("'target_urn' is required")
	}

	if actor, ok := args["actor"].(string); ok && actor != "" {
		input["actor"] = actor
	} else {
		return "", fmt.Errorf("'actor' is required")
	}

	if message, ok := args["message"].(map[string]interface{}); ok {
		input["message"] = message
	} else {
		return "", fmt.Errorf("'message' is required")
	}

	payload := map[string]interface{}{
		"entityId": entityID,
		"appName":  "linkedin",
		"input":    input,
	}

	return callComposioLinkedInAPI(composioAPIKey, entityID, "LINKEDIN_CREATE_COMMENT_ON_POST", payload)
}

// NewComposioLinkedInCreatePostTool creates a tool for creating LinkedIn posts
func NewComposioLinkedInCreatePostTool() *Tool {
	return &Tool{
		Name:        "linkedin_create_post",
		DisplayName: "LinkedIn - Create Post",
		Description: `Create and publish a new text post on LinkedIn for the user's profile or a company page.

WHEN TO USE THIS TOOL:
- User wants to post on LinkedIn
- User says "publish this on LinkedIn" or "create a LinkedIn post"
- User wants to share content on their LinkedIn profile

PARAMETERS:
- author (REQUIRED): URN of the author (person or organization). Example: "urn:li:person:abc123"
- commentary (REQUIRED): The post text content. Example: "Excited to share our latest product update!"
- visibility (optional): Who can see the post - "PUBLIC", "CONNECTIONS", or "LOGGED_IN". Default: PUBLIC
- lifecycleState (optional): "PUBLISHED" or "DRAFT". Default: PUBLISHED

RETURNS: The created post data including share URN and post URL.`,
		Icon:     "linkedin",
		Source:   ToolSourceComposio,
		Category: "integration",
		Keywords: []string{"linkedin", "post", "publish", "share", "social", "composio"},
		Parameters: map[string]interface{}{
			"type": "object",
			"properties": map[string]interface{}{
				"credential_id": map[string]interface{}{
					"type":        "string",
					"description": "INTERNAL: Auto-injected by system. Do not set manually.",
				},
				"author": map[string]interface{}{
					"type":        "string",
					"description": "URN of the author (person or organization)",
				},
				"commentary": map[string]interface{}{
					"type":        "string",
					"description": "Text content of the post",
				},
				"visibility": map[string]interface{}{
					"type":        "string",
					"description": "Post visibility: PUBLIC, CONNECTIONS, or LOGGED_IN (default: PUBLIC)",
				},
				"lifecycleState": map[string]interface{}{
					"type":        "string",
					"description": "Lifecycle state: PUBLISHED or DRAFT (default: PUBLISHED)",
				},
			},
			"required": []string{"author", "commentary"},
		},
		Execute: executeComposioLinkedInCreatePost,
	}
}

func executeComposioLinkedInCreatePost(args map[string]interface{}) (string, error) {
	if err := checkLinkedInRateLimit(args); err != nil {
		return "", err
	}

	credData, err := GetCredentialData(args, "composio_linkedin")
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
	input := map[string]interface{}{}

	if author, ok := args["author"].(string); ok && author != "" {
		input["author"] = author
	} else {
		return "", fmt.Errorf("'author' is required")
	}

	if commentary, ok := args["commentary"].(string); ok && commentary != "" {
		input["commentary"] = commentary
	} else {
		return "", fmt.Errorf("'commentary' is required")
	}

	if visibility, ok := args["visibility"].(string); ok && visibility != "" {
		input["visibility"] = visibility
	}

	if lifecycleState, ok := args["lifecycleState"].(string); ok && lifecycleState != "" {
		input["lifecycleState"] = lifecycleState
	}

	if distribution, ok := args["distribution"].(map[string]interface{}); ok {
		input["distribution"] = distribution
	}

	if isReshareDisabled, ok := args["isReshareDisabledByAuthor"].(bool); ok {
		input["isReshareDisabledByAuthor"] = isReshareDisabled
	}

	payload := map[string]interface{}{
		"entityId": entityID,
		"appName":  "linkedin",
		"input":    input,
	}

	return callComposioLinkedInAPI(composioAPIKey, entityID, "LINKEDIN_CREATE_LINKED_IN_POST", payload)
}

// NewComposioLinkedInDeletePostTool creates a tool for deleting LinkedIn posts
func NewComposioLinkedInDeletePostTool() *Tool {
	return &Tool{
		Name:        "linkedin_delete_post",
		DisplayName: "LinkedIn - Delete Post",
		Description: `Permanently delete a LinkedIn post by its share ID. This action cannot be undone.

WHEN TO USE THIS TOOL:
- User wants to delete a LinkedIn post
- User says "remove my LinkedIn post" or "delete that post"

PARAMETERS:
- share_id (REQUIRED): The share ID of the post to delete. Example: "7123456789012345678"

RETURNS: Confirmation that the post was deleted. WARNING: This is permanent.`,
		Icon:     "linkedin",
		Source:   ToolSourceComposio,
		Category: "integration",
		Keywords: []string{"linkedin", "delete", "remove", "post", "social", "composio"},
		Parameters: map[string]interface{}{
			"type": "object",
			"properties": map[string]interface{}{
				"credential_id": map[string]interface{}{
					"type":        "string",
					"description": "INTERNAL: Auto-injected by system. Do not set manually.",
				},
				"share_id": map[string]interface{}{
					"type":        "string",
					"description": "ID of the share to delete",
				},
			},
			"required": []string{"share_id"},
		},
		Execute: executeComposioLinkedInDeletePost,
	}
}

func executeComposioLinkedInDeletePost(args map[string]interface{}) (string, error) {
	if err := checkLinkedInRateLimit(args); err != nil {
		return "", err
	}

	credData, err := GetCredentialData(args, "composio_linkedin")
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

	shareID, _ := args["share_id"].(string)
	if shareID == "" {
		return "", fmt.Errorf("'share_id' is required")
	}

	payload := map[string]interface{}{
		"entityId": entityID,
		"appName":  "linkedin",
		"input": map[string]interface{}{
			"share_id": shareID,
		},
	}

	return callComposioLinkedInAPI(composioAPIKey, entityID, "LINKEDIN_DELETE_LINKED_IN_POST", payload)
}

// NewComposioLinkedInDeleteUGCPostTool creates a tool for deleting UGC posts (legacy API)
func NewComposioLinkedInDeleteUGCPostTool() *Tool {
	return &Tool{
		Name:        "linkedin_delete_ugc_post",
		DisplayName: "LinkedIn - Delete UGC Post (Legacy)",
		Description: `Delete a LinkedIn UGC (User Generated Content) post using the legacy API endpoint. Use this for older posts created via the UGC API.

WHEN TO USE THIS TOOL:
- Need to delete a post created via the legacy UGC Post API
- The standard delete post tool doesn't work for this post type

PARAMETERS:
- ugc_post_urn (REQUIRED): URN of the UGC post to delete. Example: "urn:li:ugcPost:123456789"

RETURNS: Confirmation of deletion. Previously deleted posts also return success (idempotent).`,
		Icon:     "linkedin",
		Source:   ToolSourceComposio,
		Category: "integration",
		Keywords: []string{"linkedin", "delete", "ugc", "post", "legacy", "composio"},
		Parameters: map[string]interface{}{
			"type": "object",
			"properties": map[string]interface{}{
				"credential_id": map[string]interface{}{
					"type":        "string",
					"description": "INTERNAL: Auto-injected by system. Do not set manually.",
				},
				"ugc_post_urn": map[string]interface{}{
					"type":        "string",
					"description": "URN of the UGC post to delete",
				},
			},
			"required": []string{"ugc_post_urn"},
		},
		Execute: executeComposioLinkedInDeleteUGCPost,
	}
}

func executeComposioLinkedInDeleteUGCPost(args map[string]interface{}) (string, error) {
	if err := checkLinkedInRateLimit(args); err != nil {
		return "", err
	}

	credData, err := GetCredentialData(args, "composio_linkedin")
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

	ugcPostURN, _ := args["ugc_post_urn"].(string)
	if ugcPostURN == "" {
		return "", fmt.Errorf("'ugc_post_urn' is required")
	}

	payload := map[string]interface{}{
		"entityId": entityID,
		"appName":  "linkedin",
		"input": map[string]interface{}{
			"ugc_post_urn": ugcPostURN,
		},
	}

	return callComposioLinkedInAPI(composioAPIKey, entityID, "LINKEDIN_DELETE_UGC_POST", payload)
}

// NewComposioLinkedInDeleteUGCPostsTool creates a tool for deleting UGC posts
func NewComposioLinkedInDeleteUGCPostsTool() *Tool {
	return &Tool{
		Name:        "linkedin_delete_ugc_posts",
		DisplayName: "LinkedIn - Delete UGC Posts",
		Description: `Delete a LinkedIn UGC post by its URN identifier using the batch delete endpoint.

WHEN TO USE THIS TOOL:
- Need to delete a UGC post from LinkedIn
- Alternative to the single UGC post delete tool

PARAMETERS:
- ugc_post_urn (REQUIRED): URN of the UGC post to delete. Example: "urn:li:ugcPost:123456789"

RETURNS: Confirmation that the UGC post was deleted.`,
		Icon:     "linkedin",
		Source:   ToolSourceComposio,
		Category: "integration",
		Keywords: []string{"linkedin", "delete", "ugc", "posts", "composio"},
		Parameters: map[string]interface{}{
			"type": "object",
			"properties": map[string]interface{}{
				"credential_id": map[string]interface{}{
					"type":        "string",
					"description": "INTERNAL: Auto-injected by system. Do not set manually.",
				},
				"ugc_post_urn": map[string]interface{}{
					"type":        "string",
					"description": "URN of the UGC post to delete",
				},
			},
			"required": []string{"ugc_post_urn"},
		},
		Execute: executeComposioLinkedInDeleteUGCPosts,
	}
}

func executeComposioLinkedInDeleteUGCPosts(args map[string]interface{}) (string, error) {
	if err := checkLinkedInRateLimit(args); err != nil {
		return "", err
	}

	credData, err := GetCredentialData(args, "composio_linkedin")
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

	ugcPostURN, _ := args["ugc_post_urn"].(string)
	if ugcPostURN == "" {
		return "", fmt.Errorf("'ugc_post_urn' is required")
	}

	payload := map[string]interface{}{
		"entityId": entityID,
		"appName":  "linkedin",
		"input": map[string]interface{}{
			"ugc_post_urn": ugcPostURN,
		},
	}

	return callComposioLinkedInAPI(composioAPIKey, entityID, "LINKEDIN_DELETE_UGC_POSTS", payload)
}

// NewComposioLinkedInGetCompanyInfoTool creates a tool for getting company information
func NewComposioLinkedInGetCompanyInfoTool() *Tool {
	return &Tool{
		Name:        "linkedin_get_company_info",
		DisplayName: "LinkedIn - Get Company Info",
		Description: `Get the companies and organizations where the user has admin or other roles on LinkedIn.

WHEN TO USE THIS TOOL:
- User wants to know which company pages they manage
- User asks "what LinkedIn pages do I admin" or needs to post as a company
- Need the organization URN to create posts on behalf of a company

PARAMETERS:
- role (optional): Filter by role. Example: "ADMINISTRATOR". Default: ADMINISTRATOR
- state (optional): Filter by state. Example: "APPROVED". Default: APPROVED
- count (optional): Number of results to return. Default: 10
- start (optional): Pagination offset. Default: 0

RETURNS: List of organizations with their URNs, names, and the user's role in each.`,
		Icon:     "linkedin",
		Source:   ToolSourceComposio,
		Category: "integration",
		Keywords: []string{"linkedin", "company", "organization", "info", "composio"},
		Parameters: map[string]interface{}{
			"type": "object",
			"properties": map[string]interface{}{
				"credential_id": map[string]interface{}{
					"type":        "string",
					"description": "INTERNAL: Auto-injected by system. Do not set manually.",
				},
				"role": map[string]interface{}{
					"type":        "string",
					"description": "Filter by role (default: ADMINISTRATOR)",
				},
				"state": map[string]interface{}{
					"type":        "string",
					"description": "Filter by state (default: APPROVED)",
				},
				"count": map[string]interface{}{
					"type":        "integer",
					"description": "Number of results to return (default: 10)",
				},
				"start": map[string]interface{}{
					"type":        "integer",
					"description": "Start index for pagination",
				},
			},
			"required": []string{},
		},
		Execute: executeComposioLinkedInGetCompanyInfo,
	}
}

func executeComposioLinkedInGetCompanyInfo(args map[string]interface{}) (string, error) {
	if err := checkLinkedInRateLimit(args); err != nil {
		return "", err
	}

	credData, err := GetCredentialData(args, "composio_linkedin")
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
	input := map[string]interface{}{}

	if role, ok := args["role"].(string); ok && role != "" {
		input["role"] = role
	}

	if state, ok := args["state"].(string); ok && state != "" {
		input["state"] = state
	}

	if count, ok := args["count"].(float64); ok {
		input["count"] = int(count)
	}

	if start, ok := args["start"].(float64); ok {
		input["start"] = int(start)
	}

	payload := map[string]interface{}{
		"entityId": entityID,
		"appName":  "linkedin",
		"input":    input,
	}

	return callComposioLinkedInAPI(composioAPIKey, entityID, "LINKEDIN_GET_COMPANY_INFO", payload)
}

// NewComposioLinkedInGetImagesTool creates a tool for getting images
func NewComposioLinkedInGetImagesTool() *Tool {
	return &Tool{
		Name:        "linkedin_get_images",
		DisplayName: "LinkedIn - Get Images",
		Description: `Get metadata for LinkedIn images including download URLs, dimensions, and processing status.

WHEN TO USE THIS TOOL:
- Need to get download URLs for images used in LinkedIn posts
- Want to check image processing status or dimensions

PARAMETERS:
- ids (REQUIRED): Array of image URNs to look up. Example: ["urn:li:image:abc123"]

RETURNS: Image metadata including download URLs, dimensions, and status for each image.`,
		Icon:     "linkedin",
		Source:   ToolSourceComposio,
		Category: "integration",
		Keywords: []string{"linkedin", "images", "media", "metadata", "composio"},
		Parameters: map[string]interface{}{
			"type": "object",
			"properties": map[string]interface{}{
				"credential_id": map[string]interface{}{
					"type":        "string",
					"description": "INTERNAL: Auto-injected by system. Do not set manually.",
				},
				"ids": map[string]interface{}{
					"type":        "array",
					"description": "Array of image URNs to retrieve",
					"items": map[string]interface{}{
						"type": "string",
					},
				},
			},
			"required": []string{"ids"},
		},
		Execute: executeComposioLinkedInGetImages,
	}
}

func executeComposioLinkedInGetImages(args map[string]interface{}) (string, error) {
	if err := checkLinkedInRateLimit(args); err != nil {
		return "", err
	}

	credData, err := GetCredentialData(args, "composio_linkedin")
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

	ids, ok := args["ids"].([]interface{})
	if !ok || len(ids) == 0 {
		return "", fmt.Errorf("'ids' is required and must be a non-empty array")
	}

	payload := map[string]interface{}{
		"entityId": entityID,
		"appName":  "linkedin",
		"input": map[string]interface{}{
			"ids": ids,
		},
	}

	return callComposioLinkedInAPI(composioAPIKey, entityID, "LINKEDIN_GET_IMAGES", payload)
}

// NewComposioLinkedInGetMyInfoTool creates a tool for getting user profile
func NewComposioLinkedInGetMyInfoTool() *Tool {
	return &Tool{
		Name:        "linkedin_get_my_info",
		DisplayName: "LinkedIn - Get My Info",
		Description: `Get the authenticated user's own LinkedIn profile information including name, headline, and profile picture.

WHEN TO USE THIS TOOL:
- User wants to see their LinkedIn profile info
- User asks "what's my LinkedIn profile" or "show my LinkedIn info"
- Need the user's LinkedIn URN for creating posts or comments

PARAMETERS: None required.

RETURNS: User profile data including name, headline, profile picture URL, and person URN.`,
		Icon:     "linkedin",
		Source:   ToolSourceComposio,
		Category: "integration",
		Keywords: []string{"linkedin", "profile", "user", "info", "composio"},
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
		Execute: executeComposioLinkedInGetMyInfo,
	}
}

func executeComposioLinkedInGetMyInfo(args map[string]interface{}) (string, error) {
	if err := checkLinkedInRateLimit(args); err != nil {
		return "", err
	}

	credData, err := GetCredentialData(args, "composio_linkedin")
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
		"appName":  "linkedin",
		"input":    map[string]interface{}{},
	}

	return callComposioLinkedInAPI(composioAPIKey, entityID, "LINKEDIN_GET_MY_INFO", payload)
}

// NewComposioLinkedInGetVideoTool creates a tool for getting video details
func NewComposioLinkedInGetVideoTool() *Tool {
	return &Tool{
		Name:        "linkedin_get_video",
		DisplayName: "LinkedIn - Get Video",
		Description: `Get details of a specific LinkedIn video by its URN, including status and download URL.

WHEN TO USE THIS TOOL:
- Need to check video processing status on LinkedIn
- Want to get a download URL for a LinkedIn video

PARAMETERS:
- video_urn (REQUIRED): The video URN to look up. Example: "urn:li:video:abc123"

RETURNS: Video metadata including status, download URL, duration, and dimensions.`,
		Icon:     "linkedin",
		Source:   ToolSourceComposio,
		Category: "integration",
		Keywords: []string{"linkedin", "video", "media", "details", "composio"},
		Parameters: map[string]interface{}{
			"type": "object",
			"properties": map[string]interface{}{
				"credential_id": map[string]interface{}{
					"type":        "string",
					"description": "INTERNAL: Auto-injected by system. Do not set manually.",
				},
				"video_urn": map[string]interface{}{
					"type":        "string",
					"description": "URN of the video to retrieve",
				},
			},
			"required": []string{"video_urn"},
		},
		Execute: executeComposioLinkedInGetVideo,
	}
}

func executeComposioLinkedInGetVideo(args map[string]interface{}) (string, error) {
	if err := checkLinkedInRateLimit(args); err != nil {
		return "", err
	}

	credData, err := GetCredentialData(args, "composio_linkedin")
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

	videoURN, _ := args["video_urn"].(string)
	if videoURN == "" {
		return "", fmt.Errorf("'video_urn' is required")
	}

	payload := map[string]interface{}{
		"entityId": entityID,
		"appName":  "linkedin",
		"input": map[string]interface{}{
			"video_urn": videoURN,
		},
	}

	return callComposioLinkedInAPI(composioAPIKey, entityID, "LINKEDIN_GET_VIDEO", payload)
}

// NewComposioLinkedInGetVideosTool creates a tool for getting videos with pagination
func NewComposioLinkedInGetVideosTool() *Tool {
	return &Tool{
		Name:        "linkedin_get_videos",
		DisplayName: "LinkedIn - Get Videos",
		Description: `List multiple LinkedIn videos with pagination and optional account filtering.

WHEN TO USE THIS TOOL:
- User wants to see all their LinkedIn videos
- Need to browse videos associated with a specific account

PARAMETERS:
- associated_account (optional): Filter by account URN. Example: "urn:li:organization:123"
- count (optional): Number of results to return.
- start (optional): Pagination offset.

RETURNS: List of videos with URNs, durations, dimensions, status, and download URLs.`,
		Icon:     "linkedin",
		Source:   ToolSourceComposio,
		Category: "integration",
		Keywords: []string{"linkedin", "videos", "media", "list", "pagination", "composio"},
		Parameters: map[string]interface{}{
			"type": "object",
			"properties": map[string]interface{}{
				"credential_id": map[string]interface{}{
					"type":        "string",
					"description": "INTERNAL: Auto-injected by system. Do not set manually.",
				},
				"associated_account": map[string]interface{}{
					"type":        "string",
					"description": "Filter by associated account URN",
				},
				"count": map[string]interface{}{
					"type":        "integer",
					"description": "Number of results to return",
				},
				"start": map[string]interface{}{
					"type":        "integer",
					"description": "Start index for pagination",
				},
			},
			"required": []string{},
		},
		Execute: executeComposioLinkedInGetVideos,
	}
}

func executeComposioLinkedInGetVideos(args map[string]interface{}) (string, error) {
	if err := checkLinkedInRateLimit(args); err != nil {
		return "", err
	}

	credData, err := GetCredentialData(args, "composio_linkedin")
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
	input := map[string]interface{}{}

	if associatedAccount, ok := args["associated_account"].(string); ok && associatedAccount != "" {
		input["associated_account"] = associatedAccount
	}

	if count, ok := args["count"].(float64); ok {
		input["count"] = int(count)
	}

	if start, ok := args["start"].(float64); ok {
		input["start"] = int(start)
	}

	payload := map[string]interface{}{
		"entityId": entityID,
		"appName":  "linkedin",
		"input":    input,
	}

	return callComposioLinkedInAPI(composioAPIKey, entityID, "LINKEDIN_GET_VIDEOS", payload)
}

// NewComposioLinkedInRegisterImageUploadTool creates a tool for registering image uploads
func NewComposioLinkedInRegisterImageUploadTool() *Tool {
	return &Tool{
		Name:        "linkedin_register_image_upload",
		DisplayName: "LinkedIn - Register Image Upload",
		Description: `Register an image upload with LinkedIn to get a presigned upload URL. This is step 1 of attaching an image to a LinkedIn post.

WHEN TO USE THIS TOOL:
- User wants to post an image on LinkedIn
- Need to attach a photo to a LinkedIn post (call this first, then upload, then create post)

PARAMETERS:
- owner_urn (REQUIRED): URN of the person or organization uploading. Example: "urn:li:person:abc123"

RETURNS: A presigned upload URL and a digital media asset URN. Upload image bytes to the URL, then use the asset URN when creating a post.`,
		Icon:     "linkedin",
		Source:   ToolSourceComposio,
		Category: "integration",
		Keywords: []string{"linkedin", "image", "upload", "media", "composio"},
		Parameters: map[string]interface{}{
			"type": "object",
			"properties": map[string]interface{}{
				"credential_id": map[string]interface{}{
					"type":        "string",
					"description": "INTERNAL: Auto-injected by system. Do not set manually.",
				},
				"owner_urn": map[string]interface{}{
					"type":        "string",
					"description": "URN of the owner (person or organization)",
				},
			},
			"required": []string{"owner_urn"},
		},
		Execute: executeComposioLinkedInRegisterImageUpload,
	}
}

func executeComposioLinkedInRegisterImageUpload(args map[string]interface{}) (string, error) {
	if err := checkLinkedInRateLimit(args); err != nil {
		return "", err
	}

	credData, err := GetCredentialData(args, "composio_linkedin")
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

	ownerURN, _ := args["owner_urn"].(string)
	if ownerURN == "" {
		return "", fmt.Errorf("'owner_urn' is required")
	}

	payload := map[string]interface{}{
		"entityId": entityID,
		"appName":  "linkedin",
		"input": map[string]interface{}{
			"owner_urn": ownerURN,
		},
	}

	return callComposioLinkedInAPI(composioAPIKey, entityID, "LINKEDIN_REGISTER_IMAGE_UPLOAD", payload)
}

// callComposioLinkedInAPI makes a v2 API call to Composio for LinkedIn actions
func callComposioLinkedInAPI(apiKey string, entityID string, action string, payload map[string]interface{}) (string, error) {
	// Get connected account ID
	connectedAccountID, err := getLinkedInConnectedAccountID(apiKey, entityID, "linkedin")
	if err != nil {
		return "", fmt.Errorf("failed to get connected account: %w", err)
	}

	url := "https://backend.composio.dev/api/v3/tools/execute/" + action

	v2Payload := map[string]interface{}{
		"connected_account_id": connectedAccountID,
		"user_id":              entityID,
		"arguments":            payload["input"],
	}

	jsonData, err := json.Marshal(v2Payload)
	if err != nil {
		return "", fmt.Errorf("failed to marshal request: %w", err)
	}

	log.Printf("🔍 [LINKEDIN] Action: %s, ConnectedAccount: %s", action, maskSensitiveLinkedInID(connectedAccountID))

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

	// Parse and log rate limit headers
	parseLinkedInRateLimitHeaders(resp.Header, action)

	respBody, _ := io.ReadAll(resp.Body)

	if resp.StatusCode >= 400 {
		log.Printf("❌ [LINKEDIN] API error (status %d) for action %s", resp.StatusCode, action)
		log.Printf("❌ [LINKEDIN] Composio error response: %s", string(respBody))

		// Handle rate limiting from LinkedIn API
		if resp.StatusCode == 429 {
			// Parse LinkedIn's error response
			var errorResp struct {
				Data struct {
					Message       string `json:"message"`
					StatusCode    int    `json:"status_code"`
					HTTPError     string `json:"http_error"`
					ServiceErrCode int   `json:"serviceErrorCode"`
				} `json:"data"`
				Error string `json:"error"`
			}

			if err := json.Unmarshal(respBody, &errorResp); err == nil {
				// LinkedIn's APPLICATION DAY limit reached
				if errorResp.Data.ServiceErrCode == 101 {
					log.Printf("🚨 [LINKEDIN] Daily application limit reached for this LinkedIn app")
					return "", fmt.Errorf("LinkedIn daily limit reached. This is a shared limit across all users. Please try again tomorrow or contact support to increase limits")
				}

				// LinkedIn's user/resource limit
				if errorResp.Data.HTTPError != "" {
					log.Printf("⚠️ [LINKEDIN] LinkedIn API limit: %s", errorResp.Data.HTTPError)
					return "", fmt.Errorf("LinkedIn API limit: %s. Please reduce usage or try again later", errorResp.Data.HTTPError)
				}
			}

			// Generic rate limit handling
			retryAfter := resp.Header.Get("Retry-After")
			if retryAfter != "" {
				log.Printf("⚠️ [LINKEDIN] Rate limited, retry after: %s seconds", retryAfter)
				return "", fmt.Errorf("LinkedIn rate limit exceeded. Retry after %s seconds", retryAfter)
			}
			return "", fmt.Errorf("LinkedIn rate limit exceeded. Please try again later")
		}

		// Handle specific errors
		if resp.StatusCode == 403 {
			return "", fmt.Errorf("permission denied: ensure you have access to the requested resource")
		}
		if resp.StatusCode == 404 {
			return "", fmt.Errorf("resource not found: post, company, or media does not exist")
		}
		if resp.StatusCode == 413 {
			return "", fmt.Errorf("content too large: reduce post text or media size")
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

// getLinkedInConnectedAccountID retrieves the connected account ID from Composio v3 API
func getLinkedInConnectedAccountID(apiKey string, userID string, appName string) (string, error) {
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
			if account.Deprecated.UUID != "" {
				return account.ID, nil
			}
			// Fall back to nano ID if UUID not available
			return account.ID, nil
		}
	}

	return "", fmt.Errorf("no %s connection found for user. Please connect your LinkedIn account first", appName)
}

// parseLinkedInRateLimitHeaders parses and logs rate limit information from response headers
func parseLinkedInRateLimitHeaders(headers http.Header, action string) {
	limit := headers.Get("X-RateLimit-Limit")
	remaining := headers.Get("X-RateLimit-Remaining")
	reset := headers.Get("X-RateLimit-Reset")

	if limit != "" || remaining != "" || reset != "" {
		log.Printf("📊 [LINKEDIN] Rate limits for %s - Limit: %s, Remaining: %s, Reset: %s",
			action, limit, remaining, reset)

		// Warning if approaching rate limit
		if remaining != "" && limit != "" {
			remainingInt := 0
			limitInt := 0
			fmt.Sscanf(remaining, "%d", &remainingInt)
			fmt.Sscanf(limit, "%d", &limitInt)

			if limitInt > 0 && remainingInt < limitInt/10 {
				log.Printf("⚠️ [LINKEDIN] Approaching rate limit: %d/%d remaining", remainingInt, limitInt)
			}
		}
	}
}

// maskSensitiveLinkedInID masks a sensitive ID for safe logging
func maskSensitiveLinkedInID(id string) string {
	if len(id) <= 8 {
		return "***"
	}
	return id[:4] + "..." + id[len(id)-4:]
}
