#!/usr/bin/env bash
# ABOUTME: Fixture tests for quickfix's CheckReviseBudget.sh — three revisions are allowed,
# ABOUTME: the fourth request exits 1 (the FinalDecision gate takes over).
set -uo pipefail
DIR="$(cd "$(dirname "$0")" && pwd)"
. "$DIR/test_helpers.sh"
SCRIPT="$(stage_script "$DIR/CheckReviseBudget.sh")"
new_repo
COUNT="$WORK/.tracker/quickfix/revise_count"

for prior in 0 1 2; do
  qf_run "$SCRIPT"
  check "after $prior revisions: exit 0"   "0" "$RC"
  check "after $prior revisions: count"    "$((prior + 1))" "$(cat "$COUNT")"
done
check "third revision reported"            "yes" "$(has 'revision 3 of 3')"
qf_run "$SCRIPT"
check "after 3 revisions: exit 1"          "1" "$RC"
check "exhaustion says so"                 "yes" "$(has 'revise budget exhausted')"

# A corrupted counter reads as 0 (bump_counter's guard), not an error.
echo junk > "$COUNT"
qf_run "$SCRIPT"
check "junk counter: exit 0"               "0" "$RC"
check "junk counter: reset to 1"           "1" "$(cat "$COUNT")"

exit "$fail"
