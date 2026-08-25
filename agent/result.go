// ABOUTME: SessionResult captures the outcome of a completed agent session.
// ABOUTME: Tracks turns, tool calls, file changes, token usage, and provides pretty-print formatting.
package agent

import (
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/2389-research/tracker/llm"
)

// errEmptyResponse is the hard-fail returned after repeated empty API responses
// (0 content parts, 0 output tokens). A typed error lets deriveDisposition name
// the stop as DispositionEmptyResponse (#601) without string-matching. The
// Error() text is unchanged so downstream string classifiers keep matching.
type errEmptyResponse struct{ count int }

func (e *errEmptyResponse) Error() string {
	return fmt.Sprintf("agent session failed: %d consecutive empty API responses", e.count)
}

// isEmptyResponseError reports whether err is (or wraps) an errEmptyResponse.
func isEmptyResponseError(err error) bool {
	var e *errEmptyResponse
	return errors.As(err, &e)
}

// BreachVerifyState records the result of the verify-on-breach pass (#303).
// The zero value is BreachVerifyNotRun, so an unset field is always the safe
// "could not verify" state — never mistaken for a pass.
type BreachVerifyState int

const (
	BreachVerifyNotRun BreachVerifyState = iota // no verify ran (not a breach, no explicit command, or loop detected)
	BreachVerifyPassed                          // verify command exited 0
	BreachVerifyFailed                          // verify failed (non-zero exit) or errored
)

// Disposition is the single authoritative reason a session's turn loop stopped
// (#601). It replaces the historical family of independent boolean flags
// (MaxTurnsUsed, LoopDetected, NodeCostExceeded, NoProgressDetected) as the one
// name each stop carries, so consumers switch on one value instead of
// re-deriving a hidden precedence. The booleans are retained for one release as
// DEPRECATED, derived views of this value (see deriveDisposition); new code
// should read Disposition. BreachVerify is orthogonal and stays a separate
// field — it is only meaningful on a DispositionMaxTurns stop.
type Disposition int

const (
	// DispositionCompleted is a clean stop: the model ended with text and no
	// tool calls, or a terminal tool succeeded. Also the zero value, so a
	// SessionResult built by a non-native backend (which never runs this turn
	// loop) reads as Completed.
	DispositionCompleted Disposition = iota
	// DispositionError is a hard provider/tool error that aborted the run
	// (result.Error is set to a non-empty-response error).
	DispositionError
	// DispositionEmptyResponse is the hard-fail after repeated empty API
	// responses (0 content parts, 0 output tokens) — never a success (#601).
	DispositionEmptyResponse
	// DispositionLoopDetected is an identical-tool-call loop tripped past
	// LoopDetectionThreshold.
	DispositionLoopDetected
	// DispositionNodeCostExceeded is the #304 per-node MaxCostUSD ceiling.
	DispositionNodeCostExceeded
	// DispositionNoProgress is the #304 no-progress detector.
	DispositionNoProgress
	// DispositionMaxTurns is plain turn-budget exhaustion.
	DispositionMaxTurns
)

// String returns a stable, lower-snake token for the disposition, suitable for
// logs and event payloads.
func (d Disposition) String() string {
	switch d {
	case DispositionCompleted:
		return "completed"
	case DispositionError:
		return "error"
	case DispositionEmptyResponse:
		return "empty_response"
	case DispositionLoopDetected:
		return "loop_detected"
	case DispositionNodeCostExceeded:
		return "node_cost_exceeded"
	case DispositionNoProgress:
		return "no_progress"
	case DispositionMaxTurns:
		return "max_turns"
	default:
		return "unknown"
	}
}

// deriveDisposition is the centralized precedence function (#601): it maps the
// run's terminal signals to exactly one Disposition, so the ordering lives in
// one place instead of being re-implemented by each consumer. Error-class stops
// (which set result.Error and short-circuit the loop) rank first, then the #304
// node guards, then a detected loop, then plain turn exhaustion. LoopDetected
// deliberately outranks MaxTurns because the loop path also sets MaxTurnsUsed —
// the more specific reason wins.
func deriveDisposition(r *SessionResult) Disposition {
	switch {
	case isEmptyResponseError(r.Error):
		return DispositionEmptyResponse
	case r.Error != nil:
		return DispositionError
	case r.NodeCostExceeded:
		return DispositionNodeCostExceeded
	case r.NoProgressDetected:
		return DispositionNoProgress
	case r.LoopDetected:
		return DispositionLoopDetected
	case r.MaxTurnsUsed:
		return DispositionMaxTurns
	default:
		return DispositionCompleted
	}
}

// SessionResult holds summary statistics and metadata from a completed session.
type SessionResult struct {
	SessionID string
	Provider  string
	Duration  time.Duration
	Turns     int
	// Disposition is the single authoritative stop reason (#601). Prefer it over
	// the boolean flags below, which are DEPRECATED derived views kept for one
	// release and will be removed.
	Disposition Disposition
	// Deprecated: read Disposition == DispositionMaxTurns instead. Note that the
	// loop-detection path sets both this and LoopDetected.
	MaxTurnsUsed bool
	// Deprecated: read Disposition == DispositionLoopDetected instead.
	LoopDetected bool
	BreachVerify BreachVerifyState // #303: result of the verify-on-breach pass (meaningful only on a max-turns stop)
	// Deprecated: read Disposition == DispositionNodeCostExceeded instead.
	NodeCostExceeded bool // #304: true when per-node MaxCostUSD was breached
	// Deprecated: read Disposition == DispositionNoProgress instead.
	NoProgressDetected bool // #304: true when NoProgressTurns consecutive tool-call-free turns elapsed
	ToolCalls          map[string]int
	FilesModified      []string
	FilesCreated       []string
	Usage              llm.Usage
	ContextUtilization float64
	ToolCacheHits      int
	ToolCacheMisses    int
	ToolTimings        map[string]time.Duration
	CompactionsApplied int
	LongestTurn        time.Duration
	EpisodeSummary     string
	Error              error
}

// TotalToolCalls returns the sum of all tool call counts.
func (r SessionResult) TotalToolCalls() int {
	total := 0
	for _, count := range r.ToolCalls {
		total += count
	}
	return total
}

// String returns a human-readable summary of the session result.
func (r SessionResult) String() string {
	var b strings.Builder

	status := "completed"
	if r.Error != nil {
		status = "failed"
	}
	fmt.Fprintf(&b, "Session %s %s in %s\n", r.SessionID, status, r.Duration.Round(time.Second))

	writeToolCallSummary(&b, r)
	writeFileSummary(&b, r)
	writeTokenSummary(&b, r)
	writeExtrasLine(&b, r)

	return b.String()
}

// writeToolCallSummary appends the turns and tool call breakdown line.
func writeToolCallSummary(b *strings.Builder, r SessionResult) {
	toolParts := sortedToolCallParts(r.ToolCalls)
	fmt.Fprintf(b, "Turns: %d | Tool calls: %d", r.Turns, r.TotalToolCalls())
	if len(toolParts) > 0 {
		fmt.Fprintf(b, " (%s)", strings.Join(toolParts, ", "))
	}
	b.WriteString("\n")
}

// sortedToolCallParts returns "name: count" parts sorted by name.
func sortedToolCallParts(calls map[string]int) []string {
	keys := make([]string, 0, len(calls))
	for k := range calls {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	parts := make([]string, 0, len(keys))
	for _, k := range keys {
		parts = append(parts, fmt.Sprintf("%s: %d", k, calls[k]))
	}
	return parts
}

// writeFileSummary appends modified and created files lines.
func writeFileSummary(b *strings.Builder, r SessionResult) {
	if len(r.FilesModified) > 0 {
		fmt.Fprintf(b, "Files modified: %s\n", strings.Join(r.FilesModified, ", "))
	}
	if len(r.FilesCreated) > 0 {
		fmt.Fprintf(b, "Files created: %s\n", strings.Join(r.FilesCreated, ", "))
	}
}

// writeTokenSummary appends the token/cost line.
func writeTokenSummary(b *strings.Builder, r SessionResult) {
	fmt.Fprintf(b, "Tokens: %d (in: %d, out: %d)",
		r.Usage.TotalTokens, r.Usage.InputTokens, r.Usage.OutputTokens)
	if r.Usage.EstimatedCost > 0 {
		fmt.Fprintf(b, " | Cost: $%.2f", r.Usage.EstimatedCost)
	}
	b.WriteString("\n")
}

// writeExtrasLine appends compaction and longest-turn info if present.
func writeExtrasLine(b *strings.Builder, r SessionResult) {
	var extras []string
	if r.CompactionsApplied > 0 {
		extras = append(extras, fmt.Sprintf("Compactions: %d", r.CompactionsApplied))
	}
	if r.LongestTurn > 0 {
		extras = append(extras, fmt.Sprintf("Longest turn: %s", r.LongestTurn.Round(time.Second)))
	}
	if len(extras) > 0 {
		fmt.Fprintf(b, "%s\n", strings.Join(extras, " | "))
	}
}
