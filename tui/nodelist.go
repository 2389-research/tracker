// ABOUTME: NodeList component — signal lamp panel showing pipeline nodes with status and thinking animation.
// ABOUTME: Renders colored indicator lamps per node and shows spinner frames for thinking nodes.
package tui

import (
	"fmt"
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
	store       *StateStore
	thinking    *ThinkingTracker
	height      int
	width       int
	scroll      int // scroll offset (first visible node index)
	selectedIdx int // cursor position for drill-down navigation (-1 = no selection)
}

// NewNodeList creates a NodeList that reads from the given state store and thinking tracker.
func NewNodeList(store *StateStore, thinking *ThinkingTracker, height int) *NodeList {
	return &NodeList{
		store:       store,
		thinking:    thinking,
		height:      height,
		selectedIdx: -1,
	}
}

// SetSize updates both width and height for the node list viewport.
func (nl *NodeList) SetSize(w, h int) {
	nl.width = w
	nl.height = h
}

// Init implements tea.Model.
func (nl *NodeList) Init() tea.Cmd { return nil }

// SelectedNodeID returns the ID of the currently selected node, or "".
func (nl *NodeList) SelectedNodeID() string {
	nodes := nl.store.Nodes()
	if nl.selectedIdx >= 0 && nl.selectedIdx < len(nodes) {
		return nodes[nl.selectedIdx].ID
	}
	return ""
}

// MoveUp moves the selection cursor up.
func (nl *NodeList) MoveUp() {
	nodes := nl.store.Nodes()
	if len(nodes) == 0 {
		return
	}
	if nl.selectedIdx <= 0 {
		nl.selectedIdx = 0
	} else {
		nl.selectedIdx--
	}
}

// MoveDown moves the selection cursor down.
func (nl *NodeList) MoveDown() {
	nodes := nl.store.Nodes()
	if len(nodes) == 0 {
		return
	}
	if nl.selectedIdx < 0 {
		nl.selectedIdx = 0
	} else if nl.selectedIdx < len(nodes)-1 {
		nl.selectedIdx++
	}
}

// Update implements tea.Model.
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
		all = append(all, indexedLine{nl.renderNodeLineAt(node, i), i})
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
	return nl.renderNodeLineAt(node, -1)
}

// renderNodeLineAt builds the display line, highlighting if nodeIdx matches selectedIdx.
func (nl *NodeList) renderNodeLineAt(node NodeEntry, nodeIdx int) string {
	status := nl.store.NodeStatus(node.ID)
	lamp, style := nl.resolveLamp(node.ID, status)
	label, indent := nl.resolveNodeLabel(node)
	costSuffix := nl.buildCostSuffix(node, status)

	maxLabel := nl.width - 4 - len(indent) - lipgloss.Width(costSuffix)
	if maxLabel > 0 && len(label) > maxLabel {
		label = label[:maxLabel-1] + "…"
	}

	selector := nl.buildSelector(nodeIdx)
	line := selector + indent + style.Render(lamp) + " " + Styles.PrimaryText.Render(label)
	line += costSuffix
	line += nl.nodeStatusSuffix(node.ID, status)
	return line
}

// resolveNodeLabel returns the display label and indentation string for a node.
func (nl *NodeList) resolveNodeLabel(node NodeEntry) (label, indent string) {
	label = node.Label
	if label == "" {
		label = node.ID
	}
	if IsSubgraphNode(node.ID) {
		depth := SubgraphDepth(node.ID)
		indent = strings.Repeat("  ", depth)
		if label == node.ID {
			label = SubgraphChildLabel(node.ID)
		}
	}
	return label, indent
}

// buildCostSuffix returns the formatted cost badge string for a completed node, or empty string.
func (nl *NodeList) buildCostSuffix(node NodeEntry, status NodeState) string {
	if status != NodeDone || isParallelDispatcher(node) {
		return ""
	}
	cost := nl.store.NodeCost(node.ID)
	if cost <= 0.001 {
		return ""
	}
	prefix := "$"
	if isParallelBranch(node) {
		prefix = "~"
	}
	return " " + Styles.CostBadge.Render(fmt.Sprintf("%s%.2f", prefix, cost))
}

// buildSelector returns the selection indicator prefix for a node row.
func (nl *NodeList) buildSelector(nodeIdx int) string {
	if nl.selectedIdx < 0 {
		return ""
	}
	if nodeIdx == nl.selectedIdx {
		return lipgloss.NewStyle().Foreground(ColorAmber).Bold(true).Render("▸") + " "
	}
	return "  "
}

// autoScroll adjusts the scroll offset to keep the target node visible.
// Prioritizes the selected node (user navigation), falls back to running node.
func (nl *NodeList) autoScroll(nodes []NodeEntry, lines []indexedLine) {
	if nl.height <= 0 {
		return
	}
	targetLine := nl.findScrollTarget(nodes, lines)
	if targetLine < 0 {
		return
	}
	nl.clampScrollToTarget(targetLine)
}

// findScrollTarget returns the line index to scroll to: selected node first,
// then the first running node, or -1 if neither is found.
func (nl *NodeList) findScrollTarget(nodes []NodeEntry, lines []indexedLine) int {
	if nl.selectedIdx >= 0 {
		for i, l := range lines {
			if l.nodeIdx == nl.selectedIdx {
				return i
			}
		}
	}
	for i, l := range lines {
		if l.nodeIdx >= 0 && l.nodeIdx < len(nodes) {
			if nl.store.NodeStatus(nodes[l.nodeIdx].ID) == NodeRunning {
				return i
			}
		}
	}
	return -1
}

// clampScrollToTarget adjusts nl.scroll so targetLine is visible in the viewport.
func (nl *NodeList) clampScrollToTarget(targetLine int) {
	if targetLine < nl.scroll {
		nl.scroll = targetLine
	}
	if targetLine >= nl.scroll+nl.height {
		nl.scroll = targetLine - nl.height + 1
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
	// Match the selection indent so connectors align with lamp icons.
	prefix := ""
	if nl.selectedIdx >= 0 {
		prefix = "  "
	}
	return prefix + connStyle.Render("│")
}

// isParallelDispatcher returns true if the node dispatches parallel branches.
func isParallelDispatcher(node NodeEntry) bool {
	return node.Flags.IsParallelDispatcher
}

// isParallelBranch returns true if the node runs as a parallel branch.
func isParallelBranch(node NodeEntry) bool {
	return node.Flags.IsParallelBranch
}

// resolveLamp returns the lamp icon and style for a node, overriding for active phases.
func (nl *NodeList) resolveLamp(nodeID string, status NodeState) (string, lipgloss.Style) {
	lamp, style := StatusLamp(status)
	if status != NodeRunning {
		return lamp, style
	}
	if nl.thinking.IsToolRunning(nodeID) {
		return "»", lipgloss.NewStyle().Foreground(ColorBash).Bold(true)
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
