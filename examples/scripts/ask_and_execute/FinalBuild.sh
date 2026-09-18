set -eu
RC=0
if [ -f go.mod ]; then
  { go build ./... && go test ./... ; } 2>&1 || RC=1
elif [ -f package.json ]; then
  npm test 2>&1 || RC=1
elif [ -f pyproject.toml ]; then
  uv run pytest 2>&1 || RC=1
elif [ -f Cargo.toml ]; then
  cargo test 2>&1 || RC=1
else
  echo "NOTE: no known build system (go.mod / package.json / pyproject.toml / Cargo.toml) — nothing was tested"
fi
# Verify no worktrees remain. `^worktree ` is anchored to the porcelain
# record line: a branch or path merely containing "worktree" is not one.
REMAINING=$(git worktree list --porcelain | grep -c '^worktree ' || true)
if [ "$REMAINING" -gt 1 ]; then
  echo "WARNING: leftover worktrees detected"
  git worktree list
  RC=1
fi
# Verify no ACTIVE impl branches remain. A renamed impl/<name>-abandoned-<sha>
# from an earlier run (SetupWorktrees never deletes unmerged work) is
# reported but is not a failure.
LEFT=$(git branch --list 'impl/claude' 'impl/codex' 'impl/gemini' | sed 's/^[* ]*//' || true)
if [ -n "$LEFT" ]; then
  echo "WARNING: leftover impl branches"
  printf '%s\n' "$LEFT"
  RC=1
fi
ABANDONED=$(git branch --list 'impl/*-abandoned-*' | sed 's/^[* ]*//' || true)
[ -z "$ABANDONED" ] || printf '%s\n' "$ABANDONED" | sed 's/^/NOTE: abandoned candidate branch from an earlier run (not deleted): /'
[ "$RC" -eq 0 ] || exit 1
printf 'verified-clean'
