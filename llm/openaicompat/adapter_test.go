// ABOUTME: Tests for the OpenAI Chat Completions compatible adapter (Complete and Stream).
// ABOUTME: Validates HTTP transport, SSE parsing, tool call accumulation, and error handling.
package openaicompat

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

func TestComplete_TextResponse(t *testing.T) {
	var capturedPath string
	var capturedAuth string
	var capturedBody map[string]any

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		capturedPath = r.URL.Path
		capturedAuth = r.Header.Get("Authorization")

		bodyBytes, _ := io.ReadAll(r.Body)
		json.Unmarshal(bodyBytes, &capturedBody)

		resp := `{
			"id": "chatcmpl-abc123",
			"model": "gpt-4.1",
			"choices": [{
				"index": 0,
				"message": {
					"role": "assistant",
					"content": "Hello there!"
				},
				"finish_reason": "stop"
			}],
			"usage": {
				"prompt_tokens": 10,
				"completion_tokens": 5,
				"total_tokens": 15
			}
		}`
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		fmt.Fprint(w, resp)
	}))
	defer srv.Close()

	adapter := New("test-api-key", WithBaseURL(srv.URL), WithHTTPClient(srv.Client()))

	req := &llm.Request{
		Model:    "gpt-4.1",
		Messages: []llm.Message{llm.UserMessage("Hi")},
	}

	resp, err := adapter.Complete(context.Background(), req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Verify request hit the correct endpoint
	if capturedPath != "/v1/chat/completions" {
		t.Errorf("expected path /v1/chat/completions, got %s", capturedPath)
	}

	// Verify authorization header
	if capturedAuth != "Bearer test-api-key" {
		t.Errorf("expected Authorization 'Bearer test-api-key', got %s", capturedAuth)
	}

	// Verify request body uses "messages" (Chat Completions), NOT "input" (Responses API)
	if _, ok := capturedBody["input"]; ok {
		t.Error("request body must not have 'input' field (that's Responses API)")
	}
	if _, ok := capturedBody["messages"]; !ok {
		t.Error("request body must have 'messages' field (Chat Completions format)")
	}

	// Verify response parsing
	if resp.ID != "chatcmpl-abc123" {
		t.Errorf("expected ID 'chatcmpl-abc123', got %s", resp.ID)
	}
	if resp.Provider != "openai-compat" {
		t.Errorf("expected Provider 'openai-compat', got %s", resp.Provider)
	}
	if resp.Text() != "Hello there!" {
		t.Errorf("expected text 'Hello there!', got %s", resp.Text())
	}
	if resp.Usage.InputTokens != 10 {
		t.Errorf("expected InputTokens 10, got %d", resp.Usage.InputTokens)
	}
	if resp.Usage.OutputTokens != 5 {
		t.Errorf("expected OutputTokens 5, got %d", resp.Usage.OutputTokens)
	}
	if resp.Usage.TotalTokens != 15 {
		t.Errorf("expected TotalTokens 15, got %d", resp.Usage.TotalTokens)
	}
	if resp.FinishReason.Reason != "stop" {
		t.Errorf("expected finish reason 'stop', got %s", resp.FinishReason.Reason)
	}
	if resp.Latency <= 0 {
		t.Error("expected positive latency")
	}
}

func TestComplete_HTTPError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		fmt.Fprint(w, `{"error":{"message":"Invalid API key","type":"auth_error"}}`)
	}))
	defer srv.Close()

	adapter := New("bad-key", WithBaseURL(srv.URL), WithHTTPClient(srv.Client()))

	req := &llm.Request{
		Model:    "gpt-4.1",
		Messages: []llm.Message{llm.UserMessage("Hi")},
	}

	_, err := adapter.Complete(context.Background(), req)
	if err == nil {
		t.Fatal("expected error for 401 response")
	}

	// Verify it's an AuthenticationError
	var authErr *llm.AuthenticationError
	if !errorAs(err, &authErr) {
		t.Errorf("expected AuthenticationError, got %T: %v", err, err)
	}
}

func TestStream_TextResponse(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)

		lines := []string{
			// First chunk: role only
			`data: {"id":"chatcmpl-1","choices":[{"index":0,"delta":{"role":"assistant"},"finish_reason":null}]}`,
			"",
			// Text delta: "Hello"
			`data: {"id":"chatcmpl-1","choices":[{"index":0,"delta":{"content":"Hello"},"finish_reason":null}]}`,
			"",
			// Text delta: " world"
			`data: {"id":"chatcmpl-1","choices":[{"index":0,"delta":{"content":" world"},"finish_reason":null}]}`,
			"",
			// Finish with usage
			`data: {"id":"chatcmpl-1","choices":[{"index":0,"delta":{},"finish_reason":"stop"}],"usage":{"prompt_tokens":8,"completion_tokens":2,"total_tokens":10}}`,
			"",
			// Done sentinel
			`data: [DONE]`,
			"",
		}
		for _, line := range lines {
			fmt.Fprintln(w, line)
		}
	}))
	defer srv.Close()

	adapter := New("test-key", WithBaseURL(srv.URL), WithHTTPClient(srv.Client()))

	req := &llm.Request{
		Model:    "gpt-4.1",
		Messages: []llm.Message{llm.UserMessage("Hi")},
	}

	ch := adapter.Stream(context.Background(), req)

	var events []llm.StreamEvent
	for evt := range ch {
		events = append(events, evt)
	}

	// Expect: stream_start, text_delta("Hello"), text_delta(" world"), finish
	var gotStreamStart bool
	var textAccum string
	var gotFinish bool
	var finishReason string

	for _, evt := range events {
		switch evt.Type {
		case llm.EventStreamStart:
			gotStreamStart = true
		case llm.EventTextDelta:
			textAccum += evt.Delta
		case llm.EventFinish:
			gotFinish = true
			if evt.FinishReason != nil {
				finishReason = evt.FinishReason.Reason
			}
		case llm.EventError:
			t.Fatalf("unexpected error event: %v", evt.Err)
		}
	}

	if !gotStreamStart {
		t.Error("expected EventStreamStart")
	}
	if textAccum != "Hello world" {
		t.Errorf("expected accumulated text 'Hello world', got %q", textAccum)
	}
	if !gotFinish {
		t.Error("expected EventFinish")
	}
	if finishReason != "stop" {
		t.Errorf("expected finish reason 'stop', got %q", finishReason)
	}
}

func TestStream_ToolCalls(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)

		lines := []string{
			// Role-only delta
			`data: {"id":"chatcmpl-2","choices":[{"index":0,"delta":{"role":"assistant"},"finish_reason":null}]}`,
			"",
			// Tool call start: index=0, id, name
			`data: {"id":"chatcmpl-2","choices":[{"index":0,"delta":{"tool_calls":[{"index":0,"id":"call_abc","type":"function","function":{"name":"read_file","arguments":""}}]},"finish_reason":null}]}`,
			"",
			// Tool call args delta 1
			`data: {"id":"chatcmpl-2","choices":[{"index":0,"delta":{"tool_calls":[{"index":0,"function":{"arguments":"{\"pa"}}]},"finish_reason":null}]}`,
			"",
			// Tool call args delta 2
			`data: {"id":"chatcmpl-2","choices":[{"index":0,"delta":{"tool_calls":[{"index":0,"function":{"arguments":"th\":\"foo.txt\"}"}}]},"finish_reason":null}]}`,
			"",
			// Finish with tool_calls reason
			`data: {"id":"chatcmpl-2","choices":[{"index":0,"delta":{},"finish_reason":"tool_calls"}],"usage":{"prompt_tokens":15,"completion_tokens":20,"total_tokens":35}}`,
			"",
			// Done sentinel
			`data: [DONE]`,
			"",
		}
		for _, line := range lines {
			fmt.Fprintln(w, line)
		}
	}))
	defer srv.Close()

	adapter := New("test-key", WithBaseURL(srv.URL), WithHTTPClient(srv.Client()))

	req := &llm.Request{
		Model:    "gpt-4.1",
		Messages: []llm.Message{llm.UserMessage("Read foo.txt")},
	}

	ch := adapter.Stream(context.Background(), req)

	var events []llm.StreamEvent
	for evt := range ch {
		events = append(events, evt)
	}

	var gotToolStart bool
	var toolStartName string
	var toolDeltaCount int
	var gotToolEnd bool
	var toolEndArgs string
	var toolEndID string

	for _, evt := range events {
		switch evt.Type {
		case llm.EventToolCallStart:
			gotToolStart = true
			if evt.ToolCall != nil {
				toolStartName = evt.ToolCall.Name
			}
		case llm.EventToolCallDelta:
			toolDeltaCount++
		case llm.EventToolCallEnd:
			gotToolEnd = true
			if evt.ToolCall != nil {
				toolEndArgs = string(evt.ToolCall.Arguments)
				toolEndID = evt.ToolCall.ID
			}
		case llm.EventError:
			t.Fatalf("unexpected error event: %v", evt.Err)
		}
	}

	if !gotToolStart {
		t.Error("expected EventToolCallStart")
	}
	if toolStartName != "read_file" {
		t.Errorf("expected tool name 'read_file', got %q", toolStartName)
	}
	if toolDeltaCount != 2 {
		t.Errorf("expected 2 EventToolCallDelta events, got %d", toolDeltaCount)
	}
	if !gotToolEnd {
		t.Error("expected EventToolCallEnd")
	}
	if toolEndID != "call_abc" {
		t.Errorf("expected tool call ID 'call_abc', got %q", toolEndID)
	}
	expectedArgs := `{"path":"foo.txt"}`
	if toolEndArgs != expectedArgs {
		t.Errorf("expected accumulated args %q, got %q", expectedArgs, toolEndArgs)
	}
}

func TestStream_HTTPError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusTooManyRequests)
		fmt.Fprint(w, `{"error":{"message":"Rate limit exceeded"}}`)
	}))
	defer srv.Close()

	adapter := New("test-key", WithBaseURL(srv.URL), WithHTTPClient(srv.Client()))

	req := &llm.Request{
		Model:    "gpt-4.1",
		Messages: []llm.Message{llm.UserMessage("Hi")},
	}

	ch := adapter.Stream(context.Background(), req)

	var gotError bool
	for evt := range ch {
		if evt.Type == llm.EventError {
			gotError = true
			// Verify it's a RateLimitError
			var rateErr *llm.RateLimitError
			if !errorAs(evt.Err, &rateErr) {
				t.Errorf("expected RateLimitError, got %T: %v", evt.Err, evt.Err)
			}
		}
	}

	if !gotError {
		t.Error("expected EventError for 429 response")
	}
}

func TestStream_FinishAfterToolCallEnd(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)

		lines := []string{
			`data: {"id":"c1","choices":[{"index":0,"delta":{"role":"assistant"},"finish_reason":null}]}`,
			"",
			`data: {"id":"c1","choices":[{"index":0,"delta":{"tool_calls":[{"index":0,"id":"call_1","type":"function","function":{"name":"read_file","arguments":""}}]},"finish_reason":null}]}`,
			"",
			`data: {"id":"c1","choices":[{"index":0,"delta":{"tool_calls":[{"index":0,"function":{"arguments":"{\"path\":\"a.txt\"}"}}]},"finish_reason":null}]}`,
			"",
			// Finish arrives BEFORE [DONE] — adapter must defer it.
			`data: {"id":"c1","choices":[{"index":0,"delta":{},"finish_reason":"tool_calls"}],"usage":{"prompt_tokens":5,"completion_tokens":10,"total_tokens":15}}`,
			"",
			`data: [DONE]`,
			"",
		}
		for _, line := range lines {
			fmt.Fprintln(w, line)
		}
	}))
	defer srv.Close()

	adapter := New("k", WithBaseURL(srv.URL), WithHTTPClient(srv.Client()))
	ch := adapter.Stream(context.Background(), &llm.Request{
		Model:    "test",
		Messages: []llm.Message{llm.UserMessage("go")},
	})

	var events []llm.StreamEvent
	for evt := range ch {
		events = append(events, evt)
	}

	// Find positions of ToolCallEnd and Finish.
	toolEndIdx := -1
	finishIdx := -1
	for i, evt := range events {
		if evt.Type == llm.EventToolCallEnd {
			toolEndIdx = i
		}
		if evt.Type == llm.EventFinish {
			finishIdx = i
		}
	}

	if toolEndIdx == -1 {
		t.Fatal("expected EventToolCallEnd")
	}
	if finishIdx == -1 {
		t.Fatal("expected EventFinish")
	}
	if finishIdx <= toolEndIdx {
		t.Errorf("EventFinish (idx=%d) must come after EventToolCallEnd (idx=%d)", finishIdx, toolEndIdx)
	}
}

func TestStream_MultipleToolCalls_OrderedByIndex(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)

		lines := []string{
			`data: {"id":"c2","choices":[{"index":0,"delta":{"role":"assistant"},"finish_reason":null}]}`,
			"",
			// Tool call 0 starts
			`data: {"id":"c2","choices":[{"index":0,"delta":{"tool_calls":[{"index":0,"id":"call_a","type":"function","function":{"name":"read_file","arguments":""}}]},"finish_reason":null}]}`,
			"",
			// Tool call 1 starts (interleaved)
			`data: {"id":"c2","choices":[{"index":0,"delta":{"tool_calls":[{"index":1,"id":"call_b","type":"function","function":{"name":"write_file","arguments":""}}]},"finish_reason":null}]}`,
			"",
			// Args for call 0
			`data: {"id":"c2","choices":[{"index":0,"delta":{"tool_calls":[{"index":0,"function":{"arguments":"{\"p\":\"a\"}"}}]},"finish_reason":null}]}`,
			"",
			// Args for call 1
			`data: {"id":"c2","choices":[{"index":0,"delta":{"tool_calls":[{"index":1,"function":{"arguments":"{\"p\":\"b\"}"}}]},"finish_reason":null}]}`,
			"",
			`data: {"id":"c2","choices":[{"index":0,"delta":{},"finish_reason":"tool_calls"}]}`,
			"",
			`data: [DONE]`,
			"",
		}
		for _, line := range lines {
			fmt.Fprintln(w, line)
		}
	}))
	defer srv.Close()

	adapter := New("k", WithBaseURL(srv.URL), WithHTTPClient(srv.Client()))
	ch := adapter.Stream(context.Background(), &llm.Request{
		Model:    "test",
		Messages: []llm.Message{llm.UserMessage("go")},
	})

	var events []llm.StreamEvent
	for evt := range ch {
		events = append(events, evt)
	}

	// Collect ToolCallEnd events in order.
	var endNames []string
	for _, evt := range events {
		if evt.Type == llm.EventToolCallEnd && evt.ToolCall != nil {
			endNames = append(endNames, evt.ToolCall.Name)
		}
	}

	if len(endNames) != 2 {
		t.Fatalf("expected 2 EventToolCallEnd events, got %d", len(endNames))
	}
	// Index 0 = read_file must come before index 1 = write_file.
	if endNames[0] != "read_file" {
		t.Errorf("first ToolCallEnd name = %q, want 'read_file'", endNames[0])
	}
	if endNames[1] != "write_file" {
		t.Errorf("second ToolCallEnd name = %q, want 'write_file'", endNames[1])
	}
}

func TestStream_StreamOptionsInRequest(t *testing.T) {
	var capturedBody map[string]any

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		bodyBytes, _ := io.ReadAll(r.Body)
		json.Unmarshal(bodyBytes, &capturedBody)

		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		fmt.Fprintln(w, `data: {"id":"c3","choices":[{"index":0,"delta":{"content":"hi"},"finish_reason":null}]}`)
		fmt.Fprintln(w, "")
		fmt.Fprintln(w, `data: {"id":"c3","choices":[{"index":0,"delta":{},"finish_reason":"stop"}]}`)
		fmt.Fprintln(w, "")
		fmt.Fprintln(w, `data: [DONE]`)
		fmt.Fprintln(w, "")
	}))
	defer srv.Close()

	adapter := New("k", WithBaseURL(srv.URL), WithHTTPClient(srv.Client()))
	ch := adapter.Stream(context.Background(), &llm.Request{
		Model:    "test",
		Messages: []llm.Message{llm.UserMessage("go")},
	})
	for range ch {
	}

	if capturedBody["stream"] != true {
		t.Errorf("expected stream=true in request body, got %v", capturedBody["stream"])
	}
	so, ok := capturedBody["stream_options"].(map[string]any)
	if !ok {
		t.Fatal("expected 'stream_options' in request body")
	}
	if so["include_usage"] != true {
		t.Errorf("expected stream_options.include_usage=true, got %v", so["include_usage"])
	}
}

func TestStream_SSEInStreamError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)

		lines := []string{
			`data: {"id":"c1","choices":[{"index":0,"delta":{"role":"assistant","content":"Hi"},"finish_reason":null}]}`,
			"",
			`data: {"error":{"message":"Rate limit exceeded","type":"rate_limit_error","code":"rate_limit_exceeded"}}`,
			"",
			`data: [DONE]`,
			"",
		}
		for _, line := range lines {
			fmt.Fprintln(w, line)
		}
	}))
	defer srv.Close()

	adapter := New("k", WithBaseURL(srv.URL), WithHTTPClient(srv.Client()))
	ch := adapter.Stream(context.Background(), &llm.Request{
		Model:    "test",
		Messages: []llm.Message{llm.UserMessage("go")},
	})

	var gotText bool
	var gotError bool
	var errorMsg string
	for evt := range ch {
		switch evt.Type {
		case llm.EventTextDelta:
			gotText = true
		case llm.EventError:
			gotError = true
			if evt.Err != nil {
				errorMsg = evt.Err.Error()
			}
		}
	}

	if !gotText {
		t.Error("expected text delta before error")
	}
	if !gotError {
		t.Error("expected EventError for in-stream error")
	}
	if !strings.Contains(errorMsg, "Rate limit exceeded") {
		t.Errorf("error message should contain 'Rate limit exceeded', got: %s", errorMsg)
	}
}

func TestStream_TextLifecycleEvents(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)

		lines := []string{
			`data: {"id":"c1","choices":[{"index":0,"delta":{"role":"assistant"},"finish_reason":null}]}`,
			"",
			`data: {"id":"c1","choices":[{"index":0,"delta":{"content":"Hello"},"finish_reason":null}]}`,
			"",
			`data: {"id":"c1","choices":[{"index":0,"delta":{},"finish_reason":"stop"}]}`,
			"",
			`data: [DONE]`,
			"",
		}
		for _, line := range lines {
			fmt.Fprintln(w, line)
		}
	}))
	defer srv.Close()

	adapter := New("k", WithBaseURL(srv.URL), WithHTTPClient(srv.Client()))
	ch := adapter.Stream(context.Background(), &llm.Request{
		Model:    "test",
		Messages: []llm.Message{llm.UserMessage("go")},
	})

	var types []llm.StreamEventType
	for evt := range ch {
		types = append(types, evt.Type)
	}

	var gotTextStart, gotTextEnd bool
	for _, tp := range types {
		if tp == llm.EventTextStart {
			gotTextStart = true
		}
		if tp == llm.EventTextEnd {
			gotTextEnd = true
		}
	}
	if !gotTextStart {
		t.Error("expected EventTextStart before text deltas")
	}
	if !gotTextEnd {
		t.Error("expected EventTextEnd before finish")
	}
}

func TestComplete_OversizedResponse(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		// Write more than the 10MB limit
		for i := 0; i < 11*1024; i++ {
			w.Write(make([]byte, 1024))
		}
	}))
	defer srv.Close()

	adapter := New("k", WithBaseURL(srv.URL), WithHTTPClient(srv.Client()))
	_, err := adapter.Complete(context.Background(), &llm.Request{
		Model:    "test",
		Messages: []llm.Message{llm.UserMessage("go")},
	})

	if err == nil {
		t.Fatal("expected error for oversized response")
	}
	if !strings.Contains(err.Error(), "exceeds 10MB limit") {
		t.Errorf("expected size-limit error, got: %v", err)
	}
}

// errorAs is a test helper wrapping errors.As for generic error type matching.
func errorAs[T any](err error, target *T) bool {
	return errorAsImpl(err, target)
}

func errorAsImpl(err error, target any) bool {
	// Use a type switch to delegate to errors.As properly.
	switch t := target.(type) {
	case **llm.AuthenticationError:
		return asAuthError(err, t)
	case **llm.RateLimitError:
		return asRateLimitError(err, t)
	default:
		return false
	}
}

func asAuthError(err error, target **llm.AuthenticationError) bool {
	var e *llm.AuthenticationError
	if ok := extractError(err, &e); ok {
		*target = e
		return true
	}
	return false
}

func asRateLimitError(err error, target **llm.RateLimitError) bool {
	var e *llm.RateLimitError
	if ok := extractError(err, &e); ok {
		*target = e
		return true
	}
	return false
}

func extractError[T error](err error, target *T) bool {
	return errorsAs(err, target)
}

// errorsAs wraps the standard errors.As function to keep imports clean.
func errorsAs[T error](err error, target *T) bool {
	return errors.As(err, target)
}
