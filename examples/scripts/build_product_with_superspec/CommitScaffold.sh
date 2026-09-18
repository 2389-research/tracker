set -eu
[ -n "${graph.workflow_dir}" ] || { echo "ERROR: graph.workflow_dir is empty — cannot locate build_product_with_superspec's scripts/build_product_with_superspec/lib/ (embedded built-in: engine failed to materialize .tracker/workflow/; packed .dipx: unsupported, see #430)"; exit 1; }
LIB="${graph.workflow_dir}/scripts/build_product_with_superspec/lib"
. "$LIB/traceability.sh"

# #646 5a: the streams run in worktrees forked at HEAD, so anything only in
# the working tree — SPEC.md (input-supplied), the execution plan, the
# traceability scaffold — was invisible to them, and the first phase merge
# died on "untracked working tree files would be overwritten". Commit the
# scaffold by NAME (never `git add -A`, which would sweep in whatever else
# the workdir holds) so every worktree carries it.
for f in SPEC.md docs/execution-plan.md docs/traceability.yaml; do
  [ -f "$f" ] || { echo "ERROR: $f is missing — BuildPlan must write docs/execution-plan.md and docs/traceability.yaml (committed paths, not .ai/decisions/)"; exit 1; }
done
trace_lint docs/traceability.yaml || { echo "ERROR: docs/traceability.yaml does not follow the flat one-line-per-requirement format BuildPlan was asked for"; exit 1; }
# Explicit pathspec on the commit itself (not just the add): anything an
# operator had pre-staged in the index is left staged, never swept in.
set -- SPEC.md docs/execution-plan.md docs/traceability.yaml
[ ! -f .gitignore ] || set -- "$@" .gitignore
[ ! -f docs/traceability-waivers.txt ] || set -- "$@" docs/traceability-waivers.txt
git add -- "$@"
if git diff --cached --quiet -- "$@"; then
  echo "scaffold already committed at $(git rev-parse --short HEAD)"
else
  git -c user.name="build_product_with_superspec" -c user.email="superspec@tracker.local" -c commit.gpgsign=false \
    commit -q -m "chore(superspec): commit spec, execution plan and traceability scaffold" -- "$@"
  echo "committed scaffold: $(git rev-parse --short HEAD)"
fi
printf 'scaffold-committed'
