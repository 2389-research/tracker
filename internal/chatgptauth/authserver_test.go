// ABOUTME: Fake auth.openai.com for chatgptauth's device-code sign-in tests: an httptest server
// ABOUTME: that plays the documented endpoints end to end. Task 5 extends it with the refresh grant.
package chatgptauth

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"
)

// fakeAuthServer stands in for auth.openai.com across the device-code
// endpoints (research §B): the usercode request, the poll, and the final
// token exchange at /oauth/token. Every field a handler reads or a test
// sets is guarded by mu, so -race has a recognized happens-before edge
// between configuring a scenario and the request that exercises it.
type fakeAuthServer struct {
	srv *httptest.Server

	mu sync.Mutex

	// usercode (POST /api/accounts/deviceauth/usercode)
	usercodeCalls int
	usercode404   bool
	userCodeField string  // "user_code" (default, empty) or "usercode"
	intervalRaw   *string // raw JSON fragment for "interval"; nil omits the field
	deviceAuthID  string
	userCode      string

	// poll (POST /api/accounts/deviceauth/token)
	pollCalls    int
	pendingPolls int // number of pending (403) responses before a 200
	authCode     string
	codeVerifier string

	// token exchange (POST /oauth/token, grant_type=authorization_code)
	exchangeCalls    int
	exchangeOmit     string // token field name the response should omit
	lastExchangeForm url.Values

	lastHeaders http.Header // most recent request's headers, any endpoint
}

// newFakeAuthServer starts a fake auth.openai.com with a happy-path default
// scenario: zero pending polls (an immediate 200) and a full three-token
// exchange response. The server closes when t's test ends.
func newFakeAuthServer(t *testing.T) *fakeAuthServer {
	t.Helper()
	f := &fakeAuthServer{
		deviceAuthID: "dev-auth-1",
		userCode:     "WDJB-MJHT",
		authCode:     "auth-code-1",
		codeVerifier: "server-verifier-1",
	}
	mux := http.NewServeMux()
	mux.HandleFunc("/api/accounts/deviceauth/usercode", f.handleUsercode)
	mux.HandleFunc("/api/accounts/deviceauth/token", f.handlePoll)
	mux.HandleFunc("/oauth/token", f.handleToken)
	f.srv = httptest.NewServer(mux)
	t.Cleanup(f.srv.Close)
	return f
}

// URL is the fake server's base URL — point AuthClient.Issuer at it.
func (f *fakeAuthServer) URL() string { return f.srv.URL }

// Close shuts the server down, blocking until every in-flight handler has
// finished (httptest.Server.Close's own contract). A test that needs a
// deterministic "no more requests are landing" point — rather than a timed
// guess — calls this before taking its final counter reading. Safe to call
// before the t.Cleanup-registered Close from newFakeAuthServer:
// httptest.Server.Close is idempotent.
func (f *fakeAuthServer) Close() { f.srv.Close() }

// setUsercode404 makes the usercode endpoint answer 404, as a server that
// hasn't enabled device-code sign-in does (research §B).
func (f *fakeAuthServer) setUsercode404(v bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.usercode404 = v
}

// setUserCodeField picks which JSON field name carries the user code:
// "user_code" (the default) or the alternate "usercode" spelling.
func (f *fakeAuthServer) setUserCodeField(name string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.userCodeField = name
}

// setIntervalRaw sets the raw JSON fragment the usercode response's
// "interval" field carries (e.g. `"7"` or `3`); nil omits the field.
func (f *fakeAuthServer) setIntervalRaw(raw *string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.intervalRaw = raw
}

// setPendingPolls sets how many poll requests answer "still pending" (403)
// before the next one succeeds. A large value simulates "always pending."
func (f *fakeAuthServer) setPendingPolls(n int) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.pendingPolls = n
}

// setExchangeOmit makes the token exchange response omit the named field
// ("id_token", "access_token" or "refresh_token"), to test that a response
// missing a required token field is rejected.
func (f *fakeAuthServer) setExchangeOmit(field string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.exchangeOmit = field
}

// pollCallCount reports how many poll requests the server has seen so far.
func (f *fakeAuthServer) pollCallCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.pollCalls
}

// lastRequestHeaders returns the most recently received request's headers
// (any endpoint) — good enough to check User-Agent and Content-Type on
// whichever request a test just drove to completion.
func (f *fakeAuthServer) lastRequestHeaders() http.Header {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.lastHeaders
}

// lastExchangeFormValues returns the most recent token-exchange request's
// decoded form body.
func (f *fakeAuthServer) lastExchangeFormValues() url.Values {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.lastExchangeForm
}

func (f *fakeAuthServer) handleUsercode(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	f.usercodeCalls++
	f.lastHeaders = r.Header.Clone()
	disabled := f.usercode404
	field := f.userCodeField
	intervalRaw := f.intervalRaw
	deviceAuthID := f.deviceAuthID
	userCode := f.userCode
	f.mu.Unlock()

	if disabled {
		w.WriteHeader(http.StatusNotFound)
		return
	}
	if field == "" {
		field = "user_code"
	}
	resp := map[string]json.RawMessage{
		"device_auth_id": mustRawJSON(deviceAuthID),
		field:            mustRawJSON(userCode),
	}
	if intervalRaw != nil {
		resp["interval"] = json.RawMessage(*intervalRaw)
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(resp)
}

func (f *fakeAuthServer) handlePoll(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	f.pollCalls++
	call := f.pollCalls
	pending := f.pendingPolls
	authCode := f.authCode
	codeVerifier := f.codeVerifier
	f.lastHeaders = r.Header.Clone()
	f.mu.Unlock()

	if call <= pending {
		w.WriteHeader(http.StatusForbidden)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]string{
		"authorization_code": authCode,
		"code_challenge":     "server-challenge-1",
		"code_verifier":      codeVerifier,
	})
}

// handleToken serves POST /oauth/token. It dispatches on the request shape:
// today only the authorization-code grant (form-encoded, used by the
// device-code and browser sign-in flows) is implemented. Task 5 adds a
// JSON-body branch here for grant_type=refresh_token.
func (f *fakeAuthServer) handleToken(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	f.lastHeaders = r.Header.Clone()
	f.mu.Unlock()

	if strings.HasPrefix(r.Header.Get("Content-Type"), "application/x-www-form-urlencoded") {
		f.handleAuthorizationCodeGrant(w, r)
		return
	}
	http.Error(w, "unsupported grant", http.StatusBadRequest)
}

func (f *fakeAuthServer) handleAuthorizationCodeGrant(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		http.Error(w, "bad form", http.StatusBadRequest)
		return
	}

	f.mu.Lock()
	f.exchangeCalls++
	f.lastExchangeForm = r.PostForm
	omit := f.exchangeOmit
	f.mu.Unlock()

	resp := map[string]string{
		"id_token": fakeJWT(map[string]any{
			authClaimKey: map[string]any{
				"chatgpt_account_id": "acct-1",
				"chatgpt_plan_type":  "plus",
			},
		}),
		"access_token":  "fake-access-token-1",
		"refresh_token": "fake-refresh-token-1",
	}
	delete(resp, omit) // a no-op when omit is ""

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(resp)
}

func mustRawJSON(s string) json.RawMessage {
	b, err := json.Marshal(s)
	if err != nil {
		panic("mustRawJSON: " + err.Error())
	}
	return b
}
