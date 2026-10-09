// ABOUTME: The environment the claude-code backend hands the claude CLI: Tracker's environment with the five
// ABOUTME: provider keys stripped so the CLI uses subscription auth (TRACKER_PASS_API_KEYS=1 keeps them).
package handlers

import (
	"os"
	"strings"
)

// providerKeyPrefixes are environment variable prefixes that should be stripped
// from the claude subprocess environment. When these keys are present, the
// claude CLI uses them for API auth instead of the user's Max/Pro subscription
// OAuth token. Stripping them ensures the subprocess uses subscription auth.
// Users who need API key auth can set TRACKER_PASS_API_KEYS=1 to override.
var providerKeyPrefixes = []string{
	"ANTHROPIC_API_KEY=",
	"OPENAI_API_KEY=",
	"OPENAI_COMPAT_API_KEY=",
	"GEMINI_API_KEY=",
	"GOOGLE_API_KEY=",
}

// buildEnv constructs the environment for the claude subprocess.
// Strips LLM provider API keys so the claude CLI uses subscription auth
// (Max/Pro OAuth) instead of consuming API credits. The full parent
// environment is passed through otherwise — Claude Code needs access to
// its config directory, SSH agent, and other system state.
//
// The Bash commands, hooks and MCP servers claude starts inherit this
// environment, and Tracker can't filter them from outside. Tracker does not
// set Claude Code's own scrub (CLAUDE_CODE_SUBPROCESS_ENV_SCRUB=1): under the
// bypassPermissions mode Tracker defaults to, it also makes claude refuse
// $VAR expansions, env and sh -c (#660). An operator can export it; it
// passes through like any other variable.
func buildEnv() []string {
	if os.Getenv("TRACKER_PASS_API_KEYS") == "1" {
		return os.Environ()
	}
	return filterProviderKeys(os.Environ())
}

// ClaudeCLIEnv is the environment the claude-code backend launches the claude
// CLI with (buildEnv). Callers outside this package that run the same binary,
// such as tracker doctor's --version probe, use it so a probe never gets keys
// the launch strips.
func ClaudeCLIEnv() []string {
	return buildEnv()
}

// filterProviderKeys strips LLM provider API key vars from the given environment.
func filterProviderKeys(env []string) []string {
	clean := make([]string, 0, len(env))
	for _, e := range env {
		if !hasProviderKeyPrefix(e) {
			clean = append(clean, e)
		}
	}
	return clean
}

// hasProviderKeyPrefix returns true if the env var is a provider API key that should be stripped.
func hasProviderKeyPrefix(envVar string) bool {
	for _, prefix := range providerKeyPrefixes {
		if strings.HasPrefix(envVar, prefix) {
			return true
		}
	}
	return false
}
