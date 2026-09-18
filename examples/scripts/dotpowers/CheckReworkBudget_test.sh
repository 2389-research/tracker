#!/usr/bin/env bash
# ABOUTME: Fixture tests for CheckReworkBudget.sh (#646 item 9) — the rework
# ABOUTME: counter tolerates a corrupted file (read as 0) instead of a dash
# ABOUTME: `Illegal number` abort, and caps at 2.
set -uo pipefail
DIR="$(cd "$(dirname "$0")" && pwd)"
fail=0
check() { # name expected actual
  if [ "$2" = "$3" ]; then echo "ok: $1"; else echo "FAIL: $1 — want '$2' got '$3'"; fail=1; fi
}
WORK="$(mktemp -d)"
STATE="$(mktemp -d)"
trap 'rm -rf "$WORK" "$STATE"' EXIT
. "$DIR/test_helpers.sh"
SCRIPT="$(stage_script "$DIR/CheckReworkBudget.sh")"
run() { OUT="$( (cd "$WORK" && ${TEST_SH:-sh} "$SCRIPT") 2>"$STATE/stderr")"; RC=$?; }
COUNTER="$WORK/.tracker/rework_count"

run
check "first marker"               "budget_ok" "$OUT"
run
check "second marker"              "budget_ok" "$OUT"
run
check "third exhausted"            "budget_exhausted" "$OUT"
printf '1 2\n' > "$COUNTER"; run
check "garbage exit 0"             "0" "$RC"
check "garbage marker"             "budget_ok" "$OUT"
check "garbage reset to 1"         "1" "$(tr -d '\n' < "$COUNTER")"

if [ "$fail" = 0 ]; then echo "ALL PASS"; else echo "SOME FAILED"; exit 1; fi
