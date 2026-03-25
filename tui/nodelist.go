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

	var lines []string
	for _, node := range nodes {
		lines = append(lines, nl.renderNodeLine(node))
	}

	if nl.height > 0 && len(lines) > nl.height {
		lines = lines[:nl.height]
	}
	for _, l := range lines {
		sb.WriteString(l)
		sb.WriteString("\n")
	}

	return sb.String()
}

// renderNodeLine builds the display line for a single node entry.
func (nl *NodeList) renderNodeLine(node NodeEntry) string {
	status := nl.store.NodeStatus(node.ID)
	lamp, style := nl.resolveLamp(node.ID, status)

	label := node.Label
	if label == "" {
		label = node.ID
	}

	indent := ""
	if IsSubgraphNode(node.ID) {
		depth := SubgraphDepth(node.ID)
		indent = strings.Repeat("  ", depth)
		if label == node.ID {
			label = SubgraphChildLabel(node.ID)
		}
	}

	maxLabel := nl.width - 4 - len(indent)
	if maxLabel > 0 && len(label) > maxLabel {
		label = label[:maxLabel-1] + "…"
	}

	line := indent + style.Render(lamp) + " " + Styles.PrimaryText.Render(label)
	line += nl.nodeStatusSuffix(node.ID, status)
	return line
}

// resolveLamp returns the lamp icon and style for a node, overriding for active phases.
func (nl *NodeList) resolveLamp(nodeID string, status NodeState) (string, lipgloss.Style) {
	lamp, style := StatusLamp(status)
	if status != NodeRunning {
		return lamp, style
	}
	if nl.thinking.IsToolRunning(nodeID) {
		return "⚡", lipgloss.NewStyle().Foreground(ColorBash).Bold(true)
	}
	if nl.thinking.IsThinking(nodeID) {
		if frame := nl.thinking.Frame(nodeID); frame != "" {
			return frame, lipgloss.NewStyle().Foreground(ColorRunning).Bold(true)
		}
	}
	return lamp, style
}

// nodeStatusSuffix returns trailing text for running/failed/retrying nodes.
func (nl *NodeList) nodeStatusSuffix(nodeID string, status NodeState) string {
	switch status {
	case NodeRunning:
		if toolName := nl.thinking.ToolName(nodeID); toolName != "" {
			return " " + Styles.Muted.Render(toolName)
		}
		if nl.thinking.IsThinking(nodeID) {
			elapsed := nl.thinking.Elapsed(nodeID).Truncate(time.Second)
			return " " + Styles.Muted.Render(elapsed.String())
		}
	case NodeFailed:
		if errMsg := nl.store.NodeError(nodeID); errMsg != "" {
			return " " + Styles.Error.Render(errMsg)
		}
	case NodeRetrying:
		if retryMsg := nl.store.NodeRetryMessage(nodeID); retryMsg != "" {
			return " " + Styles.Muted.Render(retryMsg)
		}
	}
	return ""
}
