#!/bin/sh
set -eu
rm -f docs/plans/implementer-status.txt
PLAN='docs/plans/plan.md'
total=$(grep -c '^- \[.\] task-[0-9]' "$PLAN" 2>/dev/null || true)
if [ "$total" -eq 0 ]; then
  printf 'no_tasks_found'
  exit 0
fi
target=$(grep -oE 'task-[0-9]+' "$PLAN" | while read -r tid; do
  if grep -q "^- \[ \] $tid" "$PLAN"; then
    echo "$tid"
    break
  fi
done)
if [ -z "$target" ]; then
  printf 'all_complete'
  exit 0
fi
printf '%s' "$target" > docs/plans/current_task_id.txt
printf 'next_task-%s' "$target"