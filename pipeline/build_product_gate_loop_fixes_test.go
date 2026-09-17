// ABOUTME: Regression guards for the build_product gate-loop fixes batch
// ABOUTME: (issues #436–#443) from the code-goblin run 400eebf3f3c7 post-mortem.
package pipeline

import (
	"os/exec"
	"path/filepath"
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

// TestBuildProductIssue440FirstBacktickParser pins #440 (as re-homed by
// #640 E6): declared-file extraction lives in ONE shared parser —
// lib/milestones.sh parse_files_block — that CheckMilestoneOutputs sources,
// takes backticked spans as paths, drops `(...)` annotations and `#` comments
// from plain items, and never whitespace-tokenizes prose into phantom paths.
func TestBuildProductIssue440FirstBacktickParser(t *testing.T) {
	cmd := toolCmd(t, "CheckMilestoneOutputs")
	if !strings.Contains(cmd, `. "$LIB/milestones.sh"`) || !strings.Contains(cmd, "parse_files_block") {
		t.Error("CheckMilestoneOutputs must parse **Files** via lib/milestones.sh parse_files_block (issues #440/#640)")
	}
	if strings.Contains(cmd, "tr -s ',[:space:]'") {
		t.Error("CheckMilestoneOutputs still whitespace-tokenizes prose into phantom paths (issue #440 regression)")
	}
	lib := buildProductLib(t, "milestones.sh")
	for _, must := range []string{"parse_files_block()", `gsub(/\([^)]*\)/, " ", s)`, `sub(/#.*$/, "", s)`} {
		if !strings.Contains(lib, must) {
			t.Errorf("lib/milestones.sh parse_files_block lost %q — inline prose after `(`/`#` would become phantom paths (issue #440)", must)
		}
	}
}

// TestBuildProductIssue440ParserTakesFirstBacktickPath is a BEHAVIORAL guard:
// the shared parser must capture a bullet's backticked PATH and not a
// backticked type name that merely looks like prose ("`Client` struct"), and
// must not lose the path when the type name comes second. (Pre-#640 the
// parser took the first backticked token only; #640 E6 takes every
// backticked span, so the type name is emitted too and dropped later by the
// caller's path heuristic — it has no `/` and no `.`.)
func TestBuildProductIssue440ParserTakesFirstBacktickPath(t *testing.T) {
	shPath, err := exec.LookPath("sh")
	if err != nil {
		t.Skip("sh not on PATH")
	}
	lib := filepath.Join("..", "examples", "scripts", "build_product", "lib", "milestones.sh")
	c := exec.Command(shPath, "-c", ". "+lib+" && parse_files_block")
	c.Stdin = strings.NewReader("**Files**:\n- `internal/openai/client.go` (create `Client` struct)\n- **Done when**: `go build ./...` passes\n")
	out, err := c.Output()
	if err != nil {
		t.Fatalf("running parse_files_block failed: %v", err)
	}
	got := strings.Split(strings.TrimSpace(string(out)), "\n")
	if len(got) == 0 || got[0] != "internal/openai/client.go" {
		t.Errorf("parse_files_block got %q, want internal/openai/client.go first (issue #440 greedy-match regression)", got)
	}
	for _, g := range got {
		if g == "go" || g == "./..." || g == "build" {
			t.Errorf("parse_files_block leaked sibling-field token %q (issue #640 E6c)", g)
		}
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
	probe := buildProductLib(t, "ci-probe.sh")
	if !strings.Contains(probe, `--new-from-rev "$LINT_NEW_FROM_REV"`) {
		t.Error("ci-probe.sh golangci-lint must honor $LINT_NEW_FROM_REV via --new-from-rev (issue #436)")
	}
	if !strings.Contains(probe, `${LINT_NEW_FROM_REV:+`) {
		t.Error("ci-probe.sh must only pass --new-from-rev when LINT_NEW_FROM_REV is set (whole-tree at FinalBuild) (issue #436)")
	}
	verify := buildProductLib(t, "verify.sh")
	if !strings.Contains(verify, "LINT_NEW_FROM_REV=") {
		t.Error("verify.sh must set LINT_NEW_FROM_REV from the milestone base so lint is scoped like go test (issue #436)")
	}
}

// TestBuildProductIssue441LintSuppressionHatch pins #441: an operator must be
// able to suppress a LINT failure (not just go-test failures) via a named file,
// and EscalateMilestone must tell them the exact files to edit.
func TestBuildProductIssue441LintSuppressionHatch(t *testing.T) {
	probe := buildProductLib(t, "ci-probe.sh")
	if !strings.Contains(probe, "known_lint_failures") {
		t.Error("ci-probe.sh must read .ai/milestones/known_lint_failures (issue #441)")
	}
	if !strings.Contains(probe, "--exclude") {
		t.Error("ci-probe.sh must feed known_lint_failures into golangci-lint --exclude (issue #441)")
	}
	if !strings.Contains(probe, `read -r pat || [ -n "$pat" ]`) {
		t.Error("known_lint_failures loop must handle a final line without a trailing newline (|| [ -n \"$pat\" ]) so a newline-less suppression file isn't silently dropped (issue #441)")
	}
	g := loadBuildProduct(t)
	esc := nodePrompt(t, g, "EscalateMilestone")
	if !strings.Contains(esc, "known_lint_failures") || !strings.Contains(esc, "known_failures") {
		t.Error("EscalateMilestone prompt must name both suppression files to edit (issue #441)")
	}
}

// TestBuildProductIssue442LintSkipIsLoud pins #442: an absent golangci-lint must
// WARN that enforcement is disabled, not silently skip as "optional".
func TestBuildProductIssue442LintSkipIsLoud(t *testing.T) {
	probe := buildProductLib(t, "ci-probe.sh")
	if !strings.Contains(probe, "WARNING: golangci-lint not installed") {
		t.Error("ci-probe.sh must WARN loudly when golangci-lint is absent (issue #442)")
	}
	if strings.Contains(probe, "golangci-lint not installed — skipping (optional)") {
		t.Error("ci-probe.sh still prints the quiet `skipping (optional)` INFO for golangci-lint (issue #442 regression)")
	}
}
