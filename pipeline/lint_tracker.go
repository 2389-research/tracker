// ABOUTME: Tracker-specific lint rules (TRK1XX). Encodes tracker's runtime
// ABOUTME: defaults — 64KB tool output cap, tail-window capture semantics —
// ABOUTME: that don't belong in dippin-lang itself but warrant validate-time
// ABOUTME: warnings because tracker owns the runtime.
package pipeline

import (
	"fmt"
	"strings"
)

// LintTrackerRules runs tracker-specific lint rules (TRK1XX). DIP1XX lint
// (DIP101–DIP137, etc.) is owned by dippin-lang and runs at .dip load time
// via LoadDippinWorkflow → validator.Lint; tracker does not duplicate it,
// so this is the only lint entry point tracker should expose.
func LintTrackerRules(g *Graph) []string {
	var warnings []string
	warnings = append(warnings, lintTRK101(g)...)
	warnings = append(warnings, lintTRK102(g)...)
	return warnings
}

// lintTRK101 warns about tool nodes that route on ctx.tool_stdout with
// an unconditional-fallback foot-gun shape (issue #211, the structural
// counterpart to #208 / #210). The failure mode: a tool node emits a
// large amount of output before its trailing routing marker; if the
// total exceeds output_limit (default 64KB), the tail-window keeps
// only the trailing bytes — but a conditional edge that doesn't match
// silently routes via the unconditional fallback. Result: broken code
// ships as if it had passed.
//
// Fires when ALL of:
//
//  1. Handler is "tool"
//  2. Exactly one outgoing edge condition references ctx.tool_stdout
//     (the asymmetric shape that masks truncation; see "Skipped when"
//     below for the contrast)
//  3. At least one outgoing edge is unconditional (the silent fallback)
//  4. No marker_grep attr (the structural fix from #210)
//  5. No explicit output_limit (relies on the 64KB default)
//  6. Command body contains a volume-emitting indicator: `tee` or
//     `2>&1` — the canonical patterns in #208's notebook_smoke
//     reproducer. Other risky shapes (single `|` to a small filter)
//     are not flagged to keep the false-positive rate low.
//
// Skipped when:
//
//   - The node also routes on ctx.outcome. The operator has acknowledged
//     the exit code as the primary signal; tool_stdout is a secondary
//     classification and the tail-window capture preserves any trailing
//     marker.
//   - The node has 2+ conditional edges referencing ctx.tool_stdout.
//     That's the "exhaustive enumeration" shape (e.g. `= contracts_pass`,
//     `= contracts_fail`, `= merge_conflict`, with an unconditional
//     fallback only for "anything else") — the author has named the
//     expected outputs, so an unmatched-because-truncated edge is
//     much less likely to silently pick the fallback. The dangerous
//     shape is 1 conditional + 1 unconditional, where "no match"
//     reads exactly like "expected match for the unconditional path."
func lintTRK101(g *Graph) []string {
	var warnings []string
	for _, node := range g.Nodes {
		if trk101NodeViolates(g, node) {
			warnings = append(warnings, fmt.Sprintf(
				"warning[TRK101]: tool node %q routes on ctx.tool_stdout with a single conditional edge plus an unconditional fallback, AND its command emits unbounded output (tee or 2>&1 detected). If total output exceeds output_limit (default 64KB) the tail-window keeps only trailing bytes, so a truncated marker silently routes via the fallback edge — the #208 failure shape. Fix options: (a) declare marker_grep: '<regex>' for a typed routing channel — see CHANGELOG v0.27.0+; (b) set output_limit: <size> large enough for the worst-case output; (c) split the volume-emitting body and the routing-signal printf into two separate tool nodes; (d) enumerate every expected marker as its own conditional edge so any miss surfaces as an unexpected fallback rather than a silent classification flip",
				node.ID))
		}
	}
	return warnings
}

// trk101NodeViolates reports whether a node exhibits the #208 silent-fallback
// foot-gun: a tool node with no marker_grep / output_limit guard, whose edges
// route silently on a single tool_stdout marker, and whose command emits
// unbounded output. Guard order preserved from the original inline checks.
func trk101NodeViolates(g *Graph, node *Node) bool {
	if node.Handler != "tool" {
		return false
	}
	cfg := node.ToolConfig()
	if cfg.MarkerGrep != "" || cfg.OutputLimit > 0 {
		return false
	}
	if !trk101EdgesRouteSilently(g.OutgoingEdges(node.ID)) {
		return false
	}
	return commandHasVolumeIndicator(cfg.Command)
}

// trk101EdgesRouteSilently reports whether the outgoing edges form the
// silent-fallback shape: exactly one tool_stdout conditional edge (0 = no
// stdout routing, >=2 = exhaustive enumeration — both safe), no ctx.outcome
// routing already adopted, and at least one unconditional fallback edge.
func trk101EdgesRouteSilently(edges []*Edge) bool {
	if countConditionsReferencing(edges, "tool_stdout") != 1 {
		return false
	}
	if edgesReferenceCtxOutcome(edges) {
		return false
	}
	return edgesHaveUnconditionalFallback(edges)
}

// countConditionsReferencing returns the number of edges whose
// condition references the given context key (e.g. "tool_stdout" or
// "outcome"). Both "ctx.<key>" and "context.<key>" spellings count
// since tracker's condition evaluator strips either prefix at runtime.
func countConditionsReferencing(edges []*Edge, key string) int {
	n := 0
	for _, e := range edges {
		if e.Condition == "" {
			continue
		}
		if strings.Contains(e.Condition, "ctx."+key) ||
			strings.Contains(e.Condition, "context."+key) {
			n++
		}
	}
	return n
}

// edgesReferenceCtxOutcome reports whether any edge's condition
// references ctx.outcome. Used to skip TRK101 on nodes that have
// already adopted exit-code-driven routing as a primary signal.
func edgesReferenceCtxOutcome(edges []*Edge) bool {
	for _, e := range edges {
		if e.Condition == "" {
			continue
		}
		c := e.Condition
		if strings.Contains(c, "ctx.outcome") ||
			strings.Contains(c, "context.outcome") {
			return true
		}
	}
	return false
}

// edgesHaveUnconditionalFallback reports whether at least one edge has
// no condition — the silent-fallback path that makes TRK101 dangerous.
func edgesHaveUnconditionalFallback(edges []*Edge) bool {
	for _, e := range edges {
		if e.Condition == "" {
			return true
		}
	}
	return false
}

// commandHasVolumeIndicator reports whether a tool_command body contains
// a known volume-emitting pattern. Word-boundary check on `tee` to
// avoid false positives like "guarantee" or "committee"; substring
// check on `2>&1` is fine since it has no benign substring meaning.
func commandHasVolumeIndicator(cmd string) bool {
	if strings.Contains(cmd, "2>&1") {
		return true
	}
	// Walk the command looking for `tee` as a standalone word/argument.
	// A simple substring check on "tee" would false-positive on
	// "guarantee" / "committee" / etc.
	for _, field := range strings.Fields(cmd) {
		if field == "tee" {
			return true
		}
	}
	return false
}

// trk102OverrideLabels enumerates the accept-shape edge labels that
// suggest a wait.human gate is accepting a failed upstream — the
// canonical override audit shape from spec §7.4. Matched
// case-insensitively after whitespace trim.
var trk102OverrideLabels = map[string]bool{
	"accept":    true,
	"mark done": true,
	"approve":   true,
}

// lintTRK102 warns when an edge from a wait.human gate looks like an
// override path (accepts a failed upstream and continues forward) but
// is not marked override: true. See spec §7.4.
//
// Fires when ALL of:
//
//  1. Source node's handler is "wait.human" (only gates can emit
//     override edges per the audit-only routing contract on
//     Edge.Override).
//  2. Edge label (case-insensitive, whitespace-trimmed) matches one of
//     "accept", "mark done", "approve". These are the canonical labels
//     pipeline authors use when accepting a failed validation.
//  3. The edge's target reaches the graph's exit node without passing
//     through another wait.human gate. The presence of another gate
//     downstream means the operator has more decisions to make — the
//     current edge is not the final accept-and-forward signal.
//  4. The source gate is reachable via at least one upstream edge whose
//     condition references ctx.outcome = fail. This is the predicate
//     that suppresses false positives on plan-approval gates
//     (ApprovePlan, ApproveSpec, ReviewPlan) — those sit on success
//     paths without an upstream failure condition.
//
// Skipped when:
//
//   - The edge is already marked Override:true — the author has
//     already recorded the audit signal.
func lintTRK102(g *Graph) []string {
	var warnings []string
	for _, e := range g.Edges {
		if trk102EdgeIsUnmarkedOverride(g, e) {
			warnings = append(warnings, fmt.Sprintf(
				"warning[TRK102]: edge from wait.human node %q to forward-progress node %q via label %q is not marked override: true. The gate is reachable from an upstream failure (ctx.outcome = fail), which suggests this edge represents accepting a failed validation. Add override: true on the edge so the run's terminal status is reported as validation_overridden (audit-only; routing is unaffected). See spec §7.4.",
				e.From, e.To, e.Label))
		}
	}
	return warnings
}

// trk102EdgeIsUnmarkedOverride reports whether e is an accept-shape edge from a
// wait.human gate that escalates a failed validation and forwards to the exit
// without another gate, but is not marked override: true. The four predicates
// mirror the rule's original inline guards.
func trk102EdgeIsUnmarkedOverride(g *Graph, e *Edge) bool {
	if e.Override {
		return false // already marked — no warning needed
	}
	if !trk102OverrideLabels[strings.ToLower(strings.TrimSpace(e.Label))] {
		return false // predicate 2: label must match
	}
	srcNode, ok := g.Nodes[e.From]
	if !ok || srcNode.Handler != "wait.human" {
		return false // predicate 1: source must be wait.human
	}
	if !trk102TargetReachesExitWithoutGate(g, e.To) {
		return false // predicate 3: target must reach exit without another gate
	}
	return trk102GateReachableViaFailEdge(g, e.From) // predicate 4
}

// trk102TargetReachesExitWithoutGate reports whether there is a path
// from target to the graph's exit node that does not pass through any
// wait.human gate (the target itself counts: if the override edge
// drops the operator into another gate, the accept-and-forward
// contract is not satisfied and predicate 3 fails).
//
// If the graph has no exit node (e.g. ad-hoc test fixtures), the
// rule conservatively returns false so TRK102 stays silent rather
// than firing on partially-constructed graphs.
func trk102TargetReachesExitWithoutGate(g *Graph, target string) bool {
	if g.ExitNode == "" {
		return false
	}
	visited := make(map[string]bool)
	return trk102DFSExit(g, target, g.ExitNode, visited)
}

// trk102DFSExit walks outgoing edges from node, returning true if a
// path to exitID exists that does not pass through a wait.human gate.
// Any wait.human node on the path (including the entry node) fails
// the predicate — see trk102TargetReachesExitWithoutGate for why.
func trk102DFSExit(g *Graph, node, exitID string, visited map[string]bool) bool {
	if visited[node] {
		return false
	}
	visited[node] = true
	if node == exitID {
		return true
	}
	n, ok := g.Nodes[node]
	if !ok {
		return false
	}
	if n.Handler == "wait.human" {
		return false
	}
	for _, e := range g.OutgoingEdges(node) {
		if trk102DFSExit(g, e.To, exitID, visited) {
			return true
		}
	}
	return false
}

// trk102GateReachableViaFailEdge reports whether the gate node is a DIRECT
// escalation target of a validation failure: at least one edge INCOMING to
// gateNodeID carries an outcome=fail condition, or the gate is named as a
// node/graph fallback_target (which fires on failure/exhaustion).
//
// Direct — not transitive — is the disambiguator that separates escalation
// gates from plan-approval gates. An escalation gate like EscalateReview has a
// direct `X -> gate when ctx.outcome = fail` edge; a plan-approval gate like
// ApprovePlan is entered via forward/unconditional flow (`ShowPlan -> ApprovePlan`).
// A transitive reverse walk mis-flags plan gates in cyclic workflows: it reaches
// unrelated upstream failures on the shared forward spine (e.g. build_product's
// spec-forge loop), which is not a failure escalating INTO the plan gate. This
// matches the rule's own stated intent — "only failures that flow directly into
// the current gate count."
//
// We accept "outcome=fail", "outcome = fail", "ctx.outcome=fail",
// "context.outcome = fail" — the same shapes the runtime condition
// evaluator accepts.
func trk102GateReachableViaFailEdge(g *Graph, gateNodeID string) bool {
	for _, e := range g.incoming[gateNodeID] {
		if edgeConditionMatchesOutcomeFail(e.Condition) {
			return true
		}
	}
	// A fallback_target (node-level or graph-level on_failure) routes to the
	// gate on failure/exhaustion — a direct failure-escalation signal too, and
	// never a plan-approval gate.
	return trk102IsFallbackTarget(g, gateNodeID)
}

// trk102IsFallbackTarget reports whether gateNodeID is named as a node-level or
// graph-level fallback_target / fallback_retry_target.
func trk102IsFallbackTarget(g *Graph, gateNodeID string) bool {
	if g.Attrs["fallback_target"] == gateNodeID || g.Attrs["fallback_retry_target"] == gateNodeID {
		return true
	}
	for _, n := range g.Nodes {
		if n.Attrs["fallback_target"] == gateNodeID || n.Attrs["fallback_retry_target"] == gateNodeID {
			return true
		}
	}
	return false
}

// edgeConditionMatchesOutcomeFail reports whether the given edge
// condition string is one of the recognized outcome=fail shapes.
// Whitespace-insensitive; accepts the ctx.* and context.* prefixes
// that the runtime evaluator strips.
func edgeConditionMatchesOutcomeFail(cond string) bool {
	if cond == "" {
		return false
	}
	stripped := strings.ToLower(strings.ReplaceAll(cond, " ", ""))
	if strings.Contains(stripped, "ctx.outcome=fail") {
		return true
	}
	if strings.Contains(stripped, "context.outcome=fail") {
		return true
	}
	if strings.Contains(stripped, "outcome=fail") {
		// Bare "outcome = fail" is the spelling used in some
		// pipelines that pre-date the ctx. prefix convention.
		return true
	}
	return false
}
