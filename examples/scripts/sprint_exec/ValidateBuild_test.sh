#!/usr/bin/env bash
# ABOUTME: Fixture tests for sprint_exec's ValidateBuild.sh (#646 items 7, 11)
# ABOUTME: — no known build system is a FAIL (not validation-pass-…), the Swift
# ABOUTME: gate uses grep (not rg) and a red `swift build` is a FAIL with its
# ABOUTME: log shown, and logs live under .ai/build/ (not a shared /tmp path).
set -uo pipefail
DIR="$(cd "$(dirname "$0")" && pwd)"
fail=0
check() { # name expected actual
  if [ "$2" = "$3" ]; then echo "ok: $1"; else echo "FAIL: $1 — want '$2' got '$3'"; fail=1; fi
}
WORK="$(mktemp -d)"
STATE="$(mktemp -d)"
trap 'rm -rf "$WORK" "$STATE"' EXIT
# Shims: go/npm/swift log argv, print $STATE/out-<tool>-<sub>, exit $STATE/rc-<tool>-<sub>.
mkdir -p "$STATE/bin"
for tool in go npm swift; do
  cat > "$STATE/bin/$tool" <<SHIM
#!/bin/sh
echo "$tool \$*" >> "$STATE/calls"
sub="\${1:-none}"
[ -f "$STATE/out-$tool-\$sub" ] && cat "$STATE/out-$tool-\$sub"
[ -f "$STATE/rc-$tool-\$sub" ] && exit "\$(cat "$STATE/rc-$tool-\$sub")"
exit 0
SHIM
  chmod +x "$STATE/bin/$tool"
done
# No rg on PATH: the gate must not depend on ripgrep.
mkdir -p "$STATE/norg"; printf '#!/bin/sh\necho "rg: should not be called" >&2; exit 127\n' > "$STATE/norg/rg"; chmod +x "$STATE/norg/rg"
reset() { rm -f "$STATE"/rc-* "$STATE"/out-* "$STATE/calls"; }
run() { OUT="$( (cd "$WORK" && PATH="$STATE/bin:$STATE/norg:$PATH" ${TEST_SH:-sh} "$DIR/ValidateBuild.sh") 2>&1)"; RC=$?; }
has() { printf '%s' "$OUT" | grep -q -- "$1" && echo yes || echo no; }
last() { printf '%s' "$OUT" | tail -1; }

# 1. No build system -> exit 1, no validation-pass marker.
run
check "no stack exit 1"            "1" "$RC"
check "no stack no pass marker"    "no" "$(has 'validation-pass')"
check "no stack diagnostic"        "yes" "$(has 'no known build system')"

# 2. Go green -> validation-pass-go; logs under .ai/build/, none in /tmp.
touch "$WORK/go.mod"; reset; run
check "go green exit 0"            "0" "$RC"
check "go green marker"            "validation-pass-go" "$(last)"
check "go log in .ai/build"        "yes" "$([ -f "$WORK/.ai/build/test.log" ] && echo yes || echo no)"
# 3. Go red test -> exit 1 with the log shown.
reset; echo "--- FAIL: TestX" > "$STATE/out-go-test"; echo 1 > "$STATE/rc-go-test"; run
check "go red exit 1"              "1" "$RC"
check "go red shows log"           "yes" "$(has 'FAIL: TestX')"
rm -f "$WORK/go.mod"

# 4. Swift: build with `error:` lines -> exit 1 (grep, not rg); rg never called.
touch "$WORK/Package.swift"; reset
printf 'Sources/A.swift:3:5: error: cannot find x\n' > "$STATE/out-swift-build"; echo 1 > "$STATE/rc-swift-build"; run
check "swift build red exit 1"     "1" "$RC"
check "swift build red shows log"  "yes" "$(has 'cannot find x')"
check "rg never called"            "no" "$(has 'rg: should not be called')"
# 5. Swift: build clean, `swift test` non-zero -> exit 1.
reset; echo 1 > "$STATE/rc-swift-test"; printf 'Test Case failed\n' > "$STATE/out-swift-test"; run
check "swift test red exit 1"      "1" "$RC"
# 6. Swift green -> validation-pass-swift.
reset; run
check "swift green exit 0"         "0" "$RC"
check "swift green marker"         "validation-pass-swift" "$(last)"
rm -f "$WORK/Package.swift"

# 7. Node green -> validation-pass-node.
touch "$WORK/package.json"; reset; run
check "node green marker"          "validation-pass-node" "$(last)"

if [ "$fail" = 0 ]; then echo "ALL PASS"; else echo "SOME FAILED"; exit 1; fi
