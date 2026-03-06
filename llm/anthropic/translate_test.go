// ABOUTME: Tests for Anthropic Messages API request/response format translation.
// ABOUTME: Validates response format (json_object, json_schema) mapping to system prompt injection.
package anthropic

import (
	"encoding/json"
	"testing"

	"github.com/2389-research/tracker/llm"
)

func TestTranslateRequestResponseFormatNil(t *testing.T) {
	req := &llm.Request{
		Model:    "claude-sonnet-4-20250514",
		Messages: []llm.Message{llm.UserMessage("Hello")},
	}

	body, err := translateRequest(req)
	if err != nil {
		t.Fatal(err)
	}

	var m map[string]any
	if err := json.Unmarshal(body, &m); err != nil {
		t.Fatal(err)
	}

	// With no ResponseFormat, the system field should not contain JSON instructions.
	system, ok := m["system"]
	if ok {
		systemSlice, ok := system.([]any)
		if ok {
			for _, item := range systemSlice {
				block, ok := item.(map[string]any)
				if ok {
					if text, ok := block["text"].(string); ok {
						if text == "Respond with valid JSON." {
							t.Error("expected no JSON instruction in system when ResponseFormat is nil")
						}
					}
				}
			}
		}
	}
}

func TestTranslateRequestResponseFormatJSONObject(t *testing.T) {
	req := &llm.Request{
		Model:    "claude-sonnet-4-20250514",
		Messages: []llm.Message{llm.UserMessage("Hello")},
		ResponseFormat: &llm.ResponseFormat{
			Type: "json_object",
		},
	}

	body, err := translateRequest(req)
	if err != nil {
		t.Fatal(err)
	}

	var m map[string]any
	if err := json.Unmarshal(body, &m); err != nil {
		t.Fatal(err)
	}

	// System field should contain a JSON instruction.
	system, ok := m["system"].([]any)
	if !ok {
		t.Fatal("expected system field to be present as array")
	}

	foundJSONInstruction := false
	for _, item := range system {
		block, ok := item.(map[string]any)
		if !ok {
			continue
		}
		if text, ok := block["text"].(string); ok {
			if text == "Respond with valid JSON." {
				foundJSONInstruction = true
			}
		}
	}

	if !foundJSONInstruction {
		t.Error("expected 'Respond with valid JSON.' instruction in system for json_object mode")
	}
}

func TestTranslateRequestResponseFormatJSONSchema(t *testing.T) {
	schema := json.RawMessage(`{"type":"object","properties":{"name":{"type":"string"}},"required":["name"]}`)
	req := &llm.Request{
		Model:    "claude-sonnet-4-20250514",
		Messages: []llm.Message{llm.UserMessage("Hello")},
		ResponseFormat: &llm.ResponseFormat{
			Type:       "json_schema",
			JSONSchema: schema,
			Strict:     true,
		},
	}

	body, err := translateRequest(req)
	if err != nil {
		t.Fatal(err)
	}

	var m map[string]any
	if err := json.Unmarshal(body, &m); err != nil {
		t.Fatal(err)
	}

	system, ok := m["system"].([]any)
	if !ok {
		t.Fatal("expected system field to be present as array")
	}

	foundSchemaInstruction := false
	expectedText := `Respond with valid JSON conforming to this schema: {"type":"object","properties":{"name":{"type":"string"}},"required":["name"]}`
	for _, item := range system {
		block, ok := item.(map[string]any)
		if !ok {
			continue
		}
		if text, ok := block["text"].(string); ok {
			if text == expectedText {
				foundSchemaInstruction = true
			}
		}
	}

	if !foundSchemaInstruction {
		t.Errorf("expected schema instruction in system for json_schema mode")
	}
}

func TestTranslateRequestResponseFormatJSONObjectWithExistingSystem(t *testing.T) {
	req := &llm.Request{
		Model: "claude-sonnet-4-20250514",
		Messages: []llm.Message{
			llm.SystemMessage("You are a helpful assistant."),
			llm.UserMessage("Hello"),
		},
		ResponseFormat: &llm.ResponseFormat{
			Type: "json_object",
		},
	}

	body, err := translateRequest(req)
	if err != nil {
		t.Fatal(err)
	}

	var m map[string]any
	if err := json.Unmarshal(body, &m); err != nil {
		t.Fatal(err)
	}

	system, ok := m["system"].([]any)
	if !ok {
		t.Fatal("expected system field to be present as array")
	}

	// Should have both the original system message and the JSON instruction.
	if len(system) < 2 {
		t.Fatalf("expected at least 2 system blocks, got %d", len(system))
	}

	// First block should be the original system message.
	firstBlock := system[0].(map[string]any)
	if firstBlock["text"] != "You are a helpful assistant." {
		t.Errorf("expected first system block to be original message, got %v", firstBlock["text"])
	}

	// Last block should be the JSON instruction.
	lastBlock := system[len(system)-1].(map[string]any)
	if lastBlock["text"] != "Respond with valid JSON." {
		t.Errorf("expected last system block to be JSON instruction, got %v", lastBlock["text"])
	}
}

func TestTranslateRequestResponseFormatText(t *testing.T) {
	req := &llm.Request{
		Model:    "claude-sonnet-4-20250514",
		Messages: []llm.Message{llm.UserMessage("Hello")},
		ResponseFormat: &llm.ResponseFormat{
			Type: "text",
		},
	}

	body, err := translateRequest(req)
	if err != nil {
		t.Fatal(err)
	}

	var m map[string]any
	if err := json.Unmarshal(body, &m); err != nil {
		t.Fatal(err)
	}

	// "text" type should not inject any JSON instructions.
	if system, ok := m["system"].([]any); ok {
		for _, item := range system {
			block, ok := item.(map[string]any)
			if !ok {
				continue
			}
			if text, ok := block["text"].(string); ok {
				if text == "Respond with valid JSON." {
					t.Error("expected no JSON instruction in system for 'text' response format type")
				}
			}
		}
	}
}
