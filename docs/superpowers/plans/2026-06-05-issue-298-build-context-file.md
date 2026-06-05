# Per-Node Build-Context File (#298) Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Cure per-node rediscovery amnesia in `build_product.dip` by adding a short, machine-written, append-only `.ai/build/build-context.md` (architecture map + one entry per completed milestone) that the build-loop agents read first.

**Architecture:** Pure `.dip` edits to `examples/build_product.dip` plus one new negative-control test file. `Setup` seeds the file with a redirected command group; `PickNextMilestone` records a per-milestone commit-range marker; `MarkMilestoneDone` appends a best-effort, can't-dead-stop entry; three agent prompts gain a read-first advisory; `FinalSpecCheck` allowlists the file. No engine Go changes.

**Tech Stack:** Dippin (`.dip`) DSL, POSIX `sh` (`set -eu`), Go tests (`strings.Contains` on `*pipeline.Graph` node `Attrs`), `dippin` CLI gates.

**Design spec:** `docs/superpowers/specs/2026-06-05-issue-298-build-context-file-design.md`

**Branch:** `fix/298-build-context` (already created).

---

## File Structure

- **Modify:** `examples/build_product.dip` — five edits (Setup seed, PickNextMilestone marker, MarkMilestoneDone append, three prompts, FinalSpecCheck allowlist).
- **Create:** `pipeline/build_product_buildcontext_test.go` — five negative-control tests. Same package (`pipeline`) as `build_product_failure_routing_test.go`, so it reuses the existing `loadBuildProduct` helper.
- **Modify:** `CHANGELOG.md` — `[Unreleased]/Added` entry.

All node line numbers below are anchors from the current file and may drift — match on the quoted anchor text, not the line number.

---

## Task 1: Write all five negative-control tests (RED)

**Files:**
- Create: `pipeline/build_product_buildcontext_test.go`

- [ ] **Step 1: Write the failing test file**

```go
// ABOUTME: Negative-control regression guard for issue #298 — the build-context
// ABOUTME: file wiring (seed / range-marker / append / read-first / allowlist).
package pipeline

import (
	"strings"
	"testing"
)

// Test 1 — Setup seeds .ai/build/build-context.md with a redirect (not a mention).
func TestBuildProductSetupSeedsBuildContext(t *testing.T) {
	g := loadBuildProduct(t)
	cmd := g.Nodes["Setup"].Attrs["tool_command"]
	if !strings.Contains(cmd, "build-context.md") {
		t.Error("Setup does not reference build-context.md (#298)")
	}
	if !strings.Contains(cmd, "> .ai/build/build-context.md") {
		t.Error("Setup has no redirect seeding build-context.md (#298)")
	}
}

// Test 2 — PickNextMilestone writes the range marker via the non-leaking guard.
func TestBuildProductPickNextWritesStartMarker(t *testing.T) {
	g := loadBuildProduct(t)
	cmd := g.Nodes["PickNextMilestone"].Attrs["tool_command"]
	if !strings.Contains(cmd, "> .ai/build/milestone-start-sha") {
		t.Error("PickNextMilestone has no truncating redirect writing milestone-start-sha (#298)")
	}
	// The bare `git rev-parse HEAD || echo ""` leaks the literal "HEAD" to stdout
	// on a commitless repo; --verify --quiet prints nothing and exits non-zero.
	if !strings.Contains(cmd, "rev-parse --verify --quiet") {
		t.Error("PickNextMilestone must capture START via `git rev-parse --verify --quiet` (#298)")
	}
}

// Test 3 — MarkMilestoneDone APPENDS an entry, cleans up the marker, and keeps
// the routing printf last so it survives output truncation.
func TestBuildProductMarkMilestoneDoneAppends(t *testing.T) {
	g := loadBuildProduct(t)
	cmd := g.Nodes["MarkMilestoneDone"].Attrs["tool_command"]
	if !strings.Contains(cmd, "git diff --name-only") {
		t.Error("MarkMilestoneDone does not compute the file list via git diff --name-only (#298)")
	}
	// APPEND (>>), not truncate (>): a truncating redirect would silently
	// discard prior milestones' entries.
	if !strings.Contains(cmd, ">> .ai/build/build-context.md") {
		t.Error("MarkMilestoneDone does not APPEND to build-context.md — per-milestone history would be lost (#298)")
	}
	if !strings.Contains(cmd, "rm -f .ai/build/milestone-start-sha") {
		t.Error("MarkMilestoneDone does not remove the milestone-start-sha marker after use (#298)")
	}
	// The append must precede the terminal routing marker (CLAUDE.md: marker last).
	appendIdx := strings.Index(cmd, ">> .ai/build/build-context.md")
	markerIdx := strings.LastIndex(cmd, `printf "milestone-`)
	if markerIdx == -1 {
		t.Fatal("MarkMilestoneDone lost its terminal printf routing marker (#298 regression)")
	}
	if appendIdx == -1 || appendIdx > markerIdx {
		t.Error("build-context.md append occurs after the terminal printf marker — routing token may be truncated (#298)")
	}
}

// Test 4 — the three build-loop agents read build-context.md first.
func TestBuildProductAgentPromptsReadBuildContextFirst(t *testing.T) {
	g := loadBuildProduct(t)
	for _, id := range []string{"Implement", "FixMilestone", "VerifyMilestone"} {
		if !strings.Contains(g.Nodes[id].Attrs["prompt"], "build-context.md") {
			t.Errorf("%s prompt does not instruct reading build-context.md (#298)", id)
		}
	}
}

// Test 5 — FinalSpecCheck allowlists build-context.md so it isn't flagged.
func TestBuildProductFinalSpecCheckAllowlistsBuildContext(t *testing.T) {
	g := loadBuildProduct(t)
	if !strings.Contains(g.Nodes["FinalSpecCheck"].Attrs["prompt"], "build-context.md") {
		t.Error("FinalSpecCheck does not allowlist build-context.md (#298)")
	}
}
```

- [ ] **Step 2: Run the tests to verify they all FAIL**

Run: `go test ./pipeline/ -run 'TestBuildProduct(SetupSeedsBuildContext|PickNextWritesStartMarker|MarkMilestoneDoneAppends|AgentPromptsReadBuildContextFirst|FinalSpecCheckAllowlistsBuildContext)' -v`
Expected: all five FAIL (substrings absent from the current `.dip`). This is the negative control — confirms each test genuinely exercises the change.

- [ ] **Step 3: Commit the RED tests**

```bash
git add pipeline/build_product_buildcontext_test.go
git commit -m "test(298): negative-control tests for build-context file wiring (RED)

Co-Authored-By: Claude Opus 4.8 (1M context) <noreply@anthropic.com>"
```

---

## Task 2: Setup seeds build-context.md (GREEN test 1)

**Files:**
- Modify: `examples/build_product.dip` — `tool Setup`, immediately before its final `printf 'setup-ready'` (anchor `~:267`).

- [ ] **Step 1: Insert the architecture-map command group**

Find this anchor (the final line of the `Setup` command body):

```
      printf 'setup-ready'
```

Insert the following block **immediately before** it (6-space indentation matches the command body):

```
      # Seed the per-node build-context file (issue #298). Advisory artifact:
      # the whole group is best-effort (|| true) so it can never dead-stop Setup.
      # Each producer pipes to `head` (pipefail is off, so a no-match `git grep`
      # yields the pipeline's `head` exit 0 and does not trip set -e). The map is
      # frozen at Setup and labelled so later-milestone agents down-weight it vs
      # the authoritative milestone log / code. Caps keep the file SHORT (#298 §4).
      {
        echo "# Build Context (machine-written — do not edit by hand)"
        echo
        echo "_Architecture map as of Setup ($(git rev-parse --short --verify --quiet HEAD 2>/dev/null || echo 'no commits')). Packages may change as milestones land; the milestone log below is authoritative for what moved._"
        echo
        echo "## Top-level layout"
        git ls-files 2>/dev/null | awk -F/ 'NF>1{print $1"/"} NF==1{print}' | sort -u | head -40
        echo
        echo "## Languages"
        git ls-files 2>/dev/null | awk -F. 'NF>1{print $NF}' | sort | uniq -c | sort -rn | head -15
        echo
        echo "## Entry points"
        git ls-files 2>/dev/null | grep -E '(^|/)(main\.go|index\.[jt]s|main\.py|__main__\.py|main\.rs|Main\.java)$|(^|/)cmd/' | head -20
        echo
        echo "## Key interfaces (best-effort: Go / TS / Rust)"
        git grep -nE 'type [[:alnum:]_]+ +interface[[:space:]{]|^(export )?(abstract )?(interface|trait) ' -- '*.go' '*.ts' '*.tsx' '*.rs' 2>/dev/null | head -20
        echo
        echo "## Milestones landed"
      } > .ai/build/build-context.md 2>/dev/null || true

```

(`.ai/build` already exists from the `mkdir -p .ai/build …` at the top of Setup.)

- [ ] **Step 2: Run test 1 to verify it passes**

Run: `go test ./pipeline/ -run TestBuildProductSetupSeedsBuildContext -v`
Expected: PASS

- [ ] **Step 3: Commit**

```bash
git add examples/build_product.dip
git commit -m "feat(build_product): Setup seeds .ai/build/build-context.md architecture map (#298)

Co-Authored-By: Claude Opus 4.8 (1M context) <noreply@anthropic.com>"
```

---

## Task 3: PickNextMilestone records the range marker (GREEN test 2)

**Files:**
- Modify: `examples/build_product.dip` — `tool PickNextMilestone`, immediately before its final `printf "milestone-$NEXT"` (anchor `~:394`), which sits after the empty-`current.md` guard.

- [ ] **Step 1: Insert the marker write**

Find this anchor (the final line of the `PickNextMilestone` command body):

```
      printf "milestone-$NEXT"
```

Insert the following block **immediately before** it:

```
      # Record this milestone's start boundary for MarkMilestoneDone's
      # files-touched diff (issue #298). --verify --quiet prints nothing and
      # exits non-zero on a commitless repo, so START is genuinely empty there
      # (a bare `git rev-parse HEAD` would leak the literal "HEAD" to stdout and
      # silently empty milestone 1's file record). Written on the has-next path
      # only — the all-done branch returned earlier. Goes to a FILE so the
      # routing marker below stays last on stdout.
      START=$(git rev-parse --verify --quiet HEAD || true)
      printf '%s\n' "$START" > .ai/build/milestone-start-sha

```

- [ ] **Step 2: Run test 2 to verify it passes**

Run: `go test ./pipeline/ -run TestBuildProductPickNextWritesStartMarker -v`
Expected: PASS

- [ ] **Step 3: Commit**

```bash
git add examples/build_product.dip
git commit -m "feat(build_product): PickNextMilestone records per-milestone commit-range marker (#298)

Co-Authored-By: Claude Opus 4.8 (1M context) <noreply@anthropic.com>"
```

---

## Task 4: MarkMilestoneDone appends one entry per milestone (GREEN test 3)

**Files:**
- Modify: `examples/build_product.dip` — `tool MarkMilestoneDone` body (anchor `~:768-777`).

This node has a single unconditional edge and no `fallback_target`, so any non-zero exit dead-stops a completed milestone. The new block therefore runs in a `( set +e; … ) || true` subshell, sends all bulky output to the file, and leaves the existing `printf` marker last.

- [ ] **Step 1: Insert the append block**

Find this anchor inside `MarkMilestoneDone`:

```
      cp .ai/milestones/current.md "$DONE_DIR/milestone-$NEXT.md"
      # Reset fix attempt counter for next milestone
```

Replace it with (insert the append block between the `cp` and the `# Reset` comment, so `current.md` is still present when read):

```
      cp .ai/milestones/current.md "$DONE_DIR/milestone-$NEXT.md"

      # Append this milestone's entry to the build-context file (issue #298).
      # MarkMilestoneDone is a strict-failure tool node (one unconditional edge,
      # no fallback_target), so a logging hiccup must NOT dead-stop a verified
      # milestone: the whole block runs in a `set +e` subshell that cannot
      # propagate a non-zero exit, all bulk output goes to the FILE, and the
      # terminal `printf` marker below stays last on stdout.
      (
        set +e
        # Two-dot range from this milestone's start to HEAD. Degrade BASE to the
        # empty tree when START is empty (first milestone / absent marker) OR
        # unreachable (a retry rewrote/orphaned it). Three-dot (...) would fatal
        # on the empty-tree object and under-report on rewritten history.
        START=$(cat .ai/build/milestone-start-sha 2>/dev/null || true)
        if [ -z "$START" ] || ! git cat-file -e "${START}^{commit}" 2>/dev/null; then
          BASE=$(git hash-object -t tree /dev/null)
        else
          BASE="$START"
        fi
        TITLE=$(head -1 .ai/milestones/current.md 2>/dev/null)
        [ -n "$TITLE" ] || TITLE="## Milestone $NEXT"
        FILES=$(git diff --name-only "${BASE}..HEAD" 2>/dev/null | sed '/^$/d')
        SUMMARY=$(git log -1 --format=%s "${BASE}..HEAD" 2>/dev/null)
        [ -n "$SUMMARY" ] || SUMMARY="$TITLE"
        NFILES=$(printf '%s\n' "$FILES" | grep -c . || true)
        {
          echo
          echo "$TITLE"
          if [ "$NFILES" -eq 0 ]; then
            echo "Files: (none)"
          else
            printf 'Files: %s\n' "$(printf '%s\n' "$FILES" | head -12 | paste -sd, -)"
            [ "$NFILES" -gt 12 ] && echo "Files: … and $((NFILES - 12)) more"
          fi
          echo "Summary: $SUMMARY"
        } >> .ai/build/build-context.md
      ) 2>/dev/null || true
      rm -f .ai/build/milestone-start-sha

      # Reset fix attempt counter for next milestone
```

- [ ] **Step 2: Run test 3 to verify it passes**

Run: `go test ./pipeline/ -run TestBuildProductMarkMilestoneDoneAppends -v`
Expected: PASS (asserts `git diff --name-only`, `>> .ai/build/build-context.md`, `rm -f …milestone-start-sha`, and append-before-marker ordering).

- [ ] **Step 3: Commit**

```bash
git add examples/build_product.dip
git commit -m "feat(build_product): MarkMilestoneDone appends per-milestone build-context entry (#298)

Best-effort ( set +e; ... ) || true so it can't dead-stop a verified milestone;
two-dot diff range with empty-tree/unreachable-START guards; routing marker last.

Co-Authored-By: Claude Opus 4.8 (1M context) <noreply@anthropic.com>"
```

---

## Task 5: Three agent prompts read build-context first (GREEN test 4)

**Files:**
- Modify: `examples/build_product.dip` — `agent Implement` (`~:405`), `agent VerifyMilestone` (`~:585`), `agent FixMilestone` (`~:790`).

The advisory block is identical for all three. Prepend it as the first lines of each prompt.

- [ ] **Step 1: Prepend the advisory to `Implement`**

Find:

```
    prompt:
      Read the current milestone spec at .ai/milestones/current.md.
      Read the full spec at SPEC.md for context.
```

Replace with:

```
    prompt:
      FIRST, in one turn, read .ai/build/build-context.md for orientation — it
      holds an architecture map and a one-entry-per-milestone log (files touched
      + commit summary) so you don't rediscover the layout cold. It is ADVISORY
      and may lag the latest code: SPEC.md and the source are authoritative — if
      they disagree with the context file, trust them. Prefer the milestone's own
      file list (.ai/milestones/current.md) plus this context file over re-reading
      SPEC.md in full; re-read only the spec portions relevant to this milestone's
      files.

      Read the current milestone spec at .ai/milestones/current.md.
      Read the full spec at SPEC.md for context.
```

- [ ] **Step 2: Prepend the advisory to `VerifyMilestone`**

Find:

```
    prompt:
      Read the current milestone spec at .ai/milestones/current.md.
      Read SPEC.md (issue #233 Gap 4). Pre-#233 this verifier only read
```

Replace with:

```
    prompt:
      FIRST, in one turn, read .ai/build/build-context.md for orientation — it
      holds an architecture map and a one-entry-per-milestone log (files touched
      + commit summary) so you don't rediscover the layout cold. It is ADVISORY
      and may lag the latest code: SPEC.md and the source are authoritative — if
      they disagree with the context file, trust them. Prefer the milestone's own
      file list (.ai/milestones/current.md) plus this context file over re-reading
      SPEC.md in full; re-read only the spec portions relevant to this milestone's
      files.

      Read the current milestone spec at .ai/milestones/current.md.
      Read SPEC.md (issue #233 Gap 4). Pre-#233 this verifier only read
```

- [ ] **Step 3: Prepend the advisory to `FixMilestone`**

Find:

```
    prompt:
      The current milestone failed verification. Read:
      - The milestone spec: .ai/milestones/current.md
```

Replace with:

```
    prompt:
      FIRST, in one turn, read .ai/build/build-context.md for orientation — it
      holds an architecture map and a one-entry-per-milestone log (files touched
      + commit summary) so you don't rediscover the layout cold. It is ADVISORY
      and may lag the latest code: SPEC.md and the source are authoritative — if
      they disagree with the context file, trust them. Prefer the milestone's own
      file list (.ai/milestones/current.md) plus this context file over re-reading
      SPEC.md in full; re-read only the spec portions relevant to this milestone's
      files.

      The current milestone failed verification. Read:
      - The milestone spec: .ai/milestones/current.md
```

- [ ] **Step 4: Run test 4 to verify it passes**

Run: `go test ./pipeline/ -run TestBuildProductAgentPromptsReadBuildContextFirst -v`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add examples/build_product.dip
git commit -m "feat(build_product): Implement/Fix/Verify read build-context.md first (#298)

Co-Authored-By: Claude Opus 4.8 (1M context) <noreply@anthropic.com>"
```

---

## Task 6: FinalSpecCheck allowlists build-context.md (GREEN test 5)

**Files:**
- Modify: `examples/build_product.dip` — `agent FinalSpecCheck` leftover-allowlist prose (anchor `~:1256-1262`).

- [ ] **Step 1: Add the allowlist entry**

Find:

```
      - No UNEXPECTED leftover files in .ai/build/ — the workflow
        intentionally writes helpers (ci-probe.sh from PR #246,
        iface-reachability-rubric.md from PR #254), the re-review
        budget counter (review_fix_attempts from Gap 5.3 of #233),
        and reviewers write reports (review-claude.md,
        review-codex.md, review-gemini.md). Only flag files outside
        this explicit allowlist.
```

Replace with:

```
      - No UNEXPECTED leftover files in .ai/build/ — the workflow
        intentionally writes helpers (ci-probe.sh from PR #246,
        iface-reachability-rubric.md from PR #254), the re-review
        budget counter (review_fix_attempts from Gap 5.3 of #233),
        the per-node build-context file (build-context.md from #298),
        and reviewers write reports (review-claude.md,
        review-codex.md, review-gemini.md). Only flag files outside
        this explicit allowlist.
```

- [ ] **Step 2: Run test 5 to verify it passes**

Run: `go test ./pipeline/ -run TestBuildProductFinalSpecCheckAllowlistsBuildContext -v`
Expected: PASS

- [ ] **Step 3: Commit**

```bash
git add examples/build_product.dip
git commit -m "feat(build_product): FinalSpecCheck allowlists build-context.md (#298)

Co-Authored-By: Claude Opus 4.8 (1M context) <noreply@anthropic.com>"
```

---

## Task 7: Full verification gates

**Files:** none (verification only), plus `CHANGELOG.md`.

- [ ] **Step 1: Full Go build + short tests**

Run: `go build ./... && go test ./... -short`
Expected: PASS. In particular the existing `pipeline/build_product_failure_routing_test.go` (the #296/#297/#303 regression pins) stays green — no edges were touched.

- [ ] **Step 2: dippin validate**

Run: `dippin validate examples/build_product.dip`
Expected: passes (no errors). If `dippin` is not on PATH, STOP and ask the user — they install it from a local checkout; do NOT `go install` it (CLAUDE.md).

- [ ] **Step 3: dippin doctor (A grade across the board)**

Run: `dippin doctor examples/build_product.dip examples/ask_and_execute.dip examples/build_product_with_superspec.dip`
Expected: A grade for each; `build_product` ≥ baseline A 90/100.

- [ ] **Step 4: dippin simulate all paths**

Run: `dippin simulate -all-paths examples/build_product.dip`
Expected: 100% terminate success, no new cycle / dead-stop (topology is byte-identical — no edges added).

- [ ] **Step 5: Update CHANGELOG.md**

Add under `[Unreleased]` → `### Added` (create the heading if absent):

```markdown
- **build_product: per-node build-context file (#298).** `Setup` seeds a short,
  machine-written `.ai/build/build-context.md` (architecture map + milestone log);
  `MarkMilestoneDone` appends one files-touched entry per milestone (via a
  per-milestone commit-range marker); `Implement`/`FixMilestone`/`VerifyMilestone`
  read it first to stop rediscovering the layout cold. Completes epic #308 Phase 1
  ("Never lose work, never overrun blindly").
```

- [ ] **Step 6: Commit**

```bash
git add CHANGELOG.md
git commit -m "docs(298): CHANGELOG entry for build-context file; completes #308 Phase 1

Co-Authored-By: Claude Opus 4.8 (1M context) <noreply@anthropic.com>"
```

---

## Task 8: Adversarial self-review + open PR

**Files:** none (review + PR).

- [ ] **Step 1: Adversarial self-review of the four risk paths**

Re-read the diff (`git diff main...HEAD -- examples/build_product.dip`) and confirm each by inspection:
- **Empty-first-milestone:** `START` empty → `cat-file -e` false → `BASE` = empty tree → `git diff --name-only <empty-tree>..HEAD` lists all of milestone 1's files (two-dot, valid). Summary non-empty (newest commit subject) or degrades to title.
- **Commit-range across retries:** `EscalateMilestone → Implement` bypasses `PickNextMilestone`; the marker persists, so `BASE..HEAD` spans all retry commits.
- **Strict-failure safety:** every new command in `MarkMilestoneDone` is inside `( set +e; … ) || true`; the terminal `printf "milestone-$NEXT-complete"` is the last stdout line; `rm -f` can't fail the node.
- **Routing unchanged:** `git diff main...HEAD -- examples/build_product.dip` shows no change inside the `edges` block; `go test ./pipeline/ -run TestBuildProductIssue296FailureRoutes` and `…CommitIfDirtyCheckpoint` and `…Issue303GreenBreachRescuePath` all pass.

- [ ] **Step 2: Push the branch**

```bash
git push -u origin fix/298-build-context
```

- [ ] **Step 3: Open the PR**

```bash
gh pr create --title "feat(build_product): per-node build-context file to cure rediscovery amnesia (#298)" --body "$(cat <<'EOF'
Closes #298. Refs epic #308 Phase 1 ("Never lose work, never overrun blindly") — this is the last Phase-1 item; builds on merged #297/#302/#303.

## What
A short, machine-written, append-only `.ai/build/build-context.md`:
- **Setup** seeds an architecture map (author-controlled greps, capped) + a `## Milestones landed` header, via a redirected command group.
- **PickNextMilestone** records a per-milestone commit-range marker (`git rev-parse --verify --quiet HEAD` → `.ai/build/milestone-start-sha`).
- **MarkMilestoneDone** appends one entry per milestone: title, files touched (`git diff --name-only <start>..HEAD`, capped), newest commit subject — best-effort so it can never dead-stop a verified milestone.
- **Implement / FixMilestone / VerifyMilestone** read it first (advisory; SPEC.md and code remain authoritative).
- **FinalSpecCheck** allowlists it.

No engine Go changes; no edges added.

## Design subtleties (deliberately handled)
1. **Commit-range boundary:** on-disk marker (mirrors the `fix_attempts` pattern), pinned at milestone start, survives retries; two-dot range with an empty-tree/unreachable-START guard.
2. **Machine-written, not LLM-written:** deterministic shell (greps + git), can't hallucinate.
3. **Lifecycle:** `.ai/` is gitignored, so the file is never committed into the product tree and is wiped by Cleanup; allowlisted in FinalSpecCheck.
4. **Token budget:** SHORT and append-only — arch map capped; ≤12 files/entry + "… and N more"; subject-only summary.

## Strict-failure safety
`MarkMilestoneDone` has one unconditional edge and no `fallback_target`, so any non-zero exit would dead-stop an already-verified milestone. The new append runs in a `( set +e; … ) || true` subshell with the routing `printf` last.

## Tests
`pipeline/build_product_buildcontext_test.go` — five node-scoped negative-control assertions, each proven RED on the pre-#298 graph. The existing `build_product_failure_routing_test.go` pins (#296/#297/#303) stay green (no edges touched).

## Verification
- `go build ./... && go test ./... -short` ✅
- `dippin validate` ✅ · `dippin doctor` A across the board ✅ · `dippin simulate -all-paths` 100% terminate ✅

Design + squad review: `docs/superpowers/specs/2026-06-05-issue-298-build-context-file-design.md`.

🤖 Generated with [Claude Code](https://claude.com/claude-code)
EOF
)"
```

- [ ] **Step 4: Address bot review (CodeRabbit / Copilot / Codex)**

For each comment: verify against the actual code, fix valid ones, decline invalid ones with a technical rationale, reply in-thread. Keep the PR body + CHANGELOG in sync and CI green. Use `superpowers:receiving-code-review` discipline (verify before implementing; no performative agreement).

---

## Self-Review (plan vs. spec)

**Spec coverage:** Setup seed → Task 2; PickNextMilestone marker → Task 3; MarkMilestoneDone append (best-effort, two-dot, guards, caps, marker cleanup) → Task 4; three prompts → Task 5; allowlist → Task 6; tests → Task 1; gates + CHANGELOG → Task 7; self-review + PR → Task 8. All spec sections mapped.

**Placeholder scan:** none — every step has exact anchors, full code, and exact commands.

**Type/string consistency:** the test substrings exactly match the inserted `.dip` text — `> .ai/build/build-context.md` (Task 2), `> .ai/build/milestone-start-sha` + `rev-parse --verify --quiet` (Task 3), `git diff --name-only` + `>> .ai/build/build-context.md` + `rm -f .ai/build/milestone-start-sha` + terminal `printf "milestone-` (Task 4), `build-context.md` in the three prompts (Task 5) and FinalSpecCheck (Task 6).
