# ABOUTME: Shared build/test gates for the dotpowers-family workflows —
# ABOUTME: ValidateBuild (build + tests + lint per stack) and VerifyTestsFinal.
# ABOUTME: Sourced (never executed) via ${graph.workflow_dir}/scripts/dotpowers/lib.
#
# #646 item 8: the old gate was a first-match if/elif chain (a Go+Node
# polyglot only ever ran Go), a red `go vet`/`mypy`/`eslint`/`clippy`
# printed `*-skipped` and passed, and a tree with no known build system
# printed `validation-unknown` and exited 0 — which the .dips route (`when
# ctx.outcome = success`) straight into the final reviews and CreatePR.
# Now EVERY detected stack runs, every red tool is a FAIL, and no stack is a
# loud exit 1 (routes to CheckReworkBudget like any other red build).
#
# Lint/type tools that need project configuration (eslint/biome, mypy) run
# only when that configuration exists — a missing config is a NOTE, a red
# run is a FAIL. Tools that ship with the toolchain (go vet, ruff via uv,
# cargo clippy) always run.

# detect_stacks — print one stack name per line for each marker file present.
detect_stacks() {
  [ -f pyproject.toml ] && echo python
  [ -f package.json ]   && echo node
  [ -f go.mod ]         && echo go
  [ -f Cargo.toml ]     && echo rust
  return 0
}

# require_stacks — set STACKS (space-separated) or fail loud when empty.
require_stacks() {
  STACKS=$(detect_stacks | tr '\n' ' ')
  if [ -z "$STACKS" ]; then
    echo 'ERROR: no known build system (pyproject.toml / package.json / go.mod / Cargo.toml) — nothing can be validated; fix the project layout or add a build system'
    printf 'validation-unknown'
    exit 1
  fi
  echo "detected stacks: $STACKS"
}

# has_eslint_config — any eslint.config.* / .eslintrc / .eslintrc.* file.
has_eslint_config() {
  for f in eslint.config.* .eslintrc .eslintrc.*; do
    [ -f "$f" ] && return 0
  done
  return 1
}

# gate CMD... — run a command and record a failure in FAILED (never abort
# mid-gate: every stack's diagnostics print before the node goes red).
FAILED=0
gate() { # marker cmd...
  marker=$1; shift
  if ! "$@" 2>&1; then
    echo "$marker"
    FAILED=1
  fi
}

# validate_build — build + tests + lint for every detected stack.
validate_build() {
  echo '=== Running full validation ==='
  require_stacks
  for stack in $STACKS; do
    case "$stack" in
      python)
        echo '--- pytest ---'; gate PYTEST_FAIL uv run pytest -v
        echo '--- ruff ---';   gate RUFF_FAIL uv run ruff check .
        if grep -q '^\[tool\.mypy\]' pyproject.toml 2>/dev/null || [ -f mypy.ini ] || [ -f .mypy.ini ]; then
          echo '--- mypy ---'; gate MYPY_FAIL uv run mypy .
        else
          echo 'NOTE: mypy not configured ([tool.mypy] / mypy.ini) — type check not run'
        fi
        ;;
      node)
        echo '--- npm test ---'; gate TEST_FAIL npm test
        if has_eslint_config; then
          echo '--- eslint ---'; gate LINT_FAIL npx eslint .
        elif [ -f biome.json ] || [ -f biome.jsonc ]; then
          echo '--- biome ---'; gate LINT_FAIL npx biome check .
        else
          echo 'NOTE: no linter configured (eslint.config.* / .eslintrc* / biome.json) — lint not run'
        fi
        ;;
      go)
        echo '--- go build ---'; gate BUILD_FAIL go build ./...
        echo '--- go test ---';  gate TEST_FAIL go test ./...
        echo '--- go vet ---';   gate VET_FAIL go vet ./...
        ;;
      rust)
        echo '--- cargo build ---';  gate BUILD_FAIL cargo build
        echo '--- cargo test ---';   gate TEST_FAIL cargo test
        echo '--- cargo clippy ---'; gate CLIPPY_FAIL cargo clippy
        ;;
    esac
  done
  if [ "$FAILED" -ne 0 ]; then
    printf 'validation-fail'
    exit 1
  fi
  printf 'validation-pass'
}

# verify_tests_final — the pre-ship test gate: every detected stack's tests.
verify_tests_final() {
  echo '=== Final verification before ship ==='
  require_stacks
  for stack in $STACKS; do
    case "$stack" in
      python) echo '--- pytest ---';     gate FINAL_TEST_FAIL uv run pytest -v ;;
      node)   echo '--- npm test ---';   gate FINAL_TEST_FAIL npm test ;;
      go)     echo '--- go test ---';    gate FINAL_TEST_FAIL go test ./... ;;
      rust)   echo '--- cargo test ---'; gate FINAL_TEST_FAIL cargo test ;;
    esac
  done
  if [ "$FAILED" -ne 0 ]; then
    printf 'final-verification-fail'
    exit 1
  fi
  printf 'final-verification-pass'
}
