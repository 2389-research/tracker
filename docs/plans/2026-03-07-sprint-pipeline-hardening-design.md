# Sprint Pipeline Hardening Design

**Date:** March 7, 2026

## Goal

Harden sprint-execution pipelines so they only select runnable sprint docs, fail explicitly when sprint metadata is inconsistent, and keep all stage artifacts inside `.tracker/runs/`.

This work covers two scopes:

- generic `tracker` runtime behavior where the bug applies to any pipeline
- the specific RemixOS sprint pipeline in `/Users/harper/Public/src/2389/justin-remix/remix-3-tracker`

## Problem

The RemixOS run failed for two different reasons with different ownership:

1. **Pipeline-specific sprint mismatch**
   The ledger contains sprint IDs `001`-`020` and `101`-`133`, but `.ai/sprints/` only contains `SPRINT-101.md` through `SPRINT-133.md`. The pipeline selected `001` anyway because selection logic only looked at the ledger, not the available sprint docs.

2. **Generic artifact propagation bug**
   Parallel branches lost `InternalKeyArtifactDir`, so review and critique nodes running inside parallel fan-out could fall back to the working directory and write stage artifacts into repo root.

These should not be solved in the same layer. The artifact bug belongs in `tracker`. The sprint-doc mismatch belongs in the sprint pipeline that created the mismatch.

## Chosen Direction

Apply a split fix:

- fix the artifact propagation bug in `tracker`
- harden the RemixOS sprint pipeline directly so it can bootstrap missing sprint docs and refuse to run mismatched sprints

This avoids overfitting `tracker` to one repo’s sprint conventions while still making the specific pipeline reliable.

## Tracker Changes

### Parallel artifact inheritance

Parallel branch contexts must preserve `InternalKeyArtifactDir` from the parent pipeline context.

That ensures review and critique branches write to:

- `.tracker/runs/<run-id>/<node-id>/...`

instead of:

- `<repo-root>/<node-id>/...`

### Verification

Add a regression test proving that a branch executed through the parallel handler can still read the internal artifact directory.

## RemixOS Pipeline Changes

### 1. Bootstrap missing sprint docs

The pipeline should create `SPRINT-001.md` through `SPRINT-020.md` from the buildout plan if they are missing.

The source of truth is:

- `/Users/harper/Public/src/2389/justin-remix/remix-3-tracker/docs/plans/2026-03-06-remixos-spec-sprint-buildout.md`

The bootstrap step should be deterministic and idempotent.

### 2. Select only runnable sprints

Sprint selection must consider both:

- ledger status
- actual existence of `.ai/sprints/SPRINT-<id>.md`

If a ledger sprint has no sprint doc, it must not be selected as runnable work.

### 3. Fail fast on missing sprint docs

`ReadSprint` should not drift to a different sprint file when the selected doc is missing.

Instead:

- fail clearly with the target sprint ID
- stop the pipeline or route to failure summary

### 4. Tighten validation

The current validation step treats:

- `go build ./...`
- `go test ./...`

as sufficient, even when `go test` returns `[no test files]`.

For test-requiring sprints, that should be a failure.

The simplest rule is:

- if the selected sprint doc contains `**Test:` checklist items or `*_test.go` expected artifacts, then `[no test files]` is not acceptable

This keeps validation lightweight but materially more honest.

## Architecture

### Tracker layer

`tracker` remains generic. It does not learn RemixOS-specific sprint numbering or sprint-plan semantics.

Its responsibility is only:

- preserve internal engine state across parallel branches
- keep artifact routing correct

### Pipeline layer

The RemixOS sprint pipeline owns:

- sprint bootstrapping from the buildout plan
- sprint-file-aware selection
- validation rules tied to sprint expectations

This is the correct boundary because those policies are project-specific, not engine-wide.

## Data Flow

The hardened RemixOS pipeline should flow like this:

1. Ensure ledger and sprint directories exist.
2. Bootstrap missing `SPRINT-001.md` through `SPRINT-020.md` from the plan.
3. Select the first non-completed sprint that also has a matching sprint file.
4. Write that ID to `.ai/current_sprint_id.txt`.
5. Read exactly that sprint file.
6. Implement.
7. Validate with sprint-aware checks.
8. Run reviews and critiques.
9. Keep all stage artifacts under `.tracker/runs/...`.

## Error Handling

Failure modes should become explicit:

- missing sprint file after bootstrap -> fail
- malformed buildout plan section for a sprint -> fail
- test-required sprint with `[no test files]` -> fail

These are preferable to silent drift or false green validation.

## Testing Strategy

### Tracker

- parallel handler regression test for `InternalKeyArtifactDir`

### RemixOS pipeline

- smoke check that bootstrapped `SPRINT-001.md` exists
- smoke check that selected sprint has a matching file
- validation script test that rejects `[no test files]` for test-required sprints

## Output

The next artifact is an implementation plan covering:

- the `tracker` regression test and parallel-handler fix
- the RemixOS `sprint_exec.dot` hardening
- the sprint-doc bootstrap mechanism
- validation tightening
