set -eu
# Run the project's tests in the quickfix worktree and report, never route:
# this node ALWAYS exits 0 (a red suite is information for the ReviewDiff
# gate, not a failure of the workflow) and prints one final marker —
# tests-pass, tests-fail or no-tests. Exit 1 only when the worktree itself is
# missing (a Preflight invariant broke).
#
# A small purpose-built detector, not build_product's verify.sh (which is
# coupled to its .ai/build/ layout). First match wins: a Makefile `test`
# target, go.mod, package.json with a "test" script, pyproject.toml or
# pytest.ini, Cargo.toml. The suite runs under a timeout
# (QUICKFIX_TEST_TIMEOUT seconds, default 600); on expiry its process tree is
# killed and the run counts as tests-fail.
WT="$(pwd)/.tracker/quickfix/worktree"
[ -d "$WT" ] || { printf 'ERROR: RunTests: no quickfix worktree at %s — Preflight did not run or it was removed\n' "$WT" >&2; exit 1; }
cd "$WT"

TIMEOUT="${QUICKFIX_TEST_TIMEOUT:-600}"
case "$TIMEOUT" in ''|*[!0-9]*|0) TIMEOUT=600 ;; esac

has_make_test() {
  for mf in Makefile makefile GNUmakefile; do
    [ -f "$mf" ] && grep -qE '^test[[:space:]]*:' "$mf" && return 0
  done
  return 1
}

# detect — set CMD (the test command, space-separated) or leave it empty.
detect() {
  CMD=""
  if has_make_test; then CMD="make test"
  elif [ -f go.mod ]; then CMD="go test ./..."
  elif [ -f package.json ] && grep -qE '"test"[[:space:]]*:' package.json; then CMD="npm test"
  elif [ -f pyproject.toml ] || [ -f pytest.ini ]; then CMD="pytest"
  elif [ -f Cargo.toml ]; then CMD="cargo test"
  fi
}

# kill_tree PID — TERM a process and every descendant (pgrep -P), deepest first.
kill_tree() {
  for _child in $(pgrep -P "$1" 2>/dev/null || true); do kill_tree "$_child"; done
  kill -TERM "$1" 2>/dev/null || true
}

# verdict WORD — print the final marker. It is data for the review gate, not a
# routing signal (this node routes on its exit code), so it goes out through a
# variable rather than as a literal.
verdict() { printf '%s' "$1"; }
NONE=no-tests PASS=tests-pass FAIL=tests-fail

detect
if [ -z "$CMD" ]; then
  cat <<'EOF'
no test command found (looked for a Makefile test target, go.mod, a package.json test script, pyproject.toml/pytest.ini, Cargo.toml)
EOF
  verdict "$NONE"
  exit 0
fi
TOOL="${CMD%% *}"
if ! command -v "$TOOL" >/dev/null 2>&1; then
  echo "found a test setup for \`$CMD\`, but \`$TOOL\` is not installed — the tests were not run"
  verdict "$NONE"
  exit 0
fi

TMP=$(mktemp -d)
trap 'rm -rf "$TMP"' EXIT
echo "running: $CMD (timeout ${TIMEOUT}s)"
# shellcheck disable=SC2086  # CMD is one of the fixed commands above
$CMD > "$TMP/log" 2>&1 &
PID=$!
( sleep "$TIMEOUT"; : > "$TMP/timed-out"; kill_tree "$PID" ) >/dev/null 2>&1 &
WATCHDOG=$!
RC=0
wait "$PID" || RC=$?
kill_tree "$WATCHDOG"
wait "$WATCHDOG" 2>/dev/null || true
cat "$TMP/log"

if [ -e "$TMP/timed-out" ]; then
  echo "tests timed out after ${TIMEOUT}s: \`$CMD\` was stopped"
  verdict "$FAIL"
elif [ "$RC" -eq 0 ]; then
  echo "\`$CMD\` passed"
  verdict "$PASS"
else
  echo "\`$CMD\` failed (exit $RC)"
  verdict "$FAIL"
fi
exit 0
