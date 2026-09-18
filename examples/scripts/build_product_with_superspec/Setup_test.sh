#!/usr/bin/env bash
# ABOUTME: Fixture tests for superspec Setup.sh (#646 5d) — requires a git repo,
# ABOUTME: seeds .gitignore append-if-absent via the parity-pinned lib, excludes
# ABOUTME: .tracker/ via the LOCAL info/exclude, installs verify.sh + ci-probe.sh
# ABOUTME: byte-for-byte into .ai/build/, resets per-run state, adopts the
# ABOUTME: #553 staged spec, and fails loud without SPEC.md / workflow_dir.
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
SCRIPT="$(stage_script "$DIR/Setup.sh")"
run() { OUT="$( (cd "$WORK" && ${TEST_SH:-sh} "$SCRIPT") 2>"$STATE/stderr")"; RC=$?; }
last() { printf '%s' "$OUT" | tail -1; }
has() { printf '%s' "$OUT" | grep -qF -- "$1" && echo yes || echo no; }
G() { git -C "$WORK" -c user.name=t -c user.email=t@t "$@"; }

# 1. Not a git repo → exit 1 before anything is written.
echo spec > "$WORK/SPEC.md"
run
check "nongit: exit 1"                 "1" "$RC"
check "nongit: message"                "yes" "$(has 'not a git repository')"
check "nongit: nothing scaffolded"     "gone" "$([ -e "$WORK/.ai" ] && echo present || echo gone)"

# 2. Git repo, SPEC.md present: scaffold, gitignore, exclude, lib install,
#    marker last.
G -c init.defaultBranch=main init -q
printf '*.log\n!keep.log' > "$WORK/.gitignore"
mkdir -p "$WORK/.ai/gates" "$WORK/.ai/build" "$WORK/.ai/milestones"; echo old > "$WORK/.ai/gates/phase1.txt"; echo abc > "$WORK/.ai/build/milestone-start-sha"; echo 2 > "$WORK/.ai/build/merge-phase"
echo TestOld > "$WORK/.ai/milestones/known_failures"; echo G404 > "$WORK/.ai/milestones/known_lint_failures"
run
check "git: exit 0"                    "0" "$RC"
check "git: marker last"               "setup-ready" "$(last)"
for d in .ai/streams .ai/decisions .ai/worktrees .ai/gates .ai/build; do
  check "git: $d exists"               "present" "$([ -d "$WORK/$d" ] && echo present || echo gone)"
done
check "git: .ai/ appended, no glue"    $'*.log\n!keep.log\n.ai/' "$(head -3 "$WORK/.gitignore" | paste -sd'\n' -)"
check "git: negation order kept"       "2" "$(grep -n '^!keep.log$' "$WORK/.gitignore" | cut -d: -f1)"
check "git: anchored build seed"       "yes" "$(grep -qxF '/build/' "$WORK/.gitignore" && echo yes || echo no)"
check "git: .tracker/ in info/exclude" "yes" "$(grep -qxF '.tracker/' "$WORK/.git/info/exclude" && echo yes || echo no)"
check "git: .tracker/ not in .gitignore" "no" "$(grep -q tracker "$WORK/.gitignore" && echo yes || echo no)"
for f in verify.sh ci-probe.sh; do
  check "git: .ai/build/$f = lib"      "same" "$(cmp -s "$WORK/.ai/build/$f" "$DIR/lib/$f" && echo same || echo differs)"
done
check "git: stale gate report reset"   "gone" "$([ -e "$WORK/.ai/gates/phase1.txt" ] && echo present || echo gone)"
check "git: stale phase base reset"    "gone" "$([ -e "$WORK/.ai/build/milestone-start-sha" ] && echo present || echo gone)"
check "git: stale merge-phase reset"   "gone" "$([ -e "$WORK/.ai/build/merge-phase" ] && echo present || echo gone)"
check "git: prior known_failures gone" "gone" "$([ -e "$WORK/.ai/milestones/known_failures" ] && echo present || echo gone)"
check "git: prior known_lint_failures gone" "gone" "$([ -e "$WORK/.ai/milestones/known_lint_failures" ] && echo present || echo gone)"
run
check "idempotent: exit 0"             "0" "$RC"
check "idempotent: one .ai/ line"      "1" "$(grep -c '^\.ai/$' "$WORK/.gitignore")"

# 3. #553: a staged spec input is adopted (overwrites SPEC.md).
mkdir -p "$WORK/.tracker/inputs"; echo staged > "$WORK/.tracker/inputs/spec"
run
check "staged spec: adopted"           "staged" "$(cat "$WORK/SPEC.md")"

# 4. No SPEC.md anywhere → exit 1 with the init hint, no marker.
rm -rf "$WORK/.tracker" "$WORK/SPEC.md"
run
check "no SPEC: exit 1"                "1" "$RC"
check "no SPEC: hint"                  "yes" "$(has 'tracker init build_product_with_superspec')"
check "no SPEC: no marker"             "no" "$(has 'setup-ready')"

# 5. Empty ${graph.workflow_dir} → fail loud before touching anything.
sed 's|LIB="[^"]*"|LIB=""|; s|\[ -n "[^"]*" \] \|\| {|[ -n "" ] \|\| {|' "$SCRIPT" > "$STATE/empty.sh"
OUT="$( (cd "$WORK" && ${TEST_SH:-sh} "$STATE/empty.sh") 2>&1)"; RC=$?
check "empty workflow_dir: exit 1"     "1" "$RC"
check "empty workflow_dir: message"    "yes" "$(printf '%s' "$OUT" | grep -q 'graph.workflow_dir is empty' && echo yes || echo no)"

[ "$fail" = 0 ] && echo "ALL PASS" || { echo "SOME FAILED"; exit 1; }
