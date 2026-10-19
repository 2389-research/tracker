#!/usr/bin/env bash
# ABOUTME: Fixture tests for quickfix's CheckReviseBudget.sh — three revisions print revise-ok,
# ABOUTME: the fourth prints revise-exhausted (FinalDecision takes over); exit 0 either way.
set -uo pipefail
DIR="$(cd "$(dirname "$0")" && pwd)"
. "$DIR/test_helpers.sh"
SCRIPT="$(stage_script "$DIR/CheckReviseBudget.sh")"
new_repo
COUNT="$WORK/.tracker/quickfix/revise_count"

for prior in 0 1 2; do
  qf_run "$SCRIPT"
  check "after $prior revisions: exit 0"   "0" "$RC"
  check "after $prior revisions: marker"   "revise-ok" "$OUT"
  check "after $prior revisions: count"    "$((prior + 1))" "$(cat "$COUNT")"
done
check "third revision reported"            "yes" "$(contains "$ERR" 'revision 3 of 3')"
qf_run "$SCRIPT"
check "after 3 revisions: exit 0"          "0" "$RC"
check "after 3 revisions: marker"          "revise-exhausted" "$OUT"
check "exhaustion says so"                 "yes" "$(contains "$ERR" 'revise budget used up')"

# A corrupted counter reads as 0 (bump_counter's guard), not an error.
echo junk > "$COUNT"
qf_run "$SCRIPT"
check "junk counter: exit 0"               "0" "$RC"
check "junk counter: marker"               "revise-ok" "$OUT"
check "junk counter: reset to 1"           "1" "$(cat "$COUNT")"

exit "$fail"
