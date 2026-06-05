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

// composioTwitterRateLimiter implements per-user rate limiting for Composio Twitter API calls
type composioTwitterRateLimiter struct {
	requests map[string][]time.Time
	mutex    sync.RWMutex
	maxCalls int
	window   time.Duration
}

var globalTwitterRateLimiter = &composioTwitterRateLimiter{
	requests: make(map[string][]time.Time),
	maxCalls: 25, // Twitter has stricter rate limits
	window:   1 * time.Minute,
}

func checkTwitterRateLimit(args map[string]interface{}) error {
	userID, ok := args["__user_id__"].(string)
	if !ok || userID == "" {
		log.Printf("⚠️ [TWITTER] No user ID for rate limiting")
		return nil
	}

	globalTwitterRateLimiter.mutex.Lock()
	defer globalTwitterRateLimiter.mutex.Unlock()

	now := time.Now()
	windowStart := now.Add(-globalTwitterRateLimiter.window)

	timestamps := globalTwitterRateLimiter.requests[userID]
	validTimestamps := []time.Time{}
	for _, ts := range timestamps {
		if ts.After(windowStart) {
			validTimestamps = append(validTimestamps, ts)
		}
	}

	if len(validTimestamps) >= globalTwitterRateLimiter.maxCalls {
		return fmt.Errorf("rate limit exceeded: max %d requests per minute", globalTwitterRateLimiter.maxCalls)
	}

	validTimestamps = append(validTimestamps, now)
	globalTwitterRateLimiter.requests[userID] = validTimestamps
	return nil
}

// NewTwitterPostTweetTool creates a tool for posting tweets
func NewTwitterPostTweetTool() *Tool {
	return &Tool{
		Name:        "twitter_post_tweet",
		DisplayName: "Twitter/X - Post Tweet",
		Description: `Post a new tweet (post) to the user's Twitter/X account.

WHEN TO USE THIS TOOL:
- User wants to tweet something or post on Twitter/X
- User says "post this on Twitter" or "tweet this"
- User wants to share a message on X/Twitter

PARAMETERS:
- text (REQUIRED): The tweet text, max 280 characters. Example: "Excited to announce our new product launch!"
- reply_to_tweet_id (optional): Tweet ID to reply to. Example: "1234567890"

RETURNS: The created tweet data including tweet ID and text.`,
		Icon:     "Twitter",
		Source:   ToolSourceComposio,
		Category: "integration",
		Keywords: []string{"twitter", "x", "tweet", "post", "social", "composio"},
		Parameters: map[string]interface{}{
			"type": "object",
			"properties": map[string]interface{}{
				"credential_id": map[string]interface{}{
					"type":        "string",
					"description": "INTERNAL: Auto-injected by system. Do not set manually.",
				},
				"text": map[string]interface{}{
					"type":        "string",
					"description": "The text content of the tweet (max 280 characters)",
				},
				"reply_to_tweet_id": map[string]interface{}{
					"type":        "string",
					"description": "ID of tweet to reply to (optional)",
				},
			},
			"required": []string{"text"},
		},
		Execute: executeTwitterPostTweet,
	}
}

func executeTwitterPostTweet(args map[string]interface{}) (string, error) {
	if err := checkTwitterRateLimit(args); err != nil {
		return "", err
	}

	credData, err := GetCredentialData(args, "composio_twitter")
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
	if replyTo, ok := args["reply_to_tweet_id"].(string); ok {
		input["reply_to_tweet_id"] = replyTo
	}

	return callComposioTwitterAPI(composioAPIKey, entityID, "TWITTER_CREATION_OF_A_POST", input)
}

// NewTwitterGetTimelineTool creates a tool for getting user timeline
func NewTwitterGetTimelineTool() *Tool {
	return &Tool{
		Name:        "twitter_get_timeline",
		DisplayName: "Twitter/X - Get Timeline",
		Description: `Fetch recent tweets from the user's Twitter/X home timeline (their feed).

WHEN TO USE THIS TOOL:
- User wants to see their Twitter feed or timeline
- User asks "what's on my Twitter" or "show my timeline"
- User wants to see recent tweets from people they follow

PARAMETERS:
- max_results (optional): Number of tweets to return, 1-100. Default: 10. Example: 20

RETURNS: List of recent tweets from the user's timeline with tweet text, author, and metadata.`,
		Icon:     "List",
		Source:   ToolSourceComposio,
		Category: "integration",
		Keywords: []string{"twitter", "x", "timeline", "feed", "composio"},
		Parameters: map[string]interface{}{
			"type": "object",
			"properties": map[string]interface{}{
				"credential_id": map[string]interface{}{
					"type":        "string",
					"description": "INTERNAL: Auto-injected by system. Do not set manually.",
				},
				"max_results": map[string]interface{}{
					"type":        "integer",
					"description": "Maximum number of tweets to return (default: 10, max: 100)",
				},
			},
			"required": []string{},
		},
		Execute: executeTwitterGetTimeline,
	}
}

func executeTwitterGetTimeline(args map[string]interface{}) (string, error) {
	if err := checkTwitterRateLimit(args); err != nil {
		return "", err
	}

	credData, err := GetCredentialData(args, "composio_twitter")
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

	if maxResults, ok := args["max_results"].(float64); ok {
		input["max_results"] = int(maxResults)
	}

	return callComposioTwitterAPI(composioAPIKey, entityID, "TWITTER_USER_HOME_TIMELINE_BY_USER_ID", input)
}

// NewTwitterSearchTweetsTool creates a tool for searching tweets
func NewTwitterSearchTweetsTool() *Tool {
	return &Tool{
		Name:        "twitter_search_tweets",
		DisplayName: "Twitter/X - Search Tweets",
		Description: `Search for tweets on Twitter/X by keyword or topic. Only returns tweets from the past 7 days.

WHEN TO USE THIS TOOL:
- User wants to find tweets about a topic
- User says "search Twitter for..." or "find tweets about..."
- User wants to see what people are saying about something on X

PARAMETERS:
- query (REQUIRED): Search keywords. Supports Twitter search operators. Example: "artificial intelligence" or "from:elonmusk"
- max_results (optional): Number of results, 1-100. Default: 10. Example: 25

RETURNS: List of matching tweets with text, author, date, and engagement metrics.`,
		Icon:     "Search",
		Source:   ToolSourceComposio,
		Category: "integration",
		Keywords: []string{"twitter", "x", "search", "find", "tweets", "composio"},
		Parameters: map[string]interface{}{
			"type": "object",
			"properties": map[string]interface{}{
				"credential_id": map[string]interface{}{
					"type":        "string",
					"description": "INTERNAL: Auto-injected by system. Do not set manually.",
				},
				"query": map[string]interface{}{
					"type":        "string",
					"description": "Search query (supports Twitter search operators)",
				},
				"max_results": map[string]interface{}{
					"type":        "integer",
					"description": "Maximum number of results (default: 10, max: 100)",
				},
			},
			"required": []string{"query"},
		},
		Execute: executeTwitterSearchTweets,
	}
}

func executeTwitterSearchTweets(args map[string]interface{}) (string, error) {
	if err := checkTwitterRateLimit(args); err != nil {
		return "", err
	}

	credData, err := GetCredentialData(args, "composio_twitter")
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
	if maxResults, ok := args["max_results"].(float64); ok {
		input["max_results"] = int(maxResults)
	}

	return callComposioTwitterAPI(composioAPIKey, entityID, "TWITTER_RECENT_SEARCH", input)
}

// NewTwitterGetUserTool creates a tool for getting user info
func NewTwitterGetUserTool() *Tool {
	return &Tool{
		Name:        "twitter_get_user",
		DisplayName: "Twitter/X - Get User",
		Description: `Look up a Twitter/X user's profile information by their username.

WHEN TO USE THIS TOOL:
- User wants to see someone's Twitter profile
- User asks "who is @username on Twitter"
- User wants follower count, bio, or profile info for a Twitter account

PARAMETERS:
- username (REQUIRED): Twitter handle without the @ symbol. Example: "elonmusk"

RETURNS: User profile data including name, bio, follower/following counts, and profile image.`,
		Icon:     "User",
		Source:   ToolSourceComposio,
		Category: "integration",
		Keywords: []string{"twitter", "x", "user", "profile", "composio"},
		Parameters: map[string]interface{}{
			"type": "object",
			"properties": map[string]interface{}{
				"credential_id": map[string]interface{}{
					"type":        "string",
					"description": "INTERNAL: Auto-injected by system. Do not set manually.",
				},
				"username": map[string]interface{}{
					"type":        "string",
					"description": "Twitter username (without @)",
				},
			},
			"required": []string{"username"},
		},
		Execute: executeTwitterGetUser,
	}
}

func executeTwitterGetUser(args map[string]interface{}) (string, error) {
	if err := checkTwitterRateLimit(args); err != nil {
		return "", err
	}

	credData, err := GetCredentialData(args, "composio_twitter")
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

	if username, ok := args["username"].(string); ok {
		input["username"] = username
	}

	return callComposioTwitterAPI(composioAPIKey, entityID, "TWITTER_USER_LOOKUP_BY_USERNAME", input)
}

// NewTwitterGetMeTool creates a tool for getting authenticated user info
func NewTwitterGetMeTool() *Tool {
	return &Tool{
		Name:        "twitter_get_me",
		DisplayName: "Twitter/X - Get My Profile",
		Description: `Get the authenticated user's own Twitter/X profile information.

NOTE: This action is currently unavailable. Use 'Twitter/X - Get User' with your own username instead.

WHEN TO USE THIS TOOL:
- User wants to see their own Twitter profile
- User asks "show my Twitter account info"

RETURNS: Error directing user to use the Get User tool with their username.`,
		Icon:     "User",
		Source:   ToolSourceComposio,
		Category: "integration",
		Keywords: []string{"twitter", "x", "me", "profile", "account", "composio"},
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
		Execute: executeTwitterGetMe,
	}
}

func executeTwitterGetMe(args map[string]interface{}) (string, error) {
	// TWITTER_GET_AUTHENTICATED_USER has been removed from Composio.
	// Use TWITTER_USER_LOOKUP_BY_USERNAME as a workaround if the username is known.
	return "", fmt.Errorf("the 'Get My Profile' action is currently unavailable via Composio. Use the 'Get User' tool with your username instead")
}

// NewTwitterLikeTweetTool creates a tool for liking tweets
func NewTwitterLikeTweetTool() *Tool {
	return &Tool{
		Name:        "twitter_like_tweet",
		DisplayName: "Twitter/X - Like Tweet",
		Description: `Like (favorite) a specific tweet on Twitter/X.

WHEN TO USE THIS TOOL:
- User wants to like a tweet
- User says "like this tweet" or "favorite that post"

PARAMETERS:
- tweet_id (REQUIRED): The numeric ID of the tweet to like. Example: "1234567890123456789"

RETURNS: Confirmation that the tweet was liked successfully.`,
		Icon:     "Heart",
		Source:   ToolSourceComposio,
		Category: "integration",
		Keywords: []string{"twitter", "x", "like", "favorite", "composio"},
		Parameters: map[string]interface{}{
			"type": "object",
			"properties": map[string]interface{}{
				"credential_id": map[string]interface{}{
					"type":        "string",
					"description": "INTERNAL: Auto-injected by system. Do not set manually.",
				},
				"tweet_id": map[string]interface{}{
					"type":        "string",
					"description": "ID of the tweet to like",
				},
			},
			"required": []string{"tweet_id"},
		},
		Execute: executeTwitterLikeTweet,
	}
}

func executeTwitterLikeTweet(args map[string]interface{}) (string, error) {
	if err := checkTwitterRateLimit(args); err != nil {
		return "", err
	}

	credData, err := GetCredentialData(args, "composio_twitter")
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

	if tweetID, ok := args["tweet_id"].(string); ok {
		input["tweet_id"] = tweetID
	}

	return callComposioTwitterAPI(composioAPIKey, entityID, "TWITTER_USER_LIKE_POST", input)
}

// NewTwitterRetweetTool creates a tool for retweeting
func NewTwitterRetweetTool() *Tool {
	return &Tool{
		Name:        "twitter_retweet",
		DisplayName: "Twitter/X - Retweet",
		Description: `Retweet (repost) a tweet on Twitter/X to share it with your followers.

WHEN TO USE THIS TOOL:
- User wants to retweet or repost a tweet
- User says "retweet this" or "share this tweet"

PARAMETERS:
- tweet_id (REQUIRED): The numeric ID of the tweet to retweet. Example: "1234567890123456789"

RETURNS: Confirmation that the tweet was retweeted successfully.`,
		Icon:     "Repeat",
		Source:   ToolSourceComposio,
		Category: "integration",
		Keywords: []string{"twitter", "x", "retweet", "share", "composio"},
		Parameters: map[string]interface{}{
			"type": "object",
			"properties": map[string]interface{}{
				"credential_id": map[string]interface{}{
					"type":        "string",
					"description": "INTERNAL: Auto-injected by system. Do not set manually.",
				},
				"tweet_id": map[string]interface{}{
					"type":        "string",
					"description": "ID of the tweet to retweet",
				},
			},
			"required": []string{"tweet_id"},
		},
		Execute: executeTwitterRetweet,
	}
}

func executeTwitterRetweet(args map[string]interface{}) (string, error) {
	if err := checkTwitterRateLimit(args); err != nil {
		return "", err
	}

	credData, err := GetCredentialData(args, "composio_twitter")
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

	if tweetID, ok := args["tweet_id"].(string); ok {
		input["tweet_id"] = tweetID
	}

	return callComposioTwitterAPI(composioAPIKey, entityID, "TWITTER_RETWEET_POST", input)
}

// NewTwitterDeleteTweetTool creates a tool for deleting tweets
func NewTwitterDeleteTweetTool() *Tool {
	return &Tool{
		Name:        "twitter_delete_tweet",
		DisplayName: "Twitter/X - Delete Tweet",
		Description: `Permanently delete a tweet from the user's Twitter/X account. This action cannot be undone.

WHEN TO USE THIS TOOL:
- User wants to delete one of their tweets
- User says "remove my tweet" or "delete that post"

PARAMETERS:
- tweet_id (REQUIRED): The numeric ID of the tweet to delete. Example: "1234567890123456789"

RETURNS: Confirmation that the tweet was deleted. WARNING: This is permanent and cannot be undone.`,
		Icon:     "Trash",
		Source:   ToolSourceComposio,
		Category: "integration",
		Keywords: []string{"twitter", "x", "delete", "remove", "composio"},
		Parameters: map[string]interface{}{
			"type": "object",
			"properties": map[string]interface{}{
				"credential_id": map[string]interface{}{
					"type":        "string",
					"description": "INTERNAL: Auto-injected by system. Do not set manually.",
				},
				"tweet_id": map[string]interface{}{
					"type":        "string",
					"description": "ID of the tweet to delete",
				},
			},
			"required": []string{"tweet_id"},
		},
		Execute: executeTwitterDeleteTweet,
	}
}

func executeTwitterDeleteTweet(args map[string]interface{}) (string, error) {
	if err := checkTwitterRateLimit(args); err != nil {
		return "", err
	}

	credData, err := GetCredentialData(args, "composio_twitter")
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

	if tweetID, ok := args["tweet_id"].(string); ok {
		input["tweet_id"] = tweetID
	}

	return callComposioTwitterAPI(composioAPIKey, entityID, "TWITTER_POST_DELETE_BY_POST_ID", input)
}

// callComposioTwitterAPI makes a v2 API call to Composio for Twitter actions
func callComposioTwitterAPI(apiKey string, entityID string, action string, input map[string]interface{}) (string, error) {
	connectedAccountID, err := getTwitterConnectedAccountID(apiKey, entityID, "twitter")
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

	log.Printf("🐦 [TWITTER] Action: %s, ConnectedAccount: %s", action, maskSensitiveID(connectedAccountID))

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
		log.Printf("❌ [TWITTER] API error (status %d) for action %s: %s", resp.StatusCode, action, string(respBody))
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

// getTwitterConnectedAccountID retrieves the connected account ID from Composio v3 API
func getTwitterConnectedAccountID(apiKey string, userID string, appName string) (string, error) {
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

	return "", fmt.Errorf("no %s connection found for user. Please connect your Twitter account first", appName)
}
