package e2b

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"os"
	"time"

	"github.com/sirupsen/logrus"
)

// APIKeyProvider is a function that returns the current E2B API key
type APIKeyProvider func() string

// E2BExecutorService handles communication with the E2B Python microservice
type E2BExecutorService struct {
	baseURL        string
	httpClient     *http.Client
	logger         *logrus.Logger
	apiKeyProvider APIKeyProvider
}

// ExecuteRequest represents a code execution request
type ExecuteRequest struct {
	Code    string `json:"code"`
	Timeout int    `json:"timeout,omitempty"` // seconds
}

// PlotResult represents a generated plot
type PlotResult struct {
	Format string `json:"format"` // "png", "svg", etc.
	Data   string `json:"data"`   // base64 encoded
}

// ExecuteResponse represents the response from code execution
type ExecuteResponse struct {
	Success       bool         `json:"success"`
	Stdout        string       `json:"stdout"`
	Stderr        string       `json:"stderr"`
	Error         *string      `json:"error"`
	Plots         []PlotResult `json:"plots"`
	ExecutionTime *float64     `json:"execution_time"`
}

// AdvancedExecuteRequest represents a request with dependencies and output files.
// ConversationID enables sandbox pooling — calls with the same conversation_id
// share a long-lived sandbox so dataframes, imports, and globals persist
// across model turns. Empty = ephemeral sandbox (legacy behaviour).
type AdvancedExecuteRequest struct {
	Code           string   `json:"code"`
	Timeout        int      `json:"timeout,omitempty"`
	Dependencies   []string `json:"dependencies,omitempty"`
	OutputFiles    []string `json:"output_files,omitempty"`
	ConversationID string   `json:"conversation_id,omitempty"`
}

// FileResult represents a file retrieved from the sandbox
type FileResult struct {
	Filename string `json:"filename"`
	Data     string `json:"data"` // base64 encoded
	Size     int    `json:"size"`
}

// AdvancedExecuteResponse represents the response with files.
// SandboxID + SandboxReused are populated by the persistent pool: they tell
// the caller which physical sandbox served the request and whether it was
// reused from a prior turn. Useful for logging cache-hit-style metrics.
type AdvancedExecuteResponse struct {
	Success       bool         `json:"success"`
	Stdout        string       `json:"stdout"`
	Stderr        string       `json:"stderr"`
	Error         *string      `json:"error"`
	Plots         []PlotResult `json:"plots"`
	Files         []FileResult `json:"files"`
	ExecutionTime *float64     `json:"execution_time"`
	InstallOutput string       `json:"install_output"`
	SandboxID     string       `json:"sandbox_id,omitempty"`
	SandboxReused bool         `json:"sandbox_reused,omitempty"`
}

var (
	e2bExecutorServiceInstance *E2BExecutorService
)

// SetAPIKeyProvider sets a function that provides the E2B API key from the database
func SetAPIKeyProvider(provider APIKeyProvider) {
	if e2bExecutorServiceInstance != nil {
		e2bExecutorServiceInstance.apiKeyProvider = provider
	}
}

// GetE2BExecutorService returns the singleton instance of E2BExecutorService
func GetE2BExecutorService() *E2BExecutorService {
	if e2bExecutorServiceInstance == nil {
		logger := logrus.New()
		logger.SetFormatter(&logrus.JSONFormatter{})

		// Get E2B service URL from environment
		baseURL := os.Getenv("E2B_SERVICE_URL")
		if baseURL == "" {
			baseURL = "http://e2b-service:8001" // Default for Docker Compose
		}

		e2bExecutorServiceInstance = &E2BExecutorService{
			baseURL: baseURL,
			httpClient: &http.Client{
				Timeout: 330 * time.Second, // 5.5 minutes to allow 5 min execution + overhead
			},
			logger: logger,
		}

		e2bExecutorServiceInstance.logger.WithField("baseURL", baseURL).Info("E2B Executor Service initialized")
	}
	return e2bExecutorServiceInstance
}

// getAPIKey returns the E2B API key from the provider or env var
func (s *E2BExecutorService) getAPIKey() string {
	if s.apiKeyProvider != nil {
		if key := s.apiKeyProvider(); key != "" {
			return key
		}
	}
	return os.Getenv("E2B_API_KEY")
}

// HealthCheck checks if the E2B service is healthy
func (s *E2BExecutorService) HealthCheck(ctx context.Context) error {
	url := fmt.Sprintf("%s/health", s.baseURL)

	req, err := http.NewRequestWithContext(ctx, "GET", url, nil)
	if err != nil {
		return fmt.Errorf("failed to create health check request: %w", err)
	}
	if apiKey := s.getAPIKey(); apiKey != "" {
		req.Header.Set("X-E2B-API-Key", apiKey)
	}

	resp, err := s.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("health check request failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("health check failed: status=%d, body=%s", resp.StatusCode, string(body))
	}

	s.logger.Info("E2B service health check passed")
	return nil
}

// Execute runs Python code in an E2B sandbox
func (s *E2BExecutorService) Execute(ctx context.Context, code string, timeout int) (*ExecuteResponse, error) {
	url := fmt.Sprintf("%s/execute", s.baseURL)

	// Prepare request
	reqBody := ExecuteRequest{
		Code:    code,
		Timeout: timeout,
	}

	jsonData, err := json.Marshal(reqBody)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal request: %w", err)
	}

	s.logger.WithFields(logrus.Fields{
		"code_length": len(code),
		"timeout":     timeout,
	}).Info("Executing code in E2B sandbox")

	// Create HTTP request
	req, err := http.NewRequestWithContext(ctx, "POST", url, bytes.NewBuffer(jsonData))
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	if apiKey := s.getAPIKey(); apiKey != "" {
		req.Header.Set("X-E2B-API-Key", apiKey)
	}

	// Execute request
	resp, err := s.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("request failed: %w", err)
	}
	defer resp.Body.Close()

	// Read response
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("failed to read response: %w", err)
	}

	// Check status code
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("execution failed: status=%d, body=%s", resp.StatusCode, string(body))
	}

	// Parse response
	var result ExecuteResponse
	if err := json.Unmarshal(body, &result); err != nil {
		return nil, fmt.Errorf("failed to parse response: %w", err)
	}

	s.logger.WithFields(logrus.Fields{
		"success":    result.Success,
		"plot_count": len(result.Plots),
		"has_stdout": len(result.Stdout) > 0,
		"has_stderr": len(result.Stderr) > 0,
	}).Info("Code execution completed")

	return &result, nil
}

// ExecuteWithFiles runs Python code with uploaded files
func (s *E2BExecutorService) ExecuteWithFiles(ctx context.Context, code string, files map[string][]byte, timeout int) (*ExecuteResponse, error) {
	url := fmt.Sprintf("%s/execute-with-files", s.baseURL)

	// Create multipart form
	body := &bytes.Buffer{}
	writer := multipart.NewWriter(body)

	// Add code field
	if err := writer.WriteField("code", code); err != nil {
		return nil, fmt.Errorf("failed to write code field: %w", err)
	}

	// Add timeout field
	if err := writer.WriteField("timeout", fmt.Sprintf("%d", timeout)); err != nil {
		return nil, fmt.Errorf("failed to write timeout field: %w", err)
	}

	// Add files
	for filename, content := range files {
		part, err := writer.CreateFormFile("files", filename)
		if err != nil {
			return nil, fmt.Errorf("failed to create form file %s: %w", filename, err)
		}

		if _, err := part.Write(content); err != nil {
			return nil, fmt.Errorf("failed to write file %s: %w", filename, err)
		}

		s.logger.WithFields(logrus.Fields{
			"filename": filename,
			"size":     len(content),
		}).Info("Added file to request")
	}

	if err := writer.Close(); err != nil {
		return nil, fmt.Errorf("failed to close multipart writer: %w", err)
	}

	s.logger.WithFields(logrus.Fields{
		"code_length": len(code),
		"file_count":  len(files),
		"timeout":     timeout,
	}).Info("Executing code with files in E2B sandbox")

	// Create HTTP request
	req, err := http.NewRequestWithContext(ctx, "POST", url, body)
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}
	req.Header.Set("Content-Type", writer.FormDataContentType())
	if apiKey := s.getAPIKey(); apiKey != "" {
		req.Header.Set("X-E2B-API-Key", apiKey)
	}

	// Execute request
	resp, err := s.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("request failed: %w", err)
	}
	defer resp.Body.Close()

	// Read response
	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("failed to read response: %w", err)
	}

	// Check status code
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("execution failed: status=%d, body=%s", resp.StatusCode, string(respBody))
	}

	// Parse response
	var result ExecuteResponse
	if err := json.Unmarshal(respBody, &result); err != nil {
		return nil, fmt.Errorf("failed to parse response: %w", err)
	}

	s.logger.WithFields(logrus.Fields{
		"success":    result.Success,
		"plot_count": len(result.Plots),
		"has_stdout": len(result.Stdout) > 0,
		"has_stderr": len(result.Stderr) > 0,
	}).Info("Code execution with files completed")

	return &result, nil
}

// ExecuteAdvanced runs Python code with dependencies and retrieves output files
func (s *E2BExecutorService) ExecuteAdvanced(ctx context.Context, req AdvancedExecuteRequest) (*AdvancedExecuteResponse, error) {
	url := fmt.Sprintf("%s/execute-advanced", s.baseURL)

	// Set default timeout
	if req.Timeout == 0 {
		req.Timeout = 30
	}

	jsonData, err := json.Marshal(req)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal request: %w", err)
	}

	s.logger.WithFields(logrus.Fields{
		"code_length":  len(req.Code),
		"timeout":      req.Timeout,
		"dependencies": req.Dependencies,
		"output_files": req.OutputFiles,
	}).Info("Executing advanced code in E2B sandbox")

	// Create HTTP request
	httpReq, err := http.NewRequestWithContext(ctx, "POST", url, bytes.NewBuffer(jsonData))
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}
	httpReq.Header.Set("Content-Type", "application/json")
	if apiKey := s.getAPIKey(); apiKey != "" {
		httpReq.Header.Set("X-E2B-API-Key", apiKey)
	}

	// Execute request
	resp, err := s.httpClient.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("request failed: %w", err)
	}
	defer resp.Body.Close()

	// Read response
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("failed to read response: %w", err)
	}

	// Check status code
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("execution failed: status=%d, body=%s", resp.StatusCode, string(body))
	}

	// Parse response
	var result AdvancedExecuteResponse
	if err := json.Unmarshal(body, &result); err != nil {
		return nil, fmt.Errorf("failed to parse response: %w", err)
	}

	s.logger.WithFields(logrus.Fields{
		"success":    result.Success,
		"plot_count": len(result.Plots),
		"file_count": len(result.Files),
		"has_stdout": len(result.Stdout) > 0,
		"has_stderr": len(result.Stderr) > 0,
	}).Info("Advanced code execution completed")

	return &result, nil
}
