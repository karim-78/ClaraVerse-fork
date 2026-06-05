package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"strings"
	"time"

	"claraverse/internal/e2b"
	"claraverse/internal/filecache"
)

// mountFilesIntoSandbox pushes the user's uploaded files into the pooled
// sandbox at /data/<original-filename>, so code like
// migi_load('/data/report.xlsx') works (the run_python description promises
// files live there). It mounts (a) every file cached for this conversation
// and (b) any explicitly-passed fileIDs — the latter is the durable path that
// still works after the 30-min in-memory cache expires, since the bytes are
// re-read from disk. Best-effort: logs failures, never blocks execution.
func mountFilesIntoSandbox(conversationID string, fileIDs []string) {
	if conversationID == "" {
		return
	}
	svc := e2b.GetE2BExecutorService()
	if svc == nil {
		return
	}
	fc := filecache.GetService()

	// name resolution: fileID -> best display filename (cached original name
	// when available, else the on-disk <fileID><ext> name).
	names := map[string]string{} // fileID -> target filename
	if fc != nil {
		for _, f := range fc.GetFilesForConversation(conversationID) {
			names[f.FileID] = f.Filename
			fc.ExtendTTL(f.FileID, time.Hour) // keep alive during active analysis
		}
	}
	for _, id := range fileIDs {
		if id == "" {
			continue
		}
		if _, seen := names[id]; !seen {
			if fc != nil {
				if cf, ok := fc.Get(id); ok {
					names[id] = cf.Filename
					continue
				}
			}
			names[id] = "" // fall back to disk name below
		}
	}

	for fileID, name := range names {
		content, diskName, err := GetUploadedFile(fileID)
		if err != nil || len(content) == 0 {
			continue // not a disk-backed data file (e.g. image/PDF) — skip
		}
		if name == "" {
			name = diskName
		}
		target := "/data/" + name
		if err := svc.PushFile(context.Background(), conversationID, target, content); err != nil {
			log.Printf("⚠️ [PYTHON] Failed to mount %s into sandbox: %v", name, err)
		} else {
			log.Printf("📁 [PYTHON] Mounted %s -> %s (%d bytes)", name, target, len(content))
		}
	}
}

// NewPythonRunnerTool creates a new Python Code Runner tool
func NewPythonRunnerTool() *Tool {
	return &Tool{
		Name:        "run_python",
		DisplayName: "Python Code Runner",
		Description: `Execute Python code in a persistent sandbox scoped to this conversation. Variables, imports, and DataFrames survive between calls — treat it like a Jupyter session.

Files uploaded by the user are mounted at /data/<filename>. ALWAYS load them with the injected migi_load() helper instead of pd.read_csv/read_excel — it auto-handles Excel multi-sheet selection and CSV encoding/delimiter detection:
  df = migi_load('/data/sales.csv')                     # CSV/TSV — encoding + delimiter auto-detected
  df = migi_load('/data/report.xlsx')                   # Excel — auto-picks the richest sheet and prints the sheet list
  df = migi_load('/data/report.xlsx', sheet='Summary')  # Excel — a specific sheet by name
  sheets = migi_load_all('/data/report.xlsx')           # dict {sheet_name: DataFrame} for whole-workbook analysis
If a path is wrong, call _migi_list_data() to see exactly what's in /data.

DATAFRAME RENDERING: when you want to show a pandas DataFrame to the user as an interactive table (with sort/filter/paginate), call display_df(df) — do not just print(df) or end the cell with df. display_df is auto-injected; you can also pass a name: display_df(df, name='Sales by region').

CHARTS: matplotlib / seaborn / plotly figures are captured automatically and rendered inline — just plt.show() at the end. No need for savefig.

PIP: pass new packages in the 'dependencies' array on first use; they install before your code runs.

OUTPUTS: any file you write to /home/user/ (or pass in 'output_files') is collected and made downloadable.

DO use this for:
- Loading + analyzing user-attached CSV/Excel/Parquet files
- Multi-step exploration ("load data" → "summarize" → "plot trends")
- ML training, statistical tests, scraping
- Generating CSVs, models, plots, PDFs for the user

DON'T use it for:
- Trivial maths (just compute in text)
- Live API queries that the search_web / scrape_web tools already do better` + ChartStyleGuide + DataColumnsGuide,
		Icon:     "Terminal",
		Source:   ToolSourceBuiltin,
		Category: "computation",
		Keywords: []string{"python", "code", "execute", "run", "script", "programming", "processing", "compute", "pip", "packages", "dependencies"},
		Parameters: map[string]interface{}{
			"type": "object",
			"properties": map[string]interface{}{
				"code": map[string]interface{}{
					"type":        "string",
					"description": "Python code to execute",
				},
				"dependencies": map[string]interface{}{
					"type":        "array",
					"description": "Pip packages to install before execution (e.g., ['torch', 'transformers', 'requests'])",
					"items": map[string]interface{}{
						"type": "string",
					},
				},
				"output_files": map[string]interface{}{
					"type":        "array",
					"description": "File paths to retrieve after execution (e.g., ['model.pt', 'output.csv'])",
					"items": map[string]interface{}{
						"type": "string",
					},
				},
				"file_ids": map[string]interface{}{
					"type":        "array",
					"description": "Optional: upload IDs of files to ensure are mounted at /data before running. Conversation files are auto-mounted; pass IDs explicitly if a file was uploaded earlier in a long session.",
					"items": map[string]interface{}{
						"type": "string",
					},
				},
			},
			"required": []string{"code"},
		},
		Execute: executePythonRunner,
	}
}

// sandboxBootstrap is prepended to every run_python call. It defines
// `display_df(df, name='')` which emits a marker-wrapped JSON envelope to
// stdout that the backend extracts and converts into a renderable DataFrame
// artifact. Re-defining the function each call is cheap and idempotent —
// safer than relying on the IPython kernel state surviving between calls
// (which it does in pooled sandboxes, but new ones still need the func).
const sandboxBootstrap = `
import json as _dobby_json, sys as _dobby_sys
def display_df(df, name=''):
    """Show a pandas DataFrame as an interactive table artifact in chat."""
    try:
        import pandas as _pd
        if not isinstance(df, _pd.DataFrame):
            print(df)
            return
        preview_rows = min(50, len(df))
        head = df.head(preview_rows)
        payload = {
            'name': str(name) or '',
            'headers': [str(c) for c in df.columns.tolist()],
            'rows': head.astype(str).values.tolist(),
            'row_count': int(len(df)),
            'col_count': int(len(df.columns)),
        }
        print('<<DOBBY_DF>>' + _dobby_json.dumps(payload) + '<</DOBBY_DF>>')
    except Exception as _e:
        print('[display_df error: ' + str(_e) + ']', file=_dobby_sys.stderr)
`

func executePythonRunner(args map[string]interface{}) (string, error) {
	// Extract code (required)
	userCode, ok := args["code"].(string)
	if !ok || userCode == "" {
		return "", fmt.Errorf("code is required")
	}
	code := DataLoaderBootstrap + "\n" + sandboxBootstrap + "\n" + userCode

	// Extract dependencies (optional)
	var dependencies []string
	if depsRaw, ok := args["dependencies"].([]interface{}); ok {
		for _, dep := range depsRaw {
			if depStr, ok := dep.(string); ok {
				dependencies = append(dependencies, depStr)
			}
		}
	}

	// Extract output files (optional)
	var outputFiles []string
	if filesRaw, ok := args["output_files"].([]interface{}); ok {
		for _, file := range filesRaw {
			if fileStr, ok := file.(string); ok {
				outputFiles = append(outputFiles, fileStr)
			}
		}
	}

	// Propagate the conversation_id so the e2b-service pools the sandbox
	// across turns — dataframes, imports, and globals survive turn-to-turn
	// instead of being torn down with each call.
	conversationID, _ := args["__conversation_id__"].(string)

	// Mount the user's uploaded files into the sandbox at /data/<filename>
	// before running, so code that reads them by path works.
	var fileIDs []string
	if idsRaw, ok := args["file_ids"].([]interface{}); ok {
		for _, id := range idsRaw {
			if idStr, ok := id.(string); ok {
				fileIDs = append(fileIDs, idStr)
			}
		}
	}
	mountFilesIntoSandbox(conversationID, fileIDs)

	req := e2b.AdvancedExecuteRequest{
		Code:           code,
		Timeout:        300, // 5 minutes for complex tasks
		Dependencies:   dependencies,
		OutputFiles:    outputFiles,
		ConversationID: conversationID,
	}

	// Execute
	e2bService := e2b.GetE2BExecutorService()
	result, err := e2bService.ExecuteAdvanced(context.Background(), req)
	if err != nil {
		return "", fmt.Errorf("failed to execute code: %w", err)
	}

	if result.SandboxReused {
		log.Printf("🐍 [PYTHON] Reused sandbox %s for conv %s (persistent state ✓)", result.SandboxID, conversationID)
	} else if result.SandboxID != "" {
		log.Printf("🐍 [PYTHON] Fresh sandbox %s for conv %s", result.SandboxID, conversationID)
	}

	if !result.Success {
		errorMsg := "execution failed"
		if result.Error != nil {
			errorMsg = *result.Error
		}
		if result.Stderr != "" {
			errorMsg += "\nStderr: " + result.Stderr
		}
		// Surface stdout (incl. any [schema] columns/dtypes migi_load printed)
		// so the model corrects wrong column names with the real ones.
		if s := firstChars(result.Stdout, 1800); strings.TrimSpace(s) != "" {
			errorMsg += "\n\n📋 Output before failure (use these EXACT column names; col(df,'name') resolves fuzzy names):\n" + s
		}
		return "", fmt.Errorf("%s", errorMsg)
	}

	// Build response
	response := map[string]interface{}{
		"success": true,
		"stdout":  result.Stdout,
	}

	// Include stderr if present
	if result.Stderr != "" {
		response["stderr"] = result.Stderr
	}

	// Include install output if dependencies were installed
	if result.InstallOutput != "" {
		response["install_output"] = result.InstallOutput
	}

	// Include plots if any
	if len(result.Plots) > 0 {
		response["plots"] = result.Plots
		response["plot_count"] = len(result.Plots)
	}

	// Include files if any were retrieved
	if len(result.Files) > 0 {
		response["files"] = result.Files
		response["file_count"] = len(result.Files)
	}

	// Include execution time
	if result.ExecutionTime != nil {
		response["execution_time"] = *result.ExecutionTime
	}

	jsonResponse, _ := json.MarshalIndent(response, "", "  ")
	return string(jsonResponse), nil
}
