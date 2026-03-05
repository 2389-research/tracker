// ABOUTME: Tests for OpenAI Responses API request/response format translation.
// ABOUTME: Validates response format (json_object, json_schema) mapping to OpenAI's text.format structure.
package openai

import (
	"encoding/json"
	"testing"

	"github.com/2389-research/mammoth-lite/llm"
)

func TestTranslateRequestResponseFormatNil(t *testing.T) {
	req := &llm.Request{
		Model:    "gpt-4.1",
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

	if _, ok := m["text"]; ok {
		t.Error("expected no 'text' field when ResponseFormat is nil")
	}
}

func TestTranslateRequestResponseFormatJSONObject(t *testing.T) {
	req := &llm.Request{
		Model:    "gpt-4.1",
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

	text, ok := m["text"].(map[string]any)
	if !ok {
		t.Fatal("expected 'text' field to be present as object")
	}

	format, ok := text["format"].(map[string]any)
	if !ok {
		t.Fatal("expected 'text.format' to be present as object")
	}

	if format["type"] != "json_object" {
		t.Errorf("expected format type 'json_object', got %v", format["type"])
	}

	// json_object mode should not have schema or name fields
	if _, ok := format["schema"]; ok {
		t.Error("expected no 'schema' field for json_object mode")
	}
}

func TestTranslateRequestResponseFormatJSONSchema(t *testing.T) {
	schema := json.RawMessage(`{"type":"object","properties":{"name":{"type":"string"}},"required":["name"]}`)
	req := &llm.Request{
		Model:    "gpt-4.1",
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

	text, ok := m["text"].(map[string]any)
	if !ok {
		t.Fatal("expected 'text' field to be present as object")
	}

	format, ok := text["format"].(map[string]any)
	if !ok {
		t.Fatal("expected 'text.format' to be present as object")
	}

	if format["type"] != "json_schema" {
		t.Errorf("expected format type 'json_schema', got %v", format["type"])
	}

	if format["name"] != "response" {
		t.Errorf("expected format name 'response', got %v", format["name"])
	}

	if format["strict"] != true {
		t.Errorf("expected format strict true, got %v", format["strict"])
	}

	schemaField, ok := format["schema"].(map[string]any)
	if !ok {
		t.Fatal("expected 'schema' field to be present as object")
	}

	if schemaField["type"] != "object" {
		t.Errorf("expected schema type 'object', got %v", schemaField["type"])
	}
}

func TestTranslateRequestResponseFormatText(t *testing.T) {
	req := &llm.Request{
		Model:    "gpt-4.1",
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

	// "text" type should not add a text.format field since it's the default
	if _, ok := m["text"]; ok {
		t.Error("expected no 'text' field for 'text' response format type")
	}
}
