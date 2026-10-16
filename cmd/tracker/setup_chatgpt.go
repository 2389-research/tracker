// ABOUTME: Setup wizard's OpenAI Auth row (API key or ChatGPT subscription) and device-code sign-in:
// ABOUTME: state, key handling and the background sign-in commands (views: setup_chatgpt_view.go).
package main

import (
	"context"

	"github.com/charmbracelet/bubbles/spinner"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/2389-research/tracker/internal/chatgptauth"
)

// openAIKeyEnv is OpenAI's API key variable. It also marks OpenAI's
// provider entry, the one config page with an Auth row.
const openAIKeyEnv = "OPENAI_API_KEY"

// focusAuthRow is fieldFocus on the OpenAI page's Auth row; 0 and 1 are the
// API key and base URL fields.
const focusAuthRow = 2

// chatgptLoginer runs a device-code sign-in. *chatgptauth.AuthClient is the
// real one; tests pass a fake.
type chatgptLoginer interface {
	StartDeviceLogin(ctx context.Context) (chatgptauth.DeviceCode, error)
	WaitForDeviceLogin(ctx context.Context, dc chatgptauth.DeviceCode) (chatgptauth.Tokens, error)
}

// chatgptCodeMsg carries StartDeviceLogin's answer. seq names the sign-in
// that asked, so a cancelled sign-in's late answer is dropped.
type chatgptCodeMsg struct {
	seq  int
	code chatgptauth.DeviceCode
	err  error
}

// chatgptDoneMsg carries a finished sign-in: the status of the saved token
// file, or why the sign-in failed. seq is as in chatgptCodeMsg.
type chatgptDoneMsg struct {
	seq    int
	status chatgptauth.Status
	err    error
}

// signinPhase is where a ChatGPT sign-in stands. In any phase but
// signinIdle, the sign-in page replaces the OpenAI page.
type signinPhase int

const (
	signinIdle       signinPhase = iota
	signinRequesting             // waiting for the device code
	signinWaiting                // code shown; polling until the user finishes
)

// chatgptSetup is the wizard's state for OpenAI's Auth row and the ChatGPT
// sign-in. setupModel embeds it, so its fields read as the model's own.
type chatgptSetup struct {
	// openaiAuth is the Auth row's choice. savedAuth is TRACKER_OPENAI_AUTH
	// as the config .env had it: ModeAPIKey when unset, "" when ParseMode
	// rejects the value.
	openaiAuth chatgptauth.Mode
	savedAuth  chatgptauth.Mode

	// login signs in, and authPath is the token file it saves to.
	// chatgptStatus summarizes that file; chatgptStatusErr says why it
	// couldn't be read.
	login            chatgptLoginer
	authPath         string
	chatgptStatus    chatgptauth.Status
	chatgptStatusErr error

	// The current or last sign-in. signinCtx is the running sign-in's
	// context, and signinCancel ends it: esc, ctrl+c, or the sign-in's end.
	signinPhase   signinPhase
	signinSeq     int
	signinCtx     context.Context
	signinCancel  context.CancelFunc
	signinCode    chatgptauth.DeviceCode
	signinErr     error // why the last sign-in failed
	signedInNow   bool  // the last sign-in succeeded in this run of setup
	signinSpinner spinner.Model
}

// newChatGPTSetup starts the Auth row on ChatGPT subscription when the
// saved switch says chatgpt, and on API key otherwise.
func newChatGPTSetup(existing map[string]string) chatgptSetup {
	// ParseMode's error is dropped on purpose: an invalid saved value
	// leaves savedAuth "", which starts the row on API key, and setup then
	// replaces the value with whichever choice OpenAI's page is left on.
	saved, _ := chatgptauth.ParseMode(existing[chatgptauth.EnvVar])
	auth := chatgptauth.ModeAPIKey
	if saved == chatgptauth.ModeChatGPT {
		auth = chatgptauth.ModeChatGPT
	}
	return chatgptSetup{
		openaiAuth: auth,
		savedAuth:  saved,
		signinSpinner: spinner.New(
			spinner.WithSpinner(spinner.MiniDot),
			spinner.WithStyle(lipgloss.NewStyle().Foreground(colorElec)),
		),
	}
}

// newOpenAIProviderEntry counts a saved TRACKER_OPENAI_AUTH=chatgpt as
// configured, as newGeminiProviderEntry counts the legacy key, because a
// subscription-only setup has no OPENAI_API_KEY. It sets
// subscriptionConfigured, not keyConfigured, so the API Key field never
// claims a saved key that isn't there.
func newOpenAIProviderEntry(existing map[string]string) providerEntry {
	p := newProviderEntry("OpenAI", openAIKeyEnv, "OPENAI_BASE_URL", existing)
	if saved, _ := chatgptauth.ParseMode(existing[chatgptauth.EnvVar]); saved == chatgptauth.ModeChatGPT {
		p.subscriptionConfigured = true
		p.enabled = true
	}
	return p
}

// withChatGPTSignin wires in the loginer and the token file the sign-in
// saves to, and reads that file's status for the OpenAI page.
func (m setupModel) withChatGPTSignin(login chatgptLoginer, authPath string) setupModel {
	m.login = login
	m.authPath = authPath
	m.chatgptStatus, m.chatgptStatusErr = chatgptauth.ReadStatus(authPath)
	return m
}

// onOpenAIPage reports whether the config page shows OpenAI.
func (m setupModel) onOpenAIPage() bool {
	if m.page != pageProviderConfig || len(m.enabledOrder) == 0 {
		return false
	}
	return m.providers[m.enabledOrder[m.providerIdx]].apiKeyEnv == openAIKeyEnv
}

// firstFieldFocus is where focus starts on a config page: the Auth row on
// OpenAI's, the API key field on every other.
func (m setupModel) firstFieldFocus() int {
	if m.onOpenAIPage() {
		return focusAuthRow
	}
	return 0
}

// billsToChatGPT reports whether p is OpenAI with ChatGPT subscription
// chosen. Its API key and base URL inputs are then not saved.
func (m setupModel) billsToChatGPT(p providerEntry) bool {
	return p.apiKeyEnv == openAIKeyEnv && m.openaiAuth == chatgptauth.ModeChatGPT
}

// collectOpenAIAuth adds TRACKER_OPENAI_AUTH to values when setup must write
// it: chatgpt for an enabled OpenAI on the subscription, and api-key for an
// enabled OpenAI on an API key while the saved value says anything else.
// Otherwise it adds nothing. API-key users get no new line, and an
// unchecked OpenAI keeps its saved switch: unchecked means "don't configure
// this provider", and setup never deletes keys.
func (m setupModel) collectOpenAIAuth(values map[string]string) {
	if !m.openAIEnabled() {
		return
	}
	switch {
	case m.openaiAuth == chatgptauth.ModeChatGPT:
		values[chatgptauth.EnvVar] = string(chatgptauth.ModeChatGPT)
	case m.savedAuth != chatgptauth.ModeAPIKey:
		values[chatgptauth.EnvVar] = string(chatgptauth.ModeAPIKey)
	}
}

// openAIEnabled reports whether OpenAI is checked on the provider list.
func (m setupModel) openAIEnabled() bool {
	for _, p := range m.providers {
		if p.apiKeyEnv == openAIKeyEnv {
			return p.enabled
		}
	}
	return false
}

// ── Keys ────────────────────────────────────────────────────────────────────

// updateOpenAIConfig handles a key on the OpenAI page. On the Auth row,
// space, ← and → switch the choice and tab moves to the API key field;
// with ChatGPT subscription chosen, enter signs in when not signed in and s
// signs in again.
func (m setupModel) updateOpenAIConfig(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	if m.fieldFocus != focusAuthRow {
		return m.updateOpenAIField(msg)
	}
	switch msg.Type {
	case tea.KeySpace, tea.KeyLeft, tea.KeyRight:
		m.openaiAuth = otherAuth(m.openaiAuth)
		return m, nil
	case tea.KeyTab, tea.KeyShiftTab:
		return m.leaveAuthRow()
	case tea.KeyEnter:
		return m.continueOpenAI()
	}
	if msg.String() == "s" && m.openaiAuth == chatgptauth.ModeChatGPT {
		return m.startChatGPTSignin()
	}
	return m, nil
}

// updateOpenAIField handles a key while the API key or base URL field has
// focus, as on any provider page, except that tab from the base URL field
// returns to the Auth row.
func (m setupModel) updateOpenAIField(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	isTab := msg.Type == tea.KeyTab || msg.Type == tea.KeyShiftTab
	if isTab && m.fieldFocus == 1 {
		m.fieldFocus = focusAuthRow
		m.focusConfigField()
		return m, nil
	}
	return m.updateProviderConfig(msg)
}

// otherAuth is the Auth row's other choice.
func otherAuth(a chatgptauth.Mode) chatgptauth.Mode {
	if a == chatgptauth.ModeChatGPT {
		return chatgptauth.ModeAPIKey
	}
	return chatgptauth.ModeChatGPT
}

// leaveAuthRow moves focus from the Auth row to the API key field. With
// ChatGPT subscription chosen there is no field, so focus stays put.
func (m setupModel) leaveAuthRow() (tea.Model, tea.Cmd) {
	if m.openaiAuth == chatgptauth.ModeAPIKey {
		m.fieldFocus = 0
		m.focusConfigField()
	}
	return m, nil
}

// continueOpenAI is enter on the Auth row. The page can't be left on
// ChatGPT subscription without a sign-in, so there enter signs in.
func (m setupModel) continueOpenAI() (tea.Model, tea.Cmd) {
	if m.openaiAuth == chatgptauth.ModeChatGPT && !m.chatgptStatus.SignedIn {
		return m.startChatGPTSignin()
	}
	return m.advanceProvider()
}

// updateSigninKey handles a key on the sign-in page: esc cancels the
// sign-in and returns to the OpenAI page, and the wizard keeps running.
func (m setupModel) updateSigninKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	if msg.Type == tea.KeyEsc {
		return m.endSignin(nil), nil
	}
	return m, nil
}

// ── Signing in ──────────────────────────────────────────────────────────────

// startChatGPTSignin opens the sign-in page and asks for a device code in
// the background, under a context that esc and ctrl+c cancel.
func (m setupModel) startChatGPTSignin() (tea.Model, tea.Cmd) {
	ctx, cancel := context.WithCancel(context.Background())
	m.signinSeq++
	m.signinPhase = signinRequesting
	m.signinCtx, m.signinCancel = ctx, cancel
	m.signinErr, m.signedInNow = nil, false

	login, seq := m.login, m.signinSeq
	requestCode := func() tea.Msg {
		code, err := login.StartDeviceLogin(ctx)
		return chatgptCodeMsg{seq: seq, code: code, err: err}
	}
	return m, tea.Batch(requestCode, m.signinSpinner.Tick)
}

// updateChatGPT handles the sign-in's background messages.
func (m setupModel) updateChatGPT(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case chatgptCodeMsg:
		return m.receiveCode(msg)
	case chatgptDoneMsg:
		return m.receiveDone(msg), nil
	case spinner.TickMsg:
		return m.tickSpinner(msg)
	}
	return m, nil
}

// receiveCode shows the device code and starts polling, or ends the
// sign-in on an error. A message from an earlier sign-in is dropped.
func (m setupModel) receiveCode(msg chatgptCodeMsg) (tea.Model, tea.Cmd) {
	if msg.seq != m.signinSeq || m.signinPhase != signinRequesting {
		return m, nil
	}
	if msg.err != nil {
		return m.endSignin(msg.err), nil
	}
	m.signinPhase = signinWaiting
	m.signinCode = msg.code

	ctx, login, authPath, seq := m.signinCtx, m.login, m.authPath, m.signinSeq
	return m, func() tea.Msg {
		status, err := finishChatGPTSignin(ctx, login, msg.code, authPath)
		return chatgptDoneMsg{seq: seq, status: status, err: err}
	}
}

// receiveDone ends the sign-in. The OpenAI page then shows the new plan, or
// the failure. A message from an earlier sign-in is dropped.
func (m setupModel) receiveDone(msg chatgptDoneMsg) setupModel {
	if msg.seq != m.signinSeq || m.signinPhase != signinWaiting {
		return m
	}
	m = m.endSignin(msg.err)
	if msg.err == nil {
		m.chatgptStatus, m.chatgptStatusErr, m.signedInNow = msg.status, nil, true
	}
	return m
}

// endSignin cancels the running sign-in's context, if any, returns to the
// OpenAI page, and records err: nil after a cancel or a success.
func (m setupModel) endSignin(err error) setupModel {
	if m.signinCancel != nil {
		m.signinCancel()
	}
	m.signinPhase, m.signinCtx, m.signinCancel = signinIdle, nil, nil
	m.signinErr = err
	return m
}

// tickSpinner turns the spinner while a sign-in runs. Once the sign-in
// ends, ticks are dropped, so the spinner stops scheduling frames.
func (m setupModel) tickSpinner(msg spinner.TickMsg) (tea.Model, tea.Cmd) {
	if m.signinPhase == signinIdle {
		return m, nil
	}
	var cmd tea.Cmd
	m.signinSpinner, cmd = m.signinSpinner.Update(msg)
	return m, cmd
}

// finishChatGPTSignin polls until the user finishes signing in, saves the
// tokens under the refresh lock, and reads back the file's status. It runs
// inside a tea.Cmd, so no file IO happens on the UI goroutine. A sign-in
// cancelled while the poll was returning saves nothing.
func finishChatGPTSignin(ctx context.Context, login chatgptLoginer, dc chatgptauth.DeviceCode, authPath string) (chatgptauth.Status, error) {
	tokens, err := login.WaitForDeviceLogin(ctx, dc)
	if err != nil {
		return chatgptauth.Status{}, err
	}
	if err := ctx.Err(); err != nil {
		return chatgptauth.Status{}, err
	}
	if err := chatgptauth.SaveSignIn(ctx, authPath, tokens); err != nil {
		return chatgptauth.Status{}, err
	}
	return chatgptauth.ReadStatus(authPath)
}
