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

// TestBuildProductIssue392AcceptDoesNotBypassVerification pins T2 of #392: the
// EscalateMilestone "accept" option must route into CheckMilestoneOutputs — the
// SAME entry the normal all-done path uses — NOT straight to Cleanup. The
// pre-#392 edge (accept -> Cleanup -> FinalCommit -> Done) let a run exit Done
// over a red suite, skipping the cross-review + FinalBuild (whole-tree test) +
// FinalSpecCheck subgraph entirely. Routing through CheckMilestoneOutputs makes
// "stop building more milestones" still ship verified work.
func TestBuildProductIssue392AcceptDoesNotBypassVerification(t *testing.T) {
	g := loadBuildProduct(t)

	if _, ok := g.Nodes["EscalateMilestone"]; !ok {
		t.Fatal("EscalateMilestone node missing from build_product.dip (issue #392)")
	}
	if !hasLabeledEdge(g, "EscalateMilestone", "CheckMilestoneOutputs", "accept") {
		t.Error("EscalateMilestone `accept` must route to CheckMilestoneOutputs so it still earns cross-review + final build + spec check (issue #392)")
	}
	if hasLabeledEdge(g, "EscalateMilestone", "Cleanup", "accept") {
		t.Error("EscalateMilestone `accept` still routes directly to Cleanup — that bypasses all verification (issue #392 regression)")
	}
	// accept re-enters CheckMilestoneOutputs, whose own fallback loops back to
	// EscalateMilestone; the edge must carry restart:true to break the DIP005
	// cycle (same mechanism as the CheckReviewFixBudget re-review edge).
	if got := labeledEdgeAttr(g, "EscalateMilestone", "CheckMilestoneOutputs", "accept", "restart"); got != "true" {
		t.Errorf("EscalateMilestone accept -> CheckMilestoneOutputs restart = %q, want \"true\" (DIP005 cycle break, issue #392)", got)
	}
}

// TestBuildProductIssue392MilestoneScopedTestGate pins T1 of #392: the
// per-milestone TestMilestone gate must run a milestone-scoped `go test`
// target, not an unconditional whole-tree `go test ./...` that the
// milestone-scoped fix loop (Implement/FixMilestone) cannot satisfy when a
// later milestone's pre-seeded package is red. The whole-tree suite still runs
// at FinalBuild before anything ships.
//
// This is a string-level guard because the gate logic lives inline in the .dip
// tool command; issue #398 tracks extracting it to a bats-testable script file,
// at which point this becomes a real script test.
func TestBuildProductIssue392MilestoneScopedTestGate(t *testing.T) {
	g := loadBuildProduct(t)
	n, ok := g.Nodes["TestMilestone"]
	if !ok {
		t.Fatal("TestMilestone node missing from build_product.dip (issue #392)")
	}
	cmd := n.ToolConfig().Command
	for _, marker := range []string{"milestone-start-sha", "GO_TEST_TARGET"} {
		if !strings.Contains(cmd, marker) {
			t.Errorf("TestMilestone command no longer references %q — the milestone-scoped go test gate may have reverted to whole-tree (issue #392)", marker)
		}
	}
	if !strings.Contains(cmd, "go test $GO_TEST_TARGET") {
		t.Error("TestMilestone must run `go test $GO_TEST_TARGET` (milestone-scoped), not a bare whole-tree `go test ./...` (issue #392)")
	}
}
