// ABOUTME: Tests gateway base-URL resolution via the Config *argument* path (#478).
package tracker

import (
	"errors"
	"testing"
)

// TestResolveProviderBaseURL_ViaConfigArgument exercises the gatewayURL argument
// (Config.GatewayURL) path — the headline of dropping the process-global
// os.Setenv. Prior tests only covered the env path (empty argument), so the
// argument path and its fail-closed refusal were unverified.
func TestResolveProviderBaseURL_ViaConfigArgument(t *testing.T) {
	// Neutralize the per-provider env vars and TRACKER_GATEWAY_* so we resolve
	// purely from the argument (Getenv "" is treated as unset by the resolver).
	t.Setenv("ANTHROPIC_BASE_URL", "")
	t.Setenv("OPENAI_COMPAT_BASE_URL", "")
	t.Setenv("TRACKER_GATEWAY_URL", "")
	t.Setenv("TRACKER_GATEWAY_KIND", "")

	// cf-aig appends a per-provider suffix to the configured gateway root.
	got, err := resolveProviderBaseURLWithGateway("anthropic", "https://gw.example/", "cf-aig")
	if err != nil {
		t.Fatalf("cf-aig anthropic: unexpected error %v", err)
	}
	if want := "https://gw.example/anthropic"; got != want {
		t.Fatalf("cf-aig anthropic base = %q, want %q", got, want)
	}

	// bedrock refuses to route openai-compat: must surface ErrGatewayRouteRefused
	// rather than silently return "" (an SDK-default fallback would leak the
	// gateway token to the public endpoint).
	if _, err := resolveProviderBaseURLWithGateway("openai-compat", "https://gw.example", "bedrock"); !errors.Is(err, ErrGatewayRouteRefused) {
		t.Fatalf("bedrock openai-compat: err = %v, want ErrGatewayRouteRefused", err)
	}

	// A per-provider env var still wins over the argument.
	t.Setenv("ANTHROPIC_BASE_URL", "https://direct.example")
	if got, _ := resolveProviderBaseURLWithGateway("anthropic", "https://gw.example", "cf-aig"); got != "https://direct.example" {
		t.Fatalf("per-provider env var must win, got %q", got)
	}
}
