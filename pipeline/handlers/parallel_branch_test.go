// ABOUTME: Regression tests for branch-context run-id propagation and billing
// ABOUTME: pause propagation in the parallel fan-out handler (bug-hunt findings).
package handlers

import (
	"context"
	"errors"
	"sync"
	"testing"

	"github.com/2389-research/tracker/pipeline"
)

// A branch target's run id must not be empty: Snapshot()/NewPipelineContextFrom
// copy only the values namespace, so runBranch must propagate InternalKeyRunID
// onto branchCtx or the branch handler stamps events with an empty run_id.
func TestParallelPropagatesRunIDToBranches(t *testing.T) {
	g := buildTestGraph([]string{"b1", "b2"}, "recorder")
	var mu sync.Mutex
	seen := map[string]string{}
	reg := pipeline.NewHandlerRegistry()
	reg.Register(&stubHandler{
		name: "recorder",
		execFunc: func(_ context.Context, node *pipeline.Node, pctx *pipeline.PipelineContext) (pipeline.Outcome, error) {
			rid, _ := pctx.GetInternal(pipeline.InternalKeyRunID)
			mu.Lock()
			seen[node.ID] = rid
			mu.Unlock()
			return pipeline.Outcome{Status: pipeline.OutcomeSuccess}, nil
		},
	})
	h := NewParallelHandler(g, reg, nil)
	pctx := pipeline.NewPipelineContext()
	pctx.SetInternal(pipeline.InternalKeyRunID, "R1")

	if _, err := h.Execute(context.Background(), g.Nodes["parallel_node"], pctx); err != nil {
		t.Fatalf("Execute: %v", err)
	}
	for _, id := range []string{"b1", "b2"} {
		if seen[id] != "R1" {
			t.Errorf("branch %q saw run_id %q, want %q (empty run_id => events unattributable)", id, seen[id], "R1")
		}
	}
}

// Each branch must see its own ctx.branch_id (#420) — the branch target node ID
// — so tool/agent nodes can namespace their on-disk per-loop counters by branch
// instead of racing over one shared path. Two branches must observe distinct,
// non-clobbering values.
func TestParallelExposesBranchIDToBranches(t *testing.T) {
	g := buildTestGraph([]string{"b1", "b2"}, "recorder")
	var mu sync.Mutex
	seen := map[string]string{}
	reg := pipeline.NewHandlerRegistry()
	reg.Register(&stubHandler{
		name: "recorder",
		execFunc: func(_ context.Context, node *pipeline.Node, pctx *pipeline.PipelineContext) (pipeline.Outcome, error) {
			bid, _ := pctx.Get(pipeline.ContextKeyBranchID)
			mu.Lock()
			seen[node.ID] = bid
			mu.Unlock()
			return pipeline.Outcome{Status: pipeline.OutcomeSuccess}, nil
		},
	})
	h := NewParallelHandler(g, reg, nil)

	if _, err := h.Execute(context.Background(), g.Nodes["parallel_node"], pipeline.NewPipelineContext()); err != nil {
		t.Fatalf("Execute: %v", err)
	}
	for _, id := range []string{"b1", "b2"} {
		if seen[id] != id {
			t.Errorf("branch %q saw branch_id %q, want %q (its own target node ID)", id, seen[id], id)
		}
	}
	if seen["b1"] == seen["b2"] {
		t.Errorf("branches shared branch_id %q — counters would clobber", seen["b1"])
	}
}

// A branch that hits billing/quota exhaustion (#487) returns a PauseError; the
// parallel node must propagate it as a resumable pause, not flatten it to a
// branch fail — and NOT mask it as node success under the default "any" policy
// when a sibling branch succeeds.
func TestParallelPropagatesBranchPause(t *testing.T) {
	g := buildTestGraph([]string{"ok", "billing"}, "payer")
	reg := pipeline.NewHandlerRegistry()
	reg.Register(&stubHandler{
		name: "payer",
		execFunc: func(_ context.Context, node *pipeline.Node, _ *pipeline.PipelineContext) (pipeline.Outcome, error) {
			if node.ID == "billing" {
				return pipeline.Outcome{}, pipeline.NewPauseError(pipeline.OutcomePausedBilling, errors.New("credit balance too low"))
			}
			return pipeline.Outcome{Status: pipeline.OutcomeSuccess}, nil
		},
	})
	h := NewParallelHandler(g, reg, nil)

	_, err := h.Execute(context.Background(), g.Nodes["parallel_node"], pipeline.NewPipelineContext())
	var pe *pipeline.PauseError
	if !errors.As(err, &pe) {
		t.Fatalf("expected a PauseError to propagate from the billing branch, got err=%v", err)
	}
	if pe.Status != pipeline.OutcomePausedBilling {
		t.Errorf("pause status = %q, want %q", pe.Status, pipeline.OutcomePausedBilling)
	}
}
