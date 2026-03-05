// ABOUTME: Bash tool executes shell commands in the working directory.
// ABOUTME: Supports configurable default/max timeouts and returns stdout+stderr+exit code.
package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/2389-research/mammoth-lite/agent/exec"
)

// BashTool runs shell commands via the execution environment.
type BashTool struct {
	env            exec.ExecutionEnvironment
	defaultTimeout time.Duration
	maxTimeout     time.Duration
}

// NewBashTool creates a BashTool with the given environment and timeout bounds.
func NewBashTool(env exec.ExecutionEnvironment, defaultTimeout, maxTimeout time.Duration) *BashTool {
	return &BashTool{
		env:            env,
		defaultTimeout: defaultTimeout,
		maxTimeout:     maxTimeout,
	}
}

func (t *BashTool) Name() string { return "bash" }

func (t *BashTool) Description() string {
	return "Execute a shell command and return stdout, stderr, and exit code."
}

func (t *BashTool) Parameters() json.RawMessage {
	return json.RawMessage(`{
		"type": "object",
		"properties": {
			"command": {
				"type": "string",
				"description": "The shell command to execute."
			},
			"timeout": {
				"type": "number",
				"description": "Timeout in seconds (optional, uses default if not specified)."
			}
		},
		"required": ["command"]
	}`)
}

func (t *BashTool) Execute(ctx context.Context, input json.RawMessage) (string, error) {
	var params struct {
		Command string   `json:"command"`
		Timeout *float64 `json:"timeout,omitempty"`
	}
	if err := json.Unmarshal(input, &params); err != nil {
		return "", fmt.Errorf("invalid input: %w", err)
	}
	if params.Command == "" {
		return "", fmt.Errorf("command is required")
	}

	timeout := t.defaultTimeout
	if params.Timeout != nil {
		requested := time.Duration(*params.Timeout * float64(time.Second))
		if requested > t.maxTimeout {
			requested = t.maxTimeout
		}
		if requested > 0 {
			timeout = requested
		}
	}

	result, err := t.env.ExecCommand(ctx, "sh", []string{"-c", params.Command}, timeout)
	if err != nil {
		return "", err
	}

	var b strings.Builder
	if result.Stdout != "" {
		b.WriteString(result.Stdout)
	}
	if result.Stderr != "" {
		if b.Len() > 0 {
			b.WriteString("\n")
		}
		fmt.Fprintf(&b, "stderr: %s", result.Stderr)
	}
	if result.ExitCode != 0 {
		if b.Len() > 0 {
			b.WriteString("\n")
		}
		fmt.Fprintf(&b, "exit code: %d", result.ExitCode)
	}

	if b.Len() == 0 {
		return "(no output)", nil
	}

	return b.String(), nil
}
