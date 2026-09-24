// ABOUTME: Loop-iteration scoping for per-target restart budgets (#643).
// ABOUTME: Derives each restart header's natural loop from the graph and resets nested budgets when an enclosing loop restarts.
package pipeline

import (
	"fmt"
	"maps"
	"sort"
	"time"
)

// restartScopes records, for every loop header in the graph, the set of nodes
// inside that header's natural loop. It is derived purely from graph shape
// (no workflow knowledge) once per engine and consulted on every loop restart.
//
// Definitions (standard compiler loop analysis, computed from Graph.StartNode):
//
//   - h dominates n when every path from the start node to n passes through h.
//   - An edge u -> h is a BACK edge when h dominates u.
//   - The natural loop of header h is h plus every node that can reach the
//     source of a back edge into h without passing through h.
//
// A header's loop therefore contains every header nested inside it (an inner
// fix loop's `Test` header sits inside the milestone loop's `Pick` header),
// while an irreducible re-entry (an edge into a node that does NOT dominate
// its source) is not a back edge and never widens a loop — so a graph the
// analysis cannot structure falls back to the pre-#643 run-wide budget
// rather than to a budget that could reset without bound.
//
// Loop containment is a strict partial order (two distinct headers cannot each
// lie inside the other's natural loop — that would force mutual dominance),
// so the resets it drives cannot cycle: every restart count is bounded by
// max_restarts per iteration of its enclosing loop, and the outermost loop's
// count, having no enclosing header, is bounded run-wide.
type restartScopes struct {
	// inner[h] is the set of nodes in h's natural loop, EXCLUDING h itself.
	// Absent key = h heads no loop.
	inner map[string]map[string]bool
	// backEdges[u][h] is true when u -> h is a back edge (h dominates u).
	backEdges map[string]map[string]bool
}

// computeRestartScopes derives the natural loop of every header in g.
// Nodes unreachable from the start node take part in no loop.
func computeRestartScopes(g *Graph) *restartScopes {
	rs := &restartScopes{inner: map[string]map[string]bool{}, backEdges: map[string]map[string]bool{}}
	if g == nil || g.StartNode == "" {
		return rs
	}
	if _, ok := g.Nodes[g.StartNode]; !ok {
		return rs
	}
	order := reachableInBFSOrder(g)
	rs.collectBackEdges(g, order, dominators(g, order))
	return rs
}

// collectBackEdges records every reachable edge u -> h where h dominates u.
// Successors include the section-level else route (#649), so an else hop into
// a loop header is classified as a back edge like an explicit one.
func (rs *restartScopes) collectBackEdges(g *Graph, order []string, dom map[string]map[string]bool) {
	for _, from := range order {
		for _, to := range successorIDs(g, from) {
			if dom[from][to] {
				rs.addBackEdge(g, from, to)
			}
		}
	}
}

// addBackEdge records u -> h as a back edge and folds the nodes that reach u
// without passing through h into h's loop body.
func (rs *restartScopes) addBackEdge(g *Graph, u, h string) {
	if rs.backEdges[u] == nil {
		rs.backEdges[u] = map[string]bool{}
	}
	rs.backEdges[u][h] = true
	if rs.inner[h] == nil {
		rs.inner[h] = map[string]bool{}
	}
	// A self-loop (u == h) contributes no body: the walk must never expand
	// the header's own predecessors.
	if u == h {
		return
	}
	for n := range reverseReachAvoiding(g, u, h) {
		rs.inner[h][n] = true
	}
}

// reverseReachAvoiding returns every node (from included) that can reach
// `from` along incoming edges without passing through `avoid`.
func reverseReachAvoiding(g *Graph, from, avoid string) map[string]bool {
	seen := map[string]bool{from: true}
	stack := []string{from}
	for len(stack) > 0 {
		n := stack[len(stack)-1]
		stack = stack[:len(stack)-1]
		for _, in := range g.IncomingEdges(n) {
			if in.From != avoid && !seen[in.From] {
				seen[in.From] = true
				stack = append(stack, in.From)
			}
		}
	}
	return seen
}

// reachableInBFSOrder returns the nodes reachable from g.StartNode in BFS
// order (start first). Only reachable nodes take part in dominance.
func reachableInBFSOrder(g *Graph) []string {
	visited := map[string]bool{g.StartNode: true}
	queue := []string{g.StartNode}
	var order []string
	for len(queue) > 0 {
		n := queue[0]
		queue = queue[1:]
		order = append(order, n)
		// Else-aware (#649): an else-only target is reachable, and a back edge
		// into it must be classified against the same graph the engine walks.
		for _, to := range successorIDs(g, n) {
			if !visited[to] {
				visited[to] = true
				queue = append(queue, to)
			}
		}
	}
	return order
}

// dominators computes dom[n] = the set of nodes that dominate n (n included)
// for every reachable node, by iterating the classic dataflow equation
//
//	dom(n) = {n} ∪ ⋂ dom(p) for reachable predecessors p
//
// to a fixpoint. Pipeline graphs are tens of nodes, so the simple
// set-of-maps formulation is plenty.
func dominators(g *Graph, order []string) map[string]map[string]bool {
	dom := initialDominators(g.StartNode, order)
	for dominatorsPass(g, order, dom) {
	}
	return dom
}

// dominatorsPass runs one sweep of the dominator equation over order and
// reports whether any set changed (the fixpoint is reached when none does).
func dominatorsPass(g *Graph, order []string, dom map[string]map[string]bool) bool {
	changed := false
	for _, n := range order {
		if n == g.StartNode {
			continue
		}
		next := predecessorDominators(g, n, dom)
		next[n] = true
		if !maps.Equal(next, dom[n]) {
			dom[n] = next
			changed = true
		}
	}
	return changed
}

// initialDominators seeds the fixpoint: the start node dominates only itself;
// every other reachable node starts as "dominated by everything".
func initialDominators(start string, order []string) map[string]map[string]bool {
	dom := make(map[string]map[string]bool, len(order))
	for _, n := range order {
		if n == start {
			dom[n] = map[string]bool{n: true}
			continue
		}
		all := make(map[string]bool, len(order))
		for _, m := range order {
			all[m] = true
		}
		dom[n] = all
	}
	return dom
}

// predecessorDominators returns the intersection of dom[p] over n's reachable
// predecessors (an empty set when n has none). dom only holds reachable nodes,
// so an unreachable predecessor has no entry and is skipped. Predecessors
// include nodes whose section-level else route lands on n (#649), so an
// else-only target is dominated by the nodes that actually precede it.
func predecessorDominators(g *Graph, n string, dom map[string]map[string]bool) map[string]bool {
	var next map[string]bool
	for _, from := range predecessorIDs(g, n) {
		pd, reachable := dom[from]
		if !reachable {
			continue
		}
		if next == nil {
			next = maps.Clone(pd)
		} else {
			intersectInto(next, pd)
		}
	}
	if next == nil {
		next = map[string]bool{}
	}
	return next
}

// intersectInto removes from dst every key absent from keep.
func intersectInto(dst, keep map[string]bool) {
	for k := range dst {
		if !keep[k] {
			delete(dst, k)
		}
	}
}

// innerNodes returns the nodes inside header's natural loop (excluding the
// header), sorted for deterministic events and tests. Empty when header heads
// no loop.
func (rs *restartScopes) innerNodes(header string) []string {
	body := rs.inner[header]
	if len(body) == 0 {
		return nil
	}
	out := make([]string, 0, len(body))
	for n := range body {
		out = append(out, n)
	}
	sort.Strings(out)
	return out
}

// isBackEdge reports whether traversing from -> to closes a natural loop
// (to dominates from). The engine treats every back-edge traversal as a loop
// restart of `to` — counted against its budget and clearing its downstream —
// even when an inner restart's clearDownstream already wiped `to`'s completed
// flag. Without this, the outer header of a nested loop was only counted on
// iterations that happened to have no inner restart, so its count was neither
// a reliable iteration counter (the #643 reset signal) nor a reliable bound.
func (rs *restartScopes) isBackEdge(from, to string) bool {
	return rs.backEdges[from][to]
}

// loopScopes returns the engine's restart scopes, deriving them from the
// graph on first use. The engine's run loop is single-threaded, so the lazy
// init needs no lock.
func (e *Engine) loopScopes() *restartScopes {
	if e.restartScopes == nil {
		e.restartScopes = computeRestartScopes(e.graph)
	}
	return e.restartScopes
}

// resetEnclosedRestartBudgets is called right after a restart of `header` has
// been counted. Every restart target nested inside header's natural loop gets
// its per-target count reset to zero and its one-shot fallback latch re-armed
// (Checkpoint.ClearFallbackTaken — see its doc comment for why the enclosing
// header's counted restart is the safe boundary): the enclosing loop has
// advanced to a new iteration, so the inner loops' budgets and escalation
// routes are fresh for the new unit of work (#643). The run-wide aggregate
// Checkpoint.RestartCount is never reset, and the header itself is never in
// its own inner set.
//
// Emits EventRestartBudgetReset per target that actually had restarts on the
// books or a latched fallback, carrying the previous count, the header that
// triggered the reset, and whether the latch was cleared, so `tracker
// diagnose` / audit can show "milestone loop advanced; TestMilestone budget
// reset".
func (e *Engine) resetEnclosedRestartBudgets(s *runState, header string, maxRestarts int) {
	for _, target := range e.loopScopes().innerNodes(header) {
		previous := s.cp.ResetRestartCount(target)
		latchCleared := s.cp.ClearFallbackTaken(target)
		if previous == 0 && !latchCleared {
			continue
		}
		msg := fmt.Sprintf("loop %q advanced: restart budget for nested target %q reset (was %d/%d)",
			header, target, previous, maxRestarts)
		if latchCleared {
			msg += "; fallback latch re-armed"
		}
		e.emit(PipelineEvent{
			Type:      EventRestartBudgetReset,
			Timestamp: time.Now(),
			RunID:     s.runID,
			NodeID:    target,
			Message:   msg,
			Decision: &DecisionDetail{
				RestartCount:         previous,
				ResetBy:              header,
				FallbackLatchCleared: latchCleared,
			},
		})
	}
}
