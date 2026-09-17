set -u
mkdir -p .ai/build
OUT=.ai/build/review-diff.md
# #424 review: bail loudly if this isn't a git work tree. Otherwise the
# git commands below emit error text, the file is non-empty so the
# empty-artifact guard never trips, and reviewers see git errors dressed
# up as a valid diff. Stamp UNAVAILABLE and route on (the diff is advisory).
if ! git rev-parse --is-inside-work-tree >/dev/null 2>&1; then
  echo "WARNING: not inside a git work tree; cannot compute review diff" >&2
  echo "# Review diff UNAVAILABLE — not a git work tree. Read the working tree directly." > "$OUT"
  printf 'review-diff-ready'
  exit 0
fi
BASE=$(cat .ai/build/run-base-sha 2>/dev/null || true)
if [ -z "$BASE" ] || ! git cat-file -e "${BASE}^{commit}" 2>/dev/null; then
  BASE=$(git hash-object -t tree /dev/null)
fi
# #424 review: diff BASE against the WORKING TREE (not BASE..HEAD), so
# accepted-but-uncommitted milestone work — the `accept` gate routes here
# WITHOUT committing first — is included instead of silently omitted from
# what reviewers are told is "the complete set of changes". `git diff
# <commit>` (no ..HEAD) compares the commit to the WORKING TREE, so it
# reflects the current on-disk content of tracked files (committed work plus
# any uncommitted edits present in the worktree). It compares against the
# worktree, NOT the index, so a staged change that doesn't match the worktree
# (#424 review: stage version B, then revert the file on disk) isn't captured
# — but in this workflow the worktree IS the source of truth (agents edit
# files, they don't manage a staging area), so worktree state is exactly what
# reviewers should see. Untracked files are listed separately below. On a
# clean tree this is identical to BASE..HEAD.
#
# git stderr is kept IN the artifact (2>&1, not 2>/dev/null) so a failure
# is diagnosable, but #424 review: an empty-output guard alone is not
# enough — most git failure modes (safe.directory, corrupt repo, bad
# object) exit non-zero while STILL printing error text, so the artifact
# is non-empty and reviewers see git errors dressed up as a valid diff.
# Capture each `git diff` exit status explicitly (the brace group runs in
# this shell, not a subshell, so DIFF_ERR persists) and stamp UNAVAILABLE
# on any failure (CLAUDE.md: never silently swallow errors).
DIFF_ERR=0
{
  echo "# Cumulative review diff (base..worktree)"
  echo
  echo "_Base: ${BASE} → working tree. The diff below covers every TRACKED-file"
  echo "change present in the working tree relative to base (committed work plus"
  echo "any uncommitted edits on disk); untracked files are listed"
  echo "by path in their own section (their content is NOT diffed here). It is"
  echo "your PRIMARY read; the full working tree is available on demand if a"
  echo "finding needs surrounding context._"
  echo
  echo '## Files changed'
  git diff --name-status "${BASE}" 2>&1 || DIFF_ERR=1
  UNTRACKED=$(git ls-files --others --exclude-standard 2>/dev/null)
  if [ -n "$UNTRACKED" ]; then
    echo
    echo '## Untracked files (not yet in any commit)'
    printf '%s\n' "$UNTRACKED"
  fi
  echo
  echo '## Full diff'
  git diff "${BASE}" 2>&1 || DIFF_ERR=1
} > "$OUT"
# Fail loudly if a diff command errored OR the artifact is empty —
# reviewers depend on it as their PRIMARY input. Don't dead-stop (the diff
# is advisory routing context), but warn on stderr and prepend a clear
# UNAVAILABLE banner so a git error can't masquerade as a valid diff. The
# git stderr captured into the file (2>&1) is PRESERVED below the banner in
# a fenced block — overwriting it outright would discard the underlying
# cause (safe.directory / corrupt repo / bad object) and make the failure
# undebuggable (#424 review). The banner removes the masquerade risk; the
# preserved output keeps it diagnosable.
if [ "$DIFF_ERR" -ne 0 ] || [ ! -s "$OUT" ]; then
  echo "WARNING: review diff generation failed or produced no output; reviewers must read the working tree directly" >&2
  # Stream the captured git output through a temp file instead of slurping
  # the whole artifact into a shell variable: a partial failure (e.g.
  # name-status succeeds and writes a large diff, then `git diff` fails)
  # can leave a large $OUT, and `CAPTURED=$(cat "$OUT")` would risk shell
  # memory / arg-length limits — turning a recoverable diff failure into a
  # node failure or OOM (#424 review). We can't `cat "$OUT"` into a
  # redirect to "$OUT" (truncation race), so build the banner+block in a
  # temp file and atomically mv it over the artifact.
  TMP="${OUT}.tmp"
  {
    echo "# Review diff UNAVAILABLE — diff generation failed. Read the working tree directly."
    echo
    if [ -s "$OUT" ]; then
      echo "Captured git output (likely the underlying cause — e.g. safe.directory, corrupt repo, bad object):"
      echo
      echo '```'
      cat "$OUT"
      echo '```'
    else
      echo "(no git output was captured)"
    fi
  } > "$TMP"
  mv "$TMP" "$OUT"
fi
printf 'review-diff-ready'