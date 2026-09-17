#!/usr/bin/env bash
# ABOUTME: Fixture tests for lib/gitignore.sh — seed_gitignore (`.ai/` + the
# ABOUTME: #405 static patterns, dedupe + sort, user entries kept),
# ABOUTME: exclude_tracker_metadata (#351 .git/info/exclude + index-only
# ABOUTME: untracking) and the shared git_exclude_add idempotent append.
#
# The seed/exclude checks were Setup_test.sh's until the helpers moved here;
# Setup_test.sh keeps the orchestration evidence (Setup calls each helper).
# The helpers are sourced by Setup, CommitIfDirty and ContinueWithMoreTurns
# via ${graph.workflow_dir}/scripts/build_product/lib/gitignore.sh.
set -uo pipefail
DIR="$(cd "$(dirname "$0")" && pwd)"
fail=0
check() { # name expected actual
  if [ "$2" = "$3" ]; then echo "ok: $1"; else echo "FAIL: $1 — want '$2' got '$3'"; fail=1; fi
}
WORK="$(mktemp -d)"
STATE="$(mktemp -d)"
trap 'rm -rf "$WORK" "$STATE"' EXIT
. "$DIR/../test_helpers.sh"
# Driver: exactly the two calls Setup makes, under Setup's `set -eu`.
cat > "$STATE/seed.sh" <<EOF
set -eu
. "$LIB_DIR/gitignore.sh"
seed_gitignore
exclude_tracker_metadata
EOF
run() { (cd "$WORK" && sh "$STATE/seed.sh") >"$STATE/stdout" 2>"$STATE/stderr"; RC=$?; }
G() { git -C "$WORK" -c user.name=t -c user.email=t@t "$@"; }
reset() { rm -rf "$WORK"; mkdir -p "$WORK"; }

# ── seed_gitignore in a non-git workdir: `.ai/` + #405 patterns, idempotent,
#    sorted, user entries survive. Safe outside a repo (exclude is a no-op).
run
check "seed exit 0 (no repo)"             "0" "$RC"
check "gitignore has .ai/"                "1" "$(grep -cx '.ai/' "$WORK/.gitignore")"
check "gitignore has #405 patterns"       "yes" "$(grep -qxF 'node_modules/' "$WORK/.gitignore" && grep -qxF '*.test' "$WORK/.gitignore" && echo yes || echo no)"
LINES_BEFORE="$(wc -l < "$WORK/.gitignore" | tr -d ' ')"
run
check "rerun exit 0"                      "0" "$RC"
check "gitignore deduped on rerun"        "$LINES_BEFORE" "$(wc -l < "$WORK/.gitignore" | tr -d ' ')"
check "gitignore sorted"                  "yes" "$(sort -u "$WORK/.gitignore" | cmp -s - "$WORK/.gitignore" && echo yes || echo no)"
# A user's own .gitignore entries survive the append+sort.
echo 'my-secret.env' >> "$WORK/.gitignore"
run
check "user gitignore entry kept"         "1" "$(grep -cx 'my-secret.env' "$WORK/.gitignore")"
check "no .git/info created outside repo" "gone" "$([ -e "$WORK/.git" ] && echo present || echo gone)"

# ── exclude_tracker_metadata in a git repo: .tracker/ -> .git/info/exclude
#    (once); a pre-#351 committed .tracker/ is untracked index-only.
reset
G -c init.defaultBranch=main init -q
mkdir -p "$WORK/.tracker/runs/r1"; echo meta > "$WORK/.tracker/runs/r1/checkpoint.json"
echo base > "$WORK/README.md"
G add -A; G commit -q -m base           # pre-#351 polluted history: .tracker/ committed
run
check "git exit 0"                        "0" "$RC"
check "exclude has .tracker/"             "1" "$(grep -cx '.tracker/' "$WORK/.git/info/exclude")"
check ".tracker untracked from index"     "" "$(G ls-files -- .tracker)"
check ".tracker kept on disk"             "meta" "$(cat "$WORK/.tracker/runs/r1/checkpoint.json")"
check "deletion staged for next commit"   "yes" "$(G status --porcelain | grep -q '^D  .tracker/runs/r1/checkpoint.json' && echo yes || echo no)"
run
check "exclude not duplicated"            "1" "$(grep -cx '.tracker/' "$WORK/.git/info/exclude")"
check "rerun with nothing to untrack ok"  "0" "$RC"

# ── git_exclude_add: the one idempotent append CommitIfDirty (sed-escaped
#    artifact paths) and ContinueWithMoreTurns (turn-override dir) share.
cat > "$STATE/add.sh" <<EOF
set -eu
. "$LIB_DIR/gitignore.sh"
git_exclude_add "\$1"
EOF
add() { (cd "$WORK" && sh "$STATE/add.sh" "$1") 2>"$STATE/stderr"; RC=$?; }
reset
G -c init.defaultBranch=main init -q
rm -rf "$WORK/.git/info"                 # a bare-ish repo: info/ must be created
add ".tracker/turn_overrides/"
check "add creates .git/info"             "present" "$([ -d "$WORK/.git/info" ] && echo present || echo gone)"
check "add appends pattern"               "1" "$(grep -cx '.tracker/turn_overrides/' "$WORK/.git/info/exclude")"
add ".tracker/turn_overrides/"
check "add is idempotent"                 "1" "$(grep -cx '.tracker/turn_overrides/' "$WORK/.git/info/exclude")"
# Backslash-escaped gitignore metachars (CommitIfDirty's sed-escaped artifact
# names) land verbatim — printf, not echo, so dash can't eat the backslashes.
add '/bin\*\?\[x\]\!\#tool'
check "add keeps escapes verbatim"        "1" "$(grep -cxF '/bin\*\?\[x\]\!\#tool' "$WORK/.git/info/exclude")"
check "add leading-dash pattern"          "0" "$(add '/-weird'; echo "$RC")"
check "add leading-dash appended"         "1" "$(grep -cxF -- '/-weird' "$WORK/.git/info/exclude")"
# Outside a repo: no-op, exit 0, nothing written.
reset
add "anything/"
check "add outside repo exit 0"           "0" "$RC"
check "add outside repo writes nothing"   "" "$(ls -A "$WORK")"

if [ "$fail" = 0 ]; then echo "ALL PASS"; else echo "SOME FAILED"; exit 1; fi
