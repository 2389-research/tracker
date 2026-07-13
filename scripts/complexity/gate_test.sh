#!/usr/bin/env bash
# ABOUTME: Fixture tests for the complexity ratchet's compare logic (gate.sh check).
set -uo pipefail
cd "$(dirname "$0")"
GATE=./gate.sh
fails=0
check() { # desc expected_exit baseline_content current_content
  local desc="$1" want="$2"; shift 2
  local b c; b=$(mktemp); c=$(mktemp)
  printf '%s\n' "$1" > "$b"; printf '%s\n' "$2" > "$c"
  bash "$GATE" check "$b" "$c" >/dev/null 2>&1; local got=$?
  if [ "$got" != "$want" ]; then echo "FAIL: $desc (want exit $want, got $got)"; fails=$((fails+1)); else echo "ok: $desc"; fi
  rm -f "$b" "$c"
}
BASE=$'cyclo|./pipeline/a.go|Foo|12\ncognitive|./pipeline/a.go|Bar|20\nfilesize|./x.go|-|700'
check "subset passes"        0 "$BASE" "$BASE" "cyclo|./pipeline/a.go|Foo|12"
check "improved-but-over passes" 0 "$BASE" "cyclo|./pipeline/a.go|Foo|10"
check "new violation fails"  1 "$BASE" "cyclo|./pipeline/b.go|New|9"
check "worse value fails"    1 "$BASE" "cyclo|./pipeline/a.go|Foo|13"
check "grown file fails"     1 "$BASE" "filesize|./x.go|-|900"
check "empty current passes" 0 "$BASE" ""
[ "$fails" -eq 0 ] && { echo "ALL PASS"; exit 0; } || { echo "$fails FAILED"; exit 1; }
