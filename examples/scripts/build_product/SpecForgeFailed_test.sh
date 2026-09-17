#!/usr/bin/env bash
# ABOUTME: Fixture tests for SpecForgeFailed.sh — the fail-closed terminal of
# ABOUTME: the spec-forge loop. A TOOL that always exits 1 (not a human gate)
# ABOUTME: so it halts in every mode; it must surface the residual findings.
set -uo pipefail
DIR="$(cd "$(dirname "$0")" && pwd)"
fail=0
check() { # name expected actual
  if [ "$2" = "$3" ]; then echo "ok: $1"; else echo "FAIL: $1 — want '$2' got '$3'"; fail=1; fi
}
WORK="$(mktemp -d)"
STATE="$(mktemp -d)"
trap 'rm -rf "$WORK" "$STATE"' EXIT
. "$DIR/test_helpers.sh"
SCRIPT="$(stage_script "$DIR/SpecForgeFailed.sh")"   # ${graph.workflow_dir} expanded as the engine does
run() { OUT="$( (cd "$WORK" && sh "$SCRIPT") 2>"$STATE/stderr")"; RC=$?; }
has() { printf '%s' "$OUT" | grep -qF -- "$1" && echo yes || echo no; }

# 1. Nothing written by the loop: still exit 1, with the placeholder lines.
run
check "always exit 1"              "1" "$RC"
check "headline"                   "yes" "$(has 'SPEC-FORGE FAILED')"
check "no findings placeholder"    "yes" "$(has '(none written)')"
check "no forge log placeholder"   "yes" "$(has '(no forge edits recorded)')"
check "operator guidance"          "yes" "$(has 'Fix SPEC.md by hand and re-run')"

# 2. Both artifacts present: their contents are echoed verbatim, exit still 1.
mkdir -p "$WORK/.ai/decisions"
printf 'FINDING: dangling ref to §4.2\n' > "$WORK/.ai/decisions/spec-quality.md"
printf 'attempt 1: added §4.2 stub\n' > "$WORK/.ai/decisions/spec-forge-log.md"
run
check "exit 1 with artifacts"      "1" "$RC"
check "findings echoed"            "yes" "$(has 'FINDING: dangling ref to §4.2')"
check "forge log echoed"           "yes" "$(has 'attempt 1: added §4.2 stub')"
check "placeholders gone"          "no" "$(has '(none written)')"

if [ "$fail" = 0 ]; then echo "ALL PASS"; else echo "SOME FAILED"; exit 1; fi
