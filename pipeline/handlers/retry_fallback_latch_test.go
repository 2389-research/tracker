// ABOUTME: Regression test for #642 — a retry-exhausted fallback_retry_target whose
// ABOUTME: path loops back into the failing node must terminate, not cycle forever.
package handlers

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/2389-research/tracker/pipeline"
)

// TestRetryExhaustedFallbackIsOneShot builds the #642 probe shape:
//
//	A (always OutcomeRetry, max_retries=1, fallback_retry_target=Gate)
//	Gate (human, auto-approved) -> B -> A
//
// Pre-#642 the engine routed A -> Gate on every exhaustion with no latch, so
// the run cycled A -> Gate -> B -> A forever (1921 gate prompts in 120 s in the
// probe). The fallback must be taken at most once per node per run; the second
// exhaustion at A is a hard OutcomeFail terminal.
func TestRetryExhaustedFallbackIsOneShot(t *testing.T) {
	g := pipeline.NewGraph("retry-fallback-latch")
	g.AddNode(&pipeline.Node{ID: "start", Shape: "Mdiamond"})
	g.AddNode(&pipeline.Node{ID: "A", Shape: "box", Attrs: map[string]string{
		"max_retries":           "1",
		"base_delay":            "1ms",
		"fallback_retry_target": "Gate",
	}})
	g.AddNode(&pipeline.Node{ID: "Gate", Shape: "hexagon", Label: "Continue?"})
	g.AddNode(&pipeline.Node{ID: "B", Shape: "box"})
	g.AddNode(&pipeline.Node{ID: "done", Shape: "Msquare"})
	g.AddEdge(&pipeline.Edge{From: "start", To: "A"})
	g.AddEdge(&pipeline.Edge{From: "A", To: "done"})
	g.AddEdge(&pipeline.Edge{From: "Gate", To: "B", Label: "accept"})
	g.AddEdge(&pipeline.Edge{From: "Gate", To: "done", Label: "abort"})
	g.AddEdge(&pipeline.Edge{From: "B", To: "A"})

	var mu sync.Mutex
	aCalls := 0
	codergen := func(ctx context.Context, node *pipeline.Node, pctx *pipeline.PipelineContext) (pipeline.Outcome, error) {
		mu.Lock()
		defer mu.Unlock()
		if node.ID == "A" {
			aCalls++
			return pipeline.Outcome{Status: pipeline.OutcomeRetry}, nil
		}
		return pipeline.Outcome{Status: pipeline.OutcomeSuccess}, nil
	}

	var events []pipeline.PipelineEvent
	emitter := pipeline.PipelineEventHandlerFunc(func(evt pipeline.PipelineEvent) {
		mu.Lock()
		defer mu.Unlock()
		events = append(events, evt)
	})

	reg := NewDefaultRegistry(g, WithCodergenFunc(codergen), WithInterviewer(&AutoApproveInterviewer{}, g), WithPipelineEventHandler(emitter))
	engine := pipeline.NewEngine(g, reg, pipeline.WithPipelineEventHandler(emitter))

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	result, err := engine.Run(ctx)
	if errors.Is(ctx.Err(), context.DeadlineExceeded) {
		t.Fatalf("engine did not terminate: fallback loop A -> Gate -> B -> A cycled until the test deadline (A ran %d times)", aCalls)
	}
	if result == nil {
		t.Fatalf("nil result (err=%v)", err)
	}
	if result.Status != pipeline.OutcomeFail {
		t.Fatalf("status = %q, want %q", result.Status, pipeline.OutcomeFail)
	}

	mu.Lock()
	defer mu.Unlock()
	// Two visits to A: the initial visit (attempt + 1 retry) exhausts and takes
	// the fallback; the loop-back visit re-enters with the retry counter still
	// at the ceiling, exhausts on its first attempt, and trips the latch.
	if aCalls != 3 {
		t.Errorf("A executed %d times, want 3 (2 on the first visit + 1 loop-back)", aCalls)
	}
	gates := gateEventsOfType(events, pipeline.EventGateOpened)
	if len(gates) != 1 {
		t.Errorf("gate_opened fired %d times, want exactly 1 (fallback is one-shot)", len(gates))
	}
	starts := gateEventsOfType(events, pipeline.EventStageStarted)
	if len(starts) > 12 {
		t.Errorf("stage_started fired %d times — unbounded growth", len(starts))
	}
	latched := gateEventsOfType(events, pipeline.EventFallbackLatched)
	if len(latched) != 1 {
		t.Fatalf("fallback_latched fired %d times, want 1", len(latched))
	}
	if latched[0].NodeID != "A" {
		t.Errorf("fallback_latched NodeID = %q, want A", latched[0].NodeID)
	}
	for _, want := range []string{`"A"`, `"Gate"`} {
		if !strings.Contains(latched[0].Message, want) {
			t.Errorf("fallback_latched message %q does not name %s", latched[0].Message, want)
		}
	}
}
