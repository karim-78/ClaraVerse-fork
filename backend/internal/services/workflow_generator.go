package services

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"regexp"
	"strings"
	"time"

	"github.com/google/uuid"

	"claraverse/internal/database"
	"claraverse/internal/models"
)

// WorkflowGeneratorService handles workflow generation with structured output
type WorkflowGeneratorService struct {
	db              *database.DB
	providerService *ProviderService
	chatService     *ChatService
}

// NewWorkflowGeneratorService creates a new workflow generator service
func NewWorkflowGeneratorService(
	db *database.DB,
	providerService *ProviderService,
	chatService *ChatService,
) *WorkflowGeneratorService {
	return &WorkflowGeneratorService{
		db:              db,
		providerService: providerService,
		chatService:     chatService,
	}
}

// V1ToolCategory is used for the legacy v1 dynamic tool injection
type V1ToolCategory struct {
	Name        string
	Keywords    []string
	Tools       string
	Description string
}

// v1ToolCategories defines tool categories for legacy v1 workflow generation
var v1ToolCategories = []V1ToolCategory{
	{Name: "data_analysis", Keywords: []string{"analyze", "analysis", "data", "csv", "excel", "spreadsheet", "chart", "graph", "statistics", "visualize", "visualization", "metrics", "calculate", "math"}, Description: "Data analysis and visualization", Tools: "📊 DATA & ANALYSIS:\n- analyze_data: Python data analysis with charts\n- calculate_math: Mathematical calculations\n- read_spreadsheet: Read Excel/CSV files\n- read_data_file: Read and parse data files\n- read_document: Extract text from documents"},
	{Name: "search_web", Keywords: []string{"search", "find", "lookup", "google", "web", "internet", "news", "articles", "scrape", "crawl", "download", "url", "website"}, Description: "Web search and scraping", Tools: "🔍 SEARCH & WEB:\n- search_web: Search the internet\n- search_images: Search for images\n- scrape_web: Scrape content from URL\n- download_file: Download a file from URL"},
	{Name: "content_creation", Keywords: []string{"create", "generate", "write", "document", "pdf", "docx", "presentation", "pptx", "powerpoint", "image", "picture", "photo", "text file", "html"}, Description: "Content creation", Tools: "📝 CONTENT CREATION:\n- create_document: Create DOCX or PDF\n- create_text_file: Create text files\n- create_presentation: Create PowerPoint\n- generate_image: Generate AI images\n- edit_image: Edit images\n- html_to_pdf: Convert HTML to PDF"},
	{Name: "media_processing", Keywords: []string{"audio", "transcribe", "speech", "voice", "mp3", "wav", "video", "image", "describe", "vision", "see", "look"}, Description: "Media processing", Tools: "🎤 MEDIA PROCESSING:\n- transcribe_audio: Transcribe audio\n- describe_image: Analyze images"},
	{Name: "utilities", Keywords: []string{"time", "date", "now", "today", "current", "python", "code", "script", "api", "http", "request", "webhook", "endpoint"}, Description: "Utilities", Tools: "⏰ UTILITIES:\n- get_current_time: Get current time\n- run_python: Execute Python code\n- api_request: Make HTTP requests\n- send_webhook: Send webhook"},
	{Name: "messaging", Keywords: []string{"discord", "slack", "telegram", "teams", "google chat", "email", "sms", "whatsapp", "message", "send", "notify", "notification", "alert", "chat"}, Description: "Messaging", Tools: "💬 MESSAGING:\n- send_discord_message: Discord\n- send_slack_message: Slack\n- send_telegram_message: Telegram\n- send_google_chat_message: Google Chat\n- send_teams_message: Teams\n- send_email: Email\n- twilio_send_sms: SMS\n- twilio_send_whatsapp: WhatsApp"},
	{Name: "video_conferencing", Keywords: []string{"zoom", "meeting", "webinar", "calendly", "calendar", "schedule", "event", "conference", "call", "register", "attendee"}, Description: "Video conferencing", Tools: "📹 VIDEO CONFERENCING:\n- zoom_meeting: Zoom meetings/webinars (actions: create, list, get, register, create_webinar, register_webinar)\n- calendly_events: Calendly events"},
	{Name: "project_management", Keywords: []string{"jira", "linear", "clickup", "trello", "asana", "task", "issue", "ticket", "project", "board", "kanban", "sprint", "backlog"}, Description: "Project management", Tools: "📋 PROJECT MANAGEMENT:\n- jira_issues/jira_create_issue/jira_update_issue\n- linear_issues/linear_create_issue/linear_update_issue\n- clickup_tasks/clickup_create_task/clickup_update_task\n- trello_boards/trello_lists/trello_cards/trello_create_card\n- asana_tasks"},
	{Name: "crm_sales", Keywords: []string{"hubspot", "leadsquared", "mailchimp", "crm", "lead", "contact", "deal", "sales", "customer", "subscriber", "marketing", "audience"}, Description: "CRM & Sales", Tools: "💼 CRM & SALES:\n- hubspot_contacts/hubspot_deals/hubspot_companies\n- leadsquared_leads/leadsquared_create_lead\n- mailchimp_lists/mailchimp_add_subscriber"},
	{Name: "analytics", Keywords: []string{"posthog", "mixpanel", "analytics", "track", "event", "identify", "user profile", "funnel", "cohort", "retention"}, Description: "Analytics", Tools: "📊 ANALYTICS:\n- posthog_capture/posthog_identify/posthog_query\n- mixpanel_track/mixpanel_user_profile"},
	{Name: "code_devops", Keywords: []string{"github", "gitlab", "netlify", "git", "repo", "repository", "issue", "pull request", "pr", "merge", "deploy", "build", "ci", "cd", "code"}, Description: "Code & DevOps", Tools: "🐙 CODE & DEVOPS:\n- github_create_issue/github_list_issues/github_get_repo/github_add_comment\n- gitlab_projects/gitlab_issues/gitlab_mrs\n- netlify_sites/netlify_deploys/netlify_trigger_build"},
	{Name: "productivity", Keywords: []string{"notion", "airtable", "database", "page", "note", "record", "table", "workspace", "wiki"}, Description: "Productivity", Tools: "📓 PRODUCTIVITY:\n- notion_search/notion_query_database/notion_create_page/notion_update_page\n- airtable_list/airtable_read/airtable_create/airtable_update"},
	{Name: "ecommerce", Keywords: []string{"shopify", "shop", "product", "order", "customer", "ecommerce", "store", "inventory", "cart"}, Description: "E-Commerce", Tools: "🛒 E-COMMERCE:\n- shopify_products/shopify_orders/shopify_customers"},
	{Name: "social_media", Keywords: []string{"twitter", "x", "tweet", "post", "social", "media", "follow", "user", "timeline"}, Description: "Social Media", Tools: "🐦 SOCIAL MEDIA:\n- x_search_posts/x_post_tweet/x_get_user/x_get_user_posts"},
}

// detectToolCategoriesV1 analyzes user message and returns relevant tool categories (legacy v1)
func detectToolCategoriesV1(userMessage string) []string {
	msg := strings.ToLower(userMessage)
	detected := make(map[string]bool)

	for _, category := range v1ToolCategories {
		for _, keyword := range category.Keywords {
			if strings.Contains(msg, keyword) {
				detected[category.Name] = true
				break
			}
		}
	}

	// Always include utilities for time-sensitive keywords
	timeSensitiveKeywords := []string{"today", "daily", "recent", "latest", "current", "now", "this week", "this month", "news", "trending", "breaking"}
	for _, keyword := range timeSensitiveKeywords {
		if strings.Contains(msg, keyword) {
			detected["utilities"] = true
			break
		}
	}

	// If no categories detected, return a default set
	if len(detected) == 0 {
		detected["data_analysis"] = true
		detected["search_web"] = true
		detected["utilities"] = true
		detected["content_creation"] = true
	}

	result := make([]string, 0, len(detected))
	for cat := range detected {
		result = append(result, cat)
	}
	return result
}

// buildDynamicToolsSectionV1 builds the tools section based on detected categories (legacy v1)
func buildDynamicToolsSectionV1(categories []string) string {
	var builder strings.Builder
	builder.WriteString("=== AVAILABLE TOOLS (Relevant to your request) ===\n\n")

	categoryMap := make(map[string]V1ToolCategory)
	for _, cat := range v1ToolCategories {
		categoryMap[cat.Name] = cat
	}

	for _, catName := range categories {
		if cat, ok := categoryMap[catName]; ok {
			builder.WriteString(cat.Tools)
			builder.WriteString("\n\n")
		}
	}

	return builder.String()
}

// buildDynamicSystemPrompt builds the complete system prompt with dynamically injected tools (legacy v1)
func buildDynamicSystemPrompt(userMessage string) string {
	categories := detectToolCategoriesV1(userMessage)
	toolsSection := buildDynamicToolsSectionV1(categories)
	prompt := strings.Replace(WorkflowSystemPromptBase, "{{DYNAMIC_TOOLS_SECTION}}", toolsSection, 1)
	log.Printf("🔧 [WORKFLOW-GEN] Detected tool categories: %v", categories)
	return prompt
}

// WorkflowSystemPromptBase is the base system prompt without tools section
const WorkflowSystemPromptBase = `You are a Dobby AI workflow generator. Your ONLY job is to output valid JSON workflow definitions.

CRITICAL: You must ONLY respond with a JSON object. No explanations, no markdown, no code blocks - JUST the JSON.

=== WORKFLOW STRUCTURE ===
{
  "blocks": [...],
  "connections": [...],
  "variables": [],
  "explanation": "Brief description of what the workflow does or what changed"
}

=== BLOCK TYPES ===
1. TRIGGER BLOCKS (Entry Points - Choose Based on Use Case)

   a) "webhook_trigger" - HTTP API / Webhook endpoint (DEFAULT for most workflows)
      Config: { "method": "POST" }
      Use when: User wants API, webhook, or interactive workflow they can trigger manually
      Output: Provides request data as {{webhook.body}}, {{webhook.query}}, {{webhook.headers}}
      Example: "Create an agent that..." → Default to webhook_trigger

   b) "schedule_trigger" - Scheduled/Cron job (for automation)
      Config: { "schedule": "0 9 * * *" }  // Cron format: minute hour day month weekday
      Use when: User explicitly mentions schedule/automation keywords
      Keywords: "every day", "daily", "hourly", "schedule", "automatically run", "cron"
      Example schedules:
        - "0 9 * * *" = Every day at 9 AM
        - "0 */6 * * *" = Every 6 hours
        - "0 0 * * 0" = Every Sunday at midnight
        - "*/15 * * * *" = Every 15 minutes

   TRIGGER SELECTION GUIDE:
   - User says "Create an agent..." → webhook_trigger (default)
   - User says "API endpoint" / "webhook" → webhook_trigger
   - User says "run daily" / "schedule" → schedule_trigger
   - User says "automation" / "automatically" → schedule_trigger
   - When unsure → webhook_trigger (most flexible)

2. "llm_inference" - AI agent with tools (EXECUTION MODE)
   Config: {
     "systemPrompt": "IMPERATIVE instructions - what the agent MUST do",
     "userPrompt": "{{input}}" or "{{previous-block.response}}",
     "temperature": 0.3,
     "enabledTools": ["tool_name"],
     "requiredTools": ["tool_name"],
     "requireToolUsage": true,
     "outputFormat": "json",
     "outputSchema": { JSON Schema object }
   }

3. "code_block" - Direct tool execution (NO LLM, FAST & DETERMINISTIC)
   Config: {
     "toolName": "tool_name_here",
     "argumentMapping": { "param1": "{{input}}", "param2": "{{block-id.response}}" }
   }

   USE code_block WHEN:
   - Task is PURELY mechanical (no reasoning/decisions needed)
   - All data and parameters are already available
   - Examples: get current time, send pre-formatted message, make API call with known params

   USE llm_inference INSTEAD WHEN:
   - Need to DECIDE what to search/do
   - Need to INTERPRET, FORMAT, or SUMMARIZE data
   - ANY intelligent decision-making is required

=== BLOCK TYPE DECISION GUIDE ===
Q: Does the task need ANY reasoning, decisions, or interpretation?
   YES → Use "llm_inference" (LLM calls tools with intelligence)
   NO  → Use "code_block" (direct execution, faster & cheaper)

EXAMPLES - When to use code_block:
- "Get current time" → code_block with toolName="get_current_time"
- "Send this exact message to Discord" (message already formatted) → code_block
- "Download file from URL" (URL provided) → code_block
- "Calculate 2+2" → code_block with toolName="calculate_math"

EXAMPLES - When to use llm_inference:
- "Search for news about X" → llm_inference (LLM decides search query)
- "Analyze this data" → llm_inference (LLM interprets results)
- "Format and send to Discord" → llm_inference (needs formatting decision)
- "Summarize the results" → llm_inference (needs interpretation)

=== SYSTEM PROMPT WRITING RULES (CRITICAL!) ===
System prompts MUST be written in IMPERATIVE/COMMAND style, not conversational:

CORRECT (Imperative - use these patterns):
- "Search for news about the topic. Call search_web. Return top 3 results with titles and summaries."
- "Send this content to Discord NOW using send_discord_message. Include embed_title."
- "Analyze the data. Generate a bar chart. Use analyze_data tool immediately."

WRONG (Conversational - NEVER use):
- "You should search for news..." (too passive)
- "Please format and send to Discord..." (too polite/optional)
- "Can you analyze this data..." (implies optionality)
- "If you want, you could..." (gives choice - NO!)

WRONG (Question-asking - NEVER generate prompts that ask questions):
- "What topic would you like to search?" (NO - data is provided)
- "Should I include charts?" (NO - decide based on context)
- "Would you like me to..." (NO - just do it)

{{DYNAMIC_TOOLS_SECTION}}

=== TOOL CONFIGURATION (CRITICAL FOR RELIABILITY!) ===
For each LLM block with tools, you MUST include:
- "enabledTools": List of tools the block CAN use
- "requiredTools": List of tools the block MUST use (usually same as enabledTools)
- "requireToolUsage": true (forces tool usage, prevents text-only responses)
- "temperature": 0.3 (low for deterministic execution)

Example for Discord Publisher block:
{
  "enabledTools": ["send_discord_message"],
  "requiredTools": ["send_discord_message"],
  "requireToolUsage": true,
  "temperature": 0.3
}

=== STRUCTURED OUTPUT (CRITICAL FOR RELIABILITY!) ===
ALWAYS use structured outputs for blocks that return data to be consumed by other blocks or rendered in UIs.
This ensures 100% predictable, parseable outputs.

When to use structured output:
- Data fetching blocks (news, search results, API data)
- Analysis blocks that return metrics or insights
- Any block whose output will be displayed in a UI
- Blocks that extract specific information

How to configure structured output:
1. Set "outputFormat": "json"
2. Define "outputSchema" with JSON Schema
3. The schema MUST match what downstream blocks or UI expect

Example for News Fetcher block:
{
  "systemPrompt": "FIRST call get_current_time. THEN call search_web for news. Return EXACTLY in the schema format.",
  "temperature": 0.3,
  "enabledTools": ["get_current_time", "search_web"],
  "requiredTools": ["get_current_time", "search_web"],
  "requireToolUsage": true,
  "outputFormat": "json",
  "outputSchema": {
    "type": "object",
    "properties": {
      "articles": {
        "type": "array",
        "items": {
          "type": "object",
          "properties": {
            "title": {"type": "string"},
            "source": {"type": "string"},
            "url": {"type": "string"},
            "summary": {"type": "string"},
            "publishedDate": {"type": "string"}
          },
          "required": ["title", "source", "url", "summary", "publishedDate"]
        }
      },
      "totalResults": {"type": "number"},
      "fetchedAt": {"type": "string"}
    },
    "required": ["articles", "totalResults", "fetchedAt"],
    "additionalProperties": false
  }
}

Common schema patterns:
- News/Articles WITH metadata: { articles: [{ title, source, url, summary, publishedDate }], totalResults, fetchedAt }
- Simple list (array at root): [{ id, name, value }] - use "type": "array" with "items" schema
- Metrics/Stats: { metrics: { key: value }, summary, analyzedAt }
- List Results: { items: [{ name, description, value }], count, retrievedAt }
- Analysis: { insights: [...], recommendations: [...], confidence: number }

Example for Simple Product List (array at root):
{
  "systemPrompt": "Call search_products and return the list of products.",
  "outputFormat": "json",
  "outputSchema": {
    "type": "array",
    "items": {
      "type": "object",
      "properties": {
        "id": {"type": "string"},
        "name": {"type": "string"},
        "price": {"type": "number"}
      },
      "required": ["id", "name", "price"]
    }
  }
}

RULES for structured output:
1. Use "additionalProperties": false to prevent extra fields
2. CRITICAL: In "required" arrays, you MUST list ALL properties defined in "properties" - OpenAI's strict mode rejects partial required arrays
3. Use descriptive property names (camelCase)
4. Include metadata (fetchedAt, analyzedAt, etc.)
5. Schema MUST be strict - no optional variations
6. Every nested object needs its own "required" array listing ALL its properties
7. ARRAYS AT ROOT LEVEL: You can use arrays directly without wrapping in an object:
   - For simple lists: { "type": "array", "items": { "type": "object", "properties": {...}, "required": [...] } }
   - For data + metadata: { "type": "object", "properties": { "items": {...}, "total": {...} }, "required": [...] }
   - Use arrays when returning just a list, use objects when you need metadata too

=== CREDENTIAL HANDLING ===
For integration tools (Discord, Slack, webhooks):
- Credentials are AUTO-INJECTED at runtime
- DO NOT include webhook URLs in prompts
- DO NOT tell the agent to ask for credentials
- System prompts should command: "Send to Discord NOW" (not "provide your webhook URL")

=== TIME-SENSITIVE QUERIES (CRITICAL!) ===
When the user's request involves time-sensitive information, the search block MUST also call get_current_time:

TIME-SENSITIVE KEYWORDS (if any of these appear, add get_current_time):
- "today", "daily", "recent", "latest", "current", "now", "this week", "this month"
- "news", "events", "updates", "trending", "breaking"
- "stock", "price", "weather", "score", "live"

For time-sensitive search blocks, use BOTH tools:
{
  "enabledTools": ["get_current_time", "search_web"],
  "requiredTools": ["get_current_time", "search_web"],
  "systemPrompt": "FIRST call get_current_time to get today's date. THEN search for [topic] using that date. Include the date in your search query for accurate results."
}

EXAMPLE - User asks "Get me today's AI news":
{
  "systemPrompt": "FIRST call get_current_time to get today's date and time. THEN call search_web with the topic AND the current date (e.g., 'AI news December 2024'). Return top 3 results with titles, sources, and the date they were published.",
  "enabledTools": ["get_current_time", "search_web"],
  "requiredTools": ["get_current_time", "search_web"]
}

=== TOOL ASSIGNMENT RULES ===
Each block = ONE specific task = ONE set of related tools. Never mix unrelated tools!

TOOL SELECTION BY BLOCK PURPOSE:
- Research/Search block (time-sensitive): enabledTools=["get_current_time", "search_web"], requiredTools=["get_current_time", "search_web"]
- Research/Search block (general): enabledTools=["search_web"], requiredTools=["search_web"]
- Data Analysis block: enabledTools=["analyze_data"], requiredTools=["analyze_data"]
- Spreadsheet Reading block: enabledTools=["read_spreadsheet"], requiredTools=["read_spreadsheet"]
- Audio Transcription block: enabledTools=["transcribe_audio"], requiredTools=["transcribe_audio"]
- Image Analysis block: enabledTools=["describe_image"], requiredTools=["describe_image"]
- Document Reading block: enabledTools=["read_document"], requiredTools=["read_document"]
- Discord Publisher: enabledTools=["send_discord_message"], requiredTools=["send_discord_message"]
- Slack Publisher: enabledTools=["send_slack_message"], requiredTools=["send_slack_message"]
- Telegram Publisher: enabledTools=["send_telegram_message"], requiredTools=["send_telegram_message"]
- Google Chat Publisher: enabledTools=["send_google_chat_message"], requiredTools=["send_google_chat_message"]
- Content Writer: enabledTools=[] (no tools - generates text only, requireToolUsage=false)

=== DATA FLOW & VARIABLE PATHS (CRITICAL!) ===

UNDERSTANDING TRIGGER BLOCK OUTPUTS:

Webhook Trigger Output Structure:
{
  "body": { ...request body data... },
  "query": { ...query parameters... },
  "headers": { ...request headers... },
  "method": "POST",
  "path": "/endpoint"
}

Schedule Trigger Output Structure:
{
  "triggeredAt": "2024-01-15T09:00:00Z",
  "schedule": "0 9 * * *"
}

VARIABLE PATH RULES:
1. Webhook data: {{webhook.body}}, {{webhook.query}}, {{webhook.body.email}}
2. Schedule data: {{schedule.triggeredAt}}
3. Previous block outputs: {{block-id.response}}
4. Nested data: {{block-id.response.articles[0].title}}

CORRECT PATHS:
- {{webhook.body}} - Entire request body from webhook trigger
- {{webhook.body.email}} - Nested field from webhook body
- {{webhook.query.topic}} - Query parameter from webhook
- {{schedule.triggeredAt}} - Timestamp from schedule trigger
- {{news-researcher.response}} - Previous block's full response
- {{block-id.response.articles}} - Nested data from previous block

WRONG PATHS (NEVER use these):
- {{input}} - NO! Use {{webhook.body}} or trigger-specific path
- {{start.email}} - NO! Triggers don't have arbitrary fields
- {{webhook}} - NO! Be specific: {{webhook.body}} or {{webhook.query}}

FOR CODE_BLOCKS with nested data:
When accessing nested fields from previous blocks in argumentMapping:
{
  "toolName": "mongodb_write",
  "argumentMapping": {
    "action": "insertOne",
    "collection": "users",
    "document": {
      "email": "{{input.email}}",      ← Access nested field
      "name": "{{input.name}}",        ← Access nested field
      "phone": "{{input.phone}}",      ← Access nested field
      "created_at": "{{get-current-time.response}}"
    }
  }
}

=== BLOCK ID NAMING ===
Block "id" MUST be kebab-case of "name":
- "News Researcher" → id: "news-researcher"
- "Discord Publisher" → id: "discord-publisher"

=== LAYOUT ===
- Trigger block: position { "x": 250, "y": 50 }
- Space blocks 150px vertically
- timeout: 30 for triggers, 120 for LLM blocks

=== EXAMPLE 1: News API + Discord (Webhook Trigger) ===
User: "Create an agent that searches for news and posts to Discord"

{
  "blocks": [
    {
      "id": "webhook",
      "type": "webhook_trigger",
      "name": "Webhook",
      "description": "API endpoint to trigger news search",
      "config": { "method": "POST" },
      "position": { "x": 250, "y": 50 },
      "timeout": 30
    },
    {
      "id": "news-researcher",
      "type": "llm_inference",
      "name": "News Researcher",
      "description": "Search and summarize latest news",
      "config": {
        "systemPrompt": "FIRST call get_current_time to get today's date. THEN call search_web for news about the topic from the request, including the current date in your query (e.g., 'AI news December 2024'). Return results EXACTLY in the output schema format with top 3 articles.",
        "userPrompt": "Search for: {{webhook.body.topic}}",
        "temperature": 0.3,
        "enabledTools": ["get_current_time", "search_web"],
        "requiredTools": ["get_current_time", "search_web"],
        "requireToolUsage": true,
        "outputFormat": "json",
        "outputSchema": {
          "type": "object",
          "properties": {
            "articles": {
              "type": "array",
              "items": {
                "type": "object",
                "properties": {
                  "title": {"type": "string"},
                  "source": {"type": "string"},
                  "url": {"type": "string"},
                  "summary": {"type": "string"},
                  "publishedDate": {"type": "string"}
                },
                "required": ["title", "source", "url", "summary", "publishedDate"]
              }
            },
            "totalResults": {"type": "number"},
            "fetchedAt": {"type": "string"}
          },
          "required": ["articles", "totalResults", "fetchedAt"],
          "additionalProperties": false
        }
      },
      "position": { "x": 250, "y": 200 },
      "timeout": 120
    },
    {
      "id": "discord-publisher",
      "type": "llm_inference",
      "name": "Discord Publisher",
      "description": "Format and send news to Discord",
      "config": {
        "systemPrompt": "Send this news summary to Discord NOW. Call send_discord_message with: content containing a brief intro, embed_title set to 'Latest News Update', embed_description with the full summary. Execute immediately.",
        "userPrompt": "{{news-researcher.response}}",
        "temperature": 0.3,
        "enabledTools": ["send_discord_message"],
        "requiredTools": ["send_discord_message"],
        "requireToolUsage": true
      },
      "position": { "x": 250, "y": 350 },
      "timeout": 120
    }
  ],
  "connections": [
    { "id": "conn-1", "sourceBlockId": "webhook", "sourceOutput": "output", "targetBlockId": "news-researcher", "targetInput": "from_webhook" },
    { "id": "conn-2", "sourceBlockId": "news-researcher", "sourceOutput": "output", "targetBlockId": "discord-publisher", "targetInput": "from_news" }
  ],
  "variables": [],
  "explanation": "3 blocks: Webhook (POST /endpoint)→News Researcher (MUST call get_current_time THEN search_web)→Discord Publisher (MUST call send_discord_message). Trigger with: POST {\"topic\": \"AI news\"}"
}

=== EXAMPLE 2: Daily News Automation (Schedule Trigger) ===
User: "Send me daily AI news every morning at 9 AM"

{
  "blocks": [
    {
      "id": "schedule",
      "type": "schedule_trigger",
      "name": "Schedule",
      "description": "Runs daily at 9 AM",
      "config": { "schedule": "0 9 * * *" },
      "position": { "x": 250, "y": 50 },
      "timeout": 30
    },
    {
      "id": "news-fetcher",
      "type": "llm_inference",
      "name": "News Fetcher",
      "description": "Fetch latest AI news",
      "config": {
        "systemPrompt": "FIRST call get_current_time. THEN search for latest AI news using search_web. Return top 5 articles in the schema format.",
        "userPrompt": "Get latest AI news",
        "temperature": 0.3,
        "enabledTools": ["get_current_time", "search_web"],
        "requiredTools": ["get_current_time", "search_web"],
        "requireToolUsage": true,
        "outputFormat": "json",
        "outputSchema": {
          "type": "object",
          "properties": {
            "articles": {
              "type": "array",
              "items": {
                "type": "object",
                "properties": {
                  "title": {"type": "string"},
                  "source": {"type": "string"},
                  "url": {"type": "string"}
                },
                "required": ["title", "source", "url"]
              }
            },
            "fetchedAt": {"type": "string"}
          },
          "required": ["articles", "fetchedAt"],
          "additionalProperties": false
        }
      },
      "position": { "x": 250, "y": 200 },
      "timeout": 120
    },
    {
      "id": "discord-sender",
      "type": "llm_inference",
      "name": "Discord Sender",
      "description": "Send news digest to Discord",
      "config": {
        "systemPrompt": "Send this news digest to Discord NOW. Call send_discord_message with embed_title='Daily AI News Digest'. Execute immediately.",
        "userPrompt": "{{news-fetcher.response}}",
        "temperature": 0.3,
        "enabledTools": ["send_discord_message"],
        "requiredTools": ["send_discord_message"],
        "requireToolUsage": true
      },
      "position": { "x": 250, "y": 350 },
      "timeout": 120
    }
  ],
  "connections": [
    { "id": "conn-1", "sourceBlockId": "schedule", "sourceOutput": "output", "targetBlockId": "news-fetcher", "targetInput": "from_schedule" },
    { "id": "conn-2", "sourceBlockId": "news-fetcher", "sourceOutput": "output", "targetBlockId": "discord-sender", "targetInput": "from_news" }
  ],
  "variables": [],
  "explanation": "3 blocks: Schedule (daily 9AM)→News Fetcher (MUST call get_current_time THEN search_web)→Discord Sender (MUST call send_discord_message). Runs automatically every day."
}

=== EXAMPLE 3: Simple API with Direct Tool (Webhook + Code Block) ===
User: "Create an API to get the current time"

{
  "blocks": [
    {
      "id": "webhook",
      "type": "webhook_trigger",
      "name": "Webhook",
      "description": "API endpoint",
      "config": { "method": "GET" },
      "position": { "x": 250, "y": 50 },
      "timeout": 30
    },
    {
      "id": "get-time",
      "type": "code_block",
      "name": "Get Time",
      "description": "Get current time directly",
      "config": {
        "toolName": "get_current_time",
        "argumentMapping": {}
      },
      "position": { "x": 250, "y": 200 },
      "timeout": 30
    }
  ],
  "connections": [
    { "id": "conn-1", "sourceBlockId": "webhook", "sourceOutput": "output", "targetBlockId": "get-time", "targetInput": "from_webhook" }
  ],
  "variables": [],
  "explanation": "2 blocks: Webhook (GET)→Get Time (code_block for fast direct execution). No LLM needed for mechanical task."
}

=== EXAMPLE 4: MongoDB Insert with Nested Field Access ===
User: "Create API to insert user data into MongoDB"

This example shows correct path usage for accessing nested webhook body fields:

{
  "blocks": [
    {
      "id": "webhook",
      "type": "webhook_trigger",
      "name": "Webhook",
      "description": "Receive user data via POST",
      "config": { "method": "POST" },
      "position": { "x": 250, "y": 50 },
      "timeout": 30
    },
    {
      "id": "get-current-time",
      "type": "code_block",
      "name": "Get Current Time",
      "description": "Get timestamp for record",
      "config": {
        "toolName": "get_current_time",
        "argumentMapping": {}
      },
      "position": { "x": 250, "y": 200 },
      "timeout": 30
    },
    {
      "id": "insert-user",
      "type": "code_block",
      "name": "Insert User",
      "description": "Insert user into MongoDB with nested field access",
      "config": {
        "toolName": "mongodb_write",
        "argumentMapping": {
          "action": "insertOne",
          "collection": "users",
          "document": {
            "email": "{{webhook.body.email}}",
            "name": "{{webhook.body.name}}",
            "phone": "{{webhook.body.phone}}",
            "created_at": "{{get-current-time.response}}",
            "status": "active"
          }
        }
      },
      "position": { "x": 250, "y": 350 },
      "timeout": 30
    }
  ],
  "connections": [
    { "id": "conn-1", "sourceBlockId": "webhook", "sourceOutput": "output", "targetBlockId": "get-current-time", "targetInput": "from_webhook" },
    { "id": "conn-2", "sourceBlockId": "get-current-time", "sourceOutput": "output", "targetBlockId": "insert-user", "targetInput": "from_time" }
  ],
  "variables": [],
  "explanation": "Inserts user data into MongoDB using correct nested field paths: {{webhook.body.email}}, {{webhook.body.name}}, {{webhook.body.phone}}. POST with JSON: {\"email\":\"test@example.com\",\"name\":\"John\",\"phone\":\"555-1234\"}"
}

KEY POINTS IN THIS EXAMPLE:
- Webhook body fields accessed as: {{webhook.body.email}}, {{webhook.body.name}}, etc.
- Code_block argumentMapping can have nested objects
- Deep interpolation resolves {{...}} at any nesting level
- Mixed literal values ("active") and variable references work together

=== EXAMPLE 5: Mixed LLM + code_block (EFFICIENT HYBRID) ===
User: "Search for AI news and send to Discord"

This example shows how to mix llm_inference (for research) with code_block (for sending).
The llm_inference block does the intelligent work (search + format), then code_block sends directly.

{
  "blocks": [
    {
      "id": "webhook",
      "type": "webhook_trigger",
      "name": "Webhook",
      "description": "Trigger news search",
      "config": { "method": "POST" },
      "position": { "x": 250, "y": 50 },
      "timeout": 30
    },
    {
      "id": "news-researcher",
      "type": "llm_inference",
      "name": "News Researcher",
      "description": "Search news and format for Discord",
      "config": {
        "systemPrompt": "FIRST call get_current_time. THEN call search_web for news about the topic from request. Format results as a Discord message with embed fields. Return EXACTLY in the output schema format.",
        "userPrompt": "Search for: {{webhook.body.topic}}",
        "temperature": 0.3,
        "enabledTools": ["get_current_time", "search_web"],
        "requiredTools": ["get_current_time", "search_web"],
        "requireToolUsage": true,
        "outputFormat": "json",
        "outputSchema": {
          "type": "object",
          "properties": {
            "content": {"type": "string", "description": "Brief intro message"},
            "embed_title": {"type": "string", "description": "Discord embed title"},
            "embed_description": {"type": "string", "description": "Full news summary with links"}
          },
          "required": ["content", "embed_title", "embed_description"],
          "additionalProperties": false
        }
      },
      "position": { "x": 250, "y": 200 },
      "timeout": 120
    },
    {
      "id": "discord-sender",
      "type": "code_block",
      "name": "Discord Sender",
      "description": "Send pre-formatted message to Discord (no LLM needed)",
      "config": {
        "toolName": "send_discord_message",
        "argumentMapping": {
          "content": "{{news-researcher.response.content}}",
          "embed_title": "{{news-researcher.response.embed_title}}",
          "embed_description": "{{news-researcher.response.embed_description}}"
        }
      },
      "position": { "x": 250, "y": 350 },
      "timeout": 30
    }
  ],
  "connections": [
    { "id": "conn-1", "sourceBlockId": "webhook", "sourceOutput": "output", "targetBlockId": "news-researcher", "targetInput": "from_webhook" },
    { "id": "conn-2", "sourceBlockId": "news-researcher", "sourceOutput": "output", "targetBlockId": "discord-sender", "targetInput": "from_researcher" }
  ],
  "variables": [],
  "explanation": "3 blocks: Webhook→News Researcher (llm_inference: search + format)→Discord Sender (code_block: direct send, no LLM overhead). POST with {\"topic\":\"AI news\"}"
}

WHY use code_block for Discord Sender?
- The message is ALREADY formatted by the researcher
- No decisions needed - just send the exact content
- FASTER execution (no LLM API call)
- CHEAPER (no token costs)
- MORE RELIABLE (no LLM interpretation)

REMEMBER:
- Temperature = 0.3 for all LLM blocks (deterministic)
- requiredTools = same as enabledTools (forces tool usage)
- requireToolUsage = true (validates tool was called)
- System prompts use IMPERATIVE style (commands, not suggestions)
- Use code_block for mechanical tasks with NO reasoning needed (faster, cheaper)
- Use llm_inference when decisions, formatting, or interpretation is required
- code_block timeout = 30 (no LLM), llm_inference timeout = 120
- Output ONLY valid JSON. No text before or after.`

// GenerateWorkflow generates a workflow based on user input
func (s *WorkflowGeneratorService) GenerateWorkflow(req *models.WorkflowGenerateRequest, userID string) (*models.WorkflowGenerateResponse, error) {
	log.Printf("🔧 [WORKFLOW-GEN] Generating workflow for agent %s, user %s", req.AgentID, userID)

	// Get provider and model
	provider, modelID, err := s.getProviderAndModel(req.ModelID)
	if err != nil {
		return &models.WorkflowGenerateResponse{
			Success: false,
			Error:   fmt.Sprintf("Failed to get provider: %v", err),
		}, nil
	}

	// Build the user message
	userMessage := s.buildUserMessage(req)

	// Build dynamic system prompt with relevant tools based on user request
	systemPrompt := buildDynamicSystemPrompt(req.UserMessage)

	// Build messages array with conversation history for better context
	messages := []map[string]interface{}{
		{
			"role":    "system",
			"content": systemPrompt,
		},
	}

	// Add conversation history if provided (for multi-turn context)
	if len(req.ConversationHistory) > 0 {
		for _, msg := range req.ConversationHistory {
			messages = append(messages, map[string]interface{}{
				"role":    msg.Role,
				"content": msg.Content,
			})
		}
	}

	// Add current user message
	messages = append(messages, map[string]interface{}{
		"role":    "user",
		"content": userMessage,
	})

	// Build request body with structured output
	requestBody := map[string]interface{}{
		"model":       modelID,
		"messages":    messages,
		"stream":      false,
		"temperature": 0.3, // Lower temperature for more consistent JSON output
	}

	// Add response_format for structured output (OpenAI-compatible)
	requestBody["response_format"] = map[string]interface{}{
		"type": "json_object",
	}

	reqBody, err := json.Marshal(requestBody)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal request: %w", err)
	}

	log.Printf("📤 [WORKFLOW-GEN] Sending request to %s with model %s", provider.BaseURL, modelID)

	// Create HTTP request
	httpReq, err := http.NewRequest("POST", provider.BaseURL+"/chat/completions", bytes.NewBuffer(reqBody))
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}

	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("Authorization", "Bearer "+provider.APIKey)

	// Send request with timeout
	client := &http.Client{Timeout: 120 * time.Second}
	resp, err := client.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("request failed: %w", err)
	}
	defer resp.Body.Close()

	// Read response
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("failed to read response: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		log.Printf("⚠️ [WORKFLOW-GEN] API error: %s", string(body))
		return &models.WorkflowGenerateResponse{
			Success: false,
			Error:   fmt.Sprintf("API error (status %d): %s", resp.StatusCode, string(body)),
		}, nil
	}

	// Parse API response
	var apiResponse struct {
		Choices []struct {
			Message struct {
				Content string `json:"content"`
			} `json:"message"`
		} `json:"choices"`
	}

	if err := json.Unmarshal(body, &apiResponse); err != nil {
		return nil, fmt.Errorf("failed to parse API response: %w", err)
	}

	if len(apiResponse.Choices) == 0 {
		return &models.WorkflowGenerateResponse{
			Success: false,
			Error:   "No response from model",
		}, nil
	}

	content := apiResponse.Choices[0].Message.Content
	log.Printf("📥 [WORKFLOW-GEN] Received response (%d chars)", len(content))

	// Parse the workflow JSON from the response
	return s.parseWorkflowResponse(content, req.CurrentWorkflow != nil, req.AgentID)
}

// buildUserMessage constructs the user message for workflow generation
func (s *WorkflowGeneratorService) buildUserMessage(req *models.WorkflowGenerateRequest) string {
	if req.CurrentWorkflow != nil && len(req.CurrentWorkflow.Blocks) > 0 {
		// Modification request - include current workflow
		workflowJSON, _ := json.MarshalIndent(req.CurrentWorkflow, "", "  ")
		return fmt.Sprintf(`MODIFICATION REQUEST

Current workflow:
%s

User request: %s

Output the complete modified workflow JSON with all blocks (not just changes).`, string(workflowJSON), req.UserMessage)
	}

	// New workflow request
	return fmt.Sprintf("CREATE NEW WORKFLOW\n\nUser request: %s", req.UserMessage)
}

// parseWorkflowResponse parses the LLM response into a workflow
func (s *WorkflowGeneratorService) parseWorkflowResponse(content string, isModification bool, agentID string) (*models.WorkflowGenerateResponse, error) {
	// Try to extract JSON from the response (handle markdown code blocks)
	jsonContent := s.extractJSON(content)

	// Parse the workflow
	var workflowData struct {
		Blocks      []models.Block      `json:"blocks"`
		Connections []models.Connection `json:"connections"`
		Variables   []models.Variable   `json:"variables"`
		Explanation string              `json:"explanation"`
	}

	if err := json.Unmarshal([]byte(jsonContent), &workflowData); err != nil {
		log.Printf("⚠️ [WORKFLOW-GEN] Failed to parse workflow JSON: %v", err)
		log.Printf("⚠️ [WORKFLOW-GEN] Content: %s", jsonContent[:min(500, len(jsonContent))])
		return &models.WorkflowGenerateResponse{
			Success:     false,
			Error:       fmt.Sprintf("Failed to parse workflow JSON: %v", err),
			Explanation: content, // Return raw content as explanation
		}, nil
	}

	// Log the generated workflow for debugging
	prettyWorkflow, _ := json.MarshalIndent(workflowData, "", "  ")
	log.Printf("📋 [WORKFLOW-GEN] Generated workflow:\n%s", string(prettyWorkflow))

	// Post-process blocks: set normalizedId to match id
	for i := range workflowData.Blocks {
		if workflowData.Blocks[i].NormalizedID == "" {
			workflowData.Blocks[i].NormalizedID = workflowData.Blocks[i].ID
		}
	}

	// Validate the workflow
	errors := s.validateWorkflow(&workflowData)
	if len(errors) > 0 {
		log.Printf("⚠️ [WORKFLOW-GEN] Workflow validation errors: %v", errors)
	}

	// Determine action
	action := "create"
	if isModification {
		action = "modify"
	}

	// Build the workflow with generated IDs
	workflow := &models.Workflow{
		ID:          uuid.New().String(),
		AgentID:     agentID,
		Blocks:      workflowData.Blocks,
		Connections: workflowData.Connections,
		Variables:   workflowData.Variables,
		Version:     1,
	}

	log.Printf("✅ [WORKFLOW-GEN] Successfully parsed workflow: %d blocks, %d connections",
		len(workflow.Blocks), len(workflow.Connections))

	return &models.WorkflowGenerateResponse{
		Success:     true,
		Workflow:    workflow,
		Explanation: workflowData.Explanation,
		Action:      action,
		Version:     1,
		Errors:      errors,
	}, nil
}

// extractJSON extracts JSON from a response that might be wrapped in markdown
func (s *WorkflowGeneratorService) extractJSON(content string) string {
	content = strings.TrimSpace(content)

	// If it starts with {, assume it's pure JSON
	if strings.HasPrefix(content, "{") {
		return content
	}

	// Try to extract from markdown code block
	re := regexp.MustCompile("(?s)```(?:json)?\\s*\\n?(\\{.*\\})\\s*\\n?```")
	matches := re.FindStringSubmatch(content)
	if len(matches) > 1 {
		return matches[1]
	}

	// Try to find JSON object anywhere in the content
	re = regexp.MustCompile(`(?s)\{.*"blocks".*\}`)
	match := re.FindString(content)
	if match != "" {
		return match
	}

	return content
}

// validateWorkflow validates the workflow structure
func (s *WorkflowGeneratorService) validateWorkflow(workflow *struct {
	Blocks      []models.Block      `json:"blocks"`
	Connections []models.Connection `json:"connections"`
	Variables   []models.Variable   `json:"variables"`
	Explanation string              `json:"explanation"`
}) []models.ValidationError {
	var errors []models.ValidationError

	// Check for empty blocks
	if len(workflow.Blocks) == 0 {
		errors = append(errors, models.ValidationError{
			Type:    "schema",
			Message: "Workflow must have at least one block",
		})
		return errors
	}

	// Build block ID set for connection validation
	blockIDs := make(map[string]bool)
	for _, block := range workflow.Blocks {
		blockIDs[block.ID] = true

		// Validate block type
		if block.Type != "llm_inference" && block.Type != "variable" {
			errors = append(errors, models.ValidationError{
				Type:    "schema",
				Message: fmt.Sprintf("Invalid block type: %s", block.Type),
				BlockID: block.ID,
			})
		}
	}

	// Validate connections reference valid blocks
	for _, conn := range workflow.Connections {
		if !blockIDs[conn.SourceBlockID] {
			errors = append(errors, models.ValidationError{
				Type:         "missing_input",
				Message:      fmt.Sprintf("Connection references non-existent source block: %s", conn.SourceBlockID),
				ConnectionID: conn.ID,
			})
		}
		if !blockIDs[conn.TargetBlockID] {
			errors = append(errors, models.ValidationError{
				Type:         "missing_input",
				Message:      fmt.Sprintf("Connection references non-existent target block: %s", conn.TargetBlockID),
				ConnectionID: conn.ID,
			})
		}
	}

	return errors
}

// getProviderAndModel gets the provider and model for the request
func (s *WorkflowGeneratorService) getProviderAndModel(modelID string) (*models.Provider, string, error) {
	// If no model specified, use default
	if modelID == "" {
		provider, model, err := s.chatService.GetDefaultProviderWithModel()
		if err != nil {
			return nil, "", err
		}
		return provider, model, nil
	}

	// Try to find the model in the database
	var providerID int
	var modelName string

	err := s.db.QueryRow(`
		SELECT m.name, m.provider_id
		FROM models m
		WHERE m.id = ? AND m.is_visible = 1
	`, modelID).Scan(&modelName, &providerID)

	if err != nil {
		// Try as model alias
		if provider, actualModel, found := s.chatService.ResolveModelAlias(modelID); found {
			return provider, actualModel, nil
		}
		// Fall back to default
		return s.chatService.GetDefaultProviderWithModel()
	}

	// Get the provider
	provider, err := s.providerService.GetByID(providerID)
	if err != nil {
		return nil, "", fmt.Errorf("failed to get provider: %w", err)
	}

	return provider, modelName, nil
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}

// AgentMetadata holds generated name and description for an agent
type AgentMetadata struct {
	Name        string `json:"name"`
	Description string `json:"description"`
}

// GenerateAgentMetadata generates a name and description for an agent based on the user's request
func (s *WorkflowGeneratorService) GenerateAgentMetadata(userMessage string) (*AgentMetadata, error) {
	// Use ChatService's GetTextProviderWithModel which dynamically finds a text-capable provider
	// This method checks model aliases from config and falls back to database providers
	provider, modelID, err := s.chatService.GetTextProviderWithModel()
	if err != nil {
		return nil, fmt.Errorf("failed to get text provider for metadata generation: %w", err)
	}

	log.Printf("🔍 [METADATA-GEN] Using dynamic model: %s (provider: %s)", modelID, provider.Name)

	// Build a prompt that generates both name and description in a simple format
	messages := []map[string]interface{}{
		{
			"role": "system",
			"content": `Generate a catchy name and brief description for an AI agent.

RULES for name:
- 2-4 words maximum
- Action-oriented and memorable (e.g., "News Pulse", "Data Wizard", "Chart Crafter", "Report Runner")
- Use descriptive verbs or nouns that indicate the agent's purpose
- NEVER use generic words like "Agent", "Bot", "AI", "Assistant", "Helper", "Tool"
- Make it sound professional but approachable
- Be creative and specific to the task

RULES for description:
- One sentence, maximum 100 characters
- Start with a verb (e.g., "Searches...", "Analyzes...", "Monitors...")
- Be specific about what the agent does
- Mention the key output or destination if relevant

RESPOND with EXACTLY this format (two lines only):
NAME: [Your agent name here]
DESC: [Your one-line description here]

Example for "search for AI news and post to Discord":
NAME: News Pulse
DESC: Searches for latest tech news and posts summaries to Discord

Example for "analyze CSV data and create charts":
NAME: Chart Crafter
DESC: Analyzes data files and generates visual charts`,
		},
		{
			"role":    "user",
			"content": fmt.Sprintf("Agent purpose: %s", userMessage),
		},
	}

	// Simple request like chat title generation - no structured output
	requestBody := map[string]interface{}{
		"model":       modelID,
		"messages":    messages,
		"stream":      false,
		"temperature": 0.7,
	}

	reqBody, err := json.Marshal(requestBody)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal request: %w", err)
	}

	// Create HTTP request - use base URL with /chat/completions
	apiURL := provider.BaseURL + "/chat/completions"
	log.Printf("🔍 [METADATA-GEN] Sending request to: %s with model: %s", apiURL, modelID)

	httpReq, err := http.NewRequest("POST", apiURL, bytes.NewBuffer(reqBody))
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}

	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("Authorization", "Bearer "+provider.APIKey)

	// Send request with timeout
	client := &http.Client{Timeout: 30 * time.Second}
	resp, err := client.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("request failed: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("failed to read response: %w", err)
	}

	log.Printf("🔍 [METADATA-GEN] Response status: %d, body length: %d", resp.StatusCode, len(body))

	if resp.StatusCode != http.StatusOK {
		log.Printf("❌ [METADATA-GEN] API error response: %s", string(body))
		return nil, fmt.Errorf("API error (status %d): %s", resp.StatusCode, string(body))
	}

	// Parse API response
	var apiResponse struct {
		Choices []struct {
			Message struct {
				Content string `json:"content"`
			} `json:"message"`
		} `json:"choices"`
	}

	if err := json.Unmarshal(body, &apiResponse); err != nil {
		return nil, fmt.Errorf("failed to parse API response: %w", err)
	}

	if len(apiResponse.Choices) == 0 {
		return nil, fmt.Errorf("no response from model")
	}

	// Parse the NAME: and DESC: format from response
	content := strings.TrimSpace(apiResponse.Choices[0].Message.Content)
	log.Printf("🔍 [METADATA-GEN] Raw response: %s", content)

	var name, description string

	// Parse line by line looking for NAME: and DESC:
	lines := strings.Split(content, "\n")
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(strings.ToUpper(line), "NAME:") {
			name = strings.TrimSpace(strings.TrimPrefix(line, "NAME:"))
			name = strings.TrimSpace(strings.TrimPrefix(line, "name:"))
			// Remove the prefix more reliably
			if idx := strings.Index(strings.ToLower(line), "name:"); idx != -1 {
				name = strings.TrimSpace(line[idx+5:])
			}
		} else if strings.HasPrefix(strings.ToUpper(line), "DESC:") {
			description = strings.TrimSpace(strings.TrimPrefix(line, "DESC:"))
			description = strings.TrimSpace(strings.TrimPrefix(line, "desc:"))
			// Remove the prefix more reliably
			if idx := strings.Index(strings.ToLower(line), "desc:"); idx != -1 {
				description = strings.TrimSpace(line[idx+5:])
			}
		}
	}

	// Fallback: if parsing failed, try to use first line as name
	if name == "" && len(lines) > 0 {
		name = strings.TrimSpace(lines[0])
		name = strings.Trim(name, `"'#*-`)
	}

	// Clean up name
	name = strings.Trim(name, `"'#*-`)

	// Limit name to 5 words
	words := strings.Fields(name)
	if len(words) > 5 {
		words = words[:5]
		name = strings.Join(words, " ")
	}

	if name == "" {
		return nil, fmt.Errorf("empty name from model")
	}

	metadata := AgentMetadata{
		Name:        name,
		Description: description,
	}

	// Truncate if too long
	if len(metadata.Name) > 50 {
		metadata.Name = metadata.Name[:50]
	}
	if len(metadata.Description) > 150 {
		metadata.Description = metadata.Description[:150]
	}

	log.Printf("📝 [WORKFLOW-GEN] Generated agent metadata: name=%s, description=%s", metadata.Name, metadata.Description)
	return &metadata, nil
}

// GenerateAgentName generates a short, descriptive name for an agent (backwards compatibility)
func (s *WorkflowGeneratorService) GenerateAgentName(userMessage string) (string, error) {
	metadata, err := s.GenerateAgentMetadata(userMessage)
	if err != nil {
		return "", err
	}
	return metadata.Name, nil
}

// GenerateDescriptionFromWorkflow generates a description for an agent based on its workflow blocks
func (s *WorkflowGeneratorService) GenerateDescriptionFromWorkflow(workflow *models.Workflow, agentName string) (string, error) {
	if workflow == nil || len(workflow.Blocks) == 0 {
		return "", fmt.Errorf("no workflow blocks to analyze")
	}

	// Use ChatService's GetTextProviderWithModel which dynamically finds a text-capable provider
	// This method checks model aliases from config and falls back to database providers
	provider, modelID, err := s.chatService.GetTextProviderWithModel()
	if err != nil {
		return "", fmt.Errorf("failed to get text provider for description generation: %w", err)
	}

	log.Printf("🔍 [DESC-GEN] Using dynamic model: %s (provider: %s)", modelID, provider.Name)

	// Build a summary of the workflow blocks for the LLM
	var blockSummary strings.Builder
	blockSummary.WriteString("Workflow blocks:\n")
	for _, block := range workflow.Blocks {
		if block.Type == "llm_inference" {
			// Extract key info from LLM blocks
			tools := ""
			if enabledTools, ok := block.Config["enabledTools"].([]interface{}); ok {
				toolNames := make([]string, 0)
				for _, t := range enabledTools {
					if toolName, ok := t.(string); ok {
						toolNames = append(toolNames, toolName)
					}
				}
				tools = strings.Join(toolNames, ", ")
			}
			if tools != "" {
				blockSummary.WriteString(fmt.Sprintf("- %s: %s (tools: %s)\n", block.Name, block.Description, tools))
			} else {
				blockSummary.WriteString(fmt.Sprintf("- %s: %s\n", block.Name, block.Description))
			}
		} else if block.Type == "variable" {
			blockSummary.WriteString(fmt.Sprintf("- %s (input): %s\n", block.Name, block.Description))
		}
	}

	messages := []map[string]interface{}{
		{
			"role": "system",
			"content": `Generate a brief, one-sentence description for an AI agent based on its workflow.

RULES:
- Maximum 100 characters
- Start with a verb (e.g., "Searches...", "Analyzes...", "Monitors...")
- Be specific about what the agent does
- Mention the key actions or outputs
- Do not include the agent name in the description
- Do not use quotes around the description

RESPOND with ONLY the description text, nothing else.`,
		},
		{
			"role":    "user",
			"content": fmt.Sprintf("Agent name: %s\n\n%s", agentName, blockSummary.String()),
		},
	}

	requestBody := map[string]interface{}{
		"model":       modelID,
		"messages":    messages,
		"stream":      false,
		"temperature": 0.5,
	}

	reqBody, err := json.Marshal(requestBody)
	if err != nil {
		return "", fmt.Errorf("failed to marshal request: %w", err)
	}

	apiURL := provider.BaseURL + "/chat/completions"
	log.Printf("🔍 [DESC-GEN] Generating description for agent: %s", agentName)

	httpReq, err := http.NewRequest("POST", apiURL, bytes.NewBuffer(reqBody))
	if err != nil {
		return "", fmt.Errorf("failed to create request: %w", err)
	}

	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("Authorization", "Bearer "+provider.APIKey)

	client := &http.Client{Timeout: 30 * time.Second}
	resp, err := client.Do(httpReq)
	if err != nil {
		return "", fmt.Errorf("request failed: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", fmt.Errorf("failed to read response: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("API error (status %d): %s", resp.StatusCode, string(body))
	}

	var apiResponse struct {
		Choices []struct {
			Message struct {
				Content string `json:"content"`
			} `json:"message"`
		} `json:"choices"`
	}

	if err := json.Unmarshal(body, &apiResponse); err != nil {
		return "", fmt.Errorf("failed to parse API response: %w", err)
	}

	if len(apiResponse.Choices) == 0 {
		return "", fmt.Errorf("no response from model")
	}

	description := strings.TrimSpace(apiResponse.Choices[0].Message.Content)
	description = strings.Trim(description, `"'`)

	// Truncate if too long
	if len(description) > 150 {
		description = description[:150]
	}

	log.Printf("📝 [DESC-GEN] Generated description: %s", description)
	return description, nil
}

// GenerateSampleInput generates sample JSON input for a workflow based on its blocks
func (s *WorkflowGeneratorService) GenerateSampleInput(workflow *models.Workflow, modelID string, userID string) (map[string]interface{}, error) {
	if workflow == nil || len(workflow.Blocks) == 0 {
		return nil, fmt.Errorf("no workflow blocks to analyze")
	}

	// Get provider and model
	provider, resolvedModelID, err := s.getProviderAndModel(modelID)
	if err != nil {
		return nil, fmt.Errorf("failed to get provider: %w", err)
	}

	log.Printf("🎯 [SAMPLE-INPUT] Generating sample input using model: %s (provider: %s)", resolvedModelID, provider.Name)

	// Build a summary of the workflow to understand what input it needs
	var workflowSummary strings.Builder
	workflowSummary.WriteString("This workflow has the following blocks:\n\n")

	for i, block := range workflow.Blocks {
		workflowSummary.WriteString(fmt.Sprintf("Block %d: %s (type: %s)\n", i+1, block.Name, block.Type))

		if block.Type == "llm_inference" {
			// Extract system prompt and enabled tools
			if systemPrompt, ok := block.Config["systemPrompt"].(string); ok && systemPrompt != "" {
				// Truncate long prompts
				if len(systemPrompt) > 500 {
					systemPrompt = systemPrompt[:500] + "..."
				}
				workflowSummary.WriteString(fmt.Sprintf("  System prompt: %s\n", systemPrompt))
			}

			if enabledTools, ok := block.Config["enabledTools"].([]interface{}); ok && len(enabledTools) > 0 {
				toolNames := make([]string, 0, len(enabledTools))
				for _, t := range enabledTools {
					if ts, ok := t.(string); ok {
						toolNames = append(toolNames, ts)
					}
				}
				if len(toolNames) > 0 {
					workflowSummary.WriteString(fmt.Sprintf("  Tools: %s\n", strings.Join(toolNames, ", ")))
				}
			}
		} else if block.Type == "variable" {
			workflowSummary.WriteString("  This is the start block that receives input\n")
		}
		workflowSummary.WriteString("\n")
	}

	// Build messages for the LLM
	messages := []map[string]interface{}{
		{
			"role": "system",
			"content": `You are a helpful assistant that generates realistic sample JSON input for AI workflow testing.

Analyze the workflow description and generate appropriate sample JSON input that would be useful for testing this workflow.

RULES:
1. Output ONLY valid JSON - no text before or after
2. Use realistic, meaningful sample data that matches what the workflow expects
3. If the workflow processes text, include relevant sample text
4. If it handles URLs, include valid example URLs
5. If it handles names/contacts, use realistic placeholder names
6. If it handles numbers/data, use reasonable sample values
7. Keep the JSON concise but complete
8. Use "input" as the top-level key if no specific structure is evident
9. Consider the tools being used - e.g., if web scraping, include a URL; if data analysis, include data points

EXAMPLES:
- For a news search workflow: {"input": "latest developments in artificial intelligence"}
- For a data analysis workflow: {"data": [{"name": "Q1", "value": 1000}, {"name": "Q2", "value": 1500}]}
- For a web scraping workflow: {"url": "https://example.com/article", "extract": "main content"}
- For a contact workflow: {"name": "John Smith", "email": "john@example.com", "company": "Acme Corp"}`,
		},
		{
			"role":    "user",
			"content": fmt.Sprintf("Generate sample JSON input for this workflow:\n\n%s", workflowSummary.String()),
		},
	}

	// Build request body
	requestBody := map[string]interface{}{
		"model":       resolvedModelID,
		"messages":    messages,
		"stream":      false,
		"temperature": 0.7,
		"response_format": map[string]interface{}{
			"type": "json_object",
		},
	}

	reqBody, err := json.Marshal(requestBody)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal request: %w", err)
	}

	// Create HTTP request
	apiURL := provider.BaseURL + "/chat/completions"
	log.Printf("🔍 [SAMPLE-INPUT] Sending request to: %s", apiURL)

	httpReq, err := http.NewRequest("POST", apiURL, bytes.NewBuffer(reqBody))
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}

	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("Authorization", "Bearer "+provider.APIKey)

	// Send request with timeout
	client := &http.Client{Timeout: 60 * time.Second}
	resp, err := client.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("request failed: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("failed to read response: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		log.Printf("⚠️ [SAMPLE-INPUT] API error: %s", string(body))
		return nil, fmt.Errorf("API error (status %d): %s", resp.StatusCode, string(body))
	}

	// Parse API response
	var apiResponse struct {
		Choices []struct {
			Message struct {
				Content string `json:"content"`
			} `json:"message"`
		} `json:"choices"`
	}

	if err := json.Unmarshal(body, &apiResponse); err != nil {
		return nil, fmt.Errorf("failed to parse API response: %w", err)
	}

	if len(apiResponse.Choices) == 0 {
		return nil, fmt.Errorf("no response from model")
	}

	content := strings.TrimSpace(apiResponse.Choices[0].Message.Content)
	log.Printf("📥 [SAMPLE-INPUT] Received response: %s", content)

	// Parse the JSON response
	var sampleInput map[string]interface{}
	if err := json.Unmarshal([]byte(content), &sampleInput); err != nil {
		// Try to extract JSON from the response
		jsonContent := s.extractJSON(content)
		if err := json.Unmarshal([]byte(jsonContent), &sampleInput); err != nil {
			return nil, fmt.Errorf("failed to parse sample input JSON: %w", err)
		}
	}

	log.Printf("✅ [SAMPLE-INPUT] Generated sample input with %d keys", len(sampleInput))
	return sampleInput, nil
}
