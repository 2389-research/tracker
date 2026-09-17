#!/usr/bin/env bash
# ABOUTME: Fixture tests for TestMilestone.sh — wraps the shared verify.sh
# ABOUTME: green-gate (#406) with the on-disk fix-attempt counter and the
# ABOUTME: tests-pass / escalate routing sentinels: exit 0 green, exit 2 =
# ABOUTME: environment escalate, exit 1 = fix loop until the 3rd attempt.
set -uo pipefail
DIR="$(cd "$(dirname "$0")" && pwd)"
SCRIPT="$DIR/TestMilestone.sh"
fail=0
check() { # name expected actual
  if [ "$2" = "$3" ]; then echo "ok: $1"; else echo "FAIL: $1 — want '$2' got '$3'"; fail=1; fi
}
WORK="$(mktemp -d)"
STATE="$(mktemp -d)"
trap 'rm -rf "$WORK" "$STATE"' EXIT
run() { OUT="$( (cd "$WORK" && sh "$SCRIPT") 2>"$STATE/stderr")"; RC=$?; }
last() { printf '%s' "$OUT" | tail -1; }
COUNTER="$WORK/.ai/milestones/fix_attempts"
# verify.sh is Setup's artifact (its real body is exercised in Setup_test.sh);
# here a stub that logs a call and exits with the code in .ai/build/verify-rc
# isolates the wrapper's counter + sentinel logic.
mkdir -p "$WORK/.ai/build" "$WORK/.ai/milestones"
cat > "$WORK/.ai/build/verify.sh" <<'STUB'
echo "verify.sh ran" >> .ai/build/verify-calls
echo "stub verify output"
exit "$(cat .ai/build/verify-rc)"
STUB
set_rc() { echo "$1" > "$WORK/.ai/build/verify-rc"; }
calls() { wc -l < "$WORK/.ai/build/verify-calls" | tr -d ' '; }

# 1. Green on first attempt: counter reset to 0, tests-pass LAST.
set_rc 0
run
check "green exit 0"                 "0" "$RC"
check "green marker last"            "tests-pass" "$(last)"
check "green resets counter"         "0" "$(cat "$COUNTER")"
check "verify output surfaced"       "yes" "$(printf '%s' "$OUT" | grep -q 'stub verify output' && echo yes || echo no)"
check "verify invoked once"          "1" "$(calls)"

# 2. Red: attempts 1 and 2 hand off to the fix loop (exit 1, NO escalate
#    marker); the 3rd consecutive failure escalates (-ge 3 = exactly 3
#    test attempts / 2 fix passes per milestone).
rm -f "$COUNTER"
set_rc 1
run
check "red 1 exit 1"                 "1" "$RC"
check "red 1 counter"                "1" "$(cat "$COUNTER")"
check "red 1 attempt line"           "yes" "$(printf '%s' "$OUT" | grep -q -- '--- attempt 1 of 3 ---' && echo yes || echo no)"
check "red 1 no escalate"            "no" "$(printf '%s' "$OUT" | grep -q 'escalate' && echo yes || echo no)"
run
check "red 2 exit 1"                 "1" "$RC"
check "red 2 counter"                "2" "$(cat "$COUNTER")"
check "red 2 no escalate"            "no" "$(printf '%s' "$OUT" | grep -q 'escalate' && echo yes || echo no)"
run
check "red 3 exit 1"                 "1" "$RC"
check "red 3 counter"                "3" "$(cat "$COUNTER")"
check "red 3 ESCALATE line"          "yes" "$(printf '%s' "$OUT" | grep -q 'ESCALATE: milestone failed after 3 attempts' && echo yes || echo no)"
check "red 3 escalate marker last"   "escalate" "$(last)"

# 3. Counter is sticky past the cap (a 4th red still escalates) until a
#    green run or MarkMilestoneDone resets it.
run
check "red 4 escalates"              "escalate" "$(last)"
set_rc 0
run
check "green after red resets"       "0" "$(cat "$COUNTER")"

# 4. Exit 2 from verify.sh = environment problem (make missing): escalate
#    IMMEDIATELY regardless of attempt count, and reset the counter so the
#    fixed environment gets a fresh fix budget.
echo 1 > "$COUNTER"
set_rc 2
run
check "env exit 1"                   "1" "$RC"
check "env escalate marker last"     "escalate" "$(last)"
check "env resets counter"           "0" "$(cat "$COUNTER")"
check "env no attempt line"          "no" "$(printf '%s' "$OUT" | grep -q -- '--- attempt' && echo yes || echo no)"

# 5. Numeric guard: corrupted counter is treated as 0 → this red is attempt 1.
set_rc 1
for junk in 'garbage' '1 2' ''; do
  printf '%s\n' "$junk" > "$COUNTER"
  run
  check "junk '$junk' -> attempt 1"  "1" "$(cat "$COUNTER")"
  check "junk '$junk' -> no escalate" "no" "$(printf '%s' "$OUT" | grep -q 'escalate' && echo yes || echo no)"
done

# 6. A runner exit that is neither 0/1/2 (e.g. 3) is an ordinary failure.
rm -f "$COUNTER"; set_rc 3
run
check "rc 3 is normal fail"          "1" "$RC"
check "rc 3 no escalate"             "no" "$(printf '%s' "$OUT" | grep -q 'escalate' && echo yes || echo no)"

# 7. verify.sh missing (Setup did not run): the node still exits 1 and never
#    prints tests-pass. KNOWN-QUIRK: which failure path it takes depends on
#    the platform `sh` — bash-as-sh (macOS) exits 127 on a missing script
#    (ordinary red attempt, counter 1, fix loop), but dash (Debian/Ubuntu)
#    exits 2, which COLLIDES with the rc=2 "make missing" reservation and
#    routes to `escalate` with the counter reset to 0. Escalating on a
#    missing gate script is arguably the better outcome, but it is reached by
#    accident. The suite probes the local sh and asserts the matching path.
rm -f "$WORK/.ai/build/verify.sh" "$COUNTER"
SH_MISSING_RC=$( (cd "$WORK" && sh .ai/build/no-such-file.sh) 2>/dev/null; echo $?)
run
check "missing verify.sh exit 1"     "1" "$RC"
check "missing verify.sh no pass"    "no" "$(printf '%s' "$OUT" | grep -q 'tests-pass' && echo yes || echo no)"
if [ "$SH_MISSING_RC" = 2 ]; then
  check "KNOWN-QUIRK dash: missing verify.sh escalates" "escalate" "$(last)"
  check "KNOWN-QUIRK dash: counter reset"               "0" "$(cat "$COUNTER")"
else
  check "bash-as-sh: missing verify.sh is a red attempt" "1" "$(cat "$COUNTER")"
  check "bash-as-sh: no escalate"                        "no" "$(printf '%s' "$OUT" | grep -q 'escalate' && echo yes || echo no)"
fi

if [ "$fail" = 0 ]; then echo "ALL PASS"; else echo "SOME FAILED"; exit 1; fi
