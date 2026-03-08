// ABOUTME: Tests for the bubbletea freeform text input component.
package components

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
)

func TestFreeformModelRendersPrompt(t *testing.T) {
	m := NewFreeformModel("Tell me something interesting")
	view := m.View()
	if !strings.Contains(view, "Tell me something interesting") {
		t.Errorf("expected prompt in view, got: %q", view)
	}
}

func TestFreeformModelEnterWithTextEmitsDoneMsg(t *testing.T) {
	m := NewFreeformModel("Say something")
	// Type some text
	for _, ch := range "hello world" {
		msg := tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{ch}}
		model, _ := m.Update(msg)
		m = model.(FreeformModel)
	}
	// Press enter
	_, cmd := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	if cmd == nil {
		t.Fatal("expected a command after enter")
	}
	msg := cmd()
	doneMsg, ok := msg.(FreeformDoneMsg)
	if !ok {
		t.Fatalf("expected FreeformDoneMsg, got %T", msg)
	}
	if doneMsg.Value != "hello world" {
		t.Errorf("expected 'hello world', got %q", doneMsg.Value)
	}
}

func TestFreeformModelEnterWithEmptyRejectsAndShowsError(t *testing.T) {
	m := NewFreeformModel("Say something")
	m2, cmd := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	fm := m2.(FreeformModel)

	// Should show error, not done
	if fm.IsDone() {
		t.Error("should not be done after empty submit")
	}
	if cmd != nil {
		t.Error("should not emit command on empty submit")
	}
	view := fm.View()
	if !strings.Contains(view, "empty") && !strings.Contains(view, "Empty") {
		t.Errorf("expected empty-input error in view, got: %q", view)
	}
}

func TestFreeformModelEscEmitsCancelMsg(t *testing.T) {
	m := NewFreeformModel("Say something")
	_, cmd := m.Update(tea.KeyMsg{Type: tea.KeyEsc})
	if cmd == nil {
		t.Fatal("expected a command after esc")
	}
	msg := cmd()
	if _, ok := msg.(FreeformCancelMsg); !ok {
		t.Fatalf("expected FreeformCancelMsg, got %T", msg)
	}
}

func TestFreeformModelViewEmptyWhenDone(t *testing.T) {
	m := NewFreeformModel("Say something")
	for _, ch := range "test" {
		model, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{ch}})
		m = model.(FreeformModel)
	}
	m2, _ := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	if m2.(FreeformModel).View() != "" {
		t.Error("expected empty view when done")
	}
}

func TestFreeformModelSetWidthAffectsPromptRendering(t *testing.T) {
	long := strings.Repeat("word ", 40)
	m := NewFreeformModel(long)
	m.SetWidth(40)
	view := m.View()
	for _, line := range strings.Split(view, "\n") {
		plain := stripANSI(line)
		runeLen := len([]rune(plain))
		if runeLen > 50 {
			t.Errorf("line too long (%d runes) for width=40: %q", runeLen, plain)
		}
	}
}

func TestFreeformModelDefaultWidthIs76(t *testing.T) {
	m := NewFreeformModel("test")
	if m.width != 76 {
		t.Errorf("expected default width=76, got %d", m.width)
	}
}
