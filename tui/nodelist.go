// ABOUTME: NodeList component — signal lamp panel showing pipeline nodes with status and thinking animation.
// ABOUTME: Renders colored indicator lamps per node and shows spinner frames for thinking nodes.
package tui

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/lipgloss"
)

// NodeList renders a signal lamp panel of pipeline nodes.
type NodeList struct {
	store    *StateStore
	thinking *ThinkingTracker
	height   int
	width    int
}

// NewNodeList creates a NodeList that reads from the given state store and thinking tracker.
func NewNodeList(store *StateStore, thinking *ThinkingTracker, height int) *NodeList {
	return &NodeList{
		store:    store,
		thinking: thinking,
		height:   height,
	}
}

// SetWidth updates the terminal width used for rendering.
func (nl *NodeList) SetWidth(w int) { nl.width = w }

// SetHeight updates the viewport height.
func (nl *NodeList) SetHeight(h int) { nl.height = h }

// View renders the node list as a signal lamp panel.
func (nl *NodeList) View() string {
	var sb strings.Builder
	sb.WriteString(Styles.ZoneLabel.Render("PIPELINE"))
	sb.WriteString("\n")

	nodes := nl.store.Nodes()
	if len(nodes) == 0 {
		sb.WriteString(Styles.DimText.Render("(no nodes)"))
		sb.WriteString("\n")
		return sb.String()
	}

	for _, node := range nodes {
		status := nl.store.NodeStatus(node.ID)
		lamp, style := nodeLamp(status)

		// Override lamp with thinking animation frame when the node is thinking.
		if status == NodeRunning && nl.thinking.IsThinking(node.ID) {
			frame := nl.thinking.Frame(node.ID)
			if frame != "" {
				lamp = frame
				style = lipgloss.NewStyle().Foreground(ColorRunning).Bold(true)
			}
		}

		label := node.Label
		if label == "" {
			label = node.ID
		}

		// Truncate long labels.
		maxLabel := nl.width - 4
		if maxLabel > 0 && len(label) > maxLabel {
			label = label[:maxLabel-1] + "…"
		}

		line := style.Render(lamp) + " " + Styles.PrimaryText.Render(label)

		// Show elapsed thinking time for running nodes.
		if status == NodeRunning && nl.thinking.IsThinking(node.ID) {
			elapsed := nl.thinking.Elapsed(node.ID).Truncate(1e9)
			line += " " + Styles.Muted.Render(fmt.Sprintf("%s", elapsed))
		}

		// Show error for failed nodes.
		if status == NodeFailed {
			errMsg := nl.store.NodeError(node.ID)
			if errMsg != "" {
				line += " " + Styles.Error.Render(errMsg)
			}
		}

		sb.WriteString(line)
		sb.WriteString("\n")
	}

	return sb.String()
}

// nodeLamp returns the indicator character and style for a node status.
func nodeLamp(status NodeState) (string, lipgloss.Style) {
	switch status {
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
