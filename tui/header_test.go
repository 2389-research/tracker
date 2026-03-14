// ABOUTME: Tests for the Header component.
// ABOUTME: Verifies rendering of pipeline info and elapsed time.
package tui

import (
	"strings"
	"testing"
)

func TestHeaderRender(t *testing.T) {
	store := NewStateStore(nil)
	h := NewHeader(store, "test-pipeline", "run-abc")
	view := h.View()
	if !strings.Contains(view, "test-pipeline") {
		t.Errorf("expected pipeline name in header, got: %s", view)
	}
	if !strings.Contains(view, "run-abc") {
		t.Errorf("expected run ID in header, got: %s", view)
	}
}

func TestHeaderElapsedTime(t *testing.T) {
	store := NewStateStore(nil)
	h := NewHeader(store, "p", "r")
	view := h.View()
	if !strings.Contains(view, "0") {
		t.Errorf("expected zero elapsed time initially, got: %s", view)
	}
}
