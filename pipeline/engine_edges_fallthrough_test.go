// ABOUTME: Tests for EventConditionalFallthrough — fires when a node has at
// ABOUTME: least one conditional outgoing edge, all evaluate false, and routing
// ABOUTME: falls back to an unconditional edge. Issue #208.
package pipeline

import (
	"context"
	"sync"
	"testing"
)

// buildFallthroughGraph wires: start -> branch (with conditional edge to A,
// unconditional edge to B) -> end. The branch handler's outcome is
// configurable via the returned handler-set closure so each test case can
// flip whether the conditional matches.
func buildFallthroughGraph(t *testing.T, branchOutcome Outcome, conditionalEdgeOnly bool) (*Graph, *HandlerRegistry, *[]PipelineEvent, *sync.Mutex) {
	t.Helper()
	g := NewGraph("test")
	g.StartNode = "start"
	g.ExitNode = "end"
	g.AddNode(&Node{ID: "start", Shape: "Msquare", Handler: "start"})
	g.AddNode(&Node{ID: "branch", Shape: "rectangle", Handler: "tool"})
	g.AddNode(&Node{ID: "A", Shape: "rectangle", Handler: "tool"})
	g.AddNode(&Node{ID: "B", Shape: "rectangle", Handler: "tool"})
	g.AddNode(&Node{ID: "end", Shape: "Msquare", Handler: "exit"})
	g.AddEdge(&Edge{From: "start", To: "branch"})
	g.AddEdge(&Edge{From: "branch", To: "A", Condition: "ctx.outcome = success"})
	if !conditionalEdgeOnly {
		g.AddEdge(&Edge{From: "branch", To: "B"})
	}
	g.AddEdge(&Edge{From: "A", To: "end"})
	g.AddEdge(&Edge{From: "B", To: "end"})

	reg := newTestRegistryWithOutcomes(map[string]Outcome{
		"branch": branchOutcome,
	})

	var mu sync.Mutex
	var events []PipelineEvent
	return g, reg, &events, &mu
}

// All conditional edges evaluate false; routing falls back to an
// unconditional edge. EventConditionalFallthrough must fire.
func TestSelectEdge_AllConditionalsFalse_FiresFallthrough(t *testing.T) {
	g, reg, eventsPtr, mu := buildFallthroughGraph(t,
		Outcome{Status: OutcomeFail, ContextUpdates: map[string]string{"outcome": "fail"}},
		false,
	)
	handler := PipelineEventHandlerFunc(func(evt PipelineEvent) {
		mu.Lock()
		*eventsPtr = append(*eventsPtr, evt)
		mu.Unlock()
	})
	engine := NewEngine(g, reg, WithPipelineEventHandler(handler))
	if _, err := engine.Run(context.Background()); err != nil {
		t.Fatalf("engine run failed: %v", err)
	}

	mu.Lock()
	defer mu.Unlock()
	var fallthroughs []PipelineEvent
	for _, e := range *eventsPtr {
		if e.Type == EventConditionalFallthrough {
			fallthroughs = append(fallthroughs, e)
		}
	}
	if len(fallthroughs) != 1 {
		t.Fatalf("expected exactly 1 EventConditionalFallthrough, got %d", len(fallthroughs))
	}
	ev := fallthroughs[0]
	if ev.NodeID != "branch" {
		t.Errorf("NodeID = %q, want %q", ev.NodeID, "branch")
	}
	if ev.Decision == nil {
		t.Fatal("Decision payload is nil")
	}
	if len(ev.Decision.ConditionsTried) != 1 {
		t.Fatalf("ConditionsTried len = %d, want 1", len(ev.Decision.ConditionsTried))
	}
	tried := ev.Decision.ConditionsTried[0]
	if tried.EdgeTo != "A" {
		t.Errorf("ConditionsTried[0].EdgeTo = %q, want %q", tried.EdgeTo, "A")
	}
	if tried.Condition != "ctx.outcome = success" {
		t.Errorf("ConditionsTried[0].Condition = %q", tried.Condition)
	}
	if ev.Decision.EdgeTo != "B" {
		t.Errorf("fallthrough went to %q, want %q", ev.Decision.EdgeTo, "B")
	}
}

// Conditional edge matches; no fallthrough event.
func TestSelectEdge_ConditionalMatches_NoFallthrough(t *testing.T) {
	g, reg, eventsPtr, mu := buildFallthroughGraph(t,
		Outcome{Status: OutcomeSuccess, ContextUpdates: map[string]string{"outcome": "success"}},
		false,
	)
	handler := PipelineEventHandlerFunc(func(evt PipelineEvent) {
		mu.Lock()
		*eventsPtr = append(*eventsPtr, evt)
		mu.Unlock()
	})
	engine := NewEngine(g, reg, WithPipelineEventHandler(handler))
	if _, err := engine.Run(context.Background()); err != nil {
		t.Fatalf("engine run failed: %v", err)
	}

	mu.Lock()
	defer mu.Unlock()
	for _, e := range *eventsPtr {
		if e.Type == EventConditionalFallthrough {
			t.Errorf("unexpected EventConditionalFallthrough: %+v", e)
		}
	}
}

// No conditional edges exist at all (purely unconditional routing). The
// fallback is intentional design, not a missed routing intent. No event
// must fire.
func TestSelectEdge_NoConditionalEdges_NoFallthrough(t *testing.T) {
	g := NewGraph("test")
	g.StartNode = "start"
	g.ExitNode = "end"
	g.AddNode(&Node{ID: "start", Shape: "Msquare", Handler: "start"})
	g.AddNode(&Node{ID: "mid", Shape: "rectangle", Handler: "tool"})
	g.AddNode(&Node{ID: "end", Shape: "Msquare", Handler: "exit"})
	g.AddEdge(&Edge{From: "start", To: "mid"})
	g.AddEdge(&Edge{From: "mid", To: "end"})

	reg := newTestRegistry()
	var mu sync.Mutex
	var events []PipelineEvent
	handler := PipelineEventHandlerFunc(func(evt PipelineEvent) {
		mu.Lock()
		events = append(events, evt)
		mu.Unlock()
	})
	engine := NewEngine(g, reg, WithPipelineEventHandler(handler))
	if _, err := engine.Run(context.Background()); err != nil {
		t.Fatalf("engine run failed: %v", err)
	}

	mu.Lock()
	defer mu.Unlock()
	for _, e := range events {
		if e.Type == EventConditionalFallthrough {
			t.Errorf("unexpected EventConditionalFallthrough on all-unconditional routing: %+v", e)
		}
	}
}
