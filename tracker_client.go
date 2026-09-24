// ABOUTME: LLM client provider-adapter construction and gateway base-URL resolution.
// ABOUTME: Split from tracker.go to keep the root API file under the size ceiling (#450).
package tracker

import (
	"errors"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/2389-research/tracker/llm"
	"github.com/2389-research/tracker/llm/anthropic"
	"github.com/2389-research/tracker/llm/google"
	"github.com/2389-research/tracker/llm/openai"
	"github.com/2389-research/tracker/llm/openaicompat"
)

// providerBaseURLEnvKey returns the per-provider *_BASE_URL env var name, or
// "" for an unknown provider.
func providerBaseURLEnvKey(provider string) string {
	switch provider {
	case "anthropic":
		return "ANTHROPIC_BASE_URL"
	case "openai":
		return "OPENAI_BASE_URL"
	case "gemini":
		return "GEMINI_BASE_URL"
	case "openai-compat":
		return "OPENAI_COMPAT_BASE_URL"
	}
	return ""
}

// resolveProviderBaseURLWithGateway resolves the base URL for a provider,
// consulting sources in priority order:
//
//  1. Per-provider env var (*_BASE_URL) — always wins.
//  2. gatewayURL argument (from Config.GatewayURL) with kind-dependent suffix appended.
//  3. TRACKER_GATEWAY_URL env var with kind-dependent suffix appended.
//  4. Empty string — use provider SDK default.
//
// The kind argument (from Config.GatewayKind) selects the suffix map; if
// empty, TRACKER_GATEWAY_KIND env var is consulted, and if that is also
// empty the default is cf-aig.
//
// **Fail-closed contract:** when a gateway URL is configured (either via
// the gatewayURL argument or TRACKER_GATEWAY_URL) AND the (kind,
// provider) pair is unsupported OR the kind is unknown, this function
// returns ErrGatewayRouteRefused. Adapter constructors propagate the
// error so client construction fails — preventing the silent SDK-default
// fallback that would otherwise leak requests (carrying the gateway
// token) to the public default endpoint.
func resolveProviderBaseURLWithGateway(provider, gatewayURL string, gatewayKind GatewayKind) (string, error) {
	envKey := providerBaseURLEnvKey(provider)
	if envKey == "" {
		return "", nil
	}
	if v := os.Getenv(envKey); v != "" {
		return v, nil
	}

	gateway := strings.TrimRight(gatewayURL, "/")
	if gateway == "" {
		gateway = strings.TrimRight(os.Getenv("TRACKER_GATEWAY_URL"), "/")
	}
	if gateway == "" {
		return "", nil
	}

	kind := gatewayKind
	if kind == "" {
		kind = GatewayKind(os.Getenv("TRACKER_GATEWAY_KIND"))
	}
	suffix, ok := gatewaySuffix(kind, provider)
	if !ok {
		return "", fmt.Errorf("%w: kind=%q provider=%q", ErrGatewayRouteRefused, kind, provider)
	}
	return gateway + suffix, nil
}

// ResolveProviderBaseURL returns the base URL a provider's HTTP client should
// use. Resolution order:
//
//  1. The provider-specific env var (ANTHROPIC_BASE_URL, OPENAI_BASE_URL,
//     GEMINI_BASE_URL, OPENAI_COMPAT_BASE_URL).
//  2. TRACKER_GATEWAY_URL with a per-provider suffix appended; the suffix
//     map is selected by TRACKER_GATEWAY_KIND (default cf-aig — Cloudflare
//     AI Gateway conventions).
//  3. Empty string, meaning the provider's SDK default.
//
// Per-provider env vars always win over TRACKER_GATEWAY_URL.
//
// **Lax variant.** This function returns the empty string for BOTH "no
// gateway configured" AND "gateway configured but routing refused." It
// is preserved for backward compatibility with library callers that
// existed before #276 added kind dispatch. New code on the adapter
// construction path MUST use [ResolveProviderBaseURLStrict] so that
// refuse-to-route surfaces as an error rather than a silent SDK-default
// fallback.
func ResolveProviderBaseURL(provider string) string {
	base, _ := ResolveProviderBaseURLStrict(provider)
	return base
}

// ResolveProviderBaseURLStrict is the fail-closed sibling of
// [ResolveProviderBaseURL]. It returns the same URL resolution but
// distinguishes "no gateway needed" (returns "", nil) from "gateway
// configured but routing refused" (returns "", [ErrGatewayRouteRefused]
// wrapped). Adapter constructors call this so a misconfigured gateway
// cannot silently leak requests to public SDK default endpoints.
func ResolveProviderBaseURLStrict(provider string) (string, error) {
	return resolveProviderBaseURLWithGateway(provider, "", "")
}

func newAnthropicAdapter(key, gatewayURL string, gatewayKind GatewayKind) (llm.ProviderAdapter, error) {
	base, err := resolveProviderBaseURLWithGateway("anthropic", gatewayURL, gatewayKind)
	if err != nil {
		return nil, fmt.Errorf("anthropic adapter: %w", err)
	}
	var opts []anthropic.Option
	if base != "" {
		opts = append(opts, anthropic.WithBaseURL(base))
	}
	return anthropic.New(key, opts...), nil
}

func newOpenAIAdapter(key, gatewayURL string, gatewayKind GatewayKind) (llm.ProviderAdapter, error) {
	base, err := resolveProviderBaseURLWithGateway("openai", gatewayURL, gatewayKind)
	if err != nil {
		return nil, fmt.Errorf("openai adapter: %w", err)
	}
	var opts []openai.Option
	if base != "" {
		opts = append(opts, openai.WithBaseURL(base))
	}
	return openai.New(key, opts...), nil
}

func newGeminiAdapter(key, gatewayURL string, gatewayKind GatewayKind) (llm.ProviderAdapter, error) {
	base, err := resolveProviderBaseURLWithGateway("gemini", gatewayURL, gatewayKind)
	if err != nil {
		return nil, fmt.Errorf("gemini adapter: %w", err)
	}
	var opts []google.Option
	if base != "" {
		opts = append(opts, google.WithBaseURL(base))
	}
	return google.New(key, opts...), nil
}

func newOpenAICompatAdapter(key, gatewayURL string, gatewayKind GatewayKind) (llm.ProviderAdapter, error) {
	base, err := resolveProviderBaseURLWithGateway("openai-compat", gatewayURL, gatewayKind)
	if err != nil {
		return nil, fmt.Errorf("openai-compat adapter: %w", err)
	}
	var opts []openaicompat.Option
	if base != "" {
		opts = append(opts, openaicompat.WithBaseURL(base))
	}
	return openaicompat.New(key, opts...), nil
}

// NewLLMClient builds a standalone LLM client from Config for embedders that need
// model calls outside a pipeline run — e.g. request classification or routing.
// Only Provider, GatewayURL, and GatewayKind are consulted; Model, LLMClient, and
// the rest are ignored. It carries the same transport-retry middleware as a run's
// client. The caller owns Close().
func NewLLMClient(cfg Config) (*llm.Client, error) {
	return buildClient(cfg.Provider, cfg.GatewayURL, cfg.GatewayKind)
}

// buildClient creates an LLM client from environment variables with
// base URL support and retry middleware. If provider is non-empty, only
// that provider is configured (returns error if unknown).
// gatewayURL is the gateway root URL from Config.GatewayURL; gatewayKind
// is the matching Config.GatewayKind (empty = cf-aig default). Both are
// consulted after per-provider *_BASE_URL env vars and before the
// TRACKER_GATEWAY_URL / TRACKER_GATEWAY_KIND env-var fallbacks (see
// resolveProviderBaseURLWithGateway).
func buildClient(provider, gatewayURL string, gatewayKind GatewayKind) (*llm.Client, error) {
	constructors := allProviderConstructors(gatewayURL, gatewayKind)

	if provider != "" {
		constructor, ok := constructors[provider]
		if !ok {
			return nil, fmt.Errorf("unknown provider %q (valid: anthropic, openai, gemini, openai-compat)", provider)
		}
		constructors = map[string]func(string) (llm.ProviderAdapter, error){
			provider: constructor,
		}
	}

	client, err := llm.NewClientFromEnv(constructors)
	if err != nil {
		return nil, err
	}

	// LLM transport retries handle transient API errors (rate limits, 5xx).
	client.AddMiddleware(llm.NewRetryMiddleware(
		llm.WithMaxRetries(3),
		llm.WithBaseDelay(2*time.Second),
	))

	return client, nil
}

// allProviderConstructors returns the full map of provider constructor functions.
// gatewayURL is the explicit gateway root URL (from Config.GatewayURL) and
// gatewayKind is the matching path-convention selector (from
// Config.GatewayKind). Both are passed to the adapter constructors so
// library consumers don't need to mutate os.Environ.
func allProviderConstructors(gatewayURL string, gatewayKind GatewayKind) map[string]func(string) (llm.ProviderAdapter, error) {
	return map[string]func(string) (llm.ProviderAdapter, error){
		"anthropic":     func(k string) (llm.ProviderAdapter, error) { return newAnthropicAdapter(k, gatewayURL, gatewayKind) },
		"openai":        func(k string) (llm.ProviderAdapter, error) { return newOpenAIAdapter(k, gatewayURL, gatewayKind) },
		"gemini":        func(k string) (llm.ProviderAdapter, error) { return newGeminiAdapter(k, gatewayURL, gatewayKind) },
		"openai-compat": func(k string) (llm.ProviderAdapter, error) { return newOpenAICompatAdapter(k, gatewayURL, gatewayKind) },
	}
}

// GatewayKind selects the path convention used when TRACKER_GATEWAY_URL is
// set. The default (cf-aig) matches Cloudflare AI Gateway's per-provider
// subpath convention; bedrock targets the 2389 bedrock-gateway Worker which
// uses native SDK URL paths.
//
// See docs/superpowers/specs/2026-06-01-issue-274-bedrock-gateway-integration-design.md.
type GatewayKind string

const (
	// GatewayKindCFAIG routes via Cloudflare AI Gateway path conventions:
	// /anthropic, /openai, /google-ai-studio, /compat. Default.
	GatewayKindCFAIG GatewayKind = "cf-aig"

	// GatewayKindBedrock routes via the 2389 bedrock-gateway Worker which
	// translates SDK requests to AWS Bedrock Converse. Uses native SDK
	// URL conventions: empty suffix for Anthropic, /v1 for OpenAI and
	// Gemini. openai-compat is not supported on this gateway.
	GatewayKindBedrock GatewayKind = "bedrock"
)

// gatewaySuffix returns the per-provider URL path suffix for the given
// gateway kind. Returns ok=false when the (kind, provider) pair is
// unsupported — callers should treat this as "do not route via gateway"
// and emit an actionable error. Unknown kind values also return ok=false
// (fail-closed) rather than silently falling through to the cf-aig default.
func gatewaySuffix(kind GatewayKind, provider string) (string, bool) {
	switch kind {
	case "", GatewayKindCFAIG:
		return cfAIGSuffix(provider)
	case GatewayKindBedrock:
		return bedrockSuffix(provider)
	}
	return "", false
}

// cfAIGSuffix maps a provider to its Cloudflare AI Gateway path suffix.
func cfAIGSuffix(provider string) (string, bool) {
	switch provider {
	case "anthropic":
		return "/anthropic", true
	case "openai":
		return "/openai", true
	case "gemini":
		return "/google-ai-studio", true
	case "openai-compat":
		return "/compat", true
	}
	return "", false
}

// bedrockSuffix maps a provider to its Bedrock gateway path suffix.
func bedrockSuffix(provider string) (string, bool) {
	switch provider {
	case "anthropic":
		return "", true // Anthropic SDK appends /v1/messages itself
	case "openai":
		return "/v1", true
	case "gemini":
		return "/v1", true
	case "openai-compat":
		return "", false // refuse: bedrock gateway has no /compat
	}
	return "", false
}

// ErrGatewayRouteRefused is returned by the strict resolver functions when
// a gateway URL is configured but the (kind, provider) pair is unsupported
// or the kind is unknown. Surfacing this as an error prevents the silent
// SDK-default fallback (e.g. openai-compat defaulting to openrouter.ai)
// that contradicts the documented fail-closed semantics of #276.
var ErrGatewayRouteRefused = errors.New("gateway route refused: kind/provider combination unsupported or unknown")
