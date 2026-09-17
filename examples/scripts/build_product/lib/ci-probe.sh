# Source this file, then call `run_project_ci_gate`. Returns:
#   0  — clean: `make <TARGET>` exited 0, OR (no Makefile gate
#        ran) the language-native gates passed / no toolchain
#        was detected (issue #299).
#   1  — a gate failed (caller should fail / retry): a failing
#        `make <TARGET>` run collapses to exactly 1 — make's own
#        exit code (including its native exit 2 on ANY recipe
#        error) is never propagated (#320) — and a failing
#        language-native gate (go vet / golangci-lint / tsc /
#        eslint / ruff / mypy / cargo fmt|clippy) likewise
#        collapses to exactly 1. Never an arbitrary N, never 2.
#   2  — Makefile present but `make` not installed (caller
#        should escalate; LLM fix loop can't install a binary).
#        rc=2 is sole-sourced to the `command -v make` check —
#        no other path in this helper can produce it.
# Sets PROJECT_CI_RAN to the chosen target on a real `make` run,
# empty string otherwise (incl. the language-native fall-through).
run_project_ci_gate() {
  PROJECT_CI_RAN=""
  MAKEFILE=""
  for mf in Makefile makefile GNUmakefile; do
    if [ -f "$mf" ]; then MAKEFILE="$mf"; break; fi
  done
  if [ -z "$MAKEFILE" ]; then
    echo "INFO: no Makefile present — running language-native gates"
    run_language_native_gates
    return $?
  fi
  if ! command -v make >/dev/null 2>&1; then
    echo "ERROR: $MAKEFILE present but 'make' not installed — escalating"
    return 2
  fi
  for TARGET in ci check lint; do
    # Parse the Makefile for a top-level target named TARGET:
    #   - skip tab-prefixed recipe lines
    #   - skip variable assignments (first ':' part of := / ::= / :::=)
    #   - tokenize everything before the first ':' as the target list
    # Handles `ci:`, `ci check lint:`, `ci: VAR := overridden`,
    # rejects `ci := value` and substring collisions like `build-ci:`.
    if sed 's/#.*//' "$MAKEFILE" | awk -v t="$TARGET" '
        /^\t/ { next }
        {
          cp = index($0, ":")
          if (cp == 0) next
          if (substr($0, cp) ~ /^:+=/) next
          head = substr($0, 1, cp - 1)
          n = split(head, tokens, /[ \t]+/)
          for (i = 1; i <= n; i++) if (tokens[i] == t) { found = 1; exit }
        }
        END { exit !found }
      '; then
      echo "--- running make $TARGET (project CI gate from $MAKEFILE) ---"
      PROJECT_CI_RAN="$TARGET"
      # `|| MAKE_RC=$?` keeps a bare set -e caller (FinalBuild) from
      # aborting mid-function, mirroring the language-gate invariant.
      MAKE_RC=0
      make "$TARGET" 2>&1 || MAKE_RC=$?
      if [ "$MAKE_RC" -eq 0 ]; then return 0; fi
      # GNU make exits 2 on ANY recipe error; collapse to 1 so a
      # fixable CI failure routes to the fix loop, not human
      # escalation — rc=2 stays sole-sourced to make-missing (#320).
      return 1
    fi
  done
  echo "INFO: no project CI target in $MAKEFILE (looked for: ci, check, lint) — running language-native gates"
  run_language_native_gates
  return $?
}
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
    echo "--- go vet ./... (language-native gate) ---"
    go vet ./... 2>&1 || LANG_RC=$?
    if command -v golangci-lint >/dev/null 2>&1; then
      echo "--- golangci-lint run (language-native gate) ---"
      # Milestone-scope the lint gate (issue #436): when verify.sh set
      # LINT_NEW_FROM_REV to the milestone base, only NEW issues fail —
      # matching the milestone-scoped `go test`. FinalBuild leaves it unset
      # → whole-tree lint.
      # Lint escape hatch (issue #441): non-comment lines in
      # .ai/milestones/known_lint_failures become --exclude patterns, so an
      # operator can suppress a lint failure the milestone can't fix (the
      # go-test hatch known_failures can't). Patterns are operator-authored
      # and only ever passed as golangci-lint args — never eval'd.
      LINT_EXCLUDES=""
      if [ -f .ai/milestones/known_lint_failures ]; then
        while IFS= read -r pat || [ -n "$pat" ]; do
          case "$pat" in ''|\#*) continue ;; esac
          LINT_EXCLUDES="$LINT_EXCLUDES --exclude $pat"
        done < .ai/milestones/known_lint_failures
      fi
      golangci-lint run ${LINT_NEW_FROM_REV:+--new-from-rev "$LINT_NEW_FROM_REV"} $LINT_EXCLUDES 2>&1 || LANG_RC=$?
    else
      echo "WARNING: golangci-lint not installed — lint enforcement is DISABLED for this run."
      echo "WARNING: install a pinned golangci-lint for reproducible gating (results may differ across environments)."
    fi
  fi
  # JS/TS — run-if-present AND only if the project opted into the tool
  # (config file present). A plain-JS repo (package.json, no tsconfig /
  # no eslint config) must NOT fail just because a global tsc/eslint is on
  # PATH: bare `tsc --noEmit` exits non-zero on a project with no tsconfig,
  # and `eslint .` errors with no config — gating on PATH alone would
  # mis-route such a repo to the fix loop (Codex PR #321 P2, #299).
  if [ -f package.json ]; then
    RAN_ANY=1
    if [ -f tsconfig.json ]; then
      if command -v tsc >/dev/null 2>&1; then
        echo "--- tsc --noEmit (language-native gate) ---"
        tsc --noEmit 2>&1 || LANG_RC=$?
      else
        echo "INFO: tsc not installed — skipping (optional)"
      fi
    else
      echo "INFO: no tsconfig.json — skipping tsc (not a TypeScript project)"
    fi
    ESLINT_CFG=""
    for ec in eslint.config.js eslint.config.mjs eslint.config.cjs eslint.config.ts .eslintrc.js .eslintrc.cjs .eslintrc.yaml .eslintrc.yml .eslintrc.json .eslintrc; do
      if [ -f "$ec" ]; then ESLINT_CFG="$ec"; break; fi
    done
    if [ -z "$ESLINT_CFG" ] && grep -q '"eslintConfig"' package.json 2>/dev/null; then
      ESLINT_CFG="package.json"
    fi
    if [ -n "$ESLINT_CFG" ]; then
      if command -v eslint >/dev/null 2>&1; then
        echo "--- eslint . (language-native gate) ---"
        eslint . 2>&1 || LANG_RC=$?
      else
        echo "INFO: eslint not installed — skipping (optional)"
      fi
    else
      echo "INFO: no eslint config — skipping eslint (not configured)"
    fi
  fi
  # Python — both run-if-present.
  if [ -f pyproject.toml ]; then
    RAN_ANY=1
    if command -v ruff >/dev/null 2>&1; then
      echo "--- ruff check . (language-native gate) ---"
      ruff check . 2>&1 || LANG_RC=$?
    else
      echo "INFO: ruff not installed — skipping (optional)"
    fi
    if command -v mypy >/dev/null 2>&1; then
      echo "--- mypy . (language-native gate) ---"
      mypy . 2>&1 || LANG_RC=$?
    else
      echo "INFO: mypy not installed — skipping (optional)"
    fi
  fi
  # Rust — run-if-present (cargo gates both fmt and clippy).
  if [ -f Cargo.toml ]; then
    RAN_ANY=1
    if command -v cargo >/dev/null 2>&1; then
      # rustfmt/clippy are separate toolchain components — `cargo` can be
      # present without them. A missing component is a tooling absence
      # (same class as an optional tool not installed), not a code defect,
      # so gate each subcommand on its own availability (#299).
      if cargo fmt --version >/dev/null 2>&1; then
        echo "--- cargo fmt --check (language-native gate) ---"
        cargo fmt --check 2>&1 || LANG_RC=$?
      else
        echo "INFO: cargo fmt (rustfmt) not installed — skipping (optional)"
      fi
      if cargo clippy --version >/dev/null 2>&1; then
        echo "--- cargo clippy -- -D warnings (language-native gate) ---"
        cargo clippy -- -D warnings 2>&1 || LANG_RC=$?
      else
        echo "INFO: cargo clippy not installed — skipping (optional)"
      fi
    else
      echo "INFO: cargo not installed — skipping (optional)"
    fi
  fi
  if [ -z "$RAN_ANY" ]; then
    echo "INFO: no recognized toolchain (go.mod/package.json/pyproject.toml/Cargo.toml) — no language-native gate"
  fi
  if [ "$LANG_RC" -ne 0 ]; then
    return 1
  fi
  return 0
}
