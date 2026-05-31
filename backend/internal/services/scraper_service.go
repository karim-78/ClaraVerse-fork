package services

import (
	"bytes"
	"context"
	"fmt"
	"log"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/markusmobius/go-trafilatura"
	cache "github.com/patrickmn/go-cache"
)

const (
	defaultUserAgent     = "ClaraVerse-Bot/1.0 (+https://claraverse.example.com/bot)"
	defaultMaxBodySize   = 10 * 1024 * 1024  // 10MB
	defaultMaxConcurrent = 10
	defaultGlobalRate    = 10.0 // requests per second
	defaultPerUserRate   = 5.0  // requests per second
)

// ScraperService handles web scraping operations
type ScraperService struct {
	client        *ScraperClient
	rateLimiter   *RateLimiter
	robotsChecker *RobotsChecker
	contentCache  *cache.Cache
	resourceMgr   *ResourceManager
}

var (
	scraperInstance *ScraperService
	scraperOnce     sync.Once
)

// GetScraperService returns the singleton scraper service instance
func GetScraperService() *ScraperService {
	scraperOnce.Do(func() {
		scraperInstance = &ScraperService{
			client:        NewScraperClient(),
			rateLimiter:   NewRateLimiter(defaultGlobalRate, defaultPerUserRate),
			robotsChecker: NewRobotsChecker(defaultUserAgent),
			contentCache:  cache.New(1*time.Hour, 10*time.Minute), // Cache for 1 hour
			resourceMgr:   NewResourceManager(defaultMaxConcurrent, defaultMaxBodySize),
		}

		log.Printf("✅ [SCRAPER] Service initialized: max_concurrent=%d, global_rate=%.1f req/s",
			defaultMaxConcurrent, defaultGlobalRate)
	})
	return scraperInstance
}

// ScrapeURL scrapes a web page and returns clean content
func (s *ScraperService) ScrapeURL(ctx context.Context, urlStr, format string, maxLength int, userID string) (string, error) {
	startTime := time.Now()

	// 1. Validate URL
	if err := s.validateURL(urlStr); err != nil {
		return "", err
	}

	parsedURL, err := url.Parse(urlStr)
	if err != nil {
		return "", fmt.Errorf("invalid URL: %w", err)
	}

	domain := parsedURL.Host

	// 2. Check cache
	cacheKey := s.getCacheKey(urlStr, format)
	if cached, found := s.contentCache.Get(cacheKey); found {
		log.Printf("✅ [SCRAPER] Cache hit for URL: %s (latency: %dms)",
			urlStr, time.Since(startTime).Milliseconds())
		return cached.(string), nil
	}

	// 3. Check robots.txt
	allowed, crawlDelay, err := s.robotsChecker.CanFetch(ctx, urlStr)
	if err != nil {
		log.Printf("⚠️  [SCRAPER] Failed to check robots.txt for %s: %v", urlStr, err)
		// Continue anyway with default delay
		crawlDelay = 1 * time.Second
	}

	if !allowed {
		return "", fmt.Errorf("access blocked by robots.txt for: %s", urlStr)
	}

	// 4. Apply rate limiting
	if err := s.rateLimiter.WaitWithCrawlDelay(ctx, userID, domain, crawlDelay); err != nil {
		return "", fmt.Errorf("rate limit error: %w", err)
	}

	// 5. Acquire resource semaphore
	if err := s.resourceMgr.Acquire(ctx); err != nil {
		return "", fmt.Errorf("resource limit reached, try again later: %w", err)
	}
	defer s.resourceMgr.Release()

	// 6. Fetch URL
	resp, err := s.client.Get(ctx, urlStr)
	if err != nil {
		log.Printf("❌ [SCRAPER] Failed to fetch URL %s: %v", urlStr, err)
		return "", fmt.Errorf("failed to fetch URL: %w", err)
	}
	defer resp.Body.Close()

	// 7. Check HTTP status
	if resp.StatusCode != 200 {
		return "", fmt.Errorf("HTTP error %d: %s", resp.StatusCode, resp.Status)
	}

	// 8. Check content type
	contentType := resp.Header.Get("Content-Type")
	if !s.isSupportedContentType(contentType) {
		return "", fmt.Errorf("unsupported content type: %s", contentType)
	}

	// 9. Read body with size limit
	body, err := s.resourceMgr.ReadBody(resp.Body)
	if err != nil {
		return "", fmt.Errorf("failed to read response: %w", err)
	}

	// 10. Extract main content using trafilatura
	opts := trafilatura.Options{
		OriginalURL: parsedURL,
	}

	result, err := trafilatura.Extract(bytes.NewReader(body), opts)
	if err != nil {
		return "", fmt.Errorf("failed to extract content: %w", err)
	}

	if result == nil || result.ContentText == "" {
		return "", fmt.Errorf("no content extracted from page")
	}

	// 11. Use extracted content (already plain text or markdown)
	content := result.ContentText

	// 12. Apply length limit
	if len(content) > maxLength {
		content = content[:maxLength] + "\n\n[Content truncated due to length limit]"
	}

	// 13. Add metadata header
	metadata := fmt.Sprintf("# %s\n\n", result.Metadata.Title)
	if result.Metadata.Author != "" {
		metadata += fmt.Sprintf("**Author:** %s  \n", result.Metadata.Author)
	}
	if !result.Metadata.Date.IsZero() {
		metadata += fmt.Sprintf("**Published:** %s  \n", result.Metadata.Date.Format("January 2, 2006"))
	}
	metadata += fmt.Sprintf("**Source:** %s  \n", urlStr)
	metadata += "\n---\n\n"

	finalContent := metadata + content

	// 14. Cache result
	s.contentCache.Set(cacheKey, finalContent, cache.DefaultExpiration)

	latency := time.Since(startTime).Milliseconds()
	log.Printf("✅ [SCRAPER] Successfully scraped URL: %s (latency: %dms, length: %d chars)",
		urlStr, latency, len(finalContent))

	return finalContent, nil
}

// validateURL checks if the URL is safe to scrape (SSRF protection)
func (s *ScraperService) validateURL(urlStr string) error {
	parsedURL, err := url.Parse(urlStr)
	if err != nil {
		return fmt.Errorf("invalid URL format: %w", err)
	}

	// Only allow HTTP and HTTPS
	if parsedURL.Scheme != "http" && parsedURL.Scheme != "https" {
		return fmt.Errorf("only HTTP/HTTPS URLs are supported, got: %s", parsedURL.Scheme)
	}

	hostname := strings.ToLower(parsedURL.Hostname())

	// Block localhost
	if hostname == "localhost" || hostname == "127.0.0.1" || hostname == "::1" {
		return fmt.Errorf("localhost URLs are not allowed")
	}

	// Block private IP ranges
	privateRanges := []string{
		"192.168.", "10.", "172.16.", "172.17.", "172.18.", "172.19.",
		"172.20.", "172.21.", "172.22.", "172.23.", "172.24.", "172.25.",
		"172.26.", "172.27.", "172.28.", "172.29.", "172.30.", "172.31.",
		"169.254.", // Link-local
		"fd",       // IPv6 private
	}

	for _, prefix := range privateRanges {
		if strings.HasPrefix(hostname, prefix) {
			return fmt.Errorf("private IP addresses are not allowed")
		}
	}

	return nil
}

// isSupportedContentType checks if the content type is supported
func (s *ScraperService) isSupportedContentType(contentType string) bool {
	contentType = strings.ToLower(contentType)

	supported := []string{
		"text/html",
		"text/plain",
		"application/xhtml+xml",
	}

	for _, ct := range supported {
		if strings.Contains(contentType, ct) {
			return true
		}
	}

	return false
}

// getCacheKey generates a cache key for URL and format
func (s *ScraperService) getCacheKey(urlStr, format string) string {
	return fmt.Sprintf("%s:%s", urlStr, format)
}
