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
├── Header (pipeline name, run ID, elapsed time)
├── NodeList (scrollable node list with signal lamps + thinking spinner)
├── AgentLog (scrollable streaming log with thinking indicator line)
├── Modal (proper overlay component, not string manipulation)
└── ThinkingTracker (tracks LLM thinking state + elapsed time, feeds NodeList + AgentLog)
```

### Key Rules

- `App` does layout and message routing only — under 150 lines.
- Each component owns its own `Update`/`View` — no cross-component reaching.
- All state flows through `StateStore` via Bubbletea messages (not direct mutation).
- `ThinkingTracker` is a standalone component that emits thinking state changes as messages.
- Shared scroll behavior lives in a `ScrollView` helper used by both NodeList and AgentLog.
- All event types are typed constants, no string comparisons.

## Component Breakdown

### StyleRegistry

Single file defining all colors, borders, lamp characters, and lipgloss styles. Every component imports from here instead of defining its own.

### StateStore

Holds pipeline state (node statuses, current stage, errors) and agent state (streaming text, tool calls, thinking status). Components read from it, never write directly. Updated only via typed Bubbletea messages.

### ScrollView

Reusable scroll container with viewport, auto-scroll-to-bottom, and manual scroll override. Used by both NodeList and AgentLog — no more duplicated scroll logic.

### ThinkingTracker

Listens for LLM request/response events. When a request starts and no output has arrived yet, it enters "thinking" state with a timer. Emits `ThinkingStarted` and `ThinkingStopped` messages. NodeList uses this to animate the lamp on the active node. AgentLog uses it to show the "Thinking..." line with elapsed time.

### Modal

Proper overlay that composes on top of the base view using lipgloss's `Place` — no string slicing. Receives its content as a Bubbletea model, so it can host error details, help text, or anything else.

### Header / NodeList / AgentLog

Same visual output as today, but each is a self-contained Bubbletea model with clean `Init`/`Update`/`View`. No reaching into other components' state.

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
- Multiple nodes running concurrently: each gets its own thinking state tracked by node ID.

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
MsgToolCallStart{NodeID, ToolName}
MsgToolCallEnd{NodeID, ToolName, Output, Error}
MsgAgentError{NodeID, Error}

// UI messages
MsgTick{}
MsgScrollUp{}
MsgScrollDown{}
MsgToggleModal{Content}
```

### Flow

Pipeline/agent events come in from the engine → adapter layer converts them to typed Bubbletea messages → `App.Update` routes each message to the relevant component(s) → components update their own state and return commands → `App.View` composes component views into the final layout.

The adapter layer is the only place that touches the `pipeline.PipelineEvent` and `agent.Event` types. Components never see raw engine types.

## File Structure

```
tui/
├── app.go              # Layout + message routing only (~120 lines)
├── state.go            # StateStore — central state container (~150 lines)
├── styles.go           # StyleRegistry — all shared styles/colors/lamps (~80 lines)
├── messages.go         # All typed message constants (~60 lines)
├── adapter.go          # Converts pipeline/agent events → typed messages (~100 lines)
├── scrollview.go       # Reusable scroll container (~120 lines)
├── header.go           # Header component (~60 lines)
├── nodelist.go         # Node list with signal lamps (~180 lines)
├── agentlog.go         # Streaming agent log (~200 lines)
├── modal.go            # Proper overlay component (~80 lines)
├── thinking.go         # ThinkingTracker — timer + state machine (~100 lines)
└── tui_test.go         # Tests for state, messages, thinking, scroll (~400 lines)
```

**Replaces:** All existing files in `tui/`, `tui/dashboard/`, `tui/components/`, `tui/render/`.

**Preserves:** The external interface — `runTUI()` in `cmd/tracker/main.go` calls into the TUI the same way it does today.

**Target:** ~1,650 lines total (down from ~4,590 — cutting duplication and dead weight while maintaining identical functionality plus thinking indicators).

## What This Does NOT Change

- The visual design (signal lamps, colors, layout proportions)
- The information displayed (same fields, same data)
- The `cmd/tracker/main.go` integration surface
- The pipeline or agent packages
