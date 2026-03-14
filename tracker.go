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
	inner  *pipeline.Engine
	client *llm.Client // nil if caller provided their own Completer
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
