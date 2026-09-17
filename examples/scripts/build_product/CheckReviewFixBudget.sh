set -eu
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
BUDGET_FILE=".ai/build/review_fix_attempts"
MAX_ATTEMPTS=1
ATTEMPTS=0
if [ -f "$BUDGET_FILE" ]; then
  ATTEMPTS=$(cat "$BUDGET_FILE")
fi
ATTEMPTS=$((ATTEMPTS + 1))
mkdir -p .ai/build
echo "$ATTEMPTS" > "$BUDGET_FILE"
if [ "$ATTEMPTS" -gt "$MAX_ATTEMPTS" ]; then
  printf 'budget-exhausted: review-fix loop ran %d times (max %d allowed re-reviews)\n' "$ATTEMPTS" "$MAX_ATTEMPTS"
  exit 1
fi
printf 'review-fix budget OK: re-review pass %d of %d\n' "$ATTEMPTS" "$MAX_ATTEMPTS"