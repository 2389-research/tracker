// ABOUTME: Combines the pauses of a parallel node's branches into the one pause
// ABOUTME: the node returns: every waiting gate recorded, billing outranking gates.
package handlers

import (
	"github.com/2389-research/tracker/pipeline"
)

// mergeBranchPauses folds per-branch pauses (indexed by branch, nil for a
// branch that did not pause) into the parallel node's pause, or nil. A billing
// pause outranks a gate pause — answering a gate cannot fix an empty balance —
// and is the first billing branch's, without gate records (the resume re-runs
// the node, whose gates ask again). Otherwise the waiting gates of every branch
// are recorded in outer-branch order: pauses already arrive in branch order, and
// a nested parallel branch returns gates carrying their inner branch index, so a
// global sort by BranchIndex would interleave gates across outer branches.
func mergeBranchPauses(pauses []*pipeline.PauseError) *pipeline.PauseError {
	if billing := firstPause(pauses, func(pe *pipeline.PauseError) bool { return pe.Status != pipeline.OutcomePausedGate }); billing != nil {
		return billing
	}
	gate := firstPause(pauses, func(pe *pipeline.PauseError) bool { return true })
	if gate == nil {
		return nil
	}
	var gates []pipeline.PausedGate
	for _, pe := range pauses {
		if pe != nil {
			gates = append(gates, pe.Gates...)
		}
	}
	merged := *gate
	merged.Gates = gates
	return &merged
}

// firstPause returns the first non-nil pause that match accepts.
func firstPause(pauses []*pipeline.PauseError, match func(*pipeline.PauseError) bool) *pipeline.PauseError {
	for _, pe := range pauses {
		if pe != nil && match(pe) {
			return pe
		}
	}
	return nil
}
