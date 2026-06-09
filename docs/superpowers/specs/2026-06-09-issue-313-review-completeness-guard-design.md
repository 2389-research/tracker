# Issue #313 — Reviewer-completeness guard for `build_product.dip`

**Status:** approved-by-delegation (owner asked the agent to decide, verified via pal consensus)
**Date:** 2026-06-09
**Issue:** #313 (`engine: parallel branches bypass strict-failure routing + aggregateStatus masks single-branch failure`) — bug / P1: high / area/engine

## Problem

`ParallelHandler.aggregateStatus` (`pipeline/handlers/parallel.go:324`) and `FanInHandler`
(`pipeline/handlers/fanin.go`) are **success-if-any**: the parallel node reports
`OutcomeSuccess` if at least one branch succeeded. In `examples/build_product.dip`
the three cross-reviewers (`ReviewClaude`, `ReviewCodex`, `ReviewGemini`) run as
parallel branches and each writes a report to `.ai/build/review-{claude,codex,gemini}.md`.
If the adversarial `ReviewGemini` exhausts `max_turns` and fails while the other two
succeed, `ReviewParallel` aggregates to success and flows `ReviewJoin → SynthesizeReviews`
with Gemini's review **silently missing**. `SynthesizeReviews` has no missing-file guard,
so a partial review set is synthesized as if complete — the exact "never silently swallow
errors" violation the project forbids, and it defeats the cross-review safety net of #233/#308.

## The blocker that shapes this design

Issue #313's preferred fix is engine-level (Go): make the fan-in aggregation policy
configurable (e.g. `all` / `quorum` / required-branch) with the default kept at the
current `any` for back-compat, then have `build_product.dip` opt into a strict policy.

**That opt-in cannot be expressed in dippin-lang v0.35.0** (the pinned parser tracker
runs at load time). Verified by reading `parser/parse_nodes.go` **and** empirically with
`dippin validate`:

- `parallel P -> A, B` (inline) does `expect(TokenNewline)` immediately — a following
  `params:`/attribute line is a fatal `unexpected top-level identifier`.
- Block-form `parallel` only accepts `branch:` sub-declarations (model/provider/
  fidelity/tool_access/writable_paths). No node-level fields.
- `fan_in J <- A, B` has no block body at all; any trailing field is a fatal parse error.
- `ir.ParallelConfig` / `ir.FanInConfig` have **no `Params` map** (unlike `AgentConfig`/
  `SubgraphConfig`), and the graph-level `defaults` block is a fixed schema.

So a per-node `fan_in_policy` attribute cannot reach the engine from `.dip` without a
**dippin-lang grammar change**, which is a separate repo (must not be edited or
`go install`-ed here; cross-repo features are filed as issues and shipped via a future
pinned version). Shipping engine-only policy code now would be unreachable-from-production
dead code.

**Decision (verified via pal consensus, gpt-5.2, 8/10):** fix the `build_product` symptom
with a workflow-level guard now; **defer** the engine policy (Defect 1) and per-branch
fallback threading (Defect 2) to follow-up work gated on the dippin-lang grammar; keep
#313 open and file the dippin-lang grammar issue.

## Design — all changes in `examples/build_product.dip` (no engine code)

### Node 1: `CheckReviewsComplete` (new `tool` node, after `ReviewJoin`)

Fails (`exit 1`) unless **all three** review files exist and are non-empty; prints which
are missing for `tracker diagnose` visibility.

```
tool CheckReviewsComplete
  label: "Verify All Reviews Present"
  timeout: 5s
  command:
    set -eu
    # #313 defense-in-depth: parallel fan-in is success-if-any, so a single
    # reviewer that exhausts max_turns and never writes its report is masked.
    # Synthesis MUST NOT run on a partial review set — fail loudly and escalate.
    missing=""
    for f in review-claude.md review-codex.md review-gemini.md; do
      [ -s ".ai/build/$f" ] || missing="$missing $f"
    done
    if [ -n "$missing" ]; then
      printf 'review-gate FAIL: missing/empty review(s):%s — escalating instead of synthesizing a partial review set\n' "$missing"
      exit 1
    fi
    printf 'review-gate OK: all 3 reviews present\n'
```

**Why ALL-3, not 2-of-3:** the canonical #313 scenario is a *single* (adversarial)
reviewer missing. A 2-of-3 quorum would still proceed with Gemini absent — i.e. it would
not fix the stated bug. The adversarial reviewer is not optional. (Owner's initial
"2-of-3" answer was given without issue context and is overridden per the owner's
instruction to decide; confirmed correct by pal consensus.)

### Node 2: `ClearStaleReviews` (new `tool` node, before `ReviewParallel`)

`rm -f` the three review files so the capped re-review restart loop cannot count a stale
file from a prior round as "present" (a reviewer that succeeded in round 1 then fails on
re-review would otherwise be masked by its stale file).

```
tool ClearStaleReviews
  label: "Clear Stale Review Reports"
  timeout: 5s
  command:
    set -eu
    # #313: the guard at CheckReviewsComplete keys on review-file presence.
    # Clear last round's files so a reviewer that fails THIS round can't be
    # satisfied by a stale report from a prior re-review pass.
    mkdir -p .ai/build
    rm -f .ai/build/review-claude.md .ai/build/review-codex.md .ai/build/review-gemini.md
    printf 'cleared stale review reports\n'
```

### Edge changes

```
# before ReviewParallel: retarget BOTH inbound edges through ClearStaleReviews
PickNextMilestone    -> ClearStaleReviews  when ctx.tool_stdout contains all-done   # was -> ReviewParallel
CheckReviewFixBudget -> ClearStaleReviews  when ctx.outcome = success  restart: true # was -> ReviewParallel
ClearStaleReviews    -> ReviewParallel

# after ReviewJoin: insert the guard between the join and synthesis
ReviewJoin           -> CheckReviewsComplete                          # was -> SynthesizeReviews
CheckReviewsComplete -> SynthesizeReviews  when ctx.outcome = success
CheckReviewsComplete -> EscalateReview     when ctx.outcome = fail
```

`ReviewParallel -> EscalateReview when ctx.outcome = fail` (the existing all-3-fail
short-circuit from #296) is **kept** — it handles the case where the parallel itself
returns fail (all branches failed) before the fan-in is reached. The two escalation
paths are mutually exclusive (all-fail short-circuits at `ReviewParallel`; ≥1-success
reaches the guard), so there is no double-routing. `CheckReviewsComplete`'s
success/fail edges are exhaustive — no unconditional fallback to a loop target (satisfies
the edge-routing rule). `EscalateReview` is a human freeform gate that does not read
review artifacts, so clearing them does not break it.

### Comment reconciliation

Rewrite the stale masking comment at `build_product.dip` lines ~1694–1702 to describe the
guard and that the *engine-level* per-branch routing remains deferred to #313. Add a
pointer in `pipeline/build_product_failure_routing_test.go` noting the #313 split (the
existing #296 assertions — reviewer branches carry no `fallback_target`, `ReviewParallel`
has a conditional fail edge to `EscalateReview` — stay valid).

## Tests (TDD — written first, must fail against current `.dip`)

New `TestBuildProductIssue313ReviewGate` in `pipeline/build_product_failure_routing_test.go`:

- `CheckReviewsComplete` exists and `Handler == "tool"`.
- `ClearStaleReviews` exists and `Handler == "tool"`.
- `ReviewJoin -> CheckReviewsComplete` edge exists.
- `ReviewJoin` no longer routes directly to `SynthesizeReviews` (negative).
- `CheckReviewsComplete -> SynthesizeReviews when ctx.outcome = success`.
- `CheckReviewsComplete -> EscalateReview when ctx.outcome = fail`.
- `ClearStaleReviews -> ReviewParallel` edge exists.
- `PickNextMilestone` and `CheckReviewFixBudget` route to `ClearStaleReviews` (not
  directly to `ReviewParallel`); `PickNextMilestone` retains its `all-done` condition and
  `CheckReviewFixBudget` retains `restart: true`.

Existing `TestBuildProductIssue296FailureRoutes` / `...AgentNodesHaveFailureRouting` must
stay green (tool nodes are skipped by the codergen-only invariant; `ReviewParallel ->
EscalateReview` intact).

## Deferred / out of scope (with issue hygiene)

- **Defect 1 (configurable fan-in aggregation policy)** and **Defect 2 (per-branch
  `fallback_target` threading through `runBranch`)**: deferred — both need the dippin-lang
  grammar to carry a per-node policy/route to be usable. Keep #313 open; add a comment
  documenting the grammar blocker and the split. File a dippin-lang issue requesting
  node-attribute (`params:`) support on `parallel`/`fan_in`.
- **Optional additive engine observability** (emit a per-branch-status event at fan-in so
  masked failures are visible in `tracker diagnose`): noted as a future enhancement; not
  implemented here (keeps this PR surgical).
- The guard checks presence + non-empty, not content validity. A reviewer that writes a
  truncated/partial report passes the guard — acceptable residual; the masking bug
  (silently *missing* review) is fixed, and content-quality is the synthesis step's job.

## Verification gates (all must pass before PR)

- `go build ./...`
- `go test ./... -short` (esp. `pipeline/`, `pipeline/handlers/`)
- `dippin validate examples/build_product.dip`
- `dippin doctor examples/ask_and_execute.dip examples/build_product.dip examples/build_product_with_superspec.dip` — A grade
- `dippin simulate -all-paths examples/build_product.dip` — confirm the new partial-failure
  path reaches `EscalateReview` and terminates
- `CHANGELOG.md` Fixed entry in the same PR
