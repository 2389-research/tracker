set -eu
# Shared helpers via the engine-interpolated ${graph.workflow_dir} (author-
# controlled, safe-key allowlisted). Fail loud if empty.
[ -n "${graph.workflow_dir}" ] || { echo "ERROR: graph.workflow_dir is empty — cannot locate build_product's scripts/build_product/lib/ (embedded built-in: engine failed to materialize .tracker/workflow/; packed .dipx: unsupported, see #430)"; exit 1; }
LIB="${graph.workflow_dir}/scripts/build_product/lib"
. "$LIB/counters.sh"
. "$LIB/gate-integrity.sh"

ATTEMPT_FILE=".ai/milestones/fix_attempts"
mkdir -p .ai/milestones .ai/build

# #640 D6: the gate machinery under .ai/build/ is agent-writable and
# gitignored. Re-emit verify.sh / ci-probe.sh from the workflow sidecar
# before every run (WARNING names any file that differed — that is the
# self-ratification signal) and diff the known_* hatch files and operator
# stamps against the snapshot PickNextMilestone took at milestone start, so
# VerifyMilestone sees every addition.
restore_gate_files "$LIB"
report_hatch_additions

# Run the ONE shared milestone green-gate (issue #406). The full
# build + per-stack tests + project CI logic lives in .ai/build/verify.sh
# so this node, the Implement breach verify_command, and the FixMilestone
# breach verify_command all adjudicate "green" with the SAME script — a
# single source of truth. This node is the only place that wraps it with
# the fix-attempt counter and the tracker routing sentinels (tests-pass /
# escalate); verify.sh itself prints no markers.
#
# verify.sh exits 0 (green) or 1 (anything else). The one environment
# case the fix loop cannot solve — Makefile present but `make` missing —
# is signalled OUT OF BAND by .ai/build/ci-make-missing (#640 E8), never
# by an exit number: a missing script exits 2 under dash and 127 under
# bash, so a bare "rc 2 means make-missing" collided with it.
rm -f .ai/build/ci-make-missing
VERIFY_RC=0
sh .ai/build/verify.sh 2>&1 || VERIFY_RC=$?

if [ -f .ai/build/ci-make-missing ]; then
  echo "ESCALATE: environment problem (see _TRACKER_CI_MAKE_MISSING above) — the fix loop cannot resolve it"
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

# Red — the fix-attempt counter is bumped ONLY on a red result, AFTER the
# verify completed (#640 B3): a Ctrl-C mid-verify + `tracker -r`, or an
# engine-level retry of FixMilestone (transient provider error) that
# re-enters this node, no longer eats an attempt. Exactly 3 red attempts
# then escalate (#443). bump_counter resets a corrupted/non-numeric counter
# so the arithmetic can't error under `set -e`.
bump_counter "$ATTEMPT_FILE"
echo "--- attempt $ATTEMPTS of 3 ---"
if [ "$ATTEMPTS" -ge 3 ]; then
  echo "ESCALATE: milestone failed after $ATTEMPTS attempts"
  # #640 B2: reset on the escalate path too, so `EscalateMilestone retry ->
  # Implement` starts with a fresh fix budget instead of "attempt 4 of 3".
  echo "0" > "$ATTEMPT_FILE"
  printf 'escalate'
  exit 1
fi

# Normal failure — let the fix loop handle it
exit 1
