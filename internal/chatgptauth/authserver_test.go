// ABOUTME: Fake auth.openai.com for chatgptauth's sign-in and refresh tests: an httptest server
// ABOUTME: that plays the device-code endpoints and both /oauth/token grants end to end.
package chatgptauth

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
)

// fakeAuthServer stands in for auth.openai.com across the device-code
// endpoints (research §B) — the usercode request, the poll, and the final
// token exchange at /oauth/token — and the refresh grant at the same URL
// (research §C). Every field a handler reads or a test sets is guarded by
// mu, so -race has a recognized happens-before edge between configuring a
// scenario and the request that exercises it.
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
	exchangeRedirect string // non-empty: answer 307 with this Location
	lastExchangeForm url.Values

	// refresh (POST /oauth/token, JSON body, grant_type=refresh_token)
	refreshCalls      int             // every refresh request received
	refreshReused     int             // requests answered 400 refresh_token_reused
	refreshInFlight   int             // requests received and not yet answered
	refreshHold       chan struct{}   // non-nil: each request waits until it closes
	refreshFailure    *cannedResponse // non-nil: every request gets this answer
	refreshKeepsToken bool            // a 200 omits refresh_token and does not rotate
	liveRefresh       string          // the one refresh token the server accepts
	refreshGen        int             // a 200 issues a-<refreshGen+1> (and, rotating, r-<refreshGen+1>)
	lastRefreshBody   []byte          // most recent refresh request's raw body

	lastHeaders http.Header // most recent request's headers, any endpoint
}

// cannedResponse is a fixed answer a handler gives: a status, a raw body,
// and, when location is set, a Location header (a redirect).
type cannedResponse struct {
	status   int
	body     string
	location string
}

// refreshReusedBody is the token endpoint's answer to a spent refresh token,
// in the error envelope recorded by Codex's own tests
// (codex-rs/login/src/auth/util.rs:20-35).
const refreshReusedBody = `{"error":{"message":"Your refresh token has already been used to generate a new access token. Please try signing in again.","type":"invalid_request_error","param":null,"code":"refresh_token_reused"}}`

// newFakeAuthServer starts a fake auth.openai.com with a happy-path default
// scenario: zero pending polls (an immediate 200), a full three-token
// exchange response, and a refresh grant that accepts r-1 first. The
// server closes when t's test ends.
func newFakeAuthServer(t *testing.T) *fakeAuthServer {
	t.Helper()
	f := &fakeAuthServer{
		deviceAuthID: "dev-auth-1",
		userCode:     "WDJB-MJHT",
		authCode:     "auth-code-1",
		codeVerifier: "server-verifier-1",
		liveRefresh:  "r-1",
		refreshGen:   1,
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

// setRefreshFailure makes every refresh request answer status with the raw
// body, ignoring rotation; a status of 0 restores normal answers.
func (f *fakeAuthServer) setRefreshFailure(status int, body string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.refreshFailure = nil
	if status != 0 {
		f.refreshFailure = &cannedResponse{status: status, body: body}
	}
}

// setRefreshRedirect makes every refresh request answer status (a 3xx)
// with a Location header naming location.
func (f *fakeAuthServer) setRefreshRedirect(status int, location string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.refreshFailure = &cannedResponse{status: status, location: location}
}

// setExchangeRedirect makes the token exchange answer 307 with a Location
// header naming location.
func (f *fakeAuthServer) setExchangeRedirect(location string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.exchangeRedirect = location
}

// newTrapServer starts a server that only counts the requests it receives:
// a redirect target that no request carrying a credential may ever reach.
// It closes when t's test ends.
func newTrapServer(t *testing.T) (url string, hits *atomic.Int32) {
	t.Helper()
	hits = new(atomic.Int32)
	trap := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		hits.Add(1)
	}))
	t.Cleanup(trap.Close)
	return trap.URL, hits
}

// setRefreshKeepsToken makes a successful refresh answer without a
// refresh_token, as a server that does not rotate it would, and keep
// accepting the same one.
func (f *fakeAuthServer) setRefreshKeepsToken(v bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.refreshKeepsToken = v
}

// setLiveRefreshToken makes tok the one refresh token the server accepts,
// as if the user had just signed in again and been issued it.
func (f *fakeAuthServer) setLiveRefreshToken(tok string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.liveRefresh = tok
}

// holdRefreshes makes every refresh request wait at the server until the
// returned release func runs, so a race test can witness its other callers
// in flight while one refresh is outstanding. release is idempotent and
// also runs at cleanup — registered after the server's own Close, so it
// runs first — so a failing test never leaves a handler blocked.
func (f *fakeAuthServer) holdRefreshes(t *testing.T) (release func()) {
	t.Helper()
	hold := make(chan struct{})
	f.mu.Lock()
	f.refreshHold = hold
	f.mu.Unlock()
	var once sync.Once
	release = func() { once.Do(func() { close(hold) }) }
	t.Cleanup(release)
	return release
}

// refreshCallCount reports how many refresh requests the server has received.
func (f *fakeAuthServer) refreshCallCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.refreshCalls
}

// refreshReusedCount reports how many refresh requests presented a spent
// refresh token and were answered refresh_token_reused.
func (f *fakeAuthServer) refreshReusedCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.refreshReused
}

// refreshInFlightCount reports how many refresh requests the server has
// received and not yet answered — under holdRefreshes, the ones waiting
// at the hold.
func (f *fakeAuthServer) refreshInFlightCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.refreshInFlight
}

// lastRefreshRequestBody returns the most recent refresh request's raw body.
func (f *fakeAuthServer) lastRefreshRequestBody() []byte {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.lastRefreshBody
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
// the authorization-code grant is form-encoded (used by the device-code and
// browser sign-in flows), the refresh grant is a JSON body (research §C).
func (f *fakeAuthServer) handleToken(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	f.lastHeaders = r.Header.Clone()
	f.mu.Unlock()

	contentType := r.Header.Get("Content-Type")
	switch {
	case strings.HasPrefix(contentType, "application/x-www-form-urlencoded"):
		f.handleAuthorizationCodeGrant(w, r)
	case strings.HasPrefix(contentType, "application/json"):
		f.handleRefreshGrant(w, r)
	default:
		http.Error(w, "unsupported grant", http.StatusBadRequest)
	}
}

// handleRefreshGrant serves the refresh grant with the real server's
// rotation rule: it accepts only the live refresh token, a successful
// refresh replaces that token with the next generation (r-1 → r-2, issuing
// a-2), and any other token — one already spent — is answered 400
// refresh_token_reused.
func (f *fakeAuthServer) handleRefreshGrant(w http.ResponseWriter, r *http.Request) {
	body, err := io.ReadAll(r.Body)
	if err != nil {
		http.Error(w, "bad body", http.StatusBadRequest)
		return
	}
	var grant struct {
		RefreshToken string `json:"refresh_token"`
	}
	_ = json.Unmarshal(body, &grant) // a malformed body presents "" — a spent token

	f.mu.Lock()
	f.refreshCalls++
	f.refreshInFlight++
	f.lastRefreshBody = body
	hold := f.refreshHold
	f.mu.Unlock()

	if hold != nil {
		select {
		case <-hold:
		case <-r.Context().Done(): // the client went away; answer nobody
		}
	}

	f.mu.Lock()
	f.refreshInFlight--
	answer := f.answerRefreshLocked(grant.RefreshToken)
	f.mu.Unlock()

	if answer.location != "" {
		w.Header().Set("Location", answer.location)
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(answer.status)
	_, _ = io.WriteString(w, answer.body)
}

// answerRefreshLocked decides one refresh request's answer, and advances
// the rotation on success. The caller holds f.mu.
func (f *fakeAuthServer) answerRefreshLocked(presented string) cannedResponse {
	if f.refreshFailure != nil {
		return *f.refreshFailure
	}
	if presented != f.liveRefresh {
		f.refreshReused++
		return cannedResponse{status: http.StatusBadRequest, body: refreshReusedBody}
	}
	f.refreshGen++
	resp := map[string]string{
		"id_token":     fakeIDToken("acct-1", "plus"),
		"access_token": fmt.Sprintf("a-%d", f.refreshGen),
	}
	if !f.refreshKeepsToken {
		f.liveRefresh = fmt.Sprintf("r-%d", f.refreshGen)
		resp["refresh_token"] = f.liveRefresh
	}
	encoded, err := json.Marshal(resp)
	if err != nil {
		panic("answerRefreshLocked: " + err.Error())
	}
	return cannedResponse{status: http.StatusOK, body: string(encoded)}
}

// fakeIDToken mints an id_token whose auth claim carries accountID and plan.
func fakeIDToken(accountID, plan string) string {
	return fakeJWT(map[string]any{
		authClaimKey: map[string]any{
			"chatgpt_account_id": accountID,
			"chatgpt_plan_type":  plan,
		},
	})
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
	redirect := f.exchangeRedirect
	f.mu.Unlock()

	if redirect != "" {
		w.Header().Set("Location", redirect)
		w.WriteHeader(http.StatusTemporaryRedirect)
		return
	}
	resp := map[string]string{
		"id_token":      fakeIDToken("acct-1", "plus"),
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
