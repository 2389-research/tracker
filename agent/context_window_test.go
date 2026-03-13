// ABOUTME: Tests for context window tracking using latest input tokens for utilization calculation.
// ABOUTME: Covers unit tests for ContextWindowTracker and integration tests for session-level warning events.
package agent

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/2389-research/tracker/llm"
)

func TestContextWindowTracker_Update(t *testing.T) {
	tracker := NewContextWindowTracker(200000, 0.8)

	tracker.Update(llm.Usage{InputTokens: 100, OutputTokens: 50})
	expectedUtil := 100.0 / 200000.0
	if tracker.Utilization() != expectedUtil {
		t.Errorf("expected utilization %f after first update, got %f", expectedUtil, tracker.Utilization())
	}

	// Second update: InputTokens represents the full context size for that turn,
	// so utilization should reflect only the latest input tokens.
	tracker.Update(llm.Usage{InputTokens: 200, OutputTokens: 100})
	expectedUtil = 200.0 / 200000.0
	if tracker.Utilization() != expectedUtil {
		t.Errorf("expected utilization %f after second update, got %f", expectedUtil, tracker.Utilization())
	}
}

func TestContextWindowTracker_Utilization(t *testing.T) {
	tracker := NewContextWindowTracker(1000, 0.8)

	// Utilization is based on latest InputTokens only, not InputTokens+OutputTokens.
	tracker.Update(llm.Usage{InputTokens: 300, OutputTokens: 200})
	util := tracker.Utilization()
	if util != 0.3 {
		t.Errorf("expected utilization 0.3, got %f", util)
	}
}

func TestContextWindowTracker_ShouldWarn(t *testing.T) {
	t.Run("below threshold", func(t *testing.T) {
		tracker := NewContextWindowTracker(1000, 0.8)
		tracker.Update(llm.Usage{InputTokens: 500, OutputTokens: 200})
		if tracker.ShouldWarn() {
			t.Error("should not warn when utilization (0.5) is below threshold (0.8)")
		}
	})

	t.Run("at threshold", func(t *testing.T) {
		tracker := NewContextWindowTracker(1000, 0.8)
		tracker.Update(llm.Usage{InputTokens: 800, OutputTokens: 300})
		if !tracker.ShouldWarn() {
			t.Error("should warn when utilization (0.8) equals threshold (0.8)")
		}
	})

	t.Run("above threshold", func(t *testing.T) {
		tracker := NewContextWindowTracker(1000, 0.8)
		tracker.Update(llm.Usage{InputTokens: 900, OutputTokens: 300})
		if !tracker.ShouldWarn() {
			t.Error("should warn when utilization (0.9) is above threshold (0.8)")
		}
	})
}

func TestContextWindowTracker_WarnOnlyOnce(t *testing.T) {
	tracker := NewContextWindowTracker(1000, 0.8)
	tracker.Update(llm.Usage{InputTokens: 900, OutputTokens: 100})

	if !tracker.ShouldWarn() {
		t.Fatal("expected first ShouldWarn to return true")
	}
	tracker.MarkWarned()

	if tracker.ShouldWarn() {
		t.Error("expected ShouldWarn to return false after MarkWarned")
	}

	// Even with continued high utilization, warning should not re-trigger.
	tracker.Update(llm.Usage{InputTokens: 950, OutputTokens: 50})
	if tracker.ShouldWarn() {
		t.Error("expected ShouldWarn to remain false after MarkWarned")
	}
}

func TestContextWindowTracker_ZeroTokens(t *testing.T) {
	tracker := NewContextWindowTracker(200000, 0.8)

	if tracker.Utilization() != 0.0 {
		t.Errorf("expected 0.0 initial utilization, got %f", tracker.Utilization())
	}
	if tracker.ShouldWarn() {
		t.Error("should not warn with zero tokens")
	}
	if tracker.WarningEmitted {
		t.Error("WarningEmitted should be false initially")
	}
}

func TestContextWindowSession_WarningEmitted(t *testing.T) {
	// Set up a small context window (1000 tokens) so the mock responses cross the threshold.
	// First response: InputTokens 400 (40% utilization) - no warning.
	// Second response (after tool call): InputTokens 850 (85% utilization) - warning emitted.
	toolCallResp := &llm.Response{
		Message: llm.Message{
			Role: llm.RoleAssistant,
			Content: []llm.ContentPart{
				{
					Kind: llm.KindToolCall,
					ToolCall: &llm.ToolCallData{
						ID:        "call_1",
						Name:      "read",
						Arguments: json.RawMessage(`{}`),
					},
				},
			},
		},
		FinishReason: llm.FinishReason{Reason: "tool_calls"},
		Usage:        llm.Usage{InputTokens: 400, OutputTokens: 200, TotalTokens: 600},
	}

	textResp := &llm.Response{
		Message:      llm.AssistantMessage("Done."),
		FinishReason: llm.FinishReason{Reason: "stop"},
		Usage:        llm.Usage{InputTokens: 850, OutputTokens: 150, TotalTokens: 1000},
	}

	client := &mockCompleter{
		responses: []*llm.Response{toolCallResp, textResp},
	}

	cfg := DefaultConfig()
	cfg.ContextWindowLimit = 1000
	cfg.ContextWindowWarningThreshold = 0.8

	var events []Event
	handler := EventHandlerFunc(func(evt Event) {
		events = append(events, evt)
	})

	readTool := &stubTool{name: "read", output: "content"}
	sess := mustNewSession(t, client, cfg, WithEventHandler(handler), WithTools(readTool))

	result, err := sess.Run(context.Background(), "Read something")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Check that a context window warning event was emitted.
	var warningEvents []Event
	for _, evt := range events {
		if evt.Type == EventContextWindowWarning {
			warningEvents = append(warningEvents, evt)
		}
	}

	if len(warningEvents) == 0 {
		t.Fatal("expected at least one EventContextWindowWarning event")
	}
	if len(warningEvents) > 1 {
		t.Errorf("expected exactly one warning event, got %d", len(warningEvents))
	}
	if warningEvents[0].ContextUtilization <= 0 {
		t.Error("expected ContextUtilization > 0 in warning event")
	}

	// Verify result has utilization set.
	if result.ContextUtilization <= 0 {
		t.Error("expected ContextUtilization > 0 in result")
	}
}

func TestContextWindowSession_UtilizationInResult(t *testing.T) {
	client := &mockCompleter{
		responses: []*llm.Response{
			{
				Message:      llm.AssistantMessage("Hello"),
				FinishReason: llm.FinishReason{Reason: "stop"},
				Usage:        llm.Usage{InputTokens: 50, OutputTokens: 25, TotalTokens: 75},
			},
		},
	}

	cfg := DefaultConfig()
	cfg.ContextWindowLimit = 1000
	sess := mustNewSession(t, client, cfg)

	result, err := sess.Run(context.Background(), "Hi")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Utilization is based on latest InputTokens only.
	expectedUtil := 50.0 / 1000.0
	if result.ContextUtilization != expectedUtil {
		t.Errorf("expected ContextUtilization %f, got %f", expectedUtil, result.ContextUtilization)
	}
}

func TestSessionConfig_CompactionValidation(t *testing.T) {
	t.Run("valid compaction auto", func(t *testing.T) {
		cfg := DefaultConfig()
		cfg.ContextCompaction = CompactionAuto
		cfg.CompactionThreshold = 0.6
		if err := cfg.Validate(); err != nil {
			t.Errorf("expected valid config: %v", err)
		}
	})

	t.Run("invalid compaction threshold zero", func(t *testing.T) {
		cfg := DefaultConfig()
		cfg.ContextCompaction = CompactionAuto
		cfg.CompactionThreshold = 0
		if err := cfg.Validate(); err == nil {
			t.Error("expected error for CompactionThreshold = 0 when compaction is auto")
		}
	})

	t.Run("invalid compaction threshold above one", func(t *testing.T) {
		cfg := DefaultConfig()
		cfg.ContextCompaction = CompactionAuto
		cfg.CompactionThreshold = 1.1
		if err := cfg.Validate(); err == nil {
			t.Error("expected error for CompactionThreshold > 1.0")
		}
	})

	t.Run("none mode skips threshold validation", func(t *testing.T) {
		cfg := DefaultConfig()
		cfg.ContextCompaction = CompactionNone
		cfg.CompactionThreshold = 0
		if err := cfg.Validate(); err != nil {
			t.Errorf("CompactionNone should not validate threshold: %v", err)
		}
	})
}

func TestSessionConfig_ContextWindowValidation(t *testing.T) {
	t.Run("valid defaults", func(t *testing.T) {
		cfg := DefaultConfig()
		if err := cfg.Validate(); err != nil {
			t.Errorf("default config should be valid: %v", err)
		}
		if cfg.ContextWindowLimit != 200000 {
			t.Errorf("expected default ContextWindowLimit 200000, got %d", cfg.ContextWindowLimit)
		}
		if cfg.ContextWindowWarningThreshold != 0.8 {
			t.Errorf("expected default ContextWindowWarningThreshold 0.8, got %f", cfg.ContextWindowWarningThreshold)
		}
	})

	t.Run("limit too small", func(t *testing.T) {
		cfg := DefaultConfig()
		cfg.ContextWindowLimit = 999
		if err := cfg.Validate(); err == nil {
			t.Error("expected error for ContextWindowLimit < 1000")
		}
	})

	t.Run("threshold zero", func(t *testing.T) {
		cfg := DefaultConfig()
		cfg.ContextWindowWarningThreshold = 0
		if err := cfg.Validate(); err == nil {
			t.Error("expected error for ContextWindowWarningThreshold = 0")
		}
	})

	t.Run("threshold above one", func(t *testing.T) {
		cfg := DefaultConfig()
		cfg.ContextWindowWarningThreshold = 1.1
		if err := cfg.Validate(); err == nil {
			t.Error("expected error for ContextWindowWarningThreshold > 1.0")
		}
	})

	t.Run("threshold exactly one is valid", func(t *testing.T) {
		cfg := DefaultConfig()
		cfg.ContextWindowWarningThreshold = 1.0
		if err := cfg.Validate(); err != nil {
			t.Errorf("threshold 1.0 should be valid: %v", err)
		}
	})
}
