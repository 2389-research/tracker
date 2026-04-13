// ABOUTME: Top-level convenience API for running pipelines (.dip preferred, .dot deprecated) with auto-wired dependencies.
// ABOUTME: Consumers import only this package — LLM clients, registries, and environments are built automatically.
package tracker

import (
	"context"
	"fmt"
	"log"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/2389-research/dippin-lang/parser"
	"github.com/2389-research/dippin-lang/validator"
	"github.com/2389-research/tracker/agent"
	"github.com/2389-research/tracker/agent/exec"
	"github.com/2389-research/tracker/llm"
	"github.com/2389-research/tracker/llm/anthropic"
	"github.com/2389-research/tracker/llm/google"
	"github.com/2389-research/tracker/llm/openai"
	"github.com/2389-research/tracker/llm/openaicompat"
	"github.com/2389-research/tracker/pipeline"
	"github.com/2389-research/tracker/pipeline/handlers"
)

// Pipeline format identifiers.
const (
	FormatDip = "dip" // Dippin format (current, default)
	FormatDOT = "dot" // DOT/Graphviz format (deprecated)
)

// Config controls pipeline execution. All fields are optional.
// Zero-value Config uses environment variables for LLM credentials,
// the current working directory, and auto-generated run directories.
type Config struct {
	WorkingDir    string                        // default: os.Getwd()
	CheckpointDir string                        // default: empty (engine auto-generates)
	ArtifactDir   string                        // default: empty (engine auto-generates)
	Format        string                        // "dip" (default), "dot" (deprecated); empty = auto-detect
	Model         string                        // default: env or claude-sonnet-4-6; graph-level attrs take precedence
	Provider      string                        // default: auto-detect from env
	RetryPolicy   string                        // "none" (default), "standard", "aggressive"; graph-level attrs take precedence
	EventHandler  pipeline.PipelineEventHandler // optional: live pipeline events
	AgentEvents   agent.EventHandler            // optional: live agent session events
	LLMClient     agent.Completer               // optional: override auto-created client
	Context       map[string]string             // optional: initial pipeline context
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
	closeOnce sync.Once
	closeErr  error
}

// NewEngine parses a pipeline source (.dip preferred, DOT deprecated),
// auto-wires all internals, and returns an Engine.
// Format is auto-detected from content if Config.Format is empty:
// sources starting with "digraph" or "strict digraph" are treated as DOT,
// everything else as .dip.
// The caller must call Close() when done to release resources.
func NewEngine(source string, cfg Config) (*Engine, error) {
	graph, err := parsePipelineSource(source, cfg.Format)
	if err != nil {
		return nil, err
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

	client, completer, err := resolveCompleter(cfg)
	if err != nil {
		return nil, err
	}

	// Clean up the auto-created client if anything below fails.
	built := false
	defer func() {
		if !built && client != nil {
			client.Close()
		}
	}()

	injectGraphDefaults(graph, cfg)

	registry := buildRegistry(graph, completer, workDir, cfg)
	engineOpts := buildEngineOpts(cfg)
	inner := pipeline.NewEngine(graph, registry, engineOpts...)

	built = true
	return &Engine{
		inner:  inner,
		client: client,
	}, nil
}

// resolveCompleter returns the LLM client and completer, building a client from env if needed.
func resolveCompleter(cfg Config) (*llm.Client, agent.Completer, error) {
	if cfg.LLMClient != nil {
		return nil, cfg.LLMClient, nil
	}
	client, err := buildClient(cfg.Provider)
	if err != nil {
		return nil, nil, fmt.Errorf("create LLM client: %w", err)
	}
	return client, client, nil
}

// injectGraphDefaults sets model, provider, and retry policy as graph-level attrs
// when specified in Config and not already present in the graph.
func injectGraphDefaults(graph *pipeline.Graph, cfg Config) {
	injectGraphAttrIfAbsent(graph, "llm_model", cfg.Model)
	injectGraphAttrIfAbsent(graph, "llm_provider", cfg.Provider)
	injectGraphAttrIfAbsent(graph, "default_retry_policy", cfg.RetryPolicy)
}

// injectGraphAttrIfAbsent sets a graph attribute only when value is non-empty and the key is not already set.
func injectGraphAttrIfAbsent(graph *pipeline.Graph, key, value string) {
	if value == "" {
		return
	}
	if graph.Attrs == nil {
		graph.Attrs = make(map[string]string)
	}
	if _, exists := graph.Attrs[key]; !exists {
		graph.Attrs[key] = value
	}
}

// buildRegistry creates a handler registry with all dependencies wired.
func buildRegistry(graph *pipeline.Graph, completer agent.Completer, workDir string, cfg Config) *pipeline.HandlerRegistry {
	env := exec.NewLocalEnvironment(workDir)
	registryOpts := []handlers.RegistryOption{
		handlers.WithLLMClient(completer, workDir),
		handlers.WithExecEnvironment(env),
	}
	if cfg.AgentEvents != nil {
		registryOpts = append(registryOpts, handlers.WithAgentEventHandler(cfg.AgentEvents))
	}
	if cfg.EventHandler != nil {
		registryOpts = append(registryOpts, handlers.WithPipelineEventHandler(cfg.EventHandler))
	}
	return handlers.NewDefaultRegistry(graph, registryOpts...)
}

// buildEngineOpts constructs engine options from Config.
func buildEngineOpts(cfg Config) []pipeline.EngineOption {
	var opts []pipeline.EngineOption
	if cfg.CheckpointDir != "" {
		opts = append(opts, pipeline.WithCheckpointPath(cfg.CheckpointDir))
	}
	if cfg.ArtifactDir != "" {
		opts = append(opts, pipeline.WithArtifactDir(cfg.ArtifactDir))
	}
	if cfg.EventHandler != nil {
		opts = append(opts, pipeline.WithPipelineEventHandler(cfg.EventHandler))
	}
	if len(cfg.Context) > 0 {
		opts = append(opts, pipeline.WithInitialContext(cfg.Context))
	}
	opts = append(opts, pipeline.WithStylesheetResolution(true))
	return opts
}

// parsePipelineSource parses a pipeline source string using the given format.
// If format is empty, auto-detects: DOT sources start with "digraph" or
// "strict digraph"; everything else is treated as .dip.
func parsePipelineSource(source, format string) (*pipeline.Graph, error) {
	if format == "" {
		format = detectSourceFormat(source)
	}

	switch format {
	case "dot":
		return parseDOTSource(source)
	case "dip":
		return parseDIPSource(source)
	default:
		return nil, fmt.Errorf("unknown format %q (valid: dip, dot)", format)
	}
}

// detectSourceFormat returns "dot" for DOT-syntax sources and "dip" otherwise.
func detectSourceFormat(source string) string {
	trimmed := strings.TrimSpace(source)
	if strings.HasPrefix(trimmed, "digraph") || strings.HasPrefix(trimmed, "strict digraph") {
		return "dot"
	}
	return "dip"
}

// parseDOTSource parses a DOT-format pipeline source.
func parseDOTSource(source string) (*pipeline.Graph, error) {
	log.Println("WARNING: DOT format is deprecated. Migrate pipelines to .dip format.")
	graph, err := pipeline.ParseDOT(source)
	if err != nil {
		return nil, fmt.Errorf("parse DOT: %w", err)
	}
	return graph, nil
}

// parseDIPSource parses a Dippin-format pipeline source, runs validation and lint.
func parseDIPSource(source string) (*pipeline.Graph, error) {
	p := parser.NewParser(source, "inline.dip")
	wf, err := p.Parse()
	if err != nil {
		return nil, fmt.Errorf("parse pipeline: %w", err)
	}
	valResult := validator.Validate(wf)
	if valResult.HasErrors() {
		for _, d := range valResult.Diagnostics {
			log.Println(d.String())
		}
		return nil, fmt.Errorf("%d validation error(s)", len(valResult.Errors()))
	}
	lintResult := validator.Lint(wf)
	for _, d := range lintResult.Diagnostics {
		log.Println(d.String())
	}
	graph, err := pipeline.FromDippinIR(wf)
	if err != nil {
		return nil, fmt.Errorf("convert pipeline IR: %w", err)
	}
	return graph, nil
}

// buildClient creates an LLM client from environment variables with
// base URL support and retry middleware. If provider is non-empty, only
// that provider is configured (returns error if unknown).
func buildClient(provider string) (*llm.Client, error) {
	constructors := allProviderConstructors()

	if provider != "" {
		constructor, ok := constructors[provider]
		if !ok {
			return nil, fmt.Errorf("unknown provider %q (valid: anthropic, openai, gemini, openai-compat)", provider)
		}
		constructors = map[string]func(string) (llm.ProviderAdapter, error){
			provider: constructor,
		}
	}

	client, err := llm.NewClientFromEnv(constructors)
	if err != nil {
		return nil, err
	}

	// LLM transport retries handle transient API errors (rate limits, 5xx).
	client.AddMiddleware(llm.NewRetryMiddleware(
		llm.WithMaxRetries(3),
		llm.WithBaseDelay(2*time.Second),
	))

	return client, nil
}

// allProviderConstructors returns the full map of provider constructor functions.
func allProviderConstructors() map[string]func(string) (llm.ProviderAdapter, error) {
	return map[string]func(string) (llm.ProviderAdapter, error){
		"anthropic":     newAnthropicAdapter,
		"openai":        newOpenAIAdapter,
		"gemini":        newGeminiAdapter,
		"openai-compat": newOpenAICompatAdapter,
	}
}

func newAnthropicAdapter(key string) (llm.ProviderAdapter, error) {
	var opts []anthropic.Option
	if base := os.Getenv("ANTHROPIC_BASE_URL"); base != "" {
		opts = append(opts, anthropic.WithBaseURL(base))
	}
	return anthropic.New(key, opts...), nil
}

func newOpenAIAdapter(key string) (llm.ProviderAdapter, error) {
	var opts []openai.Option
	if base := os.Getenv("OPENAI_BASE_URL"); base != "" {
		opts = append(opts, openai.WithBaseURL(base))
	}
	return openai.New(key, opts...), nil
}

func newGeminiAdapter(key string) (llm.ProviderAdapter, error) {
	var opts []google.Option
	if base := os.Getenv("GEMINI_BASE_URL"); base != "" {
		opts = append(opts, google.WithBaseURL(base))
	}
	return google.New(key, opts...), nil
}

func newOpenAICompatAdapter(key string) (llm.ProviderAdapter, error) {
	var opts []openaicompat.Option
	if base := os.Getenv("OPENAI_COMPAT_BASE_URL"); base != "" {
		opts = append(opts, openaicompat.WithBaseURL(base))
	}
	return openaicompat.New(key, opts...), nil
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
// with NewEngine. Safe for concurrent use; idempotent.
func (e *Engine) Close() error {
	e.closeOnce.Do(func() {
		if e.client != nil {
			e.closeErr = e.client.Close()
		}
	})
	return e.closeErr
}

// Run parses a pipeline source, auto-wires all internals, executes, and returns the result.
// This is the one-call convenience function. It handles Close() automatically.
func Run(ctx context.Context, source string, cfg Config) (*Result, error) {
	engine, err := NewEngine(source, cfg)
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
