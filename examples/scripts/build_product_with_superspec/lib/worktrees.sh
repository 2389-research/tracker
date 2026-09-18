# ABOUTME: Stream worktree + merge helpers for build_product_with_superspec (#646 item 5a/5c/5e).
# ABOUTME: Sourced (never executed) via ${graph.workflow_dir}/scripts/build_product_with_superspec/lib.

# setup_stream_worktrees PHASE STREAM... — one worktree + build/<stream>
# branch per stream, forked at HEAD. Requires the scaffold (SPEC.md,
# docs/execution-plan.md, docs/traceability.yaml) to be COMMITTED — the
# streams read them inside their worktrees, where an uncommitted or
# gitignored file is invisible (#646 5a). A previous run's branch is never
# deleted silently (5e): merged into HEAD → deleted with a log line;
# unmerged → renamed build/<stream>-abandoned-<sha> (its commits stay
# reachable). Records the phase base sha for the gate's change scoping.
setup_stream_worktrees() {
  _phase=$1; shift
  git rev-parse --verify --quiet HEAD >/dev/null 2>&1 || { echo "ERROR: the repository has no commits — CommitScaffold must run before any stream"; return 1; }
  for _f in SPEC.md docs/execution-plan.md docs/traceability.yaml; do
    git cat-file -e "HEAD:$_f" 2>/dev/null || { echo "ERROR: $_f is not committed at HEAD — streams read it from their worktrees; CommitScaffold must commit it first"; return 1; }
  done
  mkdir -p .ai/worktrees .ai/build
  for _s in "$@"; do
    _branch="build/$_s"
    _wt=".ai/worktrees/$_s"
    if [ -e "$_wt" ]; then
      echo "removing stale worktree $_wt left by a previous run"
      git worktree remove --force "$_wt" 2>/dev/null || rm -rf "$_wt"
    fi
    git worktree prune 2>/dev/null || true
    if git rev-parse --verify --quiet "refs/heads/$_branch" >/dev/null 2>&1; then
      if git merge-base --is-ancestor "$_branch" HEAD 2>/dev/null; then
        git branch -q -D "$_branch"
        echo "deleted $_branch (already merged into HEAD)"
      else
        _keep="$_branch-abandoned-$(git rev-parse --short "$_branch")"
        git branch -m "$_branch" "$_keep"
        echo "renamed unmerged $_branch from a previous run to $_keep (nothing deleted; delete it yourself once inspected)"
      fi
    fi
    git worktree add -q "$_wt" -b "$_branch" HEAD
  done
  git rev-parse HEAD > .ai/build/milestone-start-sha
  return 0
}

# merge_streams PHASE STREAM... — merge every build/<stream> branch into the
# current branch; only once ALL of them are in, tear down the worktrees +
# branches, fold the streams' traceability overlays into the master
# (lib/traceability.sh) and record the next phase's base sha. On a conflict
# (5c): the merge is aborted (`|| true` — an already-aborted merge has
# nothing to abort, and that used to exit 128), the conflicting paths are
# printed, EVERY worktree/branch is left in place (the ones that merged
# too — teardown is all-or-nothing so a retry can still account for each
# stream), and the function returns 1 — the workflow routes that to the
# MergeConflict gate, never to accept. A retry after the human merged by
# hand skips branches already in HEAD.
merge_streams() {
  _phase=$1; shift
  mkdir -p .ai/build
  echo "$_phase" > .ai/build/merge-phase
  if [ -n "$(git status --porcelain --untracked-files=no 2>/dev/null)" ]; then
    echo "ERROR: the working tree has uncommitted changes — commit or stash them before merging phase $_phase:"
    git status --porcelain --untracked-files=no
    return 1
  fi
  for _s in "$@"; do
    _branch="build/$_s"
    if ! git rev-parse --verify --quiet "refs/heads/$_branch" >/dev/null 2>&1; then
      echo "ERROR: branch $_branch does not exist — stream $_s never ran, or its branch was removed"
      return 1
    fi
    if git merge-base --is-ancestor "$_branch" HEAD 2>/dev/null; then
      echo "skip: $_branch is already in HEAD"
    elif ! git -c user.name="build_product_with_superspec" -c user.email="superspec@tracker.local" -c commit.gpgsign=false \
           merge --no-edit "$_branch" -m "feat: merge $_s (phase $_phase)"; then
      echo "MERGE CONFLICT in $_s ($_branch) — conflicting paths:"
      git diff --name-only --diff-filter=U 2>/dev/null | sed 's/^/  /'
      git merge --abort 2>/dev/null || true
      echo "Resolve by hand in the workdir: 'git merge $_branch', fix the conflicts, commit — then choose 'retry' at the gate. Worktrees and branches are untouched."
      return 1
    fi
    echo "merged $_branch"
  done
  for _s in "$@"; do
    git worktree remove --force ".ai/worktrees/$_s" 2>/dev/null || true
    git branch -q -d "build/$_s" 2>/dev/null || git branch -q -D "build/$_s"
    echo "removed worktree and branch for $_s (merged)"
  done
  git worktree prune 2>/dev/null || true
  merge_traceability_overlays "$_phase" || return 1
  git rev-parse HEAD > .ai/build/milestone-start-sha
  return 0
}
