// ABOUTME: Pipeline execution functions for both console mode (mode 1) and TUI mode (mode 2).
// ABOUTME: Includes LLM client construction and interviewer selection.
package main

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"path/filepath"
	"time"

	"github.com/2389-research/tracker/agent"
	"github.com/2389-research/tracker/agent/exec"
	"github.com/2389-research/tracker/llm"
	"github.com/2389-research/tracker/llm/anthropic"
	"github.com/2389-research/tracker/llm/google"
	"github.com/2389-research/tracker/llm/openai"
	"github.com/2389-research/tracker/pipeline"
	"github.com/2389-research/tracker/pipeline/handlers"
	"github.com/2389-research/tracker/tui"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/mattn/go-isatty"
)

// run executes the pipeline in mode 1: BubbleteaInterviewer spins up an inline
// tea.Program for each human gate, then returns control to the pipeline goroutine.
func run(pipelineFile, workdir, checkpoint, format string, verbose bool, jsonOut bool) error {
	graph, subgraphs, err := loadAndValidatePipeline(pipelineFile, format)
	if err != nil {
		return err
	}

	// Token tracker for LLM usage accumulation.
	tokenTracker := llm.NewTokenTracker()

	// Create LLM client from environment variables.
	llmClient, err := buildLLMClient(tokenTracker)
	if err != nil {
		return formatLLMClientError(err)
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
	agentEventHandler, pipelineEventHandler := buildConsoleEventHandlers(
		activityLog, llmClient, verbose, jsonOut,
	)
	engineOpts = append(engineOpts, pipeline.WithPipelineEventHandler(pipelineEventHandler))

	// Build the handler registry with real production dependencies.
	registry := handlers.NewDefaultRegistry(graph,
		handlers.WithLLMClient(llmClient, workdir),
		handlers.WithExecEnvironment(execEnv),
		handlers.WithInterviewer(interviewer, graph),
		handlers.WithAgentEventHandler(agentEventHandler),
		handlers.WithPipelineEventHandler(pipelineEventHandler),
		handlers.WithSubgraphs(subgraphs),
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
	stream := newJSONStream(os.Stdout)
	llmClient.AddTraceObserver(stream.traceObserver())
	agentHandler := agent.EventHandlerFunc(func(evt agent.Event) {
		logAgentEvent(evt)
		stream.agentHandler().HandleEvent(evt)
	})
	pipelineHandler := pipeline.PipelineMultiHandler(stream.pipelineHandler(), activityLog)
	return agentHandler, pipelineHandler
}

// buildPlainEventHandlers creates event handlers for human-readable console output.
func buildPlainEventHandlers(
	activityLog *pipeline.JSONLEventHandler,
	llmClient *llm.Client,
	verbose bool,
	logAgentEvent func(agent.Event),
) (agent.EventHandler, pipeline.PipelineEventHandler) {
	llmClient.AddTraceObserver(llm.NewTraceLogger(os.Stdout, llm.TraceLoggerOptions{Verbose: verbose}))
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
// loadAndValidatePipeline loads, validates, and resolves subgraphs for a pipeline file.
func loadAndValidatePipeline(pipelineFile, format string) (*pipeline.Graph, map[string]*pipeline.Graph, error) {
	graph, err := loadPipeline(pipelineFile, format)
	if err != nil {
		return nil, nil, fmt.Errorf("load pipeline: %w", err)
	}
	if err := pipeline.Validate(graph); err != nil {
		return nil, nil, fmt.Errorf("validate pipeline: %w", err)
	}
	subgraphs, err := loadSubgraphs(graph, pipelineFile)
	if err != nil {
		return nil, nil, fmt.Errorf("load subgraphs: %w", err)
	}
	if err := validateSubgraphRefs(graph, subgraphs); err != nil {
		return nil, nil, fmt.Errorf("subgraph validation: %w", err)
	}
	return graph, subgraphs, nil
}

func runTUI(pipelineFile, workdir, checkpoint, format string, verbose bool) error {
	graph, subgraphs, err := loadAndValidatePipeline(pipelineFile, format)
	if err != nil {
		return err
	}

	tokenTracker := llm.NewTokenTracker()
	llmClient, err := buildLLMClient(tokenTracker)
	if err != nil {
		return formatLLMClientError(err)
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
		preMarkCompletedNodes(checkpoint, nodeList, store)
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
		handlers.WithSubgraphs(subgraphs),
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
