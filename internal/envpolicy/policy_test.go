// ABOUTME: Invariant tests that freeze the env trust policy (#659): unique names, purpose⇒source tiers,
// ABOUTME: doc/since on every row, and the provider-key list as a superset of the legacy per-package tables.
package envpolicy

import (
	"regexp"
	"strings"
	"testing"
)

func TestTableNamesUnique(t *testing.T) {
	seen := map[string]bool{}
	for _, v := range Table() {
		if seen[v.Name] {
			t.Errorf("duplicate registry entry %q", v.Name)
		}
		seen[v.Name] = true
	}
	if len(seen) < 40 {
		t.Fatalf("registry has %d entries; expected the full inventory (>= 40)", len(seen))
	}
}

// TestSecurityPurposesAreShellOnly is the load-bearing invariant: a variable
// that changes Tracker's security posture, locates trusted state, or picks a
// child binary can only come from the shell; one that picks a network
// destination can come from the operator's config file at most; and a
// project .env may supply nothing but credentials.
func TestSecurityPurposesAreShellOnly(t *testing.T) {
	for _, v := range Table() {
		switch v.Purpose {
		case SecuritySwitch, TrustedState, SubprocessTarget, Output, RunIdentity:
			if v.Source != ShellOnly {
				t.Errorf("%s (%s) has Source %s, want shell-only", v.Name, v.Purpose, v.Source)
			}
		case NetworkDestination, Cost, UX:
			if v.Source > ConfigEnv {
				t.Errorf("%s (%s) has Source %s, want at most config-env", v.Name, v.Purpose, v.Source)
			}
		case Credential:
			// any tier
		default:
			t.Errorf("%s has unknown purpose %q", v.Name, v.Purpose)
		}
		if v.Source == ProjectEnv && v.Purpose != Credential {
			t.Errorf("%s is project-env but not a credential (%s)", v.Name, v.Purpose)
		}
		if v.Implicit && v.Source != ShellOnly {
			t.Errorf("implicit %s must be shell-only, got %s", v.Name, v.Source)
		}
	}
}

func TestEveryVarHasDocAndSince(t *testing.T) {
	since := regexp.MustCompile(`^(v\d+\.\d+\.\d+|unreleased)$`)
	for _, v := range Table() {
		if strings.TrimSpace(v.Doc) == "" {
			t.Errorf("%s has no Doc", v.Name)
		}
		if !since.MatchString(v.Since) {
			t.Errorf("%s has Since %q, want vX.Y.Z or unreleased", v.Name, v.Since)
		}
		if v.Name != strings.ToUpper(v.Name) && !v.Implicit {
			t.Errorf("%s is not upper-case", v.Name)
		}
	}
}

// TestProviderKeyVarsSupersetOfLegacyTables mirrors the four provider-key
// lists that predate the registry. The mirrors are literal copies on purpose:
// this package cannot import cmd/tracker, and the point is that the registry
// never silently drops a name one of them knows. The llm and handlers
// packages carry the live cross-checks against their own tables.
func TestProviderKeyVarsSupersetOfLegacyTables(t *testing.T) {
	legacy := map[string][]string{
		"llm/client.go providerEnvKeys":                    {"ANTHROPIC_API_KEY", "OPENAI_API_KEY", "GEMINI_API_KEY", "GOOGLE_API_KEY", "OPENAI_COMPAT_API_KEY"},
		"cmd/tracker/config_env.go providerEnvKeys (keys)": {"OPENAI_API_KEY", "ANTHROPIC_API_KEY", "GEMINI_API_KEY", "GOOGLE_API_KEY", "OPENAI_COMPAT_API_KEY"},
		"backend_claudecode.go providerKeyPrefixes":        {"ANTHROPIC_API_KEY", "OPENAI_API_KEY", "OPENAI_COMPAT_API_KEY", "GEMINI_API_KEY", "GOOGLE_API_KEY"},
		"backend_acp.go acpStrippedPrefixes (keys)":        {"ANTHROPIC_API_KEY", "OPENAI_API_KEY", "OPENAI_COMPAT_API_KEY", "GEMINI_API_KEY", "GOOGLE_API_KEY", "OPENROUTER_API_KEY"},
	}
	keys := toSet(ProviderKeyVars())
	for table, names := range legacy {
		for _, n := range names {
			if !keys[n] {
				t.Errorf("ProviderKeyVars() lacks %s (known to %s)", n, table)
			}
		}
	}
	legacyURLs := map[string][]string{
		"tracker_client.go providerBaseURLEnvKey":          {"ANTHROPIC_BASE_URL", "OPENAI_BASE_URL", "GEMINI_BASE_URL", "OPENAI_COMPAT_BASE_URL"},
		"cmd/tracker/config_env.go providerEnvKeys (urls)": {"OPENAI_BASE_URL", "ANTHROPIC_BASE_URL", "GEMINI_BASE_URL", "OPENAI_COMPAT_BASE_URL"},
		"backend_acp.go acpStrippedPrefixes (urls)":        {"ANTHROPIC_BASE_URL", "OPENAI_BASE_URL", "OPENAI_COMPAT_BASE_URL", "GEMINI_BASE_URL", "GOOGLE_BASE_URL"},
	}
	urls := toSet(ProviderBaseURLVars())
	for table, names := range legacyURLs {
		for _, n := range names {
			if !urls[n] {
				t.Errorf("ProviderBaseURLVars() lacks %s (known to %s)", n, table)
			}
		}
	}
	for _, n := range ProviderKeyVars() {
		if v, _ := Lookup(n); v.Source != ProjectEnv {
			t.Errorf("provider key %s must be project-env (it is the one class a project .env may supply), got %s", n, v.Source)
		}
	}
}

func TestLookupAndAllowedFrom(t *testing.T) {
	if _, ok := Lookup("DEFINITELY_NOT_REGISTERED_XYZ"); ok {
		t.Fatal("Lookup of an unknown name succeeded")
	}
	if AllowedFrom("DEFINITELY_NOT_REGISTERED_XYZ", ProjectEnv) {
		t.Fatal("an unregistered name must never be allowed from a file")
	}
	for _, name := range []string{"DYLD_INSERT_LIBRARIES", "GIT_CONFIG_KEY_0", "LD_AUDIT"} {
		v, ok := Lookup(name)
		if !ok || v.Source != ShellOnly || !v.Implicit {
			t.Errorf("Lookup(%s) = %+v, %v; want implicit shell-only family member", name, v, ok)
		}
	}
	if !AllowedFrom("ANTHROPIC_API_KEY", ProjectEnv) {
		t.Error("ANTHROPIC_API_KEY must be allowed from a project .env")
	}
	if AllowedFrom("TRACKER_PASS_ENV", ConfigEnv) || AllowedFrom("TRACKER_PASS_ENV", ProjectEnv) {
		t.Error("TRACKER_PASS_ENV must not be allowed from any file")
	}
	if !AllowedFrom("OPENAI_BASE_URL", ConfigEnv) || AllowedFrom("OPENAI_BASE_URL", ProjectEnv) {
		t.Error("OPENAI_BASE_URL must be config-env: allowed from the config file, refused from the project file")
	}
}

func TestImplicitNamesRegistered(t *testing.T) {
	for _, n := range []string{
		"HTTPS_PROXY", "HTTP_PROXY", "NO_PROXY", "SSL_CERT_FILE", "SSL_CERT_DIR",
		"LD_PRELOAD", "LD_LIBRARY_PATH", "DYLD_INSERT_LIBRARIES", "DYLD_LIBRARY_PATH",
		"PATH", "HOME", "GIT_SSH_COMMAND", "GIT_CONFIG_COUNT", "GOPROXY", "NPM_CONFIG_REGISTRY", "PIP_INDEX_URL",
	} {
		v, ok := Lookup(n)
		if !ok {
			t.Errorf("implicit name %s is not registered", n)
			continue
		}
		if !v.Implicit || v.Source != ShellOnly {
			t.Errorf("%s = %+v; want Implicit shell-only", n, v)
		}
	}
}

func TestProvenanceRecords(t *testing.T) {
	ResetProvenance()
	t.Cleanup(ResetProvenance)
	RecordApplied("OPENAI_API_KEY", "/c/.env")
	RecordApplied("OPENAI_API_KEY", "/w/.env")
	RecordSkipped("TRACKER_PASS_ENV", "/w/.env", "shell-only")
	RecordSkipped("HTTPS_PROXY", "/w/.env", "shell-only")
	if got := AppliedFrom("OPENAI_API_KEY"); got != "/w/.env" {
		t.Errorf("AppliedFrom = %q, want the later file", got)
	}
	if got := AppliedFrom("TRACKER_PASS_ENV"); got != "" {
		t.Errorf("AppliedFrom(skipped) = %q, want empty", got)
	}
	sk := Skipped()
	if len(sk) != 2 || sk[0].Name != "HTTPS_PROXY" || sk[1].Name != "TRACKER_PASS_ENV" {
		t.Errorf("Skipped() = %+v, want two sorted entries", sk)
	}
}

func toSet(names []string) map[string]bool {
	m := make(map[string]bool, len(names))
	for _, n := range names {
		m[n] = true
	}
	return m
}
