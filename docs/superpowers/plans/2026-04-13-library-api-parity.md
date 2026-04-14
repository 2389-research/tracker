# Library API Parity Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Expose backend selection, autopilot, token breakdowns, and validation through the tracker library API so consumers don't need CLI internals.

**Architecture:** Three tasks modifying `tracker.go` and `pipeline/handlers/registry.go`. Task 1 adds Config fields and threads them through. Task 2 adds token breakdowns to Result. Task 3 adds ValidateSource.

**Tech Stack:** Go 1.25, standard library.

**Spec:** `docs/superpowers/specs/2026-04-13-library-api-parity-design.md`

---

### Task 1: Add Backend, Autopilot, and Interviewer to Config (#59)

**Files:**
- Modify: `tracker.go` (Config struct, buildRegistry, Result struct)
- Test: `tracker_test.go`

- [ ] **Step 1: Add fields to Config**

In `tracker.go`, add to the `Config` struct after the existing `Context` field:

```go
	Backend     string              // "native" (default), "claude-code", "acp"; selects agent backend
	Autopilot   string              // "" (interactive), "lax", "mid", "hard", "mentor"; LLM-driven gate decisions
	AutoApprove bool                // auto-approve all human gates with default/first option
```

- [ ] **Step 2: Add Trace to Result**

In `tracker.go`, add to the `Result` struct:

```go
	Trace        *pipeline.Trace    // full execution trace (nodes, timing, stats)
```

- [ ] **Step 3: Thread Backend through buildRegistry**

In `buildRegistry`, after the existing registry options, add:

```go
	if cfg.Backend != "" {
		registryOpts = append(registryOpts, handlers.WithDefaultBackend(cfg.Backend))
	}
```

- [ ] **Step 4: Thread Autopilot/AutoApprove through buildRegistry**

This is more involved — the CLI's `chooseInterviewer` logic needs to be available to the library. Extract it into a shared function.

Add a new function in `tracker.go`:

```go
// resolveInterviewer determines the human gate handler from Config.
// Priority: AutoApprove > Autopilot > nil (engine default).
func resolveInterviewer(cfg Config, completer agent.Completer) handlers.FreeformInterviewer {
	if cfg.AutoApprove {
		return handlers.NewAutoApproveInterviewer()
	}
	if cfg.Autopilot != "" && completer != nil {
		persona, ok := handlers.ParseAutopilotPersona(cfg.Autopilot)
		if ok {
			return handlers.NewAutopilotInterviewer(completer, handlers.WithAutopilotPersona(persona))
		}
	}
	return nil
}
```

Then in `buildRegistry`, after the backend line:

```go
	interviewer := resolveInterviewer(cfg, completer)
	if interviewer != nil {
		registryOpts = append(registryOpts, handlers.WithInterviewer(interviewer, graph))
	}
```

Note: `buildRegistry` needs the `graph` parameter (already has it) and needs `completer` passed in. Update the signature.

- [ ] **Step 5: Populate Trace in Run result**

In the `Run` method of `Engine`, where `Result` is built from `EngineResult`, add:

```go
	result.Trace = engineResult.Trace
```

- [ ] **Step 6: Write tests**

Add to `tracker_test.go`:

```go
func TestConfig_BackendField(t *testing.T) {
	cfg := Config{Backend: "native"}
	if cfg.Backend != "native" {
		t.Errorf("Backend = %q, want native", cfg.Backend)
	}
}

func TestConfig_AutopilotField(t *testing.T) {
	cfg := Config{Autopilot: "mid", AutoApprove: false}
	if cfg.Autopilot != "mid" {
		t.Errorf("Autopilot = %q, want mid", cfg.Autopilot)
	}
}
```

- [ ] **Step 7: Verify build and tests**

Run: `go build ./... && go test ./... -short`

- [ ] **Step 8: Commit**

```bash
git commit -m "feat(lib): add Backend, Autopilot, AutoApprove to Config, Trace to Result (#59)"
```

---

### Task 2: Token/cost breakdowns in Result (#62)

**Files:**
- Modify: `tracker.go` (Result struct, Run method, buildEngine)
- Modify: `llm/token_tracker.go` (add AllProviderUsage method if needed)
- Test: `tracker_test.go`

- [ ] **Step 1: Add breakdown fields to Result**

```go
type Result struct {
	// ... existing fields ...
	TokensByProvider map[string]llm.Usage  // per-provider token totals
	ToolCallsByName  map[string]int        // tool call counts by name
}
```

- [ ] **Step 2: Add AllProviderUsage to TokenTracker**

In `llm/token_tracker.go`, add:

```go
// AllProviderUsage returns a copy of the per-provider usage map.
func (t *TokenTracker) AllProviderUsage() map[string]Usage {
	t.mu.RLock()
	defer t.mu.RUnlock()
	result := make(map[string]Usage, len(t.usage))
	for k, v := range t.usage {
		result[k] = v
	}
	return result
}
```

- [ ] **Step 3: Wire TokenTracker into library Engine**

The library currently doesn't create a `TokenTracker`. Add one internally:

In `tracker.go`, update the `Engine` struct:

```go
type Engine struct {
	inner        *pipeline.Engine
	client       *llm.Client
	tokenTracker *llm.TokenTracker
	closeOnce    sync.Once
	closeErr     error
}
```

In `buildEngine`, create a tracker and pass it to the registry:

```go
	tokenTracker := llm.NewTokenTracker()
	registryOpts = append(registryOpts, handlers.WithTokenTracker(tokenTracker))
```

Store it on the Engine: `tokenTracker: tokenTracker`

- [ ] **Step 4: Populate breakdowns in Run result**

In the `Run` method, after getting `EngineResult`, populate:

```go
	if e.tokenTracker != nil {
		result.TokensByProvider = e.tokenTracker.AllProviderUsage()
	}
	// Aggregate tool calls from trace
	if engineResult.Trace != nil {
		result.ToolCallsByName = engineResult.Trace.AggregateToolCalls()
	}
```

Add `AggregateToolCalls` to `pipeline/trace.go` if it doesn't exist:

```go
func (t *Trace) AggregateToolCalls() map[string]int {
	calls := make(map[string]int)
	for _, entry := range t.Entries {
		if entry.Stats != nil {
			for name, count := range entry.Stats.ToolCalls {
				calls[name] += count
			}
		}
	}
	return calls
}
```

- [ ] **Step 5: Write tests**

Test that `AllProviderUsage` returns correct data. Test that `AggregateToolCalls` sums across entries.

- [ ] **Step 6: Verify build and tests**

Run: `go build ./... && go test ./... -short`

- [ ] **Step 7: Commit**

```bash
git commit -m "feat(lib): add TokensByProvider, ToolCallsByName to Result (#62)"
```

---

### Task 3: ValidateSource API (#60)

**Files:**
- Modify: `tracker.go` (add ValidateSource function)
- Test: `tracker_test.go`

- [ ] **Step 1: Add ValidationResult type and ValidateSource function**

```go
// ValidationResult contains the outcome of pipeline validation.
type ValidationResult struct {
	Graph    *pipeline.Graph // parsed graph (nil on parse failure)
	Errors   []string        // blocking validation errors
	Warnings []string        // non-blocking warnings
	Hints    []string        // lint hints/suggestions
}

// ValidateOption configures validation behavior.
type ValidateOption func(*validateConfig)

type validateConfig struct {
	format string
}

// WithFormat sets the pipeline format for validation ("dip" or "dot").
func WithFormat(format string) ValidateOption {
	return func(c *validateConfig) { c.format = format }
}

// ValidateSource parses and validates a pipeline source string without executing it.
// Returns validation diagnostics from both dippin-lang and tracker validators.
func ValidateSource(source string, opts ...ValidateOption) (*ValidationResult, error) {
	cfg := &validateConfig{}
	for _, opt := range opts {
		opt(cfg)
	}

	graph, err := parsePipelineSource(source, cfg.format)
	if err != nil {
		return &ValidationResult{Errors: []string{err.Error()}}, err
	}

	result := &ValidationResult{Graph: graph}

	// Run tracker structural + semantic validation.
	if valErr := pipeline.ValidateAll(graph); valErr != nil {
		if ve, ok := valErr.(*pipeline.ValidationError); ok {
			result.Errors = ve.Errors()
			result.Warnings = ve.Warnings()
		} else {
			result.Errors = []string{valErr.Error()}
		}
	}

	return result, nil
}
```

Note: Check the actual `pipeline.ValidateAll` return type and adapt accordingly. The key is exposing structured diagnostics, not just a pass/fail bool.

- [ ] **Step 2: Write tests**

```go
func TestValidateSource_ValidDip(t *testing.T) {
	source := `workflow test
  start: s
  exit: e
  agent s
  agent e
  edges
    s -> e
`
	result, err := ValidateSource(source)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(result.Errors) > 0 {
		t.Errorf("unexpected errors: %v", result.Errors)
	}
	if result.Graph == nil {
		t.Error("expected non-nil graph")
	}
}

func TestValidateSource_InvalidSyntax(t *testing.T) {
	result, err := ValidateSource("not a pipeline")
	if err == nil {
		t.Fatal("expected error for invalid syntax")
	}
	if len(result.Errors) == 0 {
		t.Error("expected validation errors")
	}
}

func TestValidateSource_WithFormat(t *testing.T) {
	source := `workflow test
  start: s
  exit: e
  agent s
  agent e
  edges
    s -> e
`
	result, err := ValidateSource(source, WithFormat("dip"))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Graph == nil {
		t.Error("expected non-nil graph")
	}
}
```

- [ ] **Step 3: Verify build and tests**

Run: `go build ./... && go test ./... -short`

- [ ] **Step 4: Commit**

```bash
git commit -m "feat(lib): add ValidateSource for pipeline validation without execution (#60)"
```

---

## Task Dependency Graph

```text
Task 1 (Config fields + threading — foundational)
  └─ Task 2 (token breakdowns — uses Engine struct from Task 1)
Task 3 (ValidateSource — independent)
```

Task 1 should go first. Tasks 2 and 3 can be parallel after that.
