#!/usr/bin/env bash
# ABOUTME: Unit fixtures for the pipe-free assertion helpers in the dotpowers
# ABOUTME: test_helpers.sh (#658) — same contract as build_product (has / has_line / has_re …).
set -uo pipefail
DIR="$(cd "$(dirname "$0")" && pwd)"
STATE="$(mktemp -d)"
trap 'rm -rf "$STATE"' EXIT
fail=0
check() { # name expected actual
  if [ "$2" = "$3" ]; then echo "ok: $1"; else echo "FAIL: $1 — want '$2' got '$3'"; fail=1; fi
}
. "$DIR/test_helpers.sh"

# 1. The real shape that flaked on CI: a five-line, ~350-byte rerun log. The
#    first-line needle is the one `printf | grep -q` lost to SIGPIPE.
OUT='phase1-worktrees: pruned stale worktree .ai/worktrees/stream-a (branch build/stream-a gone)
phase1-worktrees: deleted build/stream-b (already merged into HEAD)
phase1-worktrees: build/stream-c has unmerged work — renamed to build/stream-c.stale.20260101T000000Z
phase1-worktrees: created .ai/worktrees/stream-a on build/stream-a from main
phase1-worktrees-ready'
check "has: first line"                    "yes" "$(has 'pruned stale worktree')"
check "has: middle line"                   "yes" "$(has 'already merged into HEAD')"
check "has: last line"                     "yes" "$(has 'phase1-worktrees-ready')"
check "has: absent"                        "no"  "$(has 'never printed')"
check "has: needle spanning lines"         "yes" "$(has 'from main
phase1-worktrees-ready')"
check "has_line: exact last line"          "yes" "$(has_line 'phase1-worktrees-ready')"
check "has_line: exact first line"         "yes" "$(has_line 'phase1-worktrees: pruned stale worktree .ai/worktrees/stream-a (branch build/stream-a gone)')"
check "has_line: substring is not a line"  "no"  "$(has_line 'phase1-worktrees')"
check "has_re: anchors per line"           "yes" "$(has_re '^phase1-worktrees-ready$')"
check "has_re: alternation"                "yes" "$(has_re 'renamed to build/stream-c\.stale\.[0-9]{8}T[0-9]{6}Z$')"
check "has_re: absent"                     "no"  "$(has_re '^ready$')"
check "first_line"                         "phase1-worktrees: pruned stale worktree .ai/worktrees/stream-a (branch build/stream-a gone)" "$(first_line "$OUT")"
check "last_line"                          "phase1-worktrees-ready" "$(last_line "$OUT")"

# 2. Glob metacharacters in the needle are literal (the pattern side of
#    `case` is quoted): `*`, `?`, `[` must not act as wildcards.
OUT='go test ./...;golangci-lint run
  - pkg/[a-z]*
is it ok?
plain'
check "has: literal *"                     "yes" "$(has './...')"
check "has: literal [ ] *"                 "yes" "$(has '[a-z]*')"
check "has: literal ?"                     "yes" "$(has 'ok?')"
check "has: * is not a wildcard"           "no"  "$(has 'go*lint')"
check "has: ? is not a wildcard"           "no"  "$(has 'plai?')"
check "has: [ ] is not a class"            "no"  "$(has 'p[lk]ain')"
check "has_line: literal metachars"        "yes" "$(has_line '  - pkg/[a-z]*')"
check "has_line: ? not a wildcard"         "no"  "$(has_line 'plai?')"
check "contains: other haystack"           "yes" "$(contains 'a;b;c' 'b;c')"
check "contains: absent"                   "no"  "$(contains 'a;b;c' 'd')"
check "has_line_in: exact"                 "yes" "$(has_line_in $'x\ny\nz' 'y')"
check "has_line_in: single line hay"       "yes" "$(has_line_in 'only' 'only')"
check "has_line_in: substring not a line"  "no"  "$(has_line_in $'x\nyy\nz' 'y')"
check "has_re_in: anchors"                 "yes" "$(has_re_in $'make ci\ngo test' '^go test$')"
check "first_line: single line"            "only" "$(first_line only)"
check "last_line: single line"             "only" "$(last_line only)"
check "first_line: empty"                  ""    "$(first_line '')"

# 3. A haystack far above pipe capacity (>64 KiB) — the here-string path
#    switches to a temp file and the case-glob path must not blow up.
OUT="$(seq 1 20000)"   # ~109 KB
check "big: size sanity"                   "yes" "$([ "${#OUT}" -gt 65536 ] && echo yes || echo no)"
check "big: has first"                     "yes" "$(has '1')"
check "big: has_line last"                 "yes" "$(has_line '20000')"
check "big: has_line absent"               "no"  "$(has_line '20001')"
check "big: has_re anchored last"          "yes" "$(has_re '^20000$')"
check "big: has_re absent"                 "no"  "$(has_re '^20001$')"
check "big: last_line"                     "20000" "$(last_line "$OUT")"

# 4. Empty haystack never matches a non-empty needle.
OUT=''
check "empty: has"                         "no"  "$(has 'x')"
check "empty: has_line"                    "no"  "$(has_line 'x')"
check "empty: has_re"                      "no"  "$(has_re 'x')"

[ "$fail" = 0 ] && echo "ALL PASS" || { echo "SOME FAILED"; exit 1; }
