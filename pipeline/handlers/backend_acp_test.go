// ABOUTME: Tests for ACPBackend agent name resolution, config building, and event translation.
// ABOUTME: Covers provider mapping, explicit overrides, session update conversion, and file operations.
package handlers

import (
	"bytes"
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	acp "github.com/coder/acp-go-sdk"

	"github.com/2389-research/tracker/agent"
	"github.com/2389-research/tracker/pipeline"
)

func TestResolveAgentName(t *testing.T) {
	b := NewACPBackend()

	tests := []struct {
		name     string
		cfg      pipeline.AgentRunConfig
		expected string
	}{
		{
			name:     "anthropic maps to claude-agent-acp",
			cfg:      pipeline.AgentRunConfig{Provider: "anthropic"},
			expected: "claude-agent-acp",
		},
		{
			name:     "openai maps to codex-acp",
			cfg:      pipeline.AgentRunConfig{Provider: "openai"},
			expected: "codex-acp",
		},
		{
			name:     "gemini maps to gemini",
			cfg:      pipeline.AgentRunConfig{Provider: "gemini"},
			expected: "gemini",
		},
		{
			name:     "empty provider defaults to claude-agent-acp",
			cfg:      pipeline.AgentRunConfig{Provider: ""},
			expected: "claude-agent-acp",
		},
		{
			name:     "unknown provider defaults to claude-agent-acp",
			cfg:      pipeline.AgentRunConfig{Provider: "mistral"},
			expected: "claude-agent-acp",
		},
		{
			name: "explicit acp_agent overrides provider",
			cfg: pipeline.AgentRunConfig{
				Provider: "openai",
				Extra:    &pipeline.ACPConfig{Agent: "gemini"},
			},
			expected: "gemini",
		},
		{
			name: "explicit acp_agent with empty provider",
			cfg: pipeline.AgentRunConfig{
				Extra: &pipeline.ACPConfig{Agent: "codex"},
			},
			expected: "codex",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := b.resolveAgentName(tt.cfg)
			if got != tt.expected {
				t.Errorf("resolveAgentName() = %q, want %q", got, tt.expected)
			}
		})
	}
}

func TestBuildACPPromptBlocks(t *testing.T) {
	t.Run("prompt only", func(t *testing.T) {
		cfg := pipeline.AgentRunConfig{Prompt: "do the thing"}
		blocks := buildACPPromptBlocks(cfg)
		if len(blocks) != 1 {
			t.Fatalf("expected 1 block, got %d", len(blocks))
		}
		if blocks[0].Text == nil || blocks[0].Text.Text != "do the thing" {
			t.Errorf("unexpected block content: %+v", blocks[0])
		}
	})

	t.Run("system prompt prepended", func(t *testing.T) {
		cfg := pipeline.AgentRunConfig{
			Prompt:       "do the thing",
			SystemPrompt: "you are helpful",
		}
		blocks := buildACPPromptBlocks(cfg)
		if len(blocks) != 2 {
			t.Fatalf("expected 2 blocks, got %d", len(blocks))
		}
		if blocks[0].Text == nil || blocks[0].Text.Text != "System: you are helpful" {
			t.Errorf("unexpected system block: %+v", blocks[0])
		}
		if blocks[1].Text == nil || blocks[1].Text.Text != "do the thing" {
			t.Errorf("unexpected prompt block: %+v", blocks[1])
		}
	})
}

func TestBuildACPConfig(t *testing.T) {
	t.Run("with acp_agent attribute", func(t *testing.T) {
		node := &pipeline.Node{
			ID:    "test",
			Attrs: map[string]string{"acp_agent": "codex"},
		}
		cfg := buildACPConfig(node)
		if cfg.Agent != "codex" {
			t.Errorf("expected Agent=codex, got %q", cfg.Agent)
		}
	})

	t.Run("without acp_agent attribute", func(t *testing.T) {
		node := &pipeline.Node{
			ID:    "test",
			Attrs: map[string]string{},
		}
		cfg := buildACPConfig(node)
		if cfg.Agent != "" {
			t.Errorf("expected empty Agent, got %q", cfg.Agent)
		}
	})
}

func TestACPSessionUpdateToEvents(t *testing.T) {
	t.Run("agent message chunk emits text delta", func(t *testing.T) {
		var events []agent.Event
		h := &acpClientHandler{
			emit:      func(e agent.Event) { events = append(events, e) },
			toolNames: make(map[string]string),
		}

		err := h.SessionUpdate(context.Background(), acp.SessionNotification{
			SessionId: "s1",
			Update:    acp.UpdateAgentMessageText("hello world"),
		})
		if err != nil {
			t.Fatal(err)
		}
		if len(events) != 1 {
			t.Fatalf("expected 1 event, got %d", len(events))
		}
		if events[0].Type != agent.EventTextDelta {
			t.Errorf("expected EventTextDelta, got %s", events[0].Type)
		}
		if events[0].Text != "hello world" {
			t.Errorf("expected text 'hello world', got %q", events[0].Text)
		}
	})

	t.Run("agent thought chunk emits reasoning", func(t *testing.T) {
		var events []agent.Event
		h := &acpClientHandler{
			emit:      func(e agent.Event) { events = append(events, e) },
			toolNames: make(map[string]string),
		}

		err := h.SessionUpdate(context.Background(), acp.SessionNotification{
			SessionId: "s1",
			Update:    acp.UpdateAgentThoughtText("thinking..."),
		})
		if err != nil {
			t.Fatal(err)
		}
		if len(events) != 1 {
			t.Fatalf("expected 1 event, got %d", len(events))
		}
		if events[0].Type != agent.EventLLMReasoning {
			t.Errorf("expected EventLLMReasoning, got %s", events[0].Type)
		}
	})

	t.Run("tool call emits tool start", func(t *testing.T) {
		var events []agent.Event
		h := &acpClientHandler{
			emit:      func(e agent.Event) { events = append(events, e) },
			toolNames: make(map[string]string),
		}

		err := h.SessionUpdate(context.Background(), acp.SessionNotification{
			SessionId: "s1",
			Update: acp.StartToolCall(
				"tool-1",
				"Read file",
				acp.WithStartStatus(acp.ToolCallStatusInProgress),
			),
		})
		if err != nil {
			t.Fatal(err)
		}
		if len(events) != 1 {
			t.Fatalf("expected 1 event, got %d", len(events))
		}
		if events[0].Type != agent.EventToolCallStart {
			t.Errorf("expected EventToolCallStart, got %s", events[0].Type)
		}
		if events[0].ToolName != "Read file" {
			t.Errorf("expected ToolName 'Read file', got %q", events[0].ToolName)
		}
	})

	t.Run("tool call update completed emits tool end", func(t *testing.T) {
		var events []agent.Event
		h := &acpClientHandler{
			emit:      func(e agent.Event) { events = append(events, e) },
			toolNames: map[string]string{"tool-1": "Read file"},
		}

		err := h.SessionUpdate(context.Background(), acp.SessionNotification{
			SessionId: "s1",
			Update: acp.UpdateToolCall(
				"tool-1",
				acp.WithUpdateStatus(acp.ToolCallStatusCompleted),
				acp.WithUpdateRawOutput("file contents here"),
			),
		})
		if err != nil {
			t.Fatal(err)
		}
		if len(events) != 1 {
			t.Fatalf("expected 1 event, got %d", len(events))
		}
		if events[0].Type != agent.EventToolCallEnd {
			t.Errorf("expected EventToolCallEnd, got %s", events[0].Type)
		}
		if events[0].ToolOutput == "" {
			t.Error("expected non-empty tool output")
		}
	})

	t.Run("tool call update failed emits tool end with error", func(t *testing.T) {
		var events []agent.Event
		h := &acpClientHandler{
			emit:      func(e agent.Event) { events = append(events, e) },
			toolNames: map[string]string{"tool-1": "Write file"},
		}

		err := h.SessionUpdate(context.Background(), acp.SessionNotification{
			SessionId: "s1",
			Update: acp.UpdateToolCall(
				"tool-1",
				acp.WithUpdateStatus(acp.ToolCallStatusFailed),
				acp.WithUpdateRawOutput("permission denied"),
			),
		})
		if err != nil {
			t.Fatal(err)
		}
		if len(events) != 1 {
			t.Fatalf("expected 1 event, got %d", len(events))
		}
		if events[0].ToolError == "" {
			t.Error("expected non-empty tool error")
		}
	})

	t.Run("plan update emits nothing", func(t *testing.T) {
		var events []agent.Event
		h := &acpClientHandler{
			emit:      func(e agent.Event) { events = append(events, e) },
			toolNames: make(map[string]string),
		}

		err := h.SessionUpdate(context.Background(), acp.SessionNotification{
			SessionId: "s1",
			Update:    acp.UpdatePlan(acp.PlanEntry{Content: "step 1"}),
		})
		if err != nil {
			t.Fatal(err)
		}
		if len(events) != 0 {
			t.Errorf("expected 0 events for plan update, got %d", len(events))
		}
	})
}

func TestACPClientRequestPermission(t *testing.T) {
	h := &acpClientHandler{
		emit:      func(agent.Event) {},
		toolNames: make(map[string]string),
	}

	resp, err := h.RequestPermission(context.Background(), acp.RequestPermissionRequest{
		SessionId: "s1",
		Options: []acp.PermissionOption{
			{OptionId: "allow", Name: "Allow", Kind: acp.PermissionOptionKindAllowOnce},
			{OptionId: "deny", Name: "Deny", Kind: acp.PermissionOptionKindRejectOnce},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if resp.Outcome.Selected == nil {
		t.Fatal("expected selected outcome")
	}
	if resp.Outcome.Selected.OptionId != "allow" {
		t.Errorf("expected allow, got %q", resp.Outcome.Selected.OptionId)
	}
}

func TestACPClientReadWriteFile(t *testing.T) {
	tmpDir := t.TempDir()
	h := &acpClientHandler{
		emit:       func(agent.Event) {},
		workingDir: tmpDir,
		toolNames:  make(map[string]string),
	}

	testFile := filepath.Join(tmpDir, "test.txt")

	// Write a file.
	_, err := h.WriteTextFile(context.Background(), acp.WriteTextFileRequest{
		Path:      testFile,
		Content:   "hello\nworld\n",
		SessionId: "s1",
	})
	if err != nil {
		t.Fatal(err)
	}

	// Read it back.
	resp, err := h.ReadTextFile(context.Background(), acp.ReadTextFileRequest{
		Path:      testFile,
		SessionId: "s1",
	})
	if err != nil {
		t.Fatal(err)
	}
	if resp.Content != "hello\nworld\n" {
		t.Errorf("unexpected content: %q", resp.Content)
	}

	// Read with line/limit.
	line2 := 2
	limit1 := 1
	resp, err = h.ReadTextFile(context.Background(), acp.ReadTextFileRequest{
		Path:      testFile,
		SessionId: "s1",
		Line:      &line2,
		Limit:     &limit1,
	})
	if err != nil {
		t.Fatal(err)
	}
	if resp.Content != "world" {
		t.Errorf("expected 'world', got %q", resp.Content)
	}

	// Reject relative paths.
	_, err = h.ReadTextFile(context.Background(), acp.ReadTextFileRequest{
		Path:      "relative/path.txt",
		SessionId: "s1",
	})
	if err == nil {
		t.Error("expected error for relative path")
	}
}

func TestACPClientTerminal(t *testing.T) {
	h := &acpClientHandler{
		emit:       func(agent.Event) {},
		workingDir: t.TempDir(),
		toolNames:  make(map[string]string),
	}

	// Create a terminal running a simple command.
	resp, err := h.CreateTerminal(context.Background(), acp.CreateTerminalRequest{
		Command:   "echo",
		Args:      []string{"hello from terminal"},
		SessionId: "s1",
	})
	if err != nil {
		t.Fatal(err)
	}
	if resp.TerminalId == "" {
		t.Fatal("expected terminal ID")
	}

	// Wait for it to finish.
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	waitResp, err := h.WaitForTerminalExit(ctx, acp.WaitForTerminalExitRequest{
		TerminalId: resp.TerminalId,
		SessionId:  "s1",
	})
	if err != nil {
		t.Fatal(err)
	}
	if waitResp.ExitCode == nil || *waitResp.ExitCode != 0 {
		t.Errorf("expected exit code 0, got %v", waitResp.ExitCode)
	}

	// Check output.
	outResp, err := h.TerminalOutput(context.Background(), acp.TerminalOutputRequest{
		TerminalId: resp.TerminalId,
		SessionId:  "s1",
	})
	if err != nil {
		t.Fatal(err)
	}
	if outResp.Output == "" {
		t.Error("expected non-empty output")
	}

	// Release it.
	_, err = h.ReleaseTerminal(context.Background(), acp.ReleaseTerminalRequest{
		TerminalId: resp.TerminalId,
		SessionId:  "s1",
	})
	if err != nil {
		t.Fatal(err)
	}
}

func TestACPCollectedText(t *testing.T) {
	h := &acpClientHandler{
		emit:      func(agent.Event) {},
		toolNames: make(map[string]string),
	}

	// Simulate multiple message chunks.
	if err := h.SessionUpdate(context.Background(), acp.SessionNotification{
		SessionId: "s1",
		Update:    acp.UpdateAgentMessageText("hello "),
	}); err != nil {
		t.Fatalf("SessionUpdate(hello): %v", err)
	}
	if err := h.SessionUpdate(context.Background(), acp.SessionNotification{
		SessionId: "s1",
		Update:    acp.UpdateAgentMessageText("world"),
	}); err != nil {
		t.Fatalf("SessionUpdate(world): %v", err)
	}

	got := h.collectedText()
	if got != "hello world" {
		t.Errorf("collectedText() = %q, want 'hello world'", got)
	}
}

func TestSelectBackendACP(t *testing.T) {
	h := &CodergenHandler{
		graphAttrs: map[string]string{},
	}

	t.Run("node attr backend=acp", func(t *testing.T) {
		node := &pipeline.Node{
			ID:    "test",
			Attrs: map[string]string{"backend": "acp"},
		}
		b, err := h.selectBackend(node)
		if err != nil {
			t.Fatal(err)
		}
		if _, ok := b.(*ACPBackend); !ok {
			t.Errorf("expected *ACPBackend, got %T", b)
		}
	})

	t.Run("global default acp", func(t *testing.T) {
		h2 := &CodergenHandler{
			defaultBackendName: "acp",
			graphAttrs:         map[string]string{},
		}
		node := &pipeline.Node{
			ID:    "test",
			Attrs: map[string]string{},
		}
		b, err := h2.selectBackend(node)
		if err != nil {
			t.Fatal(err)
		}
		if _, ok := b.(*ACPBackend); !ok {
			t.Errorf("expected *ACPBackend, got %T", b)
		}
	})

	t.Run("node attr overrides global default", func(t *testing.T) {
		h2 := &CodergenHandler{
			client:             &fakeCompleter{responseText: "ok"},
			defaultBackendName: "acp",
			graphAttrs:         map[string]string{},
		}
		node := &pipeline.Node{
			ID:    "test",
			Attrs: map[string]string{"backend": "native"},
		}
		b, err := h2.selectBackend(node)
		if err != nil {
			t.Fatal(err)
		}
		if _, ok := b.(*NativeBackend); !ok {
			t.Errorf("expected *NativeBackend (node attr should override global), got %T", b)
		}
	})
}

func TestExtractContentText(t *testing.T) {
	t.Run("text block", func(t *testing.T) {
		cb := acp.TextBlock("hello")
		if got := extractContentText(cb); got != "hello" {
			t.Errorf("got %q, want 'hello'", got)
		}
	})

	t.Run("non-text block", func(t *testing.T) {
		cb := acp.ContentBlock{}
		if got := extractContentText(cb); got != "" {
			t.Errorf("got %q, want empty", got)
		}
	})
}

func TestFormatRawInput(t *testing.T) {
	if got := formatRawInput(nil); got != "" {
		t.Errorf("nil: got %q", got)
	}
	if got := formatRawInput("hello"); got != "hello" {
		t.Errorf("string: got %q", got)
	}
	got := formatRawInput(map[string]any{"key": "val"})
	if got != `{"key":"val"}` {
		t.Errorf("map: got %q", got)
	}
}

func TestProviderToAgentMapping(t *testing.T) {
	// Verify the mapping table has expected entries.
	expected := map[string]string{
		"anthropic": "claude-agent-acp",
		"openai":    "codex-acp",
		"gemini":    "gemini",
	}
	for provider, agent := range expected {
		got, ok := providerToAgent[provider]
		if !ok {
			t.Errorf("missing mapping for provider %q", provider)
			continue
		}
		if got != agent {
			t.Errorf("providerToAgent[%q] = %q, want %q", provider, got, agent)
		}
	}
}

func TestACPClientTerminalCleanup(t *testing.T) {
	h := &acpClientHandler{
		emit:       func(agent.Event) {},
		workingDir: t.TempDir(),
		toolNames:  make(map[string]string),
	}

	// Create a long-running terminal.
	resp, err := h.CreateTerminal(context.Background(), acp.CreateTerminalRequest{
		Command:   "sleep",
		Args:      []string{"60"},
		SessionId: "s1",
	})
	if err != nil {
		t.Fatal(err)
	}

	// Verify terminal exists.
	h.mu.Lock()
	_, exists := h.terminals[resp.TerminalId]
	h.mu.Unlock()
	if !exists {
		t.Fatal("terminal not tracked")
	}

	// Cleanup should kill it.
	h.cleanup()

	// Wait briefly for the process to die.
	h.mu.Lock()
	ts := h.terminals[resp.TerminalId]
	h.mu.Unlock()
	select {
	case <-ts.done:
		// Process terminated.
	case <-time.After(2 * time.Second):
		t.Error("cleanup did not kill the terminal process in time")
	}
}

func TestACPClientWriteFileCreatesDir(t *testing.T) {
	tmpDir := t.TempDir()
	h := &acpClientHandler{
		emit:       func(agent.Event) {},
		workingDir: tmpDir,
		toolNames:  make(map[string]string),
	}

	nested := filepath.Join(tmpDir, "a", "b", "c", "file.txt")
	_, err := h.WriteTextFile(context.Background(), acp.WriteTextFileRequest{
		Path:      nested,
		Content:   "nested content",
		SessionId: "s1",
	})
	if err != nil {
		t.Fatal(err)
	}

	data, err := os.ReadFile(nested)
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != "nested content" {
		t.Errorf("unexpected content: %q", string(data))
	}
}

func TestMapModelToBridge(t *testing.T) {
	models := &acp.SessionModelState{
		AvailableModels: []acp.ModelInfo{
			{ModelId: acp.ModelId("sonnet")},
			{ModelId: acp.ModelId("haiku")},
			{ModelId: acp.ModelId("default")},
		},
	}

	tests := []struct {
		tracker string
		want    string
	}{
		{"sonnet", "sonnet"},            // direct match
		{"claude-sonnet-4-6", "sonnet"}, // substring match
		{"claude-haiku-4-5", "haiku"},   // substring match
		{"unknown-model", ""},           // no match
		{"default", "default"},          // direct match on default
	}
	for _, tt := range tests {
		got := mapModelToBridge(tt.tracker, models)
		if got != tt.want {
			t.Errorf("mapModelToBridge(%q) = %q, want %q", tt.tracker, got, tt.want)
		}
	}

	// nil models returns empty
	if got := mapModelToBridge("anything", nil); got != "" {
		t.Errorf("mapModelToBridge with nil models = %q, want empty", got)
	}
}

func TestBuildACPResult(t *testing.T) {
	handler := &acpClientHandler{
		textParts: []string{"hello", " world"},
		toolCount: 2,
		turnCount: 3,
		toolNames: map[string]string{"t1": "bash", "t2": "read"},
	}
	result := buildACPResult(handler, acp.PromptResponse{})
	if result.Turns != 3 {
		t.Errorf("Turns = %d, want 3", result.Turns)
	}
	if result.ToolCalls["bash"] != 1 {
		t.Errorf("ToolCalls[bash] = %d, want 1", result.ToolCalls["bash"])
	}
	if result.ToolCalls["read"] != 1 {
		t.Errorf("ToolCalls[read] = %d, want 1", result.ToolCalls["read"])
	}
}

func TestBuildACPResult_MinOneTurn(t *testing.T) {
	handler := &acpClientHandler{
		textParts: []string{"output"},
		turnCount: 0,
		toolNames: make(map[string]string),
	}
	result := buildACPResult(handler, acp.PromptResponse{})
	if result.Turns != 1 {
		t.Errorf("Turns = %d, want 1 (minimum when text present)", result.Turns)
	}
}

func TestBuildACPMcpServers(t *testing.T) {
	cfg := pipeline.AgentRunConfig{}
	servers := buildACPMcpServers(cfg)
	if servers == nil {
		t.Error("expected non-nil empty slice")
	}
	if len(servers) != 0 {
		t.Errorf("expected empty servers, got %d", len(servers))
	}
}

func TestBuildEnvForACP_PassesByDefault(t *testing.T) {
	t.Setenv("ANTHROPIC_API_KEY", "sk-secret")
	t.Setenv("TRACKER_STRIP_ACP_KEYS", "")

	env := buildEnvForACP()
	envMap := make(map[string]string)
	for _, e := range env {
		parts := strings.SplitN(e, "=", 2)
		if len(parts) == 2 {
			envMap[parts[0]] = parts[1]
		}
	}
	if _, ok := envMap["ANTHROPIC_API_KEY"]; !ok {
		t.Error("ACP default should pass API keys through")
	}
}

func TestBuildEnvForACP_StripsWhenRequested(t *testing.T) {
	t.Setenv("ANTHROPIC_API_KEY", "sk-secret")
	t.Setenv("SAFE_VAR", "keep")
	t.Setenv("TRACKER_STRIP_ACP_KEYS", "1")

	env := buildEnvForACP()
	envMap := make(map[string]string)
	for _, e := range env {
		parts := strings.SplitN(e, "=", 2)
		if len(parts) == 2 {
			envMap[parts[0]] = parts[1]
		}
	}
	if _, ok := envMap["ANTHROPIC_API_KEY"]; ok {
		t.Error("ANTHROPIC_API_KEY should be stripped with TRACKER_STRIP_ACP_KEYS=1")
	}
	if v, ok := envMap["SAFE_VAR"]; !ok || v != "keep" {
		t.Error("SAFE_VAR should be preserved")
	}
}

func TestKillProcess_NilProcess(t *testing.T) {
	// Should not panic on cmd with nil Process.
	cmd := &exec.Cmd{}
	killProcess(cmd) // no-op, should not panic
}

func TestLogStderr_NonEmpty(t *testing.T) {
	var buf bytes.Buffer
	buf.WriteString("some error output\n")
	// logStderr should not panic and should log the output.
	logStderr("test-agent", "initialize", &buf)
}

func TestLogStderr_Empty(t *testing.T) {
	var buf bytes.Buffer
	// logStderr with empty buffer should be a no-op.
	logStderr("test-agent", "prompt", &buf)
}

func TestACPHandler_IsEmpty(t *testing.T) {
	h := &acpClientHandler{toolNames: make(map[string]string)}
	if !h.isEmpty() {
		t.Error("new handler should be empty")
	}
	h.textParts = append(h.textParts, "hello")
	if h.isEmpty() {
		t.Error("handler with text should not be empty")
	}
}

func TestACPHandler_IsEmpty_ToolsOnly(t *testing.T) {
	h := &acpClientHandler{toolNames: make(map[string]string), toolCount: 1}
	if h.isEmpty() {
		t.Error("handler with tool calls should not be empty")
	}
}

func TestWaitForProcess_NormalExit(t *testing.T) {
	// Use a real quick command to test the normal exit path.
	cmd := exec.Command("true")
	stdin, _ := cmd.StdinPipe()
	if err := cmd.Start(); err != nil {
		t.Skipf("cannot start 'true': %v", err)
	}
	forceKilled := waitForProcess(cmd, stdin)
	if forceKilled {
		t.Error("'true' should exit normally, not be force-killed")
	}
}

func TestACPBackend_Run_AgentNotFound(t *testing.T) {
	b := NewACPBackend()
	cfg := pipeline.AgentRunConfig{
		Provider: "anthropic",
		Prompt:   "hello",
	}
	events := []agent.Event{}
	emit := func(e agent.Event) { events = append(events, e) }

	_, err := b.Run(context.Background(), cfg, emit)
	if err == nil {
		t.Fatal("expected error when agent binary not found")
	}
	if !strings.Contains(err.Error(), "not found in PATH") {
		t.Errorf("error = %q, want 'not found in PATH'", err)
	}
}

func TestACPBackend_StartProcess_BadPath(t *testing.T) {
	b := NewACPBackend()
	_, err := b.startProcess(context.Background(), "/nonexistent/binary", "test-agent", t.TempDir())
	if err == nil {
		t.Fatal("expected error for nonexistent binary")
	}
}

func TestSetSessionModel_NoMatch(t *testing.T) {
	// setSessionModel with no matching bridge model should log and return.
	// We can't easily test the conn interaction, but we can verify it
	// doesn't panic with a nil models response.
	sessResp := acp.NewSessionResponse{
		SessionId: "s1",
		Models:    nil,
	}
	// This should just log "skipping SetSessionModel" and return.
	setSessionModel(context.Background(), nil, sessResp, "test", "unknown-model")
}

func TestBuildEnvForACP_StripsAllPrefixes(t *testing.T) {
	// Test all stripped prefixes
	t.Setenv("ANTHROPIC_API_KEY", "x")
	t.Setenv("OPENAI_API_KEY", "x")
	t.Setenv("OPENAI_COMPAT_API_KEY", "x")
	t.Setenv("GEMINI_API_KEY", "x")
	t.Setenv("GOOGLE_API_KEY", "x")
	t.Setenv("ANTHROPIC_BASE_URL", "x")
	t.Setenv("OPENAI_BASE_URL", "x")
	t.Setenv("OPENAI_COMPAT_BASE_URL", "x")
	t.Setenv("GEMINI_BASE_URL", "x")
	t.Setenv("GOOGLE_BASE_URL", "x")
	t.Setenv("TRACKER_STRIP_ACP_KEYS", "1")

	env := buildEnvForACP()
	for _, e := range env {
		name := strings.SplitN(e, "=", 2)[0]
		for _, prefix := range acpStrippedPrefixes {
			stripped := strings.TrimSuffix(prefix, "=")
			if name == stripped {
				t.Errorf("%s should be stripped", name)
			}
		}
	}
}

func TestACPCollectedTextEmpty(t *testing.T) {
	h := &acpClientHandler{
		textParts: nil,
		toolNames: make(map[string]string),
	}
	if got := h.collectedText(); got != "" {
		t.Errorf("collectedText() = %q, want empty", got)
	}
}

func TestBuildACPResult_EmptyHandler(t *testing.T) {
	handler := &acpClientHandler{
		toolNames: make(map[string]string),
	}
	result := buildACPResult(handler, acp.PromptResponse{})
	if result.Turns != 0 {
		t.Errorf("Turns = %d, want 0 for empty handler", result.Turns)
	}
}

func TestResolveAgentName_ProviderMapping(t *testing.T) {
	b := NewACPBackend()
	tests := []struct {
		provider string
		want     string
	}{
		{"anthropic", "claude-agent-acp"},
		{"openai", "codex-acp"},
		{"gemini", "gemini"},
		{"unknown", "claude-agent-acp"}, // default fallback
	}
	for _, tt := range tests {
		cfg := pipeline.AgentRunConfig{Provider: tt.provider}
		got := b.resolveAgentName(cfg)
		if got != tt.want {
			t.Errorf("resolveAgentName(provider=%q) = %q, want %q", tt.provider, got, tt.want)
		}
	}
}

func TestValidatePathInWorkDir(t *testing.T) {
	tests := []struct {
		name    string
		path    string
		workDir string
		wantErr bool
	}{
		{"inside workdir", "/home/user/project/file.go", "/home/user/project", false},
		{"workdir itself", "/home/user/project", "/home/user/project", false},
		{"outside workdir", "/etc/passwd", "/home/user/project", true},
		{"traversal attempt", "/home/user/project/../../../etc/passwd", "/home/user/project", true},
		{"empty workdir allows all", "/etc/passwd", "", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := validatePathInWorkDir(tt.path, tt.workDir)
			if (err != nil) != tt.wantErr {
				t.Errorf("validatePathInWorkDir(%q, %q) err=%v, wantErr=%v", tt.path, tt.workDir, err, tt.wantErr)
			}
		})
	}
}

func TestApplyLineFilter_NegativeValues(t *testing.T) {
	content := "line1\nline2\nline3\nline4\nline5"

	// Negative line should not panic
	negLine := -5
	result := applyLineFilter(content, &negLine, nil)
	if result != content {
		t.Errorf("negative line: got %q, want full content", result)
	}

	// Negative limit should not panic, treated as no limit
	negLimit := -1
	result = applyLineFilter(content, nil, &negLimit)
	if result != content {
		t.Errorf("negative limit: got %q, want full content", result)
	}

	// Zero limit should not panic
	zeroLimit := 0
	result = applyLineFilter(content, nil, &zeroLimit)
	if result != content {
		t.Errorf("zero limit: got %q, want full content", result)
	}
}

func TestReadTextFile_RejectsPathOutsideWorkDir(t *testing.T) {
	h := &acpClientHandler{
		workingDir: t.TempDir(),
		toolNames:  make(map[string]string),
	}
	_, err := h.ReadTextFile(context.Background(), acp.ReadTextFileRequest{
		Path: "/etc/passwd",
	})
	if err == nil {
		t.Fatal("expected error for path outside workdir")
	}
	if !strings.Contains(err.Error(), "outside working directory") {
		t.Errorf("error = %q, want 'outside working directory'", err)
	}
}
