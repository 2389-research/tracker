set -eu
# Shared helpers via the engine-interpolated ${graph.workflow_dir} (author-
# controlled, safe-key allowlisted). Fail loud if empty.
[ -n "${graph.workflow_dir}" ] || { echo "ERROR: graph.workflow_dir is empty — cannot locate build_product's scripts/build_product/lib/ (embedded built-in: engine failed to materialize .tracker/workflow/; packed .dipx: unsupported, see #430)"; exit 1; }
LIB="${graph.workflow_dir}/scripts/build_product/lib"
. "$LIB/milestones.sh"
PLAN=".ai/decisions/milestones.md"
DONE_DIR=".ai/milestones/done"
# #640 B4: .ai/build may be gone (Cleanup ran, then EscalateReview `retry`
# re-planned without a Setup) — recreate it before the start-sha write.
mkdir -p "$DONE_DIR" .ai/build

# #640 E4/E5: ONE header regex (lib/milestones.sh) for counting AND
# extraction. Headers: `##`..`####` + "Milestone" (any case) + N, with an
# optional `#`, leading zeros, and any `: title` / ` — title` / bare suffix.
# `## Milestone overview` (no number) is not a header and `## Milestone 1.1`
# is part of milestone 1's body.
NUMBERS=$(milestone_numbers "$PLAN")
if [ -z "$NUMBERS" ]; then
  echo "ERROR: no milestone headers found in $PLAN"
  echo "Expected format: ## Milestone N: Title"
  exit 1
fi
DUPS=$(milestone_duplicates "$PLAN")
if [ -n "$DUPS" ]; then
  echo "ERROR: duplicate milestone headers in $PLAN: $(printf '%s\n' "$DUPS" | paste -sd' ' -)"
  echo "Each '## Milestone N: Title' number must be unique — re-plan (Decompose) with distinct numbers."
  exit 1
fi
TOTAL=$(printf '%s\n' "$NUMBERS" | wc -l | tr -d ' ')

# NEXT = the smallest header number with no done marker — not DONE_COUNT+1,
# so a numbering gap (1, 2, 4) or a stray marker can never point at a
# milestone that isn't in the plan.
NEXT=""
for n in $NUMBERS; do
  if [ ! -f "$DONE_DIR/milestone-$n.md" ]; then NEXT=$n; break; fi
done
if [ -z "$NEXT" ]; then
  echo "ALL_MILESTONES_COMPLETE"
  printf 'all-done'
  exit 0
fi
DONE_COUNT=$(count_done_milestones "$DONE_DIR")
echo "milestone $NEXT ($((DONE_COUNT + 1)) of $TOTAL planned)"

# Extract this milestone's section (header through the line before the next
# header). #640 B5: write to a temp file and move it into place only on
# success, so a failed extraction can never leave an EMPTY current.md for
# Implement to build from / MarkMilestoneDone to copy as a 0-byte marker.
TMP=".ai/milestones/.current.md.tmp"
extract_milestone "$NEXT" "$PLAN" > "$TMP"
if [ ! -s "$TMP" ]; then
  rm -f "$TMP"
  echo "ERROR: failed to extract milestone $NEXT from $PLAN"
  echo "Check that milestone headers match: ## Milestone N: ..."
  head -30 "$PLAN"
  exit 1
fi
mv -f "$TMP" .ai/milestones/current.md

# Record this milestone's start boundary for MarkMilestoneDone's
# files-touched diff (issue #298). --verify --quiet prints nothing and
# exits non-zero on a commitless repo, so START is genuinely empty there
# (a bare `git rev-parse HEAD` would leak the literal "HEAD" to stdout and
# silently empty milestone 1's file record). Written on the has-next path
# only — the all-done branch returned earlier. Goes to a FILE so the
# routing marker below stays last on stdout.
START=$(git rev-parse --verify --quiet HEAD 2>/dev/null || true)
printf '%s\n' "$START" > .ai/build/milestone-start-sha

printf "milestone-$NEXT"
