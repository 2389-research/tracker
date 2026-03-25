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
func (e *Engine) selectEdge(edges []*Edge, pctx *PipelineContext) (*Edge, error) {
	ctxSnap := e.routingContextSnapshot(pctx)

	if edge, err := e.selectByCondition(edges, pctx, ctxSnap); edge != nil || err != nil {
		return edge, err
	}

	if edge := e.selectByLabel(edges, pctx, ctxSnap); edge != nil {
		return edge, nil
	}

	if edge := e.selectBySuggested(edges, pctx, ctxSnap); edge != nil {
		return edge, nil
	}

	return e.selectByWeight(edges, pctx, ctxSnap)
}

// selectByCondition evaluates condition expressions on edges, returning the first match.
func (e *Engine) selectByCondition(edges []*Edge, pctx *PipelineContext, ctxSnap map[string]string) (*Edge, error) {
	for _, edge := range edges {
		if edge.Condition == "" {
			continue
		}
		match, err := EvaluateCondition(edge.Condition, pctx)
		if err != nil {
			return nil, fmt.Errorf("evaluate condition on edge %s->%s: %w", edge.From, edge.To, err)
		}
		e.emit(PipelineEvent{
			Type:      EventDecisionCondition,
			Timestamp: time.Now(),
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
		if match {
			e.emitEdgeSelected(edge, "condition", ctxSnap)
			return edge, nil
		}
	}
	return nil, nil
}

// selectByLabel matches edges by the preferred label stored in context.
func (e *Engine) selectByLabel(edges []*Edge, pctx *PipelineContext, ctxSnap map[string]string) *Edge {
	preferred, ok := pctx.Get(ContextKeyPreferredLabel)
	if !ok || preferred == "" {
		return nil
	}
	for _, edge := range edges {
		if edge.Label == preferred {
			e.emitEdgeSelected(edge, "label", ctxSnap)
			return edge
		}
	}
	return nil
}

// selectBySuggested matches edges by handler-suggested next node IDs.
func (e *Engine) selectBySuggested(edges []*Edge, pctx *PipelineContext, ctxSnap map[string]string) *Edge {
	suggested, ok := pctx.Get("suggested_next_nodes")
	if !ok || suggested == "" {
		return nil
	}
	for _, edge := range edges {
		for _, sid := range strings.Split(suggested, ",") {
			if strings.TrimSpace(sid) == edge.To {
				e.emitEdgeSelected(edge, "suggested", ctxSnap)
				return edge
			}
		}
	}
	return nil
}

// selectByWeight picks the highest-weight unconditional edge, breaking ties lexically.
func (e *Engine) selectByWeight(edges []*Edge, pctx *PipelineContext, ctxSnap map[string]string) (*Edge, error) {
	var unconditional []*Edge
	for _, edge := range edges {
		if edge.Condition == "" {
			unconditional = append(unconditional, edge)
		}
	}
	if len(unconditional) == 0 {
		return nil, e.noMatchingEdgesError(edges, pctx)
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
			NodeID:    unconditional[0].From,
			Message:   fmt.Sprintf("lexical tiebreaker used: %d unconditional edges from %q with equal weight; selected %q", len(unconditional), unconditional[0].From, unconditional[0].To),
		})
	}

	e.emitEdgeSelected(unconditional[0], priority, ctxSnap)
	return unconditional[0], nil
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
func (e *Engine) emitEdgeSelected(edge *Edge, priority string, ctxSnap map[string]string) {
	e.emit(PipelineEvent{
		Type:      EventDecisionEdge,
		Timestamp: time.Now(),
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
	for _, key := range []string{ContextKeyOutcome, ContextKeyPreferredLabel, ContextKeyToolStdout, ContextKeyHumanResponse, "suggested_next_nodes"} {
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
