# Updated Task Specification: Implement Dippin Variable Interpolation

## Executive Summary

After thorough analysis of the dippin-lang specification and tracker codebase, **only 1 feature is missing** for full dippin-lang parity:

### Missing Feature
**Variable Interpolation Syntax** - `${ctx.var}`, `${params.var}`, `${graph.var}` template expansion

### Already Implemented ✅
- ✅ All 12 semantic lint rules (DIP101-DIP112) 
- ✅ Reasoning effort runtime wiring
- ✅ Subgraph handler with context propagation (structure exists)
- ✅ Edge weights and priority selection
- ✅ Restart edges with max_restarts enforcement
- ✅ Auto status parsing and goal gates
- ✅ Context compaction

---

## What Needs to Change

### Current Behavior (Incorrect)
```go
// pipeline/handlers/codergen.go
prompt = pipeline.InjectPipelineContext(prompt, pctx)
// Appends context as markdown sections - NOT dippin spec compliant
```

### Required Behavior (Dippin Spec)
```dippin
agent Analyze
  prompt:
    User said: ${ctx.human_response}
    Goal: ${graph.goal}
    Last output: ${ctx.last_response}

subgraph SecurityScan
  ref: security/scan
  params:
    severity: critical
    model: gpt-5.4

# Inside security/scan.dip:
agent Scanner
  model: ${params.model}
  prompt:
    Scan for ${params.severity} vulnerabilities
```

---

## Implementation Tasks

### Task 1: Core Variable Expansion (2 hours)

**File:** `pipeline/expand.go` (new)

Create function:
```go
func ExpandVariables(
    text string,
    ctx *PipelineContext,
    params map[string]string,
    graphAttrs map[string]string,
    strict bool,
) (string, error)
```

**Features:**
- Parse `${namespace.key}` syntax with simple string scanner (no regex)
- Support 3 namespaces: `ctx`, `params`, `graph`
- Lenient mode (default): undefined vars → empty string
- Strict mode (opt-in): undefined vars → error

**Tests:** 8 unit tests covering all namespaces, edge cases, error handling

---

### Task 2: Subgraph Param Injection (2 hours)

**File:** `pipeline/subgraph.go` (modify)

Add param parsing and injection:
```go
func (h *SubgraphHandler) Execute(...) (Outcome, error) {
    // Parse params from node attrs
    params := parseSubgraphParams(node.Attrs["subgraph_params"])
    
    // Clone graph and expand ${params.*} variables
    subGraphWithParams := injectParamsIntoGraph(subGraph, params)
    
    // Run with expanded params
    engine := NewEngine(subGraphWithParams, ...)
    // ...
}
```

**What gets expanded:**
- Node prompts (`prompt`, `system_prompt`)
- Node attributes (`model`, `label`, etc.)
- Edge conditions (optional, can be follow-up)

**Tests:** 4 unit tests for parsing + injection + integration

---

### Task 3: Replace InjectPipelineContext (1 hour)

**File:** `pipeline/handlers/codergen.go` (modify)

Replace existing context injection:
```go
// OLD:
prompt = pipeline.InjectPipelineContext(prompt, pctx)

// NEW:
prompt, err = pipeline.ExpandVariables(prompt, pctx, nil, h.graphAttrs, false)
if err != nil {
    // Log warning but continue (lenient mode)
}
```

**Backward compatibility:** Prompts without `${}` return unchanged

**Tests:** 2 unit tests for expansion + backward compat

---

### Task 4: Integration Tests (1 hour)

**Files:** 
- `pipeline/expand_e2e_test.go` (new)
- `testdata/expand_*.dip` (new test files)

Test complete workflows:
1. Agent with `${ctx.human_response}` in prompt
2. Subgraph with `params: task=X` → child uses `${params.task}`
3. Workflow with `${graph.goal}` expansion
4. Multiple namespaces in one prompt

**Tests:** 3-4 integration tests with real .dip files

---

## Example Test Case

```dippin
# testdata/expand_subgraph_params.dip

workflow Parent
  start: CallChild
  exit: CallChild
  
  subgraph CallChild
    ref: child_workflow
    params:
      user_task: build todo app
      severity: high

# child_workflow.dip  
workflow Child
  start: Work
  exit: Work
  
  agent Work
    prompt:
      Task: ${params.user_task}
      Priority: ${params.severity}
      Please complete this.
```

**Expected result:** Child agent sees:
```
Task: build todo app
Priority: high
Please complete this.
```

---

## Success Criteria

### Must Have
- [ ] `${ctx.outcome}`, `${ctx.last_response}`, `${ctx.human_response}` all expand correctly
- [ ] Subgraph `params: k=v` → child `${params.k}` works
- [ ] `${graph.goal}`, `${graph.name}` expand to workflow attributes
- [ ] Undefined variables return empty string in lenient mode (no crashes)
- [ ] All existing tests pass (backward compatibility)
- [ ] Test coverage >95% for new code

### Nice to Have (Follow-Up Work)
- Edge condition expansion (`when ${ctx.status} = ready`)
- Human label expansion
- Escape sequences for literal `${`
- Strict mode CLI flag

---

## Risk Mitigation

| Risk | Mitigation |
|------|------------|
| Breaking existing workflows | Lenient mode default, extensive regression testing |
| Subgraph param scope leakage | Clone graphs before injection, clear boundaries |
| Performance overhead | Simple string ops (no regex), <10ms per prompt |
| Undefined variable crashes | Graceful handling, comprehensive error messages |

---

## Files to Modify

### New Files
- `pipeline/expand.go` - Core variable expansion logic
- `pipeline/expand_test.go` - Unit tests for expansion
- `pipeline/expand_e2e_test.go` - Integration tests
- `testdata/expand_*.dip` - Test .dip files

### Modified Files  
- `pipeline/subgraph.go` - Add param parsing and injection
- `pipeline/handlers/codergen.go` - Replace InjectPipelineContext
- `README.md` - Document ${} syntax
- `CHANGELOG.md` - Add feature entry

---

## Estimated Effort

| Task | Hours |
|------|-------|
| Core variable expansion function | 2 |
| Subgraph param injection | 2 |
| Codergen integration | 1 |
| Integration tests + docs | 1 |
| **TOTAL** | **6 hours** |

---

## Definition of Done

This task is complete when:

1. ✅ Function `ExpandVariables()` implemented with all 3 namespaces
2. ✅ Subgraph param injection working end-to-end
3. ✅ Codergen handler uses ExpandVariables instead of InjectPipelineContext
4. ✅ All unit tests pass (>95% coverage)
5. ✅ Integration tests with real .dip files pass
6. ✅ All existing tests pass (no regressions)
7. ✅ Documentation updated with examples
8. ✅ Code reviewed and merged

---

## Why This Matters

This is the **final 8%** needed to achieve 100% dippin-lang feature parity. After this:

- ✅ Tracker becomes the **reference implementation** for dippin-lang
- ✅ All `.dip` examples from dippin-lang repo will work in tracker
- ✅ Full spec compliance for variable interpolation
- ✅ Subgraph composition becomes fully functional (params actually work)
- ✅ Users get precise control over prompt structure

Without this feature:
- ❌ `.dip` files with `${...}` syntax fail
- ❌ Subgraph params are broken (extracted but never used)
- ❌ Can't reference graph.goal selectively
- ❌ Not dippin spec compliant

---

## Next Steps for Implementation

1. **Start with Task 1** - Core ExpandVariables() function + tests
2. **Then Task 2** - Subgraph param injection
3. **Then Task 3** - Codergen integration  
4. **Finally Task 4** - Integration tests + docs
5. **Validate** - Run all tests, verify examples work

Follow TDD: Write test → Implement → Verify → Commit

---

## Questions?

If any requirements are unclear:
- Check `TASK_SPEC_VARIABLE_INTERPOLATION.md` for detailed design
- Check `DIPPIN_FEATURE_GAP_ANALYSIS.md` for full feature comparison
- Reference dippin-lang spec: `/tmp/dippin-lang/docs/*.md`
