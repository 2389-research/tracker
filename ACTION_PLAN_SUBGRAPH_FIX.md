# ACTION PLAN: Complete Dippin Feature Parity

**Date:** 2026-03-21  
**Prepared by:** Analysis Agent  
**Status:** Ready for Execution

---

## TL;DR

✅ **Variable interpolation is done and merged.**  
❌ **Subgraph handler is implemented but not wired up.**  
⏳ **Fix requires 6-8 hours to connect existing pieces.**

---

## Current Status

### What Works ✅
- All node types: agent, human, tool, parallel, fan_in
- Variable interpolation: `${ctx.*}`, `${params.*}`, `${graph.*}`
- Conditional routing, retry policies, LLM features
- .dip file parsing, validation, linting

### What's Broken ❌
- **Subgraph nodes fail at runtime:**
  ```
  error: no handler registered for "subgraph" (node "MyNode")
  ```

### Why It's Broken
The `SubgraphHandler` exists at `pipeline/subgraph.go` but is never registered in `handlers.NewDefaultRegistry()`.

---

## Action Items

### Option 1: Quick Fix (Minimal Effort) 🏃

**Time:** 2 hours  
**Approach:** Register handler with empty subgraph map (breaks subgraph params)

**Steps:**
1. Edit `pipeline/handlers/registry.go`
2. Add after other registrations:
   ```go
   registry.Register(pipeline.NewSubgraphHandler(
       make(map[string]*pipeline.Graph),
       registry,
   ))
   ```
3. Test: `go test ./...`
4. Commit

**Pros:** Fast, unblocks basic subgraph usage  
**Cons:** Subgraph refs won't load automatically, params won't work

---

### Option 2: Full Solution (Recommended) 🎯

**Time:** 6-8 hours  
**Approach:** Auto-discover and load subgraphs with recursive loader

**Detailed plan:** See `docs/plans/IMPLEMENT_SUBGRAPH_HANDLER_REGISTRATION.md`

**Steps:**

#### Phase 1: Implement Loader (2 hours)
1. Create `cmd/tracker/subgraph_loader.go`
   - Copy implementation from plan document
   - Implements: `loadPipelineWithSubgraphs()`, cycle detection, path resolution

2. Add `WithSubgraphs()` option to `pipeline/handlers/registry.go`
   ```go
   func WithSubgraphs(graphs map[string]*Graph) RegistryOption {
       return func(c *registryConfig) {
           c.subgraphs = graphs
       }
   }
   ```

3. Register handler in `NewDefaultRegistry()`:
   ```go
   if len(cfg.subgraphs) > 0 {
       registry.Register(pipeline.NewSubgraphHandler(cfg.subgraphs, registry))
   }
   ```

#### Phase 2: Wire Into Main (1 hour)
1. Edit `cmd/tracker/main.go`
2. In `run()` function:
   ```go
   // OLD:
   // graph, err := loadPipeline(pipelineFile, format)
   
   // NEW:
   graph, subgraphs, err := loadPipelineWithSubgraphs(pipelineFile, format)
   ```
3. Add to registry options:
   ```go
   handlers.WithSubgraphs(subgraphs),
   ```
4. Repeat for `runTUI()`, `runValidateCmd()`, `runSimulateCmd()`

#### Phase 3: Test (2 hours)
1. Create `cmd/tracker/subgraph_loader_test.go`
   - Copy tests from plan document
   - Test: simple parent→child, cycle detection, nested subgraphs, missing refs

2. Create `cmd/tracker/subgraph_integration_test.go`
   - E2E test with real LLM (skip if no API key)

3. Run full test suite: `go test ./...`

#### Phase 4: Document (1 hour)
1. Update README.md with subgraph example
2. Create `examples/subgraph_demo.dip`
3. Create `examples/subgraphs/scanner.dip`
4. Update feature parity index

#### Phase 5: Verify (1 hour)
1. Test examples:
   ```bash
   tracker examples/subgraph_demo.dip
   tracker examples/variable_interpolation_demo.dip
   ```
2. Verify error messages for missing refs, cycles
3. Commit: `feat(subgraph): wire SubgraphHandler with auto-discovery`

---

## Decision Matrix

| Criteria | Option 1 (Quick) | Option 2 (Full) |
|----------|------------------|-----------------|
| **Time** | 2 hours | 6-8 hours |
| **Completeness** | Partial | Complete |
| **Subgraph refs work** | ❌ No | ✅ Yes |
| **Params work** | ❌ No | ✅ Yes |
| **Nested subgraphs** | ❌ No | ✅ Yes |
| **Error messages** | ⚠️ Confusing | ✅ Clear |
| **Production ready** | ⚠️ No | ✅ Yes |

**Recommendation:** **Option 2** for production quality.

---

## Test Before Merge

### Manual Smoke Test
```bash
# Create child workflow
cat > /tmp/child.dip <<'EOF'
workflow Child
  start: Work
  exit: Work
  agent Work
    prompt: Execute task: ${params.task}
    auto_status: true
EOF

# Create parent workflow
cat > /tmp/parent.dip <<'EOF'
workflow Parent
  start: Start
  exit: End
  
  agent Start
    label: Start
  
  subgraph CallChild
    ref: /tmp/child.dip
    params:
      task: test subgraph execution
  
  agent End
    label: End
  
  edges
    Start -> CallChild
    CallChild -> End
EOF

# Test execution
tracker /tmp/parent.dip --no-tui
```

**Expected output:**
```
[19:43:01] pipeline_started
[19:43:01] stage_started    node=Start
[19:43:01] stage_completed  node=Start
[19:43:01] stage_started    node=CallChild        # ← SUBGRAPH EXECUTES!
[19:43:02] llm start anthropic/claude-sonnet-4-5
[19:43:03] llm text preview="Execute task: test subgraph execution..."
[19:43:03] stage_completed  node=CallChild
[19:43:03] stage_started    node=End
[19:43:03] stage_completed  node=End
[19:43:03] pipeline_complete status=success
```

### Automated Tests
```bash
# All tests must pass
go test ./... -v

# No race conditions
go test ./... -race

# Coverage should be high
go test ./pipeline -cover
go test ./pipeline/handlers -cover
```

---

## Success Criteria

- [ ] `tracker parent.dip` executes subgraph nodes without "no handler" error
- [ ] Parent can pass params to child via `params:` attribute
- [ ] Child's `${params.*}` variables expand correctly
- [ ] Child can update context, changes propagate to parent
- [ ] Nested subgraphs work (grandparent→parent→child)
- [ ] Circular refs are detected and rejected with clear error
- [ ] Missing subgraph refs show helpful error with resolved path
- [ ] All existing tests still pass
- [ ] New unit tests pass (4+ tests)
- [ ] Integration test passes (or skips if no API key)
- [ ] README has subgraph example
- [ ] `examples/subgraph_demo.dip` works end-to-end

---

## Files to Create/Modify

### New Files
```
cmd/tracker/subgraph_loader.go              (180 lines)
cmd/tracker/subgraph_loader_test.go         (150 lines)
cmd/tracker/subgraph_integration_test.go    (80 lines)
examples/subgraph_demo.dip                  (30 lines)
examples/subgraphs/scanner.dip              (20 lines)
```

### Modified Files
```
pipeline/handlers/registry.go               (+15 lines)
cmd/tracker/main.go                         (+10 lines, 4 functions)
README.md                                   (+40 lines)
```

**Total:** ~515 new lines of code

---

## Risk Assessment

| Risk | Likelihood | Impact | Mitigation |
|------|------------|--------|------------|
| Circular refs cause stack overflow | Medium | High | Detect cycles in loader (already in plan) |
| Path resolution bugs | Medium | Medium | Comprehensive test coverage |
| Breaks existing workflows | Low | High | Feature is additive, no breaking changes |
| Performance on large graphs | Low | Low | Cache loaded graphs |

---

## Timeline

**Option 1 (Quick Fix):**
- Today: 2 hours → Partial functionality

**Option 2 (Full Solution):**
- Day 1 (4 hours): Implement loader + wire into main
- Day 2 (4 hours): Tests + docs + verification
- Total: 8 hours → Production-ready

---

## Next Steps for Human

1. **Choose option:** Quick fix or full solution?
   - Recommendation: **Full solution (Option 2)**

2. **Create branch:**
   ```bash
   git checkout -b feat/wire-subgraph-handler
   ```

3. **Follow implementation plan:**
   - Reference: `docs/plans/IMPLEMENT_SUBGRAPH_HANDLER_REGISTRATION.md`
   - Or use code snippets from this action plan

4. **Test continuously:**
   ```bash
   go test ./... -v  # After each phase
   ```

5. **Commit when done:**
   ```bash
   git add .
   git commit -m "feat(subgraph): wire SubgraphHandler with auto-discovery"
   git push origin feat/wire-subgraph-handler
   ```

6. **Verify examples work:**
   ```bash
   tracker examples/subgraph_demo.dip
   tracker examples/variable_interpolation_demo.dip
   ```

---

## Questions?

**Q: Why wasn't this caught earlier?**  
A: The handler was implemented and tested in isolation, but the registry integration step was missed. Tests pass because they construct the handler directly, bypassing the registry.

**Q: Will this break anything?**  
A: No. The feature is purely additive. Existing workflows without `subgraph` nodes are unaffected.

**Q: Can we ship without this?**  
A: Yes, but users can't use subgraphs. The variable interpolation examples (`variable_interpolation_demo.dip`) won't work fully.

**Q: How urgent is this?**  
A: **P0** if users are blocked on subgraph functionality. **P1** if it's just internal examples.

---

## Recommendation

**Ship the variable interpolation commit now.** It's complete, tested, and valuable.

**Schedule the subgraph fix for this week.** It's well-understood, straightforward, and completes 100% dippin parity.

---

**Prepared by:** Analysis Agent  
**Reviewed:** All implementation files, tests, and documentation  
**Status:** Ready for execution
