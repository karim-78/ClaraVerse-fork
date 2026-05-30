package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"strings"
)

// NexusArtifactAccessKey is the args-map key that chat/daemon services use
// to hand these tools a concrete store. Kept here so the tools package
// doesn't import services (cycle).
const NexusArtifactAccessKey = "__nexus_artifact_access__"

// NexusArtifactAccess is the contract the cortex service satisfies. Models
// see three explicit verbs; the interface mirrors them. Keeping the
// session_id implicit (pulled from the daemon's context) prevents one
// daemon from accidentally reading another session's data.
type NexusArtifactAccess interface {
	Produce(ctx context.Context, name, contentType, content, summary string) (ProducedArtifact, error)
	List(ctx context.Context) ([]ListedArtifact, error)
	Read(ctx context.Context, name string) (*FetchedArtifact, error)
}

type ProducedArtifact struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	ContentType string `json:"content_type"`
	SizeBytes   int    `json:"size_bytes"`
}

type ListedArtifact struct {
	Name        string `json:"name"`
	ContentType string `json:"content_type"`
	Summary     string `json:"summary,omitempty"`
	SizeBytes   int    `json:"size_bytes"`
}

type FetchedArtifact struct {
	Name        string `json:"name"`
	ContentType string `json:"content_type"`
	Content     string `json:"content"`
	Summary     string `json:"summary,omitempty"`
	SizeBytes   int    `json:"size_bytes"`
}

// ─── produce_artifact ─────────────────────────────────────────────────

func NewProduceArtifactTool() *Tool {
	return &Tool{
		Name:        "produce_artifact",
		DisplayName: "Produce Artifact",
		Description: `Save structured output (a draft, JSON object, CSV, code, URL, file reference) into the shared session store under a name. Downstream daemons in this Nexus orchestration can call read_artifact(name) to pull the full content. Use this instead of stuffing large output into your final summary — your summary should describe what you produced, not contain it.

When to use:
- You produced data the next daemon will need (research notes, draft text, parsed JSON, scraped table, generated code)
- The content is larger than 500 chars or has structure you want preserved
- The next daemon will likely re-read or transform it

When NOT to use:
- A one-line answer that fits naturally in your summary
- Already covered by an existing artifact (read it, refine, re-produce with same name to overwrite)
- Sensitive data the user wouldn't want stored beyond this session

Names: use kebab-case ("research-brief", "scraped-prices"). Re-producing the same name overwrites — that's intended for "refine the draft" pipelines.

Content types: "text" | "markdown" | "json" | "csv" | "code" | "url" | "html". Helps the consuming daemon know how to parse.

Max content size: 1 MiB. For larger artifacts (full files), store the file via the file tools and put the file id in a JSON artifact instead.`,
		Icon:     "Package",
		Source:   ToolSourceBuiltin,
		Category: "orchestration",
		Keywords: []string{"artifact", "produce", "save", "share", "nexus", "handoff"},
		Parameters: map[string]interface{}{
			"type":     "object",
			"required": []string{"name", "content"},
			"properties": map[string]interface{}{
				"name": map[string]interface{}{
					"type":        "string",
					"description": "Stable kebab-case name. Re-producing the same name overwrites.",
				},
				"content_type": map[string]interface{}{
					"type":        "string",
					"enum":        []string{"text", "markdown", "json", "csv", "code", "url", "html"},
					"description": "How downstream daemons should parse it. Default 'text'.",
				},
				"content": map[string]interface{}{
					"type":        "string",
					"description": "The full content. Max 1 MiB.",
				},
				"summary": map[string]interface{}{
					"type":        "string",
					"description": "Optional one-sentence description shown to downstream daemons before they decide to read.",
				},
			},
		},
		Execute: executeProduceArtifact,
	}
}

func executeProduceArtifact(args map[string]interface{}) (string, error) {
	access, ok := args[NexusArtifactAccessKey].(NexusArtifactAccess)
	if !ok || access == nil {
		return "", fmt.Errorf("produce_artifact is only available inside a Nexus daemon")
	}
	name, _ := args["name"].(string)
	if strings.TrimSpace(name) == "" {
		return "", fmt.Errorf("name is required")
	}
	contentType, _ := args["content_type"].(string)
	if contentType == "" {
		contentType = "text"
	}
	content, _ := args["content"].(string)
	if content == "" {
		return "", fmt.Errorf("content is required")
	}
	summary, _ := args["summary"].(string)

	res, err := access.Produce(context.Background(), name, contentType, content, summary)
	if err != nil {
		return "", err
	}
	log.Printf("📦 [TOOL produce_artifact] name=%q type=%s size=%d", res.Name, res.ContentType, res.SizeBytes)
	out, _ := json.Marshal(map[string]interface{}{
		"ok":           true,
		"id":           res.ID,
		"name":         res.Name,
		"content_type": res.ContentType,
		"size_bytes":   res.SizeBytes,
	})
	return string(out), nil
}

// ─── list_artifacts ───────────────────────────────────────────────────

func NewListArtifactsTool() *Tool {
	return &Tool{
		Name:        "list_artifacts",
		DisplayName: "List Artifacts",
		Description: `List every artifact stored in this Nexus session — yours plus every predecessor daemon's. Returns names, content types, and short summaries so you can decide which (if any) to pull with read_artifact.

Use this when:
- Your system prompt lists "Predecessor artifacts available" and you want to see the full set including ones produced by sibling branches
- You're not sure whether an artifact exists yet`,
		Icon:     "List",
		Source:   ToolSourceBuiltin,
		Category: "orchestration",
		Keywords: []string{"artifact", "list", "nexus"},
		Parameters: map[string]interface{}{
			"type":       "object",
			"properties": map[string]interface{}{},
		},
		Execute: executeListArtifacts,
	}
}

func executeListArtifacts(args map[string]interface{}) (string, error) {
	access, ok := args[NexusArtifactAccessKey].(NexusArtifactAccess)
	if !ok || access == nil {
		return "", fmt.Errorf("list_artifacts is only available inside a Nexus daemon")
	}
	items, err := access.List(context.Background())
	if err != nil {
		return "", err
	}
	out, _ := json.Marshal(map[string]interface{}{
		"count":     len(items),
		"artifacts": items,
	})
	return string(out), nil
}

// ─── read_artifact ────────────────────────────────────────────────────

func NewReadArtifactTool() *Tool {
	return &Tool{
		Name:        "read_artifact",
		DisplayName: "Read Artifact",
		Description: `Fetch the full content of a previously-produced artifact by name. Use this to load structured data a predecessor daemon stored (research brief, draft, JSON object, CSV) rather than re-deriving it from a short summary.

If the artifact doesn't exist, you'll get an error — call list_artifacts first to confirm what's available.`,
		Icon:     "FileText",
		Source:   ToolSourceBuiltin,
		Category: "orchestration",
		Keywords: []string{"artifact", "read", "fetch", "nexus"},
		Parameters: map[string]interface{}{
			"type":     "object",
			"required": []string{"name"},
			"properties": map[string]interface{}{
				"name": map[string]interface{}{
					"type":        "string",
					"description": "Exact artifact name. Case-sensitive.",
				},
			},
		},
		Execute: executeReadArtifact,
	}
}

func executeReadArtifact(args map[string]interface{}) (string, error) {
	access, ok := args[NexusArtifactAccessKey].(NexusArtifactAccess)
	if !ok || access == nil {
		return "", fmt.Errorf("read_artifact is only available inside a Nexus daemon")
	}
	name, _ := args["name"].(string)
	if strings.TrimSpace(name) == "" {
		return "", fmt.Errorf("name is required")
	}
	got, err := access.Read(context.Background(), name)
	if err != nil {
		return "", err
	}
	if got == nil {
		return "", fmt.Errorf("artifact %q not found", name)
	}
	out, _ := json.Marshal(got)
	return string(out), nil
}
