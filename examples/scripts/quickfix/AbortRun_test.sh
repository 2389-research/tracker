#!/usr/bin/env bash
# ABOUTME: Fixture tests for quickfix's AbortRun.sh — always exits 1, leaves the worktree for
# ABOUTME: inspection, and prints the cleanup commands only when a worktree exists.
set -uo pipefail
DIR="$(cd "$(dirname "$0")" && pwd)"
. "$DIR/test_helpers.sh"
SCRIPT="$(stage_script "$DIR/AbortRun.sh")"
PREFLIGHT="$(stage_script "$DIR/Preflight.sh")"

# 1. Aborted before Preflight made anything: notice only.
new_repo
qf_run "$SCRIPT"
check "no worktree: exit 1"             "1" "$RC"
check "no worktree: notice"             "yes" "$(has 'RUN ABORTED')"
check "no worktree: no cleanup lines"   "no" "$(has 'git worktree remove')"

# 2. Aborted with a worktree: kept, cleanup commands printed.
qf_run "$PREFLIGHT"
echo change >> "$WT/README.md"
qf_run "$SCRIPT"
check "worktree: exit 1"                "1" "$RC"
check "worktree: kept"                  "yes" "$(exists "$WT/README.md")"
check "worktree: change kept"           "yes" "$(contains "$(cat "$WT/README.md")" change)"
check "worktree: remove command"        "yes" "$(has "git worktree remove --force \"$WT\"")"
check "worktree: branch command"        "yes" "$(has "git branch -D $BR")"

exit "$fail"
