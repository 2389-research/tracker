// ABOUTME: Tests for the StatusBar component.
// ABOUTME: Verifies track diagram, progress summary, and keybinding hints.
package tui

import (
	"strings"
	"testing"
)

func TestStatusBarProgress(t *testing.T) {
	store := NewStateStore(nil)
	store.SetNodes([]NodeEntry{{ID: "n1"}, {ID: "n2"}, {ID: "n3"}})
	store.Apply(MsgNodeCompleted{NodeID: "n1"})
	sb := NewStatusBar(store)
	view := sb.View()
	if !strings.Contains(view, "1/3") {
		t.Errorf("expected '1/3' progress, got: %s", view)
	}
}

func TestStatusBarTrackDiagram(t *testing.T) {
	store := NewStateStore(nil)
	store.SetNodes([]NodeEntry{{ID: "n1"}, {ID: "n2"}, {ID: "n3"}})
	store.Apply(MsgNodeStarted{NodeID: "n1"})
	store.Apply(MsgNodeCompleted{NodeID: "n1"})
	store.Apply(MsgNodeFailed{NodeID: "n2"})
	sb := NewStatusBar(store)
	view := sb.View()
	if !strings.Contains(view, LampDone) {
		t.Errorf("expected done lamp in track diagram, got: %s", view)
	}
	if !strings.Contains(view, LampFailed) {
		t.Errorf("expected failed lamp in track diagram, got: %s", view)
	}
}

func TestStatusBarKeybindingHints(t *testing.T) {
	store := NewStateStore(nil)
	sb := NewStatusBar(store)
	view := sb.View()
	if !strings.Contains(view, "ctrl+o") {
		t.Errorf("expected keybinding hint, got: %s", view)
	}
}
