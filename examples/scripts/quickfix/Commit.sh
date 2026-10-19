set -eu
# Approve: commit the worktree's change on tracker/quickfix/<run-id>, remove
# the worktree, keep the branch. The user's checkout and branch are never
# touched; merging is theirs to do.
#
# Staging uses build_product's rules (lib/stage.sh, a parity copy): untracked
# secret-looking files, private keys and compiled binaries are never staged,
# and .tracker/ is excluded. The commit is unsigned (a tracker commit on a
# throwaway branch; commit.gpgsign with no usable key would otherwise fail it)
# with the user's hooks KEPT — no --no-verify: a rejecting hook exits 1 with
# its output and the worktree stays for inspection; a rewriting hook
# (formatter) is folded in with one amend. The subject is fixed and the body
# is read from the agent's summary file with `commit -F`, never interpolated
# into a command. The user's identity is used when configured.
[ -n "${graph.workflow_dir}" ] || { echo "ERROR: Commit: graph.workflow_dir is empty — cannot locate scripts/quickfix/lib/" >&2; exit 1; }
LIB="${graph.workflow_dir}/scripts/quickfix/lib"
. "$LIB/stage.sh"
die() { printf 'ERROR: Commit: %s\n' "$*" >&2; exit 1; }

TOP="$(pwd)"
WT="$TOP/.tracker/quickfix/worktree"
[ -d "$WT" ] || die "no quickfix worktree at $WT — Preflight did not run or it was removed"
BASE=$(cat .tracker/quickfix/base_sha 2>/dev/null || true)
BR=$(cat .tracker/quickfix/branch 2>/dev/null || true)
[ -n "$BASE" ] && [ -n "$BR" ] || die "no base/branch recorded under .tracker/quickfix/ — Preflight did not finish"
[ -n "${TRACKER_RUN_ID:-}" ] || die "TRACKER_RUN_ID is not set — run quickfix through tracker"

TMP=$(mktemp -d)
trap 'rm -rf "$TMP"' EXIT
cd "$WT"
stage_build_excludes "$TMP"
printf '%s\n' '.tracker/' >> "$STAGE_EXCLUDE"
stage_git add -A
if git diff --cached --quiet; then
  die "nothing to commit — the worktree has no changes against $BASE (the worktree is kept at $WT)"
fi

printf 'quickfix: change from tracker run %s\n\n' "$TRACKER_RUN_ID" > "$TMP/msg"
if [ -s .tracker/quickfix/summary.md ]; then
  cat .tracker/quickfix/summary.md >> "$TMP/msg"
else
  printf 'Made by tracker quickfix (no summary was written).\n' >> "$TMP/msg"
fi

IDENT=""
[ -n "$(git config user.email 2>/dev/null || true)" ] \
  || IDENT="-c user.name=tracker-quickfix -c user.email=quickfix@tracker.local"
commit() {
  # shellcheck disable=SC2086  # IDENT is empty or the fixed -c pairs above
  git $IDENT -c commit.gpgsign=false commit "$@" > "$TMP/commit.out" 2>&1
}
rc=0
commit -q -F "$TMP/msg" || rc=$?
cat "$TMP/commit.out"
[ "$rc" -eq 0 ] || die "git commit failed (exit $rc) — see the hook/git output above; the worktree is kept at $WT"
if [ -n "$(stage_git status --porcelain)" ]; then
  echo "a pre-commit hook rewrote files — folding them into the commit (amend once)"
  stage_git add -A
  rc=0
  commit -q --amend --no-edit || rc=$?
  cat "$TMP/commit.out"
  [ "$rc" -eq 0 ] || die "git commit --amend failed (exit $rc) — see the output above; the worktree is kept at $WT"
  [ -z "$(stage_git status --porcelain)" ] \
    || die "the tree is still dirty after the commit — a pre-commit hook rewrites files on every commit; the worktree is kept at $WT"
fi

cd "$TOP"
git worktree remove --force "$WT"
cat <<EOF
committed to branch $BR (your checkout is unchanged)
next:
  git diff $BASE..$BR
  git merge $BR      # or cherry-pick
  gh pr create --head $BR
EOF
