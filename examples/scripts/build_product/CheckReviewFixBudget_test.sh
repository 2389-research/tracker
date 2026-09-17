#!/usr/bin/env bash
# ABOUTME: Fixture tests for CheckReviewFixBudget.sh (#233 Gap 5.3) — caps the
# ABOUTME: post-ApplyReviewFixes re-review loop at MAX_ATTEMPTS=1 (gate-before-
# ABOUTME: work, -gt): exactly one re-review pass, then escalate to a human.
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
SCRIPT="$(stage_script "$DIR/CheckReviewFixBudget.sh")"   # ${graph.workflow_dir} expanded as the engine does
run() { OUT="$( (cd "$WORK" && sh "$SCRIPT") 2>"$STATE/stderr")"; RC=$?; }
last() { printf '%s' "$OUT" | tail -1; }
COUNTER="$WORK/.ai/build/review_fix_attempts"

# 1. First pass: scaffolds .ai/build (no prior mkdir needed), counter 1, allowed.
run
check "pass 1 exit 0"              "0" "$RC"
check "pass 1 message"             "review-fix budget OK: re-review pass 1 of 1" "$(last)"
check "counter = 1"                "1" "$(cat "$COUNTER")"

# 2. Second pass exceeds MAX_ATTEMPTS=1 -> exit 1 (routes to EscalateReview).
run
check "pass 2 exit 1"              "1" "$RC"
check "exhausted message"          "budget-exhausted: review-fix loop ran 2 times (max 1 allowed re-reviews)" "$(last)"
check "counter = 2"                "2" "$(cat "$COUNTER")"

# 3. After ResetReviewBudget (rm -f) the retry build gets its one pass back.
rm -f "$COUNTER"
run
check "after reset pass 1"         "review-fix budget OK: re-review pass 1 of 1" "$(last)"

# 4. KNOWN-BUG: unlike CheckSpecForgeBudget / ContinueWithMoreTurns /
#    TestMilestone, this gate has NO numeric guard on the counter. A corrupted
#    file is fed straight into $((ATTEMPTS + 1)), which under `set -eu` is an
#    arithmetic abort (dash: "Illegal number", exit 2; bash-as-sh: syntax
#    error, exit 1) — the node fails instead of treating the junk as 0 and allowing
#    the one re-review pass. It fails CLOSED (routes to EscalateReview), so
#    severity is low, but CheckSpecForgeBudget's own comment names this guard
#    as "a fix the clone must not omit". '1 2' is used because bare 'garbage'
#    is silently read as an unset variable (=0) by bash-as-sh on macOS, while
#    dash rejects both. When the guard is added, flip these to expect
#    exit 0 / "pass 1 of 1" / counter 1.
printf '1 2\n' > "$COUNTER"
run
check "KNOWN-BUG corrupted counter aborts (want exit 0 when fixed)" "nonzero" "$([ "$RC" -ne 0 ] && echo nonzero || echo zero)"
check "KNOWN-BUG no pass message (want 'pass 1 of 1' when fixed)" "" "$OUT"

if [ "$fail" = 0 ]; then echo "ALL PASS"; else echo "SOME FAILED"; exit 1; fi
