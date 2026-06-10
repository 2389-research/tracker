// ABOUTME: Tool handler that runs shell commands via exec.ExecutionEnvironment.
// ABOUTME: Captures stdout/stderr to pipeline context; exit code 0 = success, non-zero = fail.
package handlers

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/2389-research/tracker/agent/exec"
	"github.com/2389-research/tracker/pipeline"
)

const defaultToolTimeout = 30 * time.Second

const (
	DefaultOutputLimit = 64 * 1024        // 64KB per stream
	MaxOutputLimit     = 10 * 1024 * 1024 // 10MB hard ceiling
)

// ToolHandlerConfig holds security configuration for tool command execution.
// DenylistAdd is additive — patterns join the built-in denylist but cannot
// remove any built-in pattern. --bypass-denylist disables both the built-in
// and DenylistAdd patterns (the bypass flag is the intentional all-or-
// nothing escape hatch for sandboxed environments).
type ToolHandlerConfig struct {
	OutputLimit    int
	MaxOutputLimit int
	Allowlist      []string
	DenylistAdd    []string
	BypassDenylist bool
}

// sensitiveEnvPatterns lists environment variable name patterns that should be
// stripped from tool command subprocesses to prevent secret exfiltration.
var sensitiveEnvPatterns = []string{
	"_API_KEY",
	"_SECRET",
	"_TOKEN",
	"_PASSWORD",
}

// runIdentity carries the per-invocation run identity injected into tool
// subprocess environments (#323): TRACKER_RUN_ID, TRACKER_RUN_DIR,
// TRACKER_WORKDIR. RunDir is the same directory WriteStageArtifacts uses,
// so a tool subprocess can read an upstream agent's response.md without an
// `ls -dt` mtime heuristic (unsafe under concurrent runs). Empty fields are
// omitted from the env — e.g. RunID/RunDir when the run has no artifact dir.
// Injection happens on the LocalEnvironment exec path only — the same path
// that applies sensitive-env filtering; non-local ExecutionEnvironments run
// via ExecCommand, which passes no environment at all.
type runIdentity struct {
	RunID   string
	RunDir  string
	WorkDir string
}

// buildToolEnv constructs a filtered environment for tool command execution.
// Strips environment variables matching sensitive patterns to prevent
// exfiltration via malicious tool commands. Override with TRACKER_PASS_ENV=1.
// The run-identity vars are appended after filtering (and on the
// TRACKER_PASS_ENV=1 path) so they are always present.
func buildToolEnv(id runIdentity) []string {
	env := os.Environ()
	if os.Getenv("TRACKER_PASS_ENV") != "1" {
		env = filterSensitiveEnv(env)
	}
	return appendRunIdentityEnv(env, id)
}

// appendRunIdentityEnv removes any inherited TRACKER_RUN_ID / TRACKER_RUN_DIR /
// TRACKER_WORKDIR entries — an operator export must never masquerade as run
// identity, so stale values are dropped rather than duplicated — and appends
// the non-empty fields of id.
func appendRunIdentityEnv(env []string, id runIdentity) []string {
	out := make([]string, 0, len(env)+3)
	for _, e := range env {
		switch strings.SplitN(e, "=", 2)[0] {
		case "TRACKER_RUN_ID", "TRACKER_RUN_DIR", "TRACKER_WORKDIR":
			continue
		}
		out = append(out, e)
	}
	if id.RunID != "" {
		out = append(out, "TRACKER_RUN_ID="+id.RunID)
	}
	if id.RunDir != "" {
		out = append(out, "TRACKER_RUN_DIR="+id.RunDir)
	}
	if id.WorkDir != "" {
		out = append(out, "TRACKER_WORKDIR="+id.WorkDir)
	}
	return out
}

// filterSensitiveEnv returns a copy of env with sensitive vars removed.
func filterSensitiveEnv(env []string) []string {
	var filtered []string
	for _, e := range env {
		if !hasSensitivePattern(e) {
			filtered = append(filtered, e)
		}
	}
	return filtered
}

// hasSensitivePattern returns true if the env var name matches a sensitive pattern.
func hasSensitivePattern(envVar string) bool {
	upper := strings.ToUpper(strings.SplitN(envVar, "=", 2)[0])
	for _, pattern := range sensitiveEnvPatterns {
		if strings.Contains(upper, pattern) {
			return true
		}
	}
	return false
}

// ToolHandler executes shell commands specified in the node's "tool_command"
// attribute. Command output is captured and stored in the pipeline context.
type ToolHandler struct {
	env            exec.ExecutionEnvironment
	defaultTimeout time.Duration
	outputLimit    int
	maxOutputLimit int
	allowlist      []string
	denylistAdd    []string
	bypassDenylist bool
}

// NewToolHandler creates a ToolHandler with the default 30-second timeout.
func NewToolHandler(env exec.ExecutionEnvironment) *ToolHandler {
	return &ToolHandler{env: env, defaultTimeout: defaultToolTimeout, outputLimit: DefaultOutputLimit, maxOutputLimit: MaxOutputLimit}
}

// NewToolHandlerWithTimeout creates a ToolHandler with a custom default timeout.
func NewToolHandlerWithTimeout(env exec.ExecutionEnvironment, timeout time.Duration) *ToolHandler {
	return &ToolHandler{env: env, defaultTimeout: timeout, outputLimit: DefaultOutputLimit, maxOutputLimit: MaxOutputLimit}
}

// NewToolHandlerWithConfig creates a ToolHandler with full security configuration.
func NewToolHandlerWithConfig(env exec.ExecutionEnvironment, cfg ToolHandlerConfig) *ToolHandler {
	outputLimit := cfg.OutputLimit
	if outputLimit <= 0 {
		outputLimit = DefaultOutputLimit
	}
	maxLimit := cfg.MaxOutputLimit
	if maxLimit <= 0 {
		maxLimit = MaxOutputLimit
	}
	if outputLimit > maxLimit {
		outputLimit = maxLimit
	}
	return &ToolHandler{
		env:            env,
		defaultTimeout: defaultToolTimeout,
		outputLimit:    outputLimit,
		maxOutputLimit: maxLimit,
		allowlist:      cfg.Allowlist,
		denylistAdd:    cfg.DenylistAdd,
		bypassDenylist: cfg.BypassDenylist,
	}
}

// Name returns the handler name used for registry lookup.
func (h *ToolHandler) Name() string { return "tool" }

// parseByteSize parses a byte size string with optional KB/MB suffix.
// Examples: "64KB" → 65536, "1MB" → 1048576, "4096" → 4096.
func parseByteSize(s string) (int, error) {
	s = strings.TrimSpace(s)
	upper := strings.ToUpper(s)
	if strings.HasSuffix(upper, "MB") {
		n, err := strconv.Atoi(strings.TrimSuffix(upper, "MB"))
		return n * 1024 * 1024, err
	}
	if strings.HasSuffix(upper, "KB") {
		n, err := strconv.Atoi(strings.TrimSuffix(upper, "KB"))
		return n * 1024, err
	}
	return strconv.Atoi(s)
}

// extractToolMarker applies the marker_grep regex to stdout line-by-line
// and returns the last match. If the regex has at least one capture group,
// the value of group 1 is returned; otherwise the full match string is
// returned. Returns missing=true when nothing matched. Returns a non-nil
// error only on regex-compile failure (author error, surfaced via
// outcome.ContextUpdates[pipeline.ContextKeyToolMarkerError]).
//
// Line-oriented scan instead of byte-oriented: matchers like
// `^tests-(pass|fail)$` are anchored against the line, and arbitrary
// earlier output can never accidentally match a routing-marker shape.
// "Last line wins" so a tool that prints progress before its final
// marker still routes correctly.
//
// The scan uses strings.IndexByte to walk newline boundaries without
// allocating a full slice — tool stdout is capped at 10MB and a tool
// that emits millions of short lines should not balloon allocations
// during marker extraction. Trailing `\r` on each line is stripped so
// CRLF (Windows-style) output doesn't break anchored regexes like
// `^tests-pass$`.
func extractToolMarker(pattern, stdout string) (string, bool, error) {
	re, err := regexp.Compile(pattern)
	if err != nil {
		// Return the raw compile error; the pattern is already echoed
		// separately by callers (MarkerDetail.Pattern, the engine's
		// EventToolMarkerMissing message, and the `tracker diagnose`
		// suggestion copy) so repeating it here would just duplicate
		// the pattern text in every surface.
		return "", false, err
	}
	var last []string
	walkLines(stdout, func(line string) {
		line = strings.TrimSuffix(line, "\r")
		if m := re.FindStringSubmatch(line); m != nil {
			last = m
		}
	})
	if last == nil {
		return "", true, nil
	}
	if len(last) > 1 {
		return last[1], false, nil
	}
	return last[0], false, nil
}

// toolRouteSentinelPattern is the reserved sentinel regex for the
// convention-based routing channel (#212). Anchored on both ends so an
// arbitrary "_TRACKER_ROUTE" appearance inside other text doesn't match;
// optional leading/trailing whitespace tolerates accidental indentation;
// non-greedy capture + trailing whitespace gobbler keeps trailing
// spaces out of the captured value.
var toolRouteSentinelPattern = regexp.MustCompile(`^\s*_TRACKER_ROUTE=(.+?)\s*$`)

// extractToolRoute scans stdout for _TRACKER_ROUTE= sentinel lines and
// returns the captured value of the LAST matching line (or "" if none).
// CRLF handling, line-oriented scan, and allocation-free walk all
// mirror extractToolMarker — the runtime guarantees consistent
// semantics across both marker_grep (#210) and route sentinel (#212).
func extractToolRoute(stdout string) string {
	var last string
	walkLines(stdout, func(line string) {
		line = strings.TrimSuffix(line, "\r")
		if m := toolRouteSentinelPattern.FindStringSubmatch(line); m != nil {
			last = m[1]
		}
	})
	return last
}

// walkLines iterates over s line by line (separator "\n"), calling fn
// for each line including the final trailing line if it has no newline.
// Allocation-free: each line is a sub-string of s, no full slice
// materialized.
func walkLines(s string, fn func(line string)) {
	for {
		i := strings.IndexByte(s, '\n')
		if i < 0 {
			if s != "" {
				fn(s)
			}
			return
		}
		fn(s[:i])
		s = s[i+1:]
	}
}

// tailForDiag returns up to n bytes from the end of s for inclusion in
// audit/diagnose payloads. Bounds-safe for short inputs.
func tailForDiag(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[len(s)-n:]
}

// Execute runs the shell command from the node's "tool_command" attribute.
// It stores stdout and stderr in the pipeline context and returns success
// for exit code 0, fail for non-zero exit codes. An optional "timeout"
// attribute on the node overrides the default timeout.
//
// Security layers applied (in order):
//  1. ExpandVariables with toolCommandMode=true — blocks unsafe ctx.* keys (FAIL CLOSED)
//  2. CheckToolCommand — denylist/allowlist validation on the final command
//  3. Per-node output_limit capped at h.maxOutputLimit
//  4. ExecCommandWithLimit with buildToolEnv() for env stripping (LocalEnvironment only)
func (h *ToolHandler) Execute(ctx context.Context, node *pipeline.Node, pctx *pipeline.PipelineContext) (pipeline.Outcome, error) {
	command, err := h.expandAndValidateCommand(node, pctx)
	if err != nil {
		return pipeline.Outcome{}, err
	}

	artifactRoot := h.env.WorkingDir()
	identity := runIdentity{WorkDir: absPathOrSelf(h.env.WorkingDir())}
	if dir, ok := pctx.GetInternal(pipeline.InternalKeyArtifactDir); ok && dir != "" {
		artifactRoot = dir
		identity.RunDir = absPathOrSelf(dir)
		identity.RunID = filepath.Base(identity.RunDir)
	}

	command, err = h.applyWorkingDir(node, command)
	if err != nil {
		return pipeline.Outcome{}, err
	}

	timeout, err := h.parseTimeout(node)
	if err != nil {
		return pipeline.Outcome{}, err
	}

	outputLimit, err := h.parseOutputLimit(node)
	if err != nil {
		return pipeline.Outcome{}, err
	}

	return h.execAndBuildOutcome(ctx, node, command, artifactRoot, identity, timeout, outputLimit)
}

// absPathOrSelf returns the absolute form of p, or p unchanged if it cannot
// be resolved (filepath.Abs fails when the current working directory cannot
// be determined — deleted, permission-denied, or other os.Getwd failures).
func absPathOrSelf(p string) string {
	if abs, err := filepath.Abs(p); err == nil {
		return abs
	}
	return p
}

// expandAndValidateCommand expands variables in the tool_command attribute and
// runs denylist/allowlist validation. Returns the final command string or an error.
func (h *ToolHandler) expandAndValidateCommand(node *pipeline.Node, pctx *pipeline.PipelineContext) (string, error) {
	command := node.ToolConfig().Command
	if command == "" {
		return "", fmt.Errorf("node %q missing required attribute 'tool_command'", node.ID)
	}

	// Layer 1: Expand ${namespace.key} variables with toolCommandMode=true.
	// FAIL CLOSED: if expansion fails (e.g. unsafe ctx.* key), do NOT run the command.
	// Always assign the expanded result — keeping the original on empty
	// expansion would leave literal ${...} placeholders in the command
	// and ship them to the shell. An all-empty post-expansion command
	// is itself an error: the user intended something, it resolved to
	// nothing, running "" is not a meaningful fallback.
	graphAttrs, params := extractGraphAttrsAndParams(pctx)
	expanded, err := pipeline.ExpandVariables(command, pctx, params, graphAttrs, false, true)
	if err != nil {
		return "", fmt.Errorf("node %q tool_command variable expansion failed: %w", node.ID, err)
	}
	command = expanded
	if strings.TrimSpace(command) == "" {
		return "", fmt.Errorf("node %q tool_command expanded to empty — check that all ${...} references resolve", node.ID)
	}

	// Layer 2: Denylist/allowlist check on the user-authored command (before working_dir prepend,
	// so allowlist patterns don't need to account for the injected "cd" prefix).
	if err := CheckToolCommand(command, node.ID, h.allowlist, h.denylistAdd, h.bypassDenylist); err != nil {
		return "", err
	}
	return command, nil
}

// extractGraphAttrsAndParams walks the context snapshot once and returns:
//   - graphAttrs: every "graph.<key>" entry with the prefix stripped
//   - params: every "graph.params.<key>" entry (i.e. workflow-level params)
//     with both prefixes stripped.
//
// Single pass replaces the previous snapshot + two-pass extraction. The
// handler reads params via context (not directly from graph.Attrs) because
// the pipeline engine already seeds graph.Attrs → "graph.*" keys at
// startup in buildInitialContext, and checkpoint resume merges the same
// context. Subgraphs inherit parent graph.* via initialContext overlay.
func extractGraphAttrsAndParams(pctx *pipeline.PipelineContext) (graphAttrs, params map[string]string) {
	if pctx == nil {
		return nil, nil
	}
	const graphPrefix = "graph."
	const paramsInGraphPrefix = "graph.params."
	graphAttrs = make(map[string]string)
	params = make(map[string]string)
	for key, value := range pctx.Snapshot() {
		if !strings.HasPrefix(key, graphPrefix) {
			continue
		}
		graphAttrs[strings.TrimPrefix(key, graphPrefix)] = value
		if strings.HasPrefix(key, paramsInGraphPrefix) {
			params[strings.TrimPrefix(key, paramsInGraphPrefix)] = value
		}
	}
	return graphAttrs, params
}

// applyWorkingDir prepends a "cd <dir> && " prefix to command if the node has a
// working_dir attribute. Validates against path traversal and shell metacharacters.
func (h *ToolHandler) applyWorkingDir(node *pipeline.Node, command string) (string, error) {
	wd := node.ToolConfig().WorkingDir
	if wd == "" {
		return command, nil
	}
	if strings.ContainsAny(wd, "`$;|&()<>\n\r") {
		return "", fmt.Errorf("node %q has unsafe working_dir %q: contains shell metacharacters", node.ID, wd)
	}
	cleaned := filepath.Clean(wd)
	if strings.Contains(cleaned, "..") {
		return "", fmt.Errorf("node %q has unsafe working_dir %q: path traversal detected", node.ID, wd)
	}
	return fmt.Sprintf("cd %q && %s", cleaned, command), nil
}

// parseTimeout returns the timeout for the node, preferring the node attr over the default.
//
// Zero and negative durations are rejected with an error naming the node and
// the offending value. This runs when the tool node executes (inside
// ToolHandler.Execute, before the command is dispatched) rather than at
// workflow load time. Previously such values were passed through to
// context.WithTimeout and caused immediate cancellation with a confusing
// "command timed out" error; hard-failing here surfaces the misconfiguration
// to the pipeline author instead.
func (h *ToolHandler) parseTimeout(node *pipeline.Node) (time.Duration, error) {
	timeoutStr, ok := node.Attrs["timeout"]
	if !ok {
		return h.defaultTimeout, nil
	}
	parsed, err := time.ParseDuration(timeoutStr)
	if err != nil {
		return 0, fmt.Errorf("node %q has invalid timeout %q: %w", node.ID, timeoutStr, err)
	}
	if parsed <= 0 {
		return 0, fmt.Errorf("node %q has non-positive timeout %q: must be > 0", node.ID, timeoutStr)
	}
	return parsed, nil
}

// parseOutputLimit returns the output byte limit for the node, capped at h.maxOutputLimit.
func (h *ToolHandler) parseOutputLimit(node *pipeline.Node) (int, error) {
	limitStr, ok := node.Attrs["output_limit"]
	if !ok || limitStr == "" {
		return h.outputLimit, nil
	}
	parsed, err := parseByteSize(limitStr)
	if err != nil {
		return 0, fmt.Errorf("node %q has invalid output_limit %q: %w", node.ID, limitStr, err)
	}
	if parsed <= 0 {
		return 0, fmt.Errorf("node %q has non-positive output_limit %q", node.ID, limitStr)
	}
	if parsed > h.maxOutputLimit {
		parsed = h.maxOutputLimit
	}
	return parsed, nil
}

// execAndBuildOutcome runs the command and builds the pipeline outcome from the result.
// Layer 4: uses ExecCommandWithLimit on LocalEnvironment, ExecCommand otherwise.
func (h *ToolHandler) execAndBuildOutcome(ctx context.Context, node *pipeline.Node, command, artifactRoot string, identity runIdentity, timeout time.Duration, outputLimit int) (pipeline.Outcome, error) {
	var result exec.CommandResult
	var err error
	if le, ok := h.env.(*exec.LocalEnvironment); ok {
		result, err = le.ExecCommandWithLimit(ctx, "sh", []string{"-c", command}, timeout, outputLimit, buildToolEnv(identity))
	} else {
		result, err = h.env.ExecCommand(ctx, "sh", []string{"-c", command}, timeout)
	}
	if err != nil {
		return pipeline.Outcome{}, fmt.Errorf("tool command failed for node %q: %w", node.ID, err)
	}

	status := pipeline.OutcomeSuccess
	if result.ExitCode != 0 {
		status = pipeline.OutcomeFail
	}

	// Trim trailing whitespace from stdout/stderr so edge conditions
	// like context.tool_stdout=pass match reliably (shell commands
	// often emit trailing newlines). Only trim the right side to
	// preserve any intentional leading whitespace or indentation.
	stdout := strings.TrimRight(result.Stdout, " \t\n\r")
	stderr := strings.TrimRight(result.Stderr, " \t\n\r")

	outcome := pipeline.Outcome{
		Status: string(status),
		ContextUpdates: map[string]string{
			pipeline.ContextKeyToolStdout: stdout,
			pipeline.ContextKeyToolStderr: stderr,
		},
	}
	// Surface truncation as structured outcome metadata so the engine can
	// emit EventToolOutputTruncated and `tracker diagnose` can correlate
	// routing misses with dropped output (issue #208). Tail-window capture
	// preserves the routing-relevant trailing bytes; the event tells
	// operators that earlier bytes were elided.
	//
	// Byte accounting reflects the *raw* (pre-trim) captured tail and
	// dropped head — i.e., what the process actually emitted, not what
	// ended up in ctx.tool_stdout / ctx.tool_stderr after TrimRight.
	// This keeps the documented invariant from pipeline/events.go
	// (TotalBytes = CapturedBytes + DroppedBytes) and matches the
	// "how big was this stream" question operators ask. Trimming is a
	// separate presentation concern for routing conditions; consumers
	// that need the trimmed length can compute len(ctx.tool_stdout).
	if result.StdoutTruncated {
		outcome.Truncations = append(outcome.Truncations, pipeline.TruncationDetail{
			Stream:        "stdout",
			Limit:         outputLimit,
			CapturedBytes: len(result.Stdout),
			DroppedBytes:  result.StdoutBytesDropped,
			TotalBytes:    len(result.Stdout) + result.StdoutBytesDropped,
		})
	}
	if result.StderrTruncated {
		outcome.Truncations = append(outcome.Truncations, pipeline.TruncationDetail{
			Stream:        "stderr",
			Limit:         outputLimit,
			CapturedBytes: len(result.Stderr),
			DroppedBytes:  result.StderrBytesDropped,
			TotalBytes:    len(result.Stderr) + result.StderrBytesDropped,
		})
	}
	// Extract the routing marker if marker_grep is declared. This is the
	// typed alternative to "route on ctx.tool_stdout suffix" (#210): the
	// regex matches against captured stdout, the last match wins, and
	// ctx.tool_marker is populated with capture group 1 (or the full
	// match if no group). On no-match the node fails loudly via
	// EventToolMarkerMissing rather than silently falling through to an
	// unconditional edge — the whole point of marker_grep is to remove
	// that foot-gun (the #208 root cause).
	if pattern := node.ToolConfig().MarkerGrep; pattern != "" {
		// Reset marker-related keys at the start of every marker_grep-
		// declaring node so a prior node's value can't leak into this
		// node's routing context on the missing-match or compile-error
		// paths (where only some branches set the key).
		outcome.ContextUpdates[pipeline.ContextKeyToolMarker] = ""
		outcome.ContextUpdates[pipeline.ContextKeyToolMarkerError] = ""

		marker, missing, err := extractToolMarker(pattern, stdout)
		switch {
		case err != nil:
			// Bad regex on the node — author error, fail loud at runtime
			// (validate-time lint #211 will catch this earlier when it
			// lands). The compile error is surfaced both via
			// MissingMarker.Error (so EventToolMarkerMissing carries it
			// into activity.jsonl + tracker diagnose) and via
			// ctx.tool_marker_error so routing conditions can read it.
			outcome.Status = string(pipeline.OutcomeFail)
			outcome.ContextUpdates[pipeline.ContextKeyToolMarkerError] = err.Error()
			outcome.MissingMarker = &pipeline.MarkerDetail{
				Pattern: pattern,
				Error:   err.Error(),
			}
		case missing:
			outcome.Status = string(pipeline.OutcomeFail)
			outcome.MissingMarker = &pipeline.MarkerDetail{
				Pattern:      pattern,
				CapturedTail: tailForDiag(stdout, 256),
			}
		default:
			outcome.ContextUpdates[pipeline.ContextKeyToolMarker] = marker
		}
	}

	// _TRACKER_ROUTE= sentinel scanning is always-on for tool nodes
	// (issue #212). The author opts in by emitting the sentinel line
	// from the tool; no per-node attribute required. The last matching
	// line wins, value goes to ctx.tool_route. Always-clear before
	// extract so a prior node's value doesn't leak into routing.
	// When route_required: true is set on the node AND no sentinel
	// was extracted, the node fails (symmetric to marker_grep's
	// missing-match handling). The matcher is built-in so there is
	// no regex-compile error path; only the missing-sentinel path.
	outcome.ContextUpdates[pipeline.ContextKeyToolRoute] = ""
	if route := extractToolRoute(stdout); route != "" {
		outcome.ContextUpdates[pipeline.ContextKeyToolRoute] = route
	} else if node.ToolConfig().RouteRequired {
		outcome.Status = string(pipeline.OutcomeFail)
		outcome.MissingRoute = &pipeline.RouteDetail{
			CapturedTail: tailForDiag(stdout, 256),
		}
	}

	if applyDeclaredWrites(node, outcome.ContextUpdates, stdout, "Tool stdout JSON") {
		outcome.Status = string(pipeline.OutcomeFail)
	}
	return outcome, pipeline.WriteStatusArtifact(artifactRoot, node.ID, outcome)
}
