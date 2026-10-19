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
die() { printf 'ERROR: ShowDiff: %s\n' "$*" >&2; exit 1; }
WT="$(pwd)/.tracker/quickfix/worktree"
[ -d "$WT" ] || die "no quickfix worktree at $WT — Preflight did not run or it was removed"
BASE=$(cat .tracker/quickfix/base_sha 2>/dev/null || true)
[ -n "$BASE" ] || die "no base recorded at .tracker/quickfix/base_sha — Preflight did not finish"
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
export GIT_INDEX_FILE="$TMP/index"
git read-tree HEAD
git add -A -N .
git diff "$BASE" > "$TMP/diff"
git diff --stat "$BASE" > "$TMP/stat"
unset GIT_INDEX_FILE

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
cat "$TMP/stat"
echo
SIZE=$(wc -c < "$TMP/diff" | tr -d ' ')
if [ "$SIZE" -le "$LIMIT" ]; then
  cat "$TMP/diff"
  exit 0
fi
head -c "$LIMIT" "$TMP/diff"
echo
echo "diff truncated at $LIMIT of $SIZE bytes — see all of it: git -C \"$WT\" diff $BASE"
