# Dippin Language Feature Parity Analysis

**Date:** 2026-03-21  
**Status:** Analysis Complete  
**Goal:** Identify missing Dippin language features in Tracker and create implementation plan

---

## Executive Summary

Tracker has basic Dippin `.dip` file support via the `dippin_adapter.go` bridge, which converts Dippin IR to Tracker's internal Graph representation. However, **several Dippin language features are not yet fully utilized** at the execution layer, even though the adapter already extracts them from the IR.

### Completion Status

| Feature Category | Status | Notes |
|------------------|--------|-------|
| **Core Execution** | ✅ 95% Complete | All major features implemented |
| **Advanced Agent Features** | ⚠️  60% Complete | Missing reasoning_effort runtime support |
| **Context Management** | ✅ 90% Complete | Compaction implemented, fidelity working |
| **Semantic Validation** | ⚠️  40% Complete | Basic validation exists, missing 12 Dippin lint rules |
| **Subgraph Composition** | ✅ 100% Complete | Full subgraph support exists |
| **Mid-Session Features** | ✅ 100% Complete | Steering and spawn_agent implemented |

---

## Feature-by-Feature Analysis

### ✅ ALREADY IMPLEMENTED

These features are **fully supported** by Tracker:

#### 1. Subgraph Support ✅
- **File:** `pipeline/subgraph.go`
- **Status:** Complete
- **What works:**
  - SubgraphHandler executes referenced sub-pipelines
  - Context merging from child to parent
  - `subgraph_ref` attribute extraction in dippin_adapter
  - Parameter passing via `subgraph_params`
- **Gap:** None

#### 2. Spawn Agent Tool ✅
- **File:** `agent/tools/spawn.go`
- **Status:** Complete
- **What works:**
  - Child agent sessions via SessionRunner interface
  - Task delegation with isolated contexts
  - System prompt and max_turns configuration
- **Gap:** None

#### 3. Mid-Session Steering ✅
- **File:** `agent/steering.go`
- **Status:** Complete
- **What works:**
  - Channel-based steering message injection
  - Non-blocking check between agent turns
  - EventSteeringInjected event emission
- **Gap:** None

#### 4. Message Transform Middleware ✅
- **File:** `llm/transform.go`
- **Status:** Complete
- **What works:**
  - Request transform before LLM call
  - Response transform after LLM call
  - Chainable middleware pattern
- **Gap:** None

#### 5. Auto Status Parsing ✅
- **File:** `pipeline/handlers/codergen.go` (lines 141-143, 236-254)
- **Status:** Complete
- **What works:**
  - `auto_status` attribute extraction in dippin_adapter
  - `parseAutoStatus()` scans for `STATUS: success|fail|retry`
  - Automatic outcome mapping for conditional routing
- **Gap:** None

#### 6. Goal Gate ✅
- **File:** `pipeline/engine.go` (line 318)
- **Status:** Complete
- **What works:**
  - `goal_gate` attribute extraction in dippin_adapter
  - `isGoalGate()` checks node attribute
  - Pipeline fails if goal gate node fails, even if exit reached
- **Gap:** None

#### 7. Context Compaction ✅
- **File:** `agent/compaction.go`, `pipeline/fidelity.go`
- **Status:** Complete
- **What works:**
  - `compaction` and `compaction_threshold` extracted from IR
  - Auto/none modes with configurable threshold
  - Tool result summarization to prevent context overflow
- **Gap:** None

#### 8. Semantic Validation (Partial) ⚠️
- **File:** `pipeline/validate_semantic.go`
- **Status:** 40% Complete
- **What works:**
  - Handler registration validation
  - Condition syntax validation
  - Node attribute type validation (max_retries, cache_tool_results, compaction)
- **Gap:** Missing 12 Dippin lint rules (DIP101-DIP112) — see section below

---

### ⚠️ PARTIALLY IMPLEMENTED

#### 9. Reasoning Effort ⚠️
- **Dippin IR Field:** `AgentConfig.ReasoningEffort`
- **Adapter Status:** ✅ Extracted to `attrs["reasoning_effort"]`
- **Runtime Status:** ⚠️ Partially wired, not fully utilized
- **Current State:**
  - LLM request type has `ReasoningEffort` field (llm/types.go:203)
  - OpenAI provider translates it to API params (llm/openai/translate.go:151-178)
  - **BUT:** Codergen handler doesn't read `reasoning_effort` from node attrs
  - **Result:** Reasoning effort specified in `.dip` is ignored
  
**Gap:** Need to wire `node.Attrs["reasoning_effort"]` into SessionConfig in `codergen.go`

---

### ❌ MISSING FEATURES

#### 10. Dippin Semantic Linting (DIP101-DIP112) ❌

The Dippin language spec defines **12 semantic lint warnings** (DIP101-DIP112) that catch workflow design issues. Tracker has `ValidateSemantic()` but only implements **3** basic checks. Missing rules:

| Code | Rule | Priority | Complexity |
|------|------|----------|------------|
| **DIP101** | Node only reachable via conditional edges | Medium | Low |
| **DIP102** | Routing node missing default edge | High | Low |
| **DIP103** | Overlapping conditions | Medium | Medium |
| **DIP104** | Unbounded retry loop | High | Low |
| **DIP105** | No success path to exit | High | Medium |
| **DIP106** | Undefined variable in prompt | Medium | Medium |
| **DIP107** | Unused context write | Low | Low |
| **DIP108** | Unknown model/provider | Medium | Low |
| **DIP109** | Namespace collision in imports | Low | Medium |
| **DIP110** | Empty prompt on agent | High | Low |
| **DIP111** | Tool without timeout | High | Low |
| **DIP112** | Reads key not produced upstream | Medium | Medium |

**Current Implementation:**
- ✅ Handler registration (subset of DIP108)
- ✅ Condition syntax validation
- ✅ Node attribute type validation

**Gap:** 9 new lint rules needed

---

### 📋 DIPPIN IR FIELDS UTILIZATION MATRIX

This table shows which `ir.AgentConfig` fields are extracted by `dippin_adapter.go` and which are consumed by runtime handlers:

| IR Field | Adapter Extracts? | Runtime Uses? | File |
|----------|-------------------|---------------|------|
| `Prompt` | ✅ Yes | ✅ Yes | codergen.go |
| `SystemPrompt` | ✅ Yes | ✅ Yes | codergen.go |
| `Model` | ✅ Yes (`model`) | ✅ Yes | codergen.go |
| `Provider` | ✅ Yes (`provider`) | ✅ Yes | codergen.go |
| `MaxTurns` | ✅ Yes (`max_turns`) | ✅ Yes | codergen.go |
| `CmdTimeout` | ✅ Yes (`cmd_timeout`) | ✅ Yes | codergen.go |
| `CacheTools` | ✅ Yes (`cache_tools`) | ✅ Yes | codergen.go |
| `Compaction` | ✅ Yes (`compaction`) | ✅ Yes | codergen.go |
| `CompactionThreshold` | ✅ Yes (`compaction_threshold`) | ✅ Yes | codergen.go |
| `ReasoningEffort` | ✅ Yes (`reasoning_effort`) | ❌ **NO** | codergen.go |
| `Fidelity` | ✅ Yes (`fidelity`) | ✅ Yes | engine.go/fidelity.go |
| `AutoStatus` | ✅ Yes (`auto_status`) | ✅ Yes | codergen.go |
| `GoalGate` | ✅ Yes (`goal_gate`) | ✅ Yes | engine.go |

**Utilization:** 12/13 fields (92%)

---

## Implementation Plan

### Phase 1: Quick Win — Reasoning Effort (1 hour)

**Goal:** Wire `reasoning_effort` from node attrs to LLM request

**Files to modify:**
1. `pipeline/handlers/codergen.go` — Add reasoning_effort to buildConfig()

**Implementation:**
```go
// In buildConfig(), after max_turns block:
if re, ok := node.Attrs["reasoning_effort"]; ok && re != "" {
    config.ReasoningEffort = re
}
```

**Testing:**
- Create `.dip` file with `reasoning_effort: high`
- Verify LLM request includes reasoning_effort parameter
- Verify OpenAI/Anthropic provider receives it

---

### Phase 2: Semantic Lint Rules (8-12 hours)

**Goal:** Implement missing DIP101-DIP112 lint warnings

**Strategy:** Incremental implementation, one rule at a time, TDD

**Priority Order:**
1. **DIP110** (Empty prompt) — 15 min
2. **DIP111** (Tool without timeout) — 15 min  
3. **DIP102** (No default edge) — 30 min
4. **DIP104** (Unbounded retry) — 30 min
5. **DIP108** (Unknown model/provider) — 45 min
6. **DIP101** (Unreachable via conditional) — 1 hour
7. **DIP107** (Unused write) — 1 hour
8. **DIP112** (Reads not produced) — 1 hour
9. **DIP105** (No success path) — 1.5 hours
10. **DIP106** (Undefined var in prompt) — 1.5 hours
11. **DIP103** (Overlapping conditions) — 2 hours
12. **DIP109** (Namespace collision) — 1 hour

**Files:**
- Create: `pipeline/lint_dippin.go` (new lint rules)
- Create: `pipeline/lint_dippin_test.go` (comprehensive tests)
- Modify: `pipeline/validate_semantic.go` (call new linter)

**Output:**
- `ValidateSemantic()` returns both errors and warnings
- CLI displays warnings after successful validation
- Exit code 0 for warnings, 1 for errors

---

### Phase 3: Lint Integration (2 hours)

**Goal:** Expose lint warnings in CLI and pipeline execution

**Files:**
1. `cmd/tracker/validate.go` — Add `--lint` flag
2. `pipeline/engine.go` — Optionally run lint on graph load
3. `pipeline/events.go` — Add `EventLintWarning` for TUI display

**CLI Interface:**
```bash
tracker validate pipeline.dip           # Structural only (DIP001-DIP009)
tracker validate --lint pipeline.dip    # Structural + semantic (DIP001-DIP112)
```

**TUI Integration:**
- Display lint warnings in header panel
- Use yellow/orange for warnings
- Don't block execution

---

## Testing Strategy

### Unit Tests
- Each lint rule gets 3-5 test cases (positive, negative, edge cases)
- Use `testdata/*.dip` fixtures for complex workflows
- Mock handler registry for validation tests

### Integration Tests
- End-to-end `.dip` → parse → validate → execute
- Verify reasoning_effort reaches provider
- Test lint warnings don't block execution

### Regression Tests
- Ensure existing examples/ pipelines still validate
- No new false positives from lint rules

---

## Success Criteria

### Phase 1 Complete When:
- [ ] `reasoning_effort: high` in `.dip` reaches LLM provider
- [ ] OpenAI extended thinking requests include reasoning_effort
- [ ] Existing pipelines unaffected

### Phase 2 Complete When:
- [ ] All 12 lint rules implemented (DIP101-DIP112)
- [ ] Each rule has comprehensive test coverage
- [ ] Lint warnings formatted per Dippin spec (rustc-style)

### Phase 3 Complete When:
- [ ] `tracker validate --lint` works end-to-end
- [ ] TUI displays warnings without blocking execution
- [ ] Documentation updated with lint reference

### Full Parity Complete When:
- [ ] 100% of Dippin IR fields utilized (13/13)
- [ ] All Dippin validation rules implemented (21/21)
- [ ] Tracker examples/ directory includes `.dip` versions
- [ ] README updated with Dippin-first messaging

---

## Non-Goals (Out of Scope)

These are Dippin features **not** planned for this effort:

1. **DOT Export** — Already handled by dippin-lang CLI (`dippin export-dot`)
2. **Migration Tool** — Already in dippin-lang (`dippin migrate`)
3. **Formatter** — Already in dippin-lang (`dippin fmt`)
4. **Editor Integration** — LSP/syntax highlighting in dippin-lang repo
5. **Dippin Parser** — Tracker uses dippin-lang as library, no custom parser
6. **Nested Subgraphs** — Already works (recursive subgraph handler)

---

## Risk Assessment

| Risk | Probability | Impact | Mitigation |
|------|-------------|--------|------------|
| Lint rules too noisy (false positives) | Medium | Low | Conservative defaults, suppressions |
| Performance impact of full linting | Low | Low | Make lint opt-in, cache results |
| Breaking change to existing workflows | Low | High | Warnings-only, no blocking |
| Reasoning effort provider compatibility | Medium | Medium | Graceful degradation, document support |

---

## Dependencies

1. **dippin-lang v0.1.0** — Already imported, no upgrade needed
2. **LLM Provider APIs** — Reasoning effort supported by:
   - ✅ OpenAI (extended thinking)
   - ❌ Anthropic (no direct equivalent, graceful ignore)
   - ❌ Gemini (unknown, needs investigation)

---

## Conclusion

Tracker is **92% feature-complete** for Dippin language support. The remaining 8% breaks down as:

- **1 small runtime gap** (reasoning_effort wiring) — 1 hour fix
- **9 missing lint rules** — 10-12 hours work
- **CLI/TUI integration** — 2 hours polish

**Total estimated effort:** 13-15 hours

**Recommended approach:** Phase 1 first (quick win), then Phase 2 incrementally (one lint rule per day), then Phase 3 (polish).

After completion, Tracker will be the **reference implementation** for Dippin language execution, with full validation parity to the spec.
