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
