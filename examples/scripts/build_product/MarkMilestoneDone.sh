set -eu
# Shared helpers via the engine-interpolated ${graph.workflow_dir} (author-
# controlled, safe-key allowlisted). Fail loud if empty.
[ -n "${graph.workflow_dir}" ] || { echo "ERROR: graph.workflow_dir is empty — cannot locate build_product's scripts/build_product/lib/ (embedded built-in: engine failed to materialize .tracker/workflow/; packed .dipx: unsupported, see #430)"; exit 1; }
LIB="${graph.workflow_dir}/scripts/build_product/lib"
. "$LIB/milestones.sh"
DONE_DIR=".ai/milestones/done"
CUR=".ai/milestones/current.md"
mkdir -p "$DONE_DIR"
# #640 B5: fail LOUD when there is nothing to mark done. current.md is
# missing after PickNextMilestone's all-done path (it never writes one) and
# after a prior mark-done; a 0-byte one would otherwise become a 0-byte
# done marker and silently advance the plan. A strict-failure node, so no
# marker is printed and the pipeline halts here.
if [ ! -s "$CUR" ]; then
  echo "ERROR: $CUR is missing or empty — nothing to mark done."
  echo "PickNextMilestone did not extract a milestone (or this one was already marked done). Refusing to write an empty done marker."
  exit 1
fi
# The done marker is keyed by the HEADER number of the current section
# (PickNextMilestone picks by header number, so gaps like 1,2,4 stay in
# sync); a header without a number falls back to done-count + 1.
NEXT=$(milestone_number_of "$(head -1 "$CUR")")
if [ -z "$NEXT" ]; then
  DONE_COUNT=$(count_done_milestones "$DONE_DIR")
  NEXT=$((DONE_COUNT + 1))
fi
cp "$CUR" "$DONE_DIR/milestone-$NEXT.md"

# Append this milestone's entry to the build-context file (issue #298).
# MarkMilestoneDone is a strict-failure tool node (one unconditional edge,
# no fallback_target), so a logging hiccup must NOT dead-stop a verified
# milestone: the whole block runs in a `set +e` subshell that cannot
# propagate a non-zero exit, all bulk output goes to the FILE, and the
# terminal `printf` marker below stays last on stdout.
(
  set +e
  # Two-dot range from this milestone's start to HEAD. Degrade BASE to the
  # empty tree when START is empty (first milestone / absent marker) OR
  # unreachable (a retry rewrote/orphaned it). Three-dot (...) would fatal
  # on the empty-tree object and under-report on rewritten history.
  START=$(cat .ai/build/milestone-start-sha 2>/dev/null || true)
  if [ -z "$START" ] || ! git cat-file -e "${START}^{commit}" 2>/dev/null; then
    BASE=$(git hash-object -t tree /dev/null)
  else
    BASE="$START"
  fi
  TITLE=$(head -1 "$CUR" 2>/dev/null)
  [ -n "$TITLE" ] || TITLE="## Milestone $NEXT"
  FILES_ALL=$(git diff --name-only "${BASE}..HEAD" 2>/dev/null | sed '/^$/d')
  # #351 belt-and-braces: filter tracker run metadata and the build-context
  # machinery itself out of the Files: lines so they carry source signal,
  # not pipeline internals. (.ai/ is gitignored at Setup and .tracker/ is
  # excluded via .git/info/exclude, but a pre-existing tree or an agent
  # rewriting .gitignore can still land them in checkpoint commits.)
  FILES=$(printf '%s\n' "$FILES_ALL" | grep -vE '^\.tracker/|^\.ai/build/' || true)
  NFILES=$(printf '%s\n' "$FILES" | grep -c . || true)
  NALL=$(printf '%s\n' "$FILES_ALL" | grep -c . || true)
  # Summary = this milestone's newest commit subject, read from HEAD
  # directly. NOT the BASE..HEAD range: when BASE is the empty tree (first
  # milestone / unreachable START), `git log <tree>..HEAD` leans on lenient
  # `^<tree>` handling that isn't guaranteed across git versions. Gate on
  # NALL (the unfiltered count) so a no-op "mark done" milestone (zero
  # files, HEAD unmoved) degrades to the title instead of echoing the
  # prior milestone's subject — metadata-only commits still moved HEAD,
  # so their subject is genuinely this milestone's.
  if [ "$NALL" -eq 0 ]; then
    SUMMARY="$TITLE"
  else
    SUMMARY=$(git log -1 --format=%s HEAD 2>/dev/null)
    [ -n "$SUMMARY" ] || SUMMARY="$TITLE"
  fi
  {
    echo
    printf '%s\n' "$TITLE"
    if [ "$NFILES" -eq 0 ]; then
      # Distinguish "nothing changed" from "everything was filtered" —
      # an empty Files: line must say why, not print nothing (#351).
      if [ "$NALL" -gt 0 ]; then
        echo "Files: (only tracker/build metadata changed)"
      else
        echo "Files: (none)"
      fi
    else
      printf 'Files: %s\n' "$(printf '%s\n' "$FILES" | head -12 | paste -sd, -)"
      [ "$NFILES" -gt 12 ] && echo "Files: … and $((NFILES - 12)) more"
    fi
    printf 'Summary: %s\n' "$SUMMARY"
    # #351 item 3: refresh the active-source-files section so agents
    # reading the orientation file see which source files are being
    # actively worked, not just the initial entry-point list from Setup.
    if [ "$NFILES" -gt 0 ]; then
      echo "Active source files (as of milestone $NEXT): $(printf '%s\n' "$FILES" | head -20 | paste -sd, -)"
    fi
  } >> .ai/build/build-context.md
) 2>/dev/null || true
rm -f .ai/build/milestone-start-sha

# Reset the per-milestone loop state for the next milestone: the fix-attempt
# counter, group R's verify-fail counter (CheckVerifyFailBudget), group
# V's known_failures / known_lint_failures snapshots (taken on a milestone's
# first TestMilestone) — #640 A4/B2: this is the ONE place they reset — and
# the milestone's declared contract tests (tracker-runner #901; the next
# PickNextMilestone writes its own).
rm -f .ai/milestones/fix_attempts .ai/milestones/verify_fail_attempts \
      .ai/milestones/known_failures.snapshot .ai/milestones/known_lint_failures.snapshot \
      .ai/milestones/opt-outs.snapshot .ai/milestones/contract-tests
# #318: reset the warm-continue cap counter + MaxTurns override at the
# milestone boundary so the next milestone's Implement starts at its base
# turn budget with a fresh continue allowance.
rm -rf .tracker/turn_overrides
rm -f "$CUR"
printf "milestone-$NEXT-complete"