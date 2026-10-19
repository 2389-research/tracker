set -eu
# Reject: remove the quickfix worktree and its branch. Nothing else — no
# reset, no clean — so the user's checkout, branch, uncommitted edits and
# ignored files are exactly as they were. Missing pieces are not an error
# (a re-run, or a worktree removed by hand).
BR=$(cat .tracker/quickfix/branch 2>/dev/null || printf 'tracker/quickfix/%s' "${TRACKER_RUN_ID:-}")
WT="$(pwd)/.tracker/quickfix/worktree"
if [ -e "$WT" ]; then
  git worktree remove --force "$WT"
fi
git worktree prune
if git show-ref --verify -q "refs/heads/$BR"; then
  git branch -q -D "$BR"
fi
MSG="discarded $BR: the worktree and branch are gone; your checkout is unchanged"
# notice.txt is printed after the run summary (console and TUI alike).
if [ -n "${TRACKER_RUN_DIR:-}" ]; then
  mkdir -p "$TRACKER_RUN_DIR"
  printf '%s\n' "$MSG" > "$TRACKER_RUN_DIR/notice.txt"
fi
printf '%s\n' "$MSG"
