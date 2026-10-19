#!/usr/bin/env bash
# ABOUTME: Fixture tests for explain's WriteAnswer.sh — copies the Answer node's response to
# ABOUTME: <run dir>/explain.md, fails loudly without one, and prints the quickfix next step.
set -uo pipefail
DIR="$(cd "$(dirname "$0")" && pwd)"
fail=0
check() { # name expected actual
  if [ "$2" = "$3" ]; then echo "ok: $1"; else echo "FAIL: $1 — want '$2' got '$3'"; fail=1; fi
}
WORK="$(mktemp -d)"
STATE="$(mktemp -d)"
trap 'rm -rf "$WORK" "$STATE"' EXIT
. "$DIR/../build_product/test_helpers.sh"
SCRIPT="$(stage_script "$DIR/WriteAnswer.sh")"
RUN_DIR="$WORK/.tracker/runs/run-1"
run() { OUT="$( (cd "$WORK" && TRACKER_RUN_DIR="$RUN_DIR" ${TEST_SH:-sh} "$SCRIPT") 2>"$STATE/stderr")"; RC=$?; }

# 1. No Answer response: fail loudly, no explain.md, no next step.
mkdir -p "$RUN_DIR"
run
check "missing response exits 1"        "1" "$RC"
check "missing response says why"       "yes" "$(has_re_in "$(cat "$STATE/stderr")" 'ERROR: .*Answer/response.md')"
check "missing response writes nothing" "no" "$([ -e "$RUN_DIR/explain.md" ] && echo yes || echo no)"
check "missing response: no next line"  "no" "$(has 'tracker quickfix')"

# 2. An empty response is as bad as none.
mkdir -p "$RUN_DIR/Answer"
: > "$RUN_DIR/Answer/response.md"
run
check "empty response exits 1"          "1" "$RC"

# 3. A response: copied verbatim, path reported, quickfix next step printed.
printf 'This repo is a pipeline engine.\n\nIt runs `.dip` files.\n' > "$RUN_DIR/Answer/response.md"
run
check "answer exits 0"                  "0" "$RC"
check "explain.md is the response"      "yes" "$(cmp -s "$RUN_DIR/Answer/response.md" "$RUN_DIR/explain.md" && echo yes || echo no)"
check "reports where it saved"          "yes" "$(has "answer saved to $RUN_DIR/explain.md")"
check "next line"                       "yes" "$(has_line 'next: make a small change with')"
check "quickfix command"                "yes" "$(has_line '  tracker quickfix --param change="<one sentence>"')"
check "notice for the run summary"     "yes" "$(contains "$(cat "$RUN_DIR/notice.txt" 2>/dev/null)" 'tracker quickfix --param change="<one sentence>"')"
check "notice names the answer"         "yes" "$(contains "$(cat "$RUN_DIR/notice.txt" 2>/dev/null)" "answer saved to $RUN_DIR/explain.md")"
check "tracked tree untouched"          "" "$(cd "$WORK" && ls -A | grep -v '^\.tracker$' || true)"

# 4. TRACKER_RUN_DIR unset: fail loudly instead of writing to /explain.md.
OUT="$( (cd "$WORK" && env -u TRACKER_RUN_DIR ${TEST_SH:-sh} "$SCRIPT") 2>&1)"; RC=$?
check "no run dir exits 1"              "1" "$RC"
check "no run dir says why"             "yes" "$(has 'TRACKER_RUN_DIR')"

exit "$fail"
