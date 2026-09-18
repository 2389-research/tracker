#!/bin/sh
# Runs every .scratch/scenario_* script and prints the routing marker as the
# LAST line (the .dip matches with `endswith`): scenarios_pass | scenarios_fail.
# #646 item 11: zero scenario files is a failure — nothing was validated.
set -eu
mkdir -p .scratch
pass=0
fail=0
for f in .scratch/scenario_*; do
  [ -f "$f" ] || continue
  echo "=== Running: $f ==="
  chmod +x "$f" 2>/dev/null || true
  if sh "$f" 2>&1; then
    echo "PASS: $f"
    pass=$((pass + 1))
  else
    echo "FAIL: $f"
    fail=$((fail + 1))
  fi
done
if [ "$((pass + fail))" -eq 0 ]; then
  echo 'ERROR: no .scratch/scenario_* files — WriteScenarios produced nothing to validate'
  printf 'scenarios_fail'
  exit 0
fi
echo "Results: $pass passed, $fail failed"
if [ "$fail" -gt 0 ]; then
  printf 'scenarios_fail'
else
  printf 'scenarios_pass'
fi
