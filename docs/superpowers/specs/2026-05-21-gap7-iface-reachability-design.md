# Gap 7 — Interface Method Reachability (Issue #233)

**Status:** Design v3 (post-squad-review)

**Author:** Claude (Opus 4.7) + Clint Ecker

**Date:** 2026-05-21

**Closes:** Gap 7 of [#233](https://github.com/2389-research/tracker/issues/233) — `build_product` workflow audit recap.

---

## 1. Problem

A real run of `examples/build_product.dip` declared a Go project (`code-goblin`) "Done" while several interface methods were defined and unit-tested but never called from production code:

- `AuthStatus(ctx) error` (issue #233 Appendix A I9)
- `IsRebaseInProgress() bool` (issue #233 Appendix A I10)
- `DiffStat` — same shape

Tests passed because the same agent wrote the implementation and the unit tests. The workflow had no check that a defined interface method has a non-test caller.

The reviewer rubric (PR #249) added `INTERFACE REACHABILITY` as point 2 of the cross-review checklist, but as ~3 lines of prose. The audit shows reviewers can handwave past it.

## 2. Goals

- Catch the three named regression cases at the final-spec-check stage with shown-work grep evidence.
- Keep the design as **small as possible** consistent with closing the gap. v1 and v2 were rejected for over-engineering; v3 keeps roughly 1/5 the prompt budget of v2.
- Single owner — one node, by name, gates "no unwired interface method may reach Done."
- Language-agnostic by **detecting language first and skipping vacuously** when the language has no static interface concept. Per-language refinement is opt-in via `.ai/decisions/`, not enumerated in the prompt.
- Compose cleanly with PR #246's "Setup writes shared helper" pattern and PR #249's balanced 5-point rubric.

## 3. Non-goals

- Replacing `go/types`-grade callgraph analysis. Dynamic dispatch (Rust `dyn Trait`, Haskell typeclasses, TS bracket-notation, Go interface-typed parameters) is fundamentally outside grep's reach. The check skips vacuously where grep can't help.
- Per-milestone tracking of unwired interfaces. The repo-wide sweep at `FinalSpecCheck` covers the cross-milestone case without a per-milestone obligation file.
- Adding a Go-specific mechanical script. The workflow targets any product.
- Gaps 5 and 8 (Chunk C). Tracked separately.

## 4. Design

### 4.1 Single owner: `FinalSpecCheck`

The interface-reachability check lives in `FinalSpecCheck` and **only** in `FinalSpecCheck`. Reasons:

- The bug is "code shipped Done with unwired methods" — a property of the terminal state, not the build process.
- The workflow is fully automated; "catch early in milestone-3 vs at FinalSpecCheck" has no operational value when no human is debugging mid-run.
- Per-milestone Verify's diff scoping creates the cross-milestone leak shape the v2 design tried to fix with `.ai/pending_wiring.md`. Removing the per-milestone gate removes the need for that file entirely.
- `FinalSpecCheck` is already the goal-gate before `Cleanup → FinalCommit → Done` and routes failures to `EscalateReview` — the right route both for real bugs and for tool-limitation false positives.

`VerifyMilestone` is **unchanged** by this PR. The reviewer rubric gets one targeted strengthening.

### 4.2 Shared discipline lives in one file

The check's discipline (grep style, test-file exclusion globs, stdlib carve-out principle, language detection) is written **once** to `.ai/build/iface-reachability-rubric.md` by the `Setup` node — mirroring PR #246's `ci-probe.sh` pattern. `FinalSpecCheck` and the three reviewer prompts reference the file by path instead of duplicating ~80 lines of prose into each of five nodes.

This is the highest-leverage architectural decision in v3. It eliminates the drift risk PR #246 round-5 explicitly addressed, restores PR #249's rubric balance, and keeps the prompt under the line budget that `dippin doctor` warns on.

### 4.3 Language-conditional skip

`.ai/build/iface-reachability-rubric.md` opens with a language detection step:

> Survey languages in scope via `git ls-files | awk -F. '{print $NF}' | sort -u`. If the project contains files in any of these languages with static interface systems — Go (`.go`), Rust (`.rs`), Java (`.java`), Kotlin (`.kt`), Swift (`.swift`), TypeScript (`.ts`/`.tsx`), C++ (`.cc`/`.cpp`/`.cxx`/`.hh`/`.hpp`), C# (`.cs`), PHP (`.php`), Python with `ABC` or `Protocol` (`.py`) — proceed. If the project is exclusively in languages without static interface systems (Ruby, plain JS, Elixir, Zig, C, bash, shell, plain Markdown, DSL files), emit `STATUS:success` with the note: "no static-interface languages detected; reachability check skipped."

For projects with **specific** dispatch mechanisms grep can't see (Rust `dyn Trait`, Haskell typeclasses, TS bracket-notation, Swift protocol extensions, Ruby `module` mixins, Elixir `@behaviour`, etc.), the rubric lists these as known limitations and instructs the agent to skip them with a one-line note. Operators relying on those patterns declare via `.ai/decisions/`.

### 4.4 Waiver discipline

A missing production caller is `STATUS:fail` **unless** `.ai/decisions/*.md` documents this specific method by name with a non-blanket rationale. One sentence. No `.ai/pending_wiring.md`, no commit-history forensics, no 4-condition gate.

The asymmetric attack (Implement agent pre-authoring waivers) remains a real risk; the mitigation is the next layer (reviewer rubric demands the same shown-work standard, and a `.ai/decisions/` file with a blanket "all methods are intentional" rationale would be flagged by either gate). v2's stricter discipline was theatre — see the squad review (Prompt-Pragmatist finding #4): cumulative reliability of the 4-condition check across an LLM was ~15-25%.

### 4.5 Library-API carve-out

`.ai/decisions/library_api.md` (optional, per-project) lists exported symbols whose production callers are external consumers (downstream library users). Without this file, the check flags every interface in an exported package. **It lives in `.ai/decisions/` as a normal artifact**, not as a new convention — `.ai/decisions/` already holds configuration-shaped files (`spec-analysis.md`, `milestones.md`, `review-synthesis.md`, `compliance.md`), so this fits.

Schema:

```markdown
# Library API Surface

<!-- Symbols listed here are public API for downstream consumers and
     intentionally lack in-tree production callers. -->

## Package <pkg>
- <Type>.<Method> (<path>:<line>)
- <Function> (<path>:<line>)
```

### 4.6 Stdlib carve-out as principle, not enumeration

The shared discipline file states the principle once: **"if the implementing type is passed to a stdlib (or framework) function that accepts an interface argument, cite that passing site as the production caller."** Examples (not exhaustive): `http.Handle(path, handler)`, `bufio.NewReader(r)`, `sql.Register(name, drv)`, `json.Marshal(v)`, `sort.Sort(s)`, `slog.New(h)`, `flag.Var(v, name, usage)`. Python: passing to `iter()`, `next()`, `with`, framework decorators. TS: passing to APIs typed with `Iterable<T>`, `PromiseLike<T>`.

v2's per-language enumeration was incomplete and tempted cherry-picking; the principle covers the long tail.

### 4.7 STATUS contract — fail-closed by default

The `FinalSpecCheck` prompt opens with: **"Your default STATUS for this section is `fail`. Emit a single line `STATUS:success` at the very end of your response, alone on its line and outside any code fence, only if every check below passed."** This defends against the partial-completion fail-open the squad review (Prompt-Pragmatist finding #1) identified as the dispositive issue — `parseAutoStatus` is last-STATUS-wins and defaults to `success` on empty.

Combined with the language-conditional skip from §4.3, the agent emits exactly one terminal `STATUS:success` or `STATUS:fail` line, never embedded in a table cell or prose.

## 5. Concrete edits

Three artefacts change in `examples/build_product.dip`; `make sync-workflows` mirrors to `workflows/build_product.dip`.

### 5.1 `Setup` node — write the shared rubric file

After the existing `cat > .ai/build/ci-probe.sh` block, add:

```dip
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
```

### 5.2 `FinalSpecCheck` — new section at the top of its prompt

```dip
      INTERFACE REACHABILITY (issue #233 Gap 7):

      Your default STATUS for this section is `fail`. Emit a single
      `STATUS:success` line at the very end of your response — alone
      on its line, outside any code fence — only if every check below
      passes. Do NOT emit `STATUS:success` inside a table cell or
      narrative paragraph; the workflow's auto_status parser scans for
      line-leading STATUS markers and uses the last one wins.

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

### 5.3 Reviewer rubric point 2 — single-sentence upgrade

Replace lines 645-648 / 686-688 / 738-740 of `examples/build_product.dip` with:

```
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

This is ~7 lines vs. v2's ~35 — restoring the balance with points 1, 3, 4, 5 (each ~5-8 lines). The heavy discipline lives once in the rubric file; the prompt just demands grep evidence and references the file.

### 5.4 `VerifyMilestone` — no change

v3 does not modify `VerifyMilestone`. Its current 7 checks stand. The single-owner architectural decision (§4.1) makes per-milestone iface-reachability unnecessary; `FinalSpecCheck` handles it.

## 6. Build and verification

For each session working on this PR:

1. Edit `examples/build_product.dip` — three sections change: `Setup` (add rubric-file write block), `FinalSpecCheck` (add section at top of prompt), reviewer rubric point 2 in `ReviewClaude`/`Codex`/`Gemini`.
2. `make sync-workflows` — stage both files.
3. `dippin doctor examples/build_product.dip` — must remain A grade with no new warnings.
4. `dippin simulate -all-paths examples/build_product.dip` — confirm every path still terminates.
5. `tracker validate examples/build_product.dip` — clean.
6. `go build ./... && go test ./... -short` — all 17 packages.
7. `make check-workflows` — sync confirmed.
8. **Update `CHANGELOG.md`** — add a Gap 7 entry under the next `[Unreleased]` section, matching the pattern of PR #246 and PR #249.

## 7. Risk register

| Risk | Likelihood | Impact | Mitigation |
|------|------------|--------|------------|
| `FinalSpecCheck`'s prompt grows long once the iface section is added on top of its existing spec-conformance checks | Medium | Medium — `dippin doctor` may warn | Discipline lives in `.ai/build/iface-reachability-rubric.md`; the prompt section is small. If `doctor` still warns, peel more of the existing FinalSpecCheck content into a shared file. |
| Agent runs out of context mid-enumeration on a repo with hundreds of interface methods | Medium | High — without §4.7's fail-closed default, this would silently pass | §4.7's "default STATUS is `fail`" plus the explicit instruction "if you run out of context mid-enumeration, stop and emit STATUS:fail with the count" defends against this. |
| Implement agent pre-authors `.ai/decisions/*.md` to game its own future verifier | Medium | Medium — the v2 four-condition discipline was theatre against this | The reviewer rubric is the next layer; reviewers see all `.ai/decisions/` files and can flag blanket-rationale waivers. The repo-wide sweep at `FinalSpecCheck` reads decision files line-by-line, not just by presence. |
| Stdlib carve-out misapplied (agent claims "this is a stdlib interface" without evidence) | Medium | Low | Carve-out requires citing the wiring site (`http.Handle(...)`, etc.). No wiring site cited = no carve-out. |
| Language-detection survey misses a static-interface project file | Low | Low — false skip | Survey is a one-shot grep on file extensions; the listed extensions cover all currently-supported languages. New languages added later require a one-line append. |
| Reviewer rubric upgrade still gets handwaved | Medium | Medium — the same failure mode as PR #249 round-1 | Adversarial Gemini lane (PR #249) is the existing mitigation. v3 demands shown grep evidence which is the same standard point 1 (SPEC LITERALS) already enforces. |

## 8. What was considered and rejected

The squad review (5 expert reviewers, parallel dispatch, 2026-05-21) converged on the same verdict for two earlier drafts:

**v1** — Go-only mechanical script (`iface-reachability.sh`) written by Setup, sourced by `TestMilestone` and `VerifyMilestone`. Rejected because `build_product` is meant to work on any product; a Go-only mechanical script breaks the universality goal.

**v2** — Comprehensive multi-node design: per-milestone `VerifyMilestone` check, `FinalSpecCheck` repo-wide sweep, reviewer rubric upgrade, `.ai/pending_wiring.md` cross-milestone obligation tracking, `.ai/decisions/library_api.md` carve-out, four-condition waiver discipline, transitive-3-hop reachability rule, per-language enumeration (6 languages), per-method markdown table format. Rejected because:

- Triple-redundant programmatic coverage (~5N method-checks for N methods) with diffuse ownership (squad review CRIT-1, Architect)
- `parseAutoStatus` is last-STATUS-wins and defaults to `success` on empty — v2's check was fail-open by default, the *exact* failure mode of the original bug (squad review CRIT-1, Prompt-Pragmatist)
- `.ai/pending_wiring.md` was a workaround for a self-inflicted scoping issue; FinalSpecCheck's repo-wide sweep does the same work without the file (squad review CRIT-3)
- LLM-managed file mutation degrades multiplicatively per milestone (~17% reliability after 5 milestones) (squad review CRIT-3, Prompt-Pragmatist finding #5)
- "Language-agnostic" claim didn't survive contact with Java, C++, Swift, Ruby, Elixir, Zig, plain JS, or C — the 6-language enumeration was an arbitrary slice (squad review CRIT-4, Generalizability)
- Four-condition waiver discipline's condition (c) "authored before this milestone via `git log`" was unverifiable as written and degraded cumulative reliability to ~15-25% (squad review IMPORTANT-5)
- Per-method markdown table format created parser-fragility risk and added drift across five prompt copies (squad review IMPORTANT-6)
- 3-hop transitive reach via grep is impossible in practice; agents would fake it (squad review IMPORTANT-7)
- Reviewer rubric point 2 inflated to ~35 lines, breaking PR #249's balanced 5-point structure (squad review IMPORTANT-8)
- ~80 lines of grep-discipline duplicated across 5 prompts re-introduced exactly the drift pattern PR #246 round-5 addressed (squad review IMPORTANT-9)

**v3 (this document)** addresses all five CRITICAL and all four IMPORTANT squad-review findings while keeping the named-bug-catching contract.

## 9. References

- Issue #233 — `build_product` audit recap. Appendix A items I9, I10 are the named regression cases.
- PR #246 — closed Gaps 1, 3, 6. Established the `Setup`-writes-shared-helper pattern (`ci-probe.sh`) and the `.ai/decisions/` waiver convention that v3 extends.
- PR #249 — closed Gaps 2, 4. Established the balanced 5-point reviewer rubric that v3 strengthens point 2 of (without inflating its size).
- [CLAUDE.md](../../../CLAUDE.md) — repo-level instructions including `make sync-workflows` discipline.

## 10. Open questions

None at writing time. Squad-review v2 findings are addressed inline. Implementation begins next.
