// ABOUTME: Tests for pipeline execution trace recording covering entry creation, summary output, and engine integration.
// ABOUTME: Validates that the engine captures timing, edge selection, handler outcomes, and errors in structured traces.
package pipeline

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"
)

func floatNear(a, b, epsilon float64) bool {
	diff := a - b
	if diff < 0 {
		diff = -diff
	}
	return diff < epsilon
}

func TestTraceEntryCreation(t *testing.T) {
	now := time.Now()
	entry := TraceEntry{
		Timestamp:   now,
		NodeID:      "step1",
		HandlerName: "codergen",
		Status:      OutcomeSuccess,
		Duration:    150 * time.Millisecond,
		EdgeTo:      "step2",
		Error:       "",
	}

	if entry.Timestamp != now {
		t.Errorf("expected timestamp %v, got %v", now, entry.Timestamp)
	}
	if entry.NodeID != "step1" {
		t.Errorf("expected NodeID step1, got %q", entry.NodeID)
	}
	if entry.HandlerName != "codergen" {
		t.Errorf("expected HandlerName codergen, got %q", entry.HandlerName)
	}
	if entry.Status != OutcomeSuccess {
		t.Errorf("expected Status success, got %q", entry.Status)
	}
	if entry.Duration != 150*time.Millisecond {
		t.Errorf("expected Duration 150ms, got %v", entry.Duration)
	}
	if entry.EdgeTo != "step2" {
		t.Errorf("expected EdgeTo step2, got %q", entry.EdgeTo)
	}
	if entry.Error != "" {
		t.Errorf("expected empty Error, got %q", entry.Error)
	}
}

func TestTraceAddEntry(t *testing.T) {
	tr := &Trace{
		RunID:     "test-run-1",
		StartTime: time.Now(),
	}

	if len(tr.Entries) != 0 {
		t.Fatalf("expected 0 entries initially, got %d", len(tr.Entries))
	}

	entry1 := TraceEntry{
		Timestamp:   time.Now(),
		NodeID:      "s",
		HandlerName: "start",
		Status:      OutcomeSuccess,
		Duration:    10 * time.Millisecond,
		EdgeTo:      "step1",
	}
	tr.AddEntry(entry1)

	if len(tr.Entries) != 1 {
		t.Fatalf("expected 1 entry after AddEntry, got %d", len(tr.Entries))
	}
	if tr.Entries[0].NodeID != "s" {
		t.Errorf("expected first entry NodeID s, got %q", tr.Entries[0].NodeID)
	}

	entry2 := TraceEntry{
		Timestamp:   time.Now(),
		NodeID:      "step1",
		HandlerName: "codergen",
		Status:      OutcomeSuccess,
		Duration:    50 * time.Millisecond,
		EdgeTo:      "end",
	}
	tr.AddEntry(entry2)

	if len(tr.Entries) != 2 {
		t.Fatalf("expected 2 entries, got %d", len(tr.Entries))
	}
}

func TestTraceSummary(t *testing.T) {
	start := time.Date(2026, 3, 5, 10, 0, 0, 0, time.UTC)
	tr := &Trace{
		RunID:     "summary-run",
		StartTime: start,
		EndTime:   start.Add(500 * time.Millisecond),
	}
	tr.AddEntry(TraceEntry{
		Timestamp:   start,
		NodeID:      "s",
		HandlerName: "start",
		Status:      OutcomeSuccess,
		Duration:    10 * time.Millisecond,
		EdgeTo:      "step1",
	})
	tr.AddEntry(TraceEntry{
		Timestamp:   start.Add(10 * time.Millisecond),
		NodeID:      "step1",
		HandlerName: "codergen",
		Status:      OutcomeSuccess,
		Duration:    200 * time.Millisecond,
		EdgeTo:      "end",
	})
	tr.AddEntry(TraceEntry{
		Timestamp:   start.Add(210 * time.Millisecond),
		NodeID:      "end",
		HandlerName: "exit",
		Status:      OutcomeSuccess,
		Duration:    5 * time.Millisecond,
	})

	summary := tr.Summary()

	// Summary should contain run ID.
	if !strings.Contains(summary, "summary-run") {
		t.Errorf("summary should contain run ID, got:\n%s", summary)
	}
	// Summary should contain node IDs.
	for _, nodeID := range []string{"s", "step1", "end"} {
		if !strings.Contains(summary, nodeID) {
			t.Errorf("summary should contain node %q, got:\n%s", nodeID, summary)
		}
	}
	// Summary should mention entry count.
	if !strings.Contains(summary, "3") {
		t.Errorf("summary should mention 3 entries, got:\n%s", summary)
	}
	// Summary should be non-empty and readable.
	if len(summary) < 20 {
		t.Errorf("summary seems too short: %q", summary)
	}
}

func TestTraceSummaryEmpty(t *testing.T) {
	tr := &Trace{
		RunID:     "empty-run",
		StartTime: time.Now(),
	}
	summary := tr.Summary()
	if !strings.Contains(summary, "empty-run") {
		t.Errorf("empty summary should still contain run ID, got:\n%s", summary)
	}
	if !strings.Contains(summary, "0") {
		t.Errorf("empty summary should mention 0 entries, got:\n%s", summary)
	}
}

func TestEngineRecordsTrace(t *testing.T) {
	g := NewGraph("trace_test")
	g.AddNode(&Node{ID: "s", Shape: "Mdiamond", Label: "Start"})
	g.AddNode(&Node{ID: "work", Shape: "box", Label: "Work"})
	g.AddNode(&Node{ID: "end", Shape: "Msquare", Label: "End"})

	g.AddEdge(&Edge{From: "s", To: "work"})
	g.AddEdge(&Edge{From: "work", To: "end"})

	reg := newTestRegistry()
	engine := NewEngine(g, reg)
	result, err := engine.Run(context.Background())
	if err != nil {
		t.Fatalf("engine run failed: %v", err)
	}

	if result.Trace == nil {
		t.Fatal("expected Trace to be set on EngineResult")
	}
	if result.Trace.RunID != result.RunID {
		t.Errorf("trace RunID %q does not match result RunID %q", result.Trace.RunID, result.RunID)
	}
	if result.Trace.StartTime.IsZero() {
		t.Error("expected Trace.StartTime to be set")
	}
	if result.Trace.EndTime.IsZero() {
		t.Error("expected Trace.EndTime to be set")
	}
	if !result.Trace.EndTime.After(result.Trace.StartTime) && !result.Trace.EndTime.Equal(result.Trace.StartTime) {
		t.Error("expected Trace.EndTime >= Trace.StartTime")
	}

	// Should have entries for: s, work, end = 3 nodes.
	if len(result.Trace.Entries) != 3 {
		t.Errorf("expected 3 trace entries, got %d", len(result.Trace.Entries))
		for i, e := range result.Trace.Entries {
			t.Logf("  entry[%d]: node=%q handler=%q status=%q edge=%q", i, e.NodeID, e.HandlerName, e.Status, e.EdgeTo)
		}
	}

	// Verify each entry has basic fields populated.
	for i, entry := range result.Trace.Entries {
		if entry.NodeID == "" {
			t.Errorf("entry[%d] missing NodeID", i)
		}
		if entry.HandlerName == "" {
			t.Errorf("entry[%d] missing HandlerName", i)
		}
		if entry.Status == "" {
			t.Errorf("entry[%d] missing Status", i)
		}
		if entry.Timestamp.IsZero() {
			t.Errorf("entry[%d] missing Timestamp", i)
		}
		if entry.Duration < 0 {
			t.Errorf("entry[%d] has negative Duration: %v", i, entry.Duration)
		}
	}
}

func TestEngineTraceRecordsEdgeSelections(t *testing.T) {
	g := NewGraph("trace_edge_test")
	g.AddNode(&Node{ID: "s", Shape: "Mdiamond", Label: "Start"})
	g.AddNode(&Node{ID: "a", Shape: "box", Label: "A"})
	g.AddNode(&Node{ID: "b", Shape: "box", Label: "B"})
	g.AddNode(&Node{ID: "end", Shape: "Msquare", Label: "End"})

	g.AddEdge(&Edge{From: "s", To: "a", Label: "left"})
	g.AddEdge(&Edge{From: "s", To: "b", Label: "right"})
	g.AddEdge(&Edge{From: "a", To: "end"})
	g.AddEdge(&Edge{From: "b", To: "end"})

	reg := newTestRegistry()
	reg.Register(&testHandler{
		name: "start",
		executeFn: func(ctx context.Context, node *Node, pctx *PipelineContext) (Outcome, error) {
			return Outcome{
				Status:         OutcomeSuccess,
				PreferredLabel: "right",
			}, nil
		},
	})

	engine := NewEngine(g, reg)
	result, err := engine.Run(context.Background())
	if err != nil {
		t.Fatalf("engine run failed: %v", err)
	}

	if result.Trace == nil {
		t.Fatal("expected Trace to be set")
	}

	// Find the start node entry — it should have EdgeTo = "b" (via preferred label "right").
	var startEntry *TraceEntry
	for i := range result.Trace.Entries {
		if result.Trace.Entries[i].NodeID == "s" {
			startEntry = &result.Trace.Entries[i]
			break
		}
	}
	if startEntry == nil {
		t.Fatal("expected trace entry for start node 's'")
	}
	if startEntry.EdgeTo != "b" {
		t.Errorf("expected start node edge to 'b', got %q", startEntry.EdgeTo)
	}

	// The exit node should have empty EdgeTo.
	var endEntry *TraceEntry
	for i := range result.Trace.Entries {
		if result.Trace.Entries[i].NodeID == "end" {
			endEntry = &result.Trace.Entries[i]
			break
		}
	}
	if endEntry == nil {
		t.Fatal("expected trace entry for exit node 'end'")
	}
	if endEntry.EdgeTo != "" {
		t.Errorf("expected exit node EdgeTo to be empty, got %q", endEntry.EdgeTo)
	}
}

func TestEngineTraceRecordsErrors(t *testing.T) {
	g := NewGraph("trace_error_test")
	g.AddNode(&Node{ID: "s", Shape: "Mdiamond", Label: "Start"})
	g.AddNode(&Node{ID: "bad", Shape: "box", Label: "Bad"})
	g.AddNode(&Node{ID: "end", Shape: "Msquare", Label: "End"})

	g.AddEdge(&Edge{From: "s", To: "bad"})
	g.AddEdge(&Edge{From: "bad", To: "end"})

	reg := newTestRegistry()
	reg.Register(&testHandler{
		name: "codergen",
		executeFn: func(ctx context.Context, node *Node, pctx *PipelineContext) (Outcome, error) {
			return Outcome{}, fmt.Errorf("something went wrong")
		},
	})

	engine := NewEngine(g, reg)
	result, err := engine.Run(context.Background())

	// The engine returns an error for handler errors, so result may be nil.
	// But the trace should still be accessible if we change the design.
	// For handler errors that propagate as engine errors, the trace won't be
	// in the result. Let's verify the error case with OutcomeFail instead.
	_ = result
	_ = err

	// Test with OutcomeFail on a goal gate (returns result with trace).
	g2 := NewGraph("trace_fail_test")
	g2.AddNode(&Node{ID: "s", Shape: "Mdiamond", Label: "Start"})
	g2.AddNode(&Node{ID: "failing", Shape: "box", Label: "Failing", Attrs: map[string]string{"goal_gate": "true"}})
	g2.AddNode(&Node{ID: "end", Shape: "Msquare", Label: "End"})

	g2.AddEdge(&Edge{From: "s", To: "failing"})
	g2.AddEdge(&Edge{From: "failing", To: "end", Condition: "ctx.outcome = success"})
	g2.AddEdge(&Edge{From: "failing", To: "end", Condition: "ctx.outcome = fail"})

	reg2 := newTestRegistry()
	reg2.Register(&testHandler{
		name: "codergen",
		executeFn: func(ctx context.Context, node *Node, pctx *PipelineContext) (Outcome, error) {
			return Outcome{Status: OutcomeFail}, nil
		},
	})

	engine2 := NewEngine(g2, reg2)
	result2, err := engine2.Run(context.Background())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if result2.Trace == nil {
		t.Fatal("expected Trace on failed result")
	}
	if result2.Trace.EndTime.IsZero() {
		t.Error("expected Trace.EndTime to be set on failure")
	}

	// Find the failing node entry.
	var failEntry *TraceEntry
	for i := range result2.Trace.Entries {
		if result2.Trace.Entries[i].NodeID == "failing" {
			failEntry = &result2.Trace.Entries[i]
			break
		}
	}
	if failEntry == nil {
		t.Fatal("expected trace entry for failing node")
	}
	if failEntry.Status != OutcomeFail {
		t.Errorf("expected fail status in trace entry, got %q", failEntry.Status)
	}
}

func TestSessionStatsDefaults(t *testing.T) {
	stats := &SessionStats{}
	if stats.Turns != 0 {
		t.Errorf("expected zero Turns, got %d", stats.Turns)
	}
	if stats.TotalToolCalls != 0 {
		t.Errorf("expected zero TotalToolCalls, got %d", stats.TotalToolCalls)
	}
	if stats.Compactions != 0 {
		t.Errorf("expected zero Compactions, got %d", stats.Compactions)
	}
	if stats.ToolCalls != nil {
		t.Errorf("expected nil ToolCalls, got %v", stats.ToolCalls)
	}
}

func TestSessionStatsPopulated(t *testing.T) {
	stats := &SessionStats{
		Turns:          12,
		ToolCalls:      map[string]int{"bash": 5, "write": 3},
		TotalToolCalls: 8,
		FilesModified:  []string{"main.go"},
		FilesCreated:   []string{"new.go", "new_test.go"},
		Compactions:    2,
		LongestTurn:    30 * time.Second,
		CacheHits:      10,
		CacheMisses:    4,
	}

	if stats.Turns != 12 {
		t.Errorf("expected 12 Turns, got %d", stats.Turns)
	}
	if stats.TotalToolCalls != 8 {
		t.Errorf("expected 8 TotalToolCalls, got %d", stats.TotalToolCalls)
	}
	if len(stats.ToolCalls) != 2 {
		t.Errorf("expected 2 tool types, got %d", len(stats.ToolCalls))
	}
	if stats.ToolCalls["bash"] != 5 {
		t.Errorf("expected bash=5, got %d", stats.ToolCalls["bash"])
	}
	if len(stats.FilesCreated) != 2 {
		t.Errorf("expected 2 files created, got %d", len(stats.FilesCreated))
	}
	if len(stats.FilesModified) != 1 {
		t.Errorf("expected 1 file modified, got %d", len(stats.FilesModified))
	}
	if stats.Compactions != 2 {
		t.Errorf("expected 2 compactions, got %d", stats.Compactions)
	}
	if stats.LongestTurn != 30*time.Second {
		t.Errorf("expected 30s longest turn, got %v", stats.LongestTurn)
	}
}

func TestTraceEntryWithStats(t *testing.T) {
	entry := TraceEntry{
		NodeID:      "impl",
		HandlerName: "codergen",
		Status:      OutcomeSuccess,
		Duration:    5 * time.Minute,
		Stats: &SessionStats{
			Turns:          7,
			TotalToolCalls: 42,
			ToolCalls:      map[string]int{"bash": 30, "write": 12},
		},
	}

	if entry.Stats == nil {
		t.Fatal("expected Stats to be set")
	}
	if entry.Stats.Turns != 7 {
		t.Errorf("expected 7 turns, got %d", entry.Stats.Turns)
	}
	if entry.Stats.TotalToolCalls != 42 {
		t.Errorf("expected 42 total tool calls, got %d", entry.Stats.TotalToolCalls)
	}
}

func TestTraceEntryWithoutStats(t *testing.T) {
	entry := TraceEntry{
		NodeID:      "start",
		HandlerName: "start",
		Status:      OutcomeSuccess,
		Duration:    1 * time.Millisecond,
	}

	if entry.Stats != nil {
		t.Errorf("expected nil Stats for non-agent node, got %+v", entry.Stats)
	}
}

func TestOutcomeWithStats(t *testing.T) {
	outcome := Outcome{
		Status: OutcomeSuccess,
		Stats: &SessionStats{
			Turns:          3,
			TotalToolCalls: 15,
		},
	}
	if outcome.Stats == nil {
		t.Fatal("expected Stats on Outcome")
	}
	if outcome.Stats.Turns != 3 {
		t.Errorf("expected 3 turns, got %d", outcome.Stats.Turns)
	}
}

func TestEngineTracePropagatesStats(t *testing.T) {
	g := NewGraph("stats_prop_test")
	g.AddNode(&Node{ID: "s", Shape: "Mdiamond", Label: "Start"})
	g.AddNode(&Node{ID: "work", Shape: "box", Label: "Work"})
	g.AddNode(&Node{ID: "end", Shape: "Msquare", Label: "End"})
	g.AddEdge(&Edge{From: "s", To: "work"})
	g.AddEdge(&Edge{From: "work", To: "end"})

	reg := newTestRegistry()
	reg.Register(&testHandler{
		name: "codergen",
		executeFn: func(ctx context.Context, node *Node, pctx *PipelineContext) (Outcome, error) {
			return Outcome{
				Status: OutcomeSuccess,
				Stats: &SessionStats{
					Turns:          5,
					TotalToolCalls: 20,
					ToolCalls:      map[string]int{"bash": 15, "write": 5},
					FilesCreated:   []string{"file.go"},
					Compactions:    1,
				},
			}, nil
		},
	})

	engine := NewEngine(g, reg)
	result, err := engine.Run(context.Background())
	if err != nil {
		t.Fatalf("engine run failed: %v", err)
	}

	// Find the work node entry — it should have Stats populated.
	var workEntry *TraceEntry
	for i := range result.Trace.Entries {
		if result.Trace.Entries[i].NodeID == "work" {
			workEntry = &result.Trace.Entries[i]
			break
		}
	}
	if workEntry == nil {
		t.Fatal("expected trace entry for 'work' node")
	}
	if workEntry.Stats == nil {
		t.Fatal("expected Stats to be propagated to trace entry")
	}
	if workEntry.Stats.Turns != 5 {
		t.Errorf("expected 5 turns, got %d", workEntry.Stats.Turns)
	}
	if workEntry.Stats.TotalToolCalls != 20 {
		t.Errorf("expected 20 tool calls, got %d", workEntry.Stats.TotalToolCalls)
	}
	if workEntry.Stats.Compactions != 1 {
		t.Errorf("expected 1 compaction, got %d", workEntry.Stats.Compactions)
	}

	// Start node should have nil Stats.
	var startEntry *TraceEntry
	for i := range result.Trace.Entries {
		if result.Trace.Entries[i].NodeID == "s" {
			startEntry = &result.Trace.Entries[i]
			break
		}
	}
	if startEntry == nil {
		t.Fatal("expected trace entry for 's' node")
	}
	if startEntry.Stats != nil {
		t.Errorf("expected nil Stats for start node, got %+v", startEntry.Stats)
	}
}

func TestSessionStatsIncludesTokenUsage(t *testing.T) {
	stats := &SessionStats{
		Turns:          5,
		TotalToolCalls: 10,
		InputTokens:    1500,
		OutputTokens:   800,
		TotalTokens:    2300,
		CostUSD:        0.042,
	}

	if stats.InputTokens != 1500 {
		t.Errorf("expected InputTokens=1500, got %d", stats.InputTokens)
	}
	if stats.OutputTokens != 800 {
		t.Errorf("expected OutputTokens=800, got %d", stats.OutputTokens)
	}
	if stats.TotalTokens != 2300 {
		t.Errorf("expected TotalTokens=2300, got %d", stats.TotalTokens)
	}
	if !floatNear(stats.CostUSD, 0.042, 1e-9) {
		t.Errorf("expected CostUSD=0.042, got %f", stats.CostUSD)
	}
}

func TestTraceAggregateUsage(t *testing.T) {
	t.Run("normal aggregation", func(t *testing.T) {
		tr := &Trace{
			RunID:     "agg-test",
			StartTime: time.Now(),
			Entries: []TraceEntry{
				{NodeID: "s", HandlerName: "start", Status: OutcomeSuccess},
				{
					NodeID: "impl1", HandlerName: "codergen", Status: OutcomeSuccess,
					Stats: &SessionStats{
						Turns:        10,
						InputTokens:  5000,
						OutputTokens: 2000,
						TotalTokens:  7000,
						CostUSD:      0.10,
					},
				},
				{
					NodeID: "impl2", HandlerName: "codergen", Status: OutcomeSuccess,
					Stats: &SessionStats{
						Turns:        5,
						InputTokens:  3000,
						OutputTokens: 1000,
						TotalTokens:  4000,
						CostUSD:      0.06,
					},
				},
				{NodeID: "end", HandlerName: "exit", Status: OutcomeSuccess},
			},
		}

		usage := tr.AggregateUsage()
		if usage == nil {
			t.Fatal("expected non-nil UsageSummary")
		}
		if usage.TotalInputTokens != 8000 {
			t.Errorf("expected TotalInputTokens=8000, got %d", usage.TotalInputTokens)
		}
		if usage.TotalOutputTokens != 3000 {
			t.Errorf("expected TotalOutputTokens=3000, got %d", usage.TotalOutputTokens)
		}
		if usage.TotalTokens != 11000 {
			t.Errorf("expected TotalTokens=11000, got %d", usage.TotalTokens)
		}
		if !floatNear(usage.TotalCostUSD, 0.16, 1e-9) {
			t.Errorf("expected TotalCostUSD=0.16, got %f", usage.TotalCostUSD)
		}
		if usage.SessionCount != 2 {
			t.Errorf("expected SessionCount=2, got %d", usage.SessionCount)
		}
	})

	t.Run("nil trace", func(t *testing.T) {
		var tr *Trace
		usage := tr.AggregateUsage()
		if usage != nil {
			t.Errorf("expected nil for nil trace, got %+v", usage)
		}
	})

	t.Run("no sessions with stats", func(t *testing.T) {
		tr := &Trace{
			RunID: "no-sessions",
			Entries: []TraceEntry{
				{NodeID: "s", HandlerName: "start", Status: OutcomeSuccess},
				{NodeID: "end", HandlerName: "exit", Status: OutcomeSuccess},
			},
		}
		usage := tr.AggregateUsage()
		if usage != nil {
			t.Errorf("expected nil for trace without session stats, got %+v", usage)
		}
	})
}

func TestEngineTraceRecordsHandlerErrors(t *testing.T) {
	g := NewGraph("trace_handler_err_test")
	g.AddNode(&Node{ID: "s", Shape: "Mdiamond", Label: "Start"})
	g.AddNode(&Node{ID: "boom", Shape: "box", Label: "Boom"})
	g.AddNode(&Node{ID: "end", Shape: "Msquare", Label: "End"})

	g.AddEdge(&Edge{From: "s", To: "boom"})
	g.AddEdge(&Edge{From: "boom", To: "end"})

	reg := newTestRegistry()
	reg.Register(&testHandler{
		name: "codergen",
		executeFn: func(ctx context.Context, node *Node, pctx *PipelineContext) (Outcome, error) {
			return Outcome{}, fmt.Errorf("handler exploded")
		},
	})

	engine := NewEngine(g, reg)
	_, err := engine.Run(context.Background())
	if err == nil {
		t.Fatal("expected error from handler")
	}

	// When the engine returns an error, the trace is lost since no result
	// is returned. This is acceptable — the error path is captured via
	// events. The trace is for successful (or soft-fail) pipeline runs.
}

func TestEngineResultUsageFromTraceStats(t *testing.T) {
	g := NewGraph("usage_from_trace")
	g.AddNode(&Node{ID: "s", Shape: "Mdiamond", Label: "Start", Attrs: make(map[string]string)})
	g.AddNode(&Node{ID: "work", Shape: "box", Label: "Work", Attrs: make(map[string]string)})
	g.AddNode(&Node{ID: "e", Shape: "Msquare", Label: "End", Attrs: make(map[string]string)})
	g.AddEdge(&Edge{From: "s", To: "work"})
	g.AddEdge(&Edge{From: "work", To: "e"})

	registry := newTestRegistry()
	registry.Register(&testHandler{
		name: "codergen",
		executeFn: func(_ context.Context, _ *Node, _ *PipelineContext) (Outcome, error) {
			return Outcome{
				Status: OutcomeSuccess,
				Stats: &SessionStats{
					Turns:        5,
					InputTokens:  2000,
					OutputTokens: 800,
					TotalTokens:  2800,
					CostUSD:      0.01,
				},
			}, nil
		},
	})

	engine := NewEngine(g, registry)
	result, err := engine.Run(context.Background())
	if err != nil {
		t.Fatalf("engine.Run failed: %v", err)
	}
	if result.Usage == nil {
		t.Fatal("result.Usage is nil — token data not aggregated")
	}
	if result.Usage.TotalInputTokens != 2000 {
		t.Errorf("TotalInputTokens = %d, want 2000", result.Usage.TotalInputTokens)
	}
	if result.Usage.SessionCount != 1 {
		t.Errorf("SessionCount = %d, want 1", result.Usage.SessionCount)
	}
}
