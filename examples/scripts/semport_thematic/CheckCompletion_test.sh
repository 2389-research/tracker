#!/usr/bin/env bash
# ABOUTME: Fixture tests for semport_thematic's CheckCompletion.sh (#646
# ABOUTME: items 7, 11) — grep (not rg) decides INCOMPLETE vs COMPLETE, and an
# ABOUTME: unset $target_name fails with a clear message under every shell.
set -uo pipefail
DIR="$(cd "$(dirname "$0")" && pwd)"
fail=0
check() { # name expected actual
  if [ "$2" = "$3" ]; then echo "ok: $1"; else echo "FAIL: $1 — want '$2' got '$3'"; fail=1; fi
}
WORK="$(mktemp -d)"
STATE="$(mktemp -d)"
trap 'rm -rf "$WORK" "$STATE"' EXIT
mkdir -p "$STATE/norg"; printf '#!/bin/sh\necho "rg: should not be called" >&2; exit 127\n' > "$STATE/norg/rg"; chmod +x "$STATE/norg/rg"
run() { OUT="$( (cd "$WORK" && PATH="$STATE/norg:$PATH" env -u target_name ${1:+target_name=$1} ${TEST_SH:-sh} "$DIR/CheckCompletion.sh") 2>"$STATE/stderr")"; RC=$?; }
SPEC="$WORK/.ai/semport/Foo/thematic-spec.md"
mkdir -p "$(dirname "$SPEC")"

# 1. target_name unset -> exit 1 with a message naming the variable.
run ""
check "unset target exit 1"        "1" "$RC"
check "unset target message"       "yes" "$(grep -q 'target_name' "$STATE/stderr" && echo yes || echo no)"

# 2. Spec missing -> INCOMPLETE (never COMPLETE on an absent checklist).
run Foo
check "missing spec exit 1"        "1" "$RC"
check "missing spec marker"        "INCOMPLETE" "$OUT"

# 3. Open checkbox -> INCOMPLETE, exit 1 (routes needs_more_porting).
printf '## Checklist\n- [x] a\n- [ ] b\n' > "$SPEC"; run Foo
check "open item exit 1"           "1" "$RC"
check "open item marker"           "INCOMPLETE" "$OUT"
check "rg never called"            "no" "$(grep -q 'rg: should not be called' "$STATE/stderr" && echo yes || echo no)"

# 4. All checked -> COMPLETE, exit 0.
printf '## Checklist\n- [x] a\n- [x] b\n' > "$SPEC"; run Foo
check "all checked exit 0"         "0" "$RC"
check "all checked marker"         "COMPLETE" "$OUT"

if [ "$fail" = 0 ]; then echo "ALL PASS"; else echo "SOME FAILED"; exit 1; fi
