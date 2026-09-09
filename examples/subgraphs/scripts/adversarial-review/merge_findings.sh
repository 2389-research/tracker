# ABOUTME: Merges the three perspective findings files for Adversarial Review (#623).
# ABOUTME: Runs as the MergeFindings tool node after the review fan-in (and again on
# ABOUTME: each re-review loop pass, over the rewritten findings files).
#
# Reads : .ai/review/findings-correctness.json
#         .ai/review/findings-security.json
#         .ai/review/findings-design.json
#         (each: {"findings":[{"severity","claim","evidence","description"?}]})
# Writes: .ai/review/candidates.json
#         {"findings":[{"id":"F1","perspective":[...],"severity","claim","evidence","description"}]}
# Emits : final-line marker `candidates_ready` | `candidates_empty`
#
# Cross-perspective duplicates (same claim, case-insensitive) collapse into one
# finding with merged perspectives and the highest severity. Findings without a
# claim are dropped. Fails loud on any missing/invalid file (CLAUDE.md).
set -euo pipefail

for p in correctness security design; do
  f=".ai/review/findings-$p.json"
  if [ ! -f "$f" ]; then
    echo "merge_findings: missing $f — reviewer for perspective '${p}' did not write its findings file" >&2
    exit 1
  fi
  if ! jq -e '.findings | type == "array"' "$f" >/dev/null 2>&1; then
    echo "merge_findings: $f is not valid JSON with a .findings array" >&2
    exit 1
  fi
done

jq -n \
  --slurpfile c .ai/review/findings-correctness.json \
  --slurpfile s .ai/review/findings-security.json \
  --slurpfile d .ai/review/findings-design.json '
  def norm($p): {
    perspective: $p,
    severity: ((.severity // "") | tostring | ascii_downcase),
    claim:    ((.claim // "") | tostring),
    evidence: ((.evidence // "") | tostring),
    description: ((.description // "") | tostring)
  };
  def rank: {critical:4, high:3, medium:2, low:1}[.] // 0;
  [ (($c[0].findings // []) | map(norm("correctness"))),
    (($s[0].findings // []) | map(norm("security"))),
    (($d[0].findings // []) | map(norm("design"))) ]
  | add // []
  | map(select(.claim != ""))
  | map(. + {rank: (.severity | rank)})
  | group_by(.claim | ascii_downcase)
  | map(
      (max_by(.rank)) as $top
      | {
          perspective: (map(.perspective) | unique),
          severity: $top.severity,
          claim: $top.claim,
          evidence: (map(select(.evidence != "") | .evidence) | first // ""),
          description: (map(select(.description != "") | .description) | first // "")
        }
    )
  | to_entries
  | map(.value + {id: ("F" + ((.key + 1) | tostring))})
  | { findings: . }
' > .ai/review/candidates.json

count=$(jq '.findings | length' .ai/review/candidates.json)
if [ "$count" -eq 0 ]; then
  echo "merge_findings: no findings from any perspective" >&2
  echo "candidates_empty"
else
  echo "merge_findings: merged $count candidate finding(s) at .ai/review/candidates.json" >&2
  echo "candidates_ready"
fi
