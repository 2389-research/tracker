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
# Commit that .gitignore change (by name) when it is the only change to the
# file: candidate worktrees fork from HEAD, so an uncommitted main-tree edit
# to .gitignore would turn a candidate's own .gitignore edit into a fake
# merge conflict at ApplyWinner. A .gitignore that is dirty for OTHER
# reasons is left to the operator (reported, not committed).
if git rev-parse --verify --quiet HEAD >/dev/null 2>&1 && [ -n "$(git status --porcelain -- .gitignore 2>/dev/null)" ]; then
  if [ "$(git diff HEAD -- .gitignore 2>/dev/null | grep -c '^[+-][^+-]')" -le 2 ] && git diff HEAD -- .gitignore 2>/dev/null | grep -q '^+\.ai/$'; then
    git add -- .gitignore
    git -c user.name="ask_and_execute" -c user.email="ask_and_execute@tracker.local" -c commit.gpgsign=false \
      commit -q -m "chore(ask_and_execute): ignore .ai/ run metadata" -- .gitignore \
      && echo "committed .gitignore (.ai/ ignore rule)" \
      || echo "WARNING: could not commit the .gitignore change (hook?) — a candidate that edits .gitignore may conflict at ApplyWinner"
  else
    echo "NOTE: .gitignore has other uncommitted changes — left as is; a candidate that edits .gitignore may conflict at ApplyWinner"
  fi
fi
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
