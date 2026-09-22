// ABOUTME: Regression guard for issue #296 — every agent (codergen) node in the
// ABOUTME: shipped build_product.dip must route failure/turn-exhaustion, never dead-stop.
package pipeline

import (
	"os"
	"path/filepath"
	"testing"
)

// loadBuildProduct loads the embedded-on-disk examples/build_product.dip the
// same way the binary embeds it. Relative to the pipeline package dir. The
// graph gets ${graph.workflow_dir} seeded to examples/ as a CLI disk load
// would (SeedWorkflowDir), so tool bodies that source the shared
// scripts/build_product/lib/ helpers resolve them when a test runs them.
func loadBuildProduct(t *testing.T) *Graph {
	t.Helper()
	path := filepath.Join("..", "examples", "build_product.dip")
	source, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	g, _, err := LoadDippinWorkflow(string(source), path)
	if err != nil {
		t.Fatalf("LoadDippinWorkflow: %v", err)
	}
	SeedWorkflowDir(g, path)
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
	// ApplyReviewFixes failing leaves an UNVERIFIED tree, so it lands on the
	// abandon-default EscalateVerification gate; FinalCommit fails only after
	// the build is green + spec-checked, so the accept-default EscalateReview
	// remains right for it (unattended-fail-open split).
	wantFallback := map[string]string{
		"Implement":        "EscalateMilestone",
		"ApplyReviewFixes": "EscalateVerification",
		// FinalCommit is a deterministic tool node since #656 (not an agent
		// with a fallback_target); its failure routing is pinned by
		// TestBuildProductFinalCommitToolRoutes below.
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
	// edge to EscalateVerification), since a per-branch fallback_target is inert.
	if !hasConditionalEdgeTo(g, "ReviewParallel", "EscalateVerification") {
		t.Errorf("ReviewParallel has no conditional fail edge to EscalateVerification — an all-reviewers-fail outcome would dead-stop (issue #296)")
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

// TestBuildProductFinalCommitToolRoutes pins the #656 fix's routing half:
// FinalCommit is a tool node whose outcome routes on EXHAUSTIVE conditional
// edges — success -> Done, fail -> AbortRun. This replaces the old sole
// unconditional `FinalCommit -> Done` + `fallback_target: EscalateReview`,
// under which a spurious clean-tree failure fell back to EscalateReview,
// EscalateReview's auto-approve `accept` looped back through Cleanup to
// FinalCommit, and the one-shot fallback latch (keyed by FinalCommit's own id)
// dead-stopped the run with "no conditional edges to handle failure". A
// conditional fail edge means the strict-failure/latch path is never entered
// (engine.go checkStrictFailure short-circuits on any conditional edge), so a
// genuine failure routes ONCE to the AbortRun terminal and ends the run fail.
func TestBuildProductFinalCommitToolRoutes(t *testing.T) {
	g := loadBuildProduct(t)
	if _, ok := g.Nodes["FinalCommit"]; !ok {
		t.Fatal("build_product.dip has no FinalCommit node")
	}
	if !hasConditionalEdgeTo(g, "FinalCommit", "Done") {
		t.Error("FinalCommit has no conditional success edge to Done — a tool node's success must route to Done (issue #656)")
	}
	if !hasConditionalEdgeTo(g, "FinalCommit", "AbortRun") {
		t.Error("FinalCommit has no conditional fail edge to AbortRun — a genuine commit failure must route to the AbortRun terminal, not dead-stop or loop back through EscalateReview (issue #656)")
	}
	// No unconditional edge out of FinalCommit: an unconditional sibling to a
	// conditional set is the exact shape that re-opens the strict-failure/latch
	// path the fix closes.
	for _, e := range g.OutgoingEdges("FinalCommit") {
		if e.Condition == "" {
			t.Errorf("FinalCommit has an unconditional edge to %q — success/fail must be exhaustive conditional edges so the strict-failure latch path is never entered (issue #656)", e.To)
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

// TestBuildProductIssue313ReviewGate pins the #313 reviewer-completeness guard.
// The parallel fan-in is success-if-any, so a single reviewer that exhausts
// max_turns and never writes its report is masked: ReviewParallel aggregates to
// success and flows to SynthesizeReviews with that review silently missing. The
// engine-level fix (configurable fan-in policy) cannot be expressed in dippin
// v0.35.0 (parallel/fan_in nodes carry no attributes), so the symptom is fixed
// at the workflow layer:
//
//   - ClearStaleReviews (tool) runs before ReviewParallel on BOTH inbound paths
//     so the capped re-review restart loop can't satisfy the guard with a stale
//     report from a prior round.
//   - CheckReviewsComplete (tool) runs after ReviewJoin and fails unless all
//     three review files are present + non-empty, routing a partial set to the
//     abandon-default EscalateVerification human gate instead of SynthesizeReviews.
func TestBuildProductIssue313ReviewGate(t *testing.T) {
	g := loadBuildProduct(t)

	// All three guards are tool nodes (CheckMilestoneOutputs joined in #350).
	for _, id := range []string{"CheckReviewsComplete", "ClearStaleReviews", "CheckMilestoneOutputs"} {
		n, ok := g.Nodes[id]
		if !ok {
			t.Fatalf("%s node missing from build_product.dip (issue #313)", id)
		}
		if n.Handler != "tool" {
			t.Errorf("%s handler = %q, want \"tool\" (issue #313)", id, n.Handler)
		}
	}

	// Post-join guard: ReviewJoin -> CheckReviewsComplete -> {SynthesizeReviews|EscalateVerification}.
	if !hasEdgeTo(g, "ReviewJoin", "CheckReviewsComplete") {
		t.Error("ReviewJoin has no edge to CheckReviewsComplete — partial review sets would reach synthesis (issue #313)")
	}
	if hasEdgeTo(g, "ReviewJoin", "SynthesizeReviews") {
		t.Error("ReviewJoin still routes directly to SynthesizeReviews; it must go through CheckReviewsComplete (issue #313)")
	}
	if !hasEdgeWithCondition(g, "CheckReviewsComplete", "SynthesizeReviews", "ctx.outcome = success") {
		t.Error("CheckReviewsComplete has no `ctx.outcome = success` edge to SynthesizeReviews (issue #313)")
	}
	if !hasEdgeWithCondition(g, "CheckReviewsComplete", "EscalateVerification", "ctx.outcome = fail") {
		t.Error("CheckReviewsComplete has no `ctx.outcome = fail` edge to EscalateVerification — a missing review would not escalate (issue #313)")
	}

	// Pre-fan-out guards: both inbound paths to ReviewParallel pass through
	// the #350 structural existence gate (CheckMilestoneOutputs) and then
	// ClearStaleReviews. The #313 invariant — stale reports cleared on BOTH
	// entries — is preserved with the gate prepended.
	// #418 inserts ComputeReviewDiff between ClearStaleReviews and the fan-out
	// so the reviewers read a bounded base..worktree diff (BASE vs the working
	// tree, capturing uncommitted edits — not BASE..HEAD); the #313 invariant
	// (stale reports cleared before the fan-out runs) is preserved across the
	// extra hop.
	if !hasUnconditionalEdgeTo(g, "ClearStaleReviews", "ComputeReviewDiff") {
		t.Error("ClearStaleReviews has no edge to ComputeReviewDiff (issues #313/#418)")
	}
	if !hasUnconditionalEdgeTo(g, "ComputeReviewDiff", "ReviewParallel") {
		t.Error("ComputeReviewDiff has no edge to ReviewParallel (issues #313/#418)")
	}
	// #640 A3: markers route on `endswith` (exact end-of-stdout), not `contains`.
	if !hasEdgeWithCondition(g, "PickNextMilestone", "CheckMilestoneOutputs", "ctx.tool_stdout endswith all-done") {
		t.Error("PickNextMilestone all-done edge must enter CheckMilestoneOutputs before the review fan-out (issue #350)")
	}
	if !hasEdgeWithCondition(g, "CheckReviewFixBudget", "CheckMilestoneOutputs", "ctx.outcome = success") {
		t.Error("CheckReviewFixBudget re-review edge must enter CheckMilestoneOutputs before the review fan-out (issue #350)")
	}
	// The re-review edge carries the restart loop; pin restart:true so a future
	// edit can't drop the loop semantics (which would also stop clearing stale
	// reports each pass).
	if !hasEdgeAttr(g, "CheckReviewFixBudget", "CheckMilestoneOutputs", "ctx.outcome = success", "restart", "true") {
		t.Error("CheckReviewFixBudget -> CheckMilestoneOutputs must keep restart: true (issues #313/#350)")
	}
	// Gate routing (#350): success marker proceeds to ClearStaleReviews; the
	// missing marker escalates to the operator-recoverable human gate — never
	// an auto-fail loop (a declared deletion can false-positive).
	if !hasEdgeWithCondition(g, "CheckMilestoneOutputs", "ClearStaleReviews", "ctx.tool_stdout endswith outputs-present") {
		t.Error("CheckMilestoneOutputs has no outputs-present edge to ClearStaleReviews (issue #350)")
	}
	if !hasEdgeWithCondition(g, "CheckMilestoneOutputs", "EscalateMilestone", "ctx.tool_stdout endswith outputs-missing") {
		t.Error("CheckMilestoneOutputs has no outputs-missing edge to EscalateMilestone — a structurally absent milestone would not escalate (issue #350)")
	}
	if hasEdgeTo(g, "PickNextMilestone", "ReviewParallel") {
		t.Error("PickNextMilestone still routes directly to ReviewParallel; it must pass the existence gate and clear stale reviews first (issues #313/#350)")
	}
	if hasEdgeTo(g, "CheckReviewFixBudget", "ReviewParallel") {
		t.Error("CheckReviewFixBudget still routes directly to ReviewParallel; it must pass the existence gate and clear stale reviews first (issues #313/#350)")
	}
	if hasEdgeTo(g, "PickNextMilestone", "ClearStaleReviews") {
		t.Error("PickNextMilestone still routes directly to ClearStaleReviews; the #350 gate must run first")
	}
	if hasEdgeTo(g, "CheckReviewFixBudget", "ClearStaleReviews") {
		t.Error("CheckReviewFixBudget still routes directly to ClearStaleReviews; the #350 gate must run first")
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

// hasEdgeAttr reports whether the node has an outgoing edge to the given target
// matching both the condition and an edge attribute key=value (e.g. restart).
func hasEdgeAttr(g *Graph, from, to, cond, key, want string) bool {
	for _, e := range g.OutgoingEdges(from) {
		if e.To == to && e.Condition == cond && e.Attrs[key] == want {
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

// TestFixMilestoneRetriesInPlace pins #640 B3: FixMilestone carries no
// retry_target, so an engine-level OutcomeRetry (transient provider error,
// STATUS:retry, cost/no-progress guard) resolves to the node itself — the
// engine keeps the pipeline context and the working tree on that path — and
// never re-enters TestMilestone, whose red re-run would consume one of the
// three on-disk fix attempts without any fix work having happened.
func TestFixMilestoneRetriesInPlace(t *testing.T) {
	g := loadBuildProduct(t)
	n := g.Nodes["FixMilestone"]
	if n == nil {
		t.Fatal("FixMilestone node missing")
	}
	if rt := n.Attrs["retry_target"]; rt != "" {
		t.Fatalf("FixMilestone retry_target = %q; must be unset so retries re-run the node in place (#640 B3)", rt)
	}
	e := &Engine{graph: g}
	target, err := e.resolveRetryTarget(n, "FixMilestone")
	if err != nil || target != "FixMilestone" {
		t.Fatalf("resolveRetryTarget = %q, %v; want FixMilestone (in place)", target, err)
	}
}
