#!/usr/bin/env bash
# ABOUTME: Fixture tests for ApplyWinner.sh (#646 item 3) — the winner is read
# ABOUTME: ONLY from a `WINNER: <name>` line (never prose), zero/ambiguous/
# ABOUTME: malformed lines fail loud with NO merge, teardown happens only after
# ABOUTME: the winner is merged, losers are deleted with a log line, and the
# ABOUTME: candidate evidence in .ai/candidates/ is kept.
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
SCRIPT="$(stage_script "$DIR/ApplyWinner.sh")"
run() { OUT="$( (cd "$WORK" && ${TEST_SH:-sh} "$SCRIPT") 2>"$STATE/stderr")"; RC=$?; }
last() { printf '%s' "$OUT" | tail -1; }
has() { printf '%s' "$OUT" | grep -qF -- "$1" && echo yes || echo no; }
G() { git -C "$WORK" -c user.name=t -c user.email=t@t "$@"; }
head_subject() { G log -1 --format=%s; }
branch_exists() { G rev-parse --verify --quiet "refs/heads/$1" >/dev/null 2>&1 && echo yes || echo no; }
sel() { mkdir -p "$WORK/.ai/decisions"; printf '%s' "$1" > "$WORK/.ai/decisions/selection.md"; }

# A repo with three candidate branches + worktrees, each adding its own file.
setup_repo() {
  rm -rf "$WORK"; mkdir -p "$WORK"
  G -c init.defaultBranch=main init -q
  echo base > "$WORK/README.md"; G add -A; G commit -q -m base
  mkdir -p "$WORK/.ai/worktrees" "$WORK/.ai/candidates"
  echo '.ai/' > "$WORK/.gitignore"
  for n in claude codex gemini; do
    G worktree add -q "$WORK/.ai/worktrees/$n" -b "impl/$n" HEAD
    echo "$n" > "$WORK/.ai/worktrees/$n/$n.txt"
    git -C "$WORK/.ai/worktrees/$n" add -A
    git -C "$WORK/.ai/worktrees/$n" -c user.name=t -c user.email=t@t commit -q -m "feat: $n"
    echo "diff $n" > "$WORK/.ai/candidates/$n.diff"; echo "ok" > "$WORK/.ai/candidates/$n.test"
  done
}

# 1. The issue's fixture: a "## Winner" heading with the name in bold and prose
#    that mentions another candidate, but NO WINNER: line → refuse, no merge.
setup_repo
sel $'## Winner\n\n**gemini**\n\nGemini wins because it beat codex on spec fidelity.\n\n## Rejected\nclaude: scope creep\n'
run
check "no WINNER line: exit 1"          "1" "$RC"
check "no WINNER line: error"           "yes" "$(has "no 'WINNER: <claude|codex|gemini>' line")"
check "no WINNER line: nothing merged"  "base" "$(head_subject)"
check "no WINNER line: codex kept"      "yes" "$(branch_exists impl/codex)"
check "no WINNER line: worktrees kept"  "yes" "$([ -d "$WORK/.ai/worktrees/codex" ] && echo yes || echo no)"

# 2. The new contract: the same prose PLUS a trailing `WINNER: gemini` line →
#    gemini is merged (never codex, the name the old parser picked).
sel $'## Winner\n\n**gemini**\n\nGemini wins because it beat codex on spec fidelity.\n\n## Rejected\nclaude: scope creep\ncodex: failing tests\n\nWINNER: gemini'
run
check "WINNER line: exit 0"             "0" "$RC"
check "WINNER line: marker last"        "applied: gemini" "$(last)"
check "WINNER line: gemini merged"      "feat: apply gemini implementation (selected by cross-critique)" "$(head_subject)"
check "WINNER line: gemini file"        "yes" "$([ -f "$WORK/gemini.txt" ] && echo yes || echo no)"
check "WINNER line: codex NOT merged"   "no" "$([ -f "$WORK/codex.txt" ] && echo yes || echo no)"
check "teardown: worktrees gone"        "no" "$([ -d "$WORK/.ai/worktrees" ] && echo yes || echo no)"
check "teardown: winner branch gone"    "no" "$(branch_exists impl/gemini)"
check "teardown: loser branch gone"     "no" "$(branch_exists impl/codex)"
check "teardown: loser log line"        "yes" "$(has 'deleted impl/codex (not selected')"
check "evidence kept"                   "yes" "$([ -f "$WORK/.ai/candidates/codex.diff" ] && echo yes || echo no)"

# 3. Case + markdown decoration tolerated; prose after the name is not.
setup_repo; sel $'rationale\n**Winner: Claude**'
run
check "bold/case: exit 0"               "0" "$RC"
check "bold/case: claude merged"        "yes" "$([ -f "$WORK/claude.txt" ] && echo yes || echo no)"
setup_repo; sel $'WINNER: codex (see rationale)'
run
check "trailing prose: exit 1"          "1" "$RC"
check "trailing prose: not merged"      "base" "$(head_subject)"

# 4. Ambiguous: a WINNER line naming two candidates, or two WINNER lines that
#    disagree → exit 1, no merge. Two that AGREE are fine.
setup_repo; sel $'WINNER: claude or gemini'
run
check "two names: exit 1"               "1" "$RC"
check "two names: not merged"           "base" "$(head_subject)"
setup_repo; sel $'WINNER: claude\nreconsidered...\nWINNER: gemini'
run
check "disagreeing lines: exit 1"       "1" "$RC"
check "disagreeing lines: ambiguous"    "yes" "$(has 'ambiguous winner')"
check "disagreeing lines: not merged"   "base" "$(head_subject)"
setup_repo; sel $'WINNER: codex\n...\nWINNER: codex'
run
check "agreeing lines: exit 0"          "0" "$RC"
check "agreeing lines: codex merged"    "yes" "$([ -f "$WORK/codex.txt" ] && echo yes || echo no)"

# 5. An unknown name, a missing selection.md, a missing branch.
setup_repo; sel $'WINNER: gpt'
run
check "unknown name: exit 1"            "1" "$RC"
check "unknown name: not merged"        "base" "$(head_subject)"
setup_repo; rm -rf "$WORK/.ai/decisions"
run
check "no selection.md: exit 1"         "1" "$RC"
setup_repo; sel 'WINNER: gemini'; G worktree remove --force "$WORK/.ai/worktrees/gemini"; G branch -q -D impl/gemini
run
check "missing branch: exit 1"          "1" "$RC"
check "missing branch: message"         "yes" "$(has 'branch impl/gemini does not exist')"

# 6. Merge conflict: the merge is aborted, the tree is clean, every worktree
#    and branch is left in place, exit 1 — nothing torn down.
setup_repo
echo conflict > "$WORK/README.md"; G add -A; G commit -q -m "main edit"
echo other > "$WORK/.ai/worktrees/claude/README.md"
git -C "$WORK/.ai/worktrees/claude" add -A; git -C "$WORK/.ai/worktrees/claude" -c user.name=t -c user.email=t@t commit -q -m "claude edit"
sel 'WINNER: claude'
run
check "conflict: exit 1"                "1" "$RC"
check "conflict: conflicting path listed" "yes" "$(has 'README.md')"
check "conflict: merge aborted"         "main edit" "$(head_subject)"
check "conflict: tree clean"            "" "$(G status --porcelain)"
check "conflict: branches kept"         "yes" "$(branch_exists impl/claude)"
check "conflict: worktrees kept"        "yes" "$([ -d "$WORK/.ai/worktrees/codex" ] && echo yes || echo no)"

[ "$fail" = 0 ] && echo "ALL PASS" || { echo "SOME FAILED"; exit 1; }
