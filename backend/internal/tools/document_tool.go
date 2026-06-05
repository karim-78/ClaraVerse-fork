package tools

import (
	"claraverse/internal/document"
	"claraverse/internal/securefile"
	"encoding/json"
	"fmt"
	"log"
	"os"
)

// NewDocumentTool creates the create_document tool
func NewDocumentTool() *Tool {
	return &Tool{
		Name:        "create_document",
		DisplayName: "Create Document",
		Description: `Creates a professional PDF document from custom HTML content. Full creative control with HTML/CSS for maximum design flexibility.

Perfect for:
- Professional reports with custom branding
- Invoices and receipts with styled layouts
- Legal documents with precise formatting
- Technical documentation with code blocks
- Certificates and formal documents
- Creative documents with custom designs

You can use any HTML/CSS - inline styles, flexbox/grid layouts, custom fonts, colors, gradients, tables, images (base64 or URLs), etc. The document is rendered as a standard A4 portrait PDF and stored for 30 days with an access code for download.

**Page Break Control:**
- Use CSS 'page-break-before', 'page-break-after', or 'page-break-inside' to control page breaks
- Add 'page-break-inside: avoid' to prevent elements from being split across pages
- Use 'page-break-after: always' to force a new page after an element
- Tables, images, and code blocks should have 'page-break-inside: avoid' to prevent awkward cuts
- Example: <div style="page-break-inside: avoid;">Content that stays together</div>` + ReportStyleGuide,
		Icon:        "FileText",
		Parameters: map[string]interface{}{
			"type": "object",
			"properties": map[string]interface{}{
				"html": map[string]interface{}{
					"type":        "string",
					"description": "The document content as HTML. Can include inline CSS (<style> tags), custom fonts, images, tables, lists, and any web styling. Full creative freedom!",
				},
				"filename": map[string]interface{}{
					"type":        "string",
					"description": "Desired filename (without .pdf extension). Default: 'document'",
				},
				"title": map[string]interface{}{
					"type":        "string",
					"description": "Document title (shown in browser tab and PDF metadata). Default: 'Generated Document'",
				},
			},
			"required": []string{"html"},
		},
		Execute:  executeCreateDocument,
		Source:   ToolSourceBuiltin,
		Category: "output",
		Keywords: []string{"document", "pdf", "create", "generate", "file", "export", "html", "report", "write", "custom", "design"},
	}
}

func executeCreateDocument(args map[string]interface{}) (string, error) {
	// Extract parameters
	htmlContent, ok := args["html"].(string)
	if !ok || htmlContent == "" {
		return "", fmt.Errorf("html content is required")
	}

	filename, _ := args["filename"].(string)
	if filename == "" {
		filename = "document"
	}

	title, _ := args["title"].(string)
	if title == "" {
		title = "Generated Document"
	}

	// Extract injected user context (set by ChatService)
	userID, _ := args["__user_id__"].(string)
	if userID == "" {
		userID = "system" // Fallback for tools executed outside user context
	}
	conversationID, _ := args["__conversation_id__"].(string)

	// Clean up internal parameters before logging
	delete(args, "__user_id__")
	delete(args, "__conversation_id__")

	log.Printf("📄 [DOCUMENT-TOOL] Generating custom HTML document: %s (user: %s, length: %d chars)", filename, userID, len(htmlContent))

	// Inject the margin-safe / break-safe Migi base stylesheet (the renderer
	// uses zero page margins, so this prevents edge-to-edge + split content).
	htmlContent = injectReportBaseCSS(htmlContent)

	// Get document service to generate PDF
	documentService := document.GetService()

	// Generate the PDF document from HTML
	tempResult, err := documentService.GenerateDocumentFromHTML(htmlContent, filename, title, userID, conversationID)
	if err != nil {
		log.Printf("❌ [DOCUMENT-TOOL] Failed to generate document: %v", err)
		return "", fmt.Errorf("failed to generate document: %w", err)
	}

	// Read the generated PDF file
	pdfContent, err := os.ReadFile(tempResult.FilePath)
	if err != nil {
		log.Printf("❌ [DOCUMENT-TOOL] Failed to read generated PDF: %v", err)
		return "", fmt.Errorf("failed to read generated PDF: %w", err)
	}

	// Store in secure file service with 30-day retention and access code
	secureFileService := securefile.GetService()
	secureResult, err := secureFileService.CreateFile(userID, pdfContent, tempResult.Filename, "application/pdf")
	if err != nil {
		log.Printf("❌ [DOCUMENT-TOOL] Failed to store document securely: %v", err)
		return "", fmt.Errorf("failed to store document: %w", err)
	}

	// Delete the temporary PDF file (cleanup)
	if err := os.Remove(tempResult.FilePath); err != nil {
		log.Printf("⚠️ [DOCUMENT-TOOL] Failed to cleanup temp file: %v", err)
	}

	// Format result for AI
	response := map[string]interface{}{
		"success":      true,
		"file_id":      secureResult.ID,
		"filename":     secureResult.Filename,
		"download_url": secureResult.DownloadURL,
		"access_code":  secureResult.AccessCode,
		"size":         secureResult.Size,
		"expires_at":   secureResult.ExpiresAt.Format("2006-01-02"),
		"message":      fmt.Sprintf("Document '%s' created successfully. Download link (valid for 30 days): %s", secureResult.Filename, secureResult.DownloadURL),
	}

	responseJSON, _ := json.Marshal(response)

	log.Printf("✅ [DOCUMENT-TOOL] Document generated and stored securely: %s (%d bytes, expires: %s)",
		secureResult.Filename, secureResult.Size, secureResult.ExpiresAt.Format("2006-01-02"))

	return string(responseJSON), nil
}
