# First-win quickstart: `explain` → `quickfix` — design

Status: approved in brainstorming (2026-10-19), pending spec review.
Sub-project 1 of 5 from the competitive analysis (Archon, Fabro, Amplifier,
attractor implementations). The other four, in order: Claude Code skill +
`install.md`, issue → PR built-in, attention queue + notifications, runtime
`for_each` fan-out.

## Why

Today a new user's first run is `tracker doctor` → `tracker ask_and_execute`.
`ask_and_execute` is a 21-node pipeline: spec, three implementations in
separate git worktrees, cross-critique, select, apply. It is impressive but
slow and costly for a first touch, and `doctor` leads with API keys even
though `--backend claude-code` runs on a Claude subscription with none (and
fails its provider check for such pipelines, #716). Archon and Fabro both win
early with a timed, tiny first result.

Target user: both new open-source users and the team / tracker-runner users,
with the CLI as the funnel into tracker-runner (decision C). So the CLI's
first run must win on its own, and its next steps (PR output, notifications)
should preview what tracker-runner does at team scale.

## Decisions (from brainstorming)

| # | Decision | Choice |
|---|---|---|
| 1 | What the first five minutes produce | A two-step ladder: a read-only answer (~90 s), then one small real change |
| 2 | Backend for these workflows | Auto-detect: Claude subscription if `claude` is available, else a configured API key, else one clear setup message; announce the choice |
| 3 | Entry point | Two ordinary built-in workflows (`explain`, `quickfix`), no new subcommand |
| 4 | Scope of auto-detection | Only nodes that ask for it; no change to any existing workflow or the global default |
| 5 | How a `.dip` asks for it | Per-node `backend: auto` now; file a dippin-lang issue for a workflow-level `defaults: backend:` and migrate when it ships |
| 6 | What `quickfix` does | The user describes one change; the agent makes it, tests run if present, a diff gate approves / revises / rejects, approval commits to a local branch; nothing is pushed |

## Scope

In scope:

1. Engine: a new per-node backend value `backend: auto`, resolved once per run,
   announced, and recorded in the checkpoint and `run.json`.
2. Engine: a new `tool_access: read-only` value.
3. `tracker doctor`: no provider-key failure for pipelines that don't need a
   key (#716).
4. Two embedded built-in workflows, `explain` and `quickfix`, with tests.
5. Docs: README quickstart ladder, website, architecture doc, CLAUDE.md,
   CHANGELOG.
6. A dippin-lang issue for a workflow-level `defaults: backend:`.

Out of scope: pushing branches, PRs, GitHub integration (sub-project 3);
making `auto` the global default; overriding the `claude` binary path (#719,
a separate fix); the dippin pin bump to v0.80.0 (#698, pricing-only, separate
PR).

## 1. `backend: auto`

### Where

`CodergenHandler.selectBackend` (`pipeline/handlers/codergen.go`) gains an
`auto` case alongside `claude-code`, `acp`, `native` / `codergen`. Today's
precedence is unchanged: a node's explicit backend wins, then `--backend`,
then native. `auto` is a node value; it is resolved, not passed through.

dippin already passes `AgentConfig.Backend` through without validating it, so
`backend: auto` needs no dippin change.

### Resolution order

Resolved the first time any `auto` node runs in a run, then cached for the run
(mutex-guarded; parallel branches share one answer):

1. The global `--backend` (or `Config.Backend`) is set → use it. `auto` never
   overrides an explicit choice.
2. `claude` is on `PATH` and `claude --version` succeeds (the existing
   `resolveClaudePath` probe, with `buildEnv()`) → `claude-code`.
3. A native provider credential resolves — any of the five provider key names
   the native client reads, or `TRACKER_OPENAI_AUTH=chatgpt` with a saved
   sign-in → `native`.
4. Otherwise the node fails: non-retryable, routable `OutcomeFail`, with the
   message: `no backend available: install Claude Code and run "claude /login"
   (no API key needed), or run "tracker setup" to add an API key`.

### Announce and record

- Before the first `auto` node runs: one stderr line and one TUI activity line,
  e.g. `backend: auto → claude-code (claude 2.1.280 at /opt/homebrew/bin/claude; no API key needed)`
  or `backend: auto → native (ANTHROPIC_API_KEY set)`.
- A new pipeline event `EventBackendResolved{backend, reason, claude_path?}`,
  emitted once, so `--json` consumers and `tracker diagnose` see it.
- The resolved value is persisted in the checkpoint (a dedicated checkpoint
  field, not a context key, so resume compaction and tool interpolation never
  touch it) and written to `run.json` as `backend_resolved`.

### Resume

A resume reuses the recorded value and does not probe again. If the recorded
backend is unavailable now (for example `claude` was uninstalled), the run
fails at the first `auto` node with: `this run resolved backend: auto to
claude-code, which is no longer available; resume with --backend native to
switch deliberately`. It never switches silently.

### Signed-out `claude`

The probe proves `claude` is installed, not signed in. A signed-out `claude`
already fails the node non-retryably via `classifyError` (auth). For a node
whose backend came from `auto`, the failure reason appends: `this run chose
claude-code automatically; run "claude /login", or use --backend native with
an API key`.

## 2. `tool_access: read-only`

`tool_access` recognizes one value today, `none` (no tools), and fails closed:
any other non-empty value also disables all tools. This adds a second
recognized value, `read-only`, case-insensitive and whitespace-trimmed like
`none`:

| Backend | Behavior |
|---|---|
| native | Registers exactly `read`, `grep`, `glob`; no `write`, `edit`, `apply_patch`, `bash`, `spawn_agent` or any env-gated tool. |
| claude-code | `--allowedTools Read,Glob,Grep` and `--disallowedTools Bash,Edit,Write,NotebookEdit`. Refusals do not fail the node: a node with `tool_access` set expects them (`refusalsExpected`, #676). |
| acp | Refused before the agent starts: a non-retryable, routable `OutcomeFail` (the `jailRefusedOutcome` pattern), because Tracker cannot restrict an out-of-process agent's tools. |

Unknown values keep failing closed. The system prompt scrub that runs for
`none` is not applied for `read-only` (the read tools are real).

## 3. `tracker doctor` (#716)

With a pipeline file, doctor determines which backends the pipeline's agent
nodes need (honoring `--backend` and `doctor --backend`):

- Every agent node is `claude-code`, or `auto` with `claude` available → the
  LLM Providers check passes with `not needed by this pipeline (runs on Claude Code)`;
  the existing Claude Code check still runs.
- `auto` and `claude` unavailable → the provider check runs as today, with a
  hint that installing Claude Code removes the need for a key.
- Any `native` (or unset, with no `--backend`) node → unchanged.
- No pipeline file → unchanged.

## 4. Built-in workflows

Both are embedded built-ins (`tracker_workflows.go` `go:embed`), resolvable by
bare name, listed by `tracker workflows`, and copyable with `tracker init`.
Their prompts and scripts are sidecars (`prompts/<name>/`, `scripts/<name>/`),
pinned in the `go:embed` list and `TestEmbeddedSidecarsFollowDirectives`,
following the decomposed-built-in convention.

### `explain`

```
Start → AskQuestion → Answer → Done
```

- `AskQuestion` (human, freeform): "What do you want to know about this repo?"
  Skipped when `--param question="…"` is given. The skip mechanism (an edge
  condition on the param, or a tool node that checks it) is confirmed during
  planning; edge conditions read context keys, and whether params are
  reachable from them is not yet verified.
- `Answer` (agent): `backend: auto`, `tool_access: read-only`. Answers in the
  TUI and writes the answer to `explain.md` in the run directory (written by a
  following tool node from `last_response` via a file, never by interpolating
  LLM output into a shell command).
- `defaults`: `max_cost_cents: 100`, a turn cap on `Answer`, `max_wall_time: 10m`.
- No git writes; the repo is untouched.

### `quickfix`

```
Start → Preflight → AskChange → MakeChange → RunTests → ReviewDiff
ReviewDiff --approve→ Commit → Done
ReviewDiff --revise→ MakeChange   (max 3, restart)
ReviewDiff --reject→ Discard → Done
any step fail → Abort
```

- `Preflight` (tool): the workdir is a git repo with a clean working tree and a
  HEAD; records the current branch and HEAD SHA under `.tracker/quickfix/`.
  Otherwise it fails with a clear message and routes to `Abort`.
- `AskChange` (human, freeform), skipped with `--param change="…"`.
- `MakeChange` (agent): `backend: auto`, normal tools, prompted to make the
  smallest change that does what was asked and to summarize it.
- `RunTests` (tool): a copy of `build_product`'s test detection
  (`lib/verify.sh` + `lib/ci-probe.sh`) under `scripts/quickfix/lib/`, pinned
  by a `lib/parity_test.sh`. "No tests found" is reported, not a failure; a red
  suite is reported to the gate, which decides.
- `ReviewDiff` (human, labeled): shows the diff against the recorded HEAD, the
  test result, and the agent's summary. Labels `approve`, `revise`, `reject`;
  `revise` takes freeform feedback into `MakeChange`. The unattended default
  (autopilot / `--auto-approve`) is `reject` — the safe choice for a change
  nobody reviewed.
- `Commit` (tool): creates `tracker/quickfix/<run-id>` at the recorded HEAD,
  commits the change there (the `CommitIfDirty` secret / binary staging rules,
  unsigned, hooks kept), and returns the user to their original branch with a
  clean tree, so their checkout is unchanged. Prints the branch name and
  `next: turn this into a PR with tracker issue_to_pr (coming soon)`.
- `Discard` (tool): restores the working tree to the recorded HEAD.
- `Abort` (tool, exit 1): leaves the change on disk for inspection and prints
  how to discard it.
- `defaults`: `max_cost_cents: 300`, `max_wall_time: 20m`.

## 5. Testing

Engine (test-first):

- `auto` resolution: table over fake `claude` binaries on a temp `PATH`
  (works / `--version` fails / absent) × credential present / absent;
  `--backend` beats `auto`.
- Once per run: three `auto` nodes, two in parallel branches → exactly one
  probe and one backend; witnesses both the cached reads and the single probe
  (#658).
- Record and resume: `backend_resolved` is in the checkpoint and `run.json`;
  resume reuses it without probing; resume with the recorded backend
  unavailable fails with the named message.
- Signed-out path: an auth failure on an `auto`-chosen claude-code node carries
  the hint.
- `tool_access: read-only`: native registers exactly read/grep/glob and a write
  attempt has no tool; claude-code args carry the allow and deny lists; ACP is
  refused before start; unknown values still disable all tools.
- doctor (#716): all-claude-code, `auto` with and without `claude`, mixed, and
  Joi's exact repro.

Workflows:

- `examples/explain.test.json` and `examples/quickfix.test.json`, run by the
  `make dippin-test` gate: question skip via param; approve, revise ×3 then
  exhausted, reject; preflight failure (dirty tree, not a repo); red tests
  still reach the gate; abort.
- Shell fixture suites beside every `quickfix` script (Preflight, RunTests,
  Commit, Discard), under the existing harness and pipe-stress amplifier; the
  Commit suite checks the branch is created, the original branch and tree are
  unchanged, and secret-looking files are not staged.
- `lib/parity_test.sh` pinning the copied test detector.
- `dippin doctor` grade A for both, once per file.
- End to end: the built binary runs both on a scratch git repo with a fake
  `claude` on `PATH`, so CI makes no LLM calls. `--auto-approve` exercises
  `quickfix`'s reject path (its unattended default); the approve and revise
  paths run through a library-level test with a scripted interviewer.

## 6. Docs

- README Quick Start rewritten as the ladder (`tracker explain` →
  `tracker quickfix` → `tracker issue_to_pr`, coming soon), subscription path
  first.
- `site/content/workflows.html`: cards for both built-ins; the count of
  built-ins updated everywhere it appears.
- `site/content/cli.html`: `backend: auto` and `tool_access: read-only`.
- `docs/architecture/backends.md`: the resolution rules and recording;
  `docs/architecture/handlers/codergen.md`: the two attribute values.
- CLAUDE.md: a gotcha entry for `auto` (resolved once, recorded, never
  switches silently) and `read-only`.
- CHANGELOG: Added (two built-ins, two values) and Fixed (#716).

## 7. Delivery

Three PRs, each self-contained:

1. Engine: `backend: auto` (resolution, event, checkpoint and `run.json`
   recording, resume), `tool_access: read-only`, doctor (closes #716).
2. The two built-ins with their tests and docs.
3. README and website quickstart rewrite.

Plus the dippin-lang issue for workflow-level `defaults: backend:`.

## Risks

- **Detection can't see sign-in.** Mitigated by the auth-failure hint; a
  signed-out first run still fails in seconds with the fix named.
- **`claude-code` refuses `writable_paths`.** Neither workflow uses the jail;
  `explain` relies on `tool_access: read-only`, which each backend enforces.
- **Unattended `quickfix`.** Its gate defaults to `reject`, so autopilot or
  `--auto-approve` never commits an unreviewed change.
- **Built-in count drift.** "four built-in workflows" appears in docs and site
  copy; the docs PR updates every occurrence.
