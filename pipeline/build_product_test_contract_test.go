// ABOUTME: #409 guards for the build_product dogfood cascade (run 634a2527ff56):
// ABOUTME: Implement and VerifyMilestone share one test-verifies-contract rubric.
package pipeline

import (
	"strings"
	"testing"
)

// TestBuildProductImplementSharesTestContractRubric pins #409 AC2: Implement and
// VerifyMilestone must reference one shared test-verifies-contract rubric. The
// named anchor TEST-VERIFIES-CONTRACT already heads VerifyMilestone's check 4;
// Implement must cite the same rubric so it is graded against the same standard.
func TestBuildProductImplementSharesTestContractRubric(t *testing.T) {
	g := loadBuildProduct(t)
	if !strings.Contains(nodePrompt(t, g, "VerifyMilestone"), "TEST-VERIFIES-CONTRACT") {
		t.Fatal("VerifyMilestone lost its TEST-VERIFIES-CONTRACT rubric anchor (issue #409 precondition)")
	}
	if !strings.Contains(nodePrompt(t, g, "Implement"), "TEST-VERIFIES-CONTRACT") {
		t.Error("Implement does not reference the shared TEST-VERIFIES-CONTRACT rubric — it is graded against a weaker standard than VerifyMilestone, guaranteeing a Verify FAIL + fix loop (issue #409 AC2)")
	}
}

// TestBuildProductTestShapeContractEnforced pins #409 AC1: when a milestone's
// done-when names a concrete test SHAPE ("built binary" smoke test, subprocess,
// real `gh`), both Implement and VerifyMilestone must enforce that the test
// actually uses that harness — not a weaker in-process call. In run
// 634a2527ff56 Implement shipped a smoke test that called `runWithSignal()`
// in-process instead of spawning the built binary; it passed Implement's
// done-when but FAILed Verify, forcing a costly fix loop to invent the whole
// subprocess injection seam.
func TestBuildProductTestShapeContractEnforced(t *testing.T) {
	g := loadBuildProduct(t)
	for _, id := range []string{"Implement", "VerifyMilestone"} {
		if !strings.Contains(strings.ToLower(nodePrompt(t, g, id)), "built binary") {
			t.Errorf("%s does not enforce the test-shape contract (a \"built binary\" smoke test must spawn the binary, not call it in-process) — Implement and Verify diverge on the test harness standard (issue #409 AC1)", id)
		}
	}
}
