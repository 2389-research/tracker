#!/usr/bin/env bash
# ABOUTME: Fixture tests for the Adversarial Review deterministic FP gate (#622).
# ABOUTME: Proves ungrounded DISAGREE_CONCERN findings are demoted while grounded ones survive.
set -uo pipefail
DIR="$(cd "$(dirname "$0")" && pwd)"
GATE="$DIR/rank_filter.sh"
fail=0
run() { printf '%s' "$1" | bash "$GATE" 2>/tmp/rf_err; }
check() { # name expected actual
  if [ "$2" = "$3" ]; then echo "ok: $1"; else echo "FAIL: $1 — want '$2' got '$3'"; fail=1; fi
}

# 1. Confirmed (two AGREE) -> kept.
IN='{"findings":[{"id":"F1","severity":"high","claim":"null deref","verdicts":[{"critic":"opus","verdict":"AGREE"},{"critic":"gpt","verdict":"AGREE"}]}]}'
OUT="$(run "$IN")"
check "confirmed finding kept"      "1" "$(echo "$OUT" | jq '.summary.kept')"
check "confirmed keeps F1"          "F1" "$(echo "$OUT" | jq -r '.kept[0].id')"

# 2. Refuted (DISAGREE_EVIDENCE) -> dropped.
IN='{"findings":[{"id":"F2","severity":"medium","claim":"unhandled error","verdicts":[{"critic":"opus","verdict":"DISAGREE_EVIDENCE","evidence":"io.go:15 wraps it"}]}]}'
OUT="$(run "$IN")"
check "evidence-refuted dropped"    "0" "$(echo "$OUT" | jq '.summary.kept')"
check "refuted status"              "refuted" "$(echo "$OUT" | jq -r '.dropped[0].status')"

# 3. THE FP CASE: ungrounded DISAGREE_CONCERN only -> demoted/dropped.
IN='{"findings":[{"id":"F3","severity":"low","claim":"might be slow","verdicts":[{"critic":"opus","verdict":"DISAGREE_CONCERN","note":"no proof"}]}]}'
OUT="$(run "$IN")"
check "ungrounded concern dropped"  "0" "$(echo "$OUT" | jq '.summary.kept')"
check "ungrounded status"           "ungrounded" "$(echo "$OUT" | jq -r '.dropped[0].status')"
check "counted as ungrounded"       "1" "$(echo "$OUT" | jq '.summary.ungrounded_dropped')"

# 4. Uncontested (no verdicts) -> kept.
IN='{"findings":[{"id":"F4","severity":"high","claim":"race","verdicts":[]}]}'
check "uncontested kept"            "1" "$(run "$IN" | jq '.summary.kept')"

# 5. AGREE beats a co-occurring concern -> confirmed/kept.
IN='{"findings":[{"id":"F5","severity":"high","claim":"leak","verdicts":[{"critic":"opus","verdict":"AGREE"},{"critic":"gpt","verdict":"DISAGREE_CONCERN"}]}]}'
check "agree+concern kept"          "1" "$(run "$IN" | jq '.summary.kept')"

# 6. Evidence-refutation beats agreement -> dropped (grounding wins).
IN='{"findings":[{"id":"F6","severity":"high","claim":"bug","verdicts":[{"critic":"opus","verdict":"AGREE"},{"critic":"gpt","verdict":"DISAGREE_EVIDENCE","evidence":"x.go:9 guards it"}]}]}'
OUT="$(run "$IN")"
check "evidence beats agree: dropped" "0" "$(echo "$OUT" | jq '.summary.kept')"
check "evidence beats agree: refuted" "refuted" "$(echo "$OUT" | jq -r '.dropped[0].status')"

# 7. Severity sort of kept findings: critical first.
IN='{"findings":[{"id":"L","severity":"low","claim":"a","verdicts":[{"critic":"o","verdict":"AGREE"}]},{"id":"C","severity":"critical","claim":"b","verdicts":[{"critic":"o","verdict":"AGREE"}]},{"id":"M","severity":"medium","claim":"c","verdicts":[{"critic":"o","verdict":"AGREE"}]}]}'
check "severity sorted"             "C M L" "$(run "$IN" | jq -r '[.kept[].id]|join(" ")')"

# 8. Fail loud on non-JSON.
if printf 'not json' | bash "$GATE" >/dev/null 2>&1; then echo "FAIL: non-JSON should exit nonzero"; fail=1; else echo "ok: non-JSON fails loud"; fi

# 9. Mixed batch end-to-end: 1 confirmed + 1 refuted + 1 ungrounded + 1 uncontested -> 2 kept.
IN='{"findings":[
  {"id":"A","severity":"high","claim":"x","verdicts":[{"critic":"o","verdict":"AGREE"}]},
  {"id":"B","severity":"low","claim":"y","verdicts":[{"critic":"o","verdict":"DISAGREE_EVIDENCE","evidence":"e"}]},
  {"id":"C","severity":"low","claim":"z","verdicts":[{"critic":"o","verdict":"DISAGREE_CONCERN"}]},
  {"id":"D","severity":"medium","claim":"w","verdicts":[]}]}'
OUT="$(run "$IN")"
check "mixed batch kept count"      "2" "$(echo "$OUT" | jq '.summary.kept')"
check "mixed batch kept ids"        "A D" "$(echo "$OUT" | jq -r '[.kept[].id]|join(" ")')"

[ "$fail" = 0 ] && echo "ALL PASS" || { echo "SOME FAILED"; exit 1; }
