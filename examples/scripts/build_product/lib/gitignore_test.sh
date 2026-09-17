#!/usr/bin/env bash
# ABOUTME: Fixture tests for lib/gitignore.sh — seed_gitignore (`.ai/` + the
# ABOUTME: #405 static patterns, append-if-absent, user entries/order kept),
# ABOUTME: exclude_tracker_metadata (#351 info/exclude + index-only untracking,
# ABOUTME: linked-worktree aware) and the shared git_exclude_add append.
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
run() { (cd "$WORK" && ${TEST_SH:-sh} "$STATE/seed.sh") >"$STATE/stdout" 2>"$STATE/stderr"; RC=$?; }
G() { git -C "$WORK" -c user.name=t -c user.email=t@t "$@"; }
reset() { rm -rf "$WORK"; mkdir -p "$WORK"; }

# ── seed_gitignore in a non-git workdir: `.ai/` + #405 patterns, idempotent,
#    user entries survive. Safe outside a repo (exclude is a no-op).
run
check "seed exit 0 (no repo)"             "0" "$RC"
check "gitignore has .ai/"                "1" "$(grep -cx '.ai/' "$WORK/.gitignore")"
check "gitignore has #405 patterns"       "yes" "$(grep -qxF 'node_modules/' "$WORK/.gitignore" && grep -qxF '/*.test' "$WORK/.gitignore" && echo yes || echo no)"
LINES_BEFORE="$(wc -l < "$WORK/.gitignore" | tr -d ' ')"
run
check "rerun exit 0"                      "0" "$RC"
check "gitignore deduped on rerun"        "$LINES_BEFORE" "$(wc -l < "$WORK/.gitignore" | tr -d ' ')"
# A user's own .gitignore entries survive the append.
echo 'my-secret.env' >> "$WORK/.gitignore"
run
check "user gitignore entry kept"         "1" "$(grep -cx 'my-secret.env' "$WORK/.gitignore")"
check "no .git/info created outside repo" "gone" "$([ -e "$WORK/.git" ] && echo present || echo gone)"

# ── #640 C2: a user .gitignore with NO trailing newline. A bare `>>` would
#    glue `.ai/` onto the last pattern (`*.log.ai/`), destroying it and leaving
#    `.ai/` unignored. Both must ignore afterwards, on separate lines.
reset
G -c init.defaultBranch=main init -q
printf '*.log' > "$WORK/.gitignore"
run
check "C2 exit 0"                         "0" "$RC"
check "C2 *.log kept on its own line"     "1" "$(grep -cx '\*\.log' "$WORK/.gitignore")"
check "C2 no glued pattern"               "0" "$(grep -c 'log\.ai' "$WORK/.gitignore")"
check "C2 *.log still ignores"            "app.log" "$(G check-ignore app.log)"
check "C2 .ai/ ignores"                   ".ai/x" "$(mkdir -p "$WORK/.ai"; touch "$WORK/.ai/x"; G check-ignore .ai/x)"
check "C2 file ends with newline"         "" "$(tail -c1 "$WORK/.gitignore")"

# ── #640 C3: no sort. Order preserved (`*.log` before `!important.log` so the
#    negation still wins), comments/blank lines untouched, and a fully seeded
#    file is NOT rewritten on the next run (content + mtime identical) so it
#    never shows up as a dirty tracked file in milestone 1's range.
reset
G -c init.defaultBranch=main init -q
printf '# logs\n*.log\n!important.log\n\n# end\n' > "$WORK/.gitignore"
run
check "C3 exit 0"                         "0" "$RC"
check "C3 order preserved"                "# logs|*.log|!important.log||# end" "$(head -5 "$WORK/.gitignore" | paste -sd'|' -)"
check "C3 important.log un-ignored"       "" "$(touch "$WORK/important.log"; G check-ignore important.log || true)"
check "C3 other.log ignored"              "other.log" "$(touch "$WORK/other.log"; G check-ignore other.log)"
cp "$WORK/.gitignore" "$STATE/seeded"
mtime() { stat -f %m "$1" 2>/dev/null || stat -c %Y "$1"; }
touch -t 200001010000 "$WORK/.gitignore"; MT="$(mtime "$WORK/.gitignore")"
run
check "C3 rerun exit 0"                   "0" "$RC"
check "C3 rerun content identical"        "yes" "$(cmp -s "$STATE/seeded" "$WORK/.gitignore" && echo yes || echo no)"
check "C3 rerun mtime untouched"          "$MT" "$(mtime "$WORK/.gitignore")"
check "C3 rerun .gitignore not dirty"     "" "$(G add .gitignore; G -c user.name=t -c user.email=t@t commit -q -m gi; run; G status --porcelain -- .gitignore)"

# ── #640 C4: build-output seeds are ANCHORED to the repo root so a source
#    package named build/ (or a testdata/*.out fixture) is never ignored —
#    an unanchored `build/` hid internal/build/ from every commit and review.
reset
G -c init.defaultBranch=main init -q
run
mkdir -p "$WORK/internal/build" "$WORK/build" "$WORK/testdata"
echo 'package build' > "$WORK/internal/build/build.go"
echo bin > "$WORK/build/app"; echo out > "$WORK/testdata/render.out"
check "C4 internal/build/ NOT ignored"    "" "$(G check-ignore internal/build/build.go || true)"
check "C4 /build/ at root ignored"        "build/app" "$(G check-ignore build/app)"
check "C4 testdata/render.out NOT ignored" "" "$(G check-ignore testdata/render.out || true)"
check "C4 no unanchored build/ seed"      "0" "$(grep -cx 'build/' "$WORK/.gitignore")"
check "C4 no *.out seed"                  "0" "$(grep -cx '\*\.out' "$WORK/.gitignore")"
check "C4 root *.test ignored"            "pkg.test" "$(touch "$WORK/pkg.test"; G check-ignore pkg.test)"
check "C4 nested x.test NOT ignored"      "" "$(touch "$WORK/testdata/x.test"; G check-ignore testdata/x.test || true)"

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

# ── #640 C1: in a LINKED worktree (`git worktree add`) git reads only
#    $GIT_COMMON_DIR/info/exclude; `git rev-parse --git-dir` points at
#    .git/worktrees/<n>/, where an exclude is dead — `.tracker/inputs/api_key`
#    would be committed. The helper must land in the path git actually reads.
reset
G -c init.defaultBranch=main init -q
echo base > "$WORK/README.md"; G add -A; G commit -q -m base
G worktree add -q "$WORK/wt" -b feature
run_wt() { (cd "$WORK/wt" && ${TEST_SH:-sh} "$STATE/seed.sh") >"$STATE/stdout" 2>"$STATE/stderr"; RC=$?; }
run_wt
check "C1 worktree exit 0"                "0" "$RC"
mkdir -p "$WORK/wt/.tracker/inputs"; echo sk-secret > "$WORK/wt/.tracker/inputs/api_key"
check "C1 .tracker ignored in worktree"   ".tracker/inputs/api_key" "$(git -C "$WORK/wt" check-ignore .tracker/inputs/api_key)"
check "C1 worktree status clean"          "" "$(git -C "$WORK/wt" status --porcelain -- .tracker)"
check "C1 exclude in common dir"          "1" "$(grep -cx '.tracker/' "$WORK/.git/info/exclude")"
check "C1 no dead per-worktree exclude"   "no" "$([ -e "$WORK/.git/worktrees/wt/info/exclude" ] && echo yes || echo no)"

# ── git_exclude_add: the one idempotent append CommitIfDirty (sed-escaped
#    artifact paths) and ContinueWithMoreTurns (turn-override dir) share.
cat > "$STATE/add.sh" <<EOF
set -eu
. "$LIB_DIR/gitignore.sh"
git_exclude_add "\$1"
EOF
add() { (cd "$WORK" && ${TEST_SH:-sh} "$STATE/add.sh" "$1") 2>"$STATE/stderr"; RC=$?; }
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
