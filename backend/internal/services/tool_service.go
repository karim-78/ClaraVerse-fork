package services

import (
	"claraverse/internal/tools"
	"context"
	"log"
	"strings"
)

// ToolService handles tool-related operations with credential awareness.
// It filters tools based on user's configured credentials to ensure
// only usable tools are sent to the LLM.
type ToolService struct {
	toolRegistry      *tools.Registry
	credentialService *CredentialService
}

// NewToolService creates a new tool service
func NewToolService(registry *tools.Registry, credentialService *CredentialService) *ToolService {
	return &ToolService{
		toolRegistry:      registry,
		credentialService: credentialService,
	}
}

// GetAvailableTools returns tools filtered by user's credentials.
// - Tools not in ToolIntegrationMap are always included (no credential needed)
// - Tools in ToolIntegrationMap are only included if user has a credential for that integration type
func (s *ToolService) GetAvailableTools(ctx context.Context, userID string) []map[string]interface{} {
	// Get all tools for user (built-in + MCP)
	allTools := s.toolRegistry.GetUserTools(userID)

	// If no credential service, return all tools (fallback for dev mode or tests)
	if s.credentialService == nil {
		log.Printf("⚠️ [TOOL-SERVICE] No credential service, returning all %d tools", len(allTools))
		return allTools
	}

	// Get user's configured integration types
	userIntegrations, err := s.GetUserIntegrationTypes(ctx, userID)
	if err != nil {
		log.Printf("⚠️ [TOOL-SERVICE] Could not fetch user credentials, returning all tools: %v", err)
		return allTools // Graceful degradation
	}

	// Filter tools based on credential requirements
	var filteredTools []map[string]interface{}
	excludedCount := 0

	for _, toolDef := range allTools {
		toolName := extractToolName(toolDef)
		if toolName == "" {
			continue
		}

		// Check if this is an MCP tool (user-specific local client tools)
		isMCPTool := isUserSpecificTool(toolDef, userID)

		// Check if tool requires a credential
		requiredIntegration := tools.GetIntegrationTypeForTool(toolName)

		if requiredIntegration == "" {
			// Tool is NOT in integration mapping
			if isMCPTool {
				// MCP tools without explicit mapping are excluded by default (security)
				// Only include MCP tools that are explicitly mapped as not needing credentials
				log.Printf("🔒 [TOOL-SERVICE] Excluding unmapped MCP tool: %s (requires explicit mapping)", toolName)
				excludedCount++
			} else {
				// Built-in tool that doesn't require credentials - always include
				filteredTools = append(filteredTools, toolDef)
			}
		} else if userIntegrations[requiredIntegration] {
			// Tool requires credentials AND user has them - include
			filteredTools = append(filteredTools, toolDef)
		} else {
			// Tool requires credentials user doesn't have - exclude
			excludedCount++
		}
	}

	log.Printf("🔧 [TOOL-SERVICE] Filtered tools for user %s: %d available, %d excluded (missing credentials)",
		userID, len(filteredTools), excludedCount)

	return filteredTools
}

// GetUserIntegrationTypes returns a set of integration types the user has credentials for
func (s *ToolService) GetUserIntegrationTypes(ctx context.Context, userID string) (map[string]bool, error) {
	if s.credentialService == nil {
		return make(map[string]bool), nil
	}

	credentials, err := s.credentialService.ListByUser(ctx, userID)
	if err != nil {
		return nil, err
	}

	integrations := make(map[string]bool)
	for _, cred := range credentials {
		integrations[cred.IntegrationType] = true
	}

	return integrations, nil
}

// extractToolName extracts the tool name from an OpenAI tool definition
func extractToolName(toolDef map[string]interface{}) string {
	fn, ok := toolDef["function"].(map[string]interface{})
	if !ok {
		return ""
	}
	name, ok := fn["name"].(string)
	if !ok {
		return ""
	}
	return name
}

// isUserSpecificTool checks if a tool is an MCP tool (user-specific, not built-in)
// MCP tools are registered per-user and should be filtered by credentials by default
func isUserSpecificTool(toolDef map[string]interface{}, userID string) bool {
	// Check if tool has user_id metadata (MCP tools have this)
	if metadata, ok := toolDef["metadata"].(map[string]interface{}); ok {
		if toolUserID, ok := metadata["user_id"].(string); ok && toolUserID == userID {
			return true
		}
	}

	// Fallback: Check if tool name suggests it's an MCP tool
	// MCP tools often have specific naming patterns (e.g., containing "gmail", "calendar", "notion", etc.)
	toolName := extractToolName(toolDef)
	mcpPatterns := []string{
		"gmail", "calendar", "drive", "sheets", "docs", "slack", "discord",
		"notion", "trello", "asana", "jira", "linear", "github", "gitlab",
		"spotify", "twitter", "youtube", "reddit", "instagram",
	}

	toolNameLower := strings.ToLower(toolName)
	for _, pattern := range mcpPatterns {
		if strings.Contains(toolNameLower, pattern) {
			// Check if it's NOT a built-in Composio tool (which start with the integration name)
			// Built-in: "gmail_send_email", MCP: "send_gmail_message"
			if !strings.HasPrefix(toolNameLower, pattern+"_") {
				return true
			}
		}
	}

	return false
}

// GetAvailableToolsWithMCP returns tools filtered by credentials, but includes MCP tools
// even if they don't have explicit integration mappings. This is used by Telegram channels
// and routines that need access to Dobby's Claw MCP tools (filesystem, git, etc.).
func (s *ToolService) GetAvailableToolsWithMCP(ctx context.Context, userID string) []map[string]interface{} {
	allTools := s.toolRegistry.GetUserTools(userID)

	if s.credentialService == nil {
		log.Printf("⚠️ [TOOL-SERVICE] No credential service, returning all %d tools (with MCP)", len(allTools))
		return allTools
	}

	userIntegrations, err := s.GetUserIntegrationTypes(ctx, userID)
	if err != nil {
		log.Printf("⚠️ [TOOL-SERVICE] Could not fetch user credentials, returning all tools: %v", err)
		return allTools
	}

	var filteredTools []map[string]interface{}
	excludedCount := 0

	for _, toolDef := range allTools {
		toolName := extractToolName(toolDef)
		if toolName == "" {
			continue
		}

		isMCPTool := isUserSpecificTool(toolDef, userID)
		requiredIntegration := tools.GetIntegrationTypeForTool(toolName)

		if requiredIntegration == "" {
			// No integration mapping — include both built-in and MCP tools
			filteredTools = append(filteredTools, toolDef)
		} else if userIntegrations[requiredIntegration] {
			filteredTools = append(filteredTools, toolDef)
		} else {
			excludedCount++
		}

		_ = isMCPTool // used by isUserSpecificTool check above
	}

	log.Printf("🔧 [TOOL-SERVICE] Filtered tools (MCP-inclusive) for user %s: %d available, %d excluded",
		userID, len(filteredTools), excludedCount)

	return filteredTools
}

// GetCredentialForTool returns the credential ID for a tool that requires credentials.
// Returns empty string if no credential is needed or not found.
func (s *ToolService) GetCredentialForTool(ctx context.Context, userID string, toolName string) string {
	if s.credentialService == nil {
		return ""
	}

	// Check if tool requires a credential
	integrationType := tools.GetIntegrationTypeForTool(toolName)
	if integrationType == "" {
		return "" // Tool doesn't require credentials
	}

	// Get credentials for this integration type
	credentials, err := s.credentialService.ListByUserAndType(ctx, userID, integrationType)
	if err != nil {
		log.Printf("⚠️ [TOOL-SERVICE] Error getting credentials for %s: %v", integrationType, err)
		return ""
	}

	if len(credentials) == 0 {
		log.Printf("⚠️ [TOOL-SERVICE] No %s credentials found for user %s", integrationType, userID)
		return ""
	}

	// Use the first credential (or the only one)
	credentialID := credentials[0].ID
	log.Printf("🔐 [TOOL-SERVICE] Found credential %s for tool %s (type: %s)", credentialID, toolName, integrationType)
	return credentialID
}

// CreateCredentialResolver creates a credential resolver function for a user.
// Returns nil if credential service is not available.
func (s *ToolService) CreateCredentialResolver(userID string) tools.CredentialResolver {
	if s.credentialService == nil {
		return nil
	}
	return s.credentialService.CreateCredentialResolver(userID)
}

// GetCredentialService returns the underlying credential service (for advanced use cases)
func (s *ToolService) GetCredentialService() *CredentialService {
	return s.credentialService
}
