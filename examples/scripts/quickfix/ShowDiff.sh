set -eu
# Print what the ReviewDiff gate shows: the agent's summary, a diffstat, and
# the worktree's diff against the base — new files included (`git add -N`
# marks them intent-to-add in the WORKTREE's index only; .tracker/ stays
# excluded). The diff is bounded by QUICKFIX_DIFF_LIMIT bytes (default
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
cd "$WT"
git add -A -N .
git diff "$BASE" > "$TMP/diff"

echo "## Summary"
echo
if [ -s .tracker/quickfix/summary.md ]; then
  cat .tracker/quickfix/summary.md
else
  echo "(no summary was written)"
fi
echo
if [ ! -s "$TMP/diff" ]; then
  echo "## Diff"
  echo
  echo "no changes: the worktree is identical to the base ($BASE)"
  exit 0
fi
echo "## Diff against $BASE"
echo
git diff --stat "$BASE"
echo
SIZE=$(wc -c < "$TMP/diff" | tr -d ' ')
if [ "$SIZE" -le "$LIMIT" ]; then
  cat "$TMP/diff"
  exit 0
fi
head -c "$LIMIT" "$TMP/diff"
echo
echo "diff truncated at $LIMIT of $SIZE bytes — see all of it: git -C \"$WT\" diff $BASE"
