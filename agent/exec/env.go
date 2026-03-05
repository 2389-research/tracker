// ABOUTME: ExecutionEnvironment interface abstracting where agent tools run.
// ABOUTME: Enables local execution (default) with future extensibility to Docker/SSH/K8s.
package exec

import (
	"context"
	"time"
)

// CommandResult holds the output and exit status of an executed command.
type CommandResult struct {
	Stdout   string
	Stderr   string
	ExitCode int
}

// ExecutionEnvironment abstracts filesystem and process operations so that
// agent tools can run locally, in containers, or over SSH without changes.
type ExecutionEnvironment interface {
	ReadFile(ctx context.Context, path string) (string, error)
	WriteFile(ctx context.Context, path string, content string) error
	ExecCommand(ctx context.Context, command string, args []string, timeout time.Duration) (CommandResult, error)
	Glob(ctx context.Context, pattern string) ([]string, error)
	WorkingDir() string
}
