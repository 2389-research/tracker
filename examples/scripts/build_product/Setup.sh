set -eu
mkdir -p .ai/build .ai/decisions .ai/milestones
echo '.ai/' >> .gitignore 2>/dev/null || true
# #405: seed common build-output patterns so a milestone whose tests
# compile/install artifacts doesn't have them swept into a CommitIfDirty
# checkpoint and then FAILed by Verify as out-of-scope work. This is the
# STATIC half (named/dir artifacts across the toolchains build_product
# targets); arbitrary-named compiled binaries (Go `go build -o <name>` has
# no fixed extension) are caught dynamically in CommitIfDirty. Appended +
# deduped exactly like `.ai/` above; idempotent across re-runs.
for pat in \
  'node_modules/' 'dist/' 'build/' 'target/' 'coverage/' \
  '__pycache__/' '.venv/' 'venv/' '*.pyc' \
  '*.exe' '*.test' '*.out' '*.o' '*.a'; do
  echo "$pat" >> .gitignore 2>/dev/null || true
done
sort -u .gitignore -o .gitignore
# #351: keep tracker run metadata out of the product repo. CommitIfDirty
# runs `git add -A`, which would otherwise commit .tracker/runs/<id>/
# internals (prompt.md/response.md/checkpoint.json) into the user's
# history. Same LOCAL .git/info/exclude treatment the turn-override dir
# gets in ContinueWithMoreTurns (idempotent; safe outside a git repo).
GITDIR=$(git rev-parse --git-dir 2>/dev/null || true)
if [ -n "$GITDIR" ]; then
  mkdir -p "$GITDIR/info"
  grep -qxF ".tracker/" "$GITDIR/info/exclude" 2>/dev/null \
    || echo ".tracker/" >> "$GITDIR/info/exclude"
  # Untrack any .tracker/ files committed by a pre-#351 run — ignore
  # rules only affect UNTRACKED paths, so without this an already-
  # polluted repo keeps committing metadata churn forever. Index-only
  # removal (--cached, working tree untouched); the next CommitIfDirty
  # commits the deletion, cleaning the product history going forward.
  if git ls-files --cached -- .tracker | grep -q .; then
    git rm -r -q --cached .tracker
  fi
fi
# #318: clear any warm-continue cap counter / MaxTurns override left by a
# prior run interrupted before its cleanup. Setup is skipped on checkpoint
# resume (already completed), so a deliberately-resumed run keeps its state;
# only a fresh run starts the continue allowance clean.
rm -rf .tracker/turn_overrides
# #553: adopt a caller-supplied `spec` file input. The engine staged it to
# a fixed, deterministic path (derived from the input name, not the
# untrusted value), so reading that path here is safe — the untrusted
# input value itself never enters this command (it is not interpolated).
if [ -f .tracker/inputs/spec ]; then
  cp .tracker/inputs/spec SPEC.md
fi
if [ ! -f SPEC.md ]; then
  echo "ERROR: SPEC.md not found in repo root."
  echo "build_product builds from a SPEC.md describing what you want."
  echo "Get a starter one with:  tracker init build_product"
  echo "(creates build_product.dip + a SPEC.md you can edit), then re-run."
  exit 1
fi
wc -l SPEC.md | awk '{print $1" lines"}'

# Write the shared project-CI probe (issue #233 Gap 1; refactored
# in PR #246 round-5 per Copilot). TestMilestone and FinalBuild
# source this file so the probe/run logic lives in exactly one
# place — round-4 review found the awk had silently drifted
# between the two nodes; this prevents that class of bug.
cat > .ai/build/ci-probe.sh <<'PROBE_EOF'
# Source this file, then call `run_project_ci_gate`. Returns:
#   0  — clean: `make <TARGET>` exited 0, OR (no Makefile gate
#        ran) the language-native gates passed / no toolchain
#        was detected (issue #299).
#   1  — a gate failed (caller should fail / retry): a failing
#        `make <TARGET>` run collapses to exactly 1 — make's own
#        exit code (including its native exit 2 on ANY recipe
#        error) is never propagated (#320) — and a failing
#        language-native gate (go vet / golangci-lint / tsc /
#        eslint / ruff / mypy / cargo fmt|clippy) likewise
#        collapses to exactly 1. Never an arbitrary N, never 2.
#   2  — Makefile present but `make` not installed (caller
#        should escalate; LLM fix loop can't install a binary).
#        rc=2 is sole-sourced to the `command -v make` check —
#        no other path in this helper can produce it.
# Sets PROJECT_CI_RAN to the chosen target on a real `make` run,
# empty string otherwise (incl. the language-native fall-through).
run_project_ci_gate() {
  PROJECT_CI_RAN=""
  MAKEFILE=""
  for mf in Makefile makefile GNUmakefile; do
    if [ -f "$mf" ]; then MAKEFILE="$mf"; break; fi
  done
  if [ -z "$MAKEFILE" ]; then
    echo "INFO: no Makefile present — running language-native gates"
    run_language_native_gates
    return $?
  fi
  if ! command -v make >/dev/null 2>&1; then
    echo "ERROR: $MAKEFILE present but 'make' not installed — escalating"
    return 2
  fi
  for TARGET in ci check lint; do
    # Parse the Makefile for a top-level target named TARGET:
    #   - skip tab-prefixed recipe lines
    #   - skip variable assignments (first ':' part of := / ::= / :::=)
    #   - tokenize everything before the first ':' as the target list
    # Handles `ci:`, `ci check lint:`, `ci: VAR := overridden`,
    # rejects `ci := value` and substring collisions like `build-ci:`.
    if sed 's/#.*//' "$MAKEFILE" | awk -v t="$TARGET" '
        /^\t/ { next }
        {
          cp = index($0, ":")
          if (cp == 0) next
          if (substr($0, cp) ~ /^:+=/) next
          head = substr($0, 1, cp - 1)
          n = split(head, tokens, /[ \t]+/)
          for (i = 1; i <= n; i++) if (tokens[i] == t) { found = 1; exit }
        }
        END { exit !found }
      '; then
      echo "--- running make $TARGET (project CI gate from $MAKEFILE) ---"
      PROJECT_CI_RAN="$TARGET"
      # `|| MAKE_RC=$?` keeps a bare set -e caller (FinalBuild) from
      # aborting mid-function, mirroring the language-gate invariant.
      MAKE_RC=0
      make "$TARGET" 2>&1 || MAKE_RC=$?
      if [ "$MAKE_RC" -eq 0 ]; then return 0; fi
      # GNU make exits 2 on ANY recipe error; collapse to 1 so a
      # fixable CI failure routes to the fix loop, not human
      # escalation — rc=2 stays sole-sourced to make-missing (#320).
      return 1
    fi
  done
  echo "INFO: no project CI target in $MAKEFILE (looked for: ci, check, lint) — running language-native gates"
  run_language_native_gates
  return $?
}
# Language-native quality-gate fallback (issue #299, epic #308 Phase 2).
# Reached from run_project_ci_gate when no Makefile ci/check/lint target
# ran. Runs gates for EVERY detected toolchain (polyglot, not first-match).
# Returns 0 (clean / nothing to gate) or 1 (some gate failed) — NEVER 2;
# rc=2 stays reserved for the make-missing case in run_project_ci_gate.
#
# INVARIANT: every gate command MUST end in `|| LANG_RC=$?`. These nodes run
# `set -eu`; FinalBuild calls the gate BARE (set -e active in the body), so a
# bare failing gate would abort the node before later gates/markers run. The
# `|| LANG_RC=$?` neutralizes set -e per gate and accumulates the failure.
# "Core" tools fail when they FAIL; "optional" tools are command-v guarded so
# ABSENCE is a one-line INFO skip (never a failure, never rc=2).
run_language_native_gates() {
  LANG_RC=0
  RAN_ANY=""
  # Go — `go vet` is the core gate (go is present by go.mod detection).
  if [ -f go.mod ]; then
    RAN_ANY=1
    echo "--- go vet ./... (language-native gate) ---"
    go vet ./... 2>&1 || LANG_RC=$?
    if command -v golangci-lint >/dev/null 2>&1; then
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
        while IFS= read -r pat || [ -n "$pat" ]; do
          case "$pat" in ''|\#*) continue ;; esac
          LINT_EXCLUDES="$LINT_EXCLUDES --exclude $pat"
        done < .ai/milestones/known_lint_failures
      fi
      golangci-lint run ${LINT_NEW_FROM_REV:+--new-from-rev "$LINT_NEW_FROM_REV"} $LINT_EXCLUDES 2>&1 || LANG_RC=$?
    else
      echo "WARNING: golangci-lint not installed — lint enforcement is DISABLED for this run."
      echo "WARNING: install a pinned golangci-lint for reproducible gating (results may differ across environments)."
    fi
  fi
  # JS/TS — run-if-present AND only if the project opted into the tool
  # (config file present). A plain-JS repo (package.json, no tsconfig /
  # no eslint config) must NOT fail just because a global tsc/eslint is on
  # PATH: bare `tsc --noEmit` exits non-zero on a project with no tsconfig,
  # and `eslint .` errors with no config — gating on PATH alone would
  # mis-route such a repo to the fix loop (Codex PR #321 P2, #299).
  if [ -f package.json ]; then
    RAN_ANY=1
    if [ -f tsconfig.json ]; then
      if command -v tsc >/dev/null 2>&1; then
        echo "--- tsc --noEmit (language-native gate) ---"
        tsc --noEmit 2>&1 || LANG_RC=$?
      else
        echo "INFO: tsc not installed — skipping (optional)"
      fi
    else
      echo "INFO: no tsconfig.json — skipping tsc (not a TypeScript project)"
    fi
    ESLINT_CFG=""
    for ec in eslint.config.js eslint.config.mjs eslint.config.cjs eslint.config.ts .eslintrc.js .eslintrc.cjs .eslintrc.yaml .eslintrc.yml .eslintrc.json .eslintrc; do
      if [ -f "$ec" ]; then ESLINT_CFG="$ec"; break; fi
    done
    if [ -z "$ESLINT_CFG" ] && grep -q '"eslintConfig"' package.json 2>/dev/null; then
      ESLINT_CFG="package.json"
    fi
    if [ -n "$ESLINT_CFG" ]; then
      if command -v eslint >/dev/null 2>&1; then
        echo "--- eslint . (language-native gate) ---"
        eslint . 2>&1 || LANG_RC=$?
      else
        echo "INFO: eslint not installed — skipping (optional)"
      fi
    else
      echo "INFO: no eslint config — skipping eslint (not configured)"
    fi
  fi
  # Python — both run-if-present.
  if [ -f pyproject.toml ]; then
    RAN_ANY=1
    if command -v ruff >/dev/null 2>&1; then
      echo "--- ruff check . (language-native gate) ---"
      ruff check . 2>&1 || LANG_RC=$?
    else
      echo "INFO: ruff not installed — skipping (optional)"
    fi
    if command -v mypy >/dev/null 2>&1; then
      echo "--- mypy . (language-native gate) ---"
      mypy . 2>&1 || LANG_RC=$?
    else
      echo "INFO: mypy not installed — skipping (optional)"
    fi
  fi
  # Rust — run-if-present (cargo gates both fmt and clippy).
  if [ -f Cargo.toml ]; then
    RAN_ANY=1
    if command -v cargo >/dev/null 2>&1; then
      # rustfmt/clippy are separate toolchain components — `cargo` can be
      # present without them. A missing component is a tooling absence
      # (same class as an optional tool not installed), not a code defect,
      # so gate each subcommand on its own availability (#299).
      if cargo fmt --version >/dev/null 2>&1; then
        echo "--- cargo fmt --check (language-native gate) ---"
        cargo fmt --check 2>&1 || LANG_RC=$?
      else
        echo "INFO: cargo fmt (rustfmt) not installed — skipping (optional)"
      fi
      if cargo clippy --version >/dev/null 2>&1; then
        echo "--- cargo clippy -- -D warnings (language-native gate) ---"
        cargo clippy -- -D warnings 2>&1 || LANG_RC=$?
      else
        echo "INFO: cargo clippy not installed — skipping (optional)"
      fi
    else
      echo "INFO: cargo not installed — skipping (optional)"
    fi
  fi
  if [ -z "$RAN_ANY" ]; then
    echo "INFO: no recognized toolchain (go.mod/package.json/pyproject.toml/Cargo.toml) — no language-native gate"
  fi
  if [ "$LANG_RC" -ne 0 ]; then
    return 1
  fi
  return 0
}
PROBE_EOF

# Shared milestone green-gate (issue #406). TestMilestone wraps this with
# the fix-attempt counter + tests-pass/escalate sentinels; the
# Implement/FixMilestone breach verify_command runs the SAME script, so a
# turn-limit breach on a green tree classifies verified_green and commits
# the work instead of abandoning it. One source of truth — the gate logic
# lives here, exactly as it did inline in TestMilestone (mirrors the
# ci-probe.sh single-place discipline above).
cat > .ai/build/verify.sh <<'VERIFY_EOF'
# Run this directly (`sh .ai/build/verify.sh`) — do NOT source it. It
# sources .ai/build/ci-probe.sh internally for the project CI gate. Exit:
#   0  green: build + every detected stack's tests + project CI gate pass
#   2  Makefile present but `make` not installed (environment; escalate) —
#      RESERVED for that env-missing case ONLY. TestMilestone routes exit 2
#      straight to `escalate`, so no language test runner's exit may reach it.
#   1  any build/test/CI failure (normal fix-loop failure). Every non-zero
#      TEST-runner exit collapses to 1 below (a runner exiting 2 — e.g. a
#      pytest collection error — must NOT masquerade as the make-missing 2).
set -eu
TEST_EXIT=0

# Build the skip pattern from known_failures, stripping comments and blanks.
SKIP_PATTERN=""
if [ -f .ai/milestones/known_failures ]; then
  SKIP_PATTERN=$(grep -v '^#' .ai/milestones/known_failures | grep -v '^$' | paste -sd'|' -)
fi

# Run EVERY detected stack, not just the first match (issue #305).
# Each runner's non-zero exit collapses to TEST_EXIT=1 (NOT $?) so a runner
# that legitimately exits 2 (e.g. a pytest collection error) can't be
# mistaken for the make-missing escalate (exit 2) reserved below (PR #411).
# The 1 is sticky: once a stack fails a later passing stack can't reset it,
# so failures still can't be masked. `go build` stays unguarded: under set -eu a Go
# compile failure aborts immediately (exit 1, normal fix-loop failure).
# Milestone-scoped Go test target (issue #392): scope `go test` to the
# packages this milestone changed since .ai/build/milestone-start-sha; the
# WHOLE-tree suite still runs at FinalBuild. The derived package tokens are
# ONLY ever passed as `go test` package arguments — never eval'd.
if [ -f go.mod ]; then
  go build ./... 2>&1
  GO_TEST_TARGET="./..."
  MS_START=$(cat .ai/build/milestone-start-sha 2>/dev/null || true)
  if [ -z "$MS_START" ] || ! git cat-file -e "${MS_START}^{commit}" 2>/dev/null; then
    MS_BASE=$(git hash-object -t tree /dev/null)
  else
    MS_BASE="$MS_START"
    # Milestone-scope the lint gate too (issue #436) — only when we have a
    # real base commit (never the empty-tree fallback, which is not a rev
    # golangci-lint --new-from-rev accepts). ci-probe.sh, sourced below in
    # the same shell, reads this to pass --new-from-rev.
    LINT_NEW_FROM_REV="$MS_START"
    export LINT_NEW_FROM_REV
  fi
  CHANGED_PKGS=$(git diff --name-only --diff-filter=d "${MS_BASE}..HEAD" 2>/dev/null \
    | grep -E '\.go$' \
    | awk -F/ 'NF==1 { print "." } NF>1 { sub(/\/[^/]*$/, ""); print "./" $0 }' \
    | sort -u \
    | paste -sd' ' -)
  if [ -n "$CHANGED_PKGS" ]; then
    GO_TEST_TARGET="$CHANGED_PKGS"
    echo "--- milestone-scoped go test: $GO_TEST_TARGET ---"
  else
    echo "--- no changed Go packages in milestone range — testing ./... ---"
  fi
  if [ -n "$SKIP_PATTERN" ]; then
    echo "--- skipping known failures: $SKIP_PATTERN ---"
    go test $GO_TEST_TARGET -skip "$SKIP_PATTERN" 2>&1 || TEST_EXIT=1
  else
    go test $GO_TEST_TARGET 2>&1 || TEST_EXIT=1
  fi
fi
if [ -f package.json ]; then
  npm test 2>&1 || TEST_EXIT=1
fi
if [ -f pyproject.toml ]; then
  uv run pytest 2>&1 || TEST_EXIT=1
fi
if [ -f Cargo.toml ]; then
  cargo test 2>&1 || TEST_EXIT=1
fi
if [ ! -f go.mod ] && [ ! -f package.json ] && [ ! -f pyproject.toml ] && [ ! -f Cargo.toml ]; then
  echo "no known build system — skipping tests"
fi

# Project CI gate (issue #233 Gap 1). Source the shared helper; rc=2 means
# a Makefile is present but `make` isn't installed — an environment problem
# the LLM fix loop can't resolve, surfaced as exit 2 so the caller escalates.
. .ai/build/ci-probe.sh
CI_RC=0
run_project_ci_gate || CI_RC=$?
if [ "$CI_RC" -eq 2 ]; then
  exit 2
fi
if [ "$CI_RC" -ne 0 ]; then
  # Non-2 CI failure → normal fix-loop failure (exit 2 already handled above
  # and is reserved for make-missing). Collapse to 1, never propagate raw.
  TEST_EXIT=1
fi
exit "$TEST_EXIT"
VERIFY_EOF

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
TypeScript (.ts/.tsx), C++ (.cc/.cpp/.cxx/.hh/.hpp/.hxx),
C# (.cs), PHP (.php), Python with `ABC` or `Protocol` (.py) —
proceed. Note: `.h` alone is NOT a C++ trigger because the
extension is ambiguous between C (no static interfaces) and
C++ (has them). Header-only C++ projects using only `.h` are
a known blind spot — operators can declare via `.ai/decisions/`
or rename to `.hpp`/`.hxx`. The enumeration grep `--include`
list does include `.h` so that mixed-source C++ projects with
`.cpp` + `.h` still get their headers scanned.

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
  Rust:    grep -rnE '(^|[^[:alnum:]_])trait +[[:alnum:]_]+' \
             --include='*.rs' .
           Catches `trait`, `pub trait`, `pub(crate) trait`,
           and unexported traits — all can carry unwired
           methods. The `(^|[^[:alnum:]_])` prefix is a
           portable word-boundary (POSIX ERE doesn't define
           `\b`; GNU grep supports it but BSD/macOS does not).
  Java:    grep -rnE '^(public |abstract |sealed )*(interface |abstract class )' \
             --include='*.java' .
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

# #418: capture the run's base commit ONCE at Setup so the cross-review
# diff node can compute a cumulative base..worktree without a per-milestone
# marker (those are deleted at each MarkMilestoneDone). Same non-leaking
# idiom as PickNextMilestone's start marker: --verify --quiet prints
# nothing and exits non-zero on a commitless repo, so a fresh repo
# genuinely records an empty base. Goes to a FILE so the routing marker
# below stays last on stdout.
RUN_BASE=$(git rev-parse --verify --quiet HEAD 2>/dev/null || true)
printf '%s\n' "$RUN_BASE" > .ai/build/run-base-sha

# Spec-forge loop hygiene (fresh-run reset). Setup is skipped on
# checkpoint resume, so an intentional resume keeps loop state — but a NEW
# run in a workdir left dirty by a prior abandoned run must not inherit a
# poisoned budget counter or a stale original-spec snapshot (PR #264).
rm -f .ai/build/spec_forge_attempts
rm -f .ai/decisions/SPEC.original.md .ai/decisions/spec-forge-log.md

printf 'setup-ready'