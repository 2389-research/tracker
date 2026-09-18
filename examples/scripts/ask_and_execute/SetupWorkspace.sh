set -eu
# Shared shell helpers live beside this script's sidecar tree; the engine
# interpolates ${graph.workflow_dir} (author-controlled, safe-key allowlisted)
# before this body reaches `sh`. Fail loud if it is empty rather than let
# `. "/scripts/..."` abort under set -eu with a cryptic message.
[ -n "${graph.workflow_dir}" ] || { echo "ERROR: graph.workflow_dir is empty — cannot locate ask_and_execute's scripts/ask_and_execute/lib/ (embedded built-in: engine failed to materialize .tracker/workflow/; packed .dipx: unsupported, see #430)"; exit 1; }
LIB="${graph.workflow_dir}/scripts/ask_and_execute/lib"
# lib/gitignore.sh is a byte-identical copy of build_product's (pinned by
# lib/parity_test.sh): materialization only ships THIS workflow's scripts/
# tree, so the helpers cannot be sourced from build_product's directory.
. "$LIB/gitignore.sh"

mkdir -p .ai/worktrees .ai/candidates .ai/decisions
# #646: `.ai/` is run metadata, never product source. Append-if-absent — a
# bare `>>` glued `.ai/` onto a last line with no newline and `sort -u`
# reordered `!negations` and rewrote the file on every run (#640 C2/C3).
gitignore_append '.ai/'
# #351: keep tracker's own run metadata (.tracker/inputs, checkpoints, the
# materialized workflow copy) out of the product repo via the LOCAL
# info/exclude — ApplyWinner's merge commit and CommitFinal must never
# sweep it in. No-op outside a git repo.
exclude_tracker_metadata
# Fresh-run reset: SetupWorkspace is skipped on checkpoint resume, so a
# deliberately resumed run keeps its evidence — only a NEW run in a workdir a
# previous run left behind starts without stale candidate diffs/test logs.
rm -f .ai/candidates/*.diff .ai/candidates/*.test .ai/candidates/base-sha
printf 'workspace-ready'
