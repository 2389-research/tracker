// ABOUTME: How the traced completion path ends a stream (#701): an error event cancels and
// ABOUTME: drains it, and a stream that closes without its finish event is a truncated reply.
package llm

import (
	"context"
	"errors"
)

// ErrStreamTruncated is the cause of the StreamError returned when a stream
// closes before its finish event: the reply is partial, not complete.
var ErrStreamTruncated = errors.New("stream ended before its finish event; the reply is truncated")

// drainEvents discards the rest of a stream, so the adapter goroutine sending
// on it can finish and run its deferred cleanup instead of blocking on a full
// channel.
func drainEvents(events <-chan StreamEvent) {
	for range events {
	}
}

// StreamEndError reports why a stream that closed without an error event
// can't be used, or nil when it finished: a cancelled request closes its
// stream without an error event, so the context's error comes first; a stream
// with no finish event is truncated, and the StreamError lets the retry
// middleware try again. sawFinish says whether an EventFinish arrived.
func StreamEndError(ctx context.Context, provider string, sawFinish bool) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if !sawFinish {
		return &StreamError{SDKError: SDKError{Msg: provider + ": " + ErrStreamTruncated.Error(), Cause: ErrStreamTruncated}}
	}
	return nil
}

// notifyTraceAborted emits an explicit terminal boundary for an attempt that
// ended without a usable reply (it will be retried, or ultimately fail), so a
// trace reader can collapse the aborted attempt's partial deltas instead of
// showing them alongside the successful retry (#550). Usage is unaffected —
// it is recorded once from the final response, never from streamed trace
// events (see the token tracker + session accounting).
func notifyTraceAborted(observers []TraceObserver, provider, model, callID string, sessionOwned bool) {
	notifyTraceObservers(observers, TraceEvent{
		Kind:         TraceFinish,
		Provider:     provider,
		Model:        model,
		CallID:       callID,
		FinishReason: "aborted",
		SessionOwned: sessionOwned,
	})
	flushTraceObservers(observers)
}
