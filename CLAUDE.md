# CLAUDE.md — Tracker Project Instructions

## Project Overview

Tracker is a pipeline orchestration engine for multi-agent LLM workflows.
Pipelines are defined in `.dip` files (Dippin language) and executed with
parallel agents via a TUI dashboard. Built by 2389.ai.

## Where to start

- System orientation, layer diagram, end-to-end sequence: [`ARCHITECTURE.md`](ARCHITECTURE.md)
- Subsystem deep dives: [`docs/architecture/README.md`](docs/architecture/README.md)
- Run loop, edge selection, escalation: `docs/architecture/engine.md`
- Handler catalog: `docs/architecture/handlers.md`
- `.dip` → Graph adapter: `docs/architecture/adapter.md`
- Context scoping and flow: `docs/architecture/context-flow.md`
- Backends (native / claude-code / acp): `docs/architecture/backends.md`
- Transport boundary (how TUI / Slack / web front-ends plug into the core): `docs/architecture/transport-boundary.md`
- Embedding Tracker as a library (supported seam for downstream products; golden-trace drift check): `docs/architecture/embedding.md`

## Code map

- **Library API**: top-level `tracker.go`, `tracker_*.go`. Exported entry points include `Run`, `Diagnose` / `DiagnoseMostRecent`, `Doctor`, `Audit`, `ListRuns`, `Simulate`, `ExportBundle`, `AnalyzeTestFidelity`, `DetectTestRaces`, `ResolveRunDir`, `ResolveBudgetLimits`, `ResolveProviderBaseURL`, `ResolveActivityLogPath`, `NewNDJSONWriter`, `DescribeInputs` / `ValidateInputs` (declared-input introspect/validate; `tracker_inputs.go`). Prefer these over shelling out to the CLI from embedded integrations.
- **CLI**: `cmd/tracker/` — `main.go` (entry, dispatches `__jail-exec` before flag parsing), `flags.go` (every flag), `run.go`, `resolve.go` (bare-name resolution), `doctor.go`, `diagnose.go`, `summary.go`.
- **Engine**: `pipeline/` — `engine.go`, `engine_edges.go` (edge selection), `graph.go` (shape → handler map), `handler.go` (Handler/Outcome contract), `node_config.go` (typed accessors), `dippin_adapter.go` (IR → Graph), `context.go`, `condition.go`, `checkpoint.go`, `budget.go`, `audit_path.go`.
- **Handlers**: `pipeline/handlers/` — one file per handler. **`registry.go` `NewDefaultRegistry` is the wire-up point** — add new handlers there and map the shape in `pipeline/graph.go`.
- **Agent**: `agent/` — `Session` turn loop, tool registry, context compaction. `agent/exec/` holds the Landlock jail (Linux).
- **LLM**: `llm/` — `Client`, middleware, token tracker; per-provider adapters under `llm/anthropic/`, `llm/openai/`, `llm/google/`, `llm/openaicompat/`.
- **TUI**: `tui/` — Bubble Tea dashboard, modal/review/interview content types.
- **Built-in workflows**: `examples/` (embedded into the binary). The four embedded built-ins resolve their `prompt_file` / `command_file` sidecars via `pipeline.ResolveFileDirectivesFS` over the embed FS (`tracker.EmbeddedWorkflowFS()`) — a stopgap mirror of dippin's disk resolver until dippin-lang#304 ships an `fs.FS` variant (signature stays, body becomes a wrapper); the parity test in `pipeline/dippin_resolve_fs_test.go` pins it. Giving an embedded built-in sidecars requires adding its `examples/prompts/<name>` / `examples/scripts/<name>` dirs to the `go:embed` list in `tracker_workflows.go`, or the embedded run cannot find them. **Library callers must anchor a source** (`tracker.SourceRef`): `Config.Source` / `WithSource` / `WithValidateSource` with `Path` (on-disk file — sidecars resolve next to it, from any cwd) or `Builtin` (embed FS); `ResolveSource` returns the right one via `WorkflowInfo.Ref()`. An un-anchored source falls back to "byte-identical to a built-in → embed FS, else cwd", which is wrong for a `tracker init` copy with edited sidecars — always pass the ref (chatops and the CLI do).
- **Other binaries**: `cmd/tracker-conformance/` (release-shipped conformance harness), `cmd/tracker-swebench/` (SWE-bench runner with `agent-runner` subcommand), `cmd/trackerbot/` (Slack front-end; a pure consumer of the transport boundary — `Config.Interviewer`, the event stream, `RunManager`; see its `README.md`).

## Critical Rules

### NEVER use --no-verify
- `git commit --no-verify` and `git push --no-verify` are **forbidden** under all circumstances
- If pre-commit hooks fail, fix the root cause — do not bypass the hooks
- This applies even for merge commits, "pre-existing" issues, or any other justification
- The hooks (coverage, complexity, tests, lint, format) are safety gates — skipping them defeats their purpose

### Never silently swallow errors
- Provider errors (auth, model not found) must hard-fail the pipeline, not retry
- EXCEPTION — billing/quota **exhaustion** (`credit balance too low`, `insufficient_quota`) is a *recoverable* condition (#487): it stops in a resumable `OutcomePausedBilling` terminal (checkpoint + preserved WIP + `tracker -r` resume), not a fatal `OutcomeFail`. Still non-retrying (retry re-hits the empty balance). The handler signals this via `pipeline.NewPauseError(OutcomePausedBilling, err)`; detection is `llm.IsBillingError` (which returns false for a *retryable* 429 rate limit, even one whose message says "quota exceeded"). Rate limits remain retryable via the retry middleware, then hard-fail.
- Empty agent responses (0 tokens, 0 tool calls) are failures, not successes
- SSE stream errors (`error`, `response.failed` events) must be parsed and surfaced
- Condition evaluation on unresolved variables must warn, not silently return empty string

### NEVER `go install` dippin-lang
- `go install github.com/2389-research/dippin-lang/cmd/dippin@...` overwrites the `dippin` binary on `PATH` (in `$GOBIN` or `$GOPATH/bin`) with a module-cache build, displacing the user's locally-built `dippin` from their development checkout. They want to keep using their local build.
- Update the Go module dependency with `go get github.com/2389-research/dippin-lang@vX.Y.Z` only
- If `dippin` is not on `PATH` for a verification step, ask the user — don't `go install`

### Tool node safety — LLM output as shell input
- NEVER `eval` content extracted from LLM-written files (arbitrary command execution).
- `tool_command` variable expansion has a safe-key allowlist for `ctx.*`: only `outcome`, `preferred_label`, `human_response`, `interview_answers`, `branch_id` interpolate. `branch_id` (#420) is engine-set to the parallel branch's target node ID (author-controlled, never LLM output) so a branch's tool node can namespace its on-disk per-loop counters by branch. All `graph.*` and `params.*` (author-controlled) are allowed. All LLM-origin `ctx.*` keys (`last_response`, `tool_stdout`, `tool_marker`, `tool_route`, `response.*`) are blocked. `manager_loop` steer values are namespaced under `steer.*` (#177) and never on the allowlist.
- Safe pattern: write LLM output to a file in a prior tool node, then read it (`cat .ai/output.json | jq ...`).
- Tool stdout/stderr capped at 64KB per stream (per-node `output_limit`, hard ceiling 10MB via `--max-output-limit`).
- Built-in denylist blocks `eval`, pipe-to-shell, `curl|sh`. Override with `--bypass-denylist` (avoid). Optional `--tool-allowlist` / `tool_commands_allow` narrows further but cannot override the denylist.
- Sensitive env vars (`*_API_KEY`, `*_SECRET`, `*_TOKEN`, `*_PASSWORD`) are stripped from tool subprocesses; override with `TRACKER_PASS_ENV=1`.
- Strip comments (`grep -v '^#'`) and blank lines from LLM-generated lists. Use flexible regex for markdown headers (LLMs vary `##` / `###` / colon use). Add empty-file guards after extracting from LLM-written files — fail loudly.

### Activity log integrity (#213)
- Live audit log path is computed by `pipeline.SecureActivityLogPath(runID)`; reads go through `tracker.ResolveActivityLogPath`. Resolution order (each step yields `<base>/<runID>/activity.jsonl`): `$TRACKER_AUDIT_DIR/<runID>/` → `$XDG_STATE_HOME/tracker/runs/<runID>/` → on Windows, `%LOCALAPPDATA%\tracker\runs\<runID>\` → `$HOME/.local/state/tracker/runs/<runID>/` → `os.TempDir()/tracker-audit/<runID>/` (last-resort when `$HOME` is unresolvable). File mode `0o600`, opened `O_NOFOLLOW`.
- Every runtime-written line is prefixed with sentinel `\x1f\x1e` (`pipeline.ActivityLogSentinel`). Lines lacking it count as `runtimeAnomalies.InjectedLines` and fire `SuggestionAuditLogInjection`. The sentinel is detection, not authentication.
- `TRACKER_AUDIT_DIR` and `XDG_STATE_HOME` MUST be absolute paths — relative values are silently ignored (`pipeline.absEnv`) so CWD can't re-anchor the secure log.
- RunIDs are validated by `pipeline.validateRunID` (rejects separators, `..`, `.`) so a tampered checkpoint can't escape the base.
- A sentinel-stripped snapshot is written to the legacy `<workDir>/.tracker/runs/<runID>/activity.jsonl` on close (best-effort, for `--export-bundle` / git_artifacts).
- Full threat model and residual risks: see [`docs/architecture/`](docs/architecture/) and the section in Architecture Gotchas below.

### Dippin-lang compatibility
- The dippin IR uses `ctx.` namespace prefix in conditions (`ctx.outcome = success`)
- Tracker's context stores bare keys (`outcome`). The condition evaluator strips `ctx.`, `context.`, and handles `internal.*`
- Edge / manager_loop conditions are serialized from `Condition.Parsed` (#647) — dippin's AST is authoritative; `SerializeDippinCondition` lowers it to tracker's flat `||`-of-`&&` dialect (bounded DNF, negation pushed to leaf operators). `Raw` is only the fallback when dippin's parser rejects the text. dippin spells conjunctions as `and` / `or` / `not` (never `&&` / `||`), and tracker's own parser accepts the word forms as quote-aware, whitespace-bounded synonyms so hand-built graphs aren't silently wrong either.
- The adapter must synthesize implicit edges from `ParallelConfig.Targets` and `FanInConfig.Sources`
- `AgentConfig.ResponseFormat` and `AgentConfig.ResponseSchema` map to node attrs `response_format` and `response_schema`
- `AgentConfig.Params` is a generic pass-through map — typed fields take precedence over Params keys
- The adapter maps IR field names to tracker convention: `model` → `llm_model`, `provider` → `llm_provider`
- Provider name is `gemini` not `google`
- Provider base URL resolution goes through `tracker.ResolveProviderBaseURL(provider)`. Precedence: `<PROVIDER>_BASE_URL` env var wins; otherwise `TRACKER_GATEWAY_URL` is used with a per-kind suffix; otherwise empty (SDK default).
- Gateway kind dispatch via `TRACKER_GATEWAY_KIND` / `--gateway-kind` (default `cf-aig`). For `cf-aig` the suffixes are `/anthropic`, `/openai`, `/google-ai-studio`, `/compat`. For `bedrock` Anthropic gets `""` (SDK appends `/v1/messages`), OpenAI/Gemini get `/v1`, and `openai-compat` refuses to route. The strict path (`ResolveProviderBaseURLStrict`, used by adapter constructors) surfaces refusals — both unknown kinds and refused (kind, provider) pairs — as `ErrGatewayRouteRefused`; the lax `ResolveProviderBaseURL` returns `""` (back-compat — SDK default endpoint).
- Variable expansion is single-pass — never re-scan resolved values
- Section-level `else -> Node` (`ir.Workflow.ElseTarget`, #649) is stored as `Graph.ElseTarget`, NOT synthesized as an edge. `Engine.selectByElse` (`engine_edges.go`) routes to it only when the node has ≥1 outgoing edge, none unconditional, every guard/label/suggested match failed, and `ctx.outcome != fail` — mirroring dippin's `simulate.resolveConditionalNext` and its **success-side-only** contract (a genuine failure never funnels to `else`; it halts with `no matching edges` as before). Emits `decision_edge` + `conditional_fallthrough` with `edge_priority: else`. Edge-less nodes are never covered; parallel branch targets are never routed by else at run time (they execute inside `ParallelHandler`, not the run loop — in dippin it is the implicit unconditional fan-in edge that keeps `else` out). All engine graph walks (`clearDownstream`, `downstreamNodes`, #643 dominance) go through the else-aware `successorIDs`/`predecessorIDs` in `graph.go`, so an else-only target is cleared by a restart and its re-entry is not a spurious `loop_restart`.
- `ensureStartExitNodes` only assigns passthrough start/exit handlers to bare codergen (Agent) nodes with no `prompt` attribute. All other resolved handlers (`tool`, `wait.human`, `parallel`, `parallel.fan_in`, `conditional`, `subgraph`, `stack.manager_loop`, etc.) are preserved. The detection is based on `n.Handler`, not on enumerating handler-specific attributes, so future handler types are automatically covered.

### Parallel execution
- The parallel handler dispatches branches from `parallel_targets` attr, NOT from outgoing graph edges
- After dispatch, the handler sets `suggested_next_nodes` pointing to the fan-in join node
- Branch goroutines must have defer/recover for panic safety
- The parallel handler emits EventStageStarted/Completed per branch for TUI visibility

### Edge routing — no unconditional fallbacks to loop targets
- NEVER add an unconditional edge to the same target as a conditional edge (causes infinite loops)
- Safe fallbacks go to an escalation gate or Done, not to FixX or the same gate
- In `build_product.dip`, use `EscalateMilestone` for mid-build failures and `EscalateReview` for post-build failures
- Conditions `outcome=success` + `outcome=fail` are exhaustive — no fallback needed on those

### Human gate UX
- Freeform gates with labeled edges use the hybrid radio+freeform modal (HybridContent), NOT plain FreeformContent
- Labeled gates with long context (>200 chars or >5 lines after `---`) use ReviewHybridContent — fullscreen glamour viewport + radio labels + freeform "other" option
- Long prompts (20+ lines) without labels use the split-pane ReviewContent with glamour-rendered viewport
- Both HybridContent and ReviewHybridContent have an "other" option with a textarea for custom freeform input
- The full prompt (label + context) must go through glamour — never render markdown with plain lipgloss
- All modal content types must implement Cancellable — Ctrl+C calls Cancel() to close reply channels and prevent goroutine hangs
- Never block a pipeline handler goroutine on a channel send/receive without a cancellation path

### Yes/No mode
- `mode: yes_no` on human nodes presents a fixed "Yes"/"No" choice
- Yes maps to `OutcomeSuccess`, No maps to `OutcomeFail`
- Pipelines route with `ctx.outcome = success` / `ctx.outcome = fail` conditions
- This is distinct from default choice mode, where outcome is always `success` and routing uses `PreferredLabel`
- `AutoApproveInterviewer` picks "Yes" (first choice) by default — forward progress semantics

### Interview mode
- `mode: interview` on human nodes enables structured multi-field form collection
- Upstream agent outputs JSON questions: `{"questions": [{"text": "...", "context": "...", "options": [...]}]}`
- Use `response_format: json_object` on question-generating agents to force JSON output at the API level
- `ParseStructuredQuestions` validates JSON first; falls back to `ParseQuestions` markdown heuristic parsing
- Questions with "other" variants in options are filtered — the UI always provides its own "Other" escape hatch
- One question shown at a time with progress bar, answered summary above, and selection feedback (filled dot + checkmark)
- Canceled interviews return `OutcomeFail`, not `OutcomeSuccess` — pipeline edges can route on cancellation
- `questions_key` defaults to `interview_questions` (with `last_response` as a read-time fallback inside `resolveAgentOutput`); `answers_key` defaults to `interview_answers`
- Zero parsed questions falls back to freeform with the node's `prompt` attribute
- Enter confirms selection and advances; Ctrl+S submits all; Esc cancels
- Empty API responses (0 content parts, 0 output tokens, 0 prior tool calls) trigger session-level retry with diagnostic logging

### Error surfacing
- Node failures (MsgNodeFailed) and retries (MsgNodeRetrying) must be shown inline in the activity log, not just in the sidebar icon
- Tool node stderr/stdout must be visible to the user — the `tracker diagnose` command reads status.json and activity.jsonl for this
- The "no providers configured" error must include actionable setup instructions, not just the raw error message

### TUI stability
- The activity log is append-only with line-level styling — no glamour markdown rendering
- Each line is styled once on newline and never re-rendered
- The activity indicator line is always reserved (space when idle) to prevent viewport shift
- Per-node streams for parallel execution with separators on node change
- Count actual terminal rows (not entries) when budgeting the viewport

## Versioning and Releases

### Changelog
- Keep CHANGELOG.md updated with every feature, fix, and breaking change
- Use [Keep a Changelog](https://keepachangelog.com/) format
- Group entries under Added, Changed, Fixed, Removed
- Update the changelog in the same commit/branch as the code change, not after

### Releases
- Tag releases on GitHub with semantic versioning (vMAJOR.MINOR.PATCH)
- Create GitHub releases with release notes derived from CHANGELOG.md
- Tag after a coherent batch of work, not after every commit
- Breaking changes bump MAJOR, new features bump MINOR, fixes bump PATCH
- **Merging the release branch is NOT the release.** That merge only ships CHANGELOG/README doc updates. The actual release is `git tag -a vX.Y.Z <merge-commit> -m "release: vX.Y.Z"` + `git push origin vX.Y.Z`. The tag push triggers `.github/workflows/release.yml` → GoReleaser (builds darwin/linux amd64/arm64 binaries for `tracker` and `tracker-conformance`, publishes the GitHub release). A release isn't done until `gh release view vX.Y.Z` shows the published entry with assets. v0.19.0 and v0.20.0 were back-tagged retroactively because of this; don't repeat.

### Version bumps
- Update go.mod module version on MAJOR bumps
- Keep dippin-lang dependency pinned to a tagged version, not a commit hash
- After updating dippin-lang, run `dippin doctor` on all example pipelines and verify scores

## Development Workflow

### NEVER open pull requests
- **Do not create GitHub PRs.** Not for features, not for fixes, not for releases.
  Review the work *in the conversation* — show the diff and the reasoning — then
  merge it yourself.
- Flow: branch → implement → verify (see *Before committing*) → present the
  review here → `git checkout main && git merge <branch>` → `git push`.
- This applies to subagents too: they push their branch and report; they do not
  run `gh pr create`. The reviewing and merging happen here.
- Corollary: nothing waits on GitHub CI or a bot reviewer to merge. The local
  gates (pre-commit hook + `make complexity` + the *Before committing* list) are
  the gate. CI on `main` is a backstop, not a merge blocker.

### Before committing
- `go build ./...` — must pass
- `go test ./... -short` — all packages must pass
- Example workflow scripts have shell fixture suites beside them (`examples/scripts/<workflow>/<Name>_test.sh`, `examples/subgraphs/scripts/**/*_test.sh`); they run under `go test ./...` (`pipeline/example_scripts_test.go`) and directly via `make test-scripts`. A script under test is run with `sh` (dippin uses `sh -c`, so the shebang is ignored — #324). Add a `<Name>_test.sh` when adding a `command_file:` sidecar. `build_product`'s shared shell (gitignore/exclude helpers, the `.ai/build/verify.sh` + `ci-probe.sh` + rubric bodies Setup installs, the guarded attempt counter, build-context seeding) lives in `examples/scripts/build_product/lib/` and is sourced at runtime via `LIB="${graph.workflow_dir}/scripts/build_product/lib"` (fail-loud if empty); the suites source `test_helpers.sh` (`stage_script` mirrors that expansion) and the `lib/*_test.sh` suites are picked up by the same glob (`scripts/*/lib/*_test.sh`). The seven dotpowers-family workflows (`dotpowers`, `-auto`, `-simple`, `-simple-auto`, `test-kitchen`, `scenario-testing`, `kitchen-sink`; disk loads, not embedded) likewise source one implementation from `examples/scripts/dotpowers/lib/` (`tasks.sh`, `counters.sh`, `validate.sh`) through thin per-workflow wrappers, and `pipeline/example_scripts_drift_test.go` fails if any wrapper set (or the megaplan / ralph-loop copies) drifts from byte-identical — fix once, copy to every dir (#646).
- `dippin doctor examples/ask_and_execute.dip examples/build_product.dip examples/build_product_with_superspec.dip` — must be A grade
- If `dippin` is not on `PATH`, ask the user — they install it from a local dippin-lang checkout. Do not `go install` it (see Critical Rules).
- The Landlock jail suite only executes on a Linux kernel WITH Landlock — every enforce-path jail test skips on macOS **and on the Blacksmith CI runner** (no Landlock). Before merging a change under `agent/exec/jail*.go` / `pipeline/handlers/codergen_jail.go`, run branch CI (`gh workflow run ci.yml --ref <branch>`) and confirm the `jail-linux` job (ubuntu-24.04) is green — it greps for the literal PASS lines.
- `make complexity` — the complexity ratchet must stay green. It grandfathers a baseline that may only shrink (see `scripts/complexity/README.md`); a NEW or WORSE cyclo/cognitive/file-size violation fails it. Burn down with `make complexity-update`.
- `make docs-check` — the doc-drift gate (`scripts/docs/gate.sh cli-coverage`): every `commandMode` in `cmd/tracker/main.go` must be documented in `site/content/cli.html`. Adding a `tracker <cmd>` without a website entry fails it. Enforced in the pre-commit hook AND CI; add the `cli.html` block in the same change that adds the command. (Mechanical structural check only — it does NOT verify prose completeness in `architecture.html`/`engine.md`; that stays a review responsibility, e.g. the release-prep doc-audit sweep.)

### Before releasing
- Run `dippin doctor` on ALL example .dip files — aim for A grade across the board
- Run `dippin simulate -all-paths` on the three core pipelines
- Run `go test ./cmd/tracker-conformance -run TestGoldenTraces` clean — the golden-trace fixtures ship in lockstep with the tag; if an intentional engine change altered them, regenerate with `-update-golden` and commit so downstream ports pin accurate snapshots (see `docs/architecture/embedding.md` §5)
- Update CHANGELOG.md and README.md
- Update ROADMAP.md — close finished milestones, promote the next workstream up a tier (see the Maintenance contract in `ROADMAP.md`)
- After the release doc updates are merged to `main`, tag that merge commit and push:
  - `git tag -a vX.Y.Z <merge-commit-sha> -m "release: vX.Y.Z"`
  - `git push origin vX.Y.Z`
  - The tag push triggers `.github/workflows/release.yml` → GoReleaser, which builds binaries and creates the GitHub release entry. The merge alone does not create a release (see Versioning and Releases § Releases).
- Update the website changelog (`site/content/changelog.html`) with the new version's entry, and `roadmap.html` if the roadmap moved. **This is enforced at tag time:** `release.yml` runs `scripts/docs/gate.sh release-version <tag>` and *fails the release* if the version isn't in both `CHANGELOG.md` and `site/content/changelog.html` — so a stale-website release cannot publish.
- Refresh the website (`gh-pages` branch — see Project Infrastructure § Website).

### dippin-lang updates
- DO run `go get github.com/2389-research/dippin-lang@vX.Y.Z` to update the Go module dependency. The "never `go install`" rule lives in Critical Rules.
- After updating, verify: `go build ./... && go test ./... -short`

### Process patterns for security PRs
For PRs that touch a security boundary (`agent/exec/jail*.go`, `agent/exec/env.go`, `pipeline/handlers/codergen_jail.go`, the `cmd/tracker` `__jail-exec` dispatch, new `agent/tools/` filesystem/subprocess code, the tool denylist/allowlist, or the activity-log integrity path), follow the **"freeze and prove"** pattern: spec-first threat model → freeze the public API before implementing → prove the contract with invariant/property tests → audit-class sweep against `docs/architecture/agent-tool-jail-checklist.md` → keep the implementation a small patch against the frozen contract. Full guidance (and an honest current-vs-proposed breakdown): [`docs/architecture/security-pr-process.md`](docs/architecture/security-pr-process.md). Not a blanket rule — reserve it for high-blast-radius changes like #272/#275, not one-line jail tweaks.

## Architecture Gotchas

### `${graph.workflow_dir}` for embedded built-ins
Contract: `${graph.workflow_dir}/<relpath>` resolves a workflow-relative file; the concrete path is implementation-defined and workflow authors must not rely on it. For a disk load it is the source `.dip`'s directory (`pipeline.SeedWorkflowDir`, #332 — seeded by both the CLI loader and the library's `SourceRef{Path}` path). For an embedded built-in (bare name `tracker build_product`, or `SourceRef{Builtin}`) the loader has no directory, so the graph is marked `workflow_builtin = <name>` at load and `NewEngineFromGraph` — after `resolveWorkDir`, before `bindInputs` — materializes the built-in's embedded tree (`<name>.dip`, all of `prompts/<name>/` and `scripts/<name>/`, plus every directive-referenced sidecar) into the workdir via `pipeline.MaterializeBuiltinWorkflowDir` and sets `workflow_dir` to that absolute path. The whole tree is copied, not just directive-referenced files, so a sourced `lib/*.sh` helper is reachable. It is overwritten on every engine construction (fresh run and resume — the content is the running binary's, never stale); an author-declared `workflow_dir` wins; files are 0644 (sourced/`sh`'d, never exec'd), symlinked destinations are refused. The copy lives under `.tracker/` so the artifact-repo exclude keeps it out of commits/bundles. Customizing a built-in is `tracker init <name>` (a disk load), not editing the copy. Read-only entry points (`validate`, `simulate`, `doctor`, `DescribeInputs`) never materialize. The `writable_paths` jail only bounds writes (Landlock `RODirs("/")`), so a jailed tool node can still read the copy. **Packed `.dipx` is deliberately NOT covered**: its sidecars would come from an unverified sibling directory, a supply-chain regression for a SHA-verified bundle (#467), so `guardPackedWorkflowDir` keeps failing loud (#430). Built-ins are exempt from that objection because their sidecars are `go:embed`ded — the binary's own content.

### The adapter is the bridge
`pipeline/dippin_adapter.go` converts dippin IR to tracker's Graph model. Every naming mismatch between dippin and tracker conventions lives here — new IR fields land here first.

### Typed node-config accessors
Reads go through typed accessors on `*pipeline.Node`: `AgentConfig(graphAttrs)`, `ToolConfig()`, `HumanConfig()`, `ParallelConfig()`, `RetryConfig(graphAttrs)`. Defined in `pipeline/node_config.go`. When adding a new node attribute, extend the appropriate `NodeConfig` struct and its accessor — don't add fresh `node.Attrs[...]` reads. A handful of strict-parse helpers retain raw reads (see inline comments).

### `auto_status` STATUS contract
`auto_status: true` derives an agent node's outcome from a `STATUS:` line in its response (`parseAutoStatus` in `pipeline/handlers/codergen_autostatus.go`). The grammar is tolerant (#645): heading/emphasis/inline-code markers around `STATUS:` are skipped and anything after the `success|fail|retry` value (punctuation, prose, counts, emoji) is ignored, an unclosed trailing code fence is not a fence, and it is **last-line-wins** — gate-like prompts (SpecLint, ForgeSpec, VerifyMilestone, SynthesizeReviews, Decompose) emit `STATUS:fail` first and override with a final `STATUS:success`, so a truncated or verdict-less response fails closed. A missing verdict is `EventAutoStatusMissing`; only `goal_gate` nodes fail closed on it, plain nodes keep the legacy success default. Full grammar: `docs/architecture/handlers.md`.

### Structured output (`response_format`)
`response_format: json_object` on agent nodes forces JSON output at the LLM API level. Path: `.dip` → `AgentConfig.ResponseFormat` → `node.Attrs["response_format"]` → `codergen.buildConfig` copies it onto `SessionConfig.ResponseFormat` → `session.buildResponseFormat()` → `llm.Request.ResponseFormat` → provider translator (Anthropic: system instruction via `appendResponseFormatInstruction`, OpenAI: native `json_object`, Gemini: `responseMimeType`). Use on any agent that must produce structured JSON.

### The engine doesn't know about parallel/fan-in
Engine treats every node uniformly (execute handler, select edge, advance). The parallel handler dispatches branches internally and hints the next node via `suggested_next_nodes` — no special-case engine code.

### Git artifacts and bundle export
`WithGitArtifacts(true)` makes the artifact run dir a git repo and commits after every terminal node. `ExportBundle(runDir, outPath)` (`tracker_bundle.go`) wraps `git bundle create --all` for a portable history. `Result.ArtifactRunDir` is the canonical run-dir locator. `--export-bundle` calls it post-run; failures warn, don't fail. Canonical hand-off pattern for a remote worker → `git clone <bundle>` on the user's machine.

### Checkpoint resume is fragile
Checkpoints store completed nodes, per-node edge selections, and context snapshots. **Restart budget is per resolved loop target AND per iteration of the enclosing loop** (#603, #643): `Checkpoint.RestartCounts[target]` keys the `max_restarts` ceiling by the resolved restart target, so a fix loop on milestone 1 no longer consumes restart budget milestone 10 needs. The engine derives each loop header's natural loop from the graph (`pipeline/engine_restart_scope.go`: dominator analysis, back edge = edge `u -> h` where `h` dominates `u`, loop body = nodes reaching a back-edge source without passing `h`); every restart of a header resets the counts of the targets nested inside its loop (`restart_budget_reset` event), so `TestMilestone`'s budget is fresh each time `MarkMilestoneDone -> PickNextMilestone` advances. A back-edge traversal into a header is ALWAYS a counted restart, even when an inner restart's `clearDownstream` already wiped the header's completed flag. **The outermost loop has no enclosing header, so its `max_restarts` is the run-wide bound the author must size** (`build_product.dip` sets 200 = the cap on milestones). Irreducible re-entries (target does not dominate the source) are never *counted as back edges* (pre-#643 completed-node semantics), but their counts are still reset when an enclosing header restarts if the target sits inside that header's natural loop (e.g. `CheckMilestoneOutputs` inside `PickNextMilestone`'s) — no budget can reset without bound because every reset is driven by a counted, budgeted restart. The same boundary re-arms the nested targets' one-shot fallback latch (`Checkpoint.ClearFallbackTaken`), so a fallback that fired in milestone 1 can fire again in milestone 3. `Checkpoint.RestartCount` is retained as the run-wide aggregate for manifests/event payloads and is never reset; a legacy pre-#603 checkpoint carries only the scalar (unattributable to a target) so per-target budgets start fresh on resume — a conservative reset. The per-milestone on-disk circuit breakers (e.g., a `fix_attempts` file as in `build_product.dip`) are now belt-and-suspenders rather than the sole isolation mechanism. For **parallel** milestones those on-disk breakers must still be branch-namespaced or concurrent fix-loops clobber one shared path: the parallel handler seeds `ctx.branch_id` (the branch target node ID) into each branch's isolated context, so a branch's tool node can key its counter as `.ai/milestones/${ctx.branch_id}/fix_attempts` (#420, safe-key allowlisted for tool_command interpolation). A branch that fans out to a subgraph also gets its own restart budget for free — each subgraph runs a child engine with a separate checkpoint/`RestartCounts`. **Resume rewind (#651):** the engine records fail-routing provenance on the TARGET's `GateState` (#650's `FallbackOrigin` node field plus `FallbackOriginOutcome/Reason/Kind` — one persisted representation; `RecordFallbackOrigin`/`GetFallbackOrigin`) — set by a `ctx.outcome = fail` edge (kind `fail_edge`, hidden from #650's `FallbackOrigin(node)` copy accessor because an authored edge is not a fallback), `on_failure`/`fallback_target`, an exhausted retry's fallback, or a goal-gate redirect; an ordinary advance clears it before a fail-edge hop re-records, so a shared escalation node keeps the LATEST origin; a self-route is ignored and the dead-stop location (`Checkpoint.HaltedAt`, stamped by `recordHalt` on the terminal-halt paths, which now save). On `tracker -r`, `Engine.resumeEntryNode` (`pipeline/engine_resume.go`) un-completes the halted node (so an exact resume re-executes it rather than skipping through its edge to a bogus success) and, if the halted node has a `FallbackOrigin`, **rewinds** to the origin — `rewindTo` un-completes origin + downstream + the terminal, resets their retry counts and re-arms their `FallbackTaken` latches, leaves `RestartCounts` alone, emits `resume_rewound`, and saves. Skipped with a warning for a `wait.human` / `parallel` / `subgraph` / `stack.manager_loop` origin. `--resume-no-rewind` (`Config.ResumeExact`) is the opt-out; `--from <node>` (`Config.ResumeFrom`) is the explicit form — validated in `initRunState` (must exist and be completed or current) before `pipeline_started`. Legacy checkpoints without either field resume exactly as before.

### Token usage flow
`llm.Usage` (per API call) → `agent.SessionResult.Usage` (per session) → `pipeline.SessionStats` (per trace entry, built in `pipeline/handlers/transcript.go`) → `EngineResult.Usage` (aggregated by `Trace.AggregateUsage`). The parallel handler folds branch stats into its own outcome. `cmd/tracker/summary.go` uses `llm.TokenTracker` (middleware-level, per-provider) and `EngineResult.Usage` (trace-level) as independent sources.

### Pipeline context isolation
Per-node scoping (`node.<nodeID>.<key>`) is a stable feature — after each node finishes its dirty writes are aliased under `node.<nodeID>.<key>` so later nodes can reference a specific upstream node's output. Declarative `writes:` extracts a typed JSON payload into first-class keys. Full model: [`docs/architecture/context-flow.md`](docs/architecture/context-flow.md).

### Declared inputs (#553; requires dippin ≥ v0.51)
A workflow's dippin `inputs` block → `Graph.Inputs []pipeline.InputSpec` (adapter `inputsFromIR`). Library seam: `tracker.DescribeInputs` (introspect, no run), `tracker.ValidateInputs` (structured `[]pipeline.InputError`, standalone), `Config.Inputs []tracker.Input` (`StringInput`/`FileInput`/`FileInputBytes`/`SecretInput`). `bindInputs` validates + stages at run start (in `NewEngineFromGraph`, after `resolveWorkDir`) and **fails closed** (`*InputValidationError`) before any node runs on a missing-required (empty/whitespace counts) or constraint violation. Values seed a dedicated **closed `${inputs.<name>}` namespace** that is **untrusted by construction** — never on the tool_command safe-key allowlist (`inputContextPrefix` in `pipeline/expand.go`; also in `ambientVarPrefixes` so the validator doesn't flag it; dippin lints `${inputs.*}` in a `command:` as DIP157). `file` AND `secret` inputs are staged to `<workDir>/.tracker/inputs/<name>` (fixed path from the declared name; `pipeline.StageInputFile`, 0600 + O_NOFOLLOW, 10 MiB cap) so a workflow's shell reads the staged path directly — never `${inputs.spec}`. `build_product` / `build_product_with_superspec` declare `spec: file` and adopt the staged file as `SPEC.md`. A `secret` input's VALUE is staged to the 0600 file and `${inputs.<name>}` is only the PATH (#555), so the secret never enters a prompt/provider-wire/trace/checkpoint — read it from the staged path in a tool (`API_KEY=$(cat "$path")`). `.tracker/` is git-excluded from artifact repos so staged secrets never reach a commit/bundle. Residual: the 0600 file on same-UID local disk, and the value on the provider wire when the agent uses it. **Subgraph call-site binding (#556, requires dippin ≥ v0.58's DIP160 arity lint):** `SubgraphHandler` validates the parent's `subgraph_params` against the child graph's declared value-kind inputs (`bindSubgraphInputs` in `pipeline/subgraph.go`) and seeds the child's `inputs.*` namespace — a subgraph drives a child's inputs the same way a top-level run does, failing closed on a missing-required/invalid value. `file`/`secret` inputs are NOT bindable from a subgraph call site (a params string can't be staged) and are filtered out — the child resolves them itself. Design + phases: [`docs/superpowers/specs/2026-08-06-issue-553-pipeline-inputs-design.md`](docs/superpowers/specs/2026-08-06-issue-553-pipeline-inputs-design.md).

### Cost governance
- **Base model prices come from `dippin-lang/pricing` (#558), NOT a tracker table.** `llm.EstimateCost` resolves the model via `pricing.Lookup` (ID/alias + version fold) and calls `pricing.Cost`; tracker retired its own `InputCostPerM`/`OutputCostPerM` catalog fields. tracker still owns per-model **cache** multipliers as an overlay (`overlayCacheMultipliers` in `llm/pricing.go`) until dippin's `prices.json` carries cache rates, then dippin wins. Reasoning is not double-counted (`llm.Usage.OutputTokens` already includes it, so the mapping passes `Reasoning: 0`). A model dippin doesn't price → $0 + one-time warning (never a hard fail); `TestCatalogModelsArePricedByDippin` guards against a catalog model silently dropping out of pricing. New/repriced models are adopted by bumping the dippin pin. **Dated snapshot fold (#639):** dippin's `Lookup` does not strip a trailing dated-snapshot suffix (dippin#301), so `llm/pricing.go` routes every lookup through `lookupModel` / `lookupProviderModel`, which retry once with a trailing `-YYYYMMDD` or `-YYYY-MM-DD` stripped (`stripDateSuffix`). Exact match is always tried first, so a genuinely dated catalog key wins over its family; a dated id whose family is also unknown stays `(0, false)`, and the unknown-model warning names the original string.
- `UsageSummary.ProviderTotals` carries tokens+cost; `tracker.Result.Cost` exposes dollar cost via `llm.TokenTracker.CostByProvider`.
- `pipeline.BudgetGuard` is evaluated between nodes after every `emitCostUpdate`. Breach → `OutcomeBudgetExceeded`, `EngineResult.BudgetLimitsHit`, `EventBudgetExceeded`.
- Configure via `Config.Budget`, `--max-tokens` / `--max-cost` (cents) / `--max-wall-time` CLI flags, or a `defaults:` block in the `.dip` workflow.
- Precedence: CLI flags / `Config.Budget` win; `defaults:` is the fallback. Folded in by `tracker.ResolveBudgetLimits`.

### OpenAI returns errors inside 200 SSE streams
The Responses API returns HTTP 200 and sends `error` / `response.failed`
as SSE event types. The adapter must handle these — they are NOT reflected
in the HTTP status code.

### Gateway upstream transparency
Tracker does no *gateway-specific* handling: it appends a per-provider suffix to the base URL, passes model strings through unchanged, and parses each provider's normal SSE/API (error events, deltas, tool calls) exactly as it does without a gateway. So any gateway that preserves provider API/SSE semantics is transparent — gateway-side improvements land with **no tracker code change**. Two cases on the `bedrock` kind: when AWS adds OpenAI models to Bedrock, the gateway re-routes `gpt-*` / `o*-*` from the current Claude masquerade to real OpenAI by updating its own mapping; when real Bedrock streaming replaces today's synthesized stream, the SSE wire format is unchanged so the existing adapter parsing just yields live tokens in the TUI. Don't add tracker code to "handle" either — they are upstream events. Operator setup + caveats: [`docs/architecture/bedrock-gateway.md`](docs/architecture/bedrock-gateway.md). (Kind dispatch / fail-closed routing is in the Dippin-lang compatibility rules above.)

### CLI UX commands
- `tracker workflows` — lists all embedded built-in workflows with display names and goals.
- `tracker init <name>` — copies a built-in workflow to cwd for customization, together with the same sidecar set the engine materializes (`pipeline.WorkflowFiles`: all of `prompts/<name>/` + `scripts/<name>/` — including sourced `lib/` helpers no directive names — plus every `*_file` directive path, e.g. superspec's shared `prompts/build_product/SpecLint.md`), so the copied `.dip`'s relative paths and its `${graph.workflow_dir}` sourcing resolve on disk. Refuses to overwrite any of them.
- `tracker doctor` — preflight health check (API keys, dippin binary, workdir). Run before first pipeline. With a pipeline file, warns when a node pins a model dippin marks `Deprecated` (retired on the first-party provider API, still billable via Bedrock/Vertex passthrough — `llm.IsDeprecated`); suppressed when a gateway is configured (`TRACKER_GATEWAY_URL`/`KIND`), since the model then routes to a passthrough platform.
- `tracker diagnose [runID]` — deep failure analysis (reads status.json + activity.jsonl). Shows tool output, stderr, errors, timing anomalies, actionable suggestions. Without a run ID, analyzes the most recent run.
- `tracker update` — self-update to latest GitHub release. Detects install method (Homebrew/go install/binary), verifies SHA256 checksum, smoke-tests new binary, atomic swap with .bak rollback. Non-blocking update check runs on every `tracker run` (24h cache).
- `tracker version` — shows commit hash, build time, and which providers are configured. Uses Go VCS metadata for `go install` builds, GoReleaser ldflags for releases.
- `tracker verify-tests [dir] [--race]` — test-fidelity gate for a `VerifyMilestone`-style node: flags duplicate/near-duplicate Go test bodies (exit 1 if any), and `--race` adds an opt-in `go test -race ./...` pass that fails on a genuine data race. No-op (skip, not failure) when there is no `go.mod`/`go.work`, no `go` toolchain, or the race detector can't run (no cgo / unsupported platform). Library equivalents: `tracker.AnalyzeTestFidelity(dir)` and `tracker.DetectTestRaces(ctx, dir)`.

### Library API equivalents (for embedded integrations)
- Prefer `tracker.Diagnose(ctx, runDir)` / `tracker.DiagnoseMostRecent(ctx, workDir)` over shelling out to `tracker diagnose` and scraping stdout.
- Use `tracker.Doctor(ctx, cfg, opts...)` for structured preflight checks in services/tests.
- Use `tracker.Audit(ctx, runDir)` and `tracker.ListRuns(workDir, ...)` for run inspection.
- Use `tracker.NewNDJSONWriter(io.Writer)` to get the same stream shape as `tracker --json`.

### Bare name resolution
Running `tracker build_product` (no path, no extension) resolves in order:
1. `build_product.dip` in cwd (local file wins)
2. `build_product` as a file in cwd
3. Built-in embedded workflow by name
4. Error with list of available built-ins

This applies to `tracker validate`, `tracker simulate`, and `tracker run` uniformly via `resolvePipelineSource()` in `cmd/tracker/resolve.go`.

### Autopilot mode
- `--autopilot <persona>` replaces all human gates with LLM-backed decisions
- Four personas: `lax` (forward progress), `mid` (balanced, default), `hard` (high bar), `mentor` (approve with feedback)
- `--auto-approve` is deterministic (no LLM) — always picks default/first option
- The `AutopilotInterviewer` in `pipeline/handlers/autopilot.go` implements `LabeledFreeformInterviewer`
- Uses structured JSON output: `{"choice": "...", "reasoning": "..."}`
- Provider errors hard-fail per CLAUDE.md (only parse failures fall back to default)
- Two autopilot implementations: `AutopilotInterviewer` (native API) and `ClaudeCodeAutopilotInterviewer` (claude CLI subprocess)
- When `--backend claude-code`, autopilot routes through the claude subprocess — no API key needed
- Default model picks cheapest from configured provider via `Client.DefaultProvider()`
- `autopilotCfg` in `cmd/tracker/run.go` threads the config to `chooseInterviewer`
- For fully headless execution without an LLM judge, use `--webhook-url` instead — gates are POSTed to an external service and blocked on a callback (Closes #63, #86)

### Tool output capture
- `ctx.tool_stdout` / `ctx.tool_stderr` carry the **tail** (not head) of the stream up to the per-stream cap. Routing markers printed at end-of-output via `printf` survive truncation by construction.
- The captured value contains **only** the tail payload — no in-band truncation-marker suffix is appended (an earlier suffix could clobber routing tokens).
- Overflow emits `EventToolOutputTruncated{stream, limit, captured_bytes, dropped_bytes}`; `tracker diagnose` surfaces it as a suggestion.
- When a node has conditional edges that all evaluate false and routing falls through to an unconditional edge, the engine emits `EventConditionalFallthrough` with the missed conditions. Diagnose correlates this with truncation events to surface "your routing marker may have been dropped."

### Activity log threat model (full)
The Critical Rules entry above covers the operational contract. Background and residual risks:
- Pre-#213 the log lived at `<workDir>/.tracker/runs/<runID>/activity.jsonl` mode `0o644`. A tool subprocess running with `cmd.Dir = workDir` could append fake decision edges, truncate to suppress `tool_output_truncated`, or forge `pipeline_completed status=success` via relative-path shell redirection. Relocation to `$XDG_STATE_HOME/.../<runID>/` removes that *relative-path* reach.
- **Absolute-path reach by a same-UID subprocess is a residual, not a bug.** An *unjailed* tool subprocess is given `TRACKER_RUN_ID` (#323) and inherits `HOME`, so it can reconstruct `$HOME/.local/state/tracker/runs/$TRACKER_RUN_ID/activity.jsonl` and truncate/`sed -i`/delete it. Relocation only defends the relative-path (`cmd.Dir=workDir`) vector; a process at tracker's UID can always reach tracker's own state files (dir `0700`/file `0600` gate only *other* users), and could enumerate the runs dir even without `TRACKER_RUN_ID`. The sentinel counts *injected* lines, not *deleted* ones, so silent line-deletion is out of scope by construction. The real boundary for an untrusted tool node is the `writable_paths` Landlock jail (which bounds writes to the workdir globs, so `$HOME/.local/state` is unreachable). Operator copy must not claim the secure log is tamper-proof against a same-UID node.
- The sentinel detects casual injection (shell redirection, `tee -a`, `find ... -delete`). It does **not** detect a motivated forger who reads tracker's source and emits the sentinel bytes themselves. Per-line HMAC was considered (Option C) and dropped — key-management cost beats marginal gain. Operator-facing copy must not claim the runtime "prevents" forgery.
- Snapshot guards: the Close-time copy `Lstat`s `<artifactDir>` and `<artifactDir>/<runID>` before MkdirAll/open and refuses if either is a symlink. Residual TOCTOU between Lstat and MkdirAll (microsecond window) is accepted because the secure file remains authoritative.
- Legacy runs without a secure file (pre-#213 or archive-moved): `ResolveActivityLogPath` falls back to `<runDir>/activity.jsonl` without sentinel validation — absence of sentinel on the legacy path is not an injection signal.
- **Checkpoint (#559, RELOCATED like #213).** `checkpoint.json` is **authoritative for resume** (`EdgeSelections` picks the next edge, `Context` is restored), so the authoritative copy now lives in the secure state dir — `pipeline.SecureCheckpointPath(runID)`, the SAME `<secureBase>/<runID>/` as the activity log — out of the tool-reachable workdir. `ResolveCheckpoint` reads secure-first (legacy `<workDir>/.tracker/runs/<runID>/checkpoint.json` fallback for pre-#559 / archive-moved runs). A best-effort **snapshot** is still written under the artifact dir so read-only tooling (diagnose/audit/run-manifest) keeps working; that snapshot is NOT authoritative — a tampered snapshot corrupts only diagnostics, never resume routing. An explicit `WithCheckpointPath`/`Config.CheckpointDir` is honored as-is (no relocation). Residual is identical to the activity log: relocation removes the relative-path (`cmd.Dir=workDir`) tamper vector, not the absolute-path reach of a same-UID process that knows `TRACKER_RUN_ID` + `$HOME`; the `writable_paths` jail remains the boundary for an untrusted tool node. `writeFileAtomic` also `O_NOFOLLOW`s the temp write.

### Agent backends

Three backends, all implementing `AgentBackend` (`pipeline/backend.go`). `CodergenHandler` selects via `selectBackend()`.

- **`native`** (default): wraps `agent.Session` — turn loop, tool registry, context compaction.
- **`claude-code`**: spawns `claude` as a subprocess, parses NDJSON. **API keys are stripped** from the subprocess env so the claude CLI uses subscription auth (Max/Pro OAuth) — override with `TRACKER_PASS_API_KEYS=1`. With `--backend claude-code` and no per-node override, non-Anthropic model names are stripped so the CLI uses its default.
- **`acp`**: ACP-protocol client (`pipeline/handlers/backend_acp_client.go`) for headless external agents. The `writable_paths` jail refuses `acp` (out-of-process; tracker cannot apply Landlock to it).

Selection: per-node `backend:` attr wins over the global `--backend` flag (a node with `backend: native` stays native under `--backend claude-code`). The engine and TUI see the same `agent.Event` stream regardless of backend.

A `writable_paths` refuse-to-start (Landlock unavailable, bad globs, non-local env — and the dispatcher-layer `backend: claude-code` / `acp` refusal in `CodergenHandler.Execute`) is a **non-retryable, routable `OutcomeFail`** (`jailRefusedOutcome`, #642) — never `OutcomeRetry` and never a hard handler error, since retrying a host-capability check re-hits the same probe; a `fallback_target` / `when ctx.outcome = fail` edge can escalate it once. The handler's `Outcome.FailureReason` rides on the node's `stage_failed` events as `Err` so the TUI line and `tracker diagnose` show the cause.

Error classification (`classifyError` in `backend_claudecode.go`): rate-limit and network → `OutcomeRetry`; auth, credit-balance, budget-limit, SIGKILL (exit 137) → `OutcomeFail`. Credit-balance also logs actionable guidance to unset `ANTHROPIC_API_KEY` for Max subscription auth. "Escalation" is a routing convention on top of `OutcomeFail` edges, not a distinct status — see `docs/architecture/engine.md#escalate`.

### Strict failure edges and the failure cascade
- A `fail` outcome resolves in dippin's documented order (dippin `docs/edges.md` § Failure Handling; #653): (1) an outgoing edge whose condition matches (`when ctx.outcome = fail` / `on fail`); (2) bounded node retry (`retry_target` + `max_retries`, the `OutcomeRetry` path); (3) the node's own `fallback_target` / `fallback_retry_target`; (4) the graph's `defaults.on_failure` (adapter → graph attr `fallback_target`); (5) halt. `findFallbackTarget` implements 3→4. The section-level `else ->` default (#649) is success-side only and is **never** in this path.
- Pure strict-failure rule (unchanged): when a node's outcome is "fail" and ALL outgoing edges are unconditional, `checkStrictFailure` never takes the unconditional edge — it runs steps 3–5 (WIP preserved first) and dead-stops if nothing resolves.
- A failed node that HAS conditional edges but none matching `fail` no longer dead-stops with a bare `no matching edges` error (#653): `advanceToNextNode` → `unmatchedFailureCascade` (`pipeline/engine_failure_cascade.go`) runs steps 3–5 on the typed `noMatchingEdgesError`. Every fallback hop (cascade AND pure strict-failure) goes through `recordFallbackHop`: `decision_edge` with `edge_priority = fallback` (`EdgePriorityFallback`), `conditional_fallthrough` with the missed guards when any were tried, and `SetEdgeSelection` so a resume replays the hop instead of re-selecting the unconditional edge. Step 5 is a real terminal (`terminalFailureHalt`, shared with `checkStrictFailure`): WIP preserved, reason-carrying `stage_failed`, `recordHalt`, `OutcomeFail` result, error text still contains `no matching edges`. A failed node with conditional edges AND an unconditional edge still takes the unconditional edge (intentional routing, as before).
- A tool node exceeding its `timeout:` is an ordinary `OutcomeFail` (#644): the process group is killed, `ctx.tool_stderr` gets `command timed out after <timeout>` appended, the stdout tail is kept, and `EventToolTimeout` is emitted — so `when ctx.outcome = fail` / `fallback_target` route it, and the strict rule above applies when nothing routes it. Only a mid-command cancellation of the run's context (Ctrl+C, library caller ctx, parallel `branch_timeout`, parent deadline — not `--max-wall-time`, which `BudgetGuard` checks between nodes) stays a hard handler error.
- Every fallback (retry-exhausted `fallback_retry_target`, strict-failure `fallback_target`, goal-gate exhausted path) is one-shot per node per run (`FallbackTaken` latch on the checkpoint, #642): a second failure after the fallback path looped back emits `EventFallbackLatched` from all three sites (`emitFallbackLatched`) and dead-stops with `OutcomeFail` naming the consumed fallback instead of cycling forever or claiming "no failure edge".
- A fallback that resolves to the failing node itself is **no fallback** (#650): `findFallbackTarget` / `handleRetryExhausted` skip a self-target, so a fail-closed terminal like build_product's `AbortRun` (the graph-level `on_failure` target) runs once and halts — no re-entry, no latch consumed, no `fallback_latched`. The node reached via a fallback records its origin (`GateState.FallbackOrigin`, cleared on ordinary-edge entry) so the terminal `stage_failed`/CLI error names the real cause: `node "AbortRun" (reached from "Setup" failure)`. A failing node with no outgoing edges halts through `checkStrictFailure` (WIP preserved) rather than the no-outgoing-edges invariant error.
- Tool nodes set `Outcome.FailureReason = "exit <code>: <stderr tail>"` on non-zero exit (#652; stdout tail when stderr is empty) — it rides `stage_failed.Err` → activity-log `error` → TUI `FAILED:` → diagnose. Capture of `ctx.tool_stdout`/`tool_stderr` is unchanged.
- This prevents tool nodes (Setup, Build) from silently continuing after failure
- Pipelines that want a node-specific failure route use `when ctx.outcome = fail` edges (step 1); `defaults.on_failure` is the workflow-wide catch-all (step 4)

### `tracker __jail-exec` internal subcommand (#272)
`writable_paths` on an agent node makes tracker re-exec itself via `/proc/self/exe __jail-exec -- <anchor> <globs> -- sh -c <cmd>`, applying Linux Landlock ABI v3 before `syscall.Exec` into `sh -c`. Dispatched in `cmd/tracker/main.go` **before** flag parsing. Operators MUST NOT invoke `__jail-exec` directly — the `__` prefix is the "internal" signal.

Two-tier enforcement: in-process tools (`Write`, `Edit`, `ApplyPatch`) hit `openat2(RESOLVE_BENEATH | RESOLVE_NO_SYMLINKS | RESOLVE_NO_MAGICLINKS)` against a session-root fd (no TOCTOU). Bash subprocess is bounded at the directory-ancestor of each glob's static prefix (Landlock is path-prefix on directories, not glob-aware).

Refuse-to-start gate in `pipeline/handlers/codergen_jail.go`: invalid `working_dir`, malformed globs (absolute / `~` / parent-escape / **any brace usage** / unsupported doublestar / malformed character classes), backend ∈ {claude-code, acp, unknown}, Landlock unavailable (Landlock ABI < 3, i.e. kernel < 6.2, or non-Linux). The refusal is a non-retryable `OutcomeFail` on the native path (#642; see Agent backends). Residual escape classes (not bounded): network egress, reads/exfil-by-read, anything inside an allowed path. Narrow globs are the strongest posture. Full design: [`docs/superpowers/specs/2026-06-01-issue-272-writable-paths-enforcement-design.md`](docs/superpowers/specs/2026-06-01-issue-272-writable-paths-enforcement-design.md).

**`writable_paths_mode: require|prefer` (#648; default `require`, unchanged).** Delivered via the agent `params:` passthrough; `AgentConfig.WritablePathsMode`; any other value (case/whitespace variants, empty) is a load error naming the node (`pipeline.ValidateWritablePathsMode`). Gate classes: G1 (bad globs / `working_dir`) = **authoring** and G2 (claude-code / acp / unknown, plus the dispatcher-layer type check) = **backend** — both refuse in BOTH modes. G3 (`ProbeLandlock` fails) = **host capability** — refuses under `require`, **degrades** under `prefer`. What degrades: only the Bash subprocess (no `CommandWrapper`, so it has its pre-#272 write reach). What never degrades: in-process `Write`/`Edit`/`ApplyPatch` (+ env-routed `generate_code` / `write_enriched_sprint`) keep the glob policy via `installDegradedPolicy` with the strongest available symlink-safe resolver — the enforced tier's `openat2` closures on Linux 5.6–6.1 (`execpkg.ProbeOpenat2`), else `os.Root` per-component resolution (macOS, Linux < 5.6); both refuse a symlink at ANY path component (the `os.Root` tier `Lstat`-walks every component first — `rootRefuseSymlinks` — so relative in-anchor links like `ok -> .` are refused too; residual: TOCTOU between that walk and the open, same-UID only); authoring/backend refusals; the #275 hole; a post-probe `__jail-exec` Landlock failure is still a hard error, never a degrade. `branch.<n>.writable_paths_mode` (parallel params spill) is validated the same way. A degrade is recorded everywhere: `pipeline.EventJailDegraded` (`jail_mode` / `jail_reason` / `jail_declared_globs` on the wire; emitted by the codergen handler BEFORE the session's first turn, from the `NativeBackend`'s internal `jail_degraded` agent event which is consumed, not forwarded), a TUI `MsgNodeWarning` / CLI line, `tracker diagnose` `SuggestionJailDegraded`, `tracker doctor <pipeline>` warning per `prefer` node when this host lacks Landlock, `run.json` `jail_degraded_nodes` + `nodes[].jail`, and `stats.jail: "degraded"` on the trace entry. **Operator copy must never call a `prefer` node sandboxed** — every degrade message says UNJAILED. `examples/build_product.dip` `FinalCommit` uses `prefer` (so the #349 guard enforces on Linux ≥ 6.2 and the pipeline still runs on macOS). Contract + invariants C1–C11: [`docs/superpowers/specs/2026-09-17-issue-648-writable-paths-prefer.md`](docs/superpowers/specs/2026-09-17-issue-648-writable-paths-prefer.md).

## Project Infrastructure

### Website (GitHub Pages)
- Hosted at <https://2389-research.github.io/tracker/>. Source: `site/` directory on `main`, built with Hugo extended. The `gh-pages` branch is a build artifact — never edit by hand.
- Deploy: `.github/workflows/docs.yml` runs on every push to `main` that touches `site/**`, publishing `site/public/` to `gh-pages` via `peaceiris/actions-gh-pages` with `force_orphan: true`.
- Layout: hand-written HTML in `site/content/*.html`, shared layouts in `site/layouts/`, static assets in `site/static/`, nav data in `site/data/nav.yaml`.
- Local preview: `cd site && hugo server` (port 1313). `baseURL` in `site/hugo.toml` is the full production URL (`https://2389-research.github.io/tracker/`); pages are served under the `/tracker/` path. `uglyURLs = true` keeps URLs at `/tracker/<name>.html` (matching the pre-Hugo URL shape).
- Per-page front matter controls a11y metadata (`title`, `description`, `og_*`, optional `mermaid: true`, `jsonld:` block inlined as JSON-LD). Use `TechArticle` for inner pages, `SoftwareApplication` for home, `DefinedTermSet` for glossary.
- Adding a page: drop `site/content/<name>.html` with the front matter block (copy an existing page), add to `site/data/nav.yaml` if it should appear in the nav, push to `main`.
