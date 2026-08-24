# Verification cadence in `build_product` (issue #490)

> Status: **proposal / design note.** No engine or `.dip` change is shipped with
> this document. The two candidate fixes are `build_product.dip` edits that
> change *what gets checked and when*, so they need a real-run A/B on the
> cost/coverage trade-off before landing (the maintainer scoped #490 this way in
> the issue thread — same class as #353's reviewer-cap *value*). This note
> records the driver, the options, and a recommendation so that A/B is set up
> deliberately rather than as a blind edit to the flagship pipeline.

## The observation

On run `0aea2a4f9e95` (a clean 10-milestone walk, no graph-level fix loops):

| Node               | Turns | Model         |
|--------------------|-------|---------------|
| `Implement`        | 321   | opus-4-6      |
| `VerifyMilestone`  | 305   | sonnet-4-6    |

`VerifyMilestone` ran once per milestone and consumed ~95% of `Implement`'s
turns, roughly doubling the LLM cost of each milestone. Note the asymmetry:
verify runs on the *cheaper* model (sonnet vs opus) yet still reaches turn
parity — so the driver is turn count (re-derivation work), not model choice.
Per-node dollar cost is now visible directly in the `tracker run` node-execution
table (Cost column, shipped on `main` `2966574`), so the before/after is
measurable at a glance.

## The driver

`VerifyMilestone` (`examples/build_product.dip`, node `VerifyMilestone`) is a
high-`reasoning_effort` agent that **re-derives whole-spec context every
milestone**. Each invocation:

- reads `.ai/build/build-context.md` (architecture map + per-milestone log),
- reads `.ai/milestones/current.md`,
- reads **the full `SPEC.md`** (deliberately — check 5 / "SPEC GAPS BEYOND
  MILESTONE NOTES" exists precisely to catch a requirement `Decompose` dropped,
  which is only visible against the whole spec, not the milestone's own
  done-when),
- runs `git log`/`git diff` to reconstruct the milestone's commit range,
- greps the implementation for **every spec literal** in the covered sections
  (check 3), and
- dispositions behavioral contracts (check 5b) with per-contract grep evidence.

That whole-spec cross-check is the source of both the cost *and* the signal: it
is what catches over-build, dropped requirements, and prose/contract drift that
`TestMilestone` (which only runs the language-stack tests + CI gate) cannot see.
So the cost is not waste — it is thoroughness applied at per-milestone
granularity. The question is whether per-milestone is the right *cadence* for
that thoroughness, not whether the checks are valuable.

## Options

### A. Verify at phase / dependency boundaries, not every milestone

Rely on `TestMilestone` for per-milestone gating, and run the heavy
`VerifyMilestone` cross-review only at phase boundaries (e.g. after the last
milestone of a dependency group).

- **Upside:** biggest cost reduction — verify runs O(phases) instead of
  O(milestones). Where phases group 3–4 milestones, that is a ~60–75% cut in
  verify spend.
- **Cost/coverage risk:** a dropped requirement or over-build introduced in
  milestone *k* is not caught until the phase boundary, by which point later
  milestones may have built on it. The fix loop then reopens a larger blast
  radius. The value of per-milestone verify is *early* detection.
- **Structural cost:** "phase" is currently only a **spec/comment concept** in
  `build_product.dip` — milestones are a flat, dependency-ordered list with no
  first-class phase grouping the graph can route on. Implementing A requires
  `Decompose` to emit phase membership (e.g. a phase tag per milestone in
  `.ai/decisions/milestones.md`) and a routing/counter change so `PickNextMilestone`
  knows when a phase closes. That is a real change to a fragile loop (the restart
  counter is global — see CLAUDE.md "Checkpoint resume is fragile"), not a
  one-line edit.

### B. Scope `VerifyMilestone`'s reading to the milestone diff

Keep per-milestone cadence but have the verifier read only the milestone diff
plus its done-when, skipping the full `SPEC.md` re-read.

- **Upside:** cheaper per invocation, cadence unchanged, no graph change.
- **Cost/coverage risk:** this directly removes the check-5 signal. The whole
  point of the full `SPEC.md` read is to find requirements *absent* from the
  milestone's done-when (the "code-goblin miss" the prompt calls out). A
  diff-scoped verifier by construction cannot see a dropped requirement, because
  a dropped requirement leaves no diff. This trades the most valuable half of
  the verifier's coverage for cost.
- **Partial variant (`B-split`):** split the verifier — a cheap per-milestone
  diff check (checks 1–4: done-when + files-in-scope + literals + test-fidelity,
  all diff-local) every milestone, and the expensive whole-spec gap sweep
  (check 5 / 5b) only at phase boundaries. This is A applied to *one half* of the
  checks and carries A's structural cost for the phase-boundary piece.

### C. Cheaper knobs, cadence unchanged

Lower `reasoning_effort` on `VerifyMilestone` from `high` to `medium`, and/or
cache the static inputs. This is the lowest-risk lever but likely the smallest
win: the turn count (re-derivation work: git archaeology + per-literal grep) is
the driver, and those turns are tool-driven, not reasoning-depth-driven.
Prompt-caching `SPEC.md` does not help either — the agent reads it through a
`Read` tool call inside the turn loop, not as a static prompt prefix.

## Recommendation

1. **Do not ship a blind edit.** Both A and B change coverage; the maintainer
   flagged #490 as wanting run data, not a flagship edit on intuition.

2. **Preferred direction: `B-split`, validated with an A/B run.** Keep checks
   1–4 (all diff-local) per milestone — they are cheap relative to the
   whole-spec sweep and give early over-build / test-fidelity detection. Move the
   whole-spec gap sweep (check 5 / 5b) to a phase boundary. This preserves early
   detection of the failure classes that compound (over-build, weak tests) while
   cutting the expensive whole-spec re-derivation to O(phases). It still incurs
   A's structural cost (phase membership from `Decompose` + a boundary counter),
   so it is not free — but it is the option that keeps the most signal per dollar
   saved.

3. **If phase plumbing is too heavy for the near term, ship C first** as a
   measured, reversible step (drop `reasoning_effort` to `medium`) and record the
   Cost-column delta. It will under-deliver on the headline number but is a
   genuine no-coverage-loss win and validates the measurement harness for the
   later A/B.

4. **Measurement protocol for the A/B** (whichever option is trialed): run the
   same spec twice — once on current `main`, once on the branch — and compare the
   per-node Cost column (`VerifyMilestone` vs `Implement`) *and* the count of
   real issues `VerifyMilestone` caught (over-build, dropped-requirement,
   test-fidelity FAILs). A cost cut that also drops caught-issue count to zero on
   a spec that previously surfaced issues is a regression, not a win. Use a spec
   known to contain a droppable requirement (so the check-5 signal is exercised)
   and cap spend with `cost_exceeded_action: fail` (#353).

## Why this is not shipped here

Implementing A or `B-split` touches the milestone loop and the `Decompose`
output contract, and its correctness is a cost/coverage *trade-off* that only a
real build reveals — exactly the "don't force a blind change to fragile code"
case. The visibility primitives to run the A/B are already in place (per-node
Cost column `2966574`; cost-asymmetry detector in `tracker diagnose`; safe caps
via #353), so the next step is a measured trial, not a speculative edit.
