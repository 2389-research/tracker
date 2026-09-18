set -eu
[ -n "${graph.workflow_dir}" ] || { echo "ERROR: graph.workflow_dir is empty — cannot locate build_product_with_superspec's scripts/build_product_with_superspec/lib/ (embedded built-in: engine failed to materialize .tracker/workflow/; packed .dipx: unsupported, see #430)"; exit 1; }
LIB="${graph.workflow_dir}/scripts/build_product_with_superspec/lib"
. "$LIB/worktrees.sh"
# #646 5a/5e — see lib/worktrees.sh: requires the committed scaffold, never
# deletes an unmerged previous-run branch, records the phase base sha.
setup_stream_worktrees 2 stream-c stream-e
printf 'phase2-worktrees-ready'
