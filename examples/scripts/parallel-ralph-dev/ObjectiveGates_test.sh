#!/usr/bin/env bash
# ABOUTME: Fixture tests for parallel-ralph-dev's ObjectiveGates.sh (#646 item
# ABOUTME: 11) — gate diagnostics are captured to .ai/gates/*.log and their
# ABOUTME: tail is surfaced (stderr; stdout stays the bare routing marker), and
# ABOUTME: the TODO gate diffs against the recorded base ref, not a hardcoded
# ABOUTME: `main`. CreateBranches.sh records that base.
set -uo pipefail
DIR="$(cd "$(dirname "$0")" && pwd)"
fail=0
check() { # name expected actual
  if [ "$2" = "$3" ]; then echo "ok: $1"; else echo "FAIL: $1 — want '$2' got '$3'"; fail=1; fi
}
WORK="$(mktemp -d)"
STATE="$(mktemp -d)"
trap 'rm -rf "$WORK" "$STATE"' EXIT
export HOME="$STATE" GIT_CONFIG_GLOBAL=/dev/null GIT_CONFIG_SYSTEM=/dev/null
mkdir -p "$STATE/bin"
cat > "$STATE/bin/go" <<SHIM
#!/bin/sh
sub="\${1:-none}"
[ -f "$STATE/out-go-\$sub" ] && cat "$STATE/out-go-\$sub" >&2
[ -f "$STATE/rc-go-\$sub" ] && exit "\$(cat "$STATE/rc-go-\$sub")"
exit 0
SHIM
chmod +x "$STATE/bin/go"
reset() { rm -f "$STATE"/rc-* "$STATE"/out-*; }
run() { OUT="$( (cd "$WORK" && PATH="$STATE/bin:$PATH" ${TEST_SH:-sh} "$DIR/$1") 2>"$STATE/stderr")"; RC=$?; }

# Repo whose default branch is NOT main, with a feature branch on top.
(cd "$WORK" && git init -q -b trunk && git config user.email t@t && git config user.name t \
  && mkdir -p .ai && echo a > a.go && git add . && git commit -qm base)
run CreateBranches.sh
check "create branches marker"     "branches_created" "$OUT"
check "base ref recorded"          "$(cd "$WORK" && git rev-parse HEAD)" "$(cat "$WORK/.ai/base-ref.txt")"
(cd "$WORK" && git checkout -q feature/stream-a && printf 'package a // TODO later\n' > a.go && git commit -qam work)

# 1. All green, one TODO in new code -> gates_pass (TODO is a WARN), stdout is
#    ONLY the marker, results file lists the gates and the TODO count.
reset; run ObjectiveGates.sh
check "green exit 0"               "0" "$RC"
check "green marker only"          "gates_pass" "$OUT"
check "results file"               "yes" "$(grep -q '\[PASS\] go test' "$WORK/.ai/gate-results.txt" && echo yes || echo no)"
check "todo warn counted"          "yes" "$(grep -q '\[WARN\] 1 TODOs in new code' "$WORK/.ai/gate-results.txt" && echo yes || echo no)"

# 2. Red vet: diagnostics are NOT discarded — captured to .ai/gates/vet.log
#    and the tail shown on stderr; marker gates_fail.
reset; printf 'a.go:1:1: unreachable code\n' > "$STATE/out-go-vet"; echo 1 > "$STATE/rc-go-vet"
run ObjectiveGates.sh
check "vet red marker"             "gates_fail" "$OUT"
check "vet log captured"           "yes" "$(grep -q 'unreachable code' "$WORK/.ai/gates/vet.log" && echo yes || echo no)"
check "vet tail on stderr"         "yes" "$(grep -q 'unreachable code' "$STATE/stderr" && echo yes || echo no)"
check "vet fail in results"        "yes" "$(grep -q '\[FAIL\] go vet' "$WORK/.ai/gate-results.txt" && echo yes || echo no)"

# 3. No recorded base and no `main`: the TODO gate falls back to origin/HEAD
#    or, absent that, warns instead of silently diffing against nothing.
rm -f "$WORK/.ai/base-ref.txt"; reset; run ObjectiveGates.sh
check "no base still passes"       "gates_pass" "$OUT"
check "no base warns"              "yes" "$(grep -q 'no base ref' "$WORK/.ai/gate-results.txt" && echo yes || echo no)"

if [ "$fail" = 0 ]; then echo "ALL PASS"; else echo "SOME FAILED"; exit 1; fi
