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
