#!/usr/bin/env bash
# ABOUTME: Fixture tests for FinalBuild.sh (#646) — a red build/test is a
# ABOUTME: failure (no marker), the worktree count is anchored (`^worktree `),
# ABOUTME: only ACTIVE impl/{claude,codex,gemini} branches fail the check while
# ABOUTME: a renamed impl/<n>-abandoned-<sha> is a NOTE, and clean → marker.
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
install_tool_shims
SCRIPT="$(stage_script "$DIR/FinalBuild.sh")"
run() { OUT="$( (cd "$WORK" && PATH="$STATE/bin:$PATH" ${TEST_SH:-sh} "$SCRIPT") 2>"$STATE/stderr")"; RC=$?; }
last() { printf '%s' "$OUT" | tail -1; }
has() { printf '%s' "$OUT" | grep -qF -- "$1" && echo yes || echo no; }
G() { git -C "$WORK" -c user.name=t -c user.email=t@t "$@"; }
G -c init.defaultBranch=main init -q
echo base > "$WORK/README.md"; echo 'module x' > "$WORK/go.mod"; G add -A; G commit -q -m base

# 1. Clean tree, green go build/test → marker last.
run
check "green: exit 0"                    "0" "$RC"
check "green: marker"                    "verified-clean" "$(last)"
check "green: go ran"                    "go build ./...;go test ./..." "$(calls)"

# 2. Red go test → exit 1, no marker (the red output is still printed).
set_rc go test 1; rm -f "$STATE/calls"
run
check "red: exit 1"                      "1" "$RC"
check "red: no marker"                   "no" "$(has 'verified-clean')"
reset_rc

# 3. A branch merely NAMED "…worktree…" does not trip the anchored count;
#    a real leftover worktree does.
G branch feat/worktree-x
run
check "branch named worktree: exit 0"    "0" "$RC"
G worktree add -q "$WORK/.ai/worktrees/claude" -b impl/claude HEAD
run
check "leftover worktree: exit 1"        "1" "$RC"
check "leftover worktree: message"       "yes" "$(has 'leftover worktrees')"
G worktree remove --force "$WORK/.ai/worktrees/claude"

# 4. An ACTIVE impl/claude branch fails; an abandoned one is only a NOTE.
run
check "active impl branch: exit 1"       "1" "$RC"
check "active impl branch: listed"       "yes" "$(has 'impl/claude')"
G branch -q -D impl/claude
G branch impl/gemini-abandoned-abc1234
run
check "abandoned: exit 0"                "0" "$RC"
check "abandoned: NOTE"                  "yes" "$(has 'NOTE: abandoned candidate branch from an earlier run (not deleted): impl/gemini-abandoned-abc1234')"
check "abandoned: marker"                "verified-clean" "$(last)"

[ "$fail" = 0 ] && echo "ALL PASS" || { echo "SOME FAILED"; exit 1; }
