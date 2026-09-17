set -eu
# Mirrors CheckReviewFixBudget, plus two fixes the clone must not omit: a
# numeric guard (a corrupted counter must not abort under set -eu) and an
# idempotent snapshot of the ORIGINAL spec on first entry (the fidelity
# gate diffs against it; ApprovePlan surfaces it). Gate-BEFORE-work: -gt 3
# = exactly 3 ForgeSpec attempts (do NOT change to -ge; #443 shape).
mkdir -p .ai/build .ai/decisions
if [ ! -f .ai/decisions/SPEC.original.md ]; then
  cp SPEC.md .ai/decisions/SPEC.original.md
fi
BUDGET_FILE=".ai/build/spec_forge_attempts"
MAX_ATTEMPTS=3
ATTEMPTS=0
if [ -f "$BUDGET_FILE" ]; then
  ATTEMPTS=$(cat "$BUDGET_FILE" 2>/dev/null || echo 0)
fi
case "$ATTEMPTS" in
  ''|*[!0-9]*) ATTEMPTS=0 ;;
esac
ATTEMPTS=$((ATTEMPTS + 1))
echo "$ATTEMPTS" > "$BUDGET_FILE"
if [ "$ATTEMPTS" -gt "$MAX_ATTEMPTS" ]; then
  printf 'spec-forge budget exhausted: %d attempts (max %d) — spec could not be hardened autonomously\n' "$ATTEMPTS" "$MAX_ATTEMPTS"
  exit 1
fi
printf 'spec-forge budget OK: attempt %d of %d\n' "$ATTEMPTS" "$MAX_ATTEMPTS"