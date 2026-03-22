# Task Specification: Implement Dippin Variable Interpolation

**Date:** 2026-03-21  
**Priority:** Critical  
**Estimated Effort:** 6 hours  
**Status:** Ready for Implementation

---

## Objective

Implement the missing `${namespace.key}` variable interpolation syntax required by the dippin-lang specification. This is the final feature needed to achieve 100% dippin-lang feature parity in tracker.

---

## Background

### Current State
Tracker currently uses `InjectPipelineContext()` which **appends** pipeline context as markdown sections at the end of prompts. This works but:
- ❌ Doesn't follow the dippin spec `${ctx.X}` syntax
- ❌ No control over variable placement in prompts  
- ❌ Subgraph params are extracted but never injected
- ❌ Can't selectively reference graph attributes

### What Dippin Spec Requires
```dippin
agent Analyze
  prompt:
    User input: ${ctx.human_response}
    Our goal: ${graph.goal}
    Last output: ${ctx.last_response}

subgraph SecurityScan
  ref: security/scan_workflow
  params:
    severity: critical
    model: gpt-5.4

# In security/scan_workflow.dip:
agent Scanner
  model: ${params.model}
  prompt:
    Scan for ${params.severity} vulnerabilities.
```

### Gap Analysis Summary
- ✅ All 12 semantic lint rules implemented (DIP101-DIP112)
- ✅ Reasoning effort wired to LLM
- ✅ Subgraph handler exists with context propagation
- ✅ Edge weights, restart edges, auto status, goal gates all implemented
- ❌ **Variable interpolation syntax missing** ← This task

---

## Scope

### In Scope

1. **Variable Expansion Function**
   - Parse and expand `${namespace.key}` syntax
   - Support 3 namespaces: `ctx`, `params`, `graph`
   - Handle undefined variables gracefully

2. **Context Namespace (`${ctx.*}`)**
   - Expand from PipelineContext values
   - Support reserved keys: `outcome`, `last_response`, `human_response`, `tool_stdout`, etc.
   - Support custom keys written by nodes

3. **Params Namespace (`${params.*}`)**
   - Parse subgraph `params` attribute
   - Inject params into child graph before execution
   - Available in child prompts, attributes, conditions

4. **Graph Namespace (`${graph.*}`)**
   - Expand from Graph.Attrs
   - Support: `goal`, `name`, `start`, `exit`
   - Support custom graph-level attributes

5. **Integration Points**
   - Codergen handler prompts
   - Subgraph param passing
   - Potentially edge conditions (future enhancement)
   - Potentially human node labels (future enhancement)

### Out of Scope

- Edge condition expansion (can be follow-up)
- Custom variable namespaces beyond ctx/params/graph
- Escape sequences for literal `${` (can add if needed)
- Variable type validation (all strings)
- Nested variable expansion (`${ctx.${foo}}`)

---

## Requirements

### Functional Requirements

**FR1: Expand ${ctx.key} variables**
- Given: PipelineContext has `outcome = "success"`
- When: Prompt contains `Status: ${ctx.outcome}`
- Then: Result is `Status: success`

**FR2: Expand ${params.key} variables in subgraphs**
- Given: Subgraph node has `params: severity=critical,model=gpt-5.4`
- When: Child workflow prompt has `Scan for ${params.severity}`
- Then: Child sees `Scan for critical`

**FR3: Expand ${graph.key} variables**
- Given: Workflow has `goal: "Review code for bugs"`
- When: Prompt has `Our goal: ${graph.goal}`
- Then: Result is `Our goal: Review code for bugs`

**FR4: Handle undefined variables gracefully**
- Given: Variable `${ctx.nonexistent}` is referenced
- When: Expansion runs in lenient mode (default)
- Then: Variable replaced with empty string, no error
- When: Expansion runs in strict mode (opt-in)
- Then: Returns error with clear message

**FR5: Support multiple variables in one string**
- Given: Prompt `User: ${ctx.human_response}, Goal: ${graph.goal}`
- When: human_response="build app", goal="Create software"
- Then: Result is `User: build app, Goal: Create software`

**FR6: Backward compatibility with non-template prompts**
- Given: Legacy prompt with no `${...}` variables
- When: Prompt processed
- Then: Returns unchanged (no appended context sections for template prompts)

### Non-Functional Requirements

**NFR1: Performance**
- Variable expansion adds <10ms overhead per prompt
- No regex, use simple string scanning

**NFR2: Error Messages**
- Undefined variable errors include: variable name, namespace, available keys

**NFR3: Code Quality**
- Comprehensive unit test coverage (>95%)
- Integration tests with real .dip files
- ABOUTME comments on all new functions

---

## Design

### Core Function Signature

```go
// ExpandVariables replaces ${namespace.key} patterns with values from the provided sources.
// Supports three namespaces:
//   - ctx: runtime context (from PipelineContext)
//   - params: subgraph parameters (passed explicitly)
//   - graph: graph-level attributes (from Graph.Attrs)
//
// In lenient mode (strict=false), undefined variables expand to empty string.
// In strict mode (strict=true), undefined variables return an error.
func ExpandVariables(
    text string,
    ctx *PipelineContext,
    params map[string]string,
    graphAttrs map[string]string,
    strict bool,
) (string, error)
```

### Algorithm

```
1. Initialize result = text
2. Find first occurrence of "${"
3. If not found, return result
4. Find matching "}" 
5. Extract variable: namespace.key
6. Parse namespace and key
7. Look up value:
   - ctx: call ctx.Get(key)
   - params: lookup in params map
   - graph: lookup in graphAttrs map
8. If not found:
   - strict mode: return error
   - lenient mode: value = ""
9. Replace ${namespace.key} with value
10. Continue from step 2 with remainder
11. Return result
```

### Subgraph Param Injection

**Current SubgraphHandler.Execute():**
```go
func (h *SubgraphHandler) Execute(ctx context.Context, node *Node, pctx *PipelineContext) (Outcome, error) {
    ref := node.Attrs["subgraph_ref"]
    subGraph := h.graphs[ref]
    
    engine := NewEngine(subGraph, h.registry, WithInitialContext(pctx.Snapshot()))
    result, err := engine.Run(ctx)
    // ...
}
```

**Updated with param injection:**
```go
func (h *SubgraphHandler) Execute(ctx context.Context, node *Node, pctx *PipelineContext) (Outcome, error) {
    ref := node.Attrs["subgraph_ref"]
    subGraph := h.graphs[ref]
    
    // Parse params from node attrs
    params := parseSubgraphParams(node.Attrs["subgraph_params"])
    
    // Clone the subgraph and inject params into all relevant fields
    subGraphWithParams := injectParamsIntoGraph(subGraph, params)
    
    engine := NewEngine(subGraphWithParams, h.registry, WithInitialContext(pctx.Snapshot()))
    result, err := engine.Run(ctx)
    // ...
}
```

### Integration Points

**1. Codergen Handler**
```go
// BEFORE (pipeline/handlers/codergen.go):
prompt = pipeline.InjectPipelineContext(prompt, pctx)

// AFTER:
prompt, err = pipeline.ExpandVariables(prompt, pctx, nil, h.graphAttrs, false)
if err != nil {
    // Log warning but continue (lenient mode)
}
```

**2. Subgraph Handler**
```go
// New helper function in pipeline/subgraph.go:
func injectParamsIntoGraph(g *Graph, params map[string]string) *Graph {
    clone := g.Clone()
    
    // Expand variables in all node prompts
    for _, node := range clone.Nodes {
        if prompt, ok := node.Attrs["prompt"]; ok {
            expanded, _ := ExpandVariables(prompt, nil, params, clone.Attrs, false)
            node.Attrs["prompt"] = expanded
        }
        // Also expand model, system_prompt, label, etc.
        // ...
    }
    
    return clone
}
```

---

## Implementation Plan

### Task 1: Core Variable Expansion Function (2 hours)

**File:** `pipeline/expand.go` (new)

**Steps:**
1. Create `ExpandVariables()` function
2. Implement simple string scanner (no regex)
3. Parse `${namespace.key}` syntax
4. Look up values from ctx/params/graph
5. Handle undefined variables (strict/lenient modes)
6. Return expanded string

**Test Cases:**
```go
func TestExpandVariables_CtxNamespace(t *testing.T)
func TestExpandVariables_ParamsNamespace(t *testing.T)
func TestExpandVariables_GraphNamespace(t *testing.T)
func TestExpandVariables_MultipleVariables(t *testing.T)
func TestExpandVariables_UndefinedStrict(t *testing.T)
func TestExpandVariables_UndefinedLenient(t *testing.T)
func TestExpandVariables_NoVariables(t *testing.T)
func TestExpandVariables_MalformedSyntax(t *testing.T)
```

### Task 2: Subgraph Param Parsing & Injection (2 hours)

**Files:**
- `pipeline/subgraph.go` (modify)
- `pipeline/expand.go` (helper functions)

**Steps:**
1. Create `parseSubgraphParams()` to parse `key1=val1,key2=val2` format
2. Create `injectParamsIntoGraph()` to expand vars in graph clone
3. Update `SubgraphHandler.Execute()` to use param injection
4. Expand variables in: prompt, system_prompt, model, label, conditions

**Test Cases:**
```go
func TestParseSubgraphParams(t *testing.T)
func TestInjectParamsIntoGraph_Prompts(t *testing.T)
func TestInjectParamsIntoGraph_Attributes(t *testing.T)
func TestSubgraphHandler_ParamPassing(t *testing.T)
```

### Task 3: Integrate with Codergen Handler (1 hour)

**File:** `pipeline/handlers/codergen.go` (modify)

**Steps:**
1. Replace `InjectPipelineContext()` call with `ExpandVariables()`
2. Pass PipelineContext, nil params, graphAttrs, lenient mode
3. Keep backward compat: if no ${} found, result unchanged
4. Add error logging for expansion failures

**Test Cases:**
```go
func TestCodergenHandler_VariableExpansion(t *testing.T)
func TestCodergenHandler_BackwardCompat(t *testing.T)
```

### Task 4: Integration Tests (1 hour)

**File:** `pipeline/expand_e2e_test.go` (new)

**Steps:**
1. Create `.dip` test files with ${} syntax
2. Test parent → subgraph param passing
3. Test ctx.* variables in prompts
4. Test graph.* variables
5. Verify all namespaces work together

**Test Files:**
```
testdata/expand_ctx_vars.dip
testdata/expand_subgraph_params.dip
testdata/expand_graph_attrs.dip
```

**Test Cases:**
```go
func TestE2E_ContextVariableExpansion(t *testing.T)
func TestE2E_SubgraphParamPassing(t *testing.T)
func TestE2E_GraphAttributeExpansion(t *testing.T)
```

---

## Testing Strategy

### Unit Tests (80% of test effort)

Each function gets comprehensive test coverage:

**ExpandVariables():**
- Each namespace (ctx, params, graph) individually
- Multiple variables in one string
- Undefined variables (strict and lenient)
- Edge cases: empty string, no variables, malformed syntax
- Boundary cases: nested braces, escaped characters

**parseSubgraphParams():**
- Simple params: `key=val`
- Multiple params: `k1=v1,k2=v2`
- Edge cases: empty string, malformed pairs, special characters

**injectParamsIntoGraph():**
- Expand prompt attribute
- Expand system_prompt, model, label
- Expand edge conditions (if implemented)
- Verify original graph unchanged (immutability)

### Integration Tests (20% of test effort)

End-to-end workflows:

**Scenario 1: Context variables in agent prompt**
```dippin
workflow TestCtx
  start: Ask
  exit: Process
  
  human Ask
    label: "What to build?"
    mode: freeform
  
  agent Process
    prompt:
      User wants: ${ctx.human_response}
      Build it.
```

**Scenario 2: Subgraph param passing**
```dippin
# parent.dip
workflow Parent
  start: Run
  exit: Run
  
  subgraph Run
    ref: child
    params:
      task: review code
      severity: high

# child.dip
workflow Child
  start: Do
  exit: Do
  
  agent Do
    prompt:
      Task: ${params.task}
      Severity: ${params.severity}
```

**Scenario 3: Graph attributes**
```dippin
workflow GoalAware
  goal: "Build a secure authentication system"
  start: Work
  exit: Work
  
  agent Work
    prompt:
      Remember, our goal is: ${graph.goal}
```

### Regression Tests

Run all existing tests to ensure no breakage:
```bash
go test ./...
```

Verify all examples/ pipelines still execute:
```bash
for f in examples/*.dip; do
  tracker "$f" --no-tui || echo "FAIL: $f"
done
```

---

## Success Criteria

### Functional Success

- [ ] `${ctx.outcome}` expands to current outcome value
- [ ] `${ctx.last_response}` expands to previous node's output
- [ ] `${ctx.human_response}` expands to human input
- [ ] `${params.key}` expands in subgraph child prompts
- [ ] `${graph.goal}` expands to workflow goal
- [ ] Multiple variables in one string all expand
- [ ] Undefined variables handled gracefully (no crashes)
- [ ] Empty string result when variable not found (lenient mode)
- [ ] Error returned when variable not found (strict mode)

### Quality Success

- [ ] All unit tests pass (`go test ./pipeline/...`)
- [ ] All integration tests pass
- [ ] No regressions in existing tests
- [ ] Test coverage >95% for new code
- [ ] All functions have ABOUTME comments
- [ ] No linter warnings (`golangci-lint run`)

### Documentation Success

- [ ] Update README with ${} syntax examples
- [ ] Document supported namespaces (ctx, params, graph)
- [ ] Add example .dip files to examples/ directory
- [ ] Update CHANGELOG with feature addition

---

## Edge Cases & Error Handling

### Edge Case 1: Malformed Variable Syntax
```
Input: "Value: ${ctx.foo"  (missing closing brace)
Behavior: Return as-is, treat as literal text
```

### Edge Case 2: Nested Braces
```
Input: "Nested: ${ ${ctx.inner} }"
Behavior: NOT SUPPORTED - treat as malformed, return as-is
```

### Edge Case 3: Empty Variable Name
```
Input: "Empty: ${}"
Behavior: Return as-is, treat as literal
```

### Edge Case 4: Unknown Namespace
```
Input: "Unknown: ${foo.bar}"
Behavior: Lenient=empty, Strict=error
```

### Edge Case 5: Reserved Context Keys
```
Input: "${ctx.outcome}" but outcome not set
Behavior: Return empty string (context keys optional)
```

### Edge Case 6: Subgraph Without Params
```
Given: Subgraph node with no params attribute
Behavior: Child ${params.X} expands to empty (no error)
```

---

## Risk Assessment

| Risk | Probability | Impact | Mitigation |
|------|-------------|--------|------------|
| Breaking existing workflows | Low | High | Default to lenient mode, extensive regression tests |
| Performance regression | Low | Low | Simple string ops, benchmark critical paths |
| Param scope leakage | Low | Medium | Clone graphs before injection, clear param boundaries |
| Undefined var crashes | Medium | High | Lenient mode default, comprehensive error handling |
| Regex complexity | N/A | N/A | No regex, use simple scanner |

---

## Acceptance Criteria

A pull request is ready to merge when:

### Code Complete
- [ ] All 4 tasks implemented
- [ ] All functions have ABOUTME comments
- [ ] Code follows existing patterns (see pipeline/*.go)
- [ ] No hardcoded values, all configurable

### Tests Pass
- [ ] `go test ./...` passes 100%
- [ ] `go test -race ./...` passes (no race conditions)
- [ ] `go test -cover ./pipeline` shows >95% coverage
- [ ] Integration tests with .dip files pass

### Documentation Complete
- [ ] README updated with examples
- [ ] CHANGELOG entry added
- [ ] Inline code comments for complex logic

### Review Complete
- [ ] Code reviewed by at least one other developer
- [ ] All review comments addressed
- [ ] No linter warnings

---

## Follow-Up Work (Out of Scope)

After this task is complete, consider:

1. **Edge Condition Expansion** - Expand ${} in edge `when` clauses
2. **Human Label Expansion** - Expand ${} in human node labels
3. **Escape Sequences** - Support `\${` for literal `${`
4. **Strict Mode Flag** - Add CLI flag `--strict-expansion`
5. **Variable Introspection** - Tool to list all available variables at each node
6. **Performance Optimization** - Cache expanded prompts across retries

---

## Dependencies

### Internal Dependencies
- `pipeline.PipelineContext` - Context value storage
- `pipeline.Graph` - Graph attributes access
- `pipeline.SubgraphHandler` - Param injection target

### External Dependencies
None - uses only Go stdlib

### Testing Dependencies
- Existing test infrastructure
- Test .dip files in testdata/

---

## Rollout Plan

### Phase 1: Implementation (Days 1-2)
- Implement core ExpandVariables()
- Implement subgraph param injection
- Write comprehensive unit tests

### Phase 2: Integration (Day 2)
- Integrate with codergen handler
- Integration tests with real .dip files
- Regression testing

### Phase 3: Documentation & Polish (Day 3)
- Update documentation
- Add example .dip files
- Final testing and review

### Phase 4: Deployment (Day 3)
- Merge to main branch
- Tag release (v0.x.y)
- Update tracker-conformance tests

---

## Definition of Done

This task is complete when:

1. ✅ All code implemented and tested
2. ✅ All tests passing (unit + integration + regression)
3. ✅ Test coverage >95% for new code
4. ✅ Documentation updated
5. ✅ Code reviewed and approved
6. ✅ Merged to main branch
7. ✅ No open bugs or regressions
8. ✅ Examples in examples/ directory demonstrate feature

**Final Validation:**
```bash
# Create test .dip file
cat > test_vars.dip <<EOF
workflow Test
  goal: "Demonstrate variable expansion"
  start: Demo
  exit: Demo
  
  agent Demo
    prompt:
      Goal: \${graph.goal}
      This should work!
EOF

# Run it
tracker test_vars.dip --no-tui

# Should execute without errors and expand ${graph.goal}
```

---

## Questions for Clarification

Before starting implementation, clarify:

1. **Lenient vs Strict Mode:** Should strict mode be opt-in via flag or env var?
2. **Backward Compatibility:** Keep InjectPipelineContext for legacy code or remove?
3. **Performance Targets:** Any specific latency requirements for expansion?
4. **Subgraph Caching:** Should we cache param-injected graphs or expand on every call?
5. **Variable Scope:** Should params be available in parent workflow after subgraph returns?

---

## Summary

**What:** Implement ${namespace.key} variable interpolation for dippin-lang compliance

**Why:** Required spec feature, currently broken for .dip files with ${} syntax

**How:** 
1. Create ExpandVariables() function
2. Parse subgraph params and inject into child graphs
3. Replace InjectPipelineContext() with ExpandVariables()
4. Comprehensive testing

**Effort:** 6 hours (2+2+1+1)

**Impact:** Achieves 100% dippin-lang feature parity, enables full spec compliance
