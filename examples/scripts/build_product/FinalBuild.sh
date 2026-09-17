set -eu
# Run EVERY detected stack, not just the first match (issue #305).
# Test failures accumulate into STACK_EXIT (the `|| STACK_EXIT=$?`
# guards keep set -eu from aborting mid-sweep) so every stack's
# results are visible in one pass; the [ ... ] check below then
# propagates any failure as a node-level failure via set -e. `go
# build` stays unguarded: a compile failure aborts immediately, as
# before #305.
STACK_EXIT=0
if [ -f go.mod ]; then
  go build ./... 2>&1
  go test ./... 2>&1 || STACK_EXIT=$?
fi
if [ -f package.json ]; then
  npm test 2>&1 || STACK_EXIT=$?
fi
if [ -f pyproject.toml ]; then
  uv run pytest 2>&1 || STACK_EXIT=$?
fi
if [ -f Cargo.toml ]; then
  cargo test 2>&1 || STACK_EXIT=$?
fi
[ "$STACK_EXIT" -eq 0 ]

# Project CI gate (issue #233 Gap 1). Sources the same shared
# helper as TestMilestone — written by Setup; lives in exactly
# one place. FinalBuild has no fix loop, so any non-zero return
# (including the rc=2 "make missing" case) propagates as a
# node-level failure via `set -eu`, which the engine routes
# through the strict-failure-edge logic — no silent pass.
. .ai/build/ci-probe.sh
run_project_ci_gate

printf 'final-build-pass'