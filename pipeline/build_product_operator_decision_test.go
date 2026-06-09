// ABOUTME: Graph-structure guards for #318 — the operator-decision node that a
// ABOUTME: steady-progress turn breach (turn_breach_class=operator_decision) routes
// ABOUTME: to, its four labeled edges, the safe autonomous default, and the cap loop.
package pipeline

import "testing"

const operatorNodeID = "OperatorDecision"

// outgoingLabels returns the labels of nodeID's outgoing edges in declaration
// order (empty-label edges are skipped, matching collectEdgeLabels in the
// human handler — the unattended freeform fallback uses labels[0]).
func outgoingLabels(g *Graph, nodeID string) []string {
	var labels []string
	for _, e := range g.OutgoingEdges(nodeID) {
		if e.Label != "" {
			labels = append(labels, e.Label)
		}
	}
	return labels
}

// hasLabeledEdgeTo reports whether nodeID has an outgoing edge to target carrying
// the given label.
func hasLabeledEdgeTo(g *Graph, from, to, label string) bool {
	for _, e := range g.OutgoingEdges(from) {
		if e.To == to && e.Label == label {
			return true
		}
	}
	return false
}

// reachesNode reports whether target is reachable from start by following edges
// (cycle-safe via a visited set). Used to assert every operator edge terminates.
func reachesNode(g *Graph, start, target string) bool {
	visited := map[string]bool{}
	stack := []string{start}
	for len(stack) > 0 {
		cur := stack[len(stack)-1]
		stack = stack[:len(stack)-1]
		if cur == target {
			return true
		}
		if visited[cur] {
			continue
		}
		visited[cur] = true
		for _, e := range g.OutgoingEdges(cur) {
			stack = append(stack, e.To)
		}
	}
	return false
}

// indexOf returns the position of s in xs, or -1.
func indexOf(xs []string, s string) int {
	for i, x := range xs {
		if x == s {
			return i
		}
	}
	return -1
}

// TestBuildProductOperatorDecisionNodeExists pins #318: a steady-progress turn
// breach routes to a reusable wait.human (freeform) operator-decision node — NOT
// a new handler. Implement reaches it via a narrow conditional edge keyed on
// turn_breach_class = operator_decision, ordered so the unconditional
// EscalateMilestone catch-all still receives a pathological breach.
func TestBuildProductOperatorDecisionNodeExists(t *testing.T) {
	g := loadBuildProduct(t)

	n, ok := g.Nodes[operatorNodeID]
	if !ok {
		t.Fatalf("%s node missing from build_product.dip (issue #318)", operatorNodeID)
	}
	if n.Handler != "wait.human" {
		t.Errorf("%s handler = %q, want \"wait.human\" (reuse, no new handler — issue #318)", operatorNodeID, n.Handler)
	}
	if mode := n.Attrs["mode"]; mode != "freeform" {
		t.Errorf("%s mode = %q, want \"freeform\" (labeled radio + freeform hybrid — issue #318)", operatorNodeID, mode)
	}

	// Reached only by the operator_decision class. A narrow condition keeps
	// pathological breaches (and plain fails) on the catch-all.
	if !hasEdgeWithCondition(g, "Implement", operatorNodeID, "ctx.turn_breach_class = operator_decision") {
		t.Errorf("Implement has no `ctx.turn_breach_class = operator_decision` edge to %s (issue #318)", operatorNodeID)
	}
}

// TestBuildProductOperatorDecisionSafeDefault pins the CRITICAL hazard 1: the
// unattended default must deterministically resolve to the safe, non-advancing
// option ("stop"), never "continue". Two operative mechanisms, asserted here:
//
//   - Edge order: the freeform interviewer's unattended path falls back to the
//     FIRST outgoing edge label (labels[0]); dippin's `default:` keyword maps to
//     `default_choice`, which freeform's labels[0] fallback does NOT read — so
//     edge order is what drives --auto-approve / --webhook-url. "stop"/"abandon"
//     MUST precede "continue" so labels[0] is safe.
//   - default_choice: dippin maps `default: stop` here; it is the value the gate's
//     TIMEOUT path resolves (HumanConfig.DefaultChoice) and documents intent.
//
// The end-to-end resolution (AutoApprove → "stop") is pinned behaviorally in the
// handlers package (TestOperatorDecision_UnattendedResolvesToStop).
func TestBuildProductOperatorDecisionSafeDefault(t *testing.T) {
	g := loadBuildProduct(t)
	if g.Nodes[operatorNodeID] == nil {
		t.Fatalf("%s node missing from build_product.dip (issue #318)", operatorNodeID)
	}

	// dippin maps the `default:` keyword to default_choice (the timeout-path default).
	if def := g.Nodes[operatorNodeID].HumanConfig().DefaultChoice; def != "stop" {
		t.Errorf("%s resolved default = %q, want \"stop\" — unattended/timeout must NOT silently advance unverified work (issue #318 hazard 1)", operatorNodeID, def)
	}

	labels := outgoingLabels(g, operatorNodeID)
	iStop, iAbandon, iContinue := indexOf(labels, "stop"), indexOf(labels, "abandon"), indexOf(labels, "continue")
	if iStop < 0 || iAbandon < 0 || iContinue < 0 {
		t.Fatalf("%s missing one of stop/abandon/continue labels: %v (issue #318)", operatorNodeID, labels)
	}
	// labels[0] is the freeform auto-approve fallback; it must never be "continue".
	if labels[0] == "continue" {
		t.Errorf("first edge label is %q — the auto-approve labels[0] fallback must be safe, never \"continue\" (issue #318 hazard 1)", labels[0])
	}
	if !(iStop < iContinue && iAbandon < iContinue) {
		t.Errorf("edge order %v: stop(%d)/abandon(%d) must precede continue(%d) so labels[0] fallback is safe (issue #318 hazard 1)", labels, iStop, iAbandon, iContinue)
	}
}

// TestBuildProductOperatorDecisionFourEdges pins the four labeled choices and
// their safe targets, and that every operator edge reaches the exit (DIP005 —
// no dangling label). stop escalates (human can still rescue), abandon ends the
// run without advancing, commit_advance persists the tree via the existing
// commit-on-success node, continue routes through the capped warm-retry tool.
func TestBuildProductOperatorDecisionFourEdges(t *testing.T) {
	g := loadBuildProduct(t)

	wantTargets := map[string]string{
		"stop":           "EscalateMilestone",
		"abandon":        g.ExitNode,
		"commit_advance": "CommitIfDirty",
		"continue":       "ContinueWithMoreTurns",
	}
	for label, target := range wantTargets {
		if !hasLabeledEdgeTo(g, operatorNodeID, target, label) {
			t.Errorf("%s missing labeled edge %q -> %s (issue #318)", operatorNodeID, label, target)
		}
	}
	for _, e := range g.OutgoingEdges(operatorNodeID) {
		if !reachesNode(g, e.To, g.ExitNode) {
			t.Errorf("%s -> %s (label %q) does not reach exit %q (DIP005, issue #318)", operatorNodeID, e.To, e.Label, g.ExitNode)
		}
	}
}

// TestBuildProductContinueCapLoop pins hazard 3: the continue cap is a TOOL node
// gating the warm-retry edge (a per-loop disk counter, not the global engine
// RestartCount). On success it re-enters Implement (restart: true → warm
// continue+N); on fail (cap exhausted) it escalates instead of looping forever.
func TestBuildProductContinueCapLoop(t *testing.T) {
	g := loadBuildProduct(t)

	n, ok := g.Nodes["ContinueWithMoreTurns"]
	if !ok {
		t.Fatal("ContinueWithMoreTurns node missing from build_product.dip (issue #318 hazard 3)")
	}
	if n.Handler != "tool" {
		t.Errorf("ContinueWithMoreTurns handler = %q, want \"tool\" (cap circuit-breaker, issue #318)", n.Handler)
	}
	if !hasEdgeAttr(g, "ContinueWithMoreTurns", "Implement", "ctx.outcome = success", "restart", "true") {
		t.Error("ContinueWithMoreTurns -> Implement must be `ctx.outcome = success` with restart: true (warm continue+N, issue #318)")
	}
	if !hasEdgeWithCondition(g, "ContinueWithMoreTurns", "EscalateMilestone", "ctx.outcome = fail") {
		t.Error("ContinueWithMoreTurns -> EscalateMilestone must be the `ctx.outcome = fail` (cap-exhausted) route (issue #318)")
	}
}

// TestBuildProductOperatorDecisionPersistsViaCommitNode documents and pins the
// resolution of #318 hazard 4 (commitWIPBeforeRouting bypass). The engine's WIP
// preservation runs only inside checkStrictFailure, which early-returns when a
// node has ANY conditional edge — and Implement already carried conditional edges
// before this change (the `ctx.outcome = success` rescue), so routing a breach to
// the operator gate introduces NO new bypass. It is also a no-op in normal runs
// (WithGitArtifacts is off by default). Green work is therefore persisted the way
// ALL product persistence works in tracker — through the pipeline's own
// commit-on-success node, not an engine commit: the operator's work-preserving
// choices route to CommitIfDirty (commit_advance) or EscalateMilestone (stop,
// whose "mark done" leads onward), never dropping the tree.
func TestBuildProductOperatorDecisionPersistsViaCommitNode(t *testing.T) {
	g := loadBuildProduct(t)

	// commit_advance persists the tree via the existing commit node, then advances.
	if !hasLabeledEdgeTo(g, operatorNodeID, "CommitIfDirty", "commit_advance") {
		t.Error("operator commit_advance must route to CommitIfDirty (the commit-on-success node) to persist work (issue #318 hazard 4)")
	}
	if !hasEdgeTo(g, "CommitIfDirty", "TestMilestone") {
		t.Error("CommitIfDirty must continue to TestMilestone so commit_advance advances the milestone (issue #318)")
	}
	// stop escalates to a gate that can still preserve/route the work (mark done).
	if !hasLabeledEdgeTo(g, operatorNodeID, "EscalateMilestone", "stop") {
		t.Error("operator stop must route to EscalateMilestone, not drop the milestone (issue #318)")
	}
}

// TestBuildProductPathologicalStillFallsThrough pins that adding the narrow
// operator_decision conditional edge does NOT capture a pathological breach: the
// unconditional Implement -> EscalateMilestone catch-all (and the resolved
// fallback_target) must remain, so a looping agent still stops (issue #296/#303).
func TestBuildProductPathologicalStillFallsThrough(t *testing.T) {
	g := loadBuildProduct(t)

	if !hasUnconditionalEdgeTo(g, "Implement", "EscalateMilestone") {
		t.Error("Implement lost its unconditional catch-all to EscalateMilestone — a pathological breach would no longer fall through (issue #296/#303)")
	}
	if got := resolveFallback(g, g.Nodes["Implement"]); got != "EscalateMilestone" {
		t.Errorf("Implement resolved fallback = %q, want EscalateMilestone (issue #296 regression)", got)
	}
	// The green-breach rescue (success edge through CommitIfDirty) is untouched.
	if !hasEdgeWithCondition(g, "Implement", "CommitIfDirty", "ctx.outcome = success") {
		t.Error("Implement success edge to CommitIfDirty must remain (issue #297/#303)")
	}
}
