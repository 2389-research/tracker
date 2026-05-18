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
		if node.Handler != "tool" {
			continue
		}
		cfg := node.ToolConfig()
		if cfg.MarkerGrep != "" {
			continue
		}
		if cfg.OutputLimit > 0 {
			continue
		}
		edges := g.OutgoingEdges(node.ID)
		stdoutCondCount := countConditionsReferencing(edges, "tool_stdout")
		if stdoutCondCount == 0 {
			continue
		}
		if stdoutCondCount >= 2 {
			// Exhaustive enumeration — author has named the expected outputs.
			continue
		}
		if edgesReferenceCtxOutcome(edges) {
			continue
		}
		if !edgesHaveUnconditionalFallback(edges) {
			continue
		}
		if !commandHasVolumeIndicator(cfg.Command) {
			continue
		}
		warnings = append(warnings, fmt.Sprintf(
			"warning[TRK101]: tool node %q routes on ctx.tool_stdout with a single conditional edge plus an unconditional fallback, AND its command emits unbounded output (tee or 2>&1 detected). If total output exceeds output_limit (default 64KB) the tail-window keeps only trailing bytes, so a truncated marker silently routes via the fallback edge — the #208 failure shape. Fix options: (a) declare marker_grep: '<regex>' for a typed routing channel — see CHANGELOG v0.27.0+; (b) set output_limit: <size> large enough for the worst-case output; (c) split the volume-emitting body and the routing-signal printf into two separate tool nodes; (d) enumerate every expected marker as its own conditional edge so any miss surfaces as an unexpected fallback rather than a silent classification flip",
			node.ID))
	}
	return warnings
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
