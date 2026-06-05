package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"regexp"
	"strings"

	"claraverse/internal/e2b"
)

// migiLoadCall builds a call to the injected migi_load() helper (see
// DataLoaderBootstrap), which robustly loads CSV/Excel/JSON/Parquet — handling
// multi-sheet workbooks (auto-picks the richest sheet, or a named one) and CSV
// encoding/delimiter detection. Replaces the old hardcoded pd.read_csv() which
// blew up with UnicodeDecodeError on .xlsx files.
func migiLoadCall(filename, sheet string) string {
	if sheet != "" {
		return fmt.Sprintf("migi_load(%q, sheet=%q)", filename, sheet)
	}
	return fmt.Sprintf("migi_load(%q)", filename)
}

// stripDataLoadingCalls removes pd.read_csv(), pd.read_excel(), pd.read_json() calls from user code
// This prevents LLMs from trying to load files by filename (which don't exist in the sandbox)
func stripDataLoadingCalls(code string) string {
	// Patterns to match various forms of data loading
	patterns := []string{
		// Match: df = pd.read_csv('filename.csv') or similar with double quotes
		`(?m)^\s*\w+\s*=\s*pd\.read_csv\s*\([^)]+\)\s*$`,
		`(?m)^\s*\w+\s*=\s*pd\.read_excel\s*\([^)]+\)\s*$`,
		`(?m)^\s*\w+\s*=\s*pd\.read_json\s*\([^)]+\)\s*$`,
		`(?m)^\s*\w+\s*=\s*pd\.read_table\s*\([^)]+\)\s*$`,
		// Match inline read calls (not assigned)
		`(?m)^\s*pd\.read_csv\s*\([^)]+\)\s*$`,
		`(?m)^\s*pd\.read_excel\s*\([^)]+\)\s*$`,
		`(?m)^\s*pd\.read_json\s*\([^)]+\)\s*$`,
		// Match with pandas prefix
		`(?m)^\s*\w+\s*=\s*pandas\.read_csv\s*\([^)]+\)\s*$`,
		`(?m)^\s*\w+\s*=\s*pandas\.read_excel\s*\([^)]+\)\s*$`,
	}

	result := code
	stripped := false
	for _, pattern := range patterns {
		re := regexp.MustCompile(pattern)
		if re.MatchString(result) {
			stripped = true
			result = re.ReplaceAllString(result, "# [AUTO-REMOVED: Data is pre-loaded as 'df']")
		}
	}

	if stripped {
		log.Printf("🔧 [DATA-ANALYST] Stripped data loading calls from user code")
	}

	return result
}

// NewDataAnalystTool creates a new AI Data Analyst tool
func NewDataAnalystTool() *Tool {
	return &Tool{
		Name:        "analyze_data",
		DisplayName: "AI Data Analyst",
		Description: `Analyze data with Python. Full access to pandas, numpy, matplotlib, and seaborn.

⚠️ IMPORTANT: Data is AUTOMATICALLY loaded as 'df' (pandas DataFrame).
DO NOT use pd.read_csv(), pd.read_excel(), or pd.read_json() - it will fail!
Just use 'df' directly in your code.

Example usage:
- df.head() - view data
- df.describe() - statistics
- sns.barplot(data=df, x='category', y='sales') - bar chart
- df.plot() - line plot

Chart types you can create:
- Bar: sns.barplot(data=df, x='col1', y='col2')
- Pie: plt.pie(df.groupby('cat')['val'].sum(), labels=..., autopct='%1.1f%%')
- Scatter: sns.scatterplot(data=df, x='col1', y='col2', hue='col3')
- Heatmap: sns.heatmap(df.corr(), annot=True, cmap='coolwarm')
- Histogram: sns.histplot(df['col'], bins=30)
- Box: sns.boxplot(data=df, x='category', y='value')

Always use plt.show() after each plot. Add titles and labels for clarity.` + ChartStyleGuide + DataColumnsGuide,
		Icon:     "ChartBar",
		Source:   ToolSourceBuiltin,
		Category: "computation",
		Keywords: []string{"analyze", "data", "python", "pandas", "visualization", "chart", "graph", "statistics", "csv", "dataframe", "plot", "analytics"},
		Parameters: map[string]interface{}{
			"type": "object",
			"properties": map[string]interface{}{
				"file_id": map[string]interface{}{
					"type":        "string",
					"description": "Upload ID of the CSV/Excel file to analyze (from file upload). Use this for uploaded files.",
				},
				"csv_data": map[string]interface{}{
					"type":        "string",
					"description": "CSV data as a string (with headers). Use this for small inline data. Either file_id or csv_data is required.",
				},
				"python_code": map[string]interface{}{
					"type": "string",
					"description": `Custom Python code for visualization.

⚠️ CRITICAL: 'df' is ALREADY LOADED as a pandas DataFrame!
DO NOT use pd.read_csv(), pd.read_excel(), or pd.read_json()!
The file is NOT accessible by filename in the sandbox.

Just write code that uses 'df' directly:
- sns.barplot(data=df, x='category', y='sales')
- plt.pie(df.groupby('cat')['val'].sum(), labels=df['cat'].unique())
- sns.heatmap(df.corr(), annot=True)

Always end with plt.show()`,
				},
				"analysis_type": map[string]interface{}{
					"type":        "string",
					"description": "Predefined analysis type (used only if python_code is not provided)",
					"enum":        []string{"summary", "correlation", "trend", "distribution", "outliers", "full"},
					"default":     "summary",
				},
				"columns": map[string]interface{}{
					"type":        "array",
					"description": "Optional: Specific columns to analyze (if empty, analyzes all columns)",
					"items": map[string]interface{}{
						"type": "string",
					},
				},
				"sheet_name": map[string]interface{}{
					"type":        "string",
					"description": "Optional (Excel only): which sheet to load. If omitted on a multi-sheet workbook, the richest sheet is auto-selected. Call read_spreadsheet first to see sheet names.",
				},
			},
		},
		Execute: executeDataAnalyst,
	}
}

func executeDataAnalyst(args map[string]interface{}) (string, error) {
	var csvData []byte
	var filename string

	// Try file_id first (uploaded files)
	if fileID, ok := args["file_id"].(string); ok && fileID != "" {
		content, name, err := GetUploadedFile(fileID)
		if err != nil {
			return "", fmt.Errorf("failed to get uploaded file: %w", err)
		}
		csvData = content
		filename = name
	} else if csvDataStr, ok := args["csv_data"].(string); ok && csvDataStr != "" {
		// Fallback to direct CSV data for small inline data
		csvData = []byte(csvDataStr)
		filename = "data.csv"
	} else {
		return "", fmt.Errorf("either file_id or csv_data is required")
	}

	// Create file map with CSV data
	files := map[string][]byte{
		filename: csvData,
	}

	// Optional sheet selection for multi-sheet Excel workbooks.
	sheet := ""
	if sn, ok := args["sheet_name"].(string); ok {
		sheet = sn
	}
	loadCall := migiLoadCall(filename, sheet)

	var pythonCode string

	// Check for custom python_code first (LLM-generated visualizations)
	if customCode, ok := args["python_code"].(string); ok && customCode != "" {
		// Strip any pd.read_* calls - the LLM shouldn't load files, we pre-load them
		cleanedCode := stripDataLoadingCalls(customCode)

		// Use LLM-generated code with pre-loaded data
		pythonCode = fmt.Sprintf(`import pandas as pd
import numpy as np
import matplotlib.pyplot as plt
import seaborn as sns

# Apply Migi chart style (crisp 150-DPI, accent palette, despined)
migi_chart_style()

# Load data
df = %s

print("=" * 80)
print("DATA ANALYSIS")
print("=" * 80)
print(f"\nDataset: %s")
print(f"Shape: {df.shape[0]} rows × {df.shape[1]} columns")
print(f"Columns: {list(df.columns)}")
print()

# Execute custom analysis code
%s

print("\n" + "=" * 80)
print("✅ ANALYSIS COMPLETE")
print("=" * 80)
`, loadCall, filename, cleanedCode)
	} else {
		// Fallback to predefined analysis types
		analysisType := "summary"
		if at, ok := args["analysis_type"].(string); ok {
			analysisType = at
		}

		var columns []string
		if colsRaw, ok := args["columns"].([]interface{}); ok {
			for _, col := range colsRaw {
				columns = append(columns, fmt.Sprintf("%v", col))
			}
		}

		pythonCode = generateDataAnalysisCode(loadCall, filename, analysisType, columns)
	}

	// Inject the robust loader so migi_load() is defined in the sandbox.
	pythonCode = DataLoaderBootstrap + "\n" + pythonCode

	// Execute code with longer timeout for custom visualizations
	e2bService := e2b.GetE2BExecutorService()
	result, err := e2bService.ExecuteWithFiles(context.Background(), pythonCode, files, 120)
	if err != nil {
		return "", fmt.Errorf("failed to execute analysis: %w", err)
	}

	if !result.Success {
		errorMsg := result.Stderr
		if result.Error != nil {
			errorMsg = *result.Error
		}

		// Surface the actual schema (migi_load prints [schema] columns/dtypes at
		// the top of stdout) so the model fixes wrong/guessed column names with
		// the REAL ones instead of looping.
		schemaHint := ""
		if s := firstChars(result.Stdout, 1800); strings.TrimSpace(s) != "" {
			schemaHint = "\n\n📋 Use the EXACT column names from the loaded data (and col(df,'name') if unsure):\n" + s
		}

		// Check for FileNotFoundError and provide helpful message
		if strings.Contains(errorMsg, "FileNotFoundError") || strings.Contains(errorMsg, "No such file or directory") {
			return "", fmt.Errorf(`analysis failed: %s

💡 HINT: The data is already pre-loaded as 'df' (pandas DataFrame).
Do NOT use pd.read_csv(), pd.read_excel(), or pd.read_json() - those files don't exist in the sandbox!
Just use 'df' directly in your code. Example: sns.barplot(data=df, x='category', y='sales')%s`, errorMsg, schemaHint)
		}

		return "", fmt.Errorf("analysis failed: %s%s", errorMsg, schemaHint)
	}

	// Format response
	response := map[string]interface{}{
		"success":    true,
		"analysis":   result.Stdout,
		"plots":      result.Plots,
		"plot_count": len(result.Plots),
		"filename":   filename,
	}

	jsonResponse, _ := json.MarshalIndent(response, "", "  ")
	return string(jsonResponse), nil
}

func generateDataAnalysisCode(loadCall, displayName string, analysisType string, columns []string) string {
	// Column filter
	colFilter := ""
	if len(columns) > 0 {
		colsStr := "'" + strings.Join(columns, "', '") + "'"
		colFilter = fmt.Sprintf("\ndf = df[[%s]]", colsStr)
	}

	// Base code
	code := fmt.Sprintf(`import pandas as pd
import matplotlib.pyplot as plt
import numpy as np
import seaborn as sns

# Apply Migi chart style (crisp 150-DPI, accent palette, despined)
migi_chart_style()

# Load data
df = %s%s

print("=" * 80)
print("DATA ANALYSIS REPORT")
print("=" * 80)
print(f"\nDataset: %s")
print(f"Shape: {df.shape[0]} rows × {df.shape[1]} columns")
print()
`, loadCall, colFilter, displayName)

	switch analysisType {
	case "summary":
		code += `
# Summary Statistics
print("\n📊 SUMMARY STATISTICS")
print("-" * 80)
print(df.describe())

# Data types
print("\n📋 DATA TYPES")
print("-" * 80)
print(df.dtypes)

# Missing values
print("\n⚠️  MISSING VALUES")
print("-" * 80)
missing = df.isnull().sum()
if missing.sum() > 0:
    print(missing[missing > 0])
else:
    print("No missing values!")
`

	case "correlation":
		code += `
# Correlation Analysis
print("\n🔗 CORRELATION ANALYSIS")
print("-" * 80)

numeric_cols = df.select_dtypes(include=[np.number]).columns
if len(numeric_cols) > 1:
    corr = df[numeric_cols].corr()
    print(corr)

    # Correlation heatmap
    plt.figure(figsize=(10, 8))
    sns.heatmap(corr, annot=True, cmap='coolwarm', center=0, fmt='.2f')
    plt.title('Correlation Matrix')
    plt.tight_layout()
    plt.show()
else:
    print("Not enough numeric columns for correlation analysis")
`

	case "trend":
		code += `
# Trend Analysis
print("\n📈 TREND ANALYSIS")
print("-" * 80)

numeric_cols = df.select_dtypes(include=[np.number]).columns
if len(numeric_cols) > 0:
    # Line plot for numeric columns
    fig, axes = plt.subplots(len(numeric_cols), 1, figsize=(12, 4 * len(numeric_cols)))
    if len(numeric_cols) == 1:
        axes = [axes]

    for ax, col in zip(axes, numeric_cols):
        df[col].plot(ax=ax, linewidth=2)
        ax.set_title(f'{col} - Trend Over Time')
        ax.set_xlabel('Index')
        ax.set_ylabel(col)
        ax.grid(True, alpha=0.3)

    plt.tight_layout()
    plt.show()

    print(f"Analyzed trends for {len(numeric_cols)} numeric columns")
else:
    print("No numeric columns for trend analysis")
`

	case "distribution":
		code += `
# Distribution Analysis
print("\n📊 DISTRIBUTION ANALYSIS")
print("-" * 80)

numeric_cols = df.select_dtypes(include=[np.number]).columns
if len(numeric_cols) > 0:
    # Create subplots
    n_cols = min(3, len(numeric_cols))
    n_rows = (len(numeric_cols) + n_cols - 1) // n_cols

    fig, axes = plt.subplots(n_rows, n_cols, figsize=(5 * n_cols, 4 * n_rows))
    if len(numeric_cols) == 1:
        axes = [axes]
    else:
        axes = axes.flatten() if n_rows > 1 else axes

    for ax, col in zip(axes, numeric_cols):
        df[col].hist(ax=ax, bins=30, edgecolor='black', alpha=0.7)
        ax.set_title(f'{col} Distribution')
        ax.set_xlabel(col)
        ax.set_ylabel('Frequency')
        ax.grid(True, alpha=0.3)

    # Hide empty subplots
    for i in range(len(numeric_cols), len(axes)):
        axes[i].set_visible(False)

    plt.tight_layout()
    plt.show()

    # Print statistics
    for col in numeric_cols:
        print(f"\n{col}:")
        print(f"  Mean: {df[col].mean():.2f}")
        print(f"  Median: {df[col].median():.2f}")
        print(f"  Std Dev: {df[col].std():.2f}")
        print(f"  Min: {df[col].min():.2f}, Max: {df[col].max():.2f}")
else:
    print("No numeric columns for distribution analysis")
`

	case "outliers":
		code += `
# Outlier Detection
print("\n🚨 OUTLIER DETECTION")
print("-" * 80)

numeric_cols = df.select_dtypes(include=[np.number]).columns
if len(numeric_cols) > 0:
    for col in numeric_cols:
        Q1 = df[col].quantile(0.25)
        Q3 = df[col].quantile(0.75)
        IQR = Q3 - Q1
        lower_bound = Q1 - 1.5 * IQR
        upper_bound = Q3 + 1.5 * IQR

        outliers = df[(df[col] < lower_bound) | (df[col] > upper_bound)]

        print(f"\n{col}:")
        print(f"  Lower bound: {lower_bound:.2f}")
        print(f"  Upper bound: {upper_bound:.2f}")
        print(f"  Outliers found: {len(outliers)}")

        if len(outliers) > 0:
            print(f"  Outlier values: {sorted(outliers[col].unique())[:10]}")

    # Box plot
    fig, axes = plt.subplots(len(numeric_cols), 1, figsize=(10, 3 * len(numeric_cols)))
    if len(numeric_cols) == 1:
        axes = [axes]

    for ax, col in zip(axes, numeric_cols):
        df.boxplot(column=col, ax=ax, vert=False)
        ax.set_title(f'{col} - Box Plot (Outlier Detection)')
        ax.grid(True, alpha=0.3)

    plt.tight_layout()
    plt.show()
else:
    print("No numeric columns for outlier detection")
`

	case "full":
		code += `
# Full Analysis
print("\n📊 SUMMARY STATISTICS")
print("-" * 80)
print(df.describe())

print("\n📋 DATA TYPES")
print("-" * 80)
print(df.dtypes)

print("\n⚠️  MISSING VALUES")
print("-" * 80)
missing = df.isnull().sum()
if missing.sum() > 0:
    print(missing[missing > 0])
else:
    print("✅ No missing values!")

numeric_cols = df.select_dtypes(include=[np.number]).columns

if len(numeric_cols) > 1:
    # Correlation heatmap
    print("\n🔗 CORRELATION ANALYSIS")
    print("-" * 80)
    corr = df[numeric_cols].corr()
    print(corr)

    plt.figure(figsize=(10, 8))
    sns.heatmap(corr, annot=True, cmap='coolwarm', center=0, fmt='.2f',
                square=True, linewidths=0.5)
    plt.title('Correlation Matrix', fontsize=16, fontweight='bold')
    plt.tight_layout()
    plt.show()

if len(numeric_cols) > 0:
    # Distribution plots
    print("\n📊 DISTRIBUTION ANALYSIS")
    print("-" * 80)

    n_cols = min(3, len(numeric_cols))
    n_rows = (len(numeric_cols) + n_cols - 1) // n_cols

    fig, axes = plt.subplots(n_rows, n_cols, figsize=(5 * n_cols, 4 * n_rows))
    if len(numeric_cols) == 1:
        axes = [axes]
    else:
        axes = axes.flatten() if n_rows > 1 else axes

    for ax, col in zip(axes, numeric_cols):
        df[col].hist(ax=ax, bins=30, edgecolor='black', alpha=0.7, color='skyblue')
        ax.set_title(f'{col} Distribution', fontweight='bold')
        ax.set_xlabel(col)
        ax.set_ylabel('Frequency')
        ax.grid(True, alpha=0.3)

    for i in range(len(numeric_cols), len(axes)):
        axes[i].set_visible(False)

    plt.tight_layout()
    plt.show()

    for col in numeric_cols:
        print(f"{col}: μ={df[col].mean():.2f}, σ={df[col].std():.2f}")

print("\n" + "=" * 80)
print("✅ ANALYSIS COMPLETE")
print("=" * 80)
`
	}

	return code
}
