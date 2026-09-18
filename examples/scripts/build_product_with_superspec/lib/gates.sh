# ABOUTME: Quality-gate runner for build_product_with_superspec (#646 item 5f).
# ABOUTME: Sourced (never executed) via ${graph.workflow_dir}/scripts/build_product_with_superspec/lib.
# Callers must also source lib/gate-integrity.sh (restore_gate_files).
#
# Every phase gate and the final gate run the SAME green-gate as
# build_product: lib/verify.sh (a parity-pinned copy Setup installs to
# .ai/build/verify.sh), which detects EVERY stack anywhere in the tree
# (go.work / go.mod / package.json / pyproject.toml / Cargo.toml), builds +
# tests each in its own directory, then runs the project CI gate (Makefile
# ci/check/lint/test target AND the language-native gates). The old
# first-match `if pyproject/elif go.mod` chains had no npm/cargo, no Makefile
# probe, inverted order, and let a Node/Rust project pass with zero tests.
#
# Phase gates run verify.sh in milestone mode (tests scoped to the packages
# changed since the phase base + their reverse deps; a stack with zero tests
# is a loud NOTE). The final gate runs `--final`: whole tree, -count=1,
# known_failures ignored, zero Go tests = FAIL, no stack = FAIL unless the
# operator stamp .ai/build/no-tests-ok exists.

# start_gate NAME LIB — open the report .ai/gates/NAME.txt; GATE_PASS=true;
# remember LIB (the workflow's lib/ dir) for the gate-file restore.
start_gate() {
  GATE_NAME=$1
  GATE_LIB=$2
  [ -n "$GATE_LIB" ] && [ -f "$GATE_LIB/verify.sh" ] || { echo "ERROR: start_gate needs the workflow lib dir (got '$GATE_LIB')"; exit 1; }
  GATE_REPORT=".ai/gates/$GATE_NAME.txt"
  GATE_PASS=true
  mkdir -p .ai/gates
  echo "=== $GATE_NAME quality gates ===" > "$GATE_REPORT"
}

# gate_verify [--final] — build + tests + project CI gate via verify.sh.
# #640 D6: FixPhaseN / FixStreamD / StreamD / ApplyReviewFixes run in the
# main workdir and can rewrite .ai/build/verify.sh (`echo 'exit 0' >
# .ai/build/verify.sh` used to yield a green gate). restore_gate_files
# (lib/gate-integrity.sh, parity-pinned copy of build_product's) re-emits
# verify.sh + ci-probe.sh from the sidecar before every gate run and prints
# a WARNING naming a file that differed — that line lands in the report the
# fix agent and reviewers read. Requires GATE_LIB (set by start_gate's caller).
gate_verify() {
  echo "--- build + tests + project CI gate (verify.sh ${1:-milestone mode}) ---" >> "$GATE_REPORT"
  restore_gate_files "$GATE_LIB" >> "$GATE_REPORT" 2>&1
  # shellcheck disable=SC2086  # the optional --final flag
  sh .ai/build/verify.sh ${1:-} >> "$GATE_REPORT" 2>&1 || GATE_PASS=false
}

# gate_coverage — QG-3 coverage summary for a root Go module (report-only:
# the threshold is the reviewers' call; a failing test run here is already
# a gate failure from gate_verify).
gate_coverage() {
  [ -f go.mod ] && command -v go >/dev/null 2>&1 || return 0
  echo "--- coverage (QG-3, root Go module) ---" >> "$GATE_REPORT"
  if go test ./... -coverprofile=".ai/gates/$GATE_NAME-coverage.out" >/dev/null 2>&1; then
    go tool cover -func=".ai/gates/$GATE_NAME-coverage.out" 2>/dev/null | tail -1 >> "$GATE_REPORT" || echo "coverage: summary unavailable" >> "$GATE_REPORT"
  else
    echo "coverage: unavailable (tests red — see the verify.sh section)" >> "$GATE_REPORT"
  fi
}

# gate_complexity — QG-5 cyclomatic complexity via gocyclo when installed.
# A WARNING, never a gate failure: the old `gocyclo -over 10 >> report`
# under `set -e` exited the node (gocyclo exits 1 when it lists anything),
# turning the intended warning into a FAIL with no marker.
gate_complexity() {
  command -v gocyclo >/dev/null 2>&1 && [ -f go.mod ] || return 0
  echo "--- complexity (QG-5) ---" >> "$GATE_REPORT"
  COMPLEX=$(gocyclo -over 10 . 2>/dev/null | grep -c . || true)
  echo "Functions over cyclomatic 10: $COMPLEX" >> "$GATE_REPORT"
  if [ "$COMPLEX" -gt 0 ]; then
    gocyclo -over 10 . >> "$GATE_REPORT" 2>&1 || true
    echo "WARNING: complexity violations (QG-5) — reported, not a gate failure" >> "$GATE_REPORT"
  fi
}

# gate_gold — QG-7 gold-dataset evaluation when a gold/golden dir exists.
# Best-effort (`|| true`), as before: the tests themselves already ran red
# or green in gate_verify; this section is the verbose evidence.
gate_gold() {
  [ -d tests/gold ] || [ -d tests/golden ] || return 0
  echo "--- gold dataset evaluation (QG-7) ---" >> "$GATE_REPORT"
  if [ -f go.mod ]; then
    go test ./... -run 'Gold|Eval|Regression' -v >> "$GATE_REPORT" 2>&1 || true
  elif [ -f pyproject.toml ]; then
    uv run pytest -k 'gold or eval or regression' -v >> "$GATE_REPORT" 2>&1 || true
  fi
}

# finish_gate — print the report, then the routing marker LAST via printf
# with no trailing newline (`<name>-gates-PASS` / `<name>-gates-FAIL`);
# exit 1 on FAIL. On PASS the phase base (.ai/build/milestone-start-sha)
# advances to HEAD so the NEXT gate (GateStreamD after GatePhase2, whose
# stream has no worktree setup of its own) scopes to its own changes; a
# FAIL keeps the base so the fix loop re-gates the same range.
finish_gate() {
  cat "$GATE_REPORT"
  if [ "$GATE_PASS" != true ]; then
    printf '%s-gates-FAIL' "$GATE_NAME"
    exit 1
  fi
  mkdir -p .ai/build
  git rev-parse HEAD > .ai/build/milestone-start-sha 2>/dev/null || true
  printf '%s-gates-PASS' "$GATE_NAME"
}
