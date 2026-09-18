set -eu
for STREAM in stream-c stream-e; do
  BRANCH="build/$STREAM"
  WTDIR=".ai/worktrees/$STREAM"
  git worktree remove --force "$WTDIR" 2>/dev/null || true
  git branch -D "$BRANCH" 2>/dev/null || true
  git worktree add "$WTDIR" -b "$BRANCH" HEAD
done
printf 'phase2-worktrees-ready'