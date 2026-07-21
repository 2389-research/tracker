// ABOUTME: Tests provider/model failover — switch lanes on billing exhaustion,
// ABOUTME: don't switch on transient/code errors, surface the switch for audit.
package llm

import (
	"context"
	"errors"
	"testing"
)

// erroringAdapter returns a fixed error (or a response when err is nil).
type erroringAdapter struct {
	name string
	err  error
	resp *Response
}

func (a *erroringAdapter) Name() string { return a.name }
func (a *erroringAdapter) Complete(_ context.Context, _ *Request) (*Response, error) {
	if a.err != nil {
		return nil, a.err
	}
	return a.resp, nil
}
func (a *erroringAdapter) Stream(_ context.Context, _ *Request) <-chan StreamEvent {
	ch := make(chan StreamEvent)
	close(ch)
	return ch
}
func (a *erroringAdapter) Close() error { return nil }

func billingErr(provider string) error {
	return &InvalidRequestError{ProviderError: ProviderError{
		SDKError: SDKError{Msg: provider + ": invalid_request_error: Your credit balance is too low"},
		Provider: provider, StatusCode: 400,
	}}
}

func newFailoverClient(t *testing.T, adapters ...ProviderAdapter) *Client {
	t.Helper()
	opts := make([]ClientOption, 0, len(adapters)+1)
	for _, a := range adapters {
		opts = append(opts, WithProvider(a))
	}
	opts = append(opts, WithDefaultProvider(adapters[0].Name()))
	c, err := NewClient(opts...)
	if err != nil {
		t.Fatalf("build client: %v", err)
	}
	t.Cleanup(func() { c.Close() })
	return c
}

func TestCompleteFailover_SwitchesLanesOnBilling(t *testing.T) {
	primary := &erroringAdapter{name: "anthropic", err: billingErr("anthropic")}
	backup := &erroringAdapter{name: "openai", resp: &Response{ID: "ok", Message: AssistantMessage("from openai")}}
	c := newFailoverClient(t, primary, backup)

	var events []FailoverEvent
	req := &Request{Provider: "anthropic", Model: "claude-x", Messages: []Message{UserMessage("hi")}}
	resp, err := c.CompleteFailover(context.Background(), req,
		[]Target{{Provider: "openai", Model: "gpt-x"}},
		func(e FailoverEvent) { events = append(events, e) })

	if err != nil {
		t.Fatalf("failover should have succeeded on the backup lane: %v", err)
	}
	if resp.Provider != "openai" {
		t.Errorf("served by %q, want openai", resp.Provider)
	}
	if len(events) != 1 || events[0].From.Provider != "anthropic" || events[0].To.Provider != "openai" {
		t.Errorf("expected one anthropic→openai failover event, got %+v", events)
	}
	// The primary request must not be mutated by failover.
	if req.Provider != "anthropic" || req.Model != "claude-x" {
		t.Errorf("primary request was mutated: %+v", req)
	}
}

func TestCompleteFailover_DoesNotSwitchOnNonBilling(t *testing.T) {
	// A non-retryable auth error would fail identically on any lane — don't burn
	// the backup on it.
	authErr := &AuthenticationError{ProviderError: ProviderError{SDKError: SDKError{Msg: "invalid api key"}, Provider: "anthropic"}}
	primary := &erroringAdapter{name: "anthropic", err: authErr}
	backupHit := false
	backup := &erroringAdapter{name: "openai", resp: &Response{ID: "ok"}}
	_ = backupHit
	c := newFailoverClient(t, primary, backup)

	req := &Request{Provider: "anthropic", Model: "claude-x", Messages: []Message{UserMessage("hi")}}
	_, err := c.CompleteFailover(context.Background(), req, []Target{{Provider: "openai", Model: "gpt-x"}}, nil)
	if !errors.Is(err, authErr) {
		t.Errorf("auth error should be returned as-is (no failover), got %v", err)
	}
}

func TestCompleteFailover_AllLanesExhausted(t *testing.T) {
	primary := &erroringAdapter{name: "anthropic", err: billingErr("anthropic")}
	backup := &erroringAdapter{name: "openai", err: billingErr("openai")}
	c := newFailoverClient(t, primary, backup)

	req := &Request{Provider: "anthropic", Model: "claude-x", Messages: []Message{UserMessage("hi")}}
	_, err := c.CompleteFailover(context.Background(), req, []Target{{Provider: "openai", Model: "gpt-x"}}, nil)
	// Still a billing error (the last lane's), so an upstream pause still pauses.
	if !IsBillingError(err) {
		t.Errorf("all-exhausted should return a billing-class error, got %v", err)
	}
}

func TestCompleteFailover_NoFallbacksReturnsOriginal(t *testing.T) {
	primary := &erroringAdapter{name: "anthropic", err: billingErr("anthropic")}
	c := newFailoverClient(t, primary)
	req := &Request{Provider: "anthropic", Model: "claude-x", Messages: []Message{UserMessage("hi")}}
	if _, err := c.CompleteFailover(context.Background(), req, nil, nil); !IsBillingError(err) {
		t.Errorf("no fallbacks → original billing error, got %v", err)
	}
}
