#!/usr/bin/env bash
# ABOUTME: Fixture tests for RetryMerge.sh (#646 5c) — prints the exact
# ABOUTME: retry-merge-phase<N> marker for the phase merge_streams recorded, and
# ABOUTME: fails loud (no marker) when no valid phase is recorded.
set -uo pipefail
DIR="$(cd "$(dirname "$0")" && pwd)"
fail=0
check() { # name expected actual
  if [ "$2" = "$3" ]; then echo "ok: $1"; else echo "FAIL: $1 — want '$2' got '$3'"; fail=1; fi
}
WORK="$(mktemp -d)"
trap 'rm -rf "$WORK"' EXIT
run() { OUT="$( (cd "$WORK" && ${TEST_SH:-sh} "$DIR/RetryMerge.sh") 2>&1)"; RC=$?; }
last() { printf '%s' "$OUT" | tail -1; }
run
check "no file: exit 1"        "1" "$RC"
check "no file: no marker"     "no" "$(printf '%s' "$OUT" | grep -q 'retry-merge-phase' && echo yes || echo no)"
mkdir -p "$WORK/.ai/build"
for p in 1 2 4 5; do
  echo "$p" > "$WORK/.ai/build/merge-phase"; run
  check "phase $p: exit 0"     "0" "$RC"
  check "phase $p: marker last" "retry-merge-phase$p" "$(last)"
done
echo 3 > "$WORK/.ai/build/merge-phase"; run
check "phase 3 (no such merge): exit 1" "1" "$RC"
echo junk > "$WORK/.ai/build/merge-phase"; run
check "junk: exit 1"           "1" "$RC"
[ "$fail" = 0 ] && echo "ALL PASS" || { echo "SOME FAILED"; exit 1; }
