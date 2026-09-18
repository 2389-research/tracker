// ABOUTME: The failure cascade's fallback steps (#653) for a failed node whose
// ABOUTME: conditional edges all missed: node fallback_target, then graph on_failure, then halt.
package pipeline

import (
	"errors"
	"fmt"
	"time"
)

// EdgePriorityFallback marks a hop routed by the failure cascade's fallback
// steps (#653): a node whose outcome is `fail` and whose conditional edges all
// evaluated false is routed to its node-level `fallback_target` /
// `fallback_retry_target`, else the graph-level `defaults.on_failure`, before
// the no-matching-edges halt. Carried on both the decision_edge and
// conditional_fallthrough events for that hop. Additive; lives here rather
// than next to EdgePriorityElse in events.go only because of that file's size cap.
const EdgePriorityFallback = "fallback"

// selectEdgeFailed is advanceToNextNode's error tail for selectEdge: it gives
// the failure cascade (#653) a chance to route a fail outcome none of the
// guards matched, and otherwise surfaces the selection error unchanged.
func (e *Engine) selectEdgeFailed(s *runState, nodeID string, traceEntry *TraceEntry, err error) loopResult {
	if lr := e.unmatchedFailureCascade(s, nodeID, traceEntry, err); lr != nil {
		return *lr
	}
	s.trace.AddEntry(*traceEntry)
	return loopResult{action: loopReturn, err: fmt.Errorf("select edge from %q: %w", nodeID, err)}
}

// unmatchedFailureCascade runs dippin's failure cascade (docs/edges.md § Failure
// Handling) steps 3-5 for a node whose outcome is `fail` and whose outgoing
// edges are ALL conditional with none matching: node-level `fallback_target` /
// `fallback_retry_target` first, then the graph-level `defaults.on_failure`
// (`findFallbackTarget`), then a real terminal halt. Step 1 (an explicit
// `on fail` edge) already won in selectByCondition, step 2 (bounded retry) is
// the OutcomeRetry path, and the section-level `else` default is success-side
// only (#649) and never enters here.
//
// The hop reuses the strict-failure machinery (#295): WIP preservation before
// the routing decision (#302), the one-shot FallbackTaken latch (#642 — a
// latched node emits fallback_latched and halts naming the consumed
// fallback), the self-target guard (#650 — inside findFallbackTarget), and
// FallbackOrigin so the terminal names the real cause. The hop itself emits
// decision_edge + conditional_fallthrough with EdgePriorityFallback (the
// missed guards attached) and is recorded as an edge selection for resume
// (recordFallbackHop). Step 5 is the same terminal halt as checkStrictFailure
// (stage_failed, escalateWorkPreserve, recordHalt, OutcomeFail result); its
// error wraps selErr so the `no matching edges` diagnostic is preserved.
// Returns nil only when the cascade does not apply (not the typed error, or
// the outcome is not fail) — the caller then surfaces selErr unchanged.
func (e *Engine) unmatchedFailureCascade(s *runState, nodeID string, traceEntry *TraceEntry, selErr error) *loopResult {
	var nm *noMatchingEdgesError
	if !errors.As(selErr, &nm) {
		return nil
	}
	if outcome, _ := s.pctx.Get(ContextKeyOutcome); outcome != string(OutcomeFail) {
		return nil
	}
	node := e.graph.Nodes[nodeID]
	if node == nil {
		return nil
	}
	preserveErr := e.commitWIPBeforeRouting(s, nodeID, traceEntry)
	who := e.describeFailedNode(s, nodeID)
	lr, latched := e.strictFailureFallback(s, node, traceEntry, preserveErr, nm.conditionsTried)
	if lr != nil {
		return lr
	}
	haltMsg := fmt.Sprintf("node %s failed and no edge matched its fail outcome — stopping pipeline", who)
	haltErr := fmt.Errorf("node %s failed with no matching failure edge: %w", who, selErr)
	if latched != "" {
		e.emitFallbackLatched(s, nodeID, latched, node.Handler)
		haltMsg = fmt.Sprintf("node %s failed and no edge matched its fail outcome; its one-shot fallback %q was already taken — stopping pipeline", who, latched)
		haltErr = fmt.Errorf("node %s failed with no matching failure edge; its one-shot fallback %q was already taken: %w", who, latched, selErr)
	}
	return e.terminalFailureHalt(s, nodeID, traceEntry, preserveErr, haltMsg, haltErr)
}

// recordFallbackHop makes a fallback hop observable and replayable: it emits
// decision_edge with EdgePriorityFallback (plus conditional_fallthrough when
// the cascade tried guards first), and records the hop in EdgeSelections.
// Without the selection, a resume that re-walks the completed origin
// (resumeSkipNode) would re-run selectEdge and take the unconditional edge —
// silently skipping the fallback. Shared by the pure strict-failure path and
// the #653 cascade so neither double-emits.
func (e *Engine) recordFallbackHop(s *runState, nodeID, fb string, conditionsTried []ConditionEval) {
	ctxSnap := e.routingContextSnapshot(s.pctx)
	edge := &Edge{From: nodeID, To: fb, Attrs: map[string]string{"synthesized": EdgePriorityFallback}}
	e.emitEdgeSelected(s.runID, edge, EdgePriorityFallback, ctxSnap)
	e.emitFallthroughIfNeeded(s.runID, edge, EdgePriorityFallback, conditionsTried, ctxSnap)
	s.cp.SetEdgeSelection(nodeID, fb)
}

// terminalFailureHalt is the shared dead-stop tail for a failed node nothing
// routes (checkStrictFailure and the #653 cascade): it hard-escalates an
// unrecoverable WIP-preserve failure (#423), emits the reason-carrying
// stage_failed, closes the trace, persists the halt location for resume
// (#651) and returns the OutcomeFail result with err as the run error.
func (e *Engine) terminalFailureHalt(s *runState, nodeID string, traceEntry *TraceEntry, preserveErr error, haltMsg string, err error) *loopResult {
	workPreserveFailed := e.escalateWorkPreserve(s, nodeID, preserveErr)
	e.emit(PipelineEvent{
		Type:      EventStageFailed,
		Timestamp: time.Now(),
		RunID:     s.runID,
		NodeID:    nodeID,
		Message:   haltMsg,
		Err:       failureReasonErr(s),
	})
	s.trace.AddEntry(*traceEntry)
	e.recordHalt(s, nodeID) // #651: persist the dead-stop so resume can rewind past it
	s.trace.EndTime = time.Now()
	res := s.result(OutcomeFail)
	res.WorkPreserveFailed = workPreserveFailed
	return &loopResult{action: loopReturn, result: res, err: err}
}
