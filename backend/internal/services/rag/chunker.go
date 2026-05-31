package rag

import (
	"strings"
	"unicode/utf8"
)

// Chunk is one chunkable slice of a document, ready to embed.
//
// Idx is the chunk's position within its source FILE (not segment) so
// citations stay sortable across the whole document. Page and Section
// carry through from the originating DocSegment for citation
// rendering.
type Chunk struct {
	Idx     int
	Text    string
	Page    int
	Section string
}

// ChunkConfig tunes the splitter. Defaults are the values most RAG
// systems converge on (1000/200 with sentence-aware splits) — they're
// a good compromise between retrieval recall and context budget.
type ChunkConfig struct {
	TargetChars int // ~1000 — embedding window for bge-small comfortably handles ~2000 input chars
	OverlapChars int // ~200 — context bleed across boundaries so a query straddling two chunks still hits one
	MinChars     int // ~100 — drop chunks shorter than this; they're usually noise (page footers, etc.)
}

// DefaultChunkConfig returns the production tuning. Used when callers
// pass a zero-value ChunkConfig.
func DefaultChunkConfig() ChunkConfig {
	return ChunkConfig{TargetChars: 1000, OverlapChars: 200, MinChars: 100}
}

// ChunkSegments converts a sequence of DocSegments (from the parser)
// into a sequence of Chunks ready for embedding. Idx is assigned
// across the entire file so the order is preserved end-to-end.
//
// Strategy: each segment is chunked independently — we don't merge
// across page or section boundaries because that would smear the
// citation. A short page (200 chars) becomes one chunk; a long one
// (5000 chars) becomes ~5-6 overlapping chunks.
func ChunkSegments(segments []DocSegment, cfg ChunkConfig) []Chunk {
	if cfg.TargetChars == 0 {
		cfg = DefaultChunkConfig()
	}
	var out []Chunk
	idx := 0
	for _, seg := range segments {
		for _, body := range splitText(seg.Text, cfg) {
			out = append(out, Chunk{
				Idx:     idx,
				Text:    body,
				Page:    seg.Page,
				Section: seg.Section,
			})
			idx++
		}
	}
	return out
}

// splitText is the actual recursive splitter. Splits on, in order of
// preference: double-newline (paragraph), single newline, period+space
// (sentence), and finally hard char boundary. This mirrors LangChain's
// RecursiveCharacterTextSplitter but trimmed for clarity.
//
// The "respect code fences" rule is baked in: we never split inside a
// ```fence``` boundary because that would produce syntactically
// half-broken code in the chunk text, which the LLM would treat as
// malformed and ignore.
func splitText(text string, cfg ChunkConfig) []string {
	text = strings.TrimSpace(text)
	if text == "" {
		return nil
	}
	if utf8.RuneCountInString(text) <= cfg.TargetChars {
		// Fits in one chunk — return as-is (skip the min check; even
		// a short whole-document is meaningful).
		return []string{text}
	}

	// Pre-find code fence boundaries so we can avoid splitting inside.
	fences := findCodeFenceRanges(text)

	var chunks []string
	start := 0
	for start < len(text) {
		// Tentative end at target.
		end := start + cfg.TargetChars
		if end >= len(text) {
			chunks = append(chunks, strings.TrimSpace(text[start:]))
			break
		}

		// If we'd land inside a code fence, push the end to the fence
		// closure (or to start if we're already past the open).
		end = avoidFenceSplit(end, fences, len(text))

		// Pull the end back to a good boundary: paragraph > line >
		// sentence > word. If nothing found in the lookback window,
		// hard-cut at end.
		bestEnd := end
		win := text[start:end]
		if i := strings.LastIndex(win, "\n\n"); i > cfg.TargetChars/2 {
			bestEnd = start + i + 2
		} else if i := strings.LastIndex(win, "\n"); i > cfg.TargetChars/2 {
			bestEnd = start + i + 1
		} else if i := strings.LastIndex(win, ". "); i > cfg.TargetChars/2 {
			bestEnd = start + i + 2
		} else if i := strings.LastIndex(win, " "); i > cfg.TargetChars/2 {
			bestEnd = start + i + 1
		}
		if bestEnd <= start {
			bestEnd = end // hard cut to make progress
		}

		body := strings.TrimSpace(text[start:bestEnd])
		if utf8.RuneCountInString(body) >= cfg.MinChars || len(chunks) == 0 {
			chunks = append(chunks, body)
		}

		// Step forward with overlap. If the overlap would put us past
		// bestEnd (e.g. on tiny chunks), skip ahead instead.
		next := bestEnd - cfg.OverlapChars
		if next <= start {
			next = bestEnd
		}
		start = next
	}
	return chunks
}

// findCodeFenceRanges returns [start, end) byte ranges of fenced code
// blocks in the document. Used so the chunker doesn't slice through
// the middle of a ```fence```.
func findCodeFenceRanges(text string) [][2]int {
	var ranges [][2]int
	var openIdx = -1
	lines := strings.Split(text, "\n")
	pos := 0
	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "```") {
			if openIdx == -1 {
				openIdx = pos
			} else {
				ranges = append(ranges, [2]int{openIdx, pos + len(line)})
				openIdx = -1
			}
		}
		pos += len(line) + 1 // +1 for the \n
	}
	// Unclosed fence: extend to end.
	if openIdx != -1 {
		ranges = append(ranges, [2]int{openIdx, len(text)})
	}
	return ranges
}

// avoidFenceSplit nudges `end` outside any fenced region it falls
// inside. If end is inside a fence, push it to the fence's end (or
// keep at end if the fence runs to EOF).
func avoidFenceSplit(end int, fences [][2]int, total int) int {
	for _, r := range fences {
		if end > r[0] && end < r[1] {
			// Push end to fence close (or EOF if fence runs to end).
			if r[1] >= total {
				return total
			}
			return r[1]
		}
	}
	return end
}
