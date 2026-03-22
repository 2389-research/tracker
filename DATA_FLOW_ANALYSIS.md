# Subgraph Parameter Data Flow Analysis

**Issue:** Parameters extracted but never injected into child pipeline context

---

## 🔍 Current Data Flow (BROKEN)

```
┌─────────────────────────────────────────────────────────────┐
│ examples/parallel-ralph-dev.dip                             │
├─────────────────────────────────────────────────────────────┤
│ subgraph StreamA                                            │
│   ref: subgraphs/adaptive-ralph-stream                      │
│   params:                                                   │
│     stream_id: stream-a      ◄── DEFINED HERE              │
│     max_iterations: 8                                       │
└─────────────────────────────────────────────────────────────┘
                     │
                     │ Dippin Parser
                     ▼
┌─────────────────────────────────────────────────────────────┐
│ dippin-lang/ir (Intermediate Representation)               │
├─────────────────────────────────────────────────────────────┤
│ Node {                                                      │
│   Kind: NodeSubgraph                                        │
│   Config: SubgraphConfig {                                  │
│     Ref: "subgraphs/adaptive-ralph-stream"                  │
│     Params: {                      ◄── EXTRACTED HERE      │
│       "stream_id": "stream-a",                              │
│       "max_iterations": "8"                                 │
│     }                                                       │
│   }                                                         │
│ }                                                           │
└─────────────────────────────────────────────────────────────┘
                     │
                     │ FromDippinIR()
                     ▼
┌─────────────────────────────────────────────────────────────┐
│ pipeline/dippin_adapter.go:248                              │
├─────────────────────────────────────────────────────────────┤
│ // Serialize params to string attribute                    │
│ attrs["subgraph_params"] =          ◄── SERIALIZED HERE    │
│   "stream_id=stream-a,max_iterations=8"                     │
└─────────────────────────────────────────────────────────────┘
                     │
                     │ Graph.AddNode()
                     ▼
┌─────────────────────────────────────────────────────────────┐
│ pipeline.Node                                               │
├─────────────────────────────────────────────────────────────┤
│ Node {                                                      │
│   ID: "StreamA"                                             │
│   Shape: "tab"                                              │
│   Attrs: {                         ◄── STORED HERE         │
│     "subgraph_ref": "subgraphs/...",                        │
│     "subgraph_params": "stream_id=stream-a,..."             │
│   }                                                         │
│ }                                                           │
└─────────────────────────────────────────────────────────────┘
                     │
                     │ Engine.Run() → Execute()
                     ▼
┌─────────────────────────────────────────────────────────────┐
│ pipeline/subgraph.go:28-41 (SubgraphHandler.Execute)       │
├─────────────────────────────────────────────────────────────┤
│ ref := node.Attrs["subgraph_ref"]  ✅ Used                 │
│ subGraph := h.graphs[ref]                                   │
│                                                             │
│ // ❌ PROBLEM: subgraph_params NEVER READ                  │
│ // params := node.Attrs["subgraph_params"]  ◄── MISSING!   │
│                                                             │
│ engine := NewEngine(subGraph, h.registry,                   │
│   WithInitialContext(pctx.Snapshot()))  ◄── NO PARAMS!     │
│                                                             │
│ result, err := engine.Run(ctx)                              │
└─────────────────────────────────────────────────────────────┘
                     │
                     │ Child pipeline executes
                     ▼
┌─────────────────────────────────────────────────────────────┐
│ examples/subgraphs/adaptive-ralph-stream.dip                │
├─────────────────────────────────────────────────────────────┤
│ tool Setup                                                  │
│   command: |                                                │
│     stream_dir=".ai/streams/${params.stream_id}"            │
│                              ^^^^^^^^^^^^^^^                │
│                              ❌ NOT IN CONTEXT!             │
│                                                             │
│ Expands to: ".ai/streams/${params.stream_id}"  (literal!)  │
│ Expected:   ".ai/streams/stream-a"                          │
└─────────────────────────────────────────────────────────────┘
```

---

## ✅ Required Fix (Data Flow)

```
┌─────────────────────────────────────────────────────────────┐
│ pipeline/subgraph.go:28-41 (AFTER FIX)                      │
├─────────────────────────────────────────────────────────────┤
│ ref := node.Attrs["subgraph_ref"]                           │
│ subGraph := h.graphs[ref]                                   │
│                                                             │
│ // ✅ NEW: Parse subgraph_params                            │
│ params := make(map[string]string)                           │
│ if p, ok := node.Attrs["subgraph_params"]; ok {             │
│   for _, pair := range strings.Split(p, ",") {              │
│     kv := strings.SplitN(pair, "=", 2)                      │
│     params[kv[0]] = kv[1]          ◄── PARSE HERE          │
│   }                                                         │
│ }                                                           │
│                                                             │
│ // ✅ NEW: Inject params into initial context               │
│ initialCtx := pctx.Snapshot()                               │
│ for k, v := range params {                                  │
│   initialCtx["params."+k] = v     ◄── INJECT HERE          │
│ }                                                           │
│                                                             │
│ engine := NewEngine(subGraph, h.registry,                   │
│   WithInitialContext(initialCtx)) ◄── PARAMS INCLUDED!     │
│                                                             │
│ result, err := engine.Run(ctx)                              │
└─────────────────────────────────────────────────────────────┘
                     │
                     │ Child context now has params.*
                     ▼
┌─────────────────────────────────────────────────────────────┐
│ Child Pipeline Context                                      │
├─────────────────────────────────────────────────────────────┤
│ {                                                           │
│   "params.stream_id": "stream-a",    ◄── AVAILABLE!        │
│   "params.max_iterations": "8",      ◄── AVAILABLE!        │
│   ... (plus parent context values)                          │
│ }                                                           │
└─────────────────────────────────────────────────────────────┘
                     │
                     │ Variable interpolation
                     ▼
┌─────────────────────────────────────────────────────────────┐
│ pipeline/context.go (AFTER FIX)                             │
├─────────────────────────────────────────────────────────────┤
│ func InterpolateAllVariables(text, pctx, params, graph) {   │
│   // ${ctx.X}                                               │
│   for k, v := range pctx.Values {                           │
│     text = replace("${ctx."+k+"}", v)                       │
│   }                                                         │
│   // ${params.X}  ◄── NEW                                   │
│   for k, v := range params {                                │
│     text = replace("${params."+k+"}", v)                    │
│   }                                                         │
│   // ${graph.X}  ◄── NEW                                    │
│   for k, v := range graph {                                 │
│     text = replace("${graph."+k+"}", v)                     │
│   }                                                         │
│   return text                                               │
│ }                                                           │
└─────────────────────────────────────────────────────────────┘
                     │
                     │ Tool command expanded
                     ▼
┌─────────────────────────────────────────────────────────────┐
│ examples/subgraphs/adaptive-ralph-stream.dip (WORKING)      │
├─────────────────────────────────────────────────────────────┤
│ tool Setup                                                  │
│   command: |                                                │
│     stream_dir=".ai/streams/${params.stream_id}"            │
│                                                             │
│ ✅ Interpolates to: ".ai/streams/stream-a"                  │
│ ✅ Command executes correctly                               │
│ ✅ Creates .ai/streams/stream-a/iteration-log.md            │
└─────────────────────────────────────────────────────────────┘
```

---

## 📊 Context Namespace Design

After fix, three variable namespaces will be available:

```
┌──────────────────────────────────────────────────────────┐
│ Pipeline Context Namespaces                              │
├──────────────────────────────────────────────────────────┤
│                                                          │
│ 1. ${ctx.X} — Runtime context (mutable)                 │
│    ├─ ctx.outcome = "success"                           │
│    ├─ ctx.last_response = "Completed task X"            │
│    └─ ctx.iteration_count = "3"                         │
│                                                          │
│ 2. ${params.X} — Node/subgraph parameters (immutable)   │
│    ├─ params.stream_id = "stream-a"                     │
│    ├─ params.max_iterations = "8"                       │
│    └─ params.model = "gpt-4"                            │
│                                                          │
│ 3. ${graph.X} — Graph-level attributes (immutable)      │
│    ├─ graph.goal = "Build feature"                      │
│    ├─ graph.version = "1.0"                             │
│    └─ graph.default_model = "claude-sonnet-4-6"         │
│                                                          │
└──────────────────────────────────────────────────────────┘
```

**Scoping Rules:**

- `${ctx.X}` — Available everywhere, changes as pipeline executes
- `${params.X}` — Only in subgraph children, static values
- `${graph.X}` — Available everywhere, comes from workflow defaults

**Precedence (if keys collide):**

1. Node-specific params (highest)
2. Pipeline context
3. Graph defaults (lowest)

---

## 🧪 Test Case

```go
func TestSubgraphHandler_ParameterInjection(t *testing.T) {
    // Build child pipeline that uses ${params.model}
    subGraph := NewGraph("child")
    subGraph.AddNode(&Node{ID: "s", Shape: "Mdiamond"})
    subGraph.AddNode(&Node{
        ID:    "step",
        Shape: "box",
        Attrs: map[string]string{
            "prompt": "Using model: ${params.model}",
        },
    })
    subGraph.AddNode(&Node{ID: "end", Shape: "Msquare"})
    subGraph.AddEdge(&Edge{From: "s", To: "step"})
    subGraph.AddEdge(&Edge{From: "step", To: "end"})

    // Mock codergen handler captures prompt
    var capturedPrompt string
    reg := newTestRegistry()
    reg.Register(&testHandler{
        name: "codergen",
        executeFn: func(ctx context.Context, node *Node, pctx *PipelineContext) (Outcome, error) {
            // Simulate InterpolateAllVariables
            params := extractParams(pctx) // Extract "params.*" keys
            prompt := node.Attrs["prompt"]
            prompt = InterpolateAllVariables(prompt, pctx, params, nil)
            capturedPrompt = prompt
            return Outcome{Status: OutcomeSuccess}, nil
        },
    })

    // Create subgraph handler
    handler := NewSubgraphHandler(
        map[string]*Graph{"child": subGraph},
        reg,
    )

    // Execute with params
    node := &Node{
        ID:    "sg",
        Shape: "tab",
        Attrs: map[string]string{
            "subgraph_ref":    "child",
            "subgraph_params": "model=gpt-4,task=coding",
        },
    }
    pctx := NewPipelineContext()

    outcome, err := handler.Execute(context.Background(), node, pctx)
    if err != nil {
        t.Fatalf("execute failed: %v", err)
    }
    if outcome.Status != OutcomeSuccess {
        t.Errorf("expected success, got %q", outcome.Status)
    }

    // Verify prompt interpolation
    expected := "Using model: gpt-4"
    if capturedPrompt != expected {
        t.Errorf("prompt = %q, want %q", capturedPrompt, expected)
    }
}
```

---

## 🔧 Implementation Files

**Files to modify:**

1. `pipeline/subgraph.go` — Add param parsing & injection (20 lines)
2. `pipeline/context.go` — Add InterpolateAllVariables() (30 lines)
3. `pipeline/handlers/codergen.go` — Use new interpolation function (5 lines)
4. `pipeline/subgraph_test.go` — Add parameter passing test (40 lines)
5. `pipeline/context_test.go` — Add interpolation tests (30 lines)

**Total code changes:** ~125 lines

---

## 📈 Before/After Comparison

| Aspect | Before (Current) | After (Fixed) |
|--------|-----------------|---------------|
| **Params extracted?** | ✅ Yes (dippin_adapter.go) | ✅ Yes |
| **Params in node.Attrs?** | ✅ Yes (serialized) | ✅ Yes |
| **Params parsed?** | ❌ No | ✅ Yes (subgraph.go) |
| **Params in context?** | ❌ No | ✅ Yes (params.*) |
| **${params.X} interpolates?** | ❌ No | ✅ Yes |
| **${graph.X} interpolates?** | ❌ No | ✅ Yes |
| **parallel-ralph-dev.dip works?** | ❌ Broken | ✅ Works |

---

## 🎯 Summary

**Root Cause:** Params extracted from IR but never injected into child pipeline context.

**Missing Link:** `pipeline/subgraph.go` doesn't parse `node.Attrs["subgraph_params"]`

**Fix Complexity:** Low — just parse string and merge into context map

**Fix Impact:** High — unblocks all subgraph param use cases

**Estimated Time:** 3 hours (code + tests + validation)
