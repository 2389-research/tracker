#!/usr/bin/env bash
# ABOUTME: Shared plumbing for the build_product fixture suites — stages a
# ABOUTME: script under test with the engine's ${graph.workflow_dir} interpolation
# ABOUTME: applied, and PATH shims for the toolchain the green-gate scripts call.
#
# Source AFTER the suite has set DIR (the directory holding this file, or a
# lib/ subdirectory of it) and STATE (a scratch dir):
#   . "$DIR/test_helpers.sh"          # or "$DIR/../test_helpers.sh" from lib/
#   SCRIPT="$(stage_script "$DIR/Setup.sh")"
#
# The engine expands ${graph.workflow_dir} (author-controlled; on the
# tool_command safe-key allowlist) to the workflow's directory before a
# command_file body reaches `sh`: examples/ for a disk load of the built-in,
# <workDir>/.tracker/workflow/build_product/ for an embedded run, same layout
# either way. The suites mirror that expansion textually — exactly like the
# adversarial-review suite does for ${params.*} — so the staged copy sources
# examples/scripts/build_product/lib/*.sh.

# WORKFLOW_DIR is what ${graph.workflow_dir} expands to: the examples/ dir
# (DIR is scripts/build_product or its lib/ subdirectory).
case "$DIR" in */lib) BP_DIR="$(cd "$DIR/.." && pwd)" ;; *) BP_DIR="$(cd "$DIR" && pwd)" ;; esac
WORKFLOW_DIR="$(cd "$BP_DIR/../.." && pwd)"
# shellcheck disable=SC2034  # consumed by the sourcing suites
LIB_DIR="$WORKFLOW_DIR/scripts/build_product/lib"

# stage_script SRC — write SRC to $STATE with ${graph.workflow_dir} expanded
# and print the staged path. A script that never references the variable is
# copied unchanged, so every suite can use the same call.
stage_script() {
  local out
  out="$STATE/$(basename "$1")"
  sed "s|\\\${graph.workflow_dir}|$WORKFLOW_DIR|g" "$1" > "$out"
  printf '%s' "$out"
}

# install_tool_shims — PATH shims for go/npm/uv/cargo/make/golangci-lint in
# $STATE/bin. Each shim logs its argv to $STATE/calls (space-joined, one line
# per call) AND to $STATE/argv (TAB-separated, so a test can prove a value
# with spaces reached the tool as ONE argument), prints $STATE/out-<tool>-<sub>
# when present, and exits with the code in $STATE/rc-<tool>-<sub> (default 0).
# <sub> is the first argument — except for make, where it is the LAST (the
# target; ci-probe passes `-f <Makefile>` first). Pair with set_rc / reset_rc /
# calls / argv_has.
#
# The go shim models `go list` just enough for verify.sh's scoping (#640
# D2/D3/D7/D9) to run offline: the `-e` package filter echoes its ./dir
# arguments back as import paths, the reverse-deps query prints
# $STATE/out-go-list-deps (default: none), and the has-tests query prints
# $STATE/out-go-list-tests (default `1` = tests exist). The golangci-lint
# shim additionally copies the file after a `--config` flag to
# $STATE/lint-config so a suite can inspect the generated v2 hatch config.
install_tool_shims() {
  mkdir -p "$STATE/bin"
  local tool
  for tool in go npm uv cargo make golangci-lint; do
    cat > "$STATE/bin/$tool" <<SHIM
#!/bin/sh
echo "$tool \$*" >> "$STATE/calls"
{ printf '%s' "$tool"; for a; do printf '\t%s' "\$a"; done; printf '\n'; } >> "$STATE/argv"
sub="\${1:-none}"
case "$tool" in make) for a; do sub="\$a"; done ;; esac
if [ "$tool" = go ] && [ "\${1:-}" = list ]; then
  case "\$*" in
    *TestGoFiles*) if [ -f "$STATE/out-go-list-tests" ]; then cat "$STATE/out-go-list-tests"; else echo 1; fi ;;
    *.Deps*)       [ -f "$STATE/out-go-list-deps" ] && cat "$STATE/out-go-list-deps" ;;
    *)             for a; do case "\$a" in .|./*) echo "\$a" ;; esac; done ;;
  esac
  exit 0
fi
if [ "$tool" = golangci-lint ]; then
  prev=""; for a; do [ "\$prev" = --config ] && cp "\$a" "$STATE/lint-config"; prev="\$a"; done
fi
out="$STATE/out-$tool-\$sub"
[ -f "\$out" ] && cat "\$out"
rc="$STATE/rc-$tool-\$sub"
[ -f "\$rc" ] && exit "\$(cat "\$rc")"
exit 0
SHIM
    chmod +x "$STATE/bin/$tool"
  done
}
set_rc() { echo "$3" > "$STATE/rc-$1-$2"; }
set_out() { printf '%s\n' "$3" > "$STATE/out-$1-$2"; }
reset_rc() { rm -f "$STATE"/rc-* "$STATE"/out-* "$STATE/lint-config"; }
calls() { [ -f "$STATE/calls" ] && paste -sd';' "$STATE/calls" || echo ""; }
# argv_has LINE — yes/no: was some shim invoked with EXACTLY this TAB-separated
# argv (tool first)? Write the expectation with literal tabs via $'...'.
argv_has() { [ -f "$STATE/argv" ] && grep -qxF -- "$1" "$STATE/argv" && echo yes || echo no; }
