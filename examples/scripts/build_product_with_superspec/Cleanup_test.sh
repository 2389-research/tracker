#!/usr/bin/env bash
# ABOUTME: Fixture tests for superspec Cleanup.sh — removes the run's
# ABOUTME: worktrees/streams/gates scratch, preserves .ai/decisions/ and lists it,
# ABOUTME: marker last; tolerant of an already-clean workdir.
set -uo pipefail
DIR="$(cd "$(dirname "$0")" && pwd)"
fail=0
check() { # name expected actual
  if [ "$2" = "$3" ]; then echo "ok: $1"; else echo "FAIL: $1 — want '$2' got '$3'"; fail=1; fi
}
WORK="$(mktemp -d)"
trap 'rm -rf "$WORK"' EXIT
run() { OUT="$( (cd "$WORK" && ${TEST_SH:-sh} "$DIR/Cleanup.sh") 2>&1)"; RC=$?; }
last() { printf '%s' "$OUT" | tail -1; }
mkdir -p "$WORK/.ai/worktrees/stream-a" "$WORK/.ai/streams" "$WORK/.ai/gates" "$WORK/.ai/decisions"
echo x > "$WORK/.ai/gates/final.txt"; echo keep > "$WORK/.ai/decisions/final-compliance.md"
run
check "exit 0"                 "0" "$RC"
check "marker last"            "cleanup-done" "$(last)"
check "worktrees dir gone"     "gone" "$([ -e "$WORK/.ai/worktrees" ] && echo present || echo gone)"
check "gates dir gone"         "gone" "$([ -e "$WORK/.ai/gates" ] && echo present || echo gone)"
check "decisions preserved"    "keep" "$(cat "$WORK/.ai/decisions/final-compliance.md")"
check "decisions listed"       "yes" "$(printf '%s' "$OUT" | grep -q 'final-compliance.md' && echo yes || echo no)"
run
check "already clean: exit 0"  "0" "$RC"
[ "$fail" = 0 ] && echo "ALL PASS" || { echo "SOME FAILED"; exit 1; }
