// ABOUTME: Combines the pauses of a parallel node's branches into the one pause
// ABOUTME: the node returns: every waiting gate recorded, billing outranking gates.
package handlers

import (
	"sort"

	"github.com/2389-research/tracker/pipeline"
)

// mergeBranchPauses folds per-branch pauses (indexed by branch, nil for a
// branch that did not pause) into the parallel node's pause, or nil. A billing
// pause outranks a gate pause — answering a gate cannot fix an empty balance —
// and is the first billing branch's, without gate records (the resume re-runs
// the node, whose gates ask again). Otherwise the waiting gates of every
// branch are recorded, ordered by branch index.
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
	sort.SliceStable(gates, func(i, j int) bool { return gates[i].BranchIndex < gates[j].BranchIndex })
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
