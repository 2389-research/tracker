# Upgrading tracker: v0.47 → v0.56

This guide covers everything that changed across `v0.48.0`–`v0.56.0`. Most of it
is **correctness fixes that need no action** — but a few items change numbers
you may have calibrated against, and library embedders have two small source
changes. Read the [TL;DR](#tldr), do the [upgrade steps](#upgrade-steps), then
skim the sections that apply to you.

## TL;DR

- **No `.dip` workflow changes are required.** Your pipelines run unchanged.
- **Cost/budget numbers changed** (v0.50.0). Pricing was wrong before — cache
  pricing, per-provider token counts, and a 3× `run.json` overcount are all
  fixed. If you set `--max-cost` / `--max-tokens` / a `defaults:` cost ceiling,
  **re-check your thresholds against a real run** (per-run totals are now lower
  and accurate).
- **Long agent runs behave differently and better** (v0.56.0, #539): context
  compaction and the window warning were silently disabled under Anthropic
  prompt caching and are now working, so a long run that used to grow until an
  HTTP 400 now summarizes old tool output instead.
- **Do not resume an in-flight v0.47 run on a v0.56 binary.** Let running
  pipelines finish (or restart them) before upgrading — see
  [Checkpoints & resume](#checkpoints--resume).
- **Library embedders:** two small source changes — `Outcome.Tool.*` and the
  diagnostics sink. See [For library embedders](#for-library-embedders).

## Upgrade steps

**CLI (Homebrew / `go install` / binary):**

```sh
tracker update            # self-update to the latest release, or:
go install github.com/2389-research/tracker/cmd/tracker@v0.56.0
tracker version           # confirm 0.56.0
tracker doctor            # preflight: API keys, dippin, workdir
```

**Library dependency:**

```sh
go get github.com/2389-research/tracker@v0.56.0
go build ./...            # fix the two source changes below if they surface
go test ./...
```

Then **recalibrate any cost/token budgets** against one real run (see
[Cost & budgets](#cost--budgets-recalibrate)).

---

## Behavior changes (no action required, but know about them)

### Cost & budgets — recalibrate

v0.50.0 fixed a batch of cost-accounting bugs. Your dollar/token figures will
change (mostly **downward**, because the biggest bug tripled `run.json` usage):

- `run.json` reported **3× its real token usage** (#519) — fixed.
- Anthropic **cache-write priced at 0.25× instead of 1.25×** (#519) — fixed.
- **Phantom cache-write premium on OpenAI/Gemini** (#522) — removed.
- **Gemini billed thinking tokens** were dropped; **OpenAI cached input tokens**
  were dropped — both now counted (#519).
- `TokenTracker` now prices per `(provider, model)` instead of attributing all
  spend to the last-seen model (#521, v0.51.0) — mixed-model/failover runs are
  now order-independent and accurate.

**Action:** if you gate on `--max-cost` (cents), `--max-tokens`, or a `defaults:`
budget block, run one representative pipeline and reset thresholds to the new,
accurate totals. A ceiling tuned to v0.47's inflated numbers may now be far
looser (or, for previously-undercounted cache/thinking tokens, slightly tighter).

New in v0.52.0: an **`unpriced` signal** on `run.json` flags billable usage
attributed to an uncatalogued/misspelled model (which would otherwise price at
$0), so a `--max-cost` gate can't be silently bypassed.

### Context management now actually works under prompt caching (#539)

The context-window tracker counted only `InputTokens`, which excludes the
additive cache buckets. With Anthropic auto-caching (the default), a nearly-full
window reported ~1% utilization — so **auto-compaction and the context-window
warning never fired**, and long runs grew until the provider returned HTTP 400.

As of v0.56.0 the tracker counts input + cache-read + cache-write. Practical
effect: long agent nodes now **summarize old non-error tool results** when the
window fills (default `ContextCompaction: auto`), instead of crashing. If you
depended on full verbatim tool history deep in a long conversation, note that
old tool-result *content* is now replaced with a short summary (the tool can be
re-run; errors are never summarized).

### Resume correctness

Several resume bugs were fixed in v0.56.0 (they only affect a run interrupted and
restarted with `tracker -r`):

- A **passed goal gate** is no longer re-judged as unsatisfied after resume
  (could flip `success`→`fail` or re-enter escalation) (#533).
- **Child-propagated overrides** no longer double-count across resume (#535).
- A **revisited node** no longer replays a stale edge selection (#536).

### Agent turn-loop correctness

- The **no-progress detector** (`no_progress_turns`) now keys on workspace
  *edits*, not raw tool-call activity, so a tight loop that calls tools without
  advancing is caught (#531, v0.53.0).
- **Length-truncated tool calls fail closed** — a turn cut off by the token
  limit no longer dispatches half-written tool arguments (#507, v0.53.0).
- **Empty provider responses** retry cleanly instead of leaving an invalid
  message that the provider rejects (#540, v0.56.0).
- **Memoization** (`memoize: true`) is refused at non-full fidelity, so it never
  replays a stale outcome (#534, v0.56.0).

### Parallel branches

- A branch that hits **billing/quota exhaustion** now halts the run in the
  resumable `paused_billing` terminal instead of being flattened to a fail — or,
  under the default `any` fan-in policy, masked as node success (#538, v0.56.0).
- Branch-target events now carry the correct `run_id` in the activity log (#537).

### Audit / activity log

- `run_id` is now stamped on **all** handler-originated pipeline events (#448,
  v0.48.0); per-turn agent usage is written to `activity.jsonl`, not just the
  NDJSON wire.
- `tracker audit` no longer misclassifies a resumable `paused_billing` run as a
  plain failure (#449-era, v0.49.0).

---

## New features worth adopting

### Run capture — `tracker run-json` (v0.50.0)

A finished run is now fully reconstructable: the executed spec
(`workflow.dip` + `workflow.ir.json` + input files), the **verbatim provider
request bodies**, and per-call/turn/session identity land on every
`activity.jsonl` line and roll up into a `run.json` manifest (goal, terminal
status, per-node kind/attempts/outcome/turns/usage, run totals).

```sh
tracker run-json <runID>   # assemble/backfill run.json for any run, incl. a SIGKILLed one
```

Capture files are written `0600` with `O_NOFOLLOW`, and `.tracker/` is excluded
from exported bundles (v0.50.1 hardening).

### `tracker verify-tests [dir] [--race]` (v0.54.0)

A test-fidelity gate for a `VerifyMilestone`-style tool node: flags
duplicate/near-duplicate Go test bodies (exit 1), and with `--race` runs
`go test -race ./...` and fails on a detected data race. Opt-in; a no-op for
non-Go milestones.

### Numeric & regex edge-condition operators (v0.55.0, #504)

Edge `when` conditions gain numeric comparison and regex, on top of the existing
`=` / `!=` / `contains` / `startswith` / `endswith` / `in` / `&&` / `||`:

```
when ctx.count >= 5
when ctx.score < 0.8
when ctx.branch matches "^feature/"
when ctx.branch not matches "^main$"
```

Numeric (`<`, `<=`, `>`, `>=`) and `matches` operators **require surrounding
spaces** (like `==`). A malformed literal/pattern is an author error caught at
validate time; an unspaced numeric comparison (`ctx.count>=5`) fails loudly with
a "use spaces" hint rather than silently misparsing.

---

## For library embedders

Two source-level changes to expect when you bump the dependency and rebuild:

### 1. Tool-handler `Outcome` fields moved under `Outcome.Tool` (v0.55.0, #454)

If you read these on a `pipeline.Outcome` (e.g. in a custom handler or an event
consumer), update the field path:

```go
// before (≤ v0.54)
outcome.Truncations
outcome.MissingMarker
outcome.MissingRoute

// after (v0.55+)
outcome.Tool.Truncations
outcome.Tool.MissingMarker
outcome.Tool.MissingRoute
```

The zero value of `Outcome.Tool` means "not a tool node." Cross-cutting fields
(`Stats`, `ChildUsage`, `ChildOverride`) are unchanged and stay top-level.

### 2. Diagnostics route to an injectable sink (v0.48.0, #449)

Library diagnostics no longer write to the process stderr by default — they go
to a sink you control, so a host app's structured logs stay clean:

```go
tracker.SetDiagnosticLogger(mySlogLogger) // *slog.Logger; nil/default = quiet
```

### Also new for embedders

- **`Config.Capture *CaptureConfig`** (v0.51.0) — opt into the same on-disk run
  capture the CLI produces, from an embedded `Run`.
- **`GateAware` interviewer interface** (v0.48.0) — the interviewer callback can
  receive gate identity.
- **Fail-closed pre-execution tool-call guardrail hook** in the agent loop
  (v0.48.0) — reject a tool call before it runs.
- **API stability policy + exported-surface golden snapshot** (v0.49.0, #462):
  the root `tracker` package's exported surface is now guarded against accidental
  change. New supported entry points added in this range include
  `tracker.AnalyzeTestFidelity`, `tracker.DetectTestRaces`, and the run-capture /
  `run-json` helpers.

---

## Checkpoints & resume

**Recommendation: finish or restart in-flight runs across this upgrade — don't
resume a v0.47 checkpoint on a v0.56 binary.** The checkpoint schema gained
fields in this range (e.g. `node_outcomes` in v0.56.0, which the #533 goal-gate
fix relies on). New fields are `omitempty`, so an old checkpoint won't crash the
loader, but a checkpoint written by v0.47 lacks the data the newer resume-path
correctness fixes depend on — so a cross-version resume may not benefit from
them. Fresh runs on v0.56 get the full, correct behavior.

## Verifying the upgrade

```sh
tracker version                                   # 0.56.0
tracker doctor                                    # green preflight
tracker validate <your-workflow>.dip              # A grade
tracker simulate <your-workflow>.dip              # dry-run all paths
# run one real pipeline, then recheck cost/budget thresholds against run.json
tracker run-json <runID>
```

If anything regressed, `tracker diagnose <runID>` reads `status.json` +
`activity.jsonl` and surfaces tool output, errors, and actionable suggestions.
