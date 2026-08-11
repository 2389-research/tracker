#!/bin/sh
set -eu
mkdir -p .scratch
if [ -z "$(ls -A .scratch/ 2>/dev/null)" ]; then
  printf 'scenarios_fail'
  exit 0
fi
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
echo "Results: $pass passed, $fail failed"
if [ "$fail" -gt 0 ]; then
  printf 'scenarios_fail'
else
  printf 'scenarios_pass'
fi