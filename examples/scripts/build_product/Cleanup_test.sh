#!/usr/bin/env bash
# ABOUTME: Fixture tests for Cleanup.sh — removes build working files and the
# ABOUTME: turn-override dir, preserves .ai/decisions/ (the durable decision
# ABOUTME: log) and everything else under .tracker/.
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
run() { OUT="$( (cd "$WORK" && sh "$SCRIPT") 2>"$STATE/stderr")"; RC=$?; }
last() { printf '%s' "$OUT" | tail -1; }
exists() { [ -e "$WORK/$1" ] && echo present || echo gone; }

# 1. Full run-end state.
mkdir -p "$WORK/.ai/build" "$WORK/.ai/milestones/done" "$WORK/.ai/decisions" \
         "$WORK/.tracker/turn_overrides" "$WORK/.tracker/runs/abc" "$WORK/.tracker/inputs"
echo x > "$WORK/.ai/build/verify.sh"
echo x > "$WORK/.ai/milestones/done/milestone-1.md"
echo x > "$WORK/.ai/decisions/milestones.md"
echo x > "$WORK/.ai/decisions/review-synthesis.md"
echo 90 > "$WORK/.tracker/turn_overrides/Implement"
echo x > "$WORK/.tracker/runs/abc/checkpoint.json"
echo x > "$WORK/.tracker/inputs/spec"
run
check "exit 0"                        "0" "$RC"
check ".ai/build removed"             "gone" "$(exists .ai/build)"
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

# 3. Contract: .ai/decisions/ must exist (Setup always creates it). Without
#    it the `ls` fails under set -e and the marker is NOT printed — pinned so
#    a future refactor that drops the Setup scaffolding notices.
rm -rf "$WORK/.ai"
run
check "no decisions dir -> exit 1"    "1" "$RC"
check "no marker on failure"          "no" "$(printf '%s' "$OUT" | grep -q 'cleanup-done' && echo yes || echo no)"

if [ "$fail" = 0 ]; then echo "ALL PASS"; else echo "SOME FAILED"; exit 1; fi
