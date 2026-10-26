// ABOUTME: Tests for mergeBranchPauses — billing outranks gates, and waiting
// ABOUTME: gates stay in outer-branch order (no cross-branch reordering).
package handlers

import (
	"reflect"
	"testing"

	"github.com/2389-research/tracker/pipeline"
)

// TestMergeBranchPauses_KeepsOuterBranchOrder pins that gates stay grouped by
// outer branch. Pauses arrive in outer-branch order; a nested parallel branch
// returns gates carrying their *inner* branch index, so a global sort by
// BranchIndex would interleave them across outer branches. Here outer branch 0
// is a nested parallel with inner gates at indices 1 and 2, and outer branch 1
// has a gate at index 1 — the result must be [A1, A2, B1], not [A1, B1, A2].
func TestMergeBranchPauses_KeepsOuterBranchOrder(t *testing.T) {
	pauses := []*pipeline.PauseError{
		{Status: pipeline.OutcomePausedGate, Gates: []pipeline.PausedGate{
			{NodeID: "A1", BranchIndex: 1},
			{NodeID: "A2", BranchIndex: 2},
		}},
		{Status: pipeline.OutcomePausedGate, Gates: []pipeline.PausedGate{
			{NodeID: "B1", BranchIndex: 1},
		}},
	}

	merged := mergeBranchPauses(pauses)

	var order []string
	for _, g := range merged.Gates {
		order = append(order, g.NodeID)
	}
	want := []string{"A1", "A2", "B1"}
	if !reflect.DeepEqual(order, want) {
		t.Errorf("gate order = %v, want %v (outer-branch order)", order, want)
	}
}

// TestMergeBranchPauses_BillingOutranksGate pins that a billing pause in any
// branch wins over gate pauses and carries no gate records (resume re-runs the
// node, whose gates ask again).
func TestMergeBranchPauses_BillingOutranksGate(t *testing.T) {
	pauses := []*pipeline.PauseError{
		{Status: pipeline.OutcomePausedGate, Gates: []pipeline.PausedGate{{NodeID: "G"}}},
		{Status: pipeline.OutcomePausedBilling},
	}

	merged := mergeBranchPauses(pauses)

	if merged.Status != pipeline.OutcomePausedBilling {
		t.Errorf("status = %q, want %q (billing outranks a gate)", merged.Status, pipeline.OutcomePausedBilling)
	}
	if len(merged.Gates) != 0 {
		t.Errorf("billing pause carried %d gate(s), want 0", len(merged.Gates))
	}
}
