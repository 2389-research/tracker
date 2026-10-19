#!/usr/bin/env bash
# ABOUTME: Fixture tests for quickfix's RunTests.sh — detects the project's test command in
# ABOUTME: the worktree, runs it with a timeout, prints tests-pass/tests-fail/no-tests, exits 0.
set -uo pipefail
DIR="$(cd "$(dirname "$0")" && pwd)"
. "$DIR/fixture_helpers.sh"
SCRIPT="$(stage_script "$DIR/RunTests.sh")"
install_tool_shims
export PATH="$STATE/bin:$PATH"

# fresh_wt FILE… — an empty worktree dir holding only the given marker files.
fresh_wt() { rm -rf "$WT"; mkdir -p "$WT"; for f; do : > "$WT/$f"; done; : > "$STATE/calls"; reset_rc; }

# 1. No worktree: a Preflight invariant broke — fail loudly (exit 1).
rm -rf "$WT"
qf_run "$SCRIPT"
check "no worktree exits 1"             "1" "$RC"

# 2. Nothing to run.
fresh_wt
qf_run "$SCRIPT"
check "none: exit 0"                    "0" "$RC"
check "none: marker"                    "no-tests" "$(last_line "$OUT")"

# 3. A Makefile test target wins over go.mod.
fresh_wt go.mod
printf 'test:\n\t@echo hi\n' > "$WT/Makefile"
qf_run "$SCRIPT"
check "make: ran make test"             "yes" "$(contains "$(calls)" 'make test')"
check "make: go not run"                "no" "$(contains "$(calls)" 'go test')"
check "make: marker"                    "tests-pass" "$(last_line "$OUT")"

# 4. A Makefile without a test target falls through to go.mod.
fresh_wt go.mod
printf 'build:\n\t@echo hi\n' > "$WT/Makefile"
qf_run "$SCRIPT"
check "go: ran go test ./..."           "yes" "$(contains "$(calls)" 'go test ./...')"
check "go: marker"                      "tests-pass" "$(last_line "$OUT")"

# 5. A failing suite: tests-fail, still exit 0, output shown.
fresh_wt go.mod
set_rc go test 1
set_out go test '--- FAIL: TestX'
qf_run "$SCRIPT"
check "go fail: exit 0"                 "0" "$RC"
check "go fail: marker"                 "tests-fail" "$(last_line "$OUT")"
check "go fail: output shown"           "yes" "$(has 'FAIL: TestX')"

# 6. package.json with a test script → npm test; without one → no-tests.
fresh_wt
printf '{"scripts": {"test": "jest"}}\n' > "$WT/package.json"
qf_run "$SCRIPT"
check "npm: ran npm test"               "yes" "$(contains "$(calls)" 'npm test')"
check "npm: marker"                     "tests-pass" "$(last_line "$OUT")"
fresh_wt
printf '{"scripts": {"build": "tsc"}}\n' > "$WT/package.json"
qf_run "$SCRIPT"
check "npm no script: no-tests"         "no-tests" "$(last_line "$OUT")"

# 7. pyproject.toml / pytest.ini → pytest.
fresh_wt pyproject.toml
qf_run "$SCRIPT"
check "pytest: ran pytest"              "yes" "$(contains "$(calls)" 'pytest')"
fresh_wt pytest.ini
qf_run "$SCRIPT"
check "pytest.ini: ran pytest"          "yes" "$(contains "$(calls)" 'pytest')"

# 8. Cargo.toml → cargo test.
fresh_wt Cargo.toml
qf_run "$SCRIPT"
check "cargo: ran cargo test"           "yes" "$(contains "$(calls)" 'cargo test')"

# 9. A hanging suite hits the timeout: tests-fail with a timeout line, exit 0.
fresh_wt
printf 'test:\n\t@sleep 30\n' > "$WT/Makefile"
cat > "$STATE/bin/make" <<'SHIM'
#!/bin/sh
exec sleep 30
SHIM
chmod +x "$STATE/bin/make"
START=$(date +%s)
qf_run "$SCRIPT" QUICKFIX_TEST_TIMEOUT=2
ELAPSED=$(( $(date +%s) - START ))
check "timeout: exit 0"                 "0" "$RC"
check "timeout: marker"                 "tests-fail" "$(last_line "$OUT")"
check "timeout: says so"                "yes" "$(has 'timed out after 2s')"
check "timeout: did not wait it out"    "yes" "$([ "$ELAPSED" -lt 20 ] && echo yes || echo no)"

# 10. A junk timeout value falls back to the default instead of erroring.
fresh_wt
qf_run "$SCRIPT" QUICKFIX_TEST_TIMEOUT=soon
check "junk timeout: exit 0"            "0" "$RC"

exit "$fail"
