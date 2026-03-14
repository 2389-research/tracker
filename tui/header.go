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

	left := Styles.Header.Render(h.pipelineName) + "  " + Styles.Muted.Render(h.runID)
	right := Styles.Muted.Render(formatDuration(elapsed))

	if h.store != nil && h.store.Tokens != nil {
		usage := h.store.Tokens.TotalUsage()
		if usage.TotalTokens > 0 {
			tokenStr := formatTokenCount(usage.TotalTokens) + "t"
			right = Styles.Readout.Render(tokenStr) + "  " + right
		}
		if usage.EstimatedCost > 0 {
			costStr := fmt.Sprintf("$%.2f", usage.EstimatedCost)
			right = lipgloss.NewStyle().Foreground(ColorAmber).Render(costStr) + "  " + right
		}
	}

	gap := h.width - lipgloss.Width(left) - lipgloss.Width(right)
	if gap < 1 {
		gap = 1
	}
	return left + fmt.Sprintf("%*s", gap, "") + right
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
