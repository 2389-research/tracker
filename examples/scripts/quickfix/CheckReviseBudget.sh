set -eu
# Cap the revise loop: three revisions per run. Prints exactly one routing
# marker on stdout — revise-ok (another attempt) or revise-exhausted (the
# FinalDecision gate: approve or reject what is there) — and exits 0 either
# way: running out of revisions is not a failure, so the gate is not reached
# through a fail edge (which would read as overriding a failed validation).
# Exit 1 only when the counter can't be written. The counter is per run:
# Preflight removes it. max_restarts in the .dip stays above this as a
# backstop, since exhausting it hard-fails the run.
[ -n "${graph.workflow_dir}" ] || { echo "ERROR: CheckReviseBudget: graph.workflow_dir is empty — cannot locate scripts/quickfix/lib/" >&2; exit 1; }
LIB="${graph.workflow_dir}/scripts/quickfix/lib"
. "$LIB/counters.sh"
MAX=3
mkdir -p .tracker/quickfix
bump_counter .tracker/quickfix/revise_count
if [ "$ATTEMPTS" -gt "$MAX" ]; then
  printf 'revise budget used up: %d revisions already made (max %d) — approve or reject what is there\n' "$MAX" "$MAX" >&2
  printf 'revise-exhausted'
  exit 0
fi
printf 'revision %d of %d\n' "$ATTEMPTS" "$MAX" >&2
printf 'revise-ok'
