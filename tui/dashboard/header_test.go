// ABOUTME: Tests for the dashboard header component.
package dashboard

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/2389-research/tracker/llm"
)

func TestHeaderRendersTitle(t *testing.T) {
	h := NewHeaderModel("my-pipeline", nil)
	view := h.View()
	if !strings.Contains(view, "my-pipeline") {
		t.Errorf("expected pipeline name in header, got: %q", view)
	}
}

func TestHeaderRendersElapsedTime(t *testing.T) {
	h := NewHeaderModel("test", nil)
	h.SetStartTime(time.Now().Add(-5 * time.Second))
	view := h.View()
	if !strings.Contains(view, "⏱") {
		t.Errorf("expected elapsed time symbol in header, got: %q", view)
	}
}

func TestHeaderRendersTokenCountsForProvider(t *testing.T) {
	tracker := llm.NewTokenTracker()
	// Inject a usage record via WrapComplete
	wrapped := tracker.WrapComplete(func(_ context.Context, req *llm.Request) (*llm.Response, error) {
		return &llm.Response{
			Provider: "openai",
			Usage:    llm.Usage{InputTokens: 100, OutputTokens: 50},
		}, nil
	})
	_, _ = wrapped(context.Background(), &llm.Request{Provider: "openai"})

	h := NewHeaderModel("test-pipeline", tracker)
	view := h.View()
	if !strings.Contains(view, "openai") {
		t.Errorf("expected provider 'openai' in header, got: %q", view)
	}
	if !strings.Contains(view, "100") {
		t.Errorf("expected input token count 100 in header, got: %q", view)
	}
}

func TestHeaderWithNilTrackerDoesNotPanic(t *testing.T) {
	h := NewHeaderModel("my-pipeline", nil)
	view := h.View()
	if view == "" {
		t.Error("expected non-empty view even with nil tracker")
	}
}

func TestHeaderWithWidthSet(t *testing.T) {
	h := NewHeaderModel("pipeline-x", nil)
	h.SetWidth(80)
	view := h.View()
	if !strings.Contains(view, "pipeline-x") {
		t.Errorf("expected pipeline name in width-constrained header, got: %q", view)
	}
}

func TestHeaderDefaultTitleFallback(t *testing.T) {
	h := NewHeaderModel("", nil)
	view := h.View()
	if !strings.Contains(view, "pipeline") {
		t.Errorf("expected fallback title 'pipeline' in header, got: %q", view)
	}
}

func TestHeaderSetWidthUpdates(t *testing.T) {
	h := NewHeaderModel("test", nil)
	h.SetWidth(120)
	if h.width != 120 {
		t.Errorf("expected width=120, got %d", h.width)
	}
}

func TestHeaderSetStartTimeOverrides(t *testing.T) {
	h := NewHeaderModel("test", nil)
	past := time.Now().Add(-1 * time.Hour)
	h.SetStartTime(past)
	if !h.startTime.Equal(past) {
		t.Error("expected start time to be overridden")
	}
}

func TestHeaderMultipleProvidersShowsTotal(t *testing.T) {
	tracker := llm.NewTokenTracker()
	wrapped := tracker.WrapComplete(func(_ context.Context, req *llm.Request) (*llm.Response, error) {
		return &llm.Response{
			Provider: req.Provider,
			Usage:    llm.Usage{InputTokens: 10, OutputTokens: 5},
		}, nil
	})
	_, _ = wrapped(context.Background(), &llm.Request{Provider: "openai"})
	_, _ = wrapped(context.Background(), &llm.Request{Provider: "anthropic"})

	h := NewHeaderModel("multi-provider", tracker)
	view := h.View()
	if !strings.Contains(view, "total") {
		t.Errorf("expected 'total' in multi-provider header, got: %q", view)
	}
}
