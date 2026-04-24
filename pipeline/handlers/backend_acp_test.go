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
	result := buildACPResult(handler, acp.PromptResponse{}, pipeline.AgentRunConfig{})
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
	result := buildACPResult(handler, acp.PromptResponse{}, pipeline.AgentRunConfig{})
	if result.Turns != 1 {
		t.Errorf("Turns = %d, want 1 (minimum when text present)", result.Turns)
	}
}

func TestEstimateACPUsage(t *testing.T) {
	tests := []struct {
		name            string
		cfg             pipeline.AgentRunConfig
		counts          acpRuneCounts
		wantInputTokens int
		wantOutputTok   int
		wantEstimated   bool
		wantZeroTokens  bool
	}{
		{
			name:            "populates estimate and marker from rune counts",
			cfg:             pipeline.AgentRunConfig{Prompt: strings.Repeat("a", 40), SystemPrompt: strings.Repeat("b", 40)},
			counts:          acpRuneCounts{MessageOutput: 400},
			wantInputTokens: 20,  // ceil(80/4) = 20
			wantOutputTok:   100, // ceil(400/4) = 100
			wantEstimated:   true,
		},
		{
			name:           "nothing in, nothing out → zero usage",
			cfg:            pipeline.AgentRunConfig{},
			counts:         acpRuneCounts{},
			wantZeroTokens: true,
		},
		{
			name:            "prompt only, empty output — still estimated",
			cfg:             pipeline.AgentRunConfig{Prompt: strings.Repeat("x", 100)},
			counts:          acpRuneCounts{},
			wantInputTokens: 25, // ceil(100/4) = 25
			wantOutputTok:   0,
			wantEstimated:   true,
		},
		{
			// Regression: floor division produced 0 tokens for short non-empty
			// prompts, causing trackExternalBackendUsage to skip the session.
			name:            "short prompt clamps to ≥1 token instead of floor-rounding to 0",
			cfg:             pipeline.AgentRunConfig{Prompt: "hi"},
			counts:          acpRuneCounts{MessageOutput: 3},
			wantInputTokens: 1, // ceil(2/4) = 1
			wantOutputTok:   1, // ceil(3/4) = 1
			wantEstimated:   true,
		},
		{
			// Regression: len() counts bytes, not runes — would overcount CJK 3x.
			// 11 Japanese runes = 33 UTF-8 bytes; with rune counting we get
			// ceil(11/4) = 3 tokens, not ceil(33/4) = 9.
			name:            "multibyte UTF-8 counted by runes, not bytes",
			cfg:             pipeline.AgentRunConfig{Prompt: "こんにちは世界です。え"}, // 11 runes, 33 bytes
			counts:          acpRuneCounts{},
			wantInputTokens: 3, // ceil(11/4) = 3
			wantOutputTok:   0,
			wantEstimated:   true,
		},
		{
			// Reasoning counts as output (priced at output rate today) AND
			// is exposed via Usage.ReasoningTokens for future per-reasoning
			// pricing. Tool-arg runes (LLM-produced invocations) also fold
			// into output.
			name:            "reasoning + tool args fold into output side, populate ReasoningTokens",
			cfg:             pipeline.AgentRunConfig{Prompt: strings.Repeat("p", 40)},
			counts:          acpRuneCounts{MessageOutput: 40, Reasoning: 80, ToolArgs: 40},
			wantInputTokens: 10, // ceil(40/4) = 10
			wantOutputTok:   40, // ceil((40+80+40)/4) = 40
			wantEstimated:   true,
		},
		{
			// Tool results fold into the INPUT side — the bridge re-sends
			// tool output as next-turn input context.
			name:            "tool results fold into input side",
			cfg:             pipeline.AgentRunConfig{Prompt: strings.Repeat("p", 40)},
			counts:          acpRuneCounts{MessageOutput: 40, ToolResults: 200},
			wantInputTokens: 60, // ceil((40+200)/4) = 60
			wantOutputTok:   10, // ceil(40/4) = 10
			wantEstimated:   true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := estimateACPUsage(tt.cfg, tt.counts)
			if tt.wantZeroTokens {
				if got.TotalTokens != 0 || got.InputTokens != 0 || got.OutputTokens != 0 {
					t.Errorf("want zero-token usage, got %+v", got)
				}
				if got.Raw != nil {
					t.Errorf("want nil Raw for zero usage, got %+v", got.Raw)
				}
				return
			}
			if got.InputTokens != tt.wantInputTokens {
				t.Errorf("InputTokens = %d, want %d", got.InputTokens, tt.wantInputTokens)
			}
			if got.OutputTokens != tt.wantOutputTok {
				t.Errorf("OutputTokens = %d, want %d", got.OutputTokens, tt.wantOutputTok)
			}
			if got.TotalTokens != tt.wantInputTokens+tt.wantOutputTok {
				t.Errorf("TotalTokens = %d, want %d", got.TotalTokens, tt.wantInputTokens+tt.wantOutputTok)
			}
			marker, ok := got.Raw.(ACPUsageMarker)
			if !ok {
				t.Fatalf("Raw = %T, want ACPUsageMarker", got.Raw)
			}
			if marker.Estimated != tt.wantEstimated {
				t.Errorf("marker.Estimated = %v, want %v", marker.Estimated, tt.wantEstimated)
			}
			if marker.Source != "acp-chars-heuristic" {
				t.Errorf("marker.Source = %q, want acp-chars-heuristic", marker.Source)
			}
			if marker.Ratio != 4 {
				t.Errorf("marker.Ratio = %d, want 4", marker.Ratio)
			}
		})
	}
}

// TestEstimateACPUsage_CacheReadRatio pins the #185 Track B optional
// tuning knob: TRACKER_ACP_CACHE_READ_RATIO, when set to a value in
// (0, 1], splits the estimated input tokens between fresh InputTokens
// and CacheReadTokens. Unset, zero, or out-of-range → no split and
// all input is priced as fresh (conservative default — never
// under-reports spend). llm.EstimateCost prices CacheReadTokens at
// 10% of the input rate, so this lets operators running stable-context
// Claude workloads dial in a more realistic cost estimate.
func TestEstimateACPUsage_CacheReadRatio(t *testing.T) {
	tests := []struct {
		name         string
		envVal       string
		inputRunes   int // routed via cfg.Prompt
		wantFresh    int
		wantCacheRd  int
		wantCacheSet bool
	}{
		{
			name:         "default (unset) — all fresh",
			envVal:       "",
			inputRunes:   400,
			wantFresh:    100, // ceil(400/4)
			wantCacheRd:  0,
			wantCacheSet: false,
		},
		{
			name:         "80% cache-read splits input",
			envVal:       "0.8",
			inputRunes:   400,
			wantFresh:    20, // 100 - int(100*0.8) = 100 - 80
			wantCacheRd:  80,
			wantCacheSet: true,
		},
		{
			name:         "50% cache-read splits input",
			envVal:       "0.5",
			inputRunes:   400,
			wantFresh:    50,
			wantCacheRd:  50,
			wantCacheSet: true,
		},
		{
			name:         "100% cache-read routes all input to cache",
			envVal:       "1.0",
			inputRunes:   400,
			wantFresh:    0,
			wantCacheRd:  100,
			wantCacheSet: true,
		},
		{
			name:         "negative — ignored, fall back to no split",
			envVal:       "-0.3",
			inputRunes:   400,
			wantFresh:    100,
			wantCacheRd:  0,
			wantCacheSet: false,
		},
		{
			name:         "> 1 — ignored",
			envVal:       "1.5",
			inputRunes:   400,
			wantFresh:    100,
			wantCacheRd:  0,
			wantCacheSet: false,
		},
		{
			name:         "non-numeric — ignored",
			envVal:       "eighty-percent",
			inputRunes:   400,
			wantFresh:    100,
			wantCacheRd:  0,
			wantCacheSet: false,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Setenv(acpCacheReadRatioEnv, tt.envVal)
			cfg := pipeline.AgentRunConfig{Prompt: strings.Repeat("p", tt.inputRunes)}
			usage := estimateACPUsage(cfg, acpRuneCounts{MessageOutput: 4})
			if usage.InputTokens != tt.wantFresh {
				t.Errorf("InputTokens = %d, want %d", usage.InputTokens, tt.wantFresh)
			}
			if tt.wantCacheSet {
				if usage.CacheReadTokens == nil {
					t.Fatalf("CacheReadTokens = nil; want *%d", tt.wantCacheRd)
				}
				if *usage.CacheReadTokens != tt.wantCacheRd {
					t.Errorf("CacheReadTokens = %d, want %d", *usage.CacheReadTokens, tt.wantCacheRd)
				}
			} else if usage.CacheReadTokens != nil {
				t.Errorf("CacheReadTokens = *%d; want nil for no-split case", *usage.CacheReadTokens)
			}
			// TotalTokens always covers the full billable footprint: fresh
			// input + cache read + output. This is what budget guards
			// compare against.
			wantTotal := tt.wantFresh + tt.wantCacheRd + 1 // ceil(4/4) = 1 output token
			if usage.TotalTokens != wantTotal {
				t.Errorf("TotalTokens = %d, want %d", usage.TotalTokens, wantTotal)
			}
		})
	}
}

// TestACPHandler_AccumulatesChannelRunes pins that the acpClientHandler's
// three new rune counters (reasoningRunes, toolArgRunes, toolResultRunes)
// advance when the corresponding SessionUpdate types fire. Without this,
// a refactor that silently drops one of the append paths would be invisible
// to the unit tests above, which consume counts directly.
func TestACPHandler_AccumulatesChannelRunes(t *testing.T) {
	h := &acpClientHandler{
		emit:      func(agent.Event) {},
		toolNames: make(map[string]string),
	}

	thoughtText := strings.Repeat("t", 40)
	toolArgs := map[string]any{"cmd": strings.Repeat("c", 50)}
	toolResult := strings.Repeat("r", 120)

	_ = h.SessionUpdate(context.Background(), acp.SessionNotification{
		Update: acp.UpdateAgentThoughtText(thoughtText),
	})
	_ = h.SessionUpdate(context.Background(), acp.SessionNotification{
		Update: acp.StartToolCall("t1", "bash", acp.WithStartRawInput(toolArgs)),
	})
	_ = h.SessionUpdate(context.Background(), acp.SessionNotification{
		Update: acp.UpdateToolCall("t1",
			acp.WithUpdateStatus(acp.ToolCallStatusCompleted),
			acp.WithUpdateContent([]acp.ToolCallContent{acp.ToolContent(acp.TextBlock(toolResult))}),
		),
	})

	h.mu.Lock()
	defer h.mu.Unlock()
	if h.reasoningRunes != 40 {
		t.Errorf("reasoningRunes = %d, want 40", h.reasoningRunes)
	}
	if h.toolArgRunes == 0 {
		t.Error("toolArgRunes = 0; want non-zero from JSON-formatted args")
	}
	if h.toolResultRunes != 120 {
		t.Errorf("toolResultRunes = %d, want 120", h.toolResultRunes)
	}
}

// TestCountToolResultRunes_CombinedContentAndRawOutput pins that the
// billing-path helper counts BOTH Content blocks AND RawOutput when both
// are present, rather than treating RawOutput as a fallback-only field.
// extractToolCallOutput (the display helper) drops RawOutput when Content
// is non-empty; using that for billing would silently undercount any ACP
// update that ships structured content + a larger raw payload.
func TestCountToolResultRunes_CombinedContentAndRawOutput(t *testing.T) {
	content := []acp.ToolCallContent{acp.ToolContent(acp.TextBlock(strings.Repeat("c", 40)))}
	rawOutput := map[string]any{"stdout": strings.Repeat("r", 100)}

	got := countToolResultRunes(content, rawOutput)

	// Content contributes 40 runes; rawOutput JSON-serializes to
	// `{"stdout":"rrrrr..."}` which is 100 + ~12 framing characters.
	// Require at minimum both sources together, i.e. strictly > 100.
	if got <= 100 {
		t.Errorf("countToolResultRunes = %d; want > 100 (must include both Content ≈40 and RawOutput ≈112)", got)
	}
	if got < 140 {
		t.Errorf("countToolResultRunes = %d; want ≥ 140 (~40 from Content + ~100 from RawOutput JSON body)", got)
	}
}

// TestCountToolCallContentRunes_DiffCountsFullText pins that a diff content
// item contributes its full NewText + OldText + Path rune count — not just
// "diff <path>" as the display formatter would produce. Diffs are a common
// tool-call shape for editing workflows, and the pre-fix code collapsed
// them to a label, silently missing the actual byte volume that round-trips
// through the model.
func TestCountToolCallContentRunes_DiffCountsFullText(t *testing.T) {
	newText := strings.Repeat("n", 300)
	oldText := strings.Repeat("o", 200)
	diff := acp.ToolDiffContent("some/long/path.go", newText, oldText)
	got := countToolCallContentRunes(diff)

	// NewText 300 + OldText 200 + Path 17 = 517.
	if got != 517 {
		t.Errorf("countToolCallContentRunes = %d; want 517 (NewText 300 + OldText 200 + Path 17) — not just the \"diff <path>\" label", got)
	}
}

// TestBuildACPResult_CountsAllChannels threads the handler-level accumulation
// through buildACPResult and asserts the resulting Usage reflects reasoning
// + tool args on the output side, tool results on the input side, and
// Usage.ReasoningTokens is populated. Previously (#184) these channels were
// silently ignored — a multi-turn tool-heavy session would report a token
// total that missed tens of thousands of real tokens.
func TestBuildACPResult_CountsAllChannels(t *testing.T) {
	h := &acpClientHandler{
		textParts:       []string{strings.Repeat("m", 40)},
		reasoningRunes:  80,
		toolArgRunes:    40,
		toolResultRunes: 200,
		turnCount:       2,
		toolNames:       map[string]string{"t1": "bash"},
	}
	cfg := pipeline.AgentRunConfig{
		Prompt:       strings.Repeat("p", 40),
		SystemPrompt: strings.Repeat("s", 40),
		Model:        "claude-sonnet-4-5",
	}
	result := buildACPResult(h, acp.PromptResponse{}, cfg)

	// input = (prompt 40 + systemPrompt 40 + toolResults 200) / 4 = 70
	// output = (message 40 + reasoning 80 + toolArgs 40) / 4 = 40
	if result.Usage.InputTokens != 70 {
		t.Errorf("InputTokens = %d, want 70 (prompt + systemPrompt + tool results)", result.Usage.InputTokens)
	}
	if result.Usage.OutputTokens != 40 {
		t.Errorf("OutputTokens = %d, want 40 (message + reasoning + tool args)", result.Usage.OutputTokens)
	}
	if result.Usage.ReasoningTokens == nil {
		t.Fatal("ReasoningTokens is nil; want populated from reasoningRunes")
	}
	if *result.Usage.ReasoningTokens != 20 {
		t.Errorf("ReasoningTokens = %d, want 20 (ceil(80/4))", *result.Usage.ReasoningTokens)
	}
}

func TestBuildACPResult_PopulatesUsage(t *testing.T) {
	handler := &acpClientHandler{
		textParts: []string{"the quick brown fox", " jumps over the lazy dog"},
		turnCount: 1,
		toolNames: make(map[string]string),
	}
	cfg := pipeline.AgentRunConfig{Prompt: "tell me a story about a dog", SystemPrompt: "be concise"}
	result := buildACPResult(handler, acp.PromptResponse{}, cfg)
	if result.Provider != "acp" {
		t.Errorf("Provider = %q, want %q (otherwise AggregateUsage buckets ACP under 'unknown')", result.Provider, "acp")
	}
	if result.Usage.TotalTokens == 0 {
		t.Fatal("expected non-zero estimated Usage on a session with prompt + output")
	}
	if _, ok := result.Usage.Raw.(ACPUsageMarker); !ok {
		t.Fatalf("Usage.Raw = %T, want ACPUsageMarker so downstream consumers can flag estimates", result.Usage.Raw)
	}
}

// TestACPUsage_DownstreamPropagation walks an ACP session's estimated usage
// through the same path the engine uses at runtime —
// buildACPResult → buildSessionStats → pipeline.Trace → AggregateUsage — and
// asserts (a) the usage lands in ProviderTotals["acp"] (not "unknown"), and
// (b) both tokens and dollar cost survive the round-trip. This is the
// integration test that distinguishes "the estimator works in a unit test"
// from "a real pipeline actually attributes ACP spend correctly".
func TestACPUsage_DownstreamPropagation(t *testing.T) {
	handler := &acpClientHandler{
		textParts: []string{"the quick brown fox jumps over the lazy dog"},
		turnCount: 2,
		toolNames: make(map[string]string),
	}
	cfg := pipeline.AgentRunConfig{
		Prompt:       "tell me a story about a dog in 30 words",
		SystemPrompt: "be concise",
		Model:        "claude-sonnet-4-5", // real catalog entry so EstimatedCost is non-zero
	}

	result := buildACPResult(handler, acp.PromptResponse{}, cfg)
	if result.Provider != "acp" {
		t.Fatalf("Provider = %q, want \"acp\"", result.Provider)
	}
	if result.Usage.TotalTokens == 0 {
		t.Fatal("expected non-zero estimated tokens")
	}
	if result.Usage.EstimatedCost == 0 {
		t.Fatal("expected non-zero EstimatedCost for a known catalog model")
	}

	stats := buildSessionStats(result)
	if stats.Provider != "acp" {
		t.Errorf("SessionStats.Provider = %q, want \"acp\" (buildSessionStats must preserve Provider)", stats.Provider)
	}
	if stats.TotalTokens != result.Usage.TotalTokens {
		t.Errorf("SessionStats.TotalTokens = %d, want %d", stats.TotalTokens, result.Usage.TotalTokens)
	}
	if stats.CostUSD != result.Usage.EstimatedCost {
		t.Errorf("SessionStats.CostUSD = %f, want %f", stats.CostUSD, result.Usage.EstimatedCost)
	}

	trace := &pipeline.Trace{
		Entries: []pipeline.TraceEntry{{
			NodeID:      "ACPNode",
			HandlerName: "codergen",
			Status:      "success",
			Stats:       stats,
		}},
	}
	summary := trace.AggregateUsage()
	if summary == nil {
		t.Fatal("AggregateUsage returned nil")
	}
	acp, ok := summary.ProviderTotals["acp"]
	if !ok {
		t.Fatalf("ProviderTotals missing \"acp\" bucket; keys = %v", keysOfProviderTotals(summary.ProviderTotals))
	}
	if _, hasUnknown := summary.ProviderTotals["unknown"]; hasUnknown {
		t.Errorf("ProviderTotals contains \"unknown\" bucket — SessionStats.Provider must carry through")
	}
	if acp.TotalTokens != result.Usage.TotalTokens {
		t.Errorf("ProviderTotals[acp].TotalTokens = %d, want %d", acp.TotalTokens, result.Usage.TotalTokens)
	}
	if acp.CostUSD != result.Usage.EstimatedCost {
		t.Errorf("ProviderTotals[acp].CostUSD = %f, want %f", acp.CostUSD, result.Usage.EstimatedCost)
	}
	if acp.SessionCount != 1 {
		t.Errorf("ProviderTotals[acp].SessionCount = %d, want 1", acp.SessionCount)
	}
}

func keysOfProviderTotals(m map[string]pipeline.ProviderUsage) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	return keys
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
	// Use a nonexistent agent name so LookPath fails even if
	// claude-agent-acp is installed on the host.
	t.Setenv("PATH", t.TempDir()) // empty dir — no binaries found

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
	result := buildACPResult(handler, acp.PromptResponse{}, pipeline.AgentRunConfig{})
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

func TestValidatePathInWorkDir_SymlinkEscape(t *testing.T) {
	// Create a real directory structure with a symlink that could escape.
	realDir := t.TempDir()
	outsideDir := t.TempDir()
	workDir := filepath.Join(realDir, "workspace")
	if err := os.Mkdir(workDir, 0o755); err != nil {
		t.Fatalf("mkdir workDir: %v", err)
	}

	// Create a symlink inside workDir that points outside. Skip the test
	// (rather than fail) if the environment doesn't support symlinks —
	// e.g., some Windows CI runners.
	if err := os.Symlink(outsideDir, filepath.Join(workDir, "escape")); err != nil {
		t.Skipf("symlink not available in this environment: %v", err)
	}

	// A file directly under the symlinked dir should be outside workDir
	// after symlink resolution.
	err := validatePathInWorkDir(filepath.Join(workDir, "escape", "secret.txt"), workDir)
	if err == nil {
		t.Error("expected error for path through symlink pointing outside workDir")
	}

	// symlink/../sibling should be rejected (.. after symlink).
	// Build path manually — filepath.Join collapses ".." lexically.
	escapePath := workDir + "/escape/../sibling/file.txt"
	err = validatePathInWorkDir(escapePath, workDir)
	if err == nil {
		t.Error("expected error for symlink/../ traversal")
	}
}

func TestValidatePathInWorkDir_NestedNonExistent(t *testing.T) {
	// Deeply nested non-existent paths should be accepted when under workDir.
	workDir := t.TempDir()
	err := validatePathInWorkDir(filepath.Join(workDir, "a", "b", "c", "file.txt"), workDir)
	if err != nil {
		t.Errorf("expected deeply nested path under workDir to be valid, got: %v", err)
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
