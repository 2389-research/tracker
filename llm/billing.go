// ABOUTME: Detects provider credit/quota exhaustion and builds an actionable,
// ABOUTME: account-attributed message — never printing the raw API key (#487 Phase 1).
package llm

import (
	"errors"
	"fmt"
	"os"
	"strings"
)

// billingSignals are lowercase substrings that mark a provider credit/quota
// exhaustion. Anthropic surfaces credit exhaustion as a generic
// invalid_request_error, so message text — not just the error type — must be
// inspected. These mirror the claude-code backend's isCreditError plus the
// OpenAI/compat insufficient_quota signal.
var billingSignals = []string{
	"credit balance",
	"too low to access",
	"insufficient_quota",
	"insufficient quota",
	"quota exceeded",
	"exceeded your current quota",
}

// IsBillingError reports whether err is a provider credit/quota exhaustion — a
// recoverable operational condition ("add funds and retry"), distinct from a
// code bug, an auth failure, or a bad request. It matches the typed
// QuotaExceededError (OpenAI/compat) and any error in the chain whose message
// carries a billing signal (Anthropic's credit-balance case).
func IsBillingError(err error) bool {
	if err == nil {
		return false
	}
	// The typed insufficient-quota error (OpenAI / openai-compat) is always billing.
	var qe *QuotaExceededError
	if errors.As(err, &qe) {
		return true
	}
	// A *retryable* provider error is transient throttling (e.g. a 429 rate limit),
	// NOT credit exhaustion — even when its message says "Quota exceeded" (Gemini
	// reuses that text for per-minute limits). The type system already drew this
	// line (RateLimitError.Retryable()==true vs QuotaExceededError==false); don't
	// second-guess it with a substring and turn a retry into a fatal "add credit".
	var pe ProviderErrorInterface
	if errors.As(err, &pe) && pe.Retryable() {
		return false
	}
	msg := strings.ToLower(err.Error())
	for _, s := range billingSignals {
		if strings.Contains(msg, s) {
			return true
		}
	}
	return false
}

// BillingHelp returns an actionable, account-attributed message for a billing
// error (and true), or ("", false) if err is not one. The message identifies the
// failing provider, which env var supplied the key, and a masked fingerprint of
// that key — so a user with multiple accounts knows exactly where to add credit
// — plus the provider's billing URL. It NEVER includes the raw key.
func BillingHelp(err error) (string, bool) {
	if !IsBillingError(err) {
		return "", false
	}
	provider := providerOf(err)
	var b strings.Builder
	b.WriteString("provider billing/quota exhausted — a provider reported insufficient credit, so the run stopped.")
	if provider != "" {
		fmt.Fprintf(&b, "\n  provider : %s", provider)
	}
	if name, masked, ok := keyFingerprint(provider); ok {
		fmt.Fprintf(&b, "\n  key from : $%s  (%s)", name, masked)
	}
	if url := billingURL(provider); url != "" {
		fmt.Fprintf(&b, "\n  add credit: %s", url)
	}
	return b.String(), true
}

// providerOf extracts the provider name from any ProviderError in the chain.
func providerOf(err error) string {
	var pe ProviderErrorInterface
	if errors.As(err, &pe) {
		return pe.GetProvider()
	}
	return ""
}

// keyFingerprint returns the env var name that supplied the provider's key and a
// masked fingerprint of its value, so the user can match it against the key they
// expect. When no key is set it still names the primary env var (masked "not
// set") so the user knows what to look for. ok is false for an unknown provider.
func keyFingerprint(provider string) (name, masked string, ok bool) {
	envs := providerEnvKeys[provider]
	if len(envs) == 0 {
		return "", "", false
	}
	for _, e := range envs {
		if v := os.Getenv(e); v != "" {
			return e, maskKey(v), true
		}
	}
	return envs[0], "not set", true
}

// maskKey shows only a safe prefix/suffix of a secret — never the full value.
func maskKey(key string) string {
	if len(key) <= 8 {
		return "****"
	}
	return key[:4] + "…" + key[len(key)-4:]
}

// billingURL returns the provider's billing/credits page, or "" if unknown.
func billingURL(provider string) string {
	switch provider {
	case "anthropic":
		return "https://console.anthropic.com/settings/billing"
	case "openai":
		return "https://platform.openai.com/account/billing/overview"
	case "gemini":
		return "https://aistudio.google.com/app/apikey"
	default:
		return ""
	}
}
