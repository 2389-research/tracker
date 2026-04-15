// ABOUTME: Tests for ClaudeCodeBackend which spawns the claude CLI as a subprocess.
// ABOUTME: Covers arg building, env construction, NDJSON parsing, error classification, and MCP config.
package handlers

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/2389-research/tracker/agent"
	"github.com/2389-research/tracker/llm"
	"github.com/2389-research/tracker/pipeline"
)

// nopCompleter satisfies agent.Completer for tests that need a non-nil client
// but never actually call Complete.
type nopCompleter struct{}

func (nopCompleter) Complete(_ context.Context, _ *llm.Request) (*llm.Response, error) {
	return &llm.Response{}, nil
}

func now() time.Time { return time.Now() }

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
		{"unthrottled should not match", "service is unthrottled now", 1, pipeline.OutcomeFail},
		{"throttling matches", "request throttling in effect", 1, pipeline.OutcomeRetry},
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
	cfg := pipeline.AgentRunConfig{
		Prompt:   "write tests",
		Model:    "claude-sonnet-4-5",
		MaxTurns: 10,
		Extra: &pipeline.ClaudeCodeConfig{
			PermissionMode: pipeline.PermissionBypassPermissions,
		},
	}

	args, err := buildArgs(cfg)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Check required flags exist
	assertContainsFlag(t, args, "-p", "write tests")
	assertContainsFlag(t, args, "--output-format", "stream-json")
	assertContainsFlag(t, args, "--model", "claude-sonnet-4-5")
	assertContainsFlag(t, args, "--max-turns", "10")
	assertContainsFlag(t, args, "--permission-mode", "bypassPermissions")
}

func TestBuildArgsWithOptionals(t *testing.T) {
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

	args, err := buildArgs(cfg)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

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
	cfg := pipeline.AgentRunConfig{
		Prompt:   "test",
		Model:    "claude-sonnet-4-5",
		MaxTurns: 1,
		Extra: &pipeline.ClaudeCodeConfig{
			DisallowedTools: []string{"Bash", "Write"},
		},
	}

	args, err := buildArgs(cfg)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	assertContainsFlag(t, args, "--disallowedTools", "Bash,Write")
}

func TestBuildArgsNoExtra(t *testing.T) {
	cfg := pipeline.AgentRunConfig{
		Prompt:   "test",
		Model:    "claude-sonnet-4-5",
		MaxTurns: 1,
	}

	// Should not panic when Extra is nil
	args, err := buildArgs(cfg)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	assertContainsFlag(t, args, "-p", "test")
}

func TestBuildArgsSystemPromptWithoutClaudeCodeConfig(t *testing.T) {
	cfg := pipeline.AgentRunConfig{
		Prompt:       "test",
		SystemPrompt: "you are a helpful coder",
		Model:        "claude-sonnet-4-5",
	}

	args, err := buildArgs(cfg)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	assertContainsFlag(t, args, "--system-prompt", "you are a helpful coder")
}

func TestBuildArgsInvalidPermissionMode(t *testing.T) {
	cfg := pipeline.AgentRunConfig{
		Prompt: "test",
		Extra: &pipeline.ClaudeCodeConfig{
			PermissionMode: "yolo",
		},
	}

	_, err := buildArgs(cfg)
	if err == nil {
		t.Fatal("expected error for invalid permission mode")
	}
	if got := err.Error(); got != `invalid permission mode: "yolo"` {
		t.Errorf("unexpected error: %s", got)
	}
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
	msg := `{"type":"assistant","message":{"content":[{"type":"text","text":"Hello world"}]}}`
	state := &runState{toolUseIDs: make(map[string]string)}
	events := parseMessage(json.RawMessage(msg), state)

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
	msg := `{"type":"assistant","message":{"content":[{"type":"thinking","text":"Let me think..."}]}}`
	state := &runState{toolUseIDs: make(map[string]string)}
	events := parseMessage(json.RawMessage(msg), state)

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
	// Claude Code CLI sends input as a JSON object, not a string.
	msg := `{"type":"assistant","message":{"content":[{"type":"tool_use","name":"Read","input":{"path":"foo.go"},"tool_use_id":"tu_123"}]}}`
	state := &runState{toolUseIDs: make(map[string]string)}
	events := parseMessage(json.RawMessage(msg), state)

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

	// Verify tool_use_id was tracked
	if state.toolUseIDs["tu_123"] != "Read" {
		t.Errorf("expected toolUseIDs to track tu_123=Read, got %v", state.toolUseIDs)
	}
}

func TestParseNDJSONToolUseObjectInput(t *testing.T) {
	// Input as a JSON object (not a string)
	msg := `{"type":"assistant","message":{"content":[{"type":"tool_use","name":"Read","input":{"path":"foo.go"},"tool_use_id":"tu_789"}]}}`
	state := &runState{toolUseIDs: make(map[string]string)}
	events := parseMessage(json.RawMessage(msg), state)

	if len(events) != 1 {
		t.Fatalf("expected 1 event, got %d", len(events))
	}
	if events[0].ToolInput != `{"path":"foo.go"}` {
		t.Errorf("expected tool input object, got %q", events[0].ToolInput)
	}
}

func TestParseNDJSONToolResultMessage(t *testing.T) {
	state := &runState{
		toolUseIDs: map[string]string{"tu_123": "Read"},
	}
	msg := `{"type":"user","content":[{"type":"tool_result","tool_use_id":"tu_123","content":"file contents here"}]}`
	events := parseMessage(json.RawMessage(msg), state)

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
	state := &runState{
		toolUseIDs: map[string]string{"tu_456": "Bash"},
	}
	msg := `{"type":"user","content":[{"type":"tool_result","tool_use_id":"tu_456","content":"command failed","is_error":true}]}`
	events := parseMessage(json.RawMessage(msg), state)

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
	msg := `{"type":"result","num_turns":5,"total_cost_usd":0.05,"usage":{"input_tokens":1000,"output_tokens":500}}`
	state := &runState{toolUseIDs: make(map[string]string)}
	events := parseMessage(json.RawMessage(msg), state)

	// result messages don't emit events, they populate sessionResult
	if len(events) != 0 {
		t.Errorf("expected 0 events for result message, got %d", len(events))
	}

	// Check that session result fields were populated
	if state.lastResult == nil {
		t.Fatal("expected lastResult to be populated")
	}
	if state.lastResult.Turns != 5 {
		t.Errorf("expected 5 turns, got %d", state.lastResult.Turns)
	}
	if state.lastResult.Usage.InputTokens != 1000 {
		t.Errorf("expected 1000 input tokens, got %d", state.lastResult.Usage.InputTokens)
	}
	if state.lastResult.Usage.OutputTokens != 500 {
		t.Errorf("expected 500 output tokens, got %d", state.lastResult.Usage.OutputTokens)
	}
	if state.lastResult.Usage.EstimatedCost != 0.05 {
		t.Errorf("expected cost 0.05, got %f", state.lastResult.Usage.EstimatedCost)
	}
}

func TestParseNDJSONUnknownType(t *testing.T) {
	msg := `{"type":"something_unknown","data":"whatever"}`
	state := &runState{toolUseIDs: make(map[string]string)}
	events := parseMessage(json.RawMessage(msg), state)

	if len(events) != 0 {
		t.Errorf("expected 0 events for unknown type, got %d", len(events))
	}
}

func TestParseNDJSONSystemMessage(t *testing.T) {
	msg := `{"type":"system","subtype":"init","cwd":"/tmp"}`
	state := &runState{toolUseIDs: make(map[string]string)}
	events := parseMessage(json.RawMessage(msg), state)

	if len(events) != 2 {
		t.Fatalf("expected 2 events (preparing + turn start), got %d", len(events))
	}
	if events[0].Type != agent.EventLLMRequestPreparing {
		t.Errorf("expected EventLLMRequestPreparing, got %s", events[0].Type)
	}
	if events[0].Provider != "claude-code" {
		t.Errorf("expected provider 'claude-code', got %q", events[0].Provider)
	}
	if events[1].Type != agent.EventTurnStart {
		t.Errorf("expected EventTurnStart, got %s", events[1].Type)
	}
}

func TestParseNDJSONMultiContent(t *testing.T) {
	msg := `{"type":"assistant","message":{"content":[{"type":"text","text":"first"},{"type":"text","text":"second"}]}}`
	state := &runState{toolUseIDs: make(map[string]string)}
	events := parseMessage(json.RawMessage(msg), state)

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

	result, err := buildMCPServersJSON(servers)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

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

func TestBuildResultNilLastResult(t *testing.T) {
	state := &runState{toolUseIDs: make(map[string]string)}
	_, err := buildResult(state)
	if err == nil {
		t.Fatal("expected error when lastResult is nil")
	}
	if !strings.Contains(err.Error(), "no result message") {
		t.Errorf("unexpected error message: %v", err)
	}
}

func TestBuildResultWithLastResult(t *testing.T) {
	state := &runState{
		toolUseIDs: make(map[string]string),
		lastResult: &agent.SessionResult{Turns: 3},
	}
	result, err := buildResult(state)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Turns != 3 {
		t.Errorf("expected 3 turns, got %d", result.Turns)
	}
}

func TestBuildEnvIncludesSSH(t *testing.T) {
	// Set SSH vars for this test.
	t.Setenv("SSH_AUTH_SOCK", "/tmp/ssh-test.sock")
	t.Setenv("SSH_AGENT_PID", "12345")

	env := buildEnv()

	hasAuthSock := false
	hasAgentPID := false
	for _, e := range env {
		if e == "SSH_AUTH_SOCK=/tmp/ssh-test.sock" {
			hasAuthSock = true
		}
		if e == "SSH_AGENT_PID=12345" {
			hasAgentPID = true
		}
	}
	if !hasAuthSock {
		t.Error("expected SSH_AUTH_SOCK in env")
	}
	if !hasAgentPID {
		t.Error("expected SSH_AGENT_PID in env")
	}
}

func TestContainsThrottle(t *testing.T) {
	tests := []struct {
		input string
		want  bool
	}{
		{"request throttled", true},
		{"throttling in effect", true},
		{"service is unthrottled", false},
		{"no issues here", false},
		{"THROTTLED", false}, // containsThrottle expects lowered input
	}
	for _, tt := range tests {
		if got := containsThrottle(tt.input); got != tt.want {
			t.Errorf("containsThrottle(%q) = %v, want %v", tt.input, got, tt.want)
		}
	}
}

func TestSelectBackendUnknown(t *testing.T) {
	h := NewCodergenHandler(nil, "/tmp")
	node := &pipeline.Node{
		ID:    "test",
		Attrs: map[string]string{"backend": "unknown-backend"},
	}
	_, err := h.selectBackend(node)
	if err == nil {
		t.Fatal("expected error for unknown backend")
	}
	if got := err.Error(); !strings.Contains(got, "unknown backend") {
		t.Errorf("expected 'unknown backend' in error, got: %s", got)
	}
}

func TestSelectBackendNativeDefault(t *testing.T) {
	h := NewCodergenHandler(nopCompleter{}, "/tmp")
	node := &pipeline.Node{
		ID:    "test",
		Attrs: map[string]string{},
	}
	backend, err := h.selectBackend(node)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if _, ok := backend.(*NativeBackend); !ok {
		t.Errorf("expected NativeBackend, got %T", backend)
	}
}

func TestSelectBackendNativeExplicit(t *testing.T) {
	h := NewCodergenHandler(nopCompleter{}, "/tmp")
	node := &pipeline.Node{
		ID:    "test",
		Attrs: map[string]string{"backend": "native"},
	}
	backend, err := h.selectBackend(node)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if _, ok := backend.(*NativeBackend); !ok {
		t.Errorf("expected NativeBackend, got %T", backend)
	}
}

func TestSelectBackendCodergenAlias(t *testing.T) {
	h := NewCodergenHandler(nopCompleter{}, "/tmp")
	node := &pipeline.Node{
		ID:    "test",
		Attrs: map[string]string{"backend": "codergen"},
	}
	backend, err := h.selectBackend(node)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if _, ok := backend.(*NativeBackend); !ok {
		t.Errorf("expected NativeBackend, got %T", backend)
	}
}

func TestSelectBackendNativeNilClientErrors(t *testing.T) {
	h := NewCodergenHandler(nil, "/tmp")
	node := &pipeline.Node{
		ID:    "test",
		Attrs: map[string]string{},
	}
	_, err := h.selectBackend(node)
	if err == nil {
		t.Fatal("expected error for nil client, got nil")
	}
}

// TestSelectBackendNodeAttrWinsOverGlobalClaudeCode verifies that a per-node
// "backend: native" attribute overrides the global --backend claude-code flag.
// This is the core fix for issue #70: mixed-backend pipelines.
func TestSelectBackendNodeAttrWinsOverGlobalClaudeCode(t *testing.T) {
	h := NewCodergenHandler(nopCompleter{}, "/tmp")
	h.defaultBackendName = "claude-code" // simulate --backend claude-code

	node := &pipeline.Node{
		ID:    "openai-node",
		Attrs: map[string]string{"backend": "native"}, // per-node override
	}
	backend, err := h.selectBackend(node)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if _, ok := backend.(*NativeBackend); !ok {
		t.Errorf("expected NativeBackend (per-node attr wins), got %T", backend)
	}
}

// TestSelectBackendNodeAttrClaudeCodeOverGlobalNative verifies that a per-node
// "backend: claude-code" attribute works when the global default is native.
func TestSelectBackendNodeAttrClaudeCodeOverGlobalNative(t *testing.T) {
	// Set up a handler with a native client but no global claude-code default.
	h := NewCodergenHandler(nopCompleter{}, "/tmp")
	// defaultBackendName is "" (native) by default.

	// Pre-populate the claudeCodeBackend to avoid requiring the claude binary.
	h.claudeCodeBackend = &ClaudeCodeBackend{claudePath: "/usr/bin/claude"}

	node := &pipeline.Node{
		ID:    "claude-node",
		Attrs: map[string]string{"backend": "claude-code"},
	}
	backend, err := h.selectBackend(node)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if _, ok := backend.(*ClaudeCodeBackend); !ok {
		t.Errorf("expected ClaudeCodeBackend (per-node attr wins), got %T", backend)
	}
}

// TestSelectBackendNativeAttrWithGlobalClaudeCodeNoClient verifies that when
// the global backend is claude-code but no API keys are configured, a node with
// "backend: native" gets a clear, actionable error message.
func TestSelectBackendNativeAttrWithGlobalClaudeCodeNoClient(t *testing.T) {
	h := NewCodergenHandler(nil, "/tmp") // nil client = no API keys
	h.defaultBackendName = "claude-code"

	node := &pipeline.Node{
		ID:    "native-node",
		Attrs: map[string]string{"backend": "native"},
	}
	_, err := h.selectBackend(node)
	if err == nil {
		t.Fatal("expected error for native backend with nil client and global claude-code")
	}
	errMsg := err.Error()
	if !strings.Contains(errMsg, "backend: native") {
		t.Errorf("expected 'backend: native' in error, got: %s", errMsg)
	}
	if !strings.Contains(errMsg, "API keys") {
		t.Errorf("expected 'API keys' guidance in error, got: %s", errMsg)
	}
	if !strings.Contains(errMsg, "claude-code") {
		t.Errorf("expected 'claude-code' context in error, got: %s", errMsg)
	}
}

// TestSelectBackendGlobalClaudeCodeUsedWhenNoNodeAttr verifies that nodes
// without an explicit "backend" attr use the global --backend claude-code.
func TestSelectBackendGlobalClaudeCodeUsedWhenNoNodeAttr(t *testing.T) {
	h := NewCodergenHandler(nil, "/tmp")
	h.defaultBackendName = "claude-code"
	// Pre-populate the claudeCodeBackend to avoid requiring the claude binary.
	h.claudeCodeBackend = &ClaudeCodeBackend{claudePath: "/usr/bin/claude"}

	node := &pipeline.Node{
		ID:    "default-node",
		Attrs: map[string]string{}, // no backend attr
	}
	backend, err := h.selectBackend(node)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if _, ok := backend.(*ClaudeCodeBackend); !ok {
		t.Errorf("expected ClaudeCodeBackend (global default), got %T", backend)
	}
}

func TestGraphHasPerNodeBackend(t *testing.T) {
	t.Run("no backend attrs", func(t *testing.T) {
		g := pipeline.NewGraph("test")
		g.Nodes["a"] = &pipeline.Node{ID: "a", Attrs: map[string]string{"prompt": "do it"}}
		g.Nodes["b"] = &pipeline.Node{ID: "b", Attrs: map[string]string{"llm_model": "gpt-4o"}}
		if graphHasPerNodeBackend(g) {
			t.Error("expected false for graph with no backend attrs")
		}
	})

	t.Run("one node has backend attr", func(t *testing.T) {
		g := pipeline.NewGraph("test")
		g.Nodes["a"] = &pipeline.Node{ID: "a", Attrs: map[string]string{"prompt": "do it"}}
		g.Nodes["b"] = &pipeline.Node{ID: "b", Attrs: map[string]string{"backend": "claude-code"}}
		if !graphHasPerNodeBackend(g) {
			t.Error("expected true for graph with a backend attr")
		}
	})

	t.Run("empty graph", func(t *testing.T) {
		g := pipeline.NewGraph("test")
		if graphHasPerNodeBackend(g) {
			t.Error("expected false for empty graph")
		}
	})

	t.Run("native backend attr counts", func(t *testing.T) {
		g := pipeline.NewGraph("test")
		g.Nodes["a"] = &pipeline.Node{ID: "a", Attrs: map[string]string{"backend": "native"}}
		if !graphHasPerNodeBackend(g) {
			t.Error("expected true for graph with backend: native attr")
		}
	})
}

func TestBuildClaudeCodeConfig(t *testing.T) {
	h := NewCodergenHandler(nil, "/tmp")
	node := &pipeline.Node{
		ID: "test",
		Attrs: map[string]string{
			"max_budget_usd":  "2.50",
			"permission_mode": "plan",
			"allowed_tools":   "Read,Write",
		},
	}
	cfg, err := h.buildClaudeCodeConfig(node)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cfg.MaxBudgetUSD != 2.50 {
		t.Errorf("expected budget 2.50, got %f", cfg.MaxBudgetUSD)
	}
	if cfg.PermissionMode != pipeline.PermissionPlan {
		t.Errorf("expected plan mode, got %q", cfg.PermissionMode)
	}
	if len(cfg.AllowedTools) != 2 {
		t.Errorf("expected 2 allowed tools, got %d", len(cfg.AllowedTools))
	}
}

func TestBuildClaudeCodeConfigDefaults(t *testing.T) {
	h := NewCodergenHandler(nil, "/tmp")
	node := &pipeline.Node{
		ID:    "test",
		Attrs: map[string]string{},
	}
	cfg, err := h.buildClaudeCodeConfig(node)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cfg.PermissionMode != pipeline.PermissionBypassPermissions {
		t.Errorf("expected bypassPermissions default, got %q", cfg.PermissionMode)
	}
}

func TestBuildClaudeCodeConfigInvalidBudget(t *testing.T) {
	h := NewCodergenHandler(nil, "/tmp")
	node := &pipeline.Node{
		ID:    "test",
		Attrs: map[string]string{"max_budget_usd": "not-a-number"},
	}
	_, err := h.buildClaudeCodeConfig(node)
	if err == nil {
		t.Fatal("expected error for invalid budget")
	}
}

func TestBuildClaudeCodeConfigInvalidPermission(t *testing.T) {
	h := NewCodergenHandler(nil, "/tmp")
	node := &pipeline.Node{
		ID:    "test",
		Attrs: map[string]string{"permission_mode": "yolo"},
	}
	_, err := h.buildClaudeCodeConfig(node)
	if err == nil {
		t.Fatal("expected error for invalid permission mode")
	}
}

func TestBuildClaudeCodeConfigMCPServers(t *testing.T) {
	h := NewCodergenHandler(nil, "/tmp")
	node := &pipeline.Node{
		ID: "test",
		Attrs: map[string]string{
			"mcp_servers": "pg=npx @mcp/postgres connstr",
		},
	}
	cfg, err := h.buildClaudeCodeConfig(node)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(cfg.MCPServers) != 1 {
		t.Fatalf("expected 1 MCP server, got %d", len(cfg.MCPServers))
	}
	if cfg.MCPServers[0].Name != "pg" {
		t.Errorf("expected server name 'pg', got %q", cfg.MCPServers[0].Name)
	}
}

func TestBuildClaudeCodeConfigBothToolLists(t *testing.T) {
	h := NewCodergenHandler(nil, "/tmp")
	node := &pipeline.Node{
		ID: "test",
		Attrs: map[string]string{
			"allowed_tools":    "Read",
			"disallowed_tools": "Write",
		},
	}
	_, err := h.buildClaudeCodeConfig(node)
	if err == nil {
		t.Fatal("expected error when both allowed and disallowed tools are set")
	}
}

func TestStoreResultAggregatesToolCalls(t *testing.T) {
	state := &runState{
		toolUseIDs: map[string]string{
			"tu_1": "Read",
			"tu_2": "Write",
			"tu_3": "Read",
			"tu_4": "Bash",
			"tu_5": "Read",
		},
	}
	msg := ndjsonMessage{
		Type:  "result",
		Turns: 3,
		Usage: &ndjsonUsage{InputTokens: 100, OutputTokens: 50, CostUSD: 0.01},
	}
	storeResult(msg, state)

	if state.lastResult == nil {
		t.Fatal("expected lastResult to be populated")
	}
	if state.lastResult.ToolCalls["Read"] != 3 {
		t.Errorf("expected Read=3, got %d", state.lastResult.ToolCalls["Read"])
	}
	if state.lastResult.ToolCalls["Write"] != 1 {
		t.Errorf("expected Write=1, got %d", state.lastResult.ToolCalls["Write"])
	}
	if state.lastResult.ToolCalls["Bash"] != 1 {
		t.Errorf("expected Bash=1, got %d", state.lastResult.ToolCalls["Bash"])
	}
}

func TestStoreResultNoUsage(t *testing.T) {
	state := &runState{toolUseIDs: make(map[string]string)}
	msg := ndjsonMessage{Type: "result", Turns: 2}
	storeResult(msg, state)

	if state.lastResult == nil {
		t.Fatal("expected lastResult to be populated")
	}
	if state.lastResult.Usage.InputTokens != 0 {
		t.Errorf("expected 0 input tokens, got %d", state.lastResult.Usage.InputTokens)
	}
}

func TestParseMessageUnmarshalFailureTracked(t *testing.T) {
	state := &runState{toolUseIDs: make(map[string]string)}
	// Invalid JSON that decoded fine but won't unmarshal into ndjsonMessage
	// Actually, any valid JSON will unmarshal into the struct. Use truly invalid JSON.
	events := parseMessage(json.RawMessage(`{invalid json`), state)

	if len(events) != 0 {
		t.Errorf("expected 0 events, got %d", len(events))
	}
	if state.decodeErrors != 1 {
		t.Errorf("expected 1 decode error, got %d", state.decodeErrors)
	}
}

func TestParseMessageUnknownAssistantContentType(t *testing.T) {
	msg := `{"type":"assistant","message":{"content":[{"type":"image","text":"data"}]}}`
	state := &runState{toolUseIDs: make(map[string]string)}
	events := parseMessage(json.RawMessage(msg), state)

	// Unknown content types are logged but produce no events
	if len(events) != 0 {
		t.Errorf("expected 0 events for unknown content type, got %d", len(events))
	}
}

func TestParseUserContentSkipsNonToolResult(t *testing.T) {
	state := &runState{toolUseIDs: make(map[string]string)}
	content := []ndjsonContent{
		{Type: "text", Text: "hello"},
		{Type: "tool_result", ToolUseID: "tu_1", Content: json.RawMessage(`"output"`)},
	}
	events := parseUserContent(content, now(), state)

	// Only the tool_result should produce an event
	if len(events) != 1 {
		t.Fatalf("expected 1 event, got %d", len(events))
	}
	if events[0].Type != agent.EventToolCallEnd {
		t.Errorf("expected EventToolCallEnd, got %s", events[0].Type)
	}
}

func TestContentStringJSONString(t *testing.T) {
	c := &ndjsonContent{Content: json.RawMessage(`"hello world"`)}
	got := c.contentString()
	if got != "hello world" {
		t.Errorf("expected 'hello world', got %q", got)
	}
}

func TestContentStringJSONArray(t *testing.T) {
	c := &ndjsonContent{Content: json.RawMessage(`[{"type":"text","text":"part one"},{"type":"text","text":"part two"}]`)}
	got := c.contentString()
	if got != "part one\npart two" {
		t.Errorf("expected 'part one\\npart two', got %q", got)
	}
}

func TestContentStringArraySkipsEmptyText(t *testing.T) {
	c := &ndjsonContent{Content: json.RawMessage(`[{"type":"text","text":"only"},{"type":"image","text":""}]`)}
	got := c.contentString()
	if got != "only" {
		t.Errorf("expected 'only', got %q", got)
	}
}

func TestContentStringEmpty(t *testing.T) {
	c := &ndjsonContent{}
	got := c.contentString()
	if got != "" {
		t.Errorf("expected empty string, got %q", got)
	}
}

func TestContentStringNull(t *testing.T) {
	c := &ndjsonContent{Content: json.RawMessage(`null`)}
	// JSON null unmarshals into a Go string as "" successfully.
	got := c.contentString()
	if got != "" {
		t.Errorf("expected empty string for null, got %q", got)
	}
}

func TestContentStringInvalidJSON(t *testing.T) {
	c := &ndjsonContent{Content: json.RawMessage(`{not valid}`)}
	got := c.contentString()
	// Falls through to raw string return.
	if got != "{not valid}" {
		t.Errorf("expected raw fallback, got %q", got)
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
