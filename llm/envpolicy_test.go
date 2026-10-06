// ABOUTME: Cross-check that llm's provider-key table agrees with the env registry (#659).
// ABOUTME: A key the client reads but the registry does not know would escape the .env trust policy.
package llm

import (
	"testing"

	"github.com/2389-research/tracker/internal/envpolicy"
)

func TestProviderEnvKeysRegisteredInEnvpolicy(t *testing.T) {
	registered := map[string]bool{}
	for _, n := range envpolicy.ProviderKeyVars() {
		registered[n] = true
	}
	for provider, names := range providerEnvKeys {
		for _, n := range names {
			if !registered[n] {
				t.Errorf("provider %s reads %s, which envpolicy.ProviderKeyVars() does not list", provider, n)
			}
			if v, _ := envpolicy.Lookup(n); v.Purpose != envpolicy.Credential {
				t.Errorf("%s is registered as %s, want credential", n, v.Purpose)
			}
		}
	}
}
