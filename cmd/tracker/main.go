// ABOUTME: CLI entry point for the tracker pipeline engine.
// ABOUTME: Loads a DOT file, wires up LLM clients, exec environment, and runs the pipeline.
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
	"github.com/2389-research/tracker/tui/dashboard"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/mattn/go-isatty"
)

type runConfig struct {
	mode     commandMode
	dotFile  string
	workdir  string
	resumeID string // run ID to resume (resolved to checkpoint path)
	noTUI    bool
	verbose  bool
	jsonOut  bool // stream events as NDJSON to stdout
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
	run      func(string, string, string, bool, bool) error
	runTUI   func(string, string, string, bool) error
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

// run executes the pipeline in mode 1: BubbleteaInterviewer spins up an inline
// tea.Program for each human gate, then returns control to the pipeline goroutine.
func run(dotFile, workdir, checkpoint string, verbose bool, jsonOut bool) error {
	// Read and parse the DOT file.
	dotBytes, err := os.ReadFile(dotFile)
	if err != nil {
		return fmt.Errorf("read pipeline file: %w", err)
	}

	graph, err := pipeline.ParseDOT(string(dotBytes))
	if err != nil {
		return fmt.Errorf("parse pipeline: %w", err)
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

	// Wire up event handlers based on output mode.
	var agentEventHandler agent.EventHandler
	if jsonOut {
		// JSON streaming mode: all events go as typed NDJSON to stdout.
		stream := newJSONStream(os.Stdout)
		llmClient.AddTraceObserver(stream.traceObserver())
		agentEventHandler = stream.agentHandler()
		engineOpts = append(engineOpts, pipeline.WithPipelineEventHandler(
			pipeline.PipelineMultiHandler(stream.pipelineHandler(), activityLog),
		))
	} else {
		// Human-readable console output.
		llmClient.AddTraceObserver(llm.NewTraceLogger(os.Stdout, llm.TraceLoggerOptions{Verbose: verbose}))
		agentEventHandler = agent.EventHandlerFunc(func(evt agent.Event) {
			line := agent.FormatEventLine(evt)
			if line == "" {
				return
			}
			fmt.Fprintf(os.Stdout, "[%s] %s\n", time.Now().Format("15:04:05"), line)
		})
		engineOpts = append(engineOpts, pipeline.WithPipelineEventHandler(
			pipeline.PipelineMultiHandler(
				&pipeline.LoggingEventHandler{Writer: os.Stdout},
				activityLog,
			),
		))
	}

	// Build the handler registry with real production dependencies.
	registry := handlers.NewDefaultRegistry(graph,
		handlers.WithLLMClient(llmClient, workdir),
		handlers.WithExecEnvironment(execEnv),
		handlers.WithInterviewer(interviewer, graph),
		handlers.WithAgentEventHandler(agentEventHandler),
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

	result, err := engine.Run(ctx)
	if err != nil {
		return fmt.Errorf("pipeline execution: %w", err)
	}

	// Print run summary.
	var pipelineErr error
	if result.Status != pipeline.OutcomeSuccess {
		pipelineErr = fmt.Errorf("pipeline finished with status: %s", result.Status)
	}
	printRunSummary(result, pipelineErr, tokenTracker, dotFile)
	return pipelineErr
}

// runTUI executes the pipeline in mode 2: a persistent dashboard TUI owns the
// terminal; the pipeline runs in a background goroutine; human gates open modal
// overlays on the dashboard.
func runTUI(dotFile, workdir, checkpoint string, verbose bool) error {
	// Read and parse the DOT file.
	dotBytes, err := os.ReadFile(dotFile)
	if err != nil {
		return fmt.Errorf("read pipeline file: %w", err)
	}

	graph, err := pipeline.ParseDOT(string(dotBytes))
	if err != nil {
		return fmt.Errorf("parse pipeline: %w", err)
	}

	if err := pipeline.Validate(graph); err != nil {
		return fmt.Errorf("validate pipeline: %w", err)
	}

	// Token tracker for live header display.
	tokenTracker := llm.NewTokenTracker()

	// Create LLM client with token tracking middleware.
	llmClient, err := buildLLMClient(tokenTracker)
	if err != nil {
		return fmt.Errorf("create LLM client: %w", err)
	}
	defer llmClient.Close()

	// Create execution environment.
	execEnv := exec.NewLocalEnvironment(workdir)

	// Derive pipeline name from the dot file basename (without extension).
	pipelineName := graph.Name
	if pipelineName == "" {
		base := filepath.Base(dotFile)
		ext := filepath.Ext(base)
		pipelineName = base[:len(base)-len(ext)]
	}

	// Build the initial AppModel before creating the tea.Program so that we
	// can reference the Program in the interviewer.
	appModel := dashboard.NewAppModel(pipelineName, tokenTracker)
	appModel.SetVerboseTrace(verbose)
	nodeList := buildNodeList(graph)
	// When resuming from a checkpoint, pre-mark completed nodes so the TUI
	// shows them as green immediately (engine events fire before prog.Run).
	if checkpoint != "" {
		cp, cpErr := pipeline.LoadCheckpoint(checkpoint)
		if cpErr != nil {
			fmt.Fprintf(os.Stderr, "warning: could not load checkpoint for TUI: %v\n", cpErr)
		} else {
			for i := range nodeList {
				if cp.IsCompleted(nodeList[i].ID) {
					nodeList[i].Status = dashboard.NodeDone
				}
			}
		}
	}
	appModel.SetInitialNodes(nodeList)
	prog := tea.NewProgram(appModel, tea.WithAltScreen())

	llmClient.AddTraceObserver(llm.TraceObserverFunc(func(evt llm.TraceEvent) {
		prog.Send(dashboard.LLMTraceMsg{Event: evt})
	}))

	// Mode 2 interviewer: delegates gate prompts to the running dashboard program.
	interviewer, _ := tui.NewBubbleteaInterviewerMode2(prog)

	// TUI event handler: forwards pipeline events into the tea.Program loop.
	eventHandler := tui.NewTUIEventHandler(func(evt pipeline.PipelineEvent) {
		prog.Send(dashboard.PipelineEventMsg{Event: evt})
	})

	// Build the handler registry.
	registry := handlers.NewDefaultRegistry(graph,
		handlers.WithLLMClient(llmClient, workdir),
		handlers.WithExecEnvironment(execEnv),
		handlers.WithInterviewer(interviewer, graph),
		handlers.WithAgentEventHandler(agent.EventHandlerFunc(func(evt agent.Event) {
			prog.Send(dashboard.AgentEventMsg{Event: evt})
		})),
	)

	// Build engine options.
	artifactDir := filepath.Join(workdir, ".tracker", "runs")
	activityLog := pipeline.NewJSONLEventHandler(artifactDir)
	defer activityLog.Close()

	var engineOpts []pipeline.EngineOption
	engineOpts = append(engineOpts, pipeline.WithArtifactDir(artifactDir))
	engineOpts = append(engineOpts, pipeline.WithPipelineEventHandler(
		pipeline.PipelineMultiHandler(eventHandler, activityLog),
	))
	engineOpts = append(engineOpts, pipeline.WithStylesheetResolution(true))
	if checkpoint != "" {
		engineOpts = append(engineOpts, pipeline.WithCheckpointPath(checkpoint))
	}

	engine := pipeline.NewEngine(graph, registry, engineOpts...)

	// Run pipeline in a goroutine; the main thread belongs to tea.Program.
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
		prog.Send(dashboard.PipelineDoneMsg{Err: pipelineErr})
	}()

	// Start the TUI — this blocks until the program exits.
	_, err = prog.Run()

	// Cancel the pipeline context immediately so the background goroutine
	// stops. Without this the user would need a second Ctrl+C to kill the
	// pipeline after closing the TUI.
	cancel()

	if err != nil {
		return fmt.Errorf("TUI program: %w", err)
	}

	// After TUI exits, wait for the pipeline goroutine to drain.
	outcome := <-outcomeCh
	printRunSummary(outcome.result, outcome.err, tokenTracker, dotFile)
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
			cfg.dotFile = args[2]
		}
		return cfg, nil
	}

	if len(args) > 1 && args[1] == string(modeSimulate) {
		cfg.mode = modeSimulate
		if len(args) > 2 {
			cfg.dotFile = args[2]
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

	cfg.dotFile = positional[0]
	return cfg, nil
}

func printUsage(w io.Writer) {
	fmt.Fprint(w, renderStartupBanner())
	fmt.Fprintf(w, "Usage:\n")
	fmt.Fprintf(w, "  tracker [flags] <pipeline.dot> [flags]\n")
	fmt.Fprintf(w, "  tracker setup\n")
	fmt.Fprintf(w, "  tracker validate <pipeline.dot>\n")
	fmt.Fprintf(w, "  tracker simulate <pipeline.dot>\n")
	fmt.Fprintf(w, "  tracker audit [runID]\n\n")
	fmt.Fprintf(w, "Flags:\n")
	fmt.Fprintf(w, "  -w, --workdir string      Working directory (default: current directory)\n")
	fmt.Fprintf(w, "  -r, --resume string       Resume a previous run by ID (e.g. 13041bbb0a38)\n")
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
		if cfg.dotFile == "" {
			return fmt.Errorf("usage: tracker validate <pipeline.dot>")
		}
		return runValidate(cfg.dotFile, os.Stdout)
	}

	if cfg.mode == modeSimulate {
		if cfg.dotFile == "" {
			return fmt.Errorf("usage: tracker simulate <pipeline.dot>")
		}
		return runSimulate(cfg.dotFile, os.Stdout)
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
		return deps.run(cfg.dotFile, cfg.workdir, checkpoint, cfg.verbose, cfg.jsonOut)
	}
	return deps.runTUI(cfg.dotFile, cfg.workdir, checkpoint, cfg.verbose)
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
		return tui.NewBubbleteaInterviewer()
	}
	return handlers.NewConsoleInterviewer()
}

// buildNodeList creates an ordered list of dashboard NodeEntry items from the
// pipeline graph. Walks from StartNode in BFS order so the list reflects the
// natural execution flow. All nodes start as NodePending.
func buildNodeList(graph *pipeline.Graph) []dashboard.NodeEntry {
	if graph.StartNode == "" {
		return nil
	}

	var entries []dashboard.NodeEntry
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
		entries = append(entries, dashboard.NodeEntry{
			ID:     node.ID,
			Label:  label,
			Status: dashboard.NodePending,
		})

		for _, edge := range graph.OutgoingEdges(nodeID) {
			if !visited[edge.To] {
				queue = append(queue, edge.To)
			}
		}
	}

	return entries
}

// printRunSummary outputs a run summary with timing, per-node breakdown, token
// usage, and a simple ASCII node graph after the TUI exits.
func printRunSummary(result *pipeline.EngineResult, pipelineErr error, tracker *llm.TokenTracker, dotFile string) {
	fmt.Println()
	fmt.Println("═══ Run Summary ═══════════════════════════════════════════")

	if result != nil {
		fmt.Printf("  Run ID:  %s\n", result.RunID)
		fmt.Printf("  Status:  %s\n", result.Status)
		fmt.Printf("  Nodes:   %d completed\n", len(result.CompletedNodes))
	}

	// Total elapsed time from trace
	if result != nil && result.Trace != nil && !result.Trace.StartTime.IsZero() && !result.Trace.EndTime.IsZero() {
		elapsed := result.Trace.EndTime.Sub(result.Trace.StartTime)
		fmt.Printf("  Time:    %s\n", formatElapsed(elapsed))
	}

	// Per-node timing table
	if result != nil && result.Trace != nil && len(result.Trace.Entries) > 0 {
		fmt.Println()
		fmt.Println("─── Node Execution ────────────────────────────────────────")
		fmt.Printf("  %-20s  %-10s  %-10s  %s\n", "Node", "Status", "Time", "Handler")
		fmt.Printf("  %-20s  %-10s  %-10s  %s\n", "────", "──────", "────", "───────")
		for _, entry := range result.Trace.Entries {
			icon := "✓"
			switch entry.Status {
			case pipeline.OutcomeFail:
				icon = "✗"
			case pipeline.OutcomeRetry:
				icon = "↻"
			}
			nodeID := entry.NodeID
			if len(nodeID) > 20 {
				nodeID = nodeID[:17] + "…"
			}
			fmt.Printf("  %-20s  %s %-8s  %-10s  %s\n",
				nodeID, icon, entry.Status, formatElapsed(entry.Duration), entry.HandlerName)
		}
	}

	// Token usage per provider
	if tracker != nil {
		providers := tracker.Providers()
		if len(providers) > 0 {
			fmt.Println()
			fmt.Println("─── Token Usage ───────────────────────────────────────────")
			fmt.Printf("  %-12s  %10s  %10s\n", "Provider", "Input", "Output")
			fmt.Printf("  %-12s  %10s  %10s\n", "────────", "─────", "──────")
			for _, p := range providers {
				u := tracker.ProviderUsage(p)
				fmt.Printf("  %-12s  %10d  %10d\n", p, u.InputTokens, u.OutputTokens)
			}
			total := tracker.TotalUsage()
			fmt.Printf("  %-12s  %10d  %10d\n", "TOTAL", total.InputTokens, total.OutputTokens)
			if total.EstimatedCost > 0 {
				fmt.Printf("  Cost: $%.4f\n", total.EstimatedCost)
			}
		}
	}

	// Simple ASCII node graph from trace
	if result != nil && result.Trace != nil && len(result.Trace.Entries) > 0 {
		fmt.Println()
		fmt.Println("─── Pipeline Graph ────────────────────────────────────────")
		printNodeGraph(result.Trace.Entries)
	}

	if pipelineErr != nil {
		fmt.Println()
		fmt.Printf("  ERROR: %v\n", pipelineErr)
	}

	// Show resume hint when the pipeline didn't complete successfully.
	if result != nil && result.Status != pipeline.OutcomeSuccess && result.RunID != "" {
		pipelineArg := dotFile
		if pipelineArg == "" {
			pipelineArg = "<pipeline.dot>"
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
