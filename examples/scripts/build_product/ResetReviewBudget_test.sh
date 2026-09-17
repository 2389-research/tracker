#!/usr/bin/env bash
# ABOUTME: Fixture tests for ResetReviewBudget.sh — the EscalateReview "retry"
# ABOUTME: path clears .ai/build/review_fix_attempts so the re-planned build
# ABOUTME: gets its one allowed re-review pass again (Codex P2 / Copilot on #264).
set -uo pipefail
DIR="$(cd "$(dirname "$0")" && pwd)"
SCRIPT="$DIR/ResetReviewBudget.sh"
fail=0
check() { # name expected actual
  if [ "$2" = "$3" ]; then echo "ok: $1"; else echo "FAIL: $1 — want '$2' got '$3'"; fail=1; fi
}
WORK="$(mktemp -d)"
STATE="$(mktemp -d)"
trap 'rm -rf "$WORK" "$STATE"' EXIT
# The script is run with POSIX sh (dippin runs command_file via `sh -c`;
# the shebang is ignored — tracker #324).
run() { OUT="$( (cd "$WORK" && sh "$SCRIPT") 2>"$STATE/stderr")"; RC=$?; }
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

if [ "$fail" = 0 ]; then echo "ALL PASS"; else echo "SOME FAILED"; exit 1; fi
