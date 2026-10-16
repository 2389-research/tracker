// ABOUTME: Parses the TRACKER_OPENAI_AUTH switch that picks how Tracker authenticates calls to
// ABOUTME: the openai provider: the default API key, or a signed-in ChatGPT subscription (design §2).
package chatgptauth

import (
	"fmt"
	"os"
	"strings"
)

// EnvVar is the environment variable that selects openai's auth mode.
// Registered in internal/envpolicy as NetworkDestination / ConfigEnv: the
// shell or the operator's ~/.config/tracker/.env may set it, never a
// project <workdir>/.env (#659).
const EnvVar = "TRACKER_OPENAI_AUTH"

// Mode selects how Tracker authenticates calls to the openai provider.
type Mode string

const (
	// ModeAPIKey sends calls with OPENAI_API_KEY. It is the default.
	ModeAPIKey Mode = "api-key"
	// ModeChatGPT bills calls to a signed-in ChatGPT subscription instead.
	ModeChatGPT Mode = "chatgpt"
)

// ParseMode trims v and validates it against the two known modes. An empty
// string — TRACKER_OPENAI_AUTH unset — means ModeAPIKey, today's behavior.
// Any other value is a configuration error naming both valid values.
func ParseMode(v string) (Mode, error) {
	trimmed := strings.TrimSpace(v)
	if trimmed == "" {
		return ModeAPIKey, nil
	}
	m := Mode(trimmed)
	if m == ModeAPIKey || m == ModeChatGPT {
		return m, nil
	}
	return "", fmt.Errorf("invalid %s=%q: must be %q or %q", EnvVar, v, ModeAPIKey, ModeChatGPT)
}

// ModeFromEnv reads and parses EnvVar from the process environment.
func ModeFromEnv() (Mode, error) {
	return ParseMode(os.Getenv(EnvVar))
}
