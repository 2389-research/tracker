// ABOUTME: Tests buildClient under TRACKER_OPENAI_AUTH: ChatGPT mode registers openai from the token file,
// ABOUTME: sends it only to the ChatGPT backend whatever the base-URL and gateway settings, and shares one token source.
package tracker

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/2389-research/tracker/internal/chatgptauth"
	"github.com/2389-research/tracker/llm"
	"github.com/2389-research/tracker/llm/openai"
)

// clientEnvVars is every variable buildClient reads to choose providers,
// base URLs and gateways, plus the auth switch.
var clientEnvVars = []string{
	"ANTHROPIC_API_KEY", "OPENAI_API_KEY", "GEMINI_API_KEY", "GOOGLE_API_KEY", "OPENAI_COMPAT_API_KEY",
	"ANTHROPIC_BASE_URL", "OPENAI_BASE_URL", "GEMINI_BASE_URL", "OPENAI_COMPAT_BASE_URL",
	"TRACKER_GATEWAY_URL", "TRACKER_GATEWAY_KIND", chatgptauth.EnvVar,
}

// isolateClientEnv blanks clientEnvVars and points HOME and XDG_CONFIG_HOME at
// empty temp dirs, so a test sees only what it sets and never the real token
// file. It returns the token file path ChatGPT mode reads.
func isolateClientEnv(t *testing.T) string {
	t.Helper()
	for _, name := range clientEnvVars {
		t.Setenv(name, "")
	}
	t.Setenv("HOME", t.TempDir())
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	path, err := chatgptauth.DefaultPath()
	if err != nil {
		t.Fatalf("DefaultPath: %v", err)
	}
	return path
}

// testAccessToken is an unsigned JWT whose exp is an hour away, so no call
// refreshes it: a refresh would go to the real auth.openai.com. id tells
// two tokens minted in the same second apart.
func testAccessToken(t *testing.T, id string) string {
	t.Helper()
	claims, err := json.Marshal(map[string]any{"exp": time.Now().Add(time.Hour).Unix(), "test_id": id})
	if err != nil {
		t.Fatalf("marshal access token claims: %v", err)
	}
	enc := base64.RawURLEncoding.EncodeToString
	return enc([]byte(`{"alg":"none","typ":"JWT"}`)) + "." + enc(claims) + ".sig"
}

// saveChatGPTSignIn writes a sign-in for account acct-1 holding access to
// the token file at path.
func saveChatGPTSignIn(t *testing.T, path, access string) {
	t.Helper()
	tokens := chatgptauth.Tokens{AccessToken: access, RefreshToken: "refresh-1", AccountID: "acct-1", LastRefresh: time.Now()}
	if err := chatgptauth.Save(path, tokens); err != nil {
		t.Fatalf("save the sign-in: %v", err)
	}
}

// aimChatGPTBackend points ChatGPT mode at url for the rest of the test.
func aimChatGPTBackend(t *testing.T, url string) {
	t.Helper()
	prev := chatgptBackendURL
	chatgptBackendURL = url
	t.Cleanup(func() { chatgptBackendURL = prev })
}

// streamHi is a complete Codex-backend stream that answers "hi".
var streamHi = strings.Join([]string{
	"event: response.created",
	`data: {"type":"response.created","response":{"id":"resp_1","model":"gpt-test"}}`,
	"",
	"event: response.output_text.delta",
	`data: {"type":"response.output_text.delta","output_index":0,"content_index":0,"delta":"hi"}`,
	"",
	"event: response.completed",
	`data: {"type":"response.completed","response":{"id":"resp_1","status":"completed","usage":{"input_tokens":12,"output_tokens":3,"total_tokens":15},"output":[]}}`,
	"",
}, "\n")

// responsesHi is the Responses API's non-streaming reply saying "hi".
const responsesHi = `{"id":"resp_1","model":"gpt-test","status":"completed",` +
	`"output":[{"type":"message","role":"assistant","content":[{"type":"output_text","text":"hi"}]}],` +
	`"usage":{"input_tokens":12,"output_tokens":3,"total_tokens":15}}`

// trapReply is what a server that must never be reached answers. A 400 fails
// the call at once: no retry, and no refresh, which a 401 would trigger.
const trapReply = `{"error":{"message":"trap: this server must never receive a request"}}`

// seenRequest is what a recordingServer kept of one request.
type seenRequest struct {
	path   string
	header http.Header
	body   string
}

// recordingServer answers every request with one fixed reply and records
// each request it sees.
type recordingServer struct {
	*httptest.Server
	mu   sync.Mutex
	seen []seenRequest
}

func newRecordingServer(t *testing.T, status int, contentType, reply string) *recordingServer {
	t.Helper()
	s := &recordingServer{}
	s.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		s.mu.Lock()
		s.seen = append(s.seen, seenRequest{path: r.URL.Path, header: r.Header.Clone(), body: string(body)})
		s.mu.Unlock()
		w.Header().Set("Content-Type", contentType)
		w.WriteHeader(status)
		_, _ = io.WriteString(w, reply)
	}))
	t.Cleanup(s.Close)
	return s
}

// newChatGPTBackend stands in for the ChatGPT Codex backend.
func newChatGPTBackend(t *testing.T) *recordingServer {
	return newRecordingServer(t, http.StatusOK, "text/event-stream", streamHi)
}

// newTrap stands in for a planted or stale base URL or gateway.
func newTrap(t *testing.T) *recordingServer {
	return newRecordingServer(t, http.StatusBadRequest, "application/json", trapReply)
}

// requests returns a copy of the requests recorded so far.
func (s *recordingServer) requests() []seenRequest {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]seenRequest(nil), s.seen...)
}

// mustBuildClient builds a client the way every tracker entry point does,
// closing it when the test ends.
func mustBuildClient(t *testing.T, provider string) *llm.Client {
	t.Helper()
	client, err := buildClient(provider, "", "")
	if err != nil {
		t.Fatalf("buildClient(%q): %v", provider, err)
	}
	t.Cleanup(func() { _ = client.Close() })
	return client
}

// completeHello sends one short request to provider ("" = the default).
func completeHello(t *testing.T, client *llm.Client, provider string) (*llm.Response, error) {
	t.Helper()
	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()
	return client.Complete(ctx, &llm.Request{Provider: provider, Model: "gpt-test", Messages: []llm.Message{llm.UserMessage("hello")}})
}

// requireUnregistered fails the test unless provider is missing from client:
// the client refuses a request naming it before anything is sent.
func requireUnregistered(t *testing.T, client *llm.Client, provider string) {
	t.Helper()
	_, err := completeHello(t, client, provider)
	if err == nil || !strings.Contains(err.Error(), "unknown provider") {
		t.Errorf("a request to %s: err = %v, want an unknown-provider error (%s must not be registered)", provider, err, provider)
	}
}

// requireChatGPTRequest fails the test unless r is a ChatGPT-mode request
// carrying access for account acct-1.
func requireChatGPTRequest(t *testing.T, r seenRequest, access string) {
	t.Helper()
	if r.path != "/responses" {
		t.Errorf("path = %q, want /responses", r.path)
	}
	if got := r.header.Get("Authorization"); got != "Bearer "+access {
		t.Errorf("Authorization = %q, want the token file's bearer", got)
	}
	if got := r.header.Get("ChatGPT-Account-ID"); got != "acct-1" {
		t.Errorf("ChatGPT-Account-ID = %q, want acct-1", got)
	}
	if got := r.header.Get("originator"); got != "tracker" {
		t.Errorf("originator = %q, want tracker", got)
	}
}

// TestChatGPTBackendURLIsTheCodexBackend pins the production destination:
// tests aim chatgptBackendURL at httptest servers, and nothing else may
// change it.
func TestChatGPTBackendURLIsTheCodexBackend(t *testing.T) {
	if chatgptBackendURL != openai.ChatGPTBaseURL {
		t.Fatalf("chatgptBackendURL = %q, want %q", chatgptBackendURL, openai.ChatGPTBaseURL)
	}
}

func TestBuildClientChatGPTModeRegistersOpenAIWithoutKey(t *testing.T) {
	path := isolateClientEnv(t)
	access := testAccessToken(t, "access-1")
	saveChatGPTSignIn(t, path, access)
	t.Setenv(chatgptauth.EnvVar, "chatgpt")
	backend := newChatGPTBackend(t)
	aimChatGPTBackend(t, backend.URL)

	client := mustBuildClient(t, "")
	if got := client.DefaultProvider(); got != "openai" {
		t.Fatalf("DefaultProvider() = %q, want openai", got)
	}
	if got := client.UsageLabel("openai"); got != llm.OpenAISubscription {
		t.Errorf("UsageLabel(openai) = %q, want %q", got, llm.OpenAISubscription)
	}
	resp, err := completeHello(t, client, "")
	if err != nil {
		t.Fatalf("Complete: %v", err)
	}
	if resp.Text() != "hi" || resp.Provider != llm.OpenAISubscription {
		t.Errorf("reply = %q from %q, want \"hi\" from %q", resp.Text(), resp.Provider, llm.OpenAISubscription)
	}
	reqs := backend.requests()
	if len(reqs) != 1 {
		t.Fatalf("backend saw %d requests, want 1", len(reqs))
	}
	requireChatGPTRequest(t, reqs[0], access)
}

// TestBuildClientChatGPTModeProviderPin covers a caller that names one
// provider: openai registers from the sign-in alone, and any other provider
// gets no ChatGPT adapter.
func TestBuildClientChatGPTModeProviderPin(t *testing.T) {
	path := isolateClientEnv(t)
	access := testAccessToken(t, "access-1")
	saveChatGPTSignIn(t, path, access)
	t.Setenv(chatgptauth.EnvVar, "chatgpt")
	t.Setenv("ANTHROPIC_API_KEY", "sk-ant-test")
	backend := newChatGPTBackend(t)
	aimChatGPTBackend(t, backend.URL)

	t.Run("openai", func(t *testing.T) {
		client := mustBuildClient(t, "openai")
		if got := client.DefaultProvider(); got != "openai" {
			t.Errorf("DefaultProvider() = %q, want openai", got)
		}
		requireUnregistered(t, client, "anthropic")
		if _, err := completeHello(t, client, "openai"); err != nil {
			t.Fatalf("Complete: %v", err)
		}
		if reqs := backend.requests(); len(reqs) != 1 {
			t.Errorf("backend saw %d requests, want 1", len(reqs))
		}
	})
	t.Run("anthropic", func(t *testing.T) {
		client := mustBuildClient(t, "anthropic")
		if got := client.DefaultProvider(); got != "anthropic" {
			t.Errorf("DefaultProvider() = %q, want anthropic", got)
		}
		requireUnregistered(t, client, "openai")
	})
}

// TestBuildClientChatGPTModeIgnoresBaseURLAndGateway is the fixed-destination
// invariant (design §2): in ChatGPT mode the bearer goes to the ChatGPT
// backend whatever OPENAI_BASE_URL, TRACKER_GATEWAY_URL, --gateway-url and
// --gateway-kind say, and OPENAI_API_KEY is never sent.
func TestBuildClientChatGPTModeIgnoresBaseURLAndGateway(t *testing.T) {
	path := isolateClientEnv(t)
	access := testAccessToken(t, "access-1")
	saveChatGPTSignIn(t, path, access)
	t.Setenv(chatgptauth.EnvVar, "chatgpt")
	t.Setenv("OPENAI_API_KEY", "sk-test")
	backend := newChatGPTBackend(t)
	aimChatGPTBackend(t, backend.URL)
	trap := newTrap(t)

	cases := []struct {
		name  string
		build func(t *testing.T) (*llm.Client, error)
	}{
		{"OPENAI_BASE_URL", func(t *testing.T) (*llm.Client, error) {
			t.Setenv("OPENAI_BASE_URL", trap.URL)
			return buildClient("", "", "")
		}},
		{"TRACKER_GATEWAY_URL", func(t *testing.T) (*llm.Client, error) {
			t.Setenv("TRACKER_GATEWAY_URL", trap.URL)
			t.Setenv("TRACKER_GATEWAY_KIND", string(GatewayKindBedrock))
			return buildClient("", "", "")
		}},
		{"--gateway-url and --gateway-kind", func(t *testing.T) (*llm.Client, error) {
			return NewLLMClient(Config{GatewayURL: trap.URL, GatewayKind: GatewayKindCFAIG})
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			client, err := tc.build(t)
			if err != nil {
				t.Fatalf("build: %v", err)
			}
			defer client.Close()
			before, trapBefore := len(backend.requests()), len(trap.requests())
			resp, err := completeHello(t, client, "openai")
			if n := len(trap.requests()) - trapBefore; n != 0 {
				t.Fatalf("the trap received %d request(s); ChatGPT mode must never send to a base URL or gateway", n)
			}
			if err != nil {
				t.Fatalf("Complete: %v", err)
			}
			if resp.Text() != "hi" {
				t.Errorf("reply = %q, want hi", resp.Text())
			}
			reqs := backend.requests()
			if len(reqs) != before+1 {
				t.Fatalf("backend saw %d new requests, want 1", len(reqs)-before)
			}
			requireChatGPTRequest(t, reqs[before], access)
		})
	}
	for i, r := range backend.requests() {
		for name, values := range r.header {
			if strings.Contains(strings.Join(values, " "), "sk-test") {
				t.Errorf("request %d sent OPENAI_API_KEY in header %s", i, name)
			}
		}
		if strings.Contains(r.body, "sk-test") {
			t.Errorf("request %d sent OPENAI_API_KEY in its body", i)
		}
	}
}

// TestBuildClientChatGPTNotSignedInFailsOnUse: without a token file the
// client still builds, so an Anthropic-only pipeline runs; openai's first
// call fails, fatally, telling the user to sign in.
func TestBuildClientChatGPTNotSignedInFailsOnUse(t *testing.T) {
	isolateClientEnv(t)
	t.Setenv(chatgptauth.EnvVar, "chatgpt")
	backend := newChatGPTBackend(t)
	aimChatGPTBackend(t, backend.URL)

	client := mustBuildClient(t, "")
	_, err := completeHello(t, client, "openai")
	var cfgErr *llm.ConfigurationError
	if !errors.As(err, &cfgErr) {
		t.Fatalf("Complete: err = %v (%T), want *llm.ConfigurationError", err, err)
	}
	if !strings.Contains(err.Error(), "tracker setup") {
		t.Errorf("error %q should tell the user to run tracker setup", err)
	}
	if n := len(backend.requests()); n != 0 {
		t.Errorf("backend saw %d requests without a sign-in, want 0", n)
	}

	t.Setenv("ANTHROPIC_API_KEY", "sk-ant-test")
	if got := mustBuildClient(t, "").DefaultProvider(); got != "anthropic" {
		t.Errorf("with an Anthropic key, DefaultProvider() = %q, want anthropic", got)
	}
	anthropicOnly := mustBuildClient(t, "anthropic")
	if got := anthropicOnly.DefaultProvider(); got != "anthropic" {
		t.Errorf("DefaultProvider() = %q, want anthropic", got)
	}
	requireUnregistered(t, anthropicOnly, "openai")
}

func TestBuildClientInvalidSwitch(t *testing.T) {
	isolateClientEnv(t)
	t.Setenv(chatgptauth.EnvVar, "yes")
	t.Setenv("ANTHROPIC_API_KEY", "sk-ant-test")
	for _, provider := range []string{"", "anthropic"} {
		_, err := buildClient(provider, "", "")
		if err == nil {
			t.Fatalf("buildClient(%q) with %s=yes: want an error, got none", provider, chatgptauth.EnvVar)
		}
		for _, want := range []string{chatgptauth.EnvVar, "chatgpt", "api-key"} {
			if !strings.Contains(err.Error(), want) {
				t.Errorf("buildClient(%q) error %q should name %q", provider, err, want)
			}
		}
	}
}

// TestBuildClientAPIKeyModeUnchanged: with the switch unset or api-key,
// openai builds from OPENAI_API_KEY, honors OPENAI_BASE_URL, and bills under
// its own name.
func TestBuildClientAPIKeyModeUnchanged(t *testing.T) {
	for _, mode := range []string{"", "api-key"} {
		t.Run("switch="+mode, func(t *testing.T) {
			isolateClientEnv(t)
			api := newRecordingServer(t, http.StatusOK, "application/json", responsesHi)
			t.Setenv(chatgptauth.EnvVar, mode)
			t.Setenv("OPENAI_API_KEY", "sk-test")
			t.Setenv("OPENAI_BASE_URL", api.URL)

			client := mustBuildClient(t, "")
			if got := client.UsageLabel("openai"); got != "openai" {
				t.Errorf("UsageLabel(openai) = %q, want openai", got)
			}
			resp, err := completeHello(t, client, "")
			if err != nil {
				t.Fatalf("Complete: %v", err)
			}
			if resp.Provider != "openai" {
				t.Errorf("resp.Provider = %q, want openai", resp.Provider)
			}
			reqs := api.requests()
			if len(reqs) != 1 {
				t.Fatalf("OPENAI_BASE_URL server saw %d requests, want 1", len(reqs))
			}
			if got := reqs[0].header.Get("Authorization"); reqs[0].path != "/v1/responses" || got != "Bearer sk-test" {
				t.Errorf("request = %s with Authorization %q, want /v1/responses with Bearer sk-test", reqs[0].path, got)
			}
		})
	}
}

// cachedTokenSource returns the token source the process holds for path,
// failing the test when there is none.
func cachedTokenSource(t *testing.T, path string) *chatgptauth.TokenSource {
	t.Helper()
	chatgptTokenSourcesMu.Lock()
	defer chatgptTokenSourcesMu.Unlock()
	src, ok := chatgptTokenSources[path]
	if !ok {
		t.Fatalf("no token source cached for %s", path)
	}
	return src
}

// TestBuildClientSharesOneTokenSourcePerPath: every client built in one
// process reads the sign-in through one token source, so one refresher spends
// each single-use refresh token. On Windows, which has no file lock, that
// source's mutex is all that keeps two refreshes apart.
func TestBuildClientSharesOneTokenSourcePerPath(t *testing.T) {
	path := isolateClientEnv(t)
	first := testAccessToken(t, "access-1")
	saveChatGPTSignIn(t, path, first)
	t.Setenv(chatgptauth.EnvVar, "chatgpt")
	backend := newChatGPTBackend(t)
	aimChatGPTBackend(t, backend.URL)

	c1 := mustBuildClient(t, "")
	src := cachedTokenSource(t, path)
	c2 := mustBuildClient(t, "")
	if again := cachedTokenSource(t, path); again != src {
		t.Fatal("the second build replaced the cached token source")
	}

	// A token source reads the file once and keeps the token until it is due.
	// c1's call loads the first token; the file then changes. A second source
	// would read the new token, so c2 sending the first one proves it reads
	// through c1's source.
	if _, err := completeHello(t, c1, ""); err != nil {
		t.Fatalf("c1 Complete: %v", err)
	}
	saveChatGPTSignIn(t, path, testAccessToken(t, "access-2"))
	if _, err := completeHello(t, c2, ""); err != nil {
		t.Fatalf("c2 Complete: %v", err)
	}
	reqs := backend.requests()
	if len(reqs) != 2 {
		t.Fatalf("backend saw %d requests, want 2", len(reqs))
	}
	for i, r := range reqs {
		if got := r.header.Get("Authorization"); got != "Bearer "+first {
			t.Errorf("request %d: Authorization is not the first token; the clients do not share one token source", i+1)
		}
	}
}
