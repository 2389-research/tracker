# StrongDM Parity Implementation Plan

> **For Claude:** REQUIRED SUB-SKILL: Use superpowers:executing-plans to implement this plan task-by-task.

**Goal:** Bring `tracker` to strict parity with StrongDM's published `attractor`, `coding-agent-loop`, and `unified-llm` specs.

**Architecture:** Use a spec-first parity program. Add failing conformance tests that encode upstream requirements, then change the local pipeline, agent, and LLM layers in small slices until those tests pass. Keep package boundaries where they already fit, but replace local behavior anywhere it diverges from the published StrongDM semantics.

**Tech Stack:** Go, `testing`, existing package test suites under `pipeline/`, `pipeline/handlers/`, `agent/`, `agent/tools/`, `llm/`, provider adapters in `llm/anthropic`, `llm/openai`, and `llm/google`

---

### Task 1: Build the Parity Matrix

**Files:**
- Create: `docs/plans/2026-03-06-strongdm-parity-matrix.md`
- Modify: `docs/plans/2026-03-06-strongdm-parity-design.md`
- Test: none

**Step 1: Write the matrix skeleton**

```md
| Layer | Spec section | Requirement | Local file(s) | Test file | Status |
|-------|--------------|-------------|---------------|-----------|--------|
| pipeline | 2.6 | node `type` overrides `shape` | pipeline/graph.go | pipeline/parity_attractor_test.go | TODO |
```

**Step 2: Save at least the known gaps first**

Include rows for:
- goal-gate exit enforcement
- `type` override
- `house -> stack.manager_loop`
- `context.*` conditions
- stylesheet shape selectors
- comma-separated classes
- codergen multi-turn tool loop
- stage artifacts

**Step 3: Review the matrix for missing high-risk sections**

Run: `sed -n '1,220p' docs/plans/2026-03-06-strongdm-parity-matrix.md`
Expected: visible rows for all three layers, not just `pipeline/`

**Step 4: Commit**

```bash
git add docs/plans/2026-03-06-strongdm-parity-design.md docs/plans/2026-03-06-strongdm-parity-matrix.md
git commit -m "docs: add StrongDM parity matrix"
```

### Task 2: Add Attractor Parity Test Harness

**Files:**
- Create: `pipeline/parity_attractor_test.go`
- Modify: `pipeline/graph_test.go`
- Modify: `pipeline/validate_test.go`
- Modify: `pipeline/engine_test.go`
- Modify: `pipeline/stylesheet_test.go`
- Test: `pipeline/parity_attractor_test.go`

**Step 1: Write the failing parity tests**

```go
func TestNodeTypeOverridesShape(t *testing.T) {
	graph, err := ParseDOT(`
digraph g {
	start [shape=Mdiamond]
	a [shape=box, type="tool", tool_command="echo hi"]
	done [shape=Msquare]
	start -> a -> done
}`)
	if err != nil {
		t.Fatal(err)
	}
	if got := graph.Nodes["a"].Handler; got != "tool" {
		t.Fatalf("handler=%q want tool", got)
	}
}
```

Add tests for:
- `type` override
- `house` shape mapping
- `context.*` resolution
- shape selectors in stylesheet
- comma-separated classes
- exit-time goal gate enforcement
- graph-level retry fallback on unsatisfied exit

**Step 2: Run the narrow parity test target**

Run: `go test ./pipeline -run 'Test(NodeTypeOverridesShape|GoalGate|ConditionContext|Stylesheet)' -count=1`
Expected: FAIL with current implementation

**Step 3: Add helper fixtures only if repeated setup is noisy**

```go
func mustParseGraph(t *testing.T, dot string) *Graph
```

**Step 4: Re-run the same target**

Run: `go test ./pipeline -run 'Test(NodeTypeOverridesShape|GoalGate|ConditionContext|Stylesheet)' -count=1`
Expected: still FAIL, but now all target mismatches are encoded as tests

**Step 5: Commit**

```bash
git add pipeline/parity_attractor_test.go pipeline/graph_test.go pipeline/validate_test.go pipeline/engine_test.go pipeline/stylesheet_test.go
git commit -m "test: add Attractor parity coverage"
```

### Task 3: Fix Pipeline Handler Resolution and DSL Semantics

**Files:**
- Modify: `pipeline/graph.go`
- Modify: `pipeline/parser.go`
- Modify: `pipeline/validate.go`
- Modify: `pipeline/validate_semantic.go`
- Test: `pipeline/parity_attractor_test.go`

**Step 1: Write the failing validation cases if still missing**

```go
func TestHouseShapeMapsToManagerLoop(t *testing.T) {}
func TestExitNodeHasNoOutgoingEdges(t *testing.T) {}
```

**Step 2: Implement minimal graph semantics**

```go
if typ, ok := n.Attrs["type"]; ok && typ != "" {
	n.Handler = typ
} else if handler, ok := ShapeToHandler(n.Shape); ok {
	n.Handler = handler
}
```

Add the missing shape mapping:

```go
"house": "stack.manager_loop",
```

**Step 3: Tighten validation to match the spec**

Add checks for:
- exit node has no outgoing edges
- retry targets reference existing nodes
- goal-gated nodes without retry targets produce warnings or equivalent lint output if you keep warning support separate

**Step 4: Run targeted pipeline tests**

Run: `go test ./pipeline -run 'Test(NodeTypeOverridesShape|HouseShapeMapsToManagerLoop|ExitNode)' -count=1`
Expected: PASS

**Step 5: Commit**

```bash
git add pipeline/graph.go pipeline/parser.go pipeline/validate.go pipeline/validate_semantic.go pipeline/parity_attractor_test.go
git commit -m "fix: align pipeline handler resolution with Attractor"
```

### Task 4: Fix Condition Language and Stylesheet Semantics

**Files:**
- Modify: `pipeline/condition.go`
- Modify: `pipeline/validate_semantic.go`
- Modify: `pipeline/stylesheet.go`
- Modify: `pipeline/context.go`
- Test: `pipeline/condition_test.go`
- Test: `pipeline/stylesheet_test.go`
- Test: `pipeline/parity_attractor_test.go`

**Step 1: Write the failing tests**

```go
func TestEvaluateCondition_ContextPrefix(t *testing.T) {
	ctx := NewPipelineContext()
	ctx.Set("tests_passed", "true")
	ok, err := EvaluateCondition("context.tests_passed=true", ctx)
	if err != nil {
		t.Fatal(err)
	}
	if !ok {
		t.Fatal("expected condition to pass")
	}
}
```

```go
func TestStylesheetShapeSelectorAndCommaSeparatedClasses(t *testing.T) {}
```

**Step 2: Implement minimal condition resolution**

```go
if strings.HasPrefix(name, "context.") {
	if v, ok := ctx.Get(strings.TrimPrefix(name, "context.")); ok {
		return v
	}
}
```

Normalize preferred-label matching only if the parity tests prove it is missing.

**Step 3: Implement stylesheet selector parity**

Support selectors in this precedence:
- `*`
- shape selector like `box`
- class selector like `.critical`
- id selector like `#review`

Parse node classes as comma-separated first, then trim whitespace.

**Step 4: Run the focused test targets**

Run: `go test ./pipeline -run 'TestEvaluateCondition|TestStylesheet|TestConditionContext' -count=1`
Expected: PASS

**Step 5: Commit**

```bash
git add pipeline/condition.go pipeline/validate_semantic.go pipeline/stylesheet.go pipeline/context.go pipeline/condition_test.go pipeline/stylesheet_test.go pipeline/parity_attractor_test.go
git commit -m "fix: align conditions and stylesheets with Attractor"
```

### Task 5: Fix Engine Exit, Retry, Goal-Gate, and Checkpoint Semantics

**Files:**
- Modify: `pipeline/engine.go`
- Modify: `pipeline/checkpoint.go`
- Modify: `pipeline/trace.go`
- Test: `pipeline/engine_test.go`
- Test: `pipeline/checkpoint_test.go`
- Test: `pipeline/parity_attractor_test.go`

**Step 1: Add failing engine tests**

```go
func TestExitChecksGoalGatesBeforeCompleting(t *testing.T) {}
func TestUnsatisfiedGoalGateUsesRetryTargetBeforeFailing(t *testing.T) {}
func TestCheckpointResumeRestoresCurrentNodeAndContext(t *testing.T) {}
```

**Step 2: Implement exit-time goal-gate enforcement**

Before finalizing `Msquare`:
- inspect visited goal-gated nodes
- accept `success` and `partial_success`
- route to node-level retry target, then node-level fallback
- then graph-level retry target, then graph-level fallback
- fail only if no valid retry path exists

**Step 3: Keep retry accounting and checkpoint semantics coherent**

Make sure the resumed run sees:
- `CurrentNode`
- retry counts
- restored context
- the same downstream clear-and-replay behavior as a fresh run

**Step 4: Run the engine-focused tests**

Run: `go test ./pipeline -run 'Test(ExitChecksGoalGatesBeforeCompleting|UnsatisfiedGoalGate|CheckpointResume)' -count=1`
Expected: PASS

**Step 5: Commit**

```bash
git add pipeline/engine.go pipeline/checkpoint.go pipeline/trace.go pipeline/engine_test.go pipeline/checkpoint_test.go pipeline/parity_attractor_test.go
git commit -m "fix: align pipeline execution semantics with Attractor"
```

### Task 6: Add Stage Artifacts, Status Contract, and Transforms

**Files:**
- Create: `pipeline/artifacts.go`
- Create: `pipeline/transforms.go`
- Modify: `pipeline/engine.go`
- Modify: `pipeline/handler.go`
- Modify: `pipeline/handlers/codergen.go`
- Modify: `pipeline/handlers/tool.go`
- Test: `pipeline/engine_test.go`
- Test: `pipeline/handlers/codergen_test.go`
- Test: `pipeline/handlers/tool_test.go`

**Step 1: Add failing artifact tests**

```go
func TestCodergenWritesPromptResponseAndStatusArtifacts(t *testing.T) {}
func TestToolHandlerWritesStatusArtifact(t *testing.T) {}
func TestGoalVariableExpansionUsesGraphGoal(t *testing.T) {}
```

**Step 2: Implement a small artifact writer**

Write per-stage files under a deterministic run directory:
- `prompt.md`
- `response.md`
- `status.json`

Use a status payload shaped like:

```json
{
  "outcome": "success",
  "preferred_next_label": "",
  "suggested_next_ids": [],
  "context_updates": {}
}
```

**Step 3: Implement graph-level variable expansion**

Expand `$goal` in node prompts using `graph.goal`, and mirror graph attributes into runtime context before execution starts.

**Step 4: Run focused tests**

Run: `go test ./pipeline ./pipeline/handlers -run 'Test(CodergenWritesPromptResponseAndStatusArtifacts|ToolHandlerWritesStatusArtifact|GoalVariableExpansionUsesGraphGoal)' -count=1`
Expected: PASS

**Step 5: Commit**

```bash
git add pipeline/artifacts.go pipeline/transforms.go pipeline/engine.go pipeline/handler.go pipeline/handlers/codergen.go pipeline/handlers/tool.go pipeline/engine_test.go pipeline/handlers/codergen_test.go pipeline/handlers/tool_test.go
git commit -m "feat: add Attractor stage artifacts and transforms"
```

### Task 7: Complete Missing Attractor Handlers and Interviewer Variants

**Files:**
- Modify: `pipeline/handlers/registry.go`
- Modify: `pipeline/handlers/human.go`
- Create: `pipeline/handlers/manager_loop.go`
- Create: `pipeline/handlers/interviewer_test.go`
- Modify: `pipeline/handlers/human_test.go`
- Modify: `pipeline/handlers/integration_test.go`
- Test: `pipeline/handlers/*.go`

**Step 1: Add failing tests for missing surface**

```go
func TestAutoApproveInterviewerSelectsFirstChoice(t *testing.T) {}
func TestQueueInterviewerReturnsQueuedAnswers(t *testing.T) {}
func TestManagerLoopHandlerIsRegistered(t *testing.T) {}
```

**Step 2: Implement missing interviewer variants and manager loop stub**

At minimum add:
- `CallbackInterviewer`
- `QueueInterviewer`
- `stack.manager_loop` handler wired through the registry

Keep the handler minimal but spec-conformant before adding richer behavior.

**Step 3: Re-run handler tests**

Run: `go test ./pipeline/handlers -count=1`
Expected: PASS

**Step 4: Commit**

```bash
git add pipeline/handlers/registry.go pipeline/handlers/human.go pipeline/handlers/manager_loop.go pipeline/handlers/interviewer_test.go pipeline/handlers/human_test.go pipeline/handlers/integration_test.go
git commit -m "feat: complete Attractor handler surface"
```

### Task 8: Add Coding-Agent Parity Tests

**Files:**
- Create: `agent/parity_coding_agent_test.go`
- Modify: `agent/session_test.go`
- Modify: `agent/events_test.go`
- Modify: `agent/result_test.go`
- Modify: `agent/tools/registry_test.go`
- Test: `agent/parity_coding_agent_test.go`

**Step 1: Write the failing session tests**

```go
func TestSessionRunsToolLoopUntilNaturalCompletion(t *testing.T) {}
func TestUnknownToolReturnsErrorResultNotSessionFailure(t *testing.T) {}
func TestLoopDetectionEmitsWarningAndStopsProgress(t *testing.T) {}
func TestSteeringIsInjectedBetweenToolRounds(t *testing.T) {}
```

**Step 2: Add provider-profile expectations as tests**

The parity file should prove:
- tool definitions are profile-driven, not one universal set
- OpenAI-aligned editing uses the expected patch tool surface
- Anthropic-aligned editing uses the expected edit tool surface
- Gemini profile behavior is explicitly tested

**Step 3: Run the targeted agent tests**

Run: `go test ./agent ./agent/tools -run 'Test(SessionRunsToolLoopUntilNaturalCompletion|UnknownTool|LoopDetection|Steering|ProviderProfile)' -count=1`
Expected: FAIL with current behavior where parity is missing

**Step 4: Commit**

```bash
git add agent/parity_coding_agent_test.go agent/session_test.go agent/events_test.go agent/result_test.go agent/tools/registry_test.go
git commit -m "test: add coding-agent parity coverage"
```

### Task 9: Fix Codergen-to-Agent Integration and Agent Session Semantics

**Files:**
- Modify: `pipeline/handlers/codergen.go`
- Modify: `pipeline/handlers/registry.go`
- Modify: `agent/session.go`
- Modify: `agent/config.go`
- Modify: `agent/result.go`
- Modify: `agent/events.go`
- Modify: `agent/tools/registry.go`
- Test: `agent/parity_coding_agent_test.go`
- Test: `pipeline/handlers/codergen_test.go`

**Step 1: Add or keep the failing codergen integration tests**

```go
func TestCodergenUsesExecutionEnvironmentAndTools(t *testing.T) {}
func TestCodergenDoesNotForceSingleTurnForSpecParity(t *testing.T) {}
```

**Step 2: Implement minimal parity changes**

- pass an execution environment into the session used by `codergen`
- stop forcing `MaxTurns = 1`
- make tool execution and tool-result flow spec-aligned before optimizing
- preserve event coverage so the pipeline layer can capture outputs

**Step 3: Tighten truncation and error-result behavior only where tests show drift**

Prefer changing:
- session loop behavior
- tool registry execution contract
- event emission timing

before adding new complexity.

**Step 4: Run focused tests**

Run: `go test ./agent ./pipeline/handlers -run 'Test(CodergenUsesExecutionEnvironmentAndTools|CodergenDoesNotForceSingleTurnForSpecParity|SessionRunsToolLoopUntilNaturalCompletion)' -count=1`
Expected: PASS

**Step 5: Commit**

```bash
git add pipeline/handlers/codergen.go pipeline/handlers/registry.go agent/session.go agent/config.go agent/result.go agent/events.go agent/tools/registry.go agent/parity_coding_agent_test.go pipeline/handlers/codergen_test.go
git commit -m "fix: align codergen and agent loop behavior"
```

### Task 10: Add Unified LLM Parity Tests

**Files:**
- Create: `llm/parity_unified_llm_test.go`
- Modify: `llm/client_test.go`
- Modify: `llm/stream_test.go`
- Modify: `llm/transform_test.go`
- Modify: `llm/errors_test.go`
- Test: `llm/parity_unified_llm_test.go`

**Step 1: Write the failing LLM parity tests**

```go
func TestClientStreamReturnsResolutionErrorEvent(t *testing.T) {}
func TestCompleteSetsProviderAndLatency(t *testing.T) {}
func TestRequestAndResponseToolCallsRoundTripAcrossAdapters(t *testing.T) {}
```

Add coverage for:
- finish-reason normalization
- usage totals and cache fields
- provider resolution and default-provider behavior
- retryable vs terminal error mapping
- streaming event ordering and terminal events

**Step 2: Run the focused LLM tests**

Run: `go test ./llm ./llm/anthropic ./llm/openai ./llm/google -run 'Test(ClientStreamReturnsResolutionErrorEvent|CompleteSetsProviderAndLatency|RequestAndResponseToolCallsRoundTripAcrossAdapters|FinishReason|Usage|Retry)' -count=1`
Expected: FAIL where parity is still missing

**Step 3: Commit**

```bash
git add llm/parity_unified_llm_test.go llm/client_test.go llm/stream_test.go llm/transform_test.go llm/errors_test.go
git commit -m "test: add unified LLM parity coverage"
```

### Task 11: Fix Unified LLM Client and Adapter Semantics

**Files:**
- Modify: `llm/client.go`
- Modify: `llm/types.go`
- Modify: `llm/stream.go`
- Modify: `llm/retry.go`
- Modify: `llm/errors.go`
- Modify: `llm/anthropic/translate.go`
- Modify: `llm/openai/translate.go`
- Modify: `llm/google/translate.go`
- Test: `llm/parity_unified_llm_test.go`

**Step 1: Implement the minimum changes required by the failing tests**

Example targets:

```go
resp.Provider = adapter.Name()
resp.Latency = time.Since(start)
```

and any missing finish-reason or usage normalization.

**Step 2: Keep adapter tests provider-local**

Do not hide provider-specific translation fixes inside generic tests. Put adapter-specific assertions in:
- `llm/anthropic/*_test.go`
- `llm/openai/*_test.go`
- `llm/google/*_test.go`

**Step 3: Run the LLM suite**

Run: `go test ./llm ./llm/anthropic ./llm/openai ./llm/google -count=1`
Expected: PASS

**Step 4: Commit**

```bash
git add llm/client.go llm/types.go llm/stream.go llm/retry.go llm/errors.go llm/anthropic/translate.go llm/openai/translate.go llm/google/translate.go llm/parity_unified_llm_test.go
git commit -m "fix: align unified LLM behavior with spec"
```

### Task 12: Run End-to-End Conformance and Tighten CLI Coverage

**Files:**
- Modify: `cmd/tracker/main.go`
- Modify: `cmd/tracker-conformance/main.go`
- Modify: `cmd/tracker-conformance/main_test.go`
- Modify: `pipeline/handlers/integration_test.go`
- Modify: `agent/integration_test.go`
- Modify: `llm/integration_test.go`
- Modify: `docs/plans/2026-03-06-strongdm-parity-matrix.md`
- Test: all existing package suites

**Step 1: Add one end-to-end parity smoke test per layer**

Examples:
- pipeline smoke test from upstream plan/implement/review flow
- agent smoke test proving multi-turn tool use
- LLM smoke test proving translation and normalization

**Step 2: Run the full repo**

Run: `go test ./... -count=1`
Expected: PASS

**Step 3: Update the matrix**

Change each completed row from `TODO` to `PASS` or a clearly named residual gap if something is intentionally deferred.

**Step 4: Commit**

```bash
git add cmd/tracker/main.go cmd/tracker-conformance/main.go cmd/tracker-conformance/main_test.go pipeline/handlers/integration_test.go agent/integration_test.go llm/integration_test.go docs/plans/2026-03-06-strongdm-parity-matrix.md
git commit -m "test: run StrongDM parity conformance end to end"
```

### Task 13: Final Verification Before Claiming Parity

**Files:**
- Modify: `docs/plans/2026-03-06-strongdm-parity-matrix.md`
- Test: full repo verification only

**Step 1: Run the full verification set**

Run: `go test ./... -count=1`
Expected: PASS

Run: `go test ./pipeline -run Parity -count=1`
Expected: PASS

Run: `go test ./agent -run Parity -count=1`
Expected: PASS

Run: `go test ./llm -run Parity -count=1`
Expected: PASS

**Step 2: Record the evidence**

Add a short verification block to the matrix:

```md
## Verification
- `go test ./... -count=1` on 2026-03-06
- parity subsets for `pipeline`, `agent`, and `llm`
```

**Step 3: Commit**

```bash
git add docs/plans/2026-03-06-strongdm-parity-matrix.md
git commit -m "docs: record StrongDM parity verification"
```
