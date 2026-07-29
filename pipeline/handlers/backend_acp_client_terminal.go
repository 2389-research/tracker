// ABOUTME: Helpers extracted from backend_acp_client.go to keep CreateTerminal and
// ABOUTME: validatePathInWorkDir within the complexity ratchet (#449 sweep touched the file).
package handlers

import (
	"fmt"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"

	acp "github.com/coder/acp-go-sdk"
)

// hasParentSegment reports whether path contains a ".." component. Raw ".."
// is rejected before filepath.Clean can collapse it lexically and mask a
// symlink/../escape. Splits on both '/' and '\' to catch Windows-style paths
// on any platform. Extracted from validatePathInWorkDir for the ratchet.
func hasParentSegment(path string) bool {
	segments := strings.FieldsFunc(path, func(r rune) bool {
		return r == '/' || r == '\\'
	})
	for _, seg := range segments {
		if seg == ".." {
			return true
		}
	}
	return false
}

// resolveOrClean resolves symlinks for path, falling back to a lexical Clean
// when symlink resolution fails (e.g. the path does not exist yet).
func resolveOrClean(path string) string {
	resolved, err := resolvePathForValidation(path)
	if err != nil {
		return filepath.Clean(path)
	}
	return resolved
}

// denyTerminalCommand runs the tool-command denylist against a terminal
// request, returning a JSON-RPC error when the command or its full args match
// a denied pattern. Extracted from CreateTerminal for the complexity ratchet.
func denyTerminalCommand(p acp.CreateTerminalRequest) *acp.RequestError {
	// Check the bare command name first to catch "eval" / "exec" / "source"
	// with no args (the denylist patterns like "eval *" require a trailing
	// argument and would miss the bare invocation without this check).
	if denied, pattern := checkCommandDenylist(p.Command+" _", nil); denied {
		return &acp.RequestError{Code: -32602, Message: fmt.Sprintf("command matches denied pattern %q", pattern)}
	}
	// Also check the full command string with args for pipe-to-shell patterns.
	if len(p.Args) > 0 {
		fullCmd := strings.Join(append([]string{p.Command}, p.Args...), " ")
		if denied, pattern := checkCommandDenylist(fullCmd, nil); denied {
			return &acp.RequestError{Code: -32602, Message: fmt.Sprintf("command matches denied pattern %q", pattern)}
		}
	}
	return nil
}

// resolveTerminalCwd validates the request's cwd stays within the working
// directory and returns the effective cwd (the handler's working dir when the
// request omits one).
func (h *acpClientHandler) resolveTerminalCwd(p acp.CreateTerminalRequest) (string, *acp.RequestError) {
	cwd := h.workingDir
	if p.Cwd != nil && *p.Cwd != "" {
		if err := validatePathInWorkDir(*p.Cwd, h.workingDir); err != nil {
			return "", &acp.RequestError{Code: -32602, Message: err.Error()}
		}
		cwd = *p.Cwd
	}
	return cwd, nil
}

// buildTerminalCmd constructs the subprocess for a terminal request. Env
// matches the parent ACP agent process (full passthrough via buildEnvForACP)
// plus any request-supplied vars; a process group enables clean kill.
func buildTerminalCmd(cwd string, p acp.CreateTerminalRequest) *exec.Cmd {
	cmd := exec.Command(p.Command, p.Args...)
	cmd.Dir = cwd
	cmd.Env = buildEnvForACP()
	for _, ev := range p.Env {
		cmd.Env = append(cmd.Env, ev.Name+"="+ev.Value)
	}
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	return cmd
}

// registerTerminal records a running terminal under termID for later
// output/wait/kill lookups.
func (h *acpClientHandler) registerTerminal(termID string, ts *terminalState) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.terminals == nil {
		h.terminals = make(map[string]*terminalState)
	}
	h.terminals[termID] = ts
}
