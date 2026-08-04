# Freeze and prove: the process pattern for security-boundary PRs

Process guidance for authoring and reviewing PRs that touch a security boundary.
Follow-up to [#286](https://github.com/2389-research/tracker/issues/286);
grounded in how [#275](https://github.com/2389-research/tracker/pull/275) (the
`writable_paths` Landlock jail, issue
[#272](https://github.com/2389-research/tracker/issues/272)) was actually built
and reviewed.

## Why this exists

#275 landed the jail after **13 rounds of review and ~30 findings**. Some of
that churn is inherent — security PRs touching kernel primitives have a large
boundary-case surface and reviewers are trained to probe it. But a meaningful
fraction was *reactive*: ship round N, a reviewer catches a case, round N+1
fixes it *and* introduces a fresh option or comment, which the next round
scrutinizes. Each iteration re-opened the design.

The discipline that shortens this is in the name: **freeze the threat surface
and the public API before you implement, prove each residual risk is addressed,
and make the implementation a small patch against a contract that's already been
agreed.** The design cost moves up-front, where a finding is a markdown edit
instead of a code round.

This is **not** a blanket rule. A one-line glob-validation tweak or a typo fix
in `jail_errors.go` should not trigger the full ritual. It is for changes with
the blast radius of #272/#275: a new enforcement mechanism, a new subprocess
spawn path, a change to what the boundary promises.

## When it applies

Use this pattern when a PR touches any of:

- `agent/exec/jail*.go` — the Landlock / `openat2` jail and its validation.
- `agent/exec/env.go`, `agent/exec/local.go` — the `ExecutionEnvironment`
  interface (the single seam every agent-tool write and subprocess flows
  through).
- `pipeline/handlers/codergen_jail.go` — where the jail is wired into `env`.
- `cmd/tracker/main.go`'s `__jail-exec` dispatch — the re-exec entry point that
  applies Landlock before `syscall.Exec`.
- Any new code in `agent/tools/` that mutates the filesystem or spawns a
  subprocess.
- The tool-command denylist / allowlist, env-var stripping, or the
  activity-log integrity path (`pipeline.SecureActivityLogPath` and callers).

When in doubt, ask in the design step (step 1) whether the change is in scope —
that decision is cheap and itself part of the pattern.

## The pattern

### 1. Spec first — freeze the threat surface

Write the threat model and intended invariants down **before** implementing,
in `docs/superpowers/specs/` (see
[`2026-06-01-issue-272-writable-paths-enforcement-design.md`](../superpowers/specs/2026-06-01-issue-272-writable-paths-enforcement-design.md)
for the shape). State explicitly:

- what the boundary protects against, and
- what it explicitly does **not** — the residual risks (for the jail: network
  egress, reads / exfil-by-read, out-of-process backends, reflective dispatch).

Get sign-off on that scope before code. This step is **current practice** —
Tracker gates design before code on every non-trivial change (see
[`docs/notes/2026-07-16-how-we-build-tracker.md`](../notes/2026-07-16-how-we-build-tracker.md)),
and the #272 spec was reviewed by an adversarial panel before implementation
began. Enumerating the residual risks *in the spec* is the part worth insisting
on for a security boundary: an unaddressed risk should be a conscious "not
covered", never a silent gap discovered in round 8.

### 2. Freeze the public API

List every public function, option, interface method, and CLI/`.dip` attribute
the change introduces. Write the GoDoc for each **before** writing the body, and
get review on the *shape* — names, signatures, what each promises — before any
implementation. A signature agreed up front is one that isn't renegotiated in
round 6.

This is the **aspirational** delta this pattern adds: today the API tends to
settle during implementation and review, which is a large share of the #275
churn. Freezing it first is the discipline #286 proposes.

### 3. Prove with invariant tests, not examples

For each contract, write property/invariant tests that exercise the boundary
with inputs the implementer wouldn't naturally reach for — randomized generators
(`pgregory.net/rapid`, `testing/quick`) or fuzzers — not a handful of
hand-picked examples. Run them in CI.

This is **partially current practice**:
[`agent/exec/jail_property_test.go`](../../agent/exec/jail_property_test.go)
(issue #282, a direct follow-up to #275) pins the jail's validation invariants
with `rapid`, and its own header note records that "the #275 review took 13
rounds / ~30 findings on exactly these properties" — i.e. properties a reviewer
had to *find* by hand could have been proved mechanically instead. The
adversarial decode check that brute-forced the escape-aware condition parser
over 88,000+ inputs is the same idea. The delta this pattern asks for is
*ordering*: write the invariants before opening the PR, so the proof exists when
review starts rather than being added reactively.

### 4. Audit-class sweep before opening the PR

Walk the diff against the standing audit checklist and record each class as
**addressed** / **not applicable to this PR**. For the jail surface that
checklist is
[`agent-tool-jail-checklist.md`](./agent-tool-jail-checklist.md) — one row per
LLM-callable tool, the `ExecutionEnvironment`-routing invariant, and the
`make tools-jail-check` lint that enforces it.

This is **current practice**, and it has caught real bugs: the #275 round-8
audit found `generate_code` and `write_enriched_sprint` calling `os.WriteFile`
directly — bypassing the jail entirely — tools that had been in the tree for
months. That audit is now mechanized as `make tools-jail-check` (wired into the
`ci:` target). The residual-risk enumeration from step 1 is the second half of
this sweep: confirm each documented "not covered" risk is still only that, and
no new one crept in.

### 5. Implementation is the smallest visible commit

Because the spec, API, tests, and audit are settled first, the implementation
should be a small, contained patch against a proven contract — reviewable as
"does this satisfy the frozen API and pass the invariants" rather than a fresh
design discussion. The design/doc/test work and the implementation land in the
same PR (or the design lands first and is referenced).

This ordering is **aspirational** — it's the payoff of steps 1–4, not a rule
that stands alone. It only holds if the earlier freezes actually held.

## Current vs proposed, at a glance

| Step | Status | Evidence |
|---|---|---|
| 1. Spec-first threat model | **Current** | design-gated workflow; #272 spec reviewed before code |
| 2. Freeze the public API | **Proposed** (#286) | API settles during review today — a source of #275 churn |
| 3. Invariant/property tests | **Partial → propose ordering** | `jail_property_test.go` (#282) exists; write it *before* the PR |
| 4. Audit-class sweep | **Current** | `agent-tool-jail-checklist.md` + `make tools-jail-check`; round-8 audit |
| 5. Smallest implementation commit | **Aspirational** | consequence of 1–4 holding |

The honest read: Tracker already does the *design-first* and *audit* halves well.
The delta #286 asks for — and this doc records as the intended discipline for
the next high-blast-radius security PR — is **freezing the API and writing the
invariants up front**, so review verifies a contract instead of co-authoring it.

## See also

- [`agent-tool-jail-checklist.md`](./agent-tool-jail-checklist.md) — the audit
  checklist and `ExecutionEnvironment` invariant referenced in step 4.
- [`docs/superpowers/specs/2026-06-01-issue-272-writable-paths-enforcement-design.md`](../superpowers/specs/2026-06-01-issue-272-writable-paths-enforcement-design.md)
  — the #272 spec (the shape step 1 should follow).
- [`docs/notes/2026-07-16-how-we-build-tracker.md`](../notes/2026-07-16-how-we-build-tracker.md)
  — the design-before-code / adversarial-review methodology this builds on.
- `CLAUDE.md` → "Tool node safety", "writable_paths"/`__jail-exec`, "Never
  silently swallow errors" — the standing security rules a boundary PR must not
  regress.
