# Parallel milestone waves for `build_product` (#420) — design

**Status:** proposed · **Issue:** #420 · **Date:** 2026-09-22

## Problem

`build_product` builds milestones strictly one at a time (`PickNextMilestone → Implement → CommitIfDirty → TestMilestone → VerifyMilestone → MarkMilestoneDone`, looping). Independent foundation milestones (those whose plan `Depends on:` is `none`) have no data dependency on each other and could build concurrently, cutting wall-clock on the front of a build.

A prior attempt to add this (unmerged branch `wf9/420-build-product-parallel`, `ca79f92`) **failed a real end-to-end run**: two parallel foundation milestones were marked DONE while their output files (`greeting.sh`, `farewell.sh`) were never written to disk, whereas a serial milestone in the same run wrote correctly.

### Why it failed (root-caused 2026-09-22, verified against `main`)

The attempt's *dispatch* was correct — `PickReadyMilestones` derived the independent set from the plan's `Depends on:` field and dispatched up to two via `parallel MilestoneWave` (`fan_in_policy: all`) to `build_milestone` **subgraph** slots (`MilestoneSlotA`/`MilestoneSlotB`), falling through to the serial loop when fewer than two exist. That part worked and is worth keeping.

The failure is **not** a lost-write plumbing bug — a subgraph-branch agent writes to the *same* shared repo tree as a serial milestone (product writes root at the shared `WorkingDir`, inherited identically by the child engine; confirmed in the #420 investigation, and unrelated to the #657 audit-identity fix). The failure is two coupled gaps:

1. **No per-branch output gate.** The `build_milestone` subgraph's only success signal is `verify.sh`'s exit code, which returns **green / NOT-YET-VERIFIABLE** on an empty fresh scaffold, and nothing per-branch checks the milestone's declared output files exist. `CheckMilestoneOutputs` — the gate that *does* check, and that flagged the missing files — is **serial-only** (`build_product.dip`, the `PickNextMilestone → CheckMilestoneOutputs` path). A branch reaches `MarkReady`/DONE without ever writing its file.

2. **A shared mutable tree + git index across concurrent branches is unsafe.** All branch child engines execute against the same working tree and index (`parallel.go` launches concurrent goroutines; the child registry reuses the parent's environment and working dir). Even with disjoint product files: each branch's `verify.sh` runs against a tree containing siblings' half-written files (false green/red), and concurrent `git add`/`commit` race the index. The attempt worked around this by **forbidding branch commits** and deferring everything to one serial `MergeWave` `git add -A` — which left all branch work uncommitted and unverified until the join, with no per-branch gate. That workaround is *how* a branch reached DONE empty.

The merged engine half (`ctx.branch_id`, which namespaces per-branch on-disk counters) is necessary but not sufficient.

## Goals / non-goals

**Goals**
- Build independent foundation milestones concurrently, with the same correctness guarantees as the serial path: a milestone is DONE only when its declared files exist, its tests are green, and its work is committed.
- Reuse what already works: `PickReadyMilestones` dispatch, the `build_milestone` per-milestone loop, and the fixed-slot fan-out with fall-through to serial.
- No new dippin-lang features required (confirm during design).

**Non-goals**
- The mid-build wave (parallelizing milestones that depend on earlier ones). This design covers the **foundation** wave (`Depends on: none`) only, matching the attempt's scope. Mid-build waves are deferred.
- Dynamic-width fan-out (a wave of arbitrary N). dippin's `parallel` has static branch targets; this design keeps the attempt's **fixed maximum width** (e.g. 2 slots) with no-op slots when fewer milestones are ready.

## What already works and is reused

- **`PickReadyMilestones`** (`ca79f92`): derives the independent set from the plan `Depends on:` field at authoring time, emits fixed slot assignments, falls through to serial when `< 2` ready. Keep as-is.
- **The `build_milestone` subgraph**: a full `implement → green-gate → verify → fix` loop per milestone, branch-namespacing its breakers by `ctx.branch_id`. Keep, plus the additions below.
- **`build_product_with_superspec`'s worktree + merge template** (`lib/worktrees.sh`): `setup_stream_worktrees PHASE STREAM...` creates one `git worktree add .ai/worktrees/<stream> -b build/<stream> HEAD` per stream (requiring a committed scaffold); the parallel stream nodes run with `working_dir: .ai/worktrees/<stream>`; a serial `merge_streams` folds each `build/<stream>` branch back into HEAD and, on conflict, aborts and routes to a `MergeConflict` gate (never `accept`). **This is the proven pattern this design applies to the milestone wave.** Superspec parallelizes *static* streams; #420 needs the same isolation for *plan-derived* slots.
- **#657's subgraph run-identity inheritance** (`WithInheritedRunIdentity`): the precedent seam for propagating parent context into a subgraph child engine — the same shape the working-dir propagation below needs.

## Design

Three additions, all mirroring the superspec template, applied to the `MilestoneSlotA/B` fan-out:

### 1. Per-slot git worktrees (isolation)
A serial `SetupWave` tool node runs **before** `parallel MilestoneWave`, after `PickReadyMilestones` has chosen the ready set. It creates one worktree + branch per active slot (`git worktree add .ai/worktrees/milestone-slot-a -b build/milestone-slot-a HEAD`), reusing `setup_stream_worktrees`' hardening verbatim (committed-scaffold precondition, never-delete-unmerged-branch, stale-worktree cleanup, `milestone-start-sha`). Requires the scaffold committed — `build_product` already commits per milestone via `CommitIfDirty`, so HEAD is a valid fork point.

Each slot subgraph then runs **against its worktree**, so a slot's `verify.sh` sees only its own tree and its `git add`/`commit` touch only its own branch/index — eliminating the shared-tree/index race.

### 2. Propagating the worktree into the slot subgraph (the one engine change)
Superspec's streams are **direct agent nodes** carrying `working_dir: .ai/worktrees/<stream>`. A #420 slot is a **subgraph** (a multi-node milestone loop), so the worktree path must reach *every* node inside the child graph. Options:

- **(A, recommended) Propagate a per-branch working dir into the subgraph child engine.** Extend the `#657` inheritance seam: the parallel handler already sets `ctx.branch_id` per branch; add a per-branch `working_dir` override (the slot's worktree) that the subgraph handler threads into the child engine so its codergen/tool nodes resolve their working dir to the worktree. This is the minimal, general engine change and matches the `WithInheritedRunIdentity` precedent (a `WithInheritedWorkingDir`-shaped option seeded from the branch's declared worktree).
- **(B) Bind the worktree as a subgraph input** (`#556`) and reference `${inputs.worktree}` in each child node's `working_dir`. **Not viable as-is:** `ToolHandler.applyWorkingDir` (`tool.go:385`) reads `working_dir` *raw* and rejects any `$` as a shell metacharacter, so `${inputs.worktree}` is refused before expansion. B would require changing `applyWorkingDir` (and the codergen working-dir path) to expand-then-validate — a broader change than A, spread across two handlers, that also widens what a `working_dir` may contain.
- **(C) Flatten each slot into direct nodes with `working_dir`** (superspec-identical, no subgraph). Sidesteps the engine entirely but duplicates the milestone node sequence per slot — verbose and drifts from the single `build_milestone` definition.

Recommendation: **(A)** — a per-branch working-dir propagated into the subgraph child engine (the `#657` `WithInheritedRunIdentity` precedent applied to working dir). It is the smallest change that keeps a single `build_milestone` definition and applies the worktree uniformly to every node in the slot. B is disfavored (the `$`-rejection above makes it a two-handler change, not a zero-cost one); C only if we decide against any subgraph slot.

### 3. Per-slot output-existence gate (fail closed)
Inside each slot subgraph (or as the last branch node before `MarkReady`), add a **branch-scoped** `CheckMilestoneOutputs` that fails the slot **closed** when the milestone's declared files are absent from its worktree. This closes the `verify.sh`-green-on-empty-scaffold hole so a slot cannot reach DONE empty. A failed slot makes the `fan_in_policy: all` outcome fail and routes to the existing `EscalateMilestone` gate — no new routing.

### 4. Serial merge-back with a conflict gate
After `fan_in MilestoneJoin`, a serial `MergeWave` tool node merges each active slot's `build/milestone-slot-*` branch into HEAD using `merge_streams`' logic (merge each branch; on conflict abort, print conflicting paths, tear nothing down, return 1). A `MergeConflict` human/auto gate handles the conflict (retry re-enters skipping already-merged branches; `abandon → AbortRun`). No-op slots (fewer ready milestones than slots) produce an empty branch with no commits, which the merge skips.

## Node/edge shape (build_product)

```
PickReadyMilestones --(≥2 ready)--> SetupWave --> MilestoneWave(parallel: SlotA, SlotB)
PickReadyMilestones --(<2 ready)--> PickNextMilestone            # unchanged serial loop
MilestoneWave --> MilestoneJoin(fan_in, policy: all) --> MergeWave
MergeWave --(success)--> <back into the milestone loop / review phase>
MergeWave --(conflict)--> MergeConflict(gate)
MilestoneWave --(branch fail)--> EscalateMilestone               # via fan_in fail, unchanged
MergeConflict --(retry)--> MergeWave    MergeConflict --(abandon)--> AbortRun
```

Each `SlotX` is a `subgraph ref: build_milestone` with a per-branch `working_dir` (the worktree) and its own `branch_id`-namespaced breakers, ending in the per-slot output gate.

## Risks & costs
- **Worktree setup/teardown, disk, and merge conflicts** on milestones that touch overlapping files (shared `go.mod`, a common package). The `MergeConflict` gate is the escape hatch, but frequent conflicts erode the wall-clock win — the foundation wave (disjoint-by-construction milestones) is the sweet spot; mid-build waves (deferred) would conflict more.
- **The `SetupPhaseNWorktrees` CI flake** noted in `gotchas.md` (`git merge-base --is-ancestor` under `-race` load) rides along with the worktree lib; budget for it.
- **Engine change (A)** touches the subgraph→child-engine seam (same area as #657) — a security/correctness-adjacent boundary; freeze-and-prove per the security-PR process is overkill here (no jail/tool boundary), but invariant tests on the working-dir propagation are required.
- **False wall-clock win** if the foundation wave is usually 0–1 milestones wide on real specs — measure the ready-set width distribution before committing.

## Phasing & test plan
1. **Engine (A)**: per-branch `working_dir` propagation into the subgraph child engine, with a deterministic no-LLM test (a subgraph slot's tool node writes to its declared worktree, asserted on disk) — the same test shape as `TestSubgraph_InheritsParentRunIdentity` (#657). (The `working_dir`-expansion spike that would have enabled B is already answered: `applyWorkingDir` rejects `$`, so B is not the cheap path.)
2. **Authoring**: `SetupWave` + per-slot output gate + `MergeWave` + `MergeConflict` gate, with fixture suites (`SetupWave_test.sh`, `MergeWave_test.sh` covering: 2 disjoint slots merge clean; a slot with no files fails closed; a merge conflict routes to the gate; a no-op slot is skipped) run under `sh` + `dash`.
3. **Routing sims** on the real graph (`pipeline/build_product_*_test.go`): `PickReadyMilestones ≥2 → SetupWave → wave → join → MergeWave`; branch-fail → `EscalateMilestone`; merge-conflict → `MergeConflict`; `<2` → serial fall-through.
4. **`dippin doctor` A + `simulate -all-paths`** clean; **a real end-to-end run** on a spec with two disjoint foundation milestones proving both files land committed (the exact scenario `ca79f92` failed).

## Open decisions (for the maintainer)
1. **Engine change (A) vs the alternatives** — A (per-branch working-dir into the subgraph child) is recommended; B was disfavored once the `working_dir` `$`-rejection was confirmed. Sign-off on A is the main decision.
2. **Is the foundation wave worth it on real specs?** Measure the ready-set width distribution; if usually ≤1, the feature is inert and the complexity isn't justified.
3. **Fixed slot width** — 2 (matching the attempt) or 3? Wider = more parallelism, more worktrees/merges/conflicts.
4. **Do NOT merge `ca79f92` as-is** — it validates a shared-tree design that cannot be reliable. This design supersedes it.
