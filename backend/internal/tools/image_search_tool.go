package tools

import (
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

// ImageResult represents a single image search result
type ImageResult struct {
	Title        string `json:"title"`
	URL          string `json:"url"`           // Original image URL
	ThumbnailURL string `json:"thumbnail_url"` // Thumbnail URL (may be same as URL)
	SourceURL    string `json:"source_url"`    // Source page URL
	Resolution   string `json:"resolution,omitempty"`
}

// ImageSearchResponse is the structured result for frontend
type ImageSearchResponse struct {
	Success        bool          `json:"success"`
	Query          string        `json:"query"`
	Images         []ImageResult `json:"images"`
	Count          int           `json:"count"`
	MarkdownImages string        `json:"markdown_images"` // Pre-formatted markdown for LLM to embed
}

// NewImageSearchTool creates the search_images tool
func NewImageSearchTool() *Tool {
	return &Tool{
		Name:        "search_images",
		DisplayName: "Search Images",
		Description: `Search for images on the web. Use this tool to enhance responses with visual context.

USE THIS TOOL WHEN:
- User asks about characters, creatures, people, or beings (e.g., "witches in Witcher 3", "Marvel characters")
- User asks about places, landmarks, or locations (e.g., "beaches in Bali", "castles in Scotland")
- User asks about products, items, or objects (e.g., "gaming keyboards", "vintage cars")
- User asks "what does X look like" or "show me X"
- User asks for a list of things that have visual significance (games, movies, animals, plants, etc.)
- Visuals would significantly enhance understanding of the topic

DO NOT USE WHEN:
- Query is purely abstract (math, code, definitions)
- Query is about recent news or events (use search_web instead)
- User explicitly asks for text-only response

EMBEDDING IMAGES IN YOUR RESPONSE:
The tool returns a "markdown_images" field with ready-to-use markdown. You SHOULD embed relevant images inline in your response using the markdown syntax provided. For example, when listing characters, include their image next to the description.

Example format in your response:
### Character Name
![Character Name](image_url)
Description of the character...

This creates a rich, visual response similar to how Perplexity presents information.`,
		Icon: "Image",
		Parameters: map[string]interface{}{
			"type": "object",
			"properties": map[string]interface{}{
				"query": map[string]interface{}{
					"type":        "string",
					"description": "The search query to find images for. Be specific - include relevant context like game/movie/show names for characters.",
				},
				"count": map[string]interface{}{
					"type":        "integer",
					"description": "Number of images to return (default: 5, max: 10)",
				},
			},
			"required": []string{"query"},
		},
		Execute:  executeImageSearch,
		Source:   ToolSourceBuiltin,
		Category: "data_sources",
		Keywords: []string{"image", "images", "picture", "pictures", "photo", "photos", "visual", "gallery", "show me", "what does", "look like", "characters", "list of", "examples of"},
	}
}

func executeImageSearch(args map[string]interface{}) (string, error) {
	query, ok := args["query"].(string)
	if !ok {
		return "", fmt.Errorf("query parameter is required and must be a string")
	}

	query = strings.TrimSpace(query)
	log.Printf("🖼️ [SEARCH-IMAGES] Starting image search for: '%s'", query)

	// Get count (default 5, max 10)
	count := 5
	if c, ok := args["count"].(float64); ok {
		count = int(c)
		if count > 10 {
			count = 10
		}
		if count < 1 {
			count = 5
		}
	}

	// Check cache first (reuse globalSearchCache from search_tool.go)
	cacheKey := "images:" + strings.ToLower(query)
	if cached, found := globalSearchCache.get(cacheKey); found {
		log.Printf("✅ [SEARCH-IMAGES] Cache hit for: '%s'", query)
		return cached, nil
	}

	// Get SearXNG URL from environment or use default
	// Check SEARXNG_URLS (comma-separated list) first, then fall back to SEARXNG_URL
	searxngURL := ""
	urlsEnv := os.Getenv("SEARXNG_URLS")
	if urlsEnv != "" {
		// Extract first URL from comma-separated list
		urls := strings.Split(urlsEnv, ",")
		if len(urls) > 0 {
			searxngURL = strings.TrimSpace(urls[0])
		}
	}

	// Fallback to single SEARXNG_URL if SEARXNG_URLS is not set
	if searxngURL == "" {
		searxngURL = os.Getenv("SEARXNG_URL")
		if searxngURL == "" {
			searxngURL = "http://localhost:8080"
		}
	}

	searxngURL = strings.TrimSuffix(searxngURL, "/")

	// Perform the image search
	result, err := performImageSearch(searxngURL, query, count)
	if err != nil {
		log.Printf("❌ [SEARCH-IMAGES] Search failed: %v", err)
		return "", err
	}

	// Cache successful results
	globalSearchCache.set(cacheKey, result, 5*time.Minute)
	log.Printf("✅ [SEARCH-IMAGES] Cached results for: '%s'", query)

	return result, nil
}

// performImageSearch executes an image search request to SearXNG
func performImageSearch(searxngURL, query string, count int) (string, error) {
	// Build search URL with images category
	searchURL := fmt.Sprintf("%s/search?q=%s&format=json&safesearch=1&categories=images",
		searxngURL, url.QueryEscape(query))

	// Make HTTP request with required headers
	req, err := http.NewRequest("GET", searchURL, nil)
	if err != nil {
		return "", fmt.Errorf("failed to create request: %v", err)
	}
	req.Header.Set("User-Agent", "ClaraVerse/1.0 (Bot)")
	req.Header.Set("Accept", "application/json")
	req.Header.Set("X-Forwarded-For", "127.0.0.1")
	req.Header.Set("X-Real-IP", "127.0.0.1")

	client := &http.Client{Timeout: 15 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return "", fmt.Errorf("image search request failed: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("image search failed with status: %d", resp.StatusCode)
	}

	// Parse response
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", fmt.Errorf("failed to read search response: %v", err)
	}

	// SearXNG image search response structure
	var searchResults struct {
		Results []struct {
			Title        string `json:"title"`
			URL          string `json:"url"`           // Page URL
			ImgSrc       string `json:"img_src"`       // Direct image URL
			ThumbnailSrc string `json:"thumbnail_src"` // Thumbnail URL
			Content      string `json:"content"`       // Description
			Source       string `json:"source"`        // Source website
			Resolution   string `json:"resolution"`    // Image resolution
		} `json:"results"`
	}

	if err := json.Unmarshal(body, &searchResults); err != nil {
		return "", fmt.Errorf("failed to parse image search results: %v", err)
	}

	if len(searchResults.Results) == 0 {
		// Return empty response instead of error
		response := ImageSearchResponse{
			Success: true,
			Query:   query,
			Images:  []ImageResult{},
			Count:   0,
		}
		jsonResponse, _ := json.MarshalIndent(response, "", "  ")
		return string(jsonResponse), nil
	}

	// Build response with limited results
	images := make([]ImageResult, 0, count)
	for i, res := range searchResults.Results {
		if i >= count {
			break
		}

		// Skip results without image URL
		if res.ImgSrc == "" {
			continue
		}

		// Use thumbnail if available, otherwise use main image
		thumbnailURL := res.ThumbnailSrc
		if thumbnailURL == "" {
			thumbnailURL = res.ImgSrc
		}

		images = append(images, ImageResult{
			Title:        res.Title,
			URL:          res.ImgSrc,   // Original image URL
			ThumbnailURL: thumbnailURL, // Thumbnail URL
			SourceURL:    res.URL,      // Source page URL
			Resolution:   res.Resolution,
		})
	}

	// Build markdown image syntax for LLM to embed in response
	// Use proxy URLs so images load correctly in the frontend
	// Use full resolution image URLs instead of thumbnails for higher quality (4K when available)
	var markdownBuilder strings.Builder
	for i, img := range images {
		// Create proxy URL for the full resolution image (not thumbnail)
		proxyURL := fmt.Sprintf("/api/proxy/image?url=%s", url.QueryEscape(img.URL))
		// Use a simple numbered format the LLM can reference
		markdownBuilder.WriteString(fmt.Sprintf("Image %d: ![%s](%s)\n", i+1, img.Title, proxyURL))
	}

	response := ImageSearchResponse{
		Success:        true,
		Query:          query,
		Images:         images,
		Count:          len(images),
		MarkdownImages: markdownBuilder.String(),
	}

	jsonResponse, _ := json.MarshalIndent(response, "", "  ")
	log.Printf("✅ [SEARCH-IMAGES] Found %d images for '%s'", len(images), query)

	return string(jsonResponse), nil
}
