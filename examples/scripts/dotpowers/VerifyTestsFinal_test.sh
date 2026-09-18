#!/usr/bin/env bash
# ABOUTME: Fixture tests for VerifyTestsFinal.sh (#646 item 8 sibling) — the
# ABOUTME: pre-ship test gate runs every detected stack and a tree with no
# ABOUTME: known build system fails loud instead of `final-verification-pass`.
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
install_tool_shims
SCRIPT="$(stage_script "$DIR/VerifyTestsFinal.sh")"
run() { OUT="$( (cd "$WORK" && PATH="$STATE/bin:$PATH" ${TEST_SH:-sh} "$SCRIPT") 2>&1)"; RC=$?; }
has() { printf '%s' "$OUT" | grep -q -- "$1" && echo yes || echo no; }

# 1. No build system -> exit 1.
run
check "no stack exit 1"            "1" "$RC"
check "no stack no pass marker"    "no" "$(has 'final-verification-pass')"

# 2. Polyglot: both test runners run; a red second stack fails.
touch "$WORK/go.mod" "$WORK/Cargo.toml"
reset_rc; run
check "green exit 0"               "0" "$RC"
check "ran go test"                "yes" "$(calls | grep -q 'go test ./...' && echo yes || echo no)"
check "ran cargo test"             "yes" "$(calls | grep -q 'cargo test' && echo yes || echo no)"
check "green marker"               "final-verification-pass" "$(printf '%s' "$OUT" | tail -1)"
reset_rc; set_rc cargo test 1; run
check "second stack red exit 1"    "1" "$RC"
check "second stack red marker"    "yes" "$(has 'FINAL_TEST_FAIL')"

if [ "$fail" = 0 ]; then echo "ALL PASS"; else echo "SOME FAILED"; exit 1; fi
