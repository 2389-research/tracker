// ABOUTME: Tests for the Modal overlay and Choice/Freeform content models.
// ABOUTME: Verifies overlay rendering, choice selection, and freeform input.
package tui

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
)

func TestModalOverlayRendering(t *testing.T) {
	m := NewModal(80, 24)
	m.Show(NewChoiceContent("Pick one", []string{"a", "b", "c"}, nil))
	view := m.View("background content here")
	if !strings.Contains(view, "Pick one") {
		t.Errorf("expected prompt in modal, got: %s", view)
	}
}

func TestModalHideShow(t *testing.T) {
	m := NewModal(80, 24)
	if m.Visible() {
		t.Error("should not be visible initially")
	}
	m.Show(NewChoiceContent("test", []string{"a"}, nil))
	if !m.Visible() {
		t.Error("should be visible after Show")
	}
	m.Hide()
	if m.Visible() {
		t.Error("should not be visible after Hide")
	}
}

func TestChoiceContentSelection(t *testing.T) {
	ch := make(chan string, 1)
	c := NewChoiceContent("Pick", []string{"alpha", "beta", "gamma"}, ch)
	c.Update(tea.KeyMsg{Type: tea.KeyDown})
	c.Update(tea.KeyMsg{Type: tea.KeyEnter})
	select {
	case got := <-ch:
		if got != "beta" {
			t.Errorf("expected 'beta', got %q", got)
		}
	default:
		t.Error("expected value on reply channel")
	}
}

func TestFreeformContentSubmit(t *testing.T) {
	ch := make(chan string, 1)
	f := NewFreeformContent("Enter value", ch)
	// Type characters into the textarea.
	for _, r := range "hello" {
		f.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{r}})
	}
	// Submit with Ctrl+S (Enter inserts newlines in the textarea).
	f.Update(tea.KeyMsg{Type: tea.KeyCtrlS})
	select {
	case got := <-ch:
		if got != "hello" {
			t.Errorf("expected 'hello', got %q", got)
		}
	default:
		t.Error("expected value on reply channel")
	}
}

func TestFreeformContentMultiline(t *testing.T) {
	ch := make(chan string, 1)
	f := NewFreeformContent("Enter value", ch)
	for _, r := range "line one" {
		f.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{r}})
	}
	// Enter creates a newline, not submit.
	f.Update(tea.KeyMsg{Type: tea.KeyEnter})
	for _, r := range "line two" {
		f.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{r}})
	}
	f.Update(tea.KeyMsg{Type: tea.KeyCtrlS})
	select {
	case got := <-ch:
		if !strings.Contains(got, "line one") || !strings.Contains(got, "line two") {
			t.Errorf("expected multiline content, got %q", got)
		}
	default:
		t.Error("expected value on reply channel")
	}
}

func TestFreeformContentEmptyNoSubmit(t *testing.T) {
	ch := make(chan string, 1)
	f := NewFreeformContent("Enter value", ch)
	// Try to submit empty textarea.
	f.Update(tea.KeyMsg{Type: tea.KeyCtrlS})
	select {
	case got := <-ch:
		t.Errorf("should not submit empty, got %q", got)
	default:
		// expected
	}
}

// ── AutopilotContent tests ──────────────────────────────────────────────────

func TestAutopilotContentView(t *testing.T) {
	ch := make(chan string, 1)
	ac := NewAutopilotContent("Review the milestone", "approve", ch)
	view := ac.View()
	if !strings.Contains(view, "AUTOPILOT") {
		t.Errorf("expected AUTOPILOT title in view, got: %s", view)
	}
	if !strings.Contains(view, "approve") {
		t.Errorf("expected decision 'approve' in view, got: %s", view)
	}
	if !strings.Contains(view, "Review the milestone") {
		t.Errorf("expected prompt in view, got: %s", view)
	}
}

func TestAutopilotContentEnterSendsDecision(t *testing.T) {
	ch := make(chan string, 1)
	ac := NewAutopilotContent("Pick", "retry", ch)
	ac.Update(tea.KeyMsg{Type: tea.KeyEnter})
	select {
	case got := <-ch:
		if got != "retry" {
			t.Errorf("expected 'retry', got %q", got)
		}
	default:
		t.Error("expected decision on reply channel after Enter")
	}
}

func TestAutopilotContentEscSendsDecision(t *testing.T) {
	ch := make(chan string, 1)
	ac := NewAutopilotContent("Pick", "done", ch)
	ac.Update(tea.KeyMsg{Type: tea.KeyEsc})
	select {
	case got := <-ch:
		if got != "done" {
			t.Errorf("expected 'done', got %q", got)
		}
	default:
		t.Error("expected decision on reply channel after Esc")
	}
}

func TestAutopilotContentDoubleEnterNoPanic(t *testing.T) {
	ch := make(chan string, 1)
	ac := NewAutopilotContent("Pick", "ok", ch)
	ac.Update(tea.KeyMsg{Type: tea.KeyEnter})
	// Second Enter should not panic or send again.
	ac.Update(tea.KeyMsg{Type: tea.KeyEnter})
	got := <-ch
	if got != "ok" {
		t.Errorf("expected 'ok', got %q", got)
	}
}

func TestAutopilotContentCancel(t *testing.T) {
	ch := make(chan string, 1)
	ac := NewAutopilotContent("Pick", "approve", ch)
	ac.Cancel()
	// After Cancel, the channel should be closed.
	_, open := <-ch
	if open {
		t.Error("expected reply channel to be closed after Cancel")
	}
}

func TestAutopilotContentCancelThenEnterNoPanic(t *testing.T) {
	ch := make(chan string, 1)
	ac := NewAutopilotContent("Pick", "approve", ch)
	ac.Cancel()
	// Enter after cancel should not panic.
	ac.Update(tea.KeyMsg{Type: tea.KeyEnter})
}

func TestAutopilotContentTruncatesLongPrompt(t *testing.T) {
	longPrompt := strings.Repeat("a", 250)
	ch := make(chan string, 1)
	ac := NewAutopilotContent(longPrompt, "ok", ch)
	view := ac.View()
	if strings.Contains(view, longPrompt) {
		t.Error("expected long prompt to be truncated")
	}
	if !strings.Contains(view, "...") {
		t.Errorf("expected truncation ellipsis in view, got: %s", view)
	}
}
