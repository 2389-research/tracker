# ABOUTME: Gate-integrity helpers for build_product's TestMilestone / FinalBuild
# ABOUTME: (#640 D6): re-emit the agent-writable gate scripts from the workflow
# ABOUTME: sidecar before every run and diff the known_* hatch files against the
# ABOUTME: milestone-start snapshot. Sourced via ${graph.workflow_dir}/.../lib.
#
# Everything under .ai/build/ and .ai/milestones/ is writable by the
# Implement/Fix agents and gitignored, so it is invisible to VerifyMilestone's
# scope check and to reviewers. `echo 'exit 0' > .ai/build/verify.sh` used to
# yield `tests-pass` with a clean `git status`.

# restore_gate_files LIB — copy LIB/verify.sh and LIB/ci-probe.sh over
# .ai/build/ (byte-identical to what Setup installed). A pre-existing copy
# that differs is reported by name: that WARNING is the self-ratification
# signal VerifyMilestone should treat as a finding.
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

# snapshot_hatch_files — on the FIRST TestMilestone run of a milestone
# (no snapshot yet) snapshot .ai/milestones/known_failures and
# known_lint_failures to <file>.snapshot (an empty snapshot when the file
# is absent); on EVERY run print the entries added since the snapshot.
# CONTRACT: MarkMilestoneDone removes the snapshots at milestone end, so the
# next milestone re-baselines. Paths are exactly
#   .ai/milestones/known_failures.snapshot
#   .ai/milestones/known_lint_failures.snapshot
snapshot_hatch_files() {
  mkdir -p .ai/milestones
  for f in known_failures known_lint_failures; do
    cur=".ai/milestones/$f"
    snap="$cur.snapshot"
    if [ ! -f "$snap" ]; then
      if [ -f "$cur" ]; then cp "$cur" "$snap"; else : > "$snap"; fi
    fi
    [ -f "$cur" ] || continue
    added=$(grep -v '^[[:space:]]*#' "$cur" | grep -v '^[[:space:]]*$' | grep -vxFf "$snap" || true)
    if [ -n "$added" ]; then
      echo "--- $f: entries ADDED since milestone start (#640 D6 — VerifyMilestone: a finding unless justified) ---"
      printf '%s\n' "$added" | sed 's/^/  + /'
    fi
  done
}
