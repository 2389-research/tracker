// ABOUTME: Reads embedded VCS build info so `go install` builds show commit + date.
// ABOUTME: Only fills in values that are still at their default ("unknown").
package main

import "runtime/debug"

// initVersionFromVCS populates commit and date from Go's embedded VCS
// metadata (available when built from a module with git). Ldflags values
// from goreleaser take precedence since they're set before init runs.
func initVersionFromVCS() {
	info, ok := debug.ReadBuildInfo()
	if !ok {
		return
	}
	var vcsRev, vcsTime, vcsDirty string
	for _, s := range info.Settings {
		switch s.Key {
		case "vcs.revision":
			vcsRev = s.Value
		case "vcs.time":
			vcsTime = s.Value
		case "vcs.modified":
			vcsDirty = s.Value
		}
	}
	// Module version is available from `go install ...@vX.Y.Z` even though
	// VCS metadata is stripped. Use it when ldflags didn't set version.
	if version == "dev" && info.Main.Version != "" && info.Main.Version != "(devel)" {
		version = info.Main.Version
	}

	if commit == "unknown" && vcsRev != "" {
		short := vcsRev
		if len(short) > 8 {
			short = short[:8]
		}
		if vcsDirty == "true" {
			short += "-dirty"
		}
		commit = short
	}
	if date == "unknown" && vcsTime != "" {
		date = vcsTime
	}
}
