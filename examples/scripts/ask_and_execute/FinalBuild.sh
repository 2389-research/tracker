set -eu
if [ -f go.mod ]; then
  go build ./... 2>&1
  go test ./... 2>&1
elif [ -f package.json ]; then
  npm test 2>&1
fi
# Verify no worktrees remain
REMAINING=$(git worktree list --porcelain | grep -c 'worktree' || true)
if [ "$REMAINING" -gt 1 ]; then
  echo "WARNING: leftover worktrees detected"
  git worktree list
  exit 1
fi
# Verify no impl branches remain
if git branch | grep -q 'impl/'; then
  echo "WARNING: leftover impl branches"
  git branch | grep 'impl/'
  exit 1
fi
printf 'verified-clean'