// ABOUTME: Tests for the engine-level steering channel that injects context between nodes.
// ABOUTME: Validates that steered values appear in context after the draining node completes.
package pipeline

import (
	"context"
	"testing"
)

func TestEngine_SteeringChan_MergesContext(t *testing.T) {
	// Build a simple graph: start → step1 → step2 → exit.
	// After step1, the steering channel injects "steer_key=steered_value".
	// step2 should see the steered value in its context.
	g := NewGraph("steer_test")
	g.AddNode(&Node{ID: "s", Shape: "Mdiamond", Label: "Start"})
	g.AddNode(&Node{ID: "step1", Shape: "box", Label: "Step1"})
	g.AddNode(&Node{ID: "step2", Shape: "box", Label: "Step2"})
	g.AddNode(&Node{ID: "end", Shape: "Msquare", Label: "End"})
	g.AddEdge(&Edge{From: "s", To: "step1"})
	g.AddEdge(&Edge{From: "step1", To: "step2"})
	g.AddEdge(&Edge{From: "step2", To: "end"})

	// Track what step2 sees in its context.
	var step2SeesSteerKey string

	reg := newTestRegistry()
	reg.Register(&testHandler{
		name: "codergen",
		executeFn: func(ctx context.Context, node *Node, pctx *PipelineContext) (Outcome, error) {
			if node.ID == "step2" {
				val, _ := pctx.Get("steer_key")
				step2SeesSteerKey = val
			}
			return Outcome{Status: OutcomeSuccess}, nil
		},
	})

	// Create a steering channel and pre-load it with a value. The engine
	// drains after each node's applyOutcome (including the start node),
	// so because steerCh is pre-loaded before Run(), the value merges
	// after the start node completes and is visible to both step1 and
	// step2. The assertion below reads it from step2's context view.
	steerCh := make(chan map[string]string, 1)
	steerCh <- map[string]string{"steer_key": "steered_value"}

	engine := NewEngine(g, reg, WithSteeringChan(steerCh))
	result, err := engine.Run(context.Background())
	if err != nil {
		t.Fatalf("engine run failed: %v", err)
	}
	if result.Status != OutcomeSuccess {
		t.Fatalf("expected success, got %q", result.Status)
	}

	if step2SeesSteerKey != "steered_value" {
		t.Errorf("step2 saw steer_key=%q, want %q", step2SeesSteerKey, "steered_value")
	}
}

func TestEngine_SteeringChan_NilIsNoop(t *testing.T) {
	// Verify that a nil steering channel doesn't cause panics.
	g := NewGraph("nil_steer")
	g.AddNode(&Node{ID: "s", Shape: "Mdiamond", Label: "Start"})
	g.AddNode(&Node{ID: "step", Shape: "box", Label: "Step"})
	g.AddNode(&Node{ID: "end", Shape: "Msquare", Label: "End"})
	g.AddEdge(&Edge{From: "s", To: "step"})
	g.AddEdge(&Edge{From: "step", To: "end"})

	reg := newTestRegistry()
	reg.Register(&testHandler{
		name: "codergen",
		executeFn: func(ctx context.Context, node *Node, pctx *PipelineContext) (Outcome, error) {
			return Outcome{Status: OutcomeSuccess}, nil
		},
	})

	engine := NewEngine(g, reg) // no WithSteeringChan
	result, err := engine.Run(context.Background())
	if err != nil {
		t.Fatalf("engine run failed: %v", err)
	}
	if result.Status != OutcomeSuccess {
		t.Fatalf("expected success, got %q", result.Status)
	}
}

func TestEngine_SteeringChan_MultipleUpdates(t *testing.T) {
	// Multiple updates queued before draining should all be merged.
	g := NewGraph("multi_steer")
	g.AddNode(&Node{ID: "s", Shape: "Mdiamond", Label: "Start"})
	g.AddNode(&Node{ID: "step1", Shape: "box", Label: "Step1"})
	g.AddNode(&Node{ID: "step2", Shape: "box", Label: "Step2"})
	g.AddNode(&Node{ID: "end", Shape: "Msquare", Label: "End"})
	g.AddEdge(&Edge{From: "s", To: "step1"})
	g.AddEdge(&Edge{From: "step1", To: "step2"})
	g.AddEdge(&Edge{From: "step2", To: "end"})

	var seenA, seenB string

	reg := newTestRegistry()
	reg.Register(&testHandler{
		name: "codergen",
		executeFn: func(ctx context.Context, node *Node, pctx *PipelineContext) (Outcome, error) {
			if node.ID == "step2" {
				seenA, _ = pctx.Get("key_a")
				seenB, _ = pctx.Get("key_b")
			}
			return Outcome{Status: OutcomeSuccess}, nil
		},
	})

	steerCh := make(chan map[string]string, 2)
	steerCh <- map[string]string{"key_a": "val_a"}
	steerCh <- map[string]string{"key_b": "val_b"}

	engine := NewEngine(g, reg, WithSteeringChan(steerCh))
	result, err := engine.Run(context.Background())
	if err != nil {
		t.Fatalf("engine run failed: %v", err)
	}
	if result.Status != OutcomeSuccess {
		t.Fatalf("expected success, got %q", result.Status)
	}

	if seenA != "val_a" {
		t.Errorf("step2 saw key_a=%q, want %q", seenA, "val_a")
	}
	if seenB != "val_b" {
		t.Errorf("step2 saw key_b=%q, want %q", seenB, "val_b")
	}
}
