// ABOUTME: Codergen handler that creates agent.Session from node attributes and runs the agent loop.
// ABOUTME: Key integration point between Layer 3 (pipeline) and Layer 2 (agent), capturing LLM responses.
package handlers

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/2389-research/tracker/agent"
	"github.com/2389-research/tracker/agent/exec"
	"github.com/2389-research/tracker/llm"
	"github.com/2389-research/tracker/pipeline"
)

// CodergenHandler invokes an agent session with the prompt from node attributes,
// captures the response text, and maps it to a pipeline outcome.
type CodergenHandler struct {
	client       agent.Completer
	env          exec.ExecutionEnvironment
	workingDir   string
	eventHandler agent.EventHandler
	graphAttrs   map[string]string
}

// NewCodergenHandler creates a CodergenHandler that will use the given LLM client
// and working directory for agent sessions.
func NewCodergenHandler(client agent.Completer, workingDir string, opts ...CodergenOption) *CodergenHandler {
	h := &CodergenHandler{
		client:     client,
		workingDir: workingDir,
	}
	for _, opt := range opts {
		opt(h)
	}
	return h
}

// CodergenOption configures optional CodergenHandler behavior.
type CodergenOption func(*CodergenHandler)

// WithGraphAttrs passes graph-level attributes to the handler for fidelity resolution.
func WithGraphAttrs(attrs map[string]string) CodergenOption {
	return func(h *CodergenHandler) {
		h.graphAttrs = attrs
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

	// Resolve fidelity for this node and inject compacted context when not full.
	fidelity := pipeline.ResolveFidelity(node, h.graphAttrs)
	if fidelity != pipeline.FidelityFull {
		artifactDir := h.workingDir
		if dir, ok := pctx.GetInternal(pipeline.InternalKeyArtifactDir); ok && dir != "" {
			artifactDir = dir
		}
		runID := ""
		compacted := pipeline.CompactContext(pctx, nil, fidelity, artifactDir, runID)
		prompt = prependContextSummary(prompt, compacted, fidelity)
	} else {
		prompt = pipeline.InjectPipelineContext(prompt, pctx)
	}

	config := h.buildConfig(node)

	// Capture both plain assistant text and a readable execution transcript,
	// since tool-only sessions would otherwise write an empty response artifact.
	var collector transcriptCollector
	handler := agent.MultiHandler(&collector, h.eventHandler)
	opts := []agent.SessionOption{agent.WithEventHandler(handler)}
	if h.env != nil {
		opts = append(opts, agent.WithEnvironment(h.env))
	}
	sess, err := agent.NewSession(h.client, config, opts...)
	if err != nil {
		return pipeline.Outcome{}, fmt.Errorf("node %q failed to create session: %w", node.ID, err)
	}

	// Determine artifact directory: prefer pipeline context, fall back to working dir.
	artifactRoot := h.workingDir
	if dir, ok := pctx.GetInternal(pipeline.InternalKeyArtifactDir); ok && dir != "" {
		artifactRoot = dir
	}

	_, runErr := sess.Run(ctx, prompt)
	if runErr != nil {
		// Configuration errors (unknown provider, missing keys) are fatal —
		// they won't resolve on retry, so crash the pipeline immediately.
		var cfgErr *llm.ConfigurationError
		if errors.As(runErr, &cfgErr) {
			return pipeline.Outcome{}, fmt.Errorf("node %q: %w", node.ID, runErr)
		}

		// Other LLM errors (rate limits, network) are transient — map to
		// OutcomeRetry so the pipeline engine retries the node automatically.
		outcome := pipeline.Outcome{
			Status: pipeline.OutcomeRetry,
			ContextUpdates: map[string]string{
				pipeline.ContextKeyLastResponse: runErr.Error(),
			},
		}
		responseArtifact := collector.transcript()
		if responseArtifact == "" {
			responseArtifact = runErr.Error()
		}
		if err := pipeline.WriteStageArtifacts(artifactRoot, node.ID, prompt, responseArtifact, outcome); err != nil {
			return pipeline.Outcome{}, err
		}
		return outcome, nil
	}

	responseText := collector.text()
	responseArtifact := collector.transcript()
	if responseArtifact == "" {
		responseArtifact = responseText
	}

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
	if err := pipeline.WriteStageArtifacts(artifactRoot, node.ID, prompt, responseArtifact, outcome); err != nil {
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
	if mt, ok := node.Attrs["max_turns"]; ok {
		if v, err := strconv.Atoi(mt); err == nil && v > 0 {
			config.MaxTurns = v
		}
	}
	if ct, ok := node.Attrs["command_timeout"]; ok {
		if d, err := time.ParseDuration(ct); err == nil && d > 0 {
			config.CommandTimeout = d
		}
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

// prependContextSummary adds a compacted context summary section to the prompt
// based on the fidelity level and compacted context values.
func prependContextSummary(prompt string, compacted map[string]string, fidelity pipeline.Fidelity) string {
	if len(compacted) == 0 {
		return prompt
	}

	var sections []string
	sections = append(sections, fmt.Sprintf("# Context Summary (fidelity: %s)", fidelity))
	for key, val := range compacted {
		if val == "" {
			continue
		}
		sections = append(sections, fmt.Sprintf("## %s\n%s", key, val))
	}

	if len(sections) <= 1 {
		return prompt
	}

	return strings.Join(sections, "\n\n") + "\n\n---\n\n" + prompt
}

// transcriptCollector preserves an ordered plain-text transcript of a session
// while also keeping the concatenated assistant text for status parsing.
type transcriptCollector struct {
	lines     []string
	textParts []string
}

func (c *transcriptCollector) HandleEvent(evt agent.Event) {
	switch evt.Type {
	case agent.EventTurnStart:
		c.lines = append(c.lines, fmt.Sprintf("TURN %d", evt.Turn))
	case agent.EventToolCallStart:
		c.lines = append(c.lines, fmt.Sprintf("TOOL CALL: %s", evt.ToolName))
		if evt.ToolInput != "" {
			c.lines = append(c.lines, "INPUT:")
			c.lines = append(c.lines, evt.ToolInput)
		}
	case agent.EventToolCallEnd:
		c.lines = append(c.lines, fmt.Sprintf("TOOL RESULT: %s", evt.ToolName))
		if evt.ToolOutput != "" {
			c.lines = append(c.lines, "OUTPUT:")
			c.lines = append(c.lines, evt.ToolOutput)
		}
		if evt.ToolError != "" {
			c.lines = append(c.lines, "ERROR:")
			c.lines = append(c.lines, evt.ToolError)
		}
	case agent.EventTextDelta:
		if evt.Text != "" {
			c.textParts = append(c.textParts, evt.Text)
			c.lines = append(c.lines, "TEXT:")
			c.lines = append(c.lines, evt.Text)
		}
	case agent.EventError:
		if evt.Err != nil {
			c.lines = append(c.lines, "ERROR:")
			c.lines = append(c.lines, evt.Err.Error())
		}
	}
}

func (c *transcriptCollector) text() string {
	return strings.Join(c.textParts, "")
}

func (c *transcriptCollector) transcript() string {
	return strings.Join(c.lines, "\n")
}
