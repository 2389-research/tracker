#!/usr/bin/env bash
# ABOUTME: Fixture tests for CheckSpecForgeBudget.sh — gate-BEFORE-work budget
# ABOUTME: for the spec-forge loop (#443 shape: `-gt 3` = exactly 3 ForgeSpec
# ABOUTME: attempts), numeric guard on a corrupted counter, and the idempotent
# ABOUTME: SPEC.original.md snapshot taken on first entry only.
set -uo pipefail
DIR="$(cd "$(dirname "$0")" && pwd)"
SCRIPT="$DIR/CheckSpecForgeBudget.sh"
fail=0
check() { # name expected actual
  if [ "$2" = "$3" ]; then echo "ok: $1"; else echo "FAIL: $1 — want '$2' got '$3'"; fail=1; fi
}
WORK="$(mktemp -d)"
STATE="$(mktemp -d)"
trap 'rm -rf "$WORK" "$STATE"' EXIT
run() { OUT="$( (cd "$WORK" && sh "$SCRIPT") 2>"$STATE/stderr")"; RC=$?; }
last() { printf '%s' "$OUT" | tail -1; }
COUNTER="$WORK/.ai/build/spec_forge_attempts"
ORIG="$WORK/.ai/decisions/SPEC.original.md"

# 1. First entry: scaffolds dirs, snapshots the ORIGINAL spec, attempt 1 of 3.
printf 'v1 spec\n' > "$WORK/SPEC.md"
run
check "attempt 1 exit 0"            "0" "$RC"
check "attempt 1 message"           "spec-forge budget OK: attempt 1 of 3" "$(last)"
check "counter = 1"                 "1" "$(cat "$COUNTER")"
check "original snapshot taken"     "v1 spec" "$(cat "$ORIG")"

# 2. Snapshot is idempotent: ForgeSpec edits SPEC.md, the gate re-runs, the
#    snapshot still holds the ORIGINAL (the fidelity oracle diffs against it).
printf 'v2 spec (forged)\n' > "$WORK/SPEC.md"
run
check "attempt 2 exit 0"            "0" "$RC"
check "attempt 2 message"           "spec-forge budget OK: attempt 2 of 3" "$(last)"
check "snapshot unchanged"          "v1 spec" "$(cat "$ORIG")"

# 3. Exactly 3 attempts pass (-gt, not -ge); the 4th is refused.
run
check "attempt 3 exit 0"            "0" "$RC"
check "attempt 3 message"           "spec-forge budget OK: attempt 3 of 3" "$(last)"
run
check "attempt 4 exit 1"            "1" "$RC"
check "exhausted message"           "spec-forge budget exhausted: 4 attempts (max 3) — spec could not be hardened autonomously" "$(last)"
check "counter still incremented"   "4" "$(cat "$COUNTER")"

# 4. Numeric guard: a corrupted counter is treated as 0 (attempt 1), not a
#    set -eu arithmetic abort. Both a non-numeric word and an embedded space
#    (which also breaks bash-as-sh) are covered.
for junk in 'garbage' '1 2' ''; do
  printf '%s\n' "$junk" > "$COUNTER"
  run
  check "junk '$junk' -> attempt 1"  "spec-forge budget OK: attempt 1 of 3" "$(last)"
  check "junk '$junk' -> counter 1"  "1" "$(cat "$COUNTER")"
done

# 5. Setup's fresh-run hygiene (#264) removes the counter + snapshot; from
#    that state the gate starts over at attempt 1 with a NEW snapshot.
rm -f "$COUNTER" "$ORIG"
printf 'v3 spec\n' > "$WORK/SPEC.md"
run
check "after reset attempt 1"       "spec-forge budget OK: attempt 1 of 3" "$(last)"
check "after reset new snapshot"    "v3 spec" "$(cat "$ORIG")"

# 6. No SPEC.md and no snapshot: the cp fails loudly under set -eu.
rm -rf "$WORK/.ai" "$WORK/SPEC.md"
run
check "missing SPEC.md exit 1"      "1" "$RC"

if [ "$fail" = 0 ]; then echo "ALL PASS"; else echo "SOME FAILED"; exit 1; fi
