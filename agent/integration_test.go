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
