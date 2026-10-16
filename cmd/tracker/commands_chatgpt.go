// ABOUTME: Provider-status label for the openai slot under TRACKER_OPENAI_AUTH=chatgpt — used by
// ABOUTME: printProviderStatus (commands.go) for `tracker version`. Split out to stay under the file-size ratchet.
package main

import "github.com/2389-research/tracker/internal/chatgptauth"

// providerStatusLabel returns name's display label for the provider status
// line, or "" when it isn't configured. openai's ChatGPT subscription
// sign-in, when active, wins over the OPENAI_API_KEY check and renders
// under its own label.
func providerStatusLabel(name string, envs []string) string {
	if name == "openai" {
		if label, ok := chatgptSubscriptionLabel(); ok {
			return label
		}
	}
	if providerHasKey(envs) {
		return name
	}
	return ""
}

// chatgptSubscriptionLabel reports the openai slot's label when
// TRACKER_OPENAI_AUTH=chatgpt and the sign-in is present, so a machine with
// only the ChatGPT subscription — no OPENAI_API_KEY — still shows openai as
// configured, under its own label, instead of falling through to "none".
func chatgptSubscriptionLabel() (string, bool) {
	mode, err := chatgptauth.ModeFromEnv()
	if err != nil || mode != chatgptauth.ModeChatGPT {
		return "", false
	}
	path, err := chatgptauth.DefaultPath()
	if err != nil {
		return "", false
	}
	status, err := chatgptauth.ReadStatus(path)
	if err != nil || !status.SignedIn {
		return "", false
	}
	return "openai (ChatGPT subscription)", true
}
