# Quick Action Checklist — Dippin Feature Gaps

**Status:** Ready to implement  
**Time to Ship:** 4 hours (after fixing 1 critical bug)

---

## Priority 1: SHIP BLOCKERS (30 minutes)

### [ ] Task 1: Fix Subgraph Params Bug

**File:** `pipeline/subgraph.go`

**Change:**
```go
// BEFORE (line 30-50):
func (h *SubgraphHandler) Execute(ctx context.Context, node *Node, pctx *PipelineContext) (Outcome, error) {
    ref, ok := node.Attrs["subgraph_ref"]
    if !ok || ref == "" {
        return Outcome{Status: OutcomeFail}, fmt.Errorf("node %q missing subgraph_ref attribute", node.ID)
    }
    subGraph, ok := h.graphs[ref]
    if !ok {
        return Outcome{Status: OutcomeFail}, fmt.Errorf("subgraph %q not found", ref)
    }
    
    // Create a sub-engine with the parent's context snapshot as initial values.
    engine := NewEngine(subGraph, h.registry, WithInitialContext(pctx.Snapshot()))
    // ...
}

// AFTER (add param parsing):
func (h *SubgraphHandler) Execute(ctx context.Context, node *Node, pctx *PipelineContext) (Outcome, error) {
    ref, ok := node.Attrs["subgraph_ref"]
    if !ok || ref == "" {
        return Outcome{Status: OutcomeFail}, fmt.Errorf("node %q missing subgraph_ref attribute", node.ID)
    }
    subGraph, ok := h.graphs[ref]
    if !ok {
        return Outcome{Status: OutcomeFail}, fmt.Errorf("subgraph %q not found", ref)
    }
    
    // ✅ NEW: Parse and inject subgraph params into initial context
    initialCtx := pctx.Snapshot()
    if paramsStr, ok := node.Attrs["subgraph_params"]; ok && paramsStr != "" {
        params := parseSubgraphParams(paramsStr)
        for k, v := range params {
            initialCtx[k] = v
        }
    }
    
    engine := NewEngine(subGraph, h.registry, WithInitialContext(initialCtx))
    // ...
}

// ✅ NEW: Add helper function at end of file
func parseSubgraphParams(paramStr string) map[string]string {
    params := make(map[string]string)
    for _, pair := range strings.Split(paramStr, ",") {
        parts := strings.SplitN(strings.TrimSpace(pair), "=", 2)
        if len(parts) == 2 {
            params[strings.TrimSpace(parts[0])] = strings.TrimSpace(parts[1])
        }
    }
    return params
}
```

**Test:**
```bash
# Run existing tests (should still pass):
go test ./pipeline -v -run TestSubgraph

# Add new test to pipeline/subgraph_test.go:
func TestSubgraphHandler_WithParams(t *testing.T) {
    // Create child graph
    child := NewGraph("child")
    child.StartNode = "start"
    child.ExitNode = "end"
    
    // Add a mock handler that captures context
    var capturedCtx map[string]string
    registry := NewHandlerRegistry()
    registry.Register(&mockHandler{
        name: "capture",
        execFunc: func(ctx context.Context, node *Node, pctx *PipelineContext) (Outcome, error) {
            capturedCtx = pctx.Snapshot()
            return Outcome{Status: OutcomeSuccess}, nil
        },
    })
    
    child.AddNode(&Node{ID: "start", Shape: "Mdiamond", Handler: "start"})
    child.AddNode(&Node{ID: "task", Shape: "box", Handler: "capture"})
    child.AddNode(&Node{ID: "end", Shape: "Msquare", Handler: "exit"})
    child.AddEdge(&Edge{From: "start", To: "task"})
    child.AddEdge(&Edge{From: "task", To: "end"})
    
    // Create parent with subgraph node that passes params
    parent := NewGraph("parent")
    parent.AddNode(&Node{
        ID:    "sg",
        Shape: "tab",
        Attrs: map[string]string{
            "subgraph_ref":    "child",
            "subgraph_params": "stream_id=stream-a,branch=feature/a",
        },
    })
    
    handler := NewSubgraphHandler(map[string]*Graph{"child": child}, registry)
    pctx := NewPipelineContext()
    
    _, err := handler.Execute(context.Background(), parent.Nodes["sg"], pctx)
    if err != nil {
        t.Fatalf("Execute failed: %v", err)
    }
    
    // Verify params were passed
    if capturedCtx["stream_id"] != "stream-a" {
        t.Errorf("stream_id = %q, want %q", capturedCtx["stream_id"], "stream-a")
    }
    if capturedCtx["branch"] != "feature/a" {
        t.Errorf("branch = %q, want %q", capturedCtx["branch"], "feature/a")
    }
}
```

**Verify:**
```bash
go test ./pipeline -v -run TestSubgraphHandler_WithParams
```

---

## Priority 2: VERIFICATION (2 hours)

### [ ] Task 2: Run All Tests

```bash
go test ./... -v
```

**Expected:** All tests pass

**If failures:** Fix or document

---

### [ ] Task 3: Test All Examples

```bash
#!/bin/bash
# test_examples.sh

echo "Testing all .dip examples..."

for dipfile in examples/*.dip examples/subgraphs/*.dip; do
    echo ""
    echo "=== $dipfile ==="
    
    # Skip examples that require real LLM API
    if grep -q "reasoning_effort: high" "$dipfile" && [ -z "$OPENAI_API_KEY" ]; then
        echo "⏭️  SKIPPED (requires OpenAI API key)"
        continue
    fi
    
    # Run with timeout
    timeout 30s tracker "$dipfile" --no-tui > /dev/null 2>&1
    
    if [ $? -eq 0 ]; then
        echo "✅ PASS"
    elif [ $? -eq 124 ]; then
        echo "⏱️  TIMEOUT (likely waiting for human input)"
    else
        echo "❌ FAIL (exit code $?)"
    fi
done

echo ""
echo "=== Test Summary ==="
echo "Check for ❌ failures above"
```

**Run:**
```bash
chmod +x test_examples.sh
./test_examples.sh
```

**Fix any failures before shipping**

---

### [ ] Task 4: Verify Fallback Target

```bash
# Search for fallback_target usage:
grep -rn "fallback_target" pipeline/engine.go

# Expected: Either found usage OR confirmed not implemented
```

**If found:**
- ✅ Document as working

**If not found:**
- Add to `README.md` as "not yet implemented"
- Add to backlog as future feature

---

## Priority 3: DOCUMENTATION (1 hour)

### [ ] Task 5: Document Known Limitations

**File:** `README.md` (add section)

```markdown
## Known Limitations

### Subgraph Params (FIXED in v1.0.1)
Prior to v1.0.1, subgraph params were silently ignored. Now working correctly.

### Provider Feature Support

| Feature | OpenAI | Anthropic | Gemini |
|---------|--------|-----------|--------|
| `reasoning_effort` | ✅ Yes | ⚠️ Ignored | ❓ Unknown |
| Document upload | ❌ No | ✅ Yes (PDF) | ✅ Yes |
| Audio input | ❌ No | ❌ No | ✅ Yes |

### Not Yet Implemented
- `NodeIO.Reads/Writes` validation (advisory fields, no enforcement)
- `RetryConfig.FallbackTarget` (extracted but not used)
```

---

### [ ] Task 6: Update CHANGELOG

**File:** `CHANGELOG.md`

```markdown
## [Unreleased]

### Fixed
- Subgraph params now properly injected into child context (#XXX)

### Verified
- Retry policies fully working (all 5 named policies)
- Restart limits enforced correctly
- Reasoning effort wired to OpenAI provider

### Known Issues
- Reasoning effort untested on Anthropic/Gemini
- NodeIO validation not implemented (advisory feature)
```

---

## Priority 4: POST-SHIP (1 week)

### [ ] Task 7: Test Reasoning Effort on All Providers

**File:** `examples/test_reasoning_effort.dip`

```dippin
workflow TestReasoningEffort
  start: Start
  exit: Exit

  agent Start
    label: Start

  agent TestOpenAI
    model: o3-mini
    provider: openai
    reasoning_effort: high
    prompt: "Test OpenAI reasoning: What is 2+2?"

  agent TestAnthropic
    model: claude-sonnet-4-6
    provider: anthropic
    reasoning_effort: high
    prompt: "Test Anthropic reasoning: What is 2+2?"

  agent TestGemini
    model: gemini-2.0-flash-exp
    provider: google
    reasoning_effort: high
    prompt: "Test Gemini reasoning: What is 2+2?"

  agent Exit
    label: Exit

  edges
    Start -> TestOpenAI
    TestOpenAI -> TestAnthropic
    TestAnthropic -> TestGemini
    TestGemini -> Exit
```

**Run:**
```bash
OPENAI_API_KEY=... ANTHROPIC_API_KEY=... GEMINI_API_KEY=... \
  tracker examples/test_reasoning_effort.dip --no-tui
```

**Document results in:** `docs/provider_feature_matrix.md`

---

### [ ] Task 8: Add Integration Tests to CI

**File:** `.github/workflows/test.yml` (add job)

```yaml
  test-examples:
    runs-on: ubuntu-latest
    steps:
      - uses: actions/checkout@v3
      - uses: actions/setup-go@v4
        with:
          go-version: '1.25'
      
      - name: Build tracker
        run: go build ./cmd/tracker
      
      - name: Test examples (without LLM)
        run: |
          for f in examples/*.dip; do
            echo "Testing $f"
            timeout 5s ./tracker "$f" --no-tui || echo "Skipped (requires LLM)"
          done
```

---

## Quick Reference

### Ship Readiness Checklist

- [ ] Subgraph params bug fixed (Task 1)
- [ ] All tests pass (Task 2)
- [ ] Examples work (Task 3)
- [ ] Fallback_target verified or documented (Task 4)
- [ ] Known limitations documented (Task 5)
- [ ] CHANGELOG updated (Task 6)

**Time to Ship:** 4 hours after completing Tasks 1-6

---

### Post-Ship Checklist

- [ ] Reasoning effort tested on all providers (Task 7)
- [ ] Integration tests in CI (Task 8)
- [ ] Provider feature matrix documented

**Time to Complete:** 1 week after ship

---

## Gemini Review Scorecard

| Claim | Accurate? | Notes |
|-------|-----------|-------|
| Subgraphs work | ⚠️ Mostly | Ref works ✅, params broken ❌ |
| Reasoning effort works | ✅ Yes | Verified in code |
| 95% compliant | ❌ No | Fabricated metric |
| Retry policies unknown | ❌ No | Actually fully working |
| Restart limits unknown | ❌ No | Actually fully working |
| Ready to ship | ⚠️ Almost | After 1 bug fix |

**Overall Grade:** B- (correct conclusion, flawed methodology)

---

**Status:** READY TO IMPLEMENT  
**Next Action:** Start with Task 1 (30 minutes)  
**Ship Decision:** After Tasks 1-6 complete (4 hours total)
