// ABOUTME: Cross-check that the claude-code and ACP strip lists agree with the env registry (#659).
// ABOUTME: Every name a backend strips from a child must be a registered provider key or base URL.
package handlers

import (
	"strings"
	"testing"

	"github.com/2389-research/tracker/internal/envpolicy"
)

func TestBackendStripListsRegisteredInEnvpolicy(t *testing.T) {
	registered := map[string]bool{}
	for _, n := range envpolicy.ProviderKeyVars() {
		registered[n] = true
	}
	for _, n := range envpolicy.ProviderBaseURLVars() {
		registered[n] = true
	}
	lists := map[string][]string{
		"backend_claudecode providerKeyPrefixes": providerKeyPrefixes,
		"backend_acp acpStrippedPrefixes":        acpStrippedPrefixes,
	}
	for list, prefixes := range lists {
		for _, p := range prefixes {
			name := strings.TrimSuffix(p, "=")
			if !registered[name] {
				t.Errorf("%s strips %s, which envpolicy does not list as a provider key or base URL", list, name)
			}
		}
	}
}
