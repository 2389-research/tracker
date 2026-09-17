# ABOUTME: Shared .gitignore / .git/info/exclude helpers for build_product.
# ABOUTME: Sourced (never executed) via ${graph.workflow_dir}/scripts/build_product/lib.
#
# Every helper is idempotent and safe outside a git repo. Callers run under
# `set -eu`, so nothing here may fail on the no-repo path.

# seed_gitignore — append `.ai/` and the #405 static build-output patterns to
# the tracked .gitignore, then dedupe + sort in place. Appended + deduped
# exactly like `.ai/`; idempotent across re-runs (Setup).
#
# #405: seed common build-output patterns so a milestone whose tests
# compile/install artifacts doesn't have them swept into a CommitIfDirty
# checkpoint and then FAILed by Verify as out-of-scope work. This is the
# STATIC half (named/dir artifacts across the toolchains build_product
# targets); arbitrary-named compiled binaries (Go `go build -o <name>` has
# no fixed extension) are caught dynamically in CommitIfDirty.
seed_gitignore() {
  echo '.ai/' >> .gitignore 2>/dev/null || true
  for pat in \
    'node_modules/' 'dist/' 'build/' 'target/' 'coverage/' \
    '__pycache__/' '.venv/' 'venv/' '*.pyc' \
    '*.exe' '*.test' '*.out' '*.o' '*.a'; do
    echo "$pat" >> .gitignore 2>/dev/null || true
  done
  sort -u .gitignore -o .gitignore
}

# git_exclude_add PATTERN — add PATTERN to the LOCAL, untracked
# .git/info/exclude once (grep -qxF keeps the append idempotent). Never
# touches the user's tracked .gitignore: a runtime write to .gitignore is
# itself an out-of-scope tree change VerifyMilestone would FAIL (PR #411).
# No-op outside a git repo. Shared by Setup (`.tracker/`),
# ContinueWithMoreTurns (the turn-override dir) and CommitIfDirty (per-artifact
# paths, which arrive sed-escaped — printf, not echo, so dash's echo can't
# reinterpret the backslashes).
git_exclude_add() {
  _gitdir=$(git rev-parse --git-dir 2>/dev/null || true)
  [ -n "$_gitdir" ] || return 0
  mkdir -p "$_gitdir/info"
  grep -qxF "$1" "$_gitdir/info/exclude" 2>/dev/null \
    || printf '%s\n' "$1" >> "$_gitdir/info/exclude"
}

# exclude_tracker_metadata — #351: keep tracker run metadata out of the
# product repo. CommitIfDirty runs `git add -A`, which would otherwise commit
# .tracker/runs/<id>/ internals (prompt.md/response.md/checkpoint.json) into
# the user's history. Same LOCAL .git/info/exclude treatment the turn-override
# dir gets in ContinueWithMoreTurns (idempotent; safe outside a git repo).
#
# Also untracks any .tracker/ files committed by a pre-#351 run — ignore
# rules only affect UNTRACKED paths, so without this an already-polluted
# repo keeps committing metadata churn forever. Index-only removal (--cached,
# working tree untouched); the next CommitIfDirty commits the deletion,
# cleaning the product history going forward.
exclude_tracker_metadata() {
  _gitdir=$(git rev-parse --git-dir 2>/dev/null || true)
  [ -n "$_gitdir" ] || return 0
  git_exclude_add ".tracker/"
  if git ls-files --cached -- .tracker | grep -q .; then
    git rm -r -q --cached .tracker
  fi
}
