set -u
# A step failed: halt the run as `fail` in every mode (a tool that exits 1,
# with a single unconditional edge to Done). The worktree, if there is one,
# is left in place for inspection, with the commands to clean it up.
echo "RUN ABORTED: quickfix could not finish; your checkout was not changed."
echo "The failing node and its output are in the activity log: run 'tracker diagnose' for the tail."
WT="$(pwd)/.tracker/quickfix/worktree"
if [ -e "$WT" ]; then
  BR=$(cat .tracker/quickfix/branch 2>/dev/null || printf 'tracker/quickfix/%s' "${TRACKER_RUN_ID:-}")
  NOTICE=$(cat <<EOF
The work in progress is kept at $WT. When you are done with it:
  git worktree remove --force "$WT"
  git branch -D $BR
EOF
)
  printf '%s\n' "$NOTICE"
  # notice.txt is printed after the run summary (console and TUI alike).
  if [ -n "${TRACKER_RUN_DIR:-}" ] && mkdir -p "$TRACKER_RUN_DIR" 2>/dev/null; then
    printf '%s\n' "$NOTICE" > "$TRACKER_RUN_DIR/notice.txt" || true
  fi
fi
exit 1
