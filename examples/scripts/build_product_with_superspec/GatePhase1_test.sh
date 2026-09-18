#!/usr/bin/env bash
# ABOUTME: Fixture tests for GatePhase1.sh / lib/gates.sh (#646 5f) — the phase
# ABOUTME: gate runs the shared verify.sh (every stack, project CI gate), writes
# ABOUTME: .ai/gates/<name>.txt, prints the report then the exact PASS/FAIL
# ABOUTME: marker, complexity is a WARNING (gocyclo exit 1 no longer kills the
# ABOUTME: node), coverage is report-only, and a missing verify.sh fails loud.
# ABOUTME: GatePhase2/4 and GateStreamD are the same runner with fewer sections
# ABOUTME: (Go graph test pins their sidecars); FinalGates has its own suite.
set -uo pipefail
DIR="$(cd "$(dirname "$0")" && pwd)"
fail=0
check() { # name expected actual
  if [ "$2" = "$3" ]; then echo "ok: $1"; else echo "FAIL: $1 — want '$2' got '$3'"; fail=1; fi
}
WORK="$(mktemp -d)"
STATE="$(mktemp -d)"
trap 'rm -rf "$WORK" "$STATE"' EXIT
. "$DIR/../build_product/test_helpers.sh"
install_tool_shims
SCRIPT="$(stage_script "$DIR/GatePhase1.sh")"
run() { OUT="$( (cd "$WORK" && PATH="$STATE/bin:$PATH" ${TEST_SH:-sh} "$SCRIPT") 2>"$STATE/stderr")"; RC=$?; }
last() { printf '%s' "$OUT" | tail -1; }
has() { printf '%s' "$OUT" | grep -qF -- "$1" && echo yes || echo no; }
report() { grep -qF -- "$1" "$WORK/.ai/gates/phase1.txt" && echo yes || echo no; }
G() { git -C "$WORK" -c user.name=t -c user.email=t@t "$@"; }
# gocyclo shim: lists one function (exit 1, like the real tool when it finds
# something) when $STATE/cyclo-hits exists.
cat > "$STATE/bin/gocyclo" <<SH
#!/bin/sh
echo "gocyclo \$*" >> "$STATE/calls"
if [ -f "$STATE/cyclo-hits" ]; then echo "12 pkg (F) pkg/f.go:1:1"; exit 1; fi
exit 0
SH
chmod +x "$STATE/bin/gocyclo"

# 1. Missing verify.sh (Setup never ran) → FAIL marker, exit 1.
run
check "no verify.sh: exit 1"            "1" "$RC"
check "no verify.sh: marker"            "phase1-gates-FAIL" "$(last)"
check "no verify.sh: message"           "yes" "$(has '.ai/build/verify.sh missing')"

# Install the runtime gate files the way Setup does, in a Go repo.
mkdir -p "$WORK/.ai/build"
cp "$DIR/lib/verify.sh" "$WORK/.ai/build/verify.sh"; cp "$DIR/lib/ci-probe.sh" "$WORK/.ai/build/ci-probe.sh"
G -c init.defaultBranch=main init -q
echo 'module x' > "$WORK/go.mod"; mkdir -p "$WORK/pkg"; echo 'package pkg' > "$WORK/pkg/a.go"; echo 'package pkg' > "$WORK/pkg/a_test.go"
G add -A; G commit -q -m base

# 2. Green: report written, verify ran build+test+vet, coverage line,
#    complexity 0, PASS marker last, exit 0.
run
check "green: exit 0"                   "0" "$RC"
check "green: marker last"              "phase1-gates-PASS" "$(last)"
check "green: report header"            "yes" "$(report '=== phase1 quality gates ===')"
check "green: verify section"           "yes" "$(report 'build + tests + project CI gate')"
check "green: go build ran"             "yes" "$(calls | grep -q 'go build' && echo yes || echo no)"
check "green: go test ran"              "yes" "$(calls | grep -q 'go test' && echo yes || echo no)"
check "green: go vet ran"               "yes" "$(calls | grep -q 'go vet' && echo yes || echo no)"
check "green: complexity 0"             "yes" "$(report 'Functions over cyclomatic 10: 0')"
check "green: report printed"           "yes" "$(has '=== phase1 quality gates ===')"

# 3. Red tests → FAIL marker, exit 1, report still complete (complexity ran).
set_rc go test 1; rm -f "$STATE/calls"
run
check "red: exit 1"                     "1" "$RC"
check "red: marker last"                "phase1-gates-FAIL" "$(last)"
check "red: complexity still reported"  "yes" "$(report 'Functions over cyclomatic 10')"
check "red: coverage unavailable line"  "yes" "$(report 'coverage: unavailable')"
reset_rc

# 4. gocyclo lists violations (exit 1): a WARNING in the report, gate still
#    PASSES (pre-#646 `gocyclo … >> report` under set -e killed the node).
touch "$STATE/cyclo-hits"; rm -f "$STATE/calls"
run
check "cyclo: exit 0"                   "0" "$RC"
check "cyclo: marker PASS"              "phase1-gates-PASS" "$(last)"
check "cyclo: count 1"                  "yes" "$(report 'Functions over cyclomatic 10: 1')"
check "cyclo: WARNING not failure"      "yes" "$(report 'WARNING: complexity violations (QG-5) — reported, not a gate failure')"
rm -f "$STATE/cyclo-hits"

# 5. Polyglot: a nested Node stack is detected and tested too (the old
#    first-match chain never looked for package.json).
mkdir -p "$WORK/web"; echo '{}' > "$WORK/web/package.json"; G add -A; G commit -q -m web
rm -f "$STATE/calls"
run
check "polyglot: exit 0"                "0" "$RC"
check "polyglot: npm test ran"          "yes" "$(calls | grep -q 'npm test' && echo yes || echo no)"
set_rc npm test 1
run
check "polyglot: red npm → FAIL"        "phase1-gates-FAIL" "$(last)"
reset_rc

# 6. Makefile ci target runs in addition (project CI gate), and its failure
#    is a gate failure.
printf 'ci:\n\t@echo ci\n' > "$WORK/Makefile"; G add -A; G commit -q -m mk
rm -f "$STATE/calls"
run
check "make: ci ran"                    "yes" "$(calls | grep -q 'make -f Makefile ci' && echo yes || echo no)"
set_rc make ci 2
run
check "make: red ci → FAIL"             "phase1-gates-FAIL" "$(last)"
reset_rc

[ "$fail" = 0 ] && echo "ALL PASS" || { echo "SOME FAILED"; exit 1; }
