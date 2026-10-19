#!/usr/bin/env bash
# ABOUTME: Fixture tests for quickfix's Discard.sh — removes only the quickfix worktree and
# ABOUTME: branch; the user's checkout, branch and ignored files are untouched.
set -uo pipefail
DIR="$(cd "$(dirname "$0")" && pwd)"
. "$DIR/fixture_helpers.sh"
SCRIPT="$(stage_script "$DIR/Discard.sh")"
PREFLIGHT="$(stage_script "$DIR/Preflight.sh")"
new_repo
echo '.env' > "$WORK/.gitignore"; G add .gitignore; G commit -q -m ignore
echo 'SECRET=1' > "$WORK/.env"
echo dirty >> "$WORK/README.md"
qf_run "$PREFLIGHT"
echo change >> "$WT/README.md"
BEFORE="$(G status --porcelain --ignored)"
HEAD_BEFORE="$(G rev-parse HEAD)"

qf_run "$SCRIPT"
check "discard exits 0"                 "0" "$RC"
check "worktree removed"                "no" "$(exists "$WT")"
check "branch removed"                  "no" "$(G show-ref --verify -q "refs/heads/$BR" && echo yes || echo no)"
check "ignored file survives"           "SECRET=1" "$(cat "$WORK/.env")"
check "user edits survive"              "yes" "$(contains "$(cat "$WORK/README.md")" dirty)"
check "user HEAD unchanged"             "$HEAD_BEFORE" "$(G rev-parse HEAD)"
check "user still on main"              "main" "$(G branch --show-current)"
check "user status unchanged"           "$BEFORE" "$(G status --porcelain --ignored)"
check "reports the discard"             "yes" "$(has "discarded $BR")"

# Re-running with nothing left is not an error.
qf_run "$SCRIPT"
check "second discard exits 0"          "0" "$RC"

exit "$fail"
