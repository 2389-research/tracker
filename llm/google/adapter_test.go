// ABOUTME: Tests for the Google Gemini API adapter.
// ABOUTME: Validates request/response translation, SSE stream parsing, and finish reason mapping.
package google

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/2389-research/tracker/llm"
)

// --- Request translation tests ---

func TestTranslateRequestBasic(t *testing.T) {
	req := &llm.Request{
		Model:    "gemini-2.5-pro",
		Messages: []llm.Message{llm.UserMessage("Hello")},
	}

	body, err := translateRequest(req)
	if err != nil {
		t.Fatal(err)
	}

	var raw map[string]any
	json.Unmarshal(body, &raw)

	contents := raw["contents"].([]any)
	if len(contents) != 1 {
		t.Fatalf("expected 1 content item, got %d", len(contents))
	}

	item := contents[0].(map[string]any)
	if item["role"] != "user" {
		t.Errorf("expected role 'user', got %v", item["role"])
	}
}

func TestTranslateRequestSystemInstruction(t *testing.T) {
	req := &llm.Request{
		Model: "gemini-2.5-pro",
		Messages: []llm.Message{
			llm.SystemMessage("Be helpful"),
			llm.UserMessage("Hello"),
		},
	}

	body, err := translateRequest(req)
	if err != nil {
		t.Fatal(err)
	}

	var raw map[string]any
	json.Unmarshal(body, &raw)

	sysInstr, ok := raw["systemInstruction"].(map[string]any)
	if !ok {
		t.Fatal("expected systemInstruction")
	}
	parts := sysInstr["parts"].([]any)
	if len(parts) != 1 {
		t.Fatalf("expected 1 system part, got %d", len(parts))
	}
	part := parts[0].(map[string]any)
	if part["text"] != "Be helpful" {
		t.Errorf("expected 'Be helpful', got %v", part["text"])
	}

	// System messages should NOT appear in contents.
	contents := raw["contents"].([]any)
	if len(contents) != 1 {
		t.Errorf("expected 1 content (user only), got %d", len(contents))
	}
}

func TestTranslateRequestAssistantRole(t *testing.T) {
	req := &llm.Request{
		Model: "gemini-2.5-pro",
		Messages: []llm.Message{
			llm.UserMessage("Hello"),
			llm.AssistantMessage("Hi there"),
		},
	}

	body, err := translateRequest(req)
	if err != nil {
		t.Fatal(err)
	}

	var raw map[string]any
	json.Unmarshal(body, &raw)

	contents := raw["contents"].([]any)
	if len(contents) != 2 {
		t.Fatalf("expected 2 content items, got %d", len(contents))
	}

	assistant := contents[1].(map[string]any)
	if assistant["role"] != "model" {
		t.Errorf("expected role 'model' for assistant, got %v", assistant["role"])
	}
}

func TestTranslateRequestToolDefinitions(t *testing.T) {
	req := &llm.Request{
		Model:    "gemini-2.5-pro",
		Messages: []llm.Message{llm.UserMessage("Hi")},
		Tools: []llm.ToolDefinition{
			{
				Name:        "read_file",
				Description: "Read a file",
				Parameters:  json.RawMessage(`{"type":"object","properties":{"path":{"type":"string"}}}`),
			},
		},
	}

	body, err := translateRequest(req)
	if err != nil {
		t.Fatal(err)
	}

	var raw map[string]any
	json.Unmarshal(body, &raw)

	tools := raw["tools"].([]any)
	if len(tools) != 1 {
		t.Fatalf("expected 1 tool group, got %d", len(tools))
	}

	toolGroup := tools[0].(map[string]any)
	decls := toolGroup["functionDeclarations"].([]any)
	if len(decls) != 1 {
		t.Fatalf("expected 1 function declaration, got %d", len(decls))
	}

	decl := decls[0].(map[string]any)
	if decl["name"] != "read_file" {
		t.Errorf("expected name 'read_file', got %v", decl["name"])
	}
}

func TestTranslateRequestToolChoice(t *testing.T) {
	tests := []struct {
		mode     string
		toolName string
		wantMode string
	}{
		{"auto", "", "AUTO"},
		{"none", "", "NONE"},
		{"required", "", "ANY"},
	}

	for _, tt := range tests {
		t.Run(tt.mode, func(t *testing.T) {
			req := &llm.Request{
				Model:    "gemini-2.5-pro",
				Messages: []llm.Message{llm.UserMessage("Hi")},
				ToolChoice: &llm.ToolChoice{
					Mode:     tt.mode,
					ToolName: tt.toolName,
				},
			}

			body, err := translateRequest(req)
			if err != nil {
				t.Fatal(err)
			}

			var raw map[string]any
			json.Unmarshal(body, &raw)

			tc := raw["toolConfig"].(map[string]any)
			fcc := tc["functionCallingConfig"].(map[string]any)
			if fcc["mode"] != tt.wantMode {
				t.Errorf("got mode %v, want %v", fcc["mode"], tt.wantMode)
			}
		})
	}
}

func TestTranslateRequestToolCallMessage(t *testing.T) {
	req := &llm.Request{
		Model: "gemini-2.5-pro",
		Messages: []llm.Message{
			llm.UserMessage("Hello"),
			{
				Role: llm.RoleAssistant,
				Content: []llm.ContentPart{
					{Kind: llm.KindToolCall, ToolCall: &llm.ToolCallData{
						ID:        "call_123",
						Name:      "read_file",
						Arguments: json.RawMessage(`{"path":"foo.txt"}`),
					}},
				},
			},
		},
	}

	body, err := translateRequest(req)
	if err != nil {
		t.Fatal(err)
	}

	var raw map[string]any
	json.Unmarshal(body, &raw)

	contents := raw["contents"].([]any)
	if len(contents) != 2 {
		t.Fatalf("expected 2 content items, got %d", len(contents))
	}

	modelContent := contents[1].(map[string]any)
	parts := modelContent["parts"].([]any)
	fc := parts[0].(map[string]any)
	if fc["functionCall"] == nil {
		t.Fatal("expected functionCall in model part")
	}
}

func TestTranslateRequestToolResultMessage(t *testing.T) {
	req := &llm.Request{
		Model: "gemini-2.5-pro",
		Messages: []llm.Message{
			llm.ToolResultMessage("read_file", "file contents", false),
		},
	}

	body, err := translateRequest(req)
	if err != nil {
		t.Fatal(err)
	}

	var raw map[string]any
	json.Unmarshal(body, &raw)

	contents := raw["contents"].([]any)
	content := contents[0].(map[string]any)
	if content["role"] != "user" {
		t.Errorf("expected tool result role 'user', got %v", content["role"])
	}

	parts := content["parts"].([]any)
	part := parts[0].(map[string]any)
	if part["functionResponse"] == nil {
		t.Fatal("expected functionResponse in part")
	}
}

// --- Response translation tests ---

func TestTranslateResponseBasic(t *testing.T) {
	raw := `{
		"candidates": [{
			"content": {
				"role": "model",
				"parts": [{"text": "Hello!"}]
			},
			"finishReason": "STOP"
		}],
		"usageMetadata": {
			"promptTokenCount": 10,
			"candidatesTokenCount": 5,
			"totalTokenCount": 15
		},
		"modelVersion": "gemini-2.5-pro-001"
	}`

	resp, err := translateResponse([]byte(raw))
	if err != nil {
		t.Fatal(err)
	}

	if resp.Text() != "Hello!" {
		t.Errorf("expected text 'Hello!', got %q", resp.Text())
	}
	if resp.Model != "gemini-2.5-pro-001" {
		t.Errorf("expected model 'gemini-2.5-pro-001', got %q", resp.Model)
	}
	if resp.Usage.InputTokens != 10 {
		t.Errorf("expected input tokens 10, got %d", resp.Usage.InputTokens)
	}
	if resp.FinishReason.Reason != "stop" {
		t.Errorf("expected finish reason 'stop', got %q", resp.FinishReason.Reason)
	}
}

func TestTranslateResponseToolCall(t *testing.T) {
	raw := `{
		"candidates": [{
			"content": {
				"role": "model",
				"parts": [{
					"functionCall": {
						"name": "read_file",
						"args": {"path": "foo.txt"}
					}
				}]
			},
			"finishReason": "STOP"
		}],
		"usageMetadata": {"promptTokenCount": 10, "candidatesTokenCount": 5, "totalTokenCount": 15}
	}`

	resp, err := translateResponse([]byte(raw))
	if err != nil {
		t.Fatal(err)
	}

	calls := resp.ToolCalls()
	if len(calls) != 1 {
		t.Fatalf("expected 1 tool call, got %d", len(calls))
	}
	if calls[0].Name != "read_file" {
		t.Errorf("expected tool name 'read_file', got %q", calls[0].Name)
	}
	// Synthetic ID should start with "call_".
	if !strings.HasPrefix(calls[0].ID, "call_") {
		t.Errorf("expected synthetic ID starting with 'call_', got %q", calls[0].ID)
	}
	if resp.FinishReason.Reason != "tool_calls" {
		t.Errorf("expected finish reason 'tool_calls', got %q", resp.FinishReason.Reason)
	}
}

// --- Finish reason tests ---

func TestTranslateFinishReason(t *testing.T) {
	tests := []struct {
		reason     string
		hasCalls   bool
		wantReason string
	}{
		{"STOP", false, "stop"},
		{"MAX_TOKENS", false, "length"},
		{"SAFETY", false, "content_filter"},
		{"RECITATION", false, "content_filter"},
		{"STOP", true, "tool_calls"},
	}

	for _, tt := range tests {
		t.Run(fmt.Sprintf("%s_%v", tt.reason, tt.hasCalls), func(t *testing.T) {
			fr := translateFinishReason(tt.reason, tt.hasCalls)
			if fr.Reason != tt.wantReason {
				t.Errorf("got reason %q, want %q", fr.Reason, tt.wantReason)
			}
		})
	}
}

// --- Adapter integration tests (httptest) ---

func TestAdapterComplete(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Verify the API key is in the header.
		if r.Header.Get("x-goog-api-key") != "test-key" {
			t.Errorf("expected x-goog-api-key=test-key, got %q", r.Header.Get("x-goog-api-key"))
		}

		// Verify URL pattern.
		if !strings.Contains(r.URL.Path, "/v1beta/models/gemini-2.5-pro:generateContent") {
			t.Errorf("unexpected path: %s", r.URL.Path)
		}

		body, _ := io.ReadAll(r.Body)
		var raw map[string]any
		json.Unmarshal(body, &raw)

		w.WriteHeader(http.StatusOK)
		fmt.Fprint(w, `{
			"candidates": [{
				"content": {"role": "model", "parts": [{"text": "Hello from Gemini!"}]},
				"finishReason": "STOP"
			}],
			"usageMetadata": {"promptTokenCount": 10, "candidatesTokenCount": 5, "totalTokenCount": 15},
			"modelVersion": "gemini-2.5-pro-001"
		}`)
	}))
	defer server.Close()

	a := New("test-key", WithBaseURL(server.URL))
	resp, err := a.Complete(context.Background(), &llm.Request{
		Model:    "gemini-2.5-pro",
		Messages: []llm.Message{llm.UserMessage("Hello")},
	})
	if err != nil {
		t.Fatal(err)
	}

	if resp.Text() != "Hello from Gemini!" {
		t.Errorf("expected 'Hello from Gemini!', got %q", resp.Text())
	}
	if resp.Provider != "gemini" {
		t.Errorf("expected provider 'gemini', got %q", resp.Provider)
	}
}

func TestAdapterCompleteFallsBackToRequestedModelWhenModelVersionMissing(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		fmt.Fprint(w, `{
			"candidates": [{
				"content": {"role": "model", "parts": [{"text": "Hello from Gemini!"}]},
				"finishReason": "STOP"
			}],
			"usageMetadata": {"promptTokenCount": 10, "candidatesTokenCount": 5, "totalTokenCount": 15}
		}`)
	}))
	defer server.Close()

	a := New("test-key", WithBaseURL(server.URL))
	resp, err := a.Complete(context.Background(), &llm.Request{
		Model:    "gemini-2.5-pro",
		Messages: []llm.Message{llm.UserMessage("Hello")},
	})
	if err != nil {
		t.Fatal(err)
	}
	if resp.Model != "gemini-2.5-pro" {
		t.Fatalf("Model = %q, want request model", resp.Model)
	}
}

func TestAdapterCompleteErrorStatus(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		fmt.Fprint(w, `{"error": {"message": "invalid api key"}}`)
	}))
	defer server.Close()

	a := New("bad-key", WithBaseURL(server.URL))
	_, err := a.Complete(context.Background(), &llm.Request{
		Model:    "gemini-2.5-pro",
		Messages: []llm.Message{llm.UserMessage("Hi")},
	})
	if err == nil {
		t.Fatal("expected error for 401")
	}

	var authErr *llm.AuthenticationError
	if !errors.As(err, &authErr) {
		t.Errorf("expected AuthenticationError, got %T: %v", err, err)
	}
}

func TestAdapterStream(t *testing.T) {
	// Gemini SSE: each data line is a complete JSON response chunk.
	sseData := strings.Join([]string{
		`data: {"candidates":[{"content":{"role":"model","parts":[{"text":"Hello"}]}}],"usageMetadata":{"promptTokenCount":5,"candidatesTokenCount":1,"totalTokenCount":6}}`,
		"",
		`data: {"candidates":[{"content":{"role":"model","parts":[{"text":" world"}]},"finishReason":"STOP"}],"usageMetadata":{"promptTokenCount":5,"candidatesTokenCount":3,"totalTokenCount":8}}`,
		"",
	}, "\n")

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Verify streaming URL pattern.
		if !strings.Contains(r.URL.Path, ":streamGenerateContent") {
			t.Errorf("expected streamGenerateContent in path, got %s", r.URL.Path)
		}
		if r.URL.Query().Get("alt") != "sse" {
			t.Errorf("expected alt=sse, got %q", r.URL.Query().Get("alt"))
		}

		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		fmt.Fprint(w, sseData)
	}))
	defer server.Close()

	a := New("test-key", WithBaseURL(server.URL))
	ch := a.Stream(context.Background(), &llm.Request{
		Model:    "gemini-2.5-pro",
		Messages: []llm.Message{llm.UserMessage("Hello")},
	})

	var events []llm.StreamEvent
	for evt := range ch {
		events = append(events, evt)
	}

	// Should have: StreamStart, TextStart, TextDelta("Hello"), TextDelta(" world"), TextEnd, Finish
	if len(events) < 6 {
		t.Fatalf("expected at least 6 events, got %d: %+v", len(events), events)
	}

	if events[0].Type != llm.EventStreamStart {
		t.Errorf("first event should be StreamStart, got %v", events[0].Type)
	}
	if events[1].Type != llm.EventTextStart {
		t.Errorf("second event should be TextStart, got %v", events[1].Type)
	}
	if events[2].Delta != "Hello" {
		t.Errorf("expected delta 'Hello', got %q", events[2].Delta)
	}
	if events[3].Delta != " world" {
		t.Errorf("expected delta ' world', got %q", events[3].Delta)
	}
	if events[4].Type != llm.EventTextEnd {
		t.Errorf("fifth event should be TextEnd, got %v", events[4].Type)
	}

	lastEvt := events[len(events)-1]
	if lastEvt.Type != llm.EventFinish {
		t.Errorf("last event should be Finish, got %v", lastEvt.Type)
	}
	if lastEvt.FinishReason == nil || lastEvt.FinishReason.Reason != "stop" {
		t.Errorf("expected finish reason 'stop', got %+v", lastEvt.FinishReason)
	}
}

func TestAdapterName(t *testing.T) {
	a := New("key")
	if a.Name() != "gemini" {
		t.Errorf("expected 'google', got %q", a.Name())
	}
}

// Some upstreams (notably the 2389 Bedrock Gateway) emit usageMetadata as
// a standalone trailing SSE chunk after the finish chunk. Verify the
// parser threads that into the final accumulated Usage instead of dropping
// it on the floor.
func TestAdapterStreamTrailingUsageChunk(t *testing.T) {
	sseData := strings.Join([]string{
		`data: {"candidates":[{"content":{"role":"model","parts":[{"text":"ok"}]},"index":0}]}`,
		"",
		`data: {"candidates":[{"content":{"role":"model","parts":[]},"finishReason":"STOP","index":0}]}`,
		"",
		`data: {"usageMetadata":{"promptTokenCount":9,"candidatesTokenCount":4,"totalTokenCount":13}}`,
		"",
	}, "\n")

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		fmt.Fprint(w, sseData)
	}))
	defer server.Close()

	a := New("test-key", WithBaseURL(server.URL))
	ch := a.Stream(context.Background(), &llm.Request{
		Model:    "gemini-2.5-pro",
		Messages: []llm.Message{llm.UserMessage("Say ok")},
	})

	acc := llm.NewStreamAccumulator()
	for evt := range ch {
		acc.Process(evt)
	}

	resp := acc.Response()
	if resp.Usage.InputTokens != 9 {
		t.Errorf("expected InputTokens=9, got %d", resp.Usage.InputTokens)
	}
	if resp.Usage.OutputTokens != 4 {
		t.Errorf("expected OutputTokens=4, got %d", resp.Usage.OutputTokens)
	}
	if resp.Usage.TotalTokens != 13 {
		t.Errorf("expected TotalTokens=13, got %d", resp.Usage.TotalTokens)
	}
	if resp.FinishReason.Reason != "stop" {
		t.Errorf("expected finish reason 'stop', got %q", resp.FinishReason.Reason)
	}
}

// Even when an upstream splits the finish chunk and the usageMetadata
// chunk, the parser should coalesce them into a single EventFinish so
// downstream observers (trace log, NDJSON cost events) don't see a
// duplicate "llm finish" entry. The combined event must carry both the
// finish reason and the usage.
func TestAdapterStreamTrailingUsageChunkEmitsSingleFinish(t *testing.T) {
	sseData := strings.Join([]string{
		`data: {"candidates":[{"content":{"role":"model","parts":[{"text":"ok"}]},"index":0}]}`,
		"",
		`data: {"candidates":[{"content":{"role":"model","parts":[]},"finishReason":"STOP","index":0}]}`,
		"",
		`data: {"usageMetadata":{"promptTokenCount":9,"candidatesTokenCount":4,"totalTokenCount":13}}`,
		"",
	}, "\n")

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		fmt.Fprint(w, sseData)
	}))
	defer server.Close()

	a := New("test-key", WithBaseURL(server.URL))
	ch := a.Stream(context.Background(), &llm.Request{
		Model:    "gemini-2.5-pro",
		Messages: []llm.Message{llm.UserMessage("Say ok")},
	})

	var finishEvents []llm.StreamEvent
	for evt := range ch {
		if evt.Type == llm.EventFinish {
			finishEvents = append(finishEvents, evt)
		}
	}

	if len(finishEvents) != 1 {
		t.Fatalf("expected exactly 1 EventFinish, got %d: %+v", len(finishEvents), finishEvents)
	}
	fe := finishEvents[0]
	if fe.FinishReason == nil || fe.FinishReason.Reason != "stop" {
		t.Errorf("expected finish reason 'stop' on the single event, got %+v", fe.FinishReason)
	}
	if fe.Usage == nil {
		t.Fatal("expected usage on the single finish event")
	}
	if fe.Usage.InputTokens != 9 || fe.Usage.OutputTokens != 4 || fe.Usage.TotalTokens != 13 {
		t.Errorf("usage mismatch: got %+v, want In=9 Out=4 Total=13", fe.Usage)
	}
}

// Defensive: if the stream ends with a buffered finish reason and no
// trailing usage chunk ever arrives, the parser must still emit a final
// EventFinish carrying the reason (with nil usage) so callers see a
// terminal event.
func TestAdapterStreamFinishWithoutUsageChunk(t *testing.T) {
	sseData := strings.Join([]string{
		`data: {"candidates":[{"content":{"role":"model","parts":[{"text":"ok"}]},"index":0}]}`,
		"",
		`data: {"candidates":[{"content":{"role":"model","parts":[]},"finishReason":"STOP","index":0}]}`,
		"",
	}, "\n")

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		fmt.Fprint(w, sseData)
	}))
	defer server.Close()

	a := New("test-key", WithBaseURL(server.URL))
	ch := a.Stream(context.Background(), &llm.Request{
		Model:    "gemini-2.5-pro",
		Messages: []llm.Message{llm.UserMessage("Say ok")},
	})

	var finishEvents []llm.StreamEvent
	for evt := range ch {
		if evt.Type == llm.EventFinish {
			finishEvents = append(finishEvents, evt)
		}
	}

	if len(finishEvents) != 1 {
		t.Fatalf("expected exactly 1 EventFinish, got %d", len(finishEvents))
	}
	if finishEvents[0].FinishReason == nil || finishEvents[0].FinishReason.Reason != "stop" {
		t.Errorf("expected finish reason 'stop', got %+v", finishEvents[0].FinishReason)
	}
}

// Defensive: if a split-style chunk buffers a finish reason and then a
// later candidate chunk emits a combined (reason + usage) finish, the
// buffered reason must be cleared so the stream-end flush doesn't emit
// a duplicate EventFinish.
func TestAdapterStreamCombinedAfterSplitClearsPending(t *testing.T) {
	sseData := strings.Join([]string{
		`data: {"candidates":[{"content":{"role":"model","parts":[]},"finishReason":"STOP","index":0}]}`,
		"",
		`data: {"candidates":[{"content":{"role":"model","parts":[]},"finishReason":"STOP","index":0}],"usageMetadata":{"promptTokenCount":9,"candidatesTokenCount":4,"totalTokenCount":13}}`,
		"",
	}, "\n")

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		fmt.Fprint(w, sseData)
	}))
	defer server.Close()

	a := New("test-key", WithBaseURL(server.URL))
	ch := a.Stream(context.Background(), &llm.Request{
		Model:    "gemini-2.5-pro",
		Messages: []llm.Message{llm.UserMessage("Say ok")},
	})

	var finishEvents []llm.StreamEvent
	for evt := range ch {
		if evt.Type == llm.EventFinish {
			finishEvents = append(finishEvents, evt)
		}
	}

	if len(finishEvents) != 1 {
		t.Fatalf("expected exactly 1 EventFinish, got %d", len(finishEvents))
	}
}

// When an SSE chunk fails to parse after a finish reason was buffered,
// the parser must flush the buffered finish before emitting the error,
// so callers (StreamAccumulator, trace observers) still see a terminal
// EventFinish for the work that completed before the stream broke.
func TestAdapterStreamParseErrorFlushesPendingFinish(t *testing.T) {
	sseData := strings.Join([]string{
		`data: {"candidates":[{"content":{"role":"model","parts":[]},"finishReason":"STOP","index":0}]}`,
		"",
		`data: {not-valid-json`,
		"",
	}, "\n")

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		fmt.Fprint(w, sseData)
	}))
	defer server.Close()

	a := New("test-key", WithBaseURL(server.URL))
	ch := a.Stream(context.Background(), &llm.Request{
		Model:    "gemini-2.5-pro",
		Messages: []llm.Message{llm.UserMessage("Say ok")},
	})

	var events []llm.StreamEvent
	for evt := range ch {
		events = append(events, evt)
	}

	finishIdx, errIdx := -1, -1
	for i, e := range events {
		if e.Type == llm.EventFinish && finishIdx < 0 {
			finishIdx = i
		}
		if e.Type == llm.EventError && errIdx < 0 {
			errIdx = i
		}
	}
	if finishIdx < 0 {
		t.Fatalf("expected an EventFinish before EventError, got events: %+v", events)
	}
	if errIdx < 0 {
		t.Fatalf("expected an EventError after the bad chunk, got events: %+v", events)
	}
	if finishIdx > errIdx {
		t.Errorf("EventFinish (idx %d) must precede EventError (idx %d)", finishIdx, errIdx)
	}
	if events[finishIdx].FinishReason == nil || events[finishIdx].FinishReason.Reason != "stop" {
		t.Errorf("expected flushed finish reason 'stop', got %+v", events[finishIdx].FinishReason)
	}
}

func TestAdapterStreamToolCall(t *testing.T) {
	sseData := strings.Join([]string{
		`data: {"candidates":[{"content":{"role":"model","parts":[{"functionCall":{"name":"read_file","args":{"path":"test.go"}}}]},"finishReason":"STOP"}],"usageMetadata":{"promptTokenCount":10,"candidatesTokenCount":5,"totalTokenCount":15}}`,
		"",
	}, "\n")

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		fmt.Fprint(w, sseData)
	}))
	defer server.Close()

	a := New("test-key", WithBaseURL(server.URL))
	ch := a.Stream(context.Background(), &llm.Request{
		Model:    "gemini-2.5-pro",
		Messages: []llm.Message{llm.UserMessage("Read test.go")},
	})

	var events []llm.StreamEvent
	for evt := range ch {
		events = append(events, evt)
	}

	// StreamStart, ToolCallStart, ToolCallEnd, Finish
	foundToolStart := false
	foundToolEnd := false
	for _, evt := range events {
		if evt.Type == llm.EventToolCallStart {
			foundToolStart = true
			if evt.ToolCall == nil || evt.ToolCall.Name != "read_file" {
				t.Errorf("expected tool name 'read_file', got %+v", evt.ToolCall)
			}
		}
		if evt.Type == llm.EventToolCallEnd {
			foundToolEnd = true
		}
	}

	if !foundToolStart {
		t.Error("expected ToolCallStart event")
	}
	if !foundToolEnd {
		t.Error("expected ToolCallEnd event")
	}

	lastEvt := events[len(events)-1]
	if lastEvt.FinishReason == nil || lastEvt.FinishReason.Reason != "tool_calls" {
		t.Errorf("expected finish reason 'tool_calls', got %+v", lastEvt.FinishReason)
	}
}

func TestThoughtSignatureRoundTrip(t *testing.T) {
	// Simulate Gemini response with thoughtSignature on a functionCall part.
	respJSON := []byte(`{
		"candidates": [{
			"content": {
				"role": "model",
				"parts": [{
					"functionCall": {"name": "read_file", "args": {"path": "main.go"}},
					"thoughtSignature": "ABCD1234sig"
				}]
			},
			"finishReason": "STOP"
		}]
	}`)

	resp, err := translateResponse(respJSON)
	if err != nil {
		t.Fatal(err)
	}

	if len(resp.Message.Content) != 1 {
		t.Fatalf("expected 1 content part, got %d", len(resp.Message.Content))
	}
	tc := resp.Message.Content[0].ToolCall
	if tc == nil {
		t.Fatal("expected tool call")
	}
	if tc.ThoughtSigData != "ABCD1234sig" {
		t.Errorf("expected thoughtSignature 'ABCD1234sig', got %q", tc.ThoughtSigData)
	}

	// Now round-trip: translate a request that includes this tool call in history.
	req := &llm.Request{
		Model: "gemini-3-pro-preview",
		Messages: []llm.Message{
			llm.UserMessage("read main.go"),
			resp.Message,
			{
				Role: llm.RoleTool,
				Content: []llm.ContentPart{{
					Kind: llm.KindToolResult,
					ToolResult: &llm.ToolResultData{
						ToolCallID: tc.ID,
						Name:       "read_file",
						Content:    "package main",
					},
				}},
			},
		},
	}

	body, err := translateRequest(req)
	if err != nil {
		t.Fatal(err)
	}

	var raw map[string]any
	json.Unmarshal(body, &raw)
	contents := raw["contents"].([]any)

	// contents[1] should be the model message with the function call + thoughtSignature.
	modelMsg := contents[1].(map[string]any)
	parts := modelMsg["parts"].([]any)
	fcPart := parts[0].(map[string]any)

	sig, ok := fcPart["thoughtSignature"]
	if !ok {
		t.Fatal("thoughtSignature missing from round-tripped request")
	}
	if sig != "ABCD1234sig" {
		t.Errorf("expected 'ABCD1234sig', got %v", sig)
	}
}

func TestAdapterStreamErrorChunk(t *testing.T) {
	sseData := strings.Join([]string{
		`data: {"candidates":[{"content":{"role":"model","parts":[{"text":"partial"}]},"index":0}]}`,
		"",
		`data: {"error":{"code":429,"message":"Resource has been exhausted (e.g. check quota).","status":"RESOURCE_EXHAUSTED"}}`,
		"",
	}, "\n")

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		fmt.Fprint(w, sseData)
	}))
	defer server.Close()

	a := New("test-key", WithBaseURL(server.URL))
	ch := a.Stream(context.Background(), &llm.Request{
		Model:    "gemini-2.5-pro",
		Messages: []llm.Message{llm.UserMessage("Say ok")},
	})

	var streamErr error
	for evt := range ch {
		if evt.Type == llm.EventError && streamErr == nil {
			streamErr = evt.Err
		}
	}

	if streamErr == nil {
		t.Fatal("expected EventError for in-stream error chunk, got none")
	}
	if !strings.Contains(streamErr.Error(), "Resource has been exhausted") {
		t.Errorf("error %q should contain the API message", streamErr.Error())
	}
	var rl *llm.RateLimitError
	if !errors.As(streamErr, &rl) {
		t.Errorf("expected *llm.RateLimitError, got %T (%v)", streamErr, streamErr)
	}
}
