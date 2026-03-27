# Claude Code Backend Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Add an AgentBackend abstraction to tracker's pipeline engine, with a Claude Code backend that delegates agent nodes to the Claude Code CLI.

**Architecture:** One handler (`codergen`), multiple backends. The `AgentBackend` interface emits `agent.Event` directly and returns `agent.SessionResult`. The Claude Code backend is a ~400 line internal package that spawns `claude` as a subprocess and parses NDJSON. No external SDK dependency.

**Tech Stack:** Go, exec.Command, json.Decoder, existing agent/pipeline packages

**Spec:** `docs/superpowers/specs/2026-03-26-claude-code-backend-design.md`

---

## File Map

| File | Action | Responsibility |
|------|--------|---------------|
| `pipeline/backend.go` | Create | AgentBackend interface, AgentRunConfig, ClaudeCodeConfig, MCPServerConfig, PermissionMode |
| `pipeline/backend_test.go` | Create | Config validation tests, MCP parsing tests |
| `pipeline/handlers/prompt.go` | Create | Shared ResolvePrompt extracted from codergen |
| `pipeline/handlers/prompt_test.go` | Create | ResolvePrompt parity tests |
| `pipeline/handlers/backend_native.go` | Create | NativeBackend wrapping agent.Session |
| `pipeline/handlers/backend_native_test.go` | Create | NativeBackend unit tests |
| `pipeline/handlers/backend_claudecode.go` | Create | ClaudeCodeBackend: subprocess, NDJSON, events |
| `pipeline/handlers/backend_claudecode_test.go` | Create | NDJSON parsing, error classification, config building |
| `pipeline/handlers/codergen.go` | Modify | Refactor Execute() to use AgentBackend |
| `pipeline/handlers/registry.go` | Modify | Add WithBackend option, inject backends |
| `pipeline/dippin_adapter.go` | Modify | Extract backend attr |
| `cmd/tracker/main.go` | Modify | Add --backend flag |

---

### Task 1: Create AgentBackend interface and config types

**Files:**
- Create: `pipeline/backend.go`
- Create: `pipeline/backend_test.go`

- [ ] **Step 1: Write tests for config types**

```go
// pipeline/backend_test.go
package pipeline

import "testing"

func TestPermissionModeValidation(t *testing.T) {
	valid := []PermissionMode{PermissionPlan, PermissionAutoEdit, PermissionFullAuto}
	for _, m := range valid {
		if !m.Valid() {
			t.Errorf("expected %q to be valid", m)
		}
	}
	if PermissionMode("bogus").Valid() {
		t.Error("expected 'bogus' to be invalid")
	}
}

func TestParseMCPServers(t *testing.T) {
	tests := []struct {
		name    string
		input   string
		want    int
		wantErr bool
	}{
		{"single", "pg=npx server", 1, false},
		{"multi", "pg=npx server\ngh=npx github", 2, false},
		{"empty lines", "\n  pg=npx server  \n\n", 1, false},
		{"no equals", "broken", 0, true},
		{"empty name", "=command", 0, true},
		{"empty command", "name=", 0, true},
		{"equals in command", "pg=npx server --conn=host=localhost", 1, false},
		{"duplicate names", "pg=a\npg=b", 0, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			servers, err := ParseMCPServers(tt.input)
			if tt.wantErr && err == nil {
				t.Fatal("expected error")
			}
			if !tt.wantErr && err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if len(servers) != tt.want {
				t.Errorf("got %d servers, want %d", len(servers), tt.want)
			}
		})
	}
}

func TestParseToolList(t *testing.T) {
	tools := ParseToolList("Read,Write,Bash")
	if len(tools) != 3 || tools[0] != "Read" {
		t.Errorf("got %v", tools)
	}
	if len(ParseToolList("")) != 0 {
		t.Error("empty string should return empty list")
	}
}

func TestValidateToolLists(t *testing.T) {
	if err := ValidateToolLists([]string{"Read"}, []string{"Bash"}); err == nil {
		t.Error("expected error when both allowed and disallowed are set")
	}
	if err := ValidateToolLists([]string{"Read"}, nil); err != nil {
		t.Errorf("unexpected error: %v", err)
	}
	if err := ValidateToolLists(nil, nil); err != nil {
		t.Errorf("unexpected error: %v", err)
	}
}
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `go test ./pipeline/ -run 'TestPermission|TestParseMCP|TestParseToolList|TestValidateToolLists' -v`
Expected: FAIL (types don't exist yet)

- [ ] **Step 3: Implement backend.go**

```go
// pipeline/backend.go
// ABOUTME: AgentBackend interface and config types for pluggable execution backends.
// ABOUTME: Supports native (agent.Session) and Claude Code (CLI subprocess) backends.
package pipeline

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/2389-research/tracker/agent"
)

// AgentBackend executes an agent session and streams events.
type AgentBackend interface {
	Run(ctx context.Context, cfg AgentRunConfig, emit func(agent.Event)) (agent.SessionResult, error)
}

// AgentRunConfig carries common config all backends need.
type AgentRunConfig struct {
	Prompt       string
	SystemPrompt string
	Model        string
	Provider     string
	WorkingDir   string
	MaxTurns     int
	Timeout      time.Duration
	Extra        any // backend-specific: *ClaudeCodeConfig for claude-code backend
}

// ClaudeCodeConfig holds Claude-Code-specific settings.
type ClaudeCodeConfig struct {
	MCPServers      []MCPServerConfig
	AllowedTools    []string
	DisallowedTools []string
	MaxBudgetUSD    float64
	PermissionMode  PermissionMode
}

// PermissionMode controls Claude Code's tool approval behavior.
type PermissionMode string

const (
	PermissionPlan     PermissionMode = "plan"
	PermissionAutoEdit PermissionMode = "autoEdit"
	PermissionFullAuto PermissionMode = "fullAuto"
)

// Valid returns true if the permission mode is a recognized value.
func (m PermissionMode) Valid() bool {
	switch m {
	case PermissionPlan, PermissionAutoEdit, PermissionFullAuto, "":
		return true
	}
	return false
}

// MCPServerConfig defines an MCP server to attach to a session.
type MCPServerConfig struct {
	Name    string
	Command string
	Args    []string
}

// ParseMCPServers parses the mcp_servers attr format: one server per line,
// name=command arg1 arg2. Splits on first = only.
func ParseMCPServers(raw string) ([]MCPServerConfig, error) {
	var servers []MCPServerConfig
	seen := make(map[string]bool)
	for _, line := range strings.Split(raw, "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		idx := strings.Index(line, "=")
		if idx < 0 {
			return nil, fmt.Errorf("malformed mcp_servers entry: %q (missing '=')", line)
		}
		name := strings.TrimSpace(line[:idx])
		cmdStr := strings.TrimSpace(line[idx+1:])
		if name == "" {
			return nil, fmt.Errorf("malformed mcp_servers entry: %q (empty name)", line)
		}
		if cmdStr == "" {
			return nil, fmt.Errorf("malformed mcp_servers entry: %q (empty command)", line)
		}
		if seen[name] {
			return nil, fmt.Errorf("duplicate mcp_servers name: %q", name)
		}
		seen[name] = true
		parts := strings.Fields(cmdStr)
		servers = append(servers, MCPServerConfig{
			Name:    name,
			Command: parts[0],
			Args:    parts[1:],
		})
	}
	return servers, nil
}

// ParseToolList splits a comma-separated tool list, trimming whitespace.
func ParseToolList(csv string) []string {
	if strings.TrimSpace(csv) == "" {
		return nil
	}
	var tools []string
	for _, t := range strings.Split(csv, ",") {
		t = strings.TrimSpace(t)
		if t != "" {
			tools = append(tools, t)
		}
	}
	return tools
}

// ValidateToolLists returns an error if both allowed and disallowed are set.
func ValidateToolLists(allowed, disallowed []string) error {
	if len(allowed) > 0 && len(disallowed) > 0 {
		return fmt.Errorf("cannot set both allowed_tools and disallowed_tools")
	}
	return nil
}
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `go test ./pipeline/ -run 'TestPermission|TestParseMCP|TestParseToolList|TestValidateToolLists' -v`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add pipeline/backend.go pipeline/backend_test.go
git commit -m "feat(pipeline): add AgentBackend interface and config types"
```

---

### Task 2: Extract shared ResolvePrompt from codergen

**Files:**
- Create: `pipeline/handlers/prompt.go`
- Create: `pipeline/handlers/prompt_test.go`
- Modify: `pipeline/handlers/codergen.go`

- [ ] **Step 1: Write parity test for ResolvePrompt**

Read the current `resolvePrompt` method in `codergen.go` (private). Write a test that exercises the same paths: variable expansion, fidelity-based compaction, context injection.

```go
// pipeline/handlers/prompt_test.go
package handlers

import (
	"testing"

	"github.com/2389-research/tracker/pipeline"
)

func TestResolvePromptExpandsGraphVariables(t *testing.T) {
	node := &pipeline.Node{
		ID:    "test",
		Attrs: map[string]string{"prompt": "Build $target_name"},
	}
	pctx := pipeline.NewPipelineContext()
	pctx.Set("graph.target_name", "myapp")
	graphAttrs := map[string]string{"target_name": "myapp"}

	prompt, err := ResolvePrompt(node, pctx, graphAttrs, "")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if prompt != "Build myapp" {
		t.Errorf("got %q, want %q", prompt, "Build myapp")
	}
}

func TestResolvePromptInjectsPipelineContext(t *testing.T) {
	node := &pipeline.Node{
		ID:    "test",
		Attrs: map[string]string{"prompt": "Continue work."},
	}
	pctx := pipeline.NewPipelineContext()
	pctx.Set("last_response", "Previous node output here")

	prompt, err := ResolvePrompt(node, pctx, nil, "")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !contains(prompt, "Previous node output here") {
		t.Errorf("expected injected context, got %q", prompt)
	}
}

func TestResolvePromptMissingPromptAttr(t *testing.T) {
	node := &pipeline.Node{ID: "test", Attrs: map[string]string{}}
	pctx := pipeline.NewPipelineContext()
	_, err := ResolvePrompt(node, pctx, nil, "")
	if err == nil {
		t.Fatal("expected error for missing prompt")
	}
}

func contains(s, sub string) bool {
	return len(s) >= len(sub) && (s == sub || len(s) > 0 && containsStr(s, sub))
}
func containsStr(s, sub string) bool {
	for i := 0; i <= len(s)-len(sub); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `go test ./pipeline/handlers/ -run TestResolvePrompt -v`
Expected: FAIL

- [ ] **Step 3: Extract ResolvePrompt to prompt.go**

Read codergen.go's `resolvePrompt` method. Copy the logic into a new public function in `prompt.go`. Update codergen.go to call the new public function instead of the private method.

- [ ] **Step 4: Run ALL pipeline/handlers tests to verify no regression**

Run: `go test ./pipeline/handlers/ -v`
Expected: ALL PASS (existing codergen tests still pass + new prompt tests pass)

- [ ] **Step 5: Commit**

```bash
git add pipeline/handlers/prompt.go pipeline/handlers/prompt_test.go pipeline/handlers/codergen.go
git commit -m "refactor(handlers): extract ResolvePrompt as shared utility"
```

---

### Task 3: Implement NativeBackend

**Files:**
- Create: `pipeline/handlers/backend_native.go`
- Create: `pipeline/handlers/backend_native_test.go`

- [ ] **Step 1: Write test for NativeBackend**

Test that NativeBackend.Run() emits events and returns a SessionResult. Use the existing `fakeCompleter` pattern from `codergen_test.go`.

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./pipeline/handlers/ -run TestNativeBackend -v`

- [ ] **Step 3: Implement NativeBackend**

Extract the session creation and run logic from codergen's `Execute()` into `NativeBackend.Run()`. The native backend:
1. Builds `agent.SessionConfig` from `AgentRunConfig`
2. Creates `agent.Session`
3. Wires the `emit` callback as an `agent.EventHandler`
4. Calls `session.Run(ctx)`
5. Returns `session.Result()`

- [ ] **Step 4: Run tests**

Run: `go test ./pipeline/handlers/ -v`
Expected: ALL PASS

- [ ] **Step 5: Commit**

```bash
git add pipeline/handlers/backend_native.go pipeline/handlers/backend_native_test.go
git commit -m "feat(handlers): add NativeBackend wrapping agent.Session"
```

---

### Task 4: Refactor CodergenHandler to use AgentBackend

**Files:**
- Modify: `pipeline/handlers/codergen.go`
- Modify: `pipeline/handlers/registry.go`
- Modify: `pipeline/handlers/codergen_test.go` (if needed)

- [ ] **Step 1: Add backend fields to CodergenHandler**

Add `nativeBackend`, `claudeCodeBackend`, and `defaultBackend` fields. Add a registry option `WithDefaultBackend(name string)`.

- [ ] **Step 2: Refactor Execute() to select and call backend**

Replace the session creation + run block with:
1. Build `AgentRunConfig` from node attrs
2. Select backend (node attr > flag > default)
3. Call `backend.Run(ctx, config, emitFunc)`
4. Build `Outcome` from `SessionResult`

Keep: prompt resolution, artifact writing, auto-status parsing, outcome building.

- [ ] **Step 3: Run ALL existing tests**

Run: `go test ./pipeline/handlers/ -v && go test ./pipeline/ -v`
Expected: ALL PASS (pure refactoring, no behavior change)

- [ ] **Step 4: Commit**

```bash
git add pipeline/handlers/codergen.go pipeline/handlers/registry.go
git commit -m "refactor(handlers): codergen delegates to AgentBackend"
```

---

### Task 5: Implement ClaudeCodeBackend

**Files:**
- Create: `pipeline/handlers/backend_claudecode.go`
- Create: `pipeline/handlers/backend_claudecode_test.go`

- [ ] **Step 1: Write NDJSON parsing tests**

```go
func TestParseNDJSONMessages(t *testing.T) {
	tests := []struct {
		name     string
		json     string
		wantType agent.EventType
	}{
		{"text", `{"type":"assistant","content":[{"type":"text","text":"hello"}]}`, agent.EventTextDelta},
		{"tool_use", `{"type":"assistant","content":[{"type":"tool_use","name":"bash","input":"{\"command\":\"ls\"}"}]}`, agent.EventToolCallStart},
		{"tool_result", `{"type":"user","content":[{"type":"tool_result","tool_use_id":"123","content":"output"}]}`, agent.EventToolCallEnd},
		{"result", `{"type":"result","turns":5}`, agent.EventSessionEnd},
		{"unknown", `{"type":"future_type"}`, ""},
	}
	// ... test each one produces the right agent.Event
}
```

- [ ] **Step 2: Write error classification tests**

```go
func TestClassifyError(t *testing.T) {
	tests := []struct {
		name     string
		stderr   string
		exitCode int
		want     string // OutcomeRetry, OutcomeFail, etc
	}{
		{"rate limit", "Error: rate limit exceeded", 1, pipeline.OutcomeRetry},
		{"auth", "Error: authentication failed", 1, pipeline.OutcomeFail},
		{"budget", "Error: budget exceeded", 1, pipeline.OutcomeFail},
		{"network", "Error: ECONNREFUSED", 1, pipeline.OutcomeRetry},
		{"oom", "", 137, pipeline.OutcomeFail},
		{"success", "", 0, pipeline.OutcomeSuccess},
		{"unknown", "something else", 42, pipeline.OutcomeFail},
		{"rate in context", "Error: first-rate failure", 1, pipeline.OutcomeFail}, // "rate" alone shouldn't match
	}
	// ... test each one
}
```

- [ ] **Step 3: Write CLI arg construction tests**

Test that `AgentRunConfig` + `ClaudeCodeConfig` produce the correct `claude` CLI arguments.

- [ ] **Step 4: Run tests to verify they fail**

Run: `go test ./pipeline/handlers/ -run 'TestParseNDJSON|TestClassifyError' -v`

- [ ] **Step 5: Implement ClaudeCodeBackend**

The backend (~400 lines):
1. `buildArgs(cfg)` — construct CLI arguments from config
2. `Run(ctx, cfg, emit)` — spawn subprocess, stream NDJSON, emit events
3. `parseMessage(raw)` — switch on NDJSON type, construct agent.Event
4. `classifyError(stderr, exitCode)` — priority-ordered pattern matching
5. `resolveClaudePath()` — exec.LookPath + version check
6. Process group management (Setpgid, SIGTERM→SIGKILL)
7. Goroutine contract (WaitGroup, recover in emit, stderr Buffer)

- [ ] **Step 6: Run tests**

Run: `go test ./pipeline/handlers/ -v`
Expected: ALL PASS

- [ ] **Step 7: Commit**

```bash
git add pipeline/handlers/backend_claudecode.go pipeline/handlers/backend_claudecode_test.go
git commit -m "feat(handlers): add ClaudeCodeBackend (internal, no SDK dep)"
```

---

### Task 6: Add --backend CLI flag

**Files:**
- Modify: `cmd/tracker/main.go`

- [ ] **Step 1: Add backend field to runConfig and parseFlags**

Add `backend string` field. Add `fs.StringVar`. Add validation (only `""`, `"native"`, `"claude-code"` are valid). Update `printUsage` to show the flag.

- [ ] **Step 2: Thread backend through to registry construction**

Pass `cfg.backend` to registry options. Add `WithDefaultBackend` option to `NewDefaultRegistry`.

- [ ] **Step 3: Run all tests**

Run: `go test ./cmd/tracker/ -v && go test ./pipeline/... -v`
Expected: ALL PASS

- [ ] **Step 4: Commit**

```bash
git add cmd/tracker/main.go pipeline/handlers/registry.go
git commit -m "feat(cli): add --backend flag for execution backend selection"
```

---

### Task 7: Add backend attr support in dippin adapter

**Files:**
- Modify: `pipeline/dippin_adapter.go`
- Modify: `pipeline/dippin_adapter_test.go`

- [ ] **Step 1: Write test for backend attr extraction**

Test that when a node has `backend: "claude-code"` in its config, the adapter sets `attrs["backend"]` correctly. Also test the new attrs: `mcp_servers`, `allowed_tools`, `disallowed_tools`, `max_budget_usd`, `permission_mode`.

NOTE: This task is partially blocked by the dippin-lang prerequisite. For now, test that attrs from the IR's existing `Params` mechanism (or manual attr injection) flow through correctly. The dippin-lang IR change is tracked separately.

- [ ] **Step 2: Implement attr extraction**

In `extractAgentAttrs`, add passthrough for the new attrs when present in the IR config.

- [ ] **Step 3: Run tests**

Run: `go test ./pipeline/ -v`

- [ ] **Step 4: Commit**

```bash
git add pipeline/dippin_adapter.go pipeline/dippin_adapter_test.go
git commit -m "feat(pipeline): extract backend and claude-code attrs from dippin IR"
```

---

### Task 8: Integration test and rebuild

**Files:**
- Create: `pipeline/handlers/backend_claudecode_integration_test.go`

- [ ] **Step 1: Write integration test (build-tagged)**

```go
//go:build integration

func TestClaudeCodeBackendIntegration(t *testing.T) {
	// Skip if claude CLI not found
	// Run a simple prompt
	// Verify events were emitted
	// Verify SessionResult has turns > 0
}
```

- [ ] **Step 2: Run full test suite**

Run: `go test ./...`
Expected: ALL PASS

- [ ] **Step 3: Rebuild and install**

Run: `make install`

- [ ] **Step 4: Manual smoke test**

Run: `tracker --backend claude-code --no-tui examples/hello.dip` (or similar simple pipeline)

- [ ] **Step 5: Commit any fixes**

- [ ] **Step 6: Final commit**

```bash
git commit -m "feat: claude-code backend complete — integration tested"
```

---

## Task Dependency Graph

```
Task 1 (interface + config types)
    ↓
Task 2 (extract ResolvePrompt) ──→ Task 4 (refactor codergen)
    ↓                                    ↓
Task 3 (NativeBackend) ─────────→ Task 4
                                         ↓
Task 5 (ClaudeCodeBackend) ─────→ Task 6 (CLI flag)
                                         ↓
                                  Task 7 (dippin adapter)
                                         ↓
                                  Task 8 (integration + rebuild)
```

Tasks 1, 2, 3 can proceed in parallel. Task 4 depends on 1+2+3. Task 5 depends on 1. Tasks 6+7 depend on 4+5. Task 8 is the final integration.
