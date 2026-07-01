// ABOUTME: Tests that tool-call (un)marshal errors in the Gemini translator are surfaced, not swallowed (#397).
package google

import (
	"encoding/json"
	"testing"

	"github.com/2389-research/tracker/llm"
)

// TestTranslateRequestToolCallArgumentsUnmarshal covers translateToolCallPart:
// a malformed tool-call arguments blob must propagate an error out of
// translateRequest instead of being silently dropped.
func TestTranslateRequestToolCallArgumentsUnmarshal(t *testing.T) {
	tests := []struct {
		name    string
		args    json.RawMessage
		wantErr bool
	}{
		{name: "valid object", args: json.RawMessage(`{"path":"a.go"}`), wantErr: false},
		{name: "empty args", args: nil, wantErr: false},
		{name: "malformed json", args: json.RawMessage(`{invalid`), wantErr: true},
		{name: "wrong type", args: json.RawMessage(`["not","an","object"]`), wantErr: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := &llm.Request{
				Model: "gemini-2.0-flash",
				Messages: []llm.Message{{
					Role: llm.RoleAssistant,
					Content: []llm.ContentPart{{
						Kind:     llm.KindToolCall,
						ToolCall: &llm.ToolCallData{Name: "edit", Arguments: tt.args},
					}},
				}},
			}

			_, err := translateRequest(req)
			if tt.wantErr && err == nil {
				t.Fatal("translateRequest = nil error, want error for malformed tool-call arguments")
			}
			if !tt.wantErr && err != nil {
				t.Fatalf("translateRequest returned unexpected error: %v", err)
			}
		})
	}
}

// TestExtractCandidateContentMarshalError covers extractCandidateContent:
// a function-call args map that cannot be marshaled must surface an error.
func TestExtractCandidateContentMarshalError(t *testing.T) {
	tests := []struct {
		name    string
		args    map[string]any
		wantErr bool
	}{
		{name: "marshalable args", args: map[string]any{"path": "a.go"}, wantErr: false},
		{name: "unmarshalable args", args: map[string]any{"ch": make(chan int)}, wantErr: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			candidates := []geminiCandidate{{
				Content: geminiContent{Parts: []geminiPart{{
					FunctionCall: &geminiFunctionCall{Name: "edit", Args: tt.args},
				}}},
			}}

			_, _, err := extractCandidateContent(candidates)
			if tt.wantErr && err == nil {
				t.Fatal("extractCandidateContent = nil error, want error for unmarshalable args")
			}
			if !tt.wantErr && err != nil {
				t.Fatalf("extractCandidateContent returned unexpected error: %v", err)
			}
		})
	}
}

// TestProcessGeminiPartMarshalError covers the streaming path: a function-call
// args map that cannot be marshaled must emit an EventError on the channel.
func TestProcessGeminiPartMarshalError(t *testing.T) {
	a := &Adapter{}
	ch := make(chan llm.StreamEvent, 4)
	state := &geminiStreamState{}

	part := geminiPart{FunctionCall: &geminiFunctionCall{Name: "edit", Args: map[string]any{"ch": make(chan int)}}}
	a.processGeminiPart(part, ch, state)
	close(ch)

	var sawError bool
	for ev := range ch {
		if ev.Type == llm.EventError {
			sawError = true
			if ev.Err == nil {
				t.Fatal("EventError with nil Err")
			}
		}
		if ev.Type == llm.EventToolCallStart {
			t.Fatal("emitted EventToolCallStart despite marshal failure")
		}
	}
	if !sawError {
		t.Fatal("processGeminiPart did not emit EventError for unmarshalable args")
	}
}
