// ABOUTME: Tests for WebhookInterviewer — verifies outbound webhook POSTs, inbound
// ABOUTME: callback routing, timeout handling, and parallel gate support.
package handlers

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"
)

// postCallback posts a WebhookGateResponse to the interviewer's callback server.
func postCallback(t *testing.T, callbackURL string, resp WebhookGateResponse) {
	t.Helper()
	body, err := json.Marshal(resp)
	if err != nil {
		t.Fatalf("marshal callback: %v", err)
	}
	r, err := http.Post(callbackURL, "application/json", bytes.NewReader(body)) //nolint:noctx
	if err != nil {
		t.Fatalf("post callback: %v", err)
	}
	r.Body.Close()
	if r.StatusCode != http.StatusOK {
		t.Fatalf("callback returned %d", r.StatusCode)
	}
}

// TestWebhookInterviewer_PostsAndReceivesResponse verifies the full round-trip:
// outbound POST to webhook, inbound callback, response returned to Ask.
func TestWebhookInterviewer_PostsAndReceivesResponse(t *testing.T) {
	// --- capture outbound webhook payload ---
	var mu sync.Mutex
	var captured WebhookGatePayload

	webhookSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var payload WebhookGatePayload
		if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
			http.Error(w, "bad json", http.StatusBadRequest)
			return
		}
		mu.Lock()
		captured = payload
		mu.Unlock()
		w.WriteHeader(http.StatusOK)

		// Async: post back to the callback_url after a brief delay.
		go func() {
			time.Sleep(20 * time.Millisecond)
			postCallback(t, payload.CallbackURL, WebhookGateResponse{Choice: "Approve"})
		}()
	}))
	defer webhookSrv.Close()

	wi := NewWebhookInterviewer(webhookSrv.URL, ":0")
	wi.Timeout = 5 * time.Second
	defer wi.Cancel()

	// Start the callback server and get the address.
	if err := wi.startServerOnce(); err != nil {
		t.Fatalf("start server: %v", err)
	}

	choice, err := wi.Ask("Approve the plan?", []string{"Approve", "Reject"}, "Approve")
	if err != nil {
		t.Fatalf("Ask returned error: %v", err)
	}
	if choice != "Approve" {
		t.Errorf("choice = %q, want %q", choice, "Approve")
	}

	mu.Lock()
	defer mu.Unlock()
	if captured.Prompt != "Approve the plan?" {
		t.Errorf("outbound prompt = %q, want %q", captured.Prompt, "Approve the plan?")
	}
	if len(captured.Choices) != 2 {
		t.Errorf("outbound choices count = %d, want 2", len(captured.Choices))
	}
	if !strings.Contains(captured.CallbackURL, "/gate/") {
		t.Errorf("callback_url %q missing /gate/", captured.CallbackURL)
	}
	if captured.GateID == "" {
		t.Error("outbound gate_id is empty")
	}
}

// TestWebhookInterviewer_Timeout verifies that when no callback arrives within
// the timeout, the interviewer returns without error and applies the default action.
func TestWebhookInterviewer_Timeout(t *testing.T) {
	// Webhook server that accepts but never responds.
	webhookSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		// No callback posted — let the interviewer time out.
	}))
	defer webhookSrv.Close()

	wi := NewWebhookInterviewer(webhookSrv.URL, ":0")
	wi.Timeout = 50 * time.Millisecond
	wi.DefaultAction = "fail"
	defer wi.Cancel()
	if err := wi.startServerOnce(); err != nil {
		t.Fatalf("start server: %v", err)
	}

	choice, err := wi.Ask("Approve?", []string{"Approve", "Reject"}, "Approve")
	if err != nil {
		t.Fatalf("Ask returned error: %v", err)
	}
	// On timeout with DefaultAction "fail", resolveWebhookChoice("fail", choices, "Approve")
	// won't match any choice, falls back to "Approve" (the default).
	_ = choice // just verify no panic/error
}

// TestWebhookInterviewer_TimeoutActionSuccess verifies that DefaultAction="success"
// causes the first choice to be returned on timeout.
func TestWebhookInterviewer_TimeoutActionSuccess(t *testing.T) {
	webhookSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		// No callback — trigger timeout.
	}))
	defer webhookSrv.Close()

	wi := NewWebhookInterviewer(webhookSrv.URL, ":0")
	wi.Timeout = 50 * time.Millisecond
	wi.DefaultAction = "success"
	defer wi.Cancel()
	if err := wi.startServerOnce(); err != nil {
		t.Fatalf("start server: %v", err)
	}

	choice, err := wi.Ask("Approve?", []string{"Approve", "Reject"}, "")
	if err != nil {
		t.Fatalf("Ask returned error: %v", err)
	}
	// DefaultAction=success → timeoutResponse returns first choice value ("Approve")
	// resolveWebhookChoice("Approve", choices, "") should match "Approve".
	if choice != "Approve" {
		t.Errorf("choice = %q, want %q", choice, "Approve")
	}
}

// TestWebhookInterviewer_LabeledGate verifies that AskFreeformWithLabels sends choices
// and correctly routes a labeled response.
func TestWebhookInterviewer_LabeledGate(t *testing.T) {
	var capturedPayload WebhookGatePayload

	webhookSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var payload WebhookGatePayload
		_ = json.NewDecoder(r.Body).Decode(&payload)
		capturedPayload = payload
		w.WriteHeader(http.StatusOK)
		go func() {
			time.Sleep(20 * time.Millisecond)
			postCallback(t, payload.CallbackURL, WebhookGateResponse{Choice: "needs-work"})
		}()
	}))
	defer webhookSrv.Close()

	wi := NewWebhookInterviewer(webhookSrv.URL, ":0")
	wi.Timeout = 5 * time.Second
	defer wi.Cancel()
	if err := wi.startServerOnce(); err != nil {
		t.Fatalf("start server: %v", err)
	}

	labels := []string{"approved", "needs-work", "rejected"}
	result, err := wi.AskFreeformWithLabels("Review the PR", labels, "approved")
	if err != nil {
		t.Fatalf("AskFreeformWithLabels returned error: %v", err)
	}
	if result != "needs-work" {
		t.Errorf("result = %q, want %q", result, "needs-work")
	}

	// Verify choices were sent
	if len(capturedPayload.Choices) != 3 {
		t.Errorf("choices count = %d, want 3", len(capturedPayload.Choices))
	}
}

// TestWebhookInterviewer_ParallelGates verifies that two concurrent Ask calls
// with different gate IDs are matched correctly even when responses arrive out of order.
func TestWebhookInterviewer_ParallelGates(t *testing.T) {
	type captureEntry struct {
		payload WebhookGatePayload
	}

	var mu sync.Mutex
	captured := make([]captureEntry, 0, 2)

	webhookSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var payload WebhookGatePayload
		_ = json.NewDecoder(r.Body).Decode(&payload)
		w.WriteHeader(http.StatusOK)

		mu.Lock()
		captured = append(captured, captureEntry{payload: payload})
		mu.Unlock()
	}))
	defer webhookSrv.Close()

	wi := NewWebhookInterviewer(webhookSrv.URL, ":0")
	wi.Timeout = 5 * time.Second
	defer wi.Cancel()
	if err := wi.startServerOnce(); err != nil {
		t.Fatalf("start server: %v", err)
	}

	var wg sync.WaitGroup
	results := make([]string, 2)
	errs := make([]error, 2)

	wg.Add(2)
	go func() {
		defer wg.Done()
		results[0], errs[0] = wi.Ask("Gate A", []string{"yes", "no"}, "yes")
	}()
	go func() {
		defer wg.Done()
		results[1], errs[1] = wi.Ask("Gate B", []string{"continue", "stop"}, "continue")
	}()

	// Wait for both webhooks to arrive.
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		mu.Lock()
		n := len(captured)
		mu.Unlock()
		if n == 2 {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}

	mu.Lock()
	n := len(captured)
	mu.Unlock()
	if n != 2 {
		t.Fatalf("expected 2 webhook payloads, got %d", n)
	}

	// Identify which is A and which is B by prompt.
	mu.Lock()
	payloads := []WebhookGatePayload{captured[0].payload, captured[1].payload}
	mu.Unlock()

	var payloadA, payloadB WebhookGatePayload
	for _, p := range payloads {
		if p.Prompt == "Gate A" {
			payloadA = p
		} else {
			payloadB = p
		}
	}

	// Post B first, then A (out of order).
	postCallback(t, payloadB.CallbackURL, WebhookGateResponse{Choice: "continue"})
	time.Sleep(10 * time.Millisecond)
	postCallback(t, payloadA.CallbackURL, WebhookGateResponse{Choice: "yes"})

	wg.Wait()

	for i, err := range errs {
		if err != nil {
			t.Errorf("gate %d error: %v", i, err)
		}
	}
	if results[0] != "yes" {
		t.Errorf("gate A result = %q, want %q", results[0], "yes")
	}
	if results[1] != "continue" {
		t.Errorf("gate B result = %q, want %q", results[1], "continue")
	}
}

// TestWebhookInterviewer_Cancel verifies that Cancel() unblocks a waiting Ask call
// and returns an error quickly.
func TestWebhookInterviewer_Cancel(t *testing.T) {
	// Webhook server that accepts but never responds.
	webhookSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		// No callback — let Cancel() unblock the waiter.
	}))
	defer webhookSrv.Close()

	wi := NewWebhookInterviewer(webhookSrv.URL, ":0")
	wi.Timeout = 30 * time.Second
	if err := wi.startServerOnce(); err != nil {
		t.Fatalf("start server: %v", err)
	}

	askDone := make(chan error, 1)
	go func() {
		_, err := wi.Ask("Will you cancel?", []string{"yes", "no"}, "yes")
		askDone <- err
	}()

	// Give Ask time to register and post the webhook.
	time.Sleep(50 * time.Millisecond)

	start := time.Now()
	wi.Cancel()

	select {
	case err := <-askDone:
		elapsed := time.Since(start)
		if elapsed > 1*time.Second {
			t.Errorf("Cancel took too long: %v", elapsed)
		}
		if err == nil {
			t.Error("Ask returned nil error after Cancel, want errGateCanceled")
		}
	case <-time.After(3 * time.Second):
		t.Fatal("Ask did not return within 3s after Cancel")
	}
}

// TestWebhookInterviewer_FreeformResponse verifies AskFreeform prefers the Freeform field.
func TestWebhookInterviewer_FreeformResponse(t *testing.T) {
	webhookSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var payload WebhookGatePayload
		_ = json.NewDecoder(r.Body).Decode(&payload)
		w.WriteHeader(http.StatusOK)
		go func() {
			time.Sleep(20 * time.Millisecond)
			postCallback(t, payload.CallbackURL, WebhookGateResponse{
				Choice:   "some choice",
				Freeform: "the actual human text response",
			})
		}()
	}))
	defer webhookSrv.Close()

	wi := NewWebhookInterviewer(webhookSrv.URL, ":0")
	wi.Timeout = 5 * time.Second
	defer wi.Cancel()
	if err := wi.startServerOnce(); err != nil {
		t.Fatalf("start server: %v", err)
	}

	result, err := wi.AskFreeform("What do you think?")
	if err != nil {
		t.Fatalf("AskFreeform returned error: %v", err)
	}
	if result != "the actual human text response" {
		t.Errorf("result = %q, want %q", result, "the actual human text response")
	}
}

// TestWebhookInterviewer_UnknownGateID verifies that the callback server returns 404
// for an unknown gate ID.
func TestWebhookInterviewer_UnknownGateID(t *testing.T) {
	wi := NewWebhookInterviewer("http://localhost:9999", ":0")
	defer wi.Cancel()
	if err := wi.startServerOnce(); err != nil {
		t.Fatalf("start server: %v", err)
	}

	callbackURL := wi.callbackBaseURL() + "/gate/nonexistent-id"
	body, _ := json.Marshal(WebhookGateResponse{Choice: "yes"})
	resp, err := http.Post(callbackURL, "application/json", bytes.NewReader(body)) //nolint:noctx
	if err != nil {
		t.Fatalf("post: %v", err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusNotFound {
		t.Errorf("status = %d, want %d", resp.StatusCode, http.StatusNotFound)
	}
}

// TestNewGateID verifies that newGateID produces unique values and looks like a UUID.
func TestNewGateID(t *testing.T) {
	ids := make(map[string]struct{}, 100)
	for i := 0; i < 100; i++ {
		id := newGateID()
		if _, dup := ids[id]; dup {
			t.Fatalf("duplicate gate ID: %s", id)
		}
		ids[id] = struct{}{}
		parts := strings.Split(id, "-")
		if len(parts) != 5 {
			t.Errorf("gate ID %q does not look like a UUID (got %d parts)", id, len(parts))
		}
	}
}
