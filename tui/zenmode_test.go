// ABOUTME: Tests for zen mode (full-width agent log) and help overlay.
// ABOUTME: Verifies zen toggle, help modal display/dismiss, and key routing.
package tui

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
)

func TestZenModeToggle(t *testing.T) {
	store := NewStateStore(nil)
	store.SetNodes([]NodeEntry{{ID: "n1"}})
	app := NewAppModel(store, "test", "run1")
	app.Init()
	app.Update(tea.WindowSizeMsg{Width: 80, Height: 24})

	if app.lay.zenMode {
		t.Error("expected zen mode off initially")
	}

	// Press 'z' to toggle zen.
	app.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("z")})
	if !app.lay.zenMode {
		t.Error("expected zen mode on after 'z' press")
	}

	// Press 'z' again to toggle off.
	app.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("z")})
	if app.lay.zenMode {
		t.Error("expected zen mode off after second 'z' press")
	}
}

func TestZenModeRenders(t *testing.T) {
	store := NewStateStore(nil)
	store.SetNodes([]NodeEntry{{ID: "n1", Label: "Step 1"}})
	app := NewAppModel(store, "test", "run1")
	app.Init()
	app.Update(tea.WindowSizeMsg{Width: 80, Height: 24})

	// Toggle zen mode on.
	app.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("z")})
	view := app.View()

	// In zen mode, the sidebar (PIPELINE header) should not be visible.
	if strings.Contains(view, "PIPELINE") {
		t.Error("expected sidebar hidden in zen mode")
	}
	// Activity log should still be visible.
	if !strings.Contains(view, "ACTIVITY LOG") {
		t.Error("expected activity log visible in zen mode")
	}
}

func TestZenModeModalsStillWork(t *testing.T) {
	store := NewStateStore(nil)
	app := NewAppModel(store, "test", "run1")
	app.Init()
	app.Update(tea.WindowSizeMsg{Width: 80, Height: 24})

	// Enter zen mode.
	app.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("z")})

	// Show a modal.
	ch := make(chan string, 1)
	app.Update(MsgGateChoice{Prompt: "Pick", Options: []string{"a"}, ReplyCh: ch})
	if !app.modal.Visible() {
		t.Error("expected modal visible in zen mode")
	}
}

func TestHelpOverlayShowsDismiss(t *testing.T) {
	store := NewStateStore(nil)
	app := NewAppModel(store, "test", "run1")
	app.Init()
	app.Update(tea.WindowSizeMsg{Width: 80, Height: 24})

	// Press '?' to show help.
	app.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("?")})
	if !app.modal.Visible() {
		t.Error("expected help modal visible after '?'")
	}

	// View should contain shortcut info.
	view := app.View()
	if !strings.Contains(view, "KEYBOARD SHORTCUTS") {
		t.Error("expected shortcuts title in help view")
	}

	// Press Esc to dismiss.
	app.Update(tea.KeyMsg{Type: tea.KeyEscape})
	// The Esc is handled by HelpContent.Update which returns MsgModalDismiss.
	// We need to process the resulting command.
}

func TestHelpOverlayQuestionMarkDismisses(t *testing.T) {
	help := NewHelpContent()
	cmd := help.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("?")})
	if cmd == nil {
		t.Error("expected dismiss command from '?' in help overlay")
	}
	msg := cmd()
	if _, ok := msg.(MsgModalDismiss); !ok {
		t.Error("expected MsgModalDismiss from help '?' command")
	}
}

func TestHelpOverlayEscDismisses(t *testing.T) {
	help := NewHelpContent()
	cmd := help.Update(tea.KeyMsg{Type: tea.KeyEscape})
	if cmd == nil {
		t.Error("expected dismiss command from Esc in help overlay")
	}
	msg := cmd()
	if _, ok := msg.(MsgModalDismiss); !ok {
		t.Error("expected MsgModalDismiss from help Esc command")
	}
}

func TestHelpContentCancel(t *testing.T) {
	help := NewHelpContent()
	help.Cancel() // should not panic
}

func TestKeysBlockedDuringModal(t *testing.T) {
	store := NewStateStore(nil)
	store.SetNodes([]NodeEntry{{ID: "n1"}})
	app := NewAppModel(store, "test", "run1")
	app.Init()
	app.Update(tea.WindowSizeMsg{Width: 80, Height: 24})

	// Show modal.
	ch := make(chan string, 1)
	app.Update(MsgGateChoice{Prompt: "Pick", Options: []string{"a"}, ReplyCh: ch})

	// Press 'v' — should not cycle verbosity because modal is visible.
	beforeVerb := app.agentLog.Verbosity()
	app.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("v")})
	if app.agentLog.Verbosity() != beforeVerb {
		t.Error("expected verbosity unchanged when modal is visible")
	}

	// Press 'z' — should not toggle zen.
	app.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("z")})
	if app.lay.zenMode {
		t.Error("expected zen mode unchanged when modal is visible")
	}
}
