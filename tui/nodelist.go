// ABOUTME: NodeList component — signal lamp panel showing pipeline nodes with status and thinking animation.
// ABOUTME: Renders colored indicator lamps per node and shows spinner frames for thinking nodes.
package tui

import (
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

// indexedLine pairs a rendered line with the node index it belongs to.
type indexedLine struct {
	line    string
	nodeIdx int // which node this line belongs to (-1 for connectors)
}

// NodeList renders a signal lamp panel of pipeline nodes.
type NodeList struct {
	store    *StateStore
	thinking *ThinkingTracker
	height   int
	width    int
	scroll   int // scroll offset (first visible node index)
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

	// Build lines with connectors between visited nodes.
	var all []indexedLine
	for i, node := range nodes {
		if i > 0 {
			connector := nl.renderConnector(nodes[i-1].ID, node.ID)
			if connector != "" {
				all = append(all, indexedLine{connector, -1})
			}
		}
		all = append(all, indexedLine{nl.renderNodeLine(node), i})
	}

	// Auto-scroll to keep the running node visible.
	nl.autoScroll(nodes, all)

	// Apply scroll window.
	visible := all
	if nl.height > 0 && len(visible) > nl.height {
		end := nl.scroll + nl.height
		if end > len(visible) {
			end = len(visible)
		}
		visible = visible[nl.scroll:end]
	}

	for _, l := range visible {
		sb.WriteString(l.line)
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

// autoScroll adjusts the scroll offset to keep the running node visible.
func (nl *NodeList) autoScroll(nodes []NodeEntry, lines []indexedLine) {
	if nl.height <= 0 {
		return
	}
	// Find the line index of the running node.
	runningLine := -1
	for i, l := range lines {
		if l.nodeIdx >= 0 && l.nodeIdx < len(nodes) {
			status := nl.store.NodeStatus(nodes[l.nodeIdx].ID)
			if status == NodeRunning {
				runningLine = i
				break
			}
		}
	}
	if runningLine < 0 {
		return
	}
	// Ensure running node is within the visible window.
	if runningLine < nl.scroll {
		nl.scroll = runningLine
	}
	if runningLine >= nl.scroll+nl.height {
		nl.scroll = runningLine - nl.height + 1
	}
	if nl.scroll < 0 {
		nl.scroll = 0
	}
}

// renderConnector draws a vertical connector line between two nodes if both are on the visit path.
func (nl *NodeList) renderConnector(prevID, nextID string) string {
	prevOnPath := nl.store.IsOnCurrentPath(prevID)
	nextOnPath := nl.store.IsOnCurrentPath(nextID)
	if !prevOnPath && !nextOnPath {
		return ""
	}
	connStyle := Styles.DimText
	if prevOnPath && nextOnPath {
		connStyle = lipgloss.NewStyle().Foreground(ColorDone)
	}
	return connStyle.Render("│")
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
