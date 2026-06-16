// ABOUTME: Tests for per-node cost ceiling and no-progress detector in the codergen handler (#304).
// ABOUTME: Validates that NodeCostExceeded and NoProgressDetected result signals route to OutcomeRetry.
package handlers

import (
	"context"
	"testing"

	"github.com/2389-research/tracker/llm"
	"github.com/2389-research/tracker/pipeline"
)

// truncatedCostResponse builds a length-truncated LLM response with the given estimated cost.
// FinishReason "length" keeps the session loop running (no natural stop).
func truncatedCostResponse(estimatedCost float64) *llm.Response {
	return &llm.Response{
		Message:      llm.AssistantMessage("partial work..."),
		FinishReason: llm.FinishReason{Reason: "length"},
		Usage: llm.Usage{
			EstimatedCost: estimatedCost,
			InputTokens:   100,
			OutputTokens:  50,
			TotalTokens:   150,
		},
	}
}

func TestCodergenNodeCostExceededRoutesToRetry(t *testing.T) {
	// Two turns at $0.006 each → $0.012 > $0.01 limit. Guard fires after turn 2.
	client := &scriptedCompleter{responses: []*llm.Response{
		truncatedCostResponse(0.006),
		truncatedCostResponse(0.006),
		// third response never reached
	}}
	h := NewCodergenHandler(client, t.TempDir())
	node := &pipeline.Node{
		ID: "gen", Shape: "box", Handler: "codergen",
		Attrs: map[string]string{
			"prompt":       "do something",
			"max_cost_usd": "0.01",
		},
	}
	pctx := pipeline.NewPipelineContext()
	outcome, err := h.Execute(context.Background(), node, pctx)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if outcome.Status != string(pipeline.OutcomeRetry) {
		t.Errorf("want %q on cost limit exceeded, got %q", pipeline.OutcomeRetry, outcome.Status)
	}
}

func TestCodergenNoProgressDetectedRoutesToRetry(t *testing.T) {
	// no_progress_turns=2: two consecutive truncated turns without tool calls → no-progress fires.
	client := &scriptedCompleter{responses: []*llm.Response{
		truncatedCostResponse(0.0001),
		truncatedCostResponse(0.0001),
		// third response never reached
	}}
	h := NewCodergenHandler(client, t.TempDir())
	node := &pipeline.Node{
		ID: "gen", Shape: "box", Handler: "codergen",
		Attrs: map[string]string{
			"prompt":            "do something",
			"no_progress_turns": "2",
		},
	}
	pctx := pipeline.NewPipelineContext()
	outcome, err := h.Execute(context.Background(), node, pctx)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if outcome.Status != string(pipeline.OutcomeRetry) {
		t.Errorf("want %q on no-progress detection, got %q", pipeline.OutcomeRetry, outcome.Status)
	}
}

func TestCodergenNodeCostExceededContextKey(t *testing.T) {
	// The node_cost_exceeded context key should be set so pipelines can route on it.
	client := &scriptedCompleter{responses: []*llm.Response{
		truncatedCostResponse(0.1),
		truncatedCostResponse(0.1),
	}}
	h := NewCodergenHandler(client, t.TempDir())
	node := &pipeline.Node{
		ID: "gen", Shape: "box", Handler: "codergen",
		Attrs: map[string]string{
			"prompt":       "do something",
			"max_cost_usd": "0.05",
		},
	}
	pctx := pipeline.NewPipelineContext()
	outcome, err := h.Execute(context.Background(), node, pctx)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if outcome.ContextUpdates[pipeline.ContextKeyNodeCostExceeded] != "true" {
		t.Errorf("want node_cost_exceeded=true in context updates, got %q", outcome.ContextUpdates[pipeline.ContextKeyNodeCostExceeded])
	}
}

func TestCodergenGraphDefaultMaxCostUSD(t *testing.T) {
	// Graph-level default max_cost_usd applies when not overridden per-node.
	client := &scriptedCompleter{responses: []*llm.Response{
		truncatedCostResponse(0.006),
		truncatedCostResponse(0.006),
	}}
	h := NewCodergenHandler(client, t.TempDir(), WithGraphAttrs(map[string]string{
		"max_cost_usd": "0.01",
	}))
	node := &pipeline.Node{
		ID: "gen", Shape: "box", Handler: "codergen",
		Attrs: map[string]string{"prompt": "do something"},
		// No per-node max_cost_usd — inherits from graph
	}
	pctx := pipeline.NewPipelineContext()
	outcome, err := h.Execute(context.Background(), node, pctx)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if outcome.Status != string(pipeline.OutcomeRetry) {
		t.Errorf("graph-level max_cost_usd: want %q, got %q", pipeline.OutcomeRetry, outcome.Status)
	}
}

func TestCodergenNodeZeroMaxCostUSDDisablesGraphDefault(t *testing.T) {
	// A per-node max_cost_usd: "0" must disable an inherited graph-level default.
	// The session runs all responses and succeeds without hitting the guard.
	client := &scriptedCompleter{responses: []*llm.Response{
		truncatedCostResponse(0.006),
		truncatedCostResponse(0.006),
		{Message: llm.AssistantMessage("done"), FinishReason: llm.FinishReason{Reason: "stop"}},
	}}
	h := NewCodergenHandler(client, t.TempDir(), WithGraphAttrs(map[string]string{
		"max_cost_usd": "0.01", // graph-level ceiling
	}))
	node := &pipeline.Node{
		ID: "gen", Shape: "box", Handler: "codergen",
		Attrs: map[string]string{
			"prompt":       "do something",
			"max_cost_usd": "0", // node explicitly disables the graph default
		},
	}
	pctx := pipeline.NewPipelineContext()
	outcome, err := h.Execute(context.Background(), node, pctx)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if outcome.Status != string(pipeline.OutcomeSuccess) {
		t.Errorf("node max_cost_usd=0 should disable graph default: want success, got %q", outcome.Status)
	}
}

// stubPipelineEmitter records every pipeline event it receives.
type stubPipelineEmitter struct {
	events []pipeline.PipelineEvent
}

func (s *stubPipelineEmitter) HandlePipelineEvent(evt pipeline.PipelineEvent) {
	s.events = append(s.events, evt)
}

func TestCodergenNodeCostExceededEmitsPipelineEvent(t *testing.T) {
	client := &scriptedCompleter{responses: []*llm.Response{
		truncatedCostResponse(0.006),
		truncatedCostResponse(0.006),
	}}
	emitter := &stubPipelineEmitter{}
	h := NewCodergenHandler(client, t.TempDir(), WithPipelineEmitter(emitter))
	node := &pipeline.Node{
		ID: "gen", Shape: "box", Handler: "codergen",
		Attrs: map[string]string{"prompt": "do something", "max_cost_usd": "0.01"},
	}
	pctx := pipeline.NewPipelineContext()
	_, err := h.Execute(context.Background(), node, pctx)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	var found bool
	for _, evt := range emitter.events {
		if evt.Type == pipeline.EventNodeCostLimitExceeded && evt.NodeID == "gen" && !evt.Timestamp.IsZero() {
			found = true
		}
	}
	if !found {
		t.Errorf("expected EventNodeCostLimitExceeded with NodeID=gen and non-zero Timestamp; got events: %v", emitter.events)
	}
}

func TestCodergenNoProgressEmitsPipelineEvent(t *testing.T) {
	client := &scriptedCompleter{responses: []*llm.Response{
		truncatedCostResponse(0.0001),
		truncatedCostResponse(0.0001),
	}}
	emitter := &stubPipelineEmitter{}
	h := NewCodergenHandler(client, t.TempDir(), WithPipelineEmitter(emitter))
	node := &pipeline.Node{
		ID: "gen", Shape: "box", Handler: "codergen",
		Attrs: map[string]string{"prompt": "do something", "no_progress_turns": "2"},
	}
	pctx := pipeline.NewPipelineContext()
	_, err := h.Execute(context.Background(), node, pctx)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	var found bool
	for _, evt := range emitter.events {
		if evt.Type == pipeline.EventNodeNoProgressDetected && evt.NodeID == "gen" && !evt.Timestamp.IsZero() {
			found = true
		}
	}
	if !found {
		t.Errorf("expected EventNodeNoProgressDetected with NodeID=gen and non-zero Timestamp; got events: %v", emitter.events)
	}
}
