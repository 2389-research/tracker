#!/usr/bin/env bash
# ABOUTME: Fixture tests for CheckMilestoneOutputs.sh (#350 structural gate) —
# ABOUTME: reconciles the **Files**: lines Decompose declared against disk:
# ABOUTME: missing DIRECTORY or broken `go build` fails (outputs-missing),
# ABOUTME: missing files only warn, tokens are scoped to built milestones
# ABOUTME: (#439), parsed one-per-bullet (#440), and path-escapes are skipped.
set -uo pipefail
DIR="$(cd "$(dirname "$0")" && pwd)"
SCRIPT="$DIR/CheckMilestoneOutputs.sh"
fail=0
check() { # name expected actual
  if [ "$2" = "$3" ]; then echo "ok: $1"; else echo "FAIL: $1 — want '$2' got '$3'"; fail=1; fi
}
WORK="$(mktemp -d)"
STATE="$(mktemp -d)"
trap 'rm -rf "$WORK" "$STATE"' EXIT
# PATH shim: `go` logs its argv and exits with the code in $STATE/go-rc, so
# the Go-stack branch is hermetic (never a real `go build`).
mkdir -p "$STATE/bin"
cat > "$STATE/bin/go" <<SHIM
#!/bin/sh
echo "go \$*" >> "$STATE/go-calls"
exit "\$(cat "$STATE/go-rc")"
SHIM
chmod +x "$STATE/bin/go"; echo 0 > "$STATE/go-rc"
run() { OUT="$( (cd "$WORK" && PATH="$STATE/bin:$PATH" sh "$SCRIPT") 2>"$STATE/stderr")"; RC=$?; }
last() { printf '%s' "$OUT" | tail -1; }
has() { printf '%s' "$OUT" | grep -qF -- "$1" && echo yes || echo no; }
PLAN="$WORK/.ai/decisions/milestones.md"
plan() { mkdir -p "$WORK/.ai/decisions"; cat > "$PLAN"; }
mark_done() { mkdir -p "$WORK/.ai/milestones/done"; for n in "$@"; do touch "$WORK/.ai/milestones/done/milestone-$n.md"; done; }
reset() { rm -rf "$WORK"; mkdir -p "$WORK"; rm -f "$STATE/go-calls"; }

# 1. Missing / empty plan: outputs-missing, exit 1, actionable message.
run
check "no plan exit 1"                  "1" "$RC"
check "no plan marker last"             "outputs-missing" "$(last)"
check "no plan message"                 "yes" "$(has 'milestones.md missing or empty')"
mkdir -p "$WORK/.ai/decisions"; : > "$PLAN"
run
check "empty plan marker"               "outputs-missing" "$(last)"

# 2. Plan with headers but no Files lines: contract violation, exit 1.
plan <<'P'
## Milestone 1: A
**Done when**: it works
P
run
check "no Files exit 1"                 "1" "$RC"
check "no Files message"                "yes" "$(has "no '**Files**:' lines found")"
check "no Files marker"                 "outputs-missing" "$(last)"

# 3. Files lines that yield zero path-like tokens (prose only) → garbled, exit 1.
plan <<'P'
## Milestone 1: A
**Files**: the parser and its tests
P
run
check "garbled exit 1"                  "1" "$RC"
check "garbled message"                 "yes" "$(has 'yielded zero path-like tokens')"
check "garbled marker"                  "outputs-missing" "$(last)"

# 4. Everything declared is on disk (non-Go stack): outputs-present LAST,
#    exit 0, token count reported, `go` never invoked.
reset
plan <<'P'
## Milestone 1: Scaffold
**Files**: `cmd/app/main.go`, `README.md`
## Milestone 2: Lib
**Files**:
- `pkg/lib/lib.go` (new)
- `pkg/lib/lib_test.go`
- `docs/` (directory)
P
mkdir -p "$WORK/cmd/app" "$WORK/pkg/lib" "$WORK/docs"
touch "$WORK/cmd/app/main.go" "$WORK/README.md" "$WORK/pkg/lib/lib.go" "$WORK/pkg/lib/lib_test.go"
run
check "present exit 0"                  "0" "$RC"
check "present marker last"             "outputs-present" "$(last)"
check "present count"                   "yes" "$(has 'structural existence gate passed: 4 declared path tokens reconciled')"
check "no go on non-Go stack"           "no" "$([ -e "$STATE/go-calls" ] && echo yes || echo no)"
check "no WARNING"                      "no" "$(has 'WARNING')"

# 5. Missing FILE only (dir exists): WARNING, still outputs-present / exit 0
#    (Decompose Files lists include files to delete).
rm -f "$WORK/pkg/lib/lib_test.go"
run
check "missing file exit 0"             "0" "$RC"
check "missing file marker"             "outputs-present" "$(last)"
check "missing file WARNING"            "yes" "$(has 'WARNING: declared files not on disk')"
check "missing file named"              "yes" "$(has '  - pkg/lib/lib_test.go')"

# 6. Missing DIRECTORY (trailing-slash declaration): structural fail.
rm -rf "$WORK/docs"
run
check "missing dir exit 1"              "1" "$RC"
check "missing dir marker last"         "outputs-missing" "$(last)"
check "missing dir named"               "yes" "$(has '  - docs')"
check "missing dir headline"            "yes" "$(has 'declared milestone output directories are MISSING')"
check "file miss also reported"         "yes" "$(has 'Declared files also not on disk')"

# 7. Missing PARENT dir of a declared file (cmd/goblin/ never created — the
#    actual #350 case): structural fail, parent reported once.
mkdir -p "$WORK/docs"
plan <<'P'
## Milestone 1: Goblin
**Files**: `cmd/goblin/main.go`, `cmd/goblin/flags.go`
P
run
check "missing parent exit 1"           "1" "$RC"
check "missing parent marker"           "outputs-missing" "$(last)"
check "missing parent named"            "1" "$(printf '%s' "$OUT" | grep -c '^  - cmd/goblin$')"

# 8. Both markers can't both route: outputs-missing output must not ALSO
#    contain outputs-present (the .dip evaluates outputs-missing first anyway).
check "no present marker on fail"       "no" "$(has 'outputs-present')"

# 9. Go stack: `go build ./...` runs; a failing build is structural even when
#    every declared path exists.
plan <<'P'
## Milestone 1: A
**Files**: `cmd/app/main.go`
P
touch "$WORK/go.mod"
echo 0 > "$STATE/go-rc"; rm -f "$STATE/go-calls"
run
check "go build invoked"                "go build ./..." "$(cat "$STATE/go-calls")"
check "go green exit 0"                 "0" "$RC"
check "go green marker"                 "outputs-present" "$(last)"
echo 2 > "$STATE/go-rc"
run
check "go build fail exit 1"            "1" "$RC"
check "go build fail marker"            "outputs-missing" "$(last)"
check "go build fail message"           "yes" "$(has 'go build ./... failed (exit 2)')"
echo 0 > "$STATE/go-rc"; rm -f "$WORK/go.mod"

# 10. #439 scoping: with 1 of 2 milestones done, milestone 2's unbuilt dir
#     is NOT checked (accept path); with 0 done, the whole plan is checked.
reset
plan <<'P'
## Milestone 1: Built
**Files**: `cmd/app/main.go`
## Milestone 2: Not yet
**Files**: `cmd/later/`
P
mkdir -p "$WORK/cmd/app"; touch "$WORK/cmd/app/main.go"
mark_done 1
run
check "scoped exit 0"                   "0" "$RC"
check "scoped marker"                   "outputs-present" "$(last)"
check "scoped count (m1 only)"          "yes" "$(has '1 declared path tokens reconciled')"
check "scoped plan file written"        "yes" "$(grep -q 'Milestone 1' "$WORK/.ai/build/scoped-milestones.md" && ! grep -q 'Milestone 2' "$WORK/.ai/build/scoped-milestones.md" && echo yes || echo no)"
rm -rf "$WORK/.ai/milestones"
run
check "unscoped exit 1"                 "1" "$RC"
check "unscoped names cmd/later"        "yes" "$(has '  - cmd/later')"
# Normal all-done entry: DONE_COUNT == TOTAL, whole plan checked.
mark_done 1 2
run
check "all-done still checks m2"        "outputs-missing" "$(last)"

# 11. Header tolerance + one-path-per-bullet (#440): "Files:" without bold,
#     "- **Files:**", prose after `(`/`#`, first backticked token preferred.
reset
plan <<'P'
## Milestone 1: A
Files: cmd/app/main.go (entry point; see #12)
## Milestone 2: B
- **Files:** `pkg/x.go` # the core
## Milestone 3: C
**Files**:
- Deps struct in `pkg/deps.go` and NewRepo(t)
- go.sum
- ./pkg/rel.go.
P
mkdir -p "$WORK/cmd/app" "$WORK/pkg"
touch "$WORK/cmd/app/main.go" "$WORK/pkg/x.go" "$WORK/pkg/deps.go" "$WORK/go.sum" "$WORK/pkg/rel.go"
run
check "variants exit 0"                 "0" "$RC"
check "variants marker"                 "outputs-present" "$(last)"
check "variants: 5 tokens"              "yes" "$(has '5 declared path tokens reconciled')"
check "variants no WARNING"             "no" "$(has 'WARNING')"
check "parsed list"                     "cmd/app/main.go pkg/x.go pkg/deps.go go.sum pkg/rel.go" "$(paste -sd' ' "$WORK/.ai/build/declared-files.list")"

# 11b. KNOWN-BUG: "- **Files:** pkg/x.go" (colon INSIDE the bold, no
#      backticks) — a variant the script's own comment says it handles — loses
#      the inline path: after the header strip the line is "** pkg/x.go", the
#      bullet-strip removes one `*`, the first whitespace token is the other
#      `*`, and the metachar strip leaves "". As the only declaration it is
#      reported as garbled (exit 1, outputs-missing) even though the file
#      exists. Backticked paths (case 11) are unaffected because the first
#      backticked token is preferred. When fixed, flip to exit 0 / present.
reset
plan <<'P'
## Milestone 1: A
- **Files:** pkg/x.go
P
mkdir -p "$WORK/pkg"; touch "$WORK/pkg/x.go"
run
check "KNOWN-BUG bold-colon inline path dropped (want 0)"   "1" "$RC"
check "KNOWN-BUG bold-colon reported garbled (want present)" "outputs-missing" "$(last)"
check "KNOWN-BUG bold-colon garbled message"                 "yes" "$(has 'yielded zero path-like tokens')"

# 12. Path-escape guard: absolute, home-anchored and parent-escaping tokens
#     are skipped (never re-anchor an existence check outside the workdir),
#     and prose words without a separator or dot are dropped.
reset
plan <<'P'
## Milestone 1: A
**Files**:
- /etc/passwd
- ~/secrets
- ../outside/file.go
- README.md
- justaword
P
touch "$WORK/README.md"
run
check "escapes exit 0"                  "0" "$RC"
check "escapes: only README checked"    "yes" "$(has '1 declared path tokens reconciled')"
check "escapes marker"                  "outputs-present" "$(last)"

# 13. KNOWN-QUIRK: an inline comma-separated list WITHOUT backticks keeps the
#     trailing comma on the first token ("a.go," is checked, not "a.go"), so
#     an existing file is reported as a phantom missing file. Warning-only,
#     so routing is unaffected; pinned so the #440 parser's limits are visible.
reset
plan <<'P'
## Milestone 1: A
**Files**: a.go, b.go
P
touch "$WORK/a.go" "$WORK/b.go"
run
check "KNOWN-QUIRK comma token exit 0"        "0" "$RC"
check "KNOWN-QUIRK phantom 'a.go,' warned"    "yes" "$(has '  - a.go,')"

if [ "$fail" = 0 ]; then echo "ALL PASS"; else echo "SOME FAILED"; exit 1; fi
