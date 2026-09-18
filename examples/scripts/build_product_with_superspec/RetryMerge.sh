set -eu
# #646 5c: the MergeConflict gate's "retry" lands here; the phase whose merge
# failed was recorded by merge_streams, and the exact marker below routes
# back to that phase's merge node (`endswith retry-merge-phaseN`).
PHASE=$(cat .ai/build/merge-phase 2>/dev/null || true)
case "$PHASE" in
  1|2|4|5) ;;
  *) echo "ERROR: no merge phase recorded in .ai/build/merge-phase (got '$PHASE') — cannot tell which phase merge to retry"; exit 1 ;;
esac
echo "retrying the phase $PHASE merge"
printf 'retry-merge-phase%s' "$PHASE"
