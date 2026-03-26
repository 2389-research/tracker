# Claude Code Backend for Tracker Pipeline Nodes (v3)

Revised after expert panel review (Systems Architect, Go Engineer, Reliability Engineer, Security Reviewer, DX Expert, Spec Auditor, Steve Yegge).

## Problem

Tracker's codergen handler runs an agent loop using the LLM SDK directly. This limits nodes to tracker's built-in tools. Users want pipeline nodes that access the full Claude Code environment: MCP servers, skills, hooks, CLAUDE.md awareness, and Claude Code's tool suite.

## Why This Is Worth It

Claude Code in pipeline nodes gives you:
- **MCP servers** — databases, APIs, external tools unreachable by tracker's 6 built-in tools
- **CLAUDE.md** — project instructions applied automatically without baking them into every prompt
- **Same tool developers use** — no behavior gap between interactive and pipeline execution
- **Ecosystem access** — skills, hooks, plugins from Claude Code's growing ecosystem

## Key Design Decisions

1. **No third-party SDK.** Write an internal ~400 line wrapper. The NDJSON protocol is `exec.Command` + `json.Decoder`. We own every line.
2. **Backend platform, not second handler.** Extract an `AgentBackend` interface. Codergen and Claude Code both implement it. Adding backend #3 is config, not code.
3. **Use existing types.** The backend emits `agent.Event` directly and returns `agent.SessionResult`. No new event/result types — avoids round-trip translation and data loss.
4. **No build tags.** The wrapper has zero external deps. Always compile it in.
5. **Separate `backend` attr from `provider`.** The .dip `backend:` field selects the execution engine. The `provider:` field selects the LLM API. They are orthogonal.
6. **SIGTERM then SIGKILL.** Graceful shutdown for long-running sessions with MCP server connections.
7. **.dip files are trusted input.** MCP server commands and tool lists execute with the process user's privileges. This is the same trust model as the existing `tool_command` handler.

## Prerequisites

**Dippin-lang upstream change required.** The dippin IR's `AgentConfig` struct needs either:
- New fields: `Backend`, `MCPServers`, `AllowedTools`, `DisallowedTools`, `MaxBudgetUSD`, `PermissionMode`
- Or a generic `Params map[string]string` on `AgentConfig`

This is a change to `github.com/2389-research/dippin-lang`. Must be released before the .dip per-node activation path works. The `--backend` CLI flag works without this change.

## Architecture

### The Backend Interface

```go
// pipeline/backend.go

// AgentBackend executes an agent session and streams events.
type AgentBackend interface {
    Run(ctx context.Context, cfg AgentRunConfig, emit func(agent.Event)) (agent.SessionResult, error)
}

// AgentRunConfig carries common config all backends need.
type AgentRunConfig struct {
    Prompt       string
    SystemPrompt string
    Model        string
    Provider     string
    WorkingDir   string
    MaxTurns     int
    Timeout      time.Duration

    // Backend-specific config. Each backend type-asserts its own config.
    // Native backend ignores this. Claude Code backend expects *ClaudeCodeConfig.
    Extra any
}

// ClaudeCodeConfig holds Claude-Code-specific settings.
type ClaudeCodeConfig struct {
    MCPServers      []MCPServerConfig
    AllowedTools    []string
    DisallowedTools []string
    MaxBudgetUSD    float64
    PermissionMode  PermissionMode
}

// PermissionMode controls Claude Code's tool approval behavior.
type PermissionMode string

const (
    PermissionPlan     PermissionMode = "plan"
    PermissionAutoEdit PermissionMode = "autoEdit"
    PermissionFullAuto PermissionMode = "fullAuto"
)

// MCPServerConfig defines an MCP server to attach to a session.
type MCPServerConfig struct {
    Name    string
    Command string
    Args    []string
}
```

Key choices:
- `emit func(agent.Event)` — uses the existing event type. No new `AgentEvent`.
- Returns `agent.SessionResult` — uses the existing result type. Claude Code populates what it can (turns, tool calls, usage), leaves the rest zero-valued.
- `Extra any` — backend-specific config. Clean extension point that doesn't pollute the common struct.

### New Files

| File | Purpose |
|------|---------|
| `pipeline/backend.go` | `AgentBackend` interface, `AgentRunConfig`, `ClaudeCodeConfig`, `MCPServerConfig` |
| `pipeline/handlers/backend_native.go` | Native backend — wraps existing `agent.Session` |
| `pipeline/handlers/backend_claudecode.go` | Claude Code backend — spawns `claude` CLI, parses NDJSON |
| `pipeline/handlers/backend_claudecode_test.go` | Tests |
| `pipeline/handlers/prompt.go` | Shared `ResolvePrompt` extracted from codergen |

### How It Fits Together

```
.dip file → node attrs → CodergenHandler.Execute()
                              │
                              ├─ ResolvePrompt() (shared)
                              │
                              ├─ Build AgentRunConfig from attrs
                              │
                              ├─ Select backend:
                              │   ├─ node has backend="claude-code" → ClaudeCodeBackend
                              │   ├─ --backend claude-code flag set  → ClaudeCodeBackend
                              │   └─ otherwise                       → NativeBackend
                              │
                              ├─ backend.Run(ctx, config, emitFunc)
                              │   └─ streams agent.Events → TUI
                              │
                              └─ Build Outcome from agent.SessionResult
```

One handler (`codergen`), multiple backends. The handler does prompt resolution, config building, artifact writing, auto-status parsing. The backend just runs the session.

### Backend Injection

`CodergenHandler` receives backends at construction time:

```go
type CodergenHandler struct {
    nativeBackend    AgentBackend  // always available
    claudeCodeBackend AgentBackend // nil if claude CLI not found (lazy init OK)
    defaultBackend   string        // from --backend flag, "" means native
    // ... existing fields
}
```

`ClaudeCodeBackend` is constructed lazily on first use. CLI path resolution and version check happen at that point, not at handler construction (avoids Node.js startup latency on every pipeline run).

### Native Backend

Wraps existing `agent.Session`. Pure refactoring — extract the session creation + run logic from codergen's `Execute()` into `NativeBackend.Run()`. No behavior change. The `agent.Event` objects from the session pass through directly to the `emit` callback.

### Claude Code Backend (~400 lines, zero external deps)

#### CLI Invocation

```
claude -p "<prompt>"
  --output-format stream-json
  --model <model>
  --max-turns <n>
  --permission-mode <mode>
  [--system-prompt <text>]
  [--allowedTools <csv>]
  [--disallowedTools <csv>]
  [--budget <usd>]
  [--mcpServers '<json>']
```

**CRITICAL: Use `exec.Command` with discrete arguments. NEVER pass prompt through `sh -c`.** The prompt may contain shell metacharacters from prior node outputs.

#### Subprocess Environment

Construct a **minimal `cmd.Env`** containing only:
- `PATH`, `HOME`, `TERM`
- `CLAUDE_API_KEY` or `CLAUDE_CODE_OAUTH_TOKEN`
- Any env vars explicitly declared in node attrs

Do NOT inherit the full parent environment. This prevents credential leakage.

#### NDJSON Message → agent.Event Mapping

| NDJSON `type` | Content | agent.Event emitted | TUI effect |
|---------------|---------|---------------------|------------|
| `system` | — | Synthetic `EventLLMRequestPreparing` (provider, model) | Model info in header |
| `assistant` | `text` block | `EventTextDelta` | Text streams in log |
| `assistant` | `thinking` block | `EventReasoningDelta` | Reasoning in muted style |
| `assistant` | `tool_use` block | `EventToolCallStart` (name + input) | Tool indicator |
| `user` | `tool_result` block | `EventToolCallEnd` (output + error) | Tool result |
| `result` | — | Extract usage, build `SessionResult` | Summary stats |
| Unknown type | — | Log warning, skip | No TUI effect |

Claude Code backend constructs `agent.Event` values directly. The existing `transcriptCollector` and TUI adapter work unchanged.

#### Token Counting

The `result` message includes `input_tokens`, `output_tokens`, and optionally `cost_usd`. Populate `agent.SessionResult.Usage` from these. If usage data is missing, fields remain zero-valued and the TUI shows "—" (not misleading zeros).

#### Subprocess Lifecycle

**Startup:**
- Lazy-resolve `claude` binary via `exec.LookPath` on first `Run()` call
- Validate minimum CLI version (pin as constant: `MinClaudeCodeVersion = "1.0.0"`)
- Parse version with semver, fail fast with clear error
- Set `Setpgid: true` for process group isolation

**Streaming (goroutine contract):**
1. Decode goroutine reads `json.NewDecoder(stdout)` until `io.EOF` or context cancellation
2. Each decoded message switches on `type`, emits `agent.Event` via `emit()` callback
3. JSON parse errors: log warning, skip to next line (do not terminate session)
4. `emit()` is wrapped in `recover()` to prevent goroutine death on callback panic
5. Goroutine signals completion via `sync.WaitGroup`
6. Main goroutine waits for decode goroutine to finish *before* calling `cmd.Wait()`
7. Stderr captured in `bytes.Buffer` (read after process exits)
8. Set `cmd.WaitDelay = 5 * time.Second` to handle inherited pipe handles

**Cancellation (two-phase shutdown):**
1. Context cancellation sends `SIGTERM` to process group (`syscall.Kill(-pid, SIGTERM)`)
2. Wait up to 5 seconds for graceful exit (MCP servers close connections)
3. If still running, escalate to `SIGKILL` (`syscall.Kill(-pid, SIGKILL)`)

**Orphan prevention:**
- Process group kill ensures MCP server child processes die with the parent
- `cmd.Wait()` called in defer to reap zombies
- `WaitDelay` prevents hang if children keep pipes open

#### Error Handling

Classification priority (first match wins, case-insensitive):

| Priority | Stderr pattern | Exit code | Outcome | Rationale |
|----------|---------------|-----------|---------|-----------|
| 1 | Context cancelled | -1 (killed) | Propagate `ctx.Err()` | Pipeline handles |
| 2 | `"authentication"` or `"unauthorized"` or `"invalid api key"` | any | `OutcomeFail` | Non-retryable config |
| 3 | `"rate limit"` or `"429"` or `"throttl"` | any | `OutcomeRetry` | Transient |
| 4 | `"budget"` or `"spending limit"` | any | `OutcomeFail` | Intentional limit |
| 5 | `"ECONNREFUSED"` or `"network"` or `"connection"` | any | `OutcomeRetry` | Transient |
| 6 | Exit 137 | 137 | `OutcomeFail` | OOM kill, non-retryable |
| 7 | Exit 0 | 0 | Parse auto_status or `OutcomeSuccess` | Normal |
| 8 | Any other non-zero | varies | `OutcomeFail` | Unknown = don't retry |

When classification falls through to #8, log the full stderr at WARN level so operators can add new patterns.

### Prompt Resolution (Shared)

Extract from codergen into `pipeline/handlers/prompt.go`:

```go
func ResolvePrompt(node *pipeline.Node, pctx *pipeline.PipelineContext,
    graphAttrs map[string]string, artifactDir string) (string, error)
```

Four params (added `artifactDir` for fidelity/compaction path). Handles:
- Graph variable expansion (`$goal`, `$target_name`)
- Fidelity resolution + context compaction
- Pipeline context injection (prior node outputs)

Both backends call it. The codergen handler refactoring is a pure extraction — no behavior change.

## Activation

### Path 1: Per-node in .dip file

```dip
agent BuildFeature {
  backend: "claude-code"
  provider: "anthropic"
  model: "claude-sonnet-4-6"
  prompt: "Build the feature described in $goal"
  max_turns: 30
  permission_mode: "fullAuto"
  mcp_servers: "postgres=npx @modelcontextprotocol/server-postgres $DATABASE_URL"
  allowed_tools: "Read,Write,Edit,Bash,mcp__postgres__query"
}
```

`backend:` and `provider:` are separate fields. `backend` selects execution engine, `provider` selects LLM API. Requires the dippin-lang prerequisite (see Prerequisites section).

### Path 2: Global CLI override

```bash
tracker --backend claude-code pipeline.dip
```

Works without any dippin-lang changes. All codergen nodes run via Claude Code. The node's `llm_model` is passed through to `WithModel()`.

### Backend selection precedence

1. Node-level `backend: "claude-code"` → ClaudeCodeBackend (always wins)
2. Node-level `backend: "native"` → NativeBackend (explicit override, even with --backend flag)
3. `--backend claude-code` flag → ClaudeCodeBackend (default for nodes with no explicit backend)
4. No flag, no node-level backend → NativeBackend

### Permission Mode

Default: `PermissionFullAuto` (pipeline nodes run headless — interactive approval hangs the process).

To restrict: set `permission_mode: "autoEdit"` (blocks shell commands) or `permission_mode: "plan"` (blocks everything, requires approval — only for human-supervised nodes).

Invalid values are rejected at config parse time with a clear error.

### MCP Server Parsing

Format: one server per line, `name=command arg1 arg2`

Parsing rules:
- Split on newlines, trim whitespace, skip empty lines
- Split each line on first `=` only (command portion may contain `=`)
- If no `=` → error: `"malformed mcp_servers entry: %q"`
- If name empty or command empty → error
- Command portion split on whitespace for `Args` (no shell expansion, no quote handling in v1)
- Duplicate names → error
- Commands are NOT shell-expanded. They are passed directly to the Claude Code CLI's `--mcpServers` JSON.

### `allowed_tools` / `disallowed_tools`

- Both empty → Claude Code defaults
- Only one set → that list applies
- Both set → error: `"cannot set both allowed_tools and disallowed_tools"`
- Tool names are passed through as-is (no validation against Claude Code's tool list in v1)

## Observability

| Metric | Native Backend | Claude Code Backend |
|--------|---------------|-------------------|
| Per-turn tokens | Yes | Session totals only |
| Cost estimate | Per-turn | Session total (if CLI provides) |
| Files modified/created | Tracked | Not tracked (CLI manages files) |
| Cache hits/misses | Tracked | N/A |
| Compactions | Tracked | N/A |
| Tool calls by name | Per-event | Per-event (parity) |
| Text streaming | Token-level | Token-level (parity) |

Zero-valued `SessionResult` fields display as "—" in the TUI, not misleading zeros.

### Pipeline-Level Budget

The engine tracks cumulative `EstimatedCost` from `SessionResult.Usage` across all nodes. If a pipeline-level budget is configured (via graph attr `max_budget_usd`), the engine halts execution when the total exceeds the threshold. This applies to both backends.

## Behavioral Differences from Native Backend

| Feature | Native | Claude Code | Rationale |
|---------|--------|-------------|-----------|
| `reasoning_effort` | API parameter | Not supported — log warning if set | Claude Code manages reasoning |
| `cache_tool_results` | Per-session cache | N/A | Claude Code manages caching |
| `context_compaction` | Tracker compacts | N/A | Claude Code has built-in compaction |
| Path containment | `safePath()` enforcement | No containment | Claude Code manages file access |
| Concurrent file edits | Tracker controls | No coordination | Same as two developers on one repo |

## Platform Constraints

- **Linux/macOS only.** Subprocess management uses `Setpgid` (POSIX).
- **Claude Code CLI required** for claude-code backend. `npm install -g @anthropic-ai/claude-code`.
- **Min CLI version:** Pinned as constant, validated lazily on first use.
- **Auth required.** `CLAUDE_API_KEY` or `CLAUDE_CODE_OAUTH_TOKEN`. Validated before first session, clear error if missing.
- **Parallel file conflicts.** Two claude-code nodes sharing a workdir may clobber files. Document as constraint.

## Testing Strategy

### Unit Tests (always run)
- `AgentRunConfig` building from node attrs (table-driven)
- MCP server string parsing (happy path + all error cases)
- NDJSON message parsing (mock JSON lines → `agent.Event`)
- Error classification (stderr × exit code → outcome, with priority tests)
- `ResolvePrompt` parity with current codergen behavior
- Backend selection precedence (node > flag > default)
- Permission mode validation

### Integration Tests (`//go:build integration`)
- Requires Claude Code CLI installed
- One-shot prompt → verify `SessionResult`
- Tool use → verify `EventToolCallStart`/`End`
- Cancellation → verify process group killed, no orphans
- Invalid auth → verify clear error before execution
- Version check → verify rejection of old CLI

## Migration

No breaking changes. The refactoring of codergen to use `AgentBackend` is internal. External behavior is identical. The `--backend` flag is additive. The .dip `backend:` attr requires the dippin-lang prerequisite.

## Out of Scope (Future)

- MCP servers at pipeline level (shared across nodes) — currently per-node
- Subagent spawning within Claude Code sessions
- Session resume across pipeline checkpoints (document resume hazard in user docs)
- Additional backends (Cursor, Aider, etc.) — the platform makes this cheap
- Typed attrs system (replaces `map[string]string` parsing) — would benefit all handlers
- Pipeline-level concurrency limiter for claude-code nodes
