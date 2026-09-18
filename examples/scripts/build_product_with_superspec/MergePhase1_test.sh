#!/usr/bin/env bash
# ABOUTME: Fixture tests for MergePhase1.sh / lib/worktrees.sh merge_streams
# ABOUTME: (#646 5b/5c) — merges each stream, tears down only once ALL merged, folds
# ABOUTME: the streams' traceability overlays into the committed master, records
# ABOUTME: the next phase base; a conflict aborts (`|| true`, no rc 128), prints
# ABOUTME: the conflicting paths, keeps every worktree/branch, exits 1; a retry
# ABOUTME: after a by-hand resolution skips branches already in HEAD; a dirty
# ABOUTME: tree or a missing branch fails loud. The other MergePhaseN.sh sidecars
# ABOUTME: are the same one-liner with different streams (Go graph test).
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
SCRIPT="$(stage_script "$DIR/MergePhase1.sh")"
SETUP="$(stage_script "$DIR/SetupPhase1Worktrees.sh")"
run() { OUT="$( (cd "$WORK" && ${TEST_SH:-sh} "$SCRIPT") 2>"$STATE/stderr")"; RC=$?; }
last() { printf '%s' "$OUT" | tail -1; }
has() { printf '%s' "$OUT" | grep -qF -- "$1" && echo yes || echo no; }
G() { git -C "$WORK" -c user.name=t -c user.email=t@t "$@"; }
W() { local n=$1; shift; git -C "$WORK/.ai/worktrees/$n" -c user.name=t -c user.email=t@t "$@"; }
branch_exists() { G rev-parse --verify --quiet "refs/heads/$1" >/dev/null 2>&1 && echo yes || echo no; }
M="$WORK/docs/traceability.yaml"

# Scaffolded repo with two stream worktrees (via the real setup script).
setup_repo() {
  rm -rf "$WORK"; mkdir -p "$WORK/docs"
  G -c init.defaultBranch=main init -q
  echo spec > "$WORK/SPEC.md"; echo plan > "$WORK/docs/execution-plan.md"; echo '.ai/' > "$WORK/.gitignore"
  printf 'FR-1: {status: pending, impl_ref: null, test_ref: null, note: null}\nFR-2: {status: pending, impl_ref: null, test_ref: null, note: null}\n' > "$M"
  G add -A; G commit -q -m scaffold
  (cd "$WORK" && ${TEST_SH:-sh} "$SETUP" >/dev/null 2>&1) || { echo "setup failed"; exit 1; }
}
# stream NAME FILE OVERLAY-LINE — commit a file + overlay on the stream branch.
stream() {
  echo "$2" > "$WORK/.ai/worktrees/$1/$2"
  printf '%s\n' "$3" > "$WORK/.ai/worktrees/$1/docs/traceability.$1.yaml"
  W "$1" add -A; W "$1" commit -q -m "feat($1): $2"
}

# 1. Happy path: both streams merge, worktrees/branches torn down, overlays
#    folded + committed, master updated, base sha advanced, marker last.
setup_repo
stream stream-a a.go 'FR-1: {status: done, impl_ref: "a.go", test_ref: "a_test.go", note: "a"}'
stream stream-b b.go 'FR-2: {status: done, impl_ref: "b.go", test_ref: "b_test.go", note: "b"}'
run
check "happy: exit 0"                   "0" "$RC"
check "happy: marker last"              "phase1-merged" "$(last)"
check "happy: a.go merged"              "present" "$([ -f "$WORK/a.go" ] && echo present || echo gone)"
check "happy: b.go merged"              "present" "$([ -f "$WORK/b.go" ] && echo present || echo gone)"
check "happy: worktrees gone"           "0" "$(ls -d "$WORK"/.ai/worktrees/* 2>/dev/null | wc -l | tr -d ' ')"
check "happy: branches gone"            "no no" "$(branch_exists build/stream-a) $(branch_exists build/stream-b)"
check "happy: FR-1 folded"              'FR-1: {status: done, impl_ref: "a.go", test_ref: "a_test.go", note: "a"}' "$(sed -n 1p "$M")"
check "happy: FR-2 folded"              'FR-2: {status: done, impl_ref: "b.go", test_ref: "b_test.go", note: "b"}' "$(sed -n 2p "$M")"
check "happy: overlays removed"         "0" "$(ls "$WORK"/docs/traceability.*.yaml 2>/dev/null | wc -l | tr -d ' ')"
check "happy: fold committed"           "chore(traceability): merge phase 1 stream overlays" "$(G log -1 --format=%s)"
check "happy: tree clean"               "" "$(G status --porcelain)"
check "happy: merge-phase recorded"     "1" "$(cat "$WORK/.ai/build/merge-phase")"
check "happy: base sha advanced"        "$(G rev-parse HEAD)" "$(cat "$WORK/.ai/build/milestone-start-sha")"
check "happy: no add/add on the matrix" "no" "$(has 'CONFLICT')"

# 2. Conflict in stream-b (stream-a merged first): merge aborted with `|| true`
#    (no rc 128), paths printed, stream-b worktree + branch KEPT, exit 1,
#    no overlay fold, tree clean.
setup_repo
stream stream-a shared.txt 'FR-1: {status: done, impl_ref: "a", test_ref: "a_t", note: null}'
echo "b version" > "$WORK/.ai/worktrees/stream-b/shared.txt"
printf 'FR-2: {status: done, impl_ref: "b", test_ref: "b_t", note: null}\n' > "$WORK/.ai/worktrees/stream-b/docs/traceability.stream-b.yaml"
W stream-b add -A; W stream-b commit -q -m "feat(stream-b): shared"
run
check "conflict: exit 1"                "1" "$RC"
check "conflict: names the stream"      "yes" "$(has 'MERGE CONFLICT in stream-b (build/stream-b)')"
check "conflict: lists the path"        "yes" "$(has '  shared.txt')"
check "conflict: no 'no merge to abort'" "no" "$(grep -q 'no merge to abort' "$STATE/stderr" && echo yes || echo no)"
check "conflict: merge aborted, tree clean" "" "$(G status --porcelain)"
check "conflict: stream-a merged"       "feat(stream-a): shared.txt" "$(G log -1 --format=%s)"
check "conflict: stream-b branch kept"  "yes" "$(branch_exists build/stream-b)"
check "conflict: stream-a branch kept too" "yes" "$(branch_exists build/stream-a)"
check "conflict: stream-b worktree kept" "present" "$([ -d "$WORK/.ai/worktrees/stream-b" ] && echo present || echo gone)"
check "conflict: overlay not folded"    "pending" "$(sed -n 2p "$M" | grep -o 'status: [a-z]*' | cut -d' ' -f2)"
check "conflict: how-to line"           "yes" "$(has "git merge build/stream-b")"
check "conflict: no marker"             "no" "$(has 'phase1-merged')"

# 3. Retry after the human resolved by hand: stream-a is skipped (already in
#    HEAD), stream-b merges, overlays fold, marker.
G merge --no-edit build/stream-b >/dev/null 2>&1 || true
echo "resolved" > "$WORK/shared.txt"; G add -A; G commit -q -m "merge stream-b by hand" >/dev/null 2>&1
run
check "retry: exit 0"                   "0" "$RC"
check "retry: both skipped (in HEAD)"   "2" "$(printf '%s\n' "$OUT" | grep -c 'is already in HEAD')"
check "retry: marker last"              "phase1-merged" "$(last)"
check "retry: overlays folded"          "done" "$(sed -n 2p "$M" | grep -o 'status: [a-z]*' | cut -d' ' -f2)"
check "retry: branches gone"            "no no" "$(branch_exists build/stream-a) $(branch_exists build/stream-b)"

# 4. Dirty working tree → exit 1 before any merge; missing branch → exit 1.
setup_repo
stream stream-a a.go 'FR-1: {status: done, impl_ref: "a", test_ref: "a_t", note: null}'
echo dirty >> "$WORK/SPEC.md"
run
check "dirty: exit 1"                   "1" "$RC"
check "dirty: message"                  "yes" "$(has 'uncommitted changes')"
check "dirty: nothing merged"           "scaffold" "$(G log -1 --format=%s)"
G checkout -q -- SPEC.md
G worktree remove --force "$WORK/.ai/worktrees/stream-b"; G branch -q -D build/stream-b
run
check "missing branch: exit 1"          "1" "$RC"
check "missing branch: message"         "yes" "$(has 'branch build/stream-b does not exist')"

[ "$fail" = 0 ] && echo "ALL PASS" || { echo "SOME FAILED"; exit 1; }
