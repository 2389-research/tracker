// ABOUTME: Codergen handler that creates agent.Session from node attributes and runs the agent loop.
// ABOUTME: Key integration point between Layer 3 (pipeline) and Layer 2 (agent), capturing LLM responses.
package handlers

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/2389-research/tracker/agent"
	"github.com/2389-research/tracker/agent/exec"
	"github.com/2389-research/tracker/llm"
	"github.com/2389-research/tracker/pipeline"
)

// CodergenHandler invokes an agent session with the prompt from node attributes,
// captures the response text, and maps it to a pipeline outcome.
type CodergenHandler struct {
	client     agent.Completer
	env        exec.ExecutionEnvironment
	workingDir string
}

// NewCodergenHandler creates a CodergenHandler that will use the given LLM client
// and working directory for agent sessions.
func NewCodergenHandler(client agent.Completer, workingDir string) *CodergenHandler {
	return &CodergenHandler{
		client:     client,
		workingDir: workingDir,
	}
}

// Name returns the handler name used for registry lookup.
func (h *CodergenHandler) Name() string { return "codergen" }

// Execute creates an agent session from the node's attributes, runs it with the
// prompt, and captures the response. On LLM error, returns OutcomeFail (not a
// handler error). Supports auto_status parsing of STATUS:success/fail/retry
// from the response text.
func (h *CodergenHandler) Execute(ctx context.Context, node *pipeline.Node, pctx *pipeline.PipelineContext) (pipeline.Outcome, error) {
	prompt := node.Attrs["prompt"]
	if prompt == "" {
		return pipeline.Outcome{}, fmt.Errorf("node %q missing required attribute 'prompt'", node.ID)
	}
	prompt = pipeline.ExpandPromptVariables(prompt, pctx)

	config := h.buildConfig(node)

	// Use a text collector event handler to capture the final response text,
	// since SessionResult does not expose the conversation messages directly.
	var collector textCollector
	opts := []agent.SessionOption{agent.WithEventHandler(&collector)}
	if h.env != nil {
		opts = append(opts, agent.WithEnvironment(h.env))
	}
	sess, err := agent.NewSession(h.client, config, opts...)
	if err != nil {
		return pipeline.Outcome{}, fmt.Errorf("node %q failed to create session: %w", node.ID, err)
	}

	_, runErr := sess.Run(ctx, prompt)
	if runErr != nil {
		// Configuration errors (unknown provider, missing keys) are fatal —
		// they won't resolve on retry, so crash the pipeline immediately.
		var cfgErr *llm.ConfigurationError
		if errors.As(runErr, &cfgErr) {
			return pipeline.Outcome{}, fmt.Errorf("node %q: %w", node.ID, runErr)
		}

		// Other LLM errors (rate limits, network) are mapped to OutcomeFail.
		outcome := pipeline.Outcome{
			Status: pipeline.OutcomeFail,
			ContextUpdates: map[string]string{
				pipeline.ContextKeyLastResponse: runErr.Error(),
			},
		}
		if err := pipeline.WriteStageArtifacts(h.workingDir, node.ID, prompt, runErr.Error(), outcome); err != nil {
			return pipeline.Outcome{}, err
		}
		return outcome, nil
	}

	responseText := collector.text()

	status := pipeline.OutcomeSuccess
	if node.Attrs["auto_status"] == "true" {
		status = parseAutoStatus(responseText)
	}

	outcome := pipeline.Outcome{
		Status: status,
		ContextUpdates: map[string]string{
			pipeline.ContextKeyLastResponse: responseText,
		},
	}
	if err := pipeline.WriteStageArtifacts(h.workingDir, node.ID, prompt, responseText, outcome); err != nil {
		return pipeline.Outcome{}, err
	}
	return outcome, nil
}

// buildConfig constructs a SessionConfig from the node's attributes, using
// sensible defaults for any unspecified values.
func (h *CodergenHandler) buildConfig(node *pipeline.Node) agent.SessionConfig {
	config := agent.DefaultConfig()

	if h.workingDir != "" {
		config.WorkingDir = h.workingDir
	}

	if model, ok := node.Attrs["llm_model"]; ok {
		config.Model = model
	}
	if provider, ok := node.Attrs["llm_provider"]; ok {
		config.Provider = provider
	}
	if sp, ok := node.Attrs["system_prompt"]; ok {
		config.SystemPrompt = sp
	}

	return config
}

// parseAutoStatus extracts the STATUS directive from the first line of the
// response text. Valid statuses are success, fail, and retry. Falls back to
// success if no valid STATUS line is found.
func parseAutoStatus(text string) string {
	firstLine := text
	if idx := strings.Index(text, "\n"); idx >= 0 {
		firstLine = text[:idx]
	}
	firstLine = strings.TrimSpace(firstLine)

	if strings.HasPrefix(firstLine, "STATUS:") {
		status := strings.TrimSpace(strings.TrimPrefix(firstLine, "STATUS:"))
		switch status {
		case "success":
			return pipeline.OutcomeSuccess
		case "fail":
			return pipeline.OutcomeFail
		case "retry":
			return pipeline.OutcomeRetry
		}
	}

	return pipeline.OutcomeSuccess
}

// textCollector is an event handler that accumulates text delta events
// emitted by the agent session to reconstruct the final response.
type textCollector struct {
	parts []string
}

func (c *textCollector) HandleEvent(evt agent.Event) {
	if evt.Type == agent.EventTextDelta && evt.Text != "" {
		c.parts = append(c.parts, evt.Text)
	}
}

func (c *textCollector) text() string {
	return strings.Join(c.parts, "")
}
