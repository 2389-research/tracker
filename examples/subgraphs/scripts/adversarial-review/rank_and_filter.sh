# ABOUTME: Deterministic FP gate + verdict for Adversarial Review (#623).
# ABOUTME: Runs as the RankAndFilter tool node after the critique loop converges.
#
# Reads : .ai/review/annotated.json (findings + critic verdicts, from AdjudicateGate)
#         ${params.severity_threshold} (injected at subgraph bind; low|medium|high|critical)
# Writes: .ai/review/kept.json     (kept findings, ranked critical>high>medium>low)
#         .ai/review/verdict.json  ({verdict: approve|rework, summary})
# Emits : final-line marker `verdict:approve` | `verdict:rework`
#
# The disposition rule is the #622 gate (arXiv 2608.18167) applied
# DETERMINISTICALLY — the false-positive control is a tool, not a prompt:
#   any DISAGREE_EVIDENCE -> REFUTED     -> DROP
#   else any AGREE        -> CONFIRMED   -> KEEP
#   else DISAGREE_CONCERN -> UNGROUNDED  -> DROP
#   else (no verdicts)    -> UNCONTESTED -> KEEP
# (kept in lockstep with rank_filter.sh — rank_and_filter_test.sh proves the
# two agree on the shared fixture corpus). After the disposition, findings
# below the caller's severity threshold are dropped, and the verdict is
# approve iff nothing survives.
set -euo pipefail

annotated=.ai/review/annotated.json
if [ ! -f "$annotated" ]; then
  echo "rank_and_filter: missing $annotated (did the critique loop converge?)" >&2
  exit 1
fi
if ! jq -e 'has("findings") and (.findings | type == "array")' "$annotated" >/dev/null 2>&1; then
  echo "rank_and_filter: $annotated is not an object with a .findings array" >&2
  exit 1
fi

# Severity threshold: one of low|medium|high|critical (case-insensitive).
# Invalid values fall back to the default with a loud warning — this is a soft
# knob; the hard contract (annotated.json shape) fails closed above.
THRESH="medium"
case "${params.severity_threshold}" in
  low|Low|LOW|medium|Medium|MEDIUM|high|High|HIGH|critical|Critical|CRITICAL)
    THRESH="$(echo "${params.severity_threshold}" | tr 'A-Z' 'a-z')"
    ;;
  *)
    echo "rank_and_filter: severity_threshold '${params.severity_threshold}' is not in {low,medium,high,critical}; using default medium" >&2
    ;;
esac

jq \
  --arg threshold "$THRESH" '
  def sevrank: {critical:0, high:1, medium:2, low:3, "":4}[. // ""] // 4;
  def disposition($v):
    ($v | map(.verdict)) as $verds
    | if   ($verds | any(. == "DISAGREE_EVIDENCE")) then {status:"refuted",   keep:false, reason:"critic cited contradicting code (DISAGREE_EVIDENCE)"}
      elif ($verds | any(. == "AGREE"))             then {status:"confirmed", keep:true,  reason:"grounded agreement (AGREE)"}
      elif ($verds | any(. == "DISAGREE_CONCERN"))  then {status:"ungrounded",keep:false, reason:"doubt without code grounding (DISAGREE_CONCERN only)"}
      else {status:"uncontested", keep:true, reason:"no critic disputed it"}
      end;
  ($threshold | sevrank) as $t
  | .findings
  | map(. + {"_disp": disposition(.verdicts // [])})
  | map(. + {"_below_threshold": (((.severity // "") | sevrank) > $t)})
  | (map(select(._disp.keep and (._below_threshold | not)))
       | sort_by((.severity // "") | sevrank)
       | map(del(._disp, ._below_threshold))) as $kept
  | (map(. as $f | select(($f._disp.keep | not) or $f._below_threshold))
       | map({id, severity, claim,
              status: (if (._disp.keep | not) then ._disp.status else "below_threshold" end),
              reason: (if (._disp.keep | not) then ._disp.reason else "below severity threshold (\($threshold))" end)})) as $dropped
  | {
      kept: $kept,
      dropped: $dropped,
      summary: {
        total: (.|length),
        kept: ($kept|length),
        refuted: ($dropped | map(select(.status=="refuted")) | length),
        ungrounded_dropped: ($dropped | map(select(.status=="ungrounded")) | length),
        below_threshold: ($dropped | map(select(.status=="below_threshold")) | length),
        threshold: $threshold
      }
    }
' "$annotated" > .ai/review/kept.json

kept_count=$(jq '.summary.kept' .ai/review/kept.json)
if [ "$kept_count" -gt 0 ]; then
  VERDICT="rework"
else
  VERDICT="approve"
fi
jq --arg verdict "$VERDICT" '. + {verdict: $verdict}' .ai/review/kept.json > .ai/review/verdict.json

echo "rank_and_filter: $kept_count finding(s) kept after disposition + threshold" >&2
echo "verdict:$VERDICT"
