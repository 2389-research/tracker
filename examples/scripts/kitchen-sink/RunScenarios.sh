#!/bin/sh
# Runs every .scratch/scenario_* file with the project's runtime and prints
# the routing marker as the LAST line (the .dip matches with `endswith`):
#   scenarios_pass | scenarios_fail_<n>_of_<total> | scenarios_fail_none_found
# #646 item 11: zero scenario files used to print scenarios_pass — a
# WriteScenarios pass that produced nothing validated nothing and shipped.
set -eu
mkdir -p .scratch
failed=0
passed=0
for f in .scratch/scenario_*; do
  [ -f "$f" ] || continue
  echo "=== Running $f ==="
  if [ -f pyproject.toml ]; then
    uv run python "$f" 2>&1 || { failed=$((failed + 1)); continue; }
  elif [ -f package.json ]; then
    node "$f" 2>&1 || { failed=$((failed + 1)); continue; }
  elif [ -f go.mod ]; then
    go run "$f" 2>&1 || { failed=$((failed + 1)); continue; }
  else
    python3 "$f" 2>&1 || { failed=$((failed + 1)); continue; }
  fi
  passed=$((passed + 1))
done
if [ "$((failed + passed))" -eq 0 ]; then
  echo 'ERROR: no .scratch/scenario_* files — WriteScenarios produced nothing to validate'
  printf 'scenarios_fail_none_found'
  exit 0
fi
echo "Results: $passed passed, $failed failed"
if [ "$failed" -gt 0 ]; then
  printf 'scenarios_fail_%d_of_%d' "$failed" "$((failed + passed))"
else
  printf 'scenarios_pass'
fi
