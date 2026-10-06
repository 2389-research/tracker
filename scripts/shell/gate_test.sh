#!/usr/bin/env bash
# ABOUTME: Fixture tests for the shell-hygiene gate (gate.sh pipe-consumers):
# ABOUTME: pipes into early-exiting consumers under pipefail fail, escapes pass.
set -uo pipefail
cd "$(dirname "$0")"
GATE=./gate.sh
fails=0

WORK=$(mktemp -d)
trap 'rm -rf "$WORK"' EXIT

# The fixtures are built with printf and a %s-substituted pipe character so
# that this file itself (a pipefail script the gate scans) never contains the
# literal shapes it is testing for.
P='|'
mk() { # name content…  — write $WORK/<name>/f.sh from printf-formatted lines
  local name="$1"; shift
  mkdir -p "$WORK/$name"
  printf '%s\n' "$@" > "$WORK/$name/f.sh"
}

check() { # desc want_exit fixture_dir
  local desc="$1" want="$2" dir="$3"
  SHELL_GATE_ROOTS="$WORK/$dir" bash "$GATE" pipe-consumers >"$WORK/$dir.out" 2>&1
  local got=$?
  if [ "$got" != "$want" ]; then
    echo "FAIL: $desc (want exit $want, got $got)"; sed 's/^/    /' "$WORK/$dir.out"; fails=$((fails+1))
  else
    echo "ok: $desc"
  fi
}

mk clean      'set -euo pipefail' 'OUT="$(seq 3)"' 'case "$OUT" in *"2"*) echo yes ;; *) echo no ;; esac' "printf '%s' \"\$OUT\" $P tail -1" "printf '%s' \"\$OUT\" $P grep -F x >/dev/null"
mk badq       'set -euo pipefail' 'OUT="$(seq 3)"' "printf '%s' \"\$OUT\" $P grep -qF -- 2 && echo yes || echo no"
mk nopipefail 'set -eu'           'OUT="$(seq 3)"' "printf '%s' \"\$OUT\" $P grep -qF -- 2 && echo yes || echo no"
mk fileq      'set -euo pipefail' 'grep -q needle "$FILE" && echo yes || echo no' 'grep -qxF -- "$1" "$STATE/argv"'
mk ortrue     'set -euo pipefail' "git ls-files $P head -40 || true" "printf '%s' \"\$OUT\" $P grep -q x || true"
mk marker_ok  'set -euo pipefail' "cat big.log $P head -5  # pipefail-ok: status unused, output only"
mk marker_bad 'set -euo pipefail' "cat big.log $P head -5  # pipefail-ok:" "cat big.log $P head -5  # pipefail-ok"
mk head1      'set -o pipefail'   "FIRST=\"\$(printf '%s' \"\$OUT\" $P head -1)\""
mk tail1      'set -o pipefail'   "LAST=\"\$(printf '%s' \"\$OUT\" $P tail -1)\"" "calls $P tr ';' '\\n'" "sort $P uniq -c $P sort -rn"
mk oror       'set -euo pipefail' 'test -f x || grep -q y z' 'cmd || head -1 file'
mk comment    'set -euo pipefail' "# a comment that mentions printf $P grep -q is fine" "  # indented too: $P head -1"
mk sedq       'set -euo pipefail' "cat f $P sed -n '/x/{p;q;}'" "cat f $P sed 5q"
mk sedok      'set -euo pipefail' "diff a b $P sed -n '1,20p'" "cat f $P sed 's/^ *//'"
mk readq      'set -euo pipefail' "cmd $P read -r first" "cmd $P IFS= read -r first"
mk whileread  'set -euo pipefail' "cmd $P while IFS= read -r l; do echo \"\$l\"; done"
mk cmps       'set -euo pipefail' "cmd $P cmp -s - expected"
mk grepm      'set -euo pipefail' "cmd $P grep -E -m1 x" "cmd $P grep --max-count=1 x" "cmd $P grep -l x" "cmd $P grep -L x"
mk grepfine   'set -euo pipefail' "cmd $P grep -c x" "cmd $P grep -v x" "cmd $P grep -nE x" "cmd $P grep -F -- x >/dev/null"
# Logical lines folded across physical lines (#664): a pipe at end-of-line or a
# `\`-continuation carries its consumer onto the next line. The gate must join
# them before matching. '\\' in the printf arg writes one literal backslash.
mk splitpipe  'set -euo pipefail' 'OUT="$(seq 3)"' "printf '%s' \"\$OUT\" $P" 'grep -qF -- 2 && echo yes || echo no'
mk contpipe   'set -euo pipefail' 'OUT="$(seq 3)"' "printf '%s' \"\$OUT\" $P\\" 'grep -qF -- 2 && echo yes || echo no'
mk splitok    'set -euo pipefail' 'OUT="$(seq 3)"' "printf '%s' \"\$OUT\" $P" 'tail -1'
mkdir -p "$WORK/empty"; printf 'not a shell file\n' > "$WORK/empty/README.md"

check "clean file passes"                               0 clean
check "printf piped into grep -q under pipefail FAILS"  1 badq
check "same line without pipefail passes"               0 nopipefail
check "grep -q FILE (no pipe) passes"                   0 fileq
check "'|| true' suffix passes"                         0 ortrue
check "'# pipefail-ok: <reason>' passes"                0 marker_ok
check "'# pipefail-ok' without a reason FAILS"          1 marker_bad
check "pipe into head -1 under pipefail FAILS"           1 head1
check "'| tail -1' / tr / sort passes"                  0 tail1
check "'||' (logical or) is not a pipe"                 0 oror
check "comment lines are skipped"                       0 comment
check "pipe into 'sed … q' FAILS"                        1 sedq
check "pipe into 'sed -n 1,20p' / 's///' passes"         0 sedok
check "pipe into read FAILS"                             1 readq
check "pipe into a while-read loop passes (drains)"    0 whileread
check "pipe into 'cmp -s' FAILS"                         1 cmps
check "grep -m / --max-count / -l / -L FAIL"            1 grepm
check "grep -c / -v / -nE / -F >/dev/null pass"         0 grepfine
check "pipe-at-EOL folded into grep -q FAILS"           1 splitpipe
check "'\\'-continuation folded into grep -q FAILS"      1 contpipe
check "folded non-consumer pipe (| tail) passes"        0 splitok
check "empty scan (zero shell files) FATALs"            1 empty

# The hit count must be exact — every offending line reported once.
SHELL_GATE_ROOTS="$WORK/grepm" bash "$GATE" pipe-consumers > "$WORK/grepm.count" 2>&1
n=$(grep -c '^  PIPE ' "$WORK/grepm.count")
if [ "$n" = 4 ]; then echo "ok: four grep hits reported individually"; else echo "FAIL: want 4 PIPE lines for grepm, got $n"; fails=$((fails+1)); fi

[ "$fails" -eq 0 ] && { echo "ALL PASS"; exit 0; } || { echo "$fails FAILED"; exit 1; }
