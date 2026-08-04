// ABOUTME: SEARCH/REPLACE block matcher for the audit pass — four fallback
// ABOUTME: strategies (exact, indent, whitespace, fuzzy) with partial-apply semantics.
package sprintwriter

import (
	"fmt"
	"strings"
)

// applySRBlocks applies the given SEARCH/REPLACE blocks to the draft in source
// order, matching each block via a series of fallback strategies (mirrors
// pipelines/lib/merge_sr.py): exact substring match → indent-preserving →
// whitespace-insensitive → fuzzy (Levenshtein-ratio ≥ 0.9). The first strategy
// that finds a match wins; subsequent strategies are not tried for that block.
//
// Partial-apply semantics: each block is applied independently against the
// current state. A block that fails to match (no strategy succeeds) is logged
// and skipped; subsequent blocks continue. This trades the all-or-nothing
// safety of strict apply for resilience to sequential overlap (block N's
// SEARCH anchored on text changed by block M<N).
//
// Returns the patched text, the count of successfully applied blocks, and a
// slice of "block %d: <reason>" strings for blocks that were skipped. If zero
// blocks applied, the caller can treat that as "no patch happened" and fall
// back to the unaudited draft.
func applySRBlocks(draft string, blocks []srBlock) (patched string, applied int, skipped []string) {
	out := draft
	for i, b := range blocks {
		if b.Search == "" {
			skipped = append(skipped, fmt.Sprintf("block %d: empty SEARCH text", i))
			continue
		}
		if next, ok := applyOneSRBlock(out, b); ok {
			out = next
			applied++
		} else {
			skipped = append(skipped, fmt.Sprintf("block %d: SEARCH text not found (tried exact, indent, whitespace, fuzzy)", i))
		}
	}
	return out, applied, skipped
}

// applyOneSRBlock tries each match strategy in order and returns the first
// successful result. ok=false means no strategy matched b.Search.
func applyOneSRBlock(content string, b srBlock) (string, bool) {
	for _, st := range srMatchStrategies {
		if next, ok := st.fn(content, b.Search, b.Replace); ok {
			return next, true
		}
	}
	return content, false
}

type srMatchStrategy struct {
	name string
	fn   func(content, search, replace string) (string, bool)
}

// srMatchStrategies are tried in order. Indent precedes whitespace so that
// uniform-indent SEARCH blocks get their outer indent re-applied to REPLACE,
// rather than the whitespace strategy matching first and substituting REPLACE
// verbatim (which would lose the chunk's outer indent).
var srMatchStrategies = []srMatchStrategy{
	{"exact", trySRExact},
	{"indent", trySRIndent},
	{"whitespace", trySRWhitespace},
	{"fuzzy", trySRFuzzy},
}

// trySRExact returns content with the first occurrence of search replaced by
// replace, or false if search isn't present.
func trySRExact(content, search, replace string) (string, bool) {
	if !strings.Contains(content, search) {
		return content, false
	}
	return strings.Replace(content, search, replace, 1), true
}

// trySRWhitespace matches search against any N-line window of content after
// collapsing runs of whitespace on both sides, where N is the number of lines
// in search. Returns content with the matched chunk replaced verbatim by
// replace.
func trySRWhitespace(content, search, replace string) (string, bool) {
	needle := collapseWhitespace(search)
	if needle == "" {
		return content, false
	}
	contentLines := splitKeepNewlines(content)
	n := strings.Count(search, "\n") + 1
	if n > len(contentLines) {
		return content, false
	}
	for i := 0; i <= len(contentLines)-n; i++ {
		chunk := strings.Join(contentLines[i:i+n], "")
		if collapseWhitespace(chunk) == needle {
			return replaceLineWindow(content, contentLines, i, n, replace), true
		}
	}
	return content, false
}

// trySRIndent matches a dedented form of search against content's lines, then
// re-indents replace using the matched chunk's leading whitespace. Useful when
// the LLM emits SEARCH at one indent level and the draft has it at another
// (common when patching nested code or markdown).
func trySRIndent(content, search, replace string) (string, bool) {
	sIndent := commonLeadingIndent(search)
	if sIndent == "" {
		return content, false
	}
	dedentedSearch := dedentLines(search, sIndent)

	contentLines := splitKeepNewlines(content)
	n := strings.Count(search, "\n") + 1
	if n > len(contentLines) {
		return content, false
	}
	for i := 0; i <= len(contentLines)-n; i++ {
		chunk := strings.Join(contentLines[i:i+n], "")
		chunkIndent := commonLeadingIndent(chunk)
		if chunkIndent == "" {
			continue
		}
		dedentedChunk := dedentLines(chunk, chunkIndent)
		if strings.TrimRight(dedentedChunk, "\n") == strings.TrimRight(dedentedSearch, "\n") {
			// Apply the same indent offset to replace: dedent by sIndent (the
			// indent SEARCH was emitted with), then re-indent by chunkIndent
			// (the actual draft's indent). When sIndent == chunkIndent this is
			// a no-op; otherwise the offset shifts replace's content into the
			// chunk's column space.
			dedentedReplace := dedentLines(replace, sIndent)
			indentedReplace := indentLines(dedentedReplace, chunkIndent)
			return replaceLineWindow(content, contentLines, i, n, indentedReplace), true
		}
	}
	return content, false
}

// trySRFuzzy slides an N-line window through content (N = lines in search),
// computes a Levenshtein-distance-based similarity ratio against search for
// each window, and accepts the highest-scoring window if its ratio ≥ 0.9.
// This catches cases where the LLM's SEARCH has minor character drift (typos,
// reformatted indentation, slightly-different wording) that the earlier
// strategies miss.
func trySRFuzzy(content, search, replace string) (string, bool) {
	const threshold = 0.9
	contentLines := splitKeepNewlines(content)
	n := strings.Count(search, "\n") + 1
	if n == 0 || n > len(contentLines) {
		return content, false
	}
	bestRatio := 0.0
	bestIdx := -1
	for i := 0; i <= len(contentLines)-n; i++ {
		chunk := strings.Join(contentLines[i:i+n], "")
		r := similarityRatio(chunk, search)
		if r > bestRatio {
			bestRatio = r
			bestIdx = i
		}
	}
	if bestIdx < 0 || bestRatio < threshold {
		return content, false
	}
	return replaceLineWindow(content, contentLines, bestIdx, n, replace), true
}

// replaceLineWindow replaces the byte range occupied by contentLines[start:start+n]
// in content with replace. Position-based (not first-occurrence-based) so the
// windowed SR strategies can't accidentally patch an earlier identical-looking
// chunk when the matched line range appears more than once in the document.
//
// Preserves trailing-newline behavior: if the matched window ended in '\n' but
// the replacement doesn't, an LF is appended to keep the surrounding line
// structure intact.
func replaceLineWindow(content string, contentLines []string, start, n int, replace string) string {
	startByte := 0
	for j := 0; j < start; j++ {
		startByte += len(contentLines[j])
	}
	endByte := startByte
	for j := start; j < start+n; j++ {
		endByte += len(contentLines[j])
	}
	finalReplace := replace
	if endByte > 0 && content[endByte-1] == '\n' && !strings.HasSuffix(finalReplace, "\n") {
		finalReplace += "\n"
	}
	return content[:startByte] + finalReplace + content[endByte:]
}

// collapseWhitespace returns s with all runs of whitespace collapsed to a single
// space and leading/trailing whitespace stripped. Used for whitespace-insensitive
// SR-block matching.
func collapseWhitespace(s string) string {
	return strings.Join(strings.Fields(s), " ")
}

// splitKeepNewlines splits s on '\n' but keeps the trailing newline on each
// preceding element so concatenating the result reproduces s exactly. Used by
// the windowed SR-block strategies to slide N-line chunks through content.
func splitKeepNewlines(s string) []string {
	if s == "" {
		return nil
	}
	var lines []string
	start := 0
	for i := 0; i < len(s); i++ {
		if s[i] == '\n' {
			lines = append(lines, s[start:i+1])
			start = i + 1
		}
	}
	if start < len(s) {
		lines = append(lines, s[start:])
	}
	return lines
}

// commonLeadingIndent returns the longest sequence of spaces/tabs that prefixes
// every non-blank line in s.
func commonLeadingIndent(s string) string {
	nonEmpty := nonBlankLines(s)
	if len(nonEmpty) == 0 {
		return ""
	}
	prefix := nonEmpty[0]
	for _, ln := range nonEmpty[1:] {
		prefix = stringCommonPrefix(prefix, ln)
		if prefix == "" {
			return ""
		}
	}
	return leadingWhitespace(prefix)
}

// nonBlankLines returns the lines of s (split on '\n') that are not
// whitespace-only.
func nonBlankLines(s string) []string {
	var out []string
	for _, ln := range strings.Split(s, "\n") {
		if strings.TrimSpace(ln) != "" {
			out = append(out, ln)
		}
	}
	return out
}

// leadingWhitespace returns the longest prefix of s made of spaces/tabs.
func leadingWhitespace(s string) string {
	end := 0
	for end < len(s) && (s[end] == ' ' || s[end] == '\t') {
		end++
	}
	return s[:end]
}

// stringCommonPrefix returns the longest common prefix of a and b.
func stringCommonPrefix(a, b string) string {
	n := len(a)
	if len(b) < n {
		n = len(b)
	}
	for i := 0; i < n; i++ {
		if a[i] != b[i] {
			return a[:i]
		}
	}
	return a[:n]
}

// dedentLines strips the given indent from the start of each line in s that
// begins with it, leaving other lines unchanged.
func dedentLines(s, indent string) string {
	if indent == "" {
		return s
	}
	lines := strings.Split(s, "\n")
	for i, ln := range lines {
		if strings.HasPrefix(ln, indent) {
			lines[i] = ln[len(indent):]
		}
	}
	return strings.Join(lines, "\n")
}

// indentLines prepends the given indent to each non-blank line of s.
func indentLines(s, indent string) string {
	if indent == "" {
		return s
	}
	lines := strings.Split(s, "\n")
	for i, ln := range lines {
		if ln != "" {
			lines[i] = indent + ln
		}
	}
	return strings.Join(lines, "\n")
}

// similarityRatio approximates Python's difflib.SequenceMatcher.ratio() using a
// Levenshtein-edit-distance-based ratio: 1 - dist/max(len(a), len(b)). For our
// use case (catching minor character drift in audit SR blocks) the two metrics
// agree closely on whether two strings are "approximately equal" at threshold
// 0.9.
//
// Lengths are measured in runes — levenshteinDistance operates on runes too,
// so divisor and dividend are in the same unit. Using byte lengths here would
// understate the ratio for any non-ASCII content (em-dashes, smart quotes,
// CJK), which is common in sprint specs.
func similarityRatio(a, b string) float64 {
	if a == b {
		return 1.0
	}
	la := len([]rune(a))
	lb := len([]rune(b))
	if la == 0 && lb == 0 {
		return 1.0
	}
	if la == 0 || lb == 0 {
		return 0.0
	}
	dist := levenshteinDistance(a, b)
	maxLen := la
	if lb > maxLen {
		maxLen = lb
	}
	return 1.0 - float64(dist)/float64(maxLen)
}

// levenshteinDistance returns the edit distance between a and b using a
// rolling two-row DP. O(len(a) * len(b)) time, O(min(la, lb)) space.
func levenshteinDistance(a, b string) int {
	ar := []rune(a)
	br := []rune(b)
	la := len(ar)
	lb := len(br)
	if la == 0 {
		return lb
	}
	if lb == 0 {
		return la
	}
	if la < lb {
		ar, br = br, ar
		la, lb = lb, la
	}
	prev := make([]int, lb+1)
	curr := make([]int, lb+1)
	for j := 0; j <= lb; j++ {
		prev[j] = j
	}
	for i := 1; i <= la; i++ {
		levenshteinRow(prev, curr, ar, br, i)
		prev, curr = curr, prev
	}
	return prev[lb]
}

// levenshteinRow fills curr as DP row i of the edit-distance matrix from the
// previous row prev. ar is the longer rune slice, br the shorter.
func levenshteinRow(prev, curr []int, ar, br []rune, i int) {
	curr[0] = i
	for j := 1; j <= len(br); j++ {
		cost := 1
		if ar[i-1] == br[j-1] {
			cost = 0
		}
		curr[j] = min3(curr[j-1]+1, prev[j]+1, prev[j-1]+cost)
	}
}

// min3 returns the smallest of three ints.
func min3(a, b, c int) int {
	m := a
	if b < m {
		m = b
	}
	if c < m {
		m = c
	}
	return m
}
