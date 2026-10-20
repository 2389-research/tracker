// ABOUTME: Doctor check for optional binaries (git, claude CLI).
// ABOUTME: Split from tracker_doctor.go (#453) — behavior-preserving extraction.
package tracker

import (
	"context"
	"fmt"
	"os/exec"
	"strings"
	"time"

	"github.com/2389-research/tracker/pipeline"
)

// checkOtherBinaries checks for git (recommended) and claude (required
// when backend == "claude-code", optional otherwise).
func checkOtherBinaries(needs pipeline.BackendNeeds, claude doctorClaude) CheckResult {
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
	_, claudeErr := exec.LookPath("claude")
	if claude.ok {
		out.Details = append(out.Details, CheckDetail{
			Status:  CheckStatusOK,
			Message: fmt.Sprintf("claude %s at %s (from %s; for claude-code and backend: auto)", claude.version, claude.path, claude.source),
		})
		hasWarn = appendClaudeDuplicates(&out, claude) || hasWarn
	} else if claudeErr == nil {
		out.Details = append(out.Details, CheckDetail{
			Status:  CheckStatusWarn,
			Message: "claude is on PATH but did not answer `claude --version` (or is inside the working directory, which backend: auto refuses)",
		})
		hasWarn = true
	} else if needs.ClaudeCode {
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

// getBinaryVersion runs `path flag` with env and returns the first output
// line. env is the environment the binary's real launch gets, so the probe
// never hands it more than that (#660).
func getBinaryVersion(ctx context.Context, path, flag string, env []string) string {
	probeCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	cmd := exec.CommandContext(probeCtx, path, flag)
	cmd.Env = env
	out, err := cmd.CombinedOutput()
	if err != nil {
		return "(version unknown)"
	}
	lines := strings.SplitN(strings.TrimSpace(string(out)), "\n", 2)
	if len(lines) == 0 {
		return "(version unknown)"
	}
	return strings.TrimSpace(lines[0])
}

// appendClaudeDuplicates warns when several claude binaries are on PATH:
// the first one runs, and a stale one ahead of the current one fails every
// node with errors that don't point at the binary (#719).
func appendClaudeDuplicates(out *CheckResult, claude doctorClaude) bool {
	if len(claude.onPath) < 2 {
		return false
	}
	out.Details = append(out.Details, CheckDetail{
		Status:  CheckStatusWarn,
		Message: fmt.Sprintf("%d claude binaries on PATH — the first one runs:", len(claude.onPath)),
		Hint:    "set TRACKER_CLAUDE_BIN=/absolute/path/to/claude to choose one",
	})
	for _, f := range claude.onPath {
		v := f.Version
		if f.Err != nil {
			v = "no answer to --version"
		}
		out.Details = append(out.Details, CheckDetail{Status: CheckStatusWarn, Message: fmt.Sprintf("  %s: %s", f.Path, v)})
	}
	return true
}
