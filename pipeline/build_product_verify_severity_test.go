// ABOUTME: #408 guard for the build_product dogfood cascade (run 634a2527ff56):
// ABOUTME: VerifyMilestone needs an ADR-aware WARN severity tier.
package pipeline

import (
	"strings"
	"testing"
)

// TestBuildProductVerifyMilestoneHasWarnTier pins #408: VerifyMilestone's
// findings must carry a severity tier with a WARN level, so a behaviorally-
// equivalent deviation that an `.ai/decisions/` ADR documents (and whose tests
// pass) downgrades from FAIL to WARN instead of triggering an expensive fix
// loop. In run 634a2527ff56 a `signal.Notify`-vs-`signal.NotifyContext` prose
// identifier mismatch hard-FAILed behaviorally-correct, fully-tested code.
func TestBuildProductVerifyMilestoneHasWarnTier(t *testing.T) {
	p := nodePrompt(t, loadBuildProduct(t), "VerifyMilestone")
	if !strings.Contains(p, "WARN") {
		t.Error("VerifyMilestone has no WARN severity tier — an ADR-documented, behaviorally-verified deviation from a prose-named identifier hard-FAILs instead of warning (issue #408)")
	}
}
