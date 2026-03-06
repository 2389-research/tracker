// ABOUTME: Tool handler that runs shell commands via exec.ExecutionEnvironment.
// ABOUTME: Captures stdout/stderr to pipeline context; exit code 0 = success, non-zero = fail.
package handlers

import (
	"context"
	"fmt"
	"time"

	"github.com/2389-research/tracker/agent/exec"
	"github.com/2389-research/tracker/pipeline"
)

const defaultToolTimeout = 30 * time.Second

// ToolHandler executes shell commands specified in the node's "tool_command"
// attribute. Command output is captured and stored in the pipeline context.
type ToolHandler struct {
	env            exec.ExecutionEnvironment
	defaultTimeout time.Duration
}

// NewToolHandler creates a ToolHandler with the default 30-second timeout.
func NewToolHandler(env exec.ExecutionEnvironment) *ToolHandler {
	return &ToolHandler{env: env, defaultTimeout: defaultToolTimeout}
}

// NewToolHandlerWithTimeout creates a ToolHandler with a custom default timeout.
func NewToolHandlerWithTimeout(env exec.ExecutionEnvironment, timeout time.Duration) *ToolHandler {
	return &ToolHandler{env: env, defaultTimeout: timeout}
}

// Name returns the handler name used for registry lookup.
func (h *ToolHandler) Name() string { return "tool" }

// Execute runs the shell command from the node's "tool_command" attribute.
// It stores stdout and stderr in the pipeline context and returns success
// for exit code 0, fail for non-zero exit codes. An optional "timeout"
// attribute on the node overrides the default timeout.
func (h *ToolHandler) Execute(ctx context.Context, node *pipeline.Node, pctx *pipeline.PipelineContext) (pipeline.Outcome, error) {
	command := node.Attrs["tool_command"]
	if command == "" {
		return pipeline.Outcome{}, fmt.Errorf("node %q missing required attribute 'tool_command'", node.ID)
	}

	timeout := h.defaultTimeout
	if timeoutStr, ok := node.Attrs["timeout"]; ok {
		parsed, err := time.ParseDuration(timeoutStr)
		if err != nil {
			return pipeline.Outcome{}, fmt.Errorf("node %q has invalid timeout %q: %w", node.ID, timeoutStr, err)
		}
		timeout = parsed
	}

	result, err := h.env.ExecCommand(ctx, "sh", []string{"-c", command}, timeout)
	if err != nil {
		return pipeline.Outcome{}, fmt.Errorf("tool command failed for node %q: %w", node.ID, err)
	}

	status := pipeline.OutcomeSuccess
	if result.ExitCode != 0 {
		status = pipeline.OutcomeFail
	}

	return pipeline.Outcome{
			Status: status,
			ContextUpdates: map[string]string{
				pipeline.ContextKeyToolStdout: result.Stdout,
				pipeline.ContextKeyToolStderr: result.Stderr,
			},
		}, pipeline.WriteStatusArtifact(h.env.WorkingDir(), node.ID, pipeline.Outcome{
			Status: status,
			ContextUpdates: map[string]string{
				pipeline.ContextKeyToolStdout: result.Stdout,
				pipeline.ContextKeyToolStderr: result.Stderr,
			},
		})
}
