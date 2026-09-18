set -eu
# Shared helpers via the engine-interpolated ${graph.workflow_dir} (author-
# controlled, safe-key allowlisted; seeded by pipeline.SeedWorkflowDir for a
# disk load). Fail loud if empty. One copy of this wrapper per dotpowers-
# family workflow; the logic lives in scripts/dotpowers/lib/ (#646).
[ -n "${graph.workflow_dir}" ] || { echo "ERROR: graph.workflow_dir is empty — cannot locate scripts/dotpowers/lib/ (packed .dipx: unsupported, see #430)"; exit 1; }
LIB="${graph.workflow_dir}/scripts/dotpowers/lib"
[ -f "$LIB/tasks.sh" ] || { echo "ERROR: $LIB/tasks.sh not found — this workflow expects scripts/dotpowers/lib next to the .dip (move examples/scripts/dotpowers/ together with it)"; exit 1; }
. "$LIB/tasks.sh"
pick_next_task
