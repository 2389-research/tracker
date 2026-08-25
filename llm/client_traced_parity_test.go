// ABOUTME: Regression tests for #605 — a traced (streamed) completion must return
// ABOUTME: the SAME response metadata as the untraced Complete path, not a lossy one.
package llm

import (
	"context"
	"encoding/json"
	"testing"
	"time"
)

// parityAdapter is a CLIENT-PLUMBING fixture: it returns a canonical response
// from Complete and a stream whose stream-start event carries the FULL metadata
// (id/model/raw/warnings/rate-limit) on its FullResponse. It proves the client
// (completeWithTrace + StreamAccumulator) faithfully carries every FullResponse
// field an adapter chooses to attach — it does NOT prove real adapters attach
// all of them.
//
// Real-adapter reality (see #617): the shipped adapters carry id/model on the
// stream-start event and rate-limit via the pre-stream metadata event, so those
// achieve traced==untraced parity. `Raw` is Complete-only — it cannot be
// reconstructed from an SSE stream — so traced `Raw` is empty (unchanged from
// before #605; no regression), and `Warnings` currently has no producer in
// either path. This fixture deliberately over-populates to exercise the plumbing;
// do not read it as a claim that real traced calls return `Raw`.
type parityAdapter struct {
	name     string
	response *Response
	events   []StreamEvent
}

func (p *parityAdapter) Name() string { return p.name }

func (p *parityAdapter) Complete(_ context.Context, _ *Request) (*Response, error) {
	cp := *p.response
	return &cp, nil
}

func (p *parityAdapter) Stream(_ context.Context, _ *Request) <-chan StreamEvent {
	ch := make(chan StreamEvent, len(p.events))
	for _, e := range p.events {
		ch <- e
	}
	close(ch)
	return ch
}

func (p *parityAdapter) Close() error { return nil }

// canonicalResponse is the single authoritative completion result both paths
// must produce.
func canonicalResponse() *Response {
	rr := 42
	reset := time.Date(2026, 8, 25, 12, 0, 0, 0, time.UTC)
	return &Response{
		ID:    "resp-canonical-1",
		Model: "provider-model-2026",
		Message: Message{
			Role:    RoleAssistant,
			Content: []ContentPart{{Kind: KindText, Text: "hello world"}},
		},
		FinishReason: FinishReason{Reason: "stop", Raw: "end_turn"},
		Usage:        Usage{InputTokens: 5, OutputTokens: 3, TotalTokens: 8},
		Raw:          json.RawMessage(`{"id":"resp-canonical-1","model":"provider-model-2026"}`),
		Warnings:     []Warning{{Message: "deprecated field", Code: "deprecation"}},
		RateLimit:    &RateLimitInfo{RequestsRemaining: &rr, ResetAt: &reset},
	}
}

// streamOf builds a stream that reconstructs r: metadata rides on the
// stream-start event's FullResponse (as the real adapters now emit), content and
// usage/finish ride on their normal delta/finish events.
func streamOf(r *Response) []StreamEvent {
	usage := r.Usage
	fr := r.FinishReason
	return []StreamEvent{
		{Type: EventStreamStart, FullResponse: &Response{
			ID:        r.ID,
			Model:     r.Model,
			Raw:       r.Raw,
			Warnings:  r.Warnings,
			RateLimit: r.RateLimit,
		}},
		{Type: EventTextStart, TextID: "b0"},
		{Type: EventTextDelta, TextID: "b0", Delta: r.Text()},
		{Type: EventTextEnd, TextID: "b0"},
		{Type: EventFinish, FinishReason: &fr, Usage: &usage},
	}
}

func TestTracedCompletionMatchesUntracedMetadata(t *testing.T) {
	canonical := canonicalResponse()
	adapter := &parityAdapter{name: "alpha", response: canonical, events: streamOf(canonical)}
	client, err := NewClient(WithProvider(adapter), WithDefaultProvider("alpha"))
	if err != nil {
		t.Fatalf("NewClient: %v", err)
	}
	defer client.Close()

	baseReq := func() *Request {
		return &Request{Model: "requested-alias", Messages: []Message{UserMessage("hi")}}
	}

	// Untraced: no observers → adapter.Complete path.
	untraced, err := client.Complete(context.Background(), baseReq())
	if err != nil {
		t.Fatalf("untraced Complete: %v", err)
	}

	// Traced: a request-level observer → completeWithTrace streaming path.
	tracedReq := baseReq()
	tracedReq.TraceObservers = []TraceObserver{TraceObserverFunc(func(TraceEvent) {})}
	traced, err := client.Complete(context.Background(), tracedReq)
	if err != nil {
		t.Fatalf("traced Complete: %v", err)
	}

	if traced.ID != untraced.ID {
		t.Errorf("ID: traced %q != untraced %q", traced.ID, untraced.ID)
	}
	if traced.Model != untraced.Model {
		t.Errorf("Model: traced %q != untraced %q", traced.Model, untraced.Model)
	}
	if string(traced.Raw) != string(untraced.Raw) {
		t.Errorf("Raw: traced %q != untraced %q", traced.Raw, untraced.Raw)
	}
	if traced.Provider != untraced.Provider {
		t.Errorf("Provider: traced %q != untraced %q", traced.Provider, untraced.Provider)
	}
	if traced.Text() != untraced.Text() {
		t.Errorf("Text: traced %q != untraced %q", traced.Text(), untraced.Text())
	}
	if traced.FinishReason != untraced.FinishReason {
		t.Errorf("FinishReason: traced %+v != untraced %+v", traced.FinishReason, untraced.FinishReason)
	}
	if traced.Usage != untraced.Usage {
		t.Errorf("Usage: traced %+v != untraced %+v", traced.Usage, untraced.Usage)
	}

	// Warnings.
	if len(traced.Warnings) != len(untraced.Warnings) {
		t.Fatalf("Warnings length: traced %d != untraced %d", len(traced.Warnings), len(untraced.Warnings))
	}
	for i := range traced.Warnings {
		if traced.Warnings[i] != untraced.Warnings[i] {
			t.Errorf("Warnings[%d]: traced %+v != untraced %+v", i, traced.Warnings[i], untraced.Warnings[i])
		}
	}

	// Rate limit.
	if (traced.RateLimit == nil) != (untraced.RateLimit == nil) {
		t.Fatalf("RateLimit presence: traced=%v untraced=%v", traced.RateLimit != nil, untraced.RateLimit != nil)
	}
	if traced.RateLimit != nil {
		if (traced.RateLimit.RequestsRemaining == nil) != (untraced.RateLimit.RequestsRemaining == nil) {
			t.Fatalf("RateLimit.RequestsRemaining presence mismatch")
		}
		if traced.RateLimit.RequestsRemaining != nil &&
			*traced.RateLimit.RequestsRemaining != *untraced.RateLimit.RequestsRemaining {
			t.Errorf("RateLimit.RequestsRemaining: traced %d != untraced %d",
				*traced.RateLimit.RequestsRemaining, *untraced.RateLimit.RequestsRemaining)
		}
	}
}

// TestTracedCompletionCarriesReturnedModel guards the specific pre-#605 loss:
// the streaming path stamped resp.Model = req.Model (the requested alias),
// discarding the model the provider actually reported. The returned model must
// win, with the requested model only as a fallback when the stream carried none.
func TestTracedCompletionCarriesReturnedModel(t *testing.T) {
	canonical := canonicalResponse()
	adapter := &parityAdapter{name: "alpha", response: canonical, events: streamOf(canonical)}
	client, err := NewClient(WithProvider(adapter), WithDefaultProvider("alpha"))
	if err != nil {
		t.Fatalf("NewClient: %v", err)
	}
	defer client.Close()

	req := &Request{
		Model:          "requested-alias",
		Messages:       []Message{UserMessage("hi")},
		TraceObservers: []TraceObserver{TraceObserverFunc(func(TraceEvent) {})},
	}
	resp, err := client.Complete(context.Background(), req)
	if err != nil {
		t.Fatalf("Complete: %v", err)
	}
	if resp.Model != "provider-model-2026" {
		t.Errorf("Model = %q, want the returned model %q (not the requested alias)", resp.Model, "provider-model-2026")
	}
}

// TestTracedCompletionFallsBackToRequestedModel checks that a stream carrying no
// model metadata still yields the requested model (back-compat: an adapter that
// never sets FullResponse.Model must not produce an empty resp.Model).
func TestTracedCompletionFallsBackToRequestedModel(t *testing.T) {
	adapter := &parityAdapter{
		name:     "alpha",
		response: &Response{ID: "x", Message: AssistantMessage("hi")},
		events: []StreamEvent{
			{Type: EventStreamStart},
			{Type: EventTextDelta, TextID: "b0", Delta: "hi"},
			{Type: EventFinish, FinishReason: &FinishReason{Reason: "stop"}},
		},
	}
	client, err := NewClient(WithProvider(adapter), WithDefaultProvider("alpha"))
	if err != nil {
		t.Fatalf("NewClient: %v", err)
	}
	defer client.Close()

	req := &Request{
		Model:          "requested-alias",
		Messages:       []Message{UserMessage("hi")},
		TraceObservers: []TraceObserver{TraceObserverFunc(func(TraceEvent) {})},
	}
	resp, err := client.Complete(context.Background(), req)
	if err != nil {
		t.Fatalf("Complete: %v", err)
	}
	if resp.Model != "requested-alias" {
		t.Errorf("Model = %q, want fallback to requested %q", resp.Model, "requested-alias")
	}
}
