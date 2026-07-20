// ABOUTME: LLM client provider-adapter construction and gateway base-URL resolution.
// ABOUTME: Split from tracker.go to keep the root API file under the size ceiling (#450).
package tracker

import (
	"fmt"
	"os"
	"strings"

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
