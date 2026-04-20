// ABOUTME: Tool handler that runs shell commands via exec.ExecutionEnvironment.
// ABOUTME: Captures stdout/stderr to pipeline context; exit code 0 = success, non-zero = fail.
package handlers

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
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
type ToolHandlerConfig struct {
	OutputLimit    int
	MaxOutputLimit int
	Allowlist      []string
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

// buildToolEnv constructs a filtered environment for tool command execution.
// Strips environment variables matching sensitive patterns to prevent
// exfiltration via malicious tool commands. Override with TRACKER_PASS_ENV=1.
func buildToolEnv() []string {
	if os.Getenv("TRACKER_PASS_ENV") == "1" {
		return os.Environ()
	}
	return filterSensitiveEnv(os.Environ())
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
	if dir, ok := pctx.GetInternal(pipeline.InternalKeyArtifactDir); ok && dir != "" {
		artifactRoot = dir
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

	return h.execAndBuildOutcome(ctx, node, command, artifactRoot, timeout, outputLimit)
}

// expandAndValidateCommand expands variables in the tool_command attribute and
// runs denylist/allowlist validation. Returns the final command string or an error.
func (h *ToolHandler) expandAndValidateCommand(node *pipeline.Node, pctx *pipeline.PipelineContext) (string, error) {
	command := node.Attrs["tool_command"]
	if command == "" {
		return "", fmt.Errorf("node %q missing required attribute 'tool_command'", node.ID)
	}

	// Layer 1: Expand ${namespace.key} variables with toolCommandMode=true.
	// FAIL CLOSED: if expansion fails (e.g. unsafe ctx.* key), do NOT run the command.
	graphAttrs := extractGraphAttrsFromContext(pctx)
	params := pipeline.ExtractParamsFromGraphAttrs(graphAttrs)
	expanded, err := pipeline.ExpandVariables(command, pctx, params, graphAttrs, false, true)
	if err != nil {
		return "", fmt.Errorf("node %q tool_command variable expansion failed: %w", node.ID, err)
	}
	if expanded != "" {
		command = expanded
	}

	// Layer 2: Denylist/allowlist check on the user-authored command (before working_dir prepend,
	// so allowlist patterns don't need to account for the injected "cd" prefix).
	if err := CheckToolCommand(command, node.ID, h.allowlist, h.bypassDenylist); err != nil {
		return "", err
	}
	return command, nil
}

func extractGraphAttrsFromContext(pctx *pipeline.PipelineContext) map[string]string {
	if pctx == nil {
		return nil
	}
	snapshot := pctx.Snapshot()
	attrs := make(map[string]string)
	for key, value := range snapshot {
		if !strings.HasPrefix(key, "graph.") {
			continue
		}
		attrs[strings.TrimPrefix(key, "graph.")] = value
	}
	return attrs
}

// applyWorkingDir prepends a "cd <dir> && " prefix to command if the node has a
// working_dir attribute. Validates against path traversal and shell metacharacters.
func (h *ToolHandler) applyWorkingDir(node *pipeline.Node, command string) (string, error) {
	wd, ok := node.Attrs["working_dir"]
	if !ok || wd == "" {
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
func (h *ToolHandler) parseTimeout(node *pipeline.Node) (time.Duration, error) {
	timeoutStr, ok := node.Attrs["timeout"]
	if !ok {
		return h.defaultTimeout, nil
	}
	parsed, err := time.ParseDuration(timeoutStr)
	if err != nil {
		return 0, fmt.Errorf("node %q has invalid timeout %q: %w", node.ID, timeoutStr, err)
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
func (h *ToolHandler) execAndBuildOutcome(ctx context.Context, node *pipeline.Node, command, artifactRoot string, timeout time.Duration, outputLimit int) (pipeline.Outcome, error) {
	var result exec.CommandResult
	var err error
	if le, ok := h.env.(*exec.LocalEnvironment); ok {
		result, err = le.ExecCommandWithLimit(ctx, "sh", []string{"-c", command}, timeout, outputLimit, buildToolEnv())
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
		Status: status,
		ContextUpdates: map[string]string{
			pipeline.ContextKeyToolStdout: stdout,
			pipeline.ContextKeyToolStderr: stderr,
		},
	}
	return outcome, pipeline.WriteStatusArtifact(artifactRoot, node.ID, outcome)
}
