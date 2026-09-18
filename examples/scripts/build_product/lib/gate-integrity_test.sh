#!/usr/bin/env bash
# ABOUTME: Fixture tests for lib/gate-integrity.sh (#640 D6) — restore_gate_files
# ABOUTME: re-emits verify.sh / ci-probe.sh from the workflow lib and names a
# ABOUTME: RESTORED copy that differed; hatch_entries drops comment/blank lines;
# ABOUTME: snapshot_hatch_files baselines known_* + operator stamps once at
# ABOUTME: milestone start; report_hatch_additions prints exactly the entries
# ABOUTME: and stamps that appeared since, and never baselines on its own.
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
# A driver that sources the lib the way the nodes do (set -eu, POSIX sh) and
# runs ONE helper with its arguments; a trailer proves the helper returned 0
# under set -e (a `return 1` / abort would drop it).
cat > "$STATE/driver.sh" <<DRIVER
set -eu
. "$LIB_DIR/gate-integrity.sh"
# gate LIB — TestMilestone / FinalBuild's sequence: restore, then report.
gate() { restore_gate_files "\$1"; report_hatch_additions; }
"\$@"
echo "helper-rc=0"
DRIVER
# run HELPER [ARGS…] — OUT is stdout (stderr in $STATE/stderr), RC its exit.
run() { OUT="$( (cd "$WORK" && ${TEST_SH:-sh} "$STATE/driver.sh" "$@") 2>"$STATE/stderr")"; RC=$?; }
# body — OUT without the trailer, one line per output line joined by '|'.
body() { printf '%s\n' "$OUT" | grep -vx 'helper-rc=0' | paste -sd'|' -; }
ohas() { printf '%s' "$OUT" | grep -qF -- "$1" && echo yes || echo no; }
# oline LINE — yes/no: is LINE (exactly, whole line) in OUT?
oline() { printf '%s\n' "$OUT" | grep -qxF -- "$1" && echo yes || echo no; }
exists() { [ -e "$WORK/$1" ] && echo present || echo gone; }
MS="$WORK/.ai/milestones"
KF="$MS/known_failures"; KL="$MS/known_lint_failures"; STAMP="$WORK/.ai/build/no-tests-ok"
# A private copy of the workflow lib so the "sidecar incomplete" case can
# delete a file without touching the real tree.
LIBCOPY="$STATE/lib"; mkdir -p "$LIBCOPY"; cp "$LIB_DIR/verify.sh" "$LIB_DIR/ci-probe.sh" "$LIBCOPY/"
reset() { rm -rf "$WORK"; mkdir -p "$WORK/.ai/build" "$MS"; }

# ── restore_gate_files ──────────────────────────────────────────────────
# 1. Nothing under .ai/build yet (a pre-#640 resume): both files are
#    installed byte-identical to the lib, no WARNING, .ai/build created.
rm -rf "$WORK"; mkdir -p "$WORK"
run restore_gate_files "$LIB_DIR"
check "restore: exit 0"                     "0" "$RC"
check "restore: no output"                  "helper-rc=0" "$OUT"
check "restore: verify.sh installed"        "yes" "$(cmp -s "$LIB_DIR/verify.sh" "$WORK/.ai/build/verify.sh" && echo yes || echo no)"
check "restore: ci-probe.sh installed"      "yes" "$(cmp -s "$LIB_DIR/ci-probe.sh" "$WORK/.ai/build/ci-probe.sh" && echo yes || echo no)"

# 2. Identical copies already present (the steady state): silent, no false
#    RESTORED positive on a run where nothing rewrote the gate.
run restore_gate_files "$LIB_DIR"
check "restore identical: exit 0"           "0" "$RC"
check "restore identical: no WARNING"       "no" "$(ohas 'WARNING')"
check "restore identical: silent"           "helper-rc=0" "$OUT"

# 3. An agent rewrote .ai/build/verify.sh to `exit 0`: the WARNING names
#    verify.sh (and only verify.sh), says RESTORED, and the lib copy is back.
printf 'exit 0\n' > "$WORK/.ai/build/verify.sh"
run restore_gate_files "$LIB_DIR"
check "restore rewritten: exit 0"           "0" "$RC"
check "restore rewritten: WARNING line"     "WARNING: .ai/build/verify.sh differed from the workflow's lib/verify.sh and was RESTORED — something in the workdir rewrote the gate script (#640 D6)" "$(body)"
check "restore rewritten: ci-probe silent"  "no" "$(ohas 'ci-probe.sh differed')"
check "restore rewritten: verify.sh back"   "yes" "$(cmp -s "$LIB_DIR/verify.sh" "$WORK/.ai/build/verify.sh" && echo yes || echo no)"
# ...and the same for ci-probe.sh, with both rewritten at once: two lines.
printf 'exit 0\n' > "$WORK/.ai/build/verify.sh"; printf '#!/bin/sh\n' > "$WORK/.ai/build/ci-probe.sh"
run restore_gate_files "$LIB_DIR"
check "restore both: two WARNINGs"          "2" "$(printf '%s\n' "$OUT" | grep -c 'and was RESTORED')"
check "restore both: verify.sh named"       "yes" "$(ohas 'WARNING: .ai/build/verify.sh differed')"
check "restore both: ci-probe.sh named"     "yes" "$(ohas 'WARNING: .ai/build/ci-probe.sh differed')"
check "restore both: ci-probe.sh back"      "yes" "$(cmp -s "$LIB_DIR/ci-probe.sh" "$WORK/.ai/build/ci-probe.sh" && echo yes || echo no)"
run restore_gate_files "$LIB_DIR"
check "restore after restore: silent"       "helper-rc=0" "$OUT"

# 4. The workflow sidecar itself is incomplete (lib/ci-probe.sh missing):
#    fail loud naming the file, exit 1, and do NOT touch the existing copy
#    (verify.sh, listed first, is still re-emitted — the loop stops at the
#    missing file).
rm -f "$LIBCOPY/ci-probe.sh"
printf 'stale\n' > "$WORK/.ai/build/ci-probe.sh"
run restore_gate_files "$LIBCOPY"
check "restore incomplete: exit 1"          "1" "$RC"
check "restore incomplete: ERROR names file" "ERROR: $LIBCOPY/ci-probe.sh missing — workflow sidecar incomplete; cannot restore the gate" "$(body)"
check "restore incomplete: no trailer"      "no" "$(ohas 'helper-rc=0')"
check "restore incomplete: copy untouched"  "stale" "$(cat "$WORK/.ai/build/ci-probe.sh")"
cp "$LIB_DIR/ci-probe.sh" "$LIBCOPY/"

# ── hatch_entries ───────────────────────────────────────────────────────
# 5. Comment lines (also indented ones), blank and whitespace-only lines
#    are dropped; entries keep their order and their inline text (a
#    trailing `# reason` on an entry line is part of the entry, not a
#    comment); an absent file yields nothing and exit 0.
reset
printf '# header comment\n\nTestA\n   # indented comment\n   \nTestB # reason kept\n\t\nTestC\n' > "$KF"
run hatch_entries .ai/milestones/known_failures
check "entries: exit 0"                     "0" "$RC"
check "entries: filtered, ordered"          "TestA|TestB # reason kept|TestC" "$(body)"
run hatch_entries .ai/milestones/absent
check "entries absent: exit 0"              "0" "$RC"
check "entries absent: nothing"             "helper-rc=0" "$OUT"
printf '# only\n\n# comments\n' > "$KL"
run hatch_entries .ai/milestones/known_lint_failures
check "entries comments-only: nothing"      "helper-rc=0" "$OUT"
check "entries comments-only: exit 0"       "0" "$RC"

# ── snapshot_hatch_files ────────────────────────────────────────────────
# 6. Milestone start with NO hatch files and no stamp: every snapshot is
#    created EMPTY (its existence is the "baseline taken" signal), from a
#    workdir where .ai/milestones does not even exist yet.
rm -rf "$WORK"; mkdir -p "$WORK"
run snapshot_hatch_files
check "snapshot empty: exit 0"              "0" "$RC"
check "snapshot empty: silent"              "helper-rc=0" "$OUT"
check "snapshot empty: known_failures"      "present" "$(exists .ai/milestones/known_failures.snapshot)"
check "snapshot empty: known_lint_failures" "present" "$(exists .ai/milestones/known_lint_failures.snapshot)"
check "snapshot empty: opt-outs"            "present" "$(exists .ai/milestones/opt-outs.snapshot)"
check "snapshot empty: all zero bytes"      "0" "$(cat "$MS"/*.snapshot | wc -c | tr -d ' ')"
check "snapshot empty: known_failures not created" "gone" "$(exists .ai/milestones/known_failures)"

# 7. Pre-existing entries (with comments/blanks) and a pre-existing operator
#    stamp are baselined — comment lines never enter the snapshot, and the
#    stamp is recorded by its path.
reset
printf '# why\nTestOld\n\nTestOld2\n' > "$KF"; printf 'G404\n' > "$KL"; : > "$STAMP"
run snapshot_hatch_files
check "snapshot baseline: exit 0"           "0" "$RC"
check "snapshot baseline: known_failures"   "TestOld|TestOld2" "$(paste -sd'|' - < "$KF.snapshot")"
check "snapshot baseline: no comment"       "0" "$(grep -c '#' "$KF.snapshot")"
check "snapshot baseline: lint"             "G404" "$(cat "$KL.snapshot")"
check "snapshot baseline: stamp recorded"   ".ai/build/no-tests-ok" "$(cat "$MS/opt-outs.snapshot")"

# 8. Never overwritten: additions after the baseline (an agent's, or a
#    stamp that appeared later) do not move the snapshot on a second call —
#    the milestone's baseline is taken exactly once (PickNextMilestone runs
#    once per milestone, but a retry must not re-baseline either).
echo TestNew >> "$KF"; echo G505 >> "$KL"
rm -f "$STAMP"
run snapshot_hatch_files
check "snapshot once: exit 0"               "0" "$RC"
check "snapshot once: known_failures kept"  "TestOld|TestOld2" "$(paste -sd'|' - < "$KF.snapshot")"
check "snapshot once: lint kept"            "G404" "$(cat "$KL.snapshot")"
check "snapshot once: opt-outs kept"        ".ai/build/no-tests-ok" "$(cat "$MS/opt-outs.snapshot")"
# A single missing snapshot is (re)created without touching the others.
rm -f "$KL.snapshot"
run snapshot_hatch_files
check "snapshot partial: lint recreated"    "G404|G505" "$(paste -sd'|' - < "$KL.snapshot")"
check "snapshot partial: known_failures kept" "TestOld|TestOld2" "$(paste -sd'|' - < "$KF.snapshot")"

# ── report_hatch_additions ──────────────────────────────────────────────
# 9. Nothing changed since the baseline: NO output at all (the gate log
#    stays clean — no false positives), exit 0. Also with hatch files that
#    do not exist and an empty baseline.
reset
printf 'TestOld\n' > "$KF"; printf 'TestOld\n' > "$KF.snapshot"; : > "$KL.snapshot"; : > "$MS/opt-outs.snapshot"
run report_hatch_additions
check "report unchanged: exit 0"            "0" "$RC"
check "report unchanged: no output"         "helper-rc=0" "$OUT"
rm -f "$KF"
run report_hatch_additions
check "report no hatch files: no output"    "helper-rc=0" "$OUT"
# Comment / blank lines added after the baseline are not additions.
printf '# note\n\nTestOld\n   # another\n' > "$KF"
run report_hatch_additions
check "report comments only: no output"     "helper-rc=0" "$OUT"
# An entry REMOVED since the baseline is not an addition either.
: > "$KF"
run report_hatch_additions
check "report removal: no output"           "helper-rc=0" "$OUT"

# 10. Additions: exactly the new entries, each as `  + entry`, under the
#     per-file header — a baselined entry is not repeated; the diff is
#     exact-line (TestOld does not cover TestOld2) and fixed-string (regex
#     metacharacters in a baselined entry match themselves, not a pattern).
printf 'TestOld\nTestOld2\n# c\nTestNew\nTestParse/empty[0]\n' > "$KF"
printf 'TestOld\nTestParse/empty[0]\n' > "$KF.snapshot"
run report_hatch_additions
check "report added: exit 0"                "0" "$RC"
check "report added: header + entries"      "--- known_failures: entries ADDED since milestone start (#640 D6 — VerifyMilestone: a finding unless justified) ---|  + TestOld2|  + TestNew" "$(body)"
check "report added: baselined not listed"  "no" "$(oline '  + TestOld')"
check "report added: regex entry not listed" "no" "$(oline '  + TestParse/empty[0]')"
# The exact-line rule cuts both ways: a baselined `TestParse/empty[0]` must
# NOT cover `TestParse/empty` or `TestParse/empty[0]x`.
printf 'TestParse/empty\nTestParse/empty[0]x\n' > "$KF"
run report_hatch_additions
check "report added: substring not covered" "  + TestParse/empty|  + TestParse/empty[0]x" "$(body | sed 's/^[^|]*|//')"
# Both hatch files at once: two sections, each named.
printf 'TestOld\nTestNew\n' > "$KF"; printf 'TestOld\n' > "$KF.snapshot"
printf 'G404\n# c\nG505\n' > "$KL"; printf 'G404\n' > "$KL.snapshot"
run report_hatch_additions
check "report both: two sections"           "2" "$(printf '%s\n' "$OUT" | grep -c 'entries ADDED since milestone start')"
check "report both: lint section"           "yes" "$(ohas '--- known_lint_failures: entries ADDED')"
check "report both: lint entry"             "yes" "$(ohas '  + G505')"
check "report both: lint baselined silent"  "no" "$(ohas '  + G404')"
check "report both: order"                  "known_failures|known_lint_failures" "$(printf '%s\n' "$OUT" | sed -n 's/^--- \(known_[a-z_]*\):.*/\1/p' | paste -sd'|' -)"
# An EMPTY baseline (the hatch file did not exist at milestone start): every
# entry is an addition — `grep -f` on an empty pattern file must match
# nothing, not everything.
printf 'TestA\nTestB\n' > "$KF"; : > "$KF.snapshot"; rm -f "$KL"
run report_hatch_additions
check "report empty baseline: all added"    "  + TestA|  + TestB" "$(body | sed 's/^[^|]*|//')"

# 11. Missing snapshot (PickNextMilestone did not run — a pre-#640 resume):
#     WARNING naming the file, EVERY entry listed, and the snapshot is NOT
#     created here (report never baselines). A hatch file that is absent
#     produces neither a WARNING nor a listing.
reset
printf '# c\nTestOld\nTestNew\n' > "$KF"
run report_hatch_additions
check "report no snapshot: exit 0"          "0" "$RC"
check "report no snapshot: WARNING"         "yes" "$(ohas 'WARNING: no milestone-start snapshot for known_failures (PickNextMilestone did not run?) — listing every entry')"
check "report no snapshot: everything listed" "  + TestOld|  + TestNew" "$(body | sed 's/^[^|]*|[^|]*|//')"
check "report no snapshot: lint silent"     "no" "$(ohas 'snapshot for known_lint_failures')"
check "report no snapshot: not created"     "gone" "$(exists .ai/milestones/known_failures.snapshot)"
# A hatch file with only comments and no snapshot: the WARNING still fires
# (the file exists) but there is no ADDED section (nothing to list).
printf '# only comments\n' > "$KF"
run report_hatch_additions
check "report no snapshot, empty: WARNING"  "yes" "$(ohas 'WARNING: no milestone-start snapshot for known_failures')"
check "report no snapshot, empty: no section" "no" "$(ohas 'entries ADDED')"

# 12. Operator stamp: baselined at milestone start → silent; created after
#     the baseline → reported as CREATED with its path; absent → nothing;
#     present with NO opt-outs snapshot at all → reported (never assumed
#     pre-existing).
reset
: > "$STAMP"; printf '.ai/build/no-tests-ok\n' > "$MS/opt-outs.snapshot"
run report_hatch_additions
check "stamp baselined: no output"          "helper-rc=0" "$OUT"
: > "$MS/opt-outs.snapshot"
run report_hatch_additions
check "stamp created: exit 0"               "0" "$RC"
check "stamp created: block"                "--- operator stamp CREATED since milestone start (#640 D6 — VerifyMilestone: a finding unless an operator created it) ---|  + .ai/build/no-tests-ok" "$(body)"
rm -f "$MS/opt-outs.snapshot"
run report_hatch_additions
check "stamp no snapshot: reported"         "yes" "$(ohas '  + .ai/build/no-tests-ok')"
check "stamp no snapshot: no hatch WARNING" "no" "$(ohas 'WARNING')"
rm -f "$STAMP"
run report_hatch_additions
check "stamp absent: no output"             "helper-rc=0" "$OUT"

# 13. End to end, as the nodes sequence it: PickNextMilestone snapshots at
#     milestone start, Implement adds a hatch entry + a stamp, TestMilestone
#     restores the gate and reports both — then MarkMilestoneDone removes
#     the snapshots and the next milestone re-baselines the new state.
reset
printf 'TestOld\n' > "$KF"
run snapshot_hatch_files                                   # PickNextMilestone
printf 'exit 0\n' > "$WORK/.ai/build/verify.sh"            # Implement rewrote the gate
echo TestNew >> "$KF"; : > "$STAMP"                        # ...and widened the hatches
run gate "$LIB_DIR"                                        # TestMilestone
check "e2e: exit 0"                         "0" "$RC"
check "e2e: RESTORED"                       "yes" "$(ohas 'WARNING: .ai/build/verify.sh differed')"
check "e2e: entry added"                    "yes" "$(ohas '  + TestNew')"
check "e2e: baselined silent"               "no"  "$(oline '  + TestOld')"
check "e2e: stamp created"                  "yes" "$(ohas '  + .ai/build/no-tests-ok')"
rm -f "$MS"/*.snapshot                                     # MarkMilestoneDone
run snapshot_hatch_files                                   # next PickNextMilestone
run report_hatch_additions                                 # next TestMilestone
check "e2e: next milestone clean"           "helper-rc=0" "$OUT"

if [ "$fail" = 0 ]; then echo "ALL PASS"; else echo "SOME FAILED"; exit 1; fi
