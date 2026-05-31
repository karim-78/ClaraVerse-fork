package tools

import (
	"claraverse/internal/filecache"
	"claraverse/internal/vision"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// NewDescribeImageTool creates the describe_image tool for AI image analysis
func NewDescribeImageTool() *Tool {
	return &Tool{
		Name:        "describe_image",
		DisplayName: "Describe Image",
		Description: `Analyzes an image using AI vision and returns a detailed text description.

Use this tool when the user asks you to:
- Describe what's in an image
- Analyze the content of a picture
- Answer questions about an image
- Identify objects, people, or text in an image

Parameters:
- image_url: A direct URL to an image on the web (e.g., "https://example.com/image.jpg"). Supports http/https URLs.
- image_id: The image handle (e.g., "img-1") from the available images list. Use this for generated or previously referenced images.
- file_id: Alternative - use the direct file ID from an upload response
- question: Optional specific question about the image
- detail: "brief" for 1-2 sentences, "detailed" for comprehensive description

You must provide one of: image_url, image_id, OR file_id. Use image_url for web images, image_id for generated/edited images, file_id for uploaded files.`,
		Icon: "Image",
		Parameters: map[string]interface{}{
			"type": "object",
			"properties": map[string]interface{}{
				"image_url": map[string]interface{}{
					"type":        "string",
					"description": "A direct URL to an image on the web (e.g., 'https://example.com/image.jpg'). Use this to analyze images from the internet.",
				},
				"image_id": map[string]interface{}{
					"type":        "string",
					"description": "The image handle (e.g., 'img-1') from the available images list. Preferred for generated or edited images.",
				},
				"file_id": map[string]interface{}{
					"type":        "string",
					"description": "Alternative: The direct file ID of the uploaded image. Use image_id when available.",
				},
				"question": map[string]interface{}{
					"type":        "string",
					"description": "Optional specific question about the image (e.g., 'What color is the car?', 'How many people are in this photo?')",
				},
				"detail": map[string]interface{}{
					"type":        "string",
					"enum":        []string{"brief", "detailed"},
					"description": "Level of detail: 'brief' for 1-2 sentences, 'detailed' for comprehensive description. Default is 'detailed'",
				},
			},
			"required": []string{},
		},
		Execute:  executeDescribeImage,
		Source:   ToolSourceBuiltin,
		Category: "data_sources",
		Keywords: []string{"image", "describe", "analyze", "vision", "picture", "photo", "screenshot", "diagram", "chart", "url"},
	}
}

// Constants for URL image fetching
const (
	describeImageMaxSize = 20 * 1024 * 1024 // 20MB for images
	describeImageTimeout = 30 * time.Second
)

func executeDescribeImage(args map[string]interface{}) (string, error) {
	// Extract image_url, image_id (handle like "img-1") or file_id (direct UUID)
	imageURL, hasImageURL := args["image_url"].(string)
	imageID, hasImageID := args["image_id"].(string)
	fileID, hasFileID := args["file_id"].(string)

	if (!hasImageURL || imageURL == "") && (!hasImageID || imageID == "") && (!hasFileID || fileID == "") {
		return "", fmt.Errorf("one of image_url, image_id, or file_id is required. Use image_url for web images, image_id (e.g., 'img-1') for generated images, or file_id for uploaded files")
	}

	// Extract optional question parameter
	question := ""
	if q, ok := args["question"].(string); ok {
		question = q
	}

	// Extract detail level (default to "detailed")
	detail := "detailed"
	if d, ok := args["detail"].(string); ok && (d == "brief" || d == "detailed") {
		detail = d
	}

	// Extract user context (injected by tool executor)
	userID, _ := args["__user_id__"].(string)
	convID, _ := args["__conversation_id__"].(string)

	// Variables to hold image data and metadata
	var imageData []byte
	var mimeType string
	var filename string
	var resolvedFileID string
	var sourceURL string

	// If image_url is provided, fetch the image directly from the web
	if hasImageURL && imageURL != "" {
		log.Printf("🖼️ [DESCRIBE-IMAGE] Fetching image from URL: %s", imageURL)

		data, mime, fname, err := fetchImageFromURL(imageURL)
		if err != nil {
			log.Printf("❌ [DESCRIBE-IMAGE] Failed to fetch image from URL: %v", err)
			return "", fmt.Errorf("failed to fetch image from URL: %v", err)
		}

		imageData = data
		mimeType = mime
		filename = fname
		sourceURL = imageURL
		resolvedFileID = "url-image"

		log.Printf("✅ [DESCRIBE-IMAGE] Fetched image from URL: %s (%d bytes, %s)", filename, len(imageData), mimeType)
	} else {
		// Get file cache service for image_id or file_id
		fileCacheService := filecache.GetService()
		var file *filecache.CachedFile

		// If image_id is provided, resolve it via the registry
		if hasImageID && imageID != "" {
			// Get image registry (injected by chat_service)
			registry, ok := args[ImageRegistryKey].(ImageRegistryInterface)
			if !ok || registry == nil {
				// Registry not available - try to use image_id as file_id fallback
				log.Printf("⚠️ [DESCRIBE-IMAGE] Image registry not available, treating image_id as file_id")
				resolvedFileID = imageID
			} else {
				// Look up the image by handle
				entry := registry.GetByHandle(convID, imageID)
				if entry == nil {
					// Provide helpful error message with available handles
					handles := registry.ListHandles(convID)
					if len(handles) == 0 {
						return "", fmt.Errorf("image '%s' not found. No images are available in this conversation. Please upload an image first or use file_id for direct file access", imageID)
					}
					return "", fmt.Errorf("image '%s' not found. Available images: %s", imageID, strings.Join(handles, ", "))
				}
				resolvedFileID = entry.FileID
				log.Printf("🖼️ [DESCRIBE-IMAGE] Resolved image_id '%s' to file_id '%s'", imageID, resolvedFileID)
			}
		} else {
			// Use file_id directly
			resolvedFileID = fileID
		}

		log.Printf("🖼️ [DESCRIBE-IMAGE] Analyzing image file_id=%s detail=%s (user=%s, conv=%s)", resolvedFileID, detail, userID, convID)

		// Get file from cache with proper validation
		if userID != "" && convID != "" {
			var err error
			file, err = fileCacheService.GetByUserAndConversation(resolvedFileID, userID, convID)
			if err != nil {
				// Try with just user validation
				file, err = fileCacheService.GetByUser(resolvedFileID, userID)
				if err != nil {
					// Try without validation for workflow context
					file, _ = fileCacheService.Get(resolvedFileID)
					if file != nil && file.UserID != "" && file.UserID != userID {
						log.Printf("🚫 [DESCRIBE-IMAGE] Access denied: file %s belongs to different user", resolvedFileID)
						return "", fmt.Errorf("access denied: you don't have permission to access this file")
					}
				}
			}
		} else if userID != "" {
			var err error
			file, err = fileCacheService.GetByUser(resolvedFileID, userID)
			if err != nil {
				file, _ = fileCacheService.Get(resolvedFileID)
			}
		} else {
			file, _ = fileCacheService.Get(resolvedFileID)
		}

		if file == nil {
			log.Printf("❌ [DESCRIBE-IMAGE] File not found: %s", resolvedFileID)
			if hasImageID && imageID != "" {
				return "", fmt.Errorf("image '%s' has expired or is no longer available. Images are cached for 30 minutes. Please upload or generate the image again", imageID)
			}
			return "", fmt.Errorf("image file not found or has expired. Files are only available for 30 minutes after upload")
		}

		// Validate it's an image
		if !strings.HasPrefix(file.MimeType, "image/") {
			log.Printf("⚠️ [DESCRIBE-IMAGE] File is not an image: %s (%s)", resolvedFileID, file.MimeType)
			return "", fmt.Errorf("file is not an image (type: %s). Use read_document for documents or read_data_file for data files", file.MimeType)
		}

		// Read image data from disk
		if file.FilePath == "" {
			return "", fmt.Errorf("image file path not available")
		}

		var err error
		imageData, err = os.ReadFile(file.FilePath)
		if err != nil {
			log.Printf("❌ [DESCRIBE-IMAGE] Failed to read image from disk: %v (path: %s)", err, file.FilePath)
			return "", fmt.Errorf("image file has expired or been deleted. Please upload or generate the image again")
		}

		mimeType = file.MimeType
		filename = file.Filename
	}

	// Get the vision service
	visionService := vision.GetService()
	if visionService == nil {
		return "", fmt.Errorf("vision service not available. Please configure a vision-capable model (e.g., GPT-4o)")
	}

	// Build the request
	req := &vision.DescribeImageRequest{
		ImageData: imageData,
		MimeType:  mimeType,
		Question:  question,
		Detail:    detail,
	}

	// Call vision service
	result, err := visionService.DescribeImage(req)
	if err != nil {
		log.Printf("❌ [DESCRIBE-IMAGE] Vision analysis failed: %v", err)
		return "", fmt.Errorf("failed to analyze image: %v", err)
	}

	// Build response
	response := map[string]interface{}{
		"success":     true,
		"filename":    filename,
		"mime_type":   mimeType,
		"description": result.Description,
		"model":       result.Model,
		"provider":    result.Provider,
	}

	// Include source-specific fields
	if sourceURL != "" {
		response["source_url"] = sourceURL
	} else {
		response["file_id"] = resolvedFileID
	}

	// Include image_id if it was used
	if hasImageID && imageID != "" {
		response["image_id"] = imageID
	}

	if question != "" {
		response["question"] = question
	}

	responseJSON, err := json.Marshal(response)
	if err != nil {
		return "", fmt.Errorf("failed to marshal response: %w", err)
	}

	log.Printf("✅ [DESCRIBE-IMAGE] Successfully described image %s using %s", filename, result.Model)

	return string(responseJSON), nil
}

// fetchImageFromURL downloads an image from a URL and returns the data, mime type, and filename
func fetchImageFromURL(urlStr string) ([]byte, string, string, error) {
	// Validate URL using the existing validation function from download_file_tool
	parsedURL, err := validateDownloadURL(urlStr)
	if err != nil {
		return nil, "", "", fmt.Errorf("invalid URL: %v", err)
	}

	// Create HTTP client with timeout
	client := &http.Client{
		Timeout: describeImageTimeout,
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			if len(via) >= 5 {
				return fmt.Errorf("too many redirects")
			}
			// Validate redirect URL
			if _, err := validateDownloadURL(req.URL.String()); err != nil {
				return fmt.Errorf("redirect blocked: %v", err)
			}
			return nil
		},
	}

	// Create request
	req, err := http.NewRequest("GET", parsedURL.String(), nil)
	if err != nil {
		return nil, "", "", fmt.Errorf("failed to create request: %v", err)
	}

	// Set a reasonable User-Agent
	req.Header.Set("User-Agent", "ClaraVerse/1.0 (Image Analyzer)")

	// Make request
	resp, err := client.Do(req)
	if err != nil {
		return nil, "", "", fmt.Errorf("failed to fetch image: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, "", "", fmt.Errorf("failed to fetch image: HTTP %d", resp.StatusCode)
	}

	// Get content type
	contentType := resp.Header.Get("Content-Type")
	if contentType == "" {
		contentType = "application/octet-stream"
	}
	// Strip charset suffix if present
	if idx := strings.Index(contentType, ";"); idx != -1 {
		contentType = strings.TrimSpace(contentType[:idx])
	}

	// Validate it's an image
	if !strings.HasPrefix(contentType, "image/") {
		// Try to detect from URL extension
		ext := strings.ToLower(filepath.Ext(parsedURL.Path))
		switch ext {
		case ".jpg", ".jpeg":
			contentType = "image/jpeg"
		case ".png":
			contentType = "image/png"
		case ".gif":
			contentType = "image/gif"
		case ".webp":
			contentType = "image/webp"
		case ".svg":
			contentType = "image/svg+xml"
		case ".bmp":
			contentType = "image/bmp"
		default:
			return nil, "", "", fmt.Errorf("URL does not point to an image (content-type: %s)", contentType)
		}
	}

	// Check content length if available
	if resp.ContentLength > describeImageMaxSize {
		return nil, "", "", fmt.Errorf("image too large: %d bytes (max %d bytes)", resp.ContentLength, describeImageMaxSize)
	}

	// Read body with size limit
	limitedReader := io.LimitReader(resp.Body, describeImageMaxSize+1)
	content, err := io.ReadAll(limitedReader)
	if err != nil {
		return nil, "", "", fmt.Errorf("failed to read image: %v", err)
	}

	if int64(len(content)) > describeImageMaxSize {
		return nil, "", "", fmt.Errorf("image too large: max %d bytes", describeImageMaxSize)
	}

	// Extract filename from URL or Content-Disposition
	filename := extractFilename(parsedURL, contentType, resp.Header.Get("Content-Disposition"))
	filename = sanitizeFilename(filename)

	return content, contentType, filename, nil
}
