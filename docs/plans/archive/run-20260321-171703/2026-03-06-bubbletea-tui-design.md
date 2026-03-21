# Bubbletea TUI Design — Three-Mode Pipeline UI

## Goal

Replace the bare stdin/stdout console interaction with a Bubbletea-powered UI system supporting three modes: headless interactive, full TUI dashboard, and websocket (deferred).

## Three Modes

| Mode | Flag | Description |
|------|------|-------------|
| 1. Headless interactive | default | Inline bubbletea programs per human gate. Styled input with lipgloss. Pipeline progress via LoggingEventHandler to stderr. |
| 2. Full TUI | `--tui` | Persistent alternate-screen app with dashboard header, split-pane layout, and modal overlays for human gates. |
| 3. WebSocket | TBD | Deferred until modes 1 and 2 are solid. |

## Architecture

The pipeline engine talks to two interfaces it already has:

1. `Interviewer` / `FreeformInterviewer` — for human gates
2. `EventHandler` — for pipeline progress events

The three modes are different implementations of these interfaces. The engine never knows which mode it is in.

```
Mode 1:  BubbleteaInterviewer (inline tea.Program per gate) + LoggingEventHandler
Mode 2:  BubbleteaInterviewer (modal in TUI)               + TUIEventHandler
Mode 3:  WebSocketInterviewer                               + WebSocketEventHandler
```

## Package Structure

```
tui/
├── interviewer.go      # BubbleteaInterviewer — implements Interviewer + FreeformInterviewer
├── components/
│   ├── choice.go       # Bubbletea model for choice selection (arrow keys, enter)
│   ├── freeform.go     # Bubbletea model for text input
│   └── modal.go        # Modal overlay wrapper (used in mode 2)
├── dashboard/
│   ├── app.go          # Main TUI tea.Model — orchestrates layout
│   ├── header.go       # Token counts per provider, pipeline status, elapsed time
│   ├── nodelist.go     # Left panel — node tree with status icons
│   └── agentlog.go     # Right panel — scrolling agent action log
└── events.go           # TUIEventHandler — bridges pipeline events to bubbletea messages
```

## BubbleteaInterviewer

Implements both `Interviewer` and `FreeformInterviewer`. Works in both mode 1 and mode 2 based on whether a TUI program reference is present.

```go
type BubbleteaInterviewer struct {
    tuiProgram *tea.Program  // nil in mode 1
    responseCh chan string   // used in mode 2 for modal responses
}
```

**Mode 1 (headless):** `tuiProgram` is nil. Each `Ask` or `AskFreeform` call creates a short-lived `tea.Program` that renders inline (no alternate screen). Arrow-key choice selection, styled text input, enter to confirm.

**Mode 2 (full TUI):** Sends a message to the TUI program to show a modal overlay. Blocks on `responseCh` until the user submits their answer. The modal dissolves and the pipeline continues.

## Mode 1: Headless Interactive

- Choice gates: arrow-key navigation, highlighted selection, enter to confirm
- Freeform gates: styled text input with prompt and `>` cursor
- Styling via lipgloss: colored prompts, selected/unselected choices, subtle borders
- Pipeline progress printed to stderr via existing LoggingEventHandler
- No alternate screen, no persistent state

## Mode 2: Full TUI

Persistent `tea.Program` in alternate screen mode.

### Layout

```
┌─────────────────────────────────────────────────────────────┐
│ AskAndExecute  ⏱ 2m14s  running                            │
│ Anthropic: 12.4k/3.2k  OpenAI: 8.1k/2.0k  Gemini: 5.3k/1k│
├──────────────────────┬──────────────────────────────────────┤
│ Pipeline             │ Agent Log                            │
│                      │                                      │
│ ✓ Start              │ [ImplementClaude] Reading go.mod...  │
│ ✓ SetupWorkspace     │ [ImplementClaude] Creating file      │
│ ✓ AskUser            │   src/handler.go                     │
│ ✓ InterpretRequest   │ [ImplementCodex] Running tests...    │
│ ⟳ ImplementClaude    │ [ImplementGemini] Writing tests for  │
│ ⟳ ImplementCodex     │   edge case coverage...              │
│ ⟳ ImplementGemini    │                                      │
│ ○ ImplementJoin      │                                      │
│ ○ ValidateBuild      │                                      │
│ ○ CommitWork         │                                      │
├──────────────────────┴──────────────────────────────────────┤
│ ┌─────────────────────────────────────────────────────────┐ │
│ │ What would you like to do?                              │ │
│ │ > build me a REST API_                                  │ │
│ └─────────────────────────────────────────────────────────┘ │
└─────────────────────────────────────────────────────────────┘
```

### Components

- **Header**: pipeline name, elapsed time, overall status, per-provider token in/out counts
- **Left panel (node list)**: linear list of nodes with status icons — `✓` done, `⟳` running (spinner), `✗` failed, `○` pending
- **Right panel (agent log)**: scrolling viewport of agent actions — tool calls, LLM responses, truncated to fit
- **Modal overlay**: appears when a human gate fires, captures input (choice or freeform), dissolves on submit

### Event Bridging

`TUIEventHandler` implements `pipeline.EventHandler`. On each event it calls `tuiProgram.Send(msg)` to push a bubbletea message. The TUI model's `Update` method handles these messages and re-renders.

### Token Tracking

Add `TokenTrackingMiddleware` to the Layer 1 middleware chain (`llm/middleware.go`). It accumulates per-provider input/output token counts. The TUI dashboard header reads from this middleware.

## CLI Wiring

In `cmd/tracker/main.go`:

```
--tui flag present?
  yes → create TUI tea.Program
        BubbleteaInterviewer with program reference
        TUIEventHandler
        run pipeline inside TUI
  no  → BubbleteaInterviewer with nil program reference (inline mode)
        LoggingEventHandler
        run pipeline directly
```

## Not In Scope

- Mode 3 (websocket) — deferred
- Agent tool call detail view (expandable nodes)
- Custom keybindings — use bubbletea defaults
- Mouse support — keyboard only

## Dependencies

- `github.com/charmbracelet/bubbletea` — TUI framework
- `github.com/charmbracelet/lipgloss` — styling
- `github.com/charmbracelet/bubbles` — text input, viewport, spinner components
