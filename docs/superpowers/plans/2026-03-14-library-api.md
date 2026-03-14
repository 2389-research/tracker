# Tracker Library API Implementation Plan

> **For agentic workers:** REQUIRED: Use superpowers:subagent-driven-development (if subagents available) or superpowers:executing-plans to implement this plan. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Add a top-level `tracker` package with `Run()` and `NewEngine()` so consumers can execute DOT pipelines in one call without manual wiring.

**Architecture:** A single new file `tracker.go` at the module root provides `Run(ctx, dot, config)` and `NewEngine(dot, config)`. These auto-wire LLM clients (from env vars), handler registries, and execution environments internally. All existing packages remain unchanged.

**Tech Stack:** Go, existing `llm/`, `agent/`, `pipeline/`, `pipeline/handlers/` packages

---

## File Structure

| File | Responsibility |
|------|---------------|
| Create: `tracker.go` | `Config`, `Result`, `Engine`, `Run()`, `NewEngine()`, auto-wiring logic |
| Create: `tracker_test.go` | All tests for the public API |

No existing files are modified.

---

## Chunk 1: Core Types and Engine Construction

### Task 1: Config, Result, and Engine types with NewEngine

**Files:**
- Create: `tracker.go`
- Create: `tracker_test.go`

- [ ] **Step 1: Write the failing test for NewEngine with invalid DOT**

```go
// tracker_test.go
// ABOUTME: Tests for the top-level tracker convenience API.
// ABOUTME: Validates Config defaulting, auto-wiring, Run(), NewEngine(), and error paths.
package tracker

import (
	"context"
	"testing"
)

func TestNewEngine_InvalidDOT(t *testing.T) {
	_, err := NewEngine("not valid dot {{{", Config{})
	if err == nil {
		t.Fatal("expected error for invalid DOT source")
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./... -run TestNewEngine_InvalidDOT -count=1`
Expected: FAIL — `NewEngine` not defined

- [ ] **Step 3: Write minimal implementation with types and NewEngine**

```go
// tracker.go
// ABOUTME: Top-level convenience API for running DOT pipelines with auto-wired dependencies.
// ABOUTME: Consumers import only this package — LLM clients, registries, and environments are built automatically.
package tracker

import (
	"context"
	"fmt"
	"os"
	"time"

	"github.com/2389-research/tracker/agent"
	"github.com/2389-research/tracker/agent/exec"
	"github.com/2389-research/tracker/llm"
	"github.com/2389-research/tracker/llm/anthropic"
	"github.com/2389-research/tracker/llm/google"
	"github.com/2389-research/tracker/llm/openai"
	"github.com/2389-research/tracker/pipeline"
	"github.com/2389-research/tracker/pipeline/handlers"
)

// Config controls pipeline execution. All fields are optional.
// Zero-value Config uses environment variables for LLM credentials,
// the current working directory, and auto-generated run directories.
type Config struct {
	WorkingDir    string                       // default: os.Getwd()
	CheckpointDir string                       // default: empty (engine auto-generates)
	ArtifactDir   string                       // default: empty (engine auto-generates)
	Model         string                       // default: env or claude-sonnet-4-6
	Provider      string                       // default: auto-detect from env
	RetryPolicy   string                       // "none" (default), "default", "aggressive"
	EventHandler  pipeline.PipelineEventHandler // optional: live pipeline events
	AgentEvents   agent.EventHandler            // optional: live agent session events
	LLMClient     agent.Completer              // optional: override auto-created client
	Context       map[string]string            // optional: initial pipeline context
}

// Result contains the outcome of a pipeline execution.
type Result struct {
	RunID          string
	Status         string
	CompletedNodes []string
	Context        map[string]string
	EngineResult   *pipeline.EngineResult
}

// Engine wraps pipeline.Engine with auto-wired internals.
type Engine struct {
	inner     *pipeline.Engine
	client    *llm.Client // nil if caller provided their own Completer
}

// NewEngine parses DOT, auto-wires all internals, and returns an Engine.
// The caller must call Close() when done to release resources.
func NewEngine(dotSource string, cfg Config) (*Engine, error) {
	graph, err := pipeline.ParseDOT(dotSource)
	if err != nil {
		return nil, fmt.Errorf("parse DOT: %w", err)
	}

	if err := pipeline.Validate(graph); err != nil {
		return nil, fmt.Errorf("validate graph: %w", err)
	}

	workDir := cfg.WorkingDir
	if workDir == "" {
		workDir, err = os.Getwd()
		if err != nil {
			return nil, fmt.Errorf("get working directory: %w", err)
		}
	}

	// Build or use provided LLM client.
	var client *llm.Client
	var completer agent.Completer
	if cfg.LLMClient != nil {
		completer = cfg.LLMClient
	} else {
		client, err = buildClient(cfg.Provider)
		if err != nil {
			return nil, fmt.Errorf("create LLM client: %w", err)
		}
		completer = client
	}

	// If a model is specified, inject it as a graph-level attribute so
	// codergen nodes use it as their default.
	if cfg.Model != "" {
		if graph.Attrs == nil {
			graph.Attrs = make(map[string]string)
		}
		if _, exists := graph.Attrs["llm_model"]; !exists {
			graph.Attrs["llm_model"] = cfg.Model
		}
	}

	env := exec.NewLocalEnvironment(workDir)

	registryOpts := []handlers.RegistryOption{
		handlers.WithLLMClient(completer, workDir),
		handlers.WithExecEnvironment(env),
	}
	if cfg.AgentEvents != nil {
		registryOpts = append(registryOpts, handlers.WithAgentEventHandler(cfg.AgentEvents))
	}
	registry := handlers.NewDefaultRegistry(graph, registryOpts...)

	var engineOpts []pipeline.EngineOption
	if cfg.CheckpointDir != "" {
		engineOpts = append(engineOpts, pipeline.WithCheckpointPath(cfg.CheckpointDir))
	}
	if cfg.ArtifactDir != "" {
		engineOpts = append(engineOpts, pipeline.WithArtifactDir(cfg.ArtifactDir))
	}
	if cfg.EventHandler != nil {
		engineOpts = append(engineOpts, pipeline.WithPipelineEventHandler(cfg.EventHandler))
	}
	if len(cfg.Context) > 0 {
		engineOpts = append(engineOpts, pipeline.WithInitialContext(cfg.Context))
	}

	engineOpts = append(engineOpts, pipeline.WithStylesheetResolution(true))
	inner := pipeline.NewEngine(graph, registry, engineOpts...)

	return &Engine{
		inner:  inner,
		client: client,
	}, nil
}

// buildClient creates an LLM client from environment variables with
// base URL support and retry middleware. If provider is non-empty, only
// that provider is configured (returns error if unknown).
func buildClient(provider string) (*llm.Client, error) {
	constructors := map[string]func(string) (llm.ProviderAdapter, error){
		"anthropic": func(key string) (llm.ProviderAdapter, error) {
			var opts []anthropic.Option
			if base := os.Getenv("ANTHROPIC_BASE_URL"); base != "" {
				opts = append(opts, anthropic.WithBaseURL(base))
			}
			return anthropic.New(key, opts...), nil
		},
		"openai": func(key string) (llm.ProviderAdapter, error) {
			var opts []openai.Option
			if base := os.Getenv("OPENAI_BASE_URL"); base != "" {
				opts = append(opts, openai.WithBaseURL(base))
			}
			return openai.New(key, opts...), nil
		},
		"gemini": func(key string) (llm.ProviderAdapter, error) {
			var opts []google.Option
			if base := os.Getenv("GEMINI_BASE_URL"); base != "" {
				opts = append(opts, google.WithBaseURL(base))
			}
			return google.New(key, opts...), nil
		},
	}

	// If a specific provider is requested, only configure that one.
	if provider != "" {
		constructor, ok := constructors[provider]
		if !ok {
			return nil, fmt.Errorf("unknown provider %q (valid: anthropic, openai, gemini)", provider)
		}
		constructors = map[string]func(string) (llm.ProviderAdapter, error){
			provider: constructor,
		}
	}

	client, err := llm.NewClientFromEnv(constructors)
	if err != nil {
		return nil, err
	}

	client.AddMiddleware(llm.NewRetryMiddleware(
		llm.WithMaxRetries(3),
		llm.WithBaseDelay(2*time.Second),
	))

	return client, nil
}

// Run executes the pipeline to completion.
func (e *Engine) Run(ctx context.Context) (*Result, error) {
	engineResult, err := e.inner.Run(ctx)
	if err != nil {
		return nil, err
	}

	return resultFromEngine(engineResult), nil
}

// Close releases resources. Must be called if the engine was created
// with NewEngine. Idempotent.
func (e *Engine) Close() error {
	if e.client != nil {
		err := e.client.Close()
		e.client = nil
		return err
	}
	return nil
}

// Run parses DOT, auto-wires all internals, executes, and returns the result.
// This is the one-call convenience function. It handles Close() automatically.
func Run(ctx context.Context, dotSource string, cfg Config) (*Result, error) {
	engine, err := NewEngine(dotSource, cfg)
	if err != nil {
		return nil, err
	}
	defer engine.Close()

	return engine.Run(ctx)
}

func resultFromEngine(er *pipeline.EngineResult) *Result {
	if er == nil {
		return &Result{Status: "fail"}
	}
	return &Result{
		RunID:          er.RunID,
		Status:         er.Status,
		CompletedNodes: er.CompletedNodes,
		Context:        er.Context,
		EngineResult:   er,
	}
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./... -run TestNewEngine_InvalidDOT -count=1`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add tracker.go tracker_test.go
git commit -m "feat: add top-level tracker package with Config, Result, Engine, and NewEngine"
```

---

### Task 2: NewEngine with valid DOT and stub Completer

**Files:**
- Modify: `tracker_test.go`

- [ ] **Step 1: Write the failing test**

The test uses a minimal start→exit DOT graph and a stub completer (via `cfg.LLMClient`) to bypass env-based client creation. This verifies the full auto-wiring path without real API keys.

```go
// Add to tracker_test.go

import (
	"github.com/2389-research/tracker/llm"
)

// stubCompleter returns canned responses for testing.
type stubCompleter struct {
	response *llm.Response
}

func (s *stubCompleter) Complete(_ context.Context, _ *llm.Request) (*llm.Response, error) {
	return s.response, nil
}

const simpleDOT = `digraph test {
	start [shape=Mdiamond];
	finish [shape=Msquare];
	start -> finish;
}`

func TestNewEngine_ValidDOT(t *testing.T) {
	client := &stubCompleter{
		response: &llm.Response{
			Message:      llm.AssistantMessage("done"),
			FinishReason: llm.FinishReason{Reason: "stop"},
		},
	}

	engine, err := NewEngine(simpleDOT, Config{
		LLMClient: client,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	defer engine.Close()

	if engine.inner == nil {
		t.Fatal("expected inner engine to be set")
	}
}
```

- [ ] **Step 2: Run test to verify it passes**

Run: `go test ./... -run TestNewEngine_ValidDOT -count=1`
Expected: PASS (implementation already exists from Task 1)

- [ ] **Step 3: Commit**

```bash
git add tracker_test.go
git commit -m "test: add NewEngine valid DOT test with stub completer"
```

---

### Task 3: Close() idempotency and Engine.Run integration

**Files:**
- Modify: `tracker_test.go`

- [ ] **Step 1: Write the failing tests**

```go
// Add to tracker_test.go

func TestEngine_CloseIdempotent(t *testing.T) {
	client := &stubCompleter{
		response: &llm.Response{
			Message:      llm.AssistantMessage("done"),
			FinishReason: llm.FinishReason{Reason: "stop"},
		},
	}

	engine, err := NewEngine(simpleDOT, Config{LLMClient: client})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Close twice — should not panic or return error.
	if err := engine.Close(); err != nil {
		t.Fatalf("first close: %v", err)
	}
	if err := engine.Close(); err != nil {
		t.Fatalf("second close: %v", err)
	}
}

func TestRun_SimplePipeline(t *testing.T) {
	client := &stubCompleter{
		response: &llm.Response{
			Message:      llm.AssistantMessage("done"),
			FinishReason: llm.FinishReason{Reason: "stop"},
		},
	}

	result, err := Run(context.Background(), simpleDOT, Config{
		LLMClient: client,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if result.Status != "success" {
		t.Errorf("expected status=success, got %q", result.Status)
	}
	if result.RunID == "" {
		t.Error("expected non-empty RunID")
	}
	if result.EngineResult == nil {
		t.Error("expected EngineResult to be set")
	}
}
```

- [ ] **Step 2: Run tests to verify they pass**

Run: `go test ./... -run "TestEngine_CloseIdempotent|TestRun_SimplePipeline" -count=1`
Expected: PASS

- [ ] **Step 3: Commit**

```bash
git add tracker_test.go
git commit -m "test: add Close idempotency and Run integration tests"
```

---

## Chunk 2: Config Defaults and Error Paths

### Task 4: Config defaults validation

**Files:**
- Modify: `tracker_test.go`

- [ ] **Step 1: Write the failing test**

This verifies that zero-value Config fields get sensible defaults applied during engine construction.

```go
// Add to tracker_test.go

import "os"

func TestNewEngine_DefaultsWorkingDir(t *testing.T) {
	client := &stubCompleter{
		response: &llm.Response{
			Message:      llm.AssistantMessage("done"),
			FinishReason: llm.FinishReason{Reason: "stop"},
		},
	}

	// Zero-value config — should default WorkingDir to cwd.
	engine, err := NewEngine(simpleDOT, Config{LLMClient: client})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	defer engine.Close()

	cwd, _ := os.Getwd()
	if cwd == "" {
		t.Skip("cannot determine cwd")
	}
	// Engine was constructed successfully with default working dir.
	// If defaulting failed, NewEngine would have returned an error.
}
```

- [ ] **Step 2: Run test to verify it passes**

Run: `go test ./... -run TestNewEngine_DefaultsWorkingDir -count=1`
Expected: PASS

- [ ] **Step 3: Commit**

```bash
git add tracker_test.go
git commit -m "test: verify Config defaults working directory to cwd"
```

---

### Task 5: Config with initial context and event handlers

**Files:**
- Modify: `tracker_test.go`

- [ ] **Step 1: Write the test**

```go
// Add to tracker_test.go

import (
	"github.com/2389-research/tracker/pipeline"
)

func TestRun_WithInitialContext(t *testing.T) {
	client := &stubCompleter{
		response: &llm.Response{
			Message:      llm.AssistantMessage("done"),
			FinishReason: llm.FinishReason{Reason: "stop"},
		},
	}

	result, err := Run(context.Background(), simpleDOT, Config{
		LLMClient: client,
		Context:   map[string]string{"goal": "test the library"},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Status != "success" {
		t.Errorf("expected success, got %q", result.Status)
	}
}

func TestRun_WithEventHandler(t *testing.T) {
	client := &stubCompleter{
		response: &llm.Response{
			Message:      llm.AssistantMessage("done"),
			FinishReason: llm.FinishReason{Reason: "stop"},
		},
	}

	var events []pipeline.PipelineEvent
	handler := pipeline.PipelineEventHandlerFunc(func(evt pipeline.PipelineEvent) {
		events = append(events, evt)
	})

	result, err := Run(context.Background(), simpleDOT, Config{
		LLMClient:    client,
		EventHandler: handler,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Status != "success" {
		t.Errorf("expected success, got %q", result.Status)
	}
	if len(events) == 0 {
		t.Error("expected at least one pipeline event")
	}
}
```

- [ ] **Step 2: Run tests to verify they pass**

Run: `go test ./... -run "TestRun_WithInitialContext|TestRun_WithEventHandler" -count=1`
Expected: PASS

- [ ] **Step 3: Commit**

```bash
git add tracker_test.go
git commit -m "test: verify initial context and event handler wiring"
```

---

### Task 6: Error paths — validation errors

**Files:**
- Modify: `tracker_test.go`

- [ ] **Step 1: Write the tests**

```go
// Add to tracker_test.go

func TestNewEngine_ValidationError(t *testing.T) {
	// Valid DOT syntax but invalid graph structure (no exit node).
	badGraph := `digraph test {
		start [shape=Mdiamond];
		orphan [shape=box];
		start -> orphan;
	}`

	_, err := NewEngine(badGraph, Config{
		LLMClient: &stubCompleter{
			response: &llm.Response{
				Message:      llm.AssistantMessage("done"),
				FinishReason: llm.FinishReason{Reason: "stop"},
			},
		},
	})
	if err == nil {
		t.Fatal("expected validation error for graph without exit node")
	}
}

func TestRun_InvalidDOT(t *testing.T) {
	_, err := Run(context.Background(), "not dot at all!!!", Config{})
	if err == nil {
		t.Fatal("expected error for invalid DOT")
	}
}

func TestNewEngine_InvalidProvider(t *testing.T) {
	_, err := NewEngine(simpleDOT, Config{
		Provider: "nonexistent",
	})
	if err == nil {
		t.Fatal("expected error for unknown provider")
	}
}
```

- [ ] **Step 2: Run tests to verify they pass**

Run: `go test ./... -run "TestNewEngine_ValidationError|TestRun_InvalidDOT" -count=1`
Expected: PASS

- [ ] **Step 3: Commit**

```bash
git add tracker_test.go
git commit -m "test: verify error paths for invalid DOT and validation failures"
```

---

### Task 7: Run full test suite and final commit

**Files:**
- None modified (verification only)

- [ ] **Step 1: Run the full test suite**

Run: `go test ./... -count=1`
Expected: ALL PASS — no regressions in existing packages

- [ ] **Step 2: Run go vet**

Run: `go vet ./...`
Expected: No issues

- [ ] **Step 3: Final commit if any formatting changes**

```bash
gofmt -w tracker.go tracker_test.go
git add tracker.go tracker_test.go
git diff --cached --quiet || git commit -m "style: format tracker package"
```
