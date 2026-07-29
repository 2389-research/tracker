// ABOUTME: Choice-matching helper extracted from webhook_interviewer.go (#449 sweep).
// ABOUTME: Kept in its own file so the sweep didn't push webhook_interviewer.go over the size ratchet.
package handlers

import "strings"

// matchWebhookChoice returns the first choice that matches normalized, trying
// an exact case-insensitive match before a substring match. Extracted from
// resolveWebhookChoice to keep that function within the complexity ratchet.
func matchWebhookChoice(choices []string, normalized string) (string, bool) {
	for _, c := range choices {
		if strings.ToLower(c) == normalized {
			return c, true
		}
	}
	for _, c := range choices {
		if strings.Contains(normalized, strings.ToLower(c)) {
			return c, true
		}
	}
	return "", false
}
