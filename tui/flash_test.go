// ABOUTME: Tests for status bar flash messages and clipboard integration.
// ABOUTME: Verifies flash display and auto-clear via message routing.
package tui

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
)

func TestStatusBarFlash(t *testing.T) {
	store := NewStateStore(nil)
	store.SetNodes([]NodeEntry{{ID: "n1"}})
	sb := NewStatusBar(store, nil)
	sb.SetWidth(80)

	sb.SetFlash("Copied!")
	view := sb.View()
	if !strings.Contains(view, "Copied!") {
		t.Error("expected flash message in status bar")
	}

	sb.ClearFlash()
	view = sb.View()
	if strings.Contains(view, "Copied!") {
		t.Error("expected flash cleared from status bar")
	}
}

func TestAppStatusFlashRouting(t *testing.T) {
	store := NewStateStore(nil)
	store.SetNodes([]NodeEntry{{ID: "n1"}})
	app := NewAppModel(store, "test", "run1")
	app.Init()
	app.Update(tea.WindowSizeMsg{Width: 80, Height: 24})

	// Send flash message.
	_, cmd := app.Update(MsgStatusFlash{Text: "Copied!"})
	if cmd == nil {
		t.Error("expected timer command for flash auto-clear")
	}

	// Verify flash is visible.
	view := app.View()
	if !strings.Contains(view, "Copied!") {
		t.Error("expected flash in view after MsgStatusFlash")
	}

	// Simulate flash clear.
	app.Update(MsgStatusFlashClear{})
	view = app.View()
	if strings.Contains(view, "Copied!") {
		t.Error("expected flash cleared after MsgStatusFlashClear")
	}
}

func TestStatusBarVerbosityBadge(t *testing.T) {
	store := NewStateStore(nil)
	store.SetNodes([]NodeEntry{{ID: "n1"}})
	tr := NewThinkingTracker()
	al := NewAgentLog(store, tr, 20)
	sb := NewStatusBar(store, al)
	sb.SetWidth(80)

	// Default verbosity is All — no badge.
	view := sb.View()
	if strings.Contains(view, "[tools]") {
		t.Error("expected no verbosity badge with All verbosity")
	}

	// Cycle to Tools.
	al.CycleVerbosity()
	view = sb.View()
	if !strings.Contains(view, "[tools]") {
		t.Errorf("expected [tools] badge, got: %s", view)
	}
}

func TestStatusBarProgressBar(t *testing.T) {
	store := NewStateStore(nil)
	store.SetNodes([]NodeEntry{{ID: "n1"}, {ID: "n2"}, {ID: "n3"}})
	store.Apply(MsgNodeStarted{NodeID: "n1"})
	store.Apply(MsgNodeCompleted{NodeID: "n1"})
	store.Apply(MsgNodeStarted{NodeID: "n2"}) // n2 running

	sb := NewStatusBar(store, nil)
	sb.SetWidth(80)

	view := sb.View()
	// When a node is running, progress bar should show fraction.
	if !strings.Contains(view, "1/3") {
		t.Errorf("expected '1/3' progress when running, got: %s", view)
	}
}
