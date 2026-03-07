// ABOUTME: CLI entry point for the tracker pipeline engine.
// ABOUTME: Loads a DOT file, wires up LLM clients, exec environment, and runs the pipeline.
// ABOUTME: Mode 1 (default): BubbleteaInterviewer for human gates with inline TUI per gate.
// ABOUTME: Mode 2 (--tui): Full dashboard TUI with header, node list, agent log, and modal gates.
package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"path/filepath"

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
)

func main() {
	// Load .env file if present; ignore if missing.
	_ = godotenv.Load()

	var (
		workdir    string
		checkpoint string
		verbose    bool
		tuiMode    bool
	)

	flag.StringVar(&workdir, "w", "", "Working directory (default: current directory)")
	flag.StringVar(&workdir, "workdir", "", "Working directory (default: current directory)")
	flag.StringVar(&checkpoint, "c", "", "Checkpoint file path for resume support")
	flag.StringVar(&checkpoint, "checkpoint", "", "Checkpoint file path for resume support")
	flag.BoolVar(&verbose, "v", false, "Verbose event logging")
	flag.BoolVar(&verbose, "verbose", false, "Verbose event logging")
	flag.BoolVar(&tuiMode, "tui", false, "Full TUI dashboard mode with live progress and modal gates")

	flag.Usage = func() {
		fmt.Fprintf(os.Stderr, "Usage: tracker <pipeline.dot> [flags]\n\nFlags:\n")
		flag.PrintDefaults()
	}

	flag.Parse()

	if flag.NArg() < 1 {
		flag.Usage()
		os.Exit(1)
	}

	dotFile := flag.Arg(0)

	if workdir == "" {
		wd, err := os.Getwd()
		if err != nil {
			fmt.Fprintf(os.Stderr, "error: cannot determine working directory: %v\n", err)
			os.Exit(1)
		}
		workdir = wd
	}

	var err error
	if tuiMode {
		err = runTUI(dotFile, workdir, checkpoint)
	} else {
		err = run(dotFile, workdir, checkpoint, verbose)
	}
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}
}

// run executes the pipeline in mode 1: BubbleteaInterviewer spins up an inline
// tea.Program for each human gate, then returns control to the pipeline goroutine.
func run(dotFile, workdir, checkpoint string, verbose bool) error {
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

	// Mode 1: BubbleteaInterviewer for human gate nodes (replaces ConsoleInterviewer).
	interviewer := tui.NewBubbleteaInterviewer()

	// Build the handler registry with real production dependencies.
	registry := handlers.NewDefaultRegistry(graph,
		handlers.WithLLMClient(llmClient, workdir),
		handlers.WithExecEnvironment(execEnv),
		handlers.WithInterviewer(interviewer, graph),
	)

	// Build engine options.
	var engineOpts []pipeline.EngineOption

	if verbose {
		engineOpts = append(engineOpts, pipeline.WithPipelineEventHandler(
			&pipeline.LoggingEventHandler{Writer: os.Stderr},
		))
	}

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

	// Print result summary.
	fmt.Fprintf(os.Stdout, "run_id=%s status=%s completed_nodes=%d\n",
		result.RunID, result.Status, len(result.CompletedNodes))

	if result.Status != pipeline.OutcomeSuccess {
		return fmt.Errorf("pipeline finished with status: %s", result.Status)
	}

	return nil
}

// runTUI executes the pipeline in mode 2: a persistent dashboard TUI owns the
// terminal; the pipeline runs in a background goroutine; human gates open modal
// overlays on the dashboard.
func runTUI(dotFile, workdir, checkpoint string) error {
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
	prog := tea.NewProgram(appModel, tea.WithAltScreen())

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
	)

	// Build engine options.
	var engineOpts []pipeline.EngineOption
	engineOpts = append(engineOpts, pipeline.WithPipelineEventHandler(eventHandler))
	engineOpts = append(engineOpts, pipeline.WithStylesheetResolution(true))
	if checkpoint != "" {
		engineOpts = append(engineOpts, pipeline.WithCheckpointPath(checkpoint))
	}

	engine := pipeline.NewEngine(graph, registry, engineOpts...)

	// Run pipeline in a goroutine; the main thread belongs to tea.Program.
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt)
	defer cancel()

	go func() {
		result, pipelineErr := engine.Run(ctx)
		if pipelineErr == nil && result.Status != pipeline.OutcomeSuccess {
			pipelineErr = fmt.Errorf("pipeline finished with status: %s", result.Status)
		}
		prog.Send(dashboard.PipelineDoneMsg{Err: pipelineErr})
		if pipelineErr == nil {
			// Print summary after TUI exits
			_ = result
		}
	}()

	// Start the TUI — this blocks until the program exits.
	finalModel, err := prog.Run()
	if err != nil {
		return fmt.Errorf("TUI program: %w", err)
	}

	// After TUI exits, print summary.
	app, ok := finalModel.(dashboard.AppModel)
	if ok && app.PipelineErr() != nil {
		return app.PipelineErr()
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

	// Wire token tracker as middleware.
	if tokenTracker != nil {
		client.AddMiddleware(tokenTracker)
	}

	return client, nil
}
