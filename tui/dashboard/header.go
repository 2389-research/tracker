// ABOUTME: Dashboard header component showing pipeline name, elapsed time, status, and per-provider token counts.
// ABOUTME: Renders a two-line status bar at the top of the TUI dashboard with formatted token counts (e.g., 12.4k/3.2k).
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
			Background(lipgloss.Color("18")).
			Padding(0, 1)

	headerTitleStyle = lipgloss.NewStyle().
				Bold(true).
				Foreground(lipgloss.Color("14"))

	headerElapsedStyle = lipgloss.NewStyle().
				Foreground(lipgloss.Color("11"))

	headerStatusRunning = lipgloss.NewStyle().
				Foreground(lipgloss.Color("11")).
				Bold(true)

	headerStatusDone = lipgloss.NewStyle().
				Foreground(lipgloss.Color("10")).
				Bold(true)

	headerStatusFailed = lipgloss.NewStyle().
				Foreground(lipgloss.Color("9")).
				Bold(true)

	headerTokenStyle = lipgloss.NewStyle().
				Foreground(lipgloss.Color("10"))

	headerSepStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("8"))

	headerCostStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("13"))
)

// PipelineStatus represents the overall pipeline state shown in the header.
type PipelineStatus int

const (
	StatusRunning PipelineStatus = iota
	StatusCompleted
	StatusFailed
)

// HeaderModel renders the top status bar of the TUI dashboard.
type HeaderModel struct {
	pipelineName string
	startTime    time.Time
	tracker      *llm.TokenTracker
	width        int
	status       PipelineStatus
}

// NewHeaderModel creates a header model with the given pipeline name and token tracker.
func NewHeaderModel(pipelineName string, tracker *llm.TokenTracker) HeaderModel {
	return HeaderModel{
		pipelineName: pipelineName,
		startTime:    time.Now(),
		tracker:      tracker,
		status:       StatusRunning,
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

// SetStatus updates the pipeline status shown in the header.
func (h *HeaderModel) SetStatus(status PipelineStatus) {
	h.status = status
}

// View renders the header as a two-line styled block.
// Line 1: pipeline name, elapsed time, status
// Line 2: per-provider token counts and cost
func (h HeaderModel) View() string {
	elapsed := time.Since(h.startTime).Truncate(time.Second)

	// Line 1: name + elapsed + status
	var line1Parts []string

	name := h.pipelineName
	if name == "" {
		name = "pipeline"
	}
	line1Parts = append(line1Parts, headerTitleStyle.Render(name))
	line1Parts = append(line1Parts, headerElapsedStyle.Render(fmt.Sprintf("⏱ %s", formatDuration(elapsed))))
	line1Parts = append(line1Parts, h.renderStatus())

	sep := headerSepStyle.Render("  ")
	line1 := strings.Join(line1Parts, sep)

	// Line 2: per-provider token counts
	line2 := h.renderTokenLine()

	combined := line1 + "\n" + line2
	if h.width > 0 {
		return headerStyle.Width(h.width).Render(combined)
	}
	return headerStyle.Render(combined)
}

// renderStatus returns a styled status indicator.
func (h HeaderModel) renderStatus() string {
	switch h.status {
	case StatusCompleted:
		return headerStatusDone.Render("completed")
	case StatusFailed:
		return headerStatusFailed.Render("failed")
	default:
		return headerStatusRunning.Render("running")
	}
}

// renderTokenLine builds the second header line with per-provider token counts.
func (h HeaderModel) renderTokenLine() string {
	if h.tracker == nil {
		return headerTokenStyle.Render("(no token data)")
	}

	providers := h.tracker.Providers()
	if len(providers) == 0 {
		return headerTokenStyle.Render("(awaiting LLM calls)")
	}

	var parts []string
	for _, provider := range providers {
		usage := h.tracker.ProviderUsage(provider)
		tokenStr := fmt.Sprintf("%s: %s/%s",
			capitalizeFirst(provider),
			formatTokenCount(usage.InputTokens),
			formatTokenCount(usage.OutputTokens),
		)
		parts = append(parts, headerTokenStyle.Render(tokenStr))
	}

	// Add total cost if available
	total := h.tracker.TotalUsage()
	if total.EstimatedCost > 0 {
		parts = append(parts, headerCostStyle.Render(fmt.Sprintf("$%.2f", total.EstimatedCost)))
	}

	sep := headerSepStyle.Render("  ")
	return strings.Join(parts, sep)
}

// formatTokenCount formats a token count for display.
// < 1000: show as-is (e.g., "842")
// >= 1000: show as X.Xk (e.g., "12.4k")
// >= 1000000: show as X.Xm (e.g., "1.2m")
func formatTokenCount(count int) string {
	if count < 1000 {
		return fmt.Sprintf("%d", count)
	}
	if count < 1000000 {
		v := float64(count) / 1000.0
		return fmt.Sprintf("%.1fk", v)
	}
	v := float64(count) / 1000000.0
	return fmt.Sprintf("%.1fm", v)
}

// formatDuration formats a duration for the header display.
func formatDuration(d time.Duration) string {
	if d < time.Minute {
		return fmt.Sprintf("%ds", int(d.Seconds()))
	}
	minutes := int(d.Minutes())
	seconds := int(d.Seconds()) % 60
	return fmt.Sprintf("%dm%02ds", minutes, seconds)
}

// capitalizeFirst capitalizes the first letter of a string.
func capitalizeFirst(s string) string {
	if s == "" {
		return s
	}
	return strings.ToUpper(s[:1]) + s[1:]
}
