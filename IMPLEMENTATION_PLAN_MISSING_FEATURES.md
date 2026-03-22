# Implementation Plan: Missing Dippin Features
**Date:** 2026-03-21  
**Total Effort:** 4 hours  
**Features:** 2 (Subgraph recursion limiting, Spawn agent model override)

---

## Overview

This plan implements the final 2 missing features to achieve 100% dippin-lang v0.1.0 parity:

1. **Subgraph Recursion Depth Limiting** (2 hours) — Prevent stack overflow from circular references
2. **Spawn Agent Model/Provider Override** (2 hours) — Enable child agents to use different LLM configs

Both features are safety/flexibility enhancements that align with user expectations, even though not strictly mandated by the dippin spec.

---

## Feature 1: Subgraph Recursion Depth Limiting

### Problem

Currently, circular subgraph references cause stack overflow:

```dippin
# A.dip references B.dip
# B.dip references A.dip
# Result: infinite recursion → crash
```

### Solution

Add depth tracking to PipelineContext and enforce a configurable limit (default: 10).

---

### Task 1.1: Add Depth Tracking to PipelineContext (30 min)

**File:** `pipeline/types.go`

**Changes:**

```go
// In PipelineContext struct, add new fields:
type PipelineContext struct {
    data map[string]string
    mu   sync.RWMutex
    
    // Subgraph depth tracking
    subgraphDepth int
    maxDepth      int // Default 10
}

// Add methods for depth management:

// IncrementDepth increments the subgraph nesting depth and returns an error if the limit is exceeded.
func (ctx *PipelineContext) IncrementDepth() error {
    ctx.mu.Lock()
    defer ctx.mu.Unlock()
    
    ctx.subgraphDepth++
    if ctx.subgraphDepth > ctx.maxDepth {
        return fmt.Errorf("subgraph recursion depth exceeded: current=%d, max=%d", ctx.subgraphDepth, ctx.maxDepth)
    }
    return nil
}

// DecrementDepth decrements the subgraph nesting depth.
func (ctx *PipelineContext) DecrementDepth() {
    ctx.mu.Lock()
    defer ctx.mu.Unlock()
    if ctx.subgraphDepth > 0 {
        ctx.subgraphDepth--
    }
}

// CurrentDepth returns the current subgraph nesting level.
func (ctx *PipelineContext) CurrentDepth() int {
    ctx.mu.RLock()
    defer ctx.mu.RUnlock()
    return ctx.subgraphDepth
}

// SetMaxDepth sets the maximum allowed subgraph nesting depth.
func (ctx *PipelineContext) SetMaxDepth(max int) {
    ctx.mu.Lock()
    defer ctx.mu.Unlock()
    ctx.maxDepth = max
}
```

**Also update NewPipelineContext:**

```go
func NewPipelineContext() *PipelineContext {
    return &PipelineContext{
        data:     make(map[string]string),
        maxDepth: 10, // Default limit
    }
}
```

---

### Task 1.2: Enforce Depth Limit in SubgraphHandler (15 min)

**File:** `pipeline/subgraph.go`

**Changes:**

```go
// In SubgraphHandler.Execute(), add depth checking:

func (h *SubgraphHandler) Execute(ctx context.Context, node *Node, pctx *PipelineContext) (Outcome, error) {
    // Check and increment depth BEFORE executing subgraph
    if err := pctx.IncrementDepth(); err != nil {
        return Outcome{Status: OutcomeFail}, fmt.Errorf("subgraph %q: %w", node.ID, err)
    }
    // Ensure depth is decremented on return (even if error occurs)
    defer pctx.DecrementDepth()
    
    ref, ok := node.Attrs["subgraph_ref"]
    if !ok || ref == "" {
        return Outcome{Status: OutcomeFail}, fmt.Errorf("node %q missing subgraph_ref attribute", node.ID)
    }

    // ... rest of existing code unchanged
}
```

---

### Task 1.3: Add Tests for Recursion Limiting (45 min)

**File:** `pipeline/subgraph_test.go`

**Add these test cases:**

```go
func TestPipelineContext_DepthTracking(t *testing.T) {
    ctx := NewPipelineContext()
    
    // Test depth increment
    if err := ctx.IncrementDepth(); err != nil {
        t.Fatalf("unexpected error on first increment: %v", err)
    }
    if depth := ctx.CurrentDepth(); depth != 1 {
        t.Errorf("expected depth=1, got %d", depth)
    }
    
    // Test depth decrement
    ctx.DecrementDepth()
    if depth := ctx.CurrentDepth(); depth != 0 {
        t.Errorf("expected depth=0 after decrement, got %d", depth)
    }
    
    // Test max depth enforcement
    ctx.SetMaxDepth(3)
    ctx.IncrementDepth() // 1
    ctx.IncrementDepth() // 2
    ctx.IncrementDepth() // 3
    
    err := ctx.IncrementDepth() // Should fail at 4
    if err == nil {
        t.Fatal("expected error when exceeding max depth, got nil")
    }
    if !strings.Contains(err.Error(), "recursion depth exceeded") {
        t.Errorf("unexpected error message: %v", err)
    }
}

func TestSubgraphHandler_CircularReference(t *testing.T) {
    // Create two graphs that reference each other
    graphA := NewGraph("A")
    graphA.AddNode(&Node{ID: "Start", Shape: "Mdiamond"})
    graphA.AddNode(&Node{ID: "CallB", Shape: "tab", Attrs: map[string]string{"subgraph_ref": "B"}})
    graphA.AddNode(&Node{ID: "Exit", Shape: "Msquare"})
    graphA.AddEdge(&Edge{From: "Start", To: "CallB"})
    graphA.AddEdge(&Edge{From: "CallB", To: "Exit"})
    
    graphB := NewGraph("B")
    graphB.AddNode(&Node{ID: "Start", Shape: "Mdiamond"})
    graphB.AddNode(&Node{ID: "CallA", Shape: "tab", Attrs: map[string]string{"subgraph_ref": "A"}})
    graphB.AddNode(&Node{ID: "Exit", Shape: "Msquare"})
    graphB.AddEdge(&Edge{From: "Start", To: "CallA"})
    graphB.AddEdge(&Edge{From: "CallA", To: "Exit"})
    
    // Register both graphs
    graphs := map[string]*Graph{"A": graphA, "B": graphB}
    registry := NewHandlerRegistry()
    subgraphHandler := NewSubgraphHandler(graphs, registry)
    registry.Register(subgraphHandler)
    
    // Set low max depth for faster test
    ctx := NewPipelineContext()
    ctx.SetMaxDepth(5)
    
    // Execute graph A, which should hit recursion limit
    engine := NewEngine(graphA, registry, WithInitialContext(ctx.Snapshot()))
    result, err := engine.Run(context.Background())
    
    if err == nil {
        t.Fatal("expected error from circular subgraph reference, got nil")
    }
    
    if !strings.Contains(err.Error(), "recursion depth exceeded") {
        t.Errorf("expected 'recursion depth exceeded' error, got: %v", err)
    }
    
    if result.Status == OutcomeSuccess {
        t.Error("expected failure status for circular reference")
    }
}

func TestSubgraphHandler_DeepNestingValid(t *testing.T) {
    // Create a chain of subgraphs: A -> B -> C -> D -> E
    // Depth 5 is valid (under default limit of 10)
    
    graphE := NewGraph("E")
    graphE.AddNode(&Node{ID: "Start", Shape: "Mdiamond"})
    graphE.AddNode(&Node{ID: "Work", Shape: "box", Attrs: map[string]string{"prompt": "Final level"}})
    graphE.AddNode(&Node{ID: "Exit", Shape: "Msquare"})
    graphE.AddEdge(&Edge{From: "Start", To: "Work"})
    graphE.AddEdge(&Edge{From: "Work", To: "Exit"})
    
    // Build chain D -> E, C -> D, B -> C, A -> B
    graphs := map[string]*Graph{"E": graphE}
    
    for i := 4; i >= 1; i-- {
        name := string(rune('A' + i - 1))
        next := string(rune('A' + i))
        
        g := NewGraph(name)
        g.AddNode(&Node{ID: "Start", Shape: "Mdiamond"})
        g.AddNode(&Node{ID: "CallNext", Shape: "tab", Attrs: map[string]string{"subgraph_ref": next}})
        g.AddNode(&Node{ID: "Exit", Shape: "Msquare"})
        g.AddEdge(&Edge{From: "Start", To: "CallNext"})
        g.AddEdge(&Edge{From: "CallNext", To: "Exit"})
        
        graphs[name] = g
    }
    
    // Execute graph A (depth 5)
    registry := NewHandlerRegistry()
    // Register mock codergen handler for the "Work" node in E
    registry.Register(&mockCodergenHandler{})
    subgraphHandler := NewSubgraphHandler(graphs, registry)
    registry.Register(subgraphHandler)
    
    ctx := NewPipelineContext()
    ctx.SetMaxDepth(10) // Default limit
    
    engine := NewEngine(graphs["A"], registry, WithInitialContext(ctx.Snapshot()))
    result, err := engine.Run(context.Background())
    
    if err != nil {
        t.Fatalf("valid deep nesting (depth=5) should succeed, got error: %v", err)
    }
    
    if result.Status != OutcomeSuccess {
        t.Errorf("expected success for valid deep nesting, got: %s", result.Status)
    }
}

// Mock handler for testing
type mockCodergenHandler struct{}

func (h *mockCodergenHandler) Name() string { return "codergen" }

func (h *mockCodergenHandler) Execute(ctx context.Context, node *Node, pctx *PipelineContext) (Outcome, error) {
    return Outcome{Status: OutcomeSuccess}, nil
}
```

---

## Feature 2: Spawn Agent Model/Provider Override

### Problem

Currently, `spawn_agent` tool can only set `task`, `system_prompt`, and `max_turns`. Child agents always inherit parent's model and provider.

Desired: Allow LLM to override model/provider for specialized child tasks.

---

### Task 2.1: Extend spawn_agent Tool Parameters (30 min)

**File:** `agent/tools/spawn.go`

**Changes:**

```go
// Update Parameters() to include model and provider:

func (t *SpawnAgentTool) Parameters() json.RawMessage {
    return json.RawMessage(`{
        "type": "object",
        "properties": {
            "task": {
                "type": "string",
                "description": "The task description for the child agent to perform."
            },
            "system_prompt": {
                "type": "string",
                "description": "Optional system prompt for the child agent session."
            },
            "max_turns": {
                "type": "integer",
                "description": "Maximum number of turns for the child session (default 10)."
            },
            "model": {
                "type": "string",
                "description": "Optional LLM model override for the child agent (e.g., 'claude-opus-4', 'gpt-4o')."
            },
            "provider": {
                "type": "string",
                "description": "Optional LLM provider override for the child agent (e.g., 'anthropic', 'openai', 'gemini')."
            }
        },
        "required": ["task"]
    }`)
}

// Update Execute() to parse and pass new parameters:

func (t *SpawnAgentTool) Execute(ctx context.Context, input json.RawMessage) (string, error) {
    var params struct {
        Task         string `json:"task"`
        SystemPrompt string `json:"system_prompt"`
        MaxTurns     int    `json:"max_turns"`
        Model        string `json:"model"`
        Provider     string `json:"provider"`
    }
    if err := json.Unmarshal(input, &params); err != nil {
        return "", fmt.Errorf("invalid input: %w", err)
    }
    if params.Task == "" {
        return "", fmt.Errorf("task is required")
    }
    if params.MaxTurns <= 0 {
        params.MaxTurns = 10
    }

    return t.runner.RunChild(ctx, params.Task, params.SystemPrompt, params.MaxTurns, params.Model, params.Provider)
}
```

---

### Task 2.2: Extend SessionRunner Interface (30 min)

**File:** `agent/tools/spawn.go` and `agent/session.go`

**In `agent/tools/spawn.go`:**

```go
// Update SessionRunner interface:

type SessionRunner interface {
    RunChild(ctx context.Context, task string, systemPrompt string, maxTurns int, model string, provider string) (string, error)
}
```

**In `agent/session.go`:**

```go
// Update RunChild implementation:

func (s *Session) RunChild(ctx context.Context, task string, systemPrompt string, maxTurns int, model string, provider string) (string, error) {
    // Clone parent config
    childConfig := s.config
    
    // Apply overrides if specified
    if model != "" {
        childConfig.Model = model
    }
    if provider != "" {
        childConfig.Provider = provider
    }
    if systemPrompt != "" {
        childConfig.SystemPrompt = systemPrompt
    }
    if maxTurns > 0 {
        childConfig.MaxTurns = maxTurns
    }
    
    // Create child session with overridden config
    child := NewSession(childConfig, s.llmProvider, s.toolRegistry)
    
    // Run child to completion
    result, err := child.Run(ctx, task)
    if err != nil {
        return "", fmt.Errorf("child agent failed: %w", err)
    }
    
    return result.Text, nil
}
```

---

### Task 2.3: Add Tests for Model/Provider Override (60 min)

**File:** `agent/tools/spawn_test.go`

**Add these test cases:**

```go
func TestSpawnAgentTool_ModelOverride(t *testing.T) {
    var capturedModel string
    
    // Mock SessionRunner that captures the model parameter
    mockRunner := &mockSessionRunner{
        runChildFunc: func(ctx context.Context, task, systemPrompt string, maxTurns int, model, provider string) (string, error) {
            capturedModel = model
            return "child output", nil
        },
    }
    
    tool := NewSpawnAgentTool(mockRunner)
    
    input := `{
        "task": "Analyze code",
        "model": "claude-opus-4"
    }`
    
    result, err := tool.Execute(context.Background(), json.RawMessage(input))
    if err != nil {
        t.Fatalf("unexpected error: %v", err)
    }
    
    if capturedModel != "claude-opus-4" {
        t.Errorf("expected model='claude-opus-4', got '%s'", capturedModel)
    }
    
    if result != "child output" {
        t.Errorf("expected result='child output', got '%s'", result)
    }
}

func TestSpawnAgentTool_ProviderOverride(t *testing.T) {
    var capturedProvider string
    
    mockRunner := &mockSessionRunner{
        runChildFunc: func(ctx context.Context, task, systemPrompt string, maxTurns int, model, provider string) (string, error) {
            capturedProvider = provider
            return "child output", nil
        },
    }
    
    tool := NewSpawnAgentTool(mockRunner)
    
    input := `{
        "task": "Review code",
        "provider": "anthropic"
    }`
    
    result, err := tool.Execute(context.Background(), json.RawMessage(input))
    if err != nil {
        t.Fatalf("unexpected error: %v", err)
    }
    
    if capturedProvider != "anthropic" {
        t.Errorf("expected provider='anthropic', got '%s'", capturedProvider)
    }
}

func TestSpawnAgentTool_BothOverrides(t *testing.T) {
    var capturedModel, capturedProvider string
    
    mockRunner := &mockSessionRunner{
        runChildFunc: func(ctx context.Context, task, systemPrompt string, maxTurns int, model, provider string) (string, error) {
            capturedModel = model
            capturedProvider = provider
            return "child output", nil
        },
    }
    
    tool := NewSpawnAgentTool(mockRunner)
    
    input := `{
        "task": "Security audit",
        "model": "gpt-4o",
        "provider": "openai"
    }`
    
    _, err := tool.Execute(context.Background(), json.RawMessage(input))
    if err != nil {
        t.Fatalf("unexpected error: %v", err)
    }
    
    if capturedModel != "gpt-4o" {
        t.Errorf("expected model='gpt-4o', got '%s'", capturedModel)
    }
    if capturedProvider != "openai" {
        t.Errorf("expected provider='openai', got '%s'", capturedProvider)
    }
}

func TestSpawnAgentTool_InheritParentConfig(t *testing.T) {
    // When no override specified, should pass empty strings (parent inherits)
    var capturedModel, capturedProvider string
    
    mockRunner := &mockSessionRunner{
        runChildFunc: func(ctx context.Context, task, systemPrompt string, maxTurns int, model, provider string) (string, error) {
            capturedModel = model
            capturedProvider = provider
            return "child output", nil
        },
    }
    
    tool := NewSpawnAgentTool(mockRunner)
    
    input := `{
        "task": "Simple task"
    }`
    
    _, err := tool.Execute(context.Background(), json.RawMessage(input))
    if err != nil {
        t.Fatalf("unexpected error: %v", err)
    }
    
    // Empty strings mean inherit parent config
    if capturedModel != "" {
        t.Errorf("expected empty model (inherit), got '%s'", capturedModel)
    }
    if capturedProvider != "" {
        t.Errorf("expected empty provider (inherit), got '%s'", capturedProvider)
    }
}

func TestSpawnAgentTool_BackwardCompatibility(t *testing.T) {
    // Existing calls without model/provider should still work
    mockRunner := &mockSessionRunner{
        runChildFunc: func(ctx context.Context, task, systemPrompt string, maxTurns int, model, provider string) (string, error) {
            return "success", nil
        },
    }
    
    tool := NewSpawnAgentTool(mockRunner)
    
    // Old format (no model/provider fields)
    input := `{
        "task": "Legacy task",
        "system_prompt": "You are helpful",
        "max_turns": 5
    }`
    
    result, err := tool.Execute(context.Background(), json.RawMessage(input))
    if err != nil {
        t.Fatalf("backward compatibility broken: %v", err)
    }
    
    if result != "success" {
        t.Errorf("expected success, got %s", result)
    }
}

// Mock SessionRunner for testing
type mockSessionRunner struct {
    runChildFunc func(ctx context.Context, task, systemPrompt string, maxTurns int, model, provider string) (string, error)
}

func (m *mockSessionRunner) RunChild(ctx context.Context, task, systemPrompt string, maxTurns int, model, provider string) (string, error) {
    if m.runChildFunc != nil {
        return m.runChildFunc(ctx, task, systemPrompt, maxTurns, model, provider)
    }
    return "", nil
}
```

---

## Execution Checklist

### Phase 1: Subgraph Recursion Limiting (2 hours)

- [ ] **Task 1.1:** Add depth tracking to PipelineContext (30 min)
  - [ ] Add `subgraphDepth` and `maxDepth` fields
  - [ ] Implement `IncrementDepth()`, `DecrementDepth()`, `CurrentDepth()`, `SetMaxDepth()`
  - [ ] Set default `maxDepth = 10` in `NewPipelineContext()`
  
- [ ] **Task 1.2:** Enforce depth limit in SubgraphHandler (15 min)
  - [ ] Add `pctx.IncrementDepth()` check at start of `Execute()`
  - [ ] Add `defer pctx.DecrementDepth()`
  - [ ] Return clear error message on limit exceeded
  
- [ ] **Task 1.3:** Add tests (45 min)
  - [ ] `TestPipelineContext_DepthTracking` — Basic depth increment/decrement
  - [ ] `TestSubgraphHandler_CircularReference` — Detect circular refs
  - [ ] `TestSubgraphHandler_DeepNestingValid` — Allow valid deep nesting
  
- [ ] **Task 1.4:** Validation (30 min)
  - [ ] Run all existing tests: `go test ./pipeline/...`
  - [ ] Create circular subgraph example and verify error message
  - [ ] Verify depth counter resets correctly

---

### Phase 2: Spawn Agent Model/Provider Override (2 hours)

- [ ] **Task 2.1:** Extend spawn_agent tool parameters (30 min)
  - [ ] Add `model` and `provider` to `Parameters()` JSON schema
  - [ ] Update `Execute()` to parse new fields
  - [ ] Pass to `RunChild()` with updated signature
  
- [ ] **Task 2.2:** Extend SessionRunner interface (30 min)
  - [ ] Update interface in `agent/tools/spawn.go`
  - [ ] Update implementation in `agent/session.go`
  - [ ] Apply overrides to child config before session creation
  
- [ ] **Task 2.3:** Add tests (60 min)
  - [ ] `TestSpawnAgentTool_ModelOverride`
  - [ ] `TestSpawnAgentTool_ProviderOverride`
  - [ ] `TestSpawnAgentTool_BothOverrides`
  - [ ] `TestSpawnAgentTool_InheritParentConfig`
  - [ ] `TestSpawnAgentTool_BackwardCompatibility`
  
- [ ] **Task 2.4:** Validation (30 min, overlaps with 2.3)
  - [ ] Run all existing tests: `go test ./agent/...`
  - [ ] Manual test: Parent with GPT-4, spawn with Claude
  - [ ] Verify backward compatibility with existing workflows

---

## Validation Criteria

### Feature 1: Subgraph Recursion Limiting

✅ **Pass Criteria:**
- Circular subgraph references fail with clear error message
- Error message includes current depth and max limit
- Valid deep nesting (< max depth) succeeds
- Depth counter decrements on subgraph return
- All existing subgraph tests pass

❌ **Fail Criteria:**
- Circular references still cause stack overflow
- Valid deep nesting fails incorrectly
- Depth counter leaks (doesn't decrement)

---

### Feature 2: Spawn Agent Model/Provider Override

✅ **Pass Criteria:**
- `spawn_agent` accepts `model` and `provider` parameters
- Child sessions use overridden model/provider
- Child sessions inherit parent config when no override
- All existing `spawn_agent` calls work (backward compat)
- Unit tests for all override combinations pass

❌ **Fail Criteria:**
- Overrides don't apply to child session
- Backward compatibility breaks existing workflows
- Invalid provider causes cryptic error

---

## Post-Implementation

### Documentation Updates

1. **README.md:**
   - Document subgraph recursion limit (default 10, configurable)
   - Add `spawn_agent` model/provider parameters to tool reference
   
2. **Examples:**
   - Add example showing `spawn_agent` with model override
   - Add comment in subgraph examples about recursion limit

3. **Changelog:**
   ```markdown
   ## v0.x.x (2026-03-21)
   
   ### Added
   - Subgraph recursion depth limiting (default max: 10 levels)
   - `spawn_agent` tool now supports `model` and `provider` parameters
   
   ### Fixed
   - Circular subgraph references now fail gracefully instead of stack overflow
   ```

---

## Risk Mitigation

| Risk | Mitigation |
|------|------------|
| Recursion limit too low | Default of 10 is conservative; easy to increase if needed |
| Recursion limit breaks valid workflows | Highly unlikely (10 levels is very deep) |
| Model override breaks provider switching | Validate provider in `RunChild` before creating session |
| Backward compatibility break | Extensive tests for old-format `spawn_agent` calls |

---

## Timeline

**Day 1 (2 hours):**
- Morning: Implement Feature 1 (recursion limiting)
- Test and validate

**Day 1 (2 hours):**
- Afternoon: Implement Feature 2 (spawn override)
- Test and validate

**Day 1 (30 min):**
- Update documentation
- Create examples
- Final validation

**Total:** 4.5 hours (with buffer)

---

## Success Metrics

After completion:

✅ **100% dippin-lang v0.1.0 feature parity**  
✅ **All tests passing**  
✅ **Zero breaking changes**  
✅ **Documentation updated**  
✅ **Ready for release**

---

**Plan Status:** READY TO EXECUTE  
**Next Step:** Begin Task 1.1 (Add depth tracking to PipelineContext)
