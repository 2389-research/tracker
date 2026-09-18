#!/usr/bin/env bash
# ABOUTME: Fixture tests for SetupPhase1Worktrees.sh / lib/worktrees.sh
# ABOUTME: setup_stream_worktrees (#646 5a/5e) — refuses a commitless repo or an
# ABOUTME: uncommitted scaffold, forks one worktree + build/<stream> per stream,
# ABOUTME: records the phase base sha, replaces stale worktrees, and never
# ABOUTME: deletes an unmerged previous-run branch (renamed …-abandoned-<sha>).
# ABOUTME: The other SetupPhaseNWorktrees.sh sidecars are the same one-liner
# ABOUTME: with different stream names (pinned by the Go graph test).
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
SCRIPT="$(stage_script "$DIR/SetupPhase1Worktrees.sh")"
run() { OUT="$( (cd "$WORK" && ${TEST_SH:-sh} "$SCRIPT") 2>"$STATE/stderr")"; RC=$?; }
last() { printf '%s' "$OUT" | tail -1; }
has() { printf '%s' "$OUT" | grep -qF -- "$1" && echo yes || echo no; }
G() { git -C "$WORK" -c user.name=t -c user.email=t@t "$@"; }
branches() { G branch --list 'build/*' --format='%(refname:short)' | paste -sd' ' -; }
G -c init.defaultBranch=main init -q

# 1. Commitless → exit 1.
run
check "commitless: exit 1"            "1" "$RC"
check "commitless: message"           "yes" "$(has 'has no commits')"

# 2. Scaffold not committed (only SPEC.md at HEAD) → exit 1 naming the file.
echo spec > "$WORK/SPEC.md"; G add -A; G commit -q -m spec
mkdir -p "$WORK/docs"; echo plan > "$WORK/docs/execution-plan.md"; echo 'FR-1: {status: pending}' > "$WORK/docs/traceability.yaml"
run
check "uncommitted scaffold: exit 1"  "1" "$RC"
check "uncommitted scaffold: names"   "yes" "$(has 'docs/execution-plan.md is not committed at HEAD')"
check "uncommitted scaffold: no wt"   "gone" "$([ -d "$WORK/.ai/worktrees/stream-a" ] && echo present || echo gone)"

# 3. Committed scaffold → worktrees + branches at HEAD, base sha, marker.
G add -A; G commit -q -m scaffold
run
check "fresh: exit 0"                 "0" "$RC"
check "fresh: marker last"            "phase1-worktrees-ready" "$(last)"
check "fresh: branches"               "build/stream-a build/stream-b" "$(branches)"
check "fresh: worktree sees the plan" "plan" "$(cat "$WORK/.ai/worktrees/stream-b/docs/execution-plan.md")"
check "fresh: base sha = HEAD"        "$(G rev-parse HEAD)" "$(cat "$WORK/.ai/build/milestone-start-sha")"

# 4. Re-run: merged (empty) branches deleted with a log line, stale
#    worktrees replaced; an UNMERGED previous-run branch is renamed.
echo work > "$WORK/.ai/worktrees/stream-a/a.txt"
git -C "$WORK/.ai/worktrees/stream-a" add -A; git -C "$WORK/.ai/worktrees/stream-a" -c user.name=t -c user.email=t@t commit -q -m "stream-a work"
SHA=$(git -C "$WORK/.ai/worktrees/stream-a" rev-parse --short HEAD)
run
check "rerun: exit 0"                 "0" "$RC"
check "rerun: merged deleted logged"  "yes" "$(has 'deleted build/stream-b (already merged into HEAD)')"
check "rerun: unmerged renamed"       "yes" "$(has "renamed unmerged build/stream-a from a previous run to build/stream-a-abandoned-$SHA")"
check "rerun: abandoned reachable"    "stream-a work" "$(G log -1 --format=%s "build/stream-a-abandoned-$SHA")"
check "rerun: fresh build/stream-a"   "$(G rev-parse HEAD)" "$(G rev-parse build/stream-a)"
check "rerun: stale worktree logged"  "yes" "$(has 'removing stale worktree .ai/worktrees/stream-a')"
check "rerun: no silent -D"           "no" "$(has 'deleted build/stream-a')"

[ "$fail" = 0 ] && echo "ALL PASS" || { echo "SOME FAILED"; exit 1; }
