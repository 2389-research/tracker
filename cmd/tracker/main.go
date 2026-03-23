// ABOUTME: CLI entry point for the tracker pipeline engine.
// ABOUTME: Loads a pipeline file (.dip preferred, .dot deprecated) and runs it.
// ABOUTME: Mode 1 (default): BubbleteaInterviewer for human gates with inline TUI per gate.
// ABOUTME: Mode 2 (--tui): Full dashboard TUI with header, node list, agent log, and modal gates.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"time"

	"github.com/2389-research/dippin-lang/parser"
	"github.com/2389-research/dippin-lang/validator"
	"github.com/2389-research/tracker/agent"
	"github.com/joho/godotenv"

	"github.com/2389-research/tracker/agent/exec"
	"github.com/2389-research/tracker/llm"
	"github.com/2389-research/tracker/llm/anthropic"
	"github.com/2389-research/tracker/llm/google"
	"github.com/2389-research/tracker/llm/openai"
	"github.com/2389-research/tracker/pipeline"
	"github.com/2389-research/tracker/pipeline/handlers"
	"github.com/2389-research/tracker/tui"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/mattn/go-isatty"
)

type runConfig struct {
	mode         commandMode
	pipelineFile string
	format       string // "dip", "dot", or "" (auto-detect from extension)
	workdir      string
	resumeID     string // run ID to resume (resolved to checkpoint path)
	noTUI        bool
	verbose      bool
	jsonOut      bool // stream events as NDJSON to stdout
}

type commandMode string

const (
	modeRun      commandMode = "run"
	modeSetup    commandMode = "setup"
	modeAudit    commandMode = "audit"
	modeSimulate commandMode = "simulate"
	modeValidate commandMode = "validate"
)

var errUsage = errors.New("usage")

type commandDeps struct {
	loadEnv  func(string) error
	runSetup func() error
	run      func(pipelineFile, workdir, checkpoint, format string, verbose bool, jsonOut bool) error
	runTUI   func(pipelineFile, workdir, checkpoint, format string, verbose bool) error
}

type setupResult struct {
	values    map[string]string
	cancelled bool
}

func main() {
	cfg, err := parseFlags(os.Args)
	if err != nil {
		if errors.Is(err, flag.ErrHelp) {
			printUsage(os.Stdout)
			os.Exit(0)
		}
		fmt.Fprintf(os.Stderr, "Error: %v\n\n", err)
		printUsage(os.Stderr)
		os.Exit(1)
	}

	if cfg.workdir == "" {
		wd, err := os.Getwd()
		if err != nil {
			fmt.Fprintf(os.Stderr, "error: cannot determine working directory: %v\n", err)
			os.Exit(1)
		}
		cfg.workdir = wd
	}

	err = executeCommand(cfg, commandDeps{})
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}
}

// detectPipelineFormat returns "dip" or "dot" based on file extension.
func detectPipelineFormat(filename string) string {
	ext := filepath.Ext(filename)
	if ext == ".dip" {
		return "dip"
	}
	return "dot" // default to DOT for .dot and unknown extensions
}

// loadPipeline reads and parses a pipeline file, auto-detecting format from
// extension unless formatOverride is set. Emits a deprecation warning to stderr
// when the resolved format is "dot".
func loadPipeline(filename, formatOverride string) (*pipeline.Graph, error) {
	format := formatOverride
	if format == "" {
		format = detectPipelineFormat(filename)
	}

	if format == "dot" {
		emitDOTDeprecationWarning(os.Stderr)
	}

	fileBytes, err := os.ReadFile(filename)
	if err != nil {
		return nil, fmt.Errorf("read pipeline file: %w", err)
	}

	switch format {
	case "dip":
		return loadDippinPipeline(string(fileBytes), filename)
	case "dot":
		return pipeline.ParseDOT(string(fileBytes))
	default:
		return nil, fmt.Errorf("unknown pipeline format: %q (valid: dip, dot)", format)
	}
}

// emitDOTDeprecationWarning prints a one-line warning that DOT is deprecated.
func emitDOTDeprecationWarning(w io.Writer) {
	fmt.Fprintln(w, "WARNING: DOT format is deprecated. Migrate pipelines to .dip format.")
}

// loadDippinPipeline parses a .dip file using dippin-lang parser,
// runs Dippin's built-in validator and linter, then converts to Tracker's
// Graph representation. Validation errors are fatal; lint warnings are
// printed to stderr but do not block execution.
func loadDippinPipeline(source, filename string) (*pipeline.Graph, error) {
	p := parser.NewParser(source, filename)
	workflow, err := p.Parse()
	if err != nil {
		return nil, fmt.Errorf("parse Dippin file: %w", err)
	}

	// Run Dippin structural validation (DIP001–DIP009).
	valResult := validator.Validate(workflow)
	if valResult.HasErrors() {
		for _, d := range valResult.Diagnostics {
			fmt.Fprintln(os.Stderr, d.String())
		}
		return nil, fmt.Errorf("%d validation error(s) in %s", len(valResult.Errors()), filename)
	}

	// Run Dippin lint checks (DIP101–DIP115). Warnings only — don't block.
	lintResult := validator.Lint(workflow)
	for _, d := range lintResult.Diagnostics {
		fmt.Fprintln(os.Stderr, d.String())
	}

	graph, err := pipeline.FromDippinIR(workflow)
	if err != nil {
		return nil, fmt.Errorf("convert Dippin IR to graph: %w", err)
	}

	return graph, nil
}

// run executes the pipeline in mode 1: BubbleteaInterviewer spins up an inline
// tea.Program for each human gate, then returns control to the pipeline goroutine.
func run(pipelineFile, workdir, checkpoint, format string, verbose bool, jsonOut bool) error {
	// Read and parse the pipeline file (auto-detect .dip or .dot).
	graph, err := loadPipeline(pipelineFile, format)
	if err != nil {
		return fmt.Errorf("load pipeline: %w", err)
	}

	if err := pipeline.Validate(graph); err != nil {
		return fmt.Errorf("validate pipeline: %w", err)
	}

	// Token tracker for LLM usage accumulation.
	tokenTracker := llm.NewTokenTracker()

	// Create LLM client from environment variables.
	llmClient, err := buildLLMClient(tokenTracker)
	if err != nil {
		return fmt.Errorf("create LLM client: %w", err)
	}
	defer llmClient.Close()

	// Create execution environment for tool handlers.
	execEnv := exec.NewLocalEnvironment(workdir)

	// Choose interviewer based on whether stdin is a terminal.
	// BubbleteaInterviewer requires a TTY; ConsoleInterviewer works with plain stdin.
	interviewer := chooseInterviewer(isatty.IsTerminal(os.Stdin.Fd()))

	// Build engine options.
	artifactDir := filepath.Join(workdir, ".tracker", "runs")
	var engineOpts []pipeline.EngineOption
	engineOpts = append(engineOpts, pipeline.WithArtifactDir(artifactDir))

	// Log pipeline events to a JSONL activity log on disk.
	activityLog := pipeline.NewJSONLEventHandler(artifactDir)
	defer activityLog.Close()

	// Wire LLM trace events to the activity log for complete audit trail.
	llmClient.AddTraceObserver(llm.TraceObserverFunc(func(evt llm.TraceEvent) {
		activityLog.WriteLLMEvent(string(evt.Kind), evt.Provider, evt.Model, evt.ToolName, evt.Preview)
	}))

	// Wire up event handlers based on output mode.
	var agentEventHandler agent.EventHandler

	// Agent event handler that always logs to activity log.
	logAgentEvent := func(evt agent.Event) {
		errMsg := ""
		if evt.Err != nil {
			errMsg = evt.Err.Error()
		}
		activityLog.WriteAgentEvent(string(evt.Type), evt.NodeID, evt.ToolName, evt.ToolOutput, evt.ToolError, evt.Text, errMsg, evt.Provider, evt.Model)
	}

	var pipelineEventHandler pipeline.PipelineEventHandler

	if jsonOut {
		// JSON streaming mode: all events go as typed NDJSON to stdout.
		stream := newJSONStream(os.Stdout)
		llmClient.AddTraceObserver(stream.traceObserver())
		agentEventHandler = agent.EventHandlerFunc(func(evt agent.Event) {
			logAgentEvent(evt)
			stream.agentHandler().HandleEvent(evt)
		})
		pipelineEventHandler = pipeline.PipelineMultiHandler(stream.pipelineHandler(), activityLog)
	} else {
		// Human-readable console output.
		llmClient.AddTraceObserver(llm.NewTraceLogger(os.Stdout, llm.TraceLoggerOptions{Verbose: verbose}))
		agentEventHandler = agent.EventHandlerFunc(func(evt agent.Event) {
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
		pipelineEventHandler = pipeline.PipelineMultiHandler(
			&pipeline.LoggingEventHandler{Writer: os.Stdout},
			activityLog,
		)
	}
	engineOpts = append(engineOpts, pipeline.WithPipelineEventHandler(pipelineEventHandler))

	// Build the handler registry with real production dependencies.
	registry := handlers.NewDefaultRegistry(graph,
		handlers.WithLLMClient(llmClient, workdir),
		handlers.WithExecEnvironment(execEnv),
		handlers.WithInterviewer(interviewer, graph),
		handlers.WithAgentEventHandler(agentEventHandler),
		handlers.WithPipelineEventHandler(pipelineEventHandler),
	)

	if checkpoint != "" {
		engineOpts = append(engineOpts, pipeline.WithCheckpointPath(checkpoint))
	}

	// Enable stylesheet resolution so node model attrs are resolved.
	engineOpts = append(engineOpts, pipeline.WithStylesheetResolution(true))

	engine := pipeline.NewEngine(graph, registry, engineOpts...)

	// Run with signal handling.
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt)
	defer cancel()

	result, runErr := engine.Run(ctx)

	// Print run summary with resume hint even on cancellation/error,
	// as long as we have a result with a run ID.
	var pipelineErr error
	if runErr != nil {
		pipelineErr = fmt.Errorf("pipeline execution: %w", runErr)
	} else if result.Status != pipeline.OutcomeSuccess {
		pipelineErr = fmt.Errorf("pipeline finished with status: %s", result.Status)
	}
	printRunSummary(result, pipelineErr, tokenTracker, pipelineFile)
	return pipelineErr
}

// runTUI executes the pipeline in mode 2: a persistent dashboard TUI owns the
// terminal; the pipeline runs in a background goroutine; human gates open modal
// overlays on the dashboard.
func runTUI(pipelineFile, workdir, checkpoint, format string, verbose bool) error {
	graph, err := loadPipeline(pipelineFile, format)
	if err != nil {
		return fmt.Errorf("load pipeline: %w", err)
	}
	if err := pipeline.Validate(graph); err != nil {
		return fmt.Errorf("validate pipeline: %w", err)
	}

	tokenTracker := llm.NewTokenTracker()
	llmClient, err := buildLLMClient(tokenTracker)
	if err != nil {
		return fmt.Errorf("create LLM client: %w", err)
	}
	defer llmClient.Close()

	execEnv := exec.NewLocalEnvironment(workdir)

	pipelineName := graph.Name
	if pipelineName == "" {
		base := filepath.Base(pipelineFile)
		ext := filepath.Ext(base)
		pipelineName = base[:len(base)-len(ext)]
	}

	// Build the TUI model.
	store := tui.NewStateStore(tokenTracker)
	appModel := tui.NewAppModel(store, pipelineName, "")
	appModel.SetVerboseTrace(verbose)
	nodeList := buildNodeList(graph)
	appModel.SetInitialNodes(nodeList)

	// Handle checkpoint resume — pre-mark completed nodes.
	if checkpoint != "" {
		cp, cpErr := pipeline.LoadCheckpoint(checkpoint)
		if cpErr != nil {
			fmt.Fprintf(os.Stderr, "warning: could not load checkpoint for TUI: %v\n", cpErr)
		} else {
			for _, n := range nodeList {
				if cp.IsCompleted(n.ID) {
					store.Apply(tui.MsgNodeCompleted{NodeID: n.ID, Outcome: "resumed"})
				}
			}
		}
	}

	prog := tea.NewProgram(appModel, tea.WithAltScreen())

	// Activity log.
	artifactDir := filepath.Join(workdir, ".tracker", "runs")
	activityLog := pipeline.NewJSONLEventHandler(artifactDir)
	defer activityLog.Close()

	// Wire LLM trace events to both TUI and activity log.
	llmClient.AddTraceObserver(llm.TraceObserverFunc(func(evt llm.TraceEvent) {
		for _, m := range tui.AdaptLLMTraceEvent(evt, "", verbose) {
			prog.Send(m)
		}
		activityLog.WriteLLMEvent(string(evt.Kind), evt.Provider, evt.Model, evt.ToolName, evt.Preview)
	}))

	// Mode 2 interviewer.
	interviewer := tui.NewBubbleteaInterviewer(func(msg tea.Msg) {
		prog.Send(msg)
	})

	// Pipeline event handler that adapts and sends to TUI.
	pipelineHandler := pipeline.PipelineEventHandlerFunc(func(evt pipeline.PipelineEvent) {
		msg := tui.AdaptPipelineEvent(evt)
		if msg != nil {
			prog.Send(msg)
		}
	})

	// Combine pipeline event handlers for both TUI and activity log.
	pipelineCombo := pipeline.PipelineMultiHandler(pipelineHandler, activityLog)

	// Build handler registry.
	registry := handlers.NewDefaultRegistry(graph,
		handlers.WithLLMClient(llmClient, workdir),
		handlers.WithExecEnvironment(execEnv),
		handlers.WithInterviewer(interviewer, graph),
		handlers.WithAgentEventHandler(agent.EventHandlerFunc(func(evt agent.Event) {
			msg := tui.AdaptAgentEvent(evt, evt.NodeID)
			if msg != nil {
				prog.Send(msg)
			}
			errMsg := ""
			if evt.Err != nil {
				errMsg = evt.Err.Error()
			}
			activityLog.WriteAgentEvent(string(evt.Type), evt.NodeID, evt.ToolName, evt.ToolOutput, evt.ToolError, evt.Text, errMsg, evt.Provider, evt.Model)
		})),
		handlers.WithPipelineEventHandler(pipelineCombo),
	)

	var engineOpts []pipeline.EngineOption
	engineOpts = append(engineOpts, pipeline.WithArtifactDir(artifactDir))
	engineOpts = append(engineOpts, pipeline.WithPipelineEventHandler(pipelineCombo))
	engineOpts = append(engineOpts, pipeline.WithStylesheetResolution(true))
	if checkpoint != "" {
		engineOpts = append(engineOpts, pipeline.WithCheckpointPath(checkpoint))
	}

	engine := pipeline.NewEngine(graph, registry, engineOpts...)

	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt)
	defer cancel()

	type pipelineOutcome struct {
		result *pipeline.EngineResult
		err    error
	}
	outcomeCh := make(chan pipelineOutcome, 1)

	go func() {
		result, pipelineErr := engine.Run(ctx)
		if pipelineErr == nil && result.Status != pipeline.OutcomeSuccess {
			pipelineErr = fmt.Errorf("pipeline finished with status: %s", result.Status)
		}
		outcomeCh <- pipelineOutcome{result: result, err: pipelineErr}
		prog.Send(tui.MsgPipelineDone{Err: pipelineErr})
	}()

	_, err = prog.Run()
	cancel()
	if err != nil {
		return fmt.Errorf("TUI program: %w", err)
	}

	outcome := <-outcomeCh
	printRunSummary(outcome.result, outcome.err, tokenTracker, pipelineFile)
	return outcome.err
}

func parseFlags(args []string) (runConfig, error) {
	cfg := runConfig{mode: modeRun}

	if len(args) > 1 && args[1] == string(modeSetup) {
		cfg.mode = modeSetup
		return cfg, nil
	}

	if len(args) > 1 && args[1] == string(modeValidate) {
		cfg.mode = modeValidate
		if len(args) > 2 {
			cfg.pipelineFile = args[2]
		}
		return cfg, nil
	}

	if len(args) > 1 && args[1] == string(modeSimulate) {
		cfg.mode = modeSimulate
		if len(args) > 2 {
			cfg.pipelineFile = args[2]
		}
		return cfg, nil
	}

	if len(args) > 1 && args[1] == string(modeAudit) {
		cfg.mode = modeAudit
		// Parse audit-specific flags: tracker audit [-w dir] <runID>
		afs := flag.NewFlagSet("audit", flag.ContinueOnError)
		afs.SetOutput(io.Discard)
		afs.StringVar(&cfg.workdir, "w", "", "Working directory")
		afs.StringVar(&cfg.workdir, "workdir", "", "Working directory")
		if err := afs.Parse(args[2:]); err != nil {
			return cfg, fmt.Errorf("audit: %w", err)
		}
		if afs.NArg() > 0 {
			cfg.resumeID = afs.Arg(0)
		}
		return cfg, nil
	}

	fs := flag.NewFlagSet(args[0], flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	fs.StringVar(&cfg.workdir, "w", "", "Working directory (default: current directory)")
	fs.StringVar(&cfg.workdir, "workdir", "", "Working directory (default: current directory)")
	fs.StringVar(&cfg.resumeID, "r", "", "Resume a previous run by ID (e.g. 13041bbb0a38)")
	fs.StringVar(&cfg.resumeID, "resume", "", "Resume a previous run by ID (e.g. 13041bbb0a38)")
	fs.BoolVar(&cfg.noTUI, "no-tui", false, "Disable TUI dashboard; use plain console output")
	fs.BoolVar(&cfg.verbose, "verbose", false, "Show raw provider stream events and extra LLM trace detail")
	fs.BoolVar(&cfg.jsonOut, "json", false, "Stream events as newline-delimited JSON to stdout")
	fs.StringVar(&cfg.format, "format", "", "Pipeline format override: dip (default) or dot")

	// Go's flag package stops parsing at the first non-flag argument.
	// To support flags in any order (e.g. "tracker pipeline.dot -c cp.json"),
	// we gather all non-flag arguments across multiple parse passes.
	remaining := args[1:]
	var positional []string
	for len(remaining) > 0 {
		if err := fs.Parse(remaining); err != nil {
			return cfg, err
		}
		positional = append(positional, fs.Args()...)
		// If Parse consumed everything or stopped at a non-flag, we need
		// to skip past the first positional arg and try parsing the rest.
		if fs.NArg() == 0 {
			break
		}
		// Skip the first positional arg and continue parsing the rest.
		remaining = fs.Args()[1:]
		positional = positional[:len(positional)-fs.NArg()]
		positional = append(positional, fs.Args()[0])
	}

	if len(positional) < 1 {
		return cfg, errUsage
	}

	cfg.pipelineFile = positional[0]
	return cfg, nil
}

func printUsage(w io.Writer) {
	fmt.Fprint(w, renderStartupBanner())
	fmt.Fprintf(w, "Usage:\n")
	fmt.Fprintf(w, "  tracker [flags] <pipeline.dip> [flags]\n")
	fmt.Fprintf(w, "  tracker setup\n")
	fmt.Fprintf(w, "  tracker validate <pipeline.dip>\n")
	fmt.Fprintf(w, "  tracker simulate <pipeline.dip>\n")
	fmt.Fprintf(w, "  tracker audit [runID]\n\n")
	fmt.Fprintf(w, "Flags:\n")
	fmt.Fprintf(w, "  -w, --workdir string      Working directory (default: current directory)\n")
	fmt.Fprintf(w, "  -r, --resume string       Resume a previous run by ID (e.g. 13041bbb0a38)\n")
	fmt.Fprintf(w, "  --format string           Pipeline format override: dip (default) or dot (deprecated)\n")
	fmt.Fprintf(w, "  --json                    Stream events as newline-delimited JSON to stdout\n")
	fmt.Fprintf(w, "  --no-tui                  Disable TUI dashboard; use plain console output\n")
	fmt.Fprintf(w, "  --verbose                 Show raw provider stream events and extra LLM trace detail\n")
}

func executeCommand(cfg runConfig, deps commandDeps) error {
	if deps.loadEnv == nil {
		deps.loadEnv = loadEnvFiles
	}
	if deps.runSetup == nil {
		deps.runSetup = runSetup
	}
	if deps.run == nil {
		deps.run = run
	}
	if deps.runTUI == nil {
		deps.runTUI = runTUI
	}

	if cfg.mode == modeSetup {
		return deps.runSetup()
	}

	if cfg.mode == modeValidate {
		if cfg.pipelineFile == "" {
			return fmt.Errorf("usage: tracker validate <pipeline.dip>")
		}
		return runValidateCmd(cfg.pipelineFile, cfg.format, os.Stdout)
	}

	if cfg.mode == modeSimulate {
		if cfg.pipelineFile == "" {
			return fmt.Errorf("usage: tracker simulate <pipeline.dip>")
		}
		return runSimulateCmd(cfg.pipelineFile, cfg.format, os.Stdout)
	}

	if cfg.mode == modeAudit {
		if cfg.resumeID == "" {
			return listRuns(cfg.workdir)
		}
		return runAudit(cfg.workdir, cfg.resumeID)
	}

	if err := deps.loadEnv(cfg.workdir); err != nil {
		return err
	}

	printStartupBanner()

	// Resolve run ID to checkpoint path.
	checkpoint := ""
	if cfg.resumeID != "" {
		cp, err := resolveCheckpoint(cfg.workdir, cfg.resumeID)
		if err != nil {
			return err
		}
		checkpoint = cp
	}

	// JSON streaming mode forces non-TUI.
	if cfg.jsonOut {
		cfg.noTUI = true
	}

	// Fall back to plain console mode when TUI is disabled or stdin is not a
	// terminal (e.g. CI, piped input, cron). TUI requires a real TTY.
	if cfg.noTUI || !isatty.IsTerminal(os.Stdin.Fd()) {
		return deps.run(cfg.pipelineFile, cfg.workdir, checkpoint, cfg.format, cfg.verbose, cfg.jsonOut)
	}
	return deps.runTUI(cfg.pipelineFile, cfg.workdir, checkpoint, cfg.format, cfg.verbose)
}

// resolveCheckpoint finds the checkpoint file for a given run ID. It looks in
// .tracker/runs/<runID>/checkpoint.json under the working directory. If the ID
// is a prefix that uniquely matches one run, it resolves to that run.
func resolveCheckpoint(workdir, runID string) (string, error) {
	if runID == "" {
		return "", fmt.Errorf("run ID cannot be empty")
	}
	runsDir := filepath.Join(workdir, ".tracker", "runs")
	entries, err := os.ReadDir(runsDir)
	if err != nil {
		return "", fmt.Errorf("cannot read runs directory: %w", err)
	}

	var matches []string
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		if strings.HasPrefix(e.Name(), runID) {
			matches = append(matches, e.Name())
		}
	}

	switch len(matches) {
	case 0:
		return "", fmt.Errorf("no run found matching %q in %s", runID, runsDir)
	case 1:
		// Unique match (exact or prefix)
	default:
		// Check for exact match among the prefix matches
		exact := false
		for _, m := range matches {
			if m == runID {
				matches = []string{m}
				exact = true
				break
			}
		}
		if !exact {
			return "", fmt.Errorf("ambiguous run ID %q matches %d runs: %s", runID, len(matches), strings.Join(matches, ", "))
		}
	}

	cpPath := filepath.Join(runsDir, matches[0], "checkpoint.json")
	if _, err := os.Stat(cpPath); err != nil {
		return "", fmt.Errorf("checkpoint not found for run %s: %w", matches[0], err)
	}
	return cpPath, nil
}

func loadEnvFiles(workdir string) error {
	originalEnv := currentEnvKeys()

	configEnvPath, err := resolveConfigEnvPath()
	if err != nil {
		return fmt.Errorf("resolve XDG config dir: %w", err)
	}
	if err := loadEnvFileIfPresent(configEnvPath, originalEnv); err != nil {
		return err
	}

	localEnvPath := filepath.Join(workdir, ".env")
	if err := loadEnvFileIfPresent(localEnvPath, originalEnv); err != nil {
		return err
	}

	return nil
}

func runSetup() error {
	return runSetupCommand(runSetupUI)
}

func runSetupCommand(runUI func(existing map[string]string) (setupResult, error)) error {
	configPath, err := resolveConfigEnvPath()
	if err != nil {
		return fmt.Errorf("resolve XDG config dir: %w", err)
	}

	existing, err := readEnvFile(configPath)
	if err != nil {
		return err
	}

	result, err := runUI(existing)
	if err != nil {
		return err
	}
	if result.cancelled {
		return nil
	}

	merged := mergeProviderEnv(existing, result.values)
	if envMapsEqual(existing, merged) {
		return nil
	}

	return writeEnvFile(configPath, merged)
}

func runSetupUI(existing map[string]string) (setupResult, error) {
	model := newSetupModel(existing)
	finalModel, err := tea.NewProgram(model).Run()
	if err != nil {
		return setupResult{}, err
	}

	final, ok := finalModel.(setupModel)
	if !ok {
		return setupResult{}, fmt.Errorf("unexpected setup model type %T", finalModel)
	}

	return setupResult{
		values:    final.pendingUpdates(),
		cancelled: final.cancelled,
	}, nil
}

func envMapsEqual(a, b map[string]string) bool {
	if len(a) != len(b) {
		return false
	}
	for key, value := range a {
		if b[key] != value {
			return false
		}
	}
	return true
}

func currentEnvKeys() map[string]struct{} {
	keys := make(map[string]struct{})
	for _, entry := range os.Environ() {
		if idx := strings.IndexByte(entry, '='); idx > 0 {
			keys[entry[:idx]] = struct{}{}
		}
	}
	return keys
}

func loadEnvFileIfPresent(path string, originalEnv map[string]struct{}) error {
	if _, err := os.Stat(path); err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return fmt.Errorf("stat env file %s: %w", path, err)
	}

	values, err := godotenv.Read(path)
	if err != nil {
		return fmt.Errorf("load env file %s: %w", path, err)
	}

	for key, value := range values {
		if _, exists := originalEnv[key]; exists {
			continue
		}
		if err := os.Setenv(key, value); err != nil {
			return fmt.Errorf("set env %s from %s: %w", key, path, err)
		}
	}
	return nil
}

// buildLLMClient constructs the LLM client from environment variables with
// custom base URL support and attaches the token tracker middleware.
func buildLLMClient(tokenTracker *llm.TokenTracker) (*llm.Client, error) {
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

// chooseInterviewer returns a BubbleteaInterviewer when stdin is a terminal
// (nice arrow-key UI), or a ConsoleInterviewer for non-TTY contexts (piped
// input, background processes, CI).
func chooseInterviewer(isTerminal bool) handlers.FreeformInterviewer {
	if isTerminal {
		return tui.NewMode1Interviewer()
	}
	return handlers.NewConsoleInterviewer()
}

// buildNodeList creates an ordered list of node ID/label pairs from the
// pipeline graph. Walks from StartNode in BFS order so the list reflects the
// natural execution flow.
func buildNodeList(graph *pipeline.Graph) []tui.NodeEntry {
	if graph.StartNode == "" {
		return nil
	}

	var entries []tui.NodeEntry
	visited := make(map[string]bool)
	queue := []string{graph.StartNode}

	for len(queue) > 0 {
		nodeID := queue[0]
		queue = queue[1:]
		if visited[nodeID] {
			continue
		}
		visited[nodeID] = true

		node, ok := graph.Nodes[nodeID]
		if !ok {
			continue
		}

		label := node.Label
		if label == "" {
			label = node.ID
		}
		entries = append(entries, tui.NodeEntry{
			ID:    node.ID,
			Label: label,
		})

		for _, edge := range graph.OutgoingEdges(nodeID) {
			if !visited[edge.To] {
				queue = append(queue, edge.To)
			}
		}
	}

	return entries
}

// aggregatedStats holds totals computed from all trace entries with SessionStats.
type aggregatedStats struct {
	TotalTurns      int
	TotalToolCalls  int
	ToolCallsByName map[string]int
	FilesCreated    []string
	FilesModified   []string
	Compactions     int
}

// aggregateSessionStats walks trace entries and sums up agent session metrics.
func aggregateSessionStats(entries []pipeline.TraceEntry) aggregatedStats {
	agg := aggregatedStats{
		ToolCallsByName: make(map[string]int),
	}
	seenCreated := make(map[string]bool)
	seenModified := make(map[string]bool)

	for _, entry := range entries {
		if entry.Stats == nil {
			continue
		}
		s := entry.Stats
		agg.TotalTurns += s.Turns
		agg.TotalToolCalls += s.TotalToolCalls
		agg.Compactions += s.Compactions
		for name, count := range s.ToolCalls {
			agg.ToolCallsByName[name] += count
		}
		for _, f := range s.FilesCreated {
			if !seenCreated[f] {
				seenCreated[f] = true
				agg.FilesCreated = append(agg.FilesCreated, f)
			}
		}
		for _, f := range s.FilesModified {
			if !seenModified[f] {
				seenModified[f] = true
				agg.FilesModified = append(agg.FilesModified, f)
			}
		}
	}
	return agg
}

// formatToolBreakdown returns a parenthesized breakdown like "(bash: 198, write: 67)".
func formatToolBreakdown(toolCalls map[string]int) string {
	if len(toolCalls) == 0 {
		return ""
	}
	// Sort by count descending, then name ascending for stability.
	type toolCount struct {
		name  string
		count int
	}
	var sorted []toolCount
	for name, count := range toolCalls {
		sorted = append(sorted, toolCount{name, count})
	}
	for i := 0; i < len(sorted); i++ {
		for j := i + 1; j < len(sorted); j++ {
			if sorted[j].count > sorted[i].count ||
				(sorted[j].count == sorted[i].count && sorted[j].name < sorted[i].name) {
				sorted[i], sorted[j] = sorted[j], sorted[i]
			}
		}
	}
	var parts []string
	for _, tc := range sorted {
		parts = append(parts, fmt.Sprintf("%s: %d", tc.name, tc.count))
	}
	return "(" + strings.Join(parts, ", ") + ")"
}

// formatNumber adds comma separators to integers for readability.
func formatNumber(n int) string {
	if n < 1000 {
		return fmt.Sprintf("%d", n)
	}
	s := fmt.Sprintf("%d", n)
	var result []byte
	for i, c := range s {
		if i > 0 && (len(s)-i)%3 == 0 {
			result = append(result, ',')
		}
		result = append(result, byte(c))
	}
	return string(result)
}

// printRunSummary outputs a comprehensive run summary with the logo, aggregated
// session stats, per-node breakdown, token usage, and ASCII pipeline graph.
func printRunSummary(result *pipeline.EngineResult, pipelineErr error, tracker *llm.TokenTracker, pipelineFile string) {
	fmt.Println()

	// Logo
	fmt.Println(bannerStyle.Render(logo()))
	fmt.Println()

	// Header bar
	fmt.Println("═══ Run Complete ══════════════════════════════════════════")

	if result != nil {
		fmt.Printf("  Run ID:    %s\n", result.RunID)

		// Status with icon
		statusIcon := "●"
		statusText := result.Status
		switch result.Status {
		case pipeline.OutcomeSuccess:
			statusText = selectedStyle.Render(statusIcon + " success")
		case pipeline.OutcomeFail:
			statusText = lipgloss.NewStyle().Foreground(colorHot).Render(statusIcon + " fail")
		default:
			statusText = mutedStyle.Render(statusIcon + " " + result.Status)
		}
		fmt.Printf("  Status:    %s\n", statusText)
	}

	// Total elapsed time from trace
	if result != nil && result.Trace != nil && !result.Trace.StartTime.IsZero() && !result.Trace.EndTime.IsZero() {
		elapsed := result.Trace.EndTime.Sub(result.Trace.StartTime)
		fmt.Printf("  Duration:  %s\n", formatElapsed(elapsed))
	}

	// Aggregated totals section
	if result != nil && result.Trace != nil && len(result.Trace.Entries) > 0 {
		agg := aggregateSessionStats(result.Trace.Entries)

		// Only show totals section if there are agent nodes with stats
		if agg.TotalTurns > 0 || agg.TotalToolCalls > 0 {
			fmt.Println()
			fmt.Println("─── Totals ────────────────────────────────────────────────")
			fmt.Printf("  LLM Turns:    %s\n", formatNumber(agg.TotalTurns))

			toolLine := fmt.Sprintf("  Tool Calls:   %s", formatNumber(agg.TotalToolCalls))
			if breakdown := formatToolBreakdown(agg.ToolCallsByName); breakdown != "" {
				toolLine += "  " + breakdown
			}
			fmt.Println(toolLine)

			if len(agg.FilesCreated) > 0 || len(agg.FilesModified) > 0 {
				fmt.Printf("  Files:        %d created, %d modified\n",
					len(agg.FilesCreated), len(agg.FilesModified))
			}
			if agg.Compactions > 0 {
				fmt.Printf("  Compactions:  %d\n", agg.Compactions)
			}

			// Token totals inline
			if tracker != nil {
				total := tracker.TotalUsage()
				if total.InputTokens > 0 || total.OutputTokens > 0 {
					tokenLine := fmt.Sprintf("  Tokens:       %s in / %s out",
						formatNumber(total.InputTokens), formatNumber(total.OutputTokens))
					if total.EstimatedCost > 0 {
						tokenLine += fmt.Sprintf("  ($%.2f)", total.EstimatedCost)
					}
					fmt.Println(tokenLine)
				}
			}
		}
	}

	// Per-node timing table with turns and tools columns
	if result != nil && result.Trace != nil && len(result.Trace.Entries) > 0 {
		fmt.Println()
		fmt.Println("─── Node Execution ────────────────────────────────────────")
		fmt.Printf("  %-22s  %-10s  %-10s  %5s  %5s  %s\n", "Node", "Status", "Time", "Turns", "Tools", "Handler")
		fmt.Printf("  %-22s  %-10s  %-10s  %5s  %5s  %s\n", "────", "──────", "────", "─────", "─────", "───────")
		for _, entry := range result.Trace.Entries {
			icon := "✓"
			switch entry.Status {
			case pipeline.OutcomeFail:
				icon = "✗"
			case pipeline.OutcomeRetry:
				icon = "↻"
			}
			nodeID := entry.NodeID
			if len(nodeID) > 22 {
				nodeID = nodeID[:19] + "..."
			}

			turns := "-"
			tools := "-"
			if entry.Stats != nil {
				turns = fmt.Sprintf("%d", entry.Stats.Turns)
				tools = fmt.Sprintf("%d", entry.Stats.TotalToolCalls)
			}

			fmt.Printf("  %-22s  %s %-8s  %-10s  %5s  %5s  %s\n",
				nodeID, icon, entry.Status, formatElapsed(entry.Duration), turns, tools, entry.HandlerName)
		}
	}

	// Token usage per provider
	if tracker != nil {
		providers := tracker.Providers()
		if len(providers) > 0 {
			fmt.Println()
			fmt.Println("─── Tokens by Provider ────────────────────────────────────")
			fmt.Printf("  %-12s  %10s  %10s\n", "Provider", "Input", "Output")
			fmt.Printf("  %-12s  %10s  %10s\n", "────────", "─────", "──────")
			for _, p := range providers {
				u := tracker.ProviderUsage(p)
				fmt.Printf("  %-12s  %10s  %10s\n", p, formatNumber(u.InputTokens), formatNumber(u.OutputTokens))
			}
			total := tracker.TotalUsage()
			fmt.Printf("  %-12s  %10s  %10s\n", "TOTAL", formatNumber(total.InputTokens), formatNumber(total.OutputTokens))
			if total.EstimatedCost > 0 {
				fmt.Printf("  Cost: $%.4f\n", total.EstimatedCost)
			}
		}
	}

	// Simple ASCII node graph from trace
	if result != nil && result.Trace != nil && len(result.Trace.Entries) > 0 {
		fmt.Println()
		fmt.Println("─── Pipeline ──────────────────────────────────────────────")
		printNodeGraph(result.Trace.Entries)
	}

	if pipelineErr != nil {
		fmt.Println()
		fmt.Printf("  ERROR: %v\n", pipelineErr)
	}

	// Show resume hint when the pipeline didn't complete successfully.
	if result != nil && result.Status != pipeline.OutcomeSuccess && result.RunID != "" {
		pipelineArg := pipelineFile
		if pipelineArg == "" {
			pipelineArg = "<pipeline.dip>"
		}
		fmt.Println()
		fmt.Println("─── Resume ────────────────────────────────────────────────")
		fmt.Printf("  tracker -r %s %s\n", result.RunID, pipelineArg)
	}

	fmt.Println("═══════════════════════════════════════════════════════════")
}

// printNodeGraph renders a simple vertical ASCII graph of the executed nodes.
func printNodeGraph(entries []pipeline.TraceEntry) {
	for i, entry := range entries {
		icon := "✓"
		switch entry.Status {
		case pipeline.OutcomeFail:
			icon = "✗"
		case pipeline.OutcomeRetry:
			icon = "↻"
		}

		label := entry.NodeID
		timing := formatElapsed(entry.Duration)

		fmt.Printf("  %s %s (%s)\n", icon, label, timing)

		// Draw connector to next node
		if i < len(entries)-1 {
			if entry.EdgeTo != "" && entry.EdgeTo != entries[i+1].NodeID {
				// Show branching
				fmt.Printf("  │ → %s\n", entry.EdgeTo)
			}
			fmt.Println("  │")
		}
	}
}

// formatElapsed formats a duration for the summary display.
func formatElapsed(d time.Duration) string {
	if d < time.Second {
		return fmt.Sprintf("%dms", d.Milliseconds())
	}
	if d < time.Minute {
		return fmt.Sprintf("%.1fs", d.Seconds())
	}
	minutes := int(d.Minutes())
	seconds := int(d.Seconds()) % 60
	return fmt.Sprintf("%dm%02ds", minutes, seconds)
}
