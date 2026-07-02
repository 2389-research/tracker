// ABOUTME: Regression guards for the build_product gate-loop fixes batch
// ABOUTME: (issues #436–#443) from the code-goblin run 400eebf3f3c7 post-mortem.
package pipeline

import (
	"os/exec"
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

// TestBuildProductIssue437FixMilestoneSeesGateOutput pins #437: FixMilestone
// must be handed the failing gate's real stdout (not the prior node's DONE
// narrative) and told to re-run the exact gate, not just `go test`.
func TestBuildProductIssue437FixMilestoneSeesGateOutput(t *testing.T) {
	g := loadBuildProduct(t)
	p := nodePrompt(t, g, "FixMilestone")
	if !strings.Contains(p, "## Failing gate output") {
		t.Error("FixMilestone prompt must include a `## Failing gate output` heading (issue #437)")
	}
	if !strings.Contains(p, "${ctx.tool_stdout}") {
		t.Error("FixMilestone prompt must interpolate ${ctx.tool_stdout} so the fixer sees the real failure (issue #437)")
	}
	if !strings.Contains(p, "sh .ai/build/verify.sh") {
		t.Error("FixMilestone prompt must instruct re-running `sh .ai/build/verify.sh`, not just go test (issue #437)")
	}
}

// TestBuildProductIssue440FirstBacktickParser pins #440: declared-file
// extraction must take the first backticked path per bullet, not whitespace-
// tokenize prose into phantom paths (go.sum, git.Fake, gh.Fake).
func TestBuildProductIssue440FirstBacktickParser(t *testing.T) {
	cmd := toolCmd(t, "CheckMilestoneOutputs")
	if !strings.Contains(cmd, `sed -E 's/[(#].*$//'`) {
		t.Error("CheckMilestoneOutputs must strip inline prose after `(`/`#` in the path parser (issue #440)")
	}
	if strings.Contains(cmd, "tr -s ',[:space:]'") {
		t.Error("CheckMilestoneOutputs still whitespace-tokenizes prose into phantom paths (issue #440 regression)")
	}
}

// TestBuildProductIssue440ParserTakesFirstBacktickPath is a BEHAVIORAL guard:
// the declared-file parser must capture the FIRST backticked token so a bullet
// that also backticks a type name (e.g. "`Client` struct") doesn't lose the
// real path. A string-only guard missed the original greedy-`.*` regression.
func TestBuildProductIssue440ParserTakesFirstBacktickPath(t *testing.T) {
	cmd := toolCmd(t, "CheckMilestoneOutputs")
	if !strings.Contains(cmd, "s/^[^") {
		t.Error("CheckMilestoneOutputs backtick parser must anchor with ^[^`]* to take the FIRST backticked token, not a greedy .* that takes the last (issue #440)")
	}
	// Prove the anchored sed the .dip uses picks the FIRST path when a bullet
	// contains two backtick pairs (path + a backticked type name).
	sed := "sed -n 's/^[^`]*`\\([^`]*\\)`.*/\\1/p'"
	c := exec.Command("sh", "-c", sed)
	c.Stdin = strings.NewReader("- `internal/openai/client.go` (create `Client` struct)\n")
	out, err := c.Output()
	if err != nil {
		t.Fatalf("running the parser sed failed: %v", err)
	}
	if got := strings.TrimSpace(string(out)); got != "internal/openai/client.go" {
		t.Errorf("first-backtick parser got %q, want internal/openai/client.go (issue #440 greedy-match regression)", got)
	}
}

// TestBuildProductIssue439OutputsScopedToBuiltMilestones pins #439: the outputs
// gate runs on the early-`accept` ship path where later milestones aren't built
// yet, so it must scope its declared-file manifest to completed milestones
// (via the .ai/milestones/done count), not validate the whole plan.
func TestBuildProductIssue439OutputsScopedToBuiltMilestones(t *testing.T) {
	cmd := toolCmd(t, "CheckMilestoneOutputs")
	if !strings.Contains(cmd, ".ai/milestones/done") {
		t.Error("CheckMilestoneOutputs must count completed milestones from .ai/milestones/done to scope the manifest (issue #439)")
	}
	if !strings.Contains(cmd, "SCOPED_PLAN") {
		t.Error("CheckMilestoneOutputs must extract Files from a milestone-scoped plan slice, not the whole milestones.md (issue #439)")
	}
}

// TestBuildProductIssue436LintMilestoneScoped pins #436: the lint gate must be
// milestone-scoped like `go test`, via --new-from-rev fed from the milestone
// base, while FinalBuild (which leaves the env var unset) still lints whole-tree.
func TestBuildProductIssue436LintMilestoneScoped(t *testing.T) {
	setup := toolCmd(t, "Setup")
	probe := extractHeredoc(t, setup, ".ai/build/ci-probe.sh", "PROBE_EOF")
	if !strings.Contains(probe, `--new-from-rev "$LINT_NEW_FROM_REV"`) {
		t.Error("ci-probe.sh golangci-lint must honor $LINT_NEW_FROM_REV via --new-from-rev (issue #436)")
	}
	if !strings.Contains(probe, `${LINT_NEW_FROM_REV:+`) {
		t.Error("ci-probe.sh must only pass --new-from-rev when LINT_NEW_FROM_REV is set (whole-tree at FinalBuild) (issue #436)")
	}
	verify := extractHeredoc(t, setup, ".ai/build/verify.sh", "VERIFY_EOF")
	if !strings.Contains(verify, "LINT_NEW_FROM_REV=") {
		t.Error("verify.sh must set LINT_NEW_FROM_REV from the milestone base so lint is scoped like go test (issue #436)")
	}
}
