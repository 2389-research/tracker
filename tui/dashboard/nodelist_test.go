// ABOUTME: Tests for the dashboard node list component.
package dashboard

import (
	"strings"
	"testing"
)

func TestNodeListRendersNodesWithLabels(t *testing.T) {
	nodes := []NodeEntry{
		{ID: "start", Label: "Start", Status: NodeDone},
		{ID: "agent", Label: "Agent Node", Status: NodeRunning},
		{ID: "exit", Label: "Exit", Status: NodePending},
	}
	nl := NewNodeListModel(nodes)
	view := nl.View()

	if !strings.Contains(view, "Start") {
		t.Errorf("expected 'Start' in view, got: %q", view)
	}
	if !strings.Contains(view, "Agent Node") {
		t.Errorf("expected 'Agent Node' in view, got: %q", view)
	}
	if !strings.Contains(view, "Exit") {
		t.Errorf("expected 'Exit' in view, got: %q", view)
	}
}

func TestNodeListRendersStatusIcons(t *testing.T) {
	nodes := []NodeEntry{
		{ID: "a", Label: "Done Node", Status: NodeDone},
		{ID: "b", Label: "Running Node", Status: NodeRunning},
		{ID: "c", Label: "Failed Node", Status: NodeFailed},
		{ID: "d", Label: "Pending Node", Status: NodePending},
	}
	nl := NewNodeListModel(nodes)
	view := nl.View()

	if !strings.Contains(view, iconDone) {
		t.Errorf("expected done icon %q in view", iconDone)
	}
	if !strings.Contains(view, iconRunning) {
		t.Errorf("expected running icon %q in view", iconRunning)
	}
	if !strings.Contains(view, iconFailed) {
		t.Errorf("expected failed icon %q in view", iconFailed)
	}
	if !strings.Contains(view, iconPending) {
		t.Errorf("expected pending icon %q in view", iconPending)
	}
}

func TestNodeListEmptyShowsPlaceholder(t *testing.T) {
	nl := NewNodeListModel(nil)
	view := nl.View()
	if !strings.Contains(view, "no nodes") {
		t.Errorf("expected '(no nodes)' placeholder in empty list, got: %q", view)
	}
}

func TestNodeListFallsBackToIDWhenNoLabel(t *testing.T) {
	nodes := []NodeEntry{
		{ID: "my-node-id", Label: "", Status: NodePending},
	}
	nl := NewNodeListModel(nodes)
	view := nl.View()
	if !strings.Contains(view, "my-node-id") {
		t.Errorf("expected node ID as fallback label, got: %q", view)
	}
}

func TestNodeListSetNodeStatusUpdatesEntry(t *testing.T) {
	nodes := []NodeEntry{
		{ID: "node1", Label: "Node 1", Status: NodePending},
	}
	nl := NewNodeListModel(nodes)
	nl.SetNodeStatus("node1", NodeRunning)

	if nl.nodes[0].Status != NodeRunning {
		t.Errorf("expected NodeRunning after SetNodeStatus, got %v", nl.nodes[0].Status)
	}
}

func TestNodeListSetNodeStatusUnknownIDIsNoop(t *testing.T) {
	nodes := []NodeEntry{
		{ID: "node1", Label: "Node 1", Status: NodePending},
	}
	nl := NewNodeListModel(nodes)
	nl.SetNodeStatus("nonexistent", NodeDone) // should not panic
	if nl.nodes[0].Status != NodePending {
		t.Error("expected original node status unchanged")
	}
}

func TestNodeListAddNode(t *testing.T) {
	nl := NewNodeListModel(nil)
	nl.AddNode(NodeEntry{ID: "new", Label: "New Node", Status: NodePending})
	view := nl.View()
	if !strings.Contains(view, "New Node") {
		t.Errorf("expected 'New Node' after AddNode, got: %q", view)
	}
}

func TestNodeListCounts(t *testing.T) {
	nodes := []NodeEntry{
		{ID: "a", Status: NodePending},
		{ID: "b", Status: NodePending},
		{ID: "c", Status: NodeRunning},
		{ID: "d", Status: NodeDone},
		{ID: "e", Status: NodeFailed},
	}
	nl := NewNodeListModel(nodes)
	pending, running, done, failed := nl.Counts()
	if pending != 2 {
		t.Errorf("expected 2 pending, got %d", pending)
	}
	if running != 1 {
		t.Errorf("expected 1 running, got %d", running)
	}
	if done != 1 {
		t.Errorf("expected 1 done, got %d", done)
	}
	if failed != 1 {
		t.Errorf("expected 1 failed, got %d", failed)
	}
}

func TestNodeListSetWidthUpdates(t *testing.T) {
	nl := NewNodeListModel(nil)
	nl.SetWidth(80)
	if nl.width != 80 {
		t.Errorf("expected width=80, got %d", nl.width)
	}
}

func TestStatusIconAndStyleCoversAllCases(t *testing.T) {
	cases := map[NodeStatus]string{
		NodeDone:    iconDone,
		NodeRunning: iconRunning,
		NodeFailed:  iconFailed,
		NodePending: iconPending,
	}
	for status, expectedIcon := range cases {
		icon, _ := statusIconAndStyle(status)
		if icon != expectedIcon {
			t.Errorf("status %v: expected icon %q, got %q", status, expectedIcon, icon)
		}
	}
}
