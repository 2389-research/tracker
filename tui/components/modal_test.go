// ABOUTME: Tests for the lipgloss modal overlay component.
package components

import (
	"strings"
	"testing"
)

func TestModalRendersOverBackground(t *testing.T) {
	m := NewModal("Pick one", 60, 20)
	view := m.View("background content here")
	if !strings.Contains(view, "Pick one") {
		t.Error("expected modal title in view")
	}
}

func TestModalRendersInnerContent(t *testing.T) {
	m := NewModal("Question", 80, 24)
	m.SetContent("Option A\nOption B")
	view := m.View("")
	if !strings.Contains(view, "Option A") {
		t.Errorf("expected inner content in view, got: %q", view)
	}
}

func TestModalNoDimensionsFallback(t *testing.T) {
	m := NewModal("Fallback", 0, 0)
	m.SetContent("inner text")
	view := m.View("")
	if !strings.Contains(view, "Fallback") {
		t.Error("expected title even without dimensions")
	}
	if !strings.Contains(view, "inner text") {
		t.Error("expected inner text even without dimensions")
	}
}

func TestModalSetSizeUpdates(t *testing.T) {
	m := NewModal("Resize", 40, 10)
	m.SetSize(100, 30)
	if m.termWidth != 100 || m.termHeight != 30 {
		t.Errorf("expected dimensions updated, got %dx%d", m.termWidth, m.termHeight)
	}
}
