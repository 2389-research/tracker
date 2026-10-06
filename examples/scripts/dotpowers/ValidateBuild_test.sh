#!/usr/bin/env bash
# ABOUTME: Fixture tests for ValidateBuild.sh (#646 item 8) — every detected
# ABOUTME: stack runs (not first-match), a red vet/mypy/eslint/clippy is a
# ABOUTME: FAIL (never `*-skipped`), and no known build system fails loud
# ABOUTME: instead of `validation-unknown` exit 0 → reviews → ship.
set -uo pipefail
DIR="$(cd "$(dirname "$0")" && pwd)"
fail=0
check() { # name expected actual
  if [ "$2" = "$3" ]; then echo "ok: $1"; else echo "FAIL: $1 — want '$2' got '$3'"; fail=1; fi
}
WORK="$(mktemp -d)"
STATE="$(mktemp -d)"
trap 'rm -rf "$WORK" "$STATE"' EXIT
. "$DIR/test_helpers.sh"
install_tool_shims
SCRIPT="$(stage_script "$DIR/ValidateBuild.sh")"
run() { OUT="$( (cd "$WORK" && PATH="$STATE/bin:$PATH" ${TEST_SH:-sh} "$SCRIPT") 2>&1)"; RC=$?; }

# 1. No build system -> exit 1 with a diagnostic (routes ValidateBuild -> fail).
run
check "no stack exit 1"            "1" "$RC"
check "no stack diagnostic"        "yes" "$(has_re 'no known build system')"
check "no stack no validation-pass" "no" "$(has_re 'validation-pass')"

# 2. Polyglot: go.mod + package.json -> BOTH stacks run.
touch "$WORK/go.mod" "$WORK/package.json"
reset_rc; run
check "polyglot exit 0"            "0" "$RC"
check "polyglot ran go test"       "yes" "$(contains "$(calls)" 'go test ./...')"
check "polyglot ran npm test"      "yes" "$(contains "$(calls)" 'npm test')"
check "polyglot marker"            "yes" "$(has_re 'validation-pass')"

# 3. A red `go vet` is a FAIL, not vet-skipped.
reset_rc; set_rc go vet 1; run
check "vet red exit 1"             "1" "$RC"
check "vet red marker"             "yes" "$(has_re 'VET_FAIL')"
check "vet red not skipped"        "no" "$(has_re 'vet-skipped')"

# 4. Red tests in the SECOND stack still fail the node (no first-match).
reset_rc; set_rc npm test 1; run
check "npm red exit 1"             "1" "$RC"
check "npm red marker"             "yes" "$(has_re 'TEST_FAIL')"

# 5. eslint runs only when configured; a red eslint is a FAIL.
rm -f "$WORK/go.mod"
reset_rc; run
check "no eslint config -> note"   "yes" "$(has_re 'no linter configured')"
touch "$WORK/eslint.config.js"
reset_rc; set_rc npx eslint 1; run
check "eslint red exit 1"          "1" "$RC"
check "eslint red marker"          "yes" "$(has_re 'LINT_FAIL')"
check "eslint red not skipped"     "no" "$(has_re 'lint-skipped')"

# 6. Python: mypy runs when configured and a red mypy is a FAIL.
rm -f "$WORK/package.json" "$WORK/eslint.config.js"
printf '[tool.mypy]\nstrict = true\n' > "$WORK/pyproject.toml"
reset_rc; set_rc uv mypy 1; run
check "mypy red exit 1"            "1" "$RC"
check "mypy red marker"            "yes" "$(has_re 'MYPY_FAIL')"
check "mypy red not skipped"       "no" "$(has_re 'mypy-skipped')"
reset_rc; run
check "python green exit 0"        "0" "$RC"

# 7. Rust: a red clippy is a FAIL.
rm -f "$WORK/pyproject.toml"; touch "$WORK/Cargo.toml"
reset_rc; set_rc cargo clippy 1; run
check "clippy red exit 1"          "1" "$RC"
check "clippy red marker"          "yes" "$(has_re 'CLIPPY_FAIL')"
reset_rc; run
check "rust green exit 0"          "0" "$RC"
check "rust green marker"          "validation-pass" "$(printf '%s' "$OUT" | tail -1)"

if [ "$fail" = 0 ]; then echo "ALL PASS"; else echo "SOME FAILED"; exit 1; fi
