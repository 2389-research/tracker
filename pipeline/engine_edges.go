// ABOUTME: Edge selection logic extracted from engine.go to reduce function complexity.
// ABOUTME: Implements priority-based edge routing: condition > label > suggested > weight > lexical.
package pipeline

import (
	"fmt"
	"sort"
	"strconv"
	"strings"
	"time"
)

// selectEdge picks the best outgoing edge using priority: condition > preferred label > suggested IDs > weight > lexical.
// runID is stamped on every emitted decision/fallthrough event so activity.jsonl consumers can group every line by run.
func (e *Engine) selectEdge(runID string, edges []*Edge, pctx *PipelineContext) (*Edge, error) {
	ctxSnap := e.routingContextSnapshot(pctx)

	edge, conditionsTried, err := e.selectByCondition(runID, edges, pctx, ctxSnap)
	if edge != nil || err != nil {
		return edge, err
	}

	if edge := e.selectByLabel(runID, edges, pctx, ctxSnap); edge != nil {
		e.emitFallthroughIfNeeded(runID, edge, "label", conditionsTried, ctxSnap)
		return edge, nil
	}

	if edge := e.selectBySuggested(runID, edges, pctx, ctxSnap); edge != nil {
		e.emitFallthroughIfNeeded(runID, edge, "suggested", conditionsTried, ctxSnap)
		return edge, nil
	}

	edge, weightPriority, err := e.selectByWeight(runID, edges, pctx, ctxSnap)
	if err == nil && edge != nil {
		e.emitFallthroughIfNeeded(runID, edge, weightPriority, conditionsTried, ctxSnap)
	}
	return edge, err
}

// emitFallthroughIfNeeded fires EventConditionalFallthrough when conditionals
// were tried but an unconditional fallback edge was selected instead. Skip the
// event when the selected edge itself has a Condition (re-selection by label
// or weight on a conditional edge is not a fallthrough).
func (e *Engine) emitFallthroughIfNeeded(runID string, selected *Edge, priority string, conditionsTried []ConditionEval, ctxSnap map[string]string) {
	if len(conditionsTried) == 0 || selected == nil || selected.Condition != "" {
		return
	}
	e.emit(PipelineEvent{
		Type:      EventConditionalFallthrough,
		Timestamp: time.Now(),
		RunID:     runID,
		NodeID:    selected.From,
		Message:   fmt.Sprintf("conditional fallthrough on node %q: %d condition(s) evaluated false, fell back to %s edge -> %q", selected.From, len(conditionsTried), priority, selected.To),
		Decision: &DecisionDetail{
			EdgeFrom:        selected.From,
			EdgeTo:          selected.To,
			EdgePriority:    priority,
			ContextSnapshot: ctxSnap,
			ConditionsTried: conditionsTried,
		},
	})
}

// selectByCondition evaluates condition expressions on edges, returning the
// first match. Also returns the list of conditionals that evaluated false
// (in declaration order) so the caller can emit a fallthrough signal if
// routing ends up picking an unconditional fallback.
func (e *Engine) selectByCondition(runID string, edges []*Edge, pctx *PipelineContext, ctxSnap map[string]string) (*Edge, []ConditionEval, error) {
	params := ExtractParamsFromGraphAttrs(e.graph.Attrs)
	var triedFalse []ConditionEval
	for _, edge := range edges {
		if edge.Condition == "" {
			continue
		}
		match, err := e.evaluateEdgeCondition(runID, edge, pctx, params, ctxSnap)
		if err != nil {
			return nil, nil, err
		}
		if match {
			e.emitEdgeSelected(runID, edge, "condition", ctxSnap)
			return edge, nil, nil
		}
		triedFalse = append(triedFalse, ConditionEval{EdgeTo: edge.To, Condition: edge.Condition})
	}
	return nil, triedFalse, nil
}

// evaluateEdgeCondition expands and evaluates a single conditional edge,
// emits the decision event, and returns whether the condition matched.
func (e *Engine) evaluateEdgeCondition(runID string, edge *Edge, pctx *PipelineContext, params map[string]string, ctxSnap map[string]string) (bool, error) {
	expandedCondition, err := ExpandVariables(edge.Condition, pctx, params, e.graph.Attrs, false)
	if err != nil {
		return false, fmt.Errorf("expand condition on edge %s->%s: %w", edge.From, edge.To, err)
	}
	match, err := EvaluateCondition(expandedCondition, pctx)
	if err != nil {
		return false, fmt.Errorf("evaluate condition on edge %s->%s: %w", edge.From, edge.To, err)
	}
	e.emit(PipelineEvent{
		Type:      EventDecisionCondition,
		Timestamp: time.Now(),
		RunID:     runID,
		NodeID:    edge.From,
		Message:   fmt.Sprintf("condition %q on edge %s->%s evaluated to %v", edge.Condition, edge.From, edge.To, match),
		Decision: &DecisionDetail{
			EdgeFrom:        edge.From,
			EdgeTo:          edge.To,
			EdgeCondition:   edge.Condition,
			ConditionMatch:  match,
			ContextSnapshot: ctxSnap,
		},
	})
	return match, nil
}

// edgeRoutingKey returns the routing key for an edge: Choice when non-empty,
// Label when non-empty, otherwise To. Used so edges with an explicit choice:
// attribute route on the choice key, not the display label (DIP150).
func edgeRoutingKey(edge *Edge) string {
	if edge.Choice != "" {
		return edge.Choice
	}
	if edge.Label != "" {
		return edge.Label
	}
	return edge.To
}

// selectByLabel matches edges by the preferred label stored in context.
// When an edge has a Choice key, the preferred label is compared against
// Choice (the stable routing key) rather than Label (the display string).
func (e *Engine) selectByLabel(runID string, edges []*Edge, pctx *PipelineContext, ctxSnap map[string]string) *Edge {
	preferred, ok := pctx.Get(ContextKeyPreferredLabel)
	if !ok || preferred == "" {
		return nil
	}
	for _, edge := range edges {
		if edgeRoutingKey(edge) == preferred {
			e.emitEdgeSelected(runID, edge, "label", ctxSnap)
			return edge
		}
	}
	return nil
}

// selectBySuggested matches edges by handler-suggested next node IDs.
func (e *Engine) selectBySuggested(runID string, edges []*Edge, pctx *PipelineContext, ctxSnap map[string]string) *Edge {
	suggested, ok := pctx.Get(ContextKeySuggestedNextNodes)
	if !ok || suggested == "" {
		return nil
	}
	for _, edge := range edges {
		for _, sid := range strings.Split(suggested, ",") {
			if strings.TrimSpace(sid) == edge.To {
				e.emitEdgeSelected(runID, edge, "suggested", ctxSnap)
				return edge
			}
		}
	}
	return nil
}

// selectByWeight picks the highest-weight unconditional edge, breaking ties
// lexically. Returns the selected edge plus the priority label used
// ("weight" or "lexical"); the caller needs the priority to keep the
// fallthrough event consistent with the edge-selected event.
func (e *Engine) selectByWeight(runID string, edges []*Edge, pctx *PipelineContext, ctxSnap map[string]string) (*Edge, string, error) {
	var unconditional []*Edge
	for _, edge := range edges {
		if edge.Condition == "" {
			unconditional = append(unconditional, edge)
		}
	}
	if len(unconditional) == 0 {
		return nil, "", e.noMatchingEdgesError(edges, pctx)
	}

	sort.SliceStable(unconditional, func(i, j int) bool {
		wi := edgeWeight(unconditional[i])
		wj := edgeWeight(unconditional[j])
		if wi != wj {
			return wi > wj
		}
		return unconditional[i].To < unconditional[j].To
	})

	priority := "weight"
	if len(unconditional) > 1 && edgeWeight(unconditional[0]) == edgeWeight(unconditional[1]) {
		priority = "lexical"
		e.emit(PipelineEvent{
			Type:      EventEdgeTiebreaker,
			Timestamp: time.Now(),
			RunID:     runID,
			NodeID:    unconditional[0].From,
			Message:   fmt.Sprintf("lexical tiebreaker used: %d unconditional edges from %q with equal weight; selected %q", len(unconditional), unconditional[0].From, unconditional[0].To),
		})
	}

	e.emitEdgeSelected(runID, unconditional[0], priority, ctxSnap)
	return unconditional[0], priority, nil
}

// noMatchingEdgesError builds a diagnostic error when all edges have false conditions.
func (e *Engine) noMatchingEdgesError(edges []*Edge, pctx *PipelineContext) error {
	var diag []string
	for _, edge := range edges {
		if edge.Condition != "" {
			outcomeVal, _ := pctx.Get(ContextKeyOutcome)
			diag = append(diag, fmt.Sprintf("  %s->%s condition=%q (outcome=%q)", edge.From, edge.To, edge.Condition, outcomeVal))
		}
	}
	return fmt.Errorf("no matching edges: all %d edges have conditions that evaluated to false:\n%s", len(edges), strings.Join(diag, "\n"))
}

// emitEdgeSelected emits a decision_edge event recording which edge was selected and why.
// Override-marked edges always emit EdgePriorityOverride regardless of which selection
// path (label, condition, weight, etc.) actually picked them — external NDJSON consumers
// keying off edge_priority can identify the traversal as an override without watching
// the dedicated EventValidationOverridden event that rides alongside.
func (e *Engine) emitEdgeSelected(runID string, edge *Edge, priority string, ctxSnap map[string]string) {
	if edge.Override {
		priority = EdgePriorityOverride
	}
	e.emit(PipelineEvent{
		Type:      EventDecisionEdge,
		Timestamp: time.Now(),
		RunID:     runID,
		NodeID:    edge.From,
		Message:   fmt.Sprintf("edge selected %s->%s via %s", edge.From, edge.To, priority),
		Decision: &DecisionDetail{
			EdgeFrom:        edge.From,
			EdgeTo:          edge.To,
			EdgeCondition:   edge.Condition,
			EdgePriority:    priority,
			ContextSnapshot: ctxSnap,
		},
	})
}

// routingContextSnapshot returns a map of the key context values relevant to edge routing.
func (e *Engine) routingContextSnapshot(pctx *PipelineContext) map[string]string {
	snap := make(map[string]string)
	for _, key := range []string{ContextKeyOutcome, ContextKeyPreferredLabel, ContextKeyToolStdout, ContextKeyHumanResponse, ContextKeySuggestedNextNodes} {
		if val, ok := pctx.Get(key); ok && val != "" {
			snap[key] = val
		}
	}
	return snap
}

// edgeWeight parses the "weight" attribute as an integer, defaulting to 0.
func edgeWeight(e *Edge) int {
	if w, ok := e.Attrs["weight"]; ok {
		if n, err := strconv.Atoi(w); err == nil {
			return n
		}
	}
	return 0
}
