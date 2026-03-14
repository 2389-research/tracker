# TUI Overhaul + Thinking Indicators Design

## Goal

Clean-room rewrite of the tracker TUI with the same visual design but robust internals, plus LLM thinking/progress indicators.

## Context

The current TUI works but has structural problems: `app.go` mixes layout, routing, and state management (492 lines); `agentlog.go` mixes event handling, formatting, and rendering (416 lines); duplicated styling and scroll logic across components; fragile modal overlay via string manipulation; stringly-typed event checking; and no indication of LLM thinking state. Total: ~4,590 lines across 12 files in `tui/`, `tui/dashboard/`, `tui/components/`, `tui/render/`.

This is a full rewrite — all existing TUI files are replaced. The external interface (`runTUI()` signature in `cmd/tracker/main.go`) stays the same. The visual output stays the same (signal lamps, header, node list, agent log, modal).

## Architecture

### Component Tree

```
App (routing + layout only — no state logic)
├── StyleRegistry (shared colors, borders, signal lamps)
├── StateStore (single source of truth for pipeline/agent state)
├── Header (pipeline name, run ID, elapsed time, token/cost readout)
├── StatusBar (track diagram, progress summary, keybinding hints)
├── NodeList (scrollable node list with signal lamps + thinking spinner)
├── AgentLog (scrollable streaming log with thinking indicator line)
├── Modal (proper overlay using lipgloss Place)
│   ├── ChoiceModal (multiple-choice gate prompts)
│   └── FreeformModal (free-text gate prompts)
├── Interviewer (bridges gate requests/replies via channels)
└── ThinkingTracker (tracks LLM thinking state + elapsed time, feeds NodeList + AgentLog)
```

### Key Rules

- `App` does layout and message routing only — target ~200 lines (gate/modal routing adds complexity).
- Each component owns its own `Update`/`View` — no cross-component reaching.
- All state flows through `StateStore` via Bubbletea messages (not direct mutation). `App.Update` calls `StateStore.Apply(msg)` to update state, then forwards the message to child components.
- `ThinkingTracker` is a standalone component that emits thinking state changes as messages.
- Shared scroll behavior lives in a `ScrollView` helper used by both NodeList and AgentLog.
- All event types are typed constants, no string comparisons.
- Each tick consumer (ThinkingTracker at 150ms, Header at 1s) uses its own typed tick message — no shared `MsgTick`.

## Component Breakdown

### StyleRegistry

Single file defining all colors, borders, lamp characters, and lipgloss styles. Every component imports from here instead of defining its own.

### StateStore

Holds pipeline state (node statuses, current stage, errors) and agent state (streaming text, tool calls, thinking status). Components read from it via getter methods, never write directly. `App.Update` calls `store.Apply(msg)` on each incoming message to update state before forwarding to child components. Components receive a `*StateStore` pointer at construction time for reads. The store also holds the `*llm.TokenTracker` reference for the header's cost/token display.

### ScrollView

Reusable scroll container with viewport, auto-scroll-to-bottom, and manual scroll override. Used by both NodeList and AgentLog — no more duplicated scroll logic.

### ThinkingTracker

Listens for LLM request/response events. When a request starts and no output has arrived yet, it enters "thinking" state with a timer. Emits `ThinkingStarted` and `ThinkingStopped` messages. NodeList uses this to animate the lamp on the active node. AgentLog uses it to show the "Thinking..." line with elapsed time.

### Modal

Proper overlay that composes on top of the base view using lipgloss's `Place` — no string slicing. Receives its content as a Bubbletea model, so it can host error details, help text, or anything else. Two built-in content types:

- **ChoiceModal**: Renders multiple-choice gate prompts with arrow-key selection. Sends selected option back via the interviewer's reply channel.
- **FreeformModal**: Renders free-text gate prompts with a text input field. Sends typed response back via the interviewer's reply channel.

### Interviewer

Bridges pipeline gate requests into the TUI. When the pipeline engine hits a gate node, it sends a `GateChoiceMsg` or `GateFreeformMsg` through the Bubbletea program. `App.Update` opens the appropriate modal. When the user submits, the modal sends the response back on the gate's reply channel (`chan<- string`). This file also contains the mode-1/mode-2 inline runner logic for non-modal gate handling.

### StatusBar

Renders the track diagram (compact node status glyphs), progress summary ("3/7 nodes complete"), and keybinding hints ("ctrl+o expand/collapse"). Reads from `StateStore` for node statuses.

### Header / NodeList / AgentLog

Same visual output as today, but each is a self-contained Bubbletea model with clean `Init`/`Update`/`View`. No reaching into other components' state.

- **Header** takes a `*llm.TokenTracker` for live token count and cost display alongside pipeline name, run ID, and elapsed time.
- **NodeList** includes the signal lamp rendering and track diagram.
- **AgentLog** preserves the existing coalescing logic for streaming text/reasoning chunks, expand/collapse toggle (`ctrl+o`), and verbose trace mode (`SetVerboseTrace`). The coalescing state machine accumulates `MsgTextChunk` messages into contiguous blocks, with model-header deduplication.

## Thinking Indicator Behavior

### Node Lamp Animation

When the LLM is thinking, the active node's signal lamp cycles through a 4-frame animation: `◐ ◓ ◑ ◒` at 150ms intervals, using the "running" color. Once output arrives, it snaps back to the solid `◉` running lamp. Driven by a Bubbletea `tick` command inside ThinkingTracker.

### Agent Log Indicator

A line appears at the bottom of the agent log:

```
⟳ Thinking... (3.2s)
```

The elapsed time updates every 100ms. When output starts streaming, the line is replaced by the actual content. If the LLM does multiple thinking phases (between tool calls), each one gets its own indicator.

### State Transitions

```
Idle → LLM request starts → Thinking (timer starts)
Thinking → First text/tool output → Streaming (timer stops, indicator removed)
Streaming → Response complete → Idle
Streaming → New LLM request → Thinking (timer restarts)
```

### Edge Cases

- Very fast responses (<200ms): thinking indicator briefly flashes, acceptable.
- Errors during thinking: indicator stops, error displays normally.
- Multiple nodes running concurrently: each gets its own thinking state tracked by node ID. Node lamps animate independently. The agent log shows the thinking indicator for the currently-focused node only (the one whose log is being displayed).

## Message Flow & Event Types

### Typed Messages

```go
// Pipeline messages
MsgNodeStarted{NodeID}
MsgNodeCompleted{NodeID, Outcome}
MsgNodeFailed{NodeID, Error}
MsgPipelineCompleted{}
MsgPipelineFailed{Error}

// Agent messages
MsgThinkingStarted{NodeID}
MsgThinkingStopped{NodeID}
MsgTextChunk{NodeID, Text}
MsgReasoningChunk{NodeID, Text}
MsgToolCallStart{NodeID, ToolName}
MsgToolCallEnd{NodeID, ToolName, Output, Error}
MsgAgentError{NodeID, Error}

// LLM trace messages (mapped from llm.TraceEvent kinds)
MsgLLMRequestStart{NodeID, Provider, Model}
MsgLLMFinish{NodeID}
MsgLLMProviderRaw{NodeID, Data}  // only shown in verbose mode

// Gate messages
MsgGateChoice{NodeID, Prompt, Options, ReplyCh chan<- string}
MsgGateFreeform{NodeID, Prompt, ReplyCh chan<- string}

// UI messages
MsgThinkingTick{}              // 150ms — drives lamp animation + thinking elapsed
MsgHeaderTick{}                // 1s — drives elapsed time in header
MsgScrollUp{}
MsgScrollDown{}
MsgToggleModal{Content}
MsgToggleExpand{}               // ctrl+o expand/collapse agent log
```

### Flow

Pipeline/agent events come in from the engine → adapter layer converts them to typed Bubbletea messages → `App.Update` routes each message to the relevant component(s) → components update their own state and return commands → `App.View` composes component views into the final layout.

The adapter layer is the only place that touches the `pipeline.PipelineEvent`, `agent.Event`, and `llm.TraceEvent` types. Components never see raw engine types. The adapter maps:

- `pipeline.PipelineEvent` → `MsgNodeStarted`, `MsgNodeCompleted`, `MsgNodeFailed`, `MsgPipelineCompleted`, `MsgPipelineFailed`
- `agent.Event` (type `EventTextDelta`) → `MsgTextChunk`
- `agent.Event` (type `EventToolCallEnd`) → `MsgToolCallEnd`
- `llm.TraceEvent` (kind `TraceText`) → `MsgTextChunk`
- `llm.TraceEvent` (kind `TraceReasoning`) → `MsgReasoningChunk`
- `llm.TraceEvent` (kind `TraceRequestStart`) → `MsgLLMRequestStart` (also triggers `MsgThinkingStarted`)
- `llm.TraceEvent` (kind `TraceFinish`) → `MsgLLMFinish` (also triggers `MsgThinkingStopped`)
- `llm.TraceEvent` (kind `TraceToolPrepare`) → `MsgToolCallStart`
- `llm.TraceEvent` (kind `TraceProviderRaw`) → `MsgLLMProviderRaw` (filtered by verbose mode)

## File Structure

```
tui/
├── app.go              # Layout + message routing (~200 lines)
├── state.go            # StateStore — central state container (~150 lines)
├── styles.go           # StyleRegistry — all shared styles/colors/lamps (~80 lines)
├── messages.go         # All typed message constants (~80 lines)
├── adapter.go          # Converts pipeline/agent/LLM events → typed messages (~130 lines)
├── scrollview.go       # Reusable scroll container (~120 lines)
├── header.go           # Header component + token tracker (~80 lines)
├── statusbar.go        # Track diagram + progress + keybinding hints (~100 lines)
├── nodelist.go         # Node list with signal lamps (~180 lines)
├── agentlog.go         # Streaming agent log + coalescing + expand/collapse (~250 lines)
├── modal.go            # Overlay container + choice/freeform content models (~120 lines)
├── interviewer.go      # Gate request/reply bridge + mode-1/mode-2 runners (~100 lines)
├── thinking.go         # ThinkingTracker — timer + state machine (~100 lines)
└── tui_test.go         # Tests for state, messages, thinking, scroll, adapter (~500 lines)
```

Everything lives in a single `tui/` package — the existing `tui/dashboard/`, `tui/components/`, and `tui/render/` subdirectories are flattened. No import cycle risk since everything is in one package.

**Replaces:** All existing files in `tui/`, `tui/dashboard/`, `tui/components/`, `tui/render/`.

**Preserves:** The external interface — `runTUI()` in `cmd/tracker/main.go` calls into the TUI the same way it does today. Also preserves `SetVerboseTrace()` for the verbose trace flag.

**Target:** ~2,090 lines total (down from ~4,590 — cutting duplication while maintaining identical functionality plus thinking indicators).

## What This Does NOT Change

- The visual design (signal lamps, colors, layout proportions)
- The information displayed (same fields, same data)
- The `cmd/tracker/main.go` integration surface
- The pipeline or agent packages
