# Parallel Concurrency & Human Gate Timeout Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Add concurrency limits and per-branch timeout to parallel handler, add timeout with default fallback to human gates.

**Architecture:** Two independent handler enhancements. Task 1 modifies the parallel handler dispatch loop. Task 2 adds timeout wrappers to the human handler's blocking calls.

**Tech Stack:** Go 1.25, standard library (`time`, `strconv`, `context`).

**Spec:** `docs/superpowers/specs/2026-04-03-parallel-human-enhancements-design.md`

---

### Task 1: Parallel concurrency limit and branch timeout (#27)

**Files:**
- Modify: `pipeline/handlers/parallel.go:124-155` (executeBranches), `pipeline/handlers/parallel.go:157-210` (runBranch)
- Test: `pipeline/handlers/parallel_test.go`

- [ ] **Step 1: Write failing tests**

Add to `pipeline/handlers/parallel_test.go`:

```go
func TestParallelHandler_MaxConcurrency(t *testing.T) {
	// Create a parallel node with max_concurrency=2 and 4 branches.
	// Track peak concurrent goroutines to verify the limit holds.
	g := pipeline.NewGraph("concurrency-test")
	g.AddNode(&pipeline.Node{ID: "par", Shape: "component", Attrs: map[string]string{
		"max_concurrency": "2",
	}})
	for _, id := range []string{"a", "b", "c", "d"} {
		g.AddNode(&pipeline.Node{ID: id, Shape: "box", Handler: "codergen", Attrs: map[string]string{"prompt": "test"}})
		g.AddEdge(&pipeline.Edge{From: "par", To: id})
	}

	var mu sync.Mutex
	active, peak := 0, 0
	reg := &pipeline.HandlerRegistry{}
	reg.Register(&testHandler{
		name: "codergen",
		executeFn: func(ctx context.Context, node *pipeline.Node, pctx *pipeline.PipelineContext) (pipeline.Outcome, error) {
			mu.Lock()
			active++
			if active > peak {
				peak = active
			}
			mu.Unlock()
			time.Sleep(50 * time.Millisecond)
			mu.Lock()
			active--
			mu.Unlock()
			return pipeline.Outcome{Status: pipeline.OutcomeSuccess}, nil
		},
	})

	h := NewParallelHandler(g, reg, pipeline.NoopEventHandler{})
	pctx := pipeline.NewPipelineContext()
	node := g.Nodes["par"]
	_, err := h.Execute(context.Background(), node, pctx)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if peak > 2 {
		t.Errorf("peak concurrency = %d, want <= 2", peak)
	}
}

func TestParallelHandler_BranchTimeout(t *testing.T) {
	g := pipeline.NewGraph("timeout-test")
	g.AddNode(&pipeline.Node{ID: "par", Shape: "component", Attrs: map[string]string{
		"branch_timeout": "100ms",
	}})
	g.AddNode(&pipeline.Node{ID: "fast", Shape: "box", Handler: "codergen", Attrs: map[string]string{"prompt": "test"}})
	g.AddNode(&pipeline.Node{ID: "slow", Shape: "box", Handler: "codergen", Attrs: map[string]string{"prompt": "test"}})
	g.AddEdge(&pipeline.Edge{From: "par", To: "fast"})
	g.AddEdge(&pipeline.Edge{From: "par", To: "slow"})

	reg := &pipeline.HandlerRegistry{}
	reg.Register(&testHandler{
		name: "codergen",
		executeFn: func(ctx context.Context, node *pipeline.Node, pctx *pipeline.PipelineContext) (pipeline.Outcome, error) {
			if node.ID == "slow" {
				select {
				case <-time.After(5 * time.Second):
					return pipeline.Outcome{Status: pipeline.OutcomeSuccess}, nil
				case <-ctx.Done():
					return pipeline.Outcome{Status: pipeline.OutcomeFail}, ctx.Err()
				}
			}
			return pipeline.Outcome{Status: pipeline.OutcomeSuccess}, nil
		},
	})

	h := NewParallelHandler(g, reg, pipeline.NoopEventHandler{})
	pctx := pipeline.NewPipelineContext()
	node := g.Nodes["par"]

	start := time.Now()
	outcome, err := h.Execute(context.Background(), node, pctx)
	elapsed := time.Since(start)

	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// Fast branch succeeded, so aggregate should be success.
	if outcome.Status != pipeline.OutcomeSuccess {
		t.Errorf("status = %q, want success", outcome.Status)
	}
	// Should complete in well under 5s (the slow branch timeout is 100ms).
	if elapsed > 2*time.Second {
		t.Errorf("elapsed = %v, want < 2s (branch_timeout should have kicked in)", elapsed)
	}
}
```

NOTE: Check the existing test file for `testHandler` and other mock types — use whatever pattern is established. If `pipeline.HandlerRegistry` isn't directly constructable, use the existing `newTestRegistry()` helper or equivalent. The `pipeline.NoopEventHandler{}` may need to be found or created — check for an existing noop event handler implementation.

- [ ] **Step 2: Run tests to verify they fail**

Run: `go test ./pipeline/handlers/ -run "TestParallelHandler_MaxConcurrency|TestParallelHandler_BranchTimeout" -v`
Expected: FAIL — no concurrency limit or timeout behavior.

- [ ] **Step 3: Add concurrency limit to executeBranches**

In `pipeline/handlers/parallel.go`, in `executeBranches` (around line 125), parse the attrs and create a semaphore:

```go
func (h *ParallelHandler) executeBranches(ctx context.Context, edges []*pipeline.Edge, branchOverrides map[string]map[string]string, pctx *pipeline.PipelineContext) []ParallelResult {
	snapshot := pctx.Snapshot()
	artifactDir, _ := pctx.GetInternal(pipeline.InternalKeyArtifactDir)

	// Parse concurrency limit from the parallel node attrs (set by caller).
	var sem chan struct{}
	if mc, ok := h.parallelNode.Attrs["max_concurrency"]; ok {
		if n, err := strconv.Atoi(mc); err == nil && n > 0 {
			sem = make(chan struct{}, n)
		}
	}

	// Parse branch timeout.
	var branchTimeout time.Duration
	if bt, ok := h.parallelNode.Attrs["branch_timeout"]; ok {
		if d, err := time.ParseDuration(bt); err == nil && d > 0 {
			branchTimeout = d
		}
	}

	resultsCh := make(chan branchResultMsg, len(edges))
	var wg sync.WaitGroup

	for i, edge := range edges {
		// ... existing target node lookup ...

		execNode := applyBranchOverrides(targetNode, branchOverrides)
		wg.Add(1)
		go h.runBranch(ctx, i, execNode, snapshot, artifactDir, resultsCh, &wg, sem, branchTimeout)
	}
	// ... rest unchanged
```

Note: You'll need to store `h.parallelNode` — the Execute method receives the node. Either pass it through or store it on the handler temporarily. Check how `Execute` calls `executeBranches` to find the cleanest approach.

- [ ] **Step 4: Add semaphore and timeout to runBranch**

Update `runBranch` signature to accept sem and branchTimeout:

```go
func (h *ParallelHandler) runBranch(ctx context.Context, idx int, tn *pipeline.Node, snapshot map[string]string, artifactDir string, resultsCh chan<- branchResultMsg, wg *sync.WaitGroup, sem chan struct{}, branchTimeout time.Duration) {
	defer wg.Done()
	// Acquire semaphore if configured.
	if sem != nil {
		sem <- struct{}{}
		defer func() { <-sem }()
	}

	defer func() {
		// ... existing panic recovery ...
	}()

	// ... existing event emission ...

	branchCtx := pipeline.NewPipelineContextFrom(snapshot)
	// ... existing artifact dir setup ...

	// Apply branch timeout if configured.
	execCtx := ctx
	if branchTimeout > 0 {
		var cancel context.CancelFunc
		execCtx, cancel = context.WithTimeout(ctx, branchTimeout)
		defer cancel()
	}

	outcome, err := h.registry.Execute(execCtx, tn, branchCtx)
	// ... rest unchanged ...
```

- [ ] **Step 5: Run tests**

Run: `go test ./pipeline/handlers/ -run "TestParallelHandler" -v`
Expected: All pass including new concurrency and timeout tests.

- [ ] **Step 6: Run full suite**

Run: `go test ./... -short`
Expected: All 14 packages pass.

- [ ] **Step 7: Commit**

```bash
git add pipeline/handlers/parallel.go pipeline/handlers/parallel_test.go
git commit -m "feat(parallel): add max_concurrency semaphore and branch_timeout (#27)"
```

---

### Task 2: Human gate timeout with default fallback (#30)

**Files:**
- Modify: `pipeline/handlers/human.go` (executeFreeform, executeChoice, executeInterview)
- Test: `pipeline/handlers/human_test.go`

- [ ] **Step 1: Write failing tests**

Add to `pipeline/handlers/human_test.go`:

```go
func TestHumanHandler_FreeformTimeout(t *testing.T) {
	// Interviewer that blocks forever — timeout should fire.
	graph := &pipeline.Graph{
		Nodes: map[string]*pipeline.Node{
			"ask": {ID: "ask", Shape: "hexagon", Attrs: map[string]string{
				"prompt":         "what?",
				"timeout":        "100ms",
				"timeout_action": "fail",
			}},
		},
	}
	fi := &blockingInterviewer{}
	h := NewHumanHandler(fi, graph)
	node := graph.Nodes["ask"]
	pctx := pipeline.NewPipelineContext()

	start := time.Now()
	outcome, err := h.Execute(context.Background(), node, pctx)
	elapsed := time.Since(start)

	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if outcome.Status != pipeline.OutcomeFail {
		t.Errorf("status = %q, want fail", outcome.Status)
	}
	if elapsed > 2*time.Second {
		t.Errorf("elapsed = %v, timeout should have fired at 100ms", elapsed)
	}
}

func TestHumanHandler_TimeoutUsesDefault(t *testing.T) {
	graph := &pipeline.Graph{
		Nodes: map[string]*pipeline.Node{
			"ask": {ID: "ask", Shape: "hexagon", Attrs: map[string]string{
				"prompt":         "what?",
				"timeout":        "100ms",
				"default_choice": "approved",
			}},
		},
	}
	fi := &blockingInterviewer{}
	h := NewHumanHandler(fi, graph)
	node := graph.Nodes["ask"]
	pctx := pipeline.NewPipelineContext()

	outcome, err := h.Execute(context.Background(), node, pctx)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if outcome.Status != pipeline.OutcomeSuccess {
		t.Errorf("status = %q, want success", outcome.Status)
	}
	resp := outcome.ContextUpdates[pipeline.ContextKeyHumanResponse]
	if resp != "approved" {
		t.Errorf("human_response = %q, want %q", resp, "approved")
	}
}

// blockingInterviewer blocks forever on all methods.
type blockingInterviewer struct{}

func (b *blockingInterviewer) Ask(prompt string, choices []string, defaultChoice string) (string, error) {
	select {} // block forever
}

func (b *blockingInterviewer) AskFreeform(prompt string) (string, error) {
	select {} // block forever
}
```

NOTE: Check the existing test file for the `Interviewer` interface requirements. The `blockingInterviewer` must implement whichever interface the human handler expects. Add method stubs as needed.

- [ ] **Step 2: Run tests to verify they fail**

Run: `go test ./pipeline/handlers/ -run "TestHumanHandler_FreeformTimeout|TestHumanHandler_TimeoutUsesDefault" -v -timeout 10s`
Expected: FAIL — tests hang (no timeout behavior) and hit the 10s test timeout.

- [ ] **Step 3: Add timeout wrapper and sentinel error**

In `pipeline/handlers/human.go`, add near the top (after imports):

```go
var errHumanTimeout = fmt.Errorf("human gate timed out waiting for input")
```

Add a generic timeout wrapper function:

```go
// withTimeout runs fn in a goroutine and returns its result, or errHumanTimeout
// if the duration elapses first. A zero timeout means no timeout (blocks forever).
func withTimeout(timeout time.Duration, fn func() (string, error)) (string, error) {
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
		return "", errHumanTimeout
	}
}
```

Add a helper to parse timeout from node attrs:

```go
func parseHumanTimeout(node *pipeline.Node) time.Duration {
	if ts, ok := node.Attrs["timeout"]; ok {
		if d, err := time.ParseDuration(ts); err == nil {
			return d
		}
	}
	return 0
}
```

- [ ] **Step 4: Wrap freeform calls with timeout**

In `executeFreeform`, wrap the interviewer calls:

```go
	timeout := parseHumanTimeout(node)

	var response string
	var err error
	if lfi, ok := fi.(LabeledFreeformInterviewer); ok && len(labels) > 0 {
		response, err = withTimeout(timeout, func() (string, error) {
			return lfi.AskFreeformWithLabels(prompt, labels, defaultLabel)
		})
	} else {
		response, err = withTimeout(timeout, func() (string, error) {
			return fi.AskFreeform(prompt)
		})
	}
```

- [ ] **Step 5: Wrap choice call with timeout**

In `executeChoice`, wrap the `h.interviewer.Ask` call:

```go
	timeout := parseHumanTimeout(node)
	selected, err := withTimeout(timeout, func() (string, error) {
		return h.interviewer.Ask(prompt, choices, node.Attrs["default_choice"])
	})
```

- [ ] **Step 6: Add timeout_action handling in Execute**

In the main `Execute` method of the human handler, after calling `executeFreeform`/`executeChoice`/`executeInterview`, check if the error is `errHumanTimeout` and apply the timeout_action:

Find where each mode's result is returned. After the mode dispatch, add:

```go
	if errors.Is(err, errHumanTimeout) {
		action := node.Attrs["timeout_action"]
		if action == "" {
			action = "default"
		}
		switch action {
		case "fail":
			return pipeline.Outcome{Status: pipeline.OutcomeFail, ContextUpdates: map[string]string{
				pipeline.ContextKeyHumanResponse: "timed out",
			}}, nil
		default: // "default"
			def := node.Attrs["default_choice"]
			if def == "" {
				return pipeline.Outcome{Status: pipeline.OutcomeFail, ContextUpdates: map[string]string{
					pipeline.ContextKeyHumanResponse: "timed out (no default)",
				}}, nil
			}
			return pipeline.Outcome{
				Status:         pipeline.OutcomeSuccess,
				PreferredLabel: def,
				ContextUpdates: map[string]string{
					pipeline.ContextKeyHumanResponse:            def,
					pipeline.ContextKeyResponsePrefix + node.ID: def,
				},
			}, nil
		}
	}
```

Add `"errors"` to imports if not present.

- [ ] **Step 7: Run tests**

Run: `go test ./pipeline/handlers/ -run "TestHumanHandler" -v -timeout 30s`
Expected: All pass, timeout tests complete quickly.

- [ ] **Step 8: Run full suite**

Run: `go test ./... -short`
Expected: All 14 packages pass.

- [ ] **Step 9: Commit**

```bash
git add pipeline/handlers/human.go pipeline/handlers/human_test.go
git commit -m "feat(human): add timeout with default fallback for human gates (#30)"
```

---

## Task Dependency Graph

```text
Task 1 (parallel concurrency + timeout — independent)
Task 2 (human gate timeout — independent)
```

Both tasks are fully independent.
