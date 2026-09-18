#!/usr/bin/env bash
# ABOUTME: Fixture tests for TestMilestone.sh — re-emits the gate scripts from the
# ABOUTME: workflow sidecar and snapshots the known_* hatches (#640 D6), wraps the
# ABOUTME: shared verify.sh green-gate (#406) with the red-only fix-attempt counter
# ABOUTME: (#640 B2/B3, #443) and the tests-pass / tests-not-yet-verifiable /
# ABOUTME: __ROUTE_ESCALATE__ sentinels (E8 marker; tracker-runner #857 exit 3),
# ABOUTME: and reconciles the milestone's declared contract tests against the
# ABOUTME: executed-test manifest (tracker-runner #901: CONTRACT-TEST-MISSING).
#
# verify.sh is the REAL lib/verify.sh (TestMilestone restores it from
# ${graph.workflow_dir} before every run, so a stub could not survive); the
# toolchain is PATH-shimmed (test_helpers.sh) and `go test`'s exit code is the
# red/green lever.
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
SCRIPT="$(stage_script "$DIR/TestMilestone.sh")"   # ${graph.workflow_dir} expanded as the engine does
# TEST_SH=dash runs the node script under dash (the .dip runs it via `sh -c`);
# the inner `sh .ai/build/verify.sh` is the node's own runtime contract.
run() { rm -f "$STATE/calls" "$STATE/argv"; OUT="$( (cd "$WORK" && PATH="$STATE/bin:$PATH" "${TEST_SH:-sh}" "$SCRIPT") 2>"$STATE/stderr")"; RC=$?; }
last() { printf '%s' "$OUT" | tail -1; }
ohas() { printf '%s' "$OUT" | grep -qF -- "$1" && echo yes || echo no; }
chas() { printf '%s' "$(calls)" | grep -qF -- "$1" && echo yes || echo no; }
COUNTER="$WORK/.ai/milestones/fix_attempts"
G() { git -C "$WORK" -c user.name=t -c user.email=t@t "$@"; }
G -c init.defaultBranch=main init -q
mkdir -p "$WORK/.ai/build" "$WORK/.ai/milestones"
printf '.ai/\n' > "$WORK/.gitignore"
touch "$WORK/go.mod"
cp "$LIB_DIR/verify.sh" "$WORK/.ai/build/verify.sh"
cp "$LIB_DIR/ci-probe.sh" "$WORK/.ai/build/ci-probe.sh"
set_red()   { set_rc go test 1; }
set_green() { reset_rc; }

# 1. Green on first attempt: counter reset to 0, tests-pass LAST, verify
#    output surfaced, the real gate ran (go build/test/vet).
set_green
run
check "green exit 0"                 "0" "$RC"
check "green marker last"            "tests-pass" "$(last)"
check "green resets counter"         "0" "$(cat "$COUNTER")"
check "verify output surfaced"       "yes" "$(ohas '=== stack: go in . ===')"
check "real gate ran"                "yes" "$(chas 'go test')"
check "no restore warning"           "no"  "$(ohas 'WARNING: .ai/build/')"

# 2. Red: attempts 1 and 2 hand off to the fix loop (exit 1, NO escalate
#    marker); the 3rd consecutive failure escalates (-ge 3 = exactly 3
#    test attempts / 2 fix passes per milestone, #443). #640 B3: the
#    counter is bumped AFTER the verify, only on red.
rm -f "$COUNTER"
set_red
run
check "red 1 exit 1"                 "1" "$RC"
check "red 1 counter"                "1" "$(cat "$COUNTER")"
check "red 1 attempt line"           "yes" "$(ohas '--- attempt 1 of 3 ---')"
check "red 1 no escalate"            "no" "$(ohas '__ROUTE_ESCALATE__')"
run
check "red 2 exit 1"                 "1" "$RC"
check "red 2 counter"                "2" "$(cat "$COUNTER")"
check "red 2 no escalate"            "no" "$(ohas '__ROUTE_ESCALATE__')"
run
check "red 3 exit 1"                 "1" "$RC"
check "red 3 ESCALATE line"          "yes" "$(ohas 'ESCALATE: milestone failed after 3 attempts')"
check "red 3 escalate marker last"   "__ROUTE_ESCALATE__" "$(last)"
# #640 B2: escalating RESETS the counter so `EscalateMilestone retry ->
# Implement` gets a fresh 3-attempt budget (was: "attempt 4 of 3").
check "red 3 counter reset (B2)"     "0" "$(cat "$COUNTER")"
run
check "red after escalate = attempt 1" "yes" "$(ohas '--- attempt 1 of 3 ---')"
check "red after escalate no escalate" "no" "$(ohas '__ROUTE_ESCALATE__')"
set_green
run
check "green after red resets"       "0" "$(cat "$COUNTER")"

# 3. #640 B3: a verify that never completes (Ctrl-C mid-run, engine retry
#    must not eat an attempt: the go shim TERMs the TestMilestone shell
#    mid-gate (as Ctrl-C would) and the counter is untouched. (An engine
#    retry of FixMilestone re-runs FixMilestone in place — it has no
#    retry_target — so it never reaches here; pinned in Go by
#    TestFixMilestoneRetriesInPlace.)
rm -f "$COUNTER"
cat > "$STATE/bin/go" <<'SHIM'
#!/bin/sh
if [ "$1" = test ]; then
  p=$PPID
  while [ "$p" -gt 1 ]; do
    case "$(ps -o command= -p "$p")" in *TestMilestone.sh*) kill -TERM "$p"; break ;; esac
    p=$(ps -o ppid= -p "$p" | tr -d ' ')
  done
fi
exit 0
SHIM
run
check "interrupted verify: no counter"  "no" "$([ -f "$COUNTER" ] && echo yes || echo no)"
check "interrupted verify: no marker"   "no" "$(ohas 'tests-pass')"
install_tool_shims

# 4. Environment escalate (#640 E8): Makefile present but `make` missing is
#    signalled by the .ai/build/ci-make-missing marker file — NOT an exit
#    number — and escalates IMMEDIATELY regardless of attempt count, with
#    the counter reset. PATH restricted to symlinks of what the gate needs.
printf 'ci:\n\techo i\n' > "$WORK/Makefile"
mkdir -p "$STATE/pbin"
for t in sh dash bash cat grep paste git awk sed sort uniq head tail tr wc ls printf mkdir rm cp mv dirname basename cut env uname mktemp date cmp find; do
  p="$(command -v "$t" 2>/dev/null)" && [ -n "$p" ] && ln -sf "$p" "$STATE/pbin/$t"
done
ln -sf "$STATE/bin/go" "$STATE/pbin/go"
[ -z "${TEST_SH:-}" ] || ln -sf "$(command -v "$TEST_SH")" "$STATE/pbin/$TEST_SH"
echo 1 > "$COUNTER"
OUT="$( (cd "$WORK" && PATH="$STATE/pbin" "${TEST_SH:-sh}" "$SCRIPT") 2>"$STATE/stderr")"; RC=$?
check "env exit 1"                   "1" "$RC"
check "env escalate marker last"     "__ROUTE_ESCALATE__" "$(last)"
check "env resets counter"           "0" "$(cat "$COUNTER")"
check "env ESCALATE reason line"     "yes" "$(ohas 'ESCALATE: environment problem (see _TRACKER_CI_MAKE_MISSING above)')"
check "env no attempt line"          "no" "$(ohas '--- attempt')"
rm -f "$WORK/Makefile"
# A stale marker file from a previous run does not escalate a green run.
: > "$WORK/.ai/build/ci-make-missing"
set_green
run
check "stale marker ignored"         "tests-pass" "$(last)"

# 5. Numeric guard: corrupted counter is treated as 0 → this red is attempt 1.
set_red
for junk in 'garbage' '1 2' ''; do
  printf '%s\n' "$junk" > "$COUNTER"
  run
  check "junk '$junk' -> attempt 1"  "1" "$(cat "$COUNTER")"
  check "junk '$junk' -> no escalate" "no" "$(ohas '__ROUTE_ESCALATE__')"
done

# 6. A runner exit that is neither 0/1 (e.g. 2, 3) is an ordinary red — no
#    number is reserved any more (#640 E8).
for code in 2 3; do
  rm -f "$COUNTER"; set_rc go test "$code"
  run
  check "rc $code is normal fail"    "1" "$RC"
  check "rc $code no escalate"       "no" "$(ohas '__ROUTE_ESCALATE__')"
  check "rc $code counted"           "1" "$(cat "$COUNTER")"
done
set_green

# 7. #640 D6: an agent-rewritten .ai/build/verify.sh (`exit 0`) is RESTORED
#    from the sidecar before the gate runs — the real (red) test runs, a
#    WARNING names the file — and a missing verify.sh / ci-probe.sh is
#    simply re-emitted (no dash-rc-2 / bash-rc-127 collision, E8).
set_red; rm -f "$COUNTER"
printf 'exit 0\n' > "$WORK/.ai/build/verify.sh"
run
check "tampered verify.sh: red"      "1" "$RC"
check "tampered verify.sh: no pass"  "no"  "$(ohas 'tests-pass')"
check "tampered verify.sh: WARNING"  "yes" "$(ohas 'WARNING: .ai/build/verify.sh differed from the workflow')"
check "tampered verify.sh: restored" "yes" "$(cmp -s "$LIB_DIR/verify.sh" "$WORK/.ai/build/verify.sh" && echo yes || echo no)"
check "tampered verify.sh: real gate ran" "yes" "$(chas 'go test')"
printf 'run_project_ci_gate() { return 0; }\n' > "$WORK/.ai/build/ci-probe.sh"
run
check "tampered ci-probe: WARNING"   "yes" "$(ohas 'WARNING: .ai/build/ci-probe.sh differed from the workflow')"
check "tampered ci-probe: restored"  "yes" "$(cmp -s "$LIB_DIR/ci-probe.sh" "$WORK/.ai/build/ci-probe.sh" && echo yes || echo no)"
rm -f "$WORK/.ai/build/verify.sh" "$WORK/.ai/build/ci-probe.sh" "$COUNTER"
run
check "missing gate files: re-emitted" "yes" "$([ -f "$WORK/.ai/build/verify.sh" ] && [ -f "$WORK/.ai/build/ci-probe.sh" ] && echo yes || echo no)"
check "missing gate files: no WARNING" "no"  "$(ohas 'WARNING: .ai/build/')"
check "missing gate files: ordinary red" "1" "$(cat "$COUNTER")"
check "missing gate files: no escalate" "no" "$(ohas '__ROUTE_ESCALATE__')"
set_green

# 7b. tracker-runner #857/#873: verify.sh exit 3 (green as far as it goes,
#     but no oracle ran — here: `go test` executed zero tests) is
#     NOT-YET-VERIFIABLE: exit 0 with the distinct marker LAST (routes to
#     VerifyMilestone on outcome=success, never to the fix loop), the
#     counter is reset (not a red attempt), no tests-pass, no escalate. A
#     red run after it starts at attempt 1.
echo 2 > "$COUNTER"
set_out go test ""
run
check "nyv: exit 0"                  "0" "$RC"
check "nyv: marker last"             "tests-not-yet-verifiable" "$(last)"
check "nyv: no tests-pass"           "no"  "$(ohas 'tests-pass')"
check "nyv: no escalate"             "no"  "$(ohas '__ROUTE_ESCALATE__')"
check "nyv: verdict surfaced"        "yes" "$(ohas 'NOT-YET-VERIFIABLE: no runnable test suite or project CI target detected — the milestone verifier decides.')"
check "nyv: verify.sh verdict too"   "yes" "$(ohas 'Deny-by-default: absence of a runnable oracle is not a pass')"
check "nyv: counter reset"           "0" "$(cat "$COUNTER")"
set_red
run
check "red after nyv = attempt 1"    "yes" "$(ohas '--- attempt 1 of 3 ---')"
set_green
# A red native lint gate is advisory: tests-pass (the fix loop is not
# driven by imposed lint), with the ADVISORY line visible.
set_rc go vet 1
run
check "advisory lint: tests-pass"    "tests-pass" "$(last)"
check "advisory lint: ADVISORY line" "yes" "$(ohas 'ADVISORY:')"
set_green

# 8. #640 D6: hatch / stamp diff against the snapshot PickNextMilestone
#    takes at milestone start (simulated here by calling the same
#    snapshot_hatch_files). Additions — and an operator stamp that appears
#    after the snapshot — are printed on EVERY run; the snapshot is never
#    (re)taken here; a missing snapshot is a WARNING that lists everything.
. "$LIB_DIR/gate-integrity.sh"
rm -f "$WORK"/.ai/milestones/*.snapshot
printf 'TestOld\n' > "$WORK/.ai/milestones/known_failures"
run
check "no snapshot: WARNING"         "yes" "$(ohas 'WARNING: no milestone-start snapshot for known_failures')"
check "no snapshot: everything listed" "yes" "$(ohas '  + TestOld')"
check "no snapshot: not created here" "no" "$([ -f "$WORK/.ai/milestones/known_failures.snapshot" ] && echo yes || echo no)"
(cd "$WORK" && snapshot_hatch_files)   # = PickNextMilestone at milestone start
check "snapshot taken"               "TestOld" "$(cat "$WORK/.ai/milestones/known_failures.snapshot")"
run
check "no additions yet"             "no" "$(ohas 'ADDED since milestone start')"
check "no stamp finding yet"         "no" "$(ohas 'operator stamp CREATED')"
# Implement/Fix-style additions AFTER the snapshot.
printf 'TestOld\n# note\nTestSneaky\n' > "$WORK/.ai/milestones/known_failures"
printf 'G404\n' > "$WORK/.ai/milestones/known_lint_failures"
touch "$WORK/.ai/build/no-tests-ok"
run
check "added known_failures diff"    "yes" "$(ohas 'known_failures: entries ADDED since milestone start')"
check "added entry printed"          "yes" "$(ohas '  + TestSneaky')"
check "unchanged entry not printed"  "no"  "$(ohas '  + TestOld')"
check "added lint entry printed"     "yes" "$(ohas '  + G404')"
check "agent-created stamp reported" "yes" "$(ohas '  + .ai/build/no-tests-ok')"
check "snapshot unchanged"           "TestOld" "$(cat "$WORK/.ai/milestones/known_failures.snapshot")"
run
check "diff printed on every run"    "yes" "$(ohas '  + TestSneaky')"
# A stamp that existed at milestone start is baselined, not a finding.
rm -f "$WORK"/.ai/milestones/*.snapshot; (cd "$WORK" && snapshot_hatch_files)
run
check "pre-existing stamp not reported" "no" "$(ohas 'operator stamp CREATED')"
rm -f "$WORK/.ai/build/no-tests-ok" "$WORK/.ai/milestones/known_failures" "$WORK/.ai/milestones/known_lint_failures" "$WORK"/.ai/milestones/*.snapshot

# 9. tracker-runner #901: a milestone that authors NO test still went green
#    on the prior suite. PickNextMilestone writes the milestone's declared
#    `**Contract tests**` to .ai/milestones/contract-tests; after a green
#    verify, TestMilestone reconciles them against verify.sh's executed-test
#    manifest (.ai/build/executed-tests.txt). A declared test that did not
#    execute is RED — `CONTRACT-TEST-MISSING:` + exit 1, routed to
#    FixMilestone like any other red (the fix-attempt counter bumps the same
#    way, no special path). Exact match, or Go subtest / pytest param prefix,
#    or a `::`-path suffix (Rust `mod::test`, pytest `file::test`).
CT="$WORK/.ai/milestones/contract-tests"
rm -f "$COUNTER"; set_green
# 9a. Go: declared parent + subtest, both executed → green with the tally.
printf 'TestInspect\nTestInspect/contract\n' > "$CT"
set_out go test "=== RUN   TestInspect
=== RUN   TestInspect/contract
--- PASS: TestInspect (0.00s)
PASS"
run
check "ct go: exit 0"                "0" "$RC"
check "ct go: marker last"           "tests-pass" "$(last)"
check "ct go: tally line"            "yes" "$(ohas '--- contract tests: 2/2 executed ---')"
check "ct go: no MISSING"            "no"  "$(ohas 'CONTRACT-TEST-MISSING')"
# 9b. Go: the declared subtest did not run (only the parent did) → red.
set_out go test "=== RUN   TestInspect
--- PASS: TestInspect (0.00s)
PASS"
run
check "ct go subtest missing: exit 1" "1" "$RC"
check "ct go subtest missing: tally"  "yes" "$(ohas '--- contract tests: 1/2 executed ---')"
check "ct go subtest missing: named"  "yes" "$(ohas '  MISSING: TestInspect/contract')"
check "ct go subtest missing: line"   "yes" "$(printf '%s' "$OUT" | grep -q '^CONTRACT-TEST-MISSING: TestInspect/contract' && echo yes || echo no)"
check "ct go subtest missing: no pass" "no" "$(ohas 'tests-pass')"
check "ct go subtest missing: counter 1 (a red like any other)" "1" "$(cat "$COUNTER")"
check "ct go subtest missing: attempt line" "yes" "$(ohas '--- attempt 1 of 3 ---')"
# A declared PARENT is satisfied by its executed subtests (prefix match).
printf 'TestInspect\n' > "$CT"
set_out go test "=== RUN   TestInspect/contract
PASS"
run
check "ct go parent via subtest: green" "tests-pass" "$(last)"
# 9c. Third consecutive contract-red escalates exactly like a test red.
printf 'TestNever\n' > "$CT"
echo 2 > "$COUNTER"
run
check "ct 3rd red escalates"         "__ROUTE_ESCALATE__" "$(last)"
check "ct 3rd red ESCALATE line"     "yes" "$(ohas 'ESCALATE: milestone failed after 3 attempts')"
rm -f "$COUNTER"
# 9d. Rust workspace: milestone 2 declares inspector::test_inspect_contract
#     but never adds it → red; adding the test (it now appears in cargo's
#     per-test lines) → green. A crate-prefixed path matches by `::` suffix.
rm -f "$WORK/go.mod"; touch "$WORK/Cargo.toml"; reset_rc
printf 'inspector::test_inspect_contract\n' > "$CT"
set_out cargo test "running 2 tests
test loader::test_load_contract ... ok
test util::test_helper ... ok
test result: ok. 2 passed; 0 failed"
run
check "ct rust missing: exit 1"      "1" "$RC"
check "ct rust missing: line"        "yes" "$(ohas 'CONTRACT-TEST-MISSING: inspector::test_inspect_contract')"
check "ct rust missing: counter 1"   "1" "$(cat "$COUNTER")"
set_out cargo test "running 3 tests
test loader::test_load_contract ... ok
test util::test_helper ... ok
test inspector::test_inspect_contract ... ok
test result: ok. 3 passed; 0 failed"
run
check "ct rust added: green"         "tests-pass" "$(last)"
check "ct rust added: tally"         "yes" "$(ohas '--- contract tests: 1/1 executed ---')"
check "ct rust added: counter reset" "0" "$(cat "$COUNTER")"
set_out cargo test "test mycrate::inspector::test_inspect_contract ... ok
test result: ok. 1 passed; 0 failed"
run
check "ct rust crate-prefixed path: green" "tests-pass" "$(last)"
# The idiomatic `mod tests` shape (`inspector::tests::test_x`) satisfies the
# prescribed declaration `inspector::test_x` (module segments in order, then
# the leaf); a cargo integration test in tests/ prints the BARE leaf, which
# satisfies a `::`-qualified declaration only under a cargo stack.
set_out cargo test "test inspector::tests::test_inspect_contract ... ok
test result: ok. 1 passed; 0 failed"
run
check "ct rust mod tests shape: green" "tests-pass" "$(last)"
set_out cargo test "running 1 test
test test_inspect_contract ... ok
test result: ok. 1 passed; 0 failed"
run
check "ct rust bare integration leaf: green" "tests-pass" "$(last)"
# ...but the leaf alone never satisfies a DIFFERENT module path's leaf
# when the declared module segments are absent AND the executed name is
# itself qualified.
set_out cargo test "test other::test_inspect_contract ... ok
test result: ok. 1 passed; 0 failed"
run
check "ct rust wrong module qualified: red" "1" "$RC"
# An `ignored` test did not execute — still missing.
set_out cargo test "test inspector::test_inspect_contract ... ignored
test other::t ... ok
test result: ok. 1 passed; 0 failed; 1 ignored"
run
check "ct rust ignored: red"         "1" "$RC"
check "ct rust ignored: named"       "yes" "$(ohas '  MISSING: inspector::test_inspect_contract')"
rm -f "$WORK/Cargo.toml" "$COUNTER"; reset_rc
# 9e. pytest: nodeid exact, a parametrized id by prefix, and a bare test
#     name by `::` suffix; a declared test absent from -rA → red.
touch "$WORK/pyproject.toml"
printf 'tests/test_inspect.py::test_inspect_contract\ntests/test_inspect.py::test_cases\ntest_roundtrip\n' > "$CT"
set_out pytest none "PASSED tests/test_inspect.py::test_inspect_contract
PASSED tests/test_inspect.py::test_cases[a]
PASSED tests/test_inspect.py::test_cases[b]
PASSED tests/test_io.py::test_roundtrip"
run
check "ct pytest: green"             "tests-pass" "$(last)"
check "ct pytest: tally"             "yes" "$(ohas '--- contract tests: 3/3 executed ---')"
# A test method inside a class: declared `path::test_m`, executed
# `path::TestCls::test_m` (and its parametrized ids) → satisfied.
set_out pytest none "PASSED tests/test_inspect.py::TestInspect::test_inspect_contract
PASSED tests/test_inspect.py::TestInspect::test_cases[a]
PASSED tests/test_io.py::test_roundtrip"
run
check "ct pytest class-qualified: green" "tests-pass" "$(last)"
# A bare leaf never satisfies a pytest `path::leaf` declaration (only
# cargo integration tests print bare names).
set_out pytest none "PASSED test_inspect_contract
PASSED tests/test_inspect.py::test_cases
PASSED tests/test_io.py::test_roundtrip"
run
check "ct pytest bare leaf: red"      "1" "$RC"
set_out pytest none "PASSED tests/test_inspect.py::test_cases[a]
PASSED tests/test_io.py::test_roundtrip"
run
check "ct pytest missing: red"       "1" "$RC"
check "ct pytest missing: named"     "yes" "$(ohas '  MISSING: tests/test_inspect.py::test_inspect_contract')"
check "ct pytest missing: others ok" "yes" "$(ohas '--- contract tests: 2/3 executed ---')"
rm -f "$WORK/pyproject.toml" "$COUNTER"; reset_rc; touch "$WORK/go.mod"
# 9f. "none" (an empty contract-tests file — Decompose wrote `none — reason`)
#     and a MISSING file (a resume from before PickNextMilestone wrote one)
#     both pass with a line saying so; the verifier judges "none".
: > "$CT"
run
check "ct none: green"               "tests-pass" "$(last)"
check "ct none: line"                "yes" "$(ohas '--- contract tests: none declared ---')"
rm -f "$CT"
run
check "ct no file: green"            "tests-pass" "$(last)"
check "ct no file: INFO"             "yes" "$(ohas 'INFO: no .ai/milestones/contract-tests')"
# 9g. Not-yet-verifiable (verify.sh exit 3: zero tests executed) with
#     declared contract tests: they cannot have run → the same red, never
#     the tests-not-yet-verifiable marker (the fix is to write the tests).
#     With no declared tests the exit-3 path is unchanged.
printf 'TestInspect\n' > "$CT"
set_out go test ""
run
check "ct nyv+declared: exit 1"      "1" "$RC"
check "ct nyv+declared: no nyv marker" "no" "$(ohas 'tests-not-yet-verifiable')"
check "ct nyv+declared: MISSING line" "yes" "$(ohas 'CONTRACT-TEST-MISSING: TestInspect')"
check "ct nyv+declared: counter 1"   "1" "$(cat "$COUNTER")"
: > "$CT"
run
check "ct nyv+none: nyv marker"      "tests-not-yet-verifiable" "$(last)"
check "ct nyv+none: counter reset"   "0" "$(cat "$COUNTER")"
reset_rc; rm -f "$CT" "$COUNTER"
# 9i. Runners that list NO names (jest's default reporter across >1 file,
#     vitest's per-file lines, a Makefile-only oracle, pytest with the
#     summary silenced) must not be an unfixable dead end: the manifest
#     carries `# names-unavailable`, a declared name that is not provable
#     is a WARNING (`verifier decides`), and the gate stays green.
rm -f "$WORK/go.mod"; touch "$WORK/package.json"; reset_rc
printf 'adds two numbers\n' > "$CT"
set_out npm test "Tests:       3 passed, 3 total"
run
check "ct jest summary-only: green"  "tests-pass" "$(last)"
check "ct jest summary-only: WARNING" "yes" "$(ohas 'WARNING: adds two numbers not provable from the manifest (runner lists no names) — verifier decides')"
check "ct jest summary-only: no MISSING line" "no" "$(ohas 'CONTRACT-TEST-MISSING')"
check "ct jest summary-only: counter reset" "0" "$(cat "$COUNTER")"
set_out npm test " ✓ src/calc.test.ts (3 tests) 5ms
      Tests  3 passed (3)"
run
check "ct vitest per-file: green"    "tests-pass" "$(last)"
check "ct vitest per-file: WARNING"  "yes" "$(ohas 'WARNING: adds two numbers not provable')"
# When the reporter DOES list names, a missing one is still red.
set_out npm test "  ✓ subtracts (1 ms)
Tests:       1 passed, 1 total"
run
check "ct jest named, missing: red"  "1" "$RC"
check "ct jest named, missing: MISSING" "yes" "$(ohas 'CONTRACT-TEST-MISSING: adds two numbers')"
rm -f "$WORK/package.json" "$COUNTER"; reset_rc
# Makefile-only oracle (no manifest anywhere).
printf 'test:\n\techo t\n' > "$WORK/Makefile"
printf 'TestInspect\n' > "$CT"
run
check "ct Makefile-only: green"      "tests-pass" "$(last)"
check "ct Makefile-only: WARNING"    "yes" "$(ohas 'WARNING: TestInspect not provable from the manifest')"
rm -f "$WORK/Makefile" "$COUNTER"
# pytest with the summary silenced.
touch "$WORK/pyproject.toml"
printf 'tests/test_x.py::test_a\n' > "$CT"
set_out pytest none "collected 2 items"
run
check "ct pytest silent: green"      "tests-pass" "$(last)"
check "ct pytest silent: WARNING"    "yes" "$(ohas 'WARNING: tests/test_x.py::test_a not provable')"
rm -f "$WORK/pyproject.toml" "$COUNTER"; reset_rc; touch "$WORK/go.mod"

# 9h. A red verify never reaches the reconcile (the test failure is the
#     signal; a second MISSING line would only muddy the fix prompt).
printf 'TestInspect\n' > "$CT"
set_red
run
check "ct red verify: exit 1"        "1" "$RC"
check "ct red verify: no reconcile"  "no"  "$(ohas 'contract tests:')"
set_green; rm -f "$CT" "$COUNTER"

if [ "$fail" = 0 ]; then echo "ALL PASS"; else echo "SOME FAILED"; exit 1; fi
