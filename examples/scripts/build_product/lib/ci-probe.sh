# Source this file (verify.sh does; never execute it). Provides:
#   detect_stacks            — every build stack in the tree, one per line as
#                              `<kind>\t<dir>` (kind: go|npm|python|cargo)
#   hatch_lines FILE OUT     — the sanitized operator-hatch entries of FILE → OUT
#   run_project_ci_gate      — Makefile ci/check/lint/test target (if any,
#                              BLOCKING) AND the language-native gates
#                              (ADVISORY — run and reported, never blocking)
#   run_language_native_gates
#
# run_project_ci_gate returns:
#   0  — the project's own CI target passed, or there was none. The
#        language-native gates NEVER affect this: they are ADVISORY
#        (tracker-runner convergence, tracker-strategy §3/§7). The
#        pipeline-IMPOSED whole-tree static checks — go vet / golangci-lint /
#        tsc / eslint / ruff / mypy / cargo fmt|clippy — are LINT, not the
#        acceptance oracle; the milestone's own TESTS are. A whole-tree
#        `mypy`/`clippy`/`tsc`/`ruff check .` blocking a milestone drove the
#        fix loop on style the milestone never touched, so
#        run_language_native_gates always returns 0: it still RUNS every
#        gate, prints the findings, and ends with an `ADVISORY:` line on
#        stderr when any reported. A project that WANTS lint to gate declares
#        its own `make ci`/`check`/`lint`/`test` target — that project-authored
#        oracle is what blocks here.
#   1  — the Makefile target failed. A failing `make` collapses to exactly 1
#        — make's own exit (its native 2 on ANY recipe error) is never
#        propagated (#320). Never an arbitrary N. Also 1 when a Makefile is
#        present but `make` is not installed — that ENVIRONMENT case is
#        signalled OUT OF BAND (#640 E8): the line `_TRACKER_CI_MAKE_MISSING`
#        is printed and the file .ai/build/ci-make-missing is created.
#        TestMilestone keys its escalate route on that file, never on an exit
#        number (a missing script gives dash rc 2 / bash rc 127 — numbers
#        collide).
# Sets PROJECT_CI_RAN to the chosen make target when one ran, "" otherwise
# (verify.sh counts a make run as a real oracle for its green verdict).
#
# STACK DETECTION (#640 D1): stacks are found anywhere in the tree — every
# tracked or untracked (non-ignored) go.work / go.mod / package.json /
# pyproject.toml / Cargo.toml, excluding node_modules/, vendor/, .ai/,
# .tracker/, testdata/ — and each stack's gate runs IN ITS OWN DIRECTORY.
# A go.work subsumes the go.mod files beneath it (the workspace root runs
# `./...` for all of them). Root-only detection let `backend/go.mod` +
# `frontend/package.json` pass as "no known build system".
# The `*/x` specs are GIT PATHSPECS (a pathspec `*` matches across `/`, so
# `*/go.mod` finds `services/api/go.mod` too) — they must reach git
# LITERALLY. Word-splitting them unquoted under normal shell globbing let
# the shell expand `*/go.mod` against cwd first: whenever ANY first-level
# dir held a go.mod, only those paths reached git and every deeper manifest
# was silently dropped (`backend/go.mod` + `services/api/go.mod` detected
# only backend; remove backend/go.mod and services/api appeared). The
# expansion below runs under `set -f` so the specs stay wildcards.
# `.venv` / `venv` / `site-packages` are excluded like node_modules: a
# dependency's pyproject.toml inside a hook-created virtualenv is not a
# stack of this project (verify.sh's manifest-free find already pruned
# them; the manifest path must agree).
STACK_MANIFEST_SPECS="go.work */go.work go.mod */go.mod package.json */package.json pyproject.toml */pyproject.toml Cargo.toml */Cargo.toml"
STACK_EXCLUDE_RE='(^|/)(node_modules|vendor|\.ai|\.tracker|\.git|testdata|\.venv|venv|site-packages)/'

# list_stack_manifests — every candidate manifest path (relative, sorted,
# unique). git-aware when inside a repo (tracked ∪ untracked-not-ignored;
# an index entry whose file was deleted from the worktree is dropped — the
# stack is gone, not merely uncommitted); a plain find otherwise (fixtures /
# a not-yet-initialised tree).
list_stack_manifests() {
  if git rev-parse --is-inside-work-tree >/dev/null 2>&1; then
    # `set -f` (no pathname expansion) around the word-split so `*/go.mod`
    # reaches git as a pathspec, never as the shell's cwd glob result. Runs
    # in a subshell so the caller's globbing state is untouched.
    # shellcheck disable=SC2086  # STACK_MANIFEST_SPECS is a fixed literal word list
    (
      set -f
      git ls-files -- $STACK_MANIFEST_SPECS 2>/dev/null
      git ls-files --others --exclude-standard -- $STACK_MANIFEST_SPECS 2>/dev/null
    ) | while IFS= read -r m; do [ -f "$m" ] && printf '%s\n' "$m"; done
  else
    find . -type f \( -name go.work -o -name go.mod -o -name package.json \
      -o -name pyproject.toml -o -name Cargo.toml \) 2>/dev/null | sed 's|^\./||'
  fi | grep -vE "$STACK_EXCLUDE_RE" | sort -u
}

detect_stacks() {
  MANIFESTS=$(list_stack_manifests)
  OLD_IFS=$IFS
  IFS='
'
  # Pass 1: go.work roots (each subsumes every go.mod beneath it).
  WORK_ROOTS=""
  for m in $MANIFESTS; do
    case "$m" in go.work|*/go.work) ;; *) continue ;; esac
    case "$m" in */*) d="${m%/*}" ;; *) d="." ;; esac
    WORK_ROOTS="$WORK_ROOTS
$d"
    printf 'go\t%s\n' "$d"
  done
  # Pass 2: everything else.
  for m in $MANIFESTS; do
    case "$m" in */*) d="${m%/*}" ;; *) d="." ;; esac
    case "$m" in
      go.work|*/go.work) ;;
      go.mod|*/go.mod)
        for w in $WORK_ROOTS; do
          [ "$w" = "." ] && continue 2
          case "$d/" in "$w"/*) continue 2 ;; esac
        done
        printf 'go\t%s\n' "$d" ;;
      *package.json)   printf 'npm\t%s\n' "$d" ;;
      *pyproject.toml) printf 'python\t%s\n' "$d" ;;
      *Cargo.toml)     printf 'cargo\t%s\n' "$d" ;;
    esac
  done
  IFS=$OLD_IFS
}

# hatch_lines FILE OUT — write the usable entries of an operator hatch file
# (.ai/milestones/known_failures / known_lint_failures) to OUT, one per
# line: CR and surrounding whitespace stripped; blank, whitespace-only and
# `#` comment lines dropped; any entry starting with `-` REJECTED with a
# WARNING on stdout (#640 D5/E2 — a hatch entry must never become a flag:
# `SA1019 --fix` used to reach golangci-lint's argv and rewrite source).
# OUT is empty when FILE is absent. Entries are only ever passed as ONE
# quoted argument or written into a config file — never word-split, never
# eval'd.
hatch_lines() {
  : > "$2"
  [ -f "$1" ] || return 0
  while IFS= read -r pat || [ -n "$pat" ]; do
    pat=$(printf '%s' "$pat" | tr -d '\r' | sed 's/^[[:space:]]*//; s/[[:space:]]*$//')
    case "$pat" in
      ''|\#*) continue ;;
      -*) echo "WARNING: ignoring ${1##*/} entry '$pat' — an entry may not start with '-' (it would be read as a flag)"; continue ;;
    esac
    printf '%s\n' "$pat" >> "$2"
  done < "$1"
}

# makefile_has_target MAKEFILE TARGET — true when MAKEFILE defines a
# top-level rule named TARGET:
#   - skip tab-prefixed recipe lines
#   - skip variable assignments (first ':' part of := / ::= / :::=)
#   - tokenize everything before the first ':' as the target list
# Handles `ci:`, `ci check lint:`, `ci: VAR := overridden`, rejects
# `ci := value` and substring collisions like `build-ci:`.
makefile_has_target() {
  sed 's/#.*//' "$1" | awk -v t="$2" '
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
  '
}

# find_makefile — set MAKEFILE to the Makefile GNU make would pick, "" if none.
# GNU make's own lookup order (#640 D10): GNUmakefile, makefile, Makefile.
# `find -name` (not `[ -f ]`) so a case-insensitive filesystem (macOS)
# reports the on-disk spelling rather than the first probe that matched.
find_makefile() {
  MAKEFILE=""
  for mf in GNUmakefile makefile Makefile; do
    if [ -f "$mf" ] && [ "$(find . -maxdepth 1 -name "$mf" 2>/dev/null | head -1)" = "./$mf" ]; then MAKEFILE="$mf"; break; fi
  done
}

# makefile_has_ci_target — true when a Makefile defines a ci/check/lint/test
# target (sets MAKEFILE_CI_TARGET). verify.sh counts that as a test stack
# for a manifest-less repo (#640 D1 review): run_project_ci_gate runs it.
# shellcheck disable=SC2034  # MAKEFILE_CI_TARGET is read by verify.sh
makefile_has_ci_target() {
  MAKEFILE_CI_TARGET=""
  find_makefile
  [ -n "$MAKEFILE" ] || return 1
  for t in ci check lint test; do
    if makefile_has_target "$MAKEFILE" "$t"; then MAKEFILE_CI_TARGET="$t"; return 0; fi
  done
  return 1
}

run_project_ci_gate() {
  PROJECT_CI_RAN=""
  GATE_RC=0
  # Passed explicitly via -f so the file we parsed is the file make runs.
  find_makefile
  if [ -z "$MAKEFILE" ]; then
    echo "INFO: no Makefile present — running language-native gates only"
  elif ! command -v make >/dev/null 2>&1; then
    echo "ERROR: $MAKEFILE present but 'make' not installed — escalating (environment problem; the fix loop cannot install a binary)"
    echo "_TRACKER_CI_MAKE_MISSING"
    [ -d .ai/build ] || mkdir -p .ai/build 2>/dev/null
    : > .ai/build/ci-make-missing 2>/dev/null || echo "WARNING: could not write .ai/build/ci-make-missing — TestMilestone will treat this as an ordinary red attempt"
    return 1
  else
    for TARGET in ci check lint test; do
      if makefile_has_target "$MAKEFILE" "$TARGET"; then
        echo "--- running make -f $MAKEFILE $TARGET (project CI gate) ---"
        PROJECT_CI_RAN="$TARGET"
        # `|| GATE_RC=1` keeps a bare set -e caller from aborting mid-function
        # and collapses make's native exit 2 to 1 (#320).
        make -f "$MAKEFILE" "$TARGET" 2>&1 || GATE_RC=1
        break
      fi
    done
    [ -n "$PROJECT_CI_RAN" ] || echo "INFO: no project CI target in $MAKEFILE (looked for: ci, check, lint, test)"
  fi
  # #640 D8: the language-native gates run IN ADDITION to any Makefile
  # target, never instead of it — an in-tree `lint:\n\t@echo ok` used to
  # hide vet/golangci-lint/tsc/eslint/ruff/clippy findings for the whole run.
  # They are ADVISORY (always return 0): findings are printed for the fix
  # agent / verifier / reviewers to read, but only the Makefile target above
  # decides GATE_RC.
  echo "--- language-native gates (advisory; run in addition to any Makefile target) ---"
  run_language_native_gates
  return "$GATE_RC"
}

# Language-native quality gates (issue #299, epic #308 Phase 2), run for
# EVERY detected stack in that stack's own directory (#640 D1).
#
# ADVISORY, NON-BLOCKING: returns 0 ALWAYS. Every gate still runs and its
# findings are printed; when any reported, one `ADVISORY:` line goes to
# stderr so the signal is visible in the node's stderr stream (gate prompts
# interpolate ${ctx.tool_stderr}) without being mistaken for a failure. The
# milestone's tests are the acceptance oracle (see the header); a project
# that wants lint to block declares a Makefile ci/check/lint/test target.
#
# INVARIANT: every gate command MUST end in `|| LANG_RC=1` (or run inside a
# helper whose failure is caught that way). Callers may run under set -e;
# FinalBuild's verify runs this gate bare, so a bare failing gate would abort
# the node before later gates/markers run.
# "Core" tools report when they FAIL; "optional" tools are command-v guarded
# so ABSENCE is a one-line INFO skip (never a finding).
run_language_native_gates() {
  LANG_RC=0
  RAN_ANY=""
  STACKS_TMP=$(mktemp) || return 1
  detect_stacks > "$STACKS_TMP"
  while IFS="$(printf '\t')" read -r kind dir <&3; do
    [ -n "$kind" ] || continue
    RAN_ANY=1
    case "$kind" in
      go)     native_gate_go "$dir"     </dev/null || LANG_RC=1 ;;
      npm)    native_gate_npm "$dir"    </dev/null || LANG_RC=1 ;;
      python) native_gate_python "$dir" </dev/null || LANG_RC=1 ;;
      cargo)  native_gate_cargo "$dir"  </dev/null || LANG_RC=1 ;;
    esac
  done 3< "$STACKS_TMP"
  rm -f "$STACKS_TMP"
  if [ -z "$RAN_ANY" ]; then
    echo "INFO: no recognized toolchain (go.mod/go.work/package.json/pyproject.toml/Cargo.toml) — no language-native gate"
  fi
  if [ "$LANG_RC" -ne 0 ]; then
    echo "ADVISORY: one or more language-native lint/type-check gates reported findings (non-blocking — the milestone's tests are the acceptance oracle). Review above; declare a project 'make ci'/'check'/'lint'/'test' target to gate on lint." >&2
  fi
  return 0
}

# golangci_lint_major — print the major version of the golangci-lint on
# PATH ("1", "2", …) or "" when it cannot be parsed.
golangci_lint_major() {
  golangci-lint version 2>&1 | grep -oE '[0-9]+\.[0-9]+(\.[0-9]+)?' | head -1 | cut -d. -f1
}

# has_golangci_config — true when the current dir or the repo root carries
# a user golangci-lint config (v2 cannot merge two configs).
has_golangci_config() {
  for c in .golangci.yml .golangci.yaml .golangci.toml .golangci.json; do
    [ -f "$c" ] && return 0
    [ -n "${1:-}" ] && [ -f "$1/$c" ] && return 0
  done
  return 1
}

# native_gate_go DIR — `go vet ./...` then golangci-lint in DIR.
# Lint escape hatch (issue #441): entries of .ai/milestones/known_lint_failures
# (repo-relative, read via hatch_lines) suppress matching issues:
#   golangci-lint v1 → one `--exclude <pat>` positional per entry, built with
#     `set -- "$@" --exclude "$pat"` so a pattern with spaces / globs stays
#     ONE argument (#640 D5).
#   golangci-lint v2 → `--exclude` no longer exists (#640 D4); the entries go
#     into a temp config (`linters.exclusions.rules[].text`, path `.*`) passed
#     via --config. v2 cannot merge configs, so when the project already
#     has a .golangci.* file the hatch CANNOT apply: lint runs unsuppressed
#     and the entries that WOULD have matched are reported.
# Milestone scoping (issue #436): LINT_NEW_FROM_REV (set by verify.sh from
# the milestone base) becomes --new-from-rev; FinalBuild leaves it unset.
native_gate_go() {
  ROOT=$(pwd)
  (
    cd "$1" || exit 1
    RC=0
    echo "--- go vet ./... (language-native gate, $1) ---"
    go vet ./... 2>&1 || RC=1
    if ! command -v golangci-lint >/dev/null 2>&1; then
      echo "WARNING: golangci-lint not installed — the (advisory) lint gate is skipped for this run."
      echo "WARNING: install a pinned golangci-lint for reproducible findings (results may differ across environments)."
      exit "$RC"
    fi
    echo "--- golangci-lint run (language-native gate, $1) ---"
    PATS_TMP=$(mktemp) || exit 1
    hatch_lines "$ROOT/.ai/milestones/known_lint_failures" "$PATS_TMP"
    NPATS=$(grep -c . "$PATS_TMP" || true)
    MAJOR=$(golangci_lint_major)
    LINT_CFG=""
    REPORT_UNAPPLIED=""
    set --
    if [ "$NPATS" -gt 0 ]; then
      case "$MAJOR" in
        1)
          while IFS= read -r pat; do
            [ -n "$pat" ] || continue
            set -- "$@" --exclude "$pat"
          done < "$PATS_TMP"
          echo "--- known_lint_failures hatch: $NPATS pattern(s) via --exclude (golangci-lint v$MAJOR) ---" ;;
        2)
          if has_golangci_config "$ROOT"; then
            echo "WARNING: golangci-lint v$MAJOR cannot merge a project .golangci.* config with the known_lint_failures hatch — running WITHOUT the $NPATS hatch pattern(s); matching issues are listed after the run."
            REPORT_UNAPPLIED=1
          else
            LINT_CFG_BASE=$(mktemp) || exit 1
            LINT_CFG="$LINT_CFG_BASE.yml"   # golangci-lint picks the parser by extension
            {
              printf 'version: "2"\nlinters:\n  exclusions:\n    rules:\n'
              while IFS= read -r pat; do
                [ -n "$pat" ] || continue
                q=$(printf '%s' "$pat" | sed "s/'/''/g")
                printf "      - path: '.*'\n        text: '%s'\n" "$q"
              done < "$PATS_TMP"
            } > "$LINT_CFG"
            set -- --config "$LINT_CFG"
            echo "--- known_lint_failures hatch: $NPATS pattern(s) via --config (golangci-lint v$MAJOR; --exclude was removed in v2) ---"
          fi ;;
        *)
          echo "WARNING: could not determine the golangci-lint major version — running WITHOUT the $NPATS known_lint_failures pattern(s); matching issues are listed after the run."
          REPORT_UNAPPLIED=1 ;;
      esac
    fi
    LINT_OUT=$(mktemp) || exit 1
    LRC=0
    if [ -n "${LINT_NEW_FROM_REV:-}" ]; then
      golangci-lint run --new-from-rev "$LINT_NEW_FROM_REV" "$@" > "$LINT_OUT" 2>&1 || LRC=1
    else
      golangci-lint run "$@" > "$LINT_OUT" 2>&1 || LRC=1
    fi
    cat "$LINT_OUT"
    if [ -n "$REPORT_UNAPPLIED" ] && [ "$LRC" -ne 0 ]; then
      echo "--- known_lint_failures entries that WOULD have matched (hatch not applied) ---"
      while IFS= read -r pat; do
        [ -n "$pat" ] || continue
        if grep -E -e "$pat" "$LINT_OUT" >/dev/null 2>&1; then
          echo "  would match: $pat"
        fi
      done < "$PATS_TMP"
    fi
    [ "$LRC" -eq 0 ] || RC=1
    rm -f "$PATS_TMP" "$LINT_OUT"
    [ -z "$LINT_CFG" ] || rm -f "$LINT_CFG" "$LINT_CFG_BASE"
    exit "$RC"
  )
}

# native_gate_npm DIR — tsc / eslint, only when the project opted into the
# tool (config file present). A plain-JS repo (package.json, no tsconfig /
# no eslint config) must NOT fail just because a global tsc/eslint is on
# PATH: bare `tsc --noEmit` exits non-zero on a project with no tsconfig,
# and `eslint .` errors with no config (Codex PR #321 P2, #299).
native_gate_npm() {
  (
    cd "$1" || exit 1
    RC=0
    if [ -f tsconfig.json ]; then
      if command -v tsc >/dev/null 2>&1; then
        echo "--- tsc --noEmit (language-native gate, $1) ---"
        tsc --noEmit 2>&1 || RC=1
      else
        echo "INFO: tsc not installed — skipping (optional)"
      fi
    else
      echo "INFO: no tsconfig.json in $1 — skipping tsc (not a TypeScript project)"
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
        echo "--- eslint . (language-native gate, $1) ---"
        eslint . 2>&1 || RC=1
      else
        echo "INFO: eslint not installed — skipping (optional)"
      fi
    else
      echo "INFO: no eslint config in $1 — skipping eslint (not configured)"
    fi
    exit "$RC"
  )
}

# native_gate_python DIR — ruff / mypy, both run-if-present.
native_gate_python() {
  (
    cd "$1" || exit 1
    RC=0
    if command -v ruff >/dev/null 2>&1; then
      echo "--- ruff check . (language-native gate, $1) ---"
      ruff check . 2>&1 || RC=1
    else
      echo "INFO: ruff not installed — skipping (optional)"
    fi
    if command -v mypy >/dev/null 2>&1; then
      echo "--- mypy . (language-native gate, $1) ---"
      mypy . 2>&1 || RC=1
    else
      echo "INFO: mypy not installed — skipping (optional)"
    fi
    exit "$RC"
  )
}

# native_gate_cargo DIR — cargo fmt --check and clippy. rustfmt/clippy are
# separate toolchain components — `cargo` can be present without them. A
# missing component is a tooling absence (same class as an optional tool
# not installed), not a code defect, so each subcommand is gated on its
# own availability (#299).
native_gate_cargo() {
  (
    cd "$1" || exit 1
    RC=0
    if ! command -v cargo >/dev/null 2>&1; then
      echo "INFO: cargo not installed — skipping (optional)"
      exit 0
    fi
    if cargo fmt --version >/dev/null 2>&1; then
      echo "--- cargo fmt --check (language-native gate, $1) ---"
      cargo fmt --check 2>&1 || RC=1
    else
      echo "INFO: cargo fmt (rustfmt) not installed — skipping (optional)"
    fi
    if cargo clippy --version >/dev/null 2>&1; then
      echo "--- cargo clippy -- -D warnings (language-native gate, $1) ---"
      cargo clippy -- -D warnings 2>&1 || RC=1
    else
      echo "INFO: cargo clippy not installed — skipping (optional)"
    fi
    exit "$RC"
  )
}
