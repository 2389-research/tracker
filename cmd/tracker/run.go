// ABOUTME: Pipeline execution functions for both console mode (mode 1) and TUI mode (mode 2).
// ABOUTME: Includes LLM client construction and interviewer selection.
package main

import (
	"context"
	"fmt"
	"maps"
	"os"
	"os/signal"
	"path/filepath"
	"slices"
	"strings"
	"time"

	tracker "github.com/2389-research/tracker"
	"github.com/2389-research/tracker/agent"
	"github.com/2389-research/tracker/agent/exec"
	"github.com/2389-research/tracker/llm"
	"github.com/2389-research/tracker/llm/anthropic"
	"github.com/2389-research/tracker/llm/google"
	"github.com/2389-research/tracker/llm/openai"
	"github.com/2389-research/tracker/llm/openaicompat"
	"github.com/2389-research/tracker/pipeline"
	"github.com/2389-research/tracker/pipeline/handlers"
	"github.com/2389-research/tracker/tui"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/mattn/go-isatty"
)

// canceller is satisfied by interviewers that hold resources (e.g. WebhookInterviewer
// starts an HTTP server) and need cleanup when the run finishes or is interrupted.
type canceller interface {
	Cancel()
}

// autopilotCfg holds just the autopilot settings needed by chooseInterviewer.
// Set by executeRun before calling run/runTUI, because commandDeps.run has a
// fixed signature that can't be extended without breaking tests.
type autopilotCfg struct {
	persona     string // lax/mid/hard/mentor or empty
	autoApprove bool
}

var activeAutopilotCfg autopilotCfg

// activeBudgetLimits holds the budget limits for the current run.
// Set by executeRun before calling run/runTUI, matching the pattern of activeAutopilotCfg.
var activeBudgetLimits pipeline.BudgetLimits

// activeRunParams holds parsed --param overrides for the current run.
var activeRunParams map[string]string

// activeEffectiveRunParams holds effective values for params that were overridden.
var activeEffectiveRunParams map[string]string

// activeExportBundle holds the --export-bundle path for the current run.
// Set by executeRun. When non-empty, a git bundle of run artifacts is written
// to this path after the pipeline completes. Failures are reported as warnings
// and do not affect the run's exit code.
var activeExportBundle string

// activeWebhookGate holds the webhook gate config for the current run.
// Set by executeRun before calling run/runTUI, matching the pattern of activeAutopilotCfg.
// Nil means no webhook gate is active.
var activeWebhookGate *webhookGateCfg

// activeArtifactDir holds the --artifact-dir override for the current run.
// Set by executeRun before calling run/runTUI. Empty means default (<workdir>/.tracker/runs).
var activeArtifactDir string

// webhookGateCfg holds just the webhook gate settings needed by chooseInterviewer.
type webhookGateCfg struct {
	webhookURL        string
	gateCallbackAddr  string
	gateTimeout       time.Duration
	gateTimeoutAction string
	webhookAuthHeader string
}

// buildWebhookGateConfig returns a populated *webhookGateCfg when webhookURL is set,
// or nil when no webhook gate is configured.
func buildWebhookGateConfig(cfg runConfig) *webhookGateCfg {
	if cfg.webhookURL == "" {
		return nil
	}
	return &webhookGateCfg{
		webhookURL:        cfg.webhookURL,
		gateCallbackAddr:  cfg.gateCallbackAddr,
		gateTimeout:       cfg.gateTimeout,
		gateTimeoutAction: cfg.gateTimeoutAction,
		webhookAuthHeader: cfg.webhookAuthHeader,
	}
}

// newWebhookInterviewerFromCfg constructs a WebhookInterviewer from a webhookGateCfg.
func newWebhookInterviewerFromCfg(cfg *webhookGateCfg) *handlers.WebhookInterviewer {
	wi := handlers.NewWebhookInterviewer(cfg.webhookURL, cfg.gateCallbackAddr)
	if cfg.gateTimeout > 0 {
		wi.Timeout = cfg.gateTimeout
	}
	if cfg.gateTimeoutAction != "" {
		wi.DefaultAction = cfg.gateTimeoutAction
	}
	if cfg.webhookAuthHeader != "" {
		wi.AuthHeader = cfg.webhookAuthHeader
	}
	return wi
}

// run executes the pipeline in mode 1: BubbleteaInterviewer spins up an inline
// tea.Program for each human gate, then returns control to the pipeline goroutine.
func run(pipelineFile, workdir, checkpoint, format, backend string, verbose bool, jsonOut bool) error {
	graph, subgraphs, err := loadAndValidatePipeline(pipelineFile, format)
	if err != nil {
		return err
	}
	if err := applyRunParamOverrides(graph); err != nil {
		return err
	}

	tokenTracker := llm.NewTokenTracker()
	llmClient, err := prepareNativeLLMClient(tokenTracker, backend)
	if err != nil {
		return err
	}
	if llmClient != nil {
		defer llmClient.Close()
	}

	execEnv := exec.NewLocalEnvironment(workdir)
	interviewer := chooseInterviewer(isatty.IsTerminal(os.Stdin.Fd()), activeAutopilotCfg, llmClient, backend)
	if c, ok := interviewer.(canceller); ok {
		defer c.Cancel()
	}

	artifactDir := activeArtifactDir
	if artifactDir == "" {
		artifactDir = filepath.Join(workdir, ".tracker", "runs")
	}
	activityLog := pipeline.NewJSONLEventHandler(artifactDir)
	defer activityLog.Close()

	wireLLMTraceToLog(llmClient, activityLog)

	agentEventHandler, pipelineEventHandler := buildConsoleEventHandlers(
		activityLog, llmClient, verbose, jsonOut,
	)

	engineOpts := buildEngineOptions(artifactDir, checkpoint, pipelineEventHandler, graph)
	registry := handlers.NewDefaultRegistry(graph,
		handlers.WithLLMClient(llmClient, workdir),
		handlers.WithExecEnvironment(execEnv),
		handlers.WithInterviewer(interviewer, graph),
		handlers.WithAgentEventHandler(agentEventHandler),
		handlers.WithPipelineEventHandler(pipelineEventHandler),
		handlers.WithSubgraphs(subgraphs),
		handlers.WithDefaultBackend(backend),
		handlers.WithTokenTracker(tokenTracker),
	)

	engine := pipeline.NewEngine(graph, registry, engineOpts...)

	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt)
	defer cancel()

	result, runErr := engine.Run(ctx)

	pipelineErr := interpretRunResult(result, runErr)
	printRunSummary(result, pipelineErr, pipelineFile)
	if result != nil && result.RunID != "" {
		maybeExportBundle(artifactDir, result.RunID)
	}
	return pipelineErr
}

// prepareNativeLLMClient creates the LLM client, returning nil without error
// when an external backend is used and no native client is needed.
func prepareNativeLLMClient(tokenTracker *llm.TokenTracker, backend string) (*llm.Client, error) {
	client, err := buildLLMClient(tokenTracker)
	if err != nil && backend != "claude-code" && backend != "acp" {
		return nil, formatLLMClientError(err)
	}
	if err != nil {
		fmt.Fprintf(os.Stderr, "note: no native LLM client (%v) — using %s for all LLM calls\n", err, backend)
		return nil, nil
	}
	return client, nil
}

// wireLLMTraceToLog registers a trace observer that writes LLM events to the activity log.
func wireLLMTraceToLog(llmClient *llm.Client, activityLog *pipeline.JSONLEventHandler) {
	if llmClient != nil {
		llmClient.AddTraceObserver(llm.TraceObserverFunc(func(evt llm.TraceEvent) {
			activityLog.WriteLLMEvent(string(evt.Kind), evt.Provider, evt.Model, evt.ToolName, evt.Preview)
		}))
	}
}

// buildEngineOptions assembles the engine option slice from config values.
// Budget limits are the effective merge of activeBudgetLimits (CLI --max-*
// flags) over workflow-level defaults from graph.Attrs (populated by the
// dippin adapter from WorkflowDefaults.Max*). CLI flags always win.
func buildEngineOptions(artifactDir, checkpoint string, evtHandler pipeline.PipelineEventHandler, graph *pipeline.Graph) []pipeline.EngineOption {
	opts := []pipeline.EngineOption{
		pipeline.WithArtifactDir(artifactDir),
		pipeline.WithPipelineEventHandler(evtHandler),
		pipeline.WithStylesheetResolution(true),
	}
	if checkpoint != "" {
		opts = append(opts, pipeline.WithCheckpointPath(checkpoint))
	}
	effectiveBudget := tracker.ResolveBudgetLimits(activeBudgetLimits, graph)
	if guard := pipeline.NewBudgetGuard(effectiveBudget); guard != nil {
		opts = append(opts, pipeline.WithBudgetGuard(guard))
	}
	return opts
}

// interpretRunResult converts a raw engine run result into a pipeline-level error.
func interpretRunResult(result *pipeline.EngineResult, runErr error) error {
	if runErr != nil {
		return fmt.Errorf("pipeline execution: %w", runErr)
	}
	if result.Status != pipeline.OutcomeSuccess {
		return fmt.Errorf("pipeline finished with status: %s", result.Status)
	}
	return nil
}

// buildConsoleEventHandlers creates the agent and pipeline event handlers for
// console (non-TUI) mode, branching on whether JSON output is requested.
func buildConsoleEventHandlers(
	activityLog *pipeline.JSONLEventHandler,
	llmClient *llm.Client,
	verbose bool,
	jsonOut bool,
) (agent.EventHandler, pipeline.PipelineEventHandler) {
	// Agent event handler that always logs to activity log.
	logAgentEvent := func(evt agent.Event) {
		errMsg := ""
		if evt.Err != nil {
			errMsg = evt.Err.Error()
		}
		activityLog.WriteAgentEvent(string(evt.Type), evt.NodeID, evt.ToolName, evt.ToolOutput, evt.ToolError, evt.Text, errMsg, evt.Provider, evt.Model)
	}

	if jsonOut {
		return buildJSONEventHandlers(activityLog, llmClient, logAgentEvent)
	}
	return buildPlainEventHandlers(activityLog, llmClient, verbose, logAgentEvent)
}

// buildJSONEventHandlers creates event handlers for JSON streaming mode.
func buildJSONEventHandlers(
	activityLog *pipeline.JSONLEventHandler,
	llmClient *llm.Client,
	logAgentEvent func(agent.Event),
) (agent.EventHandler, pipeline.PipelineEventHandler) {
	stream := tracker.NewNDJSONWriter(os.Stdout)
	if llmClient != nil {
		llmClient.AddTraceObserver(stream.TraceObserver())
	}
	agentHandler := agent.EventHandlerFunc(func(evt agent.Event) {
		logAgentEvent(evt)
		stream.AgentHandler().HandleEvent(evt)
	})
	pipelineHandler := pipeline.PipelineMultiHandler(stream.PipelineHandler(), activityLog)
	return agentHandler, pipelineHandler
}

// buildPlainEventHandlers creates event handlers for human-readable console output.
func buildPlainEventHandlers(
	activityLog *pipeline.JSONLEventHandler,
	llmClient *llm.Client,
	verbose bool,
	logAgentEvent func(agent.Event),
) (agent.EventHandler, pipeline.PipelineEventHandler) {
	if llmClient != nil {
		llmClient.AddTraceObserver(llm.NewTraceLogger(os.Stdout, llm.TraceLoggerOptions{Verbose: verbose}))
	}
	agentHandler := agent.EventHandlerFunc(func(evt agent.Event) {
		logAgentEvent(evt)
		line := agent.FormatEventLine(evt)
		if line == "" {
			return
		}
		if evt.NodeID != "" {
			fmt.Fprintf(os.Stdout, "[%s] [%s] %s\n", time.Now().Format("15:04:05"), evt.NodeID, line)
		} else {
			fmt.Fprintf(os.Stdout, "[%s] %s\n", time.Now().Format("15:04:05"), line)
		}
	})
	pipelineHandler := pipeline.PipelineMultiHandler(
		&pipeline.LoggingEventHandler{Writer: os.Stdout},
		activityLog,
	)
	return agentHandler, pipelineHandler
}

// runTUI executes the pipeline in mode 2: a persistent dashboard TUI owns the
// terminal; the pipeline runs in a background goroutine; human gates open modal
// overlays on the dashboard.
// loadAndValidatePipeline loads, validates, and resolves subgraphs for a pipeline.
// Supports filesystem paths and bare workflow names via resolvePipelineSource.
func loadAndValidatePipeline(pipelineFile, format string) (*pipeline.Graph, map[string]*pipeline.Graph, error) {
	resolved, isEmbedded, info, err := resolvePipelineSource(pipelineFile)
	if err != nil {
		return nil, nil, err
	}

	var graph *pipeline.Graph
	if isEmbedded {
		graph, err = loadEmbeddedPipeline(info)
	} else {
		graph, err = loadPipeline(resolved, format)
	}
	if err != nil {
		return nil, nil, fmt.Errorf("load pipeline: %w", err)
	}
	if err := pipeline.Validate(graph); err != nil {
		return nil, nil, fmt.Errorf("validate pipeline: %w", err)
	}

	// Embedded workflows have no subgraphs (none of the 3 core pipelines use them).
	parentFile := resolved
	if isEmbedded {
		parentFile = info.File
	}
	subgraphs, err := loadSubgraphs(graph, parentFile)
	if err != nil {
		return nil, nil, fmt.Errorf("load subgraphs: %w", err)
	}
	if err := validateSubgraphRefs(graph, subgraphs); err != nil {
		return nil, nil, fmt.Errorf("subgraph validation: %w", err)
	}
	return graph, subgraphs, nil
}

func runTUI(pipelineFile, workdir, checkpoint, format, backend string, verbose bool) error {
	graph, subgraphs, err := loadAndValidatePipeline(pipelineFile, format)
	if err != nil {
		return err
	}
	if err := applyRunParamOverrides(graph); err != nil {
		return err
	}

	tokenTracker := llm.NewTokenTracker()
	llmClient, err := resolveLLMClient(tokenTracker, backend)
	if err != nil {
		return err
	}
	if llmClient != nil {
		defer llmClient.Close()
	}

	execEnv := exec.NewLocalEnvironment(workdir)
	pipelineName := resolvePipelineName(graph, pipelineFile)
	artifactDir := activeArtifactDir
	if artifactDir == "" {
		artifactDir = filepath.Join(workdir, ".tracker", "runs")
	}

	prog, store, activityLog, err := setupTUIProgram(graph, subgraphs, pipelineName, checkpoint, tokenTracker, llmClient, verbose, backend, artifactDir)
	if err != nil {
		return err
	}
	defer activityLog.Close()

	sendFn := tui.SendFunc(func(msg tea.Msg) { prog.Send(msg) })
	interviewer := chooseTUIInterviewer(sendFn, activeAutopilotCfg, llmClient, backend)
	if c, ok := interviewer.(canceller); ok {
		defer c.Cancel()
	}
	_ = store // store used only in setupTUIProgram

	pipelineCombo := buildTUIPipelineHandler(prog, activityLog, verbose, llmClient)

	registry := buildTUIRegistry(graph, llmClient, workdir, execEnv, interviewer, activityLog, pipelineCombo, subgraphs, backend, tokenTracker, prog)

	engine := buildTUIEngine(graph, registry, artifactDir, checkpoint, pipelineCombo)

	outcome, err := runTUIWithEngine(engine, prog)
	if err != nil {
		return err
	}

		printRunSummary(outcome.result, outcome.err, pipelineFile)
	notifyPipelineComplete(pipelineName, outcome.err)
	if outcome.result != nil && outcome.result.RunID != "" {
		maybeExportBundle(artifactDir, outcome.result.RunID)
	}
	return outcome.err
}

// resolveLLMClient builds the LLM client, handling non-fatal failures for headless backends.
func resolveLLMClient(tokenTracker *llm.TokenTracker, backend string) (*llm.Client, error) {
	llmClient, err := buildLLMClient(tokenTracker)
	if err != nil && backend != "claude-code" && backend != "acp" {
		return nil, formatLLMClientError(err)
	}
	if err != nil {
		fmt.Fprintf(os.Stderr, "note: no native LLM client (%v) — using %s for all LLM calls\n", err, backend)
	}
	return llmClient, nil
}

// runTUIWithEngine runs the TUI program and waits for pipeline completion.
func runTUIWithEngine(engine *pipeline.Engine, prog *tea.Program) (pipelineOutcome, error) {
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt)
	defer cancel()

	outcomeCh := runPipelineAsync(engine, ctx, prog)

	_, err := prog.Run()
	cancel()
	if err != nil {
		return pipelineOutcome{}, fmt.Errorf("TUI program: %w", err)
	}

	return waitForPipelineOutcome(outcomeCh), nil
}

// notifyPipelineComplete sends a system notification for pipeline completion.
func notifyPipelineComplete(pipelineName string, pipelineErr error) {
	status := "completed"
	if pipelineErr != nil {
		status = "failed"
	}
	tui.SendNotification("Tracker: "+pipelineName, "Pipeline "+status)
}

// resolvePipelineName returns the pipeline display name from graph or filename.
func resolvePipelineName(graph *pipeline.Graph, pipelineFile string) string {
	if graph.Name != "" {
		return graph.Name
	}
	base := filepath.Base(pipelineFile)
	ext := filepath.Ext(base)
	return base[:len(base)-len(ext)]
}

// setupTUIProgram creates the TUI model, state store, and activity log.
func setupTUIProgram(graph *pipeline.Graph, subgraphs map[string]*pipeline.Graph, pipelineName, checkpoint string, tokenTracker *llm.TokenTracker, llmClient *llm.Client, verbose bool, backend, artifactDir string) (*tea.Program, *tui.StateStore, *pipeline.JSONLEventHandler, error) {
	store := tui.NewStateStore(tokenTracker)
	appModel := tui.NewAppModel(store, pipelineName, "")
	appModel.SetVerboseTrace(verbose)
	configureTUIHeader(appModel, backend, activeAutopilotCfg)
	nodeList := buildNodeList(graph, subgraphs)
	appModel.SetInitialNodes(nodeList)

	if checkpoint != "" {
		preMarkCompletedNodes(checkpoint, nodeList, store)
	}

	prog := tea.NewProgram(appModel, tea.WithAltScreen())
	activityLog := pipeline.NewJSONLEventHandler(artifactDir)
	return prog, store, activityLog, nil
}

func applyRunParamOverrides(graph *pipeline.Graph) error {
	activeEffectiveRunParams = nil
	if len(activeRunParams) == 0 {
		return nil
	}
	if err := pipeline.ApplyGraphParamOverrides(graph, activeRunParams); err != nil {
		return fmt.Errorf("apply --param overrides: %w", err)
	}
	effective := make(map[string]string, len(activeRunParams))
	for key := range activeRunParams {
		effective[key] = graph.Attrs[pipeline.GraphParamAttrKey(key)]
	}
	activeEffectiveRunParams = effective
	return nil
}

func formatParamOverridesForSummary(params map[string]string) string {
	if len(params) == 0 {
		return ""
	}
	var pairs []string
	for _, key := range slices.Sorted(maps.Keys(params)) {
		pairs = append(pairs, fmt.Sprintf("%s=%s", key, params[key]))
	}
	return strings.Join(pairs, ", ")
}

// buildTUIPipelineHandler wires LLM trace events to TUI+activity log and returns the combined handler.
func buildTUIPipelineHandler(prog *tea.Program, activityLog *pipeline.JSONLEventHandler, verbose bool, llmClient *llm.Client) pipeline.PipelineEventHandler {
	if llmClient != nil {
		llmClient.AddTraceObserver(llm.TraceObserverFunc(func(evt llm.TraceEvent) {
			for _, m := range tui.AdaptLLMTraceEvent(evt, "", verbose) {
				prog.Send(m)
			}
			activityLog.WriteLLMEvent(string(evt.Kind), evt.Provider, evt.Model, evt.ToolName, evt.Preview)
		}))
	}
	pipelineHandler := pipeline.PipelineEventHandlerFunc(func(evt pipeline.PipelineEvent) {
		if msg := tui.AdaptPipelineEvent(evt); msg != nil {
			prog.Send(msg)
		}
	})
	return pipeline.PipelineMultiHandler(pipelineHandler, activityLog)
}

// buildTUIRegistry builds the handler registry for TUI mode.
func buildTUIRegistry(graph *pipeline.Graph, llmClient *llm.Client, workdir string, execEnv *exec.LocalEnvironment, interviewer handlers.LabeledFreeformInterviewer, activityLog *pipeline.JSONLEventHandler, pipelineCombo pipeline.PipelineEventHandler, subgraphs map[string]*pipeline.Graph, backend string, tokenTracker *llm.TokenTracker, prog *tea.Program) *pipeline.HandlerRegistry {
	return handlers.NewDefaultRegistry(graph,
		handlers.WithLLMClient(llmClient, workdir),
		handlers.WithExecEnvironment(execEnv),
		handlers.WithInterviewer(interviewer, graph),
		handlers.WithAgentEventHandler(agent.EventHandlerFunc(func(evt agent.Event) {
			if msg := tui.AdaptAgentEvent(evt, evt.NodeID); msg != nil {
				prog.Send(msg)
			}
			errMsg := ""
			if evt.Err != nil {
				errMsg = evt.Err.Error()
			}
			activityLog.WriteAgentEvent(string(evt.Type), evt.NodeID, evt.ToolName, evt.ToolOutput, evt.ToolError, evt.Text, errMsg, evt.Provider, evt.Model)
		})),
		handlers.WithPipelineEventHandler(pipelineCombo),
		handlers.WithSubgraphs(subgraphs),
		handlers.WithDefaultBackend(backend),
		handlers.WithTokenTracker(tokenTracker),
	)
}

// buildTUIEngine creates and configures the pipeline engine for TUI mode.
func buildTUIEngine(graph *pipeline.Graph, registry *pipeline.HandlerRegistry, artifactDir, checkpoint string, pipelineCombo pipeline.PipelineEventHandler) *pipeline.Engine {
	var engineOpts []pipeline.EngineOption
	engineOpts = append(engineOpts, pipeline.WithArtifactDir(artifactDir))
	engineOpts = append(engineOpts, pipeline.WithPipelineEventHandler(pipelineCombo))
	engineOpts = append(engineOpts, pipeline.WithStylesheetResolution(true))
	if checkpoint != "" {
		engineOpts = append(engineOpts, pipeline.WithCheckpointPath(checkpoint))
	}
	effectiveBudget := tracker.ResolveBudgetLimits(activeBudgetLimits, graph)
	if guard := pipeline.NewBudgetGuard(effectiveBudget); guard != nil {
		engineOpts = append(engineOpts, pipeline.WithBudgetGuard(guard))
	}
	return pipeline.NewEngine(graph, registry, engineOpts...)
}

// pipelineOutcome holds the result of a pipeline run.
type pipelineOutcome struct {
	result *pipeline.EngineResult
	err    error
}

// runPipelineAsync starts the pipeline in a background goroutine and returns the outcome channel.
func runPipelineAsync(engine *pipeline.Engine, ctx context.Context, prog *tea.Program) chan pipelineOutcome {
	outcomeCh := make(chan pipelineOutcome, 1)
	go func() {
		result, pipelineErr := engine.Run(ctx)
		if pipelineErr == nil && result.Status != pipeline.OutcomeSuccess {
			pipelineErr = fmt.Errorf("pipeline finished with status: %s", result.Status)
		}
		outcomeCh <- pipelineOutcome{result: result, err: pipelineErr}
		prog.Send(tui.MsgPipelineDone{Err: pipelineErr})
	}()
	return outcomeCh
}

// waitForPipelineOutcome waits for the pipeline to finish, with a 5s timeout.
func waitForPipelineOutcome(outcomeCh chan pipelineOutcome) pipelineOutcome {
	select {
	case outcome := <-outcomeCh:
		return outcome
	case <-time.After(5 * time.Second):
		return pipelineOutcome{err: fmt.Errorf("pipeline did not exit within 5s after TUI closed")}
	}
}

// preMarkCompletedNodes loads a checkpoint and marks completed nodes in the TUI store.
func preMarkCompletedNodes(checkpoint string, nodeList []tui.NodeEntry, store *tui.StateStore) {
	cp, cpErr := pipeline.LoadCheckpoint(checkpoint)
	if cpErr != nil {
		fmt.Fprintf(os.Stderr, "warning: could not load checkpoint for TUI: %v\n", cpErr)
		return
	}
	for _, n := range nodeList {
		if cp.IsCompleted(n.ID) {
			store.Apply(tui.MsgNodeCompleted{NodeID: n.ID, Outcome: "resumed"})
		}
	}
}

// buildLLMClient constructs the LLM client from environment variables with
// custom base URL support and attaches the token tracker middleware.
func buildLLMClient(tokenTracker *llm.TokenTracker) (*llm.Client, error) {
	constructors := buildProviderConstructors()

	client, err := llm.NewClientFromEnv(constructors)
	if err != nil {
		return nil, err
	}

	// Wire infra-level retry middleware. Handles transient provider errors
	// (502, 503, 429, timeouts) transparently so pipeline-level retries are
	// reserved for actual node logic failures.
	client.AddMiddleware(llm.NewRetryMiddleware(
		llm.WithMaxRetries(3),
		llm.WithBaseDelay(2*time.Second),
	))

	// Wire token tracker as middleware.
	if tokenTracker != nil {
		client.AddMiddleware(tokenTracker)
	}

	return client, nil
}

// buildProviderConstructors returns the map of provider name → adapter constructor.
func buildProviderConstructors() map[string]func(string) (llm.ProviderAdapter, error) {
	return map[string]func(string) (llm.ProviderAdapter, error){
		"anthropic":     buildAnthropicConstructor(),
		"openai":        buildOpenAIConstructor(),
		"gemini":        buildGeminiConstructor(),
		"openai-compat": buildOpenAICompatConstructor(),
	}
}

// resolveProviderBaseURLFromEnv resolves the base URL for a provider using the
// same priority order as tracker.ResolveProviderBaseURL:
//  1. Per-provider *_BASE_URL env var (always wins).
//  2. TRACKER_GATEWAY_URL with provider suffix (set by --gateway-url before
//     buildLLMClient runs, or by the user directly).
//  3. Empty string → use provider SDK default.
func resolveProviderBaseURLFromEnv(envKey, suffix string) string {
	if v := os.Getenv(envKey); v != "" {
		return v
	}
	gateway := os.Getenv("TRACKER_GATEWAY_URL")
	if gateway == "" {
		return ""
	}
	// Strip trailing slash to prevent double-slash URLs.
	for len(gateway) > 0 && gateway[len(gateway)-1] == '/' {
		gateway = gateway[:len(gateway)-1]
	}
	return gateway + suffix
}

func buildAnthropicConstructor() func(string) (llm.ProviderAdapter, error) {
	return func(key string) (llm.ProviderAdapter, error) {
		var opts []anthropic.Option
		if base := resolveProviderBaseURLFromEnv("ANTHROPIC_BASE_URL", "/anthropic"); base != "" {
			opts = append(opts, anthropic.WithBaseURL(base))
		}
		return anthropic.New(key, opts...), nil
	}
}

func buildOpenAIConstructor() func(string) (llm.ProviderAdapter, error) {
	return func(key string) (llm.ProviderAdapter, error) {
		var opts []openai.Option
		if base := resolveProviderBaseURLFromEnv("OPENAI_BASE_URL", "/openai"); base != "" {
			opts = append(opts, openai.WithBaseURL(base))
		}
		return openai.New(key, opts...), nil
	}
}

func buildGeminiConstructor() func(string) (llm.ProviderAdapter, error) {
	return func(key string) (llm.ProviderAdapter, error) {
		var opts []google.Option
		if base := resolveProviderBaseURLFromEnv("GEMINI_BASE_URL", "/google-ai-studio"); base != "" {
			opts = append(opts, google.WithBaseURL(base))
		}
		return google.New(key, opts...), nil
	}
}

func buildOpenAICompatConstructor() func(string) (llm.ProviderAdapter, error) {
	return func(key string) (llm.ProviderAdapter, error) {
		var opts []openaicompat.Option
		if base := resolveProviderBaseURLFromEnv("OPENAI_COMPAT_BASE_URL", "/compat"); base != "" {
			opts = append(opts, openaicompat.WithBaseURL(base))
		}
		return openaicompat.New(key, opts...), nil
	}
}

// chooseInterviewer selects the interviewer implementation based on config.
// Priority: --auto-approve > --webhook-url > --autopilot > terminal detection.
// When backend is claude-code and autopilot is active, routes gate decisions
// through the claude CLI subprocess instead of the native LLM client.
func chooseInterviewer(isTerminal bool, cfg autopilotCfg, llmClient *llm.Client, backend string) handlers.FreeformInterviewer {
	if cfg.autoApprove {
		return &handlers.AutoApproveFreeformInterviewer{}
	}
	if activeWebhookGate != nil {
		return newWebhookInterviewerFromCfg(activeWebhookGate)
	}
	if cfg.persona != "" {
		return chooseAutopilotInterviewer(cfg.persona, llmClient, backend)
	}
	if isTerminal {
		return tui.NewMode1Interviewer()
	}
	return handlers.NewConsoleInterviewer()
}

// chooseAutopilotInterviewer resolves the best FreeformInterviewer for autopilot mode.
// Prefers claude-code subprocess when backend matches, falls back to native LLM client.
func chooseAutopilotInterviewer(persona string, llmClient *llm.Client, backend string) handlers.FreeformInterviewer {
	p, err := handlers.ParsePersona(persona)
	if err != nil {
		fmt.Fprintf(os.Stderr, "warning: %v, falling back to auto-approve\n", err)
		return &handlers.AutoApproveFreeformInterviewer{}
	}
	if backend == "claude-code" {
		ccAutopilot, ccErr := handlers.NewClaudeCodeAutopilotInterviewer(p)
		if ccErr != nil {
			fmt.Fprintf(os.Stderr, "warning: claude-code autopilot init failed (%v), falling back to native\n", ccErr)
		} else {
			return ccAutopilot
		}
	}
	if llmClient == nil {
		fmt.Fprintf(os.Stderr, "warning: no LLM client for autopilot, falling back to auto-approve\n")
		return &handlers.AutoApproveFreeformInterviewer{}
	}
	return handlers.NewAutopilotInterviewer(llmClient, p)
}

// configureTUIHeader sets backend and autopilot tags on the TUI header bar.
func configureTUIHeader(app *tui.AppModel, backend string, cfg autopilotCfg) {
	if backend != "" && backend != "native" {
		app.Header().SetBackend(backend)
	}
	if cfg.persona != "" {
		app.Header().SetAutopilot(cfg.persona)
	}
}

// chooseTUIInterviewer selects the Mode 2 (persistent TUI) interviewer.
// If autopilot is active, wraps it so decisions flash in the TUI modal.
// When backend is claude-code, routes autopilot through the claude subprocess.
func chooseTUIInterviewer(send tui.SendFunc, cfg autopilotCfg, llmClient *llm.Client, backend string) handlers.LabeledFreeformInterviewer {
	if cfg.autoApprove {
		return &handlers.AutoApproveFreeformInterviewer{}
	}
	if activeWebhookGate != nil {
		return newWebhookInterviewerFromCfg(activeWebhookGate)
	}
	if cfg.persona != "" {
		persona, _ := handlers.ParsePersona(cfg.persona)
		// Use claude-code autopilot when backend is claude-code.
		if backend == "claude-code" {
			ccAutopilot, ccErr := handlers.NewClaudeCodeAutopilotInterviewer(persona)
			if ccErr == nil {
				return tui.NewAutopilotTUIInterviewer(ccAutopilot, send)
			}
			fmt.Fprintf(os.Stderr, "warning: claude-code autopilot init failed (%v), falling back to native\n", ccErr)
		}
		if llmClient != nil {
			autopilot := handlers.NewAutopilotInterviewer(llmClient, persona)
			return tui.NewAutopilotTUIInterviewer(autopilot, send)
		}
		fmt.Fprintf(os.Stderr, "warning: no LLM client for autopilot, falling back to interactive\n")
	}
	return tui.NewBubbleteaInterviewer(send)
}

// maybeExportBundle exports a git bundle of the run artifact repository when
// --export-bundle is set. Best-effort: failures are printed as warnings and do
// not affect the pipeline exit code. The run dir is <artifactBase>/<runID>.
func maybeExportBundle(artifactBase, runID string) {
	if activeExportBundle == "" {
		return
	}
	runDir := filepath.Join(artifactBase, runID)
	if err := tracker.ExportBundle(runDir, activeExportBundle); err != nil {
		fmt.Fprintf(os.Stderr, "warning: bundle export failed: %v\n", err)
		return
	}
	fmt.Fprintf(os.Stdout, "  bundle: %s\n", activeExportBundle)
}
