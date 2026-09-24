// ABOUTME: Tests for the SSE read-loop helpers shared by the provider adapters.
// ABOUTME: Covers read-error classification (idle hang vs. clean stop) and event-type resolution.
package llm

import (
	"context"
	"fmt"
	"io"
	"testing"
	"time"
)

// firedGuard returns a guard whose idle timer has already fired.
func firedGuard() *StreamIdleGuard {
	streamCtx, cancel := context.WithCancel(context.Background())
	guard := NewStreamIdleGuard(time.Nanosecond, cancel)
	<-streamCtx.Done() // the guard marks itself fired before it cancels
	return guard
}

func TestClassifySSERead(t *testing.T) {
	live := context.Background()
	done, cancel := context.WithCancel(context.Background())
	cancel()
	quiet := NewStreamIdleGuard(0, func() {}) // disabled: never fires
	fired := firedGuard()

	tests := []struct {
		name          string
		callerCtx     context.Context
		err           error
		guard         *StreamIdleGuard
		process, stop bool
		transient     error
	}{
		{"full line", live, nil, quiet, true, false, nil},
		{"clean EOF", live, io.EOF, quiet, true, true, nil},
		{"wrapped EOF", live, fmt.Errorf("read: %w", io.EOF), quiet, true, true, nil},
		// #576: an idle-guard cancel while the caller is live is a hang, not a clean end.
		{"idle guard fired", live, context.Canceled, fired, false, true, ErrStreamIdle},
		{"caller cancelled", done, context.Canceled, fired, true, true, nil},
		{"deadline without idle fire", live, context.DeadlineExceeded, quiet, true, true, nil},
		{"read failure", live, io.ErrUnexpectedEOF, quiet, false, true, io.ErrUnexpectedEOF},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			process, stop, transient := ClassifySSERead(tt.callerCtx, tt.err, tt.guard)
			if process != tt.process || stop != tt.stop || transient != tt.transient {
				t.Errorf("got (process=%v, stop=%v, transient=%v), want (%v, %v, %v)",
					process, stop, transient, tt.process, tt.stop, tt.transient)
			}
		})
	}
}

func TestResolveSSEEventType(t *testing.T) {
	tests := []struct {
		name, header, data, want string
	}{
		{"header wins", "message_start", `{"type":"ping"}`, "message_start"},
		{"payload type without header", "", `{"type":"response.completed"}`, "response.completed"},
		{"payload without type", "", `{"delta":"hi"}`, ""},
		{"non-JSON payload", "", "[DONE]", ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := ResolveSSEEventType(tt.header, tt.data); got != tt.want {
				t.Errorf("ResolveSSEEventType(%q, %q) = %q, want %q", tt.header, tt.data, got, tt.want)
			}
		})
	}
}
