# ACP Backend Expert Review — Consolidated Findings

**Date:** 2026-04-03
**Reviewers:** 10-agent expert panel (Architecture, Protocol Compliance, Error Handling, Concurrency, Security, API Contract, Integration, Go Idioms, Edge Cases, Cultural/Cat/iPhone7)
**Scope:** `pipeline/handlers/backend_acp.go`, `backend_acp_client.go`, `codergen.go`, `backend.go`, `dippin_adapter.go`, `cmd/tracker/flags.go`, `cmd/tracker/run.go`

---

## CRITICAL (must fix before real use)

| # | Issue | Source | Status |
|---|-------|--------|--------|
| C1 | **McpServers nil in NewSessionRequest** — SDK validates `McpServers != nil`, our code passes nil. Every `NewSession` call will fail. | Protocol, API Contract | **FIXING** |
| C2 | **CodergenHandler not registered when llmClient=nil** — `registry.go:180` skips CodergenHandler when no API keys configured. `--backend acp` without API keys = no agent nodes execute. | Architecture, Integration | **FIXING** |
| C3 | **CreateTerminal leaks API keys** — Uses `os.Environ()` instead of `buildEnv()`, exposing API keys to terminal subprocesses spawned by the agent. | Security | **FIXING** |
| C4 | **StopReason from PromptResponse ignored** — `buildACPResult` doesn't check or surface `resp.StopReason`. Cancellations, errors, and max-turns-exceeded look identical to success. | Protocol, Error Handling | TODO |
| C5 | **turnCount never incremented** — `acpClientHandler.turnCount` stays 0. The fallback in `buildACPResult` masks this but `SessionResult.Turns` is always 0 or 1. | Architecture | TODO |
| C6 | **No token usage tracking** — ACP agents return usage data in PromptResponse but we never extract it. All ACP runs report 0 tokens. | API Contract | TODO |
| C7 | **buildEnv() strips ALL env vars matching provider keys** — Non-Claude ACP agents (codex, gemini) may need their own API keys. Stripping `OPENAI_API_KEY` breaks codex-acp; stripping `GEMINI_API_KEY` breaks gemini. | Security, Integration | TODO |
| C8 | **No MCP server passthrough** — Node attrs `mcp_servers` are parsed for claude-code but never passed to ACP `NewSessionRequest.McpServers`. | Protocol | TODO |
| C9 | **context.WithTimeout leak on early return** — If `ensureAgentPath` fails after timeout is set, `cancel()` is deferred but the context could leak. Minor in practice but wrong. | Concurrency | TODO |

## IMPORTANT (should fix soon)

| # | Issue | Source |
|---|-------|--------|
| I1 | **Unbounded terminal output buffer** — `terminalState.output` is `bytes.Buffer` with no size limit. A chatty subprocess can OOM the tracker process. | Concurrency, Edge Cases |
| I2 | **Terminal output race** — `cmd.Stdout = &ts.output` shares buffer between writer goroutine and reader without synchronization on read path in `TerminalOutput`. | Concurrency |
| I3 | **No graceful shutdown** — Only SIGKILL for process cleanup. ACP protocol has no explicit "close session" before killing. Agent state may be lost. | Protocol |
| I4 | **Stderr capture goes to parent** — `cmd.Stderr = &stderr` in main process but terminal stderr goes to shared buffer. No structured error surfacing to TUI. | Error Handling |
| I5 | **ensureAgentPath --version side effect** — Runs `--version` on every new agent binary. Some ACP bridges may not support this; the log.Printf fallback is correct but noisy. | Go Idioms |
| I6 | **No retry on ACP Initialize failure** — If the agent is slow to start, Initialize times out with no retry. Unlike native backend's transient error retry. | Error Handling |
| I7 | **Missing ContentBlock type coverage** — `extractContentText` only handles `Text` blocks. ACP supports Image, ToolUse, ToolResult blocks that are silently dropped. | Protocol |
| I8 | **buildACPPromptBlocks prefixes system prompt with "System:"** — ACP protocol has no system prompt concept in Prompt. Prepending "System:" as a text block is a workaround that may confuse agents. | Protocol |
| I9 | **No permission mode passthrough** — `ACPConfig` doesn't carry `permission_mode` from node attrs. Always auto-approves everything regardless of pipeline config. | API Contract |
| I10 | **File operations don't respect workingDir as root** — `ReadTextFile`/`WriteTextFile` only check `filepath.IsAbs()`, not that the path is under `workingDir`. Agent can read/write anywhere on disk. | Security |
| I11 | **Test coverage gaps** — No tests for: error paths in Run(), Initialize failure recovery, concurrent terminal operations, large file reads, timeout behavior. | Go Idioms |
| I12 | **classifyError reused from claude-code** — Exit code semantics differ between Claude CLI and ACP bridges. Exit code 2 from claude = auth error, but from codex-acp = unknown. | Integration |
| I13 | **No model validation** — `SetSessionModel` failure is logged but the prompt proceeds with the agent's default model. Silent model mismatch. | API Contract |
| I14 | **Line/Limit filtering in ReadTextFile off by one** — `*p.Line - 1` for 0-based indexing, but `lines[start:end]` may exclude the last line when Limit is exactly the remaining count. | Edge Cases |
| I15 | **PlanEntry handling incomplete** — `u.Plan != nil` is a no-op. Plan entries contain useful info (Title, Content) that could feed the TUI activity log. | Protocol |
| I16 | **No health check on agent binary** — `ensureAgentPath` caches the path permanently. If the binary is removed or updated, the stale path is used until tracker restarts. | Edge Cases |
| I17 | **Missing ToolCall status handling** — Only ToolCallUpdate with completed/failed status emits events. Running/pending updates are dropped. | Protocol |

## MINOR (backlog)

| # | Issue | Source |
|---|-------|--------|
| M1 | **Terminal ID uses PID** — `term-<pid>` is unique per process but not globally unique. Two concurrent terminals with recycled PIDs could collide. | Edge Cases |
| M2 | **No agent binary version reporting** — Token tracker reports "acp" as provider. Should include agent name for multi-agent pipelines (e.g., "acp:claude-code-acp"). | Go Idioms |
| M3 | **Cleanup doesn't wait for SIGKILL** — `cleanup()` sends SIGKILL but doesn't wait for process exit. Zombie processes possible on macOS. | Concurrency |
| M4 | **JSON marshal fallback in formatRawInput** — Silent `json.Marshal` error path returns `fmt.Sprintf("%v")`. Unlikely but produces ugly output. | Go Idioms |
| M5 | **ToolCallContent.Diff only logs path** — `extractToolCallOutput` shows "diff path" but not the actual diff content. Useful info lost. | Protocol |
| M6 | **safeEmit panic recovery logs but doesn't propagate** — A panic in the event handler is swallowed. Could mask real bugs during development. | Error Handling |
| M7 | **No connection close** — `acp.ClientSideConnection` may need explicit Close() call. Current code relies on stdin.Close() + process exit. | Protocol |
| M8 | **Cat reviewer notes**: The review is purrfect but needs more naps between tool calls. | Cultural |
| M9 | **iPhone 7 notes**: On a 4.7" screen, ACP error messages would require horizontal scrolling. Consider shorter error prefixes. | UX |
| M10 | **extractContentText assumes single text block** — ACP ContentBlock is a union type. Multi-part content (text + image) only gets the text. | Protocol |
| M11 | **No resource cleanup on Initialize failure** — If Initialize fails, `killProcess` is called but pipes aren't closed first. | Concurrency |
| M12 | **acp_agent attr naming** — Uses underscore (`acp_agent`) while ACP ecosystem uses hyphens (`claude-code-acp`). Minor inconsistency. | Go Idioms |
| M13 | **WriteTextFile permissions hardcoded 0644** — No way to set custom permissions from the agent. Fine for most cases. | Edge Cases |
| M14 | **No test for real ACP workflow** — All tests use mocks or unit-level isolation. Zero scenario tests with actual ACP agent execution. | Integration |

---

## Key Observations

### What likely works right now
- CLI flag parsing (`--backend acp`, `--help`)
- Pipeline validation with `backend=acp` params
- Pipeline simulation (no real execution)
- Unit tests (15 tests, 35 subtests pass)
- Provider-to-agent binary name mapping
- Node attr → ACPConfig extraction
- Event translation (SessionUpdate → agent.Event) in isolation

### What definitely doesn't work
- Real ACP workflow execution (C1: McpServers nil → every NewSession fails)
- `--backend acp` without API keys (C2: CodergenHandler never registered)
- Terminal subprocess security (C3: API keys leaked)

### What we don't know (needs empirical testing)
- Whether `claude-code-acp` bridge actually initializes correctly
- Whether the event stream produces usable TUI output
- Whether multi-node ACP pipelines complete
- Whether timeout/cancellation works correctly
- Whether file operations work in real agent workflows
- Whether the auto-approve permission model is sufficient

---

## Fix Priority

1. **C1 + C2 + C3** — Fix these three to unblock real testing
2. **Install `claude-code-acp`** — `npm i -g claude-code-acp`
3. **Run real workflow** — Create minimal .dip with `backend=acp`, run it, observe
4. **C4-C9** — Fix based on real-world observations
5. **I1-I17** — Important items in priority order
6. **M1-M14** — Backlog
