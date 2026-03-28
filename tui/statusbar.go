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
	store    *StateStore
	width    int
	progress *ProgressTracker
	agentLog *AgentLog // for reading verbosity level
	flash    string    // temporary flash message (e.g., "Copied!")
}

// NewStatusBar creates a StatusBar reading state from the given store.
func NewStatusBar(store *StateStore, agentLog *AgentLog) *StatusBar {
	return &StatusBar{
		store:    store,
		progress: NewProgressTracker(store),
		agentLog: agentLog,
	}
}

// SetFlash sets a temporary message in the status bar.
func (sb *StatusBar) SetFlash(text string) { sb.flash = text }

// ClearFlash removes the flash message.
func (sb *StatusBar) ClearFlash() { sb.flash = "" }

// Progress returns the progress tracker for recording node durations.
func (sb *StatusBar) Progress() *ProgressTracker { return sb.progress }

// SetWidth updates the terminal width used for layout.
func (sb *StatusBar) SetWidth(w int) { sb.width = w }

// View renders the status bar as a single line: progress bar + badges + hints.
func (sb *StatusBar) View() string {
	// Feed node durations to the progress tracker for ETA calculation.
	sb.syncProgressDurations()

	// Use progress bar when there are running nodes, fall back to track diagram.
	done, total := sb.store.Progress()
	var left string
	if total > 0 && done < total && sb.hasRunningNode() {
		sb.progress.SetWidth(sb.width / 3)
		left = sb.progress.View()
	} else {
		left = sb.trackDiagram() + "  " + sb.progressSummary()
	}

	// Verbosity badge.
	if sb.agentLog != nil && sb.agentLog.Verbosity() != VerbosityAll {
		left += "  " + Styles.VerbosityBadge.Render("["+sb.agentLog.Verbosity().Label()+"]")
	}

	// Flash message.
	if sb.flash != "" {
		left += "  " + lipgloss.NewStyle().Foreground(ColorGreen).Bold(true).Render(sb.flash)
	}

	hints := Styles.Muted.Render("v filter  z zen  / search  ? help  q quit")
	right := hints

	gap := sb.width - lipgloss.Width(left) - lipgloss.Width(right)
	if gap < 1 {
		gap = 1
	}

	line := left + fmt.Sprintf("%*s", gap, "") + right
	return Styles.StatusBar.Render(line)
}

// syncProgressDurations feeds completed node durations to the progress tracker.
func (sb *StatusBar) syncProgressDurations() {
	needed := len(sb.store.Nodes()) // upper bound
	if len(sb.progress.durations) >= needed {
		return // already synced
	}
	sb.progress.durations = sb.progress.durations[:0]
	for _, n := range sb.store.Nodes() {
		d := sb.store.NodeDuration(n.ID)
		if d > 0 {
			sb.progress.durations = append(sb.progress.durations, d)
		}
	}
}

// hasRunningNode returns true if any node is currently running.
func (sb *StatusBar) hasRunningNode() bool {
	for _, n := range sb.store.Nodes() {
		if sb.store.NodeStatus(n.ID) == NodeRunning {
			return true
		}
	}
	return false
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
		lamp, style := StatusLamp(sb.store.NodeStatus(node.ID))
		parts = append(parts, style.Render(lamp))
	}
	return strings.Join(parts, connector)
}

// progressSummary renders "done/total" with the primary text style.
func (sb *StatusBar) progressSummary() string {
	done, total := sb.store.Progress()
	return Styles.PrimaryText.Render(fmt.Sprintf("%d/%d", done, total))
}
