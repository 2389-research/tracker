# Engine P1 Fixes Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Fix three P1 engine bugs: per-node context keys for last_response (#24), condition parser hardening (#21), and consensus pipeline parallelization (#26).

**Architecture:** Three independent fixes. Task 1 adds per-node response keys in handlers. Task 2 hardens the condition evaluator. Task 3 rewrites the consensus pipeline .dip file to use parallel fan-out/fan-in (following the existing `consensus_task_parity.dip` pattern).

**Tech Stack:** Go 1.25, standard library. Dippin `.dip` pipeline format.

**Spec:** `docs/superpowers/specs/2026-04-03-engine-p1-fixes-design.md`

---

### Task 1: Per-node namespaced context keys (#24)

**Files:**
- Modify: `pipeline/context.go:8-24` (add constant)
- Modify: `pipeline/handlers/codergen.go:284-299` (buildFailureOutcome), `pipeline/handlers/codergen.go:336-342` (buildSuccessOutcome)
- Modify: `pipeline/handlers/human.go:452-455` (freeform outcome), `pipeline/handlers/human.go:559-565` (interview outcome)
- Test: `pipeline/handlers/codergen_test.go`, `pipeline/handlers/human_test.go`

- [ ] **Step 1: Write failing test for codergen per-node key**

Add to `pipeline/handlers/codergen_test.go`:

```go
func TestCodergenHandler_WritesPerNodeResponse(t *testing.T) {
	client := &fakeCompleter{responseText: "per-node output"}
	h := NewCodergenHandler(client, t.TempDir())
	node := &pipeline.Node{ID: "mynode", Shape: "box", Handler: "codergen", Attrs: map[string]string{"prompt": "test"}}
	pctx := pipeline.NewPipelineContext()
	outcome, err := h.Execute(context.Background(), node, pctx)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// last_response should still be set (backward compat)
	if outcome.ContextUpdates[pipeline.ContextKeyLastResponse] != "per-node output" {
		t.Errorf("last_response = %q, want %q", outcome.ContextUpdates[pipeline.ContextKeyLastResponse], "per-node output")
	}

	// Per-node key should also be set
	perNodeKey := pipeline.ContextKeyResponsePrefix + "mynode"
	if outcome.ContextUpdates[perNodeKey] != "per-node output" {
		t.Errorf("%s = %q, want %q", perNodeKey, outcome.ContextUpdates[perNodeKey], "per-node output")
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./pipeline/handlers/ -run TestCodergenHandler_WritesPerNodeResponse -v`
Expected: FAIL — `ContextKeyResponsePrefix` undefined.

- [ ] **Step 3: Add the constant**

In `pipeline/context.go`, add to the constants block after `ContextKeyHumanResponse`:

```go
	ContextKeyResponsePrefix = "response."
```

- [ ] **Step 4: Run test again — still fails**

Run: `go test ./pipeline/handlers/ -run TestCodergenHandler_WritesPerNodeResponse -v`
Expected: FAIL — per-node key not set in ContextUpdates.

- [ ] **Step 5: Add per-node key to codergen success outcome**

In `pipeline/handlers/codergen.go`, in `buildSuccessOutcome`, after the existing `ContextUpdates` map initialization (line 338-340), add the per-node key:

```go
	outcome := pipeline.Outcome{
		Status: status,
		ContextUpdates: map[string]string{
			pipeline.ContextKeyLastResponse:                responseText,
			pipeline.ContextKeyResponsePrefix + node.ID:    responseText,
		},
		Stats: buildSessionStats(sessResult),
	}
```

- [ ] **Step 6: Add per-node key to codergen failure outcomes**

In `buildFailureOutcome`, in the retry outcome (line 286-288):

```go
	outcome := pipeline.Outcome{
		Status: pipeline.OutcomeRetry,
		ContextUpdates: map[string]string{
			pipeline.ContextKeyLastResponse:              runErr.Error(),
			pipeline.ContextKeyResponsePrefix + node.ID: runErr.Error(),
		},
		Stats: buildSessionStats(sessResult),
	}
```

And in the fatal empty-response outcome (around line 321), find the `ContextUpdates` map and add the per-node key alongside `ContextKeyLastResponse` with the same diagnostic message.

- [ ] **Step 7: Run test to verify it passes**

Run: `go test ./pipeline/handlers/ -run TestCodergenHandler_WritesPerNodeResponse -v`
Expected: PASS

- [ ] **Step 8: Write failing test for human handler per-node key**

Add to `pipeline/handlers/human_test.go`:

```go
func TestHumanHandler_WritesPerNodeResponse(t *testing.T) {
	// Test freeform mode sets per-node key
	graph := &pipeline.Graph{
		Nodes: map[string]*pipeline.Node{
			"ask": {ID: "ask", Shape: "hexagon", Attrs: map[string]string{"prompt": "what?"}},
		},
	}
	fi := &fakeInterviewer{freeformResponse: "user said hello"}
	h := NewHumanHandler(fi, graph)
	node := graph.Nodes["ask"]
	pctx := pipeline.NewPipelineContext()
	outcome, err := h.Execute(context.Background(), node, pctx)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// human_response should still be set
	if outcome.ContextUpdates[pipeline.ContextKeyHumanResponse] != "user said hello" {
		t.Errorf("human_response = %q, want %q", outcome.ContextUpdates[pipeline.ContextKeyHumanResponse], "user said hello")
	}

	// Per-node key should also be set
	perNodeKey := pipeline.ContextKeyResponsePrefix + "ask"
	if outcome.ContextUpdates[perNodeKey] != "user said hello" {
		t.Errorf("%s = %q, want %q", perNodeKey, outcome.ContextUpdates[perNodeKey], "user said hello")
	}
}
```

NOTE: Check the existing test file for the `fakeInterviewer` type — it likely already exists. If `freeformResponse` isn't the right field name, match whatever mock is used in the existing human handler tests.

- [ ] **Step 9: Add per-node key to human handler freeform outcome**

In `pipeline/handlers/human.go`, freeform outcome (line 452-455):

```go
	outcome := pipeline.Outcome{
		Status:         pipeline.OutcomeSuccess,
		ContextUpdates: map[string]string{
			pipeline.ContextKeyHumanResponse:              response,
			pipeline.ContextKeyResponsePrefix + node.ID:   response,
		},
	}
```

- [ ] **Step 10: Add per-node key to human handler interview outcome**

In `pipeline/handlers/human.go`, interview outcome (line 559-564):

```go
	return pipeline.Outcome{
		Status: status,
		ContextUpdates: map[string]string{
			answersKey:                                   jsonStr,
			pipeline.ContextKeyHumanResponse:             summary,
			pipeline.ContextKeyResponsePrefix + node.ID: summary,
		},
	}, nil
```

- [ ] **Step 11: Run all handler tests**

Run: `go test ./pipeline/handlers/ -v -short`
Expected: All tests pass.

- [ ] **Step 12: Commit**

```bash
git add pipeline/context.go pipeline/handlers/codergen.go pipeline/handlers/human.go pipeline/handlers/codergen_test.go pipeline/handlers/human_test.go
git commit -m "fix(handlers): write per-node response keys alongside last_response (#24)"
```

---

### Task 2: Harden condition parser (#21)

**Files:**
- Modify: `pipeline/condition.go:56-93` (evaluateClause)
- Test: `pipeline/condition_test.go`

- [ ] **Step 1: Write failing tests**

Add to `pipeline/condition_test.go`:

```go
func TestConditionDoubleEquals(t *testing.T) {
	ctx := NewPipelineContext()
	ctx.Set("outcome", "success")
	result, err := EvaluateCondition("outcome == success", ctx)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !result {
		t.Error("expected outcome == success to be true")
	}
}

func TestConditionDoubleEqualsFailure(t *testing.T) {
	ctx := NewPipelineContext()
	ctx.Set("outcome", "fail")
	result, err := EvaluateCondition("outcome == success", ctx)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result {
		t.Error("expected outcome == success to be false when outcome is fail")
	}
}

func TestConditionQuotedValues(t *testing.T) {
	ctx := NewPipelineContext()
	ctx.Set("name", "hello world")
	result, err := EvaluateCondition(`name = "hello world"`, ctx)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !result {
		t.Error(`expected name = "hello world" to be true`)
	}
}

func TestConditionQuotedNotEquals(t *testing.T) {
	ctx := NewPipelineContext()
	ctx.Set("status", "done")
	result, err := EvaluateCondition(`status != "pending"`, ctx)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !result {
		t.Error(`expected status != "pending" to be true when status is done`)
	}
}

func TestConditionDoubleEqualsWithQuotes(t *testing.T) {
	ctx := NewPipelineContext()
	ctx.Set("outcome", "success")
	result, err := EvaluateCondition(`outcome == "success"`, ctx)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !result {
		t.Error(`expected outcome == "success" to be true`)
	}
}
```

- [ ] **Step 2: Run tests to verify failures**

Run: `go test ./pipeline/ -run "TestConditionDoubleEquals|TestConditionQuoted" -v`
Expected: FAIL — `==` is not handled properly (splits at first `=`, leaving `= success` as the value).

- [ ] **Step 3: Add `==` support and quote stripping**

In `pipeline/condition.go`, replace the `!=` and `=` handling block in `evaluateClause` (lines 77-90) with:

```go
	// Try != first since it contains = as a substring.
	if idx := strings.Index(clause, "!="); idx >= 0 {
		key := strings.TrimSpace(clause[:idx])
		expected := strings.Trim(strings.TrimSpace(clause[idx+2:]), `"`)
		actual := resolveAndWarnVar(key, ctx)
		return actual != expected, nil
	}

	// Check == before = (== contains = as substring).
	if idx := strings.Index(clause, "=="); idx >= 0 {
		key := strings.TrimSpace(clause[:idx])
		expected := strings.Trim(strings.TrimSpace(clause[idx+2:]), `"`)
		actual := resolveAndWarnVar(key, ctx)
		return actual == expected, nil
	}

	if idx := strings.Index(clause, "="); idx >= 0 {
		key := strings.TrimSpace(clause[:idx])
		expected := strings.Trim(strings.TrimSpace(clause[idx+1:]), `"`)
		actual := resolveAndWarnVar(key, ctx)
		return actual == expected, nil
	}
```

- [ ] **Step 4: Add limitation documentation**

Add a comment at the top of `pipeline/condition.go`, after the ABOUTME lines and before the import:

```go
// Limitations:
//   - Operator splitting uses strings.Split on "||" and "&&". Values containing
//     these literals will be misinterpreted. Use quoted values for safety.
//   - No parentheses support for grouping. || is lowest precedence, && is higher.
//   - Both = and == are accepted for equality. Use = for consistency with .dip convention.
```

- [ ] **Step 5: Run tests to verify they pass**

Run: `go test ./pipeline/ -run "TestCondition" -v`
Expected: All condition tests pass (both new and existing).

- [ ] **Step 6: Run full pipeline tests**

Run: `go test ./pipeline/ -v`
Expected: All tests pass.

- [ ] **Step 7: Commit**

```bash
git add pipeline/condition.go pipeline/condition_test.go
git commit -m "fix(condition): support == operator and quoted values (#21)"
```

---

### Task 3: Parallelize consensus pipeline (#26)

**Files:**
- Modify: `examples/consensus_task.dip` (rewrite to use parallel/fan_in)
- Reference: `examples/consensus_task_parity.dip` (existing parallel version to match pattern)

- [ ] **Step 1: Rewrite consensus_task.dip**

Replace the entire file `examples/consensus_task.dip` with the parallel structure. The node definitions stay largely the same — the changes are:
1. Add `parallel` and `fan_in` nodes for three phases (DoD, Plan, Review)
2. Replace sequential edges with parallel fan-out/fan-in edges
3. Update the retry loop to target `PlanParallel` instead of `PlanGemini`

Write the following to `examples/consensus_task.dip`:

```
workflow ConsensusTask
  goal: "Produce a validated multi-model implementation plan and review consensus for the requested task."
  start: Start
  exit: Exit

  defaults
    max_retries: 3
    fidelity: truncate

  agent Start
    label: Start

  agent Exit
    label: Exit

  agent RefineDoD
    label: "Refine DoD Gate"
    model: claude-sonnet-4-6
    provider: anthropic
    reasoning_effort: high
    prompt:
      Refine the definition-of-done requirements for the current task and identify missing acceptance criteria.

  # ─── Phase 1: Multi-model DoD (parallel) ───────────────────

  parallel DoDParallel -> DefineDoDGemini, DefineDoDGPT, DefineDoDOpus

  agent DefineDoDGemini
    label: "Refine DoD (Gemini)"
    model: gemini-3-flash-preview
    provider: gemini
    reasoning_effort: high
    prompt:
      Propose a complete DoD draft and assumptions for this task.

  agent DefineDoDGPT
    label: "Refine DoD (GPT-5.2)"
    model: gpt-5.2
    provider: openai
    reasoning_effort: high
    prompt:
      Propose a complete DoD draft with measurable verification criteria.

  agent DefineDoDOpus
    label: "Refine DoD (Opus)"
    model: claude-sonnet-4-6
    provider: anthropic
    reasoning_effort: high
    prompt:
      Propose a complete DoD draft with risk-based prioritization.

  fan_in DoDJoin <- DefineDoDGemini, DefineDoDGPT, DefineDoDOpus

  agent ConsolidateDoD
    label: "Consolidate DoD"
    model: claude-sonnet-4-6
    provider: anthropic
    reasoning_effort: high
    prompt:
      Merge DoD drafts into a single final DoD.

  # ─── Phase 2: Multi-model Planning (parallel) ──────────────

  parallel PlanParallel -> PlanGemini, PlanGPT, PlanOpus

  agent PlanGemini
    label: "Plan (Gemini)"
    model: gemini-3-flash-preview
    provider: gemini
    reasoning_effort: high
    prompt:
      Create an implementation plan from the consolidated DoD.

  agent PlanGPT
    label: "Plan (GPT-5.2)"
    model: gpt-5.2
    provider: openai
    reasoning_effort: high
    prompt:
      Create an implementation plan from the consolidated DoD.

  agent PlanOpus
    label: "Plan (Opus)"
    model: claude-sonnet-4-6
    provider: anthropic
    reasoning_effort: high
    prompt:
      Create an implementation plan from the consolidated DoD.

  fan_in PlanJoin <- PlanGemini, PlanGPT, PlanOpus

  agent DebateConsolidate
    label: "Debate and Consolidate Plans"
    model: claude-opus-4-6
    provider: anthropic
    reasoning_effort: high
    prompt:
      Synthesize the three plans into one final execution plan.

  # ─── Phase 3: Implement & Verify (sequential) ──────────────

  agent Implement
    label: Implement
    model: claude-sonnet-4-6
    provider: anthropic
    reasoning_effort: high
    prompt:
      Execute the final plan and summarize what was implemented.

  agent VerifyOutputs
    label: "Verify Outputs"
    model: claude-haiku-4-5
    provider: anthropic
    reasoning_effort: high
    prompt:
      Verify outputs against DoD and summarize evidence.

  # ─── Phase 4: Multi-model Review (parallel) ────────────────

  parallel ReviewParallel -> ReviewGemini, ReviewGPT, ReviewOpus

  agent ReviewGemini
    label: "Review (Gemini)"
    model: gemini-3-flash-preview
    provider: gemini
    reasoning_effort: high
    prompt:
      Review implementation and verification evidence.

  agent ReviewGPT
    label: "Review (GPT-5.2)"
    model: gpt-5.2
    provider: openai
    reasoning_effort: high
    prompt:
      Review implementation and verification evidence.

  agent ReviewOpus
    label: "Review (Opus)"
    model: claude-sonnet-4-6
    provider: anthropic
    reasoning_effort: high
    prompt:
      Review implementation and verification evidence.

  fan_in ReviewJoin <- ReviewGemini, ReviewGPT, ReviewOpus

  agent ReviewConsensus
    label: "Review Consensus"
    model: claude-opus-4-6
    provider: anthropic
    reasoning_effort: high
    max_retries: 1
    retry_target: Implement
    prompt:
      Produce final consensus verdict. Use success when ready to exit, retry when rework is required, fail when blocked.

  agent Postmortem
    label: Postmortem
    model: claude-haiku-4-5
    provider: anthropic
    reasoning_effort: high
    prompt:
      Write a postmortem describing why consensus failed and what should change in the next loop.

  edges
    Start -> RefineDoD
    RefineDoD -> DoDParallel
    DoDParallel -> DefineDoDGemini
    DoDParallel -> DefineDoDGPT
    DoDParallel -> DefineDoDOpus
    DefineDoDGemini -> DoDJoin
    DefineDoDGPT -> DoDJoin
    DefineDoDOpus -> DoDJoin
    DoDJoin -> ConsolidateDoD
    ConsolidateDoD -> PlanParallel
    PlanParallel -> PlanGemini
    PlanParallel -> PlanGPT
    PlanParallel -> PlanOpus
    PlanGemini -> PlanJoin
    PlanGPT -> PlanJoin
    PlanOpus -> PlanJoin
    PlanJoin -> DebateConsolidate
    DebateConsolidate -> Implement
    Implement -> VerifyOutputs
    VerifyOutputs -> ReviewParallel
    ReviewParallel -> ReviewGemini
    ReviewParallel -> ReviewGPT
    ReviewParallel -> ReviewOpus
    ReviewGemini -> ReviewJoin
    ReviewGPT -> ReviewJoin
    ReviewOpus -> ReviewJoin
    ReviewJoin -> ReviewConsensus
    ReviewConsensus -> Exit  when ctx.outcome = success  label: pass
    ReviewConsensus -> Postmortem  when ctx.outcome = retry  label: retry
    ReviewConsensus -> Exit
    Postmortem -> PlanParallel  when ctx.internal.loop_restart_count = 0  label: retry_once  restart: true
    Postmortem -> Exit  when ctx.internal.loop_restart_count != 0  label: stop_after_retry
    Postmortem -> Exit  label: "fallback to exit"
```

- [ ] **Step 2: Validate with dippin doctor**

Run: `dippin doctor examples/consensus_task.dip`
Expected: A grade (or at least no errors).

- [ ] **Step 3: Sync embedded workflows**

If `consensus_task.dip` is an embedded workflow, run the sync check:

Run: `make sync-workflows 2>/dev/null || true`

Check if the file needs to be copied into an embedded directory. Look at the pre-commit hook or Makefile for sync commands.

- [ ] **Step 4: Run full test suite**

Run: `go build ./... && go test ./... -short`
Expected: All 14 packages pass. If embed tests reference the old structure, update them.

- [ ] **Step 5: Commit**

```bash
git add examples/consensus_task.dip
git commit -m "fix(pipeline): parallelize consensus_task.dip multi-model phases (#26)"
```

---

## Task Dependency Graph

```text
Task 1 (per-node context keys — independent)
Task 2 (condition parser hardening — independent)
Task 3 (consensus pipeline parallelization — independent of 1 & 2 at the .dip level;
         benefits from Task 1 at runtime but doesn't require code changes from it)
```

All three tasks are independent and can be implemented in any order.
