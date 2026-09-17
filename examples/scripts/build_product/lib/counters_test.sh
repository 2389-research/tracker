#!/usr/bin/env bash
# ABOUTME: Fixture tests for lib/counters.sh (bump_counter) — absent/garbage
# ABOUTME: counters read as 0, increments persist, and a counter path that
# ABOUTME: cannot be written (a directory in the way) fails loud naming the
# ABOUTME: path instead of a silent `set -e` abort (#640 B6).
set -uo pipefail
DIR="$(cd "$(dirname "$0")" && pwd)"
fail=0
check() { # name expected actual
  if [ "$2" = "$3" ]; then echo "ok: $1"; else echo "FAIL: $1 — want '$2' got '$3'"; fail=1; fi
}
WORK="$(mktemp -d)"
STATE="$(mktemp -d)"
trap 'rm -rf "$WORK" "$STATE"' EXIT
. "$DIR/../test_helpers.sh"
# A tiny driver script that sources the lib the way the nodes do (set -eu,
# POSIX sh) and prints ATTEMPTS after the bump.
cat > "$STATE/driver.sh" <<DRIVER
set -eu
. "$LIB_DIR/counters.sh"
bump_counter "\$1"
echo "attempts=\$ATTEMPTS"
DRIVER
run() { OUT="$( (cd "$WORK" && sh "$STATE/driver.sh" "$1") 2>"$STATE/stderr")"; RC=$?; }
last() { printf '%s' "$OUT" | tail -1; }

# 1. Absent counter -> 1; persists; increments.
mkdir -p "$WORK/.ai/x"
run .ai/x/n
check "absent -> 1"                 "attempts=1" "$(last)"
check "written 1"                   "1" "$(cat "$WORK/.ai/x/n")"
run .ai/x/n
check "second -> 2"                 "attempts=2" "$(last)"
check "exit 0"                      "0" "$RC"

# 2. Garbage / multi-token / blank counters read as 0.
printf '1 2\n' > "$WORK/.ai/x/n"; run .ai/x/n
check "garbage '1 2' -> 1"          "attempts=1" "$(last)"
printf 'abc\n' > "$WORK/.ai/x/n"; run .ai/x/n
check "garbage abc -> 1"            "attempts=1" "$(last)"
: > "$WORK/.ai/x/n"; run .ai/x/n
check "blank -> 1"                  "attempts=1" "$(last)"

# 3. #640 B6: the counter path is a DIRECTORY -> exit 1, message names the
#    path, no attempts line (the node must not proceed on a phantom count).
rm -f "$WORK/.ai/x/n"; mkdir -p "$WORK/.ai/x/n"
run .ai/x/n
check "dir in the way exit 1"       "1" "$RC"
check "dir in the way message"      "yes" "$(printf '%s' "$OUT" | grep -qF 'ERROR: cannot write attempt counter .ai/x/n' && echo yes || echo no)"
check "dir in the way no attempts"  "no"  "$(printf '%s' "$OUT" | grep -q 'attempts=' && echo yes || echo no)"

# 4. Parent directory missing -> same loud failure (callers mkdir first).
run .ai/missing/n
check "no parent exit 1"            "1" "$RC"
check "no parent message"           "yes" "$(printf '%s' "$OUT" | grep -qF 'cannot write attempt counter .ai/missing/n' && echo yes || echo no)"

if [ "$fail" = 0 ]; then echo "ALL PASS"; else echo "SOME FAILED"; exit 1; fi
