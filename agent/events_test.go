// ABOUTME: Tests for agent event types and the EventHandler interface.
// ABOUTME: Validates event construction and multi-handler fan-out.
package agent

import (
	"testing"
)

func TestEventTypes(t *testing.T) {
	types := []EventType{
		EventSessionStart,
		EventSessionEnd,
		EventTurnStart,
		EventTurnEnd,
		EventToolCallStart,
		EventToolCallEnd,
		EventTextDelta,
		EventError,
		EventContextWindowWarning,
		EventSteeringInjected,
		EventLLMRequestStart,
		EventLLMReasoning,
		EventLLMText,
		EventLLMToolPrepare,
		EventLLMFinish,
		EventLLMProviderRaw,
		EventToolCacheHit,
		EventContextCompaction,
		EventTurnMetrics,
	}
	seen := make(map[EventType]bool)
	for _, et := range types {
		if seen[et] {
			t.Errorf("duplicate event type: %s", et)
		}
		seen[et] = true
	}
}

func TestMultiHandler(t *testing.T) {
	var received []Event
	handler := EventHandlerFunc(func(evt Event) {
		received = append(received, evt)
	})

	multi := MultiHandler(handler, handler)
	multi.HandleEvent(Event{Type: EventTurnStart})

	if len(received) != 2 {
		t.Fatalf("expected 2 events, got %d", len(received))
	}
}

func TestNilHandlerNoPanic(t *testing.T) {
	NoopHandler.HandleEvent(Event{Type: EventTurnStart})
}

func TestMultiHandlerWithNilNoPanic(t *testing.T) {
	var received []Event
	handler := EventHandlerFunc(func(evt Event) {
		received = append(received, evt)
	})

	multi := MultiHandler(handler, nil, handler)
	multi.HandleEvent(Event{Type: EventTurnStart})

	if len(received) != 2 {
		t.Fatalf("expected 2 events (nil skipped), got %d", len(received))
	}
}

// TestNodeScopedHandlerComposesWithAnEnclosingScope pins that nesting the
// stamping handler composes instead of overwriting.
//
// A subgraph's registry factory wraps the parent's agent handler with a
// handler scoped to the subgraph node ("Task02"), and the child's codergen
// handler then wraps that AGAIN with its own node id ("Implement"). Overwriting
// left the event labelled "Implement" — losing the subgraph from the id, so a
// reader could not tell two structurally identical children of different
// subgraph nodes apart, and the scoped id no longer matched the scoped
// pipeline events for the same node ("Task02/Implement").
func TestNodeScopedHandlerComposesWithAnEnclosingScope(t *testing.T) {
	var got []Event
	inner := EventHandlerFunc(func(evt Event) { got = append(got, evt) })

	// Exactly how the registry factory + codergen handler layer them: the
	// subgraph scope is applied first, the node's own scope second.
	h := NodeScopedHandler("Implement", NodeScopedHandler("Task02", inner))
	h.HandleEvent(Event{Type: EventTurnStart})

	if len(got) != 1 {
		t.Fatalf("got %d events, want 1", len(got))
	}
	if got[0].NodeID != "Task02/Implement" {
		t.Errorf("NodeID = %q, want %q (the nested scopes must compose)", got[0].NodeID, "Task02/Implement")
	}
}

// TestNodeScopedHandlerStampsABareNodeID pins the single-scope case, which is a
// top-level agent node: there is no enclosing scope to compose with, so the
// event carries the node id alone.
func TestNodeScopedHandlerStampsABareNodeID(t *testing.T) {
	var got Event
	inner := EventHandlerFunc(func(evt Event) { got = evt })

	NodeScopedHandler("Gen", inner).HandleEvent(Event{Type: EventTurnStart})
	if got.NodeID != "Gen" {
		t.Errorf("NodeID = %q, want %q", got.NodeID, "Gen")
	}
}

// TestNodeScopedHandlerMatchesPipelineScoping pins that the agent-side stamp
// composes exactly as pipeline.NodeScopedPipelineHandler does — prefix whenever
// the event already names a node, adopt the scope when it does not. The two
// handlers are the same rule applied to the two streams, so they are pinned to
// the same expectations; a subgraph child whose id repeats its parent's is
// "Review/Review" in both. Disagreement between them is exactly the bug:
// run.json keys per-node usage by node id, so an agent whose call lines and
// whose decision_outcome rollup land under different ids is counted twice.
// (The agent package cannot import pipeline — pipeline imports agent — so the
// shared rule is spelled out here.)
func TestNodeScopedHandlerMatchesPipelineScoping(t *testing.T) {
	for _, tc := range []struct {
		name     string
		scope    string
		existing string
		want     string
	}{
		{"bare node", "Gen", "", "Gen"},
		// The enclosing subgraph scope is applied last, around the id the
		// child's own codergen wrapper already stamped.
		{"enclosing subgraph", "Task02", "Implement", "Task02/Implement"},
		{"child named like its parent", "Review", "Review", "Review/Review"},
		{"no enclosing scope", "", "Gen", "Gen"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var got Event
			NodeScopedHandler(tc.scope, EventHandlerFunc(func(evt Event) { got = evt })).
				HandleEvent(Event{Type: EventTurnStart, NodeID: tc.existing})
			if got.NodeID != tc.want {
				t.Errorf("NodeID = %q, want %q", got.NodeID, tc.want)
			}
		})
	}
}

// TestNodeScopedHandlerComposesANestedSubgraphPath pins that composition keeps
// working through more than one level, which is what a subgraph inside a
// subgraph produces ("L1/L2/Leaf"), matching the pipeline-event scoping.
func TestNodeScopedHandlerComposesANestedSubgraphPath(t *testing.T) {
	var got Event
	inner := EventHandlerFunc(func(evt Event) { got = evt })

	h := NodeScopedHandler("Leaf", NodeScopedHandler("L2", NodeScopedHandler("L1", inner)))
	h.HandleEvent(Event{Type: EventTurnStart})
	if got.NodeID != "L1/L2/Leaf" {
		t.Errorf("NodeID = %q, want %q", got.NodeID, "L1/L2/Leaf")
	}
}
