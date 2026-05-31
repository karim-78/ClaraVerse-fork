package rag

import (
	"bytes"
	"fmt"
	"path/filepath"
	"regexp"
	"strings"

	"claraverse/internal/utils"
)

// DocSegment is one logical piece of an extracted document. For PDFs
// this is a page; for markdown we synthesize segments per top-level
// heading section; for plain text we emit a single segment.
//
// Carrying segments (rather than a flat text blob) is what makes
// citations work — when a chunk is returned by search, we can point
// the UI back to "page 7" or "section 'Pricing'" rather than just
// "somewhere in this document."
type DocSegment struct {
	Text    string
	Page    int    // 1-indexed; 0 means "not applicable"
	Section string // heading text for markdown; empty otherwise
}

// ParseFile extracts segmented text from raw bytes by content type.
//
// Supported v1:
//   - PDF       → one segment per page (uses utils.ExtractPDFText)
//   - markdown  → one segment per top-level section
//   - text/plain (.txt, no extension, anything else) → one segment
//   - HTML      → strip tags, then treat as text
//
// Unsupported types return an error so the ingest worker can mark the
// file failed and surface a clear message in the UI.
func ParseFile(filename, contentType string, data []byte) ([]DocSegment, error) {
	ext := strings.ToLower(filepath.Ext(filename))
	ct := strings.ToLower(contentType)

	switch {
	case ext == ".pdf" || strings.Contains(ct, "pdf"):
		return parsePDF(data)
	case ext == ".md" || ext == ".markdown" || strings.Contains(ct, "markdown"):
		return parseMarkdown(string(data)), nil
	case ext == ".html" || ext == ".htm" || strings.Contains(ct, "html"):
		// Strip tags crudely. The scraper service is the proper path
		// for URL ingestion (which gives us cleaner text); this branch
		// handles user-uploaded .html files.
		return []DocSegment{{Text: stripHTMLTags(string(data))}}, nil
	case ext == ".txt" || ext == "" || strings.HasPrefix(ct, "text/"):
		return []DocSegment{{Text: string(data)}}, nil
	default:
		return nil, fmt.Errorf("unsupported file type: %s (content-type=%s)", ext, contentType)
	}
}

// parsePDF wraps utils.ExtractPDFText and splits the result on the
// `--- Page N ---` markers it embeds. We rebuild segments with proper
// page numbers so citations work.
func parsePDF(data []byte) ([]DocSegment, error) {
	meta, err := utils.ExtractPDFText(data)
	if err != nil {
		return nil, err
	}
	// utils marks pages with "--- Page N ---" headers. Split on those.
	pageHeader := regexp.MustCompile(`\n?--- Page (\d+) ---\n?`)
	matches := pageHeader.FindAllStringSubmatchIndex(meta.Text, -1)
	if len(matches) == 0 {
		// No page markers (single page or unmarked) — return one segment.
		txt := strings.TrimSpace(meta.Text)
		if txt == "" {
			return nil, fmt.Errorf("PDF contained no extractable text")
		}
		return []DocSegment{{Text: txt, Page: 1}}, nil
	}
	var segs []DocSegment
	for i, m := range matches {
		// Page number from the capture group.
		pageNum := 0
		fmt.Sscanf(meta.Text[m[2]:m[3]], "%d", &pageNum)
		// Text body runs from end-of-this-header to start-of-next-header.
		start := m[1]
		end := len(meta.Text)
		if i+1 < len(matches) {
			end = matches[i+1][0]
		}
		body := strings.TrimSpace(meta.Text[start:end])
		if body == "" {
			continue
		}
		segs = append(segs, DocSegment{Text: body, Page: pageNum})
	}
	if len(segs) == 0 {
		return nil, fmt.Errorf("PDF contained no extractable text")
	}
	return segs, nil
}

// parseMarkdown splits a markdown document at top-level (# / ##)
// headings. Sections preserve their heading as the Section field so
// citations can say "Section: Pricing strategy".
//
// We deliberately don't recurse into ### + below — those usually
// belong with their parent section's chunk, and over-segmenting hurts
// retrieval (one query rarely needs only the H4 in isolation).
func parseMarkdown(text string) []DocSegment {
	lines := strings.Split(text, "\n")
	var segs []DocSegment
	var currentTitle string
	var current strings.Builder

	flush := func() {
		body := strings.TrimSpace(current.String())
		if body == "" {
			return
		}
		segs = append(segs, DocSegment{Text: body, Section: currentTitle})
		current.Reset()
	}

	headingRe := regexp.MustCompile(`^(#{1,2})\s+(.+?)\s*#*\s*$`)
	inCodeFence := false
	for _, line := range lines {
		// Don't treat hashes inside code fences as headings.
		if strings.HasPrefix(strings.TrimSpace(line), "```") {
			inCodeFence = !inCodeFence
			current.WriteString(line)
			current.WriteString("\n")
			continue
		}
		if !inCodeFence {
			if m := headingRe.FindStringSubmatch(line); m != nil {
				flush()
				currentTitle = strings.TrimSpace(m[2])
				// Keep the heading in the body too — gives the chunker
				// useful context when embedding.
				current.WriteString(line)
				current.WriteString("\n")
				continue
			}
		}
		current.WriteString(line)
		current.WriteString("\n")
	}
	flush()

	if len(segs) == 0 {
		// No headings — return whole doc as one segment.
		body := strings.TrimSpace(text)
		if body == "" {
			return nil
		}
		return []DocSegment{{Text: body}}
	}
	return segs
}

// stripHTMLTags is a cheap tag remover for user-uploaded HTML. Not a
// real HTML parser — for URL ingestion we use the scraper service
// which is much better. This is the "user dragged a .html into the
// upload area" fallback.
func stripHTMLTags(html string) string {
	var out bytes.Buffer
	inTag := false
	for _, r := range html {
		switch {
		case r == '<':
			inTag = true
		case r == '>':
			inTag = false
			out.WriteByte(' ')
		case !inTag:
			out.WriteRune(r)
		}
	}
	return strings.TrimSpace(collapseWhitespace(out.String()))
}

func collapseWhitespace(s string) string {
	var b strings.Builder
	lastSpace := false
	for _, r := range s {
		if r == ' ' || r == '\t' || r == '\n' || r == '\r' {
			if !lastSpace {
				b.WriteByte(' ')
				lastSpace = true
			}
		} else {
			b.WriteRune(r)
			lastSpace = false
		}
	}
	return b.String()
}
