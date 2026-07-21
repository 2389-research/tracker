# Phase 3 — Close the CLI-Unification Coverage Blind Spots

**Date:** 2026-07-20
**Status:** proposed
**Ship:** 3
**Depends on:** Phase 0/2 (some tests pin their fixes)
**Related:** #478, transport-boundary test-coverage review

## Problem

The library additions (`RunManager`, `Config.Interviewer`, terminal-status happy/fail,
cost `NodeID`, snapshot) and the trackerbot business logic are **well-covered**. The weak
spot is the **CLI unification** (`cmd/tracker/run.go`): the rewrite routed `run()`/
`runTUI()` through `tracker.NewEngineFromGraph`, but the new glue has near-zero direct
coverage — and some old tests now exercise **dead code**, giving false confidence.

## Findings (from the coverage review, ranked)

### F3.1 — Interviewer selection is untested; its tests cover dead code (rated 8)
`run()` now selects via `applyInterviewerToConfig` (`run.go:258`), `runTUI()` via
`chooseTUIInterviewer`/`chooseTUIAutopilotInterviewer` — **none referenced by a test**.
Meanwhile `chooseInterviewer` (`run.go:957`) has **no non-test caller** (dead) yet the
five `TestChooseInterviewer*` tests still exercise it. The suite reports "interviewer
selection covered" while the real #478 selection logic is untested.

### F3.2 — `Engine.Run` returning `(result, err)` on error is unasserted (rated 8)
The "return a fail Result even on error" contract is what `RunManager.Result()` and
trackerbot's `deliver()` (posts "could not start" when `res == nil`) rely on. Failure
tests hit the failure-*halt* path (`err == nil`); the tool-safety test *tolerates* a nil
result rather than asserting non-nil. No test drives a true handler-error exit and asserts
`res != nil && err != nil` together.

### F3.3 — Gateway-via-`Config` path untested (rated 7)
The headline of the `os.Setenv` removal is "thread gateway config via `Config`." But every
gateway test resolves through `ResolveProviderBaseURLStrict` with empty gateway args — the
**env** path. `resolveProviderBaseURLWithGateway` with a non-empty gateway argument has no
test; `TestGateway*ViaConfig` stubs `run` and only asserts a global — near-vacuous.

### F3.4 — Terminal-status backstop budget/handler-error/retry-cancel unpinned (rated 6-7)
Success/provider-fail/strict-fail are covered; the budget (`haltForBudget`), handler-error,
and retry-cancel exits have no "exactly one terminal event" assertion. (Folds into Phase 0
T0.5 — same invariant test.)

### F3.5 — Slack button codec round-trip untested (rated 6)
`gateActionID`/`parseGateAction` (`slack.go:170-180`) encode/decode `gate|<id>|<index>`
for every click; `SplitN(...,3)` misroutes a `gateID` containing `|`. No round-trip test.

### F3.6 — `Config.Subgraphs` wiring untested (rated 6)
No `tracker`-package test references `Config.Subgraphs`; `NewEngineFromGraph`'s point (CLI
resolves subgraph files and passes them in) is unverified — the flat-graph test doesn't
exercise it.

### F3.7 — Lower-value gaps (rated 4-5)
- `handleAdmission` covers only `ErrRunKeyActive`; `ErrAtCapacity` + generic-error + the
  `launch` `MkdirAll`/`Start` failures untested (cheap via `WithMaxConcurrent(1)`).
- `TestRunner_DeliversFailure` asserts only a leading `❌` — both real diagnosis and terse
  fallback share it, so `formatDiagnosis` output is never actually verified.
- `Config.TokenTracker` test checks the default accessor is non-nil, not that an injected
  tracker accumulates — injection seam effectively unverified.
- `Config.LLMTrace` test asserts only `events > 0` — no type/content.
- `store` corrupt-file degradation untested (also covered by Durable Recovery TD.2).

## Tasks

### T3.1 — Test the live selection; delete the dead function
Table test `applyInterviewerToConfig(&cfg, isTerminal)` across
{auto-approve, webhook, persona, interactive} × {TTY on/off}, asserting the right `Config`
field is set; same for `chooseTUIInterviewer`/`chooseTUIAutopilotInterviewer`. **Delete
`chooseInterviewer`** and repoint/remove its five tests. (Removing dead code both fixes
the gap and drops a misleading source of green.)
- Files: `cmd/tracker/run.go` (delete dead fn), `cmd/tracker/main_test.go`.

### T3.2 — Assert `(result, err)` together on a real handler-error exit
Drive a `failingCompleter` with `RetryPolicy:"none"` to `handleNodeError`; assert
`res != nil && err != nil && res.Status == fail`.
- Files: new/near `pipeline/*_test.go` or `tracker_*_test.go`.

### T3.3 — Test gateway resolution from a non-empty `Config` argument
`resolveProviderBaseURLWithGateway("anthropic", "https://gw/…", "cf-aig")` → suffixed URL
with no env set; `(..., "bedrock")` for `openai-compat` → `ErrGatewayRouteRefused`.
- Files: `tracker_client_test.go`.

### T3.4 — Fold budget/handler-error/retry-cancel into the terminal-status invariant test
(See Phase 0 T0.5.) Extend the `collectTerminal` pattern to a budget-exceeded run (tiny
`--max-tokens`) and a handler-error run; assert `len(terminal) == 1` with the right status.

### T3.5 — `gateActionID`/`parseGateAction` round-trip
`for i, id := range {"g1","a|b","",…}`: `g, ok := parseGateAction(gateActionID(id, i))`;
assert `ok && g == id`; and a foreign action id returns `ok == false`.
- Files: `cmd/trackerbot/slack_test.go`.

### T3.6 — `Config.Subgraphs` through `NewEngineFromGraph`
Parent graph + `Config.Subgraphs` map; run; assert the subgraph node executed.
- Files: `tracker_fromgraph_test.go`.

### T3.7 — Tighten the weak assertions (F3.7)
Add the admission/capacity branches; assert `formatDiagnosis` real content, not just `❌`;
assert an injected `TokenTracker` accumulates; assert `LLMTrace` event type/content;
corrupt-`store` handled (shared with Durable Recovery TD.2).

## Verification / gates

- `go test ./... -short` green with the new tests; **coverage does not drop** and the dead
  `chooseInterviewer` is gone.
- `make complexity` green (test files don't count, but keep helpers tidy).

## Principle

Prefer tests that would have **caught the reviewed bugs** over line-coverage padding. The
invariant test (Phase 0) + these behavioral tests are the regression fence for the whole
hardening effort.
