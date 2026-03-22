# Action Plan: Immediate Next Steps

**Date:** 2024-03-21  
**Priority:** HIGH  
**Time Required:** 1.5 hours  
**Status:** Ready for implementation

---

## TL;DR

**Good News:** Tracker is 100% feature-complete with dippin-lang (not 98% as Claude claimed).  
**Bad News:** One critical robustness gap (circular subgraph refs) blocks production.  
**Action:** Implement max nesting depth check (1.5 hours) → ready to ship.

---

## Critical Fix Required

### Issue: Circular Subgraph Reference Protection

**Problem:**
```
A.dip references B.dip as subgraph
B.dip references A.dip as subgraph
→ Infinite recursion → Stack overflow → Crash
```

**Current Code:**
```go
// pipeline/subgraph.go (line ~50)
func (h *SubgraphHandler) Execute(...) {
    subGraph := h.graphs[ref]
    // ⚠️ No depth tracking - can recurse infinitely
    engine := NewEngine(subGraphWithParams, h.registry, ...)
    return engine.Run(ctx)
}
```

**Risk:** HIGH - production crash scenario

**Effort:** 1 hour implementation + 30 min testing

---

## Implementation Guide

### Step 1: Add Depth Tracking (45 minutes)

**File:** `pipeline/subgraph.go`

**Add constant:**
```go
const (
    MaxSubgraphDepth = 32  // Reasonable nesting limit
    InternalKeySubgraphDepth = "_subgraph_depth"
)
```

**Modify `Execute` method:**
```go
func (h *SubgraphHandler) Execute(ctx context.Context, node *Node, pctx *PipelineContext) (Outcome, error) {
    ref, ok := node.Attrs["subgraph_ref"]
    if !ok || ref == "" {
        return Outcome{Status: OutcomeFail}, fmt.Errorf("node %q missing subgraph_ref attribute", node.ID)
    }

    // ===== ADD THIS SECTION =====
    // Get current depth from internal context
    depthStr, _ := pctx.GetInternal(InternalKeySubgraphDepth)
    depth := 0
    if depthStr != "" {
        depth, _ = strconv.Atoi(depthStr)
    }
    
    // Check max depth
    if depth >= MaxSubgraphDepth {
        return Outcome{Status: OutcomeFail}, fmt.Errorf(
            "subgraph %q: max nesting depth (%d) exceeded - possible circular reference",
            ref, MaxSubgraphDepth,
        )
    }
    // ===== END NEW SECTION =====

    subGraph, ok := h.graphs[ref]
    if !ok {
        return Outcome{Status: OutcomeFail}, fmt.Errorf("subgraph %q not found", ref)
    }

    params := ParseSubgraphParams(node.Attrs["subgraph_params"])
    subGraphWithParams, err := InjectParamsIntoGraph(subGraph, params)
    if err != nil {
        return Outcome{Status: OutcomeFail}, fmt.Errorf("failed to inject params into subgraph %q: %w", ref, err)
    }

    // ===== ADD THIS SECTION =====
    // Increment depth for child execution
    childContext := WithInitialContext(pctx.Snapshot())
    childContext.SetInternal(InternalKeySubgraphDepth, strconv.Itoa(depth+1))
    
    engine := NewEngine(subGraphWithParams, h.registry, childContext)
    // ===== END MODIFICATION =====
    
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

**Don't forget:**
```go
import "strconv"  // Add to imports
```

---

### Step 2: Add Test Case (30 minutes)

**File:** `pipeline/subgraph_test.go`

**Add at end of file:**
```go
func TestSubgraphHandler_CircularReference(t *testing.T) {
    // Create graph A that references graph B
    graphA := NewGraph("A")
    graphA.StartNode = "start"
    graphA.ExitNode = "exit"
    graphA.AddNode(&Node{ID: "start", Shape: "Mdiamond", Handler: "start"})
    graphA.AddNode(&Node{
        ID:     "call_b",
        Shape:  "tab",
        Handler: "subgraph",
        Attrs:  map[string]string{"subgraph_ref": "B"},
    })
    graphA.AddNode(&Node{ID: "exit", Shape: "Msquare", Handler: "exit"})
    graphA.AddEdge(&Edge{From: "start", To: "call_b"})
    graphA.AddEdge(&Edge{From: "call_b", To: "exit"})

    // Create graph B that references graph A
    graphB := NewGraph("B")
    graphB.StartNode = "start"
    graphB.ExitNode = "exit"
    graphB.AddNode(&Node{ID: "start", Shape: "Mdiamond", Handler: "start"})
    graphB.AddNode(&Node{
        ID:     "call_a",
        Shape:  "tab",
        Handler: "subgraph",
        Attrs:  map[string]string{"subgraph_ref": "A"},
    })
    graphB.AddNode(&Node{ID: "exit", Shape: "Msquare", Handler: "exit"})
    graphB.AddEdge(&Edge{From: "start", To: "call_a"})
    graphB.AddEdge(&Edge{From: "call_a", To: "exit"})

    // Create registry and subgraph handler
    registry := NewHandlerRegistry()
    registry.Register(&StartHandler{})
    registry.Register(&ExitHandler{})
    
    graphs := map[string]*Graph{"A": graphA, "B": graphB}
    registry.Register(NewSubgraphHandler(graphs, registry))

    // Execute graph A (which calls B, which calls A...)
    engine := NewEngine(graphA, registry)
    ctx := context.Background()
    
    result, err := engine.Run(ctx)
    
    // Should return error about max depth exceeded
    if err == nil {
        t.Fatal("expected error for circular reference, got nil")
    }
    
    if !strings.Contains(err.Error(), "max nesting depth") {
        t.Errorf("expected 'max nesting depth' error, got: %v", err)
    }
    
    if result.Status != OutcomeFail {
        t.Errorf("expected OutcomeFail, got %s", result.Status)
    }
}
```

---

### Step 3: Verify Fix (15 minutes)

**Run tests:**
```bash
# Test the new functionality
go test ./pipeline -run TestSubgraphHandler_CircularReference -v

# Run all subgraph tests
go test ./pipeline -run TestSubgraphHandler -v

# Full test suite
go test ./...

# Check coverage
go test ./pipeline -cover
```

**Expected output:**
```
=== RUN   TestSubgraphHandler_CircularReference
--- PASS: TestSubgraphHandler_CircularReference (0.00s)
PASS
ok  	github.com/2389-research/tracker/pipeline	0.123s
```

**Create test pipeline files:**

**File:** `testdata/circular_a.dip`
```dip
workflow circular_a {
  start: Start
  exit: End
  
  node Start {
    kind: agent
    prompt: "Starting A"
  }
  
  node CallB {
    kind: subgraph
    subgraph_ref: "circular_b"
  }
  
  node End {
    kind: agent
    prompt: "Ending A"
  }
  
  edge Start -> CallB
  edge CallB -> End
}
```

**File:** `testdata/circular_b.dip`
```dip
workflow circular_b {
  start: Start
  exit: End
  
  node Start {
    kind: agent
    prompt: "Starting B"
  }
  
  node CallA {
    kind: subgraph
    subgraph_ref: "circular_a"
  }
  
  node End {
    kind: agent
    prompt: "Ending B"
  }
  
  edge Start -> CallA
  edge CallA -> End
}
```

**Test via CLI:**
```bash
tracker validate testdata/circular_a.dip
# Should fail gracefully with error message
```

---

## Optional Enhancements (Not Required)

### 1. Better Error Message (5 minutes)

**Current:**
```
subgraph "B": max nesting depth (32) exceeded - possible circular reference
```

**Enhanced:**
```go
func (h *SubgraphHandler) Execute(...) {
    // ... depth check ...
    if depth >= MaxSubgraphDepth {
        // Build call stack for debugging
        callStack := []string{}
        // Extract from internal context if stored
        
        return Outcome{Status: OutcomeFail}, fmt.Errorf(
            "subgraph %q: max nesting depth (%d) exceeded\n"+
            "This usually indicates a circular reference.\n"+
            "Call stack: %s",
            ref, MaxSubgraphDepth, strings.Join(callStack, " -> "),
        )
    }
}
```

---

### 2. Configurable Max Depth (5 minutes)

**Allow users to override:**
```go
// In graph attrs
graph.Attrs["max_subgraph_depth"] = "16"

// In handler
maxDepth := MaxSubgraphDepth
if custom := g.Attrs["max_subgraph_depth"]; custom != "" {
    if d, err := strconv.Atoi(custom); err == nil && d > 0 {
        maxDepth = d
    }
}
```

---

### 3. Documentation (15 minutes)

**Add to README.md:**
```markdown
## Subgraph Nesting Limits

Tracker enforces a maximum subgraph nesting depth of 32 levels to prevent
infinite recursion from circular references.

**Example of circular reference:**
```dip
// workflow_a.dip
node CallB { kind: subgraph; subgraph_ref: "workflow_b"; }

// workflow_b.dip  
node CallA { kind: subgraph; subgraph_ref: "workflow_a"; }
```

**Error message:**
```
subgraph "workflow_b": max nesting depth (32) exceeded - possible circular reference
```

**To override the limit (use with caution):**
```dip
workflow my_deep_graph {
  max_subgraph_depth: 64  # Increase limit
  ...
}
```
```

---

## Commit Message

```
fix(pipeline): add circular subgraph reference protection

Prevents stack overflow crashes from circular subgraph references
by enforcing a maximum nesting depth of 32 levels.

Changes:
- Add MaxSubgraphDepth constant (32 levels)
- Track depth via internal context key "_subgraph_depth"
- Return error when max depth exceeded
- Add test case for circular references

Fixes: #<issue_number>
Closes: dippin-lang parity gap analysis Task 2
```

---

## Verification Checklist

Before marking complete:

- [ ] `MaxSubgraphDepth` constant defined (32)
- [ ] `InternalKeySubgraphDepth` constant defined
- [ ] Depth check added to `SubgraphHandler.Execute()`
- [ ] Depth incremented for child context
- [ ] Error message includes "max nesting depth"
- [ ] Test case `TestSubgraphHandler_CircularReference` added
- [ ] Test case passes
- [ ] Full test suite passes (`go test ./...`)
- [ ] Example files `circular_a.dip` and `circular_b.dip` created
- [ ] CLI validation tested with circular reference
- [ ] Code reviewed by team member
- [ ] Documentation updated (optional)
- [ ] Commit message follows convention

---

## Timeline

| Task | Time | Cumulative |
|------|------|------------|
| Add depth tracking code | 45 min | 45 min |
| Write test case | 30 min | 1h 15min |
| Run tests and verify | 15 min | 1h 30min |
| **Total** | **1.5 hours** | |

**Start:** After critique review approval  
**Complete:** Same day  
**Deploy:** Immediately after verification

---

## Success Criteria

1. ✅ Test case passes
2. ✅ Full test suite passes (no regressions)
3. ✅ Coverage maintained or improved
4. ✅ CLI validation handles circular refs gracefully
5. ✅ Error message is clear and actionable
6. ✅ Code review approval
7. ✅ Documentation updated

---

## Post-Implementation

After completing the fix:

1. **Update status:**
   - Feature completeness: 100% ✅
   - Production readiness: 100% ✅
   - All dippin-lang gaps: CLOSED ✅

2. **Deploy to production:**
   - Run full regression tests
   - Monitor for circular ref errors in logs
   - Update CHANGELOG

3. **Close tickets:**
   - Dippin-lang parity analysis
   - Circular reference protection
   - Any related issues

---

## Contact

**Questions?** See ANALYSIS_INDEX.md for document navigation  
**Issues?** Check CRITIQUE_OF_CLAUDE_REVIEW.md for detailed evidence  
**Need approval?** See CORRECTED_EXECUTIVE_SUMMARY.md for stakeholder brief

---

**Priority:** HIGH  
**Blocking:** Production deployment  
**Effort:** 1.5 hours  
**Impact:** Critical (prevents crashes)  

**Status:** Ready to implement  
**Assigned:** Development team  
**Due:** ASAP
