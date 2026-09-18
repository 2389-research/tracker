# ABOUTME: Gate-integrity helpers for build_product (#640 D6): re-emit the
# ABOUTME: agent-writable gate scripts from the workflow sidecar before every
# ABOUTME: gate run, snapshot the operator hatches at milestone start, and diff
# ABOUTME: them at every gate run. Sourced via ${graph.workflow_dir}/.../lib.
#
# Everything under .ai/build/ and .ai/milestones/ is writable by the
# Implement/Fix agents and gitignored, so it is invisible to VerifyMilestone's
# scope check and to reviewers. `echo 'exit 0' > .ai/build/verify.sh` used to
# yield `tests-pass` with a clean `git status`; a `touch .ai/build/no-tests-ok`
# would silence the whole test gate for the rest of the run.
#
# SNAPSHOT CONTRACT (paths are load-bearing for MarkMilestoneDone, which
# removes them at milestone end, and for reset_plan_state, which wipes them):
#   .ai/milestones/known_failures.snapshot       — known_failures at start
#   .ai/milestones/known_lint_failures.snapshot  — known_lint_failures at start
#   .ai/milestones/opt-outs.snapshot             — operator stamps present at
#                                                  start (.ai/build/no-tests-ok)
# PickNextMilestone calls snapshot_hatch_files (create-if-missing) at
# MILESTONE START — before Implement runs — so an agent's own additions are
# never baselined. TestMilestone / FinalBuild call report_hatch_additions.

HATCH_FILES="known_failures known_lint_failures"
OPT_OUT_STAMPS=".ai/build/no-tests-ok"

# restore_gate_files LIB — copy LIB/verify.sh and LIB/ci-probe.sh over
# .ai/build/ (byte-identical to what Setup installed). A pre-existing copy
# that differs is reported by name: that WARNING is the self-ratification
# signal VerifyMilestone treats as a finding.
restore_gate_files() {
  mkdir -p .ai/build
  for f in verify.sh ci-probe.sh; do
    [ -f "$1/$f" ] || { echo "ERROR: $1/$f missing — workflow sidecar incomplete; cannot restore the gate"; exit 1; }
    if [ -e ".ai/build/$f" ] && ! cmp -s "$1/$f" ".ai/build/$f"; then
      echo "WARNING: .ai/build/$f differed from the workflow's lib/$f and was RESTORED — something in the workdir rewrote the gate script (#640 D6)"
    fi
    cp "$1/$f" ".ai/build/$f"
  done
}

# hatch_entries FILE — the non-blank, non-comment lines of FILE (nothing when
# absent), for snapshot/diff purposes.
hatch_entries() {
  [ -f "$1" ] || return 0
  grep -v '^[[:space:]]*#' "$1" | grep -v '^[[:space:]]*$' || true
}

# snapshot_hatch_files — create every snapshot that is missing (never
# overwrite one that exists: the milestone's baseline is taken once).
snapshot_hatch_files() {
  mkdir -p .ai/milestones
  for f in $HATCH_FILES; do
    snap=".ai/milestones/$f.snapshot"
    [ -f "$snap" ] || hatch_entries ".ai/milestones/$f" > "$snap"
  done
  snap=".ai/milestones/opt-outs.snapshot"
  if [ ! -f "$snap" ]; then
    : > "$snap"
    for st in $OPT_OUT_STAMPS; do
      if [ -e "$st" ]; then printf '%s\n' "$st" >> "$snap"; fi
    done
  fi
  return 0
}

# report_hatch_additions — print every hatch entry and operator stamp that
# exists now but was not in the milestone-start snapshot. A missing snapshot
# (PickNextMilestone did not run — a pre-#640 resume) is reported and every
# entry is listed, never silently baselined here.
report_hatch_additions() {
  for f in $HATCH_FILES; do
    cur=".ai/milestones/$f"
    snap="$cur.snapshot"
    [ -f "$cur" ] || continue
    if [ -f "$snap" ]; then
      added=$(hatch_entries "$cur" | grep -vxFf "$snap" || true)
    else
      echo "WARNING: no milestone-start snapshot for $f (PickNextMilestone did not run?) — listing every entry"
      added=$(hatch_entries "$cur")
    fi
    if [ -n "$added" ]; then
      echo "--- $f: entries ADDED since milestone start (#640 D6 — VerifyMilestone: a finding unless justified) ---"
      printf '%s\n' "$added" | sed 's/^/  + /'
    fi
  done
  snap=".ai/milestones/opt-outs.snapshot"
  for st in $OPT_OUT_STAMPS; do
    [ -e "$st" ] || continue
    if [ -f "$snap" ] && grep -qxF "$st" "$snap"; then continue; fi
    echo "--- operator stamp CREATED since milestone start (#640 D6 — VerifyMilestone: a finding unless an operator created it) ---"
    echo "  + $st"
  done
  return 0
}
