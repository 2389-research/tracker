# Engine-Correctness Batch (#444/#445/#446/#447/#448) — Design

**Date:** 2026-07-09
**Target release:** v0.44.0 (batch PR)
**Scope:** `pipeline/` + `pipeline/handlers/` + regression tests. No `.dip`/example changes.

## Background

Five engine-correctness bugs from the 2026-07-05 two-persona expert audit (systems-programmer
review + PM review), each filed with a precise `file:line` and "verified against main @
e600ec4". Re-verified against current HEAD (v0.43.0) before this design — all five still
reproduce as filed.

**Dropped from the batch: #348** (goal-gate retry). Its defect 1 (retry re-runs the gate) is
already fixed on `main` via #360/#361 (`GateRecheckPending`); defect 2 (mark a human "accept"
as `validation_overridden`) is blocked upstream on #271 / dippin-lang#124 (no `.dip` input path
for `Edge.Override`). The maintainer's own comment: "No action possible here until that lands."
#348 stays open.

## Verification status at HEAD

| # | Sev | Locus | Confirmed |
|---|-----|-------|-----------|
| 444 | P1 | `pipeline/condition.go:37,52` (split on `\|\|`/`&&`, no quote-awareness); `:132,141+` (word ops don't strip quotes) | yes |
| 445 | P1 | `pipeline/handler.go:18` `Status string` vs `TerminalStatus` constants; casts at `engine.go:400,470,483,752` | yes |
| 446 | P1 | `pipeline/handlers/human.go` `withTimeout`/`withTimeoutOutcome` leak the goroutine on timeout | yes |
| 447 | P1 | `pipeline/handlers/backend_claudecode.go:394` `isBudgetError` matches bare "budget"; `:399` `isNetworkError` matches bare "connection"/"network" | yes |
| 448 | P2 | `pipeline/handlers/interview_result.go` `SerializeInterviewResult` `panic`s on marshal failure | yes |

## Decisions (from brainstorming)

- **#444:** quote-aware tokenizer (split only on top-level unquoted operators) — the issue says
  values legitimately contain `||`/`&&`/URLs/regexes, so prohibiting them would reject valid
  conditions. Align quote-stripping across all operators either way.
- **#446:** `Cancel()`-on-timeout via the existing `Cancellable` interface (minimal surface
  change), not a `ctx`-into-`Ask` interface change.
- **Batch scope:** #444/#445/#446/#447/#448; drop #348.

## The fixes

### #444 — quote-aware condition tokenizer + operator quote-stripping parity

`condition.go` `evaluateOr`/`evaluateAnd` call `strings.Split(expr, "||")` / `strings.Split(expr,
"&&")` with no quote-awareness, so a clause value containing those tokens (a URL, a stderr
fragment, a regex) is split into phantom clauses and misroutes silently. Separately, `=`/`==`/`!=`
strip surrounding quotes from the expected operand (`condition.go:94,101,107`) but the word
operators `contains`/`startswith`/`endswith`/`in` pass the raw operand, so `ctx.x contains
"error"` matches the literal quote characters.

**Fix:**
- Add a quote-aware splitter: split `expr` on a two-char operator (`||` or `&&`) only when the
  operator occurs **outside** a double-quoted span. A single pass tracking an `inQuote` bool over
  the runes; accumulate segments; split at the operator only when `!inQuote`. Use it in place of
  both `strings.Split` calls.
- Strip surrounding double quotes from the operand of `contains`/`startswith`/`endswith`/`in`
  (and their `not` variants), matching the equality operators. Extract a tiny `unquote(s string)
  string` helper (trim one leading and one trailing `"`), and use it at every operand site so
  handling is uniform.

**Edge cases:** an unbalanced quote degrades to "treat the rest as quoted" (no split) — a loud
non-match is safer than a silent phantom split; `||`/`&&` as the whole value in quotes stays a
single clause; empty clauses (from a trailing operator) evaluate false, unchanged.

**Tests** (`pipeline/condition_test.go` additions):
- `ctx.url = "http://a||b"` with `ctx.url` set to that value → true (no phantom split).
- `ctx.msg contains "a&&b"` with `ctx.msg` containing `a&&b` → true.
- `ctx.last_response contains "error"` where the value is `... error ...` → true (quotes
  stripped); and where the value literally contains a `"` → matches accordingly.
- A genuine `a = 1 || b = 2` still short-circuits correctly (top-level `||` still splits).

### #445 — type `Outcome.Status` as `TerminalStatus`

`handler.go:18` declares `Status string` while the outcome constants are `TerminalStatus`, so
every engine comparison casts (`== string(OutcomeX)`) and a typoed status string compiles and
misroutes.

**Fix:** change `Outcome.Status` to `TerminalStatus`. Update the comparison/assignment sites
(`engine.go`, `engine_run.go`, `engine_checkpoint.go`, handlers that build an `Outcome`) so
`== string(OutcomeX)` becomes `== OutcomeX` and literal assignments use the typed constants.
Where a handler currently assigns a raw string (`Outcome{Status: "success"}`), switch to the
constant.

**Boundary — leave JSON DTOs as `string`.** `checkpoint.go` `Status`, `trace.go` `Status`,
`events*.go` `OutcomeStatus` are serialization structs persisted to `checkpoint.json` /
`activity.jsonl`. `TerminalStatus` is `type TerminalStatus string` so it marshals identically,
but changing persisted-DTO field types for no compiler-safety gain risks churn and reviewer
noise. Keep those as `string`; convert at the boundary (`string(outcome.Status)`) where a DTO is
populated. This is the "downstream record fields where sensible" line from the issue.

**Tests:** the migration is compiler-verified; the existing suite (`engine_*_test.go`,
`checkpoint_test.go`) must stay green. Add no bespoke test — the value is that a typoed status
now fails to compile. Confirm `go build ./...` + full suite.

### #446 — `Cancel()` the interviewer on gate timeout

`human.go` `withTimeout`/`withTimeoutOutcome` spawn a goroutine per gate; on timeout it is
abandoned (comment admits the leak). Every autopilot/embedded gate timeout leaks a goroutine
blocked on a channel or stdin.

**Fix:** thread the interviewer (or a `func()` cancel) into `withTimeout`/`withTimeoutOutcome`.
On the `time.After` branch, if the interviewer implements the existing `Cancellable` interface
(`Cancel()`), call it before returning `errHumanTimeout`, so the blocked `fn` unblocks and the
goroutine exits. Non-`Cancellable` interviewers behave exactly as today (documented residual).
The signature change is internal to `handlers`; update the (small number of) call sites.

**Tests** (`pipeline/handlers/human_test.go` additions):
- A fake interviewer whose `Ask` blocks on a channel and which implements `Cancellable` (its
  `Cancel()` closes that channel). Call `withTimeout` with a short timeout; assert it returns
  `errHumanTimeout`, `Cancel()` was invoked, and the goroutine unblocked (e.g. a `sync.WaitGroup`
  the fake's `Ask` `Done()`s on exit, awaited with a bounded timeout).
- A non-`Cancellable` fake still returns `errHumanTimeout` (no panic; documented leak path).

### #447 — robust claude-CLI error classification

`classifyError(stderr, exitCode)` routes retry-vs-fail by substring-matching the claude CLI's
raw stderr. `isBudgetError` matches bare `"budget"`; `isNetworkError` matches bare
`"connection"`/`"network"`. Any tool/log line mentioning those flips classification (a DB
"connection" error inside agent output → retryable network), and the CLI's wording is an
unversioned surface — a release reword silently turns auth failures into infinite retries.

**Fix (bounded — narrow + prefer structure + fixtures):**
- **Prefer the exit code where reliable.** Exit 137 (SIGKILL) already → fail; keep. Where the
  claude CLI uses a stable non-zero code for a class, branch on it before substring matching.
  (Document which codes are relied on; if none are stable beyond 137, this is a no-op and the
  narrowing below carries the fix.)
- **Anchor the overbroad patterns** to error-shaped phrases: `isNetworkError` → require
  `"econnrefused"`, `"connection refused"`, `"connection reset"`, `"network is unreachable"`,
  `"dial tcp"` (drop bare `"connection"`/`"network"`); `isBudgetError` → require `"spending
  limit"`, `"budget exceeded"`, `"budget limit"` (drop bare `"budget"`). Keep `isAuthError` /
  `isCreditError` / `isRateLimitError` phrases (already specific) but review each for the same
  bare-word risk.
- **Regression fixtures:** a table of representative real stderr strings per class →
  expected `TerminalStatus`, plus **negative** cases (a benign line containing "connection" /
  "budget" that must classify as the unclassified `OutcomeFail`, not retry). Fixtures live in
  the test file (short strings), documented as "capture real CLI output as it drifts."

**Non-goal (follow-up):** parsing the CLI's NDJSON error events as the primary signal — larger,
and the narrowing above closes the filed correctness holes. Note it in the tracking/CHANGELOG.

**Tests** (`pipeline/handlers/backend_claudecode_test.go` additions): the fixture table above,
asserting each `classifyError(stderr, exitCode)` result, including the benign-mention negatives.

### #448 — no panic on marshal failure

`SerializeInterviewResult` `panic`s on a `json.Marshal` error — a runtime data condition that
crashes the whole pipeline process instead of failing the node.

**Fix:** change the signature to `SerializeInterviewResult(r InterviewResult) (string, error)`;
return the marshal error. The single caller (the interview-result handler) fails the node with a
diagnostic outcome (`Outcome{Status: OutcomeFail, ...}` / returned error) instead of relying on a
panic. Update all call sites.

**Tests** (`pipeline/handlers/interview_result_test.go` additions): the happy path returns
`(json, nil)`; the caller path surfaces an error as a node failure rather than a panic. (Marshal
cannot realistically fail for this struct, so the primary guarantee is the signature + caller
wiring; assert the happy path and that no call site discards the error.)

## Testing / verification

1. `go build ./...` — must pass (the #445 migration is compiler-verified).
2. `go test ./... -short` — all packages green.
3. New per-fix tests above (`condition_test.go`, `human_test.go`,
   `backend_claudecode_test.go`, `interview_result_test.go`).
4. `dippin doctor examples/build_product.dip` as a smoke check (no `.dip` changed; expect
   unchanged grade A).
5. CHANGELOG `Fixed`/`Changed` entries; version bump to **v0.44.0**.

## Out of scope / follow-ups

- **#348 defect 2** (`validation_overridden`) — blocked on #271 / dippin-lang#124.
- **#447 NDJSON-error-event parsing** as the primary classification signal (bounded out; the
  narrowing closes the filed holes).
- Retyping persisted JSON DTO `Status` fields (#445) — deliberately left `string`.

## Commit / PR structure

One PR (`fix/engine-correctness-batch` → `main`), one commit per issue where it keeps the diff
reviewable, closing #444, #445, #446, #447, #448. Followed by a `release: v0.44.0` cut.
