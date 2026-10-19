set -eu
# Cap the revise loop: three revisions per run, then exit 1 so the
# FinalDecision gate (approve or reject what is there) takes over. The counter
# is per run: Preflight removes it. max_restarts in the .dip stays above this
# as a backstop, since exhausting it hard-fails the run.
[ -n "${graph.workflow_dir}" ] || { echo "ERROR: CheckReviseBudget: graph.workflow_dir is empty — cannot locate scripts/quickfix/lib/" >&2; exit 1; }
LIB="${graph.workflow_dir}/scripts/quickfix/lib"
. "$LIB/counters.sh"
MAX=3
mkdir -p .tracker/quickfix
bump_counter .tracker/quickfix/revise_count
if [ "$ATTEMPTS" -gt "$MAX" ]; then
  printf 'revise budget exhausted: %d revisions already made (max %d) — approve or reject what is there\n' "$MAX" "$MAX"
  exit 1
fi
printf 'revision %d of %d\n' "$ATTEMPTS" "$MAX"
