#!/usr/bin/env bash
# ABOUTME: Fixture tests for SetupWorkspace.sh (#646) — scaffolds .ai/, seeds
# ABOUTME: `.ai/` into .gitignore append-if-absent (no newline glue, no sort),
# ABOUTME: excludes .tracker/ via the LOCAL info/exclude, resets stale
# ABOUTME: candidate evidence, and fails loud when ${graph.workflow_dir} is empty.
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
SCRIPT="$(stage_script "$DIR/SetupWorkspace.sh")"
run() { OUT="$( (cd "$WORK" && ${TEST_SH:-sh} "$SCRIPT") 2>"$STATE/stderr")"; RC=$?; }
last() { printf '%s' "$OUT" | tail -1; }
G() { git -C "$WORK" -c user.name=t -c user.email=t@t "$@"; }

# 1. Non-git dir: scaffold + .gitignore seeded, marker last.
run
check "nongit: exit 0"                 "0" "$RC"
check "nongit: marker"                 "workspace-ready" "$(last)"
check "nongit: dirs"                   "3" "$(ls -d "$WORK"/.ai/worktrees "$WORK"/.ai/candidates "$WORK"/.ai/decisions | wc -l | tr -d ' ')"
check "nongit: .gitignore"             ".ai/" "$(cat "$WORK/.gitignore")"

# 2. #640 C2/C3 class: a .gitignore with no trailing newline is NOT glued, an
#    existing entry is not duplicated, and negation order is preserved (no
#    sort -u).
printf '*.log\n!keep.log\nbuild' > "$WORK/.gitignore"
run
check "glue: appended on its own line"  $'*.log\n!keep.log\nbuild\n.ai/' "$(cat "$WORK/.gitignore")"
run
check "idempotent: one .ai/ line"       "1" "$(grep -c '^\.ai/$' "$WORK/.gitignore")"

# 3. In a git repo: .tracker/ goes to the LOCAL info/exclude, never .gitignore;
#    stale candidate evidence from a previous run is reset.
G -c init.defaultBranch=main init -q
mkdir -p "$WORK/.ai/candidates"; echo old > "$WORK/.ai/candidates/claude.diff"; echo old > "$WORK/.ai/candidates/base-sha"
run
check "git: exit 0"                    "0" "$RC"
check "git: .tracker/ excluded locally" "yes" "$(grep -qxF '.tracker/' "$WORK/$(G rev-parse --git-path info/exclude)" && echo yes || echo no)"
check "git: .tracker/ not in .gitignore" "no" "$(grep -q 'tracker' "$WORK/.gitignore" && echo yes || echo no)"
check "git: stale diff reset"          "gone" "$([ -e "$WORK/.ai/candidates/claude.diff" ] && echo present || echo gone)"
check "git: stale base-sha reset"      "gone" "$([ -e "$WORK/.ai/candidates/base-sha" ] && echo present || echo gone)"

# 3b. In a repo WITH commits the `.ai/` rule is committed by name (so a
#     candidate editing .gitignore cannot fake a conflict); a .gitignore
#     dirty for other reasons is reported and left alone; other staged
#     files are not swept in.
rm -rf "$WORK"; mkdir -p "$WORK"; G -c init.defaultBranch=main init -q
printf '*.log\n' > "$WORK/.gitignore"; echo x > "$WORK/README.md"; G add -A; G commit -q -m base
echo staged > "$WORK/other.txt"; G add other.txt
run
check "commit rule: exit 0"            "0" "$RC"
check "commit rule: committed"         "chore(ask_and_execute): ignore .ai/ run metadata" "$(G log -1 --format=%s)"
check "commit rule: log line"          "yes" "$(printf '%s' "$OUT" | grep -q 'committed .gitignore' && echo yes || echo no)"
check "commit rule: .gitignore at HEAD" "yes" "$(G show HEAD:.gitignore | grep -qx '.ai/' && echo yes || echo no)"
check "commit rule: other.txt not swept" "A  other.txt" "$(G status --porcelain -- other.txt)"
G reset -q other.txt; rm -f "$WORK/other.txt"
echo '*.tmp' >> "$WORK/.gitignore"
run
check "dirty gitignore: exit 0"        "0" "$RC"
check "dirty gitignore: NOTE"          "yes" "$(printf '%s' "$OUT" | grep -q 'NOTE: .gitignore has other uncommitted changes' && echo yes || echo no)"
check "dirty gitignore: not committed" "chore(ask_and_execute): ignore .ai/ run metadata" "$(G log -1 --format=%s)"

# 4. Empty ${graph.workflow_dir} (packed .dipx / failed materialization) →
#    fail loud before touching anything.
sed 's|LIB="[^"]*"|LIB=""|; s|\[ -n "[^"]*" \] \|\| {|[ -n "" ] \|\| {|' "$SCRIPT" > "$STATE/empty.sh"
OUT="$( (cd "$WORK" && ${TEST_SH:-sh} "$STATE/empty.sh") 2>&1)"; RC=$?
check "empty workflow_dir: exit 1"     "1" "$RC"
check "empty workflow_dir: message"    "yes" "$(printf '%s' "$OUT" | grep -q 'graph.workflow_dir is empty' && echo yes || echo no)"

[ "$fail" = 0 ] && echo "ALL PASS" || { echo "SOME FAILED"; exit 1; }
