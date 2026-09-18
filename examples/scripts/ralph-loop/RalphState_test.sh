#!/usr/bin/env bash
# ABOUTME: Fixture tests for ralph-loop's CheckCompletion / IncrementCounter /
# ABOUTME: CheckBudget (#646 items 9, 10) — RALPH_COMPLETE only counts as the
# ABOUTME: LAST non-blank line of the log (a quoted instruction mid-log is not
# ABOUTME: completion), and a corrupted counter reads as 0 instead of a dash
# ABOUTME: `Illegal number` abort. fix-tracker-visibility ships the same
# ABOUTME: CheckCompletion/IncrementCounter (drift-guarded in Go).
set -uo pipefail
DIR="$(cd "$(dirname "$0")" && pwd)"
fail=0
check() { # name expected actual
  if [ "$2" = "$3" ]; then echo "ok: $1"; else echo "FAIL: $1 — want '$2' got '$3'"; fail=1; fi
}
WORK="$(mktemp -d)"
STATE="$(mktemp -d)"
trap 'rm -rf "$WORK" "$STATE"' EXIT
run() { OUT="$( (cd "$WORK" && ${TEST_SH:-sh} "$DIR/$1") 2>"$STATE/stderr")"; RC=$?; }
LOG="$WORK/.ai/ralph/iteration-log.md"
COUNTER="$WORK/.ai/ralph/iteration-count.txt"
mkdir -p "$WORK/.ai/ralph"

# --- CheckCompletion --------------------------------------------------------
run CheckCompletion.sh
check "no log -> continue"         "continue" "$OUT"
printf '# Log\n- Remember to write "RALPH_COMPLETE" as the final line when done.\n- iteration 1: did stuff\n' > "$LOG"
run CheckCompletion.sh
check "quoted mid-log -> continue" "continue" "$OUT"
printf 'RALPH_COMPLETE\n' >> "$LOG"
run CheckCompletion.sh
check "last line -> complete"      "complete" "$OUT"
printf '\n\n' >> "$LOG"
run CheckCompletion.sh
check "trailing blanks ok"         "complete" "$OUT"
printf -- '- iteration 2: more work\n' >> "$LOG"
run CheckCompletion.sh
check "work after marker -> continue" "continue" "$OUT"
# The prompts themselves print the marker quoted/decorated — last-line
# containment, not an exact match, or ralph burns to its iteration cap.
printf '"RALPH_COMPLETE"\n' >> "$LOG"; run CheckCompletion.sh
check "quoted last line -> complete" "complete" "$OUT"
printf -- '- more\n**RALPH_COMPLETE**\n' >> "$LOG"; run CheckCompletion.sh
check "bold last line -> complete"   "complete" "$OUT"
printf -- '- more\nRALPH_COMPLETE — all done\n\n' >> "$LOG"; run CheckCompletion.sh
check "dash suffix last line -> complete" "complete" "$OUT"

# --- IncrementCounter -------------------------------------------------------
rm -f "$COUNTER"
run IncrementCounter.sh
check "increment from absent"      "1" "$OUT"
run IncrementCounter.sh
check "increment to 2"             "2" "$OUT"
printf '1 2' > "$COUNTER"; run IncrementCounter.sh
check "garbage counter exit 0"     "0" "$RC"
check "garbage counter -> 1"       "1" "$OUT"
printf 'abc' > "$COUNTER"; run IncrementCounter.sh
check "abc counter -> 1"           "1" "$OUT"

# --- CheckBudget ------------------------------------------------------------
printf '3' > "$COUNTER"; printf '10' > "$WORK/.ai/ralph/max-iterations.txt"
run CheckBudget.sh
check "under budget"               "budget_ok" "$OUT"
printf '10' > "$COUNTER"; run CheckBudget.sh
check "at budget"                  "budget_exhausted" "$OUT"
printf 'abc' > "$COUNTER"; run CheckBudget.sh
check "garbage count exit 0"       "0" "$RC"
check "garbage count -> ok"        "budget_ok" "$OUT"
printf '3' > "$COUNTER"; printf 'x' > "$WORK/.ai/ralph/max-iterations.txt"; run CheckBudget.sh
check "garbage max exit 0"         "0" "$RC"
check "garbage max -> default"     "budget_ok" "$OUT"

if [ "$fail" = 0 ]; then echo "ALL PASS"; else echo "SOME FAILED"; exit 1; fi
