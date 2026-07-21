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

// activeToolSafety holds the tool handler security config for the current run.
// Set by executeRun from the --bypass-denylist, --tool-allowlist, and
// --max-output-limit CLI flags. The zero value is the default-safe config
// (denylist active, no allowlist, 10MB ceiling).
var activeToolSafety handlers.ToolHandlerConfig

// activeGatewayURL / activeGatewayKind carry the --gateway-url / --gateway-kind
// flag values from executeRun to run/runTUI, which set Config.GatewayURL /
// GatewayKind. Threading them through Config (rather than os.Setenv) keeps
// gateway routing per-run and off process-global state.
var (
	activeGatewayURL  string
	activeGatewayKind string
)

// activeResumeInfo carries resume-time metadata from resolveRunCheckpoint
// through to run/runTUI. The forced-mismatch detail in particular has to
// reach the activity log handler (constructed inside run/runTUI) so the
// override can be recorded as a bundle_mismatch_forced entry before the
// engine fires. The zero value is the new (non-resume) run case.
var activeResumeInfo resumeInfo

// activeGitConfig holds the --git / --allow-init values for the current run.
// Set by executeRun before calling run/runTUI, matching the pattern of
// activeAutopilotCfg. Consumed by the inline pipeline.Preflight call in
// run() and runTUI() just after applyRunParamOverrides.
var activeGitConfig struct {
	policy    string
	allowInit bool
}

// activeFailOnOverride captures the effective --fail-on-override decision for
// the current run, merged with the TRACKER_FAIL_ON_OVERRIDE=1 env var
// fallback. Both run paths read it because cfg is out of reach where the
// post-pipeline decision is made — runPipelineAsync (TUI) and run() (non-TUI)
// each pass `&runConfig{failOnOverride: activeFailOnOverride}` to
// interpretRunResult. executeRun calls applyFailOnOverrideEnv before the
// pipeline fires so the global reflects the merged flag+env decision.
var activeFailOnOverride bool

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

// applyGitPreflight runs the v0.29.0 git preflight check using the
// module-level activeGitConfig populated by executeRun. Called from both
// run() and runTUI() after applyRunParamOverrides — so the check fires
// before any LLM client setup or network activity. Bail on error so the
// user sees the actionable remediation instead of a deferred failure.
//
// Takes a context so Ctrl+C during slow git probes (network drives,
// dubious-ownership prompts, hung remotes) or during the optional
// `git init` side effect of `--git=init` propagates cleanly. The
// caller threads a signal.NotifyContext created before the LLM client
// setup so cancellation works uniformly across preflight and engine.
func applyGitPreflight(ctx context.Context, graph *pipeline.Graph, workdir string) error {
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
		Policy:         pipeline.GitPreflight(activeGitConfig.policy),
		AllowInit:      activeGitConfig.allowInit,
		InteractiveTTY: isatty.IsTerminal(os.Stdin.Fd()),
		Warner: func(format string, args ...any) {
			fmt.Fprintf(os.Stderr, "warning: "+format+"\n", args...)
		},
	})
}

// run executes the pipeline in mode 1: BubbleteaInterviewer spins up an inline
// tea.Program for each human gate, then returns control to the pipeline goroutine.
func run(pipelineFile, workdir, checkpoint, format, backend string, verbose bool, jsonOut bool) error {
	// Signal context lives across preflight + engine so Ctrl+C during a
	// slow git probe or auto-init also aborts cleanly. Pre-fix the
	// preflight used context.Background and only the engine got the
	// signal context, so Ctrl+C couldn't interrupt the preflight branch.
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt)
	defer cancel()

	graph, subgraphs, bundleInfo, err := loadAndPreflightPipeline(ctx, pipelineFile, format, workdir)
	if err != nil {
		return err
	}

	artifactDir := resolveArtifactDir(workdir)
	activityLog := setupActivityLog(artifactDir, verbose, bundleInfo.Identity)
	defer activityLog.Close()

	agentHandler, pipelineHandler, traceObs := buildConsoleEventHandlers(activityLog, verbose, jsonOut)

	cfg := tracker.Config{
		WorkingDir:     workdir,
		CheckpointDir:  checkpoint,
		ArtifactDir:    artifactDir,
		Backend:        backend,
		Budget:         activeBudgetLimits,
		GatewayURL:     activeGatewayURL,
		GatewayKind:    tracker.GatewayKind(activeGatewayKind),
		Subgraphs:      subgraphs,
		BundleIdentity: bundleInfo.Identity,
		ToolSafety:     &activeToolSafety,
		// loadAndPreflightPipeline already ran the CLI git preflight (with TTY
		// prompting); disable the library's non-interactive one.
		Git:          &tracker.GitConfig{Preflight: tracker.GitPreflightOff},
		EventHandler: pipelineHandler,
		AgentEvents:  agentHandler,
		LLMTrace:     traceObs,
	}
	applyInterviewerToConfig(&cfg, isatty.IsTerminal(os.Stdin.Fd()))

	eng, err := tracker.NewEngineFromGraph(ctx, graph, cfg)
	if err != nil {
		return err
	}
	defer eng.Close()

	res, runErr := eng.Run(ctx)
	return finishRun(engineResultOf(res), runErr, pipelineFile, artifactDir)
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
func applyInterviewerToConfig(cfg *tracker.Config, isTerminal bool) {
	switch {
	case activeAutopilotCfg.autoApprove:
		cfg.AutoApprove = true
	case activeWebhookGate != nil:
		cfg.WebhookGate = toTrackerWebhookGate(activeWebhookGate)
	case activeAutopilotCfg.persona != "":
		cfg.Autopilot = activeAutopilotCfg.persona
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
func finishRun(result *pipeline.EngineResult, runErr error, pipelineFile, artifactDir string) error {
	pipelineErr := interpretRunResult(result, runErr, &runConfig{failOnOverride: activeFailOnOverride})
	printRunSummary(result, pipelineErr, pipelineFile)
	if result != nil && result.RunID != "" {
		maybeExportBundle(artifactDir, result.RunID)
	}
	return pipelineErr
}

// loadAndPreflightPipeline loads + validates the pipeline, applies --param
// overrides, and runs the device/git preflight. Shared prelude for run and
// runTUI; extracted to keep both under the complexity gate.
func loadAndPreflightPipeline(ctx context.Context, pipelineFile, format, workdir string) (*pipeline.Graph, map[string]*pipeline.Graph, pipeline.BundleInfo, error) {
	graph, subgraphs, bundleInfo, err := loadAndValidatePipeline(pipelineFile, format)
	if err != nil {
		return nil, nil, pipeline.BundleInfo{}, err
	}
	if err := applyRunParamOverrides(graph); err != nil {
		return nil, nil, pipeline.BundleInfo{}, err
	}
	if err := applyGitPreflight(ctx, graph, workdir); err != nil {
		return nil, nil, pipeline.BundleInfo{}, err
	}
	return graph, subgraphs, bundleInfo, nil
}

// resolveArtifactDir returns the configured artifact dir, defaulting to
// <workdir>/.tracker/runs when none was set. Extracted from run/runTUI for the
// complexity gate.
func resolveArtifactDir(workdir string) string {
	if activeArtifactDir != "" {
		return activeArtifactDir
	}
	return filepath.Join(workdir, ".tracker", "runs")
}

// setupActivityLog constructs the JSONL activity-log handler, configures raw-LLM
// capture and bundle identity, and records any forced bundle-mismatch resume.
// Extracted from run/runTUI for the complexity gate. Caller owns Close().
func setupActivityLog(artifactDir string, verbose bool, bundleIdentity string) *pipeline.JSONLEventHandler {
	activityLog := pipeline.NewJSONLEventHandler(artifactDir)
	// Raw provider streaming chunks are debugging payload — only capture
	// them in the activity log under --verbose (#354).
	activityLog.SetCaptureRawLLM(verbose)
	// Stamp the .dipx bundle identity on agent/llm JSONL writes too —
	// these bypass HandlePipelineEvent (and therefore Engine.emit and the
	// registry's BundleIdentityStamper). Empty identity is a no-op for
	// plain .dip runs.
	activityLog.SetBundleIdentity(bundleIdentity)
	// If this resume only proceeded because --force-bundle-mismatch was
	// passed, record the override in activity.jsonl now — the engine
	// hasn't fired yet, so without this the audit trail would lack the
	// signal that the run executed against a different bundle than its
	// checkpoint claimed. No-op when no resume / no forced mismatch.
	emitForcedBundleMismatch(activityLog, activeResumeInfo)
	return activityLog
}

// emitForcedBundleMismatch writes the bundle_mismatch_forced audit entry to
// activity.jsonl when --force-bundle-mismatch allowed resume despite a
// .dipx bundle identity change. No-op for new runs and for resumes whose
// bundle identity matched (the common case).
func emitForcedBundleMismatch(activityLog *pipeline.JSONLEventHandler, info resumeInfo) {
	if !info.BundleMismatchForced {
		return
	}
	activityLog.WriteBundleMismatchForced(info.RunID, info.OriginalIdentity, info.CurrentIdentity)
}

// llmTraceLogObserver returns the client-level trace → activity log writer.
// Session-owned events are skipped: the agent session re-emits those as
// llm_* agent events which reach the log via WriteAgentEvent, so writing
// them here would log the same stream twice (#354). Non-session calls
// (e.g. the autopilot interviewer) have no agent path and are kept.
func llmTraceLogObserver(activityLog *pipeline.JSONLEventHandler) llm.TraceObserverFunc {
	return func(evt llm.TraceEvent) {
		if evt.SessionOwned {
			return
		}
		activityLog.WriteLLMEvent(string(evt.Kind), evt.Provider, evt.Model, evt.ToolName, evt.Preview)
	}
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

// buildConsoleEventHandlers creates the agent and pipeline event handlers for
// console (non-TUI) mode, branching on whether JSON output is requested.
// buildConsoleEventHandlers returns the agent + pipeline event handlers and the
// LLM trace observer for a plain/JSON (non-TUI) run. The trace observer is
// returned (not attached to a client) so the caller can pass it via
// tracker.Config.LLMTrace and let the library own the client.
func buildConsoleEventHandlers(
	activityLog *pipeline.JSONLEventHandler,
	verbose bool,
	jsonOut bool,
) (agent.EventHandler, pipeline.PipelineEventHandler, llm.TraceObserver) {
	// Agent event handler that always logs to activity log.
	logAgentEvent := func(evt agent.Event) {
		errMsg := ""
		if evt.Err != nil {
			errMsg = evt.Err.Error()
		}
		activityLog.WriteAgentEvent(string(evt.Type), evt.NodeID, evt.ToolName, evt.ToolOutput, evt.ToolError, evt.Text, errMsg, evt.Provider, evt.Model)
	}
	activityTrace := llmTraceLogObserver(activityLog)

	if jsonOut {
		return buildJSONEventHandlers(activityLog, logAgentEvent, activityTrace)
	}
	return buildPlainEventHandlers(activityLog, verbose, logAgentEvent, activityTrace)
}

// buildJSONEventHandlers creates event handlers for JSON streaming mode.
func buildJSONEventHandlers(
	activityLog *pipeline.JSONLEventHandler,
	logAgentEvent func(agent.Event),
	activityTrace llm.TraceObserver,
) (agent.EventHandler, pipeline.PipelineEventHandler, llm.TraceObserver) {
	stream := tracker.NewNDJSONWriter(os.Stdout)
	agentHandler := agent.EventHandlerFunc(func(evt agent.Event) {
		logAgentEvent(evt)
		stream.AgentHandler().HandleEvent(evt)
	})
	pipelineHandler := pipeline.PipelineMultiHandler(stream.PipelineHandler(), activityLog)
	return agentHandler, pipelineHandler, combineTraceObservers(activityTrace, stream.TraceObserver())
}

// buildPlainEventHandlers creates event handlers for human-readable console output.
func buildPlainEventHandlers(
	activityLog *pipeline.JSONLEventHandler,
	verbose bool,
	logAgentEvent func(agent.Event),
	activityTrace llm.TraceObserver,
) (agent.EventHandler, pipeline.PipelineEventHandler, llm.TraceObserver) {
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
	stdoutTrace := llm.NewTraceLogger(os.Stdout, llm.TraceLoggerOptions{Verbose: verbose})
	return agentHandler, pipelineHandler, combineTraceObservers(activityTrace, stdoutTrace)
}

// combineTraceObservers fans one LLM trace stream out to several observers, so
// the run's single tracker.Config.LLMTrace covers the activity log plus the
// mode-specific console/NDJSON trace sink.
func combineTraceObservers(obs ...llm.TraceObserver) llm.TraceObserver {
	return llm.TraceObserverFunc(func(evt llm.TraceEvent) {
		for _, o := range obs {
			if o != nil {
				o.HandleTraceEvent(evt)
			}
		}
	})
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

func runTUI(pipelineFile, workdir, checkpoint, format, backend string, verbose bool) error {
	// Signal context covers preflight + engine for consistent Ctrl+C
	// handling. The TUI's tea.Program owns the terminal once running,
	// but preflight runs before that, so a slow git probe needs an
	// interruptible context here too.
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt)
	defer cancel()

	graph, subgraphs, bundleInfo, err := loadAndPreflightPipeline(ctx, pipelineFile, format, workdir)
	if err != nil {
		return err
	}

	// The token tracker is shared between the TUI view model (StateStore) and
	// the engine, so the dashboard's live cost readout matches the run. The
	// client is built bare (no token-tracker middleware); the library attaches
	// the shared tracker exactly once via Config.TokenTracker.
	tokenTracker := llm.NewTokenTracker()
	llmClient, err := resolveLLMClient(nil, backend)
	if err != nil {
		return err
	}
	if llmClient != nil {
		defer llmClient.Close()
	}

	pipelineName := resolvePipelineName(graph, pipelineFile)
	artifactDir := resolveArtifactDir(workdir)

	prog, _, activityLog, err := setupTUIProgram(graph, subgraphs, pipelineName, checkpoint, tokenTracker, llmClient, verbose, backend, artifactDir)
	if err != nil {
		return err
	}
	defer activityLog.Close()
	// Stamp the .dipx bundle identity on agent/llm JSONL writes too —
	// these bypass HandlePipelineEvent (and therefore Engine.emit and the
	// registry's BundleIdentityStamper). Empty identity is a no-op for
	// plain .dip runs. Then record any forced bundle-mismatch resume.
	activityLog.SetBundleIdentity(bundleInfo.Identity)
	emitForcedBundleMismatch(activityLog, activeResumeInfo)

	sendFn := tui.SendFunc(func(msg tea.Msg) { prog.Send(msg) })
	interviewer := chooseTUIInterviewer(sendFn, activeAutopilotCfg, llmClient, backend)

	cfg := tracker.Config{
		WorkingDir:     workdir,
		CheckpointDir:  checkpoint,
		ArtifactDir:    artifactDir,
		Backend:        backend,
		Budget:         activeBudgetLimits,
		GatewayURL:     activeGatewayURL,
		GatewayKind:    tracker.GatewayKind(activeGatewayKind),
		Subgraphs:      subgraphs,
		BundleIdentity: bundleInfo.Identity,
		ToolSafety:     &activeToolSafety,
		Git:            &tracker.GitConfig{Preflight: tracker.GitPreflightOff},
		EventHandler:   buildTUIPipelineHandler(prog, activityLog),
		AgentEvents:    buildTUIAgentHandler(prog, activityLog),
		LLMTrace:       buildTUITraceObserver(prog, activityLog, verbose),
		TokenTracker:   tokenTracker,
		Interviewer:    interviewer, // cancelled by eng.Close() if it is a canceller
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

	outcome, err := runTUIWithEngine(ctx, eng, prog)
	if err != nil {
		return err
	}

	return finishTUIRun(outcome, pipelineName, pipelineFile, artifactDir)
}

// finishTUIRun prints the summary, fires the completion notification, and
// exports the bundle when a run ID is present. Extracted from runTUI for the
// complexity gate.
func finishTUIRun(outcome pipelineOutcome, pipelineName, pipelineFile, artifactDir string) error {
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
// ctx is the signal-aware context created in runTUI so preflight, engine,
// and the TUI program share a single cancellation surface.
func runTUIWithEngine(ctx context.Context, engine *tracker.Engine, prog *tea.Program) (pipelineOutcome, error) {
	pipelineCtx, cancel := context.WithCancel(ctx)
	defer cancel()

	outcomeCh := runPipelineAsync(engine, pipelineCtx, prog)

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
	// Raw provider streaming chunks are debugging payload — only capture
	// them in the activity log under --verbose (#354).
	activityLog.SetCaptureRawLLM(verbose)
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

// buildTUIPipelineHandler returns the pipeline event handler that drives the TUI
// (via prog.Send) and mirrors to the activity log.
func buildTUIPipelineHandler(prog *tea.Program, activityLog *pipeline.JSONLEventHandler) pipeline.PipelineEventHandler {
	// PipelineAdapter is stateful (accumulates EventValidationOverridden so the
	// terminal MsgPipelineCompleted carries Status + headline Override for the
	// completion-row renderer per Gap 5.2 D17). Scope it to one pipeline run —
	// sharing across runs would mix override state across pipelines.
	pipelineAdapter := tui.NewPipelineAdapter()
	pipelineHandler := pipeline.PipelineEventHandlerFunc(func(evt pipeline.PipelineEvent) {
		if msg := pipelineAdapter.Adapt(evt); msg != nil {
			prog.Send(msg)
		}
	})
	return pipeline.PipelineMultiHandler(pipelineHandler, activityLog)
}

// buildTUITraceObserver returns the LLM trace observer for TUI mode: it drives
// the dashboard (prog.Send) and mirrors to the activity log. Returned (not
// attached to a client) so it can be passed via tracker.Config.LLMTrace.
func buildTUITraceObserver(prog *tea.Program, activityLog *pipeline.JSONLEventHandler, verbose bool) llm.TraceObserver {
	logObserver := llmTraceLogObserver(activityLog)
	return llm.TraceObserverFunc(func(evt llm.TraceEvent) {
		for _, m := range tui.AdaptLLMTraceEvent(evt, "", verbose) {
			prog.Send(m)
		}
		logObserver(evt)
	})
}

// buildTUIAgentHandler returns the agent event handler for TUI mode: it drives
// the dashboard (prog.Send) and mirrors to the activity log.
func buildTUIAgentHandler(prog *tea.Program, activityLog *pipeline.JSONLEventHandler) agent.EventHandler {
	return agent.EventHandlerFunc(func(evt agent.Event) {
		if msg := tui.AdaptAgentEvent(evt, evt.NodeID); msg != nil {
			prog.Send(msg)
		}
		errMsg := ""
		if evt.Err != nil {
			errMsg = evt.Err.Error()
		}
		activityLog.WriteAgentEvent(string(evt.Type), evt.NodeID, evt.ToolName, evt.ToolOutput, evt.ToolError, evt.Text, errMsg, evt.Provider, evt.Model)
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
func runPipelineAsync(engine *tracker.Engine, ctx context.Context, prog *tea.Program) chan pipelineOutcome {
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
		pipelineErr := interpretRunResult(result, runErr, &runConfig{failOnOverride: activeFailOnOverride})
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
