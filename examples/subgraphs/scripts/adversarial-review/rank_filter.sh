#!/usr/bin/env bash
# ABOUTME: Deterministic false-positive gate for Adversarial Review (#622).
# ABOUTME: Applies the paper's typed-verdict rule so ungrounded concerns cannot survive.
#
# Reads a findings-with-critic-verdicts JSON on stdin (or $1) and applies the
# Adversarial Review disposition rule (arXiv 2608.18167) DETERMINISTICALLY — the
# false-positive control is a tool, not a prompt:
#
#   per finding, given its critics' typed verdicts:
#     - any DISAGREE_EVIDENCE  -> REFUTED   (a critic cited contradicting code) -> DROP
#     - else any AGREE         -> CONFIRMED (grounded agreement)                 -> KEEP
#     - else any DISAGREE_CONCERN (no AGREE, no evidence either way)
#                              -> UNGROUNDED (doubt without grounding, the FP)   -> DROP
#     - else (no verdicts)     -> UNCONTESTED (nobody disputed it)               -> KEEP
#
# Output JSON: { kept:[...sorted by severity], dropped:[{id,status,reason}], summary:{...} }.
# Kept findings are sorted critical>high>medium>low. Fails loud on unparseable input
# (never silently returns empty — CLAUDE.md).
set -euo pipefail

SRC="${1:-/dev/stdin}"
INPUT="$(cat "$SRC")"

# Fail loud on non-JSON / missing findings array (do not swallow — #622 / CLAUDE.md).
if ! printf '%s' "$INPUT" | jq -e 'has("findings") and (.findings|type=="array")' >/dev/null 2>&1; then
  echo "rank_filter: input is not an object with a .findings array" >&2
  exit 1
fi

printf '%s' "$INPUT" | jq '
  def sevrank: {critical:0, high:1, medium:2, low:3, "":4}[. // ""] // 4;
  def disposition($v):
    ($v | map(.verdict)) as $verds
    | if   ($verds | any(. == "DISAGREE_EVIDENCE")) then {status:"refuted",   keep:false, reason:"critic cited contradicting code (DISAGREE_EVIDENCE)"}
      elif ($verds | any(. == "AGREE"))             then {status:"confirmed", keep:true,  reason:"grounded agreement (AGREE)"}
      elif ($verds | any(. == "DISAGREE_CONCERN"))  then {status:"ungrounded",keep:false, reason:"doubt without code grounding (DISAGREE_CONCERN only)"}
      else {status:"uncontested", keep:true, reason:"no critic disputed it"}
      end;
  .findings
  | map(. + {"_disp": disposition(.verdicts // [])})
  | (map(select(._disp.keep))
       | sort_by((.severity // "") | sevrank)
       | map(del(._disp) + {})) as $kept
  | (map(select(._disp.keep | not))
       | map({id, severity, claim, status: ._disp.status, reason: ._disp.reason})) as $dropped
  | {
      kept: $kept,
      dropped: $dropped,
      summary: {
        total: (.|length),
        kept: ($kept|length),
        refuted: ($dropped | map(select(.status=="refuted")) | length),
        ungrounded_dropped: ($dropped | map(select(.status=="ungrounded")) | length)
      }
    }
'
