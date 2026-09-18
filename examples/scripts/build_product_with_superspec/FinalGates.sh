set -eu
[ -n "${graph.workflow_dir}" ] || { echo "ERROR: graph.workflow_dir is empty — cannot locate build_product_with_superspec's scripts/build_product_with_superspec/lib/ (embedded built-in: engine failed to materialize .tracker/workflow/; packed .dipx: unsupported, see #430)"; exit 1; }
LIB="${graph.workflow_dir}/scripts/build_product_with_superspec/lib"
. "$LIB/gates.sh"
. "$LIB/traceability.sh"
start_gate final
gate_verify --final   # whole tree, -count=1, zero tests = FAIL (#640 D7)
gate_coverage
gate_complexity
gate_gold

# QG-1: traceability completeness. Every count is a real number (#646 5f:
# `grep -c … || echo 0` printed "0\n0"). A requirement that is still
# pending or has no impl_ref FAILS; one with an impl_ref but
# `test_ref: null` FAILS unless waived in docs/traceability-waivers.txt
# (see lib/traceability.sh) — waivers are listed here for the audit.
echo "--- traceability (QG-1) ---" >> "$GATE_REPORT"
if [ -f docs/traceability.yaml ]; then
  if ! trace_lint docs/traceability.yaml >> "$GATE_REPORT" 2>&1; then
    GATE_PASS=false
  fi
  LEFTOVER_OVERLAYS=$(ls docs/traceability.*.yaml 2>/dev/null || true)
  if [ -n "$LEFTOVER_OVERLAYS" ]; then
    echo "UNMERGED STREAM OVERLAYS (a phase merge did not fold them): $LEFTOVER_OVERLAYS" >> "$GATE_REPORT"
    GATE_PASS=false
  fi
  TOTAL=$(trace_count '')
  PENDING=$(trace_count 'status: pending')
  NULL_IMPL=$(trace_count 'impl_ref: null')
  NULL_TEST=$(trace_count 'test_ref: null')
  echo "Requirements: $TOTAL  pending: $PENDING  missing impl_ref: $NULL_IMPL  missing test_ref: $NULL_TEST" >> "$GATE_REPORT"
  if [ "$PENDING" -gt 0 ] || [ "$NULL_IMPL" -gt 0 ]; then
    echo "TRACEABILITY INCOMPLETE: pending or unimplemented requirements:" >> "$GATE_REPORT"
    { trace_ids_matching 'status: pending'; trace_ids_matching 'impl_ref: null'; } | sort -u | sed 's/^/  /' >> "$GATE_REPORT"
    GATE_PASS=false
  fi
  UNTESTED=0
  for ID in $(trace_ids_matching 'test_ref: null'); do
    if trace_waived "$ID"; then
      echo "WAIVED test_ref for $ID: $(grep -E "^[[:space:]]*$ID([[:space:]]|$)" docs/traceability-waivers.txt | head -1 | sed -E 's/^[[:space:]]*[A-Z]+-[0-9]+[[:space:]]*//')" >> "$GATE_REPORT"
    else
      echo "UNTESTED requirement (impl without test_ref, no waiver): $ID" >> "$GATE_REPORT"
      UNTESTED=$((UNTESTED + 1))
    fi
  done
  if [ "$UNTESTED" -gt 0 ]; then
    echo "TRACEABILITY INCOMPLETE: $UNTESTED requirement(s) implemented without a test_ref (waive in docs/traceability-waivers.txt with a reason, or add the test)" >> "$GATE_REPORT"
    GATE_PASS=false
  fi
else
  echo "docs/traceability.yaml NOT FOUND" >> "$GATE_REPORT"
  GATE_PASS=false
fi

# Clean worktree check. `^worktree ` is anchored to the porcelain record
# line (a branch named feat/worktree-x is not a worktree); only the ACTIVE
# build/stream-<x> branches count — an abandoned-<sha> rename from an
# earlier run is a NOTE.
echo "--- worktree check ---" >> "$GATE_REPORT"
WORKTREES=$(git worktree list --porcelain | grep -c '^worktree ' || true)
if [ "$WORKTREES" -gt 1 ]; then
  echo "LEFTOVER WORKTREES" >> "$GATE_REPORT"
  git worktree list >> "$GATE_REPORT"
  GATE_PASS=false
fi
BUILD_BRANCHES=$(git branch --list 'build/stream-?' | sed 's/^[* ]*//' || true)
if [ -n "$BUILD_BRANCHES" ]; then
  echo "LEFTOVER BUILD BRANCHES" >> "$GATE_REPORT"
  printf '%s\n' "$BUILD_BRANCHES" >> "$GATE_REPORT"
  GATE_PASS=false
fi
ABANDONED=$(git branch --list 'build/*-abandoned-*' | sed 's/^[* ]*//' || true)
[ -z "$ABANDONED" ] || printf '%s\n' "$ABANDONED" | sed 's/^/NOTE: abandoned stream branch from an earlier run (not deleted): /' >> "$GATE_REPORT"

finish_gate
