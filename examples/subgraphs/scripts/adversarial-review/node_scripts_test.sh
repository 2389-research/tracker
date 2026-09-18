#!/usr/bin/env bash
# ABOUTME: Fixture tests for the Adversarial Review tool-node scripts that had
# ABOUTME: no suite (#646 item 1): compute_diff, merge_findings, adjudicate,
# ABOUTME: fail_closed. Runs each under `sh` (dash on Linux CI — the old
# ABOUTME: `set -o pipefail` was rc 2 there before any work) and proves the
# ABOUTME: adjudicate round counter tolerates a corrupted file (item 9).
set -uo pipefail
DIR="$(cd "$(dirname "$0")" && pwd)"
fail=0
check() { # name expected actual
  if [ "$2" = "$3" ]; then echo "ok: $1"; else echo "FAIL: $1 — want '$2' got '$3'"; fail=1; fi
}
WORK="$(mktemp -d)"
STATE="$(mktemp -d)"
trap 'rm -rf "$WORK" "$STATE"' EXIT
export HOME="$STATE" GIT_CONFIG_GLOBAL=/dev/null GIT_CONFIG_SYSTEM=/dev/null

# stage NAME [PARAM=VALUE...] — copy a node script to $STATE with the
# ${params.*} bind-time substitutions applied, print the staged path.
stage() {
  local src="$DIR/$1" out="$STATE/$1"; shift
  cp "$src" "$out"
  for kv in "$@"; do
    sed -i.bak "s|\\\${params.${kv%%=*}}|${kv#*=}|g" "$out" && rm -f "$out.bak"
  done
  printf '%s' "$out"
}
run() { OUT="$( (cd "$WORK" && ${TEST_SH:-sh} "$1") 2>"$STATE/stderr")"; RC=$?; }
last() { printf '%s' "$OUT" | tail -1; }
errs() { cat "$STATE/stderr"; }

# --- compute_diff -----------------------------------------------------------
CD="$(stage compute_diff.sh diff_ref=main..HEAD)"
# 1. Not a git worktree -> exit 1 with the reason (not a shell syntax error).
run "$CD"
check "compute_diff no repo exit 1"     "1" "$RC"
check "compute_diff no repo reason"     "yes" "$(errs | grep -q 'not a git worktree' && echo yes || echo no)"
check "compute_diff no pipefail error"  "no"  "$(errs | grep -q 'pipefail' && echo yes || echo no)"
# 2. Real range with changes -> diff_ready + frozen patch.
(cd "$WORK" && git init -q -b main && git config user.email t@t && git config user.name t \
  && echo a > a.txt && git add . && git commit -qm base && git checkout -qb feat \
  && echo b >> a.txt && git commit -qam change)
run "$CD"
check "compute_diff exit 0"             "0" "$RC"
check "compute_diff marker"             "diff_ready" "$(last)"
check "compute_diff patch frozen"       "yes" "$(grep -q '^+b' "$WORK/.ai/review/diff.patch" && echo yes || echo no)"
# 3. Empty range -> diff_empty, exit 0.
CD2="$(stage compute_diff.sh diff_ref=HEAD..HEAD)"
run "$CD2"
check "compute_diff empty marker"       "diff_empty" "$(last)"
check "compute_diff empty exit 0"       "0" "$RC"
# 4. Unresolvable range -> exit 1 with git's message.
CD3="$(stage compute_diff.sh diff_ref=nope..HEAD)"
run "$CD3"
check "compute_diff bad range exit 1"   "1" "$RC"

# --- merge_findings ---------------------------------------------------------
MF="$(stage merge_findings.sh)"
mkdir -p "$WORK/.ai/review"
# 5. A missing perspective file fails loud.
run "$MF"
check "merge missing file exit 1"       "1" "$RC"
check "merge missing file names it"     "yes" "$(errs | grep -q 'findings-correctness.json' && echo yes || echo no)"
# 6. Three files, one cross-perspective duplicate -> merged with top severity.
echo '{"findings":[{"severity":"high","claim":"Null deref","evidence":"a.go:2"}]}' > "$WORK/.ai/review/findings-correctness.json"
echo '{"findings":[{"severity":"critical","claim":"null deref","evidence":"a.go:2"},{"severity":"low","claim":"","evidence":""}]}' > "$WORK/.ai/review/findings-security.json"
echo '{"findings":[]}' > "$WORK/.ai/review/findings-design.json"
run "$MF"
check "merge exit 0"                    "0" "$RC"
check "merge marker"                    "candidates_ready" "$(last)"
check "merge dedupes"                   "1" "$(jq '.findings|length' "$WORK/.ai/review/candidates.json")"
check "merge top severity"              "critical" "$(jq -r '.findings[0].severity' "$WORK/.ai/review/candidates.json")"
check "merge perspectives"              "correctness security" "$(jq -r '.findings[0].perspective|join(" ")' "$WORK/.ai/review/candidates.json")"
# 7. All empty -> candidates_empty.
for p in correctness security design; do echo '{"findings":[]}' > "$WORK/.ai/review/findings-$p.json"; done
run "$MF"
check "merge empty marker"              "candidates_empty" "$(last)"

# --- adjudicate -------------------------------------------------------------
AJ="$(stage adjudicate.sh max_critique_rounds=3)"
echo '{"findings":[{"id":"F1","severity":"high","claim":"x"}]}' > "$WORK/.ai/review/candidates.json"
echo '{"verdicts":[{"finding_id":"F1","verdict":"DISAGREE_CONCERN"}]}' > "$WORK/.ai/review/critic.json"
# 8. Round 1 with a dispute -> contested; round file = 1.
run "$AJ"
check "adjudicate exit 0"               "0" "$RC"
check "adjudicate contested"            "contested" "$(last)"
check "adjudicate round 1"              "1" "$(cat "$WORK/.ai/review/round")"
# 9. #646 item 9: a corrupted round file reads as 0 -> round 1 again, not a
#    dash `Illegal number` abort.
printf 'abc\n' > "$WORK/.ai/review/round"
run "$AJ"
check "adjudicate garbage round exit 0" "0" "$RC"
check "adjudicate garbage round -> 1"   "1" "$(cat "$WORK/.ai/review/round")"
printf '1 2\n' > "$WORK/.ai/review/round"
run "$AJ"
check "adjudicate '1 2' round -> 1"     "1" "$(cat "$WORK/.ai/review/round")"
# 10. Cap reached -> converged even with a dispute.
printf '2\n' > "$WORK/.ai/review/round"
run "$AJ"
check "adjudicate cap converged"        "converged" "$(last)"
check "adjudicate cap reason"           "yes" "$(errs | grep -q 'reached the cap' && echo yes || echo no)"
# 11. Uncovered finding -> fail closed.
echo '{"verdicts":[]}' > "$WORK/.ai/review/critic.json"
run "$AJ"
check "adjudicate uncovered exit 1"     "1" "$RC"
check "adjudicate uncovered names F1"   "yes" "$(errs | grep -q 'omits verdicts for 1 finding(s): F1' && echo yes || echo no)"
# 12. All AGREE -> converged.
echo '{"verdicts":[{"finding_id":"F1","verdict":"agree"}]}' > "$WORK/.ai/review/critic.json"
rm -f "$WORK/.ai/review/round"
run "$AJ"
check "adjudicate agree converged"      "converged" "$(last)"
check "adjudicate annotated verdict"    "AGREE" "$(jq -r '.findings[0].verdicts[0].verdict' "$WORK/.ai/review/annotated.json")"

# --- fail_closed ------------------------------------------------------------
FC="$(stage fail_closed.sh)"
rm -rf "$WORK/.ai"
run "$FC"
check "fail_closed exit 0"              "0" "$RC"
check "fail_closed marker"              "degraded" "$(last)"
check "fail_closed verdict rework"      "rework" "$(jq -r '.verdict' "$WORK/.ai/review/verdict.json")"

if [ "$fail" = 0 ]; then echo "ALL PASS"; else echo "SOME FAILED"; exit 1; fi
