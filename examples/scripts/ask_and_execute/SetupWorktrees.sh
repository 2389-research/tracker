set -eu
# Candidate worktrees branch from HEAD; a commitless repo has nothing to
# branch from (and nothing for CaptureAndTest to diff against).
git rev-parse --verify --quiet HEAD >/dev/null 2>&1 || {
  echo "ERROR: the repository has no commits — ask_and_execute branches its three candidate worktrees from HEAD. Make an initial commit first."
  exit 1
}
mkdir -p .ai/worktrees .ai/candidates
# #646 item 2: record the fork point ONCE. CaptureAndTest diffs every
# candidate against this sha — `git rev-parse --abbrev-ref HEAD` was `HEAD`
# on a detached checkout, which made `git diff HEAD..HEAD` empty for all three.
git rev-parse HEAD > .ai/candidates/base-sha
for NAME in claude codex gemini; do
  BRANCH="impl/$NAME"
  WTDIR=".ai/worktrees/$NAME"
  if [ -e "$WTDIR" ]; then
    echo "removing stale worktree $WTDIR left by a previous run"
    git worktree remove --force "$WTDIR" 2>/dev/null || rm -rf "$WTDIR"
  fi
  git worktree prune 2>/dev/null || true
  # A previous run's branch is never deleted silently: merged → deleted with
  # a log line; unmerged → RENAMED (its commits stay reachable).
  if git rev-parse --verify --quiet "refs/heads/$BRANCH" >/dev/null 2>&1; then
    if git merge-base --is-ancestor "$BRANCH" HEAD 2>/dev/null; then
      git branch -q -D "$BRANCH"
      echo "deleted $BRANCH (already merged into HEAD)"
    else
      KEEP="$BRANCH-abandoned-$(git rev-parse --short "$BRANCH")"
      git branch -m "$BRANCH" "$KEEP"
      echo "renamed unmerged $BRANCH from a previous run to $KEEP (nothing deleted; delete it yourself once inspected)"
    fi
  fi
  git worktree add -q "$WTDIR" -b "$BRANCH" HEAD
done
printf 'worktrees-ready'
