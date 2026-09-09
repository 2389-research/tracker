# ABOUTME: Convergence gate for the Adversarial Review re-review loop (#623).
# ABOUTME: Runs as the AdjudicateGate tool node after every critic pass.
#
# Reads : .ai/review/candidates.json   (candidate findings with ids, from MergeFindings)
#         .ai/review/critic.json   (typed verdicts, from the Critic agent)
#         ${params.max_critique_rounds} (round cap; injected at subgraph bind)
# Writes: .ai/review/annotated.json (findings joined with their verdicts —
#                                     the rank_filter input shape)
#         .ai/review/round          (1-based critique round counter)
# Emits : final-line marker `converged` | `contested`
#
# Converged when every finding is either unchallenged or AGREE-backed, or when
# the round cap is reached (forced convergence — the verdict is then decided by
# the deterministic FP gate on the current best set). Forced convergence keeps
# the loop strictly below the engine's max_restarts safety net, so an
# adversarially-stuck critic degrades to a verdict, never to a failed run.
#
# Fail-closed: a critic verdict that omits a finding or uses an out-of-enum
# verdict fails this node (the agent's max_retries gives the critic a retry);
# an uncovered finding must not slip through as "uncontested".
set -euo pipefail

merged=.ai/review/candidates.json
critic=.ai/review/critic.json
for f in "$merged" "$critic"; do
  if [ ! -f "$f" ]; then
    echo "adjudicate: missing $f" >&2
    exit 1
  fi
  if ! jq -e . "$f" >/dev/null 2>&1; then
    echo "adjudicate: $f is not valid JSON" >&2
    exit 1
  fi
done

# Round cap: integer 1..5 (5 = the default and the engine-budget ceiling).
CAP=5
raw="${params.max_critique_rounds}"
case "$raw" in
  ''|*[!0-9]*)
    echo "adjudicate: max_critique_rounds '${raw}' is not a positive integer; using default 5" >&2
    ;;
  *)
    if [ "$raw" -ge 1 ] && [ "$raw" -le 5 ]; then CAP="$raw"; else
      echo "adjudicate: max_critique_rounds '${raw}' out of range 1..5; using default 5" >&2
    fi
    ;;
esac

round_file=.ai/review/round
round=$(cat "$round_file" 2>/dev/null || echo 0)
round=$((round + 1))
echo "$round" > "$round_file"

# Join verdicts onto findings (rank_filter input shape) and enforce
# fail-closed coverage + verdict-enum checks.
if ! jq -en \
  --slurpfile m "$merged" \
  --slurpfile c "$critic" '
  ($c[0].verdicts // []) as $vs
  | ($vs | map(.verdict // "") | map(ascii_upcase)
       | all(. as $v | ["AGREE", "DISAGREE_EVIDENCE", "DISAGREE_CONCERN"] | index($v) != null))
      as $enum_ok
  | ($m[0].findings | map(. as $f | select(
      ($vs | map(.finding_id // "") | index($f.id)) == null
    )) | length) as $uncovered
  | if ($enum_ok | not)
    then error("adjudicate: critic.json contains a verdict outside {AGREE, DISAGREE_EVIDENCE, DISAGREE_CONCERN}")
    elif $uncovered > 0
    then error("adjudicate: critic.json omits verdicts for \($uncovered) finding(s): \($m[0].findings | map(. as $f | select(($vs | map(.finding_id // "") | index($f.id)) == null) | .id) | join(", "))")
    else
      { findings: ($m[0].findings | map(. as $f | . + {
          verdicts: [$vs[] | select(.finding_id == $f.id) | {
            verdict: (.verdict | ascii_upcase),
            evidence: (.evidence // "")
          }]
        })) }
    end
' > .ai/review/annotated.json 2> .ai/review/adjudicate.err; then
  cat .ai/review/adjudicate.err >&2
  exit 1
fi

disputed=$(jq '[.findings[] | select(.verdicts | any(.verdict | startswith("DISAGREE")))] | length' .ai/review/annotated.json)

if [ "$round" -ge "$CAP" ]; then
  echo "adjudicate: round $round reached the cap ($CAP); forcing convergence (verdict decided by the FP gate)" >&2
  echo "converged"
elif [ "$disputed" -gt 0 ]; then
  echo "adjudicate: $disputed disputed finding(s) after critique round $round; re-review" >&2
  echo "contested"
else
  echo "adjudicate: all findings converged after critique round $round" >&2
  echo "converged"
fi
