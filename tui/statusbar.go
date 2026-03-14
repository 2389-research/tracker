// ABOUTME: StatusBar component — bottom bar with track diagram, progress, and keybinding hints.
// ABOUTME: Renders colored lamp glyphs per node and a compact progress summary.
package tui

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/lipgloss"
)

// StatusBar renders the bottom status bar of the TUI dashboard.
type StatusBar struct {
	store *StateStore
	width int
}

// NewStatusBar creates a StatusBar reading state from the given store.
func NewStatusBar(store *StateStore) *StatusBar {
	return &StatusBar{store: store}
}

// SetWidth updates the terminal width used for layout.
func (sb *StatusBar) SetWidth(w int) { sb.width = w }

// View renders the status bar as a single line: track diagram + progress + hints.
func (sb *StatusBar) View() string {
	track := sb.trackDiagram()
	progress := sb.progressSummary()
	hints := Styles.Muted.Render("ctrl+o expand  q quit")

	left := track + "  " + progress
	right := hints

	gap := sb.width - lipgloss.Width(left) - lipgloss.Width(right)
	if gap < 1 {
		gap = 1
	}

	line := left + fmt.Sprintf("%*s", gap, "") + right
	return Styles.StatusBar.Render(line)
}

// trackDiagram renders a compact glyph strip: one lamp per node connected by dashes.
func (sb *StatusBar) trackDiagram() string {
	nodes := sb.store.Nodes()
	if len(nodes) == 0 {
		return ""
	}

	connector := Styles.DimText.Render("━")
	var parts []string
	for _, node := range nodes {
		lamp, style := statusLamp(sb.store.NodeStatus(node.ID))
		parts = append(parts, style.Render(lamp))
	}
	return strings.Join(parts, connector)
}

// progressSummary renders "done/total" with the primary text style.
func (sb *StatusBar) progressSummary() string {
	done, total := sb.store.Progress()
	return Styles.PrimaryText.Render(fmt.Sprintf("%d/%d", done, total))
}

// statusLamp returns the lamp character and color style for a node state.
func statusLamp(state NodeState) (string, lipgloss.Style) {
	switch state {
	case NodeDone:
		return LampDone, lipgloss.NewStyle().Foreground(ColorDone)
	case NodeRunning:
		return LampRunning, lipgloss.NewStyle().Foreground(ColorRunning).Bold(true)
	case NodeFailed:
		return LampFailed, lipgloss.NewStyle().Foreground(ColorFailed)
	default:
		return LampPending, lipgloss.NewStyle().Foreground(ColorPending)
	}
}
