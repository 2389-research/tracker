// ABOUTME: Tests for the #601 session Disposition — the single authoritative stop
// ABOUTME: reason — its precedence, and the empty-after-tool-call fail-loud fix.
package agent

import (
	"context"
	"errors"
	"testing"

	"github.com/2389-research/tracker/llm"
)

// TestDeriveDisposition_Precedence pins the centralized precedence: exactly one
// stop reason wins per signal combination, error-class first, then #304 guards,
// then a detected loop (which outranks the max-turns flag it also sets), then
// plain exhaustion, then a clean completion.
func TestDeriveDisposition_Precedence(t *testing.T) {
	cases := []struct {
		name string
		r    SessionResult
		want Disposition
	}{
		{"clean", SessionResult{}, DispositionCompleted},
		{"max-turns", SessionResult{MaxTurnsUsed: true}, DispositionMaxTurns},
		// The loop path sets BOTH LoopDetected and MaxTurnsUsed; the more specific
		// reason must win.
		{"loop-outranks-maxturns", SessionResult{MaxTurnsUsed: true, LoopDetected: true}, DispositionLoopDetected},
		{"node-cost", SessionResult{NodeCostExceeded: true}, DispositionNodeCostExceeded},
		{"no-progress", SessionResult{NoProgressDetected: true}, DispositionNoProgress},
		{"empty-response", SessionResult{Error: &errEmptyResponse{count: 3}}, DispositionEmptyResponse},
		{"generic-error", SessionResult{Error: errors.New("boom")}, DispositionError},
		// Guards take precedence over a generic error only when Error is unset;
		// when a hard error is present it names the stop.
		{"error-outranks-maxturns", SessionResult{Error: errors.New("boom"), MaxTurnsUsed: true}, DispositionError},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			r := tc.r
			if got := deriveDisposition(&r); got != tc.want {
				t.Errorf("deriveDisposition = %v, want %v", got, tc.want)
			}
		})
	}
}

// TestSession_DispositionNaturalCompletion pins that a clean text stop reports
// DispositionCompleted.
func TestSession_DispositionNaturalCompletion(t *testing.T) {
	client := &mockCompleter{responses: []*llm.Response{
		{Message: llm.AssistantMessage("all done"), FinishReason: llm.FinishReason{Reason: "stop"}, Usage: llm.Usage{OutputTokens: 3}},
	}}
	sess := mustNewSession(t, client, DefaultConfig())
	result, err := sess.Run(context.Background(), "go")
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if result.Disposition != DispositionCompleted {
		t.Errorf("Disposition = %v, want completed", result.Disposition)
	}
}

// TestSession_DispositionLoopDetected pins that a detected tool-call loop reports
// DispositionLoopDetected even though the legacy MaxTurnsUsed flag is also set.
func TestSession_DispositionLoopDetected(t *testing.T) {
	threshold := 3
	responses := make([]*llm.Response, threshold+2)
	for i := range responses {
		responses[i] = makeToolCallResponse("read")
	}
	cfg := DefaultConfig()
	cfg.MaxTurns = 50
	cfg.LoopDetectionThreshold = threshold
	sess := mustNewSession(t, &mockCompleter{responses: responses}, cfg, WithTools(&stubTool{name: "read", output: "x"}))
	result, err := sess.Run(context.Background(), "go")
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if result.Disposition != DispositionLoopDetected {
		t.Errorf("Disposition = %v, want loop_detected", result.Disposition)
	}
	if !result.MaxTurnsUsed || !result.LoopDetected {
		t.Errorf("expected legacy flags MaxTurnsUsed && LoopDetected both set")
	}
}

// TestSession_DispositionMaxTurns pins plain turn-budget exhaustion.
func TestSession_DispositionMaxTurns(t *testing.T) {
	// Distinct tool calls every turn so loop detection never trips; the session
	// simply runs out of turns.
	responses := []*llm.Response{
		makeToolCallResponse("a"), makeToolCallResponse("b"),
	}
	cfg := DefaultConfig()
	cfg.MaxTurns = 2
	sess := mustNewSession(t, &mockCompleter{responses: responses}, cfg,
		WithTools(&stubTool{name: "a", output: "x"}, &stubTool{name: "b", output: "y"}))
	result, err := sess.Run(context.Background(), "go")
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if result.Disposition != DispositionMaxTurns {
		t.Errorf("Disposition = %v, want max_turns", result.Disposition)
	}
}

// TestSession_EmptyResponseAfterToolCallFails is the #601 correctness regression:
// an empty provider response after earlier tool work must FAIL LOUDLY, not be
// accepted as a clean stop. Before the fix the empty-response check was gated on
// session-total tool calls, so a later empty response silently became success.
func TestSession_EmptyResponseAfterToolCallFails(t *testing.T) {
	empty := &llm.Response{Message: llm.Message{Role: llm.RoleAssistant}, FinishReason: llm.FinishReason{Reason: "stop"}}
	client := &mockCompleter{responses: []*llm.Response{
		makeToolCallResponse("work"), // real tool work first
		empty, empty, empty,          // then the provider goes empty (initial + 2 retries)
	}}
	cfg := DefaultConfig()
	cfg.MaxTurns = 10
	sess := mustNewSession(t, client, cfg, WithTools(&stubTool{name: "work", output: "ok"}))
	result, err := sess.Run(context.Background(), "go")
	if err == nil {
		t.Fatal("expected empty-after-tool-call to fail, got nil error")
	}
	if !isEmptyResponseError(err) {
		t.Errorf("error = %v, want an empty-response error", err)
	}
	if result.Disposition != DispositionEmptyResponse {
		t.Errorf("Disposition = %v, want empty_response", result.Disposition)
	}
	// The prior tool work must still be reflected in the stats — the failure does
	// not erase it.
	if result.ToolCalls["work"] == 0 {
		t.Errorf("expected the pre-empty tool call to be recorded, got %v", result.ToolCalls)
	}
}

// TestSession_DispositionNodeCostExceeded pins the #304 per-node cost ceiling
// disposition.
func TestSession_DispositionNodeCostExceeded(t *testing.T) {
	costly := makeToolCallResponse("work")
	costly.Usage = llm.Usage{InputTokens: 10, OutputTokens: 5, EstimatedCost: 1.0}
	cfg := DefaultConfig()
	cfg.MaxTurns = 10
	cfg.MaxCostUSD = 0.01
	sess := mustNewSession(t, &mockCompleter{responses: []*llm.Response{costly}}, cfg,
		WithTools(&stubTool{name: "work", output: "ok"}))
	result, err := sess.Run(context.Background(), "go")
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if result.Disposition != DispositionNodeCostExceeded {
		t.Errorf("Disposition = %v, want node_cost_exceeded", result.Disposition)
	}
}

// TestSession_DispositionNoProgress pins the #304 no-progress detector
// disposition: consecutive turns that make no tool-call progress trip it.
func TestSession_DispositionNoProgress(t *testing.T) {
	// Length-truncated text turns inject a continuation and loop again without any
	// tool call, so the no-progress counter advances without a natural stop.
	stuck := &llm.Response{Message: llm.AssistantMessage("still working"), FinishReason: llm.FinishReason{Reason: "length"}}
	cfg := DefaultConfig()
	cfg.MaxTurns = 10
	cfg.NoProgressTurns = 2
	sess := mustNewSession(t, &mockCompleter{responses: []*llm.Response{stuck, stuck}}, cfg)
	result, err := sess.Run(context.Background(), "go")
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if result.Disposition != DispositionNoProgress {
		t.Errorf("Disposition = %v, want no_progress", result.Disposition)
	}
}
