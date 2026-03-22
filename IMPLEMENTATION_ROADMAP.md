# Dippin-Lang Feature Implementation Roadmap

**Date:** 2024-03-21  
**Status:** Ready for Execution  
**Estimated Total Time:** 6.5-8.5 hours (1-2 days)

---

## Quick Navigation

| Priority | Features | Time | Document |
|----------|----------|------|----------|
| **P0 Critical** | Subgraph safety & wiring | 3.5-5.5h | This document - Phase 1 |
| **P1 Important** | Variable interpolation, weights | 3h | This document - Phase 2 |
| **P2 Optional** | Enhanced features | 8-10h | Backlog section |

---

## Phase 1: Critical Fixes (Day 1 - 5.5 hours)

### Task 1.1: Circular Subgraph Protection (1.5 hours)

**Objective:** Prevent infinite recursion crashes from circular subgraph references

**Files to Modify:**
- `pipeline/subgraph.go`
- `pipeline/subgraph_test.go`

**Step-by-Step:**

#### Step 1: Add constants (5 min)
```go
// pipeline/subgraph.go - Add after imports

const (
    // MaxSubgraphDepth is the maximum nesting level for subgraph calls.
    // Prevents stack overflow from circular references.
    MaxSubgraphDepth = 32
    
    // InternalKeySubgraphDepth tracks current nesting depth in context
    InternalKeySubgraphDepth = "_subgraph_depth"
)
```

#### Step 2: Add depth tracking to Execute method (30 min)
```go
// pipeline/subgraph.go - Replace Execute method

func (h *SubgraphHandler) Execute(ctx context.Context, node *Node, pctx *PipelineContext) (Outcome, error) {
    ref, ok := node.Attrs["subgraph_ref"]
    if !ok || ref == "" {
        return Outcome{Status: OutcomeFail}, fmt.Errorf("node %q missing subgraph_ref attribute", node.ID)
    }

    // ===== NEW: Track recursion depth =====
    depthStr, _ := pctx.GetInternal(InternalKeySubgraphDepth)
    depth := 0
    if depthStr != "" {
        if d, err := strconv.Atoi(depthStr); err == nil {
            depth = d
        }
    }
    
    // Check max depth to prevent infinite recursion
    if depth >= MaxSubgraphDepth {
        return Outcome{Status: OutcomeFail}, fmt.Errorf(
            "subgraph %q: max nesting depth (%d) exceeded - possible circular reference",
            ref, MaxSubgraphDepth,
        )
    }
    // ===== END NEW =====

    subGraph, ok := h.graphs[ref]
    if !ok {
        return Outcome{Status: OutcomeFail}, fmt.Errorf("subgraph %q not found", ref)
    }

    params := ParseSubgraphParams(node.Attrs["subgraph_params"])
    subGraphWithParams, err := InjectParamsIntoGraph(subGraph, params)
    if err != nil {
        return Outcome{Status: OutcomeFail}, fmt.Errorf("failed to inject params into subgraph %q: %w", ref, err)
    }

    // ===== NEW: Increment depth for child =====
    childContext := WithInitialContext(pctx.Snapshot())
    childContext.SetInternal(InternalKeySubgraphDepth, strconv.Itoa(depth+1))
    
    engine := NewEngine(subGraphWithParams, h.registry, childContext)
    // ===== END NEW =====
    
    result, err := engine.Run(ctx)
    if err != nil {
        return Outcome{Status: OutcomeFail}, fmt.Errorf("subgraph %q execution failed: %w", ref, err)
    }

    status := OutcomeSuccess
    if result.Status != OutcomeSuccess {
        status = OutcomeFail
    }

    return Outcome{
        Status:         status,
        ContextUpdates: result.Context,
    }, nil
}
```

#### Step 3: Add import (1 min)
```go
// pipeline/subgraph.go - Add to imports
import (
    "context"
    "fmt"
    "strconv"  // ADD THIS
)
```

#### Step 4: Add test case (30 min)
```go
// pipeline/subgraph_test.go - Add at end

func TestSubgraphHandler_CircularReference(t *testing.T) {
    // Graph A calls B
    graphA := NewGraph("A")
    graphA.StartNode = "start"
    graphA.ExitNode = "exit"
    graphA.AddNode(&Node{ID: "start", Handler: "start"})
    graphA.AddNode(&Node{
        ID:      "call_b",
        Handler: "subgraph",
        Attrs:   map[string]string{"subgraph_ref": "B"},
    })
    graphA.AddNode(&Node{ID: "exit", Handler: "exit"})
    graphA.AddEdge(&Edge{From: "start", To: "call_b"})
    graphA.AddEdge(&Edge{From: "call_b", To: "exit"})

    // Graph B calls A (creates cycle)
    graphB := NewGraph("B")
    graphB.StartNode = "start"
    graphB.ExitNode = "exit"
    graphB.AddNode(&Node{ID: "start", Handler: "start"})
    graphB.AddNode(&Node{
        ID:      "call_a",
        Handler: "subgraph",
        Attrs:   map[string]string{"subgraph_ref": "A"},
    })
    graphB.AddNode(&Node{ID: "exit", Handler: "exit"})
    graphB.AddEdge(&Edge{From: "start", To: "call_a"})
    graphB.AddEdge(&Edge{From: "call_a", To: "exit"})

    // Register handlers
    registry := NewHandlerRegistry()
    registry.Register(&StartHandler{})
    registry.Register(&ExitHandler{})
    
    graphs := map[string]*Graph{"A": graphA, "B": graphB}
    registry.Register(NewSubgraphHandler(graphs, registry))

    // Execute - should fail gracefully
    engine := NewEngine(graphA, registry)
    ctx := context.Background()
    
    result, err := engine.Run(ctx)
    
    // Verify error
    if err == nil {
        t.Fatal("expected error for circular reference, got nil")
    }
    
    if !strings.Contains(err.Error(), "max nesting depth") {
        t.Errorf("expected 'max nesting depth' in error, got: %v", err)
    }
    
    if result.Status != OutcomeFail {
        t.Errorf("expected OutcomeFail, got %s", result.Status)
    }
}
```

#### Step 5: Verify (15 min)
```bash
# Run new test
go test ./pipeline -run TestSubgraphHandler_CircularReference -v

# Run all subgraph tests
go test ./pipeline -run TestSubgraphHandler -v

# Full test suite
go test ./...

# Check coverage
go test ./pipeline -cover
```

**Acceptance:**
- ✅ Test passes
- ✅ No regressions in existing tests
- ✅ Error message includes "max nesting depth"
- ✅ Coverage maintained

---

### Task 1.2: Wire SubgraphHandler in Default Registry (4 hours)

**Objective:** Make subgraphs work out-of-the-box without manual registry setup

**Files to Create:**
- `cmd/tracker/subgraph_loader.go`
- `cmd/tracker/subgraph_loader_test.go`

**Files to Modify:**
- `cmd/tracker/main.go`

#### Step 1: Create workflow loader (2 hours)

Create `cmd/tracker/subgraph_loader.go`:
```go
// ABOUTME: Auto-discovery and loading of subgraph workflows
package main

import (
    "fmt"
    "os"
    "path/filepath"
    "strings"
    
    "github.com/2389-research/dippin-lang/parser"
    "github.com/2389-research/tracker/pipeline"
)

// LoadWorkflowWithSubgraphs loads a workflow and recursively discovers
// and loads all referenced subgraphs. Returns the main graph and a map
// of all discovered subgraphs.
func LoadWorkflowWithSubgraphs(mainPath string) (*pipeline.Graph, map[string]*pipeline.Graph, error) {
    // Parse main workflow
    mainGraph, err := loadSingleWorkflow(mainPath)
    if err != nil {
        return nil, nil, fmt.Errorf("failed to load main workflow: %w", err)
    }
    
    // Discover subgraph references
    refs := extractSubgraphRefs(mainGraph)
    if len(refs) == 0 {
        return mainGraph, nil, nil // No subgraphs
    }
    
    // Load all subgraphs recursively
    subgraphs := make(map[string]*pipeline.Graph)
    baseDir := filepath.Dir(mainPath)
    visited := make(map[string]bool)
    
    queue := refs
    for len(queue) > 0 {
        ref := queue[0]
        queue = queue[1:]
        
        if visited[ref] {
            continue
        }
        visited[ref] = true
        
        // Try multiple resolution strategies
        subPath := resolveSubgraphPath(ref, baseDir)
        if subPath == "" {
            return nil, nil, fmt.Errorf("subgraph %q not found", ref)
        }
        
        sg, err := loadSingleWorkflow(subPath)
        if err != nil {
            return nil, nil, fmt.Errorf("failed to load subgraph %q: %w", ref, err)
        }
        
        subgraphs[ref] = sg
        
        // Recursively discover nested subgraphs
        nestedRefs := extractSubgraphRefs(sg)
        for _, nr := range nestedRefs {
            if !visited[nr] {
                queue = append(queue, nr)
            }
        }
    }
    
    // Detect circular dependencies
    if hasCircularDeps(mainGraph, subgraphs) {
        return nil, nil, fmt.Errorf("circular subgraph dependencies detected")
    }
    
    return mainGraph, subgraphs, nil
}

// loadSingleWorkflow parses a .dip file into a Graph
func loadSingleWorkflow(path string) (*pipeline.Graph, error) {
    data, err := os.ReadFile(path)
    if err != nil {
        return nil, err
    }
    
    // Parse using dippin-lang parser
    ir, err := parser.Parse(string(data))
    if err != nil {
        return nil, err
    }
    
    // Convert IR to Graph
    graph := pipeline.IRToGraph(ir)
    return graph, nil
}

// extractSubgraphRefs finds all subgraph_ref attributes in a graph
func extractSubgraphRefs(g *pipeline.Graph) []string {
    refs := []string{}
    seen := make(map[string]bool)
    
    for _, node := range g.Nodes {
        if node.Handler == "subgraph" {
            if ref, ok := node.Attrs["subgraph_ref"]; ok && ref != "" {
                if !seen[ref] {
                    refs = append(refs, ref)
                    seen[ref] = true
                }
            }
        }
    }
    
    return refs
}

// resolveSubgraphPath tries multiple strategies to find subgraph file
func resolveSubgraphPath(ref, baseDir string) string {
    // Strategy 1: Exact path (relative or absolute)
    if filepath.IsAbs(ref) {
        if fileExists(ref) {
            return ref
        }
        return ""
    }
    
    // Strategy 2: Relative to base directory
    relPath := filepath.Join(baseDir, ref)
    if fileExists(relPath) {
        return relPath
    }
    
    // Strategy 3: Add .dip extension
    if !strings.HasSuffix(ref, ".dip") {
        withExt := ref + ".dip"
        relPath := filepath.Join(baseDir, withExt)
        if fileExists(relPath) {
            return relPath
        }
    }
    
    // Strategy 4: Look in subgraphs/ subdirectory
    subgraphDir := filepath.Join(baseDir, "subgraphs", ref)
    if fileExists(subgraphDir) {
        return subgraphDir
    }
    
    if !strings.HasSuffix(ref, ".dip") {
        subgraphDir := filepath.Join(baseDir, "subgraphs", ref+".dip")
        if fileExists(subgraphDir) {
            return subgraphDir
        }
    }
    
    return ""
}

// fileExists checks if a file exists
func fileExists(path string) bool {
    _, err := os.Stat(path)
    return err == nil
}

// hasCircularDeps detects circular dependencies using DFS
func hasCircularDeps(main *pipeline.Graph, subgraphs map[string]*pipeline.Graph) bool {
    visited := make(map[string]bool)
    recStack := make(map[string]bool)
    
    var dfs func(string) bool
    dfs = func(graphName string) bool {
        visited[graphName] = true
        recStack[graphName] = true
        
        var g *pipeline.Graph
        if graphName == main.Name {
            g = main
        } else {
            g = subgraphs[graphName]
        }
        
        if g == nil {
            return false
        }
        
        // Check all subgraph refs
        refs := extractSubgraphRefs(g)
        for _, ref := range refs {
            if !visited[ref] {
                if dfs(ref) {
                    return true
                }
            } else if recStack[ref] {
                return true // Found cycle
            }
        }
        
        recStack[graphName] = false
        return false
    }
    
    return dfs(main.Name)
}
```

#### Step 2: Add tests for loader (1 hour)

Create `cmd/tracker/subgraph_loader_test.go`:
```go
package main

import (
    "os"
    "path/filepath"
    "testing"
)

func TestLoadWorkflowWithSubgraphs_Simple(t *testing.T) {
    // Create temp directory
    tmpDir := t.TempDir()
    
    // Create main workflow
    mainPath := filepath.Join(tmpDir, "main.dip")
    mainContent := `
workflow main
  start: Start
  exit: End
  
  agent Start
    prompt: "Starting"
  
  subgraph CallChild
    ref: child
  
  agent End
    prompt: "Done"
  
  edges
    Start -> CallChild
    CallChild -> End
`
    os.WriteFile(mainPath, []byte(mainContent), 0644)
    
    // Create child workflow
    childPath := filepath.Join(tmpDir, "child.dip")
    childContent := `
workflow child
  start: Start
  exit: End
  
  agent Start
    prompt: "Child task"
  
  agent End
    prompt: "Child done"
  
  edges
    Start -> End
`
    os.WriteFile(childPath, []byte(childContent), 0644)
    
    // Load with subgraphs
    main, subgraphs, err := LoadWorkflowWithSubgraphs(mainPath)
    
    if err != nil {
        t.Fatalf("LoadWorkflowWithSubgraphs failed: %v", err)
    }
    
    if main == nil {
        t.Fatal("main graph is nil")
    }
    
    if len(subgraphs) != 1 {
        t.Errorf("expected 1 subgraph, got %d", len(subgraphs))
    }
    
    if subgraphs["child"] == nil {
        t.Error("expected child subgraph to be loaded")
    }
}

func TestLoadWorkflowWithSubgraphs_CircularRef(t *testing.T) {
    tmpDir := t.TempDir()
    
    // Create A -> B
    aPath := filepath.Join(tmpDir, "a.dip")
    aContent := `
workflow a
  start: S
  exit: E
  agent S { prompt: "a" }
  subgraph CB { ref: b }
  agent E { prompt: "done" }
  edges
    S -> CB -> E
`
    os.WriteFile(aPath, []byte(aContent), 0644)
    
    // Create B -> A (circular)
    bPath := filepath.Join(tmpDir, "b.dip")
    bContent := `
workflow b
  start: S
  exit: E
  agent S { prompt: "b" }
  subgraph CA { ref: a }
  agent E { prompt: "done" }
  edges
    S -> CA -> E
`
    os.WriteFile(bPath, []byte(bContent), 0644)
    
    // Should detect circular dependency
    _, _, err := LoadWorkflowWithSubgraphs(aPath)
    
    if err == nil {
        t.Fatal("expected error for circular dependency, got nil")
    }
    
    if !strings.Contains(err.Error(), "circular") {
        t.Errorf("expected 'circular' in error, got: %v", err)
    }
}
```

#### Step 3: Wire into main.go (30 min)

Modify `cmd/tracker/main.go`:
```go
// Find the section where graph is loaded and registry created

// OLD CODE (find this):
graph, err := loadPipeline(pipelineFile, format)
if err != nil {
    return err
}

registry := handlers.NewDefaultRegistry()

// REPLACE WITH:
var graph *pipeline.Graph
var subgraphs map[string]*pipeline.Graph
var err error

if format == "dip" {
    // Auto-load subgraphs for .dip files
    graph, subgraphs, err = LoadWorkflowWithSubgraphs(pipelineFile)
} else {
    // Legacy .dot format
    graph, err = loadPipeline(pipelineFile, format)
}

if err != nil {
    return err
}

// Create registry with subgraphs if present
registry := handlers.NewDefaultRegistry()
if len(subgraphs) > 0 {
    registry.Register(pipeline.NewSubgraphHandler(subgraphs, registry))
}
```

#### Step 4: Create example (15 min)

Create `examples/subgraph_demo.dip`:
```dippin
workflow SubgraphDemo
  goal: "Demonstrate subgraph composition"
  start: Main
  exit: Done
  
  agent Main
    prompt: |
      We're going to demonstrate subgraphs.
      Next we'll call a child workflow.
  
  subgraph CallChild
    ref: subgraphs/child_task
    params: task=analyze, depth=detailed
  
  agent Report
    reads: child_result
    prompt: |
      The child workflow completed.
      Result: ${ctx.child_result}
  
  agent Done
    prompt: "Demo complete"
  
  edges
    Main -> CallChild
    CallChild -> Report
    Report -> Done
```

Create `examples/subgraphs/child_task.dip`:
```dippin
workflow ChildTask
  goal: "Process a parameterized task"
  start: Process
  exit: Complete
  
  agent Process
    prompt: |
      Processing task: ${params.task}
      Depth: ${params.depth}
      
      Simulating work...
    writes: child_result
  
  agent Complete
    prompt: "Task complete"
  
  edges
    Process -> Complete
```

#### Step 5: Test integration (15 min)
```bash
# Test the example
tracker examples/subgraph_demo.dip --dry-run

# Verify subgraph was loaded
# (should not error with "no handler for subgraph")

# Run tests
go test ./cmd/tracker -v

# Full suite
go test ./...
```

**Acceptance:**
- ✅ Example runs without errors
- ✅ Subgraphs loaded automatically
- ✅ No "no handler for subgraph" errors
- ✅ Tests pass
- ✅ Circular references detected gracefully

---

## Phase 2: Important Enhancements (Day 2 - 3 hours)

### Task 2.1: Full Variable Interpolation (2 hours)

**Objective:** Support ${params.X} and ${graph.X} in addition to ${ctx.X}

**Files to Modify:**
- `pipeline/expand.go`
- `pipeline/context.go`
- `pipeline/expand_test.go`

#### Step 1: Extend PipelineContext (20 min)

Add to `pipeline/context.go`:
```go
type PipelineContext struct {
    store    map[string]string
    params   map[string]string  // NEW
    internal map[string]string
}

func NewPipelineContext() *PipelineContext {
    return &PipelineContext{
        store:    make(map[string]string),
        params:   make(map[string]string),  // NEW
        internal: make(map[string]string),
    }
}

// NEW METHODS:
func (c *PipelineContext) SetParam(key, value string) {
    c.params[key] = value
}

func (c *PipelineContext) GetParam(key string) (string, bool) {
    val, ok := c.params[key]
    return val, ok
}

func (c *PipelineContext) Snapshot() map[string]string {
    snap := make(map[string]string)
    for k, v := range c.store {
        snap[k] = v
    }
    for k, v := range c.params {  // NEW: include params
        snap["params."+k] = v
    }
    return snap
}
```

#### Step 2: Create unified interpolation (40 min)

Rewrite `pipeline/expand.go`:
```go
package pipeline

import (
    "fmt"
    "regexp"
)

// InterpolateVariables replaces ${namespace.key} patterns with values.
// Supports three namespaces: ctx, params, graph
func InterpolateVariables(text string, ctx *PipelineContext, graph *Graph) string {
    result := text
    
    // Interpolate ${ctx.key}
    result = interpolateNamespace(result, "ctx", func(key string) (string, bool) {
        return ctx.Get(key)
    })
    
    // Interpolate ${params.key}
    result = interpolateNamespace(result, "params", func(key string) (string, bool) {
        return ctx.GetParam(key)
    })
    
    // Interpolate ${graph.key}
    result = interpolateNamespace(result, "graph", func(key string) (string, bool) {
        switch key {
        case "goal":
            return graph.Attrs["goal"], graph.Attrs["goal"] != ""
        case "name":
            return graph.Name, graph.Name != ""
        case "start":
            return graph.StartNode, graph.StartNode != ""
        case "exit":
            return graph.ExitNode, graph.ExitNode != ""
        default:
            val, ok := graph.Attrs[key]
            return val, ok
        }
    })
    
    return result
}

// interpolateNamespace handles ${namespace.key} substitution
func interpolateNamespace(text, namespace string, lookup func(string) (string, bool)) string {
    pattern := fmt.Sprintf(`\$\{%s\.([^}]+)\}`, namespace)
    re := regexp.MustCompile(pattern)
    
    return re.ReplaceAllStringFunc(text, func(match string) string {
        // Extract key from ${namespace.key}
        prefix := fmt.Sprintf("${%s.", namespace)
        key := match[len(prefix) : len(match)-1]
        
        if val, ok := lookup(key); ok {
            return val
        }
        
        // Leave unresolved variables as-is (don't error)
        return match
    })
}
```

#### Step 3: Wire params in SubgraphHandler (15 min)

Update `pipeline/subgraph.go`:
```go
func (h *SubgraphHandler) Execute(ctx context.Context, node *Node, pctx *PipelineContext) (Outcome, error) {
    // ... existing code ...
    
    params := ParseSubgraphParams(node.Attrs["subgraph_params"])
    
    // Create child context with params
    childContext := WithInitialContext(pctx.Snapshot())
    
    // NEW: Set params in child context
    for k, v := range params {
        childContext.SetParam(k, v)
    }
    
    childContext.SetInternal(InternalKeySubgraphDepth, strconv.Itoa(depth+1))
    
    // ... rest of execution ...
}
```

#### Step 4: Update interpolation call sites (30 min)

Find all calls to `InterpolateVariables` and add `graph` parameter:

```bash
# Find all call sites
grep -rn "InterpolateVariables" pipeline/

# Update each to pass graph
```

Example:
```go
// OLD:
prompt := InterpolateVariables(node.Attrs["prompt"], ctx)

// NEW:
prompt := InterpolateVariables(node.Attrs["prompt"], ctx, graph)
```

#### Step 5: Add comprehensive tests (15 min)

Add to `pipeline/expand_test.go`:
```go
func TestInterpolateVariables_AllNamespaces(t *testing.T) {
    ctx := NewPipelineContext()
    ctx.Set("outcome", "success")
    ctx.SetParam("task", "analyze")
    ctx.SetParam("depth", "detailed")
    
    graph := NewGraph("TestGraph")
    graph.Attrs["goal"] = "Test interpolation"
    
    template := `
Context: ${ctx.outcome}
Task: ${params.task}
Depth: ${params.depth}
Goal: ${graph.goal}
Name: ${graph.name}
Undefined: ${ctx.missing}
`
    
    result := InterpolateVariables(template, ctx, graph)
    
    if !strings.Contains(result, "Context: success") {
        t.Error("ctx.outcome not interpolated")
    }
    if !strings.Contains(result, "Task: analyze") {
        t.Error("params.task not interpolated")
    }
    if !strings.Contains(result, "Depth: detailed") {
        t.Error("params.depth not interpolated")
    }
    if !strings.Contains(result, "Goal: Test interpolation") {
        t.Error("graph.goal not interpolated")
    }
    if !strings.Contains(result, "Name: TestGraph") {
        t.Error("graph.name not interpolated")
    }
    if !strings.Contains(result, "Undefined: ${ctx.missing}") {
        t.Error("undefined variable should remain unchanged")
    }
}
```

**Acceptance:**
- ✅ All three namespaces work
- ✅ Unresolved variables stay as-is
- ✅ Tests pass
- ✅ Examples work

---

### Task 2.2: Edge Weight Routing (1 hour)

**Objective:** Prioritize edges by weight when multiple match

**Files to Modify:**
- `pipeline/engine.go`
- `pipeline/engine_test.go`

#### Step 1: Modify edge selection (20 min)

Update `pipeline/engine.go`:
```go
import "sort"  // Add to imports

func (e *Engine) selectNextEdge(node *Node, ctx *PipelineContext) *Edge {
    var matches []*Edge
    
    for i := range e.graph.Edges {
        edge := &e.graph.Edges[i]
        if edge.From == node.ID {
            if edge.Condition == "" || e.evaluateCondition(edge.Condition, ctx) {
                matches = append(matches, edge)
            }
        }
    }
    
    if len(matches) == 0 {
        return nil
    }
    
    // Sort by weight (descending), then label (ascending)
    sort.Slice(matches, func(i, j int) bool {
        // Higher weight wins
        if matches[i].Weight != matches[j].Weight {
            return matches[i].Weight > matches[j].Weight
        }
        // Ties broken alphabetically by label
        return matches[i].Label < matches[j].Label
    })
    
    return matches[0]
}
```

#### Step 2: Add tests (40 min)

Add to `pipeline/engine_test.go`:
```go
func TestEngine_EdgeWeightPriority(t *testing.T) {
    g := NewGraph("WeightTest")
    g.StartNode = "Start"
    g.ExitNode = "End"
    
    g.AddNode(&Node{ID: "Start", Handler: "start"})
    g.AddNode(&Node{ID: "A", Handler: "agent"})
    g.AddNode(&Node{ID: "B", Handler: "agent"})
    g.AddNode(&Node{ID: "End", Handler: "exit"})
    
    // Multiple edges from Start with different weights
    g.AddEdge(&Edge{
        From:   "Start",
        To:     "B",
        Weight: 5,
        Label:  "low_priority",
    })
    g.AddEdge(&Edge{
        From:   "Start",
        To:     "A",
        Weight: 10,
        Label:  "high_priority",
    })
    
    // Create engine and check edge selection
    ctx := NewPipelineContext()
    engine := NewEngine(g, NewHandlerRegistry())
    
    selected := engine.selectNextEdge(g.Nodes["Start"], ctx)
    
    if selected == nil {
        t.Fatal("no edge selected")
    }
    
    if selected.To != "A" {
        t.Errorf("expected high weight edge to A, got edge to %s", selected.To)
    }
    
    if selected.Weight != 10 {
        t.Errorf("expected weight 10, got %d", selected.Weight)
    }
}

func TestEngine_EdgeWeightTieBreaker(t *testing.T) {
    g := NewGraph("TieBreaker")
    g.StartNode = "Start"
    g.ExitNode = "End"
    
    g.AddNode(&Node{ID: "Start", Handler: "start"})
    g.AddNode(&Node{ID: "Z", Handler: "agent"})
    g.AddNode(&Node{ID: "A", Handler: "agent"})
    
    // Same weight, different labels - should pick alphabetically
    g.AddEdge(&Edge{From: "Start", To: "Z", Weight: 5, Label: "zebra"})
    g.AddEdge(&Edge{From: "Start", To: "A", Weight: 5, Label: "apple"})
    
    ctx := NewPipelineContext()
    engine := NewEngine(g, NewHandlerRegistry())
    
    selected := engine.selectNextEdge(g.Nodes["Start"], ctx)
    
    if selected.To != "A" {
        t.Errorf("expected alphabetically first label, got edge to %s", selected.To)
    }
}
```

**Acceptance:**
- ✅ Higher weights win
- ✅ Ties broken alphabetically
- ✅ Tests pass
- ✅ No regressions

---

## Phase 3: Verification & Documentation (30 min)

### Task 3.1: Run Full Test Suite

```bash
# Unit tests
go test ./... -v

# Coverage
go test ./... -cover

# Race detection
go test ./... -race

# All examples
for f in examples/*.dip; do
    echo "Testing $f"
    tracker "$f" --dry-run || echo "FAILED: $f"
done
```

### Task 3.2: Update Documentation

Update `README.md`:
```markdown
## Subgraph Composition

Tracker supports composing workflows from smaller reusable subgraphs:

```dippin
workflow Main
  start: Begin
  exit: Done
  
  agent Begin
    prompt: "Starting main workflow"
  
  subgraph CallChild
    ref: subgraphs/process_task
    params: task=analyze, depth=detailed
  
  agent Done
    reads: result
    prompt: |
      Child completed with: ${ctx.result}
```

Subgraphs are automatically discovered and loaded from:
1. Relative paths (e.g., `child.dip`)
2. Subgraphs directory (e.g., `subgraphs/child.dip`)

### Variable Interpolation

Three namespaces are supported:

- `${ctx.key}` - Runtime context values
- `${params.key}` - Subgraph parameters
- `${graph.goal}` - Workflow attributes

Example:
```dippin
agent Task
  prompt: |
    Goal: ${graph.goal}
    Task: ${params.task_name}
    Prior result: ${ctx.last_outcome}
```
```

---

## Success Checklist

### Phase 1 Complete When:
- [ ] Circular subgraph protection implemented
- [ ] Test for circular refs passes
- [ ] SubgraphHandler auto-registered
- [ ] Example subgraph workflows work
- [ ] No "no handler for subgraph" errors
- [ ] All existing tests pass

### Phase 2 Complete When:
- [ ] ${ctx.X}, ${params.X}, ${graph.X} all work
- [ ] Edge weights prioritize correctly
- [ ] Comprehensive test coverage
- [ ] Examples demonstrate features
- [ ] All tests pass

### Final Completion When:
- [ ] Full test suite passes (go test ./...)
- [ ] All examples run successfully
- [ ] Documentation updated
- [ ] No regressions
- [ ] Coverage maintained >85%
- [ ] Code reviewed

---

## Rollback Plan

If issues arise:

1. **Phase 2 issues:** Revert commits, ship Phase 1 only
2. **Phase 1 issues:** Revert to pre-implementation state
3. **Test failures:** Fix tests before merging
4. **Performance issues:** Add benchmarks, optimize

---

## Timeline Summary

| Phase | Tasks | Time | Cumulative |
|-------|-------|------|------------|
| **1.1** | Circular protection | 1.5h | 1.5h |
| **1.2** | Subgraph wiring | 4h | 5.5h |
| **2.1** | Variable interpolation | 2h | 7.5h |
| **2.2** | Edge weights | 1h | 8.5h |
| **3** | Verification | 0.5h | 9h |

**Total:** 9 hours (with buffer) ≈ 1-2 days

---

## Post-Implementation

### Metrics to Track
- Subgraph usage in production
- Circular reference errors (should be caught)
- Variable interpolation coverage
- Edge weight routing behavior

### Future Enhancements (Backlog)
- Enhanced spawn_agent config (2h)
- Batch processing (4-6h)
- Content type testing (2h)

---

**Status:** ✅ Ready to Execute  
**Owner:** Development Team  
**Start Date:** TBD  
**Target Completion:** 1-2 days from start
