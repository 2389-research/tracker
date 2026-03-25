package tui

import (
	"testing"

	tea "github.com/charmbracelet/bubbletea"
)

func TestHybridContentSelectLabel(t *testing.T) {
	ch := make(chan string, 1)
	h := NewHybridContent("Pick", []string{"approve", "reject"}, "", ch)
	// Down to "reject", Enter to select.
	h.Update(tea.KeyMsg{Type: tea.KeyDown})
	h.Update(tea.KeyMsg{Type: tea.KeyEnter})
	select {
	case got := <-ch:
		if got != "reject" {
			t.Errorf("expected 'reject', got %q", got)
		}
	default:
		t.Error("expected value on reply channel")
	}
}

func TestHybridContentSelectDefault(t *testing.T) {
	ch := make(chan string, 1)
	h := NewHybridContent("Pick", []string{"approve", "reject"}, "approve", ch)
	// Enter immediately selects the default.
	h.Update(tea.KeyMsg{Type: tea.KeyEnter})
	select {
	case got := <-ch:
		if got != "approve" {
			t.Errorf("expected 'approve', got %q", got)
		}
	default:
		t.Error("expected value on reply channel")
	}
}

func TestHybridContentOtherMode(t *testing.T) {
	ch := make(chan string, 1)
	h := NewHybridContent("Pick", []string{"approve"}, "", ch)
	// Down to "other", Enter to focus textarea.
	h.Update(tea.KeyMsg{Type: tea.KeyDown})
	h.Update(tea.KeyMsg{Type: tea.KeyEnter})
	if !h.onOther {
		t.Error("expected onOther after selecting 'other'")
	}
	// Type feedback.
	for _, r := range "fix the plan" {
		h.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{r}})
	}
	h.Update(tea.KeyMsg{Type: tea.KeyCtrlS})
	select {
	case got := <-ch:
		if got != "fix the plan" {
			t.Errorf("expected 'fix the plan', got %q", got)
		}
	default:
		t.Error("expected value on reply channel")
	}
}

func TestHybridContentCancelClosesChannel(t *testing.T) {
	ch := make(chan string, 1)
	h := NewHybridContent("Pick", []string{"approve"}, "", ch)
	h.Update(tea.KeyMsg{Type: tea.KeyEscape})
	_, ok := <-ch
	if ok {
		t.Error("expected channel to be closed on cancel")
	}
}

func TestHybridContentViewRendersLabels(t *testing.T) {
	h := NewHybridContent("Pick one", []string{"a", "b", "c"}, "", nil)
	view := h.View()
	for _, label := range []string{"a", "b", "c", "other"} {
		if !contains(view, label) {
			t.Errorf("expected %q in view", label)
		}
	}
}

func contains(s, sub string) bool {
	return len(s) >= len(sub) && searchString(s, sub)
}

func searchString(s, sub string) bool {
	for i := 0; i <= len(s)-len(sub); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}
