// ABOUTME: Regression guard for issue #296 — every agent (codergen) node in the
// ABOUTME: shipped build_product.dip must route failure/turn-exhaustion, never dead-stop.
package pipeline

import (
	"os"
	"path/filepath"
	"testing"
)

// loadBuildProduct loads the embedded-on-disk examples/build_product.dip the
// same way the binary embeds it. Relative to the pipeline package dir.
func loadBuildProduct(t *testing.T) *Graph {
	t.Helper()
	path := filepath.Join("..", "examples", "build_product.dip")
	source, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	g, _, err := LoadDippinWorkflow(string(source), "build_product.dip")
	if err != nil {
		t.Fatalf("LoadDippinWorkflow: %v", err)
	}
	return g
}

// resolveFallback returns the node's effective fallback target using the same
// candidate order as Engine.findFallbackTarget, or "" if none resolves. The
// .dip `fallback_target:` keyword lands in node.Attrs["fallback_retry_target"]
// after the IR→Graph adapter (extractRetryAttrs), so a direct read of
// "fallback_target" would miss it — resolve the way the engine does.
func resolveFallback(g *Graph, n *Node) string {
	for _, fb := range []string{
		n.Attrs["fallback_target"],
		n.Attrs["fallback_retry_target"],
		g.Attrs["fallback_target"],
		g.Attrs["fallback_retry_target"],
	} {
		if fb != "" {
			if _, ok := g.Nodes[fb]; ok {
				return fb
			}
		}
	}
	return ""
}

// routesFailure reports whether the engine's strict-failure rule
// (checkStrictFailure → findFallbackTarget) can NOT dead-stop this node: it has
// a conditional outgoing edge (intentional routing) or a resolvable
// fallback_target. This is the exact predicate the engine uses on the main loop.
func routesFailure(g *Graph, nodeID string) bool {
	return hasAnyConditionalEdge(g.OutgoingEdges(nodeID)) || resolveFallback(g, g.Nodes[nodeID]) != ""
}

// parallelBranchParents maps each parallel-branch node ID to the ID of the
// parallel node that dispatches it. A branch node executes via ParallelHandler
// (registry.Execute), bypassing the engine run loop — so checkStrictFailure /
// findFallbackTarget NEVER apply to it and a per-branch fallback_target is
// runtime-inert. A branch node's only real failure route is its parent parallel
// node's aggregate-failure route, so the invariant below must check the parent,
// not the branch (crediting a branch's own fallback would be a false positive).
func parallelBranchParents(g *Graph) map[string]string {
	parents := map[string]string{}
	for _, e := range g.Edges {
		if from := g.Nodes[e.From]; from != nil && from.Handler == "parallel" {
			parents[e.To] = e.From
		}
	}
	return parents
}

// TestBuildProductAgentNodesHaveFailureRouting pins the issue #296 invariant:
// no agent node in build_product.dip can silently halt the pipeline on an
// unhandled failure (e.g. max_turns exhaustion). This is the in-repo
// counterpart to dippin-lang#93's author-time lint.
//
// It mirrors the engine's strict-failure rule exactly (checkStrictFailure /
// findFallbackTarget): a failed node dead-stops only when it has NO conditional
// outgoing edge AND no fallback_target resolves. For a normal (main-loop) agent
// node, that route must be on the node itself. For a parallel-branch agent node
// (the three reviewers), the route must be on its PARENT parallel node — the
// branch itself runs via ParallelHandler and never reaches the strict-failure
// path, so its own fallback would be inert (the Codex finding on PR #312).
func TestBuildProductAgentNodesHaveFailureRouting(t *testing.T) {
	g := loadBuildProduct(t)
	branchParent := parallelBranchParents(g)

	checked := 0
	for _, n := range g.Nodes {
		// Only agent/codergen nodes run a session that can exhaust turns.
		// Passthrough start/exit carry no agent work.
		if n.Handler != "codergen" || n.ID == g.StartNode || n.ID == g.ExitNode {
			continue
		}
		checked++
		// A parallel branch is protected by its parent's aggregate-failure
		// route, not by anything on the branch node itself.
		if parent, isBranch := branchParent[n.ID]; isBranch {
			if !routesFailure(g, parent) {
				t.Errorf("reviewer branch %q can dead-stop on failure: parent parallel node %q has no conditional fail edge and no fallback_target (issue #296)", n.ID, parent)
			}
			continue
		}
		if !routesFailure(g, n.ID) {
			t.Errorf("agent node %q can dead-stop on failure: no conditional fail edge and no fallback_target (issue #296)", n.ID)
		}
	}
	if checked == 0 {
		t.Fatal("no codergen nodes found — loader or fixture changed; invariant not actually exercised")
	}
}

// TestBuildProductIssue296FailureRoutes pins the specific escalation targets so
// a future edit can't satisfy the invariant by routing failure somewhere unsafe
// (a Fix node or a loop target). Main-loop nodes resolve a fallback_target; the
// reviewers' failure is handled on the aggregating ReviewParallel node via a
// conditional fail edge (their own fallback would be inert — see above).
func TestBuildProductIssue296FailureRoutes(t *testing.T) {
	g := loadBuildProduct(t)

	// Main-loop agent nodes: resolvable fallback_target to the right gate.
	wantFallback := map[string]string{
		"Implement":        "EscalateMilestone",
		"ApplyReviewFixes": "EscalateReview",
		"FinalCommit":      "EscalateReview",
	}
	for id, target := range wantFallback {
		if _, ok := g.Nodes[id]; !ok {
			t.Errorf("node %q missing from build_product.dip", id)
			continue
		}
		if got := resolveFallback(g, g.Nodes[id]); got != target {
			t.Errorf("node %q: resolved fallback = %q, want %q (issue #296)", id, got, target)
		}
	}

	// Reviewer branches: failure is routed on ReviewParallel (a conditional fail
	// edge to EscalateReview), since a per-branch fallback_target is inert.
	if !hasConditionalEdgeTo(g, "ReviewParallel", "EscalateReview") {
		t.Errorf("ReviewParallel has no conditional fail edge to EscalateReview — an all-reviewers-fail outcome would dead-stop (issue #296)")
	}

	// The three reviewer branches must carry NO fallback_target: it would be
	// runtime-inert and is intentionally removed so the routing isn't a lie.
	branchParent := parallelBranchParents(g)
	for _, id := range []string{"ReviewClaude", "ReviewCodex", "ReviewGemini"} {
		if branchParent[id] != "ReviewParallel" {
			t.Errorf("expected %q to be a parallel branch of ReviewParallel, got parent %q", id, branchParent[id])
		}
		if fb := resolveNodeFallbackAttr(g.Nodes[id]); fb != "" {
			t.Errorf("reviewer branch %q carries an inert fallback_target=%q; route failure on ReviewParallel instead (issue #296)", id, fb)
		}
	}
}

// TestBuildProductCommitIfDirtyCheckpoint pins issue #297: a CommitIfDirty tool
// node sits on the Implement SUCCESS path so green-but-uncommitted work is
// persisted before TestMilestone runs:
//
//	Implement --(ctx.outcome = success)--> CommitIfDirty --> TestMilestone
//
// while the #296 Implement FAILURE routing (unconditional catch-all edge +
// fallback_target to EscalateMilestone) is left UNCHANGED. CommitIfDirty is
// deliberately NOT on the failure path — turn-exhaustion routes straight to
// EscalateMilestone, and persisting work on that path is engine issue #302.
//
// Negative control: removing the success edge through CommitIfDirty (so
// Implement routes to TestMilestone again) fails this test on both the
// "edge to CommitIfDirty" and "no direct edge to TestMilestone" assertions.
func TestBuildProductCommitIfDirtyCheckpoint(t *testing.T) {
	g := loadBuildProduct(t)

	// CommitIfDirty exists and is a tool node.
	n, ok := g.Nodes["CommitIfDirty"]
	if !ok {
		t.Fatal("CommitIfDirty node missing from build_product.dip (issue #297)")
	}
	if n.Handler != "tool" {
		t.Errorf("CommitIfDirty handler = %q, want \"tool\" (issue #297)", n.Handler)
	}

	// Success path: Implement --(ctx.outcome = success)--> CommitIfDirty.
	// Assert the SUCCESS condition specifically, not just "some condition" — a
	// future edit routing `ctx.outcome = fail` to CommitIfDirty must fail here.
	if !hasEdgeWithCondition(g, "Implement", "CommitIfDirty", "ctx.outcome = success") {
		t.Error("Implement has no `ctx.outcome = success` edge to CommitIfDirty (issue #297)")
	}
	if hasEdgeTo(g, "Implement", "TestMilestone") {
		t.Error("Implement still routes directly to TestMilestone; the success path must go through CommitIfDirty (issue #297)")
	}
	if !hasEdgeTo(g, "CommitIfDirty", "TestMilestone") {
		t.Error("CommitIfDirty has no edge to TestMilestone (issue #297)")
	}

	// #296 failure routing must be intact: unconditional catch-all + fallback.
	if !hasUnconditionalEdgeTo(g, "Implement", "EscalateMilestone") {
		t.Error("Implement lost its unconditional catch-all to EscalateMilestone (issue #296 regression)")
	}
	if got := resolveFallback(g, g.Nodes["Implement"]); got != "EscalateMilestone" {
		t.Errorf("Implement resolved fallback = %q, want EscalateMilestone (issue #296 regression)", got)
	}
}

// TestBuildProductIssue303GreenBreachRescuePath pins the #303 case-study rescue
// at the routing level (NOT the engine artifact repo, which is off by default
// and commits a different dir). With the graduated guard, a turn-limit breach
// whose tree verifies green returns OutcomeSuccess, so Implement takes its
// `ctx.outcome = success` edge to CommitIfDirty — which commits the product
// working tree (#297) — instead of the failure edge to EscalateMilestone.
// That is exactly how the code-goblin run 7b6e08c9e2b2 (green at turn 48 but
// uncommitted) would now be saved. This test guards against a future .dip edit
// that reroutes the success edge and silently breaks the rescue.
func TestBuildProductIssue303GreenBreachRescuePath(t *testing.T) {
	g := loadBuildProduct(t)
	if !hasEdgeWithCondition(g, "Implement", "CommitIfDirty", "ctx.outcome = success") {
		t.Error("Implement success edge no longer reaches CommitIfDirty — #303 green-breach work would not be persisted")
	}
	if !hasEdgeTo(g, "CommitIfDirty", "TestMilestone") {
		t.Error("CommitIfDirty must continue to TestMilestone so a rescued green breach advances")
	}
}

// hasEdgeTo reports whether the node has any outgoing edge to the given target.
func hasEdgeTo(g *Graph, from, to string) bool {
	for _, e := range g.OutgoingEdges(from) {
		if e.To == to {
			return true
		}
	}
	return false
}

// hasEdgeWithCondition reports whether the node has an outgoing edge to the
// given target whose condition matches exactly.
func hasEdgeWithCondition(g *Graph, from, to, cond string) bool {
	for _, e := range g.OutgoingEdges(from) {
		if e.To == to && e.Condition == cond {
			return true
		}
	}
	return false
}

// hasUnconditionalEdgeTo reports whether the node has an outgoing edge to the
// given target with no condition (the strict-failure catch-all).
func hasUnconditionalEdgeTo(g *Graph, from, to string) bool {
	for _, e := range g.OutgoingEdges(from) {
		if e.To == to && e.Condition == "" {
			return true
		}
	}
	return false
}

// hasConditionalEdgeTo reports whether the node has an outgoing conditional edge
// to the given target.
func hasConditionalEdgeTo(g *Graph, from, to string) bool {
	for _, e := range g.OutgoingEdges(from) {
		if e.To == to && e.Condition != "" {
			return true
		}
	}
	return false
}

// resolveNodeFallbackAttr returns the node's own fallback_target attr (either
// spelling), ignoring graph-level defaults — used to assert a branch node
// carries no node-level fallback of its own.
func resolveNodeFallbackAttr(n *Node) string {
	if v := n.Attrs["fallback_target"]; v != "" {
		return v
	}
	return n.Attrs["fallback_retry_target"]
}
