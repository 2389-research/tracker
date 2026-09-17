set -eu
# Shared helpers via the engine-interpolated ${graph.workflow_dir} (author-
# controlled, safe-key allowlisted). Fail loud if empty.
[ -n "${graph.workflow_dir}" ] || { echo "ERROR: graph.workflow_dir is empty — cannot locate build_product's scripts/build_product/lib/ (embedded built-in: engine failed to materialize .tracker/workflow/; packed .dipx: unsupported, see #430)"; exit 1; }
LIB="${graph.workflow_dir}/scripts/build_product/lib"
. "$LIB/counters.sh"
# Verify-fail loop breaker (#640 A4). Sits on the VerifyMilestone-fail edge,
# BEFORE FixMilestone. TestMilestone's fix_attempts counter is reset by the
# green run that precedes every verify rejection, so without this gate a
# verifier that keeps rejecting a green tree looped to the engine ceiling.
# Gate-BEFORE-work: -gt 3 = exactly 3 verify-driven fixes (do NOT change to
# -ge; #443 shape). Mirrors CheckSpecForgeBudget / CheckReviewFixBudget.
#
# Reset is NOT done here: MarkMilestoneDone clears the counter when the
# milestone is accepted, and Setup clears it on a fresh run. The routing
# marker is printed LAST with no trailing newline — the .dip routes on
# `ctx.outcome` (exit code); the marker is for the operator/diagnose.
mkdir -p .ai/milestones
BUDGET_FILE=".ai/milestones/verify_fail_attempts"
MAX_ATTEMPTS=3
bump_counter "$BUDGET_FILE"
if [ "$ATTEMPTS" -gt "$MAX_ATTEMPTS" ]; then
  printf 'verify-fail budget exhausted: %d attempts (max %d) — the verifier keeps rejecting this milestone; escalating\n' "$ATTEMPTS" "$MAX_ATTEMPTS"
  printf 'verify-budget-exhausted'
  exit 1
fi
printf 'verify-fail attempt %d of %d — handing the verifier findings to the fix loop\n' "$ATTEMPTS" "$MAX_ATTEMPTS"
printf 'verify-budget-ok'
