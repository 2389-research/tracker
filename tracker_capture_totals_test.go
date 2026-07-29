// ABOUTME: Cross-checks run.json's log-derived totals against the engine's own independently-derived usage.
// ABOUTME: The manifest reads the activity log; the engine counts as it goes — when they disagree, the manifest is wrong.
package tracker

import (
	"context"
	"encoding/json"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/2389-research/tracker/agent"
	"github.com/2389-research/tracker/llm"
	"github.com/2389-research/tracker/pipeline"
)

// TestRunManifestTotalsMatchEngineUsage is the deterministic regression test for
// a live run reporting three times its real token usage.
//
// It works because the two figures come from genuinely independent places: the
// engine accumulates usage from each Response as it runs, while the manifest
// re-derives it by reading the activity log after the fact. The log states the
// same consumption at three granularities (the call, the turn, the node), so a
// manifest that sums across them inflates while the engine stays right.
//
// A third, fully independent oracle is the stub itself: it counts how many times
// it was asked to complete, which is by definition the number of LLM calls.
func TestRunManifestTotalsMatchEngineUsage(t *testing.T) {
	workDir := t.TempDir()
	handler := pipeline.NewJSONLEventHandler(filepath.Join(workDir, ".tracker", "runs"))
	stub := &toolCallingStub{}

	// All three capture seams, wired exactly as the CLI wires them. Omitting
	// LLMTrace is what makes llm_calls unknowable: usage still reaches the log
	// through turn and node events, but nothing records the individual calls.
	result, err := Run(context.Background(), captureDip, Config{
		Format:       "dip",
		WorkingDir:   workDir,
		LLMClient:    stub,
		EventHandler: handler,
		AgentEvents:  agent.EventHandlerFunc(handler.WriteAgentEvent),
		LLMTrace:     handler.LLMTraceObserver(),
	})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if err := handler.Close(); err != nil {
		t.Fatalf("close log: %v", err)
	}

	runDir := filepath.Join(workDir, ".tracker", "runs", result.RunID)
	m, err := pipeline.AssembleRunManifest(runDir, result.RunID)
	if err != nil {
		t.Fatalf("assemble manifest: %v", err)
	}

	// What the engine counted, taken from the per-node session stats it
	// accumulated while running. Per-provider totals are not usable here: they
	// key on a provider name the stub never sets.
	engineByNode := map[string]llm.Usage{}
	var engine llm.Usage
	for _, e := range result.Trace.Entries {
		if e.Stats == nil {
			continue
		}
		engine.InputTokens += e.Stats.InputTokens
		engine.OutputTokens += e.Stats.OutputTokens
		engineByNode[e.NodeID] = llm.Usage{
			InputTokens:  e.Stats.InputTokens,
			OutputTokens: e.Stats.OutputTokens,
		}
	}
	if engine.InputTokens == 0 {
		t.Fatal("engine recorded no usage; the test cannot compare anything")
	}

	t.Run("token totals agree with the engine", func(t *testing.T) {
		if m.Totals.InputTokens != engine.InputTokens {
			t.Errorf("manifest input_tokens = %d, engine counted %d (ratio %.2f — a whole-number ratio means a tier was summed twice)",
				m.Totals.InputTokens, engine.InputTokens,
				float64(m.Totals.InputTokens)/float64(engine.InputTokens))
		}
		if m.Totals.OutputTokens != engine.OutputTokens {
			t.Errorf("manifest output_tokens = %d, engine counted %d",
				m.Totals.OutputTokens, engine.OutputTokens)
		}
	})

	t.Run("call count is unavailable without a tracing client", func(t *testing.T) {
		// Documents a real limit rather than papering over it. Tracing lives in
		// llm.Client; a bare agent.Completer emits no trace events, so nothing
		// records individual calls and Config.LLMTrace has nothing to attach
		// to. Usage still arrives via turn and node events, which is why the
		// totals above are right while the call count is absent. Deriving one
		// from turn count would be a guess: a turn can issue more than one call
		// (repair, compaction). TestCaptureChainWithTracingClient covers the
		// counted case.
		if m.Totals.LLMCalls != 0 {
			t.Errorf("llm_calls = %d, want 0 — a bare Completer logs no call events, so any number here is inferred",
				m.Totals.LLMCalls)
		}
		if stub.calls == 0 {
			t.Fatal("stub was never called; the rest of this test proves nothing")
		}
	})

	t.Run("per-node usage sums to the run total", func(t *testing.T) {
		// Attribution is only trustworthy if the parts add up to the whole.
		var in, out int
		attributed := 0
		for _, n := range m.Nodes {
			if n.Usage == nil {
				continue
			}
			attributed++
			in += n.Usage.InputTokens
			out += n.Usage.OutputTokens
		}
		if attributed == 0 {
			t.Fatal("no node carries usage, so nothing is attributable")
		}
		if in != m.Totals.InputTokens || out != m.Totals.OutputTokens {
			t.Errorf("node usage sums to %d/%d, run totals say %d/%d",
				in, out, m.Totals.InputTokens, m.Totals.OutputTokens)
		}
	})

	t.Run("each node is charged what the engine charged it", func(t *testing.T) {
		// Summing to the right total is not enough: the shares could be right
		// in aggregate and attributed to the wrong nodes.
		for _, n := range m.Nodes {
			want, ok := engineByNode[n.ID]
			if !ok || want.InputTokens == 0 {
				continue
			}
			if n.Usage == nil {
				t.Errorf("node %s consumed %d input tokens per the engine but carries no usage",
					n.ID, want.InputTokens)
				continue
			}
			if n.Usage.InputTokens != want.InputTokens || n.Usage.OutputTokens != want.OutputTokens {
				t.Errorf("node %s charged %d/%d, engine charged it %d/%d",
					n.ID, n.Usage.InputTokens, n.Usage.OutputTokens,
					want.InputTokens, want.OutputTokens)
			}
		}
	})
}

// tracingStubAdapter is a ProviderAdapter whose stream carries everything the
// capture chain reads: the verbatim wire body, a tool call, and metered usage on
// finish. Going through llm.Client rather than a bare Completer is what makes
// the trace path real — TraceBuilder, call ids, and request_raw only exist
// there, which is exactly the stretch a stub Completer silently skips.
type tracingStubAdapter struct {
	mu    sync.Mutex
	calls int
}

func (a *tracingStubAdapter) Name() string { return "anthropic" }
func (a *tracingStubAdapter) Close() error { return nil }

func (a *tracingStubAdapter) Complete(context.Context, *llm.Request) (*llm.Response, error) {
	return &llm.Response{
		Message:      llm.AssistantMessage("done"),
		FinishReason: llm.FinishReason{Reason: "stop"},
	}, nil
}

// wireBody is the fake request body the adapter reports sending. It is
// recognizable on purpose: the test asserts this exact text reaches disk, so a
// truncated or synthesized copy fails rather than passing on shape alone.
const wireBody = `{"model":"stub-model","messages":[{"role":"user","content":"capture-chain-probe"}]}`

func (a *tracingStubAdapter) Stream(_ context.Context, req *llm.Request) <-chan llm.StreamEvent {
	a.mu.Lock()
	a.calls++
	n := a.calls
	a.mu.Unlock()

	ch := make(chan llm.StreamEvent, 8)
	go func() {
		defer close(ch)
		// Same call the real adapters make, including the tracing gate.
		llm.EmitRequestSent(ch, []byte(wireBody), llm.RequestIsTraced(req))
		ch <- llm.StreamEvent{Type: llm.EventStreamStart}
		if n == 1 {
			// An unregistered tool fails fast, which still drives the session
			// into a second turn — the cheapest way to get more than one call.
			ch <- llm.StreamEvent{Type: llm.EventToolCallEnd, ToolCall: &llm.ToolCallData{
				ID:        "call_1",
				Name:      "definitely_not_a_registered_tool",
				Arguments: json.RawMessage(`{"probe":"yes"}`),
			}}
			ch <- llm.StreamEvent{
				Type:         llm.EventFinish,
				FinishReason: &llm.FinishReason{Reason: "tool_calls"},
				Usage:        &llm.Usage{InputTokens: 100, OutputTokens: 20, TotalTokens: 120},
			}
			return
		}
		ch <- llm.StreamEvent{Type: llm.EventTextDelta, Delta: "done"}
		ch <- llm.StreamEvent{
			Type:         llm.EventFinish,
			FinishReason: &llm.FinishReason{Reason: "stop"},
			Usage:        &llm.Usage{InputTokens: 130, OutputTokens: 5, TotalTokens: 135},
		}
	}()
	return ch
}

// TestCaptureChainWithTracingClient exercises the capture chain end to end
// through a real llm.Client, which is the configuration production runs in.
//
// This is the coverage the earlier stub e2e only appeared to have: it declared
// call_id and request_raw as fields and asserted neither, so both were absent
// from live runs while the test stayed green. Here the adapter counts its own
// invocations and names the exact bytes it sent, giving two oracles the
// manifest cannot fake.
func TestCaptureChainWithTracingClient(t *testing.T) {
	adapter := &tracingStubAdapter{}
	client, err := llm.NewClient(
		llm.WithProvider(adapter),
		llm.WithDefaultProvider("anthropic"),
	)
	if err != nil {
		t.Fatalf("NewClient: %v", err)
	}
	defer client.Close()

	workDir := t.TempDir()
	handler := pipeline.NewJSONLEventHandler(filepath.Join(workDir, ".tracker", "runs"))

	result, err := Run(context.Background(), captureDip, Config{
		Format:       "dip",
		WorkingDir:   workDir,
		LLMClient:    client,
		EventHandler: handler,
		AgentEvents:  agent.EventHandlerFunc(handler.WriteAgentEvent),
		LLMTrace:     handler.LLMTraceObserver(),
		Model:        "stub-model",
		Provider:     "anthropic",
	})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if err := handler.Close(); err != nil {
		t.Fatalf("close log: %v", err)
	}

	entries := readRunLog(t, workDir)
	runDir := filepath.Join(workDir, ".tracker", "runs", result.RunID)
	m, err := pipeline.AssembleRunManifest(runDir, result.RunID)
	if err != nil {
		t.Fatalf("assemble manifest: %v", err)
	}

	t.Run("every call is counted exactly once", func(t *testing.T) {
		// The adapter is ground truth. Counting the same call on both log paths
		// would read high; trusting only the agent path would read low.
		adapter.mu.Lock()
		want := adapter.calls
		adapter.mu.Unlock()
		if m.Totals.LLMCalls != want {
			t.Errorf("manifest llm_calls = %d, adapter streamed %d call(s)", m.Totals.LLMCalls, want)
		}
	})

	t.Run("usage is not multiplied by the number of log paths", func(t *testing.T) {
		var engine int
		for _, e := range result.Trace.Entries {
			if e.Stats != nil {
				engine += e.Stats.InputTokens
			}
		}
		if engine == 0 {
			t.Fatal("engine recorded no usage")
		}
		if m.Totals.InputTokens != engine {
			t.Errorf("manifest input_tokens = %d, engine counted %d", m.Totals.InputTokens, engine)
		}
	})

	t.Run("the verbatim wire body reaches disk", func(t *testing.T) {
		// The whole reconstruction claim rests on this: what the provider was
		// actually sent, not a summary of it.
		var found bool
		for _, e := range entries {
			if strings.Contains(e.Content, "capture-chain-probe") && e.Content == wireBody {
				found = true
			}
		}
		if !found {
			t.Error("no log entry carries the exact wire body the adapter sent")
		}
	})

	t.Run("calls carry an id that groups their events", func(t *testing.T) {
		ids := map[string]int{}
		for _, e := range entries {
			if e.CallID != "" {
				ids[e.CallID]++
			}
		}
		if len(ids) == 0 {
			t.Fatal("no log entry carries a call_id")
		}
		// A call id that appears on only one line groups nothing.
		for id, n := range ids {
			if n < 2 {
				t.Errorf("call_id %s appears on %d line; it should group a call's events", id, n)
			}
		}
	})
}
