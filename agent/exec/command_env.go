// ABOUTME: Builds the environment for commands run on the model's behalf, with credential-shaped variables removed.
// ABOUTME: Shared by the agent bash tool, verify commands, workflow tool nodes and git subprocesses; TRACKER_PASS_ENV=1 opts out.
package exec

import (
	"os"
	"strings"
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
// behalf: this process's environment without credential-shaped variables, or
// all of it when TRACKER_PASS_ENV=1. Provider keys sit in Tracker's own
// environment, and a model-written command must not be able to read them.
func CommandEnv() []string {
	env := os.Environ()
	if os.Getenv("TRACKER_PASS_ENV") == "1" {
		return env
	}
	return FilterSensitiveEnv(env)
}

// FilterSensitiveEnv returns a copy of env without the NAME=value entries
// whose names match a credential pattern.
func FilterSensitiveEnv(env []string) []string {
	filtered := make([]string, 0, len(env))
	for _, entry := range env {
		if !HasSensitivePattern(entry) {
			filtered = append(filtered, entry)
		}
	}
	return filtered
}

// HasSensitivePattern reports whether the name in a NAME=value entry (or a
// bare name) matches a credential pattern.
func HasSensitivePattern(entry string) bool {
	name := strings.ToUpper(strings.SplitN(entry, "=", 2)[0])
	for _, pattern := range sensitiveEnvPatterns {
		if strings.Contains(name, pattern) {
			return true
		}
	}
	return false
}
