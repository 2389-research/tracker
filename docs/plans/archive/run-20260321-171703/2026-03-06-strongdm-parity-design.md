# StrongDM Parity Design

**Date:** March 6, 2026

## Goal

Bring `tracker` to strict parity with StrongDM's published specs for:

- `attractor-spec.md`
- `coding-agent-loop-spec.md`
- `unified-llm-spec.md`

Parity here means upstream spec behavior wins over current local behavior, even where the local repo already has passing tests.

## Chosen Strategy

Use a spec-first parity program.

That means:

1. Turn upstream requirements into local, executable tests.
2. Use those tests to identify real behavioral gaps.
3. Fix the implementation in small, reviewable slices.
4. Keep a traceable mapping from spec clauses to tests and code.

This is intentionally stricter than fixing only the obvious gaps. The known issues are only the starting point.

## Scope

The work covers all three layers in this repo:

- `pipeline/` for Attractor graph parsing, validation, execution, handlers, state, artifacts, and routing
- `agent/` for the coding-agent session loop, tool execution, provider-aligned behavior, steering, truncation, and events
- `llm/` for unified request/response types, provider routing, translation, streaming, usage, finish reasons, and retry/error behavior

The CLI entrypoints under `cmd/` are in scope where they expose or hide spec behavior.

## Architecture Direction

The repo already has the right high-level package split. The mismatch is not the package layout; it is the semantics inside each layer.

The design direction is:

- preserve package boundaries where they already fit the specs
- replace local semantics where they diverge from upstream
- prefer adding conformance-style tests before refactors
- keep implementation changes incremental so each behavior change has a clear cause

## Verification Strategy

Verification will be layered:

- parser and validator tests for DSL and semantic constraints
- engine and handler tests for runtime pipeline behavior
- agent session tests for multi-turn tool orchestration and provider profiles
- provider adapter and translation tests for unified LLM behavior
- end-to-end smoke tests using local stubs that mirror upstream examples

Each new parity test should cite the upstream requirement it protects.

## Main Known Gaps

The current repo already shows several parity breaks that should anchor the first implementation slices:

- goal gates are enforced at node failure time instead of at exit time
- `type` override and `house -> stack.manager_loop` handler resolution are missing
- condition evaluation does not support the upstream `context.*` model
- stylesheet resolution does not support shape selectors and mis-parses classes
- `codergen` is wired as one-shot text generation instead of a real coding-agent loop
- stage artifacts such as `prompt.md`, `response.md`, and `status.json` are not written

These are not the full list; they are the first verified mismatches.

## Delivery Phases

1. Build a local parity matrix and failing tests.
2. Close Attractor pipeline gaps.
3. Close coding-agent loop gaps.
4. Close unified LLM client gaps.
5. Run end-to-end conformance and tighten docs.

## Constraints

- Keep fixes inside the current repo layout unless a spec mismatch forces a larger structural change.
- Do not trust existing tests if they encode non-spec behavior.
- Avoid speculative extensions until parity work is complete.
- Prefer new parity tests over broad rewrites with weak verification.

## Output

The next artifact is a detailed implementation plan saved alongside this design doc.
