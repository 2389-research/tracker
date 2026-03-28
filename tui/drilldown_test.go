// ABOUTME: Tests for node drill-down (focus) and node list selection features.
// ABOUTME: Verifies arrow navigation, Enter focus, Esc unfocus, and log filtering.
package tui

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
)

func TestNodeListSelection(t *testing.T) {
	store := NewStateStore(nil)
	store.SetNodes([]NodeEntry{
		{ID: "n1", Label: "Step 1"},
		{ID: "n2", Label: "Step 2"},
		{ID: "n3", Label: "Step 3"},
	})
	tr := NewThinkingTracker()
	nl := NewNodeList(store, tr, 20)
	nl.SetSize(30, 20)

	// Initial state: no selection.
	if nl.SelectedNodeID() != "" {
		t.Error("expected no selection initially")
	}

	// Move down.
	nl.MoveDown()
	if nl.SelectedNodeID() != "n1" {
		t.Errorf("expected n1 selected, got %q", nl.SelectedNodeID())
	}

	nl.MoveDown()
	if nl.SelectedNodeID() != "n2" {
		t.Errorf("expected n2 selected, got %q", nl.SelectedNodeID())
	}

	nl.MoveDown()
	if nl.SelectedNodeID() != "n3" {
		t.Errorf("expected n3 selected, got %q", nl.SelectedNodeID())
	}

	// Should not go past last node.
	nl.MoveDown()
	if nl.SelectedNodeID() != "n3" {
		t.Errorf("expected n3 still selected at bottom, got %q", nl.SelectedNodeID())
	}

	// Move back up.
	nl.MoveUp()
	if nl.SelectedNodeID() != "n2" {
		t.Errorf("expected n2 selected after up, got %q", nl.SelectedNodeID())
	}
}

func TestNodeListSelectionAtTop(t *testing.T) {
	store := NewStateStore(nil)
	store.SetNodes([]NodeEntry{{ID: "n1"}, {ID: "n2"}})
	tr := NewThinkingTracker()
	nl := NewNodeList(store, tr, 20)

	nl.MoveDown() // select n1
	nl.MoveUp()   // should stay at n1 (top)
	if nl.SelectedNodeID() != "n1" {
		t.Errorf("expected n1 at top, got %q", nl.SelectedNodeID())
	}
}

func TestNodeListSelectionEmptyNodes(t *testing.T) {
	store := NewStateStore(nil)
	tr := NewThinkingTracker()
	nl := NewNodeList(store, tr, 20)

	// Should not panic with empty nodes.
	nl.MoveDown()
	nl.MoveUp()
	if nl.SelectedNodeID() != "" {
		t.Error("expected empty selection with no nodes")
	}
}

func TestNodeListShowsSelectionIndicator(t *testing.T) {
	store := NewStateStore(nil)
	store.SetNodes([]NodeEntry{{ID: "n1", Label: "Step 1"}, {ID: "n2", Label: "Step 2"}})
	tr := NewThinkingTracker()
	nl := NewNodeList(store, tr, 20)
	nl.SetSize(30, 20)

	nl.MoveDown() // select n1
	view := nl.View()
	if !strings.Contains(view, "▸") {
		t.Error("expected selection indicator ▸ in node list view")
	}
}

func TestDrillDownFocusesLog(t *testing.T) {
	store := NewStateStore(nil)
	store.SetNodes([]NodeEntry{{ID: "n1"}, {ID: "n2"}})
	tr := NewThinkingTracker()
	al := NewAgentLog(store, tr, 40)
	al.SetSize(80, 40)

	// Add lines from both nodes.
	al.Update(MsgTextChunk{NodeID: "n1", Text: "from node1\n"})
	al.Update(MsgTextChunk{NodeID: "n2", Text: "from node2\n"})

	// Focus on n1.
	al.Update(MsgFocusNode{NodeID: "n1"})
	view := al.View()
	plain := stripANSI(view)
	if !strings.Contains(plain, "from node1") {
		t.Error("expected n1 content visible when focused")
	}
	if strings.Contains(plain, "from node2") {
		t.Error("expected n2 content hidden when focused on n1")
	}

	// Check header shows node ID.
	if !strings.Contains(plain, "[n1]") {
		t.Error("expected focused node ID in log header")
	}
}

func TestDrillDownClearFocus(t *testing.T) {
	store := NewStateStore(nil)
	store.SetNodes([]NodeEntry{{ID: "n1"}, {ID: "n2"}})
	tr := NewThinkingTracker()
	al := NewAgentLog(store, tr, 40)
	al.SetSize(80, 40)

	al.Update(MsgTextChunk{NodeID: "n1", Text: "from node1\n"})
	al.Update(MsgTextChunk{NodeID: "n2", Text: "from node2\n"})

	al.Update(MsgFocusNode{NodeID: "n1"})
	al.Update(MsgClearFocus{})

	view := al.View()
	plain := stripANSI(view)
	if !strings.Contains(plain, "from node1") || !strings.Contains(plain, "from node2") {
		t.Error("expected both nodes visible after clearing focus")
	}
}

func TestAppDrillDownViaKeys(t *testing.T) {
	store := NewStateStore(nil)
	store.SetNodes([]NodeEntry{{ID: "n1", Label: "Step 1"}, {ID: "n2", Label: "Step 2"}})
	app := NewAppModel(store, "test", "run1")
	app.Init()
	app.Update(tea.WindowSizeMsg{Width: 80, Height: 24})

	// Navigate to n1 and press Enter.
	app.Update(tea.KeyMsg{Type: tea.KeyDown})
	app.Update(tea.KeyMsg{Type: tea.KeyEnter})

	if app.lay.focusedNode != "n1" {
		t.Errorf("expected focusedNode 'n1', got %q", app.lay.focusedNode)
	}

	// Press Esc to exit drill-down.
	app.Update(tea.KeyMsg{Type: tea.KeyEscape})
	if app.lay.focusedNode != "" {
		t.Errorf("expected empty focusedNode after Esc, got %q", app.lay.focusedNode)
	}
}
