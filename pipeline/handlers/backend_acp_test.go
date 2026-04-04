// ABOUTME: Tests for ACPBackend agent name resolution, config building, and event translation.
// ABOUTME: Covers provider mapping, explicit overrides, session update conversion, and file operations.
package handlers

import (
	"context"
	"os"
	"path/filepath"
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
			name:     "openai maps to codex-agent-acp",
			cfg:      pipeline.AgentRunConfig{Provider: "openai"},
			expected: "codex-agent-acp",
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
		"openai":    "codex-agent-acp",
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
