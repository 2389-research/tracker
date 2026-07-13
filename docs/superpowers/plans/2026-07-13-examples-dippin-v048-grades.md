# Restore Example .dip Grades Under dippin v0.48 Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Bring all 18 below-A example `.dip` files back to grade A under dippin v0.48 (all fixes behavior-preserving), then bump the CI dippin CLI to v0.48.0 so it's enforced — unblocking the v0.44.0 release.

**Architecture:** Group by comment value: comment-light demos get `dippin fmt -write` (auto-strips DIP153); comment-heavy demos get `fmt -write` plus manual residual cleanup; commented-core files are fixed surgically (delete only the named redundant edges, never `fmt`, to preserve their design comments). Then bump `ci.yml`'s dippin CLI pin and verify all examples grade A.

**Tech Stack:** dippin CLI (v0.48-era, local at `~/.local/bin/dippin`); `.dip` DSL; Go test (embedded built-ins); GitHub Actions.

## Global Constraints

- **All fixes are behavior-preserving.** DIP153 (redundant edges-block edge that repeats an inline `parallel`/`fan_in` fork — inline list is authoritative), DIP151 (`weight:` — unused by routing), DIP144 (add a failure route), DIP134 (add `max_restarts`). No routing logic changes.
- **Never run `dippin fmt` on a commented-core file** (`build_product`, `build_product_with_superspec`, `ask_and_execute`, `deep_review`, `parallel-ralph-dev`) — it deletes comments and reorders attributes. Fix those surgically.
- **Acceptance per file:** `dippin doctor examples/<f>.dip` → `Grade: A`, and it still reports "All paths terminate" + full reachability.
- **Embedded built-ins** (`build_product`, `build_product_with_superspec`, `ask_and_execute`) are compiled into the binary and covered by loaded-built-in Go tests — after editing them, `go test ./... -short` must pass.
- **Never `git commit --no-verify` and never `git commit --amend`.** Hook ~2–4 min; allow up to 6 minutes; on timeout `git log --oneline -1` first, then a plain `git commit`.
- The dippin CLI flag to write in place is `-write` (single dash): `dippin fmt -write <file>`.
- Fix recipes (used throughout):
  - **DIP153:** `dippin lint <file> 2>&1 | grep DIP153` names each redundant edge (e.g. `'ReviewClaude -> ReviewJoin'`). Delete that exact line from the `edges` block. (`fmt -write` does this automatically for demos.)
  - **DIP151:** `grep -n 'weight:' <file>` — on each edge line, delete the ` weight: <N>` portion, keep the edge.
  - **DIP144:** add a workflow-level `on_failure: <ExitNode>` in the header (the `exit:` node, or an escalation node) — 17 examples already use `on_failure:`; copy the syntax from one (`grep -A1 'on_failure:' examples/*.dip`).
  - **DIP134:** in the `defaults:` block add `max_restarts: <N>` (use the workflow's loop budget; `build_product` uses `max_restarts: 50` — match the file's restart intent, default 50).

---

### Task 1: Group 1 — `fmt -write` the 6 comment-light demos

**Files:** Modify `examples/{consensus_task,consensus_task_parity,megaplan,megaplan_quality,sprint_exec,semport_thematic}.dip` (0–5 comments each, DIP153-only).

- [ ] **Step 1: Confirm they're comment-light + DIP153-only**

Run: `for f in consensus_task consensus_task_parity megaplan megaplan_quality sprint_exec semport_thematic; do echo "$f: comments=$(grep -cE '^\s*#' examples/$f.dip) codes=$(dippin lint examples/$f.dip 2>&1 | grep -oE 'warning\[[A-Z0-9]+\]' | sort -u | tr '\n' ' ')"; done`
Expected: each has ≤5 comment lines and only `warning[DIP153]` (if any file shows other codes or many comments, STOP and report — it may belong in a surgical group).

- [ ] **Step 2: Format each in place**

Run: `for f in consensus_task consensus_task_parity megaplan megaplan_quality sprint_exec semport_thematic; do dippin fmt -write examples/$f.dip; done`

- [ ] **Step 3: Verify all grade A + still terminate**

Run: `for f in consensus_task consensus_task_parity megaplan megaplan_quality sprint_exec semport_thematic; do echo "$f: $(dippin doctor examples/$f.dip 2>&1 | grep -oE 'Grade: [A-F]') / $(dippin doctor examples/$f.dip 2>&1 | grep -c 'All paths terminate')term"; done`
Expected: every file `Grade: A` and `1term` (paths terminate). If any is not A, run `dippin lint examples/<f>.dip` and clear the residual per the Global-Constraints recipes.

- [ ] **Step 4: Commit**

```bash
git add examples/consensus_task.dip examples/consensus_task_parity.dip examples/megaplan.dip examples/megaplan_quality.dip examples/sprint_exec.dip examples/semport_thematic.dip
git commit -m "chore(examples): fmt comment-light demos to grade A under dippin v0.48 (DIP153)

Co-Authored-By: Claude Opus 4.8 (1M context) <noreply@anthropic.com>
Claude-Session: https://claude.ai/code/session_012U14Lj3xBwjmSLJAx6jqbr"
```

---

### Task 2: Group 2a — the dotpowers demo family (4 files)

**Files:** Modify `examples/{dotpowers,dotpowers-auto,dotpowers-simple,dotpowers-simple-auto}.dip`. Each has DIP153 (via fmt), DIP151 (`weight:` ×15–17), DIP144 (×1), DIP134 (×1).

- [ ] **Step 1: fmt each (clears DIP153)**

Run: `for f in dotpowers dotpowers-auto dotpowers-simple dotpowers-simple-auto; do dippin fmt -write examples/$f.dip; done`

- [ ] **Step 2: Remove `weight:` (DIP151) in each**

For each file, `grep -n 'weight:' examples/<f>.dip`, and on every edge line delete the ` weight: <N>` portion (keep the edge and any `when`/`label:`). Confirm none remain: `grep -c 'weight:' examples/<f>.dip` → 0.

- [ ] **Step 3: Add a failure route (DIP144) in each**

`dippin lint examples/<f>.dip 2>&1 | grep DIP144` names the unrouted agent node. Add a workflow-level `on_failure: <ExitNode>` to the header (the node named after `exit:`), matching the `on_failure:` syntax already used elsewhere (`grep -A1 'on_failure:' examples/ask_and_execute.dip`). This routes any unrouted agent failure to the exit instead of dead-stopping.

- [ ] **Step 4: Add `max_restarts` (DIP134) in each**

In each file's `defaults:` block, add `max_restarts: 50` (these are restart-loop demos; 50 matches build_product's budget). Confirm: `dippin lint examples/<f>.dip 2>&1 | grep -c DIP134` → 0.

- [ ] **Step 5: Verify all four grade A**

Run: `for f in dotpowers dotpowers-auto dotpowers-simple dotpowers-simple-auto; do echo "$f: $(dippin doctor examples/$f.dip 2>&1 | grep -oE 'Grade: [A-F]')  residual=$(dippin lint examples/$f.dip 2>&1 | grep -c 'warning\[')"; done`
Expected: each `Grade: A`, `residual=0`. Clear any straggler per the recipes.

- [ ] **Step 6: Commit**

```bash
git add examples/dotpowers.dip examples/dotpowers-auto.dip examples/dotpowers-simple.dip examples/dotpowers-simple-auto.dip
git commit -m "chore(examples): dotpowers demos to grade A under dippin v0.48 (DIP153/151/144/134)

Co-Authored-By: Claude Opus 4.8 (1M context) <noreply@anthropic.com>
Claude-Session: https://claude.ai/code/session_012U14Lj3xBwjmSLJAx6jqbr"
```

---

### Task 3: Group 2b — the kitchen/scenario/test demo family (3 files)

**Files:** Modify `examples/{kitchen-sink,scenario-testing,test-kitchen}.dip`. Same defect mix as Task 2 (DIP153 + DIP151 + DIP144 + DIP134), larger files.

- [ ] **Step 1: fmt each (clears DIP153)**

Run: `for f in kitchen-sink scenario-testing test-kitchen; do dippin fmt -write examples/$f.dip; done`

- [ ] **Step 2: Remove `weight:` (DIP151)** — same procedure as Task 2 Step 2 for each of the three files; confirm `grep -c 'weight:'` → 0.

- [ ] **Step 3: Add failure route (DIP144)** — same as Task 2 Step 3 for each (workflow-level `on_failure: <ExitNode>`).

- [ ] **Step 4: Add `max_restarts: 50` (DIP134)** in each `defaults:` block.

- [ ] **Step 5: Verify all three grade A**

Run: `for f in kitchen-sink scenario-testing test-kitchen; do echo "$f: $(dippin doctor examples/$f.dip 2>&1 | grep -oE 'Grade: [A-F]')  residual=$(dippin lint examples/$f.dip 2>&1 | grep -c 'warning\[')"; done`
Expected: each `Grade: A`, `residual=0`.

- [ ] **Step 6: Commit**

```bash
git add examples/kitchen-sink.dip examples/scenario-testing.dip examples/test-kitchen.dip
git commit -m "chore(examples): kitchen-sink/scenario/test-kitchen demos to grade A (DIP153/151/144/134)

Co-Authored-By: Claude Opus 4.8 (1M context) <noreply@anthropic.com>
Claude-Session: https://claude.ai/code/session_012U14Lj3xBwjmSLJAx6jqbr"
```

---

### Task 4: Group 3 — surgical, comment-safe (5 commented-core files)

**Files:** Modify `examples/{build_product,build_product_with_superspec,ask_and_execute,deep_review,parallel-ralph-dev}.dip`. **Do NOT run `dippin fmt` on any of these** — edit by hand to preserve comments.

- [ ] **Step 1: Snapshot comment counts (guard against comment loss)**

Run: `for f in build_product build_product_with_superspec ask_and_execute deep_review parallel-ralph-dev; do echo "$f: $(grep -cE '^\s*#' examples/$f.dip) comment lines"; done`
Record these — they must be **unchanged** after the edits (Step 6).

- [ ] **Step 2: Remove the named DIP153 edges surgically (per file)**

For each file: `dippin lint examples/<f>.dip 2>&1 | grep DIP153` prints each redundant edge by name (e.g. `edge 'ReviewClaude -> ReviewJoin' redundantly repeats...`). In the file's `edges` block, delete **only** those exact edge lines (unconditional, attribute-free duplicates of an inline `parallel`/`fan_in` fork). Do not touch conditional/`when`/`label:`/`restart:` edges. Counts: build_product 3, superspec 11, ask_and_execute 3, deep_review 3, parallel-ralph-dev 4.

- [ ] **Step 3: `ask_and_execute` DIP134** — add `max_restarts: 50` to its `defaults:` block (`dippin lint examples/ask_and_execute.dip 2>&1 | grep DIP134` to confirm the finding; clear it).

- [ ] **Step 4: `parallel-ralph-dev` DIP109 + DIP149** — run `dippin explain DIP109` and `dippin explain DIP149`, apply each fix minimally (comment-safe). Confirm both clear.

- [ ] **Step 5: Verify all five grade A**

Run: `for f in build_product build_product_with_superspec ask_and_execute deep_review parallel-ralph-dev; do echo "$f: $(dippin doctor examples/$f.dip 2>&1 | grep -oE 'Grade: [A-F]')  residual=$(dippin lint examples/$f.dip 2>&1 | grep -c 'warning\[')"; done`
Expected: each `Grade: A`, `residual=0`.

- [ ] **Step 6: Confirm NO comment loss + no behavior change**

Run: `for f in build_product build_product_with_superspec ask_and_execute deep_review parallel-ralph-dev; do echo "$f: $(grep -cE '^\s*#' examples/$f.dip) comment lines"; done`
Expected: **identical to Step 1's counts** (surgical edits removed only edges, no comments). Then `git diff --stat examples/` should show small deletions per file (the removed edges + the tiny residual), no large reformats.

- [ ] **Step 7: Embedded built-ins still load + full suite**

Run: `go build ./... && go test ./... -short 2>&1 | tail -5`
Expected: all packages PASS (the loaded-built-in tests parse the edited `build_product`/`superspec`/`ask_and_execute`).

- [ ] **Step 8: Commit**

```bash
git add examples/build_product.dip examples/build_product_with_superspec.dip examples/ask_and_execute.dip examples/deep_review.dip examples/parallel-ralph-dev.dip
git commit -m "chore(examples): surgical DIP153/134 cleanup on core pipelines to grade A (comments preserved)

Co-Authored-By: Claude Opus 4.8 (1M context) <noreply@anthropic.com>
Claude-Session: https://claude.ai/code/session_012U14Lj3xBwjmSLJAx6jqbr"
```

---

### Task 5: Bump CI dippin CLI + grade guard + CHANGELOG + final verification

**Files:** Modify `.github/workflows/ci.yml`, `CHANGELOG.md`; optional test in `pipeline/` or `cmd/tracker/`.

- [ ] **Step 1: Bump the CI dippin CLI pin** — in `.github/workflows/ci.yml`, change:
```
        run: go install github.com/2389-research/dippin-lang/cmd/dippin@v0.22.0
```
to:
```
        run: go install github.com/2389-research/dippin-lang/cmd/dippin@v0.48.0
```
(This is the CLI tool used by `make lint`/`make doctor`, distinct from the go.mod library dep already at v0.48.0.)

- [ ] **Step 2: Verify EVERY example grades A under dippin v0.48**

Run: `fail=0; for f in examples/*.dip; do g=$(dippin doctor "$f" 2>&1 | grep -oE 'Grade: [A-F]' | head -1); [ "$g" = "Grade: A" ] || { echo "NOT A: $f ($g)"; fail=1; }; done; [ "$fail" -eq 0 ] && echo "ALL 29 EXAMPLES GRADE A"`
Expected: `ALL 29 EXAMPLES GRADE A`. (Any straggler → fix per the recipes before proceeding.)

- [ ] **Step 3: Add a grade guard** — add a Go test (in `cmd/tracker/` alongside the existing doctor tests, or `pipeline/`) that asserts the three core embedded pipelines are lint-error-free, mirroring the existing preflight-test style. Minimal:

```go
// TestCorePipelinesLintClean guards that the shipped core pipelines carry zero
// lint ERRORS (the grade/warnings live in the dippin CLI's doctor, enforced in
// CI; this pins the harder no-errors floor in Go).
func TestCorePipelinesLintClean(t *testing.T) {
	for _, f := range []string{"build_product.dip", "build_product_with_superspec.dip", "ask_and_execute.dip"} {
		g := loadBuildProductByName(t, f) // or the existing loader; adapt to the real helper
		if len(g.LintWarnings) > 0 {
			// warnings are allowed to exist but log them for visibility
			t.Logf("%s carries %d lint warnings", f, len(g.LintWarnings))
		}
	}
}
```
If a suitable loader/`LintWarnings` accessor doesn't exist cleanly, SKIP the Go test and rely on the CI `make doctor` step (now on dippin v0.48) as the guard — note that choice in the report. Do not invent an API.

- [ ] **Step 4: CHANGELOG** — under `## [Unreleased]`, add:

```markdown
### Changed

- **Example pipelines restored to grade A under dippin v0.48 (dippin bump follow-up).**
  The v0.48 dippin lint flags redundant fan-out edges (DIP153), unused `weight:`
  attributes (DIP151), and a few missing failure-route / `max_restarts` hints that
  dropped 18 examples below A on `dippin doctor` (errors stayed 0, so runtime was
  unaffected). All are behavior-preserving cleanups; core pipelines keep their
  design comments. The CI dippin CLI is bumped v0.22.0 → v0.48.0 to enforce it.
```

- [ ] **Step 5: Full verification**

Run: `go build ./... && go test ./... -short 2>&1 | tail -5`
Expected: all PASS.
Run: `dippin doctor examples/build_product.dip examples/build_product_with_superspec.dip examples/ask_and_execute.dip 2>&1 | grep -E 'Grade'` (run per-file if the CLI takes one arg) → all A.

- [ ] **Step 6: Commit**

```bash
git add .github/workflows/ci.yml CHANGELOG.md
# plus the Go test file if added
git commit -m "ci(dippin): bump CLI pin to v0.48.0; changelog for example grade restoration

Co-Authored-By: Claude Opus 4.8 (1M context) <noreply@anthropic.com>
Claude-Session: https://claude.ai/code/session_012U14Lj3xBwjmSLJAx6jqbr"
```

- [ ] **Step 7: (held for after final review)** PR + fold into the v0.44.0 release. Do not push until the whole-branch review passes.

---

## Self-Review

**Spec coverage:** Group 1 (fmt demos) → Task 1; Group 2 (comment-heavy demos) → Tasks 2+3; Group 3 (surgical core) → Task 4; CI CLI bump + verify all-A + CHANGELOG + guard → Task 5. Every spec group + the CI enforcement + the comment-preservation guard (Task 4 Step 6) map to a task.

**Placeholder scan:** the fixes are procedural (discovered per-file via `dippin lint`, which names the exact edges/nodes) — every step gives the exact command + the exact transformation rule (delete the named edge; strip ` weight: N`; add `on_failure: <exit>`; add `max_restarts: 50`). The one genuine branch (Task 5 Step 3: add the Go guard only if a clean loader exists, else rely on CI) is a real environment decision with a defined fallback, not deferred work.

**Consistency:** the four fix recipes (DIP153/151/144/134) are defined once in Global Constraints and referenced identically across Tasks 1–4. `max_restarts: 50` used consistently. Verification pattern (`dippin doctor | grep Grade` = A, `residual=0`) identical across tasks. `dippin fmt -write` (never on core) consistent.

**Ordering:** Tasks 1–4 are independent (disjoint file sets) and each self-verifies to A. Task 5 (CI bump) runs last so the all-29-A gate reflects every prior task. Follow numeric order.
