#!/usr/bin/env bash
# ABOUTME: Fixture tests for CheckVerifyFailBudget.sh (#640 A4) — the verify-fail
# ABOUTME: loop breaker: gate-BEFORE-work budget (`-gt 3` = exactly 3 verify-driven
# ABOUTME: fixes, #443 shape), exact end-of-stdout routing markers, numeric guard on
# ABOUTME: a corrupted counter, and no reset here (MarkMilestoneDone/Setup own that).
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
SCRIPT="$(stage_script "$DIR/CheckVerifyFailBudget.sh")"   # ${graph.workflow_dir} expanded as the engine does
# TEST_SH=dash runs the script under dash (the .dip runs it via `sh -c`).
run() { OUT="$( (cd "$WORK" && "${TEST_SH:-sh}" "$SCRIPT") 2>"$STATE/stderr")"; RC=$?; }
# The routing marker is printed LAST with no trailing newline (the .dip routes
# on `endswith`), so the suffix of the whole capture is what the engine sees.
suffix() { printf '%s' "$OUT" | tail -c "${#1}"; }
COUNTER="$WORK/.ai/milestones/verify_fail_attempts"

# 1. First rejection: creates .ai/milestones, counter = 1, routes to the fix loop.
run
check "attempt 1 exit 0"            "0" "$RC"
check "attempt 1 marker last"       "verify-budget-ok" "$(suffix verify-budget-ok)"
check "attempt 1 announces budget"  "1" "$(printf '%s' "$OUT" | grep -c 'verify-fail attempt 1 of 3')"
check "counter = 1"                 "1" "$(cat "$COUNTER")"
check "no marker newline"           "" "$(printf '%s' "$OUT" | tail -c 1 | tr -d 'k')"

# 2./3. Exactly 3 verify-driven fixes pass (-gt, not -ge); the 4th entry escalates.
run
check "attempt 2 exit 0"            "0" "$RC"
run
check "attempt 3 exit 0"            "0" "$RC"
check "attempt 3 marker"            "verify-budget-ok" "$(suffix verify-budget-ok)"
run
check "attempt 4 exit 1"            "1" "$RC"
check "attempt 4 marker last"       "verify-budget-exhausted" "$(suffix verify-budget-exhausted)"
check "attempt 4 explains"          "1" "$(printf '%s' "$OUT" | grep -c 'verify-fail budget exhausted: 4 attempts (max 3)')"
check "counter still incremented"   "4" "$(cat "$COUNTER")"
check "ok marker absent on exhaust" "0" "$(printf '%s' "$OUT" | grep -c 'verify-budget-ok')"

# 4. No reset on exhaustion (a 5th entry — e.g. EscalateMilestone retry -> ...
#    -> Verify fail again — is still refused until MarkMilestoneDone clears it).
run
check "attempt 5 still exhausted"   "1" "$RC"
check "counter = 5"                 "5" "$(cat "$COUNTER")"

# 5. Numeric guard (lib/counters.sh): a corrupted counter is treated as 0 —
#    attempt 1 — not a `set -eu` arithmetic abort (dash: "Illegal number").
for junk in 'garbage' '1 2' ''; do
  printf '%s\n' "$junk" > "$COUNTER"
  run
  check "junk '$junk' -> exit 0"     "0" "$RC"
  check "junk '$junk' -> counter 1"  "1" "$(cat "$COUNTER")"
done

# 6. MarkMilestoneDone's reset (rm -f) restarts the budget for the next milestone.
rm -f "$COUNTER"
run
check "after reset attempt 1"       "1" "$(printf '%s' "$OUT" | grep -c 'verify-fail attempt 1 of 3')"

# 7. The .ai/milestones/fix_attempts counter (TestMilestone's) is never touched.
printf '2\n' > "$WORK/.ai/milestones/fix_attempts"
run
check "fix_attempts untouched"      "2" "$(cat "$WORK/.ai/milestones/fix_attempts")"

if [ "$fail" = 0 ]; then echo "ALL PASS"; else echo "SOME FAILED"; exit 1; fi
