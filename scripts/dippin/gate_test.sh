#!/usr/bin/env bash
# ABOUTME: Shell-contract fixtures for the fail-closed Dippin gate (gate.sh).
# ABOUTME: Pins that findings parse, but crash/malformed/empty-glob fail loud.
set -uo pipefail
cd "$(dirname "$0")"
GATE=./gate.sh
fails=0

WORK=$(mktemp -d)
trap 'rm -f fake_*; rm -rf "$WORK"' EXIT
touch "$WORK/a.dip"

# make_fake <name> <stdout> <exit> — write a fake `dippin` invoked as `<cmd> check <file>`
make_fake() {
  local path="$WORK/$1"
  printf '#!/usr/bin/env bash\nprintf "%%s" %q\nexit %s\n' "$2" "$3" > "$path"
  chmod +x "$path"
  printf '%s' "$path"
}

check() { # desc want_exit dippin_cmd pattern
  local desc="$1" want="$2" dip="$3" pat="$4"
  DIPPIN="$dip" bash "$GATE" lint "$pat" >/dev/null 2>&1
  local got=$?
  if [ "$got" != "$want" ]; then
    echo "FAIL: $desc (want exit $want, got $got)"; fails=$((fails+1))
  else
    echo "ok: $desc"
  fi
}

CLEAN=$(make_fake fake_ok '{"valid":true,"errors":0}' 0)
FINDINGS=$(make_fake fake_findings '{"valid":false,"errors":2}' 1)
CRASH=$(make_fake fake_crash '' 127)
MALFORMED=$(make_fake fake_bad 'not-json' 0)

check "clean input passes"                       0 "$CLEAN"     "$WORK/*.dip"
# nonzero exit + valid JSON is a FINDINGS run, not a crash — must fail as findings
check "findings (nonzero exit, valid JSON) fail" 1 "$FINDINGS"  "$WORK/*.dip"
check "command failure (no JSON) fails loud"     1 "$CRASH"     "$WORK/*.dip"
check "malformed JSON fails loud"                1 "$MALFORMED" "$WORK/*.dip"
check "empty glob fails loud"                    1 "$CLEAN"     "$WORK/nomatch/*.dip"

# `gate.sh test`: runs `dippin test` on each .dip with a sibling .test.json.
# The fake prints a summary line and exits as given.
TW="$WORK/scen"
mkdir -p "$TW"
touch "$TW/a.dip" "$TW/a.test.json" "$TW/untested.dip"
check_test() { # desc want_exit dippin_cmd pattern
  local desc="$1" want="$2" dip="$3" pat="$4"
  DIPPIN="$dip" bash "$GATE" test "$pat" >/dev/null 2>&1
  local got=$?
  if [ "$got" != "$want" ]; then
    echo "FAIL: $desc (want exit $want, got $got)"; fails=$((fails+1))
  else
    echo "ok: $desc"
  fi
}
T_PASS=$(make_fake fake_tpass '  7 tests: 7 passed, 0 failed' 0)
T_FAIL=$(make_fake fake_tfail '  7 tests: 5 passed, 2 failed' 1)
T_ZERO=$(make_fake fake_tzero '  0 tests: 0 passed, 0 failed' 0)
T_NOSUM=$(make_fake fake_tnosum 'panic: boom' 0)
T_LIE=$(make_fake fake_tlie '  7 tests: 5 passed, 2 failed' 0)

check_test "scenarios all passing pass"                   0 "$T_PASS"  "$TW/*.dip"
check_test "a failing scenario fails"                     1 "$T_FAIL"  "$TW/*.dip"
check_test "zero scenarios fail loud"                     1 "$T_ZERO"  "$TW/*.dip"
check_test "no summary line (crash, exit 0) fails loud"   1 "$T_NOSUM" "$TW/*.dip"
check_test "failures in the summary fail despite exit 0"  1 "$T_LIE"   "$TW/*.dip"
check_test "no .dip with a .test.json fails loud"         1 "$T_PASS"  "$TW/untested.dip"
check_test "empty glob fails loud"                        1 "$T_PASS"  "$TW/nomatch/*.dip"
OW="$WORK/orphan"
mkdir -p "$OW"
touch "$OW/a.dip" "$OW/a.test.json" "$OW/renamed.test.json"
check_test "orphaned .test.json (no .dip) fails loud"     1 "$T_PASS"  "$OW/*.dip"

# Exact version derivation from a go.mod fixture.
GM="$WORK/go.mod"
printf 'module x\n\ngo 1.25\n\nrequire (\n\tgithub.com/2389-research/dippin-lang v0.68.0\n)\n' > "$GM"
got_ver=$(GO_MOD="$GM" bash "$GATE" version)
if [ "$got_ver" = "v0.68.0" ]; then
  echo "ok: version derivation from go.mod"
else
  echo "FAIL: version derivation (want v0.68.0, got '$got_ver')"; fails=$((fails+1))
fi

[ "$fails" -eq 0 ] && { echo "ALL PASS"; exit 0; } || { echo "$fails FAILED"; exit 1; }
