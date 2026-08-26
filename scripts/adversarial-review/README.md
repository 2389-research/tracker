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

## Test

`bash rank_filter_test.sh` — fixture suite covering confirmed / evidence-refuted /
the **ungrounded-concern demotion** / uncontested / agree-vs-concern /
evidence-beats-agreement / severity sort / fail-loud / a mixed end-to-end batch.

## How #623 wires this in

The reusable `examples/subgraphs/adversarial-review.dip` (issue #623) will:

1. Have the reviewers emit **structured findings** (`response_format: json_object`,
   stable `id`s) into `.ai/review/candidates.json`.
2. Have the critic(s) emit a typed verdict per finding id into
   `.ai/review/verdicts.json`.
3. Merge findings + verdicts and pipe them through this gate in a `tool` node:
   `rank_filter.sh .ai/review/annotated.json > .ai/review/kept.json`.
4. Feed `kept.json` to the consensus/synthesis node and expose it via `writes:`.

The measurement #623 still owes (real-diff FP reduction vs the current one-shot
cross-critique) needs labeled real diffs — this spike proves only that the gate
demotes ungrounded findings correctly and deterministically.
