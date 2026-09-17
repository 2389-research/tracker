set -eu
# issue #313: ReviewParallel declares fan_in_policy: all, so a reviewer
# that exhausts max_turns and FAILS now routes to EscalateReview — but a
# reviewer that reports success without writing its file is invisible to
# any aggregation policy. CheckReviewsComplete (after the join) keys on
# review-file presence; clear last round's reports so a reviewer that
# fails on a re-review pass can't be satisfied by a stale file from
# the prior pass. Runs on every inbound path to ReviewParallel: the
# first entry via CheckMilestoneOutputs (outputs-present), the capped
# re-review restart loop, and the EscalateMilestone "accept" override,
# which routes here directly — deliberately NOT re-entering
# CheckMilestoneOutputs, the check the operator is overriding (#730).
mkdir -p .ai/build
rm -f .ai/build/review-claude.md .ai/build/review-codex.md .ai/build/review-gemini.md
printf 'cleared stale review reports\n'