# build_product Gate-Loop Fixes (#436–#443) Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Fix the eight milestone gate-loop issues (#436–#443) from the `code-goblin` run `400eebf3f3c7` post-mortem so a milestone can't wedge on earlier milestones' lint debt, the fixer sees the real failure, the outputs gate stops flagging false-missing, and the run has an in-band escape hatch.

**Architecture:** All gate logic is embedded as heredoc shell scripts (`.ai/build/ci-probe.sh`, `.ai/build/verify.sh`, both written by the `Setup` node) and inline tool-node `command:` blocks / agent `prompt:` blocks inside the single file `examples/build_product.dip`. Fixes are surgical edits to those blocks, each guarded by a string-level regression test that extracts the script/prompt via the existing `extractHeredoc` / `toolCmd` / `nodePrompt` helpers.

**Tech Stack:** Dippin `.dip` pipeline DSL; POSIX `sh` (the gate scripts); Go `testing` (regression guards in `pipeline/`); `dippin doctor` (grade gate).

## Global Constraints

- **Single source file for pipeline changes:** `examples/build_product.dip`. Do NOT touch `build_product_with_superspec.dip` (does not share these nodes).
- **All new tests live in one new file:** `pipeline/build_product_gate_loop_fixes_test.go`, package `pipeline`.
- **Never `--no-verify`.** The pre-commit hook runs format/vet/build/tests/race/coverage/complexity/dippin-lint and takes ~2–4 minutes; allow up to 6 minutes per commit. If a hook fails, fix the root cause.
- **`dippin doctor examples/build_product.dip` must stay grade A** after every change. If `dippin` is not on PATH, ask the user — do NOT `go install` it.
- **Extracted LLM/path tokens are only ever used in quoted `[ -d ]` / `[ -e ]` tests or as fixed CLI args — never `eval`'d or interpolated into a command** (CLAUDE.md tool-node safety).
- **stdout interpolation token in prompts is `${ctx.tool_stdout}`**, fenced under a `---` separator + `## Heading` (mirror the existing `## TestMilestone stdout` block in `VerifyMilestone`).
- Verification commands: `go build ./...`, `go test ./... -short`, `go test ./pipeline/ -run TestBuildProductIssue<N> -v`, `dippin doctor examples/build_product.dip`.

---

### Task 1: #443 — attempt-cap off-by-one

**Files:**
- Modify: `examples/build_product.dip` (node `TestMilestone`, the `-gt 3` boundary near line 1293)
- Test: `pipeline/build_product_gate_loop_fixes_test.go` (create)

**Interfaces:**
- Produces: no code interface; a behavior guard other tasks don't depend on.

- [ ] **Step 1: Write the failing test** (create the new test file with this first function)

```go
// ABOUTME: Regression guards for the build_product gate-loop fixes batch
// ABOUTME: (issues #436–#443) from the code-goblin run 400eebf3f3c7 post-mortem.
package pipeline

import (
	"strings"
	"testing"
)

// TestBuildProductIssue443AttemptCapBoundary pins #443: a cap of 3 must run
// exactly 3 attempts. The pre-fix `-gt 3` ran a 4th attempt and logged
// "attempt 4 of 3".
func TestBuildProductIssue443AttemptCapBoundary(t *testing.T) {
	cmd := toolCmd(t, "TestMilestone")
	if !strings.Contains(cmd, `[ "$ATTEMPTS" -ge 3 ]`) {
		t.Error("TestMilestone must escalate at `-ge 3` so a cap of 3 runs exactly 3 attempts (issue #443)")
	}
	if strings.Contains(cmd, `[ "$ATTEMPTS" -gt 3 ]`) {
		t.Error("TestMilestone still uses `-gt 3` — runs a 4th attempt before escalating (issue #443 regression)")
	}
}
```

- [ ] **Step 2: Run the test to verify it fails**

Run: `go test ./pipeline/ -run TestBuildProductIssue443AttemptCapBoundary -v`
Expected: FAIL (`-ge 3` not present; `-gt 3` still present).

- [ ] **Step 3: Apply the fix** — in `examples/build_product.dip`, node `TestMilestone`:

Replace:
```
      if [ "$ATTEMPTS" -gt 3 ]; then
```
with:
```
      if [ "$ATTEMPTS" -ge 3 ]; then
```
(Leave the `echo "--- attempt $ATTEMPTS of 3 ---"` line above it unchanged — the label now agrees: attempts 1 and 2 route to the fix loop, attempt 3 escalates as "attempt 3 of 3".)

- [ ] **Step 4: Run the test to verify it passes**

Run: `go test ./pipeline/ -run TestBuildProductIssue443AttemptCapBoundary -v`
Expected: PASS.

- [ ] **Step 5: Verify the pipeline still grades A**

Run: `dippin doctor examples/build_product.dip`
Expected: grade A.

- [ ] **Step 6: Commit**

```bash
git add examples/build_product.dip pipeline/build_product_gate_loop_fixes_test.go
git commit -m "fix(examples): milestone retry cap runs exactly N attempts (#443)

Co-Authored-By: Claude Opus 4.8 (1M context) <noreply@anthropic.com>
Claude-Session: https://claude.ai/code/session_012U14Lj3xBwjmSLJAx6jqbr"
```

---

### Task 2: #437 — thread the failing gate's stdout into FixMilestone

**Files:**
- Modify: `examples/build_product.dip` (node `FixMilestone`, prompt near lines 1707–1730)
- Test: `pipeline/build_product_gate_loop_fixes_test.go` (append)

**Interfaces:**
- Consumes: `${ctx.tool_stdout}` (populated by the upstream `TestMilestone` tool node on every inbound fail edge).
- Produces: none.

- [ ] **Step 1: Write the failing test** (append to the test file)

```go
// TestBuildProductIssue437FixMilestoneSeesGateOutput pins #437: FixMilestone
// must be handed the failing gate's real stdout (not the prior node's DONE
// narrative) and told to re-run the exact gate, not just `go test`.
func TestBuildProductIssue437FixMilestoneSeesGateOutput(t *testing.T) {
	g := loadBuildProduct(t)
	p := nodePrompt(t, g, "FixMilestone")
	if !strings.Contains(p, "## Failing gate output") {
		t.Error("FixMilestone prompt must include a `## Failing gate output` heading (issue #437)")
	}
	if !strings.Contains(p, "${ctx.tool_stdout}") {
		t.Error("FixMilestone prompt must interpolate ${ctx.tool_stdout} so the fixer sees the real failure (issue #437)")
	}
	if !strings.Contains(p, "sh .ai/build/verify.sh") {
		t.Error("FixMilestone prompt must instruct re-running `sh .ai/build/verify.sh`, not just go test (issue #437)")
	}
}
```

- [ ] **Step 2: Run the test to verify it fails**

Run: `go test ./pipeline/ -run TestBuildProductIssue437FixMilestoneSeesGateOutput -v`
Expected: FAIL (none of the three markers present).

- [ ] **Step 3: Apply the fix** — three edits in node `FixMilestone`:

Edit A — replace the "previous node" bullet:
```
      - The test output showing failures (in context from the previous node)
```
with:
```
      - The failing gate's real stdout — the "## Failing gate output" block at
        the END of this prompt (build / test / lint / CI output from the gate
        that routed you here)
```

Edit B — replace step 5:
```
      5. Run the specific failing test to verify before committing
```
with:
```
      5. Re-run the EXACT failing gate to confirm the fix: `sh .ai/build/verify.sh`
         (this runs build + every stack's tests + the lint/CI gate — not just `go test`)
```

Edit C — append this block at the very end of the `FixMilestone` prompt, immediately after the line `never end a session with a green-but-uncommitted tree.`:
```

      ---
      ## Failing gate output

      Tail of the failing gate's stdout (64KB cap) that routed here — the real
      failure signal (go build / go test and the project CI / golangci-lint gate
      from .ai/build/verify.sh). Delimited under its own heading — never
      interpolated mid-sentence.

      ${ctx.tool_stdout}
```

- [ ] **Step 4: Run the test to verify it passes**

Run: `go test ./pipeline/ -run TestBuildProductIssue437FixMilestoneSeesGateOutput -v`
Expected: PASS.

- [ ] **Step 5: Verify the pipeline still grades A**

Run: `dippin doctor examples/build_product.dip`
Expected: grade A.

- [ ] **Step 6: Commit**

```bash
git add examples/build_product.dip pipeline/build_product_gate_loop_fixes_test.go
git commit -m "fix(examples): FixMilestone sees the failing gate's stdout, re-runs the real gate (#437)

Co-Authored-By: Claude Opus 4.8 (1M context) <noreply@anthropic.com>
Claude-Session: https://claude.ai/code/session_012U14Lj3xBwjmSLJAx6jqbr"
```

---

### Task 3: #440 — first-backticked-token path parser in CheckMilestoneOutputs

**Files:**
- Modify: `examples/build_product.dip` (node `CheckMilestoneOutputs`, tokenizer near lines 1804–1809)
- Test: `pipeline/build_product_gate_loop_fixes_test.go` (append)

**Interfaces:**
- Produces: `.ai/build/declared-files.list` (one path per line) — same downstream contract the existing `while IFS= read -r tok` loop consumes. Do NOT change that loop.

- [ ] **Step 1: Write the failing test** (append)

```go
// TestBuildProductIssue440FirstBacktickParser pins #440: declared-file
// extraction must take the first backticked path per bullet, not whitespace-
// tokenize prose into phantom paths (go.sum, git.Fake, gh.Fake).
func TestBuildProductIssue440FirstBacktickParser(t *testing.T) {
	cmd := toolCmd(t, "CheckMilestoneOutputs")
	if !strings.Contains(cmd, `sed -E 's/[(#].*$//'`) {
		t.Error("CheckMilestoneOutputs must strip inline prose after `(`/`#` in the path parser (issue #440)")
	}
	if strings.Contains(cmd, "tr -s ',[:space:]'") {
		t.Error("CheckMilestoneOutputs still whitespace-tokenizes prose into phantom paths (issue #440 regression)")
	}
}
```

- [ ] **Step 2: Run the test to verify it fails**

Run: `go test ./pipeline/ -run TestBuildProductIssue440FirstBacktickParser -v`
Expected: FAIL (parser marker absent; old `tr` tokenizer present).

- [ ] **Step 3: Apply the fix** — in node `CheckMilestoneOutputs`, replace this block:
```
      # Tokenize: split on commas/whitespace, strip markdown decoration
      # (backticks, brackets, parens, bold markers), leading ./ and bullet
      # dashes, trailing punctuation. Blank lines dropped per CLAUDE.md.
      tr -s ',[:space:]' '\n' < .ai/build/declared-files.raw | \
        sed -E 's/[][`*()"]//g; s/^-+//; s/^\.\///; s/[.:;]+$//' | \
        grep -v '^[[:space:]]*$' > .ai/build/declared-files.list || true
```
with:
```
      # Parse ONE path per bullet (issue #440): prefer the FIRST backticked
      # token; else strip inline prose after the first `(` or `#`, drop the
      # bullet marker, and take the first whitespace token. Whitespace-tokenizing
      # the whole bullet turned prose ("Deps struct", "go.sum", "NewRepo(t)")
      # into phantom paths. The single-quoted sed expressions make the backticks
      # literal (sh does not run command-substitution inside single quotes), and
      # the extracted tokens are only ever used in quoted [ -d ]/[ -e ] tests.
      : > .ai/build/declared-files.list
      while IFS= read -r line; do
        tok=$(printf '%s\n' "$line" | sed -n 's/.*`\([^`]*\)`.*/\1/p' | head -1)
        if [ -z "$tok" ]; then
          tok=$(printf '%s\n' "$line" \
            | sed -E 's/[(#].*$//' \
            | sed -E 's/^[[:space:]]*[-*+][[:space:]]*//' \
            | awk '{print $1}')
        fi
        tok=$(printf '%s\n' "$tok" | sed -E 's/[][`*()"]//g; s/^\.\///; s/[.:;]+$//')
        [ -n "$tok" ] && printf '%s\n' "$tok" >> .ai/build/declared-files.list
      done < .ai/build/declared-files.raw
```

- [ ] **Step 4: Run the test to verify it passes**

Run: `go test ./pipeline/ -run TestBuildProductIssue440FirstBacktickParser -v`
Expected: PASS.

- [ ] **Step 5: Verify the pipeline still grades A**

Run: `dippin doctor examples/build_product.dip`
Expected: grade A.

- [ ] **Step 6: Commit**

```bash
git add examples/build_product.dip pipeline/build_product_gate_loop_fixes_test.go
git commit -m "fix(examples): CheckMilestoneOutputs parses declared paths, not prose (#440)

Co-Authored-By: Claude Opus 4.8 (1M context) <noreply@anthropic.com>
Claude-Session: https://claude.ai/code/session_012U14Lj3xBwjmSLJAx6jqbr"
```

---

### Task 4: #439 — scope the outputs check to built milestones

**Files:**
- Modify: `examples/build_product.dip` (node `CheckMilestoneOutputs`, between the empty-PLAN guard ~line 1778 and the `**Files**:` extraction awk ~line 1784)
- Test: `pipeline/build_product_gate_loop_fixes_test.go` (append)

**Interfaces:**
- Consumes: `.ai/milestones/done` (marker-per-completed-milestone dir that `PickNextMilestone` counts as `DONE_COUNT`), `.ai/decisions/milestones.md` (`$PLAN`).
- Produces: `$SCOPED_PLAN` (a milestone-scoped slice of `$PLAN`) that the existing `**Files**:` awk now reads instead of `$PLAN`.

- [ ] **Step 1: Write the failing test** (append)

```go
// TestBuildProductIssue439OutputsScopedToBuiltMilestones pins #439: the outputs
// gate runs on the early-`accept` ship path where later milestones aren't built
// yet, so it must scope its declared-file manifest to completed milestones
// (via the .ai/milestones/done count), not validate the whole plan.
func TestBuildProductIssue439OutputsScopedToBuiltMilestones(t *testing.T) {
	cmd := toolCmd(t, "CheckMilestoneOutputs")
	if !strings.Contains(cmd, ".ai/milestones/done") {
		t.Error("CheckMilestoneOutputs must count completed milestones from .ai/milestones/done to scope the manifest (issue #439)")
	}
	if !strings.Contains(cmd, "SCOPED_PLAN") {
		t.Error("CheckMilestoneOutputs must extract Files from a milestone-scoped plan slice, not the whole milestones.md (issue #439)")
	}
}
```

- [ ] **Step 2: Run the test to verify it fails**

Run: `go test ./pipeline/ -run TestBuildProductIssue439OutputsScopedToBuiltMilestones -v`
Expected: FAIL (`.ai/milestones/done` / `SCOPED_PLAN` not referenced).

- [ ] **Step 3: Apply the fix** — two edits in node `CheckMilestoneOutputs`:

Edit A — insert this block immediately AFTER the empty-`$PLAN` guard (the `fi` closing the `if [ ! -s "$PLAN" ]; then … exit 1` block) and BEFORE the comment `# Extract the **Files**: declarations.`:
```
      # Scope the manifest to milestones actually BUILT (issue #439). This gate
      # also runs on the early `accept` ship path (EscalateMilestone -accept->),
      # where later milestones don't exist yet — validating the whole plan there
      # flags their unbuilt dirs as missing. DONE_COUNT is the number of markers
      # PickNextMilestone wrote to .ai/milestones/done. 0/unreadable → fall back
      # to the whole plan (fail safe toward catching a real skip). At the normal
      # all-done entry DONE_COUNT == TOTAL, so behavior is unchanged there.
      DONE_COUNT=$(ls -1 .ai/milestones/done 2>/dev/null | wc -l | tr -d ' ')
      case "$DONE_COUNT" in ''|*[!0-9]*) DONE_COUNT=0 ;; esac
      SCOPED_PLAN="$PLAN"
      if [ "$DONE_COUNT" -gt 0 ]; then
        awk -v last="$DONE_COUNT" '
          /^#+ *[Mm]ilestone *[0-9]+/ {
            match($0, /[0-9]+/); n = substr($0, RSTART, RLENGTH) + 0
            keep = (n >= 1 && n <= last)
          }
          keep { print }
        ' "$PLAN" > .ai/build/scoped-milestones.md
        if [ -s .ai/build/scoped-milestones.md ]; then
          SCOPED_PLAN=".ai/build/scoped-milestones.md"
        fi
      fi
```

Edit B — point the existing `**Files**:` extraction awk at the scoped slice. Replace the awk's input file:
```
      ' "$PLAN" > .ai/build/declared-files.raw
```
with:
```
      ' "$SCOPED_PLAN" > .ai/build/declared-files.raw
```

- [ ] **Step 4: Run the test to verify it passes**

Run: `go test ./pipeline/ -run TestBuildProductIssue439OutputsScopedToBuiltMilestones -v`
Expected: PASS.

- [ ] **Step 5: Verify the pipeline still grades A**

Run: `dippin doctor examples/build_product.dip`
Expected: grade A.

- [ ] **Step 6: Commit**

```bash
git add examples/build_product.dip pipeline/build_product_gate_loop_fixes_test.go
git commit -m "fix(examples): scope CheckMilestoneOutputs to built milestones (#439)

Co-Authored-By: Claude Opus 4.8 (1M context) <noreply@anthropic.com>
Claude-Session: https://claude.ai/code/session_012U14Lj3xBwjmSLJAx6jqbr"
```

---

### Task 5: #436 — milestone-scope the lint gate

**Files:**
- Modify: `examples/build_product.dip` (`Setup` node's `.ai/build/ci-probe.sh` heredoc Go block ~lines 171–176; `.ai/build/verify.sh` heredoc MS_BASE block ~lines 304–309)
- Test: `pipeline/build_product_gate_loop_fixes_test.go` (append)

**Interfaces:**
- Produces: env var `LINT_NEW_FROM_REV` set by `verify.sh` (only when the milestone base is a real commit) and consumed by `ci-probe.sh`'s `golangci-lint` invocation.

- [ ] **Step 1: Write the failing test** (append)

```go
// TestBuildProductIssue436LintMilestoneScoped pins #436: the lint gate must be
// milestone-scoped like `go test`, via --new-from-rev fed from the milestone
// base, while FinalBuild (which leaves the env var unset) still lints whole-tree.
func TestBuildProductIssue436LintMilestoneScoped(t *testing.T) {
	setup := toolCmd(t, "Setup")
	probe := extractHeredoc(t, setup, ".ai/build/ci-probe.sh", "PROBE_EOF")
	if !strings.Contains(probe, `--new-from-rev "$LINT_NEW_FROM_REV"`) {
		t.Error("ci-probe.sh golangci-lint must honor $LINT_NEW_FROM_REV via --new-from-rev (issue #436)")
	}
	if !strings.Contains(probe, `${LINT_NEW_FROM_REV:+`) {
		t.Error("ci-probe.sh must only pass --new-from-rev when LINT_NEW_FROM_REV is set (whole-tree at FinalBuild) (issue #436)")
	}
	verify := extractHeredoc(t, setup, ".ai/build/verify.sh", "VERIFY_EOF")
	if !strings.Contains(verify, "LINT_NEW_FROM_REV=") {
		t.Error("verify.sh must set LINT_NEW_FROM_REV from the milestone base so lint is scoped like go test (issue #436)")
	}
}
```

- [ ] **Step 2: Run the test to verify it fails**

Run: `go test ./pipeline/ -run TestBuildProductIssue436LintMilestoneScoped -v`
Expected: FAIL.

- [ ] **Step 3: Apply the fix** — two edits:

Edit A — in the `.ai/build/ci-probe.sh` heredoc, Go block, replace:
```
            echo "--- golangci-lint run (language-native gate) ---"
            golangci-lint run 2>&1 || LANG_RC=$?
```
with:
```
            echo "--- golangci-lint run (language-native gate) ---"
            # Milestone-scope the lint gate (issue #436): when verify.sh set
            # LINT_NEW_FROM_REV to the milestone base, only NEW issues fail —
            # matching the milestone-scoped `go test`. FinalBuild leaves it unset
            # → whole-tree lint.
            golangci-lint run ${LINT_NEW_FROM_REV:+--new-from-rev "$LINT_NEW_FROM_REV"} 2>&1 || LANG_RC=$?
```

Edit B — in the `.ai/build/verify.sh` heredoc, replace:
```
        else
          MS_BASE="$MS_START"
        fi
```
with:
```
        else
          MS_BASE="$MS_START"
          # Milestone-scope the lint gate too (issue #436) — only when we have a
          # real base commit (never the empty-tree fallback, which is not a rev
          # golangci-lint --new-from-rev accepts). ci-probe.sh, sourced below in
          # the same shell, reads this to pass --new-from-rev.
          LINT_NEW_FROM_REV="$MS_START"
          export LINT_NEW_FROM_REV
        fi
```

- [ ] **Step 4: Run the test to verify it passes**

Run: `go test ./pipeline/ -run TestBuildProductIssue436LintMilestoneScoped -v`
Expected: PASS.

- [ ] **Step 5: Verify the pipeline still grades A**

Run: `dippin doctor examples/build_product.dip`
Expected: grade A.

- [ ] **Step 6: Commit**

```bash
git add examples/build_product.dip pipeline/build_product_gate_loop_fixes_test.go
git commit -m "fix(examples): milestone-scope the lint gate like go test (#436)

Co-Authored-By: Claude Opus 4.8 (1M context) <noreply@anthropic.com>
Claude-Session: https://claude.ai/code/session_012U14Lj3xBwjmSLJAx6jqbr"
```

---

### Task 6: #441 — lint-aware suppression hatch + surface it at escalation

**Files:**
- Modify: `examples/build_product.dip` (`Setup` node's `.ai/build/ci-probe.sh` heredoc Go block — the same `golangci-lint` invocation from Task 5; node `EscalateMilestone` prompt)
- Test: `pipeline/build_product_gate_loop_fixes_test.go` (append)

**Interfaces:**
- Consumes: the `golangci-lint run ${LINT_NEW_FROM_REV:+…}` line produced by Task 5. This task inserts `$LINT_EXCLUDES` into that exact line, so apply Task 5 first.
- Produces: `.ai/milestones/known_lint_failures` (operator-editable suppression file; lines → `--exclude` patterns).

- [ ] **Step 1: Write the failing test** (append)

```go
// TestBuildProductIssue441LintSuppressionHatch pins #441: an operator must be
// able to suppress a LINT failure (not just go-test failures) via a named file,
// and EscalateMilestone must tell them the exact files to edit.
func TestBuildProductIssue441LintSuppressionHatch(t *testing.T) {
	probe := extractHeredoc(t, toolCmd(t, "Setup"), ".ai/build/ci-probe.sh", "PROBE_EOF")
	if !strings.Contains(probe, "known_lint_failures") {
		t.Error("ci-probe.sh must read .ai/milestones/known_lint_failures (issue #441)")
	}
	if !strings.Contains(probe, "--exclude") {
		t.Error("ci-probe.sh must feed known_lint_failures into golangci-lint --exclude (issue #441)")
	}
	g := loadBuildProduct(t)
	esc := nodePrompt(t, g, "EscalateMilestone")
	if !strings.Contains(esc, "known_lint_failures") || !strings.Contains(esc, "known_failures") {
		t.Error("EscalateMilestone prompt must name both suppression files to edit (issue #441)")
	}
}
```

- [ ] **Step 2: Run the test to verify it fails**

Run: `go test ./pipeline/ -run TestBuildProductIssue441LintSuppressionHatch -v`
Expected: FAIL.

- [ ] **Step 3: Apply the fix** — two edits:

Edit A — in the `.ai/build/ci-probe.sh` heredoc, replace the (Task-5-modified) block:
```
            echo "--- golangci-lint run (language-native gate) ---"
            # Milestone-scope the lint gate (issue #436): when verify.sh set
            # LINT_NEW_FROM_REV to the milestone base, only NEW issues fail —
            # matching the milestone-scoped `go test`. FinalBuild leaves it unset
            # → whole-tree lint.
            golangci-lint run ${LINT_NEW_FROM_REV:+--new-from-rev "$LINT_NEW_FROM_REV"} 2>&1 || LANG_RC=$?
```
with:
```
            echo "--- golangci-lint run (language-native gate) ---"
            # Milestone-scope the lint gate (issue #436): when verify.sh set
            # LINT_NEW_FROM_REV to the milestone base, only NEW issues fail —
            # matching the milestone-scoped `go test`. FinalBuild leaves it unset
            # → whole-tree lint.
            # Lint escape hatch (issue #441): non-comment lines in
            # .ai/milestones/known_lint_failures become --exclude patterns, so an
            # operator can suppress a lint failure the milestone can't fix (the
            # go-test hatch known_failures can't). Patterns are operator-authored
            # and only ever passed as golangci-lint args — never eval'd.
            LINT_EXCLUDES=""
            if [ -f .ai/milestones/known_lint_failures ]; then
              while IFS= read -r pat; do
                case "$pat" in ''|\#*) continue ;; esac
                LINT_EXCLUDES="$LINT_EXCLUDES --exclude $pat"
              done < .ai/milestones/known_lint_failures
            fi
            golangci-lint run ${LINT_NEW_FROM_REV:+--new-from-rev "$LINT_NEW_FROM_REV"} $LINT_EXCLUDES 2>&1 || LANG_RC=$?
```

Edit B — in node `EscalateMilestone`, add an escape-hatch bullet to the `Options:` list. Immediately after the `- **retry**:` option line, insert:
```
      - **mark a known failure, then retry**: if a specific test or lint rule is a
        false positive the milestone can't fix, add it and pick **retry**. Test
        names go in `.ai/milestones/known_failures` (one per line); golangci-lint
        exclude patterns go in `.ai/milestones/known_lint_failures`. The next
        attempt re-runs the gate with those suppressions applied.
```

- [ ] **Step 4: Run the test to verify it passes**

Run: `go test ./pipeline/ -run TestBuildProductIssue441LintSuppressionHatch -v`
Expected: PASS.

- [ ] **Step 5: Verify the pipeline still grades A**

Run: `dippin doctor examples/build_product.dip`
Expected: grade A.

- [ ] **Step 6: Commit**

```bash
git add examples/build_product.dip pipeline/build_product_gate_loop_fixes_test.go
git commit -m "feat(examples): lint-aware escape hatch surfaced at escalation (#441)

Co-Authored-By: Claude Opus 4.8 (1M context) <noreply@anthropic.com>
Claude-Session: https://claude.ai/code/session_012U14Lj3xBwjmSLJAx6jqbr"
```

---

### Task 7: #442 — make the golangci-lint skip loud

**Files:**
- Modify: `examples/build_product.dip` (`Setup` node's `.ai/build/ci-probe.sh` heredoc, the golangci-lint `else` branch ~line 175)
- Test: `pipeline/build_product_gate_loop_fixes_test.go` (append)

**Interfaces:** none.

- [ ] **Step 1: Write the failing test** (append)

```go
// TestBuildProductIssue442LintSkipIsLoud pins #442: an absent golangci-lint must
// WARN that enforcement is disabled, not silently skip as "optional".
func TestBuildProductIssue442LintSkipIsLoud(t *testing.T) {
	probe := extractHeredoc(t, toolCmd(t, "Setup"), ".ai/build/ci-probe.sh", "PROBE_EOF")
	if !strings.Contains(probe, "WARNING: golangci-lint not installed") {
		t.Error("ci-probe.sh must WARN loudly when golangci-lint is absent (issue #442)")
	}
	if strings.Contains(probe, "golangci-lint not installed — skipping (optional)") {
		t.Error("ci-probe.sh still prints the quiet `skipping (optional)` INFO for golangci-lint (issue #442 regression)")
	}
}
```

- [ ] **Step 2: Run the test to verify it fails**

Run: `go test ./pipeline/ -run TestBuildProductIssue442LintSkipIsLoud -v`
Expected: FAIL.

- [ ] **Step 3: Apply the fix** — in the `.ai/build/ci-probe.sh` heredoc, replace:
```
          else
            echo "INFO: golangci-lint not installed — skipping (optional)"
          fi
```
with:
```
          else
            echo "WARNING: golangci-lint not installed — lint enforcement is DISABLED for this run."
            echo "WARNING: install a pinned golangci-lint for reproducible gating (results may differ across environments)."
          fi
```
(Only the golangci-lint `else` branch changes — leave the tsc/eslint/ruff/mypy/cargo "optional" skips untouched.)

- [ ] **Step 4: Run the test to verify it passes**

Run: `go test ./pipeline/ -run TestBuildProductIssue442LintSkipIsLoud -v`
Expected: PASS.

- [ ] **Step 5: Verify the pipeline still grades A**

Run: `dippin doctor examples/build_product.dip`
Expected: grade A.

- [ ] **Step 6: Commit**

```bash
git add examples/build_product.dip pipeline/build_product_gate_loop_fixes_test.go
git commit -m "fix(examples): warn loudly when golangci-lint is absent (#442)

Co-Authored-By: Claude Opus 4.8 (1M context) <noreply@anthropic.com>
Claude-Session: https://claude.ai/code/session_012U14Lj3xBwjmSLJAx6jqbr"
```

---

### Task 8: #438 close-out, CHANGELOG, and full-suite verification

**Files:**
- Modify: `CHANGELOG.md` (`## [Unreleased]` section)
- Verify only: whole repo + all example pipelines

**Interfaces:** none.

- [ ] **Step 1: Add the CHANGELOG entries** — under `## [Unreleased]`, add these subsections (create the headers if absent):

```markdown
### Fixed

- **Milestone lint gate is now milestone-scoped (#436).** `.ai/build/ci-probe.sh`
  passes `golangci-lint run --new-from-rev "$MS_BASE"` when `verify.sh` sets the
  milestone base, so a milestone no longer fails on earlier, already-accepted
  milestones' lint debt. FinalBuild still lints the whole tree.
- **FixMilestone now sees the failing gate's real stdout (#437).** The prompt
  includes a `## Failing gate output` block with `${ctx.tool_stdout}` and
  instructs re-running `sh .ai/build/verify.sh` — the fixer is no longer blind to
  a lint/CI failure it never ran.
- **CheckMilestoneOutputs no longer flags unbuilt future milestones (#439).** The
  declared-outputs manifest is scoped to completed milestones (`.ai/milestones/done`
  count), so the early `accept` ship path stops reporting not-yet-built milestone
  directories as missing.
- **CheckMilestoneOutputs parses declared paths, not prose (#440).** Declared-file
  extraction takes the first backticked token per bullet instead of whitespace-
  tokenizing prose, eliminating phantom "missing" entries (`go.sum`, `git.Fake`).
- **Milestone retry cap is exact (#443).** A cap of 3 now runs exactly 3 attempts
  (was `-gt 3`, which ran a 4th and logged "attempt 4 of 3").
- **CheckMilestoneOutputs already probed the working tree (#438).** Verified the
  node `os.Stat`s disk (`[ -d ]`/`[ -e ]`, since #350); no git-tree probe exists.
  The post-mortem's "MISSING from disk" false positives are resolved by #439/#440.

### Changed

- **Lint failures now have an in-band escape hatch (#441).** Lines in
  `.ai/milestones/known_lint_failures` become `golangci-lint --exclude` patterns,
  and the `EscalateMilestone` gate names the exact suppression files to edit
  before retrying.
- **Absent golangci-lint warns loudly (#442).** The gate still runs when the
  linter is missing, but now emits a `WARNING` that lint enforcement is disabled
  (was a quiet `INFO … skipping (optional)`).
```

- [ ] **Step 2: Run the full short suite**

Run: `go build ./... && go test ./... -short`
Expected: all packages PASS.

- [ ] **Step 3: Run the new regression guards together**

Run: `go test ./pipeline/ -run 'TestBuildProductIssue4(36|37|39|40|41|42|43)' -v`
Expected: all 7 PASS.

- [ ] **Step 4: Grade the touched example pipelines**

Run: `dippin doctor examples/ask_and_execute.dip examples/build_product.dip examples/build_product_with_superspec.dip`
Expected: grade A across the board.

- [ ] **Step 5: Commit**

```bash
git add CHANGELOG.md
git commit -m "docs: changelog for build_product gate-loop fixes (#436-#443)

Co-Authored-By: Claude Opus 4.8 (1M context) <noreply@anthropic.com>
Claude-Session: https://claude.ai/code/session_012U14Lj3xBwjmSLJAx6jqbr"
```

- [ ] **Step 6: Push the branch and open the batch PR**

```bash
git push -u origin fix/build-product-gate-loop-436-443
gh pr create --base main --title "fix(examples): build_product gate-loop fixes (#436–#443)" --body "$(cat <<'EOF'
Batch fixes for the milestone gate-loop issues from the code-goblin run
400eebf3f3c7 post-mortem. All changes are in examples/build_product.dip plus
regression guards in pipeline/build_product_gate_loop_fixes_test.go.

- #436 milestone-scope the lint gate (--new-from-rev)
- #437 FixMilestone sees the failing gate's stdout, re-runs the real gate
- #438 verified already-fixed (working-tree probe since #350) — no code change
- #439 scope CheckMilestoneOutputs to built milestones (early-accept path)
- #440 first-backtick path parser (no more phantom paths)
- #441 lint-aware escape hatch, surfaced at escalation
- #442 warn loudly when golangci-lint is absent
- #443 retry cap runs exactly N attempts

Closes #436, #437, #438, #439, #440, #441, #442, #443

🤖 Generated with [Claude Code](https://claude.com/claude-code)

https://claude.ai/code/session_012U14Lj3xBwjmSLJAx6jqbr
EOF
)"
```

---

## Release cut (v0.42.0) — after the batch PR merges

Per CLAUDE.md, merging the PR is NOT the release. After this PR (and its `release: v0.42.0` doc PR) merge to `main`:

- [ ] Promote `## [Unreleased]` → `## [0.42.0] - <date>` in `CHANGELOG.md` (in the release PR).
- [ ] `git tag -a v0.42.0 <merge-commit-sha> -m "release: v0.42.0"`
- [ ] `git push origin v0.42.0` (triggers `.github/workflows/release.yml` → GoReleaser).
- [ ] Confirm with `gh release view v0.42.0` that the published entry with assets exists.

---

## Self-Review

**Spec coverage:** #436 → Task 5; #437 → Task 2; #438 → Task 8 (changelog note, no code, per decision); #439 → Task 4; #440 → Task 3; #441 → Task 6; #442 → Task 7; #443 → Task 1. CHANGELOG/version → Task 8 + Release section. Every spec fix maps to a task.

**Placeholder scan:** No TBD/TODO; every code step shows exact old→new text and every test step shows full Go code.

**Type/interface consistency:** Task 5 produces the `golangci-lint run ${LINT_NEW_FROM_REV:+…}` line; Task 6 consumes that exact line (ordering noted in Task 6 Interfaces). `LINT_NEW_FROM_REV` spelled identically in ci-probe.sh (consumer) and verify.sh (producer). `SCOPED_PLAN`, `DONE_COUNT`, `LINT_EXCLUDES`, `known_lint_failures`, `declared-files.list`, `${ctx.tool_stdout}` used consistently across tasks. Test names `TestBuildProductIssue4XX…` match the Task-8 run-filter regex.

**Ordering dependency:** Tasks 5 → 6 → 7 all touch the ci-probe.sh Go block; each edit's `old_string` is written against the state left by the prior task. Tasks 3 → 4 both touch CheckMilestoneOutputs (different regions, independent). Follow numeric order.
