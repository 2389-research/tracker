#!/usr/bin/env bash
# ABOUTME: Fixture tests for CheckImplementBudget.sh (#646 item 9) — the
# ABOUTME: per-task attempt counter tolerates a corrupted file (read as 0)
# ABOUTME: instead of a dash `Illegal number` abort, and caps at 5.
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
SCRIPT="$(stage_script "$DIR/CheckImplementBudget.sh")"
run() { OUT="$( (cd "$WORK" && ${TEST_SH:-sh} "$SCRIPT") 2>"$STATE/stderr")"; RC=$?; }
mkdir -p "$WORK/docs/plans"
printf 'task-7' > "$WORK/docs/plans/current_task_id.txt"
COUNTER="$WORK/.tracker/impl_count_task-7"

# 1. Fresh -> 1, budget_ok; increments.
run
check "first exit 0"               "0" "$RC"
check "first marker"               "budget_ok" "$OUT"
check "counter 1"                  "1" "$(tr -d '\n' < "$COUNTER")"
run; run; run; run
check "fifth marker"               "budget_ok" "$OUT"
run
check "sixth exhausted"            "budget_exhausted" "$OUT"

# 2. Corrupted counter reads as 0 -> budget_ok, counter reset to 1.
printf '1 2\n' > "$COUNTER"; run
check "garbage exit 0"             "0" "$RC"
check "garbage marker"             "budget_ok" "$OUT"
check "garbage reset to 1"         "1" "$(tr -d '\n' < "$COUNTER")"
printf 'abc' > "$COUNTER"; run
check "abc marker"                 "budget_ok" "$OUT"

if [ "$fail" = 0 ]; then echo "ALL PASS"; else echo "SOME FAILED"; exit 1; fi
