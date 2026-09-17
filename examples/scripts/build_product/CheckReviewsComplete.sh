set -eu
# issue #313 defense-in-depth: ReviewParallel now declares
# fan_in_policy: all, so a failed reviewer routes to EscalateReview
# upstream — but a reviewer that "succeeds" without writing its report
# is invisible to any aggregation policy, and SynthesizeReviews would
# synthesize from a partial set. Require ALL THREE reports present +
# non-empty; a single missing
# review (typically the adversarial ReviewGemini, the one most likely to
# exhaust turns) fails the gate and routes to EscalateReview rather than
# masking the gap. A 2-of-3 quorum would not fix this — it would still
# proceed with the adversarial reviewer absent.
# This gate routes on exit code (ctx.outcome), NOT on stdout content, so
# all diagnostics go to stderr — keeps stdout free of routing-marker noise
# (surfaced by `tracker diagnose` either way) and the dippin coverage
# analyzer correctly sees no stdout outputs to match.
missing=""
for f in review-claude.md review-codex.md review-gemini.md; do
  [ -s ".ai/build/$f" ] || missing="$missing $f"
done
if [ -n "$missing" ]; then
  printf 'review-gate FAIL: missing/empty review(s):%s — escalating instead of synthesizing a partial review set\n' "$missing" >&2
  exit 1
fi
printf 'review-gate OK: all 3 reviews present\n' >&2