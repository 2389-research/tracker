# First-win quickstart: `explain` → `quickfix` — design

Status: approved in brainstorming (2026-10-19); revised after a four-lens spec
review (engine feasibility, security, workflows/DSL, product). Pending the
user's spec review.

Sub-project 1 of 5 from the competitive analysis (Archon, Fabro, Amplifier,
attractor implementations). The other four, in order: Claude Code skill +
`install.md`, issue → PR built-in, attention queue + notifications, runtime
`for_each` fan-out.

## Why

Today a new user's first run is `tracker doctor` → `tracker ask_and_execute`.
`ask_and_execute` is a 21-node pipeline (spec, three implementations in
separate git worktrees, cross-critique, select, apply): impressive but slow and
costly for a first touch. `doctor` leads with API keys and the `dippin` binary,
fails its provider check for pipelines that run on a Claude subscription
(#716), and errors when `dippin` is missing although running a built-in never
needs it. And with no API key the engine refuses to construct unless the
*global* `--backend` is `claude-code`. Archon and Fabro both win early with a
timed, tiny first result.

Target user: both new open-source users and the team / tracker-runner users,
with the CLI as the funnel into tracker-runner. The CLI's first run must win on
its own; its next steps should preview what tracker-runner does at team scale.

## Decisions (from brainstorming)

| # | Decision | Choice |
|---|---|---|
| 1 | What the first five minutes produce | A two-step ladder: a read-only answer (~90 s), then one small real change |
| 2 | Backend for these workflows | Auto-detect: Claude subscription if `claude` is available, else a configured native client, else one clear setup message; announce the choice |
| 3 | Entry point | Two ordinary built-in workflows (`explain`, `quickfix`), no new subcommand |
| 4 | Scope of auto-detection | Only nodes that ask for it; no change to existing workflows or the global default |
| 5 | How a `.dip` asks for it | Per-node `backend: auto` now; dippin-lang issue for workflow-level `defaults: backend:` |
| 6 | What `quickfix` does | The user describes one change; the agent makes it on an isolated branch, tests run if present, a diff gate approves / revises / rejects; nothing is pushed |

## First-run transcript (acceptance target)

The design succeeds if a new user on macOS or Linux (Windows: WSL) gets this,
answer in under five minutes from `brew install`, with no API key:

```
$ brew install 2389-research/tap/tracker
$ claude --version          # already installed and signed in, or:
                            # npm i -g @anthropic-ai/claude-code && claude /login
$ cd my-repo
$ tracker explain --param question="what does this repo do?"
backend: auto → claude-code (claude 2.x at /opt/homebrew/bin/claude; no API key needed)
… TUI: Start → Answer → WriteAnswer → Done, answer streamed in the activity log …
answer saved to .tracker/runs/<id>/explain.md
next: make a small change with
  tracker quickfix --param change="<one sentence>"
cost: ~$0.04 usage (included in your Claude plan)
```

Without `--param`, `explain` opens one freeform question modal first. Nothing
before the first agent turn may block: no doctor failure, no `dippin`
requirement, no key prompt. The update check's single stderr line is
acceptable.

## Scope

In scope:

1. Engine: `backend: auto` (shared resolver, announce, record, resume).
2. Engine: tolerate a missing native LLM client when no node needs one.
3. Engine: a general routable refuse-to-start outcome.
4. Engine: `tool_access: read-only`, enforced per backend.
5. `tracker doctor`: per-pipeline backend awareness (#716); `dippin` missing is
   a warning, not an error.
6. Two embedded built-ins, `explain` and `quickfix`, with tests; listed first
   in `tracker workflows` with a "start here" tag.
7. Prep refactor: extract `build_product`'s commit staging rules into its `lib/`
   (shared by `CommitIfDirty` and `FinalCommit`), so `quickfix` can carry a
   parity copy.
8. Docs: README quickstart ladder, website, architecture docs, CLAUDE.md,
   CHANGELOG.
9. Issues: dippin-lang — workflow-level `defaults: backend:`, and `read-only`
   as a known `tool_access` value (it lints DIP139 today). Tracker — a
   freeform reply that matches no label routes by lexical target order
   (`engine_edges.go:234-241`); `build_product`'s ApprovePlan is only correct by
   accident.

Out of scope: pushing, PRs, GitHub (sub-project 3); making `auto` the global
default; overriding the `claude` path (#719); the dippin pin bump (#698,
pricing-only, separate PR).

## 1. `backend: auto`

### Shared resolver

A new `handlers.BackendResolver` (mutex + result) is created once per run in
`buildRegistry` and passed:

- to the registry as a `RegistryOption`; `NewRegistryFactory` copies options,
  so subgraph and manager_loop child registries share the same resolver;
- to the engine as an `EngineOption`, so it can seed and persist the result.

`CodergenHandler.selectBackend` gains an `auto` case that asks the resolver.
Precedence is unchanged otherwise: node value, then `--backend`, then native.
The `selectBackend` "valid:" error list adds `auto`. `Config.Backend: "auto"`
(library) is rejected the same way the CLI's `validateBackend` rejects it:
`auto` is a node value only.

### Resolution order

The first `auto` node of a run resolves; later ones reuse the result:

1. The global `--backend` / `Config.Backend` is set → use it.
2. On resume, a recorded value exists → use it (see Resume).
3. `claude` resolves via `exec.LookPath`, the resolved path is **not inside the
   workdir** (refuses a repo-planted `claude` reached through an absolute
   PATH entry such as `$PWD/bin` or `node_modules/.bin`), and
   `claude --version` succeeds within 10 s (`exec.CommandContext`, `buildEnv()`
   environment; the version text is captured for the announcement) →
   `claude-code`.
4. The native LLM client exists (`h.client != nil`) → `native`. This uses
   exactly what the native backend would use: `--provider`, ChatGPT auth,
   gateways and an injected `Config.LLMClient`, with no second copy of the
   credential logic.
5. Otherwise → refuse-to-start (§3) with: `no backend available: install
   Claude Code and run "claude /login" (no API key needed), or run
   "tracker setup" to add an API key`.

### Engine construction without a key

`resolveCompleter` (`tracker.go:463`) and the CLI's `resolveLLMClient`
(`cmd/tracker/run.go:487`) tolerate a missing client only when the *global*
backend is `claude-code` or `acp`. Both extend that to: the graph — walking
subgraph children too — has no node that needs a native client, i.e. every
agent node's backend is `auto`, `claude-code` or `acp`. Only the
"no providers configured" error is deferred; any other client error (invalid
`TRACKER_OPENAI_AUTH`, a bad gateway kind) still fails construction. A node
that later needs native without a client keeps today's actionable failure in
`ensureNativeBackend`.

### Announce and record

- `EventBackendResolved{backend, reason, claude_path, claude_version}`,
  emitted once per run. The CLI console and TUI render it as
  `backend: auto → claude-code (claude 2.1.280 at /opt/homebrew/bin/claude; no API key needed)`
  or `backend: auto → native (provider anthropic)`. The library never writes
  to stderr itself. When both `claude` and a key exist, the line adds
  `set --backend native to use your API key instead`.
- Event wiring follows `jail_degraded`: `pipeline/events.go`,
  `events_jsonl.go`, `tracker_events_payload.go`, the activity schema
  generator (`make gen-activity-schema`), `tui/adapter.go`,
  `tracker_diagnose_scan.go`, `runmanifest.go`.
- Persisted as a dedicated `Checkpoint.BackendResolved` field (never a context
  key, so resume compaction and tool interpolation never touch it), stamped in
  `saveCheckpoint` like `BundleIdentity`, and copied to `run.json` as
  `backend_resolved` in `applyCheckpointToManifest`.

### Resume

Order: `--backend` > recorded value > fresh probe. The engine seeds the
resolver from `cp.BackendResolved` in `loadCheckpointAndMerge`. A recorded
value must be `native` or `claude-code` (a tampered checkpoint can never steer
to `acp`, whose environment passes through in full); anything else fails the
resume loudly. A recorded `claude-code` still goes through
`resolveClaudePath`, which is how "no longer available" is detected; that
fails with `this run resolved backend: auto to claude-code, which is no longer
available; resume with --backend native to switch deliberately`. If the process
died before the first checkpoint save after resolution, nothing was recorded
and resume probes again — acceptable, stated in the docs.

### Signed-out `claude`

The probe proves `claude` is installed, not signed in. A signed-out `claude`
fails the node through `backendFatalError` (a run-ending handler error, not an
outcome), so for an `auto`-chosen node the error text is wrapped with `this run
chose claude-code automatically; run "claude /login", or use --backend native
with an API key`. The exact logged-out output of `claude --print` is verified
during implementation so `isAuthError` matches it.

## 2. Refuse-to-start outcome

A new `refuseToStartOutcome(kind, nodeID, err)` returns a non-retryable,
routable `OutcomeFail` with a `FailureReason`, emitted before the agent starts.
`jailRefusedOutcome` (`codergen_errors.go:121`, which hardcodes the
`writable_paths` text) becomes a caller. Uses: no backend available, recorded
backend unavailable on resume, and ACP under `tool_access: read-only`.

## 3. `tool_access: read-only`

### Mode, not a boolean

`agent.SessionConfig` and the pipeline node config gain
`ToolAccessMode() ∈ {unrestricted, none, read-only}`.
`IsToolAccessRestricted()` keeps meaning "not unrestricted", so every bypass
guard stays on for `read-only`: `params: permission_mode`, `params:
allowed_tools` and the other #258 guards keep being ignored. Every call site is
made mode-aware:

- `agent/profile.go` `builtInToolsForConfig`
- `agent/session.go` post-registration clear
- `agent/session_run.go` / `session_run_helpers.go` `ToolChoice=none`,
  dispatch drop, and the prompt scrub (applied only for `none`)
- `pipeline/handlers/codergen.go` `applyClaudeCodeToolAccess`,
  `applyToolLists`, `applyPermissionMode`
- `pipeline/handlers/backend_claudecode.go` `buildArgs`
- `pipeline/handlers/backend_claudecode_denials.go` `refusalsExpected` (true
  for `read-only`)

Unknown values keep failing closed (all tools off).

### Per backend

| Backend | Enforcement |
|---|---|
| native | An allowlist filter applied **after every registration step** (built-ins, `WithTools` extras such as `generate_code`, `write_enriched_sprint`, `dispatch_sprints`, `spawn_agent`): exactly `read`, `grep_search`, `glob`. Under `read-only` these three resolve symlinks and refuse any path that leaves the workdir (reusing `evalSymlinksLeniently` containment), and refuse files matching the secret-name rules (`.env`, `.env.*` except `.example`/`.sample`, `id_rsa*`, `*.p12`, private-key `*.pem`/`*.key`), since contents go to an LLM provider. |
| claude-code | `--tools Read,Glob,Grep` (limits the built-in set itself), `--permission-mode dontAsk` with `--allowedTools Read,Glob,Grep` (anything unlisted is refused, not allowed), the canonical deny list minus `Read`/`Glob`/`Grep` as a second layer, `--strict-mcp-config` with no servers (node `mcp_servers` ignored), and `--setting-sources user` (a repo's `.claude/settings.json` hooks don't run). Claude Code's own `Read` follows its own rules; the secret-name refusal is native-only and the docs say so. |
| acp | `refuseToStartOutcome` before the agent starts (Tracker can't restrict an out-of-process agent), checked in `Execute` next to `refuseWritablePathsOnUnsupportedBackend`. |

## 4. `tracker doctor` (#716, `dippin`)

Doctor parses the pipeline (file, built-in or `.dipx`, plus subgraph children)
before the base checks and computes the set of backends its agent nodes need,
honoring `--backend` / `doctor --backend`. That set is passed to the provider
and binary checks:

- No node needs native (all `claude-code`, or `auto` with `claude` resolvable)
  → LLM Providers passes with `not needed by this pipeline (runs on Claude Code)`.
- `auto` with no `claude` → the provider check runs as today, with a hint that
  installing Claude Code removes the need for a key.
- `claude` becomes required (error when missing) whenever a node needs
  claude-code, not only under `--backend claude-code`.
- `doctor --backend claude-code` with no pipeline also passes the provider
  check.
- The `dippin` binary check is a warning (`only needed to lint or edit .dip
  files`), never an error.

## 5. Built-in workflows

Both are embedded (`tracker_workflows.go` `go:embed`), resolvable by bare name,
copyable with `tracker init`, decomposed into `prompts/<name>/` and
`scripts/<name>/` sidecars, pinned in the `go:embed` list and
`TestEmbeddedSidecarsFollowDirectives`, and added to `make doctor`.
`tracker workflows` lists them first, tagged `start here`. Both use
`AbortRun` (tool, exit 1) with a single unconditional `AbortRun -> Done` and
`defaults: on_failure: AbortRun`; every tool node has explicit
`when ctx.outcome = success` / `= fail` edges (a failed tool with only
unconditional edges takes the strict-failure path, not its next node).

Shared conventions:

- A param skips its gate through a declared var with a sentinel and a
  `conditional` router (no `= ""` comparison; dippin rejects it):
  `vars question: ask`, then `NeedQuestion -> Answer when graph.params.question != ask`,
  `NeedQuestion -> AskQuestion`.
- Agent prompts read `${params.<var>}` and the node-scoped
  `${ctx.node.<Gate>.human_response}`. User text (`${params.*}`,
  `human_response`) is never interpolated into a shell command; tools read
  text the agent wrote to a file.

### `explain`

```
Start → NeedQuestion ─(param set)→ Answer
                    └→ AskQuestion → Answer
Answer → WriteAnswer → Done
```

- `AskQuestion` (human, freeform): "What do you want to know about this repo?"
- `Answer` (agent): `backend: auto`, `tool_access: read-only`, `max_turns: 20`;
  its prompt asks it to respect `.gitignore` and skip vendored trees.
- `WriteAnswer` (tool): `cp "$TRACKER_RUN_DIR/Answer/response.md" "$TRACKER_RUN_DIR/explain.md"`
  and prints the `next:` line pointing at `tracker quickfix --param change="…"`.
- `defaults`: `max_wall_time: 10m`, `max_cost_cents: 100` (a backstop; budget
  checks run between nodes, so the turn cap is the real per-node ceiling).
- Tracked files are never modified; Tracker writes its own state under
  `.tracker/`.

### `quickfix`

```
Start → Preflight → NeedChange ─(param set)→ MakeChange
                               └→ AskChange → MakeChange
MakeChange → RunTests → ReviewDiff
ReviewDiff ─approve→ Commit → Done
ReviewDiff ─revise / other text→ AskRevision → CheckReviseBudget
CheckReviseBudget ─ok→ MakeChange (restart)
CheckReviseBudget ─exhausted→ FinalDecision ─approve→ Commit │ ─reject→ Discard
ReviewDiff ─reject→ Discard → Done
any failure → AbortRun
```

**Isolation: a git worktree.** The change is made in a separate worktree on a
new branch, so the user's checkout, branch and working tree are never touched
— a dirty tree no longer blocks the first run, the agent cannot move the user's
branch, and discarding needs no reset. Tool scripts `cd` into the worktree
themselves rather than relying on a tool-node `working_dir`.

- `Preflight` (tool): requires a git repo with at least one commit. Runs
  `exclude_tracker_metadata` (a parity copy of `build_product`'s
  `lib/gitignore.sh`) so `.tracker/` never shows as untracked. Records the base
  SHA (HEAD; detached HEAD is fine) and creates
  `git worktree add .tracker/quickfix/worktree -b tracker/quickfix/<run-id> <base>`.
  Refuses if that path already exists (a stale prior run), printing the exact
  cleanup commands. Notes when the user's checkout is dirty: those uncommitted
  changes are not part of the quickfix base.
- `AskChange` (human, freeform): "What small change should I make?"
- `MakeChange` (agent): `backend: auto`, normal tools,
  `working_dir: .tracker/quickfix/worktree`, `max_turns: 40`. Prompted to make
  the smallest change that does what was asked, and to write a short summary to
  `.tracker/quickfix/summary.md` in the worktree.
- `RunTests` (tool): a small purpose-built detector, not `build_product`'s
  `verify.sh` (which is coupled to its `.ai/build/` layout). It looks for, in
  order: a Makefile `test` target, `go.mod`, `package.json` with a `test`
  script, `pyproject.toml`/`pytest.ini`, `Cargo.toml`; runs the first one found
  in the worktree with a timeout; **always exits 0** and prints a final marker
  `tests-pass`, `tests-fail` or `no-tests`; `output_limit` keeps the tail.
- `ReviewDiff` (human, labels `reject`, `revise`, `approve`; `default: reject`):
  shows `git diff <base>` of the worktree, including untracked new files via
  `git add -N`, with `output_limit` sized for a diff, the test marker and the
  agent's summary. With no tests: "no tests found — review the diff
  carefully".
  - Free text typed into the gate's "other" box routes to `AskRevision`: node
    names are chosen so the lexical fallback (`engine_edges.go:234-241`)
    lands there, pinned by an engine test, and the text is used as the
    revision feedback.
- `AskRevision` (human, freeform): "What should change?"
- `CheckReviseBudget` (tool): a per-run counter file (the `CheckReviewFixBudget`
  pattern), 3 revisions; `max_restarts: 10` stays as a backstop above it,
  because exhausting `max_restarts` hard-fails the run.
- `FinalDecision` (human, labels `reject`, `approve`; `default: reject`).
- `Commit` (tool): in the worktree, stages with the extracted
  `build_product` staging rules (parity copy: secret-name and private-key
  exclusion, compiled-binary exclusion, `.tracker/` excluded), commits
  unsigned with hooks kept, subject `quickfix: change from tracker run <run-id>`
  and body from `summary.md` (read from the file, never interpolated), then
  removes the worktree and keeps the branch. Prints:
  ```
  committed to branch tracker/quickfix/<run-id> (your checkout is unchanged)
  next:
    git diff <base>..tracker/quickfix/<run-id>
    git merge tracker/quickfix/<run-id>      # or cherry-pick
    gh pr create --head tracker/quickfix/<run-id>
  ```
- `Discard` (tool): `git worktree remove --force .tracker/quickfix/worktree`
  and `git branch -D tracker/quickfix/<run-id>`. No reset, no clean.
- `AbortRun` (tool): leaves the worktree in place for inspection and prints the
  two cleanup commands only when the worktree exists.
- `defaults`: `max_wall_time: 20m`, `max_cost_cents: 300` (backstop).

The branch name, base SHA and worktree path are written to
`$TRACKER_RUN_DIR/quickfix.json` so later sub-projects (issue → PR, the
attention queue) find the result without parsing stdout.

### Unattended runs

- `--auto-approve`: both decision gates take `default: reject`, so nothing is
  committed. The freeform asks return `auto-approved`; the docs say to pass
  `--param question=` / `--param change=` for unattended runs.
- `--autopilot`: an LLM judges the gate and can choose `approve`. That is
  accepted: the blast radius is a local, unpushed branch in a separate
  worktree. The docs say so plainly.
- Webhook gates: the default applies on timeout only when the node sets
  `timeout:`; both decision gates set one.

### Cost display

claude-code reports a notional `total_cost_usd` on a subscription; it is
enforced by the backstop caps like any cost. The end-of-run summary labels it
`~$X.XX usage (included in your Claude plan)` when the run's backend is
claude-code.

## 6. Testing

Engine (test-first):

- Resolver: table over fake `claude` binaries on a temp `PATH` (works /
  `--version` fails / hangs past the timeout / absent / inside the workdir) ×
  native client present / absent; `--backend` beats `auto`.
- Shared once per run: three `auto` nodes, two in parallel branches and one in
  a subgraph child → exactly one probe and one backend; witnesses both the
  cached reads and the single probe (#658).
- Construction without a key: an all-`auto` graph constructs with no provider;
  a graph with a native node still fails; an invalid `TRACKER_OPENAI_AUTH` still
  fails.
- Record and resume: `BackendResolved` in the checkpoint and `run.json`;
  resume reuses it; `--backend` overrides it; a tampered value (`acp`, junk)
  fails the resume; recorded claude-code unavailable fails with the named
  message.
- Refuse-to-start is routable and non-retryable for all three kinds.
- `read-only`: native registers exactly `read`/`grep_search`/`glob` after
  extras; symlink-out and secret-name reads are refused; claude-code argv
  matches exactly; `params: permission_mode: bypassPermissions` and
  `params: allowed_tools: Bash` are ignored; ACP is refused; unknown values
  still disable all tools.
- doctor: all-claude-code, `auto` with/without `claude`, mixed, no pipeline
  with `--backend claude-code`, Joi's repro (#716), `dippin` missing is a
  warning.
- Edge selection: free text at `ReviewDiff` routes to `AskRevision`.

Workflows:

- `examples/explain.test.json`, `examples/quickfix.test.json` (run by
  `make dippin-test`); every scenario sets the `graph.params.*` sentinel keys
  (the simulator doesn't seed `vars`). Cover param skip, approve, revise then
  approve, revise budget exhausted → FinalDecision, reject, preflight failure.
- Engine-level tests for what the simulator can't model: a red `RunTests`
  reaches the gate (strict failure), and revise exhaustion routing.
- Shell fixture suites beside every `quickfix` script (Preflight, RunTests,
  Commit, Discard, CheckReviseBudget, AbortRun) under the existing harness and
  pipe-stress amplifier: worktree and branch created; user checkout untouched
  (including a dirty one); `.tracker/` and secret-looking files never staged;
  Discard removes only the worktree and branch; AbortRun prints cleanup only
  when a worktree exists.
- Parity tests for the copied `lib/gitignore.sh` and staging library.
- `dippin doctor` grade A for both, once per file (expect A 95 until dippin
  knows `read-only`).
- End to end with the built binary and a fake `claude` on `PATH`: `explain`
  and `quickfix` with `--param`, `--auto-approve` exercises the reject path;
  the approve and revise paths run through a library test with a scripted
  interviewer.

## 7. Docs

- README Quick Start rewritten to the first-run transcript: subscription path
  first, `tracker explain --param question=…` then `tracker quickfix`, `doctor`
  as troubleshooting rather than step one, `dippin` removed from the quickstart
  line.
- `site/content/workflows.html`: cards for both; the built-in count updated in
  current copy (`workflows.html:380` and elsewhere), never in changelog
  history.
- `site/content/cli.html`: `backend: auto`, `tool_access: read-only`, the doctor
  changes.
- `docs/architecture/backends.md` (resolver, construction without a key,
  recording, resume) and `docs/architecture/handlers/codergen.md` (both
  values).
- CLAUDE.md: gotchas for `auto` (resolved once, recorded, never switches
  silently, resolver shared with child registries) and `read-only` (a mode,
  still "restricted" for every bypass guard).
- CHANGELOG: Added (two built-ins, `backend: auto`, `tool_access: read-only`),
  Changed (doctor `dippin` severity), Fixed (#716).

## 8. Delivery

Four PRs, each self-contained and green on its own:

1. Engine: resolver and `backend: auto`, construction without a key,
   refuse-to-start, `tool_access: read-only`, doctor (closes #716).
2. Prep: extract `build_product`'s commit staging rules into `lib/`, no
   behavior change.
3. The two built-ins with tests and docs.
4. README and website quickstart.

Plus the two dippin-lang issues and the tracker lexical-fallback issue.

## Risks

- **Detection can't see sign-in.** The wrapped auth error names the fix within
  seconds.
- **Claude Code flag drift.** `--tools`, `dontAsk`, `--strict-mcp-config` and
  `--setting-sources` are pinned by an exact-argv test; the minimum `claude`
  version that supports them is stated in the docs.
- **Autopilot can approve.** Accepted; contained to a local branch in a
  separate worktree.
- **Built-in name collisions.** Bare-name resolution prefers a local file
  named `explain` or `quickfix`; this is existing behavior for every built-in.
