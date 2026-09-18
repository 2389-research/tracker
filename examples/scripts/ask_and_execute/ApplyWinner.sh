set -eu
SEL=.ai/decisions/selection.md
[ -f "$SEL" ] || { echo "ERROR: $SEL not found — SelectWinner did not write its decision"; exit 1; }

# #646 item 3: the winner is read ONLY from `WINNER: <claude|codex|gemini>`
# line(s) — never from prose. The old parser (`grep -iA1 winner | grep -ioE
# 'claude|codex|gemini' | head -1`) picked the first candidate name it saw
# near the word "winner", so "## Winner / **gemini** / … beat codex …"
# merged codex. Now: no WINNER: line → exit 1 (no merge); a WINNER: line that
# is not exactly one name, or several WINNER: lines that disagree → exit 1.
# Leading markdown decoration (`**`, `#`, `_`, spaces) before WINNER: is
# tolerated; anything after the name other than decoration/whitespace is not.
# NOTE: the parser is line-based and does not track code fences — a
# `WINNER:` line inside a fence IS parsed (the prompt says to write it
# outside any fence; a fenced draft that disagrees with the real one trips
# the ambiguity check below, which is the safe outcome).
LINES=$(grep -iE '^[[:space:]*_#`>-]*WINNER:' "$SEL" || true)
if [ -z "$LINES" ]; then
  echo "ERROR: no 'WINNER: <claude|codex|gemini>' line in $SEL — SelectWinner must end the file with exactly that line. Nothing was merged."
  exit 1
fi
# shellcheck disable=SC2016  # the backtick is a literal in the bracket expression
NAMES=$(printf '%s\n' "$LINES" \
  | sed -E 's/^[[:space:]*_#`>-]*[Ww][Ii][Nn][Nn][Ee][Rr]:[[:space:]]*//; s/[[:space:]*_`.]+$//' \
  | tr '[:upper:]' '[:lower:]' | sort -u)
NNAMES=$(printf '%s\n' "$NAMES" | grep -c . || true)
if [ "$NNAMES" -ne 1 ]; then
  echo "ERROR: ambiguous winner — the WINNER: lines in $SEL name $NNAMES different values:"
  printf '%s\n' "$LINES"
  echo "Exactly one 'WINNER: <claude|codex|gemini>' line is required. Nothing was merged."
  exit 1
fi
WINNER="$NAMES"
case "$WINNER" in
  claude|codex|gemini) ;;
  *)
    echo "ERROR: the WINNER: line in $SEL does not name exactly one of claude, codex, gemini (got '$WINNER'). Nothing was merged."
    exit 1 ;;
esac

BRANCH="impl/$WINNER"
git rev-parse --verify --quiet "refs/heads/$BRANCH" >/dev/null 2>&1 || {
  echo "ERROR: branch $BRANCH does not exist — the winning candidate has no branch to merge"
  exit 1
}

# Merge the winner. --no-ff so the selection is always a real merge commit
# carrying this message (CommitFinal expects "the merge commit from
# ApplyWinner"; a fast-forward would leave only the candidate's commits).
# A conflict (or any merge failure) aborts the merge, leaves every worktree
# and branch in place for inspection, and fails the node (→ AbortRun
# terminal, never the accept gate).
if ! git -c user.name="ask_and_execute" -c user.email="ask_and_execute@tracker.local" -c commit.gpgsign=false \
     merge --no-ff --no-edit "$BRANCH" -m "feat: apply $WINNER implementation (selected by cross-critique)"; then
  echo "ERROR: merging $BRANCH failed — conflicting paths:"
  git diff --name-only --diff-filter=U 2>/dev/null || true
  git merge --abort 2>/dev/null || true
  echo "Candidate worktrees (.ai/worktrees/*) and impl/* branches are left in place. Nothing was applied."
  exit 1
fi

# Tear down ONLY after the winner is merged. The winner's branch is merged
# (-d); each loser's branch is deleted with a log line — its diff and test
# log remain in .ai/candidates/ as the decision record (kept, not rm -rf'd).
for NAME in claude codex gemini; do
  git worktree remove --force ".ai/worktrees/$NAME" 2>/dev/null || true
  if git rev-parse --verify --quiet "refs/heads/impl/$NAME" >/dev/null 2>&1; then
    if [ "$NAME" = "$WINNER" ]; then
      git branch -q -d "impl/$NAME" 2>/dev/null || git branch -q -D "impl/$NAME"
      echo "deleted impl/$NAME (winner, merged)"
    else
      git branch -q -D "impl/$NAME"
      echo "deleted impl/$NAME (not selected — its diff/test log stay in .ai/candidates/$NAME.diff and $NAME.test)"
    fi
  fi
done
git worktree prune 2>/dev/null || true
rm -rf .ai/worktrees

printf 'applied: %s' "$WINNER"
