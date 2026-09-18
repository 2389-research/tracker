#!/usr/bin/env bash
# ABOUTME: Fixture tests for adaptive-ralph-stream's CheckCompletion /
# ABOUTME: IncrementCounter / CheckBudget (#646 items 9, 10) — RALPH_COMPLETE
# ABOUTME: only counts as the LAST non-blank log line, and a corrupted counter
# ABOUTME: reads as 0 instead of a dash `Illegal number` abort.
set -uo pipefail
DIR="$(cd "$(dirname "$0")" && pwd)"
fail=0
check() { # name expected actual
  if [ "$2" = "$3" ]; then echo "ok: $1"; else echo "FAIL: $1 — want '$2' got '$3'"; fail=1; fi
}
WORK="$(mktemp -d)"
STATE="$(mktemp -d)"
trap 'rm -rf "$WORK" "$STATE"' EXIT
# Simulate the subgraph bind: ${params.stream_id} is textually injected.
stage() { sed 's/\${params.stream_id}/s1/g' "$DIR/$1" > "$STATE/$1"; printf '%s' "$STATE/$1"; }
run() { OUT="$( (cd "$WORK" && ${TEST_SH:-sh} "$(stage "$1")") 2>"$STATE/stderr")"; RC=$?; }
SD="$WORK/.ai/streams/s1"
mkdir -p "$SD"

printf '# Log\nWrite "RALPH_COMPLETE" when done.\n- did stuff\n' > "$SD/iteration-log.md"
run CheckCompletion.sh
check "quoted mid-log -> continue" "continue" "$OUT"
printf 'RALPH_COMPLETE\n\n' >> "$SD/iteration-log.md"
run CheckCompletion.sh
check "last line -> complete"      "complete" "$OUT"

run IncrementCounter.sh
check "increment from absent"      "1" "$OUT"
printf 'abc' > "$SD/iteration-count.txt"; run IncrementCounter.sh
check "garbage counter exit 0"     "0" "$RC"
check "garbage counter -> 1"       "1" "$OUT"

printf '8' > "$SD/iteration-count.txt"; printf '8' > "$SD/max-iterations.txt"; run CheckBudget.sh
check "at budget"                  "budget_exhausted" "$OUT"
printf 'x y' > "$SD/iteration-count.txt"; run CheckBudget.sh
check "garbage count exit 0"       "0" "$RC"
check "garbage count -> ok"        "budget_ok" "$OUT"

if [ "$fail" = 0 ]; then echo "ALL PASS"; else echo "SOME FAILED"; exit 1; fi
