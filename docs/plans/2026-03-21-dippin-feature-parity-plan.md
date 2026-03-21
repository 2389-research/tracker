# Dippin Feature Parity Implementation Plan

> **For Implementation Agents:** This plan uses TDD (test-first) for all tasks. Each task is independent and can be implemented in parallel or sequentially.

**Goal:** Close the 8% feature gap between Tracker and the Dippin language specification.

**Architecture:** Extends existing validation and handler patterns. No new architectural paradigms.

**Tech Stack:** Go 1.25+, existing Tracker interfaces

**Context:** See [dippin-feature-parity-analysis.md](./2026-03-21-dippin-feature-parity-analysis.md) for full analysis.

---

## Task 1: Wire Reasoning Effort to Runtime

**Priority:** HIGH — Quick win, user-visible feature  
**Estimated Time:** 1 hour  
**Complexity:** Low

### What's Missing

The `reasoning_effort` field is extracted from `.dip` files and stored in `node.Attrs["reasoning_effort"]`, but the codergen handler doesn't read it. Result: reasoning effort is ignored at runtime.

### Files to Modify

- `pipeline/handlers/codergen.go` — Add reasoning_effort to buildConfig()

### Step 1: Write the Failing Test

**File:** `pipeline/handlers/codergen_test.go`

```go
func TestCodergenHandler_ReasoningEffort(t *testing.T) {
	// Create a node with reasoning_effort attribute
	node := &pipeline.Node{
		ID:    "test",
		Attrs: map[string]string{
			"prompt":           "test prompt",
			"reasoning_effort": "high",
		},
	}

	// Create mock client that captures the config
	var capturedConfig agent.SessionConfig
	mockClient := &mockCompleter{
		onComplete: func(ctx context.Context, req *llm.Request) (*llm.Response, error) {
			return &llm.Response{Message: llm.AssistantMessage("ok")}, nil
		},
		onNewSession: func(cfg agent.SessionConfig) {
			capturedConfig = cfg
		},
	}

	handler := NewCodergenHandler(mockClient, "/tmp")
	pctx := pipeline.NewPipelineContext()

	_, err := handler.Execute(context.Background(), node, pctx)
	if err != nil {
		t.Fatal(err)
	}

	if capturedConfig.ReasoningEffort != "high" {
		t.Errorf("expected ReasoningEffort='high', got %q", capturedConfig.ReasoningEffort)
	}
}
```

### Step 2: Run Test to Verify Failure

```bash
go test ./pipeline/handlers/ -run TestCodergenHandler_ReasoningEffort -v
```

Expected: FAIL — `ReasoningEffort` is empty

### Step 3: Implement the Fix

**File:** `pipeline/handlers/codergen.go`

In the `buildConfig()` method, after the `command_timeout` block (around line 192):

```go
if ct, ok := node.Attrs["command_timeout"]; ok {
	if d, err := time.ParseDuration(ct); err == nil && d > 0 {
		config.CommandTimeout = d
	}
}

// Add this block:
if re, ok := node.Attrs["reasoning_effort"]; ok && re != "" {
	config.ReasoningEffort = re
}
```

### Step 4: Run Test to Verify Success

```bash
go test ./pipeline/handlers/ -run TestCodergenHandler_ReasoningEffort -v
```

Expected: PASS

### Step 5: Integration Test

Create `examples/reasoning_effort_test.dip`:

```
workflow ReasoningTest
  goal: "Test reasoning effort wiring"
  start: Think
  exit: Done

  agent Think
    reasoning_effort: high
    model: gpt-5.4
    provider: openai
    prompt:
      Solve this complex problem with extended thinking.

  agent Done
    prompt: Complete.

  edges
    Think -> Done
```

Run:
```bash
OPENAI_API_KEY=sk-... tracker examples/reasoning_effort_test.dip --no-tui
```

Verify in logs/trace that reasoning_effort appears in the OpenAI API request.

### Step 6: Commit

```bash
git add pipeline/handlers/codergen.go pipeline/handlers/codergen_test.go examples/reasoning_effort_test.dip
git commit -m "feat(pipeline): wire reasoning_effort from node attrs to LLM config

The reasoning_effort field was extracted from .dip files but not used
at runtime. Now codergen handler reads node.Attrs[\"reasoning_effort\"]
and passes it to SessionConfig, which flows to the LLM provider.

Closes gap between Dippin IR extraction and runtime utilization."
```

---

## Task 2: Implement DIP110 (Empty Prompt Warning)

**Priority:** HIGH — Catches common authoring error  
**Estimated Time:** 30 minutes  
**Complexity:** Low

### What This Checks

Warns when an `agent` node has no `prompt` field or an empty prompt. Without a prompt, the agent produces meaningless output.

### Files

- Create: `pipeline/lint_dippin.go`
- Create: `pipeline/lint_dippin_test.go`

### Step 1: Write the Failing Test

**File:** `pipeline/lint_dippin_test.go`

```go
package pipeline

import (
	"strings"
	"testing"
)

func TestLintDIP110_EmptyPrompt(t *testing.T) {
	g := NewGraph("test")
	g.AddNode(&Node{
		ID:      "Agent1",
		Handler: "codergen",
		Attrs:   map[string]string{}, // No prompt
	})

	warnings := LintDippinRules(g)
	if len(warnings) == 0 {
		t.Fatal("expected DIP110 warning for empty prompt")
	}
	if !strings.Contains(warnings[0], "DIP110") {
		t.Errorf("expected DIP110 warning, got: %s", warnings[0])
	}
	if !strings.Contains(warnings[0], "Agent1") {
		t.Errorf("warning should mention node ID, got: %s", warnings[0])
	}
}

func TestLintDIP110_NoWarningWithPrompt(t *testing.T) {
	g := NewGraph("test")
	g.AddNode(&Node{
		ID:      "Agent1",
		Handler: "codergen",
		Attrs:   map[string]string{"prompt": "do something"},
	})

	warnings := LintDippinRules(g)
	for _, w := range warnings {
		if strings.Contains(w, "DIP110") {
			t.Errorf("unexpected DIP110 warning: %s", w)
		}
	}
}
```

### Step 2: Run Test to Verify Failure

```bash
go test ./pipeline/ -run TestLintDIP110 -v
```

Expected: FAIL — `LintDippinRules` not defined

### Step 3: Implement

**File:** `pipeline/lint_dippin.go`

```go
// ABOUTME: Dippin semantic lint rules (DIP101-DIP112).
// ABOUTME: These are warnings that flag likely workflow design issues but don't block execution.
package pipeline

import (
	"fmt"
	"strings"
)

// LintDippinRules runs all Dippin semantic lint checks (DIP101-DIP112).
// Returns a list of warning messages. Warnings don't block execution but should be reviewed.
func LintDippinRules(g *Graph) []string {
	var warnings []string

	warnings = append(warnings, lintDIP110(g)...)
	// Future rules will be added here incrementally

	return warnings
}

// lintDIP110 checks for agent nodes with empty prompts.
func lintDIP110(g *Graph) []string {
	var warnings []string
	for _, node := range g.Nodes {
		// Only check agent nodes (handler=codergen)
		if node.Handler != "codergen" {
			continue
		}
		prompt := strings.TrimSpace(node.Attrs["prompt"])
		if prompt == "" {
			warnings = append(warnings, fmt.Sprintf(
				"warning[DIP110]: empty prompt on agent node %q", node.ID))
		}
	}
	return warnings
}
```

### Step 4: Run Test to Verify Success

```bash
go test ./pipeline/ -run TestLintDIP110 -v
```

Expected: PASS

### Step 5: Commit

```bash
git add pipeline/lint_dippin.go pipeline/lint_dippin_test.go
git commit -m "feat(pipeline): implement DIP110 lint rule (empty prompt warning)

First Dippin semantic lint rule. Warns when agent nodes have no prompt
or empty prompt text, which would produce meaningless output.

Part of Dippin feature parity effort."
```

---

## Task 3: Implement DIP111 (Tool Without Timeout Warning)

**Priority:** HIGH — Catches dangerous pattern (hanging commands)  
**Estimated Time:** 30 minutes  
**Complexity:** Low

### What This Checks

Warns when a `tool` node has no `timeout` field. Without a timeout, a hanging command blocks the pipeline indefinitely.

### Files

- Modify: `pipeline/lint_dippin.go`
- Modify: `pipeline/lint_dippin_test.go`

### Step 1: Write the Failing Test

**File:** `pipeline/lint_dippin_test.go`

```go
func TestLintDIP111_ToolWithoutTimeout(t *testing.T) {
	g := NewGraph("test")
	g.AddNode(&Node{
		ID:      "RunTests",
		Handler: "tool",
		Attrs:   map[string]string{"tool_command": "pytest"}, // No timeout
	})

	warnings := LintDippinRules(g)
	if len(warnings) == 0 {
		t.Fatal("expected DIP111 warning for tool without timeout")
	}
	if !strings.Contains(warnings[0], "DIP111") {
		t.Errorf("expected DIP111 warning, got: %s", warnings[0])
	}
}

func TestLintDIP111_NoWarningWithTimeout(t *testing.T) {
	g := NewGraph("test")
	g.AddNode(&Node{
		ID:      "RunTests",
		Handler: "tool",
		Attrs: map[string]string{
			"tool_command": "pytest",
			"timeout":      "60s",
		},
	})

	warnings := LintDippinRules(g)
	for _, w := range warnings {
		if strings.Contains(w, "DIP111") {
			t.Errorf("unexpected DIP111 warning: %s", w)
		}
	}
}
```

### Step 2: Run Test to Verify Failure

```bash
go test ./pipeline/ -run TestLintDIP111 -v
```

Expected: FAIL — no DIP111 warning

### Step 3: Implement

**File:** `pipeline/lint_dippin.go`

Add to `LintDippinRules()`:
```go
warnings = append(warnings, lintDIP111(g)...)
```

Add new function:
```go
// lintDIP111 checks for tool nodes without timeout.
func lintDIP111(g *Graph) []string {
	var warnings []string
	for _, node := range g.Nodes {
		// Only check tool nodes
		if node.Handler != "tool" {
			continue
		}
		// If node has a command but no timeout, warn
		if node.Attrs["tool_command"] != "" && node.Attrs["timeout"] == "" {
			warnings = append(warnings, fmt.Sprintf(
				"warning[DIP111]: tool node %q has no timeout", node.ID))
		}
	}
	return warnings
}
```

### Step 4: Run Test to Verify Success

```bash
go test ./pipeline/ -run TestLintDIP111 -v
```

Expected: PASS

### Step 5: Commit

```bash
git add pipeline/lint_dippin.go pipeline/lint_dippin_test.go
git commit -m "feat(pipeline): implement DIP111 lint rule (tool without timeout)

Warns when tool nodes have commands but no timeout. Prevents hanging
commands from blocking pipeline execution indefinitely."
```

---

## Task 4: Implement DIP102 (No Default Edge Warning)

**Priority:** HIGH — Catches routing dead-ends  
**Estimated Time:** 45 minutes  
**Complexity:** Medium

### What This Checks

Warns when a node has conditional outgoing edges but no unconditional fallback. If no condition matches at runtime, execution gets stuck.

### Files

- Modify: `pipeline/lint_dippin.go`
- Modify: `pipeline/lint_dippin_test.go`

### Step 1: Write the Failing Test

**File:** `pipeline/lint_dippin_test.go`

```go
func TestLintDIP102_NoDefaultEdge(t *testing.T) {
	g := NewGraph("test")
	g.AddNode(&Node{ID: "Check", Handler: "codergen"})
	g.AddNode(&Node{ID: "Pass", Handler: "codergen"})
	g.AddNode(&Node{ID: "Fail", Handler: "codergen"})

	// Two conditional edges, no unconditional fallback
	g.AddEdge(&Edge{From: "Check", To: "Pass", Condition: "ctx.outcome = success"})
	g.AddEdge(&Edge{From: "Check", To: "Fail", Condition: "ctx.outcome = fail"})

	warnings := LintDippinRules(g)
	if len(warnings) == 0 {
		t.Fatal("expected DIP102 warning for missing default edge")
	}
	if !strings.Contains(warnings[0], "DIP102") {
		t.Errorf("expected DIP102 warning, got: %s", warnings[0])
	}
	if !strings.Contains(warnings[0], "Check") {
		t.Errorf("warning should mention node ID, got: %s", warnings[0])
	}
}

func TestLintDIP102_NoWarningWithDefault(t *testing.T) {
	g := NewGraph("test")
	g.AddNode(&Node{ID: "Check", Handler: "codergen"})
	g.AddNode(&Node{ID: "Pass", Handler: "codergen"})
	g.AddNode(&Node{ID: "Fail", Handler: "codergen"})

	g.AddEdge(&Edge{From: "Check", To: "Pass", Condition: "ctx.outcome = success"})
	g.AddEdge(&Edge{From: "Check", To: "Fail"}) // Unconditional fallback

	warnings := LintDippinRules(g)
	for _, w := range warnings {
		if strings.Contains(w, "DIP102") {
			t.Errorf("unexpected DIP102 warning: %s", w)
		}
	}
}
```

### Step 2: Run Test to Verify Failure

```bash
go test ./pipeline/ -run TestLintDIP102 -v
```

Expected: FAIL

### Step 3: Implement

**File:** `pipeline/lint_dippin.go`

Add to `LintDippinRules()`:
```go
warnings = append(warnings, lintDIP102(g)...)
```

Add new function:
```go
// lintDIP102 checks for routing nodes with conditional edges but no default/unconditional edge.
func lintDIP102(g *Graph) []string {
	var warnings []string

	// Build adjacency map of outgoing edges per node
	outgoing := make(map[string][]*Edge)
	for _, edge := range g.Edges {
		outgoing[edge.From] = append(outgoing[edge.From], edge)
	}

	for nodeID, edges := range outgoing {
		if len(edges) == 0 {
			continue
		}

		hasConditional := false
		hasUnconditional := false
		for _, edge := range edges {
			if edge.Condition != "" {
				hasConditional = true
			} else {
				hasUnconditional = true
			}
		}

		// Warn if node has conditional edges but no unconditional fallback
		if hasConditional && !hasUnconditional {
			warnings = append(warnings, fmt.Sprintf(
				"warning[DIP102]: node %q has conditional edges but no default/unconditional edge", nodeID))
		}
	}

	return warnings
}
```

### Step 4: Run Test to Verify Success

```bash
go test ./pipeline/ -run TestLintDIP102 -v
```

Expected: PASS

### Step 5: Commit

```bash
git add pipeline/lint_dippin.go pipeline/lint_dippin_test.go
git commit -m "feat(pipeline): implement DIP102 lint rule (no default edge)

Warns when routing nodes have conditional edges but no unconditional
fallback. Prevents execution getting stuck when no condition matches."
```

---

## Task 5: Integrate Lint Warnings into Validation

**Priority:** MEDIUM — Makes lint warnings visible to users  
**Estimated Time:** 1 hour  
**Complexity:** Medium

### What This Does

Integrates the Dippin lint rules into the existing `ValidateSemantic()` flow and exposes them via CLI.

### Files

- Modify: `pipeline/validate_semantic.go`
- Modify: `cmd/tracker/validate.go` (if exists, else create)
- Modify: `cmd/tracker/main.go` — Add validate subcommand

### Step 1: Modify ValidateSemantic to Include Warnings

**File:** `pipeline/validate_semantic.go`

Change function signature:
```go
// ValidateSemantic checks a Graph for semantic correctness against a handler
// registry. It returns both errors (blocking) and warnings (advisory).
func ValidateSemantic(g *Graph, registry *HandlerRegistry) (errors error, warnings []string) {
	if g == nil {
		return &ValidationError{Errors: []string{"graph is nil"}}, nil
	}
	ve := &ValidationError{}
	validateHandlerRegistration(g, registry, ve)
	validateConditionSyntax(g, ve)
	validateNodeAttributes(g, ve)

	// Run Dippin lint rules (warnings only)
	lintWarnings := LintDippinRules(g)

	if ve.hasErrors() {
		return ve, lintWarnings
	}
	return nil, lintWarnings
}
```

### Step 2: Update Callers

Find all calls to `ValidateSemantic()` and update:

```bash
grep -rn "ValidateSemantic" --include="*.go" | grep -v test
```

Update each caller to handle warnings:
```go
err, warnings := ValidateSemantic(g, registry)
if err != nil {
	return err
}
for _, w := range warnings {
	log.Printf("LINT: %s\n", w)
}
```

### Step 3: Add CLI Flag

**File:** `cmd/tracker/main.go`

Add validate subcommand:
```go
var validateCmd = &cobra.Command{
	Use:   "validate [file]",
	Short: "Validate a pipeline file",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		// Parse graph
		g, err := pipeline.ParseFile(args[0])
		if err != nil {
			return err
		}

		// Structural validation
		if err := pipeline.ValidateGraph(g); err != nil {
			return err
		}

		// Semantic validation + lint
		registry := pipeline.NewHandlerRegistry()
		// ... register handlers ...

		errs, warnings := pipeline.ValidateSemantic(g, registry)
		if errs != nil {
			return errs
		}

		// Display warnings
		for _, w := range warnings {
			fmt.Fprintf(os.Stderr, "%s\n", w)
		}

		if len(warnings) == 0 {
			fmt.Println("validation passed (no warnings)")
		} else {
			fmt.Printf("validation passed (%d warnings)\n", len(warnings))
		}
		return nil
	},
}
```

### Step 4: Test

```bash
# Create test pipeline with lint warnings
cat > test_lint.dip <<'EOF'
workflow TestLint
  start: A
  exit: B

  agent A
    # Empty prompt — should trigger DIP110

  agent B
    prompt: done
  
  edges
    A -> B
EOF

tracker validate test_lint.dip
```

Expected output:
```
warning[DIP110]: empty prompt on agent node "A"
validation passed (1 warning)
```

### Step 5: Commit

```bash
git add pipeline/validate_semantic.go cmd/tracker/validate.go cmd/tracker/main.go
git commit -m "feat(cli): integrate Dippin lint warnings into validation

ValidateSemantic() now returns (errors, warnings). The validate
subcommand displays lint warnings but exits 0 (warnings don't block).

Enables users to catch workflow design issues before execution."
```

---

## Remaining Tasks (Summary)

The plan above covers:
- ✅ Task 1: Reasoning effort wiring (1 hour)
- ✅ Task 2: DIP110 (empty prompt) (30 min)
- ✅ Task 3: DIP111 (tool timeout) (30 min)
- ✅ Task 4: DIP102 (no default edge) (45 min)
- ✅ Task 5: CLI integration (1 hour)

**Remaining lint rules** (implement incrementally using same TDD pattern):

| Task | Rule | Time | Complexity |
|------|------|------|------------|
| 6 | DIP104 (unbounded retry) | 30 min | Low |
| 7 | DIP108 (unknown model/provider) | 45 min | Low |
| 8 | DIP101 (unreachable via conditional) | 1 hour | Medium |
| 9 | DIP107 (unused write) | 1 hour | Medium |
| 10 | DIP112 (reads not produced) | 1 hour | Medium |
| 11 | DIP105 (no success path) | 1.5 hours | Medium |
| 12 | DIP106 (undefined var in prompt) | 1.5 hours | Medium |
| 13 | DIP103 (overlapping conditions) | 2 hours | Hard |
| 14 | DIP109 (namespace collision) | 1 hour | Medium |

**Total remaining:** ~10.5 hours

---

## Testing Checklist

After all tasks complete:

- [ ] All unit tests pass (`go test ./...`)
- [ ] Integration tests with real `.dip` files
- [ ] Examples directory has `.dip` versions
- [ ] No regressions in existing DOT pipelines
- [ ] Lint warnings don't block execution
- [ ] `tracker validate --lint` works end-to-end
- [ ] Documentation updated

---

## Success Criteria

**Phase 1 (Reasoning Effort):**
- [ ] `reasoning_effort` field flows from `.dip` to LLM request
- [ ] OpenAI provider receives reasoning_effort parameter
- [ ] Integration test passes with real API call

**Phase 2 (Lint Rules):**
- [ ] All 12 Dippin lint rules implemented (DIP101-DIP112)
- [ ] Each rule has ≥3 test cases
- [ ] Warnings formatted per spec: `warning[DIPXXX]: message`

**Phase 3 (CLI Integration):**
- [ ] `tracker validate` command exists
- [ ] Warnings displayed, exit code 0
- [ ] TUI integration (optional stretch goal)

**Full Parity:**
- [ ] 100% Dippin IR field utilization (13/13)
- [ ] 100% Dippin validation rules (21/21)
- [ ] Documentation complete
- [ ] Examples updated

---

## Implementation Notes

### TDD Pattern for Lint Rules

All lint rules follow this pattern:

1. **Test first** — Write failing test in `lint_dippin_test.go`
2. **Run test** — Verify failure: `go test ./pipeline/ -run TestLintDIPXXX -v`
3. **Implement** — Add `lintDIPXXX()` function to `lint_dippin.go`
4. **Run test** — Verify success
5. **Commit** — One rule per commit

### Code Organization

```
pipeline/
├── lint_dippin.go          # All Dippin lint rules (DIP101-DIP112)
├── lint_dippin_test.go     # Comprehensive tests for each rule
├── validate_semantic.go    # Calls LintDippinRules(), returns warnings
└── validate.go             # Structural validation (DIP001-DIP009)
```

### Avoiding False Positives

- **DIP101/DIP105:** Graph analysis may have cycles — use BFS/DFS carefully
- **DIP106/DIP107:** Reads/writes are advisory in Dippin v1 — warn, don't error
- **DIP103:** Condition overlap detection is hard — start conservative

### Provider Compatibility

For `reasoning_effort`:
- OpenAI: Maps to `reasoning_effort` parameter ✅
- Anthropic: No direct equivalent, ignore gracefully ⚠️
- Gemini: Unknown, needs investigation ❓

Document in code comments which providers support it.

---

## Future Work (Post-Parity)

After achieving full Dippin parity:

1. **Autofix suggestions** — `tracker validate --fix` applies safe corrections
2. **LSP integration** — Real-time lint warnings in editors
3. **Custom lint rules** — User-defined rules via plugins
4. **Lint suppression** — `# dippin-lint-ignore DIP110` comments
5. **CI/CD integration** — GitHub Action for validation

These are **out of scope** for the parity effort but natural next steps.
