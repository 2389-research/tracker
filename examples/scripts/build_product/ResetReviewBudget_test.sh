#!/usr/bin/env bash
# ABOUTME: Fixture tests for ResetReviewBudget.sh — the EscalateReview "retry"
# ABOUTME: path clears .ai/build/review_fix_attempts so the re-planned build
# ABOUTME: gets its one allowed re-review pass again (Codex P2 / Copilot on #264)
# ABOUTME: and every other per-plan state file (#640 B1) so the new plan
# ABOUTME: starts at milestone 1 instead of inheriting old done/ markers.
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
SCRIPT="$(stage_script "$DIR/ResetReviewBudget.sh")"   # ${graph.workflow_dir} expanded as the engine does
# The script is run with POSIX sh (dippin runs command_file via `sh -c`;
# the shebang is ignored — tracker #324).
run() { OUT="$( (cd "$WORK" && ${TEST_SH:-sh} "$SCRIPT") 2>"$STATE/stderr")"; RC=$?; }
last() { printf '%s' "$OUT" | tail -1; }

# 1. Stale counter from the prior build is removed; marker is the last line.
mkdir -p "$WORK/.ai/build"
echo 1 > "$WORK/.ai/build/review_fix_attempts"
run
check "exit 0"                  "0" "$RC"
check "counter removed"         "gone" "$([ -e "$WORK/.ai/build/review_fix_attempts" ] && echo present || echo gone)"
check "marker last line"        "review-fix budget reset for retry" "$(last)"

# 2. Idempotent: no counter present is still a clean exit (rm -f).
run
check "no counter -> exit 0"    "0" "$RC"

# 3. Only the review-fix counter is touched — sibling budget files survive.
echo 2 > "$WORK/.ai/build/spec_forge_attempts"
echo 1 > "$WORK/.ai/build/review_fix_attempts"
run
check "spec_forge_attempts kept" "2" "$(cat "$WORK/.ai/build/spec_forge_attempts")"

# 4. #640 B1: a re-plan after 5 milestones were done must NOT inherit the
#    old plan's done markers / counters — PickNextMilestone on the new plan
#    picks milestone 1.
mkdir -p "$WORK/.ai/milestones/done" "$WORK/.ai/decisions" "$WORK/.tracker/turn_overrides"
for n in 1 2 3 4 5; do echo x > "$WORK/.ai/milestones/done/milestone-$n.md"; done
echo 2 > "$WORK/.ai/milestones/fix_attempts"; echo 1 > "$WORK/.ai/milestones/verify_fail_attempts"
echo old > "$WORK/.ai/milestones/current.md"
echo TestOld > "$WORK/.ai/milestones/known_failures"; echo TestOld > "$WORK/.ai/milestones/known_failures.snapshot"
echo G404 > "$WORK/.ai/milestones/known_lint_failures.snapshot"
echo abc > "$WORK/.ai/build/milestone-start-sha"; echo x > "$WORK/.ai/build/scoped-milestones.md"
echo 90 > "$WORK/.tracker/turn_overrides/Implement"
echo x > "$WORK/.ai/build/verify.sh"
run
check "re-plan exit 0"           "0" "$RC"
for f in .ai/milestones/done .ai/milestones/fix_attempts .ai/milestones/verify_fail_attempts .ai/milestones/current.md \
         .ai/milestones/known_failures .ai/milestones/known_failures.snapshot .ai/milestones/known_lint_failures.snapshot \
         .ai/build/milestone-start-sha .ai/build/scoped-milestones.md .tracker/turn_overrides; do
  check "re-plan cleared $f" "gone" "$([ -e "$WORK/$f" ] && echo present || echo gone)"
done
check "re-plan keeps verify.sh"  "present" "$([ -e "$WORK/.ai/build/verify.sh" ] && echo present || echo gone)"
check "re-plan keeps spec_forge_attempts" "2" "$(cat "$WORK/.ai/build/spec_forge_attempts")"
PICK="$(stage_script "$DIR/PickNextMilestone.sh")"
printf '## Milestone 1: New one\nbody\n## Milestone 2: New two\nbody\n' > "$WORK/.ai/decisions/milestones.md"
POUT="$( (cd "$WORK" && ${TEST_SH:-sh} "$PICK") 2>/dev/null)"
check "re-plan picks milestone 1"  "milestone-1" "$(printf '%s' "$POUT" | tail -1)"

if [ "$fail" = 0 ]; then echo "ALL PASS"; else echo "SOME FAILED"; exit 1; fi
