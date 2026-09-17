set -eu
PLAN=".ai/decisions/milestones.md"
DONE_DIR=".ai/milestones/done"
mkdir -p "$DONE_DIR"

# Count completed milestones
DONE_COUNT=$(ls "$DONE_DIR" 2>/dev/null | wc -l | tr -d ' ')

# Extract total milestone count — flexible: matches "## Milestone" with any suffix.
# Handles "## Milestone 1", "## Milestone 1: Title", "## Milestone 1 — Setup", etc.
TOTAL=$(grep -ciE '^#{1,3}\s*milestone\s' "$PLAN" || echo 0)

if [ "$TOTAL" -eq 0 ]; then
  echo "ERROR: no milestone headers found in $PLAN"
  echo "Expected format: ## Milestone N: Title"
  exit 1
fi

if [ "$DONE_COUNT" -ge "$TOTAL" ]; then
  echo "ALL_MILESTONES_COMPLETE"
  printf 'all-done'
  exit 0
fi

NEXT=$((DONE_COUNT + 1))
echo "milestone $NEXT of $TOTAL"

# Extract this milestone's section — match ## Milestone N with any suffix,
# stop at the next ## Milestone header or end of file.
awk "/^#+ *[Mm]ilestone *$NEXT[^0-9]/,/^#+ *[Mm]ilestone *$((NEXT+1))[^0-9]/" "$PLAN" | \
  sed '/^#\{1,3\} *[Mm]ilestone *'"$((NEXT+1))"'/d' > .ai/milestones/current.md

# Guard: fail loudly if extraction produced an empty file
if [ ! -s .ai/milestones/current.md ]; then
  echo "ERROR: failed to extract milestone $NEXT from $PLAN"
  echo "Check that milestone headers match: ## Milestone N: ..."
  cat "$PLAN" | head -30
  exit 1
fi

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