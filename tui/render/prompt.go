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
