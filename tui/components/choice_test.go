// ABOUTME: Tests for the bubbletea choice component.
package components

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
)

func TestChoiceModelRendersPromptAndChoices(t *testing.T) {
	m := NewChoiceModel("Pick a color", []string{"red", "blue", "green"}, "")
	view := m.View()
	if !strings.Contains(view, "Pick a color") {
		t.Errorf("expected prompt in view, got: %q", view)
	}
	if !strings.Contains(view, "red") || !strings.Contains(view, "blue") || !strings.Contains(view, "green") {
		t.Errorf("expected all choices in view, got: %q", view)
	}
}

func TestChoiceModelDefaultChoiceSetsCursor(t *testing.T) {
	m := NewChoiceModel("Pick", []string{"alpha", "beta", "gamma"}, "beta")
	if m.cursor != 1 {
		t.Errorf("expected cursor=1 for default 'beta', got %d", m.cursor)
	}
}

func TestChoiceModelArrowNavigation(t *testing.T) {
	m := NewChoiceModel("Pick", []string{"a", "b", "c"}, "")
	// Start at 0; press down twice
	m2, _ := m.Update(tea.KeyMsg{Type: tea.KeyDown})
	m3, _ := m2.(ChoiceModel).Update(tea.KeyMsg{Type: tea.KeyDown})
	cm := m3.(ChoiceModel)
	if cm.cursor != 2 {
		t.Errorf("expected cursor=2 after 2 downs, got %d", cm.cursor)
	}
	// Press up once
	m4, _ := cm.Update(tea.KeyMsg{Type: tea.KeyUp})
	if m4.(ChoiceModel).cursor != 1 {
		t.Errorf("expected cursor=1 after up, got %d", m4.(ChoiceModel).cursor)
	}
}

func TestChoiceModelEnterEmitsDoneMsg(t *testing.T) {
	m := NewChoiceModel("Pick", []string{"yes", "no"}, "")
	// cursor at 0 = "yes"
	_, cmd := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	if cmd == nil {
		t.Fatal("expected a command after enter")
	}
	msg := cmd()
	doneMsg, ok := msg.(ChoiceDoneMsg)
	if !ok {
		t.Fatalf("expected ChoiceDoneMsg, got %T", msg)
	}
	if doneMsg.Value != "yes" {
		t.Errorf("expected 'yes', got %q", doneMsg.Value)
	}
}

func TestChoiceModelEscEmitsCancelMsg(t *testing.T) {
	m := NewChoiceModel("Pick", []string{"yes", "no"}, "")
	_, cmd := m.Update(tea.KeyMsg{Type: tea.KeyEsc})
	if cmd == nil {
		t.Fatal("expected a command after esc")
	}
	msg := cmd()
	if _, ok := msg.(ChoiceCancelMsg); !ok {
		t.Fatalf("expected ChoiceCancelMsg, got %T", msg)
	}
}

func TestChoiceModelNoNavigationBeyondBounds(t *testing.T) {
	m := NewChoiceModel("Pick", []string{"only"}, "")
	// Press up and down beyond bounds
	m2, _ := m.Update(tea.KeyMsg{Type: tea.KeyUp})
	if m2.(ChoiceModel).cursor != 0 {
		t.Errorf("cursor should stay at 0 after up at boundary")
	}
	m3, _ := m.Update(tea.KeyMsg{Type: tea.KeyDown})
	if m3.(ChoiceModel).cursor != 0 {
		t.Errorf("cursor should stay at 0 after down at boundary")
	}
}

func TestChoiceModelViewEmptyWhenDone(t *testing.T) {
	m := NewChoiceModel("Pick", []string{"yes", "no"}, "")
	m2, _ := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	view := m2.(ChoiceModel).View()
	if view != "" {
		t.Errorf("expected empty view when done, got: %q", view)
	}
}
