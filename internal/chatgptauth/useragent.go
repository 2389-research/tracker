// ABOUTME: Builds the "tracker/<version> (<GOOS>; <GOARCH>)" User-Agent string every chatgptauth
// ABOUTME: request sends, deriving <version> from the running binary's embedded build info.
package chatgptauth

import (
	"fmt"
	"runtime"
	"runtime/debug"
)

// modulePath is tracker's own module path (go.mod), used to tell "tracker
// is the main module" apart from "tracker is a dependency" when picking a
// version out of debug.ReadBuildInfo's Main vs Deps.
const modulePath = "github.com/2389-research/tracker"

// UserAgent returns the User-Agent header tracker's ChatGPT sign-in and
// refresh requests send: "tracker/<version> (<GOOS>; <GOARCH>)".
func UserAgent() string {
	return fmt.Sprintf("tracker/%s (%s; %s)", buildVersion(), runtime.GOOS, runtime.GOARCH)
}

// buildVersion resolves tracker's version from the running binary's
// embedded build info: Main.Version when tracker itself is the main
// module, else tracker's own entry in Deps (when tracker is embedded as a
// library in another binary), else "dev". A Go "(devel)" pseudo-version,
// or no build info at all, also becomes "dev".
func buildVersion() string {
	info, ok := debug.ReadBuildInfo()
	if !ok {
		return "dev"
	}
	if info.Main.Path == modulePath {
		return normalizeVersion(info.Main.Version)
	}
	for _, dep := range info.Deps {
		if dep.Path == modulePath {
			return normalizeVersion(dep.Version)
		}
	}
	return "dev"
}

// normalizeVersion maps the empty string and Go's "(devel)" placeholder to
// "dev"; any other value passes through unchanged.
func normalizeVersion(v string) string {
	if v == "" || v == "(devel)" {
		return "dev"
	}
	return v
}
