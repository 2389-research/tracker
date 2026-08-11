#!/bin/sh
set -eu
mkdir -p .scratch
if [ ! -d .scratch ] || [ -z "$(ls -A .scratch/ 2>/dev/null)" ]; then
  printf 'scenarios_pass'
  exit 0
fi
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
if [ "$failed" -gt 0 ]; then
  printf 'scenarios_fail_%d_of_%d' "$failed" "$((failed + passed))"
else
  printf 'scenarios_pass'
fi