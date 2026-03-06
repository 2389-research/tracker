// ABOUTME: CLI entry point for the tracker pipeline engine.
// ABOUTME: Loads a DOT file, wires up LLM clients, exec environment, and runs the pipeline.
package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"os/signal"

	"github.com/2389-research/tracker/agent/exec"
	"github.com/2389-research/tracker/llm"
	"github.com/2389-research/tracker/llm/anthropic"
	"github.com/2389-research/tracker/llm/google"
	"github.com/2389-research/tracker/llm/openai"
	"github.com/2389-research/tracker/pipeline"
	"github.com/2389-research/tracker/pipeline/handlers"
)

func main() {
	var (
		workdir    string
		checkpoint string
		verbose    bool
	)

	flag.StringVar(&workdir, "w", "", "Working directory (default: current directory)")
	flag.StringVar(&workdir, "workdir", "", "Working directory (default: current directory)")
	flag.StringVar(&checkpoint, "c", "", "Checkpoint file path for resume support")
	flag.StringVar(&checkpoint, "checkpoint", "", "Checkpoint file path for resume support")
	flag.BoolVar(&verbose, "v", false, "Verbose event logging")
	flag.BoolVar(&verbose, "verbose", false, "Verbose event logging")

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

	if err := run(dotFile, workdir, checkpoint, verbose); err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}
}

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

	// Create LLM client from environment variables.
	// Support custom base URLs via *_BASE_URL env vars (e.g. Cloudflare AI Gateway).
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
		"google": func(key string) (llm.ProviderAdapter, error) {
			var opts []google.Option
			if base := os.Getenv("GEMINI_BASE_URL"); base != "" {
				opts = append(opts, google.WithBaseURL(base))
			}
			return google.New(key, opts...), nil
		},
	}

	llmClient, err := llm.NewClientFromEnv(constructors)
	if err != nil {
		return fmt.Errorf("create LLM client: %w", err)
	}
	defer llmClient.Close()

	// Create execution environment for tool handlers.
	execEnv := exec.NewLocalEnvironment(workdir)

	// Create console interviewer for human gate nodes.
	interviewer := handlers.NewConsoleInterviewer()

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
