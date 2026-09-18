// ABOUTME: The failure cascade's fallback steps (#653) for a failed node whose
// ABOUTME: conditional edges all missed: node fallback_target, then graph on_failure, then halt.
package pipeline

import (
	"errors"
	"fmt"
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
// Handling) steps 3-4 for a node whose outcome is `fail` and whose outgoing
// edges are ALL conditional with none matching: node-level `fallback_target` /
// `fallback_retry_target` first, then the graph-level `defaults.on_failure`
// (`findFallbackTarget`), before the no-matching-edges halt (step 5). Step 1
// (an explicit `on fail` edge) already won in selectByCondition, step 2
// (bounded retry) is the OutcomeRetry path, and the section-level `else`
// default is success-side only (#649) and never enters here.
//
// The hop reuses the strict-failure fallback machinery (#295): the one-shot
// FallbackTaken latch (#642 — a latched node emits fallback_latched and falls
// to the halt), the self-target guard (#650 — inside findFallbackTarget), WIP
// preservation before routing (#302), and FallbackOrigin so the terminal names
// the real cause. It additionally emits decision_edge + conditional_fallthrough
// with EdgePriorityFallback (the missed guards attached) and records the hop as
// an edge selection so a resume replays it. Returns nil when the cascade does
// not apply or resolves nothing — the caller then surfaces selErr unchanged.
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
	fb := e.findFallbackTarget(node)
	if fb == "" {
		return nil
	}
	if s.cp.IsFallbackTaken(nodeID) {
		e.emitFallbackLatched(s, nodeID, fb, node.Handler)
		return nil
	}
	ctxSnap := e.routingContextSnapshot(s.pctx)
	edge := &Edge{From: nodeID, To: fb, Attrs: map[string]string{"synthesized": EdgePriorityFallback}}
	e.emitEdgeSelected(s.runID, edge, EdgePriorityFallback, ctxSnap)
	e.emitFallthroughIfNeeded(s.runID, edge, EdgePriorityFallback, nm.conditionsTried, ctxSnap)
	// Recorded before the checkpoint save inside strictFailureFallback so a
	// resume replays Build -> fallback instead of re-evaluating the guards.
	s.cp.SetEdgeSelection(nodeID, fb)
	preserveErr := e.commitWIPBeforeRouting(s, nodeID, traceEntry)
	lr, _ := e.strictFailureFallback(s, node, traceEntry, preserveErr)
	return lr
}
