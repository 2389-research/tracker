// ABOUTME: Doctor's and `tracker version`'s report of the ChatGPT subscription sign-in for the
// ABOUTME: openai provider slot: offline status, a live refresh probe, and the hints ChatGPT mode adds.
package tracker

import (
	"context"
	"errors"
	"fmt"
	"os"
	"time"

	"github.com/2389-research/tracker/internal/chatgptauth"
)

// doctorChatGPTIssuer is where evaluateChatGPT's refresh probe is sent —
// chatgptauth.Issuer in production. Tests point it at a fake auth server,
// the same seam tracker_client.go's chatgptBackendURL uses for the ChatGPT
// backend itself.
var doctorChatGPTIssuer = chatgptauth.Issuer

// chatgptSetupHint sends the user back through the sign-in flow. Every
// evaluateChatGPT failure that only a fresh sign-in fixes shares it.
const chatgptSetupHint = "run tracker setup → OpenAI → ChatGPT subscription"

// evaluateOpenAIChatGPTSlot decides whether p's OpenAI slot is governed by
// TRACKER_OPENAI_AUTH instead of evaluateProvider's ordinary key check:
// handled is false for every provider but OpenAI, and for OpenAI itself
// when the switch is api-key (today's behavior, unchanged) — the caller
// falls through to the key-based checks in both cases.
func evaluateOpenAIChatGPTSlot(ctx context.Context, p providerDef, probe bool) (CheckDetail, providerState, bool) {
	if p.name != "OpenAI" {
		return CheckDetail{}, stateMissing, false
	}
	mode, err := chatgptauth.ModeFromEnv()
	if err != nil {
		return invalidChatGPTSwitchDetail(), stateInvalid, true
	}
	if mode != chatgptauth.ModeChatGPT {
		return CheckDetail{}, stateMissing, false
	}
	detail, state := evaluateChatGPT(ctx, probe)
	return detail, state, true
}

// invalidChatGPTSwitchDetail reports an unparseable TRACKER_OPENAI_AUTH in
// evaluateProvider's OpenAI slot, in place of the ordinary key check.
func invalidChatGPTSwitchDetail() CheckDetail {
	return CheckDetail{
		Status:  CheckStatusError,
		Message: fmt.Sprintf("%-15s %s=%s is not chatgpt or api-key", "OpenAI", chatgptauth.EnvVar, os.Getenv(chatgptauth.EnvVar)),
		Hint:    fmt.Sprintf("set %s to %s or %s, or run tracker setup", chatgptauth.EnvVar, chatgptauth.ModeAPIKey, chatgptauth.ModeChatGPT),
	}
}

// evaluateChatGPT reports OpenAI's status under TRACKER_OPENAI_AUTH=chatgpt:
// the sign-in's presence and plan offline, and — when probe is set — a live
// refresh that proves the sign-in still works without a generation
// request, a model guess, or plan usage (design §3 "Doctor"). It never
// prints the account ID, email, or any token.
func evaluateChatGPT(ctx context.Context, probe bool) (CheckDetail, providerState) {
	path, err := chatgptauth.DefaultPath()
	if err != nil {
		return CheckDetail{
			Status:  CheckStatusError,
			Message: fmt.Sprintf("%-15s resolve the ChatGPT token path: %v", "OpenAI", err),
		}, stateInvalid
	}

	status, err := chatgptauth.ReadStatus(path)
	if err != nil {
		return chatgptErrorDetail(path, err)
	}
	if !status.SignedIn {
		return CheckDetail{
			Status:  CheckStatusError,
			Message: fmt.Sprintf("%-15s %s=chatgpt but not signed in", "OpenAI", chatgptauth.EnvVar),
			Hint:    chatgptSetupHint,
		}, stateInvalid
	}

	refreshed := false
	if probe {
		tokens, err := chatgptauth.Load(path)
		if err != nil {
			return chatgptErrorDetail(path, err)
		}
		if err := probeChatGPTRefresh(ctx, path, tokens.AccessToken); err != nil {
			return chatgptProbeErrorDetail(path, err)
		}
		refreshed = true
	}
	return CheckDetail{
		Status:  CheckStatusOK,
		Message: fmt.Sprintf("%-15s %s", "OpenAI", chatgptSignedInText(status.PlanType, refreshed)),
	}, stateConfigured
}

// chatgptSignedInText renders the signed-in message body, without the
// provider-name column: the plan when known, and ", refresh ok" after a
// good live probe.
func chatgptSignedInText(plan string, refreshed bool) string {
	text := "ChatGPT subscription"
	if plan != "" {
		text += fmt.Sprintf(" (%s plan)", plan)
	}
	text += ", signed in"
	if refreshed {
		text += ", refresh ok"
	}
	return text
}

// chatgptErrorDetail classifies a ReadStatus or Load failure, or a probe
// failure that isn't transient, into its detail line. An unsafe file names
// its chmod fix; every other failure — including a permanent refresh
// failure — sends the user back through `tracker setup`.
func chatgptErrorDetail(path string, err error) (CheckDetail, providerState) {
	detail := CheckDetail{
		Status:  CheckStatusError,
		Message: fmt.Sprintf("%-15s %s", "OpenAI", err.Error()),
		Hint:    chatgptSetupHint,
	}
	var unsafe *chatgptauth.UnsafeFileError
	if errors.As(err, &unsafe) {
		detail.Hint = fmt.Sprintf("chmod 600 %s", path)
	}
	return detail, stateInvalid
}

// chatgptProbeErrorDetail classifies a failed refresh probe the way
// probeProviderDetail splits a key probe's auth failures from its network
// ones: a failure that says nothing about the sign-in (a network error, a
// timeout, a 5xx) points at connectivity, because signing in again won't
// fix it. Anything else goes through chatgptErrorDetail.
func chatgptProbeErrorDetail(path string, err error) (CheckDetail, providerState) {
	if !probeFailedTransiently(err) {
		return chatgptErrorDetail(path, err)
	}
	return CheckDetail{
		Status:  CheckStatusError,
		Message: fmt.Sprintf("%-15s %s", "OpenAI", err.Error()),
		Hint:    "the ChatGPT refresh probe failed on network/transport — verify connectivity to auth.openai.com before signing in again",
	}, stateInvalid
}

// probeFailedTransiently reports whether a refresh probe failure may pass
// on a retry: a refresh error that isn't permanent, or the probe's context
// ending while it waited for the refresh lock.
func probeFailedTransiently(err error) bool {
	var rerr *chatgptauth.RefreshError
	if errors.As(err, &rerr) {
		return rerr.Temporary()
	}
	return errors.Is(err, context.DeadlineExceeded) || errors.Is(err, context.Canceled)
}

// probeChatGPTRefresh spends a ChatGPT refresh once to prove the sign-in
// still works: no generation request, no model, no plan usage. It builds a
// fresh TokenSource aimed at doctorChatGPTIssuer rather than reusing
// tracker_client.go's process-wide cached source (which is fixed to the
// real issuer) — the token file's own lock coordinates the two either way.
func probeChatGPTRefresh(ctx context.Context, path, access string) error {
	probeCtx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	client := chatgptauth.NewAuthClient()
	client.Issuer = doctorChatGPTIssuer
	return chatgptauth.NewTokenSource(path, client).ForceRefresh(probeCtx, access)
}

// chatgptUnusedSettings names each setting that doesn't apply to openai
// under TRACKER_OPENAI_AUTH=chatgpt, with the note doctor shows when it is
// set. The gateway note says the other providers still use it.
var chatgptUnusedSettings = []struct{ name, note string }{
	{"OPENAI_BASE_URL", "OPENAI_BASE_URL is set but doesn't apply to openai in ChatGPT mode"},
	{"TRACKER_GATEWAY_URL", "TRACKER_GATEWAY_URL doesn't apply to openai in ChatGPT mode; other providers still use it"},
	{"OPENAI_API_KEY", "OPENAI_API_KEY is set but not used for openai in ChatGPT mode"},
}

// chatgptHints returns doctor's notes for TRACKER_OPENAI_AUTH=chatgpt: one
// per set setting that doesn't apply to openai, and the always-on reminder
// that dollar ceilings can't see subscription calls. They are hints, which
// never count toward doctor's exit code: each describes a working setup,
// and a correct subscription-only machine must be able to pass doctor. Nil
// outside chatgpt mode.
func chatgptHints() []CheckDetail {
	if !chatGPTSubscriptionActive() {
		return nil
	}
	var out []CheckDetail
	for _, s := range chatgptUnusedSettings {
		if os.Getenv(s.name) != "" {
			out = append(out, CheckDetail{Status: CheckStatusHint, Message: s.note})
		}
	}
	return append(out, CheckDetail{
		Status:  CheckStatusHint,
		Message: "subscription calls cost $0 here; dollar ceilings don't count them",
	})
}
