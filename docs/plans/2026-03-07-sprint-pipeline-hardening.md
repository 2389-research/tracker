# Sprint Pipeline Hardening Implementation Plan

> **For Claude:** REQUIRED SUB-SKILL: Use superpowers:executing-plans to implement this plan task-by-task.

**Goal:** Prevent sprint pipelines from selecting nonexistent sprint docs, force honest validation for test-required sprints, and ensure all stage artifacts stay inside `.tracker/runs/`.

**Architecture:** Keep the generic artifact-routing fix in `tracker`, but patch the RemixOS sprint pipeline directly for sprint bootstrapping, sprint-file-aware selection, and stricter validation. The engine remains generic; the pipeline owns repo-specific sprint policy.

**Tech Stack:** Go, existing `pipeline` handlers/tests in `tracker`, shell-based sprint pipeline in `sprint_exec.dot`, markdown sprint-plan source files under `.ai/` and `docs/plans/`.

---

### Task 1: Lock In The Generic Artifact-Dir Fix In `tracker`

**Files:**
- Modify: `pipeline/handlers/parallel.go`
- Modify: `pipeline/handlers/parallel_test.go`
- Test: `pipeline/handlers/parallel_test.go`

**Step 1: Write the failing test**

```go
func TestParallelHandlerPreservesInternalArtifactDir(t *testing.T) {
	g := buildTestGraph([]string{"branch_a"}, "stub_internal")
	registry := pipeline.NewHandlerRegistry()
	registry.Register(&stubHandler{
		name: "stub_internal",
		execFunc: func(ctx context.Context, node *pipeline.Node, pctx *pipeline.PipelineContext) (pipeline.Outcome, error) {
			dir, ok := pctx.GetInternal(pipeline.InternalKeyArtifactDir)
			if !ok || dir == "" {
				return pipeline.Outcome{Status: pipeline.OutcomeFail}, fmt.Errorf("missing internal artifact dir")
			}
			return pipeline.Outcome{Status: pipeline.OutcomeSuccess}, nil
		},
	})

	pctx := pipeline.NewPipelineContext()
	pctx.SetInternal(pipeline.InternalKeyArtifactDir, "/tmp/artifacts/run-123")

	outcome, err := NewParallelHandler(g, registry).Execute(context.Background(), g.Nodes["parallel_node"], pctx)
	if err != nil {
		t.Fatal(err)
	}
	if outcome.Status != pipeline.OutcomeSuccess {
		t.Fatalf("expected success, got %q", outcome.Status)
	}
}
```

**Step 2: Run test to verify it fails**

Run: `go test ./pipeline/handlers -run TestParallelHandlerPreservesInternalArtifactDir`

Expected: FAIL because branch contexts only clone user-visible values.

**Step 3: Write minimal implementation**

Copy `pipeline.InternalKeyArtifactDir` from the parent context into each parallel branch context before branch execution.

**Step 4: Run test to verify it passes**

Run: `go test ./pipeline/handlers -run TestParallelHandlerPreservesInternalArtifactDir`

Expected: PASS

**Step 5: Commit**

```bash
git add pipeline/handlers/parallel.go pipeline/handlers/parallel_test.go
git commit -m "fix: preserve artifact dir across parallel branches"
```

### Task 2: Add Missing RemixOS Sprint Docs `001`-`020`

**Files:**
- Create: `/Users/harper/Public/src/2389/justin-remix/remix-3-tracker/.ai/sprints/SPRINT-001.md`
- Create: `/Users/harper/Public/src/2389/justin-remix/remix-3-tracker/.ai/sprints/SPRINT-002.md`
- Create: `/Users/harper/Public/src/2389/justin-remix/remix-3-tracker/.ai/sprints/SPRINT-003.md`
- Create: `/Users/harper/Public/src/2389/justin-remix/remix-3-tracker/.ai/sprints/SPRINT-004.md`
- Create: `/Users/harper/Public/src/2389/justin-remix/remix-3-tracker/.ai/sprints/SPRINT-005.md`
- Create: `/Users/harper/Public/src/2389/justin-remix/remix-3-tracker/.ai/sprints/SPRINT-006.md`
- Create: `/Users/harper/Public/src/2389/justin-remix/remix-3-tracker/.ai/sprints/SPRINT-007.md`
- Create: `/Users/harper/Public/src/2389/justin-remix/remix-3-tracker/.ai/sprints/SPRINT-008.md`
- Create: `/Users/harper/Public/src/2389/justin-remix/remix-3-tracker/.ai/sprints/SPRINT-009.md`
- Create: `/Users/harper/Public/src/2389/justin-remix/remix-3-tracker/.ai/sprints/SPRINT-010.md`
- Create: `/Users/harper/Public/src/2389/justin-remix/remix-3-tracker/.ai/sprints/SPRINT-011.md`
- Create: `/Users/harper/Public/src/2389/justin-remix/remix-3-tracker/.ai/sprints/SPRINT-012.md`
- Create: `/Users/harper/Public/src/2389/justin-remix/remix-3-tracker/.ai/sprints/SPRINT-013.md`
- Create: `/Users/harper/Public/src/2389/justin-remix/remix-3-tracker/.ai/sprints/SPRINT-014.md`
- Create: `/Users/harper/Public/src/2389/justin-remix/remix-3-tracker/.ai/sprints/SPRINT-015.md`
- Create: `/Users/harper/Public/src/2389/justin-remix/remix-3-tracker/.ai/sprints/SPRINT-016.md`
- Create: `/Users/harper/Public/src/2389/justin-remix/remix-3-tracker/.ai/sprints/SPRINT-017.md`
- Create: `/Users/harper/Public/src/2389/justin-remix/remix-3-tracker/.ai/sprints/SPRINT-018.md`
- Create: `/Users/harper/Public/src/2389/justin-remix/remix-3-tracker/.ai/sprints/SPRINT-019.md`
- Create: `/Users/harper/Public/src/2389/justin-remix/remix-3-tracker/.ai/sprints/SPRINT-020.md`
- Modify: `/Users/harper/Public/src/2389/justin-remix/remix-3-tracker/docs/plans/2026-03-06-remixos-spec-sprint-buildout.md` (only if a source-of-truth note is needed; otherwise leave untouched)

**Step 1: Write the failing check**

Run: `test -f /Users/harper/Public/src/2389/justin-remix/remix-3-tracker/.ai/sprints/SPRINT-001.md`

Expected: FAIL because the file does not exist.

**Step 2: Create the sprint docs**

Use the sections already defined in `/Users/harper/Public/src/2389/justin-remix/remix-3-tracker/docs/plans/2026-03-06-remixos-spec-sprint-buildout.md`.

Each sprint doc should contain:

- sprint title
- goal
- context
- Definition of Done checklist
- files to create/modify

Do not invent new scope beyond the buildout plan.

**Step 3: Run check to verify they exist**

Run: `ls /Users/harper/Public/src/2389/justin-remix/remix-3-tracker/.ai/sprints/SPRINT-0{01..20}.md`

Expected: all files listed

**Step 4: Commit**

```bash
git -C /Users/harper/Public/src/2389/justin-remix/remix-3-tracker add .ai/sprints/SPRINT-0*.md
git -C /Users/harper/Public/src/2389/justin-remix/remix-3-tracker commit -m "docs: add missing foundation sprint docs"
```

### Task 3: Harden RemixOS Sprint Selection And Read Step

**Files:**
- Modify: `/Users/harper/Public/src/2389/justin-remix/remix-3-tracker/sprint_exec.dot`

**Step 1: Write the failing behavioral check**

Use the current repo state:

Run: `awk -F '\t' 'NR>1 && $3!~/^completed$/ && $3!~/^skipped$/{print $1; exit}' /Users/harper/Public/src/2389/justin-remix/remix-3-tracker/.ai/ledger.tsv`

Expected: `001`

This is insufficient because the current `SetCurrentSprint` does not verify that `/Users/harper/Public/src/2389/justin-remix/remix-3-tracker/.ai/sprints/SPRINT-001.md` exists.

**Step 2: Implement minimal pipeline change**

Update `sprint_exec.dot` so:

- sprint selection chooses the first non-completed sprint with a matching file in `.ai/sprints/`
- if none exist, the step fails with a clear message
- `ReadSprint` is instructed to read exactly `.ai/sprints/SPRINT-<current>.md`, not “some sprint under `.ai/sprints/`”

Prefer shell-based deterministic selection in the `SetCurrentSprint` tool node instead of relying on the LLM to infer file availability.

**Step 3: Run verification**

Run: `cat /Users/harper/Public/src/2389/justin-remix/remix-3-tracker/sprint_exec.dot | sed -n '20,50p'`

Expected: selection logic references both ledger status and `.ai/sprints/SPRINT-${target}.md` existence.

**Step 4: Commit**

```bash
git -C /Users/harper/Public/src/2389/justin-remix/remix-3-tracker add sprint_exec.dot
git -C /Users/harper/Public/src/2389/justin-remix/remix-3-tracker commit -m "fix: select only sprint docs that exist"
```

### Task 4: Tighten RemixOS Validation For Test-Requiring Sprints

**Files:**
- Modify: `/Users/harper/Public/src/2389/justin-remix/remix-3-tracker/sprint_exec.dot`

**Step 1: Write the failing behavioral check**

Current evidence:

Run: `cat /Users/harper/Public/src/2389/justin-remix/remix-3-tracker/.tracker/runs/97ca828648de/ValidateBuild/status.json`

Expected: shows success even though `go test ./...` returned `[no test files]`

**Step 2: Implement minimal validation change**

In the `ValidateBuild` tool step:

- read `.ai/current_sprint_id.txt`
- inspect the matching sprint doc
- if the sprint doc contains test expectations such as `**Test:` or `*_test.go`, then fail validation when `go test ./...` output contains `[no test files]`

Keep non-test-requiring sprints simple; do not over-engineer a full DoD parser.

**Step 3: Run verification**

Run: `grep -n \"no test files\\|current_sprint_id\\|SPRINT-\" /Users/harper/Public/src/2389/justin-remix/remix-3-tracker/sprint_exec.dot`

Expected: validation logic now checks sprint context and rejects empty test suites for test-required sprints.

**Step 4: Commit**

```bash
git -C /Users/harper/Public/src/2389/justin-remix/remix-3-tracker add sprint_exec.dot
git -C /Users/harper/Public/src/2389/justin-remix/remix-3-tracker commit -m "fix: fail validation when test-required sprints have no tests"
```

### Task 5: End-to-End Verification Against The RemixOS Repo

**Files:**
- Modify: `/Users/harper/Public/src/2389/justin-remix/remix-3-tracker/.ai/current_sprint_id.txt` (only as a result of running the pipeline)
- Modify: `/Users/harper/Public/src/2389/justin-remix/remix-3-tracker/.tracker/runs/...` (generated evidence)

**Step 1: Verify sprint docs and selection**

Run:

```bash
ls /Users/harper/Public/src/2389/justin-remix/remix-3-tracker/.ai/sprints/SPRINT-0{01..20}.md
```

Expected: all present

**Step 2: Run the RemixOS sprint pipeline**

Run:

```bash
cd /Users/harper/Public/src/2389/justin-remix/remix-3-tracker && tracker sprint_exec.dot
```

Expected:

- `SetCurrentSprint` selects a sprint with a real sprint file
- `ReadSprint` reads that exact sprint
- any artifacts land only under `.tracker/runs/<run-id>/...`

**Step 3: Verify no new root artifact leaks**

Run:

```bash
find /Users/harper/Public/src/2389/justin-remix/remix-3-tracker -maxdepth 1 -type d \\( -name 'Review*' -o -name 'Critique*' \\)
```

Expected: no newly created root-level review/critique directories from the fresh run

**Step 4: Verify run evidence**

Run:

```bash
find /Users/harper/Public/src/2389/justin-remix/remix-3-tracker/.tracker/runs -maxdepth 2 -type f | tail -50
```

Expected: latest run artifacts present only under `.tracker/runs/...`

**Step 5: Commit**

```bash
git -C /Users/harper/Public/src/2389/justin-remix/remix-3-tracker status --short
git status --short
```

There may or may not be a commit here depending on whether the pipeline generated content you want to keep. Do not auto-commit generated run artifacts unless explicitly desired.
