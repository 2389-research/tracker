// ABOUTME: ACP client handler implementing the acp.Client interface for headless pipeline use.
// ABOUTME: Translates ACP session updates into agent.Event objects and handles file/terminal/permission requests.
package handlers

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"time"

	acp "github.com/coder/acp-go-sdk"

	"github.com/2389-research/tracker/agent"
)

// acpClientHandler implements acp.Client, translating ACP session updates into
// agent.Event objects via the emit callback and handling agent requests for
// file operations, terminal commands, and permission approval.
type acpClientHandler struct {
	emit       func(agent.Event)
	workingDir string

	mu        sync.Mutex
	terminals map[string]*terminalState
	textParts []string
	toolNames map[string]string // toolCallId → title
	toolCount int
	turnCount int
}

// terminalState tracks a running subprocess created via CreateTerminal.
type terminalState struct {
	cmd    *exec.Cmd
	output syncBuffer
	done   chan struct{}
	err    error
}

// syncBuffer is a goroutine-safe bytes.Buffer for subprocess output.
// The subprocess writes via cmd.Stdout/Stderr, while TerminalOutput reads
// concurrently — both paths go through the embedded mutex.
type syncBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (sb *syncBuffer) Write(p []byte) (int, error) {
	sb.mu.Lock()
	defer sb.mu.Unlock()
	return sb.buf.Write(p)
}

func (sb *syncBuffer) String() string {
	sb.mu.Lock()
	defer sb.mu.Unlock()
	return sb.buf.String()
}

// SessionUpdate receives real-time updates from the ACP agent during prompt
// processing. Each update variant maps to one or more agent.Event emissions.
func (h *acpClientHandler) SessionUpdate(_ context.Context, n acp.SessionNotification) error {
	now := time.Now()
	u := n.Update

	switch {
	case u.AgentMessageChunk != nil:
		h.handleMessageChunk(u.AgentMessageChunk, now)
	case u.AgentThoughtChunk != nil:
		h.handleThoughtChunk(u.AgentThoughtChunk, now)
	case u.ToolCall != nil:
		h.handleToolCallStart(u.ToolCall, now)
	case u.ToolCallUpdate != nil:
		h.handleToolCallUpdate(u.ToolCallUpdate, now)
	case u.Plan != nil:
		// Plan updates are informational — no agent.Event equivalent.
	default:
		// AvailableCommandsUpdate, CurrentModeUpdate, UserMessageChunk — no mapping needed.
	}
	return nil
}

// handleMessageChunk processes an agent message text chunk.
func (h *acpClientHandler) handleMessageChunk(chunk *acp.SessionUpdateAgentMessageChunk, now time.Time) {
	text := extractContentText(chunk.Content)
	if text == "" {
		return
	}
	h.mu.Lock()
	h.textParts = append(h.textParts, text)
	h.mu.Unlock()
	h.safeEmit(agent.Event{Type: agent.EventTextDelta, Timestamp: now, Text: text})
}

// handleThoughtChunk processes an agent reasoning/thought chunk.
func (h *acpClientHandler) handleThoughtChunk(chunk *acp.SessionUpdateAgentThoughtChunk, now time.Time) {
	text := extractContentText(chunk.Content)
	if text != "" {
		h.safeEmit(agent.Event{Type: agent.EventLLMReasoning, Timestamp: now, Text: text})
	}
}

// handleToolCallStart processes a new tool call notification.
func (h *acpClientHandler) handleToolCallStart(tc *acp.SessionUpdateToolCall, now time.Time) {
	h.mu.Lock()
	h.toolNames[string(tc.ToolCallId)] = tc.Title
	h.toolCount++
	h.mu.Unlock()
	h.safeEmit(agent.Event{
		Type:      agent.EventToolCallStart,
		Timestamp: now,
		ToolName:  tc.Title,
		ToolInput: formatRawInput(tc.RawInput),
	})
}

// handleToolCallUpdate processes a tool call status update.
func (h *acpClientHandler) handleToolCallUpdate(tc *acp.SessionToolCallUpdate, now time.Time) {
	if tc.Status == nil {
		return
	}
	status := *tc.Status
	if status != acp.ToolCallStatusCompleted && status != acp.ToolCallStatusFailed {
		return
	}
	h.mu.Lock()
	name := h.toolNames[string(tc.ToolCallId)]
	h.turnCount++ // each tool round-trip counts as a turn
	h.mu.Unlock()

	evt := agent.Event{Type: agent.EventToolCallEnd, Timestamp: now, ToolName: name}
	output := extractToolCallOutput(tc.Content, tc.RawOutput)
	if status == acp.ToolCallStatusFailed {
		evt.ToolError = output
	} else {
		evt.ToolOutput = output
	}
	h.safeEmit(evt)
}

// RequestPermission auto-approves all permission requests by selecting the
// first non-reject option. This matches PermissionBypassPermissions behavior.
func (h *acpClientHandler) RequestPermission(_ context.Context, p acp.RequestPermissionRequest) (acp.RequestPermissionResponse, error) {
	// Prefer allow_once or allow_always; fall back to first option.
	var fallback acp.PermissionOptionId
	for _, opt := range p.Options {
		if fallback == "" {
			fallback = opt.OptionId
		}
		if opt.Kind == acp.PermissionOptionKindAllowOnce || opt.Kind == acp.PermissionOptionKindAllowAlways {
			return acp.RequestPermissionResponse{
				Outcome: acp.RequestPermissionOutcome{
					Selected: &acp.RequestPermissionOutcomeSelected{
						OptionId: opt.OptionId,
						Outcome:  "selected",
					},
				},
			}, nil
		}
	}
	if fallback != "" {
		return acp.RequestPermissionResponse{
			Outcome: acp.RequestPermissionOutcome{
				Selected: &acp.RequestPermissionOutcomeSelected{
					OptionId: fallback,
					Outcome:  "selected",
				},
			},
		}, nil
	}
	return acp.RequestPermissionResponse{}, fmt.Errorf("no permission options provided")
}

// ReadTextFile reads a file from the local filesystem.
// Paths must be absolute and within the working directory.
func (h *acpClientHandler) ReadTextFile(_ context.Context, p acp.ReadTextFileRequest) (acp.ReadTextFileResponse, error) {
	if !filepath.IsAbs(p.Path) {
		return acp.ReadTextFileResponse{}, &acp.RequestError{Code: -32602, Message: fmt.Sprintf("path must be absolute: %q", p.Path)}
	}
	if err := validatePathInWorkDir(p.Path, h.workingDir); err != nil {
		return acp.ReadTextFileResponse{}, &acp.RequestError{Code: -32602, Message: err.Error()}
	}
	data, err := os.ReadFile(p.Path)
	if err != nil {
		return acp.ReadTextFileResponse{}, &acp.RequestError{Code: -32603, Message: err.Error()}
	}
	content := applyLineFilter(string(data), p.Line, p.Limit)
	return acp.ReadTextFileResponse{Content: content}, nil
}

// validatePathInWorkDir ensures the given absolute path is under the working directory.
// Resolves symlinks to prevent escaping the sandbox via symlink chains.
func validatePathInWorkDir(path, workDir string) error {
	if workDir == "" {
		return nil // no restriction if working dir is unset
	}
	resolved, err := resolvePathForValidation(path)
	if err != nil {
		resolved = filepath.Clean(path) // fall back to Clean if symlink resolution fails
	}
	dir := filepath.Clean(workDir)
	if !strings.HasPrefix(resolved, dir+string(filepath.Separator)) && resolved != dir {
		return fmt.Errorf("path %q is outside working directory %q", path, workDir)
	}
	return nil
}

// resolvePathForValidation resolves symlinks for the longest existing prefix of path.
func resolvePathForValidation(path string) (string, error) {
	resolved, err := filepath.EvalSymlinks(path)
	if err == nil {
		return resolved, nil
	}
	// Path may not exist yet (WriteTextFile). Resolve the parent.
	parent := filepath.Dir(path)
	resolvedParent, err := filepath.EvalSymlinks(parent)
	if err != nil {
		return "", err
	}
	return filepath.Join(resolvedParent, filepath.Base(path)), nil
}

// applyLineFilter slices a file's content by optional 1-based start line and limit.
func applyLineFilter(content string, line, limit *int) string {
	if line == nil && limit == nil {
		return content
	}
	lines := strings.Split(content, "\n")
	start, end := resolveLineRange(len(lines), line, limit)
	return strings.Join(lines[start:end], "\n")
}

// resolveLineRange computes safe start/end indexes for line slicing.
func resolveLineRange(total int, line, limit *int) (int, int) {
	start := clampInt(derefLineStart(line), 0, total)
	end := total
	if limit != nil && *limit > 0 {
		end = clampInt(start+*limit, start, total)
	}
	return start, end
}

// derefLineStart converts a 1-based line pointer to a 0-based index.
func derefLineStart(line *int) int {
	if line == nil || *line <= 1 {
		return 0
	}
	return *line - 1
}

// clampInt restricts v to the range [lo, hi].
func clampInt(v, lo, hi int) int {
	if v < lo {
		return lo
	}
	if v > hi {
		return hi
	}
	return v
}

// WriteTextFile writes content to a file on the local filesystem.
// Paths must be absolute and within the working directory.
func (h *acpClientHandler) WriteTextFile(_ context.Context, p acp.WriteTextFileRequest) (acp.WriteTextFileResponse, error) {
	if !filepath.IsAbs(p.Path) {
		return acp.WriteTextFileResponse{}, &acp.RequestError{Code: -32602, Message: fmt.Sprintf("path must be absolute: %q", p.Path)}
	}
	if err := validatePathInWorkDir(p.Path, h.workingDir); err != nil {
		return acp.WriteTextFileResponse{}, &acp.RequestError{Code: -32602, Message: err.Error()}
	}
	// Ensure parent directory exists.
	dir := filepath.Dir(p.Path)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return acp.WriteTextFileResponse{}, &acp.RequestError{Code: -32603, Message: err.Error()}
	}
	if err := os.WriteFile(p.Path, []byte(p.Content), 0644); err != nil {
		return acp.WriteTextFileResponse{}, &acp.RequestError{Code: -32603, Message: err.Error()}
	}
	return acp.WriteTextFileResponse{}, nil
}

// CreateTerminal spawns a subprocess and tracks it for future output/wait/kill.
func (h *acpClientHandler) CreateTerminal(_ context.Context, p acp.CreateTerminalRequest) (acp.CreateTerminalResponse, error) {
	cmd := exec.Command(p.Command, p.Args...)

	cwd := h.workingDir
	if p.Cwd != nil && *p.Cwd != "" {
		cwd = *p.Cwd
	}
	cmd.Dir = cwd

	// Apply environment variables from the request. Use buildEnvForACP() to
	// match the parent ACP agent process (full env passthrough). The ACP
	// bridge and its terminals share the same environment.
	cmd.Env = buildEnvForACP()
	for _, ev := range p.Env {
		cmd.Env = append(cmd.Env, ev.Name+"="+ev.Value)
	}

	// Use process group for clean kill.
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}

	ts := &terminalState{
		cmd:  cmd,
		done: make(chan struct{}),
	}

	cmd.Stdout = &ts.output
	cmd.Stderr = &ts.output

	if err := cmd.Start(); err != nil {
		return acp.CreateTerminalResponse{}, &acp.RequestError{Code: -32603, Message: fmt.Sprintf("failed to start command: %v", err)}
	}

	// Wait in background and signal done.
	go func() {
		ts.err = cmd.Wait()
		close(ts.done)
	}()

	termID := fmt.Sprintf("term-%d", cmd.Process.Pid)

	h.mu.Lock()
	if h.terminals == nil {
		h.terminals = make(map[string]*terminalState)
	}
	h.terminals[termID] = ts
	h.mu.Unlock()

	return acp.CreateTerminalResponse{TerminalId: termID}, nil
}

// TerminalOutput returns the buffered output from a terminal.
func (h *acpClientHandler) TerminalOutput(_ context.Context, p acp.TerminalOutputRequest) (acp.TerminalOutputResponse, error) {
	h.mu.Lock()
	ts, ok := h.terminals[p.TerminalId]
	h.mu.Unlock()
	if !ok {
		return acp.TerminalOutputResponse{}, &acp.RequestError{Code: -32602, Message: fmt.Sprintf("unknown terminal: %q", p.TerminalId)}
	}

	output := ts.output.String()

	resp := acp.TerminalOutputResponse{Output: output}

	// Check if process has exited.
	select {
	case <-ts.done:
		exitCode := 0
		if ts.err != nil {
			if exitErr, ok := ts.err.(*exec.ExitError); ok {
				exitCode = exitErr.ExitCode()
			}
		}
		resp.ExitStatus = &acp.TerminalExitStatus{ExitCode: acp.Ptr(exitCode)}
	default:
		// Still running.
	}

	return resp, nil
}

// WaitForTerminalExit blocks until the terminal command completes.
func (h *acpClientHandler) WaitForTerminalExit(ctx context.Context, p acp.WaitForTerminalExitRequest) (acp.WaitForTerminalExitResponse, error) {
	h.mu.Lock()
	ts, ok := h.terminals[p.TerminalId]
	h.mu.Unlock()
	if !ok {
		return acp.WaitForTerminalExitResponse{}, &acp.RequestError{Code: -32602, Message: fmt.Sprintf("unknown terminal: %q", p.TerminalId)}
	}

	select {
	case <-ts.done:
		exitCode := 0
		if ts.err != nil {
			if exitErr, ok := ts.err.(*exec.ExitError); ok {
				exitCode = exitErr.ExitCode()
			}
		}
		return acp.WaitForTerminalExitResponse{ExitCode: acp.Ptr(exitCode)}, nil
	case <-ctx.Done():
		return acp.WaitForTerminalExitResponse{}, ctx.Err()
	}
}

// KillTerminalCommand kills a running terminal process using process group kill.
func (h *acpClientHandler) KillTerminalCommand(_ context.Context, p acp.KillTerminalCommandRequest) (acp.KillTerminalCommandResponse, error) {
	h.mu.Lock()
	ts, ok := h.terminals[p.TerminalId]
	h.mu.Unlock()
	if !ok {
		return acp.KillTerminalCommandResponse{}, &acp.RequestError{Code: -32602, Message: fmt.Sprintf("unknown terminal: %q", p.TerminalId)}
	}

	if ts.cmd.Process != nil && ts.cmd.Process.Pid > 0 {
		// Kill the process group for clean cleanup.
		_ = syscall.Kill(-ts.cmd.Process.Pid, syscall.SIGKILL)
	}
	return acp.KillTerminalCommandResponse{}, nil
}

// ReleaseTerminal cleans up a terminal's resources.
func (h *acpClientHandler) ReleaseTerminal(_ context.Context, p acp.ReleaseTerminalRequest) (acp.ReleaseTerminalResponse, error) {
	h.mu.Lock()
	ts, ok := h.terminals[p.TerminalId]
	if ok {
		delete(h.terminals, p.TerminalId)
	}
	h.mu.Unlock()

	if !ok {
		return acp.ReleaseTerminalResponse{}, nil // idempotent
	}

	// Ensure process is dead before releasing.
	if ts.cmd.Process != nil && ts.cmd.Process.Pid > 0 {
		select {
		case <-ts.done:
		default:
			_ = syscall.Kill(-ts.cmd.Process.Pid, syscall.SIGKILL)
			<-ts.done
		}
	}
	return acp.ReleaseTerminalResponse{}, nil
}

// collectedText returns the full agent response text accumulated during the session.
func (h *acpClientHandler) collectedText() string {
	h.mu.Lock()
	defer h.mu.Unlock()
	return strings.Join(h.textParts, "")
}

// safeEmit wraps the emit callback with panic recovery.
func (h *acpClientHandler) safeEmit(evt agent.Event) {
	defer func() {
		if r := recover(); r != nil {
			log.Printf("[acp] panic in event handler: %v", r)
		}
	}()
	h.emit(evt)
}

// cleanup kills any remaining terminal processes.
func (h *acpClientHandler) cleanup() {
	h.mu.Lock()
	terms := make(map[string]*terminalState, len(h.terminals))
	for k, v := range h.terminals {
		terms[k] = v
	}
	h.mu.Unlock()

	for _, ts := range terms {
		if ts.cmd.Process != nil && ts.cmd.Process.Pid > 0 {
			select {
			case <-ts.done:
			default:
				_ = syscall.Kill(-ts.cmd.Process.Pid, syscall.SIGKILL)
			}
		}
	}
}

// extractContentText extracts the text string from an ACP ContentBlock.
func extractContentText(cb acp.ContentBlock) string {
	if cb.Text != nil {
		return cb.Text.Text
	}
	return ""
}

// formatRawInput converts an arbitrary rawInput value to a string for ToolInput.
func formatRawInput(v any) string {
	if v == nil {
		return ""
	}
	if s, ok := v.(string); ok {
		return s
	}
	data, err := json.Marshal(v)
	if err != nil {
		return fmt.Sprintf("%v", v)
	}
	return string(data)
}

// extractToolCallOutput builds a string from tool call content and rawOutput.
func extractToolCallOutput(content []acp.ToolCallContent, rawOutput any) string {
	parts := collectToolCallParts(content)
	if len(parts) > 0 {
		return strings.Join(parts, "\n")
	}
	if rawOutput != nil {
		return formatRawInput(rawOutput)
	}
	return ""
}

// collectToolCallParts extracts text fragments from a slice of tool call content items.
func collectToolCallParts(content []acp.ToolCallContent) []string {
	var parts []string
	for _, c := range content {
		parts = appendToolCallContentPart(parts, c)
	}
	return parts
}

// appendToolCallContentPart appends the text representation of a single ToolCallContent item.
func appendToolCallContentPart(parts []string, c acp.ToolCallContent) []string {
	if c.Content != nil {
		if text := extractContentText(c.Content.Content); text != "" {
			parts = append(parts, text)
		}
	}
	if c.Diff != nil {
		parts = append(parts, fmt.Sprintf("diff %s", c.Diff.Path))
	}
	return parts
}
