#!/bin/sh
set -eu
mkdir -p .tracker
TASK=$(cat docs/plans/current_task_id.txt)
COUNTER=".tracker/impl_count_${TASK}"
count=0
[ -f "$COUNTER" ] && count=$(cat "$COUNTER")
count=$((count + 1))
printf '%s' "$count" > "$COUNTER"
if [ "$count" -gt 5 ]; then
  printf 'budget_exhausted'
else
  printf 'budget_ok'
fi