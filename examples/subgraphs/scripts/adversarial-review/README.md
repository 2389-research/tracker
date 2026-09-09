# Adversarial Review — deterministic false-positive gate (#622 spike)

Implements the false-positive-control mechanism from *Adversarial Review*
(arXiv 2608.18167) as a **deterministic tool**, not a prompt. This is the spike
half of the [Adversarial Review epic](https://github.com/2389-research/tracker/issues/625):
prove the typed-verdict FP demotion works before building the full reusable
`adversarial-review.dip` subgraph (#623).

## What it does

`rank_filter.sh` takes findings annotated with their critics' **typed verdicts**
and decides which survive, deterministically:

| Critic verdicts on a finding | Disposition | Kept? |
|---|---|---|
| any `DISAGREE_EVIDENCE` (critic cited contradicting code) | `refuted` | ✗ |
| else any `AGREE` (grounded agreement) | `confirmed` | ✓ |
| else any `DISAGREE_CONCERN` only (doubt, no code either way) | `ungrounded` | ✗ |
| else (no critic disputed it) | `uncontested` | ✓ |

The paper's key claim is that agents asked to *agree* capitulate without evidence;
the fix is to make **survival require grounding**. Encoding that as a tool (rather
than a reviewer prompt) is what makes the control non-negotiable — a model that
"forgets" the verdict constraint in a full context window (the paper's
*instruction brittleness* failure mode) cannot smuggle an ungrounded concern past
this gate.

## Input / output

Input JSON (stdin or `$1`):

```json
{ "findings": [
  { "id": "F1", "severity": "high", "claim": "null deref in parse()",
    "file": "parse.go", "line": 42,
    "verdicts": [ {"critic":"opus","verdict":"AGREE"},
                  {"critic":"gpt","verdict":"DISAGREE_EVIDENCE","evidence":"parse.go:50 nil-checks"} ] } ] }
```

Output: `{ kept:[…severity-sorted], dropped:[{id,status,reason}], summary:{total,kept,refuted,ungrounded_dropped} }`.
Fails loud (exit 1) on unparseable input — never silently returns empty.

## Files

| Script | Role |
|---|---|
| `rank_filter.sh` | The #622 reference gate: stdin/`$1` JSON → disposition JSON on stdout. Kept as the lockstep reference. |
| `rank_and_filter.sh` | The node-level gate the subgraph runs: same disposition rule + caller severity threshold + verdict; reads/writes `.ai/review/` files, emits the `verdict:approve|verdict:rework` marker. |
| `compute_diff.sh` | Freezes `${params.diff_ref}` into `.ai/review/diff.patch`; `diff_ready`/`diff_empty` marker; fail-closed on non-git/bad-range. |
| `merge_findings.sh` | Cross-perspective dedupe (case-insensitive claim) of the three reviewers' findings → `.ai/review/candidates.json` with stable `F<n>` ids. |
| `adjudicate.sh` | Joins critic verdicts onto candidates → `annotated.json`; round counter; fail-closed on uncovered ids / bad verdict enums; forces convergence at the round cap. |
| `fail_closed.sh` | on_failure sink: degrades the verdict to the conservative `rework` (a broken review never approves). |
| `rank_filter_test.sh` | #622 fixture suite. |
| `rank_and_filter_test.sh` | #623 fixture suite incl. **lockstep parity** against `rank_filter.sh` on the shared corpus. |

## How #623 wires this in (built)

`examples/subgraphs/adversarial-review.dip` is the reusable read-only subgraph:

1. `ComputeDiff` (tool) freezes the diff into `.ai/review/diff.patch`.
2. `ReviewFanOut` (parallel, `fan_in_policy: all`) runs three independent
   perspectives — correctness / security / design — each `response_format:
   json_object`, each writing `.ai/review/findings-<perspective>.json`.
3. `MergeFindings` (tool, `merge_findings.sh`) dedupes into
   `.ai/review/candidates.json` with stable `F<n>` ids (post-fan_in, single
   branch — avoids #420's parallel write-back hazard).
4. `Critic` (agent) audits every candidate with a typed verdict into
   `.ai/review/critic.json`.
5. `AdjudicateGate` (tool, `adjudicate.sh`) joins verdicts →
   `.ai/review/annotated.json` (the reference gate's input shape) and emits
   `converged`/`contested`; contested rounds re-review and loop back, bounded
   by the graph's per-target `max_restarts`.
6. `RankAndFilter` (tool, `rank_and_filter.sh`) applies THIS gate's disposition
   rule plus the caller's `severity_threshold`, writes `kept.json` +
   `verdict.json`, and emits the `verdict:approve|verdict:rework` marker.
7. `EmitFindings` (agent, exit) relays the gate's output into the subgraph's
   output contract: `writes: review_findings, review_verdict` (read from the
   caller as `ctx.review_findings` / `ctx.review_verdict`).

Caller: `examples/adversarial-review-demo.dip` (drop-in `ref:` + `params:` +
report node reading the contract).

## Test

`bash rank_filter_test.sh` — the #622 fixtures. `bash rank_and_filter_test.sh` —
threshold behavior, verdict/marker emission, fail-closed paths, and lockstep
parity with `rank_filter.sh` on the shared corpus.
