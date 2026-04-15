// ABOUTME: WebhookInterviewer posts human gate prompts to an HTTP webhook and waits
// ABOUTME: for a callback with the response. Designed for headless execution environments.
package handlers

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"log"
	"net"
	"net/http"
	"strings"
	"sync"
	"time"
)

const (
	// defaultWebhookTimeout is the default time to wait for a human reply.
	defaultWebhookTimeout = 10 * time.Minute

	// defaultCallbackAddr is the default local address for the callback server.
	defaultCallbackAddr = ":8789"
)

// WebhookGateChoice represents a single selectable option in a gate prompt.
type WebhookGateChoice struct {
	Label string `json:"label"`
	Value string `json:"value"`
}

// WebhookGatePayload is the JSON body POSTed to the outbound webhook URL.
type WebhookGatePayload struct {
	GateID         string              `json:"gate_id"`
	RunID          string              `json:"run_id,omitempty"`
	NodeID         string              `json:"node_id,omitempty"`
	Prompt         string              `json:"prompt"`
	Context        string              `json:"context,omitempty"`
	Choices        []WebhookGateChoice `json:"choices"`
	CallbackURL    string              `json:"callback_url"`
	TimeoutSeconds int                 `json:"timeout_seconds"`
	// GateToken is a per-gate shared secret. The callback must echo this value
	// in the X-Tracker-Gate-Token request header, or the server rejects it with 401.
	// This provides lightweight replay protection for local/tunneled deployments.
	// For production use, wrap the callback server behind TLS and a tunnel.
	GateToken string `json:"gate_token"`
}

// WebhookGateResponse is the JSON body POSTed back to the local callback server.
type WebhookGateResponse struct {
	Choice    string `json:"choice"`
	Freeform  string `json:"freeform,omitempty"`
	Reasoning string `json:"reasoning,omitempty"`
}

// webhookPending holds the reply channel and auth token for a pending gate.
type webhookPending struct {
	ch    chan WebhookGateResponse
	token string // per-gate shared secret; must match X-Tracker-Gate-Token header
}

// WebhookInterviewer posts human gate prompts to an HTTP webhook and waits
// for a callback with the response. It is designed for headless execution
// environments (factory worker, Slack bot, email relay, mobile push) where
// a real human responds asynchronously over some transport.
//
// The flow:
//  1. A human gate handler calls Ask / AskFreeform / AskFreeformWithLabels.
//  2. The interviewer POSTs the prompt + choices to WebhookURL with a unique gate ID.
//  3. The interviewer blocks on an internal reply channel (with timeout).
//  4. The external system POSTs the response back to CallbackAddr at /gate/<gateID>.
//  5. The interviewer delivers the response to the waiting handler.
//
// Multiple interviewer instances in parallel branches are supported via per-gate IDs.
type WebhookInterviewer struct {
	// WebhookURL is where gate prompts are POSTed. Required.
	WebhookURL string

	// CallbackAddr is the local TCP address for the inbound callback server.
	// Defaults to ":8789". The external system must be able to reach this address.
	CallbackAddr string

	// Timeout is how long to wait for a human reply before applying DefaultAction.
	// Defaults to 10 minutes when zero.
	Timeout time.Duration

	// DefaultAction controls what happens on timeout: "fail" (default, pipeline
	// routes through failure edges) or "success" (return the first choice).
	// Only these two values are supported — the interviewer has no per-call
	// visibility into node attrs at construction time.
	DefaultAction string

	// AuthHeader is sent as the Authorization header on outbound webhook POSTs.
	// Leave empty for no authentication (only appropriate for trusted internal networks).
	// Not logged in verbose mode.
	AuthHeader string

	// RunID is an optional pipeline run ID included in outbound payloads for correlation.
	RunID string

	httpClient *http.Client
	pending    sync.Map // gateID (string) -> *webhookPending
	server     *http.Server
	listener   net.Listener
	serverOnce sync.Once
	serverErr  error
	cancelOnce sync.Once
	canceled   chan struct{}
}

// NewWebhookInterviewer creates a WebhookInterviewer with sensible defaults.
func NewWebhookInterviewer(webhookURL, callbackAddr string) *WebhookInterviewer {
	if callbackAddr == "" {
		callbackAddr = defaultCallbackAddr
	}
	return &WebhookInterviewer{
		WebhookURL:   webhookURL,
		CallbackAddr: callbackAddr,
		httpClient:   &http.Client{Timeout: 30 * time.Second},
		canceled:     make(chan struct{}),
	}
}

// Compile-time assertions: WebhookInterviewer implements LabeledFreeformInterviewer.
var _ LabeledFreeformInterviewer = (*WebhookInterviewer)(nil)

// effectiveTimeout returns the configured timeout, falling back to the default.
func (w *WebhookInterviewer) effectiveTimeout() time.Duration {
	if w.Timeout > 0 {
		return w.Timeout
	}
	return defaultWebhookTimeout
}

// startServerOnce starts the callback HTTP server on the first call.
// Subsequent calls are no-ops; errors are cached and returned.
func (w *WebhookInterviewer) startServerOnce() error {
	w.serverOnce.Do(func() {
		ln, err := net.Listen("tcp", w.CallbackAddr)
		if err != nil {
			w.serverErr = fmt.Errorf("webhook callback server: listen on %s: %w", w.CallbackAddr, err)
			return
		}
		w.listener = ln

		mux := http.NewServeMux()
		mux.HandleFunc("/gate/", w.handleCallback)

		w.server = &http.Server{
			Handler:           mux,
			ReadHeaderTimeout: 10 * time.Second,
			ReadTimeout:       30 * time.Second,
			WriteTimeout:      30 * time.Second,
			IdleTimeout:       60 * time.Second,
		}
		go func() {
			if err := w.server.Serve(ln); err != nil && err != http.ErrServerClosed {
				log.Printf("[webhook] callback server error: %v", err)
			}
		}()
	})
	return w.serverErr
}

// callbackBaseURL returns the base URL where the callback server is listening.
// Uses the actual bound address (important when port 0 is used in tests).
//
// When the listener is bound to a wildcard address (0.0.0.0 or [::]), the
// returned URL uses 127.0.0.1 so that local webhook services can dial back to
// the callback server. If the webhook service is remote, the author is
// responsible for making the callback port reachable (port forwarding, ngrok,
// etc.) and should set CallbackAddr to a routable address.
func (w *WebhookInterviewer) callbackBaseURL() string {
	if w.listener != nil {
		addr := w.listener.Addr().String()
		// Rewrite wildcard listen addresses to loopback for dialable callback URLs.
		if strings.HasPrefix(addr, "0.0.0.0:") {
			addr = "127.0.0.1:" + strings.TrimPrefix(addr, "0.0.0.0:")
		} else if strings.HasPrefix(addr, "[::]:") {
			addr = "127.0.0.1:" + strings.TrimPrefix(addr, "[::]:")
		}
		return "http://" + addr
	}
	addr := w.CallbackAddr
	if strings.HasPrefix(addr, ":") {
		addr = "localhost" + addr
	}
	return "http://" + addr
}

// handleCallback handles inbound POST /gate/<gateID> requests from external systems.
// The request must carry the per-gate shared secret in the X-Tracker-Gate-Token
// header (the value was included in the outbound payload as gate_token). Requests
// with a missing or mismatched token are rejected with 401.
func (w *WebhookInterviewer) handleCallback(rw http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(rw, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	// Path is /gate/<gateID>
	gateID := strings.TrimPrefix(r.URL.Path, "/gate/")
	if gateID == "" {
		http.Error(rw, "missing gate_id", http.StatusBadRequest)
		return
	}

	// Look up the pending gate first so we can verify the token.
	val, ok := w.pending.Load(gateID)
	if !ok {
		http.Error(rw, "unknown gate_id", http.StatusNotFound)
		return
	}
	pending := val.(*webhookPending)

	// Verify the per-gate shared secret.
	if r.Header.Get("X-Tracker-Gate-Token") != pending.token {
		http.Error(rw, "invalid or missing X-Tracker-Gate-Token", http.StatusUnauthorized)
		return
	}

	// Cap body size to prevent memory exhaustion.
	r.Body = http.MaxBytesReader(rw, r.Body, 64*1024)

	var resp WebhookGateResponse
	if err := json.NewDecoder(r.Body).Decode(&resp); err != nil {
		http.Error(rw, "invalid JSON body: "+err.Error(), http.StatusBadRequest)
		return
	}

	select {
	case pending.ch <- resp:
		rw.WriteHeader(http.StatusOK)
		_, _ = rw.Write([]byte(`{"status":"ok"}`))
	default:
		// Channel full or already answered — gate already resolved.
		http.Error(rw, "gate already resolved", http.StatusConflict)
	}
}

// postWebhook POSTs the gate payload to the configured WebhookURL.
// Does not log the URL or auth header.
func (w *WebhookInterviewer) postWebhook(payload WebhookGatePayload) error {
	body, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("marshal webhook payload: %w", err)
	}

	req, err := http.NewRequestWithContext(context.Background(), http.MethodPost, w.WebhookURL, bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("create webhook request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	if w.AuthHeader != "" {
		req.Header.Set("Authorization", w.AuthHeader)
	}

	resp, err := w.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("post to webhook: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 400 {
		return fmt.Errorf("webhook returned HTTP %d", resp.StatusCode)
	}
	return nil
}

// waitForResponse blocks until a response arrives, the timeout fires, or Cancel is called.
// Returns (response, timedOut, err). On cancel, err = errGateCanceled.
func (w *WebhookInterviewer) waitForResponse(gateID string, timeout time.Duration, choices []WebhookGateChoice) (WebhookGateResponse, bool, error) {
	val, _ := w.pending.Load(gateID)
	pending := val.(*webhookPending)

	timer := time.NewTimer(timeout)
	defer timer.Stop()

	select {
	case resp := <-pending.ch:
		return resp, false, nil
	case <-timer.C:
		return w.timeoutResponse(choices), true, nil
	case <-w.canceled:
		return WebhookGateResponse{}, false, errGateCanceled
	}
}

var errGateCanceled = fmt.Errorf("webhook gate canceled")

// timeoutResponse builds the response to use on timeout based on DefaultAction.
// Supported values: "success" returns the first available choice; everything else
// (including "fail" and unrecognised values) returns a "fail" response.
func (w *WebhookInterviewer) timeoutResponse(choices []WebhookGateChoice) WebhookGateResponse {
	action := strings.ToLower(strings.TrimSpace(w.DefaultAction))
	switch action {
	case "success":
		if len(choices) > 0 {
			return WebhookGateResponse{Choice: choices[0].Value, Freeform: "gate timeout — using first choice"}
		}
		return WebhookGateResponse{Choice: "success", Freeform: "gate timeout"}
	default:
		// "fail" or anything else → fail
		return WebhookGateResponse{Choice: "fail", Freeform: "gate timeout"}
	}
}

// newGateID generates a unique gate identifier using crypto/rand.
func newGateID() string {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		// Fallback to timestamp-based ID on rand failure (extremely unlikely).
		return fmt.Sprintf("gate-%d", time.Now().UnixNano())
	}
	// Format as UUID v4-like string: xxxxxxxx-xxxx-xxxx-xxxx-xxxxxxxxxxxx
	b[6] = (b[6] & 0x0f) | 0x40 // version 4
	b[8] = (b[8] & 0x3f) | 0x80 // variant bits
	return fmt.Sprintf("%s-%s-%s-%s-%s",
		hex.EncodeToString(b[0:4]),
		hex.EncodeToString(b[4:6]),
		hex.EncodeToString(b[6:8]),
		hex.EncodeToString(b[8:10]),
		hex.EncodeToString(b[10:16]),
	)
}

// registerPending registers a pending gate and returns the gate ID and its shared secret token.
func (w *WebhookInterviewer) registerPending() (gateID, token string) {
	gateID = newGateID()
	token = newGateID() // reuse UUID generator for token — different random bytes
	pending := &webhookPending{
		ch:    make(chan WebhookGateResponse, 1),
		token: token,
	}
	w.pending.Store(gateID, pending)
	return gateID, token
}

// cleanupPending removes the gate from the pending map after it resolves.
func (w *WebhookInterviewer) cleanupPending(gateID string) {
	w.pending.Delete(gateID)
}

// ask is the shared core: starts server, posts webhook, waits for response.
func (w *WebhookInterviewer) ask(prompt, contextStr string, choices []WebhookGateChoice) (WebhookGateResponse, bool, error) {
	if err := w.startServerOnce(); err != nil {
		return WebhookGateResponse{}, false, err
	}

	gateID, token := w.registerPending()
	defer w.cleanupPending(gateID)

	timeout := w.effectiveTimeout()
	callbackURL := w.callbackBaseURL() + "/gate/" + gateID

	payload := WebhookGatePayload{
		GateID:         gateID,
		RunID:          w.RunID,
		Prompt:         prompt,
		Context:        contextStr,
		Choices:        choices,
		CallbackURL:    callbackURL,
		TimeoutSeconds: int(timeout.Seconds()),
		GateToken:      token,
	}

	if err := w.postWebhook(payload); err != nil {
		return WebhookGateResponse{}, false, fmt.Errorf("webhook post failed: %w", err)
	}

	return w.waitForResponse(gateID, timeout, choices)
}

// Ask handles choice-mode gates. Choices are sent as labeled options; the response
// Choice field must match one of the choice labels or values.
func (w *WebhookInterviewer) Ask(prompt string, choices []string, defaultChoice string) (string, error) {
	gateChoices := make([]WebhookGateChoice, len(choices))
	for i, c := range choices {
		gateChoices[i] = WebhookGateChoice{Label: c, Value: c}
	}

	resp, timedOut, err := w.ask(prompt, "", gateChoices)
	if err != nil {
		return "", err
	}

	choice := resolveWebhookChoice(resp.Choice, choices, defaultChoice)
	if timedOut {
		log.Printf("[webhook] gate timed out (action=%s), returning %q", w.DefaultAction, choice)
	}
	return choice, nil
}

// resolveWebhookChoice finds the best match for the response choice against available options.
// Falls back to defaultChoice or the first option when no match is found.
func resolveWebhookChoice(responseChoice string, choices []string, defaultChoice string) string {
	if len(choices) == 0 {
		return responseChoice
	}
	normalized := strings.ToLower(strings.TrimSpace(responseChoice))
	// Exact match (case-insensitive)
	for _, c := range choices {
		if strings.ToLower(c) == normalized {
			return c
		}
	}
	// Prefix/contains match
	for _, c := range choices {
		if strings.Contains(normalized, strings.ToLower(c)) {
			return c
		}
	}
	// Fall back to default
	if defaultChoice != "" {
		return defaultChoice
	}
	if len(choices) > 0 {
		return choices[0]
	}
	return responseChoice
}

// AskFreeform handles pure freeform gates. The response Freeform field is used
// when non-empty; otherwise the Choice field is used.
func (w *WebhookInterviewer) AskFreeform(prompt string) (string, error) {
	resp, timedOut, err := w.ask(prompt, "", nil)
	if err != nil {
		return "", err
	}
	if timedOut {
		log.Printf("[webhook] freeform gate timed out (action=%s)", w.DefaultAction)
		return resp.Freeform, nil
	}
	if resp.Freeform != "" {
		return resp.Freeform, nil
	}
	return resp.Choice, nil
}

// AskFreeformWithLabels handles hybrid gates with labeled edge options.
// The response Choice field is matched against labels; Freeform is used for custom input.
func (w *WebhookInterviewer) AskFreeformWithLabels(prompt string, labels []string, defaultLabel string) (string, error) {
	gateChoices := make([]WebhookGateChoice, len(labels))
	for i, l := range labels {
		gateChoices[i] = WebhookGateChoice{Label: l, Value: l}
	}

	resp, timedOut, err := w.ask(prompt, "", gateChoices)
	if err != nil {
		return "", err
	}

	if timedOut {
		// Route the timeout action through the same label resolver a real response
		// uses. This maps "fail"/"success" to an actual label when possible, or
		// falls back to defaultLabel so the pipeline always gets a valid edge label.
		resolved := resolveWebhookChoice(resp.Choice, labels, defaultLabel)
		log.Printf("[webhook] labeled freeform gate timed out (action=%s), returning %q", w.DefaultAction, resolved)
		return resolved, nil
	}

	// Prefer Freeform when the responder typed custom text.
	if resp.Freeform != "" {
		return resp.Freeform, nil
	}
	return resolveWebhookChoice(resp.Choice, labels, defaultLabel), nil
}

// Cancel closes all pending gates and shuts down the callback server.
// Waiting Ask/AskFreeform/AskFreeformWithLabels calls return errGateCanceled.
// Safe to call multiple times (idempotent).
func (w *WebhookInterviewer) Cancel() {
	w.cancelOnce.Do(func() {
		close(w.canceled)
		if w.server != nil {
			ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
			defer cancel()
			_ = w.server.Shutdown(ctx)
		}
	})
}
