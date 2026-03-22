# Implementation Checklist: Variable Interpolation

## Quick Reference

**Goal:** Implement `${namespace.key}` variable expansion for 100% dippin-lang parity  
**Effort:** 6 hours  
**Files:** 4 new, 3 modified  

---

## ✅ Task Breakdown

### [ ] Task 1: Core Expansion Function (2h)
**File:** `pipeline/expand.go` (new)

```go
func ExpandVariables(
    text string,
    ctx *PipelineContext,
    params map[string]string,
    graphAttrs map[string]string,
    strict bool,
) (string, error)
```

**Tests to write:**
- [ ] TestExpandVariables_CtxNamespace
- [ ] TestExpandVariables_ParamsNamespace  
- [ ] TestExpandVariables_GraphNamespace
- [ ] TestExpandVariables_MultipleVariables
- [ ] TestExpandVariables_UndefinedStrict
- [ ] TestExpandVariables_UndefinedLenient
- [ ] TestExpandVariables_NoVariables
- [ ] TestExpandVariables_MalformedSyntax

**Algorithm:**
1. Scan for `${`
2. Find matching `}`
3. Parse `namespace.key`
4. Lookup value (ctx/params/graph)
5. Replace or error
6. Continue scanning

---

### [ ] Task 2: Subgraph Param Injection (2h)
**File:** `pipeline/subgraph.go` (modify)

**Functions to add:**
```go
func parseSubgraphParams(s string) map[string]string
func injectParamsIntoGraph(g *Graph, params map[string]string) *Graph
```

**Update:**
```go
func (h *SubgraphHandler) Execute(...) (Outcome, error) {
    params := parseSubgraphParams(node.Attrs["subgraph_params"])
    subGraphWithParams := injectParamsIntoGraph(subGraph, params)
    engine := NewEngine(subGraphWithParams, ...)
    // ...
}
```

**Tests to write:**
- [ ] TestParseSubgraphParams
- [ ] TestInjectParamsIntoGraph_Prompts
- [ ] TestInjectParamsIntoGraph_Attributes  
- [ ] TestSubgraphHandler_ParamPassing

**Expand in:**
- [ ] node.Attrs["prompt"]
- [ ] node.Attrs["system_prompt"]
- [ ] node.Attrs["model"]
- [ ] node.Attrs["label"]

---

### [ ] Task 3: Codergen Integration (1h)
**File:** `pipeline/handlers/codergen.go` (modify)

**Change:**
```go
// OLD (line ~120):
prompt = pipeline.InjectPipelineContext(prompt, pctx)

// NEW:
prompt, err = pipeline.ExpandVariables(prompt, pctx, nil, h.graphAttrs, false)
if err != nil {
    // Log but don't fail (lenient mode)
}
```

**Tests to write:**
- [ ] TestCodergenHandler_VariableExpansion
- [ ] TestCodergenHandler_BackwardCompat

---

### [ ] Task 4: Integration Tests (1h)
**Files:** 
- `pipeline/expand_e2e_test.go` (new)
- `testdata/expand_ctx_vars.dip` (new)
- `testdata/expand_subgraph_params.dip` (new)
- `testdata/expand_graph_attrs.dip` (new)

**Tests to write:**
- [ ] TestE2E_ContextVariableExpansion
- [ ] TestE2E_SubgraphParamPassing
- [ ] TestE2E_GraphAttributeExpansion

---

## ✅ Variable Reference

### Supported Namespaces

**ctx.*** - Runtime context
- `ctx.outcome` - Last node status (success/fail/retry)
- `ctx.last_response` - Previous agent output
- `ctx.human_response` - Human input
- `ctx.tool_stdout` - Tool command output
- `ctx.tool_stderr` - Tool command errors
- `ctx.*` - Any custom keys from ContextUpdates

**params.*** - Subgraph parameters
- `params.severity` - Example: passed from parent
- `params.model` - Example: LLM model override
- `params.*` - Any key in subgraph params map

**graph.*** - Workflow attributes  
- `graph.goal` - Workflow goal string
- `graph.name` - Workflow name
- `graph.start` - Start node ID
- `graph.exit` - Exit node ID

---

## ✅ Test Matrix

| Scenario | Test File | Expected |
|----------|-----------|----------|
| Single ctx var | expand_test.go | Expansion works |
| Multiple vars | expand_test.go | All expand |
| Undefined (lenient) | expand_test.go | Empty string |
| Undefined (strict) | expand_test.go | Error returned |
| No variables | expand_test.go | Unchanged |
| Malformed syntax | expand_test.go | Return as-is |
| Subgraph params | expand_e2e_test.go | Child sees params |
| Graph attrs | expand_e2e_test.go | Expands goal |
| Backward compat | codergen_test.go | Old prompts work |

---

## ✅ Validation Checklist

Before marking complete:

### Code Quality
- [ ] All functions have ABOUTME comments
- [ ] No hardcoded strings (use constants)
- [ ] Error messages are descriptive
- [ ] Follows existing code patterns
- [ ] No TODO/FIXME comments left

### Testing
- [ ] `go test ./pipeline/...` passes
- [ ] `go test -race ./...` passes (no races)
- [ ] `go test -cover ./pipeline` shows >95%
- [ ] Integration tests with .dip files pass
- [ ] All existing tests pass (regression)

### Documentation
- [ ] README updated with ${} examples
- [ ] CHANGELOG entry added
- [ ] Function comments complete
- [ ] Example .dip files added to examples/

### Functionality
- [ ] `${ctx.human_response}` expands correctly
- [ ] `${params.key}` works in subgraphs
- [ ] `${graph.goal}` expands to workflow goal
- [ ] Undefined vars don't crash (lenient)
- [ ] Multiple vars in one string work
- [ ] No vars = unchanged output

---

## ✅ Common Pitfalls

**Don't:**
- ❌ Use regex for parsing (too slow)
- ❌ Mutate original graph in subgraph handler
- ❌ Crash on undefined variables (use lenient)
- ❌ Break backward compatibility
- ❌ Forget to clone graph before injection

**Do:**
- ✅ Use simple string scanning
- ✅ Clone graph before param injection
- ✅ Default to lenient mode
- ✅ Keep backward compatibility
- ✅ Test edge cases thoroughly

---

## ✅ Example Usage

### Example 1: Context Variables
```dippin
agent Summarize
  prompt:
    The user requested: ${ctx.human_response}
    The previous analysis was: ${ctx.last_response}
    Our mission: ${graph.goal}
```

### Example 2: Subgraph Params
```dippin
# parent.dip
subgraph SecurityScan
  ref: security/scanner
  params:
    severity: critical
    model: claude-opus-4-6

# security/scanner.dip  
agent Scanner
  model: ${params.model}
  prompt:
    Scan for ${params.severity} vulnerabilities.
```

### Example 3: Mixed Namespaces
```dippin
agent Reporter
  prompt:
    # Graph context
    Workflow: ${graph.name}
    Goal: ${graph.goal}
    
    # Runtime context
    Current status: ${ctx.outcome}
    Previous output: ${ctx.last_response}
    
    Please generate a summary report.
```

---

## ✅ Quick Test

After implementation, verify with:

```bash
# Create test file
cat > test_vars.dip <<'EOF'
workflow VarTest
  goal: "Test variable expansion"
  start: Demo
  exit: Demo
  
  agent Demo
    prompt:
      Goal is: ${graph.goal}
      This should expand correctly!
EOF

# Run it
tracker test_vars.dip --no-tui

# Should execute without errors
# Prompt should contain: "Goal is: Test variable expansion"
```

---

## ✅ Estimated Timeline

| Day | Tasks | Hours |
|-----|-------|-------|
| 1 | Task 1 (core expansion) | 2 |
| 1 | Task 2 (subgraph params) | 2 |
| 2 | Task 3 (codergen integration) | 1 |
| 2 | Task 4 (integration tests) | 1 |
| 2 | Documentation + polish | - |

**Total: 6 hours over 2 days**

---

## ✅ Success Indicators

You're done when:

1. ✅ Can run `.dip` files with `${ctx.*}` syntax
2. ✅ Subgraphs receive and use params correctly
3. ✅ Graph attributes accessible via `${graph.*}`
4. ✅ All tests green
5. ✅ No regressions
6. ✅ Documentation complete

**Final validation:** All examples from `/tmp/dippin-lang/examples/` work in tracker
