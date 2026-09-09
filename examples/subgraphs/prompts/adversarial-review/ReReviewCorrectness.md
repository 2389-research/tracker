# Adversarial Review — Re-review: Correctness

An adversarial critic has audited the candidate findings and disputed some of them. You are the correctness reviewer again: revisit your own findings in light of the critic's objections, and fix or withdraw what does not hold.

## Inputs (read-only)

- `.ai/review/candidates.json` — all candidate findings with ids. Your findings are those whose `perspective` array includes `"correctness"`.
- `.ai/review/critic.json` — the critic's typed verdicts (`AGREE`, `DISAGREE_EVIDENCE`, `DISAGREE_CONCERN`) with explanations, keyed by finding id.
- `.ai/review/diff.patch` — the frozen diff. The ONLY ground truth. Do not modify any file.

## For each of your findings that is disputed (`DISAGREE_EVIDENCE` or `DISAGREE_CONCERN`)

1. Read the critic's objection and re-read the cited diff code yourself.
2. Decide who is right:
   - Critic is right → **withdraw** the finding (omit it from your output).
   - Critic is wrong → **keep** the finding, and strengthen `evidence` with the exact diff line that answers the objection.
   - Partially right → **revise** the claim to what the diff actually supports, with corrected evidence.
3. You may ADD a finding if the critique directly revealed a correctness gap in the diff you previously missed — only then, and it must be diff-grounded like any other finding.
4. Do not litigate in prose: the outcome of each dispute is a kept, revised, withdrawn, or new finding. Findings that remain ungrounded will be refuted again and dropped.

## Output

Write your UPDATED correctness findings to `.ai/review/findings-correctness.json` (create/overwrite the file — this replaces your earlier version) with exactly this shape:

```json
{
  "findings": [
    {
      "severity": "high",
      "claim": "one-sentence falsifiable claim",
      "evidence": "file:line — quoted or paraphrased diff line",
      "description": "why this is wrong and what goes wrong at runtime"
    }
  ]
}
```

Include the findings you keep (revised where applicable), omit withdrawn ones, and add any new diff-grounded finding. If everything is withdrawn, write `{"findings": []}`.
