# Review fan-out cost asymmetry (#353)

Why one parallel reviewer branch can balloon to a third of a whole run's
cost, what has already shipped to contain it, and the design for the two
remaining levers. This is a **proposal / decision record** — the safe,
low-risk primitives are in place; the remaining changes are deliberately
*not* forced blind because they either need real-run calibration or trade
adversarial coverage.

## The observation

Case study: `build_product` run `b68b532619c3` (2026-06-10, $22.90 / ~110 min).
Three parallel reviewers ran a ~95%-identical 5-point rubric over the repo:

| Reviewer | Cost | Wall | Input tokens | Cache |
|---|---|---|---|---|
| ReviewGemini | $0.17 | 42 s | small | — |
| ReviewClaude | $1.56 | 6.5 min | 18M cache-read across run | cached |
| ReviewCodex | **$7.35** | **22 min** | **2.61M** | **zero** |

ReviewCodex alone was 32% of the run and gated the parallel join for 22
minutes to reach substantially the same conclusion Gemini reached in 42
seconds.

## Why the branch balloons — the mechanism

The balloon is a *product* of three independent factors, not one bug:

1. **No prompt caching on the non-Anthropic backend.** `llm.Usage` carries
   `CacheReadTokens` / `CacheWriteTokens` (`llm/types.go`). The Anthropic
   session amortized 18M cache-read tokens across the run; the Codex/OpenAI
   session reported **zero** cache reads, so every turn re-sent the full
   accumulated context as fresh, billed input. Caching is a provider/backend
   property the engine observes but does not currently *react* to.
2. **A long turn loop multiplies the un-cached context.** ReviewCodex ran 27
   turns / 22 min. With no caching, cost is roughly `turns × context_size`,
   so a slow loop over a growing transcript is the multiplier that turned a
   large-but-bounded input into 2.61M tokens and a `context_window_warning`.
3. **Fan-out-always triples the shared rubric.** All three lanes carry the
   same 5-point rubric (`examples/build_product.dip`, `ReviewClaude` /
   `ReviewCodex` / `ReviewGemini`); lane differentiation (#233) is only a few
   lines of emphasis. The shared rubric dominates, so the panel pays ~3× for
   little marginal coverage when the finding is uncontested.

Diff-scoping (#418, `ComputeReviewDiff` → `.ai/build/review-diff.md`) and
model tiering (#419) already attacked factor 3's *input size* — each reviewer
now reads a bounded `base..worktree` diff as its PRIMARY input rather than
re-walking the whole tree, and two of three lanes run mid-tier. That lowered
the cost of fan-out-always; it did not remove factors 1 and 2, and did not
change the *shape*.

## What has already shipped (do not re-implement)

- **Per-node cost ceiling** — `max_cost_usd` (+ `no_progress_turns`) from #304.
  Engine enforces between nodes; breach → `ContextKeyNodeCostExceeded=true`.
  Accessors in `pipeline/node_config.go` (`applyMaxCostNode` / `applyMaxCostGraph`);
  `"0"` on a node disables an inherited graph default.
- **Safe-cap primitive** — `cost_exceeded_action: fail`
  (`pipeline/handlers/codergen.go` `buildNodeCostExceededOutcome`). Default is
  `retry`, which *re-runs* the expensive un-cached node (up to the retry
  budget → ~3× the cap) — worse than uncapped. `fail` routes the node's fail
  edges immediately with no retry multiplier. In `build_product` the
  reviewers' fail path already routes to `EscalateReview` (a human gate), so a
  capped-then-failed reviewer escalates cleanly.
- **Make it visible** — `tracker diagnose` `cost_asymmetry` suggestion
  (`tracker_diagnose_cost.go`). Scans cumulative `cost_updated`
  `provider_totals`; fires only when the run is non-trivial (≥$1,
  `costDomMinShare = 0.35`, `costDomMinInputTokens = 200_000`), a real fan-out
  (≥2 paying providers), and the top provider is a zero-cache dominator. So the
  shape is flagged at diagnose time instead of at the bill.

## Remaining lever 1 — set the reviewer cap value (calibration-blocked)

The *primitive* is safe; the *value* is not pickable blind. The case study
shows legitimate reviewers at $0.17–$1.56 and the runaway at $7.35, but there
is no cost **distribution across builds**, so any hard-coded cap risks
false-escalating a legitimately large-diff review. Setting it blind is exactly
the change this investigation refuses to force.

Recommended config, value to be calibrated against a batch of real runs:

```
agent ReviewCodex
  max_cost_usd: <above legitimate reviewer cost, below the runaway>
  cost_exceeded_action: fail   # → EscalateReview, no retry multiplier
```

Calibration path (no code change needed — pure `.dip` authoring once the number
is known):

1. Collect `cost_updated` `provider_totals` per reviewer across ≥10 real
   `build_product` runs (the diagnose detector already surfaces the peak).
2. Take the per-reviewer cost distribution; set the cap at roughly p95 of
   legitimate cost, comfortably below the runaway tail.
3. Apply `max_cost_usd` + `cost_exceeded_action: fail` to the un-cached lane(s)
   first (ReviewCodex is the demonstrated risk), leaving the Anthropic lane
   uncapped or higher (its caching makes a runaway far less likely).
4. Verify a capped-then-failed reviewer routes `EscalateReview` and does not
   retry-loop (`pipeline/handlers/codergen_guards_test.go`
   `TestCodergenCostExceededActionFailRoutesToFail`).

## Remaining lever 2 — per-backend caching awareness (larger, ties #352)

Today the engine observes `Usage.CacheReadTokens` but does not adapt when a
backend reports zero caching. Proposal: when a session's backend reports no
cache reads, the engine compensates on the *next* turn/node — shrink injected
context and cap turns lower — so an un-cached lane cannot silently re-send a
growing transcript 27 times.

This is deliberately **not** implemented here because it touches the
agent context-injection / turn-loop path (fragile, shared by every node) and
overlaps #352's context-shrinking work. It should land there, spec-first, with
the caching signal (a zero `Usage.CacheReadTokens` in `llm/types.go`) as the
input, not as a bolt-on to the review handler. A cheaper interim mitigation that needs
no engine change: pin a lower `max_turns` on the un-cached reviewer lane in the
`.dip` (bounds factor 2 directly), calibrated the same way as the cost cap.

## Remaining lever 3 — fan-out reshape (larger, trades #233 coverage)

Escalation shape: run **one** reviewer first; fan out the full panel only on
FAIL, large diff, or disputed findings. This preserves the adversarial value
#233 built in while paying for the panel only when it earns its cost. It is a
larger change to the parallel/fan-in region — a fragile path (branches are
dispatched from the `parallel_targets` attr, not edges; see
`docs/architecture/handlers/parallel-fan-in.md`) — and it trades steady-state
adversarial coverage, so it wants a real A/B against the current always-on
panel before adoption. Dedup opportunity alongside it: let the reviewers own
reachability/test-quality and scope `FinalSpecCheck` to spec-literal compliance
(or vice versa) to stop paying for a fourth overlapping rubric pass.

## Recommendation

The visibility + safe-cap primitives are the right stopping point for a
low-risk change. The next concrete step is **lever 1** — pure `.dip` authoring
once a maintainer has the per-reviewer cost distribution from a batch of runs.
Levers 2 and 3 are engine/shape changes that should each go through the
spec-first path (2 with #352, 3 with a measured A/B) rather than a blind edit
to the context loop or the parallel dispatcher.

Related: #304 (graph budgets), #303 (guard path), #313 (parallel-branch status),
#233 (reviewer differentiation), #352 (context shrinking), #418 (diff-scope),
#419 (model tiering), #308 (epic).
