set -eu
[ -n "${graph.workflow_dir}" ] || { echo "ERROR: graph.workflow_dir is empty — cannot locate build_product_with_superspec's scripts/build_product_with_superspec/lib/ (embedded built-in: engine failed to materialize .tracker/workflow/; packed .dipx: unsupported, see #430)"; exit 1; }
LIB="${graph.workflow_dir}/scripts/build_product_with_superspec/lib"
. "$LIB/traceability.sh"
. "$LIB/worktrees.sh"
# #646 5b/5c — see lib/worktrees.sh: merge each stream, tear down only once all
# merged, fold the streams' traceability overlays into the master; a
# conflict aborts the merge, keeps everything, and fails the node → the
# MergeConflict gate (never accept).
merge_streams 2 stream-c stream-e
printf 'phase2-merged'
