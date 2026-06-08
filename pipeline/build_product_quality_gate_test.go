// ABOUTME: Negative-control + regression guard for issue #299 — the language-native
// ABOUTME: quality-gate fallback in run_project_ci_gate (ci-probe.sh, Setup node).
package pipeline

import (
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
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

// Test 6 — regression-pin (semantic): the only EXPLICIT `return 2` statement is
// the make-missing branch. Exactly one `return 2`, adjacent to `command -v make`,
// and none after any language-native gate marker. (This pins the source code, not
// runtime rc uniqueness: the `make "$TARGET"` path can still propagate make's own
// native exit 2 on a recipe error — pre-existing, tracked in #320.)
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

// extractProbe pulls the ci-probe.sh body (between `<<'PROBE_EOF'` and the closing
// `PROBE_EOF`) out of the LOADED (dippin-dedented) Setup tool_command — the exact
// bytes tracker writes to disk at runtime.
func extractProbe(t *testing.T) string {
	t.Helper()
	cmd := setupCmd(t)
	const open = "<<'PROBE_EOF'\n"
	i := strings.Index(cmd, open)
	if i == -1 {
		t.Fatal("could not find ci-probe.sh heredoc open in Setup command")
	}
	rest := cmd[i+len(open):]
	j := strings.Index(rest, "PROBE_EOF")
	if j == -1 {
		t.Fatal("could not find ci-probe.sh heredoc close")
	}
	return rest[:j]
}

// hermeticEnv builds an env whose PATH is a freshly-created temp bin containing
// ONLY symlinks to the tools the gate legitimately needs: go, sh, the coreutils
// the Makefile-parse path (`sed | awk`) and the eslint-config check (`grep`) use,
// and make when the case requires it. Optional linters (golangci-lint/tsc/eslint/
// ruff/mypy/cargo) are therefore GUARANTEED absent regardless of what the host has
// in /usr/bin, so the "not installed" INFO assertions can't flake on a machine
// that happens to ship one of them in a system dir (CodeRabbit, PR #321). go runs
// offline (GOPROXY=off) with an isolated cache. (NB: keeping /usr/bin:/bin on PATH
// — as an earlier draft did — would have leaked any system-installed linter; but
// dropping it naively would also drop sed/awk and break the Makefile-parse cases,
// hence the explicit allowlist of symlinks below.)
func hermeticEnv(t *testing.T, home, gocache string, withMake bool) []string {
	t.Helper()
	binDir := t.TempDir()
	link := func(name string, required bool) {
		src, err := exec.LookPath(name)
		if err != nil {
			if required {
				t.Fatalf("%s not available: %v", name, err)
			}
			return
		}
		if err := os.Symlink(src, filepath.Join(binDir, name)); err != nil {
			t.Fatalf("symlink %s: %v", name, err)
		}
	}
	link("go", true)
	link("sh", true)
	// echo is for make's recipe direct-exec (a `@echo ...` rule runs without a
	// shell, so make resolves echo via PATH); the rest serve the gate's own probe.
	for _, u := range []string{"sed", "awk", "grep", "cat", "echo"} {
		link(u, false)
	}
	if withMake {
		link("make", true)
	}
	return []string{
		"PATH=" + binDir,
		"HOME=" + home,
		"GOCACHE=" + gocache,
		"GOPROXY=off",
		"GOFLAGS=",
	}
}

// runGate sources the probe in dir and returns combined output + the parsed rc and
// PROJECT_CI_RAN. PATH override lets the make-missing case drop make.
func runGate(t *testing.T, probe, dir string, env []string) (out string, rc int, ciRan string) {
	t.Helper()
	probePath := filepath.Join(t.TempDir(), "ci-probe.sh")
	if err := os.WriteFile(probePath, []byte(probe), 0o644); err != nil {
		t.Fatal(err)
	}
	script := ". " + probePath + `; run_project_ci_gate; echo "RC=$?"; echo "RAN=[$PROJECT_CI_RAN]"`
	c := exec.Command("sh", "-c", script)
	c.Dir = dir
	c.Env = env
	// A non-nil err here is normal — the gate exits non-zero on a gate failure, and
	// we parse the real rc from the RC= marker. Only surface runErr if the marker is
	// absent (shell couldn't exec / syntax error), so failures aren't opaque.
	b, runErr := c.CombinedOutput()
	out = string(b)
	if m := regexp.MustCompile(`RC=(\d+)`).FindStringSubmatch(out); m != nil {
		rc = atoi(m[1])
	} else {
		t.Fatalf("no RC marker in output (exec err: %v):\n%s", runErr, out)
	}
	if m := regexp.MustCompile(`RAN=\[([^\]]*)\]`).FindStringSubmatch(out); m != nil {
		ciRan = m[1]
	}
	return out, rc, ciRan
}

func atoi(s string) int {
	n := 0
	for _, r := range s {
		n = n*10 + int(r-'0')
	}
	return n
}

// writeGoModule writes a minimal module; `vetViolation` injects an unreachable-code /
// printf-mismatch that `go vet` flags.
func writeGoModule(t *testing.T, dir string, vetViolation bool) {
	t.Helper()
	mustWrite(t, filepath.Join(dir, "go.mod"), "module example.test\n\ngo 1.22\n")
	src := "package main\n\nimport \"fmt\"\n\nfunc main() { fmt.Println(\"ok\") }\n"
	if vetViolation {
		// Printf format/arg mismatch — a deterministic, offline `go vet` error.
		src = "package main\n\nimport \"fmt\"\n\nfunc main() { fmt.Printf(\"%d\\n\", \"not-an-int\") }\n"
	}
	mustWrite(t, filepath.Join(dir, "main.go"), src)
}

func mustWrite(t *testing.T, path, content string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestRunProjectCIGateRuntime(t *testing.T) {
	goBin, err := exec.LookPath("go")
	if err != nil {
		t.Skip("go not available; skipping runtime gate test")
	}
	if _, err := exec.LookPath("sh"); err != nil {
		t.Skip("sh not available; skipping runtime gate test")
	}
	goDir := filepath.Dir(goBin)
	probe := extractProbe(t)

	// (b) clean Go repo → rc 0.
	t.Run("clean_go_rc0", func(t *testing.T) {
		dir := t.TempDir()
		writeGoModule(t, dir, false)
		out, rc, _ := runGate(t, probe, dir, hermeticEnv(t, t.TempDir(), t.TempDir(), false))
		if rc != 0 {
			t.Errorf("clean Go repo: rc=%d want 0\n%s", rc, out)
		}
		if !strings.Contains(out, "go vet ./...") {
			t.Errorf("clean Go repo: go vet did not run\n%s", out)
		}
	})

	// (a) Go vet violation → rc != 0 AND rc != 2.
	t.Run("go_vet_violation_rc1", func(t *testing.T) {
		dir := t.TempDir()
		writeGoModule(t, dir, true)
		out, rc, _ := runGate(t, probe, dir, hermeticEnv(t, t.TempDir(), t.TempDir(), false))
		if rc == 0 || rc == 2 {
			t.Errorf("vet violation: rc=%d want non-zero and != 2\n%s", rc, out)
		}
	})

	// (c) golangci-lint absent → INFO skip line, rc != 2.
	t.Run("golangci_absent_info_skip", func(t *testing.T) {
		dir := t.TempDir()
		writeGoModule(t, dir, false)
		out, rc, _ := runGate(t, probe, dir, hermeticEnv(t, t.TempDir(), t.TempDir(), false))
		if rc == 2 {
			t.Errorf("optional-absent must not yield rc=2\n%s", out)
		}
		if !strings.Contains(out, "golangci-lint not installed") {
			t.Errorf("expected golangci-lint INFO skip line\n%s", out)
		}
	})

	// (e) empty repo → rc 0 + no-toolchain note.
	t.Run("empty_repo_rc0", func(t *testing.T) {
		dir := t.TempDir()
		out, rc, _ := runGate(t, probe, dir, hermeticEnv(t, t.TempDir(), t.TempDir(), false))
		if rc != 0 {
			t.Errorf("empty repo: rc=%d want 0\n%s", rc, out)
		}
		if !strings.Contains(out, "no recognized toolchain") {
			t.Errorf("empty repo: expected no-toolchain note\n%s", out)
		}
	})

	// (f) build-only Makefile (no ci/check/lint) + vet violation → falls through
	// to language gates: rc != 0 AND PROJECT_CI_RAN empty (make CI path didn't win).
	t.Run("build_only_makefile_falls_through", func(t *testing.T) {
		if _, err := exec.LookPath("make"); err != nil {
			t.Skip("make not available") // Makefile present → gate needs make to fall through (else rc=2)
		}
		dir := t.TempDir()
		writeGoModule(t, dir, true)
		mustWrite(t, filepath.Join(dir, "Makefile"), "build:\n\tgo build ./...\n")
		out, rc, ciRan := runGate(t, probe, dir, hermeticEnv(t, t.TempDir(), t.TempDir(), true))
		if rc == 0 || rc == 2 {
			t.Errorf("build-only Makefile: rc=%d want non-zero != 2 (vet must run)\n%s", rc, out)
		}
		if ciRan != "" {
			t.Errorf("build-only Makefile: PROJECT_CI_RAN=%q want empty (no make CI target)\n%s", ciRan, out)
		}
		if !strings.Contains(out, "go vet ./...") {
			t.Errorf("build-only Makefile: go vet did not run\n%s", out)
		}
	})

	// (d) Makefile `ci:` target wins → PROJECT_CI_RAN=ci AND go vet did NOT run.
	t.Run("makefile_ci_target_wins", func(t *testing.T) {
		if _, err := exec.LookPath("make"); err != nil {
			t.Skip("make not available")
		}
		dir := t.TempDir()
		writeGoModule(t, dir, true) // vet would fail IF it ran — it must not
		mustWrite(t, filepath.Join(dir, "Makefile"), "ci:\n\t@echo running-ci\n")
		out, rc, ciRan := runGate(t, probe, dir, hermeticEnv(t, t.TempDir(), t.TempDir(), true))
		if ciRan != "ci" {
			t.Errorf("Makefile ci target: PROJECT_CI_RAN=%q want ci\n%s", ciRan, out)
		}
		if rc != 0 {
			t.Errorf("Makefile ci (echo) target: rc=%d want 0\n%s", rc, out)
		}
		if strings.Contains(out, "go vet ./...") {
			t.Errorf("Makefile ci won but go vet ALSO ran — precedence broken\n%s", out)
		}
	})

	// (h) Makefile present, make uninstalled → rc 2 BEFORE any language gate.
	t.Run("make_missing_rc2", func(t *testing.T) {
		dir := t.TempDir()
		writeGoModule(t, dir, false)
		mustWrite(t, filepath.Join(dir, "Makefile"), "ci:\n\t@echo hi\n")
		// withMake=false: make is not symlinked into the hermetic bin, so the gate's
		// `command -v make` fails and it returns rc=2 before any language gate.
		out, rc, _ := runGate(t, probe, dir, hermeticEnv(t, t.TempDir(), t.TempDir(), false))
		if rc != 2 {
			t.Errorf("make missing: rc=%d want 2\n%s", rc, out)
		}
		if strings.Contains(out, "go vet ./...") {
			t.Errorf("make-missing must short-circuit BEFORE language gates\n%s", out)
		}
	})

	// (i) polyglot: clean Go + package.json → BOTH stacks detected (proves not
	// first-match): go vet marker AND a node-stack INFO/skip line both present.
	t.Run("polyglot_runs_all_stacks", func(t *testing.T) {
		dir := t.TempDir()
		writeGoModule(t, dir, false)
		mustWrite(t, filepath.Join(dir, "package.json"), "{\"name\":\"x\",\"version\":\"0.0.0\"}\n")
		out, rc, _ := runGate(t, probe, dir, hermeticEnv(t, t.TempDir(), t.TempDir(), false))
		if rc != 0 {
			t.Errorf("polyglot clean: rc=%d want 0\n%s", rc, out)
		}
		if !strings.Contains(out, "go vet ./...") {
			t.Errorf("polyglot: Go stack did not run\n%s", out)
		}
		// package.json present but no tsconfig/eslint config → the JS block emits
		// its config-skip lines, proving it was entered (not first-match-stopped
		// at Go). Robust whether or not tsc/eslint are on PATH.
		if !strings.Contains(out, "skipping tsc") && !strings.Contains(out, "skipping eslint") {
			t.Errorf("polyglot: JS stack was not entered (first-match bug?)\n%s", out)
		}
	})

	// (j) plain-JS repo (package.json, NO tsconfig.json) must NOT run tsc even
	// when a global tsc is on PATH — bare `tsc --noEmit` exits non-zero on a
	// non-TypeScript project and would mis-route to the fix loop (Codex PR #321
	// P2). The tsc gate is opt-in via tsconfig.json. This subtest puts a real
	// global tsc (if installed) on PATH to exercise the exact reported scenario.
	t.Run("js_no_tsconfig_skips_tsc", func(t *testing.T) {
		dir := t.TempDir()
		mustWrite(t, filepath.Join(dir, "package.json"), "{\"name\":\"x\",\"version\":\"0.0.0\"}\n")
		pathDirs := goDir + ":/usr/bin:/bin"
		if tscPath, err := exec.LookPath("tsc"); err == nil {
			pathDirs = filepath.Dir(tscPath) + ":" + pathDirs // face a real global tsc
		}
		env := []string{"PATH=" + pathDirs, "HOME=" + t.TempDir(), "GOCACHE=" + t.TempDir(), "GOPROXY=off", "GOFLAGS="}
		out, rc, _ := runGate(t, probe, dir, env)
		if rc != 0 {
			t.Errorf("plain-JS repo (no tsconfig): rc=%d want 0 (tsc must be skipped)\n%s", rc, out)
		}
		if strings.Contains(out, "tsc --noEmit (language-native gate") {
			t.Errorf("tsc ran despite no tsconfig.json — must skip (Codex #321 P2)\n%s", out)
		}
		if !strings.Contains(out, "no tsconfig.json") {
			t.Errorf("expected the no-tsconfig skip message\n%s", out)
		}
	})

	// (g) optional absent + core fails simultaneously → rc 1, not 2.
	t.Run("optional_absent_core_fails_rc1", func(t *testing.T) {
		dir := t.TempDir()
		writeGoModule(t, dir, true) // go vet fails; golangci-lint absent
		out, rc, _ := runGate(t, probe, dir, hermeticEnv(t, t.TempDir(), t.TempDir(), false))
		if rc != 1 {
			t.Errorf("core-fail + optional-absent: rc=%d want 1\n%s", rc, out)
		}
		if !strings.Contains(out, "golangci-lint not installed") {
			t.Errorf("expected golangci-lint INFO skip alongside the core failure\n%s", out)
		}
	})
}
