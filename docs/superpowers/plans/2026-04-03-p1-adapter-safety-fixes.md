# P1 Adapter & Engine Safety Fixes Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Fix five P1 bugs: nil pointer guards in the dippin adapter, error wrapping with sentinel errors, deterministic map iteration, POSIX build constraint, and workflow version mapping.

**Architecture:** Five independent fixes, all self-contained. Four are in `pipeline/dippin_adapter.go`, one is a build constraint on `agent/exec/local.go`. Each task is one commit. TDD where applicable.

**Tech Stack:** Go 1.25, standard library only (`errors`, `slices`, `maps`). No new dependencies.

**Spec:** `docs/superpowers/specs/2026-04-03-p1-adapter-safety-fixes-design.md`

---

### Task 1: Add sentinel errors and %w wrapping (#33)

**Files:**
- Modify: `pipeline/dippin_adapter.go:5-11` (imports), `pipeline/dippin_adapter.go:29-37` (FromDippinIR validation), `pipeline/dippin_adapter.go:110-113` (convertNode), `pipeline/dippin_adapter.go:176-177` (extractNodeAttrs default case)
- Test: `pipeline/dippin_adapter_test.go`

This task comes first because Task 2 (nil guards) will reference the sentinel errors.

- [ ] **Step 1: Write the failing test**

Add to `pipeline/dippin_adapter_test.go`:

```go
func TestFromDippinIR_SentinelErrors(t *testing.T) {
	// nil workflow → ErrNilWorkflow
	_, err := FromDippinIR(nil)
	if !errors.Is(err, ErrNilWorkflow) {
		t.Errorf("nil workflow: got %v, want ErrNilWorkflow", err)
	}

	// missing Start → ErrMissingStart
	_, err = FromDippinIR(&ir.Workflow{Exit: "x"})
	if !errors.Is(err, ErrMissingStart) {
		t.Errorf("missing start: got %v, want ErrMissingStart", err)
	}

	// missing Exit → ErrMissingExit
	_, err = FromDippinIR(&ir.Workflow{Start: "s"})
	if !errors.Is(err, ErrMissingExit) {
		t.Errorf("missing exit: got %v, want ErrMissingExit", err)
	}

	// unknown node kind → ErrUnknownNodeKind
	_, err = FromDippinIR(&ir.Workflow{
		Name: "bad", Start: "s", Exit: "e",
		Nodes: []*ir.Node{{ID: "s", Kind: "bogus"}},
	})
	if !errors.Is(err, ErrUnknownNodeKind) {
		t.Errorf("unknown kind: got %v, want ErrUnknownNodeKind", err)
	}

	// ErrUnknownConfig is tested indirectly — it's only reachable if dippin-lang
	// adds a new NodeConfig implementation that tracker hasn't mapped yet.
	// We verify the sentinel exists and is usable with errors.Is.
	wrapped := fmt.Errorf("test: %w", ErrUnknownConfig)
	if !errors.Is(wrapped, ErrUnknownConfig) {
		t.Error("ErrUnknownConfig should be matchable via errors.Is")
	}
}
```

Add `"errors"` and `"fmt"` to the test file import block.

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./pipeline/ -run TestFromDippinIR_SentinelErrors -v`
Expected: FAIL — `ErrNilWorkflow` etc. undefined.

- [ ] **Step 3: Add sentinel errors and update call sites**

In `pipeline/dippin_adapter.go`, add `"errors"` to the import block, then add after the imports:

```go
// Sentinel errors for adapter validation failures.
var (
	ErrNilWorkflow     = errors.New("nil workflow")
	ErrMissingStart    = errors.New("workflow missing Start node")
	ErrMissingExit     = errors.New("workflow missing Exit node")
	ErrUnknownNodeKind = errors.New("unknown node kind")
	ErrUnknownConfig   = errors.New("unknown config type")
)
```

Update `FromDippinIR`:
- Line 31: `return nil, fmt.Errorf("nil workflow")` → `return nil, ErrNilWorkflow`
- Line 34: `return nil, fmt.Errorf("workflow missing Start node")` → `return nil, ErrMissingStart`
- Line 37: `return nil, fmt.Errorf("workflow missing Exit node")` → `return nil, ErrMissingExit`

Update `convertNode`:
- Line 113: `return nil, fmt.Errorf("unknown node kind: %s", irNode.Kind)` → `return nil, fmt.Errorf("%s: %w", irNode.Kind, ErrUnknownNodeKind)`

Update `extractNodeAttrs`:
- Line 177: `return fmt.Errorf("unknown config type: %T", config)` → `return fmt.Errorf("%T: %w", config, ErrUnknownConfig)`

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./pipeline/ -run TestFromDippinIR_SentinelErrors -v`
Expected: PASS

- [ ] **Step 5: Run full test suite**

Run: `go test ./pipeline/ -v`
Expected: All tests pass (existing tests still work with sentinel errors).

- [ ] **Step 6: Commit**

```bash
git add pipeline/dippin_adapter.go pipeline/dippin_adapter_test.go
git commit -m "fix(adapter): add sentinel errors and %w wrapping (#33)"
```

---

### Task 2: Add nil pointer guards in adapter (#38)

**Files:**
- Modify: `pipeline/dippin_adapter.go:53-66` (FromDippinIR loops), `pipeline/dippin_adapter.go:145-174` (extractNodeAttrs pointer cases)
- Test: `pipeline/dippin_adapter_test.go`

- [ ] **Step 1: Write the failing test**

Add to `pipeline/dippin_adapter_test.go`:

```go
func TestFromDippinIR_NilNodeSkipped(t *testing.T) {
	workflow := &ir.Workflow{
		Name:  "nil-node",
		Start: "start",
		Exit:  "exit",
		Nodes: []*ir.Node{
			{ID: "start", Kind: ir.NodeAgent, Config: ir.AgentConfig{}},
			nil, // should be skipped
			{ID: "exit", Kind: ir.NodeAgent, Config: ir.AgentConfig{}},
		},
		Edges: []*ir.Edge{
			{From: "start", To: "exit"},
			nil, // should be skipped
		},
	}

	graph, err := FromDippinIR(workflow)
	if err != nil {
		t.Fatalf("FromDippinIR failed: %v", err)
	}
	if len(graph.Nodes) != 2 {
		t.Errorf("len(graph.Nodes) = %d, want 2", len(graph.Nodes))
	}
	if len(graph.Edges) != 1 {
		t.Errorf("len(graph.Edges) = %d, want 1", len(graph.Edges))
	}
}

func TestExtractNodeAttrs_NilPointerConfig(t *testing.T) {
	attrs := map[string]string{}

	// nil pointer configs should not panic
	var agentCfg *ir.AgentConfig
	if err := extractNodeAttrs(agentCfg, attrs); err != nil {
		t.Errorf("nil *AgentConfig: unexpected error: %v", err)
	}

	var humanCfg *ir.HumanConfig
	if err := extractNodeAttrs(humanCfg, attrs); err != nil {
		t.Errorf("nil *HumanConfig: unexpected error: %v", err)
	}

	var toolCfg *ir.ToolConfig
	if err := extractNodeAttrs(toolCfg, attrs); err != nil {
		t.Errorf("nil *ToolConfig: unexpected error: %v", err)
	}

	var parallelCfg *ir.ParallelConfig
	if err := extractNodeAttrs(parallelCfg, attrs); err != nil {
		t.Errorf("nil *ParallelConfig: unexpected error: %v", err)
	}

	var fanInCfg *ir.FanInConfig
	if err := extractNodeAttrs(fanInCfg, attrs); err != nil {
		t.Errorf("nil *FanInConfig: unexpected error: %v", err)
	}

	var subgraphCfg *ir.SubgraphConfig
	if err := extractNodeAttrs(subgraphCfg, attrs); err != nil {
		t.Errorf("nil *SubgraphConfig: unexpected error: %v", err)
	}
}
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `go test ./pipeline/ -run "TestFromDippinIR_NilNodeSkipped|TestExtractNodeAttrs_NilPointerConfig" -v`
Expected: FAIL — panic on nil dereference.

- [ ] **Step 3: Add nil guards**

In `FromDippinIR`, add nil guards in the range loops:

```go
	// Map IR nodes to Graph nodes, preserving declaration order.
	for _, irNode := range workflow.Nodes {
		if irNode == nil {
			continue
		}
		gNode, err := convertNode(irNode)
```

```go
	// Map IR edges to Graph edges
	for _, irEdge := range workflow.Edges {
		if irEdge == nil {
			continue
		}
		gEdge := convertEdge(irEdge)
```

In `extractNodeAttrs`, add nil guards for each pointer case:

```go
	case *ir.AgentConfig:
		if cfg == nil {
			return nil
		}
		extractAgentAttrs(*cfg, attrs)

	case *ir.HumanConfig:
		if cfg == nil {
			return nil
		}
		extractHumanAttrs(*cfg, attrs)

	case *ir.ToolConfig:
		if cfg == nil {
			return nil
		}
		extractToolAttrs(*cfg, attrs)

	case *ir.ParallelConfig:
		if cfg == nil {
			return nil
		}
		extractParallelAttrs(*cfg, attrs)

	case *ir.FanInConfig:
		if cfg == nil {
			return nil
		}
		extractFanInAttrs(*cfg, attrs)

	case *ir.SubgraphConfig:
		if cfg == nil {
			return nil
		}
		extractSubgraphAttrs(*cfg, attrs)
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `go test ./pipeline/ -run "TestFromDippinIR_NilNodeSkipped|TestExtractNodeAttrs_NilPointerConfig" -v`
Expected: PASS

- [ ] **Step 5: Run full test suite**

Run: `go test ./pipeline/ -v`
Expected: All tests pass.

- [ ] **Step 6: Commit**

```bash
git add pipeline/dippin_adapter.go pipeline/dippin_adapter_test.go
git commit -m "fix(adapter): add nil pointer guards for IR nodes, edges, and configs (#38)"
```

---

### Task 3: Deterministic map iteration in extractSubgraphAttrs and serializeStylesheet (#8)

**Files:**
- Modify: `pipeline/dippin_adapter.go:5-11` (imports), `pipeline/dippin_adapter.go:330-336` (extractSubgraphAttrs), `pipeline/dippin_adapter.go:438-442` (serializeStylesheet)
- Test: `pipeline/dippin_adapter_test.go`

- [ ] **Step 1: Write the failing test**

Add to `pipeline/dippin_adapter_test.go`:

```go
func TestExtractSubgraphAttrs_DeterministicOrder(t *testing.T) {
	cfg := ir.SubgraphConfig{
		Ref: "my-subgraph",
		Params: map[string]string{
			"zebra": "z",
			"alpha": "a",
			"middle": "m",
		},
	}
	attrs := map[string]string{}
	extractSubgraphAttrs(cfg, attrs)
	want := "alpha=a,middle=m,zebra=z"
	if attrs["subgraph_params"] != want {
		t.Errorf("subgraph_params = %q, want %q", attrs["subgraph_params"], want)
	}

	// Run 10 times to verify determinism (Go randomizes map iteration).
	for i := 0; i < 10; i++ {
		attrs2 := map[string]string{}
		extractSubgraphAttrs(cfg, attrs2)
		if attrs2["subgraph_params"] != want {
			t.Errorf("iteration %d: subgraph_params = %q, want %q", i, attrs2["subgraph_params"], want)
		}
	}
}

func TestSerializeStylesheet_DeterministicOrder(t *testing.T) {
	rules := []ir.StylesheetRule{
		{
			Selector: ir.StyleSelector{Kind: "universal"},
			Properties: map[string]string{
				"z_prop": "z",
				"a_prop": "a",
			},
		},
	}
	result := serializeStylesheet(rules)
	// Properties should be sorted: a_prop before z_prop.
	aIdx := strings.Index(result, "a_prop")
	zIdx := strings.Index(result, "z_prop")
	if aIdx < 0 || zIdx < 0 {
		t.Fatalf("result = %q, missing properties", result)
	}
	if aIdx > zIdx {
		t.Errorf("properties not sorted: a_prop at %d, z_prop at %d in %q", aIdx, zIdx, result)
	}
}
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `go test ./pipeline/ -run "TestExtractSubgraphAttrs_DeterministicOrder|TestSerializeStylesheet_DeterministicOrder" -v -count=1`
Expected: FAIL (may pass on some runs due to randomness — `-count=1` disables caching, run a few times if needed).

- [ ] **Step 3: Sort map keys before iteration**

Add `"maps"` and `"slices"` to the import block in `pipeline/dippin_adapter.go`.

In `extractSubgraphAttrs`, replace the map range (lines 332-335):

```go
	if len(cfg.Params) > 0 {
		// Serialize params as comma-separated key=value pairs (sorted for determinism).
		var pairs []string
		for _, k := range slices.Sorted(maps.Keys(cfg.Params)) {
			pairs = append(pairs, fmt.Sprintf("%s=%s", k, cfg.Params[k]))
		}
		attrs["subgraph_params"] = strings.Join(pairs, ",")
	}
```

In `serializeStylesheet`, replace the properties range (lines 440-442):

```go
		var props []string
		for _, k := range slices.Sorted(maps.Keys(rule.Properties)) {
			props = append(props, fmt.Sprintf("%s: %s", k, rule.Properties[k]))
		}
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `go test ./pipeline/ -run "TestExtractSubgraphAttrs_DeterministicOrder|TestSerializeStylesheet_DeterministicOrder" -v -count=5`
Expected: PASS (all 5 runs).

- [ ] **Step 5: Run full test suite**

Run: `go test ./pipeline/ -v`
Expected: All tests pass.

- [ ] **Step 6: Commit**

```bash
git add pipeline/dippin_adapter.go pipeline/dippin_adapter_test.go
git commit -m "fix(adapter): deterministic map iteration in subgraph attrs and stylesheet (#8)"
```

---

### Task 4: POSIX build constraint on local.go (#28)

**Files:**
- Modify: `agent/exec/local.go:0` (add build constraint before package declaration)

- [ ] **Step 1: Add build constraint**

Add `//go:build !windows` as the very first line of `agent/exec/local.go`, before the ABOUTME comments:

```go
//go:build !windows

// ABOUTME: LocalEnvironment implements ExecutionEnvironment for local filesystem and process execution.
// ABOUTME: Enforces path containment within the working directory to prevent traversal attacks.
package exec
```

- [ ] **Step 2: Verify build**

Run: `go build ./...`
Expected: Success (no change on Linux/macOS).

- [ ] **Step 3: Verify tests**

Run: `go test ./agent/exec/ -v`
Expected: All tests pass.

- [ ] **Step 4: Commit**

```bash
git add agent/exec/local.go
git commit -m "fix(exec): add POSIX build constraint to local.go (#28)"
```

---

### Task 5: Map Workflow.Version to graph attrs (#25)

**Files:**
- Modify: `pipeline/dippin_adapter.go:44-47` (after goal mapping in FromDippinIR)
- Test: `pipeline/dippin_adapter_test.go`

- [ ] **Step 1: Write the failing test**

Add to `pipeline/dippin_adapter_test.go`:

```go
func TestFromDippinIR_WorkflowVersionMapped(t *testing.T) {
	workflow := &ir.Workflow{
		Name:    "versioned",
		Start:   "start",
		Exit:    "exit",
		Version: "1",
		Nodes: []*ir.Node{
			{ID: "start", Kind: ir.NodeAgent, Config: ir.AgentConfig{}},
			{ID: "exit", Kind: ir.NodeAgent, Config: ir.AgentConfig{}},
		},
		Edges: []*ir.Edge{
			{From: "start", To: "exit"},
		},
	}

	graph, err := FromDippinIR(workflow)
	if err != nil {
		t.Fatalf("FromDippinIR failed: %v", err)
	}
	if graph.Attrs["version"] != "1" {
		t.Errorf("graph.Attrs[version] = %q, want %q", graph.Attrs["version"], "1")
	}
}

func TestFromDippinIR_EmptyVersionOmitted(t *testing.T) {
	workflow := &ir.Workflow{
		Name:  "no-version",
		Start: "start",
		Exit:  "exit",
		Nodes: []*ir.Node{
			{ID: "start", Kind: ir.NodeAgent, Config: ir.AgentConfig{}},
			{ID: "exit", Kind: ir.NodeAgent, Config: ir.AgentConfig{}},
		},
		Edges: []*ir.Edge{
			{From: "start", To: "exit"},
		},
	}

	graph, err := FromDippinIR(workflow)
	if err != nil {
		t.Fatalf("FromDippinIR failed: %v", err)
	}
	if _, ok := graph.Attrs["version"]; ok {
		t.Error("empty version should not be set in attrs")
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./pipeline/ -run "TestFromDippinIR_WorkflowVersionMapped|TestFromDippinIR_EmptyVersionOmitted" -v`
Expected: FAIL — `graph.Attrs["version"]` is empty.

- [ ] **Step 3: Add version mapping**

In `FromDippinIR`, after the goal mapping block (after line 47), add:

```go
	if workflow.Version != "" {
		g.Attrs["version"] = workflow.Version
	}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./pipeline/ -run "TestFromDippinIR_WorkflowVersionMapped|TestFromDippinIR_EmptyVersionOmitted" -v`
Expected: PASS

- [ ] **Step 5: Run full test suite and build**

Run: `go build ./... && go test ./... -short`
Expected: Build succeeds, all 14 packages pass.

- [ ] **Step 6: Commit**

```bash
git add pipeline/dippin_adapter.go pipeline/dippin_adapter_test.go
git commit -m "fix(adapter): map Workflow.Version to graph attrs (#25)"
```

---

## Task Dependency Graph

```text
Task 1 (sentinel errors)
  └─ Task 2 (nil guards — references sentinel errors pattern)
Task 3 (deterministic map iteration — independent)
Task 4 (build constraint — independent)
Task 5 (version mapping — independent)
```

Tasks 1→2 are sequential. Tasks 3, 4, 5 are independent and can be done in any order after Task 1.
