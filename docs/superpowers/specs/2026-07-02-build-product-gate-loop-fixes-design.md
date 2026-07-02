# build_product Gate-Loop Fixes (issues #436–#443) — Design

**Date:** 2026-07-02
**Target release:** v0.42.0 (batch PR)
**Scope:** `examples/build_product.dip` (single file) + `pipeline/build_product_*_test.go` regression tests + `CHANGELOG.md` / `README.md`.

## Background

Issues #436–#443 came from a post-mortem of a wedged `build_product` run (run `400eebf3f3c7`,
milestone 8, on the `code-goblin` project). The milestone code was correct and committed
(`a636cc1`) but the run could not advance past its gates and looped with no in-band exit.

The pipeline has drifted from the version that produced that post-mortem, so each issue was
re-verified against `main` at v0.41.0 before this design. Two issues (#438, and #439 as
literally written) do **not** reproduce against current code; the remaining fixes are precise.

All gate logic lives as embedded heredoc shell scripts and agent prompts inside the single
file `examples/build_product.dip`. The `build_product_with_superspec.dip` variant does **not**
share these nodes (0 matches), so it is out of scope.

## Verification status at HEAD (v0.41.0)

| # | Sev | Status | Root cause at HEAD |
|---|-----|--------|--------------------|
| 436 | P0 | Real | `ci-probe.sh` runs `golangci-lint run` whole-repo while `verify.sh` scopes `go test` to `CHANGED_PKGS`; a milestone fails on earlier milestones' lint debt. |
| 437 | P0 | Real | `FixMilestone` prompt relies on implicit "context from the previous node" with no explicit `${ctx.tool_stdout}` block (unlike `VerifyMilestone`, which fences it). Fixer can be blind to the failing gate's stdout. |
| 438 | P0 | **Not real** | `CheckMilestoneOutputs` already `os.Stat`s the working tree (`[ ! -d ]` / `[ ! -e ]`, since #350). No `git ls-tree` probe exists in the node. Symptom was the combined effect of #439 + #440. |
| 439 | P1 | Real (via `accept`) | `CheckMilestoneOutputs` validates the full `milestones.md` manifest. It only runs at the all-done entry (correct there) **and the early `accept` ship path** — accepting at milestone 8/10 flags unbuilt milestones 9–10 dirs as missing. |
| 440 | P1 | Partially real | Path heuristic already drops bare prose words, but the `tr`-based tokenizer still leaks **dotted** prose (`go.sum`, `git.Fake`, `gh.Fake`) as phantom paths → false "missing". |
| 441 | P1 | Partially real | Escalation now surfaces `${ctx.tool_stdout}` + options, but `known_failures` feeds only `go test -skip` (cannot suppress lint) and no "mark-known-failure with the exact edit" guidance is surfaced. |
| 442 | P2 | Real | `golangci-lint` is `command -v`-guarded → silently optional; enforcement depends on the runner. |
| 443 | P2 | Real | `TestMilestone` uses `[ "$ATTEMPTS" -gt 3 ]`; a cap of 3 runs a 4th attempt and logs "attempt 4 of 3". |

## Decisions (from brainstorming)

- **#438:** Close as already-fixed. No code change. Fixing #439 + #440 removes the false
  positives the post-mortem attributed to a git-tree probe.
- **#442:** Keep `golangci-lint` optional (do **not** mutate the target project's toolchain;
  the gate runs inside an arbitrary project's build env that tracker does not own). Make the
  skip **loud** instead of silent.
- **#441:** Lint-aware suppression hatch + surface the exact edit at escalation (not the full
  skip/override/abort/manual options menu).

## The fixes

### #436 — milestone-scope the lint gate

In `ci-probe.sh` `run_language_native_gates`, the Go block runs `golangci-lint run` unscoped.
`ci-probe.sh` is sourced by both `verify.sh` (milestone-scoped) and `FinalBuild` / review nodes
(whole-tree), so scoping must be caller-controlled via an env var:

```sh
# in run_language_native_gates, Go block:
golangci-lint run ${LINT_NEW_FROM_REV:+--new-from-rev "$LINT_NEW_FROM_REV"} 2>&1 || LANG_RC=$?
```

`verify.sh` sets `LINT_NEW_FROM_REV="$MS_BASE"` **only when `MS_START` is a valid commit**
(i.e. the same branch where it uses `MS_BASE="$MS_START"` rather than the empty-tree fallback).
`FinalBuild` and the review nodes leave `LINT_NEW_FROM_REV` unset → whole-tree lint. This makes
lint milestone-scoped exactly like `go test`, and whole-tree at FinalBuild.

Edge case: when `MS_START` is empty/invalid, `verify.sh` uses the empty-tree hash for the
`go test` diff, but does **not** export `LINT_NEW_FROM_REV` — lint then runs whole-tree (safe
superset; a first milestone with no base gets full lint, never a crash on a non-commit rev).

### #437 — thread the failing gate's stdout into FixMilestone

Append a fenced block to the **end** of the `FixMilestone` prompt, mirroring the existing
`## TestMilestone stdout` block in `VerifyMilestone`:

```
## Failing gate output

Tail of the failing gate's stdout (64KB cap) that routed here — the real failure
signal (`go test`, `go build`, and the project CI / golangci-lint gate from
.ai/build/verify.sh). Delimited under its own heading — never interpolated mid-sentence.

${ctx.tool_stdout}
```

Change the fix steps so the fixer confirms against the **actual** gate, not just `go test`:
step 5 becomes "Re-run the exact failing gate to confirm the fix: `sh .ai/build/verify.sh`
(this runs build + tests + the lint/CI gate — not just `go test`)."

### #438 — close, no code change

Add a CHANGELOG note under `Fixed` explaining the earlier "MISSING from disk" false positives
are resolved by #439 + #440; the node has always `os.Stat`'d the working tree. Close the issue.

### #439 — scope the outputs check to built milestones

The bug reproduces only on the early-`accept` ship path (`EscalateMilestone -accept->
CheckMilestoneOutputs`), where not all milestones are built. Fix: extract `Files:` declarations
only for milestones `1..DONE_COUNT` from `milestones.md`, where

```sh
DONE_COUNT=$(ls -1 .ai/milestones/done 2>/dev/null | wc -l | tr -d ' ')
```

(the same `done/` marker dir `PickNextMilestone` counts). Select those milestone sections with
the same flexible `## Milestone N` header regex `PickNextMilestone` uses, then run the existing
`**Files**:` extraction over that scoped slice instead of the whole file. At the normal all-done
entry `DONE_COUNT == TOTAL`, so behavior is unchanged there. If `DONE_COUNT` is 0/unreadable,
fall back to the whole manifest (never check *fewer* than what exists — fail safe toward
catching skips).

### #440 — real path parser for declared files

Replace the whitespace tokenizer:

```sh
tr -s ',[:space:]' '\n' < .ai/build/declared-files.raw | sed -E '...' > declared-files.list
```

with a per-line parser that takes the **first backticked token**, falling back to the first
whitespace token after stripping inline prose:

```sh
# For each bullet: prefer the first `backticked` path; else strip everything
# after the first '(' or '#', then take the first whitespace token.
while IFS= read -r line; do
  tok=$(printf '%s\n' "$line" | sed -n 's/.*`\([^`]*\)`.*/\1/p' | head -1)
  if [ -z "$tok" ]; then
    tok=$(printf '%s\n' "$line" | sed -E 's/[(#].*$//' | sed -E 's/^[[:space:]]*[-*+][[:space:]]*//' | awk '{print $1}')
  fi
  [ -n "$tok" ] && printf '%s\n' "$tok"
done < .ai/build/declared-files.raw \
  | sed -E 's/[][`*()"]//g; s/^\.\///; s/[.:;]+$//' \
  | grep -v '^[[:space:]]*$' > .ai/build/declared-files.list || true
```

The existing path-heuristic filter (`case "$tok" in */*|*.*) … *) continue ;;`) stays as a
safety net. This removes phantom dotted tokens (`go.sum` from prose, `git.Fake`, `gh.Fake`).

### #441 — lint-aware suppression + surface the exact edit

1. **Lint suppression file.** Add `.ai/milestones/known_lint_failures` (non-comment, non-blank
   lines → `golangci-lint run --exclude "<pattern>"` args), threaded through the same env path
   as #436. In `ci-probe.sh` the Go block builds an `--exclude` list when the file exists:

   ```sh
   LINT_EXCLUDES=""
   if [ -f .ai/milestones/known_lint_failures ]; then
     while IFS= read -r pat; do
       case "$pat" in ''|\#*) continue ;; esac
       LINT_EXCLUDES="$LINT_EXCLUDES --exclude $pat"
     done < .ai/milestones/known_lint_failures
   fi
   golangci-lint run ${LINT_NEW_FROM_REV:+--new-from-rev "$LINT_NEW_FROM_REV"} $LINT_EXCLUDES 2>&1 || LANG_RC=$?
   ```

   (`--exclude` patterns are author/operator-controlled files, never LLM-origin `ctx.*`; they
   are only ever passed as `golangci-lint` args, never eval'd — consistent with the existing
   `known_failures` → `go test -skip` handling.)

2. **Surface at escalation.** Add a bullet to the `EscalateMilestone` prompt naming the exact
   files to edit and the follow-up action:

   > To mark a failure as known and retry: add the test name to
   > `.ai/milestones/known_failures` (one per line) or the lint pattern to
   > `.ai/milestones/known_lint_failures`, then pick **retry**.

### #442 — loud skip warning

Keep the `command -v golangci-lint` guard. Change the else branch from:

```sh
echo "INFO: golangci-lint not installed — skipping (optional)"
```

to a prominent warning noting enforcement is disabled and recommending a pinned install, e.g.:

```sh
echo "WARNING: golangci-lint not installed — lint enforcement is DISABLED for this run."
echo "WARNING: install a pinned version for reproducible gating (results may differ across environments)."
```

### #443 — attempt-cap off-by-one

In `TestMilestone`, change the boundary so a cap of 3 runs exactly 3 attempts and the label
agrees:

```sh
echo "--- attempt $ATTEMPTS of 3 ---"
if [ "$ATTEMPTS" -ge 3 ]; then
  echo "ESCALATE: milestone failed after $ATTEMPTS attempts"
  printf 'escalate'
  exit 1
fi
```

## Testing / verification

1. `dippin doctor examples/build_product.dip` — must stay **A** grade. (If `dippin` is not on
   PATH, ask the user — do not `go install` it.)
2. `go build ./...` and `go test ./... -short` — green.
3. New / extended `extractHeredoc`-based regression tests in `pipeline/build_product_*_test.go`:
   - **#436:** `verify.sh` sets `LINT_NEW_FROM_REV` from `MS_BASE`; `ci-probe.sh` passes
     `--new-from-rev` only when the env var is set; FinalBuild/review do not set it.
   - **#437:** `FixMilestone` prompt contains `${ctx.tool_stdout}` and the "re-run
     `sh .ai/build/verify.sh`" instruction.
   - **#439:** `CheckMilestoneOutputs` reads `.ai/milestones/done` and scopes extraction to
     `DONE_COUNT` milestones (assert the `done`-count logic is present; assert the all-done
     path is unchanged).
   - **#440:** the first-backtick parser is present and the raw `tr -s ',[:space:]'` tokenizer
     is gone.
   - **#441:** `known_lint_failures` is wired into the `--exclude` list and named in the
     `EscalateMilestone` prompt.
   - **#442:** the skip branch emits `WARNING` (not `INFO … optional`).
   - **#443:** `TestMilestone` uses `-ge 3`.
4. `CHANGELOG.md` `Fixed` / `Changed` entries; version bump to **v0.42.0**; `README.md` if any
   documented behavior changed.

## Out of scope

- `build_product_with_superspec.dip` (does not share these nodes).
- Auto-installing or version-pinning `golangci-lint` in the target env (rejected — tracker does
  not own the target project's toolchain).
- The full skip/override/abort/manual escalation options menu (#441 kept to the lint hatch +
  surfaced edit).
- Making `golangci-lint` a required gate (#442 kept optional-but-loud).

## Commit / PR structure

One PR (`fix/build-product-gate-loop-436-443` → `main`), one logical commit per issue where it
keeps the diff reviewable, closing #436, #437, #438, #439, #440, #441, #442, #443. Followed by a
`release: v0.42.0` step per the project's release process.
