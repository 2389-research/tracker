# Complexity Gate Ratchet Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Make `make ci`'s complexity gate real, green-today, and CI-enforced — exclude worktrees, pin the analyzers, grandfather the current baseline via an identity allowlist that can only shrink, and wire it into `ci.yml` (#468).

**Architecture:** A single bash script `scripts/complexity/gate.sh` scans production Go for gocyclo/gocognit/file-size violations, normalizes them to a line-insensitive `metric|file|func|value` form, and compares against a checked-in `scripts/complexity/baseline.txt`, failing only on a violation that is NEW (identity absent from the baseline) or WORSE (value higher than baselined). `make complexity`, `make complexity-update`, the CI job, and the baseline-may-only-shrink check all call this one script.

**Tech Stack:** bash + awk; `go run` pinned gocyclo `v0.6.0` / gocognit `v1.2.1`; GitHub Actions; GNU Make.

## Global Constraints

- Limits are **kept strict and unchanged**: cyclo ≤ 8, cognitive ≤ 8, file ≤ 500 lines. Non-weakening (#468). The baseline grandfathers current debt; it must only shrink.
- All scans exclude `./.worktrees/*`, `./.claude/*`, `./vendor/*`, `*_test.go`, `./cmd/tracker-conformance/*` — from **one** `list_files()` in `gate.sh` (single source of truth).
- Analyzers are pinned and invoked via `go run` (no PATH dependency): `github.com/fzipp/gocyclo/cmd/gocyclo@v0.6.0`, `github.com/uudashr/gocognit/cmd/gocognit@v1.2.1`.
- This makes the gate green + enforced + burning-down. It does **NOT** decompose the ~86/108/29 baseline (ongoing work; issues #452/#453/#450/#449…).
- **Never `git commit --no-verify` and never `git commit --amend`.** Pre-commit hook ~2–4 min; allow up to 6 minutes per commit; on timeout run `git log --oneline -1` before retrying, then a plain `git commit`.
- `gocyclo`/`gocognit` both print: `<value> <pkg> <func> <file>:<line>:<col>` (4 space-separated fields; `<func>` is a single token, e.g. `(*Engine).Method`).

---

### Task 1: `scripts/complexity/gate.sh` — scan + ratchet compare, with fixture tests

**Files:**
- Create: `scripts/complexity/gate.sh`
- Test: `scripts/complexity/gate_test.sh`

**Interfaces:**
- Produces: `gate.sh` subcommands — `scan` (print normalized current violations), `check <baseline> <current>` (compare two files; exit 1 on new/worse), `gate` (scan + check vs `$BASELINE`), `update` (scan → `$BASELINE`), `baseline-shrinks <ref>` (committed `$BASELINE` must be no worse than `<ref>`'s version). Normalized line form: `metric|file|func|value`, `metric ∈ {cyclo,cognitive,filesize}`.

- [ ] **Step 1: Write the failing test** — create `scripts/complexity/gate_test.sh`:

```bash
#!/usr/bin/env bash
# ABOUTME: Fixture tests for the complexity ratchet's compare logic (gate.sh check).
set -uo pipefail
cd "$(dirname "$0")"
GATE=./gate.sh
fails=0
check() { # desc expected_exit baseline_content current_content
  local desc="$1" want="$2"; shift 2
  local b c; b=$(mktemp); c=$(mktemp)
  printf '%s\n' "$3" > "$b"; printf '%s\n' "$4" > "$c"
  bash "$GATE" check "$b" "$c" >/dev/null 2>&1; local got=$?
  if [ "$got" != "$want" ]; then echo "FAIL: $desc (want exit $want, got $got)"; fails=$((fails+1)); else echo "ok: $desc"; fi
  rm -f "$b" "$c"
}
BASE=$'cyclo|./pipeline/a.go|Foo|12\ncognitive|./pipeline/a.go|Bar|20\nfilesize|./x.go|-|700'
check "subset passes"        0 "$BASE" "$BASE" "cyclo|./pipeline/a.go|Foo|12"
check "improved-but-over passes" 0 "$BASE" "cyclo|./pipeline/a.go|Foo|10"
check "new violation fails"  1 "$BASE" "cyclo|./pipeline/b.go|New|9"
check "worse value fails"    1 "$BASE" "cyclo|./pipeline/a.go|Foo|13"
check "grown file fails"     1 "$BASE" "filesize|./x.go|-|900"
check "empty current passes" 0 "$BASE" ""
[ "$fails" -eq 0 ] && { echo "ALL PASS"; exit 0; } || { echo "$fails FAILED"; exit 1; }
```
Make both scripts executable when created (`chmod +x`).

- [ ] **Step 2: Run the test to verify it fails**

Run: `bash scripts/complexity/gate_test.sh`
Expected: fails (gate.sh doesn't exist yet).

- [ ] **Step 3: Create `scripts/complexity/gate.sh`:**

```bash
#!/usr/bin/env bash
# ABOUTME: Complexity ratchet — grandfathers a baseline of gocyclo/gocognit/file-size
# ABOUTME: violations that may only shrink; fails on NEW or WORSE debt (#468).
set -euo pipefail

CYCLO_MAX="${CYCLO_MAX:-8}"
COGNITIVE_MAX="${COGNITIVE_MAX:-8}"
FILE_MAX_LINES="${FILE_MAX_LINES:-500}"
GOCYCLO_VERSION="${GOCYCLO_VERSION:-v0.6.0}"
GOCOGNIT_VERSION="${GOCOGNIT_VERSION:-v1.2.1}"
BASELINE="${BASELINE:-scripts/complexity/baseline.txt}"

# The one source of truth for what gets scanned: production Go only, excluding
# tests, vendored code, generated worktrees, and the research conformance harness.
list_files() {
  find . -name '*.go' \
    -not -name '*_test.go' \
    -not -path './vendor/*' \
    -not -path './.worktrees/*' \
    -not -path './.claude/*' \
    -not -path './cmd/tracker-conformance/*' | sort
}

# Emit current violations, normalized to metric|file|func|value (line-insensitive:
# file path only, no :line:col, so entries survive ordinary edits).
scan() {
  local files; files=$(list_files)
  printf '%s\n' "$files" | xargs go run "github.com/fzipp/gocyclo/cmd/gocyclo@${GOCYCLO_VERSION}" -over "$CYCLO_MAX" 2>/dev/null \
    | awk '{ split($4,a,":"); print "cyclo|" a[1] "|" $3 "|" $1 }'
  printf '%s\n' "$files" | xargs go run "github.com/uudashr/gocognit/cmd/gocognit@${GOCOGNIT_VERSION}" -over "$COGNITIVE_MAX" 2>/dev/null \
    | awk '{ split($4,a,":"); print "cognitive|" a[1] "|" $3 "|" $1 }'
  printf '%s\n' "$files" | while IFS= read -r f; do
    n=$(wc -l < "$f" | tr -d ' ')
    if [ "$n" -gt "$FILE_MAX_LINES" ]; then printf 'filesize|%s|-|%s\n' "$f" "$n"; fi
  done
}

# compare <baseline-file> <current-file>: prints and exits 1 if any CURRENT entry
# is absent from BASELINE (NEW) or exceeds its baselined value (WORSE). Entries in
# BASELINE but not CURRENT (fixed / improved below threshold) are fine.
compare() {
  awk -F'|' '
    NR==FNR { base[$1"|"$2"|"$3]=$4; next }
    {
      key=$1"|"$2"|"$3; val=$4+0
      if (!(key in base)) { print "  NEW    " $0; bad=1 }
      else if (val > base[key]+0) { print "  WORSE  " $0 "  (baseline " base[key] ")"; bad=1 }
    }
    END { exit bad?1:0 }
  ' "$1" "$2"
}

cmd="${1:-gate}"
case "$cmd" in
  scan)   scan | sort ;;
  update) scan | sort > "$BASELINE"; echo "wrote $BASELINE ($(wc -l < "$BASELINE" | tr -d ' ') grandfathered violations)" ;;
  check)  if compare "$2" "$3"; then echo "complexity gate OK: no new or worsened violations"; else
            echo "FAIL: new/worsened complexity or file-size violations above the grandfathered baseline."; echo "Fix them, or if a legitimate decomposition LOWERED a value, run 'make complexity-update' and commit the shrunk baseline."; exit 1; fi ;;
  gate)   tmp=$(mktemp); scan | sort > "$tmp"; rc=0; compare "$BASELINE" "$tmp" || rc=$?
          if [ "$rc" -eq 0 ]; then echo "complexity gate OK ($(wc -l < "$BASELINE" | tr -d ' ') grandfathered, 0 new)"; else
            echo "FAIL: new/worsened violations above the grandfathered baseline (see above)."; fi
          rm -f "$tmp"; exit "$rc" ;;
  baseline-shrinks) base=$(mktemp); git show "$2:$BASELINE" > "$base" 2>/dev/null || : > "$base"; rc=0; compare "$base" "$BASELINE" || rc=$?
          if [ "$rc" -eq 0 ]; then echo "baseline OK: did not grow vs $2"; else
            echo "FAIL: $BASELINE grew (new or raised entries) vs $2 — the baseline may only shrink."; fi
          rm -f "$base"; exit "$rc" ;;
  *) echo "usage: gate.sh [scan|update|gate|check <baseline> <current>|baseline-shrinks <ref>]"; exit 2 ;;
esac
```

- [ ] **Step 4: Make executable + run the test to verify it passes**

Run: `chmod +x scripts/complexity/gate.sh scripts/complexity/gate_test.sh && bash scripts/complexity/gate_test.sh`
Expected: `ALL PASS` (all 6 cases).

- [ ] **Step 5: Commit**

```bash
git add scripts/complexity/gate.sh scripts/complexity/gate_test.sh
git commit -m "feat(ci): complexity ratchet gate script + fixture tests (#468)

Co-Authored-By: Claude Opus 4.8 (1M context) <noreply@anthropic.com>
Claude-Session: https://claude.ai/code/session_012U14Lj3xBwjmSLJAx6jqbr"
```

---

### Task 2: Generate the baseline + wire the Makefile (green today)

**Files:**
- Create: `scripts/complexity/baseline.txt` (generated)
- Modify: `Makefile` (vars ~L16-18; `complexity:` target ~L69; add `complexity-update:`; `complexity-report:` exclusions ~L114-128)

**Interfaces:**
- Consumes: `scripts/complexity/gate.sh` (Task 1).
- Produces: `make complexity` runs the ratchet (green on current main); `make complexity-update` regenerates the baseline.

- [ ] **Step 1: Generate the baseline from current main**

Run: `bash scripts/complexity/gate.sh update`
Expected: writes `scripts/complexity/baseline.txt` with ~200+ grandfathered lines (the current cyclo/cognitive/filesize violations). Sanity-check: `wc -l scripts/complexity/baseline.txt` shows a few hundred; `grep -c '^filesize' scripts/complexity/baseline.txt` ≈ 29.

- [ ] **Step 2: Verify the gate is green against the fresh baseline**

Run: `bash scripts/complexity/gate.sh gate`
Expected: exit 0, `complexity gate OK (N grandfathered, 0 new)`. (Current == baseline, so no new/worse.)

- [ ] **Step 3: Rewire the Makefile** — replace the entire `complexity:` recipe (from `complexity:` through its final `if [ "$$FAIL" -gt 0 ]; then exit 1; fi`) with:

```make
# Complexity ratchet (#468): grandfathered baseline that may only shrink.
# Fails only on NEW or WORSE violations vs scripts/complexity/baseline.txt.
complexity:
	@bash scripts/complexity/gate.sh gate

# Regenerate the grandfathered baseline after a legitimate decomposition.
# The committed baseline may only shrink (enforced in CI).
complexity-update:
	@bash scripts/complexity/gate.sh update
```

Add the pinned-version vars next to the existing limits (after `FILE_MAX_LINES ?= 500`, ~L18):
```make
GOCYCLO_VERSION   ?= v0.6.0
GOCOGNIT_VERSION  ?= v1.2.1
```

Add `complexity-update` to the `.PHONY` list (the line near L5 that lists `doctor complexity complexity-report ci ...`) → add `complexity-update`.

In `complexity-report:`, add `-not -path './.worktrees/*' -not -path './.claude/*'` to each `find` and pipe each `gocyclo`/`gocognit` through `grep -v -e '/.worktrees/' -e '/.claude/'` so the report matches the gate's exclusions (informational target; keep its structure otherwise).

- [ ] **Step 4: Verify `make complexity` is green**

Run: `make complexity`
Expected: exit 0, `complexity gate OK`.

- [ ] **Step 5: Commit**

```bash
git add scripts/complexity/baseline.txt Makefile
git commit -m "feat(ci): grandfather complexity baseline + wire make complexity to the ratchet (#468)

Co-Authored-By: Claude Opus 4.8 (1M context) <noreply@anthropic.com>
Claude-Session: https://claude.ai/code/session_012U14Lj3xBwjmSLJAx6jqbr"
```

---

### Task 3: Enforce in CI + fix the pre-commit hint

**Files:**
- Modify: `.github/workflows/ci.yml` (add complexity steps after "Agent-tool jail lint")
- Modify: `.pre-commit` (the **tracked** hook source — `make setup-hooks` symlinks it to `.git/hooks/pre-commit`; edit the tracked file, not the symlink). Update the install hint (~L88) to pinned versions.

**Interfaces:**
- Consumes: `scripts/complexity/gate.sh` (`gate`, `baseline-shrinks`).

- [ ] **Step 1: Add the CI steps** — in `.github/workflows/ci.yml`, immediately after the `Agent-tool jail lint` step and before `Verify tracker binary`, insert:

```yaml
      - name: Complexity gate
        run: bash scripts/complexity/gate.sh gate

      - name: Complexity baseline may only shrink
        if: github.event_name == 'pull_request'
        run: bash scripts/complexity/gate.sh baseline-shrinks "${{ github.event.pull_request.base.sha }}"
```

(`go run` uses the module cache already enabled by `actions/setup-go` `cache: true`. The baseline-shrink check is PR-only; on push-to-main there is no base PR sha.)

- [ ] **Step 2: Update the pre-commit hook install hint** — in the tracked `.pre-commit` file (~L88), change the warn message's install hint from `@latest` to the pinned versions:

```
  warn "gocyclo or gocognit not found — skipping complexity check (install: go install github.com/fzipp/gocyclo/cmd/gocyclo@v0.6.0 github.com/uudashr/gocognit/cmd/gocognit@v1.2.1). CI enforces the whole-repo ratchet regardless."
```

(The hook stays staged-scoped and best-effort locally; CI is now the authority. No behavior change beyond the hint.)

- [ ] **Step 3: Verify the gate commands the CI will run**

Run: `bash scripts/complexity/gate.sh gate && bash scripts/complexity/gate.sh baseline-shrinks HEAD`
Expected: both exit 0 (`complexity gate OK`; `baseline OK: did not grow vs HEAD` — HEAD's committed baseline equals the working copy).

- [ ] **Step 4: Commit**

```bash
git add .github/workflows/ci.yml .pre-commit
git commit -m "feat(ci): enforce the complexity ratchet in GitHub Actions; pin hook install hint (#468)

Co-Authored-By: Claude Opus 4.8 (1M context) <noreply@anthropic.com>
Claude-Session: https://claude.ai/code/session_012U14Lj3xBwjmSLJAx6jqbr"
```

(`.pre-commit` is tracked and symlinked to `.git/hooks/pre-commit` by `make setup-hooks`, so editing it updates the active hook after re-running setup — no need to touch `.git/`.)

---

### Task 4: Docs + CHANGELOG + full verification

**Files:**
- Create: `scripts/complexity/README.md`
- Modify: `CLAUDE.md` (the "Before committing" / "Before releasing" area that references `make ci`/complexity), `CHANGELOG.md` (`## [Unreleased]`)

- [ ] **Step 1: Write `scripts/complexity/README.md`:**

```markdown
# Complexity ratchet (#468)

`make complexity` (and CI) runs `gate.sh gate`: it scans production Go for
gocyclo (>8), gocognit (>8), and file-size (>500 line) violations and compares
them against `baseline.txt`, failing only on a violation that is **new** (not
grandfathered) or **worse** (a higher value than baselined).

`baseline.txt` is a **ceiling that may only shrink**. Format, one per line:
`metric|file|func|value` where metric ∈ {cyclo, cognitive, filesize}
(line-insensitive — no line:col — so entries survive ordinary edits).

## Burning it down
Refactor a grandfathered function/file below the limit (or lower its value),
then `make complexity-update` to regenerate `baseline.txt`. The reviewer sees
the baseline shrink in the diff. CI's `baseline-shrinks` check rejects any PR
whose `baseline.txt` grew.

## Tools
Pinned and run via `go run`: gocyclo `v0.6.0`, gocognit `v1.2.1`. No local
install required for CI; the pre-commit hook uses locally-installed copies for a
fast staged-files check (best-effort — CI is authoritative).
```

- [ ] **Step 2: Update `CLAUDE.md`** — find the "Before committing" / "Before releasing" section that lists `go test` / `dippin doctor`, and add one line under it:

```markdown
- `make complexity` — the complexity ratchet must stay green. It grandfathers a baseline that may only shrink (see `scripts/complexity/README.md`); a NEW or WORSE cyclo/cognitive/file-size violation fails it. Burn down with `make complexity-update`.
```

- [ ] **Step 3: Add the CHANGELOG entry** — under `## [Unreleased]`, add (create `### Fixed`/`### Changed` if absent):

```markdown
### Fixed

- **The canonical `make ci` complexity gate is real, green, and CI-enforced (#468).**
  It previously could not pass: `ci.yml` never ran it, the pre-commit hook only
  checked staged files, and the whole-repo scan double-counted `.worktrees/`
  copies (~10× inflation). The gate now runs a **ratchet** — a checked-in
  `scripts/complexity/baseline.txt` grandfathers the current cyclo/cognitive/
  file-size debt and may **only shrink**; a new or worsened violation fails the
  build. Analyzers are pinned (gocyclo v0.6.0, gocognit v1.2.1) and run in
  GitHub Actions. Limits are unchanged (8/8/500); the baseline burns down over
  time via `make complexity-update`.
```

- [ ] **Step 4: Full verification**

Run: `make complexity && bash scripts/complexity/gate_test.sh && go build ./...`
Expected: gate green; `ALL PASS`; build clean.
Run (if `make` has a fast subset, else skip the slow gates): `make fmt-check vet build`
Expected: pass. (Full `make ci` also runs test/race/coverage — the pre-commit hook covers those on commit.)

- [ ] **Step 5: Commit**

```bash
git add scripts/complexity/README.md CLAUDE.md CHANGELOG.md
git commit -m "docs(ci): document the complexity ratchet + burn-down flow (#468)

Co-Authored-By: Claude Opus 4.8 (1M context) <noreply@anthropic.com>
Claude-Session: https://claude.ai/code/session_012U14Lj3xBwjmSLJAx6jqbr"
```

- [ ] **Step 6: (held for after final review)** Push, open the PR closing #468, then fold into the pending release. Do not do this until the whole-branch review passes.

---

## Self-Review

**Spec coverage:** exclusions → `list_files()` (Task 1) + Makefile/report (Task 2); pin analyzers → Task 1 vars + Task 2 Makefile vars + Task 3 hint; baseline allowlist + can-only-shrink → Task 1 (`gate`/`update`/`baseline-shrinks`) + Task 2 (generate) + Task 3 (CI shrink check); CI wiring → Task 3; pre-commit → Task 3; docs → Task 4; keep 8/8/500 + grandfather → baseline generation (Task 2), no threshold change anywhere. Every spec section maps to a task.

**Placeholder scan:** every code/script step is complete and runnable; the one conditional (Task 3 note on whether `.git/hooks/pre-commit` is trackable) is a real environment branch with a defined fallback, not deferred work.

**Type/interface consistency:** `gate.sh` subcommands (`scan`/`check`/`gate`/`update`/`baseline-shrinks`), the `metric|file|func|value` line form, `$BASELINE=scripts/complexity/baseline.txt`, and the pinned versions (`v0.6.0`/`v1.2.1`) are used identically across Task 1 (script + tests), Task 2 (Makefile + generation), Task 3 (CI + hook), and Task 4 (docs). `compare()` is reused by both `gate` (baseline vs current) and `baseline-shrinks` (base vs committed) — same semantics.

**Ordering:** Task 1 (script + tests, self-contained) → Task 2 (baseline + Makefile, needs the script) → Task 3 (CI/hook, needs the committed script+baseline) → Task 4 (docs/CHANGELOG/verify). Follow numeric order.
