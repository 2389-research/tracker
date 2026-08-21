// ABOUTME: Stream-idle deadline guard shared by the provider SSE adapters.
// ABOUTME: Detects a hung (byte-silent) stream and drives a retryable cancel.
package llm

import (
	"bufio"
	"context"
	"errors"
	"sync/atomic"
	"time"
)

// DefaultStreamIdleTimeout bounds how long a streaming SSE socket may go with
// ZERO bytes before it is treated as hung. It keys on raw socket bytes (any SSE
// frame — including blank-line keepalives and provider reasoning frames — resets
// it), NOT on tracker StreamEvents, because reasoning phases emit no tracker
// events (#577). The default is deliberately generous: legitimate turns reach
// ~304.5s (sonnet) and gpt-5 has 32s+ silent reasoning gaps, so the threshold
// sits comfortably above the longest observed turn to never abort a healthy
// stream. Override per-adapter via WithStreamIdleTimeout.
const DefaultStreamIdleTimeout = 10 * time.Minute

// ErrStreamIdle marks a stream cancelled by the idle deadline — distinct from a
// caller/shutdown context cancel. It is surfaced as a retryable StreamError so
// the turn-level retry middleware re-issues the completion instead of the
// channel closing with no error and the turn being silently truncated (#575,
// #576).
var ErrStreamIdle = errors.New("stream idle timeout: no SSE bytes within deadline")

// StreamIdleGuard arms an idle timer over an SSE read loop. It is created with
// the CancelFunc of an internal stream context (derived from the caller
// context): when no bytes arrive within the timeout the timer fires that cancel,
// unblocking the in-flight socket read. Each byte of progress calls Reset to
// re-arm the timer. Fired reports whether the timer — rather than the caller —
// triggered the cancel, letting the classifier distinguish an idle hang (surface
// a retryable error) from a genuine caller/shutdown cancel (stop cleanly).
//
// A non-positive timeout disables the guard (Reset/Stop are no-ops, Fired is
// always false).
type StreamIdleGuard struct {
	timeout time.Duration
	timer   *time.Timer
	fired   atomic.Bool
}

// NewStreamIdleGuard arms an idle timer that invokes cancel after timeout of
// byte-silence. A non-positive timeout returns a disabled guard.
func NewStreamIdleGuard(timeout time.Duration, cancel context.CancelFunc) *StreamIdleGuard {
	g := &StreamIdleGuard{timeout: timeout}
	if timeout > 0 {
		g.timer = time.AfterFunc(timeout, func() {
			g.fired.Store(true)
			cancel()
		})
	}
	return g
}

// Reset re-arms the idle timer after byte progress on the socket. It is a no-op
// once the timer has fired or when the guard is disabled.
func (g *StreamIdleGuard) Reset() {
	if g.timer == nil || g.fired.Load() {
		return
	}
	g.timer.Reset(g.timeout)
}

// Fired reports whether the idle timer triggered the cancel.
func (g *StreamIdleGuard) Fired() bool {
	return g.fired.Load()
}

// Stop halts the idle timer. Call it when the read loop ends.
func (g *StreamIdleGuard) Stop() {
	if g.timer != nil {
		g.timer.Stop()
	}
}

// ReadSSELine reads one line from reader and re-arms the idle deadline on any
// byte progress (a successful read — including a blank-line keepalive). It is the
// single read primitive every adapter SSE loop uses so the idle reset lives in
// one place.
func ReadSSELine(reader *bufio.Reader, guard *StreamIdleGuard) ([]byte, error) {
	line, err := reader.ReadBytes('\n')
	if err == nil {
		guard.Reset()
	}
	return line, err
}
