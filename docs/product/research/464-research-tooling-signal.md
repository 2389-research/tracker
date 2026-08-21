# #464 — `tracker-swebench` + `tracker-conformance`: research-signal dilution

**Status:** Research dossier (decision input, not a decision)
**Date:** 2026-07-28
**Issue:** [#464](https://github.com/2389-research/tracker/issues/464) — the two companion binaries read as research clutter to an external evaluator sizing up tracker as a product.

## The question

An external evaluator scanning the repo, README, and release notes hits `cmd/tracker-swebench/` and `cmd/tracker-conformance/` (plus their CHANGELOG entries) and asks: *is this a product for my workflows, or a research harness for the team's benchmark runs?* The signal dilutes the product story at the evaluation moment. Three options are floated:

1. **Split** the harnesses into separate repos (cleanest signal; conformance already ships its own release binary).
2. **Reframe** in docs as a trust asset ("how we continuously verify tracker") — benchmarks become evidence, not noise.
3. **Changelog separation** — at minimum, keep their release-note entries in a distinct section so product-facing notes stay clean.

Bottom line up front: **the two binaries are not symmetric, and should not be treated as one problem.** Conformance is already a trust asset that *must* stay in-repo; swebench is the weaker signal. The recommended sequence is 3 → 2 → (defer 1 for swebench only), gated on #465.

---

## 1. Ground truth — what these actually are

### Size and shape

| Binary | Impl LOC | Test LOC | Total | Release asset? | Coupling |
|---|---|---|---|---|---|
| `cmd/tracker-swebench/` (+ `agent-runner/`) | 2,680 | 1,618 | 4,298 | **No** — build-from-source + Docker only | imports `agent`, `agent/exec`, `llm`, `llm/openai`, and `tracker` |
| `cmd/tracker-conformance/` | 1,541 | 1,129 | 2,670 | **Yes** — GoReleaser builds + archives it for darwin/linux amd64/arm64 | imports `pipeline`, `pipeline/handlers`, `agent`, `agent/tools`, `agent/exec`, `llm/*` |

Combined ~6,968 LOC of a ~245,280-LOC non-test Go codebase — **~2.8%** of the surface. Not a bloat problem by volume. The problem is *positional*: they sit in `cmd/` next to `tracker`, `trackerbot`, `trackerchat` (the real product binaries), so a `cmd/` listing reads five peers where only three are the product.

### Coupling — this is the crux

Both binaries import tracker's **internal** (non-public-API) packages, not just the supported `tracker.*` library seam:

- **conformance** is *intentionally* coupled: it is the **executable snapshot of engine behavior**. `tracker-conformance golden <fixture.dip>` emits a normalized, deterministic trace (event sequence, per-node `SessionStats`, aggregate `UsageSummary`, terminal status/class) via a stub completer. The committed goldens at `cmd/tracker-conformance/testdata/golden/*.golden.json` are **versioned in lockstep with the `tracker` tag** — GoReleaser ships the binary precisely so downstream ports can pin a version and diff against it (see `docs/architecture/embedding.md` §5). Regeneration (`go test ./cmd/tracker-conformance -run TestGoldenTraces -update-golden`) happens **in the same commit** as an intentional engine change. This is the release checklist's own gate (`CLAUDE.md` → Before releasing).
- **swebench** couples to `agent.Session` directly (`agent-runner` constructs a session with the standard coding-tool set inside a Docker container). Per its design spec (`docs/superpowers/specs/2026-04-16-swebench-harness-design.md`), it benchmarks **Layer 2 (the agent) discretely** — no pipeline, no TUI, no `.dip`. It reaches into `agent`/`agent/exec`, not the public `Run` seam.

**Implication for splitting:** neither harness sits on the public `tracker.*` API. A split repo would either (a) pin `tracker` as a module dependency and import its *internal* packages — impossible, Go blocks cross-module `internal/` imports; or (b) force those packages to become public API — a large, unwanted surface commitment; or (c) accept cross-repo version drift with a vendored copy. Conformance additionally loses **monorepo atomicity** exactly where atomicity is the whole point: golden traces regenerated in lockstep with the engine change that altered them.

### Current doc footprint

- **README.md:** mentions *neither* binary. The product story is already clean here — it leads with `tracker run`, embedded workflows, and the `trackerbot`/`trackerchat` transports. Good.
- **ARCHITECTURE.md:** mentions neither. Clean.
- **CHANGELOG.md:** 15 lines reference swebench, 15 reference conformance, interleaved into normal `### Added/Changed/Fixed` groups across the 4,692-line history. This is the actual leak into product-facing notes. No dedicated verification/tooling subsection exists today (one prior entry improvised an "Aux binaries/examples:" bullet prefix — evidence the need is already felt).
- **site/content/cli.html:** a full `tracker-swebench` "companion binary" section (run + analyze subcommands).
- **site/content/roadmap.html / ROADMAP.md:** #464 itself is listed under "Product & positioning"; #465 (first scored SWE-bench Verified run) under a "SWE-bench first score" heading.
- **docs/architecture/embedding.md §5:** conformance documented as the downstream-port drift check (a *supported seam*, not clutter).

**Takeaway:** the README/ARCHITECTURE product story is already uncluttered. The dilution is concentrated in (1) the `cmd/` directory listing, (2) interleaved CHANGELOG entries, and (3) the cli.html companion section. These are the cheap, high-value surfaces to fix.

---

## 2. Precedents — how comparable OSS projects handle this

### (a) Split into a separate repo

- **SWE-bench ecosystem** ([SWE-bench/SWE-bench](https://github.com/SWE-bench/SWE-bench), [SWE-agent](https://github.com/SWE-bench/SWE-agent), [sb-cli](https://github.com/swe-bench/sb-cli)): one repo per concern — dataset+harness, agent scaffold, cloud-eval CLI kept apart. The rationale is inferable, not stated: the *benchmark* stays neutral/stable while *agents* evolve independently (they even keep the test split private via sb-cli to prevent overfitting). Directly relevant: this is the "keep the scaffold out of the benchmark" pattern, and it's the very benchmark tracker-swebench targets.
- **LangChain** ([langchain-benchmarks](https://github.com/langchain-ai/langchain-benchmarks)): benchmarks in a standalone package, datasets hosted in LangSmith (their commercial eval product). The split doubles as a funnel to the eval product. Caveat: low-activity repo — a *pattern* example, not a maintained flagship.
- **DuckDB / H2O.ai db-benchmark** ([duckdblabs/db-benchmark](https://github.com/duckdblabs/db-benchmark)): DuckDB maintains the cross-system benchmark in a *separate* repo because it spans competitors (Polars, ClickHouse, data.table). Cross-system comparison belongs outside the product repo as neutral ground.
- **TechEmpower FrameworkBenchmarks** ([repo](https://github.com/TechEmpower/FrameworkBenchmarks)): one external neutral repo hundreds of frameworks contribute to. *Cite as historical — archived/sunset 2026-03-24.*

**Pattern:** projects split benchmarks out when the harness is **cross-system/neutral** (comparing competitors) or when the scaffold must **evolve independently** of a frozen benchmark. Neither strongly applies to tracker: conformance is *self*-verification (not cross-system), and swebench benchmarks *our own* agent against a third-party dataset.

### (b) Kept in-repo as trust/verification evidence

- **SQLite** ([How SQLite Is Tested](https://sqlite.org/testing.html), [TH3](https://sqlite.org/th3.html)): the canonical example. Testing is presented publicly as the product's core credibility — 100% MC/DC branch coverage (DO-178B avionics standard), millions of test instances, differential testing against Postgres/MySQL/SQL Server/Oracle. Testing *is* the moat.
- **Rust compiler** ([rustc-dev-guide tests](https://rustc-dev-guide.rust-lang.org/tests/intro.html)): large in-repo suite driven by in-tree `compiletest` (`src/tools/compiletest`), framed publicly via the dev guide as the correctness guarantee.
- **Kubernetes / CNCF Conformance** ([k8s-conformance](https://github.com/cncf/k8s-conformance), [Certified Kubernetes](https://www.cncf.io/training/certification/software-conformance/)): conformance tests live in the main test suite; CNCF turns passing them into a trademark/badge with annual recertification. Conformance-as-brand-guarantee.
- **web-platform-tests** ([wpt](https://github.com/web-platform-tests/wpt), [wpt.fyi](https://wpt.fyi/about)): cross-engine conformance suite each vendor runs, results on a neutral public dashboard.

**Pattern:** a *self-verification* / *conformance* suite is a credibility asset when it stays close to the code it verifies and is presented for the buyer. This is exactly tracker-conformance's situation.

### (c) Monorepo with clear top-level separation

- **Go standard layout** ([go.dev/doc/modules/layout](https://go.dev/doc/modules/layout), [golang-standards/project-layout](https://github.com/golang-standards/project-layout)): auxiliary + benchmark binaries each get `cmd/<name>/`; the directory boundary *is* the separation. Multiple binaries in one repo is idiomatic Go. **tracker already follows this** — the fix is signal/labeling, not restructuring.
- **Rust monorepo:** `compiler/` + `library/` (product) vs `tests/` + `src/tools/` (verification) — clean top-level product-vs-verification split in one repo.
- **DuckDB main repo** ([duckdb/duckdb](https://github.com/duckdb/duckdb)): top-level `benchmark/` + `test/` alongside `src/` — visibly separated within one repo. (Verify exact dir name before citing precisely.)

### (d) Trust-asset framing — verification as credibility, not noise

- **SQLite testing page** — prose *for decision-makers*, quantified hard-to-fake claims, safety-critical lineage.
- **DuckDB "Benchmarking Ourselves over Time"** ([blog](https://duckdb.org/2024/06/26/benchmarks-over-time)): deliberately reframes away from biased cross-system comparison toward *self-over-time* metrics (improvement velocity, workload breadth, scale), and publishes raw results as a queryable database. Honesty about benchmark bias *increases* credibility.
- **CNCF Certified Kubernetes mark** — passing → trademark + public listing; annual recert keeps it fresh.
- **wpt.fyi / OSS-Fuzz badges** — neutral/third-party hosting and continuous freshness are what make them read as objective.

**What makes verification read as strength, not clutter (synthesis):**
1. Third-party/neutral or externally-verifiable hosting beats self-report.
2. Quantified, hard-to-fake specifics beat "well-tested."
3. Continuously updated / dated signals live, not a one-time stunt.
4. Audience-appropriate framing — written for the buyer answering "why trust this."
5. Honesty about bias paradoxically increases credibility.

---

## 3. The trust-asset angle — does reframing beat relocating?

tracker already holds **two** verification assets most products would kill for:

- **Golden-trace conformance** (`tracker-conformance`): a mechanical, deterministic guarantee that engine behavior doesn't silently drift across versions — the executable contract downstream ports pin against. This is *exactly* the SQLite/CNCF/WPT category: self-verification kept close to the code.
- **A SWE-bench harness** (`tracker-swebench`): the machinery to produce an industry-standard, externally-comparable agent score.

Reframing is strictly stronger than relocating *for conformance*, because relocating it **destroys** the property that makes it valuable (lockstep-with-tag atomicity + shipping as a pinnable release artifact). The correct move is to stop presenting it as a stray `cmd/` binary and start presenting it as "how we prove tracker's engine doesn't drift" — the buyer-facing framing SQLite pioneered.

For swebench the trust framing is **latent until #465 lands a real score.** A harness with no published number is machinery, not evidence — it reads as exactly the "research clutter" #464 names. The asset materializes the moment there is a *number* ("tracker's agent resolves X% of SWE-bench Verified"). **This is the key dependency: #465 converts swebench from noise into evidence.** Until then, the honest move is to keep it low-profile, not to spotlight it.

---

## 4. Options — steps, effort, cost, gain, reversibility

### Option 1 — Split into separate repo(s)

**Steps:** create `tracker-swebench` (and/or `tracker-conformance`) repo; resolve the internal-import problem (promote `agent`/`pipeline` internals to public API, or vendor, or accept drift); for conformance, rebuild release-pipeline surgery (GoReleaser currently builds+archives+checksums it; move that to the new repo's release, wire golden-regeneration to the engine tag across repos); update embedding.md §5 and downstream pin instructions.

**Effort:** High (conformance), Medium (swebench).
**Cost:**
- Conformance: **loses monorepo atomicity** exactly where it matters — golden traces can no longer be regenerated in the same commit as the engine change; cross-repo version-pin dance for every intentional behavior change; release-pipeline surgery. This actively *degrades* a working trust asset. **Recommend against.**
- swebench: cross-repo version drift against `agent.Session`; loses the "one clone runs everything" story; every agent-internals change becomes a two-repo change.
**Gain:** Cleanest `cmd/` signal — the product repo shows only product binaries.
**Reversibility:** Low. Splitting a repo and re-merging is expensive; downstream pins and release automation ossify around the split.

### Option 2 — Reframe in docs as a trust asset

**Steps:** add a short "How we verify tracker" section (README subsection + a `site/content/` page or block) covering golden-trace conformance (with the concrete guarantee: deterministic engine-behavior snapshot, pinned per tag) and — once #465 lands — the SWE-bench score. Reframe the cli.html swebench section from "companion binary you might run" to "how we benchmark the agent." Optionally a README badge once a score exists.

**Effort:** Low–Medium.
**Cost:** Writing time; a *premature* swebench score claim would backfire (hence gate on #465). Slight risk of over-claiming — keep copy honest (conformance "detects drift," it is not tamper-proof; SWE-bench number dated + reproducible).
**Gain:** Converts the strongest existing asset (conformance) from ambiguous clutter into a credibility signal, following the SQLite/CNCF/DuckDB playbook. Directly answers the evaluator's "is this serious?" with "yes — here's how we prove it."
**Reversibility:** High. Docs-only; trivially revised.

### Option 3 — Changelog (+ cli.html) separation

**Steps:** adopt a convention — route verification/benchmark/tooling entries into a dedicated `### Tooling & verification` (or `### Benchmarks & conformance`) group under each CHANGELOG version, below Added/Changed/Fixed. Optionally relocate the cli.html swebench block under a "Verification & benchmarks" heading rather than inline with product CLI. Document the convention in the changelog contributor note.

**Effort:** **Low** (a convention + a one-time re-bucket of the ~30 existing lines if desired; going forward it's just discipline).
**Cost:** Negligible. One more changelog group to maintain.
**Gain:** Product-facing release notes stay clean — a reader scanning "what's new for me" no longer trips over agent-runner timeout-classification internals. **Highest value-per-effort.**
**Reversibility:** High. Pure convention.

---

## 5. Recommendation + plan

**Do not split. Sequence the two cheap, reversible moves now; reframe as the payoff lands; keep split in reserve for swebench only.**

The two binaries are asymmetric and the issue conflates them:

- **conformance** is a *trust asset that must stay in-repo* — splitting it destroys the lockstep-with-tag atomicity that is its entire value (embedding.md §5, release checklist). Its precedent class is SQLite/CNCF/WPT (keep-in-repo, frame as credibility), not SWE-bench-the-dataset (split for neutrality).
- **swebench** is the weaker signal and the real source of the "research harness" read — but it becomes *evidence* the moment #465 publishes a score. Spotlighting it before then would amplify the exact clutter #464 flags.

### Plan

1. **Now — Option 3 (changelog + cli.html separation).** Cheapest high-value move. Add a `### Tooling & verification` changelog group convention; re-bucket the ~30 existing swebench/conformance lines under it; move the cli.html swebench block under a verification heading. Product-facing notes stop leaking research internals. Fully reversible.
2. **Now — light Option 2 for conformance only.** Add a short "How we verify tracker" doc block (README subsection + site page) framing golden-trace conformance as the buyer-facing drift guarantee, following SQLite's "written for the decision-maker" model. This is already true and shipping — just present it as the asset it is. Do **not** yet spotlight swebench.
3. **Gated on #465 — complete Option 2 for swebench.** When the first scored SWE-bench Verified run lands, add the number (dated, reproducible, honest about the dataset) to the "How we verify" section and, if desired, a README badge. This is the step that flips swebench from clutter to evidence.
4. **Reserve Option 1 (split) for swebench only, and only if it stays a distraction after 1–3.** If, after reframing, swebench still muddies the `cmd/` signal, split *just swebench* into its own repo (it's the less-coupled, non-release-asset one — Medium effort, and it matches the SWE-bench ecosystem's own one-repo-per-concern pattern). **Never split conformance.**

### Dependencies

- **#465 (first scored SWE-bench Verified run) is the pivotal dependency** — it is what makes the swebench trust-asset framing real. Steps 1–2 do not depend on it; step 3 does.
- Step 3's copy should follow the honesty discipline in the precedents (SQLite/DuckDB): quantified, dated, reproducible, no over-claiming.
- Any conformance doc copy must not claim more than the activity-log/golden-trace threat model supports (detection of drift, not tamper-proof authentication).

### The cheapest high-value move

**Option 3 (changelog + cli.html re-bucketing).** Low effort, fully reversible, and it directly fixes the concentrated leak (interleaved release notes) without touching the build, the release pipeline, or the working conformance trust asset.
