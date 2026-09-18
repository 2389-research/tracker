#!/usr/bin/env bash
# ABOUTME: Fixture tests for TestMilestone.sh — re-emits the gate scripts from the
# ABOUTME: workflow sidecar and snapshots the known_* hatches (#640 D6), wraps the
# ABOUTME: shared verify.sh green-gate (#406) with the red-only fix-attempt counter
# ABOUTME: (#640 B2/B3, #443) and the tests-pass / tests-not-yet-verifiable /
# ABOUTME: __ROUTE_ESCALATE__ sentinels (E8 marker; tracker-runner #857 exit 3).
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

if [ "$fail" = 0 ]; then echo "ALL PASS"; else echo "SOME FAILED"; exit 1; fi
