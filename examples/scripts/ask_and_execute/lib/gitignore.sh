# ABOUTME: Shared .gitignore / git info/exclude helpers for build_product.
# ABOUTME: Sourced (never executed) via ${graph.workflow_dir}/scripts/build_product/lib.
#
# Every helper is idempotent and safe outside a git repo. Callers run under
# `set -eu`, so nothing here may fail on the no-repo path.

# gitignore_append PATTERN — append PATTERN to the tracked .gitignore once.
# #640 C2/C3: append-if-absent (grep -qxF), never sort. A bare `>>` onto a
# user file with no trailing newline glued `.ai/` onto their last pattern
# (`*.log.ai/` — both broken), and `sort -u` reordered `!negations` ahead of
# what they negate and rewrote the tracked file on EVERY run, so it landed
# in milestone 1's diff as out-of-scope work. Now: a fully seeded file is
# not opened for writing at all (content + mtime untouched), and a missing
# trailing newline is repaired before the first append.
gitignore_append() {
  if [ -f .gitignore ]; then
    grep -qxF -- "$1" .gitignore && return 0
    [ -s .gitignore ] && [ -n "$(tail -c1 .gitignore)" ] && echo >> .gitignore
  fi
  printf '%s\n' "$1" >> .gitignore
}

# seed_gitignore — append `.ai/` and the #405 static build-output patterns to
# the tracked .gitignore (append-if-absent, order preserved; idempotent
# across re-runs — Setup).
#
# `.ai/` is deliberately gitignored: the decision/plan/milestone artifacts
# under it are run metadata for build_product's own gates, not product
# source, so they never ship in the product's history (#640 hunt).
#
# #405: seed common build-output patterns so a milestone whose tests
# compile/install artifacts doesn't have them swept into a CommitIfDirty
# checkpoint and then FAILed by Verify as out-of-scope work. This is the
# STATIC half (named/dir artifacts across the toolchains build_product
# targets); arbitrary-named compiled binaries (Go `go build -o <name>` has
# no fixed extension) are caught dynamically in CommitIfDirty.
#
# #640 C4: directory seeds are ANCHORED to the repo root (`/build/`, not
# `build/`) — an unanchored `build/` silently hid a source package like
# internal/build/ from every commit, scope test and review, so a fresh
# clone of the shipped repo didn't build. `*.out` is dropped (too broad:
# testdata/render.out) and Go test binaries are `/*.test` (`go test -c`
# writes to cwd, i.e. the root). node_modules/ and __pycache__/ stay
# unanchored — they are never source at any depth.
seed_gitignore() {
  gitignore_append '.ai/'
  for pat in \
    'node_modules/' '/dist/' '/build/' '/target/' '/coverage/' \
    '__pycache__/' '/.venv/' '/venv/' '*.pyc' \
    '*.exe' '/*.test' '*.o' '*.a'; do
    gitignore_append "$pat"
  done
}

# git_exclude_path — print the per-repo exclude file git actually reads, or
# nothing outside a repo. #640 C1: `git rev-parse --git-dir` returns
# .git/worktrees/<n> in a LINKED worktree (`git worktree add`), where git
# never reads info/exclude — only $GIT_COMMON_DIR/info/exclude counts, so an
# exclude written there was dead and `.tracker/inputs/api_key` got committed
# (#351/#555 regression). `--git-path info/exclude` resolves to the common
# dir in a linked worktree and to .git/info/exclude in a normal checkout.
git_exclude_path() {
  git rev-parse --git-path info/exclude 2>/dev/null || true
}

# git_exclude_add PATTERN — add PATTERN to the LOCAL, untracked
# info/exclude once (grep -qxF keeps the append idempotent). Never
# touches the user's tracked .gitignore: a runtime write to .gitignore is
# itself an out-of-scope tree change VerifyMilestone would FAIL (PR #411).
# No-op outside a git repo. Shared by Setup (`.tracker/`) and
# ContinueWithMoreTurns (the turn-override dir). printf, not echo, so dash's
# echo can't reinterpret a backslash in PATTERN.
git_exclude_add() {
  _excl=$(git_exclude_path)
  [ -n "$_excl" ] || return 0
  mkdir -p "$(dirname "$_excl")"
  grep -qxF -- "$1" "$_excl" 2>/dev/null \
    || printf '%s\n' "$1" >> "$_excl"
}

# exclude_tracker_metadata — #351: keep tracker run metadata out of the
# product repo. CommitIfDirty runs `git add -A`, which would otherwise commit
# .tracker/runs/<id>/ internals (prompt.md/response.md/checkpoint.json) into
# the user's history. Same LOCAL info/exclude treatment the turn-override
# dir gets in ContinueWithMoreTurns (idempotent; safe outside a git repo;
# linked-worktree aware via git_exclude_path).
#
# Also untracks any .tracker/ files committed by a pre-#351 run — ignore
# rules only affect UNTRACKED paths, so without this an already-polluted
# repo keeps committing metadata churn forever. Index-only removal (--cached,
# working tree untouched); the next CommitIfDirty commits the deletion,
# cleaning the product history going forward.
exclude_tracker_metadata() {
  [ -n "$(git_exclude_path)" ] || return 0
  git_exclude_add ".tracker/"
  if git ls-files --cached -- .tracker | grep -q .; then
    git rm -r -q --cached .tracker
  fi
}
