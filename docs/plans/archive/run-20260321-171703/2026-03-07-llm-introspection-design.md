# LLM Introspection Design

**Date:** March 7, 2026

## Goal

Increase observability for LLM calls in both the default console experience and the full TUI dashboard.

The new baseline should show:

- richer tool-call information, including what the model is preparing, calling, and receiving back
- more model-state information, including progress markers and short streaming previews
- provider metadata in normal mode when it is available
- raw provider stream events only when `--verbose` is enabled

## Scope

This change covers the two user-facing runtime surfaces in `tracker`:

- default console mode in [`cmd/tracker/main.go`](/Users/harper/Public/src/2389/mammoth-lite/cmd/tracker/main.go)
- full dashboard TUI in [`tui/dashboard/`](/Users/harper/Public/src/2389/mammoth-lite/tui/dashboard)

It also covers the observability path that feeds those surfaces:

- unified LLM client and stream event model in [`llm/`](/Users/harper/Public/src/2389/mammoth-lite/llm)
- agent session event emission in [`agent/`](/Users/harper/Public/src/2389/mammoth-lite/agent)

It does not change model behavior, tool behavior, or pipeline semantics.

## Problem

The repo already has pieces of LLM observability, but they stop at two shallow layers:

- non-TUI mode prints only coarse pipeline lifecycle events through [`pipeline/events_logger.go`](/Users/harper/Public/src/2389/mammoth-lite/pipeline/events_logger.go)
- TUI mode shows one-line post-hoc LLM summaries through [`llm/activity_tracker.go`](/Users/harper/Public/src/2389/mammoth-lite/llm/activity_tracker.go)

That leaves three gaps:

1. tool calls are visible only after the fact and without much context
2. model progress is mostly invisible while a call is running
3. provider stream details exist in adapters but are not exposed through a shared runtime trace

## Chosen Direction

Add one structured LLM trace pipeline that sits between provider stream events and the two UI renderers.

The design principle is:

- normalize first
- enrich with provider metadata where available
- expose raw provider events only behind `--verbose`

Both console and TUI should render from the same trace event vocabulary so they stay consistent.

## User-Facing Modes

### Default mode

Default output should include normalized model progress plus selected provider metadata:

- request start with provider/model, turn, tool count, message count
- model state transitions such as thinking, generating text, preparing tool call, waiting on tool result, resumed, finished
- short previews of reasoning/text/tool arguments and tool outputs
- tool execution lifecycle with elapsed time and error marker
- finish line with latency, finish reason, and token usage

### Verbose mode

`--verbose` should preserve all default output and add raw provider stream visibility:

- raw provider event names
- compact raw payload previews
- provider-specific details that do not fit the normalized schema

Verbose mode is for debugging adapters and parity issues, not the everyday default.

## Architecture

### 1. Structured trace events in `llm`

Introduce a first-class trace event type in [`llm/`](/Users/harper/Public/src/2389/mammoth-lite/llm) that represents the runtime of one LLM request.

The trace schema should include:

- request identity: provider, model, session or call id when available
- lifecycle phase: request start, text started, reasoning started, tool-call started, tool-call delta, tool-call finished, finish, error
- normalized preview fields: text snippet, reasoning snippet, tool name, argument preview, result preview
- provider metadata: raw event name, provider finish reason, other adapter-specific labels
- usage and latency fields
- optional raw payload preview for verbose rendering

This trace layer should be produced from streaming APIs, even when the caller still wants a final blocking `Complete()` result. That gives the UI real-time visibility while keeping the current `Complete()` contract intact.

### 2. Trace observer plumbing in `llm.Client`

[`llm/client.go`](/Users/harper/Public/src/2389/mammoth-lite/llm/client.go) currently applies middleware only to `Complete()` and does not expose streaming events to the rest of the system.

The client should gain an observer or listener path that:

- can receive normalized trace events during a completion
- can be attached once when building the client
- can be used by both console and TUI without separate code paths

The cleanest shape is a trace observer interface plus a fan-out implementation, rather than overloading existing middleware summaries.

### 3. Agent-level bridge

[`agent/session.go`](/Users/harper/Public/src/2389/mammoth-lite/agent/session.go) already emits tool start/end and final text events. It does not emit model-progress events because it only sees final responses.

The agent should bridge LLM trace events into agent-level observability so downstream consumers can correlate:

- turn start
- model request start
- reasoning/text progress
- tool call preparation
- tool execution start/end
- resumed model generation after tool results
- turn end

This bridge should preserve current session behavior while adding richer event payloads.

### 4. Surface-specific renderers

Two renderers should consume the same trace stream:

- console renderer for default CLI mode
- TUI renderer for the dashboard activity log

They should differ only in formatting density, not in what data is available.

## Data Flow

The intended data flow is:

1. `tracker` builds the LLM client with token tracking plus a new trace observer.
2. A completion request starts.
3. The provider adapter emits unified stream events.
4. The trace observer converts those events into structured trace entries, adding provider metadata and previews.
5. The agent session receives those entries and correlates them with the active turn and tool execution.
6. The console logger or TUI app renders those entries live.
7. When the response completes, the final response still flows back through the existing `Complete()` path.

This keeps one source of truth for observability and avoids reimplementing provider parsing in each UI.

## Console Design

The console should move beyond stage-only logging.

Normal mode should print concise lines such as:

- `llm start openai/gpt-5.2 turn=2 tools=4 messages=9`
- `llm thinking preview="checking workspace layout..."`
- `llm tool prepare name=read args="path=go.mod"`
- `tool start name=read`
- `tool done name=read 12ms preview="module github.com/..."`
- `llm resumed after tool=read`
- `llm text preview="I found the handler registry..."`
- `llm finish reason=tool_calls 842ms tokens=1321/211`

Verbose mode should additionally print lines like:

- `provider event=openai.response.output_item.added preview="{...}"`
- `provider event=anthropic.content_block_delta preview="{...}"`

The default logger should stay readable in long runs, so previews need truncation and multiline payloads must be collapsed.

## TUI Design

The TUI activity log in [`tui/dashboard/agentlog.go`](/Users/harper/Public/src/2389/mammoth-lite/tui/dashboard/agentlog.go) should render the same events with denser visual cues.

Normal mode should show:

- model start lines with provider/model labels
- state transitions for thinking, text generation, and tool preparation
- tool-call lines with short input/output previews
- finish lines with latency and tokens

Verbose mode should append raw provider event lines into the same log with a visually dimmer style so they are available without dominating the display.

The dashboard does not need a second panel for this work. The existing activity log can absorb richer line types as long as truncation and styling remain controlled.

## Error Handling

Observability must never break pipeline execution.

Rules:

- trace observer failures are swallowed after best-effort formatting
- malformed provider payloads still emit a normalized error trace line
- raw payload previews are size-limited before rendering
- missing provider metadata falls back to normalized fields only

If a provider emits partial stream data before failing, the surfaces should show the partial trace followed by the error.

## Testing Strategy

Verification should cover three layers.

### LLM trace tests

- unit tests for normalized trace conversion from `llm.StreamEvent`
- unit tests for snippet truncation and payload collapsing
- tests that verbose mode includes raw provider metadata and default mode does not

### Agent bridge tests

- session tests proving model-progress events are emitted in turn order
- tests covering tool-call preparation, execution, and resume transitions
- tests covering provider error propagation with partial trace output

### Surface tests

- console logger tests for normal and verbose formatting
- TUI activity log tests for new line types and truncation
- CLI tests for `--verbose` flag wiring in both non-TUI and `--tui` flows

## Tradeoffs

This design adds a new observability path instead of stretching the existing `ActivityTracker`.

That is a larger change, but it avoids three problems:

- no live progress if we stay post-hoc only
- duplicated formatting logic between console and TUI
- provider-specific hacks leaking into user-facing code

The cost is added plumbing in `llm.Client` and `agent.Session`, but that cost is justified because observability becomes a reusable subsystem rather than a UI-specific patch.

## Rollout Shape

The implementation should happen in slices:

1. add trace event types and formatter helpers
2. wire the client and agent to emit live trace events
3. upgrade console rendering
4. upgrade TUI rendering
5. add `--verbose` raw provider event output
6. tighten tests across all three layers

## Output

The next artifact is a detailed implementation plan with exact files, tests, and command-level verification steps.
