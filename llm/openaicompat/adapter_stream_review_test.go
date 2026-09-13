// ABOUTME: Challenges streaming deadline changes with terminal markers, bounds, and caller cancellation.
// ABOUTME: Provider denials and redirect policy remain authoritative after removing the total timeout.
package openaicompat

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/2389-research/tracker/llm"
)

func TestReviewStreamDoneDoesNotWaitForHTTPClosure(t *testing.T) {
	closed := make(chan struct{})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.Copy(io.Discard, r.Body)
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = io.WriteString(w, streamFinish)
		w.(http.Flusher).Flush()
		<-r.Context().Done()
		close(closed)
	}))
	defer srv.Close()
	a := New("test", WithBaseURL(srv.URL), WithStreamIdleTimeout(time.Second))
	err, finished := drainCompatStream(t, a.Stream(t.Context(), streamTestRequest()))
	if err != nil || !finished {
		t.Fatalf("DONE did not complete stream: %v, %v", err, finished)
	}
	select {
	case <-closed:
	case <-time.After(time.Second):
		t.Fatal("completed stream did not close the response body")
	}
}

func TestReviewActiveStreamStillHonorsCallerDeadline(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.Copy(io.Discard, r.Body)
		w.Header().Set("Content-Type", "text/event-stream")
		ticker := time.NewTicker(10 * time.Millisecond)
		defer ticker.Stop()
		for {
			select {
			case <-ticker.C:
				_, _ = io.WriteString(w, ": progress\n\n")
				w.(http.Flusher).Flush()
			case <-r.Context().Done():
				return
			}
		}
	}))
	defer srv.Close()
	ctx, cancel := context.WithTimeout(t.Context(), 100*time.Millisecond)
	defer cancel()
	a := New("test", WithBaseURL(srv.URL), WithStreamIdleTimeout(time.Second))
	err, finished := drainCompatStream(t, a.Stream(ctx, streamTestRequest()))
	if err == nil || finished || errors.Is(err, llm.ErrStreamIdle) {
		t.Fatalf("caller deadline changed into success or retryable idle: %v, %v", err, finished)
	}
}

func TestReviewDisabledIdleGuardStillAllowsCallerCancellation(t *testing.T) {
	for _, disabled := range []time.Duration{0, -time.Second} {
		srv := silentCompatServer(true)
		ctx, cancel := context.WithTimeout(t.Context(), 75*time.Millisecond)
		a := New("test", WithBaseURL(srv.URL), WithStreamIdleTimeout(disabled))
		err, finished := drainCompatStream(t, a.Stream(ctx, streamTestRequest()))
		cancel()
		srv.Close()
		if err == nil || finished || errors.Is(err, llm.ErrStreamIdle) {
			t.Fatalf("disabled idle guard lost caller cancellation: %v, %v", err, finished)
		}
	}
}

func TestReviewStreamRetainsLineLimit(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = io.WriteString(w, ":"+strings.Repeat("x", 1024*1024)+"\n\n"+streamFinish)
	}))
	defer srv.Close()
	a := New("test", WithBaseURL(srv.URL))
	err, finished := drainCompatStream(t, a.Stream(t.Context(), streamTestRequest()))
	if err == nil || !strings.Contains(err.Error(), "token too long") || finished {
		t.Fatalf("oversized line bypassed scanner limit: %v, %v", err, finished)
	}
}

func TestReviewStreamRetainsStatusAndRetryAfter(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Retry-After", "17")
		w.WriteHeader(http.StatusTooManyRequests)
		_, _ = io.WriteString(w, "rate limited")
	}))
	defer srv.Close()
	a := New("test", WithBaseURL(srv.URL))
	err, finished := drainCompatStream(t, a.Stream(t.Context(), streamTestRequest()))
	var limit *llm.RateLimitError
	if !errors.As(err, &limit) || !limit.Retryable() || limit.RetryAfter == nil || *limit.RetryAfter != 17 || finished {
		t.Fatalf("rate-limit classification changed: %v, %v", err, finished)
	}
}

func TestReviewStreamRetainsRedirectPolicy(t *testing.T) {
	var followed atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/redirected" {
			followed.Add(1)
			_, _ = io.WriteString(w, streamFinish)
			return
		}
		http.Redirect(w, r, "/redirected", http.StatusTemporaryRedirect)
	}))
	defer srv.Close()
	client := &http.Client{Timeout: time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	a := New("test", WithBaseURL(srv.URL), WithHTTPClient(client))
	err, finished := drainCompatStream(t, a.Stream(t.Context(), streamTestRequest()))
	if err == nil || finished || followed.Load() != 0 || client.Timeout != time.Second {
		t.Fatalf("redirect policy changed: followed=%d finish=%v err=%v", followed.Load(), finished, err)
	}
}

func TestReviewTracedAuthenticationErrorNeverRetries(t *testing.T) {
	var calls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		calls.Add(1)
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = io.WriteString(w, "data: {\"error\":{\"code\":\"invalid_api_key\",\"message\":\"rejected\"}}\n\ndata: [DONE]\n\n")
	}))
	defer srv.Close()
	a := New("test", WithBaseURL(srv.URL))
	client, err := llm.NewClient(llm.WithProvider(a), llm.WithMiddleware(llm.NewRetryMiddleware()))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if closeErr := client.Close(); closeErr != nil {
			t.Errorf("close client: %v", closeErr)
		}
	})
	client.AddTraceObserver(llm.TraceObserverFunc(func(llm.TraceEvent) {}))
	_, err = client.Complete(t.Context(), streamTestRequest())
	var auth *llm.AuthenticationError
	if !errors.As(err, &auth) || auth.Retryable() || calls.Load() != 1 {
		t.Fatalf("authentication refusal retried or lost: calls=%d err=%v", calls.Load(), err)
	}
}
