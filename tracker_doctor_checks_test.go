// ABOUTME: Unit tests for the individually-isolated doctor check helpers (#453).
// ABOUTME: These pure helpers became directly testable once the monolith split.
package tracker

import "testing"

func TestMaskKey(t *testing.T) {
	cases := map[string]string{
		"":                "****",
		"short":           "****",
		"12345678":        "****",
		"sk-ant-abcd1234": "sk-a...1234",
	}
	for in, want := range cases {
		if got := maskKey(in); got != want {
			t.Errorf("maskKey(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestIsValidAPIKey(t *testing.T) {
	cases := []struct {
		provider string
		key      string
		want     bool
	}{
		{"Anthropic", "sk-ant-abcdefghij", true},
		{"Anthropic", "sk-abcdefghij", false}, // wrong prefix
		{"Anthropic", "sk-ant-", false},       // too short
		{"OpenAI", "sk-abcdefghij", true},
		{"OpenAI", "abc", false},
		{"OpenAI-Compat", "sk-abcdefghij", true},
		{"Gemini", "AIzaSyABCDEFGH", true},
		{"Gemini", "short", false},
		{"Unknown", "abcdef", true}, // len > 5 fallback
		{"Unknown", "abc", false},   // len <= 5 fallback
		{"Anthropic", "", false},    // empty always invalid
	}
	for _, c := range cases {
		if got := isValidAPIKey(c.provider, c.key); got != c.want {
			t.Errorf("isValidAPIKey(%q, %q) = %v, want %v", c.provider, c.key, got, c.want)
		}
	}
}

func TestParseGitignoreEntries(t *testing.T) {
	content := "# comment\n.tracker/\n\nruns\n  .ai/  \n"
	entries := parseGitignoreEntries(content)
	for _, want := range []string{".tracker", "runs", ".ai"} {
		if !entries[want] {
			t.Errorf("parseGitignoreEntries missing %q; got %v", want, entries)
		}
	}
	if entries["# comment"] {
		t.Error("parseGitignoreEntries should skip comment lines")
	}
	if entries[""] {
		t.Error("parseGitignoreEntries should skip blank lines")
	}
}

func TestProviderBaseURLEnvVar(t *testing.T) {
	cases := map[string]string{
		"anthropic":     "ANTHROPIC_BASE_URL",
		"openai":        "OPENAI_BASE_URL",
		"openai-compat": "OPENAI_COMPAT_BASE_URL",
		"gemini":        "GEMINI_BASE_URL",
		// unknown label falls through to dash->underscore ToUpper.
		"my-provider": "MY_PROVIDER_BASE_URL",
	}
	for in, want := range cases {
		if got := providerBaseURLEnvVar(in); got != want {
			t.Errorf("providerBaseURLEnvVar(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestCheckDippinVersionMismatch(t *testing.T) {
	cases := []struct {
		cli, pinned  string
		wantMismatch bool
	}{
		{"v0.49.0", "v0.49.0", false},
		{"v0.49.3", "v0.49.0", false}, // patch differences ignored
		{"v0.48.0", "v0.49.0", true},  // minor differs
		{"v1.49.0", "v0.49.0", true},  // major differs
		{"garbage", "v0.49.0", false}, // unparseable -> no mismatch
	}
	for _, c := range cases {
		got, _ := checkDippinVersionMismatch(c.cli, c.pinned)
		if got != c.wantMismatch {
			t.Errorf("checkDippinVersionMismatch(%q,%q) = %v, want %v", c.cli, c.pinned, got, c.wantMismatch)
		}
	}
}

func TestParseVersionMajorMinor(t *testing.T) {
	major, minor, ok := parseVersionMajorMinor("v0.49.3")
	if !ok || major != 0 || minor != 49 {
		t.Errorf("parseVersionMajorMinor(v0.49.3) = (%d,%d,%v), want (0,49,true)", major, minor, ok)
	}
	if _, _, ok := parseVersionMajorMinor("not-a-version"); ok {
		t.Error("parseVersionMajorMinor should fail on unparseable input")
	}
}

func TestTrimErrMsg(t *testing.T) {
	if got := trimErrMsg("hello", 80); got != "hello" {
		t.Errorf("trimErrMsg short = %q, want unchanged", got)
	}
	long := make([]byte, 100)
	for i := range long {
		long[i] = 'a'
	}
	got := trimErrMsg(string(long), 10)
	if got != "aaaaaaaaaa..." {
		t.Errorf("trimErrMsg long = %q, want 10 chars + ellipsis", got)
	}
}

func TestIsAuthError(t *testing.T) {
	for _, msg := range []string{"401 Unauthorized", "invalid api key", "Forbidden", "authentication failed"} {
		if !isAuthError(msg) {
			t.Errorf("isAuthError(%q) = false, want true", msg)
		}
	}
	for _, msg := range []string{"connection timed out", "dns lookup failed", "500 internal error"} {
		if isAuthError(msg) {
			t.Errorf("isAuthError(%q) = true, want false", msg)
		}
	}
}

func TestCheckEnvWarnings(t *testing.T) {
	t.Setenv("TRACKER_PASS_ENV", "")
	t.Setenv("TRACKER_PASS_API_KEYS", "")
	if got := checkEnvWarnings(); got.Status != CheckStatusOK {
		t.Errorf("checkEnvWarnings with no dangerous vars = %v, want ok", got.Status)
	}
	t.Setenv("TRACKER_PASS_ENV", "1")
	if got := checkEnvWarnings(); got.Status != CheckStatusWarn {
		t.Errorf("checkEnvWarnings with TRACKER_PASS_ENV=1 = %v, want warn", got.Status)
	}
}
