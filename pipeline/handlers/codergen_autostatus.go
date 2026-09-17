// ABOUTME: auto_status STATUS-line parsing for codergen nodes — the tolerant
// ABOUTME: verdict grammar, last-line-wins scan, and code-fence handling.
package handlers

import (
	"regexp"
	"strings"

	"github.com/2389-research/tracker/pipeline"
)

// parseAutoStatus scans the response text for STATUS: directives and returns
// the last one found. Case-insensitive matching. Lines inside code fences
// (``` blocks) are skipped to avoid matching hallucinated STATUS lines.
// The second return reports whether any valid STATUS line was found (#346);
// when it is false the status falls back to the legacy success default and
// the caller decides whether that default is acceptable (it is not on a
// goal gate — see resolveTerminalStatus).
//
// Last-line-wins: every non-fenced line is scanned and the LAST parseable
// verdict is the result. SpecLint / ForgeSpec-style prompts rely on this —
// they emit `STATUS:fail` as the first line and override it with a final
// `STATUS:success` only after every check passes, so a truncated response
// keeps the early fail. A STATUS-prefixed line whose value is unparseable
// (`STATUS: maybe`) is NOT a verdict: it neither counts as found nor erases
// an earlier explicit verdict.
//
// Fence handling (#645): fence markers are paired in document order. When
// the total count is odd the final marker is an unclosed opener (an agent
// pasted grep/test output and never closed the fence); that marker is
// ignored so a verdict emitted after it is still the verdict. A STATUS
// line inside a properly closed fence stays ignored.
func parseAutoStatus(text string) (pipeline.TerminalStatus, bool) {
	result := pipeline.OutcomeSuccess
	found := false
	for _, line := range unfencedLines(text) {
		if s := parseStatusLine(line); s != "" {
			result = s
			found = true
		}
	}
	return result, found
}

// unfencedLines returns the trimmed lines of text that lie outside ``` code
// fences, with an odd trailing (unclosed) fence marker treated as plain text.
func unfencedLines(text string) []string {
	lines := strings.Split(text, "\n")
	unclosed := unclosedFenceIndex(lines)
	var out []string
	inCodeBlock := false
	for i, line := range lines {
		trimmed := strings.TrimSpace(line)
		switch {
		case isFenceMarker(trimmed) && i != unclosed:
			inCodeBlock = !inCodeBlock
		case isFenceMarker(trimmed), inCodeBlock:
			// unclosed opener (kept out of the scan) or fenced content
		default:
			out = append(out, trimmed)
		}
	}
	return out
}

// isFenceMarker reports whether a trimmed line opens or closes a ``` block.
func isFenceMarker(trimmed string) bool {
	return strings.HasPrefix(trimmed, "```")
}

// unclosedFenceIndex returns the line index of the final fence marker when
// the markers do not pair up (odd count), or -1 when every fence is closed.
func unclosedFenceIndex(lines []string) int {
	count, last := 0, -1
	for i, line := range lines {
		if isFenceMarker(strings.TrimSpace(line)) {
			count++
			last = i
		}
	}
	if count%2 == 0 {
		return -1
	}
	return last
}

// statusLineRE is the tolerant STATUS-line grammar (#645):
//
//	^\s*#*\s*[`*_~]*\s*STATUS\s*:\s*[`*_~]*\s*(success|fail|retry)\b
//
// applied case-insensitively. Leading markdown heading markers (#346),
// emphasis (`**` / `_` / `~~`, #233 Gap 5.1) and inline-code backticks are
// skipped before the keyword and before the value; everything after the
// value (closing markers, punctuation, prose, counts, emoji) is ignored.
// The \b after the value keeps `STATUS: failure` / `STATUS: successful`
// from parsing as a verdict.
var statusLineRE = regexp.MustCompile("(?i)^\\s*#*\\s*[`*_~]*\\s*STATUS\\s*[`*_~]*\\s*:\\s*[`*_~]*\\s*(success|fail|retry)\\b")

// parseStatusLine extracts the status value from a "STATUS: ..." line.
// Returns "" if the line is not a valid STATUS directive — including a
// STATUS-prefixed line whose value is not one of success/fail/retry.
func parseStatusLine(trimmed string) pipeline.TerminalStatus {
	m := statusLineRE.FindStringSubmatch(trimmed)
	if m == nil {
		return ""
	}
	switch strings.ToLower(m[1]) {
	case "success":
		return pipeline.OutcomeSuccess
	case "fail":
		return pipeline.OutcomeFail
	case "retry":
		return pipeline.OutcomeRetry
	}
	return ""
}
