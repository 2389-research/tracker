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

// setupCmd returns the ci-probe.sh body Setup installs into .ai/build/ — the
// lib/ci-probe.sh sidecar (it was a heredoc inside Setup's tool_command until
// the shared shell moved to scripts/build_product/lib/).
func setupCmd(t *testing.T) string {
	t.Helper()
	return buildProductLib(t, "ci-probe.sh")
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

// Test 5 — regression-pin: the Makefile path is preserved. `make -f "$MAKEFILE"
// "$TARGET"` (#640 D10: the parsed file is the file make runs) and the awk
// target parser (now the makefile_has_target helper) must survive.
func TestQualityGateMakefilePathPreserved(t *testing.T) {
	cmd := setupCmd(t)
	if !strings.Contains(cmd, `make -f "$MAKEFILE" "$TARGET" 2>&1`) {
		t.Error("Makefile gate `make -f \"$MAKEFILE\" \"$TARGET\" 2>&1` was removed/altered (#299/#640 D10 regression)")
	}
	if !strings.Contains(cmd, "awk -v t=\"$2\"") || !strings.Contains(cmd, "makefile_has_target") {
		t.Error("awk Makefile-target parser (makefile_has_target) was altered (#299)")
	}
	if !strings.Contains(cmd, "for mf in GNUmakefile makefile Makefile; do") {
		t.Error("Makefile probe order must be GNU make's (GNUmakefile, makefile, Makefile) (#640 D10)")
	}
}

// Test 6 — regression-pin (semantic): NO exit code carries meaning any more
// (#640 E8 — dash's rc 2 for a missing script collided with the old reserved
// make-missing 2). The make-missing environment case is signalled out of band:
// the `_TRACKER_CI_MAKE_MISSING` line and the .ai/build/ci-make-missing file,
// anchored to the `command -v make` branch, and no `return 2` anywhere.
func TestQualityGateRc2OnlyMakeMissing(t *testing.T) {
	cmd := setupCmd(t)
	if n := strings.Count(cmd, "return 2"); n != 0 {
		t.Fatalf("expected no `return 2` (rc numbers carry no meaning since #640 E8), found %d", n)
	}
	makeIdx := strings.Index(cmd, "command -v make")
	lineIdx := strings.Index(cmd, `echo "_TRACKER_CI_MAKE_MISSING"`)
	fileIdx := strings.LastIndex(cmd, ".ai/build/ci-make-missing") // the header comment names it first
	if makeIdx == -1 || lineIdx < makeIdx || fileIdx < makeIdx {
		t.Error("the make-missing marker line/file must be emitted in the `command -v make` branch (#640 E8)")
	}
	if g := strings.Index(cmd, "(language-native gate,"); g != -1 {
		if strings.Contains(cmd[g:], "_TRACKER_CI_MAKE_MISSING") {
			t.Error("the make-missing marker must not be emitted within/after the language-native gates")
		}
	}
}

// Test 7 — regression-pin: the gate stays CENTRALIZED. The shared
// .ai/build/verify.sh (written by Setup, run by TestMilestone, the
// Implement/FixMilestone breach verify_command, and — since #640 — FinalBuild
// in --final mode) is the ONE caller that sources ci-probe.sh and calls
// run_project_ci_gate; the node wrappers delegate to it and grow no gate logic
// of their own.
func TestQualityGateStaysCentralized(t *testing.T) {
	g := loadBuildProduct(t)
	verify := buildProductLib(t, "verify.sh")
	if !strings.Contains(verify, ". .ai/build/ci-probe.sh") {
		t.Error("verify.sh no longer sources ci-probe.sh (#299)")
	}
	if !strings.Contains(verify, "run_project_ci_gate") {
		t.Error("verify.sh no longer calls run_project_ci_gate (#299)")
	}
	wrappers := map[string]string{
		"TestMilestone": g.Nodes["TestMilestone"].Attrs["tool_command"],
		"FinalBuild":    g.Nodes["FinalBuild"].Attrs["tool_command"],
	}
	for id, cmd := range wrappers {
		if !strings.Contains(cmd, "sh .ai/build/verify.sh") {
			t.Errorf("%s no longer delegates to the shared verify.sh (#406/#640)", id)
		}
		// No duplicated gate: wrappers must not grow their own `go vet` / stack runners.
		for _, dup := range []string{"go vet", "go test", "npm test", "run_project_ci_gate"} {
			if strings.Contains(cmd, dup) {
				t.Errorf("%s grew its own %q — gate must stay in the shared helper (#299)", id, dup)
			}
		}
	}
	if !strings.Contains(wrappers["FinalBuild"], "sh .ai/build/verify.sh --final") {
		t.Error("FinalBuild must run verify.sh in --final ship mode (#640 D7/D12)")
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

// extractProbe returns the ci-probe.sh body — the exact bytes tracker copies
// to .ai/build/ci-probe.sh at runtime.
func extractProbe(t *testing.T) string {
	t.Helper()
	return buildProductLib(t, "ci-probe.sh")
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
			// A required tool absent is an environment limitation, not a code
			// defect — skip the case (the suite already skips when go/sh/make are
			// absent) rather than failing the whole run.
			if required {
				t.Skipf("%s not available; skipping runtime gate test", name)
			}
			return
		}
		if err := os.Symlink(src, filepath.Join(binDir, name)); err != nil {
			// Symlinks unsupported (e.g. Windows without Developer Mode) — skip,
			// matching the precedent in git_preflight_test.go.
			t.Skipf("cannot symlink %s (likely a platform without symlink support): %v", name, err)
		}
	}
	link("go", true)
	link("sh", true)
	// sed/awk/grep are load-bearing, not decorative: the Makefile-target parser is
	// `sed | awk` and the eslint-config check greps package.json. Require them so a
	// host missing one SKIPS rather than silently passing a parse-dependent case for
	// the wrong reason (a missing sed/awk would make the parser find no target and
	// fall through, masquerading as "no CI target").
	for _, u := range []string{"sed", "awk", "grep"} {
		link(u, true)
	}
	// #640: stack detection (find|sort outside a git repo, head/cut/tr for the
	// golangci-lint version parse, mktemp for the stack list) — all load-bearing.
	for _, u := range []string{"find", "sort", "mktemp", "head", "cut", "tr", "rm", "cat", "mkdir"} {
		link(u, true)
	}
	// echo: GNU make has a no-shell optimization — a recipe line with NO shell
	// metacharacters is exec'd DIRECTLY (not via /bin/sh), so make resolves the
	// command via PATH. The `ci:` fixture's `@echo running-ci` has none, so echo
	// must be on PATH (empirically: makefile_ci_target_wins fails with "make: echo:
	// No such file or directory" without it). A recipe WITH a metacharacter (|,>,&&,
	// …) would instead go through /bin/sh, where echo is a builtin — but this
	// fixture doesn't. cat is a belt-and-suspenders helper. Both genuinely optional.
	for _, u := range []string{"echo"} {
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
	// Run under `set -eu` and capture rc via `|| rc=$?` — the exact shape TestMilestone
	// uses (`run_project_ci_gate || CI_RC=$?` under set -eu). This adds set -u coverage
	// and realistic shell options; the FinalBuild bare-call-under-set-e path (where a
	// missing `|| LANG_RC=$?` guard would abort mid-function) is covered separately by
	// TestRunProjectCIGateSetEBareCall.
	// Pass the probe path as $1 rather than interpolating it into the script, so a
	// temp dir with spaces/special chars can't break sourcing (Copilot, PR #321).
	script := `set -eu; . "$1"; rc=0; run_project_ci_gate || rc=$?; echo "RC=$rc"; echo "RAN=[$PROJECT_CI_RAN]"`
	c := exec.Command("sh", "-c", script, "sh", probePath)
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

// sourceAndRun sources the probe then runs `body` under `set -eu` in dir, returning
// combined output and the process exit code. Unlike runGate it parses no RC marker —
// it's for the FinalBuild bare-call invariant, where set -e may abort the script
// before any trailing marker prints.
func sourceAndRun(t *testing.T, probe, dir string, env []string, body string) (string, int) {
	t.Helper()
	probePath := filepath.Join(t.TempDir(), "ci-probe.sh")
	if err := os.WriteFile(probePath, []byte(probe), 0o644); err != nil {
		t.Fatal(err)
	}
	// $1 carries the probe path (not interpolated) — space/special-char safe (PR #321).
	c := exec.Command("sh", "-c", "set -eu\n. \"$1\"\n"+body, "sh", probePath)
	c.Dir = dir
	c.Env = env
	b, err := c.CombinedOutput()
	code := 0
	if err != nil {
		ee, ok := err.(*exec.ExitError)
		if !ok {
			t.Fatalf("exec failed: %v\n%s", err, string(b))
		}
		code = ee.ExitCode()
	}
	return string(b), code
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

	// (a) Go vet violation → ADVISORY (tracker-runner convergence): the
	// finding is printed, the ADVISORY line names the policy, rc stays 0 —
	// the language-native gates never block; only a project Makefile target
	// does.
	t.Run("go_vet_violation_advisory", func(t *testing.T) {
		dir := t.TempDir()
		writeGoModule(t, dir, true)
		out, rc, _ := runGate(t, probe, dir, hermeticEnv(t, t.TempDir(), t.TempDir(), false))
		if rc != 0 {
			t.Errorf("vet violation: rc=%d want 0 (advisory)\n%s", rc, out)
		}
		if !strings.Contains(out, "wrong type string") {
			t.Errorf("vet finding not printed\n%s", out)
		}
		if !strings.Contains(out, "ADVISORY: one or more language-native lint/type-check gates reported findings") {
			t.Errorf("missing the ADVISORY line\n%s", out)
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
	// to language gates: vet RUNS (its finding + the ADVISORY line are printed),
	// rc 0 (advisory) AND PROJECT_CI_RAN empty (make CI path didn't win).
	t.Run("build_only_makefile_falls_through", func(t *testing.T) {
		if _, err := exec.LookPath("make"); err != nil {
			t.Skip("make not available") // Makefile present → gate needs make to fall through (else rc=2)
		}
		dir := t.TempDir()
		writeGoModule(t, dir, true)
		mustWrite(t, filepath.Join(dir, "Makefile"), "build:\n\tgo build ./...\n")
		out, rc, ciRan := runGate(t, probe, dir, hermeticEnv(t, t.TempDir(), t.TempDir(), true))
		if rc != 0 || !strings.Contains(out, "ADVISORY:") {
			t.Errorf("build-only Makefile: rc=%d want 0 with the ADVISORY line (vet must run, advisory)\n%s", rc, out)
		}
		if ciRan != "" {
			t.Errorf("build-only Makefile: PROJECT_CI_RAN=%q want empty (no make CI target)\n%s", ciRan, out)
		}
		if !strings.Contains(out, "go vet ./...") {
			t.Errorf("build-only Makefile: go vet did not run\n%s", out)
		}
	})

	// (d) Makefile `ci:` target runs (PROJECT_CI_RAN=ci) AND go vet ALSO runs
	// (#640 D8: a no-op Makefile target must not hide the native gates) — the
	// vet finding is reported as ADVISORY; the green `make ci` is the
	// project's own oracle, so rc is 0.
	t.Run("makefile_ci_target_and_native_gates", func(t *testing.T) {
		if _, err := exec.LookPath("make"); err != nil {
			t.Skip("make not available")
		}
		dir := t.TempDir()
		writeGoModule(t, dir, true) // vet fails — and it MUST run
		mustWrite(t, filepath.Join(dir, "Makefile"), "ci:\n\t@echo running-ci\n")
		out, rc, ciRan := runGate(t, probe, dir, hermeticEnv(t, t.TempDir(), t.TempDir(), true))
		if ciRan != "ci" {
			t.Errorf("Makefile ci target: PROJECT_CI_RAN=%q want ci\n%s", ciRan, out)
		}
		if !strings.Contains(out, "running-ci") {
			t.Errorf("Makefile ci target did not run\n%s", out)
		}
		if !strings.Contains(out, "go vet ./...") {
			t.Errorf("Makefile ci ran but go vet did NOT — native gates must run in addition (#640 D8)\n%s", out)
		}
		if rc != 0 || !strings.Contains(out, "ADVISORY:") {
			t.Errorf("no-op ci target + vet violation: rc=%d want 0 with the ADVISORY line (#640 D8, advisory)\n%s", rc, out)
		}
	})

	// (k) Makefile ci target FAILS (recipe error) → rc 1, NOT 2 (#320). GNU make
	// exits 2 on any recipe error, which previously collided with the rc=2
	// "make missing" contract and mis-routed fixable CI failures to human
	// escalation (resetting the fix-attempt counter). The helper must collapse
	// a make-run failure to exactly 1 so it routes to the FixMilestone loop.
	t.Run("makefile_ci_failure_rc1", func(t *testing.T) {
		if _, err := exec.LookPath("make"); err != nil {
			t.Skip("make not available")
		}
		dir := t.TempDir()
		// `|| exit 1` forces the recipe through /bin/sh (metacharacter defeats
		// make's no-shell fast path) and makes the failure independent of the
		// hermetic PATH: `exit` is a builtin everywhere, and if this sh resolves
		// `false` externally (POSIX doesn't require it builtin) the not-found
		// failure (127) still trips `|| exit 1` — the recipe exits 1 either way.
		mustWrite(t, filepath.Join(dir, "Makefile"), "ci:\n\t@false || exit 1\n")
		out, rc, ciRan := runGate(t, probe, dir, hermeticEnv(t, t.TempDir(), t.TempDir(), true))
		if ciRan != "ci" {
			t.Errorf("failing ci target: PROJECT_CI_RAN=%q want ci (make path must have run)\n%s", ciRan, out)
		}
		if rc != 1 {
			t.Errorf("failing ci target: rc=%d want 1 — rc=2 is reserved for make-missing (#320)\n%s", rc, out)
		}
	})

	// (h) Makefile present, make uninstalled → rc 1 + the out-of-band marker
	// line and file (#640 E8), BEFORE any language gate.
	t.Run("make_missing_marker", func(t *testing.T) {
		dir := t.TempDir()
		writeGoModule(t, dir, false)
		mustWrite(t, filepath.Join(dir, "Makefile"), "ci:\n\t@echo hi\n")
		// withMake=false: make is not symlinked into the hermetic bin, so the gate's
		// `command -v make` fails and it signals make-missing before any language gate.
		out, rc, _ := runGate(t, probe, dir, hermeticEnv(t, t.TempDir(), t.TempDir(), false))
		if rc != 1 {
			t.Errorf("make missing: rc=%d want 1 (no reserved exit number, #640 E8)\n%s", rc, out)
		}
		if !strings.Contains(out, "_TRACKER_CI_MAKE_MISSING") {
			t.Errorf("make missing: expected the _TRACKER_CI_MAKE_MISSING marker line\n%s", out)
		}
		if _, err := os.Stat(filepath.Join(dir, ".ai/build/ci-make-missing")); err != nil {
			t.Errorf("make missing: expected .ai/build/ci-make-missing marker file: %v\n%s", err, out)
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

	// (g) optional absent + core reports simultaneously → rc 0 (advisory),
	// never 2, with both the skip line and the ADVISORY line.
	t.Run("optional_absent_core_reports_advisory", func(t *testing.T) {
		dir := t.TempDir()
		writeGoModule(t, dir, true) // go vet fails; golangci-lint absent
		out, rc, _ := runGate(t, probe, dir, hermeticEnv(t, t.TempDir(), t.TempDir(), false))
		if rc != 0 || !strings.Contains(out, "ADVISORY:") {
			t.Errorf("core-report + optional-absent: rc=%d want 0 with the ADVISORY line\n%s", rc, out)
		}
		if !strings.Contains(out, "golangci-lint not installed") {
			t.Errorf("expected golangci-lint INFO skip alongside the core failure\n%s", out)
		}
	})
}

// TestRunProjectCIGateSetEBareCall covers the FinalBuild invariant that the main
// runtime harness (which captures rc via `|| rc=$?`, suppressing set -e inside the
// function) cannot: FinalBuild calls `run_project_ci_gate` BARE under `set -eu`, so
// a gate missing its `|| LANG_RC=$?` guard would abort the function the instant a
// gate fails — before the remaining gates run and before the function's own return.
// With the native gates advisory, the bare call returns 0 on a vet finding, so the
// invariant is proven by the later gates' lines AND the trailing marker being reached.
func TestRunProjectCIGateSetEBareCall(t *testing.T) {
	if _, err := exec.LookPath("go"); err != nil {
		t.Skip("go not available; skipping runtime gate test")
	}
	if _, err := exec.LookPath("sh"); err != nil {
		t.Skip("sh not available; skipping runtime gate test")
	}
	probe := extractProbe(t)

	// Guard invariant: on a vet-violation repo, a bare call under set -e must
	// still run PAST the failing go vet to the golangci-lint INFO line (proving
	// the failure was accumulated via `|| LANG_RC=1`, not aborted), print the
	// ADVISORY line, and — the gate being advisory — return 0 so the trailing
	// marker is reached. If any gate lost its guard, set -e would abort at that
	// gate and golangci's INFO line would be absent.
	t.Run("failing_gate_runs_all_then_advises", func(t *testing.T) {
		dir := t.TempDir()
		writeGoModule(t, dir, true)
		out, code := sourceAndRun(t, probe, dir, hermeticEnv(t, t.TempDir(), t.TempDir(), false),
			"run_project_ci_gate\necho REACHED-END")
		if !strings.Contains(out, "go vet ./...") {
			t.Errorf("go vet did not run\n%s", out)
		}
		if !strings.Contains(out, "golangci-lint not installed") {
			t.Errorf("function aborted at the failing go vet — a gate is missing its `|| LANG_RC=1` guard\n%s", out)
		}
		if !strings.Contains(out, "ADVISORY:") {
			t.Errorf("missing the ADVISORY line\n%s", out)
		}
		if !strings.Contains(out, "REACHED-END") || code != 0 {
			t.Errorf("advisory native gate must not abort the bare set -e caller (exit=%d)\n%s", code, out)
		}
	})

	// Happy path: a clean repo's bare call under set -eu reaches the trailing marker
	// (all gates pass → return 0 → no spurious abort).
	t.Run("clean_gate_reaches_end", func(t *testing.T) {
		dir := t.TempDir()
		writeGoModule(t, dir, false)
		out, code := sourceAndRun(t, probe, dir, hermeticEnv(t, t.TempDir(), t.TempDir(), false),
			"run_project_ci_gate\necho REACHED-END")
		if !strings.Contains(out, "REACHED-END") {
			t.Errorf("clean repo bare call did not reach end (spurious set -e abort?)\n%s", out)
		}
		if code != 0 {
			t.Errorf("clean repo bare call: exit=%d want 0\n%s", code, out)
		}
	})
}
