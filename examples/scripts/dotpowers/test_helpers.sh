#!/usr/bin/env bash
# ABOUTME: Shared plumbing for the dotpowers-family fixture suites — stages a
# ABOUTME: script under test with ${graph.workflow_dir} expanded the way the
# ABOUTME: engine does, plus PATH shims for the toolchains the gates call.
#
# Source AFTER the suite has set DIR (this directory, or its lib/) and STATE
# (a scratch dir):
#   . "$DIR/test_helpers.sh"          # or "$DIR/../test_helpers.sh" from lib/
#   SCRIPT="$(stage_script "$DIR/PickNextTask.sh")"
#
# The seven dotpowers-family workflows (dotpowers, dotpowers-auto,
# dotpowers-simple, dotpowers-simple-auto, test-kitchen, scenario-testing,
# kitchen-sink) are disk loads, so pipeline.SeedWorkflowDir seeds
# ${graph.workflow_dir} = examples/ and every copy of a wrapper script sources
# examples/scripts/dotpowers/lib/*.sh (#646). The suites mirror that expansion
# textually, exactly like build_product's test_helpers.sh.

case "$DIR" in */lib) DP_DIR="$(cd "$DIR/.." && pwd)" ;; *) DP_DIR="$(cd "$DIR" && pwd)" ;; esac
WORKFLOW_DIR="$(cd "$DP_DIR/../.." && pwd)"
# shellcheck disable=SC2034  # consumed by the sourcing suites
LIB_DIR="$WORKFLOW_DIR/scripts/dotpowers/lib"

# stage_script SRC — write SRC to $STATE with ${graph.workflow_dir} expanded
# and print the staged path.
stage_script() {
  local out
  out="$STATE/$(basename "$1")"
  sed "s|\\\${graph.workflow_dir}|$WORKFLOW_DIR|g" "$1" > "$out"
  printf '%s' "$out"
}

# install_tool_shims — PATH shims for go/npm/npx/uv/cargo in $STATE/bin.
# Each shim logs its argv to $STATE/calls (space-joined, one line per call),
# prints $STATE/out-<tool>-<sub> when present, and exits with the code in
# $STATE/rc-<tool>-<sub> (default 0). <sub> is the first argument, except
# for `uv run <tool>` where it is the tool after `run`.
install_tool_shims() {
  mkdir -p "$STATE/bin"
  local tool
  for tool in go npm npx uv cargo; do
    cat > "$STATE/bin/$tool" <<SHIM
#!/bin/sh
echo "$tool \$*" >> "$STATE/calls"
sub="\${1:-none}"
if [ "$tool" = uv ] && [ "\$sub" = run ]; then sub="\${2:-none}"; fi
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
reset_rc() { rm -f "$STATE"/rc-* "$STATE"/out-* "$STATE/calls"; }
calls() { [ -f "$STATE/calls" ] && paste -sd';' "$STATE/calls" || echo ""; }
