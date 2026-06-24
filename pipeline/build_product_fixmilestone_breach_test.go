// ABOUTME: Phase-0 RED guards for #406 — the #303/#297 turn-limit commit-if-green
// ABOUTME: guard must apply to the FixMilestone fix-loop node, not just Implement.
package pipeline

import (
	"strings"
	"testing"
)

// hasEdgeConditionContaining reports whether the node has an outgoing edge to
// the target whose condition contains the given substring (tolerant of compound
// `&&`/`||` conditions and dippin's whitespace normalization).
func hasEdgeConditionContaining(g *Graph, from, to, sub string) bool {
	for _, e := range g.OutgoingEdges(from) {
		if e.To == to && strings.Contains(e.Condition, sub) {
			return true
		}
	}
	return false
}

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

	if !hasEdgeConditionContaining(g, "FixMilestone", "CommitIfDirty", "ctx.turn_breach_class = verified_green") {
		t.Error("FixMilestone has no `ctx.turn_breach_class = verified_green` edge to CommitIfDirty — a green-at-breach fix is left uncommitted and abandoned (issue #406 AC1)")
	}
	// PR #411 finding #3 (Codex P1): turn_breach_class is STICKY in context, so a
	// verified_green value left by an EARLIER milestone's breach could route a
	// later NORMAL fail (outcome=fail) down the commit edge and checkpoint a
	// non-green tree. A compound `verified_green AND outcome=success` guard is
	// unavailable (dippin rejects `&&`; tracker's evaluator can't parse `and`), so
	// the fix exploits first-match edge ordering: a `when ctx.outcome = fail ->
	// TestMilestone` edge declared BEFORE the verified_green commit edge
	// short-circuits every fail back to re-verify. Assert both the edge exists and
	// that it precedes the commit edge.
	if !hasEdgeWithCondition(g, "FixMilestone", "TestMilestone", "ctx.outcome = fail") {
		t.Error("FixMilestone has no `ctx.outcome = fail -> TestMilestone` short-circuit edge — a stale verified_green class could commit a non-green tree on a normal fail (PR #411 finding #3)")
	}
	failIdx, commitIdx := -1, -1
	for i, e := range g.OutgoingEdges("FixMilestone") {
		if e.To == "TestMilestone" && e.Condition == "ctx.outcome = fail" && failIdx == -1 {
			failIdx = i
		}
		if e.To == "CommitIfDirty" && strings.Contains(e.Condition, "verified_green") {
			commitIdx = i
		}
	}
	if failIdx == -1 || commitIdx == -1 || failIdx > commitIdx {
		t.Errorf("the `ctx.outcome = fail -> TestMilestone` short-circuit must be declared BEFORE the verified_green commit edge (first-match ordering is the guard) — failIdx=%d commitIdx=%d (PR #411 finding #3)", failIdx, commitIdx)
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
// still reach EscalateMilestone (issue #296/#303). NOTE the routing layer is
// subtle here: adding the verified_green conditional edge makes FixMilestone
// `hasAnyConditionalEdge`, which DISABLES engine strict-fail on this node — so a
// non-green breach no longer escalates via the fallback_target attr. It instead
// falls through the unconditional `-> TestMilestone restart: true` edge, and
// TestMilestone re-runs verify.sh, increments the on-disk fix-attempt counter,
// and escalates to EscalateMilestone once the counter is exhausted. This test
// pins that actual bounded route, not merely the (now-dead-for-this-case)
// fallback_target attribute.
func TestBuildProductFixMilestonePathologicalStillEscalates(t *testing.T) {
	g := loadBuildProduct(t)

	// The verified_green edge is what disables strict-fail; if it were ever
	// removed, the assertions below would mis-describe the routing.
	if !hasEdgeConditionContaining(g, "FixMilestone", "CommitIfDirty", "ctx.turn_breach_class = verified_green") {
		t.Fatal("FixMilestone lost its verified_green conditional edge — strict-fail analysis below no longer applies (issue #406)")
	}

	// A non-green breach (outcome=fail, turn_breach_class != verified_green)
	// has no matching conditional edge, so it falls through the unconditional
	// TestMilestone loop edge rather than escalating via fallback_target.
	if !hasEdgeAttr(g, "FixMilestone", "TestMilestone", "", "restart", "true") {
		t.Error("FixMilestone lost its unconditional `-> TestMilestone restart: true` fall-through — a non-green breach would have no bounded escalation path (issue #296/#303)")
	}

	// TestMilestone is where the bounded, counter-driven escalation actually
	// fires once the on-disk fix-attempt counter is exhausted.
	if !hasEdgeWithCondition(g, "TestMilestone", "EscalateMilestone", "ctx.tool_stdout contains escalate") {
		t.Error("TestMilestone has no counter-driven `-> EscalateMilestone` edge — a non-green breach looping through FixMilestone would never stop (issue #296/#303)")
	}
}
