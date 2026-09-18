#!/usr/bin/env bash
# ABOUTME: Fixture tests for lib/verify.sh — the shared milestone green-gate
# ABOUTME: Setup installs as .ai/build/verify.sh (#406): stack detection anywhere
# ABOUTME: in the tree (#640 D1), worktree+dependents Go scope (#640 D2/D3/D9),
# ABOUTME: anchored known_failures skip (#640 D7), --final ship mode, exit collapse.
#
# Driven via PATH shims (test_helpers.sh: install_tool_shims / set_rc / calls /
# argv_has) for the offline sections and the REAL `go` toolchain for the
# scoping sections. The Makefile-target parsing, make-missing marker and lint
# hatch cases of the sourced ci-probe.sh are lib/ci-probe_test.sh's.
#   V1 no stack → milestone exit 3 NOT-YET-VERIFIABLE (tracker-runner #857),
#      --final exit 1, unless .ai/build/no-tests-ok (NOTE, exit 0)
#   V2 go: build → test -v ./... → vet (#299); zero executed tests = exit 3
#      (tracker-runner #873: the oracle is a POSITIVE `=== RUN` count)
#   V3 #392/#436 scoping + LINT_NEW_FROM_REV only with a real base; BSD awk (E1)
#   V4 known_failures → anchored `-skip` (#640 D7); invalid regex fails closed
#   V5 every runner exit collapses to 1; a red native lint gate is ADVISORY
#      (exit 0 + ADVISORY line); V6 go build failure aborts the stack
#   V7 sticky multi-stack sweep (#305); V8 nested stacks run in their own dir,
#      go.work subsumes nested go.mod, node_modules/vendor/testdata excluded
#   V9 --final: -count=1, known_failures ignored + listed, zero tests = FAIL,
#      elapsed per stack; V10 missing ci-probe.sh → exit 1 with a message
#   V11 JS/Rust reporter counts (jest/vitest/mocha/TAP; cargo summed) — an
#      unrecognized reporter is not an oracle
#   V12 python: manifest-free discovery, interpreter chain (.venv → venv →
#      pytest → uv [--frozen]), -k deselection from known_failures, exit 5
#   V13 executed-test manifest .ai/build/executed-tests.txt (tracker-runner
#      #901): one test name per line per stack (Go `=== RUN` incl. subtests,
#      cargo `test x ... ok|FAILED`, pytest -rA `PASSED|FAILED|ERROR nodeid`,
#      jest/vitest/mocha ✓/✕ titles), rewritten every run, the count and the
#      names come from the SAME parse
#   R1-R4 real go: uncommitted+untracked scope, reverse deps, non-package
#      filter, -skip anchoring semantics
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
printf '.ai/\n' > "$WORK/.gitignore"
cp "$LIB_DIR/verify.sh" "$WORK/.ai/build/verify.sh"
cp "$LIB_DIR/ci-probe.sh" "$WORK/.ai/build/ci-probe.sh"
# TEST_SH=dash runs verify.sh under dash (TestMilestone runs it via `sh`).
verify() { rm -f "$STATE/calls" "$STATE/argv"; VOUT="$( (cd "$WORK" && PATH="$STATE/bin:$PATH" "${TEST_SH:-sh}" .ai/build/verify.sh "$@") 2>&1)"; VRC=$?; }
vhas() { printf '%s' "$VOUT" | grep -qF -- "$1" && echo yes || echo no; }
chas() { printf '%s' "$(calls)" | grep -qF -- "$1" && echo yes || echo no; }
TAB=$'\t'

# V1. No build system (#640 D1, tracker-runner #857): milestone mode is
#     NOT-YET-VERIFIABLE (exit 3 — a scaffolding/docs milestone has no runner
#     yet; the outcome is neither green nor a failure and VerifyMilestone
#     judges it), --final FAILS; neither prints the opt-out command or path
#     (the fix agent reads this output). A Makefile ci/check/lint/test target
#     counts as a stack. The operator stamp passes both modes with a NOTE.
verify
check "V1 milestone: exit 3"              "3" "$VRC"
check "V1 milestone: loud NOTE"           "yes" "$(vhas 'NOTE: no build system detected — nothing was tested this milestone')"
check "V1 milestone: verdict line"        "yes" "$(vhas 'NOT-YET-VERIFIABLE: no runnable test suite and no project CI target detected.')"
check "V1 milestone: verdict is last"     "yes" "$(printf '%s' "$VOUT" | tail -1 | grep -q 'Cargo.toml) must be added so the suite becomes runnable' && echo yes || echo no)"
check "V1 no calls"                       "" "$(calls)"
check "V1 milestone: no touch command"    "no"  "$(vhas 'touch ')"
check "V1 milestone: stamp not printed"   "no"  "$(vhas 'no-tests-ok')"
verify --final
check "V1 final: exit 1"                  "1" "$VRC"
check "V1 final: error"                   "yes" "$(vhas 'ERROR: no build system detected')"
check "V1 no touch command"               "no"  "$(vhas 'touch ')"
check "V1 stamp path not printed"         "no"  "$(vhas 'no-tests-ok')"
printf 'test:\n\techo t\n' > "$WORK/Makefile"
verify --final
check "V1b Makefile test target = stack"  "0" "$VRC"
check "V1b Makefile NOTE"                 "yes" "$(vhas 'the Makefile test target is the only test runner')"
check "V1b make test ran"                 "yes" "$(chas 'make -f Makefile test')"
verify
check "V1b milestone: make test = oracle" "0" "$VRC"
# A Makefile-only oracle lists no names: the manifest says so (a declared
# contract test cannot be proven from it — the verifier decides).
check "V1b Makefile-only: names-unavailable" "yes" "$(grep -q '^# names-unavailable' "$WORK/.ai/build/executed-tests.txt" && echo yes || echo no)"
check "V1b Makefile-only: names the target" "yes" "$(grep -q 'Makefile test target was the oracle' "$WORK/.ai/build/executed-tests.txt" && echo yes || echo no)"
rm -f "$WORK/Makefile"
touch "$WORK/.ai/build/no-tests-ok"
verify --final
check "V1c opt-out final exit 0"          "0" "$VRC"
check "V1c opt-out NOTE"                  "yes" "$(vhas 'NOTE: no build system detected and the operator opt-out stamp is present')"
check "V1c no toolchain info"             "yes" "$(vhas 'no recognized toolchain')"
verify
check "V1c opt-out milestone exit 0"      "0" "$VRC"
check "V1c opt-out milestone: no verdict" "no"  "$(vhas 'NOT-YET-VERIFIABLE')"
rm -f "$WORK/.ai/build/no-tests-ok"

# V2. Go stack, no commits: build, `test -v ./...` (verbose so executed tests
#     are countable), vet; the golangci-lint shim is present so it runs
#     WITHOUT --new-from-rev (empty-tree base). The shim's default output has
#     one `=== RUN`, so the suite counts as an oracle → exit 0. A run that
#     executes ZERO tests (no `=== RUN` — tracker-runner #873: `go test`
#     exits 0 on an empty suite) is NOT green: exit 3, with the NOTE.
touch "$WORK/go.mod"
verify
check "V2 exit 0"                         "0" "$VRC"
check "V2 build before test"              "yes" "$(printf '%s' "$(calls)" | grep -q 'go build ./...;.*go test -v ./...' && echo yes || echo no)"
check "V2 vet then lint after tests"      "yes" "$(printf '%s' "$(calls)" | grep -q 'go test -v ./...;.*go vet ./...;golangci-lint version;golangci-lint run$' && echo yes || echo no)"
check "V2 ./... fallback message"         "yes" "$(vhas 'no changed Go files in milestone range — testing ./...')"
check "V2 no lint scoping w/o base"       "no" "$(vhas '--new-from-rev')"
check "V2 stack header names dir"         "yes" "$(vhas '=== stack: go in . ===')"
check "V2 test output surfaced"           "yes" "$(vhas '=== RUN   TestShim')"
MANIFEST="$WORK/.ai/build/executed-tests.txt"
mhas() { grep -qxF -- "$1" "$MANIFEST" 2>/dev/null && echo yes || echo no; }
check "V2 manifest: go stack header"      "yes" "$(grep -q '^# executed tests (stack: go in \.) — from verify.sh run 20' "$MANIFEST" && echo yes || echo no)"
check "V2 manifest: TestShim listed"      "yes" "$(mhas TestShim)"
set_out go list-tests ""
set_out go test ""
verify
check "V2b zero tests: exit 3"            "3" "$VRC"
check "V2b zero tests: loud NOTE"         "yes" "$(vhas 'NOTE: no Go test files in scope')"
check "V2b zero tests: verdict"           "yes" "$(vhas 'NOT-YET-VERIFIABLE')"
reset_rc
# Test files exist but the -skip / build tags leave nothing to run: exit 3.
set_out go test "PASS
ok  	fx	0.001s"
verify
check "V2c no === RUN: exit 3"            "3" "$VRC"
reset_rc
# A red run that ran tests is a plain failure (exit 1), never exit 3.
set_out go test "=== RUN   TestX
--- FAIL: TestX (0.00s)
FAIL"
set_rc go test 1
verify
check "V2d red run: exit 1"               "1" "$VRC"
check "V2d red run: no verdict"           "no"  "$(vhas 'NOT-YET-VERIFIABLE')"
reset_rc

# V3. #392 milestone scoping: commit A (base), then commit B touching
#     pkg/a/a.go and root main.go → targets `. ./pkg/a` (the shim echoes the
#     dirs back as import paths); #436 lint scoped to the base. #640 E1: the
#     old awk `[^/]` broke on BSD awk and silently degraded to ./... — now
#     there is no awk in the path, so the SAME assertion holds on every
#     platform.
mkdir -p "$WORK/pkg/a"; echo base > "$WORK/README.md"; G add -A; G commit -q -m A
printf '%s\n' "$(G rev-parse HEAD)" > "$WORK/.ai/build/milestone-start-sha"
echo 'package a' > "$WORK/pkg/a/a.go"; echo 'package main' > "$WORK/main.go"
G add -A; G commit -q -m B
BASE_SHA="$(cat "$WORK/.ai/build/milestone-start-sha")"
verify
check "V3 exit 0"                         "0" "$VRC"
check "V3 go test target"                 "yes" "$(chas 'go test -v . ./pkg/a')"
check "V3 scoped message"                 "yes" "$(vhas 'milestone-scoped go test (2 package(s), 0 via reverse deps): . ./pkg/a')"
check "V3 no awk error (E1)"              "no"  "$(vhas 'nonterminated character class')"
check "V3 lint --new-from-rev base"       "yes" "$(chas "golangci-lint run --new-from-rev $BASE_SHA")"
# Unreachable start SHA → empty-tree base → all packages, no lint scoping.
echo deadbeefdeadbeefdeadbeefdeadbeefdeadbeef > "$WORK/.ai/build/milestone-start-sha"
verify
check "V3b unreachable base exit 0"       "0" "$VRC"
check "V3b no --new-from-rev"             "no" "$(chas '--new-from-rev')"
check "V3b all pkgs from empty tree"      "yes" "$(chas 'go test -v . ./pkg/a')"
rm -f "$WORK/.ai/build/milestone-start-sha"

# V4. known_failures → `-skip` with every entry anchored per path segment
#     (#640 D7): comments / blank / whitespace-only lines stripped, a
#     leading `-` rejected, `TestA/sub` → `^TestA$/^sub$`, the whole pattern
#     ONE argument. An invalid regex fails closed.
printf '# expected to fail until m3\r\nTestStream\n  \n\nTestFlaky/sub \n-run\n' > "$WORK/.ai/milestones/known_failures"
verify
check "V4 exit 0"                         "0" "$VRC"
check "V4 -skip anchored, one arg"        "yes" "$(argv_has "go${TAB}test${TAB}-v${TAB}.${TAB}./pkg/a${TAB}-skip${TAB}^TestStream\$|^TestFlaky\$/^sub\$")"
check "V4 skip message"                   "yes" "$(vhas 'skipping known failures: ^TestStream$|^TestFlaky$/^sub$')"
check "V4 leading dash rejected"          "yes" "$(vhas "WARNING: ignoring known_failures entry '-run'")"
printf 'TestOk\nTest(Broken\n' > "$WORK/.ai/milestones/known_failures"
verify
check "V4b invalid regex exit 1"          "1" "$VRC"
check "V4b invalid regex named"           "yes" "$(vhas "ERROR: known_failures entry 'Test(Broken' is not a valid regular expression")"
check "V4b nothing ran"                   "" "$(calls)"
rm -f "$WORK/.ai/milestones/known_failures"

# V5. A test runner exiting 2 collapses to exit 1; later gates still run.
#     A red language-native gate (vet / golangci-lint) is ADVISORY
#     (tracker-runner convergence): exit 0, the finding is printed, and one
#     ADVISORY line names the policy. A red Makefile target still blocks
#     (lib/ci-probe_test.sh V8).
set_rc go test 2
verify
check "V5 runner rc2 -> 1"                "1" "$VRC"
check "V5 vet still ran"                  "yes" "$(chas 'go vet')"
reset_rc
set_rc go vet 3
set_out go vet "./x.go:3:2: unreachable code"
verify
check "V5b vet red is advisory: exit 0"   "0" "$VRC"
check "V5b vet finding printed"           "yes" "$(vhas './x.go:3:2: unreachable code')"
check "V5b ADVISORY line"                 "yes" "$(vhas 'ADVISORY: one or more language-native lint/type-check gates reported findings (non-blocking')"
reset_rc
set_rc golangci-lint run 1
verify
check "V5c lint red is advisory: exit 0"  "0" "$VRC"
check "V5c ADVISORY line"                 "yes" "$(vhas 'ADVISORY:')"
reset_rc
verify
check "V5d clean: no ADVISORY line"       "no"  "$(vhas 'ADVISORY:')"

# V6. go build failure aborts THAT stack (no test), but the CI gate still
#     reports; exit 1.
set_rc go build 1
verify
check "V6 build fail exit 1"              "1" "$VRC"
check "V6 no go test after build fail"    "no" "$(chas 'go test')"
reset_rc

# V7. #305 sticky sweep: go test fails, npm passes → exit 1, both ran.
touch "$WORK/package.json"
set_rc go test 1
verify
check "V7 exit 1"                         "1" "$VRC"
check "V7 npm still ran"                  "yes" "$(chas 'npm test')"
check "V7 plain-JS: tsc skipped"          "yes" "$(vhas 'no tsconfig.json in . — skipping tsc')"
check "V7 plain-JS: eslint skipped"       "yes" "$(vhas 'no eslint config in . — skipping eslint')"
reset_rc; rm -f "$WORK/package.json"

# V8. #640 D1 nested stacks: backend/go.mod + frontend/package.json with NO
#     root manifest each run in their own directory; a Cargo.toml under
#     vendor/, a package.json under node_modules/ and a go.mod under
#     testdata/ are ignored; go.work at the root subsumes mod/go.mod.
rm -f "$WORK/go.mod"
#     A SECOND-LEVEL services/api/go.mod is a stack too (fail-open fixed
#     alongside tracker-runner #901: the unquoted `*/go.mod` pathspec was
#     shell-globbed to backend/go.mod and the deeper manifest was dropped
#     whenever a first-level go.mod existed), and a hook-created
#     backend/.venv/ tree is never a python stack.
mkdir -p "$WORK/backend/.venv/lib/site-packages/dep" "$WORK/services/api" "$WORK/frontend" "$WORK/vendor/x" "$WORK/node_modules/y" "$WORK/testdata/z"
touch "$WORK/backend/go.mod" "$WORK/services/api/go.mod" "$WORK/backend/.venv/lib/site-packages/dep/pyproject.toml" "$WORK/frontend/package.json" "$WORK/vendor/x/Cargo.toml" "$WORK/node_modules/y/package.json" "$WORK/testdata/z/go.mod"
# Shims log their cwd-relative dir via the stack header; prove cwd by a
# shim that records $PWD.
cat > "$STATE/bin/npm" <<SHIM
#!/bin/sh
echo "npm \$* in \${PWD##*/}" >> "$STATE/calls"
exit 0
SHIM
verify
check "V8 exit 0"                         "0" "$VRC"
check "V8 backend go stack"               "yes" "$(vhas '=== stack: go in backend ===')"
check "V8 frontend npm runs in frontend"  "yes" "$(chas 'npm test in frontend')"
check "V8 no cargo (vendor/ excluded)"    "no"  "$(chas 'cargo')"
check "V8 no testdata go stack"           "no"  "$(vhas 'stack: go in testdata/z')"
check "V8 no node_modules stack"          "no"  "$(vhas 'stack: npm in node_modules')"
check "V8 untracked manifests detected"   "yes" "$(vhas 'stack: npm in frontend')"
check "V8 two-level go stack detected"    "yes" "$(vhas '=== stack: go in services/api ===')"
check "V8 both go stacks built"           "2" "$(printf '%s\n' "$(calls)" | tr ';' '\n' | grep -c '^go build ./...$')"
check "V8 both go stacks tested"          "2" "$(printf '%s\n' "$(calls)" | tr ';' '\n' | grep -c '^go test -v')"
check "V8 .venv pyproject not a stack"    "no"  "$(vhas 'stack: python')"
mkdir -p "$WORK/mod"; touch "$WORK/go.work" "$WORK/mod/go.mod"
verify
check "V8b go.work root runs"             "yes" "$(vhas '=== stack: go in . ===')"
check "V8b go.work subsumes mod/go.mod"   "no"  "$(vhas 'stack: go in mod')"
check "V8b root go.work subsumes backend"  "no"  "$(vhas 'stack: go in backend')"
rm -rf "$WORK/backend" "$WORK/services" "$WORK/frontend" "$WORK/vendor" "$WORK/node_modules" "$WORK/testdata" "$WORK/mod" "$WORK/go.work"
install_tool_shims

# V9. --final ship mode (#640 D7/D12/D13): whole tree with -count=1, no
#     -skip and the still-listed known_failures printed, per-stack elapsed
#     line; a Go tree with zero test files FAILS.
touch "$WORK/go.mod"
printf 'TestStillListed\n' > "$WORK/.ai/milestones/known_failures"
verify --final
check "V9 exit 0"                         "0" "$VRC"
check "V9 -count=1 whole tree"            "yes" "$(argv_has "go${TAB}test${TAB}-v${TAB}-count=1${TAB}./...")"
check "V9 no -skip"                       "no"  "$(chas '-skip')"
check "V9 known_failures listed"          "yes" "$(vhas 'still listed: TestStillListed')"
check "V9 ignore notice"                  "yes" "$(vhas 'known_failures is IGNORED by the ship gate')"
check "V9 elapsed per stack"              "yes" "$(printf '%s' "$VOUT" | grep -qE '^=== stack: go in \. — [0-9]+s, PASS ===$' && echo yes || echo no)"
check "V9 no lint scoping in final"       "no"  "$(chas '--new-from-rev')"
set_out go list-tests ""
verify --final
check "V9b zero tests: exit 1"            "1" "$VRC"
check "V9b zero tests: error"             "yes" "$(vhas 'ERROR: no Go test files in ANY Go stack')"
reset_rc
# Test files exist but nothing executed (no `=== RUN`): the ship gate is red
# too — no oracle ran — unless the operator stamp says the product is
# test-free.
set_out go test ""
verify --final
check "V9c no executed tests: exit 1"     "1" "$VRC"
check "V9c no executed tests: error"      "yes" "$(vhas 'ERROR: no runnable oracle — no language test suite executed any test')"
check "V9c stamp path not printed"        "no"  "$(vhas 'no-tests-ok')"
touch "$WORK/.ai/build/no-tests-ok"
verify --final
check "V9d stamp: exit 0"                 "0" "$VRC"
check "V9d stamp: NOTE"                   "yes" "$(vhas 'NOTE: no runnable oracle ran and the operator opt-out stamp is present')"
rm -f "$WORK/.ai/build/no-tests-ok"
reset_rc; rm -f "$WORK/.ai/milestones/known_failures"

# V10. Missing ci-probe.sh (#640 E8): a clear message and exit 1 — never a
#      bare `sh` rc that could be mistaken for anything else.
mv "$WORK/.ai/build/ci-probe.sh" "$STATE/ci-probe.bak"
verify
check "V10 missing probe exit 1"          "1" "$VRC"
check "V10 missing probe message"         "yes" "$(vhas 'ERROR: .ai/build/ci-probe.sh missing')"
mv "$STATE/ci-probe.bak" "$WORK/.ai/build/ci-probe.sh"

# V11. JS / Rust executed-test counts (tracker-runner #873). The oracle is
#      the reporter's count: jest / vitest / mocha / TAP are parsed (max
#      match); an unrecognized reporter yields 0 → exit 3; a red run stays
#      exit 1. Rust sums every binary's `test result:` summary.
rm -f "$WORK/go.mod"; touch "$WORK/package.json"
for rep in 'Tests:       2 failed, 3 passed, 5 total' 'Tests  3 passed (3)' 'Tests  2 failed | 1 passed (3)' '  3 passing (12ms)' '# tests 4'; do
  set_out npm test "$rep"
  verify
  check "V11 npm reporter counted: $rep"  "0" "$VRC"
done
set_out npm test "> app@1.0.0 test
> echo no tests here"
verify
check "V11 unrecognized reporter: exit 3" "3" "$VRC"
set_out npm test "Tests:       0 total"
verify
check "V11 jest zero total: exit 3"       "3" "$VRC"
set_out npm test "Tests:       1 failed, 1 total"
set_rc npm test 1
verify
check "V11 jest red: exit 1"              "1" "$VRC"
reset_rc
# Per-test names come from the ✓/✕ lines when the reporter prints them
# (jest verbose / vitest / mocha); a summary-only reporter leaves the
# section empty and SAYS so.
set_out npm test "  ✓ adds two numbers (2 ms)
  ✕ subtracts (1 ms)
 ✓ src/calc.test.ts > calc > multiplies 3ms
Tests:       1 failed, 2 passed, 3 total"
verify
check "V11 npm manifest: ✓ title"         "yes" "$(mhas 'adds two numbers')"
check "V11 npm manifest: ✕ title"         "yes" "$(mhas 'subtracts')"
check "V11 npm manifest: vitest title"    "yes" "$(mhas 'src/calc.test.ts > calc > multiplies')"
# The same parse under a C locale (a tool subprocess may not carry the
# operator's UTF-8 locale): the multibyte marks are stripped whole.
VOUT="$( (cd "$WORK" && LC_ALL=C LANG=C PATH="$STATE/bin:$PATH" "${TEST_SH:-sh}" .ai/build/verify.sh) 2>&1)"; VRC=$?
check "V11 npm manifest under LC_ALL=C: ✓ title" "yes" "$(mhas 'adds two numbers')"
check "V11 npm manifest under LC_ALL=C: ✕ title" "yes" "$(mhas 'subtracts')"
check "V11 npm manifest under LC_ALL=C: no mangled prefix" "3" "$(grep -vc '^#' "$MANIFEST")"
set_out npm test "Tests:       3 passed, 3 total"
verify
check "V11 npm summary-only: counted"     "0" "$VRC"
check "V11 npm summary-only: manifest note" "yes" "$(grep -q '^# (no per-test names parsed from the npm reporter output — 3 test(s) counted' "$MANIFEST" && echo yes || echo no)"
check "V11 npm summary-only: names-unavailable marker" "yes" "$(grep -q '^# names-unavailable' "$MANIFEST" && echo yes || echo no)"
# vitest's default reporter prints per-FILE lines `✓ src/calc.test.ts (3
# tests) 5ms` — a file, not a test name: dropped, so the section is empty
# and self-describes as names-unavailable.
set_out npm test " ✓ src/calc.test.ts (3 tests) 5ms
 ✓ src/io.test.ts (1 test) 2ms
 Test Files  2 passed (2)
      Tests  4 passed (4)"
verify
check "V11 vitest per-file: counted"      "0" "$VRC"
check "V11 vitest per-file: not a name"   "no"  "$(grep -q 'calc.test.ts' "$MANIFEST" && echo yes || echo no)"
check "V11 vitest per-file: no names"     "0" "$(grep -vc '^#' "$MANIFEST")"
check "V11 vitest per-file: names-unavailable" "yes" "$(grep -q '^# names-unavailable' "$MANIFEST" && echo yes || echo no)"
# When names ARE listed there is no marker.
set_out npm test "  ✓ adds (1 ms)
Tests:       1 passed, 1 total"
verify
check "V11 npm named: no names-unavailable" "no" "$(grep -q '^# names-unavailable' "$MANIFEST" && echo yes || echo no)"
reset_rc; rm -f "$WORK/package.json"
touch "$WORK/Cargo.toml"
verify
check "V11 cargo default: exit 0"         "0" "$VRC"
set_out cargo test "running 0 tests

test result: ok. 0 passed; 0 failed; 0 ignored; 0 measured; 0 filtered out"
verify
check "V11 cargo zero passed: exit 3"     "3" "$VRC"
# The count is the number of `test <name> ... ok|FAILED` lines (the same
# parse that names them in the manifest); a summary line alone counts
# nothing, and an `ignored` test did not execute.
set_out cargo test "running 1 test
test result: ok. 0 passed; 0 failed
running 3 tests
test inspector::test_inspect_contract ... ok
test util::test_helper ... ok
test slow::test_skipped ... ignored
test result: ok. 2 passed; 0 failed; 1 ignored"
verify
check "V11 cargo summed across binaries"  "0" "$VRC"
check "V11 cargo manifest: names"         "yes" "$( [ "$(mhas inspector::test_inspect_contract)" = yes ] && [ "$(mhas util::test_helper)" = yes ] && echo yes || echo no)"
check "V11 cargo manifest: ignored absent" "no"  "$(mhas slow::test_skipped)"
# `#[should_panic]` prints `test tests::it_panics - should panic ... ok`:
# the suffix is not part of the name.
set_out cargo test "test tests::it_panics - should panic ... ok
test tests::it_panics_msg - should panic with \"boom\" ... ok
test result: ok. 2 passed; 0 failed"
verify
check "V11 cargo should_panic: name only" "yes" "$(mhas tests::it_panics)"
check "V11 cargo should_panic with msg: name only" "yes" "$(mhas tests::it_panics_msg)"
check "V11 cargo should_panic: no suffix" "no"  "$(grep -q 'should panic' "$MANIFEST" && echo yes || echo no)"
check "V11 cargo manifest: stack header"  "yes" "$(grep -q '^# executed tests (stack: cargo in \.)' "$MANIFEST" && echo yes || echo no)"
set_out cargo test "test result: ok. 2 passed; 0 failed"
verify
check "V11 cargo summary w/o test lines: exit 3" "3" "$VRC"
reset_rc; rm -f "$WORK/Cargo.toml"

# V12. Python (tracker-runner #857 / fix sets #3 #5). Interpreter chain:
#      .venv/bin/python -m pytest → venv/bin/python -m pytest → pytest →
#      uv run [--frozen] pytest. A manifest (pyproject.toml) always attempts
#      the run; a MANIFEST-FREE suite (test_*.py / *_test.py on disk, no
#      pyproject.toml) runs only when a runner is importable. known_failures
#      become `-k "not (A or B)"` (Go subtest `/` entries and other non-name
#      characters excluded). pytest exit 5 (nothing collected) is not an
#      oracle (exit 3), never a failure.
touch "$WORK/pyproject.toml"
verify
check "V12 manifest: PATH pytest chosen"  "yes" "$(chas 'pytest')"
check "V12 manifest: not uv (pytest on PATH)" "no" "$(chas 'uv run')"
check "V12 manifest: exit 0"              "0" "$VRC"
check "V12 chain banner"                  "yes" "$(vhas '--- pytest (.) ---')"
# Known failures → -k, one argument; a Go subtest entry is Go-only.
printf 'TestStream
TestFlaky/sub
Test(Broken
' > "$WORK/.ai/milestones/known_failures"
verify
check "V12 invalid Go regex still fails"  "1" "$VRC"
printf 'TestStream
TestFlaky/sub
test_slow
' > "$WORK/.ai/milestones/known_failures"
verify
check "V12 -k not (...) one argument"     "yes" "$(argv_has "pytest${TAB}-rA${TAB}-k${TAB}not (TestStream or TestFlaky/sub or test_slow)")"
check "V12 -k banner"                     "yes" "$(vhas 'skipping known failures (pytest -k): not (TestStream or TestFlaky/sub or test_slow)')"
printf 'TestOk
bad name
weird:thing
' > "$WORK/.ai/milestones/known_failures"
verify
check "V12 space entry ignored (both)"    "yes" "$(vhas "WARNING: ignoring known_failures entry 'bad name'")"
check "V12 -k keeps colon entry"          "yes" "$(argv_has "pytest${TAB}-rA${TAB}-k${TAB}not (TestOk or weird:thing)")"
# A bare `and` / `or` / `not` entry is a -k expression KEYWORD: it would
# form `-k "not (not)"` → pytest exit 4 (usage error) → a red with no
# WARNING naming the cause. Applied to Go only (where it is a harmless
# anchored regex), with the WARNING.
printf 'TestOk\nnot\nand\nor\ntest_real\n' > "$WORK/.ai/milestones/known_failures"
verify
check "V12 -k keyword entries excluded"   "yes" "$(argv_has "pytest${TAB}-rA${TAB}-k${TAB}not (TestOk or test_real)")"
check "V12 -k keyword WARNING (not)"      "yes" "$(vhas "WARNING: known_failures entry 'not' is a pytest -k expression keyword — applied to Go only")"
check "V12 -k keyword WARNING (and)"      "yes" "$(vhas "WARNING: known_failures entry 'and' is a pytest -k expression keyword")"
check "V12 -k keyword not rejected for Go" "no"  "$(vhas "ignoring known_failures entry 'not'")"
check "V12 -k keyword: exit 0"            "0" "$VRC"
rm -f "$WORK/.ai/milestones/known_failures"
# -rA is always passed (the short test summary names every executed test);
# PASSED / FAILED / ERROR nodeids become the manifest.
verify
check "V12 -rA without known_failures"    "yes" "$(argv_has "pytest${TAB}-rA")"
set_out pytest none "tests/test_slug.py ..F
=========================== short test summary info ============================
PASSED tests/test_slug.py::test_basic
PASSED tests/test_slug.py::test_params[a-b]
PASSED tests/test_slug.py::test_params[a - b]
FAILED tests/test_slug.py::test_edge - AssertionError: boom
FAILED tests/test_slug.py::test_lists[x] - assert [1] == [2]
XFAIL tests/test_slug.py::test_known_bad - reason: #12
XPASS tests/test_slug.py::test_surprise
ERROR tests/test_conf.py::test_setup - fixture 'db' not found"
set_rc pytest none 1
verify
check "V12 pytest red: exit 1"            "1" "$VRC"
check "V12 pytest manifest: PASSED"       "yes" "$(mhas 'tests/test_slug.py::test_basic')"
check "V12 pytest manifest: param id"     "yes" "$(mhas 'tests/test_slug.py::test_params[a-b]')"
check "V12 pytest manifest: FAILED (no reason)" "yes" "$(mhas 'tests/test_slug.py::test_edge')"
check "V12 pytest manifest: ERROR"        "yes" "$(mhas 'tests/test_conf.py::test_setup')"
check "V12 pytest manifest: param id with ' - '" "yes" "$(mhas 'tests/test_slug.py::test_params[a - b]')"
check "V12 pytest manifest: reason with ]"  "yes" "$(mhas 'tests/test_slug.py::test_lists[x]')"
check "V12 pytest manifest: XFAIL executed" "yes" "$(mhas 'tests/test_slug.py::test_known_bad')"
check "V12 pytest manifest: XPASS executed" "yes" "$(mhas 'tests/test_slug.py::test_surprise')"
check "V12 pytest manifest: 8 names"       "8" "$(grep -vc '^#' "$MANIFEST")"
check "V12 pytest manifest: stack header" "yes" "$(grep -q '^# executed tests (stack: python in \.)' "$MANIFEST" && echo yes || echo no)"
check "V12 pytest named: no names-unavailable" "no" "$(grep -q '^# names-unavailable' "$MANIFEST" && echo yes || echo no)"
reset_rc
# A silenced summary (`-p no:terminal` / addopts) passes with no PASSED
# lines: still an oracle (exit 0 ⇒ ≥1 test), and the manifest says the
# names are unavailable rather than pretending nothing ran.
set_out pytest none "collected 3 items"
verify
check "V12 pytest silent summary: green"  "0" "$VRC"
check "V12 pytest silent summary: note"   "yes" "$(grep -q '^# (no per-test names parsed from the pytest -rA summary' "$MANIFEST" && echo yes || echo no)"
check "V12 pytest silent summary: names-unavailable" "yes" "$(grep -q '^# names-unavailable' "$MANIFEST" && echo yes || echo no)"
reset_rc
# Exit 5 = nothing collected → not an oracle (exit 3); exit 2 → red.
set_rc pytest none 5
verify
check "V12 exit 5: not-yet-verifiable"    "3" "$VRC"
check "V12 exit 5: NOTE"                  "yes" "$(vhas 'NOTE: pytest collected no tests (exit 5)')"
set_rc pytest none 2
verify
check "V12 exit 2: red"                   "1" "$VRC"
reset_rc
# .venv wins over the PATH pytest; venv next; uv last (with --frozen only
# when uv.lock exists) — and uv is used for a manifest even though it is not
# pre-verified.
mkdir -p "$WORK/.venv/bin" "$WORK/venv/bin"
cat > "$WORK/.venv/bin/python" <<SH
#!/bin/sh
echo "dotvenv-python \$*" >> "$STATE/calls"
SH
cat > "$WORK/venv/bin/python" <<SH
#!/bin/sh
echo "venv-python \$*" >> "$STATE/calls"
SH
chmod +x "$WORK/.venv/bin/python" "$WORK/venv/bin/python"
verify
check "V12 .venv wins"                    "yes" "$(chas 'dotvenv-python -m pytest')"
check "V12 .venv: PATH pytest not used"   "no"  "$(printf '%s\n' "$(calls)" | tr ';' '\n' | grep -q '^pytest' && echo yes || echo no)"
rm -rf "$WORK/.venv"
verify
check "V12 venv second"                   "yes" "$(chas 'venv-python -m pytest')"
rm -rf "$WORK/venv"
rm -f "$STATE/bin/pytest"
verify
check "V12 uv last resort"                "yes" "$(argv_has "uv${TAB}run${TAB}pytest${TAB}-rA")"
touch "$WORK/uv.lock"
verify
check "V12 uv --frozen with uv.lock"      "yes" "$(argv_has "uv${TAB}run${TAB}--frozen${TAB}pytest${TAB}-rA")"
rm -f "$WORK/uv.lock"
install_tool_shims
rm -f "$WORK/pyproject.toml"
# Manifest-free: test files but no pyproject.toml — discovered (pruned
# dirs ignored) and run from the root only when a runner is importable.
mkdir -p "$WORK/tests" "$WORK/node_modules/x" "$WORK/.venv/lib/site-packages/y"
touch "$WORK/node_modules/x/test_ignored.py" "$WORK/.venv/lib/site-packages/y/test_ignored.py"
verify
check "V12 pruned dirs: no python stack"  "3" "$VRC"
check "V12 pruned dirs: no pytest call"   "no"  "$(chas 'pytest')"
touch "$WORK/tests/test_slug.py"
verify
check "V12 manifest-free: exit 0"         "0" "$VRC"
check "V12 manifest-free: banner"         "yes" "$(vhas 'python test files found without a pyproject.toml (./tests/test_slug.py) — manifest-free pytest run')"
check "V12 manifest-free: runner verified" "yes" "$(chas 'pytest --version')"
check "V12 manifest-free: stack header"   "yes" "$(vhas '=== stack: python in . ===')"
# No importable runner: not run → exit 3 with the INFO line, never a green.
mkdir -p "$STATE/nopy"; for t in go npm cargo make golangci-lint; do ln -sf "$STATE/bin/$t" "$STATE/nopy/$t"; done
mkdir -p "$STATE/pbin"
for t in sh dash bash cat grep paste git awk sed sort uniq head tail tr wc ls printf mkdir rm cp mv dirname basename cut env uname mktemp date cmp find; do
  p="$(command -v "$t" 2>/dev/null)" && [ -n "$p" ] && ln -sf "$p" "$STATE/pbin/$t"
done
[ -z "${TEST_SH:-}" ] || ln -sf "$(command -v "$TEST_SH")" "$STATE/pbin/$TEST_SH"
rm -f "$STATE/calls" "$STATE/argv"
VOUT="$( (cd "$WORK" && PATH="$STATE/nopy:$STATE/pbin" "${TEST_SH:-sh}" .ai/build/verify.sh) 2>&1)"; VRC=$?
check "V12 no runner: exit 3"             "3" "$VRC"
check "V12 no runner: INFO"               "yes" "$(vhas 'INFO: python test files present but no importable pytest runner')"
# A manifest-free suite alongside a pyproject stack elsewhere is NOT run
# twice: the manifest stack covers python.
mkdir -p "$WORK/svc"; touch "$WORK/svc/pyproject.toml"
verify
check "V12 pyproject elsewhere: one python stack" "1" "$(printf '%s\n' "$VOUT" | grep -c '=== stack: python in')"
rm -rf "$WORK/svc" "$WORK/tests" "$WORK/node_modules" "$WORK/.venv"
touch "$WORK/go.mod"

# V13. Executed-test manifest (tracker-runner #901). Go subtests are
#      `Parent/sub`; the count that decides RAN_TESTS is the number of
#      manifest lines (one parse, so they cannot disagree); the file is
#      REWRITTEN on every run (a stale name never survives); a run with no
#      stack still writes the header.
set_out go test "=== RUN   TestInspect
=== RUN   TestInspect/contract
=== RUN   TestInspect/edge
--- PASS: TestInspect (0.00s)
    --- PASS: TestInspect/contract (0.00s)
    --- PASS: TestInspect/edge (0.00s)
PASS"
verify
check "V13 exit 0"                        "0" "$VRC"
check "V13 parent listed"                 "yes" "$(mhas TestInspect)"
check "V13 subtest listed"                "yes" "$(mhas TestInspect/contract)"
check "V13 three names"                   "3" "$(grep -vc '^#' "$MANIFEST")"
check "V13 nothing else"                  "no"  "$(grep -q 'PASS' "$MANIFEST" && echo yes || echo no)"
# A test whose NAME is `# names-unavailable` (or any `#` line) must not
# forge a manifest marker: only verify.sh's own printf writes `#` lines.
set_out go test "=== RUN   # names-unavailable
=== RUN   TestReal
PASS"
verify
check "V13 forged marker via go name: dropped" "no" "$(grep -q '^# names-unavailable' "$MANIFEST" && echo yes || echo no)"
check "V13 forged marker: real name kept" "yes" "$(mhas TestReal)"
check "V13 forged marker: count excludes it" "1" "$(grep -vc '^#' "$MANIFEST")"
touch "$WORK/pyproject.toml"
set_out pytest none "PASSED # names-unavailable
PASSED tests/test_a.py::test_a"
verify
check "V13 forged marker via pytest: dropped" "no" "$(grep -q '^# names-unavailable' "$MANIFEST" && echo yes || echo no)"
rm -f "$WORK/pyproject.toml"; reset_rc
printf 'TestStale\n' >> "$MANIFEST"
set_out go test "=== RUN   TestOnly
PASS"
verify
check "V13 rewritten each run"            "no"  "$(mhas TestStale)"
check "V13 new name present"              "yes" "$(mhas TestOnly)"
reset_rc
rm -f "$WORK/go.mod"
verify
check "V13 no stack: header only"         "yes" "$(grep -q '^# executed tests (no stack ran) — from verify.sh run 20' "$MANIFEST" && echo yes || echo no)"
check "V13 no stack: no names"            "0" "$(grep -vc '^#' "$MANIFEST")"
check "V13 no stack, no oracle: NOT names-unavailable" "no" "$(grep -q '^# names-unavailable' "$MANIFEST" && echo yes || echo no)"
touch "$WORK/go.mod"

# ---------------------------------------------------------------------------
# R. Real `go` scoping semantics. Skipped (not failed) when go is absent.
if command -v go >/dev/null 2>&1; then
  RW="$(mktemp -d)"; trap 'rm -rf "$WORK" "$STATE" "$RW"' EXIT
  RG() { git -C "$RW" -c user.name=t -c user.email=t@t "$@"; }
  RG -c init.defaultBranch=main init -q
  mkdir -p "$RW/.ai/build" "$RW/.ai/milestones" "$RW/a" "$RW/b" "$RW/c"
  printf '.ai/\n' > "$RW/.gitignore"
  cp "$LIB_DIR/verify.sh" "$RW/.ai/build/verify.sh"
  cp "$LIB_DIR/ci-probe.sh" "$RW/.ai/build/ci-probe.sh"
  printf 'module fx\n\ngo 1.22\n' > "$RW/go.mod"
  printf 'package a\n\nfunc Add(x, y int) int { return x + y }\n' > "$RW/a/a.go"
  printf 'package b\n\nimport "fx/a"\n\nfunc Twice(x int) int { return a.Add(x, x) }\n' > "$RW/b/b.go"
  printf 'package b\n\nimport "testing"\n\nfunc TestTwice(t *testing.T) {\n\tif Twice(2) != 4 {\n\t\tt.Fatal("twice")\n\t}\n}\n' > "$RW/b/b_test.go"
  printf 'package c\n\nfunc C() int { return 1 }\n' > "$RW/c/c.go"
  printf 'package c\n\nimport "testing"\n\nfunc TestC(t *testing.T) { t.Log("c") }\n' > "$RW/c/c_test.go"
  RG add -A; RG commit -q -m base
  RG rev-parse HEAD > "$RW/.ai/build/milestone-start-sha"
  # Real go, real git; shims only for the tools we do not want to run
  # (whatever golangci-lint the host has, if any, runs on the tiny fixture).
  mkdir -p "$STATE/rbin"; for t in npm uv cargo make; do ln -sf "$STATE/bin/$t" "$STATE/rbin/$t"; done
  rverify() { VOUT="$( (cd "$RW" && PATH="$STATE/rbin:$PATH" GOFLAGS=-mod=mod "${TEST_SH:-sh}" .ai/build/verify.sh) 2>&1)"; VRC=$?; }

  # R1. #640 D2: an UNCOMMITTED breaking change in a (no commit since the
  #     base) is in scope, and #640 D3 pulls b in via reverse deps → red.
  #     c is untouched and not a dependent → out of scope.
  printf 'package a\n\nfunc Add(x, y int) int { return x + y + 1 }\n' > "$RW/a/a.go"
  rverify
  check "R1 uncommitted breakage is red"    "1" "$VRC"
  check "R1 scope = a + dependent b"        "yes" "$(vhas 'milestone-scoped go test (2 package(s), 1 via reverse deps): fx/a fx/b')"
  check "R1 b's test failed"                "yes" "$(vhas 'FAIL: TestTwice')"
  check "R1 manifest: real go names"        "yes" "$(grep -qxF TestTwice "$RW/.ai/build/executed-tests.txt" && echo yes || echo no)"
  check "R1 c out of scope"                 "no"  "$(vhas 'ok  	fx/c')"
  RG checkout -q a/a.go

  # R2. #640 D2: an UNTRACKED new package with a red test is in scope.
  mkdir -p "$RW/d"
  printf 'package d\n' > "$RW/d/d.go"
  printf 'package d\n\nimport "testing"\n\nfunc TestD(t *testing.T) { t.Fatal("red") }\n' > "$RW/d/d_test.go"
  rverify
  check "R2 untracked package red"          "1" "$VRC"
  check "R2 untracked package in scope"     "yes" "$(vhas 'milestone-scoped go test (1 package(s), 0 via reverse deps): fx/d')"
  rm -rf "$RW/d"

  # R3. #640 D9: changed files that are NOT packages — testdata/, _gen/, a
  #     build-tag-excluded file, a nested module — never yield a fake red;
  #     with nothing buildable in the change set the scope falls back to ./...
  mkdir -p "$RW/testdata" "$RW/_gen" "$RW/tools" "$RW/nested"
  printf 'package testdata\n' > "$RW/testdata/t.go"
  printf 'package gen\n' > "$RW/_gen/g.go"
  printf '//go:build tools\n\npackage tools\n' > "$RW/tools/tools.go"
  printf 'module fx/nested\n\ngo 1.22\n' > "$RW/nested/go.mod"
  printf 'package nested\n' > "$RW/nested/n.go"
  RG add -A; RG commit -q -m nonpkgs
  rverify
  check "R3 non-packages: exit 0"           "0" "$VRC"
  check "R3 non-packages: fallback note"    "yes" "$(vhas 'changed Go files are not in any buildable package')"
  check "R3 nested module runs itself"      "yes" "$(vhas '=== stack: go in nested ===')"
  RG rev-parse HEAD > "$RW/.ai/build/milestone-start-sha"

  # R4. #640 D7 anchoring with the real -skip: `TestC` must skip TestC but
  #     NOT TestCX; `TestS/sub` skips only that subtest.
  printf 'package c\n\nimport "testing"\n\nfunc TestC(t *testing.T) { t.Fatal("must be skipped") }\nfunc TestCX(t *testing.T) { t.Log("CX ran") }\nfunc TestS(t *testing.T) {\n\tt.Run("sub", func(t *testing.T) { t.Fatal("sub must be skipped") })\n\tt.Run("other", func(t *testing.T) { t.Log("other ran") })\n}\n' > "$RW/c/c_test.go"
  printf 'TestC\nTestS/sub\n' > "$RW/.ai/milestones/known_failures"
  rverify
  check "R4 anchored skip: green"           "0" "$VRC"
  check "R4 TestC skipped"                  "no"  "$(vhas 'must be skipped')"
  check "R4 scope is c"                     "yes" "$(vhas 'milestone-scoped go test (1 package(s), 0 via reverse deps): fx/c')"
  check "R4 manifest: subtest as Parent/sub" "yes" "$(grep -qxF TestS/other "$RW/.ai/build/executed-tests.txt" && echo yes || echo no)"
  check "R4 manifest: skipped test absent"  "no"  "$(grep -qxF TestC "$RW/.ai/build/executed-tests.txt" && echo yes || echo no)"
  printf 'Test\n' > "$RW/.ai/milestones/known_failures"
  rverify
  check "R4b bare 'Test' no longer skips all" "1" "$VRC"
  check "R4b TestC ran and failed"          "yes" "$(vhas 'must be skipped')"
  rm -f "$RW/.ai/milestones/known_failures"

  # R5. #640 D2 review: a DELETED file still scopes its package (no
  #     --diff-filter=d) — `git rm c/c2.go` + an edit in a → scope has c.
  RG checkout -q c/c_test.go
  printf 'package c\n\nfunc C2() int { return 2 }\n' > "$RW/c/c2.go"
  RG add -A; RG commit -q -m c2
  RG rev-parse HEAD > "$RW/.ai/build/milestone-start-sha"
  RG rm -q c/c2.go
  printf 'package a\n\nfunc Add(x, y int) int { return y + x }\n' > "$RW/a/a.go"
  rverify
  check "R5 deletion scopes its package"    "yes" "$(vhas 'milestone-scoped go test (3 package(s), 1 via reverse deps): fx/a fx/b fx/c')"
  check "R5 green"                          "0" "$VRC"
else
  echo "info: go not on PATH — skipping the real-go scoping sections (R1-R4)"
fi

if [ "$fail" = 0 ]; then echo "ALL PASS"; else echo "SOME FAILED"; exit 1; fi
