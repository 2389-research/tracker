# Tracker Rename Design

**Date:** 2026-03-06

## Goal

Rename the entire project from `mammoth-lite`/`mammoth` to `tracker`, with the canonical Go module path `github.com/2389-research/tracker`.

## Approved Decisions

- Rename the project, module path, and all tracked repo references to `tracker`.
- Rename the primary CLI binary from `mammoth` to `tracker`.
- Rename the conformance binary from `conformance` to `tracker-conformance`.
- Rewrite all in-repo references, including historical docs, examples, workflows, release config, comments, help text, and command snippets.
- Do not rename the filesystem checkout directory as part of this change.

## Scope

The rename covers:

- `go.mod` module declaration and every internal import path.
- CLI entrypoints under `cmd/` and any binary names produced by tracked tooling.
- Build and release surfaces in `Makefile`, `.goreleaser.yml`, `.pre-commit-config.yaml`, `.gitignore`, and `.github/workflows/`.
- Tests, fixtures, examples, error strings, panic messages, comments, and shell snippets.
- Documentation under `docs/`, including historical plan files and embedded command transcripts.

The rename does not cover:

- The local checkout directory name.
- Third-party upstream references that must continue to mention external project names accurately.

## Approach

Use a single in-place rename pass so the repository moves from the old identity to the new one without an intermediate mixed state.

Execution order:

1. Update the module path and all Go imports so the codebase compiles under `github.com/2389-research/tracker`.
2. Rename CLI entrypoints and binary names, then align build, release, and workflow config to the new binary contract.
3. Rewrite docs, examples, comments, strings, and historical artifacts to use `tracker` and `tracker-conformance`.
4. Run full verification and search for leftover tracked references to `mammoth-lite`, `mammoth`, and legacy conformance naming.

## Naming Rules

- Module path: `github.com/2389-research/tracker`
- Primary binary: `tracker`
- Conformance binary: `tracker-conformance`
- Project name in user-facing text: `tracker`

When updating text, prefer exact replacements that preserve the surrounding meaning instead of blanket token swaps. In particular:

- Keep external references to StrongDM's Attractor and AttractorBench unchanged.
- Keep generic English uses of words like `tracker` or `conformance` unchanged unless they are acting as project identifiers.

## Verification

The rename is complete when:

- `go test ./...` passes.
- `make build` produces the renamed binaries.
- `make test` passes.
- Release and workflow config reflect the renamed binaries and module path.
- Targeted searches show no stale tracked references to `mammoth-lite`, `mammoth`, or old conformance binary names, except where external references require them.

## Risks

- Historical docs contain many literal paths and commands, so broad text replacement can create accidental mistakes if it is not followed by verification.
- The imported `.github` workflows are already inconsistent with the repo, so the rename should correct them to the current repo contract rather than preserving their old assumptions.
- The working tree already contains unrelated tracked changes, so commits for this work need to stay scoped to the rename files only.
