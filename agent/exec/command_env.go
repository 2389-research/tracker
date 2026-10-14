// ABOUTME: Builds the environment for commands run on the model's behalf, without credential names or credential-bearing values.
// ABOUTME: Shared by the agent bash tool, verify commands, workflow tool nodes, ACP terminal commands and git subprocesses; TRACKER_PASS_ENV=1 opts out.
package exec

import (
	"os"
	"strings"
	"unicode"
)

// sensitiveEnvPatterns lists the name fragments that mark an environment
// variable as a credential. Matching is a case-insensitive substring test on
// the variable's name.
var sensitiveEnvPatterns = []string{
	"_API_KEY",
	"_SECRET",
	"_TOKEN",
	"_PASSWORD",
}

// CommandEnv returns the environment for a command Tracker runs on the model's
// behalf: this process's environment without credential-shaped variables
// (credential names and credential-bearing values, see HasSensitivePattern), or
// all of it when TRACKER_PASS_ENV=1. Provider keys sit in Tracker's own
// environment, and a model-written command must not be able to read them.
func CommandEnv() []string {
	env := os.Environ()
	if os.Getenv("TRACKER_PASS_ENV") == "1" {
		return env
	}
	return FilterSensitiveEnv(env)
}

// FilterSensitiveEnv returns a copy of env without the NAME=value entries that
// HasSensitivePattern reports as credentials. Git's indexed config pairs are
// kept whole: see rebuildGitConfigPairs.
func FilterSensitiveEnv(env []string) []string {
	filtered := make([]string, 0, len(env))
	for _, entry := range env {
		if !HasSensitivePattern(entry) {
			filtered = append(filtered, entry)
		}
	}
	return rebuildGitConfigPairs(env, filtered)
}

// sensitiveEnvNames lists whole names whose value is a credential that no
// name pattern catches. Matching is case-insensitive.
var sensitiveEnvNames = map[string]bool{
	"DOCKER_AUTH_CONFIG": true, // container-registry auths JSON (#679)
}

// proxyEnvNames are exempt from the value checks: an authenticated proxy URL
// is a credential, but stripping it cuts every command behind that proxy off
// the network. Claude Code's own subprocess scrub keeps them for the same
// reason. Name patterns still apply.
var proxyEnvNames = map[string]bool{
	"HTTP_PROXY":  true,
	"HTTPS_PROXY": true,
	"ALL_PROXY":   true,
	"FTP_PROXY":   true,
	"NO_PROXY":    true,
}

// HasSensitivePattern reports whether a NAME=value entry (or a bare name) is
// a credential: its name contains a credential pattern or is a known
// credential name, or (#679) its value carries a credential under any name —
// a URL with a password, an http(s) URL with any userinfo (a token used as
// the username), or a Docker auths JSON blob. Proxy variables skip the value
// checks. A bare name gets the name checks only.
func HasSensitivePattern(entry string) bool {
	rawName, value, hasValue := strings.Cut(entry, "=")
	name := strings.ToUpper(rawName)
	for _, pattern := range sensitiveEnvPatterns {
		if strings.Contains(name, pattern) {
			return true
		}
	}
	if sensitiveEnvNames[name] {
		return true
	}
	return hasValue && !proxyEnvNames[name] && hasCredentialValue(value)
}

// hasCredentialValue reports whether value holds a credential-bearing URL or
// a Docker auths blob.
func hasCredentialValue(value string) bool {
	trimmed := strings.TrimSpace(value)
	if strings.HasPrefix(trimmed, "{") && strings.Contains(trimmed, `"auths"`) {
		return true
	}
	for rest := value; ; {
		i := strings.Index(rest, "://")
		if i < 0 {
			return false
		}
		if urlHasCredential(schemeBefore(rest[:i]), rest[i+3:]) {
			return true
		}
		rest = rest[i+3:]
	}
}

// schemeBefore returns the URL scheme ending at the end of s, lower-cased:
// the trailing run of letters, digits, '+', '-' and '.'.
func schemeBefore(s string) string {
	start := strings.LastIndexFunc(s, func(r rune) bool { return !isSchemeRune(r) }) + 1
	return strings.ToLower(s[start:])
}

// isSchemeRune reports whether r may appear in a URL scheme (RFC 3986).
func isSchemeRune(r rune) bool {
	return unicode.IsLetter(r) && r < unicode.MaxASCII ||
		unicode.IsDigit(r) && r < unicode.MaxASCII ||
		strings.ContainsRune("+-.", r)
}

// urlHasCredential reports whether the URL after "scheme://" carries a
// credential in its userinfo: a password for any scheme, or any userinfo at
// all for http(s), where a bare username is usually a token. An ssh-style
// "git@host" (no password) is not a credential.
func urlHasCredential(scheme, afterScheme string) bool {
	// Stop only at what ends an authority (or the value's text): RFC 3986
	// allows sub-delims such as , ; ' in userinfo, so they must not end it
	// early. A comma-separated list of URLs is still covered, because each
	// "://" is checked on its own.
	authority := afterScheme
	if end := strings.IndexAny(authority, "/?# \t\n\""); end >= 0 {
		authority = authority[:end]
	}
	at := strings.LastIndex(authority, "@")
	if at < 0 {
		return false
	}
	userinfo := authority[:at]
	if scheme == "http" || scheme == "https" {
		return userinfo != ""
	}
	_, password, _ := strings.Cut(userinfo, ":")
	return password != ""
}
