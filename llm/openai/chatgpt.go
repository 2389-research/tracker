// ABOUTME: ChatGPT-subscription mode for the Responses adapter: posts to ChatGPT's Codex backend with
// ABOUTME: the user's sign-in, reshapes the body, refreshes once after a 401, and maps plan-limit errors.
package openai

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"time"

	"github.com/2389-research/tracker/llm"
)

// ChatGPTBaseURL is the ChatGPT Codex backend that ChatGPT mode sends the
// bearer to (codex-rs/model-provider-info/src/lib.rs:80).
const ChatGPTBaseURL = "https://chatgpt.com/backend-api/codex"

// chatgptResponsesPath is the Responses endpoint under ChatGPTBaseURL
// (codex-rs/codex-api/src/endpoint/responses.rs:67).
const chatgptResponsesPath = "/responses"

// ChatGPTCredentials supplies the ChatGPT sign-in. Token returns the current
// access token and account ID; ForceRefresh replaces rejected, an access
// token the backend answered with a 401. When the request's context has
// ended, a failed call returns that context's error; otherwise an error whose
// Temporary() reports true becomes retryable, and any other error ends the
// run.
type ChatGPTCredentials interface {
	Token(ctx context.Context) (accessToken, accountID string, err error)
	ForceRefresh(ctx context.Context, rejected string) error
}

// chatgptMode is ChatGPT mode's per-adapter state. Nothing writes to it after
// construction, so concurrent requests share it safely.
type chatgptMode struct {
	creds     ChatGPTCredentials
	userAgent string
	sessionID string
}

// WithChatGPTAuth switches the adapter to ChatGPT mode: requests go to
// ChatGPTBaseURL with the bearer from creds, name the sender with userAgent
// and `originator: tracker`, and carry one session id for the adapter's life.
// It replaces any earlier WithBaseURL, so a base URL set for the API never
// receives the bearer; a later WithBaseURL still overrides it, which is how
// tests reach an httptest server.
func WithChatGPTAuth(creds ChatGPTCredentials, userAgent string) Option {
	return func(a *Adapter) {
		a.baseURL = ChatGPTBaseURL
		a.chatgpt = &chatgptMode{creds: creds, userAgent: userAgent, sessionID: newSessionID()}
	}
}

// UsageLabel names the bucket this adapter's usage is billed under:
// llm.OpenAISubscription in ChatGPT mode, which prices at $0, and the
// provider name otherwise.
func (a *Adapter) UsageLabel() string {
	if a.chatgpt != nil {
		return llm.OpenAISubscription
	}
	return a.Name()
}

// newSessionID returns 32 hex characters from crypto/rand, whose Read never
// returns an error (it crashes the program instead).
func newSessionID() string {
	var b [16]byte
	_, _ = rand.Read(b[:])
	return hex.EncodeToString(b[:])
}

// completeChatGPT folds a streamed reply into one Response, because the Codex
// backend has no non-streaming mode (codex-rs/core/src/client.rs:1004). The
// first error ends the call: it cancels the stream and drains it, so the
// stream goroutine finishes and releases its connection. A reply that the
// caller's context cut short returns the context's error, never a partial
// Response: the stream itself ends quietly on a cancelled read.
func (a *Adapter) completeChatGPT(ctx context.Context, req *llm.Request) (*llm.Response, error) {
	start := time.Now()
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	events := a.Stream(ctx, req)
	acc := llm.NewStreamAccumulator()
	for evt := range events {
		if evt.Err != nil {
			cancel()
			drainStream(events)
			return nil, evt.Err
		}
		acc.Process(evt)
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	resp := acc.Response()
	resp.Provider = a.UsageLabel()
	resp.Latency = time.Since(start)
	return &resp, nil
}

// drainStream discards a stream's remaining events until its goroutine closes
// the channel. Left unread, the goroutine would block on the full channel.
func drainStream(events <-chan llm.StreamEvent) {
	for range events {
	}
}

// chatgptBody is the request body in ChatGPT mode: the usual translation,
// reshaped for the Codex backend.
func (a *Adapter) chatgptBody(req *llm.Request) ([]byte, error) {
	body, err := translateRequest(req)
	if err != nil {
		return nil, fmt.Errorf("openai: translate request: %w", err)
	}
	return reshapeForChatGPT(body, a.chatgpt.sessionID)
}

// chatgptDroppedFields are absent from the Codex backend's request struct
// (codex-rs/codex-api/src/common.rs:279-304), so a node's max-tokens,
// temperature, top-p and stop settings do nothing in ChatGPT mode.
var chatgptDroppedFields = []string{"max_output_tokens", "temperature", "top_p", "stop"}

// reshapeForChatGPT sets the fields Codex always sends and deletes the ones
// its request lacks: store must be false (the backend rejects true), the
// reply always streams, encrypted reasoning comes back in the stream, and the
// session id doubles as the prompt-cache key
// (codex-rs/core/src/client.rs:581-593, 961, 1003-1006).
func reshapeForChatGPT(body []byte, sessionID string) ([]byte, error) {
	var fields map[string]any
	if err := json.Unmarshal(body, &fields); err != nil {
		return nil, fmt.Errorf("openai: reshape request for ChatGPT: %w", err)
	}
	fields["store"] = false
	fields["stream"] = true
	fields["include"] = []string{"reasoning.encrypted_content"}
	fields["prompt_cache_key"] = sessionID
	for _, name := range chatgptDroppedFields {
		delete(fields, name)
	}
	return json.Marshal(fields)
}

// setChatGPTHeaders applies ChatGPT mode's headers (research §F). It never
// applies WithExtraHeaders: those authenticate to a gateway, which this mode
// never uses.
func (a *Adapter) setChatGPTHeaders(req *http.Request, access, account string) {
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+access)
	if account != "" {
		req.Header.Set("ChatGPT-Account-ID", account)
	}
	req.Header.Set("originator", "tracker")
	req.Header.Set("User-Agent", a.chatgpt.userAgent)
	req.Header.Set("session-id", a.chatgpt.sessionID)
}

// sendChatGPT posts body with the current sign-in. After a 401 it refreshes
// the sign-in once and retries once; a second 401 means only signing in
// again can help.
func (a *Adapter) sendChatGPT(ctx context.Context, body []byte) (*http.Response, error) {
	resp, access, err := a.postSignedIn(ctx, body)
	if err != nil || resp.StatusCode != http.StatusUnauthorized {
		return resp, err
	}
	resp.Body.Close()
	if err := a.chatgpt.creds.ForceRefresh(ctx, access); err != nil {
		return nil, credentialsError(ctx, err)
	}
	resp, _, err = a.postSignedIn(ctx, body)
	if err != nil || resp.StatusCode != http.StatusUnauthorized {
		return resp, err
	}
	resp.Body.Close()
	return nil, &llm.AuthenticationError{ProviderError: llm.ProviderError{
		SDKError:   llm.SDKError{Msg: "openai: ChatGPT rejected the refreshed sign-in; run `tracker setup` to sign in again"},
		Provider:   "openai",
		StatusCode: http.StatusUnauthorized,
	}}
}

// postSignedIn posts body with the sign-in the credentials hold now, and
// returns the access token it sent, so a 401 can name the rejected one.
func (a *Adapter) postSignedIn(ctx context.Context, body []byte) (*http.Response, string, error) {
	access, account, err := a.chatgpt.creds.Token(ctx)
	if err != nil {
		return nil, "", credentialsError(ctx, err)
	}
	resp, err := a.postStream(ctx, a.baseURL+chatgptResponsesPath, body, func(req *http.Request) {
		a.setChatGPTHeaders(req, access, account)
	})
	return resp, access, err
}

// refuseRedirect is ChatGPT mode's redirect policy: the client hands back the
// 3xx instead of following it. Go copies Authorization to the same host or a
// subdomain without checking the scheme, so a redirect could carry the bearer
// to plain http, and a 307 or 308 re-sends the prompt and the account and
// session headers to any host.
func refuseRedirect(*http.Request, []*http.Request) error {
	return http.ErrUseLastResponse
}

// credentialsError sorts a ChatGPTCredentials failure. Whether the run is
// being cancelled is a property of the request's context, not of the error
// (an auth-server timeout also matches errors.Is(err,
// context.DeadlineExceeded)), so when ctx has ended its error comes back
// unchanged. Otherwise an error whose Temporary() reports true, such as a
// refresh that hit a network error, a timeout or a 5xx, becomes a retryable
// *llm.NetworkError, and the rest (not signed in, an unsafe token file, a
// revoked sign-in) a fatal *llm.ConfigurationError.
func credentialsError(ctx context.Context, err error) error {
	if ctxErr := ctx.Err(); ctxErr != nil {
		return ctxErr
	}
	sdkErr := llm.SDKError{Msg: "openai: " + err.Error(), Cause: err}
	var temporary interface{ Temporary() bool }
	if errors.As(err, &temporary) && temporary.Temporary() {
		return &llm.NetworkError{SDKError: sdkErr}
	}
	return &llm.ConfigurationError{SDKError: sdkErr}
}

// chatgptErrorBody is the JSON error the Codex backend sends with a 429
// (codex-rs/codex-api/src/api_bridge.rs:340-352).
type chatgptErrorBody struct {
	Error struct {
		Type     string `json:"type"`
		PlanType string `json:"plan_type"`
		ResetsAt int64  `json:"resets_at"` // Unix seconds
	} `json:"error"`
}

// chatgptStatusError maps a non-200 reply from the Codex backend. Two 429s are
// plan limits, not rate limits, and are never retried: usage_limit_reached,
// which pauses the run until the limit resets, and usage_not_included. Most
// other failures (including an unserved model — design doc open item 3) carry
// a plain {"detail": "..."} body instead; chatgptDetailMessage turns that
// into the message, so the error reads like the backend's own words rather
// than a raw JSON blob. Anything else maps as in API-key mode.
func chatgptStatusError(status int, body []byte, h http.Header) error {
	if status == http.StatusTooManyRequests {
		if err := chatgptPlanError(body); err != nil {
			return err
		}
	}
	msg := string(body)
	if detail, ok := chatgptDetailMessage(body); ok {
		msg = "openai: " + detail
	}
	return llm.ErrorFromStatusCodeRetryAfter(status, msg, "openai", llm.ParseRetryAfter(h))
}

// chatgptDetailBody is the Codex backend's shape for most non-plan-limit
// failures: a human-readable detail string and nothing else structured, e.g.
// a model name the signed-in plan doesn't serve ("The 'gpt-4.1-mini' model is
// not supported when using Codex with a ChatGPT account.").
type chatgptDetailBody struct {
	Detail string `json:"detail"`
}

// chatgptDetailMessage extracts detail from a body shaped like
// chatgptDetailBody. ok is false when body does not parse that way, or
// parses with an empty detail, so the caller falls back to the raw body text.
func chatgptDetailMessage(body []byte) (detail string, ok bool) {
	var parsed chatgptDetailBody
	if json.Unmarshal(body, &parsed) != nil || parsed.Detail == "" {
		return "", false
	}
	return parsed.Detail, true
}

// chatgptPlanError returns the error for a plan-limit 429 body, or nil when
// the body is not one (codex-rs/codex-api/src/api_bridge.rs:181-235).
func chatgptPlanError(body []byte) error {
	var parsed chatgptErrorBody
	if json.Unmarshal(body, &parsed) != nil {
		return nil
	}
	switch parsed.Error.Type {
	case "usage_limit_reached":
		return usageLimitError(parsed.Error.PlanType, parsed.Error.ResetsAt)
	case "usage_not_included":
		return &llm.AccessDeniedError{ProviderError: llm.ProviderError{
			SDKError:   llm.SDKError{Msg: "openai: your ChatGPT plan does not include Codex usage"},
			Provider:   "openai",
			StatusCode: http.StatusTooManyRequests,
			ErrorCode:  "usage_not_included",
		}}
	}
	return nil
}

// usageLimitError builds the usage-limit error. The message says "usage
// limit" and carries the reset time as RFC3339, so llm.IsUsageLimit and
// llm.UsageLimitResetAt recognize it and the paused run knows when to resume.
// A reset time of 0 means the backend sent none, and ResetsAt stays zero.
func usageLimitError(plan string, resetsAt int64) error {
	msg := "openai: ChatGPT usage limit reached"
	if plan != "" {
		msg += " (" + plan + " plan)"
	}
	var resets time.Time
	if resetsAt > 0 {
		resets = time.Unix(resetsAt, 0).UTC()
		msg += ", resets at " + resets.Format(time.RFC3339)
	}
	return &llm.UsageLimitError{
		ProviderError: llm.ProviderError{
			SDKError:   llm.SDKError{Msg: msg},
			Provider:   "openai",
			StatusCode: http.StatusTooManyRequests,
			ErrorCode:  "usage_limit_reached",
		},
		ResetsAt: resets,
		PlanType: plan,
	}
}
