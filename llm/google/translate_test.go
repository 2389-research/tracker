// ABOUTME: Tests for Google Gemini API request/response format translation.
// ABOUTME: Validates response format (json_object, json_schema) mapping to generationConfig fields.
package google

import (
	"encoding/json"
	"testing"

	"github.com/2389-research/tracker/llm"
)

func TestTranslateRequestResponseFormatNil(t *testing.T) {
	req := &llm.Request{
		Model:    "gemini-2.0-flash",
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

	// With no ResponseFormat and no generation config params, generationConfig should be absent.
	if gc, ok := m["generationConfig"]; ok {
		gcMap, ok := gc.(map[string]any)
		if ok {
			if _, ok := gcMap["responseMimeType"]; ok {
				t.Error("expected no responseMimeType when ResponseFormat is nil")
			}
		}
	}
}

func TestTranslateRequestResponseFormatJSONObject(t *testing.T) {
	req := &llm.Request{
		Model:    "gemini-2.0-flash",
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

	gc, ok := m["generationConfig"].(map[string]any)
	if !ok {
		t.Fatal("expected generationConfig to be present")
	}

	if gc["responseMimeType"] != "application/json" {
		t.Errorf("expected responseMimeType 'application/json', got %v", gc["responseMimeType"])
	}

	if _, ok := gc["responseSchema"]; ok {
		t.Error("expected no responseSchema for json_object mode")
	}
}

func TestTranslateRequestResponseFormatJSONSchema(t *testing.T) {
	schema := json.RawMessage(`{"type":"object","properties":{"name":{"type":"string"}},"required":["name"]}`)
	req := &llm.Request{
		Model:    "gemini-2.0-flash",
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

	gc, ok := m["generationConfig"].(map[string]any)
	if !ok {
		t.Fatal("expected generationConfig to be present")
	}

	if gc["responseMimeType"] != "application/json" {
		t.Errorf("expected responseMimeType 'application/json', got %v", gc["responseMimeType"])
	}

	responseSchema, ok := gc["responseSchema"].(map[string]any)
	if !ok {
		t.Fatal("expected responseSchema to be present as object")
	}

	if responseSchema["type"] != "object" {
		t.Errorf("expected responseSchema type 'object', got %v", responseSchema["type"])
	}
}

func TestTranslateRequestResponseFormatJSONObjectWithGenConfig(t *testing.T) {
	temp := 0.5
	req := &llm.Request{
		Model:       "gemini-2.0-flash",
		Messages:    []llm.Message{llm.UserMessage("Hello")},
		Temperature: &temp,
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

	gc, ok := m["generationConfig"].(map[string]any)
	if !ok {
		t.Fatal("expected generationConfig to be present")
	}

	// Both temperature and responseMimeType should be present.
	if gc["responseMimeType"] != "application/json" {
		t.Errorf("expected responseMimeType 'application/json', got %v", gc["responseMimeType"])
	}

	if gc["temperature"] != 0.5 {
		t.Errorf("expected temperature 0.5, got %v", gc["temperature"])
	}
}

func TestTranslateRequestResponseFormatText(t *testing.T) {
	req := &llm.Request{
		Model:    "gemini-2.0-flash",
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

	// "text" type should not add responseMimeType.
	if gc, ok := m["generationConfig"]; ok {
		gcMap, ok := gc.(map[string]any)
		if ok {
			if _, ok := gcMap["responseMimeType"]; ok {
				t.Error("expected no responseMimeType for 'text' response format type")
			}
		}
	}
}
