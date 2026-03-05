// ABOUTME: Codergen handler that creates agent.Session from node attributes and runs the agent loop.
// ABOUTME: Key integration point between Layer 3 (pipeline) and Layer 2 (agent), capturing LLM responses.
package handlers

import (
	"context"
	"fmt"
	"strings"

	"github.com/2389-research/mammoth-lite/agent"
	"github.com/2389-research/mammoth-lite/pipeline"
)

// CodergenHandler invokes an agent session with the prompt from node attributes,
// captures the response text, and maps it to a pipeline outcome.
type CodergenHandler struct {
	client     agent.Completer
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

	config := h.buildConfig(node)

	// Use a text collector event handler to capture the final response text,
	// since SessionResult does not expose the conversation messages directly.
	var collector textCollector
	sess, err := agent.NewSession(h.client, config, agent.WithEventHandler(&collector))
	if err != nil {
		return pipeline.Outcome{}, fmt.Errorf("node %q failed to create session: %w", node.ID, err)
	}

	_, runErr := sess.Run(ctx, prompt)
	if runErr != nil {
		// LLM errors are mapped to OutcomeFail, not handler errors.
		return pipeline.Outcome{
			Status: pipeline.OutcomeFail,
			ContextUpdates: map[string]string{
				pipeline.ContextKeyLastResponse: runErr.Error(),
			},
		}, nil
	}

	responseText := collector.text()

	status := pipeline.OutcomeSuccess
	if node.Attrs["auto_status"] == "true" {
		status = parseAutoStatus(responseText)
	}

	return pipeline.Outcome{
		Status: status,
		ContextUpdates: map[string]string{
			pipeline.ContextKeyLastResponse: responseText,
		},
	}, nil
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

	// For codergen nodes, a single turn is sufficient since we just need
	// the LLM to respond to a prompt without tool use.
	config.MaxTurns = 1

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
		status := strings.TrimPrefix(firstLine, "STATUS:")
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
