#!/usr/bin/env bash
# ABOUTME: Fixture tests for Setup.sh — workspace scaffolding, #553 spec-input
# ABOUTME: adoption, #318/#264 fresh-run resets, #418 run-base-sha, and the
# ABOUTME: orchestration of the lib/ helpers (gitignore seeding, .tracker/
# ABOUTME: exclusion, installing verify.sh / ci-probe.sh / rubric into .ai/build).
#
# Setup.sh branch map:
#   A  scaffold .ai/{build,decisions,milestones}; seed_gitignore (`.ai/` + the
#      #405 patterns — dedupe/sort details: lib/gitignore_test.sh)
#   B  #351 exclude_tracker_metadata in a git repo: `.tracker/` ->
#      .git/info/exclude; untrack a pre-#351 committed .tracker/ (details:
#      lib/gitignore_test.sh)
#   C  #318 rm -rf .tracker/turn_overrides (fresh run only; Setup is skipped on
#      checkpoint resume, so a resumed run keeps its state)
#   D  #553 adopt .tracker/inputs/spec -> SPEC.md (overwrites)
#   E  no SPEC.md -> exit 1 with the `tracker init build_product` hint
#   F  install lib/{ci-probe.sh,verify.sh,iface-reachability-rubric.md} into
#      .ai/build/ byte-for-byte (their green-gate logic: lib/verify_test.sh,
#      lib/ci-probe_test.sh)
#   G  best-effort build-context.md (#298) — can never fail Setup
#   H  #418 run-base-sha: HEAD, or empty on a commitless repo / non-repo
#   I  #264 rm spec_forge_attempts, SPEC.original.md, spec-forge-log.md
#   J  `setup-ready` marker last
#   K  #640 B1 fresh-run reset: .ai/milestones/ (done/, current.md, fix /
#      verify counters, known_failures + snapshots), .ai/build per-plan
#      counters/scratch — via lib/milestones.sh reset_plan_state
#   L  #640 C5 dirty-tree preflight: uncommitted/untracked files (other than
#      .ai/, .tracker/, SPEC.md, .gitignore, *.dip) -> exit 1 listing them;
#      opt-out stamp .ai/build/allow-dirty
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
SCRIPT="$(stage_script "$DIR/Setup.sh")"   # ${graph.workflow_dir} expanded as the engine does
run() { OUT="$( (cd "$WORK" && sh "$SCRIPT") 2>"$STATE/stderr")"; RC=$?; }
last() { printf '%s' "$OUT" | tail -1; }
has() { printf '%s' "$OUT" | grep -qF -- "$1" && echo yes || echo no; }
exists() { [ -e "$WORK/$1" ] && echo present || echo gone; }
G() { git -C "$WORK" -c user.name=t -c user.email=t@t "$@"; }
reset() { rm -rf "$WORK"; mkdir -p "$WORK"; }

# ── E. Missing SPEC.md: fail loud with the actionable hint; scaffolding
#      before the check still happened; no marker.
run
check "no SPEC exit 1"                    "1" "$RC"
check "no SPEC error line"                "yes" "$(has 'ERROR: SPEC.md not found in repo root.')"
check "no SPEC init hint"                 "yes" "$(has 'tracker init build_product')"
check "no SPEC no marker"                 "no"  "$(has 'setup-ready')"
check "dirs scaffolded before check"      "present" "$(exists .ai/milestones)"

# ── A/F/H/I/J. Fresh non-git workdir with a SPEC.md.
reset
printf 'line1\nline2\nline3\n' > "$WORK/SPEC.md"
mkdir -p "$WORK/.tracker/turn_overrides" "$WORK/.ai/decisions" "$WORK/.ai/build"
echo 90 > "$WORK/.tracker/turn_overrides/Implement"
echo 3 > "$WORK/.ai/build/spec_forge_attempts"
echo old > "$WORK/.ai/decisions/SPEC.original.md"
echo old > "$WORK/.ai/decisions/spec-forge-log.md"
echo keep > "$WORK/.ai/decisions/milestones.md"
# K. #640 B1: leftover state from a prior (crashed) run.
mkdir -p "$WORK/.ai/milestones/done"
touch "$WORK/.ai/milestones/done/milestone-1.md" "$WORK/.ai/milestones/done/milestone-2.md"
echo 3 > "$WORK/.ai/milestones/fix_attempts"; echo 1 > "$WORK/.ai/milestones/verify_fail_attempts"
echo old > "$WORK/.ai/milestones/current.md"
echo TestOld > "$WORK/.ai/milestones/known_failures"; echo TestOld > "$WORK/.ai/milestones/known_failures.snapshot"
echo G404 > "$WORK/.ai/milestones/known_lint_failures"; echo G404 > "$WORK/.ai/milestones/known_lint_failures.snapshot"
echo 1 > "$WORK/.ai/build/review_fix_attempts"; echo abc > "$WORK/.ai/build/milestone-start-sha"
echo x > "$WORK/.ai/build/scoped-milestones.md"; echo x > "$WORK/.ai/build/declared-files.list"; echo x > "$WORK/.ai/build/review-diff.md"; echo x > "$WORK/.ai/build/review-claude.md"
touch "$WORK/.ai/build/allow-dirty" "$WORK/.ai/build/no-tests-ok"
run
check "fresh exit 0"                      "0" "$RC"
for f in .ai/milestones/done .ai/milestones/fix_attempts .ai/milestones/verify_fail_attempts .ai/milestones/current.md \
         .ai/milestones/known_failures .ai/milestones/known_failures.snapshot .ai/milestones/known_lint_failures \
         .ai/milestones/known_lint_failures.snapshot .ai/build/review_fix_attempts .ai/build/milestone-start-sha \
         .ai/build/scoped-milestones.md .ai/build/declared-files.list .ai/build/review-diff.md .ai/build/review-claude.md; do
  check "#640 B1 reset $f" "gone" "$(exists "$f")"
done
check "#640 B1 .ai/milestones recreated"  "present" "$(exists .ai/milestones)"
check "operator stamp allow-dirty kept"   "present" "$(exists .ai/build/allow-dirty)"
check "operator stamp no-tests-ok kept"   "present" "$(exists .ai/build/no-tests-ok)"
check "marker last"                       "setup-ready" "$(last)"
check "line count reported"               "yes" "$(has '3 lines')"
for f in .ai/build/ci-probe.sh .ai/build/verify.sh .ai/build/iface-reachability-rubric.md .ai/build/build-context.md .ai/build/run-base-sha; do
  check "wrote $f" "present" "$(exists "$f")"
done
# The runtime copies are the lib/ sidecars byte-for-byte (their behaviour is
# lib/verify_test.sh's and lib/ci-probe_test.sh's).
for f in ci-probe.sh verify.sh iface-reachability-rubric.md; do
  check "$f identical to lib/" "yes" "$(cmp -s "$LIB_DIR/$f" "$WORK/.ai/build/$f" && echo yes || echo no)"
done
check "run-base-sha empty (no repo)"      "" "$(cat "$WORK/.ai/build/run-base-sha")"
check "gitignore has .ai/"                "1" "$(grep -cx '.ai/' "$WORK/.gitignore")"
check "#318 turn_overrides cleared"       "gone" "$(exists .tracker/turn_overrides)"
check "#264 forge counter cleared"        "gone" "$(exists .ai/build/spec_forge_attempts)"
check "#264 SPEC.original cleared"        "gone" "$(exists .ai/decisions/SPEC.original.md)"
check "#264 forge log cleared"            "gone" "$(exists .ai/decisions/spec-forge-log.md)"
check "other decisions kept"              "keep" "$(cat "$WORK/.ai/decisions/milestones.md")"
check "build-context header"              "# Build Context (machine-written — do not edit by hand)" "$(head -1 "$WORK/.ai/build/build-context.md")"
check "build-context no-commits label"    "yes" "$(grep -q 'as of Setup (no commits)' "$WORK/.ai/build/build-context.md" && echo yes || echo no)"
check "build-context ends at Milestones"  "## Milestones landed" "$(tail -1 "$WORK/.ai/build/build-context.md")"
# Idempotent: a second run is clean (the .gitignore dedupe/sort details are
# lib/gitignore_test.sh's).
run
check "rerun exit 0"                      "0" "$RC"

# ── D. #553 staged input adoption overrides a repo SPEC.md; and creates one
#      when absent.
mkdir -p "$WORK/.tracker/inputs"; printf 'staged spec\n' > "$WORK/.tracker/inputs/spec"
run
check "staged spec adopted (overwrite)"   "staged spec" "$(cat "$WORK/SPEC.md")"
check "staged copy exit 0"                "0" "$RC"
rm -f "$WORK/SPEC.md"
run
check "staged spec adopted (absent)"      "staged spec" "$(cat "$WORK/SPEC.md")"
check "staged file itself untouched"      "staged spec" "$(cat "$WORK/.tracker/inputs/spec")"

# ── B/H. Git repo: .tracker/ exclusion, pre-#351 untracking, run-base-sha.
reset
printf 'spec\n' > "$WORK/SPEC.md"
G -c init.defaultBranch=main init -q
mkdir -p "$WORK/.tracker/runs/r1"; echo meta > "$WORK/.tracker/runs/r1/checkpoint.json"
echo base > "$WORK/README.md"
G add -A; G commit -q -m base           # pre-#351 polluted history: .tracker/ committed
HEAD_SHA="$(G rev-parse HEAD)"
run
check "git exit 0"                        "0" "$RC"
check "run-base-sha = HEAD"               "$HEAD_SHA" "$(cat "$WORK/.ai/build/run-base-sha")"
check "exclude has .tracker/"             "1" "$(grep -cx '.tracker/' "$WORK/.git/info/exclude")"
check ".tracker untracked from index"     "" "$(G ls-files -- .tracker)"
check "build-context has short sha"       "yes" "$(grep -q "as of Setup ($(G rev-parse --short HEAD))" "$WORK/.ai/build/build-context.md" && echo yes || echo no)"
run
check "rerun with nothing to untrack ok"  "0" "$RC"
# Commitless repo: run-base-sha is genuinely empty (never the literal HEAD).
reset
printf 'spec\n' > "$WORK/SPEC.md"
G -c init.defaultBranch=main init -q
run
check "commitless exit 0"                 "0" "$RC"
check "commitless run-base-sha empty"     "" "$(cat "$WORK/.ai/build/run-base-sha")"

# ── L. #640 C5 dirty-tree preflight (git repo).
reset
printf 'spec\n' > "$WORK/SPEC.md"
G -c init.defaultBranch=main init -q
echo base > "$WORK/README.md"; G add -A; G commit -q -m base
# Clean tree (only the ignored categories present): proceeds.
mkdir -p "$WORK/.tracker/inputs" "$WORK/.ai/decisions"; echo x > "$WORK/.tracker/inputs/spec"; echo x > "$WORK/.ai/decisions/old.md"
cp "$WORK/SPEC.md" "$WORK/.tracker/inputs/spec"
echo 'workflow X' > "$WORK/build_product.dip"
printf 'spec\n' > "$WORK/SPEC.md"
run
check "C5 clean (ignored categories only) exit 0" "0" "$RC"
check "C5 clean marker"                   "setup-ready" "$(last)"
# Modified tracked file + untracked .env: refuse, list both, no marker.
echo changed >> "$WORK/README.md"; echo 'secret=abc' > "$WORK/.env"
GI_BEFORE="$(cat "$WORK/.gitignore")"; rm -rf "$WORK/.ai/build"
run
check "C5 dirty exit 1"                   "1" "$RC"
check "C5 dirty headline"                 "yes" "$(has 'ERROR: the working tree has uncommitted changes or untracked files:')"
check "C5 dirty lists README"             "yes" "$(has ' M README.md')"
check "C5 dirty lists .env"               "yes" "$(has '?? .env')"
check "C5 dirty does not list .ai"        "no"  "$(has '.ai/decisions')"
check "C5 dirty does not list .dip"       "no"  "$(has 'build_product.dip')"
check "C5 dirty opt-out hint"             "yes" "$(has 'touch .ai/build/allow-dirty')"
check "C5 dirty no marker"                "no"  "$(has 'setup-ready')"
check "C5 dirty nothing scaffolded"       "gone" "$(exists .ai/build)"
check "C5 dirty .gitignore untouched"     "$GI_BEFORE" "$(cat "$WORK/.gitignore")"
# Opt-out stamp: proceeds despite the dirty tree.
mkdir -p "$WORK/.ai/build"; touch "$WORK/.ai/build/allow-dirty"
run
check "C5 allow-dirty exit 0"             "0" "$RC"
check "C5 allow-dirty marker"             "setup-ready" "$(last)"
check "C5 allow-dirty stamp survives"     "present" "$(exists .ai/build/allow-dirty)"
# A modified .gitignore alone (Setup re-seeds it anyway) is not dirty.
rm -f "$WORK/.ai/build/allow-dirty" "$WORK/.env"; G checkout -q -- README.md
echo '*.log' >> "$WORK/.gitignore"
run
check "C5 .gitignore-only exit 0"         "0" "$RC"

if [ "$fail" = 0 ]; then echo "ALL PASS"; else echo "SOME FAILED"; exit 1; fi
