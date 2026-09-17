#!/usr/bin/env bash
# ABOUTME: Fixture tests for PickNextMilestone.sh — counts done markers vs
# ABOUTME: plan headers, extracts milestone N into .ai/milestones/current.md,
# ABOUTME: records the milestone start SHA (#298), and routes on the LAST
# ABOUTME: stdout line (`milestone-N` / `all-done`) with fail-loud guards.
set -uo pipefail
DIR="$(cd "$(dirname "$0")" && pwd)"
SCRIPT="$DIR/PickNextMilestone.sh"
fail=0
check() { # name expected actual
  if [ "$2" = "$3" ]; then echo "ok: $1"; else echo "FAIL: $1 — want '$2' got '$3'"; fail=1; fi
}
WORK="$(mktemp -d)"
STATE="$(mktemp -d)"
trap 'rm -rf "$WORK" "$STATE"' EXIT
run() { OUT="$( (cd "$WORK" && sh "$SCRIPT") 2>"$STATE/stderr")"; RC=$?; ERR="$(cat "$STATE/stderr")"; }
last() { printf '%s' "$OUT" | tail -1; }
has() { printf '%s' "$OUT" | grep -qF -- "$1" && echo yes || echo no; }
PLAN="$WORK/.ai/decisions/milestones.md"
CUR="$WORK/.ai/milestones/current.md"
mark_done() { mkdir -p "$WORK/.ai/milestones/done"; touch "$WORK/.ai/milestones/done/milestone-$1.md"; }

# Plan in the shape Decompose.md mandates (## Milestone N: title + **Files** etc).
mkdir -p "$WORK/.ai/decisions" "$WORK/.ai/build"
cat > "$PLAN" <<'PLAN'
# Build plan

Summary: three milestones.

## Milestone 1: Scaffold module
**Depends on**: none
**Files**: `go.mod`, `cmd/app/main.go`
**Done when**: `go build ./...` passes
**Verify command**: go build ./...
**DO NOT implement**: none

## Milestone 2: Core parser
**Depends on**: 1
**Files**: `pkg/parse/parse.go`
**Done when**: TestParse passes
**Verify command**: go test ./pkg/parse
**DO NOT implement**: streaming (spec §5, deferred to milestone 3)

## Milestone 3: Streaming
**Depends on**: 2
**Files**: `pkg/parse/stream.go`
**Done when**: TestStream passes
**Verify command**: go test ./pkg/parse
**DO NOT implement**: none
PLAN

# 1. Fresh run, no commits: milestone 1 extracted, start SHA genuinely empty.
run
check "m1 exit 0"                    "0" "$RC"
check "m1 marker last"               "milestone-1" "$(last)"
check "m1 progress line"             "yes" "$(has 'milestone 1 of 3')"
check "current.md has m1 header"     "yes" "$(grep -q '^## Milestone 1: Scaffold module' "$CUR" && echo yes || echo no)"
check "current.md has m1 body"       "yes" "$(grep -q 'DO NOT implement\*\*: none' "$CUR" && echo yes || echo no)"
check "current.md excludes m2"       "no"  "$(grep -q 'Milestone 2' "$CUR" && echo yes || echo no)"
check "start-sha file written"       "yes" "$([ -f "$WORK/.ai/build/milestone-start-sha" ] && echo yes || echo no)"
check "start-sha empty w/o commits"  "" "$(cat "$WORK/.ai/build/milestone-start-sha")"
check "no literal HEAD leak"         "no" "$(grep -q HEAD "$WORK/.ai/build/milestone-start-sha" && echo yes || echo no)"

# 2. With a commit, the start SHA is HEAD (the files-touched base for #298).
git -C "$WORK" -c init.defaultBranch=main init -q
git -C "$WORK" -c user.name=t -c user.email=t@t commit -q --allow-empty -m base
HEAD_SHA="$(git -C "$WORK" rev-parse HEAD)"
run
check "start-sha = HEAD"             "$HEAD_SHA" "$(cat "$WORK/.ai/build/milestone-start-sha")"

# 3. Middle milestone: section is bounded by the next header (excluded).
mark_done 1
run
check "m2 marker last"               "milestone-2" "$(last)"
check "current.md has m2 header"     "yes" "$(grep -q '^## Milestone 2: Core parser' "$CUR" && echo yes || echo no)"
check "current.md m2 DO NOT line"    "yes" "$(grep -q 'streaming (spec §5' "$CUR" && echo yes || echo no)"
check "next header stripped"         "no"  "$(grep -q 'Milestone 3' "$CUR" && echo yes || echo no)"
check "prev header absent"           "no"  "$(grep -q 'Milestone 1' "$CUR" && echo yes || echo no)"

# 4. Last milestone runs to EOF.
mark_done 2
run
check "m3 marker last"               "milestone-3" "$(last)"
check "current.md m3 to EOF"         "yes" "$(grep -q 'TestStream passes' "$CUR" && echo yes || echo no)"

# 5. All done: ALL_MILESTONES_COMPLETE + `all-done` last; current.md and the
#    start-sha are NOT (re)written on this path.
mark_done 3
rm -f "$CUR" "$WORK/.ai/build/milestone-start-sha"
run
check "all-done exit 0"              "0" "$RC"
check "all-done marker last"         "all-done" "$(last)"
check "ALL_MILESTONES_COMPLETE line" "yes" "$(has 'ALL_MILESTONES_COMPLETE')"
check "no current.md on all-done"    "no" "$([ -e "$CUR" ] && echo yes || echo no)"
check "no start-sha on all-done"     "no" "$([ -e "$WORK/.ai/build/milestone-start-sha" ] && echo yes || echo no)"

# 6. Header tolerance (LLM-written): `###`, em-dash suffix, lowercase m, single `#`.
rm -rf "$WORK/.ai/milestones"
cat > "$PLAN" <<'PLAN'
### Milestone 1 — Setup
body one
# milestone 2: Two
body two
## Milestone 3: Three
body three
PLAN
run
check "variant headers counted (3)"  "yes" "$(has 'milestone 1 of 3')"
check "variant m1 extracted"         "yes" "$(grep -q 'body one' "$CUR" && echo yes || echo no)"
check "variant m1 bounded"           "no"  "$(grep -q 'body two' "$CUR" && echo yes || echo no)"
mark_done 1
run
check "variant m2 (single #) extracted" "yes" "$(grep -q 'body two' "$CUR" && echo yes || echo no)"
check "variant m2 bounded by m3"     "no" "$(grep -q 'body three' "$CUR" && echo yes || echo no)"

# 6b. KNOWN-BUG (regex mismatch): the TOTAL count is case-insensitive
#     (`grep -ciE`) but the extraction range only accepts `[Mm]ilestone`, so an
#     all-caps `## MILESTONE 3:` header is COUNTED yet neither bounds
#     milestone 2 nor extracts as milestone 3 (exit 1 "failed to extract").
#     Fails closed; pinned so the two regexes are kept in sync deliberately.
#     When fixed, m2 must exclude 'body three' and m3 must extract.
rm -rf "$WORK/.ai/milestones"
printf '## Milestone 1: One\nbody one\n## Milestone 2: Two\nbody two\n## MILESTONE 3: Three\nbody three\n' > "$PLAN"
mark_done 1
run
check "KNOWN-BUG MILESTONE counted (3)"                 "yes" "$(has 'milestone 2 of 3')"
check "KNOWN-BUG m2 leaks into MILESTONE 3 (want no)"   "yes" "$(grep -q 'body three' "$CUR" && echo yes || echo no)"
mark_done 2
run
check "KNOWN-BUG MILESTONE 3 not extractable (want 0)"  "1" "$RC"

# 7. Milestone 1 must not swallow Milestone 10+ (the [^0-9] boundary).
rm -rf "$WORK/.ai/milestones"
: > "$PLAN"
for n in 1 2 3 4 5 6 7 8 9 10 11; do printf '## Milestone %s: Step %s\nbody-%s\n' "$n" "$n" "$n" >> "$PLAN"; done
run
check "11 milestones counted"        "yes" "$(has 'milestone 1 of 11')"
check "m1 does not include m10"      "no"  "$(grep -q 'body-10' "$CUR" && echo yes || echo no)"
check "m1 bounded at m2"             "no"  "$(grep -q 'body-2' "$CUR" && echo yes || echo no)"
for n in 1 2 3 4 5 6 7 8 9; do mark_done $n; done
run
check "m10 marker"                   "milestone-10" "$(last)"
check "m10 body"                     "yes" "$(grep -q 'body-10' "$CUR" && echo yes || echo no)"
check "m10 bounded at m11"           "no"  "$(grep -q 'body-11' "$CUR" && echo yes || echo no)"

# 8. Missing plan: fails loudly with the format hint.
rm -rf "$WORK/.ai/milestones"; rm -f "$PLAN"
run
check "missing plan exit 1"          "1" "$RC"
check "missing plan message"         "yes" "$(has 'ERROR: no milestone headers found')"
check "missing plan format hint"     "yes" "$(has 'Expected format: ## Milestone N: Title')"

# 9. KNOWN-BUG: a plan with NO milestone headers. `grep -c` prints "0" AND
#    exits 1, so `|| echo 0` prints a second 0 → TOTAL is "0\n0" and the
#    `[ "$TOTAL" -eq 0 ]` guard errors ("integer expression expected" /
#    dash "Illegal number") instead of firing the intended "no milestone
#    headers found" message. It still fails CLOSED (exit 1) via the empty-
#    extraction guard, so the routing outcome is right — only the diagnostic
#    is wrong. When fixed (e.g. `grep -ciE ... || true` with a `${TOTAL:-0}`
#    default, or `|| TOTAL=0`), flip the expectation to "yes"/"no".
printf '# Plan\nno headers here\n' > "$PLAN"
run
check "no-header plan exit 1 (fails closed)"        "1" "$RC"
check "KNOWN-BUG intended message missing (want yes when fixed)" "no" "$(has 'ERROR: no milestone headers found')"
check "KNOWN-BUG integer error leaks to stderr (want no when fixed)" "yes" "$(printf '%s' "$ERR" | grep -qiE 'integer expression|Illegal number' && echo yes || echo no)"

# 10. KNOWN-BUG (contract strictness): a bare `## Milestone 1` header with
#     nothing after the number is COUNTED by the total regex but the
#     extraction range requires a trailing non-digit (`$NEXT[^0-9]`), so
#     current.md comes out empty and the node exits 1 "failed to extract".
#     Decompose.md mandates `## Milestone N: [title]`, so a compliant planner
#     never hits this — pinned so the mismatch between the two regexes is
#     visible. When fixed, flip to exit 0 / marker milestone-1.
printf '## Milestone 1\nbody one\n## Milestone 2\nbody two\n' > "$PLAN"
run
check "KNOWN-BUG bare header counted (2)"          "yes" "$(has 'milestone 1 of 2')"
check "KNOWN-BUG bare header exit 1 (want 0 when fixed)" "1" "$RC"
check "KNOWN-BUG bare header extraction message"   "yes" "$(has 'ERROR: failed to extract milestone 1')"

if [ "$fail" = 0 ]; then echo "ALL PASS"; else echo "SOME FAILED"; exit 1; fi
