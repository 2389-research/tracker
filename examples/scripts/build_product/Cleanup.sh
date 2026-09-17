set -eu
# Keep decisions, remove build working files
rm -rf .ai/build .ai/milestones .tracker/turn_overrides
# Keep: .ai/decisions/ (spec-analysis, milestones, requirement-coverage, review-synthesis, compliance)
echo "Preserved decision log in .ai/decisions/"
ls .ai/decisions/
printf 'cleanup-done'