# Dippin Feature Parity — Implementation Roadmap

**Last Updated:** 2026-03-21  
**Status:** Ready for Implementation  
**Total Estimated Time:** 13-15 hours

---

## Quick Reference

| Phase | Tasks | Time | Priority |
|-------|-------|------|----------|
| **Phase 1** | Reasoning effort + 3 lint rules + CLI | 4 hours | **HIGH** |
| **Phase 2** | 5 medium-priority lint rules | 5 hours | MEDIUM |
| **Phase 3** | 4 complex lint rules + polish | 5 hours | LOW |

**Current Status:** 92% complete → **Target:** 100% complete

---

## Phase 1: Quick Wins (4 hours) 🚀

### ✅ Task 1.1: Wire Reasoning Effort
- **Time:** 1 hour
- **Files:** `pipeline/handlers/codergen.go`, `codergen_test.go`
- **Priority:** HIGH — User-visible feature
- **Test:** Create `.dip` with `reasoning_effort: high`, verify OpenAI request

**Implementation:**
```go
// In buildConfig() after command_timeout block:
if re, ok := node.Attrs["reasoning_effort"]; ok && re != "" {
    config.ReasoningEffort = re
}
```

**Acceptance:**
- [ ] Unit test passes
- [ ] Integration test with real OpenAI API call
- [ ] No regressions in existing tests

---

### ✅ Task 1.2: Implement DIP110 (Empty Prompt Warning)
- **Time:** 30 minutes
- **Files:** `pipeline/lint_dippin.go` (new), `lint_dippin_test.go` (new)
- **Priority:** HIGH — Catches common authoring error

**What it checks:**
```
warning[DIP110]: empty prompt on agent node "TaskA"
```

**Acceptance:**
- [ ] Warns when `agent` node has no `prompt` attribute
- [ ] Warns when `prompt` attribute is empty string
- [ ] No warning when prompt is present
- [ ] Test coverage ≥90%

---

### ✅ Task 1.3: Implement DIP111 (Tool Without Timeout)
- **Time:** 30 minutes
- **Files:** `pipeline/lint_dippin.go`, `lint_dippin_test.go`
- **Priority:** HIGH — Prevents hanging commands

**What it checks:**
```
warning[DIP111]: tool node "RunTests" has no timeout
```

**Acceptance:**
- [ ] Warns when `tool` node has `tool_command` but no `timeout`
- [ ] No warning when timeout is present
- [ ] Test coverage ≥90%

---

### ✅ Task 1.4: Implement DIP102 (No Default Edge)
- **Time:** 45 minutes
- **Files:** `pipeline/lint_dippin.go`, `lint_dippin_test.go`
- **Priority:** HIGH — Prevents routing dead-ends

**What it checks:**
```
warning[DIP102]: node "Router" has conditional edges but no default/unconditional edge
```

**Acceptance:**
- [ ] Warns when node has only conditional outgoing edges
- [ ] No warning when unconditional fallback exists
- [ ] Handles nodes with no outgoing edges gracefully
- [ ] Test coverage ≥90%

---

### ✅ Task 1.5: CLI Integration
- **Time:** 1 hour
- **Files:** `pipeline/validate_semantic.go`, `cmd/tracker/validate.go`, `cmd/tracker/main.go`
- **Priority:** HIGH — Makes warnings visible to users

**What it adds:**
```bash
tracker validate pipeline.dip
# Output:
# warning[DIP110]: empty prompt on agent node "TaskA"
# validation passed (1 warning)
```

**Acceptance:**
- [ ] `tracker validate [file]` command exists
- [ ] Displays structural errors (blocking, exit 1)
- [ ] Displays semantic warnings (non-blocking, exit 0)
- [ ] Warnings formatted per Dippin spec: `warning[DIPXXX]: message`
- [ ] Help text explains usage

---

## Phase 2: Medium Priority (5 hours) 📊

### ✅ Task 2.1: Implement DIP104 (Unbounded Retry)
- **Time:** 30 minutes
- **Complexity:** Low

**What it checks:**
```
warning[DIP104]: unbounded retry loop (no max_retries or fallback)
```

**Logic:** Warn when node has `retry_target` but no `max_retries` or `fallback_target`.

---

### ✅ Task 2.2: Implement DIP108 (Unknown Model/Provider)
- **Time:** 45 minutes
- **Complexity:** Low-Medium

**What it checks:**
```
warning[DIP108]: unknown model/provider combination "gpt-7" / "openai"
```

**Logic:** Check model/provider against known-good lists. Graceful for new models.

---

### ✅ Task 2.3: Implement DIP101 (Unreachable Via Conditional)
- **Time:** 1 hour
- **Complexity:** Medium

**What it checks:**
```
warning[DIP101]: node "Fallback" only reachable via conditional edges
```

**Logic:** BFS from start, mark nodes reachable via unconditional paths. Warn if node only reachable via conditionals.

---

### ✅ Task 2.4: Implement DIP107 (Unused Context Write)
- **Time:** 1 hour
- **Complexity:** Medium

**What it checks:**
```
warning[DIP107]: unused context key "summary" (written but never read)
```

**Logic:** 
1. Collect all `writes` from nodes
2. Collect all `reads` from downstream nodes
3. Warn for keys in writes but not in reads

---

### ✅ Task 2.5: Implement DIP112 (Reads Not Produced)
- **Time:** 1 hour
- **Complexity:** Medium

**What it checks:**
```
warning[DIP112]: reads key "plan" not produced by any upstream writes
```

**Logic:**
1. For each node with `reads`, check all upstream nodes
2. Warn if no upstream node has matching `writes`

---

## Phase 3: Polish & Complex Rules (5 hours) 🎨

### ✅ Task 3.1: Implement DIP105 (No Success Path)
- **Time:** 1.5 hours
- **Complexity:** Medium-High

**What it checks:**
```
warning[DIP105]: no guaranteed success path from start to exit
```

**Logic:** BFS from start to exit using only unconditional edges. Warn if no path exists.

---

### ✅ Task 3.2: Implement DIP106 (Undefined Variable in Prompt)
- **Time:** 1.5 hours
- **Complexity:** Medium-High

**What it checks:**
```
warning[DIP106]: undefined variable reference "${ctx.unknown}" in prompt
```

**Logic:**
1. Parse prompt for `${ctx.X}`, `${params.Y}`, `${graph.Z}` references
2. Check if X exists in upstream writes or reserved keys
3. Warn for undefined refs

---

### ✅ Task 3.3: Implement DIP103 (Overlapping Conditions)
- **Time:** 2 hours
- **Complexity:** High

**What it checks:**
```
warning[DIP103]: overlapping conditions on edges from "Router"
```

**Logic:**
1. Group edges by source node
2. Parse conditions, detect duplicates/overlaps
3. Warn for identical comparisons

---

### ✅ Task 3.4: Implement DIP109 (Namespace Collision)
- **Time:** 1 hour
- **Complexity:** Medium

**What it checks:**
```
warning[DIP109]: namespace collision in subgraph params (key "model" shadows context)
```

**Logic:** Check subgraph `params` keys against existing context keys.

---

## Progress Tracker

### Current Status (as of 2026-03-21)

**Completed:**
- ✅ Analysis documents written
- ✅ Implementation plan created
- ✅ Task specification finalized

**In Progress:**
- ⏸️ Awaiting implementation start

**Blocked:**
- None

---

## Daily Goals

### Week 1 (Phase 1)
- **Day 1:** Task 1.1 (reasoning_effort) + Task 1.2 (DIP110)
- **Day 2:** Task 1.3 (DIP111) + Task 1.4 (DIP102)
- **Day 3:** Task 1.5 (CLI integration)

**Milestone:** Users can run `tracker validate` and get warnings.

### Week 2 (Phase 2)
- **Day 1:** Task 2.1 (DIP104) + Task 2.2 (DIP108)
- **Day 2:** Task 2.3 (DIP101)
- **Day 3:** Task 2.4 (DIP107) + Task 2.5 (DIP112)

**Milestone:** Most common design issues covered.

### Week 3 (Phase 3)
- **Day 1:** Task 3.1 (DIP105)
- **Day 2:** Task 3.2 (DIP106)
- **Day 3:** Task 3.3 (DIP103)
- **Day 4:** Task 3.4 (DIP109) + final polish

**Milestone:** 100% Dippin parity achieved.

---

## Testing Checklist

After each task:
- [ ] Unit tests pass (`go test ./...`)
- [ ] Test coverage ≥90% for new code
- [ ] No regressions in existing tests
- [ ] Manual smoke test with example file
- [ ] Git commit with descriptive message

After each phase:
- [ ] Integration test with full pipeline
- [ ] Test against all examples/ files
- [ ] Performance check (lint overhead <100ms)
- [ ] Documentation updated

After all phases:
- [ ] All 21 validation rules implemented (9 errors + 12 warnings)
- [ ] CLI help text complete
- [ ] README updated
- [ ] Examples directory has `.dip` files
- [ ] No open bugs

---

## Success Metrics

### Phase 1 Success
- [ ] `tracker validate` command works
- [ ] 4 lint rules implemented (DIP110, DIP111, DIP102, + reasoning_effort)
- [ ] Warnings display correctly
- [ ] Exit codes correct (0 for warnings, 1 for errors)

### Phase 2 Success
- [ ] 9 lint rules implemented
- [ ] Coverage of common design issues
- [ ] Performance acceptable (<100ms overhead)

### Phase 3 Success
- [ ] All 12 lint rules implemented
- [ ] 100% IR field utilization (13/13)
- [ ] 100% validation rule coverage (21/21)
- [ ] Documentation complete

### Full Parity Success
- [ ] Tracker is reference Dippin executor
- [ ] All tests pass
- [ ] No false positives in examples/
- [ ] User feedback positive

---

## Rollback Plan

If implementation causes issues:

1. **Individual task rollback:**
   ```bash
   git revert <commit-hash>
   ```

2. **Phase rollback:**
   ```bash
   git revert <first-commit>..<last-commit>
   ```

3. **Full rollback:**
   ```bash
   git checkout main
   git reset --hard <pre-implementation-commit>
   ```

**Mitigation:** Each task is atomic and independently committable. Rollback is granular.

---

## Documentation Checklist

### Code Documentation
- [ ] All new functions have ABOUTME comments
- [ ] Lint rules documented with examples
- [ ] Edge cases documented

### User Documentation
- [ ] README updated with `tracker validate` usage
- [ ] Lint rules reference added (or linked)
- [ ] Provider compatibility documented (reasoning_effort)

### Developer Documentation
- [ ] Architecture decision records (if needed)
- [ ] Test patterns documented
- [ ] Contribution guide updated (if needed)

---

## Dependencies & Prerequisites

### Required Tools
- Go 1.25+
- Make (optional, for Makefile commands)
- Git

### Required Access
- OpenAI API key (for reasoning_effort integration test)
- Write access to repository
- CI/CD runner (for automated testing)

### Required Knowledge
- Dippin language spec (in dippin-lang module docs)
- Tracker architecture (layers 1-3)
- Go testing patterns

---

## Communication Plan

### Progress Updates
- Daily: Update task checkboxes in this document
- Weekly: Summary in team channel/standup
- Blockers: Immediate notification

### Code Review
- Each task: Self-review before commit
- Each phase: Request peer review
- Final: Comprehensive review before merge

### User Communication
- Phase 1 complete: Announce validate command
- All phases complete: Release notes with full feature list
- Documentation: Update changelog

---

## Risk Mitigation

### False Positives
- **Mitigation:** Conservative defaults, test against examples/
- **Fallback:** Add `--strict` flag for extra checks (future)

### Performance Impact
- **Mitigation:** Make lint opt-in, measure overhead
- **Fallback:** Cache lint results, lazy evaluation

### Breaking Changes
- **Mitigation:** Warnings only, no blocking
- **Fallback:** Add `--no-lint` flag to disable

---

## Post-Implementation

After achieving 100% parity:

### Immediate
1. Update README badges (if applicable)
2. Blog post / announcement
3. Tag release (v1.x.0 or similar)

### Short Term (1 month)
4. Gather user feedback
5. Fix false positives
6. Add suppressions (if needed)

### Long Term (3-6 months)
7. LSP integration (real-time lint in editors)
8. Autofix suggestions
9. Custom lint rules framework

---

## Conclusion

**Current state:** 92% complete (12/13 IR fields, 12/21 validation rules)  
**Target state:** 100% complete (13/13 IR fields, 21/21 validation rules)  
**Total work:** 13-15 hours across 3 phases  
**Approach:** Incremental, test-first, one task at a time

**Start here:** Task 1.1 (reasoning_effort wiring) — 1 hour quick win

For detailed implementation instructions, see:
- **Analysis:** `2026-03-21-dippin-feature-parity-analysis.md`
- **Plan:** `2026-03-21-dippin-feature-parity-plan.md`
- **Spec:** `2026-03-21-dippin-feature-parity-spec.md`
- **Summary:** `2026-03-21-dippin-feature-parity-summary.md`
