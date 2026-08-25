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

// autopilotCfg holds just the autopilot settings needed by the interviewer
// selection. Carried on runOptions from executeRun into run/runTUI.
type autopilotCfg struct {
	persona     string // lax/mid/hard/mentor or empty
	autoApprove bool
}

// applyFailOnOverrideEnv reads TRACKER_FAIL_ON_OVERRIDE and sets
// cfg.failOnOverride if it isn't already true. Strict "=1" parsing matches
// the TRACKER_PASS_* convention (TRACKER_PASS_API_KEYS, TRACKER_PASS_ENV).
// Truthy-looking values like "true", "yes", "TRUE" are deliberately rejected
// so the env-var contract stays narrow and predictable.
//
// The flag-set value always wins: a --fail-on-override flag survives an
// absent/zero env var, and the env var never *unsets* the flag.
func applyFailOnOverrideEnv(cfg *runConfig) {
	if cfg.failOnOverride {
		return
	}
	if os.Getenv("TRACKER_FAIL_ON_OVERRIDE") == "1" {
		cfg.failOnOverride = true
	}
}

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

// applyGitPreflight runs the v0.29.0 git preflight check using the git config
// threaded on runOptions. Called from both run() and runTUI() after
// applyRunParamOverrides — so the check fires before any LLM client setup or
// network activity. Bail on error so the user sees the actionable remediation
// instead of a deferred failure.
//
// Takes a context so Ctrl+C during slow git probes (network drives,
// dubious-ownership prompts, hung remotes) or during the optional
// `git init` side effect of `--git=init` propagates cleanly. The
// caller threads a signal.NotifyContext created before the LLM client
// setup so cancellation works uniformly across preflight and engine.
func applyGitPreflight(ctx context.Context, graph *pipeline.Graph, workdir string, git gitPreflightCfg) error {
	// Sandbox device-node hygiene (#423): verify standard device nodes (at
	// minimum /dev/null is a usable char device) BEFORE any git or subprocess
	// handler runs. A suspended/restored sandbox can corrupt /dev/null, which
	// silently breaks git and reviewer CLIs deep mid-run. Runs ahead of (and
	// independent of) the git policy below, since subprocess handlers depend on
	// the device regardless of whether the workflow requires git.
	if err := checkDeviceNodes(nil); err != nil {
		return err
	}
	return pipeline.Preflight(ctx, pipeline.PreflightConfig{
		WorkDir:        workdir,
		Requires:       graph.RequiredDeps(),
		Policy:         pipeline.GitPreflight(git.policy),
		AllowInit:      git.allowInit,
		InteractiveTTY: isatty.IsTerminal(os.Stdin.Fd()),
		Warner: func(format string, args ...any) {
			fmt.Fprintf(os.Stderr, "warning: "+format+"\n", args...)
		},
	})
}

// run executes the pipeline in mode 1: BubbleteaInterviewer spins up an inline
// tea.Program for each human gate, then returns control to the pipeline goroutine.
func run(opts *runOptions) error {
	// Signal context lives across preflight + engine so Ctrl+C during a
	// slow git probe or auto-init also aborts cleanly. Pre-fix the
	// preflight used context.Background and only the engine got the
	// signal context, so Ctrl+C couldn't interrupt the preflight branch.
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt)
	defer cancel()

	graph, subgraphs, bundleInfo, effectiveParams, err := loadAndPreflightPipeline(ctx, opts)
	if err != nil {
		return err
	}

	artifactDir := resolveArtifactDir(opts.workdir, opts.artifactDir)
	agentHandler, pipelineHandler, traceObs := buildConsoleEventHandlers(opts.verbose, opts.jsonOut)

	cfg := tracker.Config{
		WorkingDir:     opts.workdir,
		CheckpointDir:  opts.checkpoint,
		ArtifactDir:    artifactDir,
		Backend:        opts.backend,
		Budget:         opts.budget,
		GatewayURL:     opts.gatewayURL,
		GatewayKind:    tracker.GatewayKind(opts.gatewayKind),
		Subgraphs:      subgraphs,
		BundleIdentity: bundleInfo.Identity,
		ToolSafety:     &opts.toolSafety,
		// loadAndPreflightPipeline already ran the CLI git preflight (with TTY
		// prompting); disable the library's non-interactive one.
		Git:          &tracker.GitConfig{Preflight: tracker.GitPreflightOff},
		EventHandler: pipelineHandler,
		AgentEvents:  agentHandler,
		LLMTrace:     traceObs,
		// Run capture (run.json + spec + identity-bearing activity.jsonl) is
		// library-owned via Config.Capture — the same path an embedder uses — so
		// the CLI and library share one wiring. The console handlers above stay
		// presentation-only; the capture handler is combined in by the library.
		Capture: buildCaptureConfig(opts.verbose, opts.resume),
	}
	applyInterviewerToConfig(&cfg, opts, isatty.IsTerminal(os.Stdin.Fd()))

	eng, err := tracker.NewEngineFromGraph(ctx, graph, cfg)
	if err != nil {
		return err
	}
	defer eng.Close()

	res, runErr := eng.Run(ctx)
	return finishRun(engineResultOf(res), runErr, opts, effectiveParams, artifactDir)
}

// engineResultOf extracts the pipeline.EngineResult from a tracker.Result (nil-safe).
func engineResultOf(res *tracker.Result) *pipeline.EngineResult {
	if res == nil {
		return nil
	}
	return res.EngineResult
}

// applyInterviewerToConfig translates the CLI's interviewer selection
// (auto-approve, webhook, autopilot persona, or interactive) into tracker.Config
// fields so the library owns the interviewer and its lifecycle/cleanup. Mirrors
// the priority in the former chooseInterviewer.
func applyInterviewerToConfig(cfg *tracker.Config, opts *runOptions, isTerminal bool) {
	switch {
	case opts.autopilot.autoApprove:
		cfg.AutoApprove = true
	case opts.webhookGate != nil:
		cfg.WebhookGate = toTrackerWebhookGate(opts.webhookGate)
	case opts.autopilot.persona != "":
		cfg.Autopilot = opts.autopilot.persona
	default:
		cfg.Interviewer = interactiveInterviewer(isTerminal)
	}
}

// interactiveInterviewer returns the human interviewer for an interactive plain
// run: an inline per-gate bubbletea modal on a TTY, else a stdin/stdout console.
func interactiveInterviewer(isTerminal bool) handlers.Interviewer {
	if isTerminal {
		return tui.NewMode1Interviewer()
	}
	return handlers.NewConsoleInterviewer()
}

// toTrackerWebhookGate maps the CLI webhook gate config to the library config.
func toTrackerWebhookGate(w *webhookGateCfg) *tracker.WebhookGateConfig {
	return &tracker.WebhookGateConfig{
		WebhookURL:    w.webhookURL,
		CallbackAddr:  w.gateCallbackAddr,
		Timeout:       w.gateTimeout,
		TimeoutAction: w.gateTimeoutAction,
		AuthHeader:    w.webhookAuthHeader,
	}
}

// finishRun interprets the engine result, prints the summary, and exports the
// run bundle when a run ID is present. Extracted from run for the complexity
// gate; returns the user-facing pipeline error.
func finishRun(result *pipeline.EngineResult, runErr error, opts *runOptions, effectiveParams map[string]string, artifactDir string) error {
	pipelineErr := interpretRunResult(result, runErr, &runConfig{failOnOverride: opts.failOnOverride})
	printRunSummary(result, pipelineErr, opts.pipelineFile, effectiveParams)
	if result != nil && result.RunID != "" {
		// Spec + run.json are written by the library (Config.Capture) inside
		// eng.Run; the CLI only exports the bundle here.
		maybeExportBundle(artifactDir, result.RunID, opts.exportBundle)
	}
	return pipelineErr
}

// loadAndPreflightPipeline loads + validates the pipeline, applies --param
// overrides, and runs the device/git preflight. Shared prelude for run and
// runTUI; extracted to keep both under the complexity gate.
func loadAndPreflightPipeline(ctx context.Context, opts *runOptions) (*pipeline.Graph, map[string]*pipeline.Graph, pipeline.BundleInfo, map[string]string, error) {
	graph, subgraphs, bundleInfo, err := loadAndValidatePipeline(opts.pipelineFile, opts.format)
	if err != nil {
		return nil, nil, pipeline.BundleInfo{}, nil, err
	}
	effectiveParams, err := applyRunParamOverrides(graph, opts.params)
	if err != nil {
		return nil, nil, pipeline.BundleInfo{}, nil, err
	}
	if err := applyGitPreflight(ctx, graph, opts.workdir, opts.git); err != nil {
		return nil, nil, pipeline.BundleInfo{}, nil, err
	}
	return graph, subgraphs, bundleInfo, effectiveParams, nil
}

// resolveArtifactDir returns the configured artifact dir, defaulting to
// <workdir>/.tracker/runs when none was set. Extracted from run/runTUI for the
// complexity gate.
func resolveArtifactDir(workdir, artifactDir string) string {
	if artifactDir != "" {
		return artifactDir
	}
	return filepath.Join(workdir, ".tracker", "runs")
}

// buildCaptureConfig assembles the tracker.CaptureConfig that turns on
// library-owned run capture for a CLI run. The spec (source + expanded IR +
// path) comes from capturedSpec, recorded at load time; --verbose enables raw
// LLM capture; and a forced bundle-mismatch resume is threaded through so the
// library writes the bundle_mismatch_forced audit entry before the engine
// fires. The .dipx bundle identity itself rides Config.BundleIdentity, which
// the library stamps onto agent/llm writes and into the spec manifest.
//
// This is the single capture wiring the CLI shares with library embedders
// (tracker.Config.Capture) — the JSONLEventHandler, the SessionOwned trace
// de-dup, and the spec/run.json finalization all live in the library now, so
// the two cannot drift.
func buildCaptureConfig(verbose bool, resume resumeInfo) *tracker.CaptureConfig {
	cc := &tracker.CaptureConfig{
		Source:        capturedSpec.source,
		SourcePath:    capturedSpec.path,
		Workflow:      capturedSpec.workflow,
		CaptureRawLLM: verbose,
	}
	if resume.BundleMismatchForced {
		cc.ForcedBundleMismatch = &tracker.ForcedBundleMismatch{
			RunID:            resume.RunID,
			OriginalIdentity: resume.OriginalIdentity,
			CurrentIdentity:  resume.CurrentIdentity,
		}
	}
	return cc
}

// interpretRunResult converts a raw engine run result into a pipeline-level
// error.
//
// Mapping:
//   - runErr != nil               -> wrapped engine error (exit 1)
//   - Status.IsSuccess() && !flag -> nil (exit 0); covers both success and
//     validation_overridden by default
//   - Status==validation_overridden && --fail-on-override -> ErrValidationOverridden
//     (exit 2 — distinct from generic fail)
//   - Any other status            -> generic "pipeline finished with status: X"
//     error (exit 1); failure dominates --fail-on-override.
//
// Note: runErr precedence comes first so a low-level engine crash is surfaced
// even on a paper-success status, and the override sentinel only fires on the
// no-runErr path.
func interpretRunResult(result *pipeline.EngineResult, runErr error, cfg *runConfig) error {
	if runErr != nil {
		return fmt.Errorf("pipeline execution: %w", runErr)
	}
	if result.Status == pipeline.OutcomeValidationOverridden && cfg != nil && cfg.failOnOverride {
		head := headlineOverride(result.ValidationOverrides)
		fmt.Fprintf(os.Stderr,
			"tracker: run completed via %s at %q (label %q); --fail-on-override caused non-zero exit\n",
			result.Status, head.GateNodeID, head.Label)
		return pipeline.ErrValidationOverridden
	}
	if !result.Status.IsSuccess() {
		return fmt.Errorf("pipeline finished with status: %s", result.Status)
	}
	return nil
}

// headlineOverride returns the latest entry from in (per spec D5a — the audit
// header picks the newest entry as the "headline" since it's the override that
// drove the run to its terminal exit). Returns a zero-value OverrideDetail for
// empty input so callers can format %q on the bare fields without nil checks.
func headlineOverride(in []pipeline.OverrideDetail) pipeline.OverrideDetail {
	if len(in) == 0 {
		return pipeline.OverrideDetail{}
	}
	return in[len(in)-1]
}

// buildConsoleEventHandlers returns the presentation-only agent + pipeline
// event handlers and LLM trace observer for a plain/JSON (non-TUI) run,
// branching on whether JSON output is requested. Activity-log capture is no
// longer mirrored here: Config.Capture owns the JSONLEventHandler and combines
// it with these handlers, so this returns pure console/NDJSON output.
func buildConsoleEventHandlers(
	verbose bool,
	jsonOut bool,
) (agent.EventHandler, pipeline.PipelineEventHandler, llm.TraceObserver) {
	if jsonOut {
		return buildJSONEventHandlers()
	}
	return buildPlainEventHandlers(verbose)
}

// buildJSONEventHandlers creates the NDJSON streaming handlers for JSON mode.
func buildJSONEventHandlers() (agent.EventHandler, pipeline.PipelineEventHandler, llm.TraceObserver) {
	stream := tracker.NewNDJSONWriter(os.Stdout)
	return stream.AgentHandler(), stream.PipelineHandler(), stream.TraceObserver()
}

// buildPlainEventHandlers creates the human-readable console handlers.
func buildPlainEventHandlers(verbose bool) (agent.EventHandler, pipeline.PipelineEventHandler, llm.TraceObserver) {
	agentHandler := agent.EventHandlerFunc(func(evt agent.Event) {
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
	pipelineHandler := &pipeline.LoggingEventHandler{Writer: os.Stdout}
	stdoutTrace := llm.NewTraceLogger(os.Stdout, llm.TraceLoggerOptions{Verbose: verbose})
	return agentHandler, pipelineHandler, stdoutTrace
}

// runTUI executes the pipeline in mode 2: a persistent dashboard TUI owns the
// terminal; the pipeline runs in a background goroutine; human gates open modal
// overlays on the dashboard.
// loadAndValidatePipeline loads, validates, and resolves subgraphs for a pipeline.
// Supports filesystem paths and bare workflow names via resolvePipelineSource,
// plus sealed .dipx bundles via loadPipelineAndBundle. The returned BundleInfo
// is zero-valued for .dip files and embedded workflows; for .dipx bundles it
// carries the content-addressed identity, entry path, and manifest.
func loadAndValidatePipeline(pipelineFile, format string) (*pipeline.Graph, map[string]*pipeline.Graph, pipeline.BundleInfo, error) {
	resolved, isEmbedded, info, err := resolvePipelineSource(pipelineFile)
	if err != nil {
		return nil, nil, pipeline.BundleInfo{}, err
	}

	graph, subgraphs, bundle, err := loadGraphAndSubgraphs(resolved, format, info, isEmbedded)
	if err != nil {
		return nil, nil, pipeline.BundleInfo{}, err
	}

	if err := pipeline.Validate(graph); err != nil {
		return nil, nil, pipeline.BundleInfo{}, fmt.Errorf("validate pipeline: %w", err)
	}
	if err := validateSubgraphRefs(graph, subgraphs); err != nil {
		return nil, nil, pipeline.BundleInfo{}, fmt.Errorf("subgraph validation: %w", err)
	}
	return graph, subgraphs, bundle, nil
}

// loadGraphAndSubgraphs loads the graph + subgraphs from either an embedded
// workflow or a filesystem path / .dipx bundle. Extracted from
// loadAndValidatePipeline for the complexity gate.
func loadGraphAndSubgraphs(resolved, format string, info WorkflowInfo, isEmbedded bool) (*pipeline.Graph, map[string]*pipeline.Graph, pipeline.BundleInfo, error) {
	if !isEmbedded {
		graph, subgraphs, bundle, err := loadPipelineAndBundle(resolved, format)
		if err != nil {
			return nil, nil, pipeline.BundleInfo{}, fmt.Errorf("load pipeline: %w", err)
		}
		// A packed .dipx has no source dir, so ${graph.workflow_dir} would
		// expand to "" and abort under `set -eu`. Fail loud before running (#430).
		if err := guardPackedWorkflowDir(graph, bundle.Identity != ""); err != nil {
			return nil, nil, pipeline.BundleInfo{}, err
		}
		return graph, subgraphs, bundle, nil
	}
	// Embedded workflows have no subgraphs (none of the 3 core pipelines use them).
	graph, err := loadEmbeddedPipeline(info)
	if err != nil {
		return nil, nil, pipeline.BundleInfo{}, fmt.Errorf("load pipeline: %w", err)
	}
	subgraphs, err := loadSubgraphs(graph, info.File)
	if err != nil {
		return nil, nil, pipeline.BundleInfo{}, fmt.Errorf("load subgraphs: %w", err)
	}
	return graph, subgraphs, pipeline.BundleInfo{}, nil
}

func runTUI(opts *runOptions) error {
	// Signal context covers preflight + engine for consistent Ctrl+C
	// handling. The TUI's tea.Program owns the terminal once running,
	// but preflight runs before that, so a slow git probe needs an
	// interruptible context here too.
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt)
	defer cancel()

	graph, subgraphs, bundleInfo, effectiveParams, err := loadAndPreflightPipeline(ctx, opts)
	if err != nil {
		return err
	}

	// The token tracker is shared between the TUI view model (StateStore) and
	// the engine, so the dashboard's live cost readout matches the run. The
	// client is built bare (no token-tracker middleware); the library attaches
	// the shared tracker exactly once via Config.TokenTracker.
	tokenTracker := llm.NewTokenTracker()
	llmClient, err := resolveLLMClient(nil, opts.backend)
	if err != nil {
		return err
	}
	if llmClient != nil {
		defer llmClient.Close()
	}

	pipelineName := resolvePipelineName(graph, opts.pipelineFile)
	artifactDir := resolveArtifactDir(opts.workdir, opts.artifactDir)

	prog, _, err := setupTUIProgram(graph, subgraphs, pipelineName, opts.checkpoint, tokenTracker, llmClient, opts.verbose, opts.backend, opts.autopilot)
	if err != nil {
		return err
	}

	sendFn := tui.SendFunc(func(msg tea.Msg) { prog.Send(msg) })
	interviewer := chooseTUIInterviewer(sendFn, opts.autopilot, llmClient, opts.backend, opts.webhookGate)

	cfg := tracker.Config{
		WorkingDir:     opts.workdir,
		CheckpointDir:  opts.checkpoint,
		ArtifactDir:    artifactDir,
		Backend:        opts.backend,
		Budget:         opts.budget,
		GatewayURL:     opts.gatewayURL,
		GatewayKind:    tracker.GatewayKind(opts.gatewayKind),
		Subgraphs:      subgraphs,
		BundleIdentity: bundleInfo.Identity,
		ToolSafety:     &opts.toolSafety,
		Git:            &tracker.GitConfig{Preflight: tracker.GitPreflightOff},
		EventHandler:   buildTUIPipelineHandler(prog),
		AgentEvents:    buildTUIAgentHandler(prog),
		LLMTrace:       buildTUITraceObserver(prog, opts.verbose),
		TokenTracker:   tokenTracker,
		Interviewer:    interviewer, // cancelled by eng.Close() if it is a canceller
		// Library-owned run capture (activity.jsonl + spec + run.json), same
		// path as the non-TUI run() and library embedders. The TUI handlers
		// above stay prog.Send-only; the library combines in the capture writes.
		Capture: buildCaptureConfig(opts.verbose, opts.resume),
	}
	// Guard the typed-nil trap: llmClient is a *llm.Client; assigning a nil
	// pointer to the agent.Completer interface field would make it non-nil
	// (interface-wrapping-nil), defeating resolveCompleter's env-build fallback
	// and risking a nil-deref on a native-backend override. Only set it when a
	// real client exists.
	if llmClient != nil {
		cfg.LLMClient = llmClient
	}

	eng, err := tracker.NewEngineFromGraph(ctx, graph, cfg)
	if err != nil {
		return err
	}
	defer eng.Close()

	outcome, err := runTUIWithEngine(ctx, eng, prog, opts.failOnOverride)
	if err != nil {
		return err
	}

	return finishTUIRun(outcome, pipelineName, opts, effectiveParams, artifactDir)
}

// finishTUIRun prints the summary, fires the completion notification, and
// exports the bundle when a run ID is present. Extracted from runTUI for the
// complexity gate.
func finishTUIRun(outcome pipelineOutcome, pipelineName string, opts *runOptions, effectiveParams map[string]string, artifactDir string) error {
	printRunSummary(outcome.result, outcome.err, opts.pipelineFile, effectiveParams)
	notifyPipelineComplete(pipelineName, outcome.err)
	if outcome.result != nil && outcome.result.RunID != "" {
		// Spec + run.json are written by the library (Config.Capture) inside
		// eng.Run; the CLI only exports the bundle here.
		maybeExportBundle(artifactDir, outcome.result.RunID, opts.exportBundle)
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
// ctx is the signal-aware context created in runTUI so preflight, engine,
// and the TUI program share a single cancellation surface.
func runTUIWithEngine(ctx context.Context, engine *tracker.Engine, prog *tea.Program, failOnOverride bool) (pipelineOutcome, error) {
	pipelineCtx, cancel := context.WithCancel(ctx)
	defer cancel()

	outcomeCh := runPipelineAsync(engine, pipelineCtx, prog, failOnOverride)

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

// setupTUIProgram creates the TUI model and state store. Activity-log capture
// is library-owned (Config.Capture) — this builds only the presentation layer.
func setupTUIProgram(graph *pipeline.Graph, subgraphs map[string]*pipeline.Graph, pipelineName, checkpoint string, tokenTracker *llm.TokenTracker, llmClient *llm.Client, verbose bool, backend string, autopilot autopilotCfg) (*tea.Program, *tui.StateStore, error) {
	store := tui.NewStateStore(tokenTracker)
	appModel := tui.NewAppModel(store, pipelineName, "")
	appModel.SetVerboseTrace(verbose)
	configureTUIHeader(appModel, backend, autopilot)
	nodeList := buildNodeList(graph, subgraphs)
	appModel.SetInitialNodes(nodeList)

	if checkpoint != "" {
		preMarkCompletedNodes(checkpoint, nodeList, store)
	}

	prog := tea.NewProgram(appModel, tea.WithAltScreen())
	return prog, store, nil
}

// applyRunParamOverrides applies the --param overrides to the graph and returns
// the effective (post-override) values for the summary. Returns a nil map when
// no overrides were requested.
func applyRunParamOverrides(graph *pipeline.Graph, params map[string]string) (map[string]string, error) {
	if len(params) == 0 {
		return nil, nil
	}
	if err := pipeline.ApplyGraphParamOverrides(graph, params); err != nil {
		return nil, fmt.Errorf("apply --param overrides: %w", err)
	}
	effective := make(map[string]string, len(params))
	for key := range params {
		effective[key] = graph.Attrs[pipeline.GraphParamAttrKey(key)]
	}
	return effective, nil
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

// buildTUIPipelineHandler returns the pipeline event handler that drives the TUI
// (via prog.Send) and mirrors to the activity log.
func buildTUIPipelineHandler(prog *tea.Program) pipeline.PipelineEventHandler {
	// PipelineAdapter is stateful (accumulates EventValidationOverridden so the
	// terminal MsgPipelineTerminated carries the headline Override per Gap 5.2 D17;
	// the Status itself comes from the authoritative PipelineEvent.TerminalStatus).
	// Scope it to one run — sharing across runs would mix override state.
	pipelineAdapter := tui.NewPipelineAdapter()
	return pipeline.PipelineEventHandlerFunc(func(evt pipeline.PipelineEvent) {
		if msg := pipelineAdapter.Adapt(evt); msg != nil {
			prog.Send(msg)
		}
	})
}

// buildTUITraceObserver returns the LLM trace observer for TUI mode: it drives
// the dashboard (prog.Send). Activity-log mirroring is library-owned via
// Config.Capture. Returned (not attached to a client) so it can be passed via
// tracker.Config.LLMTrace.
func buildTUITraceObserver(prog *tea.Program, verbose bool) llm.TraceObserver {
	return llm.TraceObserverFunc(func(evt llm.TraceEvent) {
		for _, m := range tui.AdaptLLMTraceEvent(evt, "", verbose) {
			prog.Send(m)
		}
	})
}

// buildTUIAgentHandler returns the agent event handler for TUI mode: it drives
// the dashboard (prog.Send). Activity-log mirroring is library-owned via
// Config.Capture.
func buildTUIAgentHandler(prog *tea.Program) agent.EventHandler {
	return agent.EventHandlerFunc(func(evt agent.Event) {
		if msg := tui.AdaptAgentEvent(evt, evt.NodeID); msg != nil {
			prog.Send(msg)
		}
	})
}

// pipelineOutcome holds the result of a pipeline run.
type pipelineOutcome struct {
	result *pipeline.EngineResult
	err    error
}

// runPipelineAsync starts the pipeline in a background goroutine and returns the outcome channel.
//
// Status-to-error translation goes through interpretRunResult so the TUI path
// shares one source of truth with the non-TUI path: failure dominates, override
// only fires when --fail-on-override is set, and validation_overridden returns
// nil by default (because IsSuccess() covers it).
func runPipelineAsync(engine *tracker.Engine, ctx context.Context, prog *tea.Program, failOnOverride bool) chan pipelineOutcome {
	outcomeCh := make(chan pipelineOutcome, 1)
	go func() {
		defer func() {
			if r := recover(); r != nil {
				pipelineErr := fmt.Errorf("pipeline panicked: %v", r)
				outcomeCh <- pipelineOutcome{err: pipelineErr}
				prog.Send(tui.MsgPipelineDone{Err: pipelineErr})
			}
		}()
		res, runErr := engine.Run(ctx)
		result := engineResultOf(res)
		pipelineErr := interpretRunResult(result, runErr, &runConfig{failOnOverride: failOnOverride})
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

// resolveProviderBaseURLFromEnv delegates to tracker.ResolveProviderBaseURLStrict,
// which consults sources in priority order:
//  1. Per-provider *_BASE_URL env var (always wins).
//  2. TRACKER_GATEWAY_URL (set by --gateway-url before buildLLMClient runs,
//     or by the user directly), with a per-provider suffix selected by
//     TRACKER_GATEWAY_KIND (cf-aig default, or bedrock).
//  3. Empty string with nil error → use provider SDK default.
//
// Refuse-to-route surfaces as a non-nil error so adapter constructors can
// fail fast instead of silently falling back to the SDK default endpoint.
//
// The thin wrapper exists so test code in this package can exercise the
// resolved value without importing the tracker package directly.
func resolveProviderBaseURLFromEnv(provider string) (string, error) {
	return tracker.ResolveProviderBaseURLStrict(provider)
}

func buildAnthropicConstructor() func(string) (llm.ProviderAdapter, error) {
	return func(key string) (llm.ProviderAdapter, error) {
		base, err := resolveProviderBaseURLFromEnv("anthropic")
		if err != nil {
			return nil, fmt.Errorf("anthropic adapter: %w", err)
		}
		var opts []anthropic.Option
		if base != "" {
			opts = append(opts, anthropic.WithBaseURL(base))
		}
		return anthropic.New(key, opts...), nil
	}
}

func buildOpenAIConstructor() func(string) (llm.ProviderAdapter, error) {
	return func(key string) (llm.ProviderAdapter, error) {
		base, err := resolveProviderBaseURLFromEnv("openai")
		if err != nil {
			return nil, fmt.Errorf("openai adapter: %w", err)
		}
		var opts []openai.Option
		if base != "" {
			opts = append(opts, openai.WithBaseURL(base))
		}
		return openai.New(key, opts...), nil
	}
}

func buildGeminiConstructor() func(string) (llm.ProviderAdapter, error) {
	return func(key string) (llm.ProviderAdapter, error) {
		base, err := resolveProviderBaseURLFromEnv("gemini")
		if err != nil {
			return nil, fmt.Errorf("gemini adapter: %w", err)
		}
		var opts []google.Option
		if base != "" {
			opts = append(opts, google.WithBaseURL(base))
		}
		return google.New(key, opts...), nil
	}
}

func buildOpenAICompatConstructor() func(string) (llm.ProviderAdapter, error) {
	return func(key string) (llm.ProviderAdapter, error) {
		base, err := resolveProviderBaseURLFromEnv("openai-compat")
		if err != nil {
			return nil, fmt.Errorf("openai-compat adapter: %w", err)
		}
		var opts []openaicompat.Option
		if base != "" {
			opts = append(opts, openaicompat.WithBaseURL(base))
		}
		return openaicompat.New(key, opts...), nil
	}
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
func chooseTUIInterviewer(send tui.SendFunc, cfg autopilotCfg, llmClient *llm.Client, backend string, webhookGate *webhookGateCfg) handlers.LabeledFreeformInterviewer {
	if cfg.autoApprove {
		return &handlers.AutoApproveFreeformInterviewer{}
	}
	if webhookGate != nil {
		return newWebhookInterviewerFromCfg(webhookGate)
	}
	if cfg.persona != "" {
		if iv := chooseTUIAutopilotInterviewer(send, cfg.persona, llmClient, backend); iv != nil {
			return iv
		}
	}
	return tui.NewBubbleteaInterviewer(send)
}

// chooseTUIAutopilotInterviewer builds the persona-backed TUI autopilot
// interviewer (claude-code subprocess when the backend is claude-code, else the
// native LLM client). Returns nil to signal a fall-back to the interactive
// Bubbletea interviewer. Extracted from chooseTUIInterviewer for the
// complexity gate.
func chooseTUIAutopilotInterviewer(send tui.SendFunc, persona string, llmClient *llm.Client, backend string) handlers.LabeledFreeformInterviewer {
	parsed, _ := handlers.ParsePersona(persona)
	if backend == "claude-code" {
		ccAutopilot, ccErr := handlers.NewClaudeCodeAutopilotInterviewer(parsed)
		if ccErr == nil {
			return tui.NewAutopilotTUIInterviewer(ccAutopilot, send)
		}
		fmt.Fprintf(os.Stderr, "warning: claude-code autopilot init failed (%v), falling back to native\n", ccErr)
	}
	if llmClient != nil {
		autopilot := handlers.NewAutopilotInterviewer(llmClient, parsed)
		return tui.NewAutopilotTUIInterviewer(autopilot, send)
	}
	fmt.Fprintf(os.Stderr, "warning: no LLM client for autopilot, falling back to interactive\n")
	return nil
}

// maybeExportBundle exports a git bundle of the run artifact repository when
// --export-bundle is set. Best-effort: failures are printed as warnings and do
// not affect the pipeline exit code. The run dir is <artifactBase>/<runID>.
func maybeExportBundle(artifactBase, runID, exportBundle string) {
	if exportBundle == "" {
		return
	}
	runDir := filepath.Join(artifactBase, runID)
	if err := tracker.ExportBundle(runDir, exportBundle); err != nil {
		fmt.Fprintf(os.Stderr, "warning: bundle export failed: %v\n", err)
		return
	}
	fmt.Fprintf(os.Stdout, "  bundle: %s\n", exportBundle)
}
