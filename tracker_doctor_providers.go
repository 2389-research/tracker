// ABOUTME: Doctor check for configured LLM providers (key presence, format, optional auth probe).
// ABOUTME: Split from tracker_doctor.go (#453) — behavior-preserving extraction.
package tracker

import (
	"context"
	"fmt"
	"os"
	"regexp"
	"strings"
	"time"

	"github.com/2389-research/tracker/llm"
	"github.com/2389-research/tracker/llm/anthropic"
	"github.com/2389-research/tracker/llm/google"
	"github.com/2389-research/tracker/llm/openai"
	"github.com/2389-research/tracker/llm/openaicompat"
)

type providerDef struct {
	name         string
	envVars      []string
	defaultModel string
	buildAdapter func(key string) (llm.ProviderAdapter, error)
}

var knownProviders = []providerDef{
	{
		name:         "Anthropic",
		envVars:      []string{"ANTHROPIC_API_KEY"},
		defaultModel: "claude-haiku-4-5",
		buildAdapter: func(key string) (llm.ProviderAdapter, error) {
			// Strict resolver so doctor reports the same gateway-route
			// refusal that `tracker run` would hard-fail on.
			base, err := ResolveProviderBaseURLStrict("anthropic")
			if err != nil {
				return nil, err
			}
			var opts []anthropic.Option
			if base != "" {
				opts = append(opts, anthropic.WithBaseURL(base))
			}
			return anthropic.New(key, opts...), nil
		},
	},
	{
		name:         "OpenAI",
		envVars:      []string{"OPENAI_API_KEY"},
		defaultModel: "gpt-4o-mini",
		buildAdapter: func(key string) (llm.ProviderAdapter, error) {
			base, err := ResolveProviderBaseURLStrict("openai")
			if err != nil {
				return nil, err
			}
			var opts []openai.Option
			if base != "" {
				opts = append(opts, openai.WithBaseURL(base))
			}
			return openai.New(key, opts...), nil
		},
	},
	{
		name:         "OpenAI-Compat",
		envVars:      []string{"OPENAI_COMPAT_API_KEY"},
		defaultModel: "gpt-4o-mini",
		buildAdapter: func(key string) (llm.ProviderAdapter, error) {
			base, err := ResolveProviderBaseURLStrict("openai-compat")
			if err != nil {
				return nil, err
			}
			var opts []openaicompat.Option
			if base != "" {
				opts = append(opts, openaicompat.WithBaseURL(base))
			}
			return openaicompat.New(key, opts...), nil
		},
	},
	{
		name:         "Gemini",
		envVars:      []string{"GEMINI_API_KEY", "GOOGLE_API_KEY"},
		defaultModel: "gemini-3-flash-preview", // 2.0-flash is retired; it 404s
		buildAdapter: func(key string) (llm.ProviderAdapter, error) {
			base, err := ResolveProviderBaseURLStrict("gemini")
			if err != nil {
				return nil, err
			}
			var opts []google.Option
			if base != "" {
				opts = append(opts, google.WithBaseURL(base))
			}
			return google.New(key, opts...), nil
		},
	},
}

var probeProviderFn = probeProvider

// checkProviders reports on each configured LLM provider. When probe
// is true, a 1-token API call verifies auth for each configured provider.
// providerState classifies one provider's configuration outcome inside
// checkProviders, so the main loop can route the detail line and tallies.
type providerState int

const (
	stateMissing    providerState = iota // no key found — optional, not an error
	stateInvalid                         // key present but invalid-format or probe-failed
	stateConfigured                      // key present and (when probed) auth-verified
)

func checkProviders(ctx context.Context, probe bool) CheckResult {
	out := CheckResult{Name: "LLM Providers"}
	var configuredNames []string
	var missingNames []string
	hasProviderErrors := false

	for _, p := range knownProviders {
		detail, state := evaluateProvider(ctx, p, probe)
		switch state {
		case stateMissing:
			missingNames = append(missingNames, p.name)
		case stateInvalid:
			out.Details = append(out.Details, detail)
			hasProviderErrors = true
		case stateConfigured:
			out.Details = append(out.Details, detail)
			configuredNames = append(configuredNames, p.name)
		}
	}

	if len(configuredNames) == 0 {
		return providersNoneConfigured(out, missingNames)
	}
	finalizeProvidersStatus(&out, configuredNames, missingNames, hasProviderErrors, probe)
	return out
}

// evaluateProvider resolves one provider's key, validates its format, and
// (when probe is set and an adapter exists) live-probes auth, returning the
// detail line to surface plus the classified state.
func evaluateProvider(ctx context.Context, p providerDef, probe bool) (CheckDetail, providerState) {
	key, envName := findProviderKey(p.envVars)
	if key == "" {
		return CheckDetail{}, stateMissing
	}
	masked := maskKey(key)
	if !isValidAPIKey(p.name, key) {
		return CheckDetail{
			Status:  CheckStatusError,
			Message: fmt.Sprintf("%-15s %s=%s (invalid format)", p.name, envName, masked),
			Hint:    fmt.Sprintf("%s keys should match expected format — run `tracker setup`", p.name),
		}, stateInvalid
	}
	if probe && p.buildAdapter != nil {
		return probeProviderDetail(ctx, p, key, envName, masked)
	}
	return CheckDetail{
		Status:  CheckStatusOK,
		Message: fmt.Sprintf("%-15s %s=%s", p.name, envName, masked),
	}, stateConfigured
}

// probeProviderDetail live-probes a validated provider key and maps the result
// to a detail line, distinguishing auth failures (rotate the key) from
// network/transport failures (don't rotate a working key).
func probeProviderDetail(ctx context.Context, p providerDef, key, envName, masked string) (CheckDetail, providerState) {
	ok, probeMsg, isAuth := probeProviderFn(ctx, p, key)
	if !ok {
		detail := CheckDetail{Status: CheckStatusError}
		if isAuth {
			detail.Message = fmt.Sprintf("%-15s %s=%s (auth failed: %s)", p.name, envName, masked, probeMsg)
			detail.Hint = fmt.Sprintf("your %s key is invalid or expired — export a fresh key or run `tracker setup`", p.name)
		} else {
			// DNS, timeout, transport, context cancel, or other non-auth failure.
			// Do NOT tell the user to rotate a working key.
			detail.Message = fmt.Sprintf("%-15s %s=%s (probe failed: %s)", p.name, envName, masked, probeMsg)
			detail.Hint = fmt.Sprintf("probe for %s failed on network/transport — verify connectivity and %s before rotating keys", p.name, providerBaseURLEnvVar(p.name))
		}
		return detail, stateInvalid
	}
	return CheckDetail{
		Status:  CheckStatusOK,
		Message: fmt.Sprintf("%-15s %s=%s (auth verified)", p.name, envName, masked),
	}, stateConfigured
}

// providersNoneConfigured builds the terminal "no providers" result, listing
// each known provider's primary env var as a not-set error line.
func providersNoneConfigured(out CheckResult, missingNames []string) CheckResult {
	for _, name := range missingNames {
		for _, pd := range knownProviders {
			if pd.name == name {
				out.Details = append(out.Details, CheckDetail{
					Status:  CheckStatusError,
					Message: fmt.Sprintf("%-15s %s not set", pd.name, pd.envVars[0]),
				})
				break
			}
		}
	}
	out.Status = CheckStatusError
	out.Message = "no LLM providers configured"
	out.Hint = "run `tracker setup` or export ANTHROPIC_API_KEY / OPENAI_API_KEY / GEMINI_API_KEY"
	return out
}

// finalizeProvidersStatus appends the optional "not configured" hint and sets
// the summary status/message when at least one provider is configured.
func finalizeProvidersStatus(out *CheckResult, configuredNames, missingNames []string, hasProviderErrors, probe bool) {
	if len(missingNames) > 0 {
		// "not configured" is informational when at least one provider works —
		// rendered as a hint line, not an error or warning, so Status=hint.
		out.Details = append(out.Details, CheckDetail{
			Status:  CheckStatusHint,
			Message: fmt.Sprintf("not configured: %s (optional)", strings.Join(missingNames, ", ")),
		})
	}
	if hasProviderErrors {
		out.Status = CheckStatusWarn
	} else {
		out.Status = CheckStatusOK
	}
	if probe {
		out.Message = fmt.Sprintf("%d provider(s) configured and auth verified", len(configuredNames))
	} else {
		out.Message = fmt.Sprintf("%d provider(s) configured: %s", len(configuredNames), strings.Join(configuredNames, ", "))
	}
}

func findProviderKey(envVars []string) (key, envName string) {
	for _, e := range envVars {
		if v := os.Getenv(e); v != "" {
			return v, e
		}
	}
	return "", ""
}

// probeProvider returns (ok, msg, isAuthFailure). The third return lets the
// caller distinguish an actual auth failure (rotate-the-key guidance) from
// a network/transport/timeout failure (don't rotate good keys).
func probeProvider(ctx context.Context, p providerDef, key string) (bool, string, bool) {
	adapter, err := p.buildAdapter(key)
	if err != nil {
		return false, fmt.Sprintf("build adapter: %v", err), false
	}
	client, err := llm.NewClient(llm.WithProvider(adapter))
	if err != nil {
		return false, fmt.Sprintf("create client: %v", err), false
	}
	defer client.Close()
	probeCtx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	maxTok := 16
	req := &llm.Request{
		Model:     p.defaultModel,
		Messages:  []llm.Message{llm.UserMessage("ping")},
		MaxTokens: &maxTok,
	}
	_, err = client.Complete(probeCtx, req)
	if err != nil {
		msg := err.Error()
		if isAuthError(msg) {
			return false, "invalid or expired API key", true
		}
		// Sanitize FIRST, then trim. If we trim first, a key that
		// straddles the 80-char boundary gets cut into a shorter prefix
		// that no longer matches the regex, leaking the prefix. Sanitize
		// the full message, then trim whatever's left.
		return false, trimErrMsg(sanitizeProviderError(msg), 80), false
	}
	return true, "", false
}

// sanitizeProviderError strips API keys and bearer tokens from provider error
// text so they never land in CheckDetail.Message (which library consumers may
// log or forward to webhooks).
var (
	apiKeyPatterns = []*regexp.Regexp{
		regexp.MustCompile(`sk-ant-[A-Za-z0-9_\-]{6,}`),
		regexp.MustCompile(`sk-[A-Za-z0-9_\-]{10,}`),
		regexp.MustCompile(`AIza[0-9A-Za-z_\-]{20,}`),
	}
	bearerPattern = regexp.MustCompile(`(?i)bearer\s+[A-Za-z0-9._\-]{6,}`)
)

func sanitizeProviderError(msg string) string {
	for _, re := range apiKeyPatterns {
		msg = re.ReplaceAllString(msg, "[redacted-key]")
	}
	msg = bearerPattern.ReplaceAllString(msg, "Bearer [redacted]")
	return msg
}

func isAuthError(msg string) bool {
	lower := strings.ToLower(msg)
	for _, kw := range []string{"401", "403", "unauthorized", "authentication", "invalid api key", "api key", "forbidden"} {
		if strings.Contains(lower, kw) {
			return true
		}
	}
	return false
}

func trimErrMsg(msg string, maxLen int) string {
	if len(msg) <= maxLen {
		return msg
	}
	return msg[:maxLen] + "..."
}

func maskKey(key string) string {
	if len(key) <= 8 {
		return "****"
	}
	return key[:4] + "..." + key[len(key)-4:]
}

func isValidAPIKey(provider string, key string) bool {
	if key == "" {
		return false
	}
	switch provider {
	case "Anthropic":
		return strings.HasPrefix(key, "sk-ant-") && len(key) > 10
	case "OpenAI", "OpenAI-Compat":
		return strings.HasPrefix(key, "sk-") && len(key) > 10
	case "Gemini":
		return len(key) > 10
	}
	return len(key) > 5
}
