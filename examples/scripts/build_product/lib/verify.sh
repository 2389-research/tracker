# Run this directly (`sh .ai/build/verify.sh`) — do NOT source it. It
# sources .ai/build/ci-probe.sh internally for the project CI gate. Exit:
#   0  green: build + every detected stack's tests + project CI gate pass
#   2  Makefile present but `make` not installed (environment; escalate) —
#      RESERVED for that env-missing case ONLY. TestMilestone routes exit 2
#      straight to `escalate`, so no language test runner's exit may reach it.
#   1  any build/test/CI failure (normal fix-loop failure). Every non-zero
#      TEST-runner exit collapses to 1 below (a runner exiting 2 — e.g. a
#      pytest collection error — must NOT masquerade as the make-missing 2).
set -eu
TEST_EXIT=0

# Build the skip pattern from known_failures, stripping comments and blanks.
SKIP_PATTERN=""
if [ -f .ai/milestones/known_failures ]; then
  SKIP_PATTERN=$(grep -v '^#' .ai/milestones/known_failures | grep -v '^$' | paste -sd'|' -)
fi

# Run EVERY detected stack, not just the first match (issue #305).
# Each runner's non-zero exit collapses to TEST_EXIT=1 (NOT $?) so a runner
# that legitimately exits 2 (e.g. a pytest collection error) can't be
# mistaken for the make-missing escalate (exit 2) reserved below (PR #411).
# The 1 is sticky: once a stack fails a later passing stack can't reset it,
# so failures still can't be masked. `go build` stays unguarded: under set -eu a Go
# compile failure aborts immediately (exit 1, normal fix-loop failure).
# Milestone-scoped Go test target (issue #392): scope `go test` to the
# packages this milestone changed since .ai/build/milestone-start-sha; the
# WHOLE-tree suite still runs at FinalBuild. The derived package tokens are
# ONLY ever passed as `go test` package arguments — never eval'd.
if [ -f go.mod ]; then
  go build ./... 2>&1
  GO_TEST_TARGET="./..."
  MS_START=$(cat .ai/build/milestone-start-sha 2>/dev/null || true)
  if [ -z "$MS_START" ] || ! git cat-file -e "${MS_START}^{commit}" 2>/dev/null; then
    MS_BASE=$(git hash-object -t tree /dev/null)
  else
    MS_BASE="$MS_START"
    # Milestone-scope the lint gate too (issue #436) — only when we have a
    # real base commit (never the empty-tree fallback, which is not a rev
    # golangci-lint --new-from-rev accepts). ci-probe.sh, sourced below in
    # the same shell, reads this to pass --new-from-rev.
    LINT_NEW_FROM_REV="$MS_START"
    export LINT_NEW_FROM_REV
  fi
  CHANGED_PKGS=$(git diff --name-only --diff-filter=d "${MS_BASE}..HEAD" 2>/dev/null \
    | grep -E '\.go$' \
    | awk -F/ 'NF==1 { print "." } NF>1 { sub(/\/[^/]*$/, ""); print "./" $0 }' \
    | sort -u \
    | paste -sd' ' -)
  if [ -n "$CHANGED_PKGS" ]; then
    GO_TEST_TARGET="$CHANGED_PKGS"
    echo "--- milestone-scoped go test: $GO_TEST_TARGET ---"
  else
    echo "--- no changed Go packages in milestone range — testing ./... ---"
  fi
  if [ -n "$SKIP_PATTERN" ]; then
    echo "--- skipping known failures: $SKIP_PATTERN ---"
    go test $GO_TEST_TARGET -skip "$SKIP_PATTERN" 2>&1 || TEST_EXIT=1
  else
    go test $GO_TEST_TARGET 2>&1 || TEST_EXIT=1
  fi
fi
if [ -f package.json ]; then
  npm test 2>&1 || TEST_EXIT=1
fi
if [ -f pyproject.toml ]; then
  uv run pytest 2>&1 || TEST_EXIT=1
fi
if [ -f Cargo.toml ]; then
  cargo test 2>&1 || TEST_EXIT=1
fi
if [ ! -f go.mod ] && [ ! -f package.json ] && [ ! -f pyproject.toml ] && [ ! -f Cargo.toml ]; then
  echo "no known build system — skipping tests"
fi

# Project CI gate (issue #233 Gap 1). Source the shared helper; rc=2 means
# a Makefile is present but `make` isn't installed — an environment problem
# the LLM fix loop can't resolve, surfaced as exit 2 so the caller escalates.
. .ai/build/ci-probe.sh
CI_RC=0
run_project_ci_gate || CI_RC=$?
if [ "$CI_RC" -eq 2 ]; then
  exit 2
fi
if [ "$CI_RC" -ne 0 ]; then
  # Non-2 CI failure → normal fix-loop failure (exit 2 already handled above
  # and is reserved for make-missing). Collapse to 1, never propagate raw.
  TEST_EXIT=1
fi
exit "$TEST_EXIT"
