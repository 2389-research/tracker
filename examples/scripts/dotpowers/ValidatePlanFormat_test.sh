#!/usr/bin/env bash
# ABOUTME: Fixture tests for ValidatePlanFormat.sh (#646 item 9) — the
# ABOUTME: per-generation validate counter tolerates a corrupted file, and the
# ABOUTME: format checks (no plan / no checkboxes / handwaves / ok) still route.
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
SCRIPT="$(stage_script "$DIR/ValidatePlanFormat.sh")"
run() { OUT="$( (cd "$WORK" && ${TEST_SH:-sh} "$SCRIPT") 2>"$STATE/stderr")"; RC=$?; }
PLAN="$WORK/docs/plans/plan.md"
mkdir -p "$WORK/docs/plans"
COUNTER="$WORK/.tracker/plan_validate_count"

run
check "no plan"                    "fail_no_plan" "$OUT"
printf '# Plan\njust prose\n' > "$PLAN"; run
check "no checkboxes"              "fail_no_checkboxes" "$OUT"
printf -- '- [ ] task-1: add appropriate validation\n' > "$PLAN"; run
check "handwaves"                  "fail_handwaves" "$OUT"
printf -- '- [ ] task-1: write the parser in src/parse.go\n' > "$PLAN"; run
check "format ok"                  "format_ok" "$OUT"
check "counter 4"                  "4" "$(tr -d '\n' < "$COUNTER")"

# Corrupted counter reads as 0 (dash would otherwise abort with rc 2).
printf 'abc\n' > "$COUNTER"; run
check "garbage exit 0"             "0" "$RC"
check "garbage format ok"          "format_ok" "$OUT"
check "garbage reset to 1"         "1" "$(tr -d '\n' < "$COUNTER")"

# Budget: 6th validate in the same generation -> fail_retries_exhausted.
printf '5\n' > "$COUNTER"; run
check "retries exhausted"          "fail_retries_exhausted" "$OUT"

if [ "$fail" = 0 ]; then echo "ALL PASS"; else echo "SOME FAILED"; exit 1; fi
