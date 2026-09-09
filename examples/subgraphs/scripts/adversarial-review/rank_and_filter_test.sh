#!/usr/bin/env bash
# ABOUTME: Fixture tests for the Adversarial Review node-level FP gate (#623).
# ABOUTME: rank_and_filter.sh = the #622 disposition rule + severity threshold
# ABOUTME: + verdict marker. Proves threshold behavior, verdict emission, and
# ABOUTME: LOCKSTEP with rank_filter.sh on the same fixture corpus.
set -uo pipefail
DIR="$(cd "$(dirname "$0")" && pwd)"
GATE="$DIR/rank_and_filter.sh"
REFERENCE="$DIR/rank_filter.sh"
fail=0
check() { # name expected actual
  if [ "$2" = "$3" ]; then echo "ok: $1"; else echo "FAIL: $1 — want '$2' got '$3'"; fail=1; fi
}

WORK="$(mktemp -d)"
trap 'rm -rf "$WORK"' EXIT

# run_node: $1 = annotated.json contents, $2 = severity_threshold param.
# Simulates the subgraph bind: ${params.severity_threshold} is textually
# injected into the script, then run in a scratch workdir.
run_node() {
  local input="$1" threshold="$2"
  rm -rf "$WORK/.ai" && mkdir -p "$WORK/.ai/review"
  printf '%s' "$input" > "$WORK/.ai/review/annotated.json"
  sed "s/\\\${params.severity_threshold}/$threshold/g" "$GATE" > "$WORK/rank_and_filter.sh"
  (cd "$WORK" && bash rank_and_filter.sh)
}

# 1. THE FP CASE through the node gate: ungrounded DISAGREE_CONCERN dropped,
#    verdict rework only from a grounded high finding.
IN='{"findings":[
  {"id":"F1","severity":"low","claim":"might be slow","verdicts":[{"critic":"opus","verdict":"DISAGREE_CONCERN","evidence":"no proof"}]},
  {"id":"F2","severity":"high","claim":"null deref","verdicts":[{"critic":"opus","verdict":"AGREE","evidence":"a.go:2"}]}]}'
OUT="$(run_node "$IN" medium)"
check "fp dropped, grounded kept"   "F2" "$(jq -r '[.kept[].id]|join(" ")' "$WORK/.ai/review/kept.json")"
check "ungrounded status"           "ungrounded" "$(jq -r '.dropped[]|select(.id=="F1")|.status' "$WORK/.ai/review/kept.json")"
check "verdict rework"              "rework" "$(jq -r '.verdict' "$WORK/.ai/review/verdict.json")"
check "marker line rework"          "verdict:rework" "$(echo "$OUT" | tail -1)"

# 2. Threshold is what drops the low AGREE finding: low keeps it, medium drops it.
IN='{"findings":[
  {"id":"L","severity":"low","claim":"unused var","verdicts":[{"critic":"o","verdict":"AGREE","evidence":"b.go:3"}]},
  {"id":"H","severity":"high","claim":"null deref","verdicts":[{"critic":"o","verdict":"AGREE","evidence":"a.go:2"}]}]}'
OUT="$(run_node "$IN" medium)"
check "threshold medium drops low"  "H" "$(jq -r '[.kept[].id]|join(" ")' "$WORK/.ai/review/kept.json")"
check "low marked below_threshold"  "below_threshold" "$(jq -r '.dropped[]|select(.id=="L")|.status' "$WORK/.ai/review/kept.json")"
OUT="$(run_node "$IN" low)"
check "threshold low keeps both"    "H L" "$(jq -r '[.kept[].id]|join(" ")' "$WORK/.ai/review/kept.json")"

# 3. Threshold=high: high kept, medium dropped.
IN='{"findings":[
  {"id":"H","severity":"high","claim":"bug","verdicts":[{"critic":"o","verdict":"AGREE","evidence":"a.go:2"}]},
  {"id":"M","severity":"medium","claim":"smell","verdicts":[]}]}'
OUT="$(run_node "$IN" high)"
check "threshold high keeps H"      "H" "$(jq -r '[.kept[].id]|join(" ")' "$WORK/.ai/review/kept.json")"
check "medium below high"           "below_threshold" "$(jq -r '.dropped[]|select(.id=="M")|.status' "$WORK/.ai/review/kept.json")"

# 4. Threshold=critical: only a critical survives.
IN='{"findings":[
  {"id":"C","severity":"critical","claim":"rce","verdicts":[{"critic":"o","verdict":"AGREE","evidence":"s.go:1"}]},
  {"id":"H","severity":"high","claim":"bug","verdicts":[{"critic":"o","verdict":"AGREE","evidence":"a.go:2"}]}]}'
OUT="$(run_node "$IN" critical)"
check "threshold critical"          "C" "$(jq -r '[.kept[].id]|join(" ")' "$WORK/.ai/review/kept.json")"
check "below-threshold status"      "below_threshold" "$(jq -r '.dropped[]|select(.id=="H")|.status' "$WORK/.ai/review/kept.json")"

# 5. Empty after filtering -> verdict approve + marker approve.
IN='{"findings":[{"id":"L","severity":"low","claim":"style","verdicts":[{"critic":"o","verdict":"AGREE","evidence":"f.go:9"}]}]}'
OUT="$(run_node "$IN" medium)"
check "empty kept -> approve"       "approve" "$(jq -r '.verdict' "$WORK/.ai/review/verdict.json")"
check "marker line approve"         "verdict:approve" "$(echo "$OUT" | tail -1)"
check "empty kept array"            "0" "$(jq '.kept|length' "$WORK/.ai/review/kept.json")"

# 6. Invalid threshold falls back to medium with a warning (not a crash).
OUT="$(run_node "$IN" banana 2>&1)"
check "bad threshold warns"         "yes" "$(echo "$OUT" | grep -q 'not in {low,medium,high,critical}' && echo yes || echo no)"
check "bad threshold -> medium"     "approve" "$(jq -r '.verdict' "$WORK/.ai/review/verdict.json")"

# 7. LOCKSTEP with the #622 reference gate: on a mixed batch at threshold=low
#    (which drops nothing below low), kept sets must be identical.
MIXED='{"findings":[
  {"id":"A","severity":"high","claim":"x","verdicts":[{"critic":"o","verdict":"AGREE"}]},
  {"id":"B","severity":"low","claim":"y","verdicts":[{"critic":"o","verdict":"DISAGREE_EVIDENCE","evidence":"e"}]},
  {"id":"C","severity":"low","claim":"z","verdicts":[{"critic":"o","verdict":"DISAGREE_CONCERN"}]},
  {"id":"D","severity":"medium","claim":"w","verdicts":[]}]}'
REF="$(printf '%s' "$MIXED" | bash "$REFERENCE" | jq -c '[.kept[].id]')"
OUT="$(run_node "$MIXED" low)"
check "lockstep kept ids"           "$REF" "$(jq -c '[.kept[].id]' "$WORK/.ai/review/kept.json")"
REF_ORDER="$(printf '%s' "$MIXED" | bash "$REFERENCE" | jq -c '[.kept[].id]|sort')"
check "lockstep (sort-insensitive)" "$REF_ORDER" "$(jq -c '[.kept[].id]|sort' "$WORK/.ai/review/kept.json")"

# 8. Lockstep on the full #622 corpus (the 9 rank_filter_test fixtures).
CORPUS=(
  '{"findings":[{"id":"F1","severity":"high","claim":"null deref","verdicts":[{"critic":"opus","verdict":"AGREE"},{"critic":"gpt","verdict":"AGREE"}]}]}'
  '{"findings":[{"id":"F2","severity":"medium","claim":"unhandled error","verdicts":[{"critic":"opus","verdict":"DISAGREE_EVIDENCE","evidence":"io.go:15 wraps it"}]}]}'
  '{"findings":[{"id":"F3","severity":"low","claim":"might be slow","verdicts":[{"critic":"opus","verdict":"DISAGREE_CONCERN","note":"no proof"}]}]}'
  '{"findings":[{"id":"F4","severity":"high","claim":"race","verdicts":[]}]}'
  '{"findings":[{"id":"F5","severity":"high","claim":"leak","verdicts":[{"critic":"opus","verdict":"AGREE"},{"critic":"gpt","verdict":"DISAGREE_CONCERN"}]}]}'
  '{"findings":[{"id":"F6","severity":"high","claim":"bug","verdicts":[{"critic":"opus","verdict":"AGREE"},{"critic":"gpt","verdict":"DISAGREE_EVIDENCE","evidence":"x.go:9 guards it"}]}]}'
  '{"findings":[{"id":"L","severity":"low","claim":"a","verdicts":[{"critic":"o","verdict":"AGREE"}]},{"id":"C","severity":"critical","claim":"b","verdicts":[{"critic":"o","verdict":"AGREE"}]},{"id":"M","severity":"medium","claim":"c","verdicts":[{"critic":"o","verdict":"AGREE"}]}]}'
)
for i in "${!CORPUS[@]}"; do
  REF="$(printf '%s' "${CORPUS[$i]}" | bash "$REFERENCE" | jq -c '[.kept[].id]|sort')"
  OUT="$(run_node "${CORPUS[$i]}" low)"
  GOT="$(jq -c '[.kept[].id]|sort' "$WORK/.ai/review/kept.json")"
  check "lockstep corpus #$i" "$REF" "$GOT"
done

# 9. Fail closed: missing annotated.json.
rm -rf "$WORK/.ai" && mkdir -p "$WORK/.ai/review"
if (cd "$WORK" && sed "s/\\\${params.severity_threshold}/medium/g" "$GATE" > g.sh && bash g.sh >/dev/null 2>&1); then
  echo "FAIL: missing annotated.json should exit nonzero"; fail=1
else
  echo "ok: missing annotated.json fails loud"
fi

# 10. Fail closed: non-JSON annotated.json.
echo 'not json' > "$WORK/.ai/review/annotated.json"
if (cd "$WORK" && bash g.sh >/dev/null 2>&1); then
  echo "FAIL: non-JSON annotated.json should exit nonzero"; fail=1
else
  echo "ok: non-JSON annotated.json fails loud"
fi

[ "$fail" = 0 ] && echo "ALL PASS" || { echo "SOME FAILED"; exit 1; }
