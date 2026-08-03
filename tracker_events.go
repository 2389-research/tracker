// ABOUTME: Public NDJSON event writer for the tracker --json wire format.
// ABOUTME: Threaded from pipeline/LLM/agent event streams; thread-safe for concurrent writers.
package tracker

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"sync"
	"time"

	"github.com/2389-research/tracker/agent"
	"github.com/2389-research/tracker/llm"
	"github.com/2389-research/tracker/pipeline"
)

const ndjsonTimestampLayout = "2006-01-02T15:04:05.000Z07:00"

// StreamEvent is the stable wire format for the tracker --json mode.
//
// Field-stability contract:
//   - Existing JSON field names are never renamed, retyped, or removed. New
//     optional fields may be added without a major bump, so a consumer must
//     tolerate unknown keys.
//   - Every field except ts/source/type is `omitempty`: a field is present only
//     on the event types that actually carry it. A consumer keys off `type`
//     (within `source`) and reads the fields documented for it; it must not
//     infer meaning from a field's absence beyond "this event does not carry it".
//   - Where the same datum is also written to `activity.jsonl`, the JSON field
//     name here is identical, so one decoder serves both streams. The only
//     fields with no `activity.jsonl` counterpart are the run-snapshot group
//     (`snapshot_*`) and the per-turn agent usage extras (`token_cache_read`,
//     `token_cache_write`, `turn_cost_usd`).
//
// Payload size: `context_snapshot` / `context_updates` are unbounded maps of
// routing-relevant context (a decision event on a context-heavy run can carry
// tens of kilobytes), `gate_prompt` is capped at pipeline.GateMaxPromptBytes,
// and the `*_tail` diagnostic fields are capped at 256 bytes by their emitters.
// `content` is unbounded (a tool_call_end carries full tool output). A consumer
// on a bounded transport should cap or drop those fields itself.
type StreamEvent struct {
	Timestamp string `json:"ts"`
	Source    string `json:"source"`              // "pipeline", "llm", "agent"
	Type      string `json:"type"`                // event type within source
	RunID     string `json:"run_id,omitempty"`    // pipeline run ID
	NodeID    string `json:"node_id,omitempty"`   // pipeline node ID
	Message   string `json:"message,omitempty"`   // human-readable message
	Error     string `json:"error,omitempty"`     // error text
	Provider  string `json:"provider,omitempty"`  // LLM provider
	Model     string `json:"model,omitempty"`     // LLM model
	ToolName  string `json:"tool_name,omitempty"` // tool name for agent/LLM tool events
	Content   string `json:"content,omitempty"`   // text content (LLM output, tool output)
	// TerminalStatus is set only on a pipeline run's terminal event
	// (pipeline_completed / pipeline_failed / budget_exceeded): "success",
	// "validation_overridden", "fail", or "budget_exceeded". Empty otherwise.
	TerminalStatus string `json:"terminal_status,omitempty"`
	// BundleIdentity is the content-addressed identity of the .dipx bundle the
	// run executes against ("sha256:<hex>"). Set on pipeline events of a bundle
	// run; empty for a plain .dip run.
	BundleIdentity string `json:"bundle_identity,omitempty"`

	// Decision fields — set on decision_edge / decision_condition /
	// decision_outcome / decision_restart / conditional_fallthrough events, from
	// pipeline.DecisionDetail. ConditionMatch and RestartCount are pointers so a
	// consumer can distinguish "false"/"0" from "not carried".
	EdgeFrom        string                   `json:"edge_from,omitempty"`
	EdgeTo          string                   `json:"edge_to,omitempty"`
	EdgeCondition   string                   `json:"edge_condition,omitempty"`
	EdgePriority    string                   `json:"edge_priority,omitempty"`
	ConditionMatch  *bool                    `json:"condition_match,omitempty"`
	OutcomeStatus   string                   `json:"outcome_status,omitempty"`
	ContextSnapshot map[string]string        `json:"context_snapshot,omitempty"`
	ContextUpdates  map[string]string        `json:"context_updates,omitempty"`
	RestartCount    *int                     `json:"restart_count,omitempty"`
	ClearedNodes    []string                 `json:"cleared_nodes,omitempty"`
	ConditionsTried []pipeline.ConditionEval `json:"conditions_tried,omitempty"`

	// TokenInput / TokenOutput carry the token counts of whatever the event
	// describes: the node's session stats on a pipeline decision event, the
	// turn's usage on an agent turn_metrics / llm_finish event. They are never
	// run-cumulative — that is total_tokens below.
	TokenInput  int `json:"token_input,omitempty"`
	TokenOutput int `json:"token_output,omitempty"`
	// TokenCacheRead / TokenCacheWrite / TurnCostUSD are the per-turn agent
	// extras (turn_metrics, llm_finish). No activity.jsonl counterpart.
	TokenCacheRead  int     `json:"token_cache_read,omitempty"`
	TokenCacheWrite int     `json:"token_cache_write,omitempty"`
	TurnCostUSD     float64 `json:"turn_cost_usd,omitempty"`

	// Cost snapshot fields — set on cost_updated and budget_exceeded events,
	// from pipeline.CostSnapshot. Run-cumulative, not per-node. Estimated is
	// true when any contributing session was heuristic-derived.
	TotalTokens    int                               `json:"total_tokens,omitempty"`
	TotalCostUSD   float64                           `json:"total_cost_usd,omitempty"`
	ProviderTotals map[string]pipeline.ProviderUsage `json:"provider_totals,omitempty"`
	WallElapsedMs  int64                             `json:"wall_elapsed_ms,omitempty"`
	Estimated      bool                              `json:"estimated,omitempty"`

	// Truncation fields — set on tool_output_truncated events (#208).
	TruncStream   string `json:"trunc_stream,omitempty"`
	TruncLimit    int    `json:"trunc_limit,omitempty"`
	TruncCaptured int    `json:"trunc_captured_bytes,omitempty"`
	TruncDropped  int    `json:"trunc_dropped_bytes,omitempty"`
	TruncTotal    int    `json:"trunc_total_bytes,omitempty"`

	// Marker fields — set on tool_marker_missing events (#210).
	MarkerPattern string `json:"marker_pattern,omitempty"`
	MarkerTail    string `json:"marker_tail,omitempty"`
	MarkerError   string `json:"marker_error,omitempty"`

	// RouteTail is set on tool_route_missing events (#212).
	RouteTail string `json:"route_tail,omitempty"`

	// Auto-status fields — set on auto_status_missing events (#346).
	AutoStatusTail       string `json:"auto_status_tail,omitempty"`
	AutoStatusFailClosed bool   `json:"auto_status_fail_closed,omitempty"`

	// Override fields — set on validation_overridden events.
	OverrideGate         string         `json:"override_gate,omitempty"`
	OverrideLabel        string         `json:"override_label,omitempty"`
	OverrideActor        pipeline.Actor `json:"override_actor,omitempty"`
	OverrideSubgraphPath []string       `json:"override_subgraph_path,omitempty"`

	// GateID is set only on the gate lifecycle events (gate_opened /
	// gate_resolved) and correlates the pair (#509). NodeID identifies the gate
	// node on both.
	GateID string `json:"gate_id,omitempty"`
	// Gate payload fields, from pipeline.GateDetail. Open-time: GateMode,
	// GateLabel, GatePrompt, GateChoices, GateQuestions. Resolve-time:
	// GateResponse, GateOutcome, GateActor, GateTimedOut (plus Error above when
	// the gate failed to collect an answer). GateMode is repeated on the
	// resolution so GateResponse can be interpreted without joining.
	GateMode      string                  `json:"gate_mode,omitempty"`
	GateLabel     string                  `json:"gate_label,omitempty"`
	GatePrompt    string                  `json:"gate_prompt,omitempty"`
	GateChoices   []string                `json:"gate_choices,omitempty"`
	GateQuestions []pipeline.GateQuestion `json:"gate_questions,omitempty"`
	GateResponse  string                  `json:"gate_response,omitempty"`
	GateOutcome   string                  `json:"gate_outcome,omitempty"`
	GateActor     pipeline.Actor          `json:"gate_actor,omitempty"`
	GateTimedOut  bool                    `json:"gate_timed_out,omitempty"`

	// Run-reconstruction / capture fields (#519). These mirror the same-named
	// keys on pipeline's activity.jsonl entry so one struct decodes both NDJSON
	// schemas (the E1 parity contract). They carry the session/turn/call
	// identity, node kind, attempt number, untruncated tool input, and per-turn
	// economics that let a finished run be rebuilt as a tree. Populated on the
	// live --json wire too (#526): AgentHandler copies the agent identity/usage
	// detail and PipelineHandler copies NodeKind/AttemptNo, so the stream carries
	// the same parallel-branch attribution the audit log does. Still omitempty:
	// each field is present only on the event types that actually carry it.
	SessionID          string  `json:"session_id,omitempty"`
	ParentSessionID    string  `json:"parent_session_id,omitempty"`
	TurnNo             int     `json:"turn_no,omitempty"`
	AttemptNo          int     `json:"attempt_no,omitempty"`
	NodeKind           string  `json:"node_kind,omitempty"`
	ToolInput          string  `json:"tool_input,omitempty"`
	ToolDurationMs     int64   `json:"tool_duration_ms,omitempty"`
	CacheReadTokens    int     `json:"cache_read_tokens,omitempty"`
	CacheWriteTokens   int     `json:"cache_write_tokens,omitempty"`
	ReasoningTokens    int     `json:"reasoning_tokens,omitempty"`
	EstimatedCost      float64 `json:"estimated_cost,omitempty"`
	ContextUtilization float64 `json:"context_utilization,omitempty"`
	ToolCacheHits      int     `json:"tool_cache_hits,omitempty"`
	ToolCacheMisses    int     `json:"tool_cache_misses,omitempty"`
	TurnDurationMs     int64   `json:"turn_duration_ms,omitempty"`
	CallID             string  `json:"call_id,omitempty"`
	FinishReason       string  `json:"finish_reason,omitempty"`

	// Run snapshot fields — set on pipeline_started, from pipeline.RunSnapshot:
	// the top-level node inventory (sorted by ID, not execution order) plus
	// resume state, so a subscriber joining at run start can seed its progress
	// model without separate access to the graph or checkpoint.
	// SnapshotCurrentNode / SnapshotCompletedNodes are populated only on resume.
	// No activity.jsonl counterpart — these names are new.
	SnapshotNodes          []StreamSnapshotNode `json:"snapshot_nodes,omitempty"`
	SnapshotStartNode      string               `json:"snapshot_start_node,omitempty"`
	SnapshotExitNode       string               `json:"snapshot_exit_node,omitempty"`
	SnapshotCurrentNode    string               `json:"snapshot_current_node,omitempty"`
	SnapshotCompletedNodes []string             `json:"snapshot_completed_nodes,omitempty"`
}

// StreamSnapshotNode is one node of a pipeline_started run snapshot on the
// NDJSON wire. It mirrors pipeline.SnapshotNode with explicit wire tags.
type StreamSnapshotNode struct {
	ID      string `json:"id"`
	Label   string `json:"label,omitempty"`
	Handler string `json:"handler,omitempty"`
}

// NDJSONWriter is a thread-safe writer that serializes StreamEvents line by
// line onto an io.Writer. Library consumers use it to produce the same
// stream as the tracker CLI's --json mode.
//
// Backpressure note: Write holds an internal mutex for the duration of the
// underlying io.Writer.Write call. When three handler sources (pipeline,
// agent, LLM trace) share one writer, a slow backing writer serializes
// handler callbacks across those sources. If the backing writer can block
// (network socket, pipe), wrap it in a bufio.Writer or a channel-backed
// forwarder to decouple producers from the slow sink.
type NDJSONWriter struct {
	mu        sync.Mutex
	w         io.Writer
	errOnce   sync.Once
	panicOnce sync.Once
}

// NewNDJSONWriter returns a new writer backed by w.
func NewNDJSONWriter(w io.Writer) *NDJSONWriter {
	return &NDJSONWriter{w: w}
}

// Write serializes evt as a JSON line. Safe to call from multiple
// goroutines. Returns a non-nil error if marshalling or writing to the
// underlying io.Writer fails, including short writes (io.Writer.Write
// may legally return n < len(data) with a nil error). The first write
// error is also logged to os.Stderr once so long-running callers that
// ignore the return value still surface it.
func (s *NDJSONWriter) Write(evt StreamEvent) error {
	data, err := json.Marshal(evt)
	if err != nil {
		return fmt.Errorf("marshal NDJSON event: %w", err)
	}
	data = append(data, '\n')
	s.mu.Lock()
	defer s.mu.Unlock()
	n, werr := s.w.Write(data)
	if werr == nil && n < len(data) {
		werr = io.ErrShortWrite
	}
	if werr != nil {
		s.errOnce.Do(func() {
			fmt.Fprintf(os.Stderr, "tracker: NDJSON stream write error: %v (further write errors suppressed)\n", werr)
		})
		return werr
	}
	return nil
}

// PipelineHandler returns a pipeline.PipelineEventHandler that writes events
// to this stream. Panics in the underlying writer are recovered and logged
// to os.Stderr once (per writer instance) so a misbehaving sink cannot
// crash the pipeline goroutine.
func (s *NDJSONWriter) PipelineHandler() pipeline.PipelineEventHandler {
	return pipeline.PipelineEventHandlerFunc(func(evt pipeline.PipelineEvent) {
		defer s.recoverPanic("pipeline")
		entry := StreamEvent{
			Timestamp:      evt.Timestamp.Format(ndjsonTimestampLayout),
			Source:         "pipeline",
			Type:           string(evt.Type),
			RunID:          evt.RunID,
			NodeID:         evt.NodeID,
			Message:        evt.Message,
			TerminalStatus: evt.TerminalStatus,
			BundleIdentity: evt.BundleIdentity,
			NodeKind:       evt.NodeKind,
			AttemptNo:      evt.AttemptNo,
		}
		if evt.Err != nil {
			entry.Error = evt.Err.Error()
		}
		applyStreamPipelinePayloads(&entry, evt)
		_ = s.Write(entry)
	})
}

// TraceObserver returns an llm.TraceObserver that writes trace events to
// this stream. Panics in the underlying writer are recovered (see
// PipelineHandler).
func (s *NDJSONWriter) TraceObserver() llm.TraceObserver {
	return llm.TraceObserverFunc(func(evt llm.TraceEvent) {
		defer s.recoverPanic("llm")
		entry := StreamEvent{
			Timestamp: time.Now().Format(ndjsonTimestampLayout),
			Source:    "llm",
			Type:      string(evt.Kind),
			Provider:  evt.Provider,
			Model:     evt.Model,
			ToolName:  evt.ToolName,
			Content:   evt.Preview,
		}
		applyStreamUsage(&entry, evt.Usage, 0)
		_ = s.Write(entry)
	})
}

// AgentHandler returns an agent.EventHandler that writes agent events to
// this stream. Panics in the underlying writer are recovered (see
// PipelineHandler).
func (s *NDJSONWriter) AgentHandler() agent.EventHandler {
	return agent.EventHandlerFunc(func(evt agent.Event) {
		defer s.recoverPanic("agent")
		content := evt.ToolOutput
		if content == "" {
			content = evt.Text
		}
		ts := evt.Timestamp
		if ts.IsZero() {
			ts = time.Now()
		}
		entry := StreamEvent{
			Timestamp: ts.Format(ndjsonTimestampLayout),
			Source:    "agent",
			Type:      string(evt.Type),
			NodeID:    evt.NodeID,
			Provider:  evt.Provider,
			Model:     evt.Model,
			ToolName:  evt.ToolName,
			Content:   content,
		}
		entry.Error = buildStreamEntryError(evt)
		applyStreamUsage(&entry, evt.Usage, turnMetricsCost(evt.Metrics))
		applyStreamAgentCapture(&entry, evt)
		_ = s.Write(entry)
	})
}

// recoverPanic recovers from a handler panic and logs the first occurrence
// per writer instance. Using a per-instance sync.Once (not package-level)
// means multiple NDJSONWriter instances (e.g., different runs streaming to
// different sinks) each get their own suppression state, so one misbehaving
// sink does not silence unrelated panics elsewhere in the process.
func (s *NDJSONWriter) recoverPanic(source string) {
	if r := recover(); r != nil {
		s.panicOnce.Do(func() {
			fmt.Fprintf(os.Stderr, "tracker: NDJSON %s handler recovered from panic: %v (further panics suppressed)\n", source, r)
		})
	}
}

func buildStreamEntryError(evt agent.Event) string {
	if evt.ToolError == "" && evt.Err == nil {
		return ""
	}
	if evt.ToolError != "" && evt.Err != nil {
		return evt.ToolError + ": " + evt.Err.Error()
	}
	if evt.ToolError != "" {
		return evt.ToolError
	}
	return evt.Err.Error()
}
