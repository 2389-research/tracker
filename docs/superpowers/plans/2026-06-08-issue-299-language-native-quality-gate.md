# Language-native quality gate fallback (#299) — Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** When a repo has no Makefile `ci`/`check`/`lint` target, `run_project_ci_gate` must still enforce language-native lint/vet/format gates (Go/JS/TS/Python/Rust) instead of silently passing on the test runner alone — closing the no-Makefile blind spot from epic #308 Phase 2 (#299).

**Architecture:** Pure `.dip` change to `examples/build_product.dip`. The shared `ci-probe.sh` heredoc (written by the `Setup` node, sourced by `TestMilestone` and `FinalBuild`) gains a new `run_language_native_gates` helper. `run_project_ci_gate`'s two early `return 0` skip paths now call it. The awk Makefile-target parser and the `make` path are untouched. Five prose sites that describe the old "no Makefile → skipped" semantics are corrected. Tests: graph string-asserts (negative-control) + a hermetic runtime shell test.

**Tech Stack:** dippin `.dip` workflow DSL, POSIX `sh`, Go `testing` (package `pipeline`), `dippin` CLI for validate/doctor/simulate.

**Branch:** `fix/299-language-native-quality-gate` (already created off `main`). The design spec is `docs/superpowers/specs/2026-06-08-issue-299-language-native-quality-gate-design.md` — read it first.

---

## Context the executor needs

**The file under change:** `examples/build_product.dip`. All line numbers below are verified against `main` as of 2026-06-08 but **re-grep before editing** (they drift). Key anchors:
- `grep -n 'run_project_ci_gate\|PROBE_EOF\|no Makefile present\|no project CI target' examples/build_product.dip`
- The helper `run_project_ci_gate` is at `:51-92`, inside `cat > .ai/build/ci-probe.sh <<'PROBE_EOF'` (`:41`) … `PROBE_EOF` (`:93`), which lives in the `Setup` node (`tool Setup`, `:22`).
- Callers: `TestMilestone` sources at `:587` and runs `run_project_ci_gate || CI_RC=$?` at `:589`, branching on `CI_RC` at `:590-597`. `FinalBuild` sources at `:1247` and runs `run_project_ci_gate` bare (under `set -eu`) at `:1248`.

**The heredoc is `<<'PROBE_EOF'` (quoted):** every `$` inside is written literally to `ci-probe.sh` and expands at runtime when sourced. Do NOT unquote it; do NOT escape the new `$?`/`$LANG_RC`/`$TARGET`.

**Return-code contract (load-bearing):** `0` clean / `2` make-missing→escalate-to-human / `N` failure→fix-loop. `TestMilestone:590` treats `CI_RC -eq 2` as *exactly* "make missing." The new gates must **never** return 2 — they collapse to `return 1`.

**`set -e` semantics (why every gate needs `|| LANG_RC=$?`):** the helper is sourced into `set -eu` shells. `TestMilestone` calls it in a `|| CI_RC=$?` context (set -e ignored throughout the body), but `FinalBuild` calls it **bare** (set -e active in the body). Under FinalBuild's active set -e, a bare failing gate would abort the node immediately. The `|| LANG_RC=$?` on every gate neutralizes set -e per-gate so all gates run and accumulate; the function then returns 1, which propagates to FinalBuild correctly.

**Marker-last / substring-match constraint:** the engine edges substring-match `ctx.tool_stdout contains escalate` / `tests-pass`. No new INFO line may contain the literal tokens `escalate` or `tests-pass`. (The existing make-missing line says "escalat**ing**" — safe, it doesn't contain "escalate".) The helper prints no routing marker; callers print `tests-pass`/`escalate`/`final-build-pass` last.

**Local pre-commit hooks are NOT installed** in this clone (`.git/hooks/` has only `*.sample`); `make test` runs via the pre-commit framework only if `pre-commit install` was run. This is how #298's `(RED)` commit (`ad0fe76`) landed a failing test. **You may commit the negative-control test RED, mirroring that precedent. NEVER use `--no-verify`.** If you find hooks ARE active and they block the RED commit, instead keep the RED proof in-session (run + observe failure) and commit test+impl together — never bypass.

---

## File structure

- **Modify:** `examples/build_product.dip`
  - `Setup` node heredoc `:51-92`: two 2-line edits inside `run_project_ci_gate`; append new `run_language_native_gates` helper before `PROBE_EOF`; update header comment `:42-50`.
  - `VerifyMilestone` prompt: prose-sync at `:655-658`, `:745-754`, `:759-765`.
- **Create:** `pipeline/build_product_quality_gate_test.go` (package `pipeline`) — Layer A string-asserts + Layer B runtime shell test.
- **Modify:** `CHANGELOG.md` — `[Unreleased]/Added` entry.

---

## Task 1: Negative-control tests (Layer A string-asserts), proven RED

**Files:**
- Create: `pipeline/build_product_quality_gate_test.go`
- Reference precedent: `pipeline/build_product_buildcontext_test.go` (uses `loadBuildProduct(t)` and `g.Nodes["Setup"].Attrs["tool_command"]`).

- [ ] **Step 1: Write the Layer A test file.** Each assertion is commented as `negative-control` (RED pre-#299) or `regression-pin` (GREEN, guards removal).

```go
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
	// No return 2 after the first language-native gate marker.
	if g := strings.Index(cmd, "language-native gate"); g != -1 {
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
```

- [ ] **Step 2: Run the tests, verify they FAIL (RED).**

Run: `go test ./pipeline/ -run 'TestQualityGate' -v`
Expected: Tests 1–4 and 8 FAIL (`go vet`, the optional linters, the guards, and the prose are absent pre-#299); Tests 5–7 PASS (regression-pins on existing structure). Confirm 1–4 and 8 are RED — that is the negative-control proof.

- [ ] **Step 3: Commit the RED tests** (mirrors #298 `ad0fe76`).

```bash
git add pipeline/build_product_quality_gate_test.go
git commit -m "test(299): negative-control tests for language-native quality gate (RED)

Co-Authored-By: Claude Opus 4.8 (1M context) <noreply@anthropic.com>"
```

---

## Task 2: Implement the `run_language_native_gates` helper + wire the two fall-throughs

**Files:**
- Modify: `examples/build_product.dip` (Setup heredoc `:51-92`, header `:42-50`).

- [ ] **Step 1: Edit the no-Makefile early return** (`:57-60`). Re-grep first: `grep -n 'no Makefile present, no project CI gate' examples/build_product.dip`.

Replace:
```sh
        if [ -z "$MAKEFILE" ]; then
          echo "INFO: no Makefile present, no project CI gate to run"
          return 0
        fi
```
with:
```sh
        if [ -z "$MAKEFILE" ]; then
          echo "INFO: no Makefile present — running language-native gates (#299)"
          run_language_native_gates
          return $?
        fi
```

- [ ] **Step 2: Edit the no-matching-target return** (`:90-91`). Re-grep: `grep -n 'no project CI target in' examples/build_product.dip`.

Replace:
```sh
        echo "INFO: no project CI target in $MAKEFILE (looked for: ci, check, lint)"
        return 0
      }
```
with:
```sh
        echo "INFO: no project CI target in $MAKEFILE (looked for: ci, check, lint) — running language-native gates (#299)"
        run_language_native_gates
        return $?
      }
```

- [ ] **Step 3: Append the `run_language_native_gates` helper** immediately after `run_project_ci_gate`'s closing `}` and before `PROBE_EOF`. Re-grep the `PROBE_EOF` line: `grep -n 'PROBE_EOF' examples/build_product.dip` (use the SECOND match — the closing delimiter). Insert before it:

```sh
      # Language-native quality-gate fallback (issue #299, epic #308 Phase 2).
      # Reached from run_project_ci_gate when no Makefile ci/check/lint target
      # ran. Runs gates for EVERY detected toolchain (polyglot, not first-match).
      # Returns 0 (clean / nothing to gate) or 1 (some gate failed) — NEVER 2;
      # rc=2 stays reserved for the make-missing case in run_project_ci_gate.
      #
      # INVARIANT: every gate command MUST end in `|| LANG_RC=$?`. These nodes run
      # `set -eu`; FinalBuild calls the gate BARE (set -e active in the body), so a
      # bare failing gate would abort the node before later gates/markers run. The
      # `|| LANG_RC=$?` neutralizes set -e per gate and accumulates the failure.
      # "Core" tools fail when they FAIL; "optional" tools are command-v guarded so
      # ABSENCE is a one-line INFO skip (never a failure, never rc=2).
      run_language_native_gates() {
        LANG_RC=0
        RAN_ANY=""
        # Go — `go vet` is the core gate (go is present by go.mod detection).
        if [ -f go.mod ]; then
          RAN_ANY=1
          echo "--- go vet ./... (language-native gate, #299) ---"
          go vet ./... 2>&1 || LANG_RC=$?
          if command -v golangci-lint >/dev/null 2>&1; then
            echo "--- golangci-lint run (language-native gate, #299) ---"
            golangci-lint run 2>&1 || LANG_RC=$?
          else
            echo "INFO: golangci-lint not installed — skipping (optional, #299)"
          fi
        fi
        # JS/TS — both run-if-present.
        if [ -f package.json ]; then
          RAN_ANY=1
          if command -v tsc >/dev/null 2>&1; then
            echo "--- tsc --noEmit (language-native gate, #299) ---"
            tsc --noEmit 2>&1 || LANG_RC=$?
          else
            echo "INFO: tsc not installed — skipping (optional, #299)"
          fi
          if command -v eslint >/dev/null 2>&1; then
            echo "--- eslint . (language-native gate, #299) ---"
            eslint . 2>&1 || LANG_RC=$?
          else
            echo "INFO: eslint not installed — skipping (optional, #299)"
          fi
        fi
        # Python — both run-if-present.
        if [ -f pyproject.toml ]; then
          RAN_ANY=1
          if command -v ruff >/dev/null 2>&1; then
            echo "--- ruff check . (language-native gate, #299) ---"
            ruff check . 2>&1 || LANG_RC=$?
          else
            echo "INFO: ruff not installed — skipping (optional, #299)"
          fi
          if command -v mypy >/dev/null 2>&1; then
            echo "--- mypy . (language-native gate, #299) ---"
            mypy . 2>&1 || LANG_RC=$?
          else
            echo "INFO: mypy not installed — skipping (optional, #299)"
          fi
        fi
        # Rust — run-if-present (cargo gates both fmt and clippy).
        if [ -f Cargo.toml ]; then
          RAN_ANY=1
          if command -v cargo >/dev/null 2>&1; then
            echo "--- cargo fmt --check (language-native gate, #299) ---"
            cargo fmt --check 2>&1 || LANG_RC=$?
            echo "--- cargo clippy -- -D warnings (language-native gate, #299) ---"
            cargo clippy -- -D warnings 2>&1 || LANG_RC=$?
          else
            echo "INFO: cargo not installed — skipping (optional, #299)"
          fi
        fi
        if [ -z "$RAN_ANY" ]; then
          echo "INFO: no recognized toolchain (go.mod/package.json/pyproject.toml/Cargo.toml) — no language-native gate (#299)"
        fi
        if [ "$LANG_RC" -ne 0 ]; then
          return 1
        fi
        return 0
      }
```

- [ ] **Step 4: Update the helper header comment** (`:42-50`). Re-grep: `grep -n 'Source this file, then call' examples/build_product.dip`. Replace the `Returns:` block so it is truthful:

Replace:
```sh
      # Source this file, then call `run_project_ci_gate`. Returns:
      #   0  — clean (no Makefile, no matching target, OR
      #        `make <TARGET>` exited 0)
      #   2  — Makefile present but `make` not installed (caller
      #        should escalate; LLM fix loop can't install a binary)
      #   N  — `make <TARGET>` exited with non-zero N
      #        (caller should fail / retry)
      # Sets PROJECT_CI_RAN to the chosen target on a real run,
      # empty string otherwise.
```
with:
```sh
      # Source this file, then call `run_project_ci_gate`. Returns:
      #   0  — clean: `make <TARGET>` exited 0, OR (no Makefile gate
      #        ran) the language-native gates passed / no toolchain
      #        was detected (issue #299).
      #   2  — Makefile present but `make` not installed (caller
      #        should escalate; LLM fix loop can't install a binary).
      #        rc=2 is reserved for THIS case only.
      #   N  — a gate failed: `make <TARGET>` exited non-zero, OR a
      #        language-native gate (go vet / golangci-lint / tsc /
      #        eslint / ruff / mypy / cargo fmt|clippy) failed
      #        (collapsed to 1; caller should fail / retry).
      # Sets PROJECT_CI_RAN to the chosen target on a real `make` run,
      # empty string otherwise (incl. the language-native fall-through).
```

- [ ] **Step 5: Run Layer A tests, verify Tests 1–7 PASS.**

Run: `go test ./pipeline/ -run 'TestQualityGate' -v`
Expected: Tests 1–7 PASS; Test 8 still FAILS (prose not yet edited — Task 3).

- [ ] **Step 6: Validate the .dip still parses.**

Run: `dippin validate examples/build_product.dip`
Expected: passes (no parse error). If `dippin` is not on PATH, STOP and ask the user — do NOT `go install` it.

- [ ] **Step 7: Commit.**

```bash
git add examples/build_product.dip
git commit -m "feat(build_product): language-native quality gate fallback (#299)

run_project_ci_gate now falls through to run_language_native_gates when no
Makefile ci/check/lint target ran. Runs go vet (+ golangci-lint), tsc/eslint,
ruff/mypy, cargo fmt/clippy for every detected toolchain; optional tools absent
=> INFO skip; failures collapse to rc=1 (never 2). awk/make path untouched.

Refs epic #308 Phase 2 (first item).

Co-Authored-By: Claude Opus 4.8 (1M context) <noreply@anthropic.com>"
```

---

## Task 3: Prose-sync — keep VerifyMilestone truthful

**Files:**
- Modify: `examples/build_product.dip` (`VerifyMilestone` prompt `:655-765`).

- [ ] **Step 1: Fix the preamble** (`:655-658`). Re-grep: `grep -n 'OR the probe correctly skipped' examples/build_product.dip`.

Replace:
```
      (when a Makefile with a `ci`/`check`/`lint` target was present)
      OR the probe correctly skipped (no Makefile, or no matching
      target). The sentinel does NOT prove every check executed —
```
with:
```
      (a Makefile `ci`/`check`/`lint` target, OR — when no such target
      ran — the language-native gates: go vet/golangci-lint, tsc/eslint,
      ruff/mypy, cargo fmt/clippy for each detected toolchain, #299)
      OR there was nothing to gate (no recognized toolchain). The
      sentinel does NOT prove every check executed —
```

- [ ] **Step 2: Fix VERIFY item 6 "validly skipped"** (`:745-754`). Re-grep: `grep -n 'passed-or-was-validly-skipped AND its project' examples/build_product.dip`.

Replace:
```
         runner passed-or-was-validly-skipped AND its project CI gate
         passed-or-was-validly-skipped (see the preamble above for the
         exact "validly skipped" rules). The sentinel is emitted at
```
with:
```
         runner passed-or-was-validly-skipped AND its project CI gate
         passed (a Makefile target OR the language-native gates, #299)
         or had nothing to gate (see the preamble above for the exact
         rules). The sentinel is emitted at
```

- [ ] **Step 3: Fix the cause description** (`:759-765`). Re-grep: `grep -n 'CI_RC==2' examples/build_product.dip`. Read the surrounding lines and broaden any text that scopes "the project CI gate" to Makefile-only, noting that a language-native gate failure folds into `TEST_EXIT` → FixMilestone (routing unchanged). Make the minimal edit that removes the Makefile-only implication; keep the rc=2 = make-missing statement (still true). Example: if the text says the project CI gate is the `make` gate, add "(or a language-native gate, #299)".

- [ ] **Step 4: Run the prose test, verify Test 8 PASSES.**

Run: `go test ./pipeline/ -run 'TestQualityGateVerifyPromptTruthful' -v`
Expected: PASS.

- [ ] **Step 5: Re-validate + commit.**

```bash
dippin validate examples/build_product.dip
git add examples/build_product.dip
git commit -m "docs(build_product): sync VerifyMilestone prose to language-native gate (#299)

After #299 'no Makefile' no longer means 'skipped' — language gates run.
Update the preamble, VERIFY item 6, and the cause description so the
doc-in-prompt stays truthful.

Co-Authored-By: Claude Opus 4.8 (1M context) <noreply@anthropic.com>"
```

---

## Task 4: Runtime shell test (Layer B — the behavioral driver)

**Files:**
- Modify: `pipeline/build_product_quality_gate_test.go` (add the runtime test).

This is the test that proves the *behavior* (string-asserts only prove text presence). It extracts the `ci-probe.sh` body from the loaded `Setup` tool_command, writes it to a temp file, and sources it under a hermetic env in fixture repos.

- [ ] **Step 1: Add the runtime test harness + cases.**

```go
import (
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

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

// hermeticEnv builds an env that deterministically excludes optional linters
// (golangci-lint/ruff/etc. typically live in ~/go/bin or ~/.local/bin, not in the
// dir-of-go or /usr/bin:/bin) and runs go offline.
func hermeticEnv(t *testing.T, goDir, home, gocache string) []string {
	t.Helper()
	return []string{
		"PATH=" + goDir + ":/usr/bin:/bin",
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
	b, _ := c.CombinedOutput()
	out = string(b)
	if m := regexp.MustCompile(`RC=(\d+)`).FindStringSubmatch(out); m != nil {
		rc = atoi(m[1])
	} else {
		t.Fatalf("no RC marker in output:\n%s", out)
	}
	if m := regexp.MustCompile(`RAN=\[([^\]]*)\]`).FindStringSubmatch(out); m != nil {
		ciRan = m[1]
	}
	return out, rc, ciRan
}

func atoi(s string) int { n := 0; for _, r := range s { n = n*10 + int(r-'0') }; return n }

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
		out, rc, _ := runGate(t, probe, dir, hermeticEnv(t, goDir, t.TempDir(), t.TempDir()))
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
		out, rc, _ := runGate(t, probe, dir, hermeticEnv(t, goDir, t.TempDir(), t.TempDir()))
		if rc == 0 || rc == 2 {
			t.Errorf("vet violation: rc=%d want non-zero and != 2\n%s", rc, out)
		}
	})

	// (c) golangci-lint absent → INFO skip line, rc != 2.
	t.Run("golangci_absent_info_skip", func(t *testing.T) {
		dir := t.TempDir()
		writeGoModule(t, dir, false)
		out, rc, _ := runGate(t, probe, dir, hermeticEnv(t, goDir, t.TempDir(), t.TempDir()))
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
		out, rc, _ := runGate(t, probe, dir, hermeticEnv(t, goDir, t.TempDir(), t.TempDir()))
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
		dir := t.TempDir()
		writeGoModule(t, dir, true)
		mustWrite(t, filepath.Join(dir, "Makefile"), "build:\n\tgo build ./...\n")
		out, rc, ciRan := runGate(t, probe, dir, hermeticEnv(t, goDir, t.TempDir(), t.TempDir()))
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
		out, rc, ciRan := runGate(t, probe, dir, hermeticEnv(t, goDir, t.TempDir(), t.TempDir()))
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
		// PATH without /usr/bin:/bin so `command -v make` fails; only goDir present.
		env := []string{"PATH=" + goDir, "HOME=" + t.TempDir(), "GOCACHE=" + t.TempDir(), "GOPROXY=off"}
		out, rc, _ := runGate(t, probe, dir, env)
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
		out, rc, _ := runGate(t, probe, dir, hermeticEnv(t, goDir, t.TempDir(), t.TempDir()))
		if rc != 0 {
			t.Errorf("polyglot clean: rc=%d want 0\n%s", rc, out)
		}
		if !strings.Contains(out, "go vet ./...") {
			t.Errorf("polyglot: Go stack did not run\n%s", out)
		}
		// tsc/eslint absent under hermetic PATH → INFO skip lines prove the JS
		// stack was entered (not first-match-stopped at Go).
		if !strings.Contains(out, "tsc not installed") && !strings.Contains(out, "eslint not installed") {
			t.Errorf("polyglot: JS stack was not entered (first-match bug?)\n%s", out)
		}
	})

	// (g) optional absent + core fails simultaneously → rc 1, not 2.
	t.Run("optional_absent_core_fails_rc1", func(t *testing.T) {
		dir := t.TempDir()
		writeGoModule(t, dir, true) // go vet fails; golangci-lint absent
		out, rc, _ := runGate(t, probe, dir, hermeticEnv(t, goDir, t.TempDir(), t.TempDir()))
		if rc != 1 {
			t.Errorf("core-fail + optional-absent: rc=%d want 1\n%s", rc, out)
		}
		if !strings.Contains(out, "golangci-lint not installed") {
			t.Errorf("expected golangci-lint INFO skip alongside the core failure\n%s", out)
		}
	})
}
```

> **Notes for the executor:**
> - Merge the `import` block with Task 1's (single block; don't duplicate `strings`/`testing`).
> - Verify the chosen vet violation actually trips `go vet` on the installed toolchain: `cd /tmp/x && go vet ./...` on the printf-mismatch snippet should print a `Printf` diagnostic and exit non-zero. If your Go version doesn't flag it, use a different deterministic vet error (e.g. an unreachable `return`). Adjust the fixture, not the assertion.
> - If `/usr/bin`/`/bin` differ on the host (rare), derive coreutils dir dynamically; the gate needs `sed`/`awk` only on the Makefile path (cases d/f), which include `/usr/bin:/bin`.

- [ ] **Step 2: Run the full runtime test.**

Run: `go test ./pipeline/ -run 'TestRunProjectCIGateRuntime' -v`
Expected: all subtests PASS (or `t.Skip` where a tool is unavailable). If `makefile_ci_target_wins` shows `go vet` ran, the precedence is broken — fix `run_project_ci_gate`, not the test.

- [ ] **Step 3: Run the whole package + build.**

Run: `go build ./... && go test ./pipeline/ -run 'TestQualityGate|TestRunProjectCIGate' -v && go test ./... -short`
Expected: green.

- [ ] **Step 4: Commit.**

```bash
git add pipeline/build_product_quality_gate_test.go
git commit -m "test(299): runtime behavioral test for language-native quality gate

Hermetic (env -i-style PATH, GOPROXY=off, no-toolchain go.mod) source-and-run
of run_project_ci_gate across fixture repos: vet-violation→rc!=0&!=2, clean→0,
optional-absent→INFO skip & rc!=2, build-only Makefile falls through, make-ci
wins (no vet), make-missing→rc2-before-gates, polyglot runs all stacks.

Co-Authored-By: Claude Opus 4.8 (1M context) <noreply@anthropic.com>"
```

---

## Task 5: CHANGELOG

**Files:**
- Modify: `CHANGELOG.md` (`[Unreleased]` → `### Added`, after the #298 entry around `:25`).

- [ ] **Step 1: Add the entry** at the top of `### Added` under `## [Unreleased]`:

```markdown
- **build_product: language-native quality gate fallback** (closes #299, refs
  epic #308 Phase 2 — first item). Closes the no-Makefile blind spot: when a repo
  has no Makefile `ci`/`check`/`lint` target (the `code-goblin` failure mode —
  shipped with no linter ever running), `run_project_ci_gate` now falls through to
  language-native gates for **every** detected toolchain (polyglot, not
  first-match): Go `go vet ./...` (+ `golangci-lint` if present), JS/TS `tsc
  --noEmit`/`eslint`, Python `ruff`/`mypy`, Rust `cargo fmt --check`/`cargo clippy`.
  Optional linters absent → one-line INFO skip (never an error); a present gate
  that fails routes to the fix loop. The `0`/`2`/`N` return contract is preserved —
  `rc=2` stays reserved for the Makefile-present-but-`make`-missing escalation;
  language-gate failures collapse to `rc=1`. **Behavior change:** no-Makefile Go
  repos now enforce `go vet` on every milestone (previously green on tests alone).
  Both `TestMilestone` and `FinalBuild` inherit it via the shared `ci-probe.sh`
  helper. `.dip`-only change; no engine code.
```

- [ ] **Step 2: Commit.**

```bash
git add CHANGELOG.md
git commit -m "docs(299): CHANGELOG entry for language-native quality gate (#299)

Co-Authored-By: Claude Opus 4.8 (1M context) <noreply@anthropic.com>"
```

---

## Task 6: Full verification + PR

- [ ] **Step 1: dippin gates.**

```bash
dippin validate examples/build_product.dip
dippin doctor examples/build_product.dip examples/ask_and_execute.dip examples/build_product_with_superspec.dip
dippin simulate -all-paths examples/build_product.dip
```
Expected: `validate` passes; `doctor` is **A** grade across all three (build_product baseline A 90/100 — must not regress); `simulate -all-paths` reports 100% terminate success with no new cycle / dead-stop. If `dippin` is not on PATH, STOP and ask the user (they install it from a local checkout — do NOT `go install`).

- [ ] **Step 2: Go gates.**

```bash
go build ./...
go test ./... -short
gofmt -l pipeline/build_product_quality_gate_test.go   # must print nothing
```
Expected: build clean; tests green; gofmt clean.

- [ ] **Step 3: Adversarial self-review** (before pushing). Walk each explicitly:
  - **rc-contract:** grep the probe — exactly one `return 2`, anchored to `command -v make`; `run_language_native_gates` returns only via `return 1` / `return 0`.
  - **optional-absent ≠ rc2:** the runtime `optional_absent_core_fails_rc1` + `golangci_absent_info_skip` subtests cover it.
  - **polyglot-all-stacks:** `polyglot_runs_all_stacks` proves the JS stack is entered after Go.
  - **VerifyMilestone-prompt-truthful:** re-read `:655-765` — no remaining "no Makefile → skipped" claim.
  - **Makefile-path-unchanged:** `git diff main -- examples/build_product.dip` shows the awk block and `make "$TARGET"` untouched; only the two `return 0`s, the header, the new function, and the prose changed.
  - **#305 untouched:** the test-runner `if/elif` chains (`:561-578`, `:1230-1239`) are NOT in the diff.

- [ ] **Step 4: Push + open the PR.**

```bash
git push -u origin fix/299-language-native-quality-gate
gh pr create --title "feat(build_product): language-native quality gate fallback when no Makefile (#299)" --body "$(cat <<'EOF'
Closes #299. Refs epic #308 Phase 2 (first item).

## What

`run_project_ci_gate` (shared `ci-probe.sh` helper, sourced by `TestMilestone` and
`FinalBuild`) only enforced quality when a Makefile `ci`/`check`/`lint` target
existed. A single-language repo with no such target shipped green on the test
runner alone — no lint/vet/format gate ever ran (the `code-goblin` failure mode).

This adds a `run_language_native_gates` fallback, reached from both of
`run_project_ci_gate`'s former early `return 0` paths (no Makefile, **and** Makefile
with no matching target). It runs gates for **every** detected toolchain:

| Toolchain | Core (always) | Optional (run-if-present) |
|-----------|---------------|---------------------------|
| Go (`go.mod`) | `go vet ./...` | `golangci-lint run` |
| JS/TS (`package.json`) | — | `tsc --noEmit`, `eslint .` |
| Python (`pyproject.toml`) | — | `ruff check .`, `mypy .` |
| Rust (`Cargo.toml`) | — | `cargo fmt --check`, `cargo clippy -- -D warnings` |

## Design subtleties (deliberate)

- **Return-code contract preserved.** `0` clean / `2` make-missing→escalate-to-human
  / `N` failure→fix-loop. Language-gate failures **collapse to `rc=1`** — a linter
  exiting 2 can never masquerade as the make-missing escalation. `rc=2` stays
  reserved for the make-missing case (no *new* rc=2 source introduced).
- **Optional-absent is an INFO skip, never rc=2.** Escalating a human because `mypy`
  isn't installed would be wrong. Only `command -v`-guarded; absence → one-line note.
- **Polyglot — all detected stacks run** (independent `if`, not first-match). The
  adjacent test-runner `if/elif` is **#305, out of scope** and untouched here.
- **Both no-gate paths fall through** (incl. a build-only Makefile) — slightly
  exceeds the issue's literal "no Makefile" wording; closes the blind spot fully.
- **`set -eu` safe:** every gate ends in `|| LANG_RC=$?` (FinalBuild calls the gate
  bare under active `set -e`); a guard comment pins this invariant.
- **awk/make path untouched** — gates live in a new sibling helper; only the two
  `return 0` blocks, the header comment, and VerifyMilestone prose changed.
- **VerifyMilestone prose synced** — "no Makefile" no longer means "skipped."

## Out of scope / follow-up

- Pre-existing make-path rc=2 leak (`make "$TARGET"; return $?` propagates GNU
  make's native exit 2 on recipe errors): filed as **#320**. Fixing it would change
  existing Makefile-gate behavior (an acceptance criterion), so it's deliberately
  not touched here.

## Tests

- Graph string-asserts (negative-control, RED-first per the #296/#297/#298 pattern):
  `go vet`/linters/detectors present in the probe; rc=2 anchored to `command -v
  make`; gate stays centralized; VerifyMilestone prose truthful.
- Hermetic **runtime** shell test (the behavioral driver): sources the extracted
  `ci-probe.sh` under an isolated PATH/`GOPROXY=off` in fixture repos — vet
  violation→rc≠0&≠2, clean→0, optional-absent→INFO skip & rc≠2, build-only Makefile
  falls through, `make ci` wins (no vet), make-missing→rc2-before-gates, polyglot
  runs all stacks.

## Verification

`dippin validate` ✓ · `dippin doctor` A grade ✓ · `dippin simulate -all-paths` 100%
terminate ✓ · `go build ./... && go test ./... -short` ✓

🤖 Generated with [Claude Code](https://claude.com/claude-code)
EOF
)"
```

- [ ] **Step 5: Address bot review** (CodeRabbit/Copilot/Codex). Per `superpowers:receiving-code-review`: verify each comment against the actual code, fix valid ones, decline invalid ones with a technical rationale, reply in-thread, keep the PR body + CHANGELOG in sync, keep CI green. NEVER `--no-verify`.

---

## Self-review against the spec

- **Go core gate `go vet` always + golangci optional** → Task 2 Step 3 ✓
- **JS/TS/Python/Rust run-if-present** → Task 2 Step 3 ✓
- **Polyglot independent `if`** → Task 2 Step 3 + Task 4 `polyglot_runs_all_stacks` ✓
- **rc collapse to 1, never 2** → Task 2 Step 3 (`if LANG_RC != 0 → return 1`) + Task 1 Test 6 + Task 4 `make_missing_rc2`/`optional_absent_core_fails_rc1` ✓
- **Both no-gate paths fall through** → Task 2 Steps 1–2 + Task 4 `build_only_makefile_falls_through` ✓
- **Makefile path byte-for-byte** → Task 1 Test 5 + Task 6 Step 3 diff check ✓
- **Both callers inherit, no duplicate gate** → Task 1 Test 7 ✓
- **`set -eu` safe + guard comment** → Task 2 Step 3 INVARIANT comment ✓
- **No INFO line contains `escalate`/`tests-pass`** → verified in the gate text (Task 2 Step 3) ✓
- **Prose-sync all 5 sites** (header + 2 INFO strings in-gate + preamble + item-6 ×2) → Task 2 Step 4 (header + INFO strings) + Task 3 ✓
- **#305 test-runner untouched** → Task 6 Step 3 ✓
- **CHANGELOG Added, epic Phase 2, behavior-change framing** → Task 5 ✓
- **Follow-up #320 referenced** → Task 6 PR body ✓
- **dippin validate/doctor/simulate + go gates** → Task 6 Steps 1–2 ✓
