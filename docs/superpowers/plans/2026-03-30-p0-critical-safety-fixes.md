# P0 Critical Safety Fixes Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Fix three P0 bugs (parallel context loss, unbounded goal-gate retry, tainted tool_command interpolation), make checkpoint writes atomic, and add a global circuit breaker.

**Architecture:** Four independent fixes plus one cross-cutting circuit breaker. Each fix is self-contained — commit after each task. The tool_command fix gates both variable expansion systems (`ExpandVariables` and `ExpandGraphVariables`) with a hardcoded tainted-key set rather than runtime taint tracking.

**Tech Stack:** Go 1.24, standard library only. No new dependencies.

**Spec:** `docs/superpowers/specs/2026-03-30-p0-critical-safety-fixes-design.md`

---

### Task 1: Commit existing parallel context fix (#20) and goal-gate retry fix (#15)

The branch already has uncommitted, working code for these two fixes. Verify it compiles and passes tests, then commit.

**Files:**
- Already modified: `pipeline/context.go`, `pipeline/context_test.go`, `pipeline/engine_checkpoint.go`, `pipeline/engine_run.go`, `pipeline/handlers/parallel.go`, `pipeline/handlers/parallel_test.go`
- Already created: `pipeline/engine_goal_gate_test.go`

- [ ] **Step 1: Verify the build compiles**

Run: `go build ./...`
Expected: Clean build, no errors.

- [ ] **Step 2: Run all tests**

Run: `go test ./... -short -count=1`
Expected: All packages pass. Pay attention to `pipeline` and `pipeline/handlers` packages.

- [ ] **Step 3: Commit the existing work**

```bash
git add pipeline/context.go pipeline/context_test.go pipeline/engine_checkpoint.go pipeline/engine_run.go pipeline/handlers/parallel.go pipeline/handlers/parallel_test.go pipeline/engine_goal_gate_test.go
git commit -m "fix: parallel context side-effect capture (#20) and goal-gate retry budget (#15)

DiffFrom() captures pctx.Set() side effects in parallel branches.
goalGateRetryTarget() checks retry budget before allowing loops.
Emits EventStageRetrying/EventStageFailed with attempt counts."
```

---

### Task 2: Fix the escalation loop bug in goal-gate retry

When retries exhaust and the fallback path is taken, `goalGateRetryTarget` returns `retry=true`. If the escalation node also fails, the pipeline bounces between exit and escalation forever. Fix by tracking `FallbackTaken` in the checkpoint.

**Files:**
- Modify: `pipeline/checkpoint.go` (add `FallbackTaken` field)
- Modify: `pipeline/engine_checkpoint.go:151-202` (check `FallbackTaken` before returning fallback)
- Test: `pipeline/engine_goal_gate_test.go` (add escalation-fails test)

- [ ] **Step 1: Write the failing test — escalation also fails terminates**

Add to `pipeline/engine_goal_gate_test.go`:

```go
func TestGoalGateEscalationAlsoFailsTerminates(t *testing.T) {
	g := NewGraph("goal-gate-escalation-loop")

	g.AddNode(&Node{ID: "start", Shape: "Mdiamond"})
	g.AddNode(&Node{ID: "work", Shape: "box", Attrs: map[string]string{
		"goal_gate":       "true",
		"retry_target":    "repair",
		"fallback_target": "escalate",
		"max_retries":     "1",
	}})
	g.AddNode(&Node{ID: "repair", Shape: "box"})
	g.AddNode(&Node{ID: "escalate", Shape: "box"})
	g.AddNode(&Node{ID: "done", Shape: "Msquare"})

	g.AddEdge(&Edge{From: "start", To: "work"})
	g.AddEdge(&Edge{From: "work", To: "done", Condition: "ctx.outcome = success"})
	g.AddEdge(&Edge{From: "work", To: "done", Condition: "ctx.outcome = fail"})
	g.AddEdge(&Edge{From: "repair", To: "work"})
	g.AddEdge(&Edge{From: "escalate", To: "done"})

	reg := newTestRegistry()
	escalateCount := 0
	reg.Register(&testHandler{
		name: "codergen",
		executeFn: func(ctx context.Context, node *Node, pctx *PipelineContext) (Outcome, error) {
			switch node.ID {
			case "escalate":
				escalateCount++
				return Outcome{Status: OutcomeFail}, nil // escalation also fails
			default:
				return Outcome{Status: OutcomeFail}, nil // everything fails
			}
		},
	})

	engine := NewEngine(g, reg)
	result, err := engine.Run(context.Background())
	if err != nil {
		t.Fatalf("engine.Run error: %v", err)
	}

	if result.Status != OutcomeFail {
		t.Fatalf("status = %q, want %q", result.Status, OutcomeFail)
	}
	// Escalation should be attempted exactly once, not loop.
	if escalateCount != 1 {
		t.Fatalf("escalate visited %d times, want exactly 1", escalateCount)
	}
}
```

- [ ] **Step 2: Run test to verify it fails (infinite loop or wrong count)**

Run: `go test ./pipeline -run TestGoalGateEscalationAlsoFailsTerminates -timeout 10s -v`
Expected: FAIL — either times out (infinite loop) or `escalateCount` is wrong.

- [ ] **Step 3: Add `FallbackTaken` to Checkpoint**

In `pipeline/checkpoint.go`, add the field to the `Checkpoint` struct:

```go
type Checkpoint struct {
	RunID          string            `json:"run_id"`
	CurrentNode    string            `json:"current_node"`
	CompletedNodes []string          `json:"completed_nodes"`
	RetryCounts    map[string]int    `json:"retry_counts"`
	FallbackTaken  map[string]bool   `json:"fallback_taken,omitempty"`
	Context        map[string]string `json:"context"`
	Timestamp      time.Time         `json:"timestamp"`
	RestartCount   int               `json:"restart_count"`
	EdgeSelections map[string]string `json:"edge_selections,omitempty"`
	completedSet   map[string]bool   `json:"-"`
}
```

- [ ] **Step 4: Gate the fallback path in `goalGateRetryTarget`**

In `pipeline/engine_checkpoint.go`, in the retries-exhausted branch (line ~162-181), check `FallbackTaken` before returning a fallback:

```go
		// Check retry budget before allowing another loop.
		maxR := e.maxRetries(node)
		if cp.RetryCount(nodeID) >= maxR {
			// Already took the fallback for this gate? No more retries.
			if cp.FallbackTaken != nil && cp.FallbackTaken[nodeID] {
				return "", nodeID, false, true
			}
			// Retries exhausted — look for a fallback/escalation target.
			for _, fb := range []string{
				node.Attrs["fallback_target"],
				node.Attrs["fallback_retry_target"],
				e.graph.Attrs["fallback_target"],
				e.graph.Attrs["fallback_retry_target"],
			} {
				if fb == "" {
					continue
				}
				if _, ok := e.graph.Nodes[fb]; ok {
					// Mark fallback as taken so we don't loop.
					if cp.FallbackTaken == nil {
						cp.FallbackTaken = make(map[string]bool)
					}
					cp.FallbackTaken[nodeID] = true
					return fb, nodeID, true, true
				}
			}
			// No fallback — signal unsatisfied without retry.
			return "", nodeID, false, true
		}
```

- [ ] **Step 5: Run test to verify it passes**

Run: `go test ./pipeline -run TestGoalGateEscalationAlsoFailsTerminates -timeout 10s -v`
Expected: PASS — escalation visited once, pipeline fails.

- [ ] **Step 6: Also fix the existing test to assert result status**

Update `TestGoalGateRetryFallsBackToFallbackTarget` in `pipeline/engine_goal_gate_test.go` to replace the ignored result/err:

```go
	engine := NewEngine(g, reg)
	result, err := engine.Run(context.Background())
	if err != nil {
		t.Fatalf("engine.Run error: %v", err)
	}

	if !escalateVisited {
		t.Fatal("expected escalate node to be visited after retries exhausted")
	}
	// Escalation node succeeds and routes to done. But the goal gate (work)
	// is still unsatisfied. With FallbackTaken, the pipeline should fail.
	if result.Status != OutcomeFail {
		t.Fatalf("status = %q, want %q (goal gate still unsatisfied after escalation)", result.Status, OutcomeFail)
	}
```

- [ ] **Step 7: Run all goal-gate tests**

Run: `go test ./pipeline -run TestGoalGate -v`
Expected: All 5 tests pass.

- [ ] **Step 8: Run full test suite**

Run: `go test ./... -short -count=1`
Expected: All packages pass.

- [ ] **Step 9: Commit**

```bash
git add pipeline/checkpoint.go pipeline/engine_checkpoint.go pipeline/engine_goal_gate_test.go
git commit -m "fix: prevent escalation loop in goal-gate retry (#15)

FallbackTaken map in Checkpoint tracks whether fallback was already
attempted for each gate. Second pass through exhausted gate with
fallback_taken=true terminates the pipeline instead of looping."
```

---

### Task 3: Add global engine circuit breaker

Hard cap on total engine loop iterations (10,000) to catch any unbounded loop we haven't found yet.

**Files:**
- Modify: `pipeline/events.go` (add `EventCircuitBreaker` constant)
- Modify: `pipeline/engine.go:127-142` (add iteration counter and check)
- Test: `pipeline/engine_test.go` (add circuit breaker test)

- [ ] **Step 1: Write the failing test**

Add to `pipeline/engine_test.go`:

```go
func TestEngineCircuitBreakerTerminatesInfiniteLoop(t *testing.T) {
	// Build a graph with an unconditional cycle that never triggers
	// the restart counter (clearDownstream removes completed status).
	g := NewGraph("circuit-breaker")
	g.AddNode(&Node{ID: "start", Shape: "Mdiamond"})
	g.AddNode(&Node{ID: "loop_a", Shape: "box"})
	g.AddNode(&Node{ID: "loop_b", Shape: "box"})
	g.AddNode(&Node{ID: "done", Shape: "Msquare"})

	g.AddEdge(&Edge{From: "start", To: "loop_a"})
	g.AddEdge(&Edge{From: "loop_a", To: "loop_b"})
	g.AddEdge(&Edge{From: "loop_b", To: "loop_a"}) // infinite cycle
	// No edge to done — unreachable.

	reg := newTestRegistry()
	iterations := 0
	reg.Register(&testHandler{
		name: "codergen",
		executeFn: func(ctx context.Context, node *Node, pctx *PipelineContext) (Outcome, error) {
			iterations++
			return Outcome{Status: OutcomeSuccess}, nil
		},
	})

	var events []PipelineEvent
	engine := NewEngine(g, reg, WithEventHandler(PipelineEventHandlerFunc(func(evt PipelineEvent) {
		events = append(events, evt)
	})))

	result, err := engine.Run(context.Background())
	if err != nil {
		t.Fatalf("engine.Run error: %v", err)
	}
	if result.Status != OutcomeFail {
		t.Fatalf("status = %q, want %q", result.Status, OutcomeFail)
	}

	// Verify circuit breaker event was emitted.
	found := false
	for _, evt := range events {
		if evt.Type == EventCircuitBreaker {
			found = true
			break
		}
	}
	if !found {
		t.Fatal("expected EventCircuitBreaker event")
	}
}
```

- [ ] **Step 2: Run test to verify it fails (hangs or times out)**

Run: `go test ./pipeline -run TestEngineCircuitBreakerTerminatesInfiniteLoop -timeout 10s -v`
Expected: FAIL — test times out because the engine loops forever.

- [ ] **Step 3: Add `EventCircuitBreaker` to events.go**

In `pipeline/events.go`, add after `EventEdgeTiebreaker`:

```go
	EventCircuitBreaker PipelineEventType = "circuit_breaker"
```

- [ ] **Step 4: Add the circuit breaker constant and check to engine.go**

In `pipeline/engine.go`, add a constant near the top of the file:

```go
// maxEngineIterations is the hard cap on main loop iterations.
// Not configurable via .dip to prevent pipeline authors from overriding.
const maxEngineIterations = 10000
```

In the `Run` method, add an iteration counter before the `for` loop (after line 125) and a check at the top of the loop body (after the context check at line 128-130):

```go
	resumeVisited := make(map[string]bool)
	iterations := 0

	for {
		if err := ctx.Err(); err != nil {
			return e.cancelledResult(s, err)
		}

		iterations++
		if iterations > maxEngineIterations {
			e.emit(PipelineEvent{
				Type:      EventCircuitBreaker,
				Timestamp: time.Now(),
				RunID:     s.runID,
				Message:   fmt.Sprintf("circuit breaker: exceeded %d iterations", maxEngineIterations),
			})
			e.saveCheckpoint(s.cp, s.pctx, s.runID)
			s.trace.EndTime = time.Now()
			return e.failResult(s.runID, s.cp, s.pctx, s.trace), nil
		}

		lr := e.processNode(ctx, s, currentNodeID, resumeVisited)
```

- [ ] **Step 5: Run test to verify it passes**

Run: `go test ./pipeline -run TestEngineCircuitBreakerTerminatesInfiniteLoop -timeout 30s -v`
Expected: PASS. The loop terminates at 10,000 iterations.

- [ ] **Step 6: Run full test suite**

Run: `go test ./... -short -count=1`
Expected: All packages pass. No existing test hits 10,000 iterations.

- [ ] **Step 7: Commit**

```bash
git add pipeline/events.go pipeline/engine.go pipeline/engine_test.go
git commit -m "fix: add global circuit breaker (10k iteration cap)

Hard cap prevents any unbounded loop from burning API credits.
Emits EventCircuitBreaker with iteration count for diagnostics.
Not configurable via .dip to prevent pipeline author override."
```

---

### Task 4: Make checkpoint writes atomic

Write-to-temp-then-rename prevents crash mid-write from corrupting resume state.

**Files:**
- Modify: `pipeline/checkpoint.go:110-125` (atomic write)
- Test: `pipeline/checkpoint_test.go` (add atomicity tests)

- [ ] **Step 1: Write the failing test — no temp file left behind**

Add to `pipeline/checkpoint_test.go`:

```go
func TestSaveCheckpointAtomicNoTempFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "checkpoint.json")

	cp := &Checkpoint{
		RunID:       "test-atomic",
		CurrentNode: "node1",
		Context:     map[string]string{"key": "value"},
		Timestamp:   time.Now(),
	}

	if err := SaveCheckpoint(cp, path); err != nil {
		t.Fatalf("SaveCheckpoint failed: %v", err)
	}

	// The checkpoint file should exist.
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("checkpoint file missing: %v", err)
	}

	// The temp file should NOT exist after successful save.
	tmpPath := path + ".tmp"
	if _, err := os.Stat(tmpPath); !os.IsNotExist(err) {
		t.Fatalf("temp file %q should not exist after save, got err=%v", tmpPath, err)
	}

	// Verify the checkpoint is valid JSON and round-trips.
	loaded, err := LoadCheckpoint(path)
	if err != nil {
		t.Fatalf("LoadCheckpoint failed: %v", err)
	}
	if loaded.RunID != "test-atomic" {
		t.Fatalf("RunID = %q, want %q", loaded.RunID, "test-atomic")
	}
}

func TestSaveCheckpointFallbackTakenSurvivesRoundTrip(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "checkpoint.json")

	cp := &Checkpoint{
		RunID:         "test-fallback",
		FallbackTaken: map[string]bool{"gate1": true},
		Context:       map[string]string{},
		Timestamp:     time.Now(),
	}

	if err := SaveCheckpoint(cp, path); err != nil {
		t.Fatalf("SaveCheckpoint failed: %v", err)
	}

	loaded, err := LoadCheckpoint(path)
	if err != nil {
		t.Fatalf("LoadCheckpoint failed: %v", err)
	}
	if !loaded.FallbackTaken["gate1"] {
		t.Fatal("FallbackTaken[gate1] should be true after round-trip")
	}
}
```

- [ ] **Step 2: Run tests — FallbackTaken test may fail if field not added yet; atomicity test passes trivially**

Run: `go test ./pipeline -run TestSaveCheckpoint -v`
Expected: `TestSaveCheckpointFallbackTakenSurvivesRoundTrip` passes (field was added in Task 2). `TestSaveCheckpointAtomicNoTempFile` passes trivially (current `WriteFile` leaves no `.tmp` either). Both should pass.

- [ ] **Step 3: Implement atomic write-to-temp-then-rename**

Replace the body of `SaveCheckpoint` in `pipeline/checkpoint.go`:

```go
func SaveCheckpoint(cp *Checkpoint, path string) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return fmt.Errorf("create checkpoint directory: %w", err)
	}

	data, err := json.MarshalIndent(cp, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal checkpoint: %w", err)
	}

	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o600); err != nil {
		return fmt.Errorf("write checkpoint temp file: %w", err)
	}
	if err := os.Rename(tmp, path); err != nil {
		return fmt.Errorf("atomic rename checkpoint: %w", err)
	}
	return nil
}
```

- [ ] **Step 4: Run checkpoint tests**

Run: `go test ./pipeline -run TestSaveCheckpoint -v && go test ./pipeline -run TestLoadCheckpoint -v`
Expected: All pass.

- [ ] **Step 5: Run full test suite**

Run: `go test ./... -short -count=1`
Expected: All packages pass.

- [ ] **Step 6: Commit**

```bash
git add pipeline/checkpoint.go pipeline/checkpoint_test.go
git commit -m "fix: atomic checkpoint writes via write-temp-then-rename

Prevents corrupted checkpoint files on crash/OOM during save.
Also tests FallbackTaken field round-trips through JSON."
```

---

### Task 5: Regression test — prove tool_command injection vulnerability exists

Write the test BEFORE the fix. This test passes today (proving the vulnerability), and will fail after the fix is applied (proving the fix works).

**Files:**
- Modify: `pipeline/handlers/tool_test.go` (add regression test)

- [ ] **Step 1: Add a capturing mock to tool_test.go**

Add a new mock that records the exact command string passed to `ExecCommand`. Add after the existing `mockExecEnv` type:

```go
// capturingExecEnv records the exact command string passed to ExecCommand.
type capturingExecEnv struct {
	workdir        string
	capturedCmd    string
	capturedArgs   []string
}

func (c *capturingExecEnv) ReadFile(ctx context.Context, path string) (string, error) {
	return "", fmt.Errorf("not implemented")
}
func (c *capturingExecEnv) WriteFile(ctx context.Context, path, content string) error {
	return fmt.Errorf("not implemented")
}
func (c *capturingExecEnv) Glob(ctx context.Context, pattern string) ([]string, error) {
	return nil, fmt.Errorf("not implemented")
}
func (c *capturingExecEnv) WorkingDir() string { return c.workdir }
func (c *capturingExecEnv) ExecCommand(ctx context.Context, command string, args []string, timeout time.Duration) (exec.CommandResult, error) {
	c.capturedCmd = command
	c.capturedArgs = args
	return exec.CommandResult{Stdout: "ok", ExitCode: 0}, nil
}
```

- [ ] **Step 2: Write the regression test**

```go
func TestToolHandlerTaintedInterpolationRegression(t *testing.T) {
	// This test documents the vulnerability: LLM output in last_response
	// is interpolated unsanitized into the shell command.
	// After the fix, this test should be updated to expect an error.
	env := &capturingExecEnv{workdir: t.TempDir()}
	h := NewToolHandler(env)

	pctx := pipeline.NewPipelineContext()
	pctx.Set("last_response", "$(echo INJECTED)")

	node := &pipeline.Node{
		ID:    "vuln",
		Shape: "parallelogram",
		Attrs: map[string]string{"tool_command": "echo ${ctx.last_response}"},
	}

	outcome, err := h.Execute(context.Background(), node, pctx)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if outcome.Status != pipeline.OutcomeSuccess {
		t.Fatalf("status = %q, want success (vulnerability present)", outcome.Status)
	}

	// The captured command contains the unsanitized shell injection payload.
	if len(env.capturedArgs) < 2 {
		t.Fatal("expected captured args")
	}
	cmd := env.capturedArgs[1]
	if !strings.Contains(cmd, "$(echo INJECTED)") {
		t.Fatalf("expected unsanitized payload in command, got: %q", cmd)
	}
}
```

- [ ] **Step 3: Run test to verify it passes (proving vulnerability exists)**

Run: `go test ./pipeline/handlers -run TestToolHandlerTaintedInterpolationRegression -v`
Expected: PASS — the payload flows through unsanitized.

- [ ] **Step 4: Commit the regression test**

```bash
git add pipeline/handlers/tool_test.go
git commit -m "test: regression test proving tool_command injection vulnerability (#16)

Demonstrates that \${ctx.last_response} containing shell metacharacters
is interpolated unsanitized into sh -c command. This test will be
updated to expect rejection after the fix."
```

---

### Task 6: Block tainted keys in `ExpandVariables` for tool_command

Add a `rejectTainted` option to `ExpandVariables` that rejects expansion of hardcoded tainted context keys. The tool handler uses this mode.

**Files:**
- Modify: `pipeline/expand.go` (add tainted key set, add `ExpandToolCommand` wrapper)
- Modify: `pipeline/handlers/tool.go:48-55` (use `ExpandToolCommand`)
- Test: `pipeline/expand_test.go` (add tainted key tests)
- Modify: `pipeline/handlers/tool_test.go` (update regression test, add override test)

- [ ] **Step 1: Write the failing test in expand_test.go**

Add to `pipeline/expand_test.go`:

```go
func TestExpandToolCommand_BlocksTaintedKeys(t *testing.T) {
	ctx := NewPipelineContext()
	ctx.Set("last_response", "malicious")
	ctx.Set("safe_key", "benign")

	// Tainted key should be rejected.
	_, err := ExpandToolCommand("echo ${ctx.last_response}", ctx, nil, nil, nil)
	if err == nil {
		t.Fatal("expected error for tainted key in tool_command")
	}
	if !strings.Contains(err.Error(), "last_response") {
		t.Fatalf("error should mention the tainted key, got: %v", err)
	}
	if !strings.Contains(err.Error(), "unsafe") {
		t.Fatalf("error should mention 'unsafe', got: %v", err)
	}

	// Safe key should expand normally.
	result, err := ExpandToolCommand("echo ${ctx.safe_key}", ctx, nil, nil, nil)
	if err != nil {
		t.Fatalf("safe key should expand: %v", err)
	}
	if result != "echo benign" {
		t.Fatalf("got %q, want %q", result, "echo benign")
	}
}

func TestExpandToolCommand_AllowOverride(t *testing.T) {
	ctx := NewPipelineContext()
	ctx.Set("last_response", "trusted_value")

	// With allow override, tainted key should expand.
	allowed := map[string]bool{"last_response": true}
	result, err := ExpandToolCommand("echo ${ctx.last_response}", ctx, nil, nil, allowed)
	if err != nil {
		t.Fatalf("allowed tainted key should expand: %v", err)
	}
	if result != "echo trusted_value" {
		t.Fatalf("got %q, want %q", result, "echo trusted_value")
	}
}

func TestExpandToolCommand_AllowAll(t *testing.T) {
	ctx := NewPipelineContext()
	ctx.Set("last_response", "value1")
	ctx.Set("human_response", "value2")

	// "all" in the allow set permits everything.
	allowed := map[string]bool{"all": true}
	result, err := ExpandToolCommand("${ctx.last_response} ${ctx.human_response}", ctx, nil, nil, allowed)
	if err != nil {
		t.Fatalf("allow_all should permit all: %v", err)
	}
	if result != "value1 value2" {
		t.Fatalf("got %q, want %q", result, "value1 value2")
	}
}
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `go test ./pipeline -run TestExpandToolCommand -v`
Expected: FAIL — `ExpandToolCommand` doesn't exist yet.

- [ ] **Step 3: Implement `ExpandToolCommand` in expand.go**

Add to `pipeline/expand.go`:

```go
// TaintedContextKeys are context keys that may contain LLM-derived content
// and must not be interpolated into shell commands without explicit opt-in.
var TaintedContextKeys = map[string]bool{
	ContextKeyLastResponse:  true, // "last_response"
	ContextKeyHumanResponse: true, // "human_response"
	ContextKeyToolStdout:    true, // "tool_stdout"
	ContextKeyToolStderr:    true, // "tool_stderr"
}

// ExpandToolCommand expands ${namespace.key} variables in a tool_command string,
// rejecting tainted context keys unless they appear in the allowedTainted set.
// Pass allowedTainted["all"]=true to permit all tainted keys.
func ExpandToolCommand(
	text string,
	ctx *PipelineContext,
	params map[string]string,
	graphAttrs map[string]string,
	allowedTainted map[string]bool,
) (string, error) {
	if text == "" {
		return text, nil
	}

	var buf strings.Builder
	buf.Grow(len(text))
	pos := 0
	for pos < len(text) {
		startIdx := strings.Index(text[pos:], "${")
		if startIdx == -1 {
			buf.WriteString(text[pos:])
			break
		}
		startIdx += pos
		buf.WriteString(text[pos:startIdx])

		endIdx := strings.Index(text[startIdx+2:], "}")
		if endIdx == -1 {
			buf.WriteString(text[startIdx:])
			pos = len(text)
			break
		}
		endIdx += startIdx + 2

		varExpr := text[startIdx+2 : endIdx]
		parts := strings.SplitN(varExpr, ".", 2)
		if varExpr == "" || len(parts) != 2 {
			buf.WriteString(text[startIdx : endIdx+1])
			pos = endIdx + 1
			continue
		}

		namespace := parts[0]
		key := parts[1]

		// Block tainted context keys unless explicitly allowed.
		if namespace == "ctx" && TaintedContextKeys[key] {
			if !allowedTainted["all"] && !allowedTainted[key] {
				return "", fmt.Errorf(
					"tool_command references unsafe context key ${ctx.%s} which may contain LLM output. "+
						"Write the value to a file instead (e.g., read from $ARTIFACT_DIR). "+
						"To override, add 'allow_unsafe_interpolation: %s' to this node",
					key, key,
				)
			}
		}

		value, found, err := lookupVariable(namespace, key, ctx, params, graphAttrs)
		if err != nil {
			return "", err
		}
		if !found {
			value = ""
		}

		buf.WriteString(value)
		pos = endIdx + 1
	}

	return buf.String(), nil
}
```

- [ ] **Step 4: Run expand tests to verify they pass**

Run: `go test ./pipeline -run TestExpandToolCommand -v`
Expected: All 3 tests pass.

- [ ] **Step 5: Wire up ToolHandler to use `ExpandToolCommand`**

In `pipeline/handlers/tool.go`, replace lines 48-55:

```go
	// Parse allow_unsafe_interpolation attribute for tainted key override.
	var allowedTainted map[string]bool
	if allowStr, ok := node.Attrs["allow_unsafe_interpolation"]; ok && allowStr != "" {
		allowedTainted = make(map[string]bool)
		for _, key := range strings.Split(allowStr, ",") {
			allowedTainted[strings.TrimSpace(key)] = true
		}
	}

	// Expand ${namespace.key} variables, blocking tainted keys unless allowed.
	expandedCommand, err := pipeline.ExpandToolCommand(command, pctx, nil, nil, allowedTainted)
	if err != nil {
		return pipeline.Outcome{Status: pipeline.OutcomeFail}, fmt.Errorf("node %q: %w", node.ID, err)
	}
	if expandedCommand != "" {
		command = expandedCommand
	}
```

- [ ] **Step 6: Update the regression test to expect rejection**

In `pipeline/handlers/tool_test.go`, update `TestToolHandlerTaintedInterpolationRegression`:

```go
func TestToolHandlerTaintedInterpolationBlocked(t *testing.T) {
	// After the fix: tainted interpolation is rejected with an error.
	env := &capturingExecEnv{workdir: t.TempDir()}
	h := NewToolHandler(env)

	pctx := pipeline.NewPipelineContext()
	pctx.Set("last_response", "$(echo INJECTED)")

	node := &pipeline.Node{
		ID:    "vuln",
		Shape: "parallelogram",
		Attrs: map[string]string{"tool_command": "echo ${ctx.last_response}"},
	}

	_, err := h.Execute(context.Background(), node, pctx)
	if err == nil {
		t.Fatal("expected error for tainted interpolation in tool_command")
	}
	if !strings.Contains(err.Error(), "last_response") {
		t.Fatalf("error should mention tainted key, got: %v", err)
	}

	// Command should NOT have been executed.
	if env.capturedCmd != "" {
		t.Fatalf("command should not execute, but captured: %q", env.capturedCmd)
	}
}
```

- [ ] **Step 7: Add override test in tool_test.go**

```go
func TestToolHandlerAllowUnsafeInterpolation(t *testing.T) {
	env := &capturingExecEnv{workdir: t.TempDir()}
	h := NewToolHandler(env)

	pctx := pipeline.NewPipelineContext()
	pctx.Set("last_response", "safe_value")

	node := &pipeline.Node{
		ID:    "override",
		Shape: "parallelogram",
		Attrs: map[string]string{
			"tool_command":                "echo ${ctx.last_response}",
			"allow_unsafe_interpolation": "last_response",
		},
	}

	outcome, err := h.Execute(context.Background(), node, pctx)
	if err != nil {
		t.Fatalf("allowed tainted key should not error: %v", err)
	}
	if outcome.Status != pipeline.OutcomeSuccess {
		t.Fatalf("status = %q, want success", outcome.Status)
	}
	if len(env.capturedArgs) >= 2 && !strings.Contains(env.capturedArgs[1], "safe_value") {
		t.Fatalf("expected expanded value in command, got: %q", env.capturedArgs[1])
	}
}
```

- [ ] **Step 8: Run all tool tests**

Run: `go test ./pipeline/handlers -run TestToolHandler -v`
Expected: All pass.

- [ ] **Step 9: Run full test suite**

Run: `go test ./... -short -count=1`
Expected: All packages pass.

- [ ] **Step 10: Commit**

```bash
git add pipeline/expand.go pipeline/expand_test.go pipeline/handlers/tool.go pipeline/handlers/tool_test.go
git commit -m "fix: block tainted context keys in tool_command expansion (#16)

ExpandToolCommand rejects \${ctx.last_response}, \${ctx.human_response},
\${ctx.tool_stdout}, \${ctx.tool_stderr} in tool commands by default.
Pipeline authors can opt in per-variable via allow_unsafe_interpolation.
Error message includes the key name and suggests file-mediated passing."
```

---

### Task 7: Block tainted graph variables in `prepareExecNode` for tool_command

`ExpandGraphVariables` uses bare `$key` syntax with `strings.ReplaceAll` and runs BEFORE the handler. Gate it for the `tool_command` attribute.

**Files:**
- Modify: `pipeline/transforms.go` (add `ExpandGraphVariablesFiltered`)
- Modify: `pipeline/engine_run.go:148-185` (use filtered expansion for tool_command)
- Test: `pipeline/transforms_test.go` (add filtered expansion test)

- [ ] **Step 1: Write the failing test**

Create or add to `pipeline/transforms_test.go`:

```go
func TestExpandGraphVariablesFiltered_ExcludesTaintedKeys(t *testing.T) {
	vars := map[string]string{
		"$safe_var":   "benign",
		"$last_response": "malicious",
	}
	tainted := map[string]bool{"$last_response": true}

	result := ExpandGraphVariablesFiltered("echo $safe_var $last_response", vars, tainted)
	if !strings.Contains(result, "benign") {
		t.Fatalf("safe var should be expanded, got: %q", result)
	}
	if !strings.Contains(result, "$last_response") {
		t.Fatalf("tainted var should NOT be expanded, got: %q", result)
	}
}

func TestExpandGraphVariablesFiltered_NilExcludeExpandsAll(t *testing.T) {
	vars := map[string]string{
		"$key": "value",
	}
	result := ExpandGraphVariablesFiltered("echo $key", vars, nil)
	if result != "echo value" {
		t.Fatalf("got %q, want %q", result, "echo value")
	}
}
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `go test ./pipeline -run TestExpandGraphVariablesFiltered -v`
Expected: FAIL — function doesn't exist.

- [ ] **Step 3: Implement `ExpandGraphVariablesFiltered`**

Add to `pipeline/transforms.go`:

```go
// ExpandGraphVariablesFiltered is like ExpandGraphVariables but skips variables
// in the exclude set. Used to prevent tainted graph variables from being
// expanded into tool_command attributes.
func ExpandGraphVariablesFiltered(text string, vars map[string]string, exclude map[string]bool) string {
	if text == "" || len(vars) == 0 || !strings.Contains(text, "$") {
		return text
	}
	for varName, val := range vars {
		if exclude[varName] {
			continue
		}
		if strings.Contains(text, varName) {
			text = strings.ReplaceAll(text, varName, val)
		}
	}
	return text
}

// TaintedGraphVars returns the set of graph variable names (e.g., "$last_response")
// whose source context key is in the TaintedContextKeys set. These should be
// excluded from tool_command expansion.
func TaintedGraphVars(graphVars map[string]string) map[string]bool {
	exclude := make(map[string]bool)
	for varName := range graphVars {
		// varName is "$key", source context key is "graph.key".
		// Check if the bare key (without "$") is a tainted context key.
		bareKey := strings.TrimPrefix(varName, "$")
		if TaintedContextKeys[bareKey] {
			exclude[varName] = true
		}
	}
	return exclude
}
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `go test ./pipeline -run TestExpandGraphVariablesFiltered -v`
Expected: PASS.

- [ ] **Step 5: Wire up `prepareExecNode` to use filtered expansion for tool_command**

In `pipeline/engine_run.go`, modify `prepareExecNode` (lines 162-174). Replace the attribute expansion loop:

```go
	graphVars := GraphVarMap(s.pctx)
	taintedVars := TaintedGraphVars(graphVars)
	execAttrs := make(map[string]string, len(execNode.Attrs))
	changed := false
	for k, v := range execNode.Attrs {
		var expanded string
		if k == "tool_command" {
			expanded = ExpandGraphVariablesFiltered(v, graphVars, taintedVars)
		} else {
			expanded = ExpandGraphVariables(v, graphVars)
		}
		if k == "prompt" {
			expanded = ExpandPromptVariables(expanded, s.pctx)
		}
		execAttrs[k] = expanded
		if expanded != v {
			changed = true
		}
	}
```

- [ ] **Step 6: Write an engine-level test for the gated expansion**

Add to `pipeline/engine_test.go`:

```go
func TestPrepareExecNodeBlocksTaintedGraphVarsInToolCommand(t *testing.T) {
	g := NewGraph("tainted-graph-var")
	g.AddNode(&Node{ID: "start", Shape: "Mdiamond"})
	g.AddNode(&Node{ID: "tool1", Shape: "parallelogram", Handler: "tool", Attrs: map[string]string{
		"tool_command": "echo $last_response",
	}})
	g.AddNode(&Node{ID: "done", Shape: "Msquare"})
	g.AddEdge(&Edge{From: "start", To: "tool1"})
	g.AddEdge(&Edge{From: "tool1", To: "done"})

	reg := newTestRegistry()
	var capturedCommand string
	reg.Register(&testHandler{
		name: "tool",
		executeFn: func(ctx context.Context, node *Node, pctx *PipelineContext) (Outcome, error) {
			capturedCommand = node.Attrs["tool_command"]
			return Outcome{Status: OutcomeSuccess}, nil
		},
	})

	engine := NewEngine(g, reg)
	pctx := NewPipelineContext()
	// Simulate an LLM writing to a graph.* key that matches a tainted key name.
	pctx.Set("graph.last_response", "$(evil)")

	// Run the engine — the tool node should receive the unexpanded $last_response.
	result, err := engine.Run(context.Background())
	if err != nil {
		t.Fatalf("engine.Run error: %v", err)
	}
	_ = result

	// The tainted graph var should NOT have been expanded in tool_command.
	if strings.Contains(capturedCommand, "$(evil)") {
		t.Fatalf("tainted graph var was expanded in tool_command: %q", capturedCommand)
	}
	if !strings.Contains(capturedCommand, "$last_response") {
		t.Fatalf("expected unexpanded $last_response in tool_command, got: %q", capturedCommand)
	}
}
```

- [ ] **Step 7: Run all related tests**

Run: `go test ./pipeline -run "TestExpandGraphVariablesFiltered|TestPrepareExecNode" -v`
Expected: All pass.

- [ ] **Step 8: Run full test suite**

Run: `go test ./... -short -count=1`
Expected: All packages pass.

- [ ] **Step 9: Commit**

```bash
git add pipeline/transforms.go pipeline/transforms_test.go pipeline/engine_run.go pipeline/engine_test.go
git commit -m "fix: block tainted graph vars in tool_command expansion (#16)

ExpandGraphVariablesFiltered skips variables whose bare key matches
TaintedContextKeys when expanding tool_command attributes.
Prevents the ExpandGraphVariables bypass of the ExpandToolCommand gate."
```

---

### Task 8: Add lint rule DIP130 — tainted interpolation in tool_command

Static analysis catches the issue at `dippin doctor` time, before the pipeline runs.

**Files:**
- Modify: `pipeline/lint_dippin.go` (add `lintDIP130`, wire into `LintDippinRules`)
- Test: `pipeline/lint_dippin_test.go` (add DIP130 tests)

- [ ] **Step 1: Write the failing tests**

Add to `pipeline/lint_dippin_test.go`:

```go
func TestLintDIP130_TaintedInterpolationInToolCommand(t *testing.T) {
	g := NewGraph("test")
	g.AddNode(&Node{
		ID:      "RunTool",
		Handler: "tool",
		Attrs:   map[string]string{"tool_command": "echo ${ctx.last_response}"},
	})

	warnings := LintDippinRules(g)
	if !containsWarning(warnings, "DIP130", "RunTool") {
		t.Errorf("expected DIP130 warning, got: %v", warnings)
	}
}

func TestLintDIP130_NoWarningInPrompt(t *testing.T) {
	g := NewGraph("test")
	g.AddNode(&Node{
		ID:      "Agent1",
		Handler: "codergen",
		Attrs:   map[string]string{"prompt": "Analyze ${ctx.last_response}"},
	})

	warnings := LintDippinRules(g)
	if containsWarning(warnings, "DIP130", "") {
		t.Errorf("unexpected DIP130 warning for prompt attribute: %v", warnings)
	}
}

func TestLintDIP130_NoWarningWithAllowOverride(t *testing.T) {
	g := NewGraph("test")
	g.AddNode(&Node{
		ID:      "RunTool",
		Handler: "tool",
		Attrs: map[string]string{
			"tool_command":                "echo ${ctx.last_response}",
			"allow_unsafe_interpolation": "last_response",
		},
	})

	warnings := LintDippinRules(g)
	if containsWarning(warnings, "DIP130", "RunTool") {
		t.Errorf("unexpected DIP130 warning with allow_unsafe_interpolation: %v", warnings)
	}
}

func TestLintDIP130_BareVarSyntax(t *testing.T) {
	g := NewGraph("test")
	g.AddNode(&Node{
		ID:      "RunTool",
		Handler: "tool",
		Attrs:   map[string]string{"tool_command": "echo $human_response"},
	})

	warnings := LintDippinRules(g)
	if !containsWarning(warnings, "DIP130", "RunTool") {
		t.Errorf("expected DIP130 warning for bare $human_response, got: %v", warnings)
	}
}
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `go test ./pipeline -run TestLintDIP130 -v`
Expected: FAIL — `DIP130` warning not emitted.

- [ ] **Step 3: Implement `lintDIP130`**

Add to `pipeline/lint_dippin.go`:

```go
// lintDIP130 warns when a tool_command references context keys that may contain
// LLM-derived content (last_response, human_response, tool_stdout, tool_stderr).
func lintDIP130(g *Graph) []string {
	var warnings []string

	// Both ${ctx.key} and bare $key syntax.
	taintedPatterns := []struct {
		pattern string
		key     string
	}{
		{"${ctx.last_response}", "last_response"},
		{"${ctx.human_response}", "human_response"},
		{"${ctx.tool_stdout}", "tool_stdout"},
		{"${ctx.tool_stderr}", "tool_stderr"},
		{"$last_response", "last_response"},
		{"$human_response", "human_response"},
		{"$tool_stdout", "tool_stdout"},
		{"$tool_stderr", "tool_stderr"},
	}

	for _, node := range g.Nodes {
		cmd := node.Attrs["tool_command"]
		if cmd == "" {
			continue
		}

		// Parse allow_unsafe_interpolation into a set.
		allowed := make(map[string]bool)
		if allowStr := node.Attrs["allow_unsafe_interpolation"]; allowStr != "" {
			for _, k := range strings.Split(allowStr, ",") {
				allowed[strings.TrimSpace(k)] = true
			}
		}

		for _, tp := range taintedPatterns {
			if strings.Contains(cmd, tp.pattern) && !allowed["all"] && !allowed[tp.key] {
				warnings = append(warnings, fmt.Sprintf(
					"warning[DIP130]: node %q tool_command references %s which may contain LLM output; consider file-mediated passing or add allow_unsafe_interpolation: %s",
					node.ID, tp.pattern, tp.key,
				))
				break // One warning per node is enough.
			}
		}
	}
	return warnings
}
```

- [ ] **Step 4: Wire DIP130 into `LintDippinRules`**

In `pipeline/lint_dippin.go`, add to the `LintDippinRules` function:

```go
	warnings = append(warnings, lintDIP130(g)...)
```

- [ ] **Step 5: Run lint tests**

Run: `go test ./pipeline -run TestLintDIP130 -v`
Expected: All 4 tests pass.

- [ ] **Step 6: Run full test suite**

Run: `go test ./... -short -count=1`
Expected: All packages pass.

- [ ] **Step 7: Commit**

```bash
git add pipeline/lint_dippin.go pipeline/lint_dippin_test.go
git commit -m "feat: lint rule DIP130 — tainted interpolation in tool_command (#16)

Warns when tool_command references \${ctx.last_response}, \${ctx.human_response},
\${ctx.tool_stdout}, or \${ctx.tool_stderr}. Suppressed by allow_unsafe_interpolation.
Catches both \${ctx.key} and bare \$key syntax."
```

---

### Task 9: Engine-level integration test for parallel context propagation (#20)

Verifies the full path: parallel → branch side-effect writes → fan-in merge → downstream reads.

**Files:**
- Modify: `pipeline/handlers/integration_test.go` (add parallel context integration test)

- [ ] **Step 1: Write the integration test**

Add to `pipeline/handlers/integration_test.go`:

```go
func TestParallelContextPropagationIntegration(t *testing.T) {
	// Build a graph: start → parallel → [branchA, branchB] → fanin → verify → done
	g := pipeline.NewGraph("parallel-context-e2e")
	g.AddNode(&pipeline.Node{ID: "start", Shape: "Mdiamond"})
	g.AddNode(&pipeline.Node{ID: "dispatch", Shape: "box", Handler: "parallel", Attrs: map[string]string{
		"parallel_targets": "branchA,branchB",
		"parallel_join":    "fanin",
	}})
	g.AddNode(&pipeline.Node{ID: "branchA", Shape: "box"})
	g.AddNode(&pipeline.Node{ID: "branchB", Shape: "box"})
	g.AddNode(&pipeline.Node{ID: "fanin", Shape: "box", Handler: "parallel.fan_in"})
	g.AddNode(&pipeline.Node{ID: "verify", Shape: "box"})
	g.AddNode(&pipeline.Node{ID: "done", Shape: "Msquare"})

	g.AddEdge(&pipeline.Edge{From: "start", To: "dispatch"})
	g.AddEdge(&pipeline.Edge{From: "dispatch", To: "fanin"})
	g.AddEdge(&pipeline.Edge{From: "fanin", To: "verify"})
	g.AddEdge(&pipeline.Edge{From: "verify", To: "done"})

	reg := pipeline.NewHandlerRegistry()

	// Default handler for start/done.
	reg.Register(&stubHandler{name: "start", fn: successHandler})
	reg.Register(&stubHandler{name: "exit", fn: successHandler})

	// BranchA writes via pctx.Set() side effect only.
	// BranchB returns via ContextUpdates only.
	reg.Register(&stubHandler{name: "codergen", fn: func(ctx context.Context, node *pipeline.Node, pctx *pipeline.PipelineContext) (pipeline.Outcome, error) {
		switch node.ID {
		case "branchA":
			pctx.Set("from_branch_a", "side_effect_value")
			return pipeline.Outcome{Status: pipeline.OutcomeSuccess}, nil
		case "branchB":
			return pipeline.Outcome{
				Status:         pipeline.OutcomeSuccess,
				ContextUpdates: map[string]string{"from_branch_b": "explicit_value"},
			}, nil
		case "verify":
			a, aOK := pctx.Get("from_branch_a")
			b, bOK := pctx.Get("from_branch_b")
			if !aOK || a != "side_effect_value" {
				return pipeline.Outcome{Status: pipeline.OutcomeFail}, fmt.Errorf("from_branch_a missing or wrong: %q (found=%v)", a, aOK)
			}
			if !bOK || b != "explicit_value" {
				return pipeline.Outcome{Status: pipeline.OutcomeFail}, fmt.Errorf("from_branch_b missing or wrong: %q (found=%v)", b, bOK)
			}
			return pipeline.Outcome{Status: pipeline.OutcomeSuccess}, nil
		default:
			return pipeline.Outcome{Status: pipeline.OutcomeSuccess}, nil
		}
	}})

	// Register real parallel and fan-in handlers.
	reg.Register(NewParallelHandler(reg))
	reg.Register(NewFanInHandler())

	engine := pipeline.NewEngine(g, reg)
	result, err := engine.Run(context.Background())
	if err != nil {
		t.Fatalf("engine.Run error: %v", err)
	}
	if result.Status != pipeline.OutcomeSuccess {
		t.Fatalf("status = %q, want success", result.Status)
	}

	// Also verify the keys are in the final context.
	if result.Context["from_branch_a"] != "side_effect_value" {
		t.Errorf("EngineResult.Context missing from_branch_a: %v", result.Context)
	}
	if result.Context["from_branch_b"] != "explicit_value" {
		t.Errorf("EngineResult.Context missing from_branch_b: %v", result.Context)
	}
}
```

Note: You may need to check if `stubHandler` and `successHandler` already exist in integration_test.go. If they use different names, use the existing test helper pattern. If `stubHandler` doesn't exist, define it:

```go
type stubHandler struct {
	name string
	fn   func(context.Context, *pipeline.Node, *pipeline.PipelineContext) (pipeline.Outcome, error)
}

func (s *stubHandler) Name() string { return s.name }
func (s *stubHandler) Execute(ctx context.Context, node *pipeline.Node, pctx *pipeline.PipelineContext) (pipeline.Outcome, error) {
	return s.fn(ctx, node, pctx)
}

func successHandler(ctx context.Context, node *pipeline.Node, pctx *pipeline.PipelineContext) (pipeline.Outcome, error) {
	return pipeline.Outcome{Status: pipeline.OutcomeSuccess}, nil
}
```

- [ ] **Step 2: Run the test**

Run: `go test ./pipeline/handlers -run TestParallelContextPropagationIntegration -v`
Expected: PASS — the fix from Task 1 already captures side-effect writes.

- [ ] **Step 3: Commit**

```bash
git add pipeline/handlers/integration_test.go
git commit -m "test: engine-level integration test for parallel context propagation (#20)

Verifies the full path: parallel dispatch → branch side-effect writes
via pctx.Set() → DiffFrom capture → fan-in merge → downstream reads.
Both side-effect and explicit ContextUpdates verified in EngineResult."
```

---

### Task 10: Update CLAUDE.md and documentation

Add the safe pattern for LLM output in tool commands and document the new `allow_unsafe_interpolation` attribute.

**Files:**
- Modify: `CLAUDE.md` (add safe pattern docs, document DIP130)

- [ ] **Step 1: Add safe pattern documentation to CLAUDE.md**

Under the existing "Tool node safety" section, add:

```markdown
### Safe pattern for LLM output in tool commands
- Do NOT interpolate `${ctx.last_response}` or `${ctx.human_response}` in `tool_command` — the engine blocks this by default
- Instead, have the agent node write structured output to a file in `$ARTIFACT_DIR`, then read it in the tool command: `cat "$ARTIFACT_DIR/agent_output.json" | jq ...`
- If you must interpolate (rare), add `allow_unsafe_interpolation: last_response` to the tool node — you accept the injection risk
- DIP130 lint rule catches this at `dippin doctor` time
```

Also add under "Checkpoint resume is fragile":

```markdown
### Checkpoint writes are atomic
`SaveCheckpoint` uses write-to-temp-then-rename to prevent crash-corrupted checkpoints.
```

And under "Edge routing" or similar:

```markdown
### Goal-gate retry termination
- Goal gates have a retry budget (`max_retries`, default 3) checked before each retry loop
- When retries exhaust, the engine looks for `fallback_target` for one escalation attempt
- If escalation also fails (or no fallback configured), the pipeline terminates with failure
- `FallbackTaken` flag in checkpoint prevents infinite escalation loops
- Global circuit breaker (10,000 iterations) catches any remaining unbounded loop
```

- [ ] **Step 2: Run build to ensure nothing is broken**

Run: `go build ./... && go test ./... -short -count=1`
Expected: Clean build, all tests pass.

- [ ] **Step 3: Commit**

```bash
git add CLAUDE.md
git commit -m "docs: document tool_command safety, goal-gate termination, atomic checkpoints

Adds safe pattern for LLM output in tool commands (file-mediated passing).
Documents allow_unsafe_interpolation attribute and DIP130 lint rule.
Documents goal-gate retry termination and FallbackTaken mechanism.
Documents atomic checkpoint writes."
```

---

## Task Dependency Graph

```
Task 1 (commit existing work)
  └─ Task 2 (escalation loop fix)
       └─ Task 4 (atomic checkpoints — uses FallbackTaken from Task 2)
  └─ Task 3 (circuit breaker — independent)
  └─ Task 5 (regression test — independent)
       └─ Task 6 (block tainted ExpandVariables)
            └─ Task 7 (block tainted ExpandGraphVariables)
                 └─ Task 8 (DIP130 lint rule)
  └─ Task 9 (parallel integration test — verifies Task 1)
  └─ Task 10 (docs — after all code is done)
```

Tasks 3, 5, and 9 can run in parallel after Task 1. Task 10 is last.
