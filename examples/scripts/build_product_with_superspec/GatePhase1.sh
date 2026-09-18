set -eu
[ -n "${graph.workflow_dir}" ] || { echo "ERROR: graph.workflow_dir is empty — cannot locate build_product_with_superspec's scripts/build_product_with_superspec/lib/ (embedded built-in: engine failed to materialize .tracker/workflow/; packed .dipx: unsupported, see #430)"; exit 1; }
LIB="${graph.workflow_dir}/scripts/build_product_with_superspec/lib"
. "$LIB/gates.sh"
start_gate phase1
gate_verify           # QG-2/QG-3: build + tests + lint/vet, every stack
gate_coverage         # QG-3 summary (report-only)
gate_complexity       # QG-5 WARNING (never a failure)
finish_gate
