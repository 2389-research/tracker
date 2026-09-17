#!/usr/bin/env bash
# ABOUTME: Fixture tests for ComputeReviewDiff.sh (#418/#424) — writes the
# ABOUTME: cumulative base..WORKTREE diff to .ai/build/review-diff.md, degrades
# ABOUTME: to the empty tree on a missing/unreachable base, stamps UNAVAILABLE
# ABOUTME: (preserving git output) on any git failure, and always routes on.
set -uo pipefail
DIR="$(cd "$(dirname "$0")" && pwd)"
SCRIPT="$DIR/ComputeReviewDiff.sh"
fail=0
check() { # name expected actual
  if [ "$2" = "$3" ]; then echo "ok: $1"; else echo "FAIL: $1 — want '$2' got '$3'"; fail=1; fi
}
WORK="$(mktemp -d)"
STATE="$(mktemp -d)"
trap 'rm -rf "$WORK" "$STATE"' EXIT
run() { OUT="$( (cd "$WORK" && PATH="$STATE/bin:$PATH" sh "$SCRIPT") 2>"$STATE/stderr")"; RC=$?; ERR="$(cat "$STATE/stderr")"; }
last() { printf '%s' "$OUT" | tail -1; }
G() { git -C "$WORK" -c user.name=t -c user.email=t@t "$@"; }
OUTF="$WORK/.ai/build/review-diff.md"
inf() { grep -qF -- "$1" "$OUTF" && echo yes || echo no; }
mkdir -p "$STATE/bin"

# 1. Not a git work tree: UNAVAILABLE stamp, stderr warning, marker, exit 0.
run
check "no-git exit 0"                  "0" "$RC"
check "no-git marker"                  "review-diff-ready" "$(last)"
check "no-git stderr warning"          "yes" "$(printf '%s' "$ERR" | grep -q 'not inside a git work tree' && echo yes || echo no)"
check "no-git file stamped"            "yes" "$(inf 'Review diff UNAVAILABLE — not a git work tree')"

# 2. Real repo with a recorded base: committed work, an UNCOMMITTED edit, and
#    an untracked file all appear (worktree compare, not BASE..HEAD).
G -c init.defaultBranch=main init -q
printf '.ai/\n' > "$WORK/.gitignore"
echo base > "$WORK/README.md"; G add -A; G commit -q -m base
BASE="$(G rev-parse HEAD)"
mkdir -p "$WORK/.ai/build"; printf '%s\n' "$BASE" > "$WORK/.ai/build/run-base-sha"
mkdir -p "$WORK/pkg"; echo 'committed line' > "$WORK/pkg/a.go"; G add -A; G commit -q -m "feat: a"
echo 'uncommitted edit' >> "$WORK/README.md"
echo 'brand new' > "$WORK/pkg/new.go"
run
check "repo exit 0"                    "0" "$RC"
check "repo marker last"               "review-diff-ready" "$(last)"
check "stdout is only the marker"      "review-diff-ready" "$OUT"
check "base recorded in header"        "yes" "$(inf "_Base: $BASE → working tree")"
check "files-changed section"          "yes" "$(inf '## Files changed')"
check "committed file listed"          "yes" "$(grep -qE '^A[[:space:]]+pkg/a.go' "$OUTF" && echo yes || echo no)"
check "modified tracked listed"        "yes" "$(grep -qE '^M[[:space:]]+README.md' "$OUTF" && echo yes || echo no)"
check "full diff has committed hunk"   "yes" "$(inf '+committed line')"
check "full diff has UNCOMMITTED hunk" "yes" "$(inf '+uncommitted edit')"
check "untracked section"              "yes" "$(inf '## Untracked files (not yet in any commit)')"
check "untracked path listed"          "yes" "$(inf 'pkg/new.go')"
check "untracked content NOT diffed"   "no"  "$(inf '+brand new')"
check "no UNAVAILABLE stamp"           "no"  "$(inf 'UNAVAILABLE')"
check "no stderr warning"              ""    "$ERR"

# 3. Clean tree, no untracked: section omitted.
G add -A; G commit -q -m "wip"
run
check "clean: no untracked section"    "no" "$(inf '## Untracked files')"
check "clean: README still in diff"    "yes" "$(inf '+uncommitted edit')"

# 4. Missing base file (fresh repo at Setup) and an unreachable SHA both
#    degrade to the empty tree: every tracked file shows as Added.
rm -f "$WORK/.ai/build/run-base-sha"
run
check "no base exit 0"                 "0" "$RC"
check "no base: README added"          "yes" "$(grep -qE '^A[[:space:]]+README.md' "$OUTF" && echo yes || echo no)"
echo deadbeefdeadbeefdeadbeefdeadbeefdeadbeef > "$WORK/.ai/build/run-base-sha"
run
check "bad sha exit 0"                 "0" "$RC"
check "bad sha: README added"          "yes" "$(grep -qE '^A[[:space:]]+README.md' "$OUTF" && echo yes || echo no)"
check "bad sha: empty-tree base"       "yes" "$(inf "_Base: $(G hash-object -t tree /dev/null)")"

# 5. A git diff failure (simulated with a PATH shim that fails `git diff`
#    but delegates everything else) stamps UNAVAILABLE, PRESERVES the captured
#    git output in a fenced block, warns on stderr, and still routes on.
REAL_GIT="$(command -v git)"
cat > "$STATE/bin/git" <<SHIM
#!/bin/sh
if [ "\$1" = diff ]; then echo "fatal: simulated bad object" >&2; exit 128; fi
exec "$REAL_GIT" "\$@"
SHIM
chmod +x "$STATE/bin/git"
printf '%s\n' "$BASE" > "$WORK/.ai/build/run-base-sha"
run
check "diff failure exit 0"            "0" "$RC"
check "diff failure marker last"       "review-diff-ready" "$(last)"
check "diff failure stderr warning"    "yes" "$(printf '%s' "$ERR" | grep -q 'review diff generation failed' && echo yes || echo no)"
check "UNAVAILABLE banner FIRST line"  "# Review diff UNAVAILABLE — diff generation failed. Read the working tree directly." "$(head -1 "$OUTF")"
check "underlying cause preserved"     "yes" "$(inf 'fatal: simulated bad object')"
check "captured output fenced"         "yes" "$(grep -c '^```$' "$OUTF" | grep -qx 2 && echo yes || echo no)"
check "no tmp file left"               "no" "$([ -e "$OUTF.tmp" ] && echo yes || echo no)"
rm -f "$STATE/bin/git"

if [ "$fail" = 0 ]; then echo "ALL PASS"; else echo "SOME FAILED"; exit 1; fi
