set -eu
# Shared helpers via the engine-interpolated ${graph.workflow_dir} (author-
# controlled, safe-key allowlisted). Fail loud if empty.
[ -n "${graph.workflow_dir}" ] || { echo "ERROR: graph.workflow_dir is empty — cannot locate build_product's scripts/build_product/lib/ (embedded built-in: engine failed to materialize .tracker/workflow/; packed .dipx: unsupported, see #430)"; exit 1; }
LIB="${graph.workflow_dir}/scripts/build_product/lib"
. "$LIB/milestones.sh"
# When the human picks "retry" at EscalateReview, the pipeline
# restarts at Decompose without going through Cleanup or Setup, so
# every per-PLAN state file from the prior build would otherwise
# persist into the re-planned build:
#   - `.ai/build/review_fix_attempts`: the very first ApplyReviewFixes
#     of the retry build would read the stale counter (already 1),
#     increment to 2, exceed MAX_ATTEMPTS=1, and immediately escalate
#     again — the retry would never get its one allowed re-review
#     pass (Codex P2 / Copilot on PR #264).
#   - `.ai/milestones/done/` (#640 B1): the old plan's done markers
#     would make PickNextMilestone report ALL_MILESTONES_COMPLETE for
#     the NEW plan, so nothing in it gets built. Likewise fix_attempts,
#     verify_fail_attempts, current.md, known_failures + snapshots,
#     milestone-start-sha and .tracker/turn_overrides.
# reset_plan_state (lib/milestones.sh) clears all of them — the same
# reset Setup applies to a fresh run. spec_forge_attempts is NOT
# touched: the retry re-enters at Decompose, past the SpecLint loop.
reset_plan_state
printf 'review-fix budget reset for retry'
