set -eu
# Shared helpers via the engine-interpolated ${graph.workflow_dir} (author-
# controlled, safe-key allowlisted). Fail loud if empty.
[ -n "${graph.workflow_dir}" ] || { echo "ERROR: graph.workflow_dir is empty — cannot locate build_product's scripts/build_product/lib/ (embedded built-in: engine failed to materialize .tracker/workflow/; packed .dipx: unsupported, see #430)"; exit 1; }
LIB="${graph.workflow_dir}/scripts/build_product/lib"
. "$LIB/gate-integrity.sh"

# #640 D6: re-emit the agent-writable gate scripts from the sidecar before
# the ship gate runs (WARNING names a file that differed) and report hatch
# entries / operator stamps added since the last milestone start.
restore_gate_files "$LIB"
report_hatch_additions

# The ship gate is the SAME verify.sh the milestone gate runs, in --final
# mode (#406 one source of truth; #640 D1/D7/D12/D13): every detected
# stack anywhere in the tree, each in its own directory; whole-tree Go
# tests with `-count=1` (no test cache); known_failures IGNORED — still-listed
# entries are printed as the reason; a Go stack with zero test files is a
# failure (a product with zero tests must not ship green); elapsed seconds
# per stack so an operator can size the node timeout; then the project CI
# gate (Makefile target AND language-native gates). FinalBuild has no fix
# loop, so any non-zero exit — including the make-missing environment case
# — is a node-level failure the engine routes through the strict-failure
# edge; the marker is printed only on full green.
sh .ai/build/verify.sh --final 2>&1

printf 'final-build-pass'
