// ABOUTME: Stream-idle deadline tests — a byte-silent hang fails retryably while
// ABOUTME: a keepalive-paced slow stream is left alone (#575/#576/#577).
package google

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/2389-research/tracker/llm"
)

const idleOpeningFrame = "data: {\"candidates\":[{\"content\":{\"parts\":[{\"text\":\"hi\"}]}}]}\n\n"

// hangingSSEServer writes one opening frame, flushes, then blocks until release
// is closed — modeling a provider that opens the stream then goes byte-silent.
func hangingSSEServer(t *testing.T, opening string, release <-chan struct{}) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		fl, ok := w.(http.Flusher)
		if !ok {
			t.Error("expected flusher")
			return
		}
		io.WriteString(w, opening)
		fl.Flush()
		<-release
	}))
}

// TestStream_IdleDeadlineSurfacesRetryableError: a stream that opens then goes
// silent past the idle deadline must surface a retryable llm.ErrStreamIdle — NOT
// close cleanly with no error (which would silently truncate the turn, #576).
func TestStream_IdleDeadlineSurfacesRetryableError(t *testing.T) {
	release := make(chan struct{})
	srv := hangingSSEServer(t, idleOpeningFrame, release)
	defer srv.Close()
	defer close(release)

	a := New("k", WithBaseURL(srv.URL), WithStreamIdleTimeout(150*time.Millisecond))
	ch := a.Stream(context.Background(), &llm.Request{Model: "gemini-2.5-pro", Messages: []llm.Message{llm.UserMessage("hi")}})

	streamErr, _ := drainUntilClose(t, ch)
	if streamErr == nil {
		t.Fatal("idle stream closed cleanly with no error — turn silently truncated (#576)")
	}
	if !errors.Is(streamErr, llm.ErrStreamIdle) {
		t.Fatalf("expected ErrStreamIdle, got %v", streamErr)
	}
	var se *llm.StreamError
	if !errors.As(streamErr, &se) || !se.Retryable() {
		t.Fatalf("idle error must be a retryable *llm.StreamError, got %T (%v)", streamErr, streamErr)
	}
}

// TestStream_SlowButAliveNotAborted: a stream that keeps sending keepalive frames
// within the idle interval (total duration well past the deadline) must NOT be
// aborted — the guard keys on per-gap byte silence, not total time (#577).
func TestStream_SlowButAliveNotAborted(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		fl := w.(http.Flusher)
		io.WriteString(w, idleOpeningFrame)
		fl.Flush()
		// 6 keepalive gaps of 50ms = ~300ms total, each gap << the 300ms deadline.
		for i := 0; i < 6; i++ {
			time.Sleep(50 * time.Millisecond)
			io.WriteString(w, "\n")
			fl.Flush()
		}
	}))
	defer srv.Close()

	a := New("k", WithBaseURL(srv.URL), WithStreamIdleTimeout(300*time.Millisecond))
	ch := a.Stream(context.Background(), &llm.Request{Model: "gemini-2.5-pro", Messages: []llm.Message{llm.UserMessage("hi")}})

	streamErr, _ := drainUntilClose(t, ch)
	if errors.Is(streamErr, llm.ErrStreamIdle) {
		t.Fatal("a keepalive-paced stream was wrongly aborted by the idle deadline")
	}
	if streamErr != nil {
		t.Fatalf("unexpected stream error: %v", streamErr)
	}
}

// drainUntilClose reads events until the channel closes or a 5s safety deadline
// elapses (the latter means the idle guard failed to unblock the read).
func drainUntilClose(t *testing.T, ch <-chan llm.StreamEvent) (streamErr error, finished bool) {
	t.Helper()
	safety := time.After(5 * time.Second)
	for {
		select {
		case ev, ok := <-ch:
			if !ok {
				return streamErr, finished
			}
			switch ev.Type {
			case llm.EventError:
				streamErr = ev.Err
			case llm.EventFinish:
				finished = true
			}
		case <-safety:
			t.Fatal("stream did not return within 5s — idle deadline was not enforced")
			return streamErr, finished
		}
	}
}
