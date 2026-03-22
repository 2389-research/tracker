# Implementation Plan: Wire SubgraphHandler into Registry

**Date:** 2026-03-21  
**Priority:** P0 - Critical (blocks subgraph functionality)  
**Effort:** 6-8 hours  
**Status:** Ready to Execute

---

## Problem Statement

The `SubgraphHandler` is fully implemented and tested but **not registered** in the default handler registry. This causes runtime errors when executing `.dip` files with `subgraph` nodes:

```
error: no handler registered for "subgraph" (node "MySubgraph")
```

**Evidence:**
```bash
$ tracker parent.dip
[19:42:52] stage_failed  node=Child  handler error: no handler registered for "subgraph"
```

**Root Cause:** `handlers.NewDefaultRegistry()` doesn't call `Register()` for SubgraphHandler.

---

## Solution Design

### Architecture

**Current flow:**
```
main.go
  ├─ loadPipeline(file) → Graph
  └─ NewDefaultRegistry(graph)
       ├─ Register(StartHandler)
       ├─ Register(CodergenHandler)
       └─ ... (NO SubgraphHandler!)
```

**Proposed flow:**
```
main.go
  ├─ loadPipelineWithSubgraphs(file) → Graph + map[string]*Graph
  └─ NewDefaultRegistry(graph, WithSubgraphs(subgraphs))
       ├─ Register(StartHandler)
       ├─ Register(SubgraphHandler) ← NEW
       └─ ...
```

### Key Design Decisions

**1. Auto-Discovery vs Manual Registration**

| Approach | Pros | Cons |
|----------|------|------|
| **Auto-discovery** (recommended) | Zero config, works transparently | Needs recursive loader |
| Manual (`WithSubgraphs(...)`) | Simple, explicit | User must load graphs manually |

**Decision:** Auto-discovery with recursive loading.

**2. Path Resolution**

```go
// Subgraph ref in parent.dip:
subgraph Child
  ref: child.dip              // Relative to parent dir
  ref: ./child.dip            // Explicit relative
  ref: /abs/path/child.dip    // Absolute
  ref: subgraphs/child.dip    // Nested relative
```

**Resolution logic:**
```go
func resolveSubgraphPath(parentPath, ref string) string {
    if filepath.IsAbs(ref) {
        return ref
    }
    // Relative to parent's directory
    return filepath.Join(filepath.Dir(parentPath), ref)
}
```

**3. Cycle Detection**

```go
// Detect: A → B → C → A
func loadWithCycleDetection(path string, visited map[string]bool) (*Graph, error) {
    if visited[path] {
        return nil, fmt.Errorf("circular subgraph ref: %s", path)
    }
    visited[path] = true
    defer delete(visited, path)
    // ... load and recurse
}
```

---

## Implementation Steps

### Step 1: Add SubgraphLoader

**File:** `cmd/tracker/subgraph_loader.go` (new)

```go
package main

import (
	"fmt"
	"path/filepath"
	"github.com/2389-research/tracker/pipeline"
)

// subgraphRegistry holds loaded subgraphs indexed by their canonical path.
type subgraphRegistry struct {
	graphs map[string]*pipeline.Graph
}

func newSubgraphRegistry() *subgraphRegistry {
	return &subgraphRegistry{
		graphs: make(map[string]*pipeline.Graph),
	}
}

// loadPipelineWithSubgraphs loads a pipeline and recursively loads all
// referenced subgraphs. Returns the root graph and a map of all subgraphs
// indexed by their reference path (as specified in subgraph_ref attributes).
func loadPipelineWithSubgraphs(filename, formatOverride string) (*pipeline.Graph, map[string]*pipeline.Graph, error) {
	reg := newSubgraphRegistry()
	graph, err := reg.loadRecursive(filename, formatOverride, make(map[string]bool))
	if err != nil {
		return nil, nil, err
	}
	return graph, reg.graphs, nil
}

// loadRecursive loads a graph and all its subgraph dependencies.
// The visited map tracks the current path to detect cycles.
func (r *subgraphRegistry) loadRecursive(path, formatOverride string, visited map[string]bool) (*pipeline.Graph, error) {
	// Canonicalize path for cycle detection
	absPath, err := filepath.Abs(path)
	if err != nil {
		return nil, fmt.Errorf("resolve path %q: %w", path, err)
	}

	// Detect cycles
	if visited[absPath] {
		return nil, fmt.Errorf("circular subgraph reference: %s", absPath)
	}
	visited[absPath] = true
	defer delete(visited, absPath)

	// Check cache
	if cached, ok := r.graphs[path]; ok {
		return cached, nil
	}

	// Load the graph
	graph, err := loadPipeline(path, formatOverride)
	if err != nil {
		return nil, fmt.Errorf("load %q: %w", path, err)
	}

	// Discover subgraph refs in this graph
	refs := discoverSubgraphRefs(graph)

	// Recursively load each referenced subgraph
	for _, ref := range refs {
		refPath := resolveSubgraphPath(path, ref)
		subGraph, err := r.loadRecursive(refPath, "", visited)
		if err != nil {
			return nil, fmt.Errorf("load subgraph %q referenced from %q: %w", ref, path, err)
		}
		// Cache by the ref string (not absPath) so lookups match node attrs
		r.graphs[ref] = subGraph
	}

	// Cache this graph
	r.graphs[path] = graph

	return graph, nil
}

// discoverSubgraphRefs extracts all unique subgraph_ref attribute values.
func discoverSubgraphRefs(g *pipeline.Graph) []string {
	seen := make(map[string]bool)
	var refs []string
	for _, node := range g.Nodes {
		if ref, ok := node.Attrs["subgraph_ref"]; ok && ref != "" {
			if !seen[ref] {
				refs = append(refs, ref)
				seen[ref] = true
			}
		}
	}
	return refs
}

// resolveSubgraphPath resolves a subgraph ref to an absolute or relative path.
// If ref is absolute, returns it unchanged.
// Otherwise, resolves relative to the parent file's directory.
func resolveSubgraphPath(parentPath, ref string) string {
	if filepath.IsAbs(ref) {
		return ref
	}
	return filepath.Join(filepath.Dir(parentPath), ref)
}
```

---

### Step 2: Add WithSubgraphs Option

**File:** `pipeline/handlers/registry.go`

```go
// Add to registryConfig:
type registryConfig struct {
	// ... existing fields
	subgraphs map[string]*pipeline.Graph
}

// Add option constructor:
// WithSubgraphs provides a map of loaded subgraphs for the subgraph handler.
// The map key should match the subgraph_ref attribute value in nodes.
func WithSubgraphs(graphs map[string]*pipeline.Graph) RegistryOption {
	return func(c *registryConfig) {
		c.subgraphs = graphs
	}
}

// In NewDefaultRegistry, after other handlers:
// Register SubgraphHandler if subgraphs are provided.
if len(cfg.subgraphs) > 0 {
	registry.Register(pipeline.NewSubgraphHandler(cfg.subgraphs, registry))
}
```

---

### Step 3: Update main.go to Use Loader

**File:** `cmd/tracker/main.go`

**Change in `run()` function:**

```go
func run(pipelineFile, workdir, checkpoint, format string, verbose bool, jsonOut bool) error {
	// OLD:
	// graph, err := loadPipeline(pipelineFile, format)

	// NEW:
	graph, subgraphs, err := loadPipelineWithSubgraphs(pipelineFile, format)
	if err != nil {
		return fmt.Errorf("load pipeline: %w", err)
	}

	// ... existing validation ...

	// Build handler registry WITH subgraphs
	registry := handlers.NewDefaultRegistry(graph,
		handlers.WithLLMClient(llmClient, workdir),
		handlers.WithExecEnvironment(execEnv),
		handlers.WithInterviewer(interviewer, graph),
		handlers.WithAgentEventHandler(agentEventHandler),
		handlers.WithPipelineEventHandler(pipelineEventHandler),
		handlers.WithSubgraphs(subgraphs), // ← ADD THIS
	)

	// ... rest of function unchanged
}
```

**Same change in `runTUI()` function.**

**Change in `runValidateCmd()` function:**

```go
func runValidateCmd(pipelineFile, format string, w io.Writer) error {
	// OLD:
	// graph, err := loadPipeline(pipelineFile, format)

	// NEW:
	graph, _, err := loadPipelineWithSubgraphs(pipelineFile, format)
	// (subgraphs not needed for validation, but loading ensures refs resolve)

	// ... rest unchanged
}
```

**Same for `runSimulateCmd()`.**

---

### Step 4: Add Tests

**File:** `cmd/tracker/subgraph_loader_test.go` (new)

```go
package main

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLoadPipelineWithSubgraphs_Simple(t *testing.T) {
	dir := t.TempDir()

	// Create child
	childPath := filepath.Join(dir, "child.dip")
	childContent := `
workflow Child
  start: Work
  exit: Work
  agent Work
    prompt: Do work.
    auto_status: true
`
	if err := os.WriteFile(childPath, []byte(childContent), 0644); err != nil {
		t.Fatal(err)
	}

	// Create parent
	parentPath := filepath.Join(dir, "parent.dip")
	parentContent := `
workflow Parent
  start: Call
  exit: Call
  subgraph Call
    ref: child.dip
`
	if err := os.WriteFile(parentPath, []byte(parentContent), 0644); err != nil {
		t.Fatal(err)
	}

	// Load
	graph, subgraphs, err := loadPipelineWithSubgraphs(parentPath, "")
	if err != nil {
		t.Fatalf("load failed: %v", err)
	}

	if graph.Name != "Parent" {
		t.Errorf("expected root graph name=Parent, got %q", graph.Name)
	}

	if len(subgraphs) != 2 { // parent + child
		t.Errorf("expected 2 subgraphs (parent+child), got %d", len(subgraphs))
	}

	child, ok := subgraphs["child.dip"]
	if !ok {
		t.Fatalf("subgraph 'child.dip' not loaded")
	}
	if child.Name != "Child" {
		t.Errorf("expected child name=Child, got %q", child.Name)
	}
}

func TestLoadPipelineWithSubgraphs_CycleDetection(t *testing.T) {
	dir := t.TempDir()

	// Create A.dip that refs B.dip
	aPath := filepath.Join(dir, "A.dip")
	aContent := `
workflow A
  start: CallB
  exit: CallB
  subgraph CallB
    ref: B.dip
`
	os.WriteFile(aPath, []byte(aContent), 0644)

	// Create B.dip that refs A.dip (cycle!)
	bPath := filepath.Join(dir, "B.dip")
	bContent := `
workflow B
  start: CallA
  exit: CallA
  subgraph CallA
    ref: A.dip
`
	os.WriteFile(bPath, []byte(bContent), 0644)

	// Load should fail
	_, _, err := loadPipelineWithSubgraphs(aPath, "")
	if err == nil {
		t.Fatal("expected error for circular ref, got nil")
	}
	if !contains(err.Error(), "circular") {
		t.Errorf("expected 'circular' in error, got: %v", err)
	}
}

func TestLoadPipelineWithSubgraphs_NestedSubgraphs(t *testing.T) {
	dir := t.TempDir()

	// Grandchild
	gcPath := filepath.Join(dir, "grandchild.dip")
	gcContent := `
workflow Grandchild
  start: Leaf
  exit: Leaf
  agent Leaf
    prompt: Leaf node.
`
	os.WriteFile(gcPath, []byte(gcContent), 0644)

	// Child refs grandchild
	childPath := filepath.Join(dir, "child.dip")
	childContent := `
workflow Child
  start: CallGC
  exit: CallGC
  subgraph CallGC
    ref: grandchild.dip
`
	os.WriteFile(childPath, []byte(childContent), 0644)

	// Parent refs child
	parentPath := filepath.Join(dir, "parent.dip")
	parentContent := `
workflow Parent
  start: CallChild
  exit: CallChild
  subgraph CallChild
    ref: child.dip
`
	os.WriteFile(parentPath, []byte(parentContent), 0644)

	// Load
	graph, subgraphs, err := loadPipelineWithSubgraphs(parentPath, "")
	if err != nil {
		t.Fatalf("load failed: %v", err)
	}

	// Should have parent + child + grandchild
	if len(subgraphs) != 3 {
		t.Errorf("expected 3 graphs, got %d", len(subgraphs))
	}

	if _, ok := subgraphs["child.dip"]; !ok {
		t.Error("child.dip not loaded")
	}
	if _, ok := subgraphs["grandchild.dip"]; !ok {
		t.Error("grandchild.dip not loaded")
	}
}

func TestLoadPipelineWithSubgraphs_MissingRef(t *testing.T) {
	dir := t.TempDir()

	parentPath := filepath.Join(dir, "parent.dip")
	parentContent := `
workflow Parent
  start: Bad
  exit: Bad
  subgraph Bad
    ref: nonexistent.dip
`
	os.WriteFile(parentPath, []byte(parentContent), 0644)

	_, _, err := loadPipelineWithSubgraphs(parentPath, "")
	if err == nil {
		t.Fatal("expected error for missing subgraph, got nil")
	}
}

func contains(s, substr string) bool {
	return len(s) > 0 && len(substr) > 0 && len(s) >= len(substr) && 
		(s == substr || (len(s) > len(substr) && 
			(s[:len(substr)] == substr || s[len(s)-len(substr):] == substr || 
			 len(s) > len(substr) && anyContains(s, substr))))
}

func anyContains(s, substr string) bool {
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}
```

**File:** `cmd/tracker/subgraph_integration_test.go` (new)

```go
package main

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestSubgraphIntegration_E2E(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in short mode")
	}

	// Ensure LLM client is available
	if os.Getenv("ANTHROPIC_API_KEY") == "" && os.Getenv("OPENAI_API_KEY") == "" {
		t.Skip("no LLM API key available")
	}

	dir := t.TempDir()

	// Child workflow
	childPath := filepath.Join(dir, "child.dip")
	childContent := `
workflow Child
  start: Work
  exit: Work
  agent Work
    label: Child Agent
    prompt: |
      You are a child subgraph.
      Task: ${params.task}
      Output: STATUS:success
    auto_status: true
    model: gpt-5.4-turbo
    provider: openai
`
	os.WriteFile(childPath, []byte(childContent), 0644)

	// Parent workflow
	parentPath := filepath.Join(dir, "parent.dip")
	parentContent := `
workflow Parent
  start: Start
  exit: End
  
  agent Start
    label: Start
  
  subgraph CallChild
    ref: child.dip
    params:
      task: Test subgraph execution
  
  agent End
    label: End
  
  edges
    Start -> CallChild
    CallChild -> End
`
	os.WriteFile(parentPath, []byte(parentContent), 0644)

	// Execute
	cfg := runConfig{
		mode:         modeRun,
		pipelineFile: parentPath,
		workdir:      dir,
		noTUI:        true,
	}

	err := executeCommand(cfg, commandDeps{
		loadEnv: func(string) error { return nil },
		run:     run,
		runTUI:  runTUI,
	})

	if err != nil {
		t.Fatalf("pipeline execution failed: %v", err)
	}
}
```

---

### Step 5: Update Documentation

**File:** `README.md`

Add after the "Variable Interpolation" section:

```markdown
### Subgraphs

Embed sub-pipelines for modular, reusable workflows. Subgraphs execute as a single node step, with context propagation and parameterization:

```dip
# parent.dip
workflow SecurityPipeline
  start: Scan
  exit: Report
  
  subgraph Scan
    ref: scanners/vulnerability_scan.dip
    params:
      severity: critical
      target_dir: ./src
  
  agent Report
    prompt: |
      Scan results: ${ctx.last_response}
      Generate executive summary.
  
  edges
    Scan -> Report

# scanners/vulnerability_scan.dip
workflow VulnerabilityScan
  start: Execute
  exit: Execute
  
  agent Execute
    model: claude-opus-4-6
    prompt: |
      Scan ${params.target_dir} for ${params.severity} vulnerabilities.
      Report findings.
```

**Features:**
- **Nested execution** — Subgraph runs as a single parent node step
- **Parameter passing** — `params:` in parent become `${params.*}` in child
- **Context propagation** — Child can write context keys, parent reads them
- **Recursive loading** — Nested subgraphs work transitively
- **Path resolution** — Relative paths resolve from parent's directory
```

**File:** `examples/subgraph_demo.dip` (new)

```dip
workflow SubgraphDemo
  goal: "Demonstrate subgraph parameterization and context flow"
  start: Prepare
  exit: Summary
  
  agent Prepare
    label: Prepare Data
    prompt: |
      Prepare test data for security scan.
      Output: STATUS:success
    auto_status: true
  
  subgraph SecurityScan
    ref: subgraphs/scanner.dip
    params:
      severity: high
      target: ./src
      model: claude-sonnet-4-6
  
  agent Summary
    label: Generate Summary
    prompt: |
      Security scan results: ${ctx.last_response}
      
      Generate an executive summary with:
      1. Total issues found
      2. Severity breakdown
      3. Recommended actions
  
  edges
    Prepare -> SecurityScan
    SecurityScan -> Summary
```

**File:** `examples/subgraphs/scanner.dip` (new)

```dip
workflow Scanner
  goal: "Parameterized vulnerability scanner"
  start: Scan
  exit: Scan
  
  agent Scan
    label: Vulnerability Scan
    model: ${params.model}
    prompt: |
      Target: ${params.target}
      Severity filter: ${params.severity}
      
      Perform static analysis and report vulnerabilities.
      Include:
      - Vulnerability type
      - Severity level
      - File and line number
      - Remediation advice
      
      Output: STATUS:success when complete.
    auto_status: true
```

---

## Testing Checklist

### Unit Tests
- [x] `TestLoadPipelineWithSubgraphs_Simple` - Basic parent→child
- [x] `TestLoadPipelineWithSubgraphs_CycleDetection` - A→B→A fails
- [x] `TestLoadPipelineWithSubgraphs_NestedSubgraphs` - 3 levels deep
- [x] `TestLoadPipelineWithSubgraphs_MissingRef` - nonexistent.dip fails

### Integration Tests
- [ ] `TestSubgraphIntegration_E2E` - Full pipeline with LLM
- [ ] Param expansion works: `${params.task}` in child
- [ ] Context propagation: child writes, parent reads
- [ ] Relative path resolution: `./child.dip`, `subgraphs/child.dip`

### Manual Tests
```bash
# Simple parent→child
tracker examples/subgraph_demo.dip

# Nested subgraphs
tracker examples/variable_interpolation_demo.dip

# Verify error messages
tracker /tmp/bad_ref.dip  # Should show clear "subgraph not found" error
```

---

## Rollout Plan

### Phase 1: Core Implementation (2 hours)
1. Create `subgraph_loader.go` with recursive loader
2. Add `WithSubgraphs()` option to registry
3. Update `main.go` to use loader
4. Manual smoke test

### Phase 2: Testing (2 hours)
1. Write unit tests for loader
2. Write integration test
3. Run full test suite
4. Fix any issues

### Phase 3: Documentation (1 hour)
1. Update README with subgraph example
2. Create example workflows
3. Update feature parity index

### Phase 4: Validation (1 hour)
1. Test all existing `.dip` files
2. Verify no regressions
3. Test edge cases (cycles, missing refs, deep nesting)
4. Commit

**Total: 6 hours**

---

## Success Metrics

**Before:**
```bash
$ tracker parent.dip
error: no handler registered for "subgraph" (node "Child")
```

**After:**
```bash
$ tracker parent.dip
[19:43:01] pipeline_started
[19:43:01] stage_started    node=Start
[19:43:01] stage_completed  node=Start
[19:43:01] stage_started    node=Child        # ← SUBGRAPH EXECUTES!
[19:43:02] stage_completed  node=Child
[19:43:02] pipeline_complete status=success
```

**Acceptance Criteria:**
- [ ] All existing tests pass
- [ ] 4 new unit tests pass
- [ ] 1 integration test passes
- [ ] `examples/variable_interpolation_demo.dip` works
- [ ] `examples/subgraph_demo.dip` works
- [ ] README updated
- [ ] No breaking changes

---

## Risks & Mitigation

| Risk | Impact | Mitigation |
|------|--------|------------|
| Circular refs cause stack overflow | High | Detect cycles during load, fail with clear error |
| Relative path bugs | Medium | Test with `./`, `../`, nested dirs |
| Subgraph not found | Medium | Clear error message with resolved path |
| Performance (large graphs) | Low | Cache loaded graphs, avoid re-parsing |
| Breaking changes | High | Feature is additive, existing workflows unaffected |

---

## Next Steps

1. **Create branch:** `git checkout -b feat/wire-subgraph-handler`
2. **Implement Phase 1:** Core loader + registry option
3. **Test locally:** `tracker examples/subgraph_demo.dip`
4. **Implement Phase 2:** Write tests
5. **Run test suite:** `go test ./...`
6. **Implement Phase 3:** Documentation
7. **Commit:** `feat(subgraph): wire SubgraphHandler with auto-discovery`
8. **Verify:** `tracker examples/variable_interpolation_demo.dip`

---

## Appendix: Error Messages

**Before (confusing):**
```
error: no handler registered for "subgraph"
```

**After (helpful):**
```
error: load subgraph "child.dip" referenced from "parent.dip": file not found
  hint: check that the path is relative to parent's directory
  resolved: /abs/path/to/child.dip
```

**Circular ref:**
```
error: circular subgraph reference: A.dip → B.dip → A.dip
```

**Missing ref attr:**
```
error: subgraph node "Child" missing subgraph_ref attribute
```
