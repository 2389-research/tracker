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
#   V1 no stack → exit 1 (#640 D1) unless .ai/build/no-tests-ok (NOTE)
#   V2 go: build → test ./... → vet (#299); milestone NOTE on zero tests
#   V3 #392/#436 scoping + LINT_NEW_FROM_REV only with a real base; BSD awk (E1)
#   V4 known_failures → anchored `-skip` (#640 D7); invalid regex fails closed
#   V5 every runner exit collapses to 1; V6 go build failure aborts the stack
#   V7 sticky multi-stack sweep (#305); V8 nested stacks run in their own dir,
#      go.work subsumes nested go.mod, node_modules/vendor/testdata excluded
#   V9 --final: -count=1, known_failures ignored + listed, zero tests = FAIL,
#      elapsed per stack; V10 missing ci-probe.sh → exit 1 with a message
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

# V1. No build system (#640 D1): milestone mode passes with a loud NOTE (a
#     scaffolding/docs milestone has no runner yet; VerifyMilestone judges
#     it), --final FAILS; neither prints the opt-out command or path (the fix
#     agent reads this output). A Makefile ci/check/lint/test target counts
#     as a stack. The operator stamp passes both modes with a NOTE.
verify
check "V1 milestone: exit 0"              "0" "$VRC"
check "V1 milestone: loud NOTE"           "yes" "$(vhas 'NOTE: no build system detected — nothing was tested this milestone')"
check "V1 milestone: ship-gate warning"   "yes" "$(vhas 'The ship gate (FinalBuild) FAILS on this')"
check "V1 no calls"                       "" "$(calls)"
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
rm -f "$WORK/Makefile"
touch "$WORK/.ai/build/no-tests-ok"
verify --final
check "V1c opt-out final exit 0"          "0" "$VRC"
check "V1c opt-out NOTE"                  "yes" "$(vhas 'NOTE: no build system detected and the operator opt-out stamp is present')"
check "V1c no toolchain info"             "yes" "$(vhas 'no recognized toolchain')"
rm -f "$WORK/.ai/build/no-tests-ok"

# V2. Go stack, no commits: build, test ./..., vet; the golangci-lint shim is
#     present so it runs WITHOUT --new-from-rev (empty-tree base). A scope
#     with no test files passes with a loud NOTE (milestone mode only).
touch "$WORK/go.mod"
verify
check "V2 exit 0"                         "0" "$VRC"
check "V2 build before test"              "yes" "$(printf '%s' "$(calls)" | grep -q 'go build ./...;.*go test ./...' && echo yes || echo no)"
check "V2 vet then lint after tests"      "yes" "$(printf '%s' "$(calls)" | grep -q 'go test ./...;.*go vet ./...;golangci-lint version;golangci-lint run$' && echo yes || echo no)"
check "V2 ./... fallback message"         "yes" "$(vhas 'no changed Go files in milestone range — testing ./...')"
check "V2 no lint scoping w/o base"       "no" "$(vhas '--new-from-rev')"
check "V2 stack header names dir"         "yes" "$(vhas '=== stack: go in . ===')"
set_out go list-tests ""
verify
check "V2b zero tests: still exit 0"      "0" "$VRC"
check "V2b zero tests: loud NOTE"         "yes" "$(vhas 'NOTE: no Go test files in scope')"
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
check "V3 go test target"                 "yes" "$(chas 'go test . ./pkg/a')"
check "V3 scoped message"                 "yes" "$(vhas 'milestone-scoped go test (2 package(s), 0 via reverse deps): . ./pkg/a')"
check "V3 no awk error (E1)"              "no"  "$(vhas 'nonterminated character class')"
check "V3 lint --new-from-rev base"       "yes" "$(chas "golangci-lint run --new-from-rev $BASE_SHA")"
# Unreachable start SHA → empty-tree base → all packages, no lint scoping.
echo deadbeefdeadbeefdeadbeefdeadbeefdeadbeef > "$WORK/.ai/build/milestone-start-sha"
verify
check "V3b unreachable base exit 0"       "0" "$VRC"
check "V3b no --new-from-rev"             "no" "$(chas '--new-from-rev')"
check "V3b all pkgs from empty tree"      "yes" "$(chas 'go test . ./pkg/a')"
rm -f "$WORK/.ai/build/milestone-start-sha"

# V4. known_failures → `-skip` with every entry anchored per path segment
#     (#640 D7): comments / blank / whitespace-only lines stripped, a
#     leading `-` rejected, `TestA/sub` → `^TestA$/^sub$`, the whole pattern
#     ONE argument. An invalid regex fails closed.
printf '# expected to fail until m3\r\nTestStream\n  \n\nTestFlaky/sub \n-run\n' > "$WORK/.ai/milestones/known_failures"
verify
check "V4 exit 0"                         "0" "$VRC"
check "V4 -skip anchored, one arg"        "yes" "$(argv_has "go${TAB}test${TAB}.${TAB}./pkg/a${TAB}-skip${TAB}^TestStream\$|^TestFlaky\$/^sub\$")"
check "V4 skip message"                   "yes" "$(vhas 'skipping known failures: ^TestStream$|^TestFlaky$/^sub$')"
check "V4 leading dash rejected"          "yes" "$(vhas "WARNING: ignoring known_failures entry '-run'")"
printf 'TestOk\nTest(Broken\n' > "$WORK/.ai/milestones/known_failures"
verify
check "V4b invalid regex exit 1"          "1" "$VRC"
check "V4b invalid regex named"           "yes" "$(vhas "ERROR: known_failures entry 'Test(Broken' is not a valid regular expression")"
check "V4b nothing ran"                   "" "$(calls)"
rm -f "$WORK/.ai/milestones/known_failures"

# V5. A test runner exiting 2 collapses to exit 1; later gates still run.
set_rc go test 2
verify
check "V5 runner rc2 -> 1"                "1" "$VRC"
check "V5 vet still ran"                  "yes" "$(chas 'go vet')"
reset_rc
set_rc go vet 3
verify
check "V5b vet fail -> 1"                 "1" "$VRC"
reset_rc

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
mkdir -p "$WORK/backend" "$WORK/frontend" "$WORK/vendor/x" "$WORK/node_modules/y" "$WORK/testdata/z"
touch "$WORK/backend/go.mod" "$WORK/frontend/package.json" "$WORK/vendor/x/Cargo.toml" "$WORK/node_modules/y/package.json" "$WORK/testdata/z/go.mod"
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
mkdir -p "$WORK/mod"; touch "$WORK/go.work" "$WORK/mod/go.mod"
verify
check "V8b go.work root runs"             "yes" "$(vhas '=== stack: go in . ===')"
check "V8b go.work subsumes mod/go.mod"   "no"  "$(vhas 'stack: go in mod')"
check "V8b root go.work subsumes backend"  "no"  "$(vhas 'stack: go in backend')"
rm -rf "$WORK/backend" "$WORK/frontend" "$WORK/vendor" "$WORK/node_modules" "$WORK/testdata" "$WORK/mod" "$WORK/go.work"
install_tool_shims

# V9. --final ship mode (#640 D7/D12/D13): whole tree with -count=1, no
#     -skip and the still-listed known_failures printed, per-stack elapsed
#     line; a Go tree with zero test files FAILS.
touch "$WORK/go.mod"
printf 'TestStillListed\n' > "$WORK/.ai/milestones/known_failures"
verify --final
check "V9 exit 0"                         "0" "$VRC"
check "V9 -count=1 whole tree"            "yes" "$(argv_has "go${TAB}test${TAB}-count=1${TAB}./...")"
check "V9 no -skip"                       "no"  "$(chas '-skip')"
check "V9 known_failures listed"          "yes" "$(vhas 'still listed: TestStillListed')"
check "V9 ignore notice"                  "yes" "$(vhas 'known_failures is IGNORED by the ship gate')"
check "V9 elapsed per stack"              "yes" "$(printf '%s' "$VOUT" | grep -qE '^=== stack: go in \. — [0-9]+s, PASS ===$' && echo yes || echo no)"
check "V9 no lint scoping in final"       "no"  "$(chas '--new-from-rev')"
set_out go list-tests ""
verify --final
check "V9b zero tests: exit 1"            "1" "$VRC"
check "V9b zero tests: error"             "yes" "$(vhas 'ERROR: no Go test files in ANY Go stack')"
reset_rc; rm -f "$WORK/.ai/milestones/known_failures"

# V10. Missing ci-probe.sh (#640 E8): a clear message and exit 1 — never a
#      bare `sh` rc that could be mistaken for anything else.
mv "$WORK/.ai/build/ci-probe.sh" "$STATE/ci-probe.bak"
verify
check "V10 missing probe exit 1"          "1" "$VRC"
check "V10 missing probe message"         "yes" "$(vhas 'ERROR: .ai/build/ci-probe.sh missing')"
mv "$STATE/ci-probe.bak" "$WORK/.ai/build/ci-probe.sh"

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
