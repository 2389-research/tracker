set -eu
# Shared shell helpers live beside this script's sidecar tree; the engine
# interpolates ${graph.workflow_dir} (author-controlled, safe-key allowlisted)
# before this body reaches `sh`. Fail loud if it is empty rather than let
# `. "/scripts/..."` abort under set -eu with a cryptic message.
[ -n "${graph.workflow_dir}" ] || { echo "ERROR: graph.workflow_dir is empty — cannot locate build_product_with_superspec's scripts/build_product_with_superspec/lib/ (embedded built-in: engine failed to materialize .tracker/workflow/; packed .dipx: unsupported, see #430)"; exit 1; }
LIB="${graph.workflow_dir}/scripts/build_product_with_superspec/lib"
# gitignore.sh / verify.sh / ci-probe.sh are byte-identical copies of
# build_product's (pinned by lib/parity_test.sh): materialization ships only
# THIS workflow's scripts/ tree, so they cannot be sourced across workflows.
. "$LIB/gitignore.sh"

# Streams build in git worktrees and the scaffold is committed — no repo, no
# run. (A commitless repo is fine: CommitScaffold makes the first commit.)
git rev-parse --is-inside-work-tree >/dev/null 2>&1 || {
  echo "ERROR: not a git repository — build_product_with_superspec builds each stream in a git worktree. Run 'git init' first."
  exit 1
}

mkdir -p .ai/streams .ai/decisions .ai/worktrees .ai/gates .ai/build
# `.ai/` + the #405 static build-output patterns → tracked .gitignore
# (append-if-absent, order preserved, never sorted — #640 C2/C3/C4).
seed_gitignore
# #646 5d / #351: keep tracker's run metadata (.tracker/inputs/spec, checkpoints,
# the materialized workflow copy) out of the product repo via the LOCAL
# info/exclude — the phase merges and FinalCommit must never sweep it in.
exclude_tracker_metadata

# Install the shared green-gate into .ai/build/ (the gate scripts run
# `sh .ai/build/verify.sh`, which sources .ai/build/ci-probe.sh) — the same
# runtime contract build_product uses (#406 one source of truth).
cp "$LIB/ci-probe.sh" .ai/build/ci-probe.sh
cp "$LIB/verify.sh" .ai/build/verify.sh

# Fresh-run reset: Setup is skipped on checkpoint resume, so a deliberately
# resumed run keeps its state; a NEW run must not inherit a previous run's
# gate reports, phase base or merge-phase marker.
rm -f .ai/build/milestone-start-sha .ai/build/merge-phase .ai/build/ci-make-missing
rm -rf .ai/gates/*

# #553: adopt a caller-supplied `spec` file input staged by the engine to a
# fixed, deterministic path (the untrusted input value is never interpolated
# into this command; it reads the staged path directly).
if [ -f .tracker/inputs/spec ]; then
  cp .tracker/inputs/spec SPEC.md
fi
if [ ! -f SPEC.md ]; then
  echo "ERROR: SPEC.md not found in repo root."
  echo "This workflow builds from a SPEC.md describing what you want."
  echo "Get a starter one with:  tracker init build_product_with_superspec"
  echo "(creates the .dip + a SPEC.md you can edit), then re-run."
  exit 1
fi
printf 'setup-ready'
