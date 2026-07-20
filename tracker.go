// ABOUTME: Top-level convenience API for running pipelines (.dip preferred, .dot deprecated) with auto-wired dependencies.
// ABOUTME: Consumers import only this package — LLM clients, registries, and environments are built automatically.
package tracker

import (
	"context"
	"errors"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strconv"
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
	LLMTrace      llm.TraceObserver             // optional: raw LLM trace events (attached to the auto-created or provided *llm.Client)
	LLMClient     agent.Completer               // optional: override auto-created client
	Context       map[string]string             // optional: initial pipeline context
	Params        map[string]string             // optional: override declared workflow params (keys without "params." prefix)
	// Subgraphs are pre-loaded child graphs, keyed by subgraph_ref, for a graph
	// that contains subgraph nodes. Callers that resolve subgraph_ref files
	// themselves (e.g. the CLI) pass the loaded map here; it is wired into the
	// registry via handlers.WithSubgraphs. Nil/empty for flat pipelines.
	Subgraphs map[string]*pipeline.Graph
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
	GatewayURL string
	// GatewayKind selects the path convention used with GatewayURL (or its
	// TRACKER_GATEWAY_URL env-var fallback). Empty or GatewayKindCFAIG
	// (default, backcompat) routes via Cloudflare AI Gateway conventions:
	// /anthropic, /openai, /google-ai-studio, /compat. GatewayKindBedrock
	// targets the 2389 bedrock-gateway Worker which uses native SDK paths.
	// The TRACKER_GATEWAY_KIND env var is the fallback when GatewayKind is
	// empty. See ResolveProviderBaseURL.
	GatewayKind GatewayKind
	// Interviewer optionally injects a custom in-process human-gate handler.
	// This is the seam for interactive transports (TUI, Slack, web, mobile):
	// the transport implements handlers.Interviewer and, optionally, the richer
	// handlers.FreeformInterviewer / LabeledFreeformInterviewer /
	// InterviewInterviewer extensions plus the optional Actor() / Cancel() /
	// ContextSetter side-interfaces — the human handler upgrades via type
	// assertion and picks the richest supported mode. When set, it takes
	// precedence over AutoApprove, WebhookGate, and Autopilot. Nil is a no-op.
	Interviewer handlers.Interviewer
	WebhookGate *WebhookGateConfig // optional: post human gates to an HTTP webhook and wait for callback
	// BundleIdentity is the content-addressed identity ("sha256:<hex>") of
	// the .dipx bundle this run was loaded from. Stamped onto every emitted
	// PipelineEvent and persisted to the checkpoint for resume verification.
	// Empty (the default) is a no-op and matches plain .dip behavior.
	//
	// Callers that build their own JSONLEventHandler should also call
	// activityLog.SetBundleIdentity(cfg.BundleIdentity) so agent/llm writes
	// outside the engine event chain carry the same provenance.
	BundleIdentity string
	// Git configures the v0.29.0 git preflight check. Nil = auto, which
	// respects the workflow's `requires:` block. See GitConfig.
	Git *GitConfig
}

// GitPreflight is the resolved preflight policy that controls the v0.29.0
// git environment check. Type alias to pipeline.GitPreflight so callers
// don't have to import the pipeline package for this single value.
type GitPreflight = pipeline.GitPreflight

// GitPreflight values re-exported from pipeline for caller convenience.
const (
	GitPreflightAuto    = pipeline.GitPreflightAuto
	GitPreflightOff     = pipeline.GitPreflightOff
	GitPreflightWarn    = pipeline.GitPreflightWarn
	GitPreflightRequire = pipeline.GitPreflightRequire
	GitPreflightInit    = pipeline.GitPreflightInit
)

// GitConfig configures the git preflight check that runs before any node
// executes. Zero value (or nil *GitConfig on Config.Git) resolves to
// GitPreflightAuto, which respects the workflow's `requires:` block.
//
// AllowInit is required when Preflight == GitPreflightInit and stdin is
// not a TTY — it is the second safety latch on automatic `git init`.
type GitConfig struct {
	Preflight GitPreflight
	AllowInit bool
}

// ResolveGitConfig returns the (policy, allowInit) pair to apply for this
// run, considering Config.Git. The zero value resolves to (auto, false).
func ResolveGitConfig(cfg Config) (GitPreflight, bool) {
	if cfg.Git == nil {
		return GitPreflightAuto, false
	}
	return cfg.Git.Preflight, cfg.Git.AllowInit
}

// WebhookGateConfig controls headless webhook-based human gate handling.
// When set, human gate prompts are POSTed to WebhookURL and the pipeline
// waits for a callback POST to the local callback server.
type WebhookGateConfig struct {
	WebhookURL    string        // required: URL to post gate payloads to
	CallbackAddr  string        // local listen addr for callback server (default: :0, an OS-assigned ephemeral port; the bound address is advertised via each gate's callback_url)
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
	RunID string
	// Status carries the run's terminal status. One of:
	//   - "success"
	//   - "fail"
	//   - "budget_exceeded"
	//   - "validation_overridden"
	// The enum is open — future minor releases may add new values. Use
	// pipeline.TerminalStatus(r.Status).IsSuccess() to classify rather than
	// switching on the raw string.
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
	// BundleIdentity is the content-addressed identity ("sha256:<hex>") of
	// the .dipx bundle the run was loaded from, mirrored from Config.BundleIdentity
	// for the caller's convenience. Empty for plain .dip runs.
	BundleIdentity string
	// ValidationOverrides is the list of override edges traversed during the run.
	// Empty for runs with no override edges. Populated for every terminal status
	// (including fail and budget_exceeded) so forensics see overrides even when
	// failure dominates.
	ValidationOverrides []pipeline.OverrideDetail
}

// Engine wraps pipeline.Engine with auto-wired internals.
type Engine struct {
	inner          *pipeline.Engine
	client         *llm.Client // nil if caller provided their own Completer
	tokenTracker   *llm.TokenTracker
	closeOnce      sync.Once
	closeErr       error
	artifactDir    string // base artifact directory; "" if not set
	bundleIdentity string // mirrored from Config.BundleIdentity for Result population
}

// NewEngine parses a pipeline source (.dip preferred, DOT deprecated),
// auto-wires all internals, and returns an Engine.
// Format is auto-detected from content if Config.Format is empty:
// sources starting with "digraph" or "strict digraph" are treated as DOT,
// everything else as .dip.
// The caller must call Close() when done to release resources.
//
// This wrapper exists for backward compatibility: it uses
// context.Background() for the v0.29.0 git preflight. New callers that
// want to support cancellation of the preflight (especially with
// `--git=init` which has a `git init` side effect) should use
// NewEngineWithContext instead.
func NewEngine(source string, cfg Config) (*Engine, error) {
	return NewEngineWithContext(context.Background(), source, cfg)
}

// NewEngineWithContext is the context-aware form of NewEngine. The supplied
// ctx threads into the v0.29.0 git preflight check; a canceled context
// aborts preflight (including the `--git=init` `git init` side effect)
// rather than letting it complete before Engine.Run(ctx) observes the
// cancellation. tracker.Run(ctx, ...) calls this form so library callers
// who pass a real ctx get end-to-end cancellation coverage.
func NewEngineWithContext(ctx context.Context, source string, cfg Config) (*Engine, error) {
	graph, err := parsePipelineSource(source, cfg.Format)
	if err != nil {
		return nil, err
	}
	return NewEngineFromGraph(ctx, graph, cfg)
}

// NewEngineFromGraph assembles an Engine from an already-parsed graph, skipping
// source parsing. Use it when the caller has loaded the graph itself and, for a
// pipeline with subgraph nodes, resolved the subgraph_ref files into
// Config.Subgraphs — as the CLI does. Otherwise identical to
// NewEngineWithContext (validate → git preflight → resume → client → assemble).
func NewEngineFromGraph(ctx context.Context, graph *pipeline.Graph, cfg Config) (*Engine, error) {
	if err := pipeline.Validate(graph); err != nil {
		return nil, fmt.Errorf("validate graph: %w", err)
	}

	workDir, err := resolveWorkDir(cfg.WorkingDir)
	if err != nil {
		return nil, err
	}

	if err := runPreflight(ctx, graph, cfg, workDir); err != nil {
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

// runPreflight invokes pipeline.Preflight with the resolved policy from cfg.
// Returns nil if the workflow doesn't declare any deps (and CLI isn't
// forcing the check), or if the policy downgrades the check to a warning.
// Library callers default to non-interactive; the CLI overrides via its
// own preflight call (cmd/tracker/run.go) where stdin TTY detection lives.
func runPreflight(ctx context.Context, graph *pipeline.Graph, cfg Config, workDir string) error {
	policy, allowInit := ResolveGitConfig(cfg)
	return pipeline.Preflight(ctx, pipeline.PreflightConfig{
		WorkDir:        workDir,
		Requires:       graph.RequiredDeps(),
		Policy:         policy,
		AllowInit:      allowInit,
		InteractiveTTY: false,
		Warner: func(format string, args ...any) {
			fmt.Fprintf(os.Stderr, "warning: "+format+"\n", args...)
		},
	})
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
	attachClientObservers(client, completer, tokenTracker, cfg)
	registry, err := buildRegistry(graph, client, completer, workDir, cfg, tokenTracker)
	if err != nil {
		return nil, err
	}
	engineOpts := buildEngineOpts(cfg, graph)
	inner := pipeline.NewEngine(graph, registry, engineOpts...)

	built = true
	return &Engine{
		inner:          inner,
		client:         client,
		tokenTracker:   tokenTracker,
		artifactDir:    cfg.ArtifactDir,
		bundleIdentity: cfg.BundleIdentity,
	}, nil
}

// attachClientObservers wires the token tracker, and any Config.LLMTrace
// observer, onto whichever *llm.Client backs this run (the auto-created client
// or a *llm.Client supplied via Config.LLMClient). A non-*llm.Client completer
// carries no observable transport, so it is a no-op there.
func attachClientObservers(client *llm.Client, completer agent.Completer, tokenTracker *llm.TokenTracker, cfg Config) {
	c := client
	if c == nil {
		if lc, ok := completer.(*llm.Client); ok {
			c = lc
		}
	}
	if c == nil {
		return
	}
	c.AddMiddleware(tokenTracker)
	if cfg.LLMTrace != nil {
		c.AddTraceObserver(cfg.LLMTrace)
	}
}

// resolveCompleter returns the LLM client and completer, building a client from env if needed.
func resolveCompleter(cfg Config) (*llm.Client, agent.Completer, error) {
	if cfg.LLMClient != nil {
		return nil, cfg.LLMClient, nil
	}
	client, err := buildClient(cfg.Provider, cfg.GatewayURL, cfg.GatewayKind)
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
	if cfg.BundleIdentity != "" {
		registryOpts = append(registryOpts, handlers.WithHandlerBundleIdentity(cfg.BundleIdentity))
	}
	if cfg.Backend != "" {
		registryOpts = append(registryOpts, handlers.WithDefaultBackend(cfg.Backend))
	}
	if len(cfg.Subgraphs) > 0 {
		registryOpts = append(registryOpts, handlers.WithSubgraphs(cfg.Subgraphs))
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

// buildEngineOpts constructs engine options from Config. When a config
// budget field is zero, buildEngineOpts falls back to the matching
// graph-level attr (max_total_tokens, max_cost_cents, max_wall_time)
// populated by the adapter from dippin WorkflowDefaults. Explicit
// Config.Budget values always win over the workflow fallback.
func buildEngineOpts(cfg Config, graph *pipeline.Graph) []pipeline.EngineOption {
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
	budget := ResolveBudgetLimits(cfg.Budget, graph)
	if guard := pipeline.NewBudgetGuard(budget); guard != nil {
		opts = append(opts, pipeline.WithBudgetGuard(guard))
	}
	if cfg.BundleIdentity != "" {
		opts = append(opts, pipeline.WithBundleIdentity(cfg.BundleIdentity))
	}
	opts = append(opts, pipeline.WithStylesheetResolution(true))
	return opts
}

// ResolveBudgetLimits fills any zero field on cfg from the matching
// workflow-level default in graph.Attrs. Config values take precedence —
// the graph attrs are only consulted for fields the caller left unset.
// Returns the original cfg unchanged if graph is nil or has no attrs.
//
// The graph-level keys consulted are max_total_tokens, max_cost_cents,
// max_wall_time, and stall_timeout, which the dippin adapter writes from
// WorkflowDefaults fields in v0.21.0+.
//
// Exported so the tracker CLI can merge its --max-* flag values with
// workflow defaults without re-implementing the same logic.
func ResolveBudgetLimits(cfg pipeline.BudgetLimits, graph *pipeline.Graph) pipeline.BudgetLimits {
	if graph == nil || len(graph.Attrs) == 0 {
		return cfg
	}
	if cfg.MaxTotalTokens == 0 {
		cfg.MaxTotalTokens = positiveIntAttr(graph, "max_total_tokens")
	}
	if cfg.MaxCostCents == 0 {
		cfg.MaxCostCents = positiveIntAttr(graph, "max_cost_cents")
	}
	if cfg.MaxWallTime == 0 {
		cfg.MaxWallTime = positiveDurationAttr(graph, "max_wall_time")
	}
	if cfg.StallTimeout == 0 {
		cfg.StallTimeout = positiveDurationAttr(graph, "stall_timeout")
	}
	// Opt-in sleep-awareness (#422). A bool can't use the == 0 zero gate, so a
	// Config value of true wins; otherwise consult the attr. A malformed bool
	// falls through to the default (off) — it does not silently mis-enable.
	if !cfg.SleepAware {
		cfg.SleepAware = boolAttr(graph, "sleep_aware_budget")
	}
	return cfg
}

// positiveIntAttr returns the positive int value of graph.Attrs[key], or 0 when
// absent, unparseable, or non-positive (leaving the caller's field unchanged).
func positiveIntAttr(graph *pipeline.Graph, key string) int {
	if v, ok := graph.Attrs[key]; ok {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			return n
		}
	}
	return 0
}

// positiveDurationAttr returns the positive duration value of graph.Attrs[key],
// or 0 when absent, unparseable, or non-positive.
func positiveDurationAttr(graph *pipeline.Graph, key string) time.Duration {
	if v, ok := graph.Attrs[key]; ok {
		if d, err := time.ParseDuration(v); err == nil && d > 0 {
			return d
		}
	}
	return 0
}

// boolAttr returns the parsed bool value of graph.Attrs[key], or false when
// absent or unparseable (malformed values stay default-off, never mis-enabled).
func boolAttr(graph *pipeline.Graph, key string) bool {
	if v, ok := graph.Attrs[key]; ok {
		if b, err := strconv.ParseBool(v); err == nil {
			return b
		}
	}
	return false
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
// gatewayURL is the gateway root URL from Config.GatewayURL; gatewayKind
// is the matching Config.GatewayKind (empty = cf-aig default). Both are
// consulted after per-provider *_BASE_URL env vars and before the
// TRACKER_GATEWAY_URL / TRACKER_GATEWAY_KIND env-var fallbacks (see
// resolveProviderBaseURLWithGateway).
func buildClient(provider, gatewayURL string, gatewayKind GatewayKind) (*llm.Client, error) {
	constructors := allProviderConstructors(gatewayURL, gatewayKind)

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
// gatewayURL is the explicit gateway root URL (from Config.GatewayURL) and
// gatewayKind is the matching path-convention selector (from
// Config.GatewayKind). Both are passed to the adapter constructors so
// library consumers don't need to mutate os.Environ.
func allProviderConstructors(gatewayURL string, gatewayKind GatewayKind) map[string]func(string) (llm.ProviderAdapter, error) {
	return map[string]func(string) (llm.ProviderAdapter, error){
		"anthropic":     func(k string) (llm.ProviderAdapter, error) { return newAnthropicAdapter(k, gatewayURL, gatewayKind) },
		"openai":        func(k string) (llm.ProviderAdapter, error) { return newOpenAIAdapter(k, gatewayURL, gatewayKind) },
		"gemini":        func(k string) (llm.ProviderAdapter, error) { return newGeminiAdapter(k, gatewayURL, gatewayKind) },
		"openai-compat": func(k string) (llm.ProviderAdapter, error) { return newOpenAICompatAdapter(k, gatewayURL, gatewayKind) },
	}
}

// GatewayKind selects the path convention used when TRACKER_GATEWAY_URL is
// set. The default (cf-aig) matches Cloudflare AI Gateway's per-provider
// subpath convention; bedrock targets the 2389 bedrock-gateway Worker which
// uses native SDK URL paths.
//
// See docs/superpowers/specs/2026-06-01-issue-274-bedrock-gateway-integration-design.md.
type GatewayKind string

const (
	// GatewayKindCFAIG routes via Cloudflare AI Gateway path conventions:
	// /anthropic, /openai, /google-ai-studio, /compat. Default.
	GatewayKindCFAIG GatewayKind = "cf-aig"

	// GatewayKindBedrock routes via the 2389 bedrock-gateway Worker which
	// translates SDK requests to AWS Bedrock Converse. Uses native SDK
	// URL conventions: empty suffix for Anthropic, /v1 for OpenAI and
	// Gemini. openai-compat is not supported on this gateway.
	GatewayKindBedrock GatewayKind = "bedrock"
)

// gatewaySuffix returns the per-provider URL path suffix for the given
// gateway kind. Returns ok=false when the (kind, provider) pair is
// unsupported — callers should treat this as "do not route via gateway"
// and emit an actionable error. Unknown kind values also return ok=false
// (fail-closed) rather than silently falling through to the cf-aig default.
func gatewaySuffix(kind GatewayKind, provider string) (string, bool) {
	switch kind {
	case "", GatewayKindCFAIG:
		return cfAIGSuffix(provider)
	case GatewayKindBedrock:
		return bedrockSuffix(provider)
	}
	return "", false
}

// cfAIGSuffix maps a provider to its Cloudflare AI Gateway path suffix.
func cfAIGSuffix(provider string) (string, bool) {
	switch provider {
	case "anthropic":
		return "/anthropic", true
	case "openai":
		return "/openai", true
	case "gemini":
		return "/google-ai-studio", true
	case "openai-compat":
		return "/compat", true
	}
	return "", false
}

// bedrockSuffix maps a provider to its Bedrock gateway path suffix.
func bedrockSuffix(provider string) (string, bool) {
	switch provider {
	case "anthropic":
		return "", true // Anthropic SDK appends /v1/messages itself
	case "openai":
		return "/v1", true
	case "gemini":
		return "/v1", true
	case "openai-compat":
		return "", false // refuse: bedrock gateway has no /compat
	}
	return "", false
}

// ErrGatewayRouteRefused is returned by the strict resolver functions when
// a gateway URL is configured but the (kind, provider) pair is unsupported
// or the kind is unknown. Surfacing this as an error prevents the silent
// SDK-default fallback (e.g. openai-compat defaulting to openrouter.ai)
// that contradicts the documented fail-closed semantics of #276.
var ErrGatewayRouteRefused = errors.New("gateway route refused: kind/provider combination unsupported or unknown")

// providerBaseURLEnvKey returns the per-provider *_BASE_URL env var name, or
// "" for an unknown provider.
func providerBaseURLEnvKey(provider string) string {
	switch provider {
	case "anthropic":
		return "ANTHROPIC_BASE_URL"
	case "openai":
		return "OPENAI_BASE_URL"
	case "gemini":
		return "GEMINI_BASE_URL"
	case "openai-compat":
		return "OPENAI_COMPAT_BASE_URL"
	}
	return ""
}

// resolveProviderBaseURLWithGateway resolves the base URL for a provider,
// consulting sources in priority order:
//
//  1. Per-provider env var (*_BASE_URL) — always wins.
//  2. gatewayURL argument (from Config.GatewayURL) with kind-dependent suffix appended.
//  3. TRACKER_GATEWAY_URL env var with kind-dependent suffix appended.
//  4. Empty string — use provider SDK default.
//
// The kind argument (from Config.GatewayKind) selects the suffix map; if
// empty, TRACKER_GATEWAY_KIND env var is consulted, and if that is also
// empty the default is cf-aig.
//
// **Fail-closed contract:** when a gateway URL is configured (either via
// the gatewayURL argument or TRACKER_GATEWAY_URL) AND the (kind,
// provider) pair is unsupported OR the kind is unknown, this function
// returns ErrGatewayRouteRefused. Adapter constructors propagate the
// error so client construction fails — preventing the silent SDK-default
// fallback that would otherwise leak requests (carrying the gateway
// token) to the public default endpoint.
func resolveProviderBaseURLWithGateway(provider, gatewayURL string, gatewayKind GatewayKind) (string, error) {
	envKey := providerBaseURLEnvKey(provider)
	if envKey == "" {
		return "", nil
	}
	if v := os.Getenv(envKey); v != "" {
		return v, nil
	}

	gateway := strings.TrimRight(gatewayURL, "/")
	if gateway == "" {
		gateway = strings.TrimRight(os.Getenv("TRACKER_GATEWAY_URL"), "/")
	}
	if gateway == "" {
		return "", nil
	}

	kind := gatewayKind
	if kind == "" {
		kind = GatewayKind(os.Getenv("TRACKER_GATEWAY_KIND"))
	}
	suffix, ok := gatewaySuffix(kind, provider)
	if !ok {
		return "", fmt.Errorf("%w: kind=%q provider=%q", ErrGatewayRouteRefused, kind, provider)
	}
	return gateway + suffix, nil
}

// ResolveProviderBaseURL returns the base URL a provider's HTTP client should
// use. Resolution order:
//
//  1. The provider-specific env var (ANTHROPIC_BASE_URL, OPENAI_BASE_URL,
//     GEMINI_BASE_URL, OPENAI_COMPAT_BASE_URL).
//  2. TRACKER_GATEWAY_URL with a per-provider suffix appended; the suffix
//     map is selected by TRACKER_GATEWAY_KIND (default cf-aig — Cloudflare
//     AI Gateway conventions).
//  3. Empty string, meaning the provider's SDK default.
//
// Per-provider env vars always win over TRACKER_GATEWAY_URL.
//
// **Lax variant.** This function returns the empty string for BOTH "no
// gateway configured" AND "gateway configured but routing refused." It
// is preserved for backward compatibility with library callers that
// existed before #276 added kind dispatch. New code on the adapter
// construction path MUST use [ResolveProviderBaseURLStrict] so that
// refuse-to-route surfaces as an error rather than a silent SDK-default
// fallback.
func ResolveProviderBaseURL(provider string) string {
	base, _ := ResolveProviderBaseURLStrict(provider)
	return base
}

// ResolveProviderBaseURLStrict is the fail-closed sibling of
// [ResolveProviderBaseURL]. It returns the same URL resolution but
// distinguishes "no gateway needed" (returns "", nil) from "gateway
// configured but routing refused" (returns "", [ErrGatewayRouteRefused]
// wrapped). Adapter constructors call this so a misconfigured gateway
// cannot silently leak requests to public SDK default endpoints.
func ResolveProviderBaseURLStrict(provider string) (string, error) {
	return resolveProviderBaseURLWithGateway(provider, "", "")
}

func newAnthropicAdapter(key, gatewayURL string, gatewayKind GatewayKind) (llm.ProviderAdapter, error) {
	base, err := resolveProviderBaseURLWithGateway("anthropic", gatewayURL, gatewayKind)
	if err != nil {
		return nil, fmt.Errorf("anthropic adapter: %w", err)
	}
	var opts []anthropic.Option
	if base != "" {
		opts = append(opts, anthropic.WithBaseURL(base))
	}
	return anthropic.New(key, opts...), nil
}

func newOpenAIAdapter(key, gatewayURL string, gatewayKind GatewayKind) (llm.ProviderAdapter, error) {
	base, err := resolveProviderBaseURLWithGateway("openai", gatewayURL, gatewayKind)
	if err != nil {
		return nil, fmt.Errorf("openai adapter: %w", err)
	}
	var opts []openai.Option
	if base != "" {
		opts = append(opts, openai.WithBaseURL(base))
	}
	return openai.New(key, opts...), nil
}

func newGeminiAdapter(key, gatewayURL string, gatewayKind GatewayKind) (llm.ProviderAdapter, error) {
	base, err := resolveProviderBaseURLWithGateway("gemini", gatewayURL, gatewayKind)
	if err != nil {
		return nil, fmt.Errorf("gemini adapter: %w", err)
	}
	var opts []google.Option
	if base != "" {
		opts = append(opts, google.WithBaseURL(base))
	}
	return google.New(key, opts...), nil
}

func newOpenAICompatAdapter(key, gatewayURL string, gatewayKind GatewayKind) (llm.ProviderAdapter, error) {
	base, err := resolveProviderBaseURLWithGateway("openai-compat", gatewayURL, gatewayKind)
	if err != nil {
		return nil, fmt.Errorf("openai-compat adapter: %w", err)
	}
	var opts []openaicompat.Option
	if base != "" {
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
	result.BundleIdentity = e.bundleIdentity
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

// defaultModelResolver returns an llm.ModelResolver that uses per-provider
// observed models from the token tracker, falling back to the graph's default
// llm_model attr for providers where no model was observed.
func (e *Engine) defaultModelResolver() llm.ModelResolver {
	fallback := ""
	if e.inner != nil {
		if g := e.inner.Graph(); g != nil {
			fallback = g.Attrs["llm_model"]
		}
	}
	if e.tokenTracker != nil {
		return e.tokenTracker.ObservedModelResolver(fallback)
	}
	return func(provider string) string { return fallback }
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

// TokenTracker returns the per-provider token/cost tracker attached to this
// engine's run. A transport that renders spend in-process (e.g. the TUI status
// bar and per-node cost) shares this rather than reconstructing usage from the
// event stream. Non-nil whenever the engine has an observable *llm.Client (the
// auto-created client, or a *llm.Client supplied via Config.LLMClient).
func (e *Engine) TokenTracker() *llm.TokenTracker { return e.tokenTracker }

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
	engine, err := NewEngineWithContext(ctx, source, cfg)
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
		RunID:               er.RunID,
		Status:              string(er.Status),
		CompletedNodes:      er.CompletedNodes,
		Context:             er.Context,
		EngineResult:        er,
		Trace:               er.Trace,
		ValidationOverrides: append([]pipeline.OverrideDetail(nil), er.ValidationOverrides...),
	}
}
