// ABOUTME: Views for the setup wizard's OpenAI page: the Auth row, the ChatGPT sign-in status
// ABOUTME: and key help, and the device-code sign-in page with its link, code and notice.
package main

import (
	"fmt"
	"strings"
	"time"

	"github.com/charmbracelet/lipgloss"

	"github.com/2389-research/tracker/internal/chatgptauth"
)

var (
	signedInStyle    = lipgloss.NewStyle().Foreground(colorNeon)
	signinErrorStyle = lipgloss.NewStyle().Foreground(colorHot)
)

// viewOpenAIConfig is the OpenAI page: the Auth row, then the API key
// fields or the ChatGPT sign-in status. While a sign-in runs, the sign-in
// page takes its place.
func (m setupModel) viewOpenAIConfig() string {
	if m.signinPhase != signinIdle {
		return m.viewChatGPTSignin()
	}
	p := m.providers[m.enabledOrder[m.providerIdx]]

	var b strings.Builder
	b.WriteString(m.renderConfigTitle(p))
	b.WriteString(m.renderAuthRow())
	if m.openaiAuth == chatgptauth.ModeChatGPT {
		b.WriteString(m.renderChatGPTStatus())
	} else {
		b.WriteString(m.renderKeyFields(p))
	}
	b.WriteString(mutedStyle.Render(m.openAIHelp()))
	b.WriteString("\n")

	return m.fitToTerminal(b.String())
}

// fitToTerminal draws content in the provider box. Bubble Tea cuts off any
// line wider than the terminal, so a box too wide for it is drawn again at
// the terminal's width, where lipgloss wraps the long lines (the sign-in
// notice, an error) instead of losing their ends.
func (m setupModel) fitToTerminal(content string) string {
	box := providerBorder.Render(content)
	if m.width > 0 && lipgloss.Width(box) > m.width {
		box = providerBorder.Width(m.width - providerBorder.GetHorizontalBorderSize()).Render(content)
	}
	return box
}

// renderAuthRow renders "Auth  (•) ChatGPT subscription  ( ) API key", with
// the cursor when the row has focus.
func (m setupModel) renderAuthRow() string {
	cursor := "  "
	if m.fieldFocus == focusAuthRow {
		cursor = selectedStyle.Render("▸ ")
	}
	chatgpt, apiKey := "( )", "(•)"
	if m.openaiAuth == chatgptauth.ModeChatGPT {
		chatgpt, apiKey = "(•)", "( )"
	}
	return fmt.Sprintf("  %s%s  %s ChatGPT subscription  %s API key\n\n",
		cursor, labelStyle.Render("  Auth"), checkboxStyle.Render(chatgpt), checkboxStyle.Render(apiKey))
}

// renderChatGPTStatus renders the sign-in status under the Auth row, and
// the last sign-in's failure, if any.
func (m setupModel) renderChatGPTStatus() string {
	var b strings.Builder
	fmt.Fprintf(&b, "    %s\n", m.chatgptStatusLine())
	if m.signinErr != nil {
		fmt.Fprintf(&b, "    %s\n", signinErrorStyle.Render("sign-in failed: "+m.signinErr.Error()))
	}
	b.WriteByte('\n')
	return b.String()
}

// chatgptStatusLine says whether the token file holds a sign-in.
func (m setupModel) chatgptStatusLine() string {
	switch {
	case m.signedInNow:
		return signedInStyle.Render(justSignedIn(m.chatgptStatus.PlanType))
	case m.chatgptStatusErr != nil:
		return signinErrorStyle.Render("sign-in file unreadable: " + m.chatgptStatusErr.Error())
	case m.chatgptStatus.SignedIn:
		return configuredHint.Render(signedInSummary(m.chatgptStatus, time.Now()))
	default:
		return mutedStyle.Render("not signed in")
	}
}

// justSignedIn is the status after a sign-in in this run of setup:
// "✓ signed in · plus plan", without the plan when the token has none.
func justSignedIn(plan string) string {
	if plan == "" {
		return "✓ signed in"
	}
	return "✓ signed in · " + plan + " plan"
}

// signedInSummary is the status of an earlier sign-in, such as
// "plus plan · refreshed 2h ago", leaving out what the file lacks.
func signedInSummary(s chatgptauth.Status, now time.Time) string {
	var parts []string
	if s.PlanType != "" {
		parts = append(parts, s.PlanType+" plan")
	}
	if !s.LastRefresh.IsZero() {
		parts = append(parts, "refreshed "+refreshAge(now.Sub(s.LastRefresh)))
	}
	if len(parts) == 0 {
		return "signed in"
	}
	return strings.Join(parts, " · ")
}

// refreshAge says how long ago a refresh was, in whole units rounded down:
// "just now" under a minute (or for a refresh time in the future, from clock
// skew), then "<N>m ago", "<N>h ago" and "<N>d ago".
func refreshAge(d time.Duration) string {
	const day = 24 * time.Hour
	switch {
	case d < time.Minute:
		return "just now"
	case d < time.Hour:
		return fmt.Sprintf("%dm ago", int64(d/time.Minute))
	case d < day:
		return fmt.Sprintf("%dh ago", int64(d/time.Hour))
	default:
		return fmt.Sprintf("%dd ago", int64(d/day))
	}
}

// openAIHelp is the OpenAI page's key help. It follows the Auth row's
// choice, the sign-in status, and the focus.
func (m setupModel) openAIHelp() string {
	switch {
	case m.openaiAuth == chatgptauth.ModeChatGPT && m.chatgptStatus.SignedIn:
		return "  space switch  enter continue  s sign in again  esc cancel"
	case m.openaiAuth == chatgptauth.ModeChatGPT:
		return "  space switch  enter sign in  esc cancel"
	case m.fieldFocus == focusAuthRow:
		return "  space switch  tab switch field  enter next  esc cancel"
	default:
		return configHelp
	}
}

// viewChatGPTSignin is the sign-in page: the link and code to enter on any
// device while the poll waits in the background.
func (m setupModel) viewChatGPTSignin() string {
	var b strings.Builder
	b.WriteString(renderHeader())
	b.WriteByte('\n')
	b.WriteString(subtitleStyle.Render("  Sign in with ChatGPT"))
	b.WriteString("\n\n")
	if m.signinPhase == signinRequesting {
		fmt.Fprintf(&b, "  %s requesting a sign-in code…\n\n", m.signinSpinner.View())
	} else {
		b.WriteString(m.renderDeviceCode())
	}
	b.WriteString(mutedStyle.Render("  esc cancel sign-in"))
	b.WriteString("\n")

	return m.fitToTerminal(b.String())
}

// renderDeviceCode renders the two sign-in steps, the spinner, and the
// notice that this signs in through Codex's OAuth client.
func (m setupModel) renderDeviceCode() string {
	var b strings.Builder
	fmt.Fprintf(&b, "  1. On any device, open:\n     %s\n", selectedStyle.Render(m.signinCode.VerificationURL))
	fmt.Fprintf(&b, "  2. Enter this code (expires in 15 min):\n     %s\n\n", selectedStyle.Render(m.signinCode.UserCode))
	fmt.Fprintf(&b, "  %s waiting for you to finish…\n\n", m.signinSpinner.View())
	b.WriteString(mutedStyle.Render("  Signs in through Codex's OAuth client — for your own ChatGPT account on your own machine."))
	b.WriteString("\n")
	b.WriteString(mutedStyle.Render("  Continue only if you started this sign-in in tracker."))
	b.WriteString("\n\n")
	return b.String()
}
