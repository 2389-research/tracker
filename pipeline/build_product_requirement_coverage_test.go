// ABOUTME: Negative-control + regression guard for issue #300 — no-requirement-left-behind:
// ABOUTME: Decompose coverage-table gate + owner-or-deferred "future work" rule in Verify/FinalSpecCheck.
package pipeline

import (
	"strings"
	"testing"
)

// promptOf returns the named node's prompt attr (where the agent-rule text lives).
func promptOf(t *testing.T, g *Graph, id string) string {
	t.Helper()
	n, ok := g.Nodes[id]
	if !ok {
		t.Fatalf("%s node missing from build_product graph (#300)", id)
	}
	return n.Attrs["prompt"]
}

// Test 1 — regression-pin AND the load-bearing #300 pin: Decompose must carry
// auto_status:true. Without it, resolveTerminalStatus (codergen.go:635) defaults
// the node to OutcomeSuccess and the `Decompose -> EscalateReview when fail` edge
// is DEAD — a dropped requirement can never fail the node. This single test ties
// the attr to the fail edge so a future removal of either is caught.
func TestDecomposeFailEdgeIsLive(t *testing.T) {
	g := loadBuildProduct(t)
	n, ok := g.Nodes["Decompose"]
	if !ok {
		t.Fatal("Decompose node missing (#300)")
	}
	if n.Attrs["auto_status"] != "true" {
		t.Error("Decompose lacks auto_status:true — its `outcome = fail` edge is DEAD (codergen.go:635); a dropped requirement cannot fail the node (#300)")
	}
	if !hasEdgeWithCondition(g, "Decompose", "EscalateReview", "ctx.outcome = fail") {
		t.Error("Decompose has no `ctx.outcome = fail` edge to EscalateReview (#300)")
	}
}

// Test 2 — negative-control: Decompose writes the coverage table and uses the
// coverage-mapping language. Distinctive new substrings (NOT the bare token
// "SPEC.md", which is already GREEN at Decompose :467 — "SPEC.md may define a
// Fingerprint" — and would prove nothing).
func TestDecomposeCoverageTablePrompt(t *testing.T) {
	p := promptOf(t, loadBuildProduct(t), "Decompose")
	for _, sub := range []string{
		"requirement-coverage.md",
		"Owning milestone",
		"mandated",
		"COVERAGE_GAPS",
	} {
		if !strings.Contains(p, sub) {
			t.Errorf("Decompose prompt missing coverage-gate substring %q (#300)", sub)
		}
	}
}

// Test 3 — negative-control: Decompose's coverage gate carves out documented
// deferrals via a 3-way classification (owned | deferred | UNOWNED) so it does
// not false-fail a correct decomposition that properly defers Phase-2+ work.
// Asserts tokens UNIQUE to the #300 block ("UNOWNED", "neither owned") — NOT the
// bare "DO NOT implement"/"deferred", which the pre-existing anti-scope-creep
// block already contains (would be a no-op assertion; test-reviewer I1).
func TestDecomposeDeferralCarveout(t *testing.T) {
	p := promptOf(t, loadBuildProduct(t), "Decompose")
	for _, sub := range []string{"UNOWNED", "neither owned"} {
		if !strings.Contains(p, sub) {
			t.Errorf("Decompose coverage gate missing 3-way-classification substring %q (#300)", sub)
		}
	}
}

// Test 4 — negative-control: VerifyMilestone check 5 gains the owner-or-deferred
// "future work" rule. Anchor tokens matched SEPARATELY (not a full sentence) so
// benign rewording doesn't flake.
func TestVerifyMilestoneOwnerOrFailRule(t *testing.T) {
	p := promptOf(t, loadBuildProduct(t), "VerifyMilestone")
	for _, sub := range []string{"future work", "milestones.md", "DO NOT implement"} {
		if !strings.Contains(p, sub) {
			t.Errorf("VerifyMilestone prompt missing owner-or-deferred substring %q (#300)", sub)
		}
	}
}

// Test 5 — negative-control: FinalSpecCheck gains the same owner-or-deferred rule.
func TestFinalSpecCheckOwnerOrFailRule(t *testing.T) {
	p := promptOf(t, loadBuildProduct(t), "FinalSpecCheck")
	for _, sub := range []string{"future work", "milestones.md", "DO NOT implement"} {
		if !strings.Contains(p, sub) {
			t.Errorf("FinalSpecCheck prompt missing owner-or-deferred substring %q (#300)", sub)
		}
	}
}

// Test 6 — negative-control (prose-sync truthfulness pin, mirrors #299's
// TestQualityGateVerifyPromptTruthful): VerifyMilestone's prompt acknowledges
// that Decompose now owns a coverage gate (defense-in-depth cross-reference).
func TestVerifyMilestoneMentionsCoverageGate(t *testing.T) {
	p := promptOf(t, loadBuildProduct(t), "VerifyMilestone")
	if !strings.Contains(p, "requirement-coverage.md") {
		t.Error("VerifyMilestone prompt does not cross-reference requirement-coverage.md — prose-sync drift (#300)")
	}
}

// Test 7 — regression-pin: Decompose's out-edges are EXACTLY the conditional
// success→ApprovePlan and fail→EscalateReview pair. No new edge, no unconditional
// fallback (which CLAUDE.md forbids near loop targets). Use OutgoingEdges (filters
// on e.From) — NOT a loose g.Edges scan, which the incoming restart edges
// ApprovePlan->Decompose and ResetReviewBudget->Decompose would pollute.
func TestDecomposeOutEdgesUnchanged(t *testing.T) {
	g := loadBuildProduct(t)
	out := g.OutgoingEdges("Decompose")
	if len(out) != 2 {
		t.Fatalf("Decompose has %d out-edges, want exactly 2 (#300)", len(out))
	}
	if !hasEdgeWithCondition(g, "Decompose", "ApprovePlan", "ctx.outcome = success") {
		t.Error("Decompose lost its success→ApprovePlan edge (#300)")
	}
	if !hasEdgeWithCondition(g, "Decompose", "EscalateReview", "ctx.outcome = fail") {
		t.Error("Decompose lost its fail→EscalateReview edge (#300)")
	}
	for _, e := range out {
		if e.Condition == "" {
			t.Errorf("Decompose grew an unconditional out-edge → %s (forbidden near loop targets, #300)", e.To)
		}
	}
}

// Test 8 — regression-pin: the downstream gates keep auto_status:true (their
// STATUS:fail is what enforces the owner-or-deferred rule).
func TestVerifyAndFinalKeepAutoStatus(t *testing.T) {
	g := loadBuildProduct(t)
	for _, id := range []string{"VerifyMilestone", "FinalSpecCheck"} {
		if g.Nodes[id].Attrs["auto_status"] != "true" {
			t.Errorf("%s lost auto_status:true — its STATUS:fail gate is dead (#300)", id)
		}
	}
}
