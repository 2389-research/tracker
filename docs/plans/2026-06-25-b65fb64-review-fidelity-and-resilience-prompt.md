# Fresh-session prompt: review-fidelity & engine-resilience workstream (run `b65fb64ac099`)

> Paste everything below the line into a fresh Claude Code session at the repo root
> (`/home/clint/code/2389/tracker`, branch `main`, clean tree). It is self-contained.
> This workstream uses the **Workflow tool (ultracode)** for agentic orchestration.

---

## Mission

A dogfooding run of `build_product` (code-goblin, run **`b65fb64ac099`**, ~v0.40.x — total ~2h
active, **$27.13 / 3.42M tokens**) exposed two classes of problem: (1) **verification blind spots**
— every gate (TestMilestone, VerifyMilestone, the 3-model review panel) passed GREEN while real
defects shipped, because the gates verify against self-authored *fakes* at external seams; and
(2) **cost/resilience defects** — cross-review burned **90% of all tokens** (3.10M of 3.42M),
already-green work was recomputed across 11 loop-restarts, a closed-laptop suspend tripped wall-clock
budgets and corrupted the sandbox, and a green build degraded to a best-effort warning on the commit
path.

Your job is to land these as **four grouped, independently-mergeable PRs** in one workstream, with
TDD discipline, adversarial subagent review, and pragmatic surgical changes — no over- or
under-correction. **Issue #420 is explicitly OUT of scope** (it needs a dippin-lang field release;
tracked separately). **Issue #353 is context, not a deliverable** (see "Do NOT rebuild" below).

**Read these before touching anything** (in order):
1. `CLAUDE.md` — project rules. The ones that bind here: *Edge routing — no unconditional fallbacks
   to loop targets*, *Strict failure edges*, *Never silently swallow errors*, *NEVER use
   --no-verify*, *NEVER `go install` dippin-lang*, *Before committing*, and the **genericity** theme
   (section D below). These override your defaults.
2. The issues — `gh issue view 416 417 418 419 421 422 423` (run each). Also `gh issue view 353 420
   306` for context. Treat each issue's **Acceptance criteria** as your test specification.
3. `docs/architecture/engine.md` (run loop, edge selection, escalation), `docs/architecture/handlers.md`,
   `docs/architecture/context-flow.md`.

---

## Three "verify, don't trust" facts (the issues are stale on these)

Confirm by reading the code before you design — they change the shape of the work:

- **There is NO `restart:true` edge attribute.** #421 (and #420) cite `restart:true` at
  `build_product.dip:2545` etc. — wrong. Restart is triggered by **loop-back-to-an-already-completed
  node** detection: `handleCompletedTarget` (`pipeline/engine.go:693`) → `handleLoopRestart`
  (`pipeline/engine_run.go:1020-1081`), which increments the **global** `Checkpoint.RestartCount`
  (`pipeline/checkpoint.go:21`) and calls `clearDownstream` (`pipeline/engine_checkpoint.go:68-85`).
  Design #421's memo against the *real* mechanism.
- **`.ai/decisions/behavioral-contracts.md` does NOT exist yet — #306 is OPEN and unimplemented**
  (verified: `gh issue view 306` is open; grep finds no node writing or reading the file, and `#306`
  appears nowhere in `build_product.dip`). #306 is the issue that *creates* it, and it is **pure
  `build_product.dip` authoring**: `ReadSpec` writes `.ai/decisions/spec-ambiguities.md` (one
  DEFINITE ruling per spec contradiction — e.g. "max 2 retries" vs "max 2 attempts") **and**
  `.ai/decisions/behavioral-contracts.md` (every non-literal prose guarantee — MUST/never/"at
  startup, not runtime"/"within N ms"/exactly-once — with its verification method); `ApprovePlan`
  surfaces both; `Implement` cites the rulings; `VerifyMilestone`/`FinalSpecCheck` disposition each
  contract with concrete evidence or STATUS:fail. **#306 is a hard prerequisite for #417's facet (b)**
  (which extends `behavioral-contracts.md` with normative constants). It is therefore **folded into
  PR-A and sequenced FIRST** (see PR-A). You are building the artifact, not assuming it.
- **#304 (per-node cost ceiling) already SHIPPED** in v0.40.0. `max_cost_usd` / `no_progress_turns`
  are parsed in `pipeline/node_config.go:34-35,145-166`, detected in `agent/session.go:251-267`, and
  routed via `pipeline/handlers/codergen.go:530-592` emitting `EventNodeCostLimitExceeded` /
  `EventNodeNoProgressDetected`. **Do not rebuild it.** #353's live asks reduce to: diff-scope +
  tier the review panel (→ #418) and tier cheaply-verified nodes (→ #419). The remaining #353 asks
  (per-backend caching awareness; a per-session *input-token* ceiling distinct from cost) are **not**
  in this batch — note them as follow-ups, don't build them.

> **Line numbers below are current as of HEAD (v0.40.2) but will drift as you edit.** Always
> re-locate nodes by **id/content**, never by trusting a line number.

---

## Section D — the genericity rule (load-bearing, especially PR-A)

Every change MUST be **product- and language-agnostic**. Detection keys on **role**, never on a
detected language or product:
- Reviewer/spec rules key on **seam role** (LLM provider adapter / VCS-host CLI like `gh` /
  subprocess whose arg or response *shape* is contractual) — never on "Go" or "OpenAI" or "gh".
- Model/effort tiering keys on **node role** (general / quality / adversarial review lane;
  cheaply-verified vs high-stakes-synthesis agent node) — never on detected language.
- Diff scope derives from the dip's **own** `base..HEAD` SHA computation — never a hard-coded path.
- Engine features (#421/#422/#423) key on **generic node inputs, clocks, tree SHAs, device nodes,
  and the artifact repo** — zero knowledge of milestones, languages, or `build_product` structure.

A reviewer subagent rejecting "this branches on a detected language/product" is doing its job.

---

## The four PRs

Open each as its own branch + PR. **PR-A is independent of B/C/D. B, C, D are mutually independent
engine features** — develop them in parallel under **git worktree isolation** (they touch
overlapping engine files: `engine.go`, `engine_run.go`, `checkpoint.go`, `budget.go`,
`git_preflight.go` — concurrent edits to the same tree will conflict).

### PR-A — Review fidelity & cost  (#306, #416, #417, #418, #419)  — `build_product.dip` authoring

All five edit the single **~2660-line** `examples/build_product.dip`. **Serialize the edits** through
one editor, **one issue per commit**, in this order: **#306 → #417 → #416 → #418 → #419** (#306 first
because #417(b) extends its `behavioral-contracts.md` artifact; the model-tiering #418b/#419 last so
they don't churn nodes you're still editing). Re-run `dippin doctor examples/build_product.dip` (must
stay **A**) and `dippin simulate -all-paths examples/build_product.dip` between each. Do NOT
parallelize edits to this file.

**Current anchors (re-locate by node id):**
- Review panel: `parallel ReviewParallel -> ReviewClaude, ReviewCodex, ReviewGemini` (1777-1779,
  `fan_in_policy: all`); preceded by `ClearStaleReviews` (1760-1775).
  - `ReviewClaude` 1793-1855 (`claude-opus-4-6`/anthropic/`reasoning_effort: high`, lane "general"),
    scope sentence 1799-1802.
  - `ReviewCodex` 1857-1919 (`gpt-5.4`/openai/high, lane "quality"), scope 1863-1866.
  - `ReviewGemini` 1921-1994 (`gemini-2.5-pro`/gemini/high, **adversarial** lane, role block
    1932-1938), scope 1927-1930.
  - Shared 5-point rubric: Claude 1809-1849, Codex 1873-1912, Gemini 1946-1986. **"TEST VERIFIES
    CONTRACT" = rubric point 3** (Claude 1827-1845). Point 1 = SPEC LITERALS (byte-for-byte grep),
    point 2 = INTERFACE REACHABILITY.
- `SynthesizeReviews` 2027-2066 (claude-opus-4-6/high, `auto_status`), **evidence-weighted** rule
  2039-2048 (robust to a weaker lane — weights by concrete evidence, not vote count).
- `SpecLint` 569-646 (claude-opus-4-6/high, `auto_status`), STATUS:fail-first contract 578-587,
  **"Mandated tests" feed** 631-639 (rule (f) 617-621 — "feeds the Decompose requirement-coverage
  table").
- `ReadSpec` 648-666 (claude-opus-4-6/high), writes `.ai/decisions/spec-analysis.md`.
- `FinalSpecCheck` 2155-2310 (claude-opus-4-6/high, `goal_gate`): interface-reachability 2164-2211,
  test-quality/sleep-fence 2213-2257, SPEC.md compliance 2259-2302.
- `Decompose` 668-767, **requirement-coverage block** 716-767 (emits `COVERAGE_GAPS: <n>`).
- Spec-literal grep: presence-only in `Implement` 946-954; severity-tiered verifier-side 1264-1308.
- Cumulative `base..HEAD` diff already computed: `TestMilestone` 304-314 (`MS_BASE..HEAD`),
  build-context appender 1482-1490 (`BASE..HEAD`). `.ai/build/milestone-start-sha` is per-milestone;
  there is **no whole-run base SHA file** — #418 likely needs a small tool node that computes the
  cumulative review diff into a file the reviewers read.

**#306 — spec-derived contract artifacts (DO FIRST; prerequisite for #417b).** `ReadSpec` (648-666,
currently writes only `.ai/decisions/spec-analysis.md`) additionally writes (i)
`.ai/decisions/spec-ambiguities.md` — one DEFINITE ruling per detected spec contradiction (conflicting
statements + their locations, the chosen resolution, a one-line rationale; **no "depends"**), and
(ii) `.ai/decisions/behavioral-contracts.md` — every non-literal prose guarantee (MUST / never / "at
startup, not runtime" / "within N ms" / exactly-once) with a concrete verification method (a specific
test or grep). `ApprovePlan` surfaces both artifacts to the human for correction before the build.
`Implement` cites the ambiguity rulings; `VerifyMilestone` + `FinalSpecCheck` disposition each
in-scope behavioral contract with concrete evidence (same show-your-work bar as the spec-literal
grep) — an undispositioned contract or an absent-but-needed ruling → STATUS:fail. `.ai/decisions/` is
already on the write allowlist. Genericity: extraction keys on prose-guarantee *shape*
(modal verbs, timing/ordering phrases), never on a language/product. **AC (from #306):** both
artifacts exist; one ruling per contradiction, one verification method per guarantee; `ApprovePlan`
presents them; a retry-count contradiction yields a single cited ruling and a "rejects at startup"
guarantee is dispositioned with a startup-path test/grep, not skipped.

**#416 — contract-fidelity at external seams.** (a) Add a **CONTRACT-FIDELITY** checklist item to
each reviewer rubric and to `FinalSpecCheck`: for every external seam, FAIL when the only test
exercising it uses a fake the production code *also* defines (cite the seam + test path); require a
recorded/golden real-provider response or a CI-reachable real-CLI invocation. (b) `SpecLint` records
an exact external-tool invocation **literal** (a `gh`/`git`/CLI flag string) as a behavioral contract
whose verification method is "the real tool accepts this literal," so a wrong-on-its-face literal
(the `gh ... -b -` case) is caught as an unverifiable contract, not silently grepped-and-passed.
(c) *Optional stretch* — a `tracker validate`/`doctor` lint flagging an adapter response-parser whose
accepted JSON shape has no golden real-provider fixture (object-root vs array-root divergence). This
is the only **engine** facet of PR-A; if it grows, split it to a follow-up rather than blocking
a/b. Lint surfaces: a new TRK1XX rule in `pipeline/lint_tracker.go:16` or a new check in
`tracker_doctor.go:194` (`baseDoctorChecks`).
*Both observed cases are reproduced verbatim in the #416 body — you do NOT need the run artifacts.*

**#417 — self-ratifying tests & unasserted normative constants.** (a) Reviewer rubric gains a
**SPEC-EMITTED-VALUE** check distinct from spec-literal-presence: for every spec-prescribed emitted
value (log event name, error code, status string like `review.skip "no_branch"`), the reviewer must
find a test whose **asserted expected value is the SPEC literal**, not a variable referencing the
production constant — `assert(emit == prod.Constant)` is FAIL, `assert(emit == "review.skip")` is
PASS; cite the assertion line. (b) **Extends #306** — when SPEC marks a constant "normative,"
`ReadSpec`/`SpecLint` records it in the `.ai/decisions/behavioral-contracts.md` artifact created by
#306 (do #306 first) with verification method "a test asserts this exact value as a hard-coded
expectation," and `VerifyMilestone`/`FinalSpecCheck` FAIL an undispositioned normative constant.
(c) Extend SpecLint's
"Mandated tests" feed (631-639) and `Decompose`'s coverage table (716-767) to include spec-emitted
values and normative constants so they can't be silently unowned.

**#418 — diff-scope + tier the review panel** (the 90%-of-tokens problem). (a) Point each reviewer's
*primary* read at the dip-computed `base..HEAD` diff (full tree available on demand, not mandated) —
change only the scope sentence (1799-1802 / 1863-1866 / 1927-1930); the 5-point rubric is unchanged.
(b) Keep the **adversarial** lane (`ReviewGemini`) frontier; move the two non-adversarial lanes
(`ReviewClaude`, `ReviewCodex`) to a faster **mid-tier** `model:` with the SAME rubric;
`SynthesizeReviews` unchanged. Target ≥ -40% review INPUT tokens vs the **3.01M baseline** (this
number is authoritative — it is stated in the #418 body; you do NOT need the run artifacts to
establish it). AC#3's "-40% on a re-run of a comparable spec" is a *steady-state* target, not a gate
you can run inside this PR (a full `build_product` re-run costs ~$27 / 2h and needs a real spec). So
**prove AC#3 by construction + estimate**, and say so in the PR: (i) show the diff-scope edit makes
the reviewers read the dip-computed `base..HEAD` diff instead of the whole tree, (ii) note the
mid-tier model on two of three lanes, (iii) give a back-of-envelope reduction against 3.01M. Only run
a live re-run if the user explicitly asks for empirical confirmation.

**#419 — tier cheaply-verified agent nodes.** Drop `SpecLint` (569) and `ReadSpec` (648) to a
mid-tier `model:` and/or `reasoning_effort: medium` (both are gated by a cheap downstream verifier —
SpecLint's STATUS contract; ReadSpec's `ApprovePlan` human review). Keep `SynthesizeReviews`,
`FinalSpecCheck`, and the adversarial lane at frontier/high.

**PR-A acceptance:** each issue's ACs (run `gh issue view 306 416 417 418 419`). Model-tiering (#418b,
#419) is unit-testable — assert node attrs in a Go test mirroring the existing
`pipeline/build_product_*_test.go` structure pins. Rubric/prose changes (#416, #417, #418a) are not
unit-testable: verify by (i) any scenario fixture where routing is observable, (ii) a focused re-read
against each AC, (iii) the **genericity** check (section D). `dippin doctor` stays **A** throughout.

### PR-B — Node-output memoization across loop restarts  (#421)  — engine

**Problem:** 11 loop-restarts re-ran already-green nodes; `clearDownstream` BFS re-executes the
cleared set on every restart; there is **no** node-level memoization (the only caches are the
per-session tool cache `agent/tool_cache.go` and Anthropic prompt-cache *token accounting* — neither
skips a node).

**Design:** opt-in content-hash node-output cache. Key = `node.id` + hash of resolved inputs
(interpolated prompt/command, the upstream ctx values the node reads, and a relevant file/tree SHA
for tool nodes with working-tree side effects). On re-entry to a node whose key matches a prior
**successful** execution *within the same run*, replay the stored `Outcome.Status` +
`Outcome.ContextUpdates` instead of invoking the handler. Gate behind a node/graph attr
`memoize: true`. Persist the memo table in `checkpoint.json` so it survives resume.

**Key anchors / decisions:**
- Intercept before handler execution in the run loop near `handleOutcomeStatus`/`MarkCompleted`
  (`pipeline/engine_run.go:897,907`); store on success, replay on hit.
- `Outcome` (`pipeline/handler.go:17-68`) has **no JSON tags** — persist a serializable projection
  (`Status` + `ContextUpdates`); `pipeline/artifacts.go:14` has a JSON-tagged `ContextUpdates`
  analog to follow.
- Add the memo field to `Checkpoint` (`pipeline/checkpoint.go:14-65`), marshaled in `saveCheckpoint`
  (`pipeline/engine_checkpoint.go:34-63`).
- **Avoid the dippin-lang trap that sank #420:** surface `memoize` WITHOUT a dippin-lang field —
  use the generic `params:` pass-through (read `node.Attrs["memoize"]` / graph attr) and **verify
  `dippin doctor` tolerates it** (no new lint error). If dippin rejects an unknown attr, ask the user
  before adding a dippin-lang dependency.
- Be conservative on the hash: when in doubt, hash *more* of the resolved context (over-invalidate
  rather than replay stale). Document exactly what's hashed.

**Acceptance (#421):** memoized re-entry replays without invoking the handler (handler call count
== 1 across 2 entries); any hashed-input change invalidates and re-runs; memo survives checkpoint
resume; **off by default**, zero behavior change for `.dip` files lacking the attr.

### PR-C — Sleep-aware budgets + sub-node checkpointing  (#422)  — engine

**Problem:** (1) `BudgetGuard` counts **sleep time** — `MaxWallTime` and `stall_timeout` use
`time.Since` on timestamps stored as UnixNano (`pipeline/budget.go:85,98,104`), which strips Go's
monotonic reading, so a suspended laptop burns budget and can spuriously trip
`OutcomeBudgetExceeded` on resume. (2) Resume granularity is the whole completed node — a multi-turn
agent node (`Implement max_turns:50`, `build_product.dip:904`) interrupted mid-node loses all
in-progress turns (`agent/session.go:233-274` `runTurnLoop` holds `s.messages` in memory only;
`agent/checkpoint.go:11` is a prompt nudge, not state).

**Design:** (A) Sleep-aware budgets — make elapsed/stall accounting use an **injectable clock**
(interface) so a test can simulate a suspend gap; exclude detected wall-clock discontinuity from
`MaxWallTime` + `stall_timeout`; add `Pause()`/`Resume()` on `BudgetGuard` that subtract the
suspended span. (B) Sub-node checkpoint hook — let a long-running agent node persist intra-node state
(conversation/episode + working-tree SHA) at turn boundaries so resume re-enters mid-node. **Both
opt-in via graph attrs** (default = today's strict semantics, sleep counted, node-boundary resume).

**Scope note:** (A) is the cleaner, smaller lift (injectable clock + Pause/Resume + a clock-gap test)
and delivers ACs 1, 2, 4. (B) is heavier (Session must persist+rehydrate `s.messages`/episode and
the engine must resume mid-node). If (B) balloons, land (A) in this PR and split (B) to a tracked
follow-up — say so in the PR. Surface the new attrs via `params:`/graph attrs without a dippin-lang
change (verify `dippin doctor`).

**Acceptance (#422):** injected clock-gap does not advance `MaxWallTime`/`stall_timeout`; `BudgetGuard`
exposes Pause/Resume subtracting the suspended span; an agent node interrupted between turns resumes
with episode state intact; default behavior unchanged when attrs absent.

### PR-D — Sandbox device-node hygiene + git-artifact durability  (#423)  — engine

**Problem:** on resume after suspend, the sandbox's `/dev/null` became unreadable (breaking
`git hash-object -t tree /dev/null` at `build_product.dip:306,1484` and
`git diff --no-index --numstat /dev/null` at `:1050`), and `FinalCommit` failed with 'context
canceled' while the engine emitted only a soft `EventWarning` 'cannot preserve uncommitted work
(git artifact repository unavailable)' (`pipeline/engine_run.go:54-60`) — the never-lose-work
guarantee silently degraded to best-effort.

**Design:** (A) **Device-node hygiene** — at run start AND on resume, before the first git/subprocess
handler, probe standard device nodes (`/dev/null` readable+writable); attempt repair; **fail fast
with a specific diagnostic** if unrestorable — not a deep git error mid-`FinalCommit`. Hook into the
existing preflight surface (`pipeline/git_preflight.go:77` `Preflight`, called at
`cmd/tracker/run.go:201` and the resume path `:522`). (B) **Artifact-repo health check + reattach** —
before `commitWIPBeforeRouting`/`FinalCommit` rely on the artifact repo
(`pipeline/git_artifacts.go`, `commitWIPBeforeRouting` at `engine_run.go:49`), probe its
availability (e.g. `git rev-parse` in the artifact dir); if it went unavailable post-suspend, attempt
reattach/reinit to a recoverable ref and **surface a hard error** rather than degrading to the
nil-repo `EventWarning`.

**Acceptance (#423):** a stubbed device probe detects an unreadable `/dev/null` and emits a specific
diagnostic before the first git-dependent node; an injected unavailable artifact repo surfaces a hard
failure (after attempted recovery), not silent degradation; no behavior change when devices + repo
are healthy. (Note: `mknod`-class repair needs privileges — "verify + fail-fast diagnostic" is the
hard AC; repair is best-effort.)

---

## Orchestration with the Workflow tool (ultracode)

Stay in the loop: run **one Workflow per PR**, read its result, open the PR, then proceed. Do not try
to do all four in a single mega-workflow — you want a human-readable PR boundary and a checkpoint
between each.

**Phase 0 (you, inline, before any workflow):** read CLAUDE.md + the issues + architecture docs;
confirm the three "verify, don't trust" facts against the code (notably: #306 is OPEN — you are
building `behavioral-contracts.md`, not assuming it). The #418 baseline is already settled (see
Appendix A): use the 3.01M-INPUT-token figure from the #418 body as the authoritative before-number;
the evidence pack is purely optional color and is **not** blocking.

**Per-PR workflow shape** (author the script; these are the patterns to use):
- **Design panel** (engine PRs B/C/D especially — they are non-trivial): fan out 2-3 independent
  design proposals, score with a judge, synthesize one. For PR-A, a single design pass enumerating
  the per-node edits is enough.
- **Implement via TDD** in a **worktree** (`isolation: 'worktree'`) — write the failing
  test/scenario first, then the change. For B/C/D run them in parallel worktrees; for PR-A do the
  four issues **serially in one worktree** (shared file).
- **Adversarial verify** — after each PR's implementation, fan out reviewers that check, with
  file:line evidence: (1) every AC met; (2) **no under-correction** (e.g. #416's CONTRACT-FIDELITY
  must still FAIL a real fake-only seam; #421's memo must not replay a stale node; #423 must hard-fail
  a genuinely unavailable repo); (3) **no over-correction** (e.g. #417 must not let "reference the
  constant" satisfy the check trivially; #418 must not drop a rubric point; memoize/sleep-aware/device
  attrs must be **off by default**); (4) **genericity** — no branch on detected language/product
  (section D); (5) CLAUDE.md edges intact (no unconditional fallback to a loop target; strict-failure
  preserved; no swallowed errors); (6) gates green. Use a panel of distinct lenses, not N identical
  refuters. Act on the **consensus** (CLAUDE.md / squad-decides) before opening the PR.

**Per-PR verification gates (all must pass before you open the PR):**
- `go build ./...` and `go test ./... -short`; race detector on touched packages.
- For PR-A: `dippin doctor` **A** on all core examples (`ask_and_execute`, `build_product`,
  `build_product_with_superspec`) and `dippin simulate -all-paths` on the three core pipelines. If
  `dippin` is not on `PATH`, **ask the user** — do NOT `go install` it.
- For B/C/D: confirm `dippin doctor` still **A** (the new opt-in attrs must not regress lint) and
  that default behavior is unchanged with the attr absent.

---

## Hard constraints (from CLAUDE.md — these override defaults)

- **NEVER `git commit/push --no-verify`.** Fix the root cause. ⚠️ The pre-commit **complexity gate**
  (`gocyclo`/`gocognit -over 8`) scans the **whole staged file**, not just your diff — so editing
  `engine.go` / `engine_run.go` / `budget.go` / `git_artifacts.go` may surface **pre-existing**
  violations that block your commit. If that happens, reduce the flagged functions with minimal
  behavior-preserving extractions (this exact situation occurred in v0.40.2 on `human.go`); never
  bypass.
- **NEVER `go install` dippin-lang.** If a dippin-lang change seems required (it should NOT be for
  any PR here — surface new attrs via `params:`/graph attrs), STOP and ask the user. #420 was dropped
  precisely to avoid this.
- **Genericity (section D)** — no language/product detection anywhere.
- **Edge routing** — no unconditional fallback edge to a loop target (infinite loops); strict-failure
  edges intact; never silently swallow errors.
- **Opt-in / default-off** — #421 `memoize`, #422 sleep-aware + sub-node checkpoint, #423 nothing
  user-facing-default-changing: existing `.dip` files and existing runs behave identically.
- **CHANGELOG** — update `CHANGELOG.md` under `[Unreleased]` in the **same PR** (group Added /
  Changed / Fixed; cite each issue #).
- Each PR `Closes` its issue(s). End commit messages with the project `Co-Authored-By` trailer; end
  PR bodies with the `🤖 Generated with Claude Code` line. Branch off `main`; never commit to `main`
  directly.

## Suggested branches / PR titles
- `fix/review-fidelity-and-cost` → "build_product: contract-fidelity review + diff-scoped, tiered
  review panel (#416, #417, #418, #419)"
- `feat/node-output-memoization` → "engine: opt-in content-hash node-output memoization (#421)"
- `feat/sleep-aware-budgets` → "engine: sleep-aware wall-clock budgets + sub-node checkpointing (#422)"
- `feat/artifact-durability` → "engine: device-node hygiene + git-artifact durability (#423)"

## #353 disposition
Do not close it. Post a comment mapping which of its asks this workstream covers (#418 fan-out shape +
diff-scope, #419 tiering, #304 already shipped) and which remain as future work (per-backend caching
awareness; a per-session input-token ceiling distinct from cost). Let the user decide on closing.

## Definition of done
- Each PR's ACs demonstrably met (cite the test/scenario/prose-line proving each).
- `go build ./...`, `go test ./... -short`, race on touched packages: green for every PR.
- PR-A: `dippin doctor` A on all examples; `simulate -all-paths` clean on the three core pipelines.
- B/C/D: opt-in, default-off, no regression with the attr absent; `dippin doctor` still A.
- Each PR passed its adversarial review (no over/under-correction; genericity held).
- CHANGELOG updated; four PRs open, each linked to its closed issue(s); #353 comment posted.

---

## Appendix A — Token-baseline evidence pack (OPTIONAL — the #418 baseline is already settled)

**The #418 baseline is authoritative without artifacts:** the #418 body states **3.01M review INPUT
tokens / 3.10M total review tokens (90% of the 3.42M run)**. Use those numbers as the before-figure;
prove the reduction by construction + estimate (see #418 above), not by an expensive live re-run.
Everything else in this workstream is actionable from the tracker repo alone — #416's two cases are
reproduced verbatim in its body, and B/C/D are testable with injected clocks/stubs/fakes.

The evidence pack below is **purely optional color** — a per-reviewer token breakdown if you want to
target the diff-scope edit more precisely. The run artifacts live on the machine that executed the
dogfood run, not here. If you want it, have an agent with filesystem access there return this
(read-only):

```
You have filesystem access to a machine that ran tracker's build_product pipeline. For run
b65fb64ac099, locate the run dir by trying in order:
  $XDG_STATE_HOME/tracker/runs/b65fb64ac099/ , $HOME/.local/state/tracker/runs/b65fb64ac099/ ,
  ./.tracker/runs/b65fb64ac099/ , $TMPDIR/tracker-audit/b65fb64ac099/
From its activity.jsonl, emit a markdown table of every `cost_updated` provider_totals line
(provider, input tokens, output tokens, cost, cache read/write), the per-reviewer status.json
(ReviewClaude/ReviewCodex/ReviewGemini: turns, wall time, input tokens, caching), any
`context_window_warning` event, and the run total (tokens, cost, active wall time). Quote literal
JSON; do not paraphrase numbers. Output only the markdown.
```

Drop the result at `docs/plans/b65fb64ac099-token-baseline.md` (gitignored/scratch). If you can't get
it, that's fine — proceed with the 3.01M / 3.10M figures from the #418/#353 bodies; they are
sufficient for the by-construction argument.
