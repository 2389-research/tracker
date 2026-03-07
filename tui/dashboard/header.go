// ABOUTME: Dashboard header — main gauge cluster showing pipeline name, elapsed time, status, and token readouts.
// ABOUTME: "Signal Cabin" aesthetic: high-contrast readouts, amber/green status, ALL-CAPS zone label.
package dashboard

import (
	"fmt"
	"strings"
	"time"

	"github.com/charmbracelet/lipgloss"

	"github.com/2389-research/tracker/llm"
)

// PipelineStatus represents the overall pipeline state shown in the header.
type PipelineStatus int

const (
	StatusRunning PipelineStatus = iota
	StatusCompleted
	StatusFailed
)

// HeaderModel renders the top gauge cluster of the TUI dashboard.
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

// View renders the header as a two-line instrument cluster.
// Line 1: TRACKER ━━ pipeline_name    ⏱ Xm XXs    STATUS
// Line 2: PROVIDER: in/out  PROVIDER: in/out  ...
func (h HeaderModel) View() string {
	elapsed := time.Since(h.startTime).Truncate(time.Second)

	// ── Line 1: name + time + status ──
	name := h.pipelineName
	if name == "" {
		name = "pipeline"
	}

	nameStr := lipgloss.NewStyle().Foreground(colorBrightText).Bold(true).Render(
		"TRACKER " + dimTextStyle.Render("━━") + " " + name,
	)
	timeStr := readoutStyle.Render(fmt.Sprintf("⏱ %s", formatDuration(elapsed)))
	statusStr := h.renderStatus()

	// Build line 1 with spacing
	line1Left := nameStr + "    " + timeStr
	line1 := line1Left + "    " + statusStr

	// ── Line 2: token readouts ──
	line2 := h.renderTokenLine()

	combined := line1 + "\n" + line2
	if h.width > 0 {
		return lipgloss.NewStyle().
			Background(colorPanel).
			Padding(0, 1).
			Width(h.width).
			Render(combined)
	}
	return lipgloss.NewStyle().
		Background(colorPanel).
		Padding(0, 1).
		Render(combined)
}

// renderStatus returns a signal-lamp style status indicator.
func (h HeaderModel) renderStatus() string {
	switch h.status {
	case StatusCompleted:
		return lipgloss.NewStyle().Foreground(colorGreen).Bold(true).Render(lampOn + " COMPLETED")
	case StatusFailed:
		return lipgloss.NewStyle().Foreground(colorRed).Bold(true).Render(lampError + " FAILED")
	default:
		return lipgloss.NewStyle().Foreground(colorAmber).Bold(true).Render(lampActive + " RUNNING")
	}
}

// renderTokenLine builds the second line: per-provider token readouts.
func (h HeaderModel) renderTokenLine() string {
	if h.tracker == nil {
		return dimTextStyle.Render("awaiting LLM calls")
	}

	providers := h.tracker.Providers()
	if len(providers) == 0 {
		return dimTextStyle.Render("awaiting LLM calls")
	}

	var parts []string
	for _, provider := range providers {
		usage := h.tracker.ProviderUsage(provider)
		label := zoneLabelStyle.Render(strings.ToUpper(provider))
		counts := readoutStyle.Render(fmt.Sprintf("%s/%s",
			formatTokenCount(usage.InputTokens),
			formatTokenCount(usage.OutputTokens),
		))
		parts = append(parts, label+" "+counts)
	}

	// Cost readout if available
	total := h.tracker.TotalUsage()
	if total.EstimatedCost > 0 {
		parts = append(parts, lipgloss.NewStyle().Foreground(colorAmber).Render(
			fmt.Sprintf("$%.2f", total.EstimatedCost),
		))
	}

	return strings.Join(parts, dimTextStyle.Render("  "))
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
