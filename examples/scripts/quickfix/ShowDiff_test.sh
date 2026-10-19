#!/usr/bin/env bash
# ABOUTME: Fixture tests for quickfix's ShowDiff.sh — the worktree's diff against the base
# ABOUTME: (new files included), the agent's summary, and a truncation notice past the limit.
set -uo pipefail
DIR="$(cd "$(dirname "$0")" && pwd)"
. "$DIR/test_helpers.sh"
SCRIPT="$(stage_script "$DIR/ShowDiff.sh")"
PREFLIGHT="$(stage_script "$DIR/Preflight.sh")"
new_repo
qf_run "$PREFLIGHT"
BASE="$(cat "$WORK/.tracker/quickfix/base_sha")"

# 1. No change yet: says the change is empty.
qf_run "$SCRIPT"
check "empty: exit 0"                   "0" "$RC"
check "empty: says so"                  "yes" "$(has 'no changes')"

# 2. An edit and a new file both show; the summary leads; .tracker/ never does.
echo "fixed" >> "$WT/README.md"
echo "new file" > "$WT/added.txt"
mkdir -p "$WT/.tracker/quickfix"
echo "Fixed the README typo." > "$WT/.tracker/quickfix/summary.md"
qf_run "$SCRIPT"
check "diff: exit 0"                    "0" "$RC"
check "diff: summary shown"             "yes" "$(has 'Fixed the README typo.')"
check "diff: edit shown"                "yes" "$(has_line '+fixed')"
check "diff: new file shown"            "yes" "$(has_line '+new file')"
check "diff: .tracker not shown"        "no" "$(has 'summary.md')"
check "diff: stat shown"                "yes" "$(has_re 'added.txt +\| +1')"
check "diff: not truncated"             "no" "$(has 'diff truncated')"
check "user checkout untouched"         "" "$(G status --porcelain)"
check "worktree index untouched"        "" "$(git -C "$WT" diff --cached --name-only)"
check "new file still untracked"        "yes" "$(has_line_in "$(git -C "$WT" ls-files --others --exclude-standard)" added.txt)"
check "nothing committed"               "$BASE" "$(G rev-parse "$BR")"

# 3. No summary written: says so instead of showing nothing.
rm -f "$WT/.tracker/quickfix/summary.md"
qf_run "$SCRIPT"
check "no summary: noted"               "yes" "$(has 'no summary was written')"

# 4. A diff past the limit: bounded, and the full-diff command is printed.
awk 'BEGIN { for (i = 0; i < 3000; i++) print "line " i " of a large generated change" }' > "$WT/big.txt"
qf_run "$SCRIPT" QUICKFIX_DIFF_LIMIT=2000
check "big: exit 0"                     "0" "$RC"
check "big: truncation notice"          "yes" "$(has 'diff truncated')"
check "big: full-diff command"          "yes" "$(has "git -C \"$WT\" diff $BASE")"
check "big: output bounded"             "yes" "$([ "${#OUT}" -lt 6000 ] && echo yes || echo no)"
check "big: notice is last"             "yes" "$(contains "$(last_line "$OUT")" "git -C \"$WT\" diff $BASE")"

# 5. The user's checkout changed since Preflight (an agent that left its
#    worktree): the gate gets a loud warning naming what changed.
rm -f "$WT/big.txt"
qf_run "$SCRIPT"
check "outside clean: no warning"       "no" "$(has 'OUTSIDE the quickfix worktree')"
echo "stray" >> "$WORK/README.md"
qf_run "$SCRIPT"
check "outside change: warned"          "yes" "$(has 'OUTSIDE the quickfix worktree')"
check "outside change: names the file"  "yes" "$(has 'README.md')"

exit "$fail"
