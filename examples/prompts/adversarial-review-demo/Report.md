# Adversarial Review Demo — Report

The adversarial-review subgraph has finished reviewing the diff. Its output contract is in context:

- Verdict: ${ctx.review_verdict}
- Findings (ranked, evidence-backed, false-positive-filtered): ${ctx.review_findings}

Summarize the review outcome for a human reader:

1. State the verdict (approve / rework) plainly.
2. If there are findings, list each: severity, claim, and the `file:line` evidence. Keep the finding's own wording — do not soften, reword, or drop any.
3. If the verdict is approve with no findings, say the diff passed adversarial review with no surviving findings — and note that this means nothing survived the deterministic gate, not that nothing was found.

Be concise. Do not propose fixes — the caller's outer loop owns remediation.
