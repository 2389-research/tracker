# ABOUTME: Freezes the review diff for the Adversarial Review subgraph (#623).
# ABOUTME: Runs as the ComputeDiff tool node; ${params.diff_ref} is injected at
# ABOUTME: subgraph bind time from the caller's params: block (author-controlled).
#
# Reads : ${params.diff_ref} (git diff range, e.g. main..HEAD)
# Writes: .ai/review/diff.patch (the frozen diff)
# Emits : final-line marker `diff_ready` | `diff_empty`
#
# Fail-closed: not a git worktree, unresolvable range, or git failure -> exit 1.
# A failed tool node stops the subgraph (strict-failure edges) so the caller's
# on_failure routing fires — a review with no diff must never report "clean".
set -euo pipefail

RANGE="${params.diff_ref}"

if [ -z "$RANGE" ]; then
  echo "compute_diff: diff_ref is empty — the caller must pass params.diff_ref (e.g. main..HEAD)" >&2
  exit 1
fi

if ! git rev-parse --git-dir >/dev/null 2>&1; then
  echo "compute_diff: not a git worktree; cannot compute a review diff" >&2
  exit 1
fi

# Fresh review -> fresh slate: drop any .ai/review state left by a prior
# caller run in this workdir (round counter, findings, verdict). A resumed
# run never re-enters ComputeDiff (checkpoint), so its in-flight state
# survives the restart.
rm -rf .ai/review
mkdir -p .ai/review
# git is the authority on whether the range resolves — its own error message
# ("unknown revision", "ambiguous argument") is the clear failure signal.
if ! git diff --no-color "${RANGE}" > .ai/review/diff.patch 2> .ai/review/diff.err; then
  echo "compute_diff: git diff failed for '${RANGE}':" >&2
  cat .ai/review/diff.err >&2
  exit 1
fi

# Empty diff is a legitimate "nothing to review" outcome, not an error: the
# subgraph exits with review_verdict=approve and zero findings.
if [ ! -s .ai/review/diff.patch ]; then
  echo "compute_diff: diff '${RANGE}' is empty" >&2
  echo "diff_empty"
  exit 0
fi

lines=$(wc -l < .ai/review/diff.patch | tr -d ' ')
echo "compute_diff: frozen ${lines} lines of '${RANGE}' at .ai/review/diff.patch" >&2
echo "diff_ready"
