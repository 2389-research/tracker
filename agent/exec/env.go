// ABOUTME: ExecutionEnvironment interface abstracting where agent tools run.
// ABOUTME: Enables local execution (default) with future extensibility to Docker/SSH/K8s.
package exec

import (
	"context"
	"time"
)

// CommandResult holds the output and exit status of an executed command.
//
// When the command was run via ExecCommandWithLimit and a stream exceeded
// the per-stream cap, StdoutTruncated / StderrTruncated are set and the
// matching BytesDropped field carries how many bytes were elided from the
// head of the stream. The captured strings always contain the tail of the
// stream up to the cap. Truncation flags default to false / 0 when the
// stream did not overflow or when ExecCommand (unbounded) was used.
type CommandResult struct {
	Stdout             string
	Stderr             string
	ExitCode           int
	StdoutTruncated    bool
	StdoutBytesDropped int
	StderrTruncated    bool
	StderrBytesDropped int
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
