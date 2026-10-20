# Tracker — Agent Reference

Tracker is a pipeline orchestration engine for multi-agent LLM workflows.
Pipelines are defined in `.dip` files (the Dippin language) and executed
with parallel agents via a TUI dashboard. Built by 2389.ai.

## Quickstart

```bash
brew install 2389-research/tap/tracker
# or: go install github.com/2389-research/tracker/cmd/tracker@latest

cd <a git repo>
tracker explain --param question="what does this repo do?"
tracker quickfix --param change="<one sentence>"
```

- `explain` is read-only: it answers one question about the repo with read / grep / glob tools only and saves the answer to `.tracker/runs/<run-id>/explain.md`.
- `quickfix` makes one change in a separate git worktree on the branch `tracker/quickfix/<run-id>`, runs the project's tests, and stops at a review gate: `approve` commits to that branch (nothing is merged or pushed), `revise` tries again (up to 3 times), `reject` removes the worktree and branch. It needs a git repo with at least one commit; `--auto-approve` takes the gate's default, `reject`. The branch, base commit and worktree are recorded in `.tracker/runs/<run-id>/quickfix.json`.
- No API key is needed when the `claude` CLI (Claude Code) is installed and signed in: both workflows use `backend: auto`, which picks Claude Code when `claude` answers `claude --version`, otherwise a configured API key (`tracker setup`). To pin a specific `claude`, set `TRACKER_CLAUDE_BIN=/absolute/path/to/claude` in the shell (a `.env` file cannot set it).
- Without `--param`, each asks first. In a terminal without a TTY, pass `--param`: human gates otherwise read stdin.
- If something is wrong: `tracker doctor` (setup, including which `claude` binaries are on PATH), `tracker diagnose` (the last run).
- `tracker workflows` lists every built-in, starters first.

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
  - 2026-05-19 — 79 (scorecard 0.2.0)
  - 2026-05-19 — 39 (scorecard 0.2.0)
