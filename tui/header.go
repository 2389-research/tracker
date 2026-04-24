// ABOUTME: Header component — displays pipeline name, run ID, elapsed time, and token/cost readout.
// ABOUTME: Self-contained Bubbletea model with its own Update/View. Reads token data from StateStore.
package tui

import (
	"fmt"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

// Header renders the top bar of the TUI dashboard.
type Header struct {
	store        *StateStore
	pipelineName string
	runID        string
	backend      string // "claude-code", "native", or ""
	autopilot    string // persona name or ""
	startedAt    time.Time
	width        int
}

// NewHeader creates a Header with the given state, pipeline name, and run ID.
func NewHeader(store *StateStore, pipelineName, runID string) *Header {
	return &Header{
		store:        store,
		pipelineName: pipelineName,
		runID:        runID,
		startedAt:    time.Now(),
	}
}

// SetBackend sets the backend tag displayed in the header.
func (h *Header) SetBackend(backend string) { h.backend = backend }

// SetAutopilot sets the autopilot persona tag displayed in the header.
func (h *Header) SetAutopilot(persona string) { h.autopilot = persona }

// Update handles tick messages for the header.
func (h *Header) Update(msg tea.Msg) tea.Cmd {
	switch msg.(type) {
	case MsgHeaderTick:
		return nil
	}
	return nil
}

// SetWidth updates the terminal width used for layout.
func (h *Header) SetWidth(w int) { h.width = w }

// View renders the header as a single-line instrument cluster.
func (h *Header) View() string {
	elapsed := time.Since(h.startedAt).Truncate(time.Second)

	left := h.buildLeftSegment()
	right := h.buildRightSegment(elapsed)

	gap := h.width - lipgloss.Width(left) - lipgloss.Width(right)
	if gap < 1 {
		gap = 1
	}
	return left + fmt.Sprintf("%*s", gap, "") + right
}

// buildLeftSegment constructs the pipeline name + run ID + mode tags portion.
func (h *Header) buildLeftSegment() string {
	left := Styles.Header.Render(h.pipelineName) + "  " + Styles.Muted.Render(h.runID)

	tagStyle := lipgloss.NewStyle().
		Foreground(lipgloss.Color("0")).
		Background(lipgloss.Color("208")).
		Padding(0, 1).
		Bold(true)
	if h.backend != "" && h.backend != "native" {
		left += "  " + tagStyle.Render(h.backend)
	}
	if h.autopilot != "" {
		left += "  " + tagStyle.Background(lipgloss.Color("63")).Render("autopilot:"+h.autopilot)
	}
	return left
}

// buildRightSegment constructs the elapsed time + token/cost portion.
func (h *Header) buildRightSegment(elapsed time.Duration) string {
	right := Styles.Muted.Render(formatDuration(elapsed))
	if h.store == nil || h.store.Tokens == nil {
		return right
	}
	usage := h.store.Tokens.TotalUsage()
	if usage.TotalTokens > 0 {
		right = Styles.Readout.Render(formatTokenCount(usage.TotalTokens)+"t") + "  " + right
	}
	if usage.EstimatedCost > 0 {
		costLabel := fmt.Sprintf("$%.2f", usage.EstimatedCost)
		if h.isClaudeCodeOnly() || h.hasEstimatedProvider() {
			// Claude Code Max subscription is flat-rate (no actual charge),
			// and the ACP backend reports heuristic rune-count estimates;
			// both cases render the cost with the "~$X usage" marker so
			// operators don't read the header figure as a metered total.
			costLabel = fmt.Sprintf("~$%.2f usage", usage.EstimatedCost)
		}
		right = lipgloss.NewStyle().Foreground(ColorAmber).Render(costLabel) + "  " + right
	}
	return right
}

// isClaudeCodeOnly returns true if all token usage is from the claude-code provider.
func (h *Header) isClaudeCodeOnly() bool {
	if h.store == nil || h.store.Tokens == nil {
		return false
	}
	providers := h.store.Tokens.Providers()
	return len(providers) == 1 && providers[0] == "claude-code"
}

// hasEstimatedProvider returns true if any provider contributing to the
// running token total reports heuristic-derived (non-metered) usage.
// Today: the ACP backend — its per-prompt token counts are rune-based
// estimates, and mixing them with real provider spend in the header would
// silently misrepresent the mixed figure as metered. Future estimated
// providers should be added to this check.
func (h *Header) hasEstimatedProvider() bool {
	if h.store == nil || h.store.Tokens == nil {
		return false
	}
	for _, p := range h.store.Tokens.Providers() {
		if p == "acp" {
			return true
		}
	}
	return false
}

// formatTokenCount formats a token count for display.
func formatTokenCount(count int) string {
	if count < 1000 {
		return fmt.Sprintf("%d", count)
	}
	if count < 1000000 {
		return fmt.Sprintf("%.1fk", float64(count)/1000.0)
	}
	return fmt.Sprintf("%.1fm", float64(count)/1000000.0)
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
