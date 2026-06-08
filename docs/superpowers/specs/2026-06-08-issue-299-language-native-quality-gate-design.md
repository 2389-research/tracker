# Issue #299 — Language-native quality gate fallback when no Makefile exists

**Epic:** #308 Phase 2 ("Enforce quality regardless of spec/language"), first item.
**Scope:** `.dip`-only change to `examples/build_product.dip` + one new test file + CHANGELOG. No engine Go changes.
**Status:** design (squad-reviewed 2026-06-08; five-expert panel consensus folded in).

## Problem

`run_project_ci_gate` in the `.ai/build/ci-probe.sh` heredoc (Setup node, `examples/build_product.dip:51-92`) only enforces quality when a Makefile with a top-level `ci`/`check`/`lint` target exists. A single-language repo with **no such Makefile target** gets the early `return 0` (lines `:57-60` no-Makefile; `:90-91` Makefile-but-no-matching-target) and the milestone ships green on the language test runner (`go test ./...`) alone — no lint/vet/format gate ever runs. This is the `code-goblin` failure mode: a Go repo with no `.golangci.yml` and an unguarded complexity ceiling, because nothing ran a linter.

The helper is sourced by two callers — `TestMilestone` (`:587-589`, branches on `CI_RC`) and `FinalBuild` (`:1247-1248`, propagates via `set -eu`) — so a single change to the helper fixes both.

## The change

Replace the two early `return 0` skip paths with a call to a **new sibling helper `run_language_native_gates`** (defined in the same `ci-probe.sh` heredoc). The Makefile branch — detection loop, `make`-missing rc=2, the awk target-parser, and `make "$TARGET"; return $?` — is preserved **byte-for-byte**; only the two `return 0` blocks change to `run_language_native_gates; return $?` (mirroring the existing `make "$TARGET" 2>&1; return $?` idiom, which has identical `set -e` semantics under both callers). Extracting the gates into their own function leaves the awk untouched and makes the block independently unit-testable — a refinement of the reviewed inline sketch with identical runtime behavior.

### Return-code contract (load-bearing — unchanged)

```
0  — clean (Makefile target ran clean, OR language-native gates passed, OR nothing to gate)
2  — Makefile present but `make` not installed (escalate to human; LLM can't install a binary)
N  — gate failure (route to the fix loop)
```

The language-native block **collapses every gate failure to `return 1`** — never 2:

```sh
if [ "$LANG_RC" -ne 0 ]; then return 1; fi
return 0
```

This is mandatory, not stylistic: `TestMilestone:590` treats `CI_RC -eq 2` as *exactly* "make missing → escalate, reset attempts, skip the fix loop." A linter that exits 2 (golangci-lint/mypy/tsc can on internal/config errors) must NOT masquerade as that. Using `return "$LANG_RC"` (raw tool code) would leak a 2 — forbidden.

### Toolchain gates (polyglot — run ALL detected, not first-match)

Independent `if` blocks (not `elif`). Each runs if its project file is present:

| Toolchain | Detect | Gates |
|-----------|--------|-------|
| Go | `go.mod` | `go vet ./...` (**always** — core; go is present by go.mod detection); `golangci-lint run` *if on PATH* |
| JS/TS | `package.json` | `tsc --noEmit` *if on PATH*; `eslint .` *if on PATH* |
| Python | `pyproject.toml` | `ruff check .` *if on PATH*; `mypy .` *if on PATH* |
| Rust | `Cargo.toml` | `cargo fmt --check` + `cargo clippy -- -D warnings` *if cargo on PATH* |

**Core vs optional** (issue subtlety 2): "core" means *when the tool runs, its failure is fatal* — NOT that its absence is fatal. `go vet` is the only unguarded gate (runs whenever `go.mod` exists). Every other tool is `command -v`-guarded: **absent → one-line INFO skip (never rc=2, never a failure); present-and-failing → accumulate into `LANG_RC` and propagate**. This applies uniformly to every run gate, so the only thing the core/optional distinction governs in code is whether `go vet` carries a `command -v` guard (it does not).

### Both no-gate paths fall through (decided)

The fallback fires in **both** "no Makefile at all" *and* "Makefile present but no `ci`/`check`/`lint` target." A repo with a build-only Makefile still has no lint gate; running language-native gates there closes the same blind spot. This slightly exceeds the issue's literal "no Makefile" wording — flagged in the PR body. A real `ci`/`check`/`lint` target still wins and short-circuits via the unchanged `make "$TARGET"; return $?` path.

### Proposed helper body (POSIX sh, matching existing style)

```sh
run_project_ci_gate() {
  PROJECT_CI_RAN=""; MAKEFILE=""
  for mf in Makefile makefile GNUmakefile; do
    if [ -f "$mf" ]; then MAKEFILE="$mf"; break; fi
  done
  if [ -n "$MAKEFILE" ]; then
    if ! command -v make >/dev/null 2>&1; then
      echo "ERROR: $MAKEFILE present but 'make' not installed — escalating"
      return 2                              # ONLY source of rc=2 — unchanged
    fi
    for TARGET in ci check lint; do
      if <existing sed|awk target-parse — UNCHANGED>; then
        echo "--- running make $TARGET (project CI gate from $MAKEFILE) ---"
        PROJECT_CI_RAN="$TARGET"
        make "$TARGET" 2>&1; return $?       # Makefile WINS — unchanged
      fi
    done
    echo "INFO: no project CI target in $MAKEFILE — running language-native gates (#299)"
  else
    echo "INFO: no Makefile present — running language-native gates (#299)"
  fi

  # ── language-native quality-gate fallback (#299) ──
  # Every gate MUST end in `|| LANG_RC=$?` — a bare failing gate would abort the
  # node under `set -e` (these nodes run `set -eu`). Optional tools are skipped
  # via `command -v`; rc=2 is reserved for the make-missing case above only.
  LANG_RC=0; RAN_ANY=""
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
  if [ "$LANG_RC" -ne 0 ]; then return 1; fi
  return 0
}
```

Constraints honored: no `|| true` (failures surface); no INFO line contains the literal tokens `escalate` or `tests-pass` (the edges substring-match `ctx.tool_stdout`); the helper prints no routing marker (callers print `tests-pass`/`escalate`/`final-build-pass` LAST, surviving the 64KB tail cap); `LANG_RC` holds the last failing code but the caller only branches on zero/non-zero. `LANG_RC`/`RAN_ANY` leak into the sourcing shell (consistent with the existing `MAKEFILE`/`TARGET`/`PROJECT_CI_RAN` leaks); neither caller references those names (verified).

## Prose-sync edits (subtlety 5 — keep doc-in-prompt honest)

After this change, "no Makefile" / "no matching target" no longer means "skipped." Every site asserting otherwise becomes false and must be corrected:

1. **Helper header `:42-50`** — rewrite the `Returns:` block: rc=0 now includes "language-native gates passed / no toolchain detected," and "no Makefile / no matching target" can now return 1.
2. **In-function INFO strings `:58`, `:90`** — "no project CI gate to run" / "no project CI target" become "running language-native gates" (already in the proposed body above).
3. **VerifyMilestone preamble `:655-658`** — "OR the probe correctly skipped (no Makefile, or no matching target)" → the no-gate paths now run language-native gates; valid-skip is now "no recognized toolchain."
4. **VerifyMilestone item 6 `:745-754`** — "passed-or-was-validly-skipped" / "validly skipped" rules updated to match (3).
5. **VerifyMilestone item 6 cause description `:759-765`** — the "project CI gate" is no longer Makefile-only; note language-native gate failures fold into `TEST_EXIT` → FixMilestone (routing unchanged; only the cause text broadens).

Optional one-liners (truthful-but-understated, low priority): TestMilestone comment `:544-551`, FinalBuild comment `:1241-1246`.

## Out of scope (deliberate)

- **Pre-existing make-path rc=2 leak.** GNU `make` exits 2 on any recipe error, so the *untouched* `make "$TARGET"; return $?` path already routes ordinary `make ci` failures to escalate-to-human rather than the fix loop. Fixing it would change "existing Makefile-gate behavior," violating an acceptance criterion. **Decline inline; file a follow-up issue.** This spec claims only that the language-native block introduces **no new rc=2 source** — not that rc=2 is globally sole-sourced.
- **Missing core toolchain** (`go.mod` present but `go` binary absent) returns 1 → fix loop, not rc=2. Accepted per #299's explicit "rc=2 = make-missing only" and because TestMilestone's `go build`/`go test` runs *before* the gate and fails first. Documented, not changed.
- **Test-runner if/elif first-match** (`:561-578`, `:1230-1239`) — that is #305 (polyglot test runner), Phase 3. This change touches only the gate, never the test runner. The asymmetry (gate uses independent `if`, runner stays `elif`) is intentional for this issue.

## Tests (TDD, negative-control RED-first, per the #296/#297/#298 precedent)

New file `pipeline/build_product_quality_gate_test.go`, package `pipeline`, using the existing `loadBuildProduct(t)` helper.

### Layer A — graph string assertions (read `Setup.Attrs["tool_command"]`)

Each assertion labeled in a comment as **negative-control** (RED pre-#299) or **regression-pin** (GREEN, guards removal). Verified absent from the Setup node pre-change (count=0): `go vet`, `golangci-lint`, `ruff`, `eslint`, `tsc`, `mypy`, `cargo clippy`, `cargo fmt`, and the `package.json`/`pyproject.toml`/`Cargo.toml` detectors (these live in the test-runner elif, *not* in Setup's heredoc — so asserting their presence in Setup IS a valid driver).

- **Negative-controls:** `go vet ./...`, `golangci-lint run`, `ruff check`, `eslint`, `tsc --noEmit`, `mypy`, `cargo clippy`, `cargo fmt --check`, and the three detectors present in Setup's heredoc.
- **Regression-pins:** `make "$TARGET"` and the awk parse survive; `return 2` appears exactly once **and is adjacent to `command -v make`** (semantic pin, not just count — assert `command -v make` precedes the sole `return 2` and no `return 2` follows any `language-native gate` marker); both `TestMilestone` and `FinalBuild` still `. .ai/build/ci-probe.sh` and call `run_project_ci_gate` (gate stays centralized — stronger than "no `go vet` in callers").
- **Prose negative-control:** VerifyMilestone prompt no longer contains "correctly skipped (no Makefile".

### Layer B — runtime shell test (the behavioral driver)

Extract the heredoc body from the **loaded** (dippin-dedented) Setup `tool_command` — between `<<'PROBE_EOF'\n` and `\nPROBE_EOF` — write to a temp file, and source it. `t.Skip` when `go` or `sh` is unavailable. Run each case under hermetic env:

```
env -i  PATH=$(dirname $(command -v go)):/usr/bin:/bin  HOME=<t.TempDir>  GOCACHE=<t.TempDir>  GOPROXY=off
```

Fixture `go.mod` uses `go 1.22` with **no `toolchain` directive** and zero deps (so `GOPROXY=off` never resolves). No `git` needed (the gate doesn't use it). The restricted PATH deterministically removes golangci-lint/ruff/etc.

Cases:
- (a) Go repo with a `go vet` violation → rc≠0 **and** rc≠2.
- (b) clean Go repo → rc=0.
- (c) golangci-lint absent (restricted PATH) → output has the INFO skip line, rc≠2.
- (d) Makefile with a `ci:` target (+ a go.mod vet violation) → make wins: `PROJECT_CI_RAN=ci`, rc reflects make, and the `go vet` marker is **absent** (proves precedence — gate didn't run).
- (e) empty dir → rc=0 + "no recognized toolchain".
- (f) **build-only Makefile (no ci/check/lint) + go.mod vet violation → rc≠0 AND `PROJECT_CI_RAN=""`** — the key both-paths case.
- (g) optional tool absent + core gate fails simultaneously → rc=1, not 2 (+ INFO skip line).
- (h) Makefile present, `make` uninstalled (PATH without make, with go) → rc=2 still fires *before* any language gate.
- (i) polyglot proof: clean Go + `package.json` → output contains **both** the `go vet` marker AND a node-stack INFO/skip line (proves not first-match), rc=0.

No coverage/complexity-hook risk: the complexity gate excludes `_test.go`; coverage is aggregate and the new test is additive; both layers assert on embedded `.dip` data, not new production Go.

## Verification

- `dippin validate examples/build_product.dip` — passes.
- `dippin doctor examples/build_product.dip examples/ask_and_execute.dip examples/build_product_with_superspec.dip` — A grade across the board (build_product baseline A 90/100).
- `dippin simulate -all-paths examples/build_product.dip` — 100% terminate success, no new cycle / dead-stop.
- `go build ./... && go test ./... -short` — green; each new test proven RED before its wiring.
- CHANGELOG.md `[Unreleased]/Added` — language-native quality gate fallback closing the no-Makefile blind spot; epic #308 Phase 2; framed as a behavior change (no-Makefile repos now enforce `go vet`).

## Follow-up issue (filed: #320)

Pre-existing make-path rc=2 leak: `make "$TARGET"; return $?` (`:86-87`) propagates GNU make's native exit 2 on ordinary recipe failures, so a normal `make ci` lint failure escalates-to-human instead of routing to the fix loop, contradicting the contract VerifyMilestone's prose describes. Out of scope for #299 (fixing it changes Makefile-gate behavior). Tracked in **#320**.
