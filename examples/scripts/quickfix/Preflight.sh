set -eu
# quickfix's change is made in a separate git worktree on a new branch,
# tracker/quickfix/<run-id>, so the user's checkout, branch and working tree
# are never touched: a dirty tree doesn't block the run, the agent can't move
# the user's branch, and discarding needs no reset.
#
# Creates <workdir>/.tracker/quickfix/worktree from HEAD (detached HEAD is
# fine) and records the branch, base SHA and worktree path in
# $TRACKER_RUN_DIR/quickfix.json (for later tooling) and
# .tracker/quickfix/{base_sha,branch} (for this workflow's later nodes).
# Refuses — creating nothing — when a previous run's worktree or this run's
# branch is still there, printing the exact cleanup commands.
#
# Only the `.tracker/` LOCAL exclude is added (git_exclude_add, through
# `git rev-parse --git-path`, so a linked worktree resolves to the common
# info/exclude). build_product's exclude_tracker_metadata also untracks a
# committed .tracker/ — that stages a deletion in the user's index, which
# quickfix promises not to touch, so it is not used here.
[ -n "${graph.workflow_dir}" ] || { echo "ERROR: Preflight: graph.workflow_dir is empty — cannot locate scripts/quickfix/lib/" >&2; exit 1; }
LIB="${graph.workflow_dir}/scripts/quickfix/lib"
. "$LIB/gitignore.sh"
die() { printf 'ERROR: Preflight: %s\n' "$*" >&2; exit 1; }

[ -n "${TRACKER_RUN_ID:-}" ] && [ -n "${TRACKER_RUN_DIR:-}" ] \
  || die "TRACKER_RUN_ID / TRACKER_RUN_DIR are not set — run quickfix through tracker"
git rev-parse --is-inside-work-tree >/dev/null 2>&1 \
  || die "not a git repository: $(pwd) — quickfix makes its change on a new branch, so it needs a git repo (git init, then commit your files)"
BASE=$(git rev-parse --verify -q HEAD) \
  || die "the repository has no commits yet — commit your files first; quickfix branches from HEAD"

WT="$(pwd)/.tracker/quickfix/worktree"
BR="tracker/quickfix/$TRACKER_RUN_ID"

if [ -e "$WT" ]; then
  OLD=$(git -C "$WT" branch --show-current 2>/dev/null || true)
  {
    printf 'ERROR: Preflight: a previous quickfix run left its worktree at %s\n' "$WT"
    printf 'Keep or merge its branch first if you want it, then clean up and re-run:\n'
    printf '  git worktree remove --force "%s"\n' "$WT"
    [ -z "$OLD" ] || printf '  git branch -D %s\n' "$OLD"
    [ "$OLD" = "$BR" ] || ! git show-ref --verify -q "refs/heads/$BR" || printf '  git branch -D %s\n' "$BR"
  } >&2
  exit 1
fi
if git show-ref --verify -q "refs/heads/$BR"; then
  {
    printf 'ERROR: Preflight: branch %s already exists (a crashed run of this ID)\n' "$BR"
    printf 'Clean up and re-run:\n'
    printf '  git worktree prune\n'
    printf '  git branch -D %s\n' "$BR"
  } >&2
  exit 1
fi

git_exclude_add ".tracker/"

if [ -n "$(git status --porcelain)" ]; then
  echo "note: your checkout has uncommitted changes; they stay where they are and are not part of the quickfix base ($BASE)"
fi

mkdir -p .tracker/quickfix
rm -f .tracker/quickfix/revise_count
git worktree add -q -b "$BR" "$WT" "$BASE" >&2 \
  || die "git worktree add failed for $WT (see git's message above)"
printf '%s\n' "$BASE" > .tracker/quickfix/base_sha
printf '%s\n' "$BR" > .tracker/quickfix/branch

# JSON-escape the path (the only value that can hold \ or ").
json_str() { printf '%s' "$1" | sed 's/\\/\\\\/g; s/"/\\"/g'; }
mkdir -p "$TRACKER_RUN_DIR"
printf '{\n  "branch": "%s",\n  "base_sha": "%s",\n  "worktree": "%s"\n}\n' \
  "$BR" "$BASE" "$(json_str "$WT")" > "$TRACKER_RUN_DIR/quickfix.json"

echo "quickfix worktree: $WT"
echo "branch: $BR (from $BASE)"
printf 'preflight-ok'
