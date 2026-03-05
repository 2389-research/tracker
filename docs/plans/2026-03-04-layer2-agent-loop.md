# Layer 2: Coding Agent Loop Implementation Plan

> **For Claude:** REQUIRED SUB-SKILL: Use superpowers:executing-plans to implement this plan task-by-task.

**Goal:** Build a coding agent loop that orchestrates LLM calls and tool execution to complete software engineering tasks autonomously.

**Architecture:** Session-based agentic loop using Layer 1's `llm.Client`. Each turn calls `Complete()`, inspects the response for tool calls, executes them via a tool registry, appends results, and loops until the model produces a text-only response. An event emitter provides real-time visibility into session progress for UIs and loggers.

**Tech Stack:** Go standard library only. Depends on Layer 1 (`github.com/2389-research/mammoth-lite/llm`).

---

## File Map

```
agent/
├── session.go           # Session struct + agentic loop
├── session_test.go      # Session tests
├── config.go            # SessionConfig
├── config_test.go       # Config tests
├── events.go            # Event types + EventHandler interface
├── events_test.go       # Events tests
├── result.go            # SessionResult + fmt.Stringer
├── result_test.go       # Result tests
├── tools/
│   ├── registry.go      # Tool interface + Registry
│   ├── registry_test.go
│   ├── read.go          # ReadFile tool
│   ├── read_test.go
│   ├── write.go         # WriteFile tool
│   ├── write_test.go
│   ├── edit.go          # EditFile tool (search/replace)
│   ├── edit_test.go
│   ├── bash.go          # Bash tool
│   ├── bash_test.go
│   ├── glob.go          # Glob tool
│   └── glob_test.go
├── exec/
│   ├── env.go           # ExecutionEnvironment interface
│   ├── env_test.go
│   └── local.go         # LocalEnvironment implementation
```

## Dependency Order

Tasks must be built in this order due to dependencies:

```
Task 1 (events) ─┐
Task 2 (config)  ─┤
Task 3 (exec)    ─┼─→ Task 5 (registry) ─→ Task 11 (session)
Task 4 (result)  ─┤        ↑                     ↑
                   │   Tasks 6-10 (tools)         │
                   └──────────────────────────────┘
```

---

### Task 1: Event Types

Define event types that the session emits for UI/logging consumers.

**Files:**
- Create: `agent/events.go`
- Create: `agent/events_test.go`

**Step 1: Write the failing test**

```go
// agent/events_test.go
// ABOUTME: Tests for agent event types and the EventHandler interface.
// ABOUTME: Validates event construction and multi-handler fan-out.
package agent

import (
	"testing"
)

func TestEventTypes(t *testing.T) {
	// Verify all event type constants are distinct.
	types := []EventType{
		EventSessionStart,
		EventSessionEnd,
		EventTurnStart,
		EventTurnEnd,
		EventToolCallStart,
		EventToolCallEnd,
		EventTextDelta,
		EventError,
	}
	seen := make(map[EventType]bool)
	for _, et := range types {
		if seen[et] {
			t.Errorf("duplicate event type: %s", et)
		}
		seen[et] = true
	}
}

func TestMultiHandler(t *testing.T) {
	var received []Event
	handler := EventHandlerFunc(func(evt Event) {
		received = append(received, evt)
	})

	multi := MultiHandler(handler, handler)
	multi.HandleEvent(Event{Type: EventTurnStart})

	if len(received) != 2 {
		t.Fatalf("expected 2 events, got %d", len(received))
	}
}

func TestNilHandlerNoPanic(t *testing.T) {
	// NoopHandler should not panic.
	NoopHandler.HandleEvent(Event{Type: EventTurnStart})
}
```

**Step 2: Run test to verify it fails**

Run: `go test ./agent/... -run TestEvent -v`
Expected: FAIL — package doesn't exist yet

**Step 3: Write minimal implementation**

```go
// agent/events.go
// ABOUTME: Event types emitted by the agent session for UI rendering and logging.
// ABOUTME: Defines EventType constants, Event struct, EventHandler interface, and multi-handler fan-out.
package agent

import "time"

// EventType discriminates agent session events.
type EventType string

const (
	EventSessionStart  EventType = "session_start"
	EventSessionEnd    EventType = "session_end"
	EventTurnStart     EventType = "turn_start"
	EventTurnEnd       EventType = "turn_end"
	EventToolCallStart EventType = "tool_call_start"
	EventToolCallEnd   EventType = "tool_call_end"
	EventTextDelta     EventType = "text_delta"
	EventError         EventType = "error"
)

// Event is a single event emitted during an agent session.
type Event struct {
	Type      EventType
	Timestamp time.Time
	SessionID string

	// Turn metadata (set for turn events).
	Turn int

	// Tool call metadata (set for tool events).
	ToolName  string
	ToolInput string
	ToolOutput string
	ToolError  string

	// Text content (set for text events).
	Text string

	// Error (set for error events).
	Err error
}

// EventHandler receives events from an agent session.
type EventHandler interface {
	HandleEvent(evt Event)
}

// EventHandlerFunc adapts a function to the EventHandler interface.
type EventHandlerFunc func(evt Event)

func (f EventHandlerFunc) HandleEvent(evt Event) {
	f(evt)
}

// noopHandler is an EventHandler that does nothing.
type noopHandler struct{}

func (noopHandler) HandleEvent(Event) {}

// NoopHandler is an EventHandler that silently discards all events.
var NoopHandler EventHandler = noopHandler{}

// MultiHandler returns an EventHandler that fans out events to all handlers.
func MultiHandler(handlers ...EventHandler) EventHandler {
	return multiHandler(handlers)
}

type multiHandler []EventHandler

func (m multiHandler) HandleEvent(evt Event) {
	for _, h := range m {
		h.HandleEvent(evt)
	}
}
```

**Step 4: Run test to verify it passes**

Run: `go test ./agent/... -run TestEvent -v`
Expected: PASS

Run: `go test ./agent/... -run TestNil -v`
Expected: PASS

Run: `go test ./agent/... -run TestMulti -v`
Expected: PASS

**Step 5: Commit**

```bash
git add agent/events.go agent/events_test.go
git commit -m "feat(agent): add event types and handler interface"
```

---

### Task 2: Session Config

Define configuration for agent sessions.

**Files:**
- Create: `agent/config.go`
- Create: `agent/config_test.go`

**Step 1: Write the failing test**

```go
// agent/config_test.go
// ABOUTME: Tests for SessionConfig defaults and validation.
// ABOUTME: Verifies sensible defaults are applied and invalid configs are rejected.
package agent

import (
	"testing"
	"time"
)

func TestConfigDefaults(t *testing.T) {
	cfg := DefaultConfig()

	if cfg.MaxTurns != 50 {
		t.Errorf("expected MaxTurns 50, got %d", cfg.MaxTurns)
	}
	if cfg.CommandTimeout != 10*time.Second {
		t.Errorf("expected CommandTimeout 10s, got %v", cfg.CommandTimeout)
	}
	if cfg.MaxCommandTimeout != 10*time.Minute {
		t.Errorf("expected MaxCommandTimeout 10m, got %v", cfg.MaxCommandTimeout)
	}
	if cfg.LoopDetectionThreshold != 10 {
		t.Errorf("expected LoopDetectionThreshold 10, got %d", cfg.LoopDetectionThreshold)
	}
	if cfg.WorkingDir != "." {
		t.Errorf("expected WorkingDir '.', got %q", cfg.WorkingDir)
	}
}

func TestConfigValidation(t *testing.T) {
	cfg := DefaultConfig()
	if err := cfg.Validate(); err != nil {
		t.Errorf("default config should be valid: %v", err)
	}

	bad := SessionConfig{MaxTurns: 0}
	if err := bad.Validate(); err == nil {
		t.Error("expected error for MaxTurns=0")
	}
}
```

**Step 2: Run test to verify it fails**

Run: `go test ./agent/... -run TestConfig -v`
Expected: FAIL

**Step 3: Write minimal implementation**

```go
// agent/config.go
// ABOUTME: Configuration for agent sessions including turn limits, timeouts, and loop detection.
// ABOUTME: Provides sensible defaults via DefaultConfig() and validation via Validate().
package agent

import (
	"fmt"
	"time"
)

// SessionConfig holds configuration for an agent session.
type SessionConfig struct {
	// MaxTurns is the maximum number of agentic turns before stopping.
	MaxTurns int

	// CommandTimeout is the default timeout for shell command execution.
	CommandTimeout time.Duration

	// MaxCommandTimeout is the maximum allowed timeout for shell commands.
	MaxCommandTimeout time.Duration

	// LoopDetectionThreshold is the number of identical consecutive tool calls
	// before the session emits a warning and injects a steering message.
	LoopDetectionThreshold int

	// WorkingDir is the root directory for file operations.
	WorkingDir string

	// SystemPrompt is prepended to the conversation as a system message.
	SystemPrompt string

	// Model is the LLM model ID to use.
	Model string

	// Provider is the LLM provider name (empty = use client default).
	Provider string
}

// DefaultConfig returns a SessionConfig with sensible defaults.
func DefaultConfig() SessionConfig {
	return SessionConfig{
		MaxTurns:               50,
		CommandTimeout:         10 * time.Second,
		MaxCommandTimeout:      10 * time.Minute,
		LoopDetectionThreshold: 10,
		WorkingDir:             ".",
	}
}

// Validate checks the config for invalid values.
func (c SessionConfig) Validate() error {
	if c.MaxTurns < 1 {
		return fmt.Errorf("MaxTurns must be >= 1, got %d", c.MaxTurns)
	}
	if c.CommandTimeout < 0 {
		return fmt.Errorf("CommandTimeout must be >= 0, got %v", c.CommandTimeout)
	}
	if c.MaxCommandTimeout < 0 {
		return fmt.Errorf("MaxCommandTimeout must be >= 0, got %v", c.MaxCommandTimeout)
	}
	if c.LoopDetectionThreshold < 1 {
		return fmt.Errorf("LoopDetectionThreshold must be >= 1, got %d", c.LoopDetectionThreshold)
	}
	return nil
}
```

**Step 4: Run test to verify it passes**

Run: `go test ./agent/... -run TestConfig -v`
Expected: PASS

**Step 5: Commit**

```bash
git add agent/config.go agent/config_test.go
git commit -m "feat(agent): add session config with defaults and validation"
```

---

### Task 3: Execution Environment

Define the interface for where tools run, plus the default local implementation.

**Files:**
- Create: `agent/exec/env.go`
- Create: `agent/exec/env_test.go`
- Create: `agent/exec/local.go`

**Step 1: Write the failing test**

```go
// agent/exec/env_test.go
// ABOUTME: Tests for ExecutionEnvironment interface and LocalEnvironment implementation.
// ABOUTME: Validates file operations, command execution, and glob matching against real filesystem.
package exec

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestLocalReadFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "test.txt")
	os.WriteFile(path, []byte("hello world"), 0644)

	env := NewLocalEnvironment(dir)
	content, err := env.ReadFile(context.Background(), "test.txt")
	if err != nil {
		t.Fatalf("ReadFile failed: %v", err)
	}
	if content != "hello world" {
		t.Errorf("expected 'hello world', got %q", content)
	}
}

func TestLocalReadFileNotFound(t *testing.T) {
	env := NewLocalEnvironment(t.TempDir())
	_, err := env.ReadFile(context.Background(), "nonexistent.txt")
	if err == nil {
		t.Error("expected error for missing file")
	}
}

func TestLocalWriteFile(t *testing.T) {
	dir := t.TempDir()
	env := NewLocalEnvironment(dir)

	err := env.WriteFile(context.Background(), "output.txt", "content here")
	if err != nil {
		t.Fatalf("WriteFile failed: %v", err)
	}

	data, _ := os.ReadFile(filepath.Join(dir, "output.txt"))
	if string(data) != "content here" {
		t.Errorf("expected 'content here', got %q", string(data))
	}
}

func TestLocalWriteFileCreatesDirectories(t *testing.T) {
	dir := t.TempDir()
	env := NewLocalEnvironment(dir)

	err := env.WriteFile(context.Background(), "sub/dir/file.txt", "nested")
	if err != nil {
		t.Fatalf("WriteFile failed: %v", err)
	}

	data, _ := os.ReadFile(filepath.Join(dir, "sub/dir/file.txt"))
	if string(data) != "nested" {
		t.Errorf("expected 'nested', got %q", string(data))
	}
}

func TestLocalExecCommand(t *testing.T) {
	env := NewLocalEnvironment(t.TempDir())
	result, err := env.ExecCommand(context.Background(), "echo", []string{"hello"}, 5*time.Second)
	if err != nil {
		t.Fatalf("ExecCommand failed: %v", err)
	}
	if result.ExitCode != 0 {
		t.Errorf("expected exit code 0, got %d", result.ExitCode)
	}
	if !strings.Contains(result.Stdout, "hello") {
		t.Errorf("expected stdout to contain 'hello', got %q", result.Stdout)
	}
}

func TestLocalExecCommandTimeout(t *testing.T) {
	env := NewLocalEnvironment(t.TempDir())
	_, err := env.ExecCommand(context.Background(), "sleep", []string{"10"}, 100*time.Millisecond)
	if err == nil {
		t.Error("expected timeout error")
	}
}

func TestLocalExecCommandFailure(t *testing.T) {
	env := NewLocalEnvironment(t.TempDir())
	result, err := env.ExecCommand(context.Background(), "sh", []string{"-c", "exit 42"}, 5*time.Second)
	if err != nil {
		t.Fatalf("ExecCommand should not error on non-zero exit: %v", err)
	}
	if result.ExitCode != 42 {
		t.Errorf("expected exit code 42, got %d", result.ExitCode)
	}
}

func TestLocalGlob(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "a.go"), []byte(""), 0644)
	os.WriteFile(filepath.Join(dir, "b.go"), []byte(""), 0644)
	os.WriteFile(filepath.Join(dir, "c.txt"), []byte(""), 0644)

	env := NewLocalEnvironment(dir)
	matches, err := env.Glob(context.Background(), "*.go")
	if err != nil {
		t.Fatalf("Glob failed: %v", err)
	}
	if len(matches) != 2 {
		t.Errorf("expected 2 matches, got %d: %v", len(matches), matches)
	}
}

func TestLocalPathEscapePrevention(t *testing.T) {
	dir := t.TempDir()
	env := NewLocalEnvironment(dir)

	_, err := env.ReadFile(context.Background(), "../../../etc/passwd")
	if err == nil {
		t.Error("expected error for path traversal")
	}
}
```

**Step 2: Run test to verify it fails**

Run: `go test ./agent/exec/... -v`
Expected: FAIL

**Step 3: Write minimal implementation**

```go
// agent/exec/env.go
// ABOUTME: ExecutionEnvironment interface abstracting where agent tools run.
// ABOUTME: Enables local execution (default) with future extensibility to Docker/SSH/K8s.
package exec

import (
	"context"
	"time"
)

// CommandResult holds the output of a command execution.
type CommandResult struct {
	Stdout   string
	Stderr   string
	ExitCode int
}

// ExecutionEnvironment abstracts where tools run.
type ExecutionEnvironment interface {
	// ReadFile reads a file relative to the working directory.
	ReadFile(ctx context.Context, path string) (string, error)

	// WriteFile writes content to a file relative to the working directory.
	// Creates parent directories as needed.
	WriteFile(ctx context.Context, path string, content string) error

	// ExecCommand runs a command with the given arguments and timeout.
	// Returns the result even on non-zero exit codes (err is for execution failures).
	ExecCommand(ctx context.Context, command string, args []string, timeout time.Duration) (CommandResult, error)

	// Glob returns file paths matching a pattern relative to the working directory.
	Glob(ctx context.Context, pattern string) ([]string, error)

	// WorkingDir returns the root working directory.
	WorkingDir() string
}
```

```go
// agent/exec/local.go
// ABOUTME: LocalEnvironment implements ExecutionEnvironment for local filesystem and process execution.
// ABOUTME: Enforces path containment within the working directory to prevent traversal attacks.
package exec

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

// LocalEnvironment executes tools on the local filesystem.
type LocalEnvironment struct {
	workDir string
}

// NewLocalEnvironment creates a LocalEnvironment rooted at the given directory.
func NewLocalEnvironment(workDir string) *LocalEnvironment {
	abs, err := filepath.Abs(workDir)
	if err != nil {
		abs = workDir
	}
	return &LocalEnvironment{workDir: abs}
}

// WorkingDir returns the root working directory.
func (e *LocalEnvironment) WorkingDir() string {
	return e.workDir
}

// safePath resolves a relative path within the working directory,
// rejecting any path that escapes via traversal.
func (e *LocalEnvironment) safePath(rel string) (string, error) {
	if filepath.IsAbs(rel) {
		return "", fmt.Errorf("absolute paths not allowed: %s", rel)
	}

	joined := filepath.Join(e.workDir, rel)
	abs, err := filepath.Abs(joined)
	if err != nil {
		return "", err
	}

	// Ensure the resolved path is within the working directory.
	if !strings.HasPrefix(abs, e.workDir+string(filepath.Separator)) && abs != e.workDir {
		return "", fmt.Errorf("path escapes working directory: %s", rel)
	}

	return abs, nil
}

func (e *LocalEnvironment) ReadFile(ctx context.Context, path string) (string, error) {
	abs, err := e.safePath(path)
	if err != nil {
		return "", err
	}

	data, err := os.ReadFile(abs)
	if err != nil {
		return "", err
	}

	return string(data), nil
}

func (e *LocalEnvironment) WriteFile(ctx context.Context, path string, content string) error {
	abs, err := e.safePath(path)
	if err != nil {
		return err
	}

	dir := filepath.Dir(abs)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return err
	}

	return os.WriteFile(abs, []byte(content), 0644)
}

func (e *LocalEnvironment) ExecCommand(ctx context.Context, command string, args []string, timeout time.Duration) (CommandResult, error) {
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	cmd := exec.CommandContext(ctx, command, args...)
	cmd.Dir = e.workDir

	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	err := cmd.Run()

	result := CommandResult{
		Stdout: stdout.String(),
		Stderr: stderr.String(),
	}

	if err != nil {
		if ctx.Err() != nil {
			return result, fmt.Errorf("command timed out after %v", timeout)
		}
		if exitErr, ok := err.(*exec.ExitError); ok {
			result.ExitCode = exitErr.ExitCode()
			return result, nil
		}
		return result, err
	}

	return result, nil
}

func (e *LocalEnvironment) Glob(ctx context.Context, pattern string) ([]string, error) {
	fullPattern := filepath.Join(e.workDir, pattern)
	matches, err := filepath.Glob(fullPattern)
	if err != nil {
		return nil, err
	}

	// Convert to relative paths.
	var rel []string
	for _, m := range matches {
		r, err := filepath.Rel(e.workDir, m)
		if err != nil {
			continue
		}
		rel = append(rel, r)
	}

	return rel, nil
}
```

**Step 4: Run test to verify it passes**

Run: `go test ./agent/exec/... -v`
Expected: PASS (all 8 tests)

**Step 5: Commit**

```bash
git add agent/exec/
git commit -m "feat(agent): add ExecutionEnvironment interface and local implementation"
```

---

### Task 4: Session Result

Define the result type returned when a session completes.

**Files:**
- Create: `agent/result.go`
- Create: `agent/result_test.go`

**Step 1: Write the failing test**

```go
// agent/result_test.go
// ABOUTME: Tests for SessionResult formatting and statistics.
// ABOUTME: Validates the String() output matches the design doc format.
package agent

import (
	"strings"
	"testing"
	"time"

	"github.com/2389-research/mammoth-lite/llm"
)

func TestResultString(t *testing.T) {
	r := SessionResult{
		SessionID: "a3f2",
		Duration:  2*time.Minute + 34*time.Second,
		Turns:     14,
		ToolCalls: map[string]int{"read": 12, "edit": 3, "bash": 8},
		FilesModified: []string{"auth.go", "auth_test.go"},
		FilesCreated:  []string{"oauth_handler.go"},
		Usage: llm.Usage{
			InputTokens:  32100,
			OutputTokens: 13131,
			TotalTokens:  45231,
		},
	}

	s := r.String()

	if !strings.Contains(s, "a3f2") {
		t.Errorf("expected session ID in output: %s", s)
	}
	if !strings.Contains(s, "2m34s") {
		t.Errorf("expected duration in output: %s", s)
	}
	if !strings.Contains(s, "14") {
		t.Errorf("expected turn count in output: %s", s)
	}
	if !strings.Contains(s, "23") {
		t.Errorf("expected total tool calls in output: %s", s)
	}
}

func TestResultTotalToolCalls(t *testing.T) {
	r := SessionResult{
		ToolCalls: map[string]int{"read": 5, "write": 3},
	}
	if r.TotalToolCalls() != 8 {
		t.Errorf("expected 8 total tool calls, got %d", r.TotalToolCalls())
	}
}
```

**Step 2: Run test to verify it fails**

Run: `go test ./agent/... -run TestResult -v`
Expected: FAIL

**Step 3: Write minimal implementation**

```go
// agent/result.go
// ABOUTME: SessionResult captures the outcome of a completed agent session.
// ABOUTME: Tracks turns, tool calls, file changes, token usage, and provides pretty-print formatting.
package agent

import (
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/2389-research/mammoth-lite/llm"
)

// SessionResult is the outcome of a completed agent session.
type SessionResult struct {
	SessionID     string
	Duration      time.Duration
	Turns         int
	ToolCalls     map[string]int
	FilesModified []string
	FilesCreated  []string
	Usage         llm.Usage
	Error         error
}

// TotalToolCalls returns the sum of all tool call counts.
func (r SessionResult) TotalToolCalls() int {
	total := 0
	for _, count := range r.ToolCalls {
		total += count
	}
	return total
}

// String formats the result matching the design doc output format.
func (r SessionResult) String() string {
	var b strings.Builder

	status := "completed"
	if r.Error != nil {
		status = "failed"
	}

	fmt.Fprintf(&b, "Session %s %s in %s\n", r.SessionID, status, r.Duration.Round(time.Second))

	// Tool call breakdown.
	var toolParts []string
	keys := make([]string, 0, len(r.ToolCalls))
	for k := range r.ToolCalls {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		toolParts = append(toolParts, fmt.Sprintf("%s: %d", k, r.ToolCalls[k]))
	}
	fmt.Fprintf(&b, "Turns: %d | Tool calls: %d", r.Turns, r.TotalToolCalls())
	if len(toolParts) > 0 {
		fmt.Fprintf(&b, " (%s)", strings.Join(toolParts, ", "))
	}
	b.WriteString("\n")

	if len(r.FilesModified) > 0 {
		fmt.Fprintf(&b, "Files modified: %s\n", strings.Join(r.FilesModified, ", "))
	}
	if len(r.FilesCreated) > 0 {
		fmt.Fprintf(&b, "Files created: %s\n", strings.Join(r.FilesCreated, ", "))
	}

	fmt.Fprintf(&b, "Tokens: %d (in: %d, out: %d)",
		r.Usage.TotalTokens, r.Usage.InputTokens, r.Usage.OutputTokens)
	if r.Usage.EstimatedCost > 0 {
		fmt.Fprintf(&b, " | Cost: $%.2f", r.Usage.EstimatedCost)
	}
	b.WriteString("\n")

	return b.String()
}
```

**Step 4: Run test to verify it passes**

Run: `go test ./agent/... -run TestResult -v`
Expected: PASS

**Step 5: Commit**

```bash
git add agent/result.go agent/result_test.go
git commit -m "feat(agent): add SessionResult with pretty-print formatting"
```

---

### Task 5: Tool Registry

Define the Tool interface and a registry for dispatching tool calls.

**Files:**
- Create: `agent/tools/registry.go`
- Create: `agent/tools/registry_test.go`

**Step 1: Write the failing test**

```go
// agent/tools/registry_test.go
// ABOUTME: Tests for Tool interface and Registry dispatch.
// ABOUTME: Validates tool registration, lookup, definition export, and execution dispatch.
package tools

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/2389-research/mammoth-lite/llm"
)

// stubTool is a minimal Tool for testing.
type stubTool struct {
	name   string
	result string
}

func (s *stubTool) Name() string        { return s.name }
func (s *stubTool) Description() string { return "A stub tool" }
func (s *stubTool) Parameters() json.RawMessage {
	return json.RawMessage(`{"type":"object","properties":{}}`)
}
func (s *stubTool) Execute(ctx context.Context, input json.RawMessage) (string, error) {
	return s.result, nil
}

func TestRegistryRegisterAndLookup(t *testing.T) {
	r := NewRegistry()
	tool := &stubTool{name: "test_tool", result: "ok"}
	r.Register(tool)

	found := r.Get("test_tool")
	if found == nil {
		t.Fatal("expected to find tool")
	}
	if found.Name() != "test_tool" {
		t.Errorf("expected name 'test_tool', got %q", found.Name())
	}
}

func TestRegistryLookupMissing(t *testing.T) {
	r := NewRegistry()
	if r.Get("nonexistent") != nil {
		t.Error("expected nil for missing tool")
	}
}

func TestRegistryDefinitions(t *testing.T) {
	r := NewRegistry()
	r.Register(&stubTool{name: "alpha"})
	r.Register(&stubTool{name: "beta"})

	defs := r.Definitions()
	if len(defs) != 2 {
		t.Fatalf("expected 2 definitions, got %d", len(defs))
	}

	// Verify they're llm.ToolDefinition types.
	for _, d := range defs {
		if d.Name == "" {
			t.Error("expected non-empty tool name")
		}
	}
}

func TestRegistryExecute(t *testing.T) {
	r := NewRegistry()
	r.Register(&stubTool{name: "greeter", result: "hello"})

	call := llm.ToolCallData{
		ID:        "call_1",
		Name:      "greeter",
		Arguments: json.RawMessage(`{}`),
	}

	result := r.Execute(context.Background(), call)
	if result.Content != "hello" {
		t.Errorf("expected 'hello', got %q", result.Content)
	}
	if result.IsError {
		t.Error("expected no error")
	}
	if result.ToolCallID != "call_1" {
		t.Errorf("expected call ID 'call_1', got %q", result.ToolCallID)
	}
	if result.Name != "greeter" {
		t.Errorf("expected name 'greeter', got %q", result.Name)
	}
}

func TestRegistryExecuteUnknownTool(t *testing.T) {
	r := NewRegistry()
	call := llm.ToolCallData{
		ID:        "call_1",
		Name:      "unknown",
		Arguments: json.RawMessage(`{}`),
	}

	result := r.Execute(context.Background(), call)
	if !result.IsError {
		t.Error("expected error for unknown tool")
	}
}
```

**Step 2: Run test to verify it fails**

Run: `go test ./agent/tools/... -v`
Expected: FAIL

**Step 3: Write minimal implementation**

```go
// agent/tools/registry.go
// ABOUTME: Tool interface and Registry for agent tool dispatch.
// ABOUTME: Tools register by name, export LLM tool definitions, and execute via ToolCallData.
package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"

	"github.com/2389-research/mammoth-lite/llm"
)

// Tool is the interface that all agent tools implement.
type Tool interface {
	// Name returns the tool's identifier (used in LLM tool definitions).
	Name() string

	// Description returns a human-readable description for the LLM.
	Description() string

	// Parameters returns the JSON Schema for the tool's input.
	Parameters() json.RawMessage

	// Execute runs the tool with the given JSON input and returns the result string.
	Execute(ctx context.Context, input json.RawMessage) (string, error)
}

// Registry holds registered tools and dispatches tool calls.
type Registry struct {
	tools map[string]Tool
}

// NewRegistry creates an empty tool registry.
func NewRegistry() *Registry {
	return &Registry{tools: make(map[string]Tool)}
}

// Register adds a tool to the registry.
func (r *Registry) Register(tool Tool) {
	r.tools[tool.Name()] = tool
}

// Get returns a tool by name, or nil if not found.
func (r *Registry) Get(name string) Tool {
	return r.tools[name]
}

// Definitions returns LLM tool definitions for all registered tools,
// sorted by name for deterministic ordering.
func (r *Registry) Definitions() []llm.ToolDefinition {
	names := make([]string, 0, len(r.tools))
	for name := range r.tools {
		names = append(names, name)
	}
	sort.Strings(names)

	defs := make([]llm.ToolDefinition, 0, len(r.tools))
	for _, name := range names {
		t := r.tools[name]
		defs = append(defs, llm.ToolDefinition{
			Name:        t.Name(),
			Description: t.Description(),
			Parameters:  t.Parameters(),
		})
	}
	return defs
}

// Execute dispatches a tool call and returns the result.
// If the tool is not found or errors, the result has IsError=true.
func (r *Registry) Execute(ctx context.Context, call llm.ToolCallData) llm.ToolResultData {
	tool := r.Get(call.Name)
	if tool == nil {
		return llm.ToolResultData{
			ToolCallID: call.ID,
			Name:       call.Name,
			Content:    fmt.Sprintf("error: unknown tool %q", call.Name),
			IsError:    true,
		}
	}

	output, err := tool.Execute(ctx, call.Arguments)
	if err != nil {
		return llm.ToolResultData{
			ToolCallID: call.ID,
			Name:       call.Name,
			Content:    fmt.Sprintf("error: %s", err.Error()),
			IsError:    true,
		}
	}

	return llm.ToolResultData{
		ToolCallID: call.ID,
		Name:       call.Name,
		Content:    output,
		IsError:    false,
	}
}
```

**Step 4: Run test to verify it passes**

Run: `go test ./agent/tools/... -v`
Expected: PASS (all 5 tests)

**Step 5: Commit**

```bash
git add agent/tools/
git commit -m "feat(agent): add tool interface and registry with dispatch"
```

---

### Task 6: ReadFile Tool

**Files:**
- Create: `agent/tools/read.go`
- Create: `agent/tools/read_test.go`

**Step 1: Write the failing test**

```go
// agent/tools/read_test.go
// ABOUTME: Tests for the ReadFile tool.
// ABOUTME: Validates file reading, missing file error, and parameter parsing.
package tools

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/2389-research/mammoth-lite/agent/exec"
)

func TestReadToolExecute(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "hello.txt"), []byte("hello world"), 0644)
	env := exec.NewLocalEnvironment(dir)
	tool := NewReadTool(env)

	input := json.RawMessage(`{"path": "hello.txt"}`)
	result, err := tool.Execute(context.Background(), input)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result != "hello world" {
		t.Errorf("expected 'hello world', got %q", result)
	}
}

func TestReadToolMissingFile(t *testing.T) {
	env := exec.NewLocalEnvironment(t.TempDir())
	tool := NewReadTool(env)

	input := json.RawMessage(`{"path": "nope.txt"}`)
	_, err := tool.Execute(context.Background(), input)
	if err == nil {
		t.Error("expected error for missing file")
	}
}

func TestReadToolInterface(t *testing.T) {
	env := exec.NewLocalEnvironment(t.TempDir())
	tool := NewReadTool(env)

	if tool.Name() != "read" {
		t.Errorf("expected name 'read', got %q", tool.Name())
	}
	if !strings.Contains(tool.Description(), "file") {
		t.Errorf("description should mention file: %q", tool.Description())
	}
	var params map[string]any
	json.Unmarshal(tool.Parameters(), &params)
	if params["type"] != "object" {
		t.Errorf("parameters should be an object schema")
	}
}
```

**Step 2: Run test to verify it fails**

Run: `go test ./agent/tools/... -run TestRead -v`
Expected: FAIL

**Step 3: Write minimal implementation**

```go
// agent/tools/read.go
// ABOUTME: ReadFile tool reads the contents of a file in the working directory.
// ABOUTME: Accepts a path parameter and returns file contents as a string.
package tools

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/2389-research/mammoth-lite/agent/exec"
)

// ReadTool reads file contents.
type ReadTool struct {
	env exec.ExecutionEnvironment
}

// NewReadTool creates a ReadTool backed by the given execution environment.
func NewReadTool(env exec.ExecutionEnvironment) *ReadTool {
	return &ReadTool{env: env}
}

func (t *ReadTool) Name() string { return "read" }

func (t *ReadTool) Description() string {
	return "Read the contents of a file at the given path."
}

func (t *ReadTool) Parameters() json.RawMessage {
	return json.RawMessage(`{
		"type": "object",
		"properties": {
			"path": {
				"type": "string",
				"description": "The relative path of the file to read."
			}
		},
		"required": ["path"]
	}`)
}

func (t *ReadTool) Execute(ctx context.Context, input json.RawMessage) (string, error) {
	var params struct {
		Path string `json:"path"`
	}
	if err := json.Unmarshal(input, &params); err != nil {
		return "", fmt.Errorf("invalid input: %w", err)
	}
	if params.Path == "" {
		return "", fmt.Errorf("path is required")
	}
	return t.env.ReadFile(ctx, params.Path)
}
```

**Step 4: Run test to verify it passes**

Run: `go test ./agent/tools/... -run TestRead -v`
Expected: PASS

**Step 5: Commit**

```bash
git add agent/tools/read.go agent/tools/read_test.go
git commit -m "feat(agent): add read file tool"
```

---

### Task 7: WriteFile Tool

**Files:**
- Create: `agent/tools/write.go`
- Create: `agent/tools/write_test.go`

**Step 1: Write the failing test**

```go
// agent/tools/write_test.go
// ABOUTME: Tests for the WriteFile tool.
// ABOUTME: Validates file creation, overwrite, directory creation, and parameter parsing.
package tools

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/2389-research/mammoth-lite/agent/exec"
)

func TestWriteToolExecute(t *testing.T) {
	dir := t.TempDir()
	env := exec.NewLocalEnvironment(dir)
	tool := NewWriteTool(env)

	input := json.RawMessage(`{"path": "out.txt", "content": "new file"}`)
	result, err := tool.Execute(context.Background(), input)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result == "" {
		t.Error("expected non-empty result")
	}

	data, _ := os.ReadFile(filepath.Join(dir, "out.txt"))
	if string(data) != "new file" {
		t.Errorf("expected 'new file', got %q", string(data))
	}
}

func TestWriteToolCreatesDirectories(t *testing.T) {
	dir := t.TempDir()
	env := exec.NewLocalEnvironment(dir)
	tool := NewWriteTool(env)

	input := json.RawMessage(`{"path": "a/b/c.txt", "content": "deep"}`)
	_, err := tool.Execute(context.Background(), input)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	data, _ := os.ReadFile(filepath.Join(dir, "a/b/c.txt"))
	if string(data) != "deep" {
		t.Errorf("expected 'deep', got %q", string(data))
	}
}

func TestWriteToolMissingPath(t *testing.T) {
	env := exec.NewLocalEnvironment(t.TempDir())
	tool := NewWriteTool(env)

	input := json.RawMessage(`{"content": "no path"}`)
	_, err := tool.Execute(context.Background(), input)
	if err == nil {
		t.Error("expected error for missing path")
	}
}
```

**Step 2: Run test to verify it fails**

Run: `go test ./agent/tools/... -run TestWrite -v`
Expected: FAIL

**Step 3: Write minimal implementation**

```go
// agent/tools/write.go
// ABOUTME: WriteFile tool creates or overwrites a file in the working directory.
// ABOUTME: Accepts path and content parameters, creates parent directories as needed.
package tools

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/2389-research/mammoth-lite/agent/exec"
)

// WriteTool writes content to a file.
type WriteTool struct {
	env exec.ExecutionEnvironment
}

// NewWriteTool creates a WriteTool backed by the given execution environment.
func NewWriteTool(env exec.ExecutionEnvironment) *WriteTool {
	return &WriteTool{env: env}
}

func (t *WriteTool) Name() string { return "write" }

func (t *WriteTool) Description() string {
	return "Create or overwrite a file with the given content. Creates parent directories as needed."
}

func (t *WriteTool) Parameters() json.RawMessage {
	return json.RawMessage(`{
		"type": "object",
		"properties": {
			"path": {
				"type": "string",
				"description": "The relative path of the file to write."
			},
			"content": {
				"type": "string",
				"description": "The content to write to the file."
			}
		},
		"required": ["path", "content"]
	}`)
}

func (t *WriteTool) Execute(ctx context.Context, input json.RawMessage) (string, error) {
	var params struct {
		Path    string `json:"path"`
		Content string `json:"content"`
	}
	if err := json.Unmarshal(input, &params); err != nil {
		return "", fmt.Errorf("invalid input: %w", err)
	}
	if params.Path == "" {
		return "", fmt.Errorf("path is required")
	}

	if err := t.env.WriteFile(ctx, params.Path, params.Content); err != nil {
		return "", err
	}

	return fmt.Sprintf("wrote %d bytes to %s", len(params.Content), params.Path), nil
}
```

**Step 4: Run test to verify it passes**

Run: `go test ./agent/tools/... -run TestWrite -v`
Expected: PASS

**Step 5: Commit**

```bash
git add agent/tools/write.go agent/tools/write_test.go
git commit -m "feat(agent): add write file tool"
```

---

### Task 8: EditFile Tool (Search/Replace)

The edit tool uses exact string search/replace rather than a diff format. This is simpler, more reliable, and matches how Claude Code works.

**Files:**
- Create: `agent/tools/edit.go`
- Create: `agent/tools/edit_test.go`

**Step 1: Write the failing test**

```go
// agent/tools/edit_test.go
// ABOUTME: Tests for the EditFile tool (search/replace).
// ABOUTME: Validates exact replacement, multi-occurrence, missing match, and file creation.
package tools

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/2389-research/mammoth-lite/agent/exec"
)

func TestEditToolReplace(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "code.go"), []byte("func hello() {\n\treturn \"hi\"\n}"), 0644)

	env := exec.NewLocalEnvironment(dir)
	tool := NewEditTool(env)

	input := json.RawMessage(`{
		"path": "code.go",
		"old_string": "return \"hi\"",
		"new_string": "return \"hello world\""
	}`)
	result, err := tool.Execute(context.Background(), input)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result == "" {
		t.Error("expected non-empty result")
	}

	data, _ := os.ReadFile(filepath.Join(dir, "code.go"))
	if string(data) != "func hello() {\n\treturn \"hello world\"\n}" {
		t.Errorf("unexpected content: %q", string(data))
	}
}

func TestEditToolNoMatch(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "file.txt"), []byte("original"), 0644)

	env := exec.NewLocalEnvironment(dir)
	tool := NewEditTool(env)

	input := json.RawMessage(`{
		"path": "file.txt",
		"old_string": "not found",
		"new_string": "replacement"
	}`)
	_, err := tool.Execute(context.Background(), input)
	if err == nil {
		t.Error("expected error when old_string not found")
	}
}

func TestEditToolMultipleMatches(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "file.txt"), []byte("aaa bbb aaa"), 0644)

	env := exec.NewLocalEnvironment(dir)
	tool := NewEditTool(env)

	input := json.RawMessage(`{
		"path": "file.txt",
		"old_string": "aaa",
		"new_string": "ccc"
	}`)
	_, err := tool.Execute(context.Background(), input)
	if err == nil {
		t.Error("expected error for ambiguous match (multiple occurrences)")
	}
}

func TestEditToolCreateNewFile(t *testing.T) {
	dir := t.TempDir()
	env := exec.NewLocalEnvironment(dir)
	tool := NewEditTool(env)

	// When old_string is empty and file doesn't exist, create the file.
	input := json.RawMessage(`{
		"path": "new.txt",
		"old_string": "",
		"new_string": "brand new content"
	}`)
	_, err := tool.Execute(context.Background(), input)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	data, _ := os.ReadFile(filepath.Join(dir, "new.txt"))
	if string(data) != "brand new content" {
		t.Errorf("expected 'brand new content', got %q", string(data))
	}
}
```

**Step 2: Run test to verify it fails**

Run: `go test ./agent/tools/... -run TestEdit -v`
Expected: FAIL

**Step 3: Write minimal implementation**

```go
// agent/tools/edit.go
// ABOUTME: EditFile tool performs exact search/replace on files in the working directory.
// ABOUTME: Requires unique match of old_string. Empty old_string with missing file creates a new file.
package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/2389-research/mammoth-lite/agent/exec"
)

// EditTool performs search/replace edits on files.
type EditTool struct {
	env exec.ExecutionEnvironment
}

// NewEditTool creates an EditTool backed by the given execution environment.
func NewEditTool(env exec.ExecutionEnvironment) *EditTool {
	return &EditTool{env: env}
}

func (t *EditTool) Name() string { return "edit" }

func (t *EditTool) Description() string {
	return "Edit a file by replacing an exact string match with new content. " +
		"The old_string must match exactly one location in the file. " +
		"If old_string is empty and the file does not exist, the file is created with new_string as content."
}

func (t *EditTool) Parameters() json.RawMessage {
	return json.RawMessage(`{
		"type": "object",
		"properties": {
			"path": {
				"type": "string",
				"description": "The relative path of the file to edit."
			},
			"old_string": {
				"type": "string",
				"description": "The exact string to find and replace. Must match exactly once."
			},
			"new_string": {
				"type": "string",
				"description": "The replacement string."
			}
		},
		"required": ["path", "old_string", "new_string"]
	}`)
}

func (t *EditTool) Execute(ctx context.Context, input json.RawMessage) (string, error) {
	var params struct {
		Path      string `json:"path"`
		OldString string `json:"old_string"`
		NewString string `json:"new_string"`
	}
	if err := json.Unmarshal(input, &params); err != nil {
		return "", fmt.Errorf("invalid input: %w", err)
	}
	if params.Path == "" {
		return "", fmt.Errorf("path is required")
	}

	// Read existing file content.
	content, readErr := t.env.ReadFile(ctx, params.Path)

	// If old_string is empty and file doesn't exist, create the file.
	if params.OldString == "" {
		if readErr != nil {
			// File doesn't exist — create it.
			if err := t.env.WriteFile(ctx, params.Path, params.NewString); err != nil {
				return "", err
			}
			return fmt.Sprintf("created %s", params.Path), nil
		}
		// File exists but old_string is empty — prepend new_string.
		newContent := params.NewString + content
		if err := t.env.WriteFile(ctx, params.Path, newContent); err != nil {
			return "", err
		}
		return fmt.Sprintf("prepended to %s", params.Path), nil
	}

	if readErr != nil {
		return "", fmt.Errorf("cannot read file: %w", readErr)
	}

	// Count occurrences.
	count := strings.Count(content, params.OldString)
	if count == 0 {
		return "", fmt.Errorf("old_string not found in %s", params.Path)
	}
	if count > 1 {
		return "", fmt.Errorf("old_string found %d times in %s (must be unique)", count, params.Path)
	}

	// Replace.
	newContent := strings.Replace(content, params.OldString, params.NewString, 1)
	if err := t.env.WriteFile(ctx, params.Path, newContent); err != nil {
		return "", err
	}

	return fmt.Sprintf("edited %s", params.Path), nil
}
```

**Step 4: Run test to verify it passes**

Run: `go test ./agent/tools/... -run TestEdit -v`
Expected: PASS

**Step 5: Commit**

```bash
git add agent/tools/edit.go agent/tools/edit_test.go
git commit -m "feat(agent): add edit file tool (search/replace)"
```

---

### Task 9: Bash Tool

**Files:**
- Create: `agent/tools/bash.go`
- Create: `agent/tools/bash_test.go`

**Step 1: Write the failing test**

```go
// agent/tools/bash_test.go
// ABOUTME: Tests for the Bash tool.
// ABOUTME: Validates command execution, exit codes, timeout, and output formatting.
package tools

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/2389-research/mammoth-lite/agent/exec"
)

func TestBashToolExecute(t *testing.T) {
	env := exec.NewLocalEnvironment(t.TempDir())
	tool := NewBashTool(env, 5*time.Second, 10*time.Second)

	input := json.RawMessage(`{"command": "echo hello world"}`)
	result, err := tool.Execute(context.Background(), input)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(result, "hello world") {
		t.Errorf("expected output to contain 'hello world', got %q", result)
	}
}

func TestBashToolNonZeroExit(t *testing.T) {
	env := exec.NewLocalEnvironment(t.TempDir())
	tool := NewBashTool(env, 5*time.Second, 10*time.Second)

	input := json.RawMessage(`{"command": "exit 1"}`)
	result, err := tool.Execute(context.Background(), input)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// Non-zero exits are returned as results (not errors) so the model can adapt.
	if !strings.Contains(result, "exit code: 1") {
		t.Errorf("expected exit code info, got %q", result)
	}
}

func TestBashToolTimeout(t *testing.T) {
	env := exec.NewLocalEnvironment(t.TempDir())
	tool := NewBashTool(env, 200*time.Millisecond, 500*time.Millisecond)

	input := json.RawMessage(`{"command": "sleep 10"}`)
	_, err := tool.Execute(context.Background(), input)
	if err == nil {
		t.Error("expected timeout error")
	}
}

func TestBashToolCustomTimeout(t *testing.T) {
	env := exec.NewLocalEnvironment(t.TempDir())
	tool := NewBashTool(env, 200*time.Millisecond, 10*time.Second)

	// Custom timeout that's longer than default but within max.
	input := json.RawMessage(`{"command": "sleep 0.1", "timeout": 5}`)
	_, err := tool.Execute(context.Background(), input)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestBashToolEmptyCommand(t *testing.T) {
	env := exec.NewLocalEnvironment(t.TempDir())
	tool := NewBashTool(env, 5*time.Second, 10*time.Second)

	input := json.RawMessage(`{"command": ""}`)
	_, err := tool.Execute(context.Background(), input)
	if err == nil {
		t.Error("expected error for empty command")
	}
}
```

**Step 2: Run test to verify it fails**

Run: `go test ./agent/tools/... -run TestBash -v`
Expected: FAIL

**Step 3: Write minimal implementation**

```go
// agent/tools/bash.go
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

// BashTool executes shell commands.
type BashTool struct {
	env            exec.ExecutionEnvironment
	defaultTimeout time.Duration
	maxTimeout     time.Duration
}

// NewBashTool creates a BashTool with the given timeout constraints.
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
```

**Step 4: Run test to verify it passes**

Run: `go test ./agent/tools/... -run TestBash -v`
Expected: PASS

**Step 5: Commit**

```bash
git add agent/tools/bash.go agent/tools/bash_test.go
git commit -m "feat(agent): add bash tool with configurable timeouts"
```

---

### Task 10: Glob Tool

**Files:**
- Create: `agent/tools/glob.go`
- Create: `agent/tools/glob_test.go`

**Step 1: Write the failing test**

```go
// agent/tools/glob_test.go
// ABOUTME: Tests for the Glob tool.
// ABOUTME: Validates pattern matching, empty results, and parameter parsing.
package tools

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/2389-research/mammoth-lite/agent/exec"
)

func TestGlobToolExecute(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "a.go"), []byte(""), 0644)
	os.WriteFile(filepath.Join(dir, "b.go"), []byte(""), 0644)
	os.WriteFile(filepath.Join(dir, "c.txt"), []byte(""), 0644)

	env := exec.NewLocalEnvironment(dir)
	tool := NewGlobTool(env)

	input := json.RawMessage(`{"pattern": "*.go"}`)
	result, err := tool.Execute(context.Background(), input)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(result, "a.go") || !strings.Contains(result, "b.go") {
		t.Errorf("expected both .go files, got %q", result)
	}
	if strings.Contains(result, "c.txt") {
		t.Errorf("should not contain c.txt, got %q", result)
	}
}

func TestGlobToolNoMatches(t *testing.T) {
	env := exec.NewLocalEnvironment(t.TempDir())
	tool := NewGlobTool(env)

	input := json.RawMessage(`{"pattern": "*.xyz"}`)
	result, err := tool.Execute(context.Background(), input)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(result, "no matches") {
		t.Errorf("expected 'no matches' message, got %q", result)
	}
}

func TestGlobToolEmptyPattern(t *testing.T) {
	env := exec.NewLocalEnvironment(t.TempDir())
	tool := NewGlobTool(env)

	input := json.RawMessage(`{"pattern": ""}`)
	_, err := tool.Execute(context.Background(), input)
	if err == nil {
		t.Error("expected error for empty pattern")
	}
}
```

**Step 2: Run test to verify it fails**

Run: `go test ./agent/tools/... -run TestGlob -v`
Expected: FAIL

**Step 3: Write minimal implementation**

```go
// agent/tools/glob.go
// ABOUTME: Glob tool searches for files matching a pattern in the working directory.
// ABOUTME: Returns matching file paths separated by newlines, or a "no matches" message.
package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/2389-research/mammoth-lite/agent/exec"
)

// GlobTool searches for files by pattern.
type GlobTool struct {
	env exec.ExecutionEnvironment
}

// NewGlobTool creates a GlobTool backed by the given execution environment.
func NewGlobTool(env exec.ExecutionEnvironment) *GlobTool {
	return &GlobTool{env: env}
}

func (t *GlobTool) Name() string { return "glob" }

func (t *GlobTool) Description() string {
	return "Search for files matching a glob pattern relative to the working directory."
}

func (t *GlobTool) Parameters() json.RawMessage {
	return json.RawMessage(`{
		"type": "object",
		"properties": {
			"pattern": {
				"type": "string",
				"description": "The glob pattern to match (e.g. '*.go', 'src/**/*.ts')."
			}
		},
		"required": ["pattern"]
	}`)
}

func (t *GlobTool) Execute(ctx context.Context, input json.RawMessage) (string, error) {
	var params struct {
		Pattern string `json:"pattern"`
	}
	if err := json.Unmarshal(input, &params); err != nil {
		return "", fmt.Errorf("invalid input: %w", err)
	}
	if params.Pattern == "" {
		return "", fmt.Errorf("pattern is required")
	}

	matches, err := t.env.Glob(ctx, params.Pattern)
	if err != nil {
		return "", err
	}

	if len(matches) == 0 {
		return fmt.Sprintf("no matches for pattern %q", params.Pattern), nil
	}

	return strings.Join(matches, "\n"), nil
}
```

**Step 4: Run test to verify it passes**

Run: `go test ./agent/tools/... -run TestGlob -v`
Expected: PASS

**Step 5: Commit**

```bash
git add agent/tools/glob.go agent/tools/glob_test.go
git commit -m "feat(agent): add glob file search tool"
```

---

### Task 11: Session (Agentic Loop)

The core session that ties everything together: conversation state, LLM calls, tool dispatch, loop detection, and event emission.

**Files:**
- Create: `agent/session.go`
- Create: `agent/session_test.go`

**Step 1: Write the failing test**

```go
// agent/session_test.go
// ABOUTME: Tests for the agent Session and agentic loop.
// ABOUTME: Uses mock LLM client to validate turn execution, tool dispatch, loop detection, and event emission.
package agent

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/2389-research/mammoth-lite/llm"
)

// mockCompleter is a mock llm.Client for testing the agentic loop.
type mockCompleter struct {
	responses []*llm.Response
	calls     int
}

func (m *mockCompleter) Complete(ctx context.Context, req *llm.Request) (*llm.Response, error) {
	if m.calls >= len(m.responses) {
		return &llm.Response{
			Message: llm.AssistantMessage("done"),
			FinishReason: llm.FinishReason{Reason: "stop"},
		}, nil
	}
	resp := m.responses[m.calls]
	m.calls++
	return resp, nil
}

func TestSessionTextOnlyResponse(t *testing.T) {
	client := &mockCompleter{
		responses: []*llm.Response{
			{
				Message:      llm.AssistantMessage("Hello, I can help!"),
				FinishReason: llm.FinishReason{Reason: "stop"},
				Usage:        llm.Usage{InputTokens: 10, OutputTokens: 5, TotalTokens: 15},
			},
		},
	}

	cfg := DefaultConfig()
	cfg.SystemPrompt = "You are a helpful assistant."

	sess := NewSession(client, cfg)
	result, err := sess.Run(context.Background(), "Say hello")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Turns != 1 {
		t.Errorf("expected 1 turn, got %d", result.Turns)
	}
	if result.TotalToolCalls() != 0 {
		t.Errorf("expected 0 tool calls, got %d", result.TotalToolCalls())
	}
}

func TestSessionToolCallLoop(t *testing.T) {
	toolCallResp := &llm.Response{
		Message: llm.Message{
			Role: llm.RoleAssistant,
			Content: []llm.ContentPart{
				{
					Kind: llm.KindToolCall,
					ToolCall: &llm.ToolCallData{
						ID:        "call_1",
						Name:      "read",
						Arguments: json.RawMessage(`{"path": "test.txt"}`),
					},
				},
			},
		},
		FinishReason: llm.FinishReason{Reason: "tool_calls"},
		Usage:        llm.Usage{InputTokens: 20, OutputTokens: 10, TotalTokens: 30},
	}

	textResp := &llm.Response{
		Message:      llm.AssistantMessage("I read the file."),
		FinishReason: llm.FinishReason{Reason: "stop"},
		Usage:        llm.Usage{InputTokens: 30, OutputTokens: 8, TotalTokens: 38},
	}

	client := &mockCompleter{
		responses: []*llm.Response{toolCallResp, textResp},
	}

	cfg := DefaultConfig()
	sess := NewSession(client, cfg)

	result, err := sess.Run(context.Background(), "Read test.txt")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Turns != 2 {
		t.Errorf("expected 2 turns, got %d", result.Turns)
	}
	if result.ToolCalls["read"] != 1 {
		t.Errorf("expected 1 read call, got %d", result.ToolCalls["read"])
	}
}

func TestSessionMaxTurns(t *testing.T) {
	// Always return a tool call to force looping.
	toolCallResp := &llm.Response{
		Message: llm.Message{
			Role: llm.RoleAssistant,
			Content: []llm.ContentPart{
				{
					Kind: llm.KindToolCall,
					ToolCall: &llm.ToolCallData{
						ID:        "call_1",
						Name:      "read",
						Arguments: json.RawMessage(`{"path": "test.txt"}`),
					},
				},
			},
		},
		FinishReason: llm.FinishReason{Reason: "tool_calls"},
	}

	responses := make([]*llm.Response, 100)
	for i := range responses {
		responses[i] = toolCallResp
	}

	client := &mockCompleter{responses: responses}

	cfg := DefaultConfig()
	cfg.MaxTurns = 3
	sess := NewSession(client, cfg)

	result, err := sess.Run(context.Background(), "Loop forever")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Turns != 3 {
		t.Errorf("expected 3 turns (max), got %d", result.Turns)
	}
}

func TestSessionEventEmission(t *testing.T) {
	client := &mockCompleter{
		responses: []*llm.Response{
			{
				Message:      llm.AssistantMessage("Hi"),
				FinishReason: llm.FinishReason{Reason: "stop"},
			},
		},
	}

	var events []Event
	handler := EventHandlerFunc(func(evt Event) {
		events = append(events, evt)
	})

	cfg := DefaultConfig()
	sess := NewSession(client, cfg, WithEventHandler(handler))
	_, err := sess.Run(context.Background(), "Hello")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Should have at least: SessionStart, TurnStart, TurnEnd, SessionEnd.
	typeSet := make(map[EventType]bool)
	for _, e := range events {
		typeSet[e.Type] = true
	}
	for _, expected := range []EventType{EventSessionStart, EventTurnStart, EventTurnEnd, EventSessionEnd} {
		if !typeSet[expected] {
			t.Errorf("missing event type: %s", expected)
		}
	}
}

func TestSessionContextCancellation(t *testing.T) {
	client := &mockCompleter{
		responses: []*llm.Response{
			{
				Message:      llm.AssistantMessage("will not reach"),
				FinishReason: llm.FinishReason{Reason: "stop"},
			},
		},
	}

	cfg := DefaultConfig()
	sess := NewSession(client, cfg)

	ctx, cancel := context.WithCancel(context.Background())
	cancel() // Cancel immediately.

	_, err := sess.Run(ctx, "Hello")
	if err == nil {
		t.Error("expected error from cancelled context")
	}
}
```

**Step 2: Run test to verify it fails**

Run: `go test ./agent/... -run TestSession -v`
Expected: FAIL

**Step 3: Write minimal implementation**

```go
// agent/session.go
// ABOUTME: Agent session that runs the agentic loop: LLM call -> tool execution -> loop.
// ABOUTME: Manages conversation state, tool dispatch, loop detection, event emission, and result collection.
package agent

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"time"

	"github.com/2389-research/mammoth-lite/agent/exec"
	"github.com/2389-research/mammoth-lite/agent/tools"
	"github.com/2389-research/mammoth-lite/llm"
)

// Completer is the interface needed from the LLM client.
// This is satisfied by *llm.Client and by test mocks.
type Completer interface {
	Complete(ctx context.Context, req *llm.Request) (*llm.Response, error)
}

// SessionOption configures a Session.
type SessionOption func(*Session)

// WithEventHandler sets the event handler for the session.
func WithEventHandler(h EventHandler) SessionOption {
	return func(s *Session) {
		s.handler = h
	}
}

// WithTools registers additional tools with the session.
func WithTools(tt ...tools.Tool) SessionOption {
	return func(s *Session) {
		for _, t := range tt {
			s.registry.Register(t)
		}
	}
}

// WithEnvironment sets the execution environment for built-in tools.
func WithEnvironment(env exec.ExecutionEnvironment) SessionOption {
	return func(s *Session) {
		s.env = env
	}
}

// Session holds conversation state and runs the agentic loop.
type Session struct {
	client   Completer
	config   SessionConfig
	handler  EventHandler
	registry *tools.Registry
	env      exec.ExecutionEnvironment
	messages []llm.Message
	id       string
}

// NewSession creates a new agent session.
func NewSession(client Completer, config SessionConfig, opts ...SessionOption) *Session {
	s := &Session{
		client:   client,
		config:   config,
		handler:  NoopHandler,
		registry: tools.NewRegistry(),
		id:       generateSessionID(),
	}

	for _, opt := range opts {
		opt(s)
	}

	// Register built-in tools if an environment is set.
	if s.env != nil {
		s.registry.Register(tools.NewReadTool(s.env))
		s.registry.Register(tools.NewWriteTool(s.env))
		s.registry.Register(tools.NewEditTool(s.env))
		s.registry.Register(tools.NewGlobTool(s.env))
		s.registry.Register(tools.NewBashTool(s.env, s.config.CommandTimeout, s.config.MaxCommandTimeout))
	}

	return s
}

// Run executes the agentic loop with the given user input.
func (s *Session) Run(ctx context.Context, userInput string) (SessionResult, error) {
	start := time.Now()

	result := SessionResult{
		SessionID: s.id,
		ToolCalls: make(map[string]int),
	}

	s.emit(Event{Type: EventSessionStart, SessionID: s.id})
	defer func() {
		result.Duration = time.Since(start)
		s.emit(Event{Type: EventSessionEnd, SessionID: s.id})
	}()

	// Initialize conversation with system prompt and user message.
	if s.config.SystemPrompt != "" {
		s.messages = append(s.messages, llm.SystemMessage(s.config.SystemPrompt))
	}
	s.messages = append(s.messages, llm.UserMessage(userInput))

	// Agentic loop.
	for turn := 1; turn <= s.config.MaxTurns; turn++ {
		if err := ctx.Err(); err != nil {
			result.Error = err
			return result, err
		}

		s.emit(Event{Type: EventTurnStart, SessionID: s.id, Turn: turn})

		// Build request.
		req := &llm.Request{
			Model:    s.config.Model,
			Provider: s.config.Provider,
			Messages: s.messages,
			Tools:    s.registry.Definitions(),
		}

		// Call LLM.
		resp, err := s.client.Complete(ctx, req)
		if err != nil {
			result.Error = err
			s.emit(Event{Type: EventError, SessionID: s.id, Err: err})
			return result, err
		}

		// Accumulate usage.
		result.Usage = result.Usage.Add(resp.Usage)
		result.Turns = turn

		// Append assistant message to conversation.
		s.messages = append(s.messages, resp.Message)

		// Check for tool calls.
		toolCalls := resp.ToolCalls()
		if len(toolCalls) == 0 {
			// Text-only response — natural completion.
			text := resp.Text()
			if text != "" {
				s.emit(Event{Type: EventTextDelta, SessionID: s.id, Text: text})
			}
			s.emit(Event{Type: EventTurnEnd, SessionID: s.id, Turn: turn})
			break
		}

		// Execute tool calls.
		var toolResults []llm.ContentPart
		for _, call := range toolCalls {
			s.emit(Event{
				Type:      EventToolCallStart,
				SessionID: s.id,
				ToolName:  call.Name,
				ToolInput: string(call.Arguments),
			})

			toolResult := s.registry.Execute(ctx, call)
			result.ToolCalls[call.Name]++

			s.emit(Event{
				Type:       EventToolCallEnd,
				SessionID:  s.id,
				ToolName:   call.Name,
				ToolOutput: toolResult.Content,
				ToolError:  boolToErrStr(toolResult.IsError),
			})

			toolResults = append(toolResults, llm.ContentPart{
				Kind:       llm.KindToolResult,
				ToolResult: &toolResult,
			})
		}

		// Append tool results as a tool message.
		s.messages = append(s.messages, llm.Message{
			Role:    llm.RoleTool,
			Content: toolResults,
		})

		s.emit(Event{Type: EventTurnEnd, SessionID: s.id, Turn: turn})
	}

	return result, nil
}

func (s *Session) emit(evt Event) {
	evt.Timestamp = time.Now()
	s.handler.HandleEvent(evt)
}

func boolToErrStr(isErr bool) string {
	if isErr {
		return "true"
	}
	return ""
}

func generateSessionID() string {
	b := make([]byte, 4)
	if _, err := rand.Read(b); err != nil {
		return "0000"
	}
	return hex.EncodeToString(b)
}
```

**Step 4: Run test to verify it passes**

Run: `go test ./agent/... -v`
Expected: PASS (all session + event + config + result tests)

**Step 5: Commit**

```bash
git add agent/session.go agent/session_test.go
git commit -m "feat(agent): add session with agentic loop, tool dispatch, and events"
```

---

### Task 12: Integration Test (Session + Real Tools)

End-to-end test using a mock LLM client but real file tools to verify the full pipeline works.

**Files:**
- Create: `agent/integration_test.go`

**Step 1: Write the test**

```go
// agent/integration_test.go
// ABOUTME: Integration tests for the agent session with real file tools.
// ABOUTME: Uses mock LLM client but real filesystem to validate end-to-end tool dispatch.
package agent

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/2389-research/mammoth-lite/agent/exec"
	"github.com/2389-research/mammoth-lite/llm"
)

func TestIntegrationReadWriteFlow(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "input.txt"), []byte("hello"), 0644)

	// Simulate: model reads a file, then writes a new file.
	readCallResp := &llm.Response{
		Message: llm.Message{
			Role: llm.RoleAssistant,
			Content: []llm.ContentPart{{
				Kind: llm.KindToolCall,
				ToolCall: &llm.ToolCallData{
					ID:        "call_1",
					Name:      "read",
					Arguments: json.RawMessage(`{"path":"input.txt"}`),
				},
			}},
		},
		FinishReason: llm.FinishReason{Reason: "tool_calls"},
	}

	writeCallResp := &llm.Response{
		Message: llm.Message{
			Role: llm.RoleAssistant,
			Content: []llm.ContentPart{{
				Kind: llm.KindToolCall,
				ToolCall: &llm.ToolCallData{
					ID:        "call_2",
					Name:      "write",
					Arguments: json.RawMessage(`{"path":"output.txt","content":"hello world"}`),
				},
			}},
		},
		FinishReason: llm.FinishReason{Reason: "tool_calls"},
	}

	doneResp := &llm.Response{
		Message:      llm.AssistantMessage("Done! I read input.txt and wrote output.txt."),
		FinishReason: llm.FinishReason{Reason: "stop"},
	}

	client := &mockCompleter{
		responses: []*llm.Response{readCallResp, writeCallResp, doneResp},
	}

	env := exec.NewLocalEnvironment(dir)
	cfg := DefaultConfig()
	sess := NewSession(client, cfg, WithEnvironment(env))

	result, err := sess.Run(context.Background(), "Read input.txt and copy to output.txt")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if result.Turns != 3 {
		t.Errorf("expected 3 turns, got %d", result.Turns)
	}
	if result.ToolCalls["read"] != 1 {
		t.Errorf("expected 1 read, got %d", result.ToolCalls["read"])
	}
	if result.ToolCalls["write"] != 1 {
		t.Errorf("expected 1 write, got %d", result.ToolCalls["write"])
	}

	// Verify the file was actually written.
	data, err := os.ReadFile(filepath.Join(dir, "output.txt"))
	if err != nil {
		t.Fatalf("output.txt not created: %v", err)
	}
	if string(data) != "hello world" {
		t.Errorf("expected 'hello world', got %q", string(data))
	}
}

func TestIntegrationEditFlow(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "code.go"), []byte("func main() {\n\tfmt.Println(\"old\")\n}"), 0644)

	editCallResp := &llm.Response{
		Message: llm.Message{
			Role: llm.RoleAssistant,
			Content: []llm.ContentPart{{
				Kind: llm.KindToolCall,
				ToolCall: &llm.ToolCallData{
					ID:   "call_1",
					Name: "edit",
					Arguments: json.RawMessage(`{
						"path": "code.go",
						"old_string": "fmt.Println(\"old\")",
						"new_string": "fmt.Println(\"new\")"
					}`),
				},
			}},
		},
		FinishReason: llm.FinishReason{Reason: "tool_calls"},
	}

	doneResp := &llm.Response{
		Message:      llm.AssistantMessage("Updated the print statement."),
		FinishReason: llm.FinishReason{Reason: "stop"},
	}

	client := &mockCompleter{responses: []*llm.Response{editCallResp, doneResp}}
	env := exec.NewLocalEnvironment(dir)
	cfg := DefaultConfig()
	sess := NewSession(client, cfg, WithEnvironment(env))

	result, err := sess.Run(context.Background(), "Update the print statement")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if result.ToolCalls["edit"] != 1 {
		t.Errorf("expected 1 edit, got %d", result.ToolCalls["edit"])
	}

	data, _ := os.ReadFile(filepath.Join(dir, "code.go"))
	expected := "func main() {\n\tfmt.Println(\"new\")\n}"
	if string(data) != expected {
		t.Errorf("expected %q, got %q", expected, string(data))
	}
}

func TestIntegrationBashFlow(t *testing.T) {
	dir := t.TempDir()

	bashCallResp := &llm.Response{
		Message: llm.Message{
			Role: llm.RoleAssistant,
			Content: []llm.ContentPart{{
				Kind: llm.KindToolCall,
				ToolCall: &llm.ToolCallData{
					ID:        "call_1",
					Name:      "bash",
					Arguments: json.RawMessage(`{"command":"echo integration-test"}`),
				},
			}},
		},
		FinishReason: llm.FinishReason{Reason: "tool_calls"},
	}

	doneResp := &llm.Response{
		Message:      llm.AssistantMessage("Command executed."),
		FinishReason: llm.FinishReason{Reason: "stop"},
	}

	client := &mockCompleter{responses: []*llm.Response{bashCallResp, doneResp}}
	env := exec.NewLocalEnvironment(dir)
	cfg := DefaultConfig()
	sess := NewSession(client, cfg, WithEnvironment(env))

	result, err := sess.Run(context.Background(), "Run echo")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if result.ToolCalls["bash"] != 1 {
		t.Errorf("expected 1 bash call, got %d", result.ToolCalls["bash"])
	}
}
```

**Step 2: Run test to verify it passes**

Run: `go test ./agent/... -v`
Expected: PASS (all tests including integration)

**Step 3: Commit**

```bash
git add agent/integration_test.go
git commit -m "test(agent): add integration tests for session with real file tools"
```

---

## Summary

| Task | Component | Files | Dependencies |
|------|-----------|-------|--------------|
| 1 | Event types | events.go, events_test.go | none |
| 2 | Config | config.go, config_test.go | none |
| 3 | Exec environment | exec/env.go, exec/local.go, exec/env_test.go | none |
| 4 | Result | result.go, result_test.go | llm (Usage) |
| 5 | Tool registry | tools/registry.go, tools/registry_test.go | llm (ToolDefinition, ToolCallData) |
| 6 | Read tool | tools/read.go, tools/read_test.go | exec |
| 7 | Write tool | tools/write.go, tools/write_test.go | exec |
| 8 | Edit tool | tools/edit.go, tools/edit_test.go | exec |
| 9 | Bash tool | tools/bash.go, tools/bash_test.go | exec |
| 10 | Glob tool | tools/glob.go, tools/glob_test.go | exec |
| 11 | Session | session.go, session_test.go | all above + llm |
| 12 | Integration tests | integration_test.go | all above |
