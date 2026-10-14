// ABOUTME: Keeps git's indexed GIT_CONFIG_COUNT/KEY_<i>/VALUE_<i> pairs whole when the
// ABOUTME: credential filter strips a value, so git doesn't refuse to run (#679).
package exec

import (
	"fmt"
	"strconv"
	"strings"
)

const (
	gitConfigCount    = "GIT_CONFIG_COUNT"
	gitConfigKeyPfx   = "GIT_CONFIG_KEY_"
	gitConfigValuePfx = "GIT_CONFIG_VALUE_"
)

// rebuildGitConfigPairs repairs git's indexed config pairs after filtering.
// Git reads GIT_CONFIG_COUNT pairs of GIT_CONFIG_KEY_<i> / GIT_CONFIG_VALUE_<i>
// and refuses to run when one is missing, so stripping only a credential
// value (say a remote URL carrying a token) would break every git command
// that gets the filtered environment. When the filter dropped part of a pair,
// the pair goes entirely and the surviving pairs are renumbered from 0; when
// it dropped nothing, filtered is returned as is.
func rebuildGitConfigPairs(orig, filtered []string) []string {
	pairs, ok := gitConfigPairs(orig)
	if !ok {
		return filtered
	}
	kept := nonCredentialPairs(pairs)
	if len(kept) == len(pairs) {
		return filtered
	}
	return withGitConfigPairs(filtered, kept)
}

// nonCredentialPairs returns the pairs whose value the filter would keep.
func nonCredentialPairs(pairs [][2]string) [][2]string {
	kept := make([][2]string, 0, len(pairs))
	for _, p := range pairs {
		if !HasSensitivePattern(gitConfigValuePfx + "0=" + p[1]) {
			kept = append(kept, p)
		}
	}
	return kept
}

// withGitConfigPairs replaces every GIT_CONFIG_COUNT/KEY/VALUE variable in env
// with pairs, numbered from 0.
func withGitConfigPairs(env []string, pairs [][2]string) []string {
	out := make([]string, 0, len(env)+1+2*len(pairs))
	for _, entry := range env {
		if !isGitConfigPairVar(entry) {
			out = append(out, entry)
		}
	}
	out = append(out, fmt.Sprintf("%s=%d", gitConfigCount, len(pairs)))
	for i, p := range pairs {
		out = append(out, fmt.Sprintf("%s%d=%s", gitConfigKeyPfx, i, p[0]), fmt.Sprintf("%s%d=%s", gitConfigValuePfx, i, p[1]))
	}
	return out
}

// gitConfigPairs returns the complete key/value pairs GIT_CONFIG_COUNT
// declares in env, and false when env declares none or a pair is incomplete
// (git would refuse that environment anyway, so it is left alone).
func gitConfigPairs(env []string) ([][2]string, bool) {
	vars := envMap(env)
	count, err := strconv.Atoi(vars[gitConfigCount])
	if err != nil || count <= 0 {
		return nil, false
	}
	pairs := make([][2]string, count)
	for i := range pairs {
		key, okKey := vars[gitConfigKeyPfx+strconv.Itoa(i)]
		value, okValue := vars[gitConfigValuePfx+strconv.Itoa(i)]
		if !okKey || !okValue {
			return nil, false
		}
		pairs[i] = [2]string{key, value}
	}
	return pairs, true
}

// envMap indexes NAME=value entries by name.
func envMap(env []string) map[string]string {
	vars := make(map[string]string, len(env))
	for _, entry := range env {
		if name, value, found := strings.Cut(entry, "="); found {
			vars[name] = value
		}
	}
	return vars
}

// isGitConfigPairVar reports whether entry is GIT_CONFIG_COUNT or one of its
// indexed key/value variables.
func isGitConfigPairVar(entry string) bool {
	name, _, _ := strings.Cut(entry, "=")
	return name == gitConfigCount || strings.HasPrefix(name, gitConfigKeyPfx) || strings.HasPrefix(name, gitConfigValuePfx)
}
