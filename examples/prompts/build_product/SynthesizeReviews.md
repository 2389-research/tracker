STATUS contract — emit `STATUS:fail` as the FIRST line of your
response, before any other text. Then synthesize the reviews below.
Only at the very end, if Required fixes is empty, emit a final
`STATUS:success` line — alone on its line, outside any code
fence — to override the early fail. The workflow's `auto_status`
parser is last-line-wins; if your response is truncated for any
reason the early `STATUS:fail` remains and the gate fails closed.
The STATUS value must be exactly `fail` or `success`, with no
trailing prose on that line. Never emit STATUS:retry (this node
has no retry route).

Read all three reviews:
- .ai/build/review-claude.md (general)
- .ai/build/review-codex.md (quality)
- .ai/build/review-gemini.md (adversarial)

WEIGHTING RULE (weight by evidence, not vote
count): a finding with concrete evidence (grep output, file:line,
snippet, test code) is stronger than two PASSes without evidence.
When one reviewer flags a contract-level FAIL with grep / file:line
evidence and the other two say PASS without addressing that
specific literal or assertion, the FAIL wins. An earlier version of this prompt
treated reviewer findings as votes — a 2-vote PASS could drown a
1-vote FAIL with concrete evidence, and on the offending run the
synthesis missed ~33 of the 38 real audit findings because two
reviewers green-lit a spec section without grep-checking it.

Synthesize into .ai/decisions/review-synthesis.md:
## Consensus findings (all three reviewers agree)
## Evidence-backed findings (at least one reviewer with concrete
   evidence; reviewers without evidence on this specific point
   do NOT outvote the reviewer with evidence — list these BEFORE
   the disputed-without-evidence section)
## Disputed without evidence (reviewers disagree but no one
   showed grep/file:line evidence — judgment calls)
## Required fixes (things that MUST change before shipping —
   include every contract-level finding with concrete evidence,
   even if only one reviewer flagged it)
## Suggested improvements (nice-to-have, not blocking)

STATUS rule: if Required fixes is non-empty, leave the early
STATUS:fail standing. A single reviewer's evidence-backed
contract-level FAIL is enough to keep it fail — do not require >=2
reviewers to corroborate. If all reviewers pass or only suggest
improvements: emit the final STATUS:success line.