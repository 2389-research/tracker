set -eu
# Shared shell helpers live beside this script's sidecar tree; the engine
# interpolates ${graph.workflow_dir} (author-controlled, safe-key allowlisted)
# before this body reaches `sh`. Fail loud if it is empty rather than let
# `. "/scripts/..."` abort under set -eu with a cryptic message.
[ -n "${graph.workflow_dir}" ] || { echo "ERROR: graph.workflow_dir is empty — cannot locate build_product's scripts/build_product/lib/"; exit 1; }
LIB="${graph.workflow_dir}/scripts/build_product/lib"
. "$LIB/gitignore.sh"
. "$LIB/build-context.sh"

mkdir -p .ai/build .ai/decisions .ai/milestones
# `.ai/` + the #405 static build-output patterns → tracked .gitignore
# (appended, deduped, sorted; idempotent). See lib/gitignore.sh.
seed_gitignore
# #351: `.tracker/` → LOCAL .git/info/exclude + untrack a pre-#351 committed
# .tracker/ (index only). See lib/gitignore.sh.
exclude_tracker_metadata
# #318: clear any warm-continue cap counter / MaxTurns override left by a
# prior run interrupted before its cleanup. Setup is skipped on checkpoint
# resume (already completed), so a deliberately-resumed run keeps its state;
# only a fresh run starts the continue allowance clean.
rm -rf .tracker/turn_overrides
# #553: adopt a caller-supplied `spec` file input. The engine staged it to
# a fixed, deterministic path (derived from the input name, not the
# untrusted value), so reading that path here is safe — the untrusted
# input value itself never enters this command (it is not interpolated).
if [ -f .tracker/inputs/spec ]; then
  cp .tracker/inputs/spec SPEC.md
fi
if [ ! -f SPEC.md ]; then
  echo "ERROR: SPEC.md not found in repo root."
  echo "build_product builds from a SPEC.md describing what you want."
  echo "Get a starter one with:  tracker init build_product"
  echo "(creates build_product.dip + a SPEC.md you can edit), then re-run."
  exit 1
fi
wc -l SPEC.md | awk '{print $1" lines"}'

# Install the shared runtime files into .ai/build/ from the workflow's lib/
# sidecars. They are copied (not sourced) because later nodes run them from
# the workdir contract paths: TestMilestone / the Implement+FixMilestone
# breach verify_command run `sh .ai/build/verify.sh`, FinalBuild sources
# `.ai/build/ci-probe.sh`, and FinalSpecCheck + the reviewer prompts read
# `.ai/build/iface-reachability-rubric.md`.
#
# ci-probe.sh — the shared project-CI probe (issue #233 Gap 1; refactored
# in PR #246 round-5 per Copilot). TestMilestone and FinalBuild source this
# file so the probe/run logic lives in exactly one place — round-4 review
# found the awk had silently drifted between the two nodes; this prevents
# that class of bug.
cp "$LIB/ci-probe.sh" .ai/build/ci-probe.sh

# verify.sh — the shared milestone green-gate (issue #406). TestMilestone
# wraps this with the fix-attempt counter + tests-pass/escalate sentinels;
# the Implement/FixMilestone breach verify_command runs the SAME script, so a
# turn-limit breach on a green tree classifies verified_green and commits
# the work instead of abandoning it. One source of truth — the gate logic
# lives here, exactly as it did inline in TestMilestone (mirrors the
# ci-probe.sh single-place discipline above).
cp "$LIB/verify.sh" .ai/build/verify.sh

# iface-reachability-rubric.md — the shared interface-reachability rubric
# (issue #233 Gap 7). FinalSpecCheck and the three reviewer prompts source
# this file so the discipline lives in exactly one place — mirrors the
# ci-probe.sh pattern from PR #246 (Gap 1), preventing the drift that
# pre-#246 affected the awk between TestMilestone and FinalBuild.
cp "$LIB/iface-reachability-rubric.md" .ai/build/iface-reachability-rubric.md

# Seed the per-node build-context file (issue #298). Best-effort, can never
# fail Setup. See lib/build-context.sh.
seed_build_context

# #418: capture the run's base commit ONCE at Setup so the cross-review
# diff node can compute a cumulative base..worktree without a per-milestone
# marker (those are deleted at each MarkMilestoneDone). Same non-leaking
# idiom as PickNextMilestone's start marker: --verify --quiet prints
# nothing and exits non-zero on a commitless repo, so a fresh repo
# genuinely records an empty base. Goes to a FILE so the routing marker
# below stays last on stdout.
RUN_BASE=$(git rev-parse --verify --quiet HEAD 2>/dev/null || true)
printf '%s\n' "$RUN_BASE" > .ai/build/run-base-sha

# Spec-forge loop hygiene (fresh-run reset). Setup is skipped on
# checkpoint resume, so an intentional resume keeps loop state — but a NEW
# run in a workdir left dirty by a prior abandoned run must not inherit a
# poisoned budget counter or a stale original-spec snapshot (PR #264).
rm -f .ai/build/spec_forge_attempts
rm -f .ai/decisions/SPEC.original.md .ai/decisions/spec-forge-log.md

printf 'setup-ready'