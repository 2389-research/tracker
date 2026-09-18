#!/usr/bin/env bash
# ABOUTME: Fixture tests for FinalGates.sh (#646 5f) — the ship gate runs
# ABOUTME: verify.sh --final, then QG-1 traceability with exact counts (no
# ABOUTME: "0\n0"): pending / missing impl_ref FAIL, an implemented requirement
# ABOUTME: with `test_ref: null` FAILS unless waived in
# ABOUTME: docs/traceability-waivers.txt (waivers listed), leftover overlays FAIL,
# ABOUTME: the worktree count is anchored, only ACTIVE build/stream-? branches
# ABOUTME: fail (abandoned ones are a NOTE), and the marker is exact and last.
set -uo pipefail
DIR="$(cd "$(dirname "$0")" && pwd)"
fail=0
check() { # name expected actual
  if [ "$2" = "$3" ]; then echo "ok: $1"; else echo "FAIL: $1 — want '$2' got '$3'"; fail=1; fi
}
WORK="$(mktemp -d)"
STATE="$(mktemp -d)"
trap 'rm -rf "$WORK" "$STATE"' EXIT
. "$DIR/../build_product/test_helpers.sh"
install_tool_shims
SCRIPT="$(stage_script "$DIR/FinalGates.sh")"
run() { OUT="$( (cd "$WORK" && PATH="$STATE/bin:$PATH" ${TEST_SH:-sh} "$SCRIPT") 2>"$STATE/stderr")"; RC=$?; }
last() { printf '%s' "$OUT" | tail -1; }
report() { grep -qF -- "$1" "$WORK/.ai/gates/final.txt" && echo yes || echo no; }
rline() { grep -F -- "$1" "$WORK/.ai/gates/final.txt" | head -1; }
G() { git -C "$WORK" -c user.name=t -c user.email=t@t "$@"; }
M="$WORK/docs/traceability.yaml"
mkdir -p "$WORK/.ai/build" "$WORK/docs"
cp "$DIR/lib/verify.sh" "$WORK/.ai/build/verify.sh"; cp "$DIR/lib/ci-probe.sh" "$WORK/.ai/build/ci-probe.sh"
G -c init.defaultBranch=main init -q
echo 'module x' > "$WORK/go.mod"; mkdir -p "$WORK/pkg"; echo 'package pkg' > "$WORK/pkg/a.go"; echo 'package pkg' > "$WORK/pkg/a_test.go"
G add -A; G commit -q -m base

# 1. No traceability file → FAIL.
run
check "no matrix: exit 1"               "1" "$RC"
check "no matrix: marker"               "final-gates-FAIL" "$(last)"
check "no matrix: report line"          "yes" "$(report 'docs/traceability.yaml NOT FOUND')"
check "no matrix: --final verify ran"   "yes" "$(calls | grep -q 'go test -count=1' && echo yes || echo no)"

# 2. Fully traced → PASS; counts are single numbers.
printf 'FR-1: {status: done, impl_ref: "pkg/a.go:A", test_ref: "pkg/a_test.go:TestA", note: "ok"}\nQG-1: {status: done, impl_ref: "docs/x.md", test_ref: "pkg/a_test.go:TestQ", note: "ok"}\n' > "$M"
run
check "traced: exit 0"                  "0" "$RC"
check "traced: marker last"             "final-gates-PASS" "$(last)"
check "traced: counts line"             "Requirements: 2  pending: 0  missing impl_ref: 0  missing test_ref: 0" "$(rline 'Requirements:')"

# 3. Pending / missing impl_ref → FAIL listing the IDs.
printf 'FR-1: {status: done, impl_ref: "pkg/a.go:A", test_ref: "pkg/a_test.go:TestA", note: "ok"}\nFR-2: {status: pending, impl_ref: null, test_ref: null, note: null}\n' > "$M"
run
check "pending: exit 1"                 "1" "$RC"
check "pending: marker"                 "final-gates-FAIL" "$(last)"
check "pending: counts"                 "Requirements: 2  pending: 1  missing impl_ref: 1  missing test_ref: 1" "$(rline 'Requirements:')"
check "pending: ID listed"              "yes" "$(report '  FR-2')"
check "pending: INCOMPLETE line"        "yes" "$(report 'TRACEABILITY INCOMPLETE: pending or unimplemented')"

# 4. NULL_TEST is GATED: implemented, no test_ref, no waiver → FAIL naming it.
printf 'FR-1: {status: done, impl_ref: "pkg/a.go:A", test_ref: "pkg/a_test.go:TestA", note: "ok"}\nNFR-3: {status: done, impl_ref: "pkg/a.go:Obs", test_ref: null, note: "runbook"}\n' > "$M"
run
check "untested: exit 1"                "1" "$RC"
check "untested: marker"                "final-gates-FAIL" "$(last)"
check "untested: named"                 "yes" "$(report 'UNTESTED requirement (impl without test_ref, no waiver): NFR-3')"
check "untested: INCOMPLETE line"       "yes" "$(report '1 requirement(s) implemented without a test_ref')"

# 5. …and waived (docs/traceability-waivers.txt) → PASS, waiver listed with
#    its reason; a prefix ID (NFR-30) does not inherit NFR-3's waiver.
printf '# waivers\nNFR-3  observability is verified by the operator runbook\n' > "$WORK/docs/traceability-waivers.txt"
run
check "waived: exit 0"                  "0" "$RC"
check "waived: marker PASS"             "final-gates-PASS" "$(last)"
check "waived: listed with reason"      "WAIVED test_ref for NFR-3: observability is verified by the operator runbook" "$(rline 'WAIVED')"
printf 'NFR-30: {status: done, impl_ref: "pkg/a.go:Obs", test_ref: null, note: null}\n' >> "$M"
run
check "prefix not waived: exit 1"       "1" "$RC"
check "prefix not waived: NFR-30 named" "yes" "$(report 'no waiver): NFR-30')"
sed -i.bak '/^NFR-30/d' "$M"; rm -f "$M.bak"

# 6. A leftover stream overlay (a phase merge did not fold it) → FAIL.
echo 'FR-9: {status: done, impl_ref: "x", test_ref: "y", note: null}' > "$WORK/docs/traceability.stream-c.yaml"
run
check "overlay leftover: exit 1"        "1" "$RC"
check "overlay leftover: named"         "yes" "$(report 'UNMERGED STREAM OVERLAYS')"
rm -f "$WORK/docs/traceability.stream-c.yaml"

# 7. Worktree/branch checks: a branch NAMED …worktree… is not a worktree; an
#    active build/stream-x branch FAILS; an abandoned rename is a NOTE.
G branch feat/worktree-x
run
check "branch named worktree: PASS"     "final-gates-PASS" "$(last)"
G branch build/stream-c
run
check "active stream branch: exit 1"    "1" "$RC"
check "active stream branch: listed"    "yes" "$(report 'LEFTOVER BUILD BRANCHES')"
G branch -q -D build/stream-c
G branch build/stream-c-abandoned-abc1234
run
check "abandoned: PASS"                 "final-gates-PASS" "$(last)"
check "abandoned: NOTE"                 "yes" "$(report 'NOTE: abandoned stream branch from an earlier run (not deleted): build/stream-c-abandoned-abc1234')"
G worktree add -q "$WORK/.ai/worktrees/stream-c" -b build/stream-z HEAD
run
check "leftover worktree: exit 1"       "1" "$RC"
check "leftover worktree: listed"       "yes" "$(report 'LEFTOVER WORKTREES')"
G worktree remove --force "$WORK/.ai/worktrees/stream-c"; G branch -q -D build/stream-z

# 8. --final semantics from verify.sh: a Go stack with ZERO test files
#    cannot ship green (#640 D7).
rm -f "$WORK/pkg/a_test.go"; G add -A; G commit -q -m "drop tests"
printf '' > "$STATE/out-go-list-tests"
run
check "zero tests: exit 1"              "1" "$RC"
check "zero tests: D7 line"             "yes" "$(report 'no Go test files in ANY Go stack')"

[ "$fail" = 0 ] && echo "ALL PASS" || { echo "SOME FAILED"; exit 1; }
