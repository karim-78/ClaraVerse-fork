package services

import (
	"context"
	"fmt"
	"strings"

	"claraverse/internal/models"

	"go.mongodb.org/mongo-driver/bson/primitive"
)

// CortexContextBuilder assembles system prompts for Cortex and Daemons
type CortexContextBuilder struct {
	personaService       *PersonaService
	engramService        *EngramService
	sessionStore         *NexusSessionStore
	memorySelectionSvc   *MemorySelectionService
	templateStore        *DaemonTemplateStore
	skillService         *SkillService
}

// NewCortexContextBuilder creates a new context builder
func NewCortexContextBuilder(
	personaService *PersonaService,
	engramService *EngramService,
	sessionStore *NexusSessionStore,
	memorySelectionSvc *MemorySelectionService,
) *CortexContextBuilder {
	return &CortexContextBuilder{
		personaService:     personaService,
		engramService:      engramService,
		sessionStore:       sessionStore,
		memorySelectionSvc: memorySelectionSvc,
	}
}

// BuildCortexSystemPrompt assembles the full system prompt for Cortex
func (b *CortexContextBuilder) BuildCortexSystemPrompt(
	ctx context.Context,
	userID string,
	recentMessages []map[string]interface{},
	activeDaemons []models.Daemon,
	projectInstruction string,
) (string, error) {
	var sb strings.Builder

	// 1. Base identity
	sb.WriteString("You are Cortex, Dobby's AI orchestrator. You analyze user requests and either respond directly (quick mode) or deploy specialized Daemons (sub-agents) for complex tasks.\n\n")

	// 2. Persona facts
	personaPrompt, err := b.personaService.BuildSystemPrompt(ctx, userID)
	if err == nil && personaPrompt != "" {
		sb.WriteString(personaPrompt)
		sb.WriteString("\n")
	}

	// 3. User memories (if memory selection service is available)
	if b.memorySelectionSvc != nil && len(recentMessages) > 0 {
		memories, err := b.memorySelectionSvc.SelectRelevantMemories(ctx, userID, recentMessages, 5)
		if err == nil && len(memories) > 0 {
			sb.WriteString("## Relevant User Memories\n\n")
			for _, m := range memories {
				sb.WriteString(fmt.Sprintf("- %s\n", m.DecryptedContent))
			}
			sb.WriteString("\n")
		}
	}

	// 4. Session context summary
	session, err := b.sessionStore.GetByUser(ctx, userID)
	if err == nil && session != nil && session.ContextSummary != "" {
		sb.WriteString("## Session Context\n\n")
		sb.WriteString(session.ContextSummary)
		sb.WriteString("\n\n")
	}

	// 5. Active daemon statuses
	if len(activeDaemons) > 0 {
		sb.WriteString("## Active Daemons\n\n")
		for _, d := range activeDaemons {
			sb.WriteString(fmt.Sprintf("- **%s** (%s): %s — %.0f%% complete",
				d.RoleLabel, d.Role, d.CurrentAction, d.Progress*100))
			if d.Status == models.DaemonStatusWaitingInput {
				sb.WriteString(" [WAITING FOR INPUT]")
			}
			sb.WriteString("\n")
		}
		sb.WriteString("\n")
	}

	// 6. Recent engram entries
	engrams, err := b.engramService.GetRecent(ctx, userID, 10)
	if err == nil && len(engrams) > 0 {
		sb.WriteString("## Recent Knowledge (Engram)\n\n")
		for _, e := range engrams {
			if e.Summary != "" {
				sb.WriteString(fmt.Sprintf("- [%s] %s\n", e.Type, e.Summary))
			}
		}
		sb.WriteString("\n")
	}

	// 7. Project-level instructions
	if projectInstruction != "" {
		sb.WriteString("## Project Instructions\n\n")
		sb.WriteString(projectInstruction)
		sb.WriteString("\n\n")
	}

	return sb.String(), nil
}

// MultiDaemonContext captures everything a daemon needs to know about its
// position in a larger orchestration. Empty/zero values mean this daemon
// is running solo (single-daemon mode); a non-empty value triggers the
// handoff instructions in the system prompt.
//
// The orchestrator owns this — daemons don't infer it. That keeps the
// "are we in multi-daemon?" decision in one place.
type MultiDaemonContext struct {
	// PlanIndex is this daemon's index in the plans slice (0-based).
	PlanIndex int
	// TotalDaemons is len(plans) — used for "daemon 1 of 3" framing.
	TotalDaemons int
	// HasDownstream is true if at least one other plan has this daemon's
	// index in its DependsOn. Tells this daemon "produce artifacts because
	// someone else will read them".
	HasDownstream bool
	// DownstreamRoles names the dependent daemons so the prompt can say
	// "the Writer daemon depends on your output — produce an artifact
	// it can read".
	DownstreamRoles []string
	// SuggestedArtifactName is what the orchestrator wants this daemon to
	// produce, derived from its role (e.g. "research-brief" for a
	// researcher). Empty if no preference — the daemon picks the name.
	SuggestedArtifactName string
}

// BuildDaemonSystemPrompt assembles the system prompt for a specific daemon.
//
// multi is non-nil only when this daemon is part of a multi-daemon
// orchestration. When set, the prompt explicitly teaches the daemon to
// use produce_artifact / read_artifact for cross-daemon handoff —
// without this instruction the model defaults to dumping output into
// its 4000-char text summary, which downstream daemons can't fully use.
func (b *CortexContextBuilder) BuildDaemonSystemPrompt(
	ctx context.Context,
	role string,
	roleLabel string,
	persona string,
	taskSummary string,
	dependencyResults map[string]string,
	projectInstruction string,
	skillIDs []primitive.ObjectID,
	multi *MultiDaemonContext,
) string {
	var sb strings.Builder

	// 1. Role persona
	sb.WriteString(fmt.Sprintf("You are a %s Daemon — %s\n\n", roleLabel, persona))

	// 2. Multi-daemon orchestration position. Comes early because it
	// shapes *how* the daemon should approach the task — produce
	// structured artifacts vs just summarising.
	if multi != nil {
		sb.WriteString("## Orchestration Context\n\n")
		sb.WriteString(fmt.Sprintf("You are daemon %d of %d in a multi-agent orchestration.\n",
			multi.PlanIndex+1, multi.TotalDaemons))
		if multi.HasDownstream {
			if len(multi.DownstreamRoles) > 0 {
				sb.WriteString(fmt.Sprintf("Downstream daemons that depend on your output: %s.\n",
					strings.Join(multi.DownstreamRoles, ", ")))
			} else {
				sb.WriteString("At least one downstream daemon depends on your output.\n")
			}
			suggested := multi.SuggestedArtifactName
			if suggested == "" {
				suggested = role + "-output"
			}
			// IMPORTANT — keep this prompt free of `<` and `>` chars. Go's json
			// encoder HTML-escapes them as < / >, and Bedrock's
			// /openai/v1 shim returns 400 ("Expecting value") when those
			// sequences appear in a message body. Verified live on
			// 2026-05-31: replacing literal <...> placeholders with square
			// brackets [...] unblocks the loop.
			sb.WriteString(fmt.Sprintf(
				"**IMPORTANT — Hand off your work via artifacts.** Do NOT rely on the text "+
					"summary alone (it's capped at about 4000 chars and loses structure). When you have "+
					"your main output ready, call:\n\n"+
					"  produce_artifact(name=\"%s\", content_type=\"markdown\", content=[your full output here], summary=[one-line description])\n\n"+
					"Downstream daemons will see the artifact in their catalogue and call "+
					"read_artifact(\"%s\") to pull the full body. Without this, they have to "+
					"guess from your summary and the orchestration loses its value.\n",
				suggested, suggested))
		} else {
			sb.WriteString("You are the final daemon in this chain. Your output goes directly to the user via synthesis.\n")
		}
		sb.WriteString("\n")
	}

	// 3. Task goal
	sb.WriteString("## Your Task\n\n")
	sb.WriteString(taskSummary)
	sb.WriteString("\n\n")

	// 4. Dependency results (from predecessor daemons)
	// Cap each to 4000 chars (head 2000 + tail 1500) to prevent system prompt bloat
	if len(dependencyResults) > 0 {
		sb.WriteString("## Previous Daemon Results\n\n")
		for label, result := range dependencyResults {
			capped := capDependencyResult(result, 4000)
			sb.WriteString(fmt.Sprintf("### %s\n%s\n\n", label, capped))
		}
		sb.WriteString("Use the results above to inform your work. " +
			"If their full output is available as an artifact (see catalogue below), " +
			"prefer read_artifact to get the complete data — these summaries are capped.\n\n")
	}

	// 5. Active skills — inject behavioral instructions from attached skills
	skillSection, _ := b.BuildSkillsSection(ctx, skillIDs)
	if skillSection != "" {
		sb.WriteString(skillSection)
		sb.WriteString("\n\n")
	}

	// 6. Project-level instructions
	if projectInstruction != "" {
		sb.WriteString("## Project Instructions\n\n")
		sb.WriteString(projectInstruction)
		sb.WriteString("\n\n")
	}

	// 7. Behavioral instructions
	sb.WriteString("## Instructions\n\n")
	sb.WriteString("- Use available tools to accomplish your task\n")
	sb.WriteString("- Be thorough but efficient — do not repeat work unnecessarily\n")
	sb.WriteString("- When your task is complete, provide a clear summary of what you accomplished\n")
	sb.WriteString("- If you need information you cannot obtain, state what's missing\n")
	sb.WriteString("- If you encounter errors, retry with a different approach before giving up\n")
	if multi != nil && multi.HasDownstream {
		sb.WriteString("- Before finishing: confirm you called produce_artifact with your main output\n")
	}
	// Auto-nudge for document-producing tasks. The classifier doesn't
	// currently set a "deliver_pdf" flag so we infer from the task text.
	// Cheap heuristic — false positives just give the model an unused tool.
	taskLower := strings.ToLower(taskSummary)
	if strings.Contains(taskLower, "pdf") || strings.Contains(taskLower, "report") ||
		strings.Contains(taskLower, "document") || strings.Contains(taskLower, "presentation") ||
		strings.Contains(taskLower, "deliverable") {
		sb.WriteString("- If your output is meant to be a downloadable file (PDF/report/document), " +
			"call html_to_pdf(html=<rendered HTML>, filename=<name>.pdf) and include the returned " +
			"file path in your summary so the user can download it\n")
	}

	return sb.String()
}

// BuildSkillsSection resolves skill IDs and builds the prompt section + required tools list.
// Returns (prompt section, required tool names).
func (b *CortexContextBuilder) BuildSkillsSection(ctx context.Context, skillIDs []primitive.ObjectID) (string, []string) {
	if b.skillService == nil || len(skillIDs) == 0 {
		return "", nil
	}

	var sections []string
	var requiredTools []string

	for _, id := range skillIDs {
		skill, err := b.skillService.GetSkill(ctx, id.Hex())
		if err != nil || skill == nil {
			continue
		}
		if skill.SystemPrompt != "" {
			sections = append(sections, fmt.Sprintf("### Skill: %s\n%s", skill.Name, skill.SystemPrompt))
		}
		requiredTools = append(requiredTools, skill.RequiredTools...)
	}

	if len(sections) == 0 {
		return "", requiredTools
	}

	return "## Active Skills\n\n" + strings.Join(sections, "\n\n"), requiredTools
}

// BuildClassificationPrompt returns the prompt used by Cortex to classify user requests.
// This is a STANDALONE system prompt — it should NOT be combined with the full Cortex context.
// It accepts activeDaemons so the classifier can detect status/continuation queries.
// It injects available daemon templates so the LLM can match requests to pre-configured daemons.
func (b *CortexContextBuilder) BuildClassificationPrompt(ctx context.Context, userID string, activeDaemons []models.Daemon) string {
	var sb strings.Builder

	sb.WriteString(`You are a task classifier. Your ONLY job is to classify the user's message and output JSON. Do NOT answer the user's question. Do NOT provide any explanation. Respond with ONLY a JSON object.

Classify into one of these modes:

STATUS: The user is asking about progress, status, or whether something is done. Use when daemons are active or the user references previous work.
  Examples: "is it done?", "what's the status?", "continue", "what happened?", "any updates?"

QUICK: Simple questions, greetings, lookups, conversational responses.
  Examples: "what time is it", "hello", "thanks", "what did I do today"

DAEMON: A SINGLE atomic task that one specialist can complete. ONE verb on ONE target.
  Examples: "research Q4 sales", "draft an email to John", "find flights to Tokyo"
  NOT examples: anything with "and then", anything that ends with "create a PDF" / "make a report"

MULTI_DAEMON: ANY task with two or more distinct verbs/outputs. **Default to this when there is even a hint of multiple steps** — it is much cheaper to spin up a 2-daemon plan that finishes well than to ask one daemon to do too much.

  Triggers that REQUIRE multi_daemon (do not classify these as daemon):
    - "research X and write/create/make Y"
    - "find X, then format/summarize/email"
    - "scrape/fetch/gather X and produce a report/PDF/slide deck/document"
    - "compare A and B and write up the differences"
    - "analyze X and create a presentation/PDF/email"
    - "X and Y" where X and Y are different verbs (research+write, analyze+notify, fetch+save, etc.)

  Common pattern — research-then-author:
    "research <topic> and create a PDF" →
      multi_daemon: [
        researcher (no deps) — gather the data, produce an artifact named "<topic>-research",
        writer (depends_on: [0]) — read the research artifact, produce the final document with html_to_pdf
      ]

  Common pattern — fetch-then-notify:
    "check today's calendar and send me a summary on Slack" →
      multi_daemon: [
        fetcher, notifier (depends_on: [0])
      ]

`)

	if len(activeDaemons) > 0 {
		sb.WriteString("CONTEXT: The following daemons are currently active:\n")
		for _, d := range activeDaemons {
			sb.WriteString(fmt.Sprintf("- %s (%s): %s — %.0f%% complete\n",
				d.RoleLabel, d.Role, d.CurrentAction, d.Progress*100))
		}
		sb.WriteString("If the user asks about progress or status, classify as STATUS.\n\n")
	}

	// Inject available daemon templates
	if b.templateStore != nil {
		templates, err := b.templateStore.GetForUser(ctx, userID)
		if err == nil && len(templates) > 0 {
			sb.WriteString("AVAILABLE DAEMON TEMPLATES:\n")
			sb.WriteString("If a template matches the user's request well, include its slug as \"template_slug\" in the daemon plan. The template's config (persona, tools, instructions) will be applied automatically.\n")
			sb.WriteString("If no template fits, omit template_slug and provide your own daemon config as usual.\n\n")
			for _, t := range templates {
				sb.WriteString(fmt.Sprintf("- slug: \"%s\" | %s — %s\n", t.Slug, t.Name, t.Description))
			}
			sb.WriteString("\n")
		}
	}

	sb.WriteString(`IMPORTANT TIE-BREAKING RULES:
- When in doubt between quick and daemon, choose DAEMON.
- When in doubt between daemon and multi_daemon, choose MULTI_DAEMON. (A 2-daemon plan that's overkill is better UX than a single daemon trying to do two things badly.)
- If the request mentions producing a file or document (PDF, report, slides, doc), it ALMOST CERTAINLY needs at least 2 daemons: one to gather/decide content, one to produce the file.
- If daemons are active and the user asks about progress, choose STATUS.

For status mode, respond with:
{"mode": "status"}

For quick mode, respond with:
{"mode": "quick"}

For daemon mode, respond with:
{"mode": "daemon", "daemons": [{"index": 0, "role": "researcher", "role_label": "Research Daemon", "template_slug": "researcher", "task_summary": "Research Q4 sales trends across major markets", "tools_needed": ["search"], "depends_on": []}]}

For multi_daemon mode, respond with:
{"mode": "multi_daemon", "daemons": [{"index": 0, "role": "researcher", "role_label": "Research Daemon", "template_slug": "researcher", "task_summary": "Research competitor landscape", "tools_needed": ["search"], "depends_on": []}, {"index": 1, "role": "writer", "role_label": "Writer Daemon", "template_slug": "writer", "task_summary": "Write analysis report using research results", "tools_needed": ["search"], "depends_on": [0]}]}

When a template_slug is provided, you can omit "persona" — the template's persona/instructions will be used.
When no template matches, provide "persona" as before and omit "template_slug".

Roles: researcher, coder, writer, analyst, browser, creator, organizer
Tool categories: search, file, communication, code, data

Respond with ONLY valid JSON. No markdown, no explanation, no code blocks.`)

	return sb.String()
}

// capDependencyResult truncates a dependency result to maxChars using head + tail.
func capDependencyResult(result string, maxChars int) string {
	if len(result) <= maxChars {
		return result
	}
	headSize := maxChars / 2
	tailSize := maxChars * 3 / 8 // 37.5%
	omitted := len(result) - headSize - tailSize
	return result[:headSize] +
		fmt.Sprintf("\n\n... [%d chars omitted from dependency result] ...\n\n", omitted) +
		result[len(result)-tailSize:]
}
