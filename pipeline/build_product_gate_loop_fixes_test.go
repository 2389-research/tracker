// ABOUTME: Regression guards for the build_product gate-loop fixes batch
// ABOUTME: (issues #436–#443) from the code-goblin run 400eebf3f3c7 post-mortem.
package pipeline

import (
	"strings"
	"testing"
)

// TestBuildProductIssue443AttemptCapBoundary pins #443: a cap of 3 must run
// exactly 3 attempts. The pre-fix `-gt 3` ran a 4th attempt and logged
// "attempt 4 of 3".
func TestBuildProductIssue443AttemptCapBoundary(t *testing.T) {
	cmd := toolCmd(t, "TestMilestone")
	if !strings.Contains(cmd, `[ "$ATTEMPTS" -ge 3 ]`) {
		t.Error("TestMilestone must escalate at `-ge 3` so a cap of 3 runs exactly 3 attempts (issue #443)")
	}
	if strings.Contains(cmd, `[ "$ATTEMPTS" -gt 3 ]`) {
		t.Error("TestMilestone still uses `-gt 3` — runs a 4th attempt before escalating (issue #443 regression)")
	}
}
