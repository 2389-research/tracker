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
