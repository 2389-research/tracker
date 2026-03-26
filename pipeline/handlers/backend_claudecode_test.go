// ABOUTME: Tests for ClaudeCodeBackend which spawns the claude CLI as a subprocess.
// ABOUTME: Covers arg building, env construction, NDJSON parsing, error classification, and MCP config.
package handlers

import (
	"encoding/json"
	"testing"

	"github.com/2389-research/tracker/agent"
	"github.com/2389-research/tracker/pipeline"
)

func TestClassifyError(t *testing.T) {
	tests := []struct {
		name     string
		stderr   string
		exitCode int
		want     string
	}{
		{"exit 0 is success", "", 0, pipeline.OutcomeSuccess},
		{"authentication error", "Error: authentication failed", 1, pipeline.OutcomeFail},
		{"unauthorized", "HTTP 401 Unauthorized", 1, pipeline.OutcomeFail},
		{"invalid api key", "Error: Invalid API Key provided", 1, pipeline.OutcomeFail},
		{"rate limit", "Error: rate limit exceeded", 1, pipeline.OutcomeRetry},
		{"429 status", "HTTP 429 Too Many Requests", 1, pipeline.OutcomeRetry},
		{"throttled", "Request throttled by server", 1, pipeline.OutcomeRetry},
		{"budget exceeded", "Error: budget limit reached", 1, pipeline.OutcomeFail},
		{"spending limit", "Error: spending limit exceeded", 1, pipeline.OutcomeFail},
		{"econnrefused", "Error: ECONNREFUSED 127.0.0.1:443", 1, pipeline.OutcomeRetry},
		{"network error", "network error: timeout", 1, pipeline.OutcomeRetry},
		{"connection reset", "connection reset by peer", 1, pipeline.OutcomeRetry},
		{"oom killed", "", 137, pipeline.OutcomeFail},
		{"unknown error", "something weird happened", 1, pipeline.OutcomeFail},
		{"first-rate failure should not match rate limit", "first-rate failure in processing", 1, pipeline.OutcomeFail},
		{"case insensitive auth", "AUTHENTICATION ERROR", 1, pipeline.OutcomeFail},
		{"case insensitive rate", "RATE LIMIT HIT", 1, pipeline.OutcomeRetry},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := classifyError(tt.stderr, tt.exitCode)
			if got != tt.want {
				t.Errorf("classifyError(%q, %d) = %q, want %q", tt.stderr, tt.exitCode, got, tt.want)
			}
		})
	}
}

func TestBuildArgs(t *testing.T) {
	b := &ClaudeCodeBackend{}
	cfg := pipeline.AgentRunConfig{
		Prompt:   "write tests",
		Model:    "claude-sonnet-4-5",
		MaxTurns: 10,
		Extra: &pipeline.ClaudeCodeConfig{
			PermissionMode: pipeline.PermissionFullAuto,
		},
	}

	args := b.buildArgs(cfg)

	// Check required flags exist
	assertContainsFlag(t, args, "-p", "write tests")
	assertContainsFlag(t, args, "--output-format", "stream-json")
	assertContainsFlag(t, args, "--model", "claude-sonnet-4-5")
	assertContainsFlag(t, args, "--max-turns", "10")
	assertContainsFlag(t, args, "--permission-mode", "fullAuto")
}

func TestBuildArgsWithOptionals(t *testing.T) {
	b := &ClaudeCodeBackend{}
	cfg := pipeline.AgentRunConfig{
		Prompt:       "do it",
		SystemPrompt: "be helpful",
		Model:        "claude-sonnet-4-5",
		MaxTurns:     5,
		Extra: &pipeline.ClaudeCodeConfig{
			PermissionMode:  pipeline.PermissionPlan,
			AllowedTools:    []string{"Read", "Write"},
			DisallowedTools: nil,
			MaxBudgetUSD:    1.50,
			MCPServers: []pipeline.MCPServerConfig{
				{Name: "myserver", Command: "npx", Args: []string{"-y", "my-mcp"}},
			},
		},
	}

	args := b.buildArgs(cfg)

	assertContainsFlag(t, args, "--system-prompt", "be helpful")
	assertContainsFlag(t, args, "--allowedTools", "Read,Write")
	assertContainsFlag(t, args, "--permission-mode", "plan")

	// Check --budget
	found := false
	for i, a := range args {
		if a == "--budget" && i+1 < len(args) {
			found = true
			if args[i+1] != "1.50" {
				t.Errorf("expected --budget 1.50, got %s", args[i+1])
			}
		}
	}
	if !found {
		t.Error("expected --budget flag")
	}

	// Check --mcpServers flag present
	hasMCP := false
	for _, a := range args {
		if a == "--mcpServers" {
			hasMCP = true
		}
	}
	if !hasMCP {
		t.Error("expected --mcpServers flag")
	}
}

func TestBuildArgsDisallowedTools(t *testing.T) {
	b := &ClaudeCodeBackend{}
	cfg := pipeline.AgentRunConfig{
		Prompt:   "test",
		Model:    "claude-sonnet-4-5",
		MaxTurns: 1,
		Extra: &pipeline.ClaudeCodeConfig{
			DisallowedTools: []string{"Bash", "Write"},
		},
	}

	args := b.buildArgs(cfg)
	assertContainsFlag(t, args, "--disallowedTools", "Bash,Write")
}

func TestBuildArgsNoExtra(t *testing.T) {
	b := &ClaudeCodeBackend{}
	cfg := pipeline.AgentRunConfig{
		Prompt:   "test",
		Model:    "claude-sonnet-4-5",
		MaxTurns: 1,
	}

	// Should not panic when Extra is nil
	args := b.buildArgs(cfg)
	assertContainsFlag(t, args, "-p", "test")
}

func TestBuildEnv(t *testing.T) {
	env := buildEnv()

	hasPath := false
	hasHome := false
	for _, e := range env {
		if len(e) > 5 && e[:5] == "PATH=" {
			hasPath = true
		}
		if len(e) > 5 && e[:5] == "HOME=" {
			hasHome = true
		}
	}
	if !hasPath {
		t.Error("expected PATH in env")
	}
	if !hasHome {
		t.Error("expected HOME in env")
	}
}

func TestParseNDJSONTextMessage(t *testing.T) {
	msg := `{"type":"assistant","content":[{"type":"text","text":"Hello world"}]}`
	b := &ClaudeCodeBackend{}
	events := b.parseMessage(json.RawMessage(msg))

	if len(events) != 1 {
		t.Fatalf("expected 1 event, got %d", len(events))
	}
	if events[0].Type != agent.EventTextDelta {
		t.Errorf("expected EventTextDelta, got %s", events[0].Type)
	}
	if events[0].Text != "Hello world" {
		t.Errorf("expected 'Hello world', got %q", events[0].Text)
	}
}

func TestParseNDJSONReasoningMessage(t *testing.T) {
	msg := `{"type":"assistant","content":[{"type":"thinking","text":"Let me think..."}]}`
	b := &ClaudeCodeBackend{}
	events := b.parseMessage(json.RawMessage(msg))

	if len(events) != 1 {
		t.Fatalf("expected 1 event, got %d", len(events))
	}
	// Should emit EventLLMReasoning for thinking content
	if events[0].Type != agent.EventLLMReasoning {
		t.Errorf("expected EventLLMReasoning, got %s", events[0].Type)
	}
	if events[0].Text != "Let me think..." {
		t.Errorf("expected 'Let me think...', got %q", events[0].Text)
	}
}

func TestParseNDJSONToolUseMessage(t *testing.T) {
	msg := `{"type":"assistant","content":[{"type":"tool_use","name":"Read","input":"{\"path\":\"foo.go\"}","tool_use_id":"tu_123"}]}`
	b := &ClaudeCodeBackend{}
	events := b.parseMessage(json.RawMessage(msg))

	if len(events) != 1 {
		t.Fatalf("expected 1 event, got %d", len(events))
	}
	if events[0].Type != agent.EventToolCallStart {
		t.Errorf("expected EventToolCallStart, got %s", events[0].Type)
	}
	if events[0].ToolName != "Read" {
		t.Errorf("expected tool name 'Read', got %q", events[0].ToolName)
	}
	if events[0].ToolInput != `{"path":"foo.go"}` {
		t.Errorf("expected tool input, got %q", events[0].ToolInput)
	}
}

func TestParseNDJSONToolResultMessage(t *testing.T) {
	// First register a tool_use_id mapping
	b := &ClaudeCodeBackend{
		toolUseIDs: map[string]string{"tu_123": "Read"},
	}
	msg := `{"type":"user","content":[{"type":"tool_result","tool_use_id":"tu_123","content":"file contents here"}]}`
	events := b.parseMessage(json.RawMessage(msg))

	if len(events) != 1 {
		t.Fatalf("expected 1 event, got %d", len(events))
	}
	if events[0].Type != agent.EventToolCallEnd {
		t.Errorf("expected EventToolCallEnd, got %s", events[0].Type)
	}
	if events[0].ToolName != "Read" {
		t.Errorf("expected tool name 'Read', got %q", events[0].ToolName)
	}
	if events[0].ToolOutput != "file contents here" {
		t.Errorf("expected tool output, got %q", events[0].ToolOutput)
	}
}

func TestParseNDJSONToolResultError(t *testing.T) {
	b := &ClaudeCodeBackend{
		toolUseIDs: map[string]string{"tu_456": "Bash"},
	}
	msg := `{"type":"user","content":[{"type":"tool_result","tool_use_id":"tu_456","content":"command failed","is_error":true}]}`
	events := b.parseMessage(json.RawMessage(msg))

	if len(events) != 1 {
		t.Fatalf("expected 1 event, got %d", len(events))
	}
	if events[0].Type != agent.EventToolCallEnd {
		t.Errorf("expected EventToolCallEnd, got %s", events[0].Type)
	}
	if events[0].ToolError != "command failed" {
		t.Errorf("expected tool error 'command failed', got %q", events[0].ToolError)
	}
}

func TestParseNDJSONResultMessage(t *testing.T) {
	msg := `{"type":"result","turns":5,"usage":{"input_tokens":1000,"output_tokens":500,"cost_usd":0.05}}`
	b := &ClaudeCodeBackend{}
	events := b.parseMessage(json.RawMessage(msg))

	// result messages don't emit events, they populate sessionResult
	if len(events) != 0 {
		t.Errorf("expected 0 events for result message, got %d", len(events))
	}

	// Check that session result fields were populated
	if b.lastResult == nil {
		t.Fatal("expected lastResult to be populated")
	}
	if b.lastResult.Turns != 5 {
		t.Errorf("expected 5 turns, got %d", b.lastResult.Turns)
	}
	if b.lastResult.Usage.InputTokens != 1000 {
		t.Errorf("expected 1000 input tokens, got %d", b.lastResult.Usage.InputTokens)
	}
	if b.lastResult.Usage.OutputTokens != 500 {
		t.Errorf("expected 500 output tokens, got %d", b.lastResult.Usage.OutputTokens)
	}
	if b.lastResult.Usage.EstimatedCost != 0.05 {
		t.Errorf("expected cost 0.05, got %f", b.lastResult.Usage.EstimatedCost)
	}
}

func TestParseNDJSONUnknownType(t *testing.T) {
	msg := `{"type":"something_unknown","data":"whatever"}`
	b := &ClaudeCodeBackend{}
	events := b.parseMessage(json.RawMessage(msg))

	if len(events) != 0 {
		t.Errorf("expected 0 events for unknown type, got %d", len(events))
	}
}

func TestParseNDJSONSystemMessage(t *testing.T) {
	msg := `{"type":"system","content":[{"type":"text","text":"Claude Code v1.2.3"}]}`
	b := &ClaudeCodeBackend{}
	events := b.parseMessage(json.RawMessage(msg))

	if len(events) != 1 {
		t.Fatalf("expected 1 event, got %d", len(events))
	}
	if events[0].Type != agent.EventSessionStart {
		t.Errorf("expected EventSessionStart, got %s", events[0].Type)
	}
}

func TestParseNDJSONMultiContent(t *testing.T) {
	msg := `{"type":"assistant","content":[{"type":"text","text":"first"},{"type":"text","text":"second"}]}`
	b := &ClaudeCodeBackend{}
	events := b.parseMessage(json.RawMessage(msg))

	if len(events) != 2 {
		t.Fatalf("expected 2 events, got %d", len(events))
	}
	if events[0].Text != "first" {
		t.Errorf("expected 'first', got %q", events[0].Text)
	}
	if events[1].Text != "second" {
		t.Errorf("expected 'second', got %q", events[1].Text)
	}
}

func TestBuildMCPServersJSON(t *testing.T) {
	servers := []pipeline.MCPServerConfig{
		{Name: "filesystem", Command: "npx", Args: []string{"-y", "@anthropic/mcp-fs"}},
		{Name: "git", Command: "uvx", Args: []string{"mcp-git"}},
	}

	result := buildMCPServersJSON(servers)

	// Verify it's valid JSON
	var parsed map[string]any
	if err := json.Unmarshal([]byte(result), &parsed); err != nil {
		t.Fatalf("buildMCPServersJSON produced invalid JSON: %v", err)
	}

	// Verify structure
	fs, ok := parsed["filesystem"]
	if !ok {
		t.Fatal("expected 'filesystem' key in MCP servers JSON")
	}
	fsMap, ok := fs.(map[string]any)
	if !ok {
		t.Fatal("expected filesystem value to be an object")
	}
	if fsMap["command"] != "npx" {
		t.Errorf("expected command 'npx', got %v", fsMap["command"])
	}
	argsRaw, ok := fsMap["args"].([]any)
	if !ok {
		t.Fatal("expected args to be an array")
	}
	if len(argsRaw) != 2 {
		t.Errorf("expected 2 args, got %d", len(argsRaw))
	}

	git, ok := parsed["git"]
	if !ok {
		t.Fatal("expected 'git' key in MCP servers JSON")
	}
	gitMap := git.(map[string]any)
	if gitMap["command"] != "uvx" {
		t.Errorf("expected command 'uvx', got %v", gitMap["command"])
	}
}

// assertContainsFlag checks that args contains flag followed by value.
func assertContainsFlag(t *testing.T, args []string, flag, value string) {
	t.Helper()
	for i, a := range args {
		if a == flag && i+1 < len(args) && args[i+1] == value {
			return
		}
	}
	t.Errorf("expected args to contain %s %s, got %v", flag, value, args)
}
