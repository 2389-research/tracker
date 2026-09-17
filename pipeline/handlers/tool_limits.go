// ABOUTME: Tool-node limit parsing (timeout, output_limit) and the #644 timeout
// ABOUTME: classification that turns a node-timeout kill into a routable OutcomeFail.
package handlers

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/2389-research/tracker/agent/exec"
	"github.com/2389-research/tracker/pipeline"
)

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

// classifyToolTimeout separates the node's own `timeout:` breach from every
// other exec error (#644). A *exec.TimeoutError while the CALLER's ctx is still
// live is the node timeout: returned as the detail with a nil error so the
// handler builds a routable OutcomeFail. If the caller's ctx is done, the run
// itself was cancelled (Ctrl+C, --max-wall-time, parent deadline) — that is not
// the node's timeout and stays a hard error so the engine's cancellation path
// runs. Any other error passes through unchanged.
func classifyToolTimeout(ctx context.Context, err error) (*exec.TimeoutError, error) {
	var te *exec.TimeoutError
	if errors.As(err, &te) && ctx.Err() == nil {
		return te, nil
	}
	return nil, err
}

// applyToolTimeout folds a node-timeout kill into the captured result (#644):
// the reason is appended to whatever stderr tail was captured so
// `when ctx.outcome = fail` edges, fallback_target and the escalation prompt
// can all see why. The stdout tail is preserved untouched. No-op when the
// command did not time out.
func applyToolTimeout(result *exec.CommandResult, timedOut *exec.TimeoutError) {
	if timedOut == nil {
		return
	}
	if result.Stderr != "" && !strings.HasSuffix(result.Stderr, "\n") {
		result.Stderr += "\n"
	}
	result.Stderr += timedOut.Error()
}

// toolTimeoutDetail builds the EventToolTimeout payload, or nil when the
// command did not time out. CapturedBytes is the raw (pre-trim) captured
// stdout+stderr tail so an operator can tell "hung silently" from "was
// mid-way through a long run".
func toolTimeoutDetail(result exec.CommandResult, timedOut *exec.TimeoutError) *pipeline.ToolTimeoutDetail {
	if timedOut == nil {
		return nil
	}
	return &pipeline.ToolTimeoutDetail{
		Timeout:       timedOut.Timeout,
		CapturedBytes: len(result.Stdout) + len(result.Stderr),
	}
}
