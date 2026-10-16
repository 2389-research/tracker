// ABOUTME: Tests for the setup wizard's OpenAI Auth row and ChatGPT device-code sign-in: keys
// ABOUTME: drive the model, a fake loginer stands in for auth.openai.com, the token file is real.
package main

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/charmbracelet/bubbles/spinner"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/2389-research/tracker/internal/chatgptauth"
	"github.com/2389-research/tracker/internal/testutil"
)

// ── Fixtures ────────────────────────────────────────────────────────────────

const (
	testVerificationURL = "https://auth.openai.com/codex/device"
	testUserCode        = "ABCD-EFGH"
)

// fakeLoginer stands in for auth.openai.com: start and wait answer
// StartDeviceLogin and WaitForDeviceLogin.
type fakeLoginer struct {
	start func(ctx context.Context) (chatgptauth.DeviceCode, error)
	wait  func(ctx context.Context, dc chatgptauth.DeviceCode) (chatgptauth.Tokens, error)
}

func (f fakeLoginer) StartDeviceLogin(ctx context.Context) (chatgptauth.DeviceCode, error) {
	return f.start(ctx)
}

func (f fakeLoginer) WaitForDeviceLogin(ctx context.Context, dc chatgptauth.DeviceCode) (chatgptauth.Tokens, error) {
	return f.wait(ctx, dc)
}

// noLogin is a loginer for tests in which no sign-in may start: any call
// fails the test.
func noLogin(t *testing.T) fakeLoginer {
	return fakeLoginer{
		start: func(context.Context) (chatgptauth.DeviceCode, error) {
			t.Error("StartDeviceLogin called; this test starts no sign-in")
			return chatgptauth.DeviceCode{}, errors.New("unexpected sign-in")
		},
		wait: func(context.Context, chatgptauth.DeviceCode) (chatgptauth.Tokens, error) {
			t.Error("WaitForDeviceLogin called; this test starts no sign-in")
			return chatgptauth.Tokens{}, errors.New("unexpected sign-in")
		},
	}
}

// issueCode answers StartDeviceLogin with testUserCode.
func issueCode(context.Context) (chatgptauth.DeviceCode, error) {
	return chatgptauth.DeviceCode{
		VerificationURL: testVerificationURL,
		UserCode:        testUserCode,
		Interval:        5 * time.Second,
		ExpiresAt:       time.Now().Add(15 * time.Minute),
	}, nil
}

// signInTokens is what a finished device-code sign-in returns: an id_token
// whose auth claim carries the plan, plus access and refresh tokens.
func signInTokens(t *testing.T, plan string, lastRefresh time.Time) chatgptauth.Tokens {
	t.Helper()
	claims, err := json.Marshal(map[string]any{
		"https://api.openai.com/auth": map[string]any{
			"chatgpt_account_id": "acct-1",
			"chatgpt_plan_type":  plan,
		},
	})
	if err != nil {
		t.Fatalf("marshal id_token claims: %v", err)
	}
	return chatgptauth.Tokens{
		IDToken:      "e30." + base64.RawURLEncoding.EncodeToString(claims) + ".sig",
		AccessToken:  "access-token",
		RefreshToken: "refresh-token",
		AccountID:    "acct-1",
		LastRefresh:  lastRefresh,
	}
}

// testAuthPath is a token file path whose directory doesn't exist yet, as
// on a machine that has never signed in.
func testAuthPath(t *testing.T) string {
	return filepath.Join(t.TempDir(), "tracker", chatgptauth.FileName)
}

// savedSignIn writes a plus-plan sign-in last refreshed two hours ago and
// returns its path.
func savedSignIn(t *testing.T) string {
	t.Helper()
	path := testAuthPath(t)
	if err := chatgptauth.Save(path, signInTokens(t, "plus", time.Now().Add(-2*time.Hour))); err != nil {
		t.Fatalf("save the existing sign-in: %v", err)
	}
	return path
}

// ── Driving the model ───────────────────────────────────────────────────────

var (
	keySpace = tea.KeyMsg{Type: tea.KeySpace}
	keyEnter = tea.KeyMsg{Type: tea.KeyEnter}
	keyEsc   = tea.KeyMsg{Type: tea.KeyEsc}
	keyTab   = tea.KeyMsg{Type: tea.KeyTab}
	keyLeft  = tea.KeyMsg{Type: tea.KeyLeft}
	keyRight = tea.KeyMsg{Type: tea.KeyRight}
	keyCtrlC = tea.KeyMsg{Type: tea.KeyCtrlC}
	keyS     = tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'s'}}
)

// step delivers msg to the model and returns the updated model and command.
func step(t *testing.T, m setupModel, msg tea.Msg) (setupModel, tea.Cmd) {
	t.Helper()
	next, cmd := m.Update(msg)
	updated, ok := next.(setupModel)
	if !ok {
		t.Fatalf("Update returned %T, want setupModel", next)
	}
	return updated, cmd
}

// runCmd runs cmd, and each command of a tea.Batch inside it, and returns
// the messages they produce.
func runCmd(cmd tea.Cmd) []tea.Msg {
	if cmd == nil {
		return nil
	}
	msg := cmd()
	batch, ok := msg.(tea.BatchMsg)
	if !ok {
		return []tea.Msg{msg}
	}
	var msgs []tea.Msg
	for _, c := range batch {
		msgs = append(msgs, runCmd(c)...)
	}
	return msgs
}

// msgOf returns the one message of type T among msgs.
func msgOf[T tea.Msg](t *testing.T, msgs []tea.Msg) T {
	t.Helper()
	var found []T
	for _, msg := range msgs {
		if typed, ok := msg.(T); ok {
			found = append(found, typed)
		}
	}
	if len(found) != 1 {
		var zero T
		t.Fatalf("got %d messages of type %T in %#v, want exactly 1", len(found), zero, msgs)
	}
	return found[0]
}

// openAIPage checks OpenAI on the provider list, presses enter and returns
// the model on the OpenAI config page. existing is the config .env as
// setup read it.
func openAIPage(t *testing.T, existing map[string]string, login chatgptLoginer, authPath string) setupModel {
	t.Helper()
	m := newSetupModel(existing).withChatGPTSignin(login, authPath)
	m.providers[0].enabled = true
	m, _ = step(t, m, keyEnter)
	if view := m.View(); !strings.Contains(view, "Configure OpenAI") {
		t.Fatalf("not on the OpenAI page after enter:\n%s", view)
	}
	return m
}

// deliverCode runs the command that started a sign-in, hands its code
// message to the model, and returns the model and the command that polls
// for the user to finish.
func deliverCode(t *testing.T, m setupModel, startCmd tea.Cmd) (setupModel, tea.Cmd) {
	t.Helper()
	return step(t, m, msgOf[chatgptCodeMsg](t, runCmd(startCmd)))
}

// finish presses enter on the model's last provider page and returns the
// values setup would save.
func finish(t *testing.T, m setupModel) map[string]string {
	t.Helper()
	_, cmd := step(t, m, keyEnter)
	return msgOf[setupSaveMsg](t, runCmd(cmd)).Values
}

// ── The Auth row ────────────────────────────────────────────────────────────

func TestSetupOpenAIPageStartsOnAuthRow(t *testing.T) {
	m := openAIPage(t, nil, noLogin(t), testAuthPath(t))

	if view := m.View(); !strings.Contains(view, "Auth  ( ) ChatGPT subscription  (•) API key") {
		t.Fatalf("OpenAI page has no Auth row:\n%s", view)
	}
	// Focus is on the Auth row, so typing reaches neither text field.
	m, _ = step(t, m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'x'}})
	p := m.providers[0]
	if p.apiKeyInput.Focused() || p.baseURLInput.Focused() {
		t.Fatal("a text field has focus; the OpenAI page must start on the Auth row")
	}
	if got := p.apiKeyInput.Value() + p.baseURLInput.Value(); got != "" {
		t.Fatalf("typing on the Auth row reached a text field: %q", got)
	}
}

func TestSetupAuthChoiceDefaultsFromSwitch(t *testing.T) {
	const (
		chatgptChosen = "(•) ChatGPT subscription  ( ) API key"
		apiKeyChosen  = "( ) ChatGPT subscription  (•) API key"
	)
	tests := []struct {
		name  string
		saved string
		want  string
	}{
		{"chatgpt", "chatgpt", chatgptChosen},
		{"chatgpt with spaces", " chatgpt ", chatgptChosen},
		{"api-key", "api-key", apiKeyChosen},
		{"unset", "", apiKeyChosen},
		{"invalid", "ChatGPT", apiKeyChosen},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			m := openAIPage(t, map[string]string{chatgptauth.EnvVar: tt.saved}, noLogin(t), testAuthPath(t))
			if view := m.View(); !strings.Contains(view, tt.want) {
				t.Fatalf("Auth row for saved %s=%q: want %q in\n%s", chatgptauth.EnvVar, tt.saved, tt.want, view)
			}
		})
	}
}

func TestSetupAuthToggle(t *testing.T) {
	m := openAIPage(t, nil, noLogin(t), testAuthPath(t))

	m, _ = step(t, m, keySpace)
	view := m.View()
	if !strings.Contains(view, "(•) ChatGPT subscription") {
		t.Fatalf("space did not pick ChatGPT subscription:\n%s", view)
	}
	if strings.Contains(view, "API Key") || strings.Contains(view, "Base URL") {
		t.Fatalf("ChatGPT subscription still shows the API key fields:\n%s", view)
	}

	m, _ = step(t, m, keyRight)
	view = m.View()
	if !strings.Contains(view, "(•) API key") || !strings.Contains(view, "API Key") || !strings.Contains(view, "Base URL") {
		t.Fatalf("→ did not switch back to API key with its fields:\n%s", view)
	}

	m, _ = step(t, m, keyLeft)
	if view := m.View(); !strings.Contains(view, "(•) ChatGPT subscription") {
		t.Fatalf("← did not switch to ChatGPT subscription:\n%s", view)
	}
}

func TestSetupOpenAITabCyclesAuthKeyURL(t *testing.T) {
	m := openAIPage(t, nil, noLogin(t), testAuthPath(t))

	m, _ = step(t, m, keyTab)
	if !m.providers[0].apiKeyInput.Focused() {
		t.Fatal("tab from the Auth row did not focus API Key")
	}
	m, _ = step(t, m, keyTab)
	if !m.providers[0].baseURLInput.Focused() {
		t.Fatal("tab from API Key did not focus Base URL")
	}
	m, _ = step(t, m, keyTab)
	if m.providers[0].apiKeyInput.Focused() || m.providers[0].baseURLInput.Focused() {
		t.Fatal("tab from Base URL left a text field focused; it must return to the Auth row")
	}

	// Back on the Auth row, space switches again. With ChatGPT chosen there
	// is no field to tab to, so tab keeps the Auth row and space still works.
	m, _ = step(t, m, keySpace)
	m, _ = step(t, m, keyTab)
	m, _ = step(t, m, keySpace)
	if view := m.View(); !strings.Contains(view, "(•) API key") {
		t.Fatalf("after tab on ChatGPT subscription, space did not switch back to API key:\n%s", view)
	}
}

// TestSetupSavedSubscriptionCountsOpenAIConfigured: with only the switch
// saved and no OPENAI_API_KEY, the provider list shows OpenAI as configured
// and checked. The API Key field still doesn't claim a saved key.
func TestSetupSavedSubscriptionCountsOpenAIConfigured(t *testing.T) {
	m := newSetupModel(map[string]string{chatgptauth.EnvVar: "chatgpt"}).withChatGPTSignin(noLogin(t), testAuthPath(t))

	if !m.providers[0].enabled {
		t.Fatal("OpenAI not checked although the saved switch bills it to a ChatGPT subscription")
	}
	if view := m.View(); !strings.Contains(view, "OpenAI (configured)") {
		t.Fatalf("provider list doesn't show OpenAI as configured:\n%s", view)
	}

	m, _ = step(t, m, keyEnter)
	m, _ = step(t, m, keySpace) // API key
	if view := m.View(); strings.Contains(view, "configured") {
		t.Fatalf("API Key field claims a saved key, but none is saved:\n%s", view)
	}
}

// ── Values setup saves ──────────────────────────────────────────────────────

func TestSetupSwitchBackToAPIKeyWritesAPIKey(t *testing.T) {
	tests := []struct {
		name    string
		saved   string // TRACKER_OPENAI_AUTH in the config .env; "" leaves it out
		want    string
		wantSet bool
	}{
		{name: "saved chatgpt", saved: "chatgpt", want: "api-key", wantSet: true},
		// An invalid value would fail every openai call; picking API key fixes it.
		{name: "saved invalid", saved: "ChatGPT", want: "api-key", wantSet: true},
		{name: "unset", saved: "", wantSet: false},
		{name: "saved api-key", saved: "api-key", wantSet: false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			existing := map[string]string{}
			if tt.saved != "" {
				existing[chatgptauth.EnvVar] = tt.saved
			}
			m := openAIPage(t, existing, noLogin(t), testAuthPath(t))
			if strings.Contains(m.View(), "(•) ChatGPT subscription") {
				m, _ = step(t, m, keySpace) // pick API key
			}

			values := finish(t, m)
			got, set := values[chatgptauth.EnvVar]
			if set != tt.wantSet || got != tt.want {
				t.Fatalf("%s = %q (set %v), want %q (set %v); values %v", chatgptauth.EnvVar, got, set, tt.want, tt.wantSet, values)
			}
		})
	}
}

// TestSetupUncheckedOpenAILeavesSwitchAlone: unchecking OpenAI means "don't
// configure this provider", and setup never deletes a key, so the saved
// switch stays as it was.
func TestSetupUncheckedOpenAILeavesSwitchAlone(t *testing.T) {
	m := newSetupModel(map[string]string{chatgptauth.EnvVar: "chatgpt"}).withChatGPTSignin(noLogin(t), testAuthPath(t))
	m, _ = step(t, m, keySpace) // uncheck OpenAI

	values := finish(t, m)
	if got, set := values[chatgptauth.EnvVar]; set {
		t.Fatalf("%s = %q with OpenAI unchecked; want no entry", chatgptauth.EnvVar, got)
	}
}

// TestSetupUIModelUsesTheDefaultSignIn pins runSetupUI's wiring: the real
// auth client, the token file at its XDG default path, and that file's
// status on the OpenAI page.
func TestSetupUIModelUsesTheDefaultSignIn(t *testing.T) {
	configHome := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", configHome)
	authPath := filepath.Join(configHome, "tracker", chatgptauth.FileName)
	if err := chatgptauth.Save(authPath, signInTokens(t, "pro", time.Now())); err != nil {
		t.Fatalf("save the existing sign-in: %v", err)
	}

	m, err := newSetupUIModel(map[string]string{chatgptauth.EnvVar: "chatgpt"})
	if err != nil {
		t.Fatalf("newSetupUIModel: %v", err)
	}
	if _, ok := m.login.(*chatgptauth.AuthClient); !ok {
		t.Fatalf("login = %T, want *chatgptauth.AuthClient", m.login)
	}
	if m.authPath != authPath {
		t.Fatalf("authPath = %q, want %q", m.authPath, authPath)
	}
	m, _ = step(t, m, keyEnter)
	if view := m.View(); !strings.Contains(view, "pro plan · refreshed") {
		t.Fatalf("OpenAI page doesn't show the saved sign-in:\n%s", view)
	}
}

func TestRunSetupCommandWritesSwitch(t *testing.T) {
	configHome := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", configHome)

	err := runSetupCommand(func(map[string]string) (setupResult, error) {
		return setupResult{values: map[string]string{chatgptauth.EnvVar: "chatgpt"}}, nil
	})
	if err != nil {
		t.Fatalf("runSetupCommand: %v", err)
	}

	values, err := readEnvFile(filepath.Join(configHome, "tracker", ".env"))
	if err != nil {
		t.Fatalf("read the config .env: %v", err)
	}
	if got := values[chatgptauth.EnvVar]; got != "chatgpt" {
		t.Fatalf("config .env %s = %q, want %q", chatgptauth.EnvVar, got, "chatgpt")
	}
}

// ── Signing in ──────────────────────────────────────────────────────────────

func TestSetupChatGPTSigninHappyPath(t *testing.T) {
	authPath := testAuthPath(t)
	login := fakeLoginer{
		start: issueCode,
		wait: func(_ context.Context, dc chatgptauth.DeviceCode) (chatgptauth.Tokens, error) {
			if dc.UserCode != testUserCode {
				return chatgptauth.Tokens{}, fmt.Errorf("polled for code %q, want %q", dc.UserCode, testUserCode)
			}
			return signInTokens(t, "plus", time.Now()), nil
		},
	}
	m := openAIPage(t, nil, login, authPath)
	// Inputs typed before switching to the subscription must not be saved.
	m.providers[0].apiKeyInput.SetValue("sk-typed")
	m.providers[0].baseURLInput.SetValue("https://proxy.example.com")

	m, _ = step(t, m, keySpace) // ChatGPT subscription
	if view := m.View(); !strings.Contains(view, "not signed in") || !strings.Contains(view, "enter sign in") {
		t.Fatalf("ChatGPT subscription without a sign-in should say so and offer enter to sign in:\n%s", view)
	}

	m, cmd := step(t, m, keyEnter)
	m, waitCmd := deliverCode(t, m, cmd)
	view := m.View()
	for _, want := range []string{
		"Sign in with ChatGPT",
		"1. On any device, open:",
		testVerificationURL,
		"2. Enter this code (expires in 15 min):",
		testUserCode,
		"waiting for you to finish…",
		"Signs in through Codex's OAuth client — for your own ChatGPT account on your own machine.",
		"Continue only if you started this sign-in in tracker.",
		"esc cancel sign-in",
	} {
		if !strings.Contains(view, want) {
			t.Errorf("sign-in page lacks %q", want)
		}
	}
	if t.Failed() {
		t.Fatalf("sign-in page:\n%s", view)
	}

	m, _ = step(t, m, msgOf[chatgptDoneMsg](t, runCmd(waitCmd)))
	info, err := os.Stat(authPath)
	if err != nil {
		t.Fatalf("token file after sign-in: %v", err)
	}
	if perm := info.Mode().Perm(); runtime.GOOS != "windows" && perm != 0o600 {
		t.Errorf("token file mode = %04o, want 0600", perm)
	}
	if view := m.View(); !strings.Contains(view, "✓ signed in · plus plan") {
		t.Fatalf("OpenAI page after sign-in should show the plan:\n%s", view)
	}

	values := finish(t, m) // signed in: enter continues
	if got := values[chatgptauth.EnvVar]; got != "chatgpt" {
		t.Errorf("%s = %q, want %q", chatgptauth.EnvVar, got, "chatgpt")
	}
	for _, key := range []string{"OPENAI_API_KEY", "OPENAI_BASE_URL"} {
		if v, ok := values[key]; ok {
			t.Errorf("%s = %q saved although ChatGPT subscription is chosen", key, v)
		}
	}
}

// TestSetupChatGPTSigninEscCancelsPolling is Review Focus 4: esc on the
// sign-in page stops the poll, writes no token file, and returns to the
// OpenAI page with the wizard still running.
func TestSetupChatGPTSigninEscCancelsPolling(t *testing.T) {
	authPath := testAuthPath(t)
	w := testutil.NewWitness("wait-entered", "wait-saw-cancel")
	entered, sawCancel := make(chan struct{}), make(chan struct{})
	login := fakeLoginer{
		start: issueCode,
		wait: func(ctx context.Context, _ chatgptauth.DeviceCode) (chatgptauth.Tokens, error) {
			w.Hit("wait-entered")
			close(entered)
			<-ctx.Done()
			w.Hit("wait-saw-cancel")
			close(sawCancel)
			return chatgptauth.Tokens{}, ctx.Err()
		},
	}
	m := openAIPage(t, nil, login, authPath)
	m, _ = step(t, m, keySpace)
	m, cmd := step(t, m, keyEnter)
	m, waitCmd := deliverCode(t, m, cmd)

	polled := make(chan []tea.Msg, 1)
	go func() { polled <- runCmd(waitCmd) }()
	select {
	case <-entered:
	case <-time.After(time.Second):
		t.Fatalf("the poll never started within 1s — counters: %s", w)
	}

	m, cmd = step(t, m, keyEsc)
	select {
	case <-sawCancel:
	case <-time.After(time.Second):
		t.Fatalf("esc did not cancel the poll's context within 1s — counters: %s", w)
	}
	w.Require(t, 1, "wait-entered", "wait-saw-cancel")

	if m.cancelled {
		t.Fatal("esc on the sign-in page cancelled the whole wizard")
	}
	for _, msg := range runCmd(cmd) {
		if _, quit := msg.(setupCancelMsg); quit {
			t.Fatal("esc on the sign-in page sent setupCancelMsg")
		}
	}
	if view := m.View(); !strings.Contains(view, "Configure OpenAI") || strings.Contains(view, "Sign in with ChatGPT") {
		t.Fatalf("esc did not return to the OpenAI page:\n%s", view)
	}

	// The cancelled poll reports back; the wizard drops it and writes nothing.
	var msgs []tea.Msg
	select {
	case msgs = <-polled:
	case <-time.After(time.Second):
		t.Fatalf("the cancelled poll's command never returned — counters: %s", w)
	}
	m, _ = step(t, m, msgOf[chatgptDoneMsg](t, msgs))
	if _, err := os.Stat(authPath); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("token file after esc: stat err = %v, want not-exist", err)
	}
	if view := m.View(); !strings.Contains(view, "not signed in") || strings.Contains(view, "sign-in failed") {
		t.Fatalf("after esc the OpenAI page should read not signed in, with no failure:\n%s", view)
	}
}

func TestSetupCtrlCDuringSigninCancelsWizardAndPoll(t *testing.T) {
	w := testutil.NewWitness("wait-entered", "wait-saw-cancel")
	entered, sawCancel := make(chan struct{}), make(chan struct{})
	login := fakeLoginer{
		start: issueCode,
		wait: func(ctx context.Context, _ chatgptauth.DeviceCode) (chatgptauth.Tokens, error) {
			w.Hit("wait-entered")
			close(entered)
			<-ctx.Done()
			w.Hit("wait-saw-cancel")
			close(sawCancel)
			return chatgptauth.Tokens{}, ctx.Err()
		},
	}
	m := openAIPage(t, nil, login, testAuthPath(t))
	m, _ = step(t, m, keySpace)
	m, cmd := step(t, m, keyEnter)
	m, waitCmd := deliverCode(t, m, cmd)
	go runCmd(waitCmd)
	select {
	case <-entered:
	case <-time.After(time.Second):
		t.Fatalf("the poll never started within 1s — counters: %s", w)
	}

	m, cmd = step(t, m, keyCtrlC)
	if !m.cancelled {
		t.Fatal("ctrl+c on the sign-in page didn't cancel the wizard")
	}
	msgOf[setupCancelMsg](t, runCmd(cmd))
	select {
	case <-sawCancel:
	case <-time.After(time.Second):
		t.Fatalf("ctrl+c did not cancel the poll's context within 1s — counters: %s", w)
	}
	w.Require(t, 1, "wait-entered", "wait-saw-cancel")
}

func TestSetupChatGPTSigninFailureShown(t *testing.T) {
	tests := []struct {
		name  string
		login fakeLoginer
		want  string
	}{
		{
			name: "code request",
			login: fakeLoginer{start: func(context.Context) (chatgptauth.DeviceCode, error) {
				return chatgptauth.DeviceCode{}, chatgptauth.ErrDeviceCodeDisabled
			}},
			want: "sign-in failed: device-code sign-in is not enabled for this account (OpenAI returned 404)",
		},
		{
			name: "poll",
			login: fakeLoginer{start: issueCode, wait: func(context.Context, chatgptauth.DeviceCode) (chatgptauth.Tokens, error) {
				return chatgptauth.Tokens{}, chatgptauth.ErrDeviceCodeExpired
			}},
			want: "sign-in failed: the sign-in code expired after 15 minutes; start again",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			m := openAIPage(t, nil, tt.login, testAuthPath(t))
			m, _ = step(t, m, keySpace)
			m, cmd := step(t, m, keyEnter)
			m, waitCmd := deliverCode(t, m, cmd)
			if waitCmd != nil {
				m, _ = step(t, m, msgOf[chatgptDoneMsg](t, runCmd(waitCmd)))
			}

			view := m.View()
			if !strings.Contains(view, tt.want) {
				t.Fatalf("want %q on the OpenAI page:\n%s", tt.want, view)
			}
			if !strings.Contains(view, "Configure OpenAI") {
				t.Fatalf("a failed sign-in should return to the OpenAI page:\n%s", view)
			}
			// Still not signed in, so the page can't be left: enter tries again.
			_, cmd = step(t, m, keyEnter)
			msgOf[chatgptCodeMsg](t, runCmd(cmd))
		})
	}
}

func TestSetupSignedInEnterContinues(t *testing.T) {
	m := openAIPage(t, map[string]string{chatgptauth.EnvVar: "chatgpt"}, noLogin(t), savedSignIn(t))

	view := m.View()
	for _, want := range []string{"plus plan · refreshed 120m", "enter continue", "s sign in again"} {
		if !strings.Contains(view, want) {
			t.Fatalf("signed-in OpenAI page lacks %q:\n%s", want, view)
		}
	}

	if got := finish(t, m)[chatgptauth.EnvVar]; got != "chatgpt" {
		t.Fatalf("%s = %q, want %q", chatgptauth.EnvVar, got, "chatgpt")
	}
}

func TestSetupSignedInSResigns(t *testing.T) {
	starts := 0
	login := fakeLoginer{start: func(ctx context.Context) (chatgptauth.DeviceCode, error) {
		starts++
		return issueCode(ctx)
	}}
	m := openAIPage(t, map[string]string{chatgptauth.EnvVar: "chatgpt"}, login, savedSignIn(t))

	m, cmd := step(t, m, keyS)
	m, _ = deliverCode(t, m, cmd)
	if starts != 1 {
		t.Fatalf("StartDeviceLogin called %d times after s, want 1", starts)
	}
	if view := m.View(); !strings.Contains(view, testUserCode) {
		t.Fatalf("s did not show a new sign-in code:\n%s", view)
	}
}

func TestSetupShowsUnreadableSignInFile(t *testing.T) {
	authPath := testAuthPath(t)
	if err := os.MkdirAll(filepath.Dir(authPath), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(authPath, []byte("{not json"), 0o600); err != nil {
		t.Fatal(err)
	}
	m := openAIPage(t, map[string]string{chatgptauth.EnvVar: "chatgpt"}, fakeLoginer{start: issueCode}, authPath)

	if view := m.View(); !strings.Contains(view, "sign-in file unreadable: parse token file") {
		t.Fatalf("OpenAI page should report the unreadable sign-in file:\n%s", view)
	}
	// An unreadable file is no sign-in: enter signs in again, replacing it.
	_, cmd := step(t, m, keyEnter)
	msgOf[chatgptCodeMsg](t, runCmd(cmd))
}

// TestSetupChatGPTIgnoresStaleSigninMessages: the user presses esc while
// the first code request is in flight, then starts again. The first
// request's late answer must not take over the second sign-in.
func TestSetupChatGPTIgnoresStaleSigninMessages(t *testing.T) {
	codes := []string{"AAAA-1111", "BBBB-2222"}
	starts := 0
	login := fakeLoginer{start: func(context.Context) (chatgptauth.DeviceCode, error) {
		code := codes[starts]
		starts++
		return chatgptauth.DeviceCode{VerificationURL: testVerificationURL, UserCode: code}, nil
	}}
	m := openAIPage(t, nil, login, testAuthPath(t))
	m, _ = step(t, m, keySpace)
	m, firstCmd := step(t, m, keyEnter)
	m, _ = step(t, m, keyEsc)
	m, secondCmd := step(t, m, keyEnter)

	m, cmd := step(t, m, msgOf[chatgptCodeMsg](t, runCmd(firstCmd)))
	if cmd != nil {
		t.Fatal("the cancelled sign-in's late code started a poll")
	}
	if view := m.View(); strings.Contains(view, "AAAA-1111") {
		t.Fatalf("the cancelled sign-in's code took over the new sign-in:\n%s", view)
	}

	m, _ = deliverCode(t, m, secondCmd)
	if view := m.View(); !strings.Contains(view, "BBBB-2222") {
		t.Fatalf("the new sign-in's code isn't shown:\n%s", view)
	}
}

// TestSetupSigninPagesFitTheTerminal: the sign-in notice and a network
// error are wider than an 80-column terminal, where Bubble Tea cuts every
// line off at the edge. The pages wrap them instead, so nothing is lost.
func TestSetupSigninPagesFitTheTerminal(t *testing.T) {
	const width = 80
	longErr := errors.New(`request device code: Post "https://auth.openai.com/api/accounts/deviceauth/usercode": dial tcp: lookup auth.openai.com: no such host`)
	tests := []struct {
		name  string
		start func(context.Context) (chatgptauth.DeviceCode, error)
		want  []string // the last words of the long lines, which a cut-off would lose
	}{
		{
			name:  "sign-in page",
			start: issueCode,
			want:  []string{"machine.", "Continue only if you started this sign-in in tracker."},
		},
		{
			name:  "failure on the OpenAI page",
			start: func(context.Context) (chatgptauth.DeviceCode, error) { return chatgptauth.DeviceCode{}, longErr },
			want:  []string{"host"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			m := newSetupModel(nil).withChatGPTSignin(fakeLoginer{start: tt.start}, testAuthPath(t))
			m, _ = step(t, m, tea.WindowSizeMsg{Width: width, Height: 40})
			m.providers[0].enabled = true
			m, _ = step(t, m, keyEnter)
			m, _ = step(t, m, keySpace)
			m, cmd := step(t, m, keyEnter)
			m, _ = deliverCode(t, m, cmd)

			view := m.View()
			if got := lipgloss.Width(view); got > width {
				t.Errorf("page is %d columns wide on a %d-column terminal:\n%s", got, width, view)
			}
			for _, want := range tt.want {
				if !strings.Contains(view, want) {
					t.Errorf("page lacks %q:\n%s", want, view)
				}
			}
		})
	}
}

// TestSetupSigninSpinnerStopsWhenSigninEnds: the spinner turns while a
// sign-in runs and stops scheduling frames once it ends, so the wizard
// doesn't wake ten times a second for nothing.
func TestSetupSigninSpinnerStopsWhenSigninEnds(t *testing.T) {
	m := openAIPage(t, nil, fakeLoginer{start: issueCode}, testAuthPath(t))
	m, _ = step(t, m, keySpace)
	m, cmd := step(t, m, keyEnter)
	tick := msgOf[spinner.TickMsg](t, runCmd(cmd))

	if _, next := step(t, m, tick); next == nil {
		t.Fatal("the spinner scheduled no next frame while signing in")
	}
	m, _ = step(t, m, keyEsc)
	if _, next := step(t, m, tick); next != nil {
		t.Fatal("the spinner kept scheduling frames after the sign-in ended")
	}
}

// TestFinishChatGPTSigninWritesNothingAfterCancel: the user approves the
// code on a phone, but presses esc before the poll reports it. The sign-in
// still counts as cancelled, so no token file appears.
func TestFinishChatGPTSigninWritesNothingAfterCancel(t *testing.T) {
	authPath := testAuthPath(t)
	ctx, cancel := context.WithCancel(context.Background())
	login := fakeLoginer{wait: func(context.Context, chatgptauth.DeviceCode) (chatgptauth.Tokens, error) {
		cancel() // esc lands while the approved poll returns
		return signInTokens(t, "plus", time.Now()), nil
	}}

	if _, err := finishChatGPTSignin(ctx, login, chatgptauth.DeviceCode{}, authPath); !errors.Is(err, context.Canceled) {
		t.Fatalf("finishChatGPTSignin err = %v, want context.Canceled", err)
	}
	if _, err := os.Stat(authPath); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("token file after a cancelled sign-in: stat err = %v, want not-exist", err)
	}
}

// ── Status text ─────────────────────────────────────────────────────────────

func TestSignedInSummary(t *testing.T) {
	now := time.Date(2026, 10, 16, 12, 0, 0, 0, time.UTC)
	twoHoursAgo := now.Add(-2 * time.Hour)
	tests := []struct {
		name string
		s    chatgptauth.Status
		want string
	}{
		{"plan and refresh", chatgptauth.Status{SignedIn: true, PlanType: "plus", LastRefresh: twoHoursAgo}, "plus plan · refreshed 120m00s ago"},
		{"no plan", chatgptauth.Status{SignedIn: true, LastRefresh: twoHoursAgo}, "refreshed 120m00s ago"},
		{"no refresh time", chatgptauth.Status{SignedIn: true, PlanType: "plus"}, "plus plan"},
		{"neither", chatgptauth.Status{SignedIn: true}, "signed in"},
		{"refresh in the future (clock skew)", chatgptauth.Status{SignedIn: true, PlanType: "pro", LastRefresh: now.Add(time.Minute)}, "pro plan · refreshed 0ms ago"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := signedInSummary(tt.s, now); got != tt.want {
				t.Errorf("signedInSummary() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestJustSignedIn(t *testing.T) {
	if got, want := justSignedIn("plus"), "✓ signed in · plus plan"; got != want {
		t.Errorf("justSignedIn(plus) = %q, want %q", got, want)
	}
	if got, want := justSignedIn(""), "✓ signed in"; got != want {
		t.Errorf("justSignedIn(\"\") = %q, want %q", got, want)
	}
}
