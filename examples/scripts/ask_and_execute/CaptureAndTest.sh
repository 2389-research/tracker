set -eu
# #646 item 2: EVERY candidate gets a diff and a test log. The old body ran
# `go build && go test` / `npm test` bare under `set -e`, so the first red
# candidate aborted the node — no .test files for the rest, empty RESULTS,
# and the "TESTS FAIL (exit=N)" branch was unreachable. Now each runner is
# wrapped `|| TEST_EXIT=$?` and the node's own exit is a verdict on the SET:
# exit 1 (→ the AllCandidatesRed gate) only when no candidate is usable.
BASE=$(cat .ai/candidates/base-sha 2>/dev/null || true)
RESULTS=""
RED=0
TOTAL=0
for NAME in claude codex gemini; do
  TOTAL=$((TOTAL + 1))
  WTDIR=".ai/worktrees/$NAME"
  BRANCH="impl/$NAME"
  DIFF_FILE=".ai/candidates/$NAME.diff"
  TEST_FILE=".ai/candidates/$NAME.test"
  : > "$DIFF_FILE"
  : > "$TEST_FILE"
  if [ ! -d "$WTDIR" ]; then
    echo "worktree $WTDIR is missing — the candidate was never set up or was removed" > "$TEST_FILE"
    RESULTS="$RESULTS\n$NAME: MISSING WORKTREE (no diff, no tests)"
    RED=$((RED + 1))
    continue
  fi

  # Fork point: the sha SetupWorktrees recorded; fall back to the merge-base
  # (a resumed run whose .ai/candidates/ was cleaned).
  CAND_BASE="$BASE"
  if [ -z "$CAND_BASE" ] || ! git cat-file -e "${CAND_BASE}^{commit}" 2>/dev/null; then
    CAND_BASE=$(git merge-base HEAD "$BRANCH" 2>/dev/null || true)
  fi

  # The prompts say "commit your work", but an agent that stopped short left
  # an EMPTY branch diff and its work was destroyed by the old teardown.
  # Checkpoint uncommitted work ON THE BRANCH (what ApplyWinner merges) with a
  # fixed message. Never --no-verify: a hook that rejects the commit is a
  # finding — the work stays STAGED (so `git diff <base>` below still shows
  # it, new files included) and the result line carries the hook's own
  # output so the critique can see why.
  NOTE=""
  if [ -n "$(git -C "$WTDIR" status --porcelain --untracked-files=all 2>/dev/null)" ]; then
    # Explicit identity + unsigned, like build_product's CommitIfDirty: the
    # checkpoint must not depend on the tool subprocess's git config.
    COMMIT_OUT=$(git -C "$WTDIR" add -A 2>&1 && git -C "$WTDIR" -c user.name="ask_and_execute" -c user.email="ask_and_execute@tracker.local" -c commit.gpgsign=false commit -q -m "chore($NAME): checkpoint uncommitted candidate work (ask_and_execute CaptureAndTest)" 2>&1) && COMMIT_RC=0 || COMMIT_RC=$?
    if [ "$COMMIT_RC" -eq 0 ]; then
      NOTE=" [uncommitted work checkpointed on $BRANCH]"
    else
      HOOK_MSG=$(printf '%s' "$COMMIT_OUT" | tr '\n' ' ' | sed 's/[[:space:]]\{2,\}/ /g' | cut -c1-300)
      NOTE=" [WARNING: uncommitted work could NOT be committed (git exit $COMMIT_RC: $HOOK_MSG) — it is in the diff (staged) but NOT on $BRANCH; ApplyWinner merges the branch only]"
    fi
  fi

  # Diff fork point → WORKTREE: committed work, and — when the checkpoint
  # commit was rejected — the still-staged work, new files included (`git
  # diff <commit>` compares against the working tree, and a staged new file
  # is part of it). Anything still untracked is listed after the diff.
  if [ -n "$CAND_BASE" ]; then
    git -C "$WTDIR" diff "$CAND_BASE" > "$DIFF_FILE" 2>/dev/null || true
    UNTRACKED=$(git -C "$WTDIR" ls-files --others --exclude-standard 2>/dev/null || true)
    if [ -n "$UNTRACKED" ]; then
      printf '%s\n' "$UNTRACKED" | sed 's/^/# untracked (NOT in this diff, NOT on the branch): /' >> "$DIFF_FILE"
    fi
  else
    echo "# no fork point recorded — diff unavailable" > "$DIFF_FILE"
  fi

  # Run the candidate's tests in its worktree. Every runner is wrapped so a
  # red candidate records its exit and the loop continues to the next one.
  TEST_EXIT=0
  STACK="none"
  if [ -f "$WTDIR/go.mod" ]; then
    STACK=go
    ( cd "$WTDIR" && go build ./... && go test ./... ) > "$TEST_FILE" 2>&1 || TEST_EXIT=$?
  elif [ -f "$WTDIR/package.json" ]; then
    STACK=npm
    ( cd "$WTDIR" && npm test ) > "$TEST_FILE" 2>&1 || TEST_EXIT=$?
  elif [ -f "$WTDIR/pyproject.toml" ]; then
    STACK=python
    ( cd "$WTDIR" && uv run pytest ) > "$TEST_FILE" 2>&1 || TEST_EXIT=$?
  elif [ -f "$WTDIR/Cargo.toml" ]; then
    STACK=cargo
    ( cd "$WTDIR" && cargo test ) > "$TEST_FILE" 2>&1 || TEST_EXIT=$?
  else
    echo "no known build system (go.mod / package.json / pyproject.toml / Cargo.toml) — nothing was tested" > "$TEST_FILE"
  fi

  DIFF_LINES=$(grep -c '' "$DIFF_FILE" 2>/dev/null || true)
  CHANGED=$(grep -c '^diff --git ' "$DIFF_FILE" 2>/dev/null || true)
  if [ "$CHANGED" -eq 0 ]; then
    RESULTS="$RESULTS\n$NAME: EMPTY DIFF — no implementation on $BRANCH$NOTE"
    RED=$((RED + 1))
  elif [ "$STACK" = none ]; then
    RESULTS="$RESULTS\n$NAME: NO TESTS (no known build system), diff=$DIFF_LINES lines, $CHANGED files$NOTE"
  elif [ "$TEST_EXIT" -eq 0 ]; then
    RESULTS="$RESULTS\n$NAME: TESTS PASS ($STACK), diff=$DIFF_LINES lines, $CHANGED files$NOTE"
  else
    RESULTS="$RESULTS\n$NAME: TESTS FAIL ($STACK, exit=$TEST_EXIT), diff=$DIFF_LINES lines, $CHANGED files$NOTE"
    RED=$((RED + 1))
  fi
done
# %b expands the \n escapes in RESULTS while keeping it data, not a
# format string — test names containing % can't break the printf. Written
# to a file and cat'd so the only printf'd stdout are the two routing
# markers below (dippin's coverage analysis reads printf formats as outputs).
printf '%b\n' "$RESULTS" > .ai/candidates/results.txt
cat .ai/candidates/results.txt
if [ "$RED" -ge "$TOTAL" ]; then
  echo "ERROR: no usable candidate — all $TOTAL are red (tests failing, empty, or missing). Evidence: .ai/candidates/<name>.{diff,test}"
  printf 'all-candidates-red'
  exit 1
fi
echo "usable candidates: $((TOTAL - RED)) of $TOTAL"
printf 'candidates-captured'
