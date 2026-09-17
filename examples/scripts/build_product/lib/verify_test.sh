#!/usr/bin/env bash
# ABOUTME: Fixture tests for lib/verify.sh — the shared milestone green-gate
# ABOUTME: Setup installs as .ai/build/verify.sh (#406): build + every detected
# ABOUTME: stack's tests (#305) + milestone-scoped go test (#392), known_failures
# ABOUTME: skip, exit-code collapsing (#411), then the ci-probe.sh CI gate.
#
# Driven via PATH shims (test_helpers.sh: install_tool_shims / set_rc / calls).
# The Makefile-target parsing and make-missing cases of the sourced ci-probe.sh
# are lib/ci-probe_test.sh's; these checks were Setup_test.sh's until the
# green-gate moved out of Setup's heredoc.
#   V1 no build system -> tests skipped, no toolchain -> exit 0
#   V2 go: build -> milestone-scoped test (#392) -> vet (#299); ./... fallback
#   V3 #436 LINT_NEW_FROM_REV only with a real base; #441 known_lint_failures
#   V4 known_failures -> `go test -skip A|B` (comments/blanks stripped)
#   V5 every runner exit collapses to 1 (a runner's 2 is NOT make-missing)
#   V6 go build failure aborts (exit 1); V7 sticky multi-stack sweep (#305)
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
install_tool_shims
G() { git -C "$WORK" -c user.name=t -c user.email=t@t "$@"; }
# Fixture: a fresh git repo with the two lib files installed at the runtime
# contract paths, exactly as Setup copies them (Setup_test.sh proves the copy
# is byte-identical). verify.sh sources .ai/build/ci-probe.sh itself.
G -c init.defaultBranch=main init -q
mkdir -p "$WORK/.ai/build" "$WORK/.ai/milestones"
cp "$LIB_DIR/verify.sh" "$WORK/.ai/build/verify.sh"
cp "$LIB_DIR/ci-probe.sh" "$WORK/.ai/build/ci-probe.sh"
verify() { rm -f "$STATE/calls"; VOUT="$( (cd "$WORK" && PATH="$STATE/bin:$PATH" sh .ai/build/verify.sh) 2>&1)"; VRC=$?; }
vhas() { printf '%s' "$VOUT" | grep -qF -- "$1" && echo yes || echo no; }

# V1. No build system, no Makefile.
verify
check "V1 exit 0"                         "0" "$VRC"
check "V1 tests skipped"                  "yes" "$(vhas 'no known build system — skipping tests')"
check "V1 no Makefile info"               "yes" "$(vhas 'no Makefile present — running language-native gates')"
check "V1 no toolchain info"              "yes" "$(vhas 'no recognized toolchain')"
check "V1 no calls"                       "" "$(calls)"

# V2. Go stack, no commits: build, test ./..., vet; golangci-lint absent
#     warning is emitted only when the tool is really missing — with the
#     shim present it runs WITHOUT --new-from-rev (empty-tree base).
touch "$WORK/go.mod"
verify
check "V2 exit 0"                         "0" "$VRC"
check "V2 call order"                     "go build ./...;go test ./...;go vet ./...;golangci-lint run" "$(calls)"
check "V2 ./... fallback message"         "yes" "$(vhas 'no changed Go packages in milestone range — testing ./...')"
check "V2 no lint scoping w/o base"       "no" "$(vhas '--new-from-rev')"

# V3. #392 milestone scoping: commit A (base), then commit B touching
#     pkg/a/a.go and root main.go → targets `. ./pkg/a`; #436 lint scoped to
#     the base; #441 known_lint_failures → --exclude (comments stripped).
#
#     KNOWN-BUG (portability): verify.sh derives the package list with
#       awk -F/ '... sub(/\/[^/]*$/, "") ...'
#     The unescaped `/` inside the bracket expression of a regex LITERAL is
#     accepted by gawk (and mawk >= 1.3.4, Ubuntu's default), but BSD/macOS
#     awk terminates the literal at it ("nonterminated character class") —
#     so on macOS the scoping SILENTLY degrades to `go test ./...` with an
#     awk error interleaved in the node output, and #392's "only the changed
#     packages" contract does not hold. `[^\/]` would be portable. The
#     suite probes the local awk and asserts whichever behaviour applies so
#     the run is deterministic on both platforms; when the script is fixed
#     the BSD branch becomes unreachable and can be deleted.
if printf 'a/b.go\n' | awk -F/ 'NF>1 { sub(/\/[^/]*$/, ""); print "./" $0 }' 2>/dev/null | grep -qx './a'; then
  AWK_OK=1; SCOPED_TARGET=". ./pkg/a"; echo "info: awk accepts [^/] in a regex literal — asserting #392 scoping"
else
  AWK_OK=0; SCOPED_TARGET="./..."; echo "info: BSD awk — asserting the KNOWN-BUG ./... fallback"
fi
mkdir -p "$WORK/pkg/a"; echo base > "$WORK/README.md"; G add -A; G commit -q -m A
printf '%s\n' "$(G rev-parse HEAD)" > "$WORK/.ai/build/milestone-start-sha"
echo 'package a' > "$WORK/pkg/a/a.go"; echo 'package main' > "$WORK/main.go"
G add -A; G commit -q -m B
BASE_SHA="$(cat "$WORK/.ai/build/milestone-start-sha")"
printf '# operator note\n\nSA1019\nG404\n' > "$WORK/.ai/milestones/known_lint_failures"
verify
check "V3 exit 0"                         "0" "$VRC"
check "V3 go test target"                 "yes" "$(printf '%s' "$(calls)" | grep -qF "go test $SCOPED_TARGET" && echo yes || echo no)"
if [ "$AWK_OK" = 1 ]; then
  check "V3 scoped message"               "yes" "$(vhas 'milestone-scoped go test: . ./pkg/a')"
  check "V3 no awk error"                 "no"  "$(vhas 'nonterminated character class')"
else
  check "V3 KNOWN-BUG awk error surfaced" "yes" "$(vhas 'nonterminated character class')"
  check "V3 KNOWN-BUG fell back to ./..." "yes" "$(vhas 'no changed Go packages in milestone range')"
fi
check "V3 lint --new-from-rev base"       "yes" "$(printf '%s' "$(calls)" | grep -q "golangci-lint run --new-from-rev $BASE_SHA --exclude SA1019 --exclude G404" && echo yes || echo no)"
# KNOWN-BUG (#441 hatch): a WHITESPACE-ONLY line in known_lint_failures is
# not stripped — the `case ''|\#*` guard only catches truly empty and comment
# lines — so it becomes a dangling `--exclude` whose VALUE is the next
# `--exclude` flag, and the real pattern after it (G404) is demoted to a
# positional path argument. CLAUDE.md's "strip comments AND blank lines"
# rule intends `[[:space:]]*` blanks too. When fixed, expect no
# `--exclude --exclude` and G404 excluded.
printf 'SA1019\n  \nG404\n' > "$WORK/.ai/milestones/known_lint_failures"
verify
check "V3c KNOWN-BUG dangling --exclude (want no)" "yes" "$(printf '%s' "$(calls)" | grep -qF -- '--exclude SA1019 --exclude --exclude G404' && echo yes || echo no)"
# Unreachable start SHA → empty-tree base → all packages, no lint scoping.
echo deadbeefdeadbeefdeadbeefdeadbeefdeadbeef > "$WORK/.ai/build/milestone-start-sha"
rm -f "$WORK/.ai/milestones/known_lint_failures"
verify
check "V3b unreachable base exit 0"       "0" "$VRC"
check "V3b no --new-from-rev"             "no" "$(printf '%s' "$(calls)" | grep -q -- '--new-from-rev' && echo yes || echo no)"
check "V3b all pkgs from empty tree"      "yes" "$(printf '%s' "$(calls)" | grep -qF "go test $SCOPED_TARGET" && echo yes || echo no)"
rm -f "$WORK/.ai/build/milestone-start-sha"

# V4. #known_failures → -skip regex with comments and blank lines stripped.
printf '# expected to fail until m3\nTestStream\n\nTestFlaky\n' > "$WORK/.ai/milestones/known_failures"
verify
check "V4 -skip pattern"                  "yes" "$(printf '%s' "$(calls)" | grep -qF "go test $SCOPED_TARGET -skip TestStream|TestFlaky" && echo yes || echo no)"
check "V4 skip message"                   "yes" "$(vhas 'skipping known failures: TestStream|TestFlaky')"
rm -f "$WORK/.ai/milestones/known_failures"

# V5. A test runner exiting 2 collapses to exit 1 (never the make-missing 2);
#     later gates still run.
set_rc go test 2
verify
check "V5 runner rc2 -> 1"                "1" "$VRC"
check "V5 vet still ran"                  "yes" "$(printf '%s' "$(calls)" | grep -q 'go vet' && echo yes || echo no)"
reset_rc
# go vet failure (language-native gate) also collapses to 1.
set_rc go vet 3
verify
check "V5b vet fail -> 1"                 "1" "$VRC"
reset_rc

# V6. go build failure aborts immediately (unguarded): no test, no vet.
set_rc go build 1
verify
check "V6 build fail exit 1"              "1" "$VRC"
check "V6 nothing after build"            "go build ./..." "$(calls)"
reset_rc

# V7. #305 sticky sweep: go test fails, npm passes → exit 1, both ran.
touch "$WORK/package.json"
set_rc go test 1
verify
check "V7 exit 1"                         "1" "$VRC"
check "V7 npm still ran"                  "yes" "$(printf '%s' "$(calls)" | grep -q 'npm test' && echo yes || echo no)"
check "V7 plain-JS: tsc skipped"          "yes" "$(vhas 'no tsconfig.json — skipping tsc')"
check "V7 plain-JS: eslint skipped"       "yes" "$(vhas 'no eslint config — skipping eslint')"
reset_rc; rm -f "$WORK/package.json"

if [ "$fail" = 0 ]; then echo "ALL PASS"; else echo "SOME FAILED"; exit 1; fi
