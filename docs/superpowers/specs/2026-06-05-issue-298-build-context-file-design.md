# Issue #298 — Cure per-node amnesia with a machine-written build-context file

**Status:** design approved (post squad-review), pre-implementation
**Branch:** `fix/298-build-context`
**Epic:** #308 Phase 1 ("Never lose work, never overrun blindly") — last remaining item; builds on merged #295/#296/#302/#297/#303-PR1.
**Scope:** `.dip` edits to `examples/build_product.dip` + negative-control tests. **No engine Go changes.**

## Problem

Every agent node in the build loop (`Implement`, `FixMilestone`, `VerifyMilestone`) re-reads `SPEC.md` and re-greps the tree cold each milestone. On case-study run `7b6e08c9e2b2` the exhausted `Implement` node spent ~10 of 50 turns re-orienting before writing code — on the final milestone, the difference between finishing and turn-exhaustion.

## Solution overview

A short, append-only `.ai/build/build-context.md`:
- **Seeded by `Setup`** with a machine-written architecture map (author-controlled greps) + a `## Milestones landed` header.
- **Appended by `MarkMilestoneDone`**, one machine-written entry per completed milestone: number/title, files touched (`git diff --name-only` over the milestone's commit range), one-line summary (the milestone's newest commit subject).
- **Read first** by `Implement`/`FixMilestone`/`VerifyMilestone` for orientation.
- **Allowlisted** in `FinalSpecCheck` so it isn't flagged as a leftover.

The file is tracker scratch under `.ai/` (gitignored by `Setup`), so it is never committed into the product tree and is wiped by `Cleanup`.

## Why machine-written, not LLM-written

The per-milestone entry is a deterministic tool-node shell append (greps + git), never an agent prompt, so it cannot hallucinate (CLAUDE.md tool-node safety). All inputs are author-controlled or raw git output.

---

## The commit-range mechanism (design subtlety #1)

`MarkMilestoneDone` needs to know where **this** milestone started. We use an on-disk marker file, mirroring the existing `.ai/milestones/fix_attempts` circuit-breaker pattern (CLAUDE.md: "use a `fix_attempts` file on disk as in `build_product.dip`").

- **`PickNextMilestone`** captures `START = git rev-parse --verify --quiet HEAD` into `.ai/build/milestone-start-sha`, on the has-next path only (the `all-done` branch returns earlier).
- **`MarkMilestoneDone`** reads it, computes `git diff --name-only ${BASE}..HEAD`, appends the entry, then `rm -f`s the marker.

### Boundary correctness across paths
- **Normal:** `START` = previous milestone's tip; at `MarkMilestoneDone`, `HEAD` = this milestone's tip → range = exactly this milestone's commits.
- **Retry** (`EscalateMilestone → Implement`, restart): bypasses `PickNextMilestone`, so the marker stays pinned at the original milestone start — the range correctly spans all retry commits too. (Confirmed by git reviewer reproduction.)
- **"mark done"** (`EscalateMilestone → MarkMilestoneDone`): marker still on disk from this milestone's `PickNextMilestone`; if the milestone made no commits, the range is empty → degrade (below).
- **Stale-marker safety:** after the final milestone, `PickNextMilestone` takes the `all-done` branch and writes no marker; the last marker was `rm`'d by its `MarkMilestoneDone`. `Cleanup` (`rm -rf .ai/build`) wipes any residue from the `accept` path. The marker never reaches `FinalSpecCheck`, so it needs no allowlist entry.

### Critical git correctness (from squad review)
1. **`git rev-parse --verify --quiet HEAD || true`** — a bare `git rev-parse HEAD 2>/dev/null || echo ""` leaks the literal string `HEAD` to stdout on a commitless repo, making `START="HEAD"` (not empty) and silently emptying milestone 1's file list. `--verify --quiet` prints nothing and exits non-zero when HEAD is unresolvable.
2. **Two-dot range `${BASE}..HEAD`** — three-dot `...` *fatals* on the empty-tree object and under-reports on rewritten history. Two-dot's tree-to-tree semantics is correct and safest for "what did this milestone touch."
3. **Reachability guard** — degrade `BASE` to the empty tree (`git hash-object -t tree /dev/null`) when `START` is empty **or unreachable** (`! git cat-file -e "${START}^{commit}"`), covering: first milestone (no prior commit), absent marker (`PickNextMilestone → EscalateMilestone → "mark done"`), and a retry that `git reset`/orphaned the START commit.
4. **Empty-range summary** — `git log -1 --format=%s` over an empty range prints nothing; fall back to the milestone title.

---

## Strict-failure safety (design subtlety #2 — the load-bearing constraint)

`MarkMilestoneDone` has a single **unconditional** outgoing edge (`-> PickNextMilestone restart:true`) and **no `fallback_target`**. By the engine's strict-failure rule, any non-zero exit **dead-stops the pipeline on an already-verified-green milestone** — the worst place to halt. The node runs under `set -eu`, and the new git/append commands have many non-zero-exit surfaces.

**Therefore the entire new block runs best-effort and cannot propagate failure:**

```sh
( set +e
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
      [ "$NFILES" -gt 12 ] && echo "Files: … and $((NFILES-12)) more"
    fi
    echo "Summary: $SUMMARY"
  } >> .ai/build/build-context.md
) 2>/dev/null || true
rm -f .ai/build/milestone-start-sha
```

This block is inserted **after** the existing `cp .ai/milestones/current.md "$DONE_DIR/..."` and **before** the existing `rm .ai/milestones/current.md` (so `head -1 current.md` reads a live file). The existing terminal `printf "milestone-$NEXT-complete"` stays the **last** line on stdout — all bulky diff output goes to the file, never stdout, so the routing marker survives truncation.

This degrade-don't-fail posture does **not** violate "never silently swallow errors": that rule targets correctness gates (provider/auth/SSE errors, empty agent responses), not a cosmetic advisory-log append on the verified-success path. Contrast `PickNextMilestone`'s `current.md` extraction, which correctly `exit 1`s loudly because an empty milestone *is* a correctness failure.

---

## The architecture-map seed (Setup) — command group, NOT heredoc

The existing `Setup` heredocs use **quoted** delimiters to write file *content* literally (the rubric greps are documentation for an LLM to run later). The build-context map is the **opposite**: the greps must **run at Setup time and their stdout captured**. So it is a **redirected command group**, appended before the existing `printf 'setup-ready'`:

```sh
# Seed the per-node build-context file (issue #298). Advisory artifact:
# the whole group is best-effort (|| true) so it can never dead-stop Setup.
# Each producer pipes to `head` (pipefail is off → pipeline exit is head's
# 0, so a no-match `git grep` does not trip set -e). Caps keep the file
# SHORT (subtlety #4). The map is frozen at Setup and explicitly labelled
# so a later-milestone agent down-weights it vs the authoritative log/code.
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

`.ai/build` already exists (Setup `mkdir -p .ai/build …` at the top). The `printf 'setup-ready'` routing marker remains last.

---

## Prompt edits — read-first, advisory framing

Prepended to `Implement`, `FixMilestone`, and `VerifyMilestone` (before their existing first instruction). Worded to be **followable** and to keep SPEC.md/code authoritative (squad: the original "only re-grep sections this milestone touches" is unfollowable — `current.md` lists files, not spec anchors; fixing that properly means a `Decompose` `Spec sections:` line, which is #300 territory and out of scope):

```
FIRST, in one turn, read .ai/build/build-context.md for orientation — it
holds an architecture map and a one-entry-per-milestone log (files touched
+ commit summary) so you don't rediscover the layout cold. It is ADVISORY
and may lag the latest code: SPEC.md and the source are authoritative — if
they disagree with the context file, trust them. Prefer the milestone's own
file list (.ai/milestones/current.md) plus this context file over re-reading
SPEC.md in full; re-read only the spec portions relevant to this milestone's
files.
```

---

## Allowlist edit — FinalSpecCheck

Add `build-context.md` to the enumerated `.ai/build/` leftover allowlist prose (currently lists `ci-probe.sh`, `iface-reachability-rubric.md`, `review_fix_attempts`, `review-{claude,codex,gemini}.md`). The marker file is consumed each milestone and never reaches `FinalSpecCheck`, so it needs no entry.

---

## Lifecycle / safety (design subtlety #3)

`.ai/` is gitignored by `Setup` before the first `PickNextMilestone`, so:
- `git diff --name-only` and `git log` never list `.ai/build/build-context.md` or the marker (no self-reference noise).
- `CommitIfDirty`'s `git add -A` never stages them (never committed into the product tree).
- `Cleanup` (`rm -rf .ai/build .ai/milestones`) wipes the file. It does **not** persist into `.ai/decisions/` for post-run inspection — acceptable; it's build-loop scratch, not a decision record.

## Token budget (design subtlety #4)

The file stays SHORT and append-only: arch map is one-time and capped (`head -40/15/20/20`); each milestone entry is title + ≤12 files (then "… and N more") + one subject line. Honest accounting (squad): three of four per-milestone fields duplicate `done/` + git; the genuine value is the **architecture map** + **one-read collation** (one read replaces `ls done/ && cat done/*.md && git log`). The caps protect that thin margin.

---

## Test plan — `pipeline/build_product_buildcontext_test.go`

String-content assertions on node `Attrs`, **node-scoped** (never a whole-file grep), each proven RED on the pre-#298 graph. Reuses `loadBuildProduct`/`hasEdgeTo` helpers from `build_product_failure_routing_test.go` (same package). Negative controls verified valid: `build-context.md` and `milestone-start-sha` occur **0** times today; `git diff --name-only` occurs once but only in **VerifyMilestone's prompt** (a different node/attr than the `MarkMilestoneDone.tool_command` test 3 asserts on).

1. **Setup seeds it** — `Setup.Attrs["tool_command"]` contains `build-context.md` AND `> .ai/build/build-context.md` (redirect, not just a mention).
2. **PickNextMilestone writes the marker** — `PickNextMilestone.Attrs["tool_command"]` contains `> .ai/build/milestone-start-sha` (truncating redirect) AND `rev-parse --verify --quiet` (the correct, non-leaking guard).
3. **MarkMilestoneDone appends per milestone** — `MarkMilestoneDone.Attrs["tool_command"]` contains `git diff --name-only` AND `>> .ai/build/build-context.md` (**append**, not `>` truncate — pins the highest-value bug: a truncating redirect would discard prior milestones). Plus a **marker-position guard**: `LastIndex(cmd, 'printf "milestone-')` > `Index(cmd, ">> .ai/build/build-context.md")` (append precedes the terminal routing marker). Plus a **cleanup pin**: contains `rm` … `milestone-start-sha`.
4. **Prompts read it first** — `Implement`, `FixMilestone`, `VerifyMilestone` `.Attrs["prompt"]` each contain `build-context.md` (plain `Contains` — prompts are free text, no operative token).
5. **Allowlist** — `FinalSpecCheck.Attrs["prompt"]` contains `build-context.md`.
6. **Regression pins (unchanged):** `build_product_failure_routing_test.go` already pins #296/#297/#303 routing; no edges are added, so those stay green. (`#296` Implement success→CommitIfDirty + fallback to EscalateMilestone; `#303` green-breach rescue.)

Out-of-scope-for-string-tests (squad): the empty-tree/first-milestone runtime behavior is shell-execution, not graph content — covered by `dippin simulate -all-paths` (topology) + `go test`; a temp-repo integration test is deferred (noted as a known untested runtime edge, not blocking this wiring PR).

## Verification gates

- `dippin validate examples/build_product.dip` — passes.
- `dippin doctor examples/build_product.dip examples/ask_and_execute.dip examples/build_product_with_superspec.dip` — A across the board (build_product baseline A 90/100).
- `dippin simulate -all-paths examples/build_product.dip` — 100% terminate success, no new cycle / dead-stop.
- `go build ./... && go test ./... -short` — green; each new test proven RED before its wiring.
- `CHANGELOG.md` `[Unreleased]/Added` — per-node build-context file curing rediscovery amnesia; completes epic #308 Phase 1.

## Out of scope (separate issues — do not touch)

#299 quality-gate, #300/#301 spec coherence (incl. a `Decompose` `Spec sections:` line), #304 cost/no-progress, #305 polyglot, #306 contract artifacts, #313 parallel strict-failure, the orphaned #303-PR2 operator node, and the `summary:medium` fidelity lever (measure-first optimization).
