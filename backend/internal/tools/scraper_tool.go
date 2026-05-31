package tools

import (
	"bytes"
	"claraverse/internal/security"
	"context"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"time"

	"github.com/markusmobius/go-trafilatura"
	cache "github.com/patrickmn/go-cache"
	"github.com/temoto/robotstxt"
	"golang.org/x/time/rate"
)

// ScraperTool provides web scraping capabilities
type ScraperTool struct {
	cache       *cache.Cache
	rateLimiter *rate.Limiter
	client      *http.Client
}

var scraperToolInstance *ScraperTool

func init() {
	scraperToolInstance = &ScraperTool{
		cache:       cache.New(1*time.Hour, 10*time.Minute),
		rateLimiter: rate.NewLimiter(rate.Limit(10.0), 20), // 10 req/s global
		client: &http.Client{
			Timeout: 60 * time.Second,
		},
	}
}

// NewScraperTool creates the scrape_web tool
func NewScraperTool() *Tool {
	return &Tool{
		Name:        "scrape_web",
		DisplayName: "Scrape Web Page",
		Description: "Extract clean, readable content from a web page URL. Returns main article content without ads, navigation, or other boilerplate. Respects robots.txt and rate limits. Best for articles, blog posts, and documentation pages.",
		Icon:        "Globe",
		Parameters: map[string]interface{}{
			"type": "object",
			"properties": map[string]interface{}{
				"url": map[string]interface{}{
					"type":        "string",
					"description": "The URL of the web page to scrape (must be a valid HTTP/HTTPS URL)",
				},
				"max_length": map[string]interface{}{
					"type":        "number",
					"description": "Optional maximum content length in characters (default: 50000, max: 100000)",
					"default":     50000,
				},
				"format": map[string]interface{}{
					"type":        "string",
					"description": "Output format: 'markdown' or 'text' (default: markdown)",
					"enum":        []string{"markdown", "text"},
					"default":     "markdown",
				},
			},
			"required": []string{"url"},
		},
		Execute:  executeScrapWeb,
		Source:   ToolSourceBuiltin,
		Category: "data_sources",
		Keywords: []string{"scrape", "fetch", "extract", "web", "page", "content", "article", "url", "website", "html", "crawl"},
	}
}

func executeScrapWeb(args map[string]interface{}) (string, error) {
	// Extract URL parameter
	urlStr, ok := args["url"].(string)
	if !ok || urlStr == "" {
		return "", fmt.Errorf("url parameter is required and must be a string")
	}

	// Validate URL
	if err := validateURL(urlStr); err != nil {
		return "", err
	}

	// Extract max_length parameter
	maxLength := 50000
	if ml, ok := args["max_length"].(float64); ok {
		if ml > 100000 {
			ml = 100000
		}
		if ml < 1000 {
			ml = 1000
		}
		maxLength = int(ml)
	}

	// Extract format parameter
	format := "markdown"
	if f, ok := args["format"].(string); ok {
		if f == "text" || f == "markdown" {
			format = f
		}
	}

	// Check cache
	cacheKey := fmt.Sprintf("%s:%s", urlStr, format)
	if cached, found := scraperToolInstance.cache.Get(cacheKey); found {
		return cached.(string), nil
	}

	// Check robots.txt
	if allowed, err := checkRobots(urlStr); err == nil && !allowed {
		return "", fmt.Errorf("blocked by robots.txt")
	}

	// Apply rate limiting
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	if err := scraperToolInstance.rateLimiter.Wait(ctx); err != nil {
		return "", fmt.Errorf("rate limit exceeded, try again later")
	}

	// Fetch URL
	req, err := http.NewRequestWithContext(ctx, "GET", urlStr, nil)
	if err != nil {
		return "", fmt.Errorf("failed to create request: %w", err)
	}

	req.Header.Set("User-Agent", "ClaraVerse-Bot/1.0 (+https://claraverse.example.com/bot)")
	req.Header.Set("Accept", "text/html,application/xhtml+xml")

	resp, err := scraperToolInstance.client.Do(req)
	if err != nil {
		return "", fmt.Errorf("failed to fetch URL: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != 200 {
		return "", fmt.Errorf("HTTP error %d: %s", resp.StatusCode, resp.Status)
	}

	// Read body with limit
	body, err := io.ReadAll(io.LimitReader(resp.Body, 10*1024*1024)) // 10MB limit
	if err != nil {
		return "", fmt.Errorf("failed to read response: %w", err)
	}

	// Extract content
	parsedURL, _ := url.Parse(urlStr)
	opts := trafilatura.Options{
		OriginalURL: parsedURL,
	}

	result, err := trafilatura.Extract(bytes.NewReader(body), opts)
	if err != nil || result == nil || result.ContentText == "" {
		return "", fmt.Errorf("failed to extract content from page")
	}

	// Use extracted content (already plain text or markdown)
	content := result.ContentText

	// Apply length limit
	if len(content) > maxLength {
		content = content[:maxLength] + "\n\n[Content truncated due to length limit]"
	}

	// Add metadata
	metadata := fmt.Sprintf("# %s\n\n", result.Metadata.Title)
	if result.Metadata.Author != "" {
		metadata += fmt.Sprintf("**Author:** %s  \n", result.Metadata.Author)
	}
	if !result.Metadata.Date.IsZero() {
		metadata += fmt.Sprintf("**Published:** %s  \n", result.Metadata.Date.Format("January 2, 2006"))
	}
	metadata += fmt.Sprintf("**Source:** %s  \n\n---\n\n", urlStr)

	finalContent := metadata + content

	// Cache result
	scraperToolInstance.cache.Set(cacheKey, finalContent, cache.DefaultExpiration)

	return finalContent, nil
}

func validateURL(urlStr string) error {
	// Use centralized SSRF protection which includes:
	// - Private IP range blocking (10.x, 172.16-31.x, 192.168.x, etc.)
	// - Localhost/loopback blocking
	// - Cloud metadata endpoint blocking (169.254.169.254, metadata.google.internal)
	// - DNS resolution checks to catch hostname-based bypasses
	// - IPv6 private address blocking
	return security.ValidateURLForSSRF(urlStr)
}

func checkRobots(urlStr string) (bool, error) {
	parsedURL, err := url.Parse(urlStr)
	if err != nil {
		return true, err
	}

	robotsURL := parsedURL.Scheme + "://" + parsedURL.Host + "/robots.txt"

	client := &http.Client{Timeout: 10 * time.Second}
	resp, err := client.Get(robotsURL)
	if err != nil {
		return true, nil // Allow if robots.txt doesn't exist
	}
	defer resp.Body.Close()

	if resp.StatusCode != 200 {
		return true, nil
	}

	body, err := io.ReadAll(io.LimitReader(resp.Body, 1*1024*1024))
	if err != nil {
		return true, nil
	}

	robotsData, err := robotstxt.FromBytes(body)
	if err != nil {
		return true, nil
	}

	group := robotsData.FindGroup("ClaraVerse-Bot")
	if group == nil {
		group = robotsData.FindGroup("*")
	}

	if group != nil {
		return group.Test(parsedURL.Path), nil
	}

	return true, nil
}
