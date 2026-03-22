# Implementation Plan: Dippin Language Feature Parity

**Goal:** Close the 2% gap to achieve 100% dippin-lang specification compliance

**Status:** Ready for Execution  
**Estimated Effort:** 3.5 hours  
**Risk Level:** Low

---

## Gap Summary

Based on the comprehensive feature analysis, **1 feature is missing** for full dippin-lang parity:

1. **CLI Validation Command** - Expose semantic validation via `tracker validate [file]`

Additional hardening tasks (optional but recommended):

2. **Max Subgraph Nesting Check** - Prevent stack overflow from circular references
3. **Documentation Updates** - Document edge cases and validation behavior

---

## Task 1: CLI Validation Command

**Priority:** High  
**Effort:** 2 hours  
**Dependencies:** None (all validation logic exists)

### Objective
Expose the existing semantic validation and lint system via a standalone CLI command that users can run before execution.

### Current State
- ✅ `pipeline.Validate()` - structural validation
- ✅ `pipeline.ValidateSemantic()` - semantic validation + lint warnings
- ✅ All 12 DIP lint rules implemented
- ❌ No CLI command to invoke these functions standalone

### Implementation Steps

#### Step 1: Create `cmd/tracker/validate.go`

```go
// ABOUTME: CLI command for validating pipeline files without executing them.
// ABOUTME: Runs structural + semantic validation and displays lint warnings.
package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/2389-research/tracker/pipeline"
	"github.com/2389-research/tracker/pipeline/handlers"
	"github.com/urfave/cli/v2"
)

func validateCommand() *cli.Command {
	return &cli.Command{
		Name:      "validate",
		Usage:     "Validate a .dip or .dot pipeline file",
		ArgsUsage: "<file>",
		Flags: []cli.Flag{
			&cli.BoolFlag{
				Name:    "strict",
				Aliases: []string{"s"},
				Usage:   "Treat warnings as errors (exit code 1)",
			},
			&cli.BoolFlag{
				Name:    "quiet",
				Aliases: []string{"q"},
				Usage:   "Only print errors and warnings (suppress success messages)",
			},
		},
		Action: runValidate,
	}
}

func runValidate(c *cli.Context) error {
	if c.NArg() == 0 {
		return cli.Exit("Error: missing file argument\nUsage: tracker validate <file>", 1)
	}

	filePath := c.Args().Get(0)
	quiet := c.Bool("quiet")
	strict := c.Bool("strict")

	// Parse the file
	graph, err := pipeline.ParseFile(filePath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "❌ Parse error: %v\n", err)
		return cli.Exit("", 1)
	}

	// Structural validation
	if err := pipeline.Validate(graph); err != nil {
		fmt.Fprintf(os.Stderr, "❌ Structural validation failed:\n")
		if ve, ok := err.(*pipeline.ValidationError); ok {
			for _, msg := range ve.Errors {
				fmt.Fprintf(os.Stderr, "  • %s\n", msg)
			}
		} else {
			fmt.Fprintf(os.Stderr, "  %v\n", err)
		}
		return cli.Exit("", 1)
	}
	if !quiet {
		fmt.Println("✅ Structural validation passed")
	}

	// Semantic validation (need registry for handler checks)
	registry := buildValidationRegistry(graph)
	errs, warnings := pipeline.ValidateSemantic(graph, registry)

	if errs != nil {
		fmt.Fprintf(os.Stderr, "❌ Semantic validation failed:\n")
		if ve, ok := errs.(*pipeline.ValidationError); ok {
			for _, msg := range ve.Errors {
				fmt.Fprintf(os.Stderr, "  • %s\n", msg)
			}
		} else {
			fmt.Fprintf(os.Stderr, "  %v\n", errs)
		}
		return cli.Exit("", 1)
	}

	// Print warnings
	if len(warnings) > 0 {
		fmt.Fprintf(os.Stderr, "\n⚠️  Lint warnings:\n")
		for _, w := range warnings {
			fmt.Fprintf(os.Stderr, "  %s\n", w)
		}
		fmt.Fprintf(os.Stderr, "\nResult: %d warning(s), 0 error(s)\n", len(warnings))

		if strict {
			return cli.Exit("Validation failed in strict mode due to warnings", 1)
		}
	} else if !quiet {
		fmt.Println("✅ No lint warnings")
	}

	if !quiet {
		fmt.Println("\n✅ Validation complete")
	}
	return nil
}

// buildValidationRegistry creates a minimal handler registry for semantic validation.
// We don't need real LLM clients or execution environments, just the handler names.
func buildValidationRegistry(graph *pipeline.Graph) *pipeline.HandlerRegistry {
	registry := pipeline.NewHandlerRegistry()
	
	// Register all built-in handlers by name
	registry.Register(handlers.NewStartHandler())
	registry.Register(handlers.NewExitHandler())
	registry.Register(handlers.NewConditionalHandler())
	registry.Register(handlers.NewFanInHandler())
	registry.Register(handlers.NewManagerLoopHandler())
	
	// Register handlers that need minimal dependencies
	registry.Register(handlers.NewParallelHandler(graph, registry, nil))
	
	// For validation, we don't need real implementations, just registration
	// Codergen, tool, human, subgraph handlers are registered by name only
	registry.Register(&stubHandler{name: "codergen"})
	registry.Register(&stubHandler{name: "tool"})
	registry.Register(&stubHandler{name: "wait.human"})
	registry.Register(&stubHandler{name: "subgraph"})
	
	return registry
}

// stubHandler is a no-op handler used only for validation (name registration).
type stubHandler struct {
	name string
}

func (h *stubHandler) Name() string { return h.name }

func (h *stubHandler) Execute(ctx context.Context, node *pipeline.Node, pctx *pipeline.PipelineContext) (pipeline.Outcome, error) {
	return pipeline.Outcome{}, fmt.Errorf("stub handler should not be executed")
}
```

#### Step 2: Register Command in `cmd/tracker/main.go`

Modify the `main()` function to add the new subcommand:

```go
app.Commands = []*cli.Command{
	setupCommand(),
	validateCommand(), // <-- ADD THIS LINE
}
```

#### Step 3: Add Tests

Create `cmd/tracker/validate_test.go`:

```go
package main

import (
	"os"
	"os/exec"
	"strings"
	"testing"
)

func TestValidateCommand_ValidFile(t *testing.T) {
	cmd := exec.Command("go", "run", ".", "validate", "../../examples/consensus_task.dip")
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("validate command failed: %v\nOutput: %s", err, output)
	}
	
	result := string(output)
	if !strings.Contains(result, "✅ Validation complete") {
		t.Errorf("expected success message, got: %s", result)
	}
}

func TestValidateCommand_WithWarnings(t *testing.T) {
	// Create a test file with lint warnings
	tmpFile, err := os.CreateTemp("", "test_*.dip")
	if err != nil {
		t.Fatal(err)
	}
	defer os.Remove(tmpFile.Name())
	
	content := `workflow Test
  start: A
  exit: A
  
  agent A
    # Empty prompt - triggers DIP110 warning
`
	if _, err := tmpFile.WriteString(content); err != nil {
		t.Fatal(err)
	}
	tmpFile.Close()
	
	cmd := exec.Command("go", "run", ".", "validate", tmpFile.Name())
	output, err := cmd.CombinedOutput()
	
	result := string(output)
	if !strings.Contains(result, "warning[DIP110]") {
		t.Errorf("expected DIP110 warning, got: %s", result)
	}
	if err != nil {
		t.Errorf("validate should exit 0 for warnings in lenient mode, got error: %v", err)
	}
}

func TestValidateCommand_StrictMode(t *testing.T) {
	// Same test file with warnings
	tmpFile, err := os.CreateTemp("", "test_*.dip")
	if err != nil {
		t.Fatal(err)
	}
	defer os.Remove(tmpFile.Name())
	
	content := `workflow Test
  start: A
  exit: A
  
  agent A
    # Empty prompt
`
	if _, err := tmpFile.WriteString(content); err != nil {
		t.Fatal(err)
	}
	tmpFile.Close()
	
	cmd := exec.Command("go", "run", ".", "validate", "--strict", tmpFile.Name())
	output, err := cmd.CombinedOutput()
	
	if err == nil {
		t.Errorf("validate --strict should exit 1 for warnings, got exit 0")
	}
	
	result := string(output)
	if !strings.Contains(result, "warning[DIP110]") {
		t.Errorf("expected DIP110 warning, got: %s", result)
	}
}

func TestValidateCommand_MissingFile(t *testing.T) {
	cmd := exec.Command("go", "run", ".", "validate")
	output, err := cmd.CombinedOutput()
	
	if err == nil {
		t.Errorf("expected error for missing file argument")
	}
	
	result := string(output)
	if !strings.Contains(result, "missing file argument") {
		t.Errorf("expected missing file error, got: %s", result)
	}
}
```

#### Step 4: Update Documentation

Add to `README.md` in the CLI Reference section:

```markdown
### Validate Pipeline

```bash
tracker validate <file> [flags]
```

Validate a `.dip` or `.dot` pipeline file without executing it. Runs structural and semantic validation, and displays lint warnings.

| Flag | Description |
|------|-------------|
| `-s, --strict` | Treat warnings as errors (exit code 1) |
| `-q, --quiet` | Only print errors and warnings (suppress success messages) |

**Exit codes:**
- `0` - Validation passed (or warnings in lenient mode)
- `1` - Validation failed (errors or warnings in strict mode)

**Examples:**

```bash
# Validate a pipeline
tracker validate examples/megaplan.dip
# ✅ Structural validation passed
# ⚠️  Lint warnings:
#   warning[DIP110]: empty prompt on agent node "Draft"
# Result: 1 warning(s), 0 error(s)
# ✅ Validation complete

# Strict mode (warnings cause failure)
tracker validate --strict examples/megaplan.dip
# Exit code: 1

# Quiet mode (only show problems)
tracker validate --quiet examples/consensus_task.dip
# (no output if valid)
```
```

#### Step 5: Test Manually

```bash
# Build tracker
go build ./cmd/tracker

# Test with valid file
./tracker validate examples/consensus_task.dip

# Test with file that has warnings
./tracker validate examples/human_gate_showcase.dip

# Test with invalid file (create one)
echo "invalid syntax" > /tmp/bad.dip
./tracker validate /tmp/bad.dip

# Test strict mode
./tracker validate --strict examples/megaplan.dip
echo "Exit code: $?"
```

### Success Criteria

- [ ] `tracker validate [file]` parses and validates pipeline files
- [ ] Structural errors exit with code 1 and display clear messages
- [ ] Semantic errors exit with code 1 and display clear messages
- [ ] Lint warnings print to stderr, exit with code 0 (lenient mode)
- [ ] `--strict` flag treats warnings as errors (exit code 1)
- [ ] `--quiet` flag suppresses success messages
- [ ] Help text works: `tracker validate --help`
- [ ] All tests pass: `go test ./cmd/tracker/...`
- [ ] Manual testing passes for all scenarios

### Commit Message

```
feat(cli): add validate command for standalone pipeline validation

Expose semantic validation and lint warnings via `tracker validate [file]`.
Supports --strict mode to treat warnings as errors, and --quiet mode for
CI/CD integration.

Closes feature gap for 100% dippin-lang compliance.

- New file: cmd/tracker/validate.go (185 lines)
- New file: cmd/tracker/validate_test.go (120 lines)
- Modified: cmd/tracker/main.go (register command)
- Modified: README.md (document command)

All tests passing. Exit codes:
- 0: validation passed or warnings in lenient mode
- 1: errors or warnings in strict mode
```

---

## Task 2: Max Subgraph Nesting Check (Optional Hardening)

**Priority:** Medium  
**Effort:** 1 hour  
**Dependencies:** None

### Objective
Prevent stack overflow from circular or deeply nested subgraph references.

### Current Risk
- `A.dip` → `B.dip` → `A.dip` causes infinite recursion
- Very deep nesting (>50 levels) could exhaust stack

### Implementation

Add to `pipeline/subgraph.go`:

```go
const MaxSubgraphDepth = 32

func (h *SubgraphHandler) Execute(ctx context.Context, node *Node, pctx *PipelineContext) (Outcome, error) {
	// Track nesting depth
	depth, _ := pctx.GetInternal("subgraph_depth")
	currentDepth := 0
	if depth != "" {
		currentDepth, _ = strconv.Atoi(depth)
	}
	
	if currentDepth >= MaxSubgraphDepth {
		return Outcome{Status: OutcomeFail}, fmt.Errorf(
			"subgraph nesting depth exceeded (%d levels) - possible circular reference", 
			MaxSubgraphDepth,
		)
	}
	
	// ... existing code ...
	
	// Pass incremented depth to child
	subCtx := pctx.Clone()
	subCtx.SetInternal("subgraph_depth", strconv.Itoa(currentDepth+1))
	
	engine := NewEngine(subGraphWithParams, h.registry, WithInitialContext(subCtx.Snapshot()))
	// ... rest of existing code ...
}
```

### Tests

Add to `pipeline/subgraph_test.go`:

```go
func TestSubgraphHandler_MaxDepthPreventsInfiniteRecursion(t *testing.T) {
	// Create a self-referencing subgraph
	selfRef := NewGraph("selfref")
	selfRef.AddNode(&Node{ID: "start", Shape: "Mdiamond"})
	selfRef.AddNode(&Node{
		ID: "recurse", 
		Shape: "tab", 
		Attrs: map[string]string{"subgraph_ref": "selfref"},
	})
	selfRef.AddNode(&Node{ID: "exit", Shape: "Msquare"})
	selfRef.AddEdge(&Edge{From: "start", To: "recurse"})
	selfRef.AddEdge(&Edge{From: "recurse", To: "exit"})
	
	registry := NewHandlerRegistry()
	registry.Register(NewStartHandler())
	registry.Register(NewSubgraphHandler(map[string]*Graph{"selfref": selfRef}, registry))
	
	engine := NewEngine(selfRef, registry)
	_, err := engine.Run(context.Background())
	
	if err == nil {
		t.Fatal("expected error for circular subgraph reference")
	}
	if !strings.Contains(err.Error(), "nesting depth exceeded") {
		t.Errorf("expected depth error, got: %v", err)
	}
}
```

### Commit Message

```
fix(pipeline): add max subgraph nesting depth to prevent stack overflow

Limit subgraph nesting to 32 levels (configurable via MaxSubgraphDepth const).
Prevents infinite recursion from circular references (A → B → A).

- Modified: pipeline/subgraph.go (depth tracking logic)
- Modified: pipeline/subgraph_test.go (new test case)

Closes robustness gap identified in feature audit.
```

---

## Task 3: Documentation Updates (Optional)

**Priority:** Low  
**Effort:** 30 minutes  
**Dependencies:** None

### Objective
Document edge cases, validation behavior, and best practices.

### Changes to README.md

Add new section after "Pipeline Features":

```markdown
## Validation & Best Practices

### Lint Rules

Tracker implements all 12 Dippin semantic lint rules (DIP101-DIP112):

| Rule | Description | Example |
|------|-------------|---------|
| DIP101 | Node only reachable via conditional edges | Node has no unconditional path from start |
| DIP102 | Routing node missing default edge | Node has conditionals but no fallback |
| DIP103 | Overlapping conditions | Multiple edges with identical `when` clause |
| DIP104 | Unbounded retry loop | Retry without `max_retries` or `fallback` |
| DIP105 | No success path to exit | All paths require conditions |
| DIP106 | Undefined variable in prompt | `${ctx.unknown}` referenced |
| DIP107 | Unused context write | Node writes key never read downstream |
| DIP108 | Unknown model/provider | Potentially invalid LLM configuration |
| DIP109 | Namespace collision | Subgraph param shadows reserved context key |
| DIP110 | Empty prompt on agent | Agent node missing `prompt` attribute |
| DIP111 | Tool without timeout | Tool command has no timeout specified |
| DIP112 | Reads key not produced upstream | Node reads key never written by predecessors |

Run `tracker validate [file]` to check for these issues before execution.

### Variable Interpolation Edge Cases

**Undefined Variables:**
- Lenient mode (default): `${ctx.undefined}` → `""` (empty string)
- Strict mode: `${ctx.undefined}` → error
- Lint rule DIP106 warns about undefined variables in prompts

**Escaping:**
```dippin
agent Demo
  prompt:
    Use \${literal} to avoid interpolation.
    Use ${ctx.actual} for real variable.
```

**Namespace Precedence:**
- `${params.*}` only available in subgraph children
- `${ctx.*}` available everywhere
- `${graph.*}` is read-only (workflow attributes)

### Subgraph Best Practices

**Param Namespacing:**
Avoid shadowing reserved context keys (`outcome`, `last_response`, etc.):

```dippin
# Bad - triggers DIP109 warning
subgraph Scanner
  params:
    outcome: pending  # Shadows ctx.outcome!

# Good - use distinct names
subgraph Scanner
  params:
    scan_mode: aggressive
```

**Circular References:**
Tracker prevents circular subgraph references automatically (max depth: 32 levels).

### Parallel Execution Gotchas

**Context Isolation:**
Parallel branches see a snapshot of context at fan-out time. Changes in one branch don't affect siblings:

```dippin
parallel FanOut -> A, B, C

agent A
  prompt: Write foo=bar
  # Sets foo=bar in A's isolated context

agent B
  prompt: Read ${ctx.foo}
  # ${ctx.foo} is empty! (B doesn't see A's writes)
```

Use `fan_in` to merge results back to main context.

**Resource Limits:**
No artificial limit on parallel branches. For 100+ branches, consider batching or sequential execution.

### Timeout Configuration

**Tool Timeouts:**
```dippin
tool RunTests
  command: pytest --slow
  timeout: 600  # 10 minutes (highly recommended)
```

Without timeout, runaway commands block forever (lint rule DIP111 warns).

**Agent Max Turns:**
```dippin
agent TaskSolver
  max_turns: 20  # Prevent infinite loops
```

Default is unlimited. Set limits for predictable execution.
```

### Commit Message

```
docs(readme): document validation rules and edge cases

Add sections for:
- All 12 Dippin lint rules (DIP101-DIP112)
- Variable interpolation edge cases
- Subgraph best practices
- Parallel execution gotchas
- Timeout configuration

Improves user awareness of validation system and common pitfalls.
```

---

## Execution Strategy

### Recommended Order

1. **Task 1: CLI Validation Command** (2 hours)
   - High impact, low risk
   - Achieves 100% spec compliance
   - User-facing feature

2. **Task 2: Max Subgraph Nesting** (1 hour)
   - Medium impact, low risk
   - Closes robustness gap
   - Prevents crashes

3. **Task 3: Documentation** (30 min)
   - Low impact, zero risk
   - Improves user experience
   - Can be done last

### Testing Checklist

After each task:

```bash
# Unit tests
go test ./...

# Specific package tests
go test ./pipeline/... -v
go test ./cmd/tracker/... -v

# Integration tests
go test ./pipeline/... -run Integration

# Build and smoke test
go build ./cmd/tracker
./tracker validate examples/consensus_task.dip
./tracker examples/consensus_task.dip --no-tui

# Regression check (run existing examples)
for file in examples/*.dip; do
  echo "Validating $file..."
  ./tracker validate "$file" || echo "FAILED: $file"
done
```

### Commit Strategy

One commit per task:

```bash
# Task 1
git add cmd/tracker/validate.go cmd/tracker/validate_test.go cmd/tracker/main.go README.md
git commit -m "feat(cli): add validate command for standalone pipeline validation"

# Task 2
git add pipeline/subgraph.go pipeline/subgraph_test.go
git commit -m "fix(pipeline): add max subgraph nesting depth to prevent stack overflow"

# Task 3
git add README.md
git commit -m "docs(readme): document validation rules and edge cases"
```

### Rollback Plan

If any task causes issues:

```bash
# Revert specific commit
git revert <commit-hash>

# Or reset to before task
git reset --hard HEAD~1

# Tests should always pass after any commit
go test ./...
```

---

## Success Criteria

### Task 1: CLI Validation
- [ ] `tracker validate` subcommand registered
- [ ] Structural validation runs and displays errors
- [ ] Semantic validation runs and displays errors
- [ ] Lint warnings display with DIPxxx codes
- [ ] `--strict` flag treats warnings as errors
- [ ] `--quiet` flag suppresses success messages
- [ ] Help text works: `tracker validate --help`
- [ ] Exit codes correct (0=success/warnings, 1=errors)
- [ ] All tests pass: `go test ./cmd/tracker/...`
- [ ] Manual testing validates all scenarios
- [ ] README documents new command with examples

### Task 2: Max Nesting
- [ ] MaxSubgraphDepth constant defined (32)
- [ ] Depth tracking via internal context key
- [ ] Error raised when depth exceeded
- [ ] Test case for circular reference
- [ ] Test case for deep nesting (>32 levels)
- [ ] All tests pass: `go test ./pipeline/...`
- [ ] No regressions in existing examples

### Task 3: Documentation
- [ ] All 12 lint rules documented in table
- [ ] Variable interpolation edge cases explained
- [ ] Subgraph best practices section added
- [ ] Parallel execution gotchas documented
- [ ] Timeout configuration examples provided
- [ ] Markdown renders correctly on GitHub
- [ ] No broken links

### Overall
- [ ] All unit tests pass: `go test ./...`
- [ ] All integration tests pass
- [ ] All existing examples validate successfully
- [ ] No regressions in functionality
- [ ] Code coverage maintained or improved
- [ ] Documentation complete and accurate

---

## Timeline

| Task | Start | Duration | End |
|------|-------|----------|-----|
| Task 1: CLI Validation | T+0h | 2h | T+2h |
| Task 2: Max Nesting | T+2h | 1h | T+3h |
| Task 3: Documentation | T+3h | 30m | T+3.5h |
| **Total** | | **3.5h** | |

Can be executed sequentially in a single session, or split across multiple sessions.

---

## Risk Mitigation

| Risk | Mitigation | Contingency |
|------|------------|-------------|
| CLI breaks existing usage | New subcommand, no changes to main flow | Can revert commit cleanly |
| Validation false positives | Use existing tested validation logic | Adjust lint rules if needed |
| Depth check breaks valid workflows | 32 level limit is extremely generous | Make constant configurable |
| Documentation inaccuracies | Cross-reference with code | Community can submit corrections |

All risks are low. No breaking changes to existing functionality.

---

## Post-Implementation Verification

After all tasks complete:

```bash
# Full test suite
go test ./... -v -race -cover

# Coverage report
go test ./pipeline/... -coverprofile=coverage.out
go tool cover -html=coverage.out

# Build and install
go install ./cmd/tracker

# Validate all examples
tracker validate examples/*.dip

# Run a full pipeline
tracker examples/consensus_task.dip

# Check help text
tracker validate --help
tracker --help
```

Expected results:
- ✅ All tests pass
- ✅ Coverage >90% for pipeline package
- ✅ All examples validate successfully
- ✅ Help text displays correctly
- ✅ No regressions in execution

---

## Questions & Support

If issues arise during implementation:

1. **Test failures:** Check git diff for unintended changes
2. **Import errors:** Run `go mod tidy`
3. **Build failures:** Ensure Go 1.25+ is installed
4. **Logic questions:** Refer to existing validation tests
5. **Spec ambiguity:** Check dippin-lang specification or skip feature

This plan is self-contained and can be executed independently.

---

**Status:** ✅ Ready for Implementation  
**Last Updated:** 2024-03-21  
**Estimated Completion:** 3.5 hours from start
