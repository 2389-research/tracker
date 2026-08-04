// ABOUTME: Doctor check for optional binaries (git, claude CLI).
// ABOUTME: Split from tracker_doctor.go (#453) — behavior-preserving extraction.
package tracker

import (
	"context"
	"fmt"
	"os/exec"
	"strings"
	"time"
)

// checkOtherBinaries checks for git (recommended) and claude (required
// when backend == "claude-code", optional otherwise).
func checkOtherBinaries(ctx context.Context, backend string) CheckResult {
	out := CheckResult{Name: "Optional Binaries"}
	hasErr := false
	hasWarn := false
	if _, err := exec.LookPath("git"); err == nil {
		out.Details = append(out.Details, CheckDetail{
			Status:  CheckStatusOK,
			Message: "git found (recommended for pipeline versioning)",
		})
	} else {
		out.Details = append(out.Details, CheckDetail{
			Status:  CheckStatusWarn,
			Message: "git not found in PATH (recommended for pipeline versioning)",
		})
		hasWarn = true
	}
	claudePath, claudeErr := exec.LookPath("claude")
	if claudeErr == nil {
		claudeVer := getBinaryVersion(ctx, claudePath, "--version")
		out.Details = append(out.Details, CheckDetail{
			Status:  CheckStatusOK,
			Message: fmt.Sprintf("claude %s (for --backend claude-code)", claudeVer),
		})
	} else if backend == "claude-code" {
		out.Details = append(out.Details, CheckDetail{
			Status:  CheckStatusError,
			Message: "claude CLI not found in PATH (required for --backend claude-code)",
			Hint:    "install the Claude CLI from https://claude.ai/code",
		})
		hasErr = true
	} else {
		out.Details = append(out.Details, CheckDetail{
			Status:  CheckStatusWarn,
			Message: "claude not found in PATH (install for --backend claude-code support)",
		})
		hasWarn = true
	}
	switch {
	case hasErr:
		out.Status = CheckStatusError
		out.Message = "required binary missing for selected backend"
		out.Hint = "install the Claude CLI from https://claude.ai/code"
	case hasWarn:
		out.Status = CheckStatusWarn
		out.Message = "some optional binaries missing"
		out.Hint = "install git and/or the Claude CLI to unlock all tracker features"
	default:
		out.Status = CheckStatusOK
		out.Message = "optional binaries available"
	}
	return out
}

func getBinaryVersion(ctx context.Context, path, flag string) string {
	probeCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	out, err := exec.CommandContext(probeCtx, path, flag).CombinedOutput()
	if err != nil {
		return "(version unknown)"
	}
	lines := strings.SplitN(strings.TrimSpace(string(out)), "\n", 2)
	if len(lines) == 0 {
		return "(version unknown)"
	}
	return strings.TrimSpace(lines[0])
}
