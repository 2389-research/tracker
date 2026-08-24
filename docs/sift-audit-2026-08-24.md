# SIFT Codebase Audit

- **Status:** `COMPLETE`
- **Repository:** `2389-research/tracker`
- **Revision:** `217a50d4aa6bffc45d555b0e83b04a2d9bc00037` (`main`)
- **Scope:** whole tracked repository, plus relevant pre-existing local tests
- **Audit mode:** read-only; tests, builds, scripts, generators, workflows, and local test cases were not run
- **Disclosure:** one security finding is withheld from this public edition and tracked in a private repository security advisory

## 1. Executive summary

The audit inventoried 19 non-overlapping subsystems. Fourteen have public recommendations, one has a finding tracked privately, and four are explicit skips. Twenty-three public recommendations survived independent validation and three fresh audit-of-audit passes.

The dominant issue is split authority: usage and checkpoint state omit fields that later enforcement assumes are complete; graph and terminal state are reconstructed from mutable mirrors; and release, CLI, workflow, and documentation facts live in more than one editable place.

The highest-value public slices close release-quality false greens: stamp release binaries with their real identity and make the Dippin CI gate fail closed.

This audit found direct correctness evidence for the top public findings. Lower-ranked schema, CLI, example, and documentation findings address demonstrated drift but do not claim the same operational urgency. This public report is complete for the stated revision apart from the disclosed private security finding; runtime behavior was not executed because SIFT requires a read-only audit.

## 2. Coverage contract

| ID | Subsystem | Exact ownership boundary | Key files/interfaces/tests | Status |
|---|---|---|---|---|
| SUB-01 | Library execution facade | `tracker.go`; root client, input, resolve, workflow, attribute, and interviewer facade files; `doc.go`; API-surface golden | `Config`, `Engine`, `Run`, `NewLLMClient`, `Input`, source/run resolution | `skip` |
| SUB-02 | Events, capture, artifacts, and forensics | Root activity/event/capture/audit/bundle/failure/status files; `pipeline/events*`, activity paths, run manifest/record serialization; `internal/bundleid` | `StreamEvent`, `ActivityEntry`, `jsonlLogEntry`, `CaptureConfig`, `Audit`, `Diagnose` | `recommend: SIFT-SUB-02-01` |
| SUB-03 | Workflow schema, parsing, expansion, and validation | Pipeline graph/parser/Dippin/DIPX/schema/input/expand/lint/workflow-dir files; `pipeline/testdata/**`; root expansion fixtures | `Graph`, `Edge`, `LoadDippinWorkflow`, validation and condition syntax | `recommend: SIFT-SUB-03-01, SIFT-SUB-03-02` |
| SUB-04 | Pipeline engine and persistent run state | Top-level pipeline engine/checkpoint/context/budget/routing/runtime-artifact/git-lifecycle/usage/snapshot/spec files | `Engine`, `Checkpoint`, routing, restart, goal-gate and budget tests; relevant local tests inspected only | `recommend: SIFT-SUB-04-01, SIFT-SUB-04-02` |
| SUB-05 | Handlers and backend registry | Handler registry/backends and `pipeline/handlers/**` | `Outcome`, human/parallel/subgraph/manager-loop handlers; relevant local tests inspected only | `recommend: SIFT-SUB-05-01, SIFT-SUB-05-02` |
| SUB-06 | Main CLI | `cmd/tracker/**` | modes, flag parsing, command dispatch, help, run/TUI wiring, command tests | `recommend: SIFT-SUB-06-01` |
| SUB-07 | Agent session runtime | Root `agent/*.go` and tests, excluding execution environments and tools | `Session`, `SessionResult`, turn loop, compaction, guards, checkpoints; relevant local tests inspected only | `recommend: SIFT-SUB-07-01, SIFT-SUB-07-02` |
| SUB-08 | Execution environments and agent tools | `agent/exec/**`, `agent/tools/**`, `tools/jailcheck/**` and fixtures | `ExecutionEnvironment`, `LocalEnvironment`, writable-path policy, writing tools | `private security finding withheld` |
| SUB-09 | LLM client and providers | `llm/**` including Anthropic, OpenAI, Gemini, and OpenAI-compatible adapters | `Usage`, `Response`, `ProviderAdapter`, streaming, retry, pricing and trace tests | `recommend: SIFT-SUB-09-01, SIFT-SUB-09-02` |
| SUB-10 | Terminal UI | `tui/**` plus direct CLI/event wiring evidence | adapter, store, agent log, search, status bar and TUI tests | `recommend: SIFT-SUB-10-01, SIFT-SUB-10-02` |
| SUB-11 | Chat transports and frontends | `transport/chatops/**`, `transport/cli/**`, `cmd/trackerbot/**`, `cmd/trackerchat/**` | `Runner`, `ThreadInterviewer`, notifier/status sinks; relevant local tests inspected only | `recommend: SIFT-SUB-11-01` |
| SUB-12 | Conformance binary and suite | `cmd/tracker-conformance/**`, `transport/conformance/**`, goldens and fixtures | live client commands, conformance subject/suite, golden runs | `recommend: SIFT-SUB-12-01` |
| SUB-13 | SWE-bench harness | `cmd/tracker-swebench/**`, including `agent-runner`, Docker/build files, result analysis and tests | harness config, Docker runner, timeout and cleanup paths | `recommend: SIFT-SUB-13-01, SIFT-SUB-13-02` |
| SUB-14 | Shipped workflows and example assets | `examples/**`, `scenarios.jsonl`, `tracker_workflows.go` and direct catalog tests | 38 DIP files, 18 DOT files, prompts, commands, subgraphs, simulator manifests | `recommend: SIFT-SUB-14-01` |
| SUB-15 | Build, CI, release, and developer tooling | `.github/**`, `.gitignore`, `.goreleaser.yml`, `.pre-commit`, `.pre-commit-config.yaml`, `Makefile`, `scripts/**`, `go.mod`, `go.sum` | build identity, Dippin checks, docs/complexity/generation gates | `recommend: SIFT-SUB-15-01, SIFT-SUB-15-02` |
| SUB-16 | Documentation, website, and historical records | Top-level prose/license files, `docs/**`, `site/**`, `CLAUDE.md` | architecture index, gateway guides, Hugo deployment, historical plans/specs | `recommend: SIFT-SUB-16-01, SIFT-SUB-16-02` |
| SUB-17 | Managed-run supervision | `tracker_runmanager.go` and tests; direct transport call sites as evidence | `RunManager`, `ManagedRun`, capacity, cancel, pause and terminal-state model | `skip` |
| SUB-18 | Health and static-analysis APIs | Root doctor, simulate, estimate, test-fidelity, test-trace and diagnose files; `internal/diag`; `testdata/runs/**` as fixtures | `Doctor`, `Simulate`, `Estimate`, `AnalyzeTestFidelity`, diagnosis scanners | `skip` |
| SUB-19 | Shared test support and repository metadata | `internal/dipxtest/**` and residual support fixtures not owned above | DIPX packing helper and support-only data | `skip` |

Ignored caches, worktrees, run artifacts, local journals, installed audit skills, `.superpowers/**`, and `skills-lock.json` are not repository product sources. Relevant local tests were read under their domain owner and were never executed.

## 3. Prioritized recommendations

| Priority | Finding | Subsystem | Impact | Confidence | Effort | Blast radius | Prerequisites |
|---:|---|---|---|---|---|---|---|
| 2 | Release build identity | SUB-15 | high | high | small | subsystem | none |
| 3 | Fail-closed pinned Dippin gate | SUB-15 | high | high | small | subsystem | confirm Dippin exit contract |
| 4 | Parallel child-usage folding | SUB-05 | high | high | small | subsystem | none |
| 5 | Complete resumed-agent progress | SUB-07 | high | high | medium | subsystem | snapshot migration policy |
| 6 | Derived normalized token totals | SUB-09 | high | high | small | cross-subsystem | confirm fresh-token budget convention |
| 7 | Ownership-aware SWE cleanup | SUB-13 | high | high | small | subsystem | stale-container policy |
| 8 | Gate-scoped cancellation | SUB-05 | high | high | medium | cross-subsystem | interviewer contract decision |
| 9 | Buffered chat event delivery | SUB-11 | high | high | small | subsystem | none |
| 10 | Explicit session stop reason | SUB-07 | high | high | medium | cross-subsystem | public migration decision |
| 11 | Goal-gate checkpoint state machine | SUB-04 | high | high | medium | subsystem | checkpoint migration policy |
| 12 | Per-loop restart budgets | SUB-04 | high | high | medium | subsystem | legacy scalar migration |
| 13 | Authoritative TUI terminal state | SUB-10 | high | high | medium | subsystem | scoped-terminal rule |
| 14 | Traced/untraced completion parity | SUB-09 | high | high | large | application-wide | staged provider migration |
| 15 | Prepared execution graph | SUB-03 | high | medium | medium | cross-subsystem | define final invariants |
| 16 | One parsed condition authority | SUB-03 | high | high | medium | cross-subsystem | SIFT-SUB-03-02 |
| 17 | SWE deadline plus watchdog grace | SUB-13 | high | high | small | subsystem | SIFT-SUB-13-02 for labels only |
| 18 | Production client in conformance | SUB-12 | medium | high | small | subsystem | preserve raw-test seams |
| 19 | One Bedrock gateway guide | SUB-16 | high | high | small | subsystem | verify external gateway claims |
| 20 | Search-selected log viewport | SUB-10 | medium | high | small | local | none |
| 21 | CLI command metadata authority | SUB-06 | medium | high | small | local | none |
| 22 | Generated reader-side activity schema | SUB-02 | medium | medium | medium | cross-subsystem | schema/generator owner |
| 23 | DIP-only shipped workflow authority | SUB-14 | medium | high | small first slice | subsystem | example-path compatibility decision |
| 24 | Retire undeployed docs site copy | SUB-16 | medium | high | small | subsystem | external-link check |

Original audit priorities are retained; priority 1 is the finding tracked privately.

### SIFT-SUB-15-01 — Stamp release build identity

- **Finding ID:** `SIFT-SUB-15-01`
- **Authoritative subsystem:** SUB-15 — Build, CI, release, and developer tooling
- **Title:** Make release build identity explicit and verifiable
- **Verdict:** `recommend`
- **Primary evidence:** `Makefile:8-12,24-27` injects `main.version`, `main.commit`, and `main.date`; `.goreleaser.yml:12-25` replaces build flags with only `-s -w`.
- **Interfaces and call sites:** Defaults are `dev`/`unknown` at `cmd/tracker/main.go:99-109`; `cmd/tracker/vcs.go:7-18,36-40` cannot recover a tag from a local `(devel)` module build. `cmd/tracker/update.go:47-51` rejects dev builds and `update_check.go:24-31` suppresses update hints.
- **Tests and intent evidence:** `cmd/tracker/vcs.go:7-9` and `CHANGELOG.md:4764-4767` say release flags take precedence. The Homebrew test at `.goreleaser.yml:80-83` checks exit only, not the reported version.
- **Current representation:** Local and release builds have separate identity contracts; only the local Make path populates all runtime fields.
- **Current complexity or invalid states:** An archive can have a release name while its binary reports `dev`, an unknown commit/date, and disables shipped update behavior.
- **Why it is material:** Published artifacts can carry false provenance and reject self-update.
- **Proposed representation:** Stamp all three runtime fields from GoReleaser release metadata and verify the built archive binary.
- **Why it is simpler:** One complete identity tuple applies to every shipped binary.
- **Implementation scope:** `.goreleaser.yml` and a release-workflow assertion.
- **Smallest credible slice:** Add the three ldflags to the tracker build and assert a snapshot binary reports a non-dev version plus commit/date.
- **Regression risks:** Package paths, quoting, `v` prefix, snapshots, and prerelease templates may be wrong.
- **Migration concerns:** none; only newly built artifacts change.
- **Existing validation:** Local Make builds and runtime version-comparison tests.
- **Additional validation required:** Snapshot, stable, and prerelease archive checks; make the Homebrew smoke test assert identity rather than exit alone.
- **Impact:** high — release provenance and update behavior are wrong together.
- **Confidence:** high — the release flags and dev-only consumers directly establish the path.
- **Implementation effort:** small.
- **Blast radius:** subsystem.
- **Prerequisites:** none.
- **Priority:** 2.

### SIFT-SUB-15-02 — Make the Dippin gate fail closed

- **Finding ID:** `SIFT-SUB-15-02`
- **Authoritative subsystem:** SUB-15 — Build, CI, release, and developer tooling
- **Title:** Make Dippin validation one pinned, fail-closed CI contract
- **Verdict:** `recommend`
- **Primary evidence:** `Makefile:116-120` derives Dippin `v0.68.0` from `go.mod`, but `Makefile:122-132` turns any command or JSON failure into zero errors with `|| echo "0"`. `.github/workflows/ci.yml:25-26` independently installs unused `v0.49.0`, then runs the Make targets at lines 46-50.
- **Interfaces and call sites:** `make lint`, `make doctor`, CI, and the installed `.pre-commit:81-100` hook.
- **Tests and intent evidence:** CI labels these quality gates; `CLAUDE.md:41-46` requires loud failures. No shell-contract fixture covers command failure, malformed JSON, or an empty glob.
- **Current representation:** Tool version selection and result interpretation are split across CI, Make, and the installed hook.
- **Current complexity or invalid states:** Findings, tool failure, malformed output, and no input files can collapse to the same zero-error success.
- **Why it is material:** A required workflow-language gate can print success while validation did not run correctly.
- **Proposed representation:** Treat `go.mod` and one Make target as the Dippin authority. Capture each invocation once and distinguish valid findings from execution/decode failure; remove the stale unused install.
- **Why it is simpler:** One version and explicit result states replace two pins and a fail-open fallback.
- **Implementation scope:** `Makefile`, `.github/workflows/ci.yml`, and the adjacent installed hook or its delegation path.
- **Smallest credible slice:** Reject failed/unparseable invocations and empty input sets, then remove the unused install step.
- **Regression risks:** Dippin may use nonzero exit status for ordinary findings while still emitting valid JSON; preserve its actual contract.
- **Migration concerns:** Previously hidden infrastructure failures will become visible failures.
- **Existing validation:** none for this shell boundary.
- **Additional validation required:** Fixtures for clean, findings, command failure, malformed JSON, empty input, and exact version derivation.
- **Impact:** high — removes a deterministic false-green gate.
- **Confidence:** high.
- **Implementation effort:** small.
- **Blast radius:** subsystem.
- **Prerequisites:** verify Dippin's exit/output contract rather than guessing.
- **Priority:** 3.

### SIFT-SUB-05-01 — Preserve usage through parallel branches

- **Finding ID:** `SIFT-SUB-05-01`
- **Authoritative subsystem:** SUB-05 — Handlers and backend registry
- **Title:** Preserve child-run usage through parallel branches
- **Verdict:** `recommend`
- **Primary evidence:** `pipeline/handler.go:16-29` makes `Outcome.ChildUsage` the parent-budget channel. Subgraph and manager-loop handlers return it at `pipeline/subgraph.go:249` and `pipeline/handlers/manager_loop.go:705`; `pipeline/handlers/parallel.go:20-27,241-299,370-388` carries stats, overrides, and pause errors but drops `ChildUsage`.
- **Interfaces and call sites:** `Trace.AggregateUsage` at `pipeline/trace.go:118-124`, `BudgetGuard`, `ParallelHandler`, branch result aggregation.
- **Tests and intent evidence:** `subgraph_test.go:367` and `manager_loop_test.go:1095` pin child usage. Parallel tests cover stats and overrides but not child usage.
- **Current representation:** Parallel branch messages project only part of `Outcome`; the aggregate outcome cannot expose nested child usage.
- **Current complexity or invalid states:** Child spend disappears only when the child handler sits under parallel fan-out, so parent token/cost limits become topology-dependent.
- **Why it is material:** Parent budgets can silently become non-binding for costly nested parallel work.
- **Proposed representation:** Carry `ChildUsage` beside each branch result and fold it into the aggregate `Outcome.ChildUsage` once.
- **Why it is simpler:** One vertical accounting channel works for direct, subgraph, manager-loop, and parallel execution.
- **Implementation scope:** `pipeline/handlers/parallel.go` and focused handler/budget tests; keep the public JSON `ParallelResult` stable.
- **Smallest credible slice:** Add child usage to the internal branch message and aggregate return without changing serialized branch results.
- **Regression risks:** Double-counting if branch stats and child rollups overlap.
- **Migration concerns:** none on disk or public JSON when the internal channel remains separate.
- **Existing validation:** direct child-usage and parallel stats/override tests.
- **Additional validation required:** Parallel subgraph and manager-loop cases that prove parent totals and budget halts include child spend exactly once.
- **Impact:** high.
- **Confidence:** high.
- **Implementation effort:** small.
- **Blast radius:** subsystem.
- **Prerequisites:** none.
- **Priority:** 4.

### SIFT-SUB-07-01 — Persist resumed-agent progress

- **Finding ID:** `SIFT-SUB-07-01`
- **Authoritative subsystem:** SUB-07 — Agent session runtime
- **Title:** Persist the complete control-flow and accounting state in turn checkpoints
- **Verdict:** `recommend`
- **Primary evidence:** `agent/turn_checkpoint.go:27-88` claims complete resume state but stores only identity, turn, messages, episodes, and Git SHA. `agent/session.go:158-205,244-288`, `session_loop.go:19-35`, and `session_run.go:192-228` initialize guard, usage, result, context, and compaction state afresh.
- **Interfaces and call sites:** Codergen enables checkpoints while also applying `MaxCostUSD` and `NoProgressTurns` at `pipeline/handlers/codergen.go:931-955` and `codergen_native_inject.go:28-47`.
- **Tests and intent evidence:** `agent/turn_checkpoint_test.go:169-195,216-290` covers messages and identity, not accounting or guard continuity.
- **Current representation:** Durable conversation state and decision/accounting state live in different objects; only the former is serialized.
- **Current complexity or invalid states:** Resume resets cumulative cost, usage, tool counts, loop/no-progress history, retry counters, edit history, compaction counters, and warnings. Final stats omit pre-interruption work.
- **Why it is material:** Per-node cost and anti-loop limits gain a fresh budget after interruption.
- **Proposed representation:** Add one typed, versioned `Progress` member to `TurnSnapshot` containing normalized cumulative accounting and every state value that affects later decisions. Restore result, context tracker, and loop state before the next turn.
- **Why it is simpler:** One resume contract states what is durable; clients, contexts, locks, and safe caches remain explicitly ephemeral.
- **Implementation scope:** turn checkpoint, session/loop/run files, codergen integration checks, and checkpoint/guard tests.
- **Smallest credible slice:** Persist cumulative usage/tool counts plus loop and no-progress counters; prove cost and repeated-signature guards continue across one interruption.
- **Regression risks:** Double-counting the first resumed turn and serializing arbitrary `Usage.Raw`.
- **Migration concerns:** Bump the snapshot schema and choose a clear reject-or-migrate policy for schema 1.
- **Existing validation:** checkpoint round trips and separate usage/guard/compaction suites.
- **Additional validation required:** Interrupt/resume cases for cost, tool totals, repeated tool signatures, edit/no-progress history, compaction, and old-schema error messages.
- **Impact:** high.
- **Confidence:** high.
- **Implementation effort:** medium.
- **Blast radius:** subsystem.
- **Prerequisites:** snapshot migration policy.
- **Priority:** 5.

### SIFT-SUB-09-01 — Derive normalized token totals

- **Finding ID:** `SIFT-SUB-09-01`
- **Authoritative subsystem:** SUB-09 — LLM client and providers
- **Title:** Make normalized token totals a derived invariant
- **Verdict:** `recommend`
- **Primary evidence:** `llm/types.go:215-267` defines fresh input, output, cache buckets, and an independently writable total. Anthropic derives fresh input plus output at `llm/anthropic/translate_response.go:51-64`; OpenAI, Gemini, and OpenAI-compatible translators subtract cache buckets from input but preserve provider totals at `llm/openai/usage.go:27-60`, `llm/google/usage.go:19-41`, and `llm/openaicompat/translate.go:355-376`.
- **Interfaces and call sites:** `Usage.Add` sums inconsistent totals; parent budgets compare the aggregate at `pipeline/budget.go:350`. External backends document fresh input plus output in `docs/architecture/backends.md:210`.
- **Tests and intent evidence:** Cache tests verify bucket extraction but omit the cached total invariant; `llm/google/usage_test.go:30-34` checks it only for a non-cached case.
- **Current representation:** Normalized buckets and provider-reported totals can disagree inside one `Usage`.
- **Current complexity or invalid states:** Identical normalized usage can breach token limits differently by provider when caches are involved.
- **Why it is material:** Token budgets and summaries are provider-dependent despite a provider-neutral type.
- **Proposed representation:** Finalize normalized usage in one helper and derive internal `TotalTokens` as fresh input plus output. Keep the public/wire field during a compatible release; name a separate provider-reported total only if diagnostics need it.
- **Why it is simpler:** Every producer and aggregator shares one arithmetic invariant.
- **Implementation scope:** `llm/types.go`, four translators/streaming paths, token tracking, external backend boundaries, and budget/accounting tests.
- **Smallest credible slice:** Add a finalizer, apply it to all provider translators, and make `Usage.Add` derive rather than trust totals while preserving the field.
- **Regression risks:** Cache-heavy totals and budget timing will decrease; downstream JSON consumers may expect provider-reported totals.
- **Migration concerns:** Do not remove the public field without an approved breaking release.
- **Existing validation:** provider cache, pricing, token-tracker, external-backend, and budget tests.
- **Additional validation required:** Shared cached/uncached/reasoning/malformed/streaming conformance table proving the invariant and unchanged cost.
- **Impact:** high.
- **Confidence:** high.
- **Implementation effort:** small.
- **Blast radius:** cross-subsystem.
- **Prerequisites:** confirm that run token ceilings intentionally count fresh input plus output.
- **Priority:** 6.

### SIFT-SUB-13-02 — Make container cleanup ownership-aware

- **Finding ID:** `SIFT-SUB-13-02`
- **Authoritative subsystem:** SUB-13 — SWE-bench harness
- **Title:** Make container cleanup ownership-aware
- **Verdict:** `recommend`
- **Primary evidence:** `cmd/tracker-swebench/docker.go:28-31` claims concurrent-run isolation, but `main.go:160-168` creates a second-resolution run ID. Containers receive a shared `swebench=<RunLabel>` at `docker.go:242-244`; startup cleanup queries every container with any `swebench` label and force-removes it at `docker.go:201-217`.
- **Interfaces and call sites:** harness startup, container naming/labels, Docker cleanup command.
- **Tests and intent evidence:** Existing tests cover names, not concurrent ownership/status/age cleanup.
- **Current representation:** “Stale” is inferred from possession of a broad label; ownership, state, and age are absent.
- **Current complexity or invalid states:** A second live harness can delete the first harness's active container, and same-second runs may collide in labels.
- **Why it is material:** Concurrent benchmark work can be destroyed by normal startup.
- **Proposed representation:** Use a collision-resistant run identity and labels for harness owner/run/creation time. Remove exited containers and only expired running containers under a documented policy; never all shared-label containers.
- **Why it is simpler:** Cleanup acts on explicit ownership and lifecycle facts rather than a guess.
- **Implementation scope:** SWE main config, Docker helpers, cleanup and tests.
- **Smallest credible slice:** Generate a unique run ID, add owner/run labels, and restrict automatic cleanup to exited containers owned by this harness version.
- **Regression risks:** A too-short stale threshold can still kill valid long runs.
- **Migration concerns:** Legacy containers have only the old label; handle them conservatively or via an explicit manual cleanup path.
- **Existing validation:** container naming and Docker command tests.
- **Additional validation required:** Current-run, concurrent-live, exited, expired, and legacy scenarios.
- **Impact:** high.
- **Confidence:** high.
- **Implementation effort:** small.
- **Blast radius:** subsystem.
- **Prerequisites:** define stale-container policy.
- **Priority:** 7.

### SIFT-SUB-05-02 — Separate gate cancellation from teardown

- **Finding ID:** `SIFT-SUB-05-02`
- **Authoritative subsystem:** SUB-05 — Handlers and backend registry
- **Title:** Separate gate-scoped timeout cancellation from run-wide interviewer teardown
- **Verdict:** `recommend`
- **Primary evidence:** `pipeline/handlers/human.go:18-76,294-317,440-450,700-739` invokes optional `Cancel()` when one gate times out. `transport/chatops/interviewer.go:35-46,76-80,184-208` implements `Cancel()` by permanently closing the run-wide channel. Webhook cancellation closes all pending gates and the server at `pipeline/handlers/webhook_interviewer.go:482`.
- **Interfaces and call sites:** Human interviewer extension interfaces, `ThreadInterviewer`, webhook/TUI implementations, `Engine.Close` teardown.
- **Tests and intent evidence:** `transport/conformance/conformance.go:29-32,262-273` and `docs/architecture/transport-boundary.md:91-99` define cancel as run-wide; tests cover teardown, not a later or sibling gate after one timeout.
- **Current representation:** One optional method owns both a gate-call lifetime and the interviewer's run lifetime.
- **Current complexity or invalid states:** Timing out gate A permanently cancels later gates and concurrent sibling gates.
- **Why it is material:** A local timeout deterministically corrupts the rest of a run's human-gate behavior.
- **Proposed representation:** Carry a gate-call context through the interviewer contract and reserve close/cancel for run teardown.
- **Why it is simpler:** Gate lifetime and run lifetime each have one cancellation source.
- **Implementation scope:** Human interfaces/wrappers, interviewer implementations, conformance suite, tests, and boundary docs.
- **Smallest credible slice:** Add a context-aware gate call for the chat interviewer and human handler, with a deliberate migration plan for the other implementations.
- **Regression risks:** Answer-versus-timeout races, partial interviews, and goroutine cleanup.
- **Migration concerns:** Avoid an indefinite optional dual interface; this public contract change needs an agreed compatibility boundary.
- **Existing validation:** teardown and timeout tests.
- **Additional validation required:** Timeout then later answer, concurrent sibling survival, run teardown of current/future gates, and race-enabled cleanup tests.
- **Impact:** high.
- **Confidence:** high.
- **Implementation effort:** medium.
- **Blast radius:** cross-subsystem.
- **Prerequisites:** choose the interviewer contract migration.
- **Priority:** 8.

### SIFT-SUB-11-01 — Buffer chat event delivery

- **Finding ID:** `SIFT-SUB-11-01`
- **Authoritative subsystem:** SUB-11 — Chat transports and frontends
- **Title:** Put chat rendering behind the existing bounded event queue
- **Verdict:** `recommend`
- **Primary evidence:** `transport/chatops/runner.go:225-240` installs notifier/status rendering directly as `Config.EventHandler`. `notify.go:35-63` and `status.go:68-86` call UI methods synchronously; Slack implementations perform network calls at `cmd/trackerbot/slack.go:233-292`.
- **Interfaces and call sites:** `Runner`, `ThreadUI`, notifier/status composition, `pipeline.BufferedPipelineHandler` at `pipeline/events_buffered.go:255-314`.
- **Tests and intent evidence:** `docs/architecture/transport-boundary.md:161-190` says handlers run on the engine goroutine and network subscribers must be buffered.
- **Current representation:** Throttling controls call frequency but the engine still owns network latency.
- **Current complexity or invalid states:** Slow Slack calls stall pipeline progress and consume a `RunManager` slot; forced start and terminal updates always use this path.
- **Why it is material:** An external network sink can halt unrelated execution work.
- **Proposed representation:** Have `Runner` own one bounded buffered handler around the composed chat sink, flush/close after run completion, and report drops.
- **Why it is simpler:** Reuse the queue already designed for this boundary rather than adding transport-specific concurrency.
- **Implementation scope:** chat runner and notifier/status runner tests.
- **Smallest credible slice:** Wrap the composed sink with `OverflowDropOldest`, guarantee terminal/gate handling, and close on admission failure and completion.
- **Regression risks:** Async ordering, deliberate progress drops, blocked final flush, and goroutine leaks.
- **Migration concerns:** Outbound client timeouts remain necessary; do not treat buffering as a cure for a permanently blocked HTTP call.
- **Existing validation:** notifier, status, delivery, runner, and buffered-handler tests.
- **Additional validation required:** Blocking fake UI, terminal flush/order, overflow count, and failed-start cleanup.
- **Impact:** high.
- **Confidence:** high.
- **Implementation effort:** small.
- **Blast radius:** subsystem.
- **Prerequisites:** none.
- **Priority:** 9.

### SIFT-SUB-07-02 — Use an explicit session stop reason

- **Finding ID:** `SIFT-SUB-07-02`
- **Authoritative subsystem:** SUB-07 — Agent session runtime
- **Title:** Replace boolean stop protocols with explicit turn and session dispositions
- **Verdict:** `recommend`
- **Primary evidence:** `agent/session.go:242-410` returns nested boolean tuples while mutating result flags. `agent/result.go:25-48` exposes independent `MaxTurnsUsed`, `LoopDetected`, `NodeCostExceeded`, `NoProgressDetected`, `BreachVerify`, and `Error`. `pipeline/handlers/codergen.go:490-516,780-863` reimplements their precedence.
- **Interfaces and call sites:** Session turn loop, `SessionResult`, codergen mapping, external backend result construction.
- **Tests and intent evidence:** `agent/loop_detection_test.go:37-68` requires two booleans for one outcome. `agent/session.go:343-375` uses session-total tool calls to classify a current empty response; tests cover first-turn emptiness but not emptiness after earlier tool work. `docs/architecture/agent.md:92-99,235-240` says empty responses fail loudly.
- **Current representation:** Turn flow and final outcome are encoded by boolean combinations and hidden precedence.
- **Current complexity or invalid states:** Contradictory result states are representable, and a later empty provider response can be accepted after any earlier tool call.
- **Why it is material:** A real failure can become success, while every consumer must duplicate outcome precedence.
- **Proposed representation:** Return one internal `TurnDisposition` from turn classification and expose one `SessionStopReason` in `SessionResult`; attach breach verification only to turn-limit termination.
- **Why it is simpler:** Each stop has one name and one consumer switch.
- **Implementation scope:** agent session/loop/run/result files, codergen mapping, external backend mapping, and tests.
- **Smallest credible slice:** Classify each response once and add an authoritative stop reason while retaining old public booleans only if an explicit compatibility plan approves the temporary duplication.
- **Regression risks:** Loop-as-breach routing, terminal tools, truncated responses, and external backend mappings.
- **Migration concerns:** Removing public fields is breaking and needs an approved release boundary; do not leave two authorities indefinitely.
- **Existing validation:** session, loop, guard, truncation, breach, and codergen outcome suites.
- **Additional validation required:** A disposition table for text/tools/truncation/empty-before-and-after-tools/terminal tools/guards/provider errors and valid breach combinations.
- **Impact:** high.
- **Confidence:** high.
- **Implementation effort:** medium.
- **Blast radius:** cross-subsystem.
- **Prerequisites:** public `SessionResult` migration decision.
- **Priority:** 10.

### SIFT-SUB-04-02 — Model goal-gate state explicitly

- **Finding ID:** `SIFT-SUB-04-02`
- **Authoritative subsystem:** SUB-04 — Pipeline engine and persistent run state
- **Title:** Replace scattered goal-gate flags with a checkpoint state machine
- **Verdict:** `recommend`
- **Primary evidence:** `pipeline/checkpoint.go:28-59` distributes gate state across `NodeOutcomes`, `FallbackTaken`, `GateRecheckPending`, and `OverriddenGates`. Discovery and branching span `engine_checkpoint.go:184-265`; clearing and transitions span `engine_run.go:742-766,989-1053`.
- **Interfaces and call sites:** checkpoint serialization, exit-time gate checks, retry/fallback redirects, override handling, resume.
- **Tests and intent evidence:** `engine_goal_gate_recheck_test.go:55-62,261-268,371-377` records past unbounded/recheck defects; `engine_goal_gate_override_test.go:288-300` permits pending plus overridden state.
- **Current representation:** One gate's phase is inferred from several maps whose combinations have order-dependent meaning.
- **Current complexity or invalid states:** Contradictory pending/fallback/override states are representable and transition rules are spread across engine paths.
- **Why it is material:** This is safety-critical validation routing with repeated regressions and resume semantics.
- **Proposed representation:** Persist one per-gate enum/record with explicit phases and central transition methods; keep the last outcome as data on that record where needed.
- **Why it is simpler:** Impossible flag combinations disappear and transition validation moves to one boundary.
- **Implementation scope:** checkpoint schema/helpers, goal-gate selection/redirect/override execution, migration, and tests.
- **Smallest credible slice:** Add typed state plus transition helpers for pending/rechecked/overridden while preserving existing wire fields only during an approved migration.
- **Regression risks:** Retry charging, one-shot fallback, override precedence, and resume ordering.
- **Migration concerns:** Old checkpoints need deterministic conversion; ambiguous combinations should fail clearly.
- **Existing validation:** extensive goal-gate recheck, override, retry, and resume tests.
- **Additional validation required:** Exhaustive transition table, invalid-combination rejection, and old-checkpoint fixtures.
- **Impact:** high.
- **Confidence:** high.
- **Implementation effort:** medium.
- **Blast radius:** subsystem.
- **Prerequisites:** checkpoint migration policy.
- **Priority:** 11.

### SIFT-SUB-04-01 — Budget restarts per loop

- **Finding ID:** `SIFT-SUB-04-01`
- **Authoritative subsystem:** SUB-04 — Pipeline engine and persistent run state
- **Title:** Track restart budgets per resolved loop target
- **Verdict:** `recommend`
- **Primary evidence:** `pipeline/checkpoint.go:13-26` stores one scalar `RestartCount`; `pipeline/engine_run.go:1095-1143` checks and increments it for every loop-back.
- **Interfaces and call sites:** restart target resolution, checkpoint JSON, decision events, budget diagnostics.
- **Tests and intent evidence:** `docs/architecture/engine.md:358-373` documents that early loops consume later loops' budget and that `build_product.dip` uses file counters as a workaround. `engine_restart_test.go:353-449,564-587` pins global behavior.
- **Current representation:** All loop sites share one run-wide counter.
- **Current complexity or invalid states:** Independent milestone loops interfere, so the configured ceiling does not mean “attempts for this loop.”
- **Why it is material:** Legitimate later recovery can fail because unrelated earlier work spent its budget.
- **Proposed representation:** Persist restart counts keyed by the resolved restart target; derive an aggregate count only for existing manifests/events if needed.
- **Why it is simpler:** Each loop's configuration governs its own state and workflow-local counter files become unnecessary.
- **Implementation scope:** checkpoint schema/helpers, restart handling, event payloads/docs, workflow workaround cleanup, and tests.
- **Smallest credible slice:** Add `RestartCounts[target]`, use it for enforcement, and retain scalar aggregate output during migration.
- **Regression risks:** Nested loops sharing a target and changed failure timing.
- **Migration concerns:** A legacy scalar cannot be attributed to a loop; define conservative reset or explicit refusal.
- **Existing validation:** restart and resume tests plus documented workaround.
- **Additional validation required:** Two independent loops, nested/shared-target loops, resume, and legacy checkpoint fixtures.
- **Impact:** high.
- **Confidence:** high.
- **Implementation effort:** medium.
- **Blast radius:** subsystem.
- **Prerequisites:** legacy checkpoint policy.
- **Priority:** 12.

### SIFT-SUB-10-01 — Consume terminal status once

- **Finding ID:** `SIFT-SUB-10-01`
- **Authoritative subsystem:** SUB-10 — Terminal UI
- **Title:** Consume authoritative terminal events instead of rebuilding terminal state
- **Verdict:** `recommend`
- **Primary evidence:** `pipeline/events.go:320-364` defines `PipelineEvent.TerminalStatus` as authoritative. Producers populate it in `pipeline/engine.go:300-322`, `pipeline/terminal_emit.go:49-127`, and `pipeline/pause.go:64-82`. `tui/adapter.go:13-107` ignores it, omits budget/pause cases, and reconstructs override status; `tui/state.go:67-90,375-405` reconstructs it again.
- **Interfaces and call sites:** TUI adapter/messages/store/status bar and CLI wiring at `cmd/tracker/run.go:625-637`.
- **Tests and intent evidence:** Adapter/state tests pin both reconstruction paths and omit budget/pause terminal events.
- **Current representation:** Pipeline event, stateful adapter, and store each classify completion.
- **Current complexity or invalid states:** Done, error, status, override list, and headline can disagree; live budget and billing-pause terminals are dropped.
- **Why it is material:** The live UI can miss or mislabel how a run ended.
- **Proposed representation:** Emit one `MsgPipelineTerminated` from non-empty authoritative status and store one optional terminal record. Keep override history only as display detail.
- **Why it is simpler:** One state transition replaces three classifiers and independent booleans.
- **Implementation scope:** TUI adapter/messages/store/status bar/tests and CLI wiring.
- **Smallest credible slice:** Pass root-scoped terminal events through the adapter and delete status inference while retaining override detail rendering.
- **Regression risks:** Child scoped terminal events, malformed custom events, and ordering of override details.
- **Migration concerns:** none outside TUI message tests.
- **Existing validation:** lifecycle/status-bar/override tests.
- **Additional validation required:** Every terminal status end-to-end, child-scoped events, malformed empty status, latest override, and exactly one terminal transition.
- **Impact:** high.
- **Confidence:** high.
- **Implementation effort:** medium.
- **Blast radius:** subsystem.
- **Prerequisites:** preserve the documented unscoped-root rule.
- **Priority:** 13.

### SIFT-SUB-09-02 — Equalize traced and untraced completion

- **Finding ID:** `SIFT-SUB-09-02`
- **Authoritative subsystem:** SUB-09 — LLM client and providers
- **Title:** Make tracing observe one canonical completion result
- **Verdict:** `recommend`
- **Primary evidence:** `llm/client.go:227-307` calls `Complete` without observers and `Stream` with observers. `llm/stream.go:38-50,90-113,226-264` exposes `FullResponse` but the accumulator retains only content, finish reason, and usage. `llm/types.go:303-315` also requires ID, raw, warnings, rate limits, and returned model. Streaming status paths omit `Retry-After`; e.g. `llm/openai/adapter.go:122-124` versus `178-181`.
- **Interfaces and call sites:** `ProviderAdapter`, `Client.Complete`, `StreamEvent`, `StreamAccumulator`, all providers, agent request observers at `agent/session_run.go:75,154`.
- **Tests and intent evidence:** Agent calls always trace, so production takes the lossy path. Trace tests assert observer delivery, not response/error parity.
- **Current representation:** Observability selects a second transport/translation path and reconstructs a smaller response.
- **Current complexity or invalid states:** Adding an observer changes response metadata and retry behavior.
- **Why it is material:** Production agent sessions can lose provider model/ID/raw/warnings/rate limits and retry hints solely because tracing is enabled.
- **Proposed representation:** Make one completion result authoritative and tee trace events without changing it. Stage this by carrying complete response metadata and retry hints through the stream path provider by provider.
- **Why it is simpler:** Tracing becomes an observer, not a protocol switch.
- **Implementation scope:** client/stream contracts, providers, error mapping, tests, and docs.
- **Smallest credible slice:** For one provider, populate/use `FullResponse`, retain every response field in the accumulator, preserve `Retry-After`, and assert traced/untraced parity. Repeat before considering removal of public `ProviderAdapter.Complete`.
- **Regression risks:** IDs/models/raw payloads, typed errors, idle deadlines, latency, event order, and custom adapters.
- **Migration concerns:** Removing a public adapter method is a later breaking decision; do not create an indefinite dual authority.
- **Existing validation:** provider stream/complete, SSE, accumulator, retry, trace, and idle tests.
- **Additional validation required:** Per-provider field-by-field response and typed-error parity with zero/one observer.
- **Impact:** high.
- **Confidence:** high.
- **Implementation effort:** large.
- **Blast radius:** application-wide.
- **Prerequisites:** staged provider migration; public interface decision only after parity lands.
- **Priority:** 14.

### SIFT-SUB-03-02 — Prepare an immutable execution graph

- **Finding ID:** `SIFT-SUB-03-02`
- **Authoritative subsystem:** SUB-03 — Workflow schema, parsing, expansion, and validation
- **Title:** Add one graph finalization boundary before execution
- **Verdict:** `recommend`
- **Primary evidence:** `pipeline/graph.go:28-74,140-196` exports mutable nodes/edges and keeps private adjacency indexes updated only by `AddEdge`. `pipeline/validate.go:115-177` trusts caller-visible `DippinValidated` to skip structural checks. `pipeline/expand.go:347-381` manually synchronizes graph state.
- **Interfaces and call sites:** `NewEngineFromGraph` at `tracker.go:279-318`, simulation, expansion, Dippin/DOT loaders, public graph construction.
- **Tests and intent evidence:** `pipeline/graph_test.go:214-234` covers nil indexes, not stale ones. `pipeline/validate_test.go:362-429` shows invalid graphs pass when the flag is set.
- **Current representation:** Mutable public source fields, derived private indexes, and validation provenance coexist in one object.
- **Current complexity or invalid states:** Direct edge mutation leaves stale indexes; post-validation mutation can retain a flag that suppresses checks.
- **Why it is material:** Execution and simulation may see different graph topology or accept structurally invalid post-parse graphs.
- **Proposed representation:** At engine construction, deep-clone the caller graph, rebuild derived indexes from `Edges`, apply defaults, and run a defined set of tracker-owned final invariants. Treat loader provenance as diagnostic input, not permission to skip final invariants.
- **Why it is simpler:** Execution consumes one prepared snapshot whose derived data matches its source.
- **Implementation scope:** graph clone/index helpers, engine-construction seam, final validation split, and tests. Hiding public fields is deferred.
- **Smallest credible slice:** Prepare only at `NewEngineFromGraph`; test direct `Edges` and post-Dippin mutation. Defer simulation migration and public field removal.
- **Regression risks:** Pointer identity, post-construction mutation expectations, shallow attribute copies, and newly rejected graphs.
- **Migration concerns:** Define source-independent invariants without rerunning Dippin-owned lint and diagnostics.
- **Existing validation:** graph, validation, expansion, parser, and engine construction tests.
- **Additional validation required:** Clone isolation, stale-index repair, mutation after Dippin validation, and unchanged diagnostics.
- **Impact:** high.
- **Confidence:** medium — the invalid states are proven, but caller mutation expectations need confirmation.
- **Implementation effort:** medium.
- **Blast radius:** cross-subsystem.
- **Prerequisites:** define tracker-owned final invariants.
- **Priority:** 15.

### SIFT-SUB-03-01 — Compile conditions once

- **Finding ID:** `SIFT-SUB-03-01`
- **Authoritative subsystem:** SUB-03 — Workflow schema, parsing, expansion, and validation
- **Title:** Keep one parsed condition model from load through routing
- **Verdict:** `recommend`
- **Primary evidence:** `pipeline/graph.go:242-258` stores both `Edge.Condition` and `Attrs["condition"]`. `pipeline/dippin_adapter.go:594-719,844-883` converts typed conditions to text and rejects parenthesized parsed trees, while raw equivalents can pass. Validation, lint, variable analysis, runtime evaluation, and manager-loop checks each parse or split condition text independently (`validate_semantic.go:66-171`, `validate_vars_refs.go:80-149`, `lint_tracker.go:104-136,330-347`, `condition.go:23-131`).
- **Interfaces and call sites:** Dippin IR adapter, graph edge, graph preparation, edge selection and manager-loop evaluation.
- **Tests and intent evidence:** `pipeline/dippin_adapter_manager_loop_test.go:695-760` rejects a mixed-precedence parsed tree while accepting equivalent raw parenthesized text.
- **Current representation:** Typed parser output is serialized to a limited string dialect, then several consumers reparsed it with different rules.
- **Current complexity or invalid states:** Equivalent conditions have different acceptance and semantics based on whether `Raw` or `Parsed` happened to be populated.
- **Why it is material:** Routing logic can reject or misread valid workflow intent and every consumer maintains another parser.
- **Proposed representation:** Compile one tracker-owned typed condition AST during graph preparation; retain raw text only for diagnostics/audit and let validation/lint/runtime read the same tree.
- **Why it is simpler:** One parser and one precedence model replace string mirrors and repeated split logic.
- **Implementation scope:** graph edge/preparation, Dippin adapter, condition evaluator, semantic/variable/lint consumers, manager loop, and tests.
- **Smallest credible slice:** Define/compile the AST for edge conditions in the prepared graph and switch runtime evaluation plus syntax validation; migrate lint/manager-loop consumers afterward.
- **Regression risks:** Exact coercion, quoting, `not`, precedence, diagnostics, DOT inputs, and serialized audit text.
- **Migration concerns:** Preserve raw condition strings on public/reporting surfaces.
- **Existing validation:** condition evaluator, adapter, semantic validation, variable reference, lint, engine-edge, and manager-loop suites.
- **Additional validation required:** Shared truth table for raw/parsed/DOT conditions and mixed precedence across all consumers.
- **Impact:** high.
- **Confidence:** high.
- **Implementation effort:** medium.
- **Blast radius:** cross-subsystem.
- **Prerequisites:** `SIFT-SUB-03-02` graph preparation boundary.
- **Priority:** 16.

### SIFT-SUB-13-01 — Separate benchmark deadline from watchdog

- **Finding ID:** `SIFT-SUB-13-01`
- **Authoritative subsystem:** SUB-13 — SWE-bench harness
- **Title:** Give the agent timeout one semantic owner and a separate watchdog grace
- **Verdict:** `recommend`
- **Primary evidence:** The same flag becomes `DockerRunner.Timeout` at `cmd/tracker-swebench/main.go:160-164` and `SWEBENCH_TIMEOUT` at lines 268-274. Host `docker exec` uses it at `docker.go:290-303`; the container applies the equal deadline to `Session.Run` at `agent-runner/main.go:198-203`, then can emit its required summary only at lines 205-224.
- **Interfaces and call sites:** harness config/env, Docker runner, agent-runner summary and host parser.
- **Tests and intent evidence:** The design requires the last output line to be the summary at `docs/superpowers/specs/2026-04-16-swebench-harness-design.md:89-93,315-316`.
- **Current representation:** Parent and child own equal timers around the same work.
- **Current complexity or invalid states:** The host can kill Docker before the child records timeout reason, turns, or usage.
- **Why it is material:** Timeout runs lose the benchmark data needed to classify them.
- **Proposed representation:** Let the child own the benchmark deadline; make the host a watchdog at deadline plus bounded reporting/teardown grace with a distinct classification.
- **Why it is simpler:** Deadline and stuck-process protection have different names and budgets.
- **Implementation scope:** SWE config, Docker runner, agent-runner, summary classification, and tests.
- **Smallest credible slice:** Add fixed watchdog grace and distinguish child deadline from watchdog expiry.
- **Regression risks:** Parent cancellation latency and changed benchmark duration semantics.
- **Migration concerns:** Reports may gain a watchdog failure class.
- **Existing validation:** timeout classification and summary parsing tests.
- **Additional validation required:** Child-deadline summary before watchdog, hung child watchdog, and immediate parent cancellation.
- **Impact:** high.
- **Confidence:** high.
- **Implementation effort:** small.
- **Blast radius:** subsystem.
- **Prerequisites:** ownership labels from `SIFT-SUB-13-02` are useful but not required for timer semantics.
- **Priority:** 17.

### SIFT-SUB-12-01 — Use the production client in conformance

- **Finding ID:** `SIFT-SUB-12-01`
- **Authoritative subsystem:** SUB-12 — Conformance binary and suite
- **Title:** Use the shipped LLM client constructor for live conformance commands
- **Verdict:** `recommend`
- **Primary evidence:** `cmd/tracker-conformance/main.go:201-204,1118-1143` builds a private three-provider client with only direct base URLs. Live commands use it at lines 323, 354, 391, 423, 458, 502, 770, and 981. `tracker.NewLLMClient` at `tracker.go:640-687` and `tracker_client.go:104-159` supports four providers, strict gateway routing, and retry middleware.
- **Interfaces and call sites:** live conformance commands, `client-from-env`, public root client, controlled `test_endpoint` seam.
- **Tests and intent evidence:** `main_test.go:96-160` pins only three providers; `docs/architecture/llm.md:203-234` calls the root resolver canonical. Golden generation already uses the root seam.
- **Current representation:** The release-shipped conformance tool maintains a stale production-client copy.
- **Current complexity or invalid states:** It can ignore gateway variables, reject OpenAI-compatible configuration, and send to an SDK default instead of the fail-closed route.
- **Why it is material:** A conformance result may describe a different client than the product ships.
- **Proposed representation:** Use `tracker.NewLLMClient` only for commands whose contract is production parity; retain explicit raw-provider/test-endpoint paths where retries or injection are the test subject.
- **Why it is simpler:** Production parity comes from the production constructor without erasing conformance-specific seams.
- **Implementation scope:** conformance main/tests/imports and environment reporting.
- **Smallest credible slice:** Migrate one live completion command plus `client-from-env`; add OpenAI-compatible and gateway refusal coverage before migrating the rest.
- **Regression risks:** Retry behavior can hide raw transient failures; ambient gateway/env values can affect tests.
- **Migration concerns:** Preserve controlled endpoint injection and document which commands test raw adapters.
- **Existing validation:** conformance unit tests and golden traces.
- **Additional validation required:** OpenAI-compatible detection, gateway suffix/refusal/precedence, retry semantics, and isolated environments.
- **Impact:** medium.
- **Confidence:** high.
- **Implementation effort:** small.
- **Blast radius:** subsystem.
- **Prerequisites:** preserve raw-test seams.
- **Priority:** 18.

### SIFT-SUB-16-02 — Consolidate the Bedrock guide

- **Finding ID:** `SIFT-SUB-16-02`
- **Authoritative subsystem:** SUB-16 — Documentation, website, and historical records
- **Title:** Establish one authoritative Bedrock gateway guide
- **Verdict:** `recommend`
- **Primary evidence:** `docs/bedrock-gateway.md:16-29,44-66` says omit gateway kind, use Cloudflare suffixes/OpenAI-compatible, and avoid OpenAI. `docs/architecture/bedrock-gateway.md:54-115` requires `bedrock`, uses native suffixes/OpenAI, and refuses OpenAI-compatible.
- **Interfaces and call sites:** Architecture index points to the latter at `docs/architecture/README.md:27-28`; deployed `site/content/cli.html:252-254` links the stale root guide. Runtime matches the architecture guide at `tracker.go:690-750` and `tracker_client.go:33-77`.
- **Tests and intent evidence:** Gateway routing/refusal tests at `tracker_client_test.go:9-35` and `tracker_test.go:700-884`.
- **Current representation:** Two live-looking operator guides describe incompatible provider matrices and URL rules.
- **Current complexity or invalid states:** Readers can configure the wrong gateway kind/provider and receive behavior opposite to the guide they followed.
- **Why it is material:** This is operational routing and credential guidance, not cosmetic prose drift.
- **Proposed representation:** Make the architecture guide the authority, merge unique valid troubleshooting, update current links, and remove or reduce the root page to a relocation notice.
- **Why it is simpler:** One provider/routing matrix follows the runtime source of truth.
- **Implementation scope:** Both guide paths and current site links.
- **Smallest credible slice:** Fix the site link and replace contradictory root instructions with a short pointer; merge unique material later.
- **Regression risks:** External links and loss of valid troubleshooting.
- **Migration concerns:** A relocation notice may be needed for the old URL.
- **Existing validation:** routing unit tests and docs gate/link mechanisms.
- **Additional validation required:** Link/build preview and manual matrix review against current gateway behavior.
- **Impact:** high.
- **Confidence:** high.
- **Implementation effort:** small.
- **Blast radius:** subsystem.
- **Prerequisites:** confirm gateway-side capability claims when editing.
- **Priority:** 19.

### SIFT-SUB-10-02 — Make search own the viewport

- **Finding ID:** `SIFT-SUB-10-02`
- **Authoritative subsystem:** SUB-10 — Terminal UI
- **Title:** Make search selection the single authority for the log viewport
- **Verdict:** `recommend`
- **Primary evidence:** `tui/app.go:198-214` advances search; `tui/search.go:98-123` stores the current match. `tui/agentlog.go:590-615` always renders backward from the tail and never reads it. `AgentLog.scroll` and `tui/scrollview.go:5-108` have no production consumer for visible bounds.
- **Interfaces and call sites:** search bar, agent log rendering, app key handling, unused scroll view.
- **Tests and intent evidence:** `docs/architecture/tui.md:151-153,181-186` promises navigation. Search tests verify the isolated index; integration checks only match count.
- **Current representation:** Selected match, rendered window, and scroll offset are disconnected.
- **Current complexity or invalid states:** `n`/`N` can change internal state without moving an off-screen match into view.
- **Why it is material:** A documented interactive feature does not work for the case that needs navigation.
- **Proposed representation:** When a term exists, derive the rendered window from `CurrentMatchLine`; otherwise tail-follow. Remove the unused scroll model if no consumer remains.
- **Why it is simpler:** One selected index owns one window.
- **Implementation scope:** agent log, search/app integration, scroll helper and tests.
- **Smallest credible slice:** Make current match visible in a short viewport and delete only the unused `AgentLog.scroll` field; remove the helper after reference confirmation.
- **Regression risks:** Wrapped rows, 10,000-line trim, filtering, focus, resize, separators, and streaming partials.
- **Migration concerns:** none.
- **Existing validation:** isolated search, Unicode highlight, verbosity, and tail viewport tests.
- **Additional validation required:** Beginning/middle/end match visibility, wraparound, growth/trim/filter/resize, and return to tail mode.
- **Impact:** medium.
- **Confidence:** high.
- **Implementation effort:** small.
- **Blast radius:** local.
- **Prerequisites:** none.
- **Priority:** 20.

### SIFT-SUB-06-01 — Centralize CLI command metadata

- **Finding ID:** `SIFT-SUB-06-01`
- **Authoritative subsystem:** SUB-06 — Main CLI
- **Title:** Give public commands one metadata authority
- **Verdict:** `recommend`
- **Primary evidence:** Modes live at `cmd/tracker/main.go:73-91`, aliases at `flags.go:29-47`, parser classes at `flags.go:62-80`, dispatch at `commands.go:43-100`, and help at `usage.go:10-31`.
- **Interfaces and call sites:** subcommand parsing, specialized flag parsers, explicit execution functions, help, website docs gate.
- **Tests and intent evidence:** `run-json` is parsed/dispatched and documented on the site but absent from CLI help. `scripts/docs/gate.sh:17-37` records earlier undocumented `verify-tests` and `status` drift.
- **Current representation:** A public command requires coordinated edits to several unrelated declarations.
- **Current complexity or invalid states:** Commands can execute while missing from local help or using the wrong parser class.
- **Why it is material:** The demonstrated drift has recurred on the user-facing CLI.
- **Proposed representation:** Use one small table for canonical name, aliases, parser class, and help synopsis; derive `subcommandMap`, parser grouping, usage rows, and docs-gate input. Keep execution functions and dispatch switches explicit.
- **Why it is simpler:** Metadata has one source without hiding control flow behind function-valued descriptors.
- **Implementation scope:** main/flags/usage/docs gate and tests; dispatch stays explicit.
- **Smallest credible slice:** Derive parser map and help from metadata, including `run-json`; leave execution switches unchanged.
- **Regression risks:** `__jail-exec`, help flags, `--version`, `list`, and positional syntax are special cases.
- **Migration concerns:** none on public syntax if table data matches current behavior.
- **Existing validation:** flag, mode, routing, usage, and docs-gate tests.
- **Additional validation required:** Registry completeness and rendered-help coverage for every public mode/alias.
- **Impact:** medium.
- **Confidence:** high.
- **Implementation effort:** small.
- **Blast radius:** local.
- **Prerequisites:** none.
- **Priority:** 21.

### SIFT-SUB-02-01 — Generate the reader-side activity schema

- **Finding ID:** `SIFT-SUB-02-01`
- **Authoritative subsystem:** SUB-02 — Events, capture, artifacts, and forensics
- **Title:** Generate reader-side activity types and copy code from one schema
- **Verdict:** `recommend`
- **Primary evidence:** Flat field authorities repeat across `tracker_events.go:41-183`, `tracker_activity.go:149-274`, `tracker_activity_payload.go:20-236`, and `pipeline/events_jsonl_entry.go:11-279`; copy logic also repeats in `tracker_events_payload.go` and the pipeline writer.
- **Interfaces and call sites:** public `StreamEvent`/`ActivityEntry`, private reader/writer shapes, parse/replay/live NDJSON consumers.
- **Tests and intent evidence:** `tracker_events_parity_test.go:16-130` parses a private source file with Go AST to compare tags; `tracker_activity_parity_test.go:16-197` enforces three-way parity and populates every field. `tracker_events_test.go:334-370` documents a declared-but-unpopulated defect class.
- **Current representation:** Four structs and several copier functions encode one mostly shared flat payload.
- **Current complexity or invalid states:** Each new field requires coordinated declarations, tags, copy sites, and exemptions; current tests compensate by parsing source rather than removing the duplicated authority.
- **Why it is material:** The repository has already built extensive mechanical guards around recurring loss/drift risk.
- **Proposed representation:** Introduce a checked-in schema/generator for `activityRawLine`, `ActivityEntry`, and their copier first. Preserve public names/types, timestamps, omission rules, unknown-field behavior, and existing parity tests. Defer generation of public `StreamEvent` and the pipeline writer.
- **Why it is simpler:** The first slice removes reader declaration/copy duplication without forcing all event domains into one type.
- **Implementation scope:** schema/generator, reader-side generated files, generation gate, parity/API/wire fixtures.
- **Smallest credible slice:** Generate only `activityRawLine`, `ActivityEntry`, and `toEntry` helpers; keep writer/live types handwritten and checked by parity tests.
- **Regression risks:** Type/tag/`omitempty` drift, wire-only snapshots, timestamp differences, reflection/API identity, and non-reproducible generation.
- **Migration concerns:** Checked-in output must be byte-stable; no public field may move or change type in this slice.
- **Existing validation:** parity, payload, round-trip, API-surface, and wire-shape tests.
- **Additional validation required:** Generator freshness gate plus byte-level JSON and unknown-field fixtures.
- **Impact:** medium.
- **Confidence:** medium — the drift is proven; generator ownership and payoff need confirmation in implementation.
- **Implementation effort:** medium.
- **Blast radius:** cross-subsystem.
- **Prerequisites:** choose schema and generator ownership.
- **Priority:** 22.

### SIFT-SUB-14-01 — Make DIP the shipped example authority

- **Finding ID:** `SIFT-SUB-14-01`
- **Authoritative subsystem:** SUB-14 — Shipped workflows and example assets
- **Title:** Make DIP the sole authority for shipped workflow examples
- **Verdict:** `recommend`
- **Primary evidence:** `tracker.go:23-27` calls DIP current and DOT deprecated. Seventeen examples have same-basename DIP/DOT definitions. `examples/sprint_exec.dip:67-72` uses Sonnet for `ReviewClaude`; `examples/sprint_exec.dot:75-81` uses Opus. `pipeline/handlers/integration_test.go:13-24` still consumes the stale DOT file.
- **Interfaces and call sites:** filesystem example paths, handler integration test, catalog at `tracker_workflows.go:18-22`, explicit resolver compatibility at `tracker_resolve.go:15-23,85-89`.
- **Tests and intent evidence:** The built-in catalog embeds DIP only. DOT parser compatibility has dedicated parser tests and need not rely on a product workflow.
- **Current representation:** A named example can have two independently editable execution definitions selected by suffix.
- **Current complexity or invalid states:** Models, prompts, commands, and behavior drift while a test endorses the deprecated copy.
- **Why it is material:** Shipped examples are executable product guidance; two authorities can produce different work and cost.
- **Proposed representation:** Keep one DIP source per shipped example. Preserve public DOT parser support in small dedicated fixtures. Treat the DOT-only machine-specific `glimpser-port.dot` as a separate product decision.
- **Why it is simpler:** Each example name has one executable definition while format compatibility remains tested directly.
- **Implementation scope:** paired example DOT files, links/tests that name them, and dedicated DOT fixtures. Public parser support is out of scope.
- **Smallest credible slice:** Migrate the integration test to `sprint_exec.dip`, verify its expectations, and remove only `sprint_exec.dot`; defer the other 16 pairs pending path/link review.
- **Regression risks:** Direct links/automation may use example DOT paths; the DIP graph already differs from the DOT graph.
- **Migration concerns:** Deletion requires explicit approval, link search, and release notes. Decide separately whether to migrate, move, or remove `glimpser-port.dot` and its absolute path.
- **Existing validation:** example catalog/resolver/parser/integration tests.
- **Additional validation required:** Parse/validate retained DIP, converted integration test, generic DOT fixtures, and an inventory check rejecting new paired authorities.
- **Impact:** medium.
- **Confidence:** high.
- **Implementation effort:** small for first slice.
- **Blast radius:** subsystem plus one handler test.
- **Prerequisites:** example-path compatibility decision.
- **Priority:** 23.

### SIFT-SUB-16-01 — Retire the stale website copy

- **Finding ID:** `SIFT-SUB-16-01`
- **Authoritative subsystem:** SUB-16 — Documentation, website, and historical records
- **Title:** Retire the undeployed `docs/site/**` website copy
- **Verdict:** `recommend`
- **Primary evidence:** `CLAUDE.md:321-327` declares `site/**` the source; `.github/workflows/docs.yml:3-8,31-40` watches/builds/deploys only it. `docs/site/changelog.html:33-38` stops at v0.14.0 while the live site reaches current releases.
- **Interfaces and call sites:** six tracked `docs/site/**` files, Hugo source/deployment, tracked reference graph.
- **Tests and intent evidence:** No current tracked surface outside historical material references `docs/site/**`; all corresponding pages differ and the live site has seven more pages.
- **Current representation:** Two directories look editable, but only one deploys.
- **Current complexity or invalid states:** Maintainers/readers can treat obsolete CLI, architecture, workflow, and release HTML as current.
- **Why it is material:** The duplicate is a whole stale user-documentation surface, not a small historical note.
- **Proposed representation:** Keep `site/**` as sole website source and remove `docs/site/**`; use an explicit archive label only if retention has a real requirement.
- **Why it is simpler:** One directory owns deployed web content.
- **Implementation scope:** six legacy files and any external/internal link migration.
- **Smallest credible slice:** Verify no live inbound references, then remove the six-file tree with a release note or relocation notice if required.
- **Regression risks:** External GitHub blob links may break.
- **Migration concerns:** Deletion requires explicit approval under repository rules.
- **Existing validation:** docs deployment workflow and tracked-reference search.
- **Additional validation required:** Link search, docs checks, Hugo build/link validation, and deployment preview.
- **Impact:** medium.
- **Confidence:** high.
- **Implementation effort:** small.
- **Blast radius:** subsystem.
- **Prerequisites:** external-link check.
- **Priority:** 24.

## 4. Smallest credible implementation slices

1. **Release identity:** Add the three GoReleaser ldflags and inspect a produced snapshot binary's `tracker version` output. No runtime code change is needed.
2. **Dippin gate:** Make the existing Make target distinguish valid findings from execution/JSON failure, reject an empty input set, and delete the unused CI pin.
3. **Parallel accounting:** Add `ChildUsage` only to the internal branch message and fold it once into the aggregate outcome; keep `ParallelResult` JSON unchanged.
4. **Resumed progress:** Add a versioned snapshot progress record for cumulative usage/tool calls and loop/no-progress state; prove the two guards continue across one interruption.
5. **Traced completion parity:** Migrate one provider using existing `FullResponse`, preserve retry hints, and add traced/untraced parity tests before considering the public adapter contract.
6. **Prepared graph:** Clone/reindex and run tracker-owned final invariants only at `NewEngineFromGraph`; defer hiding fields and simulation migration.
7. **Event schema:** Generate reader-side types/copiers only; leave public live and writer types handwritten behind existing parity tests.
8. **CLI metadata:** Derive names, aliases, parser class, and help from a table while keeping explicit dispatch functions.
9. **DIP authority:** Convert the one live `sprint_exec.dot` integration consumer, then remove only that mirror after compatibility review.

## 5. Explicit skips

### SUB-01 — Library execution facade

Inspected engine construction, defaults, client/provider resolution, input binding/staging, interviewer selection, public API surface, source/run resolution, workflow catalog hooks, and close behavior. Candidate tagged unions for `Input` and gate strategies would remove representable combinations, but the current precedence/validation is explicit and tests establish the intended public contract. Changing these public shapes without a demonstrated defect would create more migration work than simplification. Provider construction drift is owned by SUB-12 where it is concrete.

### SUB-17 — Managed-run supervision

Inspected state publication, result/error semantics, pause/resume, capacity ordering, same-key admission, cancellation, panic cleanup, workdir isolation, and tests. The code already uses a single open `RunState`, central terminal classification, immutable exported run identity, and careful release-before-publication ordering. No candidate passed the materiality gate. Transport event blocking is owned by SUB-11.

### SUB-18 — Health and static-analysis APIs

Inspected Doctor checks and platform splits, simulation/estimate projections, diagnosis and activity scanners, test-fidelity AST analysis, test-trace helpers, diagnostics, and fixtures. Specialized activity scans intentionally project only data needed for one analysis and often preserve legacy log fields; replacing all of them with a generic query layer would over-abstract. Reader schema drift is owned by SUB-02; graph preparation is owned by SUB-03.

### SUB-19 — Shared test support and metadata

Inspected `internal/dipxtest/**` and residual support assets after assigning every domain fixture to its owning subsystem. These are small test-only helpers with clear callers. No shared abstraction or runtime simplification passed the materiality gate.

## 6. Cross-cutting patterns

- **Authority split creates invalid states.** Graph topology, session outcomes, goal-gate state, token totals, terminal UI state, CLI metadata, release identity, workflow examples, and gateway docs each have more than one writable authority. The recommendations stay local to their domain; this does not justify a repository-wide “state framework.”
- **Hierarchical execution drops accounting at projection boundaries.** Parallel branches lose child usage, resumed sessions lose earlier usage/guards, and providers disagree on total-token arithmetic. These findings sequence from preserving data to normalizing it; they should not be merged into one generic accounting object.
- **Lifecycle scope is often implicit.** Per-gate timeout versus run teardown, benchmark deadline versus watchdog, and engine event delivery versus network latency each need named ownership boundaries.
- **Compatibility must be deliberate.** Public `SessionResult`, `ProviderAdapter`, graph mutation, checkpoint JSON, example paths, and documentation URLs cannot be silently dual-maintained. Temporary compatibility paths need explicit approval and a removal point.

## 7. Rejected, merged, and superseded candidates

| Candidate | Disposition | Reason | Authoritative finding/subsystem |
|---|---|---|---|
| Gate cancellation as a transport finding | superseded | The handler timeout wrapper defines the flawed contract; chat/webhook implementations prove the consequence | `SIFT-SUB-05-02`; SUB-11 is evidence |
| Deduplicate 361 byte-identical prompt/command assets | rejected | Hash equality does not prove shared semantic ownership. History deliberately externalized assets per workflow, and one-to-one paths support independent packaging/future divergence | SUB-14 |
| Remove public DOT support with example mirrors | rejected | Deprecated example copies are a content-authority problem; public format compatibility has its own declared removal boundary | `SIFT-SUB-14-01` |
| Centralize SWE-bench client construction | rejected | The harness intentionally supports Anthropic/OpenAI only and adds required `CF_AIG_TOKEN` headers absent from `tracker.NewLLMClient` | SUB-13 |
| Public `Config` gate strategy union | demoted | The precedence is explicit and tested; the concrete lifecycle failure belongs to gate-scoped cancellation | `SIFT-SUB-05-02` |
| Public `Input` tagged source union | rejected | It would improve type shape but no conflicting-representation defect was established; public migration cost exceeds current evidence | SUB-01 |
| Replace all diagnosis activity scans with one generic reader | merged | Specialized projections include intentional legacy fields; only repeated reader schema/copier authority passed the gate | `SIFT-SUB-02-01` |
| Generate every live/writer event type immediately | superseded | Public types, snapshot-only fields, timestamps, and omission rules make this over-broad | Narrow first slice in `SIFT-SUB-02-01` |
| Remove `ProviderAdapter.Complete` in the first trace fix | demoted | Parity can land provider by provider through existing `FullResponse`; interface removal requires a later approved breaking boundary | `SIFT-SUB-09-02` |
| Combine CHANGELOG and website release prose | rejected | They serve different audiences and already have a release drift gate | SUB-16 |

## 8. Audit-of-audit results

- **Coverage and missing boundaries:** The fresh pass found that top-level `pipeline/**` needed an explicit four-way partition across SUB-02 through SUB-05. It also assigned `.pre-commit`, `.gitignore`, `transport/conformance/**`, root fixtures, and relevant local tests to exact owners. After those repairs, every tracked file class has one row.
- **Duplication and ownership overlap:** Gate cancellation moved from transport to the handler contract; condition ownership stayed with schema/preparation; parallel usage and token normalization remained separate because one preserves metadata while the other defines arithmetic.
- **Materiality and over-abstraction:** Exact asset deduplication was rejected; CLI and event-schema proposals were narrowed; public DOT parsing, public provider-interface removal, and broad graph revalidation were excluded from first slices.
- **Finding-schema completeness:** Every retained finding now includes identity, evidence, current model/invalid states/materiality, proposed model/scope/slice, risk/migration/existing/additional validation, and all ranking fields. The fresh pass tightened compatibility notes for graph cloning, public session/provider types, generated wire shapes, and example deletion.
- **Dependency-aware ranking:** The security finding was moved to a private advisory before publication. Small release/CI correctness fixes lead the public list. Accounting/lifecycle correctness precedes structural cleanup. Graph preparation precedes condition compilation; event generation follows stabilization of stop/status/usage shapes; deletions remain last and require explicit approval.

No audit-of-audit pass ran tests, builds, scripts, generators, workflows, local test cases, or network calls.

## 9. Repository integrity

- **Baseline status:** `main` at `217a50d4aa6bffc45d555b0e83b04a2d9bc00037`; no tracked or staged diff; 15 pre-existing untracked paths. Their names are omitted from this public edition because some contain security research.

- **Final pre-report status:** exact match to baseline; `git diff --exit-code` and `git diff --cached --exit-code` both passed.
- **Comparison:** unchanged before the authorized report write.
- **Integrity verdict:** pass.
- **Post-audit writes:** `docs/sift-audit-2026-08-24.md` only.
