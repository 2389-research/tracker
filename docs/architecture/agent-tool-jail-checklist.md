# Agent-tool jail checklist & threat model

Follow-up to [#275](https://github.com/2389-research/tracker/pull/275) /
[#272](https://github.com/2389-research/tracker/issues/272). This doc pins the
invariant the `writable_paths` filesystem jail depends on — **every agent tool
routes its filesystem mutations through `exec.ExecutionEnvironment`** — and the
CI lint (`make tools-jail-check`) that keeps it from rotting.

## Why this exists

When an agent node sets `writable_paths`, tracker wires a Landlock + `openat2`
sandbox into a single seam: the `ExecutionEnvironment` interface
(`agent/exec/env.go`). `pipeline/handlers/codergen_jail.go` populates
`LocalEnvironment.WriteOpener` / `Remover` / `CommandWrapper`, so any write,
delete, or subprocess that flows through `env.WriteFile` / `env.RemoveFile` /
`env.ExecCommand` is bounded to the declared globs. A tool that instead calls
`os.WriteFile` / `os.Remove` / `os.MkdirAll` directly **bypasses the jail
entirely** — the write lands wherever the path points, with no glob check and
no Landlock.

That is not hypothetical. The #275 round-8 audit found exactly this bug in two
tools (`generate_code`, `write_enriched_sprint`) that called `os.WriteFile`
directly. They had been in the tree for months; the only reason they were
caught is a manual grep for `os.WriteFile` in `agent/tools/`. This lint
replaces that luck with a gate.

## The rule

> Any `agent/tools/` tool that mutates the filesystem **MUST** route through
> `exec.ExecutionEnvironment`. A direct `os.WriteFile` / `os.MkdirAll` /
> `os.Mkdir` / `os.Remove` / `os.RemoveAll` / `os.Rename` / `os.Create` /
> `os.OpenFile` / `os.Truncate` / `os.Symlink` / `os.Link` / `os.Chmod` /
> `os.Chown` call in `agent/tools/*.go` is a **review-blocker**.

The one legal exception is an **unjailed fallback** — see
[The env==nil-fallback invariant](#the-envnil-fallback-invariant).

Read-only `os.*` (`os.ReadFile`, `os.Open`, `os.Stat`, `os.ReadDir`) is **not**
covered by this rule. The jail bounds *writes*, not reads; read/exfil is an
accepted residual risk of the design (see the activity-log threat model in
`CLAUDE.md` and the `writable_paths` spec). The lint deliberately ignores
read-only calls.

## Threat-model table

One row per LLM-callable tool in `agent/tools/` (the 11 types that implement a
`Name()` method and are registered for the model to call). Verified against the
code at the cited lines — re-verify with `grep -nE 'os\.[A-Z]|env\.'
agent/tools/<file>.go` before trusting a row after a refactor.

| Tool (`Name()`)        | File                       | Reads                          | Writes                              | Deletes            | Subprocess                       | Routes through `ExecutionEnvironment`? |
| ---------------------- | -------------------------- | ------------------------------ | ----------------------------------- | ------------------ | -------------------------------- | -------------------------------------- |
| `read`                 | `read.go`                  | `env.ReadFile`                 | —                                   | —                  | —                                | ✅ yes                                  |
| `write`                | `write.go`                 | —                              | `env.WriteFile`                     | —                  | —                                | ✅ yes                                  |
| `edit`                 | `edit.go`                  | `env.ReadFile`                 | `env.WriteFile`                     | —                  | —                                | ✅ yes                                  |
| `apply_patch`          | `apply_patch.go`           | `env.ReadFile`                 | `env.WriteFile`                     | `env.RemoveFile`   | —                                | ✅ yes (add/update/delete + move)       |
| `glob`                 | `glob.go`                  | `env.Glob`                     | —                                   | —                  | —                                | ✅ yes                                  |
| `grep_search`          | `grep.go`                  | `os.Stat` / `os.Open` (read-only, scoped under `env.WorkingDir()`) | — | — | — | ➖ read-only (no mutation; reads bypass jail by design) |
| `bash`                 | `bash.go`                  | —                              | (subprocess)                        | (subprocess)       | `env.ExecCommand` (→ `CommandWrapper` → `__jail-exec`) | ✅ yes |
| `generate_code`        | `generate_code.go`         | —                              | `env.WriteFile` **+ annotated `os.*` fallback when `env==nil`** | — | — | ✅ yes (guarded fallback) |
| `write_enriched_sprint`| `write_enriched_sprint.go` | —                              | `env.WriteFile` **+ annotated `os.*` fallback when `env==nil`** | — | — | ✅ yes (guarded fallback) |
| `dispatch_sprints`     | `dispatch_sprints.go`      | `os.Open` (reads the JSONL plan, read-only) | (delegated)            | —                  | —                                | ✅ yes (writes delegated to the jailed `write_enriched_sprint` tool) |
| `spawn_agent`          | `spawn.go`                 | —                              | (child session)                     | (child session)    | child via `SessionRunner`        | ✅ inherited (child session carries its own `env`; no direct `os.*`) |

Infrastructure files in `agent/tools/` that are **not** LLM-callable tools and
hold no filesystem-mutation surface: `completer.go` (the `Completer` LLM-client
interface + a read-only symlink-resolution helper using `os.Stat`),
`registry.go` (the tool dispatch registry), `truncate.go` (in-memory output
truncation). The lint still scans them; they contain no mutating `os.*` calls.

## The env==nil-fallback invariant

`generate_code` and `write_enriched_sprint` keep a deliberate direct-`os.*`
fallback for the unjailed path:

```go
func (t *GenerateCodeTool) writeFile(ctx context.Context, path, content string) error {
	if t.env != nil {
		// jailed path: env.WriteFile → WriteOpener → openat2 glob check
		return t.env.WriteFile(ctx, rel, content)
	}
	// fallback: only reachable when env == nil
	os.MkdirAll(dir, 0o755)
	return os.WriteFile(path, []byte(content), 0o644)
}
```

**This fallback is not a bypass.** The invariant, traced end-to-end:

1. `pipeline/handlers/backend_native.go` builds `env`. When
   `sessionCfg.WritablePathsSet` is **true**, it requires `b.env` to be a
   `*LocalEnvironment` (else it **refuses to start** — `backend_native.go:45-48`),
   builds a fresh jailed env via `configureJail`, and assigns it to `env`.
2. That same `env` is then passed into **both** tools via
   `tools.WithGenerateEnv(env)` / `tools.WithSprintWriterEnv(env)`
   (`backend_native.go:114,137`).
3. So whenever a jail is active, `t.env` is the **jailed** `*LocalEnvironment`,
   non-nil, and the tool takes the `env.WriteFile` branch.
4. The `os.*` fallback is therefore reachable **only** when `t.env == nil`,
   which can happen only when `b.env` is nil — and that state cannot coexist
   with `WritablePathsSet == true` (step 1 refuses it). **`env == nil` ⟹ no
   active jail ⟹ nothing to bypass.**

Because the invariant is real but invisible to a grep, each fallback function
carries a marker comment:

```go
//jail:allow-unjailed-fallback env==nil ⟹ no active jail; see agent-tool-jail-checklist.md
```

The lint whitelists mutating `os.*` calls inside any function whose doc comment
or body contains that marker. A new tool that adds an `os.WriteFile` **without**
the marker fails the gate.

> If you ever find a path where a writing tool can reach its `os.*` fallback
> **while `writable_paths` is active** (i.e. `env` should have been wired but
> wasn't), that is a real jail bypass — **stop and file it**, don't add a
> marker. The marker asserts the env==nil invariant above; it is not a mute
> button.

## Checklist for adding a new tool

When you add a tool to `agent/tools/` that touches the filesystem:

- [ ] If the tool **writes** files, it takes an `exec.ExecutionEnvironment`
      (not a bare `workDir string`) and writes via `env.WriteFile`.
- [ ] If the tool **deletes** files, it uses `env.RemoveFile`, not `os.Remove`.
- [ ] If the tool **renames/moves** files, it composes `env.ReadFile` +
      `env.WriteFile` + `env.RemoveFile` (see `apply_patch.go`'s move path) —
      there is no `env.Rename`.
- [ ] If the tool **spawns subprocesses**, it uses `env.ExecCommand`, not
      `os/exec.Cmd` directly, so the jail's `CommandWrapper` can re-exec through
      `__jail-exec` with Landlock applied.
- [ ] The tool's writes work when `env` is a jailed `*LocalEnvironment` — add a
      `writable_paths` e2e fixture exercising the new tool.
- [ ] If the tool genuinely needs a direct-`os.*` unjailed fallback, gate it on
      `env == nil`, prove the [env==nil invariant](#the-envnil-fallback-invariant)
      holds for it, and annotate the function with
      `//jail:allow-unjailed-fallback`.
- [ ] `make tools-jail-check` passes.

## The lint

`make tools-jail-check` runs the `go/ast` analyzer at `tools/jailcheck/`. It
parses every non-`_test.go` file in `agent/tools/` and reports any call to a
mutating `os.*` function (`WriteFile`, `MkdirAll`, `Mkdir`, `MkdirTemp`,
`Remove`, `RemoveAll`, `Rename`, `Create`, `CreateTemp`, `OpenFile`,
`Truncate`, `Symlink`, `Link`, `Chmod`, `Chown`, `Lchown`, `Chtimes`),
exiting non-zero with `file:line: os.X called directly in <func>` for each.
AST (not grep) so a mention of `os.WriteFile` inside a doc comment or string
does not false-positive. The single exemption is the
`//jail:allow-unjailed-fallback` marker described above.

It is wired into the `ci:` Makefile target and the CI "Quality Gates" job, and
is unit-tested against `clean` / `violation` fixtures under
`tools/jailcheck/testdata/`.

## Residual risks (not covered by this lint)

- **Reads / exfil-by-read.** The jail bounds writes, not reads. A tool that
  reads outside the workspace (or `grep_search`/`dispatch_sprints` reading an
  attacker-chosen path within it) is not flagged.
- **Network egress.** Out of scope for the filesystem jail entirely.
- **Aliased `os` imports.** The lint matches the `os.X(...)` selector on an
  unaliased `os` import (the convention everywhere in this package). An
  `import ospkg "os"` alias would slip past — flagged here so a reviewer knows
  to reject aliasing the `os` import in `agent/tools/`.
- **Out-of-process backends.** `claude-code` and `acp` run the agent in a
  separate process tracker cannot Landlock; `writable_paths` refuses them at
  start (see `CLAUDE.md` → Agent backends). This lint only governs the
  in-process `native` tool surface.

## See also

- `CLAUDE.md` → "Tool node safety", "writable_paths"/`__jail-exec`, "Agent
  backends".
- `docs/superpowers/specs/2026-06-01-issue-272-writable-paths-enforcement-design.md`
  — full jail design and threat model.
- `agent/exec/env.go`, `agent/exec/local.go` — the `ExecutionEnvironment` seam.
- `pipeline/handlers/codergen_jail.go` — where the jail is wired into `env`.
