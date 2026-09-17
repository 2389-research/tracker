set -eu
# Shared helpers via the engine-interpolated ${graph.workflow_dir} (author-
# controlled, safe-key allowlisted). Fail loud if empty.
[ -n "${graph.workflow_dir}" ] || { echo "ERROR: graph.workflow_dir is empty — cannot locate build_product's scripts/build_product/lib/ (embedded built-in: engine failed to materialize .tracker/workflow/; packed .dipx: unsupported, see #430)"; exit 1; }
LIB="${graph.workflow_dir}/scripts/build_product/lib"
. "$LIB/counters.sh"
# Per-build counter that caps the re-review loop after
# ApplyReviewFixes. Without this gate, a fix that introduces
# zero-assertion / wrong-target / DI-bypass regressions (the
# reviewer-layer W4/W5/W13 smells from Gap 8) would never be
# re-reviewed before Done — reviewers run pre-fix only and
# FinalSpecCheck deliberately scopes to W17 sleep-fence +
# iface-reachability + SPEC.md compliance.
#
# Budget = 1 re-review pass. If the first re-review surfaces
# new issues that ApplyReviewFixes can't resolve cleanly,
# escalate to a human rather than burn LLM cycles forever.
# Closes Gap 5.3 of issue #233.
#
# #640 E3: the counter goes through the shared bump_counter (numeric guard —
# a corrupted file reads as 0 instead of aborting under set -eu, and a
# failed write fails loud), like its three sibling gates.
BUDGET_FILE=".ai/build/review_fix_attempts"
MAX_ATTEMPTS=1
mkdir -p .ai/build
bump_counter "$BUDGET_FILE"
if [ "$ATTEMPTS" -gt "$MAX_ATTEMPTS" ]; then
  printf 'budget-exhausted: review-fix loop ran %d times (max %d allowed re-reviews)\n' "$ATTEMPTS" "$MAX_ATTEMPTS"
  exit 1
fi
printf 'review-fix budget OK: re-review pass %d of %d\n' "$ATTEMPTS" "$MAX_ATTEMPTS"
