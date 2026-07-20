// ABOUTME: Plain-text prompt rendering for human gate UIs.
// ABOUTME: Lives in handlers (not tui/) so the core has no dependency on the TUI package.
package handlers

import "strings"

// PromptPlain renders a prompt string with word wrapping but no ANSI styling.
// Used by ConsoleInterviewer so piped/CI output stays free of escape sequences.
func PromptPlain(prompt string, width int) string {
	if strings.TrimSpace(prompt) == "" {
		return ""
	}
	if width <= 0 {
		width = 80
	}

	var lines []string
	for _, paragraph := range strings.Split(prompt, "\n") {
		if strings.TrimSpace(paragraph) == "" {
			lines = append(lines, "")
			continue
		}
		lines = append(lines, wrapPromptLine(paragraph, width)...)
	}
	return strings.Join(lines, "\n")
}

// wrapPromptLine splits a single paragraph into lines that fit within width.
func wrapPromptLine(line string, width int) []string {
	words := strings.Fields(line)
	if len(words) == 0 {
		return []string{""}
	}
	var lines []string
	current := words[0]
	for _, word := range words[1:] {
		if len(current)+1+len(word) > width {
			lines = append(lines, current)
			current = word
		} else {
			current += " " + word
		}
	}
	lines = append(lines, current)
	return lines
}
