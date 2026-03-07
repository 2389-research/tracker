// ABOUTME: Dashboard header component showing pipeline name, elapsed time, and per-provider token counts.
// ABOUTME: Renders a single-line status bar at the top of the TUI dashboard.
package dashboard

import (
	"fmt"
	"strings"
	"time"

	"github.com/charmbracelet/lipgloss"

	"github.com/2389-research/tracker/llm"
)

var (
	headerStyle = lipgloss.NewStyle().
			Bold(true).
			Foreground(lipgloss.Color("15")).
			Background(lipgloss.Color("18")).
			Padding(0, 1)

	headerTitleStyle = lipgloss.NewStyle().
				Bold(true).
				Foreground(lipgloss.Color("14"))

	headerElapsedStyle = lipgloss.NewStyle().
				Foreground(lipgloss.Color("11"))

	headerTokenStyle = lipgloss.NewStyle().
				Foreground(lipgloss.Color("10"))

	headerSepStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("8"))
)

// HeaderModel renders the top status bar of the TUI dashboard.
type HeaderModel struct {
	pipelineName string
	startTime    time.Time
	tracker      *llm.TokenTracker
	width        int
}

// NewHeaderModel creates a header model with the given pipeline name and token tracker.
func NewHeaderModel(pipelineName string, tracker *llm.TokenTracker) HeaderModel {
	return HeaderModel{
		pipelineName: pipelineName,
		startTime:    time.Now(),
		tracker:      tracker,
	}
}

// SetWidth updates the terminal width used for layout.
func (h *HeaderModel) SetWidth(width int) {
	h.width = width
}

// SetStartTime overrides the start time (useful for testing and resume scenarios).
func (h *HeaderModel) SetStartTime(t time.Time) {
	h.startTime = t
}

// View renders the header as a single styled line.
func (h HeaderModel) View() string {
	elapsed := time.Since(h.startTime).Truncate(time.Second)

	var parts []string

	// Pipeline name
	name := h.pipelineName
	if name == "" {
		name = "pipeline"
	}
	parts = append(parts, headerTitleStyle.Render("⬡ "+name))

	// Elapsed time
	parts = append(parts, headerElapsedStyle.Render(fmt.Sprintf("⏱ %s", elapsed)))

	// Per-provider token counts
	if h.tracker != nil {
		for _, provider := range h.tracker.Providers() {
			usage := h.tracker.ProviderUsage(provider)
			tokenStr := fmt.Sprintf("%s: ↑%d ↓%d", provider, usage.InputTokens, usage.OutputTokens)
			parts = append(parts, headerTokenStyle.Render(tokenStr))
		}

		// Show total if multiple providers
		if len(h.tracker.Providers()) > 1 {
			total := h.tracker.TotalUsage()
			totalStr := fmt.Sprintf("total: ↑%d ↓%d", total.InputTokens, total.OutputTokens)
			parts = append(parts, headerTokenStyle.Render(totalStr))
		}
	}

	sep := headerSepStyle.Render("  │  ")
	line := strings.Join(parts, sep)

	if h.width > 0 {
		return headerStyle.Width(h.width).Render(line)
	}
	return headerStyle.Render(line)
}
