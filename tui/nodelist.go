// ABOUTME: NodeList component — signal lamp panel showing pipeline nodes with status and thinking animation.
// ABOUTME: Renders colored indicator lamps per node and shows spinner frames for thinking nodes.
package tui

import (
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
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

// SetSize updates both width and height for the node list viewport.
func (nl *NodeList) SetSize(w, h int) {
	nl.width = w
	nl.height = h
}

// Update handles messages for the NodeList component.
func (nl *NodeList) Update(msg tea.Msg) tea.Cmd {
	return nil
}

// View renders the node list as a signal lamp panel, clipped to the configured height.
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

	// Build all node lines.
	var lines []string
	for _, node := range nodes {
		status := nl.store.NodeStatus(node.ID)
		lamp, style := StatusLamp(status)

		// Override lamp based on activity phase for running nodes.
		if status == NodeRunning {
			if nl.thinking.IsToolRunning(node.ID) {
				lamp = "⚡"
				style = lipgloss.NewStyle().Foreground(ColorBash).Bold(true)
			} else if nl.thinking.IsThinking(node.ID) {
				frame := nl.thinking.Frame(node.ID)
				if frame != "" {
					lamp = frame
					style = lipgloss.NewStyle().Foreground(ColorRunning).Bold(true)
				}
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

		// Show activity context for running nodes.
		if status == NodeRunning {
			if toolName := nl.thinking.ToolName(node.ID); toolName != "" {
				line += " " + Styles.Muted.Render(toolName)
			} else if nl.thinking.IsThinking(node.ID) {
				elapsed := nl.thinking.Elapsed(node.ID).Truncate(time.Second)
				line += " " + Styles.Muted.Render(elapsed.String())
			}
		}

		// Show error for failed nodes.
		if status == NodeFailed {
			errMsg := nl.store.NodeError(node.ID)
			if errMsg != "" {
				line += " " + Styles.Error.Render(errMsg)
			}
		}

		lines = append(lines, line)
	}

	// Clip lines to the configured height.
	if nl.height > 0 && len(lines) > nl.height {
		lines = lines[:nl.height]
	}
	for _, l := range lines {
		sb.WriteString(l)
		sb.WriteString("\n")
	}

	return sb.String()
}
