#!/usr/bin/env bash
# ABOUTME: Fixture tests for quickfix's Preflight.sh — a worktree on a new branch from HEAD,
# ABOUTME: the user's checkout untouched, stale state refused with cleanup commands.
set -uo pipefail
DIR="$(cd "$(dirname "$0")" && pwd)"
. "$DIR/test_helpers.sh"
SCRIPT="$(stage_script "$DIR/Preflight.sh")"

# 1. Not a git repo: clear failure, nothing created.
rm -rf "$WORK"; mkdir -p "$WORK"
qf_run "$SCRIPT"
check "no repo exits 1"                 "1" "$RC"
check "no repo says why"                "yes" "$(has_re_in "$ERR" 'ERROR: .*not a git repository')"
check "no repo creates no worktree"     "no" "$(exists "$WT")"

# 2. A repo with no commits: clear failure.
init_repo
qf_run "$SCRIPT"
check "no commits exits 1"              "1" "$RC"
check "no commits says why"             "yes" "$(has_re_in "$ERR" 'ERROR: .*no commits')"

# 3. Clean repo (path has a space): worktree + branch from HEAD, state recorded.
new_repo
BASE="$(G rev-parse HEAD)"
qf_run "$SCRIPT"
check "clean exits 0"                   "0" "$RC"
check "marker last"                     "preflight-ok" "$(last_line "$OUT")"
check "worktree created"                "yes" "$(exists "$WT/README.md")"
check "branch created at HEAD"          "$BASE" "$(G rev-parse "$BR")"
check "worktree is on the branch"       "$BR" "$(git -C "$WT" branch --show-current)"
check "user stays on main"              "main" "$(G branch --show-current)"
check "user checkout clean"             "" "$(G status --porcelain)"
check ".tracker/ excluded via git-path" "yes" "$(has_line_in "$(cat "$WORK/.git/info/exclude")" '.tracker/')"
check "quickfix.json branch"            "yes" "$(contains "$(cat "$RUN_DIR/quickfix.json")" "\"branch\": \"$BR\"")"
check "quickfix.json base_sha"          "yes" "$(contains "$(cat "$RUN_DIR/quickfix.json")" "\"base_sha\": \"$BASE\"")"
check "quickfix.json worktree"          "yes" "$(contains "$(cat "$RUN_DIR/quickfix.json")" "\"worktree\": \"$WT\"")"
check "base recorded for later nodes"   "$BASE" "$(cat "$WORK/.tracker/quickfix/base_sha")"
check "reports the branch"              "yes" "$(has "$BR")"
check "clean: no dirty note"            "no" "$(has 'uncommitted changes')"

# 4. Re-run with the worktree still there: refused, cleanup commands, nothing new.
qf_run "$SCRIPT" TRACKER_RUN_ID=run-456
check "stale worktree exits 1"          "1" "$RC"
check "stale: names the path"           "yes" "$(contains "$ERR" "$WT")"
check "stale: worktree remove command"  "yes" "$(contains "$ERR" "git worktree remove --force \"$WT\"")"
check "stale: branch delete command"    "yes" "$(contains "$ERR" "git branch -D $BR")"
check "stale: no new branch"            "no" "$(G show-ref --verify -q refs/heads/tracker/quickfix/run-456 && echo yes || echo no)"

# 5. Branch left behind without the worktree: refused, nothing created.
G worktree remove --force "$WT"
qf_run "$SCRIPT"
check "stale branch exits 1"            "1" "$RC"
check "stale branch: delete command"    "yes" "$(contains "$ERR" "git branch -D $BR")"
check "stale branch: no worktree"       "no" "$(exists "$WT")"
G branch -q -D "$BR"

# 6. Dirty user checkout: noted, left exactly as it was, not part of the base.
echo edited >> "$WORK/README.md"
echo new > "$WORK/untracked.txt"
BEFORE="$(G status --porcelain)"
qf_run "$SCRIPT"
check "dirty exits 0"                   "0" "$RC"
check "dirty is noted"                  "yes" "$(has 'uncommitted changes')"
check "dirty tree untouched"            "$BEFORE" "$(G status --porcelain)"
check "dirty edits not in worktree"     "base" "$(cat "$WT/README.md")"
check "untracked not in worktree"       "no" "$(exists "$WT/untracked.txt")"
G worktree remove --force "$WT"; G branch -q -D "$BR"
G checkout -q -- README.md; rm -f "$WORK/untracked.txt"

# 7. Detached HEAD works; the branch starts there.
G checkout -q --detach
qf_run "$SCRIPT"
check "detached exits 0"                "0" "$RC"
check "detached base is HEAD"           "$(G rev-parse HEAD)" "$(G rev-parse "$BR")"
G worktree remove --force "$WT"; G branch -q -D "$BR"; G checkout -q main

# 8. Run from inside a linked worktree: the exclude lands in the common
#    info/exclude and the quickfix worktree is created.
G worktree add -q "$ROOT/linked wt" -b feature
LINKED="$ROOT/linked wt"
OUT="$( (cd "$LINKED" && env TRACKER_RUN_ID="$RUN_ID" TRACKER_RUN_DIR="$LINKED/.tracker/runs/$RUN_ID" ${TEST_SH:-sh} "$SCRIPT") 2>"$STATE/stderr")"; RC=$?
check "linked worktree exits 0"         "0" "$RC"
check "linked: quickfix worktree made"  "yes" "$(exists "$LINKED/.tracker/quickfix/worktree/README.md")"
check "linked: common exclude has it"   "yes" "$(has_line_in "$(cat "$WORK/.git/info/exclude")" '.tracker/')"
check "linked: its checkout clean"      "" "$(git -C "$LINKED" status --porcelain)"

# 9. A tracked .tracker/ in the user's repo is NOT untracked by Preflight
#    (that would stage a deletion in the user's index).
new_repo
mkdir -p "$WORK/.tracker"; echo x > "$WORK/.tracker/tracked.txt"
G add -f .tracker/tracked.txt; G commit -q -m "tracked tracker file"
qf_run "$SCRIPT"
check "tracked .tracker: exits 0"       "0" "$RC"
check "tracked .tracker: index intact"  "" "$(G status --porcelain)"

exit "$fail"
