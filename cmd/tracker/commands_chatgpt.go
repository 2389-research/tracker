// ABOUTME: Provider-status label for the openai slot under TRACKER_OPENAI_AUTH=chatgpt — used by
// ABOUTME: printProviderStatus (commands.go) for `tracker version`. Split out to stay under the file-size ratchet.
package main

import "github.com/2389-research/tracker/internal/chatgptauth"

// providerStatusLabel returns name's display label for the provider status
// line, or "" when it isn't configured. Under TRACKER_OPENAI_AUTH=chatgpt,
// openai's slot depends only on the ChatGPT sign-in: OPENAI_API_KEY goes
// unused in that mode, so it never makes openai look ready.
func providerStatusLabel(name string, envs []string) string {
	if name == "openai" && chatgptModeOn() {
		return chatgptSubscriptionLabel()
	}
	if providerHasKey(envs) {
		return name
	}
	return ""
}

// chatgptModeOn reports whether TRACKER_OPENAI_AUTH=chatgpt. An invalid
// value counts as off here; `tracker run` and `tracker doctor` report it.
func chatgptModeOn() bool {
	mode, err := chatgptauth.ModeFromEnv()
	return err == nil && mode == chatgptauth.ModeChatGPT
}

// chatgptSubscriptionLabel is openai's label in ChatGPT mode: "openai
// (ChatGPT subscription)" when the token file holds a sign-in — so a
// machine with only the subscription still shows openai — and "" when it
// doesn't, because every openai call would then fail.
func chatgptSubscriptionLabel() string {
	path, err := chatgptauth.DefaultPath()
	if err != nil {
		return ""
	}
	status, err := chatgptauth.ReadStatus(path)
	if err != nil || !status.SignedIn {
		return ""
	}
	return "openai (ChatGPT subscription)"
}
