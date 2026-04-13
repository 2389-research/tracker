// ABOUTME: Bash tool executes shell commands in the working directory.
// ABOUTME: Supports configurable default/max timeouts and returns stdout+stderr+exit code.
package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/2389-research/tracker/agent/exec"
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

	timeout := t.resolveTimeout(params.Timeout)

	result, err := t.env.ExecCommand(ctx, "sh", []string{"-c", params.Command}, timeout)
	if err != nil {
		return "", err
	}

	return formatCommandOutput(result.Stdout, result.Stderr, result.ExitCode), nil
}

// resolveTimeout returns the effective timeout, clamping to maxTimeout.
func (t *BashTool) resolveTimeout(requested *float64) time.Duration {
	if requested == nil {
		return t.defaultTimeout
	}
	d := time.Duration(*requested * float64(time.Second))
	if d > t.maxTimeout {
		d = t.maxTimeout
	}
	if d > 0 {
		return d
	}
	return t.defaultTimeout
}

// formatCommandOutput assembles stdout, stderr, and exit code into a single result string.
func formatCommandOutput(stdout, stderr string, exitCode int) string {
	var b strings.Builder
	if stdout != "" {
		b.WriteString(stdout)
	}
	if stderr != "" {
		if b.Len() > 0 {
			b.WriteString("\n")
		}
		fmt.Fprintf(&b, "stderr: %s", stderr)
	}
	if exitCode != 0 {
		if b.Len() > 0 {
			b.WriteString("\n")
		}
		fmt.Fprintf(&b, "exit code: %d", exitCode)
	}
	if b.Len() == 0 {
		return "(no output)"
	}
	return b.String()
}

// CachePolicy declares that bash execution is mutating and invalidates caches.
func (t *BashTool) CachePolicy() CachePolicy { return CachePolicyMutating }
