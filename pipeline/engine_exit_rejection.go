// ABOUTME: #633 terminal handling for the exit node's success path —
// ABOUTME: budget breach and operator rejections at human gates (a rejected
// ABOUTME: run terminates fail, not the exit node's passthrough success),
// ABOUTME: plus the goal-gate retry redirect extracted from handleExitNode.
package pipeline

import (
	"fmt"
	"strings"
	"time"
)

// isRejectionLabel reports whether a human-gate edge label denotes an
// operator rejection of the run (#633). Deliberately a small exact-match
// denylist (case-insensitive) rather than inference from edge shape: every
// bundled workflow labels its human-gate → exit decline edges "abandon" or
// "reject", and no affirmative edge in any bundled workflow carries either
// word — while legitimate workflows DO route affirmative ("accept") and
// unlabeled (freeform/interview continue) gate edges into the exit node as
// normal success, so shape-based inference is unsound.
func isRejectionLabel(s string) bool {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "abandon", "reject":
		return true
	}
	return false
}

// exitRejectionGate returns the wait.human gate node whose recorded edge
// selection routes the run into the exit node via a rejection-labeled
// non-override edge, or "" when the exit was not reached that way (#633).
//
// Derived from the durable checkpoint edge selections (not in-memory run
// state) so a crash/resume immediately before exit processing still reports
// the rejection.
func (e *Engine) exitRejectionGate(cp *Checkpoint) string {
	for from, to := range cp.EdgeSelections {
		if to != e.graph.ExitNode {
			continue
		}
		if e.exitSelectionIsRejection(cp, from) {
			return from
		}
	}
	return ""
}

// exitSelectionIsRejection reports whether node's recorded selection of the
// exit node was a rejection: node is a wait.human gate, the run did NOT
// traverse an override edge from it into the exit (an accept), and it has a
// rejection-labeled non-override edge into the exit.
func (e *Engine) exitSelectionIsRejection(cp *Checkpoint, from string) bool {
	fromNode, ok := e.graph.Nodes[from]
	if !ok || fromNode.Handler != "wait.human" {
		return false
	}
	if e.overrideIntoExitTraversed(cp, from) {
		return false
	}
	return e.hasRejectionEdgeToExit(from)
}

// hasRejectionEdgeToExit reports whether gate has a non-override edge into
// the exit node whose label/choice is a rejection word.
func (e *Engine) hasRejectionEdgeToExit(gate string) bool {
	for _, edge := range e.graph.OutgoingEdges(gate) {
		if edge.To != e.graph.ExitNode || edge.Override {
			continue
		}
		if isRejectionLabel(edge.Label) || isRejectionLabel(edge.Choice) {
			return true
		}
	}
	return false
}

// overrideIntoExitTraversed reports whether the run recorded a traversal of
// an override edge from gate into the exit node (an operator accept that ends
// the run directly). Matched on (gate, label) against the edge set so an
// override recorded on a DIFFERENT override edge of the same gate (e.g. an
// earlier accept that routed to further work) does not mask a later
// rejection-labeled exit selection.
func (e *Engine) overrideIntoExitTraversed(cp *Checkpoint, gate string) bool {
	for _, d := range cp.ValidationOverrides {
		if d.GateNodeID == gate && e.overrideDetailHitsExitEdge(gate, d) {
			return true
		}
	}
	return false
}

// overrideDetailHitsExitEdge reports whether recorded override detail d
// matches an override edge from gate into the exit node by label.
func (e *Engine) overrideDetailHitsExitEdge(gate string, d OverrideDetail) bool {
	for _, edge := range e.graph.OutgoingEdges(gate) {
		if edge.To == e.graph.ExitNode && edge.Override && (d.Label == edge.Label || d.Label == edge.Choice) {
			return true
		}
	}
	return false
}

// handleGoalGateRetry processes the goal-gate retry redirect extracted from
// handleExitNode to keep that function under the complexity gate. It emits the
// retry/recheck event, records the trace, and redirects the run to target.
// Returns (false, target, nil) so the caller re-enters at target.
func (e *Engine) handleGoalGateRetry(s *runState, currentNodeID, target, gateNodeID string, traceEntry *TraceEntry) (bool, string, *EngineResult) {
	// A pending re-entry (target == the gate itself, flagged by a prior
	// redirect) completes that redirect's retry cycle — the budget was
	// charged when the redirect fired, so it is not charged again here.
	// It cannot loop: the gate executes next, clearing the pending flag.
	reentry := s.cp.IsGateRecheckPending(gateNodeID) && target == gateNodeID
	gateNode := e.nodeOrDefault(gateNodeID)
	msg := fmt.Sprintf("goal-gate recheck: re-entering %q so the gate re-judges the current tree (attempt %d/%d)",
		gateNodeID, s.cp.RetryCount(gateNodeID), e.maxRetries(gateNode))
	if !reentry {
		s.cp.IncrementRetry(gateNodeID)
		msg = fmt.Sprintf("goal-gate retry for %q → %q (attempt %d/%d)",
			gateNodeID, target,
			s.cp.RetryCount(gateNodeID), e.maxRetries(gateNode))
	}
	e.emit(PipelineEvent{
		Type:      EventStageRetrying,
		Timestamp: time.Now(),
		RunID:     s.runID,
		NodeID:    gateNodeID, NodeKind: gateNode.Handler, AttemptNo: s.cp.RetryCount(gateNodeID),
		Message: msg,
	})
	traceEntry.EdgeTo = target
	s.trace.AddEntry(*traceEntry)
	e.emitGitCommit(s, currentNodeID, traceEntry)
	// #348 defect 1: the redirect's clearDownstream below may remove the
	// gate from CompletedNodes while the executed path routes around it
	// to the exit. Mark the gate recheck-pending so it stays visible to
	// this check and the next retry re-enters at the gate itself; the
	// flag clears when the gate actually re-executes (applyOutcome).
	s.cp.SetGateRecheckPending(gateNodeID)
	e.clearDownstream(target, s.cp)
	s.cp.CurrentNode = target
	e.saveCheckpointWithTag(s.cp, s.pctx, s.runID, s, currentNodeID)
	return false, target, nil
}

// exitSuccessHalt runs the terminal checks on the exit node's success path:
// a budget breach first (preserving the historical budget_exceeded terminal
// for cost-bound runs), then an operator rejection at a human gate (#633).
// Returns nil when the run may complete successfully.
func (e *Engine) exitSuccessHalt(s *runState, exitNodeID string, traceEntry *TraceEntry) *EngineResult {
	if halt := e.checkBudgetHaltForExit(s); halt != nil {
		return halt
	}
	if gate := e.exitRejectionGate(s.cp); gate != "" {
		return e.exitRejectionResult(s, exitNodeID, traceEntry, gate)
	}
	return nil
}

// exitRejectionResult builds the terminal fail EngineResult for a run the
// operator rejected at a human gate (#633). Mirrors the other exit-node fail
// paths: record the exit trace entry, preserve any dirty tree, stamp the
// trace end, emit the terminal failed event, and return a fail result.
func (e *Engine) exitRejectionResult(s *runState, exitNodeID string, traceEntry *TraceEntry, gate string) *EngineResult {
	// Never-lose-work: preserve any dirty tree before the rejection terminal
	// (same guarantee as the outcome-fail exit path).
	preserveErr := e.commitWIPBeforeRouting(s, exitNodeID, traceEntry)
	s.trace.AddEntry(*traceEntry)
	e.emitGitCommit(s, exitNodeID, traceEntry)
	s.trace.EndTime = time.Now()
	e.emitFailed(s, fmt.Sprintf("run rejected at human gate %q: exit reached via rejection edge (abandon/reject)", gate), nil)
	result := s.result(OutcomeFail)
	result.WorkPreserveFailed = e.escalateWorkPreserve(s, exitNodeID, preserveErr)
	return result
}
