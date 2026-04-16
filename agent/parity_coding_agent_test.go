package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/2389-research/tracker/agent/exec"
	"github.com/2389-research/tracker/agent/tools"
	"github.com/2389-research/tracker/llm"
)

type inspectingCompleter struct {
	requests  []*llm.Request
	responses []*llm.Response
}

func (c *inspectingCompleter) Complete(ctx context.Context, req *llm.Request) (*llm.Response, error) {
	c.requests = append(c.requests, req)
	if len(c.responses) == 0 {
		return &llm.Response{
			Message:      llm.AssistantMessage("done"),
			FinishReason: llm.FinishReason{Reason: "stop"},
		}, nil
	}
	resp := c.responses[0]
	c.responses = c.responses[1:]
	return resp, nil
}

func TestParityUnknownToolReturnsErrorResultNotSessionFailure(t *testing.T) {
	client := &inspectingCompleter{
		responses: []*llm.Response{
			{
				Message: llm.Message{
					Role: llm.RoleAssistant,
					Content: []llm.ContentPart{{
						Kind: llm.KindToolCall,
						ToolCall: &llm.ToolCallData{
							ID:        "call_1",
							Name:      "missing_tool",
							Arguments: json.RawMessage(`{}`),
						},
					}},
				},
				FinishReason: llm.FinishReason{Reason: "tool_calls"},
			},
			{
				Message:      llm.AssistantMessage("I handled the missing tool error."),
				FinishReason: llm.FinishReason{Reason: "stop"},
			},
		},
	}

	sess := mustNewSession(t, client, DefaultConfig())
	result, err := sess.Run(context.Background(), "Call a missing tool and recover")
	if err != nil {
		t.Fatalf("session should not fail on unknown tool: %v", err)
	}
	if result.Turns != 2 {
		t.Fatalf("turns = %d, want 2", result.Turns)
	}
	if len(client.requests) < 2 {
		t.Fatalf("expected 2 requests, got %d", len(client.requests))
	}

	var sawErrorResult bool
	for _, msg := range client.requests[1].Messages {
		for _, part := range msg.Content {
			if part.Kind == llm.KindToolResult && part.ToolResult != nil && part.ToolResult.IsError {
				if strings.Contains(part.ToolResult.Content, "unknown tool") {
					sawErrorResult = true
				}
			}
		}
	}
	if !sawErrorResult {
		t.Fatal("expected unknown tool to be returned as an error tool result")
	}
}

func TestParitySessionRunsToolLoopUntilNaturalCompletion(t *testing.T) {
	client := &inspectingCompleter{
		responses: []*llm.Response{
			{
				Message: llm.Message{
					Role: llm.RoleAssistant,
					Content: []llm.ContentPart{{
						Kind: llm.KindToolCall,
						ToolCall: &llm.ToolCallData{
							ID:        "call_1",
							Name:      "echo_tool",
							Arguments: json.RawMessage(`{}`),
						},
					}},
				},
				FinishReason: llm.FinishReason{Reason: "tool_calls"},
			},
			{
				Message:      llm.AssistantMessage("All done."),
				FinishReason: llm.FinishReason{Reason: "stop"},
			},
		},
	}

	tool := &stubTool{name: "echo_tool", output: "ok"}
	sess := mustNewSession(t, client, DefaultConfig(), WithTools(tool))
	result, err := sess.Run(context.Background(), "Use a tool and finish")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Turns != 2 {
		t.Fatalf("turns = %d, want 2", result.Turns)
	}
	if result.MaxTurnsUsed {
		t.Fatal("expected natural completion, not turn exhaustion")
	}
}

var _ tools.Tool = (*stubTool)(nil)

type errorTool struct {
	name string
	err  error
}

func (e *errorTool) Name() string        { return e.name }
func (e *errorTool) Description() string { return "tool that fails" }
func (e *errorTool) Parameters() json.RawMessage {
	return json.RawMessage(`{"type":"object","properties":{}}`)
}
func (e *errorTool) Execute(_ context.Context, _ json.RawMessage) (string, error) {
	return "", e.err
}

type steeringTool struct {
	name string
	ch   chan<- string
}

func (s *steeringTool) Name() string        { return s.name }
func (s *steeringTool) Description() string { return "tool that queues steering" }
func (s *steeringTool) Parameters() json.RawMessage {
	return json.RawMessage(`{"type":"object","properties":{}}`)
}
func (s *steeringTool) Execute(_ context.Context, _ json.RawMessage) (string, error) {
	s.ch <- "first steering"
	s.ch <- "second steering"
	return "tool completed", nil
}

func TestParityToolExecutionErrorsBecomeNamedErrorResults(t *testing.T) {
	client := &inspectingCompleter{
		responses: []*llm.Response{
			{
				Message: llm.Message{
					Role: llm.RoleAssistant,
					Content: []llm.ContentPart{{
						Kind: llm.KindToolCall,
						ToolCall: &llm.ToolCallData{
							ID:        "call_1",
							Name:      "broken_tool",
							Arguments: json.RawMessage(`{}`),
						},
					}},
				},
				FinishReason: llm.FinishReason{Reason: "tool_calls"},
			},
			{
				Message:      llm.AssistantMessage("Recovered from tool failure."),
				FinishReason: llm.FinishReason{Reason: "stop"},
			},
		},
	}

	sess := mustNewSession(t, client, DefaultConfig(), WithTools(&errorTool{name: "broken_tool", err: fmt.Errorf("boom")}))
	result, err := sess.Run(context.Background(), "Run the broken tool")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Turns != 2 {
		t.Fatalf("turns = %d, want 2", result.Turns)
	}

	// Find the tool result message in the second request (reflection prompt may follow it).
	var toolResult *llm.ToolResultData
	for _, msg := range client.requests[1].Messages {
		if msg.Role != llm.RoleTool {
			continue
		}
		if len(msg.Content) == 1 && msg.Content[0].ToolResult != nil {
			toolResult = msg.Content[0].ToolResult
		}
	}
	if toolResult == nil {
		t.Fatal("expected exactly one tool result in second request")
	}
	if !toolResult.IsError {
		t.Fatal("expected tool result to be marked as error")
	}
	if toolResult.Content != "Tool error (broken_tool): boom" {
		t.Fatalf("tool error content = %q", toolResult.Content)
	}
}

func TestParitySteeringDrainsQueuedMessagesBetweenToolRounds(t *testing.T) {
	ch := make(chan string, 2)
	client := &inspectingCompleter{
		responses: []*llm.Response{
			{
				Message: llm.Message{
					Role: llm.RoleAssistant,
					Content: []llm.ContentPart{{
						Kind: llm.KindToolCall,
						ToolCall: &llm.ToolCallData{
							ID:        "call_1",
							Name:      "queue_steering",
							Arguments: json.RawMessage(`{}`),
						},
					}},
				},
				FinishReason: llm.FinishReason{Reason: "tool_calls"},
			},
			{
				Message:      llm.AssistantMessage("Done after steering."),
				FinishReason: llm.FinishReason{Reason: "stop"},
			},
		},
	}

	sess := mustNewSession(t, client, DefaultConfig(), WithSteering(ch), WithTools(&steeringTool{name: "queue_steering", ch: ch}))
	result, err := sess.Run(context.Background(), "Do the task")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Turns != 2 {
		t.Fatalf("turns = %d, want 2", result.Turns)
	}
	if len(client.requests) != 2 {
		t.Fatalf("requests = %d, want 2", len(client.requests))
	}

	var steeringMessages []string
	for _, msg := range client.requests[1].Messages {
		if msg.Role == llm.RoleUser && strings.HasPrefix(msg.Text(), "[STEERING] ") {
			steeringMessages = append(steeringMessages, strings.TrimPrefix(msg.Text(), "[STEERING] "))
		}
	}
	if len(steeringMessages) != 2 {
		t.Fatalf("steering message count = %d, want 2 (%v)", len(steeringMessages), steeringMessages)
	}
	if steeringMessages[0] != "first steering" || steeringMessages[1] != "second steering" {
		t.Fatalf("steering messages = %v", steeringMessages)
	}
}

func TestParitySessionToolOutputLimitOverride(t *testing.T) {
	client := &inspectingCompleter{
		responses: []*llm.Response{
			{
				Message: llm.Message{
					Role: llm.RoleAssistant,
					Content: []llm.ContentPart{{
						Kind: llm.KindToolCall,
						ToolCall: &llm.ToolCallData{
							ID:        "call_1",
							Name:      "read",
							Arguments: json.RawMessage(`{}`),
						},
					}},
				},
				FinishReason: llm.FinishReason{Reason: "tool_calls"},
			},
			{
				Message:      llm.AssistantMessage("Done."),
				FinishReason: llm.FinishReason{Reason: "stop"},
			},
		},
	}

	cfg := DefaultConfig()
	cfg.ToolOutputLimits = map[string]int{"read": 100}
	longRead := strings.Repeat("r", 1000)
	sess := mustNewSession(t, client, cfg, WithTools(&stubTool{name: "read", output: longRead}))
	_, err := sess.Run(context.Background(), "Read a large file")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	lastMsg := client.requests[1].Messages[len(client.requests[1].Messages)-1]
	if len(lastMsg.Content) != 1 || lastMsg.Content[0].ToolResult == nil {
		t.Fatal("expected one tool result in second request")
	}
	content := lastMsg.Content[0].ToolResult.Content
	if !strings.HasPrefix(content, "[... truncated") {
		prefix := content
		if len(prefix) > 40 {
			prefix = prefix[:40]
		}
		t.Fatalf("expected overridden read limit to truncate output, got %q", prefix)
	}
}

func TestParityProviderProfilesExposeProviderAlignedToolsets(t *testing.T) {
	testCases := []struct {
		name        string
		provider    string
		model       string
		wantPresent string
		wantMissing string
	}{
		{
			name:        "openai provider gets apply_patch",
			provider:    "openai",
			wantPresent: "apply_patch",
			wantMissing: "edit",
		},
		{
			name:        "anthropic provider keeps edit",
			provider:    "anthropic",
			wantPresent: "edit",
			wantMissing: "apply_patch",
		},
		{
			name:        "model catalog infers google profile",
			model:       "gemini-3-pro-preview",
			wantPresent: "edit",
			wantMissing: "apply_patch",
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			client := &inspectingCompleter{}
			cfg := DefaultConfig()
			cfg.Provider = tc.provider
			cfg.Model = tc.model

			sess := mustNewSession(t, client, cfg, WithEnvironment(exec.NewLocalEnvironment(t.TempDir())))
			if _, err := sess.Run(context.Background(), "inspect tool profile"); err != nil {
				t.Fatalf("Run failed: %v", err)
			}
			if len(client.requests) == 0 {
				t.Fatal("expected at least one LLM request")
			}

			toolNames := toolDefinitionNames(client.requests[0].Tools)
			if !containsString(toolNames, tc.wantPresent) {
				t.Fatalf("tools = %v, want %q to be present", toolNames, tc.wantPresent)
			}
			if containsString(toolNames, tc.wantMissing) {
				t.Fatalf("tools = %v, want %q to be absent", toolNames, tc.wantMissing)
			}
		})
	}
}

func TestParityOpenAIProfileExecutesApplyPatchTool(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "code.txt")
	if err := os.WriteFile(path, []byte("before\n"), 0644); err != nil {
		t.Fatalf("write seed file: %v", err)
	}

	client := &inspectingCompleter{
		responses: []*llm.Response{
			{
				Message: llm.Message{
					Role: llm.RoleAssistant,
					Content: []llm.ContentPart{{
						Kind: llm.KindToolCall,
						ToolCall: &llm.ToolCallData{
							ID:   "call_1",
							Name: "apply_patch",
							Arguments: json.RawMessage(`{
								"patch": "*** Begin Patch\n*** Update File: code.txt\n@@\n-before\n+after\n*** End Patch\n"
							}`),
						},
					}},
				},
				FinishReason: llm.FinishReason{Reason: "tool_calls"},
			},
			{
				Message:      llm.AssistantMessage("Patched."),
				FinishReason: llm.FinishReason{Reason: "stop"},
			},
		},
	}

	cfg := DefaultConfig()
	cfg.Provider = "openai"
	cfg.Model = "gpt-5.2-codex"

	sess := mustNewSession(t, client, cfg, WithEnvironment(exec.NewLocalEnvironment(dir)))
	result, err := sess.Run(context.Background(), "Patch the file")
	if err != nil {
		t.Fatalf("Run failed: %v", err)
	}
	if result.Turns != 2 {
		t.Fatalf("turns = %d, want 2", result.Turns)
	}

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read patched file: %v", err)
	}
	if string(data) != "after\n" {
		t.Fatalf("patched content = %q, want %q", string(data), "after\n")
	}
}

func toolDefinitionNames(defs []llm.ToolDefinition) []string {
	names := make([]string, 0, len(defs))
	for _, def := range defs {
		names = append(names, def.Name)
	}
	return names
}

func containsString(values []string, want string) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
}
