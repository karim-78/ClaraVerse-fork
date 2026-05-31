package services

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"time"
)

// ScraperClient wraps an HTTP client with optimized settings for web scraping
type ScraperClient struct {
	httpClient *http.Client
	userAgent  string
	timeout    time.Duration
}

// NewScraperClient creates a new HTTP client optimized for web scraping
func NewScraperClient() *ScraperClient {
	// Custom transport with optimized connection pooling
	transport := &http.Transport{
		MaxIdleConns:        100,              // Total idle connections across all hosts
		MaxIdleConnsPerHost: 20,               // CRITICAL: Default is 2! Increase for performance
		MaxConnsPerHost:     50,               // Maximum connections per host
		IdleConnTimeout:     90 * time.Second, // Keep connections alive
		TLSHandshakeTimeout: 10 * time.Second,
		DisableCompression:  false,

		// Dial settings for connection establishment
		DialContext: (&net.Dialer{
			Timeout:   30 * time.Second,
			KeepAlive: 30 * time.Second,
		}).DialContext,
	}

	return &ScraperClient{
		httpClient: &http.Client{
			Transport: transport,
			Timeout:   60 * time.Second, // Overall request timeout
			CheckRedirect: func(req *http.Request, via []*http.Request) error {
				if len(via) >= 10 {
					return fmt.Errorf("too many redirects (max 10)")
				}
				return nil
			},
		},
		userAgent: "ClaraVerse-Bot/1.0 (+https://claraverse.example.com/bot)",
		timeout:   60 * time.Second,
	}
}

// Get performs an HTTP GET request with proper headers
func (c *ScraperClient) Get(ctx context.Context, url string) (*http.Response, error) {
	req, err := http.NewRequestWithContext(ctx, "GET", url, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}

	// Set proper headers
	req.Header.Set("User-Agent", c.userAgent)
	req.Header.Set("Accept", "text/html,application/xhtml+xml,application/xml;q=0.9,*/*;q=0.8")
	req.Header.Set("Accept-Language", "en-US,en;q=0.9")

	return c.httpClient.Do(req)
}

// SetUserAgent updates the user agent string
func (c *ScraperClient) SetUserAgent(userAgent string) {
	c.userAgent = userAgent
}

// SetTimeout updates the request timeout
func (c *ScraperClient) SetTimeout(timeout time.Duration) {
	c.timeout = timeout
	c.httpClient.Timeout = timeout
}
