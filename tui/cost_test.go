// ABOUTME: Tests for per-node cost tracking and display.
// ABOUTME: Verifies cost delta snapshot, node list suffix, and state accessors.
package tui

import (
	"strings"
	"testing"

	"github.com/2389-research/tracker/llm"
)

func TestNodeCostDelta(t *testing.T) {
	tracker := llm.NewTokenTracker()
	store := NewStateStore(tracker)
	store.SetNodes([]NodeEntry{{ID: "n1"}, {ID: "n2"}})

	// Simulate n1 starting and completing with token usage.
	store.Apply(MsgNodeStarted{NodeID: "n1"})
	// Simulate some token usage by adding directly to tracker.
	tracker.AddUsage("anthropic", llm.Usage{
		InputTokens:   1000,
		OutputTokens:  500,
		TotalTokens:   1500,
		EstimatedCost: 0.05,
	})
	store.Apply(MsgNodeCompleted{NodeID: "n1"})

	cost := store.NodeCost("n1")
	if cost < 0.04 || cost > 0.06 {
		t.Errorf("expected cost ~$0.05, got $%.4f", cost)
	}

	tokens := store.NodeTokens("n1")
	if tokens != 1500 {
		t.Errorf("expected 1500 tokens, got %d", tokens)
	}
}

func TestNodeCostDeltaMultipleNodes(t *testing.T) {
	tracker := llm.NewTokenTracker()
	store := NewStateStore(tracker)
	store.SetNodes([]NodeEntry{{ID: "n1"}, {ID: "n2"}})

	// n1 starts, uses some tokens, completes.
	store.Apply(MsgNodeStarted{NodeID: "n1"})
	tracker.AddUsage("anthropic", llm.Usage{
		EstimatedCost: 0.10,
		TotalTokens:   1000,
	})
	store.Apply(MsgNodeCompleted{NodeID: "n1"})

	// n2 starts, uses more tokens, completes.
	store.Apply(MsgNodeStarted{NodeID: "n2"})
	tracker.AddUsage("anthropic", llm.Usage{
		EstimatedCost: 0.20,
		TotalTokens:   2000,
	})
	store.Apply(MsgNodeCompleted{NodeID: "n2"})

	// n1 should have cost $0.10, n2 should have cost $0.20 (delta from n2 start).
	if c := store.NodeCost("n1"); c < 0.09 || c > 0.11 {
		t.Errorf("expected n1 cost ~$0.10, got $%.4f", c)
	}
	if c := store.NodeCost("n2"); c < 0.19 || c > 0.21 {
		t.Errorf("expected n2 cost ~$0.20, got $%.4f", c)
	}
}

func TestNodeCostUnknownNode(t *testing.T) {
	store := NewStateStore(nil)
	if store.NodeCost("unknown") != 0 {
		t.Error("expected 0 cost for unknown node")
	}
	if store.NodeTokens("unknown") != 0 {
		t.Error("expected 0 tokens for unknown node")
	}
}

func TestNodeCostNoTracker(t *testing.T) {
	store := NewStateStore(nil) // nil tracker
	store.SetNodes([]NodeEntry{{ID: "n1"}})
	store.Apply(MsgNodeStarted{NodeID: "n1"})
	store.Apply(MsgNodeCompleted{NodeID: "n1"})

	// Should not panic, cost should be 0.
	if store.NodeCost("n1") != 0 {
		t.Error("expected 0 cost with nil tracker")
	}
}

func TestNodeListCostSuffix(t *testing.T) {
	tracker := llm.NewTokenTracker()
	store := NewStateStore(tracker)
	store.SetNodes([]NodeEntry{{ID: "n1", Label: "Agent1"}})

	store.Apply(MsgNodeStarted{NodeID: "n1"})
	tracker.AddUsage("anthropic", llm.Usage{
		EstimatedCost: 0.15,
		TotalTokens:   3000,
	})
	store.Apply(MsgNodeCompleted{NodeID: "n1"})

	tr := NewThinkingTracker()
	nl := NewNodeList(store, tr, 20)
	nl.SetSize(40, 20)

	view := nl.View()
	if !strings.Contains(view, "$0.15") {
		t.Errorf("expected cost suffix '$0.15' in node list, got: %s", view)
	}
}

func TestNodeListNoCostBelowThreshold(t *testing.T) {
	tracker := llm.NewTokenTracker()
	store := NewStateStore(tracker)
	store.SetNodes([]NodeEntry{{ID: "n1", Label: "Agent1"}})

	store.Apply(MsgNodeStarted{NodeID: "n1"})
	tracker.AddUsage("anthropic", llm.Usage{
		EstimatedCost: 0.0001, // Below $0.001 threshold
		TotalTokens:   10,
	})
	store.Apply(MsgNodeCompleted{NodeID: "n1"})

	tr := NewThinkingTracker()
	nl := NewNodeList(store, tr, 20)
	nl.SetSize(40, 20)

	view := nl.View()
	if strings.Contains(view, "$") {
		t.Errorf("expected no cost suffix below threshold, got: %s", view)
	}
}
