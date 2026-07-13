# Restore Example .dip Grades Under dippin v0.48 (A across the board) — Design

**Date:** 2026-07-13
**Status:** design approved
**Scope:** `examples/*.dip` (18 below-A files), `.github/workflows/ci.yml` (dippin CLI pin), CHANGELOG, a grade regression guard. Prerequisite for cutting **v0.44.0**.

## Problem

The dippin go.mod dep was bumped v0.43.0 → v0.48.0 (merged, build+suite green). The modern dippin's stricter lint drops 18 of 29 example `.dip` files below grade A on `dippin doctor` — driven by **lint *warning* count** (errors are 0 everywhere, so `dippin lint` / CI still pass). CI currently pins the dippin *CLI* at the old `v0.22.0`, which has none of these rules, so CI grades the examples A and stays green — masking the drop. Before cutting a release we want the examples A under modern dippin and the CI CLI bumped to enforce it.

The grade drop is **not** functional — every fix here is behavior-preserving:
- **DIP153 (295 total):** an edges-block edge redundantly repeats an inline `parallel`/`fan_in` fork. The inline list is authoritative; the duplicate conveys nothing. Removing it changes no routing.
- **DIP151 (115):** an edge `weight:` attribute — soft-deprecated, **unused by the routing cascade**. Removing it changes no routing.
- **DIP144 (8):** an agent node with no failure route. Add one (a `when ctx.outcome = fail` edge, `fallback_target:`, bounded `retry_target`, or a workflow-level `on_failure:`).
- **DIP134 (8):** `defaults` set `max_retries` but no `max_restarts` while the workflow has `restart: true` edges — add `max_restarts` (or the intended budget).
- Stragglers: DIP109/DIP126/DIP149/DIP135 (≤4 each), per-file.

## Key constraint: `dippin fmt` strips comments

`dippin fmt -write` auto-removes DIP153 edges — but it also **deletes comments and reorders attributes** (verified: on `ask_and_execute` it removed the `# ─── Phase N ───` headers and design comments, 51 diff lines). So it is **only** acceptable on files whose comments are disposable. Files with load-bearing design commentary (build_product's ~719 comment lines) must be fixed **surgically** — deleting only the named redundant edges by hand, preserving every comment.

## The three groups (18 files)

### Group 1 — comment-light demos: `dippin fmt -write` (6 files)
`consensus_task`, `consensus_task_parity`, `megaplan`, `megaplan_quality`, `sprint_exec`, `semport_thematic` — 0–5 comment lines, DIP153-only. `dippin fmt -write <file>` → grade A. Verify each parses and grades A afterward.

### Group 2 — comment-heavy demos: `fmt -write` + residual cleanup (7 files)
`dotpowers`, `dotpowers-auto`, `dotpowers-simple`, `dotpowers-simple-auto`, `kitchen-sink`, `scenario-testing`, `test-kitchen`. Steps per file:
1. `dippin fmt -write` (clears DIP153; comments are demo-explanatory, acceptable to reformat).
2. Remove every `weight:` attribute (DIP151) — unused, safe.
3. Add a failure route for the one unrouted agent node (DIP144) — prefer a workflow-level `on_failure:` to the workflow's terminal/exit node (17 of 29 examples already use `on_failure:`), matching the file's existing pattern.
4. Add `max_restarts:` to `defaults` (DIP134) — set it to the existing `max_retries` value (or the sensible loop budget) so the restart budget is explicit.
5. Verify grade A + still parses.

### Group 3 — commented core: surgical, comment-safe, NO fmt (5 files)
`build_product` (3 DIP153), `build_product_with_superspec` (11), `ask_and_execute` (3 DIP153 + 1 DIP134), `deep_review` (3), `parallel-ralph-dev` (4 DIP153 + DIP109 + DIP149). Per file: run `dippin lint <file>` to get the exact redundant edge names, delete **only** those edges-block lines (and only those), preserve all comments; add `ask_and_execute`'s `max_restarts` (DIP134); resolve `parallel-ralph-dev`'s DIP109/DIP149 per their `dippin explain`. Verify grade A + `git diff` shows only the removed edges (+ the tiny residual), no comment loss.

## CI enforcement

Bump `.github/workflows/ci.yml`'s `go install github.com/2389-research/dippin-lang/cmd/dippin@v0.22.0` → `@v0.48.0`, so `make lint` and `make doctor` run under the modern dippin that now grades the examples A. (This is the CLI tool pin, distinct from the go.mod library dep already at v0.48.0.)

## Testing / verification

- **Every example grades A:** `for f in examples/*.dip; do echo "$f: $(dippin doctor "$f" | grep -oE 'Grade: [A-F]')"; done` → all `Grade: A`. This is the acceptance signal.
- **Behavior preserved:** `dippin doctor` still reports "All paths terminate" and full node reachability for each edited file (the removals are redundant/unused, so reachability/termination are unchanged).
- **Repo green:** `go build ./... && go test ./... -short` pass (the embedded built-ins `build_product`, `build_product_with_superspec`, `ask_and_execute` are edited — the loaded-built-in tests must still pass).
- **Grade guard:** extend the existing CI `make doctor` (now on dippin v0.48) so a future regression re-reds it; optionally a Go test asserting the three core pipelines grade A.
- `dippin doctor examples/build_product.dip examples/build_product_with_superspec.dip examples/ask_and_execute.dip` A — the CLAUDE.md core-pipeline bar.

## Out of scope

- Reformatting the core files (comments preserved — surgical only).
- New dippin lint rules beyond restoring grades (no threshold/rule authoring).
- The `dipx.PackOptions.NoInline` capability (dippin#73, shipped in v0.48) that could reopen **#467** option B — noted for a separate follow-up, not part of this batch.

## Commit / PR structure

One PR (`fix/examples-dippin-v048-grades` → `main`), grouped commits (fmt demos; comment-heavy demos; each core file or a small cluster; CI CLI bump; CHANGELOG). Then the **v0.44.0** release.
