# Tracker Library API Design

## Goal

Add a top-level `tracker` package that lets consumers (primarily Mammoth) run pipelines (`.dip` or `.dot`) in one function call, without manually wiring LLM clients, handler registries, or execution environments.

## Problem

Using tracker as a library today requires importing 5+ packages and assembling ~15 lines of boilerplate:

```go
client, _ := llm.NewClientFromEnv(...)
env := exec.NewLocalEnvironment(workDir)
registry := handlers.NewDefaultRegistry(graph,
    handlers.WithLLMClient(client, workDir),
    handlers.WithExecEnvironment(env),
    handlers.WithAgentEventHandler(eventHandler),
)
engine := pipeline.NewEngine(graph, registry,
    pipeline.WithCheckpointPath(cpPath),
    pipeline.WithArtifactDir(artifactDir),
    pipeline.WithPipelineEventHandler(pipelineEvents),
)
result, err := engine.Run(ctx)
```

Mammoth (the primary consumer) wants to hand tracker a pipeline (`.dip` preferred, `.dot` deprecated) and say "run this."

## Design

### Package Location

New package at module root: `github.com/2389-research/tracker`. This is the only package Mammoth needs to import for pipeline execution.

### Public API

```go
package tracker

// Run parses a pipeline source (.dip or .dot), auto-wires all internals,
// executes, and returns the result. The format is specified via cfg.Format
// ("dip" preferred, "dot" deprecated; defaults to "dip").
// The LLM client is created from environment variables unless cfg.LLMClient
// is provided. All other dependencies are constructed automatically.
func Run(ctx context.Context, source string, cfg Config) (*Result, error)

// NewEngine parses a pipeline source and auto-wires all internals, returning
// an Engine for manual control. The caller must call Close() when done to
// release resources (e.g., auto-created LLM clients).
func NewEngine(source string, cfg Config) (*Engine, error)

// Engine wraps pipeline.Engine with auto-wired internals.
type Engine struct { /* unexported fields */ }

// Run executes the pipeline to completion.
func (e *Engine) Run(ctx context.Context) (*Result, error)

// Close releases resources. Must be called if the engine was created
// with NewEngine. Run() calls Close() automatically via tracker.Run().
func (e *Engine) Close() error

// Config controls pipeline execution. All fields are optional.
// Zero-value Config uses environment variables for LLM credentials,
// the current working directory, and auto-generated run directories.
type Config struct {
    Format        string                        // "dip" (default) or "dot" (deprecated)
    WorkingDir    string                        // default: os.Getwd()
    CheckpointDir string                        // default: .tracker/runs/<runID>/
    ArtifactDir   string                        // default: .tracker/runs/<runID>/
    Model         string                        // default: env or claude-sonnet-4-6
    Provider      string                        // default: auto-detect from env
    RetryPolicy   string                        // maps to named policies: "none" (default), "default", "aggressive"
    EventHandler  pipeline.PipelineEventHandler  // optional: live pipeline events
    AgentEvents   agent.EventHandler             // optional: live agent session events
    LLMClient     agent.Completer               // optional: override auto-created client
    Context       map[string]string             // optional: initial pipeline context
}

// Result contains the outcome of a pipeline execution.
// The underlying EngineResult is embedded for access to Trace and other
// advanced fields without breaking the simple top-level accessors.
type Result struct {
    RunID          string
    Status         string            // "success" or "fail"
    CompletedNodes []string
    Context        map[string]string
    EngineResult   *pipeline.EngineResult // full engine result for advanced use
}
```

### Auto-Wiring Sequence

When `Run` or `NewEngine` is called:

1. **Parse & Validate**: For `.dip` (default): parse via `dippin-lang` then `pipeline.FromDippinIR()`. For `.dot` (deprecated): `pipeline.ParseDOT(source)`. Then `pipeline.Validate(graph)`. Errors returned immediately.

2. **LLM Client**: If `cfg.LLMClient` is set, use it. Otherwise, `llm.NewClientFromEnv()` with all registered provider constructors (anthropic, openai, google). `cfg.Model` and `cfg.Provider` override env defaults. Invalid provider or model values surface as errors from `NewEngine`/`Run` at construction time — fail fast, not at first LLM call. Construction is purely synchronous (no I/O), so no `context.Context` is needed for `NewEngine`.

3. **Execution Environment**: `exec.NewLocalEnvironment(cfg.WorkingDir)`. Mammoth controls the Docker/sandbox layer above tracker.

4. **Handler Registry**: `handlers.NewDefaultRegistry(graph, ...)` with the auto-created client, environment, and event handlers wired in. Graph-level attributes are passed through automatically.

5. **Engine**: `pipeline.NewEngine(graph, registry, ...)` with checkpoint dir, artifact dir, event handler, and initial context from config.

6. **Cleanup**: `tracker.Run` closes the auto-created LLM client on return. `tracker.NewEngine` defers cleanup to `engine.Close()`.

### What Mammoth's Integration Looks Like

Before (with attractor):
```go
handlers := attractor.DefaultHandlerRegistry()
handlers = wrapRegistryWithInterviewer(handlers, &mcpInterviewer{run: run})
engineConfig := attractor.EngineConfig{
    CheckpointDir: run.CheckpointDir,
    ArtifactDir:   run.ArtifactDir,
    RunID:         run.ID,
    Handlers:      handlers,
    EventHandler:  newEventHandler(run),
    Backend:       backend,
    BaseURL:       run.Config.BaseURL,
    DefaultRetry:  retryPolicyFromName(run.Config.RetryPolicy),
}
engine := attractor.NewEngine(engineConfig)
result, err := engine.RunGraph(ctx, graph)
```

After (with tracker):
```go
result, err := tracker.Run(ctx, dipSource, tracker.Config{
    WorkingDir:    workDir,
    CheckpointDir: cpDir,
    ArtifactDir:   artifactDir,
    EventHandler:  newEventHandler(run),
    RetryPolicy:   run.Config.RetryPolicy,
})
```

## Scope Boundaries

- **No new features**: Pure convenience wrapper around existing internals.
- **No Interviewer support in v1**: Mammoth handles human gates by wrapping handlers. A `cfg.Interviewer` field can be added later.
- **No run management**: Mammoth manages run IDs, status tracking, persistence. Tracker executes and returns.
- **Existing packages unchanged**: `llm/`, `agent/`, `pipeline/` keep current APIs. The root package is additive only.
- **Result is a thin wrapper**: Exposes the same data as `pipeline.EngineResult` with a flatter shape.

## Testing

- Unit tests for `Run` and `NewEngine` using stub `Completer` implementations (passed via `cfg.LLMClient`) to avoid real API calls
- Test that zero-value Config produces valid defaults
- Test that `cfg.LLMClient` override skips env detection
- Test that `Close()` is idempotent
- Integration test: parse a simple `.dip`, run with a stub handler, verify result
- Integration test: parse a simple `.dot` (deprecated path), verify backward compat
- Test invalid source returns parse error
- Test invalid provider/model returns construction error

## Files

- Create: `tracker.go` — `Run`, `NewEngine`, `Engine`, `Config`, `Result`
- Create: `tracker_test.go` — tests
- No modifications to existing packages
