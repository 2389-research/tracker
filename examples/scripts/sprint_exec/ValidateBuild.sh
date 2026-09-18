set -eu
# Sprint validation gate (#646 items 7, 11): logs live under .ai/build/ in
# the workdir (a fixed /tmp path was shared across concurrent runs), the
# Swift gate uses grep (ripgrep is not a CI given — an absent `rg` made
# `if rg …` false and passed a broken tree), a red `swift build` is a FAIL
# with its log shown, and a tree with no known build system is a FAIL — the
# old `validation-pass-no-known-build-system` was a fake green routed
# straight to CommitSprintWork.
mkdir -p .ai/build
BUILD_LOG=.ai/build/build.log
TEST_LOG=.ai/build/test.log
if [ -f go.mod ]; then
  go build ./... >"$BUILD_LOG" 2>&1 || { cat "$BUILD_LOG"; exit 1; }
  go test ./... >"$TEST_LOG" 2>&1 || { cat "$TEST_LOG"; exit 1; }
  cat "$TEST_LOG"
  printf 'validation-pass-go'
  exit 0
fi
if [ -f Package.swift ]; then
  BUILD_RC=0
  swift build >"$BUILD_LOG" 2>&1 || BUILD_RC=$?
  cat "$BUILD_LOG"
  if [ "$BUILD_RC" -ne 0 ] || grep -q 'error:' "$BUILD_LOG"; then
    echo "swift build failed (rc=$BUILD_RC)"
    exit 1
  fi
  TEST_RC=0
  swift test >"$TEST_LOG" 2>&1 || TEST_RC=$?
  cat "$TEST_LOG"
  if [ "$TEST_RC" -ne 0 ]; then
    echo "swift test failed (rc=$TEST_RC)"
    exit 1
  fi
  printf 'validation-pass-swift'
  exit 0
fi
if [ -f package.json ]; then
  npm test >"$TEST_LOG" 2>&1 || { cat "$TEST_LOG"; exit 1; }
  printf 'validation-pass-node'
  exit 0
fi
echo 'ERROR: no known build system (go.mod / Package.swift / package.json) — nothing can be validated; fix the project layout or add a build system'
printf 'validation-fail-no-known-build-system'
exit 1
