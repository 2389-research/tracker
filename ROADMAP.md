# Tracker Roadmap

A rolling engineering roadmap organized into thematic workstreams across
**Now / Next / Later** tiers. Priorities, not dates. The GitHub milestones
mirror the **Now** tier and are the source of truth for what's actively in
flight; everything below Now is directional and will churn.

> **Maintenance contract:** a workstream finishes → close its milestone →
> promote the next workstream up a tier → update this file, all in the same
> PR. Only **Now**-tier workstreams get GitHub milestones. Refreshed at every
> release (see the *Before releasing* checklist in `CLAUDE.md`).

---

## Now

The open GitHub milestones, which are the source of truth for what's in
flight. Issues without a milestone that belong to a workstream are listed
with it.

### Agent-driven Tracker — *next up: sub-project 2 of the competitive-analysis plan*
Let an agent (Claude Code first) install Tracker, run workflows for you and
relay their human gates, without a TTY.
- **2a — pause at a human gate**: a non-interactive gate mode. At a gate the
  run checkpoints and exits as a resumable `paused` terminal (beside
  `paused_billing`); `tracker -r <run> --answer <label or text>` resumes it,
  bound to the gate it answers. Serves any non-TTY driver (agents, CI,
  scripts) and is the base for an attention queue of waiting runs.
- **2b — Claude Code skill + `install.md`**: shipped from this repo as a
  plugin in the 2389 marketplace, in three rungs — onboarding (install, then
  `explain` / `quickfix`), driving (run built-ins, relay gates, `diagnose`),
  authoring (`.dip` workflows to grade A).

### 1 · Trust & verification — *milestone*
- **#655** — converge the built-in `build_product` with the tracker-runner
  fork; the tracker side is done (v0.74.0–v0.75.0), the runner switch is
  runner-side.
- **#624 / #625** — adopt adversarial review (typed verdicts, deterministic
  false-positive gate) in `build_product`'s review loop.
- **#490 / #353** — verification costs about as much as implementation;
  one reviewer burned 32% of a run. Dedup and cap the review fan-out.
- **#627** — Meta Muse models: priced and reachable.
- **#280 / #281** — `writable_paths` follow-ups: file-scoped Bash
  enforcement; per-OS enforcement on macOS / FreeBSD.

### Parallel-first resilience — *milestone (promoted v0.74.0)*
First-class parallel milestones, so branches retry and resume independently.
- **#420** — branch-scoped retry, context and fix-attempt counters
  (`ctx.branch_id` is the namespace a branch's tool node uses today).
  (#427, sub-node turn checkpointing, ✅ shipped.)

### Transport boundary — *milestone*
- **#634** — human-gate artifacts as named attachments, gate status tokens,
  structured gate payloads.

### 2 · `.dip` language & engine expressiveness — *milestone*
- **#641** — a workflow-level progress narrator.
- **#721** — route an unmatched freeform gate reply explicitly, not by
  lexical target order (`quickfix` relies on the order today).
- **#717 / #718** — subgraph accounting: `run.json` totals count subgraph
  tokens twice; subgraph events carry the child's run ID.
- **#726** — `verify.sh` tests an unchanged cargo member crate whole.

### 3 · Library, CLI & refactor quality — *milestone*
- **#463** — the run-flag surface (~36 flags) needs presets or progressive
  disclosure.
- **#450** — the root package mixes the public API with assembly helpers.
- **#395** — collapse near-identical duplication.
- **#672** — port `agent/exec` and the handler backends to Windows.

### 4 · Product & 1.0 readiness — *milestone*
- **#465 / #589** — the first scored SWE-bench Verified run, published.
- **#460 / #583 / #585 / #586** — naming and discoverability: qualify or
  rename, own a domain.
- **#458 / #459 / #581** — show the TUI; lead with the trust story; a /trust
  page.
- **#461 / #464** — the Dippin adoption path; relocate the research tooling.
- **#638** — local `hugo server` on Hugo 0.166.

---

## Next

Directional. Expected to promote to Now as the milestones above close.

### Competitive-analysis sub-projects 3–5
After the agent-driven work (Now), from the 2026-10 competitive analysis:
- **An issue → PR built-in**: take a GitHub issue to a reviewed branch and a
  pull request, building on `quickfix`'s worktree and gate.
- **An attention queue**: the runs waiting on you (paused at a gate, paused on
  billing, failed) in one list, with notifications; pause-at-gate (Now) is its
  base.
- **Runtime `for_each` fan-out**: fan a node out over a list produced at run
  time.



---

## Recently shipped

Workstreams that were in the Now tier until their milestones closed.

### Resume trust & provider-pause hardening — ✅ shipped (v0.82.0; #714, #722)
A checkpoint-restored tool_command-safe key (`outcome`, `preferred_label`,
`human_response`, `interview_answers`, `branch_id`) is now shell-untrusted on
resume — a tampered checkpoint can no longer inject into a tool node's `sh -c`
(the confused-deputy residual #708/#713 left open); the marks propagate into
parallel-branch, subgraph and `manager_loop` child contexts and clear only when
a live node re-derives the value (#714). A Gemini "prepayment credits are
depleted" 429 now pauses as resumable `paused_billing` instead of retrying into
a milestone rejection (#722). Also: the `paused_gate` terminal + `gate_paused`
event groundwork for pause-at-gate (2a, interviewer/answer side still in
flight), and dippin-lang → v0.83.0 (#737, #741).

### First win in five minutes — ✅ shipped (v0.81.0; #723, #724, #725, #729)
Sub-project 1 of the competitive-analysis plan
(`docs/superpowers/specs/2026-10-19-first-win-quickstart-design.md`): a new
user with Claude Code and no API key gets an answer from `tracker explain`
and one reviewed change from `tracker quickfix` (own worktree and branch,
approve / revise / reject). Underneath: `backend: auto` (resolved once per
run, recorded, never switches silently), a keyless start when no node needs
the native backend, a routable refuse-to-start outcome, `tool_access:
read_only`, and `tracker doctor` checking the backends a pipeline actually
needs (#716). Shipped with it: `TRACKER_CLAUDE_BIN` and every run naming the
`claude` it ran (#719), and dippin-lang v0.80.0 (#698). Follow-ups: dippin-lang#54 (`read_only` in dippin; removes
tracker's DIP139 stopgap), dippin-lang#338 (workflow-level `defaults:
backend`), #721 (route unmatched gate replies explicitly, not lexically).
Next: *Agent-driven Tracker* (Now).

### ChatGPT-subscription auth — ✅ shipped (v0.80.0)
`openai` nodes can bill a signed-in ChatGPT plan instead of an API key, for one
person's own account on their own machine: `tracker setup` signs in through
Codex's OAuth device-code flow and sets `TRACKER_OPENAI_AUTH=chatgpt`; the
native backend then sends openai requests to ChatGPT's Codex backend as
`originator: tracker`, labels the usage `openai-subscription` at $0, and pauses
in the resumable `paused_billing` state at the plan's usage limit. `trackerbot`
refuses the mode. `--autopilot-model` (#699) points autopilot at a model the
plan serves.
v0.80.1 closed the follow-ups: a failed token save or a transient refresh
error no longer forces a fresh sign-in or fails the call (#700), the traced
completion path rejects a stream that ends before its finish event (#701),
and failover usage lands under the provider that served it (#702).

### Env-trust & credential-boundary hardening — ✅ shipped (v0.78.0)
Source-aware `.env` loading so a project `.env` (writable by any committer or a
prior agent) can set provider `*_API_KEY`s only — not security switches, base
URLs, or trusted-state paths — with the `internal/envpolicy` registry, invariant
tests, the `--env-files` switch and `tracker doctor` provenance (#659); the
turn-checkpoint and swebench git probes credential-filtered, the last two
unfiltered git subprocesses (#671); the `writable_paths` jail `/dev/null` fix and
a witnessed `TestParallelBranchSymlinkRace` redesign, plus elimination of the
SIGPIPE fixture-flake and a `make shell-check` gate (#658); the Windows
cross-compile of `agent`/`tracker-swebench` (#661); and the dead `tool_pass_env`
knob removed (#663). v0.78.1 closed the #660 code fix: ACP terminal commands
get `exec.CommandEnv` (with `TRACKER_STRIP_ACP_KEYS=1` still applied on top),
and every backend `--version` probe — including `tracker doctor`'s — gets its
binary's launch environment. Claude Code's own subprocess scrub stays an
operator opt-in (it breaks `$VAR` / `env` / `sh -c` under `bypassPermissions`).
v0.78.2 strips each provider key's `*_BASE_URL` from the claude-code env with
it, so a native-backend gateway no longer receives `claude`'s subscription
login (#677). v0.79.0 closed the last two code gaps: `exec.CommandEnv` now strips
credential-bearing values (a URL with a password, an `http(s)` URL with
userinfo, a Docker `auths` blob) as well as names (#679), and a claude-code
node on the default `bypassPermissions`, with no `tool_access` or
`disallowed_tools`, now fails when Claude Code refuses its tool calls instead of
reporting success (#676).
**Still open:** unifying
the `TRACKER_PASS_*` / `TRACKER_STRIP_ACP_KEYS` switches; and the full
`GOOS=windows` build (`agent/exec` core port, #672).

### Run capture & cost correctness — ✅ shipped (v0.50.0)
Landed the tracker-runner run-capture PR (#519): executed spec + verbatim
provider request bodies + per-call/turn/session identity + a `run.json` manifest
(`tracker run-json` backfills archived runs). A deep-dive review of that work
found and fixed a batch of cost-accuracy and security issues, all shipped in the
same release: provider-aware cache-write pricing (#522), mixed-backend token
undercount in `run.json` (#523), session cost priced with the response model so
`--max-cost` survives a failover (#524), the world-readable run-dir mirror (#525),
and capture identity on the live `--json` wire (#526). The capture-file security
hardening followed in v0.50.1 (#521/#528/#529: atomic 0600 + O_NOFOLLOW writes,
and `.tracker/` excluded from WIP/bundle staging), and v0.51.0 closed out the
remaining follow-ups: the `Config.Capture` embedder seam + docs (#530),
`TokenTracker` per-(provider,model) pricing (#527), and the dual
cost-field-name convergence (#520). v0.52.0 shipped the decision-free half of #518 (pricing source-of-truth): the
`unpriced`/`--max-cost` signal and published-price provenance with a build-time
drift guard. The one piece still held is #518's optional scheduled pricing-page
staleness scraper (an outbound-network CI job), pending ops sign-off.

### Transport boundary — ✅ shipped (v0.46.0)
The core is now fully UI-agnostic: TUI, Slack, web, and mobile are first-class
transport peers on one `tracker.Config` → `Engine` path. Shipped #472/#474/#475/
#476/#477/#478/#479 (absorbing #396/#450/#451): the `Config.Interviewer` seam,
event-stream completeness (authoritative terminal-status, start snapshot,
cost-as-events), N-concurrent-run safety, the `tui/render` relocation, full
CLI→library unification, and the transport-neutral `RunManager`. Followed by a
hardening pass — engine/RunManager panic containment, atomic checkpoint/state
writes, `trackerbot` authz/budget/lifecycle, and a `transport/conformance` suite
that a new transport runs to prove correctness. Boundary contract:
[`docs/architecture/transport-boundary.md`](docs/architecture/transport-boundary.md).

### Policy, hygiene & coherence — ✅ shipped (v0.48.0)
Built on the v0.47.0 embedding surface. Shipped a fail-closed pre-execution
tool-call guardrail hook (#506) — an opt-in `GuardrailPolicy` that gates every
agent tool call on `(tool, args, context)` and returns the denial reason to the
model; an injectable diagnostic sink (#449, `tracker.SetDiagnosticLogger`) so the
library no longer writes to the global logger; gate identity on the interviewer
callback (`GateAware`), sharing the `gate_opened` id so an out-of-process
transport can correlate the blind `Ask*` callback with the event stream; and
three fixes for cross-cutting gaps a post-release audit found between the v0.47.0
event/wire landings — per-turn agent usage now round-trips through `activity.jsonl`,
the bounded event handler never splits a gate pair under a lossy overflow policy,
and every handler-originated event carries `run_id`. Follow-ups filed: #514
(`tracker audit` classes a paused run as failed), #516 (resume-in-capacity-window
race), #517 (diagnose blank-line injection counting).

### Embedding surface completeness — ✅ shipped (v0.47.0)
The event surface an out-of-process, event-sourced control plane
(`tracker-runner`) drives Tracker through is now complete. Shipped in v0.47.0
(sourced from a tracker-runner enablement audit): `turn_metrics` attribution
(#508) and gate lifecycle events (#509) so per-turn cost and gate history
reconstruct from the stream alone; NDJSON `StreamEvent` payload parity with
`activity.jsonl` and a lossless `ActivityEntry` reader, both held to the private
schema by a mechanical field-name guard; `paused_billing` as a first-class
resumable `RunManager.RunPaused` state (#487) instead of `RunFailed`; submit-time
variable-availability validation (#505); and a bounded/async event-handler seam
so a slow subscriber cannot block the engine. The follow-on enablement work has
since shipped: #449 and #506 in v0.48.0, and the coverage-hole golden fixtures +
#462 (API stability policy + surface snapshot) in v0.49.0.

### Coverage & API stability — ✅ shipped (v0.49.0)
Closed the embedding batch. Golden-trace fixtures now pin the five previously
unverified handler/terminal contracts (`validation_overridden`, `subgraph`,
`stack.manager_loop`, `interview`, `paused_billing`); `docs/api-stability.md` plus
an exported-surface golden snapshot (`api_surface_test.go`) guard the root
`tracker` package against accidental signature change; and three audit follow-ups
landed — #514 (`tracker audit` classes a paused run as `paused`, not `failed`),
#516 (spurious resume-time `ErrAtCapacity`), #517 (diagnose blank-line injection
counting).

### Adversarial Review — ✅ shipped (v0.73.0)
Reusable read-only adversarial code review, epic #625. The FP control from
*Adversarial Review* (arXiv 2608.18167) ships as engine structure +
deterministic tools, not prompt discipline: `examples/subgraphs/adversarial-review.dip`
runs three independent perspectives over a frozen diff in parallel, a
typed-verdict critic audits the merged findings, contested findings enter a
bounded re-review loop, and a deterministic FP gate (the #622 disposition rule
+ caller `severity_threshold`) emits `ctx.review_findings` /
`ctx.review_verdict` (`approve`/`rework`). Read-only by construction and
fail-closed at the approval boundary — a review that cannot complete degrades
to `rework`, never `approve`. The measurement half of the epic (real-diff FP
reduction vs one-shot cross-critique) still needs labeled real diffs.

### Failure UX & recovery — ✅ shipped (epic #493 closed)
Shipped a batch of this epic:
- **#487** — ✅ resolved (P0): billing/quota exhaustion is now a *recoverable*
  `paused_billing` terminal (checkpoint + preserved WIP + `tracker -r` resume)
  with an account-attributed message (provider + env var + masked key + billing
  URL), not a fatal abort.
- **#492** — ✅ resolved: failures lead with a classified cause + remediation
  (`💳 Billing…`, `🔑 Auth…`, `⏳ Rate limited…`) via `tracker.ClassifyFailure`,
  not the `handler error at node "X"` wrapper.
- **#488** — ✅ resolved: in-flight work is preserved on node failure, and the
  warning leads with what was lost/safe + recovery.
- **#489** — verify-milestone test-fidelity: concrete parts shipped — duplicate
  test-body detection (`tracker verify-tests`) and a `go test -race` gate
  (`--race`, v0.54.0). The fuzzy fidelity heuristics (structural near-duplicate,
  unreached claimed path, production-logic-in-test) shipped as **#532** (✅).
- **#486** — ✅ resolved: provider/model failover on quota, billing and
  rate-limit errors.

### Engine correctness — ✅ milestone closed
The engine must route and terminate exactly as authored. No silent
mis-routes, no phantom "Done" on an unresolved gate.
- **#348** — ✅ resolved: goal-gate retry now re-enters the gate node (via a
  persisted `GateRecheckPending` flag) so a remediated tree is re-judged, and a
  human "accept" marks the gate `validation_overridden` (#271) rather than
  ending in a silent success with an unsatisfied gate. Regression-tested in
  `pipeline/engine_goal_gate_recheck_test.go` + `_override_test.go`.
- **#633** — ✅ resolved (v0.72.7): a run reaching the exit node via an
  operator's rejection-labeled (`abandon` / `reject`) non-override gate edge
  terminated `success` — the exit passthrough always succeeds, so the rejection
  was invisible in `result.Status`. The exit path now consults the durable
  checkpoint edge selections and terminates `fail` (`run rejected at human
  gate …`); accepts and unlabeled freeform/interview edges are unaffected.
- **#642–#654** — ✅ resolved (v0.74.0): the failure-semantics batch the #640
  fixture suites and probe runs surfaced. One-shot fallback latch on retry
  exhaustion (#642), restart budgets scoped per loop iteration via dominator
  natural loops (#643), tool `timeout:` as a routable `OutcomeFail` (#644),
  tolerant `STATUS:` grammar with an early-fail contract (#645), edge
  conditions serialized from dippin's AST so `and`/`or`/`not` route
  correctly (#647), section-level `else ->` honored success-side only (#649),
  self-targeting fallback is a no-op with fail-routing provenance in every
  failure message (#650), `tracker -r` rewinds past a fail-closed dead end
  with `--from` / `--resume-no-rewind` (#651, #654), tool `FailureReason` on
  non-zero exit (#652), and the full dippin failure cascade for a failed node
  whose guards all miss (#653). Plus `writable_paths_mode: prefer` (#648) so a
  hardened node degrades to a loud, recorded unjailed run instead of refusing
  where Landlock is absent, and a `jail-linux` CI job that finally executes
  the Landlock suite.
- **#697 / #696 / #686 / #684** — ✅ resolved (v0.80.0): resume and run-dir
  correctness. Resume compaction no longer blanks `parallel.results`,
  declared inputs or live graph attributes, so a run halted between a fan-out
  and its fan-in resumes into the fan-in (#697). `tracker -r` finds runs in
  `--artifact-dir` and reads the authoritative secure checkpoint first (#696).
  `TRACKER_RUN_DIR` exists before the first node runs (#686). An in-stream LLM
  error with an unrecognized code is retried, while billing exhaustion still
  pauses (#684).
- **#708 / #711** — ✅ resolved (v0.80.1): resume refuses a checkpoint whose
  run ID isn't one path element, before any node runs or any directory is
  created, and compaction skips a completed-node ID that isn't one (#708). A
  parallel node's codergen branches count under their own provider, one
  session each, with reasoning and cache tokens summed (#711). #714 (checkpoint-restored context values that tool commands interpolate
  unquoted) is tracked in the Now tier below.

*(The v0.44.0 engine-correctness batch — #444/#445/#446/#447/#448 — #430, and
#348 shipped and are closed. No known routing defects remain open.)*

### Epic #308 closeout — ✅ milestone closed (epic #308 closed)
Harden `build_product` against the structural and process gaps surfaced by
the case-study runs.
- **#308** — ✅ the epic: structural & process gaps beyond #233.
- **#304** — ✅ resolved: budget-by-cost ceiling + a no-progress detector, with
  per-node turn count demoted to a backstop. Follow-up **#531** (✅ shipped
  v0.53.0) refined the detector to key on workspace edits (a commit/verify-state
  proxy) rather than raw tool-call activity, catching a tight-looping agent that
  the old heuristic missed.
- **#307** — ✅ resolved (v0.66.0): `build_product` vs `superspec`
  documented, spec-coherence preflight backported, `examples/` vs
  `workflows/` duplication resolved (#256).
- **#640** — ✅ resolved (v0.74.0): the consolidated `build_product` bug list
  from the fixture suites + 4-lens deep hunt. All three shipped pipelines are
  decomposed into `prompts/<name>/` + `scripts/<name>/` sidecars (#398) with
  ~900 shell fixture checks run under `sh` and `dash`; fail-closed routing
  (mechanical failures → `AbortRun`, `EscalateVerification` default
  `abandon`, every gate's `abandon` ends the run `fail`), git safety
  (worktree-correct excludes, secrets denylist, unsigned hook-respecting
  checkpoints), per-plan state resets + one shared plan parser, and
  green-means-green verification (tree-wide stack detection, worktree-scoped
  Go tests, gate-file integrity, operator stamps). #646 applied the same
  class of fixes to every sibling pipeline.
- **#655** — moved to *1 · Trust & verification* (Now): converge tracker's built-in with the
  tracker-runner fork. v0.74.0 shipped the superset — the runner's
  battle-tested deltas are upstreamed (`EnsureEnv` seed bootstrap, `verify.sh`
  exit-3 not-yet-verifiable, positive executed-test oracle, advisory native
  lint, `__ROUTE_ESCALATE__`, per-milestone contract tests — runner
  #846/#857/#873/#840/#901). v0.75.0 closes the last two tracker-side gaps:
  the `tracker.ParseSource` seam (the runner's force-model bridge can parse a
  built-in, mutate the graph, then `NewEngineFromGraph` — runner #906) and
  milestone-scoped `cargo test` (runner #901). **The runner switch is
  unblocked**: remaining is runner-side — bump the pin, switch to
  `SourceRef{Builtin: "build_product"}`, delete the fork, and answer the
  open questions on gate mode / `validation_overridden` mapping / clean
  workdirs / Linux ≥ 6.2.
- **tracker-runner#730** — ✅ resolved (v0.72.6): the `EscalateMilestone` gate's `accept`
  choice re-entered `CheckMilestoneOutputs`, the very structural check the
  operator was overriding — a flagged tree looped to the gate until only
  `abandon` exited. `accept` now routes forward to `ClearStaleReviews` (the
  normal ship entry), still earning cross-review + `FinalBuild` +
  `FinalSpecCheck`. The gate's underlying false positive on a green tree is
  a separate open question (needs the run's captured stdout — `tracker
  diagnose <runID>`).

---

## Later

Backlog. Real, but not scheduled.

### Sandbox breadth
- **#279** — ✅ resolved: `overrideAlreadyRecorded` rescoped to checkpoint generation.
- **#280 / #281** — moved to *1 · Trust & verification* (Now).

### Security documentation & process — ✅ shipped (v0.53.0)
- **#284** — ✅ Linux security primitives reference doc (`docs/architecture/linux-security-primitives.md`).
- **#285** — ✅ 9-class audit checklist for `writable_paths` changes (`docs/architecture/writable-paths-audit-checklist.md`).
- **#286** — ✅ "freeze and prove" pattern for security PRs (`docs/architecture/security-pr-process.md`).

### Structural & cosmetic refactors
- **#395** — moved to *3 · Library, CLI & refactor quality* (Now).
- **#398** — ✅ resolved (v0.74.0): every shipped built-in
  (`build_product`, `build_product_with_superspec`, `ask_and_execute`) is
  decomposed into `prompt_file:` / `command_file:` sidecars with fixture
  suites; embedded built-ins resolve them from the binary's embed FS and
  materialize `${graph.workflow_dir}` per run.
- **#452** — ✅ resolved: `write_enriched_sprint.go` (1,250 lines) is a domain workflow
  embedded in `agent/`.
- **#453** — ✅ resolved: split the 1,687-line `tracker_doctor.go` into unit-testable
  checks.
- **#454** — ✅ resolved: group handler-specific `Outcome` fields into sub-structs.
- **#455** — ✅ resolved: repo hygiene sweep.

### Transports
- **#473** — ✅ shipped (v0.46.0): Slack transport (`cmd/trackerbot`) — drive
  Tracker from Slack via Socket Mode; `@trackerbot` starts runs, threads receive
  notifications and gate questions, results land back in the thread. All four
  gate modes, natural-language intent, control commands, per-thread concurrency,
  failure diagnosis (#480–#485), **durable resume across restarts**, authz +
  fail-closed budget + workdir lifecycle, and conformance-suite coverage. The
  first non-TUI consumer that proves the boundary. See
  [`cmd/trackerbot/README.md`](cmd/trackerbot/README.md).
  Experience layer (v0.46.0): a live status card, up-front cost `estimate` +
  confirm-over-threshold gate, richer delivery, and the `retry` / `bump` /
  `steer` / `workflows` commands + workflow suggestions; plus the Tier-3
  `/tracker` slash command and App Home tab. Remaining: live-Slack verification
  of the visual surfaces + the slash/App-Home plumbing against a staging
  workspace.
- **Mid-run steer** — ✅ shipped (v0.46.0): `Config.SteeringChan` forwards
  external context updates into a running pipeline (drained between nodes);
  `trackerbot`'s `steer <text>` command is the first consumer.
- **CLI REPL (`cmd/trackerchat`)** — ✅ shipped (v0.46.0): a terminal front-end,
  the **second** boundary consumer — reuses all of `transport/chatops`, adding
  only a terminal `ThreadUI` + a stdin loop (`transport/cli`). Concrete proof the
  boundary is I/O-only. See [`cmd/trackerchat/README.md`](cmd/trackerchat/README.md).
- Next transports (from the expansion plan): web dashboard, Discord, Teams,
  email, GitHub/GitLab bot — each a `transport/chatops` (or event-stream)
  consumer. See [`docs/plans/2026-07-21-transport-expansion.md`](docs/plans/2026-07-21-transport-expansion.md).

### Product & positioning
- **#460 / #461 / #464** — moved to *4 · Product & 1.0 readiness* (Now);
  **#463** to *3 · Library, CLI & refactor quality* (Now).

### The 1.0 question
- **#462** — API stability policy shipped: [`docs/api-stability.md`](docs/api-stability.md)
  states the supported root-`tracker` surface, the open-enum rule, and the pre-1.0
  deprecation contract, mechanically guarded by an exported-surface golden snapshot
  (`api_surface_test.go` + `testdata/api_surface.golden`). Remaining for v1.0: the
  actual no-breaking-changes commitment, benchmark cadence, and extending the
  surface snapshot to the supported sub-package types (handlers/pipeline/llm).
