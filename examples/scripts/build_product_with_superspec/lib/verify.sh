# Run this directly (`sh .ai/build/verify.sh [--final]`) — do NOT source
# it. It sources .ai/build/ci-probe.sh internally (stack detection + the
# project CI gate). Exit:
#   0  green: a REAL oracle ran and passed — build + every detected stack's
#      tests with a POSITIVE executed-test count, or the project's own
#      Makefile ci/check/lint/test target. Deny-by-default (tracker-runner
#      #857/#873): a manifest whose suite ran ZERO tests is not green (`go
#      test` / `npm test` / `cargo test` exit 0 on an empty suite; pytest
#      exits 5), and "no manifest found" is not green either — see exit 3.
#   1  any build/test/CI failure (normal fix-loop failure), a missing
#      ci-probe.sh, an unusable known_failures entry, or (--final only) no
#      runnable oracle without the operator opt-out. Every non-zero
#      TEST-runner exit collapses to 1 (a pytest collection error exiting 2
#      is a failure like any other). There is NO semantic exit code for the
#      environment case: Makefile present but `make` missing is signalled
#      by ci-probe.sh printing `_TRACKER_CI_MAKE_MISSING` and creating
#      .ai/build/ci-make-missing (#640 E8) — TestMilestone keys its
#      escalate route on that file.
#   3  NOT-YET-VERIFIABLE (milestone mode only): the build is green as far
#      as it goes but NO runnable oracle ran — no language test suite
#      executed a positive number of tests and no project CI target ran
#      (a milestone-1 tree before packaging/tests exist, a scaffolding or
#      docs milestone, a suite the runner couldn't collect). NOT a green
#      (absence of an oracle is not a pass) and NOT a failure that discards
#      the work — TestMilestone routes it to the milestone verifier (the
#      independent judge) via the `tests-not-yet-verifiable` marker.
#
# Modes:
#   (default) milestone gate — Go tests are scoped to the packages this
#     milestone touched (#392, #640 D2/D3/D9: base→WORKTREE incl. uncommitted
#     and untracked files, plus every package that depends on them, filtered
#     through `go list -e`); known_failures entries are skipped (Go `-skip`,
#     pytest `-k`); no oracle → exit 3.
#   --final  ship gate (FinalBuild) — whole tree, `go test -count=1`,
#     known_failures IGNORED (the still-listed entries are printed as the
#     reason a red run is red), no oracle → exit 1 unless the operator stamp
#     .ai/build/no-tests-ok is present, a Go stack with zero test files is a
#     FAILURE (#640 D7), elapsed seconds printed per stack (#640 D13).
set -eu
VERIFY_MODE=milestone
[ "${1:-}" = "--final" ] && VERIFY_MODE=final
TEST_EXIT=0

[ -f .ai/build/ci-probe.sh ] || { echo "ERROR: .ai/build/ci-probe.sh missing — Setup did not run (or Cleanup removed .ai/build/); cannot adjudicate green"; exit 1; }
. .ai/build/ci-probe.sh
rm -f .ai/build/ci-make-missing

# --- known_failures → `go test -skip` pattern + pytest `-k` names ----------
# (Go: #640 D7/D12; pytest: tracker-runner fix set #3.) Each Go entry is
# anchored PER PATH SEGMENT — `TestA` becomes `^TestA$` (no longer also
# skipping TestAB / TestAlpha), `TestA/sub` becomes `^TestA$/^sub$` (only
# that subtest) — and the alternatives are joined with a top-level `|`,
# which `go test` splits into independent skip filters. The assembled regex
# is validated (`grep -E`) so a malformed entry fails closed instead of
# silently skipping nothing or everything. The same entries become a pytest
# `-k "not (A or B)"` deselection (KF_NAMES, one per line); an entry with a
# character outside [A-Za-z0-9_./:-] is left out of the -k expression (it
# would be read as an expression operator) with a WARNING.
SKIP_PATTERN=""
KF_NAMES=""
if [ "$VERIFY_MODE" = milestone ] && [ -f .ai/milestones/known_failures ]; then
  KF_TMP=$(mktemp)
  hatch_lines .ai/milestones/known_failures "$KF_TMP"
  while IFS= read -r name; do
    [ -n "$name" ] || continue
    case "$name" in
      *[[:space:]]*) echo "WARNING: ignoring known_failures entry '$name' — one test name per line, no spaces"; continue ;;
    esac
    anchored="^$(printf '%s' "$name" | sed 's|/|$/^|g')\$"
    GRC=0
    printf '\n' | grep -E -e "$anchored" >/dev/null 2>&1 || GRC=$?
    if [ "$GRC" -ge 2 ]; then
      echo "ERROR: known_failures entry '$name' is not a valid regular expression — fix or remove it (.ai/milestones/known_failures)"
      rm -f "$KF_TMP"
      exit 1
    fi
    SKIP_PATTERN="${SKIP_PATTERN:+$SKIP_PATTERN|}$anchored"
    case "$name" in
      *[!A-Za-z0-9_./:-]*) echo "WARNING: known_failures entry '$name' is not usable as a pytest -k name (only [A-Za-z0-9_./:-]) — applied to Go only" ;;
      *) KF_NAMES="${KF_NAMES:+$KF_NAMES
}$name" ;;
    esac
  done < "$KF_TMP"
  rm -f "$KF_TMP"
fi
if [ "$VERIFY_MODE" = final ] && [ -f .ai/milestones/known_failures ]; then
  KF_TMP=$(mktemp)
  hatch_lines .ai/milestones/known_failures "$KF_TMP"
  if [ -s "$KF_TMP" ]; then
    echo "--- NOTE: known_failures is IGNORED by the ship gate — these entries still listed must pass (#640 D12) ---"
    sed 's/^/  still listed: /' "$KF_TMP"
    echo "--- (a red run below is expected if any of them still fails; remove entries once their tests pass) ---"
  fi
  rm -f "$KF_TMP"
fi

# --- milestone base (milestone mode only) ------------------------------------
# Degrade to the empty tree when the start SHA is absent or unreachable (a
# retry rewrote/orphaned it). With a real base the lint gate is scoped too
# (#436) — ci-probe.sh reads LINT_NEW_FROM_REV to pass --new-from-rev; the
# empty-tree fallback is not a rev golangci-lint accepts, so it stays unset.
MS_BASE=""
if [ "$VERIFY_MODE" = milestone ]; then
  MS_START=$(cat .ai/build/milestone-start-sha 2>/dev/null || true)
  if [ -n "$MS_START" ] && git cat-file -e "${MS_START}^{commit}" 2>/dev/null; then
    MS_BASE="$MS_START"
    LINT_NEW_FROM_REV="$MS_START"
    export LINT_NEW_FROM_REV
  elif git rev-parse --is-inside-work-tree >/dev/null 2>&1; then
    MS_BASE=$(git hash-object -t tree /dev/null)
  fi
fi

# go_scope_targets — set GO_TEST_TARGET for the Go module in the CURRENT
# directory (milestone mode): the packages holding every .go file changed
# since MS_BASE in the WORKTREE (committed or not — deletions included, so a
# removed file still scopes its package) plus untracked .go files (#640
# D2), each filtered through `go list -e` so a non-package (testdata/,
# _gen/, a build-tag-excluded file, a nested module) never yields a fake red
# (#640 D9), plus every package in this module whose dependency closure
# touches a changed package (#640 D3). Bounded: an unreadable `go list` or
# a closure over GO_SCOPE_MAX packages falls back to ./... with a note.
GO_SCOPE_MAX=64
go_scope_targets() {
  GO_TEST_TARGET="./..."
  [ -n "$MS_BASE" ] || { echo "--- not a git repo / no base — testing ./... ---"; return 0; }
  CHANGED_FILES=$( {
      git diff --relative --name-only "$MS_BASE" -- . 2>/dev/null
      git ls-files --others --exclude-standard -- '*.go' 2>/dev/null
    } | grep -E '\.go$' | sort -u)
  if [ -z "$CHANGED_FILES" ]; then
    echo "--- no changed Go files in milestone range — testing ./... ---"
    return 0
  fi
  # File → directory, dropping testdata/, _*/ and .*/ segments (never
  # packages for ./...; explicit paths would resolve them). No awk: BSD awk
  # rejects an unescaped / inside a bracket expression (#640 E1).
  CHANGED_DIRS=""
  OLD_IFS=$IFS; IFS='
'
  for f in $CHANGED_FILES; do
    case "$f" in */*) rel="${f%/*}" ;; *) rel="" ;; esac
    case "/$rel/" in */testdata/*|*/_*|*/.*) continue ;; esac
    if [ -n "$rel" ]; then d="./$rel"; else d="."; fi
    CHANGED_DIRS="$CHANGED_DIRS
$d"
  done
  IFS=$OLD_IFS
  CHANGED_DIRS=$(printf '%s\n' "$CHANGED_DIRS" | grep . | sort -u)
  # shellcheck disable=SC2086  # one ./dir token per line, no globs
  CHANGED_PKGS=$(go list -e -f '{{if not .Error}}{{.ImportPath}}{{end}}' $CHANGED_DIRS 2>/dev/null | grep . | sort -u || true)
  if [ -z "$CHANGED_PKGS" ]; then
    echo "--- changed Go files are not in any buildable package (testdata/, build-tag-excluded, nested module) — testing ./... ---"
    return 0
  fi
  DEPS_TMP=$(mktemp)
  if ! go list -f '{{.ImportPath}} {{join .Deps " "}} {{join .TestImports " "}} {{join .XTestImports " "}}' ./... > "$DEPS_TMP" 2>/dev/null; then
    rm -f "$DEPS_TMP"
    echo "--- go list ./... failed — cannot compute dependents; testing ./... ---"
    return 0
  fi
  CHANGED_SET=" $(printf '%s' "$CHANGED_PKGS" | tr '\n' ' ') "
  DEPENDENTS=$(awk -v changed="$CHANGED_SET" '{ for (i = 2; i <= NF; i++) if (index(changed, " " $i " ")) { print $1; break } }' "$DEPS_TMP")
  rm -f "$DEPS_TMP"
  SCOPE=$(printf '%s\n%s\n' "$CHANGED_PKGS" "$DEPENDENTS" | grep . | sort -u)
  NSCOPE=$(printf '%s\n' "$SCOPE" | grep -c .)
  if [ "$NSCOPE" -gt "$GO_SCOPE_MAX" ]; then
    echo "--- milestone scope is $NSCOPE packages (> $GO_SCOPE_MAX) — testing ./... ---"
    return 0
  fi
  GO_TEST_TARGET=$(printf '%s\n' "$SCOPE" | paste -sd' ' -)
  NDEP=$(printf '%s\n' "$DEPENDENTS" | grep -c . || true)
  echo "--- milestone-scoped go test ($NSCOPE package(s), $NDEP via reverse deps): $GO_TEST_TARGET ---"
}

# go_has_tests TARGETS... — true when any target package has test files.
go_has_tests() {
  # shellcheck disable=SC2086  # caller passes the space-separated target list
  go list -f '{{if or .TestGoFiles .XTestGoFiles}}1{{end}}' "$@" 2>/dev/null | grep -q 1
}

# mark_tests_ran COUNT — record that a language suite executed a POSITIVE
# number of tests (the deny-by-default verification signal read in the
# verdict below, tracker-runner #873). A stack runs in its own subshell, so
# the signal is a file, like GO_TESTS_SEEN.
mark_tests_ran() {
  if [ "${1:-0}" -gt 0 ] 2>/dev/null; then : > "$TESTS_RAN"; fi
}

# run_stack KIND DIR — build + test one stack in its own directory. Every
# failure returns 1 (never the runner's raw exit). Prints elapsed seconds
# in final mode so an operator can size the node timeouts (#640 D13).
run_stack() {
  KIND=$1; DIR=$2
  T0=$(date +%s)
  echo "=== stack: $KIND in $DIR ==="
  RC=0
  run_stack_"$KIND" "$DIR" </dev/null || RC=1
  if [ "$VERIFY_MODE" = final ]; then
    echo "=== stack: $KIND in $DIR — $(( $(date +%s) - T0 ))s, $( [ "$RC" -eq 0 ] && echo PASS || echo FAIL ) ==="
  fi
  return "$RC"
}
# Go: `-v` so executed tests are countable — '=== RUN' fires once per test
# (and subtest); a zero-test run prints none, so the suite does not count as
# an oracle (#873). The output goes through a file so the count is taken
# from exactly what was printed.
run_stack_go() {
  (
    cd "$1" || exit 1
    go build ./... 2>&1 || exit 1
    GO_OUT=$(mktemp) || exit 1
    GRC=0
    if [ "$VERIFY_MODE" = final ]; then
      # Product-wide zero-tests check (#640 D7): record that SOME Go stack
      # has tests; the ship gate fails after the sweep if none did.
      : > "$GO_STACK_SEEN"
      if go_has_tests ./...; then : > "$GO_TESTS_SEEN"; else echo "NOTE: no Go test files in $1"; fi
      go test -v -count=1 ./... > "$GO_OUT" 2>&1 || GRC=1
    else
      go_scope_targets
      # shellcheck disable=SC2086
      if ! go_has_tests $GO_TEST_TARGET; then
        echo "NOTE: no Go test files in scope ($GO_TEST_TARGET) — go test proves only that the code compiles; with no other oracle this milestone is NOT-YET-VERIFIABLE (the milestone verifier decides whether its done-when needs tests)."
      fi
      if [ -n "$SKIP_PATTERN" ]; then
        echo "--- skipping known failures: $SKIP_PATTERN ---"
        # shellcheck disable=SC2086  # package tokens from go list, never eval'd
        go test -v $GO_TEST_TARGET -skip "$SKIP_PATTERN" > "$GO_OUT" 2>&1 || GRC=1
      else
        # shellcheck disable=SC2086
        go test -v $GO_TEST_TARGET > "$GO_OUT" 2>&1 || GRC=1
      fi
    fi
    cat "$GO_OUT"
    mark_tests_ran "$(grep -c '^=== RUN' "$GO_OUT" 2>/dev/null || true)"
    rm -f "$GO_OUT"
    exit "$GRC"
  )
}
# npm: no portable skip-by-test-name, so known_failures cannot deselect a JS
# test (a deferred JS test should be marked pending in-suite). The
# executed-test count is parsed from the common reporters (max match); an
# unrecognized reporter yields 0 → not an oracle (deny-by-default).
run_stack_npm() {
  (
    cd "$1" || exit 1
    JS_OUT=$(mktemp) || exit 1
    JRC=0
    npm test > "$JS_OUT" 2>&1 || JRC=1
    cat "$JS_OUT"
    # (each probe ends in `|| true`: this subshell inherits set -e, and a
    # reporter that doesn't match one pattern must not abort the others)
    JS_TESTS_RUN=$(
      {
        # jest:   "Tests:  1 failed, 3 passed, 4 total"
        grep -oE 'Tests:.*[0-9]+ total' "$JS_OUT" | grep -oE '[0-9]+ total' | grep -oE '^[0-9]+' || true
        # vitest: "Tests  3 passed (3)"  /  "Tests  2 failed | 1 passed (3)"
        grep -oE 'Tests +[0-9].*\([0-9]+\)' "$JS_OUT" | grep -oE '\([0-9]+\)$' | grep -oE '[0-9]+' || true
        # mocha:  "3 passing"
        grep -oE '[0-9]+ passing' "$JS_OUT" | grep -oE '^[0-9]+' || true
        # node:test / TAP: "# tests 4"
        grep -oE '^# tests [0-9]+' "$JS_OUT" | grep -oE '[0-9]+$' || true
      } 2>/dev/null | sort -n | tail -1
    )
    mark_tests_ran "${JS_TESTS_RUN:-0}"
    rm -f "$JS_OUT"
    exit "$JRC"
  )
}
# python — DIR holds a pyproject.toml (manifest path) or is the tree root
# of a manifest-free suite (`.` — see the detection below). Interpreter
# chain (tracker-runner fix set #5, no version literals): a pinned .venv
# (e.g. built by EnsureEnv's bootstrap hook) wins, then a venv, then a PATH
# pytest, then uv as the last resort. UV_FROZEN is computed BEFORE the chain
# so the uv branch never reads an unset variable under `set -u` (a bug the
# runner fixed). Manifest path: attempt even when no runner pre-verifies
# (uv is the fallback), so a broken env surfaces as a failure, not a silent
# skip. Manifest-free path: run ONLY when a runner is genuinely importable —
# test files with no runner are not-yet-verifiable, never a green.
# known_failures are deselected via `-k "not (A or B)"`. pytest exit 5 (no
# tests collected) is neither green nor red: the suite is not an oracle.
run_stack_python() {
  (
    cd "$1" || exit 1
    PYRUN=""
    UV_FROZEN=""; [ -f uv.lock ] && UV_FROZEN="--frozen"   # a verify step must not rewrite the lockfile
    if [ -x .venv/bin/python ]; then PYRUN=".venv/bin/python -m pytest"
    elif [ -x venv/bin/python ]; then PYRUN="venv/bin/python -m pytest"
    elif command -v pytest >/dev/null 2>&1; then PYRUN="pytest"
    elif command -v uv >/dev/null 2>&1; then PYRUN="uv run $UV_FROZEN pytest"; fi
    if [ -f pyproject.toml ]; then
      [ -z "$PYRUN" ] && PYRUN="uv run $UV_FROZEN pytest"
    elif [ -z "$PYRUN" ] || ! $PYRUN --version >/dev/null 2>&1; then
      echo "INFO: python test files present but no importable pytest runner (.venv/venv/pytest/uv) — suite not run"
      exit 0
    fi
    echo "--- $PYRUN ($1) ---"
    PRC=0
    if [ -n "$KF_NAMES" ]; then
      K_EXPR=$(printf '%s\n' "$KF_NAMES" | paste -sd'|' - | sed 's/|/ or /g')
      echo "--- skipping known failures (pytest -k): not ($K_EXPR) ---"
      $PYRUN -k "not ($K_EXPR)" 2>&1 || PRC=$?
    else
      $PYRUN 2>&1 || PRC=$?
    fi
    if [ "$PRC" -eq 5 ]; then
      echo "NOTE: pytest collected no tests (exit 5) — the suite is not an oracle for this run"
      exit 0
    fi
    [ "$PRC" -eq 0 ] || exit 1
    mark_tests_ran 1
  )
}
# cargo: no portable skip-by-name either (a deferred Rust test should be
# #[ignore]'d). Executed tests are summed across every test binary's
# "test result: ok. N passed" summary line.
run_stack_cargo() {
  (
    cd "$1" || exit 1
    RS_OUT=$(mktemp) || exit 1
    CRC=0
    cargo test > "$RS_OUT" 2>&1 || CRC=1
    cat "$RS_OUT"
    RUST_TESTS_RUN=$(grep -oE 'test result:[^0-9]*[0-9]+ passed' "$RS_OUT" 2>/dev/null \
      | grep -oE '[0-9]+ passed' | grep -oE '^[0-9]+' \
      | awk '{s+=$1} END{print s+0}')
    mark_tests_ran "${RUST_TESTS_RUN:-0}"
    rm -f "$RS_OUT"
    exit "$CRC"
  )
}

# --- run EVERY detected stack (#305, #640 D1) ---------------------------------
# A failure is sticky (TEST_EXIT=1): a later passing stack can't mask it,
# and every stack still runs so one pass shows all the results.
STACKS_TMP=$(mktemp)
GO_STACK_SEEN="$STACKS_TMP.go"; GO_TESTS_SEEN="$STACKS_TMP.gotests"; TESTS_RAN="$STACKS_TMP.ran"
detect_stacks > "$STACKS_TMP"
# Manifest-free Python suite (tracker-runner #857): pytest DISCOVERS
# test_*.py / *_test.py without any packaging manifest, so a milestone-1
# greenfield (slugify.py + tests/, no pyproject.toml yet) is a REAL oracle
# that manifest-only detection threw away (run_045e95e failed a correct
# milestone whose 4 tests passed). When test files exist and NO pyproject
# stack was detected, run pytest once from the tree root. Go/JS/Rust need
# their manifest to run at all, so only Python has a manifest-free path.
if ! grep -q '^python' "$STACKS_TMP"; then
  py_test_hit=$(find . \
    \( -path './.git' -o -path './.ai' -o -path './.tracker' -o -path './.venv' -o -path './venv' -o -name node_modules -o -name vendor -o -name testdata -o -name __pycache__ -o -name site-packages \) -prune \
    -o -type f \( -name 'test_*.py' -o -name '*_test.py' \) -print 2>/dev/null | head -n1)
  if [ -n "$py_test_hit" ]; then
    echo "--- python test files found without a pyproject.toml ($py_test_hit) — manifest-free pytest run ---"
    printf 'python\t.\n' >> "$STACKS_TMP"
  fi
fi
if [ ! -s "$STACKS_TMP" ]; then
  # No manifest anywhere (and no python test files). A Makefile with a
  # ci/check/lint/test target IS a test stack (run_project_ci_gate runs it
  # below); otherwise nothing runs and the verdict below decides:
  #   --final     → FAIL: a product with no test runner cannot ship green.
  #   milestone   → exit 3 NOT-YET-VERIFIABLE: an early scaffolding/docs
  #                 milestone legitimately has no runner yet; VerifyMilestone
  #                 decides whether the milestone's done-when needs tests.
  # An OPERATOR stamp (.ai/build/no-tests-ok) passes both modes with a NOTE
  # for a genuinely test-free project. Its path is deliberately not printed
  # here: this output is what the fix agent reads, and the stamp must never
  # be created from a build session (a stamp created mid-milestone is
  # reported by report_hatch_additions as a finding).
  if makefile_has_ci_target; then
    echo "NOTE: no go.work / go.mod / package.json / pyproject.toml / Cargo.toml anywhere — the Makefile $MAKEFILE_CI_TARGET target is the only test runner"
  elif [ -f .ai/build/no-tests-ok ]; then
    echo "NOTE: no build system detected and the operator opt-out stamp is present — nothing was tested (VerifyMilestone: a finding unless the project is genuinely test-free)"
  elif [ "$VERIFY_MODE" = final ]; then
    echo "ERROR: no build system detected — looked for go.work / go.mod / package.json / pyproject.toml / Cargo.toml (and a Makefile ci/check/lint/test target) in every tracked or untracked directory (excluding node_modules/, vendor/, .ai/, testdata/), and for python test files. A product with no test runner cannot ship green."
    echo "ERROR: if this project genuinely has no test stack, the OPERATOR can place the opt-out stamp documented under 'Operator stamps' in the build_product section of the workflow README (and in the EscalateVerification gate) — never a build session."
    rm -f "$STACKS_TMP"
    exit 1
  else
    echo "NOTE: no build system detected — nothing was tested this milestone (no go.work / go.mod / package.json / pyproject.toml / Cargo.toml, no python test files, no Makefile ci/check/lint/test target). See the NOT-YET-VERIFIABLE verdict below."
  fi
fi
while IFS="$(printf '\t')" read -r kind dir <&3; do
  [ -n "$kind" ] || continue
  run_stack "$kind" "$dir" || TEST_EXIT=1
done 3< "$STACKS_TMP"
if [ "$VERIFY_MODE" = final ] && [ -f "$GO_STACK_SEEN" ] && [ ! -f "$GO_TESTS_SEEN" ]; then
  echo "ERROR: no Go test files in ANY Go stack — a product with zero tests cannot ship green (#640 D7)"
  TEST_EXIT=1
fi
RAN_TESTS=""
[ -f "$TESTS_RAN" ] && RAN_TESTS=1
rm -f "$STACKS_TMP" "$GO_STACK_SEEN" "$GO_TESTS_SEEN" "$TESTS_RAN"

# --- project CI gate (issue #233 Gap 1) --------------------------------------
# Makefile ci/check/lint/test target (BLOCKING — the project's own oracle)
# AND the language-native lint/vet gates for every stack (#640 D8; ADVISORY
# since the tracker-runner convergence — they run and report, never fail).
# Any failure is a normal fix-loop failure; the make-missing environment
# case is signalled out of band (see ci-probe.sh).
CI_RC=0
run_project_ci_gate || CI_RC=1
if [ "$CI_RC" -ne 0 ]; then
  TEST_EXIT=1
fi
# A real build/test/CI failure routes to the fix loop first (exit 1),
# regardless of what did or didn't run.
if [ "$TEST_EXIT" -ne 0 ]; then
  exit 1
fi

# --- deny-by-default verdict (tracker-runner #857/#873) ----------------------
# GREEN only when a real oracle ran and passed — a language test suite that
# executed a positive number of tests (RAN_TESTS) OR the project's own
# declared CI target (PROJECT_CI_RAN, set by ci-probe.sh on a `make
# ci`/`check`/`lint`/`test` run). The pipeline-imposed language-native lint
# gates are advisory and do NOT count as verification. The operator stamp
# .ai/build/no-tests-ok declares a genuinely test-free project (both modes).
if [ -n "$RAN_TESTS" ] || [ -n "${PROJECT_CI_RAN:-}" ]; then
  exit 0
fi
if [ -f .ai/build/no-tests-ok ]; then
  echo "NOTE: no runnable oracle ran and the operator opt-out stamp is present — nothing was tested (VerifyMilestone: a finding unless the project is genuinely test-free)"
  exit 0
fi
if [ "$VERIFY_MODE" = final ]; then
  echo "ERROR: no runnable oracle — no language test suite executed any test and no Makefile ci/check/lint/test target ran. A product with no executed tests cannot ship green."
  echo "ERROR: if this project genuinely has no test stack, the OPERATOR can place the opt-out stamp documented under 'Operator stamps' in the build_product section of the workflow README (and in the EscalateVerification gate) — never a build session."
  exit 1
fi
echo "NOT-YET-VERIFIABLE: no runnable test suite and no project CI target detected."
echo "  Deny-by-default: absence of a runnable oracle is not a pass. This is NOT a"
echo "  build failure — the completed work is kept; the milestone verifier decides"
echo "  whether this milestone legitimately needs no executable verification, or"
echo "  whether tests / packaging (a pyproject.toml, go.mod, package.json, or"
echo "  Cargo.toml) must be added so the suite becomes runnable."
exit 3
