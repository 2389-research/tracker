#!/usr/bin/env bash
# ABOUTME: Fixture tests for ShowPlan.sh — cats the six plan artifacts into
# ABOUTME: ctx.tool_stdout for the ApprovePlan gate. Every section header
# ABOUTME: must always render; a missing file yields a placeholder, never a
# ABOUTME: strict-failure exit that would knock the run out at the gate.
set -uo pipefail
DIR="$(cd "$(dirname "$0")" && pwd)"
SCRIPT="$DIR/ShowPlan.sh"
fail=0
check() { # name expected actual
  if [ "$2" = "$3" ]; then echo "ok: $1"; else echo "FAIL: $1 — want '$2' got '$3'"; fail=1; fi
}
WORK="$(mktemp -d)"
STATE="$(mktemp -d)"
trap 'rm -rf "$WORK" "$STATE"' EXIT
run() { OUT="$( (cd "$WORK" && sh "$SCRIPT") 2>"$STATE/stderr")"; RC=$?; }
has() { printf '%s' "$OUT" | grep -qF -- "$1" && echo yes || echo no; }

# 1. Empty workdir: exit 0, all six headers, all placeholders.
run
check "exit 0 with nothing"         "0" "$RC"
for h in "# Milestone plan" "# Requirement coverage" "# Spec ambiguity rulings" \
         "# Behavioral contracts" "# Spec coherence findings" "# Spec auto-hardening log"; do
  check "header: $h" "yes" "$(has "$h")"
done
check "plan placeholder"            "yes" "$(has 'milestones.md not found — Decompose did not write a plan')"
check "coverage placeholder"        "yes" "$(has 'requirement-coverage.md not found')"
check "ambiguities placeholder"     "yes" "$(has 'spec-ambiguities.md not found')"
check "contracts placeholder"       "yes" "$(has 'behavioral-contracts.md not found')"
check "quality placeholder"         "yes" "$(has 'spec-quality.md not found')"
check "no-hardening note"           "yes" "$(has 'no auto-hardening — SPEC.md passed coherence as written')"

# 2. All artifacts present: contents verbatim, placeholders gone, forge NOTE shown.
mkdir -p "$WORK/.ai/decisions"
# shellcheck disable=SC2016 # the backticks are literal markdown, not a command substitution
printf '## Milestone 1: Core\n**Files**: `a.go`\n' > "$WORK/.ai/decisions/milestones.md"
printf '| req | owner |\nCOVERAGE_GAPS: 0\n' > "$WORK/.ai/decisions/requirement-coverage.md"
printf 'RULING-1: retries are 3\n' > "$WORK/.ai/decisions/spec-ambiguities.md"
printf 'CONTRACT-1: p99 < 50ms\n' > "$WORK/.ai/decisions/behavioral-contracts.md"
printf 'WARN(d): vague timeout\n' > "$WORK/.ai/decisions/spec-quality.md"
printf 'forge attempt 1: pinned timeout to 30s\n' > "$WORK/.ai/decisions/spec-forge-log.md"
run
check "exit 0 with artifacts"       "0" "$RC"
check "plan content"                "yes" "$(has '## Milestone 1: Core')"
check "coverage content"            "yes" "$(has 'COVERAGE_GAPS: 0')"
check "ruling content"              "yes" "$(has 'RULING-1: retries are 3')"
check "contract content"            "yes" "$(has 'CONTRACT-1: p99 < 50ms')"
check "quality content + advisory"  "yes" "$(has 'did not block the build')"
check "forge log content"           "yes" "$(has 'forge attempt 1: pinned timeout to 30s')"
check "forge NOTE"                  "yes" "$(has 'SPEC.md was auto-edited by the spec-forge loop')"
check "plan placeholder gone"       "no"  "$(has 'Decompose did not write a plan')"
check "no-hardening note gone"      "no"  "$(has 'no auto-hardening')"
# Section order is what the gate renders: plan first, forge log last.
check "plan before forge log" "yes" "$(printf '%s' "$OUT" | grep -n '^# ' | awk -F: 'NR==1{a=$1} END{if(a<$1) print "yes"; else print "no"}')"

if [ "$fail" = 0 ]; then echo "ALL PASS"; else echo "SOME FAILED"; exit 1; fi
