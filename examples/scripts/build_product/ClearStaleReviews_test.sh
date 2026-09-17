#!/usr/bin/env bash
# ABOUTME: Fixture tests for ClearStaleReviews.sh (#313) — wipes the three
# ABOUTME: reviewer reports before every ReviewParallel fan-out so a stale
# ABOUTME: file from a prior pass can never satisfy CheckReviewsComplete.
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
SCRIPT="$(stage_script "$DIR/ClearStaleReviews.sh")"   # ${graph.workflow_dir} expanded as the engine does
run() { OUT="$( (cd "$WORK" && sh "$SCRIPT") 2>"$STATE/stderr")"; RC=$?; }
last() { printf '%s' "$OUT" | tail -1; }

# 1. All three prior-round reports are removed; unrelated build files stay.
mkdir -p "$WORK/.ai/build"
for f in review-claude.md review-codex.md review-gemini.md; do echo stale > "$WORK/.ai/build/$f"; done
echo keep > "$WORK/.ai/build/review-synthesis.md"
echo keep > "$WORK/.ai/build/review-diff.md"
run
check "exit 0"                     "0" "$RC"
for f in review-claude.md review-codex.md review-gemini.md; do
  check "$f removed" "gone" "$([ -e "$WORK/.ai/build/$f" ] && echo present || echo gone)"
done
check "review-synthesis.md kept"   "keep" "$(cat "$WORK/.ai/build/review-synthesis.md")"
check "review-diff.md kept"        "keep" "$(cat "$WORK/.ai/build/review-diff.md")"
check "marker line"                "cleared stale review reports" "$(last)"

# 2. First entry (no .ai/build yet — e.g. the accept override path) scaffolds
#    the dir and exits 0 instead of tripping set -e on a missing directory.
rm -rf "$WORK/.ai"
run
check "fresh workdir exit 0"       "0" "$RC"
check ".ai/build created"          "yes" "$([ -d "$WORK/.ai/build" ] && echo yes || echo no)"

if [ "$fail" = 0 ]; then echo "ALL PASS"; else echo "SOME FAILED"; exit 1; fi
