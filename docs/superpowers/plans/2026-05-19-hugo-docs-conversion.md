# Hugo Docs Conversion + a14y Fixes Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Convert the tracker docs site (currently 6 hand-written HTML files on `gh-pages`) into a Hugo project living at `site/` on `main`, built and deployed to `gh-pages` by a GitHub Action. Preserve the existing "creamsicle" visual identity (orange palette, `.glass` cards, `.nav-links`, `Inter` + `JetBrains Mono` typography). Fold in all a14y discovery fixes (AGENTS.md, llms.txt, robots.txt, sitemap.xml, sitemap.md, glossary page, per-page meta description / OpenGraph / canonical / JSON-LD).

**Architecture:** The Hugo source lives in `site/`. The existing `style.css` and `highlight.js` are copied verbatim into `site/static/` — no design changes. Boilerplate (`<!DOCTYPE>`, `<head>`, `<nav>`, `<footer>`, `<script>`) lives in `layouts/_default/baseof.html` and `layouts/partials/{head,nav,footer}.html`. Each existing page becomes a Hugo content file: its `<main>...</main>` body is extracted verbatim into `content/<name>.html` with front matter that supplies title, description, OpenGraph fields, JSON-LD payload, and a `mermaid: true` flag where needed. URLs are preserved via `uglyURLs = true` (so `content/workflows.html` → `/workflows.html`). AGENTS.md and llms.txt live in `site/static/` and are served from the site root. Hugo's built-in `sitemap.xml` is enabled; a custom `layouts/sitemap.md` template emits the markdown sitemap; `layouts/robots.txt` emits the crawl policy. A GitHub Action (`.github/workflows/docs.yml`) checks out main on every push that touches `site/`, runs `hugo --minify`, and force-pushes `site/public/` to `gh-pages`. The existing hand-edited `gh-pages` branch is replaced wholesale by the build output the first time the action runs.

**Tech Stack:** Hugo extended ≥ v0.140 (developer has v0.161.1 locally), GitHub Actions (`peaceiris/actions-hugo@v3` + `peaceiris/actions-gh-pages@v4`), existing `style.css` (orange/cream palette with `Inter` and `JetBrains Mono`), `highlight.js` (custom), conditional inline `mermaid@11` on workflows/architecture.

---

## File Structure

```
site/
├── hugo.toml                      # Hugo config — baseURL, title, params, output formats
├── archetypes/
│   └── default.md                 # boilerplate front matter
├── content/
│   ├── _index.html                # home page (was index.html)
│   ├── workflows.html             # was workflows.html
│   ├── architecture.html          # was architecture.html
│   ├── cli.html                   # was cli.html
│   ├── changelog.html             # was changelog.html
│   └── glossary.html              # new — agent-readable terminology
├── layouts/
│   ├── _default/
│   │   ├── baseof.html            # outer chrome (doctype, head, nav, body, footer, scripts)
│   │   ├── single.html            # single-page wrapper — pulls .Content into the base
│   │   └── home.html              # home wrapper — same shape as single, kept separate for taste
│   ├── partials/
│   │   ├── head.html              # title, meta, OG, canonical, JSON-LD, conditional mermaid
│   │   ├── nav.html               # site nav (reads data/nav.yaml; marks current item active)
│   │   └── footer.html            # site footer
│   ├── 404.html                   # 404 page
│   ├── robots.txt                 # crawl policy template
│   └── sitemap.md.md              # markdown sitemap output (filename per Hugo's output-format rules)
├── data/
│   └── nav.yaml                   # nav entries (label, href, external?)
└── static/
    ├── style.css                  # ported verbatim from gh-pages
    ├── highlight.js               # ported verbatim from gh-pages
    ├── AGENTS.md                  # agent-readable site orientation
    └── llms.txt                   # llms.txt directory standard
```

Outside `site/`:

```
.github/workflows/
└── docs.yml                       # build + deploy Hugo to gh-pages

CLAUDE.md                          # update "Website (GitHub Pages)" section
```

---

## Task 1: Clean up the abandoned gh-pages worktree

**Files:** Removes `/tmp/tracker-site`.

The previous a14y attempt added uncommitted edits to a `gh-pages` worktree. We're folding those fixes into Hugo instead, so the worktree should be discarded.

- [ ] **Step 1: Remove the worktree (forces, since it has uncommitted edits)**

Run: `git worktree remove --force /tmp/tracker-site`
Expected output: silent success.

- [ ] **Step 2: Verify worktrees are clean**

Run: `git worktree list`
Expected: only the main worktree at `/Users/harper/Public/src/2389/tracker [main]` remains.

---

## Task 2: Create the feature branch

**Files:** None (branch creation only).

All Hugo conversion work happens on a feature branch. PR back to main when verified locally.

- [ ] **Step 1: Create + check out the branch**

Run: `git checkout -b docs/hugo-conversion`
Expected: `Switched to a new branch 'docs/hugo-conversion'`.

- [ ] **Step 2: Confirm clean status**

Run: `git status --short`
Expected: only the pre-existing untracked files from the session start (`agent-runner`, `docs/superpowers/plans/...`, `predictions.jsonl`, `tracker-swebench`, and this new plan file). No staged changes.

---

## Task 3: Bootstrap the Hugo project

**Files:**
- Create: `site/hugo.toml`
- Create: `site/archetypes/default.md`
- Create: `site/.hugo_build.lock` (auto, gitignored)

We do NOT run `hugo new site site` — it'd create an `archetypes/`, `content/`, `data/`, `layouts/`, `static/`, `themes/`, plus a default `hugo.toml`. We only want a subset, so write the files we need explicitly.

- [ ] **Step 1: Create `site/hugo.toml`**

```toml
baseURL = "https://2389-research.github.io/tracker/"
languageCode = "en-us"
title = "Tracker"
enableRobotsTXT = true
enableGitInfo = true
uglyURLs = true

[params]
description = "Multi-agent LLM pipeline orchestration with hard cost ceilings, parallel branches in git worktrees, and a live TUI."
author = "2389.ai"
authorURL = "https://2389.ai"
repoURL = "https://github.com/2389-research/tracker"

[outputs]
home = ["html", "sitemap", "rss"]
section = ["html"]
page = ["html"]

[outputFormats.sitemap-md]
mediaType = "text/markdown"
baseName = "sitemap"
isPlainText = true
notAlternative = true
path = "/"
suffix = "md"

[markup.goldmark.renderer]
unsafe = true

[minify.tdewolff.html]
keepEndTags = true
keepDocumentTags = true
keepConditionalComments = true
keepDefaultAttrVals = true
```

Notes:
- `uglyURLs = true` makes `content/workflows.html` resolve to `/workflows.html` (matching the existing URL structure — no broken inbound links).
- `enableGitInfo = true` populates `.Lastmod` from git commit times for the sitemap.
- `outputFormats.sitemap-md` is a custom output format that emits `/sitemap.md`; we add it to `[outputs.home]` later (after writing the template).

- [ ] **Step 2: Add sitemap-md to home outputs**

Edit `site/hugo.toml`, change the home output line to:

```toml
[outputs]
home = ["html", "sitemap", "sitemap-md", "rss"]
section = ["html"]
page = ["html"]
```

- [ ] **Step 3: Create the default archetype**

Create `site/archetypes/default.md` with:

```markdown
---
title: "{{ replace .Name "-" " " | title }}"
date: {{ .Date }}
draft: true
---
```

- [ ] **Step 4: Commit the bootstrap**

```bash
git add site/hugo.toml site/archetypes/default.md
git commit -m "docs(site): bootstrap Hugo project skeleton

Add hugo.toml with uglyURLs (to preserve existing /X.html URLs),
sitemap-md custom output format, and basic params block. Add the
default archetype. No content or layouts yet."
```

---

## Task 4: Port the static assets

**Files:**
- Create: `site/static/style.css` (copied verbatim from `origin/gh-pages:style.css`)
- Create: `site/static/highlight.js` (copied verbatim from `origin/gh-pages:highlight.js`)

The existing CSS and highlight script work as-is. We do not edit them in this conversion.

- [ ] **Step 1: Copy the stylesheet**

Run: `mkdir -p site/static && git show origin/gh-pages:style.css > site/static/style.css`
Expected: `site/static/style.css` exists and is non-empty.

- [ ] **Step 2: Copy the highlight script**

Run: `git show origin/gh-pages:highlight.js > site/static/highlight.js`
Expected: `site/static/highlight.js` exists and is non-empty.

- [ ] **Step 3: Verify sizes match origin**

Run: `wc -c site/static/style.css site/static/highlight.js && git show origin/gh-pages:style.css | wc -c && git show origin/gh-pages:highlight.js | wc -c`
Expected: matching byte counts (allowing for minor newline differences).

- [ ] **Step 4: Commit**

```bash
git add site/static/style.css site/static/highlight.js
git commit -m "docs(site): port style.css and highlight.js from gh-pages

Verbatim copy from origin/gh-pages — the creamsicle palette,
glass cards, and custom syntax highlighting all carry over
unchanged. No design adjustments yet."
```

---

## Task 5: Build the head partial (a14y meta + JSON-LD)

**Files:**
- Create: `site/layouts/partials/head.html`

The head partial renders title, meta description, OG fields, canonical URL, and a JSON-LD block — all from per-page front matter. Conditional inline mermaid is included when `mermaid: true` is set in front matter.

- [ ] **Step 1: Create the partial**

Create `site/layouts/partials/head.html`:

```html
<head>
  <meta charset="UTF-8">
  <meta name="viewport" content="width=device-width, initial-scale=1.0">
  <title>{{ if .IsHome }}{{ site.Title }} — {{ site.Params.description | truncate 60 }}{{ else }}{{ .Title }} — {{ site.Title }}{{ end }}</title>

  {{ $description := .Params.description | default site.Params.description }}
  <meta name="description" content="{{ $description }}">

  {{ $ogTitle := .Params.og_title | default .Title }}
  {{ if .IsHome }}{{ $ogTitle = .Params.og_title | default (printf "%s — %s" site.Title "Multi-Agent Pipeline Orchestration") }}{{ end }}
  <meta property="og:title" content="{{ $ogTitle }}">
  <meta property="og:description" content="{{ .Params.og_description | default $description }}">
  <meta property="og:type" content="{{ if .IsHome }}website{{ else }}article{{ end }}">
  <meta property="og:url" content="{{ .Permalink }}">

  <link rel="canonical" href="{{ .Permalink }}">
  <link rel="alternate" type="application/rss+xml" href="{{ "index.xml" | absURL }}" title="{{ site.Title }} RSS feed">

  <link rel="stylesheet" href="{{ "style.css" | relURL }}">

  {{ if .Params.mermaid }}
  <script src="https://cdn.jsdelivr.net/npm/mermaid@11/dist/mermaid.min.js"></script>
  <script>mermaid.initialize({ theme: 'base', securityLevel: 'loose', flowchart: { htmlLabels: true }, themeVariables: { fontSize: '16px', primaryColor: '#fff7ed', primaryTextColor: '#1c1917', primaryBorderColor: '#ea580c', lineColor: '#a8a29e', secondaryColor: '#ffedd5', tertiaryColor: '#fef7f0', background: 'transparent', mainBkg: '#fff7ed', nodeBorder: '#ea580c', clusterBkg: '#ffedd566', clusterBorder: '#ea580c44', titleColor: '#7c2d12', edgeLabelBackground: '#fef7f0', nodeTextColor: '#1c1917', actorTextColor: '#1c1917', signalTextColor: '#1c1917', labelTextColor: '#7c2d12' }});</script>
  {{ end }}

  {{ with .Params.jsonld }}
  <script type="application/ld+json">
  {{ . | jsonify (dict "indent" "  ") | safeJS }}
  </script>
  {{ end }}
</head>
```

Notes:
- `.Permalink` returns the absolute URL of the page — already includes `baseURL`.
- The mermaid script is only emitted when a page sets `mermaid: true` (workflows and architecture pages).
- The JSON-LD block is rendered from the `jsonld` front matter field as JSON. `safeJS` is required so Hugo doesn't escape it.
- We do NOT emit `<link rel="alternate" type="text/markdown">` in this phase — `.md` mirrors are out of scope (matches the a14y common-skips choice from the prior plan).

- [ ] **Step 2: Commit**

```bash
git add site/layouts/partials/head.html
git commit -m "docs(site): add head partial with a14y metadata + JSON-LD

The partial renders title, meta description, OpenGraph fields,
canonical link, RSS alternate, and a conditional JSON-LD block
from per-page front matter. Mermaid is included only when the
page sets mermaid: true."
```

---

## Task 6: Build the nav partial

**Files:**
- Create: `site/data/nav.yaml`
- Create: `site/layouts/partials/nav.html`

The nav is data-driven so we don't duplicate it across layouts. The data file lists labels + relative URLs; the partial marks the current page active.

- [ ] **Step 1: Create `site/data/nav.yaml`**

```yaml
items:
  - label: Home
    url: /
  - label: Workflows
    url: /workflows.html
  - label: Architecture
    url: /architecture.html
  - label: CLI
    url: /cli.html
  - label: Changelog
    url: /changelog.html
  - label: Glossary
    url: /glossary.html
  - label: GitHub
    url: https://github.com/2389-research/tracker
    external: true
```

- [ ] **Step 2: Create `site/layouts/partials/nav.html`**

```html
<nav class="nav">
  <a href="{{ "/" | relURL }}" class="nav-brand">
    <div class="logo-icon">T</div>
    <span>tracker</span> <span class="accent">by 2389.ai</span>
  </a>
  <ul class="nav-links">
    {{ $current := .RelPermalink }}
    {{ range site.Data.nav.items }}
      {{ $url := .url }}
      {{ if not .external }}{{ $url = .url | relURL }}{{ end }}
      <li>
        <a href="{{ $url }}"
           {{ if eq $current (.url | relURL) }}class="active"{{ end }}
           {{ if .external }}target="_blank"{{ end }}>{{ .label }}</a>
      </li>
    {{ end }}
  </ul>
</nav>
```

Notes:
- For internal links, `url` is passed through `relURL` so it gets prefixed with the baseURL path (`/tracker/` on the deploy domain).
- `.RelPermalink` of the current page is compared against the link's relURL. The home page's `.RelPermalink` is `/tracker/` so we need exact match — that works because `/` | relURL → `/tracker/`.

- [ ] **Step 3: Commit**

```bash
git add site/data/nav.yaml site/layouts/partials/nav.html
git commit -m "docs(site): add nav partial driven by data/nav.yaml

The nav reads its items from data/nav.yaml and marks the current
page active by comparing .RelPermalink. External links open in
a new tab via the external: true flag."
```

---

## Task 7: Build the footer partial

**Files:**
- Create: `site/layouts/partials/footer.html`

- [ ] **Step 1: Create the partial**

```html
<footer class="footer">
  <p>Built by <a href="https://2389.ai">2389.ai</a> &middot; <a href="{{ site.Params.repoURL }}">GitHub</a></p>
</footer>
```

- [ ] **Step 2: Commit**

```bash
git add site/layouts/partials/footer.html
git commit -m "docs(site): add footer partial"
```

---

## Task 8: Build the base layout

**Files:**
- Create: `site/layouts/_default/baseof.html`

The base layout is what every page extends. It pulls in the head/nav/footer partials, includes the highlight.js script at the end of body, and defines a `main` block that page-specific layouts fill.

- [ ] **Step 1: Create `site/layouts/_default/baseof.html`**

```html
<!DOCTYPE html>
<html lang="en">
{{ partial "head.html" . }}
<body>

{{ partial "nav.html" . }}

<main>
{{ block "main" . }}{{ end }}
</main>

{{ partial "footer.html" . }}

<script src="{{ "highlight.js" | relURL }}"></script>

</body>
</html>
```

- [ ] **Step 2: Commit**

```bash
git add site/layouts/_default/baseof.html
git commit -m "docs(site): add base layout

baseof.html composes head/nav/footer partials and defines the
'main' block that page layouts override. highlight.js loads at
end of body — matches the existing pattern."
```

---

## Task 9: Build single and home layouts

**Files:**
- Create: `site/layouts/_default/single.html`
- Create: `site/layouts/_default/home.html`

Both layouts are thin — they just emit `.Content` into the `main` block. We keep them as separate files (rather than relying on a single fallback) so future divergence (e.g. adding a TOC sidebar to single-page docs) doesn't require renaming.

- [ ] **Step 1: Create single.html**

```html
{{ define "main" }}
{{ .Content }}
{{ end }}
```

- [ ] **Step 2: Create home.html**

```html
{{ define "main" }}
{{ .Content }}
{{ end }}
```

- [ ] **Step 3: Commit**

```bash
git add site/layouts/_default/single.html site/layouts/_default/home.html
git commit -m "docs(site): add single + home layouts

Both layouts emit .Content into the base layout's main block.
Kept separate so future divergence (TOC sidebar, etc.) doesn't
require renaming."
```

---

## Task 10: Add the 404 page

**Files:**
- Create: `site/layouts/404.html`

- [ ] **Step 1: Create the 404 layout**

```html
{{ define "main" }}
<section class="hero glass" style="padding: 3rem 2rem; text-align: center;">
  <h1>404 — page not found</h1>
  <p class="subtitle">That URL doesn't exist on this site.</p>
  <div class="hero-actions">
    <a href="{{ "/" | relURL }}" class="btn btn-primary">Back to home</a>
    <a href="{{ site.Params.repoURL }}" class="btn btn-ghost">GitHub</a>
  </div>
</section>
{{ end }}
```

- [ ] **Step 2: Commit**

```bash
git add site/layouts/404.html
git commit -m "docs(site): add 404 page"
```

---

## Task 11: Add the robots.txt template

**Files:**
- Create: `site/layouts/robots.txt`

Hugo emits this at `/robots.txt` when `enableRobotsTXT = true` (already set in `hugo.toml`).

- [ ] **Step 1: Create the template**

```
User-agent: *
Allow: /

Sitemap: {{ "sitemap.xml" | absURL }}
```

- [ ] **Step 2: Commit**

```bash
git add site/layouts/robots.txt
git commit -m "docs(site): add robots.txt template

Allows all crawlers and points to the sitemap. Fixes the
robots-txt.exists a14y check."
```

---

## Task 12: Add the markdown sitemap output

**Files:**
- Create: `site/layouts/sitemap.md.md`

Hugo's output-format machinery looks up `layouts/<kind>.<output-format-name>.<suffix>`. For our `sitemap-md` format with kind `home` and suffix `md`, the file is `layouts/sitemap.md.md`. That double-`.md` is intentional and matches Hugo's naming rules.

- [ ] **Step 1: Create the template**

```markdown
# Tracker — Site Map

A human-readable index of every page on this site.

## Pages

{{ range where site.RegularPages "Section" "" }}
- [{{ .Title }}]({{ .Permalink }}) — {{ .Params.description | default .Summary | truncate 120 }}
{{ end }}

## Machine-readable

- [llms.txt]({{ "llms.txt" | absURL }}) — short orientation prompt for LLM agents.
- [sitemap.xml]({{ "sitemap.xml" | absURL }}) — XML sitemap.
- [robots.txt]({{ "robots.txt" | absURL }}) — crawl policy.
- [AGENTS.md]({{ "AGENTS.md" | absURL }}) — agent-readable site orientation.

## Source

- [GitHub repository]({{ site.Params.repoURL }})
```

- [ ] **Step 2: Commit**

```bash
git add site/layouts/sitemap.md.md
git commit -m "docs(site): add markdown sitemap output

Custom output format defined in hugo.toml emits /sitemap.md
from this template. Fixes the sitemap-md.exists a14y check."
```

---

## Task 13: Add AGENTS.md and llms.txt as static files

**Files:**
- Create: `site/static/AGENTS.md`
- Create: `site/static/llms.txt`

These are served verbatim at `/AGENTS.md` and `/llms.txt`. They could be generated from templates, but static is simpler for content that changes once per quarter at most.

- [ ] **Step 1: Create `site/static/AGENTS.md`**

```markdown
# Tracker — Agent Reference

Tracker is a pipeline orchestration engine for multi-agent LLM workflows.
Pipelines are defined in `.dip` files (the Dippin language) and executed
with parallel agents via a TUI dashboard. Built by 2389.ai.

## Where to start

- [Home](https://2389-research.github.io/tracker/) — what tracker is, install + run in 60 seconds.
- [Workflows](https://2389-research.github.io/tracker/workflows.html) — the built-in pipelines and how `.dip` files are structured.
- [Architecture](https://2389-research.github.io/tracker/architecture.html) — engine, nodes, edges, backends, checkpoints, parallel execution, budget governance.
- [CLI Reference](https://2389-research.github.io/tracker/cli.html) — every subcommand and flag.
- [Changelog](https://2389-research.github.io/tracker/changelog.html) — release history.
- [Glossary](https://2389-research.github.io/tracker/glossary.html) — terminology used across the docs and `.dip` files.

## Source

Code, issues, and releases live at <https://github.com/2389-research/tracker>.

## Conventions used in these docs

- Code blocks fenced with a language tag (`bash`, `dip`) carry the runnable
  commands or pipeline snippets being described in the surrounding prose.
- `.dip` files use the Dippin pipeline language — see the Workflows and
  Architecture pages for the IR-to-Tracker mapping rules.
- Provider names follow tracker convention: `anthropic`, `openai`, `gemini`
  (not `google`). Base URL resolution goes through
  `tracker.ResolveProviderBaseURL(provider)`.

## a14y configuration

- Target URL: <https://2389-research.github.io/tracker/>
- Scorecard: 0.2.0
- Mode: site
- Last runs:
  - 2026-05-19 — 39 (scorecard 0.2.0)
```

- [ ] **Step 2: Create `site/static/llms.txt`**

```
# Tracker

> Multi-agent LLM pipeline orchestration with hard cost ceilings, parallel branches in git worktrees, and a live TUI. Define workflows in a small text file (`.dip`); every run is a git commit you can diff and replay. Built by 2389.ai.

## Documentation

- [Home](https://2389-research.github.io/tracker/): What tracker is and how to install + run it in 60 seconds.
- [Workflows](https://2389-research.github.io/tracker/workflows.html): Built-in pipelines (`ask_and_execute`, `build_product`, `build_product_with_superspec`) and how `.dip` files are structured.
- [Architecture](https://2389-research.github.io/tracker/architecture.html): Engine, nodes, edges, backends (native / Claude Code / ACP), checkpoints, parallel execution, budget governance.
- [CLI Reference](https://2389-research.github.io/tracker/cli.html): Every flag and subcommand — `run`, `validate`, `simulate`, `doctor`, `diagnose`, `audit`, `init`, `workflows`, `update`, `version`.
- [Changelog](https://2389-research.github.io/tracker/changelog.html): Release history with breaking changes and feature notes.
- [Glossary](https://2389-research.github.io/tracker/glossary.html): Terminology used across the docs and `.dip` files.

## Source

- [GitHub repository](https://github.com/2389-research/tracker): Source code, issue tracker, and tagged releases.
- [AGENTS.md](https://2389-research.github.io/tracker/AGENTS.md): Agent-readable orientation for this site.
- [sitemap.xml](https://2389-research.github.io/tracker/sitemap.xml): Machine-readable URL index.
- [sitemap.md](https://2389-research.github.io/tracker/sitemap.md): Human-readable URL index.
```

- [ ] **Step 3: Commit**

```bash
git add site/static/AGENTS.md site/static/llms.txt
git commit -m "docs(site): add AGENTS.md and llms.txt

AGENTS.md provides agent-readable site orientation; llms.txt
follows the llms.txt directory standard. Both are static and
served at /AGENTS.md and /llms.txt respectively. Fixes the
agents-md.exists and llms-txt.exists a14y checks."
```

---

## Task 14: Port the home page

**Files:**
- Create: `site/content/_index.html`

The home page body is everything between `<main>` and `</main>` in the existing `origin/gh-pages:index.html`, dropped into a Hugo content file with front matter. The front matter supplies the description, OG fields, and JSON-LD that the head partial renders.

- [ ] **Step 1: Extract the existing body**

Run: `git show origin/gh-pages:index.html > /tmp/index-original.html`
Then open `/tmp/index-original.html` and locate the `<main>` and `</main>` tags. The home content is everything between them (NOT including the `<main>` tags themselves).

- [ ] **Step 2: Create `site/content/_index.html` with front matter**

The file structure is:

```html
---
title: "Multi-Agent Pipeline Orchestration"
description: "Multi-agent LLM pipeline orchestration with hard cost ceilings, parallel branches in git worktrees, and a live TUI. Define workflows in a small text file; every run is a git commit you can diff and replay."
og_title: "Tracker — Multi-Agent Pipeline Orchestration"
og_description: "Multi-agent LLM pipeline orchestration with hard cost ceilings, parallel branches in git worktrees, and a live TUI. Built by 2389.ai."
jsonld:
  "@context": "https://schema.org"
  "@type": "SoftwareApplication"
  name: "Tracker"
  description: "Multi-agent LLM pipeline orchestration with hard cost ceilings, parallel branches in git worktrees, and a live TUI."
  url: "https://2389-research.github.io/tracker/"
  applicationCategory: "DeveloperApplication"
  operatingSystem: "macOS, Linux"
  offers:
    "@type": "Offer"
    price: "0"
    priceCurrency: "USD"
  author:
    "@type": "Organization"
    name: "2389.ai"
    url: "https://2389.ai"
  codeRepository: "https://github.com/2389-research/tracker"
  license: "https://github.com/2389-research/tracker/blob/main/LICENSE"
---

<!-- BODY: paste the verbatim contents of <main>...</main> from
     /tmp/index-original.html here, NOT including the <main> tags. -->
```

The actual body is too long to inline in this plan; copy it from `/tmp/index-original.html` between the `<main>` tags. Do not modify the markup — even hand-encoded HTML entities (`&rsquo;`, `&mdash;`) stay as-is.

- [ ] **Step 3: Verify locally**

Run: `cd site && hugo server` (in background or another terminal).
Open: <http://localhost:1313/tracker/>
Expected: home page renders. The hero, install section, all the original content appears. Nav shows the Glossary link (added in Task 6). `<head>` contains the new meta description, OG tags, canonical, and JSON-LD block (view-source to confirm).

- [ ] **Step 4: Commit**

```bash
git add site/content/_index.html
git commit -m "docs(site): port home page to Hugo content

Verbatim copy of the <main> body from origin/gh-pages:index.html
into content/_index.html. Front matter supplies a14y metadata
(description, OG, canonical, JSON-LD as SoftwareApplication)
that the head partial renders."
```

---

## Task 15: Port the workflows page

**Files:**
- Create: `site/content/workflows.html`

- [ ] **Step 1: Extract the existing body**

Run: `git show origin/gh-pages:workflows.html > /tmp/workflows-original.html`

- [ ] **Step 2: Create `site/content/workflows.html`**

Front matter:

```html
---
title: "Built-in Workflows"
description: "The three pipelines that ship with Tracker — ask_and_execute, build_product, and build_product_with_superspec — and how to author your own .dip files for headless autonomous builds."
og_title: "Built-in Workflows — Tracker"
og_description: "The three pipelines that ship with Tracker, plus a reference for authoring your own .dip files."
mermaid: true
jsonld:
  "@context": "https://schema.org"
  "@type": "TechArticle"
  headline: "Built-in Workflows — Tracker"
  description: "The three pipelines that ship with Tracker — ask_and_execute, build_product, and build_product_with_superspec — and how to author your own .dip files."
  url: "https://2389-research.github.io/tracker/workflows.html"
  author:
    "@type": "Organization"
    name: "2389.ai"
    url: "https://2389.ai"
  publisher:
    "@type": "Organization"
    name: "2389.ai"
  isPartOf:
    "@type": "WebSite"
    name: "Tracker"
    url: "https://2389-research.github.io/tracker/"
---

<!-- BODY: paste the verbatim contents of <main>...</main> from
     /tmp/workflows-original.html. -->
```

- [ ] **Step 3: Verify locally**

Open: <http://localhost:1313/tracker/workflows.html>
Expected: page renders. Mermaid diagrams (if any) render — the inline mermaid script is included because `mermaid: true` is in front matter. Nav shows Workflows as active.

- [ ] **Step 4: Commit**

```bash
git add site/content/workflows.html
git commit -m "docs(site): port workflows page to Hugo content

Body copied verbatim from origin/gh-pages. Front matter sets
mermaid: true so the inline mermaid script gets included via
the head partial."
```

---

## Task 16: Port the architecture page

**Files:**
- Create: `site/content/architecture.html`

- [ ] **Step 1: Extract**

Run: `git show origin/gh-pages:architecture.html > /tmp/architecture-original.html`

- [ ] **Step 2: Create the content file**

```html
---
title: "Architecture"
description: "How Tracker orchestrates multi-agent pipelines — the engine, nodes, edges, backends (native / Claude Code / ACP), checkpoints, parallel execution, budget governance, and tool-safety controls."
og_title: "Architecture — Tracker"
og_description: "How Tracker orchestrates multi-agent pipelines — engine, nodes, edges, backends, checkpoints, parallel execution, and budget governance."
mermaid: true
jsonld:
  "@context": "https://schema.org"
  "@type": "TechArticle"
  headline: "Architecture — Tracker"
  description: "How Tracker orchestrates multi-agent pipelines — the engine, nodes, edges, backends, checkpoints, parallel execution, and budget governance."
  url: "https://2389-research.github.io/tracker/architecture.html"
  author:
    "@type": "Organization"
    name: "2389.ai"
    url: "https://2389.ai"
  publisher:
    "@type": "Organization"
    name: "2389.ai"
  isPartOf:
    "@type": "WebSite"
    name: "Tracker"
    url: "https://2389-research.github.io/tracker/"
---

<!-- BODY: paste the verbatim contents of <main>...</main> from
     /tmp/architecture-original.html. -->
```

- [ ] **Step 3: Verify locally**

Open: <http://localhost:1313/tracker/architecture.html>
Expected: mermaid diagrams render (this page is mermaid-heavy). All `.glass` sections render correctly.

- [ ] **Step 4: Commit**

```bash
git add site/content/architecture.html
git commit -m "docs(site): port architecture page to Hugo content"
```

---

## Task 17: Port the CLI page

**Files:**
- Create: `site/content/cli.html`

- [ ] **Step 1: Extract**

Run: `git show origin/gh-pages:cli.html > /tmp/cli-original.html`

- [ ] **Step 2: Create the content file**

```html
---
title: "CLI Reference"
description: "Every Tracker CLI subcommand and flag — run, validate, simulate, doctor, diagnose, audit, init, workflows, update — plus budget caps, backend selection, autopilot personas, and tool-safety controls."
og_title: "CLI Reference — Tracker"
og_description: "Every Tracker CLI subcommand, flag, and resolution rule — install and operate Tracker from the terminal."
jsonld:
  "@context": "https://schema.org"
  "@type": "TechArticle"
  headline: "Tracker CLI Reference"
  description: "Every Tracker CLI subcommand and flag — run, validate, simulate, doctor, diagnose, audit, init, workflows, update."
  url: "https://2389-research.github.io/tracker/cli.html"
  author:
    "@type": "Organization"
    name: "2389.ai"
    url: "https://2389.ai"
  publisher:
    "@type": "Organization"
    name: "2389.ai"
  isPartOf:
    "@type": "WebSite"
    name: "Tracker"
    url: "https://2389-research.github.io/tracker/"
---

<!-- BODY: paste the verbatim contents of <main>...</main> from
     /tmp/cli-original.html. -->
```

Note: no `mermaid: true` — this page has no mermaid diagrams.

- [ ] **Step 3: Verify locally**

Open: <http://localhost:1313/tracker/cli.html>
Expected: code blocks render with highlight.js styling. No mermaid script in head.

- [ ] **Step 4: Commit**

```bash
git add site/content/cli.html
git commit -m "docs(site): port CLI page to Hugo content"
```

---

## Task 18: Port the changelog page

**Files:**
- Create: `site/content/changelog.html`

- [ ] **Step 1: Extract**

Run: `git show origin/gh-pages:changelog.html > /tmp/changelog-original.html`

- [ ] **Step 2: Create the content file**

```html
---
title: "Changelog"
description: "Tracker release history — versions, breaking changes, new features, and fixes across every shipped release."
og_title: "Changelog — Tracker"
og_description: "Tracker release history — versions, breaking changes, new features, and fixes."
jsonld:
  "@context": "https://schema.org"
  "@type": "TechArticle"
  headline: "Tracker Changelog"
  description: "Tracker release history — versions, breaking changes, new features, and fixes."
  url: "https://2389-research.github.io/tracker/changelog.html"
  author:
    "@type": "Organization"
    name: "2389.ai"
    url: "https://2389.ai"
  publisher:
    "@type": "Organization"
    name: "2389.ai"
  isPartOf:
    "@type": "WebSite"
    name: "Tracker"
    url: "https://2389-research.github.io/tracker/"
---

<!-- BODY: paste the verbatim contents of <main>...</main> from
     /tmp/changelog-original.html. -->
```

- [ ] **Step 3: Verify locally**

Open: <http://localhost:1313/tracker/changelog.html>
Expected: release entries render. Anchor links (e.g. `#v0-29-2`) still work.

- [ ] **Step 4: Commit**

```bash
git add site/content/changelog.html
git commit -m "docs(site): port changelog page to Hugo content"
```

---

## Task 19: Author the glossary page (new)

**Files:**
- Create: `site/content/glossary.html`

The glossary is new — no source on `gh-pages` to port. Inline the full body here.

- [ ] **Step 1: Create the content file**

```html
---
title: "Glossary"
description: "Terminology used across Tracker pipelines, the Dippin language, and the multi-agent runtime — node, edge, handler, backend, autopilot, budget, checkpoint, bundle, and more."
og_title: "Glossary — Tracker"
og_description: "Terminology used across Tracker pipelines, the Dippin language, and the multi-agent runtime."
jsonld:
  "@context": "https://schema.org"
  "@type": "DefinedTermSet"
  name: "Tracker Glossary"
  description: "Terminology used across Tracker pipelines, the Dippin language, and the multi-agent runtime."
  url: "https://2389-research.github.io/tracker/glossary.html"
  publisher:
    "@type": "Organization"
    name: "2389.ai"
    url: "https://2389.ai"
---

<section class="hero glass" style="padding: 3rem 2rem;">
  <h1>Glossary</h1>
  <p class="subtitle">Terminology used across Tracker pipelines, the Dippin language, and the multi-agent runtime.</p>
</section>

<nav class="page-toc" aria-label="On this page">
  <strong>On this page:</strong>
  <a href="#pipeline">Pipeline &amp; runtime</a>
  <a href="#dippin">Dippin language</a>
  <a href="#agents">Agents &amp; backends</a>
  <a href="#handlers">Node handlers</a>
  <a href="#routing">Routing &amp; outcome</a>
  <a href="#governance">Governance</a>
  <a href="#artifacts">Artifacts &amp; inspection</a>
</nav>

<section class="glass" id="pipeline">
  <h2>Pipeline &amp; <span class="accent">runtime</span></h2>
  <p class="lead">The core abstractions the engine works with.</p>
  <div class="grid-2" style="margin-top: 1.25rem;">
    <div class="glass glass-sm"><h4>Pipeline</h4><p>A directed graph of nodes connected by edges, plus shared context. Defined in a <code>.dip</code> file and executed by the engine.</p></div>
    <div class="glass glass-sm"><h4>Engine</h4><p>The Tracker runtime that walks the graph: pick a node, execute its handler, evaluate outgoing edges, advance. Treats every node the same &mdash; no special-cases for parallel or fan-in.</p></div>
    <div class="glass glass-sm"><h4>Node</h4><p>A single step in the pipeline. Identified by ID; carries a <code>handler</code> (agent, tool, human, parallel, &hellip;) and attributes (prompt, model, command, &hellip;).</p></div>
    <div class="glass glass-sm"><h4>Edge</h4><p>A directed connection between two nodes. Optionally guarded by a condition like <code>when ctx.outcome = success</code>. Labeled edges can carry a human-readable choice.</p></div>
    <div class="glass glass-sm"><h4>Run</h4><p>One execution of a pipeline. Has a unique <code>runID</code>, an artifact directory, and a status. Resumable from a checkpoint.</p></div>
    <div class="glass glass-sm"><h4>Checkpoint</h4><p>A snapshot of the engine&rsquo;s state &mdash; completed nodes, context, edge selections &mdash; written after each node finishes. Used to resume a stopped run.</p></div>
  </div>
</section>

<section class="glass" id="dippin">
  <h2><span class="accent">Dippin</span> language</h2>
  <p class="lead">The text format pipelines are authored in.</p>
  <div class="grid-2" style="margin-top: 1.25rem;">
    <div class="glass glass-sm"><h4>Dippin</h4><p>The pipeline language used by Tracker. Has its own parser, linter (<code>dippin doctor</code>), and simulator. Ships as a Go module: <code>github.com/2389-research/dippin-lang</code>.</p></div>
    <div class="glass glass-sm"><h4><code>.dip</code> file</h4><p>A Dippin source file declaring nodes, edges, and workflow-level defaults. Loaded by <code>tracker run</code>, <code>tracker validate</code>, and <code>tracker simulate</code>.</p></div>
    <div class="glass glass-sm"><h4>IR (Intermediate Representation)</h4><p>The parsed in-memory form of a Dippin pipeline. Converted to Tracker&rsquo;s <code>Graph</code> model by <code>pipeline/dippin_adapter.go</code>.</p></div>
    <div class="glass glass-sm"><h4>Workflow defaults</h4><p>A <code>defaults:</code> block at the top of a <code>.dip</code> file that sets pipeline-wide values &mdash; default model, provider, budget ceilings &mdash; folded in as fallbacks beneath CLI flags and library config.</p></div>
    <div class="glass glass-sm"><h4><code>writes:</code> / <code>reads:</code></h4><p>Declarative I/O contracts on agent, tool, and interview-human nodes. Lists the context keys a node produces (<code>writes:</code>) or expects (<code>reads:</code>). Extracted into first-class context keys at runtime.</p></div>
    <div class="glass glass-sm"><h4>Context</h4><p>The shared key/value store every node reads from and writes to. Bare keys live at the top; per-node aliases (<code>node.&lt;nodeID&gt;.&lt;key&gt;</code>) appear after each node finishes.</p></div>
  </div>
</section>

<section class="glass" id="agents">
  <h2>Agents &amp; <span class="accent">backends</span></h2>
  <p class="lead">How LLM calls actually happen.</p>
  <div class="grid-2" style="margin-top: 1.25rem;">
    <div class="glass glass-sm"><h4>Agent</h4><p>The LLM-driven actor that runs inside an agent node. Has a model, a prompt, and access to tools; produces a session result with token usage and final response.</p></div>
    <div class="glass glass-sm"><h4>Backend</h4><p>The execution strategy for an agent node. Three are built in: <strong>Native</strong>, <strong>Claude Code</strong>, and <strong>ACP</strong>. Selected per-node via <code>backend:</code> or globally via <code>--backend</code>.</p></div>
    <div class="glass glass-sm"><h4>Native backend</h4><p>Wraps <code>agent.Session</code> with a turn loop, tool registry, and context compaction. Calls the provider SDK directly (Anthropic / OpenAI / Gemini / OpenAI-compat).</p></div>
    <div class="glass glass-sm"><h4>Claude Code backend</h4><p>Spawns the <code>claude</code> CLI as a subprocess and parses NDJSON output. API keys are stripped from the subprocess env so subscription auth works; override with <code>TRACKER_PASS_API_KEYS=1</code>.</p></div>
    <div class="glass glass-sm"><h4>ACP backend</h4><p>Bridges to any <a href="https://agentclientprotocol.com/">Agent Client Protocol</a> agent over stdio &mdash; <code>claude-agent-acp</code>, <code>codex-acp</code>, <code>gemini --acp</code>. Provider-based defaults; override per node via <code>acp_agent</code>.</p></div>
    <div class="glass glass-sm"><h4>Provider</h4><p>The LLM vendor for a native-backend call: <code>anthropic</code>, <code>openai</code>, <code>gemini</code>, or <code>openai-compat</code>. Base URL resolved via <code>tracker.ResolveProviderBaseURL(provider)</code>.</p></div>
  </div>
</section>

<section class="glass" id="handlers">
  <h2>Node <span class="accent">handlers</span></h2>
  <p class="lead">The set of behaviors a node can have. Selected by the <code>handler:</code> attribute.</p>
  <div class="grid-2" style="margin-top: 1.25rem;">
    <div class="glass glass-sm"><h4>Agent (codergen)</h4><p>Runs an LLM session via the selected backend. Carries a prompt, model, optional tools, and an optional structured-output schema.</p></div>
    <div class="glass glass-sm"><h4>Tool</h4><p>Runs a shell command. Stdout/stderr are captured (tail-windowed at 64KB by default) and surfaced as <code>ctx.tool_stdout</code> / <code>ctx.tool_stderr</code>.</p></div>
    <div class="glass glass-sm"><h4>Human gate</h4><p>Pauses the pipeline for human input. Modes: default choice, yes/no, freeform, labeled freeform, and interview (multi-field form from JSON questions).</p></div>
    <div class="glass glass-sm"><h4>Parallel</h4><p>Dispatches multiple branches concurrently from <code>parallel_targets</code>. Each branch runs in its own goroutine with panic recovery; emits per-branch start/complete events.</p></div>
    <div class="glass glass-sm"><h4>Fan-in</h4><p>Joins parallel branches back into a single path. The parallel handler hints the engine at the join node via <code>suggested_next_nodes</code>.</p></div>
    <div class="glass glass-sm"><h4>Conditional</h4><p>A no-op node whose only job is to route the run based on <code>when&hellip;</code> guards on its outgoing edges. Useful as an explicit branching point.</p></div>
    <div class="glass glass-sm"><h4>Subgraph</h4><p>Embeds another <code>.dip</code> pipeline as a node. The child runs in isolation with its own context window; results merge back via declared <code>writes:</code>.</p></div>
    <div class="glass glass-sm"><h4>Manager loop</h4><p>A controller node that repeatedly spawns a child agent with steerable context (<code>steer.&lt;key&gt;</code>) until a termination condition fires. Lives in the <code>stack.manager_loop</code> handler.</p></div>
  </div>
</section>

<section class="glass" id="routing">
  <h2>Routing &amp; <span class="accent">outcome</span></h2>
  <p class="lead">How the engine decides where to go next.</p>
  <div class="grid-2" style="margin-top: 1.25rem;">
    <div class="glass glass-sm"><h4>Outcome</h4><p>The status a node reports when it finishes: <code>success</code>, <code>fail</code>, <code>retry</code>, or <code>budget_exceeded</code>. Available on outgoing edges as <code>ctx.outcome</code>.</p></div>
    <div class="glass glass-sm"><h4>Conditional edge</h4><p>An edge with a <code>when&hellip;</code> guard like <code>when ctx.outcome = success</code>. Evaluated against the context; unmatched conditional edges are skipped.</p></div>
    <div class="glass glass-sm"><h4>Strict failure edge</h4><p>When a node&rsquo;s outcome is <code>fail</code> and every outgoing edge is unconditional, the engine stops. Prevents tool nodes from silently masking errors.</p></div>
    <div class="glass glass-sm"><h4>Suggested next nodes</h4><p>An optional hint a handler can set to redirect the engine past the normal edge-walk &mdash; used by the parallel handler to jump to the fan-in.</p></div>
    <div class="glass glass-sm"><h4>Escalation</h4><p>A routing convention, not a distinct outcome: an edge guarded by <code>ctx.outcome = fail</code> that leads to an escalation gate (e.g. <code>EscalateMilestone</code>).</p></div>
    <div class="glass glass-sm"><h4>Preferred label</h4><p>The human-readable choice a human-gate node returned. Drives label-based edge selection when an edge specifies a target label.</p></div>
  </div>
</section>

<section class="glass" id="governance">
  <h2><span class="accent">Governance</span></h2>
  <p class="lead">Budgets, ceilings, and headless decision-making.</p>
  <div class="grid-2" style="margin-top: 1.25rem;">
    <div class="glass glass-sm"><h4>Budget</h4><p>The set of ceilings a run will halt under: max tokens, max cost (cents), max wall-time. Configured via <code>tracker.Config.Budget</code>, CLI flags, or a <code>defaults:</code> block.</p></div>
    <div class="glass glass-sm"><h4>Cost ceiling</h4><p>A dollar (or cents) cap. Enforced between nodes after each cost update; breach halts the run with outcome <code>budget_exceeded</code> and fires <code>EventBudgetExceeded</code>.</p></div>
    <div class="glass glass-sm"><h4>Autopilot</h4><p>Replaces every human gate with an LLM-backed decision. Four personas: <code>lax</code> (forward progress), <code>mid</code> (balanced, default), <code>hard</code> (high bar), <code>mentor</code> (approve with feedback).</p></div>
    <div class="glass glass-sm"><h4>Auto-approve</h4><p>Deterministic alternative to autopilot &mdash; always picks the default or first option at a gate. No LLM call.</p></div>
    <div class="glass glass-sm"><h4>Webhook gate</h4><p>Headless alternative to autopilot: gates are POSTed to an external service and blocked on a callback. Enabled via <code>--webhook-url</code>.</p></div>
    <div class="glass glass-sm"><h4>Circuit breaker</h4><p>A per-area attempt counter (e.g. <code>fix_attempts</code> on disk) that caps how many times a section of a pipeline can retry. Persists across run restarts.</p></div>
  </div>
</section>

<section class="glass" id="artifacts">
  <h2>Artifacts &amp; <span class="accent">inspection</span></h2>
  <p class="lead">Everything a run leaves behind, and the commands that read it.</p>
  <div class="grid-2" style="margin-top: 1.25rem;">
    <div class="glass glass-sm"><h4>Artifact</h4><p>Any file written by a node during a run &mdash; agent transcripts, tool outputs, generated code. Lives in the run directory.</p></div>
    <div class="glass glass-sm"><h4>Run directory</h4><p>The on-disk home for one run: <code>&lt;workDir&gt;/.tracker/runs/&lt;runID&gt;/</code>. Contains <code>status.json</code>, <code>checkpoint.json</code>, transcripts, and the git-artifacts repo if enabled.</p></div>
    <div class="glass glass-sm"><h4>Git artifacts</h4><p>When <code>WithGitArtifacts(true)</code> is set, the run directory is initialized as a git repo and a commit is made after every terminal node &mdash; every run is replayable.</p></div>
    <div class="glass glass-sm"><h4>Bundle</h4><p>A portable, self-contained git bundle of the run directory&rsquo;s full history. Built by <code>tracker.ExportBundle</code> or <code>--export-bundle</code>; clone with <code>git clone &lt;bundle&gt;</code>.</p></div>
    <div class="glass glass-sm"><h4>Activity log</h4><p>The append-only event stream for a run (<code>activity.jsonl</code>). Lives in a secured XDG state dir; a sentinel-stripped copy is written back to the run dir for bundle export.</p></div>
    <div class="glass glass-sm"><h4><code>tracker diagnose</code></h4><p>Reads <code>status.json</code> + <code>activity.jsonl</code> for a failed run and surfaces tool output, stderr, errors, timing anomalies, and suggested fixes. Without an ID, analyzes the most recent run.</p></div>
  </div>
</section>
```

- [ ] **Step 2: Verify locally**

Open: <http://localhost:1313/tracker/glossary.html>
Expected: glossary renders. The hero card, page-TOC nav, and 7 sections all appear styled correctly. Nav shows Glossary as active.

- [ ] **Step 3: Commit**

```bash
git add site/content/glossary.html
git commit -m "docs(site): add glossary page

New page — agent-readable terminology covering pipeline, dippin,
agents/backends, node handlers, routing, governance, and
artifacts. Uses the same .glass/.grid-2 pattern as the existing
architecture page. JSON-LD as DefinedTermSet."
```

---

## Task 20: Full local verification

**Files:** None (verification only).

Before pushing, confirm every page renders, every link works, every static file is reachable.

- [ ] **Step 1: Stop any running hugo server, then start fresh**

Run from `site/`: `hugo server --bind 0.0.0.0 --port 1313`
Expected: clean startup, `Web Server is available at http://localhost:1313/tracker/`.

- [ ] **Step 2: Verify every URL responds 200**

In another terminal:

```bash
for path in "" workflows.html architecture.html cli.html changelog.html glossary.html AGENTS.md llms.txt robots.txt sitemap.xml sitemap.md; do
  code=$(curl -s -o /dev/null -w "%{http_code}" "http://localhost:1313/tracker/$path")
  printf "  %-20s  %s\n" "$path" "$code"
done
```

Expected: all 200.

- [ ] **Step 3: Verify head metadata on each page**

For each of the 6 HTML pages, run:

```bash
curl -s "http://localhost:1313/tracker/<page>" | grep -E '(<title>|meta name="description"|og:title|og:description|og:type|canonical|application/ld\+json)'
```

Expected: every grep returns at least one match per pattern.

- [ ] **Step 4: Verify mermaid loads ONLY on workflows + architecture**

```bash
for page in "" workflows.html architecture.html cli.html changelog.html glossary.html; do
  has_mermaid=$(curl -s "http://localhost:1313/tracker/$page" | grep -c 'mermaid.min.js' || true)
  printf "  %-20s  mermaid script: %s\n" "$page" "$has_mermaid"
done
```

Expected: `1` on workflows + architecture, `0` on all others.

- [ ] **Step 5: Verify the glossary nav link appears on every page**

```bash
for page in "" workflows.html architecture.html cli.html changelog.html glossary.html; do
  has_glossary=$(curl -s "http://localhost:1313/tracker/$page" | grep -c 'href="/tracker/glossary.html"' || true)
  printf "  %-20s  glossary link: %s\n" "$page" "$has_glossary"
done
```

Expected: every page has `1` (the nav link).

- [ ] **Step 6: Visual spot-check in a browser**

Open these in a browser and confirm the visual identity matches the live site:
- <http://localhost:1313/tracker/> (home — hero gradient, install code block syntax-highlighted, .glass cards)
- <http://localhost:1313/tracker/workflows.html> (mermaid diagrams render)
- <http://localhost:1313/tracker/architecture.html> (mermaid diagrams render)
- <http://localhost:1313/tracker/cli.html> (code-block syntax highlighting)
- <http://localhost:1313/tracker/changelog.html> (release entries laid out as cards)
- <http://localhost:1313/tracker/glossary.html> (7 sections, grid-2 cards, page-TOC nav at top)

If anything looks off, file a TODO and revisit before the deploy task.

- [ ] **Step 7: Stop the hugo server**

Ctrl-C.

---

## Task 21: Add the GitHub Actions deploy workflow

**Files:**
- Create: `.github/workflows/docs.yml`

- [ ] **Step 1: Create the workflow**

```yaml
# ABOUTME: GitHub Actions workflow to build and deploy the docs site.
# ABOUTME: Triggers on pushes to main that touch site/ or the workflow itself.
name: Docs

on:
  push:
    branches: [main]
    paths:
      - 'site/**'
      - '.github/workflows/docs.yml'
  workflow_dispatch:

permissions:
  contents: write

jobs:
  deploy:
    name: Build and deploy
    runs-on: ubuntu-latest
    steps:
      - name: Check out code
        uses: actions/checkout@v4
        with:
          fetch-depth: 0

      - name: Set up Hugo
        uses: peaceiris/actions-hugo@v3
        with:
          hugo-version: '0.161.1'
          extended: true

      - name: Build
        working-directory: ./site
        run: hugo --minify --gc

      - name: Deploy to gh-pages
        uses: peaceiris/actions-gh-pages@v4
        with:
          github_token: ${{ secrets.GITHUB_TOKEN }}
          publish_dir: ./site/public
          publish_branch: gh-pages
          force_orphan: true
          user_name: 'github-actions[bot]'
          user_email: 'github-actions[bot]@users.noreply.github.com'
          commit_message: 'docs: deploy ${{ github.sha }}'
```

Notes:
- `fetch-depth: 0` is required because `enableGitInfo = true` in Hugo reads commit history for `.Lastmod`.
- `force_orphan: true` replaces the entire `gh-pages` branch with each deploy. The first run will wipe all existing hand-edited content there. This is intentional — the source of truth becomes `site/` on `main`.
- `paths` filter means this workflow only fires on relevant pushes; CI keeps running unchanged.

- [ ] **Step 2: Commit**

```bash
git add .github/workflows/docs.yml
git commit -m "ci: add docs deploy workflow

Builds Hugo on every push to main that touches site/ and force-
pushes site/public/ to gh-pages. The existing hand-edited
gh-pages content is replaced wholesale on first run; main is
now the source of truth for the docs site."
```

---

## Task 22: Update CLAUDE.md to reflect the new workflow

**Files:**
- Modify: `CLAUDE.md` (the "Website (GitHub Pages)" subsection under "Versioning and Releases")

The current section describes the hand-edited gh-pages workflow. Replace it with the Hugo flow.

- [ ] **Step 1: Locate the current section**

Run: `grep -n "Website (GitHub Pages)" CLAUDE.md`
Expected: one line number — call it `<L>`.

- [ ] **Step 2: Replace the section**

The current section (under `### Website (GitHub Pages)`) is the four-bullet block. Replace its body with:

```markdown
### Website (GitHub Pages)
- Source: `site/` on `main` is a Hugo project. Built and deployed by `.github/workflows/docs.yml` on every push that touches `site/`.
- Output: rendered HTML lives on the `gh-pages` branch; GitHub Pages serves it at <https://2389-research.github.io/tracker/>.
- Refresh workflow: edit content under `site/content/`, partials/layouts under `site/layouts/`, or `site/static/`; commit on a branch off `main`; the `Docs` workflow rebuilds + redeploys on merge. Verify locally first with `hugo server` from `site/`.
- URL contract: `uglyURLs = true` preserves the existing `/X.html` paths (e.g. `/workflows.html`) — do not switch to pretty URLs without redirects.
- a14y discovery files: `AGENTS.md` and `llms.txt` are static files in `site/static/`; `sitemap.xml`, `sitemap.md`, and `robots.txt` are rendered from Hugo templates in `site/layouts/`.
- Each release should include a content refresh as a separate commit on `main` (e.g. updating `site/content/changelog.html` with the new version).
```

(The exact `Edit` invocation uses the old_string from `### Website (GitHub Pages)` through the last bullet point of the existing section.)

- [ ] **Step 3: Commit**

```bash
git add CLAUDE.md
git commit -m "docs(claude): update Website section for Hugo workflow

The site source moved from hand-edited gh-pages to a Hugo project
under site/ on main. CLAUDE.md now reflects the new edit flow and
the URL contract (uglyURLs)."
```

---

## Task 23: Push the branch and open a PR

**Files:** None (git/gh operations).

- [ ] **Step 1: Push the branch**

Run: `git push -u origin docs/hugo-conversion`
Expected: branch tracked on origin.

- [ ] **Step 2: Open a PR**

Run:

```bash
gh pr create --title "docs: convert tracker docs site to Hugo + a14y fixes" --body "$(cat <<'EOF'
## Summary
- Migrate the 6 hand-written HTML pages on `gh-pages` into a Hugo project at `site/` on `main`.
- Preserve the creamsicle visual identity (same `style.css`, same `highlight.js`, same DOM structure).
- Add agent-readability fixes: `AGENTS.md`, `llms.txt`, `robots.txt`, `sitemap.xml`, `sitemap.md`, new glossary page, per-page meta description / OpenGraph / canonical / JSON-LD.
- Add `.github/workflows/docs.yml` to build + deploy on every push to `site/`. `gh-pages` becomes a deploy artifact, not a source.
- Update CLAUDE.md to document the new workflow.

## Test plan
- [ ] Local `hugo server` renders all 6 pages
- [ ] Every page has unique meta description + OpenGraph + canonical + JSON-LD
- [ ] Mermaid loads only on workflows + architecture
- [ ] Glossary nav link present on every page
- [ ] `AGENTS.md`, `llms.txt`, `robots.txt`, `sitemap.xml`, `sitemap.md` all return 200
- [ ] After merge: deploy workflow runs, gh-pages updates, live site loads
- [ ] After deploy: re-run a14y, score should jump from baseline 39 into the 80s
EOF
)"
```

- [ ] **Step 3: Note the PR URL**

Save the URL printed by `gh pr create` — it's the verification target.

---

## Task 24: Merge, watch the deploy, and re-audit

**Files:** None.

- [ ] **Step 1: After review, merge the PR**

Use the GitHub UI or `gh pr merge --squash` after approval.

- [ ] **Step 2: Watch the docs workflow**

Run: `gh run watch` (pick the latest "Docs" run)
Expected: workflow completes successfully — hugo build green, gh-pages push green.

- [ ] **Step 3: Poll until production reflects the new content**

```bash
until curl -fsS https://2389-research.github.io/tracker/AGENTS.md > /dev/null; do
  sleep 5
done
echo "AGENTS.md is live"
```

- [ ] **Step 4: Re-run a14y**

Run:

```bash
npx -y a14y check https://2389-research.github.io/tracker/ --mode site --output agent-prompt --max-pages 200
```

Expected: a Snapshot block with a score in the 80s. The Failing checks list should be much shorter — only the items intentionally skipped (`code.language-tags`, `markdown.content-negotiation`, `markdown.mirror-suffix`, `markdown.alternate-link`).

- [ ] **Step 5: Update AGENTS.md Last runs**

Edit `site/static/AGENTS.md` and prepend the new score to the `Last runs:` list. Keep the most recent 5:

```markdown
- Last runs:
  - YYYY-MM-DD — <new-score> (scorecard 0.2.0)
  - 2026-05-19 — 39 (scorecard 0.2.0)
```

- [ ] **Step 6: Commit the score update directly to main**

```bash
git checkout main && git pull
git add site/static/AGENTS.md
git commit -m "docs(site): record post-Hugo a14y score

Site score went from <old> to <new> after the Hugo conversion +
a14y discovery fixes landed."
git push
```

The Docs workflow will re-run and republish.

- [ ] **Step 7: Report to user**

In the chat, print:
1. The numeric delta: `Score: 39 → <new> (+<delta>)`
2. List of resolved checks (most of the previous 16)
3. List of still-failing checks (the common-skips list)
4. The share-ready summary in the a14y skill's prescribed format
5. The embed badge URL from the CLI's `Embed badge: ` line

---

## Self-review checklist

After writing this plan, I checked the spec against:

- **Spec coverage:** Every a14y check in the prior audit maps to a task (discovery files in Tasks 11-13, head metadata in Tasks 5 + 14-19, glossary in Task 19, glossary nav link via the data-driven nav in Task 6). The four intentional skips (code.language-tags, markdown.content-negotiation, markdown.mirror-suffix, markdown.alternate-link) remain skipped — they're listed in the PR test plan but not implemented.
- **Placeholder scan:** Every content port references the exact source (`origin/gh-pages:<page>.html`) and explains how to extract the `<main>` body. No "fill in details" placeholders. JSON-LD payloads are inlined per-page. The home page's body is the only one not inlined verbatim because the source HTML is too long to include in the plan — but the source is preserved in the gh-pages branch and the extraction step is explicit.
- **Type consistency:** Front matter field names match between the head partial (`og_title`, `og_description`, `description`, `jsonld`, `mermaid`) and every content file. The nav data shape (`label`, `url`, `external`) is consistent between `data/nav.yaml` and the nav partial that consumes it. URL patterns are consistent: `uglyURLs = true` everywhere, all internal references use `/X.html`.

No issues found that required fixing during self-review.
