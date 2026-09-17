#!/usr/bin/env bash
# ABOUTME: Fixture tests for MarkMilestoneDone.sh — moves current.md to the
# ABOUTME: done/ marker, appends the milestone entry to build-context.md (#298,
# ABOUTME: #351 metadata filtering), resets fix_attempts / turn_overrides /
# ABOUTME: milestone-start-sha, and prints `milestone-N-complete` last.
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
SCRIPT="$(stage_script "$DIR/MarkMilestoneDone.sh")"   # ${graph.workflow_dir} expanded as the engine does
run() { OUT="$( (cd "$WORK" && sh "$SCRIPT") 2>"$STATE/stderr")"; RC=$?; }
last() { printf '%s' "$OUT" | tail -1; }
CTX="$WORK/.ai/build/build-context.md"
G() { git -C "$WORK" -c user.name=t -c user.email=t@t "$@"; }
seed_current() { mkdir -p "$WORK/.ai/milestones" "$WORK/.ai/build"; printf '## Milestone %s: %s\n**Files**: x\n' "$1" "$2" > "$WORK/.ai/milestones/current.md"; }
exists() { [ -e "$WORK/$1" ] && echo present || echo gone; }

# 1. Outside git, first milestone: side effects + marker; the logging block
#    is a swallowed subshell so the missing repo cannot dead-stop the node.
seed_current 1 Scaffold
echo 2 > "$WORK/.ai/milestones/fix_attempts"
echo 1 > "$WORK/.ai/milestones/verify_fail_attempts"
echo TestA > "$WORK/.ai/milestones/known_failures"; echo TestA > "$WORK/.ai/milestones/known_failures.snapshot"
echo G404 > "$WORK/.ai/milestones/known_lint_failures.snapshot"
mkdir -p "$WORK/.tracker/turn_overrides"; echo 90 > "$WORK/.tracker/turn_overrides/Implement"
echo abc > "$WORK/.ai/build/milestone-start-sha"
run
check "no-git exit 0"                 "0" "$RC"
check "marker milestone-1-complete"   "milestone-1-complete" "$(last)"
check "done marker written"           "present" "$(exists .ai/milestones/done/milestone-1.md)"
check "done marker = current.md"      "## Milestone 1: Scaffold" "$(head -1 "$WORK/.ai/milestones/done/milestone-1.md")"
check "current.md removed"            "gone" "$(exists .ai/milestones/current.md)"
check "fix_attempts reset"            "gone" "$(exists .ai/milestones/fix_attempts)"
check "verify_fail_attempts reset"    "gone" "$(exists .ai/milestones/verify_fail_attempts)"
check "known_failures.snapshot reset" "gone" "$(exists .ai/milestones/known_failures.snapshot)"
check "known_lint_failures.snapshot reset" "gone" "$(exists .ai/milestones/known_lint_failures.snapshot)"
check "known_failures itself kept"    "present" "$(exists .ai/milestones/known_failures)"
check "turn_overrides reset"          "gone" "$(exists .tracker/turn_overrides)"
check "start-sha removed"             "gone" "$(exists .ai/build/milestone-start-sha)"
check "no-git build-context entry"    "yes" "$(grep -q '^## Milestone 1: Scaffold' "$CTX" && echo yes || echo no)"
check "no-git Files: (none)"          "yes" "$(grep -q '^Files: (none)' "$CTX" && echo yes || echo no)"
check "no-git Summary = title"        "yes" "$(grep -q '^Summary: ## Milestone 1: Scaffold' "$CTX" && echo yes || echo no)"

# 2. Git repo, START marker present: Files = START..HEAD diff, tracker/build
#    metadata filtered (#351), Summary = HEAD subject.
rm -rf "$WORK/.ai" "$WORK/.tracker"
G -c init.defaultBranch=main init -q
# Setup gitignores .ai/ — mirror that so the fixture's own scratch files are
# not part of the milestone diff (only a pre-#351 polluted .tracker/ is
# force-added below to prove the filter).
printf '.ai/\n' > "$WORK/.gitignore"
echo base > "$WORK/README.md"; G add -A; G commit -q -m "base"
START="$(G rev-parse HEAD)"
seed_current 1 "Core parser"
: > "$CTX"
printf '%s\n' "$START" > "$WORK/.ai/build/milestone-start-sha"
mkdir -p "$WORK/pkg/parse" "$WORK/.tracker/runs/r1" "$WORK/.ai/build"
echo code > "$WORK/pkg/parse/parse.go"; echo test > "$WORK/pkg/parse/parse_test.go"
echo meta > "$WORK/.tracker/runs/r1/checkpoint.json"; echo ctx > "$WORK/.ai/build/scoped-milestones.md"
G add -A; G add -f .tracker; G commit -q -m "feat(parse): add parser"
run
check "git exit 0"                    "0" "$RC"
check "marker again m1"               "milestone-1-complete" "$(last)"
check "Files lists source only"       "Files: pkg/parse/parse.go,pkg/parse/parse_test.go" "$(grep '^Files:' "$CTX")"
check "Summary = HEAD subject"        "Summary: feat(parse): add parser" "$(grep '^Summary:' "$CTX")"
check "Active source files line"      "yes" "$(grep -q '^Active source files (as of milestone 1): pkg/parse/parse.go,pkg/parse/parse_test.go' "$CTX" && echo yes || echo no)"

# 3. Second milestone with NO start marker (empty-tree base): every tracked
#    file is listed, done marker is milestone-2.
seed_current 2 Streaming
: > "$CTX"
run
check "m2 marker"                     "milestone-2-complete" "$(last)"
check "m2 done marker"                "present" "$(exists .ai/milestones/done/milestone-2.md)"
check "empty-tree base lists all"     "yes" "$(grep '^Files:' "$CTX" | grep -q 'README.md' && echo yes || echo no)"

# 4. Only metadata moved: the Files line says why it is empty (#351).
seed_current 3 MetaOnly
: > "$CTX"
printf '%s\n' "$(G rev-parse HEAD)" > "$WORK/.ai/build/milestone-start-sha"
echo more > "$WORK/.tracker/runs/r1/status.json"; G add -f .tracker; G commit -q -m "chore: metadata"
run
check "metadata-only Files line"      "Files: (only tracker/build metadata changed)" "$(grep '^Files:' "$CTX")"
check "metadata-only Summary=HEAD"    "Summary: chore: metadata" "$(grep '^Summary:' "$CTX")"

# 5. HEAD unmoved since START (no-op "mark done" override): Files (none) and
#    Summary degrades to the TITLE, not the prior milestone's subject.
seed_current 4 Noop
: > "$CTX"
printf '%s\n' "$(G rev-parse HEAD)" > "$WORK/.ai/build/milestone-start-sha"
run
check "noop Files (none)"             "Files: (none)" "$(grep '^Files:' "$CTX")"
check "noop Summary = title"          "Summary: ## Milestone 4: Noop" "$(grep '^Summary:' "$CTX")"

# 6. Unreachable START (retry rewrote history) degrades to the empty tree.
seed_current 5 Rewritten
: > "$CTX"
echo deadbeefdeadbeefdeadbeefdeadbeefdeadbeef > "$WORK/.ai/build/milestone-start-sha"
run
check "unreachable START exit 0"      "0" "$RC"
check "unreachable START lists all"   "yes" "$(grep '^Files:' "$CTX" | grep -q 'README.md' && echo yes || echo no)"

# 7. More than 12 files: capped list + "… and N more".
seed_current 6 Wide
: > "$CTX"
printf '%s\n' "$(G rev-parse HEAD)" > "$WORK/.ai/build/milestone-start-sha"
mkdir -p "$WORK/wide"; for i in $(seq 1 15); do echo "$i" > "$WORK/wide/f$i.go"; done
G add -A; G commit -q -m "feat: wide"
run
check "wide first line has 12"        "12" "$(grep '^Files: wide' "$CTX" | tr ',' '\n' | wc -l | tr -d ' ')"
check "wide overflow line"            "Files: … and 3 more" "$(grep '^Files: …' "$CTX")"

# 8. #640 B5: missing current.md fails LOUD with a message — no marker, no
#    done file (previously a bare `cp` error).
rm -rf "$WORK/.ai/milestones/current.md"
run
check "missing current.md exit 1"     "1" "$RC"
check "missing current.md message"    "yes" "$(printf '%s' "$OUT" | grep -qF 'ERROR: .ai/milestones/current.md is missing or empty' && echo yes || echo no)"
check "no marker on failure"          "no" "$(printf '%s' "$OUT" | grep -q -- '-complete' && echo yes || echo no)"
check "no phantom done marker"        "gone" "$(exists .ai/milestones/done/milestone-7.md)"

# 9. #640 B5: an EMPTY current.md is refused too — never a 0-byte done marker.
: > "$WORK/.ai/milestones/current.md"
run
check "empty current.md exit 1"       "1" "$RC"
check "empty current.md message"      "yes" "$(printf '%s' "$OUT" | grep -qF 'missing or empty' && echo yes || echo no)"
check "empty current.md kept (not consumed)" "present" "$(exists .ai/milestones/current.md)"
check "empty: no done marker"         "gone" "$(exists .ai/milestones/done/milestone-7.md)"

# 10. Done marker keyed by the HEADER number (gap plans: 1,2,4 -> after 2 the
#     current section is `## Milestone 4`), not done-count + 1.
seed_current 4 Gap
rm -rf "$WORK/.ai/milestones/done"; mkdir -p "$WORK/.ai/milestones/done"; touch "$WORK/.ai/milestones/done/milestone-1.md" "$WORK/.ai/milestones/done/milestone-2.md"
run
check "gap marker milestone-4-complete" "milestone-4-complete" "$(last)"
check "gap done file milestone-4.md"  "present" "$(exists .ai/milestones/done/milestone-4.md)"
check "gap no milestone-3.md"         "gone" "$(exists .ai/milestones/done/milestone-3.md)"
# A header without a number falls back to done-count + 1.
mkdir -p "$WORK/.ai/milestones"; printf 'no header here\nbody\n' > "$WORK/.ai/milestones/current.md"
run
check "numberless header -> count+1"  "milestone-4-complete" "$(last)"

# 11. #640 E9: LLM-written titles/subjects with backslash escapes reach
#     build-context.md verbatim (printf '%s', not echo — dash/xpg_echo would
#     interpret \f and \c and truncate the line).
mkdir -p "$WORK/.ai/milestones"; printf '## Milestone 5: form\\feed \\c tail\n' > "$WORK/.ai/milestones/current.md"
: > "$CTX"
G commit -q --allow-empty -m 'fix: escape \f and \c in subjects'
run
check "E9 exit 0"                     "0" "$RC"
check "E9 title verbatim"             "yes" "$(grep -qF '## Milestone 5: form\feed \c tail' "$CTX" && echo yes || echo no)"
check "E9 summary verbatim"           "yes" "$(grep -qF 'Summary: fix: escape \f and \c in subjects' "$CTX" && echo yes || echo no)"

if [ "$fail" = 0 ]; then echo "ALL PASS"; else echo "SOME FAILED"; exit 1; fi
