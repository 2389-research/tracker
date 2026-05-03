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
	"unicode/utf8"

	acp "github.com/coder/acp-go-sdk"

	"github.com/2389-research/tracker/agent"
)

// acpClientHandler implements acp.Client, translating ACP session updates into
// agent.Event objects via the emit callback and handling agent requests for
// file operations, terminal commands, and permission approval.
//
// The rune-count fields below feed estimateACPUsage. Each is an O(input) sum
// accumulated as notifications arrive — we store counts, not text, so a
// session with megabytes of tool output or reasoning costs O(1) memory per
// channel rather than buffering the raw content. The three channels map
// onto token-usage lines:
//
//   - reasoningRunes → llm.Usage.ReasoningTokens (priced at output rate
//     by providers today; also folded into OutputTokens for EstimateCost).
//   - toolArgRunes   → tool-call arguments the model produced, billable as
//     output alongside message chunks.
//   - toolResultRunes → tool-call output the bridge feeds back as input
//     context on the next model turn; counts on the input side.
type acpClientHandler struct {
	emit       func(agent.Event)
	workingDir string

	mu              sync.Mutex
	terminals       map[string]*terminalState
	textParts       []string
	toolNames       map[string]string // toolCallId → title
	toolCount       int
	turnCount       int
	reasoningRunes  int
	toolArgRunes    int
	toolResultRunes int
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

// handleThoughtChunk processes an agent reasoning/thought chunk. The chunk
// text is billed as output (and recorded in llm.Usage.ReasoningTokens); see
// estimateACPUsage for how reasoningRunes flows into the heuristic.
func (h *acpClientHandler) handleThoughtChunk(chunk *acp.SessionUpdateAgentThoughtChunk, now time.Time) {
	text := extractContentText(chunk.Content)
	if text == "" {
		return
	}
	h.mu.Lock()
	h.reasoningRunes += utf8.RuneCountInString(text)
	h.mu.Unlock()
	h.safeEmit(agent.Event{Type: agent.EventLLMReasoning, Timestamp: now, Text: text})
}

// handleToolCallStart processes a new tool call notification. The tool-call
// arguments (the JSON the model produced to invoke the tool) are billable
// output alongside text message chunks.
func (h *acpClientHandler) handleToolCallStart(tc *acp.SessionUpdateToolCall, now time.Time) {
	argStr := formatRawInput(tc.RawInput)
	h.mu.Lock()
	h.toolNames[string(tc.ToolCallId)] = tc.Title
	h.toolCount++
	h.toolArgRunes += utf8.RuneCountInString(argStr)
	h.mu.Unlock()
	h.safeEmit(agent.Event{
		Type:      agent.EventToolCallStart,
		Timestamp: now,
		ToolName:  tc.Title,
		ToolInput: argStr,
	})
}

// handleToolCallUpdate processes a tool call status update. The tool output
// (or error body) is what the ACP bridge feeds back into the model's
// context on the next turn, so it's billable as input and tracked under
// toolResultRunes — counted even on failed tool calls since the failure
// payload still round-trips through the model. The rune count uses the
// full billable payload (all Content blocks INCLUDING diff NewText/OldText
// + RawOutput when present) rather than the display-oriented
// extractToolCallOutput, which drops RawOutput whenever Content exists
// and reduces diffs to a one-line label.
func (h *acpClientHandler) handleToolCallUpdate(tc *acp.SessionToolCallUpdate, now time.Time) {
	if tc.Status == nil {
		return
	}
	status := *tc.Status
	if status != acp.ToolCallStatusCompleted && status != acp.ToolCallStatusFailed {
		return
	}
	billableRunes := countToolResultRunes(tc.Content, tc.RawOutput)

	h.mu.Lock()
	name := h.toolNames[string(tc.ToolCallId)]
	h.turnCount++ // each tool round-trip counts as a turn
	h.toolResultRunes += billableRunes
	h.mu.Unlock()

	// Display path uses the existing summary formatter — humans reading the
	// event stream don't need the full diff bytes, just a "diff <path>"
	// marker. The billing path above uses the full payload.
	displayOutput := extractToolCallOutput(tc.Content, tc.RawOutput)
	evt := agent.Event{Type: agent.EventToolCallEnd, Timestamp: now, ToolName: name}
	if status == acp.ToolCallStatusFailed {
		evt.ToolError = displayOutput
	} else {
		evt.ToolOutput = displayOutput
	}
	h.safeEmit(evt)
}

// countToolResultRunes sums rune counts across every billable field of a
// tool-call completion payload: text content blocks, diff NewText/OldText/
// Path, terminal IDs, and RawOutput (JSON-serialized when non-nil). Unlike
// the display formatter extractToolCallOutput, this counts Content and
// RawOutput independently — not as fallbacks — because the bridge may send
// both, and both round-trip through the model as next-turn input context.
// Diff items contribute their full before/after text, not just the path,
// since the bridge re-sends the diff payload to the model.
func countToolResultRunes(content []acp.ToolCallContent, rawOutput any) int {
	total := 0
	for _, c := range content {
		total += countToolCallContentRunes(c)
	}
	if rawOutput != nil {
		total += utf8.RuneCountInString(formatRawInput(rawOutput))
	}
	return total
}

// countToolCallContentRunes sums rune counts across all billable fields of
// a single ToolCallContent item.
func countToolCallContentRunes(c acp.ToolCallContent) int {
	total := 0
	if c.Content != nil {
		total += utf8.RuneCountInString(extractContentText(c.Content.Content))
	}
	if c.Diff != nil {
		total += utf8.RuneCountInString(c.Diff.NewText)
		total += utf8.RuneCountInString(c.Diff.Path)
		if c.Diff.OldText != nil {
			total += utf8.RuneCountInString(*c.Diff.OldText)
		}
	}
	if c.Terminal != nil {
		total += utf8.RuneCountInString(c.Terminal.TerminalId)
	}
	return total
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
// SECURITY: rejects paths containing ".." segments before resolution to prevent
// symlink/.. traversal attacks (where a symlink points outside and .. then exits).
func validatePathInWorkDir(path, workDir string) error {
	if workDir == "" {
		return nil // no restriction if working dir is unset
	}
	// Reject raw paths containing ".." to prevent symlink/../escape attacks.
	// filepath.Clean would collapse these lexically, masking the escape.
	// Split on both '/' and '\' to catch Windows-style paths on any platform.
	segments := strings.FieldsFunc(path, func(r rune) bool {
		return r == '/' || r == '\\'
	})
	for _, seg := range segments {
		if seg == ".." {
			return fmt.Errorf("path %q contains '..' component", path)
		}
	}
	resolved, err := resolvePathForValidation(path)
	if err != nil {
		resolved = filepath.Clean(path) // fall back to Clean if symlink resolution fails
	}
	dir, err := resolvePathForValidation(workDir)
	if err != nil {
		dir = filepath.Clean(workDir) // fall back to Clean if symlink resolution fails
	}
	if !strings.HasPrefix(resolved, dir+string(filepath.Separator)) && resolved != dir {
		return fmt.Errorf("path %q is outside working directory %q", path, workDir)
	}
	return nil
}

// resolvePathForValidation resolves symlinks for the longest existing prefix of path.
// Walks up the directory tree until it finds an existing ancestor, resolves symlinks
// on that ancestor, then re-appends the non-existent tail segments.
func resolvePathForValidation(path string) (string, error) {
	resolved, err := filepath.EvalSymlinks(path)
	if err == nil {
		return resolved, nil
	}
	// Path may not exist yet (WriteTextFile). Walk up to find the first
	// existing ancestor and resolve symlinks on it.
	clean := filepath.Clean(path)
	cur := clean
	var tail []string
	for {
		parent := filepath.Dir(cur)
		if parent == cur {
			// Reached root without finding an existing dir.
			return "", fmt.Errorf("no existing ancestor for %q", path)
		}
		tail = append([]string{filepath.Base(cur)}, tail...)
		resolved, err := filepath.EvalSymlinks(parent)
		if err == nil {
			return filepath.Join(append([]string{resolved}, tail...)...), nil
		}
		cur = parent
	}
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
	// Check the bare command name first to catch "eval" / "exec" / "source"
	// with no args (the denylist patterns like "eval *" require a trailing
	// argument and would miss the bare invocation without this check).
	if denied, pattern := checkCommandDenylist(p.Command+" _", nil); denied {
		return acp.CreateTerminalResponse{}, &acp.RequestError{
			Code:    -32602,
			Message: fmt.Sprintf("command matches denied pattern %q", pattern),
		}
	}
	// Also check the full command string with args for pipe-to-shell patterns.
	if len(p.Args) > 0 {
		fullCmd := strings.Join(append([]string{p.Command}, p.Args...), " ")
		if denied, pattern := checkCommandDenylist(fullCmd, nil); denied {
			return acp.CreateTerminalResponse{}, &acp.RequestError{
				Code:    -32602,
				Message: fmt.Sprintf("command matches denied pattern %q", pattern),
			}
		}
	}

	// Validate cwd stays within the working directory.
	cwd := h.workingDir
	if p.Cwd != nil && *p.Cwd != "" {
		if err := validatePathInWorkDir(*p.Cwd, h.workingDir); err != nil {
			return acp.CreateTerminalResponse{}, &acp.RequestError{Code: -32602, Message: err.Error()}
		}
		cwd = *p.Cwd
	}

	cmd := exec.Command(p.Command, p.Args...)
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
