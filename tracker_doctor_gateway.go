// ABOUTME: Doctor checks for environment-variable warnings and gateway routing caveats.
// ABOUTME: Split from tracker_doctor.go (#453) — behavior-preserving extraction.
package tracker

import (
	"fmt"
	"os"
	"strings"
)

// checkEnvWarnings warns when opt-in security overrides are active.
func checkEnvWarnings() CheckResult {
	dangerousVars := map[string]string{
		"TRACKER_PASS_ENV":      "passes all env vars to tool subprocesses (security risk)",
		"TRACKER_PASS_API_KEYS": "passes API keys to tool subprocesses (security risk)",
	}
	var found []string
	for envVar, desc := range dangerousVars {
		if os.Getenv(envVar) == "1" {
			found = append(found, fmt.Sprintf("%s (%s)", envVar, desc))
		}
	}
	if len(found) == 0 {
		return CheckResult{Name: "Environment Warnings", Status: CheckStatusOK, Message: "no dangerous environment variables detected"}
	}
	return CheckResult{
		Name:    "Environment Warnings",
		Status:  CheckStatusWarn,
		Message: fmt.Sprintf("dangerous variables set: %s", strings.Join(found, "; ")),
		Hint:    "unset TRACKER_PASS_ENV and TRACKER_PASS_API_KEYS to restore default security posture",
	}
}

// gatewayBaseURLEnvVars maps a provider label to its per-provider *_BASE_URL
// override env var. The Gateway Routing check uses this to report which
// overrides win over TRACKER_GATEWAY_URL. Order mirrors knownProviders.
var gatewayBaseURLEnvVars = []struct {
	provider string
	envVar   string
}{
	{"anthropic", "ANTHROPIC_BASE_URL"},
	{"openai", "OPENAI_BASE_URL"},
	{"openai-compat", "OPENAI_COMPAT_BASE_URL"},
	{"gemini", "GEMINI_BASE_URL"},
}

// providerBaseURLEnvVar returns the canonical *_BASE_URL env var name for a
// provider label via gatewayBaseURLEnvVars — naive ToUpper(name) renders
// invalid names for dashed labels (OPENAI-COMPAT_BASE_URL).
func providerBaseURLEnvVar(name string) string {
	for _, e := range gatewayBaseURLEnvVars {
		if e.provider == name {
			return e.envVar
		}
	}
	return strings.ReplaceAll(strings.ToUpper(name), "-", "_") + "_BASE_URL"
}

// checkGatewayRouting surfaces non-fatal gateway routing caveats (#277). It
// runs only when TRACKER_GATEWAY_URL or TRACKER_GATEWAY_KIND is set (see
// Doctor) and emits informational notes — never warnings or errors, since
// every condition it reports is an intentional configuration:
//
//   - B.1 bedrock masquerade: when OpenAI traffic actually routes through the
//     bedrock gateway (KIND=bedrock + a gateway URL + OPENAI_API_KEY, with no
//     OPENAI_BASE_URL override), gpt-* / o*-* model strings route to Claude
//     today because AWS Bedrock has no OpenAI models yet. Surfaced once at
//     setup time rather than as a per-session runtime warning.
//   - B.2 per-provider precedence: a *_BASE_URL override silently wins over
//     TRACKER_GATEWAY_URL for that provider.
func checkGatewayRouting() CheckResult {
	out := CheckResult{Name: "Gateway Routing", Status: CheckStatusOK}

	gatewayURL := strings.TrimRight(os.Getenv("TRACKER_GATEWAY_URL"), "/")
	kind := os.Getenv("TRACKER_GATEWAY_KIND")
	kindLabel := kind
	if kindLabel == "" {
		kindLabel = string(GatewayKindCFAIG) + " (default)"
	}

	out.Details = append(out.Details, gatewayContextDetail(gatewayURL, kindLabel))

	notes := 0
	if note, ok := bedrockMasqueradeNote(kind, gatewayURL); ok {
		out.Details = append(out.Details, note)
		notes++
	}
	if note, ok := baseURLOverrideNote(gatewayURL); ok {
		out.Details = append(out.Details, note)
		notes++
	}

	out.Message = gatewayRoutingMessage(gatewayURL, kindLabel, notes)
	return out
}

// gatewayContextDetail returns the leading "what routing is in effect" line.
func gatewayContextDetail(gatewayURL, kindLabel string) CheckDetail {
	if gatewayURL != "" {
		return CheckDetail{
			Status:  CheckStatusOK,
			Message: fmt.Sprintf("TRACKER_GATEWAY_URL=%s (kind=%s)", gatewayURL, kindLabel),
		}
	}
	return CheckDetail{
		Status:  CheckStatusOK,
		Message: fmt.Sprintf("TRACKER_GATEWAY_KIND=%s (no TRACKER_GATEWAY_URL — per-provider *_BASE_URL or SDK defaults in effect)", kindLabel),
	}
}

// bedrockMasqueradeNote returns the B.1 OpenAI→Claude masquerade hint when
// OpenAI traffic actually traverses the bedrock gateway:
//   - kind must be bedrock;
//   - a gateway URL must be configured — without one, openai resolves to the
//     SDK default (api.openai.com), so there is no gateway and no masquerade;
//   - OPENAI_BASE_URL must be unset — it wins over the gateway in the resolver,
//     so when set, openai bypasses the gateway (the B.2 note covers that case).
//
// Residual gap: an OPENAI_BASE_URL pointed explicitly at the bedrock gateway
// (e.g. <gateway>/v1) would still masquerade, but an arbitrary URL can't be
// reliably recognized as a gateway endpoint, so we defer to the B.2 note.
func bedrockMasqueradeNote(kind, gatewayURL string) (CheckDetail, bool) {
	if kind != string(GatewayKindBedrock) || gatewayURL == "" || os.Getenv("OPENAI_BASE_URL") != "" {
		return CheckDetail{}, false
	}
	if key, _ := findProviderKey([]string{"OPENAI_API_KEY"}); key == "" {
		return CheckDetail{}, false
	}
	return CheckDetail{
		Status:  CheckStatusHint,
		Message: "TRACKER_GATEWAY_KIND=bedrock: gpt-* / o*-* model strings route to Claude Sonnet 4.6 today (the bedrock gateway translates because AWS hasn't added OpenAI to Bedrock yet). When AWS adds it, the gateway updates its mapping without tracker changes.",
	}, true
}

// baseURLOverrideNote returns the B.2 hint listing per-provider *_BASE_URL
// overrides that win over TRACKER_GATEWAY_URL.
func baseURLOverrideNote(gatewayURL string) (CheckDetail, bool) {
	if gatewayURL == "" {
		return CheckDetail{}, false
	}
	var overridden []string
	for _, p := range gatewayBaseURLEnvVars {
		if os.Getenv(p.envVar) != "" {
			overridden = append(overridden, fmt.Sprintf("%s (%s)", p.provider, p.envVar))
		}
	}
	if len(overridden) == 0 {
		return CheckDetail{}, false
	}
	return CheckDetail{
		Status:  CheckStatusHint,
		Message: fmt.Sprintf("per-provider overrides win over TRACKER_GATEWAY_URL: %s", strings.Join(overridden, ", ")),
	}, true
}

// gatewayRoutingMessage composes the summary line from the routing context.
func gatewayRoutingMessage(gatewayURL, kindLabel string, notes int) string {
	switch {
	case gatewayURL == "":
		// Only the kind is set; without a URL the resolver never routes via a
		// gateway, so don't imply one is in use.
		return fmt.Sprintf("TRACKER_GATEWAY_KIND=%s set without TRACKER_GATEWAY_URL — no gateway routing in effect", kindLabel)
	case notes > 0:
		return fmt.Sprintf("gateway configured (%d routing note(s))", notes)
	default:
		return "gateway configured (no routing caveats)"
	}
}
