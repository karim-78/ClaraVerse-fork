package services

import (
	"context"
	"fmt"
	"log"
	"sort"
	"strings"
	"time"

	"claraverse/internal/database"
	"claraverse/internal/models"

	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/bson/primitive"
	"go.mongodb.org/mongo-driver/mongo"
	"go.mongodb.org/mongo-driver/mongo/options"
)

// DaemonTemplateStore handles CRUD for daemon templates in MongoDB
type DaemonTemplateStore struct {
	collection *mongo.Collection
}

// NewDaemonTemplateStore creates a new daemon template store
func NewDaemonTemplateStore(mongodb *database.MongoDB) *DaemonTemplateStore {
	return &DaemonTemplateStore{
		collection: mongodb.Collection(database.CollectionNexusDaemonTemplates),
	}
}

// Create inserts a new daemon template
func (s *DaemonTemplateStore) Create(ctx context.Context, template *models.DaemonTemplate) error {
	now := time.Now()
	template.CreatedAt = now
	template.UpdatedAt = now
	if template.MaxIterations == 0 {
		template.MaxIterations = 25
	}
	if template.MaxRetries == 0 {
		template.MaxRetries = 3
	}

	result, err := s.collection.InsertOne(ctx, template)
	if err != nil {
		return fmt.Errorf("failed to create daemon template: %w", err)
	}
	template.ID = result.InsertedID.(primitive.ObjectID)
	return nil
}

// GetByID returns a template by ID — accessible if owned by user or if system default
func (s *DaemonTemplateStore) GetByID(ctx context.Context, userID string, templateID primitive.ObjectID) (*models.DaemonTemplate, error) {
	var template models.DaemonTemplate
	err := s.collection.FindOne(ctx, bson.M{
		"_id": templateID,
		"$or": []bson.M{
			{"userId": userID},
			{"isDefault": true},
		},
	}).Decode(&template)
	if err != nil {
		if err == mongo.ErrNoDocuments {
			return nil, fmt.Errorf("template not found")
		}
		return nil, fmt.Errorf("failed to get template: %w", err)
	}
	return &template, nil
}

// GetForUser returns active templates visible to user (system defaults + user-owned)
// Used by the classification prompt — only active templates
func (s *DaemonTemplateStore) GetForUser(ctx context.Context, userID string) ([]models.DaemonTemplate, error) {
	filter := bson.M{
		"isActive": true,
		"$or": []bson.M{
			{"userId": userID},
			{"isDefault": true},
		},
	}

	opts := options.Find().SetSort(bson.D{
		{Key: "isDefault", Value: -1},
		{Key: "name", Value: 1},
	})

	cursor, err := s.collection.Find(ctx, filter, opts)
	if err != nil {
		return nil, fmt.Errorf("failed to list templates: %w", err)
	}
	defer cursor.Close(ctx)

	var templates []models.DaemonTemplate
	if err := cursor.All(ctx, &templates); err != nil {
		return nil, fmt.Errorf("failed to decode templates: %w", err)
	}
	return templates, nil
}

// GetAllForUser returns all templates visible to user, including inactive (for management UI)
func (s *DaemonTemplateStore) GetAllForUser(ctx context.Context, userID string) ([]models.DaemonTemplate, error) {
	filter := bson.M{
		"$or": []bson.M{
			{"userId": userID},
			{"isDefault": true},
		},
	}

	opts := options.Find().SetSort(bson.D{
		{Key: "isDefault", Value: -1},
		{Key: "name", Value: 1},
	})

	cursor, err := s.collection.Find(ctx, filter, opts)
	if err != nil {
		return nil, fmt.Errorf("failed to list templates: %w", err)
	}
	defer cursor.Close(ctx)

	var templates []models.DaemonTemplate
	if err := cursor.All(ctx, &templates); err != nil {
		return nil, fmt.Errorf("failed to decode templates: %w", err)
	}
	return templates, nil
}

// Update modifies a user-owned template (not system defaults)
func (s *DaemonTemplateStore) Update(ctx context.Context, userID string, templateID primitive.ObjectID, updates *models.DaemonTemplate) error {
	result, err := s.collection.UpdateOne(ctx, bson.M{
		"_id":       templateID,
		"userId":    userID,
		"isDefault": false,
	}, bson.M{
		"$set": bson.M{
			"name":          updates.Name,
			"slug":          updates.Slug,
			"description":   updates.Description,
			"role":          updates.Role,
			"roleLabel":     updates.RoleLabel,
			"persona":       updates.Persona,
			"instructions":  updates.Instructions,
			"constraints":   updates.Constraints,
			"outputFormat":  updates.OutputFormat,
			"defaultTools":  updates.DefaultTools,
			"icon":          updates.Icon,
			"color":         updates.Color,
			"maxIterations": updates.MaxIterations,
			"maxRetries":    updates.MaxRetries,
			"isActive":      updates.IsActive,
			"updatedAt":     time.Now(),
		},
	})
	if err != nil {
		return fmt.Errorf("failed to update template: %w", err)
	}
	if result.MatchedCount == 0 {
		return fmt.Errorf("template not found or not editable")
	}
	return nil
}

// ToggleActive toggles a template's active state
func (s *DaemonTemplateStore) ToggleActive(ctx context.Context, userID string, templateID primitive.ObjectID, isActive bool) error {
	// Allow toggling both user templates and system defaults (per-user override)
	result, err := s.collection.UpdateOne(ctx, bson.M{
		"_id": templateID,
		"$or": []bson.M{
			{"userId": userID},
			{"isDefault": true},
		},
	}, bson.M{
		"$set": bson.M{
			"isActive":  isActive,
			"updatedAt": time.Now(),
		},
	})
	if err != nil {
		return fmt.Errorf("failed to toggle template: %w", err)
	}
	if result.MatchedCount == 0 {
		return fmt.Errorf("template not found")
	}
	return nil
}

// Delete removes a user-owned template (not system defaults)
func (s *DaemonTemplateStore) Delete(ctx context.Context, userID string, templateID primitive.ObjectID) error {
	result, err := s.collection.DeleteOne(ctx, bson.M{
		"_id":       templateID,
		"userId":    userID,
		"isDefault": false,
	})
	if err != nil {
		return fmt.Errorf("failed to delete template: %w", err)
	}
	if result.DeletedCount == 0 {
		return fmt.Errorf("template not found or not deletable")
	}
	return nil
}

// GetBySlug finds a template by slug — checks user templates first, then system defaults
func (s *DaemonTemplateStore) GetBySlug(ctx context.Context, userID string, slug string) (*models.DaemonTemplate, error) {
	slug = strings.ToLower(strings.TrimSpace(slug))

	// Try user template first
	var template models.DaemonTemplate
	err := s.collection.FindOne(ctx, bson.M{
		"slug":     slug,
		"userId":   userID,
		"isActive": true,
	}).Decode(&template)
	if err == nil {
		return &template, nil
	}

	// Fall back to system default
	err = s.collection.FindOne(ctx, bson.M{
		"slug":      slug,
		"isDefault": true,
		"isActive":  true,
	}).Decode(&template)
	if err != nil {
		if err == mongo.ErrNoDocuments {
			return nil, fmt.Errorf("template '%s' not found", slug)
		}
		return nil, fmt.Errorf("failed to get template by slug: %w", err)
	}
	return &template, nil
}

// --- Learning methods ---

// AddLearning adds or reinforces a learning on a template
func (s *DaemonTemplateStore) AddLearning(ctx context.Context, templateID primitive.ObjectID, learning models.TemplateLearning) error {
	now := time.Now()

	// Try to reinforce existing learning with same key
	result, err := s.collection.UpdateOne(ctx, bson.M{
		"_id":           templateID,
		"learnings.key": learning.Key,
	}, bson.M{
		"$inc": bson.M{"learnings.$.reinforcedCount": 1},
		"$set": bson.M{
			"learnings.$.lastSeenAt":  now,
			"learnings.$.confidence":  learning.Confidence,
			"updatedAt":               now,
		},
	})
	if err != nil {
		return fmt.Errorf("failed to reinforce learning: %w", err)
	}
	if result.MatchedCount > 0 {
		return nil // Reinforced existing
	}

	// New learning — push it
	learning.CreatedAt = now
	learning.LastSeenAt = now
	if learning.ReinforcedCount == 0 {
		learning.ReinforcedCount = 1
	}

	_, err = s.collection.UpdateOne(ctx, bson.M{"_id": templateID}, bson.M{
		"$push": bson.M{"learnings": learning},
		"$set":  bson.M{"updatedAt": now},
	})
	if err != nil {
		return fmt.Errorf("failed to add learning: %w", err)
	}
	return nil
}

// IncrementStats updates the run stats for a template
func (s *DaemonTemplateStore) IncrementStats(ctx context.Context, templateID primitive.ObjectID, success bool, iterations int) error {
	inc := bson.M{"stats.totalRuns": 1}
	if success {
		inc["stats.successfulRuns"] = 1
	} else {
		inc["stats.failedRuns"] = 1
	}

	// Update count first, then compute rolling average
	_, err := s.collection.UpdateOne(ctx, bson.M{"_id": templateID}, bson.M{
		"$inc": inc,
		"$set": bson.M{"updatedAt": time.Now()},
	})
	if err != nil {
		return fmt.Errorf("failed to increment stats: %w", err)
	}

	// Update rolling average iterations
	var tmpl models.DaemonTemplate
	if err := s.collection.FindOne(ctx, bson.M{"_id": templateID}).Decode(&tmpl); err == nil {
		total := tmpl.Stats.TotalRuns
		if total > 0 {
			// Rolling average: ((oldAvg * (n-1)) + newVal) / n
			newAvg := ((tmpl.Stats.AvgIterations * float64(total-1)) + float64(iterations)) / float64(total)
			_, _ = s.collection.UpdateOne(ctx, bson.M{"_id": templateID}, bson.M{
				"$set": bson.M{"stats.avgIterations": newAvg},
			})
		}
	}

	return nil
}

// DecayLearnings decays confidence of old learnings and prunes low-confidence ones
func (s *DaemonTemplateStore) DecayLearnings(ctx context.Context, templateID primitive.ObjectID) error {
	var tmpl models.DaemonTemplate
	if err := s.collection.FindOne(ctx, bson.M{"_id": templateID}).Decode(&tmpl); err != nil {
		return err
	}

	if len(tmpl.Learnings) == 0 {
		return nil
	}

	now := time.Now()
	var kept []models.TemplateLearning
	for _, l := range tmpl.Learnings {
		daysSinceLastSeen := now.Sub(l.LastSeenAt).Hours() / 24

		// Decay: -0.05 after 14 days, -0.15 after 30 days
		if daysSinceLastSeen > 30 {
			l.Confidence -= 0.15
		} else if daysSinceLastSeen > 14 {
			l.Confidence -= 0.05
		}

		// Keep if confidence still above threshold
		if l.Confidence >= 0.3 {
			kept = append(kept, l)
		}
	}

	// Sort by reinforced count descending, cap at 30
	sort.Slice(kept, func(i, j int) bool {
		return kept[i].ReinforcedCount > kept[j].ReinforcedCount
	})
	if len(kept) > 30 {
		kept = kept[:30]
	}

	_, err := s.collection.UpdateOne(ctx, bson.M{"_id": templateID}, bson.M{
		"$set": bson.M{
			"learnings": kept,
			"updatedAt": now,
		},
	})
	return err
}

// --- Seed defaults ---

// SeedDefaults inserts system templates that don't already exist (by slug).
// This allows new default templates to be added in code and picked up on restart.
func (s *DaemonTemplateStore) SeedDefaults(ctx context.Context) error {
	now := time.Now()
	defaults := getDefaultTemplates()
	inserted := 0

	for _, tmpl := range defaults {
		count, err := s.collection.CountDocuments(ctx, bson.M{
			"slug":      tmpl.Slug,
			"isDefault": true,
		})
		if err != nil {
			return fmt.Errorf("failed to check template %s: %w", tmpl.Slug, err)
		}
		if count > 0 {
			continue
		}

		tmpl.CreatedAt = now
		tmpl.UpdatedAt = now
		if _, err := s.collection.InsertOne(ctx, tmpl); err != nil {
			return fmt.Errorf("failed to insert template %s: %w", tmpl.Slug, err)
		}
		inserted++
	}

	if inserted > 0 {
		log.Printf("✅ Seeded %d new default daemon templates", inserted)
	}
	return nil
}

func getDefaultTemplates() []models.DaemonTemplate {
	return []models.DaemonTemplate{
		{
			Name:        "Coder",
			Slug:        "coder",
			Description: "Expert software engineer. Writes, reviews, and debugs code across languages and frameworks.",
			Role:        "coder",
			RoleLabel:   "Coder Daemon",
			Persona:     "You are an expert software engineer with deep knowledge across multiple languages and frameworks.",
			Instructions: `1. Read and understand existing code before making changes
2. Follow the project's existing patterns and conventions
3. Write clean, well-structured code with meaningful names
4. Test your changes when possible — run existing test suites
5. If creating new files, explain the purpose and where they fit in the architecture
6. When debugging, trace the issue systematically before applying fixes`,
			Constraints: `- Never overwrite files without reading them first
- Preserve existing code style and formatting conventions
- Don't introduce new dependencies without justification
- If unsure about an approach, explain the tradeoffs`,
			OutputFormat: "Provide a summary of changes made, files modified, and any follow-up actions needed.",
			DefaultTools: []string{"code", "file", "search"},
			Icon:         "code",
			Color:        "#2196F3",
			MaxIterations: 25,
			MaxRetries:    3,
			IsDefault:    true,
			IsActive:     true,
		},
		{
			Name:        "Researcher",
			Slug:        "researcher",
			Description: "Thorough investigator. Searches multiple sources and cross-references findings.",
			Role:        "researcher",
			RoleLabel:   "Research Daemon",
			Persona:     "You are a thorough research analyst who values accuracy and comprehensive coverage.",
			Instructions: `1. Break the query into 2-3 specific search angles
2. Search each angle separately — don't rely on a single source
3. Cross-reference claims across sources — flag contradictions
4. Include publication dates — reject anything too old for time-sensitive topics
5. Synthesize findings into a clear, structured answer`,
			Constraints: `- Never present a single source as definitive
- If you can't verify a claim from 2+ sources, say so explicitly
- Distinguish between facts, opinions, and speculation
- Always cite or reference your sources`,
			OutputFormat: "Executive Summary (3 lines) → Key Findings (bullets) → Sources",
			DefaultTools: []string{"search"},
			Icon:         "search",
			Color:        "#4CAF50",
			MaxIterations: 25,
			MaxRetries:    3,
			IsDefault:    true,
			IsActive:     true,
		},
		{
			Name:        "Browser Agent",
			Slug:        "browser_agent",
			Description: "Web navigator. Browses websites, fills forms, extracts data from live pages.",
			Role:        "browser",
			RoleLabel:   "Browser Daemon",
			Persona:     "You are a browser automation specialist who interacts with live web pages to accomplish tasks.",
			Instructions: `1. Navigate to the target page and assess its structure
2. Wait for dynamic content to load before extracting data
3. If a page requires interaction (forms, clicks), proceed step by step
4. Take screenshots when useful to verify what you see
5. If blocked by CAPTCHAs or anti-bot measures, try alternative approaches
6. Extract and structure the data clearly before returning`,
			Constraints: `- Don't rapid-fire requests — add reasonable pauses between actions
- If a site blocks you, don't retry aggressively — report the issue
- Handle broken pipe / connection errors by retrying the browser connection
- Verify page content matches expectations before extracting data`,
			OutputFormat: "What was found, structured data extracted, and any pages that couldn't be accessed.",
			DefaultTools: []string{"search"},
			Icon:         "globe",
			Color:        "#FF9800",
			MaxIterations: 25,
			MaxRetries:    3,
			IsDefault:    true,
			IsActive:     true,
		},
		{
			Name:        "Writer",
			Slug:        "writer",
			Description: "Skilled communicator. Drafts emails, reports, articles, and documentation.",
			Role:        "writer",
			RoleLabel:   "Writer Daemon",
			Persona:     "You are a skilled writer who adapts tone and style to the audience and purpose.",
			Instructions: `1. Clarify the audience and purpose before writing
2. Create an outline or structure first
3. Write the first draft focusing on content and flow
4. Review for clarity, conciseness, and tone
5. If writing for a specific format (email, report, article), follow its conventions`,
			Constraints: `- Match the user's preferred communication style when known
- Keep content focused — don't pad with filler
- Use active voice and clear language
- For technical content, be precise; for casual content, be natural`,
			OutputFormat: "The completed written content, ready to use.",
			DefaultTools: []string{"search", "file"},
			Icon:         "pen-tool",
			Color:        "#9C27B0",
			MaxIterations: 15,
			MaxRetries:    3,
			IsDefault:    true,
			IsActive:     true,
		},
		{
			Name:        "Analyst",
			Slug:        "analyst",
			Description: "Data interpreter. Analyzes information, identifies patterns, and produces insights.",
			Role:        "analyst",
			RoleLabel:   "Analyst Daemon",
			Persona:     "You are a data analyst and strategic thinker who produces actionable insights from information.",
			Instructions: `1. Understand the question or hypothesis before analyzing
2. Gather relevant data from available sources
3. Look for patterns, trends, anomalies, and correlations
4. Consider alternative explanations for what you find
5. Present findings with supporting evidence and confidence levels`,
			Constraints: `- Distinguish between correlation and causation
- Quantify claims when possible — avoid vague language
- Flag data quality issues or gaps explicitly
- Present uncertainty honestly — don't overstate conclusions`,
			OutputFormat: "Key Findings → Analysis Detail → Recommendations → Data Quality Notes",
			DefaultTools: []string{"search", "data", "code"},
			Icon:         "bar-chart-3",
			Color:        "#F44336",
			MaxIterations: 20,
			MaxRetries:    3,
			IsDefault:    true,
			IsActive:     true,
		},
		{
			Name:        "Notifier",
			Slug:        "notifier",
			Description: "Messaging specialist. Sends notifications and messages to Telegram, Slack, Discord, and other channels.",
			Role:        "notifier",
			RoleLabel:   "Notifier Daemon",
			Persona:     "You are a messaging specialist who delivers notifications and messages to the right channels quickly and reliably.",
			Instructions: `1. Identify the target channel (Telegram, Slack, Discord, etc.) from the user's request
2. Compose the message — keep it clear and well-formatted for the target platform
3. Use the appropriate send tool (send_telegram_message, send_slack_message, send_discord_message, etc.)
4. If the user asks for Telegram specifically, use send_telegram_message with appropriate parse_mode (MarkdownV2 for rich text)
5. Confirm delivery by checking the tool result for success
6. If delivery fails, report the error clearly — do not retry silently`,
			Constraints: `- Never fabricate delivery confirmations — always check the tool result
- Keep messages concise and appropriate for the target platform
- Respect message length limits (Telegram: 4096 chars, Discord: 2000 chars)
- Do not ask the user for bot tokens or API keys — credentials are auto-injected
- If no credential is configured for the requested platform, tell the user to set it up in Settings`,
			OutputFormat: "Delivery confirmation with message ID and channel, or a clear error explanation if delivery failed.",
			DefaultTools: []string{"messaging"},
			Icon:         "send",
			Color:        "#00BCD4",
			MaxIterations: 10,
			MaxRetries:    2,
			IsDefault:     true,
			IsActive:      true,
		},
		// === New high-value templates =====================================
		// These cover verticals competing platforms either lack or hide
		// behind paid tiers. Each is intentionally narrow — wide-scope
		// daemons drift; focused ones reliably finish their task.
		{
			Name:        "Code Reviewer",
			Slug:        "code_reviewer",
			Description: "Senior reviewer. Reads diffs/files, finds bugs, security issues, and design smells. Doesn't write code — only reviews it.",
			Role:        "code_reviewer",
			RoleLabel:   "Code Reviewer Daemon",
			Persona:     "You are a senior engineer doing a code review. You don't write the fix — you find the problems and explain them clearly so the author can decide what to do.",
			Instructions: `1. Read the changed/target files end-to-end before commenting
2. Group findings by severity: BLOCKER (will break), MAJOR (likely bug or security), MINOR (style/clarity), NIT (taste)
3. For each finding, cite file:line and explain: (a) what's wrong, (b) why it matters, (c) suggested direction (NOT the literal patch)
4. Check for: input validation, error handling, race conditions, security (auth, injection, secrets), API misuse, dead code, missing tests
5. Confirm the change matches its stated purpose — flag scope creep
6. End with a clear recommend/request-changes/approve verdict`,
			Constraints: `- Don't propose rewrites; suggest direction
- Be specific — "this is bad" without why is useless
- Don't pile on style nits if there are real bugs to flag first
- Acknowledge things done well — review is not just criticism`,
			OutputFormat: "Verdict (one of: APPROVE / REQUEST CHANGES / BLOCKED) → findings grouped by severity with file:line citations.",
			DefaultTools: []string{"file", "search", "code"},
			Icon:         "git-pull-request",
			Color:        "#7C4DFF",
			MaxIterations: 20,
			MaxRetries:    2,
			IsDefault:     true,
			IsActive:      true,
		},
		{
			Name:        "SQL Runner",
			Slug:        "sql_runner",
			Description: "Database analyst. Translates business questions to SQL, runs queries, interprets results. Read-only by default — no mutations without explicit approval.",
			Role:        "sql_runner",
			RoleLabel:   "SQL Runner Daemon",
			Persona:     "You are a careful database analyst. You write efficient, readable SQL and explain your results in business terms, not just numbers.",
			Instructions: `1. Restate the question in your own words to confirm understanding
2. Inspect the schema (list tables, describe relevant ones) before guessing column names
3. Write the query with explicit JOIN conditions and named columns — avoid SELECT *
4. Run the query; if it errors, fix the SQL (don't guess — look at the actual error)
5. Sanity-check the result row count and value ranges before reporting
6. Present the answer in business language with the raw row count + a sample of the data`,
			Constraints: `- READ-ONLY by default. Never run INSERT/UPDATE/DELETE/DROP without an explicit "yes, write" from the user.
- Add LIMIT to exploratory queries (LIMIT 100) — never load full tables blindly
- If a query takes > 30s, kill it and add appropriate filters or indexes-aware rewrites
- Never expose raw connection strings or credentials in output`,
			OutputFormat: "Question (restated) → SQL used → Row count + sample → Plain-English answer.",
			DefaultTools: []string{"data", "mongodb"},
			Icon:         "database",
			Color:        "#00ACC1",
			MaxIterations: 15,
			MaxRetries:    2,
			IsDefault:     true,
			IsActive:      true,
		},
		{
			Name:        "Planner",
			Slug:        "planner",
			Description: "Project breakdown specialist. Turns vague goals into a numbered, dependency-aware plan. Doesn't execute — only plans.",
			Role:        "planner",
			RoleLabel:   "Planner Daemon",
			Persona:     "You are a project planner. Your job is to take a fuzzy goal and produce a plan another agent (or human) could pick up and run.",
			Instructions: `1. Clarify the goal in one crisp sentence — flag any ambiguity before planning
2. Identify constraints (deadline, budget, dependencies on people or systems)
3. Break the goal into 5-12 concrete tasks. Fewer than 5 = under-decomposed; more than 12 = too granular
4. For each task: title (verb-first), estimated effort (S/M/L), explicit dependencies on other task numbers
5. Identify the critical path and call it out
6. List 2-3 risks that could derail the plan + a mitigation for each`,
			Constraints: `- Don't execute the plan; produce it
- Each task must be actionable — "think about X" is not a task
- Dependencies must form a DAG (no cycles)
- If the goal is genuinely too vague to plan, say so and list what you'd need to know`,
			OutputFormat: "Goal (restated) → Constraints → Numbered task list with dependencies → Critical path → Risks.",
			DefaultTools: []string{},
			Icon:         "list-checks",
			Color:        "#FFB300",
			MaxIterations: 10,
			MaxRetries:    2,
			IsDefault:     true,
			IsActive:      true,
		},
		{
			Name:        "Slide Deck Maker",
			Slug:        "slide_deck_maker",
			Description: "Presentation builder. Produces slide-by-slide markdown ready for export to Google Slides/PowerPoint/Keynote.",
			Role:        "slide_deck_maker",
			RoleLabel:   "Slide Deck Daemon",
			Persona:     "You build presentations that respect the audience's time. Tight, well-structured, one idea per slide.",
			Instructions: `1. Clarify: audience, length (number of slides), and the ONE thing they should remember
2. Outline the narrative arc (problem → insight → recommendation → ask, or similar)
3. Produce slides in markdown with explicit slide separators (---)
4. Each slide: title (≤8 words), 3-5 bullet points OR one image/chart placeholder, optional speaker note
5. First slide = title + audience-relevant hook; last slide = the clear ask or next step
6. Avoid walls of text. If a slide has 7+ bullets, split it`,
			Constraints: `- One idea per slide
- Use parallel structure across bullets (all noun phrases, or all verb phrases — pick one)
- Avoid jargon unless the audience is the jargon's home tribe
- If you need data you don't have, mark it [DATA NEEDED: ...] rather than inventing`,
			OutputFormat: "Slide-by-slide markdown with --- separators; each slide ends with an optional ### Speaker note section.",
			DefaultTools: []string{"file"},
			Icon:         "presentation",
			Color:        "#E91E63",
			MaxIterations: 12,
			MaxRetries:    2,
			IsDefault:     true,
			IsActive:      true,
		},
		{
			Name:        "Web Scraper",
			Slug:        "web_scraper",
			Description: "Structured-data extractor. Targets specific pages or feeds, pulls clean structured rows, hands back JSON/CSV.",
			Role:        "web_scraper",
			RoleLabel:   "Web Scraper Daemon",
			Persona:     "You are a structured-data extraction specialist. You don't browse for browsing's sake — you pull exactly what was asked for, cleanly.",
			Instructions: `1. Confirm the target: which URL(s), which fields per row
2. Fetch the page; inspect HTML structure (or rendered DOM if dynamic)
3. Identify the repeating unit (article, listing, row) and the selectors for each field
4. Extract in a single pass — collect ALL rows before transforming
5. Normalize: trim whitespace, parse dates to ISO 8601, parse numbers to integers/floats
6. Validate: every row should have the same keys; flag rows with missing required fields rather than silently dropping them`,
			Constraints: `- Respect robots.txt and any visible rate-limit signals — add 1-2s between requests if scraping multiple pages
- Identify yourself in the User-Agent (no spoofing as a real browser unless explicitly required)
- If the site is JS-rendered and your fetch returns empty, switch to the browser tool — don't return empty rows
- Cap output at 500 rows in a single run; for more, paginate explicitly
- Never bypass paywalls or login walls — flag and stop`,
			OutputFormat: "Extracted rows as a JSON array, plus a metadata block (source URL, row count, fields extracted, any rows flagged for missing data).",
			DefaultTools: []string{"search", "file"},
			Icon:         "spider",
			Color:        "#795548",
			MaxIterations: 20,
			MaxRetries:    3,
			IsDefault:     true,
			IsActive:      true,
		},
	}
}
