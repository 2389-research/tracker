// ABOUTME: Top-level convenience API for running pipelines (.dip preferred, .dot deprecated) with auto-wired dependencies.
// ABOUTME: Consumers import only this package — LLM clients, registries, and environments are built automatically.
package tracker

import (
	"context"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

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
	CheckpointDir string                        // checkpoint file path (checkpoint.json); default: empty (engine auto-generates)
	ResumeRunID   string                        // optional: resume a previous run by ID or unique prefix; resolved via ResolveCheckpoint
	ArtifactDir   string                        // default: empty (engine auto-generates)
	Format        string                        // "dip" (default), "dot" (deprecated); empty = auto-detect
	Model         string                        // default: env or claude-sonnet-4-6; graph-level attrs take precedence
	Provider      string                        // default: auto-detect from env
	RetryPolicy   string                        // "none" (default), "standard", "aggressive"; graph-level attrs take precedence
	EventHandler  pipeline.PipelineEventHandler // optional: live pipeline events
	AgentEvents   agent.EventHandler            // optional: live agent session events
	LLMClient     agent.Completer               // optional: override auto-created client
	Context       map[string]string             // optional: initial pipeline context
	Params        map[string]string             // optional: override declared workflow params (keys without "params." prefix)
	Backend       string                        // "native" (default), "claude-code", "acp"; selects agent backend
	Autopilot     string                        // "" (interactive), "lax", "mid", "hard", "mentor"; LLM-driven gate decisions
	AutoApprove   bool                          // auto-approve all human gates with default/first option
	Budget        pipeline.BudgetLimits         // configures pipeline-level token, cost, and wall-time ceilings
	// GatewayURL is the root URL of a Cloudflare AI Gateway (or any compatible
	// proxy). When non-empty it is used as the base for all provider URLs, with
	// the per-provider suffix appended (e.g. "<gateway>/anthropic"). A
	// per-provider *_BASE_URL env var always takes precedence over GatewayURL so
	// library callers can still override individual providers. The TRACKER_GATEWAY_URL
	// env var is the fallback when GatewayURL is empty.
	GatewayURL  string
	WebhookGate *WebhookGateConfig // optional: post human gates to an HTTP webhook and wait for callback
}

// WebhookGateConfig controls headless webhook-based human gate handling.
// When set, human gate prompts are POSTed to WebhookURL and the pipeline
// waits for a callback POST to the local callback server.
type WebhookGateConfig struct {
	WebhookURL    string        // required: URL to post gate payloads to
	CallbackAddr  string        // local listen addr for callback server (default: :8789)
	Timeout       time.Duration // wait timeout per gate (default: 10m)
	TimeoutAction string        // "fail" (default) or "success" on timeout
	AuthHeader    string        // Authorization header for outbound requests
	RunID         string        // optional: run ID embedded in gate payloads
}

// CostReport summarizes spend for a pipeline run.
// TotalUSD is the sum of ByProvider[*].USD.
// LimitsHit names the budget dimensions that halted the run (empty when the
// run completed normally).
type CostReport struct {
	TotalUSD   float64
	ByProvider map[string]llm.ProviderCost
	LimitsHit  []string
}

// Result contains the outcome of a pipeline execution.
type Result struct {
	RunID            string
	Status           string
	CompletedNodes   []string
	Context          map[string]string
	EngineResult     *pipeline.EngineResult
	Trace            *pipeline.Trace      // full execution trace (nodes, timing, stats)
	TokensByProvider map[string]llm.Usage // per-provider token totals
	ToolCallsByName  map[string]int       // tool call counts by name
	Cost             *CostReport          // per-provider cost rollup; nil when no usage recorded
	// ArtifactRunDir is the run-specific artifact directory (e.g.
	// "<artifactDir>/<runID>"). Populated when WithArtifactDir is set via
	// Config.ArtifactDir. Pass this to ExportBundle to create a portable
	// git bundle of the run's history.
	ArtifactRunDir string
	// BundlePath is the path of the exported git bundle. Populated only when
	// ExportBundle is invoked by the caller after Run completes.
	BundlePath string
}

// Engine wraps pipeline.Engine with auto-wired internals.
type Engine struct {
	inner        *pipeline.Engine
	client       *llm.Client // nil if caller provided their own Completer
	tokenTracker *llm.TokenTracker
	closeOnce    sync.Once
	closeErr     error
	artifactDir  string // base artifact directory; "" if not set
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

	workDir, err := resolveWorkDir(cfg.WorkingDir)
	if err != nil {
		return nil, err
	}

	if err := applyResumeRunID(&cfg, workDir); err != nil {
		return nil, err
	}

	client, completer, err := resolveCompleter(cfg)
	if err != nil {
		return nil, err
	}

	return buildEngine(graph, cfg, workDir, client, completer)
}

// applyResumeRunID resolves Config.ResumeRunID to a concrete checkpoint path
// and stores it on Config.CheckpointDir. A non-empty CheckpointDir on the
// incoming config is honored as an explicit override — the user is telling
// us exactly which file to use.
func applyResumeRunID(cfg *Config, workDir string) error {
	if cfg.ResumeRunID == "" || cfg.CheckpointDir != "" {
		return nil
	}
	cpPath, err := ResolveCheckpoint(workDir, cfg.ResumeRunID)
	if err != nil {
		return fmt.Errorf("resume run %q: %w", cfg.ResumeRunID, err)
	}
	cfg.CheckpointDir = cpPath
	return nil
}

// resolveWorkDir returns the working directory, falling back to cwd if empty.
func resolveWorkDir(workDir string) (string, error) {
	if workDir != "" {
		return workDir, nil
	}
	dir, err := os.Getwd()
	if err != nil {
		return "", fmt.Errorf("get working directory: %w", err)
	}
	return dir, nil
}

// buildEngine assembles the Engine after all dependencies are resolved.
func buildEngine(graph *pipeline.Graph, cfg Config, workDir string, client *llm.Client, completer agent.Completer) (*Engine, error) {
	// Clean up the auto-created client if anything below fails.
	built := false
	defer func() {
		if !built && client != nil {
			client.Close()
		}
	}()

	if err := pipeline.ApplyGraphParamOverrides(graph, cfg.Params); err != nil {
		return nil, fmt.Errorf("apply params: %w", err)
	}
	injectGraphDefaults(graph, cfg)

	tokenTracker := llm.NewTokenTracker()
	// Attach token tracker as middleware to the LLM client so it captures
	// per-provider usage during native backend runs. Works for both
	// auto-created clients and user-provided *llm.Client via Config.LLMClient.
	if client != nil {
		client.AddMiddleware(tokenTracker)
	} else if lc, ok := completer.(*llm.Client); ok {
		lc.AddMiddleware(tokenTracker)
	}
	registry, err := buildRegistry(graph, client, completer, workDir, cfg, tokenTracker)
	if err != nil {
		return nil, err
	}
	engineOpts := buildEngineOpts(cfg)
	inner := pipeline.NewEngine(graph, registry, engineOpts...)

	built = true
	return &Engine{
		inner:        inner,
		client:       client,
		tokenTracker: tokenTracker,
		artifactDir:  cfg.ArtifactDir,
	}, nil
}

// resolveCompleter returns the LLM client and completer, building a client from env if needed.
func resolveCompleter(cfg Config) (*llm.Client, agent.Completer, error) {
	if cfg.LLMClient != nil {
		return nil, cfg.LLMClient, nil
	}
	client, err := buildClient(cfg.Provider, cfg.GatewayURL)
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
func buildRegistry(graph *pipeline.Graph, client *llm.Client, completer agent.Completer, workDir string, cfg Config, tokenTracker *llm.TokenTracker) (*pipeline.HandlerRegistry, error) {
	env := exec.NewLocalEnvironment(workDir)
	registryOpts := []handlers.RegistryOption{
		handlers.WithLLMClient(completer, workDir),
		handlers.WithExecEnvironment(env),
		handlers.WithTokenTracker(tokenTracker),
	}
	if cfg.AgentEvents != nil {
		registryOpts = append(registryOpts, handlers.WithAgentEventHandler(cfg.AgentEvents))
	}
	if cfg.EventHandler != nil {
		registryOpts = append(registryOpts, handlers.WithPipelineEventHandler(cfg.EventHandler))
	}
	if cfg.Backend != "" {
		registryOpts = append(registryOpts, handlers.WithDefaultBackend(cfg.Backend))
	}
	interviewer, err := resolveInterviewer(cfg, client, completer)
	if err != nil {
		return nil, err
	}
	if interviewer != nil {
		registryOpts = append(registryOpts, handlers.WithInterviewer(interviewer, graph))
	}
	return handlers.NewDefaultRegistry(graph, registryOpts...), nil
}

// resolveInterviewer selects an automated interviewer based on Config.
// Returns nil if no automation is configured (interactive/default mode).
// Priority: AutoApprove > WebhookGate > Autopilot.
// When Backend is "claude-code", prefers ClaudeCodeAutopilotInterviewer.
func resolveInterviewer(cfg Config, client *llm.Client, completer agent.Completer) (handlers.FreeformInterviewer, error) {
	if cfg.AutoApprove {
		return &handlers.AutoApproveFreeformInterviewer{}, nil
	}
	if cfg.WebhookGate != nil {
		return resolveWebhookInterviewer(cfg.WebhookGate)
	}
	if cfg.Autopilot == "" {
		return nil, nil
	}
	return resolveAutopilot(cfg, client, completer)
}

// resolveWebhookInterviewer creates a WebhookInterviewer from a WebhookGateConfig.
// Returns an error if WebhookURL is not set.
func resolveWebhookInterviewer(wgc *WebhookGateConfig) (handlers.FreeformInterviewer, error) {
	if wgc.WebhookURL == "" {
		return nil, fmt.Errorf("WebhookGate.WebhookURL is required")
	}
	addr := wgc.CallbackAddr
	if addr == "" {
		addr = ":8789"
	}
	wi := handlers.NewWebhookInterviewer(wgc.WebhookURL, addr)
	if wgc.Timeout > 0 {
		wi.Timeout = wgc.Timeout
	}
	if wgc.TimeoutAction != "" {
		wi.DefaultAction = wgc.TimeoutAction
	}
	if wgc.AuthHeader != "" {
		wi.AuthHeader = wgc.AuthHeader
	}
	if wgc.RunID != "" {
		wi.RunID = wgc.RunID
	}
	return wi, nil
}

// resolveAutopilot builds an autopilot interviewer for the given persona and backend.
func resolveAutopilot(cfg Config, client *llm.Client, completer agent.Completer) (handlers.FreeformInterviewer, error) {
	persona, err := handlers.ParsePersona(cfg.Autopilot)
	if err != nil {
		return nil, fmt.Errorf("invalid autopilot persona %q: %w", cfg.Autopilot, err)
	}
	if cfg.Backend == "claude-code" {
		if iv, ccErr := handlers.NewClaudeCodeAutopilotInterviewer(persona); ccErr == nil {
			return iv, nil
		}
		log.Printf("[tracker] claude-code autopilot init failed, trying native")
	}
	client = resolveAutopilotClient(client, completer)
	if client == nil {
		return nil, fmt.Errorf("autopilot %q requires an LLM client (set Config.LLMClient or configure API keys)", cfg.Autopilot)
	}
	return handlers.NewAutopilotInterviewer(client, persona), nil
}

// resolveAutopilotClient returns the LLM client for native autopilot,
// trying a type assertion on completer if client is nil.
func resolveAutopilotClient(client *llm.Client, completer agent.Completer) *llm.Client {
	if client != nil {
		return client
	}
	if lc, ok := completer.(*llm.Client); ok {
		return lc
	}
	return nil
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
	if guard := pipeline.NewBudgetGuard(cfg.Budget); guard != nil {
		opts = append(opts, pipeline.WithBudgetGuard(guard))
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
	graph, diags, err := pipeline.LoadDippinWorkflow(source, "inline.dip")
	// Log validation errors and lint warnings before returning so callers
	// see the specific diagnostics even on fatal failures.
	for _, d := range diags {
		log.Println(d.String())
	}
	if err != nil {
		return nil, err
	}
	return graph, nil
}

// buildClient creates an LLM client from environment variables with
// base URL support and retry middleware. If provider is non-empty, only
// that provider is configured (returns error if unknown).
// gatewayURL is the Cloudflare AI Gateway root URL from Config.GatewayURL;
// it is consulted after per-provider *_BASE_URL env vars and before
// TRACKER_GATEWAY_URL (see resolveProviderBaseURLWithGateway).
func buildClient(provider, gatewayURL string) (*llm.Client, error) {
	constructors := allProviderConstructors(gatewayURL)

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
// gatewayURL is the explicit gateway root URL (from Config.GatewayURL); it is
// passed to the adapter constructors so library consumers don't need to mutate
// os.Environ.
func allProviderConstructors(gatewayURL string) map[string]func(string) (llm.ProviderAdapter, error) {
	return map[string]func(string) (llm.ProviderAdapter, error){
		"anthropic":     func(k string) (llm.ProviderAdapter, error) { return newAnthropicAdapter(k, gatewayURL) },
		"openai":        func(k string) (llm.ProviderAdapter, error) { return newOpenAIAdapter(k, gatewayURL) },
		"gemini":        func(k string) (llm.ProviderAdapter, error) { return newGeminiAdapter(k, gatewayURL) },
		"openai-compat": func(k string) (llm.ProviderAdapter, error) { return newOpenAICompatAdapter(k, gatewayURL) },
	}
}

// resolveProviderBaseURLWithGateway resolves the base URL for a provider,
// consulting sources in priority order:
//
//  1. Per-provider env var (*_BASE_URL) — always wins.
//  2. gatewayURL argument (from Config.GatewayURL) with provider suffix appended.
//  3. TRACKER_GATEWAY_URL env var with provider suffix appended.
//  4. Empty string — use provider SDK default.
func resolveProviderBaseURLWithGateway(provider, gatewayURL string) string {
	var envKey, suffix string
	switch provider {
	case "anthropic":
		envKey, suffix = "ANTHROPIC_BASE_URL", "/anthropic"
	case "openai":
		envKey, suffix = "OPENAI_BASE_URL", "/openai"
	case "gemini":
		envKey, suffix = "GEMINI_BASE_URL", "/google-ai-studio"
	case "openai-compat":
		envKey, suffix = "OPENAI_COMPAT_BASE_URL", "/compat"
	default:
		return ""
	}
	if v := os.Getenv(envKey); v != "" {
		return v
	}
	if gatewayURL != "" {
		return strings.TrimRight(gatewayURL, "/") + suffix
	}
	gateway := strings.TrimRight(os.Getenv("TRACKER_GATEWAY_URL"), "/")
	if gateway == "" {
		return ""
	}
	return gateway + suffix
}

// ResolveProviderBaseURL returns the base URL a provider's HTTP client should
// use. Resolution order:
//
//  1. The provider-specific env var (ANTHROPIC_BASE_URL, OPENAI_BASE_URL,
//     GEMINI_BASE_URL, OPENAI_COMPAT_BASE_URL).
//  2. TRACKER_GATEWAY_URL with the Cloudflare AI Gateway provider suffix
//     appended (e.g. ".../anthropic", ".../openai", ".../google-ai-studio").
//  3. Empty string, meaning the provider's SDK default.
//
// Per-provider env vars always win over TRACKER_GATEWAY_URL, so users can set
// a single gateway URL for everything and still override individual providers.
// Trailing slashes on the gateway root are stripped before the suffix is
// appended to prevent double-slash URLs. Unknown provider names return the
// empty string.
func ResolveProviderBaseURL(provider string) string {
	var envKey, suffix string
	switch provider {
	case "anthropic":
		envKey, suffix = "ANTHROPIC_BASE_URL", "/anthropic"
	case "openai":
		envKey, suffix = "OPENAI_BASE_URL", "/openai"
	case "gemini":
		envKey, suffix = "GEMINI_BASE_URL", "/google-ai-studio"
	case "openai-compat":
		envKey, suffix = "OPENAI_COMPAT_BASE_URL", "/compat"
	default:
		return ""
	}
	if v := os.Getenv(envKey); v != "" {
		return v
	}
	gateway := strings.TrimRight(os.Getenv("TRACKER_GATEWAY_URL"), "/")
	if gateway == "" {
		return ""
	}
	return gateway + suffix
}

func newAnthropicAdapter(key, gatewayURL string) (llm.ProviderAdapter, error) {
	var opts []anthropic.Option
	if base := resolveProviderBaseURLWithGateway("anthropic", gatewayURL); base != "" {
		opts = append(opts, anthropic.WithBaseURL(base))
	}
	return anthropic.New(key, opts...), nil
}

func newOpenAIAdapter(key, gatewayURL string) (llm.ProviderAdapter, error) {
	var opts []openai.Option
	if base := resolveProviderBaseURLWithGateway("openai", gatewayURL); base != "" {
		opts = append(opts, openai.WithBaseURL(base))
	}
	return openai.New(key, opts...), nil
}

func newGeminiAdapter(key, gatewayURL string) (llm.ProviderAdapter, error) {
	var opts []google.Option
	if base := resolveProviderBaseURLWithGateway("gemini", gatewayURL); base != "" {
		opts = append(opts, google.WithBaseURL(base))
	}
	return google.New(key, opts...), nil
}

func newOpenAICompatAdapter(key, gatewayURL string) (llm.ProviderAdapter, error) {
	var opts []openaicompat.Option
	if base := resolveProviderBaseURLWithGateway("openai-compat", gatewayURL); base != "" {
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
	result := resultFromEngine(engineResult)
	e.populateResultTokensAndCost(result, engineResult)
	e.populateBudgetHaltIfNeeded(result, engineResult)
	if engineResult != nil && engineResult.Trace != nil {
		result.ToolCallsByName = engineResult.Trace.AggregateToolCalls()
	}
	if e.artifactDir != "" && result.RunID != "" {
		result.ArtifactRunDir = filepath.Join(e.artifactDir, result.RunID)
	}
	return result, nil
}

// populateResultTokensAndCost fills in per-provider token counts and cost report from the tracker.
func (e *Engine) populateResultTokensAndCost(result *Result, engineResult *pipeline.EngineResult) {
	if e.tokenTracker == nil {
		return
	}
	result.TokensByProvider = e.tokenTracker.AllProviderUsage()
	resolver := e.defaultModelResolver()
	byProvider := e.tokenTracker.CostByProvider(resolver)
	if len(byProvider) > 0 {
		total := 0.0
		for _, pc := range byProvider {
			total += pc.USD
		}
		result.Cost = &CostReport{
			TotalUSD:   total,
			ByProvider: byProvider,
		}
	}
}

// populateBudgetHaltIfNeeded fills in LimitsHit when a budget guard halted the run.
func (e *Engine) populateBudgetHaltIfNeeded(result *Result, engineResult *pipeline.EngineResult) {
	if engineResult == nil || engineResult.Status != pipeline.OutcomeBudgetExceeded {
		return
	}
	if result.Cost == nil {
		result.Cost = &CostReport{}
	}
	result.Cost.LimitsHit = engineResult.BudgetLimitsHit
}

// defaultModelResolver returns an llm.ModelResolver that maps any provider to
// the graph's default model. Per-provider model overrides are not yet supported
// (the same model attr is used for cost estimation regardless of provider).
// When the graph has no llm_model attr set, the resolver returns "", which
// yields USD=0 via llm.EstimateCost.
func (e *Engine) defaultModelResolver() llm.ModelResolver {
	model := ""
	if e.inner != nil {
		if g := e.inner.Graph(); g != nil {
			model = g.Attrs["llm_model"]
		}
	}
	return func(provider string) string { return model }
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

// ValidationResult contains the outcome of pipeline validation.
type ValidationResult struct {
	Graph    *pipeline.Graph
	Errors   []string
	Warnings []string
	Hints    []string
}

// ValidateOption configures ValidateSource behavior.
type ValidateOption func(*validateConfig)

type validateConfig struct {
	format string
}

// WithValidateFormat sets the pipeline source format ("dip" or "dot").
func WithValidateFormat(format string) ValidateOption {
	return func(c *validateConfig) { c.format = format }
}

// ValidateSource parses and validates a pipeline source string without executing it.
// Returns a ValidationResult with structured errors, warnings, and hints.
// An error is returned when the source cannot be parsed or has structural errors.
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

	// Structural + semantic validation (includes warnings).
	ve := pipeline.ValidateAll(graph)
	if ve != nil {
		result.Errors = append(result.Errors, ve.Errors...)
		result.Warnings = append(result.Warnings, ve.Warnings...)
	}

	if len(result.Errors) > 0 {
		return result, fmt.Errorf("validation failed: %s", result.Errors[0])
	}
	return result, nil
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
		Trace:          er.Trace,
	}
}
