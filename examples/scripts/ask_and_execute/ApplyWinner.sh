set -eu
MAIN=$(git rev-parse --abbrev-ref HEAD)

# Extract winner name from selection.md — handles ## Winner, ### Winner, **Winner**, etc.
WINNER=$(grep -iA1 'winner' .ai/decisions/selection.md | grep -ioE 'claude|codex|gemini' | head -1 | tr '[:upper:]' '[:lower:]')

# Validate
if [ -z "$WINNER" ] || ! echo "claude codex gemini" | grep -qw "$WINNER"; then
  echo "ERROR: could not determine winner from .ai/decisions/selection.md"
  echo "Parsed: '$WINNER'"
  exit 1
fi

BRANCH="impl/$WINNER"

# Merge the winning branch
git merge "$BRANCH" --no-edit -m "feat: apply $WINNER implementation (selected by cross-critique)"

# Remove all worktrees
for NAME in claude codex gemini; do
  git worktree remove --force ".ai/worktrees/$NAME" 2>/dev/null || true
  git branch -D "impl/$NAME" 2>/dev/null || true
done

# Clean up candidate artifacts (keep decisions)
rm -rf .ai/worktrees .ai/candidates

printf 'applied: %s' "$WINNER"