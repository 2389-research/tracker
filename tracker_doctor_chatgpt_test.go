// ABOUTME: Tests for doctor's ChatGPT-subscription check on the openai provider slot: offline
// ABOUTME: sign-in status, the live refresh probe, and the informational hints that mode adds.
package tracker

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"os"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/2389-research/tracker/internal/chatgptauth"
)

// fakeChatGPTIDToken mints an unsigned id_token whose auth claim carries
// accountID and plan. ReadStatus (via chatgptauth.ParseIDToken) trusts it
// without checking the signature, matching production.
func fakeChatGPTIDToken(t *testing.T, accountID, plan string) string {
	t.Helper()
	claims, err := json.Marshal(map[string]any{
		"https://api.openai.com/auth": map[string]any{
			"chatgpt_account_id": accountID,
			"chatgpt_plan_type":  plan,
		},
	})
	if err != nil {
		t.Fatalf("marshal id_token claims: %v", err)
	}
	enc := base64.RawURLEncoding.EncodeToString
	return enc([]byte(`{"alg":"none","typ":"JWT"}`)) + "." + enc(claims) + ".sig"
}

// saveFullChatGPTSignIn writes a complete sign-in for account acct-1 — an
// id_token carrying plan, plus access and refresh tokens — so ReadStatus
// reports a plan and evaluateChatGPT's probe has a refresh token to spend.
// Unlike saveChatGPTSignIn (tracker_chatgpt_client_test.go), this fills in
// IDToken: ReadStatus parses it to read the plan type.
func saveFullChatGPTSignIn(t *testing.T, path, access, refresh, plan string) {
	t.Helper()
	tokens := chatgptauth.Tokens{
		IDToken:      fakeChatGPTIDToken(t, "acct-1", plan),
		AccessToken:  access,
		RefreshToken: refresh,
		AccountID:    "acct-1",
		LastRefresh:  time.Now(),
	}
	if err := chatgptauth.Save(path, tokens); err != nil {
		t.Fatalf("save the sign-in: %v", err)
	}
}

// aimDoctorChatGPTIssuer points evaluateChatGPT's refresh probe at url for
// the rest of the test, restoring the production issuer after.
func aimDoctorChatGPTIssuer(t *testing.T, url string) {
	t.Helper()
	prev := doctorChatGPTIssuer
	doctorChatGPTIssuer = url
	t.Cleanup(func() { doctorChatGPTIssuer = prev })
}

// TestDoctorChatGPTSignedIn covers both OK message shapes verbatim (design
// §3 "Doctor"): a probed sign-in reports the plan and ", refresh ok" and
// spends exactly one refresh (never a generation request); a skipped probe
// reports "signed in" alone and touches the network not at all. With no
// plan on the id_token, the plan suffix drops.
func TestDoctorChatGPTSignedIn(t *testing.T) {
	cases := []struct {
		name  string
		plan  string
		probe bool
		want  string
	}{
		{"plan and a good probe", "plus", true, "OpenAI          ChatGPT subscription (plus plan), signed in, refresh ok"},
		{"no plan, no probe", "", false, "OpenAI          ChatGPT subscription, signed in"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			path := isolateClientEnv(t)
			t.Setenv(chatgptauth.EnvVar, "chatgpt")
			saveFullChatGPTSignIn(t, path, "access-1", "refresh-1", tc.plan)

			trap := newTrap(t) // a skipped probe must reach no server
			issuerURL := trap.URL
			var backend *recordingServer
			if tc.probe {
				resp := `{"id_token":"` + fakeChatGPTIDToken(t, "acct-1", tc.plan) +
					`","access_token":"access-2","refresh_token":"refresh-2"}`
				backend = newRecordingServer(t, http.StatusOK, "application/json", resp)
				issuerURL = backend.URL
			}
			aimDoctorChatGPTIssuer(t, issuerURL)

			detail, state := evaluateChatGPT(context.Background(), tc.probe)

			if state != stateConfigured {
				t.Errorf("state = %v, want stateConfigured", state)
			}
			if detail.Status != CheckStatusOK || detail.Message != tc.want {
				t.Errorf("detail = %+v, want Status=ok Message=%q", detail, tc.want)
			}
			if !tc.probe {
				if n := len(trap.requests()); n != 0 {
					t.Errorf("trap received %d request(s); probe=false must refresh nothing", n)
				}
				return
			}
			reqs := backend.requests()
			if len(reqs) != 1 || reqs[0].path != "/oauth/token" {
				t.Errorf("backend requests = %+v, want exactly one /oauth/token refresh", reqs)
			}
		})
	}
}

// TestDoctorChatGPTNotSignedIn: the switch is on but the token file doesn't
// exist. This must be decided offline — the trap fails the test if
// evaluateChatGPT reaches the network at all.
func TestDoctorChatGPTNotSignedIn(t *testing.T) {
	isolateClientEnv(t)
	t.Setenv(chatgptauth.EnvVar, "chatgpt")
	trap := newTrap(t)
	aimDoctorChatGPTIssuer(t, trap.URL)

	detail, state := evaluateChatGPT(context.Background(), true)

	if state != stateInvalid {
		t.Errorf("state = %v, want stateInvalid", state)
	}
	wantMsg := "OpenAI          TRACKER_OPENAI_AUTH=chatgpt but not signed in"
	wantHint := "run tracker setup → OpenAI → ChatGPT subscription"
	if detail.Status != CheckStatusError || detail.Message != wantMsg || detail.Hint != wantHint {
		t.Errorf("detail = %+v, want Status=error Message=%q Hint=%q", detail, wantMsg, wantHint)
	}
	if n := len(trap.requests()); n != 0 {
		t.Errorf("trap received %d request(s); not-signed-in must probe nothing", n)
	}
}

// TestDoctorChatGPTProbeRevoked: the fake issuer answers the refresh grant
// with the permanent refresh_token_invalidated code (classifyRefreshFailure,
// refresh.go), as it would after a sign-out or a token reused elsewhere.
func TestDoctorChatGPTProbeRevoked(t *testing.T) {
	path := isolateClientEnv(t)
	t.Setenv(chatgptauth.EnvVar, "chatgpt")
	saveFullChatGPTSignIn(t, path, "access-1", "refresh-1", "plus")
	backend := newRecordingServer(t, http.StatusBadRequest, "application/json", `{"error":"refresh_token_invalidated"}`)
	aimDoctorChatGPTIssuer(t, backend.URL)

	detail, state := evaluateChatGPT(context.Background(), true)

	if state != stateInvalid {
		t.Errorf("state = %v, want stateInvalid", state)
	}
	if detail.Status != CheckStatusError {
		t.Errorf("Status = %v, want error", detail.Status)
	}
	if !strings.Contains(detail.Message, "tracker setup") {
		t.Errorf("Message = %q, want it to mention tracker setup", detail.Message)
	}
	wantHint := "run tracker setup → OpenAI → ChatGPT subscription"
	if detail.Hint != wantHint {
		t.Errorf("Hint = %q, want %q", detail.Hint, wantHint)
	}
}

// TestDoctorChatGPTUnsafeFile: a loosened mode on the token file must be
// refused before any content is read — including before a probe, which the
// trap proves never ran.
func TestDoctorChatGPTUnsafeFile(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("mode bits aren't checked on Windows; see internal/chatgptauth/store_windows.go")
	}
	path := isolateClientEnv(t)
	t.Setenv(chatgptauth.EnvVar, "chatgpt")
	saveFullChatGPTSignIn(t, path, "access-1", "refresh-1", "plus")
	if err := os.Chmod(path, 0o644); err != nil {
		t.Fatalf("chmod: %v", err)
	}
	trap := newTrap(t)
	aimDoctorChatGPTIssuer(t, trap.URL)

	detail, state := evaluateChatGPT(context.Background(), true)

	if state != stateInvalid {
		t.Errorf("state = %v, want stateInvalid", state)
	}
	if detail.Status != CheckStatusError || !strings.Contains(detail.Message, "chmod 600") {
		t.Errorf("detail = %+v, want Status=error Message mentioning chmod 600", detail)
	}
	wantHint := "chmod 600 " + path
	if detail.Hint != wantHint {
		t.Errorf("Hint = %q, want %q", detail.Hint, wantHint)
	}
	if n := len(trap.requests()); n != 0 {
		t.Errorf("trap received %d request(s); an unsafe file must probe nothing", n)
	}
}

// TestDoctorChatGPTHints pins the ChatGPT-mode notes verbatim: each setting
// that doesn't apply to openai gets its own line, and the cost note is
// unconditional in chatgpt mode. Every one is a hint, which never makes
// doctor exit 2 on its own, and the gateway line says the other providers
// still use the gateway. Outside chatgpt mode there are none.
func TestDoctorChatGPTHints(t *testing.T) {
	isolateClientEnv(t)

	t.Run("not chatgpt mode", func(t *testing.T) {
		if got := chatgptHints(); got != nil {
			t.Errorf("chatgptHints() outside chatgpt mode = %+v, want nil", got)
		}
	})

	t.Setenv(chatgptauth.EnvVar, "chatgpt")

	t.Run("nothing else set", func(t *testing.T) {
		got := chatgptHints()
		want := "subscription calls cost $0 here; dollar ceilings don't count them"
		if len(got) != 1 || got[0].Status != CheckStatusHint || got[0].Message != want {
			t.Errorf("chatgptHints() = %+v, want exactly one hint: Status=hint Message=%q", got, want)
		}
	})

	t.Run("base URL, key and gateway set", func(t *testing.T) {
		t.Setenv("OPENAI_BASE_URL", "https://example.com")
		t.Setenv("TRACKER_GATEWAY_URL", "https://gw.example.com")
		t.Setenv("OPENAI_API_KEY", "sk-test")

		got := chatgptHints()
		wantMessages := []string{
			"OPENAI_BASE_URL is set but doesn't apply to openai in ChatGPT mode",
			"TRACKER_GATEWAY_URL doesn't apply to openai in ChatGPT mode; other providers still use it",
			"OPENAI_API_KEY is set but not used for openai in ChatGPT mode",
			"subscription calls cost $0 here; dollar ceilings don't count them",
		}
		if len(got) != len(wantMessages) {
			t.Fatalf("chatgptHints() = %+v, want %d details", got, len(wantMessages))
		}
		for i, want := range wantMessages {
			if got[i].Status != CheckStatusHint || got[i].Message != want {
				t.Errorf("hint %d = %+v, want Status=hint Message=%q", i, got[i], want)
			}
		}
	})
}

// TestDoctorChatGPTNeverPrintsTokensOrAccount scans every evaluateChatGPT
// outcome — a successful probe, a revoked refresh, and no sign-in at all —
// for the account ID or either token value. None of doctor's text may name
// them (design §3 "Doctor": "It never prints the account ID or email").
func TestDoctorChatGPTNeverPrintsTokensOrAccount(t *testing.T) {
	const access, refresh = "super-secret-access-xyz", "super-secret-refresh-xyz"
	sensitive := []string{access, refresh, "acct-1"}
	check := func(t *testing.T, d CheckDetail) {
		t.Helper()
		for _, s := range sensitive {
			if strings.Contains(d.Message, s) || strings.Contains(d.Hint, s) {
				t.Errorf("detail %+v leaks %q", d, s)
			}
		}
	}

	t.Run("signed in, refresh ok", func(t *testing.T) {
		path := isolateClientEnv(t)
		t.Setenv(chatgptauth.EnvVar, "chatgpt")
		saveFullChatGPTSignIn(t, path, access, refresh, "plus")
		resp := `{"id_token":"` + fakeChatGPTIDToken(t, "acct-1", "plus") +
			`","access_token":"new-access","refresh_token":"new-refresh"}`
		backend := newRecordingServer(t, http.StatusOK, "application/json", resp)
		aimDoctorChatGPTIssuer(t, backend.URL)

		detail, _ := evaluateChatGPT(context.Background(), true)
		check(t, detail)
	})

	t.Run("probe revoked", func(t *testing.T) {
		path := isolateClientEnv(t)
		t.Setenv(chatgptauth.EnvVar, "chatgpt")
		saveFullChatGPTSignIn(t, path, access, refresh, "plus")
		backend := newRecordingServer(t, http.StatusUnauthorized, "application/json", `{"error":"refresh_token_invalidated"}`)
		aimDoctorChatGPTIssuer(t, backend.URL)

		detail, _ := evaluateChatGPT(context.Background(), true)
		check(t, detail)
	})

	t.Run("not signed in", func(t *testing.T) {
		isolateClientEnv(t)
		t.Setenv(chatgptauth.EnvVar, "chatgpt")

		detail, _ := evaluateChatGPT(context.Background(), false)
		check(t, detail)
	})
}

// TestEvaluateProviderInvalidChatGPTSwitch covers evaluateProvider's
// OpenAI-slot dispatch directly: an unparseable TRACKER_OPENAI_AUTH is an
// error in place of the ordinary key check, naming the bad value.
func TestEvaluateProviderInvalidChatGPTSwitch(t *testing.T) {
	isolateClientEnv(t)
	t.Setenv(chatgptauth.EnvVar, "yes")
	var openai providerDef
	for _, p := range knownProviders {
		if p.name == "OpenAI" {
			openai = p
		}
	}

	detail, state := evaluateProvider(context.Background(), openai, true)

	if state != stateInvalid {
		t.Errorf("state = %v, want stateInvalid", state)
	}
	want := "OpenAI          TRACKER_OPENAI_AUTH=yes is not chatgpt or api-key"
	if detail.Status != CheckStatusError || detail.Message != want {
		t.Errorf("detail = %+v, want Status=error Message=%q", detail, want)
	}
}

// TestCheckProvidersChatGPTOnlySignInCountsAsConfigured: a machine with
// only the ChatGPT sign-in — no API key for any provider — must not get
// doctor's "no providers configured" result, and the always-on cost note
// must land in the LLM Providers check's own details as a hint. The check
// stays ok, so a correct subscription-only setup can pass doctor with exit
// 0.
func TestCheckProvidersChatGPTOnlySignInCountsAsConfigured(t *testing.T) {
	path := isolateClientEnv(t)
	t.Setenv(chatgptauth.EnvVar, "chatgpt")
	saveFullChatGPTSignIn(t, path, "access-1", "refresh-1", "plus")
	resp := `{"id_token":"` + fakeChatGPTIDToken(t, "acct-1", "plus") +
		`","access_token":"access-2","refresh_token":"refresh-2"}`
	backend := newRecordingServer(t, http.StatusOK, "application/json", resp)
	aimDoctorChatGPTIssuer(t, backend.URL)

	r := checkProviders(context.Background(), true)

	if r.Status == CheckStatusError || strings.Contains(r.Message, "no LLM providers configured") {
		t.Errorf("checkProviders() = %+v, a signed-in ChatGPT subscription alone must count as configured", r)
	}
	if r.Status != CheckStatusOK {
		t.Errorf("Status = %v, want ok (the ChatGPT-mode notes are hints, never warnings)", r.Status)
	}
	const costNote = "subscription calls cost $0 here; dollar ceilings don't count them"
	found := false
	for _, d := range r.Details {
		if d.Message == costNote && d.Status == CheckStatusHint {
			found = true
		}
	}
	if !found {
		t.Errorf("Details = %+v, want the always-on ChatGPT cost note as a hint", r.Details)
	}
}

// TestDoctorChatGPTProbeTransient: a refresh probe that fails on the
// network or with a server error says nothing about the sign-in, so its
// hint points at connectivity, as probeProviderDetail's does for a key
// probe, and never sends the user back through tracker setup.
func TestDoctorChatGPTProbeTransient(t *testing.T) {
	closed := newTrap(t)
	closed.Close() // nothing listens at this URL any more
	cases := []struct {
		name   string
		issuer func(t *testing.T) string
		want   string // in the detail message
	}{
		{"server error", func(t *testing.T) string {
			return newRecordingServer(t, http.StatusServiceUnavailable, "application/json", `{"error":"overloaded"}`).URL
		}, "HTTP 503"},
		{"unreachable issuer", func(*testing.T) string { return closed.URL }, "refreshing the ChatGPT sign-in failed"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			path := isolateClientEnv(t)
			t.Setenv(chatgptauth.EnvVar, "chatgpt")
			saveFullChatGPTSignIn(t, path, "access-1", "refresh-1", "plus")
			aimDoctorChatGPTIssuer(t, tc.issuer(t))

			detail, state := evaluateChatGPT(context.Background(), true)

			if state != stateInvalid || detail.Status != CheckStatusError {
				t.Errorf("state = %v, detail = %+v; want stateInvalid and Status=error", state, detail)
			}
			if !strings.Contains(detail.Message, tc.want) {
				t.Errorf("Message = %q, want it to contain %q", detail.Message, tc.want)
			}
			if strings.Contains(detail.Hint, "tracker setup") || !strings.Contains(detail.Hint, "connectivity") {
				t.Errorf("Hint = %q, want a connectivity hint that doesn't send the user to tracker setup", detail.Hint)
			}
		})
	}
}
