// ABOUTME: Tests for the Header component.
// ABOUTME: Verifies rendering of pipeline info, elapsed time, backend tag, and autopilot tag.
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

func TestHeaderBackendTag(t *testing.T) {
	store := NewStateStore(nil)
	h := NewHeader(store, "pipeline", "run-1")
	h.SetBackend("claude-code")
	view := h.View()
	if !strings.Contains(view, "claude-code") {
		t.Errorf("expected backend tag 'claude-code' in header, got: %s", view)
	}
}

func TestHeaderBackendNativeHidden(t *testing.T) {
	store := NewStateStore(nil)
	h := NewHeader(store, "pipeline", "run-1")
	h.SetBackend("native")
	view := h.View()
	// "native" backend should not appear as a tag (it's the default).
	if strings.Contains(view, "native") {
		t.Errorf("expected native backend to be hidden, got: %s", view)
	}
}

func TestHeaderAutopilotTag(t *testing.T) {
	store := NewStateStore(nil)
	h := NewHeader(store, "pipeline", "run-1")
	h.SetAutopilot("reviewer")
	view := h.View()
	if !strings.Contains(view, "autopilot:reviewer") {
		t.Errorf("expected autopilot tag in header, got: %s", view)
	}
}

func TestHeaderNoTagsWhenUnset(t *testing.T) {
	store := NewStateStore(nil)
	h := NewHeader(store, "pipeline", "run-1")
	view := h.View()
	if strings.Contains(view, "autopilot") {
		t.Errorf("expected no autopilot tag when unset, got: %s", view)
	}
	if strings.Contains(view, "claude-code") {
		t.Errorf("expected no backend tag when unset, got: %s", view)
	}
}

func TestHeaderBothTags(t *testing.T) {
	store := NewStateStore(nil)
	h := NewHeader(store, "pipeline", "run-1")
	h.SetBackend("claude-code")
	h.SetAutopilot("planner")
	view := h.View()
	if !strings.Contains(view, "claude-code") {
		t.Errorf("expected backend tag, got: %s", view)
	}
	if !strings.Contains(view, "autopilot:planner") {
		t.Errorf("expected autopilot tag, got: %s", view)
	}
}
