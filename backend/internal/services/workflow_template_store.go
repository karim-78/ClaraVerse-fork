package services

// Workflow template store — pre-built workflow DAGs that solve common
// automation problems. New users land on an empty workflow page and have
// no idea what's possible; templates fix that.
//
// Operationally:
//   - Templates live in mongo collection `workflow_templates`. Built-in
//     ones get seeded on backend boot via SeedDefaults() (idempotent by
//     slug).
//   - Cloning instantiates a fresh Agent + Workflow under the requesting
//     user — the template is a recipe, not a singleton the user shares.
//   - A template's `required_credentials` field hints to the UI which
//     provider credentials the user must wire before the cloned workflow
//     can actually run. The handler can use this to show a setup
//     checklist post-clone.
//
// Design choices worth flagging:
//   - We store the full Blocks/Connections JSON in mongo, not in code.
//     This means an admin can edit a template via API without a redeploy.
//     SeedDefaults() only inserts when the slug doesn't exist, so admin
//     edits are preserved across restarts.
//   - Clone generates fresh block IDs to avoid collisions with the
//     template's IDs (which could otherwise collide if a user clones
//     the same template twice).
//   - Templates are global (no user_id) but cloned workflows are user-
//     scoped via the AgentService path.

import (
	"context"
	"fmt"
	"log"
	"time"

	"claraverse/internal/database"
	"claraverse/internal/models"

	"github.com/google/uuid"
	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/bson/primitive"
	"go.mongodb.org/mongo-driver/mongo"
	"go.mongodb.org/mongo-driver/mongo/options"
)

// WorkflowTemplate is a global, cloneable workflow definition.
type WorkflowTemplate struct {
	ID                  primitive.ObjectID  `bson:"_id,omitempty" json:"id"`
	Name                string              `bson:"name" json:"name"`
	Slug                string              `bson:"slug" json:"slug"`
	Description         string              `bson:"description" json:"description"`
	Category            string              `bson:"category" json:"category"`
	Icon                string              `bson:"icon" json:"icon"`
	Color               string              `bson:"color" json:"color"`
	Tags                []string            `bson:"tags" json:"tags"`
	// RequiredCredentials lists provider/integration names the cloned
	// workflow will need (e.g. ["slack", "openai", "notion"]). The UI
	// renders these as a setup checklist after a successful clone.
	RequiredCredentials []string            `bson:"required_credentials" json:"required_credentials"`
	Blocks              []models.Block      `bson:"blocks" json:"blocks"`
	Connections         []models.Connection `bson:"connections" json:"connections"`
	Variables           []models.Variable   `bson:"variables" json:"variables"`
	IsBuiltin           bool                `bson:"is_builtin" json:"is_builtin"`
	CreatedAt           time.Time           `bson:"created_at" json:"created_at"`
	UpdatedAt           time.Time           `bson:"updated_at" json:"updated_at"`
}

// WorkflowTemplateStore is the mongo-backed catalogue.
type WorkflowTemplateStore struct {
	coll         *mongo.Collection
	agentService *AgentService
}

// NewWorkflowTemplateStore constructs the store and ensures indexes. The
// agentService is needed so Clone can hand off to the existing
// agent+workflow creation path instead of duplicating that logic.
func NewWorkflowTemplateStore(db *database.MongoDB, agentService *AgentService) (*WorkflowTemplateStore, error) {
	coll := db.Collection("workflow_templates")
	s := &WorkflowTemplateStore{coll: coll, agentService: agentService}
	if err := s.ensureIndexes(context.Background()); err != nil {
		return nil, err
	}
	return s, nil
}

func (s *WorkflowTemplateStore) ensureIndexes(ctx context.Context) error {
	idx := []mongo.IndexModel{
		{
			// Slug is the stable identifier we re-seed against. Unique so
			// SeedDefaults won't insert duplicates if called twice.
			Keys:    bson.D{{Key: "slug", Value: 1}},
			Options: options.Index().SetUnique(true).SetName("slug_unique"),
		},
		{
			Keys:    bson.D{{Key: "category", Value: 1}, {Key: "name", Value: 1}},
			Options: options.Index().SetName("by_category_name"),
		},
	}
	_, err := s.coll.Indexes().CreateMany(ctx, idx)
	return err
}

// List returns all templates, optionally filtered by category.
func (s *WorkflowTemplateStore) List(ctx context.Context, category string) ([]WorkflowTemplate, error) {
	filter := bson.M{}
	if category != "" {
		filter["category"] = category
	}
	opts := options.Find().SetSort(bson.D{{Key: "category", Value: 1}, {Key: "name", Value: 1}})
	cur, err := s.coll.Find(ctx, filter, opts)
	if err != nil {
		return nil, err
	}
	defer cur.Close(ctx)
	out := []WorkflowTemplate{}
	if err := cur.All(ctx, &out); err != nil {
		return nil, err
	}
	return out, nil
}

// GetBySlug fetches a single template by slug.
func (s *WorkflowTemplateStore) GetBySlug(ctx context.Context, slug string) (*WorkflowTemplate, error) {
	var out WorkflowTemplate
	err := s.coll.FindOne(ctx, bson.M{"slug": slug}).Decode(&out)
	if err == mongo.ErrNoDocuments {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &out, nil
}

// GetByID fetches a single template by hex object id.
func (s *WorkflowTemplateStore) GetByID(ctx context.Context, id string) (*WorkflowTemplate, error) {
	oid, err := primitive.ObjectIDFromHex(id)
	if err != nil {
		return nil, fmt.Errorf("invalid template id: %w", err)
	}
	var out WorkflowTemplate
	if err := s.coll.FindOne(ctx, bson.M{"_id": oid}).Decode(&out); err != nil {
		if err == mongo.ErrNoDocuments {
			return nil, nil
		}
		return nil, err
	}
	return &out, nil
}

// CloneForUser materialises a template into a new agent + workflow owned
// by the requesting user. Returns the created Agent + Workflow so the
// caller can redirect the user into the workflow editor immediately.
//
// New block IDs are generated so re-cloning the same template doesn't
// collide on block-level state (e.g. trace spans keyed by block_id).
func (s *WorkflowTemplateStore) CloneForUser(
	ctx context.Context,
	templateID, userID, customName string,
) (*models.Agent, *models.Workflow, error) {
	tmpl, err := s.GetByID(ctx, templateID)
	if err != nil {
		return nil, nil, err
	}
	if tmpl == nil {
		return nil, nil, fmt.Errorf("template %s not found", templateID)
	}

	name := customName
	if name == "" {
		name = tmpl.Name
	}

	agent, err := s.agentService.CreateAgent(userID, name, tmpl.Description)
	if err != nil {
		return nil, nil, fmt.Errorf("create agent: %w", err)
	}

	// Re-key blocks + connections so a re-clone doesn't share IDs with a
	// previous clone. Connections must be updated in lockstep with blocks.
	oldToNew := map[string]string{}
	newBlocks := make([]models.Block, len(tmpl.Blocks))
	for i, b := range tmpl.Blocks {
		newID := uuid.New().String()
		oldToNew[b.ID] = newID
		b.ID = newID
		newBlocks[i] = b
	}
	newConns := make([]models.Connection, len(tmpl.Connections))
	for i, c := range tmpl.Connections {
		c.ID = uuid.New().String()
		if rn, ok := oldToNew[c.SourceBlockID]; ok {
			c.SourceBlockID = rn
		}
		if rn, ok := oldToNew[c.TargetBlockID]; ok {
			c.TargetBlockID = rn
		}
		newConns[i] = c
	}

	wf, err := s.agentService.SaveWorkflow(agent.ID, userID, &models.SaveWorkflowRequest{
		Blocks:      newBlocks,
		Connections: newConns,
		Variables:   tmpl.Variables,
	})
	if err != nil {
		return agent, nil, fmt.Errorf("save workflow: %w", err)
	}

	log.Printf("📦 [WORKFLOW-TEMPLATE] Cloned %q (slug=%s) → agent=%s for user=%s",
		tmpl.Name, tmpl.Slug, agent.ID, userID)
	return agent, wf, nil
}

// SeedDefaults inserts built-in templates that don't already exist (by
// slug). Idempotent — safe to run on every backend boot, and admins can
// edit existing template docs without their changes being clobbered.
func (s *WorkflowTemplateStore) SeedDefaults(ctx context.Context) error {
	defaults := getDefaultWorkflowTemplates()
	inserted := 0
	for _, tmpl := range defaults {
		tmpl.IsBuiltin = true
		tmpl.CreatedAt = time.Now()
		tmpl.UpdatedAt = time.Now()
		filter := bson.M{"slug": tmpl.Slug}
		// $setOnInsert preserves any admin edits to existing templates.
		_, err := s.coll.UpdateOne(ctx, filter,
			bson.M{"$setOnInsert": tmpl},
			options.Update().SetUpsert(true),
		)
		if err != nil {
			log.Printf("⚠️ [WORKFLOW-TEMPLATE] Failed to seed %s: %v", tmpl.Slug, err)
			continue
		}
		inserted++
	}
	log.Printf("✅ Workflow templates ready (%d built-in)", inserted)
	return nil
}

// getDefaultWorkflowTemplates is the built-in catalogue. New users see
// these in the gallery the first time they open the Workflows tab.
//
// The shapes here are intentionally minimal: the goal is to show the
// pattern (input → process → side-effect) and let the user customise
// configs to their environment. Each template's RequiredCredentials
// guides the post-clone setup checklist.
func getDefaultWorkflowTemplates() []WorkflowTemplate {
	return []WorkflowTemplate{
		// ----------------------------------------------------------------
		// 1. Incoming-email → Slack notification (with LLM filtering)
		// ----------------------------------------------------------------
		{
			Name:        "Email → Slack Notification",
			Slug:        "email-to-slack",
			Description: "Watches an inbox for new important emails, summarises them with an LLM, and posts the summary to a Slack channel. Filters out newsletters + marketing.",
			Category:    "communication",
			Icon:        "mail",
			Color:       "#4A154B",
			Tags:        []string{"email", "slack", "summarisation", "notification"},
			RequiredCredentials: []string{"gmail", "slack", "openai"},
			Blocks: []models.Block{
				{
					ID:           "email-trigger-1",
					NormalizedID: "new-email",
					Type:         "webhook",
					Name:         "New Email Trigger",
					Description:  "Fired when Gmail receives a new email (push subscription).",
					Config: map[string]any{
						"path":   "/wh/gmail",
						"method": "POST",
					},
					Position: models.Position{X: 100, Y: 200},
					Timeout:  30,
				},
				{
					ID:           "filter-llm-1",
					NormalizedID: "filter-important",
					Type:         "llm_inference",
					Name:         "Filter & Summarise",
					Description:  "Decides if the email is important; if so, returns a 2-line summary.",
					Config: map[string]any{
						"prompt": "Subject: {{new-email.subject}}\nFrom: {{new-email.from}}\nBody: {{new-email.body}}\n\nIs this email important (not marketing/newsletter)? If yes, output a 2-line summary. If no, output exactly: SKIP",
					},
					Position: models.Position{X: 400, Y: 200},
					Timeout:  60,
					RetryConfig: &models.RetryConfig{
						MaxRetries: 2,
						RetryOn:    []string{"rate_limit", "timeout"},
					},
				},
				{
					ID:           "slack-post-1",
					NormalizedID: "post-slack",
					Type:         "tool_execution",
					Name:         "Post to Slack",
					Description:  "Posts the summary to the configured Slack channel. Skips if filter returned SKIP.",
					Config: map[string]any{
						"tool":    "send_slack_message",
						"channel": "#inbox-watch",
						"text":    "📩 {{filter-important.output}}",
						"skip_if": "{{filter-important.output}} == 'SKIP'",
					},
					Position: models.Position{X: 700, Y: 200},
					Timeout:  20,
				},
			},
			Connections: []models.Connection{
				{ID: "c1", SourceBlockID: "email-trigger-1", TargetBlockID: "filter-llm-1"},
				{ID: "c2", SourceBlockID: "filter-llm-1", TargetBlockID: "slack-post-1"},
			},
			Variables: []models.Variable{
				{Name: "channel", Type: "string", DefaultValue: "#inbox-watch"},
			},
		},

		// ----------------------------------------------------------------
		// 2. Weekly KPI report from a SQL database
		// ----------------------------------------------------------------
		{
			Name:        "Weekly KPI Report",
			Slug:        "weekly-kpi-report",
			Description: "Runs every Monday at 9am. Pulls KPI metrics from a database, formats them as a markdown report, emails it to a recipient list.",
			Category:    "reporting",
			Icon:        "bar-chart-3",
			Color:       "#F44336",
			Tags:        []string{"reporting", "kpi", "sql", "email", "schedule"},
			RequiredCredentials: []string{"postgres", "smtp", "openai"},
			Blocks: []models.Block{
				{
					ID:           "schedule-1",
					NormalizedID: "weekly-monday",
					Type:         "schedule",
					Name:         "Every Monday 9am",
					Description:  "Cron trigger: 0 9 * * 1 (every Monday at 09:00 in the workflow timezone).",
					Config: map[string]any{
						"cron":     "0 9 * * 1",
						"timezone": "UTC",
					},
					Position: models.Position{X: 100, Y: 200},
					Timeout:  10,
				},
				{
					ID:           "sql-fetch-1",
					NormalizedID: "fetch-kpis",
					Type:         "tool_execution",
					Name:         "Fetch KPIs",
					Description:  "Read-only SQL query against the analytics DB. Returns a single row of metrics.",
					Config: map[string]any{
						"tool":  "sql_query",
						"query": "SELECT signups, active_users, mrr FROM kpi_weekly WHERE week = date_trunc('week', now() - interval '1 week')",
					},
					Position: models.Position{X: 400, Y: 200},
					Timeout:  60,
				},
				{
					ID:           "format-llm-1",
					NormalizedID: "format-report",
					Type:         "llm_inference",
					Name:         "Format Report",
					Description:  "Turns the row into a markdown report with brief WoW commentary.",
					Config: map[string]any{
						"prompt": "Last week's KPIs:\n{{fetch-kpis.rows}}\n\nWrite a 1-page markdown report. Include a Highlights section, a Lowlights section, and one suggested action. Keep tone professional.",
					},
					Position: models.Position{X: 700, Y: 200},
					Timeout:  90,
				},
				{
					ID:           "email-send-1",
					NormalizedID: "send-email",
					Type:         "tool_execution",
					Name:         "Email Report",
					Description:  "Sends the formatted markdown report to the recipient list.",
					Config: map[string]any{
						"tool":    "send_email",
						"to":      "team@example.com",
						"subject": "Weekly KPI Report — {{now | date:'YYYY-MM-DD'}}",
						"body":    "{{format-report.output}}",
					},
					Position: models.Position{X: 1000, Y: 200},
					Timeout:  20,
				},
			},
			Connections: []models.Connection{
				{ID: "c1", SourceBlockID: "schedule-1", TargetBlockID: "sql-fetch-1"},
				{ID: "c2", SourceBlockID: "sql-fetch-1", TargetBlockID: "format-llm-1"},
				{ID: "c3", SourceBlockID: "format-llm-1", TargetBlockID: "email-send-1"},
			},
			Variables: []models.Variable{
				{Name: "recipients", Type: "string", DefaultValue: "team@example.com"},
			},
		},

		// ----------------------------------------------------------------
		// 3. Scheduled web scraping → JSON dataset
		// ----------------------------------------------------------------
		{
			Name:        "Scheduled Web Scraping",
			Slug:        "scheduled-scraping",
			Description: "Runs daily. Fetches a list of pages, extracts structured rows, appends to a Google Sheet or stores as JSON. Polite delays + retry on transient errors.",
			Category:    "data",
			Icon:        "spider",
			Color:       "#795548",
			Tags:        []string{"scraping", "schedule", "extraction", "etl"},
			RequiredCredentials: []string{"google_sheets"},
			Blocks: []models.Block{
				{
					ID:           "schedule-1",
					NormalizedID: "daily-9am",
					Type:         "schedule",
					Name:         "Daily 9am",
					Config: map[string]any{
						"cron":     "0 9 * * *",
						"timezone": "UTC",
					},
					Position: models.Position{X: 100, Y: 200},
					Timeout:  10,
				},
				{
					ID:           "scrape-1",
					NormalizedID: "scrape-pages",
					Type:         "tool_execution",
					Name:         "Scrape Target Pages",
					Description:  "Iterates over the target URL list, extracts the configured fields per page. 1.5s delay between requests.",
					Config: map[string]any{
						"tool":           "web_scrape",
						"urls":           []string{"https://example.com/list"},
						"fields":         []string{"title", "price", "url"},
						"delay_ms":       1500,
						"respect_robots": true,
					},
					Position: models.Position{X: 400, Y: 200},
					Timeout:  300,
					RetryConfig: &models.RetryConfig{
						MaxRetries: 3,
						RetryOn:    []string{"network_error", "server_error"},
						BackoffMs:  2000,
					},
				},
				{
					ID:           "sheet-append-1",
					NormalizedID: "append-sheet",
					Type:         "tool_execution",
					Name:         "Append to Sheet",
					Description:  "Appends today's scraped rows to a Google Sheet.",
					Config: map[string]any{
						"tool":      "google_sheets_append",
						"sheet_id":  "your-google-sheet-id",
						"range":     "Scraped!A:D",
						"rows":      "{{scrape-pages.rows}}",
					},
					Position: models.Position{X: 700, Y: 200},
					Timeout:  60,
				},
			},
			Connections: []models.Connection{
				{ID: "c1", SourceBlockID: "schedule-1", TargetBlockID: "scrape-1"},
				{ID: "c2", SourceBlockID: "scrape-1", TargetBlockID: "sheet-append-1"},
			},
		},

		// ----------------------------------------------------------------
		// 4. Document summariser → Notion page
		// ----------------------------------------------------------------
		{
			Name:        "Doc Summariser → Notion",
			Slug:        "doc-summariser-notion",
			Description: "Drop a long doc (URL or text) on the trigger; the workflow extracts text, chunks it, summarises each chunk, combines into an executive summary, and creates a Notion page.",
			Category:    "knowledge",
			Icon:        "book-open",
			Color:       "#000000",
			Tags:        []string{"summarisation", "notion", "rag", "documents"},
			RequiredCredentials: []string{"notion", "openai"},
			Blocks: []models.Block{
				{
					ID:           "webhook-1",
					NormalizedID: "doc-trigger",
					Type:         "webhook",
					Name:         "Document Trigger",
					Description:  "POST a JSON body {url|text, title?} to start the summary.",
					Config: map[string]any{
						"path":   "/wh/summarise-doc",
						"method": "POST",
					},
					Position: models.Position{X: 100, Y: 200},
					Timeout:  20,
				},
				{
					ID:           "extract-1",
					NormalizedID: "extract-text",
					Type:         "tool_execution",
					Name:         "Extract Text",
					Description:  "If input.url present, fetch + extract clean text; otherwise pass through input.text.",
					Config: map[string]any{
						"tool":     "document_extract",
						"source":   "{{doc-trigger.url|doc-trigger.text}}",
					},
					Position: models.Position{X: 400, Y: 200},
					Timeout:  90,
				},
				{
					ID:           "summarise-1",
					NormalizedID: "summarise-chunks",
					Type:         "llm_inference",
					Name:         "Chunked Summarise",
					Description:  "Map-reduce summariser: per-chunk summaries, then a final synthesis pass.",
					Config: map[string]any{
						"prompt":      "Summarise this in 3-5 bullet points capturing the core ideas:\n\n{{extract-text.text}}",
						"map_reduce":  true,
						"chunk_size":  3000,
					},
					Position: models.Position{X: 700, Y: 200},
					Timeout:  300,
				},
				{
					ID:           "notion-create-1",
					NormalizedID: "create-notion-page",
					Type:         "tool_execution",
					Name:         "Create Notion Page",
					Description:  "Creates a page under the configured Notion database with the summary as content.",
					Config: map[string]any{
						"tool":        "notion_create_page",
						"database_id": "your-notion-database-id",
						"title":       "{{doc-trigger.title|extract-text.title|Untitled}}",
						"content":     "{{summarise-chunks.output}}",
					},
					Position: models.Position{X: 1000, Y: 200},
					Timeout:  30,
				},
			},
			Connections: []models.Connection{
				{ID: "c1", SourceBlockID: "webhook-1", TargetBlockID: "extract-1"},
				{ID: "c2", SourceBlockID: "extract-1", TargetBlockID: "summarise-1"},
				{ID: "c3", SourceBlockID: "summarise-1", TargetBlockID: "notion-create-1"},
			},
		},

		// ----------------------------------------------------------------
		// 5. Daily digest: news + calendar + tasks
		// ----------------------------------------------------------------
		{
			Name:        "Daily Personal Digest",
			Slug:        "daily-personal-digest",
			Description: "Runs every weekday morning. Pulls top news in your interest areas, today's calendar, and open tasks; assembles into a single digest delivered via Telegram or email.",
			Category:    "personal",
			Icon:        "sunrise",
			Color:       "#FFB300",
			Tags:        []string{"digest", "personal", "morning", "news", "calendar"},
			RequiredCredentials: []string{"google_calendar", "telegram", "openai"},
			Blocks: []models.Block{
				{
					ID:           "schedule-1",
					NormalizedID: "weekday-7am",
					Type:         "schedule",
					Name:         "Weekdays 7am",
					Config: map[string]any{
						"cron":     "0 7 * * 1-5",
						"timezone": "UTC",
					},
					Position: models.Position{X: 100, Y: 200},
					Timeout:  10,
				},
				{
					ID:           "news-1",
					NormalizedID: "fetch-news",
					Type:         "tool_execution",
					Name:         "Fetch Top News",
					Config: map[string]any{
						"tool":   "search_web",
						"query":  "top tech news today",
						"limit":  5,
					},
					Position: models.Position{X: 400, Y: 100},
					Timeout:  60,
				},
				{
					ID:           "calendar-1",
					NormalizedID: "fetch-calendar",
					Type:         "tool_execution",
					Name:         "Fetch Today's Calendar",
					Config: map[string]any{
						"tool":    "google_calendar_list",
						"range":   "today",
					},
					Position: models.Position{X: 400, Y: 250},
					Timeout:  30,
				},
				{
					ID:           "assemble-1",
					NormalizedID: "assemble-digest",
					Type:         "llm_inference",
					Name:         "Assemble Digest",
					Description:  "Combines news + calendar into a friendly morning brief (max 200 words).",
					Config: map[string]any{
						"prompt": "Create a friendly 200-word morning digest from:\n\nNEWS:\n{{fetch-news.results}}\n\nCALENDAR:\n{{fetch-calendar.events}}\n\nFormat: greeting → 3 news bullets → today's first 3 events → one motivational line.",
					},
					Position: models.Position{X: 700, Y: 175},
					Timeout:  60,
				},
				{
					ID:           "telegram-1",
					NormalizedID: "send-digest",
					Type:         "tool_execution",
					Name:         "Send via Telegram",
					Config: map[string]any{
						"tool":       "send_telegram_message",
						"chat_id":    "your-telegram-chat-id",
						"text":       "{{assemble-digest.output}}",
						"parse_mode": "MarkdownV2",
					},
					Position: models.Position{X: 1000, Y: 175},
					Timeout:  20,
				},
			},
			Connections: []models.Connection{
				{ID: "c1", SourceBlockID: "schedule-1", TargetBlockID: "news-1"},
				{ID: "c2", SourceBlockID: "schedule-1", TargetBlockID: "calendar-1"},
				{ID: "c3", SourceBlockID: "news-1", TargetBlockID: "assemble-1"},
				{ID: "c4", SourceBlockID: "calendar-1", TargetBlockID: "assemble-1"},
				{ID: "c5", SourceBlockID: "assemble-1", TargetBlockID: "telegram-1"},
			},
		},
	}
}
