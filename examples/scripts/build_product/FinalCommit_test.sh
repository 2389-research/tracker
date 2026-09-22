#!/usr/bin/env bash
# ABOUTME: Fixture tests for FinalCommit.sh (#656) — the deterministic final
# ABOUTME: committer that replaced the auto_status agent. A clean / already-
# ABOUTME: committed tree is a SUCCESS (reports HEAD, marker last); a dirty tree
# ABOUTME: is committed once; a genuine problem (not-a-repo, no HEAD, hook
# ABOUTME: reject) exits non-zero and is routed to AbortRun. Secrets excluded.
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
SCRIPT="$(stage_script "$DIR/FinalCommit.sh")"   # ${graph.workflow_dir} expanded as the engine does
run() { OUT="$( (cd "$WORK" && ${TEST_SH:-sh} "$SCRIPT") 2>"$STATE/stderr")"; RC=$?; }
last() { printf '%s' "$OUT" | tail -1; }
G() { git -C "$WORK" "$@"; }
commits() { G rev-list --count HEAD 2>/dev/null || echo 0; }
head_hash() { G rev-parse HEAD 2>/dev/null || echo none; }
tracked() { G ls-files --error-unmatch -- "$1" >/dev/null 2>&1 && echo tracked || echo untracked; }
has_marker() { printf '%s' "$OUT" | grep -q 'final-commit-done' && echo yes || echo no; }

# 1. Not a git repo → fail loud, no marker (build_product requires: git).
run
check "no-git exits non-zero"     "yes" "$([ "$RC" -ne 0 ] && echo yes || echo no)"
check "no-git prints no marker"   "no"  "$(has_marker)"
check "no-git error message"      "yes" "$(printf '%s' "$OUT$(cat "$STATE/stderr")" | grep -q 'not a git repository' && echo yes || echo no)"

# 2. Empty repo, NO HEAD (nothing was ever committed the whole run) → genuine
#    failure: a clean tree with no commit means no build happened. Non-zero.
G -c init.defaultBranch=main init -q
run
check "no-HEAD exits non-zero"    "yes" "$([ "$RC" -ne 0 ] && echo yes || echo no)"
check "no-HEAD prints no marker"  "no"  "$(has_marker)"

# 3. Clean / already-committed tree → SUCCESS: reports HEAD, marker last, no new
#    commit. THIS is the #656 case that used to crash the run.
echo base > "$WORK/README.md"; G add -A; G -c user.name=t -c user.email=t@t commit -q -m base
BASE_HEAD="$(head_hash)"
run
check "clean exit 0"              "0" "$RC"
check "clean marker last"         "final-commit-done" "$(last)"
check "clean: no new commit"      "1" "$(commits)"
check "clean: reports HEAD"       "yes" "$(printf '%s' "$OUT" | grep -q "$BASE_HEAD" && echo yes || echo no)"

# 4. Dirty tree (untracked + modified) → committed once, HEAD advanced, success.
echo change >> "$WORK/README.md"
mkdir -p "$WORK/pkg"; echo 'package pkg' > "$WORK/pkg/a.go"
run
check "dirty exit 0"              "0" "$RC"
check "dirty marker last"         "final-commit-done" "$(last)"
check "dirty: committed once"     "2" "$(commits)"
check "dirty: a.go tracked"       "tracked" "$(tracked pkg/a.go)"
check "dirty: HEAD advanced"      "yes" "$([ "$(head_hash)" != "$BASE_HEAD" ] && echo yes || echo no)"

# 5. Untracked secret (private-key .pem) is NOT staged, warned, left on disk.
printf -- '-----BEGIN RSA PRIVATE KEY-----\nMIIfake\n-----END RSA PRIVATE KEY-----\n' > "$WORK/server.pem"
echo 'more' >> "$WORK/pkg/a.go"
run
check "secret exit 0"             "0" "$RC"
check "secret NOT committed"      "untracked" "$(tracked server.pem)"
check "secret warned"             "yes" "$(printf '%s' "$OUT" | grep -qi 'secret' && echo yes || echo no)"

# 6. Staged change + a failing pre-commit hook → fail loud, non-zero, no marker.
HOOK="$WORK/.git/hooks/pre-commit"; printf '#!/bin/sh\necho "hook says no" >&2\nexit 1\n' > "$HOOK"; chmod +x "$HOOK"
echo 'trigger' >> "$WORK/README.md"
run
check "hook-reject non-zero"      "yes" "$([ "$RC" -ne 0 ] && echo yes || echo no)"
check "hook-reject no marker"     "no"  "$(has_marker)"

echo "---"
[ "$fail" -eq 0 ] && echo "PASS" || echo "FAILED"
exit "$fail"
