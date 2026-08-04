// ABOUTME: Doctor checks for the dippin binary and dippin<->go.mod version compatibility.
// ABOUTME: Split from tracker_doctor.go (#453) — behavior-preserving extraction.
package tracker

import (
	"context"
	"fmt"
	"os/exec"
	"regexp"
	"strings"
	"time"
)

// checkDippin verifies the dippin binary is installed. The full "dippin
// <ver> at <path>" string goes into the details so the CLI can print a
// per-item line; the composite summary carries the shorter "dippin <ver>"
// form. Historically the CLI emits both lines.
func checkDippin(ctx context.Context) CheckResult {
	path, err := exec.LookPath("dippin")
	if err != nil {
		return CheckResult{
			Name:    "Dippin Language",
			Status:  CheckStatusError,
			Message: "dippin binary not found in PATH",
			Hint:    "install from https://github.com/2389-research/dippin-lang  (required for pipeline linting)",
		}
	}
	ver := getDippinVersion(ctx, path)
	return CheckResult{
		Name:   "Dippin Language",
		Status: CheckStatusOK,
		Details: []CheckDetail{{
			Status:  CheckStatusOK,
			Message: fmt.Sprintf("dippin %s at %s", ver, path),
		}},
		Message: fmt.Sprintf("dippin %s", ver),
	}
}

func getDippinVersion(ctx context.Context, path string) string {
	probeCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	out, err := exec.CommandContext(probeCtx, path, "--version").CombinedOutput()
	if err != nil {
		out, err = exec.CommandContext(probeCtx, path, "version").CombinedOutput()
		if err != nil {
			return "(version unknown)"
		}
	}
	ver := strings.TrimSpace(string(out))
	ver = strings.TrimPrefix(ver, "dippin ")
	ver = strings.TrimPrefix(ver, "version ")
	if ver == "" {
		return "(version unknown)"
	}
	return ver
}

// checkVersionCompat verifies the installed dippin version matches the
// go.mod pin (on major and minor). trackerVersion / trackerCommit, when
// non-empty, are surfaced as a detail line.
func checkVersionCompat(ctx context.Context, trackerVersion, trackerCommit string) CheckResult {
	out := CheckResult{Name: "Version Compatibility"}
	if trackerVersion != "" {
		out.Details = append(out.Details, CheckDetail{Status: CheckStatusOK, Message: trackerVersionLine(trackerVersion, trackerCommit)})
	}
	dippinPath, err := exec.LookPath("dippin")
	if err != nil {
		return versionCompatNoDippin(out, trackerVersion)
	}
	cliVer := getDippinVersion(ctx, dippinPath)
	out.Details = append(out.Details, CheckDetail{
		Status:  CheckStatusOK,
		Message: fmt.Sprintf("dippin    %s (installed) / %s (go.mod pin)", cliVer, PinnedDippinVersion),
	})

	if mismatch, msg := checkDippinVersionMismatch(cliVer, PinnedDippinVersion); mismatch {
		return versionCompatMismatch(out, cliVer, trackerVersion, msg)
	}
	out.Status = CheckStatusOK
	if trackerVersion != "" {
		out.Message = fmt.Sprintf("tracker %s / dippin %s", trackerVersion, cliVer)
	} else {
		out.Message = fmt.Sprintf("dippin %s", cliVer)
	}
	return out
}

// trackerVersionLine formats the leading "tracker <ver> (commit <c>)" detail.
func trackerVersionLine(version, commit string) string {
	if commit != "" {
		return fmt.Sprintf("tracker   %s (commit %s)", version, commit)
	}
	return fmt.Sprintf("tracker   %s", version)
}

// versionCompatNoDippin builds the warn result when dippin isn't on PATH.
func versionCompatNoDippin(out CheckResult, trackerVersion string) CheckResult {
	out.Details = append(out.Details, CheckDetail{
		Status:  CheckStatusWarn,
		Message: "dippin not found — skipping version compatibility check",
	})
	out.Status = CheckStatusWarn
	if trackerVersion != "" {
		out.Message = fmt.Sprintf("tracker %s / dippin not found", trackerVersion)
	} else {
		out.Message = "dippin not found"
	}
	return out
}

// versionCompatMismatch builds the warn result when the installed dippin
// diverges from the go.mod pin on major/minor.
func versionCompatMismatch(out CheckResult, cliVer, trackerVersion, msg string) CheckResult {
	out.Details = append(out.Details, CheckDetail{
		Status:  CheckStatusWarn,
		Message: fmt.Sprintf("dippin version mismatch: %s", msg),
	})
	out.Status = CheckStatusWarn
	if trackerVersion != "" {
		out.Message = fmt.Sprintf("tracker %s / dippin %s (mismatched — expected %s)", trackerVersion, cliVer, PinnedDippinVersion)
	} else {
		out.Message = fmt.Sprintf("dippin %s (mismatched — expected %s)", cliVer, PinnedDippinVersion)
	}
	out.Hint = fmt.Sprintf("install dippin %s to match the go.mod pin", PinnedDippinVersion)
	return out
}

// checkDippinVersionMismatch returns (true, reason) if the installed CLI version
// diverges from the pinned version on major or minor components.
func checkDippinVersionMismatch(cliVer, pinned string) (bool, string) {
	cliMajor, cliMinor, ok1 := parseVersionMajorMinor(cliVer)
	pinMajor, pinMinor, ok2 := parseVersionMajorMinor(pinned)
	if !ok1 || !ok2 {
		return false, ""
	}
	if cliMajor != pinMajor {
		return true, fmt.Sprintf("installed major v%d != pinned major v%d", cliMajor, pinMajor)
	}
	if cliMinor != pinMinor {
		return true, fmt.Sprintf("installed v%d.%d != pinned v%d.%d", cliMajor, cliMinor, pinMajor, pinMinor)
	}
	return false, ""
}

var semverRe = regexp.MustCompile(`v?(\d+)\.(\d+)`)

func parseVersionMajorMinor(ver string) (major, minor int, ok bool) {
	m := semverRe.FindStringSubmatch(ver)
	if m == nil {
		return 0, 0, false
	}
	fmt.Sscanf(m[1], "%d", &major)
	fmt.Sscanf(m[2], "%d", &minor)
	return major, minor, true
}
