set -eu
# Shared helpers via the engine-interpolated ${graph.workflow_dir} (author-
# controlled, safe-key allowlisted; seeded by pipeline.SeedWorkflowDir for a
# disk load). Fail loud if empty. One copy of this wrapper per dotpowers-
# family workflow; the logic lives in scripts/dotpowers/lib/ (#646).
[ -n "${graph.workflow_dir}" ] || { echo "ERROR: graph.workflow_dir is empty — cannot locate scripts/dotpowers/lib/ (packed .dipx: unsupported, see #430)"; exit 1; }
LIB="${graph.workflow_dir}/scripts/dotpowers/lib"
. "$LIB/counters.sh"
# Per-task implementation attempts, capped at 5 (gate-after-bump, -gt).
mkdir -p .tracker
TASK=$(cat docs/plans/current_task_id.txt)
bump_counter ".tracker/impl_count_${TASK}"
if [ "$COUNT" -gt 5 ]; then
  printf 'budget_exhausted'
else
  printf 'budget_ok'
fi
