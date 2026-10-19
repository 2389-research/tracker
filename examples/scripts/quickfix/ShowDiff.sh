set -eu
# Print what the ReviewDiff gate shows: the agent's summary, a diffstat, and
# the worktree's diff against the base — new files included (`git add -N`
# into a THROWAWAY index: the worktree's own index is never touched, so new
# files stay untracked and Commit's secret/binary exclusion still sees them;
# .tracker/ stays excluded). It also warns when the user's own checkout has
# changed since Preflight — nothing confines a claude-code agent to its
# worktree, so the gate is where a stray write is caught. The diff is bounded by QUICKFIX_DIFF_LIMIT bytes (default
# 40000, under the tool's output_limit); past it the diff is cut and the last
# line says so with the command that shows all of it, never a silent tail.
# Nothing is committed and the user's checkout is not touched.
[ -n "${graph.workflow_dir}" ] || { echo "ERROR: ShowDiff: graph.workflow_dir is empty — cannot locate scripts/quickfix/lib/" >&2; exit 1; }
LIB="${graph.workflow_dir}/scripts/quickfix/lib"
. "$LIB/stage.sh"
die() { printf 'ERROR: ShowDiff: %s\n' "$*" >&2; exit 1; }
WT="$(pwd)/.tracker/quickfix/worktree"
[ -d "$WT" ] || die "no quickfix worktree at $WT — Preflight did not run or it was removed"
BASE=$(cat .tracker/quickfix/base_sha 2>/dev/null || true)
[ -n "$BASE" ] || die "no base recorded at .tracker/quickfix/base_sha — Preflight did not finish"
# fence FILE… — a backtick fence longer than any backtick run in FILEs (at
# least 3): the gate renders markdown, and an unfenced diff loses its +/-
# lines; a ``` inside the content must not close the fence early.
fence() {
  _n=$(awk '{ s = $0; while (match(s, /`+/)) { if (RLENGTH > m) m = RLENGTH; s = substr(s, RSTART + RLENGTH) } } END { print m + 1 }' "$@")
  [ "$_n" -ge 3 ] || _n=3
  _f=""; while [ "${#_f}" -lt "$_n" ]; do _f="$_f\`"; done
  printf '%s' "$_f"
}
LIMIT="${QUICKFIX_DIFF_LIMIT:-40000}"
case "$LIMIT" in ''|*[!0-9]*|0) LIMIT=40000 ;; esac

TMP=$(mktemp -d)
trap 'rm -rf "$TMP"' EXIT

# The user's checkout, compared with Preflight's snapshot.
OUTSIDE=""
if [ -f .tracker/quickfix/user_state ]; then
  { git rev-parse HEAD; git status --porcelain; git diff HEAD | cksum; } > "$TMP/user_state" 2>/dev/null || true
  cmp -s .tracker/quickfix/user_state "$TMP/user_state" || OUTSIDE=$(git status --porcelain)
fi

cd "$WT"
# The same exclusions Commit stages with (lib/stage.sh, .tracker/ too): what
# the gate shows is what approval commits, and an excluded secret's contents
# never reach the gate or the run's output. Its skip notices go to the gate.
mkdir -p "$TMP/stage"
stage_build_excludes "$TMP/stage" > "$TMP/skipped"
printf '%s\n' '.tracker/' >> "$STAGE_EXCLUDE"
export GIT_INDEX_FILE="$TMP/index"
git read-tree HEAD
stage_git add -A -N .
git diff "$BASE" > "$TMP/diff"
git diff --stat "$BASE" > "$TMP/stat"
unset GIT_INDEX_FILE

if [ -s "$TMP/skipped" ]; then
  cat <<'EOF'
## Not shown, and not committed on approve

EOF
  cat "$TMP/skipped"
  echo
fi

if [ -n "$OUTSIDE" ]; then
  cat <<EOF
## WARNING: files changed OUTSIDE the quickfix worktree

Your own checkout changed since this run started (the agent may have
written outside its worktree, or you edited it yourself). Check it before
you approve; approving commits only the worktree's change.

$OUTSIDE

EOF
fi

# Headings and notices are heredocs: this node routes on its exit code, and
# none of its output is a routing marker.
cat <<'EOF'
## Summary

EOF
if [ -s .tracker/quickfix/summary.md ]; then
  cat .tracker/quickfix/summary.md
else
  cat <<'EOF'
(no summary was written)
EOF
fi
echo
if [ ! -s "$TMP/diff" ]; then
  cat <<EOF
## Diff

no changes: the worktree is identical to the base ($BASE)
EOF
  exit 0
fi
echo "## Diff against $BASE"
echo
F=$(fence "$TMP/stat" "$TMP/diff")
echo "${F}text"
cat "$TMP/stat"
echo "$F"
echo
echo "${F}diff"
SIZE=$(wc -c < "$TMP/diff" | tr -d ' ')
if [ "$SIZE" -le "$LIMIT" ]; then
  cat "$TMP/diff"
  echo "$F"
  exit 0
fi
head -c "$LIMIT" "$TMP/diff"
echo
echo "$F"
echo "diff truncated at $LIMIT of $SIZE bytes — see all of it: git -C \"$WT\" diff $BASE"
