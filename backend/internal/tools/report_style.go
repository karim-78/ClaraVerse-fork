package tools

import "strings"

// reportBaseCSS is auto-injected into every create_document render. The PDF
// renderer (Chromium via chromedp) uses ZERO page margins, so without this,
// content runs edge-to-edge and splits across page boundaries. This guarantees
// margin-safe, page-break-safe output AND ships Clara's "modern accent" design
// system (indigo #4F46E5). Author CSS loads after this and wins on conflicts.
const reportBaseCSS = `
@page { size: A4; margin: 16mm 14mm; }
* { box-sizing: border-box; }
html { -webkit-print-color-adjust: exact; print-color-adjust: exact; }
body { font-family: -apple-system,'Segoe UI',Roboto,Helvetica,Arial,sans-serif; font-size: 11pt; line-height: 1.55; color: #1E293B; margin: 0; }
h1,h2,h3,h4,h5 { color: #0F172A; line-height: 1.25; break-after: avoid; page-break-after: avoid; margin: 1.1em 0 .5em; }
h1 { font-size: 23pt; } h2 { font-size: 15.5pt; border-bottom: 2px solid #4F46E5; padding-bottom: 4px; } h3 { font-size: 12.5pt; color: #4F46E5; }
p { margin: 0 0 .7em; } a { color: #4F46E5; } small,.muted { color: #64748B; }
ul,ol { margin: 0 0 .7em 1.1em; } li { margin: .2em 0; }
img, figure, table, pre, blockquote, .card, .callout, .kpi { break-inside: avoid; page-break-inside: avoid; }
img { max-width: 100%; display: block; }
figure { margin: 0 0 1em; } figcaption { font-size: 9.5pt; color: #64748B; margin-top: 4px; text-align: center; }
table { border-collapse: collapse; width: 100%; font-size: 10pt; margin: .4em 0 1em; }
thead { display: table-header-group; }
th { background: #4F46E5; color: #fff; text-align: left; padding: 8px 10px; font-weight: 600; }
td { padding: 7px 10px; border-bottom: 1px solid #E2E8F0; }
tbody tr:nth-child(even) { background: #F8FAFC; }
.report-header { border-left: 6px solid #4F46E5; padding: 16px 20px; background: #F5F3FF; border-radius: 10px; margin-bottom: 20px; }
.report-header h1 { margin: 0 0 2px; } .report-header .sub { color: #6D28D9; font-weight: 600; font-size: 11pt; }
.report-header .wordmark { color: #4F46E5; font-weight: 800; letter-spacing: .02em; font-size: 10pt; text-transform: uppercase; }
.card { border: 1px solid #E2E8F0; border-radius: 12px; padding: 16px 18px; margin: 0 0 14px; }
.callout { border-left: 4px solid #4F46E5; background: #F5F3FF; border-radius: 8px; padding: 11px 15px; margin: 0 0 12px; }
.callout.warn { border-color: #F59E0B; background: #FFFBEB; } .callout.danger { border-color: #EF4444; background: #FEF2F2; } .callout.ok { border-color: #10B981; background: #ECFDF5; }
.kpi-row { display: flex; gap: 12px; margin: 0 0 16px; }
.kpi { flex: 1; border: 1px solid #E2E8F0; border-radius: 12px; padding: 12px 15px; }
.kpi .value { font-size: 20pt; font-weight: 700; color: #4F46E5; line-height: 1.1; } .kpi .label { font-size: 8.5pt; color: #64748B; text-transform: uppercase; letter-spacing: .05em; }
.badge { display: inline-block; padding: 2px 9px; border-radius: 999px; font-size: 8.5pt; font-weight: 600; background: #EEF2FF; color: #4F46E5; }
hr { border: none; border-top: 1px solid #E2E8F0; margin: 1.2em 0; }
code { font-family: 'SF Mono',Menlo,Consolas,monospace; font-size: 9.5pt; background: #F1F5F9; padding: 1px 5px; border-radius: 4px; }
pre { font-family: 'SF Mono',Menlo,Consolas,monospace; font-size: 9.5pt; background: #0F172A; color: #E2E8F0; padding: 12px 14px; border-radius: 8px; white-space: pre-wrap; overflow-wrap: anywhere; }
.page-break { break-before: page; page-break-before: always; }
`

// injectReportBaseCSS inserts reportBaseCSS so it loads BEFORE any author CSS
// (author rules then override on conflict). Handles full documents and bare
// fragments alike.
func injectReportBaseCSS(html string) string {
	style := "<style id=\"migi-report-base\">" + reportBaseCSS + "</style>"
	lo := strings.ToLower(html)
	// Inject right after an existing <head ...> if present.
	if pos := tagEnd(lo, "head"); pos >= 0 {
		return html[:pos] + style + html[pos:]
	}
	// Has <html ...> but no <head>: add a head block after it.
	if pos := tagEnd(lo, "html"); pos >= 0 {
		return html[:pos] + "<head><meta charset=\"utf-8\">" + style + "</head>" + html[pos:]
	}
	// Bare fragment: wrap in a minimal document.
	return "<!DOCTYPE html><html><head><meta charset=\"utf-8\">" + style + "</head><body>" + html + "</body></html>"
}

// firstChars returns up to n characters from the start of s (used to surface
// the schema printed at the top of sandbox stdout into error messages).
func firstChars(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "\n…(truncated)"
}

// tagEnd returns the index just past the first "<tag...>" opening tag in lo,
// or -1 if not found.
func tagEnd(lo, tag string) int {
	i := strings.Index(lo, "<"+tag)
	if i < 0 {
		return -1
	}
	// Confirm it's an opening tag (next char is '>' or whitespace), not e.g. <header>.
	after := i + 1 + len(tag)
	if after < len(lo) {
		c := lo[after]
		if c != '>' && c != ' ' && c != '\t' && c != '\n' && c != '\r' {
			return -1
		}
	}
	if j := strings.Index(lo[i:], ">"); j >= 0 {
		return i + j + 1
	}
	return -1
}

// ReportStyleGuide is appended to the create_document tool description so the
// model produces presentable, print-safe reports (RCA, analyses, etc.).
const ReportStyleGuide = `

────────────────────────────────────────
CLARA REPORT STYLE — READ BEFORE WRITING HTML
A base stylesheet (modern indigo #4F46E5 theme) is AUTO-INJECTED, and it already
guarantees A4 page margins and page-break safety. Build on it; don't fight it.

Ready-made classes (use them for a consistent, polished look):
- <div class="report-header"><div class="wordmark">CLARA</div><h1>Title</h1>
  <div class="sub">Subtitle · Date</div></div>   ← cover/title block
- <div class="kpi-row"><div class="kpi"><div class="value">₹4.2L</div>
  <div class="label">Total</div></div> … </div>  ← headline metrics
- <div class="card"> … </div>                     ← a section that stays together
- <div class="callout warn|danger|ok"> … </div>   ← highlights / findings
- <span class="badge">label</span>, <div class="page-break"></div>
- Tables are auto-styled (indigo header, zebra rows, header repeats per page).

EMBEDDING CHARTS (important):
1. Generate the chart with run_python or analyze_data — they RETURN the image as
   a base64 PNG (the "plots" array, format "png").
2. Embed it as a data URI inside a figure:
   <figure><img src="data:image/png;base64,PASTE_THE_BASE64_HERE"
     style="width:100%"><figcaption>Fig 1 — what it shows</figcaption></figure>
   (figures already have break-inside:avoid, so charts never split.)

PAGE-BREAK RULES:
- Wrap any block that must stay intact in <div class="card"> (or add style="break-inside:avoid").
- Use <div class="page-break"></div> to start a new page (e.g., before each major section).
- Don't hard-code heights or absolute positioning — let content flow.
- Page numbers are NOT available from the renderer; don't rely on them.

RCA REPORT SKELETON (adapt as needed):
  report-header (title + date)
  → "Executive Summary" card (2-3 sentences + a kpi-row)
  → "Incident Timeline" card (table)
  → "Root Cause" card (callout danger + explanation + a chart figure)
  → "Impact" card (kpi-row / table)
  → "Corrective & Preventive Actions" card (checklist table: Action | Owner | Due)
Keep prose tight, lead with the answer, label every chart and table.`

// DataColumnsGuide is appended to run_python + analyze_data so the model uses
// the real column names (the #1 cause of KeyError loops on messy sheets).
const DataColumnsGuide = `

────────────────────────────────────────
COLUMN NAMES & SHEETS (prevents KeyError loops):
- For an Excel workbook, call migi_describe(path) FIRST — it prints columns +
  dtypes for EVERY sheet, so you know which sheet has which columns before you
  analyze. migi_load() also prints all sheets' columns when it auto-loads.
- Load a specific sheet with migi_load(path, sheet='Name'); migi_load_all(path)
  returns {sheet_name: DataFrame} for cross-sheet work.
- Copy column names VERBATIM from the printed [schema] — never invent, abbreviate
  or "tidy" them. They belong to a SPECIFIC sheet's DataFrame.
- If unsure a name matches, use col(df, 'your guess') — it fuzzy-resolves to the
  real column (case/punctuation/word-order-insensitive): df[col(df, 'monthly salary')].
- Create any derived/cleaned column explicitly BEFORE you reference it.`

// ChartStyleGuide is appended to the run_python and analyze_data descriptions
// so generated charts are crisp and on-brand (and ready to embed in reports).
const ChartStyleGuide = `

────────────────────────────────────────
CHART AESTHETICS:
- Call migi_chart_style() ONCE before plotting — it sets crisp 150-DPI output,
  a clean despined look, and Clara's accent palette (auto-injected, defined for you).
- Every chart needs: a bold title, axis labels, and a legend when >1 series.
- Format large numbers (e.g. ax.yaxis.set_major_formatter for thousands/lakhs).
- Rotate long x labels: plt.xticks(rotation=30, ha='right').
- One chart per figure; end with plt.tight_layout(); plt.show().
- Charts are returned to you as base64 PNGs — to put one in a PDF report, pass
  that base64 into create_document as <img src="data:image/png;base64,...">.`
