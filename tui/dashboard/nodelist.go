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
type NodeListModel struct {
	nodes []NodeEntry
	width int
}

// NewNodeListModel creates a node list with the given initial entries.
func NewNodeListModel(nodes []NodeEntry) NodeListModel {
	return NodeListModel{nodes: nodes}
}

// SetWidth updates the width used for rendering.
func (n *NodeListModel) SetWidth(width int) {
	n.width = width
}

// SetNodeStatus updates the status of the node with the given ID.
// If the node is not found, no change is made.
func (n *NodeListModel) SetNodeStatus(id string, status NodeStatus) {
	for i := range n.nodes {
		if n.nodes[i].ID == id {
			n.nodes[i].Status = status
			return
		}
	}
}

// AddNode appends a new node entry to the list.
func (n *NodeListModel) AddNode(entry NodeEntry) {
	n.nodes = append(n.nodes, entry)
}

// View renders the node list as a signal lamp panel.
func (n NodeListModel) View() string {
	var sb strings.Builder
	sb.WriteString(zoneLabelStyle.Render("PIPELINE"))
	sb.WriteString("\n")

	for _, node := range n.nodes {
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

	if len(n.nodes) == 0 {
		sb.WriteString(dimTextStyle.Render("(no nodes)"))
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
