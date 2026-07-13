# Complexity Gate: Make It Real, Green, and Enforced (#468) — Design

**Date:** 2026-07-13
**Status:** design approved
**Scope:** `Makefile`, `.github/workflows/ci.yml`, `.git/hooks/pre-commit`, new `scripts/complexity/` (gate + baseline), docs. No production Go behavior change.

## Problem

The canonical `make ci` gate cannot pass on `main`. `make complexity` reports ~86 functions over cyclomatic 8, ~108 over cognitive 8, and 29 production files over 500 lines — against the stated strict limits (cyclo 8 / cognitive 8 / 500 lines). Three compounding failures make the gate decorative:

1. **Not enforced anywhere authoritative.** `.github/workflows/ci.yml` does **not** run the complexity gate (no `gocyclo`/`gocognit`/`make ci`). The only thing that runs it is the pre-commit hook, which is **staged-files-only** and **warns-and-skips when the tools aren't installed**. So every incremental commit passed its own slice while the whole-repo baseline was never forced down — the scoped-vs-whole-repo trap (same class as #436).
2. **Worktree double-count.** The Makefile scans `.`, pulling in `./.worktrees/` and `./.claude/worktrees/` copies; `gocognit` doesn't skip dot-dirs, inflating counts ~10× (e.g. 1129 raw vs ~108 real). The number is meaningless noise.
3. **Unpinned analyzers.** `gocyclo`/`gocognit` are installed `@latest` per whatever is on PATH; complexity results drift between environments.

## Decisions (from brainstorming)

- **Keep the strict limits (8 / 8 / 500); grandfather the baseline and burn it down.** Non-weakening (per #468 AC1). Do NOT relax thresholds.
- **Add the CI job now** so PRs are actually gated on "no new violations" (#468 AC2).
- **Ratchet via an identity baseline (allowlist)** — chosen over `--new-from-rev` because it catches a new violation even in an *untouched* file, and it produces a checked-in artifact that can only shrink (visible burn-down; matches the project's trust/transparency ethos). `--new-from-rev` is the milestone-scoped tool (#436); the whole-repo baseline wants a real baseline.

## The fix

### 1. De-inflate the scans (the bug)

Every scan builds its file list from one explicit `find` and passes it to the tools, instead of scanning `.` and grep-filtering output. Excluded: `./.worktrees/*`, `./.claude/*`, `./vendor/*`, `*_test.go`, `./cmd/tracker-conformance/*`. This removes the worktree/`.claude` copies at the source. The shared exclusion list lives in one place (`scripts/complexity/files.sh` or a Makefile variable) and is used by the Makefile target, `gate.sh`, and mirrored in the pre-commit hook.

### 2. Pin the analyzers

Invoke via `go run` at pinned versions (the versions #468 cites), so there is no PATH dependency and CI cannot silently skip:
- `go run github.com/fzipp/gocyclo/cmd/gocyclo@v0.6.0`
- `go run github.com/uudashr/gocognit/cmd/gocognit@v1.2.1`

The pinned versions are declared once (Makefile variables `GOCYCLO_VERSION`/`GOCOGNIT_VERSION`) and reused by the Makefile, `gate.sh`, and CI. The pre-commit hook keeps its `command -v` fast path for local dev (warn-skip is acceptable locally because CI is now authoritative), but its install hint is updated to the pinned versions.

### 3. Baseline allowlist (the ratchet)

**`scripts/complexity/baseline.txt`** — one line per grandfathered violation:
```
<metric>\t<file>\t<func>\t<value>
```
where `<metric>` ∈ {`cyclo`, `cognitive`, `filesize`}, keyed **line-insensitively** (no `line:col`) so it survives ordinary edits. For `filesize`, `<func>` is `-` and `<value>` is the line count. Sorted for stable diffs.

**`scripts/complexity/gate.sh`** — the ratcheted check (also what `make complexity` calls):
1. Compute the current violation set (gocyclo + gocognit + file-size), normalized to the baseline format, sorted.
2. `NEW = current \ baseline` (violations present now but not grandfathered — including any whose `value` rose above the baselined value, since the value is part of the key). If `NEW` is non-empty → print them and **exit 1**.
3. On success print a one-line summary (e.g. `complexity gate OK: N grandfathered, 0 new`).

Because the value is part of the key, a violation getting *worse* reads as a new line and fails; fixing one drops it from `current` (still a subset → passes). To burn down, a dev fixes code then runs **`make complexity-update`** (regenerates `baseline.txt` from current) — the reviewer sees the baseline **shrink** in the PR diff.

**Non-weakening guarantee — `scripts/complexity/gate.sh --check-baseline-shrinks` (run in CI):** compares the committed `baseline.txt` against the PR base's version (`git show <base>:scripts/complexity/baseline.txt`) and **fails if any line was added** (the baseline may only shrink or stay equal). This stops someone regenerating a *larger* baseline to launder new debt. On `main`/first-commit (no base version) it's a no-op.

### 4. Wire into CI (`ci.yml`)

Add a `complexity` job (or step in the existing job) that:
- sets up Go (matching the repo's version),
- runs `bash scripts/complexity/gate.sh` (pinned tools via `go run`),
- runs `bash scripts/complexity/gate.sh --check-baseline-shrinks` against `${{ github.event.pull_request.base.sha }}` (skipped on push-to-main),
- fails the PR on new violations or a grown baseline.

`make ci` keeps `complexity` in its target list and is now **green on `main`** because the baseline grandfathers the current violations.

### 5. Pre-commit hook

Keep the staged-files fast check as the quick local signal; fix its exclusions to match (drop worktree/`.claude` paths); update the install hint to the pinned versions. It stays best-effort locally (CI is the authority).

### 6. Documentation

- Makefile comments + a short `scripts/complexity/README.md` explaining: the baseline is a *ceiling that only shrinks*, how to burn down (`make complexity-update`), and the pinned tool versions.
- Update `CLAUDE.md` "Before committing" if it references `make ci`/complexity, noting the baseline flow.

## What this does NOT do (scope boundary / YAGNI)

It makes the gate **green today, enforced on PRs, and burning down** — it does **not** decompose the ~108 functions / 29 oversized files. That reduction is ongoing work already mapped to refactor issues (#452 write_enriched_sprint, #453 tracker_doctor, #450 root package, #449 log routing, etc.); each is an opportunity to prune a baseline entry. The 29-file and function baselines simply grandfather the current state.

## Testing

- **`gate.sh` unit/fixture tests** (`scripts/complexity/gate_test.sh` or a Go test invoking the script): given fixture `baseline.txt` + a fixture "current" set — (a) subset → exit 0; (b) an extra violation → exit 1 naming it; (c) a baselined violation with a higher value → exit 1; (d) `--check-baseline-shrinks` with an added line → exit 1, with only-removed lines → exit 0.
- **`make complexity` exits 0 on current `main`** (the committed baseline grandfathers reality). This is the acceptance signal.
- **`make ci` is green** end-to-end locally.
- The CI job is green on `main` after merge.

## Open risks

- **Baseline drift from renames:** renaming a still-complex function changes its `func` key → reads as "new violation" and fails until `make complexity-update` is run. Acceptable — a rename is a change that should re-baseline; documented in the README.
- **`go run @version` cold-cache latency in CI:** first run downloads the tool modules; mitigated by Go module cache in the CI action. Minor.
- **The gate does not (yet) prevent a file *growing* within the 500 baseline** — filesize is keyed on the grandfathered line count, so a baselined 700-line file growing to 900 reads as a new violation (fails). Good — that's the intended ratchet on size too.
