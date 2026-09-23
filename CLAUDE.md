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
- **Built-in workflows**: `examples/` (embedded into the binary). Embedded built-ins resolve `prompt_file` / `command_file` sidecars over the embed FS (`pipeline.ResolveFileDirectivesFS`); a new sidecar dir must be added to the `go:embed` list in `tracker_workflows.go`. **Library callers must anchor a source** (`tracker.SourceRef` — `Path` or `Builtin` — via `Config.Source` / `WithSource`); an un-anchored source guesses and is wrong for an edited `tracker init` copy. Details: `docs/architecture/engine.md#graphworkflow_dir-for-embedded-built-ins`.
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
- In `build_product.dip`, route mechanical failures (Setup/tool errors, budget/turn exhaustion with nothing to salvage) to `AbortRun`, mid-build stuck-ness (a milestone that will not go green) to `EscalateMilestone`, red or unverified builds to `EscalateVerification`, and post-build review/planning failures (reviewer/re-review budget) to `EscalateReview`.
- `build_product`'s test gate converges with tracker-runner's fork: language-native lint (`ci-probe.sh` `run_language_native_gates`) is ADVISORY (always returns 0, prints an `ADVISORY:` line; only a project Makefile `ci`/`check`/`lint`/`test` target blocks), and `verify.sh` exits 0 green / 1 fail / 3 NOT-YET-VERIFIABLE (milestone mode: no suite executed a positive test count and no Makefile target ran) — `TestMilestone.sh` maps 3 to the `tests-not-yet-verifiable` marker on outcome=success (VerifyMilestone decides), and its escalate sentinel is `__ROUTE_ESCALATE__` (quoted in the `.dip`: dippin's lexer drops a bare leading `__`). `EnsureEnv` (a `marker_grep` node) runs a seed `build-setup.sh` hook between Setup and SpecLint; `env-failed` / a missing marker abort.
- `build_product` has two post-build gates: `EscalateVerification` (default `abandon` → `AbortRun`; red FinalBuild, failed FinalSpecCheck, reviewer/CheckReviewsComplete failure, SynthesizeReviews/ApplyReviewFixes catch-alls) and `EscalateReview` (default `accept`, `override: true`; ONLY `CheckReviewFixBudget` exhausted). (`FinalCommit` no longer falls back here — since #656 it is a deterministic tool node whose fail edge routes to `AbortRun`.) Route new red/unverified failures to the former; pre-build agent failures (ReadSpec/Decompose) go to `AbortRun`. Every gate's `abandon` routes to `AbortRun` — a gate edge to `Done` ends the run `success`.
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
- Example workflow scripts have shell fixture suites beside them (`examples/scripts/<workflow>/<Name>_test.sh`, `examples/subgraphs/scripts/**/*_test.sh`); they run under `go test ./...` (`pipeline/example_scripts_test.go`) and directly via `make test-scripts`. A script under test is run with `sh` (dippin uses `sh -c`, so the shebang is ignored — #324). Add a `<Name>_test.sh` when adding a `command_file:` sidecar.
- All three shipped pipelines (`build_product`, `build_product_with_superspec`, `ask_and_execute`) are decomposed — every agent `prompt:` and tool `command:` is a `prompts/<name>/<NodeID>.md` / `scripts/<name>/<NodeID>.sh` sidecar, except human-gate `prompt:` blocks (dippin has no `prompt_file` for human nodes) and the deliberately inline three-line `AbortRun` notice; superspec's `SpecLint` loads the shared `prompts/build_product/SpecLint.md`. Keep it that way, and pin a new dir in the `go:embed` list + `TestEmbeddedSidecarsFollowDirectives`.
- Shared shell is NOT sourced across workflows — materialization ships only a built-in's own `scripts/<name>/` tree — so `ask_and_execute` and superspec carry byte-identical COPIES of build_product's `lib/gitignore.sh` (superspec also `verify.sh` + `ci-probe.sh`) under their own `lib/`, each pinned by a `lib/parity_test.sh` (#646); fix the build_product original first, then re-copy.
- `build_product`'s shared shell (gitignore/exclude helpers, the `.ai/build/verify.sh` + `ci-probe.sh` + rubric bodies Setup installs, the guarded attempt counter, build-context seeding) lives in `examples/scripts/build_product/lib/` and is sourced at runtime via `LIB="${graph.workflow_dir}/scripts/build_product/lib"` (fail-loud if empty); the suites source `test_helpers.sh` (`stage_script` mirrors that expansion) and the `lib/*_test.sh` suites are picked up by the same glob (`scripts/*/lib/*_test.sh`).
- The seven dotpowers-family workflows (`dotpowers`, `-auto`, `-simple`, `-simple-auto`, `test-kitchen`, `scenario-testing`, `kitchen-sink`; disk loads, not embedded) likewise source one implementation from `examples/scripts/dotpowers/lib/` (`tasks.sh`, `counters.sh`, `validate.sh`) through thin per-workflow wrappers, and `pipeline/example_scripts_drift_test.go` fails if any wrapper set (or the megaplan / ralph-loop copies) drifts from byte-identical — fix once, copy to every dir (#646).
- `make doctor` — every core pipeline must be A grade. It runs `dippin doctor` once per pipeline at the dippin-lang version pinned in `go.mod`. Never hand `dippin doctor` several files: it grades the first, ignores the rest, and exits 0.
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
For PRs that touch a security boundary (`agent/exec/jail*.go`, `agent/exec/env.go`, `pipeline/handlers/codergen_jail.go`, the `__jail-exec` dispatch, new `agent/tools/` filesystem/subprocess code, the tool denylist/allowlist, or the activity-log integrity path), follow the **"freeze and prove"** pattern: threat model → freeze the public API → prove the contract with invariant/property tests → audit-class sweep against `docs/architecture/agent-tool-jail-checklist.md` → small patch against the frozen contract.
Full guidance: [`docs/architecture/security-pr-process.md`](docs/architecture/security-pr-process.md). Reserve it for high-blast-radius changes like #272/#275, not one-line jail tweaks.

## Architecture Gotchas

### `${graph.workflow_dir}` for embedded built-ins
- Contract: `${graph.workflow_dir}/<relpath>` resolves a workflow-relative file; the concrete path is implementation-defined — never rely on it. Disk load: the `.dip`'s directory (`pipeline.SeedWorkflowDir`, #332). Embedded built-in: `NewEngineFromGraph` (`stageWorkDir`, before `bindInputs`) materializes the whole embedded tree under `<workDir>/.tracker/` via `pipeline.MaterializeBuiltinWorkflowDir` on every construction (fresh and resume); read-only entry points never materialize.
- Packed `.dipx` is deliberately NOT covered (`guardPackedWorkflowDir` fails loud, #430/#467) — its sidecars would come from an unverified sibling dir. Customize a built-in with `tracker init <name>`, never by editing the copy.
- Full text (embed FS resolver, `go:embed` list, `SourceRef` anchoring): `docs/architecture/engine.md#graphworkflow_dir-for-embedded-built-ins`.

### The adapter is the bridge
`pipeline/dippin_adapter.go` converts dippin IR to tracker's Graph model. Every naming mismatch between dippin and tracker conventions lives here — new IR fields land here first.

### Typed node-config accessors
Reads go through typed accessors on `*pipeline.Node`: `AgentConfig(graphAttrs)`, `ToolConfig()`, `HumanConfig()`, `ParallelConfig()`, `RetryConfig(graphAttrs)`. Defined in `pipeline/node_config.go`. When adding a new node attribute, extend the appropriate `NodeConfig` struct and its accessor — don't add fresh `node.Attrs[...]` reads. A handful of strict-parse helpers retain raw reads (see inline comments).

### `auto_status` STATUS contract
`auto_status: true` derives an agent node's outcome from a `STATUS:` line (`parseAutoStatus`, `pipeline/handlers/codergen_autostatus.go`). Tolerant grammar (#645) and **last-line-wins** — gate-like prompts (SpecLint, ForgeSpec, VerifyMilestone, SynthesizeReviews, Decompose) emit `STATUS:fail` first and override with a final `STATUS:success`, so a truncated or verdict-less response fails closed. A missing verdict is `EventAutoStatusMissing`; only `goal_gate` nodes fail closed on it, plain nodes keep the legacy success default. Full grammar: `docs/architecture/handlers.md`.

### Structured output (`response_format`)
`response_format: json_object` on agent nodes forces JSON output at the LLM API level. Path: `.dip` → `AgentConfig.ResponseFormat` → `node.Attrs["response_format"]` → `codergen.buildConfig` copies it onto `SessionConfig.ResponseFormat` → `session.buildResponseFormat()` → `llm.Request.ResponseFormat` → provider translator (Anthropic: system instruction via `appendResponseFormatInstruction`, OpenAI: native `json_object`, Gemini: `responseMimeType`). Use on any agent that must produce structured JSON.

### The engine doesn't know about parallel/fan-in
Engine treats every node uniformly (execute handler, select edge, advance). The parallel handler dispatches branches internally and hints the next node via `suggested_next_nodes` — no special-case engine code.

### Git artifacts and bundle export
`WithGitArtifacts(true)` makes the artifact run dir a git repo and commits after every terminal node. `ExportBundle(runDir, outPath)` (`tracker_bundle.go`) wraps `git bundle create --all` for a portable history. `Result.ArtifactRunDir` is the canonical run-dir locator. `--export-bundle` calls it post-run; failures warn, don't fail. Canonical hand-off pattern for a remote worker → `git clone <bundle>` on the user's machine.

### Checkpoint resume is fragile
- Restart budget is per resolved loop target AND per iteration of the enclosing loop (#603, #643): `Checkpoint.RestartCounts[target]`, reset (and the `FallbackTaken` latch re-armed via `Checkpoint.ClearFallbackTaken`) when an enclosing header restarts — `pipeline/engine_restart_scope.go` derives natural loops by dominator analysis. **The outermost loop's `max_restarts` is the run-wide bound the author must size** (`build_product.dip`: 200). Never add a reset that is not driven by a counted, budgeted restart.
- Parallel milestones must branch-namespace on-disk breakers (`.ai/milestones/${ctx.branch_id}/fix_attempts`, #420); a subgraph branch has its own child checkpoint.
- Fail-routing provenance lives on the TARGET's `GateState` (`RecordFallbackOrigin` / `GetFallbackOrigin`, #650/#651/#654); `recordHalt` stamps `Checkpoint.HaltedAt`. On `tracker -r`, `Engine.resumeEntryNode` (`pipeline/engine_resume.go`) un-completes the halted node and rewinds to the origin only when the halted node is a fail dead end (`isFailDeadEnd`); `--from <node>` / `--resume-no-rewind` are the explicit forms. Never resume by skipping a completed terminal through its edge.
- Full text: `docs/architecture/engine.md#checkpoint-semantics` and `#restart`.

### Token usage flow
`llm.Usage` → `agent.SessionResult.Usage` → `pipeline.SessionStats` (`pipeline/handlers/transcript.go`) → `EngineResult.Usage` (`Trace.AggregateUsage`); the parallel handler folds branch stats into its own outcome. `cmd/tracker/summary.go` treats `llm.TokenTracker` (middleware-level) and `EngineResult.Usage` (trace-level) as independent sources. See `docs/architecture/engine.md#cost-estimation-and-pricing-558-639`.

### Pipeline context isolation
Per-node scoping (`node.<nodeID>.<key>`) is a stable feature — after each node finishes its dirty writes are aliased under `node.<nodeID>.<key>` so later nodes can reference a specific upstream node's output. Declarative `writes:` extracts a typed JSON payload into first-class keys. Full model: [`docs/architecture/context-flow.md`](docs/architecture/context-flow.md).

### Declared inputs (#553; requires dippin ≥ v0.51)
- `bindInputs` (`tracker_inputs.go`, in `NewEngineFromGraph` after `resolveWorkDir`) validates and stages at run start and **fails closed** (`*InputValidationError`) before any node runs. `${inputs.<name>}` is a closed namespace that is **untrusted by construction** — never on the tool_command safe-key allowlist (`inputContextPrefix`, `pipeline/expand.go`; dippin DIP157).
- `file` AND `secret` inputs are staged to `<workDir>/.tracker/inputs/<name>` (`pipeline.StageInputFile`, 0600, 10 MiB); a `secret`'s `${inputs.<name>}` is only the PATH (#555) — read it from the staged path in a tool, never interpolate the value. Subgraph call sites bind value-kind inputs via `bindSubgraphInputs` (`pipeline/subgraph.go`, #556); `file`/`secret` are not bindable there.
- Full text and library seam: `docs/architecture/engine.md#declared-inputs-binding-553-555-556`, `docs/architecture/embedding.md` §1a.

### Cost governance
- **Base model prices come from `dippin-lang/pricing` (#558), NOT a tracker table** — `llm.EstimateCost` → `pricing.Lookup` / `pricing.Cost`; tracker owns only the per-model cache-multiplier overlay (`overlayCacheMultipliers`, `llm/pricing.go`) and the dated-snapshot fold (`stripDateSuffix`, #639). An unpriced model is $0 + one warning, never a hard fail; `TestCatalogModelsArePricedByDippin` guards the catalog. Adopt new/repriced models by bumping the dippin pin.
- `pipeline.BudgetGuard` runs between nodes after every `emitCostUpdate`; breach → `OutcomeBudgetExceeded` / `EventBudgetExceeded`. Precedence: CLI `--max-*` flags / `Config.Budget` win over the `.dip` `defaults:` block (`tracker.ResolveBudgetLimits`).
- Full text (pricing fold, token flow, budget dimensions): `docs/architecture/engine.md#cost-estimation-and-pricing-558-639` and `#budget-guard`.

### OpenAI returns errors inside 200 SSE streams
The Responses API returns HTTP 200 and sends `error` / `response.failed`
as SSE event types. The adapter must handle these — they are NOT reflected
in the HTTP status code.

### Gateway upstream transparency
Tracker does no *gateway-specific* handling: it appends a per-provider suffix to the base URL, passes model strings through unchanged, and parses each provider's normal SSE/API exactly as without a gateway. Gateway-side improvements (Bedrock re-routing `gpt-*` / `o*-*` to real OpenAI; real Bedrock streaming replacing the synthesized stream) land with **no tracker code change** — don't add tracker code to "handle" them. Setup + caveats: [`docs/architecture/bedrock-gateway.md`](docs/architecture/bedrock-gateway.md).

### CLI UX commands
- `tracker workflows` — lists all embedded built-in workflows with display names and goals.
- `tracker init <name>` — copies a built-in workflow to cwd for customization, together with the same sidecar set the engine materializes (`pipeline.WorkflowFiles`); refuses to overwrite any of them. See `docs/architecture/engine.md#graphworkflow_dir-for-embedded-built-ins`.
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
`tracker build_product` (no path, no extension) resolves in order: `build_product.dip` in cwd (local file wins) → `build_product` as a file in cwd → built-in embedded workflow by name → error listing the built-ins. Uniform across `run` / `validate` / `simulate` via `resolvePipelineSource()` in `cmd/tracker/resolve.go`.

### Autopilot mode
- `--autopilot <persona>` (`lax` / `mid` default / `hard` / `mentor`) replaces all human gates with LLM-backed decisions — `AutopilotInterviewer` (native API) or `ClaudeCodeAutopilotInterviewer` (claude subprocess, no API key needed under `--backend claude-code`) in `pipeline/handlers/autopilot.go`, implementing `LabeledFreeformInterviewer` with structured JSON `{"choice", "reasoning"}`.
- Provider errors hard-fail (only parse failures fall back to the default). `--auto-approve` is deterministic (no LLM) — always the default/first option. Default model is the cheapest from the configured provider (`Client.DefaultProvider()`); `autopilotCfg` in `cmd/tracker/run.go` threads config to `chooseInterviewer`.
- Fully headless without an LLM judge: `--webhook-url` POSTs gates to an external service and blocks on a callback (#63, #86). Details: `docs/architecture/handlers/human.md`.

### Tool output capture
- `ctx.tool_stdout` / `ctx.tool_stderr` carry the **tail** (not head) of the stream up to the per-stream cap. Routing markers printed at end-of-output via `printf` survive truncation by construction.
- The captured value contains **only** the tail payload — no in-band truncation-marker suffix is appended (an earlier suffix could clobber routing tokens).
- Overflow emits `EventToolOutputTruncated{stream, limit, captured_bytes, dropped_bytes}`; `tracker diagnose` surfaces it as a suggestion.
- When a node has conditional edges that all evaluate false and routing falls through to an unconditional edge, the engine emits `EventConditionalFallthrough` with the missed conditions. Diagnose correlates this with truncation events to surface "your routing marker may have been dropped."

### Activity log and checkpoint threat model (#213, #559)
- The Critical Rules entry above is the operational contract. The secure log/checkpoint relocation defends the *relative-path* (`cmd.Dir=workDir`) tamper vector only; a same-UID unjailed subprocess with `TRACKER_RUN_ID` + `$HOME` can still reach the files, and the sentinel detects casual injection, not a motivated forger. **Operator copy must not claim the secure log is tamper-proof or that forgery is prevented** — the `writable_paths` jail is the real boundary.
- `checkpoint.json` is authoritative for resume, so its authoritative copy lives at `pipeline.SecureCheckpointPath(runID)` (same dir as the log; `tracker.ResolveCheckpoint` reads secure-first); the artifact-dir copy is a non-authoritative snapshot. Explicit `WithCheckpointPath` / `Config.CheckpointDir` is honored as-is.
- Full threat model and residuals: `docs/architecture/engine.md#run-state-integrity-activity-log-and-checkpoint-213-559`.

### Agent backends

Three backends implement `AgentBackend` (`pipeline/backend.go`; `CodergenHandler.selectBackend()`): **`native`** (default, `agent.Session`), **`claude-code`** (`claude` subprocess + NDJSON), **`acp`** (`pipeline/handlers/backend_acp_client.go`; the `writable_paths` jail refuses it — out-of-process). Per-node `backend:` wins over global `--backend`. Full doc: `docs/architecture/backends.md`.
- claude-code: **API keys are stripped** so the CLI uses subscription auth — `TRACKER_PASS_API_KEYS=1` overrides; non-Anthropic model names are stripped unless set per-node.
- `classifyError` (`backend_claudecode.go`): rate-limit and network → `OutcomeRetry`; auth, credit-balance, budget-limit, SIGKILL (exit 137) → `OutcomeFail` (credit-balance logs guidance to unset `ANTHROPIC_API_KEY`). A `writable_paths` refuse-to-start is a non-retryable, routable `OutcomeFail` (`jailRefusedOutcome`, #642).
- "Escalation" is a routing convention over `OutcomeFail` edges, not a status — `docs/architecture/engine.md#escalate`.

### Strict failure edges and the failure cascade
- A `fail` outcome resolves in dippin's order (#653): matching `when ctx.outcome = fail` edge → bounded retry → node `fallback_target` / `fallback_retry_target` → graph `defaults.on_failure` → halt. `else ->` (#649) is success-side only and never in this path. `findFallbackTarget` implements the fallback steps; `checkStrictFailure` (`pipeline/engine.go`) handles all-unconditional edges, `unmatchedFailureCascade` (`pipeline/engine_failure_cascade.go`) handles conditional-but-unmatched.
- NEVER let a failed tool node (Setup, Build) continue through an unconditional edge — the strict rule preserves WIP, tries the fallbacks, then dead-stops via `terminalFailureHalt`. Nodes with ANY conditional edge are assumed intentionally routed (an unconditional sibling edge is still taken).
- Every fallback is one-shot per node per run (`GateState.FallbackTaken`, #642 — `EventFallbackLatched` then dead-stop); a self-target is no fallback (#650); every hop goes through `recordFallbackHop` (`decision_edge` priority `fallback` + `SetEdgeSelection`) so resume replays it.
- Tool `timeout:` is an ordinary routable `OutcomeFail` (#644, `EventToolTimeout`); tool non-zero exit sets `Outcome.FailureReason` (#652) which rides `stage_failed.Err` → TUI/diagnose.
- Full text (cascade, terminal, latch, provenance, timeout vs cancellation): `docs/architecture/engine.md#strict-failure-edges-and-the-failure-cascade`.

### `tracker __jail-exec` internal subcommand (#272)
`writable_paths` on an agent node makes tracker re-exec itself via `/proc/self/exe __jail-exec -- <anchor> <globs> -- sh -c <cmd>`, applying Linux Landlock ABI v3 before `syscall.Exec` into `sh -c`. Dispatched in `cmd/tracker/main.go` **before** flag parsing. Operators MUST NOT invoke `__jail-exec` directly — the `__` prefix is the "internal" signal.
- Two tiers: in-process tools (`Write`, `Edit`, `ApplyPatch`) hit `openat2(RESOLVE_BENEATH | RESOLVE_NO_SYMLINKS | RESOLVE_NO_MAGICLINKS)` against a session-root fd (no TOCTOU); the Bash subprocess is bounded at the directory-ancestor of each glob's static prefix (Landlock is path-prefix on directories, not glob-aware).
- Refuse-to-start gate (`pipeline/handlers/codergen_jail.go`): G1 authoring (invalid `working_dir`; malformed globs — absolute / `~` / parent-escape / **any brace usage** / unsupported doublestar / bad character classes), G2 backend (claude-code / acp / unknown), G3 host (Landlock ABI < 3, i.e. kernel < 6.2, or non-Linux). A refusal is a non-retryable, routable `OutcomeFail` (`jailRefusedOutcome`, #642).
- Residual escape classes: network egress, reads/exfil-by-read, anything inside an allowed path — narrow globs are the strongest posture. Design: `docs/superpowers/specs/2026-06-01-issue-272-writable-paths-enforcement-design.md`.

**`writable_paths_mode: require|prefer` (#648; default `require`).** Authoring (G1) and backend (G2) gates refuse in BOTH modes; only the host-capability gate (G3, `ProbeLandlock`) degrades under `prefer` — and what degrades is ONLY the Bash subprocess (no `CommandWrapper`). In-process `Write`/`Edit`/`ApplyPatch` keep the glob policy via `installDegradedPolicy` (`openat2` closures on Linux 5.6–6.1, else `os.Root` + `rootRefuseSymlinks`). Any other mode value is a load error (`pipeline.ValidateWritablePathsMode`). Typed agent-node / parallel-branch IR field since dippin-lang v0.75.0 (dippin-lang#307; DIP163–165) — the adapter maps it, typed wins over the legacy `params: writable_paths_mode:` passthrough (still accepted; dippin hints DIP133). No shipped built-in currently declares `writable_paths_mode` — `build_product.dip` FinalCommit (its former example) became a deterministic tool node in #656, which needs no jail; the typed field remains supported for author pipelines.
- Every degrade is recorded (`pipeline.EventJailDegraded`, TUI `MsgNodeWarning`, diagnose `SuggestionJailDegraded`, doctor warning, `run.json` `jail_degraded_nodes`, `stats.jail`). **Operator copy must never call a `prefer` node sandboxed** — every degrade message says UNJAILED.
- Full contract, tiers, and the C1–C11 invariants: `docs/architecture/engine.md#agent-jail-refusal-and-writable_paths_mode-642-648` and `docs/superpowers/specs/2026-09-17-issue-648-writable-paths-prefer.md`.

## Project Infrastructure

### Website (GitHub Pages)
- Hosted at <https://2389-research.github.io/tracker/>. Source: `site/` directory on `main`, built with Hugo extended. The `gh-pages` branch is a build artifact — never edit by hand.
- Deploy: `.github/workflows/docs.yml` runs on every push to `main` that touches `site/**`, publishing `site/public/` to `gh-pages` via `peaceiris/actions-gh-pages` with `force_orphan: true`.
- Layout: hand-written HTML in `site/content/*.html`, shared layouts in `site/layouts/`, static assets in `site/static/`, nav data in `site/data/nav.yaml`.
- Local preview: `cd site && hugo server` (port 1313). `baseURL` in `site/hugo.toml` is the full production URL (`https://2389-research.github.io/tracker/`); pages are served under the `/tracker/` path. `uglyURLs = true` keeps URLs at `/tracker/<name>.html` (matching the pre-Hugo URL shape).
- Per-page front matter controls a11y metadata (`title`, `description`, `og_*`, optional `mermaid: true`, `jsonld:` block inlined as JSON-LD). Use `TechArticle` for inner pages, `SoftwareApplication` for home, `DefinedTermSet` for glossary.
- Adding a page: drop `site/content/<name>.html` with the front matter block (copy an existing page), add to `site/data/nav.yaml` if it should appear in the nav, push to `main`.
