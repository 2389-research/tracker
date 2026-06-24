// ABOUTME: Phase-0 RED guards for #406 — the #303/#297 turn-limit commit-if-green
// ABOUTME: guard must apply to the FixMilestone fix-loop node, not just Implement.
package pipeline

import "testing"

// Case study run 634a2527ff56: FixMilestone repaired every VerifyMilestone
// finding and reached a fully green tree at turn 47, then burned turns 48–50 on
// read-only `git status` and hit the 50-turn limit. The breach classified as
// `operator_decision` (NOT `verified_green`) because FixMilestone carries no
// `verify_command`, so the breach verifier never ran — and FixMilestone's only
// edge (`-> TestMilestone restart: true`, plus the EscalateMilestone fallback)
// has no commit-if-green path. The green fix was abandoned uncommitted; HEAD
// stayed at the broken pre-fix state. These tests pin the fix.

// TestBuildProductFixMilestoneCarriesVerifyCommand pins #406 AC2: "the breach
// classifier records verify: PASS when the milestone verify command succeeds at
// breach time." That can only happen if the fix-loop node actually carries a
// verify_command for resolveBreachVerifier to run — without one, every breach
// degrades to operator_decision. The guard is meant for "every agent node that
// can mutate the tree," so the original Implement node must carry it too (its
// existing commit-on-success breach path is dead without a verifier).
func TestBuildProductFixMilestoneCarriesVerifyCommand(t *testing.T) {
	g := loadBuildProduct(t)

	for _, id := range []string{"FixMilestone", "Implement"} {
		n, ok := g.Nodes[id]
		if !ok {
			t.Fatalf("%s node missing from build_product.dip", id)
		}
		if vc := n.AgentConfig(g.Attrs).VerifyCommand; vc == "" {
			t.Errorf("%s resolves empty verify_command — a turn breach can never classify verified_green, so a green tree is adjudicated as a failure (issue #406 AC2)", id)
		}
	}
}

// TestBuildProductFixMilestoneGreenBreachCommits pins #406 AC1: "a FixMilestone
// node that exhausts its turn budget with a green verify command results in a
// committed tree (durable ref), not a dirty working tree." A verified_green
// breach sets ctx.turn_breach_class = verified_green (and outcome = success); a
// narrow conditional edge keyed on that class must route to the commit-on-success
// node (CommitIfDirty — the same durable-persistence mechanism used for the
// #318 OperatorDecision commit_advance path), which then continues the loop.
func TestBuildProductFixMilestoneGreenBreachCommits(t *testing.T) {
	g := loadBuildProduct(t)

	if !hasEdgeWithCondition(g, "FixMilestone", "CommitIfDirty", "ctx.turn_breach_class = verified_green") {
		t.Error("FixMilestone has no `ctx.turn_breach_class = verified_green` edge to CommitIfDirty — a green-at-breach fix is left uncommitted and abandoned (issue #406 AC1)")
	}
	// commit_advance/green-breach must continue the milestone, not dead-end.
	if !hasEdgeTo(g, "CommitIfDirty", "TestMilestone") {
		t.Error("CommitIfDirty must continue to TestMilestone so a committed green-breach fix re-verifies and advances (issue #406)")
	}
}

// TestBuildProductFixMilestoneNormalLoopPreserved is a regression guard: the
// narrow verified_green edge must NOT disturb FixMilestone's ordinary fix→retest
// loop. A normal success (agent committed + DONE, no breach) carries no
// turn_breach_class, so it must still fall through to the warm TestMilestone
// restart edge.
func TestBuildProductFixMilestoneNormalLoopPreserved(t *testing.T) {
	g := loadBuildProduct(t)

	if !hasEdgeAttr(g, "FixMilestone", "TestMilestone", "", "restart", "true") {
		t.Error("FixMilestone lost its unconditional `-> TestMilestone restart: true` loop edge — normal (non-breach) fixes would no longer re-verify (issue #406 regression)")
	}
}

// TestBuildProductFixMilestonePathologicalStillEscalates is a regression guard:
// a pathological / non-green breach (turn_breach_class != verified_green) must
// still reach EscalateMilestone via the resolved fallback_target, so a looping
// fix agent still stops rather than committing a non-green tree (issue #296/#303).
func TestBuildProductFixMilestonePathologicalStillEscalates(t *testing.T) {
	g := loadBuildProduct(t)

	if got := resolveFallback(g, g.Nodes["FixMilestone"]); got != "EscalateMilestone" {
		t.Errorf("FixMilestone resolved fallback = %q, want EscalateMilestone — a non-green breach must still escalate (issue #296/#303)", got)
	}
}
