# Claude Code Backend for Tracker Pipeline Nodes (v2)

Revised after pedantic review by Steve Yegge, Dependency Hawk, Go Purist, HN Commenter, and Spec Lawyer.

## Problem

Tracker's codergen handler runs an agent loop using the LLM SDK directly. This limits nodes to tracker's built-in tools. Users want pipeline nodes that access the full Claude Code environment: MCP servers, skills, hooks, CLAUDE.md awareness, and Claude Code's tool suite.

## Why This Is Worth It

Claude Code in pipeline nodes gives you:
- **MCP servers** — databases, APIs, external tools unreachable by tracker's 6 built-in tools
- **CLAUDE.md** — project instructions applied automatically without baking them into every prompt
- **Same tool developers use** — no behavior gap between interactive and pipeline execution
- **Ecosystem access** — skills, hooks, plugins from Claude Code's growing ecosystem

The ongoing cost (subprocess management, NDJSON parsing, event adapter maintenance) is justified only if we build it as a **backend platform** — not a one-off second handler.

## Key Design Decisions (from review feedback)

1. **No third-party SDK.** Write an internal ~300 line wrapper. The NDJSON protocol is `exec.Command` + `json.Decoder`. We own every line.
2. **Backend platform, not second handler.** Extract an `AgentBackend` interface. Codergen and Claude Code both implement it. Adding backend #3 (Cursor, Aider, whatever) is config, not code.
3. **Registry aliasing, not name override.** Add `Alias(from, to)` to `HandlerRegistry` for the global override. Handlers keep their real identity.
4. **No build tags.** The wrapper has zero external deps. Always compile it in.
5. **Concrete error mapping.** Define exact exit code → outcome rules. No hand-waving.

## Architecture

### The Backend Platform

```go
// pipeline/backend.go

// AgentBackend executes an agent session and streams events.
// Each backend (native, claude-code, future) implements this.
type AgentBackend interface {
    // Run executes an agent session with the given prompt and config.
    // Events are emitted via the callback as they stream in.
    // Returns the final transcript text when complete.
    Run(ctx context.Context, cfg AgentRunConfig, emit func(AgentEvent)) (AgentResult, error)
}

// AgentRunConfig carries everything a backend needs to run a session.
type AgentRunConfig struct {
    Prompt       string
    SystemPrompt string
    Model        string
    Provider     string
    WorkingDir   string
    MaxTurns     int
    Timeout      time.Duration

    // Claude Code specific (ignored by native backend)
    MCPServers      []MCPServerConfig
    AllowedTools    []string
    DisallowedTools []string
    MaxBudgetUSD    float64
    PermissionMode  string // "plan", "autoEdit", "fullAuto"
}

// MCPServerConfig defines an MCP server to attach to a session.
type MCPServerConfig struct {
    Name    string
    Command string
    Args    []string
}

// AgentEvent is the union of events backends can emit.
// Maps 1:1 to existing agent.Event types.
type AgentEvent struct {
    Type      AgentEventType
    Text      string
    ToolName  string
    ToolInput string
    ToolOutput string
    ToolError  string
    Provider  string
    Model     string
    Usage     TokenUsage
    Err       error
}

// AgentResult is the final output of a backend run.
type AgentResult struct {
    Transcript string
    Turns      int
    ToolCalls  map[string]int
    Usage      TokenUsage
}

// TokenUsage tracks token consumption.
type TokenUsage struct {
    InputTokens   int
    OutputTokens  int
    EstimatedCost float64
}
```

### New Files

| File | Purpose |
|------|---------|
| `pipeline/backend.go` | `AgentBackend` interface, `AgentRunConfig`, `AgentEvent`, `AgentResult` |
| `pipeline/handlers/backend_native.go` | Native backend — wraps existing `agent.Session` |
| `pipeline/handlers/backend_claudecode.go` | Claude Code backend — spawns `claude` CLI, parses NDJSON |
| `pipeline/handlers/backend_claudecode_test.go` | Tests for Claude Code backend |
| `pipeline/handlers/codergen.go` | Refactored — delegates to whichever `AgentBackend` is configured |
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
                              │   ├─ node has provider="claude-code" → ClaudeCodeBackend
                              │   ├─ --backend claude-code flag set  → ClaudeCodeBackend
                              │   └─ otherwise                       → NativeBackend
                              │
                              ├─ backend.Run(ctx, config, emitFunc)
                              │   └─ streams AgentEvents → TUI
                              │
                              └─ Build Outcome from AgentResult
```

Key insight: **one handler (`codergen`), multiple backends.** The handler does prompt resolution, config building, artifact writing, auto-status parsing. The backend just runs the agent session. This means:
- No registry aliasing needed — it's always `codergen`
- No handler name tricks — backend selection happens inside the handler
- Adding a third backend = implementing one interface, zero handler changes

### Native Backend (wraps existing agent.Session)

```go
// backend_native.go — wraps the existing agent loop

type NativeBackend struct {
    client    *llm.Client
    workdir   string
}

func (b *NativeBackend) Run(ctx context.Context, cfg AgentRunConfig, emit func(AgentEvent)) (AgentResult, error) {
    // Build agent.SessionConfig from cfg (same as current codergen logic)
    // Create agent.Session
    // Run session, translating agent.Event → AgentEvent via emit()
    // Return AgentResult with transcript and stats
}
```

This is a refactoring of the existing codergen handler's session management. No behavior change.

### Claude Code Backend (internal, no SDK dependency)

```go
// backend_claudecode.go — spawns claude CLI, parses NDJSON

type ClaudeCodeBackend struct {
    cliPath string // resolved path to claude binary
}

func (b *ClaudeCodeBackend) Run(ctx context.Context, cfg AgentRunConfig, emit func(AgentEvent)) (AgentResult, error) {
    // 1. Build CLI args from config
    // 2. Spawn claude subprocess with process group isolation
    // 3. Stream NDJSON from stdout via json.Decoder
    // 4. Switch on message type, emit AgentEvents
    // 5. On context cancellation: SIGKILL process group
    // 6. Return AgentResult
}
```

~300 lines. Zero external dependencies.

### CLI Args Construction

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
  [--mcpServers '{"name": {"command": "cmd", "args": ["a1"]}}']
```

### NDJSON Message Types We Handle

Based on the Claude Code CLI's stream-json output:

| NDJSON `type` field | Our AgentEvent | TUI Effect |
|---------------------|----------------|------------|
| `system` | Emit `Preparing` (provider, model) | Model info in header |
| `assistant` with `text` content | Emit `TextDelta` | Text streams in log |
| `assistant` with `thinking` content | Emit `ReasoningDelta` | Reasoning in muted style |
| `assistant` with `tool_use` content | Emit `ToolCallStart` | Tool indicator |
| `user` with `tool_result` content | Emit `ToolCallEnd` | Tool result |
| `result` | Extract usage, build AgentResult | Summary stats |
| Unknown type | Log warning, skip | No TUI effect |

Unknown message types are logged and skipped — not crashes. This is the key defense against CLI protocol changes.

### Subprocess Lifecycle

**Startup:**
- Resolve `claude` binary via `exec.LookPath`
- Validate minimum CLI version via `claude --version` (fail fast with clear error)
- Set `Setpgid: true` for process group isolation (same pattern as `agent/exec/local.go`)

**Streaming:**
- `json.NewDecoder(stdout)` in a goroutine
- Each decoded message dispatched via `emit()` callback
- Stderr captured for error diagnostics

**Cancellation (ctrl-c):**
- Context cancellation triggers `syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)` (process group kill)
- Same proven pattern from `agent/exec/local.go:98-130`
- Claude Code CLI's own signal handlers and MCP server cleanup are bypassed — we kill the whole group

**Concurrent sessions:**
- Each `Run()` call is independent (own subprocess, own stdin/stdout)
- Two parallel nodes running claude-code will have separate working directories or use the same workdir
- **Constraint: parallel claude-code nodes sharing a workdir may clobber files.** This is the same constraint as two developers editing the same repo. Document it, don't try to solve it.

**Orphan prevention:**
- Process group kill ensures child processes (MCP servers) die with the parent
- `cmd.Wait()` called in defer to reap zombies

### Error Handling

| Condition | Exit code | Outcome | Rationale |
|-----------|-----------|---------|-----------|
| CLI not on PATH | — | Fatal error (handler returns error) | Can't proceed |
| CLI version too old | — | Fatal error | Protocol mismatch risk |
| Auth missing (no CLAUDE_API_KEY / CLAUDE_CODE_OAUTH_TOKEN) | 1 + stderr contains "auth" | `OutcomeFail` with clear error | Non-retryable config issue |
| Rate limited | 1 + stderr contains "rate" or "429" | `OutcomeRetry` | Transient |
| Network error | 1 + stderr contains "network" or "ECONNREFUSED" | `OutcomeRetry` | Transient |
| Budget exceeded | 1 + stderr contains "budget" | `OutcomeFail` | Intentional limit |
| Context cancelled | -1 (killed) | Propagate ctx.Err() | Pipeline handles |
| OOM killed | 137 | `OutcomeFail` | Non-retryable |
| Other non-zero exit | varies | `OutcomeFail` | Unknown = don't retry |
| Successful completion | 0 | Parse auto_status or `OutcomeSuccess` | Normal path |

Error classification: parse stderr for known patterns. This is imperfect but pragmatic — the CLI doesn't define structured error codes.

### Token Counting

The Claude Code CLI's `result` message includes a `usage` field with `input_tokens`, `output_tokens`, and `cost_usd` (when available). We parse what's there and pass it through.

**If usage data is missing:** The TUI header shows "—" for tokens/cost instead of 0. The `AgentResult.Usage` fields remain zero-valued. This is an honest representation of "we don't have this data" rather than a misleading zero.

**Behavioral difference from native backend:** The native backend tracks per-turn token usage via `llm.TokenTracker`. The Claude Code backend provides session-level totals only. The `TokenTracker` accumulates these the same way — just fewer data points.

## Activation

### Path 1: Per-node in .dip file

```dip
agent BuildFeature {
  provider: "claude-code"
  model: "claude-sonnet-4-6"
  prompt: "Build the feature described in $goal"
  max_turns: 30
  mcp_servers: "postgres=npx @modelcontextprotocol/server-postgres $DATABASE_URL"
  allowed_tools: "Read,Write,Edit,Bash,mcp__postgres__query"
  permission_mode: "fullAuto"
}
```

**Dippin adapter change** (`pipeline/dippin_adapter.go:extractAgentAttrs`):

No change needed. `cfg.Provider` already maps to `attrs["llm_provider"]`. The codergen handler reads `llm_provider` and selects the backend accordingly. No `type` override needed since we're not adding a new handler — just a new backend within the existing handler.

New attrs (`mcp_servers`, `allowed_tools`, `disallowed_tools`, `max_budget_usd`, `permission_mode`) pass through as raw string attrs via the dippin IR's `Params` map.

### Path 2: Global CLI override

```bash
tracker --backend claude-code pipeline.dip
```

**CLI changes:**
- Add `backend string` field to `runConfig`
- Add `fs.StringVar(&cfg.backend, "backend", "", "Agent backend: claude-code (default: native)")` to parseFlags
- Pass `cfg.backend` to registry construction
- Registry passes it to `CodergenHandler` at construction time
- No interaction with `--format` or `--resume` — backend is orthogonal

**Backend selection precedence:**
1. Node-level `llm_provider: "claude-code"` → ClaudeCodeBackend (always wins)
2. Node-level `llm_provider: "native"` or `llm_provider: "codergen"` → NativeBackend (explicit override, even with --backend flag)
3. `--backend claude-code` flag → ClaudeCodeBackend (default for unspecified nodes)
4. No flag, no node-level override → NativeBackend

### MCP Server Parsing

Format: one server per line, `name=command arg1 arg2`

```
mcp_servers: """
postgres=npx @modelcontextprotocol/server-postgres $DATABASE_URL
github=npx @modelcontextprotocol/server-github
"""
```

Parsing rules:
- Split on newlines, trim whitespace, skip empty lines
- Split each line on first `=` only (command may contain `=`)
- If no `=` found → error: "malformed mcp_servers entry: %q"
- If name is empty → error
- If command is empty → error
- Shell-split the command portion (respecting quotes) for `Args`
- Duplicate names → error
- MCP server process failure at startup → `OutcomeFail` with error describing which server failed

### `allowed_tools` / `disallowed_tools` Interaction

- Both empty → Claude Code uses its defaults
- Only `allowed_tools` set → allowlist only
- Only `disallowed_tools` set → denylist only
- Both set → error: "cannot set both allowed_tools and disallowed_tools"

### `reasoning_effort` Handling

Not passed to Claude Code. The native backend maps it to the API parameter. For Claude Code, Claude's own reasoning behavior applies. If users need to influence reasoning, they include instructions in their `system_prompt`. This is a documented behavioral difference, not a bug.

## Prompt Resolution (Shared)

Extract from codergen into `pipeline/handlers/prompt.go`:

```go
func ResolvePrompt(node *pipeline.Node, pctx *pipeline.PipelineContext,
    graphAttrs map[string]string) (string, error)
```

Three params (dropped `artifactDir` and `runID` — not used in prompt resolution). Handles:
- Graph variable expansion (`$goal`, `$target_name`)
- Fidelity resolution + context compaction
- Pipeline context injection (prior node outputs)

Both backends call it. The codergen handler refactoring is a pure extraction — no behavior change.

## Observability Differences

Honest about what's degraded:

| Metric | Native Backend | Claude Code Backend |
|--------|---------------|-------------------|
| Per-turn tokens | Yes | Session totals only |
| Cost estimate | Per-turn | Session total (if CLI provides) |
| Files modified/created | Tracked by agent session | Not tracked (CLI manages files) |
| Cache hits/misses | Tracked | N/A |
| Compactions | Tracked | N/A (CLI manages context) |
| Tool calls by name | Tracked per-event | Tracked per-event (parity) |
| Thinking/reasoning | Streamed | Streamed (parity) |
| Text streaming | Token-level | Token-level (parity) |

The TUI shows "—" for unavailable metrics, not misleading zeros.

## Platform Constraints

- **Linux/macOS only.** The subprocess management uses `Setpgid` (POSIX). Windows support is out of scope, same as the existing tool handler.
- **Claude Code CLI required.** `npm install -g @anthropic-ai/claude-code`. Validated at handler construction time, not first use.
- **Auth required.** `CLAUDE_API_KEY` or `CLAUDE_CODE_OAUTH_TOKEN`. Validated before first session.
- **Parallel file conflicts.** Two claude-code nodes sharing a workdir may clobber files. Document as a constraint, same as two developers on one repo.

## Testing Strategy

### Unit Tests (always run)
- `AgentRunConfig` building from node attrs
- MCP server string parsing (happy path + all error cases)
- NDJSON message parsing (mock JSON lines → AgentEvent)
- Error classification (stderr patterns → outcome mapping)
- `ResolvePrompt` parity with current codergen behavior
- Backend selection precedence (node-level > flag > default)

### Integration Tests (`//go:build integration`)
- Requires Claude Code CLI installed
- One-shot prompt → verify AgentResult
- Tool use → verify ToolCallStart/End events
- Budget cap → verify OutcomeFail
- Cancellation → verify process group killed
- Invalid auth → verify clear error

## Migration

No breaking changes. The refactoring of codergen to use `AgentBackend` is internal. External behavior is identical. The platform abstraction exists for future backends but adds zero overhead to the current path.

## Out of Scope (Future)

- MCP servers at pipeline level (shared across nodes) — currently per-node
- Subagent spawning within Claude Code sessions
- Session resume across pipeline checkpoints
- Additional backends (Cursor, Aider, etc.) — the platform makes this cheap
