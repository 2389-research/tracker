# Naming & Discoverability Dossier (Issue #460)

**Status:** research / decision-support. Not a decision.
**Scope:** the product name "Tracker" and the DSL name "Dippin" (`.dip`).
**Date:** 2026-08-21

---

## 0. TL;DR

- **Recommended path: Option A now, gate Option B.** Do the low-regret moves
  immediately — qualify the name to **"Tracker by 2389"** everywhere in copy, and
  **acquire a distinctive domain + redirect** to the GitHub Pages site. These are
  cheap, reversible, and improve discoverability whether or not a rename ever
  happens. Then set an explicit **decision gate** for a full rename (Option B),
  triggered by growth signals, not by aesthetics.
- **"Tracker" is a genuinely bad organic-search / word-of-mouth name** — the word
  is owned by issue trackers, time trackers, GPS/package tracking, fitness
  trackers. A generic single dictionary word for a dev tool is close to
  un-ownable in search. This is the strongest argument for eventual rename.
- **The switching cost is real but bounded and known** — this project has
  **already done a full in-place rename once** (`mammoth-lite` → `tracker`, see
  `docs/plans/2026-03-06-tracker-rename-design.md`). The checklist exists. The
  compounding cost is *external* surface (module path, brew tap, published
  binaries, SEO/backlinks), which grows every month adoption grows — hence the
  "decide before serious growth" framing is correct.
- **"Dippin" is a smaller, separable problem.** It reads as playful, which is a
  liability in enterprise evaluation but a mild one — and it's a *separate
  upstream repo* (`2389-research/dippin-lang`), so its rename cost is decoupled.
  Recommend: keep the name, **qualify the first mention** ("Dippin, our pipeline
  DSL"), and never rely on `.dip`/"Dippin" carrying the professional first
  impression alone.

---

## 1. Ground truth — where the name lives (switching-cost inventory)

The name "tracker" is load-bearing across code, distribution, and web surfaces.
A rename ripples through all of the following. This is the switching-cost
inventory, grouped by how hard each is to change (internal string edits are
cheap; anything with an external contract or public URL is expensive because old
references persist forever).

### 1a. Code / module surface (internal — cheap to edit, in one pass)
| Surface | Current value | Notes |
|---|---|---|
| Go module path | `github.com/2389-research/tracker` (`go.mod`) | Every internal import; **breaking** for `go install` / library importers (`docs/embedding.md`, `docs/api-stability.md`). A module-path change is a new import path — old pins keep resolving the old path. |
| Primary binary | `tracker` (`cmd/tracker`) | Help text, error strings, `commandMode` names, `__jail-exec` re-exec of `/proc/self/exe`. |
| Secondary binaries | `tracker-conformance`, `tracker-swebench`, `trackerbot`, `trackerchat` | Each is a separate published/entry name. |
| Env vars / paths | `TRACKER_AUDIT_DIR`, `TRACKER_GATEWAY_URL/KIND`, `TRACKER_RUN_ID`, `TRACKER_PASS_ENV`, `$XDG_STATE_HOME/tracker/runs/...`, `.tracker/` artifact dir | **Compatibility-sensitive** — renaming these breaks existing runs/checkpoints/state dirs on users' disks. Would need dual-read fallback. |
| Golden traces / conformance fixtures | ship in lockstep with tags (`docs/embedding.md` §5) | String churn but mechanical. |

### 1b. Distribution surface (external — expensive, old refs persist)
| Surface | Current value | Cost of change |
|---|---|---|
| Homebrew tap | `brew install 2389-research/tap/tracker` | Formula rename; old formula must alias or users' `brew upgrade` breaks. |
| `go install` | `github.com/2389-research/tracker/cmd/tracker@latest` | Old path keeps serving old versions forever; new path starts from zero. |
| GoReleaser | `.goreleaser.yml` builds `tracker` + `tracker-conformance` | Asset names in every past GitHub release stay `tracker_*`. |
| GitHub repo | `2389-research/tracker` | GitHub auto-redirects renamed repos (soft landing) but `go get` on the old path can break; stars/URL identity move. |

### 1c. Web / brand surface (external — SEO + backlinks, the compounding cost)
| Surface | Current value | Cost of change |
|---|---|---|
| Site URL | `https://2389-research.github.io/tracker/` (`site/hugo.toml` `baseURL`, `uglyURLs=true`) | Every page URL, sitemap, RSS, JSON-LD `url`/`name` ("Tracker", `SoftwareApplication`). No owned domain = **no redirect control today** (GH Pages path is fixed). |
| Doc corpus | ~195 files under `site/content`, `docs/`, `README.md` reference "tracker" | Mechanical rewrite; the prior rename already did exactly this once. |
| Backlinks / mentions | changelog, any external write-ups, HN/social | Cannot be edited by us — this is the SEO "leakage" that compounds monthly. |

**Key inference:** the *internal* surface (1a) is a solved problem — the
`mammoth-lite → tracker` rename plan (`docs/plans/2026-03-06-tracker-rename-design.md`)
is a ready-made checklist. The *external* surface (1b/1c) is where cost accrues
and why timing matters: every new star, brew install, blog mention, and backlink
under "tracker" is a reference that a rename strands.

---

## 2. The collision / SEO reality

**"Tracker" is one of the most contested generic nouns in software.** Organic
search for the bare word returns: issue trackers (Jira, Linear, GitHub Issues,
"Pivotal Tracker" — itself a real product), time trackers (Toggl et al.),
package/shipment tracking, GPS/asset trackers, fitness trackers, ad/privacy
trackers. A new dev tool has effectively zero chance of ranking for its own name,
and — as #460 notes — word-of-mouth breaks at the first syllable ("have you tried
tracker?" → "tracker *what*?").

Research consensus on generic vs. coined names for tools:

- **Descriptive/keyword names help only when discovery is search-intent driven
  for a *category keyword*** ("json formatter", "cron parser") — the user
  searches the *function*, not the brand. That is *not* how a differentiated
  orchestration engine gets found; nobody searches "tracker" hoping to find an
  LLM pipeline runner. So Tracker gets the *downside* of a generic name
  (unrankable, un-word-of-mouthable) without the keyword-SEO *upside*.
  ([DomainDetails / Namestall on utility-site naming](https://www.namestall.com/blog/naming-a-single-purpose-utility-site-what-a-niche-domain-strategy-actually-looks-like/),
  [Namemesh: brandable vs keyword](https://www.namemesh.com/guides/domain-naming/brandable-vs-keyword-domains))
- **Coined/brandable names win for word-of-mouth, memorability, trademark, and
  handle/domain availability.** An analysis cited across naming guides found **87%
  of YC startups chose brandable names and 59% used coined/invented words** — the
  dominant modern pattern precisely because generic words are un-ownable.
  ([Bizonym](https://bizonym.com/how-to-choose-between-a-coined-name-keyword-name-and-founder-name/),
  [SmallBizTrends](https://smallbiztrends.com/2009/04/brand-name-descriptive-unique.html))
- **Qualifying ("X Tracker") is a partial fix.** It creates a unique *two-token*
  string ("2389 Tracker", "Tracker by 2389") that *is* searchable and
  attributable, without a full rename. It doesn't fix the spoken-word ambiguity
  as well as a coined name, but it's the highest-leverage cheap move.
  ([FasterCapital naming best practices](https://fastercapital.com/term/naming-best-practices.html),
  [GitHub SEO / Nakora](https://nakora.ai/blog/github-seo))

**Bottom line:** Tracker sits in the worst quadrant — a generic word that is
neither the searched category keyword nor a distinctive brand. Qualification
lifts it out of un-attributability; only a coined name lifts it out of the
spoken-word failure.

---

## 3. Rename precedents (what it costs, what it buys)

### Forced renames — trademark/collision (the expensive kind)
- **PrestoSQL → Trino (Dec 2020).** The Presto founders left Facebook, forked,
  and were *forced* off the "Presto" name after the Linux Foundation acquired the
  trademark. They rebranded to the coined word **Trino** on a *short deadline with
  little ability to minimize user disruption* — the founders publicly called it
  "an incredibly sad and disappointing turn of events." Yet: the coined name is
  now cleanly ownable, unambiguous vs. PrestoDB, and the project kept ~3× the
  development velocity of the original. **Lesson:** a forced, rushed rename to a
  coined word is survivable *and* the coined name is strictly better long-term —
  but the disruption (docs, packages, community confusion, dual-name period) is
  real and best done *before* you're forced.
  ([Trino announcement](https://trino.io/blog/2020/12/27/announcing-trino.html),
  [Starburst](https://www.starburst.io/blog/prestosql-becomes-trino/),
  [HN thread](https://news.ycombinator.com/item?id=25566055))

### Voluntary renames — brand clarity (the strategic kind)
- **ZEIT → Vercel (Apr 2020).** Voluntary rebrand from a generic German word
  ("zeit" = time — itself un-searchable and mis-perceived) to a coined word built
  to evoke *versatile / accelerate / excel*, timed with a Series A. The coined,
  developer-typable, globally-unambiguous name coincided with a fundraising and
  growth inflection ($21M → $40M → $102M → $1.1B valuation). **Lesson:** renaming
  from a generic/ambiguous word to a coined brand is a recognized growth move, and
  the *right time is at/just-before an inflection*, bundled with a positive
  narrative — not reactively.
  ([Vercel: ZEIT is now Vercel](https://vercel.com/blog/zeit-is-now-vercel),
  [Lexicon Branding case study](https://www.lexiconbranding.com/case-studies/startup-branding-zeit-vercel/))

### Open-source rename mechanics / trademark
- Open-source licenses grant copy rights but **generally exclude trademark
  rights**; a project can hold and enforce a mark. Renames of volunteer projects
  carry a *community-consensus* cost on top of the mechanical one (openSUSE/SUSE,
  Debian/Firefox→Iceweasel precedents). For a single-vendor project like Tracker
  (2389.ai owns it), that governance cost is low — the decision is 2389's alone.
  ([TermsFeed](https://www.termsfeed.com/blog/open-source-trademark/),
  [Google Open Source Casebook: Trademarks](https://google.github.io/opencasebook/trademarks/))

**Synthesis of precedents:** every coined-name outcome (Trino, Vercel) ended up
*better-branded* than the generic/ambiguous predecessor. The cost is always the
same three buckets — **broken/duplicated package+URL surface, a dual-name
transition period, and community confusion** — and it is *smaller the earlier you
do it*. That directly supports #460's "cost compounds monthly."

---

## 4. The "Dippin" question

**Is "Dippin" a real liability?** Mild and separable. In enterprise/professional
evaluation, a playful DSL name can register as "unserious," but:
- It's the *DSL/file-extension* name (`.dip`), not the product a buyer evaluates
  first. The product name carries the professional first impression; the DSL name
  is encountered *after* someone is already reading docs.
- It's a **coined, distinctive, ownable word** — which is exactly what "tracker"
  is *not*. "Dippin" is searchable and word-of-mouth-able. That's a real asset.
- It lives in a **separate upstream repo** (`2389-research/dippin-lang`), so its
  rename cost is decoupled from Tracker's and its versioning already ships
  independently (pinned via `go get ...dippin-lang@vX.Y.Z`).

**Options for the DSL name:**
- **Keep + qualify (recommended).** Always introduce it as "Dippin, our pipeline
  definition language" and let the *product* name carry gravitas. Cost: ~0.
- **Keep the extension, soften the prose name.** Refer to it primarily as "the
  `.dip` pipeline language" in enterprise-facing material; "Dippin" stays as the
  friendly project name. Cost: ~0, purely editorial.
- **Rename the DSL.** Highest cost (separate repo, its own module/CLI `dippin`
  binary on users' PATH per CLAUDE.md, `.dip` extension churn) for the *smallest*
  discoverability payoff, because a coined name is already ownable. **Not
  recommended** unless it's bundled into a broader Tracker rebrand.

**Verdict:** "Dippin" is not the problem #460 should spend on. Qualify it; don't
rename it standalone.

---

## 5. Strategy options

### Option A — Qualify + acquire domain, keep the name
- **What:** Standardize on **"Tracker by 2389"** (or "2389 Tracker") in all copy,
  titles, JSON-LD `name`, README H1, site `<title>`. Buy a distinctive domain and
  redirect it to the GH Pages site.
- **Switching cost:** *Very low.* Copy/metadata edits only — no module path, no
  binary, no brew, no user-disk state touched. Fully reversible.
- **Timing:** Now. No gate needed.
- **SEO/brand upside:** Creates a unique, attributable two-token string that can
  rank and be cited; a real domain gives redirect control and a memorable spoken
  URL. Does **not** fix "tracker what?" in speech.
- **Risk:** Low. Worst case it's a stepping stone to B.

### Option B — Full rename to a coined/distinctive name
- **What:** New product name; migrate module path, binaries, brew, site, docs.
- **Switching cost:** *High but bounded and pre-scoped* — the
  `mammoth-lite→tracker` plan is the template (§7). Adds a dual-name transition,
  stranded backlinks, `go install`/brew aliasing, and env-var/state-dir
  back-compat shims.
- **Timing:** At/just-before a growth inflection (public launch, funding, first
  big adopters) — the Vercel pattern. Every month of delay past that inflection
  raises cost.
- **SEO/brand upside:** *Highest.* Ownable, rankable, word-of-mouth-able,
  trademark-able, unambiguous. Matches the dominant modern pattern (87% brandable).
- **Risk:** Transition confusion; must be executed decisively with redirects and a
  clear "X is now Y" narrative. Half-done renames are worse than none.

**Candidate name *directions* (shape, not final picks — validate availability):**
1. **Coined portmanteau of orchestration + budget/ledger** (evokes the "won't
   surprise you on the bill" positioning) — e.g. a fusion of *conduct/orchestra*
   + *ledger/tally*.
2. **Coined from "pipeline/relay/route"** — evokes multi-agent flow.
3. **Short invented CVC/CVCV word** (Trino/Vercel shape: 2 syllables,
   front-loaded consonant, ends in a vowel or hard stop) — maximally typable,
   handle-available.
4. **Latin/Greek root for "guide/steer/watch"** (gubernare→govern, kybernan→
   steer) filed to a coined form — evokes trustworthy control.
5. **"Worktree/worktrees" family** — leans into the signature git-worktree
   parallelism, but risks being descriptive/collidable.
6. **Nautical/conductor metaphor** (helm, maestro, tempo) — many are taken; check
   hard.
Criteria for all: 2 syllables, unambiguous spelling from sound, `.dev`/`.ai`
domain likely free, npm/GitHub org handle free, no live USPTO/EUIPO mark in
class 9/42, no meaning collision in the LLM-tooling space.

### Option C — Hybrid: keep `tracker` binary, brand the product distinctly
- **What:** Product/marketing name is a distinct coined brand; the *CLI binary*
  and module stay `tracker` (like "Vercel" the brand vs. `vercel`/`now` CLI, or
  "Trino" brand vs. package internals).
- **Switching cost:** *Medium.* Web/brand surface (1c) changes; code/distribution
  (1a/1b) mostly stays, so `go install`/brew/user-state don't break.
- **Timing:** Now-ish; less gated than B because it's non-breaking for users.
- **SEO/brand upside:** Most of B's brand upside (a rankable, ownable product
  name) at a fraction of the breakage — the classic decoupling of *brand* from
  *command name*.
- **Risk:** Two names to teach ("it's Foo — the `tracker` CLI"); mild ongoing
  cognitive tax. But this is *extremely common* and well-tolerated in dev tools.

> **C is the underrated option.** It captures the discoverability win (a unique,
> ownable product+domain brand) while sidestepping the expensive half of a rename
> (module path, brew, binaries, on-disk state). If A proves insufficient, prefer
> **C over B** unless there's a compelling reason to also change the command.

### Option D — Status quo
- **Switching cost:** 0. **Upside:** 0. **Risk:** the #460 problem compounds
  monthly (unsearchable, un-word-of-mouthable, no owned domain). Only defensible
  if the project is deliberately staying small/internal. **Not recommended.**

---

## 6. Domain strategy

A domain purchase is the **cheapest high-leverage move** and it should **gate**
the rename decision — *availability of a good domain is itself a naming
constraint.*

- **TLD priority for a dev tool:** **`.dev`** (cheap ~$12–20/yr, HTTPS/HSTS
  enforced by default, signals "developer/OSS", community-facing) is the best fit
  for Tracker's audience; **`.ai`** is on-brand for an LLM-orchestration tool and
  strong for word-of-mouth but pricey; **`.io`** is the startup default but
  costly (~$50–100) and increasingly generic; **`.sh`** is a cute nod to the CLI
  but niche; **`.com`** remains the universal-trust default and is worth securing
  *if* affordable/available for a coined name. Buy the coined-name `.com` **and**
  the on-brand `.dev`/`.ai` together if going the rename route.
  ([DomainDetails .io vs .dev](https://domaindetails.com/tlds/compare/io-vs-dev),
  [Namesilo on .dev](https://www.namesilo.com/blog/en/-developer-reputation-dev-domain-extension),
  [DarazHost on .sh](https://www.darazhost.com/sh-domain-why-developers-love-the-shell-script-tld-and-when-to-use-it/))
- **For Option A (keep "Tracker"):** the bare `tracker.*` in good TLDs is almost
  certainly taken/expensive. Target a **qualified** domain you *can* own —
  something in the shape of `tracker.2389.ai` (subdomain — free, immediate,
  on-brand) or a `2389tracker.dev` / `trackerby2389.dev`. A subdomain of the
  already-owned `2389.ai` is the zero-cost immediate win and gives redirect
  control today.
- **Availability check before committing to any name:** run one pass across
  domain (.com/.dev/.ai), **GitHub org/repo**, **npm/Homebrew**, and **USPTO/EUIPO
  trademark** (classes 9 & 42). Tools: NameScout, BrandNameCheckr, Nombrio
  (real USPTO/EUIPO), or the Brandomica MCP server.
  ([NameScout](https://namescout.dev/), [Nombrio](https://nombrio.com/))

**Gate rule:** *do not finalize a rename name until its domain + GitHub + package
+ trademark are all clear.* A coined name with no available `.com`/`.dev` is not a
better name than the qualified status quo.

---

## 7. Recommendation + phased plan

### Recommended path
**Option A immediately; hold Option C in reserve behind a decision gate; treat B
as the extreme case.** Rationale: A is free, reversible, and monotonically
helpful. C, if needed, gets ~all the brand upside without breaking users. B's
full breakage is only justified if the *command name itself* must change (rare)
or a trademark forces it.

### Phase 1 — Immediate, low-regret (do now, no gate)
1. **Qualify the name in copy.** Standardize on "Tracker by 2389" for first
   mention / titles / metadata: README H1, `site/hugo.toml` `title`,
   `_index.html` `og_title`/JSON-LD `name`, repo description. Keep `tracker` as
   the binary/command everywhere (don't touch code). → *verify:* grep shows
   qualified form in all `<title>`/`og_title`/JSON-LD `name` and README H1.
2. **Buy the domain(s).** At minimum stand up **`tracker.2389.ai`** (subdomain,
   free, today) redirecting to the GH Pages site; put a **`CNAME`** in `site/` so
   the Pages site serves from it. Optionally register a qualified `.dev`. → *verify:*
   domain resolves and 301s to the docs; `baseURL` reachable via the owned host.
3. **Qualify "Dippin" once.** Edit first-mention prose to "Dippin, our pipeline
   DSL" / "the `.dip` pipeline language." No repo/extension change. → *verify:*
   README + site intro read professionally on first mention.
4. **Add trademark/handle reconnaissance** for 3–5 candidate coined directions
   (§5B) so a future rename isn't starting cold. Output: a short shortlist with
   availability status. (Research only — no commitment.)

### Phase 2 — Decision gate (rename vs. stay)
Set an explicit review. **Tip toward rename (C, or B if the command must change)
when ≥2 of these fire:**
- A **public launch / funding / first marquee adopters** are imminent (the
  Vercel-timing inflection — rename *before* the backlink base grows).
- **Support/word-of-mouth friction** is observed (users can't find it, "tracker
  what?" recurs, docs traffic doesn't attribute).
- A **trademark/collision threat** emerges (someone else ships a dev tool named
  "tracker" and asserts a mark — the Presto/Trino scenario).
- A **clean coined name clears all availability checks** (domain+GitHub+package+
  trademark) — opportunity, not just pressure.

**Stay/qualify-only (A) when:** the project stays internal/niche, no inflection is
near, and no collision/trademark pressure exists. Cost of waiting is only the
monthly compounding backlink drift — acceptable while small.

**If gate fires, prefer C over B** unless the command/module rename is
specifically required. C keeps `go install`, brew, and on-disk state intact.

### Phase 3 — Rename migration checklist (only if gate fires; template proven)
Derived from §1 and the executed `docs/plans/2026-03-06-tracker-rename-design.md`
(`mammoth-lite → tracker`), which is the working precedent:
- **Code (1a):** module path + all imports; binary names (`tracker*`); help/error
  strings, `commandMode` names, `__jail-exec` self-exec; golden/conformance
  fixtures. Add **dual-read back-compat** for env vars + state/checkpoint dirs
  (`TRACKER_*`, `$XDG_STATE_HOME/tracker`, `.tracker/`) so existing runs resume.
- **Distribution (1b):** new Homebrew formula **aliasing** the old; keep old
  `go install` path serving old tags, announce new path; `.goreleaser.yml` asset
  names; GitHub repo rename (relies on GH redirect) — **but** publish the new
  `go get` path explicitly since module redirects are unreliable.
- **Web/brand (1c):** site `baseURL`/`CNAME`, all page URLs (mind `uglyURLs`),
  sitemap/RSS, JSON-LD `name`/`url`, ~195 doc references; publish an **"X is now
  Y"** page and redirect the old URL; update `site/content/changelog.html`.
- **Transition hygiene:** keep both names discoverable for ≥2 minor releases;
  one canonical announcement; update CHANGELOG/README/ROADMAP in the same change
  (per CLAUDE.md release discipline); run `make docs-check` so CLI docs stay in
  sync. **Do it in one decisive pass** — a lingering mixed state is the failure
  mode the Presto→Trino team warned about.

---

## Sources
- [Trino: We're rebranding PrestoSQL as Trino](https://trino.io/blog/2020/12/27/announcing-trino.html) · [Starburst: PrestoSQL becomes Trino](https://www.starburst.io/blog/prestosql-becomes-trino/) · [HN discussion](https://news.ycombinator.com/item?id=25566055)
- [Vercel: ZEIT is now Vercel](https://vercel.com/blog/zeit-is-now-vercel) · [Lexicon Branding: ZEIT→Vercel case study](https://www.lexiconbranding.com/case-studies/startup-branding-zeit-vercel/)
- [TermsFeed: Trademarks in Open Source](https://www.termsfeed.com/blog/open-source-trademark/) · [Google Open Source Casebook: Trademarks](https://google.github.io/opencasebook/trademarks/)
- [Bizonym: coined vs keyword vs founder names](https://bizonym.com/how-to-choose-between-a-coined-name-keyword-name-and-founder-name/) · [SmallBizTrends: descriptive vs coined](https://smallbiztrends.com/2009/04/brand-name-descriptive-unique.html) · [Namemesh: brandable vs keyword domains](https://www.namemesh.com/guides/domain-naming/brandable-vs-keyword-domains)
- [FasterCapital: naming best practices](https://fastercapital.com/term/naming-best-practices.html) · [Nakora: GitHub SEO](https://nakora.ai/blog/github-seo) · [Namestall: niche/utility naming](https://www.namestall.com/blog/naming-a-single-purpose-utility-site-what-a-niche-domain-strategy-actually-looks-like/)
- [DomainDetails: .io vs .dev](https://domaindetails.com/tlds/compare/io-vs-dev) · [Namesilo: why .dev](https://www.namesilo.com/blog/en/-developer-reputation-dev-domain-extension) · [DarazHost: .sh domain](https://www.darazhost.com/sh-domain-why-developers-love-the-shell-script-tld-and-when-to-use-it/)
- [NameScout](https://namescout.dev/) · [Nombrio (USPTO/EUIPO checks)](https://nombrio.com/) · [BrandNameCheckr](https://brandnamecheckr.com/blog)
- Internal: `docs/plans/2026-03-06-tracker-rename-design.md` (prior `mammoth-lite→tracker` rename — proven checklist); `README.md`, `site/hugo.toml`, `site/content/_index.html`, `go.mod`, `.goreleaser.yml`, `CLAUDE.md`.
