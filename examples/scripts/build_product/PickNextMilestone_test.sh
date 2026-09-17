#!/usr/bin/env bash
# ABOUTME: Fixture tests for PickNextMilestone.sh — counts done markers vs
# ABOUTME: plan headers, extracts milestone N into .ai/milestones/current.md,
# ABOUTME: records the milestone start SHA (#298), and routes on the LAST
# ABOUTME: stdout line (`milestone-N` / `all-done`) with fail-loud guards.
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
SCRIPT="$(stage_script "$DIR/PickNextMilestone.sh")"   # ${graph.workflow_dir} expanded as the engine does
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
check "m1 progress line"             "yes" "$(has 'milestone 1 (1 of 3 planned)')"
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
check "variant headers counted (3)"  "yes" "$(has 'milestone 1 (1 of 3 planned)')"
check "variant m1 extracted"         "yes" "$(grep -q 'body one' "$CUR" && echo yes || echo no)"
check "variant m1 bounded"           "no"  "$(grep -q 'body two' "$CUR" && echo yes || echo no)"
mark_done 1
run
check "variant m2 (single #) extracted" "yes" "$(grep -q 'body two' "$CUR" && echo yes || echo no)"
check "variant m2 bounded by m3"     "no" "$(grep -q 'body three' "$CUR" && echo yes || echo no)"

# 6b. #640 E5 (was KNOWN-BUG): ONE regex counts and extracts, so an
#     all-caps `## MILESTONE 3:` header bounds milestone 2 AND extracts as 3.
rm -rf "$WORK/.ai/milestones"
printf '## Milestone 1: One\nbody one\n## Milestone 2: Two\nbody two\n## MILESTONE 3: Three\nbody three\n' > "$PLAN"
mark_done 1
run
check "MILESTONE counted (3)"                 "yes" "$(has 'milestone 2 (2 of 3 planned)')"
check "m2 bounded by MILESTONE 3"             "no"  "$(grep -q 'body three' "$CUR" && echo yes || echo no)"
mark_done 2
run
check "MILESTONE 3 extracts"                  "0" "$RC"
check "MILESTONE 3 marker"                    "milestone-3" "$(last)"
check "MILESTONE 3 body"                      "yes" "$(grep -q 'body three' "$CUR" && echo yes || echo no)"

# 7. Milestone 1 must not swallow Milestone 10+ (the [^0-9] boundary).
rm -rf "$WORK/.ai/milestones"
: > "$PLAN"
for n in 1 2 3 4 5 6 7 8 9 10 11; do printf '## Milestone %s: Step %s\nbody-%s\n' "$n" "$n" "$n" >> "$PLAN"; done
run
check "11 milestones counted"        "yes" "$(has 'milestone 1 (1 of 11 planned)')"
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

# 9. #640 E4 (was KNOWN-BUG): a plan with NO milestone headers fires the
#    intended "no milestone headers found" message (no `grep -c || echo 0`
#    double-zero, no integer-expression error on stderr), exits 1, and
#    leaves NO current.md (B5: never an empty one for Implement to run on).
printf '# Plan\nno headers here\n' > "$PLAN"
run
check "no-header plan exit 1 (fails closed)"        "1" "$RC"
check "no-header intended message"                  "yes" "$(has 'ERROR: no milestone headers found')"
check "no-header no integer error on stderr"        "no" "$(printf '%s' "$ERR" | grep -qiE 'integer expression|Illegal number' && echo yes || echo no)"
check "no-header no current.md"                     "no" "$([ -e "$CUR" ] && echo yes || echo no)"
check "no-header no temp left"                      "no" "$([ -e "$WORK/.ai/milestones/.current.md.tmp" ] && echo yes || echo no)"

# 10. #640 E5 (was KNOWN-BUG): a bare `## Milestone 1` header (nothing after
#     the number) counts AND extracts.
printf '## Milestone 1\nbody one\n## Milestone 2\nbody two\n' > "$PLAN"
run
check "bare header counted (2)"       "yes" "$(has 'milestone 1 (1 of 2 planned)')"
check "bare header exit 0"            "0" "$RC"
check "bare header marker"            "milestone-1" "$(last)"
check "bare header body"              "yes" "$(grep -q 'body one' "$CUR" && echo yes || echo no)"
check "bare header bounded"           "no"  "$(grep -q 'body two' "$CUR" && echo yes || echo no)"

# 11. #640 E5 header-form tolerance from the hunt: `Milestone #1`, leading
#     zeros (`Milestone 02`), a TAB after `##`, `####`, a trailing `.`, and
#     `## Milestone 1.1` / `## Milestone overview` are NOT headers (the first
#     stays inside milestone 1's body, the second is skipped).
rm -rf "$WORK/.ai/milestones"
printf '## Milestone overview\nthree steps\n## Milestone #1: One\nbody one\n### Milestone 1.1 detail\nsub one\n##\tMilestone 02 — Two\nbody two\n#### Milestone 3.\nbody three\n' > "$PLAN"
run
check "forms: 3 counted"              "yes" "$(has 'milestone 1 (1 of 3 planned)')"
check "forms: overview not extracted" "no"  "$(grep -q 'three steps' "$CUR" && echo yes || echo no)"
check "forms: #1 extracted"           "yes" "$(grep -q 'body one' "$CUR" && echo yes || echo no)"
check "forms: 1.1 inside m1"          "yes" "$(grep -q 'sub one' "$CUR" && echo yes || echo no)"
check "forms: m1 bounded at 02"       "no"  "$(grep -q 'body two' "$CUR" && echo yes || echo no)"
mark_done 1
run
check "forms: 02 -> milestone-2"      "milestone-2" "$(last)"
check "forms: tab header extracted"   "yes" "$(grep -q 'body two' "$CUR" && echo yes || echo no)"
check "forms: done marker is milestone-2.md (no leading zero)" "milestone-2" "$(last)"
mark_done 2
run
check "forms: #### + trailing dot"    "milestone-3" "$(last)"
check "forms: m3 body"                "yes" "$(grep -q 'body three' "$CUR" && echo yes || echo no)"

# 12. #640 E5 numbering gap (1, 2, 4): NEXT is the smallest header number
#     without a done marker — milestone 2's section stops at 4's header, and
#     after 2 is done the pick is milestone-4 (never a phantom 3).
rm -rf "$WORK/.ai/milestones"
printf '## Milestone 1: One\nbody one\n## Milestone 2: Two\nbody two\n## Milestone 4: Four\nbody four\n' > "$PLAN"
mark_done 1
run
check "gap: m2 picked"                "milestone-2" "$(last)"
check "gap: m2 bounded at 4"          "no"  "$(grep -q 'body four' "$CUR" && echo yes || echo no)"
mark_done 2
run
check "gap: m4 picked (not 3)"        "milestone-4" "$(last)"
check "gap: m4 body"                  "yes" "$(grep -q 'body four' "$CUR" && echo yes || echo no)"
check "gap: progress 3 of 3"          "yes" "$(has 'milestone 4 (3 of 3 planned)')"
mark_done 4
run
check "gap: all done"                 "all-done" "$(last)"

# 13. #640 E5 duplicate `## Milestone 2` -> fail loud, no current.md.
rm -rf "$WORK/.ai/milestones"
printf '## Milestone 1: One\nbody one\n## Milestone 2: Two\nbody two\n## Milestone 2: Two again\nbody dup\n' > "$PLAN"
run
check "dup: exit 1"                   "1" "$RC"
check "dup: message names 2"          "yes" "$(has 'ERROR: duplicate milestone headers in .ai/decisions/milestones.md: 2')"
check "dup: no current.md"            "no"  "$([ -e "$CUR" ] && echo yes || echo no)"

# 14. #640 B4: after Cleanup removed .ai/build, PickNextMilestone recreates
#     it and still writes the start-sha.
rm -rf "$WORK/.ai/milestones" "$WORK/.ai/build"
printf '## Milestone 1: One\nbody one\n' > "$PLAN"
run
check "no .ai/build: exit 0"          "0" "$RC"
check "no .ai/build: marker"          "milestone-1" "$(last)"
check "no .ai/build: start-sha written" "yes" "$([ -f "$WORK/.ai/build/milestone-start-sha" ] && echo yes || echo no)"

# 15. #640 B1: stale done markers that are NOT in the plan (left by a prior
#     run's plan) do not block — the pick is by header number, so only
#     markers matching this plan's numbers count. (Setup/ResetReviewBudget
#     wipe them anyway; this pins the by-number contract.)
rm -rf "$WORK/.ai/milestones"
mark_done 7; mark_done 8
run
check "stale markers: m1 still picked" "milestone-1" "$(last)"

if [ "$fail" = 0 ]; then echo "ALL PASS"; else echo "SOME FAILED"; exit 1; fi
