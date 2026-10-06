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

# ─── Pipe-free assertion helpers (#658) ─────────────────────────────────────
# The suites run under `set -o pipefail`, and a producer piped into an
# early-exiting consumer is a SIGPIPE race there: bash's builtin printf
# flushes at every newline, `grep -q` exits on its FIRST match, the producer's
# next write(2) hits the closed pipe (exit 141) and pipefail turns a needle
# that IS present into "no". None of these helpers pipe into such a consumer
# (a here-string is written in full before the reader starts, so it cannot
# SIGPIPE), and `make shell-check` rejects any new line that does.
#
# Every helper prints yes/no for `check "…" yes "$(has …)"`. NEEDLE is a
# literal for contains / has / has_line (glob metacharacters * ? [ in it are
# safe — the pattern side of `case` is quoted) and an ERE for has_re, where ^
# and $ anchor per LINE exactly as `grep -E` does. The *_in forms take the
# haystack explicitly for suites that assert on "$(calls)" or a stderr file.
# Same contract as build_product/test_helpers.sh.
contains()    { case "$1" in *"$2"*) echo yes ;; *) echo no ;; esac; }  # contains HAY NEEDLE
has()         { contains "$OUT" "$1"; }                                 # has NEEDLE — anywhere in $OUT
has_line_in() { case $'\n'"$1"$'\n' in *$'\n'"$2"$'\n'*) echo yes ;; *) echo no ;; esac; }  # exact line
has_line()    { has_line_in "$OUT" "$1"; }
has_re_in()   { grep -qE -- "$2" <<<"$1" && echo yes || echo no; }      # has_re_in HAY ERE
has_re()      { has_re_in "$OUT" "$1"; }
# first_line / last_line HAY — the first / last line of HAY without `head`
# or `tail` (so a `$(printf '%s' "$OUT" | head -1)` never trips pipefail).
first_line()  { printf '%s' "${1%%$'\n'*}"; }
last_line()   { printf '%s' "${1##*$'\n'}"; }
