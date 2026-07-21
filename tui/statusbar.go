// ABOUTME: StatusBar component — bottom bar with track diagram, progress, and keybinding hints.
// ABOUTME: Renders colored lamp glyphs per node and a compact progress summary.
package tui

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/lipgloss"

	"github.com/2389-research/tracker/pipeline"
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
// When the pipeline has reached a terminal state, the progress region is
// replaced by CompletionRow so operators see the final status (and any
// override gate/label/actor detail) inline.
func (sb *StatusBar) View() string {
	// Feed node durations to the progress tracker for ETA calculation.
	sb.syncProgressDurations()

	left := sb.buildLeftSegment()

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

// buildLeftSegment picks the leftmost status region: completion row when the
// pipeline has finished (so the terminal status is visible at-a-glance),
// progress bar while nodes are still running, otherwise the static track
// diagram + done/total summary.
func (sb *StatusBar) buildLeftSegment() string {
	if sb.store.PipelineDone() {
		return CompletionRow(sb.store.PipelineStatus(), sb.store.HeadlineOverride(), sb.store.PipelineError())
	}
	done, total := sb.store.Progress()
	if total > 0 && done < total && sb.hasRunningNode() {
		sb.progress.SetWidth(sb.width / 3)
		return sb.progress.View()
	}
	return sb.trackDiagram() + "  " + sb.progressSummary()
}

// CompletionRow renders the terminal-status banner shown in the status bar
// once the pipeline has reached a final state. The bullet glyph and textual
// status string carry the same semantic signal as the color, so NO_COLOR /
// monochrome terminals still distinguish the four states (Gap 5.2 spec D17
// + D18). Override is non-nil only for validation_overridden runs; pipelineErr
// supplies the failed-at-node detail when present.
//
// Status branches:
//   - OutcomeSuccess:               green ● Completed
//   - OutcomeValidationOverridden:  amber ● Completed — validation override at <gate> (label "<label>" by <actor>)
//   - OutcomeBudgetExceeded:        red   ✗ Budget exceeded
//   - OutcomeFail:                  red   ✗ Failed[: <error>]
//   - unknown/zero status:          dim   ● Completed (defensive default)
func CompletionRow(status pipeline.TerminalStatus, override *pipeline.OverrideDetail, pipelineErr string) string {
	switch status {
	case pipeline.OutcomeValidationOverridden:
		return renderOverrideCompletion(override)
	case pipeline.OutcomeBudgetExceeded:
		style := lipgloss.NewStyle().Foreground(ColorRed).Bold(true)
		return style.Render(LampFailed + " Budget exceeded")
	case pipeline.OutcomePausedBilling:
		// Amber, not red: a recoverable pause the user can resume, not a failure.
		style := lipgloss.NewStyle().Foreground(ColorAmber).Bold(true)
		return style.Render("⏸ Paused — billing/quota (add credit, then resume)")
	case pipeline.OutcomeFail:
		style := lipgloss.NewStyle().Foreground(ColorRed).Bold(true)
		text := LampFailed + " Failed"
		if pipelineErr != "" {
			text += ": " + pipelineErr
		}
		return style.Render(text)
	case pipeline.OutcomeSuccess:
		style := lipgloss.NewStyle().Foreground(ColorGreen).Bold(true)
		return style.Render(LampDone + " Completed")
	default:
		// Unknown / zero status — render a neutral default so the user still
		// sees that the run ended. This branch fires for legacy stateless-adapter
		// completions where status wasn't computed.
		return Styles.DimText.Render(LampDone + " Completed")
	}
}

// renderOverrideCompletion formats the amber "validation override at <gate>
// (label \"<label>\" by <actor>)" line. The bullet glyph + textual status
// keeps the signal in NO_COLOR terminals; the amber tint is for sighted-color
// terminals (spec D18). When override is nil (defensive — shouldn't happen
// for OutcomeValidationOverridden in practice), falls back to the bare
// "validation override" suffix.
func renderOverrideCompletion(override *pipeline.OverrideDetail) string {
	style := lipgloss.NewStyle().Foreground(ColorOverride).Bold(true)
	if override == nil {
		return style.Render(LampDone + " Completed — validation override")
	}
	text := fmt.Sprintf("%s Completed — validation override at %s (label %q by %s)",
		LampDone, override.GateNodeID, override.Label, override.Actor)
	return style.Render(text)
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
