// ABOUTME: golden-trace conformance fixtures — deterministic engine runs driven
// ABOUTME: by a stub completer (no API keys) and snapshotted so downstream ports
// ABOUTME: can diff for event-schema / handler-contract / usage-shape drift.
package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"

	"github.com/2389-research/tracker"
	"github.com/2389-research/tracker/llm"
	"github.com/2389-research/tracker/pipeline"
)

// goldenSchemaVersion is bumped when the golden-trace document shape itself
// changes (a new top-level field, a renamed key). Downstream harnesses pin a
// (tracker version, schema version) pair and refuse to diff across a bump.
const goldenSchemaVersion = "1"

// stubCompleter is a deterministic agent.Completer: it returns a fixed response
// and fixed token usage for every turn, so a codergen node produces stable
// SessionStats/UsageSummary without any live provider. Provider "stub" has no
// entry in the pricing table, so cost stays a deterministic 0.
type stubCompleter struct{}

func (stubCompleter) Complete(_ context.Context, _ *llm.Request) (*llm.Response, error) {
	return &llm.Response{
		ID:           "stub-response",
		Model:        "stub-model",
		Provider:     "stub",
		Message:      llm.AssistantMessage("golden-trace deterministic response"),
		FinishReason: llm.FinishReason{Reason: "stop"},
		Usage:        llm.Usage{InputTokens: 100, OutputTokens: 50, TotalTokens: 150},
	}, nil
}

// goldenTrace is the normalized, deterministic snapshot emitted per fixture.
// Volatile fields (timestamps, run IDs, durations, absolute paths) are stripped
// so the document is byte-stable across runs and machines.
type goldenTrace struct {
	SchemaVersion  string                 `json:"schema_version"`
	Pipeline       string                 `json:"pipeline"`
	TerminalStatus string                 `json:"terminal_status"`
	StatusClass    string                 `json:"status_class"`
	CompletedNodes []string               `json:"completed_nodes"`
	Events         []goldenEvent          `json:"events"`
	TraceEntries   []goldenEntry          `json:"trace_entries"`
	Usage          *pipeline.UsageSummary `json:"usage"`
}

// goldenEvent captures only the drift-relevant fields of a PipelineEvent: the
// event type and the node it targets. Message text and typed sub-payloads carry
// volatile content, so they are intentionally excluded.
type goldenEvent struct {
	Type   string `json:"type"`
	NodeID string `json:"node_id,omitempty"`
}

// goldenEntry is a normalized TraceEntry: node identity + status + edge routed,
// plus the full SessionStats shape with the one volatile field (longest_turn)
// removed.
type goldenEntry struct {
	NodeID      string         `json:"node_id"`
	HandlerName string         `json:"handler_name"`
	Status      string         `json:"status"`
	EdgeTo      string         `json:"edge_to,omitempty"`
	Stats       map[string]any `json:"stats,omitempty"`
}

// handleGolden runs a fixture through the library seam with the stub completer
// and writes its normalized golden-trace JSON to stdout.
func handleGolden(args []string, stdout, _ io.Writer) int {
	if len(args) < 3 {
		writeJSON(stdout, map[string]string{"error": "usage: tracker-conformance golden <dotfile>"})
		return 1
	}
	gt, err := generateGoldenTrace(args[2])
	if err != nil {
		writeJSON(stdout, map[string]string{"error": err.Error()})
		return 1
	}
	writeJSON(stdout, gt)
	return 0
}

// generateGoldenTrace executes one fixture deterministically and returns its
// normalized snapshot. It drives the run through tracker.Run + tracker.Config —
// the same library seam downstream products embed — so the fixtures double as a
// smoke test of that surface.
func generateGoldenTrace(fixture string) (*goldenTrace, error) {
	workDir, err := os.MkdirTemp("", "golden-*")
	if err != nil {
		return nil, fmt.Errorf("temp workdir: %w", err)
	}
	defer os.RemoveAll(workDir)

	source, err := os.ReadFile(fixture)
	if err != nil {
		return nil, fmt.Errorf("read fixture: %w", err)
	}

	var events []goldenEvent
	collector := pipeline.PipelineEventHandlerFunc(func(evt pipeline.PipelineEvent) {
		events = append(events, goldenEvent{Type: string(evt.Type), NodeID: evt.NodeID})
	})

	cfg := tracker.Config{
		WorkingDir:   workDir,
		LLMClient:    stubCompleter{},
		EventHandler: collector,
		AutoApprove:  true,
		Model:        "stub-model",
		Provider:     "stub",
	}

	// A run that ends in a terminal failure (e.g. a strict-failure-edge stop)
	// returns a non-nil *Result alongside a non-nil error — that is the exact
	// failure-path contract a golden fixture must pin, so keep the result and
	// only treat a nil result (parse/init failure) as fatal.
	result, runErr := tracker.Run(context.Background(), string(source), cfg)
	if result == nil {
		return nil, fmt.Errorf("run produced no result: %w", runErr)
	}

	return buildGoldenTrace(fixture, result, events), nil
}

// buildGoldenTrace assembles the normalized document from a completed run.
func buildGoldenTrace(fixture string, result *tracker.Result, events []goldenEvent) *goldenTrace {
	statusClass := "failed"
	if pipeline.TerminalStatus(result.Status).IsSuccess() {
		statusClass = "succeeded"
	}

	gt := &goldenTrace{
		SchemaVersion:  goldenSchemaVersion,
		Pipeline:       filepath.Base(fixture),
		TerminalStatus: result.Status,
		StatusClass:    statusClass,
		CompletedNodes: result.CompletedNodes,
		Events:         events,
	}

	if result.Trace != nil {
		for _, e := range result.Trace.Entries {
			gt.TraceEntries = append(gt.TraceEntries, goldenEntry{
				NodeID:      e.NodeID,
				HandlerName: e.HandlerName,
				Status:      e.Status,
				EdgeTo:      e.EdgeTo,
				Stats:       normalizeStats(e.Stats),
			})
		}
		gt.Usage = result.Trace.AggregateUsage()
	}

	return gt
}

// normalizeStats JSON-roundtrips SessionStats to a map and drops the one
// non-deterministic field (longest_turn, a wall-clock duration). Every other
// field is deterministic under the stub completer. Returns nil for a node with
// no stats (start/exit/tool/gate/conditional), keeping the JSON compact.
func normalizeStats(s *pipeline.SessionStats) map[string]any {
	if s == nil {
		return nil
	}
	b, err := json.Marshal(s)
	if err != nil {
		return nil
	}
	var m map[string]any
	if err := json.Unmarshal(b, &m); err != nil {
		return nil
	}
	delete(m, "longest_turn")
	return m
}
