# Tracker Dippin Language Support: Missing Features & Implementation Plan

**Date:** 2026-03-22  
**Status:** ⚠️ **NOT PRODUCTION READY** — 2 blocking bugs, ~85% spec compliant  
**Est. Fix Time:** 7 hours

---

## 🎯 Executive Summary

### Human's Question:
> "There are a number of features of the dippin lang that tracker doesn't support as of yet, like subgraphs. Determine the missing subset and make a plan to effectuate it."

### Answer:

**Subgraphs DO exist** but are **partially broken**. The handler implementation is present, but:
- ❌ **Parameters don't work** — Extracted but never injected into child context
- ❌ **Variable interpolation incomplete** — Only `${ctx.X}` works, not `${params.X}` or `${graph.X}`

**Missing Features (4 total):**

| # | Feature | Priority | Time | Status |
|---|---------|----------|------|--------|
| 1 | Subgraph parameter injection | 🔴 HIGH | 3h | Broken — blocks examples/parallel-ralph-dev.dip |
| 2 | Full variable interpolation | 🔴 HIGH | 2h | Partial — only ${ctx.X} works |
| 3 | Edge weight prioritization | 🟡 MED | 1h | Missing — non-deterministic routing |
| 4 | Spawn agent config | 🟢 LOW | 2h | Limited — no model/provider control |

**Total Fix Time:** 7 hours

---

## 🔍 Detailed Gap Analysis

### 1. Subgraph Parameter Injection ⛔ BLOCKING

**What's Broken:**

The `.dip` parser extracts `params:` from subgraph nodes, but the `SubgraphHandler` never injects them into the child pipeline context.

**Evidence:**

```go
// pipeline/dippin_adapter.go:248 — Extraction WORKS
attrs["subgraph_params"] = strings.Join(pairs, ",")

// pipeline/subgraph.go:39-41 — Injection MISSING
engine := NewEngine(subGraph, h.registry, WithInitialContext(pctx.Snapshot()))
// ❌ Params from node.Attrs["subgraph_params"] are NEVER parsed or injected
```

**Real Example That Breaks:**

```dippin
# examples/parallel-ralph-dev.dip:46-52
subgraph StreamA
  label: "Adaptive Stream A"
  ref: subgraphs/adaptive-ralph-stream
  params:
    stream_id: stream-a       # ❌ Never reaches child pipeline
    max_iterations: 8         # ❌ Never reaches child pipeline
```

```bash
# examples/subgraphs/adaptive-ralph-stream.dip:22
stream_dir=".ai/streams/${params.stream_id}"
# ❌ This expands to literal "${params.stream_id}" instead of "stream-a"
```

**Impact:** Any workflow using subgraph params (like `parallel-ralph-dev.dip`) will fail silently or produce incorrect results.

**Fix Required:**

```go
// pipeline/subgraph.go:Execute() needs:
func (h *SubgraphHandler) Execute(ctx context.Context, node *Node, pctx *PipelineContext) (Outcome, error) {
    ref := node.Attrs["subgraph_ref"]
    subGraph := h.graphs[ref]
    
    // Parse subgraph_params attribute
    params := make(map[string]string)
    if paramsStr, ok := node.Attrs["subgraph_params"]; ok && paramsStr != "" {
        for _, pair := range strings.Split(paramsStr, ",") {
            parts := strings.SplitN(pair, "=", 2)
            if len(parts) == 2 {
                params[parts[0]] = parts[1]
            }
        }
    }
    
    // Inject params into initial context
    initialCtx := pctx.Snapshot()
    for k, v := range params {
        initialCtx["params."+k] = v  // Namespace under "params."
    }
    
    engine := NewEngine(subGraph, h.registry, WithInitialContext(initialCtx))
    // ...
}
```

**Test Required:**

```go
func TestSubgraphHandler_ParameterPassing(t *testing.T) {
    subGraph := buildSubgraph()  // Uses ${params.model} in a prompt
    
    reg := newTestRegistry()
    handler := NewSubgraphHandler(map[string]*Graph{"child": subGraph}, reg)
    
    node := &Node{
        ID:    "sg",
        Shape: "tab",
        Attrs: map[string]string{
            "subgraph_ref":    "child",
            "subgraph_params": "model=gpt-4,task=coding",
        },
    }
    
    outcome, err := handler.Execute(context.Background(), node, NewPipelineContext())
    // Verify ${params.model} expanded to "gpt-4" in child execution
}
```

**Estimated Time:** 3 hours (code + test + verification)

---

### 2. Full Variable Interpolation ⛔ BLOCKING

**What's Missing:**

Dippin spec requires **3 variable namespaces** in prompts:
1. ✅ `${ctx.X}` — Pipeline context (WORKS)
2. ❌ `${params.X}` — Subgraph/node parameters (BROKEN)
3. ❌ `${graph.X}` — Graph-level attributes (BROKEN)

**Current Implementation:**

```go
// pipeline/context.go:ExpandPromptVariables() — Only handles ${ctx.X}
func ExpandPromptVariables(prompt string, pctx *PipelineContext) string {
    for k, v := range pctx.Values {
        prompt = strings.ReplaceAll(prompt, "${ctx."+k+"}", v)
    }
    return prompt
    // ❌ No code for ${params.X} or ${graph.X}
}
```

**Example That Fails:**

```dippin
workflow Example
  goal: "Build a feature"
  
agent Reviewer
  prompt: |
    Workflow goal: ${graph.goal}           # ❌ Not interpolated
    Using model: ${params.model}           # ❌ Not interpolated
    Last outcome: ${ctx.last_outcome}      # ✅ Works
```

**Impact:** Users can't reference graph config or subgraph params in prompts, limiting composability.

**Fix Required:**

```go
// pipeline/context.go — New function
func InterpolateAllVariables(text string, pctx *PipelineContext, 
                             params, graphAttrs map[string]string) string {
    // ${ctx.X} — Pipeline context
    for k, v := range pctx.Values {
        text = strings.ReplaceAll(text, "${ctx."+k+"}", v)
    }
    
    // ${params.X} — Node/subgraph parameters
    for k, v := range params {
        text = strings.ReplaceAll(text, "${params."+k+"}", v)
    }
    
    // ${graph.X} — Graph-level attributes
    for k, v := range graphAttrs {
        text = strings.ReplaceAll(text, "${graph."+k+"}", v)
    }
    
    return text
}
```

```go
// pipeline/handlers/codergen.go:Execute() — Use new function
func (h *CodergenHandler) Execute(...) (Outcome, error) {
    prompt := node.Attrs["prompt"]
    
    // Extract params from node (for subgraph children)
    params := extractNodeParams(node)
    
    // Use new interpolation function
    prompt = InterpolateAllVariables(prompt, pctx, params, h.graphAttrs)
    
    // ... rest of execution
}
```

**Test Required:**

```go
func TestInterpolateAllVariables(t *testing.T) {
    pctx := NewPipelineContext()
    pctx.Set("outcome", "success")
    
    params := map[string]string{"model": "gpt-4", "task": "coding"}
    graphAttrs := map[string]string{"goal": "Build feature", "version": "1.0"}
    
    input := "Outcome: ${ctx.outcome}, Model: ${params.model}, Goal: ${graph.goal}"
    result := InterpolateAllVariables(input, pctx, params, graphAttrs)
    
    expected := "Outcome: success, Model: gpt-4, Goal: Build feature"
    if result != expected {
        t.Errorf("got %q, want %q", result, expected)
    }
}
```

**Estimated Time:** 2 hours (function + integration + tests)

---

### 3. Edge Weight Prioritization 🟡 NON-BLOCKING

**What's Missing:**

Edge weights are extracted from `.dip` files but **ignored during routing**.

**Current Behavior:**

```go
// pipeline/engine.go:selectNextEdge()
candidates := matchingEdges(from, pctx)
// ❌ No sorting by weight — first match wins (non-deterministic)
return candidates[0]
```

**Example Problem:**

```dippin
edges
  A -> B  weight: 10  # Should be preferred
  A -> C  weight: 1   # Fallback
```

If both conditions match, selection is **undefined** (depends on slice iteration order).

**Fix Required:**

```go
// pipeline/engine.go:selectNextEdge()
func (e *Engine) selectNextEdge(from string, pctx *PipelineContext) *Edge {
    candidates := matchingEdges(from, pctx)
    
    // Sort by weight descending (higher weight = higher priority)
    sort.Slice(candidates, func(i, j int) bool {
        wi := getEdgeWeight(candidates[i])
        wj := getEdgeWeight(candidates[j])
        return wi > wj
    })
    
    return candidates[0]
}

func getEdgeWeight(e *Edge) int {
    if w, ok := e.Attrs["weight"]; ok {
        if i, err := strconv.Atoi(w); err == nil {
            return i
        }
    }
    return 0  // Default weight
}
```

**Test Required:**

```go
func TestEdgeWeightPriority(t *testing.T) {
    g := NewGraph("test")
    g.AddEdge(&Edge{From: "A", To: "B", Attrs: map[string]string{"weight": "10"}})
    g.AddEdge(&Edge{From: "A", To: "C", Attrs: map[string]string{"weight": "1"}})
    
    engine := NewEngine(g, registry)
    selected := engine.selectNextEdge("A", NewPipelineContext())
    
    if selected.To != "B" {
        t.Errorf("expected edge A->B (weight 10), got A->%s", selected.To)
    }
}
```

**Estimated Time:** 1 hour

---

### 4. Spawn Agent Configuration 🟢 LOW PRIORITY

**What's Missing:**

`spawn_agent` tool only accepts `task` parameter. Can't configure child agent's model, provider, max_turns, or system_prompt.

**Current Limitation:**

```json
// agent/tools/spawn_agent.go — Only accepts task
{
  "task": "Write unit tests"
  // ❌ Can't specify: model, provider, max_turns, system_prompt
}
```

**Impact:** All spawned agents use hardcoded defaults. No fine-grained delegation control.

**Workaround:** Use nested subgraphs instead of spawn_agent.

**Fix Required:**

```go
// agent/tools/spawn_agent.go
type SpawnAgentArgs struct {
    Task         string `json:"task"`
    Model        string `json:"model,omitempty"`
    Provider     string `json:"provider,omitempty"`
    MaxTurns     int    `json:"max_turns,omitempty"`
    SystemPrompt string `json:"system_prompt,omitempty"`
}

func (t *SpawnAgentTool) Execute(ctx context.Context, input string) (string, error) {
    var args SpawnAgentArgs
    json.Unmarshal([]byte(input), &args)
    
    config := agent.DefaultConfig()
    if args.Model != "" {
        config.Model = args.Model
    }
    if args.Provider != "" {
        config.Provider = args.Provider
    }
    if args.MaxTurns > 0 {
        config.MaxTurns = args.MaxTurns
    }
    if args.SystemPrompt != "" {
        config.SystemPrompt = args.SystemPrompt
    }
    
    // Create child session with custom config
    sess := agent.NewSession(t.client, config)
    // ...
}
```

**Estimated Time:** 2 hours

---

## 📋 Implementation Checklist

### Phase 1: Fix Blocking Issues (5 hours)

- [ ] **Task 1.1:** Parse subgraph_params in SubgraphHandler (1h)
- [ ] **Task 1.2:** Inject params into child pipeline context (1h)
- [ ] **Task 1.3:** Add test `TestSubgraphHandler_ParameterPassing` (1h)
- [ ] **Task 2.1:** Implement `InterpolateAllVariables()` function (1h)
- [ ] **Task 2.2:** Integrate into codergen.go and subgraph.go (30m)
- [ ] **Task 2.3:** Add test `TestInterpolateAllVariables` (30m)

### Phase 2: Non-Blocking Enhancements (2 hours)

- [ ] **Task 3.1:** Implement edge weight sorting in selectNextEdge() (30m)
- [ ] **Task 3.2:** Add test `TestEdgeWeightPriority` (30m)
- [ ] **Task 4.1:** Extend SpawnAgentArgs with config fields (1h)

### Phase 3: Validation (1 hour)

- [ ] **End-to-end test:** Run `tracker examples/parallel-ralph-dev.dip`
- [ ] **Verify:** ${params.stream_id} interpolates correctly
- [ ] **Verify:** Subgraph branches execute with different param values
- [ ] **Update:** README with variable interpolation examples

**Total Time:** 7 hours

---

## 🔬 Test Strategy

### Unit Tests:
```bash
go test ./pipeline -v -run TestSubgraph
go test ./pipeline -v -run TestInterpolate
go test ./pipeline -v -run TestEdgeWeight
```

### Integration Test:
```bash
# This MUST work after fixes:
tracker examples/parallel-ralph-dev.dip --no-tui

# Should create:
# - .ai/streams/stream-a/iteration-log.md
# - .ai/streams/stream-b/iteration-log.md

# Should NOT contain literal "${params.stream_id}" in any file
```

### Validation:
```bash
tracker validate examples/parallel-ralph-dev.dip
# Should report: valid (21 nodes, 23 edges)
# Should NOT warn about undefined variables
```

---

## 📊 Spec Compliance Matrix

| Dippin Feature | Status | Evidence |
|----------------|--------|----------|
| **Node Types** | | |
| └─ agent | ✅ 100% | pipeline/handlers/codergen.go |
| └─ human | ✅ 100% | pipeline/handlers/human.go |
| └─ tool | ✅ 100% | pipeline/handlers/tool.go |
| └─ parallel | ✅ 100% | pipeline/handlers/parallel.go |
| └─ fan_in | ✅ 100% | pipeline/handlers/parallel.go |
| └─ subgraph | ⚠️ 70% | pipeline/subgraph.go (params broken) |
| **Variables** | | |
| └─ ${ctx.X} | ✅ 100% | pipeline/context.go:ExpandPromptVariables |
| └─ ${params.X} | ❌ 0% | Not implemented |
| └─ ${graph.X} | ❌ 0% | Not implemented |
| **Edge Features** | | |
| └─ Conditionals | ✅ 100% | pipeline/engine.go:evaluateCondition |
| └─ Labels | ✅ 100% | Used in TUI display |
| └─ Weights | ⚠️ 50% | Extracted but not used in routing |
| **Agent Config** | | |
| └─ model/provider | ✅ 100% | handlers/codergen.go |
| └─ reasoning_effort | ✅ 100% | handlers/codergen.go:200-206 |
| └─ fidelity | ✅ 100% | pipeline/fidelity.go |
| └─ max_turns | ✅ 100% | handlers/codergen.go |
| └─ goal_gate | ✅ 100% | pipeline/engine.go |
| └─ auto_status | ✅ 100% | handlers/codergen.go:parseAutoStatus |
| **Tools** | | |
| └─ Built-in tools | ✅ 100% | agent/tools/*.go |
| └─ spawn_agent | ⚠️ 50% | Works but limited config |
| **Runtime** | | |
| └─ Checkpointing | ✅ 100% | pipeline/engine.go |
| └─ Retries | ✅ 100% | pipeline/engine.go |
| └─ Parallel exec | ✅ 100% | pipeline/handlers/parallel.go |
| └─ Context compaction | ✅ 100% | pipeline/compact.go |

**Overall:** ~85% complete (was claimed 95-100%)

---

## 🎯 Recommended Action Plan

### Immediate (This Sprint):

1. ✅ **Accept reality:** Tracker is ~85% complete, not "production ready"
2. 🔨 **Fix blocking bugs:**
   - Subgraph parameter injection (3h)
   - Full variable interpolation (2h)
3. ✅ **Add tests:** Verify fixes with unit + integration tests
4. 📝 **Update docs:** README should document variable namespaces

### Next Sprint:

5. 🔧 **Polish features:**
   - Edge weight prioritization (1h)
   - Spawn agent config (2h)
6. 📊 **Comprehensive testing:** Run all 21 example workflows
7. 🚀 **Beta release:** Mark as "Beta" until proven stable

### Long Term:

8. 📚 **Feature tracking:** Create issues for each gap
9. 🔍 **Better QA:** Require runtime tests before marking features "complete"
10. 📖 **Spec compliance report:** Maintain living document of Dippin parity

---

## 🚨 Critical Warnings

### DO NOT:
- ❌ Ship with "production ready" label
- ❌ Merge examples/parallel-ralph-dev.dip without fixing params
- ❌ Claim "100% subgraph support" in marketing

### DO:
- ✅ Label as "Beta" or "85% spec compliant"
- ✅ Document known limitations in README
- ✅ Provide workarounds for missing features
- ✅ Set clear roadmap for remaining 15%

---

## 📖 References

**Code Files:**
- `pipeline/subgraph.go` — Subgraph handler (params not injected)
- `pipeline/dippin_adapter.go` — IR conversion (extracts but doesn't use params)
- `pipeline/context.go` — Variable interpolation (only ${ctx.X})
- `pipeline/handlers/codergen.go` — Agent execution (reasoning_effort works)
- `pipeline/engine.go` — Routing logic (ignores weights)

**Examples:**
- `examples/parallel-ralph-dev.dip` — Uses subgraph params (BROKEN)
- `examples/subgraphs/adaptive-ralph-stream.dip` — Depends on ${params.X}

**Tests:**
- `pipeline/subgraph_test.go` — 6 tests, NONE verify params
- `pipeline/dippin_adapter_test.go` — Tests extraction, not injection

**Planning Docs:**
- `docs/plans/2026-03-21-VALIDATION-REPORT.md` — Acknowledges gaps but downplays severity
- `docs/plans/2026-03-21-dippin-feature-parity-FINAL-REVIEW.md` — Claims 98% complete (incorrect)

---

**Conclusion:**

Tracker has a **solid foundation** with **good architecture**, but is **not ready for production** due to 2 blocking bugs in subgraph params and variable interpolation. The remaining work is **well-scoped** (7 hours) and **straightforward to fix**. 

After fixing these gaps, Tracker will be a **complete, production-ready implementation** of the Dippin language specification.
