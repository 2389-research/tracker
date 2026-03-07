# LLM Introspection Implementation Plan

> **For Claude:** REQUIRED SUB-SKILL: Use superpowers:executing-plans to implement this plan task-by-task.

**Goal:** Add live, richer LLM introspection to both the console and TUI, with normalized progress/tool details by default and raw provider stream events only when `--verbose` is set.

**Architecture:** Introduce a structured LLM trace event path in `llm`, wire `llm.Client` and `agent.Session` to emit those events during completions, and render the same trace vocabulary in both the console and TUI. Keep the default output provider-agnostic with light provider metadata, and gate raw provider event rendering behind a CLI verbosity flag.

**Tech Stack:** Go, Bubble Tea, Lip Gloss, existing `llm.StreamEvent` adapters, existing agent event system, CLI flags in `cmd/tracker`.

---

### Task 1: Add Structured LLM Trace Types And Formatting Helpers

**Files:**
- Create: `llm/trace.go`
- Create: `llm/trace_test.go`
- Create: `llm/trace_format_test.go`
- Modify: `llm/stream.go`
- Test: `llm/trace_test.go`
- Test: `llm/trace_format_test.go`

**Step 1: Write the failing tests**

```go
func TestTraceBuilderEmitsNormalizedEvents(t *testing.T) {
	builder := NewTraceBuilder(TraceOptions{Verbose: false})

	builder.Process(StreamEvent{Type: EventStreamStart})
	builder.Process(StreamEvent{Type: EventReasoningStart})
	builder.Process(StreamEvent{Type: EventReasoningDelta, ReasoningDelta: "checking files"})
	builder.Process(StreamEvent{
		Type: EventToolCallStart,
		ToolCall: &ToolCallData{Name: "read", Arguments: json.RawMessage(`{"path":"go.mod"}`)},
	})
	builder.Process(StreamEvent{Type: EventToolCallEnd})
	builder.Process(StreamEvent{
		Type: EventFinish,
		FinishReason: &FinishReason{Reason: "tool_calls", Raw: "tool_use"},
		Usage:        &Usage{InputTokens: 12, OutputTokens: 3},
	})

	events := builder.Events()

	requireKinds(t, events, TraceRequestStart, TraceReasoning, TraceToolPrepare, TraceFinish)
	if containsTraceKind(events, TraceProviderRaw) {
		t.Fatal("did not expect raw provider events in non-verbose mode")
	}
}

func TestFormatTraceLineVerboseIncludesProviderEvent(t *testing.T) {
	line := FormatTraceLine(TraceEvent{
		Kind:          TraceProviderRaw,
		Provider:      "openai",
		ProviderEvent: "response.output_item.added",
		RawPreview:    `{"type":"function_call"}`,
	}, true)

	if !strings.Contains(line, "response.output_item.added") {
		t.Fatalf("expected provider event in line: %q", line)
	}
}
```

**Step 2: Run test to verify it fails**

Run: `go test ./llm -run 'TestTraceBuilderEmitsNormalizedEvents|TestFormatTraceLineVerboseIncludesProviderEvent'`

Expected: FAIL with undefined `NewTraceBuilder`, `TraceEvent`, or `FormatTraceLine`.

**Step 3: Write minimal implementation**

```go
type TraceKind string

const (
	TraceRequestStart TraceKind = "request_start"
	TraceReasoning    TraceKind = "reasoning"
	TraceText         TraceKind = "text"
	TraceToolPrepare  TraceKind = "tool_prepare"
	TraceToolDone     TraceKind = "tool_done"
	TraceFinish       TraceKind = "finish"
	TraceProviderRaw  TraceKind = "provider_raw"
)

type TraceEvent struct {
	Kind          TraceKind
	Provider      string
	Model         string
	ToolName      string
	Preview       string
	ProviderEvent string
	RawPreview    string
	FinishReason  string
	Usage         Usage
}
```

Add a small builder that converts `StreamEvent` into `TraceEvent` slices, truncates previews, and only emits raw provider events when `TraceOptions.Verbose` is true.

**Step 4: Run test to verify it passes**

Run: `go test ./llm -run 'TestTraceBuilderEmitsNormalizedEvents|TestFormatTraceLineVerboseIncludesProviderEvent'`

Expected: PASS

**Step 5: Commit**

```bash
git add llm/trace.go llm/trace_test.go llm/trace_format_test.go llm/stream.go
git commit -m "feat: add structured llm trace events"
```

### Task 2: Wire `llm.Client` To Emit Live Trace Events During `Complete`

**Files:**
- Modify: `llm/client.go`
- Modify: `llm/client_test.go`
- Modify: `llm/provider_test.go`
- Test: `llm/client_test.go`

**Step 1: Write the failing tests**

```go
func TestClientCompletePublishesTraceEvents(t *testing.T) {
	provider := &mockAdapter{
		name: "streamer",
		events: []StreamEvent{
			{Type: EventStreamStart},
			{Type: EventTextStart, TextID: "t1"},
			{Type: EventTextDelta, TextID: "t1", Delta: "hello"},
			{Type: EventFinish, FinishReason: &FinishReason{Reason: "stop"}},
		},
	}

	var traces []TraceEvent
	client, _ := NewClient(
		WithProvider(provider),
		WithDefaultProvider("streamer"),
		WithTraceObserver(TraceObserverFunc(func(evt TraceEvent) { traces = append(traces, evt) })),
	)

	resp, err := client.Complete(context.Background(), &Request{Model: "m", Messages: []Message{UserMessage("hi")}})
	if err != nil {
		t.Fatal(err)
	}
	if resp.Text() != "hello" {
		t.Fatalf("resp.Text() = %q", resp.Text())
	}
	if len(traces) == 0 {
		t.Fatal("expected trace events")
	}
}
```

**Step 2: Run test to verify it fails**

Run: `go test ./llm -run TestClientCompletePublishesTraceEvents`

Expected: FAIL because `WithTraceObserver` does not exist or `Complete()` still bypasses streaming traces.

**Step 3: Write minimal implementation**

```go
type TraceObserver interface {
	HandleTraceEvent(TraceEvent)
}

func WithTraceObserver(obs TraceObserver) ClientOption {
	return func(c *clientConfig) {
		c.traceObservers = append(c.traceObservers, obs)
	}
}
```

Update `Client.Complete()` to:

- resolve the provider
- call `adapter.Stream(...)`
- feed events through `StreamAccumulator`
- build `TraceEvent`s with the new builder
- notify observers during the stream
- return the accumulated `Response`

Keep `resp.Provider`, `resp.Model`, `resp.Latency`, and `FinishReason` populated so existing callers still work.

**Step 4: Run test to verify it passes**

Run: `go test ./llm -run 'TestClientCompletePublishesTraceEvents|TestClientRouting|TestClientStream'`

Expected: PASS

**Step 5: Commit**

```bash
git add llm/client.go llm/client_test.go llm/provider_test.go
git commit -m "feat: emit llm trace events during complete"
```

### Task 3: Bridge LLM Trace Events Into Agent Session Events

**Files:**
- Modify: `agent/events.go`
- Modify: `agent/session.go`
- Modify: `agent/session_test.go`
- Test: `agent/session_test.go`

**Step 1: Write the failing tests**

```go
func TestSessionEmitsLLMTraceEventsInTurnOrder(t *testing.T) {
	client := &mockCompleter{
		responses: []*llm.Response{
			{
				Message: llm.Message{
					Role: llm.RoleAssistant,
					Content: []llm.ContentPart{
						{Kind: llm.KindToolCall, ToolCall: &llm.ToolCallData{
							ID: "call_1", Name: "read", Arguments: json.RawMessage(`{"path":"go.mod"}`),
						}},
					},
				},
				FinishReason: llm.FinishReason{Reason: "tool_calls"},
			},
		},
	}

	var got []EventType
	handler := EventHandlerFunc(func(evt Event) { got = append(got, evt.Type) })
	sess := mustNewSession(t, client, DefaultConfig(), WithEventHandler(handler), WithTools(&stubTool{name: "read", output: "ok"}))

	_, _ = sess.Run(context.Background(), "inspect")

	assertContainsInOrder(t, got,
		EventTurnStart,
		EventLLMRequestStart,
		EventLLMReasoning,
		EventLLMToolPrepare,
		EventToolCallStart,
		EventToolCallEnd,
		EventTurnEnd,
	)
}
```

**Step 2: Run test to verify it fails**

Run: `go test ./agent -run TestSessionEmitsLLMTraceEventsInTurnOrder`

Expected: FAIL with undefined event types or missing event emission.

**Step 3: Write minimal implementation**

```go
const (
	EventLLMRequestStart EventType = "llm_request_start"
	EventLLMReasoning    EventType = "llm_reasoning"
	EventLLMText         EventType = "llm_text"
	EventLLMToolPrepare  EventType = "llm_tool_prepare"
	EventLLMFinish       EventType = "llm_finish"
	EventLLMProviderRaw  EventType = "llm_provider_raw"
)
```

Extend `agent.Event` with fields needed by the UI:

- `Provider`
- `Model`
- `Preview`
- `ProviderEvent`
- `Latency`
- `Usage`

Then, when the session receives trace callbacks from the client, translate `llm.TraceEvent` into `agent.Event` and emit them through the existing handler.

**Step 4: Run test to verify it passes**

Run: `go test ./agent -run 'TestSessionEmitsLLMTraceEventsInTurnOrder|TestSessionEventEmission|TestSessionToolCallLoop'`

Expected: PASS

**Step 5: Commit**

```bash
git add agent/events.go agent/session.go agent/session_test.go
git commit -m "feat: expose llm trace events from agent sessions"
```

### Task 4: Upgrade The Console Renderer And Add `--verbose` Wiring

**Files:**
- Create: `llm/trace_logger.go`
- Create: `llm/trace_logger_test.go`
- Create: `cmd/tracker/main_test.go`
- Modify: `cmd/tracker/main.go`
- Test: `llm/trace_logger_test.go`
- Test: `cmd/tracker/main_test.go`

**Step 1: Write the failing tests**

```go
func TestTraceLoggerDefaultOmitsRawProviderEvents(t *testing.T) {
	var buf bytes.Buffer
	logger := NewTraceLogger(&buf, TraceLoggerOptions{Verbose: false})

	logger.HandleTraceEvent(TraceEvent{Kind: TraceProviderRaw, ProviderEvent: "message_delta", RawPreview: `{"x":1}`})

	if buf.Len() != 0 {
		t.Fatalf("expected raw provider events to be hidden by default, got %q", buf.String())
	}
}

func TestParseFlagsEnablesVerbose(t *testing.T) {
	cfg, err := parseFlags([]string{"tracker", "--verbose", "pipe.dot"})
	if err != nil {
		t.Fatal(err)
	}
	if !cfg.verbose {
		t.Fatal("expected verbose to be true")
	}
}
```

**Step 2: Run test to verify it fails**

Run: `go test ./llm ./cmd/tracker -run 'TestTraceLoggerDefaultOmitsRawProviderEvents|TestParseFlagsEnablesVerbose'`

Expected: FAIL because no trace logger or reusable flag parsing exists yet.

**Step 3: Write minimal implementation**

```go
type TraceLoggerOptions struct {
	Verbose bool
}

type TraceLogger struct {
	w       io.Writer
	verbose bool
}
```

In `cmd/tracker/main.go`:

- add a `--verbose` bool flag
- thread that flag through `run()` and `runTUI()`
- attach a trace logger observer in non-TUI mode

Refactor flag parsing into a small helper so it can be unit-tested without invoking `main()`.

**Step 4: Run test to verify it passes**

Run: `go test ./llm ./cmd/tracker -run 'TestTraceLoggerDefaultOmitsRawProviderEvents|TestParseFlagsEnablesVerbose'`

Expected: PASS

**Step 5: Commit**

```bash
git add llm/trace_logger.go llm/trace_logger_test.go cmd/tracker/main.go cmd/tracker/main_test.go
git commit -m "feat: add verbose console llm trace logging"
```

### Task 5: Upgrade The TUI Activity Log To Render Structured Trace Events

**Files:**
- Modify: `tui/dashboard/app.go`
- Modify: `tui/dashboard/app_test.go`
- Modify: `tui/dashboard/agentlog.go`
- Modify: `tui/dashboard/agentlog_test.go`
- Test: `tui/dashboard/app_test.go`
- Test: `tui/dashboard/agentlog_test.go`

**Step 1: Write the failing tests**

```go
func TestAppModelHandlesLLMTraceMsg(t *testing.T) {
	app := NewAppModel("pipe", nil)
	app.width = 120
	app.height = 40
	app.relayout()

	m2, _ := app.Update(LLMTraceMsg{Event: llm.TraceEvent{
		Kind:     llm.TraceToolPrepare,
		Provider: "anthropic",
		Model:    "claude-opus-4-6",
		ToolName: "read",
		Preview:  `{"path":"go.mod"}`,
	}})

	updated := m2.(AppModel)
	if updated.agentLog.Len() != 1 {
		t.Fatalf("expected 1 log entry, got %d", updated.agentLog.Len())
	}
	if !strings.Contains(updated.agentLog.entries[0].Message, "read") {
		t.Fatalf("expected tool name in message: %+v", updated.agentLog.entries[0])
	}
}
```

**Step 2: Run test to verify it fails**

Run: `go test ./tui/dashboard -run TestAppModelHandlesLLMTraceMsg`

Expected: FAIL because `LLMTraceMsg` does not exist and the agent log only accepts raw strings or pipeline events.

**Step 3: Write minimal implementation**

```go
type LLMTraceMsg struct {
	Event llm.TraceEvent
}
```

Update the dashboard so:

- `AppModel.Update()` handles `LLMTraceMsg`
- `AgentLogModel` has `AppendTrace(llm.TraceEvent, verbose bool)`
- verbose-only raw provider lines use dim styling
- default lines show provider/model, state, preview, tokens, and latency

Keep truncation logic centralized in the log formatter so long previews do not wrap the panel.

**Step 4: Run test to verify it passes**

Run: `go test ./tui/dashboard -run 'TestAppModelHandlesLLMTraceMsg|TestAgentLogAppendLine|TestAgentLogFormatIncludesMessage'`

Expected: PASS

**Step 5: Commit**

```bash
git add tui/dashboard/app.go tui/dashboard/app_test.go tui/dashboard/agentlog.go tui/dashboard/agentlog_test.go
git commit -m "feat: render llm trace events in tui activity log"
```

### Task 6: Final Integration And Verification

**Files:**
- Modify: `cmd/tracker/main.go`
- Modify: `llm/activity_tracker.go`
- Modify: `llm/activity_tracker_test.go`
- Test: `llm/activity_tracker_test.go`
- Test: `llm/client_test.go`
- Test: `agent/session_test.go`
- Test: `tui/dashboard/app_test.go`
- Test: `tui/dashboard/agentlog_test.go`

**Step 1: Write the failing tests**

```go
func TestActivityTrackerSummaryBuiltFromTraceEvent(t *testing.T) {
	evt := ActivityEvent{
		Model:           "gpt-5.2",
		Provider:        "openai",
		State:           "tool_prepare",
		ToolCalls:       []string{"read"},
		ResponseSnippet: `{"path":"go.mod"}`,
	}
	if got := evt.Summary(); !strings.Contains(got, "tool_prepare") {
		t.Fatalf("expected state in summary: %q", got)
	}
}
```

**Step 2: Run test to verify it fails**

Run: `go test ./llm -run TestActivityTrackerSummaryBuiltFromTraceEvent`

Expected: FAIL because the legacy summary type does not carry the richer state model yet.

**Step 3: Write minimal implementation**

Either:

- retire `ActivityTracker` and route TUI updates directly from trace observers, or
- adapt `ActivityTracker` to be a thin wrapper around the new trace stream

Prefer the first option if it keeps duplication down. Then run full-package cleanup so there is only one live LLM observability path.

**Step 4: Run test to verify it passes**

Run: `go test ./llm ./agent ./tui/dashboard ./cmd/tracker`

Expected: PASS

Then run broader verification:

Run: `go test ./...`

Expected: PASS

**Step 5: Commit**

```bash
git add llm/activity_tracker.go llm/activity_tracker_test.go cmd/tracker/main.go
git commit -m "refactor: unify llm introspection across console and tui"
```
