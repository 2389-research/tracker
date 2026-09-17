#!/usr/bin/env bash
# ABOUTME: Fixture tests for CommitIfDirty.sh (#297/#405) — checkpoints a dirty
# ABOUTME: tree (untracked files count) with an explicit identity, excludes
# ABOUTME: untracked executable BINARIES via the LOCAL .git/info/exclude (never
# ABOUTME: the tracked .gitignore), and prints the marker last.
set -uo pipefail
DIR="$(cd "$(dirname "$0")" && pwd)"
SCRIPT="$DIR/CommitIfDirty.sh"
fail=0
check() { # name expected actual
  if [ "$2" = "$3" ]; then echo "ok: $1"; else echo "FAIL: $1 — want '$2' got '$3'"; fail=1; fi
}
WORK="$(mktemp -d)"
STATE="$(mktemp -d)"
trap 'rm -rf "$WORK" "$STATE"' EXIT
run() { OUT="$( (cd "$WORK" && sh "$SCRIPT") 2>"$STATE/stderr")"; RC=$?; }
last() { printf '%s' "$OUT" | tail -1; }
G() { git -C "$WORK" "$@"; }
EXCL="$WORK/.git/info/exclude"
commits() { G rev-list --count HEAD 2>/dev/null || echo 0; }
tracked() { G ls-files --error-unmatch -- "$1" >/dev/null 2>&1 && echo tracked || echo untracked; }
# Minimal ELF-ish executable: NUL bytes make git classify it binary.
mk_binary() { printf '\177ELF\000\001\002\003binary payload\000\n' > "$WORK/$1"; chmod +x "$WORK/$1"; }

# 1. KNOWN-QUIRK: outside a git repo the node reports SUCCESS. `git status`
#    fails (exit 128, "fatal: not a git repository" on stderr) but it runs
#    inside `$(...)` within `[ -n ... ]`, where set -e does not apply, so the
#    empty result reads as "clean" and the marker is printed. The checkpoint
#    is silently skipped — against the script's own "a genuine commit failure
#    is NOT swallowed" intent. build_product's `requires: git` checks only for
#    the binary, not a repo. Pinned as current behaviour; if CommitIfDirty
#    grows a `git rev-parse --is-inside-work-tree` guard, flip to nonzero/no.
run
check "KNOWN-QUIRK no-git exits 0 (want nonzero when guarded)" "0" "$RC"
check "KNOWN-QUIRK no-git prints marker (want no when guarded)" "yes" "$(printf '%s' "$OUT" | grep -q 'commit-if-dirty-done' && echo yes || echo no)"
check "no-git git fatal on stderr"    "yes" "$(grep -q 'not a git repository' "$STATE/stderr" && echo yes || echo no)"

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
#    excluded via .git/info/exclude and NOT committed; the tracked .gitignore
#    is untouched (a runtime .gitignore edit would itself be out-of-scope work).
mk_binary app
echo src > "$WORK/pkg/b.go"
run
check "binary run exit 0"             "0" "$RC"
check "binary excluded line"          "/app" "$(grep -x '/app' "$EXCL")"
check "binary not tracked"            "untracked" "$(tracked app)"
check "sibling source committed"      "tracked" "$(tracked pkg/b.go)"
check ".gitignore not created"        "no" "$([ -e "$WORK/.gitignore" ] && echo yes || echo no)"
check "binary invisible to status"    "" "$(G status --porcelain)"
# Idempotent: re-running does not duplicate the exclude line.
run
check "exclude not duplicated"        "1" "$(grep -cx '/app' "$EXCL")"

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
check "dash-name excluded"            "/-weird" "$(grep -x -- '/-weird' "$EXCL")"
check "dash-name not tracked"         "untracked" "$(tracked -- -weird)"
check "metachar escaped"              '/star\*name' "$(grep -xF -- '/star\*name' "$EXCL")"
check "metachar not tracked"          "untracked" "$(tracked 'star*name')"
check "c.go still committed"          "tracked" "$(tracked pkg/c.go)"
# The escaped pattern must not swallow a legitimate sibling like "starXname".
echo src > "$WORK/starXname"
run
check "escape is literal"             "tracked" "$(tracked starXname)"

# 9. Binary in a subdirectory keeps its path in the exclude.
mkdir -p "$WORK/bin"; mk_binary bin/tool
run
check "subdir binary excluded"        "/bin/tool" "$(grep -x '/bin/tool' "$EXCL")"

if [ "$fail" = 0 ]; then echo "ALL PASS"; else echo "SOME FAILED"; exit 1; fi
