# Tracker Rename Implementation Plan

> **For Claude:** REQUIRED SUB-SKILL: Use superpowers:executing-plans to implement this plan task-by-task.

**Goal:** Rename the entire repository from `mammoth-lite`/`mammoth` to `tracker`, including the module path, binaries, tooling, workflows, examples, and docs.

**Architecture:** Apply the rename in a single pass but keep the work segmented: first restore compile correctness under the new module path, then align binary and tooling surfaces, then rewrite textual/docs surfaces, and finish with a repository-wide stale-reference sweep. Every task keeps the tree verifiable so regressions can be isolated quickly.

**Tech Stack:** Go modules, Go test, Make, GoReleaser, pre-commit, GitHub Actions, Markdown docs, shell search tooling

---

### Task 1: Rename the module path and Go imports

**Files:**
- Modify: `go.mod`
- Modify: Go source and test files under `agent/`, `llm/`, `pipeline/`, and `cmd/` that import `github.com/2389-research/mammoth-lite/...`
- Test: affected package tests under `agent/...`, `llm/...`, `pipeline/...`

**Step 1: Write the failing check**

Run: `rg -n 'github.com/2389-research/mammoth-lite' go.mod agent llm pipeline cmd`

Expected: matches in `go.mod` and multiple Go files.

**Step 2: Update the module path and imports**

- Change `module github.com/2389-research/mammoth-lite` to `module github.com/2389-research/tracker`.
- Rewrite every internal import from `github.com/2389-research/mammoth-lite/...` to `github.com/2389-research/tracker/...`.

**Step 3: Run formatting and package tests**

Run: `GOCACHE=$(pwd)/.gocache go test ./agent/... ./llm/... ./pipeline/... ./cmd/...`

Expected: PASS.

**Step 4: Verify stale import removal**

Run: `rg -n 'github.com/2389-research/mammoth-lite' go.mod agent llm pipeline cmd`

Expected: no matches.

**Step 5: Commit**

```bash
git add go.mod agent llm pipeline cmd
git commit -m "refactor: rename module path to tracker"
```

### Task 2: Rename binaries and tracked tooling surfaces

**Files:**
- Modify: `cmd/mammoth/main.go`
- Modify: `Makefile`
- Modify: `.goreleaser.yml`
- Modify: `.pre-commit-config.yaml`
- Modify: `.gitignore`
- Modify: `.github/workflows/ci.yml`
- Modify: `.github/workflows/release.yml`
- Test: `cmd/conformance/main_test.go` and any tests asserting binary/help text names

**Step 1: Write the failing checks**

Run: `rg -n '\\bmammoth\\b|\\bconformance\\b|mammoth-lite' Makefile .goreleaser.yml .pre-commit-config.yaml .gitignore .github/workflows cmd`

Expected: matches showing old binary and project names.

**Step 2: Apply the binary rename**

- Rename the user-facing CLI from `mammoth` to `tracker`.
- Rename the conformance binary from `conformance` to `tracker-conformance`.
- Update build outputs, archive names, workflow artifact names, release job behavior, help text, and config comments to the new names.
- Keep the conformance command surface aligned with AttractorBench expectations while using the renamed binary filename.

**Step 3: Verify build outputs**

Run: `make build`

Expected: `bin/tracker` and `bin/tracker-conformance` exist.

**Step 4: Run targeted tests**

Run: `GOCACHE=$(pwd)/.gocache go test ./cmd/...`

Expected: PASS.

**Step 5: Commit**

```bash
git add cmd Makefile .goreleaser.yml .pre-commit-config.yaml .gitignore .github/workflows
git commit -m "build: rename tracker binaries and tooling"
```

### Task 3: Rewrite docs, examples, comments, and user-facing strings

**Files:**
- Modify: `docs/plans/**/*.md`
- Modify: `examples/**/*.dot`
- Modify: comments, panic strings, and help text in tracked source files that still mention `mammoth-lite`, `mammoth`, or the old conformance binary name
- Test: documentation and example files are validated by search; no dedicated unit tests required

**Step 1: Write the failing checks**

Run: `rg -n 'mammoth-lite|\\bmammoth\\b|\\bconformance\\b' docs examples agent llm pipeline cmd`

Expected: matches across docs, examples, comments, and strings.

**Step 2: Rewrite tracked text surfaces**

- Replace project-name references with `tracker`.
- Replace CLI references with `tracker`.
- Replace binary references that mean the conformance executable with `tracker-conformance`.
- Update embedded command transcripts and absolute example commands to the new names and module path.
- Leave external upstream names like Attractor and AttractorBench unchanged.

**Step 3: Re-run the text search**

Run: `rg -n 'mammoth-lite|\\bmammoth\\b|\\bconformance\\b' docs examples agent llm pipeline cmd`

Expected: only intentional external-context matches remain, if any.

**Step 4: Spot-check examples and docs**

Run: `sed -n '1,120p' docs/plans/2026-03-04-attractor-design.md`

Expected: header and command examples refer to `tracker`.

**Step 5: Commit**

```bash
git add docs examples agent llm pipeline cmd
git commit -m "docs: rewrite repository references to tracker"
```

### Task 4: Full verification and stale-reference sweep

**Files:**
- Verify: entire repository

**Step 1: Run the full test suite**

Run: `GOCACHE=$(pwd)/.gocache go test ./... -count=1`

Expected: PASS.

**Step 2: Run the repo build and test entrypoints**

Run: `make build`

Expected: PASS and both renamed binaries produced in `bin/`.

Run: `make test`

Expected: PASS.

**Step 3: Check the renamed conformance binary**

Run: `./bin/tracker-conformance list-handlers`

Expected: succeeds and includes `stack.manager_loop`.

**Step 4: Sweep for stale tracked references**

Run: `rg -n 'mammoth-lite|github.com/2389-research/mammoth-lite|\\bmammoth\\b|(^|[^-])\\bconformance\\b' --glob '!.git' --glob '!bin/**' --glob '!.gocache/**' .`

Expected: no stale tracked references; any remaining matches are reviewed and either rewritten or justified as external references.

**Step 5: Commit**

```bash
git add .
git commit -m "refactor: complete tracker repository rename"
```
