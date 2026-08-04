// ABOUTME: Auditor-response parsing: tolerant verdict detection and Aider-style
// ABOUTME: SEARCH/REPLACE block extraction for the enriched-sprint audit pass.
package sprintwriter

import (
	"fmt"
	"strings"
)

// srBlock is one Aider-style SEARCH/REPLACE patch.
type srBlock struct {
	Search  string
	Replace string
}

const (
	verdictPrefix      = "AUDIT-VERDICT:"
	verdictSearchLines = 10
	searchMarker       = "<<<<<<< SEARCH"
	divideMarker       = "======="
	replaceMarker      = ">>>>>>> REPLACE"
)

// parseAuditResponse parses the auditor's output. The verdict line
// `AUDIT-VERDICT: PASS|PATCHED` may appear anywhere in the first ~10 lines —
// not just the literal first line — to tolerate models that prepend a brief
// preamble ("Looking at the draft...", "After review..."), a markdown fence,
// or a leading blank line despite the prompt's instructions.
//
// For PATCHED, everything AFTER the verdict line is parsed as Aider-style
// SEARCH/REPLACE blocks:
//
//	<<<<<<< SEARCH
//	<old text>
//	=======
//	<new text>
//	>>>>>>> REPLACE
//
// Returns the verdict, the parsed blocks, and an error if no verdict line is
// found in the first 10 lines or the verdict value is unrecognized.
func parseAuditResponse(s string) (verdict string, blocks []srBlock, err error) {
	trimmed := strings.TrimSpace(s)
	if trimmed == "" {
		return "", nil, fmt.Errorf("empty audit response")
	}

	// Strip a single enclosing markdown fence, e.g. ```\n...\n```
	trimmed = trimEnclosingMarkdownFence(trimmed)

	lines := strings.Split(trimmed, "\n")
	verdictIdx := findVerdictLine(lines)
	if verdictIdx == -1 {
		return "", nil, fmt.Errorf("no %q line found in first %d non-empty lines", verdictPrefix, verdictSearchLines)
	}

	verdict, err = extractVerdict(lines[verdictIdx])
	if err != nil {
		return "", nil, err
	}
	if verdict == "PASS" {
		return "PASS", nil, nil
	}

	// PATCHED — parse SR blocks from everything after the verdict line.
	if verdictIdx+1 >= len(lines) {
		return verdict, nil, fmt.Errorf("PATCHED verdict but no body after verdict line")
	}
	body := strings.Join(lines[verdictIdx+1:], "\n")
	blocks, err = parseSRBlocks(body)
	if err != nil {
		return verdict, nil, err
	}
	if len(blocks) == 0 {
		return verdict, nil, fmt.Errorf("PATCHED verdict but zero SR blocks parsed")
	}
	return verdict, blocks, nil
}

// findVerdictLine returns the index of the first line (within the first
// verdictSearchLines non-empty lines) that carries the verdict prefix, even
// when wrapped in inline backticks or bold/italic markdown. Returns -1 when
// not found in the scan window.
func findVerdictLine(lines []string) int {
	scanned := 0
	for i, ln := range lines {
		stripped := strings.TrimSpace(ln)
		if stripped == "" {
			continue
		}
		bare := strings.TrimFunc(stripped, isMarkdownDecoration)
		if strings.HasPrefix(bare, verdictPrefix) {
			return i
		}
		scanned++
		if scanned >= verdictSearchLines {
			break
		}
	}
	return -1
}

// extractVerdict pulls the verdict value ("PASS" or "PATCHED") out of the
// verdict line, trimming surrounding markdown decoration and trailing
// punctuation. Returns an error for any other value.
func extractVerdict(verdictLine string) (string, error) {
	verdictLine = strings.TrimSpace(verdictLine)
	verdictLine = strings.TrimFunc(verdictLine, isMarkdownDecoration)
	verdict := strings.TrimSpace(strings.TrimPrefix(verdictLine, verdictPrefix))
	// Some models append trailing markdown decoration or a period after the verdict word.
	verdict = strings.TrimFunc(verdict, func(r rune) bool {
		return isMarkdownDecoration(r) || r == '.'
	})
	verdict = strings.TrimSpace(verdict)
	if verdict != "PASS" && verdict != "PATCHED" {
		return "", fmt.Errorf("unrecognized verdict %q (must be PASS or PATCHED)", verdict)
	}
	return verdict, nil
}

// isMarkdownDecoration reports whether r is an inline-markdown decoration
// character (backtick, asterisk, underscore) that may wrap a verdict line.
func isMarkdownDecoration(r rune) bool {
	return r == '`' || r == '*' || r == '_'
}

// parseSRBlocks extracts Aider-style SEARCH/REPLACE blocks from a body of text.
// Surrounding fences (e.g., ```) are tolerated and ignored. Returns blocks in
// source order; on a malformed block it returns the blocks parsed so far plus
// the error.
func parseSRBlocks(body string) ([]srBlock, error) {
	var blocks []srBlock
	rest := body
	for {
		blk, remainder, found, err := nextSRBlock(rest)
		if err != nil {
			return blocks, err
		}
		if !found {
			break
		}
		blocks = append(blocks, blk)
		rest = remainder
	}
	return blocks, nil
}

// nextSRBlock finds the next SEARCH/REPLACE block in rest. found=false with a
// nil error means no further block; found=false with a non-nil error means a
// malformed block. On success it returns the block and the unparsed remainder.
func nextSRBlock(rest string) (blk srBlock, remainder string, found bool, err error) {
	startIdx := strings.Index(rest, searchMarker)
	if startIdx < 0 {
		return srBlock{}, "", false, nil
	}
	afterStart := rest[startIdx+len(searchMarker):]
	// Skip the rest of the marker line (handles "<<<<<<< SEARCH\n" and "<<<<<<< SEARCH something\n").
	nlIdx := strings.IndexByte(afterStart, '\n')
	if nlIdx < 0 {
		return srBlock{}, "", false, fmt.Errorf("malformed block: SEARCH marker without following newline")
	}
	afterStart = afterStart[nlIdx+1:]
	divIdx := strings.Index(afterStart, divideMarker)
	if divIdx < 0 {
		return srBlock{}, "", false, fmt.Errorf("malformed block: SEARCH without ======= divider")
	}
	searchText := strings.TrimRight(afterStart[:divIdx], "\n")
	afterDiv := afterStart[divIdx+len(divideMarker):]
	nlIdx = strings.IndexByte(afterDiv, '\n')
	if nlIdx < 0 {
		return srBlock{}, "", false, fmt.Errorf("malformed block: ======= divider without following newline")
	}
	afterDiv = afterDiv[nlIdx+1:]
	endIdx := strings.Index(afterDiv, replaceMarker)
	if endIdx < 0 {
		return srBlock{}, "", false, fmt.Errorf("malformed block: ======= without >>>>>>> REPLACE marker")
	}
	replaceText := strings.TrimRight(afterDiv[:endIdx], "\n")
	remainder = afterDiv[endIdx+len(replaceMarker):]
	return srBlock{Search: searchText, Replace: replaceText}, remainder, true, nil
}

// trimEnclosingMarkdownFence removes a single pair of markdown fences wrapping
// the entire response (some models wrap markdown output in ```markdown ... ```).
// It does NOT touch internal fenced code blocks within the document.
func trimEnclosingMarkdownFence(s string) string {
	trimmed := strings.TrimSpace(s)
	lines := strings.Split(trimmed, "\n")
	if len(lines) < 2 {
		return s
	}
	first := strings.TrimSpace(lines[0])
	last := strings.TrimSpace(lines[len(lines)-1])
	if strings.HasPrefix(first, "```") && last == "```" {
		return strings.Join(lines[1:len(lines)-1], "\n")
	}
	return s
}
