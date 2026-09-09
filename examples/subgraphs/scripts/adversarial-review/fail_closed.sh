# ABOUTME: Fail-closed sink for the Adversarial Review subgraph (#623).
# ABOUTME: Reached via the workflow-level on_failure route when any node fails
# ABOUTME: after its retries (or via a failed fan_in join).
#
# A review that could not complete must never emit "approve". This node
# degrades the verdict to the conservative outcome — "rework" — so the
# caller's outer loop re-reviews (and the underlying failure stays loud in
# the activity log and `tracker diagnose`). If this node itself ever fails,
# its only edge is unconditional, so the engine's strict-failure rule stops
# the run there.
set -euo pipefail

mkdir -p .ai/review
echo '{"verdict":"rework","kept":[],"summary":{"total":0,"kept":0,"note":"review failed before the FP gate; verdict degraded to rework (fail closed)"}}' > .ai/review/verdict.json
echo "adversarial-review: review failed; verdict degraded to rework at .ai/review/verdict.json" >&2
echo "degraded"
