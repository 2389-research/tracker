#!/usr/bin/env bash
# ABOUTME: Fixture tests for FinalBuild.sh (#305/#233 Gap 1) — whole-tree
# ABOUTME: sweep of EVERY detected stack (failures accumulate, all run), then
# ABOUTME: the shared project-CI gate; any non-zero (incl. rc=2 make-missing)
# ABOUTME: is a node failure and the marker is only printed on full green.
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
SCRIPT="$(stage_script "$DIR/FinalBuild.sh")"   # ${graph.workflow_dir} expanded as the engine does
# PATH shims: each toolchain logs its argv and exits with the per-tool code in
# $STATE/rc-<tool>-<subcommand> (default 0). Never a real go/npm/uv/cargo.
mkdir -p "$STATE/bin"
for tool in go npm uv cargo; do
  cat > "$STATE/bin/$tool" <<SHIM
#!/bin/sh
echo "$tool \$*" >> "$STATE/calls"
rc="$STATE/rc-$tool-\$1"
[ -f "\$rc" ] && exit "\$(cat "\$rc")"
exit 0
SHIM
  chmod +x "$STATE/bin/$tool"
done
set_rc() { echo "$3" > "$STATE/rc-$1-$2"; }
# ci-probe.sh is Setup's artifact (its real body is exercised in
# Setup_test.sh); a stub returning the code in .ai/build/ci-rc isolates
# FinalBuild's own control flow.
mkdir -p "$WORK/.ai/build"
cat > "$WORK/.ai/build/ci-probe.sh" <<'STUB'
run_project_ci_gate() { echo "ci gate ran"; return "$(cat .ai/build/ci-rc)"; }
STUB
set_ci() { echo "$1" > "$WORK/.ai/build/ci-rc"; }
run() { rm -f "$STATE/calls"; OUT="$( (cd "$WORK" && PATH="$STATE/bin:$PATH" sh "$SCRIPT") 2>"$STATE/stderr")"; RC=$?; }
last() { printf '%s' "$OUT" | tail -1; }
calls() { [ -f "$STATE/calls" ] && paste -sd';' "$STATE/calls" || echo ""; }
reset_rc() { rm -f "$STATE"/rc-*; }

# 1. No build system, CI green: only the CI gate runs; marker last.
set_ci 0
run
check "no stack exit 0"               "0" "$RC"
check "no stack marker last"          "final-build-pass" "$(last)"
check "no stack no toolchain calls"   "" "$(calls)"
check "ci gate ran"                   "yes" "$(printf '%s' "$OUT" | grep -q 'ci gate ran' && echo yes || echo no)"

# 2. All four stacks green: every runner runs (go build BEFORE go test).
touch "$WORK/go.mod" "$WORK/package.json" "$WORK/pyproject.toml" "$WORK/Cargo.toml"
run
check "all stacks exit 0"             "0" "$RC"
check "all stacks marker"             "final-build-pass" "$(last)"
check "all runners in order"          "go build ./...;go test ./...;npm test;uv run pytest;cargo test" "$(calls)"

# 3. #305 sweep: go test AND npm test fail — BOTH still run, cargo/uv still
#    run, exit 1, no marker, and the CI gate is NOT reached.
set_rc go test 1; set_rc npm test 1
run
check "sweep exit 1"                  "1" "$RC"
check "sweep no marker"               "no" "$(printf '%s' "$OUT" | grep -q 'final-build-pass' && echo yes || echo no)"
check "sweep all runners still ran"   "go build ./...;go test ./...;npm test;uv run pytest;cargo test" "$(calls)"
check "sweep ci gate skipped"         "no" "$(printf '%s' "$OUT" | grep -q 'ci gate ran' && echo yes || echo no)"
reset_rc

# 4. `go build` failure aborts immediately (unguarded under set -e): nothing
#    after it runs.
set_rc go build 2
run
check "build fail exit nonzero"       "nonzero" "$([ "$RC" -ne 0 ] && echo nonzero || echo zero)"
check "build fail stops sweep"        "go build ./..." "$(calls)"
reset_rc

# 5. A runner that exits 2 is still just a failure (no special meaning here).
set_rc cargo test 2
run
check "cargo rc2 exit 1"              "1" "$RC"
reset_rc

# 6. CI gate rc=1 (make target failed) and rc=2 (make missing) BOTH fail the
#    node — FinalBuild has no fix loop, so there is no escalate distinction.
set_ci 1
run
check "ci rc1 exit 1"                 "1" "$RC"
check "ci rc1 no marker"              "no" "$(printf '%s' "$OUT" | grep -q 'final-build-pass' && echo yes || echo no)"
set_ci 2
run
check "ci rc2 exit 2"                 "2" "$RC"
check "ci rc2 no marker"              "no" "$(printf '%s' "$OUT" | grep -q 'final-build-pass' && echo yes || echo no)"
set_ci 0

# 7. Missing ci-probe.sh (Setup did not run): sourcing fails loudly.
rm -f "$WORK/.ai/build/ci-probe.sh"
run
check "missing probe exit nonzero"    "nonzero" "$([ "$RC" -ne 0 ] && echo nonzero || echo zero)"
check "missing probe no marker"       "no" "$(printf '%s' "$OUT" | grep -q 'final-build-pass' && echo yes || echo no)"

if [ "$fail" = 0 ]; then echo "ALL PASS"; else echo "SOME FAILED"; exit 1; fi
