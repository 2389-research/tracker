set -eu
# Keep decisions, remove the build's transient working files. #640 B4:
# EscalateReview `retry` (re-plan) can follow Cleanup WITHOUT a Setup, so
# the runtime gate files the retry build needs stay in place —
# .ai/build/verify.sh, ci-probe.sh, iface-reachability-rubric.md,
# build-context.md, run-base-sha and the operator opt-in stamps
# (allow-dirty, no-tests-ok). Only the per-plan counters, markers and
# scratch go (the same set ResetReviewBudget clears, plus the spec-forge
# counter and the review scratch); PickNextMilestone recreates .ai/build
# if it is ever missing.
rm -rf .ai/milestones .tracker/turn_overrides
for f in review_fix_attempts spec_forge_attempts milestone-start-sha \
         declared-files.raw declared-files.list scoped-milestones.md \
         review-diff.md review-claude.md review-codex.md review-gemini.md; do
  rm -f ".ai/build/$f"
done
# Keep: .ai/decisions/ (spec-analysis, milestones, requirement-coverage, review-synthesis, compliance)
echo "Preserved decision log in .ai/decisions/"
ls .ai/decisions/
printf 'cleanup-done'
