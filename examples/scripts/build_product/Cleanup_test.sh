#!/usr/bin/env bash
# ABOUTME: Fixture tests for Cleanup.sh — removes the per-plan counters, markers
# ABOUTME: and review scratch plus the turn-override dir; preserves the runtime
# ABOUTME: gate files a post-Cleanup retry needs (#640 B4), .ai/decisions/ (the
# ABOUTME: durable decision log) and everything else under .tracker/.
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
SCRIPT="$(stage_script "$DIR/Cleanup.sh")"   # ${graph.workflow_dir} expanded as the engine does
run() { OUT="$( (cd "$WORK" && ${TEST_SH:-sh} "$SCRIPT") 2>"$STATE/stderr")"; RC=$?; }
last() { printf '%s' "$OUT" | tail -1; }
exists() { [ -e "$WORK/$1" ] && echo present || echo gone; }

# 1. Full run-end state.
mkdir -p "$WORK/.ai/build" "$WORK/.ai/milestones/done" "$WORK/.ai/decisions" \
         "$WORK/.tracker/turn_overrides" "$WORK/.tracker/runs/abc" "$WORK/.tracker/inputs"
echo x > "$WORK/.ai/build/verify.sh"; echo x > "$WORK/.ai/build/ci-probe.sh"
echo x > "$WORK/.ai/build/iface-reachability-rubric.md"; echo x > "$WORK/.ai/build/build-context.md"
echo abc > "$WORK/.ai/build/run-base-sha"; touch "$WORK/.ai/build/allow-dirty" "$WORK/.ai/build/no-tests-ok"
for f in review_fix_attempts spec_forge_attempts milestone-start-sha declared-files.raw declared-files.list \
         scoped-milestones.md review-diff.md review-claude.md review-codex.md review-gemini.md; do echo x > "$WORK/.ai/build/$f"; done
echo x > "$WORK/.ai/milestones/done/milestone-1.md"
echo x > "$WORK/.ai/decisions/milestones.md"
echo x > "$WORK/.ai/decisions/review-synthesis.md"
echo 90 > "$WORK/.tracker/turn_overrides/Implement"
echo x > "$WORK/.tracker/runs/abc/checkpoint.json"
echo x > "$WORK/.tracker/inputs/spec"
run
check "exit 0"                        "0" "$RC"
for f in review_fix_attempts spec_forge_attempts milestone-start-sha declared-files.raw declared-files.list \
         scoped-milestones.md review-diff.md review-claude.md review-codex.md review-gemini.md; do
  check "#640 B4 transient .ai/build/$f removed" "gone" "$(exists ".ai/build/$f")"
done
for f in verify.sh ci-probe.sh iface-reachability-rubric.md build-context.md run-base-sha allow-dirty no-tests-ok; do
  check "#640 B4 runtime .ai/build/$f kept" "present" "$(exists ".ai/build/$f")"
done
check ".ai/milestones removed"        "gone" "$(exists .ai/milestones)"
check "turn_overrides removed"        "gone" "$(exists .tracker/turn_overrides)"
check ".ai/decisions kept"            "present" "$(exists .ai/decisions/milestones.md)"
check "review-synthesis kept"         "present" "$(exists .ai/decisions/review-synthesis.md)"
check ".tracker/runs untouched"       "present" "$(exists .tracker/runs/abc/checkpoint.json)"
check ".tracker/inputs untouched"     "present" "$(exists .tracker/inputs/spec)"
check "decision log listed"           "yes" "$(printf '%s' "$OUT" | grep -q 'milestones.md' && echo yes || echo no)"
check "preserved notice"              "yes" "$(printf '%s' "$OUT" | grep -q 'Preserved decision log in .ai/decisions/' && echo yes || echo no)"
check "marker last line"              "cleanup-done" "$(last)"

# 2. Idempotent: a second run with the dirs already gone still exits 0.
run
check "second run exit 0"             "0" "$RC"
check "marker again"                  "cleanup-done" "$(last)"

# 2b. #640 B4: a retry after Cleanup (EscalateReview retry -> Decompose,
#     no Setup) can still pick a milestone: PickNextMilestone runs green on
#     the post-Cleanup tree and TestMilestone's gate script is still there.
PICK="$(stage_script "$DIR/PickNextMilestone.sh")"
mkdir -p "$WORK/.ai/decisions"; printf '## Milestone 1: One\nbody\n' > "$WORK/.ai/decisions/milestones.md"
POUT="$( (cd "$WORK" && ${TEST_SH:-sh} "$PICK") 2>/dev/null)"; PRC=$?
check "post-Cleanup Pick exit 0"      "0" "$PRC"
check "post-Cleanup Pick marker"      "milestone-1" "$(printf '%s' "$POUT" | tail -1)"
check "post-Cleanup start-sha written" "present" "$(exists .ai/build/milestone-start-sha)"
check "post-Cleanup verify.sh present" "present" "$(exists .ai/build/verify.sh)"

# 3. Contract: .ai/decisions/ must exist (Setup always creates it). Without
#    it the `ls` fails under set -e and the marker is NOT printed — pinned so
#    a future refactor that drops the Setup scaffolding notices.
rm -rf "$WORK/.ai"
run
check "no decisions dir -> exit 1"    "1" "$RC"
check "no marker on failure"          "no" "$(printf '%s' "$OUT" | grep -q 'cleanup-done' && echo yes || echo no)"

if [ "$fail" = 0 ]; then echo "ALL PASS"; else echo "SOME FAILED"; exit 1; fi
