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

// composioYouTubeRateLimiter implements per-user rate limiting for Composio YouTube API calls
type composioYouTubeRateLimiter struct {
	requests map[string][]time.Time
	mutex    sync.RWMutex
	maxCalls int
	window   time.Duration
}

var globalYouTubeRateLimiter = &composioYouTubeRateLimiter{
	requests: make(map[string][]time.Time),
	maxCalls: 30,
	window:   1 * time.Minute,
}

func checkYouTubeRateLimit(args map[string]interface{}) error {
	userID, ok := args["__user_id__"].(string)
	if !ok || userID == "" {
		log.Printf("⚠️ [YOUTUBE] No user ID for rate limiting")
		return nil
	}

	globalYouTubeRateLimiter.mutex.Lock()
	defer globalYouTubeRateLimiter.mutex.Unlock()

	now := time.Now()
	windowStart := now.Add(-globalYouTubeRateLimiter.window)

	timestamps := globalYouTubeRateLimiter.requests[userID]
	validTimestamps := []time.Time{}
	for _, ts := range timestamps {
		if ts.After(windowStart) {
			validTimestamps = append(validTimestamps, ts)
		}
	}

	if len(validTimestamps) >= globalYouTubeRateLimiter.maxCalls {
		return fmt.Errorf("rate limit exceeded: max %d requests per minute", globalYouTubeRateLimiter.maxCalls)
	}

	validTimestamps = append(validTimestamps, now)
	globalYouTubeRateLimiter.requests[userID] = validTimestamps
	return nil
}

// NewYouTubeSearchVideosTool creates a tool for searching YouTube videos
func NewYouTubeSearchVideosTool() *Tool {
	return &Tool{
		Name:        "youtube_search_videos",
		DisplayName: "YouTube - Search Videos",
		Description: `Search for videos on YouTube by keyword or phrase. Returns a list of matching videos with their titles, descriptions, channel names, and video IDs.

WHEN TO USE THIS TOOL:
- The user wants to find YouTube videos about a topic (e.g., "find videos about machine learning")
- The user wants to discover content on YouTube
- The user asks "search YouTube for..." or "find videos about..."

PARAMETERS:
- query (REQUIRED): The search text, e.g., "how to cook pasta" or "machine learning tutorial"
- max_results (optional): How many videos to return (default: 5, max: 50)
- type (optional): Filter by resource type: "video", "channel", or "playlist" (default: "video")

RETURNS: A list of videos with video IDs, titles, descriptions, channel names, and publish dates.`,
		Icon:     "Youtube",
		Source:   ToolSourceComposio,
		Category: "integration",
		Keywords: []string{"youtube", "video", "search", "find", "composio"},
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
				"max_results": map[string]interface{}{
					"type":        "integer",
					"description": "Maximum number of results (default: 5, max: 50)",
				},
				"type": map[string]interface{}{
					"type":        "string",
					"description": "Filter by resource type: 'video', 'channel', or 'playlist'",
					"enum":        []string{"video", "channel", "playlist"},
				},
			},
			"required": []string{"query"},
		},
		Execute: executeYouTubeSearchVideos,
	}
}

func executeYouTubeSearchVideos(args map[string]interface{}) (string, error) {
	if err := checkYouTubeRateLimit(args); err != nil {
		return "", err
	}

	credData, err := GetCredentialData(args, "composio_youtube")
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

	if query, ok := args["query"].(string); ok && query != "" {
		input["q"] = query
	} else {
		return "", fmt.Errorf("query parameter is required for YouTube search")
	}
	if maxResults, ok := args["max_results"].(float64); ok {
		input["maxResults"] = int(maxResults)
	}
	if searchType, ok := args["type"].(string); ok {
		input["type"] = searchType
	}

	return callComposioYouTubeAPI(composioAPIKey, entityID, "YOUTUBE_SEARCH_YOU_TUBE", input)
}

// NewYouTubeGetVideoTool creates a tool for getting video details
func NewYouTubeGetVideoTool() *Tool {
	return &Tool{
		Name:        "youtube_get_video",
		DisplayName: "YouTube - Get Video Details",
		Description: `Get detailed information about a specific YouTube video using its video ID. Returns the video's title, description, view count, like count, channel info, publish date, and more.

WHEN TO USE THIS TOOL:
- The user wants details about a specific video (e.g., "tell me about this video: dQw4w9WgXcQ")
- The user wants to check view count, likes, or description of a video
- You already have a video ID and need its metadata

PARAMETERS:
- video_id (REQUIRED): The YouTube video ID from the URL. For example, if the URL is youtube.com/watch?v=dQw4w9WgXcQ, the video_id is "dQw4w9WgXcQ"

RETURNS: Video title, description, channel name, view count, like count, publish date, and other metadata.`,
		Icon:     "Video",
		Source:   ToolSourceComposio,
		Category: "integration",
		Keywords: []string{"youtube", "video", "details", "info", "composio"},
		Parameters: map[string]interface{}{
			"type": "object",
			"properties": map[string]interface{}{
				"credential_id": map[string]interface{}{
					"type":        "string",
					"description": "INTERNAL: Auto-injected by system. Do not set manually.",
				},
				"video_id": map[string]interface{}{
					"type":        "string",
					"description": "YouTube video ID (from URL: youtube.com/watch?v=VIDEO_ID)",
				},
			},
			"required": []string{"video_id"},
		},
		Execute: executeYouTubeGetVideo,
	}
}

func executeYouTubeGetVideo(args map[string]interface{}) (string, error) {
	if err := checkYouTubeRateLimit(args); err != nil {
		return "", err
	}

	credData, err := GetCredentialData(args, "composio_youtube")
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

	if videoID, ok := args["video_id"].(string); ok && videoID != "" {
		input["id"] = videoID
	} else {
		return "", fmt.Errorf("video_id parameter is required for YouTube video details")
	}

	return callComposioYouTubeAPI(composioAPIKey, entityID, "YOUTUBE_VIDEO_DETAILS", input)
}

// NewYouTubeGetChannelTool creates a tool for getting channel details
func NewYouTubeGetChannelTool() *Tool {
	return &Tool{
		Name:        "youtube_get_channel",
		DisplayName: "YouTube - Get Channel",
		Description: `Get statistics and information about a YouTube channel using its channel ID. Returns subscriber count, total view count, video count, and channel description.

WHEN TO USE THIS TOOL:
- The user wants info about a specific YouTube channel (e.g., "how many subscribers does this channel have?")
- The user provides a channel ID and wants its stats
- You need to look up channel metrics like subscriber count or total views

PARAMETERS:
- channel_id (REQUIRED): The YouTube channel ID (e.g., "UCxxxxxx"). This is NOT the channel name or handle.

RETURNS: Channel name, subscriber count, total view count, video count, and description.`,
		Icon:     "User",
		Source:   ToolSourceComposio,
		Category: "integration",
		Keywords: []string{"youtube", "channel", "info", "composio"},
		Parameters: map[string]interface{}{
			"type": "object",
			"properties": map[string]interface{}{
				"credential_id": map[string]interface{}{
					"type":        "string",
					"description": "INTERNAL: Auto-injected by system. Do not set manually.",
				},
				"channel_id": map[string]interface{}{
					"type":        "string",
					"description": "YouTube channel ID",
				},
			},
			"required": []string{"channel_id"},
		},
		Execute: executeYouTubeGetChannel,
	}
}

func executeYouTubeGetChannel(args map[string]interface{}) (string, error) {
	if err := checkYouTubeRateLimit(args); err != nil {
		return "", err
	}

	credData, err := GetCredentialData(args, "composio_youtube")
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

	if channelID, ok := args["channel_id"].(string); ok && channelID != "" {
		input["id"] = channelID
	} else {
		return "", fmt.Errorf("channel_id parameter is required for YouTube channel statistics")
	}

	return callComposioYouTubeAPI(composioAPIKey, entityID, "YOUTUBE_GET_CHANNEL_STATISTICS", input)
}

// NewYouTubeGetMyChannelTool creates a tool for getting authenticated user's channel
func NewYouTubeGetMyChannelTool() *Tool {
	return &Tool{
		Name:        "youtube_get_my_channel",
		DisplayName: "YouTube - Get My Channel",
		Description: `Get information about the currently authenticated user's own YouTube channel. Returns the user's channel statistics including subscriber count, view count, and video count. No channel ID is needed - it automatically uses the connected YouTube account.

WHEN TO USE THIS TOOL:
- The user asks "what's my channel info?" or "show me my YouTube stats"
- The user wants to see their own subscriber count, view count, or video count
- You need the authenticated user's channel details without knowing their channel ID

PARAMETERS: None required. Automatically uses the authenticated user's channel.

RETURNS: The user's own channel name, subscriber count, total view count, and video count.`,
		Icon:     "User",
		Source:   ToolSourceComposio,
		Category: "integration",
		Keywords: []string{"youtube", "my", "channel", "account", "composio"},
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
		Execute: executeYouTubeGetMyChannel,
	}
}

func executeYouTubeGetMyChannel(args map[string]interface{}) (string, error) {
	if err := checkYouTubeRateLimit(args); err != nil {
		return "", err
	}

	credData, err := GetCredentialData(args, "composio_youtube")
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

	// YOUTUBE_LIST_CHANNEL_VIDEOS with the authenticated user's context
	// Composio's connected account handles authentication, so we can list the user's subscriptions
	// to get their channel info
	return callComposioYouTubeAPI(composioAPIKey, entityID, "YOUTUBE_LIST_USER_SUBSCRIPTIONS", input)
}

// NewYouTubeListPlaylistsTool creates a tool for listing playlists
func NewYouTubeListPlaylistsTool() *Tool {
	return &Tool{
		Name:        "youtube_list_playlists",
		DisplayName: "YouTube - List Playlists",
		Description: `List playlists owned by the authenticated YouTube user. Returns playlist names, IDs, and descriptions. If a channel_id is provided, lists that channel's playlists instead.

WHEN TO USE THIS TOOL:
- The user asks "show me my playlists" or "what playlists do I have?"
- The user wants to browse playlists before viewing their contents
- You need playlist IDs to use with the "Get Playlist Videos" tool

PARAMETERS:
- max_results (optional): How many playlists to return (default: 10)

RETURNS: List of playlists with their IDs, titles, and descriptions.`,
		Icon:     "List",
		Source:   ToolSourceComposio,
		Category: "integration",
		Keywords: []string{"youtube", "playlist", "list", "composio"},
		Parameters: map[string]interface{}{
			"type": "object",
			"properties": map[string]interface{}{
				"credential_id": map[string]interface{}{
					"type":        "string",
					"description": "INTERNAL: Auto-injected by system. Do not set manually.",
				},
				"max_results": map[string]interface{}{
					"type":        "integer",
					"description": "Maximum number of results (default: 10)",
				},
			},
			"required": []string{},
		},
		Execute: executeYouTubeListPlaylists,
	}
}

func executeYouTubeListPlaylists(args map[string]interface{}) (string, error) {
	if err := checkYouTubeRateLimit(args); err != nil {
		return "", err
	}

	credData, err := GetCredentialData(args, "composio_youtube")
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
		input["maxResults"] = int(maxResults)
	}

	return callComposioYouTubeAPI(composioAPIKey, entityID, "YOUTUBE_LIST_USER_PLAYLISTS", input)
}

// NewYouTubeGetPlaylistItemsTool creates a tool for getting videos in a playlist
func NewYouTubeGetPlaylistItemsTool() *Tool {
	return &Tool{
		Name:        "youtube_get_playlist_items",
		DisplayName: "YouTube - Get Playlist Videos",
		Description: `Get the list of videos inside a specific YouTube playlist. Returns video titles, IDs, descriptions, and positions within the playlist. You need the playlist ID (get it from the "List Playlists" tool or from a YouTube playlist URL).

WHEN TO USE THIS TOOL:
- The user asks "what videos are in this playlist?"
- The user wants to see the contents of a specific playlist
- You have a playlist ID and need to enumerate its videos

PARAMETERS:
- playlist_id (REQUIRED): The YouTube playlist ID (e.g., "PLxxxxxx"). Found in playlist URLs or from the "List Playlists" tool.
- max_results (optional): How many videos to return (default: 10)

RETURNS: List of videos in the playlist with their video IDs, titles, descriptions, and positions.`,
		Icon:     "PlaySquare",
		Source:   ToolSourceComposio,
		Category: "integration",
		Keywords: []string{"youtube", "playlist", "videos", "items", "composio"},
		Parameters: map[string]interface{}{
			"type": "object",
			"properties": map[string]interface{}{
				"credential_id": map[string]interface{}{
					"type":        "string",
					"description": "INTERNAL: Auto-injected by system. Do not set manually.",
				},
				"playlist_id": map[string]interface{}{
					"type":        "string",
					"description": "ID of the playlist",
				},
				"max_results": map[string]interface{}{
					"type":        "integer",
					"description": "Maximum number of results (default: 10)",
				},
			},
			"required": []string{"playlist_id"},
		},
		Execute: executeYouTubeGetPlaylistItems,
	}
}

func executeYouTubeGetPlaylistItems(args map[string]interface{}) (string, error) {
	if err := checkYouTubeRateLimit(args); err != nil {
		return "", err
	}

	credData, err := GetCredentialData(args, "composio_youtube")
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

	if playlistID, ok := args["playlist_id"].(string); ok && playlistID != "" {
		input["playlistId"] = playlistID
	} else {
		return "", fmt.Errorf("playlist_id parameter is required for YouTube playlist items")
	}
	if maxResults, ok := args["max_results"].(float64); ok {
		input["maxResults"] = int(maxResults)
	}

	return callComposioYouTubeAPI(composioAPIKey, entityID, "YOUTUBE_LIST_PLAYLIST_ITEMS", input)
}

// NewYouTubeGetCommentsTool creates a tool for getting video comments
func NewYouTubeGetCommentsTool() *Tool {
	return &Tool{
		Name:        "youtube_get_comments",
		DisplayName: "YouTube - Get Comments",
		Description: `Get comments (comment threads) on a specific YouTube video. Returns top-level comments with their text, author names, like counts, and reply counts.

WHEN TO USE THIS TOOL:
- The user asks "what are people saying about this video?" or "show me the comments"
- The user wants to read comments on a specific YouTube video
- You need to analyze sentiment or feedback from video comments

PARAMETERS:
- video_id (REQUIRED): The YouTube video ID to get comments from (e.g., "dQw4w9WgXcQ")
- max_results (optional): How many comments to return (default: 20)

RETURNS: List of comment threads with comment text, author display names, like counts, and number of replies.`,
		Icon:     "MessageSquare",
		Source:   ToolSourceComposio,
		Category: "integration",
		Keywords: []string{"youtube", "comments", "discussion", "composio"},
		Parameters: map[string]interface{}{
			"type": "object",
			"properties": map[string]interface{}{
				"credential_id": map[string]interface{}{
					"type":        "string",
					"description": "INTERNAL: Auto-injected by system. Do not set manually.",
				},
				"video_id": map[string]interface{}{
					"type":        "string",
					"description": "ID of the video to get comments from",
				},
				"max_results": map[string]interface{}{
					"type":        "integer",
					"description": "Maximum number of comments (default: 20)",
				},
			},
			"required": []string{"video_id"},
		},
		Execute: executeYouTubeGetComments,
	}
}

func executeYouTubeGetComments(args map[string]interface{}) (string, error) {
	if err := checkYouTubeRateLimit(args); err != nil {
		return "", err
	}

	credData, err := GetCredentialData(args, "composio_youtube")
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

	if videoID, ok := args["video_id"].(string); ok && videoID != "" {
		input["videoId"] = videoID
	} else {
		return "", fmt.Errorf("video_id parameter is required for YouTube comments")
	}
	if maxResults, ok := args["max_results"].(float64); ok {
		input["maxResults"] = int(maxResults)
	}

	return callComposioYouTubeAPI(composioAPIKey, entityID, "YOUTUBE_LIST_COMMENT_THREADS", input)
}

// NewYouTubeSubscribeTool creates a tool for subscribing to channels
func NewYouTubeSubscribeTool() *Tool {
	return &Tool{
		Name:        "youtube_subscribe",
		DisplayName: "YouTube - Subscribe to Channel",
		Description: `Subscribe the authenticated user to a YouTube channel. This is equivalent to clicking the "Subscribe" button on YouTube. The user will start receiving updates from the channel.

WHEN TO USE THIS TOOL:
- The user asks "subscribe me to this channel"
- The user wants to follow a YouTube channel

PARAMETERS:
- channel_id (REQUIRED): The YouTube channel ID to subscribe to (e.g., "UCxxxxxx")

RETURNS: Confirmation of the subscription. WARNING: This performs a real action on the user's YouTube account.`,
		Icon:     "Bell",
		Source:   ToolSourceComposio,
		Category: "integration",
		Keywords: []string{"youtube", "subscribe", "channel", "follow", "composio"},
		Parameters: map[string]interface{}{
			"type": "object",
			"properties": map[string]interface{}{
				"credential_id": map[string]interface{}{
					"type":        "string",
					"description": "INTERNAL: Auto-injected by system. Do not set manually.",
				},
				"channel_id": map[string]interface{}{
					"type":        "string",
					"description": "ID of the channel to subscribe to",
				},
			},
			"required": []string{"channel_id"},
		},
		Execute: executeYouTubeSubscribe,
	}
}

func executeYouTubeSubscribe(args map[string]interface{}) (string, error) {
	if err := checkYouTubeRateLimit(args); err != nil {
		return "", err
	}

	credData, err := GetCredentialData(args, "composio_youtube")
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

	if channelID, ok := args["channel_id"].(string); ok && channelID != "" {
		input["channelId"] = channelID
	} else {
		return "", fmt.Errorf("channel_id parameter is required for YouTube subscribe")
	}

	return callComposioYouTubeAPI(composioAPIKey, entityID, "YOUTUBE_SUBSCRIBE_CHANNEL", input)
}

// callComposioYouTubeAPI makes a v2 API call to Composio for YouTube actions
func callComposioYouTubeAPI(apiKey string, entityID string, action string, input map[string]interface{}) (string, error) {
	connectedAccountID, err := getYouTubeConnectedAccountID(apiKey, entityID, "youtube")
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

	log.Printf("📺 [YOUTUBE] Action: %s, ConnectedAccount: %s", action, maskSensitiveID(connectedAccountID))

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
		log.Printf("❌ [YOUTUBE] API error (status %d) for action %s: %s", resp.StatusCode, action, string(respBody))
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

// getYouTubeConnectedAccountID retrieves the connected account ID from Composio v3 API
func getYouTubeConnectedAccountID(apiKey string, userID string, appName string) (string, error) {
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

	return "", fmt.Errorf("no %s connection found for user. Please connect your YouTube account first", appName)
}
