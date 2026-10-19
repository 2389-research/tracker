set -eu
# Issue #297: persist green-but-uncommitted work on the Implement SUCCESS
# path so it survives a later node death. Explicit identity makes the
# commit independent of the tool subprocess's git config (unproven here —
# commits otherwise happen inside agent sessions). A genuine commit
# failure is NOT swallowed (no `|| true`); it surfaces per CLAUDE.md
# "never silently swallow errors". The marker is printed last so it
# survives output truncation (CLAUDE.md "Tool output capture").
#
# #640 E7: outside a git repo this is an invariant violation (build_product
# `requires: git`; every checkpoint depends on a repo), so fail LOUD instead
# of reading a failed `git status` as "clean" and shipping the marker.
git rev-parse --is-inside-work-tree >/dev/null 2>&1 \
  || { echo "ERROR: CommitIfDirty: not a git repository: $(pwd) — build_product checkpoints require the workdir to be a git repo (\`git init\` it, or run from the repo root)"; exit 1; }
# Everything below is whole-tree (status/add/commit), and exclude patterns in
# an excludes file are root-relative, so anchor at the toplevel once.
cd "$(git rev-parse --show-toplevel)"

# `git status --porcelain` (not `git diff`) so UNTRACKED files count as
# dirty — a milestone that only adds new files would otherwise look clean
# to `git diff` and skip the commit, leaving exactly the green-but-
# uncommitted tree this node exists to prevent.
#
# Per-invocation excludes (secrets, private keys, compiled binaries, the
# user's global ignore): lib/stage.sh, shared by CommitIfDirty and FinalCommit.
TMP=$(mktemp -d)
trap 'rm -rf "$TMP"' EXIT
LIB="${graph.workflow_dir}/scripts/build_product/lib"
. "$LIB/stage.sh"
stage_build_excludes "$TMP"

if [ -z "$(stage_git status --porcelain)" ]; then
  printf 'commit-if-dirty-done'
  exit 0
fi
stage_git add -A

# #640 C7: these checkpoints are tracker's, not the user's signed history —
# `commit.gpgsign=true` with no usable key would otherwise fail every
# checkpoint with exit 128, so the commit is made unsigned. The user's hooks
# are KEPT (no --no-verify — CLAUDE.md forbids it, and a failing hook is a
# real finding): git sends hook output to stderr, so it is captured and
# echoed on stdout either way, and a hook failure exits 1 loudly with no
# marker (the run then escalates instead of shipping over a broken tree).
COMMIT_OUT="$TMP/commit.out"
commit() {
  git -c user.name="build_product" -c user.email="build_product@tracker.local" \
      -c commit.gpgsign=false commit "$@" >"$COMMIT_OUT" 2>&1
}
rc=0
commit -m "chore(milestone): checkpoint working tree (auto-commit by build_product)" || rc=$?
cat "$COMMIT_OUT"
if [ "$rc" -ne 0 ]; then
  echo "ERROR: checkpoint commit failed (git exit $rc) — see the hook/git output above; fix the hook or the tree, then resume"
  exit 1
fi
# A pre-commit hook that REWRITES files (formatter) leaves ` M` behind after
# a successful commit. Fold the rewrite into the checkpoint with ONE amend
# (the hook runs again on the amend; an idempotent formatter converges);
# a tree still dirty after that means a non-converging hook — fail loud
# rather than print done over a dirty tree.
if [ -n "$(stage_git status --porcelain)" ]; then
  echo "pre-commit hook rewrote files — folding them into the checkpoint (amend once)"
  stage_git add -A
  rc=0
  commit --amend --no-edit || rc=$?
  cat "$COMMIT_OUT"
  if [ "$rc" -ne 0 ]; then
    echo "ERROR: checkpoint amend failed (git exit $rc) — see the hook/git output above"
    exit 1
  fi
  if [ -n "$(stage_git status --porcelain)" ]; then
    echo "ERROR: working tree still dirty after the checkpoint — a pre-commit hook rewrites files on every commit:"
    stage_git status --porcelain
    exit 1
  fi
fi
printf 'commit-if-dirty-done'
