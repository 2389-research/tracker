#!/usr/bin/env bash
# ABOUTME: Fixture tests for SetupWorktrees.sh (#646) — records the fork sha,
# ABOUTME: refuses a commitless repo, replaces stale worktrees, and never
# ABOUTME: deletes a previous run's UNMERGED impl/* branch silently (merged →
# ABOUTME: deleted with a log line; unmerged → renamed impl/<n>-abandoned-<sha>).
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
SCRIPT="$(stage_script "$DIR/SetupWorktrees.sh")"
run() { OUT="$( (cd "$WORK" && ${TEST_SH:-sh} "$SCRIPT") 2>"$STATE/stderr")"; RC=$?; }
last() { printf '%s' "$OUT" | tail -1; }
has() { printf '%s' "$OUT" | grep -qF -- "$1" && echo yes || echo no; }
G() { git -C "$WORK" -c user.name=t -c user.email=t@t "$@"; }
branches() { G branch --list 'impl/*' --format='%(refname:short)' | paste -sd' ' -; }
reset() { rm -rf "$WORK"; mkdir -p "$WORK"; G -c init.defaultBranch=main init -q; }

# 1. Commitless repo → exit 1, no worktrees.
reset
run
check "commitless: exit 1"                "1" "$RC"
check "commitless: message"               "yes" "$(has 'has no commits')"
check "commitless: no marker"             "no" "$(has 'worktrees-ready')"

# 2. Fresh: three worktrees + branches at HEAD, fork sha recorded, marker.
echo base > "$WORK/README.md"; G add -A; G commit -q -m base
run
check "fresh: exit 0"                     "0" "$RC"
check "fresh: marker last"                "worktrees-ready" "$(last)"
check "fresh: branches"                   "impl/claude impl/codex impl/gemini" "$(branches)"
check "fresh: worktree dirs"              "3" "$(ls -d "$WORK"/.ai/worktrees/* | wc -l | tr -d ' ')"
check "fresh: base-sha = HEAD"            "$(G rev-parse HEAD)" "$(cat "$WORK/.ai/candidates/base-sha")"
check "fresh: worktree at HEAD"           "$(G rev-parse HEAD)" "$(git -C "$WORK/.ai/worktrees/codex" rev-parse HEAD)"

# 3. Re-run on the same repo: the (merged, empty) branches are deleted with a
#    log line and recreated; stale worktrees replaced.
run
check "rerun: exit 0"                     "0" "$RC"
check "rerun: merged deleted logged"      "yes" "$(has 'deleted impl/claude (already merged into HEAD)')"
check "rerun: stale worktree logged"      "yes" "$(has 'removing stale worktree .ai/worktrees/claude')"
check "rerun: branches recreated"         "impl/claude impl/codex impl/gemini" "$(branches)"

# 4. A previous run's UNMERGED branch (commits not in HEAD) is RENAMED, not
#    deleted; its commit stays reachable; the new impl/<n> branch is fresh.
echo work > "$WORK/.ai/worktrees/gemini/work.txt"
git -C "$WORK/.ai/worktrees/gemini" add -A
git -C "$WORK/.ai/worktrees/gemini" -c user.name=t -c user.email=t@t commit -q -m "gemini work"
SHA=$(git -C "$WORK/.ai/worktrees/gemini" rev-parse --short HEAD)
run
check "unmerged: exit 0"                  "0" "$RC"
check "unmerged: renamed logged"          "yes" "$(has "renamed unmerged impl/gemini from a previous run to impl/gemini-abandoned-$SHA")"
check "unmerged: abandoned branch exists" "yes" "$(G rev-parse --verify --quiet "refs/heads/impl/gemini-abandoned-$SHA" >/dev/null && echo yes || echo no)"
check "unmerged: work reachable"          "gemini work" "$(G log -1 --format=%s "impl/gemini-abandoned-$SHA")"
check "unmerged: fresh impl/gemini"       "$(G rev-parse HEAD)" "$(G rev-parse impl/gemini)"
check "unmerged: no -D in output"         "no" "$(has 'deleted impl/gemini')"

# 5. Detached HEAD works (no branch name needed).
G checkout -q --detach HEAD
run
check "detached: exit 0"                  "0" "$RC"
check "detached: marker"                  "worktrees-ready" "$(last)"

[ "$fail" = 0 ] && echo "ALL PASS" || { echo "SOME FAILED"; exit 1; }
