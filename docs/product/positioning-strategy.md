# Positioning, Naming & Product-Signal Strategy

**Status:** research synthesis + proposed plan · **Date:** 2026-08-21
**Scope:** issues #459 (positioning), #460 (naming/discoverability), #464 (research-tooling signal)
**Detailed dossiers:** [`research/459-positioning-trust.md`](research/459-positioning-trust.md) · [`research/460-naming-discoverability.md`](research/460-naming-discoverability.md) · [`research/464-research-tooling-signal.md`](research/464-research-tooling-signal.md)

---

## 1. Executive summary

All three issues came from a single 2026-07-05 two-persona expert audit and share **one meta-problem**: at the exact moment an external evaluator forms an impression, tracker *undersells itself*. The differentiators that matter are buried or invisible, the name is unsearchable, and the repo surface signals "research harness" as loudly as "product."

The three fixes reinforce each other and split cleanly by cost and reversibility:

| Issue | Recommendation | Cost | Reversible? |
|---|---|---|---|
| **#459 Positioning** | Reframe around **"safe to let run unattended"** — the trust bundle (budget caps + tamper-evident audit + `diagnose` + Linux sandbox), not features. The lane is **open**. | Low (copy + a few site sections) | Yes |
| **#460 Naming** | **Qualify now, gate the rename.** "Tracker by 2389" + `tracker.2389.ai` immediately; set explicit criteria for a later rename. | Free now / High if rename | Qualify: yes; rename: no |
| **#464 Tooling signal** | **Never split conformance** (it's a trust asset). Re-bucket changelog/CLI now; reframe as "how we verify tracker"; add the SWE-bench score once #465 is real. | Low now | Yes |

**The single highest-leverage dependency is #465** (first scored SWE-bench run): it converts `tracker-swebench` from noise into evidence (#464) *and* unlocks a "Benchmarks" proof point for the trust story (#459).

---

## 2. #459 — Positioning: lead with trust

### The finding: the trust lane is open
A live competitive scan (Aug 2026, 10+ tools) found the "trust / won't-blow-your-budget" position **essentially unoccupied**:

- **Durable-execution engines** (Temporal "as reliable as gravity," Inngest "Unbreakable Agents," Restate "innately resilient") are homogeneous on *durability* and **silent on cost**.
- **Agent frameworks** (Mastra, AutoGen — now maintenance-mode, CrewAI, Griptape) lead on *expressiveness / observability / enterprise governance*.
- **LangChain** is the only one bundling observe+govern+spend+sandbox — but it's cloud/LangSmith-coupled and frames spend as a sub-feature, not the headline.
- The one deliberate "your agents are burning budget" claimant (Portal26) is an adjacent governance product, not a runner.

Nobody assembles tracker's **local-first, single-binary, budget-caps + tamper-evident git-committed audit + `diagnose` + sandbox** combination at the hero level. The current hero leads with cost alone — a *single-feature* story a rival copies with one flag. Widening to the four-guardrail **bundle** is a far harder moat and matches what tracker actually ships.

### The honesty backbone (non-negotiable)
The positioning must not overclaim — security-literate readers are the target audience, and an overclaim destroys the trust it's selling:
- **Budget caps** — enforced *between nodes*: "halts between steps on breach," not exact-to-the-cent. All OSes.
- **Audit log** — **tamper-EVIDENT, not tamper-proof.** The sentinel detects casual injection (shell redirection, `tee -a`); a same-UID process can still delete/forge and deletions aren't detected. Never say "cryptographically verified."
- **`writable_paths` sandbox** — **Linux-only** (Landlock ABI v3, native backend), bounds *writes* only (not network/reads), refuses-to-start elsewhere. Always OS-qualify. macOS/FreeBSD = #281.
- **SWE-bench** — not scored yet (#465). Must **not** appear on the site until real.

### Options considered (full A–E in the dossier)
| Option | Hero | Wins | Loses / risk |
|---|---|---|---|
| **A. Trust-first ("safe to let run")** ← recommended | "Multi-agent pipelines you can trust to run unattended" | The open lane; matches what ships; hard to copy | Requires disciplined honesty copy |
| B. Cost-first (status quo, sharpened) | "Won't surprise you on the bill" | Concrete, proven | Single-feature; one-flag-copyable |
| C. Local-first / git-native | "Every run is a git commit" | True, differentiated | Narrower buyer; doesn't lead with the pain |
| D. Observability-first | "See every token, tool call, gate" | Crowded but strong | Directly contested by Mastra/LangSmith |
| E. Durable-execution challenger | "Reliable agent workflows" | Big category | Contested by Temporal et al.; not tracker's edge |

Recommendation absorbs B's cost story as proof-point #1 inside A's bundle.

### Plan (moves)
1. **Rewrite the hero** → "Multi-agent pipelines you can trust to run unattended," with three proof bullets: *halt at $5 not $500* · *git-committed, tamper-evident audit + `tracker diagnose`* · *(Linux) `writable_paths` sandbox*. (Full copy in dossier §5.2.)
2. **Surface the two invisible pillars.** Confirmed: the tamper-evident audit log and the sandbox appear **nowhere** on `_index.html`; `diagnose` is buried as install-step 3. Promote all four guardrails to named cards, each with a one-line honest-limits note.
3. **Add a `/trust` page** linking the threat-model docs (#213/#272/#281), a GitHub-stars badge, and a reserved "Benchmarks" slot for the SWE-bench number *once #465 lands*.

---

## 3. #460 — Naming & discoverability

### The finding: "Tracker" is in the worst naming quadrant
It's a generic dictionary word already owned in search by issue trackers, time trackers, package/GPS/fitness tracking — so it gets the *downside* of a generic name (unrankable, "tracker *what*?" in speech) with **none** of the keyword-SEO upside (nobody searches "tracker" hoping to find an LLM pipeline engine). ~87% of YC startups pick brandable names; the practice runs the other way.

### Switching cost is real, bounded, and already scoped
The name is load-bearing across three surfaces:
- **Code:** module path `github.com/2389-research/tracker`, binary `tracker`, `TRACKER_*` env vars, `.tracker/` state dirs.
- **Distribution:** brew tap `2389-research/tap/tracker`, `go install …/cmd/tracker`, GoReleaser.
- **Web/brand:** `2389-research.github.io/tracker`, ~195 doc references, JSON-LD.

Crucially, **the project already did a full rename once** (`mammoth-lite → tracker`, see `docs/plans/2026-03-06-tracker-rename-design.md`) — that plan is a ready-made checklist. The cost that *compounds monthly* is external (stranded backlinks, published binaries), which is exactly why "decide before growth" is the right framing.

### Precedents
- **PrestoSQL → Trino** — forced/rushed/disruptive, but the coined name was strictly better.
- **ZEIT → Vercel** — voluntary, timed to a funding inflection, huge upside.
- Pattern: every coined outcome beat its generic predecessor; cost is always the same three buckets (duplicated package/URL surface, a dual-name transition, community confusion) and smaller the earlier you act.

### Options
| Option | What | Cost | Notes |
|---|---|---|---|
| **A. Qualify-only** ← do now | "Tracker by 2389" everywhere + own a domain, keep the name | Free (copy) | Low-regret, reversible |
| **B. Full rename** | Coined name → new module/binary/domain | High | Use the mammoth-lite checklist; only if the gate fires |
| **C. Brand ≠ command** (underrated middle path) | Distinct *product brand*, keep the `tracker` binary/module | Medium | The Vercel pattern — brand upside without breaking `go install`/brew/on-disk state |
| D. Status quo | — | Compounds monthly | Not recommended |

**The audit only named A vs B; Option C is the one to surface** — it captures most of the rename upside at a fraction of the switching cost.

### "Dippin"
A minor, *separable* liability. It's a coined, ownable, searchable word (everything "tracker" isn't) in a **separate** upstream repo. Keep it; just qualify the first mention ("Dippin, our pipeline DSL"). Don't rename it standalone.

### Rename decision gate (tip toward rename when ≥2 fire)
1. An imminent public launch / funding / marquee-adopter inflection.
2. Observed word-of-mouth or attribution friction in the wild.
3. A trademark / collision threat.
4. A clean coined name clearing **all** availability checks (domain + GitHub + package + USPTO/EUIPO).

If it fires, **prefer Option C** unless the command itself must change.

### Immediate low-regret moves
1. Standardize on **"Tracker by 2389"** in titles / metadata / README H1 (copy only).
2. Stand up **`tracker.2389.ai`** (free subdomain) redirecting to docs; add a `CNAME`.
3. Qualify "Dippin" once at first mention.
4. Recon 3–5 coined candidate directions + availability (feeds the gate, commits to nothing).

---

## 4. #464 — Research-tooling signal

### The finding: the two binaries are asymmetric — don't treat them as one problem
- **`tracker-conformance`** (~2,670 LOC) is a genuine **trust asset**, not clutter. It ships as a GoReleaser release binary; its golden traces are versioned **in lockstep with the `tracker` tag** so downstream ports pin-and-diff (embedding.md §5). It imports internal engine packages **by design** — it's the executable snapshot of engine behavior. Splitting it destroys the lockstep atomicity that is its entire value. **Never split it.**
- **`tracker-swebench`** (~4,298 LOC) is the weaker signal and the real source of the "research harness?" read. It's **not** a release asset (build-from-source + Docker) and imports `agent.Session` internals. It becomes *evidence* only once **#465** publishes a real score; before then, spotlighting it amplifies the clutter #464 flags.
- Combined ~2.8% of the codebase — dilution is **positional**, not volume: 5 peers in `cmd/` where only 3 are product; ~30 interleaved CHANGELOG lines; a cli.html "companion binary" section. README and ARCHITECTURE already read clean.

### Precedents
- **Split** (SWE-bench org, LangChain, DuckDB/H2O) happens for *cross-system/neutral* benchmarks — doesn't fit tracker's *self*-verification.
- **Keep-in-repo-as-trust** (SQLite "How SQLite Is Tested," Rust `compiletest`, CNCF Certified Kubernetes, wpt.fyi) is exactly conformance's class. Go's own layout endorses `cmd/<name>/` aux binaries.

### Recommendation & sequence
1. **Now** — re-bucket changelog + `cli.html` into a `### Tooling & verification` group (cheapest high-value move; fully reversible; touches no build/release).
2. **Now** — a light "How we verify tracker" doc framing conformance as the drift guarantee, SQLite-style.
3. **Gated on #465** — add the SWE-bench score to flip swebench from noise → evidence.
4. **Reserve** a repo-split for **swebench only**, and only if it still distracts after reframing.

---

## 5. Unified phased plan

### Phase 0 — Free / low-regret (this week, no decisions required)
- [ ] Rewrite the hero around trust; surface the four guardrails as named cards with honest-limits notes (#459).
- [ ] Standardize "Tracker by 2389" in copy/metadata; qualify "Dippin" once (#460).
- [ ] Stand up `tracker.2389.ai` → docs redirect + `CNAME` (#460).
- [ ] Re-bucket changelog + cli.html into "Tooling & verification"; draft "How we verify tracker" (#464).
- [ ] Add a GitHub-stars badge; reserve a "Benchmarks" slot (empty until #465).

### Phase 1 — Gated decisions (team calls; criteria defined above)
- [ ] Recon coined name candidates + availability → feeds the **rename gate** (#460).
- [ ] Decide qualify-forever vs Option C (brand ≠ command) vs full rename, per the ≥2-criteria gate.
- [ ] Build the `/trust` page + link the threat-model docs (#459).

### Phase 2 — Dependency-unlocked (after #465)
- [ ] Publish the first scored SWE-bench Verified number.
- [ ] Add it to the site "Benchmarks" slot (#459) **and** flip swebench to a documented trust asset (#464).
- [ ] Revisit whether swebench still distracts → split only if so (#464).

### Cross-cutting dependencies
- **#465 (scored SWE-bench)** — unlocks Phase 2 for both #459 (proof) and #464 (evidence). Highest-leverage prerequisite.
- **#281 (macOS/FreeBSD sandbox)** — bounds the honest scope of #459's sandbox claim; until then, every sandbox mention is OS-qualified to Linux.
- **Domain acquisition** — gates the `tracker.2389.ai` move and informs the rename decision.

---

## 6. Open decisions for the team (genuine judgment calls)
1. **Rename: whether and when.** Phase-0 qualifying is unambiguous; the rename is a real bet — decide against the gate criteria, not aesthetics. If yes, strongly consider **Option C** first.
2. **Domain / brand.** `tracker.2389.ai` now; a distinct owned domain is a prerequisite for any rename.
3. **"Dippin".** Keep-and-qualify is recommended; a standalone rename is not worth it.
4. **swebench relocation.** Deferred behind reframing + #465; revisit only if it still distracts.

All claims in this document are grounded in the three dossiers under `research/`, which carry the competitor taglines, precedent case studies, per-file source references, and the full options with pros/cons.
