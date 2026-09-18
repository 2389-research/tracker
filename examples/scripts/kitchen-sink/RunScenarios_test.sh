#!/usr/bin/env bash
# ABOUTME: Fixture tests for kitchen-sink's RunScenarios.sh (#646 item 11) —
# ABOUTME: zero scenario files is a loud failure marker (never scenarios_pass),
# ABOUTME: and the final line is the routing marker the .dip matches on.
set -uo pipefail
DIR="$(cd "$(dirname "$0")" && pwd)"
fail=0
check() { # name expected actual
  if [ "$2" = "$3" ]; then echo "ok: $1"; else echo "FAIL: $1 — want '$2' got '$3'"; fail=1; fi
}
WORK="$(mktemp -d)"
trap 'rm -rf "$WORK"' EXIT
# No build system in $WORK, so the script runs each scenario with python3;
# a PATH shim makes that `sh` so the suite has no Python dependency.
mkdir -p "$WORK/bin"; printf '#!/bin/sh\nexec sh "$@"\n' > "$WORK/bin/python3"; chmod +x "$WORK/bin/python3"
run() { OUT="$( (cd "$WORK" && PATH="$WORK/bin:$PATH" ${TEST_SH:-sh} "$DIR/RunScenarios.sh") 2>&1)"; RC=$?; }
last() { printf '%s' "$OUT" | tail -1; }

# 1. No .scratch dir at all -> fail marker, exit 0 (routes to FixScenarioFailures).
run
check "no scratch exit 0"          "0" "$RC"
check "no scratch marker"          "scenarios_fail_none_found" "$(last)"

# 2. .scratch present but no scenario_* files -> same.
touch "$WORK/.scratch/notes.txt"
run
check "no scenario files marker"   "scenarios_fail_none_found" "$(last)"

# 3. Two scenarios, one red (no build system -> python3 runner, shimmed).
printf 'exit 0\n' > "$WORK/.scratch/scenario_a.py"
printf 'exit 1\n' > "$WORK/.scratch/scenario_b.py"
run
check "one red marker"             "scenarios_fail_1_of_2" "$(last)"

# 4. All green -> scenarios_pass as the LAST line (earlier lines are logs).
printf 'exit 0\n' > "$WORK/.scratch/scenario_b.py"
run
check "all green marker"           "scenarios_pass" "$(last)"
check "all green exit 0"           "0" "$RC"

if [ "$fail" = 0 ]; then echo "ALL PASS"; else echo "SOME FAILED"; exit 1; fi
