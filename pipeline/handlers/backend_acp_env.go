// ABOUTME: The environment the ACP backend hands an agent process and its --version probe: full passthrough by default,
// ABOUTME: provider keys and base URLs stripped with TRACKER_STRIP_ACP_KEYS=1, which also applies on top of terminal commands' exec.CommandEnv.
package handlers

import (
	"os"
	"strings"
)

// acpStrippedPrefixes are env var prefixes stripped from ACP agent subprocesses.
// ACP agents (Claude Code, Codex, Gemini CLI) handle their own auth natively
// via subscription/OAuth — injecting tracker's API keys and base URLs overrides
// the agent's own auth and can redirect it to the wrong endpoint (e.g. a
// Cloudflare AI Gateway that doesn't support the agent's protocol).
var acpStrippedPrefixes = []string{
	"ANTHROPIC_API_KEY=",
	"OPENAI_API_KEY=",
	"OPENAI_COMPAT_API_KEY=",
	"GEMINI_API_KEY=",
	"GOOGLE_API_KEY=",
	"ANTHROPIC_BASE_URL=",
	"OPENAI_BASE_URL=",
	"OPENAI_COMPAT_BASE_URL=",
	"GEMINI_BASE_URL=",
	"GOOGLE_BASE_URL=",
	"OPENROUTER_API_KEY=",
}

// buildEnvForACP returns the environment for ACP agent subprocesses.
// ACP bridges handle their own credential routing internally, so the full
// environment (including API keys) is passed through by default.
// Set TRACKER_STRIP_ACP_KEYS=1 to strip provider keys (e.g., when bridges
// should use subscription auth instead of API key auth).
func buildEnvForACP() []string {
	if stripACPKeys() {
		return filterEnvForACP(os.Environ())
	}
	return os.Environ()
}

// stripACPKeys reports whether the operator set TRACKER_STRIP_ACP_KEYS=1.
func stripACPKeys() bool {
	return os.Getenv("TRACKER_STRIP_ACP_KEYS") == "1"
}

// filterEnvForACP strips API key and base URL env vars from the given environment.
func filterEnvForACP(env []string) []string {
	clean := make([]string, 0, len(env))
	for _, e := range env {
		if !hasACPStrippedPrefix(e) {
			clean = append(clean, e)
		}
	}
	return clean
}

// hasACPStrippedPrefix returns true if the env var should be stripped for ACP agents.
func hasACPStrippedPrefix(envVar string) bool {
	for _, prefix := range acpStrippedPrefixes {
		if strings.HasPrefix(envVar, prefix) {
			return true
		}
	}
	return false
}
