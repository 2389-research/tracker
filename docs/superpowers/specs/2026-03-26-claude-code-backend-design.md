# Claude Code Backend for Tracker Pipeline Nodes

## Problem

Tracker's codergen handler runs an agent loop using the LLM SDK directly (Anthropic/OpenAI/Gemini APIs). This works but limits nodes to tracker's 6 built-in tools (read, write, edit, bash, glob, grep_search). Users want pipeline nodes that can access the full Claude Code environment: MCP servers, skills, hooks, and Claude Code's richer tool suite.

## Solution

Add a `claude-code` execution backend that delegates agent nodes to Claude Code via the [partio-io/claude-agent-sdk-go](https://github.com/partio-io/claude-agent-sdk-go) SDK. Two activation paths:

1. **Per-node**: Set `provider: "claude-code"` in the .dip file — only that node uses Claude Code
2. **Global override**: CLI flag `--backend claude-code` — ALL agent nodes run via Claude Code, with the .dip file's `llm_model` passed through via `WithModel()`

## Architecture

### New Files

| File | Purpose |
|------|---------|
| `pipeline/handlers/claudecode.go` | ClaudeCodeHandler — implements Handler interface |
| `pipeline/handlers/claudecode_events.go` | SDK message → agent.Event adapter |
| `pipeline/handlers/claudecode_test.go` | Tests |

### Handler: `ClaudeCodeHandler`

Implements `pipeline.Handler`. Constructor accepts a name override so it can be registered as either `"claude-code"` or `"codergen"` (for the global override path).

```
Execute(ctx, node, pctx) → Outcome
  1. Read node attrs (prompt, system_prompt, llm_model, etc.)
  2. Resolve prompt via shared resolvePrompt() utility
  3. Build SDK options from attrs
  4. Run claude session via SDK
  5. Stream messages, emitting agent.Event for each
  6. Collect transcript, parse auto_status
  7. Write artifacts (prompt + response)
  8. Return Outcome with context updates
```

### Prompt Resolution (Shared with Codergen)

Extract codergen's private `resolvePrompt` logic into a shared package-level function in `pipeline/handlers/prompt.go`. Both `CodergenHandler` and `ClaudeCodeHandler` call it:

```go
func ResolvePrompt(node *pipeline.Node, pctx *pipeline.PipelineContext,
    graphAttrs map[string]string, artifactDir, runID string) (string, error)
```

This handles: graph variable expansion, prompt variable expansion (`$goal`), fidelity resolution, context compaction, and pipeline context injection. Prevents behavioral drift between backends.

### Attr Mapping

| .dip node attr | SDK option | Notes |
|----------------|-----------|-------|
| `prompt` | prompt text (first message) | Required. Resolved via shared ResolvePrompt |
| `system_prompt` | `WithSystemPrompt()` | Optional |
| `llm_model` | `WithModel()` | Optional. Passed through in global override mode too |
| `max_turns` | `WithMaxTurns()` | Optional. Default: 50 (match current codergen) |
| `command_timeout` | context.WithTimeout on ctx | Optional |
| `auto_status` | Parse STATUS: from final response | Same logic as codergen |
| `mcp_servers` | `WithMCPServer()` | New attr. Format: `name=command arg1 arg2` per line |
| `allowed_tools` | `WithAllowedTools()` | New attr. Comma-separated tool names |
| `disallowed_tools` | `WithDisallowedTools()` | New attr. Comma-separated |
| `max_budget_usd` | `WithMaxBudgetUSD()` | New attr. Float |
| `permission_mode` | `WithPermissionMode()` | New attr. Default: auto-accept for pipeline use |
| `working_dir` | `WithCwd()` | Per-node workdir override (falls back to pipeline workdir) |

### SDK Message → agent.Event Mapping

The SDK streams typed messages. Map them to tracker's existing event system for full TUI parity:

| SDK Message | Content Block | agent.Event / llm.TraceEvent | TUI Effect |
|-------------|--------------|------------------------------|-----------|
| Session start | — | Synthetic `EventLLMRequestPreparing` | Model/provider in header |
| First `AssistantMessage` | — | Synthetic `EventLLMRequestStart` | Thinking spinner starts |
| `AssistantMessage` | `TextBlock` | `EventTextDelta` | Text streams in activity log |
| `AssistantMessage` | `ThinkingBlock` | `EventReasoningDelta` | Reasoning in muted style |
| `AssistantMessage` | `ToolUseBlock` | `EventToolCallStart` (name + input) | `⚡ tool` indicator |
| `UserMessage` | `ToolResultBlock` | `EventToolCallEnd` (output + error) | Tool result display |
| End of `AssistantMessage` | — | Synthetic `EventLLMFinish` | Thinking spinner stops |
| `ResultMessage` | — | Session complete. Extract token usage | Summary stats |
| Error | — | `EventError` | Error display |

The event adapter emits real `agent.Event` objects so the existing `transcriptCollector` can be reused for transcript assembly and artifact writing.

### Token Counting

The SDK's `ResultMessage` includes turn count. For token-level tracking:
- Use SDK hooks or stream stats to capture input/output tokens per turn
- Feed into tracker's `llm.TokenTracker` so header shows cost/tokens
- If the SDK doesn't expose per-turn tokens, use `GetStreamStats()` for totals

### Turn Counting

Track turns by counting `AssistantMessage` arrivals. Store in `Outcome.Stats` for the run summary.

### Tool Use Events

Each `ToolUseBlock` in an `AssistantMessage` emits:
```
agent.Event{Type: EventToolCallStart, ToolName: block.Name, ToolInput: block.Input}
```

The corresponding `ToolResultBlock` in the next `UserMessage` emits:
```
agent.Event{Type: EventToolCallEnd, ToolName: name, ToolOutput: block.Content, ToolError: block.Error}
```

This drives the TUI's tool indicators (`⚡ bash`, `▸ read /path`, elapsed time).

### Artifact Writing

Same pattern as codergen via the shared `transcriptCollector`:
- Write expanded prompt to `<artifact_dir>/<node_id>/prompt.md`
- Write full response to `<artifact_dir>/<node_id>/response.md`

### Error Handling

| Error type | Behavior |
|-----------|----------|
| CLI not found | Fatal error — fail the node with clear message |
| Connection error | Map to `OutcomeRetry` (transient) |
| Budget exceeded | Map to `OutcomeFail` |
| Context cancelled | Propagate — pipeline handles cancellation |
| Non-zero exit from claude | Map to `OutcomeFail` or `OutcomeRetry` based on error type |

## Behavioral Differences from Codergen

These codergen features are intentionally not replicated in ClaudeCodeHandler:

| Feature | Codergen | Claude Code | Rationale |
|---------|----------|-------------|-----------|
| `reasoning_effort` | Maps to API parameter | Not directly supported — appended to system prompt as instruction | Claude Code manages its own reasoning |
| `cache_tool_results` | Per-session tool cache | Not applicable | Claude Code manages its own caching |
| `context_compaction` | Tracker compacts context at threshold | Not applicable | Claude Code has built-in auto-compaction |
| `context_compaction_threshold` | Configurable % | Not applicable | Claude Code manages this internally |
| `fidelity` | Affects prompt construction via CompactContext | Prompt is still resolved via shared ResolvePrompt (fidelity applies) | Fidelity affects what context is injected into the prompt, which still matters |

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
}
```

**Dippin adapter change** (`pipeline/dippin_adapter.go`, function `extractAgentAttrs`):

When `cfg.Provider == "claude-code"`, set `attrs["type"] = "claude-code"` in addition to `attrs["llm_provider"]`. This causes `Graph.AddNode()` to use the explicit `type` attribute for handler resolution instead of the default shape-based `"codergen"` mapping.

```go
// In extractAgentAttrs, after setting llm_provider:
if cfg.Provider == "claude-code" {
    attrs["type"] = "claude-code"
}
```

New attrs (`mcp_servers`, `allowed_tools`, `disallowed_tools`, `max_budget_usd`) are passed through as raw string attributes. They do not require changes to dippin-lang's `ir.AgentConfig` — the adapter already handles arbitrary attrs via the config's `Params` map.

### Path 2: Global CLI override

```bash
tracker --backend claude-code pipeline.dip
```

New CLI flag: `--backend` (values: `""` default, `"claude-code"`).

**CLI changes** (`cmd/tracker/main.go`):
- Add `backend string` field to `runConfig`
- Add `fs.StringVar(&cfg.backend, "backend", "", "Execution backend override: claude-code")` to `parseRunFlags`
- Pass `cfg.backend` to `executeCommand` and through to registry construction
- No interaction with `--format` or `--resume` — backend is orthogonal

When set:
- The registry routes `codergen` → `claude-code`: any node that would resolve to the `codergen` handler gets routed to `ClaudeCodeHandler` instead
- The node's `llm_model` attr is passed through to `WithModel()`
- All other node attrs (prompt, system_prompt, max_turns, etc.) are respected

**Registry changes** (`pipeline/handlers/registry.go`):
- Add `WithBackendOverride(backend string)` registry option
- When backend is `"claude-code"`, `NewDefaultRegistry` creates a `ClaudeCodeHandler` with name override `"codergen"` so it intercepts all codergen dispatch
- `ClaudeCodeHandler` constructor: `NewClaudeCodeHandler(name string, ...)` — accepts the handler name to register under

This avoids needing a `RegisterAs` method on the registry. The handler itself reports the overridden name.

### Combined

Both paths can coexist. If `--backend claude-code` is set, it overrides everything. If not set, only nodes with `provider: "claude-code"` use the Claude Code backend.

## Working Directory

The SDK's `WithCwd()` is set to either:
1. The node's `working_dir` attr (per-node override), or
2. The pipeline's workdir (default)

This matches how codergen's agent session gets its working directory.

## Testing Strategy

### Unit Tests
- Attr mapping: verify all node attrs correctly map to SDK options
- Event adapter: verify SDK messages map to correct agent.Events with full TUI coverage
- Auto-status parsing: reuse existing parseAutoStatus logic
- Error mapping: verify error types map to correct outcomes
- Shared ResolvePrompt: verify parity with codergen's prompt construction

### Integration Tests
- Requires Claude Code CLI installed (skip with `//go:build integration`)
- One-shot prompt execution
- Multi-turn with tool use
- MCP server configuration
- Budget cap enforcement

### Conformance
- Run existing tracker-conformance tests with `--backend claude-code` to verify parity

## Dependencies

Add to go.mod:
```
github.com/partio-io/claude-agent-sdk-go
```

Pin to a specific commit hash for stability (the SDK is young, single-maintainer). Consider vendoring if the repo goes unmaintained.

Use a build tag (`//go:build claudecode`) on the handler files so the dependency is optional. Users who don't need Claude Code don't pull the SDK.

Requires at runtime:
- Claude Code CLI installed (`npm install -g @anthropic-ai/claude-code`)
- Authentication: `CLAUDE_API_KEY` or `CLAUDE_CODE_OAUTH_TOKEN`

## Migration

No breaking changes. Existing pipelines work exactly as before. The claude-code backend is opt-in via either per-node declaration or CLI flag.

## Out of Scope (Future)

- Subagent spawning within Claude Code sessions (WithAgent)
- Session resume across pipeline checkpoints
- Custom Claude Code hooks defined in .dip files
- Sharing MCP servers across nodes (currently per-node)
