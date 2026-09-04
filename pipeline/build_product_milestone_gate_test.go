// ABOUTME: Regression guard for issue #392 — build_product.dip's per-milestone
// ABOUTME: test gate is milestone-scoped and `accept` still earns verification.
package pipeline

import (
	"strings"
	"testing"
)

// hasLabeledEdge reports whether `from` has an outgoing human-gate edge to `to`
// with the given label. Labeled gate edges store the choice in Edge.Label (or
// Edge.Choice for DIP150) — check both.
func hasLabeledEdge(g *Graph, from, to, label string) bool {
	for _, e := range g.OutgoingEdges(from) {
		if e.To == to && (e.Label == label || e.Choice == label) {
			return true
		}
	}
	return false
}

// labeledEdgeAttr returns the value of attr key on the labeled gate edge
// from->to, or "" if no such edge/attr exists.
func labeledEdgeAttr(g *Graph, from, to, label, key string) string {
	for _, e := range g.OutgoingEdges(from) {
		if e.To == to && (e.Label == label || e.Choice == label) {
			return e.Attrs[key]
		}
	}
	return ""
}

// TestBuildProductIssue392AcceptDoesNotBypassVerification pins T2 of #392 as
// amended by #730: the EscalateMilestone "accept" option must route FORWARD into
// the ship/review path (ClearStaleReviews) — NOT straight to Cleanup (which
// would skip verification, the pre-#392 bug) and NOT back into
// CheckMilestoneOutputs (the #730 trap: accept re-enters the very structural
// check the operator is overriding -> outputs-missing -> EscalateMilestone, an
// inescapable loop where only "abandon" exits). Routing to ClearStaleReviews
// still earns the three-way cross-review + FinalBuild (whole-tree test) +
// FinalSpecCheck subgraph, so "stop building more milestones" ships verified
// work AND always has a real forward exit.
func TestBuildProductIssue392AcceptDoesNotBypassVerification(t *testing.T) {
	g := loadBuildProduct(t)

	if _, ok := g.Nodes["EscalateMilestone"]; !ok {
		t.Fatal("EscalateMilestone node missing from build_product.dip (issue #392)")
	}
	if !hasLabeledEdge(g, "EscalateMilestone", "ClearStaleReviews", "accept") {
		t.Error("EscalateMilestone `accept` must route forward to ClearStaleReviews so it still earns cross-review + final build + spec check while escaping the CheckMilestoneOutputs loop (issues #392, #730)")
	}
	if hasLabeledEdge(g, "EscalateMilestone", "Cleanup", "accept") {
		t.Error("EscalateMilestone `accept` still routes directly to Cleanup — that bypasses all verification (issue #392 regression)")
	}
	if hasLabeledEdge(g, "EscalateMilestone", "CheckMilestoneOutputs", "accept") {
		t.Error("EscalateMilestone `accept` still routes back into CheckMilestoneOutputs — accept can never override that gate, so a flagged tree loops forever (issue #730 regression)")
	}
	// accept is a forward transition out of the per-milestone loop into the
	// review phase; the edge carries restart:true to reset the restart budget
	// (mirrors MarkMilestoneDone -> PickNextMilestone).
	if got := labeledEdgeAttr(g, "EscalateMilestone", "ClearStaleReviews", "accept", "restart"); got != "true" {
		t.Errorf("EscalateMilestone accept -> ClearStaleReviews restart = %q, want \"true\" (issue #730)", got)
	}
}

// TestBuildProductIssue392MilestoneScopedTestGate pins T1 of #392: the
// per-milestone TestMilestone gate must run a milestone-scoped `go test`
// target, not an unconditional whole-tree `go test ./...` that the
// milestone-scoped fix loop (Implement/FixMilestone) cannot satisfy when a
// later milestone's pre-seeded package is red. The whole-tree suite still runs
// at FinalBuild before anything ships.
//
// This is a string-level guard. As of #406 the gate logic lives in the shared
// .ai/build/verify.sh script (written by Setup, delegated to by TestMilestone
// and the Implement/FixMilestone breach verify_command) — a single source of
// truth — so the guard reads that extracted script, not TestMilestone's thin
// wrapper.
func TestBuildProductIssue392MilestoneScopedTestGate(t *testing.T) {
	cmd := extractHeredoc(t, toolCmd(t, "Setup"), ".ai/build/verify.sh", "VERIFY_EOF")
	for _, marker := range []string{"milestone-start-sha", "GO_TEST_TARGET"} {
		if !strings.Contains(cmd, marker) {
			t.Errorf("TestMilestone command no longer references %q — the milestone-scoped go test gate may have reverted to whole-tree (issue #392)", marker)
		}
	}
	if !strings.Contains(cmd, "go test $GO_TEST_TARGET") {
		t.Error("TestMilestone must run `go test $GO_TEST_TARGET` (milestone-scoped), not a bare whole-tree `go test ./...` (issue #392)")
	}
}
