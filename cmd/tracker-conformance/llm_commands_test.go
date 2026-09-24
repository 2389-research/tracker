// ABOUTME: Tests the LLM subcommands' stdout JSON, stderr text and exit codes.
// ABOUTME: A local server stands in for the Anthropic API so success and failure paths both run.
package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"maps"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

const benchInput = `{"model": "claude-opus-4-6", "provider": "anthropic", "messages": [{"role": "user", "content": "Hi"}]}`

// clearProviderEnv removes every provider key, base URL and gateway setting so
// the test alone decides what client createClient builds.
func clearProviderEnv(t *testing.T) {
	t.Helper()
	for _, k := range []string{
		"ANTHROPIC_API_KEY", "OPENAI_API_KEY", "GEMINI_API_KEY", "GOOGLE_API_KEY", "OPENAI_COMPAT_API_KEY",
		"ANTHROPIC_BASE_URL", "OPENAI_BASE_URL", "GEMINI_BASE_URL", "OPENAI_COMPAT_BASE_URL",
		"TRACKER_GATEWAY_URL", "TRACKER_GATEWAY_KIND",
	} {
		t.Setenv(k, "")
	}
}

// serveAnthropic points the Anthropic client at a local server that answers
// every request with status and body.
func serveAnthropic(t *testing.T, status int, body string) {
	t.Helper()
	clearProviderEnv(t)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		fmt.Fprint(w, body)
	}))
	t.Cleanup(srv.Close)
	t.Setenv("ANTHROPIC_API_KEY", "test-key")
	t.Setenv("ANTHROPIC_BASE_URL", srv.URL)
}

// anthropicReply is a Messages API response whose only content is text.
func anthropicReply(text string) string {
	quoted, _ := json.Marshal(text)
	return `{"id": "msg_test", "model": "claude-opus-4-6", "type": "message", "role": "assistant",
		"content": [{"type": "text", "text": ` + string(quoted) + `}],
		"stop_reason": "end_turn", "usage": {"input_tokens": 5, "output_tokens": 3}}`
}

// runSubcommand runs one subcommand and decodes its stdout as a JSON object.
func runSubcommand(t *testing.T, sub string) (code int, out map[string]any, stderr string) {
	t.Helper()
	var so, se bytes.Buffer
	code = run([]string{"tracker-conformance", sub}, strings.NewReader(benchInput), &so, &se)
	if err := json.Unmarshal(so.Bytes(), &out); err != nil {
		t.Fatalf("%s: stdout is not a JSON object: %q", sub, so.String())
	}
	return code, out, se.String()
}

// assertReportedError checks the failure contract: exit 1, stdout carrying
// {"error": prefix + cause}, and stderr carrying "error: " + cause.
func assertReportedError(t *testing.T, sub string, code int, out map[string]any, stderr, prefix string) {
	t.Helper()
	cause, ok := strings.CutPrefix(strings.TrimSuffix(stderr, "\n"), "error: ")
	if code != 1 || !ok || cause == "" {
		t.Fatalf("%s: exit %d, stderr %q; want exit 1 and an error line", sub, code, stderr)
	}
	want := map[string]any{"error": prefix + cause}
	if !maps.Equal(out, want) {
		t.Errorf("%s: stdout %v, want %v", sub, out, want)
	}
}

func TestLLMSubcommandsReportClientCreationFailure(t *testing.T) {
	clearProviderEnv(t)
	if _, err := createClient(); err == nil {
		t.Fatal("expected createClient to fail with no provider keys")
	}
	for _, sub := range []string{"complete", "stream", "tool-call", "generate-object", "session-create", "process-input"} {
		code, out, stderr := runSubcommand(t, sub)
		assertReportedError(t, sub, code, out, stderr, "client creation failed: ")
	}
}

func TestClientFromEnvReportsClientCreationFailure(t *testing.T) {
	clearProviderEnv(t)
	t.Setenv("ANTHROPIC_API_KEY", "test-key")
	t.Setenv("TRACKER_GATEWAY_URL", "https://gateway.example")
	t.Setenv("TRACKER_GATEWAY_KIND", "definitely-not-a-real-kind")

	code, out, stderr := runSubcommand(t, "client-from-env")
	assertReportedError(t, "client-from-env", code, out, stderr, "client creation failed: ")
}

func TestLLMSubcommandsReportCompletionFailure(t *testing.T) {
	serveAnthropic(t, http.StatusUnauthorized, `{"type": "error", "error": {"type": "authentication_error", "message": "invalid x-api-key"}}`)
	for _, sub := range []string{"complete", "tool-call", "generate-object"} {
		code, out, stderr := runSubcommand(t, sub)
		assertReportedError(t, sub, code, out, stderr, "completion failed: ")
		if !strings.Contains(stderr, "invalid x-api-key") {
			t.Errorf("%s: stderr %q lacks the API's error message", sub, stderr)
		}
	}
}

func TestCompleteAndToolCallWriteTheSameResponse(t *testing.T) {
	serveAnthropic(t, http.StatusOK, anthropicReply("Hello from test!"))

	code, complete, stderr := runSubcommand(t, "complete")
	if code != 0 {
		t.Fatalf("complete: exit %d, stderr %q", code, stderr)
	}
	if complete["id"] != "msg_test" || complete["text"] != "Hello from test!" || complete["provider"] != "anthropic" {
		t.Errorf("complete: unexpected response %v", complete)
	}

	code, toolCall, stderr := runSubcommand(t, "tool-call")
	if code != 0 {
		t.Fatalf("tool-call: exit %d, stderr %q", code, stderr)
	}
	if fmt.Sprint(toolCall) != fmt.Sprint(complete) {
		t.Errorf("tool-call response %v differs from complete response %v", toolCall, complete)
	}
}

func TestGenerateObjectWritesParsedOrRawText(t *testing.T) {
	serveAnthropic(t, http.StatusOK, anthropicReply(`{"answer": 42}`))
	code, out, stderr := runSubcommand(t, "generate-object")
	if code != 0 || fmt.Sprint(out) != fmt.Sprint(map[string]any{"answer": float64(42)}) {
		t.Errorf("generate-object JSON reply: exit %d, stdout %v, stderr %q", code, out, stderr)
	}

	serveAnthropic(t, http.StatusOK, anthropicReply("not json"))
	code, out, stderr = runSubcommand(t, "generate-object")
	if code != 0 || fmt.Sprint(out) != fmt.Sprint(map[string]any{"raw_text": "not json"}) {
		t.Errorf("generate-object text reply: exit %d, stdout %v, stderr %q", code, out, stderr)
	}
}
