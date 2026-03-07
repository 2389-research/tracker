// ABOUTME: Dashboard node list component showing pipeline node execution status.
// ABOUTME: Renders a linear list with status icons: ✓ done, ⟳ running, ✗ failed, ○ pending.
package dashboard

import (
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

var (
	nodeListTitleStyle = lipgloss.NewStyle().
				Bold(true).
				Foreground(lipgloss.Color("12")).
				MarginBottom(1)

	nodeDoneStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("10")) // green

	nodeRunningStyle = lipgloss.NewStyle().
				Foreground(lipgloss.Color("11")). // yellow
				Bold(true)

	nodeFailedStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("9")) // red

	nodePendingStyle = lipgloss.NewStyle().
				Foreground(lipgloss.Color("8")) // gray

	nodeLabelStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("15"))
)

const (
	iconDone    = "✓"
	iconRunning = "⟳"
	iconFailed  = "✗"
	iconPending = "○"
)

// NodeListModel renders a list of pipeline nodes with execution status icons.
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

// View renders the node list with status icons and labels.
func (n NodeListModel) View() string {
	var sb strings.Builder
	sb.WriteString(nodeListTitleStyle.Render("Pipeline Nodes"))
	sb.WriteString("\n")

	for _, node := range n.nodes {
		icon, style := statusIconAndStyle(node.Status)
		label := node.Label
		if label == "" {
			label = node.ID
		}
		line := style.Render(icon) + "  " + nodeLabelStyle.Render(label)
		sb.WriteString(line)
		sb.WriteString("\n")
	}

	if len(n.nodes) == 0 {
		sb.WriteString(nodePendingStyle.Render("(no nodes)"))
		sb.WriteString("\n")
	}

	return sb.String()
}

// statusIconAndStyle returns the icon character and lipgloss style for a given NodeStatus.
func statusIconAndStyle(status NodeStatus) (string, lipgloss.Style) {
	switch status {
	case NodeDone:
		return iconDone, nodeDoneStyle
	case NodeRunning:
		return iconRunning, nodeRunningStyle
	case NodeFailed:
		return iconFailed, nodeFailedStyle
	default:
		return iconPending, nodePendingStyle
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
