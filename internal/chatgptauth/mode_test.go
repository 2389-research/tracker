// ABOUTME: Tests for the TRACKER_OPENAI_AUTH switch: value parsing, defaulting, the env read,
// ABOUTME: and the config-tier registration that keeps it out of a project .env (#659).
package chatgptauth

import (
	"strings"
	"testing"

	"github.com/2389-research/tracker/internal/envpolicy"
)

func TestParseMode(t *testing.T) {
	cases := []struct {
		name    string
		in      string
		want    Mode
		wantErr bool
	}{
		{name: "empty defaults to api-key", in: "", want: ModeAPIKey},
		{name: "api-key", in: "api-key", want: ModeAPIKey},
		{name: "chatgpt", in: "chatgpt", want: ModeChatGPT},
		{name: "chatgpt padded with whitespace", in: " chatgpt ", want: ModeChatGPT},
		{name: "wrong case is rejected", in: "ChatGPT", wantErr: true},
		{name: "unknown value is rejected", in: "subscription", wantErr: true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := ParseMode(tc.in)
			if tc.wantErr {
				if err == nil {
					t.Fatalf("ParseMode(%q) = %q, nil; want an error", tc.in, got)
				}
				msg := err.Error()
				if !strings.Contains(msg, string(ModeChatGPT)) || !strings.Contains(msg, string(ModeAPIKey)) {
					t.Errorf("ParseMode(%q) error %q does not name both valid values", tc.in, msg)
				}
				return
			}
			if err != nil {
				t.Fatalf("ParseMode(%q) unexpected error: %v", tc.in, err)
			}
			if got != tc.want {
				t.Errorf("ParseMode(%q) = %q, want %q", tc.in, got, tc.want)
			}
		})
	}
}

// TestModeFromEnv proves the glue reads EnvVar itself (not a typo'd name) by
// round-tripping through t.Setenv; ParseMode's own cases cover the parsing.
func TestModeFromEnv(t *testing.T) {
	t.Setenv(EnvVar, "chatgpt")
	got, err := ModeFromEnv()
	if err != nil {
		t.Fatalf("ModeFromEnv() unexpected error: %v", err)
	}
	if got != ModeChatGPT {
		t.Errorf("ModeFromEnv() = %q, want %q", got, ModeChatGPT)
	}
}

func TestModeEnvVarRegisteredConfigTier(t *testing.T) {
	if !envpolicy.AllowedFrom(EnvVar, envpolicy.ConfigEnv) {
		t.Errorf("%s must be allowed from the config .env", EnvVar)
	}
	if envpolicy.AllowedFrom(EnvVar, envpolicy.ProjectEnv) {
		t.Errorf("%s must not be allowed from a project .env", EnvVar)
	}
	v, ok := envpolicy.Lookup(EnvVar)
	if !ok {
		t.Fatalf("%s is not registered in envpolicy", EnvVar)
	}
	if v.Purpose != envpolicy.NetworkDestination {
		t.Errorf("%s Purpose = %q, want NetworkDestination", EnvVar, v.Purpose)
	}
}
