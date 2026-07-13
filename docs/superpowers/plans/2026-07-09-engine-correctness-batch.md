# Engine-Correctness Batch Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Fix five engine-correctness bugs from the 2026-07-05 audit — condition-evaluator misrouting (#444), untyped `Outcome.Status` (#445), human-gate goroutine leak (#446), overbroad claude-CLI error classification (#447), and a panic on marshal failure (#448).

**Architecture:** All changes are in `pipeline/` and `pipeline/handlers/`, each a localized fix with a table/behavioral regression test. No `.dip`/example changes. Single PR, one commit per issue.

**Tech Stack:** Go; `pipeline` + `pipeline/handlers` packages; standard `testing`.

## Global Constraints

- Changes confined to `pipeline/` and `pipeline/handlers/`. No `.dip`, README, or example edits.
- **Never `git commit --no-verify`.** Pre-commit runs format/vet/build/tests/race/coverage/complexity/dippin-lint (~2–4 min); allow up to 6 minutes per commit. If a commit times out, run `git log --oneline -1` before retrying — never `git commit --amend`.
- `go build ./... && go test ./... -short` must pass after every task.
- `dippin doctor examples/build_product.dip` is a smoke check only (no `.dip` changed; expect unchanged grade A) — run it once at the end.
- Follow existing style; keep each fix surgical (no unrelated refactors).

---

### Task 1: #444 — quote-aware condition tokenizer + operator quote-stripping parity

**Files:**
- Modify: `pipeline/condition.go` (`evaluateOr` ~L37, `evaluateAnd` ~L52; `tryWordOp` ~L141, `tryNegatedWordOp` ~L123)
- Test: `pipeline/condition_test.go` (append; create if absent)

**Interfaces:**
- Produces: `splitOutsideQuotes(s, sep string) []string` (package-private helper in `condition.go`).

- [ ] **Step 1: Write the failing tests** (append to `pipeline/condition_test.go`)

```go
func TestConditionQuoteAwareSplitAndOperands(t *testing.T) {
	ctx := NewPipelineContext()
	ctx.Set("url", "http://a||b")
	ctx.Set("msg", "x && y")
	ctx.Set("resp", "saw an error here")

	cases := []struct {
		name string
		expr string
		want bool
	}{
		{"or-inside-quoted-value", `ctx.url = "http://a||b"`, true},
		{"and-inside-quoted-value", `ctx.msg = "x && y"`, true},
		{"contains-strips-quotes", `ctx.resp contains "error"`, true},
		{"top-level-or-still-splits", `ctx.url = "nope" || ctx.resp contains "error"`, true},
		{"top-level-and-still-splits", `ctx.resp contains "error" && ctx.url = "http://a||b"`, true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, err := EvaluateCondition(c.expr, ctx)
			if err != nil {
				t.Fatalf("EvaluateCondition(%q) error: %v", c.expr, err)
			}
			if got != c.want {
				t.Errorf("EvaluateCondition(%q) = %v, want %v", c.expr, got, c.want)
			}
		})
	}
}
```

Note: confirm the public entry point's name (`EvaluateCondition`) by checking the top of `condition.go`; if it differs (e.g. `Evaluate`), use the actual exported name in the test.

- [ ] **Step 2: Run the tests to verify they fail**

Run: `go test ./pipeline/ -run TestConditionQuoteAwareSplitAndOperands -v`
Expected: FAIL on `or-inside-quoted-value` / `and-inside-quoted-value` (phantom split) and `contains-strips-quotes` (quotes matched literally).

- [ ] **Step 3: Add the quote-aware splitter** — in `pipeline/condition.go`, add this helper (near `evaluateOr`):

```go
// splitOutsideQuotes splits s on the two-character sep ("||" or "&&"), but only
// where sep occurs OUTSIDE a double-quoted span. A value that legitimately
// contains || or && (a URL, a regex, a stderr fragment) is no longer split into
// phantom clauses (#444). An unterminated quote treats the rest as quoted, so a
// stray operator inside it never splits — a loud non-match beats a silent split.
func splitOutsideQuotes(s, sep string) []string {
	var parts []string
	start := 0
	inQuote := false
	for i := 0; i < len(s); i++ {
		if s[i] == '"' {
			inQuote = !inQuote
			continue
		}
		if !inQuote && strings.HasPrefix(s[i:], sep) {
			parts = append(parts, s[start:i])
			i += len(sep) - 1
			start = i + 1
		}
	}
	return append(parts, s[start:])
}
```

- [ ] **Step 4: Use it in `evaluateOr` and `evaluateAnd`** — replace:

```go
	branches := strings.Split(expr, "||")
```
with:
```go
	branches := splitOutsideQuotes(expr, "||")
```
and replace:
```go
	clauses := strings.Split(expr, "&&")
```
with:
```go
	clauses := splitOutsideQuotes(expr, "&&")
```

- [ ] **Step 5: Strip quotes on word-operator operands** — in `tryWordOp`, change the operand line:

```go
		value := strings.TrimSpace(clause[idx+len(op):])
```
to:
```go
		value := strings.Trim(strings.TrimSpace(clause[idx+len(op):]), `"`)
```
and make the identical change in `tryNegatedWordOp` (its `value := strings.TrimSpace(clause[idx+len(op):])` line). This matches the `strings.Trim(..., `"`)` the equality operators already use, so quote handling is uniform across all operators.

- [ ] **Step 6: Run the tests to verify they pass**

Run: `go test ./pipeline/ -run 'TestConditionQuoteAwareSplitAndOperands|TestEvaluateCondition|TestCondition' -v`
Expected: PASS (new test + existing condition tests stay green).

- [ ] **Step 7: Commit**

```bash
git add pipeline/condition.go pipeline/condition_test.go
git commit -m "fix(engine): quote-aware condition tokenizer + operator quote parity (#444)

Co-Authored-By: Claude Opus 4.8 (1M context) <noreply@anthropic.com>
Claude-Session: https://claude.ai/code/session_012U14Lj3xBwjmSLJAx6jqbr"
```

---

### Task 2: #446 — Cancel() the interviewer on gate timeout

**Files:**
- Modify: `pipeline/handlers/human.go` (`withTimeout` ~L29, `withTimeoutOutcome` ~L51, and the 5 call sites: ~L582, ~L717, ~L721, ~L972, ~L1008)
- Test: `pipeline/handlers/human_test.go` (append)

**Interfaces:**
- Consumes: the existing `Interviewer` interface; the optional `interface{ Cancel() }` (implemented by e.g. `WebhookInterviewer` and TUI-backed interviewers).
- Produces: new signatures `withTimeout(timeout time.Duration, i Interviewer, fn func() (string, error))` and `withTimeoutOutcome(timeout time.Duration, i Interviewer, fn func() (pipeline.Outcome, error))`; helper `cancelInterviewer(i Interviewer)`.

- [ ] **Step 1: Write the failing test** (append to `pipeline/handlers/human_test.go`)

```go
// blockingCancelInterviewer blocks in Ask until Cancel() closes its release
// channel; it records that Cancel() fired. Used to prove a gate timeout tears
// the interviewer down instead of leaking the goroutine (#446).
type blockingCancelInterviewer struct {
	release  chan struct{}
	canceled chan struct{}
	done     chan struct{}
}

func newBlockingCancelInterviewer() *blockingCancelInterviewer {
	return &blockingCancelInterviewer{
		release:  make(chan struct{}),
		canceled: make(chan struct{}),
		done:     make(chan struct{}),
	}
}
func (b *blockingCancelInterviewer) Ask(string, []string, string) (string, error) {
	<-b.release
	close(b.done)
	return "", nil
}
func (b *blockingCancelInterviewer) Cancel() {
	close(b.canceled)
	close(b.release)
}

func TestWithTimeoutCancelsInterviewer(t *testing.T) {
	b := newBlockingCancelInterviewer()
	_, err := withTimeout(10*time.Millisecond, b, func() (string, error) {
		return b.Ask("p", nil, "")
	})
	if err != errHumanTimeout {
		t.Fatalf("want errHumanTimeout, got %v", err)
	}
	select {
	case <-b.canceled:
	case <-time.After(time.Second):
		t.Fatal("Cancel() was not called on timeout — interviewer goroutine leaks (#446)")
	}
	select {
	case <-b.done:
	case <-time.After(time.Second):
		t.Fatal("Ask goroutine did not unblock after Cancel()")
	}
}
```

- [ ] **Step 2: Run the test to verify it fails**

Run: `go test ./pipeline/handlers/ -run TestWithTimeoutCancelsInterviewer -v`
Expected: FAIL to compile (`withTimeout` takes 2 args, not 3).

- [ ] **Step 3: Add the cancel helper + new signatures** — in `pipeline/handlers/human.go`, add:

```go
// cancelInterviewer tears down an interviewer that supports cancellation. Called
// when a gate times out so the blocked Ask goroutine unblocks instead of leaking
// (#446). Non-cancellable interviewers are a documented no-op.
func cancelInterviewer(i Interviewer) {
	if c, ok := i.(interface{ Cancel() }); ok {
		c.Cancel()
	}
}
```

Change `withTimeout` to take the interviewer and cancel on timeout:
```go
func withTimeout(timeout time.Duration, i Interviewer, fn func() (string, error)) (string, error) {
	if timeout <= 0 {
		return fn()
	}
	type result struct {
		val string
		err error
	}
	ch := make(chan result, 1)
	go func() {
		v, e := fn()
		ch <- result{v, e}
	}()
	select {
	case r := <-ch:
		return r.val, r.err
	case <-time.After(timeout):
		cancelInterviewer(i)
		return "", errHumanTimeout
	}
}
```

Make the identical change to `withTimeoutOutcome` (add `i Interviewer` param; call `cancelInterviewer(i)` on the `time.After` branch before `return pipeline.Outcome{}, errHumanTimeout`). Update the doc comment on `withTimeout` (~L22-28) to say the goroutine is now canceled via `cancelInterviewer` when the interviewer implements `Cancel()`.

- [ ] **Step 4: Update the 5 call sites** to pass the interviewer in scope:
  - ~L582: `withTimeoutOutcome(cfg.Timeout, h.interviewer, func() (pipeline.Outcome, error) {`
  - ~L717 (inside `askFreeformWithTimeout`, `lfi` branch): `withTimeout(timeout, lfi, func() (string, error) {`
  - ~L721 (`askFreeformWithTimeout`, `fi` branch): `withTimeout(timeout, fi, func() (string, error) {`
  - ~L972 (executeChoice): `withTimeout(cfg.Timeout, h.interviewer, func() (string, error) {`
  - ~L1008 (executeYesNo): `withTimeout(timeout, h.interviewer, func() (string, error) {`

(`fi`/`lfi` are `h.interviewer` type-asserted to a Freeform/Labeled interface, which still satisfies `Interviewer`.)

- [ ] **Step 5: Run the test to verify it passes**

Run: `go test ./pipeline/handlers/ -run 'TestWithTimeout|TestHuman' -v`
Expected: PASS.

- [ ] **Step 6: Commit**

```bash
git add pipeline/handlers/human.go pipeline/handlers/human_test.go
git commit -m "fix(engine): cancel the interviewer on human-gate timeout (#446)

Co-Authored-By: Claude Opus 4.8 (1M context) <noreply@anthropic.com>
Claude-Session: https://claude.ai/code/session_012U14Lj3xBwjmSLJAx6jqbr"
```

---

### Task 3: #447 — narrow the overbroad claude-CLI error classification + fixtures

**Files:**
- Modify: `pipeline/handlers/backend_claudecode.go` (`isBudgetError` ~L394, `isNetworkError` ~L399)
- Test: `pipeline/handlers/backend_claudecode_test.go` (append; create if absent)

**Interfaces:**
- Consumes: existing `classifyError(stderr string, exitCode int) pipeline.TerminalStatus`.

- [ ] **Step 1: Write the failing test** (append)

```go
func TestClassifyErrorNarrowMatches(t *testing.T) {
	cases := []struct {
		name     string
		stderr   string
		exitCode int
		want     pipeline.TerminalStatus
	}{
		{"real-network-econnrefused", "dial tcp: econnrefused", 1, pipeline.OutcomeRetry},
		{"real-network-conn-refused", "connection refused", 1, pipeline.OutcomeRetry},
		{"benign-connection-mention", "database connection established for the tool", 1, pipeline.OutcomeFail},
		{"real-budget", "budget exceeded for this run", 1, pipeline.OutcomeFail},
		{"benign-budget-mention", "the monthly budget is $500", 1, pipeline.OutcomeFail},
		{"rate-limit-still-retries", "rate limit reached (429)", 1, pipeline.OutcomeRetry},
		{"sigkill", "", 137, pipeline.OutcomeFail},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := classifyError(c.stderr, c.exitCode); got != c.want {
				t.Errorf("classifyError(%q, %d) = %v, want %v", c.stderr, c.exitCode, got, c.want)
			}
		})
	}
}
```

- [ ] **Step 2: Run the test to verify it fails**

Run: `go test ./pipeline/handlers/ -run TestClassifyErrorNarrowMatches -v`
Expected: FAIL on `benign-connection-mention` (bare "connection" → misclassed retry) and `benign-budget-mention` (bare "budget" → misclassed fail-via-budget; here still Fail, but for the wrong reason — the test pins the outcome, and the narrowing is what makes it robust; the connection case is the hard failure).

- [ ] **Step 3: Narrow the two overbroad matchers** — in `pipeline/handlers/backend_claudecode.go`, replace:

```go
func isBudgetError(lower string) bool {
	return strings.Contains(lower, "budget") ||
		strings.Contains(lower, "spending limit")
}
```
with:
```go
// Anchored to error-shaped phrases (#447) — bare "budget" matched benign agent
// output ("the budget is $5") and flipped classification.
func isBudgetError(lower string) bool {
	return strings.Contains(lower, "budget exceeded") ||
		strings.Contains(lower, "budget limit") ||
		strings.Contains(lower, "spending limit")
}
```
and replace:
```go
func isNetworkError(lower string) bool {
	return strings.Contains(lower, "econnrefused") ||
		strings.Contains(lower, "network") ||
		strings.Contains(lower, "connection")
}
```
with:
```go
// Anchored to error-shaped phrases (#447) — bare "connection"/"network" matched
// benign lines (a DB "connection" message) and flipped fails into retries.
func isNetworkError(lower string) bool {
	return strings.Contains(lower, "econnrefused") ||
		strings.Contains(lower, "connection refused") ||
		strings.Contains(lower, "connection reset") ||
		strings.Contains(lower, "network is unreachable") ||
		strings.Contains(lower, "dial tcp")
}
```

Leave `isAuthError`/`isCreditError`/`isRateLimitError` unchanged — their phrases (`"invalid api key"`, `"credit balance"`, `"rate limit"`) are already specific; narrowing auth risks missing real auth failures (a worse trade than a rare benign match). Add a one-line comment on `classifyError` noting NDJSON-error-event parsing is a deferred follow-up.

- [ ] **Step 4: Run the test to verify it passes**

Run: `go test ./pipeline/handlers/ -run 'TestClassifyError' -v`
Expected: PASS (incl. existing classifyError tests if any).

- [ ] **Step 5: Commit**

```bash
git add pipeline/handlers/backend_claudecode.go pipeline/handlers/backend_claudecode_test.go
git commit -m "fix(engine): anchor overbroad claude-CLI error classification (#447)

Co-Authored-By: Claude Opus 4.8 (1M context) <noreply@anthropic.com>
Claude-Session: https://claude.ai/code/session_012U14Lj3xBwjmSLJAx6jqbr"
```

---

### Task 4: #448 — return an error instead of panicking on marshal failure

**Files:**
- Modify: `pipeline/handlers/interview_result.go` (`SerializeInterviewResult` ~L30), `pipeline/handlers/human.go` (caller ~L860)
- Test: `pipeline/handlers/interview_result_test.go` (update existing callers)

**Interfaces:**
- Produces: `SerializeInterviewResult(r InterviewResult) (string, error)` (was `string`).

- [ ] **Step 1: Change the signature** — in `pipeline/handlers/interview_result.go`, replace:

```go
func SerializeInterviewResult(r InterviewResult) string {
	b, err := json.Marshal(r)
	if err != nil {
		panic(fmt.Sprintf("interview result marshal failed: %v", err))
	}
	return string(b)
}
```
with:
```go
// SerializeInterviewResult marshals an InterviewResult to a JSON string. A
// marshal failure is a runtime data condition, not a programmer invariant, so it
// is returned (the caller fails the node) rather than panicking the process (#448).
func SerializeInterviewResult(r InterviewResult) (string, error) {
	b, err := json.Marshal(r)
	if err != nil {
		return "", fmt.Errorf("interview result marshal failed: %w", err)
	}
	return string(b), nil
}
```
(Drop the now-unused `fmt` import only if `fmt` is otherwise unused — check; `%w` keeps it used here.)

- [ ] **Step 2: Update the real caller** — in `pipeline/handlers/human.go` ~L860, replace:

```go
	jsonStr := SerializeInterviewResult(*result)
```
with:
```go
	jsonStr, err := SerializeInterviewResult(*result)
	if err != nil {
		return pipeline.Outcome{Status: string(pipeline.OutcomeFail)}, fmt.Errorf("serialize interview result for node %q: %w", node.ID, err)
	}
```
(Confirm `node` and a returnable `(pipeline.Outcome, error)` signature are in scope at L860 — the enclosing function is the interview executor; if the local is named differently than `node`, use the actual identifier. `pipeline.OutcomeFail` becomes typed after Task 5; string() cast here is fine pre-Task-5 and is swept by Task 5.)

- [ ] **Step 3: Update the test callers** — in `pipeline/handlers/interview_result_test.go` (`TestSerializeInterviewResult` ~L30, `TestSerializeInterviewResult_Flags` ~L87), change `s := SerializeInterviewResult(r)` to:
```go
	s, err := SerializeInterviewResult(r)
	if err != nil {
		t.Fatalf("SerializeInterviewResult: %v", err)
	}
```
And in `pipeline/handlers/interview_integration_test.go` ~L178 and `pipeline/handlers/human_test.go` ~L604, change the inline `SerializeInterviewResult(x)` in `pctx.Set(...)` to a two-line form:
```go
	js, err := SerializeInterviewResult(prev) // or previousResult
	if err != nil {
		t.Fatal(err)
	}
	pctx.Set("interview_answers", js)
```
(use the local variable name present at each site).

- [ ] **Step 4: Run tests**

Run: `go build ./... && go test ./pipeline/handlers/ -run 'TestSerializeInterviewResult|TestInterview' -v`
Expected: build passes; tests PASS.

- [ ] **Step 5: Commit**

```bash
git add pipeline/handlers/interview_result.go pipeline/handlers/human.go pipeline/handlers/interview_result_test.go pipeline/handlers/interview_integration_test.go pipeline/handlers/human_test.go
git commit -m "fix(engine): fail the node instead of panicking on interview marshal error (#448)

Co-Authored-By: Claude Opus 4.8 (1M context) <noreply@anthropic.com>
Claude-Session: https://claude.ai/code/session_012U14Lj3xBwjmSLJAx6jqbr"
```

---

### Task 5: #445 — type `Outcome.Status` as `TerminalStatus` (compiler-driven sweep)

**Files:**
- Modify: `pipeline/handler.go` (`Outcome.Status` ~L18) + every site the compiler flags across `pipeline/` and `pipeline/handlers/` (≈56 sites in 14 files: engine.go, engine_run.go, engine_checkpoint.go, subgraph.go, and handlers start/exit/human/codergen/fanin_policy/parallel/conditional/fanin/tool/manager_loop).
- Test: none new — the change is compiler-verified; the full suite must stay green.

**Interfaces:**
- Produces: `Outcome.Status` is now `TerminalStatus` (was `string`). Comparisons use the typed constants directly; JSON DTO fields stay `string`.

- [ ] **Step 1: Flip the field type** — in `pipeline/handler.go`, change:
```go
	Status             string
```
to:
```go
	Status             TerminalStatus
```

- [ ] **Step 2: Compile to enumerate every break**

Run: `go build ./... 2>&1 | head -80`
Expected: a list of type-mismatch errors — each is a site to fix. Iterate until clean:
- **Comparisons** `x.Status == string(pipeline.OutcomeSuccess)` → `x.Status == pipeline.OutcomeSuccess` (drop the `string(...)` cast). Same for `OutcomeFail`/`OutcomeRetry`/`"partial_success"` (use the typed constant, or `TerminalStatus("partial_success")` if no constant exists).
- **Assignments** `Outcome{Status: "success"}` or `Outcome{Status: string(pipeline.OutcomeX)}` → `Outcome{Status: pipeline.OutcomeX}` (typed constant).
- **JSON DTO boundaries** — where an `Outcome.Status` is written into a persisted/serialized struct whose field is `string` (`Checkpoint`/`checkpoint.go`, `TraceEntry`/`trace.go`, event structs in `events.go`/`events_jsonl.go`), convert explicitly: `dto.Status = string(outcome.Status)`. Do **not** change those DTO field types.

- [ ] **Step 3: Compile clean + run the full suite**

Run: `go build ./... && go test ./... -short 2>&1 | tail -20`
Expected: build clean; all packages PASS. Fix any test-file comparisons the same way (`== string(OutcomeX)` → `== OutcomeX`, or `string(outcome.Status)` when comparing to a raw string literal is genuinely intended).

- [ ] **Step 4: Grep to confirm no stray casts remain in comparisons**

Run: `grep -rn "Status == string(\|string(.*\.Status)" pipeline/ --include="*.go" | grep -v _test.go`
Expected: only legitimate DTO-boundary conversions (`= string(outcome.Status)` into a serialized struct), no comparison casts.

- [ ] **Step 5: Commit**

```bash
git add pipeline/
git commit -m "refactor(engine): type Outcome.Status as TerminalStatus (#445)

Co-Authored-By: Claude Opus 4.8 (1M context) <noreply@anthropic.com>
Claude-Session: https://claude.ai/code/session_012U14Lj3xBwjmSLJAx6jqbr"
```

---

### Task 6: CHANGELOG + full-suite verification

**Files:**
- Modify: `CHANGELOG.md` (`## [Unreleased]`)
- Verify only: whole repo + example pipelines

- [ ] **Step 1: Add the CHANGELOG entries** — under `## [Unreleased]`, add (create `### Fixed`/`### Changed` if absent):

```markdown
### Fixed

- **Condition evaluator no longer misroutes on `||`/`&&` inside values (#444).**
  Splitting is now quote-aware, so a condition value that legitimately contains
  `||`/`&&` (a URL, regex, or stderr fragment) evaluates correctly instead of
  silently splitting into phantom clauses. `contains`/`startswith`/`endswith`/`in`
  now strip surrounding quotes on their operand like `=`/`==`/`!=` already did.
- **Human-gate timeouts no longer leak the interviewer goroutine (#446).** On
  timeout the handler now cancels the interviewer (when it implements `Cancel()`),
  turning a permanently-blocked goroutine into orderly teardown.
- **claude-code error classification is no longer flipped by benign output (#447).**
  The network/budget matchers are anchored to error-shaped phrases (e.g.
  `connection refused`, `budget exceeded`) instead of bare `connection`/`budget`,
  so an unrelated log line can't turn a hard failure into an infinite retry.
  (Parsing the CLI's NDJSON error events as the primary signal is a follow-up.)
- **The interview-result handler fails the node instead of panicking (#448).** A
  JSON marshal failure now returns an error routed as a node failure rather than
  crashing the pipeline process.

### Changed

- **`Outcome.Status` is now typed `TerminalStatus` (#445).** Engine comparisons
  drop their `string(...)` casts and a typoed status string (`"succes"`) now fails
  to compile instead of silently routing to the wrong branch. Persisted JSON
  fields stay `string` (unchanged on-disk format).
```

- [ ] **Step 2: Full short suite**

Run: `go build ./... && go test ./... -short`
Expected: all packages PASS.

- [ ] **Step 3: Batch guard tests together**

Run: `go test ./pipeline/ ./pipeline/handlers/ -run 'TestConditionQuoteAware|TestWithTimeoutCancels|TestClassifyErrorNarrow|TestSerializeInterviewResult' -v`
Expected: all PASS.

- [ ] **Step 4: Smoke the core pipeline**

Run: `dippin doctor examples/build_product.dip`
Expected: grade A (unchanged — no `.dip` touched). If `dippin` is not on PATH, report it and do NOT `go install` it.

- [ ] **Step 5: Commit**

```bash
git add CHANGELOG.md
git commit -m "docs: changelog for engine-correctness batch (#444-#448)

Co-Authored-By: Claude Opus 4.8 (1M context) <noreply@anthropic.com>
Claude-Session: https://claude.ai/code/session_012U14Lj3xBwjmSLJAx6jqbr"
```

- [ ] **Step 6: (held for after final review)** Push the branch, open the PR closing #444/#445/#446/#447/#448, then cut `release: v0.44.0`. Do not do this until the whole-branch review passes.

---

## Self-Review

**Spec coverage:** #444 → Task 1; #446 → Task 2; #447 → Task 3; #448 → Task 4; #445 → Task 5; CHANGELOG/verify/release → Task 6. #348 correctly excluded (blocked). Every spec fix maps to a task.

**Placeholder scan:** every code/test step shows exact old→new or full test code; the two "confirm the identifier at this site" notes (Task 1 entry-point name, Task 4 `node` local) are real disambiguations against the live file, not deferred work.

**Type/interface consistency:** `splitOutsideQuotes(s, sep string) []string` used consistently (Task 1). `withTimeout(timeout, i Interviewer, fn)` / `cancelInterviewer(i Interviewer)` consistent across Task 2's signature + 5 call sites. `SerializeInterviewResult(...) (string, error)` consistent across Task 4's definition + all callers. Task 5's `TerminalStatus` typing is compiler-enforced. `pipeline.OutcomeFail`/`OutcomeSuccess`/`OutcomeRetry` used with the pre/post-Task-5 cast convention noted where relevant.

**Ordering:** Tasks 1–4 are localized and independently green. Task 5 (the sweep) runs after them so it types the whole tree including any new assignments the earlier tasks added (e.g. Task 4's `Outcome{Status: string(pipeline.OutcomeFail)}` becomes typed). Follow numeric order.
