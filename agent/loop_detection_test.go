// ABOUTME: Tests for loop detection in the agent session's agentic loop.
// ABOUTME: Verifies that repeated identical tool call patterns are detected and break the loop.
package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"github.com/2389-research/mammoth-lite/llm"
)

// makeToolCallResponse creates an LLM response with the given tool call names.
func makeToolCallResponse(names ...string) *llm.Response {
	var parts []llm.ContentPart
	for i, name := range names {
		parts = append(parts, llm.ContentPart{
			Kind: llm.KindToolCall,
			ToolCall: &llm.ToolCallData{
				ID:        fmt.Sprintf("call_%d", i),
				Name:      name,
				Arguments: json.RawMessage(`{}`),
			},
		})
	}
	return &llm.Response{
		Message: llm.Message{
			Role:    llm.RoleAssistant,
			Content: parts,
		},
		FinishReason: llm.FinishReason{Reason: "tool_calls"},
	}
}

func TestLoopDetection_BreaksOnRepeatedToolCalls(t *testing.T) {
	threshold := 3
	// Create enough responses to exceed the threshold (threshold identical tool calls in a row).
	responses := make([]*llm.Response, threshold+5)
	for i := range responses {
		responses[i] = makeToolCallResponse("read")
	}

	client := &mockCompleter{responses: responses}

	cfg := DefaultConfig()
	cfg.MaxTurns = 50
	cfg.LoopDetectionThreshold = threshold

	readTool := &stubTool{name: "read", output: "content"}
	sess := mustNewSession(t, client, cfg, WithTools(readTool))

	result, err := sess.Run(context.Background(), "Do something")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// The loop should have been broken after exactly `threshold` turns.
	if result.Turns != threshold {
		t.Errorf("expected %d turns (loop detected), got %d", threshold, result.Turns)
	}
	if !result.LoopDetected {
		t.Error("expected LoopDetected to be true")
	}
	if !result.MaxTurnsUsed {
		t.Error("expected MaxTurnsUsed to be true when loop detected")
	}
}

func TestLoopDetection_NoFalsePositiveOnNormalExecution(t *testing.T) {
	// Normal flow: one tool call then a text response. No loop.
	toolResp := makeToolCallResponse("read")
	textResp := &llm.Response{
		Message:      llm.AssistantMessage("Done."),
		FinishReason: llm.FinishReason{Reason: "stop"},
	}

	client := &mockCompleter{responses: []*llm.Response{toolResp, textResp}}

	cfg := DefaultConfig()
	cfg.LoopDetectionThreshold = 3

	readTool := &stubTool{name: "read", output: "content"}
	sess := mustNewSession(t, client, cfg, WithTools(readTool))

	result, err := sess.Run(context.Background(), "Read a file")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if result.LoopDetected {
		t.Error("expected LoopDetected to be false for normal execution")
	}
	if result.Turns != 2 {
		t.Errorf("expected 2 turns, got %d", result.Turns)
	}
}

func TestLoopDetection_NoTriggerWhenToolCallsVary(t *testing.T) {
	// Each turn uses a different tool -- no loop should be detected.
	toolNames := []string{"read", "write", "edit", "glob", "grep", "bash"}

	responses := make([]*llm.Response, len(toolNames)+1)
	for i, name := range toolNames {
		responses[i] = makeToolCallResponse(name)
	}
	responses[len(toolNames)] = &llm.Response{
		Message:      llm.AssistantMessage("All done."),
		FinishReason: llm.FinishReason{Reason: "stop"},
	}

	client := &mockCompleter{responses: responses}

	cfg := DefaultConfig()
	cfg.LoopDetectionThreshold = 3

	tools := make([]SessionOption, 0, len(toolNames))
	for _, name := range toolNames {
		tools = append(tools, WithTools(&stubTool{name: name, output: "ok"}))
	}

	sess := mustNewSession(t, client, cfg, tools...)

	result, err := sess.Run(context.Background(), "Do varied work")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if result.LoopDetected {
		t.Error("expected LoopDetected to be false when tool calls vary")
	}
}

func TestLoopDetection_ConfigurableThreshold(t *testing.T) {
	// Test with a lower threshold of 2.
	threshold := 2
	responses := make([]*llm.Response, 20)
	for i := range responses {
		responses[i] = makeToolCallResponse("bash")
	}

	client := &mockCompleter{responses: responses}

	cfg := DefaultConfig()
	cfg.MaxTurns = 50
	cfg.LoopDetectionThreshold = threshold

	bashTool := &stubTool{name: "bash", output: "output"}
	sess := mustNewSession(t, client, cfg, WithTools(bashTool))

	result, err := sess.Run(context.Background(), "Run something")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if result.Turns != threshold {
		t.Errorf("expected %d turns with threshold %d, got %d", threshold, threshold, result.Turns)
	}
	if !result.LoopDetected {
		t.Error("expected LoopDetected to be true")
	}
}

func TestLoopDetection_MultipleToolCallSignature(t *testing.T) {
	// Repeated pattern of multiple tool calls per turn (read,edit) should be detected.
	threshold := 3
	responses := make([]*llm.Response, threshold+5)
	for i := range responses {
		responses[i] = makeToolCallResponse("read", "edit")
	}

	client := &mockCompleter{responses: responses}

	cfg := DefaultConfig()
	cfg.MaxTurns = 50
	cfg.LoopDetectionThreshold = threshold

	readTool := &stubTool{name: "read", output: "content"}
	editTool := &stubTool{name: "edit", output: "edited"}
	sess := mustNewSession(t, client, cfg, WithTools(readTool, editTool))

	result, err := sess.Run(context.Background(), "Edit a file")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if result.Turns != threshold {
		t.Errorf("expected %d turns, got %d", threshold, result.Turns)
	}
	if !result.LoopDetected {
		t.Error("expected LoopDetected to be true for repeated multi-tool pattern")
	}
}

func TestLoopDetection_ResetsCounterOnDifferentPattern(t *testing.T) {
	// 2x "read", then 1x "write", then 2x "read" again -- should NOT trigger with threshold 3.
	threshold := 3
	responses := []*llm.Response{
		makeToolCallResponse("read"),
		makeToolCallResponse("read"),
		makeToolCallResponse("write"),
		makeToolCallResponse("read"),
		makeToolCallResponse("read"),
	}
	// End with a text response.
	responses = append(responses, &llm.Response{
		Message:      llm.AssistantMessage("Done."),
		FinishReason: llm.FinishReason{Reason: "stop"},
	})

	client := &mockCompleter{responses: responses}

	cfg := DefaultConfig()
	cfg.MaxTurns = 50
	cfg.LoopDetectionThreshold = threshold

	readTool := &stubTool{name: "read", output: "content"}
	writeTool := &stubTool{name: "write", output: "written"}
	sess := mustNewSession(t, client, cfg, WithTools(readTool, writeTool))

	result, err := sess.Run(context.Background(), "Mixed work")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if result.LoopDetected {
		t.Error("expected LoopDetected to be false when counter resets between patterns")
	}
	if result.Turns != 6 {
		t.Errorf("expected 6 turns, got %d", result.Turns)
	}
}

func TestLoopDetection_EmitsErrorEvent(t *testing.T) {
	threshold := 2
	responses := make([]*llm.Response, 20)
	for i := range responses {
		responses[i] = makeToolCallResponse("read")
	}

	client := &mockCompleter{responses: responses}

	var events []Event
	handler := EventHandlerFunc(func(evt Event) {
		events = append(events, evt)
	})

	cfg := DefaultConfig()
	cfg.MaxTurns = 50
	cfg.LoopDetectionThreshold = threshold

	readTool := &stubTool{name: "read", output: "content"}
	sess := mustNewSession(t, client, cfg, WithTools(readTool), WithEventHandler(handler))

	_, err := sess.Run(context.Background(), "Loop forever")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Check that an EventError was emitted with the loop detection message.
	foundLoopError := false
	for _, evt := range events {
		if evt.Type == EventError && evt.Err != nil && strings.Contains(evt.Err.Error(), "loop detected") {
			foundLoopError = true
			break
		}
	}
	if !foundLoopError {
		t.Error("expected an EventError with 'loop detected' message")
	}
}
