# ABOUTME: Traceability-matrix helpers for build_product_with_superspec (#646 item 5b/5f).
# ABOUTME: Sourced (never executed) via ${graph.workflow_dir}/scripts/build_product_with_superspec/lib.
#
# FORMAT CONTRACT — docs/traceability.yaml is FLAT: one requirement per line,
# a flow-style YAML mapping keyed by the requirement ID, e.g.
#
#   FR-1: {status: pending, impl_ref: null, test_ref: null, note: null}
#   QG-3: {status: done, impl_ref: "internal/eval/cov.go:Check", test_ref: "internal/eval/cov_test.go:TestCheck", note: "85% overall"}
#
# Comment (`#`) and blank lines are allowed anywhere. Nothing else is: the
# flat shape is what makes the per-stream OVERLAYS mergeable mechanically
# and the FinalGates counts exact. Parallel streams never edit the master —
# each writes docs/traceability.<stream>.yaml holding ONLY the lines for the
# IDs it covers, and merge_traceability_overlays folds them into the master
# after the phase merge (no add/add conflicts on one shared file).
TRACE_MASTER="docs/traceability.yaml"
TRACE_ID_RE='^[A-Z]+-[0-9]+:'

# trace_lint FILE — 0 when FILE has at least one requirement line and every
# non-blank, non-comment line is one; otherwise prints the offending lines
# and returns 1.
trace_lint() {
  [ -f "$1" ] || { echo "ERROR: $1 not found"; return 1; }
  if ! grep -qE "$TRACE_ID_RE" "$1"; then
    echo "ERROR: $1 has no requirement lines — the flat format is one line per requirement: 'FR-1: {status: pending, impl_ref: null, test_ref: null, note: null}' (a nested/block YAML matrix is not accepted)"
    return 1
  fi
  BAD=$(grep -vE "$TRACE_ID_RE|^[[:space:]]*(#|$)" "$1" || true)
  if [ -n "$BAD" ]; then
    echo "ERROR: $1 has lines that are neither a requirement line ('ID: {...}'), a comment, nor blank:"
    printf '%s\n' "$BAD" | sed 's/^/  /'
    return 1
  fi
  return 0
}

# trace_count PATTERN [FILE] — the number of requirement lines of FILE
# (default: the master) containing PATTERN. Always prints a number: the old
# `grep -c … || echo 0` printed "0\n0" (grep -c prints 0 itself and exits 1).
trace_count() {
  grep -E "$TRACE_ID_RE" "${2:-$TRACE_MASTER}" 2>/dev/null | grep -c -- "$1" || true
}

# trace_ids_matching PATTERN [FILE] — the IDs of the requirement lines
# containing PATTERN, one per line.
trace_ids_matching() {
  grep -E "$TRACE_ID_RE" "${2:-$TRACE_MASTER}" 2>/dev/null | grep -- "$1" | sed -E 's/^([A-Z]+-[0-9]+):.*/\1/' || true
}

# trace_merge_overlay MASTER OVERLAY — replace, in MASTER, every requirement
# line whose ID the OVERLAY also carries (the overlay's line wins); an
# overlay ID absent from the master is APPENDED with a WARNING on stdout (a
# stream traced a requirement the plan never scaffolded — surfaced in the
# gate prompt, not lost).
trace_merge_overlay() {
  _tmp=$(mktemp)
  _master_before=$(mktemp)
  cp "$1" "$_master_before"
  awk -v ov="$2" '
    BEGIN {
      n = 0
      while ((getline l < ov) > 0) {
        if (match(l, /^[A-Z]+-[0-9]+:/)) {
          id = substr(l, 1, RLENGTH - 1)
          if (!(id in repl)) order[++n] = id
          repl[id] = l
        }
      }
      close(ov)
    }
    {
      if (match($0, /^[A-Z]+-[0-9]+:/)) {
        id = substr($0, 1, RLENGTH - 1)
        if (id in repl) { print repl[id]; done[id] = 1; next }
      }
      print
    }
    END {
      for (i = 1; i <= n; i++) {
        id = order[i]
        if (!(id in done)) print repl[id]
      }
    }
  ' "$1" > "$_tmp" && cat "$_tmp" > "$1"
  rm -f "$_tmp"
  # The appended IDs, reported on STDOUT so the line reaches the gate prompt
  # (a /dev/stderr print inside awk was invisible there).
  for _id in $(trace_ids_matching '' "$2"); do
    grep -qE "^$_id:" "$_master_before" || echo "WARNING: overlay entry $_id is not in the master matrix — appended (the plan never scaffolded it)"
  done
  rm -f "$_master_before"
}

# merge_traceability_overlays PHASE — fold every committed
# docs/traceability.<stream>.yaml overlay into the master, remove the
# overlays from the tree, and commit the result (explicit identity, unsigned,
# hooks honoured). Prints one line per overlay. Nothing to merge → no commit.
# Two overlays in one phase that both set the same ID is a FAILURE (return
# 1, master untouched): "later filename wins" would silently drop one
# stream's refs — the human resolves it at the MergeConflict gate.
merge_traceability_overlays() {
  _phase=$1
  [ -f "$TRACE_MASTER" ] || { echo "ERROR: $TRACE_MASTER missing — the scaffold was not committed (CommitScaffold)"; return 1; }
  _found=""
  _seen_ids=""
  # Pass 1: validate every overlay and detect clashes BEFORE touching the
  # master, so a failure leaves nothing half-merged.
  for _ov in docs/traceability.*.yaml; do
    [ -f "$_ov" ] || continue
    _found=1
    if ! trace_lint "$_ov"; then
      echo "ERROR: overlay $_ov is malformed — fix it (or delete it) and retry the merge"
      return 1
    fi
    for _id in $(trace_ids_matching '' "$_ov"); do
      case " $_seen_ids " in
        *" $_id "*)
          echo "ERROR: $_id is set by more than one overlay this phase ($_ov and an earlier one) — two streams traced the same requirement; keep one line, delete the other, commit, then retry the merge"
          return 1 ;;
      esac
      _seen_ids="$_seen_ids $_id"
    done
  done
  for _ov in docs/traceability.*.yaml; do
    [ -f "$_ov" ] || continue
    trace_merge_overlay "$TRACE_MASTER" "$_ov"
    echo "merged overlay $_ov into $TRACE_MASTER ($(trace_count '' "$_ov") entries)"
    git rm -q -- "$_ov"
  done
  [ -n "$_found" ] || { echo "no traceability overlays to merge (docs/traceability.<stream>.yaml)"; return 0; }
  git add -- "$TRACE_MASTER"
  if git diff --cached --quiet; then
    echo "traceability master unchanged"
    return 0
  fi
  git -c user.name="build_product_with_superspec" -c user.email="superspec@tracker.local" -c commit.gpgsign=false \
    commit -q -m "chore(traceability): merge phase $_phase stream overlays" || {
    echo "ERROR: could not commit the merged traceability matrix (a hook rejected it?) — see above"
    return 1
  }
}

# trace_waived ID — 0 when ID is listed in the test-ref waiver file.
# WAIVER CONTRACT (#646 5f): docs/traceability-waivers.txt, one requirement
# ID per line followed by whitespace and the reason, e.g.
#   NFR-3  observability is verified by the operator runbook, not a unit test
# A requirement with an impl_ref and `test_ref: null` FAILS FinalGates unless
# waived here; waivers are committed, listed in the gate report, and the
# TraceabilityAudit is told to challenge each one.
TRACE_WAIVERS="docs/traceability-waivers.txt"
trace_waived() {
  [ -f "$TRACE_WAIVERS" ] || return 1
  grep -qE "^[[:space:]]*$1([[:space:]]|$)" "$TRACE_WAIVERS"
}
