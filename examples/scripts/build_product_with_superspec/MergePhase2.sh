set -eu
for STREAM in stream-c stream-e; do
  BRANCH="build/$STREAM"
  git merge "$BRANCH" --no-edit -m "feat: merge $STREAM (phase 2)" || {
    echo "MERGE CONFLICT in $STREAM"
    git merge --abort
    exit 1
  }
  git worktree remove --force ".ai/worktrees/$STREAM" 2>/dev/null || true
  git branch -D "$BRANCH" 2>/dev/null || true
done
rm -rf .ai/worktrees/stream-c .ai/worktrees/stream-e
printf 'phase2-merged'