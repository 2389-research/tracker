# When the human picks "retry" at EscalateReview, the pipeline
# restarts at Decompose without going through Cleanup, so the
# `.ai/build/review_fix_attempts` counter from the prior build
# would otherwise persist. Without this reset, the very first
# ApplyReviewFixes of the retry build would read the stale
# counter (already 1), increment to 2, exceed MAX_ATTEMPTS=1,
# and immediately escalate again — the retry would never get
# the one allowed re-review pass. Flagged by Codex P2 and
# Copilot on PR #264.
rm -f .ai/build/review_fix_attempts
printf 'review-fix budget reset for retry'