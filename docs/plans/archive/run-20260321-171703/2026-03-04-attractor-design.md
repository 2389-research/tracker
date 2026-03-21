# tracker: Go Implementation of Attractor

## Overview

Go implementation of the [strongdm/attractor](https://github.com/strongdm/attractor) framework — a three-layer system for building AI-powered software factories.

**Module path:** `github.com/2389-research/tracker`

## Architecture: Monorepo with Internal Packages

Single `go.mod`, three internal packages mirroring the three spec layers. Built bottom-up: LLM Client → Agent Loop → Pipeline Engine.

```
tracker/
├── cmd/
│   └── tracker/              # CLI entrypoint (run, validate, agent, ask)
│       └── main.go
├── llm/                      # Layer 1: Unified LLM Client
│   ├── client.go             # Client struct, routing, middleware
│   ├── types.go              # Message, Request, Response, Usage, etc.
│   ├── stream.go             # StreamEvent, channel-based streaming
│   ├── catalog.go            # Model catalog (ModelInfo registry)
│   ├── provider.go           # ProviderAdapter interface
│   ├── middleware.go         # Middleware chain
│   ├── anthropic/            # Anthropic Messages API adapter
│   │   └── adapter.go
│   ├── openai/               # OpenAI Responses API adapter
│   │   └── adapter.go
│   └── google/               # Gemini API adapter
│       └── adapter.go
├── agent/                    # Layer 2: Coding Agent Loop
│   ├── session.go            # Session struct, agentic loop
│   ├── config.go             # SessionConfig
│   ├── events.go             # Event types and emitter
│   ├── profile.go            # ProviderProfile (per-provider tools/prompts)
│   ├── tools/                # Tool implementations
│   │   ├── registry.go       # Tool registry + dispatch
│   │   ├── read.go           # File read
│   │   ├── write.go          # File write
│   │   ├── edit.go           # File edit (apply_patch v4a)
│   │   ├── bash.go           # Shell command execution
│   │   └── glob.go           # File search
│   ├── exec/                 # Execution environment abstraction
│   │   ├── env.go            # ExecutionEnvironment interface
│   │   └── local.go          # Local execution (default)
│   └── subagent.go           # Subagent spawning
├── pipeline/                 # Layer 3: Attractor Pipeline Engine
│   ├── engine.go             # Graph traversal, checkpoint/resume
│   ├── parser.go             # DOT file parsing (using gographviz)
│   ├── context.go            # Pipeline state/context
│   ├── validate.go           # Graph validation/linting
│   ├── condition.go          # Condition expression evaluator
│   ├── stylesheet.go         # Model stylesheet parser
│   └── handlers/             # Node handler implementations
│       ├── handler.go        # Handler interface
│       ├── codergen.go       # LLM code generation (uses agent layer)
│       ├── gate.go           # Human approval gate
│       ├── parallel.go       # Fan-out/fan-in
│       ├── conditional.go    # Branch routing
│       ├── script.go         # Shell script execution
│       └── subpipeline.go    # Nested pipeline
├── tui/                      # TUI mode (bubbletea)
│   ├── app.go                # Main TUI model
│   ├── views/                # Sub-views (graph, activity, tokens)
│   └── styles.go             # Lipgloss styles
├── go.mod
└── go.sum
```

## Layer 1: Unified LLM Client

### Providers (all three from day one)

| Provider  | API                          | Env Var             |
|-----------|------------------------------|---------------------|
| Anthropic | Messages API `/v1/messages`  | `ANTHROPIC_API_KEY` |
| OpenAI    | Responses API `/v1/responses`| `OPENAI_API_KEY`    |
| Google    | Gemini API `/v1beta/...`     | `GEMINI_API_KEY`    |

### Key Design Decisions

- **Streaming via Go channels** — `chan StreamEvent`, idiomatic, composable with goroutines/select
- **Native APIs only** — each adapter speaks the provider's native protocol, not compatibility layers
- **Middleware chain** — onion pattern for logging, caching, cost tracking, rate limiting
- **Provider options escape hatch** — `ProviderOptions map[string]any` for provider-specific features
- **Model catalog** — built-in `ModelInfo` registry for capability-based model selection
- **Prompt caching** — automatic `cache_control` injection for Anthropic; OpenAI/Gemini auto-cache
- **Usage tracking with cost estimation** — `EstimatedCost` field computed from catalog pricing

### Core Types

```go
// Client — routes requests to provider adapters
type Client struct { ... }
func NewClientFromEnv() (*Client, error)
func NewClient(opts ...ClientOption) (*Client, error)
func (c *Client) Complete(ctx context.Context, req *Request) (*Response, error)
func (c *Client) Stream(ctx context.Context, req *Request) <-chan StreamEvent

// ProviderAdapter — implemented per provider
type ProviderAdapter interface {
    Name() string
    Complete(ctx context.Context, req *Request) (*Response, error)
    Stream(ctx context.Context, req *Request) <-chan StreamEvent
    Close() error
}

// Key data types
type Message struct { Role Role; Content []ContentPart; ... }
type Request struct { Model string; Messages []Message; Tools []ToolDefinition; ... }
type Response struct { ID string; Model string; Message Message; Usage Usage; Latency time.Duration; ... }
type StreamEvent struct { Text string; ToolCall *ToolCallData; Usage *Usage; Done bool; Err error }
type Usage struct { InputTokens, OutputTokens, TotalTokens int; EstimatedCost float64; ... }
```

### Response Output (Pretty-printable)

```
[anthropic/claude-opus-4-6] 245 tokens in, 89 out (cache: 200 read)
Cost: $0.003 | Latency: 1.2s | Finish: stop
```

## Layer 2: Coding Agent Loop

### Core Concepts

- **Session** — holds conversation state, dispatches tool calls, emits events
- **ProviderProfile** — per-provider tool definitions and system prompts (Anthropic gets Claude Code-style tools, OpenAI gets codex-style tools)
- **ExecutionEnvironment** — abstraction for where tools run (local default, extensible to Docker/K8s/SSH)
- **Event stream** — typed events for UI rendering, logging, integration

### Agentic Loop

1. User submits input
2. Build request using provider profile (system prompt + tools)
3. Call `Client.Complete()`
4. If tool calls in response → execute tools → append results → loop
5. If text-only response → natural completion
6. Drain steering/follow-up queues between rounds
7. Loop detection after 10 identical consecutive tool calls

### Tools

| Tool       | Description                      |
|------------|----------------------------------|
| `read`     | Read file contents               |
| `write`    | Create/overwrite files           |
| `edit`     | Apply patches (v4a diff format)  |
| `bash`     | Execute shell commands           |
| `glob`     | Search for files by pattern      |

### Session Result Output

```
Session a3f2 completed in 2m34s
Turns: 14 | Tool calls: 23 (read: 12, edit: 3, bash: 8)
Files modified: auth.go, auth_test.go
Files created: oauth_handler.go
Tokens: 45,231 (in: 32,100, out: 13,131) | Cost: $0.12
```

## Layer 3: Attractor Pipeline Engine

### Core Concepts

- **DOT parsing** — using `gographviz` library, restricted to Attractor's DOT subset
- **Graph validation** — exactly one start (Mdiamond) and one exit (Msquare), valid handler types, no unreachable nodes
- **Node handlers** — pluggable, implement `Handler` interface
- **Checkpoint/resume** — serializable state after each node for crash recovery
- **Human-in-the-loop** — gate nodes block until human responds
- **Condition expressions** — evaluated against pipeline context for edge routing
- **Model stylesheet** — CSS-like per-node LLM configuration

### Node Types

| Shape           | Handler            | Description                    |
|-----------------|--------------------|--------------------------------|
| `Mdiamond`      | `start`            | Entry point (no-op)            |
| `Msquare`       | `exit`             | Exit point (no-op)             |
| `box`           | `codergen`         | LLM task via coding agent      |
| `hexagon`       | `wait.human`       | Human approval gate            |
| `diamond`       | `conditional`      | Conditional routing            |
| `component`     | `parallel`         | Fan-out                        |
| `tripleoctagon` | `parallel.fan_in`  | Fan-in                         |
| `parallelogram` | `tool`             | Shell/API execution            |
| `house`         | `stack.manager_loop`| Supervisor loop               |

### Pipeline Result Output

```
Pipeline "Implement feature" — SUCCESS in 8m12s
  ✓ Plan         (codergen)   32s    $0.02
  ✓ Implement    (codergen)   5m01s  $0.34
  ✓ Validate     (codergen)   1m44s  $0.08
  ✓ Review Gate  (human)      45s    — [approved by human]
  Total: 67,432 tokens | $0.44
```

## TUI Mode

Built with `bubbletea` + `lipgloss`. Shows:

- Pipeline graph with node status (pending/running/done/failed)
- Current node details (handler, model, turn count, prompt)
- Real-time agent activity stream (tool calls, text output, thinking)
- Token usage with cost estimation and budget bar
- Keyboard shortcuts: quit, pause, steer, view logs, toggle views

## CLI Interface

```bash
tracker run workflow.dot          # Run pipeline (add --tui for TUI mode)
tracker validate workflow.dot     # Validate DOT file
tracker agent "Fix the login bug" # One-shot agent task
tracker ask "What is 2+2?"        # Direct LLM query
```

## Error Handling

### Layer 1
- Typed `LLMError` with provider, status code, retryable flag
- Automatic retry with exponential backoff for 429/5xx (default 3 retries)
- Context cancellation respected throughout
- Streaming errors on channel `.Err` field

### Layer 2
- Tool failures returned to model as error results (model adapts)
- Command timeouts: default 10s, max 10min
- Loop detection: 10 identical consecutive calls → steering warning
- Unrecoverable errors close session with error state

### Layer 3
- Node retry up to `max_retries`, then fallback to `retry_target`
- Checkpoint after every node for crash recovery
- `goal_gate` enforcement before pipeline exit
- Validation errors caught before execution

## Dependencies

| Dependency | Purpose |
|---|---|
| `github.com/awalterschulze/gographviz` | DOT file parsing |
| `github.com/charmbracelet/bubbletea` | TUI framework |
| `github.com/charmbracelet/lipgloss` | TUI styling |

Standard library for HTTP, JSON, concurrency. Minimal external deps.

## Testing Strategy

- **Unit tests**: Interface-based mocking for provider adapters, execution environments, handlers
- **Integration tests**: Real provider API calls behind `go test -tags=integration`
- **TUI tests**: Bubbletea's programmatic test framework
- **DOT parsing tests**: Known-good and known-bad DOT files
- **E2E**: Small pipeline running a real agent task

## Build Order

1. Layer 1: Unified LLM Client (Anthropic adapter first, then OpenAI, then Google)
2. Layer 2: Coding Agent Loop (session, tools, agentic loop)
3. Layer 3: Attractor Pipeline Engine (parser, engine, handlers)
4. TUI mode
5. CLI
