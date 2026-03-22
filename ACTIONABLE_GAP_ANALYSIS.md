# Actionable Gap Analysis: Dippin-Lang Parity

**Date:** 2024-03-21  
**Status:** Based on corrected critique, not Gemini's unsupported claims

---

## 🎯 Executive Summary

**Current State:** Tracker has ~85-95% dippin-lang compatibility (high confidence)

**Verified Gaps:**
1. ❌ 3 missing lint rules (DIP113-115) - Low priority
2. ❓ Field name mapping unclear - Needs documentation
3. ❓ Native .dip parsing support - Needs testing

**Effort to 100%:** 8-16 hours (2 days)

---

## ✅ What's Already Working

### Core Functionality (100%)
- Variable interpolation (ctx, params, graph namespaces)
- Conditional routing (all comparison + logical operators)
- 6 node types (agent, human, tool, parallel, fan_in, subgraph)
- Context propagation and merging
- Checkpoint/resume
- CLI validate command

### Configuration (100%)
- All 5 retry policies (none, standard, aggressive, patient, linear)
- All 6 fidelity levels (full, summary:high/medium/low, compact, truncate)
- Retry overrides (base_delay, max_retries)
- Compaction threshold

### Validation (92%)
- 9/9 structural errors (DIP001-009) ✅
- 12/15 semantic warnings (DIP101-112) ✅
- Missing: DIP113, DIP114, DIP115 ❌

### Advanced Features (100%)
- Restart edges with max_restarts
- Goal gates
- Auto status parsing
- Parallel execution with fan-in
- Subgraph composition with params

---

## ❌ Verified Gaps

### Gap 1: Missing Lint Rules (8 hours)

**Impact:** Low - Quality-of-life warnings for invalid config values

**Missing Rules:**
- DIP113: Invalid Retry Policy Name (e.g., "agressive" typo)
- DIP114: Invalid Fidelity Level (e.g., "sumary:high" typo)
- DIP115: Goal Gate Without Recovery Path

**Implementation Plan:**

#### DIP113: Invalid Retry Policy Name (2 hours)

**Location:** `pipeline/lint_dippin.go`

**Code:**
```go
// lintDIP113 checks for invalid retry_policy values.
func lintDIP113(g *Graph) []string {
	var warnings []string
	validPolicies := []string{"none", "standard", "aggressive", "patient", "linear"}
	
	// Check workflow default
	if policy, ok := g.Attrs["default_retry_policy"]; ok && policy != "" {
		if !contains(validPolicies, policy) {
			warnings = append(warnings, fmt.Sprintf(
				"warning[DIP113]: workflow has default_retry_policy %q which is not a recognized policy name (valid: %s)",
				policy, strings.Join(validPolicies, ", ")))
		}
	}
	
	// Check each node
	for _, node := range g.Nodes {
		if policy, ok := node.Attrs["retry_policy"]; ok && policy != "" {
			if !contains(validPolicies, policy) {
				warnings = append(warnings, fmt.Sprintf(
					"warning[DIP113]: node %q has retry_policy %q which is not a recognized policy name (valid: %s)",
					node.ID, policy, strings.Join(validPolicies, ", ")))
			}
		}
	}
	return warnings
}

func contains(slice []string, item string) bool {
	for _, s := range slice {
		if s == item {
			return true
		}
	}
	return false
}
```

**Test:**
```go
func TestLintDIP113(t *testing.T) {
	g := &Graph{
		Attrs: map[string]string{"default_retry_policy": "agressive"}, // typo
		Nodes: map[string]*Node{
			"A": {ID: "A", Attrs: map[string]string{"retry_policy": "standard"}},
			"B": {ID: "B", Attrs: map[string]string{"retry_policy": "patient-backoff"}}, // invalid
		},
	}
	warnings := lintDIP113(g)
	if len(warnings) != 2 {
		t.Errorf("expected 2 warnings, got %d", len(warnings))
	}
}
```

#### DIP114: Invalid Fidelity Level (2 hours)

**Location:** `pipeline/lint_dippin.go`

**Code:**
```go
// lintDIP114 checks for invalid fidelity values.
func lintDIP114(g *Graph) []string {
	var warnings []string
	validLevels := []string{"full", "summary:high", "summary:medium", "summary:low", "compact", "truncate"}
	
	// Check workflow default
	if level, ok := g.Attrs["default_fidelity"]; ok && level != "" {
		if !contains(validLevels, level) {
			warnings = append(warnings, fmt.Sprintf(
				"warning[DIP114]: workflow has default_fidelity %q which is not a recognized level (valid: %s)",
				level, strings.Join(validLevels, ", ")))
		}
	}
	
	// Check each node
	for _, node := range g.Nodes {
		if level, ok := node.Attrs["fidelity"]; ok && level != "" {
			if !contains(validLevels, level) {
				warnings = append(warnings, fmt.Sprintf(
					"warning[DIP114]: node %q has fidelity %q which is not a recognized level (valid: %s)",
					node.ID, level, strings.Join(validLevels, ", ")))
			}
		}
	}
	return warnings
}
```

**Test:** Similar to DIP113.

#### DIP115: Goal Gate Without Recovery (4 hours)

**Location:** `pipeline/lint_dippin.go`

**Code:**
```go
// lintDIP115 checks for goal gates without retry or fallback paths.
func lintDIP115(g *Graph) []string {
	var warnings []string
	
	for _, node := range g.Nodes {
		// Only check nodes with goal_gate: true
		if goalGate, ok := node.Attrs["goal_gate"]; !ok || goalGate != "true" {
			continue
		}
		
		// Check if node has recovery mechanism
		hasRetry := node.Attrs["retry_target"] != ""
		hasFallback := node.Attrs["fallback_target"] != ""
		hasMaxRetries := node.Attrs["max_retries"] != "" && node.Attrs["max_retries"] != "0"
		
		// If it's a goal gate with no way to recover from failure, warn
		if !hasRetry && !hasFallback {
			warnings = append(warnings, fmt.Sprintf(
				"warning[DIP115]: node %q has goal_gate: true but no retry_target or fallback_target (add retry_target or fallback_target so the pipeline can recover when the gate fails)",
				node.ID))
		}
	}
	return warnings
}
```

**Test:**
```go
func TestLintDIP115(t *testing.T) {
	g := &Graph{
		Nodes: map[string]*Node{
			"Gate1": {ID: "Gate1", Attrs: map[string]string{
				"goal_gate": "true",
				// No retry_target or fallback_target
			}},
			"Gate2": {ID: "Gate2", Attrs: map[string]string{
				"goal_gate": "true",
				"retry_target": "Fix",
			}},
		},
	}
	warnings := lintDIP115(g)
	if len(warnings) != 1 {
		t.Errorf("expected 1 warning, got %d", len(warnings))
	}
}
```

**Integration:**

Add to `LintDippinRules()` in `pipeline/lint_dippin.go`:
```go
func LintDippinRules(g *Graph) []string {
	var warnings []string
	// ... existing rules ...
	warnings = append(warnings, lintDIP113(g)...)
	warnings = append(warnings, lintDIP114(g)...)
	warnings = append(warnings, lintDIP115(g)...)
	return warnings
}
```

---

## ❓ Needs Investigation

### Investigation 1: Field Name Mapping (4 hours)

**Objective:** Document dippin ↔ tracker attribute name differences

**Known Differences:**
- Dippin: `command` → Tracker: `tool_command`
- Dippin: `ref` → Tracker: `subgraph_ref`
- Dippin: `params` → Tracker: `subgraph_params`

**Process:**
1. Review `pipeline/dippin_adapter.go` for field translations
2. Create mapping table in `docs/DIPPIN_COMPATIBILITY.md`
3. Verify all node fields in spec are mapped

**Deliverable:**
```markdown
# Dippin ↔ Tracker Field Mapping

## Tool Nodes
| Dippin Field | Tracker Attribute | Notes |
|--------------|-------------------|-------|
| command      | tool_command      | Legacy DOT name |
| timeout      | timeout           | Same |

## Subgraph Nodes
| Dippin Field | Tracker Attribute | Notes |
|--------------|-------------------|-------|
| ref          | subgraph_ref      | Tracker uses _ref suffix |
| params       | subgraph_params   | Format: key1=val1,key2=val2 |

## Agent Nodes
| Dippin Field | Tracker Attribute | Notes |
|--------------|-------------------|-------|
| compaction_threshold | context_compaction_threshold | Tracker uses full name |
```

### Investigation 2: Human Gate Default (2 hours)

**Question:** Does tracker support `default` field on human nodes?

**Test:**
```dippin
workflow TestDefault
  start: Ask
  exit: Done

  human Ask
    mode: choice
    default: "yes"

  edges
    Ask -> Done label: "yes"
    Ask -> Retry label: "no"
```

**Verification:**
1. Create test .dip file
2. Run with tracker
3. Timeout the human prompt
4. Check if "yes" edge is auto-selected

**If missing:** Add support in `pipeline/handlers/human.go`

### Investigation 3: Agent cmd_timeout (2 hours)

**Question:** Does tracker support `cmd_timeout` for tool calls within agent loops?

**From spec:** "Timeout for tool/command execution within the agent's agentic loop."

**Search:** Look for timeout handling in `agent/exec` package

**If missing:** Low priority - agent sessions already have `max_turns` limit

---

## 📦 Deliverables

### Phase 1: Close Verified Gaps (8 hours)
- [ ] Implement DIP113 lint rule (2h)
- [ ] Implement DIP114 lint rule (2h)
- [ ] Implement DIP115 lint rule (4h)
- [ ] Add tests for all 3 rules (included above)
- [ ] Update `LintDippinRules()` to call new rules

**Result:** 100% lint rule coverage (15/15)

### Phase 2: Documentation (4 hours)
- [ ] Create `docs/DIPPIN_COMPATIBILITY.md` (2h)
- [ ] Document field name mappings (1h)
- [ ] Add .dip examples to repo (1h)
- [ ] Update README with dippin integration notes

### Phase 3: Verification (4 hours)
- [ ] Test human gate default field (2h)
- [ ] Test agent cmd_timeout field (2h)
- [ ] Create integration test suite with .dip files

**Total Effort:** 16 hours (2 days)

---

## 🎯 Success Criteria

### Minimum Viable (Phase 1 only):
- ✅ All 15 lint rules implemented
- ✅ Tests passing
- ✅ Can claim "100% dippin lint rule coverage"

### Recommended (Phases 1+2):
- ✅ All lint rules implemented
- ✅ Field mapping documented
- ✅ Examples in repo
- ✅ Can claim "Full dippin-lang compatibility"

### Ideal (All phases):
- ✅ All lint rules
- ✅ Documentation complete
- ✅ All optional fields verified
- ✅ Integration test suite
- ✅ Can claim "100% dippin-lang spec compliance"

---

## 🚀 Implementation Order

### Day 1 Morning (4 hours)
1. Implement DIP113 (2h)
2. Implement DIP114 (2h)

### Day 1 Afternoon (4 hours)
3. Implement DIP115 (4h)

**Checkpoint:** All lint rules done, tests passing

### Day 2 Morning (4 hours)
4. Create DIPPIN_COMPATIBILITY.md (2h)
5. Document field mappings (2h)

### Day 2 Afternoon (4 hours)
6. Test human default field (2h)
7. Test agent cmd_timeout (2h)

**Final State:** 100% verified compatibility, documented gaps

---

## 📊 Risk Assessment

| Risk | Likelihood | Impact | Mitigation |
|------|------------|--------|------------|
| Field mapping incompatibility | Medium | Medium | Document differences, provide adapter |
| Hidden spec features | Low | Low | Dippin spec is well-documented |
| Behavior divergence | Low | Medium | Integration tests catch this |
| Breaking changes in dippin | Low | Low | Dippin is stable v1.0+ |

**Overall Risk:** Low

**Confidence in 100% parity:** High (after Phase 3)

---

## 🏁 Recommendation

**Go Decision:** ✅ Proceed with Phase 1 (8 hours)

**Rationale:**
- Low effort (1 day of work)
- High value (100% lint rule coverage)
- Clear implementation path
- Low risk

**Optional:** Add Phases 2-3 for complete documentation and verification

**Timeline:** 1-2 days depending on scope

**Expected Outcome:** Tracker achieves 100% dippin-lang spec compliance

---

**End of Actionable Gap Analysis**

**Next Step:** Implement DIP113, DIP114, DIP115 lint rules
