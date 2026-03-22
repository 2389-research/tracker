# Dippin Feature Parity - Remaining Gaps Implementation Plan

**Date:** 2026-03-21  
**Status:** Ready for Implementation  
**Estimated Time:** 5 hours  
**Priority:** MEDIUM (Nice-to-have, not blocking)

---

## Overview

After comprehensive review, Tracker is **98% feature-complete** for Dippin language support. Three minor gaps remain that would bring it to 100%:

1. **Full Variable Interpolation** — `${params.X}` and `${graph.X}` support
2. **Edge Weight Prioritization** — Use weights for routing decisions
3. **Spawn Agent Configuration** — Configure child agent parameters

All three are **non-breaking additive features** that enhance existing functionality.

---

## Task 1: Full Variable Interpolation (2 hours)

### Current Behavior
Only `${ctx.X}` interpolation works. Prompts using `${params.X}` or `${graph.X}` are left uninterpolated.

### Target Behavior
All three namespaces interpolate correctly:
```
prompt:
  Context outcome: ${ctx.outcome}
  Subgraph param: ${params.model}
  Graph goal: ${graph.goal}
```

### Implementation Steps

#### Step 1: Create Interpolation Function (30 min)

**File:** `pipeline/interpolation.go` (new)

```go
// ABOUTME: Variable interpolation for ${ctx.X}, ${params.X}, ${graph.X} in prompts.
package pipeline

import "strings"

// InterpolateVariables replaces ${ctx.X}, ${params.X}, ${graph.X} placeholders
// with actual values from context, parameters, and graph attributes.
func InterpolateVariables(text string, ctx *PipelineContext, params map[string]string, graphAttrs map[string]string) string {
	// ${ctx.X} — Pipeline context
	for k, v := range ctx.Store {
		text = strings.ReplaceAll(text, "${ctx."+k+"}", v)
	}
	
	// ${params.X} — Subgraph/node parameters
	for k, v := range params {
		text = strings.ReplaceAll(text, "${params."+k+"}", v)
	}
	
	// ${graph.X} — Graph-level attributes
	for k, v := range graphAttrs {
		text = strings.ReplaceAll(text, "${graph."+k+"}", v)
	}
	
	return text
}
```

**Tests:**

**File:** `pipeline/interpolation_test.go` (new)

```go
package pipeline

import "testing"

func TestInterpolateVariables_AllNamespaces(t *testing.T) {
	ctx := NewPipelineContext()
	ctx.Store["outcome"] = "success"
	
	params := map[string]string{"model": "gpt-4", "task": "coding"}
	graphAttrs := map[string]string{"goal": "Build X", "version": "1.0"}
	
	input := "Status: ${ctx.outcome}, Model: ${params.model}, Goal: ${graph.goal}"
	result := InterpolateVariables(input, ctx, params, graphAttrs)
	
	expected := "Status: success, Model: gpt-4, Goal: Build X"
	if result != expected {
		t.Errorf("got %q, want %q", result, expected)
	}
}

func TestInterpolateVariables_MissingKeys(t *testing.T) {
	ctx := NewPipelineContext()
	params := map[string]string{}
	graphAttrs := map[string]string{}
	
	input := "Missing: ${ctx.unknown}, ${params.missing}, ${graph.none}"
	result := InterpolateVariables(input, ctx, params, graphAttrs)
	
	// Missing keys are left as-is
	if result != input {
		t.Errorf("expected missing keys to remain unchanged, got %q", result)
	}
}

func TestInterpolateVariables_EmptyInput(t *testing.T) {
	result := InterpolateVariables("", nil, nil, nil)
	if result != "" {
		t.Errorf("expected empty string, got %q", result)
	}
}
```

Run tests:
```bash
go test ./pipeline -run TestInterpolate -v
```

Expected: PASS

---

#### Step 2: Integrate into Fidelity/Prompt Processing (30 min)

**File:** `pipeline/fidelity.go`

Find the `ApplyFidelity` function and update it to use the new interpolation:

```go
func ApplyFidelity(prompt string, ctx *PipelineContext, params map[string]string, graphAttrs map[string]string) string {
	// Apply variable interpolation
	prompt = InterpolateVariables(prompt, ctx, params, graphAttrs)
	
	// Existing fidelity logic...
	// ...
	
	return prompt
}
```

**File:** `pipeline/handlers/codergen.go`

Update the `buildConfig()` method to pass graph attrs:

```go
func (h *CodergenHandler) buildConfig(node *Node, pctx *PipelineContext) agent.SessionConfig {
	// ... existing config building ...
	
	// Interpolate prompt with all namespaces
	prompt := InterpolateVariables(
		node.Attrs["prompt"],
		pctx,
		node.Attrs, // Node attrs as params
		h.graphAttrs, // Graph-level attrs
	)
	
	config.Prompt = prompt
	
	// ... rest of config ...
	return config
}
```

---

#### Step 3: Update Subgraph Handler to Pass Params (30 min)

**File:** `pipeline/handlers/subgraph.go`

Ensure subgraph params are available for interpolation:

```go
func (h *SubgraphHandler) Execute(ctx context.Context, node *Node, pctx *PipelineContext) (Outcome, error) {
	// ... load subgraph ...
	
	// Extract params from node attrs
	params := make(map[string]string)
	if paramsStr := node.Attrs["subgraph_params"]; paramsStr != "" {
		// Parse "key1=val1,key2=val2" format
		for _, pair := range strings.Split(paramsStr, ",") {
			kv := strings.SplitN(strings.TrimSpace(pair), "=", 2)
			if len(kv) == 2 {
				params[strings.TrimSpace(kv[0])] = strings.TrimSpace(kv[1])
			}
		}
	}
	
	// Store params in child context for interpolation
	childCtx := pctx.Derive()
	for k, v := range params {
		childCtx.Store["param_"+k] = v // Prefix to avoid collision
	}
	
	// ... execute subgraph with childCtx ...
}
```

---

#### Step 4: Update DIP106 Lint Rule (30 min)

**File:** `pipeline/lint_dippin.go`

Update the `lintDIP106` function to validate all three namespaces:

```go
func lintDIP106(g *Graph) []string {
	var warnings []string
	
	// Collect all writes to determine available ctx keys
	allWrites := make(map[string]bool)
	for _, node := range g.Nodes {
		if w := node.Attrs["writes"]; w != "" {
			for _, key := range strings.Split(w, ",") {
				key = strings.TrimSpace(key)
				if key != "" {
					allWrites[key] = true
				}
			}
		}
	}
	
	// Reserved context keys
	reservedKeys := map[string]bool{
		"goal": true, "outcome": true, "last_response": true,
		"last_cost": true, "last_turns": true, "human_response": true,
	}
	
	for _, node := range g.Nodes {
		prompt := node.Attrs["prompt"]
		if prompt == "" {
			continue
		}
		
		// Find all ${...} references
		refs := findVariableReferences(prompt)
		for _, ref := range refs {
			// Parse ${namespace.key}
			parts := strings.SplitN(ref, ".", 2)
			if len(parts) != 2 {
				warnings = append(warnings, fmt.Sprintf(
					"warning[DIP106]: node %q has malformed variable reference ${%s} (should be ${namespace.key})", node.ID, ref))
				continue
			}
			
			namespace := parts[0]
			key := parts[1]
			
			// Validate based on namespace
			switch namespace {
			case "ctx":
				if !reservedKeys[key] && !allWrites[key] {
					warnings = append(warnings, fmt.Sprintf(
						"warning[DIP106]: node %q prompt references undefined variable ${ctx.%s}", node.ID, key))
				}
			case "params":
				// params are passed at runtime, can't validate statically
				// Just ensure it's well-formed
			case "graph":
				// graph attrs are defined at graph level
				if _, ok := g.Attrs[key]; !ok {
					warnings = append(warnings, fmt.Sprintf(
						"warning[DIP106]: node %q prompt references undefined graph attribute ${graph.%s}", node.ID, key))
				}
			default:
				warnings = append(warnings, fmt.Sprintf(
					"warning[DIP106]: node %q has unknown namespace %q (expected ctx, params, or graph)", node.ID, namespace))
			}
		}
	}
	
	return warnings
}
```

Update tests:

**File:** `pipeline/lint_dippin_test.go`

```go
func TestLintDIP106_Namespaces(t *testing.T) {
	g := NewGraph("test")
	g.Attrs["version"] = "1.0"
	
	g.AddNode(&Node{
		ID:      "Test",
		Handler: "codergen",
		Attrs: map[string]string{
			"prompt": "Valid: ${ctx.outcome}, ${params.model}, ${graph.version}. Invalid: ${ctx.unknown}, ${graph.missing}",
		},
	})
	
	warnings := LintDippinRules(g)
	
	// Should warn about ctx.unknown and graph.missing
	expectedWarnings := 2
	actualWarnings := 0
	for _, w := range warnings {
		if strings.Contains(w, "DIP106") {
			actualWarnings++
		}
	}
	
	if actualWarnings != expectedWarnings {
		t.Errorf("expected %d DIP106 warnings, got %d: %v", expectedWarnings, actualWarnings, warnings)
	}
}
```

Run tests:
```bash
go test ./pipeline -run TestLintDIP106_Namespaces -v
```

Expected: PASS

---

### Integration Test

Create example file:

**File:** `examples/variable-interpolation-test.dip`

```
workflow VariableInterpolationTest
  goal: "Test all three variable namespaces"
  start: Start
  exit: Exit
  version: "1.0"

  agent Start
    prompt:
      Testing interpolation:
      - Graph goal: ${graph.goal}
      - Graph version: ${graph.version}
      - Context outcome will be set after execution
    writes: result

  subgraph SubTest
    ref: examples/subgraphs/param-test.dip
    params: model=gpt-4,task=coding

  agent CheckResult
    prompt:
      Previous result: ${ctx.result}
      This should work now.

  agent Exit
    prompt: Done.

  edges
    Start -> SubTest
    SubTest -> CheckResult
    CheckResult -> Exit
```

Run:
```bash
tracker examples/variable-interpolation-test.dip --no-tui
```

Verify in logs that all interpolations resolve correctly.

---

### Acceptance Criteria

- [ ] `${ctx.X}` interpolation works (already working)
- [ ] `${params.X}` interpolation works in subgraphs
- [ ] `${graph.X}` interpolation works for graph attrs
- [ ] DIP106 validates all three namespaces
- [ ] Unit tests pass
- [ ] Integration test passes
- [ ] No regressions in existing examples

---

## Task 2: Edge Weight Prioritization (1 hour)

### Current Behavior
Edge `weight` attribute is extracted but ignored. When multiple edges match, selection is non-deterministic.

### Target Behavior
Higher-weight edges are preferred when multiple edges match conditions.

### Implementation Steps

#### Step 1: Modify Edge Selection (30 min)

**File:** `pipeline/engine.go`

Find the `selectNextEdge()` function and add weight sorting:

```go
import "sort"

func (e *Engine) selectNextEdge(nodeID string, ctx *PipelineContext) *Edge {
	candidates := e.graph.OutgoingEdges(nodeID)
	
	var matches []*Edge
	for _, edge := range candidates {
		// Unconditional edges always match
		if edge.Condition == "" {
			matches = append(matches, edge)
			continue
		}
		
		// Evaluate conditional edges
		if ok, err := EvaluateCondition(edge.Condition, ctx); err == nil && ok {
			matches = append(matches, edge)
		}
	}
	
	if len(matches) == 0 {
		return nil
	}
	
	// Sort by weight (descending), then by label for determinism
	sort.Slice(matches, func(i, j int) bool {
		if matches[i].Weight != matches[j].Weight {
			return matches[i].Weight > matches[j].Weight
		}
		return matches[i].Label < matches[j].Label
	})
	
	return matches[0]
}
```

---

#### Step 2: Add Tests (30 min)

**File:** `pipeline/engine_test.go`

```go
func TestEngine_EdgeWeightPrioritization(t *testing.T) {
	g := NewGraph("test")
	g.AddNode(&Node{ID: "Start", Handler: "start"})
	g.AddNode(&Node{ID: "High", Handler: "codergen"})
	g.AddNode(&Node{ID: "Low", Handler: "codergen"})
	g.AddNode(&Node{ID: "Exit", Handler: "exit"})
	
	g.StartNode = "Start"
	g.ExitNode = "Exit"
	
	// Two unconditional edges with different weights
	g.AddEdge(&Edge{From: "Start", To: "High", Weight: 10})
	g.AddEdge(&Edge{From: "Start", To: "Low", Weight: 1})
	g.AddEdge(&Edge{From: "High", To: "Exit"})
	g.AddEdge(&Edge{From: "Low", To: "Exit"})
	
	registry := NewHandlerRegistry()
	registry.Register(&mockHandler{name: "start"})
	registry.Register(&mockHandler{name: "exit"})
	registry.Register(&mockHandler{name: "codergen"})
	
	engine := NewEngine(g, registry)
	ctx := NewPipelineContext()
	
	// Start should route to High (weight 10 > 1)
	nextEdge := engine.selectNextEdge("Start", ctx)
	if nextEdge.To != "High" {
		t.Errorf("expected edge to High (weight 10), got edge to %s (weight %d)", nextEdge.To, nextEdge.Weight)
	}
}

func TestEngine_EdgeWeightTieBreak(t *testing.T) {
	g := NewGraph("test")
	g.AddNode(&Node{ID: "Start", Handler: "start"})
	g.AddNode(&Node{ID: "A", Handler: "codergen"})
	g.AddNode(&Node{ID: "B", Handler: "codergen"})
	
	g.StartNode = "Start"
	
	// Same weight — should fall back to label alphabetically
	g.AddEdge(&Edge{From: "Start", To: "A", Weight: 5, Label: "zebra"})
	g.AddEdge(&Edge{From: "Start", To: "B", Weight: 5, Label: "alpha"})
	
	registry := NewHandlerRegistry()
	registry.Register(&mockHandler{name: "start"})
	registry.Register(&mockHandler{name: "codergen"})
	
	engine := NewEngine(g, registry)
	ctx := NewPipelineContext()
	
	// Should route to B (label "alpha" < "zebra")
	nextEdge := engine.selectNextEdge("Start", ctx)
	if nextEdge.To != "B" {
		t.Errorf("expected edge to B (label alpha), got edge to %s (label %s)", nextEdge.To, nextEdge.Label)
	}
}
```

Run tests:
```bash
go test ./pipeline -run TestEngine_EdgeWeight -v
```

Expected: PASS

---

### Documentation Update

**File:** `README.md`

Add to edge syntax section:

```markdown
### Edge Weights

Edges can have optional weights for prioritization when multiple edges match:

```
edges
  A -> B  weight: 10  # Preferred route
  A -> C  weight: 1   # Fallback route
```

Higher weights are preferred. When weights are equal, edges are chosen alphabetically by label.
```

---

### Acceptance Criteria

- [ ] Higher-weight edges preferred
- [ ] Equal weights tie-break alphabetically by label
- [ ] Weight 0 (or unset) treated as default
- [ ] Unit tests pass
- [ ] Documentation updated

---

## Task 3: Spawn Agent Configuration (2 hours)

### Current Behavior
The `spawn_agent` tool accepts only a `task` argument. Child agents use hardcoded defaults.

### Target Behavior
Full configuration of child agent sessions:
```go
spawn_agent:
  task: "Implement feature X"
  model: claude-opus-4
  provider: anthropic
  max_turns: 10
  system_prompt: "Custom instructions"
```

### Implementation Steps

#### Step 1: Extend Spawn Tool Arguments (1 hour)

**File:** `agent/tools/spawn.go`

```go
func (t *SpawnAgentTool) Execute(args map[string]any) (string, error) {
	task, ok := args["task"].(string)
	if !ok || task == "" {
		return "", fmt.Errorf("spawn_agent requires 'task' argument")
	}
	
	// Build config from args (with defaults)
	config := agent.SessionConfig{
		SystemPrompt: getStringArg(args, "system_prompt", "You are a helpful AI assistant delegating a subtask."),
		MaxTurns:     getIntArg(args, "max_turns", 10),
		Model:        getStringArg(args, "model", ""),
		Provider:     getStringArg(args, "provider", ""),
		Prompt:       task,
	}
	
	// Delegate to runner
	result, err := t.runner.RunSession(context.Background(), config)
	if err != nil {
		return "", fmt.Errorf("spawn_agent failed: %w", err)
	}
	
	return result.Response, nil
}

// Helper to extract string arg with default
func getStringArg(args map[string]any, key, defaultVal string) string {
	if v, ok := args[key]; ok {
		if s, ok := v.(string); ok {
			return s
		}
	}
	return defaultVal
}

// Helper to extract int arg with default
func getIntArg(args map[string]any, key string, defaultVal int) int {
	if v, ok := args[key]; ok {
		switch val := v.(type) {
		case int:
			return val
		case float64:
			return int(val)
		case string:
			if i, err := strconv.Atoi(val); err == nil {
				return i
			}
		}
	}
	return defaultVal
}
```

---

#### Step 2: Update Tool Definition (30 min)

**File:** `agent/tools/spawn.go`

Update the tool definition to include new parameters:

```go
func (t *SpawnAgentTool) Definition() llm.Tool {
	return llm.Tool{
		Name:        "spawn_agent",
		Description: "Delegate a subtask to a child AI agent session. Returns the agent's final response.",
		Parameters: llm.ToolParameters{
			Type: "object",
			Properties: map[string]llm.ToolProperty{
				"task": {
					Type:        "string",
					Description: "The task/prompt for the child agent.",
				},
				"model": {
					Type:        "string",
					Description: "Optional: LLM model to use (e.g., 'claude-opus-4', 'gpt-4'). Defaults to current agent's model.",
				},
				"provider": {
					Type:        "string",
					Description: "Optional: LLM provider (e.g., 'anthropic', 'openai'). Defaults to current agent's provider.",
				},
				"max_turns": {
					Type:        "integer",
					Description: "Optional: Maximum conversation turns for the child agent. Defaults to 10.",
				},
				"system_prompt": {
					Type:        "string",
					Description: "Optional: System prompt for the child agent. Defaults to generic assistant prompt.",
				},
			},
			Required: []string{"task"},
		},
	}
}
```

---

#### Step 3: Add Tests (30 min)

**File:** `agent/tools/spawn_test.go`

```go
func TestSpawnAgent_ConfigArgs(t *testing.T) {
	mockRunner := &mockSessionRunner{
		capturedConfig: nil,
	}
	
	tool := &SpawnAgentTool{runner: mockRunner}
	
	result, err := tool.Execute(map[string]any{
		"task":          "Test task",
		"model":         "gpt-4",
		"provider":      "openai",
		"max_turns":     5,
		"system_prompt": "Custom instructions",
	})
	
	if err != nil {
		t.Fatalf("spawn_agent failed: %v", err)
	}
	
	// Verify config was passed correctly
	config := mockRunner.capturedConfig
	if config.Model != "gpt-4" {
		t.Errorf("expected model gpt-4, got %s", config.Model)
	}
	if config.Provider != "openai" {
		t.Errorf("expected provider openai, got %s", config.Provider)
	}
	if config.MaxTurns != 5 {
		t.Errorf("expected max_turns 5, got %d", config.MaxTurns)
	}
	if config.SystemPrompt != "Custom instructions" {
		t.Errorf("expected custom system prompt, got %s", config.SystemPrompt)
	}
}

func TestSpawnAgent_Defaults(t *testing.T) {
	mockRunner := &mockSessionRunner{}
	tool := &SpawnAgentTool{runner: mockRunner}
	
	// Only provide task — should use defaults
	result, err := tool.Execute(map[string]any{
		"task": "Test task",
	})
	
	if err != nil {
		t.Fatalf("spawn_agent failed: %v", err)
	}
	
	config := mockRunner.capturedConfig
	if config.MaxTurns != 10 {
		t.Errorf("expected default max_turns 10, got %d", config.MaxTurns)
	}
}

type mockSessionRunner struct {
	capturedConfig *agent.SessionConfig
}

func (m *mockSessionRunner) RunSession(ctx context.Context, config agent.SessionConfig) (*agent.SessionResult, error) {
	m.capturedConfig = &config
	return &agent.SessionResult{Response: "mock response"}, nil
}
```

Run tests:
```bash
go test ./agent/tools -run TestSpawnAgent -v
```

Expected: PASS

---

### Integration Test

Create example that uses configured spawn:

**File:** `examples/spawn-config-test.dip`

```
workflow SpawnConfigTest
  goal: "Test spawn_agent configuration"
  start: Start
  exit: Exit

  agent Start
    prompt:
      Use spawn_agent to delegate a task with custom config.
      
      Call:
      spawn_agent(
        task="Write a haiku about recursion",
        model="gpt-4",
        max_turns=3,
        system_prompt="You are a poetic AI assistant specializing in technical haiku."
      )
      
      Return the haiku.

  agent Exit
    prompt: Done.

  edges
    Start -> Exit
```

Run:
```bash
OPENAI_API_KEY=sk-... tracker examples/spawn-config-test.dip --no-tui
```

Verify in logs that child agent uses specified model and config.

---

### Acceptance Criteria

- [ ] spawn_agent accepts config args (model, provider, max_turns, system_prompt)
- [ ] Config flows to child session
- [ ] Defaults work when args omitted
- [ ] Unit tests pass
- [ ] Integration test passes
- [ ] Tool definition updated

---

## Testing Checklist

After all tasks complete:

### Unit Tests
- [ ] All new tests pass (`go test ./...`)
- [ ] No regressions in existing tests
- [ ] Coverage ≥80% for new code

### Integration Tests
- [ ] Variable interpolation example works
- [ ] Edge weight routing works
- [ ] Spawn config example works

### Regression Tests
- [ ] All examples/ pipelines validate
- [ ] All examples/ pipelines execute (if API keys available)
- [ ] No new lint warnings on existing files

### Performance
- [ ] Interpolation overhead <10ms per prompt
- [ ] Weight sorting overhead negligible
- [ ] spawn_agent config parsing <1ms

---

## Commit Strategy

Each task gets its own commit:

```bash
# Task 1
git add pipeline/interpolation.go pipeline/interpolation_test.go pipeline/fidelity.go pipeline/handlers/codergen.go pipeline/handlers/subgraph.go pipeline/lint_dippin.go pipeline/lint_dippin_test.go examples/variable-interpolation-test.dip
git commit -m "feat(pipeline): full variable interpolation for ${ctx}, ${params}, ${graph}

Implements complete namespace support for variable interpolation in prompts.
Previously only ${ctx.X} was interpolated. Now ${params.X} (subgraph params)
and ${graph.X} (graph attributes) work as well.

Updated DIP106 lint rule to validate all three namespaces.

Closes Dippin parity gap #1."

# Task 2
git add pipeline/engine.go pipeline/engine_test.go README.md
git commit -m "feat(pipeline): use edge weights for routing prioritization

When multiple edges match (unconditional or conditional), higher-weight
edges are now preferred. Equal weights tie-break alphabetically by label
for deterministic routing.

Closes Dippin parity gap #2."

# Task 3
git add agent/tools/spawn.go agent/tools/spawn_test.go examples/spawn-config-test.dip
git commit -m "feat(agent): spawn_agent tool configuration parameters

Extends spawn_agent tool to accept model, provider, max_turns, and
system_prompt arguments for fine-grained control of delegated tasks.

Closes Dippin parity gap #3."
```

---

## Success Metrics

### Task 1 Success
- [ ] All three namespaces interpolate correctly
- [ ] DIP106 validates all namespaces
- [ ] Integration test passes
- [ ] No regressions

### Task 2 Success
- [ ] Edge weights influence routing
- [ ] Tie-breaking is deterministic
- [ ] Documentation updated
- [ ] Tests pass

### Task 3 Success
- [ ] Spawn accepts full config
- [ ] Config flows to child session
- [ ] Defaults work correctly
- [ ] Tests pass

### Overall Success
- [ ] 100% Dippin feature parity achieved
- [ ] All tests pass
- [ ] Documentation complete
- [ ] No open bugs

---

## Risk Assessment

| Risk | Probability | Impact | Mitigation |
|------|-------------|--------|------------|
| Interpolation breaks existing prompts | Low | Medium | Extensive testing, only interpolates ${...} |
| Weight sorting changes behavior | Low | High | Feature is additive (weight 0 = no preference) |
| Spawn config breaks existing calls | Very Low | Low | Backward compatible (task-only still works) |

**Overall Risk:** VERY LOW — All features are additive and backward-compatible.

---

## Timeline

**Sequential Implementation:** 5 hours
- Task 1: 2 hours
- Task 2: 1 hour
- Task 3: 2 hours

**Parallel Implementation:** 3 hours (if independent developers)
- All tasks can be implemented in parallel
- Final integration: 30 minutes

---

## Conclusion

These three tasks close the remaining 2% gap to achieve **100% Dippin language feature parity**. All tasks are:

- ✅ Non-breaking (backward compatible)
- ✅ Well-defined (clear acceptance criteria)
- ✅ Testable (unit + integration tests)
- ✅ Low-risk (additive features only)
- ✅ High-value (complete spec compliance)

**Recommended Approach:** Implement sequentially in order (Task 1 → 2 → 3) for simplicity, or in parallel for speed.

**Final State:** Tracker as the reference implementation of the Dippin language.
