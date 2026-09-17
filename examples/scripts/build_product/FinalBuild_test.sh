#!/usr/bin/env bash
# ABOUTME: Fixture tests for FinalBuild.sh — re-emits the gate scripts from the
# ABOUTME: sidecar (#640 D6) then runs the SAME verify.sh in --final ship mode:
# ABOUTME: every stack anywhere in the tree, -count=1, known_failures ignored,
# ABOUTME: zero Go tests = red, elapsed per stack; marker only on full green.
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
SCRIPT="$(stage_script "$DIR/FinalBuild.sh")"   # ${graph.workflow_dir} expanded as the engine does
G() { git -C "$WORK" -c user.name=t -c user.email=t@t "$@"; }
G -c init.defaultBranch=main init -q
mkdir -p "$WORK/.ai/build" "$WORK/.ai/milestones"
printf '.ai/\n' > "$WORK/.gitignore"
run() { rm -f "$STATE/calls" "$STATE/argv"; OUT="$( (cd "$WORK" && PATH="$STATE/bin:$PATH" sh "$SCRIPT") 2>"$STATE/stderr")"; RC=$?; }
last() { printf '%s' "$OUT" | tail -1; }
ohas() { printf '%s' "$OUT" | grep -qF -- "$1" && echo yes || echo no; }
chas() { printf '%s' "$(calls)" | grep -qF -- "$1" && echo yes || echo no; }
TAB=$'\t'

# 1. No build system: RED (#640 D1) — a product with no test stack cannot
#    ship green — unless the operator opted out.
run
check "no stack exit 1"               "1" "$RC"
check "no stack no marker"            "no" "$(ohas 'final-build-pass')"
check "no stack error"                "yes" "$(ohas 'ERROR: no build system detected')"
touch "$WORK/.ai/build/no-tests-ok"
run
check "opt-out exit 0"                "0" "$RC"
check "opt-out marker last"           "final-build-pass" "$(last)"
rm -f "$WORK/.ai/build/no-tests-ok"

# 2. All four stacks green: every runner runs in order (go build BEFORE
#    go test -count=1); ci-probe's native gates run too; marker last;
#    elapsed line per stack (#640 D13).
touch "$WORK/go.mod" "$WORK/package.json" "$WORK/pyproject.toml" "$WORK/Cargo.toml"
run
check "all stacks exit 0"             "0" "$RC"
check "all stacks marker"             "final-build-pass" "$(last)"
check "go build then test -count=1"   "yes" "$(printf '%s' "$(calls)" | grep -q 'go build ./...;.*go test -count=1 ./...' && echo yes || echo no)"
check "-count=1 is one argv"          "yes" "$(argv_has "go${TAB}test${TAB}-count=1${TAB}./...")"
for want in 'npm test' 'uv run pytest' 'cargo test' 'go vet ./...' 'golangci-lint run'; do
  check "ran: $want"                  "yes" "$(chas "$want")"
done
check "elapsed per stack"             "yes" "$(printf '%s' "$OUT" | grep -qE '^=== stack: cargo in \. — [0-9]+s, PASS ===$' && echo yes || echo no)"
check "no lint scoping at ship gate"  "no"  "$(chas '--new-from-rev')"

# 3. #305 sweep: go test AND npm test fail — BOTH still run, cargo/uv still
#    run, exit 1, no marker; the CI gate still reports (all results in one
#    pass).
set_rc go test 1; set_rc npm test 1
run
check "sweep exit 1"                  "1" "$RC"
check "sweep no marker"               "no" "$(ohas 'final-build-pass')"
for want in 'go test -count=1 ./...' 'npm test' 'uv run pytest' 'cargo test'; do
  check "sweep still ran: $want"      "yes" "$(chas "$want")"
done
check "sweep FAIL elapsed line"       "yes" "$(printf '%s' "$OUT" | grep -qE '^=== stack: go in \. — [0-9]+s, FAIL ===$' && echo yes || echo no)"
reset_rc

# 4. `go build` failure fails the Go stack (no go test for it); the other
#    stacks still run; exit 1.
set_rc go build 2
run
check "build fail exit 1"             "1" "$RC"
check "build fail: no go test"        "no"  "$(argv_has "go${TAB}test${TAB}-count=1${TAB}./...")"
check "build fail: npm still ran"     "yes" "$(chas 'npm test')"
reset_rc

# 5. A runner that exits 2 is just a failure (no special meaning here).
set_rc cargo test 2
run
check "cargo rc2 exit 1"              "1" "$RC"
reset_rc

# 6. CI gate: a failing make target fails the node; make missing (marker)
#    fails the node too — FinalBuild has no fix loop, no escalate
#    distinction, and no reserved exit number (#640 E8).
printf 'ci:\n\techo i\n' > "$WORK/Makefile"
set_rc make ci 2
run
check "ci fail exit 1"                "1" "$RC"
check "ci fail no marker"             "no" "$(ohas 'final-build-pass')"
reset_rc
mkdir -p "$STATE/pbin"
for t in sh dash bash cat grep paste git awk sed sort uniq head tail tr wc ls printf mkdir rm cp mv dirname basename cut env uname mktemp date cmp find; do
  p="$(command -v "$t" 2>/dev/null)" && [ -n "$p" ] && ln -sf "$p" "$STATE/pbin/$t"
done
for t in go npm uv cargo golangci-lint; do ln -sf "$STATE/bin/$t" "$STATE/pbin/$t"; done
OUT="$( (cd "$WORK" && PATH="$STATE/pbin" sh "$SCRIPT") 2>"$STATE/stderr")"; RC=$?
check "make missing exit 1"           "1" "$RC"
check "make missing marker line"      "yes" "$(printf '%s' "$OUT" | grep -qx '_TRACKER_CI_MAKE_MISSING' && echo yes || echo no)"
check "make missing no marker"        "no" "$(ohas 'final-build-pass')"
rm -f "$WORK/Makefile"

# 7. #640 D12: known_failures is IGNORED by the ship gate — no -skip — and
#    the still-listed entries are printed as the reason.
printf 'TestStillListed\n' > "$WORK/.ai/milestones/known_failures"
run
check "known_failures: no -skip"      "no"  "$(chas '-skip')"
check "known_failures: listed"        "yes" "$(ohas 'still listed: TestStillListed')"
rm -f "$WORK/.ai/milestones/known_failures"

# 8. #640 D7: a Go tree with zero test files cannot ship green.
set_out go list-tests ""
run
check "zero tests exit 1"             "1" "$RC"
check "zero tests error"              "yes" "$(ohas 'ERROR: no Go test files in ANY Go stack')"
check "zero tests no marker"          "no"  "$(ohas 'final-build-pass')"
reset_rc

# 9. #640 D6: tampered / missing gate scripts are re-emitted from the
#    sidecar before the ship gate runs (a stub could never pass it).
printf 'exit 0\n' > "$WORK/.ai/build/verify.sh"
set_rc go test 1
run
check "tampered verify.sh: red"       "1" "$RC"
check "tampered verify.sh: WARNING"   "yes" "$(ohas 'WARNING: .ai/build/verify.sh differed from the workflow')"
check "tampered verify.sh: restored"  "yes" "$(cmp -s "$LIB_DIR/verify.sh" "$WORK/.ai/build/verify.sh" && echo yes || echo no)"
reset_rc
rm -f "$WORK/.ai/build/ci-probe.sh" "$WORK/.ai/build/verify.sh"
run
check "missing gate files: re-emitted" "yes" "$([ -f "$WORK/.ai/build/verify.sh" ] && [ -f "$WORK/.ai/build/ci-probe.sh" ] && echo yes || echo no)"
check "missing gate files: green"     "final-build-pass" "$(last)"

# 10. #640 D1: nested stacks with no root manifest each run in their own
#     directory.
rm -f "$WORK/go.mod" "$WORK/package.json" "$WORK/pyproject.toml" "$WORK/Cargo.toml"
mkdir -p "$WORK/backend" "$WORK/frontend"
touch "$WORK/backend/go.mod" "$WORK/frontend/package.json"
cat > "$STATE/bin/npm" <<SHIM
#!/bin/sh
echo "npm \$* in \${PWD##*/}" >> "$STATE/calls"
exit 0
SHIM
run
check "nested: exit 0"                "0" "$RC"
check "nested: backend go stack"      "yes" "$(ohas '=== stack: go in backend ===')"
check "nested: npm ran in frontend"   "yes" "$(chas 'npm test in frontend')"
check "nested: marker last"           "final-build-pass" "$(last)"

if [ "$fail" = 0 ]; then echo "ALL PASS"; else echo "SOME FAILED"; exit 1; fi
