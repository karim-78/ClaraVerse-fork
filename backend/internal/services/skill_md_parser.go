package services

import (
	"claraverse/internal/models"
	"fmt"
	"strings"
	"time"
	"unicode"

	"gopkg.in/yaml.v3"
)

const maxSkillMDSize = 100 * 1024 // 100KB

// SkillMDFrontmatter represents the YAML frontmatter of a SKILL.md file
type SkillMDFrontmatter struct {
	Name          string            `yaml:"name"`
	Description   string            `yaml:"description"`
	License       string            `yaml:"license"`
	Compatibility string            `yaml:"compatibility"`
	AllowedTools  interface{}       `yaml:"allowed-tools"` // string or []interface{}
	Metadata      map[string]string `yaml:"metadata"`
}

// ParseSkillMD parses SKILL.md content into frontmatter + markdown body.
// Returns (frontmatter, body, error). If no frontmatter delimiters are found,
// the entire content is treated as the body with an empty frontmatter.
func ParseSkillMD(content string) (*SkillMDFrontmatter, string, error) {
	if len(content) > maxSkillMDSize {
		return nil, "", fmt.Errorf("content exceeds maximum size of %d bytes", maxSkillMDSize)
	}

	// Normalize line endings
	content = strings.ReplaceAll(content, "\r\n", "\n")
	content = strings.TrimSpace(content)

	if content == "" {
		return nil, "", fmt.Errorf("empty content")
	}

	fm := &SkillMDFrontmatter{}

	// Check for YAML frontmatter delimiters
	if !strings.HasPrefix(content, "---\n") && !strings.HasPrefix(content, "---\r") {
		// No frontmatter — entire content is the body
		return fm, content, nil
	}

	// Find closing delimiter (skip the opening "---\n")
	rest := content[4:]
	closingIdx := strings.Index(rest, "\n---")
	if closingIdx == -1 {
		// Only opening delimiter, no closing — treat entire content as body
		return fm, content, nil
	}

	yamlContent := rest[:closingIdx]
	body := strings.TrimSpace(rest[closingIdx+4:]) // skip "\n---"

	if err := yaml.Unmarshal([]byte(yamlContent), fm); err != nil {
		return nil, "", fmt.Errorf("invalid YAML frontmatter: %w", err)
	}

	return fm, body, nil
}

// resolveAllowedTools extracts tool names from the allowed-tools field,
// which can be a space-delimited string or a YAML list.
func resolveAllowedTools(raw interface{}) []string {
	if raw == nil {
		return nil
	}

	switch v := raw.(type) {
	case string:
		if v == "" {
			return nil
		}
		// Space-delimited or comma-delimited
		parts := strings.FieldsFunc(v, func(r rune) bool {
			return r == ' ' || r == ','
		})
		result := make([]string, 0, len(parts))
		for _, p := range parts {
			p = strings.TrimSpace(p)
			if p != "" {
				result = append(result, p)
			}
		}
		return result
	case []interface{}:
		result := make([]string, 0, len(v))
		for _, item := range v {
			if s, ok := item.(string); ok && s != "" {
				result = append(result, strings.TrimSpace(s))
			}
		}
		return result
	}

	return nil
}

// kebabToTitleCase converts "my-skill-name" to "My Skill Name"
func kebabToTitleCase(s string) string {
	words := strings.Split(s, "-")
	for i, w := range words {
		if len(w) > 0 {
			runes := []rune(w)
			runes[0] = unicode.ToUpper(runes[0])
			words[i] = string(runes)
		}
	}
	return strings.Join(words, " ")
}

// SkillMDToSkill converts parsed SKILL.md data to a ClaraVerse Skill.
func SkillMDToSkill(fm *SkillMDFrontmatter, body string) *models.Skill {
	name := fm.Name
	displayName := name
	if name != "" {
		displayName = kebabToTitleCase(name)
	}

	description := fm.Description
	if description == "" && body != "" {
		// Use first 150 chars of the body as description
		desc := body
		if len(desc) > 150 {
			desc = desc[:150] + "..."
		}
		// Take only the first line/paragraph
		if idx := strings.Index(desc, "\n\n"); idx > 0 {
			desc = desc[:idx]
		}
		description = strings.TrimPrefix(desc, "# ")
		description = strings.TrimSpace(description)
	}

	category := inferCategory(name, description+" "+body)
	icon := categoryDefaultIcon(category)

	version := "1.0"
	authorID := ""
	if fm.Metadata != nil {
		if v, ok := fm.Metadata["version"]; ok {
			version = v
		}
		if a, ok := fm.Metadata["author"]; ok {
			authorID = a
		}
	}

	now := time.Now()
	return &models.Skill{
		Name:            displayName,
		Description:     description,
		Icon:            icon,
		Category:        category,
		SystemPrompt:    body,
		RequiredTools:   resolveAllowedTools(fm.AllowedTools),
		PreferredServers: []string{},
		Keywords:        []string{},
		TriggerPatterns: []string{},
		Mode:            "manual",
		IsBuiltin:       false,
		AuthorID:        authorID,
		Version:         version,
		CreatedAt:       now,
		UpdatedAt:       now,
	}
}

// SkillToSkillMD converts a ClaraVerse Skill to SKILL.md format.
func SkillToSkillMD(skill *models.Skill) string {
	var sb strings.Builder

	sb.WriteString("---\n")
	// Convert name to kebab-case
	kebabName := strings.ToLower(strings.ReplaceAll(skill.Name, " ", "-"))
	sb.WriteString(fmt.Sprintf("name: %s\n", kebabName))

	if skill.Description != "" {
		// Quote description if it contains special chars
		sb.WriteString(fmt.Sprintf("description: %q\n", skill.Description))
	}

	if len(skill.RequiredTools) > 0 {
		sb.WriteString(fmt.Sprintf("allowed-tools: %s\n", strings.Join(skill.RequiredTools, " ")))
	}

	// Metadata
	sb.WriteString("metadata:\n")
	if skill.AuthorID != "" {
		sb.WriteString(fmt.Sprintf("  author: %s\n", skill.AuthorID))
	}
	if skill.Version != "" {
		sb.WriteString(fmt.Sprintf("  version: %q\n", skill.Version))
	}

	sb.WriteString("---\n\n")
	sb.WriteString(skill.SystemPrompt)
	sb.WriteString("\n")

	return sb.String()
}

// inferCategory guesses category from name + description text.
func inferCategory(name, description string) string {
	text := strings.ToLower(name + " " + description)

	categoryKeywords := map[string][]string{
		"research":           {"research", "search", "web", "find", "lookup", "news", "fact", "competitor", "seo", "market"},
		"communication":      {"email", "slack", "discord", "message", "chat", "notify", "whatsapp", "telegram", "sms", "inbox"},
		"project-management": {"github", "jira", "linear", "trello", "issue", "project", "sprint", "kanban", "gitlab", "clickup", "asana", "bug report"},
		"data":               {"data", "analytics", "spreadsheet", "database", "csv", "chart", "statistics", "posthog", "mixpanel", "survey", "inventory"},
		"content":            {"blog", "content", "social", "image", "design", "presentation", "video", "canva", "infographic", "tweet", "linkedin post"},
		"productivity":       {"calendar", "file", "schedule", "organize", "task", "meeting", "briefing", "zoom", "drive", "notion journal", "reminder"},
		"sales":              {"sales", "lead", "crm", "hubspot", "outreach", "campaign", "linkedin outreach", "prospect", "proposal", "pipeline", "leadsquared"},
		"code":               {"code", "python", "api", "deploy", "test", "debug", "build", "devops", "mcp", "regex", "webhook", "json", "cron", "hash"},
		"database":           {"mongodb", "redis", "sql", "s3", "storage", "query", "cache", "backup"},
		"ecommerce":          {"shopify", "order", "product catalog", "e-commerce", "ecommerce", "store", "shop", "customer lookup", "stock alert"},
		"writing":            {"write", "grammar", "proofread", "summarize", "resume", "cover letter", "contract", "invoice", "transcript", "minutes", "document"},
	}

	bestCategory := "productivity"
	bestScore := 0
	for cat, keywords := range categoryKeywords {
		score := 0
		for _, kw := range keywords {
			if strings.Contains(text, kw) {
				score++
			}
		}
		if score > bestScore {
			bestScore = score
			bestCategory = cat
		}
	}
	return bestCategory
}

// categoryDefaultIcon returns the default lucide icon name for a category.
func categoryDefaultIcon(category string) string {
	icons := map[string]string{
		"research":           "search",
		"communication":      "mail",
		"project-management": "briefcase",
		"data":               "bar-chart-3",
		"content":            "pen-tool",
		"productivity":       "zap",
		"sales":              "briefcase",
		"code":               "code",
		"database":           "database",
		"ecommerce":          "shopping-cart",
		"writing":            "file-text",
	}
	if icon, ok := icons[category]; ok {
		return icon
	}
	return "zap"
}
