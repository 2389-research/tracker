set -eu
[ -n "${graph.workflow_dir}" ] || { echo "ERROR: graph.workflow_dir is empty — cannot locate build_product_with_superspec's scripts/build_product_with_superspec/lib/ (embedded built-in: engine failed to materialize .tracker/workflow/; packed .dipx: unsupported, see #430)"; exit 1; }
LIB="${graph.workflow_dir}/scripts/build_product_with_superspec/lib"
. "$LIB/gates.sh"
start_gate phase4
gate_verify
finish_gate
