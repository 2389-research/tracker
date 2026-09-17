set -eu
# Shared helpers via the engine-interpolated ${graph.workflow_dir} (author-
# controlled, safe-key allowlisted). Fail loud if empty.
[ -n "${graph.workflow_dir}" ] || { echo "ERROR: graph.workflow_dir is empty — cannot locate build_product's scripts/build_product/lib/ (embedded built-in: engine failed to materialize .tracker/workflow/; packed .dipx: unsupported, see #430)"; exit 1; }
LIB="${graph.workflow_dir}/scripts/build_product/lib"
. "$LIB/milestones.sh"
# Keep decisions, remove the build's transient working files. #640 B4:
# EscalateReview `retry` (re-plan) can follow Cleanup WITHOUT a Setup, so
# the runtime gate files the retry build needs stay in place —
# .ai/build/verify.sh, ci-probe.sh, iface-reachability-rubric.md,
# build-context.md, run-base-sha and the operator opt-in stamps
# (allow-dirty, no-tests-ok). What goes is exactly the per-plan set
# reset_plan_state clears (the same reset Setup/ResetReviewBudget apply —
# one list, so the two can't drift), plus the spec-forge counter and the
# review scratch; PickNextMilestone recreates .ai/build if it is ever
# missing.
reset_plan_state
rmdir .ai/milestones 2>/dev/null || true
rm -f .ai/build/spec_forge_attempts \
      .ai/build/review-diff.md .ai/build/review-claude.md .ai/build/review-codex.md .ai/build/review-gemini.md
# Keep: .ai/decisions/ (spec-analysis, milestones, requirement-coverage, review-synthesis, compliance)
echo "Preserved decision log in .ai/decisions/"
ls .ai/decisions/
printf 'cleanup-done'
