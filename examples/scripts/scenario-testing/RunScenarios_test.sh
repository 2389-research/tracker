#!/usr/bin/env bash
# ABOUTME: Fixture tests for scenario-testing's RunScenarios.sh (#646 item 11
# ABOUTME: sibling) — zero scenario files is a loud failure marker, and the
# ABOUTME: final line is the routing marker the .dip matches on (endswith).
set -uo pipefail
DIR="$(cd "$(dirname "$0")" && pwd)"
fail=0
check() { # name expected actual
  if [ "$2" = "$3" ]; then echo "ok: $1"; else echo "FAIL: $1 — want '$2' got '$3'"; fail=1; fi
}
WORK="$(mktemp -d)"
trap 'rm -rf "$WORK"' EXIT
run() { OUT="$( (cd "$WORK" && ${TEST_SH:-sh} "$DIR/RunScenarios.sh") 2>&1)"; RC=$?; }
last() { printf '%s' "$OUT" | tail -1; }

run
check "no scratch marker"          "scenarios_fail" "$(last)"
check "no scratch exit 0"          "0" "$RC"
touch "$WORK/.scratch/notes.txt"
run
check "no scenario files marker"   "scenarios_fail" "$(last)"
printf 'exit 0\n' > "$WORK/.scratch/scenario_a.sh"
printf 'exit 1\n' > "$WORK/.scratch/scenario_b.sh"
run
check "one red marker"             "scenarios_fail" "$(last)"
check "one red summary"            "yes" "$(printf '%s' "$OUT" | grep -q '1 passed, 1 failed' && echo yes || echo no)"
printf 'exit 0\n' > "$WORK/.scratch/scenario_b.sh"
run
check "all green marker"           "scenarios_pass" "$(last)"

if [ "$fail" = 0 ]; then echo "ALL PASS"; else echo "SOME FAILED"; exit 1; fi
