// ABOUTME: Tests for per-node cost ceiling and no-progress detector guards (#304).
// ABOUTME: Validates that the session halts and sets the correct result flags.
package agent

import (
	"context"
	"testing"

	"github.com/2389-research/tracker/llm"
)

// lengthTruncatedResponse returns a response with the given estimated cost
// and FinishReason "length" so the session loop continues without stopping naturally.
func lengthTruncatedResponse(estimatedCost float64) *llm.Response {
	return &llm.Response{
		Message:      llm.AssistantMessage("still working..."),
		FinishReason: llm.FinishReason{Reason: "length"},
		Usage: llm.Usage{
			EstimatedCost: estimatedCost,
			InputTokens:   100,
			OutputTokens:  50,
			TotalTokens:   150,
		},
	}
}

func TestSessionNodeCostExceededHaltsLoop(t *testing.T) {
	// Two turns at $0.006 each → cumulative $0.012 > $0.01 limit after turn 2.
	// The third response should never be reached.
	client := &mockCompleter{responses: []*llm.Response{
		lengthTruncatedResponse(0.006),
		lengthTruncatedResponse(0.006),
		{Message: llm.AssistantMessage("done"), FinishReason: llm.FinishReason{Reason: "stop"}},
	}}

	cfg := DefaultConfig()
	cfg.MaxCostUSD = 0.01
	cfg.MaxTurns = 10

	sess := mustNewSession(t, client, cfg)
	result, err := sess.Run(context.Background(), "do work")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !result.NodeCostExceeded {
		t.Error("NodeCostExceeded should be true when cost exceeds MaxCostUSD")
	}
	if result.NoProgressDetected {
		t.Error("NoProgressDetected should not be set when cost guard fires")
	}
	if result.MaxTurnsUsed {
		t.Error("MaxTurnsUsed should not be set when cost guard fires — guard stop is not turn exhaustion")
	}
	if result.Usage.EstimatedCost <= cfg.MaxCostUSD {
		t.Errorf("cost %.4f should exceed limit %.4f", result.Usage.EstimatedCost, cfg.MaxCostUSD)
	}
	// Client should have been called exactly twice (cost guard fires after turn 2).
	if client.calls != 2 {
		t.Errorf("expected 2 LLM calls, got %d", client.calls)
	}
}

func TestSessionNodeCostExceededNotSetWhenLimitUnset(t *testing.T) {
	// With MaxCostUSD=0 (unset), the cost guard is disabled — even expensive
	// responses should not set NodeCostExceeded.
	client := &mockCompleter{responses: []*llm.Response{
		lengthTruncatedResponse(0.999),
		{Message: llm.AssistantMessage("done"), FinishReason: llm.FinishReason{Reason: "stop"}},
	}}

	cfg := DefaultConfig()
	cfg.MaxCostUSD = 0 // disabled
	cfg.MaxTurns = 5

	sess := mustNewSession(t, client, cfg)
	result, err := sess.Run(context.Background(), "do work")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.NodeCostExceeded {
		t.Error("NodeCostExceeded should not be set when MaxCostUSD is 0")
	}
}

func TestSessionNoProgressDetectedAfterKTurns(t *testing.T) {
	// K=2: two consecutive length-truncated turns with no tool calls → no progress.
	client := &mockCompleter{responses: []*llm.Response{
		lengthTruncatedResponse(0.0001),
		lengthTruncatedResponse(0.0001),
		{Message: llm.AssistantMessage("done"), FinishReason: llm.FinishReason{Reason: "stop"}},
	}}

	cfg := DefaultConfig()
	cfg.NoProgressTurns = 2
	cfg.MaxTurns = 10
	cfg.MaxCostUSD = 0 // disable cost guard so no-progress fires first

	sess := mustNewSession(t, client, cfg)
	result, err := sess.Run(context.Background(), "do work")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !result.NoProgressDetected {
		t.Error("NoProgressDetected should be true after K consecutive no-tool turns")
	}
	if result.NodeCostExceeded {
		t.Error("NodeCostExceeded should not be set when no-progress guard fires")
	}
	if result.MaxTurnsUsed {
		t.Error("MaxTurnsUsed should not be set when no-progress guard fires — guard stop is not turn exhaustion")
	}
	// Guard fires after K=2 turns; third response never reached.
	if client.calls != 2 {
		t.Errorf("expected 2 LLM calls, got %d", client.calls)
	}
}

func TestSessionNoProgressNotSetWhenLimitUnset(t *testing.T) {
	// With NoProgressTurns=0 (unset), the no-progress guard is disabled.
	client := &mockCompleter{responses: []*llm.Response{
		lengthTruncatedResponse(0.0001),
		lengthTruncatedResponse(0.0001),
		{Message: llm.AssistantMessage("done"), FinishReason: llm.FinishReason{Reason: "stop"}},
	}}

	cfg := DefaultConfig()
	cfg.NoProgressTurns = 0 // disabled
	cfg.MaxTurns = 10

	sess := mustNewSession(t, client, cfg)
	result, err := sess.Run(context.Background(), "do work")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.NoProgressDetected {
		t.Error("NoProgressDetected should not be set when NoProgressTurns is 0")
	}
}

func TestSessionNoProgressNotTriggeredByEmptyResponseRetry(t *testing.T) {
	// An empty API response causes the session to retry (not a progress failure).
	// With NoProgressTurns=1, the retry turn must NOT count as a no-progress turn.
	empty := &llm.Response{
		Message:      llm.Message{Role: llm.RoleAssistant},
		FinishReason: llm.FinishReason{Reason: "stop"},
		Usage:        llm.Usage{OutputTokens: 0},
	}
	done := &llm.Response{
		Message:      llm.AssistantMessage("done"),
		FinishReason: llm.FinishReason{Reason: "stop"},
		Usage:        llm.Usage{OutputTokens: 10},
	}
	client := &mockCompleter{responses: []*llm.Response{empty, done}}

	cfg := DefaultConfig()
	cfg.NoProgressTurns = 1 // very aggressive — fires on first no-tool turn
	cfg.MaxTurns = 10

	sess := mustNewSession(t, client, cfg)
	result, err := sess.Run(context.Background(), "do work")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.NoProgressDetected {
		t.Error("NoProgressDetected should not fire during empty-response retry sequence")
	}
}

func TestSessionNoProgressResetsWhenToolsCalled(t *testing.T) {
	// Tool calls reset the no-progress counter. Pattern: no-tool, tool-call,
	// no-tool — only one consecutive no-tool turn before and after the tool
	// turn; with K=2, the guard never fires.
	toolResp := &llm.Response{
		Message: llm.Message{
			Role: llm.RoleAssistant,
			Content: []llm.ContentPart{{
				Kind: llm.KindToolCall,
				ToolCall: &llm.ToolCallData{
					ID:        "t1",
					Name:      "read",
					Arguments: []byte(`{"path":"no_such_file.txt"}`),
				},
			}},
		},
		FinishReason: llm.FinishReason{Reason: "tool_calls"},
		Usage:        llm.Usage{EstimatedCost: 0.0001},
	}
	client := &mockCompleter{responses: []*llm.Response{
		lengthTruncatedResponse(0.0001), // turn 1: no tool, counter=1
		toolResp,                        // turn 2: tool called, counter reset to 0; loop continues due to error response from tool
		lengthTruncatedResponse(0.0001), // turn 3: no tool, counter=1
		{Message: llm.AssistantMessage("done"), FinishReason: llm.FinishReason{Reason: "stop"}},
	}}

	cfg := DefaultConfig()
	cfg.NoProgressTurns = 2
	cfg.MaxTurns = 10

	sess := mustNewSession(t, client, cfg)
	result, err := sess.Run(context.Background(), "do work")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.NoProgressDetected {
		t.Error("NoProgressDetected should not fire when tool calls reset the counter")
	}
}
