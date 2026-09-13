// ABOUTME: Proves slow compatible streams survive total deadlines while silent hangs still fail.
// ABOUTME: Exercises actual HTTP effects, raw-byte progress, completion markers, and cancellation.
package openaicompat

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/2389-research/tracker/llm"
)

const streamFinish = "data: {\"choices\":[{\"index\":0,\"delta\":{\"content\":\"finished\"},\"finish_reason\":\"stop\"}]}\n\ndata: [DONE]\n\n"

func TestStreamActiveBeyondClientTotalTimeout(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		fl := w.(http.Flusher)
		for i := 0; i < 8; i++ {
			_, _ = io.WriteString(w, ": keepalive\n\n")
			fl.Flush()
			select {
			case <-time.After(25 * time.Millisecond):
			case <-r.Context().Done():
				return
			}
		}
		_, _ = io.WriteString(w, streamFinish)
		fl.Flush()
	}))
	defer srv.Close()
	client := &http.Client{Timeout: 50 * time.Millisecond}
	a := New("test", WithBaseURL(srv.URL), WithHTTPClient(client))
	start := time.Now()
	err, finished := drainCompatStream(t, a.Stream(t.Context(), streamTestRequest()))
	if err != nil || !finished {
		t.Fatalf("active stream truncated at total timeout: finish=%v err=%v", finished, err)
	}
	if time.Since(start) < 4*client.Timeout {
		t.Fatal("fixture did not run beyond the configured total deadline")
	}
	if client.Timeout != 50*time.Millisecond {
		t.Fatal("streaming changed the caller-owned client")
	}
	_, err = a.Complete(t.Context(), streamTestRequest())
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("non-streaming request lost its total timeout: %v", err)
	}
}

func TestStreamIdleAndCallerCancellation(t *testing.T) {
	for _, headers := range []bool{false, true} {
		t.Run(map[bool]string{false: "before headers", true: "after headers"}[headers], func(t *testing.T) {
			srv := silentCompatServer(headers)
			defer srv.Close()
			a := New("test", WithBaseURL(srv.URL), WithStreamIdleTimeout(50*time.Millisecond))
			err, finished := drainCompatStream(t, a.Stream(t.Context(), streamTestRequest()))
			var retryable *llm.StreamError
			if !errors.Is(err, llm.ErrStreamIdle) || !errors.As(err, &retryable) || !retryable.Retryable() || finished {
				t.Fatalf("silent hang must fail retryably without finish: %v, %v", err, finished)
			}
			ctx, cancel := context.WithTimeout(t.Context(), 30*time.Millisecond)
			defer cancel()
			a = New("test", WithBaseURL(srv.URL), WithStreamIdleTimeout(time.Second))
			err, finished = drainCompatStream(t, a.Stream(ctx, streamTestRequest()))
			if err == nil || errors.Is(err, llm.ErrStreamIdle) || finished {
				t.Fatalf("caller cancellation mislabeled or accepted: %v, %v", err, finished)
			}
		})
	}
}

func silentCompatServer(headers bool) *httptest.Server {
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Consume the request so the server can observe a disconnected client.
		_, _ = io.Copy(io.Discard, r.Body)
		if headers {
			w.Header().Set("Content-Type", "text/event-stream")
			_, _ = io.WriteString(w, ": opened\n\n")
			w.(http.Flusher).Flush()
		}
		<-r.Context().Done()
	}))
}

func TestStreamPartialLineBytesResetIdleDeadline(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		fl := w.(http.Flusher)
		_, _ = io.WriteString(w, ":")
		fl.Flush()
		for i := 0; i < 8; i++ {
			select {
			case <-time.After(25 * time.Millisecond):
			case <-r.Context().Done():
				return
			}
			_, _ = io.WriteString(w, "x") // No newline until after the idle interval.
			fl.Flush()
		}
		_, _ = io.WriteString(w, "\n\n"+streamFinish)
		fl.Flush()
	}))
	defer srv.Close()
	a := New("test", WithBaseURL(srv.URL), WithStreamIdleTimeout(150*time.Millisecond))
	err, finished := drainCompatStream(t, a.Stream(t.Context(), streamTestRequest()))
	if err != nil || !finished {
		t.Fatalf("raw-byte progress was mistaken for silence: %v, %v", err, finished)
	}
}

func TestStreamIdleChangeStillRejectsMissingDone(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = io.WriteString(w, strings.Split(streamFinish, "data: [DONE]")[0])
	}))
	defer srv.Close()
	a := New("test", WithBaseURL(srv.URL))
	err, finished := drainCompatStream(t, a.Stream(t.Context(), streamTestRequest()))
	if err == nil || !strings.Contains(err.Error(), "[DONE] not received") || finished {
		t.Fatalf("incomplete stream accepted: %v, %v", err, finished)
	}
}

func streamTestRequest() *llm.Request {
	return &llm.Request{Model: "fixture", Messages: []llm.Message{llm.UserMessage("test")}}
}

func drainCompatStream(t *testing.T, ch <-chan llm.StreamEvent) (error, bool) {
	t.Helper()
	safety := time.NewTimer(3 * time.Second)
	defer safety.Stop()
	var streamErr error
	var finished bool
	for {
		select {
		case event, ok := <-ch:
			if !ok {
				return streamErr, finished
			}
			if event.Type == llm.EventError {
				streamErr = event.Err
			}
			finished = finished || event.Type == llm.EventFinish
		case <-safety.C:
			t.Fatal("stream failed to close within three seconds")
			return nil, false
		}
	}
}
