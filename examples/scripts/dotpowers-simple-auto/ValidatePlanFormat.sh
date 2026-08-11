#!/bin/sh
set -eu
PLAN='docs/plans/plan.md'
mkdir -p .tracker
COUNTER='.tracker/plan_validate_count'
GEN='.tracker/plan_validate_gen'
cur_gen=$(ls -1d .tracker/runs/*/ 2>/dev/null | tail -1 || echo 'none')
old_gen=''
[ -f "$GEN" ] && old_gen=$(cat "$GEN")
if [ "$cur_gen" != "$old_gen" ]; then
  printf '0' > "$COUNTER"
  printf '%s' "$cur_gen" > "$GEN"
fi
vc=0
[ -f "$COUNTER" ] && vc=$(cat "$COUNTER")
vc=$((vc + 1))
printf '%s' "$vc" > "$COUNTER"
if [ "$vc" -gt 5 ]; then
  printf 'fail_retries_exhausted'
  exit 0
fi
if [ ! -s "$PLAN" ]; then
  printf 'fail_no_plan'
  exit 0
fi
count=$(grep -c '^- \[ \] task-[0-9]\+:' "$PLAN" || true)
if [ "$count" -eq 0 ]; then
  printf 'fail_no_checkboxes'
  exit 0
fi
handwaves=$(grep -ciE '(write a complete implementation|add appropriate|add validation logic|add error handling logic|add the logic)' "$PLAN" || true)
if [ "$handwaves" -gt 0 ]; then
  printf 'fail_handwaves'
  exit 0
fi
printf 'format_ok'