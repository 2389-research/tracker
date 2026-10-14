// ABOUTME: Tests the credential filter shared by every command Tracker runs on the model's behalf:
// ABOUTME: name patterns, DOCKER_AUTH_CONFIG, and credential-bearing values (#679), with proxies exempt.
package exec

import (
	"slices"
	"strings"
	"testing"
)

func TestHasSensitivePattern(t *testing.T) {
	cases := []struct {
		entry string
		want  bool
	}{
		// Name patterns (unchanged).
		{"ANTHROPIC_API_KEY=sk-ant-x", true},
		{"GITHUB_TOKEN=ghp_x", true},
		{"aws_secret_access_key=x", true},
		{"DB_PASSWORD=x", true},
		{"GITHUB_TOKEN", true}, // bare name

		// #679: a name no pattern catches.
		{"DOCKER_AUTH_CONFIG={}", true},
		{"docker_auth_config", true},

		// #679: a URL carrying a password, whatever the variable is called.
		{"DATABASE_URL=postgres://app:s3cret@db:5432/app", true},
		{"PIP_INDEX_URL=https://user:pw@pypi.example.com/simple", true},
		{"NPM_CONFIG_REGISTRY=https://u:p@npm.example.com/", true},
		{"REDIS_URL=redis://:pw@cache:6379/0", true},
		{"PIP_EXTRA_INDEX_URL=https://pypi.org/simple https://u:pw@private.example.com/simple", true},
		// An http(s) userinfo with no password is a token used as the username.
		{"REPO_URL=https://ghp_abc123@github.com/org/repo.git", true},
		{"REPO_URL=https://ghp_abc123:@github.com/org/repo.git", true}, // empty password, same token
		// RFC 3986 sub-delims are valid in userinfo and must not end the scan.
		{"DATABASE_URL=postgres://app:pa;ss@db/app", true},
		{"DATABASE_URL=postgres://app:pa,ss@db/app", true},
		{"DATABASE_URL=postgres://app:pa'ss@db/app", true},
		{"DATABASE_URL=postgres://app:pa!$&()*+=ss@db/app", true},
		// A comma-separated list still has each URL checked.
		{"MIRRORS=https://a.example.com/x,https://u:pw@b.example.com/y", true},
		// A Docker auths blob under any name.
		{`REGISTRY_CREDS={"auths":{"ghcr.io":{"auth":"dXNlcjpwdw=="}}}`, true},

		// Not credentials.
		{"PATH=/usr/bin:/bin", false},
		{"HOME=/home/me", false},
		{"DATABASE_URL=postgres://db:5432/app", false},
		{"PIP_INDEX_URL=https://pypi.org/simple", false},
		{"GIT_REMOTE=ssh://git@github.com/org/repo.git", false}, // ssh user, no secret
		{"MAILTO=ops@example.com", false},
		{"NOTE=see https://example.com/a@b for details", false}, // '@' in the path, not userinfo
		{"GOOGLE_APPLICATION_CREDENTIALS=/etc/gcp/key.json", false},
		{`JSON_CONFIG={"auth":"none"}`, false},

		// Proxies stay: commands behind an authenticated proxy keep network access.
		{"HTTPS_PROXY=http://user:pw@proxy:3128", false},
		{"http_proxy=http://user:pw@proxy:3128", false},
		{"ALL_PROXY=socks5://u:p@proxy:1080", false},
		{"NO_PROXY=localhost", false},
	}
	for _, c := range cases {
		if got := HasSensitivePattern(c.entry); got != c.want {
			t.Errorf("HasSensitivePattern(%q) = %v, want %v", c.entry, got, c.want)
		}
	}
}

func TestFilterSensitiveEnv_StripsCredentialValues(t *testing.T) {
	env := []string{
		"PATH=/usr/bin",
		"DATABASE_URL=postgres://app:s3cret@db/app",
		"DOCKER_AUTH_CONFIG={\"auths\":{}}",
		"HTTPS_PROXY=http://u:p@proxy:3128",
	}
	got := FilterSensitiveEnv(env)
	want := []string{"PATH=/usr/bin", "HTTPS_PROXY=http://u:p@proxy:3128"}
	if !slices.Equal(got, want) {
		t.Errorf("FilterSensitiveEnv = %q, want %q", got, want)
	}
}

func TestCommandEnv_PassEnvKeepsCredentialValues(t *testing.T) {
	t.Setenv("DATABASE_URL", "postgres://app:s3cret@db/app")
	t.Setenv("TRACKER_PASS_ENV", "1")
	if !slices.Contains(CommandEnv(), "DATABASE_URL=postgres://app:s3cret@db/app") {
		t.Error("TRACKER_PASS_ENV=1 must pass a credential-bearing value through")
	}
	t.Setenv("TRACKER_PASS_ENV", "")
	if slices.Contains(CommandEnv(), "DATABASE_URL=postgres://app:s3cret@db/app") {
		t.Error("CommandEnv leaked a URL carrying a password")
	}
}

// FuzzHasSensitivePattern checks the filter's invariants on arbitrary input:
// it never panics, a credential name is stripped whatever its value (the
// value checks and the proxy exemption only ever add to the name checks),
// and a URL with a password is stripped under any non-proxy name.
func FuzzHasSensitivePattern(f *testing.F) {
	for _, seed := range []string{"", "x", "://", "a://@", "postgres://u:p@h", "{\"auths\":", "https://t@h/p@q"} {
		f.Add(seed)
	}
	f.Fuzz(func(t *testing.T, value string) {
		if !HasSensitivePattern("SERVICE_TOKEN=" + value) {
			t.Fatalf("a *_TOKEN name must be stripped whatever its value: %q", value)
		}
		if !HasSensitivePattern("DOCKER_AUTH_CONFIG=" + value) {
			t.Fatalf("DOCKER_AUTH_CONFIG must be stripped whatever its value: %q", value)
		}
		_ = HasSensitivePattern("HTTPS_PROXY=" + value) // must not panic
		if strings.ContainsAny(value, "/?#@ \t\n,;\"'") {
			return // value would end the userinfo or authority early
		}
		if !HasSensitivePattern("SOME_URL=postgres://u:" + value + "x@db/app") {
			t.Fatalf("URL with password %q not stripped", value+"x")
		}
	})
}

// Git reads GIT_CONFIG_COUNT/KEY_<i>/VALUE_<i> as complete pairs and refuses
// to run when one is missing, so a stripped credential value takes its key
// with it and the remaining pairs are renumbered.
func TestFilterSensitiveEnv_RebuildsGitConfigPairs(t *testing.T) {
	env := []string{
		"PATH=/usr/bin",
		"GIT_CONFIG_COUNT=3",
		"GIT_CONFIG_KEY_0=user.name",
		"GIT_CONFIG_VALUE_0=bot",
		"GIT_CONFIG_KEY_1=remote.origin.url",
		"GIT_CONFIG_VALUE_1=https://ghp_abc@github.com/org/repo",
		"GIT_CONFIG_KEY_2=core.editor",
		"GIT_CONFIG_VALUE_2=true",
	}
	got := FilterSensitiveEnv(env)
	want := []string{
		"PATH=/usr/bin",
		"GIT_CONFIG_COUNT=2",
		"GIT_CONFIG_KEY_0=user.name",
		"GIT_CONFIG_VALUE_0=bot",
		"GIT_CONFIG_KEY_1=core.editor",
		"GIT_CONFIG_VALUE_1=true",
	}
	if !slices.Equal(got, want) {
		t.Errorf("FilterSensitiveEnv =\n%q\nwant\n%q", got, want)
	}
}

// With nothing to strip, the pairs pass through untouched and in place.
func TestFilterSensitiveEnv_LeavesCleanGitConfigPairs(t *testing.T) {
	env := []string{"GIT_CONFIG_COUNT=1", "GIT_CONFIG_KEY_0=user.name", "GIT_CONFIG_VALUE_0=bot", "HOME=/h"}
	if got := FilterSensitiveEnv(env); !slices.Equal(got, env) {
		t.Errorf("FilterSensitiveEnv = %q, want unchanged %q", got, env)
	}
}
