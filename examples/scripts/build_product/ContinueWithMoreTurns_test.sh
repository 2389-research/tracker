#!/usr/bin/env bash
# ABOUTME: Fixture tests for ContinueWithMoreTurns.sh (#318 warm continue+N) —
# ABOUTME: per-loop disk counter capped at 3, MaxTurns override written to the
# ABOUTME: node-scoped .tracker/turn_overrides/Implement (50 + n*40), override
# ABOUTME: dir kept out of the product repo via info/exclude (linked-worktree aware).
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
SCRIPT="$(stage_script "$DIR/ContinueWithMoreTurns.sh")"   # ${graph.workflow_dir} expanded as the engine does
run() { OUT="$( (cd "$WORK" && ${TEST_SH:-sh} "$SCRIPT") 2>"$STATE/stderr")"; RC=$?; }
last() { printf '%s' "$OUT" | tail -1; }
OVR="$WORK/.tracker/turn_overrides"

# 1. Outside a git repo the node still works (GITDIR empty path is safe).
run
check "no-git exit 0"               "0" "$RC"
check "no-git marker"               "continue-ok" "$(last)"
check "no-git override 50+40"       "90" "$(cat "$OVR/Implement")"

# 2. In a git repo: counter, override progression, and a single exclude line.
rm -rf "$WORK/.tracker"
git -C "$WORK" -c init.defaultBranch=main init -q
run
check "continue 1 exit 0"           "0" "$RC"
check "continue 1 marker"           "continue-ok" "$(last)"
check "continue 1 counter"          "1" "$(cat "$OVR/continue_attempts")"
check "continue 1 override"         "90" "$(cat "$OVR/Implement")"
check "continue 1 message"          "yes" "$(printf '%s' "$OUT" | grep -q 'warm continue 1/3 — bumped Implement max_turns to 90' && echo yes || echo no)"
check "exclude line added"          "1" "$(grep -cxF '.tracker/turn_overrides/' "$WORK/.git/info/exclude")"
run
check "continue 2 override"         "130" "$(cat "$OVR/Implement")"
check "exclude line not duplicated" "1" "$(grep -cxF '.tracker/turn_overrides/' "$WORK/.git/info/exclude")"
run
check "continue 3 exit 0"           "0" "$RC"
check "continue 3 override"         "170" "$(cat "$OVR/Implement")"
# The override dir is invisible to `git add -A` (it lives in info/exclude).
check "override dir git-excluded"   "" "$(git -C "$WORK" status --porcelain)"

# 3. Fourth continue exceeds CAP=3 -> exit 1, marker, override NOT bumped.
run
check "continue 4 exit 1"           "1" "$RC"
check "continue 4 marker"           "continue-cap-exhausted" "$(last)"
check "cap message"                 "yes" "$(printf '%s' "$OUT" | grep -q 'continue cap (3) exhausted after 4 attempt(s)' && echo yes || echo no)"
check "override untouched at cap"   "170" "$(cat "$OVR/Implement")"
check "counter = 4"                 "4" "$(cat "$OVR/continue_attempts")"

# 4. Numeric guard: stale junk from an interrupted run resets to attempt 1.
for junk in 'garbage' '1 2' ''; do
  printf '%s\n' "$junk" > "$OVR/continue_attempts"
  run
  check "junk '$junk' -> exit 0"     "0" "$RC"
  check "junk '$junk' -> override 90" "90" "$(cat "$OVR/Implement")"
  check "junk '$junk' -> counter 1"  "1" "$(cat "$OVR/continue_attempts")"
done

# 5. Milestone boundary (MarkMilestoneDone) / Setup wipe the dir -> fresh allowance.
rm -rf "$OVR"
run
check "after wipe attempt 1"        "90" "$(cat "$OVR/Implement")"

# 6. #640 C1: in a LINKED worktree the exclude must land in the common dir
#    (the only info/exclude git reads there), so the override dir stays
#    invisible to `git add -A` in that worktree too.
echo base > "$WORK/README.md"; git -C "$WORK" add -A; git -C "$WORK" -c user.name=t -c user.email=t@t commit -q -m base
git -C "$WORK" worktree add -q "$WORK/wt" -b feature
rm -f "$WORK/.git/info/exclude"          # case 2 seeded the common dir; start clean
OUT="$( (cd "$WORK/wt" && ${TEST_SH:-sh} "$SCRIPT") 2>"$STATE/stderr")"; RC=$?
check "worktree continue exit 0"    "0" "$RC"
check "worktree override written"   "90" "$(cat "$WORK/wt/.tracker/turn_overrides/Implement")"
check "worktree override excluded"  "" "$(git -C "$WORK/wt" status --porcelain)"
check "worktree exclude in common"  "1" "$(grep -cxF '.tracker/turn_overrides/' "$WORK/.git/info/exclude")"

if [ "$fail" = 0 ]; then echo "ALL PASS"; else echo "SOME FAILED"; exit 1; fi
