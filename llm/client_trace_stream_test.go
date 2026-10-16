// ABOUTME: Tests for how the traced completion path ends a stream (#701): a stream that closes
// ABOUTME: without its finish event is a retryable error, and an error event cancels and drains it.
package llm

import (
	"context"
	"errors"
	"slices"
	"sync/atomic"
	"testing"
	"time"
)

// tracedComplete runs one completion through the traced path (a request-level
// observer forces it) and returns the TraceFinish reasons the observer saw.
func tracedComplete(t *testing.T, ctx context.Context, adapter ProviderAdapter, onEvent func(TraceEvent)) (*Response, []string, error) {
	t.Helper()
	client, err := NewClient(WithProvider(adapter), WithDefaultProvider(adapter.Name()))
	if err != nil {
		t.Fatalf("NewClient: %v", err)
	}
	t.Cleanup(func() { _ = client.Close() })
	var finishes []string
	obs := TraceObserverFunc(func(evt TraceEvent) {
		if evt.Kind == TraceFinish {
			finishes = append(finishes, evt.FinishReason)
		}
		if onEvent != nil {
			onEvent(evt)
		}
	})
	resp, err := client.Complete(ctx, &Request{
		Model:          "m",
		Messages:       []Message{UserMessage("hi")},
		TraceObservers: []TraceObserver{obs},
	})
	return resp, finishes, err
}

func TestCompleteWithTraceRejectsStreamWithoutFinish(t *testing.T) {
	adapter := &mockAdapter{name: "alpha", events: []StreamEvent{
		{Type: EventStreamStart},
		{Type: EventTextDelta, Delta: "half a rep"},
	}}

	resp, finishes, err := tracedComplete(t, context.Background(), adapter, nil)

	if err == nil {
		t.Fatalf("a stream that closed before its finish event returned success: %q", resp.Message.Text())
	}
	if !errors.Is(err, ErrStreamTruncated) {
		t.Fatalf("err = %v, want ErrStreamTruncated", err)
	}
	var streamErr *StreamError
	if !errors.As(err, &streamErr) || !streamErr.Retryable() {
		t.Fatalf("err = %T %v, want a retryable *StreamError", err, err)
	}
	if !slices.Contains(finishes, "aborted") {
		t.Fatalf("no aborted TraceFinish for the truncated attempt; finishes = %v", finishes)
	}
}

func TestCompleteWithTraceAcceptsStreamWithFinish(t *testing.T) {
	adapter := &mockAdapter{name: "alpha", events: streamingMockEvents()}

	resp, _, err := tracedComplete(t, context.Background(), adapter, nil)

	if err != nil {
		t.Fatalf("finished stream: %v", err)
	}
	if got := resp.Message.Text(); got != "hello" {
		t.Fatalf("text = %q, want %q", got, "hello")
	}
}

// blockingAdapter sends from a goroutine on an unbuffered channel with plain
// blocking sends, as the real adapters do: an error event, then more events.
// A consumer that stops reading at the error leaves the goroutine blocked, so
// its deferred cleanup never runs.
type blockingAdapter struct {
	cleanedUp      chan struct{}
	lateSends      atomic.Int32
	cancelledAtEnd atomic.Bool
}

func (a *blockingAdapter) Name() string                                          { return "alpha" }
func (a *blockingAdapter) Complete(context.Context, *Request) (*Response, error) { return nil, nil }
func (a *blockingAdapter) Close() error                                          { return nil }

func (a *blockingAdapter) Stream(ctx context.Context, _ *Request) <-chan StreamEvent {
	ch := make(chan StreamEvent)
	go func() {
		defer close(a.cleanedUp)
		defer close(ch)
		ch <- StreamEvent{Type: EventTextDelta, Delta: "partial"}
		ch <- StreamEvent{Type: EventError, Err: &StreamError{SDKError: SDKError{Msg: "connection reset"}}}
		for range 3 {
			ch <- StreamEvent{Type: EventTextDelta, Delta: "late"}
			a.lateSends.Add(1)
		}
		a.cancelledAtEnd.Store(ctx.Err() != nil)
	}()
	return ch
}

func TestCompleteWithTraceCancelsAndDrainsStreamOnError(t *testing.T) {
	adapter := &blockingAdapter{cleanedUp: make(chan struct{})}

	_, _, err := tracedComplete(t, context.Background(), adapter, nil)

	if err == nil || err.Error() != "connection reset" {
		t.Fatalf("err = %v, want the stream's error", err)
	}
	select {
	case <-adapter.cleanedUp:
	case <-time.After(5 * time.Second):
		t.Fatalf("adapter goroutine still blocked 5s after the error: %d of 3 late sends delivered; the stream was not drained", adapter.lateSends.Load())
	}
	if got := adapter.lateSends.Load(); got != 3 {
		t.Fatalf("late sends delivered = %d, want 3", got)
	}
	if !adapter.cancelledAtEnd.Load() {
		t.Fatal("the stream's context was still live after the error; it must be cancelled before draining")
	}
}

// cancelClosingAdapter sends one delta, then closes the stream without an
// error event once its context ends, as an adapter does when its request is
// cancelled.
type cancelClosingAdapter struct{}

func (cancelClosingAdapter) Name() string                                          { return "alpha" }
func (cancelClosingAdapter) Complete(context.Context, *Request) (*Response, error) { return nil, nil }
func (cancelClosingAdapter) Close() error                                          { return nil }

func (cancelClosingAdapter) Stream(ctx context.Context, _ *Request) <-chan StreamEvent {
	ch := make(chan StreamEvent)
	go func() {
		defer close(ch)
		ch <- StreamEvent{Type: EventTextDelta, Delta: "partial"}
		<-ctx.Done()
	}()
	return ch
}

func TestCompleteWithTraceReturnsContextErrorWhenCancelledMidStream(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	cancelOnDelta := func(evt TraceEvent) {
		if evt.Kind == TraceText {
			cancel()
		}
	}

	resp, _, err := tracedComplete(t, ctx, cancelClosingAdapter{}, cancelOnDelta)

	if !errors.Is(err, context.Canceled) {
		if err == nil {
			t.Fatalf("a cancelled stream returned success: %q", resp.Message.Text())
		}
		t.Fatalf("err = %v, want context.Canceled", err)
	}
}
