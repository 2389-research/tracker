#!/usr/bin/env bash
# ABOUTME: Fixture tests for CommitIfDirty.sh (#297/#405/#640) — checkpoints a
# ABOUTME: dirty tree (untracked files count) with an explicit identity, skips
# ABOUTME: untracked executable BINARIES and secret-looking files via a
# ABOUTME: per-invocation exclude (never .gitignore / info/exclude), unsigned
# ABOUTME: commits, hooks kept (fail loud / amend once), marker last.
set -uo pipefail
DIR="$(cd "$(dirname "$0")" && pwd)"
fail=0
check() { # name expected actual
  if [ "$2" = "$3" ]; then echo "ok: $1"; else echo "FAIL: $1 — want '$2' got '$3'"; fail=1; fi
}
WORK="$(mktemp -d)"
STATE="$(mktemp -d)"
trap 'rm -rf "$WORK" "$STATE"' EXIT
. "$DIR/test_helpers.sh"
SCRIPT="$(stage_script "$DIR/CommitIfDirty.sh")"   # ${graph.workflow_dir} expanded as the engine does
run() { OUT="$( (cd "$WORK" && ${TEST_SH:-sh} "$SCRIPT") 2>"$STATE/stderr")"; RC=$?; }
last() { printf '%s' "$OUT" | tail -1; }
G() { git -C "$WORK" "$@"; }
EXCL="$WORK/.git/info/exclude"
commits() { G rev-list --count HEAD 2>/dev/null || echo 0; }
tracked() { G ls-files --error-unmatch -- "$1" >/dev/null 2>&1 && echo tracked || echo untracked; }
# Minimal ELF-ish executable: NUL bytes make git classify it binary.
mk_binary() { printf '\177ELF\000\001\002\003binary payload\000\n' > "$WORK/$1"; chmod +x "$WORK/$1"; }

# 1. #640 E7: outside a git repo the node FAILS LOUD. build_product
#    `requires: git` and every checkpoint depends on a repo, so a non-repo is
#    an invariant violation — previously `git status` failed inside `$(...)`,
#    read as "clean", and the marker shipped with exit 0 (silent skip).
run
check "no-git exits 1"                "1" "$RC"
check "no-git prints no marker"       "no" "$(printf '%s' "$OUT" | grep -q 'commit-if-dirty-done' && echo yes || echo no)"
check "no-git error message"          "yes" "$(printf '%s' "$OUT" | grep -q 'ERROR: .*not a git repository' && echo yes || echo no)"

# 2. Clean tree: no commit, marker printed.
G -c init.defaultBranch=main init -q
echo base > "$WORK/README.md"; G add -A; G -c user.name=t -c user.email=t@t commit -q -m base
run
check "clean exit 0"                  "0" "$RC"
check "clean marker last"             "commit-if-dirty-done" "$(last)"
check "clean: still 1 commit"         "1" "$(commits)"

# 3. UNTRACKED text file counts as dirty (porcelain, not diff): committed with
#    the explicit build_product identity and the checkpoint subject.
mkdir -p "$WORK/pkg"; echo 'package pkg' > "$WORK/pkg/a.go"
run
check "untracked exit 0"              "0" "$RC"
check "untracked committed"           "2" "$(commits)"
check "a.go tracked"                  "tracked" "$(tracked pkg/a.go)"
check "commit subject"                "chore(milestone): checkpoint working tree (auto-commit by build_product)" "$(G log -1 --format=%s)"
check "commit author"                 "build_product <build_product@tracker.local>" "$(G log -1 --format='%an <%ae>')"
check "tree clean after"              "" "$(G status --porcelain)"

# 4. Modified tracked file: committed.
echo changed > "$WORK/README.md"
run
check "modified committed"            "3" "$(commits)"

# 5. #405: an untracked EXECUTABLE BINARY (a `go build -o app` artifact) is
#    NOT committed; the tracked .gitignore is untouched (a runtime .gitignore
#    edit would itself be out-of-scope work). #640 C6: the exclusion is
#    PER-INVOCATION — nothing persists in info/exclude, so a later directory
#    of the same name (`server/server.go`) is still staged (case 10).
mk_binary app
echo src > "$WORK/pkg/b.go"
run
check "binary run exit 0"             "0" "$RC"
check "binary not persisted"          "no" "$(grep -qx '/app' "$EXCL" 2>/dev/null && echo yes || echo no)"
check "binary not tracked"            "untracked" "$(tracked app)"
check "sibling source committed"      "tracked" "$(tracked pkg/b.go)"
check ".gitignore not created"        "no" "$([ -e "$WORK/.gitignore" ] && echo yes || echo no)"
check "binary still on disk"          "yes" "$([ -x "$WORK/app" ] && echo yes || echo no)"
# Idempotent: re-running with only the binary left is a no-op (no commit).
N="$(commits)"
run
check "binary-only rerun no commit"   "$N" "$(commits)"

# 6. Executable TEXT (a shell script) and an EMPTY executable are real work:
#    both are committed, neither is excluded.
printf '#!/bin/sh\necho hi\n' > "$WORK/run.sh"; chmod +x "$WORK/run.sh"
: > "$WORK/empty.bin"; chmod +x "$WORK/empty.bin"
run
check "exec script committed"         "tracked" "$(tracked run.sh)"
check "empty exec committed"          "tracked" "$(tracked empty.bin)"
check "no exclude for script"         "no" "$(grep -q 'run.sh' "$EXCL" && echo yes || echo no)"

# 7. Non-executable binary (e.g. a fixture .png) is NOT filtered — only
#    executables are candidates.
printf '\211PNG\000\001\002\n' > "$WORK/fixture.png"
run
check "non-exec binary committed"     "tracked" "$(tracked fixture.png)"

# 8. Hostile artifact names: a leading dash is not parsed as a switch, and
#    gitignore metacharacters are escaped so the exclude matches ONLY that path.
mk_binary -weird
mk_binary 'star*name'
echo src > "$WORK/pkg/c.go"
run
check "dash-name not tracked"         "untracked" "$(tracked -- -weird)"
check "metachar not tracked"          "untracked" "$(tracked 'star*name')"
check "c.go still committed"          "tracked" "$(tracked pkg/c.go)"
# The escaped pattern must not swallow a legitimate sibling like "starXname".
echo src > "$WORK/starXname"
run
check "escape is literal"             "tracked" "$(tracked starXname)"

# 9. Binary in a subdirectory is skipped by its full path.
mkdir -p "$WORK/bin"; mk_binary bin/tool
run
check "subdir binary not tracked"     "untracked" "$(tracked bin/tool)"

# 10. #640 C6: a binary `server` skipped at milestone 1 must not poison a
#     later `server/server.go` — the old persistent `/server` info/exclude
#     line matched the DIRECTORY forever and the source was never staged.
rm -f "$WORK/-weird" "$WORK/star*name" "$WORK/app" "$WORK/bin/tool"
mk_binary server
run
check "C6 binary server not tracked"  "untracked" "$(tracked server)"
rm -f "$WORK/server"; mkdir -p "$WORK/server"; echo 'package server' > "$WORK/server/server.go"
run
check "C6 milestone-2 exit 0"         "0" "$RC"
check "C6 server/server.go committed" "tracked" "$(tracked server/server.go)"
check "C6 tree clean after"           "" "$(G status --porcelain)"

# 11. #640 C5: untracked SECRET-looking files are never swept into a
#     checkpoint — loud warning naming them; normal files still commit.
printf 'secret=abc\n' > "$WORK/.env"
printf 'secret=prod\n' > "$WORK/.env.production"
printf 'KEY=example\n' > "$WORK/.env.example"
printf 'PRIVATE\n' > "$WORK/server.key"; printf 'pem\n' > "$WORK/cert.pem"; printf 'rsa\n' > "$WORK/id_rsa"
echo src > "$WORK/pkg/d.go"
run
check "C5 exit 0"                     "0" "$RC"
check "C5 .env not tracked"           "untracked" "$(tracked .env)"
check "C5 .env.production not tracked" "untracked" "$(tracked .env.production)"
check "C5 server.key not tracked"     "untracked" "$(tracked server.key)"
check "C5 cert.pem not tracked"       "untracked" "$(tracked cert.pem)"
check "C5 id_rsa not tracked"         "untracked" "$(tracked id_rsa)"
check "C5 secret never in history"    "0" "$(G log -p --all | grep -c 'secret=abc')"
check "C5 .env.example committed"     "tracked" "$(tracked .env.example)"
check "C5 d.go committed"             "tracked" "$(tracked pkg/d.go)"
check "C5 warning printed"            "yes" "$(printf '%s' "$OUT" | grep -q 'WARNING: not staging secret-looking' && echo yes || echo no)"
check "C5 warning names .env"         "yes" "$(printf '%s' "$OUT" | grep -qx '  \.env' && echo yes || echo no)"
check "C5 warning names server.key"   "yes" "$(printf '%s' "$OUT" | grep -qx '  server.key' && echo yes || echo no)"
check "C5 warning omits .env.example" "no" "$(printf '%s' "$OUT" | grep -qx '  \.env\.example' && echo yes || echo no)"
check "C5 marker still last"          "commit-if-dirty-done" "$(last)"
rm -f "$WORK/.env" "$WORK/.env.production" "$WORK/server.key" "$WORK/cert.pem" "$WORK/id_rsa"

# 12. #640 C7a: commit.gpgsign=true with no usable key must not 128 the
#     checkpoint — these are tracker's checkpoints, not the user's signed
#     history, so the commit is made unsigned.
G config commit.gpgsign true; G config gpg.program /bin/false
echo src > "$WORK/pkg/e.go"
run
check "C7a gpgsign exit 0"            "0" "$RC"
check "C7a e.go committed"            "tracked" "$(tracked pkg/e.go)"
check "C7a commit unsigned"           "" "$(G log -1 --format=%GK)"
G config --unset commit.gpgsign; G config --unset gpg.program

# 13. #640 C7b: a FAILING pre-commit hook is respected (no --no-verify);
#     the node exits 1 loudly with the hook's output visible, no marker.
mkdir -p "$WORK/.git/hooks"
printf '#!/bin/sh\necho "hook: lint failed"\nexit 1\n' > "$WORK/.git/hooks/pre-commit"; chmod +x "$WORK/.git/hooks/pre-commit"
echo src > "$WORK/pkg/f.go"
BEFORE="$(commits)"
run
check "C7b hook failure exit 1"       "1" "$RC"
check "C7b no commit made"            "$BEFORE" "$(commits)"
check "C7b hook output surfaced"      "yes" "$(printf '%s' "$OUT" | grep -q 'hook: lint failed' && echo yes || echo no)"
check "C7b loud error"                "yes" "$(printf '%s' "$OUT" | grep -q 'ERROR: checkpoint commit failed' && echo yes || echo no)"
check "C7b no marker"                 "no" "$(printf '%s' "$OUT" | grep -q 'commit-if-dirty-done' && echo yes || echo no)"

# 14. #640 C7c: a hook that REWRITES files (formatter) leaves ` M` after a
#     successful commit; the node amends once so the checkpoint is clean.
printf '#!/bin/sh\n[ -f fmt.txt ] || echo formatted > fmt.txt\nexit 0\n' > "$WORK/.git/hooks/pre-commit"
run
check "C7c rewrite exit 0"            "0" "$RC"
check "C7c f.go committed"            "tracked" "$(tracked pkg/f.go)"
check "C7c hook output committed"     "tracked" "$(tracked fmt.txt)"
check "C7c tree clean after amend"    "" "$(G status --porcelain)"
check "C7c single commit (amended)"   "$((BEFORE + 1))" "$(commits)"
check "C7c marker last"               "commit-if-dirty-done" "$(last)"

# 15. #640 C7d: a hook that rewrites on EVERY run never converges — fail loud
#     rather than print done over a dirty tree.
printf '#!/bin/sh\ndate +%%N%%s >> churn.txt\nexit 0\n' > "$WORK/.git/hooks/pre-commit"
echo src > "$WORK/pkg/g.go"
run
check "C7d non-converging exit 1"     "1" "$RC"
check "C7d loud error"                "yes" "$(printf '%s' "$OUT" | grep -q 'ERROR: .*still dirty' && echo yes || echo no)"
check "C7d no marker"                 "no" "$(printf '%s' "$OUT" | grep -q 'commit-if-dirty-done' && echo yes || echo no)"
rm -f "$WORK/.git/hooks/pre-commit"

# 16. #640 C1: inside a LINKED worktree the checkpoint works and `.tracker/`
#     (excluded by Setup in the common dir) plus a binary stay out.
G worktree add -q "$WORK/wt" -b feature 2>/dev/null
cat > "$STATE/excl.sh" <<EOF
set -eu
. "$LIB_DIR/gitignore.sh"
exclude_tracker_metadata
EOF
(cd "$WORK/wt" && sh "$STATE/excl.sh")
mkdir -p "$WORK/wt/.tracker/inputs"; echo sk > "$WORK/wt/.tracker/inputs/api_key"
echo src > "$WORK/wt/wt.go"
printf '\177ELF\000\001binary\000\n' > "$WORK/wt/wtbin"; chmod +x "$WORK/wt/wtbin"
OUT="$( (cd "$WORK/wt" && ${TEST_SH:-sh} "$SCRIPT") 2>"$STATE/stderr")"; RC=$?
check "C1 worktree exit 0"            "0" "$RC"
check "C1 worktree wt.go committed"   "yes" "$(git -C "$WORK/wt" ls-files --error-unmatch wt.go >/dev/null 2>&1 && echo yes || echo no)"
check "C1 worktree api_key NOT committed" "no" "$(git -C "$WORK/wt" ls-files --error-unmatch .tracker/inputs/api_key >/dev/null 2>&1 && echo yes || echo no)"
check "C1 worktree binary NOT committed" "no" "$(git -C "$WORK/wt" ls-files --error-unmatch wtbin >/dev/null 2>&1 && echo yes || echo no)"
# The binary stays on disk untracked (per-invocation exclude, C6); nothing else is left.
check "C1 worktree tree clean"        "?? wtbin" "$(git -C "$WORK/wt" status --porcelain)"

if [ "$fail" = 0 ]; then echo "ALL PASS"; else echo "SOME FAILED"; exit 1; fi
