# Transport Hardening — Plan Index

**Date:** 2026-07-20
**Branch of origin:** `feat/transport-boundary-474`
**Status:** ✅ shipped (Ships 1–4 + durability proof + coverage follow-ups) — merged to `main`
**Scope:** make the transport boundary (core `tracker.Config` → `Engine`, `RunManager`,
the `Interviewer` seam, the event streams) and its consumers (TUI, `cmd/trackerbot`,
future web/mobile) **robust, safe, and scalable to new features across every surface.**

## Outcome (delivered)

All phases landed on `main` (see `CHANGELOG.md` for the itemized entries):

- **Ship 1 — failure containment:** engine + RunManager panic recovery; terminal-status
  backstop for nil-result exits; `retry_target` validation; atomic checkpoint/state
  writes; path-traversal guard; typed-nil client guard.
- **Ship 2 — safe & governed:** authz allowlist; fail-closed budget; workdir reaper +
  orphan sweep; `TokenTracker` double-count guard; terminal-status doc scoping.
- **Ship 3 — kill the false green:** deleted dead `chooseInterviewer`, repointed at the
  live selection; fixed the Slack button codec misroute; gateway-via-`Config` test.
- **Ship 4 — conformance:** `transport/conformance` suite (reference impl + `SlackInterviewer`
  both pass); boundary doc lists inherited invariants.
- **Durability:** proved resume-at-a-gate skips the completed upstream node — **TD.3 needed
  no code** (node-granularity + advance-time checkpointing already satisfy it).
- **Coverage follow-ups:** `Config.Subgraphs` wiring test; NDJSON `StreamEvent` wire-envelope
  stability guard.

The per-phase docs below are kept as the design record; where a phase's assumption turned
out to already hold (TD.3), the outcome above is authoritative.

## Why this exists

After the transport-boundary + Slack (`trackerbot`) work landed, a four-dimension
review (correctness/concurrency, security, test coverage, API/docs) plus a targeted
crash-recovery audit found a cluster of defects. Every one is a **missing invariant at
a shared seam** — a panic that isn't contained, a terminal signal that isn't guaranteed,
a source name that isn't validated, a recovery file written unsafely.

The organizing principle of this plan:

> Enforce each invariant **once, in the core or `RunManager`**, so the TUI, Slack, and
> any future web/mobile transport inherit it for free. A transport should be able to
> choose *presentation*, never be able to violate a safety or durability property.

That is what turns "fix these bugs" into "scalable across surfaces."

## The phases

| Phase | Doc | Theme | Ship |
|---|---|---|---|
| **0** | [`phase0-failure-containment`](2026-07-20-transport-hardening-phase0-failure-containment.md) | Contain failure & guarantee terminal signals (core invariants) | 1 |
| **1** | [`phase1-safety-and-authz`](2026-07-20-transport-hardening-phase1-safety-and-authz.md) | Safe by construction — source containment, authz, budget, resource lifecycle | 1–2 |
| **D** | [`durable-recovery`](2026-07-20-transport-hardening-durable-recovery.md) | Crash recovery — atomic writes, checkpoint-before-gate, precise resume | 1–2 |
| **2** | [`phase2-contract-hardening`](2026-07-20-transport-hardening-phase2-contract-hardening.md) | Close API foot-guns — typed-nil client, token double-count, doc truth-up | 2 |
| **3** | [`phase3-test-coverage`](2026-07-20-transport-hardening-phase3-test-coverage.md) | Close the CLI-unification coverage blind spots; delete false green | 3 |
| **4** | [`phase4-transport-conformance`](2026-07-20-transport-hardening-phase4-transport-conformance.md) | Scalable to new surfaces — conformance harness, event-stream stability | 4 |

## Priority — what bites a live bot first

Ranked by real-world impact for the stated product (a concurrent Slack bot driving
paid runs):

1. **Phase 0** — a handler panic on one run's goroutine kills the whole process; two
   classes of error exit emit no terminal event, hanging a Slack thread forever.
2. **Durable Recovery** — checkpoint and state files are written non-atomically; a
   crash *during* the write can corrupt the one file whose job is recovery.
3. **Phase 1** — the grammar intent fallback loads an arbitrary `.dip` off the host;
   any channel member can trigger paid runs.
4. **Phase 2** — a typed-nil client trap and a token double-count foot-gun.
5. **Phase 3 / 4** — coverage and the conformance harness that keep the above from
   regressing as surfaces #4, #5, #6 are built.

## Ship sequencing

- **Ship 1 (this week):** Phase 0 in full + Phase 1 source-containment + Durable
  Recovery atomic writes + Phase 2 typed-nil. One focused "boundary hardening" PR —
  these are the live-bot risks and they are all small, low-blast-radius changes.
- **Ship 2:** rest of Phase 1 (authz, budget, reaper), rest of Durable Recovery
  (checkpoint-before-gate), rest of Phase 2 (token guard, docs).
- **Ship 3:** Phase 3 coverage — delete dead `chooseInterviewer`, add the gateway /
  interviewer-selection / backstop / codec / subgraph tests.
- **Ship 4:** Phase 4 conformance harness + event-stream stability contract — the
  investment that keeps every future transport cheap and safe.

Each ship updates `CHANGELOG.md` and `ROADMAP.md` in the same PR, per the Maintenance
contract in `CLAUDE.md`.

## Confirmed vs. plausible

Every finding referenced in these docs was **re-derived against the source** before
being written down (file:line anchors are in each phase doc). Findings are tagged
`CONFIRMED` (reproduced in code) or `PLAUSIBLE` (argued but not fully reproduced).
Nothing in this plan is a speculative "a review said so."

## Open decisions (need a call before the relevant phase)

- **D-1 (Phase 4):** does the conformance harness live in the core module (every
  transport imports it as a test dependency) or as a standalone `transport/conformance`
  package? Affects the core module's test-only dependency surface.
- **D-2 (Phase 1):** admission policy at capacity — reject (today), queue, or preempt
  oldest. Currently `ErrAtCapacity` → reject; queueing is a product choice.
- **D-3 (Durable Recovery):** on a corrupt state file, fail loud and refuse to start,
  or degrade to empty + warn? Trade-off is "lose all resume" vs. "block the bot."
