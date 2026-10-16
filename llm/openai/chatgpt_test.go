// ABOUTME: Tests for the Responses adapter's ChatGPT-subscription mode against an httptest backend:
// ABOUTME: request shape, session id, 401 refresh, usage-limit and credential errors, usage label.
package openai

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"regexp"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/2389-research/tracker/llm"
)

// testUserAgent stands in for chatgptauth.UserAgent().
const testUserAgent = "tracker/v0.0.0-test (testos; testarch)"

// usageLimitBody is the Codex backend's usage-limit 429 body
// (codex-rs/codex-api/src/api_bridge.rs:340-352).
const usageLimitBody = `{"error":{"type":"usage_limit_reached","plan_type":"plus","resets_at":1760558400}}`

// unservedModelBody is the Codex backend's error for a model name the
// signed-in plan doesn't serve — recorded 2026-10-16 against gpt-4.1-mini
// under the self_serve_business_prolite plan (design doc open item 3).
const unservedModelBody = `{"detail":"The 'gpt-4.1-mini' model is not supported when using Codex with a ChatGPT account."}`

// fakeChatGPTCreds is a ChatGPTCredentials that counts its ForceRefresh
// calls. Token hands out access and account; ForceRefresh records the token
// it was handed and swaps access for refreshed. tokenErr and refreshErr, when
// set, come back instead.
type fakeChatGPTCreds struct {
	mu           sync.Mutex
	access       string
	account      string
	refreshed    string
	tokenErr     error
	refreshErr   error
	refreshCalls int
	rejected     []string
}

func (f *fakeChatGPTCreds) Token(context.Context) (string, string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.tokenErr != nil {
		return "", "", f.tokenErr
	}
	return f.access, f.account, nil
}

func (f *fakeChatGPTCreds) ForceRefresh(_ context.Context, rejected string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.refreshCalls++
	f.rejected = append(f.rejected, rejected)
	if f.refreshErr != nil {
		return f.refreshErr
	}
	f.access = f.refreshed
	return nil
}

// refreshes reports how many times ForceRefresh ran and the tokens it was handed.
func (f *fakeChatGPTCreds) refreshes() (int, []string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.refreshCalls, append([]string(nil), f.rejected...)
}

// signedIn returns credentials holding access token a-1 for account acct-1,
// which a refresh replaces with a-2.
func signedIn() *fakeChatGPTCreds {
	return &fakeChatGPTCreds{access: "a-1", account: "acct-1", refreshed: "a-2"}
}

// temporaryErr imitates chatgptauth.RefreshError: Temporary reports whether a
// retry may succeed, and Unwrap exposes the failure behind it.
type temporaryErr struct {
	msg   string
	temp  bool
	cause error
}

func (e *temporaryErr) Error() string   { return e.msg }
func (e *temporaryErr) Temporary() bool { return e.temp }
func (e *temporaryErr) Unwrap() error   { return e.cause }

// backendReply is one scripted answer from the stand-in Codex backend.
type backendReply struct {
	status int
	body   string
}

// backendRequest is what the stand-in backend saw of one request.
type backendRequest struct {
	path   string
	header http.Header
	body   map[string]any
}

// scriptedBackend stands in for the ChatGPT Codex backend: it answers the
// n-th request with the n-th reply (the last reply repeats) and records each
// request it sees.
type scriptedBackend struct {
	*httptest.Server
	mu       sync.Mutex
	replies  []backendReply
	requests []backendRequest
}

func newScriptedBackend(t *testing.T, replies ...backendReply) *scriptedBackend {
	t.Helper()
	b := &scriptedBackend{replies: replies}
	b.Server = httptest.NewServer(http.HandlerFunc(b.serve))
	t.Cleanup(b.Close)
	return b
}

func (b *scriptedBackend) serve(w http.ResponseWriter, r *http.Request) {
	raw, _ := io.ReadAll(r.Body)
	var body map[string]any
	_ = json.Unmarshal(raw, &body)
	b.mu.Lock()
	reply := b.replies[min(len(b.requests), len(b.replies)-1)]
	b.requests = append(b.requests, backendRequest{path: r.URL.Path, header: r.Header.Clone(), body: body})
	b.mu.Unlock()
	if reply.status == http.StatusOK {
		w.Header().Set("Content-Type", "text/event-stream")
	} else {
		w.Header().Set("Content-Type", "application/json")
	}
	w.WriteHeader(reply.status)
	io.WriteString(w, reply.body)
}

// seen returns a copy of the requests recorded so far.
func (b *scriptedBackend) seen() []backendRequest {
	b.mu.Lock()
	defer b.mu.Unlock()
	return append([]backendRequest(nil), b.requests...)
}

// sseLines joins SSE lines into a 200 reply.
func sseLines(lines ...string) backendReply {
	return backendReply{status: http.StatusOK, body: strings.Join(lines, "\n")}
}

// streamHi is a complete backend stream that answers "hi".
func streamHi() backendReply {
	return sseLines(
		"event: response.created",
		`data: {"type":"response.created","response":{"id":"resp_chatgpt","model":"gpt-5.6-sol"}}`,
		"",
		"event: response.output_text.delta",
		`data: {"type":"response.output_text.delta","output_index":0,"content_index":0,"delta":"hi"}`,
		"",
		"event: response.completed",
		`data: {"type":"response.completed","response":{"id":"resp_chatgpt","status":"completed","usage":{"input_tokens":12,"output_tokens":3,"total_tokens":15},"output":[]}}`,
		"",
	)
}

// reply401 is the backend refusing the access token.
func reply401() backendReply {
	return backendReply{status: http.StatusUnauthorized, body: `{"error":{"message":"unauthorized"}}`}
}

// chatgptAdapter builds a ChatGPT-mode adapter aimed at backend.
func chatgptAdapter(backend *scriptedBackend, creds ChatGPTCredentials, opts ...Option) *Adapter {
	return New("", append([]Option{WithChatGPTAuth(creds, testUserAgent), WithBaseURL(backend.URL)}, opts...)...)
}

func helloRequest() *llm.Request {
	return &llm.Request{Model: "gpt-5.6-sol", Messages: []llm.Message{llm.UserMessage("hello")}}
}

// testCtx bounds a test's adapter calls, so a hung stream fails the test
// instead of stalling the run.
func testCtx(t *testing.T) context.Context {
	t.Helper()
	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	t.Cleanup(cancel)
	return ctx
}

// collect reads a stream to its end and returns its events and first error.
func collect(ch <-chan llm.StreamEvent) ([]llm.StreamEvent, error) {
	var events []llm.StreamEvent
	var firstErr error
	for evt := range ch {
		events = append(events, evt)
		if evt.Err != nil && firstErr == nil {
			firstErr = evt.Err
		}
	}
	return events, firstErr
}

// holdOpenBackend answers with body as a 200 event stream, then holds the
// reply open until the client hangs up, which closes hungUp. A 5 s safety
// timeout ends the handler if the client never does.
func holdOpenBackend(t *testing.T, body string) (*httptest.Server, <-chan struct{}) {
	t.Helper()
	hungUp := make(chan struct{})
	var once sync.Once
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// The server watches for a hang-up only after the request body has
		// been read to its end.
		_, _ = io.Copy(io.Discard, r.Body)
		w.Header().Set("Content-Type", "text/event-stream")
		io.WriteString(w, body)
		w.(http.Flusher).Flush()
		select {
		case <-r.Context().Done():
			once.Do(func() { close(hungUp) })
		case <-time.After(5 * time.Second):
		}
	}))
	t.Cleanup(srv.Close)
	return srv, hungUp
}

// markerTransport sends requests through http.DefaultTransport and closes
// reached once a response body has delivered marker, so a test can act at a
// known point in a reply.
type markerTransport struct {
	marker  string
	reached chan struct{}
}

func (m markerTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	resp, err := http.DefaultTransport.RoundTrip(req)
	if err == nil {
		resp.Body = &markerBody{ReadCloser: resp.Body, marker: m.marker, reached: m.reached}
	}
	return resp, err
}

// markerBody closes reached once the bytes read so far contain marker. Only
// the stream goroutine reads it.
type markerBody struct {
	io.ReadCloser
	marker  string
	reached chan struct{}
	seen    []byte
	once    sync.Once
}

func (b *markerBody) Read(p []byte) (int, error) {
	n, err := b.ReadCloser.Read(p)
	b.seen = append(b.seen, p[:n]...)
	if strings.Contains(string(b.seen), b.marker) {
		b.once.Do(func() { close(b.reached) })
	}
	return n, err
}

func TestChatGPTRequestShape(t *testing.T) {
	backend := newScriptedBackend(t, streamHi())
	// A WithExtraHeaders header authenticates to a gateway, which this mode
	// never uses, so it must not reach the backend.
	gateway := WithExtraHeaders(map[string]string{"cf-aig-authorization": "Bearer gateway-token"})
	a := chatgptAdapter(backend, signedIn(), gateway)
	maxTokens, temperature, topP := 100, 0.2, 0.9
	req := &llm.Request{
		Model:    "gpt-5.6-sol",
		Messages: []llm.Message{llm.SystemMessage("You are terse."), llm.UserMessage("hello")},
		Tools: []llm.ToolDefinition{{
			Name:        "read_file",
			Description: "Read a file",
			Parameters:  json.RawMessage(`{"type":"object","properties":{"path":{"type":"string"}}}`),
		}},
		MaxTokens:     &maxTokens,
		Temperature:   &temperature,
		TopP:          &topP,
		StopSequences: []string{"X"},
	}
	if _, err := collect(a.Stream(testCtx(t), req)); err != nil {
		t.Fatalf("stream: %v", err)
	}

	seen := backend.seen()
	if len(seen) != 1 {
		t.Fatalf("backend saw %d requests, want 1", len(seen))
	}
	got := seen[0]
	if got.path != "/responses" {
		t.Errorf("path = %q, want /responses", got.path)
	}

	if v, ok := got.body["store"]; !ok || v != false {
		t.Errorf("store = %v (present: %v), want false", v, ok)
	}
	if got.body["stream"] != true {
		t.Errorf("stream = %v, want true", got.body["stream"])
	}
	if include, _ := json.Marshal(got.body["include"]); string(include) != `["reasoning.encrypted_content"]` {
		t.Errorf("include = %s, want [\"reasoning.encrypted_content\"]", include)
	}
	sessionID := got.header.Get("session-id")
	if !regexp.MustCompile(`^[0-9a-f]{32}$`).MatchString(sessionID) {
		t.Errorf("session-id = %q, want 32 hex characters", sessionID)
	}
	if got.body["prompt_cache_key"] != sessionID {
		t.Errorf("prompt_cache_key = %v, want the session-id %q", got.body["prompt_cache_key"], sessionID)
	}
	if got.body["instructions"] != "You are terse." {
		t.Errorf("instructions = %v, want the system prompt", got.body["instructions"])
	}
	if got.body["model"] != "gpt-5.6-sol" {
		t.Errorf("model = %v, want gpt-5.6-sol", got.body["model"])
	}
	for _, key := range []string{"input", "tools"} {
		if _, ok := got.body[key]; !ok {
			t.Errorf("body lacks %q", key)
		}
	}
	for _, key := range []string{"max_output_tokens", "temperature", "top_p", "stop"} {
		if v, ok := got.body[key]; ok {
			t.Errorf("body has %q = %v; the Codex backend's request has no such field", key, v)
		}
	}

	wantHeaders := map[string]string{
		"Authorization":      "Bearer a-1",
		"ChatGPT-Account-ID": "acct-1",
		"originator":         "tracker",
		"User-Agent":         testUserAgent,
		"Content-Type":       "application/json",
	}
	for name, want := range wantHeaders {
		if v := got.header.Get(name); v != want {
			t.Errorf("header %s = %q, want %q", name, v, want)
		}
	}
	for _, name := range []string{"OpenAI-Beta", "cf-aig-authorization"} {
		if vals := got.header.Values(name); len(vals) > 0 {
			t.Errorf("header %s = %q, want none", name, vals)
		}
	}
}

func TestChatGPTSessionIDStable(t *testing.T) {
	backend := newScriptedBackend(t, streamHi())
	first := chatgptAdapter(backend, signedIn())
	second := chatgptAdapter(backend, signedIn())
	for _, a := range []*Adapter{first, first, second} {
		if _, err := collect(a.Stream(testCtx(t), helloRequest())); err != nil {
			t.Fatalf("stream: %v", err)
		}
	}

	seen := backend.seen()
	if len(seen) != 3 {
		t.Fatalf("backend saw %d requests, want 3", len(seen))
	}
	a1, a2, b1 := seen[0].header.Get("session-id"), seen[1].header.Get("session-id"), seen[2].header.Get("session-id")
	if a1 == "" || a1 != a2 {
		t.Errorf("one adapter sent session ids %q and %q, want one stable id", a1, a2)
	}
	if b1 == a1 {
		t.Errorf("two adapters shared session id %q, want one per adapter", a1)
	}
}

func TestChatGPTOmitsEmptyAccountID(t *testing.T) {
	backend := newScriptedBackend(t, streamHi())
	a := chatgptAdapter(backend, &fakeChatGPTCreds{access: "a-1"})
	if _, err := collect(a.Stream(testCtx(t), helloRequest())); err != nil {
		t.Fatalf("stream: %v", err)
	}
	if vals := backend.seen()[0].header.Values("ChatGPT-Account-ID"); len(vals) > 0 {
		t.Errorf("ChatGPT-Account-ID = %q, want no header when the account is unknown", vals)
	}
}

func TestChatGPTCompleteStreams(t *testing.T) {
	backend := newScriptedBackend(t, streamHi())
	resp, err := chatgptAdapter(backend, signedIn()).Complete(testCtx(t), helloRequest())
	if err != nil {
		t.Fatalf("Complete: %v", err)
	}
	if resp.Text() != "hi" {
		t.Errorf("text = %q, want hi", resp.Text())
	}
	if resp.Usage.InputTokens != 12 || resp.Usage.OutputTokens != 3 {
		t.Errorf("usage = %+v, want 12 in / 3 out", resp.Usage)
	}
	if resp.ID != "resp_chatgpt" {
		t.Errorf("ID = %q, want resp_chatgpt from response.created", resp.ID)
	}
	if resp.Provider != llm.OpenAISubscription {
		t.Errorf("Provider = %q, want %q", resp.Provider, llm.OpenAISubscription)
	}
	if stream := backend.seen()[0].body["stream"]; stream != true {
		t.Errorf("request stream = %v, want true: the backend only streams", stream)
	}
}

func TestChatGPTCompleteCutShortFails(t *testing.T) {
	backend, _ := holdOpenBackend(t, strings.Join([]string{
		"event: response.created",
		`data: {"type":"response.created","response":{"id":"resp_cut","model":"gpt-5.6-sol"}}`,
		"",
		"event: response.output_text.delta",
		`data: {"type":"response.output_text.delta","output_index":0,"content_index":0,"delta":"hi"}`,
		"",
	}, "\n"))
	deltaRead := make(chan struct{})
	client := &http.Client{Transport: markerTransport{marker: `"delta":"hi"`, reached: deltaRead}}
	ctx, cancel := context.WithCancel(testCtx(t))
	go func() {
		// The caller gives up after the first delta, before response.completed.
		select {
		case <-deltaRead:
		case <-ctx.Done():
		}
		cancel()
	}()

	a := New("", WithChatGPTAuth(signedIn(), testUserAgent), WithBaseURL(backend.URL), WithHTTPClient(client))
	resp, err := a.Complete(ctx, helloRequest())
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("Complete = %+v, %v; want context.Canceled, not a partial reply", resp, err)
	}
}

func TestChatGPTCompleteHangsUpAfterStreamError(t *testing.T) {
	lines := []string{
		"event: response.created",
		`data: {"type":"response.created","response":{"id":"resp_bad","model":"gpt-5.6-sol"}}`,
		"",
		"event: response.output_text.delta",
		`data: {not json`,
		"",
	}
	// More good events than the stream's 64-slot channel holds.
	for range 100 {
		lines = append(lines,
			"event: response.output_text.delta",
			`data: {"type":"response.output_text.delta","output_index":0,"content_index":0,"delta":"x"}`,
			"",
		)
	}
	backend, hungUp := holdOpenBackend(t, strings.Join(lines, "\n"))

	a := New("", WithChatGPTAuth(signedIn(), testUserAgent), WithBaseURL(backend.URL))
	_, err := a.Complete(testCtx(t), helloRequest())
	if err == nil || !strings.Contains(err.Error(), "parse output_text.delta") {
		t.Fatalf("err = %v, want the malformed delta's parse error", err)
	}
	select {
	case <-hungUp:
	case <-time.After(5 * time.Second):
		t.Fatal("the backend never saw the client hang up: the stream goroutine still holds the connection")
	}
}

func TestChatGPTIgnoresMetadataEvent(t *testing.T) {
	// The ChatGPT backend adds response.metadata to the public Responses
	// events (research §G, codex-rs/codex-api/src/sse/responses.rs:211-253).
	// Only its nested "type": "safety_buffering" is documented; the rest of
	// the payload is illustrative, since the parser routes on the event name.
	backend := newScriptedBackend(t, sseLines(
		"event: response.created",
		`data: {"type":"response.created","response":{"id":"resp_meta","model":"gpt-5.6-sol"}}`,
		"",
		"event: response.output_text.delta",
		`data: {"type":"response.output_text.delta","output_index":0,"content_index":0,"delta":"Hel"}`,
		"",
		"event: response.metadata",
		`data: {"type":"response.metadata","metadata":{"type":"safety_buffering"}}`,
		"",
		"event: response.output_text.delta",
		`data: {"type":"response.output_text.delta","output_index":0,"content_index":0,"delta":"lo"}`,
		"",
		"event: response.completed",
		`data: {"type":"response.completed","response":{"id":"resp_meta","status":"completed","usage":{"input_tokens":5,"output_tokens":2,"total_tokens":7},"output":[]}}`,
		"",
	))
	events, err := collect(chatgptAdapter(backend, signedIn()).Stream(testCtx(t), helloRequest()))
	if err != nil {
		t.Fatalf("stream error: %v", err)
	}

	acc := llm.NewStreamAccumulator()
	var providerEvents []llm.StreamEvent
	for _, evt := range events {
		acc.Process(evt)
		if evt.Type == llm.EventProviderEvent {
			providerEvents = append(providerEvents, evt)
		}
	}
	if text := acc.Response().Text(); text != "Hello" {
		t.Errorf("text = %q, want Hello", text)
	}
	// response.metadata falls to the provider-event default in
	// handleSSEDataOtherEvents; untraced, it is the only provider event.
	if len(providerEvents) != 1 || !strings.Contains(string(providerEvents[0].Raw), "safety_buffering") {
		t.Errorf("provider events = %+v, want exactly the response.metadata payload", providerEvents)
	}
}

func TestChatGPTRefreshesOnceOn401(t *testing.T) {
	backend := newScriptedBackend(t, reply401(), streamHi())
	creds := signedIn()
	resp, err := chatgptAdapter(backend, creds).Complete(testCtx(t), helloRequest())
	if err != nil {
		t.Fatalf("Complete: %v", err)
	}
	if resp.Text() != "hi" {
		t.Errorf("text = %q, want hi", resp.Text())
	}

	calls, rejected := creds.refreshes()
	if calls != 1 || len(rejected) != 1 || rejected[0] != "a-1" {
		t.Errorf("ForceRefresh ran %d times with %q, want once with a-1", calls, rejected)
	}
	seen := backend.seen()
	if len(seen) != 2 {
		t.Fatalf("backend saw %d requests, want 2", len(seen))
	}
	if auth := seen[0].header.Get("Authorization"); auth != "Bearer a-1" {
		t.Errorf("first request Authorization = %q, want Bearer a-1", auth)
	}
	if auth := seen[1].header.Get("Authorization"); auth != "Bearer a-2" {
		t.Errorf("retry Authorization = %q, want Bearer a-2", auth)
	}
}

func TestChatGPTSecond401Fails(t *testing.T) {
	backend := newScriptedBackend(t, reply401())
	creds := signedIn()
	_, err := chatgptAdapter(backend, creds).Complete(testCtx(t), helloRequest())

	var authErr *llm.AuthenticationError
	if !errors.As(err, &authErr) {
		t.Fatalf("err = %T %v, want *llm.AuthenticationError", err, err)
	}
	if !strings.Contains(err.Error(), "tracker setup") {
		t.Errorf("err = %q, want it to send the user to tracker setup", err)
	}
	if calls, _ := creds.refreshes(); calls != 1 {
		t.Errorf("ForceRefresh ran %d times, want 1", calls)
	}
	if n := len(backend.seen()); n != 2 {
		t.Errorf("backend saw %d requests, want 2 (the request and one retry)", n)
	}
}

func TestChatGPTUsageLimitPausesNotRetries(t *testing.T) {
	backend := newScriptedBackend(t, backendReply{status: http.StatusTooManyRequests, body: usageLimitBody})
	client, err := llm.NewClient(
		llm.WithProvider(chatgptAdapter(backend, signedIn())),
		// The short base delay only keeps a regression (a retried usage limit)
		// quick to fail.
		llm.WithMiddleware(llm.NewRetryMiddleware(llm.WithMaxRetries(3), llm.WithBaseDelay(time.Millisecond))),
	)
	if err != nil {
		t.Fatalf("NewClient: %v", err)
	}
	_, err = client.Complete(testCtx(t), helloRequest())

	var limitErr *llm.UsageLimitError
	if !errors.As(err, &limitErr) {
		t.Fatalf("err = %T %v, want *llm.UsageLimitError", err, err)
	}
	if !llm.IsUsageLimit(err) {
		t.Errorf("IsUsageLimit(%q) = false, want true", err)
	}
	wantReset := time.Unix(1760558400, 0).UTC()
	if reset, ok := llm.UsageLimitResetAt(err); !ok || !reset.Equal(wantReset) {
		t.Errorf("UsageLimitResetAt = %v, %v; want %v", reset, ok, wantReset)
	}
	if !limitErr.ResetsAt.Equal(wantReset) || limitErr.PlanType != "plus" {
		t.Errorf("ResetsAt = %v, PlanType = %q; want %v, plus", limitErr.ResetsAt, limitErr.PlanType, wantReset)
	}
	if llm.IsBillingError(err) {
		t.Error("IsBillingError = true; a usage limit is not credit exhaustion")
	}
	if n := len(backend.seen()); n != 1 {
		t.Errorf("backend saw %d requests, want 1: a usage limit must not be retried", n)
	}
}

func TestChatGPTUsageNotIncluded(t *testing.T) {
	backend := newScriptedBackend(t, backendReply{status: http.StatusTooManyRequests, body: `{"error":{"type":"usage_not_included"}}`})
	_, err := chatgptAdapter(backend, signedIn()).Complete(testCtx(t), helloRequest())

	var denied *llm.AccessDeniedError
	if !errors.As(err, &denied) {
		t.Fatalf("err = %T %v, want *llm.AccessDeniedError", err, err)
	}
	if want := "openai: your ChatGPT plan does not include Codex usage"; err.Error() != want {
		t.Errorf("err = %q, want %q", err, want)
	}
}

func TestChatGPTStatusErrorMapping(t *testing.T) {
	reset := time.Unix(1760558400, 0).UTC()
	tests := []struct {
		name          string
		status        int
		body          string
		wantMsg       string    // exact message; "" skips the check
		wantReset     time.Time // for a usage-limit error; zero means unknown
		wantRetryable bool
		wantUsage     bool
		wantBilling   bool
	}{
		{
			name: "plan and reset", status: 429, body: usageLimitBody,
			wantMsg:   "openai: ChatGPT usage limit reached (plus plan), resets at 2025-10-15T20:00:00Z",
			wantReset: reset, wantUsage: true,
		},
		{
			name: "no plan or reset", status: 429, body: `{"error":{"type":"usage_limit_reached"}}`,
			wantMsg: "openai: ChatGPT usage limit reached", wantUsage: true,
		},
		{
			name: "reset only", status: 429, body: `{"error":{"type":"usage_limit_reached","resets_at":1760558400}}`,
			wantMsg:   "openai: ChatGPT usage limit reached, resets at 2025-10-15T20:00:00Z",
			wantReset: reset, wantUsage: true,
		},
		{
			name: "a plain rate limit stays retryable", status: 429, body: `{"error":{"message":"Rate limit reached"}}`,
			wantRetryable: true,
		},
		{
			name: "insufficient_quota takes the billing path", status: 429,
			body:        `{"error":{"type":"insufficient_quota","message":"You exceeded your current quota"}}`,
			wantBilling: true,
		},
		{
			name: "an unparseable 429 stays retryable", status: 429, body: "Too Many Requests",
			wantRetryable: true,
		},
		{
			name: "unserved model (400) uses detail as the message", status: 400, body: unservedModelBody,
			wantMsg: "openai: The 'gpt-4.1-mini' model is not supported when using Codex with a ChatGPT account.",
		},
		{
			name: "unserved model (404) uses detail as the message", status: 404, body: unservedModelBody,
			wantMsg: "openai: The 'gpt-4.1-mini' model is not supported when using Codex with a ChatGPT account.",
		},
		{
			name: "a body without detail keeps the raw message", status: 400, body: `{"error":"bad request"}`,
			wantMsg: `{"error":"bad request"}`,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			err := chatgptStatusError(tc.status, []byte(tc.body), http.Header{})
			if err == nil {
				t.Fatal("chatgptStatusError returned nil")
			}
			if tc.wantMsg != "" && err.Error() != tc.wantMsg {
				t.Errorf("message = %q, want %q", err, tc.wantMsg)
			}
			if strings.Contains(err.Error(), `{"detail"`) {
				t.Errorf("message leaks the raw detail envelope: %q", err)
			}
			var r interface{ Retryable() bool }
			if retryable := errors.As(err, &r) && r.Retryable(); retryable != tc.wantRetryable {
				t.Errorf("retryable = %v, want %v (%T)", retryable, tc.wantRetryable, err)
			}
			if got := llm.IsUsageLimit(err); got != tc.wantUsage {
				t.Errorf("IsUsageLimit = %v, want %v", got, tc.wantUsage)
			}
			if got := llm.IsBillingError(err); got != tc.wantBilling {
				t.Errorf("IsBillingError = %v, want %v", got, tc.wantBilling)
			}
			var limitErr *llm.UsageLimitError
			if errors.As(err, &limitErr) && !limitErr.ResetsAt.Equal(tc.wantReset) {
				t.Errorf("ResetsAt = %v, want %v", limitErr.ResetsAt, tc.wantReset)
			}
		})
	}
}

func TestChatGPTCredentialErrors(t *testing.T) {
	revoked := &temporaryErr{msg: "ChatGPT sign-in expired or was revoked (refresh_token_expired); run `tracker setup` to sign in again"}
	tests := []struct {
		name         string
		creds        *fakeChatGPTCreds
		runCancelled bool // the run's context ends before the call
		reply        backendReply
		want         string // "configuration", "network" or "cancelled"
		wantRequests int
	}{
		{
			name:  "a plain error is fatal",
			creds: &fakeChatGPTCreds{tokenErr: errors.New("not signed in to ChatGPT; run `tracker setup`")},
			reply: streamHi(), want: "configuration",
		},
		{
			name:  "a temporary error is retryable",
			creds: &fakeChatGPTCreds{tokenErr: &temporaryErr{msg: "refreshing the ChatGPT sign-in failed: HTTP 502", temp: true}},
			reply: streamHi(), want: "network",
		},
		{
			name:  "a permanent refresh error is fatal",
			creds: &fakeChatGPTCreds{tokenErr: revoked},
			reply: streamHi(), want: "configuration",
		},
		{
			// Only the run's own context marks the run's cancellation. While
			// the run is alive, a context error from the credentials is sorted
			// like any other, and context.Canceled has no Temporary method.
			name:  "a context error alone is not the run's cancellation",
			creds: &fakeChatGPTCreds{tokenErr: context.Canceled},
			reply: streamHi(), want: "configuration",
		},
		{
			// The auth client's own timeout matches errors.Is(err,
			// context.DeadlineExceeded) through RefreshError's Unwrap, but the
			// run is alive, so a slow auth server is a transient failure.
			name: "an auth-server timeout is retryable",
			creds: &fakeChatGPTCreds{tokenErr: &temporaryErr{
				msg:   `refreshing the ChatGPT sign-in failed: Post "https://auth.openai.com/oauth/token": context deadline exceeded (Client.Timeout exceeded while awaiting headers)`,
				temp:  true,
				cause: context.DeadlineExceeded,
			}},
			reply: streamHi(), want: "network",
		},
		{
			name: "the run's cancellation comes back unchanged",
			creds: &fakeChatGPTCreds{tokenErr: &temporaryErr{
				msg: "refreshing the ChatGPT sign-in failed: context canceled", temp: true, cause: context.Canceled,
			}},
			runCancelled: true,
			reply:        streamHi(), want: "cancelled",
		},
		{
			name:  "a refresh failure after a 401 is fatal",
			creds: &fakeChatGPTCreds{access: "a-1", account: "acct-1", refreshErr: revoked},
			reply: reply401(), want: "configuration", wantRequests: 1,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			backend := newScriptedBackend(t, tc.reply)
			ctx := testCtx(t)
			if tc.runCancelled {
				var cancel context.CancelFunc
				ctx, cancel = context.WithCancel(ctx)
				cancel()
			}
			_, err := chatgptAdapter(backend, tc.creds).Complete(ctx, helloRequest())
			cause := tc.creds.tokenErr
			if cause == nil {
				cause = tc.creds.refreshErr
			}

			switch tc.want {
			case "configuration":
				var ce *llm.ConfigurationError
				if !errors.As(err, &ce) {
					t.Fatalf("err = %T %v, want *llm.ConfigurationError", err, err)
				}
				if want := "openai: " + cause.Error(); err.Error() != want {
					t.Errorf("message = %q, want %q", err, want)
				}
			case "network":
				var ne *llm.NetworkError
				if !errors.As(err, &ne) || !ne.Retryable() {
					t.Fatalf("err = %T %v, want a retryable *llm.NetworkError", err, err)
				}
			case "cancelled":
				if err != context.Canceled {
					t.Fatalf("err = %T %v, want the run's own context.Canceled", err, err)
				}
			}
			if n := len(backend.seen()); n != tc.wantRequests {
				t.Errorf("backend saw %d requests, want %d", n, tc.wantRequests)
			}
		})
	}
}

func TestChatGPTErrorsNeverCarryToken(t *testing.T) {
	tests := []struct {
		name  string
		reply backendReply
	}{
		{"401 twice", reply401()},
		{"usage limit", backendReply{status: http.StatusTooManyRequests, body: usageLimitBody}},
		{"server error", backendReply{status: http.StatusInternalServerError, body: `{"error":{"message":"internal error"}}`}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			backend := newScriptedBackend(t, tc.reply)
			creds := &fakeChatGPTCreds{access: "SECRET-a-1", account: "acct-SECRET", refreshed: "SECRET-a-2"}
			_, err := chatgptAdapter(backend, creds).Complete(testCtx(t), helloRequest())
			if err == nil {
				t.Fatal("Complete succeeded, want an error")
			}
			for e := err; e != nil; e = errors.Unwrap(e) {
				if text := e.Error() + " " + fmt.Sprintf("%#v", e); strings.Contains(text, "SECRET") {
					t.Errorf("error carries a sign-in secret: %s", text)
				}
			}
		})
	}
}

func TestChatGPTUsageLabel(t *testing.T) {
	chatgpt := New("", WithChatGPTAuth(signedIn(), testUserAgent))
	apiKey := New("sk-test")
	var _ llm.UsageLabeler = chatgpt

	if got := chatgpt.UsageLabel(); got != llm.OpenAISubscription {
		t.Errorf("ChatGPT mode UsageLabel = %q, want %q", got, llm.OpenAISubscription)
	}
	if got := apiKey.UsageLabel(); got != "openai" {
		t.Errorf("API-key mode UsageLabel = %q, want openai", got)
	}
	for _, a := range []*Adapter{chatgpt, apiKey} {
		if a.Name() != "openai" {
			t.Errorf("Name = %q, want openai in both modes", a.Name())
		}
	}
}

func TestWithChatGPTAuthTargetsChatGPTBackend(t *testing.T) {
	creds := signedIn()
	tests := []struct {
		name string
		opts []Option
		want string
	}{
		{"default", []Option{WithChatGPTAuth(creds, testUserAgent)}, "https://chatgpt.com/backend-api/codex"},
		{
			"replaces an earlier base URL",
			[]Option{WithBaseURL("https://gateway.example/v1"), WithChatGPTAuth(creds, testUserAgent)},
			"https://chatgpt.com/backend-api/codex",
		},
		{"a later base URL wins", []Option{WithChatGPTAuth(creds, testUserAgent), WithBaseURL("http://127.0.0.1:9")}, "http://127.0.0.1:9"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := New("", tc.opts...).baseURL; got != tc.want {
				t.Errorf("base URL = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestChatGPTRefusesRedirects(t *testing.T) {
	// redirectTo answers every request with a 307 to target's /responses.
	redirectTo := func(t *testing.T, target string) *httptest.Server {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			_, _ = io.Copy(io.Discard, r.Body)
			http.Redirect(w, r, target+"/responses", http.StatusTemporaryRedirect)
		}))
		t.Cleanup(srv.Close)
		return srv
	}

	t.Run("ChatGPT mode stops at the redirect", func(t *testing.T) {
		trap := newScriptedBackend(t, streamHi())
		client := &http.Client{}
		a := New("", WithChatGPTAuth(signedIn(), testUserAgent), WithBaseURL(redirectTo(t, trap.URL).URL), WithHTTPClient(client))
		if _, err := a.Complete(testCtx(t), helloRequest()); err == nil {
			t.Error("Complete succeeded through the redirect, want an error")
		}
		if n := len(trap.seen()); n != 0 {
			t.Errorf("the redirect target received %d requests, want none", n)
		}
		if client.CheckRedirect != nil {
			t.Error("the WithHTTPClient client was changed; only the per-request copy may refuse redirects")
		}
	})
	t.Run("API-key mode still follows it", func(t *testing.T) {
		trap := newScriptedBackend(t, streamHi())
		a := New("sk-test", WithBaseURL(redirectTo(t, trap.URL).URL))
		if _, err := collect(a.Stream(testCtx(t), helloRequest())); err != nil {
			t.Fatalf("stream: %v", err)
		}
		if n := len(trap.seen()); n != 1 {
			t.Errorf("the redirect target received %d requests, want 1", n)
		}
	})
}

func TestAPIKeyModeStillSendsMaxOutputTokens(t *testing.T) {
	backend := newScriptedBackend(t, streamHi())
	req := helloRequest()
	maxTokens := 100
	req.MaxTokens = &maxTokens
	if _, err := collect(New("sk-test", WithBaseURL(backend.URL)).Stream(testCtx(t), req)); err != nil {
		t.Fatalf("stream: %v", err)
	}

	got := backend.seen()[0]
	if got.path != "/v1/responses" {
		t.Errorf("path = %q, want /v1/responses", got.path)
	}
	if got.body["max_output_tokens"] != float64(100) {
		t.Errorf("max_output_tokens = %v, want 100", got.body["max_output_tokens"])
	}
	if auth := got.header.Get("Authorization"); auth != "Bearer sk-test" {
		t.Errorf("Authorization = %q, want Bearer sk-test", auth)
	}
	for _, name := range []string{"session-id", "originator", "ChatGPT-Account-ID"} {
		if vals := got.header.Values(name); len(vals) > 0 {
			t.Errorf("header %s = %q in API-key mode, want none", name, vals)
		}
	}
	if _, ok := got.body["prompt_cache_key"]; ok {
		t.Error("API-key mode sent prompt_cache_key")
	}
}
