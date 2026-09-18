#!/usr/bin/env bash
# ABOUTME: Fixture tests for semport's TryBuild/VerifyBuild/LoadErrors (#646
# ABOUTME: items 11, 12) — the build log is per-workdir (.ai/semport/build.log,
# ABOUTME: not a shared /tmp path), a red build with ZERO `error:` lines still
# ABOUTME: prints STILL_FAILING under set -e, and LoadErrors' total is a single
# ABOUTME: number (the old `grep -c … || echo 0` printed `0\n0`).
set -uo pipefail
DIR="$(cd "$(dirname "$0")" && pwd)"
fail=0
check() { # name expected actual
  if [ "$2" = "$3" ]; then echo "ok: $1"; else echo "FAIL: $1 — want '$2' got '$3'"; fail=1; fi
}
WORK="$(mktemp -d)"
STATE="$(mktemp -d)"
trap 'rm -rf "$WORK" "$STATE"' EXIT
mkdir -p "$STATE/bin"
cat > "$STATE/bin/swift" <<SHIM
#!/bin/sh
[ -f "$STATE/out-swift" ] && cat "$STATE/out-swift"
[ -f "$STATE/rc-swift" ] && exit "\$(cat "$STATE/rc-swift")"
exit 0
SHIM
chmod +x "$STATE/bin/swift"
run() { OUT="$( (cd "$WORK" && PATH="$STATE/bin:$PATH" ${TEST_SH:-sh} "$DIR/$1") 2>&1)"; RC=$?; }
LOG="$WORK/.ai/semport/build.log"

# 1. VerifyBuild: red build with zero `error:` lines (e.g. a linker failure)
#    -> STILL_FAILING (0 errors), exit 1 — not a silent set -e abort.
printf 'ld: symbol not found\n' > "$STATE/out-swift"; echo 1 > "$STATE/rc-swift"
run VerifyBuild.sh
check "zero-error red exit 1"      "1" "$RC"
check "zero-error red marker"      "STILL_FAILING (0 errors)" "$OUT"
check "log under .ai/semport"      "yes" "$([ -f "$LOG" ] && echo yes || echo no)"

# 2. VerifyBuild: red build with two errors -> STILL_FAILING (2 errors).
printf 'Sources/OmniAgentsSDK/A.swift:1:1: error: x\nSources/OmniAgentsSDK/B.swift:2:2: error: y\n' > "$STATE/out-swift"
run VerifyBuild.sh
check "two errors marker"          "STILL_FAILING (2 errors)" "$OUT"

# 3. TryBuild: red -> FAIL + error lines, exit 1; reads/writes the same log.
run TryBuild.sh
check "trybuild red exit 1"        "1" "$RC"
check "trybuild red first line"    "FAIL" "$(printf '%s' "$OUT" | head -1)"
check "trybuild shows errors"      "2" "$(printf '%s' "$OUT" | grep -c 'error:')"

# 4. LoadErrors: total is exactly one number; zero errors -> 0 (not 0\n0).
run LoadErrors.sh
check "loaderrors total 2"         "=== Total errors: 2 ===" "$(printf '%s' "$OUT" | head -1)"
printf 'ld: symbol not found\n' > "$STATE/out-swift"; run VerifyBuild.sh
run LoadErrors.sh
check "loaderrors exit 0"          "0" "$RC"
check "loaderrors total 0"         "=== Total errors: 0 ===" "$(printf '%s' "$OUT" | head -1)"
check "loaderrors single 0"        "1" "$(printf '%s' "$OUT" | grep -c '^0$\|Total errors: 0')"

# 5. Green -> BUILD_CLEAN / PASS.
rm -f "$STATE/rc-swift" "$STATE/out-swift"
run VerifyBuild.sh
check "green verify"               "BUILD_CLEAN" "$OUT"
run TryBuild.sh
check "green try"                  "PASS" "$OUT"

if [ "$fail" = 0 ]; then echo "ALL PASS"; else echo "SOME FAILED"; exit 1; fi
