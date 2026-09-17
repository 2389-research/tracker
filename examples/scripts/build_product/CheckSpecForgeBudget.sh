set -eu
# Shared helpers via the engine-interpolated ${graph.workflow_dir} (author-
# controlled, safe-key allowlisted). Fail loud if empty.
[ -n "${graph.workflow_dir}" ] || { echo "ERROR: graph.workflow_dir is empty — cannot locate build_product's scripts/build_product/lib/ (embedded built-in: engine failed to materialize .tracker/workflow/; packed .dipx: unsupported, see #430)"; exit 1; }
LIB="${graph.workflow_dir}/scripts/build_product/lib"
. "$LIB/counters.sh"
# Mirrors CheckReviewFixBudget, plus two fixes the clone must not omit: a
# numeric guard (a corrupted counter must not abort under set -eu) and an
# idempotent snapshot of the ORIGINAL spec on first entry (the fidelity
# gate diffs against it; ApprovePlan surfaces it). Gate-BEFORE-work: -gt 3
# = exactly 3 ForgeSpec attempts (do NOT change to -ge; #443 shape).
mkdir -p .ai/build .ai/decisions
if [ ! -f .ai/decisions/SPEC.original.md ]; then
  cp SPEC.md .ai/decisions/SPEC.original.md
fi
BUDGET_FILE=".ai/build/spec_forge_attempts"
MAX_ATTEMPTS=3
bump_counter "$BUDGET_FILE"
if [ "$ATTEMPTS" -gt "$MAX_ATTEMPTS" ]; then
  printf 'spec-forge budget exhausted: %d attempts (max %d) — spec could not be hardened autonomously\n' "$ATTEMPTS" "$MAX_ATTEMPTS"
  exit 1
fi
printf 'spec-forge budget OK: attempt %d of %d\n' "$ATTEMPTS" "$MAX_ATTEMPTS"