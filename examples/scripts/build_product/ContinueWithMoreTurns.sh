set -eu
# Shared helpers via the engine-interpolated ${graph.workflow_dir} (author-
# controlled, safe-key allowlisted). Fail loud if empty.
[ -n "${graph.workflow_dir}" ] || { echo "ERROR: graph.workflow_dir is empty — cannot locate build_product's scripts/build_product/lib/ (embedded built-in: engine failed to materialize .tracker/workflow/; packed .dipx: unsupported, see #430)"; exit 1; }
LIB="${graph.workflow_dir}/scripts/build_product/lib"
. "$LIB/gitignore.sh"
. "$LIB/counters.sh"
# Issue #318 warm continue+N. The operator picked "continue" at the
# OperatorDecision gate: re-enter Implement WARM (it keeps its episode
# memory across the restart) but with a larger turn budget so it doesn't
# breach at the same wall again.
#
# Per-loop circuit-breaker: a disk counter — NOT the global engine
# RestartCount, which is shared across every loop in the run and would
# reset/consume budget with the wrong semantics (#318 hazard 3). Once the
# cap is exhausted we route to EscalateMilestone via ctx.outcome = fail
# (exit 1) instead of looping forever.
#
# The bump is delivered through the tracker-owned, node-scoped MaxTurns
# override file that codergen.buildConfig consults
# (.tracker/turn_overrides/<nodeID>); BASE matches Implement's max_turns.
CAP=3
BASE=50
BUMP=40
OVR_DIR=".tracker/turn_overrides"
ATTEMPT_FILE="$OVR_DIR/continue_attempts"
# Keep these tracker-internal control files OUT of the product repo. A later
# CommitIfDirty runs `git add -A`, which would otherwise commit the counter +
# override — polluting the user's repo AND leaving a stale Implement override
# that a future run would read (via codergen.buildConfig) before any operator
# decision. Ignore them via the LOCAL, untracked info/exclude so we never
# touch the user's tracked .gitignore (idempotent; safe outside a git repo;
# #640 C1: resolved via `--git-path` so a linked worktree gets the common-dir
# file git actually reads, not a dead .git/worktrees/<n>/info/exclude).
git_exclude_add "$OVR_DIR/"
mkdir -p "$OVR_DIR"
# bump_counter resets a corrupted/non-numeric counter (e.g. a prior run
# interrupted before MarkMilestoneDone/Cleanup cleared it) so the arithmetic
# can't error under `set -e` and a fresh operator decision isn't denied its
# continues by stale junk. Setup also clears this dir at run start.
bump_counter "$ATTEMPT_FILE"
if [ "$ATTEMPTS" -gt "$CAP" ]; then
  echo "continue cap ($CAP) exhausted after $ATTEMPTS attempt(s) — escalating"
  printf 'continue-cap-exhausted'
  exit 1
fi
NEWMAX=$((BASE + ATTEMPTS * BUMP))
echo "$NEWMAX" > "$OVR_DIR/Implement"
echo "warm continue $ATTEMPTS/$CAP — bumped Implement max_turns to $NEWMAX"
printf 'continue-ok'