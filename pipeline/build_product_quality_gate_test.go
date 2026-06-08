// ABOUTME: Negative-control + regression guard for issue #299 — the language-native
// ABOUTME: quality-gate fallback in run_project_ci_gate (ci-probe.sh, Setup node).
package pipeline

import (
	"strings"
	"testing"
)

// setupCmd returns the Setup node's tool_command, where the ci-probe.sh heredoc lives.
func setupCmd(t *testing.T) string {
	t.Helper()
	g := loadBuildProduct(t)
	n, ok := g.Nodes["Setup"]
	if !ok {
		t.Fatal("Setup node missing from build_product graph")
	}
	return n.Attrs["tool_command"]
}

// Test 1 — negative-control: the Go core gate `go vet ./...` must appear in the
// probe. Absent from Setup pre-#299 (verified count=0) → RED before implementation.
func TestQualityGateGoVetPresent(t *testing.T) {
	if !strings.Contains(setupCmd(t), "go vet ./...") {
		t.Error("ci-probe.sh has no `go vet ./...` language-native gate (#299)")
	}
}

// Test 2 — negative-control: optional linters wired (run-if-present).
func TestQualityGateOptionalLintersPresent(t *testing.T) {
	cmd := setupCmd(t)
	for _, tool := range []string{
		"golangci-lint run",
		"tsc --noEmit",
		"eslint .",
		"ruff check .",
		"mypy .",
		"cargo fmt --check",
		"cargo clippy -- -D warnings",
	} {
		if !strings.Contains(cmd, tool) {
			t.Errorf("ci-probe.sh missing language-native gate %q (#299)", tool)
		}
	}
}

// Test 3 — negative-control: all four toolchain detectors present INSIDE the probe
// (they live in the test-runner elif elsewhere, NOT in Setup pre-#299; their presence
// in Setup's tool_command is what #299 adds).
func TestQualityGateDetectorsPresent(t *testing.T) {
	cmd := setupCmd(t)
	for _, det := range []string{"go.mod", "package.json", "pyproject.toml", "Cargo.toml"} {
		if !strings.Contains(cmd, det) {
			t.Errorf("ci-probe.sh missing toolchain detector %q (#299)", det)
		}
	}
}

// Test 4 — negative-control: optional tools are command-v guarded (absence → skip,
// never failure). Assert each optional tool is preceded by a `command -v` guard.
func TestQualityGateOptionalToolsGuarded(t *testing.T) {
	cmd := setupCmd(t)
	for _, tool := range []string{"golangci-lint", "tsc", "eslint", "ruff", "mypy", "cargo"} {
		if !strings.Contains(cmd, "command -v "+tool) {
			t.Errorf("optional tool %q is not `command -v`-guarded (#299)", tool)
		}
	}
}

// Test 5 — regression-pin: the Makefile path is preserved byte-for-byte. `make
// "$TARGET"` and the awk parser must survive (GREEN now and after; guards removal).
func TestQualityGateMakefilePathPreserved(t *testing.T) {
	cmd := setupCmd(t)
	if !strings.Contains(cmd, `make "$TARGET" 2>&1`) {
		t.Error("Makefile gate `make \"$TARGET\" 2>&1` was removed/altered (#299 regression)")
	}
	if !strings.Contains(cmd, "awk -v t=\"$TARGET\"") {
		t.Error("awk Makefile-target parser was altered (#299 must not touch it)")
	}
}

// Test 6 — regression-pin (semantic): rc=2 stays sole-sourced to the make-missing
// branch. Exactly one `return 2`, and it is adjacent to `command -v make` — no
// `return 2` may appear after any language-native gate marker.
func TestQualityGateRc2OnlyMakeMissing(t *testing.T) {
	cmd := setupCmd(t)
	if n := strings.Count(cmd, "return 2"); n != 1 {
		t.Fatalf("expected exactly one `return 2` (make-missing only), found %d (#299)", n)
	}
	makeIdx := strings.Index(cmd, "command -v make")
	ret2Idx := strings.Index(cmd, "return 2")
	if makeIdx == -1 || ret2Idx < makeIdx {
		t.Error("`return 2` is not anchored to the `command -v make` branch (#299 rc contract)")
	}
	// No return 2 within/after the language-native gate execution markers. Anchor
	// on the parenthesized gate marker `(language-native gate,` (the form emitted
	// by each run gate inside run_language_native_gates) — NOT the prose "running
	// language-native gates" references in run_project_ci_gate's INFO echoes, which
	// precede the make-missing `return 2` in text and would false-positive.
	if g := strings.Index(cmd, "(language-native gate,"); g != -1 {
		if strings.Contains(cmd[g:], "return 2") {
			t.Error("a `return 2` appears within/after the language-native gates — rc=2 must stay make-missing-only (#299)")
		}
	}
}

// Test 7 — regression-pin: the gate stays CENTRALIZED. Both callers must still
// source ci-probe.sh and call run_project_ci_gate (one helper, two callers — no
// duplicated gate logic in the caller bodies).
func TestQualityGateStaysCentralized(t *testing.T) {
	g := loadBuildProduct(t)
	for _, id := range []string{"TestMilestone", "FinalBuild"} {
		cmd := g.Nodes[id].Attrs["tool_command"]
		if !strings.Contains(cmd, ". .ai/build/ci-probe.sh") {
			t.Errorf("%s no longer sources ci-probe.sh (#299)", id)
		}
		if !strings.Contains(cmd, "run_project_ci_gate") {
			t.Errorf("%s no longer calls run_project_ci_gate (#299)", id)
		}
		// No duplicated gate: callers must not grow their own `go vet`.
		if strings.Contains(cmd, "go vet") {
			t.Errorf("%s grew its own `go vet` — gate must stay in the shared helper (#299)", id)
		}
	}
}

// Test 8 — prose negative-control: VerifyMilestone no longer claims the no-Makefile
// path is "correctly skipped". RED until the prose-sync edit (Task 3).
func TestQualityGateVerifyPromptTruthful(t *testing.T) {
	g := loadBuildProduct(t)
	p := g.Nodes["VerifyMilestone"].Attrs["prompt"]
	if strings.Contains(p, "correctly skipped (no Makefile") {
		t.Error("VerifyMilestone prompt still says the no-Makefile path is skipped — false after #299")
	}
}
