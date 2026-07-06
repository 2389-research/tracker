# OpenAI-Compat Review Fixes Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Address all critical and important findings from the 11-reviewer expert panel review of the `feat/openai-compat-provider` branch.

**Architecture:** Fixes are organized by severity (critical first) and independence (parallelizable tasks grouped). Each task is self-contained with tests written before implementation.

**Tech Stack:** Go, httptest, TDD

---

### Task 1: Fix credential leak — add OPENAI_COMPAT env vars to subprocess stripping

**Files:**
- Modify: `pipeline/handlers/backend_claudecode.go:240-245`
- Modify: `pipeline/handlers/backend_acp.go:371-381`
- Test: `pipeline/handlers/backend_claudecode_env_test.go`

This is a one-line security fix per file. The existing test in `backend_claudecode_env_test.go` already iterates `providerKeyPrefixes` and asserts none leak — adding the new prefix to the list means the test covers it automatically.

- [ ] **Step 1: Write a failing test that sets OPENAI_COMPAT_API_KEY and asserts it's stripped**

In `pipeline/handlers/backend_claudecode_env_test.go`, add a test case that explicitly sets `OPENAI_COMPAT_API_KEY` and verifies `buildEnv()` strips it:

```go
func TestBuildEnv_StripsOpenAICompatKey(t *testing.T) {
	t.Setenv("OPENAI_COMPAT_API_KEY", "test-compat-key-12345")
	t.Setenv("TRACKER_PASS_API_KEYS", "")

	env := buildEnv()

	for _, e := range env {
		if strings.HasPrefix(e, "OPENAI_COMPAT_API_KEY=") {
			t.Errorf("expected OPENAI_COMPAT_API_KEY stripped from env, but found: %s", e)
		}
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `cd /Users/harper/Public/src/2389/tracker && go test ./pipeline/handlers/ -run TestBuildEnv_StripsOpenAICompatKey -v`
Expected: FAIL — `OPENAI_COMPAT_API_KEY` is present in env because it's not in `providerKeyPrefixes`.

- [ ] **Step 3: Add OPENAI_COMPAT keys to both stripping lists**

In `pipeline/handlers/backend_claudecode.go:240-245`, add the new prefix:

```go
var providerKeyPrefixes = []string{
	"ANTHROPIC_API_KEY=",
	"OPENAI_API_KEY=",
	"GEMINI_API_KEY=",
	"GOOGLE_API_KEY=",
	"OPENAI_COMPAT_API_KEY=",
}
```

In `pipeline/handlers/backend_acp.go:371-381`, add both compat prefixes:

```go
var acpStrippedPrefixes = []string{
	"ANTHROPIC_API_KEY=",
	"OPENAI_API_KEY=",
	"GEMINI_API_KEY=",
	"GOOGLE_API_KEY=",
	"ANTHROPIC_BASE_URL=",
	"OPENAI_COMPAT_API_KEY=",
	"OPENAI_COMPAT_BASE_URL=",
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./pipeline/handlers/ -run TestBuildEnv_StripsOpenAICompatKey -v`
Expected: PASS

- [ ] **Step 5: Run full handler test suite**

Run: `go test ./pipeline/handlers/ -short -v`
Expected: All tests pass.

- [ ] **Step 6: Commit**

```bash
git add pipeline/handlers/backend_claudecode.go pipeline/handlers/backend_acp.go pipeline/handlers/backend_claudecode_env_test.go
git commit -m "fix: strip OPENAI_COMPAT env vars from subprocess environments"
```

---

### Task 2: Handle SSE in-stream errors

**Files:**
- Modify: `llm/openaicompat/adapter.go`
- Test: `llm/openaicompat/adapter_test.go`

The Chat Completions API (and OpenRouter) can embed error objects inside 200 SSE streams. The current parser silently ignores these. We need to detect `{"error": {...}}` payloads and emit typed `EventError` events, matching the pattern in `llm/openai/adapter_sse.go:208-267`.

- [ ] **Step 1: Write a failing test for SSE in-stream error detection**

Add to `adapter_test.go`:

```go
func TestStream_SSEInStreamError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)

		lines := []string{
			// Normal text delta first
			`data: {"id":"c1","choices":[{"index":0,"delta":{"role":"assistant","content":"Hi"},"finish_reason":null}]}`,
			"",
			// Error mid-stream (e.g., quota exceeded)
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
```

Add `"strings"` to the import block if not already present.

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./llm/openaicompat/ -run TestStream_SSEInStreamError -v`
Expected: FAIL — error event is not emitted because the error JSON parses as a zero-valued `chatStreamChunk` and is silently ignored.

- [ ] **Step 3: Add chatStreamError type and detection to parseSSE**

Add a new struct after the existing SSE chunk types at the bottom of `adapter.go`:

```go
// chatStreamError represents an error object embedded in a 200 SSE stream.
type chatStreamError struct {
	Error struct {
		Message string `json:"message"`
		Type    string `json:"type"`
		Code    string `json:"code"`
	} `json:"error"`
}
```

In the `parseSSE` method, after the `[DONE]` check (line 204) and before the JSON unmarshal of `chatStreamChunk` (line 207), add error detection:

```go
		// Check for error objects embedded in the SSE stream.
		// Some providers (OpenRouter, etc.) send {"error": {...}} within 200 streams.
		if strings.Contains(data, `"error"`) {
			var errChunk chatStreamError
			if err := json.Unmarshal([]byte(data), &errChunk); err == nil && errChunk.Error.Message != "" {
				ch <- llm.StreamEvent{
					Type: llm.EventError,
					Err:  sseErrorToTyped(errChunk.Error.Code, errChunk.Error.Message),
				}
				continue
			}
		}
```

Add the `sseErrorToTyped` function (matching the pattern in `llm/openai/adapter_sse.go:243-267`):

```go
// sseErrorToTyped maps an error code from an SSE stream event to a typed error.
func sseErrorToTyped(code, message string) error {
	base := llm.ProviderError{
		SDKError:  llm.SDKError{Msg: "openai-compat: " + message},
		Provider:  "openai-compat",
		ErrorCode: code,
	}
	switch code {
	case "insufficient_quota":
		return &llm.QuotaExceededError{ProviderError: base}
	case "invalid_api_key", "authentication_error":
		return &llm.AuthenticationError{ProviderError: base}
	case "model_not_found":
		return &llm.NotFoundError{ProviderError: base}
	case "invalid_request_error", "invalid_request":
		return &llm.InvalidRequestError{ProviderError: base}
	case "context_length_exceeded":
		return &llm.ContextLengthError{ProviderError: base}
	case "content_filter", "content_policy_violation":
		return &llm.ContentFilterError{ProviderError: base}
	case "rate_limit_exceeded":
		return &llm.RateLimitError{ProviderError: base}
	case "server_error", "internal_error":
		return &llm.ServerError{ProviderError: base}
	default:
		return &llm.InvalidRequestError{ProviderError: base}
	}
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./llm/openaicompat/ -run TestStream_SSEInStreamError -v`
Expected: PASS

- [ ] **Step 5: Run full openaicompat test suite**

Run: `go test ./llm/openaicompat/ -v`
Expected: All tests pass (new + existing).

- [ ] **Step 6: Commit**

```bash
git add llm/openaicompat/adapter.go llm/openaicompat/adapter_test.go
git commit -m "fix: handle SSE in-stream errors in openai-compat provider"
```

---

### Task 3: Treat empty responses as errors

**Files:**
- Modify: `llm/openaicompat/translate.go:310-313`
- Modify: `llm/openaicompat/translate_test.go`

The CLAUDE.md rule: "Empty agent responses (0 tokens, 0 tool calls) are failures, not successes." Currently `translateResponse` returns `FinishReason: "stop"` for empty choices.

- [ ] **Step 1: Update the existing TestTranslateResponse_EmptyChoices test to expect an error**

Replace the existing test in `translate_test.go`:

```go
func TestTranslateResponse_EmptyChoices(t *testing.T) {
	respJSON := `{
		"id": "chatcmpl-789",
		"model": "gpt-4.1",
		"choices": [],
		"usage": {
			"prompt_tokens": 5,
			"completion_tokens": 0,
			"total_tokens": 5
		}
	}`

	_, err := translateResponse([]byte(respJSON))
	if err == nil {
		t.Fatal("expected error for empty choices, got nil")
	}
	if !strings.Contains(err.Error(), "empty") {
		t.Errorf("error should mention 'empty', got: %s", err)
	}
}
```

Add `"strings"` to the import block if not already present.

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./llm/openaicompat/ -run TestTranslateResponse_EmptyChoices -v`
Expected: FAIL — currently returns nil error.

- [ ] **Step 3: Return an error for empty choices**

In `translate.go`, change the empty choices handling at line 310-313:

```go
	if len(cr.Choices) > 0 {
		choice := cr.Choices[0]
		resp.Message.Content = translateChoiceMessage(choice.Message)
		resp.FinishReason = translateFinishReason(choice.FinishReason, len(choice.Message.ToolCalls) > 0)
	} else {
		return nil, fmt.Errorf("openai-compat: empty response (0 choices, 0 content)")
	}
```

Add `"fmt"` to the import block.

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./llm/openaicompat/ -run TestTranslateResponse_EmptyChoices -v`
Expected: PASS

- [ ] **Step 5: Run full test suite**

Run: `go test ./llm/openaicompat/ -v`
Expected: All tests pass.

- [ ] **Step 6: Commit**

```bash
git add llm/openaicompat/translate.go llm/openaicompat/translate_test.go
git commit -m "fix: treat empty API responses as errors per project rules"
```

---

### Task 4: Add openai-compat to tracker doctor, error messages, and config persistence

**Files:**
- Modify: `cmd/tracker/doctor.go:66-73`
- Modify: `cmd/tracker/doctor.go:15-27`
- Modify: `cmd/tracker/config_env.go:11-19`

- [ ] **Step 1: Add openai-compat to the doctor provider check list**

In `cmd/tracker/doctor.go`, add the compat provider to the `providers` slice inside `checkProviders()`:

```go
	providers := []struct {
		name    string
		envVars []string
	}{
		{"Anthropic", []string{"ANTHROPIC_API_KEY"}},
		{"OpenAI", []string{"OPENAI_API_KEY"}},
		{"Gemini", []string{"GEMINI_API_KEY", "GOOGLE_API_KEY"}},
		{"OpenAI-Compat", []string{"OPENAI_COMPAT_API_KEY"}},
	}
```

- [ ] **Step 2: Update formatLLMClientError to mention openai-compat**

In `cmd/tracker/doctor.go`, update the error message in `formatLLMClientError`:

```go
func formatLLMClientError(err error) error {
	if strings.Contains(err.Error(), "no providers configured") {
		return fmt.Errorf(`no LLM providers configured

  Set at least one API key:
    export ANTHROPIC_API_KEY=sk-ant-...
    export OPENAI_API_KEY=sk-...
    export GEMINI_API_KEY=...
    export OPENAI_COMPAT_API_KEY=...  (+ OPENAI_COMPAT_BASE_URL for non-OpenRouter endpoints)

  Or run: tracker setup`)
	}
	return fmt.Errorf("create LLM client: %w", err)
}
```

- [ ] **Step 3: Add compat env vars to config persistence allowlist**

In `cmd/tracker/config_env.go`, add the two new keys to `providerEnvKeys`:

```go
var providerEnvKeys = map[string]struct{}{
	"OPENAI_API_KEY":          {},
	"ANTHROPIC_API_KEY":       {},
	"GEMINI_API_KEY":          {},
	"GOOGLE_API_KEY":          {},
	"OPENAI_BASE_URL":         {},
	"ANTHROPIC_BASE_URL":      {},
	"GEMINI_BASE_URL":         {},
	"OPENAI_COMPAT_API_KEY":   {},
	"OPENAI_COMPAT_BASE_URL":  {},
}
```

- [ ] **Step 4: Verify build**

Run: `go build ./cmd/tracker/`
Expected: Clean build.

- [ ] **Step 5: Commit**

```bash
git add cmd/tracker/doctor.go cmd/tracker/config_env.go
git commit -m "fix: add openai-compat to doctor, error messages, and config persistence"
```

---

### Task 5: Add missing TextStart/TextEnd lifecycle events to SSE parser

**Files:**
- Modify: `llm/openaicompat/adapter.go`
- Test: `llm/openaicompat/adapter_test.go`

The `StreamAccumulator` auto-creates text parts when `TextID` is missing, but other providers emit `TextStart`/`TextEnd` lifecycle events. Adding these ensures conformance and prevents bugs in consumers that expect the full lifecycle.

- [ ] **Step 1: Write a failing test that checks for TextStart and TextEnd events**

Add to `adapter_test.go`:

```go
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

	// Must contain TextStart before TextDelta and TextEnd before Finish
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
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./llm/openaicompat/ -run TestStream_TextLifecycleEvents -v`
Expected: FAIL — no `TextStart` or `TextEnd` events emitted.

- [ ] **Step 3: Add TextStart/TextEnd emission to parseSSE**

In `adapter.go`, add a `textStarted` bool alongside the existing state variables in `parseSSE` (near line 182):

```go
	firstChunk := true
	textStarted := false
```

In the text content delta block (around line 224), emit `TextStart` on first text delta:

```go
			// Text content delta.
			if choice.Delta.Content != "" {
				if !textStarted {
					ch <- llm.StreamEvent{Type: llm.EventTextStart, TextID: "text"}
					textStarted = true
				}
				ch <- llm.StreamEvent{
					Type:   llm.EventTextDelta,
					TextID: "text",
					Delta:  choice.Delta.Content,
				}
			}
```

In the `[DONE]` handling block (around line 198), emit `TextEnd` before tool call ends:

```go
		if data == "[DONE]" {
			if textStarted {
				ch <- llm.StreamEvent{Type: llm.EventTextEnd, TextID: "text"}
			}
			a.emitAccumulatedToolCallEnds(toolCalls, ch)
			if deferredFinish != nil {
				ch <- *deferredFinish
			}
			break
		}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./llm/openaicompat/ -run TestStream_TextLifecycleEvents -v`
Expected: PASS

- [ ] **Step 5: Run full openaicompat test suite**

Run: `go test ./llm/openaicompat/ -v`
Expected: All tests pass.

- [ ] **Step 6: Commit**

```bash
git add llm/openaicompat/adapter.go llm/openaicompat/adapter_test.go
git commit -m "fix: emit TextStart/TextEnd lifecycle events in openai-compat SSE parser"
```

---

### Task 6: Add bounded response reads

**Files:**
- Modify: `llm/openaicompat/adapter.go`
- Test: `llm/openaicompat/adapter_test.go`

`io.ReadAll` with no size limit is an OOM risk, especially since this adapter targets user-controlled endpoints.

- [ ] **Step 1: Write a failing test for oversized response rejection**

Add to `adapter_test.go`:

```go
func TestComplete_OversizedResponse(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		// Write more than the limit (we'll set limit to 10MB)
		// Just write a header that claims a huge body — the actual limit check
		// happens on the read side, so write 11MB of data.
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
}
```

- [ ] **Step 2: Run test to verify it fails (passes without error currently)**

Run: `go test ./llm/openaicompat/ -run TestComplete_OversizedResponse -v`
Expected: FAIL — currently reads the full body without limit.

- [ ] **Step 3: Replace io.ReadAll with io.LimitReader**

In `adapter.go`, add a constant near the top:

```go
const (
	defaultBaseURL   = "https://openrouter.ai/api"
	chatCompletePath = "/v1/chat/completions"
	// maxResponseSize caps the response body to prevent OOM from malicious servers.
	maxResponseSize = 10 * 1024 * 1024 // 10MB
)
```

Replace the `io.ReadAll` call in `Complete()` (line 98):

```go
	respBody, err := io.ReadAll(io.LimitReader(httpResp.Body, maxResponseSize+1))
	if err != nil {
		return nil, &llm.NetworkError{SDKError: llm.SDKError{Msg: fmt.Sprintf("openai-compat: read response: %s", err.Error()), Cause: err}}
	}
	if int64(len(respBody)) > maxResponseSize {
		return nil, &llm.InvalidRequestError{ProviderError: llm.ProviderError{
			SDKError: llm.SDKError{Msg: "openai-compat: response body exceeds 10MB limit"},
			Provider: "openai-compat",
		}}
	}
```

Replace the `io.ReadAll` call in `Stream()` error path (line 146):

```go
			respBody, _ := io.ReadAll(io.LimitReader(httpResp.Body, maxResponseSize))
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./llm/openaicompat/ -run TestComplete_OversizedResponse -v`
Expected: PASS

- [ ] **Step 5: Run full test suite**

Run: `go test ./llm/openaicompat/ -v`
Expected: All tests pass.

- [ ] **Step 6: Commit**

```bash
git add llm/openaicompat/adapter.go llm/openaicompat/adapter_test.go
git commit -m "fix: bound response body reads to 10MB in openai-compat adapter"
```

---

### Task 7: Add missing test coverage for context cancellation and malformed SSE

**Files:**
- Modify: `llm/openaicompat/adapter_test.go`

These are the two critical test gaps flagged by the Test Coverage reviewer.

- [ ] **Step 1: Write test for context cancellation during streaming**

Add to `adapter_test.go`:

```go
func TestStream_ContextCancellation(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		flusher, _ := w.(http.Flusher)

		// Send one chunk then block until client disconnects
		fmt.Fprintln(w, `data: {"id":"c1","choices":[{"index":0,"delta":{"content":"Hi"},"finish_reason":null}]}`)
		fmt.Fprintln(w, "")
		if flusher != nil {
			flusher.Flush()
		}

		// Block until the request context is done (client cancels)
		<-r.Context().Done()
	}))
	defer srv.Close()

	ctx, cancel := context.WithCancel(context.Background())
	adapter := New("k", WithBaseURL(srv.URL), WithHTTPClient(srv.Client()))
	ch := adapter.Stream(ctx, &llm.Request{
		Model:    "test",
		Messages: []llm.Message{llm.UserMessage("go")},
	})

	// Read the first text delta
	evt := <-ch
	if evt.Type != llm.EventStreamStart && evt.Type != llm.EventTextStart && evt.Type != llm.EventTextDelta {
		// Accept any of the early events
	}

	// Cancel the context
	cancel()

	// Drain remaining events — should NOT see a context error event
	var gotContextError bool
	for evt := range ch {
		if evt.Type == llm.EventError {
			if errors.Is(evt.Err, context.Canceled) || errors.Is(evt.Err, context.DeadlineExceeded) {
				gotContextError = true
			}
		}
	}

	if gotContextError {
		t.Error("context cancellation errors should be suppressed, not emitted as EventError")
	}
}
```

- [ ] **Step 2: Write test for malformed JSON in SSE chunks**

Add to `adapter_test.go`:

```go
func TestStream_MalformedSSEChunk(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)

		lines := []string{
			`data: {"id":"c1","choices":[{"index":0,"delta":{"content":"Hello"},"finish_reason":null}]}`,
			"",
			`data: {this is not valid json}`,
			"",
			`data: {"id":"c1","choices":[{"index":0,"delta":{"content":" world"},"finish_reason":null}]}`,
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

	var textAccum string
	var errorCount int
	var gotFinish bool
	for evt := range ch {
		switch evt.Type {
		case llm.EventTextDelta:
			textAccum += evt.Delta
		case llm.EventError:
			errorCount++
		case llm.EventFinish:
			gotFinish = true
		}
	}

	// Should have received both text deltas despite the bad chunk
	if textAccum != "Hello world" {
		t.Errorf("expected 'Hello world', got %q", textAccum)
	}
	// Should have received exactly one parse error
	if errorCount != 1 {
		t.Errorf("expected 1 error event for malformed chunk, got %d", errorCount)
	}
	// Stream should still finish cleanly
	if !gotFinish {
		t.Error("expected EventFinish after malformed chunk recovery")
	}
}
```

- [ ] **Step 3: Run both tests**

Run: `go test ./llm/openaicompat/ -run "TestStream_ContextCancellation|TestStream_MalformedSSEChunk" -v`
Expected: Both PASS (these test existing behavior that already works — we're adding coverage, not fixing bugs).

- [ ] **Step 4: Commit**

```bash
git add llm/openaicompat/adapter_test.go
git commit -m "test: add context cancellation and malformed SSE coverage for openai-compat"
```

---

### Task 8: Add missing test coverage for error types and request field translation

**Files:**
- Modify: `llm/openaicompat/adapter_test.go`
- Modify: `llm/openaicompat/translate_test.go`

Covers: 5xx server errors, temperature/topP/stop translation, base URL normalization.

- [ ] **Step 1: Add 500 server error test**

Add to `adapter_test.go`:

```go
func TestComplete_ServerError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		fmt.Fprint(w, `{"error":{"message":"Internal server error"}}`)
	}))
	defer srv.Close()

	adapter := New("k", WithBaseURL(srv.URL), WithHTTPClient(srv.Client()))
	_, err := adapter.Complete(context.Background(), &llm.Request{
		Model:    "test",
		Messages: []llm.Message{llm.UserMessage("go")},
	})

	if err == nil {
		t.Fatal("expected error for 500 response")
	}
	var serverErr *llm.ServerError
	if !errors.As(err, &serverErr) {
		t.Errorf("expected ServerError, got %T: %v", err, err)
	}
}
```

- [ ] **Step 2: Add temperature/topP/stop translation test**

Add to `translate_test.go`:

```go
func TestTranslateRequest_OptionalFields(t *testing.T) {
	temp := 0.7
	topP := 0.9
	maxTok := 4096
	req := &llm.Request{
		Model:         "gpt-4.1",
		Messages:      []llm.Message{llm.UserMessage("hi")},
		Temperature:   &temp,
		TopP:          &topP,
		MaxTokens:     &maxTok,
		StopSequences: []string{"END", "STOP"},
	}

	body, err := translateRequest(req, false)
	if err != nil {
		t.Fatal(err)
	}

	var raw map[string]any
	if err := json.Unmarshal(body, &raw); err != nil {
		t.Fatal(err)
	}

	if raw["temperature"] != 0.7 {
		t.Errorf("temperature = %v, want 0.7", raw["temperature"])
	}
	if raw["top_p"] != 0.9 {
		t.Errorf("top_p = %v, want 0.9", raw["top_p"])
	}
	if int(raw["max_tokens"].(float64)) != 4096 {
		t.Errorf("max_tokens = %v, want 4096", raw["max_tokens"])
	}
	stop, ok := raw["stop"].([]any)
	if !ok || len(stop) != 2 {
		t.Fatalf("stop = %v, want [END, STOP]", raw["stop"])
	}
	if stop[0] != "END" || stop[1] != "STOP" {
		t.Errorf("stop = %v, want [END, STOP]", stop)
	}
}
```

- [ ] **Step 3: Add base URL /v1 normalization test**

Add to `adapter_test.go`:

```go
func TestNew_BaseURLNormalization(t *testing.T) {
	tests := []struct {
		input    string
		expected string
	}{
		{"http://localhost:1234/v1", "http://localhost:1234"},
		{"http://localhost:1234", "http://localhost:1234"},
		{"https://openrouter.ai/api", "https://openrouter.ai/api"},
		{"https://api.example.com/v1", "https://api.example.com"},
	}

	for _, tt := range tests {
		adapter := New("key", WithBaseURL(tt.input))
		if adapter.baseURL != tt.expected {
			t.Errorf("New(WithBaseURL(%q)).baseURL = %q, want %q", tt.input, adapter.baseURL, tt.expected)
		}
	}
}
```

- [ ] **Step 4: Run all new tests**

Run: `go test ./llm/openaicompat/ -run "TestComplete_ServerError|TestTranslateRequest_OptionalFields|TestNew_BaseURLNormalization" -v`
Expected: All PASS.

- [ ] **Step 5: Commit**

```bash
git add llm/openaicompat/adapter_test.go llm/openaicompat/translate_test.go
git commit -m "test: add coverage for 5xx errors, request fields, and base URL normalization"
```

---

### Task 9: Simplify the errorAs test helper

**Files:**
- Modify: `llm/openaicompat/adapter_test.go`

Replace the 40-line Rube Goldberg machine with direct `errors.As` calls. The cat insists.

- [ ] **Step 1: Remove the errorAs helper functions (lines 498-540)**

Delete these functions from `adapter_test.go`:
- `errorAs`
- `errorAsImpl`
- `asAuthError`
- `asRateLimitError`
- `extractError`
- `errorsAs`

- [ ] **Step 2: Replace the two call sites with direct errors.As**

In `TestComplete_HTTPError` (around line 131), replace:
```go
	var authErr *llm.AuthenticationError
	if !errorAs(err, &authErr) {
```
with:
```go
	var authErr *llm.AuthenticationError
	if !errors.As(err, &authErr) {
```

In `TestStream_HTTPError` (around line 329), replace:
```go
	var rateErr *llm.RateLimitError
	if !errorAs(evt.Err, &rateErr) {
```
with:
```go
	var rateErr *llm.RateLimitError
	if !errors.As(evt.Err, &rateErr) {
```

- [ ] **Step 3: Run tests to verify nothing broke**

Run: `go test ./llm/openaicompat/ -v`
Expected: All tests pass.

- [ ] **Step 4: Commit**

```bash
git add llm/openaicompat/adapter_test.go
git commit -m "refactor: replace errorAs helper pyramid with direct errors.As calls"
```

---

### Task 10: Final verification

- [ ] **Step 1: Run full build**

Run: `go build ./...`
Expected: Clean.

- [ ] **Step 2: Run full test suite**

Run: `go test ./... -short`
Expected: All packages pass.

- [ ] **Step 3: Run vet/lint**

Run: `go vet ./...`
Expected: Clean.

- [ ] **Step 4: Verify test count increased**

Run: `go test ./llm/openaicompat/ -v 2>&1 | grep "^--- PASS" | wc -l`
Expected: Should be ~25+ tests (up from 17).

---

## Decisions Deferred to Doctor Biz

These were flagged by reviewers but require a product decision:

1. **`OPENAI_API_KEY` fallback** — Spec says add it, some reviewers say don't (to avoid surprise routing to OpenRouter). Pick one, update both spec and code to match.

2. **Default base URL** — VP Tech says OpenRouter default is a data exfiltration risk for enterprise. Options: keep OpenRouter (startup audience), change to empty/localhost (enterprise audience), or require explicit config (safest). This is a product positioning call.

3. **`tracker setup` wizard** — Adding openai-compat to the interactive setup is a UI change that should be its own task after the above decisions are made.

4. **CHANGELOG.md entry** — Should be written after all fixes land and the above decisions are resolved.
