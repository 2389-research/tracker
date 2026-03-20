# Layer 3: Pipeline Engine — Design Document

## Goal

Build a pipeline orchestration engine that parses workflow definitions into directed graphs, validates them, and executes nodes sequentially with support for conditional routing, parallel fan-out/fan-in, human gates, retry logic, checkpoint/resume, and multi-model LLM configuration via stylesheets.

The engine supports two input formats:
- **Dippin (`.dip`)** — preferred format, parsed via `dippin-lang` and converted through `FromDippinIR()`
- **DOT (`.dot`)** — deprecated format, parsed via `gographviz` (retained for backward compatibility)

## Architecture

The pipeline engine is the top layer of the three-layer attractor architecture. It consumes pipeline definitions (`.dip` or `.dot`) and orchestrates execution by invoking Layer 2 (agent sessions) for LLM tasks and Layer 1 (LLM client) indirectly through the agent.

The engine follows a five-phase lifecycle: Parse → Validate → Initialize → Execute → Finalize. Execution is single-threaded at the engine level (one node at a time), with parallel fan-out handled by spawning goroutines within the parallel handler.

## Package Structure

```
pipeline/
├── graph.go            # Graph, Node, Edge data model
├── parser.go           # DOT → Graph using gographviz (deprecated format)
├── dippin_adapter.go   # Dippin IR → Graph via FromDippinIR() (preferred format)
├── validate.go         # DAG validation rules
├── context.go          # Pipeline context (shared key-value state)
├── condition.go        # Condition expression evaluator
├── handler.go          # Handler interface + registry
├── engine.go           # Core execution loop
├── checkpoint.go       # Checkpoint/resume serialization
├── events.go           # Pipeline event types
├── stylesheet.go       # Model stylesheet (CSS-like LLM config)
├── handlers/
│   ├── start.go        # Start node (no-op)
│   ├── exit.go         # Exit node (no-op)
│   ├── codergen.go     # LLM agent task (invokes Layer 2)
│   ├── tool.go         # Shell command execution
│   ├── conditional.go  # Diamond routing node
│   ├── parallel.go     # Fan-out (component shape)
│   ├── fanin.go        # Fan-in (tripleoctagon shape)
│   └── human.go        # Human gate (hexagon shape)
└── *_test.go files
```

## Graph Model

```go
type Graph struct {
    Name       string
    Nodes      map[string]*Node
    Edges      []*Edge
    Attrs      map[string]string   // graph-level attributes
    StartNode  string              // ID of Mdiamond node
    ExitNode   string              // ID of Msquare node
}

type Node struct {
    ID         string
    Shape      string              // box, hexagon, diamond, etc.
    Label      string
    Attrs      map[string]string   // all DOT attributes
    Handler    string              // resolved handler name from shape
}

type Edge struct {
    From       string
    To         string
    Label      string
    Condition  string              // condition expression
    Attrs      map[string]string   // weight, loop_restart, etc.
}
```

Shape-to-handler mapping:

| Shape           | Handler             |
|-----------------|---------------------|
| `Mdiamond`      | `start`             |
| `Msquare`       | `exit`              |
| `box`           | `codergen`          |
| `hexagon`       | `wait.human`        |
| `diamond`       | `conditional`       |
| `component`     | `parallel`          |
| `tripleoctagon` | `parallel.fan_in`   |
| `parallelogram` | `tool`              |

## Engine Execution Loop

The engine traverses the graph from start to exit, executing one node at a time:

1. Load or create checkpoint
2. Resolve current node (start node or checkpoint resume point)
3. Execute handler for current node
4. Merge context updates from handler outcome
5. Select next edge using priority algorithm
6. Save checkpoint
7. Repeat until exit node reached or pipeline fails

Edge selection priority (from attractor spec):
1. Condition match — evaluate condition expressions against context
2. Preferred label — set by handler via context key
3. Suggested IDs — handler suggests specific next nodes
4. Edge weight — higher weight = higher priority
5. Lexical ordering — deterministic fallback

### Handler Interface

```go
type Outcome struct {
    Status         string            // "success", "retry", "fail"
    ContextUpdates map[string]string // merged into pipeline context
    PreferredLabel string            // hint for edge selection
}

type Handler interface {
    Name() string
    Execute(ctx context.Context, node *Node, pctx *Context) (Outcome, error)
}
```

### Retry Logic

After handler execution:
- If outcome is "retry" and retries remain, jump to `retry_target` node
- If outcome is "fail" and `fallback_retry_target` exists, jump there
- Otherwise pipeline fails

Node-level `max_retries` overrides graph-level `default_max_retry`.

### Goal Gates

Before allowing pipeline exit, the engine checks all `goal_gate=true` nodes achieved "success". Failed goal gates route to their retry targets.

## Context

Thread-safe key-value store shared across all nodes:

```go
type Context struct {
    mu       sync.RWMutex
    values   map[string]string
    internal map[string]string  // loop counters, retry tracking
}
```

Built-in keys: `outcome`, `preferred_label`, `graph.goal`, `last_response`.

Handlers return `ContextUpdates` in their `Outcome`, which the engine merges after each node completes.

## Condition Evaluator

Minimal boolean expression language for edge gating:

```
outcome=success && context.tests_passed=true
outcome!=fail
```

Operators: `=` (equals), `!=` (not equals), `&&` (AND).

Variables resolve against context values. `outcome` and `preferred_label` are special keys. Empty conditions always evaluate true.

## Checkpoint

JSON serialization after each node completion:

```go
type Checkpoint struct {
    RunID          string
    CurrentNode    string
    CompletedNodes []string
    RetryCounts    map[string]int
    Context        map[string]string
    Timestamp      time.Time
}
```

Saved to run directory. On resume, loads checkpoint and continues from next unexecuted node.

## Handlers

### Start / Exit
No-ops. Return success outcome.

### Codergen (box)
Creates `agent.Session` from node attributes (`llm_provider`, `llm_model`, `prompt`, `timeout`). Calls `Session.Run()`. Captures final text as `last_response` in context. Sets outcome based on `auto_status` parsing or defaults to "success".

### Tool (parallelogram)
Runs `tool_command` via execution environment's `ExecCommand`. Captures stdout/stderr in context (`tool_stdout`, `tool_stderr`). Exit code 0 = success, non-zero = fail.

### Conditional (diamond)
No execution logic. The engine's edge selection handles routing based on conditions on outgoing edges.

### Parallel (component)
Spawns goroutines for each outgoing edge's target node. Collects all outcomes. Blocks until all complete. Stores results keyed by node ID in context.

### Fan-in (tripleoctagon)
Waits for all predecessor nodes to complete (tracked via checkpoint). Merges their context outputs. Gating is implicit.

### Human (hexagon)
Uses an `Interviewer` interface. Derives choices from outgoing edge labels. Presents to user, captures response, sets `preferred_label` for edge selection.

```go
type Interviewer interface {
    Ask(prompt string, choices []string, defaultChoice string) (string, error)
}
```

Implementations: `ConsoleInterviewer` (terminal), `AutoApproveInterviewer` (CI/testing).

## Events

```go
type PipelineEventType string

const (
    EventPipelineStarted
    EventPipelineCompleted
    EventPipelineFailed
    EventStageStarted
    EventStageCompleted
    EventStageFailed
    EventStageRetrying
    EventCheckpointSaved
    EventInterviewStarted
    EventInterviewCompleted
    EventParallelStarted
    EventParallelCompleted
)
```

Same observer pattern as Layer 2's `EventHandler` interface.

## Model Stylesheet

CSS-like configuration from `model_stylesheet` graph attribute:

```
* { llm_model: claude-sonnet-4-5; }
.code { llm_model: claude-opus-4-6; }
#review { reasoning_effort: high; }
```

Specificity: universal `*` < class `.name` < ID `#name`. Explicit node attributes override stylesheet rules. Applied during handler initialization to resolve final LLM configuration per node.

## Dependencies

- `github.com/2389-research/dippin-lang/parser` — Dippin `.dip` parsing (preferred format)
- `github.com/awalterschulze/gographviz` — DOT parsing (deprecated format)
- `github.com/2389-research/tracker/agent` — Layer 2 agent sessions
- `github.com/2389-research/tracker/llm` — Layer 1 LLM client (via agent)

## Build Order

Bottom-up, matching Layer 1 and Layer 2 patterns:

1. Graph model (data types)
2. Dippin IR adapter (`FromDippinIR` — preferred format)
3. DOT parser (gographviz → Graph — deprecated format)
4. Validator (structural rules)
5. Context (shared state)
6. Condition evaluator
7. Events
8. Handler interface + registry
9. Individual handlers (start, exit, tool, conditional, codergen, human, parallel, fan-in)
10. Stylesheet
11. Engine (execution loop)
12. Checkpoint (serialization/resume)
13. Integration tests

## NLSpec

The attractor framework defines NLSpec as "a human-readable spec intended to be directly usable by coding agents to implement/validate behavior." This design document itself serves as an NLSpec for Layer 3 — it describes the system in enough detail that a coding agent can implement it directly.
