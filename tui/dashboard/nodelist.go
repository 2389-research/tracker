// ABOUTME: Dashboard node list — signal lamp panel showing pipeline node execution status.
// ABOUTME: Uses colored indicator dots: ● done (green), ◉ running (amber), ○ pending (dim), ✖ failed (red).
package dashboard

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/lipgloss"
)

// NodeStatus represents the execution state of a pipeline node.
type NodeStatus int

const (
	NodePending NodeStatus = iota
	NodeRunning
	NodeDone
	NodeFailed
)

// NodeEntry represents a single node in the pipeline with its current status.
type NodeEntry struct {
	ID     string
	Label  string
	Status NodeStatus
}

// NodeListModel renders a signal lamp panel of pipeline nodes.
// When height is set and the list exceeds it, only a visible window is
// rendered and auto-scrolls to keep the most recent running node visible.
type NodeListModel struct {
	nodes  []NodeEntry
	width  int
	height int // 0 means unlimited (render all)
	offset int // first visible node index
}

// NewNodeListModel creates a node list with the given initial entries.
func NewNodeListModel(nodes []NodeEntry) NodeListModel {
	return NodeListModel{nodes: nodes}
}

// SetWidth updates the width used for rendering.
func (n *NodeListModel) SetWidth(width int) {
	n.width = width
}

// SetHeight sets the maximum number of lines (including header and scroll
// indicators) available for the node list. 0 means unlimited.
func (n *NodeListModel) SetHeight(height int) {
	n.height = height
}

// SetNodeStatus updates the status of the node with the given ID.
// If the node is not found, no change is made. When a node transitions to
// NodeRunning, the viewport auto-scrolls to keep it visible.
func (n *NodeListModel) SetNodeStatus(id string, status NodeStatus) {
	for i := range n.nodes {
		if n.nodes[i].ID == id {
			n.nodes[i].Status = status
			if status == NodeRunning {
				n.ensureVisible(i)
			}
			return
		}
	}
}

// AddNode appends a new node entry to the list.
func (n *NodeListModel) AddNode(entry NodeEntry) {
	n.nodes = append(n.nodes, entry)
}

// visibleWindow returns the start and end (exclusive) indices of the node
// slice to render, plus whether up/down scroll indicators are needed.
func (n NodeListModel) visibleWindow() (start, end int, upIndicator, downIndicator bool) {
	total := len(n.nodes)
	if total == 0 {
		return 0, 0, false, false
	}

	// Not enough room for header + any node rows
	if n.height > 0 && n.height < 2 {
		return 0, 0, false, false
	}

	// header line = 1, each node = 1 line, indicators = 1 each
	// If height is 0 or large enough for everything, show all.
	if n.height <= 0 || n.height >= total+1 {
		return 0, total, false, false
	}

	// Available lines for node rows: height - 1 (header)
	avail := n.height - 1

	// Reserve lines for scroll indicators only if there is room
	needUp := n.offset > 0
	needDown := n.offset+avail < total
	if needUp && avail > 1 {
		avail--
	}
	if needDown && avail > 1 {
		avail--
	}
	if avail < 1 {
		avail = 1
	}

	start = n.offset
	end = start + avail
	if end > total {
		end = total
	}

	// Only show indicators when there is room beyond the minimum node row
	upIndicator = start > 0 && n.height > 2
	downIndicator = end < total && n.height > 2

	return start, end, upIndicator, downIndicator
}

// ensureVisible adjusts the scroll offset so that the node at index idx
// is within the visible window.
func (n *NodeListModel) ensureVisible(idx int) {
	total := len(n.nodes)
	if n.height <= 0 || total == 0 {
		return
	}

	// When scrolled to the middle, both ↑ and ↓ indicators are shown, so
	// available node lines = height - 1 (header) - 1 (up) - 1 (down).
	// At the top only ↓ is shown; at the bottom only ↑. We use the
	// worst-case (both indicators) and then clamp the offset.
	avail := n.height - 3
	if avail < 1 {
		avail = 1
	}

	// Scroll down if below viewport
	if idx >= n.offset+avail {
		n.offset = idx - avail + 1
	}
	// Scroll up if above viewport
	if idx < n.offset {
		n.offset = idx
	}

	// Clamp
	if n.offset < 0 {
		n.offset = 0
	}
	maxOffset := total - avail
	if maxOffset < 0 {
		maxOffset = 0
	}
	if n.offset > maxOffset {
		n.offset = maxOffset
	}
}

// View renders the node list as a signal lamp panel.
func (n NodeListModel) View() string {
	var sb strings.Builder
	sb.WriteString(zoneLabelStyle.Render("PIPELINE"))
	sb.WriteString("\n")

	if len(n.nodes) == 0 {
		sb.WriteString(dimTextStyle.Render("(no nodes)"))
		sb.WriteString("\n")
		return sb.String()
	}

	start, end, upInd, downInd := n.visibleWindow()

	// Height-constrained to show no nodes — just return the header
	if start == end && len(n.nodes) > 0 {
		return sb.String()
	}

	if upInd {
		sb.WriteString(dimTextStyle.Render(fmt.Sprintf("  ↑ %d more", start)))
		sb.WriteString("\n")
	}

	for i := start; i < end; i++ {
		node := n.nodes[i]
		lamp, style := signalLamp(node.Status)
		label := node.Label
		if label == "" {
			label = node.ID
		}
		// Truncate long labels
		maxLabel := n.width - 4
		if maxLabel > 0 && len(label) > maxLabel {
			label = label[:maxLabel-1] + "…"
		}

		line := style.Render(lamp) + " " + primaryTextStyle.Render(label)
		sb.WriteString(line)
		sb.WriteString("\n")
	}

	if downInd {
		remaining := len(n.nodes) - end
		sb.WriteString(dimTextStyle.Render(fmt.Sprintf("  ↓ %d more", remaining)))
		sb.WriteString("\n")
	}

	return sb.String()
}

// signalLamp returns the indicator character and style for a node status.
func signalLamp(status NodeStatus) (string, lipgloss.Style) {
	switch status {
	case NodeDone:
		return lampOn, lipgloss.NewStyle().Foreground(colorGreen)
	case NodeRunning:
		return lampActive, lipgloss.NewStyle().Foreground(colorAmber).Bold(true)
	case NodeFailed:
		return lampError, lipgloss.NewStyle().Foreground(colorRed)
	default:
		return lampOff, lipgloss.NewStyle().Foreground(colorOff)
	}
}

// Counts returns the number of nodes in each status category.
func (n NodeListModel) Counts() (pending, running, done, failed int) {
	for _, node := range n.nodes {
		switch node.Status {
		case NodePending:
			pending++
		case NodeRunning:
			running++
		case NodeDone:
			done++
		case NodeFailed:
			failed++
		}
	}
	return
}

// TrackDiagram renders a compact track diagram for the status bar.
// Shows connected dots representing node states: ●━●━◉━○━○
func (n NodeListModel) TrackDiagram() string {
	if len(n.nodes) == 0 {
		return ""
	}

	var parts []string
	for _, node := range n.nodes {
		lamp, style := signalLamp(node.Status)
		parts = append(parts, style.Render(lamp))
	}

	connector := dimTextStyle.Render(connectorH)
	return strings.Join(parts, connector)
}

// ProgressSummary returns a compact string like "6/10 ● 2 ◉ 2 ○"
func (n NodeListModel) ProgressSummary() string {
	pending, running, done, failed := n.Counts()
	total := pending + running + done + failed
	if total == 0 {
		return ""
	}

	var parts []string
	parts = append(parts, primaryTextStyle.Render(fmt.Sprintf("%d/%d", done, total)))

	if done > 0 {
		parts = append(parts, lipgloss.NewStyle().Foreground(colorGreen).Render(
			fmt.Sprintf("%s%d", lampOn, done)))
	}
	if running > 0 {
		parts = append(parts, lipgloss.NewStyle().Foreground(colorAmber).Render(
			fmt.Sprintf("%s%d", lampActive, running)))
	}
	if failed > 0 {
		parts = append(parts, lipgloss.NewStyle().Foreground(colorRed).Render(
			fmt.Sprintf("%s%d", lampError, failed)))
	}
	if pending > 0 {
		parts = append(parts, lipgloss.NewStyle().Foreground(colorOff).Render(
			fmt.Sprintf("%s%d", lampOff, pending)))
	}

	return strings.Join(parts, " ")
}
