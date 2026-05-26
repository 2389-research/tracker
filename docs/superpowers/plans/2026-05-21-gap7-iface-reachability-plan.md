# Gap 7 — Interface Reachability Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Close Gap 7 of issue #233 — workflow declared a Go project "Done" while several interface methods were defined and unit-tested but never called from production code. Add a single-owner interface-reachability check at `FinalSpecCheck` with a shared discipline file written by `Setup`, plus a targeted upgrade to reviewer rubric point 2.

**Architecture:** Single-owner check at `FinalSpecCheck` (the existing goal-gate before `Cleanup → FinalCommit → Done`). Shared discipline file `.ai/build/iface-reachability-rubric.md` written by `Setup` (mirrors PR #246's `ci-probe.sh` pattern). Reviewer rubric point 2 upgraded with one sentence demanding shown grep evidence. **No changes to `VerifyMilestone`. No `.ai/pending_wiring.md` file. No 4-condition waiver discipline. No 3-hop transitive reach. No markdown table format.** Defaults `STATUS:fail` first, flips to `success` only at the very end — defeats the parseAutoStatus fail-open default that caused the original bug. Spec at `docs/superpowers/specs/2026-05-21-gap7-iface-reachability-design.md`.

**Tech Stack:** Dippin DSL (`examples/build_product.dip`), Go (`pipeline/handlers/codergen_test.go` for parser regression test), `make sync-workflows` for `examples/` ↔ `workflows/` mirror, `dippin doctor` + `dippin simulate -all-paths` + `tracker validate` for verification.

---

## Spec coverage map

| Spec section | Plan task |
|--------------|-----------|
| §4.1 Single owner: FinalSpecCheck | Task 4 |
| §4.2 Shared discipline file via Setup | Task 3 |
| §4.3 Language-conditional skip | Task 3 (rubric file content) |
| §4.4 Waiver discipline (1 sentence) | Task 3 (rubric file content) |
| §4.5 Library-API carve-out via `.ai/decisions/library_api.md` | Task 3 (rubric file content) |
| §4.6 Stdlib carve-out as principle | Task 3 (rubric file content) |
| §4.7 STATUS contract — fail-closed by default | Tasks 2 and 4 |
| §5.1 Setup writes rubric file | Task 3 |
| §5.2 FinalSpecCheck adds section | Task 4 |
| §5.3 Reviewer rubric point 2 upgrade × 3 | Task 5 |
| §5.4 VerifyMilestone unchanged | (no task — explicit non-change) |
| §6 step 8 CHANGELOG entry | Task 6 |
| §6 verification gates | Task 7 |

---

## File map

| File | Action |
|------|--------|
| `pipeline/handlers/codergen_test.go` | Modify — append regression test for v3 STATUS contract |
| `examples/build_product.dip` | Modify — Setup node (~line 93), FinalSpecCheck (~line 847), ReviewClaude rubric point 2 (line 645), ReviewCodex rubric point 2 (line 686), ReviewGemini rubric point 2 (line 738) |
| `workflows/build_product.dip` | Modify — via `make sync-workflows` (no manual edits) |
| `CHANGELOG.md` | Modify — add Gap 7 entry under `[Unreleased]` |

---

## Task 1: Worktree + branch setup (optional)

**Files:** none (git operations only)

- [ ] **Step 1: Confirm clean working tree on main**

Run: `git status && git log --oneline -1`
Expected: clean tree, latest commit `09116f5 release: v0.30.0 (#252)`.

- [ ] **Step 2: Decide on worktree vs direct branch**

If you want isolation, use `superpowers:using-git-worktrees`. Otherwise:

Run: `git checkout -b feat/233-gap7-iface-reachability`
Expected: switched to new branch.

---

## Task 2: Regression test for v3 STATUS contract

**Why this is first:** v3's §4.7 inverts the STATUS contract — the agent emits `STATUS:fail` first and only flips to `success` at the very end. This relies on the existing `parseAutoStatus` being last-STATUS-wins. The squad review (Prompt-Pragmatist finding) called out parseAutoStatus's default-to-success behavior as the dispositive risk. A regression test that locks in the parser's behavior for the v3 pattern is the cheapest possible insurance.

**Files:**
- Modify: `pipeline/handlers/codergen_test.go` (append at end of existing parseAutoStatus tests)

- [ ] **Step 1: Locate existing parser tests**

Run: `grep -n "parseAutoStatus\|TestParseStatus\|TestAutoStatus" pipeline/handlers/codergen_test.go`
Expected: at least one existing test in this file referencing the parser. Note the function name pattern (e.g., `TestParseAutoStatus`).

- [ ] **Step 2: Write the failing test**

Open `pipeline/handlers/codergen_test.go`. Append the following test (adjust to match existing test style — use `t.Run` subtests if the file uses table-driven style, otherwise standalone `func Test...`):

```go
// TestParseAutoStatus_V3FailFirstContract locks in the parser behavior
// that Gap 7 design v3 §4.7 depends on: agent emits STATUS:fail first
// and only flips to STATUS:success at the end if every check passed.
// If parseAutoStatus's last-wins semantics changes, this contract breaks
// and Gap 7's check becomes fail-open (the original bug shape).
func TestParseAutoStatus_V3FailFirstContract(t *testing.T) {
	cases := []struct {
		name   string
		input  string
		expect Outcome
	}{
		{
			name: "agent completes all checks: terminal success wins",
			input: "STATUS:fail\n" +
				"Default fail per Gap 7 §4.7 contract.\n" +
				"... full enumeration ...\n" +
				"All checks passed.\n" +
				"STATUS:success",
			expect: OutcomeSuccess,
		},
		{
			name: "agent gives up mid-check: only initial fail remains",
			input: "STATUS:fail\n" +
				"Default fail per Gap 7 §4.7 contract.\n" +
				"... partial enumeration, ran out of context ...",
			expect: OutcomeFail,
		},
		{
			name: "agent emits no STATUS line at all (legacy default)",
			input: "Some narrative without any STATUS marker.",
			expect: OutcomeSuccess,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := parseAutoStatus(tc.input)
			if got != tc.expect {
				t.Fatalf("parseAutoStatus = %v, want %v", got, tc.expect)
			}
		})
	}
}
```

**Note:** the exact `Outcome` constant names (`OutcomeSuccess`, `OutcomeFail`) and the `parseAutoStatus` package path may differ. Adjust to match what the existing test file uses. Run `grep -n "Outcome\(Success\|Fail\)" pipeline/handlers/codergen.go` to confirm.

- [ ] **Step 3: Run the test, verify it passes**

Run: `go test ./pipeline/handlers/ -run TestParseAutoStatus_V3FailFirstContract -v`
Expected: all three subtests PASS. **If any fails, STOP** — that's a real divergence in parser semantics and Gap 7 needs to be re-thought. Do not proceed.

Note: this test is expected to pass on first run because the parser already behaves as required. We are not driving new behavior; we are pinning current behavior.

- [ ] **Step 4: Run full package tests to confirm no breakage**

Run: `go test ./... -short`
Expected: all 17 packages pass.

- [ ] **Step 5: Commit**

```bash
git add pipeline/handlers/codergen_test.go
git commit -m "$(cat <<'EOF'
test(codergen): pin parseAutoStatus v3 fail-first contract (#233 Gap 7)

Gap 7 design v3 §4.7 inverts the STATUS contract: the FinalSpecCheck
prompt instructs the agent to emit STATUS:fail first and only flip
to STATUS:success at the very end if every check passes. This relies
on parseAutoStatus being last-STATUS-wins. Add a regression test that
pins that behavior so a future parser refactor doesn't silently make
Gap 7's check fail-open (which is the exact original bug shape).

Test cases cover: (a) agent completes all checks → terminal success
wins, (b) agent gives up mid-check → only initial fail remains, and
(c) legacy no-STATUS default → success (documents the default the v3
contract is defending against).

Co-Authored-By: Claude Opus 4.7 (1M context) <noreply@anthropic.com>
EOF
)"
```

---

## Task 3: Setup node — write the shared rubric file

**Files:**
- Modify: `examples/build_product.dip` (insert ~6 lines + heredoc after existing `PROBE_EOF` block at line 93, before `printf 'setup-ready'` at line 95)

- [ ] **Step 1: Read the current Setup node bracketing the insertion point**

Use Read tool on `examples/build_product.dip` lines 80-100 to confirm exact whitespace and indentation around the insertion point.

- [ ] **Step 2: Insert the new heredoc block**

Use Edit tool. Find this text (which currently appears at lines 93-95):

```text
      PROBE_EOF

      printf 'setup-ready'
```

Replace with:

```text
      PROBE_EOF

      # Shared interface-reachability rubric (issue #233 Gap 7).
      # FinalSpecCheck and the three reviewer prompts source this file
      # so the discipline lives in exactly one place — mirrors the
      # ci-probe.sh pattern from PR #246 (Gap 1), preventing the drift
      # that pre-#246 affected the awk between TestMilestone and
      # FinalBuild.
      cat > .ai/build/iface-reachability-rubric.md <<'RUBRIC_EOF'
      # Interface Reachability Rubric (issue #233 Gap 7)

      ## When this rubric applies (language detection)

      Survey languages in this project:
        git ls-files | awk -F. '{print $NF}' | sort -u

      If the project contains files in a static-interface language —
      Go (.go), Rust (.rs), Java (.java), Kotlin (.kt), Swift (.swift),
      TypeScript (.ts/.tsx), C++ (.cc/.cpp/.cxx/.hh/.hpp), C# (.cs),
      PHP (.php), Python with `ABC` or `Protocol` (.py) — proceed.

      If the project is exclusively in languages WITHOUT a static
      interface system (Ruby, plain JS, Elixir, Zig, C without
      function-pointer-table conventions, bash, shell, plain Markdown,
      DSL files), skip this check with a one-line note: "no
      static-interface languages detected; reachability check skipped."

      ## Enumeration

      For each detected language, enumerate interfaces / protocols /
      traits / typeclasses / abstract classes:
        Go:      grep -rnE 'type [[:alnum:]_]+ +interface[[:space:]{]' \
                   --include='*.go' .
                 Catches exported and unexported names; allows brace
                 immediately after `interface` (no space). Generic
                 interfaces (`type Foo[T any] interface`) are rarer
                 and not enumerated by this pattern — if the project
                 uses them, run a follow-up grep with the bracket
                 syntax.
        Rust:    grep -rnE '\btrait +[[:alnum:]_]+' \
                   --include='*.rs' .
                 Catches `trait`, `pub trait`, `pub(crate) trait`,
                 and unexported traits — all can carry unwired
                 methods.
        Java:    grep -rnE '^(public |abstract |sealed )*interface ' \
                   --include='*.java' .  ; also abstract class
        Kotlin:  grep -rnE '(interface |abstract class |fun interface )' \
                   --include='*.kt' .
        Swift:   grep -rnE 'protocol [A-Z][A-Za-z0-9_]* *(:|\{)' \
                   --include='*.swift' .
        TS:      grep -rnE '(interface |abstract class )' \
                   --include='*.ts' --include='*.tsx' .
        C++:     best-effort — look for pure-virtual classes:
                   grep -rnE 'virtual [^;]+= *0 *;' \
                   --include='*.cc' --include='*.cpp' --include='*.cxx' \
                   --include='*.h' --include='*.hh' --include='*.hpp' \
                   --include='*.hxx' .
                 The containing class is an abstract interface. CRTP
                 and templates are not enumerable via grep; skip those
                 with a one-line note.
        C#:      grep -rnE '(interface |abstract class )' \
                   --include='*.cs' .
        PHP:     grep -rnE '(interface |abstract function )' \
                   --include='*.php' .
        Python:  grep -rnE 'class [A-Z][A-Za-z0-9_]*\(.*(ABC|Protocol).*\)' \
                   --include='*.py' .

      Skip constraint-only Go interfaces (`interface { ~int | ~string }`).
      Skip embedded interface methods (those inherited from another
      interface declared elsewhere are out of scope; they're checked
      where they're declared).

      ## Caller discipline

      For each declared method M, find a non-test production caller.

      GREP CALL SYNTAX, NOT METHOD NAME. The grep must target an
      invocation: `\.M(` (Go/Java/TS/Swift/Kotlin/C#/PHP),
      `\.method_name(` (Python/Ruby), `->M(` (C++ via pointer),
      `::M(` (Rust via type), etc. The cited file:line MUST be a
      call expression, not a declaration line and not a receiver
      definition line.

      When M's name is common (Close, Read, String, Error, Run, Send,
      Get, Set, Init, New, ToString), the grep MUST include receiver
      context — e.g.
        grep -B1 -A0 '\.M(' --include='*.go' . | grep -v _test.go
      and the cited line must show the receiver type alongside the
      call. Same-name collisions are the dominant false-positive shape.

      EXCLUSIONS — these do NOT count as production callers:
        *_test.go, **/testutil/**, **/testing/**, **/mocks/**,
        **/fakes/**, **/fixtures/**, **/testdata/**, *_mock.go,
        **/__tests__/**, *.test.*, *.spec.*, conftest.py,
        src/test/java/**, test/*_test.exs, spec/**, Tests/, tests/,
        .gocache/**.
      Note: build-output trees (`target/**`, `build/**`) are NOT
      blanket-excluded — they often contain generated-source callers
      (see next paragraph) that ARE production. The test-specific
      globs above (`src/test/java/**`, etc.) handle hand-written test
      trees regardless of where they live.

      Generated code (*.pb.go, *_gen.go, zz_generated_*.go,
      target/generated-sources/**, __generated__/**, *.pb.cc,
      moc_*.cpp) DOES count as a production caller — note it
      explicitly when relevant.

      ## Stdlib / framework satisfaction (principle)

      If the implementing type is passed to a stdlib or framework
      function that accepts an interface argument, cite that passing
      site as the production caller. Examples:
        Go:     http.Handle(p, h), bufio.NewReader(r),
                sql.Register(n, d), json.Marshal(v), sort.Sort(s),
                slog.New(h), flag.Var(v, n, u)
        Python: passing to iter(), next(), with, framework
                decorators (@app.route, @app.task)
        TS:     passing to APIs typed with Iterable<T>, PromiseLike<T>
        Java:   Spring @Component / @Autowired / @RestController,
                JAX-RS @Path, ServiceLoader via META-INF/services
        Swift:  SwiftUI body protocol (framework calls it),
                Combine subscribers
      This is non-exhaustive; the principle is "framework or stdlib
      consumes the interface" → cite the wiring site, not a direct
      method call.

      ## Waivers

      A missing production caller is FAIL unless `.ai/decisions/*.md`
      documents this specific method by name with a non-blanket
      rationale. Before declaring FAIL run:
        grep -rn '<MethodName>' .ai/decisions/
      A hit naming the method with a rationale that wouldn't apply
      equally to every method (so not just "API surface" / "future
      use" / "by design") → PASS, cite the decision-log path. Blanket
      waivers → still FAIL.

      ## Library-API carve-out

      If `.ai/decisions/library_api.md` exists and lists this
      method's interface OR receiver type, treat as PASS and cite
      the declaration line. This is for public library symbols whose
      production callers are external (downstream) consumers.

      ## Known limitations — skip with one-line note, not FAIL

      Some dispatch mechanisms are fundamentally outside grep's reach.
      Skip these with a one-line note rather than flagging:
        Rust `dyn Trait` / `Box<dyn T>` collections (cite the trait-
          object site as the linkage if visible)
        Haskell typeclass dispatch (cite a constrained function)
        TypeScript bracket-notation dispatch: obj['method']()
        Java sealed interfaces with exhaustive switch dispatch
        Swift `extension Type: Protocol` conformances added away
          from the type's declaration site
        Ruby module mixins (not a static interface system)
        Elixir / Erlang @behaviour callbacks (called by OTP)
        Go interface-typed parameter dispatch where the static type
          can't be proven — show the grep you ran and cite the
          parameter site; do NOT use this as a default carve-out.

      When you skip, name the specific reason. Don't blanket-skip an
      entire interface as "dynamic dispatch."
      RUBRIC_EOF

      printf 'setup-ready'
```

- [ ] **Step 3: Verify the file parses**

Run: `dippin doctor examples/build_product.dip`
Expected: A grade, no new warnings vs. main. **If `doctor` warns on prompt length** for the Setup node, the rubric heredoc may need to be split. See spec §7 risk register — the spec's expectation is that this lives in the heredoc body, which dippin counts differently than agent-prompt content. If it warns, capture the warning text and stop; we'll address before continuing.

- [ ] **Step 4: Commit**

```bash
git add examples/build_product.dip
git commit -m "$(cat <<'EOF'
feat(build_product): Setup writes iface-reachability-rubric.md (#233 Gap 7)

Mirrors PR #246's ci-probe.sh pattern: the discipline lives in exactly
one place (.ai/build/iface-reachability-rubric.md) so it can be
referenced from FinalSpecCheck and the three reviewer prompts without
duplicating ~80 lines of grep/exclusion/carve-out prose across 5 nodes.
Pre-#246 the awk silently drifted between TestMilestone and FinalBuild;
v3 design (post-squad-review) explicitly applies the same drift-prevention
lesson to Gap 7.

The rubric file contains: language-detection step (10 supported static-
interface languages + skip-vacuously rule for Ruby/plain JS/Elixir/Zig/C/
shell/Markdown/DSL), per-language enumeration grep patterns, caller
discipline (call-syntax targeting, common-name receiver context, broad
test-file exclusion glob, generated-code-counts-as-production rule),
stdlib/framework satisfaction principle, one-sentence waiver discipline,
library-API carve-out via .ai/decisions/library_api.md, and known-
limitation skips (Rust dyn Trait, Haskell typeclasses, TS bracket
notation, etc.) with named-reason discipline.

The full sync to workflows/build_product.dip lands in a later commit
after the FinalSpecCheck and reviewer-rubric edits, so the workflow
mirror stays consistent across the PR.

Co-Authored-By: Claude Opus 4.7 (1M context) <noreply@anthropic.com>
EOF
)"
```

---

## Task 4: FinalSpecCheck — add INTERFACE REACHABILITY section

**Files:**
- Modify: `examples/build_product.dip` (FinalSpecCheck agent at line 847, insert section at top of its prompt body)

- [ ] **Step 1: Read FinalSpecCheck's current prompt**

Use Read tool on `examples/build_product.dip` lines 847-900 (the FinalSpecCheck node spans roughly that range — read until you find the next `agent` or `tool` declaration). Identify the exact indented `prompt:` block boundary.

- [ ] **Step 2: Insert the new section at the top of the prompt body**

Find the first non-blank line of FinalSpecCheck's prompt body (after the `prompt:` line). Insert the following block as the very first content of the prompt, with the same indentation the existing prompt body uses:

```text
      INTERFACE REACHABILITY (issue #233 Gap 7):

      Your default STATUS for this section is `fail`. Emit a single
      `STATUS:success` line at the very end of your response — alone
      on its line, outside any code fence — only if every check below
      passes. Do NOT emit `STATUS:success` inside a table cell or
      narrative paragraph; the workflow's auto_status parser scans
      for line-leading STATUS markers and uses the last one wins.

      Read `.ai/build/iface-reachability-rubric.md` FIRST (it was
      written by Setup at the start of this run; cat the file). It
      contains the language-detection step, enumeration grep patterns
      per language, caller discipline (call-syntax targeting,
      receiver-context requirements, test-file exclusion globs),
      stdlib/framework carve-out principle, waiver rules, library-API
      carve-out, and known-limitation skips. Apply it across the
      entire repo. Enumerate every method declared on any interface /
      protocol / trait / typeclass / abstract class in the repo (NOT
      just last-milestone diff). For each declared method, name a
      non-test production caller with grep evidence per the rubric,
      OR cite the applicable carve-out:
        - stdlib / framework wiring site
        - `.ai/decisions/library_api.md` entry
        - `.ai/decisions/*.md` waiver with non-blanket rationale
        - known-limitation skip with named reason

      Output is prose enumeration — name each method, give file:line
      of the production caller (or the carve-out reason). Do NOT
      group, do NOT skip, do NOT write "ditto" or "similar." If you
      run out of context mid-enumeration, stop and leave the early
      `STATUS:fail` from your response's first line in place; you
      may add a separate line stating how many methods you reached.
      Do NOT emit `STATUS:fail <N>` or `STATUS:fail with count` —
      the parser requires the STATUS value to be exactly `fail`,
      `success`, or `retry`, with no trailing prose on that line.

      Regression cases this check exists to catch (issue #233
      Appendix A I9, I10): `AuthStatus(ctx) error` defined and
      unit-tested but never called from `cmd/goblin/main.go`;
      `IsRebaseInProgress() bool` defined and tested but unused in
      `internal/review/loop.go`. Both shipped green because this
      check didn't exist.

```

(The blank line at the end before the existing prompt content is intentional.)

- [ ] **Step 3: Verify the file parses**

Run: `dippin doctor examples/build_product.dip`
Expected: A grade, no new warnings.

- [ ] **Step 4: Verify simulation still terminates**

Run: `dippin simulate -all-paths examples/build_product.dip`
Expected: every path terminates; no new infinite loops introduced. The output should look similar to the v0.30.0 baseline.

- [ ] **Step 5: Commit**

```bash
git add examples/build_product.dip
git commit -m "$(cat <<'EOF'
feat(build_product): FinalSpecCheck owns iface reachability (#233 Gap 7)

Add INTERFACE REACHABILITY section to the top of FinalSpecCheck's
prompt — single-owner check, repo-wide sweep, sources discipline from
.ai/build/iface-reachability-rubric.md. The check fires once per run
at the workflow's goal-gate immediately before Cleanup/FinalCommit/Done.

§4.7 STATUS contract — fail-closed by default: the prompt explicitly
instructs the agent to default STATUS:fail and only flip to
STATUS:success at the very end after every check passes. This defends
against the parseAutoStatus default-to-success-on-empty fail-open shape
that the squad review identified as the dispositive risk in v2's design
(and that is the exact failure mode of the original Gap 7 bug).

Output is prose enumeration — no markdown table requirement (v2 had
one; squad review found it created parser-fragility risk because
STATUS tokens inside table cells aren't parsed). Agent names every
method, gives file:line of production caller or cites carve-out
(stdlib/framework wiring, library_api.md entry, .ai/decisions/ waiver,
known-limitation skip).

Regression cases I9 (AuthStatus) and I10 (IsRebaseInProgress) named
explicitly in the prompt so the agent knows what shape of bug to
catch.

VerifyMilestone is unchanged by this PR — per §4.1, per-milestone
iface checks were rejected because (a) the bug is a property of the
terminal state not the build process, (b) per-milestone scoping
creates a cross-milestone leak shape that needed .ai/pending_wiring.md
to paper over (rejected as 17%-reliability LLM-managed state per
squad review), and (c) the workflow is fully automated so "catch
early" has no operational value when no human is debugging mid-run.

Co-Authored-By: Claude Opus 4.7 (1M context) <noreply@anthropic.com>
EOF
)"
```

---

## Task 5: Reviewer rubric point 2 — single-sentence upgrade (3 reviewers)

**Files:**
- Modify: `examples/build_product.dip` lines 645-648 (ReviewClaude point 2), 686-688 (ReviewCodex point 2), 738-740 (ReviewGemini point 2)

- [ ] **Step 1: Read all three current point-2 blocks**

Use Read tool on `examples/build_product.dip` lines 640-650, 680-695, and 735-745 to confirm exact current wording and indentation.

- [ ] **Step 2: Update ReviewClaude rubric point 2**

Find this text at approximately lines 645-648:

```text
      2. INTERFACE REACHABILITY: For every interface method defined in this
         section's files, name at least one production (non-test) caller.
         Where there is none, flag it.
```

Replace with:

```text
      2. INTERFACE REACHABILITY: For every interface method defined in
         this section's files, name at least one production (non-test)
         caller with grep evidence — show the grep command, paste its
         output, cite the file:line. Apply the same show-your-work
         standard as SPEC LITERALS at point 1. "Looks reachable" /
         "exported, so something must call it" without a file:line hit
         is FAIL. Follow the discipline in
         `.ai/build/iface-reachability-rubric.md` (test-file exclusions,
         stdlib carve-out, waiver rules, known limitations).
```

- [ ] **Step 3: Update ReviewCodex rubric point 2**

Find the equivalent text at approximately lines 686-688 (identical 3-line wording in the ReviewCodex prompt body). Replace with the same 9-line block as above.

- [ ] **Step 4: Update ReviewGemini rubric point 2**

Find the equivalent text at approximately lines 738-740. Replace with the same 9-line block.

- [ ] **Step 5: Verify all three blocks are present and identical**

Run: `grep -A 8 "INTERFACE REACHABILITY:" examples/build_product.dip | head -30`
Expected: three identical 9-line blocks across the three reviewer prompts. **If they differ, fix the divergence** — the rubric must stay identical across reviewers per PR #249's design.

- [ ] **Step 6: Verify the file still parses**

Run: `dippin doctor examples/build_product.dip`
Expected: A grade, no new warnings.

Run: `tracker validate examples/build_product.dip`
Expected: clean (ctx.outcome warnings are simulator noise per CLAUDE.md).

- [ ] **Step 7: Commit**

```bash
git add examples/build_product.dip
git commit -m "$(cat <<'EOF'
feat(build_product): strengthen reviewer rubric point 2 (#233 Gap 7)

Upgrade INTERFACE REACHABILITY point in all three reviewer prompts
(ReviewClaude, ReviewCodex, ReviewGemini) from 3 lines of free-form
prose to a 9-line block that:

- Demands grep evidence (show the command, paste the output, cite
  file:line) — same show-your-work standard as point 1 SPEC LITERALS,
  which the original audit showed reviewers DID follow rigorously
  while skipping point 2's lighter "name a caller" requirement.
- Explicitly rejects "looks reachable" / "exported, so something must
  call it" handwaving without file:line evidence.
- Defers the heavy discipline (test-file exclusions, stdlib carve-out,
  waiver rules, known limitations) to .ai/build/iface-reachability-
  rubric.md so the rubric in the reviewer prompt stays balanced with
  points 1, 3, 4, 5 (~5-8 lines each per PR #249's design).

The shared rubric file means the discipline lives in one place; the
reviewer just demands the standard and points at the file. Mirrors
PR #246's ci-probe.sh referenced-from-multiple-nodes pattern.

Co-Authored-By: Claude Opus 4.7 (1M context) <noreply@anthropic.com>
EOF
)"
```

---

## Task 6: CHANGELOG entry

**Files:**
- Modify: `CHANGELOG.md` — under `[Unreleased]` section (line 8)

- [ ] **Step 1: Read current CHANGELOG header**

Already confirmed: `## [Unreleased]` is at line 8 followed by an empty section. The most recent entry under it would land between line 8 and line 10 (`## [0.30.0] - 2026-05-19`).

- [ ] **Step 2: Insert Gap 7 entry**

Use Edit tool. Find:

```text
## [Unreleased]

## [0.30.0] - 2026-05-19
```

Replace with:

```text
## [Unreleased]

### Changed

- **`build_product` workflow: closed Gap 7 from the #233 audit (interface-method reachability)** ([#233](https://github.com/2389-research/tracker/issues/233)). The audit caught three Go interface methods defined and unit-tested but never called from production code: `AuthStatus(ctx) error` (Appendix A I9), `IsRebaseInProgress() bool` (I10), `DiffStat` (similar shape). Tests passed because the same agent wrote impl and tests; the workflow had no check that defined interface methods have a non-test caller. New mechanism:
  - **`Setup` now writes `.ai/build/iface-reachability-rubric.md`** — a shared discipline file mirroring PR #246's `ci-probe.sh` pattern. Contains language detection (10 static-interface languages with skip-vacuously rule for Ruby / plain JS / Elixir / Zig / C / shell), per-language enumeration grep patterns, caller-discipline rules (call-syntax targeting, common-name receiver context, broad test-file exclusion glob, generated-code-counts-as-production), stdlib/framework satisfaction principle, single-sentence waiver discipline, library-API carve-out via `.ai/decisions/library_api.md`, and known-limitation skips (Rust `dyn Trait`, Haskell typeclasses, TS bracket-notation, Swift extension conformances, etc.) with named-reason discipline.
  - **`FinalSpecCheck` owns the check** — repo-wide sweep, single owner, fires once per run at the goal-gate immediately before Cleanup/FinalCommit/Done. The prompt opens with an inverted STATUS contract: default `STATUS:fail`, flip to `STATUS:success` only at the very end after every check passes. This defends against the `parseAutoStatus` default-to-success-on-empty fail-open shape that is exactly the original Gap 7 bug. Output is prose enumeration (no markdown table requirement — that introduced parser-fragility risk because STATUS tokens inside table cells aren't parsed).
  - **Reviewer rubric point 2 strengthened** in `ReviewClaude`, `ReviewCodex`, `ReviewGemini` — from 3-line "name a caller" to 9-line "show the grep command, paste the output, cite file:line — same show-your-work standard as SPEC LITERALS at point 1." The heavy discipline lives in the shared rubric file the reviewers reference, keeping rubric balance with the other four points (per PR #249's design).
  - **`VerifyMilestone` is unchanged.** Per-milestone iface checks were considered and rejected (squad review): the bug is a property of the terminal state not the build process, per-milestone scoping creates a cross-milestone leak shape that would have required LLM-managed `.ai/pending_wiring.md` bookkeeping (~17% reliability after 5 milestones per parser-pragmatist analysis), and the workflow is fully automated so "catch early" has no operational value when no human is debugging mid-run.
  - Workflow score on `dippin doctor examples/build_product.dip` stays **A / 100/100**, no new lint warnings. The remaining #233 gaps (5 engine-level `auto_status` audit, 8 `TestQuality` step) are Chunk C, queued for a follow-up PR.

## [0.30.0] - 2026-05-19
```

- [ ] **Step 3: Commit**

```bash
git add CHANGELOG.md
git commit -m "$(cat <<'EOF'
docs(changelog): Gap 7 entry under [Unreleased] (#233)

Standard pattern matching PR #246 and PR #249's CHANGELOG entries:
narrate the gap, the regression cases (I9/I10/DiffStat), the
mechanism (Setup writes shared rubric file; FinalSpecCheck owns the
check; reviewer rubric point 2 strengthened; VerifyMilestone
deliberately unchanged), and the squad-review rationale for the
no-per-milestone-check decision.

Co-Authored-By: Claude Opus 4.7 (1M context) <noreply@anthropic.com>
EOF
)"
```

---

## Task 7: Sync workflows + full verification gates

**Files:**
- Modify: `workflows/build_product.dip` (via `make sync-workflows` — no manual edits)

- [ ] **Step 1: Run sync-workflows**

Run: `make sync-workflows`
Expected: `workflows/build_product.dip` updated to mirror `examples/build_product.dip`. The command should run cleanly. If it prints diffs, that's the sync content.

- [ ] **Step 2: Confirm sync is complete**

Run: `make check-workflows`
Expected: no diff. **If diff is non-empty**, re-run `make sync-workflows` and stage both files.

- [ ] **Step 3: Run dippin doctor on all relevant examples**

Run: `dippin doctor examples/ask_and_execute.dip examples/build_product.dip examples/build_product_with_superspec.dip`
Expected: all three A grade, no new warnings. The `build_product` score should match v0.30.0's baseline (`A / 100/100`).

- [ ] **Step 4: Run dippin simulate -all-paths on the three core pipelines**

Run: `dippin simulate -all-paths examples/ask_and_execute.dip`
Run: `dippin simulate -all-paths examples/build_product.dip`
Run: `dippin simulate -all-paths examples/build_product_with_superspec.dip`
Expected: every path terminates for all three. No new infinite loops.

- [ ] **Step 5: Run tracker validate**

Run: `tracker validate examples/build_product.dip`
Expected: clean (ctx.outcome warnings are simulator noise per CLAUDE.md).

- [ ] **Step 6: Run full Go build and tests**

Run: `go build ./...`
Expected: clean build.

Run: `go test ./... -short`
Expected: all 17 packages pass. The new TestParseAutoStatus_V3FailFirstContract test from Task 2 should pass.

- [ ] **Step 7: Commit the synced workflows file**

```bash
git add workflows/build_product.dip
git commit -m "$(cat <<'EOF'
chore(workflows): sync workflows/build_product.dip with examples (#233 Gap 7)

Mirror of examples/build_product.dip via make sync-workflows. No new
content; this is the standard examples/ ↔ workflows/ sync done as a
single commit at the end of the Gap 7 PR for clarity, matching the
PR #246 and PR #249 sync-commit pattern.

Co-Authored-By: Claude Opus 4.7 (1M context) <noreply@anthropic.com>
EOF
)"
```

---

## Task 8: Push branch and open PR

**Files:** none (git remote + GitHub operations)

- [ ] **Step 1: Push the branch**

Run: `git push -u origin feat/233-gap7-iface-reachability`
Expected: branch pushed.

- [ ] **Step 2: Open the PR via gh CLI**

Run:

```bash
gh pr create --title "feat(build_product): close Gap 7 from #233 audit (interface reachability)" --body "$(cat <<'EOF'
## Summary

Closes Gap 7 of [#233](https://github.com/2389-research/tracker/issues/233) — the original audit caught three Go interface methods defined and unit-tested but never called from production: `AuthStatus(ctx) error` (Appendix A I9), `IsRebaseInProgress() bool` (I10), and `DiffStat` (similar shape). Tests passed because the same agent wrote impl and tests; the workflow had no check that defined interface methods had a non-test caller.

This PR adds a single-owner interface-reachability check at `FinalSpecCheck` plus a shared discipline file written by `Setup` (mirroring PR #246's `ci-probe.sh` pattern) plus a targeted reviewer-rubric point 2 upgrade.

## Why this shape

The design went through three iterations + extensive adversarial review:

- **v1** (Go-only mechanical script): rejected — `build_product` must work on any product.
- **v2** (comprehensive multi-node design with `.ai/pending_wiring.md` cross-milestone tracking, 4-condition waiver discipline, per-method markdown table format, 3-hop transitive reach): rejected by all 5 squad reviewers. Key findings: (a) the design was fail-open by default because `parseAutoStatus` is last-STATUS-wins and defaults to `success` on empty, recreating the exact original bug, (b) LLM-managed `.ai/pending_wiring.md` file mutation degrades ~30%/milestone, ~17% reliability after 5 milestones, (c) 5N method-checks for N methods with diffuse ownership, (d) the markdown table format introduced parser-fragility risk because STATUS tokens inside cells aren't parsed.
- **v3** (this PR): single owner at `FinalSpecCheck`, no per-milestone gate, no `.ai/pending_wiring.md`, single-sentence waiver, prose enumeration with inverted STATUS contract, language-conditional skip for non-static-interface projects, shared rubric file. ~80 lines added vs v2's ~200+. Same regression-case coverage.

Spec: `docs/superpowers/specs/2026-05-21-gap7-iface-reachability-design.md` (v3).
Plan: `docs/superpowers/plans/2026-05-21-gap7-iface-reachability-plan.md`.

## What v3 catches

The three named regression cases (I9 `AuthStatus`, I10 `IsRebaseInProgress`, `DiffStat`) all caught at `FinalSpecCheck`'s repo-wide sweep with shown grep evidence. Specifically:
- Agent reads `.ai/build/iface-reachability-rubric.md` first
- Surveys repo for static-interface-language files; skips vacuously if none (Ruby/plain JS/Elixir/Zig/C/shell-only projects)
- For each declared interface method, runs the call-syntax grep with test-file exclusions per the rubric
- Emits `STATUS:fail` first, only emits `STATUS:success` at the end if every method has a production caller, a stdlib/framework wiring site cite, a `.ai/decisions/` waiver naming the method, or a named-reason known-limitation skip

## What's deliberately NOT in this PR

- No `VerifyMilestone` changes (per-milestone iface checks rejected; bug is a terminal-state property)
- No `.ai/pending_wiring.md` (FinalSpecCheck's repo-wide sweep covers cross-milestone case)
- No 4-condition waiver discipline (single-sentence rule + reviewer-rubric next-layer defense)
- No markdown table format (caused parser fail-open in v2)
- No 3-hop transitive reach (impossible-via-grep; agents would fake it)
- No Go-specific mechanical script

## Test plan

- [x] `dippin doctor examples/build_product.dip` — A grade, no new warnings
- [x] `dippin simulate -all-paths examples/build_product.dip` — every path terminates
- [x] `tracker validate examples/build_product.dip` — clean
- [x] `go build ./... && go test ./... -short` — all 17 packages pass
- [x] `make check-workflows` — examples/ and workflows/ in sync
- [x] New test `TestParseAutoStatus_V3FailFirstContract` locks in parser behavior the v3 STATUS contract depends on
- [ ] CHANGELOG entry under `[Unreleased]`
- [ ] Manual mental replay of I9 / I10 / DiffStat scenarios against the rubric prompt confirms each would now produce `STATUS:fail`

🤖 Generated with [Claude Code](https://claude.com/claude-code)
EOF
)"
```

Expected: PR URL printed. Return the URL to the user.

---

## Verification checklist (final)

Before declaring done:

- [ ] All 7 verification gates from spec §6 passed (dippin doctor, dippin simulate, tracker validate, go build, go test, make check-workflows, CHANGELOG entry)
- [ ] All three reviewer prompts contain the identical strengthened rubric point 2 (grep confirms 3 matches)
- [ ] `examples/build_product.dip` and `workflows/build_product.dip` are byte-identical mirror (modulo the standard auto-sync header)
- [ ] `dippin doctor examples/build_product.dip` produces A grade with no new warnings vs. v0.30.0 baseline
- [ ] PR opened and CI checks running
