#!/usr/bin/env bash
# ABOUTME: Fixture tests for CaptureAndTest.sh (#646 item 2) — every candidate
# ABOUTME: gets a diff + test log even when an earlier one is red (no set -e
# ABOUTME: abort), diffs use the recorded fork sha (not `--abbrev-ref HEAD`),
# ABOUTME: uncommitted candidate work is checkpointed on its branch, and the
# ABOUTME: node fails (all-candidates-red marker) only when NO candidate is usable.
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
SCRIPT="$(stage_script "$DIR/CaptureAndTest.sh")"
# Per-worktree `go` shim: `go test` is red inside worktree <n> when
# $STATE/red-<n> exists (keyed on the cwd basename); every call is logged.
mkdir -p "$STATE/bin"
cat > "$STATE/bin/go" <<SH
#!/bin/sh
echo "go \$1 in \$(basename "\$PWD")" >> "$STATE/calls"
if [ "\$1" = test ] && [ -f "$STATE/red-\$(basename "\$PWD")" ]; then echo "--- FAIL: TestX"; exit 1; fi
echo "ok"
SH
chmod +x "$STATE/bin/go"
run() { OUT="$( (cd "$WORK" && PATH="$STATE/bin:$PATH" ${TEST_SH:-sh} "$SCRIPT") 2>"$STATE/stderr")"; RC=$?; }
last() { printf '%s' "$OUT" | tail -1; }
has() { printf '%s' "$OUT" | grep -qF -- "$1" && echo yes || echo no; }
line() { printf '%s\n' "$OUT" | grep -E "^$1:" | head -1; }
G() { git -C "$WORK" -c user.name=t -c user.email=t@t "$@"; }
W() { local n=$1; shift; git -C "$WORK/.ai/worktrees/$n" -c user.name=t -c user.email=t@t "$@"; }
nfiles() { grep -c '^diff --git ' "$WORK/.ai/candidates/$1.diff"; }
calls() { [ -f "$STATE/calls" ] && paste -sd';' "$STATE/calls" || echo ""; }

# A DETACHED-HEAD repo (the case that made every diff empty) with a go.mod
# so the go shim is the runner; three worktrees forked at HEAD.
setup_repo() {
  rm -rf "$WORK"; mkdir -p "$WORK"; rm -f "$STATE/calls" "$STATE"/red-*
  G -c init.defaultBranch=main init -q
  echo base > "$WORK/README.md"; echo 'module x' > "$WORK/go.mod"; G add -A; G commit -q -m base
  G checkout -q --detach HEAD
  mkdir -p "$WORK/.ai/worktrees" "$WORK/.ai/candidates"
  G rev-parse HEAD > "$WORK/.ai/candidates/base-sha"
  for n in claude codex gemini; do G worktree add -q "$WORK/.ai/worktrees/$n" -b "impl/$n" HEAD; done
}
implement() { echo "$1 impl" > "$WORK/.ai/worktrees/$1/$1.go"; W "$1" add -A; W "$1" commit -q -m "feat: $1"; }

# 1. The #646 repro: claude red, codex + gemini green, all committed.
#    Pre-fix `set -e` aborted on claude → no codex/gemini logs, exit 1.
setup_repo; implement claude; implement codex; implement gemini
touch "$STATE/red-claude"
run
check "mixed: exit 0"                       "0" "$RC"
check "mixed: marker last"                  "candidates-captured" "$(last)"
check "mixed: claude FAIL line"             "yes" "$(printf '%s' "$(line claude)" | grep -q 'TESTS FAIL (go, exit=1)' && echo yes || echo no)"
check "mixed: codex PASS line"              "yes" "$(printf '%s' "$(line codex)" | grep -q 'TESTS PASS (go)' && echo yes || echo no)"
check "mixed: gemini PASS line"             "yes" "$(printf '%s' "$(line gemini)" | grep -q 'TESTS PASS (go)' && echo yes || echo no)"
check "mixed: all three tested"             "go build in claude;go test in claude;go build in codex;go test in codex;go build in gemini;go test in gemini" "$(calls)"
check "mixed: claude log has failure"       "yes" "$(grep -q 'FAIL: TestX' "$WORK/.ai/candidates/claude.test" && echo yes || echo no)"
check "mixed: gemini log written"           "ok" "$(tail -1 "$WORK/.ai/candidates/gemini.test")"
check "mixed: usable count"                 "yes" "$(has 'usable candidates: 2 of 3')"
# Detached HEAD: the diff is against the recorded fork sha, so it is NOT empty.
check "detached: claude diff has 1 file"    "1" "$(nfiles claude)"
check "detached: gemini diff has 1 file"    "1" "$(nfiles gemini)"
check "detached: diff names the file"       "yes" "$(grep -q 'gemini.go' "$WORK/.ai/candidates/gemini.diff" && echo yes || echo no)"

# 2. Uncommitted candidate work is checkpointed ON THE BRANCH (what
#    ApplyWinner merges), with the fixed message, and appears in the diff.
setup_repo; implement claude; implement gemini
echo "codex impl" > "$WORK/.ai/worktrees/codex/codex.go"   # untracked, uncommitted
run
check "uncommitted: exit 0"                 "0" "$RC"
check "uncommitted: checkpoint note"        "yes" "$(printf '%s' "$(line codex)" | grep -q 'uncommitted work checkpointed on impl/codex' && echo yes || echo no)"
check "uncommitted: on the branch"          "chore(codex): checkpoint uncommitted candidate work (ask_and_execute CaptureAndTest)" "$(W codex log -1 --format=%s)"
check "uncommitted: identity is explicit"   "ask_and_execute" "$(W codex log -1 --format=%an)"
check "uncommitted: worktree clean"         "" "$(W codex status --porcelain)"
check "uncommitted: in diff"                "1" "$(nfiles codex)"
check "uncommitted: committed ones untouched" "feat: claude" "$(W claude log -1 --format=%s)"

# 3. A hook that rejects the checkpoint: never --no-verify. The NEW file's
#    content is in the diff (it stays staged), the branch is unchanged, and
#    the result line carries the hook's own output.
setup_repo; implement claude; implement gemini
echo "codex impl" > "$WORK/.ai/worktrees/codex/codex.go"
HOOKS="$WORK/$(G rev-parse --git-path hooks)"; mkdir -p "$HOOKS"
printf '#!/bin/sh\necho "lint: codex.go has issues"\nexit 1\n' > "$HOOKS/pre-commit"; chmod +x "$HOOKS/pre-commit"
run
rm -f "$HOOKS/pre-commit"
check "hook: exit 0 (others usable)"       "0" "$RC"
check "hook: warning in result line"        "yes" "$(printf '%s' "$(line codex)" | grep -q 'could NOT be committed (git exit 1: lint: codex.go has issues' && echo yes || echo no)"
check "hook: branch unchanged"              "base" "$(W codex log -1 --format=%s)"
check "hook: new file content IN the diff"  "yes" "$(grep -q '^+codex impl' "$WORK/.ai/candidates/codex.diff" && echo yes || echo no)"
check "hook: counted as a changed file"     "1" "$(nfiles codex)"
check "hook: nothing listed as untracked"   "no" "$(grep -q '# untracked' "$WORK/.ai/candidates/codex.diff" && echo yes || echo no)"
check "hook: work still staged"             "A  codex.go" "$(W codex status --porcelain)"

# 4. All red → every log still written, all-candidates-red marker, exit 1.
setup_repo; implement claude; implement codex; implement gemini
touch "$STATE/red-claude" "$STATE/red-codex" "$STATE/red-gemini"
run
check "all red: exit 1"                     "1" "$RC"
check "all red: marker last"                "all-candidates-red" "$(last)"
check "all red: three FAIL lines"           "3" "$(printf '%s\n' "$OUT" | grep -c 'TESTS FAIL')"
check "all red: gemini log written"         "yes" "$(grep -q 'FAIL: TestX' "$WORK/.ai/candidates/gemini.test" && echo yes || echo no)"
check "all red: results file"               "yes" "$(grep -q 'claude: TESTS FAIL' "$WORK/.ai/candidates/results.txt" && echo yes || echo no)"

# 5. An empty diff (agent did nothing) is red; a missing worktree is red.
setup_repo; implement claude
G worktree remove --force "$WORK/.ai/worktrees/gemini"
run
check "empty/missing: exit 0 (1 usable)"    "0" "$RC"
check "empty/missing: codex EMPTY DIFF"     "yes" "$(printf '%s' "$(line codex)" | grep -q 'EMPTY DIFF' && echo yes || echo no)"
check "empty/missing: gemini MISSING"       "yes" "$(printf '%s' "$(line gemini)" | grep -q 'MISSING WORKTREE' && echo yes || echo no)"
check "empty/missing: claude still usable"  "yes" "$(printf '%s' "$(line claude)" | grep -q 'TESTS PASS' && echo yes || echo no)"
check "empty/missing: 1 usable → marker"    "candidates-captured" "$(last)"

# 6. No base-sha file (cleaned .ai/candidates/) → merge-base fallback still
#    yields a real diff; no build system → NO TESTS (usable, not red).
setup_repo; rm -f "$WORK/.ai/candidates/base-sha"
for n in claude codex gemini; do rm "$WORK/.ai/worktrees/$n/go.mod"; W "$n" add -A; W "$n" commit -q -m "drop go.mod"; done
implement claude; implement codex; implement gemini
run
check "fallback: exit 0"                    "0" "$RC"
check "fallback: NO TESTS line"             "yes" "$(printf '%s' "$(line claude)" | grep -q 'NO TESTS (no known build system)' && echo yes || echo no)"
check "fallback: diff via merge-base"       "yes" "$(grep -q 'claude.go' "$WORK/.ai/candidates/claude.diff" && echo yes || echo no)"
check "fallback: nothing ran"               "" "$(calls)"

[ "$fail" = 0 ] && echo "ALL PASS" || { echo "SOME FAILED"; exit 1; }
