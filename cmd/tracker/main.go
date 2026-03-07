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
	"time"

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
	artifactDir := filepath.Join(workdir, ".tracker", "runs")
	var engineOpts []pipeline.EngineOption
	engineOpts = append(engineOpts, pipeline.WithArtifactDir(artifactDir))

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
	appModel.SetInitialNodes(buildNodeList(graph))
	prog := tea.NewProgram(appModel, tea.WithAltScreen())

	// Activity tracker: forwards LLM call summaries to the agent log.
	activityTracker := llm.NewActivityTracker(func(evt llm.ActivityEvent) {
		prog.Send(dashboard.LLMActivityMsg{Summary: evt.Summary()})
	})
	llmClient.AddMiddleware(activityTracker)

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
	artifactDir := filepath.Join(workdir, ".tracker", "runs")
	var engineOpts []pipeline.EngineOption
	engineOpts = append(engineOpts, pipeline.WithArtifactDir(artifactDir))
	engineOpts = append(engineOpts, pipeline.WithPipelineEventHandler(eventHandler))
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
	if err != nil {
		return fmt.Errorf("TUI program: %w", err)
	}

	// After TUI exits, print run summary.
	outcome := <-outcomeCh
	printRunSummary(outcome.result, outcome.err, tokenTracker)
	return outcome.err
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
func printRunSummary(result *pipeline.EngineResult, pipelineErr error, tracker *llm.TokenTracker) {
	fmt.Println()
	fmt.Println("═══ Run Summary ═══════════════════════════════════════════")

	if result != nil {
		fmt.Printf("  Run ID:  %s\n", result.RunID)
		fmt.Printf("  Status:  %s\n", result.Status)
		fmt.Printf("  Nodes:   %d completed\n", len(result.CompletedNodes))
	}

	// Total elapsed time from trace
	if result != nil && result.Trace != nil && !result.Trace.StartTime.IsZero() {
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
