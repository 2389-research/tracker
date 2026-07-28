// ABOUTME: Tests for session observability features: turn metrics emission, tool timing, and cost estimation.
// ABOUTME: Validates that the session loop emits TurnMetrics events and populates result fields.
package agent

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/2389-research/tracker/llm"
)

func TestSession_EmitsTurnMetrics(t *testing.T) {
	client := &mockCompleter{
		responses: []*llm.Response{
			{
				Message:      llm.AssistantMessage("Hello!"),
				FinishReason: llm.FinishReason{Reason: "stop"},
				Usage:        llm.Usage{InputTokens: 100, OutputTokens: 20, TotalTokens: 120},
			},
		},
	}

	var events []Event
	handler := EventHandlerFunc(func(evt Event) {
		events = append(events, evt)
	})

	cfg := DefaultConfig()
	sess := mustNewSession(t, client, cfg, WithEventHandler(handler))
	_, err := sess.Run(context.Background(), "Say hello")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	var metricsEvents []Event
	for _, e := range events {
		if e.Type == EventTurnMetrics {
			metricsEvents = append(metricsEvents, e)
		}
	}

	if len(metricsEvents) != 1 {
		t.Fatalf("expected exactly 1 turn_metrics event, got %d", len(metricsEvents))
	}

	m := metricsEvents[0].Metrics
	if m == nil {
		t.Fatal("metrics is nil")
	}
	if m.InputTokens != 100 {
		t.Errorf("expected InputTokens=100, got %d", m.InputTokens)
	}
	if m.OutputTokens != 20 {
		t.Errorf("expected OutputTokens=20, got %d", m.OutputTokens)
	}
}

// TestSession_TurnMetricsCarryAttribution pins issue #508: a turn_metrics event
// must carry the same top-level Provider/Model/Usage attribution as llm_finish,
// so an event-stream consumer building per-turn cost rollups off turn_metrics
// does not silently read zeros.
func TestSession_TurnMetricsCarryAttribution(t *testing.T) {
	client := &mockCompleter{
		responses: []*llm.Response{
			{
				Model:        "claude-sonnet-4-6",
				Provider:     "anthropic",
				Message:      llm.AssistantMessage("Hello!"),
				FinishReason: llm.FinishReason{Reason: "stop"},
				Usage:        llm.Usage{InputTokens: 100, OutputTokens: 20, TotalTokens: 120},
			},
		},
	}

	var events []Event
	handler := EventHandlerFunc(func(evt Event) { events = append(events, evt) })

	sess := mustNewSession(t, client, DefaultConfig(), WithEventHandler(handler))
	if _, err := sess.Run(context.Background(), "Say hello"); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	var evt *Event
	for i := range events {
		if events[i].Type == EventTurnMetrics {
			evt = &events[i]
		}
	}
	if evt == nil {
		t.Fatal("no turn_metrics event emitted")
	}
	if evt.Model != "claude-sonnet-4-6" {
		t.Errorf("Model = %q, want claude-sonnet-4-6", evt.Model)
	}
	if evt.Provider != "anthropic" {
		t.Errorf("Provider = %q, want anthropic", evt.Provider)
	}
	if evt.Usage.InputTokens != 100 || evt.Usage.OutputTokens != 20 || evt.Usage.TotalTokens != 120 {
		t.Errorf("Usage = %+v, want input=100 output=20 total=120", evt.Usage)
	}
}

// TestSession_TurnMetricsAttributionFallsBackToConfig covers adapters that leave
// Response.Model/Provider unset: the configured model/provider is the correct
// attribution, and an empty string would be indistinguishable from "unknown".
func TestSession_TurnMetricsAttributionFallsBackToConfig(t *testing.T) {
	client := &mockCompleter{
		responses: []*llm.Response{
			{
				Message:      llm.AssistantMessage("Hello!"),
				FinishReason: llm.FinishReason{Reason: "stop"},
				Usage:        llm.Usage{InputTokens: 5, OutputTokens: 1, TotalTokens: 6},
			},
		},
	}

	var events []Event
	handler := EventHandlerFunc(func(evt Event) { events = append(events, evt) })

	cfg := DefaultConfig()
	cfg.Model = "gpt-5"
	cfg.Provider = "openai"
	sess := mustNewSession(t, client, cfg, WithEventHandler(handler))
	if _, err := sess.Run(context.Background(), "Say hello"); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	for _, e := range events {
		if e.Type != EventTurnMetrics {
			continue
		}
		if e.Model != "gpt-5" {
			t.Errorf("Model = %q, want gpt-5", e.Model)
		}
		if e.Provider != "openai" {
			t.Errorf("Provider = %q, want openai", e.Provider)
		}
		return
	}
	t.Fatal("no turn_metrics event emitted")
}

func TestSession_ToolCallEndHasDuration(t *testing.T) {
	toolCallResp := &llm.Response{
		Message: llm.Message{
			Role: llm.RoleAssistant,
			Content: []llm.ContentPart{
				{
					Kind: llm.KindToolCall,
					ToolCall: &llm.ToolCallData{
						ID:        "call_1",
						Name:      "read",
						Arguments: json.RawMessage(`{"path": "test.txt"}`),
					},
				},
			},
		},
		FinishReason: llm.FinishReason{Reason: "tool_calls"},
		Usage:        llm.Usage{InputTokens: 20, OutputTokens: 10, TotalTokens: 30},
	}

	textResp := &llm.Response{
		Message:      llm.AssistantMessage("Done."),
		FinishReason: llm.FinishReason{Reason: "stop"},
		Usage:        llm.Usage{InputTokens: 30, OutputTokens: 8, TotalTokens: 38},
	}

	client := &mockCompleter{
		responses: []*llm.Response{toolCallResp, textResp},
	}

	var events []Event
	handler := EventHandlerFunc(func(evt Event) {
		events = append(events, evt)
	})

	cfg := DefaultConfig()
	readTool := &stubTool{name: "read", output: "file contents"}
	sess := mustNewSession(t, client, cfg, WithEventHandler(handler), WithTools(readTool))

	_, err := sess.Run(context.Background(), "Read test.txt")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	found := false
	for _, e := range events {
		if e.Type == EventToolCallEnd && e.ToolName == "read" {
			found = true
			if e.ToolDuration <= 0 {
				t.Errorf("expected ToolDuration > 0 for non-cached tool call, got %v", e.ToolDuration)
			}
		}
	}
	if !found {
		t.Error("expected at least one EventToolCallEnd for 'read'")
	}
}

func TestSession_ResultHasToolTimings(t *testing.T) {
	toolCallResp := &llm.Response{
		Message: llm.Message{
			Role: llm.RoleAssistant,
			Content: []llm.ContentPart{
				{
					Kind: llm.KindToolCall,
					ToolCall: &llm.ToolCallData{
						ID:        "call_1",
						Name:      "read",
						Arguments: json.RawMessage(`{"path": "test.txt"}`),
					},
				},
			},
		},
		FinishReason: llm.FinishReason{Reason: "tool_calls"},
		Usage:        llm.Usage{InputTokens: 20, OutputTokens: 10, TotalTokens: 30},
	}

	textResp := &llm.Response{
		Message:      llm.AssistantMessage("Done."),
		FinishReason: llm.FinishReason{Reason: "stop"},
		Usage:        llm.Usage{InputTokens: 30, OutputTokens: 8, TotalTokens: 38},
	}

	client := &mockCompleter{
		responses: []*llm.Response{toolCallResp, textResp},
	}

	cfg := DefaultConfig()
	readTool := &stubTool{name: "read", output: "file contents"}
	sess := mustNewSession(t, client, cfg, WithTools(readTool))

	result, err := sess.Run(context.Background(), "Read test.txt")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if result.ToolTimings == nil {
		t.Fatal("expected ToolTimings to be non-nil")
	}
	if _, ok := result.ToolTimings["read"]; !ok {
		t.Error("expected ToolTimings to contain 'read' key")
	}
	if result.LongestTurn <= 0 {
		t.Errorf("expected LongestTurn > 0, got %v", result.LongestTurn)
	}
}

func TestSession_ResultHasCostEstimate(t *testing.T) {
	client := &mockCompleter{
		responses: []*llm.Response{
			{
				Message:      llm.AssistantMessage("Done."),
				FinishReason: llm.FinishReason{Reason: "stop"},
				Usage:        llm.Usage{InputTokens: 100000, OutputTokens: 10000, TotalTokens: 110000},
			},
		},
	}

	cfg := DefaultConfig()
	cfg.Model = "claude-sonnet-4-5"
	sess := mustNewSession(t, client, cfg)

	result, err := sess.Run(context.Background(), "Expensive query")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Expected: 0.1M * $3 + 0.01M * $15 = $0.30 + $0.15 = $0.45
	cost := result.Usage.EstimatedCost
	if cost < 0.40 || cost > 0.50 {
		t.Errorf("expected cost in range [0.40, 0.50], got %.4f", cost)
	}
}
