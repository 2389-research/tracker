set -eu
# Shared helpers via the engine-interpolated ${graph.workflow_dir} (author-
# controlled, safe-key allowlisted; seeded by pipeline.SeedWorkflowDir for a
# disk load). Fail loud if empty. One copy of this wrapper per dotpowers-
# family workflow; the logic lives in scripts/dotpowers/lib/ (#646).
[ -n "${graph.workflow_dir}" ] || { echo "ERROR: graph.workflow_dir is empty — cannot locate scripts/dotpowers/lib/ (packed .dipx: unsupported, see #430)"; exit 1; }
LIB="${graph.workflow_dir}/scripts/dotpowers/lib"
[ -f "$LIB/tasks.sh" ] || { echo "ERROR: $LIB/tasks.sh not found — this workflow expects scripts/dotpowers/lib next to the .dip (move examples/scripts/dotpowers/ together with it)"; exit 1; }
. "$LIB/counters.sh"
PLAN='docs/plans/plan.md'
mkdir -p .tracker
COUNTER='.tracker/plan_validate_count'
GEN='.tracker/plan_validate_gen'
# The validate budget is per run generation (newest .tracker/runs/<id>/):
# a new run starts the count at 0.
cur_gen=$(ls -1d .tracker/runs/*/ 2>/dev/null | tail -1 || echo 'none')
old_gen=''
[ -f "$GEN" ] && old_gen=$(cat "$GEN")
if [ "$cur_gen" != "$old_gen" ]; then
  printf '0' > "$COUNTER"
  printf '%s' "$cur_gen" > "$GEN"
fi
bump_counter "$COUNTER"
if [ "$COUNT" -gt 5 ]; then
  printf 'fail_retries_exhausted'
  exit 0
fi
if [ ! -s "$PLAN" ]; then
  printf 'fail_no_plan'
  exit 0
fi
count=$(grep -c '^- \[ \] task-[0-9][0-9]*:' "$PLAN" || true)
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
