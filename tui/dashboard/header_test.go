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
	if !strings.Contains(view, "OPENAI") {
		t.Errorf("expected provider 'OPENAI' in header, got: %q", view)
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

func TestHeaderMultipleProvidersShowsBoth(t *testing.T) {
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
	if !strings.Contains(view, "OPENAI") {
		t.Errorf("expected 'OPENAI' in multi-provider header, got: %q", view)
	}
	if !strings.Contains(view, "ANTHROPIC") {
		t.Errorf("expected 'ANTHROPIC' in multi-provider header, got: %q", view)
	}
}

func TestFormatTokenCountSmall(t *testing.T) {
	if got := formatTokenCount(42); got != "42" {
		t.Errorf("expected '42', got %q", got)
	}
	if got := formatTokenCount(999); got != "999" {
		t.Errorf("expected '999', got %q", got)
	}
}

func TestFormatTokenCountThousands(t *testing.T) {
	if got := formatTokenCount(1000); got != "1.0k" {
		t.Errorf("expected '1.0k', got %q", got)
	}
	if got := formatTokenCount(12400); got != "12.4k" {
		t.Errorf("expected '12.4k', got %q", got)
	}
	if got := formatTokenCount(999999); got != "1000.0k" {
		t.Errorf("expected '1000.0k', got %q", got)
	}
}

func TestFormatTokenCountMillions(t *testing.T) {
	if got := formatTokenCount(1000000); got != "1.0m" {
		t.Errorf("expected '1.0m', got %q", got)
	}
	if got := formatTokenCount(2500000); got != "2.5m" {
		t.Errorf("expected '2.5m', got %q", got)
	}
}

func TestFormatDuration(t *testing.T) {
	if got := formatDuration(5 * time.Second); got != "5s" {
		t.Errorf("expected '5s', got %q", got)
	}
	if got := formatDuration(2*time.Minute + 14*time.Second); got != "2m14s" {
		t.Errorf("expected '2m14s', got %q", got)
	}
	if got := formatDuration(10 * time.Minute); got != "10m00s" {
		t.Errorf("expected '10m00s', got %q", got)
	}
}

func TestHeaderShowsRunningStatus(t *testing.T) {
	h := NewHeaderModel("test", nil)
	view := h.View()
	if !strings.Contains(view, "RUNNING") {
		t.Errorf("expected 'RUNNING' in header, got: %q", view)
	}
}

func TestHeaderShowsCompletedStatus(t *testing.T) {
	h := NewHeaderModel("test", nil)
	h.SetStatus(StatusCompleted)
	view := h.View()
	if !strings.Contains(view, "COMPLETED") {
		t.Errorf("expected 'COMPLETED' in header, got: %q", view)
	}
}

func TestHeaderShowsFailedStatus(t *testing.T) {
	h := NewHeaderModel("test", nil)
	h.SetStatus(StatusFailed)
	view := h.View()
	if !strings.Contains(view, "FAILED") {
		t.Errorf("expected 'FAILED' in header, got: %q", view)
	}
}

func TestCapitalizeFirst(t *testing.T) {
	if got := capitalizeFirst("openai"); got != "Openai" {
		t.Errorf("expected 'Openai', got %q", got)
	}
	if got := capitalizeFirst(""); got != "" {
		t.Errorf("expected empty string, got %q", got)
	}
	if got := capitalizeFirst("A"); got != "A" {
		t.Errorf("expected 'A', got %q", got)
	}
}
