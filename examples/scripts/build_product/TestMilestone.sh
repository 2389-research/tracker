set -eu

ATTEMPT_FILE=".ai/milestones/fix_attempts"
ATTEMPTS=0
if [ -f "$ATTEMPT_FILE" ]; then
  ATTEMPTS=$(cat "$ATTEMPT_FILE" 2>/dev/null || echo 0)
fi
# Reset a corrupted/non-numeric counter so the arithmetic below can't
# error under `set -e` (same guard as the warm-continue counter above).
case "$ATTEMPTS" in
  ''|*[!0-9]*) ATTEMPTS=0 ;;
esac
ATTEMPTS=$((ATTEMPTS + 1))
echo "$ATTEMPTS" > "$ATTEMPT_FILE"

# Run the ONE shared milestone green-gate (issue #406). The full
# build + per-stack tests + project CI logic lives in .ai/build/verify.sh
# (written by Setup) so this node, the Implement breach verify_command,
# and the FixMilestone breach verify_command all adjudicate "green" with
# the SAME script — a single source of truth. This node is the only place
# that wraps it with the fix-attempt counter and the tracker routing
# sentinels (tests-pass / escalate); verify.sh itself prints no markers.
#
# verify.sh exit codes:
#   0  green: build + every detected stack's tests + project CI gate pass
#   2  environment escalation (e.g. Makefile present but `make` absent) —
#      the LLM fix loop can't resolve it, so route straight to escalate
#   1  ordinary build/test/CI failure — hand to the fix loop
VERIFY_RC=0
sh .ai/build/verify.sh 2>&1 || VERIFY_RC=$?

if [ "$VERIFY_RC" -eq 2 ]; then
  echo "0" > "$ATTEMPT_FILE"
  printf 'escalate'
  exit 1
fi

# Green — reset counter and succeed.
if [ "$VERIFY_RC" -eq 0 ]; then
  echo "0" > "$ATTEMPT_FILE"
  printf 'tests-pass'
  exit 0
fi

# Failure — check if we've exhausted fix attempts.
echo "--- attempt $ATTEMPTS of 3 ---"
if [ "$ATTEMPTS" -ge 3 ]; then
  echo "ESCALATE: milestone failed after $ATTEMPTS attempts"
  printf 'escalate'
  exit 1
fi

# Normal failure — let the fix loop handle it
exit 1