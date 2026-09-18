set -eu
MAIN_BRANCH=$(git rev-parse --abbrev-ref HEAD)
for NAME in claude codex gemini; do
  BRANCH="impl/$NAME"
  WTDIR=".ai/worktrees/$NAME"
  git worktree remove --force "$WTDIR" 2>/dev/null || true
  git branch -D "$BRANCH" 2>/dev/null || true
  git worktree add "$WTDIR" -b "$BRANCH" HEAD
done
printf 'worktrees-ready'