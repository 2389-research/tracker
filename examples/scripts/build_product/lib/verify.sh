# Run this directly (`sh .ai/build/verify.sh [--final]`) — do NOT source
# it. It sources .ai/build/ci-probe.sh internally (stack detection + the
# project CI gate). Exit:
#   0  green: build + every detected stack's tests + project CI gate pass
#   1  anything else — any build/test/CI failure (normal fix-loop failure),
#      no stack detected without the operator opt-out, a missing
#      ci-probe.sh, an unusable known_failures entry. Every non-zero
#      TEST-runner exit collapses to 1 (a pytest collection error exiting 2
#      is a failure like any other). There is NO semantic exit code: the
#      one environment case the fix loop cannot solve (Makefile present,
#      `make` missing) is signalled by ci-probe.sh printing
#      `_TRACKER_CI_MAKE_MISSING` and creating .ai/build/ci-make-missing
#      (#640 E8) — TestMilestone keys `escalate` on that file.
#
# Modes:
#   (default) milestone gate — Go tests are scoped to the packages this
#     milestone touched (#392, #640 D2/D3/D9: base→WORKTREE incl. uncommitted
#     and untracked files, plus every package that depends on them, filtered
#     through `go list -e`); known_failures entries are skipped (Go only);
#     a stack with zero tests passes with a loud NOTE.
#   --final  ship gate (FinalBuild) — whole tree, `go test -count=1`,
#     known_failures IGNORED (the still-listed entries are printed as the
#     reason a red run is red), a Go stack with zero test files is a FAILURE
#     (#640 D7), elapsed seconds printed per stack (#640 D13).
set -eu
VERIFY_MODE=milestone
[ "${1:-}" = "--final" ] && VERIFY_MODE=final
TEST_EXIT=0

[ -f .ai/build/ci-probe.sh ] || { echo "ERROR: .ai/build/ci-probe.sh missing — Setup did not run (or Cleanup removed .ai/build/); cannot adjudicate green"; exit 1; }
. .ai/build/ci-probe.sh
rm -f .ai/build/ci-make-missing

# --- known_failures → `go test -skip` pattern (Go only; #640 D7/D12) -------
# Each entry is anchored PER PATH SEGMENT — `TestA` becomes `^TestA$` (no
# longer also skipping TestAB / TestAlpha), `TestA/sub` becomes
# `^TestA$/^sub$` (only that subtest) — and the alternatives are joined with
# a top-level `|`, which `go test` splits into independent skip filters.
# The assembled regex is validated (`grep -E`) so a malformed entry fails
# closed instead of silently skipping nothing or everything.
SKIP_PATTERN=""
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
# since MS_BASE in the WORKTREE (committed or not) plus untracked .go files
# (#640 D2), each filtered through `go list -e` so a non-package (testdata/,
# _gen/, a build-tag-excluded file, a nested module) never yields a fake red
# (#640 D9), plus every package in this module whose dependency closure
# touches a changed package (#640 D3). Bounded: an unreadable `go list` or
# a closure over GO_SCOPE_MAX packages falls back to ./... with a note.
GO_SCOPE_MAX=64
go_scope_targets() {
  GO_TEST_TARGET="./..."
  [ -n "$MS_BASE" ] || { echo "--- not a git repo / no base — testing ./... ---"; return 0; }
  CHANGED_FILES=$( {
      git diff --relative --name-only --diff-filter=d "$MS_BASE" -- . 2>/dev/null
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
run_stack_go() {
  (
    cd "$1" || exit 1
    go build ./... 2>&1 || exit 1
    if [ "$VERIFY_MODE" = final ]; then
      # Product-wide zero-tests check (#640 D7): record that SOME Go stack
      # has tests; the ship gate fails after the sweep if none did.
      : > "$GO_STACK_SEEN"
      if go_has_tests ./...; then : > "$GO_TESTS_SEEN"; else echo "NOTE: no Go test files in $1"; fi
      go test -count=1 ./... 2>&1 || exit 1
      exit 0
    fi
    go_scope_targets
    # shellcheck disable=SC2086
    if ! go_has_tests $GO_TEST_TARGET; then
      echo "NOTE: no Go test files in scope ($GO_TEST_TARGET) — go test proves only that the code compiles. Acceptable for a docs-only milestone; VerifyMilestone must confirm the milestone's done-when needs no tests."
    fi
    if [ -n "$SKIP_PATTERN" ]; then
      echo "--- skipping known failures: $SKIP_PATTERN ---"
      # shellcheck disable=SC2086  # package tokens from go list, never eval'd
      go test $GO_TEST_TARGET -skip "$SKIP_PATTERN" 2>&1 || exit 1
    else
      # shellcheck disable=SC2086
      go test $GO_TEST_TARGET 2>&1 || exit 1
    fi
  )
}
run_stack_npm()    { ( cd "$1" && npm test 2>&1 ); }
run_stack_python() { ( cd "$1" && uv run pytest 2>&1 ); }
run_stack_cargo()  { ( cd "$1" && cargo test 2>&1 ); }

# --- run EVERY detected stack (#305, #640 D1) ---------------------------------
# A failure is sticky (TEST_EXIT=1): a later passing stack can't mask it,
# and every stack still runs so one pass shows all the results.
STACKS_TMP=$(mktemp)
GO_STACK_SEEN="$STACKS_TMP.go"; GO_TESTS_SEEN="$STACKS_TMP.gotests"
detect_stacks > "$STACKS_TMP"
if [ ! -s "$STACKS_TMP" ]; then
  if [ -f .ai/build/no-tests-ok ]; then
    echo "NOTE: no build system detected and .ai/build/no-tests-ok is present — operator opted out of the test gate; nothing was tested (VerifyMilestone: treat as a finding unless the milestone is genuinely test-free)."
  else
    echo "ERROR: no build system detected — looked for go.work / go.mod / package.json / pyproject.toml / Cargo.toml in every tracked or untracked directory (excluding node_modules/, vendor/, .ai/, testdata/). A milestone with no test runner cannot be green."
    echo "ERROR: if this project genuinely has no test stack, an OPERATOR may opt out with: touch .ai/build/no-tests-ok"
    rm -f "$STACKS_TMP"
    exit 1
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
rm -f "$STACKS_TMP" "$GO_STACK_SEEN" "$GO_TESTS_SEEN"

# --- project CI gate (issue #233 Gap 1) --------------------------------------
# Makefile ci/check/lint target AND the language-native gates for every
# stack (#640 D8). Any failure is a normal fix-loop failure; the make-missing
# environment case is signalled out of band (see ci-probe.sh).
CI_RC=0
run_project_ci_gate || CI_RC=1
if [ "$CI_RC" -ne 0 ]; then
  TEST_EXIT=1
fi
exit "$TEST_EXIT"
