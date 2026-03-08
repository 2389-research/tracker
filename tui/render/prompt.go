// ABOUTME: Shared prompt rendering for human gate UIs.
// ABOUTME: Renders markdown content with word wrapping for terminal display.
package render

import (
	"strings"

	"github.com/charmbracelet/glamour"
)

// Prompt renders a prompt string as markdown with word wrapping to fit the
// given terminal width. Used by both bubbletea components and the console
// interviewer to give humans formatted, readable context from prior LLM nodes.
func Prompt(prompt string, width int) string {
	if strings.TrimSpace(prompt) == "" {
		return ""
	}

	if width <= 0 {
		width = 80
	}

	renderer, err := glamour.NewTermRenderer(
		glamour.WithAutoStyle(),
		glamour.WithWordWrap(width),
	)
	if err != nil {
		return prompt
	}

	rendered, err := renderer.Render(prompt)
	if err != nil {
		return prompt
	}

	return strings.TrimRight(rendered, "\n")
}

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
		lines = append(lines, wrapLine(paragraph, width)...)
	}
	return strings.Join(lines, "\n")
}

// wrapLine splits a single paragraph into lines that fit within width.
func wrapLine(line string, width int) []string {
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
