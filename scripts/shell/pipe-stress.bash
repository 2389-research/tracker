# ABOUTME: BASH_ENV preamble that turns the probabilistic SIGPIPE flake of #658
# ABOUTME: into a deterministic failure so fixture suites cannot pipe into grep -q.
#
# Root cause (Linux bash 5.2, reproduced under strace): the builtin printf
# flushes at every newline, so `printf '%s' "$OUT" | grep -q NEEDLE` is a
# producer that writes one line at a time. grep -q exits on its FIRST match,
# the producer's next write(2) hits a closed pipe (SIGPIPE, exit 141), and
# `set -o pipefail` reports 141 for the whole pipeline — so an assertion
# helper such as `has() { printf '%s' "$OUT" | grep -qF -- "$1" && echo yes
# || echo no; }` answers "no" for text that IS present. Below the pipe
# capacity the race is load dependent (5/720 suite runs at 12-way parallelism
# in the triage); here it is made certain on every platform.
#
# How: redefine the printf and echo builtins as functions that emit ONE
# builtin write per line with a ~1 ms scheduler yield between lines, so an
# early-exiting consumer always closes the pipe before the next line lands.
# `printf -v VAR` is passed straight through (no stdout involved).
#
# Wiring: pipeline/example_scripts_test.go sets BASH_ENV to this file for
# every fixture suite (opt-out TRACKER_SCRIPT_PIPE_STRESS=0); `make
# test-scripts-stress` does the same from the shell. Only the suite's own
# bash reads BASH_ENV — the scripts under test run via `sh` (dippin uses
# `sh -c`), which ignores it, so the behaviour under test is unchanged.
__pipe_stress_emit() {
  # $1 is the fully formatted text; write it line by line. The here-string
  # appends one newline, so strip one trailing newline first and put it back
  # at the end — otherwise "a\nb\n" would gain a blank line.
  local __out="$1" __body __nl=0 __first=1 __line
  case "$__out" in *$'\n') __nl=1 ;; esac
  __body="${__out%$'\n'}"
  while IFS= read -r __line; do
    [ "$__first" = 1 ] || { builtin printf '\n' || return; sleep 0.001; }
    builtin printf '%s' "$__line" || return
    __first=0
  done <<<"$__body"
  [ "$__nl" = 0 ] || builtin printf '\n'
}
printf() {
  # Pass-through: `printf -v` (no stdout), and a format that spells a NUL
  # (`\0…`, `\x0…`) — a command substitution cannot carry NUL bytes, and such
  # calls write binary fixtures to files, never into an assertion pipe.
  case "${1:-}" in -v|*'\0'*|*'\x0'*) builtin printf "$@"; return ;; esac
  local __out
  __out="$(builtin printf "$@"; builtin printf x)"; __out="${__out%x}"
  __pipe_stress_emit "$__out"
}
echo() {
  local __out
  __out="$(builtin echo "$@"; builtin printf x)"; __out="${__out%x}"
  __pipe_stress_emit "$__out"
}
