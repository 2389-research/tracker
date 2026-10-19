# First-win quickstart (`explain` → `quickfix`) Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** A new user gets a read-only answer about their repo in about 90 seconds with no API key (`tracker explain`), then lands one small change on an isolated local branch (`tracker quickfix`).

**Architecture:** Four PRs. (1) Engine: a per-run `pipeline.BackendResolver` behind a new node value `backend: auto`, engine construction without a native client when no node needs one, a routable refuse-to-start outcome, a `tool_access: read-only` mode, and per-pipeline doctor checks. (2) A behavior-neutral extraction of `build_product`'s commit staging rules into `lib/`. (3) Two embedded built-ins. (4) README and website quickstart.

**Tech Stack:** Go 1.25, Dippin (`.dip`) at the go.mod pin, POSIX `sh` tool scripts with `*_test.sh` fixture suites, Claude Code CLI flags.

**Spec:** `docs/superpowers/specs/2026-10-19-first-win-quickstart-design.md`

## Global Constraints

- TDD: every behavior change starts with a failing test; record the red run in the commit message body.
- Never `--no-verify`; the pre-commit hook (fmt, vet, build, tests, complexity ratchet — cognitive ≤ ~8 per function, dippin lint, scenario suites, docs gates, shell gate) must pass on every commit.
- Each PR: branch from latest `origin/main`, body `## Problem` / `## Fix` / `## Verification`, ends with `🤖 Generated with [Claude Code](https://claude.com/claude-code)`; merge with `gh pr merge <n> --merge --delete-branch`.
- Commit trailer: `Co-Authored-By: Claude Opus 5.5 (1M context) <noreply@anthropic.com>`.
- `backend: auto` resolves only to `claude-code` or `native`, never `acp`.
- `auto` is a node value only: library `Config.Backend: "auto"` and CLI `--backend auto` are rejected.
- Resume precedence: `--backend` > recorded `Checkpoint.BackendResolved` > fresh probe.
- `claude --version` probe timeout: 10 s; a resolved `claude` path inside the workdir is refused.
- `tool_access` modes: `""` → unrestricted, `none`, `read-only`; any other non-empty value → all tools off (unchanged). `IsToolAccessRestricted()` is true for `none`, `read-only` and unknown values.
- Native read-only tool set: exactly `read`, `grep_search`, `glob`.
- claude-code read-only argv: `--tools Read,Glob,Grep`, `--permission-mode dontAsk`, `--allowedTools Read,Glob,Grep`, `--disallowedTools` = canonical deny list minus `Read`,`Glob`,`Grep`, `--strict-mcp-config`, `--setting-sources user`; node `mcp_servers` ignored.
- Secret-name rules (native read-only refusal and `quickfix` staging): `.env`, `.env.*` except `.env.example`/`.env.sample`, `id_rsa*`, `*.p12`, and `*.pem`/`*.key` containing `-----BEGIN ... PRIVATE KEY-----`.
- Workflow defaults: `explain` — `max_wall_time: 10m`, `max_cost_cents: 100`, Answer `max_turns: 20`; `quickfix` — `max_wall_time: 20m`, `max_cost_cents: 300`, MakeChange `max_turns: 40`, revise budget 3, `max_restarts: 10`.
- `quickfix` paths: worktree `.tracker/quickfix/worktree`, branch `tracker/quickfix/<run-id>`, record `$TRACKER_RUN_DIR/quickfix.json` with keys `branch`, `base_sha`, `worktree`.
- User text (`${params.*}`, `human_response`) is never interpolated into a shell command.
- Never edit dippin-lang; dippin changes go in issues (memory: dippin-issues-only).

## Review Focus

1. A repo path containing spaces or non-ASCII characters: every `quickfix` script quotes paths; the worktree is created and removed correctly. (Task 13 adds a fixture.)
2. `claude` is installed but its first `--version` exceeds the 10 s timeout (cold npm start): the user expects to be told, not silently moved to a paid API key. Resolution reports `claude found at <path> but did not respond within 10s` and, if it then falls back to native, says so in the announcement. (Task 5 adds a case.)
3. The user is already inside a linked git worktree or a repo with submodules: Preflight's `git worktree add` and `.git/info/exclude` handling still work (exclude resolves through `git rev-parse --git-path info/exclude`). (Task 13 adds a fixture.)
4. A diff larger than the gate's `output_limit`: the gate says the diff was truncated and prints the `git diff <base>..` command to see all of it, rather than showing a silent tail. (Task 13 adds a fixture on `ShowDiff.sh` output; Task 14 checks the gate text.)
5. A branch named `tracker/quickfix/<run-id>` or the worktree path already exists from a crashed run: Preflight fails with the exact cleanup commands and creates nothing. (Task 13 adds a fixture.)

---

## PR 0 — issues (no code)

### Task 0: File the three follow-up issues

- [ ] **Step 1: dippin-lang — workflow-level backend default**

```bash
gh issue create -R 2389-research/dippin-lang --title "workflow defaults: add backend (e.g. defaults: backend: auto)" --body-file - <<'EOF'
Tracker adds a per-node `backend: auto` (resolve claude-code if the claude CLI is available, else the native client). A workflow where every agent should auto-resolve has to repeat it per node. Request: a `defaults: backend:` field in `ir.WorkflowDefaults`, cascading to agent nodes that set none. Values Tracker accepts: native, claude-code, acp, auto.
EOF
```

- [ ] **Step 2: dippin-lang — `read-only` as a known `tool_access` value**

Body: Tracker adds `tool_access: read-only` (native: read/grep/glob only; claude-code: `--tools Read,Glob,Grep` + `dontAsk`); dippin lints DIP139 because it only knows `none`. Request: recognize `read-only`.

- [ ] **Step 3: tracker — freeform text that matches no label routes by lexical target order**

Body: cite `pipeline/engine_edges.go:234-241`; a freeform reply on a labeled gate with equal weights takes the alphabetically first target; `build_product`'s ApprovePlan is correct only because `Decompose` sorts first; propose routing unmatched text to an explicit `other` edge or failing loudly.

- [ ] **Step 4: Record the three issue numbers in the plan's PR bodies.**

---

## PR 1 — engine (`feat/first-win-engine`)

### Task 1: `pipeline.BackendResolver`

**Files:**
- Create: `pipeline/backend_resolver.go`, `pipeline/backend_resolver_test.go`
- Modify: `pipeline/engine.go` (option), `pipeline/handlers/registry.go` (option + factory copy), `tracker.go:529` `buildRegistry` and the engine construction that follows it

**Interfaces:**
- Produces:
  ```go
  type BackendResolution struct {
      Backend       string // "claude-code" | "native"
      Reason        string // human-readable, used in the announcement
      ClaudePath    string
      ClaudeVersion string
      FromRecord    bool   // seeded from the checkpoint
  }
  func NewBackendResolver() *BackendResolver
  func (r *BackendResolver) Resolve(probe func() (BackendResolution, error)) (BackendResolution, error) // runs probe at most once; caches success
  func (r *BackendResolver) Seed(backend string) error // from checkpoint; error unless backend ∈ {"native","claude-code"}
  func (r *BackendResolver) Result() (BackendResolution, bool)
  func WithBackendResolver(r *BackendResolver) EngineOption            // pipeline
  func WithBackendResolver(r *pipeline.BackendResolver) RegistryOption // handlers
  ```
- `NewRegistryFactory` copies the option so child registries share the pointer.

- [ ] **Step 1: Write failing tests** `TestBackendResolver_ResolveOnce` (16 goroutines call `Resolve`; probe counter == 1; all see the same `Backend`; uses `internal/testutil.Witness` for both the probe and the cached reads), `TestBackendResolver_ProbeErrorNotCached` (error then success → probe count 2), `TestBackendResolver_SeedValidates` (`"acp"`, `"auto"`, `"junk"` error; `"native"`, `"claude-code"` ok; seeded `Resolve` returns `FromRecord: true` and never calls probe), `TestRegistryFactory_SharesBackendResolver` (child registry from the factory holds the same pointer).
- [ ] **Step 2:** `go test ./pipeline ./pipeline/handlers -run 'BackendResolver' -count=1` → FAIL (undefined).
- [ ] **Step 3:** Implement with a `sync.Mutex`; `buildRegistry` creates one resolver per run and passes it to the registry and the engine.
- [ ] **Step 4:** Same command → PASS; `go test ./... -short`.
- [ ] **Step 5:** Commit `feat(engine): per-run BackendResolver shared with child registries`.

### Task 2: Record and seed `BackendResolved`

**Files:**
- Modify: `pipeline/checkpoint.go` (field), `pipeline/engine_checkpoint.go:63-68` (stamp), `pipeline/engine_run.go:425` `loadCheckpointAndMerge` (seed), `pipeline/runmanifest.go:399` (copy)
- Test: `pipeline/backend_resolver_checkpoint_test.go`

**Interfaces:**
- Consumes: Task 1 `Seed`, `Result`.
- Produces: `Checkpoint.BackendResolved string \`json:"backend_resolved,omitempty"\``; `RunManifest.BackendResolved string \`json:"backend_resolved,omitempty"\``.

- [ ] **Step 1: Write failing tests** `TestCheckpoint_StampsBackendResolved` (resolver result `claude-code` → saved checkpoint and `run.json` carry `backend_resolved: "claude-code"`), `TestResume_SeedsResolverFromCheckpoint` (checkpoint `native` → resolver `Result()` is `native` with `FromRecord`), `TestResume_TamperedBackendResolvedFails` (`"acp"` → `loadCheckpointAndMerge` error naming the value), `TestResume_GlobalBackendBeatsRecord` (recorded `claude-code`, engine `--backend native` → nodes use native).
- [ ] **Step 2:** Run → FAIL.
- [ ] **Step 3:** Implement the field, stamp, seed (only when `--backend` is unset) and copy.
- [ ] **Step 4:** Run → PASS; `go test ./cmd/tracker-conformance -run TestGoldenTraces -count=1` unchanged.
- [ ] **Step 5:** Commit `feat(engine): record backend: auto's resolution in the checkpoint and run.json`.

### Task 3: `EventBackendResolved`

**Files:**
- Modify: `pipeline/events.go` (type + `BackendResolvedDetail` payload next to `JailDegradedDetail` at :310), `pipeline/events_jsonl.go`, `tracker_events_payload.go`, `scripts/gen/activitylog` + regenerated schema (`make gen-activity-schema`), `tui/adapter.go:39`, `tracker_diagnose_scan.go:152`, `cmd/tracker` console renderer
- Test: event round-trip tests beside the existing `EventJailDegraded` ones

**Interfaces:**
- Produces: `EventBackendResolved PipelineEventType = "backend_resolved"`; `type BackendResolvedDetail struct { Backend, Reason, ClaudePath, ClaudeVersion string; FromRecord bool }` on `PipelineEvent.BackendResolved *BackendResolvedDetail`.

- [ ] **Step 1: Write failing tests** for NDJSON payload fields, activity-log schema round trip, TUI line text `backend: auto → claude-code (claude 2.1.280 at /opt/homebrew/bin/claude; no API key needed)`, the extra clause `set --backend native to use your API key instead` when a native client also exists, and diagnose surfacing the resolved backend.
- [ ] **Step 2:** Run → FAIL. **Step 3:** Implement (the library emits only the event; the CLI console and TUI render it). **Step 4:** Run → PASS; `make docs-check` (schema in sync). **Step 5:** Commit `feat(events): EventBackendResolved, rendered by the console and TUI`.

### Task 4: Routable refuse-to-start

**Files:**
- Modify: `pipeline/handlers/codergen_errors.go:116-` (`jailRefusedOutcome` → caller), `pipeline/handlers/codergen.go:79` `Execute`
- Create: `pipeline/handlers/codergen_refuse.go`, `pipeline/handlers/codergen_refuse_test.go`

**Interfaces:**
- Produces:
  ```go
  type refuseToStartError struct{ Kind string; Err error } // Kind: "writable_paths" | "no_backend" | "backend_unavailable" | "read_only_acp"
  func (e *refuseToStartError) Error() string
  func (e *refuseToStartError) Unwrap() error
  func (h *CodergenHandler) refuseToStartOutcome(kind string, node *pipeline.Node, err error, prompt, artifactRoot string) (pipeline.Outcome, error)
  ```
  `Execute` turns a `*refuseToStartError` from `selectBackend` or the pre-start checks into `refuseToStartOutcome`.

- [ ] **Step 1: Write failing tests** `TestRefuseToStart_RoutableNonRetryable` (each kind → `OutcomeFail`, `FailureReason` contains the error text, no retry, a `when ctx.outcome = fail` edge is taken), `TestJailRefusedOutcome_Unchanged` (existing writable_paths text unchanged).
- [ ] **Step 2–4:** red → implement → green. **Step 5:** Commit `refactor(codergen): general refuse-to-start outcome; jail refusal uses it`.

### Task 5: `backend: auto` in `selectBackend`

**Files:**
- Modify: `pipeline/handlers/codergen.go:231-251` (`selectBackend`, "valid:" list at :240), `pipeline/handlers/backend_claudecode.go:56` (`resolveClaudePath` → capture version, `exec.CommandContext` with 10 s timeout), `pipeline/handlers/backend_claudecode.go:215` / `codergen_errors.go:69` (auth wrap)
- Create: `pipeline/handlers/backend_auto.go`, `pipeline/handlers/backend_auto_test.go`

**Interfaces:**
- Consumes: Task 1 resolver (`h.backendResolver`), Task 4 `refuseToStartError`.
- Produces: `func (h *CodergenHandler) probeAutoBackend() (pipeline.BackendResolution, error)`; `func resolveClaudePathVersion(ctx context.Context, workDir string) (path, version string, err error)`; `errClaudeInWorkdir` sentinel.

- [ ] **Step 1: Write failing tests** using fake `claude` scripts on a temp `PATH`:
  - `TestAuto_ClaudeWorks` → `claude-code`, `ClaudeVersion` = fake output.
  - `TestAuto_ClaudeVersionFails_NativeClient` → `native`.
  - `TestAuto_ClaudeTimesOut` (fake sleeps 15 s; test overrides the timeout to 100 ms) → falls to native when a client exists and `Reason` contains `did not respond`; refuse-to-start `no_backend` when none.
  - `TestAuto_ClaudeInsideWorkdirRefused` (fake at `<workdir>/bin/claude` on PATH) → not used.
  - `TestAuto_NothingAvailable` → refuse-to-start with the spec's exact `no backend available: …` text.
  - `TestAuto_GlobalBackendWins` → `--backend native` with claude present → native, no probe.
  - `TestAuto_SubgraphAndParallelShareOneProbe` (three `auto` nodes: two parallel branches, one subgraph child) → probe count 1.
  - `TestAuto_RecordedClaudeUnavailableOnResume` → refuse-to-start `backend_unavailable` with the spec's exact text.
  - `TestAuto_SignedOutHint` (fake claude emits the auth error) → error text ends with the spec's hint.
- [ ] **Step 2:** `go test ./pipeline/handlers -run 'TestAuto_' -count=1` → FAIL.
- [ ] **Step 3:** Implement; emit `EventBackendResolved` once on first resolution; keep every new function ≤ cognitive 8.
- [ ] **Step 4:** Run → PASS; `go test ./... -short`; `make complexity`.
- [ ] **Step 5:** Commit `feat(codergen): backend: auto resolves claude-code or native once per run`.

### Task 6: Construction without a native client

**Files:**
- Modify: `tracker.go:463` `resolveCompleter`, `tracker.go:516` (reject `Config.Backend == "auto"`), `cmd/tracker/run.go:487` `resolveLLMClient`, `cmd/tracker/flags.go:442` `validateBackend` (error text lists valid values, excludes auto)
- Create: `pipeline/backend_needs.go`, `pipeline/backend_needs_test.go`

**Interfaces:**
- Produces:
  ```go
  type BackendNeeds struct{ Native, ClaudeCode, ACP, Auto bool }
  func ComputeBackendNeeds(g *Graph, subgraphs map[string]*Graph, globalBackend string) BackendNeeds
  func (n BackendNeeds) NeedsNativeClient() bool // true when any agent node resolves to native: explicit native/codergen, or no node backend and globalBackend ""/"native"; auto, claude-code and acp nodes never need it
  ```
  The "no providers configured" condition must be a typed sentinel; if `llm` returns an untyped error today, add `var ErrNoProviders = errors.New(...)` in `llm/` and wrap it.

- [ ] **Step 1: Write failing tests** `TestComputeBackendNeeds` (table: all-auto; auto + claude-code; one native node; subgraph child with a native node; `globalBackend: "claude-code"`), `TestResolveCompleter_DefersNoProvidersForAutoGraph`, `TestResolveCompleter_InvalidChatGPTAuthStillFails`, `TestConfigBackendAutoRejected`, `TestCLIBackendAutoRejected`.
- [ ] **Step 2–4:** red → implement → green. **Step 5:** Commit `feat(engine): construct without a native client when no node needs one`.

### Task 7: `tool_access` mode and the native read-only set

**Files:**
- Modify: `agent/config.go:94-117,185-190`, `agent/profile.go:13`, `agent/session.go:118`, `agent/session_run.go:99,369`, `agent/session_run_helpers.go:36`, `pipeline/handlers/backend_native.go:243-273`, `agent/tools/read.go`, `agent/tools/grep.go:152`, `agent/tools/glob.go`
- Create: `agent/tools/readonly_paths.go`, `agent/tools/readonly_paths_test.go`, `agent/tool_access_mode_test.go`

**Interfaces:**
- Produces:
  ```go
  type ToolAccessMode string // agent
  const ToolAccessUnrestricted, ToolAccessNone, ToolAccessReadOnly ToolAccessMode = "", "none", "read-only"
  func (c SessionConfig) ToolAccessMode() ToolAccessMode // unknown non-empty → ToolAccessNone
  func (c SessionConfig) IsToolAccessRestricted() bool   // unchanged meaning: mode != unrestricted
  func CheckReadOnlyPath(root, path string) error         // tools: symlink containment via evalSymlinksLeniently + secret-name rules
  ```
  The read-only allowlist filter runs after every registration step (built-ins, `WithTools` extras, `spawn_agent`).

- [ ] **Step 1: Write failing tests** `TestToolAccessMode_Parse` (`""`, `none`, ` Read-Only `, `junk`), `TestReadOnly_RegistersExactlyReadGrepGlob` (with `TRACKER_CODEGEN_MODEL` and `TRACKER_SPRINT_WRITER_MODEL` set and a `SessionRunner`, the registry holds exactly `read`, `grep_search`, `glob`), `TestReadOnly_ToolChoiceNotNone` and `TestReadOnly_PromptNotScrubbed`, `TestNone_Unchanged`, `TestCheckReadOnlyPath` (in-repo file ok; symlink to `$HOME/.ssh/id_rsa` refused; `.env` refused; `.env.example` ok; private-key `.pem` refused; public cert `.pem` ok), and read/grep/glob calls returning the refusal as a tool error, not a crash.
- [ ] **Step 2–4:** red → implement → green; `make complexity`. **Step 5:** Commit `feat(agent): tool_access: read-only — read/grep/glob with symlink and secret containment`.

### Task 8: claude-code and ACP under read-only

**Files:**
- Modify: `pipeline/handlers/codergen.go:375,407,422,454-462,495` (`applyMCPServers`, `applyToolLists`, `isNodeToolAccessRestricted`, `applyClaudeCodeToolAccess`, `applyPermissionMode`), `pipeline/handlers/backend_claudecode.go:258-268` (`buildArgs`), `pipeline/handlers/backend_claudecode_denials.go:75`, `pipeline/handlers/codergen.go:107` (ACP pre-start check)
- Test: `pipeline/handlers/backend_claudecode_readonly_test.go`

**Interfaces:**
- Consumes: Task 7 mode parsing (mirror it as `nodeToolAccessMode(node) agent.ToolAccessMode` in handlers), Task 4 `refuseToStartError{Kind: "read_only_acp"}`.

- [ ] **Step 1: Write failing tests** `TestClaudeCodeReadOnlyArgv` (exact argv slice per Global Constraints, in order), `TestClaudeCodeReadOnlyIgnoresParamsBypass` (`params: permission_mode: bypassPermissions`, `params: allowed_tools: Bash`, `mcp_servers: {...}` → argv unchanged), `TestClaudeCodeReadOnlyRefusalsExpected`, `TestClaudeCodeNoneUnchanged` (full canonical deny list as today), `TestACPReadOnlyRefusedBeforeStart` (routable, non-retryable, agent never spawned).
- [ ] **Step 2–4:** red → implement → green. **Step 5:** Commit `feat(claude-code): enforce tool_access: read-only with --tools and dontAsk; refuse ACP`.

### Task 9: doctor — per-pipeline backends and `dippin` severity

**Files:**
- Modify: `tracker_doctor.go:128,181` (parse the pipeline before `baseDoctorChecks`; pass needs), `tracker_doctor_providers.go:110` `checkProviders`, `tracker_doctor_binaries.go:17` `checkOtherBinaries`, `tracker_doctor_dippin.go:35` `checkDippin`
- Test: `tracker_doctor_backends_test.go`

**Interfaces:**
- Consumes: Task 6 `ComputeBackendNeeds`, Task 5 `resolveClaudePathVersion` (export a doctor-facing wrapper `handlers.ClaudeAvailable(ctx, workDir) (path, version string, ok bool)`).
- Produces: `checkProviders(ctx context.Context, probe bool, needs pipeline.BackendNeeds, claudeOK bool) CheckResult`; `checkOtherBinaries(ctx context.Context, needs pipeline.BackendNeeds) CheckResult`.

- [ ] **Step 1: Write failing tests** `TestDoctor_AllClaudeCodePipelineNeedsNoKey` (passes with `not needed by this pipeline (runs on Claude Code)`), `TestDoctor_AutoWithClaudeNeedsNoKey`, `TestDoctor_AutoWithoutClaudeChecksKeysWithHint`, `TestDoctor_MixedNativeUnchanged`, `TestDoctor_BackendFlagClaudeCodeNoPipeline`, `TestDoctor_Issue716Repro` (Joi's pipeline from #716), `TestDoctor_ClaudeRequiredWhenNodeNeedsIt`, `TestDoctor_DippinMissingIsWarning` (status warn, message `only needed to lint or edit .dip files`).
- [ ] **Step 2–4:** red → implement → green. **Step 5:** Commit `fix(doctor): check backends per pipeline (#716); dippin missing is a warning`.

### Task 10: PR 1 docs, gates, PR

**Files:** `docs/architecture/backends.md`, `docs/architecture/handlers/codergen.md`, `site/content/cli.html` (`backend: auto`, `tool_access: read-only`, doctor), `CLAUDE.md` (two gotchas), `CHANGELOG.md` `[Unreleased]` (Added: `backend: auto`, `tool_access: read-only`; Changed: doctor dippin severity; Fixed: #716).

- [ ] **Step 1:** Write the docs; state the minimum `claude` version that supports `--tools`, `dontAsk`, `--strict-mcp-config`, `--setting-sources` (check `claude --help` on the version tested).
- [ ] **Step 2:** `make ci` → all gates pass; `go test ./cmd/tracker-conformance -run TestGoldenTraces -count=1` ok.
- [ ] **Step 3:** Push, open the PR (`Closes #716`, references the spec and the Task 0 issues); address CodeRabbit/Codex; merge when green.

---

## PR 2 — prep (`refactor/build-product-staging-lib`)

### Task 11: Extract the commit staging rules

**Files:**
- Create: `examples/scripts/build_product/lib/stage.sh`, `examples/scripts/build_product/lib/stage_test.sh`
- Modify: `examples/scripts/build_product/CommitIfDirty.sh`, `examples/scripts/build_product/FinalCommit.sh` (source the lib, delete the duplicated blocks)

**Interfaces:**
- Produces (sh): `stage_build_excludes <tmpdir>` — writes `<tmpdir>/exclude` (user's global excludes + secret-name rules + private-key files + compiled-binary artifacts) and prints the skipped-secret warning; `stage_git() { git -c core.excludesFile="$STAGE_EXCLUDE" "$@"; }` after `STAGE_EXCLUDE` is set. Callers keep their own commit/amend logic.

- [ ] **Step 1:** Move the shared block verbatim into `stage.sh`; write `stage_test.sh` covering `.env` skipped, `.env.example` kept, private-key `.pem` skipped, public `.pem` kept, compiled binary skipped, script kept, global excludes honored, path with spaces.
- [ ] **Step 2:** `sh examples/scripts/build_product/lib/stage_test.sh` and the existing `CommitIfDirty_test.sh` / `FinalCommit_test.sh` → all pass unchanged (behavior-neutral).
- [ ] **Step 3:** `go test ./pipeline -run TestExampleScripts -count=1`, `make dippin-test`, `make doctor` → pass.
- [ ] **Step 4:** Commit `refactor(build_product): extract commit staging rules into lib/stage.sh (#395/#646)`; PR, review, merge.

---

## PR 3 — built-ins (`feat/explain-quickfix`)

### Task 12: `explain`

**Files:**
- Create: `examples/explain.dip`, `examples/prompts/explain/Answer.md` (the AskQuestion gate's prompt stays inline: dippin has no `prompt_file` for human nodes), `examples/scripts/explain/WriteAnswer.sh`, `examples/scripts/explain/WriteAnswer_test.sh`, `examples/explain.test.json`
- Modify: `tracker_workflows.go` (`go:embed` lines, catalog: starters first), `cmd/tracker/commands.go:173` `executeWorkflows` (`start here` tag), `cmd/tracker/embed_test.go:457` (pin the new sidecar dirs), `Makefile:184` (`make doctor` list)

**Interfaces:**
- Produces: `WorkflowInfo.StartHere bool`; catalog order: `explain`, `quickfix`, then the rest alphabetically.
- Graph (spec §5): `Start → NeedQuestion` (conditional: `graph.params.question != ask` → `Answer`, else `AskQuestion`) `→ Answer → WriteAnswer → Done`; `vars question: ask`; `defaults on_failure: AbortRun`; `AbortRun → Done`.

- [ ] **Step 1: Write failing tests** — `explain.test.json`: param given skips AskQuestion; no param visits AskQuestion; Answer failure reaches AbortRun (every scenario sets `graph.params.question`). `WriteAnswer_test.sh`: copies `$TRACKER_RUN_DIR/Answer/response.md` to `explain.md`, fails loudly when the response is missing, prints the `next:` line `tracker quickfix --param change="…"`. `TestWorkflowsStartHereFirst`.
- [ ] **Step 2:** `make dippin-test`, the fixture suite, `go test ./cmd/tracker -run 'TestWorkflows|TestEmbedded' -count=1` → FAIL.
- [ ] **Step 3:** Write the `.dip`, prompt (respect `.gitignore`, skip vendored trees, answer concisely), script, embed and catalog changes.
- [ ] **Step 4:** Run → PASS; `dippin doctor examples/explain.dip` (pinned) → grade A (95 until dippin knows `read-only`).
- [ ] **Step 5:** Commit `feat(workflows): explain — a read-only first-win built-in`.

### Task 13: `quickfix` scripts and libraries

**Files:**
- Create under `examples/scripts/quickfix/`: `Preflight.sh`, `RunTests.sh`, `ShowDiff.sh`, `CheckReviseBudget.sh`, `Commit.sh`, `Discard.sh`, each with `<Name>_test.sh`; `lib/gitignore.sh`, `lib/stage.sh`, `lib/counters.sh` (byte-identical copies of the `build_product/lib` originals), `lib/parity_test.sh` (copy of `ask_and_execute/lib/parity_test.sh`)

**Interfaces:**
- Consumes: Task 11 `stage.sh`; `build_product/lib/gitignore.sh` `exclude_tracker_metadata`; `counters.sh` `bump_counter`.
- Produces: Preflight writes `$TRACKER_RUN_DIR/quickfix.json` `{branch, base_sha, worktree}` and prints marker `preflight-ok`; RunTests prints final marker `tests-pass` | `tests-fail` | `no-tests` and always exits 0; CheckReviseBudget exits 0 below 3 revisions, 1 at exhaustion (counter at `.tracker/quickfix/revise_count`); Commit prints the spec's `committed to branch …` block; Discard removes worktree and branch.

- [ ] **Step 1: Write the failing fixture suites** (pipe-free helpers from `test_helpers.sh`):
  - Preflight: worktree + branch created from HEAD; a dirty user checkout is left untouched and noted; detached HEAD works; not a repo / no commits fail with clear text; stale worktree path or existing branch fails with exact cleanup commands and creates nothing (Review Focus 5); `.tracker/` is excluded via `git rev-parse --git-path info/exclude` (works from a linked worktree and with submodules — Review Focus 3); repo path with spaces (Review Focus 1).
  - RunTests: Makefile `test` target, `go.mod`, `package.json` test script, pytest, Cargo, none → right marker; a hanging test hits the timeout → `tests-fail` with a timeout line; exit code always 0.
  - CheckReviseBudget: 0, 1, 2 → exit 0; 3 → exit 1.
  - Commit: change committed on the branch only; user's branch and tree unchanged; `.tracker/` and secret-looking files not staged; subject `quickfix: change from tracker run <id>`, body from `summary.md`; hook failure exits 1; worktree removed, branch kept.
  - Discard: only the worktree and branch are removed; ignored files in the user checkout survive.
  - A diff larger than the limit is reported as truncated with the full-diff command (Review Focus 4): `ShowDiff.sh` prints `git diff <base>` of the worktree (untracked files via `git add -N`) and, past the limit, a `diff truncated — see: git diff <base>..tracker/quickfix/<id>` line.
- [ ] **Step 2:** `make test-scripts SUITE=Preflight_test.sh` (each suite) → FAIL.
- [ ] **Step 3:** Implement the scripts (each `cd`s into the worktree itself; all paths quoted; no user text interpolated).
- [ ] **Step 4:** All suites and `lib/parity_test.sh` → PASS; `make shell-check`.
- [ ] **Step 5:** Commit `feat(workflows): quickfix tool scripts with fixture suites`.

### Task 14: `quickfix.dip`, prompts, scenarios and engine tests

**Files:**
- Create: `examples/quickfix.dip`, `examples/prompts/quickfix/MakeChange.md`, `examples/quickfix.test.json`, `pipeline/handlers/quickfix_routing_test.go`
- Modify: `tracker_workflows.go` (embed), `cmd/tracker/embed_test.go`, `Makefile` (`make doctor`)

**Interfaces:**
- Graph (spec §5): `Start → Preflight → NeedChange` (conditional on `graph.params.change != ask`) `→ AskChange | MakeChange → RunTests → ShowDiff → ReviewDiff`; ReviewDiff labels in order `reject`, `revise`, `approve`, `default: reject`, `timeout` set; `reject → Discard`, `revise → AskRevision → CheckReviseBudget`, `approve → Commit`; CheckReviseBudget `success → MakeChange restart: true`, `fail → FinalDecision` (labels `reject`, `approve`, `default: reject`); every tool node has explicit success/fail edges; `defaults on_failure: AbortRun`, `max_restarts: 10`. Node names keep `AskRevision` lexically before `Commit` and `Discard`.

- [ ] **Step 1: Write failing tests** — `quickfix.test.json` (set `graph.params.change` in every scenario): param skip; approve; revise then approve; revise ×3 then FinalDecision; reject; preflight fail → AbortRun. `quickfix_routing_test.go` (real engine, scripted interviewer): `TestQuickfix_FreeTextRoutesToAskRevision`, `TestQuickfix_ReviseExhaustionReachesFinalDecision`, `TestQuickfix_RedTestsReachGate`, `TestQuickfix_AutoApproveRejects`.
- [ ] **Step 2:** Run → FAIL. **Step 3:** Write the `.dip` and `MakeChange.md` (smallest change; write `.tracker/quickfix/summary.md`; never run `git push`). **Step 4:** Run → PASS; `dippin doctor examples/quickfix.dip` → A (95). **Step 5:** Commit `feat(workflows): quickfix — one small change on an isolated branch`.

### Task 15: End-to-end and PR 3 docs

**Files:**
- Create: `cmd/tracker/first_win_e2e_test.go` (builds the binary; fake `claude` on `PATH` that answers NDJSON from fixtures)
- Modify: `cmd/tracker/summary.go:300-315` (when `isClaudeCodeOnly`, the cost line reads `~$X.XX usage (included in your Claude plan)`; test `TestSummaryClaudeCodeCostLabel`), `site/content/workflows.html` (cards; built-in count in current copy only, e.g. `:380`), `CHANGELOG.md` (Added: two built-ins)

- [ ] **Step 1: Write the failing e2e tests:** `explain --param question=…` writes `explain.md` and prints the `next:` line and `backend: auto → claude-code`; `quickfix --param change=… --auto-approve` ends with no branch and an untouched checkout; a library test with a scripted interviewer approves and finds `tracker/quickfix/<id>` with the change.
- [ ] **Step 2–4:** red → green; `make ci`.
- [ ] **Step 5:** Docs; push, PR (references spec and Task 0 issues); review; merge.

---

## PR 4 — quickstart (`docs/first-win-quickstart`)

### Task 16: README and website

**Files:** `README.md` Quick Start (the spec's first-run transcript; subscription path first; `doctor` as troubleshooting; `dippin` out of the quickstart line), `site/content/_index.html` (hero quickstart), `site/content/workflows.html` (ladder intro), `ROADMAP.md` (sub-project 1 shipped; next: Claude Code skill).

- [ ] **Step 1:** Write the copy; every command shown must run as written on the merged `main` (run each in a scratch repo with the fake-`claude` setup from Task 15).
- [ ] **Step 2:** `make docs-check`, internal link check (the `href` / Markdown link sweep used in the docs audit).
- [ ] **Step 3:** PR, review, merge; confirm the site deploys.
