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
	vcsRev, vcsTime, vcsDirty := extractVCSSettings(info.Settings)
	applyModuleVersion(info.Main.Version)
	applyVCSCommit(vcsRev, vcsDirty)
	applyVCSDate(vcsTime)
}

// extractVCSSettings pulls vcs.revision, vcs.time, and vcs.modified from build settings.
func extractVCSSettings(settings []debug.BuildSetting) (rev, vcsTime, dirty string) {
	for _, s := range settings {
		switch s.Key {
		case "vcs.revision":
			rev = s.Value
		case "vcs.time":
			vcsTime = s.Value
		case "vcs.modified":
			dirty = s.Value
		}
	}
	return rev, vcsTime, dirty
}

// applyModuleVersion sets version from module metadata when ldflags didn't set it.
func applyModuleVersion(mainVersion string) {
	if version == "dev" && mainVersion != "" && mainVersion != "(devel)" {
		version = mainVersion
	}
}

// applyVCSCommit sets commit from VCS revision when ldflags didn't set it.
func applyVCSCommit(vcsRev, vcsDirty string) {
	if commit != "unknown" || vcsRev == "" {
		return
	}
	short := vcsRev
	if len(short) > 8 {
		short = short[:8]
	}
	if vcsDirty == "true" {
		short += "-dirty"
	}
	commit = short
}

// applyVCSDate sets date from VCS time when ldflags didn't set it.
func applyVCSDate(vcsTime string) {
	if date == "unknown" && vcsTime != "" {
		date = vcsTime
	}
}
