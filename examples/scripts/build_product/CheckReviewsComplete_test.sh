#!/usr/bin/env bash
# ABOUTME: Fixture tests for CheckReviewsComplete.sh (#313 defense-in-depth) —
# ABOUTME: all THREE reviewer reports must be present AND non-empty; the gate
# ABOUTME: routes on exit code only, so stdout stays free of marker noise.
set -uo pipefail
DIR="$(cd "$(dirname "$0")" && pwd)"
SCRIPT="$DIR/CheckReviewsComplete.sh"
fail=0
check() { # name expected actual
  if [ "$2" = "$3" ]; then echo "ok: $1"; else echo "FAIL: $1 — want '$2' got '$3'"; fail=1; fi
}
WORK="$(mktemp -d)"
STATE="$(mktemp -d)"
trap 'rm -rf "$WORK" "$STATE"' EXIT
run() { OUT="$( (cd "$WORK" && sh "$SCRIPT") 2>"$STATE/stderr")"; RC=$?; ERR="$(cat "$STATE/stderr")"; }
seed() { # write the named reports (space-separated) as non-empty
  rm -rf "$WORK/.ai" && mkdir -p "$WORK/.ai/build"
  for f in "$@"; do echo "# review" > "$WORK/.ai/build/$f"; done
}

# 1. All three present and non-empty -> exit 0; stdout EMPTY (routes on
#    ctx.outcome, not stdout); OK diagnostic on stderr.
seed review-claude.md review-codex.md review-gemini.md
run
check "all present exit 0"        "0" "$RC"
check "stdout empty"              "" "$OUT"
check "stderr OK line"            "yes" "$(printf '%s' "$ERR" | grep -q 'review-gate OK: all 3 reviews present' && echo yes || echo no)"

# 2. The adversarial (gemini) report missing -> exit 1, named on stderr.
seed review-claude.md review-codex.md
run
check "gemini missing exit 1"     "1" "$RC"
check "stdout still empty"        "" "$OUT"
check "names missing file"        "yes" "$(printf '%s' "$ERR" | grep -q 'review-gate FAIL: missing/empty review(s): review-gemini.md' && echo yes || echo no)"

# 3. An EMPTY report counts as missing (a reviewer that "succeeded" but wrote nothing).
seed review-claude.md review-codex.md review-gemini.md
: > "$WORK/.ai/build/review-codex.md"
run
check "empty report exit 1"       "1" "$RC"
check "empty report named"        "yes" "$(printf '%s' "$ERR" | grep -q 'review-codex.md' && echo yes || echo no)"

# 4. No 2-of-3 quorum: two missing lists both.
seed review-claude.md
run
check "two missing exit 1"        "1" "$RC"
check "both named"                "yes" "$(printf '%s' "$ERR" | grep -q 'review-codex.md review-gemini.md' && echo yes || echo no)"

# 5. No .ai/build at all -> exit 1 (never a silent pass).
rm -rf "$WORK/.ai"
run
check "no build dir exit 1"       "1" "$RC"

if [ "$fail" = 0 ]; then echo "ALL PASS"; else echo "SOME FAILED"; exit 1; fi
