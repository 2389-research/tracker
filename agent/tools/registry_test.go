// ABOUTME: Tests for Tool interface and Registry dispatch.
// ABOUTME: Validates tool registration, lookup, definition export, and execution dispatch.
package tools

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/2389-research/mammoth-lite/llm"
)

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
