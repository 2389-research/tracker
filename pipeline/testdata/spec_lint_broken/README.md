# SpecLint manual verification fixture (issue #301)

`SPEC.md` in this directory is deliberately broken — one defect per
SpecLint rule. The Go tests (`pipeline/spec_lint_preflight_test.go`)
pin the graph structure and routing only; the LLM's judgment (does it
actually catch these defects?) is non-deterministic and cannot be a
`go test`. Verify it manually after any change to the SpecLint prompt:

## Path enumeration (deterministic, no LLM)

```sh
dippin simulate -all-paths examples/build_product.dip
dippin simulate -all-paths examples/build_product_with_superspec.dip
```

Both must enumerate a `SpecLint -> EscalateReview` (or
`-> EscalateToHuman`) fail path and a success path toward
ReadSpec/AnalyzeSpec, and every path must terminate.

## Judgment check (LLM, manual)

```sh
repo=$(mktemp -d) && cd "$repo" && git init -q
cp <tracker>/pipeline/testdata/spec_lint_broken/SPEC.md .
tracker run build_product --auto-approve
```

Expected: the run stops at the EscalateReview gate without ever
reaching Decompose, and `.ai/decisions/spec-quality.md` contains:

- **Critical findings**: rule (a) — dangling `PLAN.md` + missing
  "Shared Contracts" section; rule (b) — "max 2 retries" vs "maximum
  of 2 attempts total"; rule (c) — Idempotency-Key derived from
  `review_id + commit_sha` but `Review(ctx, model, envelope)` receives
  neither.
- **Warnings**: rule (d) — "fast and robust under load"; rule (e) —
  `BuildEnvelope` example dropping the `error` return.
- **Mandated tests**: the synthetic-429 test and the
  cancel-within-100ms test, each with its SPEC.md source.

Counter-check: run the same command against a repo with a coherent
SPEC.md — the run must pass straight through SpecLint into ReadSpec
with `## Critical findings` = none.
